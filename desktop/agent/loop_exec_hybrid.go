package main

// loop_exec_hybrid.go — runner adapter that lets `yaver autodev`
// (and `yaver loop`) drive a hybrid planner+local-implementer pass on
// every kick instead of a single frontier-model spawn. Slots into
// phaseThink's runner switch as the "hybrid" case.

import (
	"context"
	"fmt"
	"os"
	"strings"
)

// spawnHybrid runs one hybrid pass for the current kick: a frontier
// planner decomposes the kick prompt into small file-scoped subtasks
// and the local implementer (default aider+ollama) executes them.
// The HybridReport is folded into the AIResponse contract so the rest
// of the loop machinery doesn't care which engine ran.
func spawnHybrid(ctx context.Context, l *LoopState, workDir string, report *HeuristicReport, reportPath string, nudge string) (*AIResponse, error) {
	prompt, err := buildLoopPrompt(l, workDir, report, nudge)
	if err != nil {
		return nil, err
	}

	spec := HybridSpec{
		WorkDir: workDir,
		Prompt:  prompt,
		// Planner + Implementer left empty → applyHybridDefaults picks
		// "claude" + "aider-ollama". Model/BaseURL inherit from the
		// loop spec when set so users keep the same overrides they
		// already use for `aider-ollama` runner kicks.
		Model:   l.Spec.Think.Model,
		BaseURL: l.Spec.Think.BaseURL,
		// One kick = one small chunk of work; the loop's repetition
		// does the long-horizon planning, not a single planner call.
		MaxSubtasks: 5,
	}

	fmt.Fprintf(os.Stderr, "[loop %s] hybrid kick — planner=%s implementer=%s (max %d subtasks)\n",
		l.Spec.Name, defaultStr(spec.Planner, "claude"),
		defaultStr(spec.Implementer, "aider-ollama"), spec.MaxSubtasks)

	rep, err := RunHybrid(ctx, spec)
	if err != nil {
		// Hard failure (planner unreachable, etc.) — bubble up. The
		// loop will surface this in the kick log.
		return nil, fmt.Errorf("hybrid: %w", err)
	}
	return hybridReportToAIResponse(rep), nil
}

func hybridReportToAIResponse(rep *HybridReport) *AIResponse {
	if rep == nil {
		return &AIResponse{Status: "stuck", Summary: "hybrid: no report"}
	}

	// Planner produced nothing executable.
	if len(rep.Subtasks) == 0 {
		summary := "hybrid: planner returned no subtasks"
		if rep.PlanError != "" {
			summary += " — " + truncateLine(rep.PlanError, 240)
		}
		return &AIResponse{Status: "needs_human", Summary: summary}
	}

	okCount := 0
	failCount := 0
	titles := []string{}
	files := []string{}
	seenFile := map[string]bool{}
	blockers := []string{}
	for _, r := range rep.Results {
		switch r.Status {
		case "ok":
			okCount++
			titles = append(titles, r.Subtask.Title)
			for _, f := range r.Subtask.Files {
				if !seenFile[f] {
					seenFile[f] = true
					files = append(files, f)
				}
			}
		case "error":
			failCount++
			blockers = append(blockers, fmt.Sprintf("%s: %s",
				r.Subtask.Title, truncateLine(r.Error, 160)))
		}
	}

	resp := &AIResponse{
		FilesTouched: files,
		Blockers:     blockers,
	}
	switch {
	case okCount == 0:
		resp.Status = "stuck"
		resp.Summary = fmt.Sprintf("hybrid: %d subtasks, all failed", failCount)
	case failCount == 0:
		resp.Status = "in_progress"
		resp.Summary = fmt.Sprintf("hybrid: %d subtasks completed (%s)",
			okCount, summariseTitles(titles, 3))
	default:
		resp.Status = "in_progress"
		resp.Summary = fmt.Sprintf("hybrid: %d/%d subtasks completed, %d failed (%s)",
			okCount, okCount+failCount, failCount, summariseTitles(titles, 3))
	}
	return resp
}

func summariseTitles(titles []string, max int) string {
	if len(titles) == 0 {
		return ""
	}
	if len(titles) <= max {
		return strings.Join(titles, "; ")
	}
	return strings.Join(titles[:max], "; ") + fmt.Sprintf("; +%d more", len(titles)-max)
}

func truncateLine(s string, max int) string {
	s = strings.TrimSpace(s)
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}

func defaultStr(v, fallback string) string {
	if strings.TrimSpace(v) == "" {
		return fallback
	}
	return v
}

