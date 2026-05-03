package main

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// fakeRunnerBinary writes a tiny shell script at <dir>/<name> that prints
// `output` to stdout when invoked, then returns its absolute path. Used
// to simulate "binary named codex on PATH that isn't OpenAI Codex" and
// "binary that genuinely is codex" without depending on real installs
// in the test environment.
func fakeRunnerBinary(t *testing.T, dir, name, output string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell-script fakes don't run on windows; skip on this platform")
	}
	path := filepath.Join(dir, name)
	body := "#!/bin/sh\nprintf '%s\\n' " + shellSingleQuote(output) + "\n"
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatalf("write fake runner: %v", err)
	}
	return path
}

func shellSingleQuote(s string) string {
	return "'" + s + "'"
}

func TestVerifyRunnerBinarySignature_AcceptsRealVersionBanner(t *testing.T) {
	runnerSignatureCacheClear()
	dir := t.TempDir()
	cases := []struct {
		runnerID string
		name     string
		banner   string
		wantVer  string
	}{
		{"claude", "claude", "Claude Code 1.0.42", "Claude Code 1.0.42"},
		{"codex", "codex", "codex-cli 0.21.0", "codex-cli 0.21.0"},
		{"opencode", "opencode", "opencode 0.1.34", "opencode 0.1.34"},
	}
	for _, c := range cases {
		t.Run(c.runnerID, func(t *testing.T) {
			path := fakeRunnerBinary(t, dir, c.name+"-"+c.runnerID, c.banner)
			ok, version := verifyRunnerBinarySignature(c.runnerID, path)
			if !ok {
				t.Fatalf("expected ok=true for %s banner %q", c.runnerID, c.banner)
			}
			if version != c.wantVer {
				t.Fatalf("version = %q, want %q", version, c.wantVer)
			}
		})
	}
}

func TestVerifyRunnerBinarySignature_RejectsForeignBinary(t *testing.T) {
	runnerSignatureCacheClear()
	dir := t.TempDir()
	// VS Code's `code` wrapper is the canonical false-positive — same
	// 4-letter prefix, totally different tool. A user with a `~/code/`
	// directory on PATH or a shell function named `codex` produces the
	// same shape of garbage banner.
	path := fakeRunnerBinary(t, dir, "codex", "Visual Studio Code 1.94.2")
	ok, version := verifyRunnerBinarySignature("codex", path)
	if ok {
		t.Fatalf("expected ok=false for foreign binary, got version=%q", version)
	}
}

func TestVerifyRunnerBinarySignature_HandlesMissingBinary(t *testing.T) {
	runnerSignatureCacheClear()
	ok, version := verifyRunnerBinarySignature("codex", "/no/such/path/codex")
	if ok || version != "" {
		t.Fatalf("expected ok=false version=\"\" for missing path, got ok=%v version=%q", ok, version)
	}
}

func TestVerifyRunnerBinarySignature_EmptyPath(t *testing.T) {
	runnerSignatureCacheClear()
	ok, version := verifyRunnerBinarySignature("codex", "")
	if ok || version != "" {
		t.Fatalf("empty path must return false, got ok=%v version=%q", ok, version)
	}
}

func TestVerifyRunnerBinarySignature_AcceptsBareVersionForOpenCode(t *testing.T) {
	runnerSignatureCacheClear()
	dir := t.TempDir()
	// opencode 1.4.0+ prints only the version number, with no banner
	// containing the word "opencode". The signature substring check
	// alone would reject it. Verify the bare-version fallback accepts.
	cases := []struct {
		banner  string
		wantVer string
	}{
		{"1.4.0", "1.4.0"},
		{"v1.4.0", "v1.4.0"},
		{"1.4.0-rc.2", "1.4.0-rc.2"},
	}
	for _, c := range cases {
		t.Run(c.banner, func(t *testing.T) {
			path := fakeRunnerBinary(t, dir, "opencode-"+c.banner, c.banner)
			runnerSignatureCacheClear()
			ok, version := verifyRunnerBinarySignature("opencode", path)
			if !ok {
				t.Fatalf("expected ok=true for opencode banner %q, got false", c.banner)
			}
			if version != c.wantVer {
				t.Fatalf("version = %q, want %q", version, c.wantVer)
			}
		})
	}
}

func TestVerifyRunnerBinarySignature_BareVersionDoesNotAcceptForeign(t *testing.T) {
	runnerSignatureCacheClear()
	dir := t.TempDir()
	// "Visual Studio Code 1.94.2" must still be rejected for codex —
	// it contains a version, but also other words, so it's not a bare
	// version banner. Guards against the relaxed opencode fallback
	// accidentally letting foreign binaries through for codex/claude.
	path := fakeRunnerBinary(t, dir, "codex-foreign-extra", "Visual Studio Code 1.94.2")
	ok, _ := verifyRunnerBinarySignature("codex", path)
	if ok {
		t.Fatalf("foreign banner with surrounding words must still be rejected")
	}
}

func TestVerifyRunnerBinarySignature_UnknownRunnerIsTrusted(t *testing.T) {
	runnerSignatureCacheClear()
	// We only ship signatures for the three first-class runners. A
	// custom runner (e.g. the user's own opencode-fork) shouldn't be
	// rejected just because we don't know its banner shape.
	dir := t.TempDir()
	path := fakeRunnerBinary(t, dir, "weirdtool", "weirdtool 9.9.9")
	ok, _ := verifyRunnerBinarySignature("weirdtool", path)
	if !ok {
		t.Fatalf("unknown runners must be trusted (returned false)")
	}
}

func TestVerifyRunnerBinarySignature_CachesResult(t *testing.T) {
	runnerSignatureCacheClear()
	dir := t.TempDir()
	path := fakeRunnerBinary(t, dir, "codex", "codex 0.1.0")
	ok1, _ := verifyRunnerBinarySignature("codex", path)
	if !ok1 {
		t.Fatalf("first probe should succeed")
	}
	// Replace the script with a foreign banner. If caching works the
	// second call still returns ok=true because the result is fresh
	// in cache. (The 5 min TTL plus an explicit cache-clear lets us
	// detect a regression in either direction.)
	if err := os.WriteFile(path, []byte("#!/bin/sh\nprintf 'nothing here\\n'\n"), 0o755); err != nil {
		t.Fatalf("rewrite fake runner: %v", err)
	}
	ok2, _ := verifyRunnerBinarySignature("codex", path)
	if !ok2 {
		t.Fatalf("cached probe should still return ok=true; got false")
	}
	// Now clear the cache and re-probe — should pick up the new
	// (foreign) banner and return ok=false.
	runnerSignatureCacheClear()
	ok3, _ := verifyRunnerBinarySignature("codex", path)
	if ok3 {
		t.Fatalf("after cache clear, foreign banner should be rejected")
	}
}
