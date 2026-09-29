package main

import (
	"strings"
	"testing"
)

// TestDesktopMCPToolsShape guards the discoverability contract: the three named
// computer-use tools must be advertised, each with an object inputSchema. If a
// tool silently disappears, the model goes back to not knowing it can drive a
// desktop — the July 2026 finding this lane exists to fix.
func TestDesktopMCPToolsShape(t *testing.T) {
	tools := desktopMCPTools()
	want := map[string]bool{
		"desktop_screenshot": false,
		"desktop_act":        false,
		"desktop_elements":   false,
		"desktop_operator":   false,
	}
	for _, tool := range tools {
		name, _ := tool["name"].(string)
		if _, ok := want[name]; !ok {
			t.Errorf("unexpected tool %q", name)
			continue
		}
		want[name] = true
		if tool["description"] == "" {
			t.Errorf("%s: missing description", name)
		}
		schema, ok := tool["inputSchema"].(map[string]interface{})
		if !ok {
			t.Errorf("%s: missing inputSchema", name)
			continue
		}
		if schema["type"] != "object" {
			t.Errorf("%s: inputSchema.type = %v, want object", name, schema["type"])
		}
	}
	for name, seen := range want {
		if !seen {
			t.Errorf("tool %q was not advertised", name)
		}
	}
}

// TestDesktopFamilyReachesTheWedge proves the allowlist does not silently drop
// the new family — the wedge profile filters by family, and a new family is out
// by default. This is the guard for "the tool exists but no model ever sees it".
func TestDesktopFamilyReachesTheWedge(t *testing.T) {
	if got := mcpToolFamily("desktop_screenshot"); got != "desktop" {
		t.Fatalf("mcpToolFamily = %q, want desktop", got)
	}
	if !wedgeToolFamilies["desktop"] {
		t.Fatal("wedge allowlist is missing the desktop family — the tools would be filtered out")
	}
	if !peripheralToolFamilies["desktop"] {
		t.Fatal("core profile should treat desktop control as peripheral (owner-only), like ghost")
	}
}

// TestDesktopActionToVerb locks the mapping so the named tool and the ops verbs
// cannot drift apart. Pure — no ghost, no server.
func TestDesktopActionToVerb(t *testing.T) {
	cases := []struct {
		name    string
		args    desktopArgs
		want    string
		wantErr bool
	}{
		{"click_element needs query", desktopArgs{Action: "click_element"}, "", true},
		{"click_element", desktopArgs{Action: "click_element", Query: "Save"}, "ghost_click_element", false},
		{"type_into_element needs query", desktopArgs{Action: "type_into_element", Text: "x"}, "", true},
		{"type_into_element", desktopArgs{Action: "type_into_element", Query: "Search", Text: "hello"}, "ghost_type_into_element", false},
		{"click coords", desktopArgs{Action: "click", X: 10, Y: 20}, "ghost_click", false},
		{"double_click", desktopArgs{Action: "double_click", X: 1, Y: 2}, "ghost_click", false},
		{"move", desktopArgs{Action: "move", X: 1, Y: 2}, "ghost_move", false},
		{"scroll", desktopArgs{Action: "scroll", DY: -3}, "ghost_scroll", false},
		{"type needs text", desktopArgs{Action: "type"}, "", true},
		{"type", desktopArgs{Action: "type", Text: "hi"}, "ghost_type", false},
		{"key needs keys", desktopArgs{Action: "key"}, "", true},
		{"key", desktopArgs{Action: "key", Keys: []string{"ctrl", "s"}}, "ghost_key", false},
		{"launch needs app", desktopArgs{Action: "launch"}, "", true},
		{"launch", desktopArgs{Action: "launch", App: "Safari"}, "ghost_launch_app", false},
		{"focus needs app", desktopArgs{Action: "focus"}, "", true},
		{"focus", desktopArgs{Action: "focus", App: "Safari"}, "ghost_focus_app", false},
		{"unknown", desktopArgs{Action: "nope"}, "", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			verb, _, errMsg := desktopActionToVerb(c.args)
			if c.wantErr {
				if errMsg == "" {
					t.Fatalf("expected an error message, got verb %q", verb)
				}
				return
			}
			if errMsg != "" {
				t.Fatalf("unexpected error: %s", errMsg)
			}
			if verb != c.want {
				t.Fatalf("verb = %q, want %q", verb, c.want)
			}
		})
	}
}

// TestDesktopActExpectIsForwarded proves the closed-loop verification knob is
// actually passed to the element verb — a dropped `expect` would silently
// disable verification, which is the whole reason the element verb exists.
func TestDesktopActExpectIsForwarded(t *testing.T) {
	verb, payload, errMsg := desktopActionToVerb(desktopArgs{Action: "click_element", Query: "Save", Expect: "Saved"})
	if errMsg != "" {
		t.Fatal(errMsg)
	}
	if verb != "ghost_click_element" {
		t.Fatalf("verb = %q", verb)
	}
	if got, _ := payload["expect"].(string); !strings.Contains(got, "Saved") {
		t.Fatalf("expect not forwarded: %#v", payload)
	}
}
