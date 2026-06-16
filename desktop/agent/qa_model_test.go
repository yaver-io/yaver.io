package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yaver-io/agent/testkit"
)

func TestPreflightQAModelAcceptsOllamaLatestTag(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/tags" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.Write([]byte(`{"models":[{"name":"llava:latest"}]}`)) //nolint:errcheck
	}))
	defer srv.Close()

	err := preflightQAModel(context.Background(), testkit.VisionConfig{
		Provider: testkit.VisionProviderOllama,
		Model:    "llava",
		Endpoint: srv.URL + "/api/chat",
	})
	if err != nil {
		t.Fatalf("preflight should accept llava:latest for llava: %v", err)
	}
}

func TestPreflightQAModelReportsMissingOllamaModel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"models":[{"name":"qwen2.5vl:7b"}]}`)) //nolint:errcheck
	}))
	defer srv.Close()

	err := preflightQAModel(context.Background(), testkit.VisionConfig{
		Provider: testkit.VisionProviderOllama,
		Model:    "llava",
		Endpoint: srv.URL + "/api/chat",
	})
	if err == nil {
		t.Fatal("preflight should fail when the configured model is missing")
	}
	msg := err.Error()
	if !strings.Contains(msg, `Ollama model "llava" is not installed`) || !strings.Contains(msg, "qwen2.5vl:7b") || !strings.Contains(msg, "ollama pull llava") {
		t.Fatalf("missing-model error is not actionable enough: %s", msg)
	}
}

func TestPreflightQAModelSkipsHostedProviders(t *testing.T) {
	err := preflightQAModel(context.Background(), testkit.VisionConfig{
		Provider: testkit.VisionProviderOpenAI,
		Model:    "gpt-4o-mini",
		Endpoint: "http://127.0.0.1:1/should-not-be-called",
	})
	if err != nil {
		t.Fatalf("hosted providers should not preflight network calls here: %v", err)
	}
}
