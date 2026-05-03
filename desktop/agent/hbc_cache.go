package main

// hbc_cache.go — content-addressable cache for Hermes bytecode (HBC)
// outputs. Sits between Metro and the phone's bundle download:
//
//   Metro JS bundle  →  sha256 + flags  →  cached HBC?
//                                              │
//                              hit ───────────►│ skip hermesc, serve cached
//                                              │
//                              miss ──────────►│ run hermesc, store result
//
// This is Layer 0 of the secondary-reload optimisation stack documented
// in docs/hermes-secondary-reload-optimization.md. It catches the
// common case where two reloads of the same project produce identical
// Metro output (no source change between reloads, or Metro emitted
// byte-equivalent output for an idempotent edit) — saves the 5-12 s
// hermesc step.
//
// Safety properties (from the doc's §13.1):
//   SP1  Atomic writes — partial files never visible.
//   SP2  Validation on read — corrupt entries are skipped, not served.
//   SP3  Cache key encodes EVERY input that affects output (source hash,
//        hermesc version, optimisation level, target arch, sourcemap on/
//        off). Forgotten flag = silently-wrong code = priority bug.
//   SP4  Schema-versioned — bumping HBCCacheSchemaVersion wipes the
//        whole cache on next startup so we never serve old-format entries.
//   SP5  Production builds untouched — the cache is consulted via an
//        explicit code path in the dev iteration flow, never via the
//        TestFlight / Play Store / wire-push pipelines.

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// HBCCacheSchemaVersion is bumped whenever the on-disk format changes.
// On startup, a mismatch wipes the whole cache directory. Never serve
// old-format entries against new code — that's how silent corruption
// happens.
const HBCCacheSchemaVersion = 1

// HBCFileMagic is the four-byte big-endian magic at offset 4 of every
// valid Hermes bytecode file (see mobile/ios/Yaver/YaverBundleValidator.
// swift). Mirrored here so the cache can validate before serving; a
// corrupted cache entry is dropped rather than served to the phone.
const HBCFileMagic uint32 = 0x1F1903C1

// hbcMagicOffset / hbcVersionOffset — offsets within the Hermes header.
// Layout: bytes 0..3 reserved (used for SHA hashes in some Hermes
// versions), bytes 4..7 magic (big-endian), bytes 8..11 BC version
// (little-endian).
const (
	hbcMagicOffset   = 4
	hbcVersionOffset = 8
	hbcHeaderMinLen  = 32 // smallest plausible HBC; anything shorter is corrupt
)

// HBCCacheKey is the deterministic content key for an HBC entry.
// EVERY field that influences hermesc output MUST be encoded here.
// Forgotten field = stale entry = silent corruption.
type HBCCacheKey struct {
	// SourceHash is sha256 of the JS bundle hermesc reads as input.
	// THE primary key — same JS in produces same HBC out for a given
	// hermesc version + flag set.
	SourceHash string

	// HermesVersion is sha256(hermesc -version output). Bumping
	// hermesc invalidates every entry that mentioned the old version.
	HermesVersion string

	// OptLevel is the -O flag the caller will pass: "O" (optimised,
	// release), "O0" (dev, no optimisation), or anything else hermesc
	// supports. Different opt levels produce different bytecode.
	OptLevel string

	// EmitSourceMap is true when -output-source-map is in effect. The
	// HBC itself differs (debug info embedded vs not).
	EmitSourceMap bool

	// TargetArch is "arm64" / "x86_64" / "" for native, when relevant.
	// Hermes produces architecture-neutral bytecode in modern versions
	// but we encode it anyway so we don't have to revisit the schema if
	// that changes.
	TargetArch string

	// Extra is a free-form bag of anything else a caller wants in the
	// key (project hash, mobile app version, custom flags). Caller's
	// responsibility to keep this stable for stable hits.
	Extra map[string]string
}

// fingerprint computes the canonical sha256 of every key field, in a
// stable order. This is what we name files on disk by.
func (k HBCCacheKey) fingerprint() string {
	h := sha256.New()
	// Field order is part of the schema. Changing it requires a
	// schema bump.
	io.WriteString(h, "v"+fmt.Sprint(HBCCacheSchemaVersion)+"\x00")
	io.WriteString(h, "src:"+k.SourceHash+"\x00")
	io.WriteString(h, "hermes:"+k.HermesVersion+"\x00")
	io.WriteString(h, "opt:"+k.OptLevel+"\x00")
	if k.EmitSourceMap {
		io.WriteString(h, "smap:1\x00")
	} else {
		io.WriteString(h, "smap:0\x00")
	}
	io.WriteString(h, "arch:"+k.TargetArch+"\x00")
	// Stable iteration over Extra.
	keys := make([]string, 0, len(k.Extra))
	for ek := range k.Extra {
		keys = append(keys, ek)
	}
	sort.Strings(keys)
	for _, ek := range keys {
		io.WriteString(h, "x:"+ek+"="+k.Extra[ek]+"\x00")
	}
	return hex.EncodeToString(h.Sum(nil))
}

// HBCCache is a process-local handle to the on-disk cache. Construct
// with NewHBCCache; concurrent Lookup/Store calls are safe.
type HBCCache struct {
	root     string
	maxBytes int64
	mu       sync.Mutex // serialises store-then-evict so the caller doesn't see a half-evicted state
}

// NewHBCCache opens (or creates) the cache directory. Pass an empty
// root to use the default `~/.yaver/hbc-cache/`. maxBytes <= 0 means
// "no eviction"; pass a sane default (e.g. 1<<30 for 1 GiB).
//
// On schema mismatch the existing cache directory is wiped — that's
// the whole point of HBCCacheSchemaVersion. Never serve old-format
// entries against new code.
func NewHBCCache(root string, maxBytes int64) (*HBCCache, error) {
	if root == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("hbc-cache: resolve home dir: %w", err)
		}
		root = filepath.Join(home, ".yaver", "hbc-cache")
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, fmt.Errorf("hbc-cache: mkdir %s: %w", root, err)
	}
	c := &HBCCache{root: root, maxBytes: maxBytes}
	if err := c.ensureSchema(); err != nil {
		return nil, err
	}
	return c, nil
}

// ensureSchema reads (or writes) the SCHEMA file. If the version on
// disk doesn't match the constant, the whole directory is wiped — we
// never want to serve an old-format entry.
func (c *HBCCache) ensureSchema() error {
	schemaPath := filepath.Join(c.root, "SCHEMA")
	got, err := os.ReadFile(schemaPath)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("hbc-cache: read schema: %w", err)
		}
		// Fresh directory — write our schema and return.
		return c.writeSchema(schemaPath)
	}
	if strings.TrimSpace(string(got)) == fmt.Sprint(HBCCacheSchemaVersion) {
		return nil
	}
	// Mismatch — wipe everything except SCHEMA, then rewrite SCHEMA.
	log.Printf("[hbc-cache] schema mismatch (got %q, want %d) — wiping %s",
		strings.TrimSpace(string(got)), HBCCacheSchemaVersion, c.root)
	entries, err := os.ReadDir(c.root)
	if err != nil {
		return fmt.Errorf("hbc-cache: read root for wipe: %w", err)
	}
	for _, e := range entries {
		if err := os.RemoveAll(filepath.Join(c.root, e.Name())); err != nil {
			return fmt.Errorf("hbc-cache: wipe %s: %w", e.Name(), err)
		}
	}
	return c.writeSchema(schemaPath)
}

func (c *HBCCache) writeSchema(schemaPath string) error {
	if err := atomicWriteFile(schemaPath, []byte(fmt.Sprint(HBCCacheSchemaVersion)+"\n"), 0o644); err != nil {
		return fmt.Errorf("hbc-cache: write schema: %w", err)
	}
	return nil
}

// hbcCacheMeta is the JSON sidecar stored next to each .hbc entry.
// Used for LRU eviction (LastUsedAt) and for diagnostics.
type hbcCacheMeta struct {
	CreatedAt    time.Time `json:"createdAt"`
	LastUsedAt   time.Time `json:"lastUsedAt"`
	SizeBytes    int64     `json:"sizeBytes"`
	BCVersion    uint32    `json:"bcVersion"`
	HermesVer    string    `json:"hermesVersion"`
	OptLevel     string    `json:"optLevel"`
	SourceHashHd string    `json:"sourceHashHead"` // first 12 chars, for diagnostics
}

func (c *HBCCache) entryPath(fingerprint string) string {
	return filepath.Join(c.root, fingerprint+".hbc")
}

func (c *HBCCache) metaPath(fingerprint string) string {
	return filepath.Join(c.root, fingerprint+".meta")
}

// HBCCacheLookupResult is what Lookup returns. Hit=true means Data is
// a validated HBC byte slice the caller can write to disk and serve.
// Hit=false with err==nil means clean miss; err != nil means corrupted/
// errored entry (caller should fall through to a fresh hermesc run).
type HBCCacheLookupResult struct {
	Hit       bool
	Data      []byte
	BCVersion uint32 // populated on hit, useful for the SSE phase event
}

// Lookup tries to load a cached HBC. Validates magic + BC version
// before returning; corrupt entries are dropped from disk and a clean
// miss is reported instead. Updates LastUsedAt on hit.
//
// Errors are non-fatal — callers should treat any error as "miss,
// continue with hermesc". The error is logged so a regression is
// observable.
func (c *HBCCache) Lookup(key HBCCacheKey) (HBCCacheLookupResult, error) {
	fp := key.fingerprint()
	entry := c.entryPath(fp)
	meta := c.metaPath(fp)

	data, err := os.ReadFile(entry)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return HBCCacheLookupResult{Hit: false}, nil
		}
		return HBCCacheLookupResult{Hit: false}, fmt.Errorf("hbc-cache: read %s: %w", fp[:12], err)
	}

	bcVer, vErr := validateHBCBytes(data)
	if vErr != nil {
		// Corruption — drop the entry so we don't keep tripping on it,
		// and surface the reason so the user sees why their cache hit
		// became a miss.
		log.Printf("[hbc-cache] dropping corrupt entry %s: %v", fp[:12], vErr)
		_ = os.Remove(entry)
		_ = os.Remove(meta)
		return HBCCacheLookupResult{Hit: false}, fmt.Errorf("hbc-cache: corrupt entry %s: %w", fp[:12], vErr)
	}

	// Touch LastUsedAt for LRU. Best-effort — if this fails the next
	// eviction may consider the entry stale, which is acceptable.
	if m, err := readMetaSidecar(meta); err == nil {
		m.LastUsedAt = time.Now().UTC()
		_ = writeMetaSidecar(meta, m)
	}

	return HBCCacheLookupResult{Hit: true, Data: data, BCVersion: bcVer}, nil
}

// Store writes data to the cache atomically. Validates first — we
// never write something we wouldn't be willing to serve.
//
// On any error the cache is left in its prior state (no partial entry
// can be observed by Lookup).
func (c *HBCCache) Store(key HBCCacheKey, data []byte) error {
	bcVer, err := validateHBCBytes(data)
	if err != nil {
		return fmt.Errorf("hbc-cache: refusing to store invalid HBC: %w", err)
	}

	fp := key.fingerprint()
	entry := c.entryPath(fp)
	meta := c.metaPath(fp)

	// Atomic write — temp + rename. Avoids any partial-file window.
	if err := atomicWriteFile(entry, data, 0o644); err != nil {
		return fmt.Errorf("hbc-cache: store entry: %w", err)
	}
	srcHead := key.SourceHash
	if len(srcHead) > 12 {
		srcHead = srcHead[:12]
	}
	now := time.Now().UTC()
	if err := writeMetaSidecar(meta, hbcCacheMeta{
		CreatedAt:    now,
		LastUsedAt:   now,
		SizeBytes:    int64(len(data)),
		BCVersion:    bcVer,
		HermesVer:    key.HermesVersion,
		OptLevel:     key.OptLevel,
		SourceHashHd: srcHead,
	}); err != nil {
		// Meta write failure is non-fatal — entry is still readable;
		// it'll just be evicted earlier than ideal. Don't roll back
		// the entry write.
		log.Printf("[hbc-cache] meta write failed for %s: %v", fp[:12], err)
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.maxBytes > 0 {
		if err := c.evictLockedToFit(c.maxBytes); err != nil {
			// Eviction failure isn't fatal either; the disk just grows
			// a little more. Surface it so we notice in logs.
			log.Printf("[hbc-cache] eviction warning: %v", err)
		}
	}
	return nil
}

// evictLockedToFit walks the cache, sorts by LastUsedAt ascending, and
// removes oldest entries until total size fits under maxBytes. Caller
// must hold c.mu.
func (c *HBCCache) evictLockedToFit(maxBytes int64) error {
	type entryStat struct {
		fingerprint string
		size        int64
		lastUsed    time.Time
	}

	entries, err := os.ReadDir(c.root)
	if err != nil {
		return fmt.Errorf("read root: %w", err)
	}
	stats := make([]entryStat, 0, len(entries))
	var total int64
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".hbc") {
			continue
		}
		fp := strings.TrimSuffix(name, ".hbc")
		info, err := e.Info()
		if err != nil {
			continue
		}
		lastUsed := info.ModTime()
		if m, err := readMetaSidecar(c.metaPath(fp)); err == nil && !m.LastUsedAt.IsZero() {
			lastUsed = m.LastUsedAt
		}
		stats = append(stats, entryStat{
			fingerprint: fp,
			size:        info.Size(),
			lastUsed:    lastUsed,
		})
		total += info.Size()
	}

	if total <= maxBytes {
		return nil
	}

	sort.Slice(stats, func(i, j int) bool {
		return stats[i].lastUsed.Before(stats[j].lastUsed)
	})

	for _, s := range stats {
		if total <= maxBytes {
			break
		}
		_ = os.Remove(c.entryPath(s.fingerprint))
		_ = os.Remove(c.metaPath(s.fingerprint))
		total -= s.size
	}
	return nil
}

// Root returns the cache directory path. Useful for tests + diagnostics.
func (c *HBCCache) Root() string {
	return c.root
}

// HashJSBundle returns the cache key's SourceHash for a given input
// path. Reads the file in a single shot — bundles are typically a few
// MB, well within memory budget.
func HashJSBundle(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("hash js bundle: %w", err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

// validateHBCBytes is the SP2 gate. Returns the parsed BC version on
// success; any error means "do not serve this".
//
// We deliberately parse rather than blindly trust because:
//   - A truncated cache file (process killed mid-write) might still
//     have the magic but no body.
//   - A schema migration we missed could leave entries with the wrong
//     version field.
//   - Disk corruption is rare but real, and the consequence of serving
//     a malformed HBC to the phone is a hard crash on the device.
func validateHBCBytes(data []byte) (uint32, error) {
	if len(data) < hbcHeaderMinLen {
		return 0, fmt.Errorf("hbc too short (%d bytes; need ≥%d)", len(data), hbcHeaderMinLen)
	}
	// Magic at bytes 4..7 of an HBC file. Confirmed empirically with a
	// real bundle from yaver-test-ephemeral: on-disk layout is
	// `C1 03 19 1F`, so reading as little-endian yields the constant
	// 0x1F1903C1. The earlier comment claiming big-endian was wrong —
	// it caused every cache Store to fail with "magic mismatch: want
	// 0x1F1903C1 got 0xC103191F" (the byte-reversed value). Mobile's
	// YaverBundleValidator works because it interprets the same bytes
	// the same way; we mirror that.
	gotMagic := binary.LittleEndian.Uint32(data[hbcMagicOffset : hbcMagicOffset+4])
	if gotMagic != HBCFileMagic {
		return 0, fmt.Errorf("hbc magic mismatch: want 0x%08X, got 0x%08X", HBCFileMagic, gotMagic)
	}
	// BC version (little-endian).
	bcVer := binary.LittleEndian.Uint32(data[hbcVersionOffset : hbcVersionOffset+4])
	if bcVer == 0 {
		return 0, fmt.Errorf("hbc bc version is 0 (likely truncated)")
	}
	return bcVer, nil
}

// atomicWriteFile writes data to a temp sibling, fsyncs, then renames
// over the target. Concurrent writers may race the rename but the
// final state is always one valid file (no partial reads, no
// half-written content visible to readers).
func atomicWriteFile(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp.*")
	if err != nil {
		return fmt.Errorf("create temp: %w", err)
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("write temp: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("fsync temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return fmt.Errorf("close temp: %w", err)
	}
	if err := os.Chmod(tmpName, mode); err != nil {
		cleanup()
		return fmt.Errorf("chmod temp: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		cleanup()
		return fmt.Errorf("rename: %w", err)
	}
	return nil
}

func readMetaSidecar(path string) (hbcCacheMeta, error) {
	var m hbcCacheMeta
	data, err := os.ReadFile(path)
	if err != nil {
		return m, err
	}
	if err := json.Unmarshal(data, &m); err != nil {
		return m, err
	}
	return m, nil
}

func writeMetaSidecar(path string, m hbcCacheMeta) error {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return atomicWriteFile(path, data, 0o644)
}
