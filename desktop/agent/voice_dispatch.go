package main

// voice_dispatch.go — given a final transcript, create a task and
// pipe its result back through TTS. The bridge between the speech
// layer (Deepgram in / Cartesia out) and the existing task pipeline
// (TaskManager.CreateTaskWithOptions, source="voice-input").
//
// The pattern intentionally mirrors how mobile creates tasks
// (source="mobile") — voice is just another source arm. The agent
// prompt wrapper inspects Source and adjusts tone for voice
// turn-takings (terse, "Approve?"-style asks).

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"
)

// VoiceDispatchOptions tunes the task created from a transcript.
type VoiceDispatchOptions struct {
	Project string // project slug ("yaver" / "talos" / …) — affects WorkDir / keyterm bias
	Model   string // runner model override; empty = task manager default
	Runner  string // runner id; empty = default
	// Title is used to display the task in the UI. Defaults to the
	// first sentence of the transcript truncated to ~60 chars.
	Title string
	// Viewport — surface where the user is reading the response.
	// Set by the WS handler from the start frame; nil = no hint.
	Viewport *TaskViewport
	// Placement lets voice ingress avoid spending relay runner time when the
	// machine layer selects a Cloud Workspace.
	Placement TaskIngressPlacementConfig
}

// VoiceTaskResult is what we return after the task finishes — used
// by the WS handler to feed TTS readback.
type VoiceTaskResult struct {
	TaskID     string
	ResultText string
	Status     string
	Error      error
}

// DispatchVoiceTranscript creates a task from a final transcript and
// blocks until it completes (or ctx is cancelled / timeout). The
// returned VoiceTaskResult is what gets read back over TTS.
//
// We do NOT stream Claude tokens to TTS here — the runner runs as a
// black-box agent on a separate process. Once it finishes we have a
// single result string to speak.
func DispatchVoiceTranscript(ctx context.Context, tm *TaskManager, transcript string, opts VoiceDispatchOptions) (*VoiceTaskResult, error) {
	if tm == nil {
		return nil, fmt.Errorf("task manager unavailable")
	}
	transcript = strings.TrimSpace(transcript)
	if transcript == "" {
		return nil, fmt.Errorf("empty transcript")
	}

	if result, deferred := deferVoiceTranscriptToCloudWorkspace(ctx, opts.Placement); deferred {
		return result, nil
	}

	// Proactive runner pre-flight: if a specific runner is named and its auth is
	// missing/expired, surface a spoken re-auth CTA NOW instead of creating a task
	// that will 401 mid-flight (the "codex expired" friction). Only fires when a
	// runner is named — the default-runner path is resolved inside the TaskManager.
	if pf := RunnerPreflightByID(opts.Runner, ""); pf.NeedsReauth {
		return &VoiceTaskResult{
			Status:     "auth_required",
			ResultText: runnerPreflightSpoken(pf),
		}, nil
	}

	title := opts.Title
	if title == "" {
		title = voiceTitleFromTranscript(transcript)
	}

	task, err := tm.CreateTaskWithOptions(
		title,
		transcript,
		opts.Model,
		"voice-input",
		opts.Runner,
		"", // customCommand
		nil,
		TaskCreateOptions{},
	)
	if err != nil {
		return nil, fmt.Errorf("create voice task: %w", err)
	}
	// Always mark voice-originated tasks so the prompt wrapper sets
	// a voice-budgeted readback constraint. Surface comes from the
	// WS caller; default to "" (the wrapper handles no-surface case).
	vp := opts.Viewport
	if vp == nil {
		vp = &TaskViewport{Voice: true}
	} else {
		vp.Voice = true
	}
	tm.mu.Lock()
	task.TaskViewport = vp
	tm.mu.Unlock()

	// Poll for completion. The task manager already runs the task on
	// a goroutine — we just need to wait for terminal status.
	deadline := time.Now().Add(15 * time.Minute)
	tick := time.NewTicker(250 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return &VoiceTaskResult{TaskID: task.ID, Status: "cancelled"}, ctx.Err()
		case <-tick.C:
			cur, ok := tm.GetTask(task.ID)
			if !ok {
				return &VoiceTaskResult{TaskID: task.ID, Status: "missing"}, fmt.Errorf("task vanished")
			}
			if isTerminalTaskStatus(cur.Status) {
				return &VoiceTaskResult{
					TaskID:     cur.ID,
					ResultText: voicePickResultText(cur),
					Status:     string(cur.Status),
				}, nil
			}
			if time.Now().After(deadline) {
				return &VoiceTaskResult{TaskID: task.ID, Status: string(cur.Status)}, fmt.Errorf("voice task timed out")
			}
		}
	}
}

func deferVoiceTranscriptToCloudWorkspace(ctx context.Context, cfg TaskIngressPlacementConfig) (*VoiceTaskResult, bool) {
	deferral, deferred, err := deferIngressTaskToCloudWorkspace(ctx, cfg, "voice-input", "unknown")
	if err != nil && !deferred {
		log.Printf("[placement] voice preview skipped before task create: %v", err)
		return nil, false
	}
	if !deferred {
		return nil, false
	}
	if err != nil {
		pendingTaskID := ""
		if deferral != nil {
			pendingTaskID = deferral.PendingTaskID
		}
		log.Printf("[placement] voice cloud deferral failed for %s: %v", pendingTaskID, err)
		return &VoiceTaskResult{
			TaskID:     pendingTaskID,
			Status:     "cloud_workspace_required",
			ResultText: fmt.Sprintf("Cloud Workspace is selected for this request, but the handoff is not ready yet: %v", err),
		}, true
	}
	if blocker := strings.TrimSpace(deferral.Blocker); blocker != "" {
		return &VoiceTaskResult{
			TaskID:     deferral.PendingTaskID,
			Status:     "cloud_workspace_required",
			ResultText: "Cloud Workspace is selected for this request, but it needs your attention first: " + blocker,
		}, true
	}
	target := ""
	if deferral.Placement != nil {
		target = strings.TrimSpace(deferral.Placement.TargetDeviceID)
	}
	if target == "" {
		target = "Cloud Workspace"
	}
	return &VoiceTaskResult{
		TaskID:     deferral.PendingTaskID,
		Status:     "cloud_workspace_required",
		ResultText: fmt.Sprintf("Cloud Workspace is selected for this request. I queued a pending handoff for %s and will not run it on the relay while the workspace wakes.", target),
	}, true
}

// voiceTitleFromTranscript turns a raw transcript into a short title
// for the Tasks list. ~60 chars, ends at first sentence boundary.
func voiceTitleFromTranscript(t string) string {
	t = strings.TrimSpace(t)
	if len(t) <= 60 {
		return t
	}
	// stop at first ., ?, !, comma at >40 chars, else hard truncate
	for i, r := range t {
		if i > 40 && (r == '.' || r == '?' || r == '!' || r == ',') {
			return strings.TrimSpace(t[:i])
		}
		if i >= 60 {
			break
		}
	}
	return strings.TrimSpace(t[:60]) + "…"
}

// voicePickResultText prefers the explicit ResultText (clean Claude
// output) over the raw Output blob, falling back as needed.
func voicePickResultText(t *Task) string {
	if t == nil {
		return ""
	}
	if r := strings.TrimSpace(t.ResultText); r != "" {
		return r
	}
	out := strings.TrimSpace(t.Output)
	// Output can be enormous (whole agent transcript). For TTS, keep
	// only the last ~600 chars — anything longer is unlistenable.
	if len(out) > 600 {
		return "…" + out[len(out)-600:]
	}
	return out
}

// isTerminalTaskStatus tells whether a status is a "done" status —
// any value the TaskManager won't transition out of. We treat
// TaskStatusReview as terminal so a runner that pauses for a
// human prompt still surfaces audibly via TTS instead of hanging.
func isTerminalTaskStatus(s TaskStatus) bool {
	switch s {
	case TaskStatusFinished, TaskStatusFailed, TaskStatusStopped, TaskStatusReview:
		return true
	}
	return false
}
