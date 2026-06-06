package robot

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
)

// VisionConfig points at any OpenAI-compatible /chat/completions endpoint. The
// resolution ladder mirrors ghost_vision.go: explicit → GHOST_VISION_* →
// OPENAI_* (the runner's provider) → local Ollama. No new creds; on-prem-capable.
type VisionConfig struct {
	BaseURL string
	APIKey  string
	Model   string
}

const localOllamaV1 = "http://localhost:11434/v1"

func (vc VisionConfig) resolve() (VisionConfig, error) {
	if vc.BaseURL == "" {
		vc.BaseURL = firstNonEmpty(os.Getenv("GHOST_VISION_BASE_URL"), os.Getenv("OPENAI_BASE_URL"), localOllamaV1)
	}
	if vc.APIKey == "" {
		vc.APIKey = firstNonEmpty(os.Getenv("GHOST_VISION_API_KEY"), os.Getenv("OPENAI_API_KEY"))
	}
	if vc.Model == "" {
		vc.Model = firstNonEmpty(os.Getenv("GHOST_VISION_MODEL"), os.Getenv("OPENAI_MODEL"))
		if vc.Model == "" {
			if strings.Contains(vc.BaseURL, "11434") {
				vc.Model = "llama3.2-vision"
			} else {
				vc.Model = "gpt-4o-mini"
			}
		}
	}
	vc.BaseURL = strings.TrimRight(vc.BaseURL, "/")
	if vc.BaseURL == "" {
		return vc, fmt.Errorf("no vision endpoint resolved")
	}
	return vc, nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

const verifySystemPrompt = `You verify a Cartesian robot (a 3D-printer frame repurposed as an XYZ positioner).
You are given a BEFORE image, an AFTER image, and the EXPECTED motion.
Reply with ONLY a compact JSON object, no prose, no markdown:
{"moved":bool,"confidence":0..1,"obstruction":bool,"reason":"short","observed":"short"}
- moved=true ONLY if the carriage/gantry visibly moved consistent with the expectation.
- obstruction=true if anything is in the tool path or the machine looks crashed/jammed.
- confidence reflects how clearly the images support your answer.
Return strictly valid JSON.`

// VerifyMotion asks the configured vision model whether the machine moved as
// expected, comparing before/after frames. This is the agent-side ("agent"
// mode) judgment; in "frames" mode the caller's model does this instead.
func VerifyMotion(ctx context.Context, vc VisionConfig, before, after []byte, expectation string) (Verdict, error) {
	cfg, err := vc.resolve()
	if err != nil {
		return Verdict{}, err
	}
	content := []any{
		map[string]any{"type": "text", "text": "EXPECTED motion: " + expectation + "\nBEFORE image:"},
		map[string]any{"type": "image_url", "image_url": map[string]any{"url": jpegDataURL(before)}},
		map[string]any{"type": "text", "text": "AFTER image:"},
		map[string]any{"type": "image_url", "image_url": map[string]any{"url": jpegDataURL(after)}},
		map[string]any{"type": "text", "text": "Return the verdict JSON."},
	}
	body := map[string]any{
		"model":       cfg.Model,
		"temperature": 0,
		"messages": []any{
			map[string]any{"role": "system", "content": verifySystemPrompt},
			map[string]any{"role": "user", "content": content},
		},
	}
	buf, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, "POST", cfg.BaseURL+"/chat/completions", bytes.NewReader(buf))
	if err != nil {
		return Verdict{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	if cfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.APIKey)
	}
	client := &http.Client{Timeout: 90 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return Verdict{}, fmt.Errorf("vision request failed: %w", err)
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
		return Verdict{}, fmt.Errorf("vision decode failed: %w", err)
	}
	if out.Error != nil {
		return Verdict{}, fmt.Errorf("vision error: %s", out.Error.Message)
	}
	if len(out.Choices) == 0 {
		return Verdict{}, fmt.Errorf("vision returned no choices")
	}
	return parseVerdict(out.Choices[0].Message.Content, expectation)
}

func parseVerdict(s, expectation string) (Verdict, error) {
	s = strings.TrimSpace(s)
	if i := strings.Index(s, "{"); i >= 0 {
		if j := strings.LastIndex(s, "}"); j >= i {
			s = s[i : j+1]
		}
	}
	var v Verdict
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		return Verdict{}, fmt.Errorf("vision returned non-JSON verdict: %q", s)
	}
	v.Mode = "agent"
	v.Expectation = expectation
	return v, nil
}

func jpegDataURL(b []byte) string {
	return "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(b)
}
