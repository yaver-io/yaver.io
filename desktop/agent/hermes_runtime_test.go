package main

// Tests for the Hermes runtime layer.
//
// We don't shell out to a real hermes binary here — CI runners won't
// have it. We exercise:
//   - ValidateHermesBundle on real / corrupt / wrong-version inputs
//   - HermesRun behavior when the binary is missing (exec layer)
//   - HermesSmokeTest's overall short-circuit logic

import (
	"context"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

func writeHermesHeader(t *testing.T, dir, name string, magic, version uint32, extraTail []byte) string {
	t.Helper()
	path := filepath.Join(dir, name)
	buf := make([]byte, 16)
	// HBC layout per the source-of-truth in hermes_runtime.go: magic
	// lives at offset 4, version at offset 8.
	binary.LittleEndian.PutUint32(buf[4:8], magic)
	binary.LittleEndian.PutUint32(buf[8:12], version)
	if extraTail != nil {
		buf = append(buf, extraTail...)
	}
	if err := os.WriteFile(path, buf, 0644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

func TestValidateHermesBundle_Valid(t *testing.T) {
	dir := t.TempDir()
	p := writeHermesHeader(t, dir, "good.hbc", HermesBytecodeMagic, HermesBytecodeVersion, nil)
	v := ValidateHermesBundle(p)
	if !v.OK {
		t.Fatalf("expected OK, got: %+v", v)
	}
	if v.Magic != HermesBytecodeMagic || v.Version != HermesBytecodeVersion {
		t.Errorf("magic/version mismatch: %+v", v)
	}
}

func TestValidateHermesBundle_BadMagic(t *testing.T) {
	dir := t.TempDir()
	p := writeHermesHeader(t, dir, "bad.hbc", 0xdeadbeef, HermesBytecodeVersion, nil)
	v := ValidateHermesBundle(p)
	if v.OK {
		t.Fatal("expected OK=false on bad magic")
	}
	if v.Error == "" {
		t.Error("expected non-empty error on bad magic")
	}
}

func TestValidateHermesBundle_VersionMismatch(t *testing.T) {
	dir := t.TempDir()
	// Magic OK, version old. Should be OK=true but Error non-empty
	// (treat as warning, not fatal).
	p := writeHermesHeader(t, dir, "old.hbc", HermesBytecodeMagic, HermesBytecodeVersion-1, nil)
	v := ValidateHermesBundle(p)
	if !v.OK {
		t.Errorf("expected OK=true on version mismatch (warning, not fatal): %+v", v)
	}
	if v.Error == "" {
		t.Error("expected warning message on version mismatch")
	}
}

func TestValidateHermesBundle_Missing(t *testing.T) {
	v := ValidateHermesBundle(filepath.Join(t.TempDir(), "no-such-file.hbc"))
	if v.OK {
		t.Error("expected OK=false on missing file")
	}
	if v.Error == "" {
		t.Error("expected non-empty error on missing file")
	}
}

func TestValidateHermesBundle_Directory(t *testing.T) {
	dir := t.TempDir()
	v := ValidateHermesBundle(dir)
	if v.OK {
		t.Error("expected OK=false on directory")
	}
}

func TestValidateHermesBundle_TooShort(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "tiny.hbc")
	if err := os.WriteFile(p, []byte{0x01, 0x02}, 0644); err != nil {
		t.Fatal(err)
	}
	v := ValidateHermesBundle(p)
	if v.OK {
		t.Error("expected OK=false on 2-byte file")
	}
}

func TestHermesRun_BinaryMissing(t *testing.T) {
	// Force binary resolution to fail by pointing the override at a
	// non-existent path and stripping hermes from PATH for the test.
	t.Setenv("YAVER_HERMES_BIN", "/nonexistent/path/to/hermes")
	res := HermesRun(context.Background(), HermesRunOpts{
		BundlePath: filepath.Join(t.TempDir(), "anything.hbc"),
	})
	if res.Error == "" {
		t.Fatalf("expected Error when binary missing, got: %+v", res)
	}
}

func TestHermesSmokeTest_BadHeaderShortCircuits(t *testing.T) {
	dir := t.TempDir()
	// Write a file at index.hbc with wrong magic. Smoke test should
	// fail fast at validation and never attempt to invoke hermes.
	writeHermesHeader(t, dir, "index.hbc", 0xbaadf00d, 96, nil)
	t.Setenv("YAVER_HERMES_BIN", "/nonexistent/path") // would fail if reached
	res := HermesSmokeTest(context.Background(), dir)
	if res.OK {
		t.Fatal("expected OK=false on bad header")
	}
	if res.Hint == "" {
		t.Error("expected user-facing hint on smoke fail")
	}
	// Run should NOT have been attempted — no error from binary lookup
	if res.Run.Error != "" {
		t.Errorf("smoke test should short-circuit before invoking hermes; got Run.Error=%q", res.Run.Error)
	}
}

func TestHermesSmokeTest_MissingIndexHbc(t *testing.T) {
	dir := t.TempDir()
	res := HermesSmokeTest(context.Background(), dir)
	if res.OK {
		t.Fatal("expected OK=false when index.hbc missing")
	}
	if res.Validation.OK {
		t.Errorf("validation should fail when file missing")
	}
}
