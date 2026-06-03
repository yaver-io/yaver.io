package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// vaultWithProvider builds a temp vault carrying a runner-provider config and
// installs it as the runtime vault for the test.
func vaultWithProvider(t *testing.T, baseURL, apiKey string) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	vs, err := NewVaultStore("test-passphrase")
	if err != nil {
		t.Fatalf("NewVaultStore: %v", err)
	}
	if err := vs.Set(VaultEntry{Project: runnerProviderVaultProject, Name: "BASE_URL", Value: baseURL}); err != nil {
		t.Fatalf("set BASE_URL: %v", err)
	}
	if apiKey != "" {
		if err := vs.Set(VaultEntry{Project: runnerProviderVaultProject, Name: "API_KEY", Value: apiKey}); err != nil {
			t.Fatalf("set API_KEY: %v", err)
		}
	}
	setRuntimeVaultStore(vs)
	t.Cleanup(func() { setRuntimeVaultStore(nil) })
}

func TestRunnerProviderPreflight_ReachableListsModels(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		if r.URL.Path != "/v1/models" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data":[{"id":"llama3.1:8b"},{"id":"qwen2.5-coder:7b"}]}`))
	}))
	defer srv.Close()

	vaultWithProvider(t, srv.URL, "local-key")
	res := runnerProviderPreflight("codex", srv.Client())
	if !res.Configured || !res.Reachable {
		t.Fatalf("expected configured+reachable, got %+v", res)
	}
	if res.Protocol != "openai" {
		t.Fatalf("codex protocol = %q", res.Protocol)
	}
	if len(res.Models) != 2 || res.Models[0] != "llama3.1:8b" {
		t.Fatalf("models = %v", res.Models)
	}
	if gotAuth != "Bearer local-key" {
		t.Fatalf("expected bearer auth forwarded, got %q", gotAuth)
	}
	// The result must never carry the key itself.
	if res.KeyPresent != true {
		t.Fatalf("KeyPresent should be true")
	}
}

func TestRunnerProviderPreflight_AuthRejected(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	vaultWithProvider(t, srv.URL, "") // no key configured
	res := runnerProviderPreflight("claude", srv.Client())
	if !res.Reachable {
		t.Fatalf("401 should still count as reachable: %+v", res)
	}
	if res.Status != http.StatusUnauthorized || res.Error == "" {
		t.Fatalf("expected an auth-needed error, got %+v", res)
	}
}

func TestRunnerProviderPreflight_NotConfigured(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	vs, err := NewVaultStore("p")
	if err != nil {
		t.Fatalf("NewVaultStore: %v", err)
	}
	setRuntimeVaultStore(vs)
	t.Cleanup(func() { setRuntimeVaultStore(nil) })

	res := runnerProviderPreflight("claude", nil)
	if res.Configured || res.Reachable {
		t.Fatalf("unconfigured provider must report not-configured/not-reachable: %+v", res)
	}
}

func TestProviderModelsProbeURL(t *testing.T) {
	cases := map[string]string{
		"http://h:8000":     "http://h:8000/v1/models",
		"http://h:8000/":    "http://h:8000/v1/models",
		"http://h:8000/v1":  "http://h:8000/v1/models",
		"http://h:8000/v1/": "http://h:8000/v1/models",
	}
	for in, want := range cases {
		if got := providerModelsProbeURL(in); got != want {
			t.Fatalf("probeURL(%q) = %q, want %q", in, got, want)
		}
	}
}
