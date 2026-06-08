package studio

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"
)

// base.go — the Yaver Base Image (`yaver-base`): a warm, snapshot-restorable
// redroid /data volume so app-test runs skip the cold path. Cold boot loads
// binder, pulls the image, boots Android (~12s on magara), and (for the Hermes
// path) installs the Yaver container + signs into a test account — all invariant
// across runs. Build pays that ONCE and tars /data into a versioned snapshot; Up
// restores the snapshot and warm-boots (no install, no sign-in), so an agentic
// test of an RN app becomes bundle-push → drive in seconds.
//
// Everything goes through the studio.Runner seam (redroid.go), so build/restore
// work identically on a managed-cloud farm box (LocalRunner) and an on-prem box
// (SSHRunner). Snapshots live on the DOCKER host (where redroid runs), not the
// orchestrating agent — a base must be restored on the same kind of host that
// built it. The tar is moved with a privileged-free alpine helper container so
// host file ownership (redroid writes /data as root) never blocks a non-root
// host user — the same trick pullExchange uses for screenshots.

// BaseManifest is the metadata sidecar written next to every snapshot tar.
type BaseManifest struct {
	Version     string `json:"version"`     // label, e.g. "2026-06-08-1"
	Image       string `json:"image"`       // redroid image the snapshot was built from
	Arch        string `json:"arch"`        // "arm64" | "x86_64"
	SHA256      string `json:"sha256"`      // tar.gz digest (integrity, like the rootfs manifest)
	Bytes       int64  `json:"bytes"`       // tar.gz size
	CreatedAtMs int64  `json:"createdAtMs"` // build time
	YaverBaked  bool   `json:"yaverBaked"`  // Yaver container installed in /data
	Width       int    `json:"width"`
	Height      int    `json:"height"`
	DPI         int    `json:"dpi"`
}

// BaseSpec parameterizes a base build/restore. Runner + HostWorkDir + SnapshotDir
// are required; the rest default.
type BaseSpec struct {
	R           Runner
	Image       string // redroid image; default redroid 13
	HostWorkDir string // the live /data dir bind-mounted into redroid (built/restored in place)
	SnapshotDir string // host dir holding versioned snapshots (arch subdir added automatically)
	Version     string // build: snapshot label; up: which version (empty → latest)
	Container   string // container name; default "yaver-base"
	YaverAPK    string // optional: Yaver mobile (or app) APK to bake into the base
	Width       int
	Height      int
	DPI         int

	// Prepare runs once during Build after the APK install, against the live
	// surface — the seam to sign into a DISPOSABLE test account / seed fixtures
	// before snapshotting. Never bake the owner's real account (privacy
	// contract). Nil → snapshot the freshly-installed state as-is.
	Prepare func(ctx context.Context, d Driver) error

	Log func(string)
}

const baseHelperImage = "alpine"

func (b *BaseSpec) logf(format string, a ...any) {
	if b.Log != nil {
		b.Log(fmt.Sprintf(format, a...))
	}
}

func (b *BaseSpec) defaults() {
	if b.Container == "" {
		b.Container = "yaver-base"
	}
	if b.Image == "" {
		b.Image = "redroid/redroid:13.0.0-latest"
	}
}

// DetectArch maps the host's `uname -m` to a snapshot arch label. A snapshot is
// not portable across arches (it carries an Android system image), so Up refuses
// to restore a snapshot built for a different arch.
func DetectArch(ctx context.Context, r Runner) (string, error) {
	out, err := r.Exec(ctx, "uname -m")
	if err != nil {
		return "", fmt.Errorf("uname -m: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return normalizeArch(strings.TrimSpace(string(out))), nil
}

func normalizeArch(m string) string {
	switch strings.ToLower(m) {
	case "aarch64", "arm64":
		return "arm64"
	case "x86_64", "amd64":
		return "x86_64"
	default:
		return m
	}
}

// archDir is SnapshotDir/<arch>.
func (b *BaseSpec) archDir(arch string) string { return path.Join(b.SnapshotDir, arch) }

// surface builds a RedroidSurface for this base (shared by build + restore).
func (b *BaseSpec) surface() *RedroidSurface {
	return &RedroidSurface{
		R: b.R, Name: b.Container, Image: b.Image, HostWorkDir: b.HostWorkDir,
		Width: b.Width, Height: b.Height, DPI: b.DPI, Log: b.Log,
	}
}

// Build provisions a fresh redroid, optionally installs the Yaver/app APK, runs
// Prepare, then tars /data into a versioned snapshot and writes the manifest.
// The container is torn down at the end — the artifact is the tar, not a running
// box.
func (b *BaseSpec) Build(ctx context.Context) (*BaseManifest, error) {
	b.defaults()
	if b.HostWorkDir == "" || b.SnapshotDir == "" {
		return nil, fmt.Errorf("base build: HostWorkDir and SnapshotDir required")
	}
	arch, err := DetectArch(ctx, b.R)
	if err != nil {
		return nil, err
	}
	version := strings.TrimSpace(b.Version)
	if version == "" {
		version = time.Now().Format("2006-01-02") + "-1"
	}

	// Start from a clean /data so the golden image is reproducible (redroid
	// writes /data as root, so clear via the helper container).
	b.logf("preparing clean /data at %s", b.HostWorkDir)
	if err := b.clearData(ctx); err != nil {
		return nil, fmt.Errorf("clear data: %w", err)
	}

	surf := b.surface()
	if err := surf.Provision(ctx); err != nil {
		return nil, fmt.Errorf("provision: %w", err)
	}
	// Always tear the build container down; the snapshot is what we keep.
	defer surf.Teardown(context.WithoutCancel(ctx)) //nolint:errcheck

	baked := false
	if strings.TrimSpace(b.YaverAPK) != "" {
		b.logf("baking %s into the base", path.Base(b.YaverAPK))
		if err := surf.Install(ctx, b.YaverAPK); err != nil {
			return nil, fmt.Errorf("bake apk: %w", err)
		}
		baked = true
	}
	if b.Prepare != nil {
		b.logf("running base Prepare (test-account sign-in / fixtures)")
		if err := b.Prepare(ctx, surf.Driver()); err != nil {
			return nil, fmt.Errorf("prepare: %w", err)
		}
	}

	// Stop the container before snapshotting so /data is quiescent (Android has
	// flushed). Teardown removes it; we re-provision on Up.
	b.logf("stopping container to quiesce /data")
	if err := surf.Teardown(ctx); err != nil {
		return nil, fmt.Errorf("pre-snapshot teardown: %w", err)
	}

	dir := b.archDir(arch)
	tarRel := version + ".tar.gz"
	if _, err := b.exec(ctx, "mkdir -p "+shellQuote(dir)); err != nil {
		return nil, fmt.Errorf("mkdir snapshot dir: %w", err)
	}

	b.logf("snapshotting /data → %s", path.Join(dir, tarRel))
	// tar /data + compute the digest INSIDE the helper so neither depends on the
	// host user being able to read root-owned files.
	snap := fmt.Sprintf(
		"docker run --rm -v %s:/data:ro -v %s:/out %s sh -c %s",
		shellQuote(b.HostWorkDir), shellQuote(dir), baseHelperImage,
		shellQuote(fmt.Sprintf(
			"cd /data && tar czf /out/%s . && cd /out && sha256sum %s > %s.sha256 && chmod a+rw %s %s.sha256",
			tarRel, tarRel, tarRel, tarRel, tarRel)))
	if out, err := b.exec(ctx, snap); err != nil {
		return nil, fmt.Errorf("snapshot tar: %w (%s)", err, out)
	}

	sha, err := b.readDigest(ctx, path.Join(dir, tarRel+".sha256"))
	if err != nil {
		return nil, err
	}
	size, err := b.fileSize(ctx, path.Join(dir, tarRel))
	if err != nil {
		return nil, err
	}

	man := &BaseManifest{
		Version: version, Image: b.Image, Arch: arch, SHA256: sha, Bytes: size,
		CreatedAtMs: time.Now().UnixMilli(), YaverBaked: baked,
		Width: surf.Width, Height: surf.Height, DPI: surf.DPI,
	}
	if err := b.writeManifest(ctx, dir, version, man); err != nil {
		return nil, err
	}
	b.logf("base %s built (%s, %d bytes, sha %s…)", version, arch, size, shortSHA(sha))
	return man, nil
}

// Up restores a snapshot into HostWorkDir and warm-boots redroid, returning the
// live surface ready to install/drive an app. Version empty → latest.
func (b *BaseSpec) Up(ctx context.Context) (*RedroidSurface, *BaseManifest, error) {
	b.defaults()
	if b.HostWorkDir == "" || b.SnapshotDir == "" {
		return nil, nil, fmt.Errorf("base up: HostWorkDir and SnapshotDir required")
	}
	arch, err := DetectArch(ctx, b.R)
	if err != nil {
		return nil, nil, err
	}
	dir := b.archDir(arch)
	version := strings.TrimSpace(b.Version)
	if version == "" {
		version, err = b.latestVersion(ctx, dir)
		if err != nil {
			return nil, nil, err
		}
	}
	man, err := b.readManifest(ctx, dir, version)
	if err != nil {
		return nil, nil, fmt.Errorf("read manifest %s: %w", version, err)
	}
	if man.Arch != "" && man.Arch != arch {
		return nil, nil, fmt.Errorf("base %s is %s, host is %s — snapshots are not arch-portable", version, man.Arch, arch)
	}

	tarRel := version + ".tar.gz"
	b.logf("restoring base %s (%s) → %s", version, arch, b.HostWorkDir)
	if _, err := b.exec(ctx, "mkdir -p "+shellQuote(b.HostWorkDir)); err != nil {
		return nil, nil, fmt.Errorf("mkdir host workdir: %w", err)
	}
	// Clear + extract through the helper (root) so we overwrite the prior /data.
	restore := fmt.Sprintf(
		"docker run --rm -v %s:/data -v %s:/out:ro %s sh -c %s",
		shellQuote(b.HostWorkDir), shellQuote(dir), baseHelperImage,
		shellQuote(fmt.Sprintf(
			"rm -rf /data/* /data/.[!.]* /data/..?* 2>/dev/null; cd /data && tar xzf /out/%s", tarRel)))
	if out, err := b.exec(ctx, restore); err != nil {
		return nil, nil, fmt.Errorf("restore tar: %w (%s)", err, out)
	}

	surf := b.surface()
	if man.Width > 0 { // keep the dims the snapshot was built at
		surf.Width, surf.Height, surf.DPI = man.Width, man.Height, man.DPI
	}
	b.logf("warm-booting redroid from restored /data")
	if err := surf.Provision(ctx); err != nil {
		return nil, nil, fmt.Errorf("warm boot: %w", err)
	}
	return surf, man, nil
}

// List returns the manifests of every snapshot for the host's arch, newest first.
func (b *BaseSpec) List(ctx context.Context) ([]BaseManifest, error) {
	b.defaults()
	arch, err := DetectArch(ctx, b.R)
	if err != nil {
		return nil, err
	}
	dir := b.archDir(arch)
	versions, err := b.listVersions(ctx, dir)
	if err != nil {
		return nil, err
	}
	out := make([]BaseManifest, 0, len(versions))
	for _, v := range versions {
		man, err := b.readManifest(ctx, dir, v)
		if err != nil {
			// A tar without a manifest still exists; surface a minimal record.
			out = append(out, BaseManifest{Version: v, Arch: arch})
			continue
		}
		out = append(out, *man)
	}
	return out, nil
}

// GC removes all but the newest `keep` snapshots (tar + sha256 + manifest).
func (b *BaseSpec) GC(ctx context.Context, keep int) ([]string, error) {
	b.defaults()
	if keep < 0 {
		keep = 0
	}
	arch, err := DetectArch(ctx, b.R)
	if err != nil {
		return nil, err
	}
	dir := b.archDir(arch)
	versions, err := b.listVersions(ctx, dir)
	if err != nil {
		return nil, err
	}
	if len(versions) <= keep {
		return nil, nil
	}
	removed := versions[keep:]
	for _, v := range removed {
		_, _ = b.exec(ctx, fmt.Sprintf("rm -f %s %s %s",
			shellQuote(path.Join(dir, v+".tar.gz")),
			shellQuote(path.Join(dir, v+".tar.gz.sha256")),
			shellQuote(path.Join(dir, v+".json"))))
	}
	b.logf("gc removed %d snapshot(s): %s", len(removed), strings.Join(removed, ", "))
	return removed, nil
}

// --- helpers ---

func (b *BaseSpec) exec(ctx context.Context, cmd string) (string, error) {
	out, err := b.R.Exec(ctx, cmd)
	if err != nil {
		return string(out), fmt.Errorf("%s: %w: %s", b.R.Label(), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

// clearData wipes HostWorkDir's contents via the helper (root-owned files).
func (b *BaseSpec) clearData(ctx context.Context) error {
	if _, err := b.exec(ctx, "mkdir -p "+shellQuote(b.HostWorkDir)); err != nil {
		return err
	}
	_, err := b.exec(ctx, fmt.Sprintf(
		"docker run --rm -v %s:/data %s sh -c %s",
		shellQuote(b.HostWorkDir), baseHelperImage,
		shellQuote("rm -rf /data/* /data/.[!.]* /data/..?* 2>/dev/null || true")))
	return err
}

// listVersions returns snapshot version labels in dir, newest first. Labels sort
// lexically descending — the default "YYYY-MM-DD-N" scheme orders correctly.
func (b *BaseSpec) listVersions(ctx context.Context, dir string) ([]string, error) {
	out, err := b.exec(ctx, fmt.Sprintf("ls -1 %s 2>/dev/null || true", shellQuote(dir)))
	if err != nil {
		return nil, err
	}
	var versions []string
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasSuffix(line, ".tar.gz") {
			versions = append(versions, strings.TrimSuffix(line, ".tar.gz"))
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(versions)))
	return versions, nil
}

func (b *BaseSpec) latestVersion(ctx context.Context, dir string) (string, error) {
	versions, err := b.listVersions(ctx, dir)
	if err != nil {
		return "", err
	}
	if len(versions) == 0 {
		return "", fmt.Errorf("no base snapshots in %s (run `yaver studio base build`)", dir)
	}
	return versions[0], nil
}

func (b *BaseSpec) readDigest(ctx context.Context, shaPath string) (string, error) {
	out, err := b.exec(ctx, "cat "+shellQuote(shaPath))
	if err != nil {
		return "", fmt.Errorf("read sha: %w", err)
	}
	fields := strings.Fields(out)
	if len(fields) == 0 {
		return "", fmt.Errorf("empty sha256 file %s", shaPath)
	}
	return fields[0], nil
}

func (b *BaseSpec) fileSize(ctx context.Context, p string) (int64, error) {
	out, err := b.exec(ctx, "wc -c < "+shellQuote(p))
	if err != nil {
		return 0, fmt.Errorf("size: %w", err)
	}
	n, err := strconv.ParseInt(strings.TrimSpace(out), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse size %q: %w", strings.TrimSpace(out), err)
	}
	return n, nil
}

// writeManifest marshals the manifest locally and PutFiles it next to the tar.
func (b *BaseSpec) writeManifest(ctx context.Context, dir, version string, man *BaseManifest) error {
	data, err := json.MarshalIndent(man, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp("", "base-manifest-*.json")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	_, _ = tmp.Write(data)
	tmp.Close()
	defer os.Remove(tmpPath)
	if err := b.R.PutFile(ctx, tmpPath, path.Join(dir, version+".json")); err != nil {
		return fmt.Errorf("write manifest: %w", err)
	}
	return nil
}

func (b *BaseSpec) readManifest(ctx context.Context, dir, version string) (*BaseManifest, error) {
	out, err := b.exec(ctx, "cat "+shellQuote(path.Join(dir, version+".json")))
	if err != nil {
		return nil, err
	}
	var man BaseManifest
	if err := json.Unmarshal([]byte(out), &man); err != nil {
		return nil, fmt.Errorf("parse manifest: %w", err)
	}
	if man.Version == "" {
		man.Version = version
	}
	return &man, nil
}

func shortSHA(s string) string {
	if len(s) > 12 {
		return s[:12]
	}
	return s
}
