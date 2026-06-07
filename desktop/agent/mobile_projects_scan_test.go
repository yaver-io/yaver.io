package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// writeExpoProject creates a minimal Expo project (package.json + .git) so
// the scanner detects it as a mobile-capable project regardless of where
// it sits.
func writeExpoProject(t *testing.T, parent, name string) string {
	t.Helper()
	dir := filepath.Join(parent, name)
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatalf("mkdir project: %v", err)
	}
	pkg := `{"name":"` + name + `","dependencies":{"expo":"^51.0.0","react-native":"0.74.0"}}`
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(pkg), 0o644); err != nil {
		t.Fatalf("write package.json: %v", err)
	}
	return dir
}

// resetMobileCache clears global scan state so tests don't bleed into each
// other (the cache is package-global).
func resetMobileCache() {
	mobileProjectCache.mu.Lock()
	mobileProjectCache.projects = nil
	mobileProjectCache.scannedAt = time.Time{}
	mobileProjectCache.scanning = false
	mobileProjectCache.cancel = false
	mobileProjectCache.stats = mobileScanStats{}
	mobileProjectCache.mu.Unlock()
}

func TestScanFindsExpoProject(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeExpoProject(t, home, "myapp")

	projects, stats := scanMobileProjectsWithDeadline(time.Now().Add(mobileScanTimeout))
	if stats.TimedOut {
		t.Fatalf("scan timed out unexpectedly")
	}
	found := false
	for _, p := range projects {
		// Name is a display string ("myapp / mobile"); match on path + framework.
		if p.Framework == "expo" && p.MobileCapable && filepath.Base(p.Path) == "myapp" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected to find expo project at .../myapp, got %+v", projects)
	}
}

func TestScanRespectsDeadline(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeExpoProject(t, home, "myapp")

	// A deadline already in the past must abort on the very first entry and
	// flag TimedOut — proving a runaway walk can never spin forever.
	start := time.Now()
	_, stats := scanMobileProjectsWithDeadline(time.Now().Add(-time.Second))
	if !stats.TimedOut {
		t.Fatalf("expected TimedOut=true for a past deadline")
	}
	if time.Since(start) > 5*time.Second {
		t.Fatalf("past-deadline scan should return immediately, took %s", time.Since(start))
	}
}

func TestScanCountsPermissionDenied(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root bypasses permission checks")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeExpoProject(t, home, "myapp")

	// A directory the agent can't read (mirrors macOS TCC denying access to
	// a protected folder without Full Disk Access).
	locked := filepath.Join(home, "locked")
	if err := os.MkdirAll(locked, 0o755); err != nil {
		t.Fatalf("mkdir locked: %v", err)
	}
	if err := os.Chmod(locked, 0o000); err != nil {
		t.Fatalf("chmod locked: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) }) // so TempDir cleanup works

	_, stats := scanMobileProjectsWithDeadline(time.Now().Add(mobileScanTimeout))
	if stats.PermDenied < 1 {
		t.Fatalf("expected PermDenied >= 1 for an unreadable dir, got %d", stats.PermDenied)
	}
}

func TestRunMobileScanAlwaysClearsScanning(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeExpoProject(t, home, "myapp")
	resetMobileCache()

	runMobileScan("unit-test")

	mobileProjectCache.mu.RLock()
	scanning := mobileProjectCache.scanning
	count := len(mobileProjectCache.projects)
	elapsedMs := mobileProjectCache.stats.ElapsedMs
	mobileProjectCache.mu.RUnlock()

	if scanning {
		t.Fatalf("scanning must be false after runMobileScan returns")
	}
	if count < 1 {
		t.Fatalf("expected at least one project cached, got %d", count)
	}
	if elapsedMs < 0 {
		t.Fatalf("elapsedMs should be recorded, got %d", elapsedMs)
	}
}

func TestRunMobileScanCoalescesConcurrent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeExpoProject(t, home, "myapp")
	resetMobileCache()

	// Simulate an in-flight scan; a second caller must no-op (the guard),
	// leaving the in-flight flag intact rather than racing the cache.
	mobileProjectCache.mu.Lock()
	mobileProjectCache.scanning = true
	mobileProjectCache.mu.Unlock()

	runMobileScan("should-noop") // returns immediately because scanning==true

	mobileProjectCache.mu.RLock()
	stillScanning := mobileProjectCache.scanning
	mobileProjectCache.mu.RUnlock()
	if !stillScanning {
		t.Fatalf("guard should leave the in-flight scan flag set")
	}
	resetMobileCache()
}
