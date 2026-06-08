package studio

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// RedroidSurface drives a real Android (AOSP) instance via redroid
// (Android-in-Docker) on a Linux host — no emulator, no KVM. Verified
// end-to-end on an on-prem x86 box (Ubuntu 20.04, kernel 5.4) on 2026-06-08:
// boot ~12s, app install + run, foreground-service notification, screen
// recording. Every step here mirrors that proven sequence.
//
// Host file exchange uses the container's /data, bind-mounted to HostWorkDir on
// the host, so screenshots/recordings written under /data/local/tmp/studio are
// directly retrievable via the Runner without docker-cp'ing Android's FUSE
// /sdcard (which does not work).
type RedroidSurface struct {
	R           Runner
	Name        string // container name
	Image       string // e.g. redroid/redroid:13.0.0-latest
	HostWorkDir string // absolute host dir bind-mounted to the container's /data
	Port        int    // host port for adb 5555 — only published when PublishADB
	PublishADB  bool   // publish -p Port:5555 (off by default: blocks multi-container; surface uses docker exec)
	Width       int
	Height      int
	DPI         int

	Log func(string) // optional progress sink
}

const (
	redroidExchangeContainer = "/data/local/tmp/studio" // inside the container
	redroidExchangeHostSub   = "local/tmp/studio"       // relative to HostWorkDir
)

func (s *RedroidSurface) Platform() string { return "android" }
func (s *RedroidSurface) Driver() Driver {
	s.defaults()
	return &redroidDriver{s: s}
}

func (s *RedroidSurface) logf(format string, a ...any) {
	if s.Log != nil {
		s.Log(fmt.Sprintf(format, a...))
	}
}

func (s *RedroidSurface) defaults() {
	if s.Name == "" {
		s.Name = "yaver-studio-redroid"
	}
	if s.Image == "" {
		s.Image = "redroid/redroid:13.0.0-latest"
	}
	if s.Port == 0 {
		s.Port = 5555
	}
	if s.Width == 0 {
		s.Width, s.Height, s.DPI = 1080, 2340, 440
	}
}

// de wraps a command to run inside the redroid container.
func (s *RedroidSurface) de(inner string) string {
	return fmt.Sprintf("docker exec %s sh -c %s", s.Name, shellQuote(inner))
}

func (s *RedroidSurface) exec(ctx context.Context, cmd string) (string, error) {
	out, err := s.R.Exec(ctx, cmd)
	if err != nil {
		return string(out), fmt.Errorf("%s: %w: %s", s.R.Label(), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

// Provision loads binder, boots the container, and waits for Android.
func (s *RedroidSurface) Provision(ctx context.Context) error {
	s.defaults()
	if s.HostWorkDir == "" {
		return fmt.Errorf("redroid: HostWorkDir required")
	}

	// 1. binder_linux in the HOST kernel, loaded WITHOUT host sudo via a
	//    privileged helper container (the Docker daemon is root). Proven path.
	if out, _ := s.exec(ctx, "lsmod | grep -c '^binder_linux' || true"); strings.TrimSpace(out) == "0" || strings.TrimSpace(out) == "" {
		s.logf("loading binder_linux via privileged helper")
		_, err := s.exec(ctx, `docker run --rm --privileged -v /lib/modules:/lib/modules debian:bullseye-slim bash -c `+
			shellQuote(`apt-get update -qq >/dev/null 2>&1; apt-get install -y -qq kmod >/dev/null 2>&1; modprobe binder_linux devices=binder,hwbinder,vndbinder || modprobe binder_linux`))
		if err != nil {
			return fmt.Errorf("load binder_linux (host kernel may lack the module; install linux-modules-extra-$(uname -r)): %w", err)
		}
	}

	// 2. boot redroid (privileged; it mounts binderfs itself). No --rm so a
	//    crash leaves logs.
	s.logf("booting redroid %s", s.Image)
	_, _ = s.exec(ctx, fmt.Sprintf("docker rm -f %s >/dev/null 2>&1 || true", s.Name))
	_, _ = s.exec(ctx, fmt.Sprintf("mkdir -p %s", shellQuote(s.HostWorkDir)))
	// No `-p 5555:5555` publish: the surface drives via `docker exec`, not adb
	// over TCP, so the host port mapping is pointless AND it blocks running more
	// than one redroid container (a base + a qa instance fight over 5555 —
	// observed on magara 2026-06-09). Optional adb-over-TCP can re-add an
	// ephemeral port later if a use case needs it.
	pubPort := ""
	if s.Port > 0 && s.PublishADB {
		pubPort = fmt.Sprintf("-p %d:5555 ", s.Port)
	}
	runCmd := fmt.Sprintf(
		"docker run -itd --privileged --name %s -v %s:/data %s%s "+
			"androidboot.redroid_width=%d androidboot.redroid_height=%d androidboot.redroid_dpi=%d",
		s.Name, shellQuote(s.HostWorkDir), pubPort, s.Image, s.Width, s.Height, s.DPI)
	if out, err := s.exec(ctx, runCmd); err != nil {
		return fmt.Errorf("redroid run: %w (%s)", err, out)
	}

	// 3. wait for boot_completed
	s.logf("waiting for Android boot")
	deadline := time.Now().Add(150 * time.Second)
	for time.Now().Before(deadline) {
		out, _ := s.exec(ctx, s.de("getprop sys.boot_completed"))
		if strings.TrimSpace(out) == "1" {
			s.logf("Android booted")
			// ensure the exchange dir exists
			_, _ = s.exec(ctx, s.de("mkdir -p "+redroidExchangeContainer))
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(3 * time.Second):
		}
	}
	logs, _ := s.exec(ctx, fmt.Sprintf("docker logs --tail 30 %s 2>&1", s.Name))
	return fmt.Errorf("redroid did not boot in time; logs:\n%s", logs)
}

// EnsureReady provisions the surface only if it is not already booted, so a warm
// base (a container left running by `yaver studio base up`) is REUSED instead of
// torn down and rebuilt. A cold/absent container takes the full Provision path.
// This is what lets the android-redroid test target attach to a warm Yaver Base
// Image in seconds rather than cold-booting every run.
func (s *RedroidSurface) EnsureReady(ctx context.Context) error {
	s.defaults()
	if s.HostWorkDir == "" {
		return fmt.Errorf("redroid: HostWorkDir required")
	}
	if out, err := s.exec(ctx, s.de("getprop sys.boot_completed")); err == nil && strings.TrimSpace(out) == "1" {
		s.logf("redroid %s already warm — reusing", s.Name)
		_, _ = s.exec(ctx, s.de("mkdir -p "+redroidExchangeContainer))
		return nil
	}
	return s.Provision(ctx)
}

// Install transfers the artifact to the host, copies it into the container, and
// pm-installs it (no adb needed on the host).
func (s *RedroidSurface) Install(ctx context.Context, artifactPath string) error {
	hostAPK := filepath.Join(s.HostWorkDir, "studio-install.apk")
	s.logf("transferring %s", filepath.Base(artifactPath))
	if err := s.R.PutFile(ctx, artifactPath, hostAPK); err != nil {
		return fmt.Errorf("put apk: %w", err)
	}
	if _, err := s.exec(ctx, fmt.Sprintf("docker cp %s %s:/data/local/tmp/app.apk", shellQuote(hostAPK), s.Name)); err != nil {
		return fmt.Errorf("docker cp apk: %w", err)
	}
	out, err := s.exec(ctx, s.de("pm install -r -g /data/local/tmp/app.apk"))
	if err != nil || !strings.Contains(out, "Success") {
		return fmt.Errorf("pm install: %v: %s", err, strings.TrimSpace(out))
	}
	s.logf("installed")
	return nil
}

func (s *RedroidSurface) Teardown(ctx context.Context) error {
	if s.Name == "" {
		return nil
	}
	_, err := s.exec(ctx, fmt.Sprintf("docker rm -f %s >/dev/null 2>&1 || true", s.Name))
	return err
}

// --- Driver implementation ---

type redroidDriver struct {
	s         *RedroidSurface
	recording bool
}

func (d *redroidDriver) Launch(ctx context.Context, app App) error {
	target := app.Package
	if app.Activity != "" {
		target = app.Package + "/" + app.Activity
		_, err := d.s.exec(ctx, d.s.de("am start -n "+target))
		return err
	}
	_, err := d.s.exec(ctx, d.s.de(fmt.Sprintf("monkey -p %s -c android.intent.category.LAUNCHER 1", app.Package)))
	return err
}

func (d *redroidDriver) ForceStop(ctx context.Context, app App) error {
	_, err := d.s.exec(ctx, d.s.de("am force-stop "+app.Package))
	return err
}

func (d *redroidDriver) StartForegroundService(ctx context.Context, component, action string) error {
	cmd := "am start-foreground-service -n " + component
	if action != "" {
		cmd += " -a " + action
	}
	_, err := d.s.exec(ctx, d.s.de(cmd))
	return err
}

func (d *redroidDriver) StopService(ctx context.Context, component string) error {
	_, err := d.s.exec(ctx, d.s.de("am stopservice -n "+component))
	return err
}

func (d *redroidDriver) Tap(ctx context.Context, x, y int) error {
	_, err := d.s.exec(ctx, d.s.de(fmt.Sprintf("input tap %d %d", x, y)))
	return err
}

func (d *redroidDriver) Type(ctx context.Context, text string) error {
	// input text uses %s for spaces; escape per `input` quirks.
	esc := strings.ReplaceAll(text, " ", "%s")
	_, err := d.s.exec(ctx, d.s.de("input text "+shellQuote(esc)))
	return err
}

var keyAliases = map[string]string{
	"HOME": "KEYCODE_HOME", "BACK": "KEYCODE_BACK", "ENTER": "KEYCODE_ENTER",
	"TAB": "KEYCODE_TAB", "APP_SWITCH": "KEYCODE_APP_SWITCH",
}

func (d *redroidDriver) Key(ctx context.Context, key string) error {
	kc := keyAliases[strings.ToUpper(key)]
	if kc == "" {
		kc = key
	}
	_, err := d.s.exec(ctx, d.s.de("input keyevent "+kc))
	return err
}

func (d *redroidDriver) Back(ctx context.Context) error { return d.Key(ctx, "BACK") }
func (d *redroidDriver) Home(ctx context.Context) error { return d.Key(ctx, "HOME") }

func (d *redroidDriver) ExpandNotifications(ctx context.Context) error {
	_, err := d.s.exec(ctx, d.s.de("cmd statusbar expand-notifications"))
	return err
}

func (d *redroidDriver) CollapseNotifications(ctx context.Context) error {
	_, err := d.s.exec(ctx, d.s.de("cmd statusbar collapse"))
	return err
}

func (d *redroidDriver) NotificationText(ctx context.Context) (string, error) {
	return d.s.exec(ctx, d.s.de(`dumpsys notification --noredact 2>/dev/null | grep -iE "android.title|android.text|tickerText"`))
}

// uiDump returns the current view hierarchy XML. The dump target MUST be under
// /data (redroid's /sdcard is a FUSE mount that silently rejects writes — the
// same reason Screenshot uses the /data exchange dir; verified on magara
// 2026-06-09 where /sdcard/window_dump.xml came back "Invalid argument").
// NOTE: uiautomator dump is itself unreliable on some headless redroid images
// (no idle UiAutomation) — callers should treat an empty tree as "no dump" and
// fall back to the vision path, not as a blank screen.
func (d *redroidDriver) uiDump(ctx context.Context) (string, error) {
	const p = redroidExchangeContainer + "/window_dump.xml"
	return d.s.exec(ctx, d.s.de("uiautomator dump "+p+" >/dev/null 2>&1; cat "+p+" 2>/dev/null"))
}

// ViewTree returns the current UIAutomator view hierarchy XML. Exported (and
// surfaced via the Dumper interface) so out-of-package consumers — the testkit
// android-redroid adapter — can resolve selectors against the same dump the
// in-package flows use.
func (d *redroidDriver) ViewTree(ctx context.Context) (string, error) {
	return d.uiDump(ctx)
}

// Logcat returns the last `lines` of the device log — the channel the crash /
// red-box oracles inspect. -d dumps and exits (no streaming).
func (d *redroidDriver) Logcat(ctx context.Context, lines int) (string, error) {
	if lines <= 0 {
		lines = 300
	}
	return d.s.exec(ctx, d.s.de(fmt.Sprintf("logcat -d -t %d", lines)))
}

var boundsRe = regexp.MustCompile(`\[(\d+),(\d+)\]\[(\d+),(\d+)\]`)

// findTextCenter parses a uiautomator dump for a node whose text/content-desc
// contains `text` and returns its center coordinates.
func findTextCenter(xml, text string) (int, int, bool) {
	// crude but dependency-free: split into node tags, find one mentioning text,
	// pull its bounds attribute.
	for _, frag := range strings.Split(xml, "<node ") {
		if !strings.Contains(frag, `text="`) && !strings.Contains(frag, `content-desc="`) {
			continue
		}
		if !strings.Contains(frag, text) {
			continue
		}
		m := boundsRe.FindStringSubmatch(frag)
		if m == nil {
			continue
		}
		x1, _ := strconv.Atoi(m[1])
		y1, _ := strconv.Atoi(m[2])
		x2, _ := strconv.Atoi(m[3])
		y2, _ := strconv.Atoi(m[4])
		return (x1 + x2) / 2, (y1 + y2) / 2, true
	}
	return 0, 0, false
}

func (d *redroidDriver) TapText(ctx context.Context, text string) error {
	dump, err := d.uiDump(ctx)
	if err != nil {
		return err
	}
	x, y, ok := findTextCenter(dump, text)
	if !ok {
		return fmt.Errorf("text %q not found on screen", text)
	}
	return d.Tap(ctx, x, y)
}

func (d *redroidDriver) WaitText(ctx context.Context, text string, timeoutSec int) error {
	deadline := time.Now().Add(time.Duration(timeoutSec) * time.Second)
	for {
		dump, _ := d.uiDump(ctx)
		if strings.Contains(dump, text) {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out waiting for %q", text)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

func (d *redroidDriver) Screenshot(ctx context.Context) ([]byte, error) {
	const cpath = redroidExchangeContainer + "/shot.png"
	if _, err := d.s.exec(ctx, d.s.de("screencap -p "+cpath)); err != nil {
		return nil, err
	}
	return d.pullExchange(ctx, "shot.png")
}

func (d *redroidDriver) RecordStart(ctx context.Context, maxSec int) error {
	if maxSec <= 0 || maxSec > 180 {
		maxSec = 180
	}
	// screenrecord must target /sdcard; we copy it into the bind-mount on stop.
	_, err := d.s.exec(ctx, fmt.Sprintf("docker exec -d %s screenrecord --bit-rate 6000000 --time-limit %d /sdcard/studio-rec.mp4", d.s.Name, maxSec))
	if err == nil {
		d.recording = true
	}
	return err
}

func (d *redroidDriver) RecordStop(ctx context.Context) ([]byte, error) {
	if !d.recording {
		return nil, fmt.Errorf("not recording")
	}
	d.recording = false
	// SIGINT screenrecord for a clean moov atom, give it a moment to flush.
	_, _ = d.s.exec(ctx, d.s.de("pkill -INT screenrecord || true"))
	time.Sleep(2 * time.Second)
	if _, err := d.s.exec(ctx, d.s.de("cp /sdcard/studio-rec.mp4 "+redroidExchangeContainer+"/rec.mp4")); err != nil {
		return nil, err
	}
	return d.pullExchange(ctx, "rec.mp4")
}

// pullExchange makes a file under the container exchange dir world-readable on
// the host (it is root-owned because docker writes it) and retrieves its bytes.
func (d *redroidDriver) pullExchange(ctx context.Context, name string) ([]byte, error) {
	hostSub := filepath.Join(s_host(d.s), name)
	// chmod via a helper container so a non-sudo host user can read it.
	_, _ = d.s.exec(ctx, fmt.Sprintf("docker run --rm -v %s:/d alpine chmod -R a+rX /d/%s 2>/dev/null || true",
		shellQuote(d.s.HostWorkDir), redroidExchangeHostSub))
	tmp, err := os.CreateTemp("", "studio-*"+filepath.Ext(name))
	if err != nil {
		return nil, err
	}
	tmpPath := tmp.Name()
	tmp.Close()
	defer os.Remove(tmpPath)
	if err := d.s.R.GetFile(ctx, hostSub, tmpPath); err != nil {
		return nil, fmt.Errorf("retrieve %s: %w", name, err)
	}
	return os.ReadFile(tmpPath)
}

// s_host returns the host path of an exchange file.
func s_host(s *RedroidSurface) string {
	return filepath.Join(s.HostWorkDir, redroidExchangeHostSub)
}
