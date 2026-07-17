package main

// binary_discovery_test.go — pins the single-source-of-truth contract
// between augmentAgentPATH and DiscoverBinary.
//
// The incident that motivated this (2026-07-17): both functions did the
// same job — "figure out where tools live beyond $PATH" — but from two
// separate hardcoded lists that covered different directories. A tool
// in ~/go/bin (every `go install`) was found by DiscoverBinary and
// reported as installed by /infra/summary, but invisible to any
// subprocess the daemon shelled out to by bare name, because that path
// was never added to $PATH. These tests pin the fix: one list, both
// callers, and the specific directories that fell through the crack.

import (
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
)

// TestCommonInstallPrefixesCarriesGoAndCargo is the regression test the
// task asks for: ~/go/bin and ~/.cargo/bin MUST both appear in the
// shared list. They're where `go install` and `cargo install` put every
// binary, and neither is on a default launchd/systemd $PATH.
func TestCommonInstallPrefixesCarriesGoAndCargo(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		t.Skip("no home directory — cannot assert home-relative prefixes")
	}
	prefixes := commonInstallPrefixes()

	want := []string{
		filepath.Join(home, "go", "bin"),
		filepath.Join(home, ".cargo", "bin"),
	}
	for _, w := range want {
		found := false
		for _, p := range prefixes {
			if p == w {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("commonInstallPrefixes() missing %q\nhave: %v", w, prefixes)
		}
	}
}

// TestCommonInstallPrefixesCarriesGcloudSDK pins the Google Cloud SDK
// install path. Unlike most CLIs that arrive via npm/pipx/brew, gcloud
// installs to a fixed home directory and was covered by NEITHER of the
// two old lists.
func TestCommonInstallPrefixesCarriesGcloudSDK(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("gcloud SDK home-dir layout is macOS/Linux only")
	}
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		t.Skip("no home directory")
	}
	gcloud := filepath.Join(home, "google-cloud-sdk", "bin")
	prefixes := commonInstallPrefixes()
	for _, p := range prefixes {
		if p == gcloud {
			return
		}
	}
	t.Errorf("commonInstallPrefixes() missing gcloud SDK path %q\nhave: %v", gcloud, prefixes)
}

// TestAugmentAgentPATHConsumesCommonList is the structural pin: after
// augmentation, every existing directory from commonInstallPrefixes()
// that is also a real directory on disk MUST be on $PATH. If the two
// ever diverge again (someone re-hardcodes a list in main.go), this
// test fails — which is the whole point of "one source of truth".
//
// We don't shell out to a binary; we synthesize a directory under the
// test temp home, run augmentAgentPATH against a clean PATH, and
// confirm the synthesized dir landed on $PATH. That's enough to prove
// augmentAgentPATH is reading commonInstallPrefixes, not its own list.
func TestAugmentAgentPATHConsumesCommonList(t *testing.T) {
	// Build a throwaway home with two dirs the OLD augmentAgentPATH list
	// omitted: go/bin and .cargo/bin. If augmentAgentPATH still used its
	// own hardcoded list, these would never appear on PATH.
	tmpHome := t.TempDir()
	for _, sub := range []string{"go/bin", ".cargo/bin", ".local/bin"} {
		if err := os.MkdirAll(filepath.Join(tmpHome, sub), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", sub, err)
		}
	}

	restore := setEnvForTest(t, map[string]string{
		"HOME": tmpHome,
		"PATH": "/usr/bin:/bin",
	})
	defer restore()

	// commonInstallPrefixes reads HOME, so both callers now see tmpHome.
	expected := []string{
		filepath.Join(tmpHome, "go", "bin"),
		filepath.Join(tmpHome, ".cargo", "bin"),
		filepath.Join(tmpHome, ".local", "bin"),
	}

	augmentAgentPATH()

	got := strings.Split(os.Getenv("PATH"), ":")
	gotSet := map[string]bool{}
	for _, p := range got {
		gotSet[p] = true
	}
	for _, e := range expected {
		if !gotSet[e] {
			t.Errorf("PATH after augment missing %q\nfull PATH: %s", e, os.Getenv("PATH"))
		}
	}
}

// TestAugmentAgentPATHAppendsNeverReorders pins the "append, never
// reorder" contract: a directory ALREADY on the user's $PATH must keep
// its existing position. New fallbacks go at the end, not the front.
// If someone flips augmentation back to prepend, a user's chosen
// /usr/local/bin would lose to our fallback — this test catches that.
func TestAugmentAgentPATHAppendsNeverReorders(t *testing.T) {
	tmpHome := t.TempDir()
	// Create a fallback candidate dir that is NOT on the initial PATH.
	fallback := filepath.Join(tmpHome, "go", "bin")
	if err := os.MkdirAll(fallback, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	userChoice := "/usr/local/bin" // pretend the user put this first
	initial := userChoice + ":/usr/bin:/bin"
	restore := setEnvForTest(t, map[string]string{"HOME": tmpHome, "PATH": initial})
	defer restore()

	augmentAgentPATH()

	result := os.Getenv("PATH")
	parts := strings.Split(result, ":")
	if len(parts) == 0 {
		t.Fatalf("PATH empty after augment: %q", result)
	}
	// The user's first choice must still be first. Prepending the
	// fallback would have pushed it down.
	if parts[0] != userChoice {
		t.Errorf("user's first PATH entry moved: got %q, want %q\nfull: %s", parts[0], userChoice, result)
	}
	// The fallback must be present somewhere AFTER the user's block.
	// (Dedup ensures /usr/bin etc. aren't repeated, so the fallback
	// lands at the tail.)
	idxChoice := indexOfStr(parts, userChoice)
	idxFallback := indexOfStr(parts, fallback)
	if idxFallback < 0 {
		t.Fatalf("fallback %q not on PATH at all: %s", fallback, result)
	}
	if idxFallback < idxChoice {
		t.Errorf("fallback %q prepended ahead of user choice %q — append contract broken\nfull: %s",
			fallback, userChoice, result)
	}
}

// TestAugmentAgentPATHDedupes confirms we don't emit duplicate entries
// if the same dir appears on PATH and in the candidate list. A
// duplicate PATH entry is harmless to execution but ugly in `echo $PATH`
// and breaks tools that naively split-and-count.
func TestAugmentAgentPATHDedupes(t *testing.T) {
	tmpHome := t.TempDir()
	// .local/bin is on BOTH the initial PATH and commonInstallPrefixes.
	localBin := filepath.Join(tmpHome, ".local", "bin")
	if err := os.MkdirAll(localBin, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	restore := setEnvForTest(t, map[string]string{
		"HOME": tmpHome,
		"PATH": localBin + ":/usr/bin:/bin",
	})
	defer restore()

	augmentAgentPATH()

	parts := strings.Split(os.Getenv("PATH"), ":")
	seen := map[string]int{}
	for _, p := range parts {
		seen[p]++
	}
	for p, n := range seen {
		if n > 1 {
			t.Errorf("PATH entry %q appears %d times: %s", p, n, os.Getenv("PATH"))
		}
	}
}

// TestCommonInstallPrefixesIsSortedStableAcrossCalls guards against a
// future "optimisation" that reorders prefixes by recency or any other
// runtime signal. The order is the contract — more-specific (homebrew,
// cargo, pipx) before generic (/usr/local/bin) — and callers depend on
// the first match winning. Two calls in a row must return identical
// slices; map-based construction would break that.
func TestCommonInstallPrefixesIsStableAcrossCalls(t *testing.T) {
	a := commonInstallPrefixes()
	b := commonInstallPrefixes()
	if len(a) != len(b) {
		t.Fatalf("commonInstallPrefixes length drift: %d vs %d", len(a), len(b))
	}
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("commonInstallPrefixes order not stable:\n call1[%d]=%q\n call2[%d]=%q", i, a[i], i, b[i])
		}
	}
	// Spot-check: the Apple Silicon brew path (or the Linux /usr/local/bin)
	// must come BEFORE the generic /usr/bin on the platforms that have
	// both — so a brew-installed tool wins over a system one. We only
	// assert the relative order of two definitely-present prefixes; we
	// don't assert their absolute positions so the test doesn't break
	// when a new prefix is inserted between them.
	if runtime.GOOS == "darwin" {
		brew := indexOfStr(a, "/opt/homebrew/bin")
		usr := indexOfStr(a, "/usr/local/bin")
		if brew < 0 || usr < 0 {
			t.Fatalf("expected darwin brew + usr/local prefixes: %v", a)
		}
		if brew > usr {
			t.Errorf("/opt/homebrew/bin should precede /usr/local/bin so Apple Silicon brew wins; got order %d > %d", brew, usr)
		}
	}
	// Sanity: the returned slice is non-empty on every supported platform.
	if len(a) == 0 {
		t.Fatalf("commonInstallPrefixes returned empty on %s", runtime.GOOS)
	}
	_ = sort.StringsAreSorted // keep the import honest if future tests want sort
}

// setEnvForTest sets a batch of env vars and returns a restore func. It
// also t.Fatal's if setting fails, so the caller doesn't have to check.
// We can't use t.Setenv (Go 1.17+) for HOME because augmentAgentPATH
// calls os.UserHomeDir which on darwin caches via `os/user` which may
// bypass the env override in some Go versions — direct os.Setenv of
// HOME is the reliable path and we restore it ourselves.
func setEnvForTest(t *testing.T, vars map[string]string) func() {
	t.Helper()
	saved := map[string]string{}
	for k, v := range vars {
		if old, ok := os.LookupEnv(k); ok {
			saved[k] = old
		} else {
			saved[k] = "\x00" // sentinel: was unset
		}
		if err := os.Setenv(k, v); err != nil {
			t.Fatalf("setenv %s: %v", k, err)
		}
	}
	return func() {
		for k, v := range saved {
			if v == "\x00" {
				_ = os.Unsetenv(k)
			} else {
				_ = os.Setenv(k, v)
			}
		}
	}
}

func indexOfStr(s []string, v string) int {
	for i, x := range s {
		if x == v {
			return i
		}
	}
	return -1
}
