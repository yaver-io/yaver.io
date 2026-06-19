package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

type mfgPixelSeedBenchCase struct {
	Name       string
	ScreenText string
	BOM        []mfgBOMLine
	Expected   []mfgPixelSeed
	Tolerance  float64
}

type mfgPixelSeedBenchUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type mfgPixelSeedBenchResult struct {
	Model       string                 `json:"model"`
	Score       float64                `json:"score"`
	DurationMS  int64                  `json:"durationMs"`
	Usage       mfgPixelSeedBenchUsage `json:"usage"`
	EstimatedUS float64                `json:"estimatedUsd"`
	Output      string                 `json:"output,omitempty"`
	Error       string                 `json:"error,omitempty"`
}

type mfgPixelSeedBenchSeedList struct {
	Seeds []mfgPixelSeed `json:"seeds"`
}

func TestMfgPixelSeedBenchmarkScoring(t *testing.T) {
	cases := mfgPixelSeedBenchFixtures()
	if len(cases) == 0 {
		t.Fatal("no fixtures")
	}
	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			got := mfgPixelSeedBenchScore(tc, tc.Expected)
			if got != 1 {
				t.Fatalf("perfect prediction score = %.4f, want 1", got)
			}
			partial := append([]mfgPixelSeed(nil), tc.Expected...)
			if len(partial) > 0 {
				partial[0].X = ptrFloat(999)
				partial[0].Location = "wrong shelf"
			}
			got = mfgPixelSeedBenchScore(tc, partial)
			if got >= 0.95 {
				t.Fatalf("bad prediction scored too high: %.4f", got)
			}
		})
	}
}

func TestMfgPixelSeedBenchmarkOpenRouterRequest(t *testing.T) {
	var seenAuth, seenModel string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Fatalf("path = %s, want /chat/completions", r.URL.Path)
		}
		seenAuth = r.Header.Get("Authorization")
		var req struct {
			Model    string `json:"model"`
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		seenModel = req.Model
		if len(req.Messages) != 2 || !strings.Contains(req.Messages[1].Content, "Return ONLY valid compact JSON") {
			t.Fatalf("unexpected messages: %+v", req.Messages)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"choices":[{"message":{"content":"{\"seeds\":[{\"lineRef\":\"J1\",\"x\":164,\"y\":88,\"quantity\":12,\"location\":\"A1 feeder\"},{\"lineRef\":\"R3\",\"x\":416,\"y\":214,\"quantity\":4,\"location\":\"B2 bin\"}]}"}}],
			"usage":{"prompt_tokens":480,"completion_tokens":72,"total_tokens":552}
		}`))
	}))
	defer srv.Close()

	res, err := mfgRunPixelSeedBench(context.Background(), srv.URL, "test-key", []string{"google/gemini-2.5-flash-lite"}, mfgPixelSeedBenchFixtures()[:1])
	if err != nil {
		t.Fatalf("benchmark failed: %v", err)
	}
	if seenAuth != "Bearer test-key" {
		t.Fatalf("auth header = %q", seenAuth)
	}
	if seenModel != "google/gemini-2.5-flash-lite" {
		t.Fatalf("model = %q", seenModel)
	}
	if len(res) != 1 || res[0].Score != 1 {
		t.Fatalf("result = %+v, want one perfect result", res)
	}
	if res[0].EstimatedUS <= 0 {
		t.Fatalf("estimated cost should be populated: %+v", res[0])
	}
}

func TestMfgPixelSeedLiveOpenRouterBenchmark(t *testing.T) {
	if os.Getenv("YAVER_MFG_PIXEL_SEED_LIVE") != "1" {
		t.Skip("set YAVER_MFG_PIXEL_SEED_LIVE=1 to run live OpenRouter RFQ pixel seed benchmark")
	}
	key := strings.TrimSpace(os.Getenv("OPENROUTER_API_KEY"))
	if key == "" {
		t.Skip("OPENROUTER_API_KEY required for live OpenRouter benchmark")
	}
	baseURL := firstNonEmptyStr(os.Getenv("YAVER_MFG_PIXEL_SEED_BASE_URL"), "https://openrouter.ai/api/v1")
	models := mfgPixelSeedBenchModels()
	results, err := mfgRunPixelSeedBench(context.Background(), baseURL, key, models, mfgPixelSeedBenchFixtures())
	if err != nil {
		t.Fatalf("live benchmark failed: %v", err)
	}
	for _, res := range results {
		t.Logf("model=%s score=%.3f duration_ms=%d prompt_tokens=%d completion_tokens=%d estimated_usd=%.8f err=%s",
			res.Model, res.Score, res.DurationMS, res.Usage.PromptTokens, res.Usage.CompletionTokens, res.EstimatedUS, res.Error)
	}
	t.Logf("recommendation=%s", mfgPixelSeedBenchRecommendation(results))
}

func mfgRunPixelSeedBench(ctx context.Context, baseURL, apiKey string, models []string, cases []mfgPixelSeedBenchCase) ([]mfgPixelSeedBenchResult, error) {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		return nil, fmt.Errorf("base URL required")
	}
	client := &http.Client{Timeout: 90 * time.Second}
	var out []mfgPixelSeedBenchResult
	for _, model := range models {
		model = strings.TrimSpace(model)
		if model == "" {
			continue
		}
		var modelScore float64
		var modelDuration int64
		var usage mfgPixelSeedBenchUsage
		var lastOutput, lastErr string
		for _, tc := range cases {
			start := time.Now()
			raw, gotUsage, err := mfgPixelSeedBenchCall(ctx, client, baseURL, apiKey, model, tc)
			modelDuration += time.Since(start).Milliseconds()
			usage.PromptTokens += gotUsage.PromptTokens
			usage.CompletionTokens += gotUsage.CompletionTokens
			usage.TotalTokens += gotUsage.TotalTokens
			lastOutput = raw
			if err != nil {
				lastErr = err.Error()
				continue
			}
			seeds, err := mfgParsePixelSeedBenchOutput(raw)
			if err != nil {
				lastErr = err.Error()
				continue
			}
			modelScore += mfgPixelSeedBenchScore(tc, seeds)
		}
		score := 0.0
		if len(cases) > 0 {
			score = modelScore / float64(len(cases))
		}
		out = append(out, mfgPixelSeedBenchResult{
			Model:       model,
			Score:       score,
			DurationMS:  modelDuration,
			Usage:       usage,
			EstimatedUS: mfgPixelSeedBenchCost(model, usage),
			Output:      lastOutput,
			Error:       lastErr,
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].EstimatedUS < out[j].EstimatedUS
	})
	return out, nil
}

func mfgPixelSeedBenchCall(ctx context.Context, client *http.Client, baseURL, apiKey, model string, tc mfgPixelSeedBenchCase) (string, mfgPixelSeedBenchUsage, error) {
	body := map[string]any{
		"model":       model,
		"temperature": 0,
		"messages": []map[string]string{
			{"role": "system", "content": mfgPixelSeedBenchSystemPrompt},
			{"role": "user", "content": mfgPixelSeedBenchUserPrompt(tc)},
		},
	}
	buf, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/chat/completions", bytes.NewReader(buf))
	if err != nil {
		return "", mfgPixelSeedBenchUsage{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", mfgPixelSeedBenchUsage{}, err
	}
	defer resp.Body.Close()
	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage mfgPixelSeedBenchUsage `json:"usage"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", out.Usage, err
	}
	if out.Error != nil {
		return "", out.Usage, fmt.Errorf("%s", out.Error.Message)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return "", out.Usage, fmt.Errorf("status %d", resp.StatusCode)
	}
	if len(out.Choices) == 0 {
		return "", out.Usage, fmt.Errorf("no choices")
	}
	return out.Choices[0].Message.Content, out.Usage, nil
}

const mfgPixelSeedBenchSystemPrompt = `You extract RFQ manual-assist pixel seeds from BOM and screen notes.
Return ONLY valid compact JSON: {"seeds":[{"lineRef":"...","x":0,"y":0,"quantity":0,"location":"..."}]}
Rules:
- x/y are pixels from the top-left of the screen.
- lineRef must match a BOM ref exactly.
- quantity must reflect the user's visible/manual correction when provided; otherwise use BOM qty.
- location must be the visible storage/board/feeder/bin label.
- Do not invent seeds for BOM lines that are not visible.`

func mfgPixelSeedBenchUserPrompt(tc mfgPixelSeedBenchCase) string {
	var b strings.Builder
	b.WriteString("BOM lines:\n")
	for _, line := range tc.BOM {
		b.WriteString(fmt.Sprintf("- ref=%s qty=%s part=%s package=%s location=%s\n",
			line.Ref, trimBenchFloat(line.Qty), line.Part, line.Package, line.Location))
	}
	b.WriteString("\nScreen/manual assist notes:\n")
	b.WriteString(tc.ScreenText)
	b.WriteString("\n\nReturn ONLY valid compact JSON.")
	return b.String()
}

func mfgParsePixelSeedBenchOutput(s string) ([]mfgPixelSeed, error) {
	s = strings.TrimSpace(s)
	if i := strings.Index(s, "{"); i >= 0 {
		if j := strings.LastIndex(s, "}"); j >= i {
			s = s[i : j+1]
		}
	}
	var list mfgPixelSeedBenchSeedList
	if err := json.Unmarshal([]byte(s), &list); err != nil {
		return nil, err
	}
	return list.Seeds, nil
}

func mfgPixelSeedBenchScore(tc mfgPixelSeedBenchCase, actual []mfgPixelSeed) float64 {
	if len(tc.Expected) == 0 {
		if len(actual) == 0 {
			return 1
		}
		return 0
	}
	byRef := map[string]mfgPixelSeed{}
	for _, seed := range actual {
		byRef[strings.ToUpper(strings.TrimSpace(seed.LineRef))] = seed
	}
	var sum float64
	for _, want := range tc.Expected {
		got, ok := byRef[strings.ToUpper(strings.TrimSpace(want.LineRef))]
		if !ok {
			continue
		}
		lineScore := 0.35
		if want.Quantity != nil && got.Quantity != nil && math.Abs(*want.Quantity-*got.Quantity) < 0.0001 {
			lineScore += 0.20
		}
		if strings.EqualFold(strings.TrimSpace(want.Location), strings.TrimSpace(got.Location)) {
			lineScore += 0.20
		}
		if want.X != nil && want.Y != nil && got.X != nil && got.Y != nil {
			dist := math.Hypot(*want.X-*got.X, *want.Y-*got.Y)
			if dist <= tc.Tolerance {
				lineScore += 0.25
			} else if dist <= tc.Tolerance*2 {
				lineScore += 0.10
			}
		}
		sum += lineScore
	}
	return math.Min(1, sum/float64(len(tc.Expected)))
}

func mfgPixelSeedBenchModels() []string {
	raw := strings.TrimSpace(os.Getenv("YAVER_MFG_PIXEL_SEED_MODELS"))
	if raw == "" {
		raw = "google/gemini-2.5-flash-lite,google/gemini-2.5-flash,google/gemini-3.5-flash"
	}
	parts := strings.Split(raw, ",")
	var out []string
	for _, part := range parts {
		if s := strings.TrimSpace(part); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func mfgPixelSeedBenchCost(model string, usage mfgPixelSeedBenchUsage) float64 {
	in, out := mfgPixelSeedBenchPrice(model)
	return (float64(usage.PromptTokens)/1_000_000)*in + (float64(usage.CompletionTokens)/1_000_000)*out
}

func mfgPixelSeedBenchPrice(model string) (inputPerM, outputPerM float64) {
	override := os.Getenv("YAVER_MFG_PIXEL_SEED_PRICE_" + strings.NewReplacer("/", "_", "-", "_", ".", "_").Replace(strings.ToUpper(model)))
	if override != "" {
		parts := strings.Split(override, ",")
		if len(parts) == 2 {
			in, _ := strconv.ParseFloat(strings.TrimSpace(parts[0]), 64)
			out, _ := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
			return in, out
		}
	}
	switch strings.ToLower(strings.TrimSpace(model)) {
	case "google/gemini-2.5-flash-lite":
		return 0.10, 0.40
	case "google/gemini-2.5-flash":
		return 0.30, 2.50
	case "google/gemini-3.5-flash":
		return 1.50, 9.00
	default:
		return 0, 0
	}
}

func mfgPixelSeedBenchRecommendation(results []mfgPixelSeedBenchResult) string {
	if len(results) == 0 {
		return "no results"
	}
	best := results[0]
	for _, res := range results {
		if res.Error != "" {
			continue
		}
		if res.Score >= 0.92 && (best.Error != "" || best.Score < 0.92 || mfgPixelSeedBenchBetterShortOutput(res, best)) {
			best = res
		}
	}
	if best.Score >= 0.92 {
		return fmt.Sprintf("%s: best short-output price/performance for RFQ pixel seeding at score %.3f, %dms, estimated $%.8f", best.Model, best.Score, best.DurationMS, best.EstimatedUS)
	}
	return fmt.Sprintf("%s: highest measured quality, but score %.3f is below the 0.92 production target", results[0].Model, results[0].Score)
}

func mfgPixelSeedBenchBetterShortOutput(a, b mfgPixelSeedBenchResult) bool {
	if a.Score != b.Score {
		return a.Score > b.Score
	}
	if a.DurationMS != b.DurationMS {
		return a.DurationMS < b.DurationMS
	}
	return a.EstimatedUS < b.EstimatedUS
}

func mfgPixelSeedBenchFixtures() []mfgPixelSeedBenchCase {
	return []mfgPixelSeedBenchCase{
		{
			Name: "basic-two-visible-lines",
			BOM: []mfgBOMLine{
				{Ref: "J1", Qty: 1, Part: "USB-C", Package: "SMD", Location: "A"},
				{Ref: "R3", Qty: 2, Part: "10k", Package: "0402", Location: "B"},
				{Ref: "U2", Qty: 1, Part: "ESP32", Package: "QFN", Location: "C"},
			},
			ScreenText: "Canvas 800x600. Manual assists visible: J1 label centered at pixel (164,88), user corrected quantity to 12, location A1 feeder. R3 label centered at pixel (416,214), user corrected quantity to 4, location B2 bin. U2 is not visible on this screenshot.",
			Expected: []mfgPixelSeed{
				{LineRef: "J1", X: ptrFloat(164), Y: ptrFloat(88), Quantity: ptrFloat(12), Location: "A1 feeder"},
				{LineRef: "R3", X: ptrFloat(416), Y: ptrFloat(214), Quantity: ptrFloat(4), Location: "B2 bin"},
			},
			Tolerance: 12,
		},
		{
			Name: "quantity-fallback-and-location",
			BOM: []mfgBOMLine{
				{Ref: "C7", Qty: 30, Part: "1uF", Package: "0603", Location: "tray C"},
				{Ref: "D2", Qty: 5, Part: "LED green", Package: "0603", Location: "reel D"},
			},
			ScreenText: "Canvas 1280x720. C7 appears under crosshair at x=722 y=312; no new quantity badge is shown, so use BOM quantity; location label reads tray C. D2 appears at x=910 y=470 with visible quantity override 9 and storage label reel D.",
			Expected: []mfgPixelSeed{
				{LineRef: "C7", X: ptrFloat(722), Y: ptrFloat(312), Quantity: ptrFloat(30), Location: "tray C"},
				{LineRef: "D2", X: ptrFloat(910), Y: ptrFloat(470), Quantity: ptrFloat(9), Location: "reel D"},
			},
			Tolerance: 16,
		},
	}
}

func ptrFloat(v float64) *float64 { return &v }

func trimBenchFloat(v float64) string {
	if math.Trunc(v) == v {
		return strconv.FormatInt(int64(v), 10)
	}
	return strconv.FormatFloat(v, 'f', -1, 64)
}
