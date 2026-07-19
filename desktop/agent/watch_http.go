package main

// watch_http.go — the agent endpoint a STANDALONE smartwatch calls when it
// has no paired phone to run the loop for it (docs/yaver-smartwatch-voice-
// terminal.md §3, transport mode B/C). In the DEFAULT phone-paired mode the
// watch never reaches this — the phone app runs the carVoiceCoding loop and
// these routes are unused.
//
//   POST /watch/turn    {v,kind,text|token+reply|intent} → a wire reply
//   GET  /watch/result?taskId=…                          → poll a summary
//
// Both are owner-auth (s.auth), same bearer the watch gets from the
// RFC 8628 device-code flow — identical to the iOS/tvOS/mobile clients.
//
// The turn endpoint is intentionally NON-BLOCKING: it creates the task and
// returns immediately with a taskId, then the watch polls /watch/result.
// A wrist must never hold a 15-minute HTTP request open. The risk gate,
// read-code guard, intent expansion, and one-sentence summarization all
// live in watch_risk.go (pure, tested separately).
//
// Confirm is STATELESS: when a transcript needs confirmation we return a
// `confirm-needed` reply whose `token` is the base64 of the transcript.
// The watch echoes that token back as a `confirm` turn; we decode it and
// dispatch with the risk gate bypassed. No server-side pending-state map.

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"log"
	"net/http"
	"strings"
)

// watchTurnRequest is the v1 wire message a watch POSTs. Mirrors
// WatchProtocol.swift / WatchProtocol.kt and watchBridge.ts.
type watchTurnRequest struct {
	V       int    `json:"v"`
	Kind    string `json:"kind"`    // "transcript" | "confirm" | "intent"
	Text    string `json:"text"`    // kind=transcript
	Token   string `json:"token"`   // kind=confirm (base64 of the transcript)
	Reply   string `json:"reply"`   // kind=confirm: "confirm" | "cancel"
	Intent  string `json:"intent"`  // kind=intent
	Project string `json:"project"` // optional project slug
}

// watchReply is the v1 wire message we return. Mirrors the same three
// protocol definitions. Only the fields relevant to `Kind` are populated.
type watchReply struct {
	V      int    `json:"v"`
	Kind   string `json:"kind"` // ack|confirm-needed|working|summary|error|handoff
	Spoken string `json:"spoken,omitempty"`
	Token  string `json:"token,omitempty"`
	Prompt string `json:"prompt,omitempty"`
	TaskID string `json:"taskId,omitempty"`
	Status string `json:"status,omitempty"`
	Target string `json:"target,omitempty"`
}

func watchOK(kind string) watchReply { return watchReply{V: 1, Kind: kind} }

func (s *HTTPServer) handleWatchTurn(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.taskMgr == nil {
		writeJSON(w, http.StatusServiceUnavailable, watchReply{V: 1, Kind: "error", Spoken: "No runner is available."})
		return
	}
	var req watchTurnRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad json: "+err.Error(), http.StatusBadRequest)
		return
	}

	switch strings.ToLower(strings.TrimSpace(req.Kind)) {
	case "confirm":
		// A reply to a prior confirm-needed. Negation is the safe default:
		// anything that isn't an explicit "confirm" cancels.
		if !strings.EqualFold(strings.TrimSpace(req.Reply), "confirm") {
			writeJSON(w, http.StatusOK, watchReply{V: 1, Kind: "ack", Spoken: "Cancelled."})
			return
		}
		raw, err := base64.StdEncoding.DecodeString(req.Token)
		if err != nil || strings.TrimSpace(string(raw)) == "" {
			writeJSON(w, http.StatusBadRequest, watchReply{V: 1, Kind: "error", Spoken: "I lost what you were confirming."})
			return
		}
		// Risk gate already satisfied by the explicit confirm — dispatch.
		s.dispatchWatchTranscript(w, string(raw), req.Project)
		return

	case "intent":
		text := watchIntentToTranscript(req.Intent)
		if text == "" {
			writeJSON(w, http.StatusBadRequest, watchReply{V: 1, Kind: "error", Spoken: "I don't know that shortcut."})
			return
		}
		s.handleWatchTranscript(w, text, req.Project)
		return

	case "transcript", "":
		s.handleWatchTranscript(w, req.Text, req.Project)
		return

	default:
		writeJSON(w, http.StatusBadRequest, watchReply{V: 1, Kind: "error", Spoken: "I didn't understand that."})
	}
}

// handleWatchTranscript applies the two guards (read-code, risk) before
// dispatching. Shared by the transcript and intent paths.
func (s *HTTPServer) handleWatchTranscript(w http.ResponseWriter, text, project string) {
	clean := strings.TrimSpace(text)
	if clean == "" {
		writeJSON(w, http.StatusOK, watchReply{V: 1, Kind: "error", Spoken: "I didn't catch that."})
		return
	}
	if watchIsReadCodeRequest(clean) {
		writeJSON(w, http.StatusOK, watchReply{
			V: 1, Kind: "handoff", Target: "phone",
			Spoken: "I won't read code on your wrist — it'll be on your phone.",
		})
		return
	}
	if watchNeedsConfirm(clean) {
		writeJSON(w, http.StatusOK, watchReply{
			V: 1, Kind: "confirm-needed",
			Token:  base64.StdEncoding.EncodeToString([]byte(clean)),
			Prompt: watchConfirmPrompt(clean),
		})
		return
	}
	s.dispatchWatchTranscript(w, clean, project)
}

// dispatchWatchTranscript creates the task (non-blocking) with the
// wearable-watch viewport so the runner answers in one short sentence,
// then returns a `working` reply carrying the taskId to poll.
func (s *HTTPServer) dispatchWatchTranscript(w http.ResponseWriter, text, project string) {
	plan := buildWatchPrompt(text)
	vp := &TaskViewport{
		Surface:      "wearable-watch",
		Voice:        true,
		TTSEnabled:   true,
		TTSBudget:    watchReadbackMaxChars,
		Interaction:  "voice",
		VisualBudget: "glance",
		RiskPolicy:   "watch",
	}
	title := voiceTitleFromTranscript(text)
	taskOpts := TaskCreateOptions{Viewport: vp}
	meta := taskPlacementRequestFromTaskBody(taskPlacementRequestInput{
		Title:          title,
		Description:    plan.Prompt,
		Source:         "voice-input",
		ProjectName:    project,
		WorkDir:        s.taskMgr.workDir,
		TargetDeviceID: s.deviceID,
	})
	if previewPlacement, perr := s.previewTaskPlacement(context.Background(), meta); perr != nil {
		log.Printf("[placement] watch preview skipped before task create: %v", perr)
	} else if shouldDeferLocalTaskForPlacement(previewPlacement, s.deviceID) {
		pendingTaskID := newPendingCloudTaskID()
		recordedPlacement := previewPlacement
		if placement, rerr := s.recordTaskPlacement(context.Background(), pendingTaskID, meta); rerr != nil {
			log.Printf("[placement] watch pending record skipped for %s: %v", pendingTaskID, rerr)
		} else if placement != nil {
			recordedPlacement = placement
		}
		var activation map[string]any
		if recordedPlacement != nil && (recordedPlacement.PlacementID != "" || pendingTaskID != "") {
			if result, aerr := s.activateTaskPlacement(context.Background(), recordedPlacement.PlacementID, pendingTaskID); aerr != nil {
				activation = activationMapFromError(aerr)
				log.Printf("[placement] watch activation skipped for %s: %v", pendingTaskID, aerr)
			} else {
				activation = result
			}
		}
		status := "cloud_workspace_required"
		if action := cloudActivationBlockerAction(activation); action != "" {
			status = action
		}
		writeJSON(w, http.StatusOK, watchReply{
			V:      1,
			Kind:   "handoff",
			Target: "cloud-workspace",
			TaskID: pendingTaskID,
			Status: status,
			Spoken: "Your Cloud Workspace is getting ready. Continue on your phone.",
		})
		return
	} else if previewPlacement != nil {
		taskOpts.Placement = previewPlacement
	}
	task, err := s.taskMgr.CreateTaskWithOptions(
		title,
		plan.Prompt,
		"",            // model: task manager default
		"voice-input", // source: same arm as the car/voice loop
		"",            // runner: default
		"",            // customCommand
		nil,           // images
		taskOpts,
	)
	if err != nil {
		writeJSON(w, http.StatusOK, watchReply{V: 1, Kind: "error", Spoken: "I couldn't start that."})
		return
	}
	writeJSON(w, http.StatusOK, watchReply{
		V: 1, Kind: "working", TaskID: task.ID, Spoken: "On it.",
	})
}

// handleWatchResult is the poll the wrist hits until the task is terminal.
// Returns `working` while running, `summary` (one sentence) when done.
func (s *HTTPServer) handleWatchResult(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.taskMgr == nil {
		writeJSON(w, http.StatusServiceUnavailable, watchReply{V: 1, Kind: "error", Spoken: "No runner is available."})
		return
	}
	taskID := strings.TrimSpace(r.URL.Query().Get("taskId"))
	if taskID == "" {
		http.Error(w, "taskId is required", http.StatusBadRequest)
		return
	}
	task, ok := s.taskMgr.GetTask(taskID)
	if !ok {
		writeJSON(w, http.StatusNotFound, watchReply{V: 1, Kind: "error", Spoken: "I lost track of that."})
		return
	}
	if !isTerminalTaskStatus(task.Status) {
		writeJSON(w, http.StatusOK, watchReply{
			V: 1, Kind: "working", TaskID: task.ID, Status: string(task.Status),
		})
		return
	}
	writeJSON(w, http.StatusOK, watchReply{
		V:      1,
		Kind:   "summary",
		TaskID: task.ID,
		Status: string(task.Status),
		Spoken: summarizeForWatch(string(task.Status), voicePickResultText(task)),
	})
}
