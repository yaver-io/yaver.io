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
// and the implementer (default opencode) executes them. The
// HybridReport is folded into the AIResponse contract so the rest of
// the loop machinery doesn't care which engine ran.
func spawnHybrid(ctx context.Context, l *LoopState, workDir string, report *HeuristicReport, reportPath string, nudge string) (*AIResponse, error) {
	prompt, err := buildLoopPrompt(l, workDir, report, nudge)
	if err != nil {
		return nil, err
	}

	// Per-tier agent×model layering. The user can compose any
	// combination via --planner / --implementer (env-injected by
	// autodev_cmd) so e.g. "Opus plans, Codex implements" is one
	// flag set away. Empty env → applyHybridDefaults picks
	// "claude planner + opencode implementer".
	plannerSpec := strings.TrimSpace(os.Getenv("YAVER_HYBRID_PLANNER"))
	implementerSpec := strings.TrimSpace(os.Getenv("YAVER_HYBRID_IMPLEMENTER"))
	plannerAgent, _ := splitAgentSpec(plannerSpec)
	implAgent, implModel := splitAgentSpec(implementerSpec)

	spec := HybridSpec{
		WorkDir:     workDir,
		Prompt:      prompt,
		Planner:     plannerAgent,
		Implementer: implAgent,
		Model:       firstNonEmpty(implModel, l.Spec.Think.Model),
		// One kick = one small chunk of work; the loop's repetition
		// does the long-horizon planning, not a single planner call.
		MaxSubtasks: 5,
	}

	fmt.Fprintf(os.Stderr, "[loop %s] hybrid kick — planner=%s implementer=%s (max %d subtasks)\n",
		l.Spec.Name, defaultStr(spec.Planner, "claude"),
		defaultStr(spec.Implementer, "opencode"), spec.MaxSubtasks)

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

// splitAgentSpec parses "agent[:model]" into (agent, model). Model
// aliases (sonnet/opus/haiku) are expanded to current generation
// claude model ids; other strings pass through verbatim so users
// can pass full ids ("claude-opus-4-6") or any opencode-compatible
// model id ("anthropic/claude-sonnet-4-6", "openrouter/...").
func splitAgentSpec(spec string) (agent, model string) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return "", ""
	}
	parts := strings.SplitN(spec, ":", 2)
	agent = strings.ToLower(parts[0])
	if len(parts) == 1 {
		return agent, ""
	}
	model = parts[1]
	if agent == "claude" || agent == "claude-code" {
		switch strings.ToLower(model) {
		case "sonnet":
			model = "claude-sonnet-4-6"
		case "opus":
			model = "claude-opus-4-6"
		case "haiku":
			model = "claude-haiku-4-5"
		}
	}
	return agent, model
}


