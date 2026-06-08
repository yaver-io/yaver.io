package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/yaver-io/agent/testkit"
)

// qa_model.go — the inference seam for the app-test brain (qa_brain.go). Two
// methods so the doc's two-model cost split (docs/yaver-ai-app-test-agent.md §4)
// is natural: Decide is a cheap TEXT turn over the view hierarchy (navigation);
// Judge is a stronger VISION turn over a screenshot (the assertion that matters).
//
// Keys are the USER's (BYOK or gateway) — resolved by testkit.LoadVisionConfig
// from env / ~/.yaver config, never read from Convex (privacy contract). Tests
// inject fakeQAModel and never touch the network.
type qaModel interface {
	// Decide returns the model's text reply to (system,user) — the next action
	// JSON. png is an OPTIONAL screenshot: pass it for vision navigation when the
	// view tree is unavailable (redroid's uiautomator is unreliable — verified on
	// magara 2026-06-09), nil for the cheap text path.
	Decide(ctx context.Context, system, user string, png []byte) (string, error)
	// Judge looks at a screenshot and rules on an expectation, returning a
	// verdict ("pass"|"warn"|"fail") + a one-line reason.
	Judge(ctx context.Context, expectation string, png []byte) (verdict, reason string, err error)
}

// httpQAModel calls the user's configured provider. Decide is a chat completion;
// Judge reuses testkit's vision inspector (one code path for image calls).
type httpQAModel struct {
	cfg testkit.VisionConfig
}

func newHTTPQAModel(cfg testkit.VisionConfig) *httpQAModel { return &httpQAModel{cfg: cfg} }

func (m *httpQAModel) Judge(ctx context.Context, expectation string, png []byte) (string, string, error) {
	tmp, err := os.CreateTemp("", "qa-judge-*.png")
	if err != nil {
		return "", "", err
	}
	tmpPath := tmp.Name()
	_, _ = tmp.Write(png)
	tmp.Close()
	defer os.Remove(tmpPath)
	q := "You are QA-reviewing a mobile app screenshot. Expectation: " + expectation +
		"\nReply with PASS, WARN, or FAIL on the first line, then one short line explaining why. Be conservative — only FAIL on a clear miss."
	res := testkit.InspectImage(ctx, m.cfg, tmpPath, q)
	reason := ""
	if len(res.Issues) > 0 {
		reason = res.Issues[0]
	}
	return res.Verdict, reason, nil
}

func (m *httpQAModel) Decide(ctx context.Context, system, user string, png []byte) (string, error) {
	body, err := m.buildChatBody(system, user, png)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, "POST", m.cfg.Endpoint, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	switch m.cfg.Provider {
	case testkit.VisionProviderMistral, testkit.VisionProviderOpenAI:
		req.Header.Set("Authorization", "Bearer "+m.cfg.APIKey)
	case testkit.VisionProviderAnthropic:
		req.Header.Set("x-api-key", m.cfg.APIKey)
		req.Header.Set("anthropic-version", "2023-06-01")
	}
	cl := &http.Client{Timeout: 45 * time.Second}
	resp, err := cl.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	rb, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("model %d: %s", resp.StatusCode, truncQA(string(rb), 400))
	}
	return parseChatText(m.cfg.Provider, rb), nil
}

func (m *httpQAModel) buildChatBody(system, user string, png []byte) ([]byte, error) {
	b64 := ""
	if len(png) > 0 {
		b64 = base64.StdEncoding.EncodeToString(png)
	}
	switch m.cfg.Provider {
	case testkit.VisionProviderAnthropic:
		content := []map[string]any{}
		if b64 != "" {
			content = append(content, map[string]any{
				"type":   "image",
				"source": map[string]string{"type": "base64", "media_type": "image/png", "data": b64},
			})
		}
		content = append(content, map[string]any{"type": "text", "text": user})
		return json.Marshal(map[string]any{
			"model":      m.cfg.Model,
			"max_tokens": 500,
			"system":     system,
			"messages":   []map[string]any{{"role": "user", "content": content}},
		})
	case testkit.VisionProviderOllama:
		msg := map[string]any{"role": "user", "content": user}
		if b64 != "" {
			msg["images"] = []string{b64}
		}
		return json.Marshal(map[string]any{
			"model":    m.cfg.Model,
			"stream":   false,
			"messages": []map[string]any{{"role": "system", "content": system}, msg},
		})
	default: // openai / mistral chat completions
		var content any = user
		if b64 != "" {
			content = []map[string]any{
				{"type": "text", "text": user},
				{"type": "image_url", "image_url": map[string]string{"url": "data:image/png;base64," + b64}},
			}
		}
		return json.Marshal(map[string]any{
			"model": m.cfg.Model,
			"messages": []map[string]any{
				{"role": "system", "content": system},
				{"role": "user", "content": content},
			},
			"max_tokens": 500,
		})
	}
}

func parseChatText(p testkit.VisionProvider, body []byte) string {
	switch p {
	case testkit.VisionProviderAnthropic:
		var r struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		}
		_ = json.Unmarshal(body, &r)
		if len(r.Content) > 0 {
			return r.Content[0].Text
		}
	case testkit.VisionProviderOllama:
		var r struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		}
		_ = json.Unmarshal(body, &r)
		return r.Message.Content
	default:
		var r struct {
			Choices []struct {
				Message struct {
					Content string `json:"content"`
				} `json:"message"`
			} `json:"choices"`
		}
		_ = json.Unmarshal(body, &r)
		if len(r.Choices) > 0 {
			return r.Choices[0].Message.Content
		}
	}
	return ""
}

func truncQA(s string, n int) string {
	if len(s) > n {
		return s[:n] + "..."
	}
	return strings.TrimSpace(s)
}
