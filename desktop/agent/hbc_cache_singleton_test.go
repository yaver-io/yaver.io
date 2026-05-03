package main

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// resetHBCCacheSingleton lets a test re-initialise the singleton against
// a fresh temp dir. We can't replace the real $HOME at-test-time
// without affecting other code paths, so we override the cache root
// directly and reset the once-guards.
func resetHBCCacheSingleton(t *testing.T, root string) {
	t.Helper()
	hbcCacheOnce = sync.Once{}
	hbcCache = nil
	hbcCacheInitErr = nil
	hbcHermesVersionOnce = sync.Once{}
	hbcHermesVersionVal = ""
	hbcHermesVersionErr = nil
	// Force the singleton's eventual init to use `root`.
	t.Setenv("HOME", root)
	// Make sure cache is enabled (previous test may have set the flag).
	t.Setenv(hbcCacheEnvFlag, "1")
}

func writeFakeJSBundle(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("module.exports = function(){return 42;}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// fakeHermescBinary creates a tiny shell script that pretends to be
// hermesc — it accepts `-version` (prints a fixed string), and otherwise
// emits a valid HBC blob to the path after `-out`. Used by the
// integration-flow test below; this avoids a real Hermes dependency.
func fakeHermescBinary(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "hermesc")
	// We can't shell-script binary HBC easily, so instead the test
	// shells to `cp` of a pre-generated HBC. This keeps the binary's
	// `-version` path simple while still emitting a valid bytecode.
	hbcBlob := makeValidHBC(t, 96, 256)
	hbcPath := filepath.Join(dir, "fake.hbc")
	if err := os.WriteFile(hbcPath, hbcBlob, 0o644); err != nil {
		t.Fatal(err)
	}
	script := `#!/bin/sh
case "$1" in
  -version) echo "Fake hermesc v1.0.0 (tests)"; exit 0 ;;
esac
out=""
i=1
for arg in "$@"; do
  if [ "$prev" = "-out" ]; then out="$arg"; fi
  prev="$arg"
done
if [ -n "$out" ]; then
  cat ` + hbcPath + ` > "$out"
fi
exit 0
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestHBCCacheSingleton_Disabled(t *testing.T) {
	resetHBCCacheSingleton(t, t.TempDir())
	t.Setenv(hbcCacheEnvFlag, "0")
	c, err := getHBCCache()
	if err == nil {
		t.Fatal("expected error when cache disabled")
	}
	if c != nil {
		t.Fatal("expected nil cache when disabled")
	}
}

func TestHBCCacheSingleton_LazyInit(t *testing.T) {
	root := t.TempDir()
	resetHBCCacheSingleton(t, root)
	c, err := getHBCCache()
	if err != nil {
		t.Fatalf("first init: %v", err)
	}
	if c == nil {
		t.Fatal("expected non-nil cache")
	}
	c2, err := getHBCCache()
	if err != nil || c2 != c {
		t.Fatalf("second call should return same instance, got %p vs %p (err=%v)", c, c2, err)
	}
	// Cache root should land under the overridden HOME.
	if !strings.HasPrefix(c.Root(), root) {
		t.Fatalf("cache root %q not rooted at %q", c.Root(), root)
	}
}

func TestHermesVersionFingerprint_StableAcrossCalls(t *testing.T) {
	resetHBCCacheSingleton(t, t.TempDir())
	herm := fakeHermescBinary(t)
	a, err := hermesVersionFingerprint(herm)
	if err != nil {
		t.Fatalf("first probe: %v", err)
	}
	b, err := hermesVersionFingerprint(herm)
	if err != nil {
		t.Fatalf("second probe: %v", err)
	}
	if a != b {
		t.Fatalf("fingerprint not stable: %s vs %s", a, b)
	}
	if len(a) != 64 {
		t.Fatalf("expected 64-char hex sha256, got %d chars: %q", len(a), a)
	}
}

func TestTryServeFromHBCCache_MissThenHit(t *testing.T) {
	root := t.TempDir()
	resetHBCCacheSingleton(t, root)
	herm := fakeHermescBinary(t)
	bundleDir := t.TempDir()
	jsPath := filepath.Join(bundleDir, "main.jsbundle")
	writeFakeJSBundle(t, jsPath)

	var events []HBCCacheEvent
	opts := HBCCacheCompileOpts{
		OnEvent: func(e HBCCacheEvent) { events = append(events, e) },
	}

	// First call: clean miss.
	hit, key := tryServeFromHBCCache(jsPath, herm, opts)
	if hit {
		t.Fatal("first call should miss")
	}
	if key.SourceHash == "" {
		t.Fatal("key should be populated even on miss (caller needs it for Store)")
	}
	if !containsKind(events, "lookup") || !containsKind(events, "miss") {
		t.Fatalf("expected lookup+miss events, got %+v", events)
	}

	// Pretend hermesc ran: write valid HBC into jsPath.
	hbc := makeValidHBC(t, 96, 512)
	if err := os.WriteFile(jsPath, hbc, 0o644); err != nil {
		t.Fatal(err)
	}
	// Persist via the post-compile hook.
	storeHBCCacheAfterCompile(jsPath, key, opts)
	if !containsKind(events, "stored") {
		t.Fatalf("expected stored event after store, got %+v", events)
	}

	// Restore JS source so the next lookup hashes the same input.
	writeFakeJSBundle(t, jsPath)

	// Second call: hit. The cached HBC is written back over jsPath.
	events = nil
	hit2, _ := tryServeFromHBCCache(jsPath, herm, opts)
	if !hit2 {
		t.Fatal("second call should hit")
	}
	if !containsKind(events, "hit") {
		t.Fatalf("expected hit event, got %+v", events)
	}
	got, err := os.ReadFile(jsPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(hbc) {
		t.Fatalf("hit did not write cached HBC over the bundle path (size %d, expected %d)", len(got), len(hbc))
	}
}

func TestTryServeFromHBCCache_FlagsAffectKey(t *testing.T) {
	root := t.TempDir()
	resetHBCCacheSingleton(t, root)
	herm := fakeHermescBinary(t)
	bundleDir := t.TempDir()
	jsPath := filepath.Join(bundleDir, "main.jsbundle")
	writeFakeJSBundle(t, jsPath)

	// Store under -O.
	hit, keyO := tryServeFromHBCCache(jsPath, herm, HBCCacheCompileOpts{OptLevel: "O"})
	if hit {
		t.Fatal("first probe must miss")
	}
	if err := os.WriteFile(jsPath, makeValidHBC(t, 96, 256), 0o644); err != nil {
		t.Fatal(err)
	}
	storeHBCCacheAfterCompile(jsPath, keyO, HBCCacheCompileOpts{OptLevel: "O"})

	writeFakeJSBundle(t, jsPath)

	// Lookup under -O0 — must miss because key differs.
	hit0, _ := tryServeFromHBCCache(jsPath, herm, HBCCacheCompileOpts{OptLevel: "O0"})
	if hit0 {
		t.Fatal("-O0 lookup hit a -O entry — flag not in cache key")
	}

	// Original -O lookup still hits.
	hit, _ = tryServeFromHBCCache(jsPath, herm, HBCCacheCompileOpts{OptLevel: "O"})
	if !hit {
		t.Fatal("original -O lookup unexpectedly miss")
	}
}

func TestTryServeFromHBCCache_RecoversFromPanic(t *testing.T) {
	// SP1: cache code MUST NOT crash the build. We can't easily force
	// a panic in production code, but we can sanity-check the
	// recover() guard by invoking the helper with a nil-y input that
	// some downstream call could choke on.
	root := t.TempDir()
	resetHBCCacheSingleton(t, root)

	// Empty hermesc path — version probe will fail, helper should
	// emit "skipped" and return miss without panicking.
	jsPath := filepath.Join(t.TempDir(), "main.jsbundle")
	writeFakeJSBundle(t, jsPath)

	var events []HBCCacheEvent
	hit, _ := tryServeFromHBCCache(jsPath, "", HBCCacheCompileOpts{
		OnEvent: func(e HBCCacheEvent) { events = append(events, e) },
	})
	if hit {
		t.Fatal("hit with bogus hermesc path")
	}
	if !containsKind(events, "skipped") {
		t.Fatalf("expected skipped event when hermesc path empty, got %+v", events)
	}
}

func TestStoreHBCCacheAfterCompile_NoOpWhenKeyEmpty(t *testing.T) {
	// If the upstream lookup bailed before computing a key (e.g.
	// cache disabled), the post-compile store must be a no-op so
	// the build path remains clean.
	resetHBCCacheSingleton(t, t.TempDir())
	jsPath := filepath.Join(t.TempDir(), "main.jsbundle")
	if err := os.WriteFile(jsPath, makeValidHBC(t, 96, 128), 0o644); err != nil {
		t.Fatal(err)
	}
	// Empty key — should silently return.
	storeHBCCacheAfterCompile(jsPath, HBCCacheKey{}, HBCCacheCompileOpts{})
}

func containsKind(events []HBCCacheEvent, kind string) bool {
	for _, e := range events {
		if e.Kind == kind {
			return true
		}
	}
	return false
}
