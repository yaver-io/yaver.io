package main

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// browser_resolve_test.go — reproduce the ubuntu-4gb box in a temp dir.
//
// These tests do not touch HOME, ~/.yaver, the keychain or any credential
// path: they build fake browser binaries in t.TempDir() and point PATH at
// them. Safe to run with a narrow -run (see
// memory/project_go_test_wipes_real_yaver_auth).
//
// The topology below is not invented. It was MEASURED on the owner's box on
// 2026-08-03:
//
//	chromium              /snap/bin/chromium         exit=1  cannot create temporary directory…
//	chromium-browser      /usr/bin/chromium-browser  exit=1  cannot create temporary directory…
//	google-chrome         /usr/bin/google-chrome     exit=0  Google Chrome 150.0.7871.186
//
// Two shipped defects live in that table, and each has a test here that fails
// when the fix is reverted.

// fakeChromeBinary writes a shell script that behaves like a browser binary.
// exitCode 0 + output = launches; exitCode 1 + the snap message = confined.
func fakeChromeBinary(t *testing.T, dir, name string, exitCode int, output string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	script := "#!/bin/sh\necho '" + output + "'\nexit " + strconv.Itoa(exitCode) + "\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake %s: %v", name, err)
	}
	return path
}

// withOnlyPath replaces PATH with dir for the duration of the test, so the
// resolver can only see the fakes. t.Setenv restores automatically.
func withOnlyPath(t *testing.T, dir string) {
	t.Helper()
	t.Setenv("PATH", dir)
	// HOME too: playwrightChromePath globs ~/Library/Caches/ms-playwright and
	// ~/.cache/ms-playwright, and this machine HAS a Playwright Chromium. Left
	// alone, the "every browser is confined" test would find a real, working
	// browser on the developer's laptop and pass for the wrong reason — while
	// failing in CI. A test that depends on the host's real installs is not a
	// test. (Same discipline as memory/project_go_test_wipes_real_yaver_auth,
	// applied before it can bite.)
	t.Setenv("HOME", t.TempDir())
	originalPlatformCandidates := chromePlatformCandidatePaths
	chromePlatformCandidatePaths = func() []chromeCandidate { return nil }
	t.Cleanup(func() { chromePlatformCandidatePaths = originalPlatformCandidates })
	// The resolver caches; each test must start from a clean probe or it would
	// be asserting the previous test's box.
	invalidateChromeResolution()
	t.Cleanup(invalidateChromeResolution)
}

func TestResolveChrome_StandardMacAppBeatsBrokenPathShim(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script fakes are POSIX-only")
	}
	dir := t.TempDir()
	broken := fakeChromeBinary(t, dir, "chromium", 1, "Chromium.app: No such file or directory")
	working := fakeChromeBinary(t, dir, "Google Chrome", 0, "Google Chrome 150.0.7871.186")
	withOnlyPath(t, dir)
	chromePlatformCandidatePaths = func() []chromeCandidate {
		return []chromeCandidate{{name: "google-chrome-app", path: working}}
	}

	got, attempts := resolveLaunchableChrome(context.Background())
	if got != working {
		t.Fatalf("resolved %q, want installed app %q; attempts: %s", got, working, chromeAttemptsSummary(attempts))
	}
	if len(attempts) != 2 || attempts[0].Path != broken || attempts[0].OK || attempts[1].Path != working || !attempts[1].OK {
		t.Fatalf("resolver did not reject the stale shim before selecting the app: %+v", attempts)
	}
}

const snapFailure = "cannot create temporary directory for the root file system: No such file or directory"

// THE BOX, EXACTLY. Both chromium names are confined snaps; google-chrome
// works. The resolver must skip past the failures and pick Chrome.
//
// NEGATIVE CONTROL for defect #1 (preview_capability_probe.go): the previous
// probeBrowserLaunches searched `chromium` first and RETURNED on the first
// failure, so this box reported "no browser" and told the user to install a
// chromium deb they already effectively had. Restore that `return` and this
// test fails.
func TestResolveChrome_SkipsConfinedSnapsAndPicksWorkingChrome(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script fakes are POSIX-only")
	}
	dir := t.TempDir()
	fakeChromeBinary(t, dir, "chromium", 1, snapFailure)
	fakeChromeBinary(t, dir, "chromium-browser", 1, snapFailure)
	want := fakeChromeBinary(t, dir, "google-chrome", 0, "Google Chrome 150.0.7871.186")
	withOnlyPath(t, dir)

	got, attempts := resolveLaunchableChrome(context.Background())
	if got != want {
		t.Fatalf("resolved %q, want the launchable %q", got, want)
	}
	if len(attempts) == 0 {
		t.Fatal("no attempts recorded — a failure message could not name what was tried")
	}
	var okCount int
	for _, a := range attempts {
		if a.OK {
			okCount++
		}
	}
	if okCount != 1 {
		t.Fatalf("expected exactly one launchable candidate, got %d: %s", okCount, chromeAttemptsSummary(attempts))
	}
}

// DEFECT #2 (browser.go, shipped as 1.99.399): preferredChromePath
// deprioritised paths containing "/snap/". /usr/bin/chromium-browser is
// Ubuntu's snap REDIRECTOR — a 2,408-byte shell script with no "/snap/" in its
// path that fails identically. Measured on the box: exit 1, "cannot create
// temporary directory".
//
// The discriminator is NOT which path is returned — a path-string heuristic and
// a launch probe both return this one, because it is the only candidate. It is
// whether the product CLAIMS IT WORKS. The heuristic marks it unconfined and
// the capability report goes green on a browser that cannot start; the probe
// runs it and reports the truth.
//
// Restore `strings.Contains(p, "/snap/")` in place of the launch probe and this
// test fails on the `r.OK` assertion — verified by doing exactly that.
func TestResolveChrome_SnapRedirectorIsNotReportedAsWorking(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script fakes are POSIX-only")
	}
	dir := t.TempDir()
	// The ONLY browser on this box: an innocent-looking path that cannot launch.
	redirector := fakeChromeBinary(t, dir, "chromium-browser", 1, snapFailure)
	withOnlyPath(t, dir)

	got, attempts := resolveLaunchableChrome(context.Background())
	if got != redirector {
		t.Fatalf("resolved %q, want the only candidate %q as a best-effort fallback", got, redirector)
	}
	for _, a := range attempts {
		if a.OK {
			t.Fatalf("the snap redirector was recorded as LAUNCHABLE (%s) — that is the 1.99.399 false green: "+
				"its path has no \"/snap/\" in it, and only running it reveals the truth", a.Path)
		}
	}

	// The half that reaches a user: the capability report must not offer a
	// preview this box cannot produce.
	r := probeBrowserLaunches(context.Background())
	if r.OK {
		t.Fatal("preview capability reported OK on a box whose only browser is a snap redirector")
	}
	if !strings.Contains(r.Detail, "CONFINED snap") {
		t.Fatalf("the report did not name the cause or the fix: %s", r.Detail)
	}
}

// And the ordering case, kept separate: with BOTH a redirector and a working
// Chrome present (the real ubuntu-4gb topology), the working one must win.
func TestResolveChrome_WorkingChromeBeatsRedirector(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script fakes are POSIX-only")
	}
	dir := t.TempDir()
	fakeChromeBinary(t, dir, "chromium-browser", 1, snapFailure)
	want := fakeChromeBinary(t, dir, "google-chrome-stable", 0, "Google Chrome 150.0.7871.186")
	withOnlyPath(t, dir)

	got, attempts := resolveLaunchableChrome(context.Background())
	if got != want {
		t.Fatalf("resolved %q, want %q. attempts: %s", got, want, chromeAttemptsSummary(attempts))
	}
}

// When NOTHING launches, the answer must name the remedy — not "unavailable".
// The cost of a vague error here is measured in whole sessions.
func TestResolveChrome_AllConfined_NamesTheRemedy(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script fakes are POSIX-only")
	}
	dir := t.TempDir()
	fakeChromeBinary(t, dir, "chromium", 1, snapFailure)
	fakeChromeBinary(t, dir, "chromium-browser", 1, snapFailure)
	withOnlyPath(t, dir)

	got, attempts := resolveLaunchableChrome(context.Background())
	if got == "" {
		t.Fatal("expected the best-effort fallback path, got \"\" — a browser that might work in a user session beats none")
	}
	summary := chromeAttemptsSummary(attempts)
	if !strings.Contains(summary, "CONFINED snap") {
		t.Fatalf("remedy did not name confinement: %s", summary)
	}
	if !strings.Contains(summary, "google-chrome-stable") {
		t.Fatalf("remedy did not name the install that fixes it: %s", summary)
	}

	// And the capability probe must report the same thing — the report and the
	// capture path can never disagree about what this box has.
	r := probeBrowserLaunches(context.Background())
	if r.OK {
		t.Fatal("probeBrowserLaunches reported OK on a box where every browser fails to launch")
	}
	if !strings.Contains(r.Detail, "CONFINED snap") {
		t.Fatalf("probe detail did not carry the remedy: %s", r.Detail)
	}
}

// The capability probe must AGREE with the resolver on the working box too.
// The two used to have separate candidate orders in separate files, which is
// how a report saying "not supported" survived next to a capture path that
// worked.
func TestPreviewCapabilityProbe_AgreesWithResolver(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script fakes are POSIX-only")
	}
	dir := t.TempDir()
	fakeChromeBinary(t, dir, "chromium", 1, snapFailure)
	want := fakeChromeBinary(t, dir, "google-chrome", 0, "Google Chrome 150.0.7871.186")
	withOnlyPath(t, dir)

	r := probeBrowserLaunches(context.Background())
	if !r.OK {
		t.Fatalf("probe reported unavailable on a box with a working Chrome: %s", r.Detail)
	}
	if !strings.Contains(r.Detail, want) {
		t.Fatalf("probe did not name the binary it actually validated: %s", r.Detail)
	}
}

// No browser at all is still a NAMED answer with an install command, not a
// shrug.
func TestResolveChrome_NoBrowser_StillNamesTheInstall(t *testing.T) {
	dir := t.TempDir()
	withOnlyPath(t, dir)

	got, attempts := resolveLaunchableChrome(context.Background())
	if got != "" {
		t.Fatalf("resolved %q on an empty PATH", got)
	}
	if len(attempts) != 0 {
		t.Fatalf("expected no attempts on an empty PATH, got %s", chromeAttemptsSummary(attempts))
	}
	r := probeBrowserLaunches(context.Background())
	if r.OK || !strings.Contains(r.Detail, "google-chrome-stable") {
		t.Fatalf("empty-PATH probe must name the install: ok=%v detail=%s", r.OK, r.Detail)
	}
}
