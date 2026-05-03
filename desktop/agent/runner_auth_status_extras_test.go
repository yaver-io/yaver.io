package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOpencodeProviderEnvKey_KnownProviders(t *testing.T) {
	cases := map[string]string{
		"anthropic":  "ANTHROPIC_API_KEY",
		"openai":     "OPENAI_API_KEY",
		"glm":        "GLM_API_KEY",
		"zhipu":      "GLM_API_KEY", // alias
		"zai":        "ZAI_API_KEY",
		"openrouter": "OPENROUTER_API_KEY",
		"groq":       "GROQ_API_KEY",
		"mistral":    "MISTRAL_API_KEY",
		"deepseek":   "DEEPSEEK_API_KEY",
		"google":     "GEMINI_API_KEY",
		"gemini":     "GEMINI_API_KEY",
	}
	for provider, want := range cases {
		got, ok := opencodeProviderEnvKey(provider)
		if !ok {
			t.Errorf("opencodeProviderEnvKey(%q) returned ok=false, want true", provider)
			continue
		}
		if got != want {
			t.Errorf("opencodeProviderEnvKey(%q) = %q, want %q", provider, got, want)
		}
	}
}

func TestOpencodeProviderEnvKey_OllamaIsKeyless(t *testing.T) {
	if _, ok := opencodeProviderEnvKey("ollama"); ok {
		t.Errorf("ollama should be keyless — opencodeProviderEnvKey returned ok=true")
	}
}

func TestOpencodeProviderEnvKey_UnknownProvider(t *testing.T) {
	if _, ok := opencodeProviderEnvKey("definitely-not-a-real-provider"); ok {
		t.Errorf("unknown provider must return ok=false")
	}
}

// TestOllamaRunnerRowReady confirms ollamaRunnerStatusRow flips to
// ready+authConfigured when the daemon answers /api/tags. Uses a
// temp httptest server pointed at via OLLAMA_HOST so we don't depend
// on the host machine actually running Ollama.
func TestOllamaRunnerRowReady(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/tags" {
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"models":[]}`))
	}))
	defer srv.Close()
	t.Setenv("OLLAMA_HOST", srv.URL)

	row := ollamaRunnerStatusRow()
	if !row.Ready {
		t.Fatalf("Ready = false, want true (daemon answered)")
	}
	if !row.AuthConfigured {
		t.Fatalf("AuthConfigured = false, want true (Ollama needs no key)")
	}
	if !strings.Contains(row.Detail, "ready") {
		t.Fatalf("Detail = %q, want it to mention ready", row.Detail)
	}
	if row.AuthSource != srv.URL {
		t.Fatalf("AuthSource = %q, want %q", row.AuthSource, srv.URL)
	}
}

// TestOllamaRunnerRowDaemonDown confirms a bound but-not-yaver-aware
// HTTP server still produces a non-ready row when /api/tags 404s
// (catches the case where the user has a different process holding
// :11434 — daemon "up" but isn't ollama).
func TestOllamaRunnerRowDaemonDown(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	t.Setenv("OLLAMA_HOST", srv.URL)

	row := ollamaRunnerStatusRow()
	if row.Ready {
		t.Fatalf("Ready = true on /api/tags 404 — should be false")
	}
}

// TestOpencodeProviderStatusRows_SkippedWhenOpencodeMissing makes sure
// we don't inject phantom provider rows when opencode itself isn't
// installed — the parent row already says "not installed" and a tree
// of dangling provider entries underneath would confuse the user.
func TestOpencodeProviderStatusRows_SkippedWhenOpencodeMissing(t *testing.T) {
	rows := opencodeProviderStatusRows([]runnerAuthStatusRow{
		{ID: "opencode", Installed: false},
	})
	if len(rows) != 0 {
		t.Fatalf("opencode missing → expected zero rows, got %d", len(rows))
	}
}
