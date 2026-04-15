package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
)

// HandoffSpec describes a "pass session to yaver" request.
//
// Source identifies the AI session being handed off:
//   - SourceTaskID: a Yaver-managed task ID
//   - SourceSessionFile: a path to a Claude Code / Aider / Codex session file
//   - SourceBundle: an already-exported TransferBundle (used by remote handoff)
//
// Engine selects how Yaver continues the work:
//   - "claude" / "" — runner=claude-code, full frontier model
//   - "hybrid"      — planner+local implementer (cheap)
//   - "runner"      — an arbitrary single runner (Runner field)
//
// Target is empty for local handoff, otherwise a device hint (id/hostname).
type HandoffSpec struct {
	SourceTaskID      string          `json:"sourceTaskId,omitempty"`
	SourceSessionFile string          `json:"sourceSessionFile,omitempty"`
	SourceBundle      *TransferBundle `json:"sourceBundle,omitempty"`

	Target string `json:"target,omitempty"` // remote device hint; empty = local

	Engine      string `json:"engine,omitempty"` // claude | hybrid | runner
	Runner      string `json:"runner,omitempty"` // when engine=runner
	WorkDir     string `json:"workDir,omitempty"`
	MaxKicks    int    `json:"maxKicks,omitempty"`
	DeadlineSec int    `json:"deadlineSec,omitempty"`

	// StopSource: when true, attempt to stop the source Yaver task before
	// kicking the new loop (only meaningful when SourceTaskID is set).
	StopSource bool `json:"stopSource,omitempty"`

	// ExtraPrompt, when non-empty, is appended to the resume prompt.
	ExtraPrompt string `json:"extraPrompt,omitempty"`

	// SkipInitialKick, when true, suppresses the async first kick. Used
	// only by tests so they can inspect persisted loop state without
	// racing a goroutine that tries to invoke an AI runner binary.
	SkipInitialKick bool `json:"-"`

	// Autodev, when true, expands the resume prompt: finish the existing
	// remained items AND mine the session for new improvements/tests/ideas
	// to also implement. This is the `yaver handoff autodev` mode — Yaver
	// becomes a proactive successor, not just a continuation.
	Autodev bool `json:"autodev,omitempty"`
}

// HandoffResult is what the orchestrator returns to the caller (CLI/MCP/HTTP).
type HandoffResult struct {
	OK            bool     `json:"ok"`
	LocalTaskID   string   `json:"localTaskId,omitempty"`
	LoopName      string   `json:"loopName,omitempty"`
	Engine        string   `json:"engine"`
	Runner        string   `json:"runner"`
	SentinelFile  string   `json:"sentinelFile"`
	RemoteDevice  string   `json:"remoteDevice,omitempty"`
	Warnings      []string `json:"warnings,omitempty"`
	Message       string   `json:"message"`
	ExitNow       bool     `json:"exitNow"` // signal to source agent to terminate
}

// HandoffSentinel is written to ~/.yaver/handoff/<loopName>.json so the
// source AI agent (Claude Code, etc.) — which may have called this via
// MCP — can poll/watch for it and exit cleanly without us force-killing
// an external CLI.
type HandoffSentinel struct {
	WrittenAt   string `json:"writtenAt"`
	LoopName    string `json:"loopName"`
	LocalTaskID string `json:"localTaskId,omitempty"`
	Engine      string `json:"engine"`
	Runner      string `json:"runner"`
	Message     string `json:"message"`
}

// resolveHandoffRunner maps an Engine choice to a concrete runner ID.
func resolveHandoffRunner(engine, runner string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(engine)) {
	case "", "claude", "claude-code":
		return "claude-code", nil
	case "hybrid":
		return "hybrid", nil
	case "runner":
		if runner == "" {
			return "", fmt.Errorf("engine=runner requires --runner (e.g. aider, codex, ollama:qwen2.5-coder:14b)")
		}
		return runner, nil
	default:
		// Treat unknown engine as a literal runner id (forward-compat).
		return engine, nil
	}
}

// RunHandoff performs the local half of a handoff. For remote handoff the
// CLI/HTTP caller proxies the bundle to the target's /session/handoff
// endpoint, which then invokes RunHandoff there.
//
// Steps:
//  1. Resolve source → TransferBundle (export, read file, or use spec.SourceBundle)
//  2. Import bundle into local TaskManager
//  3. (Optional) StopTask on source Yaver task
//  4. Build a develop-mode LoopSpec keyed to the chosen engine/runner
//  5. Register the loop and persist its initial PromptInline (resume prompt)
//  6. Write a sentinel file the source agent can poll
//  7. Best-effort: kick the loop once so work starts immediately
func RunHandoff(s *HTTPServer, spec HandoffSpec) (*HandoffResult, error) {
	tm := s.taskMgr
	if tm == nil {
		return nil, fmt.Errorf("task manager unavailable")
	}

	runnerID, err := resolveHandoffRunner(spec.Engine, spec.Runner)
	if err != nil {
		return nil, err
	}

	// 1. Source → bundle ------------------------------------------------------
	var bundle *TransferBundle
	switch {
	case spec.SourceBundle != nil:
		bundle = spec.SourceBundle
	case spec.SourceTaskID != "":
		b, err := ExportSession(tm, spec.SourceTaskID, ExportOptions{IncludeWorkspace: false})
		if err != nil {
			return nil, fmt.Errorf("export source task: %w", err)
		}
		bundle = b
	case spec.SourceSessionFile != "":
		data, err := os.ReadFile(spec.SourceSessionFile)
		if err != nil {
			return nil, fmt.Errorf("read source session file: %w", err)
		}
		// Two accepted formats: a full TransferBundle JSON, or a raw Claude
		// session.jsonl which we wrap into a minimal bundle.
		var b TransferBundle
		if err := json.Unmarshal(data, &b); err != nil || b.Version == 0 {
			b = TransferBundle{
				Version:    1,
				ExportedAt: time.Now().UTC().Format(time.RFC3339),
				AgentType:  "claude",
				Task: TransferTask{
					Title:    "Handoff: " + filepath.Base(spec.SourceSessionFile),
					RunnerID: "claude",
					WorkDir:  spec.WorkDir,
				},
				AgentFiles: map[string]string{
					"claude/session.jsonl": encodeBase64(data),
				},
			}
		}
		bundle = &b
	default:
		// No source: still allow handoff (just spin up an autodev loop in
		// the workdir using an empty resume prompt). Caller usually wants
		// this when "resume" semantics aren't important.
		bundle = &TransferBundle{
			Version:    1,
			ExportedAt: time.Now().UTC().Format(time.RFC3339),
			AgentType:  "unknown",
			Task: TransferTask{
				Title:    "Handoff: ad-hoc",
				RunnerID: runnerID,
				WorkDir:  spec.WorkDir,
			},
		}
	}

	// 2. Import ---------------------------------------------------------------
	importedTaskID, warnings, err := ImportSession(tm, bundle, ImportOptions{
		WorkDir: spec.WorkDir,
	})
	if err != nil {
		return nil, fmt.Errorf("import: %w", err)
	}

	// 3. Stop source if asked ------------------------------------------------
	if spec.StopSource && spec.SourceTaskID != "" {
		if err := tm.StopTask(spec.SourceTaskID); err != nil {
			warnings = append(warnings, fmt.Sprintf("stop source task: %v", err))
		}
	}

	// 4. Build LoopSpec ------------------------------------------------------
	loopName := fmt.Sprintf("handoff-%s", time.Now().UTC().Format("20060102-150405"))
	maxKicks := spec.MaxKicks
	if maxKicks <= 0 {
		maxKicks = 20
	}

	prompt := buildHandoffPrompt(bundle, spec.ExtraPrompt, spec.Autodev, s)
	loopWorkDir := spec.WorkDir
	if loopWorkDir == "" {
		loopWorkDir, _ = os.Getwd()
	}

	lspec := LoopSpec{
		Name:   loopName,
		Mode:   LoopModeDevelop,
		Target: detectAutodevTarget(loopWorkDir),
		Schedule: LoopSchedule{
			Every:         "5m",
			MaxIterations: maxKicks,
		},
		Think: LoopThink{
			Runner:         runnerID,
			MaxKicksPerRun: maxKicks,
			PromptInline:   prompt,
		},
		Ship: LoopShip{Branch: "main"},
	}
	applyLoopDefaults(&lspec)
	if err := validateLoopSpec(&lspec); err != nil {
		return nil, fmt.Errorf("invalid loop spec: %w", err)
	}

	// 5. Persist loop --------------------------------------------------------
	loops, err := loadLoops()
	if err != nil {
		return nil, fmt.Errorf("load loops: %w", err)
	}
	state := &LoopState{
		ID:           uuid.New().String(),
		Spec:         lspec,
		Status:       LoopStatusIdle,
		CreatedAt:    time.Now().UTC().Format(time.RFC3339),
		WorkDir:      loopWorkDir,
		PromptInline: prompt,
	}
	loops[loopName] = state
	if err := saveLoops(loops); err != nil {
		return nil, fmt.Errorf("save loop: %w", err)
	}

	// 6. Sentinel ------------------------------------------------------------
	sentinelPath, sentinelErr := writeHandoffSentinel(loopName, importedTaskID, runnerID, prompt)
	if sentinelErr != nil {
		warnings = append(warnings, fmt.Sprintf("write sentinel: %v", sentinelErr))
	}

	// 7. Kick once (best-effort, async so the HTTP/CLI caller returns fast) -
	if !spec.SkipInitialKick {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if _, _, kerr := kickLoopOnce(ctx, loopName); kerr != nil {
				log.Printf("[handoff] initial kick failed: %v", kerr)
			}
		}()
	}

	log.Printf("[handoff] task=%s loop=%s engine=%s runner=%s sentinel=%s",
		importedTaskID, loopName, spec.Engine, runnerID, sentinelPath)

	return &HandoffResult{
		OK:           true,
		LocalTaskID:  importedTaskID,
		LoopName:     loopName,
		Engine:       firstNonEmpty(spec.Engine, "claude"),
		Runner:       runnerID,
		SentinelFile: sentinelPath,
		Warnings:     warnings,
		Message:      fmt.Sprintf("Yaver has taken over. Loop %q (%s) is running. Source agent should exit.", loopName, runnerID),
		ExitNow:      true,
	}, nil
}

// buildHandoffPrompt synthesises the resume prompt fed into the new loop.
// Sources of "what's left":
//   - The TransferBundle's last assistant turn (if any) — for context
//   - TodoListManager pending items — structured, authoritative
//   - Caller-supplied ExtraPrompt — overrides priority
func buildHandoffPrompt(bundle *TransferBundle, extra string, autodev bool, s *HTTPServer) string {
	var sb strings.Builder
	sb.WriteString("You are resuming a session that was handed off to Yaver. ")
	sb.WriteString("Continue the in-progress work to completion. ")
	sb.WriteString("Do not re-introduce yourself; pick up where the previous agent stopped.\n\n")

	if bundle.Task.Title != "" {
		sb.WriteString("Original task: " + bundle.Task.Title + "\n")
	}
	if bundle.AgentType != "" && bundle.AgentType != "unknown" {
		sb.WriteString("Previous agent: " + bundle.AgentType + "\n")
	}
	if n := len(bundle.Task.Turns); n > 0 {
		sb.WriteString(fmt.Sprintf("Conversation so far: %d turns (imported into Yaver task store).\n", n))
	}
	sb.WriteString("\n")

	if s != nil && s.todolistMgr != nil {
		pending := s.todolistMgr.PendingItems()
		if len(pending) > 0 {
			sb.WriteString("Uncompleted todos to address:\n")
			for i, item := range pending {
				if i >= 20 {
					sb.WriteString(fmt.Sprintf("  ... and %d more\n", len(pending)-20))
					break
				}
				desc := strings.ReplaceAll(item.Description, "\n", " ")
				if len(desc) > 200 {
					desc = desc[:200] + "..."
				}
				sb.WriteString(fmt.Sprintf("  %d. %s\n", i+1, desc))
			}
			sb.WriteString("\n")
		}
	}

	if extra != "" {
		sb.WriteString("Additional instructions from caller:\n")
		sb.WriteString(extra)
		sb.WriteString("\n")
	}

	if autodev {
		sb.WriteString(`
AUTODEV MODE — be proactive, not just reactive:

1. First, finish every uncompleted item the previous agent left open
   (todos above, half-applied diffs, missing implementations).
2. Then mine the imported session for follow-up work the previous agent
   *discussed but never did*: TODO comments left in code, "we should
   also …" lines in the conversation, missing tests for new code paths,
   obvious next-iteration improvements.
3. For every change, add or update tests covering the behavior. Do not
   ship a feature without tests; do not ship a fix without a regression
   test.
4. Research the market around the session's subject. Use web search to
   find competing products, read their docs / changelogs / pricing /
   user reviews, and identify gaps the market does not yet serve well.
   Bias toward smart differentiation: don't copy the leader, find the
   angle they ignore. Capture findings briefly in a commit message or
   a short note alongside the change so the next iteration can build
   on the research.
5. Propose 1-3 new improvements that fit the spirit of the session,
   are informed by the market research, and meaningfully differentiate
   our product. If they are small and safe, implement them. Commit
   each as a separate change so they can be reviewed independently.
6. Stop when there is genuinely nothing useful left to add — do not
   invent unrelated work.

`)
	}

	sb.WriteString("\nWork iteratively. After each meaningful change, commit and continue with the next item.")
	return sb.String()
}

// detectAutodevTarget mirrors autodev_cmd.go's heuristic: ios-sim if a
// mobile/ios dir exists at the workdir, otherwise web. The target is only
// used by the loop runner for things like build/playtest defaults; for a
// pure handoff loop it rarely matters but a value is required by validate.
func detectAutodevTarget(wd string) string {
	if fileExists(filepath.Join(wd, "mobile", "ios")) {
		return "ios-sim"
	}
	return "web"
}

// writeHandoffSentinel writes the sentinel JSON the source agent can watch.
func writeHandoffSentinel(loopName, taskID, runner, message string) (string, error) {
	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	sdir := filepath.Join(dir, "handoff")
	if err := os.MkdirAll(sdir, 0700); err != nil {
		return "", err
	}
	path := filepath.Join(sdir, loopName+".json")
	sentinel := HandoffSentinel{
		WrittenAt:   time.Now().UTC().Format(time.RFC3339),
		LoopName:    loopName,
		LocalTaskID: taskID,
		Runner:      runner,
		Message:     "Yaver has taken over this session. Source agent should exit.",
	}
	data, _ := json.MarshalIndent(sentinel, "", "  ")
	if err := os.WriteFile(path, data, 0600); err != nil {
		return "", err
	}
	// Also maintain a stable "latest" pointer for poll-based watchers.
	_ = os.WriteFile(filepath.Join(sdir, "latest.json"), data, 0600)
	return path, nil
}

func encodeBase64(b []byte) string {
	return base64.StdEncoding.EncodeToString(b)
}
