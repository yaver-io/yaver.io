package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestResolveRunner_PerDeviceWins verifies that when Convex returns both
// a global userSettings.runnerId and a primaryRunnerByDevice entry for the
// caller's deviceID, the per-device row wins. This is the chat-from-web
// case where the user pinned codex on yaver-test-ephemeral but the global
// runnerId is still claude.
func TestResolveRunner_PerDeviceWins(t *testing.T) {
	const deviceID = "2859819c-23cf-444f-ac7c-fc41b81c394e"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"settings": map[string]interface{}{
				"runnerId": "claude",
				"primaryRunnerByDevice": []map[string]interface{}{
					{"deviceId": deviceID, "runnerId": "codex", "model": "gpt-5.4"},
				},
			},
		})
	}))
	defer srv.Close()

	got := resolveRunner(srv.URL, "tok", deviceID)
	if got.RunnerID != "codex" {
		t.Fatalf("expected per-device pref to win (codex), got %q", got.RunnerID)
	}
	if got.Model != "gpt-5.4" {
		t.Fatalf("expected per-device model gpt-5.4, got %q", got.Model)
	}
}

// TestResolveRunner_FallsBackToGlobal verifies the global runnerId is
// honored when no per-device pref exists for this box.
func TestResolveRunner_FallsBackToGlobal(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"settings": map[string]interface{}{
				"runnerId": "codex",
				"primaryRunnerByDevice": []map[string]interface{}{
					{"deviceId": "different-device", "runnerId": "claude"},
				},
			},
		})
	}))
	defer srv.Close()

	got := resolveRunner(srv.URL, "tok", "this-device")
	if got.RunnerID != "codex" {
		t.Fatalf("expected global runnerId codex, got %q", got.RunnerID)
	}
}

// TestResolveRunner_DefaultWhenBothEmpty verifies the auto-detected default
// kicks in when neither a global nor per-device runner is set.
func TestResolveRunner_DefaultWhenBothEmpty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"settings": map[string]interface{}{},
		})
	}))
	defer srv.Close()

	got := resolveRunner(srv.URL, "tok", "any-device")
	if !got.AutoDetected {
		t.Fatalf("expected AutoDetected=true on empty settings, got %+v", got)
	}
	if got.RunnerID != defaultRunner.RunnerID {
		t.Fatalf("expected default runner %q, got %q", defaultRunner.RunnerID, got.RunnerID)
	}
}

// TestResolveRunner_PerDeviceWithoutModelKeepsBuiltinModel verifies that
// when the per-device entry omits a model, we keep the builtin runner's
// configured model rather than blanking it.
func TestResolveRunner_PerDeviceWithoutModelKeepsBuiltinModel(t *testing.T) {
	const deviceID = "dev-id"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"settings": map[string]interface{}{
				"primaryRunnerByDevice": []map[string]interface{}{
					{"deviceId": deviceID, "runnerId": "claude"},
				},
			},
		})
	}))
	defer srv.Close()

	got := resolveRunner(srv.URL, "tok", deviceID)
	if got.RunnerID != "claude" {
		t.Fatalf("expected claude, got %q", got.RunnerID)
	}
	// builtinRunners["claude"].Model is "claude-opus-4-7" (see tasks.go);
	// per-device entry without an explicit model should not blank it.
	if got.Model == "" {
		t.Fatalf("expected builtin model preserved when per-device model empty, got blank")
	}
}

// TestResolveRunner_EmptyDeviceIDIgnoresPerDeviceList guards against a
// regression where a missing/empty deviceID would accidentally match the
// first per-device entry.
func TestResolveRunner_EmptyDeviceIDIgnoresPerDeviceList(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"settings": map[string]interface{}{
				"runnerId": "codex",
				"primaryRunnerByDevice": []map[string]interface{}{
					{"deviceId": "", "runnerId": "aider"},
					{"deviceId": "real", "runnerId": "claude"},
				},
			},
		})
	}))
	defer srv.Close()

	got := resolveRunner(srv.URL, "tok", "")
	if got.RunnerID != "codex" {
		t.Fatalf("empty deviceID should fall back to global runnerId; got %q", got.RunnerID)
	}
}
