package main

import (
	"context"
	"testing"
)

// TestNewVisionLocatorEnvOverride proves an explicit env provider wins and is
// normalized, so grounding can be pointed at any OpenAI-compatible endpoint.
func TestNewVisionLocatorEnvOverride(t *testing.T) {
	t.Setenv("GHOST_VISION_BASE_URL", "https://example.test/v1/")
	t.Setenv("GHOST_VISION_API_KEY", "k")
	t.Setenv("GHOST_VISION_MODEL", "some-vision-model")
	loc, err := newVisionLocator("", "", "")
	if err != nil {
		t.Fatalf("newVisionLocator: %v", err)
	}
	if loc.baseURL != "https://example.test/v1" {
		t.Fatalf("baseURL = %q", loc.baseURL)
	}
	if loc.model != "some-vision-model" {
		t.Fatalf("model = %q", loc.model)
	}
	if loc.apiKey != "k" {
		t.Fatalf("apiKey = %q", loc.apiKey)
	}
}

// TestNewVisionLocatorExplicitWins proves the payload beats env.
func TestNewVisionLocatorExplicitWins(t *testing.T) {
	t.Setenv("GHOST_VISION_BASE_URL", "https://env.test/v1")
	loc, err := newVisionLocator("https://explicit.test/v1", "pk", "m")
	if err != nil {
		t.Fatalf("newVisionLocator: %v", err)
	}
	if loc.baseURL != "https://explicit.test/v1" || loc.apiKey != "pk" || loc.model != "m" {
		t.Fatalf("explicit args not honored: %+v", loc)
	}
}

// TestNewVisionLocatorAlwaysResolves proves it never returns the old nil/err on
// a normal box: it falls back to the local Ollama default so grounding has a
// target (the request will name a connection failure if Ollama is absent, which
// is the honest error).
func TestNewVisionLocatorAlwaysResolves(t *testing.T) {
	// Clear the env providers this test cares about.
	t.Setenv("GHOST_VISION_BASE_URL", "")
	t.Setenv("OPENAI_BASE_URL", "")
	loc, err := newVisionLocator("", "", "")
	if err != nil {
		t.Fatalf("newVisionLocator: %v", err)
	}
	if loc.baseURL == "" || loc.model == "" {
		t.Fatalf("expected a resolved endpoint+model, got %+v", loc)
	}
	_ = context.Background()
}
