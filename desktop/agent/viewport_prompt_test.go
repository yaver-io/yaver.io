package main

import (
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
