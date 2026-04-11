package testkit

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

// Visual LLM inspection — let a multimodal model look at a screenshot
// and answer "is this UI broken?" in plain English. The point is *not*
// to replace pixel-diff snapshots; it's to catch the kinds of breakage
// pixel diffs miss (text overlapping a button, a CTA disappearing into
// the background, an image failing to load). Devs who pay for
// Applitools today are buying exactly this — and they pay $500+/mo for
// it. We do it on the user's own machine, against the user's own API
// key, and the screenshots never leave the user's network beyond the
// LLM provider they explicitly configured.
//
// Privacy posture (per the open-source safety rule):
//
//   - The user provides their own API key. We never read the key from
//     Convex / never send it to anything we control.
//   - Keys live in `~/.yaver/config.json` under the `vision_keys` map
//     OR in env vars (`MISTRAL_API_KEY`, `OPENAI_API_KEY`,
//     `ANTHROPIC_API_KEY`).
//   - The screenshot bytes are POSTed to the chosen provider directly
//     from the agent on the dev's machine. No Yaver server is in the
//     path. No telemetry.

// VisionProvider is the LLM backend used for image inspection.
type VisionProvider string

const (
	VisionProviderMistral   VisionProvider = "mistral"
	VisionProviderOpenAI    VisionProvider = "openai"
	VisionProviderAnthropic VisionProvider = "anthropic"
	VisionProviderOllama    VisionProvider = "ollama" // local, $0
)

// VisionConfig is the per-call configuration. Loaded from config or env.
type VisionConfig struct {
	Provider VisionProvider
	APIKey   string
	Model    string // e.g. "pixtral-12b-2409", "gpt-4o-mini", "claude-haiku-4-5-20251001", "llava"
	Endpoint string // override for self-hosted (Ollama, vLLM, etc.)
}

// LoadVisionConfig pulls config from env vars in priority order:
// MISTRAL_API_KEY → OPENAI_API_KEY → ANTHROPIC_API_KEY → fall back to
// local Ollama on http://127.0.0.1:11434. The caller can also build a
// VisionConfig manually.
func LoadVisionConfig() VisionConfig {
	if k := os.Getenv("MISTRAL_API_KEY"); k != "" {
		return VisionConfig{
			Provider: VisionProviderMistral,
			APIKey:   k,
			Model:    envOr("YAVER_VISION_MODEL", "pixtral-12b-2409"),
			Endpoint: "https://api.mistral.ai/v1/chat/completions",
		}
	}
	if k := os.Getenv("OPENAI_API_KEY"); k != "" {
		return VisionConfig{
			Provider: VisionProviderOpenAI,
			APIKey:   k,
			Model:    envOr("YAVER_VISION_MODEL", "gpt-4o-mini"),
			Endpoint: "https://api.openai.com/v1/chat/completions",
		}
	}
	if k := os.Getenv("ANTHROPIC_API_KEY"); k != "" {
		return VisionConfig{
			Provider: VisionProviderAnthropic,
			APIKey:   k,
			Model:    envOr("YAVER_VISION_MODEL", "claude-haiku-4-5-20251001"),
			Endpoint: "https://api.anthropic.com/v1/messages",
		}
	}
	// Final fallback: local Ollama (no key needed, $0).
	return VisionConfig{
		Provider: VisionProviderOllama,
		Model:    envOr("YAVER_VISION_MODEL", "llava"),
		Endpoint: envOr("OLLAMA_HOST", "http://127.0.0.1:11434") + "/api/chat",
	}
}

func envOr(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}

// InspectionResult is the structured answer the LLM gives back.
type InspectionResult struct {
	Provider VisionProvider `json:"provider"`
	Model    string         `json:"model"`
	Verdict  string         `json:"verdict"` // "pass" | "warn" | "fail"
	Issues   []string       `json:"issues,omitempty"`
	Raw      string         `json:"raw"` // full LLM output for debugging
}

// InspectImage asks the configured LLM to look at the screenshot and
// answer the question. Question defaults to a generic "is this UI
// broken?" if empty. The function never panics; on any provider error
// it returns a Result with Verdict=warn and the error in Issues, so a
// failing API call doesn't tank a CI run.
func InspectImage(ctx context.Context, cfg VisionConfig, imagePath, question string) *InspectionResult {
	if question == "" {
		question = "Look at this screenshot of a web app. Are there any visual bugs, broken layouts, overlapping elements, missing content, or anything that looks wrong? Reply with one of: PASS / WARN / FAIL on the first line, then a short bulleted list of issues if any. Be conservative — only flag real problems."
	}
	res := &InspectionResult{Provider: cfg.Provider, Model: cfg.Model, Verdict: "warn"}

	imgBytes, err := os.ReadFile(imagePath)
	if err != nil {
		res.Issues = []string{fmt.Sprintf("read image: %v", err)}
		return res
	}
	b64 := base64.StdEncoding.EncodeToString(imgBytes)
	dataURL := "data:image/png;base64," + b64

	body, contentType, err := buildVisionRequest(cfg, dataURL, question)
	if err != nil {
		res.Issues = []string{err.Error()}
		return res
	}
	httpReq, err := http.NewRequestWithContext(ctx, "POST", cfg.Endpoint, bytes.NewReader(body))
	if err != nil {
		res.Issues = []string{err.Error()}
		return res
	}
	httpReq.Header.Set("Content-Type", contentType)
	switch cfg.Provider {
	case VisionProviderMistral, VisionProviderOpenAI:
		httpReq.Header.Set("Authorization", "Bearer "+cfg.APIKey)
	case VisionProviderAnthropic:
		httpReq.Header.Set("x-api-key", cfg.APIKey)
		httpReq.Header.Set("anthropic-version", "2023-06-01")
	}

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		res.Issues = []string{fmt.Sprintf("vision provider: %v", err)}
		return res
	}
	defer resp.Body.Close()
	respBytes, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		res.Issues = []string{fmt.Sprintf("vision provider %d: %s", resp.StatusCode, truncate(string(respBytes), 500))}
		return res
	}
	res.Raw = string(respBytes)
	res.Verdict, res.Issues = parseVisionResponse(cfg, respBytes)
	return res
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n] + "..."
	}
	return s
}

// buildVisionRequest produces the JSON body for each provider.
func buildVisionRequest(cfg VisionConfig, dataURL, question string) ([]byte, string, error) {
	switch cfg.Provider {
	case VisionProviderMistral, VisionProviderOpenAI:
		// OpenAI-compatible chat/completions with image_url.
		body := map[string]interface{}{
			"model": cfg.Model,
			"messages": []map[string]interface{}{
				{
					"role": "user",
					"content": []map[string]interface{}{
						{"type": "text", "text": question},
						{"type": "image_url", "image_url": map[string]string{"url": dataURL}},
					},
				},
			},
			"max_tokens": 400,
		}
		b, err := json.Marshal(body)
		return b, "application/json", err
	case VisionProviderAnthropic:
		body := map[string]interface{}{
			"model":      cfg.Model,
			"max_tokens": 400,
			"messages": []map[string]interface{}{
				{
					"role": "user",
					"content": []map[string]interface{}{
						{
							"type": "image",
							"source": map[string]string{
								"type":       "base64",
								"media_type": "image/png",
								"data":       dataURL[len("data:image/png;base64,"):],
							},
						},
						{"type": "text", "text": question},
					},
				},
			},
		}
		b, err := json.Marshal(body)
		return b, "application/json", err
	case VisionProviderOllama:
		body := map[string]interface{}{
			"model":  cfg.Model,
			"stream": false,
			"messages": []map[string]interface{}{
				{
					"role":    "user",
					"content": question,
					"images":  []string{dataURL[len("data:image/png;base64,"):]},
				},
			},
		}
		b, err := json.Marshal(body)
		return b, "application/json", err
	}
	return nil, "", fmt.Errorf("unknown vision provider %q", cfg.Provider)
}

// parseVisionResponse normalizes the various provider response shapes
// into (verdict, issues). Best-effort — falls back to "warn" if the
// response isn't recognizable.
func parseVisionResponse(cfg VisionConfig, body []byte) (string, []string) {
	var text string
	switch cfg.Provider {
	case VisionProviderMistral, VisionProviderOpenAI:
		var r struct {
			Choices []struct {
				Message struct {
					Content string `json:"content"`
				} `json:"message"`
			} `json:"choices"`
		}
		_ = json.Unmarshal(body, &r)
		if len(r.Choices) > 0 {
			text = r.Choices[0].Message.Content
		}
	case VisionProviderAnthropic:
		var r struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		}
		_ = json.Unmarshal(body, &r)
		if len(r.Content) > 0 {
			text = r.Content[0].Text
		}
	case VisionProviderOllama:
		var r struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		}
		_ = json.Unmarshal(body, &r)
		text = r.Message.Content
	}
	verdict := "warn"
	upper := bytes.ToUpper([]byte(text))
	switch {
	case bytes.HasPrefix(upper, []byte("PASS")):
		verdict = "pass"
	case bytes.HasPrefix(upper, []byte("FAIL")):
		verdict = "fail"
	case bytes.HasPrefix(upper, []byte("WARN")):
		verdict = "warn"
	}
	// Pull bullet lines as issues.
	issues := []string{}
	for _, raw := range bytes.Split([]byte(text), []byte("\n")) {
		l := bytes.TrimSpace(raw)
		if len(l) == 0 {
			continue
		}
		ls := string(l)
		// Recognize "- ", "* ", "• " as bullets.
		if ls[0] == '-' || ls[0] == '*' || (len(ls) >= 3 && ls[:3] == "\u2022") {
			rest := ls[1:]
			if len(ls) >= 3 && ls[:3] == "\u2022" {
				rest = ls[3:]
			}
			issues = append(issues, string(bytes.TrimSpace([]byte(rest))))
		}
	}
	return verdict, issues
}
