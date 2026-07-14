package main

// ops_play_submit.go — Google Play *submission* verbs for the `ops` MCP
// grand-tool. The Apple mirror is ops_store_submit.go; this file is its Play
// counterpart and deliberately matches its shape (opsVerbSpec + init(),
// per-project vault creds, gates that refuse to lie, verbatim upstream errors).
//
// Multi-tenant like every other store_* verb: each verb takes an optional
// `project` and resolves that project's Play service account from its vault
// scope via newPlayClient(project, pkg) → resolveGoogleSA(project) +
// getGoogleAccessToken(...). A managed-cloud box publishes dev B's app with
// dev B's service account, never Yaver's. Auth is NOT reinvented here.
//
// ─────────────────────────────────────────────────────────────────────────────
// THE STRUCTURAL DIFFERENCE FROM APPLE: PLAY IS TRANSACTIONAL.
//
// Apple mutates resources in place. Play does not: every change to a listing,
// image, bundle or track happens inside an *edit* — a server-side transaction —
// and is invisible until the edit is committed:
//
//	POST   /applications/{pkg}/edits              → {id}
//	…      mutate listings / images / bundles / tracks within {id}
//	POST   /applications/{pkg}/edits/{id}:validate
//	POST   /applications/{pkg}/edits/{id}:commit  → changes go live
//	DELETE /applications/{pkg}/edits/{id}          → discard
//
// An edit holds a lock on the app: while one is open, a *concurrent* edit that
// touches the same resources will be rejected at commit time with
// `editAlreadyCommitted`/conflict errors. A leaked edit is therefore not
// harmless garbage — it is a landmine for the next run. So every verb here
// runs its edit through playEditTx, whose abort() is `defer`-ed
// unconditionally and becomes a no-op only after a successful commit. Any
// error, any early return, any panic → the edit is DELETEd.
//
// COMMIT SEMANTICS ARE THE SUBMISSION. There is no separate "submit" call on
// Play: committing an edit with `changesNotSentForReview=false` IS sending the
// app to review. That is a one-way door, so:
//
//   - every NON-submit verb commits with changesNotSentForReview=TRUE. A
//     play_listing_set must never quietly ship the app to review.
//   - only play_submit_for_review commits with changesNotSentForReview=false.
//
// ─────────────────────────────────────────────────────────────────────────────
// CONSOLE-ONLY GATES. Verified against the Android Publisher v3 REST surface
// (developers.google.com/android-publisher/api-ref/rest), not assumed:
//
//  1. CONTENT RATING (IARC questionnaire). There is NO resource for it in v3 —
//     not under edits.*, not at application level. It can neither be set nor
//     read over the API. Play blocks a production release until it is done.
//
//  2. APP CONTENT declarations (target audience & content, ads, news apps,
//     government apps, financial features, …). Same: no v3 resource exists.
//
//  3. FIRST RELEASE OF A NEVER-PUBLISHED ("draft") APP. The API accepts only
//     `draft`-status releases on a draft app; rolling the first one out must be
//     done in the Console. Google reports this at commit time with
//     reason=`rolloutNotPermittedOnDraftApp` ("Only releases with status draft
//     may be created on draft app."). We detect that reason and attach the
//     Console path rather than leaving the operator staring at a raw 400.
//
//  4. PER-EMAIL CLOSED-TESTING TESTERS. edits.testers manages `googleGroups`
//     only — there is no per-email list in the API. The repo already knows this
//     (ops_store.go / CLAUDE.md); it is restated here so play_submit_status is
//     self-contained. Advisory: it does not block a submission.
//
// NOT a Console-only gate, and deliberately NOT claimed as one: DATA SAFETY.
// An API *does* exist — POST /applications/{pkg}/dataSafety, which takes the
// contents of the Data-safety CSV. These verbs do not drive it (it lives
// outside the edits transaction and needs the CSV), and it is WRITE-ONLY, so we
// cannot read back whether it has been filled in. It is reported under
// `notAutomated` with its real API path — not smuggled into consoleOnly, and
// never reported as done.
//
// Google's error bodies are surfaced VERBATIM (playAPIError → googleError in
// the result). They are never collapsed into "failed".
// ─────────────────────────────────────────────────────────────────────────────

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// playUploadBase is the media-upload host prefix. Google serves the upload
// variant of edits.bundles.upload / edits.images.upload from a DIFFERENT path
// (`/upload/androidpublisher/v3`) than the JSON API. Overridable in tests.
var playUploadBase = "https://androidpublisher.googleapis.com/upload/androidpublisher/v3"

// playUploadClient carries the .aab / image bytes. Separate from playClient.http
// (45s) because an app bundle is routinely 100+ MB and Google's own docs
// recommend raising the timeout for edits.bundles.upload.
var playUploadClient = &http.Client{Timeout: 15 * time.Minute}

// newPlayClientFn is the seam the submission verbs resolve their client
// through. Production points at newPlayClient (per-project vault creds); tests
// point it at a client aimed at a real httptest Play server, so the handlers —
// edit transaction and Console gates included — are exercised end to end
// without a vault.
var newPlayClientFn = newPlayClient

// ───────────────────── verbatim Google errors ──────────────────────

// playAPIErrorDetail is one entry of the googleapi `error.errors[]` array. The
// `reason` is the machine-readable code that tells us WHICH gate we hit
// (e.g. rolloutNotPermittedOnDraftApp).
type playAPIErrorDetail struct {
	Domain   string `json:"domain,omitempty"`
	Reason   string `json:"reason,omitempty"`
	Message  string `json:"message,omitempty"`
	Location string `json:"location,omitempty"`
}

func (d playAPIErrorDetail) String() string {
	s := d.Reason
	if s == "" {
		s = d.Domain
	}
	if d.Message != "" {
		if s != "" {
			s += ": "
		}
		s += d.Message
	}
	if d.Location != "" {
		s += "  [" + d.Location + "]"
	}
	return s
}

// playAPIError is Google's standard error envelope:
//
//	{"error":{"code":400,"message":"…","status":"INVALID_ARGUMENT",
//	          "errors":[{"domain":"…","reason":"…","message":"…"}]}}
//
// This is the ONLY place Google says precisely what is wrong. Kept whole.
type playAPIError struct {
	Code    int                  `json:"code,omitempty"`
	Message string               `json:"message,omitempty"`
	Status  string               `json:"status,omitempty"`
	Errors  []playAPIErrorDetail `json:"errors,omitempty"`
}

// parsePlayAPIError pulls Google's error envelope out of a response body.
// Returns nil when the body is not a googleapi error (e.g. an HTML 502 from a
// proxy) — callers then fall back to the raw body, which is still surfaced.
func parsePlayAPIError(body []byte) *playAPIError {
	var env struct {
		Error *playAPIError `json:"error"`
	}
	if json.Unmarshal(body, &env) != nil || env.Error == nil {
		return nil
	}
	if env.Error.Message == "" && len(env.Error.Errors) == 0 {
		return nil
	}
	return env.Error
}

// reasons returns every machine-readable reason code Google attached.
func (e *playAPIError) reasons() []string {
	if e == nil {
		return nil
	}
	out := make([]string, 0, len(e.Errors))
	for _, d := range e.Errors {
		if d.Reason != "" {
			out = append(out, d.Reason)
		}
	}
	return out
}

// haystack is every word Google gave us, lowercased — used only to recognise a
// known gate. The ORIGINAL text is what we report.
func (e *playAPIError) haystack() string {
	if e == nil {
		return ""
	}
	parts := []string{e.Message, e.Status}
	for _, d := range e.Errors {
		parts = append(parts, d.Reason, d.Message)
	}
	return strings.ToLower(strings.Join(parts, " "))
}

// playFailure turns a failed Play call into an OpsResult that keeps Google's
// own words. The raw body is always available (doJSON returns it even on
// error), so nothing Google said is lost.
func playFailure(what string, body []byte, err error) OpsResult {
	msg := what + ": " + err.Error()
	gerr := parsePlayAPIError(body)
	res := OpsResult{OK: false, Code: "google_error"}
	if gerr != nil {
		head := "  " + gerr.Message
		// Google's canonical status (PERMISSION_DENIED, INVALID_ARGUMENT, …) is
		// the part an operator greps for. Keep it.
		if gerr.Status != "" {
			head += " [" + gerr.Status + "]"
		}
		lines := []string{msg, "Google reported (verbatim):", head}
		for _, d := range gerr.Errors {
			lines = append(lines, "  • "+d.String())
		}
		res.Code = "google_rejected"
		res.Error = strings.Join(lines, "\n")
		res.Initial = map[string]interface{}{"googleError": gerr}
		return res
	}
	// Not a googleapi envelope — still hand back what came off the wire rather
	// than swallowing it.
	if snippet := strings.TrimSpace(string(body)); snippet != "" {
		msg += "\nResponse body (verbatim):\n  " + snippet
	}
	res.Error = msg
	return res
}

// ───────────────────────── Console-only gates ──────────────────────────

// playGate is a step a human must perform in the Play Console because Google
// ships no API for it — or, when APIPath is set, a step that HAS an API which
// these verbs deliberately do not drive. Never silently "handled".
type playGate struct {
	Gate        string `json:"gate"`
	Reason      string `json:"reason"`
	ConsolePath string `json:"consolePath"`
	// Verifiable reports whether the agent can even CHECK this over the API.
	// Content rating and App content cannot be read back at all → false.
	Verifiable bool `json:"verifiable"`
	// APIPath is non-empty when Google DOES expose an endpoint for this and we
	// simply do not automate it here. Such a gate is reported under
	// `notAutomated`, never under `consoleOnly` — calling it Console-only when
	// an API exists would be a lie in the other direction.
	APIPath string `json:"apiPath,omitempty"`
}

func gateContentRating() playGate {
	return playGate{
		Gate:        "content_rating",
		Reason:      "The IARC content-rating questionnaire has NO resource in Android Publisher v3 — not under edits.*, not at application level. It can neither be set nor read over the API, so this agent cannot verify or perform it. Play blocks a production release until it is complete.",
		ConsolePath: "Play Console → your app → Monetize/Policy → App content → Content ratings → complete the IARC questionnaire.",
		Verifiable:  false,
	}
}

func gateAppContent() playGate {
	return playGate{
		Gate:        "app_content_declarations",
		Reason:      "The App content declarations (target audience & content, ads, news apps, government apps, financial features, health, …) have NO resource in Android Publisher v3. They can neither be set nor read over the API. Play blocks a release until the required ones are declared.",
		ConsolePath: "Play Console → your app → Policy → App content → complete each required declaration.",
		Verifiable:  false,
	}
}

// gateFirstRelease is Google's draft-app rule. Discoverable ONLY at commit
// time: the edits API happily accepts everything, then the commit fails with
// reason=rolloutNotPermittedOnDraftApp. Exactly how Apple hides the motion
// label behind meta.associatedErrors.
func gateFirstRelease(pkg string) playGate {
	return playGate{
		Gate:        "first_release_on_draft_app",
		Reason:      "This app has never been published, so Google treats it as a DRAFT app. The API will only accept releases with status `draft` on a draft app — the FIRST rollout must be performed by a human in the Play Console. Google reports this at commit time as reason=rolloutNotPermittedOnDraftApp (\"Only releases with status draft may be created on draft app.\"). It cannot be read ahead of time: no v3 endpoint exposes whether an app is still a draft.",
		ConsolePath: "Play Console → " + pkg + " → Test and release → select the track → review the release the API prepared → Start rollout. After that first human rollout, every verb here works unattended.",
		Verifiable:  false,
	}
}

// gateChangesReviewFromConsole covers apps Google refuses to auto-send for
// review. Google's own remedy is literally in the error text ("Please set the
// query parameter changesNotSentForReview to true"), which means the commit can
// land but the SUBMISSION must be pushed from the Console.
func gateChangesReviewFromConsole(pkg string) playGate {
	return playGate{
		Gate:        "send_changes_for_review",
		Reason:      "Google refused to send this edit for review automatically and asked for changesNotSentForReview=true. The changes can be committed, but the act of sending them to review must then be done by a human in the Console. Re-run with changesNotSentForReview:true to land the changes, then submit from the Console.",
		ConsolePath: "Play Console → " + pkg + " → Publishing overview → Send changes for review.",
		Verifiable:  false,
	}
}

func gatePerEmailTesters() playGate {
	return playGate{
		Gate:        "per_email_testers",
		Reason:      "edits.testers manages `googleGroups` ONLY — Android Publisher v3 has no per-email tester list. Individual testers must be added in the Console, or (automatable) added to a Google Group that is bound to the track with store_tester_invite. Advisory: this does NOT block a submission.",
		ConsolePath: "Play Console → " + "your app" + " → Test and release → Testing → Closed/Internal testing → Testers → add emails.",
		Verifiable:  true,
	}
}

// gateDataSafety is NOT Console-only — an API exists. Reported under
// notAutomated so the operator knows (a) it is required, (b) we did not do it,
// and (c) it is write-only so we cannot even check it.
func gateDataSafety(pkg string) playGate {
	return playGate{
		Gate:        "data_safety",
		Reason:      "The Data safety form is REQUIRED by Play. An API DOES exist — POST /applications/" + pkg + "/dataSafety, which takes the contents of the Data-safety CSV — but these verbs do not drive it (it lives outside the edits transaction and needs the CSV). It is also WRITE-ONLY: nothing in v3 reads the declaration back, so this agent cannot tell you whether it has been filled in. Not Console-only; simply not automated here.",
		ConsolePath: "Play Console → " + pkg + " → Policy → App content → Data safety (or export/import the CSV and POST it to the dataSafety endpoint).",
		Verifiable:  false,
		APIPath:     "POST /androidpublisher/v3/applications/" + pkg + "/dataSafety",
	}
}

// playDraftAppBlocked reports whether Google's error is the draft-app gate.
func playDraftAppBlocked(e *playAPIError) bool {
	for _, r := range e.reasons() {
		if strings.EqualFold(r, "rolloutNotPermittedOnDraftApp") {
			return true
		}
	}
	h := e.haystack()
	return strings.Contains(h, "draft app") ||
		strings.Contains(h, "only releases with status draft")
}

// playSendForReviewBlocked reports whether Google is demanding
// changesNotSentForReview=true.
func playSendForReviewBlocked(e *playAPIError) bool {
	return strings.Contains(e.haystack(), "changesnotsentforreview")
}

// ─────────────────────── raw Play transport (status + body) ───────────────────

// doJSON is doStatus for JSON calls. Unlike playClient.do (which snips the body
// into the error string), it returns the FULL body alongside the error so
// playFailure can surface Google's envelope verbatim.
func (p *playClient) doJSON(method, path string, body interface{}) ([]byte, int, error) {
	var rdr io.Reader
	if body != nil {
		bb, err := json.Marshal(body)
		if err != nil {
			return nil, 0, err
		}
		rdr = bytes.NewReader(bb)
	}
	u := path
	if !strings.HasPrefix(path, "http") {
		u = playAPIBase + path
	}
	req, err := http.NewRequest(method, u, rdr)
	if err != nil {
		return nil, 0, err
	}
	// Never logged, never echoed back in any OpsResult.
	req.Header.Set("Authorization", "Bearer "+p.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := p.http.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return out, resp.StatusCode, fmt.Errorf("play %s %s: HTTP %d", method, path, resp.StatusCode)
	}
	return out, resp.StatusCode, nil
}

// uploadMedia performs a `uploadType=media` POST against the /upload/ host with
// raw bytes. Used by both edits.bundles.upload and edits.images.upload.
func (p *playClient) uploadMedia(path, contentType string, data []byte) ([]byte, int, error) {
	u := playUploadBase + path + "?uploadType=media"
	req, err := http.NewRequest("POST", u, bytes.NewReader(data))
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+p.token)
	req.Header.Set("Content-Type", contentType)
	req.ContentLength = int64(len(data))
	resp, err := playUploadClient.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return out, resp.StatusCode, fmt.Errorf("play upload %s: HTTP %d", path, resp.StatusCode)
	}
	return out, resp.StatusCode, nil
}

func (p *playClient) editPath(editID string) string {
	return "/applications/" + p.pkg + "/edits/" + editID
}

// ────────────────────────── the edit transaction ──────────────────────────

// playEditTx is one Play edit, modelled as what it actually is: a transaction.
//
// Lifecycle contract — this is the whole point of the type:
//
//	tx, res := beginPlayEdit(cl, args)   // opens, or adopts a caller's editId
//	if res != nil { return *res }
//	defer tx.abort()                     // ALWAYS. no-op after commit/adopted.
//	… mutate …
//	if res := tx.finish(sendForReview); res != nil { return *res }
//
// abort() is deliberately unconditional at the defer site so that no error
// path, early return or panic can leak an edit. It turns itself off once the
// edit has been committed (nothing to discard) or when the edit belongs to the
// caller (they passed editId and will commit it themselves).
type playEditTx struct {
	cl *playClient
	id string
	// owned is false when the caller supplied editId: we must not commit it and
	// must not delete it — it is theirs to finish.
	owned bool
	// settled is set once the edit is committed or deleted; abort() then no-ops.
	settled bool
	// expiry is Google's expiryTimeSeconds, surfaced so an operator holding an
	// edit across verbs knows when it dies.
	expiry string
}

// beginPlayEdit opens a fresh edit, or adopts the caller's `editId`.
func beginPlayEdit(cl *playClient, editID string) (*playEditTx, *OpsResult) {
	if id := strings.TrimSpace(editID); id != "" {
		// Adopted: the caller opened it and owns its commit. We never delete it
		// on failure either — discarding someone else's transaction would throw
		// away work they have not committed yet.
		return &playEditTx{cl: cl, id: id, owned: false}, nil
	}
	body, _, err := cl.doJSON("POST", "/applications/"+cl.pkg+"/edits", map[string]interface{}{})
	if err != nil {
		res := playFailure("open play edit", body, err)
		return nil, &res
	}
	var r struct {
		ID                string `json:"id"`
		ExpiryTimeSeconds string `json:"expiryTimeSeconds"`
	}
	if err := json.Unmarshal(body, &r); err != nil || r.ID == "" {
		return nil, &OpsResult{OK: false, Code: "google_error",
			Error: "open play edit: Google returned no edit id. Body (verbatim):\n  " + strings.TrimSpace(string(body))}
	}
	return &playEditTx{cl: cl, id: r.ID, owned: true, expiry: r.ExpiryTimeSeconds}, nil
}

// abort discards the edit. Safe to call twice; no-op once settled, and never
// touches an edit the caller owns. Best-effort by design: the verb's real error
// must survive, so a failure to clean up is not allowed to mask it. A leaked
// edit expires on Google's side anyway — but we do not rely on that.
func (tx *playEditTx) abort() {
	if tx == nil || tx.settled || !tx.owned {
		return
	}
	tx.settled = true
	_, _, _ = tx.cl.doJSON("DELETE", tx.cl.editPath(tx.id), nil)
}

// validate runs Google's dry-run over the staged changes.
func (tx *playEditTx) validate() ([]byte, error) {
	body, _, err := tx.cl.doJSON("POST", tx.cl.editPath(tx.id)+":validate", nil)
	return body, err
}

// commit makes the staged changes real. sendForReview=false ⇒
// changesNotSentForReview=true, i.e. land the changes but do NOT submit.
func (tx *playEditTx) commit(sendForReview bool) ([]byte, error) {
	path := tx.cl.editPath(tx.id) + ":commit"
	if !sendForReview {
		path += "?changesNotSentForReview=true"
	}
	body, _, err := tx.cl.doJSON("POST", path, nil)
	if err == nil {
		tx.settled = true // committed: there is nothing left to discard
	}
	return body, err
}

// finish is validate → commit for a verb that opened its own edit. When the
// edit was adopted (caller-owned) it does nothing: the caller commits.
//
// Returns nil on success, or the OpsResult to hand straight back on failure —
// with the Console gate attached when Google named one.
func (tx *playEditTx) finish(sendForReview bool) *OpsResult {
	if !tx.owned {
		return nil // caller's transaction; they validate + commit it
	}
	if body, err := tx.validate(); err != nil {
		res := tx.gated("validate play edit "+tx.id, body, err)
		return &res
	}
	body, err := tx.commit(sendForReview)
	if err != nil {
		res := tx.gated("commit play edit "+tx.id, body, err)
		return &res
	}
	return nil
}

// gated surfaces Google's error verbatim AND, when the error is one of the
// known Console-only gates, names it with the exact Console path instead of
// leaving the operator holding a raw 400.
func (tx *playEditTx) gated(what string, body []byte, err error) OpsResult {
	res := playFailure(what, body, err)
	gerr := parsePlayAPIError(body)
	if gerr == nil {
		return res
	}
	var gate *playGate
	switch {
	case playDraftAppBlocked(gerr):
		g := gateFirstRelease(tx.cl.pkg)
		gate = &g
	case playSendForReviewBlocked(gerr):
		g := gateChangesReviewFromConsole(tx.cl.pkg)
		gate = &g
	}
	if gate == nil {
		return res
	}
	res.Code = "console_only"
	res.Error += "\n\nThis is a Console-only step — Google exposes NO API for it.\n" +
		gate.Reason + "\n\nDo this in the Console: " + gate.ConsolePath
	res.Initial = map[string]interface{}{
		"googleError": gerr,
		"consoleOnly": []playGate{*gate},
		"editId":      tx.id,
	}
	return res
}

// ───────────────────────────── typed Play resources ─────────────────────────

// PlayListing is one locale's store listing (edits.listings).
type PlayListing struct {
	Language         string `json:"language,omitempty"`
	Title            string `json:"title,omitempty"`
	ShortDescription string `json:"shortDescription,omitempty"`
	FullDescription  string `json:"fullDescription,omitempty"`
	Video            string `json:"video,omitempty"`
}

// PlayImage is one uploaded graphic asset.
type PlayImage struct {
	ID     string `json:"id,omitempty"`
	URL    string `json:"url,omitempty"`
	Sha256 string `json:"sha256,omitempty"`
}

// PlayBundle is one uploaded .aab.
type PlayBundle struct {
	VersionCode int64  `json:"versionCode,omitempty"`
	Sha256      string `json:"sha256,omitempty"`
}

// PlayReleaseNote is one locale's "what's new".
type PlayReleaseNote struct {
	Language string `json:"language,omitempty"`
	Text     string `json:"text,omitempty"`
}

// PlayTrackRelease is a release on a track. Mirrors Google's TrackRelease.
type PlayTrackRelease struct {
	Name         string            `json:"name,omitempty"`
	VersionCodes []string          `json:"versionCodes,omitempty"`
	Status       string            `json:"status,omitempty"` // draft|inProgress|halted|completed
	UserFraction float64           `json:"userFraction,omitempty"`
	ReleaseNotes []PlayReleaseNote `json:"releaseNotes,omitempty"`
}

// PlayTrackFull is a track and its releases.
type PlayTrackFull struct {
	Track    string             `json:"track"`
	Releases []PlayTrackRelease `json:"releases,omitempty"`
}

// PlayAppDetails is edits.details (default language + contact info).
type PlayAppDetails struct {
	DefaultLanguage string `json:"defaultLanguage,omitempty"`
	ContactEmail    string `json:"contactEmail,omitempty"`
	ContactWebsite  string `json:"contactWebsite,omitempty"`
	ContactPhone    string `json:"contactPhone,omitempty"`
}

// playImageTypes is the AppImageType enum, verified against
// developers.google.com/android-publisher/api-ref/rest/v3/AppImageType.
var playImageTypes = map[string]string{
	"icon":                 "App icon — 512x512 PNG (REQUIRED by Play)",
	"featureGraphic":       "Feature graphic — 1024x500 (REQUIRED by Play)",
	"phoneScreenshots":     "Phone screenshots — at least 2 REQUIRED by Play",
	"sevenInchScreenshots": "7-inch tablet screenshots",
	"tenInchScreenshots":   "10-inch tablet screenshots",
	"tvScreenshots":        "Android TV screenshots",
	"wearScreenshots":      "Wear OS screenshots",
	"tvBanner":             "Android TV banner — 1280x720",
}

// playTracks are the four review-relevant tracks.
var playTracks = map[string]bool{
	"internal": true, "alpha": true, "beta": true, "production": true,
}

// playReleaseStatuses are Google's TrackRelease.status values.
var playReleaseStatuses = map[string]bool{
	"draft": true, "inProgress": true, "halted": true, "completed": true,
}

// GetDetails reads the app-level details inside an edit.
func (tx *playEditTx) GetDetails() (*PlayAppDetails, []byte, error) {
	body, _, err := tx.cl.doJSON("GET", tx.cl.editPath(tx.id)+"/details", nil)
	if err != nil {
		return nil, body, err
	}
	var d PlayAppDetails
	if err := json.Unmarshal(body, &d); err != nil {
		return nil, body, err
	}
	return &d, body, nil
}

// Listings lists every locale's listing inside an edit.
func (tx *playEditTx) Listings() ([]PlayListing, []byte, error) {
	body, _, err := tx.cl.doJSON("GET", tx.cl.editPath(tx.id)+"/listings", nil)
	if err != nil {
		return nil, body, err
	}
	var r struct {
		Listings []PlayListing `json:"listings"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, body, err
	}
	return r.Listings, body, nil
}

// SetListing PATCHes one locale's listing (only the fields supplied).
func (tx *playEditTx) SetListing(lang string, fields map[string]interface{}) (*PlayListing, []byte, error) {
	body, _, err := tx.cl.doJSON("PATCH", tx.cl.editPath(tx.id)+"/listings/"+lang, fields)
	if err != nil {
		return nil, body, err
	}
	var l PlayListing
	if err := json.Unmarshal(body, &l); err != nil {
		return nil, body, err
	}
	if l.Language == "" {
		l.Language = lang
	}
	return &l, body, nil
}

// imagesPath is the REAL upload/list path for a graphic. NOTE: the resource is
// called edits.images, but the URL is `/listings/{lang}/{imageType}` — NOT
// `/images/{lang}/{imageType}`. Verified against
// developers.google.com/android-publisher/api-ref/rest/v3/edits.images/upload.
func (tx *playEditTx) imagesPath(lang, imageType string) string {
	return tx.cl.editPath(tx.id) + "/listings/" + lang + "/" + imageType
}

// Images lists the graphics currently in one bucket.
func (tx *playEditTx) Images(lang, imageType string) ([]PlayImage, []byte, error) {
	body, _, err := tx.cl.doJSON("GET", tx.imagesPath(lang, imageType), nil)
	if err != nil {
		return nil, body, err
	}
	var r struct {
		Images []PlayImage `json:"images"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, body, err
	}
	return r.Images, body, nil
}

// DeleteAllImages is edits.images.deleteall for one bucket (the `replace` path).
func (tx *playEditTx) DeleteAllImages(lang, imageType string) ([]PlayImage, []byte, error) {
	body, _, err := tx.cl.doJSON("DELETE", tx.imagesPath(lang, imageType), nil)
	if err != nil {
		return nil, body, err
	}
	var r struct {
		Deleted []PlayImage `json:"deleted"`
	}
	_ = json.Unmarshal(body, &r)
	return r.Deleted, body, nil
}

// UploadImage is edits.images.upload — raw bytes to the /upload/ host.
func (tx *playEditTx) UploadImage(lang, imageType, path string) (*PlayImage, []byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	if len(data) == 0 {
		return nil, nil, fmt.Errorf("%s is empty", path)
	}
	ct := "image/png"
	if ext := strings.ToLower(filepath.Ext(path)); ext == ".jpg" || ext == ".jpeg" {
		ct = "image/jpeg"
	}
	body, _, err := tx.cl.uploadMedia(tx.imagesPath(lang, imageType), ct, data)
	if err != nil {
		return nil, body, err
	}
	var r struct {
		Image PlayImage `json:"image"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, body, err
	}
	return &r.Image, body, nil
}

// Bundles lists the .aabs Google holds for the app.
func (tx *playEditTx) Bundles() ([]PlayBundle, []byte, error) {
	body, _, err := tx.cl.doJSON("GET", tx.cl.editPath(tx.id)+"/bundles", nil)
	if err != nil {
		return nil, body, err
	}
	var r struct {
		Bundles []PlayBundle `json:"bundles"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, body, err
	}
	return r.Bundles, body, nil
}

// UploadBundle is edits.bundles.upload — the .aab bytes to the /upload/ host.
func (tx *playEditTx) UploadBundle(path string) (*PlayBundle, []byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	if len(data) == 0 {
		return nil, nil, fmt.Errorf("%s is empty", path)
	}
	body, _, err := tx.cl.uploadMedia(tx.cl.editPath(tx.id)+"/bundles", "application/octet-stream", data)
	if err != nil {
		return nil, body, err
	}
	var b PlayBundle
	if err := json.Unmarshal(body, &b); err != nil {
		return nil, body, err
	}
	return &b, body, nil
}

// Tracks lists every track and its releases.
func (tx *playEditTx) Tracks() ([]PlayTrackFull, []byte, error) {
	body, _, err := tx.cl.doJSON("GET", tx.cl.editPath(tx.id)+"/tracks", nil)
	if err != nil {
		return nil, body, err
	}
	var r struct {
		Tracks []PlayTrackFull `json:"tracks"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, body, err
	}
	return r.Tracks, body, nil
}

// GetTrackRaw reads one track BOTH as a typed value (for reporting) and as the
// raw JSON object (for a lossless read-modify-write).
//
// The raw map matters: a Track carries fields we do not model —
// countryTargeting, inAppUpdatePriority — and a halt/resume that round-trips
// through a typed struct would silently DROP them, quietly changing a staged
// rollout's country targeting. So halt/resume mutate the raw object in place.
func (tx *playEditTx) GetTrackRaw(track string) (*PlayTrackFull, map[string]interface{}, []byte, error) {
	body, _, err := tx.cl.doJSON("GET", tx.cl.editPath(tx.id)+"/tracks/"+track, nil)
	if err != nil {
		return nil, nil, body, err
	}
	var typed PlayTrackFull
	if err := json.Unmarshal(body, &typed); err != nil {
		return nil, nil, body, err
	}
	var raw map[string]interface{}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, nil, body, err
	}
	return &typed, raw, body, nil
}

// PutTrack writes a track back (PUT replaces the track's release list).
func (tx *playEditTx) PutTrack(track string, payload interface{}) (*PlayTrackFull, []byte, error) {
	body, _, err := tx.cl.doJSON("PUT", tx.cl.editPath(tx.id)+"/tracks/"+track, payload)
	if err != nil {
		return nil, body, err
	}
	var t PlayTrackFull
	if err := json.Unmarshal(body, &t); err != nil {
		return nil, body, err
	}
	if t.Track == "" {
		t.Track = track
	}
	return &t, body, nil
}

// ───────────────────────────────── ops verbs ────────────────────────────────

// playSubmitArgs is the union payload of the Play submission verbs. Optional
// strings are pointers so "" (deliberately clear the field) is distinguishable
// from "not supplied" — a plain string would silently wipe a description.
type playSubmitArgs struct {
	Project     string `json:"project"`
	PackageName string `json:"packageName"`
	// EditID adopts a caller-owned edit: the verb mutates inside it and does NOT
	// validate, commit or delete it. Chain several verbs, then commit once with
	// play_submit_for_review.
	EditID   string `json:"editId"`
	Language string `json:"language"`
	Locale   string `json:"locale"` // alias for language

	// play_listing_set
	Title            *string `json:"title"`
	ShortDescription *string `json:"shortDescription"`
	FullDescription  *string `json:"fullDescription"`
	Video            *string `json:"video"`

	// play_images_set
	ImageType string   `json:"imageType"`
	Files     []string `json:"files"`
	Replace   bool     `json:"replace"`

	// play_bundle_upload
	File string `json:"file"`

	// play_track_release / play_release_halt / play_release_resume
	Track        string   `json:"track"`
	VersionCodes []string `json:"versionCodes"`
	VersionCode  string   `json:"versionCode"`
	Status       string   `json:"status"`
	UserFraction float64  `json:"userFraction"`
	ReleaseName  *string  `json:"releaseName"`
	ReleaseNotes *string  `json:"releaseNotes"`

	// play_submit_for_review
	ChangesNotSentForReview bool `json:"changesNotSentForReview"`
}

// lang resolves the listing language (Play's term), accepting `locale` as an
// alias so a caller coming from the Apple verbs is not tripped up.
func (a playSubmitArgs) lang() string {
	if s := strings.TrimSpace(a.Language); s != "" {
		return s
	}
	if s := strings.TrimSpace(a.Locale); s != "" {
		return s
	}
	return "en-US"
}

// codes returns the version codes the caller specified, accepting either the
// list or the single-value convenience form.
func (a playSubmitArgs) codes() []string {
	out := make([]string, 0, len(a.VersionCodes)+1)
	for _, c := range a.VersionCodes {
		if c = strings.TrimSpace(c); c != "" {
			out = append(out, c)
		}
	}
	if c := strings.TrimSpace(a.VersionCode); c != "" {
		out = append(out, c)
	}
	return out
}

func parsePlaySubmitArgs(payload json.RawMessage) (playSubmitArgs, *OpsResult) {
	var a playSubmitArgs
	if len(payload) > 0 {
		if err := json.Unmarshal(payload, &a); err != nil {
			return a, &OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
		}
	}
	if strings.TrimSpace(a.PackageName) == "" {
		return a, &OpsResult{OK: false, Code: "bad_payload", Error: "packageName required (e.g. com.acme.app)"}
	}
	return a, nil
}

// resolvePlayClient builds the per-project Play client.
func resolvePlayClient(a playSubmitArgs) (*playClient, *OpsResult) {
	cl, err := newPlayClientFn(a.Project, strings.TrimSpace(a.PackageName))
	if err != nil {
		return nil, &OpsResult{OK: false, Code: "no_credentials", Error: err.Error()}
	}
	return cl, nil
}

// requireTrack validates the track name up front rather than letting Google
// create a phantom custom track from a typo.
func requireTrack(track string) (string, *OpsResult) {
	t := strings.TrimSpace(track)
	if t == "" {
		return "", &OpsResult{OK: false, Code: "bad_payload", Error: "track required (internal | alpha | beta | production)"}
	}
	if !playTracks[t] {
		return "", &OpsResult{OK: false, Code: "bad_payload",
			Error: fmt.Sprintf("unknown track %q (want internal | alpha | beta | production)", track)}
	}
	return t, nil
}

func init() {
	registerOpsVerb(opsVerbSpec{
		Name:        "play_submit_status",
		Description: "PREFLIGHT a Google Play submission: report the app's default language, every track and its releases (versionCodes + status + rollout fraction), which bundles Google holds, per-locale listing completeness (title/shortDescription/fullDescription), whether the REQUIRED graphics exist (icon, featureGraphic, and at least 2 phoneScreenshots) — and exactly WHAT IS MISSING. Also names the steps Google exposes NO API for (content rating / IARC, App content declarations, the first rollout of a never-published app) with the exact Console path. Run this before play_submit_for_review.",
		Schema: ghostJSONSchema(map[string]interface{}{
			"project":     map[string]interface{}{"type": "string", "description": "Project slug whose vault holds the Play service account. Omit for the default project."},
			"packageName": map[string]interface{}{"type": "string", "description": "Android package name, e.g. com.acme.app."},
			"language":    map[string]interface{}{"type": "string", "description": "Primary listing language to check in detail (default en-US, or the app's defaultLanguage)."},
			"track":       map[string]interface{}{"type": "string", "description": "Track to focus the readiness check on: internal | alpha | beta | production (default production)."},
		}, "packageName"),
		Handler:    playSubmitStatusHandler,
		AllowGuest: false,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "play_listing_set",
		Description: "Set the Play store listing for one language (title ≤30, shortDescription ≤80, fullDescription ≤4000, promo video URL). Only the fields you pass are changed. Opens its own edit and commits it WITHOUT sending the app for review — pass editId to stage the change inside an edit you already own instead.",
		Schema: ghostJSONSchema(map[string]interface{}{
			"project":          map[string]interface{}{"type": "string"},
			"packageName":      map[string]interface{}{"type": "string"},
			"editId":           map[string]interface{}{"type": "string", "description": "Stage inside an existing edit instead of opening+committing one. The edit is NOT committed or deleted — you own it."},
			"language":         map[string]interface{}{"type": "string", "description": "BCP-47 listing language, e.g. en-US (default en-US)."},
			"title":            map[string]interface{}{"type": "string", "description": "App name on the listing. Play limit: 30 chars."},
			"shortDescription": map[string]interface{}{"type": "string", "description": "Play limit: 80 chars."},
			"fullDescription":  map[string]interface{}{"type": "string", "description": "Play limit: 4000 chars."},
			"video":            map[string]interface{}{"type": "string", "description": "YouTube URL for the promo video."},
		}, "packageName"),
		Handler:    playListingSetHandler,
		AllowGuest: false,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "play_images_set",
		Description: "Upload Play listing graphics for one language and image type (icon, featureGraphic, phoneScreenshots, sevenInchScreenshots, tenInchScreenshots, tvScreenshots, wearScreenshots, tvBanner). Pass replace:true to delete every image already in that bucket first (required for icon/featureGraphic, which hold exactly one). Opens its own edit and commits it WITHOUT sending the app for review.",
		Schema: ghostJSONSchema(map[string]interface{}{
			"project":     map[string]interface{}{"type": "string"},
			"packageName": map[string]interface{}{"type": "string"},
			"editId":      map[string]interface{}{"type": "string", "description": "Stage inside an existing edit instead of opening+committing one."},
			"language":    map[string]interface{}{"type": "string", "description": "Default en-US."},
			"imageType":   map[string]interface{}{"type": "string", "description": "icon | featureGraphic | phoneScreenshots | sevenInchScreenshots | tenInchScreenshots | tvScreenshots | wearScreenshots | tvBanner."},
			"files":       map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}, "description": "Absolute paths to the PNG/JPEG images on the target machine, in display order."},
			"replace":     map[string]interface{}{"type": "boolean", "description": "Delete the images already in this bucket before uploading (default false = append)."},
		}, "packageName", "imageType", "files"),
		Handler:    playImagesSetHandler,
		AllowGuest: false,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "play_bundle_upload",
		Description: "Upload an Android App Bundle (.aab) to Google Play and return the versionCode Google assigned. Uploading does NOT release it — follow with play_track_release to put the versionCode on a track. Opens its own edit and commits it WITHOUT sending the app for review; pass editId to upload + release in one transaction.",
		Schema: ghostJSONSchema(map[string]interface{}{
			"project":     map[string]interface{}{"type": "string"},
			"packageName": map[string]interface{}{"type": "string"},
			"editId":      map[string]interface{}{"type": "string", "description": "Stage inside an existing edit instead of opening+committing one."},
			"file":        map[string]interface{}{"type": "string", "description": "Absolute path to the .aab on the target machine."},
		}, "packageName", "file"),
		Handler:    playBundleUploadHandler,
		AllowGuest: false,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "play_track_release",
		Description: "Put version codes on a track as a release. track: internal | alpha | beta | production. status: draft | inProgress | halted | completed. userFraction (0<f<1) stages a partial rollout and is only valid with status inProgress. NOTE: Play's track PUT REPLACES the track's release list — the release you describe becomes the track's release. Commits WITHOUT sending for review; use play_submit_for_review to actually submit.",
		Schema: ghostJSONSchema(map[string]interface{}{
			"project":      map[string]interface{}{"type": "string"},
			"packageName":  map[string]interface{}{"type": "string"},
			"editId":       map[string]interface{}{"type": "string", "description": "Stage inside an existing edit instead of opening+committing one."},
			"track":        map[string]interface{}{"type": "string", "description": "internal | alpha | beta | production."},
			"versionCodes": map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}, "description": "Version codes to release (from play_bundle_upload)."},
			"versionCode":  map[string]interface{}{"type": "string", "description": "Convenience: a single version code."},
			"status":       map[string]interface{}{"type": "string", "description": "draft | inProgress | halted | completed (default completed)."},
			"userFraction": map[string]interface{}{"type": "number", "description": "Staged-rollout fraction, strictly between 0 and 1. Only with status inProgress."},
			"releaseName":  map[string]interface{}{"type": "string", "description": "Human-readable release name shown in the Console."},
			"releaseNotes": map[string]interface{}{"type": "string", "description": "What's-new text for `language` (default en-US)."},
			"language":     map[string]interface{}{"type": "string", "description": "Language of releaseNotes (default en-US)."},
		}, "packageName", "track"),
		Handler:    playTrackReleaseHandler,
		AllowGuest: false,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "play_submit_for_review",
		Description: "SUBMIT to Google Play: validate the edit, then commit it WITH changes sent for review. On Play there is no separate submit call — a committed edit whose track release is inProgress/completed IS the submission. Validates first and surfaces Google's error body VERBATIM. Names the Console-only gates when Google reports them (first rollout of a never-published app; an app whose changes Google refuses to auto-send for review). Run play_submit_status first.",
		Schema: ghostJSONSchema(map[string]interface{}{
			"project":                 map[string]interface{}{"type": "string"},
			"packageName":             map[string]interface{}{"type": "string"},
			"editId":                  map[string]interface{}{"type": "string", "description": "Commit an edit you already staged with the other verbs. Omit to open (and immediately commit) an empty edit — only useful to flush changes already staged elsewhere."},
			"track":                   map[string]interface{}{"type": "string", "description": "Track being submitted, for the readiness note in the result (optional)."},
			"changesNotSentForReview": map[string]interface{}{"type": "boolean", "description": "Commit the changes but do NOT send them for review (a human then hits 'Send changes for review' in the Console). Google DEMANDS this for some apps — it says so in the error."},
		}, "packageName"),
		Handler:    playSubmitForReviewHandler,
		AllowGuest: false,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "play_release_halt",
		Description: "Halt a live/staged rollout on a track (status → halted) — Play's equivalent of pulling a submission back. Halts the release matching versionCode, else the track's inProgress release.",
		Schema: ghostJSONSchema(map[string]interface{}{
			"project":     map[string]interface{}{"type": "string"},
			"packageName": map[string]interface{}{"type": "string"},
			"editId":      map[string]interface{}{"type": "string"},
			"track":       map[string]interface{}{"type": "string", "description": "internal | alpha | beta | production."},
			"versionCode": map[string]interface{}{"type": "string", "description": "Version code of the release to halt. Omit to halt the track's inProgress release."},
		}, "packageName", "track"),
		Handler:    playReleaseHaltHandler,
		AllowGuest: false,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "play_release_resume",
		Description: "Resume a halted rollout on a track (status → inProgress). Pass userFraction to resume as a staged rollout. Resumes the release matching versionCode, else the track's halted release.",
		Schema: ghostJSONSchema(map[string]interface{}{
			"project":      map[string]interface{}{"type": "string"},
			"packageName":  map[string]interface{}{"type": "string"},
			"editId":       map[string]interface{}{"type": "string"},
			"track":        map[string]interface{}{"type": "string", "description": "internal | alpha | beta | production."},
			"versionCode":  map[string]interface{}{"type": "string", "description": "Version code of the release to resume. Omit to resume the track's halted release."},
			"userFraction": map[string]interface{}{"type": "number", "description": "Resume as a staged rollout at this fraction (0<f<1). Omit for a full rollout."},
		}, "packageName", "track"),
		Handler:    playReleaseResumeHandler,
		AllowGuest: false,
	})
}

// ── play_submit_status ──

// playListingStatus is the per-locale readiness view.
type playListingStatus struct {
	Language            string `json:"language"`
	HasTitle            bool   `json:"hasTitle"`
	HasShortDescription bool   `json:"hasShortDescription"`
	HasFullDescription  bool   `json:"hasFullDescription"`
	HasVideo            bool   `json:"hasVideo"`
	TitleLen            int    `json:"titleLen"`
	ShortDescriptionLen int    `json:"shortDescriptionLen"`
	FullDescriptionLen  int    `json:"fullDescriptionLen"`
}

// playImageStat is one graphics bucket's count.
type playImageStat struct {
	ImageType string `json:"imageType"`
	Count     int    `json:"count"`
	Hint      string `json:"hint,omitempty"`
}

// playRequiredImages are the buckets Play REQUIRES on a listing, with the
// minimum count each must hold.
var playRequiredImages = []struct {
	Type string
	Min  int
}{
	{"icon", 1},
	{"featureGraphic", 1},
	{"phoneScreenshots", 2},
}

func playSubmitStatusHandler(c OpsContext, payload json.RawMessage) OpsResult {
	a, bad := parsePlaySubmitArgs(payload)
	if bad != nil {
		return *bad
	}
	cl, deny := resolvePlayClient(a)
	if deny != nil {
		return *deny
	}
	track := strings.TrimSpace(a.Track)
	if track == "" {
		track = "production"
	}

	// A status read still needs an edit (Play has no read-only view of listings
	// or tracks outside one). It is discarded unconditionally — a preflight must
	// never leave a lock behind.
	tx, deny := beginPlayEdit(cl, a.EditID)
	if deny != nil {
		return *deny
	}
	defer tx.abort()

	var missing []string
	res := map[string]interface{}{
		"packageName": cl.pkg,
		"editId":      tx.id,
		"track":       track,
	}
	if tx.expiry != "" {
		res["editExpiryTimeSeconds"] = tx.expiry
	}

	// App details → default language.
	details, body, err := tx.GetDetails()
	if err != nil {
		return tx.gated("read app details", body, err)
	}
	res["details"] = details
	lang := a.lang()
	if strings.TrimSpace(a.Language) == "" && strings.TrimSpace(a.Locale) == "" &&
		strings.TrimSpace(details.DefaultLanguage) != "" {
		lang = details.DefaultLanguage
	}
	res["language"] = lang

	// Listings.
	listings, body, err := tx.Listings()
	if err != nil {
		return tx.gated("list store listings", body, err)
	}
	stats := make([]playListingStatus, 0, len(listings))
	var primary *PlayListing
	for i := range listings {
		l := listings[i]
		stats = append(stats, playListingStatus{
			Language:            l.Language,
			HasTitle:            strings.TrimSpace(l.Title) != "",
			HasShortDescription: strings.TrimSpace(l.ShortDescription) != "",
			HasFullDescription:  strings.TrimSpace(l.FullDescription) != "",
			HasVideo:            strings.TrimSpace(l.Video) != "",
			TitleLen:            len([]rune(l.Title)),
			ShortDescriptionLen: len([]rune(l.ShortDescription)),
			FullDescriptionLen:  len([]rune(l.FullDescription)),
		})
		if strings.EqualFold(l.Language, lang) {
			primary = &listings[i]
		}
	}
	res["listings"] = stats
	if len(listings) == 0 {
		missing = append(missing, "the app has no store listing at all — create one with play_listing_set")
	}
	if primary == nil {
		missing = append(missing, lang+": no store listing for this language — create it with play_listing_set")
		// Fall through with an EMPTY listing rather than stopping here: an agent
		// reading `missing` needs to know WHICH fields it must supply, not just
		// that the listing is absent.
		primary = &PlayListing{Language: lang}
	}
	// Play's own limits: title 30, shortDescription 80, fullDescription 4000.
	for _, f := range []struct {
		name  string
		value string
		limit int
	}{
		{"title", primary.Title, 30},
		{"shortDescription", primary.ShortDescription, 80},
		{"fullDescription", primary.FullDescription, 4000},
	} {
		if strings.TrimSpace(f.value) == "" {
			missing = append(missing, fmt.Sprintf("%s: %s is empty — set it with play_listing_set (Play limit %d chars)",
				lang, f.name, f.limit))
		} else if n := len([]rune(f.value)); n > f.limit {
			missing = append(missing, fmt.Sprintf("%s: %s is %d chars — Play's limit is %d",
				lang, f.name, n, f.limit))
		}
	}

	// Graphics. Play REQUIRES icon + featureGraphic + ≥2 phone screenshots.
	imageStats := make([]playImageStat, 0, len(playRequiredImages))
	for _, want := range playRequiredImages {
		imgs, body, err := tx.Images(lang, want.Type)
		if err != nil {
			return tx.gated("list "+want.Type+" for "+lang, body, err)
		}
		imageStats = append(imageStats, playImageStat{
			ImageType: want.Type,
			Count:     len(imgs),
			Hint:      playImageTypes[want.Type],
		})
		if len(imgs) < want.Min {
			missing = append(missing, fmt.Sprintf(
				"%s: %s has %d image(s), Play REQUIRES at least %d — upload with play_images_set (%s)",
				lang, want.Type, len(imgs), want.Min, playImageTypes[want.Type]))
		}
	}
	res["images"] = imageStats

	// Bundles Google holds.
	bundles, body, err := tx.Bundles()
	if err != nil {
		return tx.gated("list bundles", body, err)
	}
	res["bundles"] = bundles
	res["bundleUploaded"] = len(bundles) > 0
	if len(bundles) == 0 {
		missing = append(missing, "no app bundle has been uploaded — upload one with play_bundle_upload")
	}

	// Tracks + their releases.
	tracks, body, err := tx.Tracks()
	if err != nil {
		return tx.gated("list tracks", body, err)
	}
	res["tracks"] = tracks
	var focus *PlayTrackFull
	for i := range tracks {
		if strings.EqualFold(tracks[i].Track, track) {
			focus = &tracks[i]
		}
	}
	if focus == nil || len(focus.Releases) == 0 {
		missing = append(missing, "the "+track+" track has no release — create one with play_track_release")
	} else {
		hasCodes := false
		for _, r := range focus.Releases {
			if len(r.VersionCodes) > 0 {
				hasCodes = true
			}
			if r.Status == "draft" {
				missing = append(missing, "the "+track+" track's release is still `draft` — it will NOT reach users or review until it is inProgress/completed (play_track_release)")
			}
		}
		if !hasCodes {
			missing = append(missing, "the "+track+" track's release carries no versionCodes — attach one with play_track_release")
		}
	}

	// Gates. These are steps the API genuinely cannot perform (or, for
	// data safety, deliberately does not) — reported, never faked.
	consoleOnly := []playGate{gateContentRating(), gateAppContent(), gateFirstRelease(cl.pkg), gatePerEmailTesters()}
	res["consoleOnly"] = consoleOnly
	res["notAutomated"] = []playGate{gateDataSafety(cl.pkg)}
	res["missing"] = missing
	// `ready` covers ONLY what the API can see. The unverifiable gates are listed
	// separately and deliberately do NOT flip it — claiming readiness we cannot
	// check would be the lie this whole file exists to avoid.
	res["ready"] = len(missing) == 0
	res["note"] = "`ready` reflects ONLY API-visible state. consoleOnly lists steps Android Publisher v3 exposes NO endpoint for: the content rating (IARC) questionnaire and the App content declarations cannot even be READ back from here, and the first rollout of a never-published app is refused by the API (Google answers rolloutNotPermittedOnDraftApp at commit time). notAutomated lists the Data safety form, which DOES have an API (POST /applications/" + cl.pkg + "/dataSafety, CSV) that these verbs do not drive and cannot read back. None of these are performed by this agent."
	return OpsResult{OK: true, Initial: res}
}

// ── play_listing_set ──

func playListingSetHandler(c OpsContext, payload json.RawMessage) OpsResult {
	a, bad := parsePlaySubmitArgs(payload)
	if bad != nil {
		return *bad
	}
	fields := map[string]interface{}{}
	put := func(key string, v *string) {
		if v != nil {
			fields[key] = *v
		}
	}
	put("title", a.Title)
	put("shortDescription", a.ShortDescription)
	put("fullDescription", a.FullDescription)
	put("video", a.Video)
	if len(fields) == 0 {
		return OpsResult{OK: false, Code: "bad_payload",
			Error: "nothing to set — pass at least one of title, shortDescription, fullDescription, video"}
	}
	// Fail on Play's documented limits here rather than burning an edit on a
	// round-trip Google will reject anyway.
	lang := a.lang()
	for key, limit := range map[string]int{"title": 30, "shortDescription": 80, "fullDescription": 4000} {
		v, ok := fields[key].(string)
		if !ok {
			continue
		}
		if n := len([]rune(v)); n > limit {
			return OpsResult{OK: false, Code: "bad_payload",
				Error: fmt.Sprintf("%s is %d chars — Play's limit is %d", key, n, limit)}
		}
	}
	fields["language"] = lang

	cl, deny := resolvePlayClient(a)
	if deny != nil {
		return *deny
	}
	tx, deny := beginPlayEdit(cl, a.EditID)
	if deny != nil {
		return *deny
	}
	defer tx.abort() // discards the edit on EVERY failure path below

	listing, body, err := tx.SetListing(lang, fields)
	if err != nil {
		return tx.gated("set listing for "+lang, body, err)
	}
	// Commit WITHOUT sending for review: setting a description must never
	// silently ship the app to Google's reviewers.
	if res := tx.finish(false); res != nil {
		return *res
	}

	updated := make([]string, 0, len(fields))
	for k := range fields {
		if k != "language" {
			updated = append(updated, k)
		}
	}
	sort.Strings(updated)
	return OpsResult{OK: true, Initial: map[string]interface{}{
		"packageName":   cl.pkg,
		"editId":        tx.id,
		"editCommitted": tx.owned,
		"language":      listing.Language,
		"updated":       updated,
		"listing":       listing,
		"sentForReview": false,
	}}
}

// ── play_images_set ──

func playImagesSetHandler(c OpsContext, payload json.RawMessage) OpsResult {
	a, bad := parsePlaySubmitArgs(payload)
	if bad != nil {
		return *bad
	}
	imageType := strings.TrimSpace(a.ImageType)
	if imageType == "" {
		return OpsResult{OK: false, Code: "bad_payload", Error: "imageType required (icon | featureGraphic | phoneScreenshots | sevenInchScreenshots | tenInchScreenshots | tvScreenshots | wearScreenshots | tvBanner)"}
	}
	if _, ok := playImageTypes[imageType]; !ok {
		known := make([]string, 0, len(playImageTypes))
		for k := range playImageTypes {
			known = append(known, k)
		}
		sort.Strings(known)
		return OpsResult{OK: false, Code: "bad_payload",
			Error: fmt.Sprintf("unknown imageType %q (want one of: %s)", imageType, strings.Join(known, ", "))}
	}
	if len(a.Files) == 0 {
		return OpsResult{OK: false, Code: "bad_payload", Error: "files required — absolute paths to the images, in display order"}
	}
	for _, f := range a.Files {
		st, err := os.Stat(f)
		if err != nil {
			return OpsResult{OK: false, Code: "not_found", Error: "image " + f + ": " + err.Error()}
		}
		if st.IsDir() {
			return OpsResult{OK: false, Code: "bad_payload", Error: f + " is a directory"}
		}
		switch strings.ToLower(filepath.Ext(f)) {
		case ".png", ".jpg", ".jpeg":
		default:
			return OpsResult{OK: false, Code: "bad_payload", Error: f + ": Play listing graphics must be PNG or JPEG"}
		}
	}
	// icon and featureGraphic are single-slot buckets: appending a second one is
	// a guaranteed Google rejection. Say so here instead of half-uploading.
	if (imageType == "icon" || imageType == "featureGraphic" || imageType == "tvBanner") && len(a.Files) > 1 {
		return OpsResult{OK: false, Code: "bad_payload",
			Error: fmt.Sprintf("%s holds exactly ONE image; you passed %d files", imageType, len(a.Files))}
	}

	lang := a.lang()
	cl, deny := resolvePlayClient(a)
	if deny != nil {
		return *deny
	}
	tx, deny := beginPlayEdit(cl, a.EditID)
	if deny != nil {
		return *deny
	}
	defer tx.abort()

	deleted := 0
	if a.Replace {
		gone, body, err := tx.DeleteAllImages(lang, imageType)
		if err != nil {
			return tx.gated("delete existing "+imageType, body, err)
		}
		deleted = len(gone)
	}

	uploaded := make([]PlayImage, 0, len(a.Files))
	for _, f := range a.Files {
		img, body, err := tx.UploadImage(lang, imageType, f)
		if err != nil {
			// Partial progress is stated plainly, and the edit is discarded by the
			// deferred abort() — so a half-uploaded bucket never lands on Play.
			res := tx.gated(fmt.Sprintf("upload %s (%d/%d succeeded before it failed)",
				filepath.Base(f), len(uploaded), len(a.Files)), body, err)
			res.Code = "upload_failed"
			return res
		}
		uploaded = append(uploaded, *img)
	}
	if res := tx.finish(false); res != nil {
		return *res
	}
	return OpsResult{OK: true, Initial: map[string]interface{}{
		"packageName":   cl.pkg,
		"editId":        tx.id,
		"editCommitted": tx.owned,
		"language":      lang,
		"imageType":     imageType,
		"hint":          playImageTypes[imageType],
		"deleted":       deleted,
		"uploaded":      uploaded,
		"uploadedCount": len(uploaded),
		"sentForReview": false,
	}}
}

// ── play_bundle_upload ──

func playBundleUploadHandler(c OpsContext, payload json.RawMessage) OpsResult {
	a, bad := parsePlaySubmitArgs(payload)
	if bad != nil {
		return *bad
	}
	file := strings.TrimSpace(a.File)
	if file == "" {
		return OpsResult{OK: false, Code: "bad_payload", Error: "file required — absolute path to the .aab"}
	}
	st, err := os.Stat(file)
	if err != nil {
		return OpsResult{OK: false, Code: "not_found", Error: "bundle " + file + ": " + err.Error()}
	}
	if st.IsDir() {
		return OpsResult{OK: false, Code: "bad_payload", Error: file + " is a directory"}
	}
	if !strings.EqualFold(filepath.Ext(file), ".aab") {
		return OpsResult{OK: false, Code: "bad_payload",
			Error: file + ": play_bundle_upload takes an Android App Bundle (.aab). For an .apk use the Play Console or edits.apks."}
	}

	cl, deny := resolvePlayClient(a)
	if deny != nil {
		return *deny
	}
	tx, deny := beginPlayEdit(cl, a.EditID)
	if deny != nil {
		return *deny
	}
	defer tx.abort()

	bundle, body, err := tx.UploadBundle(file)
	if err != nil {
		return tx.gated("upload bundle "+filepath.Base(file), body, err)
	}
	// Commit so the bundle is durably Google's. Not sent for review: an upload
	// is not a submission.
	if res := tx.finish(false); res != nil {
		return *res
	}
	return OpsResult{OK: true, Initial: map[string]interface{}{
		"packageName":   cl.pkg,
		"editId":        tx.id,
		"editCommitted": tx.owned,
		"file":          filepath.Base(file),
		"sizeBytes":     st.Size(),
		"versionCode":   bundle.VersionCode,
		"sha256":        bundle.Sha256,
		"sentForReview": false,
		"next":          "play_track_release to put versionCode on a track, then play_submit_for_review",
	}}
}

// ── play_track_release ──

func playTrackReleaseHandler(c OpsContext, payload json.RawMessage) OpsResult {
	a, bad := parsePlaySubmitArgs(payload)
	if bad != nil {
		return *bad
	}
	track, deny := requireTrack(a.Track)
	if deny != nil {
		return *deny
	}
	status := strings.TrimSpace(a.Status)
	if status == "" {
		status = "completed"
	}
	if !playReleaseStatuses[status] {
		return OpsResult{OK: false, Code: "bad_payload",
			Error: fmt.Sprintf("unknown status %q (want draft | inProgress | halted | completed)", a.Status)}
	}
	codes := a.codes()
	if len(codes) == 0 {
		return OpsResult{OK: false, Code: "bad_payload",
			Error: "versionCodes (or versionCode) required — the codes returned by play_bundle_upload"}
	}
	// Google rejects userFraction outside (0,1), and rejects it at all unless the
	// release is inProgress. Refuse locally rather than burn an edit.
	if a.UserFraction != 0 {
		if status != "inProgress" {
			return OpsResult{OK: false, Code: "bad_payload",
				Error: fmt.Sprintf("userFraction is only valid with status inProgress (got status %q)", status)}
		}
		if a.UserFraction <= 0 || a.UserFraction >= 1 {
			return OpsResult{OK: false, Code: "bad_payload",
				Error: fmt.Sprintf("userFraction must be strictly between 0 and 1 (got %v)", a.UserFraction)}
		}
	}

	rel := PlayTrackRelease{VersionCodes: codes, Status: status}
	if a.ReleaseName != nil {
		rel.Name = *a.ReleaseName
	}
	if status == "inProgress" && a.UserFraction > 0 {
		rel.UserFraction = a.UserFraction
	}
	if a.ReleaseNotes != nil {
		rel.ReleaseNotes = []PlayReleaseNote{{Language: a.lang(), Text: *a.ReleaseNotes}}
	}

	cl, deny := resolvePlayClient(a)
	if deny != nil {
		return *deny
	}
	tx, deny := beginPlayEdit(cl, a.EditID)
	if deny != nil {
		return *deny
	}
	defer tx.abort()

	updated, body, err := tx.PutTrack(track, PlayTrackFull{Track: track, Releases: []PlayTrackRelease{rel}})
	if err != nil {
		return tx.gated("set release on "+track+" track", body, err)
	}
	if res := tx.finish(false); res != nil {
		return *res
	}
	return OpsResult{OK: true, Initial: map[string]interface{}{
		"packageName":   cl.pkg,
		"editId":        tx.id,
		"editCommitted": tx.owned,
		"track":         updated.Track,
		"releases":      updated.Releases,
		"sentForReview": false,
		"note":          "Committed WITHOUT sending for review. Run play_submit_for_review to actually submit this release to Google.",
	}}
}

// ── play_submit_for_review ──

func playSubmitForReviewHandler(c OpsContext, payload json.RawMessage) OpsResult {
	a, bad := parsePlaySubmitArgs(payload)
	if bad != nil {
		return *bad
	}
	cl, deny := resolvePlayClient(a)
	if deny != nil {
		return *deny
	}

	// Unlike every other verb here, this one COMMITS the caller's edit — that is
	// the whole point. So it takes ownership of an adopted editId.
	tx, deny := beginPlayEdit(cl, a.EditID)
	if deny != nil {
		return *deny
	}
	tx.owned = true
	defer tx.abort() // no-op once the commit lands

	// Validate first: a dry run against Google's own rules, so a bad edit is
	// reported without half-submitting anything.
	if body, err := tx.validate(); err != nil {
		return tx.gated("validate play edit "+tx.id, body, err)
	}

	sendForReview := !a.ChangesNotSentForReview
	body, err := tx.commit(sendForReview)
	if err != nil {
		return tx.gated("commit play edit "+tx.id, body, err)
	}

	res := map[string]interface{}{
		"packageName":   cl.pkg,
		"editId":        tx.id,
		"committed":     true,
		"sentForReview": sendForReview,
		"googleResponse": func() interface{} {
			var m map[string]interface{}
			if json.Unmarshal(body, &m) == nil {
				return m
			}
			return strings.TrimSpace(string(body))
		}(),
	}
	if track := strings.TrimSpace(a.Track); track != "" {
		res["track"] = track
	}
	if sendForReview {
		res["note"] = "The edit was committed WITH changes sent for review. On Play this IS the submission: a committed release with status inProgress/completed on a review-triggering track goes to Google's reviewers. Google's review outcome is not exposed over Android Publisher v3 — watch the Play Console's Publishing overview."
	} else {
		res["note"] = "The edit was committed with changesNotSentForReview=true: the changes ARE live in the Console but have NOT been submitted. A human must hit 'Send changes for review' in Play Console → Publishing overview. This agent did NOT submit the app."
		res["consoleOnly"] = []playGate{gateChangesReviewFromConsole(cl.pkg)}
	}
	return OpsResult{OK: true, Initial: res}
}

// ── play_release_halt / play_release_resume ──

// setReleaseStatus is the shared read-modify-write behind halt and resume.
//
// It mutates the RAW track object so fields we do not model (countryTargeting,
// inAppUpdatePriority, releaseNotes on other locales) survive the round-trip
// untouched. A typed round-trip would drop them — silently rewriting the
// operator's rollout config while claiming to have only flipped a status.
func setReleaseStatus(a playSubmitArgs, want, from string, userFraction float64) OpsResult {
	track, deny := requireTrack(a.Track)
	if deny != nil {
		return *deny
	}
	cl, deny := resolvePlayClient(a)
	if deny != nil {
		return *deny
	}
	tx, deny := beginPlayEdit(cl, a.EditID)
	if deny != nil {
		return *deny
	}
	defer tx.abort()

	typed, raw, body, err := tx.GetTrackRaw(track)
	if err != nil {
		return tx.gated("read "+track+" track", body, err)
	}
	rawReleases, _ := raw["releases"].([]interface{})
	if len(typed.Releases) == 0 || len(rawReleases) == 0 {
		return OpsResult{OK: false, Code: "not_found",
			Error: "the " + track + " track has no release to " + want}
	}

	wantCode := strings.TrimSpace(a.VersionCode)
	idx := -1
	for i, r := range typed.Releases {
		if wantCode != "" {
			for _, vc := range r.VersionCodes {
				if vc == wantCode {
					idx = i
				}
			}
			continue
		}
		if r.Status == from {
			idx = i
			break
		}
	}
	if idx < 0 || idx >= len(rawReleases) {
		have := make([]string, 0, len(typed.Releases))
		for _, r := range typed.Releases {
			have = append(have, fmt.Sprintf("%s=%s", strings.Join(r.VersionCodes, "+"), r.Status))
		}
		msg := fmt.Sprintf("no %s release on the %s track to %s", from, track, want)
		if wantCode != "" {
			msg = fmt.Sprintf("versionCode %s is not on the %s track", wantCode, track)
		}
		if len(have) > 0 {
			msg += " (current releases: " + strings.Join(have, ", ") + ")"
		}
		return OpsResult{OK: false, Code: "not_found", Error: msg}
	}

	target, ok := rawReleases[idx].(map[string]interface{})
	if !ok {
		return OpsResult{OK: false, Code: "google_error",
			Error: "unexpected release shape on the " + track + " track"}
	}
	target["status"] = want
	if want == "inProgress" && userFraction > 0 {
		if userFraction >= 1 {
			return OpsResult{OK: false, Code: "bad_payload",
				Error: fmt.Sprintf("userFraction must be strictly between 0 and 1 (got %v)", userFraction)}
		}
		target["userFraction"] = userFraction
	} else {
		// A halted or fully-rolled-out release must not carry a fraction; Google
		// rejects the combination.
		delete(target, "userFraction")
	}

	updated, body, err := tx.PutTrack(track, raw)
	if err != nil {
		return tx.gated(want+" release on "+track+" track", body, err)
	}
	if res := tx.finish(false); res != nil {
		return *res
	}
	return OpsResult{OK: true, Initial: map[string]interface{}{
		"packageName":   cl.pkg,
		"editId":        tx.id,
		"editCommitted": tx.owned,
		"track":         updated.Track,
		"action":        want,
		"releases":      updated.Releases,
	}}
}

func playReleaseHaltHandler(c OpsContext, payload json.RawMessage) OpsResult {
	a, bad := parsePlaySubmitArgs(payload)
	if bad != nil {
		return *bad
	}
	return setReleaseStatus(a, "halted", "inProgress", 0)
}

func playReleaseResumeHandler(c OpsContext, payload json.RawMessage) OpsResult {
	a, bad := parsePlaySubmitArgs(payload)
	if bad != nil {
		return *bad
	}
	return setReleaseStatus(a, "inProgress", "halted", a.UserFraction)
}
