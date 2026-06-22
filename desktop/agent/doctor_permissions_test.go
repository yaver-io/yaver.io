package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestManifestGapsDetectsMissing(t *testing.T) {
	dir := t.TempDir()
	// RN project that uses camera + location but declares neither.
	os.WriteFile(filepath.Join(dir, "package.json"),
		[]byte(`{"dependencies":{"expo-camera":"*","expo-location":"*"}}`), 0644)
	os.WriteFile(filepath.Join(dir, "app.json"), []byte(`{"expo":{"name":"X"}}`), 0644)

	iosM, andM, readable := manifestGaps(dir)
	if !readable {
		t.Fatal("app.json present ⇒ should be readable")
	}
	if !strSliceHas(iosM, "NSCameraUsageDescription") {
		t.Errorf("expected NSCameraUsageDescription gap, got %v", iosM)
	}
	if !strSliceHas(andM, "android.permission.CAMERA") {
		t.Errorf("expected CAMERA gap, got %v", andM)
	}
}

func TestManifestGapsComplete(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"dependencies":{"expo-camera":"*"}}`), 0644)
	os.WriteFile(filepath.Join(dir, "app.json"), []byte(
		`{"expo":{"ios":{"infoPlist":{"NSCameraUsageDescription":"cam"}},"android":{"permissions":["android.permission.CAMERA"]}}}`), 0644)

	iosM, andM, readable := manifestGaps(dir)
	if !readable {
		t.Fatal("readable")
	}
	if len(iosM) != 0 || len(andM) != 0 {
		t.Errorf("fully declared ⇒ no gaps, got ios=%v android=%v", iosM, andM)
	}
}

func TestManifestGapsSkipsNonReadable(t *testing.T) {
	// No app.json (app.config.js-only) ⇒ skip, never false-block.
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"dependencies":{"expo-camera":"*"}}`), 0644)
	if _, _, readable := manifestGaps(dir); readable {
		t.Error("no app.json ⇒ must report not-readable (skip)")
	}
	// No package.json ⇒ not an RN project.
	if _, _, readable := manifestGaps(t.TempDir()); readable {
		t.Error("no package.json ⇒ not-readable")
	}
}

func TestApplyManifestGapsBlocksTestFlightOniOSCrash(t *testing.T) {
	// Missing iOS usage string on TestFlight = guaranteed launch crash ⇒ block.
	r := &BuildDoctorReport{Stack: "react-native-expo", OK: true}
	applyManifestGaps(r, "testflight", "app", []string{"NSCameraUsageDescription"}, nil, true)
	if r.OK {
		t.Error("missing iOS usage string must FAIL the TestFlight doctor")
	}
	if r.PermissionsComplete == nil || *r.PermissionsComplete {
		t.Error("PermissionsComplete should be false")
	}

	// Missing Android perms only = warning, not a block.
	r2 := &BuildDoctorReport{Stack: "react-native-expo", OK: true}
	applyManifestGaps(r2, "playstore", "app", nil, []string{"android.permission.CAMERA"}, true)
	if !r2.OK {
		t.Error("missing Android perms should warn, not block")
	}
	if len(r2.MissingDeclarations) == 0 {
		t.Error("missing declarations should be recorded")
	}

	// Complete ⇒ ok stays true, PermissionsComplete true.
	r3 := &BuildDoctorReport{Stack: "react-native-expo", OK: true}
	applyManifestGaps(r3, "testflight", "app", nil, nil, true)
	if r3.PermissionsComplete == nil || !*r3.PermissionsComplete {
		t.Error("no gaps ⇒ PermissionsComplete true")
	}
}
