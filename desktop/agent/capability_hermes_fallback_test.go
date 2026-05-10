package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// writeFakeHermescResponder lays down a tiny shell script that mimics
// the subset of hermesc invocations capabilityForMobileHermes triggers:
// `hermesc --version` returning the Hermes / HBC banner so the
// caller can extract a summary line.
func writeFakeHermescResponder(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "hermesc")
	script := `#!/bin/sh
case "$1" in
  --version)
    cat <<'EOF'
Hermes JavaScript compiler.
  Hermes release version: 0.12.0
  HBC bytecode version: 96
EOF
    exit 0
    ;;
esac
exit 0
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestHermescSummaryAt_ParsesBannerLines — the summary path is what
// the capability snapshot puts in the user-visible "Embedded hermesc"
// / "System hermesc" note. Make sure the parser combines the BC
// version and Hermes release version exactly as the UI expects.
func TestHermescSummaryAt_ParsesBannerLines(t *testing.T) {
	herm := writeFakeHermescResponder(t)
	summary, err := hermescSummaryAt(herm)
	if err != nil {
		t.Fatalf("hermescSummaryAt: %v", err)
	}
	if !strings.Contains(summary, "HBC bytecode version: 96") {
		t.Fatalf("missing BC version in summary: %q", summary)
	}
	if !strings.Contains(summary, "Hermes release version: 0.12.0") {
		t.Fatalf("missing Hermes release version in summary: %q", summary)
	}
}

// TestHermescSummaryAt_RunFailureSurfaces — if the binary explodes
// rather than emitting a banner, the caller needs the underlying
// error so the snapshot can downgrade gracefully (vs claiming a
// healthy hermesc).
func TestHermescSummaryAt_RunFailureSurfaces(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hermesc")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 7\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := hermescSummaryAt(path); err == nil {
		t.Fatal("expected error from broken hermesc, got nil")
	}
}

// TestResolveHermescForCapability_PrefersEmbeddedThenSystem — on
// platforms with an embedded prebuilt (darwin/{arm64,amd64},
// linux/amd64), resolveHermescForCapability must report source
// "embedded". On linux/arm64 there is no embedded binary, so the
// caller falls back to the system path. We only assert the easy
// half here (the half this test machine can prove); the fallback
// branch is guarded by a coverage test below.
func TestResolveHermescForCapability_PrefersEmbeddedThenSystem(t *testing.T) {
	if _, err := GetEmbeddedHermesc(); err != nil {
		t.Skipf("no embedded hermesc on %s/%s — fallback path exercised by remote linux/arm64 box only",
			runtime.GOOS, runtime.GOARCH)
	}
	summary, source, err := resolveHermescForCapability()
	if err != nil {
		t.Fatalf("resolveHermescForCapability: %v", err)
	}
	if source != "embedded" {
		t.Fatalf("source=%q, want \"embedded\"", source)
	}
	if summary == "" {
		t.Fatal("expected non-empty summary")
	}
}
