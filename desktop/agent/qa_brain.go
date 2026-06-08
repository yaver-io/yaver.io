package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/yaver-io/agent/studio"
)

// qa_brain.go — the LLM TestBrain (T1) for the app-test agent. It implements
// studio.TestBrain so it drops into studio's drive+assert loop with no runner
// change (the scaffold already anticipated this seam). NextAction is a cheap
// TEXT turn over the UIAutomator view tree (resource-ids + text + bounds give
// the model precise grounding); Assert is a VISION turn over the screenshot.

// llmBrain drives one scenario toward its goal.
type llmBrain struct {
	model qaModel
	goal  string
}

func newLLMBrain(model qaModel, goal string) *llmBrain {
	return &llmBrain{model: model, goal: goal}
}

const navSystemPrompt = `You are an autonomous QA agent driving a real Android app to accomplish a goal.
You see the current screen's UIAutomator view hierarchy (XML with text, resource-id, content-desc, bounds) and your action history.
Choose the SINGLE next action. Reply with ONLY a JSON object, no prose:
{"verb":"<verb>","args":{...},"done":<bool>,"why":"<short reason>"}
Verbs:
  taptext  args:{"text":"<visible label>"}   tap a button/element by its visible text
  tap      args:{"x":"<int>","y":"<int>"}    tap exact coordinates (use a node's bounds center)
  type     args:{"text":"<text>"}            type into the focused field
  key      args:{"key":"BACK|ENTER|HOME"}    press a hardware/IME key
  back     args:{}                            go back
  wait     args:{}                            wait one tick for the screen to settle
Set "done":true when the goal is achieved OR you are stuck and cannot progress. Prefer taptext over tap when a label exists.`

func (b *llmBrain) NextAction(ctx context.Context, obs studio.Observation) (studio.BrainAction, error) {
	// Prefer the cheap text-over-view-tree path; but redroid's uiautomator is
	// unreliable (verified on magara 2026-06-09), so when the tree is missing,
	// drive by VISION on the screenshot instead of flying blind.
	vision := len(strings.TrimSpace(obs.ViewTree)) < 40 && len(obs.Screenshot) > 0
	user := buildNavPrompt(b.goal, obs, vision)
	var png []byte
	if vision {
		png = obs.Screenshot
	}
	raw, err := b.model.Decide(ctx, navSystemPrompt, user, png)
	if err != nil {
		return studio.BrainAction{}, err
	}
	act, perr := parseBrainAction(raw)
	if perr != nil {
		// A model that didn't return clean JSON shouldn't crash the run — treat
		// an unparseable reply as "done" with the reason, so the flow ends and
		// the report captures what happened.
		return studio.BrainAction{Done: true, Why: "unparseable model reply: " + truncQA(raw, 120)}, nil
	}
	return act, nil
}

func (b *llmBrain) Assert(ctx context.Context, expectation string, screenshot []byte) (studio.AssertVerdict, error) {
	verdict, reason, err := b.model.Judge(ctx, expectation, screenshot)
	if err != nil {
		return studio.AssertVerdict{Expectation: expectation, Pass: false, Reason: err.Error(), Severity: "fail"}, nil
	}
	pass := strings.EqualFold(verdict, "pass")
	severity := "info"
	if !pass {
		if strings.EqualFold(verdict, "fail") {
			severity = "fail"
		} else {
			severity = "warn"
		}
	}
	if reason == "" {
		reason = "model verdict: " + verdict
	}
	return studio.AssertVerdict{Expectation: expectation, Pass: pass, Reason: reason, Severity: severity}, nil
}

func buildNavPrompt(goal string, obs studio.Observation, vision bool) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "GOAL: %s\n\n", goal)
	if len(obs.History) > 0 {
		fmt.Fprintf(&sb, "ACTIONS SO FAR (%d): %s\n\n", len(obs.History), strings.Join(obs.History, " → "))
	}
	if vision {
		sb.WriteString("CURRENT SCREEN: see the attached screenshot (no view hierarchy is available).\n")
		sb.WriteString("Tap by COORDINATES from the image (verb \"tap\" with x,y) — \"taptext\" won't work without a view tree.\n")
	} else {
		fmt.Fprintf(&sb, "CURRENT SCREEN (UIAutomator view hierarchy):\n%s\n", truncQA(obs.ViewTree, 6000))
	}
	sb.WriteString("\nReturn the next action as JSON.")
	return sb.String()
}

// parseBrainAction extracts the JSON object the model returned (tolerating
// surrounding prose / code fences) and maps it to a studio.BrainAction.
func parseBrainAction(raw string) (studio.BrainAction, error) {
	js := qaExtractJSONObject(raw)
	if js == "" {
		return studio.BrainAction{}, fmt.Errorf("no JSON object in reply")
	}
	var p struct {
		Verb string            `json:"verb"`
		Args map[string]string `json:"args"`
		Done bool              `json:"done"`
		Why  string            `json:"why"`
	}
	if err := json.Unmarshal([]byte(js), &p); err != nil {
		return studio.BrainAction{}, err
	}
	if p.Args == nil {
		p.Args = map[string]string{}
	}
	return studio.BrainAction{
		Step: studio.TestStep{Verb: strings.ToLower(strings.TrimSpace(p.Verb)), Args: p.Args},
		Done: p.Done,
		Why:  p.Why,
	}, nil
}

// qaExtractJSONObject returns the first balanced {...} span in s, or "".
func qaExtractJSONObject(s string) string {
	start := strings.IndexByte(s, '{')
	if start < 0 {
		return ""
	}
	depth := 0
	inStr := false
	esc := false
	for i := start; i < len(s); i++ {
		c := s[i]
		switch {
		case esc:
			esc = false
		case c == '\\':
			esc = true
		case c == '"':
			inStr = !inStr
		case inStr:
			// skip
		case c == '{':
			depth++
		case c == '}':
			depth--
			if depth == 0 {
				return s[start : i+1]
			}
		}
	}
	return ""
}
