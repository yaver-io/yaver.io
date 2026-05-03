package main

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// makeValidHBC produces a byte slice that passes validateHBCBytes.
// We don't need a real Hermes-emitted bytecode for cache tests — we
// just need the magic + BC version slots filled correctly.
func makeValidHBC(t *testing.T, bcVersion uint32, payloadSize int) []byte {
	t.Helper()
	if payloadSize < hbcHeaderMinLen {
		payloadSize = hbcHeaderMinLen
	}
	buf := make([]byte, payloadSize)
	// Magic at offset 4 is little-endian on disk (matches real HBC
	// produced by hermesc — verified empirically against a 4 MB
	// bundle from yaver-test-ephemeral). The test harness has to
	// write the same shape the validator now reads.
	binary.LittleEndian.PutUint32(buf[hbcMagicOffset:hbcMagicOffset+4], HBCFileMagic)
	binary.LittleEndian.PutUint32(buf[hbcVersionOffset:hbcVersionOffset+4], bcVersion)
	return buf
}

func newTestCache(t *testing.T) *HBCCache {
	t.Helper()
	root := t.TempDir()
	c, err := NewHBCCache(root, 1<<20)
	if err != nil {
		t.Fatalf("NewHBCCache: %v", err)
	}
	return c
}

func TestHBCCache_RoundTrip(t *testing.T) {
	c := newTestCache(t)
	key := HBCCacheKey{
		SourceHash:    "abc123",
		HermesVersion: "hermes-v1",
		OptLevel:      "O0",
	}
	payload := makeValidHBC(t, 96, 256)

	// Miss before any store.
	if r, err := c.Lookup(key); err != nil || r.Hit {
		t.Fatalf("expected clean miss, got hit=%v err=%v", r.Hit, err)
	}

	if err := c.Store(key, payload); err != nil {
		t.Fatalf("Store: %v", err)
	}

	r, err := c.Lookup(key)
	if err != nil {
		t.Fatalf("Lookup after store: %v", err)
	}
	if !r.Hit {
		t.Fatal("expected hit after store")
	}
	if string(r.Data) != string(payload) {
		t.Fatalf("payload mismatch: got %d bytes, want %d", len(r.Data), len(payload))
	}
	if r.BCVersion != 96 {
		t.Fatalf("bc version: got %d, want 96", r.BCVersion)
	}
}

func TestHBCCache_DifferentKeysDifferentEntries(t *testing.T) {
	c := newTestCache(t)
	keyA := HBCCacheKey{SourceHash: "a", HermesVersion: "v1", OptLevel: "O"}
	keyB := HBCCacheKey{SourceHash: "b", HermesVersion: "v1", OptLevel: "O"}
	keyOpt := HBCCacheKey{SourceHash: "a", HermesVersion: "v1", OptLevel: "O0"}
	keyHermes := HBCCacheKey{SourceHash: "a", HermesVersion: "v2", OptLevel: "O"}
	keySmap := HBCCacheKey{SourceHash: "a", HermesVersion: "v1", OptLevel: "O", EmitSourceMap: true}

	payloadA := makeValidHBC(t, 96, 64)
	payloadB := makeValidHBC(t, 96, 96)
	payloadO := makeValidHBC(t, 96, 128)
	payloadH := makeValidHBC(t, 97, 96)
	payloadS := makeValidHBC(t, 96, 200)

	for _, kp := range []struct {
		k HBCCacheKey
		p []byte
	}{
		{keyA, payloadA},
		{keyB, payloadB},
		{keyOpt, payloadO},
		{keyHermes, payloadH},
		{keySmap, payloadS},
	} {
		if err := c.Store(kp.k, kp.p); err != nil {
			t.Fatalf("Store: %v", err)
		}
	}

	checks := []struct {
		k HBCCacheKey
		p []byte
	}{
		{keyA, payloadA},
		{keyB, payloadB},
		{keyOpt, payloadO},
		{keyHermes, payloadH},
		{keySmap, payloadS},
	}
	for _, c2 := range checks {
		r, err := c.Lookup(c2.k)
		if err != nil || !r.Hit {
			t.Fatalf("lookup miss for key %+v: hit=%v err=%v", c2.k, r.Hit, err)
		}
		if len(r.Data) != len(c2.p) {
			t.Fatalf("payload size mismatch for %+v: got %d want %d", c2.k, len(r.Data), len(c2.p))
		}
	}
}

func TestHBCCache_RejectsCorruptOnStore(t *testing.T) {
	c := newTestCache(t)
	key := HBCCacheKey{SourceHash: "x"}

	// Too short.
	if err := c.Store(key, []byte("nope")); err == nil {
		t.Fatal("Store accepted too-short payload")
	}

	// Wrong magic.
	bad := make([]byte, 64)
	binary.BigEndian.PutUint32(bad[hbcMagicOffset:hbcMagicOffset+4], 0xDEADBEEF)
	binary.LittleEndian.PutUint32(bad[hbcVersionOffset:hbcVersionOffset+4], 96)
	if err := c.Store(key, bad); err == nil {
		t.Fatal("Store accepted bad-magic payload")
	}

	// BC version 0 (truncation marker).
	zero := make([]byte, 64)
	binary.LittleEndian.PutUint32(zero[hbcMagicOffset:hbcMagicOffset+4], HBCFileMagic)
	if err := c.Store(key, zero); err == nil {
		t.Fatal("Store accepted bc-version-0 payload")
	}
}

func TestHBCCache_DropsCorruptEntryOnLookup(t *testing.T) {
	c := newTestCache(t)
	key := HBCCacheKey{SourceHash: "x"}
	payload := makeValidHBC(t, 96, 128)

	// Store legit, then corrupt the file behind the cache's back.
	if err := c.Store(key, payload); err != nil {
		t.Fatalf("Store: %v", err)
	}
	fp := key.fingerprint()
	entry := filepath.Join(c.Root(), fp+".hbc")
	if err := os.WriteFile(entry, []byte("garbage"), 0o644); err != nil {
		t.Fatalf("corrupt entry: %v", err)
	}

	// Lookup must report miss + an error, AND must remove the bad
	// entry so subsequent lookups don't keep tripping on it.
	r, err := c.Lookup(key)
	if r.Hit {
		t.Fatal("Lookup returned hit on corrupt entry")
	}
	if err == nil {
		t.Fatal("Lookup did not surface corruption error")
	}
	if !strings.Contains(err.Error(), "corrupt") {
		t.Fatalf("error should mention corruption, got %v", err)
	}
	if _, statErr := os.Stat(entry); !os.IsNotExist(statErr) {
		t.Fatal("corrupt entry was not removed from disk")
	}

	// Second lookup is a clean miss with no error.
	r2, err2 := c.Lookup(key)
	if err2 != nil || r2.Hit {
		t.Fatalf("expected clean miss after drop, got hit=%v err=%v", r2.Hit, err2)
	}
}

func TestHBCCache_AtomicWriteNoPartial(t *testing.T) {
	// We can't easily simulate a process kill mid-write, but we can
	// assert that Store either leaves NO file or a fully-valid file —
	// never a half-written one. The atomic-rename pattern guarantees
	// this; the test confirms there is no path where the entry file
	// exists with a length other than the input payload.
	c := newTestCache(t)
	key := HBCCacheKey{SourceHash: "atomic"}
	payload := makeValidHBC(t, 96, 8192)
	if err := c.Store(key, payload); err != nil {
		t.Fatalf("Store: %v", err)
	}
	entry := filepath.Join(c.Root(), key.fingerprint()+".hbc")
	got, err := os.ReadFile(entry)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(got) != len(payload) {
		t.Fatalf("entry size %d != payload %d (partial write?)", len(got), len(payload))
	}
}

func TestHBCCache_ConcurrentStoresSafe(t *testing.T) {
	// Two goroutines storing to the SAME key shouldn't trample each
	// other's atomicity. Final state must be a complete, valid entry.
	c := newTestCache(t)
	key := HBCCacheKey{SourceHash: "race"}
	payloadA := makeValidHBC(t, 96, 4096)
	payloadB := makeValidHBC(t, 96, 4096)
	// Make A and B distinguishable.
	for i := range payloadA {
		if i >= hbcHeaderMinLen {
			payloadA[i] = 0xAA
		}
	}
	for i := range payloadB {
		if i >= hbcHeaderMinLen {
			payloadB[i] = 0xBB
		}
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); _ = c.Store(key, payloadA) }()
	go func() { defer wg.Done(); _ = c.Store(key, payloadB) }()
	wg.Wait()

	r, err := c.Lookup(key)
	if err != nil || !r.Hit {
		t.Fatalf("Lookup after concurrent stores: hit=%v err=%v", r.Hit, err)
	}
	if len(r.Data) != len(payloadA) {
		t.Fatalf("entry size %d, expected %d", len(r.Data), len(payloadA))
	}
	// Body byte must be 0xAA OR 0xBB — never a mix (which would mean
	// a non-atomic overlay).
	body := r.Data[hbcHeaderMinLen]
	for i := hbcHeaderMinLen; i < len(r.Data); i++ {
		if r.Data[i] != body {
			t.Fatalf("non-atomic payload at offset %d: got %02x, expected uniform %02x", i, r.Data[i], body)
		}
	}
}

func TestHBCCache_SchemaInvalidationWipes(t *testing.T) {
	root := t.TempDir()
	// Create a cache, store an entry.
	c1, err := NewHBCCache(root, 1<<20)
	if err != nil {
		t.Fatalf("NewHBCCache 1: %v", err)
	}
	key := HBCCacheKey{SourceHash: "schema"}
	if err := c1.Store(key, makeValidHBC(t, 96, 256)); err != nil {
		t.Fatalf("Store: %v", err)
	}

	// Tamper with SCHEMA file to simulate an old version on disk.
	if err := os.WriteFile(filepath.Join(root, "SCHEMA"), []byte("0\n"), 0o644); err != nil {
		t.Fatalf("tamper schema: %v", err)
	}

	// Re-open: must wipe.
	c2, err := NewHBCCache(root, 1<<20)
	if err != nil {
		t.Fatalf("NewHBCCache 2: %v", err)
	}
	r, err := c2.Lookup(key)
	if err != nil {
		t.Fatalf("Lookup after schema bump: %v", err)
	}
	if r.Hit {
		t.Fatal("entry survived schema bump (should have been wiped)")
	}
}

func TestHBCCache_LRUEvictionToFit(t *testing.T) {
	// Cache cap of 4 KiB. Store five 1-KiB entries; oldest must be
	// evicted to fit.
	root := t.TempDir()
	c, err := NewHBCCache(root, 4096)
	if err != nil {
		t.Fatalf("NewHBCCache: %v", err)
	}
	type entry struct {
		k HBCCacheKey
		p []byte
	}
	entries := []entry{
		{HBCCacheKey{SourceHash: "1"}, makeValidHBC(t, 96, 1024)},
		{HBCCacheKey{SourceHash: "2"}, makeValidHBC(t, 96, 1024)},
		{HBCCacheKey{SourceHash: "3"}, makeValidHBC(t, 96, 1024)},
		{HBCCacheKey{SourceHash: "4"}, makeValidHBC(t, 96, 1024)},
		{HBCCacheKey{SourceHash: "5"}, makeValidHBC(t, 96, 1024)},
	}
	for _, e := range entries {
		if err := c.Store(e.k, e.p); err != nil {
			t.Fatalf("Store %s: %v", e.k.SourceHash, err)
		}
	}
	// At least one must have been evicted (5 KiB > 4 KiB cap).
	hits := 0
	for _, e := range entries {
		r, err := c.Lookup(e.k)
		if err != nil {
			continue
		}
		if r.Hit {
			hits++
		}
	}
	if hits >= 5 {
		t.Fatalf("expected at least one eviction, got %d hits / 5", hits)
	}
}

func TestHBCCache_FingerprintStable(t *testing.T) {
	// Same key fields must produce the same fingerprint across calls
	// — otherwise cache lookups never hit. This is the determinism
	// guarantee the doc's §13.1 calls out.
	k1 := HBCCacheKey{
		SourceHash:    "src",
		HermesVersion: "v1",
		OptLevel:      "O0",
		EmitSourceMap: false,
		TargetArch:    "arm64",
		Extra:         map[string]string{"a": "1", "b": "2"},
	}
	k2 := HBCCacheKey{
		SourceHash:    "src",
		HermesVersion: "v1",
		OptLevel:      "O0",
		EmitSourceMap: false,
		TargetArch:    "arm64",
		Extra:         map[string]string{"b": "2", "a": "1"}, // inserted in different order
	}
	if k1.fingerprint() != k2.fingerprint() {
		t.Fatal("fingerprint differs by Extra-map insertion order; ordering must be stable")
	}

	// Sanity: any field change shifts the fingerprint.
	mutators := []func(*HBCCacheKey){
		func(k *HBCCacheKey) { k.SourceHash = "src2" },
		func(k *HBCCacheKey) { k.HermesVersion = "v2" },
		func(k *HBCCacheKey) { k.OptLevel = "O" },
		func(k *HBCCacheKey) { k.EmitSourceMap = true },
		func(k *HBCCacheKey) { k.TargetArch = "x86_64" },
		func(k *HBCCacheKey) { k.Extra = map[string]string{"a": "1", "b": "3"} },
	}
	base := k1.fingerprint()
	for i, mut := range mutators {
		k := k1
		k.Extra = map[string]string{}
		for ek, ev := range k1.Extra {
			k.Extra[ek] = ev
		}
		mut(&k)
		if k.fingerprint() == base {
			t.Fatalf("mutator %d did not change fingerprint", i)
		}
	}
}

func TestValidateHBCBytes_Cases(t *testing.T) {
	// Magic and BC version validation is the first line of defence
	// before serving cache hits. Each branch tested explicitly.
	cases := []struct {
		name    string
		data    []byte
		wantErr string
		wantVer uint32
	}{
		{"too-short", []byte{1, 2, 3}, "too short", 0},
		{
			"bad-magic",
			func() []byte {
				b := make([]byte, 64)
				binary.BigEndian.PutUint32(b[hbcMagicOffset:hbcMagicOffset+4], 0xDEADBEEF)
				binary.LittleEndian.PutUint32(b[hbcVersionOffset:hbcVersionOffset+4], 96)
				return b
			}(),
			"magic mismatch", 0,
		},
		{
			"zero-bc-version",
			func() []byte {
				b := make([]byte, 64)
				binary.LittleEndian.PutUint32(b[hbcMagicOffset:hbcMagicOffset+4], HBCFileMagic)
				return b
			}(),
			"bc version is 0", 0,
		},
		{
			"valid-96",
			makeValidHBC(t, 96, 64),
			"", 96,
		},
		{
			"valid-large-version",
			makeValidHBC(t, 4_000_000_000, 64),
			"", 4_000_000_000,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ver, err := validateHBCBytes(tc.data)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if ver != tc.wantVer {
					t.Fatalf("ver = %d, want %d", ver, tc.wantVer)
				}
			} else {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tc.wantErr)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("error %q does not contain %q", err, tc.wantErr)
				}
			}
		})
	}
}

func TestHashJSBundle_Determinism(t *testing.T) {
	// Same bytes → same hash, different bytes → different hash.
	tmp := t.TempDir()
	a := filepath.Join(tmp, "a.js")
	b := filepath.Join(tmp, "b.js")
	if err := os.WriteFile(a, []byte("module.exports = 1;\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(b, []byte("module.exports = 1;\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	hA, err := HashJSBundle(a)
	if err != nil {
		t.Fatal(err)
	}
	hB, err := HashJSBundle(b)
	if err != nil {
		t.Fatal(err)
	}
	if hA != hB {
		t.Fatalf("identical contents produced different hashes: %s vs %s", hA, hB)
	}

	if err := os.WriteFile(b, []byte("module.exports = 2;\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	hB2, err := HashJSBundle(b)
	if err != nil {
		t.Fatal(err)
	}
	if hB2 == hA {
		t.Fatal("different contents produced identical hash")
	}
}
