package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestIsExpoProject(t *testing.T) {
	t.Run("detects expo project", func(t *testing.T) {
		dir := t.TempDir()
		pkg := `{"dependencies":{"expo":"~52.0.0","react":"18.3.1","react-native":"0.76.0"}}`
		os.WriteFile(filepath.Join(dir, "package.json"), []byte(pkg), 0644)

		if !isExpoProject(dir) {
			t.Error("expected isExpoProject to return true for Expo project")
		}
	})

	t.Run("detects expo in devDependencies", func(t *testing.T) {
		dir := t.TempDir()
		pkg := `{"dependencies":{"react":"18.3.1"},"devDependencies":{"expo":"~52.0.0"}}`
		os.WriteFile(filepath.Join(dir, "package.json"), []byte(pkg), 0644)

		if !isExpoProject(dir) {
			t.Error("expected isExpoProject to return true when expo is in devDependencies")
		}
	})

	t.Run("rejects bare react native project", func(t *testing.T) {
		dir := t.TempDir()
		pkg := `{"dependencies":{"react":"18.3.1","react-native":"0.76.0"}}`
		os.WriteFile(filepath.Join(dir, "package.json"), []byte(pkg), 0644)

		if isExpoProject(dir) {
			t.Error("expected isExpoProject to return false for bare RN project")
		}
	})

	t.Run("rejects missing package.json", func(t *testing.T) {
		dir := t.TempDir()
		if isExpoProject(dir) {
			t.Error("expected isExpoProject to return false when no package.json")
		}
	})
}

func TestDetectPackageManagerExpo(t *testing.T) {
	t.Run("detects yarn", func(t *testing.T) {
		dir := t.TempDir()
		os.WriteFile(filepath.Join(dir, "yarn.lock"), []byte(""), 0644)
		if pm := detectPackageManager(dir); pm != "yarn" {
			t.Errorf("expected yarn, got %s", pm)
		}
	})

	t.Run("detects pnpm", func(t *testing.T) {
		dir := t.TempDir()
		os.WriteFile(filepath.Join(dir, "pnpm-lock.yaml"), []byte(""), 0644)
		if pm := detectPackageManager(dir); pm != "pnpm" {
			t.Errorf("expected pnpm, got %s", pm)
		}
	})

	t.Run("defaults to npm with package.json", func(t *testing.T) {
		dir := t.TempDir()
		os.WriteFile(filepath.Join(dir, "package.json"), []byte("{}"), 0644)
		if pm := detectPackageManager(dir); pm != "npm" {
			t.Errorf("expected npm, got %s", pm)
		}
	})

	t.Run("npm lock file takes priority", func(t *testing.T) {
		dir := t.TempDir()
		os.WriteFile(filepath.Join(dir, "package-lock.json"), []byte("{}"), 0644)
		os.WriteFile(filepath.Join(dir, "yarn.lock"), []byte(""), 0644)
		if pm := detectPackageManager(dir); pm != "npm" {
			t.Errorf("expected npm (package-lock.json found first), got %s", pm)
		}
	})
}

func TestAddPluginToAppJSON(t *testing.T) {
	t.Run("adds plugin to empty plugins array", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "app.json")
		os.WriteFile(path, []byte(`{"expo":{"name":"test","slug":"test","plugins":[]}}`), 0644)

		if err := addPluginToAppJSON(path); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		data, _ := os.ReadFile(path)
		var config map[string]interface{}
		json.Unmarshal(data, &config)

		expo := config["expo"].(map[string]interface{})
		plugins := expo["plugins"].([]interface{})
		if len(plugins) != 1 || plugins[0] != "yaver-feedback-react-native" {
			t.Errorf("expected [yaver-feedback-react-native], got %v", plugins)
		}
	})

	t.Run("adds plugin when no plugins key exists", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "app.json")
		os.WriteFile(path, []byte(`{"expo":{"name":"test","slug":"test"}}`), 0644)

		if err := addPluginToAppJSON(path); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		data, _ := os.ReadFile(path)
		var config map[string]interface{}
		json.Unmarshal(data, &config)

		expo := config["expo"].(map[string]interface{})
		plugins := expo["plugins"].([]interface{})
		if len(plugins) != 1 || plugins[0] != "yaver-feedback-react-native" {
			t.Errorf("expected [yaver-feedback-react-native], got %v", plugins)
		}
	})

	t.Run("idempotent — does not duplicate", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "app.json")
		os.WriteFile(path, []byte(`{"expo":{"name":"test","plugins":["yaver-feedback-react-native"]}}`), 0644)

		if err := addPluginToAppJSON(path); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		data, _ := os.ReadFile(path)
		var config map[string]interface{}
		json.Unmarshal(data, &config)

		expo := config["expo"].(map[string]interface{})
		plugins := expo["plugins"].([]interface{})
		if len(plugins) != 1 {
			t.Errorf("expected 1 plugin (idempotent), got %d: %v", len(plugins), plugins)
		}
	})

	t.Run("preserves existing plugins", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "app.json")
		os.WriteFile(path, []byte(`{"expo":{"plugins":["expo-camera","expo-notifications"]}}`), 0644)

		if err := addPluginToAppJSON(path); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		data, _ := os.ReadFile(path)
		var config map[string]interface{}
		json.Unmarshal(data, &config)

		expo := config["expo"].(map[string]interface{})
		plugins := expo["plugins"].([]interface{})
		if len(plugins) != 3 {
			t.Errorf("expected 3 plugins, got %d: %v", len(plugins), plugins)
		}
		if plugins[2] != "yaver-feedback-react-native" {
			t.Errorf("expected last plugin to be yaver-feedback-react-native, got %v", plugins[2])
		}
	})

	t.Run("handles flat config without expo wrapper", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "app.json")
		os.WriteFile(path, []byte(`{"name":"test","slug":"test","plugins":[]}`), 0644)

		if err := addPluginToAppJSON(path); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		data, _ := os.ReadFile(path)
		var config map[string]interface{}
		json.Unmarshal(data, &config)

		plugins := config["plugins"].([]interface{})
		if len(plugins) != 1 || plugins[0] != "yaver-feedback-react-native" {
			t.Errorf("expected [yaver-feedback-react-native], got %v", plugins)
		}
	})
}
