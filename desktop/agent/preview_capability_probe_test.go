package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestProbeRefusesIOSOnLinuxWorkspace(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	r := ProbePreviewCapability(ctx, PreviewIOSSimulator, "")
	if r.CanRun {
		t.Fatal("iOS simulator must never report runnable on a workspace probe")
	}
	if !strings.Contains(r.Remedy, "Mac host") {
		t.Fatalf("remedy must name the Mac host: %q", r.Remedy)
	}
}

func TestProbeReportsObservedNotConfigured(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	r := ProbePreviewCapability(ctx, PreviewChromeWebRTC, "")
	if len(r.Probes) == 0 {
		t.Fatal("chrome-webrtc must probe something")
	}
	for _, p := range r.Probes {
		// Every probe must say what it OBSERVED — an empty detail is a probe
		// that checked a flag rather than running anything.
		if strings.TrimSpace(p.Detail) == "" {
			t.Fatalf("probe %q reported no detail", p.Name)
		}
		if p.TookMs < 0 {
			t.Fatalf("probe %q has negative duration", p.Name)
		}
	}
	// A blocking failure must surface as a remedy, never a bare false.
	if !r.CanRun && strings.TrimSpace(r.Remedy) == "" {
		t.Fatal("a failing report must carry a remedy")
	}
}

func TestReportIsRenderableOnEverySurface(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	r := ProbePreviewCapability(ctx, PreviewChromeWebRTC, "")
	b, err := r.JSON()
	if err != nil {
		t.Fatalf("report must serialise for client surfaces: %v", err)
	}
	var back map[string]any
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatalf("report must round-trip: %v", err)
	}
	for _, k := range []string{"strategy", "canRun", "summary", "probes"} {
		if _, ok := back[k]; !ok {
			t.Fatalf("payload missing %q — every surface renders the same struct", k)
		}
	}
	// Watch-length: if the summary does not fit a watch it is too long for anyone.
	if len(r.Summary) > 60 {
		t.Fatalf("summary must fit a watch face, got %d chars: %q", len(r.Summary), r.Summary)
	}
}
