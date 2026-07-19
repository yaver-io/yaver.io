package main

// remote_runtime_desktop.go — the host desktop as a first-class remote-runtime
// target ("desktop-screen").
//
// WHY THIS LIVES HERE AND NOT IN /rd/:
// The pre-existing Remote Desktop path (remotedesktop.go, /rd/stream) is MJPEG:
// full-frame JPEG at a hardcoded quality of 55, fps clamped 1-10 with no client
// control, no change detection, and — over the relay — a hard 15-minute cut
// (main.go:11376 tunnel client timeout) plus a held device concurrency slot and
// outbound bytes recorded as literal zero (relay/server.go:1889). It is fine for
// glancing at a box and cannot carry real work.
//
// The remote-runtime layer already solves every one of those problems for
// emulators and simulators: RTP H.264 over WebRTC, an adaptive encode profile
// (getActiveEncodeProfile — fps/width/bitrate), and a single-writer control
// lease (remote_runtime_lease.go) so two clients can't fight over one target.
// Implementing runtimeTarget makes the desktop inherit all of it instead of
// growing a second, worse copy. It also closes the audit finding that GUI
// control had consent+audit but NO lease, while the runtime layer had a lease
// but no consent policy — this target is the first thing wired to both.
//
// Actuation is the existing `ghost` package (screen capture + input injection,
// real on macOS+cgo / Linux-X11 / Windows). Consent is the existing Remote
// Desktop policy (~/.yaver/remotedesktop/policy.json) — NOT the `--ghost` flag.
// That is deliberate: `--ghost` gates unattended automation of someone else's
// box; this is the owner driving their own machine and is governed by the same
// view/control opt-in the /rd/ surface already uses, so flipping control off in
// one place turns it off everywhere.
//
// Video is ffmpeg-native screen grab (avfoundation / x11grab / gdigrab) piped as
// Annex-B H.264 straight into the existing RTP pump — the frames never round-trip
// through JPEG, which is the whole point of moving off /rd/stream.

import (
	"context"
	"fmt"
	"image/png"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"

	"github.com/yaver-io/agent/ghost"
)

// desktopScreenTargetID is the session targetID clients pass to
// runtime_create. Kept as a const so the mobile/web clients and the
// verb schemas can't drift from the dispatch arm.
const desktopScreenTargetID = "desktop-screen"

// desktopGhostEngine is a package-level lazy engine, mirroring how
// ghostStream is a package global. runtimeTarget methods are values with
// no *HTTPServer in scope, so they cannot reach s.ensureGhost(); this is
// the same sync.Once contract, just reachable from here. Both accessors
// construct at most one Engine per process.
var (
	desktopGhostOnce sync.Once
	desktopGhostEng  *ghost.Engine
	desktopGhostErr  error
)

func desktopGhost() (*ghost.Engine, error) {
	desktopGhostOnce.Do(func() {
		desktopGhostEng, desktopGhostErr = ghost.New()
	})
	if desktopGhostErr != nil {
		return nil, fmt.Errorf("desktop capture/input unavailable on %s: %w", runtime.GOOS, desktopGhostErr)
	}
	return desktopGhostEng, nil
}

// desktopScreenTarget streams and drives the host's own desktop.
//
// display is the monitor index; ghost currently enumerates only the primary
// on every OS (screen_darwin.go:70, screen_linux.go:43, screen_windows.go:66
// each return a single Display and error on index != 0), so anything non-zero
// is rejected up front with a clear message rather than failing deep inside a
// capture call.
type desktopScreenTarget struct{ display int }

// ---- consent -------------------------------------------------------------

// desktopControlAllowed applies the Remote Desktop policy to an input
// attempt. The runtime lease answers "who is driving"; this answers
// "is driving permitted at all". Both must pass.
//
// remote=true is the safe default here: a WebRTC session's input arrives
// over a data channel whose original transport we no longer have in scope,
// so we never assume loopback. That matches rdControlEnforce's stricter arm.
func desktopControlAllowed() error {
	pol := loadRemoteDesktopPolicy()
	if ok, reason := rdControlEnforce(pol, true); !ok {
		return fmt.Errorf("%s", reason)
	}
	return nil
}

func desktopViewAllowed() error {
	pol := loadRemoteDesktopPolicy()
	if ok, reason := rdViewEnforce(pol); !ok {
		return fmt.Errorf("%s", reason)
	}
	return nil
}

// ---- runtimeTarget -------------------------------------------------------

func (t desktopScreenTarget) Attach(context.Context) (string, error) {
	if t.display != 0 {
		return "", fmt.Errorf("desktop-screen: only the primary display (0) is supported; multi-monitor enumeration is not implemented in ghost yet")
	}
	if err := desktopViewAllowed(); err != nil {
		return "", err
	}
	if _, err := desktopGhost(); err != nil {
		return "", err
	}
	return desktopScreenTargetID, nil
}

func (t desktopScreenTarget) Tap(_ context.Context, _ string, x, y int) error {
	if err := desktopControlAllowed(); err != nil {
		return err
	}
	eng, err := desktopGhost()
	if err != nil {
		return err
	}
	return eng.Input.Click(ghost.ButtonLeft, x, y)
}

func (t desktopScreenTarget) Swipe(_ context.Context, _ string, x1, y1, x2, y2, _ int) error {
	if err := desktopControlAllowed(); err != nil {
		return err
	}
	eng, err := desktopGhost()
	if err != nil {
		return err
	}
	// durationMs is ignored: ghost.Drag is press→move→release with a fixed
	// inter-event delay per OS (input_windows.go:157, input_linux.go:112).
	// Honouring an arbitrary duration would need motion interpolation the
	// Input interface does not expose.
	return eng.Input.Drag(ghost.ButtonLeft, x1, y1, x2, y2)
}

func (t desktopScreenTarget) Text(_ context.Context, _ string, text string) error {
	if err := desktopControlAllowed(); err != nil {
		return err
	}
	eng, err := desktopGhost()
	if err != nil {
		return err
	}
	return eng.Input.TypeText(text)
}

// Key accepts either a chord ("ctrl+s", "cmd+space") or a bare key name
// ("enter"). ghost.KeyCombo normalizes the names per OS.
func (t desktopScreenTarget) Key(_ context.Context, _ string, key string) error {
	if err := desktopControlAllowed(); err != nil {
		return err
	}
	eng, err := desktopGhost()
	if err != nil {
		return err
	}
	parts := strings.Split(strings.TrimSpace(key), "+")
	keys := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			keys = append(keys, p)
		}
	}
	if len(keys) == 0 {
		return fmt.Errorf("empty key")
	}
	return eng.Input.KeyCombo(keys...)
}

// Pinch has no ghost primitive: ghost.Input is a single-pointer interface
// (Click/Drag/MoveMouse) built on XTest and SendInput, neither of which
// synthesizes a second simultaneous contact. Refusing explicitly beats a
// silent no-op — see errPinchUnsupported.
func (t desktopScreenTarget) Pinch(_ context.Context, _ string, _, _ int, _ float64, _ int) error {
	return fmt.Errorf("%w: ghost drives a single pointer (XTest/SendInput), not multi-touch", errPinchUnsupported)
}

// Navigate opens the URL in the desktop's default browser. Unlike the device
// targets there is no deep-link primitive here — the entry point is the OS
// opener (open/xdg-open/start), which is exactly what a person sitting at this
// machine would do with a link.
//
// It is gated on desktopControlAllowed, not desktopViewAllowed: launching an
// application is an input action on the user's real desktop, not a read of it.
func (t desktopScreenTarget) Navigate(_ context.Context, _, rawURL string) error {
	if err := desktopControlAllowed(); err != nil {
		return err
	}
	target, err := validateNavigateURL(rawURL)
	if err != nil {
		return err
	}
	openBrowser(target)
	return nil
}

// Screenshot is the JPEG/PNG data-channel fallback used when the host can't
// produce an RTP H.264 track (no ffmpeg). It is also what the still-frame
// verbs read.
func (t desktopScreenTarget) Screenshot(_ context.Context, _ string, pngPath string) error {
	if err := desktopViewAllowed(); err != nil {
		return err
	}
	eng, err := desktopGhost()
	if err != nil {
		return err
	}
	img, err := eng.Screen.Capture(t.display)
	if err != nil {
		return fmt.Errorf("desktop capture failed: %w", err)
	}
	f, err := os.Create(pngPath)
	if err != nil {
		return err
	}
	defer f.Close()
	return png.Encode(f, img)
}

func (t desktopScreenTarget) Dims(_ context.Context, _ string) DeviceDims {
	eng, err := desktopGhost()
	if err != nil {
		return DeviceDims{}
	}
	displays, err := eng.Screen.Displays()
	if err != nil || len(displays) == 0 {
		return DeviceDims{}
	}
	d := displays[0]
	for _, cand := range displays {
		if cand.Index == t.display {
			d = cand
			break
		}
	}
	rotation := "landscape"
	if d.Height > d.Width {
		rotation = "portrait"
	}
	return DeviceDims{Width: d.Width, Height: d.Height, Scale: 1.0, Rotation: rotation}
}

func (desktopScreenTarget) NewNALReader(r io.Reader) (nalSource, error) {
	return NewAnnexBReader(r), nil
}

// CanEncodeRTPH264 requires BOTH a working ghost build (so there is a screen
// to grab) and ffmpeg (to encode it). Reporting true without ghost would make
// the session fall through to a capture that errors on first frame.
func (desktopScreenTarget) CanEncodeRTPH264() bool {
	if ffmpegPath() == "" {
		return false
	}
	_, err := desktopGhost()
	return err == nil
}

// SpawnCapture starts ffmpeg grabbing the host screen natively and emitting raw
// H.264 Annex-B on stdout. Unlike streamSourceTarget (which re-encodes a JPEG
// frame buffer fed over stdin), this reads the framebuffer directly — no JPEG
// round-trip, which is the entire reason this target exists.
//
// The encode profile is shared with the rest of the WebRTC layer
// (getActiveEncodeProfile), so bitrate/fps/width adapt exactly as they do for
// emulator sessions instead of being pinned like /rd/stream's quality 55.
func (t desktopScreenTarget) SpawnCapture(ctx context.Context, deviceID string) (*exec.Cmd, io.ReadCloser, error) {
	if err := desktopViewAllowed(); err != nil {
		return nil, nil, err
	}
	ff := ffmpegPath()
	if ff == "" {
		return nil, nil, fmt.Errorf("ffmpeg not found — required for WebRTC screen encode (brew install ffmpeg / apt install ffmpeg)")
	}

	encodeKey := deviceID
	if encodeKey == "" {
		encodeKey = desktopScreenTargetID
	}
	prof := getActiveEncodeProfile(encodeKey)
	fps := prof.FPS
	if fps <= 0 || fps > 30 {
		fps = 12
	}

	grab, err := desktopGrabArgs(fps, t.display)
	if err != nil {
		return nil, nil, err
	}

	args := append([]string{}, grab...)
	args = append(args,
		"-c:v", "libx264", "-preset", "ultrafast", "-tune", "zerolatency",
		"-pix_fmt", "yuv420p", "-g", strconv.Itoa(fps*2), "-bf", "0",
	)
	if prof.MaxWidth > 0 {
		args = append(args, "-vf", fmt.Sprintf("scale='min(%d,iw)':-2", prof.MaxWidth))
	}
	if prof.BitrateKbps > 0 {
		args = append(args,
			"-b:v", fmt.Sprintf("%dk", prof.BitrateKbps),
			"-maxrate", fmt.Sprintf("%dk", prof.BitrateKbps),
			"-bufsize", fmt.Sprintf("%dk", prof.BitrateKbps*2),
		)
	}
	args = append(args, "-f", "h264", "pipe:1")

	cmd := exec.CommandContext(ctx, ff, args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, err
	}
	// ffmpeg is chatty on stderr; discard rather than leak it into agent logs.
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		return nil, nil, fmt.Errorf("ffmpeg screen capture failed to start: %w", err)
	}
	return cmd, stdout, nil
}

// ---- capability probe ----------------------------------------------------

// probeDesktopScreenTarget reports whether this host can stream its own
// desktop, and if not, why — in the same shape every other target uses so the
// n2n picker can render a disabled row with a real reason instead of hiding it.
func probeDesktopScreenTarget() RemoteRuntimeTarget {
	target := RemoteRuntimeTarget{
		ID:          desktopScreenTargetID,
		Label:       "This machine's desktop over WebRTC",
		Platform:    runtime.GOOS,
		HostOS:      runtime.GOOS,
		RequiredCLI: "ffmpeg",
		Surface:     "desktop",
	}
	if _, err := desktopGhost(); err != nil {
		target.Reason = err.Error()
		return target
	}
	if ffmpegPath() == "" {
		target.Reason = "ffmpeg not found — install it to stream the desktop (brew install ffmpeg / apt install ffmpeg)"
		return target
	}
	if _, err := desktopGrabArgs(12, 0); err != nil {
		target.Reason = err.Error()
		return target
	}
	// View is the floor: a box with viewing switched off has nothing to offer.
	// Control is reported separately (the policy is per-request) so the picker
	// can show a view-only session rather than refusing outright.
	if err := desktopViewAllowed(); err != nil {
		target.Reason = err.Error()
		return target
	}
	target.Enabled = true
	return target
}

// ---- app launch ----------------------------------------------------------

// launchDesktopApp starts a native application by name. This is the missing
// primitive the audit flagged: before it, reaching an unlaunched app meant
// synthesizing cmd+space and typing into a launcher, because /rd/input only
// ever accepted pointer and keyboard events.
//
// `name` is an application name ("Safari", "AutoCAD"), not a path — each OS
// resolves it through its own launcher so the caller doesn't need to know
// install locations.
func launchDesktopApp(ctx context.Context, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("launch-app: empty application name")
	}
	// Reject shell metacharacters outright. Every branch below execs a
	// launcher binary directly (no shell), so this is defence in depth
	// rather than the only guard — but `name` arrives over the session
	// command channel and must never be able to grow into a command.
	if strings.ContainsAny(name, ";|&$`\n\r<>") {
		return fmt.Errorf("launch-app: illegal characters in application name %q", name)
	}
	if err := desktopControlAllowed(); err != nil {
		return err
	}

	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.CommandContext(ctx, "open", "-a", name)
	case "windows":
		// `start` is a cmd builtin, so it needs cmd /c. The empty "" is the
		// window title argument — without it, a quoted app path is consumed
		// as the title and nothing launches.
		cmd = exec.CommandContext(ctx, "cmd", "/c", "start", "", name)
	case "linux":
		// gtk-launch takes a .desktop id; fall back to exec'ing the binary
		// directly, which covers anything on PATH.
		if _, err := exec.LookPath("gtk-launch"); err == nil {
			cmd = exec.CommandContext(ctx, "gtk-launch", name)
		} else {
			cmd = exec.CommandContext(ctx, name)
		}
	default:
		return fmt.Errorf("launch-app: unsupported on %s", runtime.GOOS)
	}

	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("launch-app %q failed: %s", name, msg)
	}
	return nil
}

// desktopGrabArgs returns the per-OS ffmpeg input flags for a native screen
// grab. Kept separate so it is unit-testable without spawning ffmpeg.
//
// macOS: avfoundation addresses screens as capture devices. Screen indices
// follow the camera indices, so the correct one varies per machine; ffmpeg's
// own convention and recorder.go:184 both use "1" for the main display on a
// typical laptop. YAVER_DESKTOP_AVF_INDEX overrides without a rebuild.
// ":none" selects no audio input.
//
// Linux: x11grab against $DISPLAY. Wayland is NOT handled — ghost has no
// Wayland path at all, and x11grab under XWayland silently captures a root
// window containing no native-Wayland client content. Failing loudly here is
// better than streaming a blank desktop.
//
// Windows: gdigrab "desktop" grabs the whole virtual screen.
func desktopGrabArgs(fps, display int) ([]string, error) {
	switch runtime.GOOS {
	case "darwin":
		idx := strings.TrimSpace(os.Getenv("YAVER_DESKTOP_AVF_INDEX"))
		if idx == "" {
			idx = "1"
		}
		return []string{
			"-f", "avfoundation",
			"-capture_cursor", "1",
			"-framerate", strconv.Itoa(fps),
			"-i", idx + ":none",
		}, nil
	case "linux":
		if wl := strings.TrimSpace(os.Getenv("WAYLAND_DISPLAY")); wl != "" {
			return nil, fmt.Errorf("desktop-screen: Wayland session detected (WAYLAND_DISPLAY=%s) — screen capture requires X11; ghost has no Wayland backend", wl)
		}
		disp := strings.TrimSpace(os.Getenv("DISPLAY"))
		if disp == "" {
			return nil, fmt.Errorf("desktop-screen: no DISPLAY set — a headless Linux box has no desktop to stream")
		}
		return []string{
			"-f", "x11grab",
			"-framerate", strconv.Itoa(fps),
			"-i", disp,
		}, nil
	case "windows":
		return []string{
			"-f", "gdigrab",
			"-framerate", strconv.Itoa(fps),
			"-i", "desktop",
		}, nil
	default:
		return nil, fmt.Errorf("desktop-screen: screen capture unsupported on %s", runtime.GOOS)
	}
}
