package main

// devserver_progress.go — parse Metro / Expo / Webpack / Hermesc stdout
// into structured DevServerEvent progress messages. The whole point of
// this file: end users should NEVER feel disconnected. Metro prints
// real progress; we extract it and surface it instead of leaving the
// UI to fake a wallclock-based progress bar. When the underlying tool
// is silent, the agent still emits a snapshot every 5s so the UI has
// SOMETHING to render.
//
// Wire format additions on DevServerEvent (all backwards-compat,
// omitempty):
//
//   Type:        "progress" | "phase" | "snapshot"  (plus existing types)
//   Phase:       "queued" | "preparing" | "installing_deps" | "starting"
//                | "metro_bundling" | "hermesc_compiling" | "validating"
//                | "web_bundling" | "listening" | "ready" | "idle"
//                | "stopped" | "error"
//   Topic:       "dev/start" | "webview/build" | "hermes/compile" | "bundle/push"
//   Pct:         0..100 (real, parsed from tool output)
//   Done, Total: e.g. modules count
//   Unit:        "modules" | "bytes" | "files" | "tasks"
//   CurrentFile: e.g. "node_modules/expo-router/build/Route.js"
//   ProgressSrc: "exact" | "heuristic" | "unknown" — UI uses this to
//                decide whether to render an indeterminate spinner
//                (UNKNOWN) or a real progress bar.
//   EtaMs:       est remaining millis, only when ProgressSrc == "exact"
//
// New phase + progress events flow through DevServerManager.emit() so
// they hit history (Subscribe replays) AND live subscribers AND get
// captured in the recent_log_tail of the next snapshot.

import (
	"bufio"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Regex bank — match what the actual tools emit.
//
// Metro (Expo SDK 51+) prints during a bundle, e.g.
//   "iOS Bundling 67.3% (1247/2390)"
//   "Web Bundling 24% (300/1234)"
//   "Web Bundling complete 5678ms"
// Bare React Native CLI emits
//   "Bundling: [============>            ]  68% (1547/2274)"
// Webpack-via-Expo-Web emits
//   "  ⠼ Compiling 42 of 1234 modules"
// Hermesc with --profile emits
//   "Generating bytecode: 5384192 bytes / 7842816 bytes"
//
// We intentionally accept multiple shapes and converge on a single
// canonical Progress event — the consumer sees one shape regardless
// of which tool is on the wire.
var (
	rxMetroPct = regexp.MustCompile(
		// "iOS Bundling 67.3% (1247/2390)" or "Web Bundling 12% (3/100)"
		`(?:iOS|Android|Web)\s+Bundling\s+([\d.]+)%\s+\((\d+)\/(\d+)\)`,
	)
	rxBareRNPct = regexp.MustCompile(
		// "Bundling: [============>            ]  68% (1547/2274)"
		`Bundling:?\s*\[[^\]]*\]\s*([\d.]+)%\s+\((\d+)\/(\d+)\)`,
	)
	rxExpoSpinner = regexp.MustCompile(
		// "⠼ Compiling 42 of 1234 modules"
		`Compiling\s+(\d+)\s+of\s+(\d+)\s+modules`,
	)
	rxHermescBytes = regexp.MustCompile(
		`Generating\s+bytecode:?\s+(\d+)\s+bytes\s*\/\s*(\d+)\s+bytes`,
	)
	rxBundleCurrentFile = regexp.MustCompile(
		// "Transforming /Users/foo/proj/node_modules/react/index.js"
		// or "(node_modules/react-native/Libraries/Foo.js)"
		`(?:Transforming|\()(\/?[a-zA-Z0-9_./@\-]+\.(?:tsx?|jsx?|mjs|cjs))(?:\)|\s|$)`,
	)
	rxBundleComplete = regexp.MustCompile(
		// "iOS Bundling complete 5678ms" or "Web Bundling complete 12345ms"
		`(iOS|Android|Web)\s+Bundling\s+complete\s+(\d+)\s*ms`,
	)
	rxMetroReady = regexp.MustCompile(
		// "Waiting on http://localhost:8081"
		// "Web is waiting on http://localhost:19006"
		`(?:Web\s+is\s+)?[Ww]aiting\s+on\s+https?:\/\/[^\s]+:(\d+)`,
	)
	// "Starting Metro Bundler" — phase change
	rxStartingMetro = regexp.MustCompile(`(?i)Starting\s+Metro\s+Bundler`)
)

// progressTracker is owned by a baseDevServer; it parses log lines as
// they come off Metro/Expo and emits structured progress + phase
// events to the manager. One tracker per running dev server. When
// idle (no progress lines for >2s), it stops emitting deltas — but
// the manager's 5s snapshot loop still keeps the consumer informed.
type progressTracker struct {
	emit       func(DevServerEvent)
	framework  string
	topic      string // "dev/start" | "webview/build" | "hermes/compile"
	surface    string // "hot-reload" | "web-reload"

	mu              sync.Mutex
	currentPhase    string
	currentPct      float32
	currentDone     int
	currentTotal    int
	currentUnit     string
	currentFile     string
	currentSrc      string
	phaseStartedAt  time.Time
	phaseStartedMs  int64
	rateModulesPerS float32
	lastRateAt      time.Time
	lastRateDone    int
	emitMu          sync.Mutex
	lastEmitAt      time.Time
	lastEmitPct     float32
}

func newProgressTracker(emit func(DevServerEvent), framework, topic, surface string) *progressTracker {
	return &progressTracker{
		emit:           emit,
		framework:      framework,
		topic:          topic,
		surface:        surface,
		currentSrc:     "unknown",
		phaseStartedAt: time.Now(),
	}
}

// FeedLine is called for every stdout/stderr line from the dev server.
// Emits at most ~5 events per second to avoid SSE flood; consumer reads
// snapshot every 5s anyway for ground truth.
func (p *progressTracker) FeedLine(line string) {
	if line == "" {
		return
	}

	// Phase transitions before regex hits — these tell us where we
	// are even when the percent regex hasn't matched yet.
	if rxStartingMetro.MatchString(line) {
		p.transitionPhase("metro_bundling")
		return
	}
	if m := rxMetroReady.FindStringSubmatch(line); m != nil {
		p.transitionPhase("listening")
		return
	}
	if m := rxBundleComplete.FindStringSubmatch(line); m != nil {
		// platform := m[1]
		p.transitionPhase("ready")
		// Final 100% emission so the bar lands cleanly.
		p.mu.Lock()
		p.currentPct = 100
		p.currentDone = p.currentTotal
		p.currentSrc = "exact"
		p.mu.Unlock()
		p.emitProgress(true /*force*/)
		return
	}

	// Real progress — try Metro/Expo formats first, then bare RN, then
	// the legacy spinner, finally hermesc.
	if m := rxMetroPct.FindStringSubmatch(line); m != nil {
		pct := atofSafe(m[1])
		done := atoiSafe(m[2])
		total := atoiSafe(m[3])
		p.recordProgress(pct, done, total, "modules", "exact", line)
		return
	}
	if m := rxBareRNPct.FindStringSubmatch(line); m != nil {
		pct := atofSafe(m[1])
		done := atoiSafe(m[2])
		total := atoiSafe(m[3])
		p.recordProgress(pct, done, total, "modules", "exact", line)
		return
	}
	if m := rxExpoSpinner.FindStringSubmatch(line); m != nil {
		done := atoiSafe(m[1])
		total := atoiSafe(m[2])
		var pct float32
		if total > 0 {
			pct = float32(done) * 100 / float32(total)
		}
		p.recordProgress(pct, done, total, "modules", "exact", line)
		return
	}
	if m := rxHermescBytes.FindStringSubmatch(line); m != nil {
		done := atoiSafe(m[1])
		total := atoiSafe(m[2])
		var pct float32
		if total > 0 {
			pct = float32(done) * 100 / float32(total)
		}
		p.recordProgress(pct, done, total, "bytes", "exact", line)
		return
	}

	// Note current file being transformed even when no pct is present.
	if m := rxBundleCurrentFile.FindStringSubmatch(line); m != nil {
		p.mu.Lock()
		p.currentFile = m[1]
		p.mu.Unlock()
		// Don't emit just for a current-file change unless it's been
		// >500ms since last emit — too chatty otherwise.
		p.emitProgressIfStale(500 * time.Millisecond)
	}
}

// transitionPhase fires a "phase" event. Idempotent — same phase twice
// is a no-op. Phase-end times are filled in by the next transition's
// "ts" so the consumer can compute durations.
func (p *progressTracker) transitionPhase(phase string) {
	p.mu.Lock()
	prev := p.currentPhase
	if prev == phase {
		p.mu.Unlock()
		return
	}
	p.currentPhase = phase
	p.phaseStartedAt = time.Now()
	p.phaseStartedMs = time.Since(p.phaseStartedAt).Milliseconds()
	// Reset rate so the next chunk's eta is recomputed from scratch.
	p.rateModulesPerS = 0
	p.lastRateAt = time.Time{}
	p.lastRateDone = 0
	p.mu.Unlock()

	if p.emit != nil {
		p.emit(DevServerEvent{
			Type:      "phase",
			Topic:     p.topic,
			Phase:     phase,
			PrevPhase: prev,
			Framework: p.framework,
			Surface:   p.surface,
			Timestamp: time.Now().UTC().Format(time.RFC3339),
		})
	}
}

// recordProgress updates the cached state and may emit an event.
func (p *progressTracker) recordProgress(pct float32, done, total int, unit, src, sourceLine string) {
	p.mu.Lock()
	p.currentPct = pct
	p.currentDone = done
	p.currentTotal = total
	p.currentUnit = unit
	p.currentSrc = src
	// Update rate (modules/sec) for ETA estimate.
	now := time.Now()
	if !p.lastRateAt.IsZero() {
		dt := now.Sub(p.lastRateAt).Seconds()
		if dt > 0.5 && done > p.lastRateDone {
			p.rateModulesPerS = float32(done-p.lastRateDone) / float32(dt)
		}
	}
	p.lastRateAt = now
	p.lastRateDone = done
	// Phase: if we got progress and we're not in a known compiling
	// phase, transition to metro_bundling (most common case).
	if p.currentPhase == "" || p.currentPhase == "queued" || p.currentPhase == "preparing" || p.currentPhase == "starting" {
		p.currentPhase = "metro_bundling"
	}
	p.mu.Unlock()
	p.emitProgress(false)
}

// emitProgress sends a "progress" event respecting throttle (200ms or 5pct).
func (p *progressTracker) emitProgress(force bool) {
	p.emitMu.Lock()
	defer p.emitMu.Unlock()
	now := time.Now()
	if !force {
		if !p.lastEmitAt.IsZero() && now.Sub(p.lastEmitAt) < 200*time.Millisecond {
			// Too soon, skip — but only if pct also didn't change much.
			p.mu.Lock()
			pctDelta := p.currentPct - p.lastEmitPct
			p.mu.Unlock()
			if pctDelta < 5 && pctDelta > -5 {
				return
			}
		}
	}
	p.mu.Lock()
	pct := p.currentPct
	done := p.currentDone
	total := p.currentTotal
	unit := p.currentUnit
	currentFile := p.currentFile
	src := p.currentSrc
	phase := p.currentPhase
	rate := p.rateModulesPerS
	p.mu.Unlock()

	var etaMs int64 = 0
	if src == "exact" && rate > 0 && total > done {
		remaining := total - done
		etaMs = int64(float32(remaining)/rate) * 1000
	}

	if p.emit != nil {
		p.emit(DevServerEvent{
			Type:        "progress",
			Topic:       p.topic,
			Phase:       phase,
			Framework:   p.framework,
			Surface:     p.surface,
			Pct:         pct,
			Done:        done,
			Total:       total,
			Unit:        unit,
			CurrentFile: currentFile,
			ProgressSrc: src,
			EtaMs:       etaMs,
			Timestamp:   time.Now().UTC().Format(time.RFC3339),
		})
	}
	p.lastEmitAt = now
	p.lastEmitPct = pct
}

// emitProgressIfStale only emits if it's been longer than `since` since the
// last emit — used for current-file updates that shouldn't flood SSE.
func (p *progressTracker) emitProgressIfStale(since time.Duration) {
	p.emitMu.Lock()
	stale := p.lastEmitAt.IsZero() || time.Since(p.lastEmitAt) > since
	p.emitMu.Unlock()
	if stale {
		p.emitProgress(false)
	}
}

// Snapshot returns the current tracker state for the manager's 5s
// snapshot ticker. Includes derived fields (eta, elapsed).
type ProgressSnapshot struct {
	Phase       string  `json:"phase"`
	Topic       string  `json:"topic"`
	Pct         float32 `json:"pct,omitempty"`
	Done        int     `json:"done,omitempty"`
	Total       int     `json:"total,omitempty"`
	Unit        string  `json:"unit,omitempty"`
	CurrentFile string  `json:"currentFile,omitempty"`
	ProgressSrc string  `json:"progressSrc,omitempty"`
	EtaMs       int64   `json:"etaMs,omitempty"`
	ElapsedMs   int64   `json:"elapsedMs,omitempty"`
}

func (p *progressTracker) Snapshot() ProgressSnapshot {
	p.mu.Lock()
	defer p.mu.Unlock()
	return ProgressSnapshot{
		Phase:       p.currentPhase,
		Topic:       p.topic,
		Pct:         p.currentPct,
		Done:        p.currentDone,
		Total:       p.currentTotal,
		Unit:        p.currentUnit,
		CurrentFile: p.currentFile,
		ProgressSrc: p.currentSrc,
		ElapsedMs:   time.Since(p.phaseStartedAt).Milliseconds(),
	}
}

// FeedReader is a convenience wrapper for cmd.Stdout/Stderr — caller
// can also use FeedLine directly via devLogWriter.onLogLine.
func (p *progressTracker) FeedReader(prefix string) func(string) {
	return func(line string) {
		// Many tools emit a carriage-return-rewrite line for spinners;
		// our devLogWriter splits on \n only, so most spinner updates
		// are lost. That's acceptable — the regex still captures the
		// final line per chunk. If we ever switch to \r-aware splitting
		// we'll get even smoother progress.
		_ = prefix
		_ = bufio.Scanner{}
		p.FeedLine(line)
	}
}

func atoiSafe(s string) int {
	n, _ := strconv.Atoi(strings.TrimSpace(s))
	return n
}

func atofSafe(s string) float32 {
	f, _ := strconv.ParseFloat(strings.TrimSpace(s), 32)
	return float32(f)
}
