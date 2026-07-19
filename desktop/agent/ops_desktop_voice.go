package main

// ops_desktop_voice.go — ONE verb that turns a spoken sentence into a desktop
// action and answers out loud. This is the all-surfaces entry point.
//
// WHY A VERB AND NOT AN HTTP ROUTE PER SURFACE:
// Every Yaver surface already speaks `ops` — mobile, tablet, web, CarPlay,
// glass/AR-VR, tvOS, watchOS, Wear, the CLI. Routing desktop control through a
// verb means all of them reach it with the client they already have, and
// dispatchOps (ops.go:268) gives cross-machine proxying for free: a watch can
// drive a Windows box by sending ops(machine="desktop-1", verb="desktop_voice").
// A bespoke /voice/desktop route would have needed per-surface wiring, and the
// native surfaces (tvOS/watchOS/Wear) have their own HTTP stacks that would
// each need porting — the exact "fix it on one surface only" trap CLAUDE.md's
// cross-surface parity rule warns about.
//
// SPEECH-ONLY IS THE POINT.
// This verb never needs a video stream. It reads the OS accessibility tree and
// replies with a sentence, so it works on a watch face, over CarPlay, or on a
// link too thin for video — and it costs zero egress, which is what keeps the
// free relay tier from losing money (desktop_session_policy.go).
//
// The reply is designed to be SPOKEN, not read: short, no coordinates, no JSON.
// Ambiguity comes back as a question ("two matches: Save, Save As — which one?")
// because that is how a person disambiguates when they cannot see a list.

import (
	"encoding/json"
	"fmt"
	"strings"
)

func init() {
	registerOpsVerb(opsVerbSpec{
		Name: "desktop_voice",
		Description: "Speak-to-control a desktop: give a natural sentence ('open Safari', 'click Save', " +
			"'type hello into the search box', 'what's on screen') and get a short spoken-style reply. " +
			"Works WITHOUT a video stream — reads the OS accessibility tree — so it is usable from a watch, " +
			"CarPlay, TV or any thin link. Combine with `machine` to drive another box. Requires --ghost.",
		Schema: ghostJSONSchema(map[string]interface{}{
			"transcript": map[string]interface{}{"type": "string", "description": "What the user said, verbatim."},
			"dryRun":     map[string]interface{}{"type": "boolean", "description": "Resolve and report the intent without acting."},
		}, "transcript"),
		Handler:    desktopVoiceHandler,
		AllowGuest: false,
	})
}

func desktopVoiceHandler(c OpsContext, payload json.RawMessage) OpsResult {
	var p struct {
		Transcript string `json:"transcript"`
		DryRun     bool   `json:"dryRun"`
	}
	if r := ghostUnmarshal(payload, &p); r != nil {
		return *r
	}
	said := strings.TrimSpace(p.Transcript)
	if said == "" {
		return OpsResult{OK: false, Code: "bad_payload", Error: "`transcript` is required"}
	}

	// Reuse the CLI's normalizer so "Hey Yaver, click Save." and "click save"
	// route identically on every surface.
	act, ok := routeVoiceDesktopCommand(normalizeVoiceCommand(said))
	if !ok {
		return OpsResult{
			OK:   false,
			Code: "not_found",
			Initial: map[string]interface{}{
				"heard":  said,
				"spoken": "I didn't understand that. Try: open Safari, click Save, type hello into the search box, or what's on screen.",
			},
			Error: "no desktop intent matched",
		}
	}

	if p.DryRun {
		return OpsResult{OK: true, Initial: map[string]interface{}{
			"heard":   said,
			"verb":    act.Verb,
			"payload": json.RawMessage(act.PayloadJSON),
			"spoken":  act.Speak,
			"dryRun":  true,
		}}
	}

	spec, found := opsRegistry[act.Verb]
	if !found {
		return OpsResult{OK: false, Code: "unknown_verb", Error: "routed to unregistered verb " + act.Verb}
	}
	res := spec.Handler(c, json.RawMessage(act.PayloadJSON))

	// Attach a spoken sentence to whatever came back. The structured result is
	// preserved alongside it so a screen-capable surface can still render it.
	spoken := desktopVoiceSpeech(act, res)
	initial := map[string]interface{}{
		"heard":  said,
		"verb":   act.Verb,
		"spoken": spoken,
		"result": res.Initial,
	}
	return OpsResult{OK: res.OK, Code: res.Code, Error: res.Error, Initial: initial}
}

// desktopVoiceSpeech renders a result as one short sentence meant for TTS.
func desktopVoiceSpeech(act voiceAction, res OpsResult) string {
	if res.OK {
		switch act.Verb {
		case "ghost_elements":
			return summarizeElementsForSpeech(res.Initial)
		case "ghost_click_element":
			if name := clickedNameFrom(res.Initial, "clicked"); name != "" {
				return "clicked " + name
			}
			return "clicked"
		case "ghost_type_into_element":
			if name := clickedNameFrom(res.Initial, "typedInto"); name != "" {
				return "typed into " + name
			}
			return "typed"
		case "ghost_launch_app", "ghost_focus_app":
			return act.Speak
		}
		return "done"
	}

	switch res.Code {
	case "ambiguous":
		// The disambiguation question. Without sight, this IS the UI.
		if names := candidateNames(res.Initial, 5); len(names) > 0 {
			return fmt.Sprintf("%d matches: %s. Which one?", len(names), strings.Join(names, ", "))
		}
		return "several things match that. Can you be more specific?"
	case "not_found":
		return "I couldn't find that on screen."
	case "unauthorized":
		return "desktop control is switched off on that machine."
	case "unsupported":
		return "that machine can't be controlled right now. " + res.Error
	}
	if res.Error != "" {
		return "that failed: " + res.Error
	}
	return "that didn't work."
}

func clickedNameFrom(initial interface{}, key string) string {
	m, ok := initial.(map[string]interface{})
	if !ok {
		return ""
	}
	// The handlers put a ghostElementMatch struct here, not a map, so go
	// through JSON rather than asserting a map type that will never match.
	b, err := json.Marshal(m[key])
	if err != nil {
		return ""
	}
	var e struct {
		Name string `json:"name"`
		Role string `json:"role"`
	}
	if json.Unmarshal(b, &e) != nil {
		return ""
	}
	if strings.TrimSpace(e.Name) != "" {
		return e.Name
	}
	return e.Role
}

func candidateNames(initial interface{}, max int) []string {
	m, ok := initial.(map[string]interface{})
	if !ok {
		return nil
	}
	b, err := json.Marshal(m["matches"])
	if err != nil {
		return nil
	}
	var list []struct {
		Name string `json:"name"`
		Role string `json:"role"`
	}
	if json.Unmarshal(b, &list) != nil {
		return nil
	}
	var out []string
	for _, e := range list {
		n := strings.TrimSpace(e.Name)
		if n == "" {
			n = e.Role
		}
		if n == "" {
			continue
		}
		out = append(out, n)
		if len(out) >= max {
			break
		}
	}
	return out
}
