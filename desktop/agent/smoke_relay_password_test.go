package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestRelayPasswordSmoke_PassesWhenRelayValidatorAccepts(t *testing.T) {
	var deletes atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/auth/signup", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"token": "tok-abc"})
	})
	mux.HandleFunc("/settings", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"settings": map[string]interface{}{"relayPassword": "good-pw"},
		})
	})
	mux.HandleFunc("/relay/validate", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"userId": "u-1"})
	})
	mux.HandleFunc("/config", func(w http.ResponseWriter, r *http.Request) {
		// No relay URLs — skip the live-relay probe.
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"relayServers": []interface{}{}})
	})
	mux.HandleFunc("/auth/delete-account", func(w http.ResponseWriter, r *http.Request) {
		deletes.Add(1)
		w.WriteHeader(http.StatusOK)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	if err := runRelayPasswordSmoke(ctx, &http.Client{Timeout: 2 * time.Second}, srv.URL); err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	if deletes.Load() != 1 {
		t.Fatalf("expected throwaway user deletion, got %d calls", deletes.Load())
	}
}

func TestRelayPasswordSmoke_RepairWhenSettingsEmptyThenRetry(t *testing.T) {
	var settingsCalls, repairCalls atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/auth/signup", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"token": "tok-abc"})
	})
	mux.HandleFunc("/settings", func(w http.ResponseWriter, r *http.Request) {
		n := settingsCalls.Add(1)
		if n == 1 {
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"settings": map[string]interface{}{"relayPassword": ""},
			})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"settings": map[string]interface{}{"relayPassword": "new-pw"},
		})
	})
	mux.HandleFunc("/settings/repair-relay", func(w http.ResponseWriter, r *http.Request) {
		repairCalls.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"ok": true, "repaired": true})
	})
	mux.HandleFunc("/relay/validate", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"userId": "u-1"})
	})
	mux.HandleFunc("/config", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"relayServers": []interface{}{}})
	})
	mux.HandleFunc("/auth/delete-account", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	srv := httptest.NewServer(mux)
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := runRelayPasswordSmoke(ctx, &http.Client{Timeout: 2 * time.Second}, srv.URL); err != nil {
		t.Fatalf("expected success after repair, got %v", err)
	}
	if repairCalls.Load() != 1 {
		t.Fatalf("expected exactly 1 repair-relay call, got %d", repairCalls.Load())
	}
	if settingsCalls.Load() != 2 {
		t.Fatalf("expected settings to be re-fetched after repair, got %d calls", settingsCalls.Load())
	}
}

func TestRelayPasswordSmoke_FailsWhenLiveRelay401s(t *testing.T) {
	// Separate the live-relay host so we can 401 on it without
	// polluting the Convex surface.
	relayMux := http.NewServeMux()
	relayMux.HandleFunc("/d/smoke-nonexistent/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"invalid relay password"}`))
	})
	relay := httptest.NewServer(relayMux)
	defer relay.Close()

	mux := http.NewServeMux()
	mux.HandleFunc("/auth/signup", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"token": "tok-abc"})
	})
	mux.HandleFunc("/settings", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"settings": map[string]interface{}{"relayPassword": "good-pw"},
		})
	})
	mux.HandleFunc("/relay/validate", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"userId": "u-1"})
	})
	mux.HandleFunc("/config", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"relayServers": []map[string]string{{"httpUrl": relay.URL}},
		})
	})
	mux.HandleFunc("/auth/delete-account", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	convex := httptest.NewServer(mux)
	defer convex.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	err := runRelayPasswordSmoke(ctx, &http.Client{Timeout: 2 * time.Second}, convex.URL)
	if err == nil || !strings.Contains(err.Error(), "live relay 401") {
		t.Fatalf("expected live-relay 401 regression, got %v", err)
	}
}
