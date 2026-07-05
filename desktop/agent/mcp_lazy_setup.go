package main

// mcp_lazy_setup.go — the `yaver_lazy_setup` MCP tool.
//
// Target user is the cousin-at-a-cafe: a non-developer asking their
// coding agent (Claude Code mobile, etc.) to "install yaver" and
// nothing more. The AI is expected to orchestrate the whole dance —
// bash out the install, surface the OAuth URL to the human, poll for
// completion, relay the mobile-app install link — all without the
// human ever touching a CLI themselves.
//
// Without this tool the AI has to stitch together auth_status +
// auth_start + auth_wait + maybe a bash call to `yaver` + app-store
// links. That works, but is brittle: each tool has its own failure
// mode, the AI has to know the ordering, and a short tool-call
// timeout turns into a dead end. This one-shot tool wraps the whole
// orchestration into a single idempotent call that reports structured
// state and a `next_action` string the AI can speak verbatim.
//
// Design rules:
//
//   - **Idempotent.** Call it any number of times. On each call it
//     inspects on-disk state (config, pending-auth file, daemon
//     health) and computes the correct next step.
//   - **Non-destructive.** Never burns a fresh device code if a valid
//     pending one already exists — resumes it. Never mutates the
//     user's config except via the existing finalizers
//     (authFinalizeToken) that the other MCP tools already use.
//   - **Agent-friendly.** Returns a structured response that lets an
//     AI render a fixed UX:
//       status        — machine-readable state marker
//       sign_in_url   — if non-empty, the URL the human must tap
//       user_code     — short code shown on the URL for confirmation
//       mobile_app    — {ios,android} store links to relay to the human
//       next_action   — single sentence the AI says to the human now
//       detail        — human-readable troubleshooting notes
//   - **Optional in-call wait.** `wait_seconds > 0` blocks inside the
//     tool up to that many seconds (capped at 180) polling for sign-in
//     to complete. Handy when the calling AI can tolerate a longer
//     tool call. Otherwise it returns immediately and the AI polls by
//     recalling the tool.

import (
	"context"
	"net/http"
	"strings"
	"time"
)

// LazySetupResult is the payload returned by yaver_lazy_setup.
type LazySetupResult struct {
	// Machine-readable state. One of:
	//   "signed_in"        — everything is configured; no action needed
	//   "waiting_sign_in"  — the human has a pending URL to tap; sign-in hasn't landed yet
	//   "error"            — something unrecoverable happened (detail explains)
	Status string `json:"status"`

	// Populated when Status == "signed_in". Mirrors AuthStatusSnapshot.
	UserEmail string `json:"user_email,omitempty"`
	Provider  string `json:"provider,omitempty"`
	DeviceID  string `json:"device_id,omitempty"`

	// DaemonServing reports whether the local Yaver agent is actually
	// reachable on 127.0.0.1:18080 once Status == "signed_in". This is
	// the #1 silent first-run failure: the token saves fine, but the
	// best-effort `yaver serve` fork never came up (locked-down box,
	// sandbox, missing perms), so the human's phone will NEVER discover
	// this machine. When false, NextAction tells them to run `yaver
	// serve` manually instead of falsely reporting "all set".
	DaemonServing bool `json:"daemon_serving"`

	// Populated when Status == "waiting_sign_in".
	SignInURL        string `json:"sign_in_url,omitempty"`
	UserCode         string `json:"user_code,omitempty"`
	DeviceCode       string `json:"device_code,omitempty"`
	ExpiresInSeconds int    `json:"expires_in_seconds,omitempty"`

	// Mobile app install links — always populated so the AI can
	// surface them as soon as the human is signed in (or earlier, it
	// doesn't hurt to tell them in parallel).
	MobileApp struct {
		IOS         string `json:"ios"`
		Android     string `json:"android"`
		Instruction string `json:"instruction"`
	} `json:"mobile_app"`

	// IntegrationSetup is an idempotent readiness plan for provider
	// integrations used by MCP/remote-runtime/car/watch/TV. Connected
	// providers are reported as ready and reused; missing providers include
	// the exact setup action and Yaver verbs they unlock.
	IntegrationSetup map[string]interface{} `json:"integration_setup"`

	// Single sentence the AI can speak verbatim. Covers the current
	// state's next step — "tap this URL", "install the app", "you're
	// all set", etc.
	NextAction string `json:"next_action"`

	// Human-readable context for troubleshooting. Never contains
	// secrets.
	Detail string `json:"detail,omitempty"`
}

// yaverLazySetup runs the one-shot "install + auth + relay mobile
// app" flow. See the package comment for design notes.
func yaverLazySetup(ctx context.Context, waitSeconds int) (LazySetupResult, error) {
	out := LazySetupResult{}
	out.MobileApp.IOS = "https://apps.apple.com/us/app/yaver-io/id6760467669"
	out.MobileApp.Android = "https://play.google.com/store/apps/details?id=io.yaver.mobile"
	out.MobileApp.Instruction = "Open the Yaver app on your phone and sign in with the same account. If it is not installed yet, use the official download link. Your dev machine will appear automatically."
	out.IntegrationSetup = yaverIntegrationSetupStatus()

	// Fast path: already signed in.
	if snap := authStatusSnapshot(); snap.SignedIn {
		out.applySignedIn(snap)
		return out, nil
	}

	// Either we have a pending device-code flow (resume), or we need
	// to start a new one. authStartDeviceCode already shares the
	// pending-auth file with the CLI, so the same URL is reused if
	// one is in flight.
	start, err := authStartDeviceCode(ctx, "")
	if err != nil {
		out.Status = "error"
		out.Detail = "could not obtain a device-code URL: " + err.Error()
		out.NextAction = "Yaver couldn't reach the sign-in backend. Check your internet connection and try again."
		return out, nil
	}

	out.Status = "waiting_sign_in"
	out.SignInURL = start.URL
	out.UserCode = start.UserCode
	out.DeviceCode = start.DeviceCode
	out.ExpiresInSeconds = start.ExpiresIn
	out.NextAction = "Tap this link on your phone to sign in: " + start.URL + "  (sign in with Apple, Google, or Microsoft — whichever account you want to use with Yaver). I'll keep checking; no need to come back and tell me, I'll see it."

	if waitSeconds <= 0 {
		return out, nil
	}

	// Optional blocking wait — poll the same device code.
	deadline := time.Now().Add(time.Duration(waitSeconds) * time.Second)
	for time.Now().Before(deadline) {
		if ctx.Err() != nil {
			out.Detail = "polling cancelled by caller"
			return out, nil
		}
		// 3s poll interval matches the default in authWaitDeviceCode
		// and keeps us under the Convex rate limits.
		pollResult, err := authPollDeviceCode(ctx, "", start.DeviceCode)
		if err == nil && strings.EqualFold(pollResult.Status, "authorized") && pollResult.TokenSaved {
			// Sign-in landed during the wait. Snapshot again to
			// pick up userEmail, daemon state, etc.
			out.applySignedIn(authStatusSnapshot())
			return out, nil
		}
		select {
		case <-ctx.Done():
			out.Detail = "polling cancelled by caller"
			return out, nil
		case <-time.After(3 * time.Second):
		}
	}

	// Timed out waiting. Return the pending URL as-is so the next
	// call resumes the same code. Update the next_action to nudge
	// the AI to keep the human engaged.
	out.NextAction = "Still waiting for sign-in. Tell the human to tap " + start.URL + " — I'll keep checking."
	out.Detail = "wait_seconds elapsed with sign-in still pending; call yaver_lazy_setup again to continue"
	return out, nil
}

// applySignedIn fills the signed-in fields on the result and runs the
// daemon-health gate. A human is "signed in" the instant the token lands,
// but their phone can only discover this machine if the local agent is
// actually serving (and broadcasting its LAN beacon). That post-auth
// daemon start is best-effort and fails silently on locked-down or
// sandboxed boxes — so we verify it here, make ONE start attempt if it's
// down, and tell the human exactly what to do rather than reporting a
// false "you're all set".
func (out *LazySetupResult) applySignedIn(snap AuthStatusSnapshot) {
	out.Status = "signed_in"
	out.UserEmail = snap.UserEmail
	out.Provider = snap.Provider
	out.DeviceID = snap.DeviceID
	out.SignInURL = ""
	out.UserCode = ""
	out.DeviceCode = ""
	out.ExpiresInSeconds = 0
	out.IntegrationSetup = yaverIntegrationSetupStatus()

	who := strings.TrimSpace(snap.UserEmail)
	if who == "" {
		who = "your account"
	}

	serving := daemonServing()
	if !serving {
		// The fork that authFinalizeToken kicks off may have failed or
		// never ran (e.g. the MCP server started cold and the human
		// authed without `yaver serve` ever running). Try once, then
		// give serve a moment to bind its port before judging.
		safeStartDaemon()
		serving = waitDaemonServing(6 * time.Second)
	}
	out.DaemonServing = serving

	if serving {
		out.NextAction = "Signed in as " + who + " and your dev machine is online. Open the Yaver mobile app with the same account. For Gmail, Teams, GitHub, GitLab, and other MCP integrations, use integration_setup: ready providers are reused and missing ones show connect actions."
		return
	}

	// Honest failure — do NOT claim all-set. This is gap #1 for normies.
	out.NextAction = "Signed in as " + who + " — but the Yaver agent isn't running yet, so your phone won't be able to find this machine. Open a terminal and run `yaver serve` and leave it running, then open the Yaver mobile app and sign in with the same account."
	out.Detail = "Auth token is saved, but the local agent is not answering on 127.0.0.1:18080. The post-auth daemon start was attempted and did not come up within 6s — common on locked-down, sandboxed, or permission-restricted machines. Fix: run `yaver serve` manually. Until then, mobile discovery and phone-driven tasks will not work."
}

// daemonServing probes the local agent's HTTP API once with a short
// timeout. Returns true if anything answers on 127.0.0.1:18080 — proving
// the daemon process is up and listening (which is what mobile discovery
// needs). A stale PID file or a half-dead process yields a connection
// error and returns false. We accept any HTTP response (not just 200) so
// a token mismatch doesn't masquerade as "not serving".
func daemonServing() bool {
	token := ""
	if cfg, _ := LoadConfig(); cfg != nil {
		token = strings.TrimSpace(cfg.AuthToken)
	}
	req, err := http.NewRequest("GET", "http://127.0.0.1:18080/info", nil)
	if err != nil {
		return false
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	_ = resp.Body.Close()
	return true
}

// waitDaemonServing polls daemonServing until it succeeds or the budget
// elapses — giving a freshly-started `yaver serve` a moment to bind its
// port before we report a false negative.
func waitDaemonServing(budget time.Duration) bool {
	deadline := time.Now().Add(budget)
	for {
		if daemonServing() {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(500 * time.Millisecond)
	}
}
