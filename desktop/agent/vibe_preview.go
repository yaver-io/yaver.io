package main

// vibe_preview.go — VibePreviewManager: live screenshot stream of a remote
// dev server, viewable from the mobile app while vibe-coding.
//
// Distinct from preview.go (PreviewManager), which deploys git-worktree
// branch previews on a chosen port. This subsystem captures the rendered
// output of an already-running dev server at FPS, so a phone-side modal
// can watch the UI change as the AI runner edits the codebase.
//
// Phase 1: in-memory ringbuffer, no HTTP exposure beyond start/stop/status,
// no summary pipeline. Frames are captured via BrowserManager.captureState
// and stored as raw PNG bytes; a stderr log line is emitted per capture so
// integration tests can verify the loop without parsing SSE.

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

// ─── Profile ──────────────────────────────────────────────────────────────────

// VibePreviewProfile selects FPS / resolution / quality for a session.
// Resolution + quality are honoured in Phase 2 (JPEG transcode); Phase 1
// only acts on FPS.
type VibePreviewProfile struct {
	Name    string  `json:"name"`
	FPS     float64 `json:"fps"`
	Width   int     `json:"width"`
	Height  int     `json:"height"`
	Quality int     `json:"quality"`    // JPEG 1-100; 0 = keep PNG
	MaxKB   int     `json:"maxFrameKB"` // throttle target; 0 = unbounded
}

// vibePreviewProfiles is the set of named profiles. Selection rules in
// ProfileFor: explicit name wins; otherwise pick from the netMode hint;
// fall back to "live-relay-wifi".
var vibePreviewProfiles = map[string]VibePreviewProfile{
	"live-direct":     {Name: "live-direct", FPS: 8, Width: 1280, Height: 720, Quality: 75, MaxKB: 300},
	"live-relay-wifi": {Name: "live-relay-wifi", FPS: 4, Width: 1280, Height: 720, Quality: 60, MaxKB: 200},
	"live-relay-cell": {Name: "live-relay-cell", FPS: 2, Width: 854, Height: 480, Quality: 50, MaxKB: 80},
	"change-only":     {Name: "change-only", FPS: 0, Width: 1280, Height: 720, Quality: 70, MaxKB: 250},
	"summary-only":    {Name: "summary-only", FPS: 0, Width: 854, Height: 480, Quality: 55, MaxKB: 100},
}

// ProfileFor resolves a profile name + netMode hint to a concrete profile.
// netMode values: "direct", "relay-wifi", "relay-cell". Empty = wifi.
func ProfileFor(name, netMode string) VibePreviewProfile {
	if name != "" {
		if p, ok := vibePreviewProfiles[name]; ok {
			return p
		}
	}
	switch netMode {
	case "direct":
		return vibePreviewProfiles["live-direct"]
	case "relay-cell", "cellular", "cell":
		return vibePreviewProfiles["live-relay-cell"]
	default:
		return vibePreviewProfiles["live-relay-wifi"]
	}
}

// ─── Session + frame record ──────────────────────────────────────────────────

// VibePreviewMode controls capture cadence semantics.
//
//	live         — capture at profile.FPS continuously
//	change-only  — capture only when an external trigger fires (Phase 2+)
//	summary-only — capture only for before/after diffs (Phase 4+)
type VibePreviewMode string

const (
	VibePreviewModeLive        VibePreviewMode = "live"
	VibePreviewModeChangeOnly  VibePreviewMode = "change-only"
	VibePreviewModeSummaryOnly VibePreviewMode = "summary-only"
)

// VibePreviewSession is a single active preview, one per (project, target).
type VibePreviewSession struct {
	ID         string             `json:"id"`
	Project    string             `json:"project"`
	TargetURL  string             `json:"targetUrl"`
	BrowserID  string             `json:"browserId"`
	Mode       VibePreviewMode    `json:"mode"`
	Profile    VibePreviewProfile `json:"profile"`
	StartedAt  time.Time          `json:"startedAt"`
	LastFrame  time.Time          `json:"lastFrame"`
	FrameCount uint64             `json:"frameCount"`
	StableHits uint64             `json:"stableHits"` // captures that hashed identical to prior
	Errors     uint64             `json:"errors"`

	// runtime
	cancel  context.CancelFunc
	stopped atomic.Bool
}

// vibeFrameRecord is one entry in the per-session ringbuffer.
// PNG bytes are kept in memory for the most-recent few frames so cold
// re-subscribers can serve a frame without disk I/O; older frames evict
// to disk-only and are read back on /frames/:hash GET.
type vibeFrameRecord struct {
	Seq         uint64
	Hash        string // first 12 hex of sha256(bytes)
	Bytes       []byte // PNG (chromedp default) — may be cleared after persist
	Width       int
	Height      int
	CapturedAt  time.Time
	diskPath    string // ~/.yaver/vibe-preview/<sessionId>/<hash>.png; "" = not persisted
	crashTagged bool   // set by OnCrashDetected — surfaced in /frames/<hash> headers
}

// ─── Manager ─────────────────────────────────────────────────────────────────

// vibePreviewRingCap is the per-session frame ringbuffer capacity.
const vibePreviewRingCap = 200

// vibePreviewSubBufSize is the per-subscriber channel buffer. 16 events of
// slack absorbs short bursts without dropping; beyond that, slow consumers
// lose frames (intentional — better than stalling capture).
const vibePreviewSubBufSize = 16

// VibeClipRecord is the Phase 2.5 clip metadata shape. Defined here as a
// forward-declared type so Phase 2 can hold a slice of clips per session
// without circular imports. The full lifecycle (record, store, serve)
// lives in vibe_preview_clip.go.
type VibeClipRecord struct {
	ID          string    `json:"id"`
	Project     string    `json:"project"`
	Source      string    `json:"source"` // browser|sim-ios|sim-android|phone
	StartedAt   time.Time `json:"startedAt"`
	EndedAt     time.Time `json:"endedAt,omitempty"`
	DurationSec float64   `json:"durationSec,omitempty"`
	SizeBytes   int64     `json:"sizeBytes,omitempty"`
	Status      string    `json:"status"`             // recording|ready|failed
	Path        string    `json:"-"`                  // on-disk MP4 path; never JSON-leaked
	PosterPath  string    `json:"-"`                  // on-disk poster JPG; never JSON-leaked
	ShareURL    string    `json:"shareUrl,omitempty"` // durable presigned URL (P4) when object storage is configured; outlives the box
	Err         string    `json:"err,omitempty"`
}

// vibePreviewBrowserGetter is the slice of BrowserManager that
// VibePreviewManager actually depends on. Lets tests inject a fake.
type vibePreviewBrowserGetter interface {
	OpenSession(id string, headful bool) error
	Navigate(id, url string) (*BrowserActionResult, error)
	Screenshot(id string) (*BrowserActionResult, error)
	CloseSession(id string) error
}

// vibePreviewEventHistory keeps the last N events per session for replay
// on SSE subscribe. Mirrors DevServer's behavior so a late web-dashboard
// reconnect sees what just happened.
const vibePreviewEventHistory = 50

// VibePreviewEvent flows on the /vibing/preview/events SSE channel. Type
// values are the public protocol — be careful changing them.
type VibePreviewEvent struct {
	Type      string  `json:"type"` // frame|stable|throttle|capture_error|started|stopped|clip_started|clip_ready|summary
	Project   string  `json:"project,omitempty"`
	Seq       uint64  `json:"seq,omitempty"`
	Hash      string  `json:"hash,omitempty"`
	Size      int     `json:"size,omitempty"`
	Width     int     `json:"width,omitempty"`
	Height    int     `json:"height,omitempty"`
	FPS       float64 `json:"fps,omitempty"`
	Mode      string  `json:"mode,omitempty"`
	ClipID    string  `json:"clipId,omitempty"`
	Source    string  `json:"source,omitempty"` // browser|sim-ios|sim-android|phone
	DurationS float64 `json:"durationSec,omitempty"`
	Message   string  `json:"message,omitempty"`
	Timestamp string  `json:"ts"` // RFC3339 UTC
}

// VibePreviewManager owns active sessions and their ringbuffers. One per
// agent process. Lifecycle is tied to HTTPServer.
type VibePreviewManager struct {
	mu       sync.Mutex
	sessions map[string]*VibePreviewSession
	ring     map[string][]*vibeFrameRecord // sessionID -> ringbuffer
	seqCtr   map[string]*uint64

	// SSE fan-out + replay buffer per session. Subscribers are channels
	// with non-blocking sends (select-default drop) so a slow consumer
	// can never stall the capture loop.
	subs     map[string][]chan VibePreviewEvent
	eventLog map[string][]VibePreviewEvent

	// Phase 2.5 — clip records keyed by project, populated by clip recorder.
	// Manager-owned so list/list-by-session queries don't need a second
	// layer of locking.
	clips map[string][]*VibeClipRecord

	browser vibePreviewBrowserGetter
	// nowFn lets tests freeze the clock.
	nowFn func() time.Time

	// diskRoot is where frame bytes + clips live. Empty = "~/.yaver/vibe-preview".
	// Tests inject a tempdir.
	diskRoot string

	// lastCrash dedups identical crash messages within a 1 s window.
	// Read + written under m.mu.
	lastCrash *VibeCrashSignal

	// summaryCtr is the seq number assigned to the next QueueSummary
	// call. Persisted in summaries.jsonl alongside the text.
	summaryCtr uint64
}

// activeVibePreviewMgr is the process-wide singleton accessor. main.go's
// runServe sets it after constructing the HTTPServer; loop_exec.go reads
// it during the smart-develop-mode gate. Tests can swap it in/out via
// SetActiveVibePreviewManager.
//
// Package-level singleton instead of threading a reference through every
// LoopState because the manager is genuinely process-scoped (one Chrome
// browser pool per agent) and forwarding through every spec/state struct
// would be churn for no gain.
var activeVibePreviewMgr atomic.Value // stores *VibePreviewManager (or nil)

// SetActiveVibePreviewManager registers the global manager. Idempotent;
// last writer wins. Pass nil to clear.
func SetActiveVibePreviewManager(m *VibePreviewManager) {
	if m == nil {
		activeVibePreviewMgr.Store((*VibePreviewManager)(nil))
		return
	}
	activeVibePreviewMgr.Store(m)
}

// ActiveVibePreviewManager returns the registered manager or nil. Safe
// from any goroutine.
func ActiveVibePreviewManager() *VibePreviewManager {
	v, _ := activeVibePreviewMgr.Load().(*VibePreviewManager)
	return v
}

// NewVibePreviewManager returns a manager wired to the supplied
// BrowserManager. Pass nil for tests that don't need real captures —
// Start will return an error if browser is nil.
func NewVibePreviewManager(browser vibePreviewBrowserGetter) *VibePreviewManager {
	return &VibePreviewManager{
		sessions: make(map[string]*VibePreviewSession),
		ring:     make(map[string][]*vibeFrameRecord),
		seqCtr:   make(map[string]*uint64),
		subs:     make(map[string][]chan VibePreviewEvent),
		eventLog: make(map[string][]VibePreviewEvent),
		clips:    make(map[string][]*VibeClipRecord),
		browser:  browser,
		nowFn:    time.Now,
	}
}

// SetDiskRoot overrides the default disk root (~/.yaver/vibe-preview).
// Used by tests; callers should call before Start.
func (m *VibePreviewManager) SetDiskRoot(path string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.diskRoot = path
}

// resolveDiskRoot returns the on-disk root for frames + clips, mkdir-ing if
// missing. Empty diskRoot means "default to ~/.yaver/vibe-preview".
func (m *VibePreviewManager) resolveDiskRoot() string {
	m.mu.Lock()
	root := m.diskRoot
	m.mu.Unlock()
	if root == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			home = os.TempDir()
		}
		root = filepath.Join(home, ".yaver", "vibe-preview")
	}
	return root
}

// VibePreviewStartOpts is the input to Start.
type VibePreviewStartOpts struct {
	Project   string          `json:"project"`
	TargetURL string          `json:"targetUrl"`
	Mode      VibePreviewMode `json:"mode"`
	Profile   string          `json:"profile"` // explicit profile name
	NetMode   string          `json:"netMode"` // "direct" | "relay-wifi" | "relay-cell"
	// Width/Height override the profile's capture viewport when > 0, so a caller
	// can request a phone (390×844) or tablet (820×1180) render instead of the
	// profile's default. The TV surface uses this to preview a web app at a
	// chosen form factor. Zero means "use the profile".
	Width  int `json:"width,omitempty"`
	Height int `json:"height,omitempty"`
}

// Start boots a new preview session: opens a headless Chrome, navigates to
// targetUrl, and (for live mode) launches the capture loop.
//
// Returns an error if a session for the project already exists, the browser
// manager is missing, or the initial navigation fails. Caller is expected
// to surface errors verbatim — they're already user-readable.
func (m *VibePreviewManager) Start(opts VibePreviewStartOpts) (*VibePreviewSession, error) {
	if m == nil {
		return nil, fmt.Errorf("vibe-preview manager not initialised")
	}
	if m.browser == nil {
		return nil, fmt.Errorf("browser automation unavailable: install Chrome/Chromium")
	}
	if opts.Project == "" {
		return nil, fmt.Errorf("project is required")
	}
	if opts.TargetURL == "" {
		return nil, fmt.Errorf("targetUrl is required (e.g. http://127.0.0.1:3000)")
	}
	if opts.Mode == "" {
		opts.Mode = VibePreviewModeLive
	}

	profile := ProfileFor(opts.Profile, opts.NetMode)
	// Caller-requested viewport wins over the profile's default (e.g. a TV asking
	// for a phone/tablet render). Bounded to sane pixels so a bad value can't ask
	// Chrome for a 100k-wide canvas.
	if opts.Width >= 200 && opts.Width <= 3840 {
		profile.Width = opts.Width
	}
	if opts.Height >= 200 && opts.Height <= 2160 {
		profile.Height = opts.Height
	}

	// One session per project — caller must Stop before re-Starting.
	m.mu.Lock()
	if _, exists := m.sessions[opts.Project]; exists {
		m.mu.Unlock()
		return nil, fmt.Errorf("preview session for project %q already active; stop it first", opts.Project)
	}
	m.mu.Unlock()

	now := m.nowFn()
	browserID := fmt.Sprintf("vibe-preview-%s-%d", sanitizeBranchName(opts.Project), now.UnixNano()%1_000_000)

	if err := m.browser.OpenSession(browserID, false); err != nil {
		return nil, fmt.Errorf("open browser: %w", err)
	}
	if _, err := m.browser.Navigate(browserID, opts.TargetURL); err != nil {
		_ = m.browser.CloseSession(browserID)
		return nil, fmt.Errorf("navigate to %s: %w", opts.TargetURL, err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	var seq uint64
	sess := &VibePreviewSession{
		ID:        browserID,
		Project:   opts.Project,
		TargetURL: opts.TargetURL,
		BrowserID: browserID,
		Mode:      opts.Mode,
		Profile:   profile,
		StartedAt: now,
		LastFrame: now,
		cancel:    cancel,
	}

	m.mu.Lock()
	m.sessions[opts.Project] = sess
	m.ring[opts.Project] = make([]*vibeFrameRecord, 0, vibePreviewRingCap)
	m.seqCtr[opts.Project] = &seq
	m.mu.Unlock()

	// Emit lifecycle event before any capture so subscribers see a clean
	// {started → frame → ...} sequence.
	m.emit(opts.Project, VibePreviewEvent{
		Type:    "started",
		Project: opts.Project,
		Mode:    string(opts.Mode),
		FPS:     profile.FPS,
		Width:   profile.Width,
		Height:  profile.Height,
	})

	// Live mode: drive the capture loop.
	// Other modes still capture an initial frame so callers see something.
	if _, err := m.captureOnce(opts.Project); err != nil {
		log.Printf("[vibe-preview] initial capture failed: %v", err)
	}
	if opts.Mode == VibePreviewModeLive && profile.FPS > 0 {
		go m.runCaptureLoop(ctx, opts.Project, profile.FPS)
	}

	return cloneSession(sess), nil
}

// Stop tears down a session by project name. Idempotent: missing project
// returns a typed error; double-stop is a no-op on the second call.
func (m *VibePreviewManager) Stop(project string) error {
	if m == nil {
		return fmt.Errorf("vibe-preview manager not initialised")
	}
	m.mu.Lock()
	sess, ok := m.sessions[project]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("no preview session for project %q", project)
	}
	delete(m.sessions, project)
	delete(m.ring, project)
	delete(m.seqCtr, project)
	m.mu.Unlock()

	if !sess.stopped.Swap(true) {
		if sess.cancel != nil {
			sess.cancel()
		}
		if m.browser != nil && sess.BrowserID != "" {
			if err := m.browser.CloseSession(sess.BrowserID); err != nil {
				// Browser may have already gone away; warn but don't fail.
				log.Printf("[vibe-preview] close browser %s: %v", sess.BrowserID, err)
			}
		}
	}
	m.emit(project, VibePreviewEvent{Type: "stopped", Project: project})
	// Tear down subscribers — late SSE clients reconnecting will get a
	// 404 / empty event log and know the session is over.
	m.mu.Lock()
	for _, ch := range m.subs[project] {
		close(ch)
	}
	delete(m.subs, project)
	delete(m.eventLog, project)
	m.mu.Unlock()
	return nil
}

// Status returns a snapshot copy of every active session. Safe for handlers
// to JSON-encode directly.
func (m *VibePreviewManager) Status() []*VibePreviewSession {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]*VibePreviewSession, 0, len(m.sessions))
	for _, s := range m.sessions {
		out = append(out, cloneSession(s))
	}
	return out
}

// Snapshot forces a one-shot capture for an existing session. Used by the
// /vibing/preview/snapshot endpoint and by the change-only/summary-only hooks.
func (m *VibePreviewManager) Snapshot(project string) (*vibeFrameRecord, error) {
	return m.captureOnce(project)
}

// LatestFrame returns the most recent frame for a project, or nil if none.
// The returned record is a shallow copy — callers must not mutate Bytes.
func (m *VibePreviewManager) LatestFrame(project string) *vibeFrameRecord {
	m.mu.Lock()
	defer m.mu.Unlock()
	ring := m.ring[project]
	if len(ring) == 0 {
		return nil
	}
	return ring[len(ring)-1]
}

// FrameByHash returns a frame matching the given hash prefix, or nil.
// O(N) over the ringbuffer; fine for ring caps in the hundreds.
func (m *VibePreviewManager) FrameByHash(project, hash string) *vibeFrameRecord {
	if hash == "" {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, f := range m.ring[project] {
		if f.Hash == hash {
			return f
		}
	}
	return nil
}

// Stop all sessions. Called on agent shutdown.
func (m *VibePreviewManager) StopAll() {
	if m == nil {
		return
	}
	m.mu.Lock()
	projects := make([]string, 0, len(m.sessions))
	for p := range m.sessions {
		projects = append(projects, p)
	}
	m.mu.Unlock()
	for _, p := range projects {
		_ = m.Stop(p)
	}
}

// ─── Internal: capture loop ──────────────────────────────────────────────────

// runCaptureLoop ticks at fps Hz and captures frames until ctx is cancelled.
// Errors are logged + counted on the session but do not abort the loop —
// the dev server may have hiccuped and will recover.
func (m *VibePreviewManager) runCaptureLoop(ctx context.Context, project string, fps float64) {
	if fps <= 0 {
		return
	}
	interval := time.Duration(float64(time.Second) / fps)
	if interval < 50*time.Millisecond {
		interval = 50 * time.Millisecond // hard floor 20 FPS
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := m.captureOnce(project); err != nil {
				m.bumpErrors(project)
				log.Printf("[vibe-preview] %s: capture failed: %v", project, err)
				// Backoff a little on consecutive errors.
				time.Sleep(500 * time.Millisecond)
			}
		}
	}
}

// captureOnce takes one screenshot via the browser manager, hashes it, and
// pushes onto the ringbuffer. Returns the new record, or nil + error.
func (m *VibePreviewManager) captureOnce(project string) (*vibeFrameRecord, error) {
	m.mu.Lock()
	sess, ok := m.sessions[project]
	if !ok {
		m.mu.Unlock()
		return nil, fmt.Errorf("no session for project %q", project)
	}
	browserID := sess.BrowserID
	m.mu.Unlock()

	if m.browser == nil {
		return nil, fmt.Errorf("browser unavailable")
	}
	res, err := m.browser.Screenshot(browserID)
	if err != nil {
		return nil, err
	}
	raw, err := base64.StdEncoding.DecodeString(res.ScreenshotB64)
	if err != nil {
		return nil, fmt.Errorf("decode screenshot: %w", err)
	}

	sum := sha256.Sum256(raw)
	hash := hex.EncodeToString(sum[:])[:12]

	rec := &vibeFrameRecord{
		Hash:       hash,
		Bytes:      raw,
		CapturedAt: m.nowFn(),
	}

	// Persist to disk *outside* the lock — disk I/O can block. Failures
	// are logged but don't drop the in-memory record; the relay-side
	// fetch will fall back to bytes if the file is missing.
	if path, err := m.persistFrame(project, rec); err != nil {
		log.Printf("[vibe-preview] persist %s/%s: %v", project, hash, err)
	} else {
		rec.diskPath = path
	}

	m.mu.Lock()
	// Session may have been stopped while we held the screenshot above.
	sess, ok = m.sessions[project]
	if !ok {
		m.mu.Unlock()
		return nil, fmt.Errorf("session %q ended during capture", project)
	}
	ctr := m.seqCtr[project]
	*ctr++
	rec.Seq = *ctr

	// Stable-frame collapse: identical hash to the most recent frame is
	// not stored a second time, but we still emit a "stable" event so
	// the consumer can update FPS/heartbeat state.
	ring := m.ring[project]
	if len(ring) > 0 && ring[len(ring)-1].Hash == hash {
		sess.StableHits++
		stableSeq := rec.Seq
		m.mu.Unlock()
		log.Printf("[vibe-preview] %s seq=%d hash=%s STABLE (no change)", project, stableSeq, hash)
		m.emit(project, VibePreviewEvent{
			Type:    "stable",
			Project: project,
			Seq:     stableSeq,
			Hash:    hash,
		})
		return ring[len(ring)-1], nil
	}

	var evicted *vibeFrameRecord
	if len(ring) >= vibePreviewRingCap {
		evicted = ring[0]
		ring = ring[1:]
	}
	ring = append(ring, rec)
	m.ring[project] = ring

	sess.FrameCount++
	sess.LastFrame = rec.CapturedAt
	emitSeq := rec.Seq
	emitSize := len(raw)
	m.mu.Unlock()

	// Disk delete + emit happen after lock release — both can block.
	if evicted != nil {
		_ = m.removeDiskFrame(project, evicted)
	}

	log.Printf("[vibe-preview] %s seq=%d hash=%s bytes=%d", project, emitSeq, hash, emitSize)
	m.emit(project, VibePreviewEvent{
		Type:    "frame",
		Project: project,
		Seq:     emitSeq,
		Hash:    hash,
		Size:    emitSize,
	})
	return rec, nil
}

func (m *VibePreviewManager) bumpErrors(project string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s := m.sessions[project]; s != nil {
		s.Errors++
	}
}

// ─── Helpers ─────────────────────────────────────────────────────────────────

// ─── Disk persistence ─────────────────────────────────────────────────────────

// persistFrame writes the frame bytes to disk and returns the path.
// Idempotent: if the hashed file already exists, returns the existing path
// without rewriting. Caller does not need to hold the manager lock.
func (m *VibePreviewManager) persistFrame(project string, rec *vibeFrameRecord) (string, error) {
	if rec == nil || len(rec.Bytes) == 0 {
		return "", nil
	}
	dir := filepath.Join(m.resolveDiskRoot(), sanitizeBranchName(project))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(dir, rec.Hash+".png")
	if _, err := os.Stat(path); err == nil {
		return path, nil
	}
	if err := os.WriteFile(path, rec.Bytes, 0o600); err != nil {
		return "", err
	}
	return path, nil
}

// removeDiskFrame deletes the on-disk artifact for an evicted record.
// Best-effort: errors are logged but not surfaced.
func (m *VibePreviewManager) removeDiskFrame(project string, rec *vibeFrameRecord) error {
	if rec == nil || rec.diskPath == "" {
		return nil
	}
	if err := os.Remove(rec.diskPath); err != nil && !os.IsNotExist(err) {
		log.Printf("[vibe-preview] evict %s: %v", rec.diskPath, err)
		return err
	}
	return nil
}

// ReadFrameBytes returns the PNG bytes for a given hash. Looks in the
// in-memory ring first; falls back to disk; returns nil + error if neither.
// Caller is the HTTP frame-fetch handler.
func (m *VibePreviewManager) ReadFrameBytes(project, hash string) ([]byte, error) {
	if hash == "" {
		return nil, fmt.Errorf("hash required")
	}
	rec := m.FrameByHash(project, hash)
	if rec != nil && len(rec.Bytes) > 0 {
		// Return a copy to avoid handing the http handler a slice that
		// the manager could mutate (eviction sets Bytes=nil today, but
		// future code might compact; cheap to be safe).
		out := make([]byte, len(rec.Bytes))
		copy(out, rec.Bytes)
		return out, nil
	}
	// Disk fallback — content-addressed by filename, project-scoped.
	dir := filepath.Join(m.resolveDiskRoot(), sanitizeBranchName(project))
	path := filepath.Join(dir, hash+".png")
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("frame %s/%s not found: %w", project, hash, err)
	}
	return b, nil
}

// ─── SSE event broadcasting ───────────────────────────────────────────────────

// emit appends to the per-session event log and fans out to live subscribers.
// Non-blocking: a slow subscriber loses events, never stalls the producer.
func (m *VibePreviewManager) emit(project string, ev VibePreviewEvent) {
	if ev.Timestamp == "" {
		ev.Timestamp = m.nowFn().UTC().Format("2006-01-02T15:04:05.000Z")
	}
	if ev.Project == "" {
		ev.Project = project
	}

	m.mu.Lock()
	log := m.eventLog[project]
	if len(log) >= vibePreviewEventHistory {
		log = log[1:]
	}
	log = append(log, ev)
	m.eventLog[project] = log

	// Snapshot the subscriber slice so we can release the lock before
	// pushing — channel sends with select-default are still non-blocking,
	// but holding the manager lock during fan-out would serialize the
	// whole agent if there are many subscribers.
	subs := make([]chan VibePreviewEvent, len(m.subs[project]))
	copy(subs, m.subs[project])
	m.mu.Unlock()

	for _, ch := range subs {
		select {
		case ch <- ev:
		default:
			// Slow consumer: drop. Their /events SSE will see a gap.
		}
	}
}

// Subscribe registers a new SSE consumer for project events. Returns the
// channel + a snapshot of the recent event log (for replay) + an
// unsubscribe func. Caller is expected to drain the channel and call
// unsubscribe when the connection closes.
func (m *VibePreviewManager) Subscribe(project string) (<-chan VibePreviewEvent, []VibePreviewEvent, func()) {
	ch := make(chan VibePreviewEvent, vibePreviewSubBufSize)

	m.mu.Lock()
	m.subs[project] = append(m.subs[project], ch)
	histCopy := make([]VibePreviewEvent, len(m.eventLog[project]))
	copy(histCopy, m.eventLog[project])
	m.mu.Unlock()

	unsubscribe := func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		curr := m.subs[project]
		for i, c := range curr {
			if c == ch {
				curr = append(curr[:i], curr[i+1:]...)
				m.subs[project] = curr
				// Drain + close so the writer goroutine doesn't leak.
				go func() {
					for range ch {
					}
				}()
				close(ch)
				return
			}
		}
	}
	return ch, histCopy, unsubscribe
}

// EmitClipEvent is the entry point used by Phase 2.5 clip recorder + Phase 4
// summary pipeline to push events into the same per-session SSE channel.
// Exposed because the clip recorder is in another file.
func (m *VibePreviewManager) EmitClipEvent(project string, ev VibePreviewEvent) {
	m.emit(project, ev)
}

// RegisterClip adds a clip record to the per-project list and emits a
// `clip_ready` event when status flips to ready. Idempotent on ID.
func (m *VibePreviewManager) RegisterClip(project string, clip *VibeClipRecord) {
	if clip == nil {
		return
	}
	m.mu.Lock()
	list := m.clips[project]
	for i, c := range list {
		if c.ID == clip.ID {
			list[i] = clip
			m.clips[project] = list
			m.mu.Unlock()
			return
		}
	}
	m.clips[project] = append(list, clip)
	m.mu.Unlock()
}

// ListClips returns a copy of clip records for a project, newest first.
func (m *VibePreviewManager) ListClips(project string) []*VibeClipRecord {
	m.mu.Lock()
	defer m.mu.Unlock()
	src := m.clips[project]
	out := make([]*VibeClipRecord, len(src))
	for i, c := range src {
		cp := *c
		out[len(src)-1-i] = &cp
	}
	return out
}

// ClipByID looks up a clip by ID across every project. O(N) over all
// recorded clips; fine for the few-dozen-clips ringbuffer the manager is
// expected to hold.
func (m *VibePreviewManager) ClipByID(id string) *VibeClipRecord {
	if id == "" {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, list := range m.clips {
		for _, c := range list {
			if c.ID == id {
				cp := *c
				return &cp
			}
		}
	}
	return nil
}

// ─── Helpers ─────────────────────────────────────────────────────────────────

// cloneSession returns a JSON-safe copy that omits internal fields.
// Builds a fresh struct field-by-field rather than struct-copy because
// VibePreviewSession contains sync/atomic.Bool (noCopy).
func cloneSession(s *VibePreviewSession) *VibePreviewSession {
	if s == nil {
		return nil
	}
	return &VibePreviewSession{
		ID:         s.ID,
		Project:    s.Project,
		TargetURL:  s.TargetURL,
		BrowserID:  s.BrowserID,
		Mode:       s.Mode,
		Profile:    s.Profile,
		StartedAt:  s.StartedAt,
		LastFrame:  s.LastFrame,
		FrameCount: s.FrameCount,
		StableHits: s.StableHits,
		Errors:     s.Errors,
	}
}
