package main

// Tests for ValidateHBC — the contract is "if I send these bytes to a
// phone they will not crash". Today the only verification was a manual
// scripts/qemu-hermes-cycle.sh sweep, so a corrupt bundle could ship
// silently. Fail any of these and the relay-side push gate trips.
//
// All cases write a fixture file to t.TempDir() and call ValidateHBC.
// No hermesc / metro / network deps — pure unit.

import (
	"bytes"
	"crypto/md5"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeBundleFixture builds a synthetic file that looks like an HBC
// header followed by `pad` zero bytes, returning its path. The
// 12-byte header is:
//
//	bytes  0–3   reserved (any 4 bytes; ValidateHBC ignores them)
//	bytes  4–7   magic, little-endian uint32
//	bytes  8–11  bytecode version, little-endian uint32
func writeBundleFixture(t *testing.T, magic, bcVersion uint32, pad int) string {
	t.Helper()
	hdr := make([]byte, 12)
	// reserved field — any value is fine
	hdr[0], hdr[1], hdr[2], hdr[3] = 0xDE, 0xAD, 0xBE, 0xEF
	hdr[4] = byte(magic)
	hdr[5] = byte(magic >> 8)
	hdr[6] = byte(magic >> 16)
	hdr[7] = byte(magic >> 24)
	hdr[8] = byte(bcVersion)
	hdr[9] = byte(bcVersion >> 8)
	hdr[10] = byte(bcVersion >> 16)
	hdr[11] = byte(bcVersion >> 24)

	body := append(hdr, bytes.Repeat([]byte{0xAB}, pad)...)
	path := filepath.Join(t.TempDir(), "fixture.hbc")
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return path
}

func TestValidateHBC_HappyPath(t *testing.T) {
	// Pad with enough bytes to clear MinBundleSize (1024) so the size
	// gate doesn't fail before we test magic + BC.
	path := writeBundleFixture(t, HermesMagic, 96, int(MinBundleSize))
	meta, err := ValidateHBC(path, 96)
	if err != nil {
		t.Fatalf("expected ok, got %v", err)
	}
	if meta.HermesBCVersion != 96 {
		t.Errorf("want BC=96, got %d", meta.HermesBCVersion)
	}
	if meta.Format != "hbc" {
		t.Errorf("want format=hbc, got %q", meta.Format)
	}
	if meta.Size != int64(12+int(MinBundleSize)) {
		t.Errorf("want size=%d, got %d", 12+int(MinBundleSize), meta.Size)
	}
	if meta.Version != 1 {
		t.Errorf("want protocol Version=1, got %d", meta.Version)
	}
	// MD5 must round-trip — recompute and compare. This is the
	// contract the mobile validator relies on (X-Yaver-Bundle-Metadata
	// includes md5; YaverBundleValidator recomputes and rejects on
	// mismatch). If we ever silently swap MD5 for SHA-1 etc., this
	// fails noisily.
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	want := hex.EncodeToString(md5.New().Sum(nil)[:0]) // primer
	h := md5.New()
	h.Write(raw)
	want = hex.EncodeToString(h.Sum(nil))
	if meta.MD5 != want {
		t.Errorf("MD5 mismatch: got %s want %s", meta.MD5, want)
	}
}

func TestValidateHBC_MagicMismatch(t *testing.T) {
	// Wrong magic bytes — would catch e.g. a raw Metro JS bundle
	// (which has a different signature) or a tarball mistakenly
	// served as the HBC payload.
	path := writeBundleFixture(t, 0xCAFEBABE, 96, int(MinBundleSize))
	meta, err := ValidateHBC(path, 96)
	if err == nil {
		t.Fatalf("expected magic mismatch error, got meta=%+v", meta)
	}
	msg := err.Error()
	if !strings.Contains(msg, "NOT a Hermes bytecode bundle") {
		t.Errorf("error should call out the wrong-bundle case explicitly: %v", err)
	}
	if !strings.Contains(msg, "0xCAFEBABE") {
		t.Errorf("error should include the offending magic for debugging: %v", err)
	}
}

func TestValidateHBC_BCVersionMismatch(t *testing.T) {
	// HBC magic OK but bytecode version wrong — would happen if a
	// dev built with the wrong RN version's hermesc. The classic
	// failure mode this catches is "everything looks fine but the
	// app crashes on bridge load".
	path := writeBundleFixture(t, HermesMagic, 89, int(MinBundleSize))
	_, err := ValidateHBC(path, 96)
	if err == nil {
		t.Fatal("expected BC mismatch error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "BC89") || !strings.Contains(msg, "BC96") {
		t.Errorf("error should name both the actual and expected BC: %v", err)
	}
}

func TestValidateHBC_BCMatchSkippedWhenExpectedZero(t *testing.T) {
	// expectedBCVersion=0 means caller doesn't care — we still
	// validate magic + size but skip the BC gate. This is what
	// /dev/native-bundle uses when the manifest's BC isn't loaded
	// yet (early boot path).
	path := writeBundleFixture(t, HermesMagic, 42, int(MinBundleSize))
	meta, err := ValidateHBC(path, 0)
	if err != nil {
		t.Fatalf("expected ok with no BC check, got %v", err)
	}
	if meta.HermesBCVersion != 42 {
		t.Errorf("metadata should still report the actual BC for downstream use: got %d", meta.HermesBCVersion)
	}
}

func TestValidateHBC_TooSmall(t *testing.T) {
	// File shorter than MinBundleSize → catches the "build silently
	// produced 0-byte output" failure mode that bit us before the
	// gate existed. Pad=0 → file is 12 bytes (header only).
	path := writeBundleFixture(t, HermesMagic, 96, 0)
	_, err := ValidateHBC(path, 96)
	if err == nil {
		t.Fatal("expected too-small error")
	}
	if !strings.Contains(err.Error(), "too small") {
		t.Errorf("error should say too small: %v", err)
	}
}

func TestValidateHBC_MissingFile(t *testing.T) {
	_, err := ValidateHBC(filepath.Join(t.TempDir(), "no-such.hbc"), 96)
	if err == nil {
		t.Fatal("expected open error for missing file")
	}
	if !strings.Contains(err.Error(), "cannot open") {
		t.Errorf("error should be the open error: %v", err)
	}
}

func TestValidateHBC_TruncatedHeader(t *testing.T) {
	// File is over MinBundleSize so size gate passes, but header is
	// only 8 bytes — reading 12 should fail.
	path := filepath.Join(t.TempDir(), "trunc.hbc")
	body := append([]byte{0, 0, 0, 0, 0xC1, 0x03, 0x19, 0x1F}, bytes.Repeat([]byte{0}, int(MinBundleSize))...)
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	// We have 8 + MinBundleSize bytes total — that's ≥ MinBundleSize so
	// the size gate passes, but the BC version (offset 8..11) is part
	// of the padding, which is all zeros. So magic check passes, BC
	// reads as 0, expected=96 → BC mismatch. Verifies graceful
	// degradation rather than a panic.
	_, err := ValidateHBC(path, 96)
	if err == nil {
		t.Fatal("expected BC mismatch on padded zeros")
	}
	if !strings.Contains(err.Error(), "BC0") && !strings.Contains(err.Error(), "BC96") {
		t.Errorf("expected BC mismatch error, got %v", err)
	}
}

func TestBundleMetadataJSON_RoundTrip(t *testing.T) {
	// The X-Yaver-Bundle-Metadata header sends BundleMetadata as
	// JSON. The mobile validator parses it back. Lock the wire shape
	// — anything that breaks the JSON keys breaks the iPhone too.
	meta := &BundleMetadata{
		Version:         1,
		Size:            12345,
		MD5:             "abc123",
		HermesBCVersion: 96,
		ModuleName:      "main",
		Format:          "hbc",
	}
	js := meta.JSON()
	for _, key := range []string{`"version":`, `"size":`, `"md5":`, `"hermesBCVersion":`, `"moduleName":`, `"format":`} {
		if !strings.Contains(js, key) {
			t.Errorf("JSON missing key %q: %s", key, js)
		}
	}
}
