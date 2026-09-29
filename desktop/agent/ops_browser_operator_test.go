package main

import "testing"

func TestParseBrowserAction(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		want    string
		wantErr bool
	}{
		{"plain json", `{"action":"navigate","url":"https://x.test"}`, "navigate", false},
		{"code fenced", "```json\n{\"action\":\"click\",\"selector\":\"#go\"}\n```", "click", false},
		{"prose wrapped", "Sure: {\"action\":\"done\",\"reason\":\"there\"} — done.", "done", false},
		{"case normalized", `{"action":"CLICK","selector":"a"}`, "click", false},
		{"navigate without url", `{"action":"navigate"}`, "navigate", true},
		{"click without selector", `{"action":"click"}`, "click", true},
		{"type without selector", `{"action":"type","text":"hi"}`, "type", true},
		{"wait without selector", `{"action":"wait"}`, "wait", true},
		{"non-json", "I cannot help with that", "", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a, err := parseBrowserAction(c.in)
			if c.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %+v", a)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if a.Action != c.want {
				t.Fatalf("action = %q, want %q", a.Action, c.want)
			}
		})
	}
}

func TestSameBrowserAction(t *testing.T) {
	a := browserOperatorAction{Action: "click", Selector: "#s"}
	if !sameBrowserAction(a, browserOperatorAction{Action: "click", Selector: "#s"}) {
		t.Fatal("identical actions should match")
	}
	if sameBrowserAction(a, browserOperatorAction{Action: "click", Selector: "#other"}) {
		t.Fatal("different selectors must not match")
	}
	if sameBrowserAction(browserOperatorAction{Action: "scroll", DX: 1}, browserOperatorAction{Action: "scroll", DX: 2}) {
		t.Fatal("different scroll deltas must not match")
	}
}
