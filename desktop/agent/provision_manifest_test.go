package main

import (
	"os"
	"path/filepath"
	"testing"
)

func writeManifest(t *testing.T, dir, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, ProvisionManifestName), []byte(body), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
}

func TestLoadProvisionManifest(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, dir, `
version: 1
product: talos-edge-v1
model: "Talos Edge Node"
vendor: Talos
platform: linux
services:
  - modbus-master
  - edge-loop
setup:
  - name: bring up companion workload
    run: "yaver companion up"
  - name: optional warmup
    run: "echo warm"
    allow_failure: true
`)
	m, err := LoadProvisionManifest(dir)
	if err != nil {
		t.Fatalf("LoadProvisionManifest: %v", err)
	}
	if m == nil {
		t.Fatal("expected manifest, got nil")
	}
	if m.Product != "talos-edge-v1" || m.Model != "Talos Edge Node" || m.Vendor != "Talos" {
		t.Fatalf("unexpected fields: %+v", m)
	}
	if len(m.Services) != 2 || m.Services[0] != "modbus-master" {
		t.Fatalf("services not parsed: %+v", m.Services)
	}
	if len(m.Setup) != 2 || m.Setup[0].Run != "yaver companion up" || !m.Setup[1].AllowFailure {
		t.Fatalf("setup not parsed: %+v", m.Setup)
	}
}

func TestLoadProvisionManifestAbsentAndInvalid(t *testing.T) {
	// Absent → (nil, nil).
	dir := t.TempDir()
	if m, err := LoadProvisionManifest(dir); err != nil || m != nil {
		t.Fatalf("absent manifest: got m=%v err=%v, want nil,nil", m, err)
	}
	// Missing required product → error.
	bad := t.TempDir()
	writeManifest(t, bad, "version: 1\nmodel: x\n")
	if _, err := LoadProvisionManifest(bad); err == nil {
		t.Fatal("expected error for manifest missing `product`")
	}
}

func TestFindProvisionManifestViaEnv(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, dir, "version: 1\nproduct: acme-box\nmodel: Acme Box\n")
	t.Setenv("YAVER_PROVISION_MANIFEST_DIR", dir)
	m, err := FindProvisionManifest("")
	if err != nil {
		t.Fatalf("FindProvisionManifest: %v", err)
	}
	if m == nil || m.Product != "acme-box" {
		t.Fatalf("did not find manifest via env: %+v", m)
	}
}

func TestRunProvisionSetup(t *testing.T) {
	// All steps succeed.
	ok := runProvisionSetup(&ProvisionManifest{
		Product: "p",
		Setup: []ProvisionSetupStep{
			{Name: "a", Run: "true"},
			{Name: "b", Run: "true"},
		},
	})
	if !ok {
		t.Fatal("expected success when all steps pass")
	}

	// A required step fails → abort (false).
	ok = runProvisionSetup(&ProvisionManifest{
		Product: "p",
		Setup: []ProvisionSetupStep{
			{Name: "boom", Run: "false"},
			{Name: "never", Run: "true"},
		},
	})
	if ok {
		t.Fatal("expected failure when a required step fails")
	}

	// A failing step with allow_failure does not abort.
	ok = runProvisionSetup(&ProvisionManifest{
		Product: "p",
		Setup: []ProvisionSetupStep{
			{Name: "soft", Run: "false", AllowFailure: true},
			{Name: "ok", Run: "true"},
		},
	})
	if !ok {
		t.Fatal("expected success when only allow_failure steps fail")
	}
}
