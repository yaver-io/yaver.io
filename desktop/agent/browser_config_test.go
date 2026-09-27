package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeBrowserConfig(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	yaverDir := filepath.Join(dir, ".yaver")
	if err := os.MkdirAll(yaverDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(yaverDir, "browser.yaml"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestResolveBrowserTargetDefaultsToChromeCDPWithoutConfig(t *testing.T) {
	name, target, err := resolveBrowserTarget(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	if name != "default" || target.Engine != "chrome" || target.Driver != "cdp" {
		t.Fatalf("unexpected default: %q %#v", name, target)
	}
}

func TestResolveBrowserTargetPreservesRealSafari(t *testing.T) {
	dir := writeBrowserConfig(t, `version: 1
default: safari-mac
targets:
  safari-mac:
    engine: safari
    driver: webdriver
    headful: true
`)
	name, target, err := resolveBrowserTarget(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	if name != "safari-mac" || target.Engine != "safari" || target.Driver != "webdriver" || !target.Headful {
		t.Fatalf("unexpected Safari target: %q %#v", name, target)
	}
}

func TestResolveBrowserTargetRejectsUnsupportedEngineInsteadOfMislabeling(t *testing.T) {
	dir := writeBrowserConfig(t, `version: 1
default: portable
targets:
  portable:
    engine: webkit
    driver: playwright
`)
	_, _, err := resolveBrowserTarget(dir, "")
	if err == nil || !strings.Contains(err.Error(), "engine must be chrome, firefox, or safari") {
		t.Fatalf("expected named unsupported-engine error, got %v", err)
	}
}
