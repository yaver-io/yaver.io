package main

// screenlog.go — local-only "screen as a stream of images" black box.
//
// This is the Yaver reincarnation of the talos PC-monitor frame engine,
// with the trust model inverted: talos shipped JPEG frames to an org's
// Hetzner Storage Box + Convex (employer surveillance); screenlog keeps
// every byte on THIS machine, under ~/.yaver/screenlog/, and never
// touches Convex/SFTP/relay. The convex privacy test fences the frame
// field names so a future sync path can't leak them.
//
// Mechanism: one goroutine ticks on an interval, grabs the screen
// (reusing the cross-platform capture path, WSL-aware — see
// screenlog_capture.go), perceptually de-duplicates consecutive frames
// (dHash + Hamming), optionally downscales, re-encodes to PNG/JPEG, and
// writes the frame + a metadata row into the session's index.json. A
// disk-budget ring buffer evicts the oldest frames so an unattended
// recorder can't fill the disk.
//
// Three intended uses (all local): (1) personal black box you scrub via
// the /screenlog/<id> viewer; (2) agent-readable — the runner can list
// recent frames to answer "what was I doing at 14:30"; (3) attach recent
// frames to a feedback/bug report.
//
// One session records at a time per agent (matches clip_*/cast_*).

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"math/bits"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	xdraw "golang.org/x/image/draw"
)

// ScreenlogConfig is the capture configuration for one session. Defaults
// favour fidelity (full-res PNG) per the product decision; the disk
// budget keeps that honest.
type ScreenlogConfig struct {
	IntervalSec   int    `json:"intervalSec"`   // seconds between captures
	Format        string `json:"format"`        // "png" | "jpg"
	Quality       int    `json:"quality"`       // jpg quality 1-100 (ignored for png)
	MaxWidth      int    `json:"maxWidth"`      // per-frame downscale cap in px; 0 = full res
	Displays      string `json:"displays"`      // "all" | "primary"
	Dedup         bool   `json:"dedup"`         // perceptual de-dup of consecutive frames
	DedupThresh   int    `json:"dedupThresh"`   // Hamming distance under which a frame is a dup
	HeartbeatSec  int    `json:"heartbeatSec"`  // keep one frame every N sec even if unchanged
	MaxDiskMB     int    `json:"maxDiskMB"`     // ring-buffer disk budget; 0 = unlimited
	MaxFrames     int    `json:"maxFrames"`     // in-memory + on-disk frame-count cap; 0 = default (bounds RAM)
	RetentionDays int    `json:"retentionDays"` // prune sessions older than N days on start; 0 = keep
	TagWindow     bool   `json:"tagWindow"`     // best-effort active-window/app tagging
	WSLTarget     string `json:"wslTarget"`     // "auto" | "host" | "wslg" (WSL only)
	// Input-event companion stream (keys + mouse). OFF by default and
	// additionally gated by ScreenlogPolicy.AllowInputCapture — keylogging
	// is far more sensitive than screenshots. AllowRawText=false (default)
	// redacts typed characters, keeping action structure for training
	// without storing secrets. See screenlog_input.go.
	CaptureInput bool `json:"captureInput,omitempty"`
	AllowRawText bool `json:"allowRawText,omitempty"`
	// EphemeralFrames = "temporary screenshots": capture each frame to
	// derive its label (which app/window) + perceptual hash + active
	// interval, then DISCARD the image — only the activity trace is kept.
	// Storage-light + privacy-friendly; the report still answers "what was
	// it doing", just without retained pictures. Default false (keep
	// images). A real-time runner could label the temp frame before it's
	// dropped (see docs — runner-optional smart labeling).
	EphemeralFrames bool `json:"ephemeralFrames,omitempty"`
}

func defaultScreenlogConfig() ScreenlogConfig {
	return ScreenlogConfig{
		IntervalSec: 2,
		// Whole screen, cheap: JPEG + a downscale cap. Quality isn't
		// important for activity monitoring, and JPEG-of-a-downscaled-frame
		// is far lighter to encode/store than a full-res PNG (which spiked
		// CPU on Retina). This is NOT power-saving — cadence + coverage are
		// unchanged; only per-frame cost drops.
		Format:        "jpg",
		Quality:       60,
		MaxWidth:      1920, // downscale wide screens; still the WHOLE screen
		Displays:      "all",
		Dedup:         true,
		DedupThresh:   6,
		HeartbeatSec:  300,
		MaxDiskMB:     4096,
		MaxFrames:     defaultMaxFrames,
		RetentionDays: 7,
		TagWindow:     true,
		WSLTarget:     "auto",
	}
}

// normalize clamps user-supplied config into safe ranges.
func (c *ScreenlogConfig) normalize() {
	d := defaultScreenlogConfig()
	if c.IntervalSec <= 0 {
		c.IntervalSec = d.IntervalSec
	}
	if c.Format != "png" && c.Format != "jpg" {
		c.Format = d.Format
	}
	if c.Quality <= 0 || c.Quality > 100 {
		c.Quality = d.Quality
	}
	if c.MaxWidth < 0 {
		c.MaxWidth = 0
	}
	if c.Displays != "all" && c.Displays != "primary" {
		c.Displays = d.Displays
	}
	if c.DedupThresh < 0 {
		c.DedupThresh = d.DedupThresh
	}
	if c.HeartbeatSec < 0 {
		c.HeartbeatSec = 0
	}
	if c.MaxDiskMB < 0 {
		c.MaxDiskMB = 0
	}
	if c.MaxFrames < 0 {
		c.MaxFrames = 0 // → defaultMaxFrames at enforce time
	}
	if c.RetentionDays < 0 {
		c.RetentionDays = 0
	}
	if c.WSLTarget != "host" && c.WSLTarget != "wslg" && c.WSLTarget != "auto" {
		c.WSLTarget = d.WSLTarget
	}
}

// ScreenlogFrame is one kept frame's metadata (the bytes live in File).
type ScreenlogFrame struct {
	Idx             int    `json:"idx"`
	CapturedAt      int64  `json:"capturedAt"` // unix ms
	Display         int    `json:"display"`
	File            string `json:"file"` // filename inside the session dir
	Bytes           int64  `json:"bytes"`
	Width           int    `json:"width"`
	Height          int    `json:"height"`
	PHash           uint64 `json:"phash"`
	HammingFromPrev int    `json:"hammingFromPrev,omitempty"`
	ActiveApp       string `json:"activeApp,omitempty"`
	ActiveWindow    string `json:"activeWindow,omitempty"`
	Heartbeat       bool   `json:"heartbeat,omitempty"`
	// ActiveToMs closes the interval this frame represents. Because
	// duplicate frames are de-duplicated (not stored), a kept frame's
	// [CapturedAt, ActiveToMs] span covers every identical screen until
	// the NEXT distinct frame — i.e. "this screen was on from 12:01 to
	// 12:53". Set when the next kept frame for the same display arrives;
	// the final frame is closed at StoppedAt.
	ActiveToMs int64 `json:"activeToMs,omitempty"`
}

// ScreenlogSession is the persisted record for one recording. Stored as
// index.json inside the session dir so listings survive restarts.
type ScreenlogSession struct {
	ID        string           `json:"id"`
	Title     string           `json:"title,omitempty"`
	Host      string           `json:"host,omitempty"`
	StartedAt int64            `json:"startedAt"` // unix ms
	StoppedAt int64            `json:"stoppedAt,omitempty"`
	Config    ScreenlogConfig  `json:"config"`
	Frames    []ScreenlogFrame `json:"frames"`
}

// activeScreenlog is the running recorder. Only one at a time per agent.
type activeScreenlog struct {
	mu           sync.Mutex
	session      *ScreenlogSession
	cancel       chan struct{}
	done         chan struct{}
	lastKept     map[int]uint64 // per-display last kept phash
	lastKeptAt   map[int]int64  // per-display last kept unix ms
	lastKeptSlot map[int]int    // per-display index into session.Frames of the last kept frame
	nextIdx      int
	totalBytes   int64
	dropped      int
	dirty        int // kept frames since last index.json save (throttle)
	lastErr      string
	startedAt    time.Time
}

// defaultMaxFrames bounds the in-memory index + on-disk frame count so an
// unattended recorder can't grow unbounded. ~5k frames of metadata is a
// small index; the MaxDiskMB ring buffer bounds bytes independently.
const defaultMaxFrames = 5000

var (
	screenlogMu      sync.Mutex
	screenlogActive  *activeScreenlog
	screenlogBaseDir string
)

func screenlogDir() (string, error) {
	screenlogMu.Lock()
	defer screenlogMu.Unlock()
	if screenlogBaseDir != "" {
		return screenlogBaseDir, nil
	}
	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	p := filepath.Join(dir, "screenlog")
	if err := os.MkdirAll(p, 0o700); err != nil {
		return "", err
	}
	screenlogBaseDir = p
	return p, nil
}

func screenlogSessionDir(id string) (string, error) {
	base, err := screenlogDir()
	if err != nil {
		return "", err
	}
	p := filepath.Join(base, id)
	if err := os.MkdirAll(p, 0o700); err != nil {
		return "", err
	}
	return p, nil
}

func saveScreenlogSession(s *ScreenlogSession) error {
	dir, err := screenlogSessionDir(s.ID)
	if err != nil {
		return err
	}
	data, _ := json.MarshalIndent(s, "", "  ")
	return os.WriteFile(filepath.Join(dir, "index.json"), data, 0o600)
}

func loadScreenlogSession(id string) (*ScreenlogSession, error) {
	dir, err := screenlogSessionDir(id)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(filepath.Join(dir, "index.json"))
	if err != nil {
		return nil, err
	}
	var s ScreenlogSession
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

// listScreenlogSessions returns lightweight summaries (no frame arrays)
// newest-first.
func listScreenlogSessions() ([]ScreenlogSession, error) {
	base, err := screenlogDir()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(base)
	if err != nil {
		return nil, err
	}
	out := make([]ScreenlogSession, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if s, err := loadScreenlogSession(e.Name()); err == nil {
			out = append(out, *s)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].StartedAt > out[j].StartedAt })
	return out, nil
}

// pruneOldScreenlogSessions deletes session dirs older than retentionDays.
func pruneOldScreenlogSessions(retentionDays int) {
	if retentionDays <= 0 {
		return
	}
	base, err := screenlogDir()
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-time.Duration(retentionDays) * 24 * time.Hour).UnixMilli()
	entries, _ := os.ReadDir(base)
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		s, err := loadScreenlogSession(e.Name())
		if err != nil {
			continue
		}
		ref := s.StoppedAt
		if ref == 0 {
			ref = s.StartedAt
		}
		if ref > 0 && ref < cutoff {
			_ = os.RemoveAll(filepath.Join(base, e.Name()))
		}
	}
}

// --- perceptual hash (dHash) ----------------------------------------------

// dHash computes a 64-bit difference hash: downsample to 9x8 grayscale,
// then for each row emit 8 bits comparing each pixel to its left
// neighbour. Tolerant to small changes (cursor, clock), sensitive to
// real content changes. Pure Go, no deps.
func dHash(img image.Image) uint64 {
	const w, h = 9, 8
	b := img.Bounds()
	sw, sh := b.Dx(), b.Dy()
	if sw == 0 || sh == 0 {
		return 0
	}
	var hash uint64
	var bit uint
	for y := 0; y < h; y++ {
		var prev uint32
		for x := 0; x < w; x++ {
			sx := b.Min.X + x*sw/w
			sy := b.Min.Y + y*sh/h
			r, g, bl, _ := img.At(sx, sy).RGBA() // each 0..65535
			gray := (19595*r + 38470*g + 7471*bl + 1<<15) >> 16
			if x > 0 {
				if gray > prev {
					hash |= 1 << bit
				}
				bit++
			}
			prev = gray
		}
	}
	return hash
}

func hammingDistance(a, b uint64) int { return bits.OnesCount64(a ^ b) }

// --- frame encode/downscale ------------------------------------------------

// encodeFrame downscales (if cfg.MaxWidth>0 and the image is wider) and
// re-encodes to the configured format. Returns bytes + final dimensions.
func encodeFrame(img image.Image, cfg ScreenlogConfig) ([]byte, int, int, error) {
	if cfg.MaxWidth > 0 && img.Bounds().Dx() > cfg.MaxWidth {
		srcB := img.Bounds()
		scale := float64(cfg.MaxWidth) / float64(srcB.Dx())
		dw := cfg.MaxWidth
		dh := int(float64(srcB.Dy()) * scale)
		if dh < 1 {
			dh = 1
		}
		dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
		// ApproxBiLinear: fast, low-quality downscale — fidelity isn't the
		// point for activity monitoring, throughput is.
		xdraw.ApproxBiLinear.Scale(dst, dst.Bounds(), img, srcB, xdraw.Over, nil)
		img = dst
	}
	var buf bytes.Buffer
	switch cfg.Format {
	case "jpg":
		if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: cfg.Quality}); err != nil {
			return nil, 0, 0, err
		}
	default: // png
		if err := png.Encode(&buf, img); err != nil {
			return nil, 0, 0, err
		}
	}
	return buf.Bytes(), img.Bounds().Dx(), img.Bounds().Dy(), nil
}

func frameExt(cfg ScreenlogConfig) string {
	if cfg.Format == "jpg" {
		return "jpg"
	}
	return "png"
}

// --- recording lifecycle ---------------------------------------------------

// startScreenlog begins a local frame-capture session. Returns the
// session (with empty Frames) or an error if one is already running /
// no capture driver is available.
func startScreenlog(cfg ScreenlogConfig, title string) (*ScreenlogSession, error) {
	cfg.normalize()

	screenlogMu.Lock()
	if screenlogActive != nil {
		screenlogMu.Unlock()
		return nil, fmt.Errorf("a screenlog session is already running — stop it first")
	}
	screenlogMu.Unlock()

	// Test/dev: drive the real loop from a synthetic, no-display source.
	maybeUseSyntheticSource()

	// Fail fast if we can't capture at all on this host.
	if _, err := screenlogProbe(cfg); err != nil {
		return nil, err
	}

	pruneOldScreenlogSessions(cfg.RetentionDays)

	host, _ := os.Hostname()
	sess := &ScreenlogSession{
		ID:        "slog-" + randomFormID(),
		Title:     title,
		Host:      host,
		StartedAt: time.Now().UnixMilli(),
		Config:    cfg,
		Frames:    []ScreenlogFrame{},
	}
	if err := saveScreenlogSession(sess); err != nil {
		return nil, err
	}

	a := &activeScreenlog{
		session:      sess,
		cancel:       make(chan struct{}),
		done:         make(chan struct{}),
		lastKept:     map[int]uint64{},
		lastKeptAt:   map[int]int64{},
		lastKeptSlot: map[int]int{},
		nextIdx:      1,
		startedAt:    time.Now(),
	}
	screenlogMu.Lock()
	screenlogActive = a
	screenlogMu.Unlock()

	go a.run()

	// Optional input-event companion capture (keys + mouse). Gated by the
	// stronger AllowInputCapture policy; non-fatal if no producer exists —
	// the frame stream still records.
	if cfg.CaptureInput {
		if !loadScreenlogPolicy().AllowInputCapture {
			_, _ = stopScreenlog()
			return nil, fmt.Errorf("input capture requested but disabled — owner must run `yaver screenlog allow-input`")
		}
		note, ok := startInputCapture(sess.ID, cfg, !cfg.AllowRawText)
		appendScreenlogAudit(screenlogAuditEntry{Action: "input", Session: sess.ID, Note: note})
		if !ok {
			a.mu.Lock()
			a.lastErr = "input capture: " + note
			a.mu.Unlock()
		}
	}
	return sess, nil
}

// startScreenlogGuarded enforces the machine's ScreenlogPolicy (kill
// switch + remote-control gate + mesh-peer grant), writes the audit
// trail, and only then starts. Both the HTTP handler and the MCP verb go
// through here so the consent gate can't be bypassed by picking a surface.
func startScreenlogGuarded(cfg ScreenlogConfig, title string, caller screenlogCaller) (*ScreenlogSession, error) {
	pol := loadScreenlogPolicy()
	if ok, reason := screenlogEnforce(pol, caller); !ok {
		appendScreenlogAudit(screenlogAuditEntry{
			Action: "deny", Remote: caller.Remote, Mesh: caller.Mesh, PeerID: caller.PeerID, Note: reason,
		})
		return nil, fmt.Errorf("%s", reason)
	}
	sess, err := startScreenlog(cfg, title)
	if err != nil {
		return nil, err
	}
	appendScreenlogAudit(screenlogAuditEntry{
		Action: "start", Session: sess.ID, Remote: caller.Remote, Mesh: caller.Mesh, PeerID: caller.PeerID,
	})
	// Tell the machine owner when a NON-local caller starts recording —
	// screen capture is sensitive enough that it should never be silent.
	// Reuses the same notification fan-out (push channels) the uptime
	// monitor uses, plus a local desktop toast so the person at the
	// keyboard sees it too.
	if caller.Remote && pol.NotifyOnStart {
		who := "a remote device on your account"
		if caller.Mesh {
			who = "mesh peer " + caller.PeerID
		}
		detail := "Screen recording was started remotely by " + who + " (session " + sess.ID + "). Stop it with `yaver screenlog stop` or disable with `yaver screenlog disable`."
		if globalNotifyManager != nil {
			globalNotifyManager.NotifyAgentEvent("Screen recording started remotely", detail)
		}
		defaultDesktopNotify("Yaver: screen recording started", "Started remotely by "+who)
	}
	return sess, nil
}

// stopScreenlog finalizes the active session and returns it.
func stopScreenlog() (*ScreenlogSession, error) {
	screenlogMu.Lock()
	a := screenlogActive
	screenlogActive = nil
	screenlogMu.Unlock()
	if a == nil {
		return nil, fmt.Errorf("no active screenlog session")
	}
	close(a.cancel)
	<-a.done
	stopInputCapture()

	a.mu.Lock()
	a.session.StoppedAt = time.Now().UnixMilli()
	// Close out any still-open frame intervals at stop time.
	for i := range a.session.Frames {
		if a.session.Frames[i].ActiveToMs == 0 {
			a.session.Frames[i].ActiveToMs = a.session.StoppedAt
		}
	}
	_ = saveScreenlogSession(a.session)
	out := *a.session
	a.mu.Unlock()
	return &out, nil
}

// screenlogStatus reports the live session counters, or running=false.
func screenlogStatus() map[string]interface{} {
	screenlogMu.Lock()
	a := screenlogActive
	screenlogMu.Unlock()
	if a == nil {
		return map[string]interface{}{"running": false}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return map[string]interface{}{
		"running":    true,
		"id":         a.session.ID,
		"startedAt":  a.session.StartedAt,
		"keptFrames": len(a.session.Frames),
		"dropped":    a.dropped,
		"bytes":      a.totalBytes,
		"config":     a.session.Config,
		"lastError":  a.lastErr,
		"elapsedSec": int(time.Since(a.startedAt).Seconds()),
	}
}

func (a *activeScreenlog) run() {
	defer close(a.done)
	cfg := a.session.Config
	a.captureOnce(cfg)
	ticker := time.NewTicker(time.Duration(cfg.IntervalSec) * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-a.cancel:
			return
		case <-ticker.C:
			a.captureOnce(cfg)
		}
	}
}

// Injectable sources so a headless emulator / tests can drive the REAL
// pipeline without a display or input device (see screenlog_emulator.go).
var (
	screenlogCaptureFn = captureScreenlogFrames
	screenlogWindowFn  = activeWindowInfo
)

// screenlogSaveEvery throttles index.json writes: saving the whole frame
// slice on every tick is O(n) per frame → O(n²) over a session and thrashes
// IO. We persist every N kept frames (and always on stop), so the index
// stays cheap regardless of session length.
const screenlogSaveEvery = 10

// captureOnce grabs every display and feeds each decoded frame through the
// shared ingestFrame path.
func (a *activeScreenlog) captureOnce(cfg ScreenlogConfig) {
	raws, err := screenlogCaptureFn(cfg)
	if err != nil {
		a.mu.Lock()
		a.lastErr = err.Error()
		a.mu.Unlock()
		return
	}
	var app, win string
	if cfg.TagWindow {
		app, win = screenlogWindowFn()
	}
	now := time.Now().UnixMilli()
	for _, raw := range raws {
		img, _, derr := image.Decode(bytes.NewReader(raw.png))
		if derr != nil {
			continue
		}
		a.ingestFrame(now, raw.display, img, app, win, cfg)
	}
}

// ingestFrame runs de-dup + interval-close + persist for ONE decoded frame
// at an explicit timestamp. Shared by the live loop and the headless
// emulator. Bounded by design so an unattended recorder can NEVER balloon
// RAM, fill disk, or thrash CPU:
//   - in-memory index capped at MaxFrames (oldest evicted, file deleted);
//   - on-disk frames capped by the MaxDiskMB ring buffer;
//   - index.json writes throttled (screenlogSaveEvery), so we don't
//     re-marshal a growing slice every tick;
//   - duplicate screens are dropped before any encode/write.
func (a *activeScreenlog) ingestFrame(now int64, display int, img image.Image, app, win string, cfg ScreenlogConfig) {
	ph := dHash(img)

	a.mu.Lock()
	prev, hadPrev := a.lastKept[display]
	prevAt := a.lastKeptAt[display]
	ham := 0
	heartbeat := false
	if hadPrev {
		ham = hammingDistance(ph, prev)
		if cfg.Dedup && ham <= cfg.DedupThresh {
			if cfg.HeartbeatSec > 0 && now-prevAt >= int64(cfg.HeartbeatSec)*1000 {
				heartbeat = true
			} else {
				a.dropped++
				a.mu.Unlock()
				return
			}
		}
	}
	idx := a.nextIdx
	a.nextIdx++
	a.mu.Unlock()

	fr := ScreenlogFrame{
		Idx: idx, CapturedAt: now, Display: display, PHash: ph,
		HammingFromPrev: ham, ActiveApp: app, ActiveWindow: win, Heartbeat: heartbeat,
		Width: img.Bounds().Dx(), Height: img.Bounds().Dy(),
	}

	dir, derr := screenlogSessionDir(a.session.ID)
	if derr != nil {
		return
	}

	// Ephemeral = "temporary screenshots": keep only the trace, no image.
	if !cfg.EphemeralFrames {
		enc, w, h, eerr := encodeFrame(img, cfg)
		if eerr != nil {
			return
		}
		fname := fmt.Sprintf("%06d_d%d_%d.%s", idx, display, now, frameExt(cfg))
		if werr := os.WriteFile(filepath.Join(dir, fname), enc, 0o600); werr != nil {
			a.mu.Lock()
			a.lastErr = werr.Error()
			a.mu.Unlock()
			return
		}
		fr.File = fname
		fr.Bytes = int64(len(enc))
		fr.Width = w
		fr.Height = h
	}

	a.mu.Lock()
	if prevSlot, ok := a.lastKeptSlot[display]; ok && prevSlot < len(a.session.Frames) {
		a.session.Frames[prevSlot].ActiveToMs = now // close the previous interval
	}
	a.session.Frames = append(a.session.Frames, fr)
	a.lastKeptSlot[display] = len(a.session.Frames) - 1
	a.lastKept[display] = ph
	a.lastKeptAt[display] = now
	a.totalBytes += fr.Bytes

	removed := a.enforceFrameCapLocked(cfg, dir) + a.enforceDiskBudgetLocked(cfg, dir)
	a.shiftSlotsLocked(removed)
	a.saveIfDueLocked()
	a.mu.Unlock()
}

// enforceFrameCapLocked bounds the in-memory index + on-disk frame count.
// Returns how many were evicted from the front. Caller holds a.mu.
func (a *activeScreenlog) enforceFrameCapLocked(cfg ScreenlogConfig, dir string) int {
	cap := cfg.MaxFrames
	if cap <= 0 {
		cap = defaultMaxFrames
	}
	removed := 0
	for len(a.session.Frames) > cap {
		old := a.session.Frames[0]
		a.session.Frames = a.session.Frames[1:]
		a.totalBytes -= old.Bytes
		if old.File != "" {
			_ = os.Remove(filepath.Join(dir, old.File))
		}
		removed++
	}
	return removed
}

// enforceDiskBudgetLocked evicts oldest frames until under the byte budget.
// Returns how many were evicted. Caller holds a.mu.
func (a *activeScreenlog) enforceDiskBudgetLocked(cfg ScreenlogConfig, dir string) int {
	if cfg.MaxDiskMB <= 0 {
		return 0
	}
	budget := int64(cfg.MaxDiskMB) * 1024 * 1024
	removed := 0
	for a.totalBytes > budget && len(a.session.Frames) > 1 {
		old := a.session.Frames[0]
		a.session.Frames = a.session.Frames[1:]
		a.totalBytes -= old.Bytes
		if old.File != "" {
			_ = os.Remove(filepath.Join(dir, old.File))
		}
		removed++
	}
	return removed
}

// shiftSlotsLocked fixes per-display "last kept frame" indices after the
// front of the slice was evicted (without this, interval-close writes to
// the wrong frame). Caller holds a.mu.
func (a *activeScreenlog) shiftSlotsLocked(removed int) {
	if removed == 0 {
		return
	}
	for d, s := range a.lastKeptSlot {
		if ns := s - removed; ns < 0 {
			delete(a.lastKeptSlot, d)
		} else {
			a.lastKeptSlot[d] = ns
		}
	}
}

// saveIfDueLocked persists the index every screenlogSaveEvery kept frames.
// Caller holds a.mu.
func (a *activeScreenlog) saveIfDueLocked() {
	a.dirty++
	if a.dirty >= screenlogSaveEvery {
		_ = saveScreenlogSession(a.session)
		a.dirty = 0
	}
}
