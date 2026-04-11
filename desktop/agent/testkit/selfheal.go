package testkit

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// AI selector self-healing.
//
// When a step like `click: button.submit` fails because the page
// refactored the class name, this function asks the user's chosen LLM
// for a new selector that matches the same intent. The result feeds
// back into the runner via MCP so an external AI agent (Claude Code,
// Cursor, etc.) can patch the spec without the dev opening it.
//
// Privacy posture: same as visual_llm.go — the dev brings their own
// API key, the request goes straight from the agent to the chosen
// provider, no Yaver server is in the path.

// SelfHealRequest is the input to SelfHealSelector.
type SelfHealRequest struct {
	// FailedSelector is the CSS selector that no longer matches.
	FailedSelector string
	// DOMHTML is the current page HTML the agent captured at failure
	// time. We deliberately don't trim it — modern LLMs handle ~30k
	// tokens, and seeing the full DOM is often necessary to find the
	// right element.
	DOMHTML string
	// Intent is an optional human label like "submit button" or
	// "email input." Helps the LLM disambiguate when the failing
	// selector is opaque.
	Intent string
	// Override the default vision config. Optional.
	Vision *VisionConfig
}

// SelfHealResult is the LLM's proposal.
type SelfHealResult struct {
	Selector  string
	Reasoning string
	Provider  VisionProvider
	Model     string
}

// SelfHealSelector calls the configured LLM with the failing selector
// and the DOM, returning a suggested replacement. The function never
// panics — provider errors come back as a regular error so the
// autonomous loop can decide whether to retry or give up.
func SelfHealSelector(ctx context.Context, req SelfHealRequest) (*SelfHealResult, error) {
	if req.FailedSelector == "" || req.DOMHTML == "" {
		return nil, fmt.Errorf("failed_selector and dom_html are required")
	}
	cfg := LoadVisionConfig()
	if req.Vision != nil {
		cfg = *req.Vision
	}

	intentLine := ""
	if req.Intent != "" {
		intentLine = fmt.Sprintf("Original intent: %s\n", req.Intent)
	}
	prompt := fmt.Sprintf(`A CSS selector in an automated browser test no longer matches anything on the page. Propose a single replacement CSS selector that would match the same intended element on the new page.

Failed selector: %s
%s
Current page HTML (truncated to 24k chars):
%s

Reply with exactly two lines:
SELECTOR: <new css selector>
REASON: <one short sentence explaining why this matches>

Be conservative — prefer stable selectors (id, role, text content, semantic tags) over brittle ones (auto-generated class hashes). If you cannot find a confident match, reply SELECTOR: NONE`, req.FailedSelector, intentLine, truncate(req.DOMHTML, 24*1024))

	body, ctype, err := buildSelfHealRequest(cfg, prompt)
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, "POST", cfg.Endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", ctype)
	switch cfg.Provider {
	case VisionProviderMistral, VisionProviderOpenAI:
		httpReq.Header.Set("Authorization", "Bearer "+cfg.APIKey)
	case VisionProviderAnthropic:
		httpReq.Header.Set("x-api-key", cfg.APIKey)
		httpReq.Header.Set("anthropic-version", "2023-06-01")
	}
	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("self-heal LLM call: %w", err)
	}
	defer resp.Body.Close()
	respBytes, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("self-heal LLM %d: %s", resp.StatusCode, truncate(string(respBytes), 500))
	}
	text := extractLLMText(cfg, respBytes)
	selector, reason := parseSelfHealResponse(text)
	if selector == "" || selector == "NONE" {
		return nil, fmt.Errorf("LLM could not propose a confident selector")
	}
	return &SelfHealResult{
		Selector:  selector,
		Reasoning: reason,
		Provider:  cfg.Provider,
		Model:     cfg.Model,
	}, nil
}

func buildSelfHealRequest(cfg VisionConfig, prompt string) ([]byte, string, error) {
	switch cfg.Provider {
	case VisionProviderMistral, VisionProviderOpenAI:
		body := map[string]interface{}{
			"model":      cfg.Model,
			"max_tokens": 200,
			"messages": []map[string]interface{}{
				{"role": "user", "content": prompt},
			},
		}
		b, err := json.Marshal(body)
		return b, "application/json", err
	case VisionProviderAnthropic:
		body := map[string]interface{}{
			"model":      cfg.Model,
			"max_tokens": 200,
			"messages": []map[string]interface{}{
				{"role": "user", "content": prompt},
			},
		}
		b, err := json.Marshal(body)
		return b, "application/json", err
	case VisionProviderOllama:
		body := map[string]interface{}{
			"model":  cfg.Model,
			"stream": false,
			"messages": []map[string]interface{}{
				{"role": "user", "content": prompt},
			},
		}
		b, err := json.Marshal(body)
		return b, "application/json", err
	}
	return nil, "", fmt.Errorf("unknown provider %q", cfg.Provider)
}

func extractLLMText(cfg VisionConfig, body []byte) string {
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
			return r.Choices[0].Message.Content
		}
	case VisionProviderAnthropic:
		var r struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		}
		_ = json.Unmarshal(body, &r)
		if len(r.Content) > 0 {
			return r.Content[0].Text
		}
	case VisionProviderOllama:
		var r struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		}
		_ = json.Unmarshal(body, &r)
		return r.Message.Content
	}
	return ""
}

func parseSelfHealResponse(text string) (selector, reason string) {
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "SELECTOR:") {
			selector = strings.TrimSpace(strings.TrimPrefix(line, "SELECTOR:"))
		} else if strings.HasPrefix(line, "REASON:") {
			reason = strings.TrimSpace(strings.TrimPrefix(line, "REASON:"))
		}
	}
	return
}
