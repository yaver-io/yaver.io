package main

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// recording.go — platform-aware screen/session recorder for the
// morning match report. Each driver knows how to start, stop, and
// report Availability; RecordingManager picks the best-available
// driver for a requested target.
//
// Why this shape
//
//   - Keeps the recording decision local to the agent. Mobile / web /
//     yaver-to-yaver never orchestrate recording directly; they only
//     consume the recorded mp4 via the byte-range endpoint.
//   - Platform asymmetry (macOS vs Linux vs Windows vs iOS sim vs
//     Android emu) lives behind a single Driver interface so the rest
//     of the agent doesn't branch on runtime.GOOS.
//   - `yaver doctor` and `yaver install` reflect Availability() so a
//     fresh developer can see what's missing and fix it with one
//     command. No silent failures — if a recording can't start we
//     surface the reason in RecordingManager.Start's error.

// RecordingTarget hints at what the caller wants to capture. The
// manager may fall back to a less specific driver if the exact one
// isn't available; callers should expect `(target, driverUsed)` may
// differ.
type RecordingTarget string

const (
	// RecordingTargetScreen = the user's desktop (ffmpeg-based).
	RecordingTargetScreen RecordingTarget = "screen"
	// RecordingTargetIOSSim = `xcrun simctl io booted recordVideo`.
	RecordingTargetIOSSim RecordingTarget = "ios-sim"
	// RecordingTargetAndroidEmu = `adb shell screenrecord` piped back.
	RecordingTargetAndroidEmu RecordingTarget = "android-emu"
)

// RecordingHandle is the ticket returned by Start and required by Stop.
type RecordingHandle struct {
	ID       string          // internal id; usually "<runId>:<taskId>"
	Target   RecordingTarget // resolved target (may differ from requested)
	Driver   string          // driver name used (for telemetry)
	RunID    string
	TaskID   string
	Path     string // final mp4 path on disk
	Started  time.Time
}

// RecordingResult is what Stop returns.
type RecordingResult struct {
	Handle     RecordingHandle
	DurationMs int
	SizeBytes  int64
}

// RecordingDriver is implemented by each platform-specific recorder.
// Start must be non-blocking — it spawns the capture process and
// returns once the recorder is writing to disk. Stop must gracefully
// finalize the mp4 (send SIGINT to ffmpeg, wait for it to finish the
// container). Available() is cheap and safe to call often.
type RecordingDriver interface {
	Name() string
	Available() (bool, string)
	Start(ctx context.Context, outPath string, runID, taskID string) (driverState, error)
	Stop(state driverState) error
}

// driverState is an opaque per-driver handle for the running capture.
type driverState struct {
	Cmd      *exec.Cmd
	SimID    string // iOS simulator UDID when applicable
	Device   string // Android device serial when applicable
	TmpPath  string // intermediate capture path (e.g. Android /sdcard/...)
	OutPath  string
}

// ── Manager ────────────────────────────────────────────────────────────

// RecordingManager owns the active-recording state and the driver set.
// Keyed by internal id (usually runId:taskId).
type RecordingManager struct {
	root      string // recordings root, e.g. ~/.yaver/recordings
	mu        sync.Mutex
	active    map[string]activeRecording
	drivers   map[RecordingTarget]RecordingDriver
}

type activeRecording struct {
	Handle RecordingHandle
	State  driverState
	Driver RecordingDriver
}

func NewRecordingManager(root string) *RecordingManager {
	return &RecordingManager{
		root:    root,
		active:  map[string]activeRecording{},
		drivers: defaultRecordingDrivers(),
	}
}

// DefaultRecordingManager is rooted at ~/.yaver/recordings.
func DefaultRecordingManager() *RecordingManager {
	base, err := ConfigDir()
	if err != nil {
		base = "."
	}
	return NewRecordingManager(filepath.Join(base, "recordings"))
}

// Drivers returns the driver name → Available map so yaver doctor can
// render the table.
func (m *RecordingManager) Drivers() map[string]RecordingDriverStatus {
	out := map[string]RecordingDriverStatus{}
	for target, d := range m.drivers {
		ok, reason := d.Available()
		out[string(target)] = RecordingDriverStatus{
			Driver:    d.Name(),
			Target:    string(target),
			Available: ok,
			Reason:    reason,
		}
	}
	return out
}

// RecordingDriverStatus is surfaced to doctor + /recordings/drivers.
type RecordingDriverStatus struct {
	Driver    string `json:"driver"`
	Target    string `json:"target"`
	Available bool   `json:"available"`
	Reason    string `json:"reason,omitempty"`
}

// Start begins a recording for (runID, taskID). If `target` is empty or
// the requested driver isn't available, falls back to screen capture.
// Returns ErrNoDriverAvailable if nothing on this host can record.
func (m *RecordingManager) Start(ctx context.Context, runID, taskID string, target RecordingTarget) (*RecordingHandle, error) {
	runID = sanitizeMorningID(runID)
	taskID = sanitizeMorningID(taskID)
	if runID == "" || taskID == "" {
		return nil, fmt.Errorf("runID and taskID required")
	}
	id := runID + ":" + taskID

	m.mu.Lock()
	if _, running := m.active[id]; running {
		m.mu.Unlock()
		return nil, fmt.Errorf("recording %s is already running", id)
	}
	m.mu.Unlock()

	driver, resolvedTarget, err := m.pickDriver(target)
	if err != nil {
		return nil, err
	}

	dir := filepath.Join(m.root, runID, taskID)
	if err := ensureRecordingDir(dir); err != nil {
		return nil, err
	}
	outPath := filepath.Join(dir, "video.mp4")

	state, err := driver.Start(ctx, outPath, runID, taskID)
	if err != nil {
		return nil, fmt.Errorf("%s.Start: %w", driver.Name(), err)
	}
	handle := RecordingHandle{
		ID:      id,
		Target:  resolvedTarget,
		Driver:  driver.Name(),
		RunID:   runID,
		TaskID:  taskID,
		Path:    outPath,
		Started: time.Now(),
	}
	m.mu.Lock()
	m.active[id] = activeRecording{Handle: handle, State: state, Driver: driver}
	m.mu.Unlock()
	return &handle, nil
}

// Stop finalizes the recording for (runID, taskID) and returns its
// on-disk stats. Safe to call even if Start failed partway through; in
// that case it's a no-op that returns an "not running" error.
func (m *RecordingManager) Stop(runID, taskID string) (*RecordingResult, error) {
	id := sanitizeMorningID(runID) + ":" + sanitizeMorningID(taskID)
	m.mu.Lock()
	rec, ok := m.active[id]
	if ok {
		delete(m.active, id)
	}
	m.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("no active recording for %s", id)
	}
	if err := rec.Driver.Stop(rec.State); err != nil {
		return nil, err
	}
	info, err := statMp4(rec.Handle.Path)
	if err != nil {
		return nil, err
	}
	return &RecordingResult{
		Handle:     rec.Handle,
		DurationMs: int(time.Since(rec.Handle.Started).Milliseconds()),
		SizeBytes:  info.Size(),
	}, nil
}

// HasAnyAppDemoDriver reports whether this host can record a
// product-demo video right now (i.e. ios-sim OR android-emu is
// ready). The autodev hook consults this before attempting to record
// so it can skip gracefully on a headless Linux box without making
// noise.
func (m *RecordingManager) HasAnyAppDemoDriver() bool {
	for _, pref := range []RecordingTarget{RecordingTargetIOSSim, RecordingTargetAndroidEmu} {
		if d, ok := m.drivers[pref]; ok {
			if ready, _ := d.Available(); ready {
				return true
			}
		}
	}
	return false
}

// Active reports which recordings are live. Keyed by internal id.
func (m *RecordingManager) Active() []RecordingHandle {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]RecordingHandle, 0, len(m.active))
	for _, a := range m.active {
		out = append(out, a.Handle)
	}
	return out
}

// pickDriver resolves the requested target to an available driver.
//
// Two cardinal rules:
//
//  1. The `screen` driver is NEVER auto-selected. The morning match
//     report is supposed to show the *finished product* — the app
//     running in a simulator / emulator / browser — not the IDE or
//     terminal. Capturing the whole desktop would violate that. Only
//     explicit callers (`yaver record start … screen`) get it.
//
//  2. When the caller omits a target, we try `ios-sim` then
//     `android-emu` in that order. If neither is available we
//     return a clear not-available error so the autodev hook can
//     log-and-skip instead of blocking the run.
func (m *RecordingManager) pickDriver(target RecordingTarget) (RecordingDriver, RecordingTarget, error) {
	// Explicit request: honor it even for screen (advanced).
	if target != "" {
		if d, ok := m.drivers[target]; ok {
			if ready, _ := d.Available(); ready {
				return d, target, nil
			}
			_, reason := d.Available()
			if reason == "" {
				reason = "driver not available"
			}
			return nil, "", fmt.Errorf("%s: %s", d.Name(), reason)
		}
		return nil, "", fmt.Errorf("unknown recording target %q", target)
	}

	// Auto-pick: product-demo priority order.
	for _, pref := range []RecordingTarget{RecordingTargetIOSSim, RecordingTargetAndroidEmu} {
		if d, ok := m.drivers[pref]; ok {
			if ready, _ := d.Available(); ready {
				return d, pref, nil
			}
		}
	}
	return nil, "", errNoAppDemoDriver
}

// errNoAppDemoDriver is a sentinel the autodev hook uses to decide
// "skip video, keep running" vs. surface a hard error. Callers that
// need the message should wrap with fmt.Errorf.
var errNoAppDemoDriver = fmt.Errorf("no product-demo recording driver available: boot an iOS Simulator (`xcrun simctl boot`) or attach an Android device/emulator. Screen capture is NOT used by default — the morning reel shows the finished app, not the IDE")

// ── Driver set ────────────────────────────────────────────────────────

func defaultRecordingDrivers() map[RecordingTarget]RecordingDriver {
	return map[RecordingTarget]RecordingDriver{
		RecordingTargetScreen:     &ffmpegScreenDriver{},
		RecordingTargetIOSSim:     &iosSimDriver{},
		RecordingTargetAndroidEmu: &androidEmuDriver{},
	}
}

// ── Platform helpers ──────────────────────────────────────────────────

func ensureRecordingDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}
	return nil
}

// statMp4 stats the produced file and verifies a positive size. Used
// at Stop to surface empty-recording failures early. Shared helpers
// (like removeFileIfExists) live in recording_helpers.go.
func statMp4(path string) (fs.FileInfo, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("stat recording %s: %w", path, err)
	}
	if info.Size() == 0 {
		return nil, fmt.Errorf("recording at %s is zero bytes — capture likely failed before any frames were written", path)
	}
	return info, nil
}

// Platform-name the user sees in doctor output.
func platformDescription() string {
	switch runtime.GOOS {
	case "darwin":
		return "macOS"
	case "linux":
		return "Linux"
	case "windows":
		return "Windows"
	default:
		return strings.Title(runtime.GOOS)
	}
}

// ffmpegLookup reports whether ffmpeg is on PATH and returns its
// full path when available.
func ffmpegLookup() (string, bool) {
	p, err := exec.LookPath("ffmpeg")
	if err != nil {
		return "", false
	}
	return p, true
}
