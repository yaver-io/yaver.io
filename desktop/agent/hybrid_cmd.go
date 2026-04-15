package main

// hybrid_cmd.go — `yaver hybrid` CLI entry point.
//
// Mirrors hybrid_http.go's surface so the CLI and mobile/web clients
// behave identically. Prefer the HTTP endpoint for long-running runs
// (SSE progress is still TODO); the CLI is the quickest way to try
// planner/implementer pairs on a laptop.

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

func runHybrid(args []string) {
	fs := flag.NewFlagSet("hybrid", flag.ExitOnError)
	var (
		planner     = fs.String("planner", "claude", "planner runner ID (claude | codex | opencode | ...)")
		implementer = fs.String("implementer", "aider-ollama", "implementer runner ID (aider-ollama | aider | ...)")
		model       = fs.String("model", "", "override implementer model (e.g. ollama_chat/qwen2.5-coder:14b)")
		baseURL     = fs.String("base-url", "", "override implementer base URL (e.g. http://host:11434)")
		workDir     = fs.String("workdir", "", "project root (default: cwd)")
		maxSubs     = fs.Int("max-subtasks", 20, "cap on subtasks the planner is allowed to emit")
		timeout     = fs.Duration("timeout", 30*time.Minute, "overall run timeout")
		jsonOut     = fs.Bool("json", false, "emit the full HybridReport as JSON")
		checkOnly   = fs.Bool("check", false, "run preflight only (aider + ollama + model) and exit")
	)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, `Usage:
  yaver hybrid [flags] "<feature prompt>"

Plans with an expensive frontier model, implements with a cheap local one.
The planner is asked to break the request into narrow, file-scoped subtasks
the implementer can finish in one edit pass.

Flags:`)
		fs.PrintDefaults()
		fmt.Fprintln(os.Stderr, `
Example:
  yaver hybrid --planner claude --implementer aider-ollama \
    --model ollama_chat/qwen2.5-coder:14b \
    "Add a Convex mutation to create a portfolio with starting cash"`)
	}
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}
	if *checkOnly {
		m := *model
		if m == "" {
			m = "ollama_chat/qwen2.5-coder:14b"
		}
		b := *baseURL
		if b == "" {
			b = "http://127.0.0.1:11434"
		}
		pf := checkHybrid(*implementer, m, b)
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(pf)
		if !pf.AllOK() {
			os.Exit(1)
		}
		return
	}
	if fs.NArg() == 0 {
		fs.Usage()
		os.Exit(2)
	}
	prompt := ""
	for i, a := range fs.Args() {
		if i > 0 {
			prompt += " "
		}
		prompt += a
	}

	wd := *workDir
	if wd == "" {
		if cwd, err := os.Getwd(); err == nil {
			wd = cwd
		}
	}
	wd, _ = filepath.Abs(wd)

	spec := HybridSpec{
		Planner:     *planner,
		Implementer: *implementer,
		Model:       *model,
		BaseURL:     *baseURL,
		WorkDir:     wd,
		Prompt:      prompt,
		MaxSubtasks: *maxSubs,
		Timeout:     *timeout,
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	fmt.Fprintf(os.Stderr, "[hybrid] planner=%s implementer=%s model=%s workdir=%s\n",
		spec.Planner, spec.Implementer, spec.Model, spec.WorkDir)
	fmt.Fprintf(os.Stderr, "[hybrid] planning…\n")

	rep, err := RunHybrid(ctx, spec)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[hybrid] FAILED: %v\n", err)
		if *jsonOut && rep != nil {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			_ = enc.Encode(rep)
		}
		os.Exit(1)
	}

	if *jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(rep)
		return
	}

	fmt.Printf("\nPlan: %d subtask(s)\n", len(rep.Subtasks))
	for i, st := range rep.Subtasks {
		fmt.Printf("  %2d. %s  [%v]\n", i+1, st.Title, st.Files)
	}
	fmt.Printf("\nResults:\n")
	for i, r := range rep.Results {
		marker := "ok"
		if r.Status != "ok" {
			marker = r.Status
		}
		fmt.Printf("  %2d. [%s] %s  (%s)\n", i+1, marker, r.Subtask.Title, r.Duration.Round(time.Millisecond))
		if r.Error != "" {
			fmt.Printf("      error: %s\n", truncOneLine(r.Error, 200))
		}
	}
	fmt.Printf("\nSummary: %d ok, %d failed (total %s)\n",
		len(rep.Results)-rep.FailedSteps, rep.FailedSteps,
		rep.FinishedAt.Sub(rep.StartedAt).Round(time.Second))
	if !rep.OK {
		os.Exit(1)
	}
}

// truncOneLine collapses s to a single line capped at max chars.
// Named with a hybrid-specific prefix to avoid colliding with
// `firstLine` in doctor_ci.go which has a different signature.
func truncOneLine(s string, max int) string {
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			s = s[:i]
			break
		}
	}
	if len(s) > max {
		s = s[:max] + "…"
	}
	return s
}
