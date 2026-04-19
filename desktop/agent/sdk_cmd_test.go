package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBuildFeedbackInstallPlan(t *testing.T) {
	t.Run("auto-detects expo", func(t *testing.T) {
		dir := t.TempDir()
		os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"dependencies":{"expo":"~52.0.0","react-native":"0.76.0"}}`), 0644)
		os.WriteFile(filepath.Join(dir, "package-lock.json"), []byte(`{}`), 0644)
		os.WriteFile(filepath.Join(dir, "app.json"), []byte(`{"expo":{"name":"demo"}}`), 0644)

		plan, err := buildSDKInstallPlan(dir, "feedback", "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if plan.Platform != "expo" {
			t.Fatalf("expected expo, got %s", plan.Platform)
		}
		if plan.PackageName != "yaver-feedback-react-native" {
			t.Fatalf("expected RN feedback package, got %s", plan.PackageName)
		}
		if plan.Command != "npm" {
			t.Fatalf("expected npm command, got %s", plan.Command)
		}
	})

	t.Run("auto-detects flutter", func(t *testing.T) {
		dir := t.TempDir()
		os.WriteFile(filepath.Join(dir, "pubspec.yaml"), []byte("name: demo\n"), 0644)

		plan, err := buildSDKInstallPlan(dir, "feedback", "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if plan.Platform != "flutter" {
			t.Fatalf("expected flutter, got %s", plan.Platform)
		}
		if plan.Command != "flutter" {
			t.Fatalf("expected flutter command, got %s", plan.Command)
		}
	})
}

func TestBuildCoreInstallPlan(t *testing.T) {
	t.Run("auto-detects javascript", func(t *testing.T) {
		dir := t.TempDir()
		os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"name":"demo"}`), 0644)
		os.WriteFile(filepath.Join(dir, "package-lock.json"), []byte(`{}`), 0644)

		plan, err := buildSDKInstallPlan(dir, "core", "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if plan.Platform != "js" {
			t.Fatalf("expected js, got %s", plan.Platform)
		}
		if plan.PackageName != "yaver-sdk" {
			t.Fatalf("expected yaver-sdk, got %s", plan.PackageName)
		}
	})

	t.Run("prefers uv for python projects with uv lock", func(t *testing.T) {
		dir := t.TempDir()
		os.WriteFile(filepath.Join(dir, "pyproject.toml"), []byte("[project]\nname='demo'\n"), 0644)
		os.WriteFile(filepath.Join(dir, "uv.lock"), []byte("version = 1"), 0644)

		plan, err := buildSDKInstallPlan(dir, "core", "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if plan.Platform != "python" {
			t.Fatalf("expected python, got %s", plan.Platform)
		}
		if plan.Command != "uv" {
			t.Fatalf("expected uv command, got %s", plan.Command)
		}
	})
}
