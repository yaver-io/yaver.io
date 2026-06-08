package studio

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// IOSSimSurface drives an iOS Simulator via `xcrun simctl` (and `idb` for UI
// taps when present). Simulators are macOS-only, so the Runner must target a Mac
// — the owner's Mac, a Mac in the managed fleet, or a paired Mac-mini. This is
// the iOS half of the capture layer; the screenshot + preview-video path mirrors
// the existing `shots`/`vibe_preview` iOS recording, now behind the same
// CaptureSurface/Driver interfaces as redroid.
//
// iOS has no foreground-service concept, so the FGS verbs are no-ops/errors on
// this surface; iOS permission evidence is background-modes + usage-description
// prose (Apple uses review notes, not a Play-style video). The value here is
// screenshots and app-preview videos, which iOS very much needs.
type IOSSimSurface struct {
	R      Runner
	Device string // simulator UDID or name; empty = the currently "booted" one
	Log    func(string)

	hostTmp string
}

func (s *IOSSimSurface) Platform() string { return "ios" }
func (s *IOSSimSurface) Driver() Driver   { return &iosDriver{s: s} }

func (s *IOSSimSurface) logf(format string, a ...any) {
	if s.Log != nil {
		s.Log(fmt.Sprintf(format, a...))
	}
}

func (s *IOSSimSurface) target() string {
	if strings.TrimSpace(s.Device) == "" {
		return "booted"
	}
	return s.Device
}

func (s *IOSSimSurface) exec(ctx context.Context, cmd string) (string, error) {
	out, err := s.R.Exec(ctx, cmd)
	if err != nil {
		return string(out), fmt.Errorf("%s: %w: %s", s.R.Label(), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

func (s *IOSSimSurface) Provision(ctx context.Context) error {
	if s.hostTmp == "" {
		s.hostTmp = "/tmp/yaver-studio-ios"
	}
	_, _ = s.exec(ctx, "mkdir -p "+shellQuote(s.hostTmp))
	if s.target() != "booted" {
		s.logf("booting simulator %s", s.Device)
		_, _ = s.exec(ctx, "xcrun simctl boot "+shellQuote(s.Device)+" 2>/dev/null || true")
		_, _ = s.exec(ctx, "xcrun simctl bootstatus "+shellQuote(s.Device)+" -b 2>/dev/null || true")
	}
	// confirm a booted device exists
	out, err := s.exec(ctx, "xcrun simctl list devices booted 2>/dev/null")
	if err != nil || !strings.Contains(out, "(Booted)") {
		return fmt.Errorf("no booted iOS simulator (boot one or pass --device): %v", err)
	}
	s.logf("simulator booted")
	return nil
}

// Install accepts a built .app simulator bundle (simctl installs .app, not .ipa).
func (s *IOSSimSurface) Install(ctx context.Context, artifactPath string) error {
	// transfer if the runner is remote (no-op for LocalRunner copying onto itself
	// would clobber; only PutFile when the host differs).
	remote := artifactPath
	if _, ok := s.R.(LocalRunner); !ok {
		remote = filepath.Join(s.hostTmp, filepath.Base(artifactPath))
		if err := s.R.PutFile(ctx, artifactPath, remote); err != nil {
			return err
		}
	}
	_, err := s.exec(ctx, "xcrun simctl install "+s.target()+" "+shellQuote(remote))
	return err
}

func (s *IOSSimSurface) Teardown(ctx context.Context) error {
	// Leave a pre-existing "booted" sim alone; only shut down one we named.
	if s.target() != "booted" {
		_, _ = s.exec(ctx, "xcrun simctl shutdown "+shellQuote(s.Device)+" 2>/dev/null || true")
	}
	return nil
}

type iosDriver struct {
	s         *IOSSimSurface
	recording bool
}

func (d *iosDriver) Launch(ctx context.Context, app App) error {
	_, err := d.s.exec(ctx, "xcrun simctl launch "+d.s.target()+" "+shellQuote(app.Package))
	return err
}
func (d *iosDriver) ForceStop(ctx context.Context, app App) error {
	_, err := d.s.exec(ctx, "xcrun simctl terminate "+d.s.target()+" "+shellQuote(app.Package))
	return err
}

// idb-backed UI verbs (best-effort; require `idb` installed on the Mac).
func (d *iosDriver) Tap(ctx context.Context, x, y int) error {
	_, err := d.s.exec(ctx, fmt.Sprintf("idb ui tap --udid %s %d %d", shellQuote(d.s.target()), x, y))
	return err
}
func (d *iosDriver) Type(ctx context.Context, text string) error {
	_, err := d.s.exec(ctx, "idb ui text --udid "+shellQuote(d.s.target())+" "+shellQuote(text))
	return err
}
func (d *iosDriver) Key(ctx context.Context, key string) error {
	_, err := d.s.exec(ctx, "idb ui key --udid "+shellQuote(d.s.target())+" "+shellQuote(key))
	return err
}
func (d *iosDriver) TapText(ctx context.Context, text string) error {
	return fmt.Errorf("TapText on iOS needs an accessibility describe step (idb ui describe-all) — use Maestro flow or coordinate Tap")
}
func (d *iosDriver) WaitText(ctx context.Context, text string, timeoutSec int) error {
	return fmt.Errorf("WaitText not implemented on iOS surface (use Maestro flow)")
}
func (d *iosDriver) Back(ctx context.Context) error { return nil }
func (d *iosDriver) Home(ctx context.Context) error {
	_, err := d.s.exec(ctx, "xcrun simctl io "+d.s.target()+" home 2>/dev/null || true")
	return err
}

// iOS has no foreground service / notification-shade equivalent.
func (d *iosDriver) StartForegroundService(ctx context.Context, component, action string) error {
	return fmt.Errorf("foreground services are Android-only; iOS uses background modes (see Info.plist analysis)")
}
func (d *iosDriver) StopService(ctx context.Context, component string) error { return nil }
func (d *iosDriver) ExpandNotifications(ctx context.Context) error           { return nil }
func (d *iosDriver) CollapseNotifications(ctx context.Context) error         { return nil }
func (d *iosDriver) NotificationText(ctx context.Context) (string, error)    { return "", nil }

func (d *iosDriver) Screenshot(ctx context.Context) ([]byte, error) {
	host := filepath.Join(d.s.hostTmp, "shot.png")
	if _, err := d.s.exec(ctx, "xcrun simctl io "+d.s.target()+" screenshot "+shellQuote(host)); err != nil {
		return nil, err
	}
	return d.pull(ctx, host)
}

func (d *iosDriver) RecordStart(ctx context.Context, maxSec int) error {
	host := filepath.Join(d.s.hostTmp, "rec.mp4")
	_, _ = d.s.exec(ctx, "rm -f "+shellQuote(host))
	// background recordVideo; stop via SIGINT for a clean file.
	_, err := d.s.exec(ctx, fmt.Sprintf("nohup xcrun simctl io %s recordVideo --codec=h264 %s >/dev/null 2>&1 & echo started",
		d.s.target(), shellQuote(host)))
	if err == nil {
		d.recording = true
	}
	return err
}

func (d *iosDriver) RecordStop(ctx context.Context) ([]byte, error) {
	if !d.recording {
		return nil, fmt.Errorf("not recording")
	}
	d.recording = false
	_, _ = d.s.exec(ctx, `pkill -INT -f "simctl io.*recordVideo" || true`)
	time.Sleep(2 * time.Second)
	return d.pull(ctx, filepath.Join(d.s.hostTmp, "rec.mp4"))
}

func (d *iosDriver) pull(ctx context.Context, host string) ([]byte, error) {
	if _, ok := d.s.R.(LocalRunner); ok {
		return os.ReadFile(host)
	}
	tmp, err := os.CreateTemp("", "studio-ios-*"+filepath.Ext(host))
	if err != nil {
		return nil, err
	}
	p := tmp.Name()
	tmp.Close()
	defer os.Remove(p)
	if err := d.s.R.GetFile(ctx, host, p); err != nil {
		return nil, err
	}
	return os.ReadFile(p)
}
