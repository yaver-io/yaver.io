package main

// perf_workspace.go — Performance and load testing manager for the Yaver workspace.
//
// Provides Lighthouse audits via Chrome DevTools Protocol and load testing via
// hey/k6/ab. Named perf_workspace.go to avoid conflicts with Linux perf counter
// helpers in mcp_analysis.go (perf_record, perf_stat).

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	osexec "os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ---------------------------------------------------------------------------
// Result types
// ---------------------------------------------------------------------------

// LighthouseResult holds scores and Core Web Vitals from a Lighthouse audit.
type LighthouseResult struct {
	URL                     string    `json:"url"`
	Performance             float64   `json:"performance"`
	Accessibility           float64   `json:"accessibility"`
	BestPractices           float64   `json:"best_practices"`
	SEO                     float64   `json:"seo"`
	FirstContentfulPaint    string    `json:"fcp"`
	LargestContentfulPaint  string    `json:"lcp"`
	TotalBlockingTime       string    `json:"tbt"`
	CumulativeLayoutShift   string    `json:"cls"`
	SpeedIndex              string    `json:"speed_index"`
	ReportPath              string    `json:"report_path"`
	CapturedAt              time.Time `json:"captured_at"`
}

// LoadTestResult holds aggregated statistics from a single load test run.
type LoadTestResult struct {
	URL         string         `json:"url"`
	Requests    int            `json:"requests"`
	Concurrency int            `json:"concurrency"`
	Duration    time.Duration  `json:"duration_ns"`
	RPS         float64        `json:"rps"`
	AvgLatency  time.Duration  `json:"avg_latency_ns"`
	P50         time.Duration  `json:"p50_ns"`
	P95         time.Duration  `json:"p95_ns"`
	P99         time.Duration  `json:"p99_ns"`
	MaxLatency  time.Duration  `json:"max_latency_ns"`
	ErrorCount  int            `json:"error_count"`
	ErrorRate   float64        `json:"error_rate"`
	StatusCodes map[int]int    `json:"status_codes"`
	CapturedAt  time.Time      `json:"captured_at"`
}

// PerfComparison holds before/after Lighthouse results and per-metric diffs.
// Positive Diffs mean improvement; negative mean regression.
type PerfComparison struct {
	Before *LighthouseResult  `json:"before"`
	After  *LighthouseResult  `json:"after"`
	Diffs  map[string]float64 `json:"diffs"`
}

// ---------------------------------------------------------------------------
// Manager
// ---------------------------------------------------------------------------

// PerfManager runs performance and load tests and persists results.
type PerfManager struct {
	mu         sync.Mutex
	workDir    string
	resultsDir string // ~/yaver/perf/
}

// NewPerfManager creates a PerfManager rooted at workDir.
// Results are stored in ~/yaver/perf/.
func NewPerfManager(workDir string) *PerfManager {
	home, err := os.UserHomeDir()
	if err != nil {
		home = os.Getenv("HOME")
	}
	resultsDir := filepath.Join(home, "yaver", "perf")
	_ = os.MkdirAll(resultsDir, 0o755)
	return &PerfManager{
		workDir:    workDir,
		resultsDir: resultsDir,
	}
}

// ---------------------------------------------------------------------------
// Lighthouse
// ---------------------------------------------------------------------------

// Lighthouse runs a Lighthouse audit on url. device is "mobile" or "desktop"
// (defaults to "mobile"). The HTML report is saved to resultsDir.
func (pm *PerfManager) Lighthouse(url, device string) (*LighthouseResult, error) {
	if device == "" {
		device = "mobile"
	}
	if device != "mobile" && device != "desktop" {
		return nil, fmt.Errorf("device must be 'mobile' or 'desktop', got %q", device)
	}

	// Ensure lighthouse CLI is available.
	lhPath, err := osexec.LookPath("lighthouse")
	if err != nil {
		// Try to install globally.
		installCmd := osexec.Command("npm", "install", "-g", "lighthouse")
		installCmd.Stdout = os.Stdout
		installCmd.Stderr = os.Stderr
		if ierr := installCmd.Run(); ierr != nil {
			return nil, fmt.Errorf(
				"lighthouse not found and auto-install failed: %v\n"+
					"Install manually: npm install -g lighthouse",
				ierr,
			)
		}
		lhPath, err = osexec.LookPath("lighthouse")
		if err != nil {
			return nil, fmt.Errorf("lighthouse installed but still not found in PATH: %v", err)
		}
	}

	// Build output paths.
	slug := urlSlug(url)
	ts := time.Now()
	baseName := fmt.Sprintf("%s-%s", slug, ts.Format("20060102-150405"))
	jsonPath := filepath.Join(pm.resultsDir, baseName+".report.json")
	htmlPath := filepath.Join(pm.resultsDir, baseName+".report.html")

	formFactor := "mobile"
	if device == "desktop" {
		formFactor = "desktop"
	}

	args := []string{
		url,
		"--output=json,html",
		"--output-path=" + filepath.Join(pm.resultsDir, baseName),
		"--form-factor=" + formFactor,
		"--chrome-flags=--headless --no-sandbox --disable-dev-shm-usage",
		"--quiet",
	}
	if device == "desktop" {
		args = append(args, "--preset=desktop")
	}

	cmd := osexec.Command(lhPath, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("lighthouse failed: %v\noutput: %s", err, string(out))
	}

	// Read JSON report.
	jsonData, err := os.ReadFile(jsonPath)
	if err != nil {
		// Lighthouse sometimes appends ".report.json" to the path differently.
		alt := filepath.Join(pm.resultsDir, baseName+".report.json")
		jsonData, err = os.ReadFile(alt)
		if err != nil {
			return nil, fmt.Errorf("could not read lighthouse JSON report at %s: %v", jsonPath, err)
		}
	}

	result, err := parseLighthouseJSON(jsonData)
	if err != nil {
		return nil, fmt.Errorf("could not parse lighthouse output: %v", err)
	}
	result.URL = url
	result.ReportPath = htmlPath
	result.CapturedAt = ts

	// Persist for later comparison.
	if serr := pm.saveResult("lighthouse-"+slug, result); serr != nil {
		// Non-fatal — log but continue.
		fmt.Fprintf(os.Stderr, "perf_workspace: saveResult: %v\n", serr)
	}

	return result, nil
}

// ---------------------------------------------------------------------------
// Load test
// ---------------------------------------------------------------------------

// LoadTest runs a load test against url.
// It tries hey → k6 → ab, in that order.
// requests and concurrency are ignored when duration > 0 and the tool only
// supports duration-based runs (k6).
func (pm *PerfManager) LoadTest(url string, requests, concurrency int, duration time.Duration) (*LoadTestResult, error) {
	if requests <= 0 && duration <= 0 {
		return nil, fmt.Errorf("provide requests > 0 or duration > 0")
	}
	if concurrency <= 0 {
		concurrency = 10
	}

	ts := time.Now()

	// Try hey.
	if _, err := osexec.LookPath("hey"); err == nil {
		result, err := pm.runHey(url, requests, concurrency, duration)
		if err == nil {
			result.CapturedAt = ts
			_ = pm.saveResult("loadtest-"+urlSlug(url), result)
			return result, nil
		}
		fmt.Fprintf(os.Stderr, "perf_workspace: hey failed (%v), trying k6\n", err)
	}

	// Try k6.
	if _, err := osexec.LookPath("k6"); err == nil {
		result, err := pm.runK6(url, requests, concurrency, duration)
		if err == nil {
			result.CapturedAt = ts
			_ = pm.saveResult("loadtest-"+urlSlug(url), result)
			return result, nil
		}
		fmt.Fprintf(os.Stderr, "perf_workspace: k6 failed (%v), trying ab\n", err)
	}

	// Try ab (Apache Bench).
	if _, err := osexec.LookPath("ab"); err == nil {
		result, err := pm.runAB(url, requests, concurrency)
		if err == nil {
			result.CapturedAt = ts
			_ = pm.saveResult("loadtest-"+urlSlug(url), result)
			return result, nil
		}
		return nil, fmt.Errorf("ab failed: %v", err)
	}

	return nil, fmt.Errorf(
		"no load testing tool found\n" +
			"Install one of:\n" +
			"  hey:  brew install hey\n" +
			"  k6:   brew install k6\n" +
			"  ab:   brew install httpd",
	)
}

func (pm *PerfManager) runHey(url string, requests, concurrency int, duration time.Duration) (*LoadTestResult, error) {
	args := []string{"-c", strconv.Itoa(concurrency)}
	if duration > 0 {
		args = append(args, "-z", fmtDurationHey(duration))
	} else {
		args = append(args, "-n", strconv.Itoa(requests))
	}
	args = append(args, url)

	cmd := osexec.Command("hey", args...)
	start := time.Now()
	out, err := cmd.CombinedOutput()
	elapsed := time.Since(start)
	if err != nil {
		return nil, fmt.Errorf("hey: %v\noutput: %s", err, string(out))
	}
	result, err := parseHeyOutput(string(out))
	if err != nil {
		return nil, err
	}
	result.URL = url
	result.Concurrency = concurrency
	if result.Duration == 0 {
		result.Duration = elapsed
	}
	if requests > 0 && result.Requests == 0 {
		result.Requests = requests
	}
	return result, nil
}

func (pm *PerfManager) runK6(url string, requests, concurrency int, duration time.Duration) (*LoadTestResult, error) {
	if duration <= 0 {
		duration = 30 * time.Second
	}
	if concurrency <= 0 {
		concurrency = 10
	}

	// Build an inline k6 script.
	script := fmt.Sprintf(`
import http from 'k6/http';
import { sleep } from 'k6';

export let options = {
  vus: %d,
  duration: '%s',
};

export default function () {
  http.get('%s');
}
`, concurrency, fmtDurationK6(duration), url)

	scriptFile, err := os.CreateTemp("", "yaver-k6-*.js")
	if err != nil {
		return nil, fmt.Errorf("k6: create temp script: %v", err)
	}
	defer os.Remove(scriptFile.Name())

	if _, err = scriptFile.WriteString(script); err != nil {
		return nil, fmt.Errorf("k6: write script: %v", err)
	}
	scriptFile.Close()

	cmd := osexec.Command("k6", "run", "--summary-export=/dev/stdout", scriptFile.Name())
	start := time.Now()
	out, err := cmd.CombinedOutput()
	elapsed := time.Since(start)
	if err != nil {
		return nil, fmt.Errorf("k6: %v\noutput: %s", err, string(out))
	}
	result, err := parseK6Output(string(out))
	if err != nil {
		return nil, err
	}
	result.URL = url
	result.Concurrency = concurrency
	if result.Duration == 0 {
		result.Duration = elapsed
	}
	return result, nil
}

func (pm *PerfManager) runAB(url string, requests, concurrency int) (*LoadTestResult, error) {
	if requests <= 0 {
		requests = 1000
	}
	args := []string{
		"-n", strconv.Itoa(requests),
		"-c", strconv.Itoa(concurrency),
		"-k", // keep-alive
		url,
	}
	cmd := osexec.Command("ab", args...)
	start := time.Now()
	out, err := cmd.CombinedOutput()
	elapsed := time.Since(start)
	if err != nil {
		return nil, fmt.Errorf("ab: %v\noutput: %s", err, string(out))
	}
	result := parseABOutput(string(out))
	result.URL = url
	result.Requests = requests
	result.Concurrency = concurrency
	if result.Duration == 0 {
		result.Duration = elapsed
	}
	return result, nil
}

// ---------------------------------------------------------------------------
// Compare
// ---------------------------------------------------------------------------

// Compare runs a new Lighthouse audit and computes score deltas vs. the last
// saved result for the same URL. Positive diffs = improvement.
func (pm *PerfManager) Compare(url string) (*PerfComparison, error) {
	before, err := pm.loadLastResult(url)
	if err != nil {
		return nil, fmt.Errorf("no previous result for %s: %v", url, err)
	}

	after, err := pm.Lighthouse(url, "mobile")
	if err != nil {
		return nil, fmt.Errorf("lighthouse audit failed: %v", err)
	}

	diffs := map[string]float64{
		"performance":    after.Performance - before.Performance,
		"accessibility":  after.Accessibility - before.Accessibility,
		"best_practices": after.BestPractices - before.BestPractices,
		"seo":            after.SEO - before.SEO,
	}

	return &PerfComparison{Before: before, After: after, Diffs: diffs}, nil
}

// ---------------------------------------------------------------------------
// Report
// ---------------------------------------------------------------------------

// Report generates a human-readable summary of all recent perf results.
func (pm *PerfManager) Report() (string, error) {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	entries, err := os.ReadDir(pm.resultsDir)
	if err != nil {
		return "", fmt.Errorf("could not read results dir %s: %v", pm.resultsDir, err)
	}

	var lhFiles []string
	var ltFiles []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasPrefix(name, "lighthouse-") && strings.HasSuffix(name, ".json") {
			lhFiles = append(lhFiles, filepath.Join(pm.resultsDir, name))
		} else if strings.HasPrefix(name, "loadtest-") && strings.HasSuffix(name, ".json") {
			ltFiles = append(ltFiles, filepath.Join(pm.resultsDir, name))
		}
	}
	sort.Strings(lhFiles)
	sort.Strings(ltFiles)

	var sb strings.Builder
	sb.WriteString("═══════════════════════════════════════════════════\n")
	sb.WriteString("  Yaver Performance Report\n")
	sb.WriteString(fmt.Sprintf("  Generated: %s\n", time.Now().Format(time.RFC1123)))
	sb.WriteString("═══════════════════════════════════════════════════\n\n")

	if len(lhFiles) == 0 && len(ltFiles) == 0 {
		sb.WriteString("  No results found in " + pm.resultsDir + "\n")
		return sb.String(), nil
	}

	if len(lhFiles) > 0 {
		sb.WriteString("Lighthouse Audits\n")
		sb.WriteString(strings.Repeat("─", 51) + "\n")
		for _, f := range lhFiles {
			data, err := os.ReadFile(f)
			if err != nil {
				continue
			}
			var r LighthouseResult
			if err := json.Unmarshal(data, &r); err != nil {
				continue
			}
			sb.WriteString(pm.FormatLighthouse(&r))
			sb.WriteByte('\n')
		}
	}

	if len(ltFiles) > 0 {
		sb.WriteString("Load Tests\n")
		sb.WriteString(strings.Repeat("─", 51) + "\n")
		for _, f := range ltFiles {
			data, err := os.ReadFile(f)
			if err != nil {
				continue
			}
			var r LoadTestResult
			if err := json.Unmarshal(data, &r); err != nil {
				continue
			}
			sb.WriteString(pm.FormatLoadTest(&r))
			sb.WriteByte('\n')
		}
	}

	return sb.String(), nil
}

// ---------------------------------------------------------------------------
// Formatters
// ---------------------------------------------------------------------------

// FormatLighthouse formats a LighthouseResult as human-readable text.
func (pm *PerfManager) FormatLighthouse(r *LighthouseResult) string {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("Lighthouse Report: %s\n", r.URL))
	sb.WriteString(strings.Repeat("═", 44) + "\n")

	type scoreRow struct {
		label string
		score float64
	}
	rows := []scoreRow{
		{"Performance", r.Performance},
		{"Accessibility", r.Accessibility},
		{"Best Practices", r.BestPractices},
		{"SEO", r.SEO},
	}
	for _, row := range rows {
		bar := scoreBar(row.score)
		sb.WriteString(fmt.Sprintf("  %-16s %-3.0f %s\n", row.label, row.score*100, bar))
	}

	sb.WriteString("\n  Core Web Vitals:\n")

	type vitalRow struct {
		label    string
		value    string
		goodFn   func(string) bool
	}
	vitals := []vitalRow{
		{"FCP", r.FirstContentfulPaint, fcpGood},
		{"LCP", r.LargestContentfulPaint, lcpGood},
		{"TBT", r.TotalBlockingTime, tbtGood},
		{"CLS", r.CumulativeLayoutShift, clsGood},
		{"SI", r.SpeedIndex, siGood},
	}
	for _, v := range vitals {
		if v.value == "" {
			continue
		}
		rating := "✓ Good"
		if !v.goodFn(v.value) {
			rating = "✗ Needs Improvement"
		}
		sb.WriteString(fmt.Sprintf("    %-5s %-8s %s\n", v.label, v.value, rating))
	}

	if !r.CapturedAt.IsZero() {
		sb.WriteString(fmt.Sprintf("\n  Captured: %s\n", r.CapturedAt.Format(time.RFC1123)))
	}
	if r.ReportPath != "" {
		sb.WriteString(fmt.Sprintf("  Report:   %s\n", r.ReportPath))
	}

	return sb.String()
}

// FormatLoadTest formats a LoadTestResult as human-readable text.
func (pm *PerfManager) FormatLoadTest(r *LoadTestResult) string {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("Load Test: %s\n", r.URL))
	sb.WriteString(strings.Repeat("═", 43) + "\n")

	durationSec := r.Duration.Seconds()
	sb.WriteString(fmt.Sprintf("  Requests:    %d\n", r.Requests))
	sb.WriteString(fmt.Sprintf("  Concurrency: %d\n", r.Concurrency))
	sb.WriteString(fmt.Sprintf("  Duration:    %.1fs\n", durationSec))
	sb.WriteString(fmt.Sprintf("  RPS:         %.1f req/s\n", r.RPS))
	sb.WriteString("\n  Latency:\n")
	sb.WriteString(fmt.Sprintf("    Avg   %s\n", fmtLatency(r.AvgLatency)))
	sb.WriteString(fmt.Sprintf("    P50   %s\n", fmtLatency(r.P50)))
	sb.WriteString(fmt.Sprintf("    P95   %s\n", fmtLatency(r.P95)))
	sb.WriteString(fmt.Sprintf("    P99   %s\n", fmtLatency(r.P99)))
	sb.WriteString(fmt.Sprintf("    Max   %s\n", fmtLatency(r.MaxLatency)))

	if len(r.StatusCodes) > 0 {
		sb.WriteString("\n  Status Codes:\n")
		// Sorted keys for deterministic output.
		var codes []int
		for c := range r.StatusCodes {
			codes = append(codes, c)
		}
		sort.Ints(codes)
		total := r.Requests
		if total == 0 {
			for _, n := range r.StatusCodes {
				total += n
			}
		}
		for _, code := range codes {
			count := r.StatusCodes[code]
			pct := 0.0
			if total > 0 {
				pct = float64(count) / float64(total) * 100
			}
			sb.WriteString(fmt.Sprintf("    %d: %-6d (%.1f%%)\n", code, count, pct))
		}
	}

	if r.ErrorCount > 0 {
		sb.WriteString(fmt.Sprintf("\n  Errors: %d (%.2f%%)\n", r.ErrorCount, r.ErrorRate*100))
	}

	if !r.CapturedAt.IsZero() {
		sb.WriteString(fmt.Sprintf("\n  Captured: %s\n", r.CapturedAt.Format(time.RFC1123)))
	}

	return sb.String()
}

// ---------------------------------------------------------------------------
// Internal helpers
// ---------------------------------------------------------------------------

// parseLighthouseJSON extracts scores and Core Web Vitals from a Lighthouse
// JSON report (v10/v11 format).
func parseLighthouseJSON(data []byte) (*LighthouseResult, error) {
	var raw struct {
		Categories struct {
			Performance struct {
				Score float64 `json:"score"`
			} `json:"performance"`
			Accessibility struct {
				Score float64 `json:"score"`
			} `json:"accessibility"`
			BestPractices struct {
				Score float64 `json:"score"`
			} `json:"best-practices"`
			SEO struct {
				Score float64 `json:"score"`
			} `json:"seo"`
		} `json:"categories"`
		Audits struct {
			FCP struct {
				DisplayValue string `json:"displayValue"`
			} `json:"first-contentful-paint"`
			LCP struct {
				DisplayValue string `json:"displayValue"`
			} `json:"largest-contentful-paint"`
			TBT struct {
				DisplayValue string `json:"displayValue"`
			} `json:"total-blocking-time"`
			CLS struct {
				DisplayValue string `json:"displayValue"`
			} `json:"cumulative-layout-shift"`
			SI struct {
				DisplayValue string `json:"displayValue"`
			} `json:"speed-index"`
		} `json:"audits"`
	}

	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("json unmarshal: %v", err)
	}

	return &LighthouseResult{
		Performance:            raw.Categories.Performance.Score,
		Accessibility:          raw.Categories.Accessibility.Score,
		BestPractices:          raw.Categories.BestPractices.Score,
		SEO:                    raw.Categories.SEO.Score,
		FirstContentfulPaint:   raw.Audits.FCP.DisplayValue,
		LargestContentfulPaint: raw.Audits.LCP.DisplayValue,
		TotalBlockingTime:      raw.Audits.TBT.DisplayValue,
		CumulativeLayoutShift:  raw.Audits.CLS.DisplayValue,
		SpeedIndex:             raw.Audits.SI.DisplayValue,
	}, nil
}

// parseHeyOutput parses the text output of the hey load testing tool.
//
// Sample hey output (excerpt):
//
//	Summary:
//	  Total:        30.2371 secs
//	  Slowest:      1.2302 secs
//	  Fastest:      0.0431 secs
//	  Average:      0.1514 secs
//	  Requests/sec: 331.1000
//
//	  Total data:   ...
//	  Size/request: ...
//
//	Response time histogram:
//	  ...
//
//	Latency distribution:
//	  10% in 0.0523 secs
//	  50% in 0.1420 secs
//	  75% in 0.1900 secs
//	  95% in 0.2890 secs
//	  99% in 0.4450 secs
//
//	Status code distribution:
//	  [200] 9987 responses
//	  [500] 13 responses
func parseHeyOutput(output string) (*LoadTestResult, error) {
	result := &LoadTestResult{
		StatusCodes: make(map[int]int),
	}

	reFloat := func(pattern string) float64 {
		re := regexp.MustCompile(pattern)
		m := re.FindStringSubmatch(output)
		if len(m) < 2 {
			return 0
		}
		v, _ := strconv.ParseFloat(strings.TrimSpace(m[1]), 64)
		return v
	}
	reInt := func(pattern string) int {
		re := regexp.MustCompile(pattern)
		m := re.FindStringSubmatch(output)
		if len(m) < 2 {
			return 0
		}
		v, _ := strconv.Atoi(strings.TrimSpace(m[1]))
		return v
	}

	totalSec := reFloat(`Total:\s+([\d.]+)\s+secs`)
	result.Duration = secsToDuration(totalSec)
	result.RPS = reFloat(`Requests/sec:\s+([\d.]+)`)
	result.Requests = reInt(`\[200\]\s+(\d+)\s+responses`) // approximate; overwritten below
	avgSec := reFloat(`Average:\s+([\d.]+)\s+secs`)
	result.AvgLatency = secsToDuration(avgSec)
	maxSec := reFloat(`Slowest:\s+([\d.]+)\s+secs`)
	result.MaxLatency = secsToDuration(maxSec)

	// Latency percentiles.
	pctRe := regexp.MustCompile(`(\d+)%\s+in\s+([\d.]+)\s+secs`)
	for _, m := range pctRe.FindAllStringSubmatch(output, -1) {
		pct, _ := strconv.Atoi(m[1])
		val := secsToDuration(parseFloat(m[2]))
		switch pct {
		case 50:
			result.P50 = val
		case 95:
			result.P95 = val
		case 99:
			result.P99 = val
		}
	}

	// Status codes.
	scRe := regexp.MustCompile(`\[(\d+)\]\s+(\d+)\s+responses`)
	total := 0
	for _, m := range scRe.FindAllStringSubmatch(output, -1) {
		code, _ := strconv.Atoi(m[1])
		count, _ := strconv.Atoi(m[2])
		result.StatusCodes[code] = count
		total += count
		if code >= 400 {
			result.ErrorCount += count
		}
	}
	result.Requests = total
	if total > 0 {
		result.ErrorRate = float64(result.ErrorCount) / float64(total)
	}

	return result, nil
}

// parseK6Output parses the JSON summary that k6 writes to --summary-export.
//
// k6 summary export schema (partial):
//
//	{
//	  "metrics": {
//	    "http_reqs":           { "values": { "count": 9987, "rate": 331.1 } },
//	    "http_req_duration":   { "values": { "avg": 151.3, "med": 142.0, "p(95)": 289.0, "p(99)": 445.0, "max": 1200.0 } },
//	    "http_req_failed":     { "values": { "passes": 13, "rate": 0.001 } }
//	  }
//	}
func parseK6Output(output string) (*LoadTestResult, error) {
	result := &LoadTestResult{
		StatusCodes: make(map[int]int),
	}

	// k6 --summary-export writes JSON to stdout mixed with ANSI progress lines;
	// find the last '{' ... '}' block.
	start := strings.LastIndex(output, "{")
	end := strings.LastIndex(output, "}")
	if start < 0 || end <= start {
		return result, nil // best-effort; no JSON found
	}
	jsonStr := output[start : end+1]

	var raw struct {
		Metrics struct {
			HTTPReqs struct {
				Values struct {
					Count float64 `json:"count"`
					Rate  float64 `json:"rate"`
				} `json:"values"`
			} `json:"http_reqs"`
			HTTPReqDuration struct {
				Values struct {
					Avg float64 `json:"avg"`
					Med float64 `json:"med"`
					P95 float64 `json:"p(95)"`
					P99 float64 `json:"p(99)"`
					Max float64 `json:"max"`
				} `json:"values"`
			} `json:"http_req_duration"`
			HTTPReqFailed struct {
				Values struct {
					Passes float64 `json:"passes"`
					Rate   float64 `json:"rate"`
				} `json:"values"`
			} `json:"http_req_failed"`
		} `json:"metrics"`
	}
	if err := json.Unmarshal([]byte(jsonStr), &raw); err != nil {
		return result, nil // best-effort
	}

	result.Requests = int(math.Round(raw.Metrics.HTTPReqs.Values.Count))
	result.RPS = raw.Metrics.HTTPReqs.Values.Rate
	result.AvgLatency = msToDuration(raw.Metrics.HTTPReqDuration.Values.Avg)
	result.P50 = msToDuration(raw.Metrics.HTTPReqDuration.Values.Med)
	result.P95 = msToDuration(raw.Metrics.HTTPReqDuration.Values.P95)
	result.P99 = msToDuration(raw.Metrics.HTTPReqDuration.Values.P99)
	result.MaxLatency = msToDuration(raw.Metrics.HTTPReqDuration.Values.Max)
	result.ErrorCount = int(math.Round(raw.Metrics.HTTPReqFailed.Values.Passes))
	result.ErrorRate = raw.Metrics.HTTPReqFailed.Values.Rate

	return result, nil
}

// parseABOutput parses Apache Bench (ab) output.
func parseABOutput(output string) *LoadTestResult {
	result := &LoadTestResult{
		StatusCodes: make(map[int]int),
	}

	reFloat := func(pattern string) float64 {
		re := regexp.MustCompile(pattern)
		m := re.FindStringSubmatch(output)
		if len(m) < 2 {
			return 0
		}
		v, _ := strconv.ParseFloat(strings.TrimSpace(m[1]), 64)
		return v
	}
	reInt := func(pattern string) int {
		re := regexp.MustCompile(pattern)
		m := re.FindStringSubmatch(output)
		if len(m) < 2 {
			return 0
		}
		v, _ := strconv.Atoi(strings.TrimSpace(m[1]))
		return v
	}

	result.RPS = reFloat(`Requests per second:\s+([\d.]+)`)
	totalSec := reFloat(`Time taken for tests:\s+([\d.]+)\s+seconds`)
	result.Duration = secsToDuration(totalSec)
	result.AvgLatency = msToDuration(reFloat(`Time per request:\s+([\d.]+)\s+\[ms\] \(mean\)`))
	result.MaxLatency = msToDuration(reFloat(`Time per request:\s+([\d.]+)\s+\[ms\] \(mean, across all\)`))

	// ab percentile table: "  50      142"
	pctRe := regexp.MustCompile(`\s+(\d+)\s+(\d+)`)
	inPercentiles := false
	for _, line := range strings.Split(output, "\n") {
		if strings.Contains(line, "Percentage of the requests served within a certain time") {
			inPercentiles = true
			continue
		}
		if !inPercentiles {
			continue
		}
		m := pctRe.FindStringSubmatch(line)
		if len(m) < 3 {
			continue
		}
		pct, _ := strconv.Atoi(m[1])
		ms, _ := strconv.ParseFloat(m[2], 64)
		val := msToDuration(ms)
		switch pct {
		case 50:
			result.P50 = val
		case 95:
			result.P95 = val
		case 99:
			result.P99 = val
		case 100:
			result.MaxLatency = val
		}
	}

	fail := reInt(`Failed requests:\s+(\d+)`)
	result.ErrorCount = fail
	total := reInt(`Complete requests:\s+(\d+)`)
	result.Requests = total
	if total > 0 {
		result.ErrorRate = float64(fail) / float64(total)
		result.StatusCodes[200] = total - fail
		if fail > 0 {
			result.StatusCodes[500] = fail
		}
	}

	return result
}

// saveResult persists data as JSON in resultsDir/<name>-<timestamp>.json.
func (pm *PerfManager) saveResult(name string, data interface{}) error {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	fileName := fmt.Sprintf("%s-%s.json", name, time.Now().Format("20060102-150405"))
	path := filepath.Join(pm.resultsDir, fileName)

	b, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal: %v", err)
	}
	return os.WriteFile(path, b, 0o644)
}

// loadLastResult loads the most recent LighthouseResult for the given URL from
// resultsDir. Files are named lighthouse-<slug>-<timestamp>.json.
func (pm *PerfManager) loadLastResult(url string) (*LighthouseResult, error) {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	slug := urlSlug(url)
	prefix := "lighthouse-" + slug + "-"

	entries, err := os.ReadDir(pm.resultsDir)
	if err != nil {
		return nil, fmt.Errorf("read dir: %v", err)
	}

	var candidates []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if strings.HasPrefix(e.Name(), prefix) && strings.HasSuffix(e.Name(), ".json") {
			candidates = append(candidates, e.Name())
		}
	}
	if len(candidates) == 0 {
		return nil, fmt.Errorf("no saved lighthouse results for %s", url)
	}
	sort.Strings(candidates)
	last := candidates[len(candidates)-1]

	data, err := os.ReadFile(filepath.Join(pm.resultsDir, last))
	if err != nil {
		return nil, fmt.Errorf("read file: %v", err)
	}

	var r LighthouseResult
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("unmarshal: %v", err)
	}
	return &r, nil
}

// ---------------------------------------------------------------------------
// Utility functions
// ---------------------------------------------------------------------------

// urlSlug converts a URL into a filesystem-safe slug.
func urlSlug(url string) string {
	r := strings.NewReplacer(
		"https://", "",
		"http://", "",
		"/", "-",
		":", "-",
		".", "-",
		"?", "-",
		"&", "-",
		"=", "-",
		"#", "-",
	)
	s := r.Replace(url)
	// Strip trailing dashes.
	s = strings.Trim(s, "-")
	if len(s) > 64 {
		s = s[:64]
	}
	return s
}

// scoreBar returns a 22-char progress bar for a score in [0,1].
func scoreBar(score float64) string {
	const total = 22
	filled := int(math.Round(score * total))
	if filled < 0 {
		filled = 0
	}
	if filled > total {
		filled = total
	}
	return strings.Repeat("█", filled) + strings.Repeat("░", total-filled)
}

// fmtLatency formats a duration as a short latency string (ms or s).
func fmtLatency(d time.Duration) string {
	if d >= time.Second {
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
	return fmt.Sprintf("%dms", d.Milliseconds())
}

// secsToDuration converts a float64 seconds value to time.Duration.
func secsToDuration(s float64) time.Duration {
	return time.Duration(s * float64(time.Second))
}

// msToDuration converts a float64 milliseconds value to time.Duration.
func msToDuration(ms float64) time.Duration {
	return time.Duration(ms * float64(time.Millisecond))
}

// parseFloat is a thin wrapper around strconv.ParseFloat that ignores errors.
func parseFloat(s string) float64 {
	v, _ := strconv.ParseFloat(strings.TrimSpace(s), 64)
	return v
}

// fmtDurationHey formats a duration for the hey -z flag (e.g. "30s", "2m").
func fmtDurationHey(d time.Duration) string {
	if d >= time.Minute {
		return fmt.Sprintf("%.0fm", d.Minutes())
	}
	return fmt.Sprintf("%.0fs", d.Seconds())
}

// fmtDurationK6 formats a duration for k6 options.duration (e.g. "30s", "2m0s").
func fmtDurationK6(d time.Duration) string {
	if d >= time.Minute {
		mins := int(d.Minutes())
		secs := int(d.Seconds()) % 60
		if secs == 0 {
			return fmt.Sprintf("%dm", mins)
		}
		return fmt.Sprintf("%dm%ds", mins, secs)
	}
	return fmt.Sprintf("%.0fs", d.Seconds())
}

// ---------------------------------------------------------------------------
// Core Web Vitals thresholds (Google "Good" tier)
// ---------------------------------------------------------------------------

// fcpGood returns true if FCP value string is within Google's "Good" threshold (< 1.8s).
func fcpGood(v string) bool  { return vitalLTE(v, 1800) }

// lcpGood returns true if LCP value string is within Google's "Good" threshold (< 2.5s).
func lcpGood(v string) bool  { return vitalLTE(v, 2500) }

// tbtGood returns true if TBT value string is within Google's "Good" threshold (< 200ms).
func tbtGood(v string) bool  { return vitalLTE(v, 200) }

// clsGood returns true if CLS value string is within Google's "Good" threshold (< 0.1).
func clsGood(v string) bool {
	// CLS is unitless (e.g. "0.02").
	v = strings.TrimSpace(v)
	val, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return false
	}
	return val <= 0.1
}

// siGood returns true if Speed Index is within Google's "Good" threshold (< 3.4s).
func siGood(v string) bool { return vitalLTE(v, 3400) }

// vitalLTE parses a Core Web Vital display value (e.g. "1.2 s", "320 ms") and
// returns true if the value is <= thresholdMs milliseconds.
func vitalLTE(v string, thresholdMs float64) bool {
	v = strings.TrimSpace(v)
	if v == "" {
		return false
	}
	v = strings.ToLower(v)
	// Strip non-numeric suffix characters.
	numRe := regexp.MustCompile(`^([\d.]+)\s*(.*)$`)
	m := numRe.FindStringSubmatch(v)
	if len(m) < 3 {
		return false
	}
	val, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		return false
	}
	unit := strings.TrimSpace(m[2])
	switch {
	case strings.HasPrefix(unit, "s"):
		val *= 1000 // seconds → ms
	case strings.HasPrefix(unit, "ms"):
		// already ms
	default:
		// Assume seconds (Lighthouse default for FCP/LCP/SI).
		val *= 1000
	}
	return val <= thresholdMs
}
