package main

// Flutter SDK installer. Mirrors android_sdk_install.go's shape — a
// runFunc the install_cmd.go registry can dispatch to via
// `yaver install flutter` or POST /install/flutter from the phone.
//
// Why we install Flutter ourselves rather than telling users to follow
// flutter.dev: the remote-runtime experience is "phone says reload
// X.flutter project, agent makes it happen". A user who just ran
// `npm install -g yaver-cli` on a fresh Linux dev box doesn't want to
// be told "now go to flutter.dev and pick the right tarball" — they
// want it to work. Same shape as our Hermes/Android self-bootstrap.

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// flutterRoot is /opt/flutter on Linux + macOS. Matches the
// bootstrap.sh layout for yaver-test-ephemeral. Yaver's PATH probes
// pick this up alongside system flutter when both exist.
func flutterRoot() string {
	if v := strings.TrimSpace(os.Getenv("FLUTTER_ROOT")); v != "" {
		return v
	}
	if home, err := os.UserHomeDir(); err == nil {
		// Prefer ~/flutter for non-root devs (no sudo required); fall
		// back to /opt/flutter when running as root (yaver-test-ephemeral).
		if os.Geteuid() != 0 {
			return filepath.Join(home, "flutter")
		}
	}
	return "/opt/flutter"
}

// flutterStableTarball returns the platform-appropriate Flutter SDK
// archive URL + local archive name. flutter.dev publishes per-arch
// builds at storage.googleapis.com; the bootstrap pinned 3.27.4 stable
// for yaver-test-ephemeral arm64. Keep that in lockstep so a phone-
// driven install matches what bootstrap.sh already provisioned.
func flutterStableTarball() (string, string, bool) {
	const ver = "3.27.4"
	switch runtime.GOOS {
	case "linux":
		switch runtime.GOARCH {
		case "arm64":
			name := fmt.Sprintf("flutter_linux_arm64_%s-stable.tar.xz", ver)
			return "https://storage.googleapis.com/flutter_infra_release/releases/stable/linux/" + name, name, true
		case "amd64":
			name := fmt.Sprintf("flutter_linux_%s-stable.tar.xz", ver)
			return "https://storage.googleapis.com/flutter_infra_release/releases/stable/linux/" + name, name, true
		}
	case "darwin":
		// Flutter on macOS ships a single archive (universal binary
		// since Flutter 3.10). One tarball covers both arm64 and x86_64
		// hosts, so we don't branch on GOARCH.
		name := fmt.Sprintf("flutter_macos_%s-stable.zip", ver)
		return "https://storage.googleapis.com/flutter_infra_release/releases/stable/macos/" + name, name, true
	}
	return "", "", false
}

// runFlutterInstall is invoked from the install_cmd registry. The
// progress callback flows through `runInstallPlan` to the same SSE
// stream the phone's "Setting up your dev box…" UI subscribes to,
// so every step shows up live.
func runFlutterInstall(ctx context.Context, progress func(string)) error {
	logf := func(s string) {
		if progress != nil {
			progress(s)
		}
	}

	root := flutterRoot()
	flutterBin := filepath.Join(root, "bin", "flutter")

	if info, err := os.Stat(flutterBin); err == nil && !info.IsDir() && info.Mode()&0o111 != 0 {
		logf(fmt.Sprintf("Flutter already installed at %s — skipping download.", root))
		return ensureFlutterShellPath(progress)
	}

	url, archiveName, ok := flutterStableTarball()
	if !ok {
		return fmt.Errorf("flutter install: unsupported platform %s/%s — run `flutter doctor` manually after installing per flutter.dev/install", runtime.GOOS, runtime.GOARCH)
	}

	logf(fmt.Sprintf("Downloading Flutter SDK (%s) …", archiveName))
	tmpArchive := filepath.Join(os.TempDir(), archiveName)
	if err := downloadFile(ctx, url, tmpArchive); err != nil {
		return fmt.Errorf("download flutter: %w", err)
	}
	defer os.Remove(tmpArchive)

	parent := filepath.Dir(root)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", parent, err)
	}

	logf(fmt.Sprintf("Extracting Flutter SDK to %s …", root))
	switch {
	case strings.HasSuffix(archiveName, ".tar.xz"):
		// tar -C parent -xJf archive — flutter.dev tarballs extract a
		// top-level `flutter/` dir into parent (so root == parent/flutter
		// when the user took our default).
		cmd := exec.CommandContext(ctx, "tar", "-C", parent, "-xJf", tmpArchive)
		cmd.Env = augmentEnv(nil)
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("extract flutter: %v: %s", err, strings.TrimSpace(string(out)))
		}
	case strings.HasSuffix(archiveName, ".zip"):
		if err := unzipArchive(tmpArchive, parent); err != nil {
			return fmt.Errorf("extract flutter zip: %w", err)
		}
	default:
		return fmt.Errorf("flutter install: unrecognised archive type %s", archiveName)
	}

	// flutter.dev archive extracts as `<parent>/flutter/...`; if our
	// chosen root is something else (e.g. ~/flutter-3.27), rename.
	defaultRoot := filepath.Join(parent, "flutter")
	if defaultRoot != root {
		if err := os.RemoveAll(root); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("clean prior flutter root: %w", err)
		}
		if err := os.Rename(defaultRoot, root); err != nil {
			return fmt.Errorf("rename flutter root: %w", err)
		}
	}

	if info, err := os.Stat(flutterBin); err != nil || info.IsDir() {
		return fmt.Errorf("flutter binary missing at %s after extract", flutterBin)
	}

	// Pre-warm `flutter --version` so the first agent-driven build
	// doesn't pay the snapshot-build cost.
	logf("Pre-warming Flutter (`flutter --version`) — first run downloads pub-cache snapshot, ~30s …")
	warm := exec.CommandContext(ctx, flutterBin, "--version")
	warm.Env = append(augmentEnv(nil), "FLUTTER_ROOT="+root)
	_ = warm.Run() // best-effort; failure is just lazy load on first task

	logf("Flutter SDK ready at " + root)
	return ensureFlutterShellPath(progress)
}

// ensureFlutterShellPath drops a /etc/profile.d snippet (when running
// as root) or appends to ~/.profile (otherwise) so non-interactive
// shells the agent spawns find `flutter` on PATH. Idempotent.
func ensureFlutterShellPath(progress func(string)) error {
	root := flutterRoot()
	line := fmt.Sprintf("export PATH=\"%s/bin:$PATH\"\n", root)
	if os.Geteuid() == 0 {
		// /etc/profile.d/flutter.sh — system-wide, picked up by every
		// login shell the agent spawns including ssh sessions.
		path := "/etc/profile.d/flutter.sh"
		if existing, _ := os.ReadFile(path); strings.Contains(string(existing), root+"/bin") {
			return nil
		}
		return os.WriteFile(path, []byte(line), 0o644)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	profile := filepath.Join(home, ".profile")
	existing, _ := os.ReadFile(profile)
	if strings.Contains(string(existing), root+"/bin") {
		return nil
	}
	f, err := os.OpenFile(profile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.WriteString("\n# yaver: flutter sdk\n" + line); err != nil {
		return err
	}
	if progress != nil {
		progress("Appended Flutter to ~/.profile — open a new shell to pick it up.")
	}
	return nil
}

// (runRemoteRuntimeInstall lives in install_cmd.go — extended to call
// flutter + webrtc-stack alongside the existing android-sdk path. This
// file just contributes the Flutter sub-step.)
