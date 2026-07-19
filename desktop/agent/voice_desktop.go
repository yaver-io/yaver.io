package main

// voice_desktop.go — spoken phrases → desktop-control ops verbs.
//
// This is the last link in "say it → it happens". Before it, voice reached
// exactly three places (TaskManager, nullary ops verbs, Hermes launch) and none
// of them could drive a GUI: routeVoiceCommand only ever matched a bare verb
// NAME, and runVoiceOpsVerb sent no arguments. "click Save" was unroutable.
//
// SPEECH-ONLY IS A FIRST-CLASS MODE, NOT A DEGRADED ONE.
// Several intents here (`what's on screen`, `find …`) answer out loud from the
// accessibility tree, so the user can operate a remote PC with NO video stream
// at all. That matters for three independent reasons:
//   - it works on a watch, in a car, or over a link too thin for video;
//   - the tree is ground truth, so it is more reliable than describing a JPEG
//     to a vision model;
//   - it costs no egress, which is what makes the free relay tier viable
//     (see desktop_session_policy.go).
//
// Every parser here is PURE — no I/O — matching routeVoiceCommand's contract so
// the whole intent surface stays unit-testable without a mic or a desktop.

import (
	"encoding/json"
	"strconv"
	"strings"
)

// voiceDesktopIntent is one spoken-phrase → verb rule.
type voiceDesktopIntent struct {
	// prefixes that introduce this intent, longest-first at match time.
	prefixes []string
	build    func(arg string) (voiceAction, bool)
}

// voiceDesktopPayload marshals a payload map. Marshalling a map[string]any of strings
// and bools cannot fail, but we never hand-build JSON: an app or element name
// is arbitrary user speech and string concatenation would let a quote or
// backslash corrupt the request.
func voiceDesktopPayload(m map[string]interface{}) string {
	b, err := json.Marshal(m)
	if err != nil {
		return "{}"
	}
	return string(b)
}

// splitTypeInto splits `<text> into <field>` → (text, field, true).
// Uses the LAST " into " so text containing the word still parses:
// "type log into file into the search box" → text="log into file".
func splitTypeInto(arg string) (text, field string, ok bool) {
	const sep = " into "
	i := strings.LastIndex(arg, sep)
	if i < 0 {
		return "", "", false
	}
	text = strings.TrimSpace(arg[:i])
	field = strings.TrimSpace(arg[i+len(sep):])
	// Speech often yields a leading article on the target.
	for _, a := range []string{"the ", "a ", "an "} {
		field = strings.TrimPrefix(field, a)
	}
	if text == "" || field == "" {
		return "", "", false
	}
	return text, field, true
}

// voiceDesktopIntents is ordered: the first prefix that matches wins, and
// longer prefixes are listed before their shorter substrings ("double click"
// before "click", "switch to" before "type") so the specific rule fires first.
var voiceDesktopIntents = []voiceDesktopIntent{
	{
		prefixes: []string{"double click ", "double-click "},
		build: func(arg string) (voiceAction, bool) {
			return voiceAction{
				Kind: "ops", Verb: "ghost_click_element",
				PayloadJSON: voiceDesktopPayload(map[string]interface{}{"query": arg, "double": true}),
				Speak:       "double clicking " + arg,
			}, true
		},
	},
	{
		prefixes: []string{"right click ", "right-click "},
		build: func(arg string) (voiceAction, bool) {
			return voiceAction{
				Kind: "ops", Verb: "ghost_click_element",
				PayloadJSON: voiceDesktopPayload(map[string]interface{}{"query": arg, "button": "right"}),
				Speak:       "right clicking " + arg,
			}, true
		},
	},
	{
		prefixes: []string{"click on ", "click ", "press button ", "tap "},
		build: func(arg string) (voiceAction, bool) {
			return voiceAction{
				Kind: "ops", Verb: "ghost_click_element",
				PayloadJSON: voiceDesktopPayload(map[string]interface{}{"query": arg}),
				Speak:       "clicking " + arg,
				// The result is spoken because ambiguity REFUSES with a
				// candidate list — in speech-only mode that list is the only
				// way the user learns why nothing happened and what to say next.
				SpeakResult: true,
			}, true
		},
	},
	{
		prefixes: []string{"type "},
		build: func(arg string) (voiceAction, bool) {
			if text, field, ok := splitTypeInto(arg); ok {
				return voiceAction{
					Kind: "ops", Verb: "ghost_type_into_element",
					PayloadJSON: voiceDesktopPayload(map[string]interface{}{"query": field, "text": text}),
					Speak:       "typing into " + field,
					SpeakResult: true,
				}, true
			}
			// No target named — type into whatever already has focus.
			return voiceAction{
				Kind: "ops", Verb: "ghost_type",
				PayloadJSON: voiceDesktopPayload(map[string]interface{}{"text": arg}),
				Speak:       "typing",
			}, true
		},
	},
	{
		prefixes: []string{"replace ", "clear and type "},
		build: func(arg string) (voiceAction, bool) {
			text, field, ok := splitTypeInto(arg)
			if !ok {
				return voiceAction{}, false
			}
			return voiceAction{
				Kind: "ops", Verb: "ghost_type_into_element",
				PayloadJSON: voiceDesktopPayload(map[string]interface{}{"query": field, "text": text, "clear": true}),
				Speak:       "replacing " + field,
				SpeakResult: true,
			}, true
		},
	},
	{
		prefixes: []string{"switch to ", "focus ", "go to app ", "bring up "},
		build: func(arg string) (voiceAction, bool) {
			return voiceAction{
				Kind: "ops", Verb: "ghost_focus_app",
				PayloadJSON: voiceDesktopPayload(map[string]interface{}{"app": arg}),
				Speak:       "switching to " + arg,
			}, true
		},
	},
	{
		prefixes: []string{"open app ", "launch ", "open "},
		build: func(arg string) (voiceAction, bool) {
			return voiceAction{
				Kind: "ops", Verb: "ghost_launch_app",
				PayloadJSON: voiceDesktopPayload(map[string]interface{}{"app": arg}),
				Speak:       "opening " + arg,
			}, true
		},
	},
	{
		prefixes: []string{"press keys ", "press key ", "hit "},
		build: func(arg string) (voiceAction, bool) {
			keys := parseSpokenChord(arg)
			if len(keys) == 0 {
				return voiceAction{}, false
			}
			return voiceAction{
				Kind: "ops", Verb: "ghost_key",
				PayloadJSON: voiceDesktopPayload(map[string]interface{}{"keys": keys}),
				Speak:       "pressing " + strings.Join(keys, " "),
			}, true
		},
	},
	{
		prefixes: []string{"find ", "where is ", "is there a "},
		build: func(arg string) (voiceAction, bool) {
			return voiceAction{
				Kind: "ops", Verb: "ghost_elements",
				PayloadJSON: voiceDesktopPayload(map[string]interface{}{"query": arg}),
				Speak:       "looking for " + arg,
				SpeakResult: true,
			}, true
		},
	},
}

// voiceDesktopScreenReads are whole-phrase (not prefix) intents that describe
// the screen out loud. These are the core of speech-only operation.
var voiceDesktopScreenReads = map[string]string{
	"what's on screen":     "",
	"whats on screen":      "",
	"what is on screen":    "",
	"read the screen":      "",
	"describe the screen":  "",
	"what can i click":     "button",
	"what can i press":     "button",
	"list buttons":         "button",
	"what are my options":  "button",
	"what fields are here": "text",
}

// parseSpokenChord turns "control s", "command shift p", "ctrl+s" into
// ["ctrl","s"]. Spoken modifier words are normalized to the names ghost's
// per-OS KeyCombo expects.
func parseSpokenChord(arg string) []string {
	arg = strings.ReplaceAll(arg, "+", " ")
	arg = strings.ReplaceAll(arg, " plus ", " ")
	var keys []string
	for _, f := range strings.Fields(arg) {
		switch f {
		case "control", "ctrl":
			f = "ctrl"
		case "command", "cmd", "apple":
			f = "cmd"
		case "option", "alt":
			f = "alt"
		case "shift":
			f = "shift"
		case "escape", "esc":
			f = "esc"
		case "return":
			f = "enter"
		case "and", "then", "the", "key", "keys":
			continue
		}
		keys = append(keys, f)
	}
	return keys
}

// routeVoiceDesktopCommand maps a NORMALIZED transcript to a desktop action.
// Returns ok=false when nothing matches so the caller falls through to the
// existing bare-verb routing — this layer only ever ADDS reachable phrases.
func routeVoiceDesktopCommand(t string) (voiceAction, bool) {
	if t == "" {
		return voiceAction{}, false
	}
	if role, ok := voiceDesktopScreenReads[t]; ok {
		payload := map[string]interface{}{}
		if role != "" {
			payload["role"] = role
		} else {
			// No role filter: ask for everything actionable. An empty query
			// AND empty role is rejected by the verb, so name the broadest
			// useful role rather than sending nothing.
			payload["role"] = "button"
		}
		return voiceAction{
			Kind: "ops", Verb: "ghost_elements",
			PayloadJSON: voiceDesktopPayload(payload),
			Speak:       "reading the screen",
			SpeakResult: true,
		}, true
	}
	for _, intent := range voiceDesktopIntents {
		for _, p := range intent.prefixes {
			if !strings.HasPrefix(t, p) {
				continue
			}
			arg := strings.TrimSpace(t[len(p):])
			if arg == "" {
				continue
			}
			if act, ok := intent.build(arg); ok {
				return act, true
			}
		}
	}
	return voiceAction{}, false
}

// summarizeElementsForSpeech turns a ghost_elements result into one short
// spoken sentence. Speech has no scrollback, so this caps the list and says
// how many were omitted rather than reading forty names at someone.
func summarizeElementsForSpeech(initial interface{}) string {
	m, ok := initial.(map[string]interface{})
	if !ok {
		return ""
	}
	raw, ok := m["matches"].([]interface{})
	if !ok || len(raw) == 0 {
		return "nothing found"
	}
	const maxSpoken = 6
	var names []string
	for _, r := range raw {
		e, ok := r.(map[string]interface{})
		if !ok {
			continue
		}
		name, _ := e["name"].(string)
		if strings.TrimSpace(name) == "" {
			name, _ = e["role"].(string)
		}
		if strings.TrimSpace(name) == "" {
			continue
		}
		names = append(names, name)
		if len(names) >= maxSpoken {
			break
		}
	}
	if len(names) == 0 {
		return "nothing found"
	}
	out := strings.Join(names, ", ")
	if rest := len(raw) - len(names); rest > 0 {
		out += ", and " + strconv.Itoa(rest) + " more"
	}
	return out
}
