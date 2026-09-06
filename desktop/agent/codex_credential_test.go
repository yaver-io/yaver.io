package main

// codex_credential_test.go — tests for the Codex keep-alive, its guards, and the
// false-sign-out fix.
//
// Every test here owns its HOME (t.Setenv + t.TempDir). That is not boilerplate: a
// test in this package once called authLogout() against the real ~/.yaver and signed
// the developer out of Yaver mid-session. Anything touching a credential path owns
// its home, always.
//
// The suite is built around NEGATIVE CONTROLS — for each guard there is a test that
// reproduces the bug the guard exists to stop, so the guard has been SEEN to fail.

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// mintJWT builds an unsigned JWT whose payload carries the given exp. Signature is
// irrelevant — jwtUnverifiedExpiry deliberately does not verify (we are reading our
// own file to decide when to renew it, not making an authz decision).
func mintJWT(t *testing.T, exp time.Time) string {
	t.Helper()
	payload, err := json.Marshal(map[string]any{"exp": exp.Unix(), "iat": exp.Add(-240 * time.Hour).Unix()})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	enc := base64.RawURLEncoding.EncodeToString
	return enc([]byte(`{"alg":"none"}`)) + "." + enc(payload) + "." + enc([]byte("sig"))
}

// seedCodexHome points CODEX_HOME at a temp dir and writes an auth.json whose access
// token expires at expiry. Returns the credential path.
func seedCodexHome(t *testing.T, expiry time.Time, extra map[string]any) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("CODEX_HOME", dir)
	t.Setenv("HOME", dir)

	doc := map[string]any{
		"auth_mode":      "chatgpt",
		"OPENAI_API_KEY": nil,
		"tokens": map[string]any{
			"access_token":  mintJWT(t, expiry),
			"refresh_token": "refresh-original",
			"id_token":      mintJWT(t, expiry.Add(-239*time.Hour)),
			"account_id":    "acct-123",
		},
		"last_refresh": time.Now().Add(-72 * time.Hour).UTC().Format(time.RFC3339Nano),
	}
	for k, v := range extra {
		doc[k] = v
	}
	blob, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		t.Fatalf("marshal doc: %v", err)
	}
	path := filepath.Join(dir, "auth.json")
	if err := os.WriteFile(path, blob, 0o600); err != nil {
		t.Fatalf("write auth.json: %v", err)
	}
	return path
}

func readAuthJSON(t *testing.T, path string) map[string]any {
	t.Helper()
	blob, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var out map[string]any
	if err := json.Unmarshal(blob, &out); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return out
}

func tokenField(t *testing.T, doc map[string]any, key string) string {
	t.Helper()
	tokens, _ := doc["tokens"].(map[string]any)
	if tokens == nil {
		t.Fatalf("no tokens object")
	}
	s, _ := tokens[key].(string)
	return s
}

// ---------------------------------------------------------------------------
// Expiry oracle
// ---------------------------------------------------------------------------

func TestJWTUnverifiedExpiry(t *testing.T) {
	want := time.Now().Add(240 * time.Hour).Truncate(time.Second)
	got, ok := jwtUnverifiedExpiry(mintJWT(t, want))
	if !ok {
		t.Fatal("expected a readable exp")
	}
	if !got.Equal(want) {
		t.Fatalf("exp = %v, want %v", got, want)
	}
	// An opaque token is a legitimate future shape, not an error — and crucially
	// must NOT read as expired, or we would renew (and rotate) on a guess.
	if _, ok := jwtUnverifiedExpiry("not-a-jwt"); ok {
		t.Fatal("opaque token must not report an expiry")
	}
}

func TestCodexFreshnessVerdicts(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name                      string
		expiry                    time.Time
		wantExpired, wantNeedsRef bool
	}{
		{"fresh 10 days out", now.Add(240 * time.Hour), false, false},
		{"inside renewal window", now.Add(6 * time.Hour), false, true},
		{"already expired", now.Add(-1 * time.Hour), true, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := seedCodexHome(t, tc.expiry, nil)
			doc, err := readCodexCredentialDoc(path)
			if err != nil {
				t.Fatalf("read: %v", err)
			}
			f := codexCredentialFreshnessOf(doc, now)
			if !f.Known {
				t.Fatal("expiry should be known")
			}
			if f.Expired != tc.wantExpired {
				t.Errorf("Expired = %v, want %v", f.Expired, tc.wantExpired)
			}
			if f.NeedsRefresh != tc.wantNeedsRef {
				t.Errorf("NeedsRefresh = %v, want %v", f.NeedsRefresh, tc.wantNeedsRef)
			}
		})
	}
}

// A truncated auth.json is the OOM fingerprint. It must be named as an interrupted
// write, NOT reported as "no credentials found" — which would send the user to redo a
// login they already did, looking for a problem that isn't there.
func TestCorruptCredentialIsNamedNotMistakenForMissing(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CODEX_HOME", dir)
	t.Setenv("HOME", dir)
	path := filepath.Join(dir, "auth.json")
	if err := os.WriteFile(path, []byte(""), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := readCodexCredentialDoc(path)
	if err == nil {
		t.Fatal("expected an error for a 0-byte credential")
	}
	if err == errNoCodexCredential {
		t.Fatal("a truncated credential must not be reported as a missing one")
	}
	if !strings.Contains(err.Error(), "EMPTY") {
		t.Errorf("error should name the interrupted write, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Writing: preserve everything we do not model
// ---------------------------------------------------------------------------

func TestWriteAtomicPreservesUnknownFieldsAndPermissions(t *testing.T) {
	path := seedCodexHome(t, time.Now().Add(2*time.Hour), map[string]any{
		"some_future_field": "must survive",
		"nested_future":     map[string]any{"a": float64(1)},
	})
	doc, err := readCodexCredentialDoc(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if err := doc.applyRefreshedTokens("new-access", "new-refresh", "", time.Now()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if err := doc.writeAtomic(); err != nil {
		t.Fatalf("write: %v", err)
	}

	got := readAuthJSON(t, path)
	if got["some_future_field"] != "must survive" {
		t.Error("an unmodelled field was dropped — the file we hand back to Codex must never be poorer than the one we found")
	}
	if _, ok := got["nested_future"].(map[string]any); !ok {
		t.Error("nested unmodelled field was dropped")
	}
	if tokenField(t, got, "account_id") != "acct-123" {
		t.Error("account_id was dropped")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("credential mode = %o, want 600", perm)
	}
}

// A response that does not rotate must not blank the token we hold. Blanking it would
// destroy the lineage — the one irreversible mistake in this whole file.
func TestApplyRefreshedTokensKeepsLineageWhenServerDoesNotRotate(t *testing.T) {
	path := seedCodexHome(t, time.Now().Add(2*time.Hour), nil)
	doc, err := readCodexCredentialDoc(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := doc.applyRefreshedTokens("new-access", "", "", time.Now()); err != nil {
		t.Fatal(err)
	}
	if got := doc.refreshToken(); got != "refresh-original" {
		t.Fatalf("refresh token = %q, want the original kept", got)
	}
	// And an empty access token is refused outright rather than written.
	if err := doc.applyRefreshedTokens("", "x", "", time.Now()); err == nil {
		t.Fatal("an empty access token must be refused")
	}
}

// ---------------------------------------------------------------------------
// The exchange
// ---------------------------------------------------------------------------

func fakeTokenEndpoint(t *testing.T, status int, body string, seen *int) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if seen != nil {
			*seen++
		}
		_ = r.ParseForm()
		if got := r.Form.Get("grant_type"); got != "refresh_token" {
			t.Errorf("grant_type = %q, want refresh_token", got)
		}
		if got := r.Form.Get("client_id"); got != codexOAuthClientID {
			t.Errorf("client_id = %q, want the public codex client id", got)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	t.Setenv("YAVER_CODEX_TOKEN_URL", srv.URL)
	return srv
}

func TestRefreshRenewsAndPersists(t *testing.T) {
	path := seedCodexHome(t, time.Now().Add(2*time.Hour), map[string]any{"keep_me": "yes"})
	calls := 0
	fakeTokenEndpoint(t, http.StatusOK,
		`{"access_token":"`+mintJWT(t, time.Now().Add(240*time.Hour))+`","refresh_token":"refresh-rotated","id_token":"","expires_in":864000}`, &calls)

	res := refreshCodexCredentialIfNeeded(context.Background(), false)
	if res.Outcome != codexRefreshRenewed {
		t.Fatalf("outcome = %s (%s), want renewed", res.Outcome, res.Reason)
	}
	if calls != 1 {
		t.Fatalf("token endpoint called %d times, want 1", calls)
	}
	got := readAuthJSON(t, path)
	if tokenField(t, got, "refresh_token") != "refresh-rotated" {
		t.Error("rotated refresh token was not persisted — the next renewal would replay a consumed token")
	}
	if got["keep_me"] != "yes" {
		t.Error("unmodelled field lost during refresh")
	}
	if !res.Healthy() {
		t.Error("a renewed credential must be healthy")
	}
}

// A credential outside the renewal window must cost NOTHING: no network, no fork.
// This is the property that makes the pre-spawn hook safe to run on every turn.
func TestRefreshIsFreeWhenNotNeeded(t *testing.T) {
	seedCodexHome(t, time.Now().Add(240*time.Hour), nil)
	calls := 0
	fakeTokenEndpoint(t, http.StatusOK, `{"access_token":"x"}`, &calls)

	res := refreshCodexCredentialIfNeeded(context.Background(), false)
	if res.Outcome != codexRefreshNotNeeded {
		t.Fatalf("outcome = %s, want not_needed", res.Outcome)
	}
	if calls != 0 {
		t.Fatalf("token endpoint was called %d times for a fresh credential — the check must be free", calls)
	}
}

// invalid_grant is the lineage-lost case: the ONE outcome a human must resolve. It
// must be classified distinctly (never as a generic failure a surface would render
// "Try again" over) and it must leave the credential file untouched.
func TestRefreshInvalidGrantIsLineageLostAndLeavesFileIntact(t *testing.T) {
	path := seedCodexHome(t, time.Now().Add(2*time.Hour), nil)
	before := readAuthJSON(t, path)
	fakeTokenEndpoint(t, http.StatusBadRequest, `{"error":"invalid_grant","error_description":"token is expired or revoked"}`, nil)

	res := refreshCodexCredentialIfNeeded(context.Background(), false)
	if res.Outcome != codexRefreshLineageLost {
		t.Fatalf("outcome = %s, want lineage_lost", res.Outcome)
	}
	if res.Code != ReasonRunnerCodexRefreshLineageLost {
		t.Errorf("code = %q, want %q", res.Code, ReasonRunnerCodexRefreshLineageLost)
	}
	if !res.Reauthable {
		t.Error("lineage loss must be marked reauthable — retrying can never fix it")
	}
	if !strings.Contains(res.Reason, "copied to another machine") {
		t.Errorf("the reason must name the real cause, got: %s", res.Reason)
	}
	if res.Healthy() {
		t.Error("lineage loss must not read as healthy")
	}
	after := readAuthJSON(t, path)
	if tokenField(t, after, "refresh_token") != tokenField(t, before, "refresh_token") {
		t.Error("a failed refresh must leave the credential exactly as found")
	}
}

// Transient failures must be retryable AND must not touch the file — the existing
// token is still valid until its own expiry.
func TestRefreshTransientFailureLeavesCredentialUsable(t *testing.T) {
	path := seedCodexHome(t, time.Now().Add(2*time.Hour), nil)
	before := readAuthJSON(t, path)
	fakeTokenEndpoint(t, http.StatusInternalServerError, `{"error":"server_error"}`, nil)

	res := refreshCodexCredentialIfNeeded(context.Background(), false)
	if res.Outcome != codexRefreshFailed {
		t.Fatalf("outcome = %s, want failed", res.Outcome)
	}
	if res.Reauthable {
		t.Error("a 5xx is not a reason to send the user to a sign-in screen")
	}
	after := readAuthJSON(t, path)
	if tokenField(t, after, "refresh_token") != tokenField(t, before, "refresh_token") {
		t.Error("a transient failure must not mutate the credential")
	}
	// And the turn must still be allowed to proceed: the token has not expired.
	if got := ensureRunnerCredentialFreshForTurn(context.Background(), "codex"); !got.Healthy() {
		t.Error("a network blip must not block a turn whose credential is still valid")
	}
}

// A refresh must never be attempted against a plaintext non-loopback endpoint — that
// would be a way to walk a refresh token off the box via the environment.
func TestTokenEndpointRefusesPlaintextOffBox(t *testing.T) {
	t.Setenv("YAVER_CODEX_TOKEN_URL", "http://evil.example.com/oauth/token")
	if got := codexTokenEndpoint(); got != codexOAuthTokenURL {
		t.Fatalf("endpoint = %q, want the pinned https endpoint", got)
	}
	t.Setenv("YAVER_CODEX_TOKEN_URL", "https://proxy.example.com/oauth/token")
	if got := codexTokenEndpoint(); got != "https://proxy.example.com/oauth/token" {
		t.Fatalf("https override should be honored, got %q", got)
	}
}

// ---------------------------------------------------------------------------
// Lineage — the "signed out again" oscillation
// ---------------------------------------------------------------------------

func TestForeignCopyIsNeverRenewed(t *testing.T) {
	path := seedCodexHome(t, time.Now().Add(2*time.Hour), nil)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	writeCodexLineageMarker(path, "kivancs-mac", raw)

	calls := 0
	fakeTokenEndpoint(t, http.StatusOK, `{"access_token":"x","refresh_token":"y"}`, &calls)

	res := refreshCodexCredentialIfNeeded(context.Background(), false)
	if res.Outcome != codexRefreshImpossible {
		t.Fatalf("outcome = %s, want impossible", res.Outcome)
	}
	if res.Code != ReasonRunnerCodexCredentialIsCopy {
		t.Errorf("code = %q, want %q", res.Code, ReasonRunnerCodexCredentialIsCopy)
	}
	if calls != 0 {
		t.Fatal("renewing a copied credential would consume the SOURCE machine's token — no request may be made")
	}
	if !strings.Contains(res.Reason, "kivancs-mac") {
		t.Errorf("the reason must name the source machine, got: %s", res.Reason)
	}
}

// Once this box has signed in for itself the copy is history and it owns its lineage.
func TestDivergedCredentialIsNoLongerForeign(t *testing.T) {
	path := seedCodexHome(t, time.Now().Add(2*time.Hour), nil)
	raw, _ := os.ReadFile(path)
	writeCodexLineageMarker(path, "kivancs-mac", raw)

	// Simulate a real sign-in here: a different refresh token lands.
	doc, err := readCodexCredentialDoc(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := doc.applyRefreshedTokens(mintJWT(t, time.Now().Add(2*time.Hour)), "our-own-token", "", time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := doc.writeAtomic(); err != nil {
		t.Fatal(err)
	}

	if foreign, _ := codexCredentialIsForeignCopy(path); foreign {
		t.Fatal("a credential this box established itself must be renewable here")
	}
}

func TestMirrorConditionalSeeding(t *testing.T) {
	incoming := func(t *testing.T, refresh string, expiry time.Time) []byte {
		t.Helper()
		blob, _ := json.Marshal(map[string]any{
			"tokens": map[string]any{"access_token": mintJWT(t, expiry), "refresh_token": refresh},
		})
		return blob
	}

	t.Run("refuses to clobber a healthy different lineage", func(t *testing.T) {
		path := seedCodexHome(t, time.Now().Add(240*time.Hour), nil)
		err := guardCodexMirrorOverwrite("codex", path, incoming(t, "someone-elses", time.Now().Add(240*time.Hour)), false)
		if err == nil {
			t.Fatal("this is the oscillation: two boxes holding one rotating token invalidate each other")
		}
		if !strings.Contains(err.Error(), "device-auth") {
			t.Errorf("the refusal must name the fix, got: %v", err)
		}
	})

	t.Run("seeds freely when there is no credential", func(t *testing.T) {
		dir := t.TempDir()
		t.Setenv("CODEX_HOME", dir)
		t.Setenv("HOME", dir)
		dest := filepath.Join(dir, "auth.json")
		if err := guardCodexMirrorOverwrite("codex", dest, incoming(t, "fresh", time.Now().Add(240*time.Hour)), false); err != nil {
			t.Fatalf("bootstrapping an empty box is the whole point of mirroring: %v", err)
		}
	})

	t.Run("seeds over an expired credential", func(t *testing.T) {
		path := seedCodexHome(t, time.Now().Add(-1*time.Hour), nil)
		if err := guardCodexMirrorOverwrite("codex", path, incoming(t, "new", time.Now().Add(240*time.Hour)), false); err != nil {
			t.Fatalf("nothing worth protecting on an expired credential: %v", err)
		}
	})

	t.Run("force overrides", func(t *testing.T) {
		path := seedCodexHome(t, time.Now().Add(240*time.Hour), nil)
		if err := guardCodexMirrorOverwrite("codex", path, incoming(t, "someone-elses", time.Now().Add(240*time.Hour)), true); err != nil {
			t.Fatalf("an explicit force must be obeyed: %v", err)
		}
	})

	t.Run("same lineage is a legitimate handoff", func(t *testing.T) {
		path := seedCodexHome(t, time.Now().Add(240*time.Hour), nil)
		if err := guardCodexMirrorOverwrite("codex", path, incoming(t, "refresh-original", time.Now().Add(240*time.Hour)), false); err != nil {
			t.Fatalf("same token family should pass: %v", err)
		}
	})
}

// ---------------------------------------------------------------------------
// The false sign-out — negative control for the tail scan
// ---------------------------------------------------------------------------

// NEGATIVE CONTROL. This is the bug: a task that merely PRINTS an auth string — which
// this repository's own source does — signed a healthy runner out for 30 minutes.
// Reverting runnerAuthClassifyTail makes this test fail, which is how we know the
// guard works.
func TestOrdinaryOutputMentioningAuthDoesNotSignRunnerOut(t *testing.T) {
	// Both runners, because the classifier attributes by PHRASE, not by which
	// runner produced the output: a codex turn that quotes Claude's wording marks
	// CLAUDE signed out. Same defect, different victim — and the cross-runner path
	// is the easier one to miss.
	cases := []struct {
		name      string
		runner    string
		victim    string
		quoted    string
		checkBoth bool
	}{
		{
			name:   "codex turn quoting codex's own wording",
			runner: "codex",
			victim: "codex",
			quoted: "  strings.Contains(m, \"please run `codex login`\")\n",
		},
		{
			name:   "codex turn quoting Claude's wording marks CLAUDE",
			runner: "codex",
			victim: "claude",
			quoted: "  API Error: 401 OAuth access token has been revoked.\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ClearRunnerAuthInvalid("codex")
			ClearRunnerAuthInvalid("claude")
			t.Cleanup(func() {
				ClearRunnerAuthInvalid("codex")
				ClearRunnerAuthInvalid("claude")
			})

			// A realistic vibing turn: the runner reads this very repo, so the
			// phrase appears EARLY, then thousands of bytes of honest work follow.
			output := "reading desktop/agent/runner_auth.go\n" + tc.quoted +
				strings.Repeat("edited a file and ran the tests; everything passes.\n", 400)

			ObserveRunnerAuthFromOutput(tc.runner, output, string(TaskStatusFinished))

			if reason, rejected := runnerAuthFailureRecent(tc.victim); rejected {
				t.Fatalf("a SUCCESSFUL turn that merely quoted an auth string signed %s out: %s", tc.victim, reason)
			}
		})
	}
}

// The true positive must survive: a runner announcing its own auth death does so at
// the END, as it gives up.
func TestRunnerAnnouncingItsOwnAuthDeathIsStillCaught(t *testing.T) {
	ClearRunnerAuthInvalid("codex")
	t.Cleanup(func() { ClearRunnerAuthInvalid("codex") })

	output := strings.Repeat("working...\n", 500) +
		"\nPlease run `codex login` — your ChatGPT credential is no longer accepted.\n"

	ObserveRunnerAuthFromOutput("codex", output, string(TaskStatusFinished))

	if _, rejected := runnerAuthFailureRecent("codex"); !rejected {
		t.Fatal("a runner that says it is signed out at the end of its output must be believed")
	}
}

// ---------------------------------------------------------------------------
// Parked turns — the user's words must survive an expiry
// ---------------------------------------------------------------------------

func TestParkedTurnSurvivesAndReplaysOnce(t *testing.T) {
	tm := &TaskManager{}
	t.Cleanup(func() { dropParkedTurn("task-1") })

	if !tm.ParkPendingTurn("task-1", "keep going please", nil, TaskResumeOptions{RunnerID: "codex"}) {
		t.Fatal("a follow-up must be parkable")
	}
	got, ok := ParkedTurnFor("task-1")
	if !ok || got.Input != "keep going please" {
		t.Fatal("the user's words must be retrievable verbatim")
	}

	// Taking them is atomic: a second concurrent replay must find nothing, so one
	// prompt can never be dispatched twice.
	first := takeReplayableTurns()
	second := takeReplayableTurns()
	if len(first) != 1 {
		t.Fatalf("first take got %d turns, want 1", len(first))
	}
	if len(second) != 0 {
		t.Fatalf("second take got %d turns, want 0 — a prompt must never fire twice", len(second))
	}
}

func TestParkedTurnExpires(t *testing.T) {
	tm := &TaskManager{}
	tm.ParkPendingTurn("task-stale", "old intent", nil, TaskResumeOptions{})
	parkedTurns.Lock()
	entry := parkedTurns.byTask["task-stale"]
	entry.ParkedAt = time.Now().Add(-parkedTurnTTL - time.Minute)
	parkedTurns.byTask["task-stale"] = entry
	parkedTurns.Unlock()

	if _, ok := ParkedTurnFor("task-stale"); ok {
		t.Fatal("a two-hour-old instruction is stale intent and must not fire")
	}
}

// A box whose agent has no credential at ITS path may still run tasks under a
// different tenant home. Blocking the turn there would invent an outage.
func TestMissingCredentialAtAgentPathDoesNotBlockTheTurn(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CODEX_HOME", dir)
	t.Setenv("HOME", dir)

	got := ensureRunnerCredentialFreshForTurn(context.Background(), "codex")
	if !got.Healthy() {
		t.Fatalf("absence at the agent's own path is not proof the runner cannot work: %s / %s", got.Outcome, got.Reason)
	}
}

// A runner with no refresh lineage Yaver can drive must pass straight through.
func TestNonCodexRunnersArePassThrough(t *testing.T) {
	for _, id := range []string{"claude", "opencode", ""} {
		if got := ensureRunnerCredentialFreshForTurn(context.Background(), id); !got.Healthy() {
			t.Errorf("runner %q must not be gated by the codex keep-alive", id)
		}
	}
}

// ---------------------------------------------------------------------------
// Structured signal on the STATUS channel
// ---------------------------------------------------------------------------

// A dead credential must be reported with a CODE, not only a sentence. Without
// this, every surface that wants to branch on "expired" vs "never signed in" is
// back to regexing prose — the drift that gave mobile three relay-auth matchers.
func TestExpiredCredentialCarriesStructuredCode(t *testing.T) {
	stubCodexLinuxSandboxPrereq(t, "")
	ClearRunnerAuthInvalid("codex")
	ClearRunnerAuthProven("codex")
	t.Cleanup(func() { ClearRunnerAuthInvalid("codex") })
	seedCodexHome(t, time.Now().Add(-2*time.Hour), nil)

	st := DetectRunnerRuntimeStatus(RunnerConfig{RunnerID: "codex"}, t.TempDir())
	if st.Code != ReasonRunnerCodexCredentialExpired {
		t.Fatalf("Code = %q, want %q (warning was: %s)", st.Code, ReasonRunnerCodexCredentialExpired, st.Warning)
	}
	if st.Ready {
		t.Error("an expired credential must not report Ready")
	}
	// The prose must still name the headless fix — the code is for machines,
	// the sentence is for the human.
	if !strings.Contains(st.Warning, "--device-auth") {
		t.Errorf("warning must name the headless sign-in, got: %s", st.Warning)
	}
}

// NEGATIVE CONTROL for a defect that shipped: runnerCapabilityReason decided
// "is codex blocked?" by substring-matching status.Error for "not
// authenticated", while detectCodexStatus writes "no credentials were found".
// Those never matched, so a runner with NO CREDENTIAL AT ALL reported itself as
// not blocked — a prose matcher failing silently, which looks exactly like
// nothing being wrong.
//
// Revert runnerCapabilityReason to the regex and this fails.
func TestCapabilityReasonKeysOffCodeNotProse(t *testing.T) {
	status := RunnerRuntimeStatus{
		// Deliberately the REAL sentence, which contains no "not authenticated".
		Error: "Codex is installed but no credentials were found. Run `codex login --device-auth` ...",
		Code:  ReasonRunnerCodexNotAuthenticated,
	}
	code, reason, action, blocked := runnerCapabilityReason("codex", status)
	if !blocked {
		t.Fatal("a codex runner with no credential must report as blocked")
	}
	if code != ReasonRunnerCodexNotAuthenticated {
		t.Errorf("code = %q, want %q", code, ReasonRunnerCodexNotAuthenticated)
	}
	if !strings.Contains(action, "--device-auth") {
		t.Errorf("the action must be one a headless box can perform, got: %s", action)
	}
	if strings.TrimSpace(reason) == "" {
		t.Error("a blocked capability must carry a human sentence too")
	}

	// And the new codex states must each be blocking with their own sentence.
	for _, c := range []string{
		ReasonRunnerCodexCredentialExpired,
		ReasonRunnerCodexCredentialCorrupt,
		ReasonRunnerCodexCredentialIsCopy,
	} {
		gotCode, gotReason, gotAction, gotBlocked := runnerCapabilityReason("codex", RunnerRuntimeStatus{Code: c})
		if !gotBlocked || gotCode != c {
			t.Errorf("%s: blocked=%v code=%q", c, gotBlocked, gotCode)
		}
		if !strings.Contains(gotAction, "--device-auth") {
			t.Errorf("%s: action must work on a headless box, got %q", c, gotAction)
		}
		if strings.TrimSpace(gotReason) == "" {
			t.Errorf("%s: missing human sentence", c)
		}
	}
}

// An empty follow-up is not work worth keeping.
func TestParkRejectsEmptyInput(t *testing.T) {
	tm := &TaskManager{}
	if tm.ParkPendingTurn("task-2", "   ", nil, TaskResumeOptions{}) {
		t.Fatal("an empty prompt must not be parked — the caller would then promise the user something it did not keep")
	}
}

var _ = fmt.Sprintf
