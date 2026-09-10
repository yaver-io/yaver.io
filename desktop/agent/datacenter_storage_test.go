package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDatacenterStorageReadinessLocalIsOperationalAndSafe(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "present.txt"), []byte("ok"), 0o600); err != nil {
		t.Fatal(err)
	}
	profile := SharedStorageProfile{
		ID: "ssd-cache", Name: "Private SSD", Type: "local", Path: root,
		Username: "private-user", Password: "private-password", Notes: "private note",
	}
	summary := datacenterStorageReadiness(context.Background(), profile)
	if summary.Readiness != DatacenterReadinessReady || summary.ReasonCode != DatacenterReasonNone {
		t.Fatalf("unexpected readiness: %#v", summary)
	}
	body, err := json.Marshal(summary)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{root, profile.Name, profile.Username, profile.Password, profile.Notes, "present.txt"} {
		if strings.Contains(string(body), secret) {
			t.Fatalf("roaming summary leaked %q: %s", secret, body)
		}
	}
}

func TestDatacenterStorageReadinessModelsEveryConfiguredBackend(t *testing.T) {
	cases := map[string]string{
		"local": "local", "smb": "smb", "webdav": "webdav", "storagebox": "storagebox", "s3": "s3",
	}
	for profileType, want := range cases {
		if got := datacenterStorageBackend(profileType); got != want {
			t.Errorf("datacenterStorageBackend(%q) = %q, want %q", profileType, got, want)
		}
	}
}

func TestDatacenterStorageReadinessRejectsIncompleteProfileWithoutNetwork(t *testing.T) {
	summary := datacenterStorageReadiness(context.Background(), SharedStorageProfile{ID: "box", Name: "Box", Type: "s3"})
	if summary.Readiness != DatacenterReadinessBlocked || summary.ReasonCode != DatacenterReasonStorageNotConfigured {
		t.Fatalf("unexpected readiness: %#v", summary)
	}
	if summary.LocalEvidence == "" {
		t.Fatal("expected local-only diagnostic evidence")
	}
	if summary.RouteToFix == nil || summary.RouteToFix.Method != "POST" || summary.RouteToFix.Path != "/shared-storage/profiles" {
		t.Fatalf("missing actionable route to fix: %#v", summary.RouteToFix)
	}
	body, err := json.Marshal(summary)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), summary.LocalEvidence) {
		t.Fatalf("local evidence leaked: %s", body)
	}
}

func TestDatacenterStorageReadinessHonorsCallerDeadline(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	started := time.Now()
	summary := datacenterStorageReadiness(ctx, SharedStorageProfile{ID: "slow", Name: "Slow", Type: "smb", Remote: "//192.0.2.1/share"})
	if summary.ReasonCode != DatacenterReasonStorageTimedOut {
		t.Fatalf("unexpected readiness: %#v", summary)
	}
	if elapsed := time.Since(started); elapsed > 250*time.Millisecond {
		t.Fatalf("cancelled readiness took %s", elapsed)
	}
}

func TestDatacenterContractsKeepLocalEvidenceOffWire(t *testing.T) {
	probe := DatacenterCapabilityProbe{Capability: "storage.files", Readiness: DatacenterReadinessBlocked, LocalEvidence: "/private/path token-output"}
	body, err := json.Marshal(probe)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "private") || strings.Contains(string(body), "token-output") {
		t.Fatalf("local evidence leaked: %s", body)
	}
}

func TestDatacenterContractsAreVersionedAndPathFree(t *testing.T) {
	now := time.Unix(1, 0).UTC()
	status := DatacenterStatus{
		SchemaVersion:   DatacenterSchemaVersion,
		ProtocolVersion: DatacenterProtocolVersion,
		GeneratedAt:     now,
		ExpiresAt:       now.Add(time.Minute),
		Readiness:       DatacenterReadinessReady,
		Nodes: []DatacenterNodeCapabilityDigest{{
			SchemaVersion:   DatacenterSchemaVersion,
			ProtocolVersion: DatacenterProtocolVersion,
			GeneratedAt:     now,
			ExpiresAt:       now.Add(time.Minute),
			DeviceID:        "device-public-id",
			Compatibility:   DatacenterCompatibilityCompatible,
			Capabilities: []DatacenterCapabilityProbe{{
				SchemaVersion: DatacenterSchemaVersion,
				Capability:    "storage.files",
				Readiness:     DatacenterReadinessReady,
				MeasuredAt:    now,
				ExpiresAt:     now.Add(time.Minute),
				LocalEvidence: "/root/private token output",
			}},
		}},
	}
	body, err := json.Marshal(status)
	if err != nil {
		t.Fatal(err)
	}
	encoded := string(body)
	for _, forbidden := range []string{"/root/", "private", "token output", "workDir", "prompt", "stdout"} {
		if strings.Contains(encoded, forbidden) {
			t.Fatalf("Datacenter status leaked %q: %s", forbidden, body)
		}
	}
	for _, required := range []string{`"schemaVersion":1`, `"protocolVersion":1`, `"compatibility":"compatible"`} {
		if !strings.Contains(encoded, required) {
			t.Fatalf("Datacenter status omitted %s: %s", required, body)
		}
	}
}
