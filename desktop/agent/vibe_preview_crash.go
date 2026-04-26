package main

// vibe_preview_crash.go — Phase 4.5 crash-awareness.
//
// When a framework crashes (Flutter exception, RN red-box, native
// FATAL EXCEPTION, Xcode crash report) during an active vibe-preview
// session, we need to:
//   1. Tag the most-recent frame so the mobile UI shows a red badge.
//   2. Emit a "crash" SSE event so subscribers update timelines.
//   3. Cut short any in-progress clip recording and mark it
//      "captured_during_crash" so the consumer knows the MP4 is the
//      crash itself, not the success demo.
//
// Wiring to actual log streams (DevServer events, BlackBox sessions,
// adb logcat, xcrun simctl log) is intentionally NOT here — that's
// the smart-develop-mode work in task #14. This file is the receive
// surface. Anything that detects a crash calls OnCrashDetected and
// the manager fans the rest out.

import (
	"context"
	"regexp"
	"strings"
	"time"
)

// VibeCrashSignal is what callers pass to OnCrashDetected. Source is
// free-form ("flutter", "rn-redbox", "android-logcat", "ios-crashreport",
// etc.) so future log scrapers can self-identify in the SSE stream.
type VibeCrashSignal struct {
	Project   string
	Source    string
	Message   string
	Snippet   string // optional verbatim log line(s) — first ~512 bytes
	Timestamp time.Time
}

// OnCrashDetected is the public entrypoint. Idempotent on the same
// (project, msg, ts) tuple within a 1 s window so duplicate scrapers
// don't double-count. Returns the most-recent frame's hash if any (so
// the caller can include it in their own logs).
func (m *VibePreviewManager) OnCrashDetected(sig VibeCrashSignal) string {
	if m == nil || strings.TrimSpace(sig.Project) == "" {
		return ""
	}
	if sig.Timestamp.IsZero() {
		sig.Timestamp = m.nowFn()
	}
	if sig.Source == "" {
		sig.Source = "unknown"
	}

	// Coalesce duplicates: identical message in last 1 s = same crash.
	m.mu.Lock()
	if m.lastCrash != nil &&
		m.lastCrash.Project == sig.Project &&
		m.lastCrash.Message == sig.Message &&
		sig.Timestamp.Sub(m.lastCrash.Timestamp) < time.Second {
		m.mu.Unlock()
		return ""
	}
	m.lastCrash = &VibeCrashSignal{
		Project:   sig.Project,
		Source:    sig.Source,
		Message:   sig.Message,
		Timestamp: sig.Timestamp,
	}

	// Annotate most-recent frame so the modal can decorate it.
	var taggedHash string
	ring := m.ring[sig.Project]
	if len(ring) > 0 {
		ring[len(ring)-1].crashTagged = true
		taggedHash = ring[len(ring)-1].Hash
	}
	// Snapshot the active clip IDs for this project so we can stop them
	// without the lock held.
	var activeClipIDs []string
	for _, c := range m.clips[sig.Project] {
		if c.Status == "recording" {
			activeClipIDs = append(activeClipIDs, c.ID)
		}
	}
	m.mu.Unlock()

	// Stop any in-flight clips so we don't keep recording past the
	// crash. Best-effort — race against the recorder is fine, the
	// finalize step in vibe_preview_clip.go marks status=ready when the
	// process exits regardless of the cause.
	for _, id := range activeClipIDs {
		_ = m.StopClip(id)
		// And re-tag the clip record so the mobile UI knows.
		if rec := m.ClipByID(id); rec != nil {
			rec.Status = "captured_during_crash"
			rec.Err = sig.Message
			m.RegisterClip(sig.Project, rec)
		}
	}

	snippet := sig.Snippet
	if len(snippet) > 512 {
		snippet = snippet[:512] + "…"
	}
	m.emit(sig.Project, VibePreviewEvent{
		Type:    "crash",
		Project: sig.Project,
		Source:  sig.Source,
		Hash:    taggedHash,
		Message: sig.Message,
	})
	return taggedHash
}

// ─── Log-scraper regexes ─────────────────────────────────────────────────────
//
// Public so the smart-develop-mode work (task #14) can plug them into
// adb logcat / xcrun simctl log / DevServer event subscribers without
// re-deriving the patterns. Order matters — first match wins.

var vibeCrashPatterns = []struct {
	source string
	re     *regexp.Regexp
}{
	// Flutter widget framework exception
	{source: "flutter", re: regexp.MustCompile(`(?m)Exception caught by widgets library|FlutterError\.reportError|StateError:|Error: TypeError:`)},
	// React Native red-box / unhandled JS exception
	{source: "rn-redbox", re: regexp.MustCompile(`(?m)RedBox|Unhandled JS Exception|TypeError:.*\(.*\.bundle.*\)`)},
	// Android native fatal
	{source: "android-fatal", re: regexp.MustCompile(`(?m)\bFATAL EXCEPTION\b|AndroidRuntime: FATAL|Process .* has died`)},
	// iOS native crash / NSException
	{source: "ios-crash", re: regexp.MustCompile(`(?m)\*\*\* Terminating app due to uncaught exception|Thread \d+ Crashed:|libsystem_kernel\.dylib.*__pthread_kill`)},
	// Generic Node/server exceptions (Vite/Next dev server)
	{source: "node", re: regexp.MustCompile(`(?m)UnhandledPromiseRejection|^Error: .*\n\s+at `)},
}

// VibeStabilityResult is what WaitForStability returns: whether the
// project stayed crash-free over the window and (if not) which crash
// fired first. The autodev "smart develop mode" loop reads this after
// every kick to decide whether to declare the kick done or re-queue
// it with the crash transcript appended.
type VibeStabilityResult struct {
	Stable bool             // true = no crash arrived during the window
	Crash  *VibeCrashSignal // populated when Stable=false
	Window time.Duration    // how long we actually waited (caps below)
}

// WaitForStability blocks for `window` (default 8 s, max 60 s) watching
// for crash events on the project's SSE channel. Returns stable=true
// if the window passes without a crash; stable=false the moment a
// crash event arrives. Either way, returns immediately on context
// cancellation.
//
// Designed to be called by the autodev post-kick gate (task #14) but
// useful in any "is this app actually still alive after I touched it?"
// flow. Cheap — just a channel read with a timer, no polling.
func (m *VibePreviewManager) WaitForStability(ctx context.Context, project string, window time.Duration) VibeStabilityResult {
	if m == nil || project == "" {
		return VibeStabilityResult{Stable: true}
	}
	if window <= 0 {
		window = 8 * time.Second
	}
	if window > 60*time.Second {
		window = 60 * time.Second
	}

	ch, _, unsubscribe := m.Subscribe(project)
	defer unsubscribe()

	deadline := time.NewTimer(window)
	defer deadline.Stop()

	for {
		select {
		case <-ctx.Done():
			return VibeStabilityResult{Stable: true, Window: window}
		case <-deadline.C:
			return VibeStabilityResult{Stable: true, Window: window}
		case ev, open := <-ch:
			if !open {
				// Manager closed the subscriber (Stop was called) —
				// treat that as stable; the session ended cleanly.
				return VibeStabilityResult{Stable: true, Window: window}
			}
			if ev.Type == "crash" {
				m.mu.Lock()
				lc := m.lastCrash
				m.mu.Unlock()
				return VibeStabilityResult{
					Stable: false,
					Crash:  lc,
					Window: window,
				}
			}
			// Any other event (frame / stable / clip_*) is fine — keep
			// listening. The loop continues until window expires or a
			// crash arrives.
		}
	}
}

// MatchVibeCrashLine returns the source name + a normalised message if the
// supplied log line(s) look like a crash signature. Empty source = no match.
// Callers pass full log chunks (could be multi-line), not single bytes.
func MatchVibeCrashLine(text string) (source, message string) {
	for _, p := range vibeCrashPatterns {
		if loc := p.re.FindStringIndex(text); loc != nil {
			// Trim the matched substring + a tiny window of context.
			start := loc[0]
			end := loc[1]
			if end-start < 200 && len(text)-end > 50 {
				end += 50
			}
			snippet := strings.TrimSpace(text[start:end])
			return p.source, snippet
		}
	}
	return "", ""
}
