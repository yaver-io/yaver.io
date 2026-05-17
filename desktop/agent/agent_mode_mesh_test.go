package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestAutoIdeasBuildArgsIncludesRunner(t *testing.T) {
	args := autoIdeasBuildArgs("demo", AutoIdeasStart{
		Runner:     "codex",
		Engine:     "claude",
		Output:     "ideas.md",
		MaxBatches: 2,
	})
	got := strings.Join(args, " ")
	for _, want := range []string{"demo", "--runner codex", "--engine claude", "--output ideas.md", "--max-batches 2"} {
		if !strings.Contains(got, want) {
			t.Fatalf("autoIdeasBuildArgs() = %q, want substring %q", got, want)
		}
	}
}

func TestWaitForRemoteAutoIdeasReturnsFirstTitle(t *testing.T) {
	var calls int
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer token-123" {
			t.Fatalf("Authorization header = %q", got)
		}
		if got := r.URL.Path; got != "/autoideas/file" {
			t.Fatalf("path = %q", got)
		}
		if got := r.URL.Query().Get("work_dir"); got != "/tmp/project" {
			t.Fatalf("work_dir = %q", got)
		}
		if got := r.URL.Query().Get("output"); got != "ideas.md" {
			t.Fatalf("output = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		calls++
		if calls < 2 {
			_, _ = w.Write([]byte(`{"ok":true,"items":[],"raw":""}`))
			return
		}
		_, _ = w.Write([]byte(`{"ok":true,"items":[{"title":"Ship mesh autoideas"}],"raw":"- [ ] Ship mesh autoideas"}`))
	}))
	defer server.Close()

	client := server.Client()
	origTransport := http.DefaultTransport
	http.DefaultTransport = client.Transport
	defer func() { http.DefaultTransport = origTransport }()

	ctx, cancel := context.WithTimeout(context.Background(), 7*time.Second)
	defer cancel()

	got, err := waitForRemoteAutoIdeas(ctx, server.URL, "token-123", "/tmp/project", "ideas.md")
	if err != nil {
		t.Fatalf("waitForRemoteAutoIdeas() error = %v", err)
	}
	if got != "Ship mesh autoideas" {
		t.Fatalf("waitForRemoteAutoIdeas() = %q, want %q", got, "Ship mesh autoideas")
	}
}
