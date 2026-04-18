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
	out.MobileApp.IOS = "https://testflight.apple.com/join/yaver"
	out.MobileApp.Android = "https://play.google.com/store/apps/details?id=io.yaver.mobile"
	out.MobileApp.Instruction = "Install the Yaver app on your phone and sign in with the same account. Your dev machine will appear automatically."

	// Fast path: already signed in.
	if snap := authStatusSnapshot(); snap.SignedIn {
		out.Status = "signed_in"
		out.UserEmail = snap.UserEmail
		out.Provider = snap.Provider
		out.DeviceID = snap.DeviceID
		if snap.UserEmail != "" {
			out.NextAction = "You're signed in as " + snap.UserEmail + ". Install the Yaver mobile app and sign in with the same account — your dev machine will show up in its device list."
		} else {
			out.NextAction = "You're signed in. Install the Yaver mobile app and sign in with the same account — your dev machine will show up in its device list."
		}
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
			snap := authStatusSnapshot()
			out.Status = "signed_in"
			out.UserEmail = snap.UserEmail
			out.Provider = snap.Provider
			out.DeviceID = snap.DeviceID
			out.SignInURL = ""
			out.UserCode = ""
			out.DeviceCode = ""
			out.ExpiresInSeconds = 0
			if snap.UserEmail != "" {
				out.NextAction = "Signed in as " + snap.UserEmail + ". Now install the Yaver mobile app (TestFlight for iPhone, Play Store for Android) and sign in with the same account — your machine will appear automatically."
			} else {
				out.NextAction = "Signed in. Now install the Yaver mobile app and sign in with the same account — your machine will appear automatically."
			}
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
