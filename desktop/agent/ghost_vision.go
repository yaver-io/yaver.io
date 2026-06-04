package main

// ghost_vision.go — the vision grounding for the UI ghost. It implements
// ghost.Locator against any OpenAI-compatible /chat/completions endpoint, so it
// works uniformly with OpenRouter, a local Ollama/vLLM server, or any gateway.
// Provider config is supplied by the caller (the Talos driver "drives it") with
// env fallback for a fully-local default — keeping the ghost package itself free
// of any LLM dependency and letting a customer keep everything on-prem.

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/yaver-io/agent/ghost"
)

type visionLocator struct {
	baseURL string
	apiKey  string
	model   string
}

func newVisionLocator(baseURL, apiKey, model string) (*visionLocator, error) {
	if baseURL == "" {
		baseURL = firstNonEmptyStr(os.Getenv("GHOST_VISION_BASE_URL"), os.Getenv("OPENAI_BASE_URL"))
	}
	if apiKey == "" {
		apiKey = firstNonEmptyStr(os.Getenv("GHOST_VISION_API_KEY"), os.Getenv("OPENAI_API_KEY"))
	}
	if model == "" {
		model = firstNonEmptyStr(os.Getenv("GHOST_VISION_MODEL"), "gpt-4o-mini")
	}
	if baseURL == "" {
		return nil, fmt.Errorf("no vision endpoint: pass baseUrl in the payload or set GHOST_VISION_BASE_URL/OPENAI_BASE_URL (e.g. http://localhost:11434/v1 for a local model)")
	}
	return &visionLocator{baseURL: strings.TrimRight(baseURL, "/"), apiKey: apiKey, model: model}, nil
}

func firstNonEmptyStr(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

const ghostVisionSystemPrompt = `You are a precise UI-grounding model operating a desktop GUI like a careful human clerk.
You are given a screenshot and an instruction. Decide the SINGLE next action.
Reply with ONLY a compact JSON object, no prose, no markdown:
{"kind":"click|double_click|type|key|scroll|move|none","x":int,"y":int,"button":"left|right|middle","text":"...","keys":["ctrl","s"],"dx":int,"dy":int,"reason":"short"}
- x,y are pixels from the TOP-LEFT of the screenshot.
- Use "type" with "text" to enter text into the already-focused field.
- Use "key" with "keys" for shortcuts/chords (e.g. ["ctrl","s"], ["enter"]).
- Use "none" if the instruction is already satisfied or impossible.
Return strictly valid JSON.`

func (v *visionLocator) Locate(ctx context.Context, screenshotPNG []byte, instruction string) (ghost.Action, error) {
	b64 := base64.StdEncoding.EncodeToString(screenshotPNG)
	body := map[string]any{
		"model":       v.model,
		"temperature": 0,
		"messages": []any{
			map[string]any{"role": "system", "content": ghostVisionSystemPrompt},
			map[string]any{"role": "user", "content": []any{
				map[string]any{"type": "text", "text": "Instruction: " + instruction + "\nReturn the single next action as JSON."},
				map[string]any{"type": "image_url", "image_url": map[string]any{"url": "data:image/png;base64," + b64}},
			}},
		},
	}
	buf, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, "POST", v.baseURL+"/chat/completions", bytes.NewReader(buf))
	if err != nil {
		return ghost.Action{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	if v.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+v.apiKey)
	}
	client := &http.Client{Timeout: 90 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return ghost.Action{}, fmt.Errorf("vision request failed: %w", err)
	}
	defer resp.Body.Close()
	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return ghost.Action{}, fmt.Errorf("vision decode failed: %w", err)
	}
	if out.Error != nil {
		return ghost.Action{}, fmt.Errorf("vision error: %s", out.Error.Message)
	}
	if len(out.Choices) == 0 {
		return ghost.Action{}, fmt.Errorf("vision returned no choices")
	}
	return parseGhostAction(out.Choices[0].Message.Content)
}

// parseGhostAction tolerates models that wrap JSON in prose or code fences.
func parseGhostAction(s string) (ghost.Action, error) {
	s = strings.TrimSpace(s)
	if i := strings.Index(s, "{"); i >= 0 {
		if j := strings.LastIndex(s, "}"); j >= i {
			s = s[i : j+1]
		}
	}
	var a ghost.Action
	if err := json.Unmarshal([]byte(s), &a); err != nil {
		return ghost.Action{}, fmt.Errorf("vision returned non-JSON action: %q", s)
	}
	if a.Kind == "" {
		a.Kind = ghost.ActionNone
	}
	return a, nil
}
