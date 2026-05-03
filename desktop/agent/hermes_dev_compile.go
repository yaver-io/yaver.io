package main

// hermes_dev_compile.go — small integration glue between Layer 0
// (the HBC content-hash cache in hbc_cache.go / hbc_cache_singleton.go)
// and the inline hermesc invocation path in devserver_http.go's
// /dev/build-native handler. The mobile vibe-feedback flow goes through
// that handler, so Layer 0 must be consulted there to actually skip the
// 5–12 s hermesc step on a cache hit.
//
// Three properties to preserve:
//
//   1. The existing hermesc invocation is left structurally intact —
//      timeout context, registerActiveBuild, debug flag, incident
//      store, phase tracker transitions are all the responsibility
//      of the caller. This file adds a cache LOOKUP before the
//      compile and a cache STORE after, nothing else.
//
//   2. Cache failure is silent: the caller behaves exactly as today
//      when the cache is disabled, errors out, or returns a clean
//      miss. Production paths (TestFlight, Play Store, wire push)
//      do not invoke this glue at all.
//
//   3. Multi-phone is handled at the cache layer (content-addressable):
//      different phones reloading the same project hit the same cache
//      entries. No phone-specific state is involved.
//
// The "always stream, even on fallback" guarantee from the user's
// design intent: cache events are surfaced through the existing
// hermesTracker on the dev-server's SSE channel, so the mobile UI
// sees `hbc_cache_lookup` → `hbc_cache_hit` (or `_miss` →
// `hermesc_compiling` → `ready`) regardless of whether we took the
// optimised path or fell through to a fresh compile.

import (
	"log"
)

// hbcDevCacheContext bundles the cache opts + lookup result for one
// /dev/build-native invocation. Empty zero-value is safe; an empty
// SourceHash on the embedded key means "lookup never ran" so the
// post-compile Store call is a no-op.
type hbcDevCacheContext struct {
	opts HBCCacheCompileOpts
	key  HBCCacheKey
	hit  bool
}

// prepareDevHBCCacheLookup is the cache short-circuit for the dev
// hermesc path. Always returns a context — caller checks `.Hit()`.
//
// Side-effects on hit: emits `hbc_cache_lookup` → `hbc_cache_hit` →
// `ready` phase transitions on `mgr.hermesTracker` so the mobile UI
// sees the cache reuse rather than a silent skip.
//
// Side-effects on miss: emits `hbc_cache_lookup` → `hbc_cache_miss`
// (or `hbc_cache_corrupt` if a stale entry was just dropped) so the
// mobile UI knows hermesc is about to run for real. Caller is
// expected to follow with `hermesc_compiling`.
//
// Robust to mgr == nil and to any cache panic (recovered inside
// tryServeFromHBCCache).
func prepareDevHBCCacheLookup(
	bundleJSPath string,
	hermescPath string,
	debug bool,
	mgr *DevServerManager,
) hbcDevCacheContext {
	optLevel := "O"
	if debug {
		// Debug builds key separately so a -Og hit never serves a
		// production bundle and vice versa. Matches the hermesc
		// invocation in devserver_http.go.
		optLevel = "Og"
	}
	opts := HBCCacheCompileOpts{
		OptLevel:      optLevel,
		EmitSourceMap: debug,
		OnEvent:       makeHBCCacheTrackerForwarder(mgr),
	}
	emitPhase(mgr, "hbc_cache_lookup")
	hit, key := tryServeFromHBCCache(bundleJSPath, hermescPath, opts)
	if hit {
		emitPhase(mgr, "hbc_cache_hit")
		// Mobile UI's standard "compile finished" signal is `ready`
		// on the hermes/compile topic — emit it so the existing
		// consumer logic doesn't have to learn a new terminal phase.
		emitPhase(mgr, "ready")
	} else {
		// `tryServeFromHBCCache` already routed `corrupt` /
		// `skipped` through the OnEvent forwarder when a stale
		// entry was dropped or init failed — this final
		// `hbc_cache_miss` covers the clean-miss case.
		emitPhase(mgr, "hbc_cache_miss")
	}
	return hbcDevCacheContext{opts: opts, key: key, hit: hit}
}

// Hit reports whether the cache satisfied this build. When true the
// caller MUST skip the hermesc invocation — bundleJSPath has already
// been overwritten with the cached, validated HBC bytes.
func (c hbcDevCacheContext) Hit() bool { return c.hit }

// CommitOnSuccess is the caller's hook to file the freshly-compiled
// HBC into the cache. Call ONLY after the hermesc invocation
// returned without error AND the bundle file at `bundleJSPath` is
// the validated HBC the caller would normally have served.
//
// No-op if the lookup was a hit (we already have this entry) or if
// the cache layer wasn't reachable during lookup (key.SourceHash
// empty).
func (c hbcDevCacheContext) CommitOnSuccess(bundleJSPath string) {
	if c.hit {
		return
	}
	if c.key.SourceHash == "" {
		return
	}
	storeHBCCacheAfterCompile(bundleJSPath, c.key, c.opts)
}

// emitPhase is a nil-safe shorthand around mgr.hermesTracker. We do
// NOT lazily create the tracker here — the caller in
// /dev/build-native is responsible for its lifecycle. If the tracker
// hasn't been created yet (very early in the build), the cache event
// is logged and dropped, which is acceptable: the user sees the
// equivalent log line and the mobile SSE stream is still served by
// later transitions.
func emitPhase(mgr *DevServerManager, phase string) {
	if mgr == nil {
		log.Printf("[hbc-cache] phase=%s (no DevServerManager)", phase)
		return
	}
	if mgr.hermesTracker == nil {
		// Don't auto-create — risk of a tracker tied to the wrong
		// framework label. Caller in devserver_http.go creates one
		// before the hermesc compile; we land here only if the cache
		// path runs before that, which shouldn't happen in practice.
		log.Printf("[hbc-cache] phase=%s (hermesTracker not yet attached)", phase)
		return
	}
	mgr.hermesTracker.transitionPhase(phase)
}

// makeHBCCacheTrackerForwarder produces an OnEvent callback for the
// HBCCacheCompileOpts that forwards `corrupt` / `fallback` /
// `skipped` events as their own phase transitions on the dev-server
// SSE channel. Hits and misses are surfaced directly by
// prepareDevHBCCacheLookup so they aren't duplicated here.
func makeHBCCacheTrackerForwarder(mgr *DevServerManager) func(HBCCacheEvent) {
	if mgr == nil {
		return nil
	}
	return func(e HBCCacheEvent) {
		switch e.Kind {
		case "corrupt":
			emitPhase(mgr, "hbc_cache_corrupt")
		case "fallback":
			emitPhase(mgr, "hbc_cache_fallback")
		case "skipped":
			// "skipped" = cache was unavailable (env disabled, init
			// error, hash failure). Surface it so the mobile UI
			// knows the optimisation didn't apply this round.
			emitPhase(mgr, "hbc_cache_skipped")
		case "stored":
			emitPhase(mgr, "hbc_cache_stored")
		default:
			// "lookup" / "hit" / "miss" already handled by
			// prepareDevHBCCacheLookup directly; no double-emit.
		}
	}
}
