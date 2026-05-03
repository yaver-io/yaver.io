package main

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// TestPrepareDevHBCCacheLookup_NilManagerSafe — the helper must
// tolerate a nil DevServerManager. Some callers (CLI bg builds, tests)
// might invoke without a live dev server; we should never panic.
func TestPrepareDevHBCCacheLookup_NilManagerSafe(t *testing.T) {
	resetHBCCacheSingleton(t, t.TempDir())
	herm := fakeHermescBinary(t)
	jsPath := filepath.Join(t.TempDir(), "main.jsbundle")
	writeFakeJSBundle(t, jsPath)

	// Should not panic, must return a non-hit context.
	ctx := prepareDevHBCCacheLookup(jsPath, herm, false, nil)
	if ctx.Hit() {
		t.Fatal("first lookup must miss")
	}
}

// TestPrepareDevHBCCacheLookup_FlowMissThenHit — the canonical happy
// path: first reload misses, store after compile, second reload hits.
func TestPrepareDevHBCCacheLookup_FlowMissThenHit(t *testing.T) {
	resetHBCCacheSingleton(t, t.TempDir())
	herm := fakeHermescBinary(t)
	bundlePath := filepath.Join(t.TempDir(), "main.jsbundle")
	writeFakeJSBundle(t, bundlePath)

	// Use a tiny event sink so we can assert phase events fire.
	var seenPhases []string
	var mu sync.Mutex
	mgr := &DevServerManager{}
	mgr.hermesTracker = newProgressTracker(func(e DevServerEvent) {
		if e.Type != "phase" {
			return
		}
		mu.Lock()
		seenPhases = append(seenPhases, e.Phase)
		mu.Unlock()
	}, "expo", "hermes/compile", "build-native")

	// First call — miss.
	ctx := prepareDevHBCCacheLookup(bundlePath, herm, false, mgr)
	if ctx.Hit() {
		t.Fatal("first call should miss")
	}
	mu.Lock()
	phases1 := append([]string(nil), seenPhases...)
	seenPhases = nil
	mu.Unlock()
	if !containsPhase(phases1, "hbc_cache_lookup") || !containsPhase(phases1, "hbc_cache_miss") {
		t.Fatalf("expected lookup+miss phases on first call, got %v", phases1)
	}

	// Pretend hermesc ran: write valid HBC to bundlePath.
	hbc := makeValidHBC(t, 96, 768)
	if err := os.WriteFile(bundlePath, hbc, 0o644); err != nil {
		t.Fatal(err)
	}
	ctx.CommitOnSuccess(bundlePath)

	// Restore JS for the second lookup.
	writeFakeJSBundle(t, bundlePath)

	ctx2 := prepareDevHBCCacheLookup(bundlePath, herm, false, mgr)
	if !ctx2.Hit() {
		t.Fatal("second call should hit")
	}
	mu.Lock()
	phases2 := append([]string(nil), seenPhases...)
	mu.Unlock()
	if !containsPhase(phases2, "hbc_cache_hit") {
		t.Fatalf("expected hbc_cache_hit on second call, got %v", phases2)
	}
	if !containsPhase(phases2, "ready") {
		t.Fatalf("expected ready transition on hit (mirrors success path), got %v", phases2)
	}
	got, err := os.ReadFile(bundlePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(hbc) {
		t.Fatalf("hit did not write cached HBC over bundle path (got %d bytes, want %d)", len(got), len(hbc))
	}
}

// TestPrepareDevHBCCacheLookup_DebugFlagDifferentSlot — debug builds
// must key separately so a -Og hit never serves a -O bundle and vice
// versa. Mirrors the hermesc invocation in devserver_http.go.
func TestPrepareDevHBCCacheLookup_DebugFlagDifferentSlot(t *testing.T) {
	resetHBCCacheSingleton(t, t.TempDir())
	herm := fakeHermescBinary(t)
	bundlePath := filepath.Join(t.TempDir(), "main.jsbundle")
	writeFakeJSBundle(t, bundlePath)

	// Store under -O.
	mgr := &DevServerManager{}
	mgr.hermesTracker = newProgressTracker(func(DevServerEvent) {}, "expo", "hermes/compile", "build-native")
	ctx := prepareDevHBCCacheLookup(bundlePath, herm, false, mgr)
	if ctx.Hit() {
		t.Fatal("first call must miss")
	}
	if err := os.WriteFile(bundlePath, makeValidHBC(t, 96, 256), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx.CommitOnSuccess(bundlePath)
	writeFakeJSBundle(t, bundlePath)

	// Look up under debug=true (different OptLevel + EmitSourceMap) —
	// must miss.
	ctx2 := prepareDevHBCCacheLookup(bundlePath, herm, true, mgr)
	if ctx2.Hit() {
		t.Fatal("debug=true must NOT hit a debug=false entry (different cache key)")
	}
}

// TestPrepareDevHBCCacheLookup_NoTrackerNoCrash — the helper must
// emit through `mgr.hermesTracker` which may be nil at very early
// points in the build. Should log + drop, not panic.
func TestPrepareDevHBCCacheLookup_NoTrackerNoCrash(t *testing.T) {
	resetHBCCacheSingleton(t, t.TempDir())
	herm := fakeHermescBinary(t)
	bundlePath := filepath.Join(t.TempDir(), "main.jsbundle")
	writeFakeJSBundle(t, bundlePath)

	mgr := &DevServerManager{} // hermesTracker == nil
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("panicked with nil hermesTracker: %v", r)
		}
	}()
	ctx := prepareDevHBCCacheLookup(bundlePath, herm, false, mgr)
	if ctx.Hit() {
		t.Fatal("first call must miss")
	}
}

// TestCommitOnSuccess_NoOpWhenHit — committing after a hit is a
// pass-through; cache already contains the entry.
func TestCommitOnSuccess_NoOpWhenHit(t *testing.T) {
	resetHBCCacheSingleton(t, t.TempDir())
	herm := fakeHermescBinary(t)
	bundlePath := filepath.Join(t.TempDir(), "main.jsbundle")
	writeFakeJSBundle(t, bundlePath)

	// Establish a hit.
	mgr := &DevServerManager{}
	mgr.hermesTracker = newProgressTracker(func(DevServerEvent) {}, "expo", "hermes/compile", "build-native")
	ctx := prepareDevHBCCacheLookup(bundlePath, herm, false, mgr)
	if err := os.WriteFile(bundlePath, makeValidHBC(t, 96, 256), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx.CommitOnSuccess(bundlePath) // first write
	writeFakeJSBundle(t, bundlePath)
	ctx2 := prepareDevHBCCacheLookup(bundlePath, herm, false, mgr)
	if !ctx2.Hit() {
		t.Fatal("expected hit")
	}
	// Calling CommitOnSuccess on a hit must not blow up (no-op).
	ctx2.CommitOnSuccess(bundlePath)
}

// TestPrepareDevHBCCacheLookup_CacheDisabled — env flag turns the
// cache OFF; helper must still emit lookup events and report miss
// without erroring.
func TestPrepareDevHBCCacheLookup_CacheDisabled(t *testing.T) {
	resetHBCCacheSingleton(t, t.TempDir())
	t.Setenv(hbcCacheEnvFlag, "0")
	herm := fakeHermescBinary(t)
	bundlePath := filepath.Join(t.TempDir(), "main.jsbundle")
	writeFakeJSBundle(t, bundlePath)

	var phases []string
	var mu sync.Mutex
	mgr := &DevServerManager{}
	mgr.hermesTracker = newProgressTracker(func(e DevServerEvent) {
		if e.Type != "phase" {
			return
		}
		mu.Lock()
		phases = append(phases, e.Phase)
		mu.Unlock()
	}, "expo", "hermes/compile", "build-native")

	ctx := prepareDevHBCCacheLookup(bundlePath, herm, false, mgr)
	if ctx.Hit() {
		t.Fatal("disabled cache must never hit")
	}
	mu.Lock()
	defer mu.Unlock()
	// Must see the lookup event regardless of cache availability —
	// the "always stream" guarantee.
	if !containsPhase(phases, "hbc_cache_lookup") {
		t.Fatalf("expected lookup phase even when cache disabled, got %v", phases)
	}
	// And the helper should report skipped through the forwarder.
	if !containsPhase(phases, "hbc_cache_skipped") {
		t.Fatalf("expected skipped phase when cache disabled, got %v", phases)
	}
}

// TestMakeHBCCacheTrackerForwarder_NilManager — forwarder factory
// returns nil for a nil manager so callers can pass it straight into
// HBCCacheCompileOpts.OnEvent without checking.
func TestMakeHBCCacheTrackerForwarder_NilManager(t *testing.T) {
	if fn := makeHBCCacheTrackerForwarder(nil); fn != nil {
		t.Fatal("expected nil callback for nil manager (lets callers wire it without a guard)")
	}
}

// containsPhase is a slice-membership helper for the phase event
// assertions in this file. The package already has a `contains`
// function with a different signature (string-substring) in
// pipeline_cmd.go, so we use a phase-specific name to avoid the
// collision.
func containsPhase(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}
