package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func payloadOf(t *testing.T, act voiceAction) map[string]interface{} {
	t.Helper()
	if act.PayloadJSON == "" {
		return map[string]interface{}{}
	}
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(act.PayloadJSON), &m); err != nil {
		t.Fatalf("payload %q is not valid JSON: %v", act.PayloadJSON, err)
	}
	return m
}

func TestVoiceDesktopClick(t *testing.T) {
	act, ok := routeVoiceDesktopCommand("click save")
	if !ok {
		t.Fatal("\"click save\" should route")
	}
	if act.Verb != "ghost_click_element" {
		t.Errorf("verb = %q", act.Verb)
	}
	if got := payloadOf(t, act)["query"]; got != "save" {
		t.Errorf("query = %v, want \"save\"", got)
	}
}

func TestVoiceDesktopClickVariants(t *testing.T) {
	for phrase, want := range map[string]bool{
		"double click save": true,
		"right click save":  true,
		"click on save":     true,
		"tap save":          true,
	} {
		act, ok := routeVoiceDesktopCommand(phrase)
		if ok != want {
			t.Errorf("%q routed=%v", phrase, ok)
			continue
		}
		if act.Verb != "ghost_click_element" {
			t.Errorf("%q → verb %q", phrase, act.Verb)
		}
	}
	// The longer prefixes must win over their substrings.
	dbl, _ := routeVoiceDesktopCommand("double click save")
	if payloadOf(t, dbl)["double"] != true {
		t.Error("\"double click\" lost to the \"click\" prefix")
	}
	rgt, _ := routeVoiceDesktopCommand("right click save")
	if payloadOf(t, rgt)["button"] != "right" {
		t.Error("\"right click\" lost to the \"click\" prefix")
	}
	if payloadOf(t, rgt)["query"] != "save" {
		t.Errorf("right-click query = %v", payloadOf(t, rgt)["query"])
	}
}

func TestVoiceDesktopTypeInto(t *testing.T) {
	act, ok := routeVoiceDesktopCommand("type hello world into the search box")
	if !ok {
		t.Fatal("should route")
	}
	if act.Verb != "ghost_type_into_element" {
		t.Fatalf("verb = %q", act.Verb)
	}
	p := payloadOf(t, act)
	if p["text"] != "hello world" {
		t.Errorf("text = %v", p["text"])
	}
	// The leading article must be stripped from the target.
	if p["query"] != "search box" {
		t.Errorf("query = %v, want \"search box\"", p["query"])
	}
}

// "into" inside the typed text must not split early — the LAST separator wins.
func TestVoiceDesktopTypeIntoAmbiguousText(t *testing.T) {
	act, ok := routeVoiceDesktopCommand("type log into file into the command box")
	if !ok {
		t.Fatal("should route")
	}
	p := payloadOf(t, act)
	if p["text"] != "log into file" {
		t.Errorf("text = %v, want \"log into file\"", p["text"])
	}
	if p["query"] != "command box" {
		t.Errorf("query = %v, want \"command box\"", p["query"])
	}
}

// No target named → type into whatever has focus, using the plain verb.
func TestVoiceDesktopTypeNoTarget(t *testing.T) {
	act, ok := routeVoiceDesktopCommand("type hello")
	if !ok {
		t.Fatal("should route")
	}
	if act.Verb != "ghost_type" {
		t.Errorf("verb = %q, want ghost_type", act.Verb)
	}
	if payloadOf(t, act)["text"] != "hello" {
		t.Errorf("text = %v", payloadOf(t, act)["text"])
	}
}

func TestVoiceDesktopReplaceSetsClear(t *testing.T) {
	act, ok := routeVoiceDesktopCommand("replace 42 into the quantity field")
	if !ok {
		t.Fatal("should route")
	}
	p := payloadOf(t, act)
	if p["clear"] != true {
		t.Error("replace must set clear=true")
	}
	if p["text"] != "42" {
		t.Errorf("text = %v", p["text"])
	}
}

func TestVoiceDesktopLaunchAndFocus(t *testing.T) {
	launch, ok := routeVoiceDesktopCommand("open safari")
	if !ok || launch.Verb != "ghost_launch_app" {
		t.Fatalf("open → ok=%v verb=%q", ok, launch.Verb)
	}
	if payloadOf(t, launch)["app"] != "safari" {
		t.Errorf("app = %v", payloadOf(t, launch)["app"])
	}

	focus, ok := routeVoiceDesktopCommand("switch to autocad")
	if !ok || focus.Verb != "ghost_focus_app" {
		t.Fatalf("switch to → ok=%v verb=%q", ok, focus.Verb)
	}
	if payloadOf(t, focus)["app"] != "autocad" {
		t.Errorf("app = %v", payloadOf(t, focus)["app"])
	}
}

// Speech-only: these must ask for a spoken answer, or the mode does not work.
func TestVoiceDesktopScreenReadsSpeakResult(t *testing.T) {
	for _, phrase := range []string{
		"what's on screen", "read the screen", "what can i click", "list buttons",
	} {
		act, ok := routeVoiceDesktopCommand(phrase)
		if !ok {
			t.Errorf("%q should route", phrase)
			continue
		}
		if act.Verb != "ghost_elements" {
			t.Errorf("%q → verb %q", phrase, act.Verb)
		}
		if !act.SpeakResult {
			t.Errorf("%q must set SpeakResult — speech-only depends on it", phrase)
		}
		// The verb rejects an empty query AND empty role, so a role must be set.
		if payloadOf(t, act)["role"] == nil {
			t.Errorf("%q sent neither query nor role — the verb will reject it", phrase)
		}
	}
}

func TestVoiceDesktopChordParsing(t *testing.T) {
	cases := map[string][]string{
		"control s":           {"ctrl", "s"},
		"command shift p":     {"cmd", "shift", "p"},
		"ctrl+s":              {"ctrl", "s"},
		"control and shift n": {"ctrl", "shift", "n"},
	}
	for in, want := range cases {
		got := parseSpokenChord(in)
		if strings.Join(got, "+") != strings.Join(want, "+") {
			t.Errorf("parseSpokenChord(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestVoiceDesktopPressRoutes(t *testing.T) {
	act, ok := routeVoiceDesktopCommand("press keys control s")
	if !ok || act.Verb != "ghost_key" {
		t.Fatalf("ok=%v verb=%q", ok, act.Verb)
	}
	keys, _ := payloadOf(t, act)["keys"].([]interface{})
	if len(keys) != 2 || keys[0] != "ctrl" || keys[1] != "s" {
		t.Errorf("keys = %v", keys)
	}
}

// Unmatched phrases must fall through so the existing bare-verb routing still
// works — this layer only ever ADDS reachable phrases.
func TestVoiceDesktopFallsThrough(t *testing.T) {
	for _, phrase := range []string{"", "status", "git push", "deploy", "click", "open"} {
		if _, ok := routeVoiceDesktopCommand(phrase); ok {
			t.Errorf("%q should NOT be captured by the desktop router", phrase)
		}
	}
}

// The desktop router must not shadow the pre-existing verb routing.
func TestRouteVoiceCommandStillMatchesBareVerbs(t *testing.T) {
	known := map[string]bool{"status": true, "git_push": true, "ghost_click_element": true}
	if act := routeVoiceCommand("status", known); act.Kind != "ops" || act.Verb != "status" {
		t.Errorf("bare verb broke: %+v", act)
	}
	if act := routeVoiceCommand("git push", known); act.Verb != "git_push" {
		t.Errorf("multiword verb broke: %+v", act)
	}
	// And "run" still wins its prefix.
	if act := routeVoiceCommand("run ls -la", known); act.Verb != "run" || act.Cmd != "ls -la" {
		t.Errorf("run prefix broke: %+v", act)
	}
	// Desktop phrases now route where they previously fell to "none".
	if act := routeVoiceCommand("click save", known); act.Verb != "ghost_click_element" {
		t.Errorf("desktop phrase did not route: %+v", act)
	}
}

// Payloads are marshalled, never concatenated: a quote in spoken text must not
// corrupt the request.
func TestVoiceDesktopPayloadEscaping(t *testing.T) {
	act, ok := routeVoiceDesktopCommand(`click say "hi"`)
	if !ok {
		t.Fatal("should route")
	}
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(act.PayloadJSON), &m); err != nil {
		t.Fatalf("payload corrupted by quotes: %v (%s)", err, act.PayloadJSON)
	}
	if m["query"] != `say "hi"` {
		t.Errorf("query = %v", m["query"])
	}
}

func TestSummarizeElementsForSpeech(t *testing.T) {
	initial := map[string]interface{}{
		"matches": []interface{}{
			map[string]interface{}{"name": "Save"},
			map[string]interface{}{"name": "Cancel"},
		},
	}
	if got := summarizeElementsForSpeech(initial); got != "Save, Cancel" {
		t.Errorf("got %q", got)
	}
	if got := summarizeElementsForSpeech(map[string]interface{}{"matches": []interface{}{}}); got != "nothing found" {
		t.Errorf("empty → %q", got)
	}
}

// Speech has no scrollback — a long list must be truncated with a count, not
// read out in full.
func TestSummarizeElementsForSpeechTruncates(t *testing.T) {
	var many []interface{}
	for i := 0; i < 20; i++ {
		many = append(many, map[string]interface{}{"name": "btn"})
	}
	got := summarizeElementsForSpeech(map[string]interface{}{"matches": many})
	if !strings.Contains(got, "more") {
		t.Errorf("long list not truncated: %q", got)
	}
	if strings.Count(got, "btn") > 6 {
		t.Errorf("read out too many names: %q", got)
	}
}

// The ambiguity question is the whole speech-only disambiguation UX.
func TestDesktopVoiceSpeechAmbiguity(t *testing.T) {
	res := OpsResult{
		OK:   false,
		Code: "ambiguous",
		Initial: map[string]interface{}{
			"matches": []interface{}{
				map[string]interface{}{"name": "Save"},
				map[string]interface{}{"name": "Save As…"},
			},
		},
	}
	got := desktopVoiceSpeech(voiceAction{Verb: "ghost_click_element"}, res)
	if !strings.Contains(got, "Save") || !strings.Contains(got, "Which one") {
		t.Errorf("ambiguity must be spoken as a question with candidates, got %q", got)
	}
}

func TestDesktopVoiceSpeechFailures(t *testing.T) {
	act := voiceAction{Verb: "ghost_click_element"}
	if got := desktopVoiceSpeech(act, OpsResult{Code: "not_found"}); !strings.Contains(got, "couldn't find") {
		t.Errorf("not_found → %q", got)
	}
	if got := desktopVoiceSpeech(act, OpsResult{Code: "unauthorized"}); !strings.Contains(got, "switched off") {
		t.Errorf("unauthorized → %q", got)
	}
}

func TestDesktopVoiceVerbRegistered(t *testing.T) {
	if _, ok := opsRegistry["desktop_voice"]; !ok {
		t.Fatal("desktop_voice is not registered — no surface can reach it")
	}
	if _, ok := opsRegistry["ghost_launch_app"]; !ok {
		t.Fatal("ghost_launch_app is not registered")
	}
}
