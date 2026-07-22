package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

func init() {
	registerOpsVerb(opsVerbSpec{
		Name:        "runtime_turn",
		Description: "Surface-neutral development turn for watch/car/TV/AR/mobile/SDK. Minimal usage: {text:\"fix the app\",run:true,surface:{class:\"watch\"}}. Full usage can include target/session/evidence/queue metadata. Target remote runtimes with machine=<deviceId|primary>.",
		Schema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"utterance": map[string]interface{}{"type": "string"},
				"text":      map[string]interface{}{"type": "string"},
				"prompt":    map[string]interface{}{"type": "string"},
				"choice":    map[string]interface{}{"type": "string"},
				"mode":      map[string]interface{}{"type": "string"},
				"run":       map[string]interface{}{"type": "boolean"},
				"queue":     map[string]interface{}{"type": "boolean"},
				"target":    map[string]interface{}{"type": "object"},
				"surface":   map[string]interface{}{"type": "object"},
				"development": map[string]interface{}{
					"type": "object",
				},
			},
			"additionalProperties": true,
		},
		Handler:    opsRuntimeTurnHandler,
		Streaming:  false,
		AllowGuest: false,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "runtime_turns",
		Description: "List recent runtime_turn queue items for simple watch/car/TV/mobile status surfaces.",
		Schema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"limit": map[string]interface{}{"type": "number"},
			},
			"additionalProperties": false,
		},
		Handler:    opsRuntimeTurnsHandler,
		Streaming:  false,
		AllowGuest: false,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "runtime_turn_status",
		Description: "Read/refresh one runtime_turn queue item. Accepts {itemId:\"...\"} or {turnId:\"...\"}. If it created a task, this maps task status into queue states such as running, ready_to_test, or failed.",
		Schema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"itemId": map[string]interface{}{"type": "string"},
				"turnId": map[string]interface{}{"type": "string"},
			},
			"additionalProperties": false,
		},
		Handler:    opsRuntimeTurnStatusHandler,
		Streaming:  false,
		AllowGuest: false,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "runtime_turn_run",
		Description: "Promote a captured runtime_turn idea into real work. Accepts {turnId:\"rq_...\"}. Keeps the original turnId so surfaces don't show a duplicate.",
		Schema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"itemId": map[string]interface{}{"type": "string"},
				"turnId": map[string]interface{}{"type": "string"},
			},
			"additionalProperties": false,
		},
		Handler:    opsRuntimeTurnRunHandler,
		Streaming:  false,
		AllowGuest: false,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "runtime_turn_verify",
		Description: "Attempt the device reload for a runtime turn and report what actually happened. Returns testTarget.state=delivered (a live device accepted it) or unreachable (nothing is listening). Task-finished alone never means testable.",
		Schema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"itemId": map[string]interface{}{"type": "string"},
				"turnId": map[string]interface{}{"type": "string"},
			},
			"additionalProperties": false,
		},
		Handler:    opsRuntimeTurnVerifyHandler,
		Streaming:  false,
		AllowGuest: false,
	})
}

// runtimeTurnIDFromPayload reads the {itemId|turnId} shape shared by the
// status/run/verify verbs.
func runtimeTurnIDFromPayload(payload json.RawMessage) (string, *OpsResult) {
	var req struct {
		ItemID string `json:"itemId"`
		TurnID string `json:"turnId"`
	}
	if len(payload) > 0 {
		if err := json.Unmarshal(payload, &req); err != nil {
			return "", &OpsResult{OK: false, Code: "bad_payload", Error: "invalid payload: " + err.Error()}
		}
	}
	id := firstNonEmptyStr(strings.TrimSpace(req.ItemID), strings.TrimSpace(req.TurnID))
	if id == "" {
		return "", &OpsResult{OK: false, Code: "bad_payload", Error: "itemId or turnId is required"}
	}
	return id, nil
}

func opsRuntimeTurnRunHandler(c OpsContext, payload json.RawMessage) OpsResult {
	id, bad := runtimeTurnIDFromPayload(payload)
	if bad != nil {
		return *bad
	}
	resp := runtimeTurnRun(c, id)
	if !resp.OK {
		return OpsResult{OK: false, Code: firstNonEmptyStr(resp.Code, "runtime_turn_failed"), Error: firstNonEmptyStr(resp.Error, resp.Spoken, "could not start that turn"), Initial: resp}
	}
	return OpsResult{OK: true, Initial: resp}
}

func opsRuntimeTurnVerifyHandler(c OpsContext, payload json.RawMessage) OpsResult {
	id, bad := runtimeTurnIDFromPayload(payload)
	if bad != nil {
		return *bad
	}
	resp := runtimeTurnVerify(c, id)
	if !resp.OK {
		return OpsResult{OK: false, Code: firstNonEmptyStr(resp.Code, "not_verified"), Error: firstNonEmptyStr(resp.Error, resp.Spoken, "reload not delivered"), Initial: resp}
	}
	return OpsResult{OK: true, Initial: resp}
}

func opsRuntimeTurnHandler(c OpsContext, payload json.RawMessage) OpsResult {
	var req RuntimeTurnRequest
	if len(payload) > 0 {
		if err := json.Unmarshal(payload, &req); err != nil {
			return OpsResult{OK: false, Code: "bad_payload", Error: "invalid payload: " + err.Error()}
		}
	}
	normalizeRuntimeTurnRequest(&req)
	resp := executeRuntimeTurn(c, req)
	if !resp.OK {
		return OpsResult{OK: false, Code: firstNonEmptyStr(resp.Code, "runtime_turn_failed"), Error: firstNonEmptyStr(resp.Error, resp.Spoken, "runtime turn failed"), Initial: resp}
	}
	return OpsResult{OK: true, Initial: resp}
}

func opsRuntimeTurnsHandler(c OpsContext, payload json.RawMessage) OpsResult {
	var req struct {
		Limit int `json:"limit"`
	}
	if len(payload) > 0 {
		if err := json.Unmarshal(payload, &req); err != nil {
			return OpsResult{OK: false, Code: "bad_payload", Error: "invalid payload: " + err.Error()}
		}
	}
	items := runtimeQueue.list(c.ActorUserID, req.Limit)
	if c.Server != nil && c.Server.taskMgr != nil {
		refreshed := false
		for _, item := range items {
			// Terminal items can never change again. Refreshing them cost a
			// task lookup per poll and, before the fingerprint guard landed,
			// reshuffled the whole list on every phone poll.
			if item.TaskID != "" && !isRuntimeQueueTerminal(item.State) {
				_ = runtimeTurnStatus(c, item.ItemID)
				refreshed = true
			}
		}
		if refreshed {
			items = runtimeQueue.list(c.ActorUserID, req.Limit)
		}
	}
	return OpsResult{OK: true, Initial: RuntimeTurnListResponse{OK: true, Items: items, Count: len(items)}}
}

func opsRuntimeTurnStatusHandler(c OpsContext, payload json.RawMessage) OpsResult {
	var req struct {
		ItemID string `json:"itemId"`
		TurnID string `json:"turnId"`
	}
	if len(payload) > 0 {
		if err := json.Unmarshal(payload, &req); err != nil {
			return OpsResult{OK: false, Code: "bad_payload", Error: "invalid payload: " + err.Error()}
		}
	}
	req.ItemID = firstNonEmptyStr(strings.TrimSpace(req.ItemID), strings.TrimSpace(req.TurnID))
	if req.ItemID == "" {
		return OpsResult{OK: false, Code: "bad_payload", Error: "itemId or turnId is required"}
	}
	resp := runtimeTurnStatus(c, req.ItemID)
	if !resp.OK {
		return OpsResult{OK: false, Code: firstNonEmptyStr(resp.Code, "not_found"), Error: firstNonEmptyStr(resp.Error, "runtime turn not found"), Initial: resp}
	}
	return OpsResult{OK: true, Initial: resp}
}

func normalizeRuntimeTurnRequest(req *RuntimeTurnRequest) {
	if req == nil {
		return
	}
	req.Utterance = firstNonEmptyStr(strings.TrimSpace(req.Utterance), strings.TrimSpace(req.Text), strings.TrimSpace(req.Prompt))
	if req.Run && strings.TrimSpace(req.Development.Queue.Mode) == "" {
		req.Development.Queue.Mode = "run"
	}
	if req.Queue && strings.TrimSpace(req.Development.Queue.Mode) == "" {
		req.Development.Queue.Mode = "enqueue-or-run"
	}
	if strings.EqualFold(strings.TrimSpace(req.Mode), "run") && strings.TrimSpace(req.Development.Queue.Mode) == "" {
		req.Development.Queue.Mode = "run"
	}
	if req.Surface.Class == "" && req.Surface.ID != "" {
		req.Surface.Class = req.Surface.ID
	}
}

func executeRuntimeTurn(c OpsContext, req RuntimeTurnRequest) RuntimeTurnResponse {
	req.Utterance = strings.TrimSpace(req.Utterance)
	req.Choice = strings.TrimSpace(req.Choice)
	intentClass := classifyRuntimeTurn(req)
	if req.Utterance == "" && req.Choice == "" {
		return RuntimeTurnResponse{OK: false, State: runtimeQueueStateFailed, Code: "bad_payload", Error: "utterance or choice is required", Spoken: "I didn't catch that."}
	}

	item := runtimeQueue.add(&RuntimeTurnQueueItem{
		OwnerUserID: c.ActorUserID,
		State:       runtimeQueueStateQueued,
		Utterance:   req.Utterance,
		IntentClass: intentClass,
		Target:      req.Target,
		Surface:     req.Surface,
		Evidence:    append([]RuntimeTurnEvidence{}, req.Development.Evidence...),
		Reason:      runtimeTurnReason(req, intentClass),
		Meta:        req.Development.Meta,
	})

	if intentClass == "idea-capture" && !runtimeTurnShouldRun(req) {
		item, _ = runtimeQueue.update(item.ItemID, func(i *RuntimeTurnQueueItem) {
			i.State = runtimeQueueStateCaptured
			i.Spoken = "Captured. Say run it, or start it from your phone."
		})
		return runtimeTurnResponseFromItem(item, "Captured. Say run it, or start it from your phone.", true)
	}

	if shouldUseRuntimeSessionTurn(req, intentClass) {
		reply, status := executeRunnerSessionTurn(runnerSessionTurnRequest{
			Session: req.Target.Session,
			Runner:  req.Target.Runner,
			Text:    runtimeTurnText(req, intentClass),
			Choice:  runtimeTurnChoice(req),
			WaitMs:  runtimeTurnWaitMs(req),
		})
		spoken := summarizeRunnerTurnForSpeech(reply)
		state := runtimeQueueStateRunning
		if reply.AwaitingChoice {
			state = runtimeQueueStateNeedsInput
		}
		if status != http.StatusOK && status != http.StatusConflict {
			state = runtimeQueueStateFailed
		}
		item, _ = runtimeQueue.update(item.ItemID, func(i *RuntimeTurnQueueItem) {
			i.State = state
			i.Session = reply.Session
			i.Runner = reply.Runner
			i.Spoken = spoken
			if reply.Error != "" {
				i.Error = reply.Error
			}
		})
		resp := runtimeTurnResponseFromItem(item, spoken, status == http.StatusOK || status == http.StatusConflict)
		resp.AwaitingChoice = reply.AwaitingChoice
		resp.Options = reply.Options
		if reply.Pane != "" && !isVoiceSurface(req.Surface.Class) {
			resp.Panel = map[string]string{"kind": "session_pane", "text": reply.Pane}
		}
		if reply.Error != "" {
			resp.Error = reply.Error
			resp.Code = runnerTurnErrorCode(status)
		}
		return resp
	}

	return startRuntimeTurnTask(c, req, intentClass, item.ItemID)
}

// startRuntimeTurnTask creates the backing task for an existing queue item and
// folds the outcome back into that same item. It is shared by the initial
// runtime_turn dispatch and by runtime_turn_run promoting a captured idea, so a
// promoted idea keeps its original turnId instead of forking a second row that
// the phone would render as a duplicate.
func startRuntimeTurnTask(c OpsContext, req RuntimeTurnRequest, intentClass, itemID string) RuntimeTurnResponse {
	if c.Server == nil || c.Server.taskMgr == nil {
		item, _ := runtimeQueue.update(itemID, func(i *RuntimeTurnQueueItem) {
			i.State = runtimeQueueStateFailed
			i.Error = "task manager unavailable"
			i.Spoken = "No runner is available."
		})
		return runtimeTurnResponseFromItem(item, "No runner is available.", false)
	}

	task, err := c.Server.taskMgr.CreateTaskWithOptions(
		runtimeTurnTitle(req),
		runtimeTurnPrompt(req, intentClass),
		"",
		"runtime-turn",
		req.Target.Runner,
		"",
		nil,
		TaskCreateOptions{
			WorkDir:  req.Target.WorkDir,
			Viewport: runtimeViewportFromSurface(req.Surface),
		},
	)
	if err != nil {
		item, _ := runtimeQueue.update(itemID, func(i *RuntimeTurnQueueItem) {
			i.State = runtimeQueueStateFailed
			i.Error = err.Error()
			i.Spoken = "I couldn't start that."
		})
		return runtimeTurnResponseFromItem(item, "I couldn't start that.", false)
	}
	item, _ := runtimeQueue.update(itemID, func(i *RuntimeTurnQueueItem) {
		i.State = runtimeQueueStateRunning
		i.TaskID = task.ID
		i.Runner = task.RunnerID
		i.Spoken = "Added to the remote runner queue."
	})
	resp := runtimeTurnResponseFromItem(item, "Added to the remote runner queue.", true)
	resp.Haptic = "start"
	return resp
}

// runtimeTurnRun promotes a previously captured idea into real work. Without
// this, `captured` was a black hole: the surface said "I'll attach it to the
// current app" and no code path ever moved the item out of that state again.
func runtimeTurnRun(c OpsContext, itemID string) RuntimeTurnResponse {
	item, ok := runtimeQueue.get(c.ActorUserID, itemID)
	if !ok {
		return RuntimeTurnResponse{OK: false, State: runtimeQueueStateFailed, Code: "not_found", Error: "runtime turn not found", Spoken: "I lost track of that."}
	}
	if item.TaskID != "" {
		return runtimeTurnResponseFromItem(item, "That's already running.", true)
	}
	if isRuntimeQueueTerminal(item.State) {
		return RuntimeTurnResponse{OK: false, State: item.State, Code: "already_terminal", Error: "runtime turn is already " + item.State, Spoken: "That one's already finished."}
	}
	req := RuntimeTurnRequest{
		Utterance: item.Utterance,
		Target:    item.Target,
		Surface:   item.Surface,
		Development: RuntimeTurnDevelopment{
			Evidence: item.Evidence,
			Meta:     item.Meta,
		},
	}
	// A captured idea promoted on purpose is no longer "capture only" — the
	// user explicitly asked for it to run, so it must take the coding path.
	intentClass := item.IntentClass
	if intentClass == "idea-capture" {
		intentClass = "start-coding"
	}
	return startRuntimeTurnTask(c, req, intentClass, item.ItemID)
}

// runtimeTurnVerify attempts the reload and reports what actually happened.
//
// This exists because task-finished was being sold to the user as "ready to
// test". It is the inventory-vs-operation trap: the task manager's inventory
// says the work is done, but the operation the user cares about — the app
// reloading on a device they can touch — may never have been attempted, and a
// phone that registered a session but is not holding the command stream counts
// for nothing. BroadcastCommand returns the number of LIVE listeners, so we
// attempt the reload and report that number rather than assuming delivery.
func runtimeTurnVerify(c OpsContext, itemID string) RuntimeTurnResponse {
	item, ok := runtimeQueue.get(c.ActorUserID, itemID)
	if !ok {
		return RuntimeTurnResponse{OK: false, State: runtimeQueueStateFailed, Code: "not_found", Error: "runtime turn not found", Spoken: "I lost track of that."}
	}
	if c.Server == nil || c.Server.blackboxMgr == nil {
		return RuntimeTurnResponse{OK: false, State: item.State, Code: "unavailable", Error: "no device command channel on this agent", Spoken: "I can't reach a device from here."}
	}

	delivered := c.Server.blackboxMgr.BroadcastCommand(BlackBoxCommand{Command: "reload"})
	tt := &RuntimeTurnTestTarget{
		Kind:        "yaver-mobile-container",
		DeviceID:    item.Target.DeviceID,
		Listeners:   delivered,
		AttemptedAt: time.Now().UTC(),
	}
	spoken := ""
	if delivered > 0 {
		tt.State = "delivered"
		tt.Detail = "reload accepted by a live device command stream"
		spoken = "Reloaded. Take a look."
	} else {
		// The honest answer. Saying "ready to test" here is what sent users
		// hunting through a phone that was never listening.
		tt.State = "unreachable"
		tt.Detail = "no device is holding the command stream; open Yaver on your phone and try again"
		spoken = "Nothing's listening. Open Yaver on your phone."
	}
	item, _ = runtimeQueue.update(item.ItemID, func(i *RuntimeTurnQueueItem) {
		i.TestTarget = tt
		i.Spoken = spoken
	})
	resp := runtimeTurnResponseFromItem(item, spoken, delivered > 0)
	resp.TestTarget = tt
	if delivered == 0 {
		resp.Code = "no_listener"
		resp.Haptic = "attention"
	}
	return resp
}

func runtimeTurnStatus(c OpsContext, itemID string) RuntimeTurnResponse {
	item, ok := runtimeQueue.get(c.ActorUserID, itemID)
	if !ok {
		return RuntimeTurnResponse{OK: false, State: runtimeQueueStateFailed, Code: "not_found", Error: "runtime turn not found", Spoken: "I lost track of that."}
	}
	if item.TaskID != "" && c.Server != nil && c.Server.taskMgr != nil {
		if task, ok := c.Server.taskMgr.GetTask(item.TaskID); ok {
			item, _ = runtimeQueue.update(item.ItemID, func(i *RuntimeTurnQueueItem) {
				i.State = runtimeQueueStateFromTask(task.Status)
				i.Spoken = runtimeTurnSpokenFromTask(task)
				if task.Status == TaskStatusFailed {
					i.Error = runtimeTurnTaskText(task)
				}
			})
		}
	}
	return runtimeTurnResponseFromItem(item, item.Spoken, item.State != runtimeQueueStateFailed)
}

func runtimeQueueStateFromTask(status TaskStatus) string {
	switch status {
	case TaskStatusQueued:
		return runtimeQueueStateQueued
	case TaskStatusRunning:
		return runtimeQueueStateRunning
	case TaskStatusReview:
		return runtimeQueueStateNeedsInput
	case TaskStatusFinished:
		return runtimeQueueStateReadyToTest
	case TaskStatusFailed, TaskStatusStopped:
		return runtimeQueueStateFailed
	default:
		return runtimeQueueStateRunning
	}
}

func runtimeTurnSpokenFromTask(task *Task) string {
	if task == nil {
		return ""
	}
	switch task.Status {
	case TaskStatusFinished:
		// Deliberately not "you can test it" — nothing has reloaded yet. The
		// user has to ask for that, and runtime_turn_verify reports whether a
		// device was actually listening.
		return "Code's done. Say test it to push it to your phone."
	case TaskStatusFailed:
		return "That failed. I sent the details to your phone."
	case TaskStatusReview:
		return "It needs your review."
	case TaskStatusQueued:
		return "Queued."
	default:
		return "Working."
	}
}

func runtimeTurnTaskText(task *Task) string {
	if task == nil {
		return ""
	}
	if s := strings.TrimSpace(task.ResultText); s != "" {
		return s
	}
	return strings.TrimSpace(task.Output)
}

func runtimeTurnResponseFromItem(item RuntimeTurnQueueItem, spoken string, ok bool) RuntimeTurnResponse {
	resp := RuntimeTurnResponse{
		OK:     ok,
		TurnID: item.ItemID,
		State:  item.State,
		Spoken: firstNonEmptyStr(spoken, item.Spoken),
		Queue:  &item,
		Target: item.Target,
		Reason: item.Reason,
		Glance: map[string]string{
			"title": firstNonEmptyStr(item.Target.Project, item.Target.DeviceAlias, item.Target.DeviceID, "Current app"),
			"line":  firstNonEmptyStr(spoken, item.Spoken, item.State),
		},
	}
	if item.Error != "" {
		resp.Error = item.Error
	}
	if item.State == runtimeQueueStateReadyToTest {
		// Report what we actually know. A finished task proves code changed,
		// never that anything reloaded on a device — so the default is
		// "unverified" until runtime_turn_verify attempts the real reload and
		// records a listener count.
		if item.TestTarget != nil {
			resp.TestTarget = item.TestTarget
		} else {
			resp.TestTarget = &RuntimeTurnTestTarget{
				Kind:   "yaver-mobile-container",
				State:  "unverified",
				Detail: "code work finished; no device reload attempted yet",
			}
		}
		resp.Haptic = "success"
	}
	if item.State == runtimeQueueStateNeedsInput {
		resp.Haptic = "attention"
	}
	if item.State == runtimeQueueStateFailed {
		resp.Haptic = "failure"
	}
	return resp
}

func runtimeTurnShouldRun(req RuntimeTurnRequest) bool {
	mode := strings.ToLower(strings.TrimSpace(req.Development.Queue.Mode))
	return mode == "enqueue" || mode == "enqueue-or-run" || mode == "run" || strings.EqualFold(strings.TrimSpace(req.Mode), "run")
}

func shouldUseRuntimeSessionTurn(req RuntimeTurnRequest, intentClass string) bool {
	if req.Choice != "" || runtimeTurnChoice(req) != "" {
		return true
	}
	if strings.TrimSpace(req.Target.Session) != "" {
		return true
	}
	switch intentClass {
	case "session-turn":
		return true
	default:
		return false
	}
}

func runtimeTurnChoice(req RuntimeTurnRequest) string {
	if c := strings.TrimSpace(req.Choice); c != "" {
		return c
	}
	return runtimeChoiceFromUtterance(req.Utterance)
}

func runtimeTurnText(req RuntimeTurnRequest, intentClass string) string {
	if runtimeTurnChoice(req) != "" && intentClass == "session-turn" {
		return ""
	}
	return strings.TrimSpace(req.Utterance)
}

func runtimeTurnWaitMs(req RuntimeTurnRequest) int {
	switch strings.ToLower(strings.TrimSpace(req.Surface.Class)) {
	case "watch", "wearable-watch":
		return 4000
	case "car", "car-audio":
		return 6000
	default:
		return 6000
	}
}

func runtimeTurnTitle(req RuntimeTurnRequest) string {
	if g := strings.TrimSpace(req.Development.Goal); g != "" {
		return voiceTitleFromTranscript(g)
	}
	return voiceTitleFromTranscript(req.Utterance)
}

func runtimeTurnPrompt(req RuntimeTurnRequest, intentClass string) string {
	base := strings.TrimSpace(req.Utterance)
	if base == "" {
		base = strings.TrimSpace(req.Development.Goal)
	}
	lines := []string{
		"Surface-neutral Yaver development turn.",
		"Treat this as development vibing against the selected remote runtime.",
		"Use attached evidence references when available; do not assume private logs or screenshots are synced unless present.",
		"Do not deploy, publish, push tags, or release externally without explicit user confirmation.",
		"Finish with a short test-ready summary.",
		"",
		"Intent class: " + intentClass,
	}
	if intentClass == "idea-capture" {
		lines = append(lines,
			"Treat this as idea capture first: preserve the product thought, infer the likely app/context, write a concise implementation note, and identify the next coding step.",
			"Do not edit code unless the utterance explicitly asks to implement now.",
		)
	} else {
		lines = append(lines, "Make focused code/config changes, run the smallest useful check, and leave detailed output for phone/web.")
	}
	if req.Development.Goal != "" {
		lines = append(lines, "Goal: "+strings.TrimSpace(req.Development.Goal))
	}
	if len(req.Development.Evidence) > 0 {
		lines = append(lines, "Evidence references:")
		for _, ev := range req.Development.Evidence {
			lines = append(lines, "- "+strings.TrimSpace(ev.Kind)+" "+strings.TrimSpace(ev.Ref)+" "+strings.TrimSpace(ev.Screen))
		}
	}
	lines = append(lines, "", "User utterance: "+base)
	return strings.Join(lines, "\n")
}

func runtimeTurnReason(req RuntimeTurnRequest, intentClass string) string {
	target := firstNonEmptyStr(req.Target.Project, req.Target.DeviceAlias, req.Target.DeviceID, "current context")
	switch intentClass {
	case "idea-capture":
		return "captured as an idea for " + target
	case "autorun":
		return "queued for async development on " + target
	case "session-turn":
		return "routed to the live runner session for " + target
	case "goal":
		return "captured as goal-oriented work for " + target
	default:
		return "queued for development work on " + target
	}
}
