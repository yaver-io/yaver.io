package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The guard is the only thing standing between an autorun loop and someone's
// signing key, so it gets the most coverage here. A class being wrong is
// survivable; the guard being wrong is not.

func isolateDiskGuardGitAncestry(t *testing.T) {
	t.Helper()
	previous := diskGuardGitWorkTreeProbe
	diskGuardGitWorkTreeProbe = func(string) (string, bool) { return "", false }
	t.Cleanup(func() { diskGuardGitWorkTreeProbe = previous })
}

func TestDiskGuardVerbsRegisteredOwnerOnly(t *testing.T) {
	opsRegistryMu.RLock()
	defer opsRegistryMu.RUnlock()
	for _, name := range []string{"diskguard_scan", "diskguard_clear", "diskguard_sweep"} {
		spec, ok := opsRegistry[name]
		if !ok {
			t.Fatalf("%s not registered", name)
		}
		if spec.AllowCompanion {
			t.Errorf("%s must be owner-only: a guest could delete files", name)
		}
		if spec.Schema == nil {
			t.Errorf("%s has nil schema — it would be undiscoverable via ops_verbs", name)
		}
		if spec.Handler == nil {
			t.Errorf("%s has nil handler", name)
		}
	}
}

// Regression (2026-09-07, Ubuntu test box): diskguard_scan reported a full
// filesystem and recommended find_large_files, but that name is an MCP-only
// tool and `yaver ops find_large_files` rejected it. The recovery text must
// point at a registered read-only ops verb that the same caller can invoke.
func TestDiskGuardNoCandidatesNamesCallableRecoveryVerb(t *testing.T) {
	recommendation := diskGuardNoCandidatesRecommendation(100, 85)
	if strings.Contains(recommendation, "find_large_files") {
		t.Fatalf("recommendation still names the unavailable ops verb: %q", recommendation)
	}
	if !strings.Contains(recommendation, "storage_scan") {
		t.Fatalf("recommendation does not name the structured recovery verb: %q", recommendation)
	}
	opsRegistryMu.RLock()
	spec, ok := opsRegistry["storage_scan"]
	opsRegistryMu.RUnlock()
	if !ok || spec.Handler == nil {
		t.Fatal("storage_scan recovery route is not registered")
	}
}

func TestDiskGuardPathAllowedRefusesProtected(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home dir")
	}
	cases := []struct {
		path string
		why  string
	}{
		{"/", "filesystem root"},
		{"/tmp", "top-level path"},
		{filepath.Join(home, ".ssh", "id_rsa"), "ssh key"},
		{filepath.Join(home, ".ssh"), "ssh dir"},
		{filepath.Join(home, ".appstoreconnect", "yaver.env"), "ASC env"},
		{filepath.Join(home, ".yaver", "vault", "blob"), "vault"},
		{"/opt/proj/keys/yaver-upload.keystore", "keystore"},
		{"/opt/proj/AuthKey_ABC.p8", "apple signing key"},
		{"/opt/proj/.env", "env file"},
		{"/opt/proj/.env.production", "env file variant"},
		{"/opt/proj/cert.p12", "p12"},
		{"/opt/proj/keys/google-play-service-account.json", "play service account"},
	}
	for _, c := range cases {
		if ok, reason := diskGuardPathAllowed(c.path); ok {
			t.Errorf("guard ALLOWED %s (%s) — must refuse", c.path, c.why)
		} else if reason == "" {
			t.Errorf("guard refused %s without a reason", c.path)
		}
	}
}

func TestDiskGuardPathAllowedRefusesGitWorkTree(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "myrepo")
	deep := filepath.Join(repo, "sub", "dir")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := runGit(repo, "init"); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	target := filepath.Join(deep, "build-artifact.bin")
	if err := os.WriteFile(target, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	ok, reason := diskGuardPathAllowed(target)
	if ok {
		t.Fatalf("guard allowed a path inside a git work tree: %s", target)
	}
	if !strings.Contains(reason, "git work tree") {
		t.Errorf("expected git-work-tree reason, got %q", reason)
	}

	// A sibling OUTSIDE the repo must still be allowed, otherwise the guard
	// is useless (it would refuse everything).
	outside := filepath.Join(root, "loose.bin")
	if err := os.WriteFile(outside, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if ok, reason := diskGuardPathAllowedWithGitProbe(outside, func(string) (string, bool) { return "", false }); !ok {
		t.Errorf("guard refused a safe path %s: %s", outside, reason)
	}
}

// Regression (2026-09-06, ubuntu-4gb): an empty /root/.git directory was
// enough to make every ~/.yaver cache look like source code. Diskguard then
// reported 0 B reclaimable while superseded agents occupied 2 GB and the disk
// had reached 100%. Only actual Git metadata may establish this boundary.
func TestDiskGuardIgnoresEmptyGitSentinel(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "cache", "artifact.bin")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if repo, ok := diskGuardInGitWorkTree(target); ok {
		t.Fatalf("empty .git sentinel classified %s as source rooted at %s", target, repo)
	}
}

func TestOpencodeArtifactRegex(t *testing.T) {
	// Real names observed on the box that filled to 100%.
	match := []string{
		".3adf9ebbffffffff-00000000.so",
		".3adfdffbbfebe7ef-00000000.so",
		".3addfebbfbb3ffef-00000000.so",
	}
	for _, m := range match {
		if !opencodeArtifactRe.MatchString(m) {
			t.Errorf("should match opencode artifact: %s", m)
		}
	}
	// Near-misses that must NOT match — a real library, a non-hidden file,
	// a different suffix. Over-matching here deletes someone's .so.
	noMatch := []string{
		"libssl.so",
		"libc.so.6",
		".hidden.so",
		"3adf9ebbffffffff-00000000.so",  // not hidden
		".3adf9ebbffffffff-00000001.so", // non-zero suffix
		".3adf9ebbffffffff-00000000.dylib",
		".zzzz-00000000.so", // non-hex
	}
	for _, m := range noMatch {
		if opencodeArtifactRe.MatchString(m) {
			t.Errorf("must NOT match: %s", m)
		}
	}
}

func TestDiskGuardOldAgentsKeepsCurrentAndNewest(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	binDir := filepath.Join(home, ".yaver", "bin")
	for _, v := range []string{"1.99.100", "1.99.293", "1.99.299", "1.99.306"} {
		d := filepath.Join(binDir, v)
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(d, "yaver"), []byte(strings.Repeat("x", 1024)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// `current` points at an OLDER version than the newest present — this is
	// the real situation after an npm upgrade that hasn't been restarted into
	// yet, and the live binary must survive regardless of version order.
	if err := os.Symlink(filepath.Join(binDir, "1.99.299"), filepath.Join(binDir, "current")); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}

	cands, err := diskGuardCollectOldAgents()
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, c := range cands {
		got[filepath.Base(c.Path)] = true
	}
	if got["1.99.299"] {
		t.Error("must never propose the version `current` points to")
	}
	if got["1.99.306"] {
		t.Error("must keep the newest version as a rollback spare")
	}
	if !got["1.99.100"] || !got["1.99.293"] {
		t.Errorf("expected superseded versions to be reclaimable, got %v", got)
	}
}

func TestDiskGuardOldAgentsKeepsSpareWhenCurrentIsNewest(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	binDir := filepath.Join(home, ".yaver", "bin")
	for _, v := range []string{"1.99.450", "1.99.451", "1.99.452"} {
		d := filepath.Join(binDir, v)
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(d, "yaver"), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(filepath.Join(binDir, "1.99.452"), filepath.Join(binDir, "current")); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}

	cands, err := diskGuardCollectOldAgents()
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, c := range cands {
		got[filepath.Base(c.Path)] = true
	}
	if got["1.99.452"] || got["1.99.451"] {
		t.Fatalf("current and newest spare must survive, candidates: %v", got)
	}
	if !got["1.99.450"] {
		t.Fatalf("oldest version should be reclaimable, candidates: %v", got)
	}
}

// Regression (2026-08-10, ubuntu-4gb): `1.99.406-dev` and
// `current.stale-1.99.299` accumulated because semverDirRe only matched strict
// `X.Y.Z` — dev builds and pre-swap backup trees were invisible to the guard,
// so a box carried 441M of superseded binaries while diskguard reported zero.
// Both classes must now be reclaimable; `current.stale-*` must NEVER be kept
// as the rollback spare (the npm launcher re-downloads any release).
func TestDiskGuardOldAgentsCatchesDevAndStaleCurrent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	binDir := filepath.Join(home, ".yaver", "bin")
	for _, v := range []string{"1.99.100", "1.99.406-dev", "1.99.408-dev", "1.99.410", "1.99.411.released-backup", "1.99.411.released-redownload", "current.stale-1.99.299"} {
		d := filepath.Join(binDir, v)
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(d, "yaver"), []byte(strings.Repeat("x", 1024)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// current → 1.99.411; 1.99.410 is the newest other real release and is the
	// one rollback spare. All older and non-release trees remain reclaimable.
	if err := os.Symlink(filepath.Join(binDir, "1.99.411"), filepath.Join(binDir, "current")); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	d411 := filepath.Join(binDir, "1.99.411")
	if err := os.MkdirAll(d411, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d411, "yaver"), []byte(strings.Repeat("y", 1024)), 0o644); err != nil {
		t.Fatal(err)
	}

	cands, err := diskGuardCollectOldAgents()
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, c := range cands {
		got[filepath.Base(c.Path)] = true
	}
	if got["1.99.411"] {
		t.Error("must never propose the version `current` points to")
	}
	if got["1.99.410"] {
		t.Error("must keep the newest real release as the rollback spare")
	}
	for _, v := range []string{"1.99.100", "1.99.406-dev", "1.99.408-dev", "1.99.411.released-backup", "1.99.411.released-redownload", "current.stale-1.99.299"} {
		if !got[v] {
			t.Errorf("expected %q to be reclaimable (dev/stale-current must not accumulate), got %v", v, got)
		}
	}
}

func TestDiskGuardClearDryRunDeletesNothing(t *testing.T) {
	isolateDiskGuardGitAncestry(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	binDir := filepath.Join(home, ".yaver", "bin")
	for _, v := range []string{"1.99.100", "1.99.293", "1.99.306"} {
		d := filepath.Join(binDir, v)
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(d, "yaver"), []byte(strings.Repeat("x", 2048)), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	res := diskGuardRunClear("/", []string{"yaver-old-agents"}, true, time.Minute, false)
	if !res.DryRun {
		t.Fatal("dryRun flag lost")
	}
	if res.DeletedFiles == 0 {
		t.Fatal("dry run should still report what it WOULD delete")
	}
	// The whole point: nothing actually gone.
	for _, v := range []string{"1.99.100", "1.99.293", "1.99.306"} {
		if _, err := os.Stat(filepath.Join(binDir, v)); err != nil {
			t.Errorf("dry run deleted %s — it must not touch the filesystem", v)
		}
	}
}

func TestDiskGuardClearActuallyDeletesWhenAsked(t *testing.T) {
	isolateDiskGuardGitAncestry(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	binDir := filepath.Join(home, ".yaver", "bin")
	for _, v := range []string{"1.99.100", "1.99.306"} {
		d := filepath.Join(binDir, v)
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(d, "yaver"), []byte(strings.Repeat("x", 2048)), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	res := diskGuardRunClear("/", []string{"yaver-old-agents"}, false, time.Minute, false)
	if res.FreedBytes == 0 {
		t.Fatal("expected to free bytes")
	}
	if _, err := os.Stat(filepath.Join(binDir, "1.99.100")); !os.IsNotExist(err) {
		t.Error("superseded version should be gone")
	}
	if _, err := os.Stat(filepath.Join(binDir, "1.99.306")); err != nil {
		t.Error("newest version must be kept as a rollback spare")
	}
}

func TestDiskGuardSweepBelowThresholdDoesNotDelete(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	binDir := filepath.Join(home, ".yaver", "bin", "1.99.100")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(binDir, "yaver"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	// threshold 100 can never be met (df reports < 100 on a working box), so
	// sweep stays in below-threshold mode. It may hold the FIFO cap on
	// always-enforce classes, but it must NOT reclaim anything else — an
	// agent binary is a rollback path, not garbage.
	res := diskGuardSweepHandler(OpsContext{}, json.RawMessage(`{"thresholdPercent":100}`))
	if !res.OK {
		t.Fatalf("sweep failed: %s", res.Error)
	}
	view, ok := res.Initial.(diskGuardSweepResult)
	if !ok {
		t.Fatalf("unexpected result type %T", res.Initial)
	}
	if view.Clear != nil {
		for _, c := range view.Clear.Classes {
			if c.Class == "yaver-old-agents" {
				t.Error("below-threshold sweep must not consider yaver-old-agents")
			}
		}
	}
	if _, err := os.Stat(binDir); err != nil {
		t.Error("sweep below threshold deleted an agent version it must keep")
	}
}

func TestDiskGuardApplyFIFOKeepsNewestEvictsOldest(t *testing.T) {
	now := time.Now()
	mk := func(name string, ageMin int) diskGuardCandidate {
		return diskGuardCandidate{
			Path:    "/tmp/" + name,
			Bytes:   4551776,
			ModTime: now.Add(-time.Duration(ageMin) * time.Minute),
		}
	}
	// Deliberately unsorted input — FIFO must not depend on collection order.
	in := []diskGuardCandidate{
		mk("mid", 30), mk("oldest", 500), mk("newest", 1), mk("old", 200), mk("recent", 5),
	}
	out := diskGuardApplyFIFO(in, 2)

	// Two newest survive.
	for _, c := range out {
		if strings.HasSuffix(c.Path, "newest") || strings.HasSuffix(c.Path, "recent") {
			t.Errorf("FIFO evicted a kept-newest artifact: %s", c.Path)
		}
	}
	if len(out) != 3 {
		t.Fatalf("expected 3 evictions, got %d", len(out))
	}
	// Oldest-first ordering: an interrupted pass should have removed the
	// oldest bytes, not a random scatter.
	if !strings.HasSuffix(out[0].Path, "oldest") {
		t.Errorf("expected oldest first, got %s", out[0].Path)
	}
	if !strings.HasSuffix(out[len(out)-1].Path, "mid") {
		t.Errorf("expected newest-of-the-evicted last, got %s", out[len(out)-1].Path)
	}
}

func TestDiskGuardApplyFIFOUnderCapEvictsNothing(t *testing.T) {
	now := time.Now()
	in := []diskGuardCandidate{
		{Path: "/tmp/a", ModTime: now.Add(-time.Hour)},
		{Path: "/tmp/b", ModTime: now.Add(-2 * time.Hour)},
	}
	if out := diskGuardApplyFIFO(in, 3); len(out) != 0 {
		t.Errorf("under the cap nothing should be evicted, got %d", len(out))
	}
	// keepNewest == 0 means "no FIFO retention" — every candidate is fair game
	// (the old-agents class does its own version-aware keep).
	if out := diskGuardApplyFIFO(in, 0); len(out) != 2 {
		t.Errorf("keepNewest=0 should evict all candidates, got %d", len(out))
	}
}

func TestDiskGuardEnforceOnlySelectsPureGarbageClasses(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	binDir := filepath.Join(home, ".yaver", "bin")
	for _, v := range []string{"1.99.100", "1.99.306"} {
		if err := os.MkdirAll(filepath.Join(binDir, v), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(binDir, v, "yaver"), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// enforceOnly must skip yaver-old-agents (not AlwaysEnforce): a
	// below-threshold sweep has no business removing rollback binaries.
	reports, _, _ := diskGuardCollect(nil, time.Minute, true)
	for _, r := range reports {
		if r.Class == "yaver-old-agents" {
			t.Error("enforceOnly sweep must not touch yaver-old-agents")
		}
	}
	// Without enforceOnly it is in scope again.
	reports, _, _ = diskGuardCollect(nil, time.Minute, false)
	found := false
	for _, r := range reports {
		if r.Class == "yaver-old-agents" {
			found = true
		}
	}
	if !found {
		t.Error("full sweep should include yaver-old-agents")
	}
}

func TestDiskGuardBadPayloadIsTyped(t *testing.T) {
	for _, h := range []VerbHandler{diskGuardScanHandler, diskGuardClearHandler, diskGuardSweepHandler} {
		res := h(OpsContext{}, json.RawMessage(`{"thresholdPercent":`))
		if res.OK {
			t.Error("malformed payload should fail")
		}
		if res.Code != "bad_payload" {
			t.Errorf("expected code bad_payload, got %q", res.Code)
		}
	}
}

func TestDiskGuardStatParsesDf(t *testing.T) {
	fs, err := diskGuardStat("/")
	if err != nil {
		t.Fatalf("df parse failed: %v", err)
	}
	if fs.TotalBytes <= 0 {
		t.Error("total should be positive")
	}
	if fs.UsedPercent < 0 || fs.UsedPercent > 100 {
		t.Errorf("nonsensical used percent: %d", fs.UsedPercent)
	}
	if fs.Human == "" {
		t.Error("human summary should be populated")
	}
}
