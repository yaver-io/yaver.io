package main

// ops_desktop_operator.go — the agent-side operator loop.
//
// This is the "PC usage tool" capability: one verb that takes a goal in natural
// language and drives the machine toward it, using the model's vision to decide
// each next action. It rides the surfaces that already exist — the result is a
// plain step trace that the chat/task output and the video-recording lanes
// already know how to render. No new UI, and no separate product: same agent,
// same `--ghost` gate, same `ops`/MCP plumbing as everything else.
//
// It is deliberately thin: the loop logic lives in ghost.RunLoop (pure,
// unit-tested) and the model call lives in newVisionLocator (ghost_vision.go,
// any OpenAI-compatible endpoint, on-prem Ollama included). This file only
// resolves the provider, bounds the run, and shapes the trace.

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/yaver-io/agent/ghost"
)

func init() {
	registerOpsVerb(opsVerbSpec{
		Name: "desktop_operator",
		Description: "Run a bounded, vision-driven operator loop on the desktop: capture the screen, ask the " +
			"configured vision model for the single next action toward `goal`, execute it, and repeat until the " +
			"goal is met or the step/time budget is spent. Returns a step-by-step trace (what was tried, why, " +
			"and where it stopped). Requires --ghost and a vision provider (GHOST_VISION_* / OPENAI_* / a local " +
			"Ollama, or explicit baseUrl/apiKey/model). Chain `machine` to drive another owned box.",
		Schema: ghostJSONSchema(map[string]interface{}{
			"goal":       map[string]interface{}{"type": "string", "description": "What to accomplish, in plain language, e.g. \"open Safari and search for the weather\"."},
			"maxSteps":   map[string]interface{}{"type": "integer", "description": "Iteration cap (default 8)."},
			"display":    map[string]interface{}{"type": "integer", "description": "Display index to capture (default 0 = primary)."},
			"timeoutSec": map[string]interface{}{"type": "integer", "description": "Total wall-clock budget in seconds (default 300)."},
			"baseUrl":    map[string]interface{}{"type": "string", "description": "Vision endpoint override (OpenAI-compatible). Empty = resolve from env / local Ollama."},
			"apiKey":     map[string]interface{}{"type": "string", "description": "Vision provider API key override."},
			"model":      map[string]interface{}{"type": "string", "description": "Vision model override, e.g. gpt-4o-mini or llama3.2-vision."},
		}, "goal"),
		Handler:        desktopOperatorHandler,
		AllowCompanion: false,
	})
}

func desktopOperatorHandler(c OpsContext, payload json.RawMessage) OpsResult {
	eng, deny := ghostEngineForOps(c)
	if deny != nil {
		return *deny
	}
	var p struct {
		Goal       string `json:"goal"`
		MaxSteps   int    `json:"maxSteps"`
		Display    int    `json:"display"`
		TimeoutSec int    `json:"timeoutSec"`
		BaseURL    string `json:"baseUrl"`
		APIKey     string `json:"apiKey"`
		Model      string `json:"model"`
	}
	if r := ghostUnmarshal(payload, &p); r != nil {
		return *r
	}
	goal := strings.TrimSpace(p.Goal)
	if goal == "" {
		return OpsResult{OK: false, Code: "bad_payload", Error: "goal is required"}
	}

	loc, err := newVisionLocator(p.BaseURL, p.APIKey, p.Model)
	if err != nil {
		return OpsResult{
			OK:    false,
			Code:  "no_vision_provider",
			Error: "no vision model configured for grounding: " + err.Error() + " — set GHOST_VISION_BASE_URL/API_KEY/MODEL or OPENAI_*, or pass baseUrl/apiKey/model",
		}
	}

	opts := ghost.LoopOptions{
		Display:              p.Display,
		MaxSteps:             p.MaxSteps,
		NoProgressStopsAfter: 3,
	}
	// Grounded completion + change detection from the accessibility tree, so the
	// loop does not merely trust the model's "I'm done". Both are best-effort:
	// on a platform with no tree (or a headless box) TreeText errors and the
	// loop falls back to the locator's own "none" signal.
	opts.Observe = eng.TreeText
	opts.Verify = loc.VerifyGoal
	if p.TimeoutSec > 0 {
		opts.TotalTimeout = time.Duration(p.TimeoutSec) * time.Second
	}

	ctx := c.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	res, loopErr := eng.RunLoop(ctx, loc, goal, opts)

	out := map[string]interface{}{
		"goal":          res.Goal,
		"completed":     res.Completed,
		"verified":      res.Verified,
		"verifyDetail":  res.VerifyDetail,
		"stoppedReason": res.StoppedReason,
		"stepCount":     len(res.Steps),
		"steps":         res.Steps,
	}
	if loopErr != nil {
		return OpsResult{OK: false, Code: "operator_failed", Error: loopErr.Error(), Initial: out}
	}
	return OpsResult{OK: true, Initial: out}
}
