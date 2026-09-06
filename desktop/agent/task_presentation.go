package main

// task_presentation.go is the runner -> Yaver surface presentation contract.
//
// Runner stdout is evidence, not a user interface. Codex, Claude Code and
// OpenCode each render an excellent local TUI because their renderer still
// knows which payload is assistant prose, progress, a tool call or a diff.
// Once those bytes are flattened into a PTY stream that meaning is gone. This
// contract preserves the meaning beside (never instead of) the lossless raw
// lane so every remote surface can show a calm human answer and keep the
// terminal transcript folded for diagnosis.

import (
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const (
	// Schema 1 is intentionally additive: clients render text + visibility
	// generically, so future Go-agent activity kinds do not require a mobile
	// release just to remain readable.
	TaskPresentationSchema      = 1
	maxTaskPresentationMessages = 64
	maxTaskPresentationText     = 32 * 1024
	maxTaskPresentationTotal    = 64 * 1024
	maxTaskPresentationListText = 4 * 1024
)

// TaskPresentationMessage is a surface-safe semantic message. Kind is one of
// message, status, action_required, warning, error, tool or patch. Detailed
// command lines, paths, patches and runner dumps do not belong here; they ride
// command_* events or the raw stream.
type TaskPresentationMessage struct {
	ID         string    `json:"id"`
	Kind       string    `json:"kind"`
	Role       string    `json:"role,omitempty"`
	Text       string    `json:"text"`
	Visibility string    `json:"visibility,omitempty"`
	Phase      string    `json:"phase,omitempty"`
	State      string    `json:"state,omitempty"`
	Runner     string    `json:"runner,omitempty"`
	Project    string    `json:"project,omitempty"`
	Machine    string    `json:"machine,omitempty"`
	Platform   string    `json:"platform,omitempty"`
	Surface    string    `json:"surface,omitempty"`
	CreatedAt  time.Time `json:"createdAt"`
	UpdatedAt  time.Time `json:"updatedAt"`
}

// TaskPresentationEvent rides task SSE. op=append extends the text of an
// existing message; op=upsert replaces it. A presentation_snapshot is replayed
// on every subscription, so a dropped live delta is self-healing.
type TaskPresentationEvent struct {
	Type    string                   `json:"type"`
	Schema  int                      `json:"schema"`
	Op      string                   `json:"op"`
	Seq     int64                    `json:"seq"`
	Message *TaskPresentationMessage `json:"message,omitempty"`
}

type taskPresentationInput struct {
	ID         string
	Kind       string
	Role       string
	Text       string
	Phase      string
	State      string
	Surface    string
	Visibility string
	Append     bool
}

func normalizePresentationKind(kind string) string {
	kind = strings.ToLower(strings.TrimSpace(kind))
	if kind == "" || len(kind) > 64 {
		return "status"
	}
	for _, r := range kind {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '_' && r != '-' {
			return "status"
		}
	}
	return kind
}

func normalizePresentationVisibility(visibility string) string {
	if strings.EqualFold(strings.TrimSpace(visibility), "details") {
		return "details"
	}
	return "primary"
}

func trimPresentationText(text string) string {
	if len(text) <= maxTaskPresentationText {
		return text
	}
	return "…" + text[len(text)-maxTaskPresentationText:]
}

// present records and streams one semantic update. It is safe to call from
// runner reader goroutines. Do not call it while holding tm.mu; terminal paths
// that already hold the lock use presentLocked and stream after unlocking.
func (tm *TaskManager) present(task *Task, in taskPresentationInput) {
	if tm == nil || task == nil || strings.TrimSpace(in.Text) == "" {
		return
	}
	tm.mu.Lock()
	ev := tm.presentLocked(task, in)
	// Token deltas can arrive hundreds of times per second; persist the
	// authoritative upsert/status boundaries, never each append fragment. The
	// first real token replaces the immediate acknowledgement, so it is an
	// upsert even though the runner sent it through the append API.
	if ev.Op != "append" {
		tm.persistAsync()
	}
	tm.mu.Unlock()
	if ev.Message != nil {
		emitTaskEvent(task, map[string]interface{}{
			"type": "presentation", "schema": ev.Schema, "op": ev.Op,
			"seq": ev.Seq, "message": ev.Message,
		})
	}
}

func (tm *TaskManager) presentLocked(task *Task, in taskPresentationInput) TaskPresentationEvent {
	now := time.Now()
	id := strings.TrimSpace(in.ID)
	if id == "" {
		id = task.ID + "-presentation-" + now.Format("150405.000000")
	}
	text := in.Text
	// Assistant messages cross a deliberate product boundary. The runner may
	// output a patch or terminal transcript despite its instructions; clients
	// must never need to identify that themselves.
	if normalizePresentationKind(in.Kind) == "message" && strings.EqualFold(strings.TrimSpace(in.Role), "assistant") && !in.Append {
		text = humanReadableRunnerAnswer(text)
	}
	op := "upsert"
	idx := -1
	for i := range task.Presentation {
		if task.Presentation[i].ID == id {
			idx = i
			break
		}
	}
	if in.Append && idx >= 0 && task.Presentation[idx].State != "acknowledged" {
		text = task.Presentation[idx].Text + text
		op = "append"
	}
	text = trimPresentationText(text)
	host, _ := os.Hostname()
	createdAt := now
	if idx >= 0 {
		createdAt = task.Presentation[idx].CreatedAt
	}
	msg := TaskPresentationMessage{
		ID: id, Kind: normalizePresentationKind(in.Kind), Role: strings.TrimSpace(in.Role),
		Text: text, Visibility: normalizePresentationVisibility(in.Visibility),
		Phase: strings.TrimSpace(in.Phase), State: strings.TrimSpace(in.State),
		Runner: normalizeRunnerID(task.RunnerID), Project: strings.TrimSpace(task.ProjectName),
		Machine: host, Platform: runtime.GOOS + "/" + runtime.GOARCH,
		Surface: strings.TrimSpace(in.Surface), CreatedAt: createdAt, UpdatedAt: now,
	}
	if idx >= 0 {
		task.Presentation[idx] = msg
	} else {
		task.Presentation = append(task.Presentation, msg)
		if len(task.Presentation) > maxTaskPresentationMessages {
			task.Presentation = append([]TaskPresentationMessage(nil), task.Presentation[len(task.Presentation)-maxTaskPresentationMessages:]...)
		}
	}
	// Bound the persisted semantic lane as a whole, not just each row. Without
	// this, 64 valid 32 KB messages turn every task detail into a multi-megabyte
	// poll. Preserve the newest state and answer; lossless history remains in
	// Turns and the raw runner lane.
	for presentationTextBytes(task.Presentation) > maxTaskPresentationTotal && len(task.Presentation) > 1 {
		remove := 0
		if task.Presentation[remove].ID == id {
			remove = 1
		}
		task.Presentation = append(task.Presentation[:remove], task.Presentation[remove+1:]...)
	}
	task.PresentationSeq++
	// append events carry only the delta; snapshots and upserts carry the
	// complete text. This keeps token streaming O(n) while reconnect remains
	// authoritative.
	wire := msg
	if op == "append" {
		wire.Text = in.Text
	}
	return TaskPresentationEvent{Type: "presentation", Schema: TaskPresentationSchema, Op: op, Seq: task.PresentationSeq, Message: &wire}
}

// taskAssistantPresentationID identifies the one visible assistant message for
// the active conversation turn. Admission, live token streaming and final
// completion all upsert this ID so an immediate acknowledgement evolves into
// the real answer instead of becoming a duplicate chat turn.
func taskAssistantPresentationID(task *Task) string {
	if task == nil {
		return "assistant"
	}
	return task.ID + "-assistant-" + strconv.Itoa(len(task.Turns)+1)
}

func taskAcceptedPresentation(task *Task) taskPresentationInput {
	return taskPresentationInput{
		ID: taskAssistantPresentationID(task), Kind: "message", Role: "assistant",
		Text:  "I’m on it. I’ll keep clear progress and the final result visible here.",
		Phase: "accepted", State: "acknowledged", Surface: task.LastSurface,
	}
}

func presentationTextBytes(messages []TaskPresentationMessage) int {
	total := 0
	for i := range messages {
		total += len(messages[i].Text)
	}
	return total
}

// taskHasSemanticAssistantTextLocked reports whether text is already backed by
// the runner's semantic presentation lane. Callers hold TaskManager.mu. A
// ResultText value without this evidence is compatibility/terminal material:
// it remains available in Details but must not become a visible chat turn.
func taskHasSemanticAssistantTextLocked(task *Task, text string) bool {
	if task == nil || strings.TrimSpace(text) == "" {
		return false
	}
	want := strings.TrimSpace(text)
	for i := len(task.Presentation) - 1; i >= 0; i-- {
		message := task.Presentation[i]
		if message.Kind == "message" && message.Role == "assistant" && strings.TrimSpace(message.Text) == want {
			return true
		}
	}
	return false
}

func taskRunningPresentation(task *Task) taskPresentationInput {
	// Once the runner has resolved a model, that is the useful conversation
	// identity on every client surface. Repeating "Codex" or "OpenCode" in a
	// task/follow-up status wastes the one short line the user is watching and
	// hides whether /model actually took effect.
	runner := strings.TrimSpace(task.Model)
	if runner == "" {
		runner = strings.TrimSpace(task.runner.Model)
	}
	effort := strings.TrimSpace(task.ReasoningEffort)
	if effort == "" {
		effort = strings.TrimSpace(task.runner.ReasoningEffort)
	}
	if runner != "" && effort != "" {
		runner += " · " + effort
	}
	if runner == "" {
		runner = strings.TrimSpace(task.RunnerName)
	}
	if runner == "" {
		runner = strings.TrimSpace(task.RunnerID)
	}
	if runner == "" {
		runner = "Coding runner"
	}
	project := strings.TrimSpace(task.ProjectName)
	text := runner + " is working"
	if project != "" {
		text += " on " + project
	}
	return taskPresentationInput{ID: task.ID + "-activity", Kind: "status", Text: text + ".", Phase: "coding", State: "running", Surface: task.LastSurface}
}

// taskPresentationSnapshot derives the activity row from authoritative task
// state at read time. This prevents a process that exits between its last SSE
// delta and persistence from leaving "is working" on a completed task.
func taskPresentationSnapshot(task *Task) []TaskPresentationMessage {
	if task == nil {
		return nil
	}
	out := append([]TaskPresentationMessage(nil), task.Presentation...)
	for i := range out {
		if out[i].ID != task.ID+"-activity" {
			continue
		}
		project := strings.TrimSpace(task.ProjectName)
		suffix := "."
		if project != "" {
			suffix = " on " + project + "."
		}
		switch task.Status {
		case TaskStatusFinished:
			out[i].Text, out[i].Phase, out[i].State = "Completed"+suffix, "complete", "completed"
		case TaskStatusReady:
			out[i].Text, out[i].Phase, out[i].State = "Waiting for your next message"+suffix, "conversation", "ready"
		case TaskStatusReview:
			out[i].Text, out[i].Phase, out[i].State = "The runner says the work is fully complete"+suffix, "review", "review"
		case TaskStatusFailed:
			out[i].Text, out[i].Phase, out[i].State, out[i].Kind = "The task needs attention"+suffix, "blocked", "failed", "error"
		case TaskStatusStopped:
			out[i].Text, out[i].Phase, out[i].State = "Stopped"+suffix, "stopped", "stopped"
		case TaskStatusQueued:
			out[i].Text, out[i].Phase, out[i].State = "Waiting to start"+suffix, "queued", "queued"
		}
	}
	return out
}

// Lists refresh frequently and only need the newest human state plus newest
// assistant answer. Detail and SSE retain the bounded full semantic snapshot.
func taskPresentationListSnapshot(task *Task) []TaskPresentationMessage {
	all := taskPresentationSnapshot(task)
	if len(all) == 0 {
		return nil
	}
	selected := make([]TaskPresentationMessage, 0, 2)
	seen := map[string]bool{}
	for i := len(all) - 1; i >= 0 && len(selected) < 2; i-- {
		message := all[i]
		if message.Kind != "message" && message.Kind != "status" && message.Kind != "action_required" && message.Kind != "warning" && message.Kind != "error" {
			continue
		}
		group := "status"
		if message.Kind == "message" && message.Role == "assistant" {
			group = "assistant"
		}
		if seen[group] || (group == "status" && message.Kind == "message") {
			continue
		}
		seen[group] = true
		if len(message.Text) > maxTaskPresentationListText {
			message.Text = "…" + message.Text[len(message.Text)-maxTaskPresentationListText:]
		}
		selected = append(selected, message)
	}
	for left, right := 0, len(selected)-1; left < right; left, right = left+1, right-1 {
		selected[left], selected[right] = selected[right], selected[left]
	}
	return selected
}

func taskInfoFriendlyPresentation(info *TaskInfo) *TaskPresentationMessage {
	if info == nil {
		return nil
	}
	var state *TaskPresentationMessage
	for i := range info.Presentation {
		message := &info.Presentation[i]
		if strings.TrimSpace(message.Text) == "" {
			continue
		}
		if message.Kind == "message" && message.Role == "assistant" {
			state = message
			continue
		}
		if state == nil && (message.Kind == "status" || message.Kind == "action_required" || message.Kind == "warning" || message.Kind == "error") {
			state = message
		}
	}
	return state
}
