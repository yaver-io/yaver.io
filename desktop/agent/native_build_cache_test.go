package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNativeBuildCacheDecisionForConsumer(t *testing.T) {
	contract := nativeBuildConsumerContract{
		Platform:        "ios",
		AppVersion:      "1.18.22",
		AppBuild:        "260",
		SDKVersion:      "1.0.0",
		HermesBCVersion: 96,
	}
	key := nativeBuildConsumerKey(contract)

	t.Run("valid when consumer matches", func(t *testing.T) {
		decision := nativeBuildCacheDecisionForConsumer(nativeBuildStatus{
			ConsumerKey:   key,
			ConsumerLabel: nativeBuildConsumerLabel(contract),
		}, contract)
		if !decision.Valid {
			t.Fatalf("expected valid cache decision, got %#v", decision)
		}
		if !strings.Contains(decision.Message, "matches this Yaver build") {
			t.Fatalf("unexpected message: %q", decision.Message)
		}
	})

	t.Run("invalid when prior consumer differs", func(t *testing.T) {
		decision := nativeBuildCacheDecisionForConsumer(nativeBuildStatus{
			ConsumerKey:   "platform=ios|version=1.18.21|build=259|sdk=1.0.0|bc=96",
			ConsumerLabel: "1.18.21 (259), SDK 1.0.0, BC96 on ios",
		}, contract)
		if decision.Valid {
			t.Fatalf("expected invalid cache decision, got %#v", decision)
		}
		if !strings.Contains(decision.Message, "Clearing bundle cache") {
			t.Fatalf("unexpected message: %q", decision.Message)
		}
	})
}

func TestPrepareNativeBuildOutputClearsStaleAssetsWhenConsumerChanges(t *testing.T) {
	workDir := t.TempDir()
	buildDir := filepath.Join(workDir, ".yaver-build")
	assetsDir := filepath.Join(buildDir, "assets")
	if err := os.MkdirAll(assetsDir, 0o755); err != nil {
		t.Fatalf("mkdir assets dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(assetsDir, "stale.txt"), []byte("stale"), 0o644); err != nil {
		t.Fatalf("write stale asset: %v", err)
	}
	writeNativeBuildStatus(workDir, nativeBuildStatus{
		State:         "ready",
		Platform:      "ios",
		ConsumerKey:   "platform=ios|version=1.18.21|build=259|sdk=1.0.0|bc=96",
		ConsumerLabel: "1.18.21 (259), SDK 1.0.0, BC96 on ios",
	})

	decision, err := prepareNativeBuildOutput(buildDir, workDir, nativeBuildConsumerContract{
		Platform:        "ios",
		AppVersion:      "1.18.22",
		AppBuild:        "260",
		SDKVersion:      "1.0.0",
		HermesBCVersion: 96,
	})
	if err != nil {
		t.Fatalf("prepareNativeBuildOutput: %v", err)
	}
	if decision.Valid {
		t.Fatalf("expected invalid/stale cache decision, got %#v", decision)
	}
	if _, err := os.Stat(filepath.Join(assetsDir, "stale.txt")); !os.IsNotExist(err) {
		t.Fatalf("stale asset should be removed, stat err=%v", err)
	}
}
