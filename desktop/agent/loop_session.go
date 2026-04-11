package main

// loop_session.go — per-provider session-window tracking for Auto
// Dev. The rule it enforces: "don't burn my Claude Code 5-hour
// window on Auto Dev while I'm trying to work at 3pm."
//
// How it works:
//
//  1. Every kick records its wall-clock duration under the active
//     runner in ~/.yaver/loops/<name>/session_usage.json.
//  2. Before the next kick, runDevelopLoop / runAutoTestLoop call
//     pickRunnerWithinLimits which:
//       - expires stale windows (current time > window start +
//         ProviderLimits.SessionWindow),
//       - sums usage inside the live window,
//       - picks the primary runner if it's under soft_cap_percent,
//       - otherwise walks think.fallback in order and returns the
//         first runner that still has headroom,
//       - or returns ("", false) meaning "every provider is over
//         its cap, yield the loop with status=budget_hit."
//
// This file is deliberately file-based (not a goroutine / channel)
// so the scheduler's subprocess ticks can resume where the previous
// process left off. Every function is cheap to call on every kick.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// providerUsage is the persisted counter for one runner.
type providerUsage struct {
	// UsedSeconds is wall-clock seconds charged to the runner inside
	// the current window.
	UsedSeconds int `json:"usedSeconds"`
	// WindowStartedAt is the RFC3339 timestamp of the first kick
	// inside the current window. Rolls forward when the window
	// expires.
	WindowStartedAt string `json:"windowStartedAt"`
}

type sessionUsageFile struct {
	Providers map[string]*providerUsage `json:"providers"`
}

var sessionUsageMu sync.Mutex

// sessionUsagePath returns the on-disk path for a loop's usage file.
func sessionUsagePath(loopName string) (string, error) {
	base, err := ConfigDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(base, "loops", loopName)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	return filepath.Join(dir, "session_usage.json"), nil
}

// loadSessionUsage reads the loop's counter file, returning an empty
// map if the file doesn't exist yet.
func loadSessionUsage(loopName string) (*sessionUsageFile, error) {
	p, err := sessionUsagePath(loopName)
	if err != nil {
		return nil, err
	}
	data, rerr := os.ReadFile(p)
	if rerr != nil {
		if os.IsNotExist(rerr) {
			return &sessionUsageFile{Providers: map[string]*providerUsage{}}, nil
		}
		return nil, rerr
	}
	var f sessionUsageFile
	if jerr := json.Unmarshal(data, &f); jerr != nil {
		return nil, jerr
	}
	if f.Providers == nil {
		f.Providers = map[string]*providerUsage{}
	}
	return &f, nil
}

// saveSessionUsage writes the counter file atomically.
func saveSessionUsage(loopName string, f *sessionUsageFile) error {
	p, err := sessionUsagePath(loopName)
	if err != nil {
		return err
	}
	data, jerr := json.MarshalIndent(f, "", "  ")
	if jerr != nil {
		return jerr
	}
	tmp := p + ".tmp"
	if werr := os.WriteFile(tmp, data, 0600); werr != nil {
		return werr
	}
	return os.Rename(tmp, p)
}

// rollExpiredWindows zeroes out any provider whose window ran out,
// so the next kick starts counting fresh. Called every time we
// touch the counter file so the state is always consistent.
func rollExpiredWindows(f *sessionUsageFile) {
	now := time.Now().UTC()
	for runner, usage := range f.Providers {
		if usage.WindowStartedAt == "" {
			continue
		}
		start, perr := time.Parse(time.RFC3339, usage.WindowStartedAt)
		if perr != nil {
			// Corrupt timestamp — treat as expired.
			usage.UsedSeconds = 0
			usage.WindowStartedAt = ""
			continue
		}
		lim := defaultProviderLimits(runner)
		if lim.SessionWindow == "" {
			continue // unlimited (e.g. local ollama)
		}
		window, derr := time.ParseDuration(lim.SessionWindow)
		if derr != nil || window <= 0 {
			continue
		}
		if now.After(start.Add(window)) {
			usage.UsedSeconds = 0
			usage.WindowStartedAt = ""
		}
	}
}

// runnerBudgetState reports whether a runner is currently over its
// soft cap. Used both for the primary-runner check and for deciding
// whether a fallback is worth trying.
func runnerBudgetState(f *sessionUsageFile, runner string) (used, cap int, overCap bool) {
	usage := f.Providers[runner]
	lim := defaultProviderLimits(runner)
	if lim.SessionWindow == "" {
		// Unlimited provider — never over cap.
		return 0, 0, false
	}
	window, derr := time.ParseDuration(lim.SessionWindow)
	if derr != nil || window <= 0 {
		return 0, 0, false
	}
	softPct := lim.SoftCapPercent
	if softPct <= 0 || softPct > 100 {
		softPct = 80
	}
	cap = int(window.Seconds()) * softPct / 100
	if usage != nil {
		used = usage.UsedSeconds
	}
	overCap = used >= cap
	return used, cap, overCap
}

// pickRunnerWithinLimits returns the runner the next kick should
// use. If the loop's primary runner has headroom, that's it. If
// not, we walk think.fallback for the first runner that still
// fits. Returns ("", false) when every option is over cap — the
// caller should terminate with budget_hit.
//
// The reason string is surfaced in the loop's LastSummary so the
// dev on the phone sees "Claude 4h 20m over cap — fell back to
// codex" instead of an unexplained runner swap.
func pickRunnerWithinLimits(loopName string, think LoopThink) (runner, reason string, ok bool) {
	if think.RespectSessionLimits != nil && !*think.RespectSessionLimits {
		return think.Runner, "", true
	}

	sessionUsageMu.Lock()
	defer sessionUsageMu.Unlock()

	f, err := loadSessionUsage(loopName)
	if err != nil {
		// If we can't read the counter file, fail open — run the
		// kick anyway with the primary runner. An I/O error here
		// should not stall the whole loop.
		return think.Runner, "", true
	}
	rollExpiredWindows(f)
	_ = saveSessionUsage(loopName, f) // persist any expired resets

	// Build the ordered candidate list: primary + fallback entries.
	candidates := []string{think.Runner}
	for _, fb := range think.Fallback {
		fb = strings.TrimSpace(fb)
		if fb == "" || fb == think.Runner {
			continue
		}
		candidates = append(candidates, fb)
	}

	for i, cand := range candidates {
		used, cap, over := runnerBudgetState(f, cand)
		if !over {
			if i == 0 {
				return cand, "", true
			}
			return cand, fmt.Sprintf("primary %q at %ds/%ds — falling back to %q",
				think.Runner, used, cap, cand), true
		}
	}
	return "", fmt.Sprintf("every configured runner is over cap (%s + fallbacks)",
		think.Runner), false
}

// recordKickUsage charges a kick's wall-clock duration to the
// named runner and persists the counter. Called from runSingleKick
// (and runAutoTestLoop) after each kick with the elapsed time.
// If the runner has no active window, this kick starts a new one.
func recordKickUsage(loopName, runner string, dur time.Duration) {
	if loopName == "" || runner == "" || dur <= 0 {
		return
	}
	sessionUsageMu.Lock()
	defer sessionUsageMu.Unlock()

	f, err := loadSessionUsage(loopName)
	if err != nil {
		return
	}
	rollExpiredWindows(f)

	usage := f.Providers[runner]
	if usage == nil {
		usage = &providerUsage{}
		f.Providers[runner] = usage
	}
	if usage.WindowStartedAt == "" {
		usage.WindowStartedAt = time.Now().UTC().Format(time.RFC3339)
	}
	usage.UsedSeconds += int(dur.Seconds())

	_ = saveSessionUsage(loopName, f)
}
