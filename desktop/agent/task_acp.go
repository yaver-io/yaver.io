package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"mime"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	taskTransportACP = "acp"
	taskTransportCLI = "cli-pty"
)

// Test seam around ACP subprocess creation. Production uses the same resolver
// as ACP auth probes and doctor.
var newACPTaskClient = newACPClientForRunner

// shouldUseRunnerACP is intentionally conservative about task semantics, but
// not about runner brand. OpenCode uses ACP natively; Codex and Claude use a
// local ACP adapter when installed. Failure before session/prompt always falls
// through to CLI/tmux, so ACP improves the protocol without making a normal
// subscription runner less runnable.
func shouldUseRunnerACP(task *Task, runner RunnerConfig, effectiveModel string, rawRunnerCommand bool) (bool, string) {
	runnerID := normalizeRunnerID(runner.RunnerID)
	if runnerID != "opencode" && runnerID != "codex" && runnerID != "claude" {
		return false, "runner has no ACP task lane"
	}
	if strings.TrimSpace(os.Getenv("YAVER_ACP")) == "0" ||
		(runnerID == "opencode" && strings.TrimSpace(os.Getenv("YAVER_OPENCODE_ACP")) == "0") {
		return false, "disabled by YAVER_ACP=0"
	}
	if task == nil {
		return false, "missing task"
	}
	if rawRunnerCommand {
		return false, "runner-native commands require the CLI lane"
	}
	if task.ResumeLast || task.SessionID != "" {
		return false, "resume remains on the CLI lane"
	}
	// ACP itself is a clean bidirectional stdio protocol. A fresh task may use
	// it even when the host has tmux or a shared runner terminal configured:
	// tmux's send-keys/capture-pane path is a *different* compatibility
	// transport and would add shell echo, wrapping and terminal control bytes to
	// ACP JSON-RPC. Only a task already tied to a terminal seat must stay on the
	// CLI lane until the dedicated tmux↔ACP bridge owns that seat.
	if task.IsAdopted || task.TmuxSession != "" {
		return false, "existing tmux execution requires the CLI lane"
	}
	return true, "ACP task lane is eligible"
}

// acpTaskOptions are runner choices represented by ACP session config options
// rather than CLI flags. A task falls back before its prompt starts when its
// agent does not advertise a needed option, preserving exact user intent.
type acpTaskOptions struct {
	Model           string
	Mode            string
	ReasoningEffort string
}

// tryStartRunnerACP performs only reversible startup synchronously. Before
// Prompt begins, falling back cannot execute a user's request twice.
func (tm *TaskManager) tryStartRunnerACP(ctx context.Context, task *Task, prompt, taskDir string, options acpTaskOptions) (bool, error) {
	var outputMu sync.Mutex
	var output strings.Builder
	output.WriteString(task.Output)
	presentationMessageID := taskAssistantPresentationID(task)

	runnerID := normalizeRunnerID(task.RunnerID)
	client, err := newACPTaskClient(runnerID, taskDir, acpClientOptions{
		Env:       taskEnv(task),
		OnRequest: tm.acpTaskRequestHandler(task),
		OnNotify: func(method string, params json.RawMessage) {
			if method != "session/update" {
				return
			}
			var update acpSessionUpdate
			if err := json.Unmarshal(params, &update); err != nil {
				log.Printf("[task %s] ignoring malformed ACP session/update: %v", task.ID, err)
				return
			}
			emitTaskEvent(task, map[string]interface{}{
				"type": "runner_event", "schema": 1,
				"runner": runnerID, "transport": taskTransportACP,
				"event": update.Update.SessionUpdate, "messageId": update.Update.MessageID,
			})
			if update.Update.SessionUpdate == "tool_call" || update.Update.SessionUpdate == "tool_call_update" {
				label := acpToolActivityLabel(update.Update.Title, update.Update.RawInput)
				tm.present(task, taskPresentationInput{
					ID: task.ID + "-activity", Kind: "status", Text: label,
					Phase: "tool", State: firstNonEmpty(strings.TrimSpace(update.Update.Status), "running"),
				})
				// Tool output is diagnostic terminal evidence, not assistant prose.
				// Codex sends incremental terminal bytes in `_meta`; other ACP
				// adapters commonly expose a final rawOutput or structured diff.
				// All forms flow to the one capped raw lane that every Yaver client
				// already renders with ANSI/diff support.
				for _, evidence := range acpToolEvidence(update.Update.RawInput, update.Update.RawOutput, update.Update.Content, update.Update.Meta) {
					outputMu.Lock()
					tm.emitRaw(task, []byte(evidence))
					outputMu.Unlock()
				}
				return
			}
			if update.Update.SessionUpdate != "agent_message_chunk" {
				return
			}
			for _, text := range acpMessageText(update.Update.Content) {
				// ACP chunks are an assistant's semantic narration, not terminal
				// evidence. Stream them through the presentation boundary as a
				// replaceable live message so the phone is not silent until the
				// final result. The final completion upserts this same ID after
				// the normal human-readable safety filter runs.
				outputMu.Lock()
				tm.emitRaw(task, []byte(text))
				chunk := joinACPAssistantChunk(output.String(), text)
				tm.emit(task, &output, chunk)
				tm.present(task, taskPresentationInput{
					ID: presentationMessageID, Kind: "message", Role: "assistant",
					Text: chunk, Phase: "responding", State: "streaming", Append: true,
				})
				outputMu.Unlock()
			}
		},
	})
	if err != nil {
		return false, fmt.Errorf("spawn: %w", err)
	}

	initCtx, initCancel := context.WithTimeout(ctx, 45*time.Second)
	_, err = client.Initialize(initCtx)
	initCancel()
	if err != nil {
		client.Close()
		return false, fmt.Errorf("initialize: %w", err)
	}

	mcpServers := acpMCPServersForTask(findYaverBinary(), enabledExternalServersFor(task.MCPServers), task.IncludeYaverMcp)
	sessionCtx, sessionCancel := context.WithTimeout(ctx, 30*time.Second)
	sessionID, configOptions, err := client.NewSession(sessionCtx, taskDir, mcpServers)
	sessionCancel()
	if err != nil {
		client.Close()
		return false, fmt.Errorf("session/new: %w", err)
	}
	if err := applyACPTaskOptions(ctx, client, sessionID, configOptions, options); err != nil {
		client.Close()
		return false, err
	}
	content, err := acpTaskPromptContent(task, prompt)
	if err != nil {
		client.Close()
		return false, err
	}

	now := time.Now()
	task.SessionID = sessionID
	task.Transport = taskTransportACP
	task.StartedAt = &now
	task.Status = TaskStatusRunning
	tm.present(task, taskRunningPresentation(task))
	emitTaskEvent(task, map[string]interface{}{
		"type": "runner_transport", "schema": 1,
		"runner": runnerID, "transport": taskTransportACP,
	})
	go tm.runRunnerACPPrompt(ctx, client, task, sessionID, content)
	return true, nil
}

// ACP carries images as base64 content blocks. Bound each attachment before
// encoding so a malformed client cannot multiply an 8 GB machine's memory
// use at the task boundary.
const acpTaskImageMaxBytes = 4 * 1024 * 1024

func acpTaskPromptContent(task *Task, prompt string) ([]acpContentBlock, error) {
	content := []acpContentBlock{acpTextBlock(prompt)}
	if task == nil {
		return content, nil
	}
	for _, path := range task.ImagePaths {
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read ACP image attachment: %w", err)
		}
		if len(data) == 0 || len(data) > acpTaskImageMaxBytes {
			return nil, fmt.Errorf("ACP image attachment must be between 1 byte and %d MiB", acpTaskImageMaxBytes/(1024*1024))
		}
		mimeType := mime.TypeByExtension(strings.ToLower(filepath.Ext(path)))
		if !strings.HasPrefix(mimeType, "image/") {
			return nil, fmt.Errorf("ACP attachment is not a supported image: %s", filepath.Base(path))
		}
		content = append(content, acpImageBlock(base64.StdEncoding.EncodeToString(data), mimeType))
	}
	return content, nil
}

func applyACPTaskOptions(ctx context.Context, client *acpClient, sessionID string, configOptions []acpConfigOption, options acpTaskOptions) error {
	type requestedOption struct {
		name  string
		value string
	}
	requested := []requestedOption{
		{name: "model", value: strings.TrimSpace(options.Model)},
		{name: "mode", value: strings.TrimSpace(options.Mode)},
		{name: "reasoning", value: strings.TrimSpace(options.ReasoningEffort)},
	}
	for _, want := range requested {
		if want.value == "" {
			continue
		}
		id := acpConfigOptionID(configOptions, want.name)
		if id == "" {
			return fmt.Errorf("ACP runner does not advertise a %s option required by this task", want.name)
		}
		optionCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
		next, err := client.SetSessionConfigOption(optionCtx, sessionID, id, want.value)
		cancel()
		if err != nil {
			return fmt.Errorf("ACP set %s: %w", want.name, err)
		}
		if len(next) != 0 {
			configOptions = next
		}
	}
	return nil
}

func acpConfigOptionID(options []acpConfigOption, wanted string) string {
	wanted = strings.ToLower(strings.TrimSpace(wanted))
	for _, option := range options {
		category := strings.ToLower(strings.TrimSpace(option.Category))
		id := strings.ToLower(strings.TrimSpace(option.ID))
		name := strings.ToLower(strings.TrimSpace(option.Name))
		switch wanted {
		case "model":
			if category == "model" || id == "model" || name == "model" {
				return option.ID
			}
		case "mode":
			if category == "mode" || id == "mode" || name == "mode" {
				return option.ID
			}
		case "reasoning":
			if category == "reasoning" || category == "reasoning_effort" || id == "reasoning" || id == "reasoning_effort" || name == "reasoning effort" {
				return option.ID
			}
		}
	}
	return ""
}

func (tm *TaskManager) runRunnerACPPrompt(ctx context.Context, client *acpClient, task *Task, sessionID string, content []acpContentBlock) {
	result, promptErr := client.Prompt(ctx, sessionID, content)
	client.Close()
	if promptErr == nil {
		tm.mu.RLock()
		finalText := strings.TrimSpace(task.Output)
		tm.mu.RUnlock()
		if finalText != "" {
			tm.present(task, taskPresentationInput{
				ID: taskAssistantPresentationID(task), Kind: "message", Role: "assistant",
				Text: finalText, Phase: "complete", State: "completed",
			})
		}
	}

	finishNow := time.Now()
	tm.mu.Lock()
	task.cancel = nil
	task.FinishedAt = &finishNow
	if result != nil && result.Usage != nil {
		task.InputTokens = result.Usage.InputTokens
		task.OutputTokens = result.Usage.OutputTokens
	}
	task.ResultText = strings.TrimSpace(task.Output)
	combinedFailure := task.Output + "\n" + task.ResultText
	if promptErr != nil {
		combinedFailure += "\n" + promptErr.Error()
	}
	if refusedModel, refusalReason := classifyUnsupportedModelForAttempt(task.Model, combinedFailure); refusedModel != "" {
		globalModelSupport.Record(task.RunnerID, refusedModel, refusalReason)
		fallback, canFallback := modelFallbackForRefusal(task.RunnerID, refusedModel, task.modelFallbackAttempted)
		if canFallback && !globalModelSupport.Refused(task.RunnerID, fallback.Model) {
			task.modelFallbackAttempted = true
			task.Model = fallback.Model
			if normalizeRunnerID(task.RunnerID) == "codex" {
				task.ReasoningEffort = firstNonEmpty(normalizeCodexReasoningEffort(fallback.ReasoningEffort), "medium")
			}
			task.SessionID = ""
			task.ResumeLast = false
			task.Failure = nil
			task.Status = TaskStatusQueued
			task.FinishedAt = nil
			notice := fmt.Sprintf("\nModel %s was rejected by this account. Retrying once with Yaver default %s", refusedModel, fallback.Model)
			if task.ReasoningEffort != "" {
				notice += " · " + task.ReasoningEffort
			}
			notice += ".\n"
			task.Output += notice
			select {
			case task.outputCh <- notice:
			default:
			}
			tm.persist()
			tm.mu.Unlock()
			log.Printf("[task %s] ACP model %q rejected — retrying once with Yaver default %q", task.ID, refusedModel, fallback.Model)
			if restartErr := tm.startProcess(task); restartErr != nil {
				tm.mu.Lock()
				task.Status = TaskStatusFailed
				now := time.Now()
				task.FinishedAt = &now
				task.ResultText = restartErr.Error()
				task.Failure = diagnoseTaskFailure(task, now)
				tm.persist()
				tm.mu.Unlock()
				closeTaskStream(task.outputCh)
				closeTaskDone(task.doneCh)
			}
			return
		}
	}

	cancelled := false
	switch {
	case errors.Is(promptErr, context.Canceled) || errors.Is(ctx.Err(), context.Canceled):
		// Publish the stopped state before doneCh so an SSE consumer cannot
		// observe done while the snapshot still says running. StopTask performs
		// the persistence/callback after its wait returns.
		task.Status = TaskStatusStopped
		cancelled = true
	case promptErr != nil:
		task.Status = TaskStatusFailed
		if task.ResultText == "" {
			task.ResultText = firstNonEmpty(strings.TrimSpace(task.runner.Name), strings.TrimSpace(task.RunnerID), "Runner") + " ACP stopped before producing a reply: " + promptErr.Error()
			task.Output = task.ResultText
		}
		log.Printf("[task %s] OpenCode ACP prompt failed: %v", task.ID, promptErr)
	case isEmptyRunnerReply(task.Output, task.ResultText):
		task.Status = TaskStatusFailed
		task.ResultText = "The runner ACP lane completed without producing a reply. Retry on the CLI compatibility lane or run Yaver Doctor to probe the runner."
		task.Output = task.ResultText
	default:
		task.Status = taskSuccessStatus(task)
	}
	if cancelled {
		tm.mu.Unlock()
		closeTaskStream(task.outputCh)
		closeTaskDone(task.doneCh)
		return
	}

	ObserveRunnerAuthFromOutput(task.RunnerID, task.Output+"\n"+task.ResultText, string(task.Status))
	task.Failure = diagnoseTaskFailure(task, finishNow)
	if task.ResultText != "" {
		task.Turns = append(task.Turns, ConversationTurn{
			Role: "assistant", Content: task.ResultText, Timestamp: finishNow,
			Hidden: !taskHasSemanticAssistantTextLocked(task, task.ResultText),
		})
	}
	if (task.Status == TaskStatusReady || task.Status == TaskStatusReview || task.Status == TaskStatusFinished) && len(task.PendingFollowUps) > 0 {
		next := task.PendingFollowUps[0]
		task.PendingFollowUps = task.PendingFollowUps[1:]
		oldOutputCh := task.outputCh
		oldDoneCh := task.doneCh
		task.Turns = append(task.Turns, ConversationTurn{Role: "user", Content: next.Input, Timestamp: time.Now()})
		if len(next.Images) > 0 {
			task.ImagePaths = append(task.ImagePaths, saveImages(task.ID, next.Images)...)
		}
		if runnerID := normalizeRunnerID(next.Options.RunnerID); runnerID != "" {
			previousRunner := normalizeRunnerID(task.RunnerID)
			nextRunner := GetRunnerConfig(runnerID)
			task.runner = nextRunner
			task.RunnerID = nextRunner.RunnerID
			if nextRunner.RunnerID != previousRunner {
				task.SessionID = ""
			}
		}
		if model := strings.TrimSpace(next.Options.Model); model != "" {
			task.Model = model
		}
		if mode := strings.TrimSpace(next.Options.Mode); mode != "" {
			nextRunner := task.runner
			if nextRunner.Command == "" {
				nextRunner = tm.runner
			}
			nextRunner.Mode = mode
			task.runner = nextRunner
		}
		task.Output = ""
		task.RawOutput = ""
		task.RawOutputOffset = 0
		task.RawOutputBase = 0
		task.ResultText = ""
		task.FinishedAt = nil
		task.Status = TaskStatusQueued
		task.outputCh = make(chan string, 512)
		task.rawOutputCh = make(chan taskRawFrame, 256)
		task.eventCh = make(chan map[string]interface{}, 32)
		task.doneCh = make(chan struct{})
		tm.persist()
		tm.mu.Unlock()

		closeTaskStream(oldOutputCh)
		closeTaskDone(oldDoneCh)
		if err := tm.startResume(task, next.Input); err != nil {
			tm.mu.Lock()
			task.Status = TaskStatusFailed
			now := time.Now()
			task.FinishedAt = &now
			task.ResultText = "Could not continue task on the CLI compatibility lane: " + err.Error()
			task.Output = task.ResultText
			tm.persist()
			tm.fireTaskDone(task)
			tm.mu.Unlock()
			closeTaskStream(task.outputCh)
			closeTaskDone(task.doneCh)
		}
		return
	}

	if tm.ConvexURL != "" && task.StartedAt != nil && task.FinishedAt != nil {
		duration := task.FinishedAt.Sub(*task.StartedAt).Seconds()
		startMs := task.StartedAt.UnixMilli()
		finishMs := task.FinishedAt.UnixMilli()
		runnerName, model, source, taskID := task.runner.Name, task.Model, task.Source, task.ID
		go func() {
			if err := ReportRunnerUsage(tm.ConvexURL, tm.AuthToken, tm.DeviceID, taskID, runnerName, model, source, duration, startMs, finishMs); err != nil {
				log.Printf("[usage] failed to report ACP task: %v", err)
			}
		}()
	}
	tm.persist()
	tm.fireTaskDone(task)
	tm.maybeProposeSchedule(task)
	// Session persistence runs outside the task lock, so hand it an immutable
	// snapshot. Stop/retry APIs may mutate the live task immediately after the
	// done event; reading that pointer asynchronously is a data race and can
	// write a history file with a mixed terminal state.
	sessionTask := *task
	sessionTask.Turns = append([]ConversationTurn(nil), task.Turns...)
	runnerName := task.runner.Name
	workDir := tm.effectiveTaskWorkDir(task)
	tm.mu.Unlock()

	go saveSessionFile(&sessionTask, runnerName, workDir)
	closeTaskStream(task.outputCh)
	closeTaskDone(task.doneCh)
}

func closeTaskStream(ch chan string) {
	if ch == nil {
		return
	}
	defer func() { _ = recover() }()
	close(ch)
}

func closeTaskDone(ch chan struct{}) {
	if ch == nil {
		return
	}
	defer func() { _ = recover() }()
	close(ch)
}
