package main

import (
	"reflect"
	"testing"
)

func TestAssistantWakeWords(t *testing.T) {
	cases := []struct {
		name string
		want []string
	}{
		{"", []string{"hey yaver", "ok yaver", "okay yaver", "yaver", "please"}},
		{"yaver", []string{"hey yaver", "ok yaver", "okay yaver", "yaver", "please"}},
		{"  Sam ", []string{"hey sam", "ok sam", "okay sam", "sam", "please"}},
		{"FEYI", []string{"hey feyi", "ok feyi", "okay feyi", "feyi", "please"}},
		{"kole", []string{"hey kole", "ok kole", "okay kole", "kole", "please"}},
	}
	for _, c := range cases {
		if got := assistantWakeWords(c.name); !reflect.DeepEqual(got, c.want) {
			t.Errorf("assistantWakeWords(%q) = %v, want %v", c.name, got, c.want)
		}
	}
}

// A renamed assistant must strip its own name exactly like "yaver" does,
// and must NOT strip the default "yaver" (otherwise the rename is cosmetic).
func TestNormalizeVoiceCommandWith_CustomName(t *testing.T) {
	sam := assistantWakeWords("sam")
	cases := []struct {
		in   string
		want string
	}{
		{"hey sam, status", "status"},
		{"sam deploy web", "deploy web"},
		{"okay sam cloud status", "cloud status"},
		{"sam", ""},
		{"status", "status"},                       // bare command still works
		{"please status", "status"},                // universal filler still stripped
		{"hey yaver, status", "hey yaver, status"}, // old name no longer a wake word
	}
	for _, c := range cases {
		if got := normalizeVoiceCommandWith(c.in, sam); got != c.want {
			t.Errorf("normalizeVoiceCommandWith(%q, sam) = %q, want %q", c.in, got, c.want)
		}
	}
}

// routeVoiceCommand reads the package-global wake words; swapping them to a
// renamed assistant routes the renamed wake phrase to the right verb.
func TestRouteVoiceCommand_RenamedAssistant(t *testing.T) {
	known := map[string]bool{"status": true, "deploy": true}
	orig := voiceControlWakeWords
	t.Cleanup(func() { voiceControlWakeWords = orig })
	voiceControlWakeWords = assistantWakeWords("feyi")

	if got := routeVoiceCommand("hey feyi, status", known); got.Kind != "ops" || got.Verb != "status" {
		t.Errorf(`routeVoiceCommand("hey feyi, status") = {%s %s}, want {ops status}`, got.Kind, got.Verb)
	}
	if got := routeVoiceCommand("feyi deploy", known); got.Kind != "ops" || got.Verb != "deploy" || !got.Confirm {
		t.Errorf(`routeVoiceCommand("feyi deploy") = {%s %s confirm=%v}, want {ops deploy confirm=true}`, got.Kind, got.Verb, got.Confirm)
	}
}

func TestEffectiveAssistantName(t *testing.T) {
	cases := []struct {
		v    *VoiceConfig
		want string
	}{
		{nil, "yaver"},
		{&VoiceConfig{}, "yaver"},
		{&VoiceConfig{AssistantName: "  "}, "yaver"},
		{&VoiceConfig{AssistantName: "Sam"}, "sam"},
	}
	for _, c := range cases {
		if got := c.v.EffectiveAssistantName(); got != c.want {
			t.Errorf("EffectiveAssistantName(%+v) = %q, want %q", c.v, got, c.want)
		}
	}
}

func TestAssistantNameWarning(t *testing.T) {
	// Distinctive 3+ char names: no warning.
	for _, ok := range []string{"sam", "feyi", "kole", "jarvis", "", "yaver"} {
		if w := assistantNameWarning(ok); w != "" {
			t.Errorf("assistantNameWarning(%q) = %q, want no warning", ok, w)
		}
	}
	// Too short or common: warn.
	for _, bad := range []string{"jo", "x", "yes", "okay", "go"} {
		if assistantNameWarning(bad) == "" {
			t.Errorf("assistantNameWarning(%q) = empty, want a warning", bad)
		}
	}
}
