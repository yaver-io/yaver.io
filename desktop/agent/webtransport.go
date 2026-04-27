package main

import (
	"log"
	"strings"
	"sync"
	"time"
)

// webTransport tracks the lifecycle of a single web-bundle delivery from
// the moment compile completes through the iframe's "loaded" ack. Emits
// SSE events on `topic=webview/transport` so the dashboard CONSOLE can
// render a phase ladder for the post-compile pipeline:
//
//   compiled        — bundle written to .yaver-build-web/, manager has
//                     WebBundleInfo. The HTTP build-native response
//                     just landed on the caller.
//   ready_to_serve  — manifest of files+sizes prepped, transport ready
//                     to ship bytes. Same wall-clock as compiled today
//                     but a distinct phase so future async pre-warming
//                     can fit cleanly.
//   serving         — first GET to /dev/web-bundle/ has arrived. Iframe
//                     started fetching index.html.
//   streaming       — a file landed; running totals attached to progress
//                     payload. Throttled to ≤ 5 events/sec to stay
//                     polite to slow consumers.
//   delivered       — iframe POSTed /dev/web-bundle/ack {ms_to_load}.
//                     Bundle has fully loaded + Hermes/V8 evaluated it.
//   error           — iframe POSTed /dev/web-bundle/error {message,stack}
//                     OR a transport-level fault (file 404 / 5xx).
//
// Producer lives entirely on the agent. Consumers (web dashboard,
// future mobile-side surface) read dev/events and render the phase
// pills + progress bar from `done/total` byte counts.
type webTransport struct {
	emit    func(DevServerEvent)
	target  string // "web-js-bundle" | "web-hermes-wasm"
	caller  string // X-Yaver-Caller of the originating build-native call
	startAt time.Time

	mu             sync.Mutex
	phase          string
	totalBytes     int64
	totalFiles     int
	servedBytes    int64
	servedFiles    int
	manifest       map[string]int64 // path -> bytes from initial scan
	lastEmittedAt  time.Time
	emittedFinal   bool
	deliveredAt    time.Time
	failureMessage string
}

// newWebTransport returns a tracker pre-loaded with the bundle's full
// manifest so progress events can include real totals from event #1.
// The caller is responsible for calling close() when the bundle is
// superseded by a fresh build.
func newWebTransport(emit func(DevServerEvent), target, caller string, manifest map[string]int64) *webTransport {
	var totalBytes int64
	for _, b := range manifest {
		totalBytes += b
	}
	return &webTransport{
		emit:       emit,
		target:     target,
		caller:     caller,
		startAt:    time.Now(),
		manifest:   manifest,
		totalBytes: totalBytes,
		totalFiles: len(manifest),
		phase:      "compiled",
	}
}

// transition emits a phase change event and updates internal state.
// Idempotent on identical phase — won't double-fire.
func (t *webTransport) transition(phase string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	if t.phase == phase {
		t.mu.Unlock()
		return
	}
	t.phase = phase
	t.mu.Unlock()
	log.Printf("[web-bundle:transport] phase=%s target=%s caller=%s served=%d/%d files served_bytes=%d/%d",
		phase, t.target, t.caller, t.servedFiles, t.totalFiles, t.servedBytes, t.totalBytes)
	t.emit(DevServerEvent{
		Type:        "phase",
		Topic:       "webview/transport",
		Phase:       phase,
		Caller:      t.caller,
		Framework:   t.target,
		Timestamp:   time.Now().UTC().Format(time.RFC3339Nano),
	})
}

// recordFile is called by handleServeWebBundle on every served file. It
// updates running totals + emits a `progress` event under
// topic=webview/transport, throttled to ~5/sec. The first call for a
// new bundle also transitions phase compiled → serving.
func (t *webTransport) recordFile(rel string, bytes int64) {
	if t == nil {
		return
	}
	t.mu.Lock()
	if t.phase == "compiled" || t.phase == "ready_to_serve" {
		t.phase = "serving"
		// Emit serving phase synchronously so the dashboard sees the
		// transition the moment the iframe asks for index.html.
		go func(caller, target string) {
			t.emit(DevServerEvent{
				Type: "phase", Topic: "webview/transport",
				Phase: "serving", Caller: caller, Framework: target,
				Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
			})
		}(t.caller, t.target)
	}
	t.servedFiles++
	t.servedBytes += bytes

	// Per-file streaming progress event (throttled).
	now := time.Now()
	throttle := 200 * time.Millisecond
	emitNow := now.Sub(t.lastEmittedAt) >= throttle || t.servedFiles == 1 || t.servedFiles == t.totalFiles
	if emitNow {
		t.lastEmittedAt = now
	}
	pct := 0.0
	if t.totalBytes > 0 {
		pct = 100.0 * float64(t.servedBytes) / float64(t.totalBytes)
	}
	servedFiles := t.servedFiles
	servedBytes := t.servedBytes
	totalBytes := t.totalBytes
	totalFiles := t.totalFiles
	phase := t.phase
	t.mu.Unlock()

	if !emitNow {
		return
	}
	if phase != "serving" && phase != "streaming" {
		return
	}
	log.Printf("[web-bundle:transport] streaming file=%s bytes=%d cumulative=%d/%d files=%d/%d (%.1f%%)",
		truncatePath(rel), bytes, servedBytes, totalBytes, servedFiles, totalFiles, pct)
	t.emit(DevServerEvent{
		Type:        "progress",
		Topic:       "webview/transport",
		Phase:       "streaming",
		Caller:      t.caller,
		Framework:   t.target,
		Pct:         float32(pct),
		Done:        servedBytes,
		Total:       totalBytes,
		Unit:        "bytes",
		CurrentFile: truncatePath(rel),
		ProgressSrc: "exact",
		Timestamp:   time.Now().UTC().Format(time.RFC3339Nano),
	})
}

// markDelivered handles the iframe's "I loaded everything" ack. Idempotent.
func (t *webTransport) markDelivered(msToLoad int64) {
	if t == nil {
		return
	}
	t.mu.Lock()
	if t.phase == "delivered" || t.emittedFinal {
		t.mu.Unlock()
		return
	}
	t.deliveredAt = time.Now()
	t.phase = "delivered"
	t.emittedFinal = true
	t.mu.Unlock()
	log.Printf("[web-bundle:transport] delivered target=%s ms=%d files=%d bytes=%d",
		t.target, msToLoad, t.servedFiles, t.servedBytes)
	t.emit(DevServerEvent{
		Type:      "phase",
		Topic:     "webview/transport",
		Phase:     "delivered",
		Caller:    t.caller,
		Framework: t.target,
		EtaMs:     msToLoad,
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
	})
}

// markError handles iframe-reported JS errors and transport-level
// failures. Sets phase=error with the message; idempotent on subsequent
// calls (only the first message is recorded).
func (t *webTransport) markError(message string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	if t.failureMessage != "" {
		t.mu.Unlock()
		return
	}
	t.failureMessage = message
	t.phase = "error"
	t.emittedFinal = true
	t.mu.Unlock()
	log.Printf("[web-bundle:transport] error target=%s: %s", t.target, message)
	t.emit(DevServerEvent{
		Type:      "error",
		Topic:     "webview/transport",
		Phase:     "error",
		Caller:    t.caller,
		Framework: t.target,
		Message:   message,
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
	})
}

// snapshot copies the current state for the manager's snapshot loop.
func (t *webTransport) snapshot() WebTransportSnapshot {
	if t == nil {
		return WebTransportSnapshot{}
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return WebTransportSnapshot{
		Phase:       t.phase,
		Target:      t.target,
		Caller:      t.caller,
		ServedFiles: t.servedFiles,
		ServedBytes: t.servedBytes,
		TotalFiles:  t.totalFiles,
		TotalBytes:  t.totalBytes,
		StartedAt:   t.startAt.UTC().Format(time.RFC3339Nano),
	}
}

// WebTransportSnapshot is the JSON shape the manager's snapshot loop
// embeds for new SSE subscribers so a freshly-connected dashboard sees
// the in-flight bundle delivery without waiting for the next streaming
// event.
type WebTransportSnapshot struct {
	Phase       string `json:"phase,omitempty"`
	Target      string `json:"target,omitempty"`
	Caller      string `json:"caller,omitempty"`
	ServedFiles int    `json:"servedFiles,omitempty"`
	ServedBytes int64  `json:"servedBytes,omitempty"`
	TotalFiles  int    `json:"totalFiles,omitempty"`
	TotalBytes  int64  `json:"totalBytes,omitempty"`
	StartedAt   string `json:"startedAt,omitempty"`
}

// truncatePath shortens noisy long absolute paths for log lines and
// SSE payloads so the dashboard can render them without wrapping.
func truncatePath(p string) string {
	const max = 60
	if len(p) <= max {
		return p
	}
	// Keep the leaf — useful for distinguishing index.html from foo.js
	if i := strings.LastIndexByte(p, '/'); i >= 0 && i < len(p)-1 {
		leaf := p[i+1:]
		if len(leaf) <= max-3 {
			return ".../" + leaf
		}
	}
	return p[:max-3] + "..."
}
