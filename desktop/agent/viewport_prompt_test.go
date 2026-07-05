package main

import (
	"net/http"
	"strings"
	"testing"
)

func TestFormatViewportHint_Nil(t *testing.T) {
	if got := formatViewportHint(nil); got != "" {
		t.Errorf("nil viewport should yield empty hint, got %q", got)
	}
}

func TestFormatViewportHint_Empty(t *testing.T) {
	vp := &TaskViewport{}
	if got := formatViewportHint(vp); got != "" {
		t.Errorf("empty viewport should yield empty hint, got %q", got)
	}
}

func TestFormatViewportHint_GlassesHUD(t *testing.T) {
	vp := &TaskViewport{Surface: "glasses-mentra-display"}
	hint := formatViewportHint(vp)
	if !strings.Contains(hint, "monocular") || !strings.Contains(hint, "one-line") {
		t.Errorf("HUD hint missing expected nudges: %q", hint)
	}
	if !strings.HasPrefix(hint, "\n[Display:") {
		t.Errorf("hint missing prefix marker: %q", hint)
	}
}

func TestFormatViewportHint_VoiceReadback(t *testing.T) {
	vp := &TaskViewport{Surface: "mobile-phone", Voice: true, TTSBudget: 200}
	hint := formatViewportHint(vp)
	if !strings.Contains(hint, "voice readback") {
		t.Errorf("voice mode hint missing voice cue: %q", hint)
	}
	if !strings.Contains(hint, "200") {
		t.Errorf("voice mode should mention TTS budget: %q", hint)
	}
}

func TestFormatViewportHint_TTSMode(t *testing.T) {
	vp := &TaskViewport{Surface: "mobile-phone", TTSMode: true}
	hint := formatViewportHint(vp)
	if !strings.Contains(hint, "TTS:") {
		t.Errorf("TTS mode should ask for a TTS:-prefixed summary line: %q", hint)
	}
	if !strings.Contains(hint, "TTS mode is on") {
		t.Errorf("TTS mode hint missing: %q", hint)
	}
	// TTS mode shapes the whole reply, not a headline budget — it must NOT
	// fall through to the readback-budget wording.
	if strings.Contains(hint, "voice readback") {
		t.Errorf("TTS mode must not emit the readback-budget hint: %q", hint)
	}
}

// TTSMode takes precedence over the readback budget even when both flags
// are set: whole-reply shaping wins over the headline budget.
func TestFormatViewportHint_TTSModeBeatsReadback(t *testing.T) {
	vp := &TaskViewport{TTSMode: true, TTSEnabled: true, Voice: true, TTSBudget: 120}
	hint := formatViewportHint(vp)
	if strings.Contains(hint, "voice readback") {
		t.Errorf("TTSMode should suppress readback budget: %q", hint)
	}
	if !strings.Contains(hint, "TTS mode is on") {
		t.Errorf("TTSMode hint missing: %q", hint)
	}
}

func TestFormatViewportHint_VoiceDefaultBudget(t *testing.T) {
	vp := &TaskViewport{Voice: true}
	hint := formatViewportHint(vp)
	if !strings.Contains(hint, "280") {
		t.Errorf("voice default TTSBudget should be 280: %q", hint)
	}
}

func TestFormatViewportHint_MultiPane(t *testing.T) {
	vp := &TaskViewport{Surface: "web-desktop", PaneCount: 4}
	hint := formatViewportHint(vp)
	if !strings.Contains(hint, "4 parallel") {
		t.Errorf("multi-pane hint should mention count: %q", hint)
	}
	if !strings.Contains(hint, "file paths") {
		t.Errorf("multi-pane hint should ask for file paths: %q", hint)
	}
}

func TestFormatViewportHint_VRTmux(t *testing.T) {
	vp := &TaskViewport{Surface: "web-spatial-vr", PaneCount: 3}
	hint := formatViewportHint(vp)
	if !strings.Contains(hint, "VR") || !strings.Contains(hint, "tmux") {
		t.Errorf("VR hint missing tmux frame: %q", hint)
	}
	if !strings.Contains(hint, "3 parallel") {
		t.Errorf("multi-pane on VR should mention 3 sessions: %q", hint)
	}
}

func TestFormatViewportHint_AudioOnly(t *testing.T) {
	vp := &TaskViewport{Surface: "glasses-mentra-live", Voice: true}
	hint := formatViewportHint(vp)
	if !strings.Contains(hint, "audio-only") {
		t.Errorf("audio-only hint missing surface cue: %q", hint)
	}
	if !strings.Contains(hint, "NO code blocks") {
		t.Errorf("audio-only should explicitly forbid code blocks: %q", hint)
	}
}

func TestFormatViewportHint_Smartwatch(t *testing.T) {
	// Smartwatch is the thinnest surface — one sentence, never code. Both the
	// Apple Watch and Wear OS aliases must resolve to the same shape.
	for _, surface := range []string{"wearable-watch", "wearable-wear"} {
		vp := &TaskViewport{Surface: surface, Voice: true}
		hint := formatViewportHint(vp)
		if !strings.Contains(hint, "smartwatch") {
			t.Errorf("%s hint missing surface cue: %q", surface, hint)
		}
		if !strings.Contains(hint, "ONE short sentence") {
			t.Errorf("%s hint should demand a one-sentence answer: %q", surface, hint)
		}
		if !strings.Contains(hint, "no code") {
			t.Errorf("%s hint should forbid code: %q", surface, hint)
		}
	}
}

func TestFormatViewportHint_CarDrivingPolicy(t *testing.T) {
	vp := &TaskViewport{
		Surface:      "car-android-auto",
		Interaction:  "voice",
		VisualBudget: "none",
		RiskPolicy:   "driving",
		Voice:        true,
		TTSBudget:    160,
	}
	hint := formatViewportHint(vp)
	for _, want := range []string{"car surface", "driving-safe", "voice interaction", "no visual budget", "driving policy", "160"} {
		if !strings.Contains(hint, want) {
			t.Errorf("car hint missing %q: %q", want, hint)
		}
	}
	if strings.Contains(hint, "code blocks") {
		t.Errorf("car hint should forbid code/diffs/logs without inviting code-block detail: %q", hint)
	}
}

func TestFormatViewportHint_TVSharedDpad(t *testing.T) {
	vp := &TaskViewport{
		Surface:      "tv-living-room",
		Interaction:  "dpad",
		VisualBudget: "glance",
		RiskPolicy:   "shared-tv",
	}
	hint := formatViewportHint(vp)
	for _, want := range []string{"TV shared-room display", "D-pad interaction", "glance visual budget", "shared TV policy"} {
		if !strings.Contains(hint, want) {
			t.Errorf("TV hint missing %q: %q", want, hint)
		}
	}
}

func TestFormatViewportHint_HeadsetSpatial(t *testing.T) {
	vp := &TaskViewport{
		Surface:      "headset-android-xr",
		Interaction:  "touch",
		VisualBudget: "panel",
		RiskPolicy:   "spatial",
	}
	hint := formatViewportHint(vp)
	for _, want := range []string{"AR/VR headset spatial panel", "touch interaction", "panel visual budget", "spatial policy"} {
		if !strings.Contains(hint, want) {
			t.Errorf("headset hint missing %q: %q", want, hint)
		}
	}
}

func TestFormatViewportHint_MCPStructuredApproval(t *testing.T) {
	vp := &TaskViewport{
		Surface:      "mcp",
		Interaction:  "approval",
		VisualBudget: "full",
		RiskPolicy:   "mcp",
	}
	hint := formatViewportHint(vp)
	for _, want := range []string{"MCP agent caller", "approval interaction", "full visual budget", "MCP policy"} {
		if !strings.Contains(hint, want) {
			t.Errorf("MCP hint missing %q: %q", want, hint)
		}
	}
}

func TestMergeClientVoiceHints_SurfaceMetadataHeaders(t *testing.T) {
	req, err := http.NewRequest(http.MethodPost, "/tasks", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Yaver-Surface", "tv-apple")
	req.Header.Set("X-Yaver-Interaction", "dpad")
	req.Header.Set("X-Yaver-Visual-Budget", "glance")
	req.Header.Set("X-Yaver-Risk-Policy", "shared-tv")
	vp := mergeClientVoiceHints(req, nil, "")
	if vp == nil {
		t.Fatal("expected viewport")
	}
	if vp.Surface != "tv-apple" || vp.Interaction != "dpad" || vp.VisualBudget != "glance" || vp.RiskPolicy != "shared-tv" {
		t.Fatalf("unexpected viewport: %#v", vp)
	}
}

func TestFormatViewportHint_CustomGeometry(t *testing.T) {
	// No known surface, but explicit pane dims — should be passed through
	vp := &TaskViewport{PaneCols: 40, PaneRows: 12}
	hint := formatViewportHint(vp)
	if !strings.Contains(hint, "40") || !strings.Contains(hint, "12") {
		t.Errorf("explicit geometry should appear in hint: %q", hint)
	}
}

func TestFormatViewportHint_UnknownSurface(t *testing.T) {
	vp := &TaskViewport{Surface: "future-headset-x"}
	hint := formatViewportHint(vp)
	if !strings.Contains(hint, "future-headset-x") {
		t.Errorf("unknown surface should pass through to help users debug: %q", hint)
	}
}

func TestFormatViewportHint_OneLine(t *testing.T) {
	// Multiple signals → still one bracketed line, separated by semis
	vp := &TaskViewport{Surface: "mobile-phone", PaneCount: 2, Voice: true}
	hint := formatViewportHint(vp)
	// Count newlines — the prefix \n is intentional; no others should appear
	if strings.Count(hint, "\n") != 1 {
		t.Errorf("hint must be exactly one line (one leading \\n): %q", hint)
	}
	if strings.Count(hint, ";") < 2 {
		t.Errorf("multi-signal hint should separate parts with semis: %q", hint)
	}
}
