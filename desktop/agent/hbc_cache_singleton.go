package main

// hbc_cache_singleton.go — process-local lazy initialiser for the
// HBC content cache, plus the cache-aware wrapper that
// compileHermesBundle and other callers consult before falling
// through to a fresh `hermesc` invocation.
//
// Two non-negotiables this file enforces:
//
//   1. The cache is purely OPTIMISATIONAL. Any failure (init, lookup,
//      store, validation, panic) MUST degrade silently to the existing
//      hermesc path. Cache code never blocks a build or surfaces a
//      crash to the caller.
//
//   2. Production paths (release_cmd.go, TestFlight, Play Store, wire
//      push) get cache hits transparently because the cache key
//      includes -O level and hermesc version — release builds with -O
//      will only ever hit cache entries that were also written under
//      -O. Dev builds (when we eventually pass -O0) will key into a
//      separate slot.
//
// See docs/hermes-secondary-reload-optimization.md §13 for the safety
// analysis behind the design.

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

const (
	// defaultHBCCacheBytes is the LRU ceiling for the on-disk cache.
	// 1 GiB fits hundreds of bundle variants comfortably; eviction
	// runs lazily after each successful Store.
	defaultHBCCacheBytes int64 = 1 << 30

	// hbcCacheEnvFlag — set to "0" / "false" to disable the cache
	// entirely (escape hatch for debugging or for the rare case where
	// a regression is suspected). Default is enabled.
	hbcCacheEnvFlag = "YAVER_HBC_CACHE"
)

var (
	hbcCacheOnce         sync.Once
	hbcCache             *HBCCache
	hbcCacheInitErr      error
	hbcHermesVersionOnce sync.Once
	hbcHermesVersionVal  string
	hbcHermesVersionErr  error
)

// hbcCacheEnabled returns false when the env flag explicitly disables
// the cache. Default is enabled.
func hbcCacheEnabled() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv(hbcCacheEnvFlag)))
	switch v {
	case "0", "false", "no", "off":
		return false
	}
	return true
}

// getHBCCache returns the singleton, lazily initialising on first
// call. nil + error on init failure — callers must handle gracefully
// (log + skip the cache path).
func getHBCCache() (*HBCCache, error) {
	if !hbcCacheEnabled() {
		return nil, fmt.Errorf("hbc cache disabled via %s", hbcCacheEnvFlag)
	}
	hbcCacheOnce.Do(func() {
		c, err := NewHBCCache("", defaultHBCCacheBytes)
		if err != nil {
			hbcCacheInitErr = err
			return
		}
		hbcCache = c
		log.Printf("[hbc-cache] enabled at %s (cap=%d MiB)",
			c.Root(), defaultHBCCacheBytes/(1<<20))
	})
	if hbcCacheInitErr != nil {
		return nil, hbcCacheInitErr
	}
	return hbcCache, nil
}

// hermesVersionFingerprint returns a stable string that uniquely
// identifies the hermesc binary we're about to run. We use this in
// the cache key so a Hermes upgrade invalidates every previous entry.
//
// Strategy:
//   1. If `hermesc -version` succeeds, use sha256 of its output (most
//      portable; output strings change per version).
//   2. Otherwise, fall back to sha256 of the binary's file contents
//      (cheap, deterministic, but bigger I/O — only used as backup).
//
// Cached process-wide; assume the binary doesn't get swapped under
// us during a single agent run.
func hermesVersionFingerprint(hermescPath string) (string, error) {
	hbcHermesVersionOnce.Do(func() {
		// Try `hermesc -version` first.
		out, err := exec.Command(hermescPath, "-version").CombinedOutput()
		if err == nil && len(out) > 0 {
			h := sha256.Sum256(out)
			hbcHermesVersionVal = hex.EncodeToString(h[:])
			return
		}
		// Fall back to hashing the binary itself.
		data, ferr := os.ReadFile(hermescPath)
		if ferr != nil {
			hbcHermesVersionErr = fmt.Errorf("hermes version probe: -version failed (%v) and binary read failed (%w)", err, ferr)
			return
		}
		h := sha256.Sum256(data)
		hbcHermesVersionVal = hex.EncodeToString(h[:])
	})
	if hbcHermesVersionErr != nil {
		return "", hbcHermesVersionErr
	}
	return hbcHermesVersionVal, nil
}

// HBCCacheCompileOpts threads optional caller context into the cache
// wrapper without changing any public function signatures. All fields
// are optional; zero-value works.
type HBCCacheCompileOpts struct {
	// OptLevel maps to the hermesc -O flag used for the eventual
	// compile. Defaults to "O" when empty (matches today's
	// compileHermesBundle behaviour).
	OptLevel string
	// EmitSourceMap mirrors hermesc -output-source-map. Defaults to
	// false.
	EmitSourceMap bool
	// Extra is folded into the cache key — useful for caller-specific
	// invariants we want to invalidate on (e.g. mobile app version).
	Extra map[string]string
	// OnEvent is an optional callback for SSE/log surfacing. Called
	// from inside the wrapper with stage-specific Cache* events.
	// Nil = no callbacks (production default).
	OnEvent func(HBCCacheEvent)
}

// HBCCacheEvent is the narrow surface the wrapper exposes to callers
// that want to forward cache state to a UI / SSE channel.
type HBCCacheEvent struct {
	Kind            string // "lookup" | "hit" | "miss" | "corrupt" | "stored" | "skipped" | "fallback"
	SourceHashHead  string // first 12 hex chars of the JS bundle hash, for diagnostics
	BCVersion       uint32 // populated on "hit"
	BytesCached     int64  // populated on "hit" / "stored"
	FallbackReason  string // populated on "corrupt" / "fallback" / "skipped"
}

// tryServeFromHBCCache is the cache-hit short-circuit. Returns true if
// the bundle was satisfied from cache and `bundlePath` now contains a
// validated HBC; false if the caller must run hermesc.
//
// Any failure — disabled cache, init error, hash error, validation
// failure, write error — returns false so the caller falls through to
// hermesc. This function NEVER returns an error; the cache path is
// purely optimisational.
func tryServeFromHBCCache(bundleJSPath string, hermescPath string, opts HBCCacheCompileOpts) (hit bool, key HBCCacheKey) {
	defer func() {
		// SP1: cache code MUST NOT crash the build. Any panic (e.g.
		// nil deref, unexpected disk state) is swallowed and reported
		// as a clean miss.
		if r := recover(); r != nil {
			log.Printf("[hbc-cache] PANIC in lookup path (recovered): %v", r)
			hit = false
		}
	}()

	emit := func(e HBCCacheEvent) {
		if opts.OnEvent != nil {
			opts.OnEvent(e)
		}
	}

	c, err := getHBCCache()
	if err != nil || c == nil {
		emit(HBCCacheEvent{Kind: "skipped", FallbackReason: fmt.Sprintf("cache unavailable: %v", err)})
		return false, HBCCacheKey{}
	}

	srcHash, err := HashJSBundle(bundleJSPath)
	if err != nil {
		emit(HBCCacheEvent{Kind: "skipped", FallbackReason: fmt.Sprintf("hash input: %v", err)})
		return false, HBCCacheKey{}
	}
	hermesVer, err := hermesVersionFingerprint(hermescPath)
	if err != nil {
		emit(HBCCacheEvent{Kind: "skipped", FallbackReason: fmt.Sprintf("hermes version probe: %v", err)})
		return false, HBCCacheKey{}
	}

	optLevel := opts.OptLevel
	if optLevel == "" {
		optLevel = "O"
	}
	key = HBCCacheKey{
		SourceHash:    srcHash,
		HermesVersion: hermesVer,
		OptLevel:      optLevel,
		EmitSourceMap: opts.EmitSourceMap,
		Extra:         opts.Extra,
	}
	srcHead := srcHash
	if len(srcHead) > 12 {
		srcHead = srcHead[:12]
	}

	emit(HBCCacheEvent{Kind: "lookup", SourceHashHead: srcHead})

	r, lookupErr := c.Lookup(key)
	if lookupErr != nil {
		// Corrupt entry was just dropped; report the fallback.
		emit(HBCCacheEvent{
			Kind:           "corrupt",
			SourceHashHead: srcHead,
			FallbackReason: lookupErr.Error(),
		})
		return false, key
	}
	if !r.Hit {
		emit(HBCCacheEvent{Kind: "miss", SourceHashHead: srcHead})
		return false, key
	}

	// Hit — write the cached HBC over the bundle path atomically. We
	// can't trust the JS file the caller pointed us at to remain
	// untouched (caller may rotate it), so write through atomicWriteFile.
	if err := atomicWriteFile(bundleJSPath, r.Data, 0o644); err != nil {
		log.Printf("[hbc-cache] failed to write hit to %s: %v — falling through", bundleJSPath, err)
		emit(HBCCacheEvent{
			Kind:           "fallback",
			SourceHashHead: srcHead,
			FallbackReason: fmt.Sprintf("write hit failed: %v", err),
		})
		return false, key
	}

	emit(HBCCacheEvent{
		Kind:           "hit",
		SourceHashHead: srcHead,
		BCVersion:      r.BCVersion,
		BytesCached:    int64(len(r.Data)),
	})
	log.Printf("[hbc-cache] HIT for %s (%d bytes, bc=%d) — skipping hermesc",
		filepath.Base(bundleJSPath), len(r.Data), r.BCVersion)
	return true, key
}

// storeHBCCacheAfterCompile is the post-hermesc hook. Reads the just-
// produced HBC and stores it under the same key the lookup probed for.
// Any failure is logged but never propagated — a cache write failure
// must not fail the build.
//
// Caller passes the key from tryServeFromHBCCache (which returns it
// even on miss). If key.SourceHash is empty (cache disabled or hash
// failed earlier), this is a no-op.
func storeHBCCacheAfterCompile(bundlePath string, key HBCCacheKey, opts HBCCacheCompileOpts) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[hbc-cache] PANIC in store path (recovered): %v", r)
		}
	}()

	if key.SourceHash == "" {
		return // lookup path bailed before computing the key — nothing to store under
	}
	emit := func(e HBCCacheEvent) {
		if opts.OnEvent != nil {
			opts.OnEvent(e)
		}
	}

	c, err := getHBCCache()
	if err != nil || c == nil {
		return
	}

	data, err := os.ReadFile(bundlePath)
	if err != nil {
		log.Printf("[hbc-cache] post-compile read failed: %v", err)
		return
	}
	if err := c.Store(key, data); err != nil {
		log.Printf("[hbc-cache] post-compile store failed: %v", err)
		return
	}

	srcHead := key.SourceHash
	if len(srcHead) > 12 {
		srcHead = srcHead[:12]
	}
	emit(HBCCacheEvent{
		Kind:           "stored",
		SourceHashHead: srcHead,
		BytesCached:    int64(len(data)),
	})
	log.Printf("[hbc-cache] stored %s (%d bytes) for next reload", filepath.Base(bundlePath), len(data))
}
