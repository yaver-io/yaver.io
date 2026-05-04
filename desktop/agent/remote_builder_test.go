package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestBuilderRegistry_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("YAVER_HOME", dir)

	reg, err := LoadBuilders()
	if err != nil {
		t.Fatalf("initial load: %v", err)
	}
	if len(reg.Builders) != 0 {
		t.Errorf("fresh registry should be empty, got %d", len(reg.Builders))
	}

	// First add becomes the default automatically — single-Mac
	// users shouldn't have to flip a separate switch.
	if err := reg.AddBuilder(BuilderEntry{
		Alias:     "mac-rack-1",
		URL:       "http://10.0.0.5:18080",
		Token:     "secret",
		Platforms: []string{"ios"},
	}); err != nil {
		t.Fatalf("add: %v", err)
	}
	if reg.Default != "mac-rack-1" {
		t.Errorf("first builder should become default, got %q", reg.Default)
	}
	if err := SaveBuilders(reg); err != nil {
		t.Fatalf("save: %v", err)
	}

	// Verify mode 0600 on the file — token sits in there, must not
	// be world-readable.
	st, err := os.Stat(filepath.Join(dir, "builders.json"))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Errorf("file mode = %v, want 0600 (token sensitivity)", st.Mode().Perm())
	}

	loaded, err := LoadBuilders()
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if loaded.Default != "mac-rack-1" {
		t.Errorf("default after reload = %q", loaded.Default)
	}
	got := loaded.Builders["mac-rack-1"]
	if got.URL != "http://10.0.0.5:18080" {
		t.Errorf("URL = %q", got.URL)
	}
	if got.Token != "secret" {
		t.Errorf("token round-trip failed: got %q", got.Token)
	}
}

func TestBuilderRegistry_DefaultsToFirstAddedFallsToAlphaOnForget(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("YAVER_HOME", dir)
	reg, _ := LoadBuilders()
	_ = reg.AddBuilder(BuilderEntry{Alias: "zebra", URL: "http://z"})
	_ = reg.AddBuilder(BuilderEntry{Alias: "alpha", URL: "http://a"})
	_ = reg.AddBuilder(BuilderEntry{Alias: "mike", URL: "http://m"})
	if reg.Default != "zebra" {
		t.Errorf("first add should be default, got %q", reg.Default)
	}
	// Removing the default should pick alphabetically-first
	// remaining alias rather than leaving Default as "" — keeps a
	// confused user from hitting "no default builder" on the next
	// session.
	if !reg.Forget("zebra") {
		t.Fatal("forget(zebra) should succeed")
	}
	if reg.Default != "alpha" {
		t.Errorf("after forgetting default, fallback = %q, want alphabetic-first \"alpha\"", reg.Default)
	}
}

func TestBuilderRegistry_RejectsEmptyFields(t *testing.T) {
	reg := &BuilderRegistry{Builders: map[string]BuilderEntry{}}
	if err := reg.AddBuilder(BuilderEntry{URL: "http://x"}); err == nil {
		t.Error("empty alias should be rejected")
	}
	if err := reg.AddBuilder(BuilderEntry{Alias: "x"}); err == nil {
		t.Error("empty url should be rejected")
	}
}

func TestBuilderRegistry_SetDefault_RejectsUnknown(t *testing.T) {
	reg := &BuilderRegistry{Builders: map[string]BuilderEntry{}}
	if err := reg.SetDefault("does-not-exist"); err == nil {
		t.Error("setting default to unknown alias should error")
	}
}

func TestPingBuilder_HappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify the agent forwarded the bearer token. Without this,
		// a future change that swaps to a different auth scheme
		// would silently break paired builders.
		if got := r.Header.Get("Authorization"); got != "Bearer testtoken" {
			t.Errorf("auth header = %q, want Bearer testtoken", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"version":   "1.99.131",
			"hostname":  "macmini",
			"isBuilder": true,
			"platforms": []string{"ios", "macos"},
		})
	}))
	defer srv.Close()
	info, err := PingBuilder(srv.Client(), BuilderEntry{
		URL: srv.URL, Token: "testtoken",
	})
	if err != nil {
		t.Fatalf("ping: %v", err)
	}
	if !info.OK {
		t.Error("info.OK should be true on 200")
	}
	if !info.IsBuilder {
		t.Error("info.IsBuilder should round-trip")
	}
	if len(info.Platforms) != 2 || info.Platforms[0] != "ios" {
		t.Errorf("platforms = %v", info.Platforms)
	}
}

func TestPingBuilder_NonOKStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusForbidden)
	}))
	defer srv.Close()
	_, err := PingBuilder(srv.Client(), BuilderEntry{URL: srv.URL})
	if err == nil {
		t.Fatal("non-200 should error")
	}
}

func TestPingBuilder_BadJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("{not valid json"))
	}))
	defer srv.Close()
	_, err := PingBuilder(srv.Client(), BuilderEntry{URL: srv.URL})
	if err == nil {
		t.Error("malformed JSON should error rather than silently succeed")
	}
}

func TestSetBuilderPlatforms_Normalizes(t *testing.T) {
	SetBuilderPlatforms([]string{"  iOS ", "macOS", "ios", ""})
	got := builderPlatforms()
	if len(got) != 2 {
		t.Fatalf("got %v, want 2 unique entries (deduped + lowercased)", got)
	}
	if got[0] != "ios" || got[1] != "macos" {
		t.Errorf("got %v, want [ios macos]", got)
	}
	// Reset for other tests in the suite.
	SetBuilderPlatforms(nil)
}

func TestRemoteRuntimeCapabilities_IncludesPairedBuilders(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("YAVER_HOME", dir)
	// Pre-seed two builders, only one of which serves iOS.
	reg, _ := LoadBuilders()
	_ = reg.AddBuilder(BuilderEntry{
		Alias: "mac-1", URL: "http://10.0.0.5", Platforms: []string{"ios"},
	})
	_ = reg.AddBuilder(BuilderEntry{
		Alias: "mac-2-android-only", URL: "http://10.0.0.6", Platforms: []string{"android"},
	})
	if err := SaveBuilders(reg); err != nil {
		t.Fatalf("save: %v", err)
	}

	caps := remoteRuntimeCapabilitiesForProject("/tmp/swift-app", "swift")
	if len(caps.RemoteBuilders) != 1 {
		t.Fatalf("RemoteBuilders count = %d, want 1 (android-only filtered out)", len(caps.RemoteBuilders))
	}
	got := caps.RemoteBuilders[0]
	if got.Alias != "mac-1" {
		t.Errorf("alias = %q", got.Alias)
	}
	if !got.Default {
		t.Error("default should be true on the only iOS builder")
	}
	if got.URL == "" {
		t.Error("URL should be populated for the dashboard")
	}
}
