package studio

import (
	"context"
	"strings"
	"testing"
)

// baseRunner extends the fakeRunner idea with a content store so build can write
// a manifest (PutFile) and up/list can read it back (cat), exercising the full
// snapshot round-trip with no Docker.
type baseRunner struct {
	cmds  []string
	files map[string]string // host path -> contents (PutFile + simulated cat)
	resp  func(cmd string) string
}

func newBaseRunner(resp func(string) string) *baseRunner {
	return &baseRunner{files: map[string]string{}, resp: resp}
}

func (r *baseRunner) Label() string { return "fake-base" }

func (r *baseRunner) Exec(ctx context.Context, cmd string) ([]byte, error) {
	r.cmds = append(r.cmds, cmd)
	// emulate `cat <path>` against the file store (for manifests/sha)
	if strings.HasPrefix(strings.TrimSpace(cmd), "cat ") {
		p := unquoteLast(cmd)
		if v, ok := r.files[p]; ok {
			return []byte(v), nil
		}
	}
	if r.resp != nil {
		return []byte(r.resp(cmd)), nil
	}
	return nil, nil
}

func (r *baseRunner) PutFile(ctx context.Context, local, remote string) error {
	// We can't read the temp file here cheaply; record that a manifest was
	// written so readManifest can be satisfied by tests that pre-seed files.
	if _, ok := r.files[remote]; !ok {
		r.files[remote] = ""
	}
	return nil
}

func (r *baseRunner) GetFile(ctx context.Context, remote, local string) error { return nil }

func (r *baseRunner) saw(substr string) bool {
	for _, c := range r.cmds {
		if strings.Contains(c, substr) {
			return true
		}
	}
	return false
}

// unquoteLast pulls the last single-quoted token out of a command (our paths are
// shellQuote'd), falling back to the last whitespace field.
func unquoteLast(cmd string) string {
	if i := strings.LastIndex(cmd, "'"); i > 0 {
		if j := strings.LastIndex(cmd[:i], "'"); j >= 0 {
			return cmd[j+1 : i]
		}
	}
	f := strings.Fields(cmd)
	if len(f) == 0 {
		return ""
	}
	return f[len(f)-1]
}

func baseResp(cmd string) string {
	switch {
	case strings.Contains(cmd, "uname -m"):
		return "aarch64"
	case strings.Contains(cmd, "getprop sys.boot_completed"):
		return "1"
	case strings.Contains(cmd, "lsmod"):
		return "1"
	case strings.Contains(cmd, "wc -c"):
		return "4096"
	case strings.HasPrefix(strings.TrimSpace(cmd), "cat ") && strings.Contains(cmd, ".sha256"):
		return "deadbeefcafebabe0123  2026-06-08-1.tar.gz"
	}
	return ""
}

func newBaseSpec(r Runner) *BaseSpec {
	return &BaseSpec{
		R: r, HostWorkDir: "/var/lib/yaver/base-data",
		SnapshotDir: "/var/lib/yaver/base", Version: "2026-06-08-1",
	}
}

func TestDetectArch(t *testing.T) {
	r := newBaseRunner(func(cmd string) string {
		if strings.Contains(cmd, "uname -m") {
			return "x86_64\n"
		}
		return ""
	})
	a, err := DetectArch(context.Background(), r)
	if err != nil || a != "x86_64" {
		t.Fatalf("arch = %q, err = %v", a, err)
	}
}

func TestBaseBuildSnapshots(t *testing.T) {
	r := newBaseRunner(func(cmd string) string {
		if strings.Contains(cmd, "cat ") && strings.Contains(cmd, ".sha256") {
			return "deadbeefcafebabe0123  2026-06-08-1.tar.gz"
		}
		return baseResp(cmd)
	})
	b := newBaseSpec(r)
	man, err := b.Build(context.Background())
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if man.Version != "2026-06-08-1" || man.Arch != "arm64" {
		t.Errorf("manifest = %+v", man)
	}
	if man.SHA256 != "deadbeefcafebabe0123" {
		t.Errorf("sha = %q", man.SHA256)
	}
	if man.Bytes != 4096 {
		t.Errorf("bytes = %d", man.Bytes)
	}
	// clean /data before cold boot
	if !r.saw("rm -rf /data/*") {
		t.Error("did not clear /data before build")
	}
	// cold-booted a redroid
	if !r.saw("docker run -itd --privileged --name yaver-base") {
		t.Errorf("did not boot build container; cmds=%v", r.cmds)
	}
	// tarred /data through the helper + digested
	if !r.saw("tar czf /out/2026-06-08-1.tar.gz") || !r.saw("sha256sum 2026-06-08-1.tar.gz") {
		t.Errorf("did not snapshot+digest; cmds=%v", r.cmds)
	}
	// container removed (pre-snapshot quiesce + deferred teardown)
	if !r.saw("docker rm -f yaver-base") {
		t.Error("did not tear the build container down")
	}
}

func TestBaseBuildBakesAPK(t *testing.T) {
	r := newBaseRunner(func(cmd string) string {
		if strings.Contains(cmd, "pm install") {
			return "Success"
		}
		return baseResp(cmd)
	})
	b := newBaseSpec(r)
	b.YaverAPK = "/local/yaver.apk"
	prepared := false
	b.Prepare = func(ctx context.Context, d Driver) error { prepared = true; return nil }
	man, err := b.Build(context.Background())
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if !man.YaverBaked {
		t.Error("manifest should record YaverBaked")
	}
	if !r.saw("pm install -r -g") {
		t.Errorf("did not install the baked apk; cmds=%v", r.cmds)
	}
	if !prepared {
		t.Error("Prepare hook not run")
	}
}

func TestBaseUpRestoresAndWarmBoots(t *testing.T) {
	r := newBaseRunner(baseResp)
	// Pre-seed the manifest the restore reads back.
	r.files["/var/lib/yaver/base/arm64/2026-06-08-1.json"] =
		`{"version":"2026-06-08-1","arch":"arm64","image":"redroid/redroid:13.0.0-latest","width":1080,"height":2340,"dpi":440,"yaverBaked":true}`
	b := newBaseSpec(r)
	surf, man, err := b.Up(context.Background())
	if err != nil {
		t.Fatalf("up: %v", err)
	}
	if man.Version != "2026-06-08-1" || !man.YaverBaked {
		t.Errorf("manifest = %+v", man)
	}
	if surf == nil || surf.Width != 1080 || surf.DPI != 440 {
		t.Errorf("surface dims not taken from manifest: %+v", surf)
	}
	// extracted the tar into /data, then warm-booted
	if !r.saw("tar xzf /out/2026-06-08-1.tar.gz") {
		t.Errorf("did not extract snapshot; cmds=%v", r.cmds)
	}
	if !r.saw("docker run -itd --privileged --name yaver-base") {
		t.Errorf("did not warm-boot; cmds=%v", r.cmds)
	}
}

func TestBaseUpRejectsArchMismatch(t *testing.T) {
	r := newBaseRunner(baseResp) // host = arm64
	r.files["/var/lib/yaver/base/arm64/2026-06-08-1.json"] =
		`{"version":"2026-06-08-1","arch":"x86_64"}`
	b := newBaseSpec(r)
	_, _, err := b.Up(context.Background())
	if err == nil || !strings.Contains(err.Error(), "not arch-portable") {
		t.Fatalf("expected arch-mismatch error, got %v", err)
	}
}

func TestBaseUpLatestWhenVersionEmpty(t *testing.T) {
	r := newBaseRunner(func(cmd string) string {
		if strings.Contains(cmd, "ls -1") {
			return "2026-06-07-1.tar.gz\n2026-06-08-1.tar.gz\n2026-06-08-2.tar.gz\n"
		}
		return baseResp(cmd)
	})
	r.files["/var/lib/yaver/base/arm64/2026-06-08-2.json"] =
		`{"version":"2026-06-08-2","arch":"arm64"}`
	b := newBaseSpec(r)
	b.Version = "" // → latest
	_, man, err := b.Up(context.Background())
	if err != nil {
		t.Fatalf("up latest: %v", err)
	}
	if man.Version != "2026-06-08-2" {
		t.Errorf("latest resolved to %q, want 2026-06-08-2", man.Version)
	}
}

func TestBaseGCKeepsNewest(t *testing.T) {
	r := newBaseRunner(func(cmd string) string {
		if strings.Contains(cmd, "ls -1") {
			return "2026-06-05-1.tar.gz\n2026-06-06-1.tar.gz\n2026-06-07-1.tar.gz\n2026-06-08-1.tar.gz\n"
		}
		return baseResp(cmd)
	})
	b := newBaseSpec(r)
	removed, err := b.GC(context.Background(), 2)
	if err != nil {
		t.Fatalf("gc: %v", err)
	}
	if len(removed) != 2 {
		t.Fatalf("removed = %v, want 2 oldest", removed)
	}
	// keeps the two newest, removes the two oldest
	if removed[0] != "2026-06-06-1" || removed[1] != "2026-06-05-1" {
		t.Errorf("removed wrong versions: %v", removed)
	}
	if !r.saw("rm -f '/var/lib/yaver/base/arm64/2026-06-05-1.tar.gz'") {
		t.Errorf("did not rm the oldest tar; cmds=%v", r.cmds)
	}
}
