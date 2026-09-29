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
	"image"
	_ "image/png"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/yaver-io/agent/ghost"
	"github.com/yaver-io/agent/testkit"
)

type visionLocator struct {
	baseURL string
	apiKey  string
	model   string
}

// localOllamaV1 is Yaver's on-box local model server (OpenAI-compatible).
const localOllamaV1 = "http://localhost:11434/v1"

// newVisionLocator resolves the grounding model through Yaver's underlying AI
// infra, in priority order:
//  1. explicit payload (baseUrl/apiKey/model) — the Talos driver picks.
//  2. GHOST_VISION_* env.
//  3. OPENAI_* env — this is what Yaver's runner/llm-settings layer injects for
//     the user's chosen provider (Claude Code / Codex / OpenRouter / gateway),
//     so the ghost reuses the same configured AI with no extra creds.
//  4. local Ollama (Yaver's on-box model server) as the on-prem default.
//
// This keeps the ghost provider-agnostic and lets a customer run fully local.
func newVisionLocator(baseURL, apiKey, model string) (*visionLocator, error) {
	if baseURL == "" {
		baseURL = firstNonEmptyStr(os.Getenv("GHOST_VISION_BASE_URL"), os.Getenv("OPENAI_BASE_URL"))
	}
	if apiKey == "" {
		apiKey = firstNonEmptyStr(os.Getenv("GHOST_VISION_API_KEY"), os.Getenv("OPENAI_API_KEY"))
	}
	if model == "" {
		model = firstNonEmptyStr(os.Getenv("GHOST_VISION_MODEL"), os.Getenv("OPENAI_MODEL"))
	}
	// Shared vision config is the SECOND resolution seam, so a provider set once
	// (yaver set vision-key, or the web/mobile settings surfaces) enables BOTH
	// vision_analyze_image and the ghost's grounding — the audit's "two config
	// paths for the model that can see" gap. Only OpenAI-compatible providers
	// are usable by the OpenAI-shaped chat call below; Anthropic's /v1/messages
	// is intentionally skipped rather than silently mis-called.
	if baseURL == "" {
		if vc, ok := resolvedVisionConfig("", model); ok &&
			(vc.Provider == testkit.VisionProviderOpenAI || vc.Provider == testkit.VisionProviderMistral) {
			baseURL = strings.TrimSuffix(vc.Endpoint, "/chat/completions")
			if apiKey == "" {
				apiKey = vc.APIKey
			}
			if model == "" {
				model = vc.Model
			}
		}
	}
	// On-prem default: Yaver's local Ollama, so a fully local box still grounds.
	if baseURL == "" {
		baseURL = localOllamaV1
	}
	if model == "" {
		if strings.Contains(baseURL, "11434") {
			model = "llama3.2-vision"
		} else {
			model = "gpt-4o-mini"
		}
	}
	if baseURL == "" {
		return nil, fmt.Errorf("no vision endpoint resolved")
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
- The screen may be a remote PC mirrored via a remote-view tool (RustDesk/AnyDesk/VNC); ignore the remote-view toolbar/title-bar and act on the ERP application content itself.
Return strictly valid JSON.`

func (v *visionLocator) Locate(ctx context.Context, screenshotPNG []byte, instruction string) (ghost.Action, error) {
	b64 := base64.StdEncoding.EncodeToString(screenshotPNG)
	dims := ""
	if cfg, _, err := image.DecodeConfig(bytes.NewReader(screenshotPNG)); err == nil {
		dims = fmt.Sprintf("\nThe screenshot is %dx%d pixels; x,y must be within 0..%d and 0..%d from the top-left.", cfg.Width, cfg.Height, cfg.Width, cfg.Height)
	}
	content, err := v.chat(ctx, []any{
		map[string]any{"role": "system", "content": ghostVisionSystemPrompt},
		map[string]any{"role": "user", "content": []any{
			map[string]any{"type": "text", "text": "Instruction: " + instruction + dims + "\nReturn the single next action as JSON."},
			map[string]any{"type": "image_url", "image_url": map[string]any{"url": "data:image/png;base64," + b64}},
		}},
	})
	if err != nil {
		return ghost.Action{}, err
	}
	return parseGhostAction(content)
}

const ghostVerifySystemPrompt = `You are a strict UI verifier. You are given a goal and a textual dump of the current UI's accessibility tree (role:name lines). Decide whether the goal is ALREADY satisfied by what the tree shows right now.
Reply with ONLY a compact JSON object, no prose, no markdown:
{"done":true|false,"reason":"short"}
Be strict: if the tree does not clearly show the goal achieved, return done=false. Do not assume an action worked just because it was attempted.`

// VerifyGoal is the grounded completion check ghost.RunLoop's Verify hook calls:
// given the goal and the current UI text (the accessibility tree), decide
// whether the goal is already satisfied. Text, not pixels — cheap, tree-first,
// and usable by text-only runners as well as vision ones.
func (v *visionLocator) VerifyGoal(ctx context.Context, goal, uiState string) (bool, string, error) {
	content, err := v.chat(ctx, []any{
		map[string]any{"role": "system", "content": ghostVerifySystemPrompt},
		map[string]any{"role": "user", "content": "Goal: " + goal + "\n\nCurrent UI (accessibility tree):\n" + uiState + "\n\nReturn JSON only."},
	})
	if err != nil {
		return false, "", err
	}
	s := strings.TrimSpace(content)
	if i := strings.Index(s, "{"); i >= 0 {
		if j := strings.LastIndex(s, "}"); j >= i {
			s = s[i : j+1]
		}
	}
	var out struct {
		Done   bool   `json:"done"`
		Reason string `json:"reason"`
	}
	if err := json.Unmarshal([]byte(s), &out); err != nil {
		return false, "", fmt.Errorf("verifier returned non-JSON: %q", content)
	}
	return out.Done, out.Reason, nil
}

// chat performs one OpenAI-compatible chat completion and returns the first
// choice's text content. Shared by the locator (image message) and the verifier
// (text message) so both use the same provider, key and timeout.
func (v *visionLocator) chat(ctx context.Context, messages []any) (string, error) {
	body := map[string]any{"model": v.model, "temperature": 0, "messages": messages}
	buf, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, "POST", v.baseURL+"/chat/completions", bytes.NewReader(buf))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if v.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+v.apiKey)
	}
	client := &http.Client{Timeout: 90 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("vision request failed: %w", err)
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
		return "", fmt.Errorf("vision decode failed: %w", err)
	}
	if out.Error != nil {
		return "", fmt.Errorf("vision error: %s", out.Error.Message)
	}
	if len(out.Choices) == 0 {
		return "", fmt.Errorf("vision returned no choices")
	}
	return out.Choices[0].Message.Content, nil
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
