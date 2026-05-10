package main

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// shellGitInit creates a real git repo with one commit and returns the
// repo path. Tests use a real git binary because runGit shells out to
// it; mocking the helper would defeat the purpose of the freshness
// check (which is built around exact git semantics).
func shellGitInit(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"config", "user.email", "test@example.com"},
		{"config", "user.name", "test"},
		{"commit", "--allow-empty", "-q", "-m", "initial"},
	} {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Skipf("git unavailable or failed (%v): %s", err, string(out))
		}
	}
	return dir
}

func gitEmptyCommit(t *testing.T, dir string) {
	t.Helper()
	out, err := exec.Command("git", "-C", dir, "commit", "--allow-empty", "-q", "-m", "another").CombinedOutput()
	if err != nil {
		t.Fatalf("commit failed: %v: %s", err, out)
	}
}

func TestWebBundleStaleVsHead_NotAGitRepo(t *testing.T) {
	dir := t.TempDir() // no .git
	stale, _, ok := webBundleStaleVsHead(dir, time.Now().Format(time.RFC3339))
	if ok {
		t.Fatalf("expected ok=false for non-git workdir, got stale=%v ok=%v", stale, ok)
	}
}

func TestWebBundleStaleVsHead_BuiltAfterHead(t *testing.T) {
	dir := shellGitInit(t)
	// Build is in the future relative to HEAD → not stale.
	future := time.Now().Add(2 * time.Hour).UTC().Format(time.RFC3339)
	stale, _, ok := webBundleStaleVsHead(dir, future)
	if !ok {
		t.Fatalf("expected ok=true for git workdir")
	}
	if stale {
		t.Fatalf("expected stale=false when build is newer than HEAD")
	}
}

func TestWebBundleStaleVsHead_HeadAfterBuild(t *testing.T) {
	dir := shellGitInit(t)
	// Build was an hour ago, then HEAD advanced.
	oldBuilt := time.Now().Add(-1 * time.Hour).UTC().Format(time.RFC3339)
	time.Sleep(1100 * time.Millisecond) // ensure new commit timestamp > oldBuilt
	gitEmptyCommit(t, dir)
	stale, headTime, ok := webBundleStaleVsHead(dir, oldBuilt)
	if !ok {
		t.Fatalf("expected ok=true")
	}
	if !stale {
		t.Fatalf("expected stale=true after new commit; headTime=%s", headTime)
	}
	if headTime.IsZero() {
		t.Fatalf("expected non-zero headTime")
	}
}

func TestWebBundleStaleVsHead_NoBuiltAtTreatsAsStale(t *testing.T) {
	dir := shellGitInit(t)
	stale, _, ok := webBundleStaleVsHead(dir, "")
	if !ok {
		t.Fatalf("expected ok=true")
	}
	if !stale {
		t.Fatalf("expected stale=true when builtAt is empty (no prior build)")
	}
}

func TestWebBundleStaleVsHead_UnparseableBuiltAtIsNotStale(t *testing.T) {
	dir := shellGitInit(t)
	// Garbage timestamp — we should bail out (ok=false) rather than
	// flap into "always stale" and trigger rebuild storms.
	stale, _, ok := webBundleStaleVsHead(dir, "not-a-timestamp")
	if ok {
		t.Fatalf("expected ok=false on unparseable builtAt, got stale=%v ok=%v", stale, ok)
	}
}

func TestResolveWebBundleWorkDir_PrefersExplicitWorkDir(t *testing.T) {
	info := WebBundleInfo{WorkDir: "/explicit/work", BuildDir: "/somewhere/.yaver-build-web"}
	if got := resolveWebBundleWorkDir(info); got != "/explicit/work" {
		t.Fatalf("expected explicit WorkDir, got %q", got)
	}
}

func TestResolveWebBundleWorkDir_DerivesFromBuildDirSuffix(t *testing.T) {
	cases := []struct {
		buildDir, want string
	}{
		{"/root/proj/.yaver-build-web", "/root/proj"},
		{"/root/proj/.yaver-build-web-hermes", "/root/proj"},
	}
	for _, tc := range cases {
		got := resolveWebBundleWorkDir(WebBundleInfo{BuildDir: tc.buildDir})
		if got != tc.want {
			t.Errorf("buildDir=%s: got %q want %q", tc.buildDir, got, tc.want)
		}
	}
}

func TestResolveWebBundleWorkDir_UnknownSuffixReturnsEmpty(t *testing.T) {
	// Don't guess for unrecognized BuildDir layouts — we'd risk
	// running git in the wrong place.
	got := resolveWebBundleWorkDir(WebBundleInfo{BuildDir: "/somewhere/random/dir"})
	if got != "" {
		t.Fatalf("expected empty for unrecognized BuildDir suffix, got %q", got)
	}
}

func TestClaimWebRebuildSlot_SerializesPerWorkDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "proj")
	defer releaseWebRebuildSlot(dir)
	if !claimWebRebuildSlot(dir) {
		t.Fatalf("first claim should succeed")
	}
	if claimWebRebuildSlot(dir) {
		t.Fatalf("second claim should be blocked while slot is held")
	}
	releaseWebRebuildSlot(dir)
	if !claimWebRebuildSlot(dir) {
		t.Fatalf("claim after release should succeed")
	}
}

func TestClaimWebRebuildSlot_DifferentWorkDirsDoNotConflict(t *testing.T) {
	a := filepath.Join(t.TempDir(), "a")
	b := filepath.Join(t.TempDir(), "b")
	defer releaseWebRebuildSlot(a)
	defer releaseWebRebuildSlot(b)
	if !claimWebRebuildSlot(a) {
		t.Fatalf("claim a should succeed")
	}
	if !claimWebRebuildSlot(b) {
		t.Fatalf("claim b should succeed independently of a")
	}
}

func TestRenderWebRebuildingPage_ContainsPollScriptAndTimestamps(t *testing.T) {
	html := string(renderWebRebuildingPage("2026-05-10T08:00:00Z", time.Date(2026, 5, 10, 8, 30, 0, 0, time.UTC)))
	for _, want := range []string{
		"Rebuilding web bundle",
		"/dev/web-bundle/info",
		"location.reload",
		"2026-05-10T08:00:00Z",
		"2026-05-10T08:30:00Z",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("rebuilding page missing %q\n--- page ---\n%s", want, html)
		}
	}
}

func TestRenderWebRebuildingPage_HandlesEmptyBuiltAt(t *testing.T) {
	html := string(renderWebRebuildingPage("", time.Date(2026, 5, 10, 8, 30, 0, 0, time.UTC)))
	if !strings.Contains(html, "(no prior build)") {
		t.Errorf("expected friendly fallback for empty builtAt; got: %s", html)
	}
}
