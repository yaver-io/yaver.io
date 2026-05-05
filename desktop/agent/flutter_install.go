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
// builds at storage.googleapis.com — but NOT for Linux ARM64. Verified
// against releases_linux.json: zero arm64 archive entries as of
// 3.27.4. For that target we fall through to git-clone (handled by
// the caller). macOS arm64 + x86_64 share a universal archive since
// Flutter 3.10.
func flutterStableTarball() (string, string, bool) {
	const ver = "3.27.4"
	switch runtime.GOOS {
	case "linux":
		switch runtime.GOARCH {
		case "amd64":
			name := fmt.Sprintf("flutter_linux_%s-stable.tar.xz", ver)
			return "https://storage.googleapis.com/flutter_infra_release/releases/stable/linux/" + name, name, true
		case "arm64":
			// No official tarball for Linux ARM64. Caller falls
			// through to git-clone install path.
			return "", "", false
		}
	case "darwin":
		name := fmt.Sprintf("flutter_macos_%s-stable.zip", ver)
		return "https://storage.googleapis.com/flutter_infra_release/releases/stable/macos/" + name, name, true
	}
	return "", "", false
}

// flutterGitClone is the install fallback for platforms without an
// official tarball (today: Linux ARM64). Clones the flutter repo at
// the stable branch into root; `flutter --version` on first run
// fetches the per-arch Dart SDK + completes bootstrap. Returns nil
// on success or a description of why we couldn't clone.
func flutterGitClone(ctx context.Context, root string) error {
	if _, err := exec.LookPath("git"); err != nil {
		return fmt.Errorf("git is required for the Linux ARM64 Flutter install path (no official tarball exists). Install git first.")
	}
	if err := os.MkdirAll(filepath.Dir(root), 0o755); err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, "git", "clone", "--depth", "1", "-b", "stable",
		"https://github.com/flutter/flutter.git", root)
	cmd.Env = augmentEnv(nil)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git clone flutter: %v: %s", err, strings.TrimSpace(string(out)))
	}
	// git safe.directory so flutter doesn't refuse to operate on this
	// checkout when the agent later runs flutter under a different uid.
	safe := exec.CommandContext(ctx, "git", "config", "--global", "--add", "safe.directory", root)
	_ = safe.Run()
	return nil
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
		// Linux ARM64 + any other platform without an official
		// tarball: fall back to git-clone. Slower first run (the
		// `flutter --version` pre-warm has to download the Dart SDK
		// for arm64), but bog-standard supported by Flutter itself.
		logf(fmt.Sprintf("No official Flutter tarball for %s/%s — falling back to git clone (Flutter's supported install for this platform).", runtime.GOOS, runtime.GOARCH))
		if err := flutterGitClone(ctx, root); err != nil {
			return err
		}
		// Pre-warm so the first agent-driven build doesn't pay the
		// snapshot-build cost. Same as the tarball path below.
		warm := exec.CommandContext(ctx, flutterBin, "--version")
		warm.Env = append(augmentEnv(nil), "FLUTTER_ROOT="+root)
		_ = warm.Run()
		logf("Flutter SDK ready at " + root + " (git clone)")
		return ensureFlutterShellPath(progress)
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
