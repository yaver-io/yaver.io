package main

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"
)

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w
	defer func() {
		os.Stdout = old
	}()

	fn()

	_ = w.Close()
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read stdout: %v", err)
	}
	return string(out)
}

func TestRenderStreamEventYaverSayNoANSIWhenPiped(t *testing.T) {
	got := captureStdout(t, func() {
		renderStreamEvent(map[string]interface{}{
			"type": "yaver_say",
			"text": "Working through the checklist.",
		})
	})
	if strings.Contains(got, "\x1b[") {
		t.Fatalf("expected no ANSI escapes in piped output, got %q", got)
	}
	if !strings.Contains(got, "[yaver] Working through the checklist.") {
		t.Fatalf("missing human-readable yaver line: %q", got)
	}
}

func TestRenderStreamEventRunnerResultHumanReadable(t *testing.T) {
	got := captureStdout(t, func() {
		renderStreamEvent(map[string]interface{}{
			"type":        "runner_result",
			"runner":      "claude",
			"status":      "success",
			"duration_ms": float64(2500),
			"cost_usd":    float64(0.0123),
		})
	})
	wantParts := []string{
		"[claude done",
		"success",
		"2.5s",
		"$0.0123",
	}
	for _, part := range wantParts {
		if !strings.Contains(got, part) {
			t.Fatalf("expected %q in %q", part, got)
		}
	}
}

func TestRenderStreamEventUnknownFallsBackToCompactJSON(t *testing.T) {
	got := captureStdout(t, func() {
		renderStreamEvent(map[string]interface{}{
			"type": "custom_event",
			"foo":  "bar",
		})
	})
	if !strings.Contains(got, "[custom_event]") {
		t.Fatalf("missing custom event tag: %q", got)
	}
	if !strings.Contains(got, `"foo":"bar"`) {
		t.Fatalf("missing compact JSON payload: %q", got)
	}
	if bytes.Count([]byte(got), []byte("\n")) != 1 {
		t.Fatalf("expected single-line fallback output, got %q", got)
	}
}
