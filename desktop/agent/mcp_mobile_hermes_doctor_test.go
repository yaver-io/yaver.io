package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMobileHermesDoctorResolvesMonorepoMobileApp(t *testing.T) {
	root := t.TempDir()
	mobileDir := filepath.Join(root, "apps", "mobile")
	if err := os.MkdirAll(filepath.Join(mobileDir, "node_modules"), 0o755); err != nil {
		t.Fatal(err)
	}
	pkg := `{"name":"demo-mobile","version":"0.0.0","dependencies":{"expo":"^52.0.0","react":"18.3.1","react-native":"0.76.0"}}`
	if err := os.WriteFile(filepath.Join(mobileDir, "package.json"), []byte(pkg), 0o644); err != nil {
		t.Fatalf("write package.json: %v", err)
	}
	if err := os.WriteFile(filepath.Join(mobileDir, "app.json"), []byte(`{"expo":{"name":"demo","slug":"demo"}}`), 0o644); err != nil {
		t.Fatalf("write app.json: %v", err)
	}

	got := mobileHermesDoctor(mobileHermesDoctorInput{Directory: root})
	if got["projectDir"] != mobileDir {
		t.Fatalf("projectDir = %v, want %s", got["projectDir"], mobileDir)
	}
	if got["framework"] != "expo" {
		t.Fatalf("framework = %v, want expo", got["framework"])
	}
	if _, ok := got["projectStatus"].(map[string]interface{}); !ok {
		t.Fatalf("projectStatus missing or wrong type: %#v", got["projectStatus"])
	}
	if got["target"] != "mobile-hermes" {
		t.Fatalf("target = %v, want mobile-hermes", got["target"])
	}
}

func TestMobileHermesDoctorNoProjectSuggestsSelfHostedCreate(t *testing.T) {
	got := mobileHermesDoctor(mobileHermesDoctorInput{Directory: t.TempDir()})
	actions, ok := got["nextActions"].([]map[string]string)
	if !ok {
		t.Fatalf("nextActions missing or wrong type: %#v", got["nextActions"])
	}
	for _, action := range actions {
		if action["tool"] == "project_self_host_create" {
			return
		}
	}
	t.Fatalf("expected project_self_host_create next action, got %#v", actions)
}
