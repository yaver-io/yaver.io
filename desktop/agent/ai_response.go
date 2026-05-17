package main

// ai_response.go — the AIResponse contract returned by Claude/Codex/
// Opencode when they finish a one-shot turn. Originally part of the
// autodev loop machinery; kept after autodev removal because the
// shared claude_stream.go parser and ai_generator.go (autoideas /
// autoinit) still need to consume the final "result" JSON envelope.

import (
	"encoding/json"
	"strings"
)

// AIResponse is what the runner emits as a JSON object on the final
// "result" event line. parseAIResponse extracts and unmarshals the
// last balanced JSON object out of the runner's stdout.
type AIResponse struct {
	Status       string   `json:"status"` // "done" | "stuck" | "needs_human" | "in_progress"
	Summary      string   `json:"summary"`
	FilesTouched []string `json:"files_touched,omitempty"`
	NextStep     string   `json:"next_step,omitempty"`
	Blockers     []string `json:"blockers,omitempty"`

	// Ideas-mode only: the generated feature list. Used by `yaver
	// autoideas` to surface picker items on mobile/web.
	Ideas       []FeatureIdea `json:"ideas,omitempty"`
	GeneratedAt string        `json:"generated_at,omitempty"`
}

// FeatureIdea is one entry in an Ideas-mode generation.
type FeatureIdea struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Radicalness int    `json:"radicalness"`
	Effort      string `json:"effort"` // "small" | "medium" | "large"
	WhyPersona  string `json:"why_persona,omitempty"`
	WhyNot      string `json:"why_not,omitempty"`
	Prompt      string `json:"prompt,omitempty"`
	Reasoning   string `json:"reasoning,omitempty"`
	Category    string `json:"category,omitempty"`
}

// parseAIResponse extracts the last balanced JSON object from the
// runner's output and unmarshals it into an AIResponse. Returns a
// "stuck" sentinel when no parseable JSON is present so callers can
// surface the raw text to the user.
func parseAIResponse(output string) (*AIResponse, error) {
	output = strings.ReplaceAll(output, "```json", "```")

	start := strings.LastIndex(output, "{")
	for start >= 0 {
		candidate := output[start:]
		for end := len(candidate); end > 0; end-- {
			var resp AIResponse
			if err := json.Unmarshal([]byte(candidate[:end]), &resp); err == nil && resp.Status != "" {
				return &resp, nil
			}
		}
		start = strings.LastIndex(output[:start], "{")
	}
	snippet := output
	if len(snippet) > 400 {
		snippet = snippet[:400] + "..."
	}
	return &AIResponse{
		Status:  "stuck",
		Summary: "AI runner emitted no parseable status JSON: " + snippet,
	}, nil
}
