package main

// MCP tool dispatch for the embedded yaver-test-sdk runner. Lets any AI
// the dev is already using (Claude Code, Cursor, Aider, Codex, Ollama
// front-ends) drive local CI without leaving the editor — same agent
// process, same data, no central server in the loop.

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/yaver-io/agent/testkit"
)

func (s *HTTPServer) mcpTestkitListSpecs(raw json.RawMessage) interface{} {
	var args struct {
		Root string `json:"root"`
	}
	_ = json.Unmarshal(raw, &args)
	root, err := mcpResolveRoot(args.Root)
	if err != nil {
		return mcpToolError(err.Error())
	}
	specs, err := testkit.DiscoverSpecs(root)
	if err != nil {
		return mcpToolError(err.Error())
	}
	if len(specs) == 0 {
		return mcpToolResult(fmt.Sprintf("No specs found under %s. Create %s/example.test.yaml to start.", root, root))
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Specs in %s:\n", root))
	for _, sp := range specs {
		sb.WriteString(fmt.Sprintf("  - %s [%s] %d step(s) %s\n", sp.Name, sp.Target, len(sp.Steps), sp.URL))
	}
	return mcpToolResult(sb.String())
}

func (s *HTTPServer) mcpTestkitRun(raw json.RawMessage) interface{} {
	var args struct {
		Root        string `json:"root"`
		Only        string `json:"only"`
		Concurrency int    `json:"concurrency"`
		Retries     int    `json:"retries"`
		Headful     bool   `json:"headful"`
	}
	_ = json.Unmarshal(raw, &args)
	root, err := mcpResolveRoot(args.Root)
	if err != nil {
		return mcpToolError(err.Error())
	}
	specs, err := testkit.DiscoverSpecs(root)
	if err != nil {
		return mcpToolError(err.Error())
	}
	if args.Only != "" {
		filtered := specs[:0]
		for _, sp := range specs {
			if sp.Name == args.Only {
				filtered = append(filtered, sp)
			}
		}
		specs = filtered
		if len(specs) == 0 {
			return mcpToolError("no spec named " + args.Only)
		}
	}
	if args.Concurrency < 1 {
		args.Concurrency = 1
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	opts := testkit.RunOptions{
		Headful:      args.Headful,
		FlakeRetries: args.Retries,
	}
	suite := testkit.RunSuite(ctx, specs, opts, args.Concurrency)

	// Append to local history. Never sent anywhere.
	hist := &testkit.History{Path: testkit.HistoryPathFor(root)}
	_ = hist.AppendSuite(suite, "", "", runtime.GOOS)

	total, passed, failed := suite.Counts()
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("yaver-test-sdk: %d total, %d passed, %d failed (%s)\n",
		total, passed, failed, suite.FinishedAt.Sub(suite.StartedAt).Round(time.Millisecond)))
	for _, r := range suite.Results {
		if r == nil {
			continue
		}
		mark := "OK"
		if !r.Passed {
			mark = "FAIL"
		}
		sb.WriteString(fmt.Sprintf("  [%s] %s (%s)\n", mark, r.Spec.Name, r.Duration().Round(time.Millisecond)))
		if r.Err != nil {
			sb.WriteString(fmt.Sprintf("        error: %s\n", r.Err.Error()))
		}
		for _, st := range r.Steps {
			if st.Err != nil {
				sb.WriteString(fmt.Sprintf("        step %d (%s): %s\n", st.Index, st.Description, st.Err.Error()))
				if st.ScreenshotPath != "" {
					sb.WriteString(fmt.Sprintf("          screenshot: %s\n", st.ScreenshotPath))
				}
			}
		}
	}
	return mcpToolResult(sb.String())
}

func (s *HTTPServer) mcpTestkitLastFailure(raw json.RawMessage) interface{} {
	var args struct {
		Root string `json:"root"`
	}
	_ = json.Unmarshal(raw, &args)
	root, err := mcpResolveRoot(args.Root)
	if err != nil {
		return mcpToolError(err.Error())
	}
	hist := &testkit.History{Path: testkit.HistoryPathFor(root)}
	entries, err := hist.Tail(50)
	if err != nil {
		return mcpToolError(err.Error())
	}
	for i := len(entries) - 1; i >= 0; i-- {
		e := entries[i]
		if e.Failed == 0 {
			continue
		}
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("Most recent failed run: %s\n", e.StartedAt.Format(time.RFC3339)))
		if e.GitBranch != "" {
			sb.WriteString(fmt.Sprintf("Branch: %s\n", e.GitBranch))
		}
		sb.WriteString(fmt.Sprintf("Total: %d, passed: %d, failed: %d\n\n", e.Total, e.Passed, e.Failed))
		for _, sp := range e.Specs {
			if sp.Passed {
				continue
			}
			sb.WriteString(fmt.Sprintf("- %s (%s, %d ms)\n", sp.Name, sp.Path, sp.DurationMS))
			if sp.Error != "" {
				sb.WriteString(fmt.Sprintf("  error: %s\n", sp.Error))
			}
		}
		return mcpToolResult(sb.String())
	}
	return mcpToolResult("No failed runs in local history.")
}

func (s *HTTPServer) mcpTestkitFlakeReport(raw json.RawMessage) interface{} {
	var args struct {
		Root string `json:"root"`
	}
	_ = json.Unmarshal(raw, &args)
	root, err := mcpResolveRoot(args.Root)
	if err != nil {
		return mcpToolError(err.Error())
	}
	hist := &testkit.History{Path: testkit.HistoryPathFor(root)}
	stats, err := hist.FlakeReport(100)
	if err != nil {
		return mcpToolError(err.Error())
	}
	if len(stats) == 0 {
		return mcpToolResult("No history yet.")
	}
	var sb strings.Builder
	sb.WriteString("Flake report (last 100 runs):\n")
	for _, st := range stats {
		sb.WriteString(fmt.Sprintf("  %s: %d/%d passed, %d flaky (%.1f%% failure)\n",
			st.Name, st.Passed, st.Total, st.Flaky, st.FailureRatio()*100))
	}
	return mcpToolResult(sb.String())
}

// mcpTestkitSelfHealSelector is the AI self-healing primitive. Given a
// failing CSS selector and the current page HTML, ask the user's
// configured LLM to propose a replacement selector. Returns the raw
// LLM output for the autonomous loop to parse.
func (s *HTTPServer) mcpTestkitSelfHealSelector(raw json.RawMessage) interface{} {
	var args struct {
		FailedSelector string `json:"failed_selector"`
		DOMHTML        string `json:"dom_html"`
		Intent         string `json:"intent"`
	}
	_ = json.Unmarshal(raw, &args)
	if args.FailedSelector == "" || args.DOMHTML == "" {
		return mcpToolError("failed_selector and dom_html are required")
	}
	suggestion, err := testkit.SelfHealSelector(context.Background(), testkit.SelfHealRequest{
		FailedSelector: args.FailedSelector,
		DOMHTML:        args.DOMHTML,
		Intent:         args.Intent,
	})
	if err != nil {
		return mcpToolError(err.Error())
	}
	return mcpToolResult(fmt.Sprintf("Suggested selector: %s\n\nReasoning:\n%s", suggestion.Selector, suggestion.Reasoning))
}

func mcpResolveRoot(root string) (string, error) {
	if root == "" {
		root = "yaver-tests"
	}
	return filepath.Abs(root)
}
