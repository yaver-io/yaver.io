package main

// task_fork.go — POST /tasks/{id}/fork
//
// Lets a user change the coding agent (claude / codex / opencode + mode)
// from inside an active chat by creating a NEW child task with bounded
// recent-context handoff, while leaving the parent task immutable.
//
// Why a fork instead of in-place runner switch:
//   - continueTask is runner-session specific. Claude/Codex/OpenCode
//     don't share a session format; their resume semantics, output
//     rendering, and analytics all assume one runner per task.
//   - the existing transfer / fork primitives already serialise
//     turns + result text + workdir, so reusing them keeps web/mobile
//     parity guaranteed.
//
// Phase 1 only: server-side primitive + tests. Web (Phase 2) and mobile
// (Phase 3) wiring lands in follow-up commits.
//
// See CODING_AGENT_CHANGE_FROM_MOBILE_APP_CHAT.md for the full design.

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"
)

// taskForkRequest is the wire format for POST /tasks/{id}/fork.
type taskForkRequest struct {
	Runner       string `json:"runner"`                 // claude | codex | opencode (required)
	Model        string `json:"model,omitempty"`        // empty = runner default
	Mode         string `json:"mode,omitempty"`         // opencode mode: build | plan | <custom> | "" = defaultAgent
	Input        string `json:"input"`                  // user's new message (required)
	ContextWords int    `json:"contextWords,omitempty"` // word budget for recent-context handoff (default 1200)
}

// taskForkResponse is the wire format we return.
type taskForkResponse struct {
	OK               bool   `json:"ok"`
	TaskID           string `json:"taskId"`
	RunnerID         string `json:"runnerId"`
	Status           string `json:"status"`
	ParentTaskID     string `json:"parentTaskId"`
	Relationship     string `json:"relationship"`
	ContextWordsUsed int    `json:"contextWordsUsed"`
}

const (
	// defaultForkContextWords is the recent-context word budget when the
	// caller doesn't supply one. ~1200 words ≈ 1500 tokens, fits inside
	// every supported runner's prompt headroom while still carrying the
	// last few turns + the latest assistant tail.
	defaultForkContextWords = 1200
	// minForkContextWords / maxForkContextWords clamp the caller's
	// budget. Anything <100 truncates to noise; anything >5000 risks
	// OpenCode's 8k-token limit.
	minForkContextWords = 100
	maxForkContextWords = 5000
)

// handleTaskFork serves POST /tasks/{id}/fork. Wired in by handleTaskByID.
func (s *HTTPServer) handleTaskFork(w http.ResponseWriter, r *http.Request, parentID string) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}
	parent, ok := s.taskMgr.GetTask(parentID)
	if !ok || parent == nil {
		jsonError(w, http.StatusNotFound, "parent task not found")
		return
	}

	var req taskForkRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	req.Runner = normalizeRunnerID(strings.TrimSpace(req.Runner))
	req.Input = strings.TrimSpace(req.Input)
	req.Mode = strings.TrimSpace(req.Mode)
	req.Model = strings.TrimSpace(req.Model)
	if req.Runner == "" {
		jsonError(w, http.StatusBadRequest, "runner is required (claude | codex | opencode)")
		return
	}
	if req.Input == "" {
		jsonError(w, http.StatusBadRequest, "input is required")
		return
	}
	// Reject unknown runners early. Mode is only meaningful for opencode;
	// it's silently ignored for claude/codex (matches existing /tasks
	// + /vibing/execute behaviour).
	if !isSupportedForkRunner(req.Runner) {
		jsonError(w, http.StatusBadRequest, fmt.Sprintf("unsupported runner %q (use claude, codex, or opencode)", req.Runner))
		return
	}

	contextWords := req.ContextWords
	if contextWords <= 0 {
		contextWords = defaultForkContextWords
	}
	if contextWords < minForkContextWords {
		contextWords = minForkContextWords
	}
	if contextWords > maxForkContextWords {
		contextWords = maxForkContextWords
	}

	// Build the bounded handoff prompt. Reuses the same compact-context
	// renderer the CLI's `yaver code fork` flow uses (turn-count gated)
	// but also clips to a word budget — the audit doc's primary lever
	// for keeping the new agent's context window safe.
	handoff := buildForkHandoffPrompt(parent, req, contextWords)
	wordsUsed := countWords(handoff)

	// Forward parent's workdir + project context so the new runner
	// operates on the same code. Without this the new task's workdir
	// would be the agent's global cwd which is rarely the right answer
	// for runner switches.
	taskOpts := TaskCreateOptions{
		WorkDir:           parent.WorkDir,
		InitialUserPrompt: req.Input,
		Mode:              req.Mode,
		// Carry the parent's conversation into the child for DISPLAY only, so
		// the fork renders as one continuous thread on every surface instead of
		// an orphaned single exchange. The runner still gets its context from
		// the bounded handoff prompt above — SeedTurns never reaches the runner.
		SeedTurns: parent.Turns,
	}

	// Inherit guest scoping if the parent was a guest task. A guest
	// switching agents must stay scoped to their allowed runners +
	// projects — handled by CreateTaskWithOptions which validates
	// taskOpts.GuestUserID against guestConfigMgr.
	if parent.GuestUserID != "" {
		taskOpts.GuestUserID = parent.GuestUserID
		taskOpts.GuestUseHostAPIKeys = parent.GuestUseHostAPIKeys
		taskOpts.GuestRequireIsolation = parent.GuestRequireIsolation
		taskOpts.GuestAllowGuestProvidedKeys = parent.GuestAllowGuestProvidedKeys
		taskOpts.GuestCPULimitPercent = parent.GuestCPULimitPercent
		taskOpts.GuestRAMLimitMB = parent.GuestRAMLimitMB
	}

	if parent.GuestUserID == "" {
		meta := taskPlacementRequestFromTaskBody(taskPlacementRequestInput{
			KindHint:       "unknown",
			Title:          "Fork task " + parent.ID,
			Source:         "runner-switch-fork",
			Runner:         req.Runner,
			WorkDir:        firstNonEmpty(parent.WorkDir, s.taskMgr.workDir),
			TargetDeviceID: s.deviceID,
		})
		if previewPlacement, perr := s.previewTaskPlacement(r.Context(), meta); perr != nil {
			log.Printf("[placement] fork preview skipped before task create: %v", perr)
		} else if shouldDeferLocalTaskForPlacement(previewPlacement, s.deviceID) {
			pendingTaskID := newPendingCloudTaskID()
			recordedPlacement := previewPlacement
			if placement, rerr := s.recordTaskPlacement(r.Context(), pendingTaskID, meta); rerr != nil {
				log.Printf("[placement] fork pending record skipped for %s: %v", pendingTaskID, rerr)
			} else if placement != nil {
				recordedPlacement = placement
			}
			var activation map[string]any
			if recordedPlacement != nil && (recordedPlacement.PlacementID != "" || pendingTaskID != "") {
				if result, aerr := s.activateTaskPlacement(r.Context(), recordedPlacement.PlacementID, pendingTaskID); aerr != nil {
					activation = activationMapFromError(aerr)
					log.Printf("[placement] fork activation skipped for %s: %v", pendingTaskID, aerr)
				} else {
					activation = result
				}
			}
			bodyJSON, _ := json.Marshal(map[string]any{
				"title":             handoff,
				"description":       "forked from " + parent.ID + " (runner switch)",
				"source":            "runner-switch-fork",
				"runner":            req.Runner,
				"model":             req.Model,
				"mode":              req.Mode,
				"workDir":           taskOpts.WorkDir,
				"userPrompt":        req.Input,
				"initialUserPrompt": req.Input,
				"placementKind":     meta.Kind,
			})
			cloudErr := &CloudWorkspaceRequiredError{
				PendingTaskID: pendingTaskID,
				Placement:     recordedPlacement,
				Activation:    activation,
				Reason:        "placement selected a Cloud Workspace for this forked task",
			}
			authHeader := "Bearer " + strings.TrimSpace(s.token)
			if _, remoteTask, herr := createTaskOnCloudWorkspace(r.Context(), cloudErr, authHeader, bodyJSON, 20*time.Second); herr == nil && remoteTask != nil {
				targetDeviceID := ""
				if recordedPlacement != nil {
					targetDeviceID = recordedPlacement.TargetDeviceID
				}
				jsonReply(w, http.StatusAccepted, map[string]interface{}{
					"ok":               true,
					"mode":             "cloud_workspace",
					"taskId":           remoteTask.TaskID,
					"runnerId":         remoteTask.RunnerID,
					"status":           string(remoteTask.Status),
					"parentTaskId":     parent.ID,
					"relationship":     "forked-by-yaver",
					"contextWordsUsed": wordsUsed,
					"pendingTaskId":    pendingTaskID,
					"targetDeviceId":   targetDeviceID,
					"placement":        recordedPlacement,
				})
				return
			} else {
				reason := "Cloud Workspace is waking or needs attention before this forked task can run."
				if herr != nil {
					reason = herr.Error()
				}
				jsonReply(w, http.StatusConflict, map[string]interface{}{
					"ok":            false,
					"action":        "cloud_workspace_required",
					"pendingTaskId": pendingTaskID,
					"placement":     recordedPlacement,
					"activation":    activation,
					"reason":        reason,
				})
				return
			}
		} else if previewPlacement != nil {
			taskOpts.Placement = previewPlacement
		}
	}

	child, err := s.taskMgr.CreateTaskWithOptions(
		handoff,
		"forked from "+parent.ID+" (runner switch)",
		req.Model,
		"runner-switch-fork",
		req.Runner,
		"",  // customCommand
		nil, // images
		taskOpts,
	)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "create child task: "+err.Error())
		return
	}

	jsonReply(w, http.StatusOK, taskForkResponse{
		OK:               true,
		TaskID:           child.ID,
		RunnerID:         child.RunnerID,
		Status:           string(child.Status),
		ParentTaskID:     parent.ID,
		Relationship:     "forked-by-yaver",
		ContextWordsUsed: wordsUsed,
	})
}

// isSupportedForkRunner accepts the three canonical runners. Custom
// runner IDs flow through the regular /tasks path; fork is for the
// UI runner picker, which only offers these three.
func isSupportedForkRunner(id string) bool {
	switch normalizeRunnerID(id) {
	case "claude", "codex", "opencode":
		return true
	}
	return false
}

// buildForkHandoffPrompt produces the prompt the child runner sees.
// Format: structured handoff block (parent task id, runner, project,
// recent turns, assistant tail) followed by the user's new request.
//
// Word budget is split: the handoff block gets ~75%, the input gets
// the rest. Within the handoff block, the assistant tail eats up to
// 1/3, recent turns get the rest. Hard caps keep us out of OpenCode's
// 8k limit even when the user asks for the maximum 5000 words.
func buildForkHandoffPrompt(parent *Task, req taskForkRequest, wordBudget int) string {
	if parent == nil {
		return req.Input
	}
	handoffBudget := (wordBudget * 75) / 100
	if handoffBudget < 200 {
		handoffBudget = 200
	}

	// Reuse the existing CodeCompactContext builder for turn extraction.
	// maxTurns=8 matches the doc's max_turns_considered; the word
	// budget below trims further when needed.
	compact := buildCodeCompactContext(parent, 8)

	// Allocate inside the handoff budget.
	tailBudget := handoffBudget / 3
	turnsBudget := handoffBudget - tailBudget

	turnsBlock := joinWithinWordBudget(compact.RecentTurns, turnsBudget)
	tailBlock := truncateToWords(compact.LastResult, tailBudget)

	var b strings.Builder
	b.WriteString("[Conversation Handoff]\n")
	b.WriteString("Previous task: ")
	b.WriteString(parent.ID)
	b.WriteString("\n")
	if parent.RunnerID != "" {
		b.WriteString("Previous runner: ")
		b.WriteString(parent.RunnerID)
		b.WriteString("\n")
	}
	if parent.Model != "" {
		b.WriteString("Previous model: ")
		b.WriteString(parent.Model)
		b.WriteString("\n")
	}
	if parent.Title != "" {
		b.WriteString("Previous task title: ")
		b.WriteString(strings.TrimSpace(parent.Title))
		b.WriteString("\n")
	}
	if parent.WorkDir != "" {
		b.WriteString("Work dir: ")
		b.WriteString(parent.WorkDir)
		b.WriteString("\n")
	}
	if compact.UserIntent != "" {
		b.WriteString("Latest user intent: ")
		b.WriteString(compact.UserIntent)
		b.WriteString("\n")
	}
	b.WriteString("\nRecent chat context follows. This is a clipped excerpt, not the full transcript.\n\n")
	if turnsBlock != "" {
		b.WriteString(turnsBlock)
		b.WriteString("\n")
	}
	if tailBlock != "" {
		b.WriteString("\nLatest assistant tail:\n")
		b.WriteString(tailBlock)
		b.WriteString("\n")
	}
	b.WriteString("\n[New User Request]\n")
	b.WriteString(req.Input)
	return b.String()
}

// joinWithinWordBudget joins lines until the cumulative word count
// would exceed budget, then stops. Lines are kept whole — partial
// turns confuse runners more than missing turns do.
func joinWithinWordBudget(lines []string, budget int) string {
	if budget <= 0 || len(lines) == 0 {
		return ""
	}
	var out []string
	used := 0
	// Walk lines newest-first so when we run out of budget the OLDEST
	// turns get dropped, not the most recent.
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		w := countWords(line)
		if used+w > budget {
			break
		}
		out = append([]string{line}, out...)
		used += w
	}
	return strings.Join(out, "\n")
}

// truncateToWords cuts s after the budget'th word, appending a "..."
// marker so the runner can tell it received a clipped excerpt.
func truncateToWords(s string, budget int) string {
	if budget <= 0 {
		return ""
	}
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	words := strings.Fields(s)
	if len(words) <= budget {
		return s
	}
	return strings.Join(words[:budget], " ") + "..."
}

func countWords(s string) int {
	return len(strings.Fields(strings.TrimSpace(s)))
}
