package main

import (
	"net/http"
	"strings"
)

// X-Yaver-Caller convention.
//
// Every Yaver client (mobile app, web dashboard, CLI, desktop Electron,
// SDK feedback) SHOULD send `X-Yaver-Caller: <surface>/<version>` on every
// agent-bound request. The agent threads this into:
//
//   - Per-line server logs:
//       [dev:build-native] caller=mobile-app/1.18.15 target=ios workdir=...
//   - Every emitted DevServerEvent (the SSE stream):
//       { "type": "phase", "caller": "web-dashboard/1.1.81", "topic": "webview/build", ... }
//   - Audit / activity logs.
//
// Browsers can't set custom headers on EventSource. The web dashboard
// therefore also accepts `?caller=` as a fallback query param; the relay
// preserves it. extractCaller looks at both.
//
// Header / query value is free-form text limited to 64 chars and
// printable ASCII; we strip / lowercase nothing — the caller knows
// best how to identify itself. The slash separator is convention,
// not enforced.
//
// Recognized surface prefixes (informational; new ones are accepted as
// raw strings without rejection):
//
//   mobile-app/<v>     — the Yaver iOS / Android app
//   web-dashboard/<v>  — yaver.io/dashboard
//   cli/<v>            — `yaver` Go binary subcommands
//   desktop/<v>        — Electron desktop app
//   sdk-feedback/<v>   — Feedback SDK runtime (RN, Web, Flutter)
//   support/<v>        — Remote Support REPL
//   unknown            — request didn't carry the header

const (
	callerHeader      = "X-Yaver-Caller"
	callerQueryParam  = "caller"
	callerMaxLen      = 64
	callerUnknown     = "unknown"
	callerCallerField = "caller" // JSON field name when threaded into events
)

// extractCaller returns the cleaned caller surface string from the
// request, or "unknown" if the request didn't supply one. Never returns
// the empty string.
func extractCaller(r *http.Request) string {
	if r == nil {
		return callerUnknown
	}
	if v := strings.TrimSpace(r.Header.Get(callerHeader)); v != "" {
		return clampCaller(v)
	}
	if r.URL != nil {
		if v := strings.TrimSpace(r.URL.Query().Get(callerQueryParam)); v != "" {
			return clampCaller(v)
		}
	}
	return callerUnknown
}

// clampCaller bounds the value at callerMaxLen and strips control bytes
// so a malicious caller can't spam log lines with newlines / ANSI.
func clampCaller(v string) string {
	if len(v) > callerMaxLen {
		v = v[:callerMaxLen]
	}
	var b strings.Builder
	b.Grow(len(v))
	for _, c := range v {
		if c < 0x20 || c == 0x7f {
			continue
		}
		b.WriteRune(c)
	}
	out := strings.TrimSpace(b.String())
	if out == "" {
		return callerUnknown
	}
	return out
}
