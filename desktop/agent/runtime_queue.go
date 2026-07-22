package main

import (
	"strings"
	"time"
)

const (
	runtimeQueueStateCaptured      = "captured"
	runtimeQueueStateQueued        = "queued"
	runtimeQueueStateRunning       = "running"
	runtimeQueueStateNeedsInput    = "needs_input"
	runtimeQueueStateReadyToTest   = "ready_to_test"
	runtimeQueueStateReadyToDeploy = "ready_to_deploy"
	runtimeQueueStateDone          = "done"
	runtimeQueueStateFailed        = "failed"
	runtimeQueueStateCancelled     = "cancelled"
)

type RuntimeTurnEvidence struct {
	Kind          string `json:"kind,omitempty"`
	Ref           string `json:"ref,omitempty"`
	SourceSurface string `json:"sourceSurface,omitempty"`
	Screen        string `json:"screen,omitempty"`
	DurationMs    int    `json:"durationMs,omitempty"`
}

type RuntimeTurnTarget struct {
	DeviceID    string `json:"deviceId,omitempty"`
	DeviceAlias string `json:"deviceAlias,omitempty"`
	Session     string `json:"session,omitempty"`
	Runner      string `json:"runner,omitempty"`
	Project     string `json:"project,omitempty"`
	WorkDir     string `json:"workDir,omitempty"`
}

type RuntimeTurnSurface struct {
	ID           string `json:"id,omitempty"`
	Class        string `json:"class,omitempty"`
	Interaction  string `json:"interaction,omitempty"`
	VisualBudget string `json:"visualBudget,omitempty"`
	TTSBudget    int    `json:"ttsBudget,omitempty"`
	RiskPolicy   string `json:"riskPolicy,omitempty"`
	ReplyTo      string `json:"replyTo,omitempty"`
}

type RuntimeTurnQueuePrefs struct {
	Mode        string   `json:"mode,omitempty"`
	Priority    string   `json:"priority,omitempty"`
	AfterFinish []string `json:"afterFinish,omitempty"`
}

type RuntimeTurnDevelopment struct {
	Goal        string                 `json:"goal,omitempty"`
	IntentClass string                 `json:"intentClass,omitempty"`
	Evidence    []RuntimeTurnEvidence  `json:"evidence,omitempty"`
	Queue       RuntimeTurnQueuePrefs  `json:"queue,omitempty"`
	Meta        map[string]interface{} `json:"meta,omitempty"`
}

type RuntimeTurnRequest struct {
	Utterance   string                 `json:"utterance"`
	Text        string                 `json:"text,omitempty"`
	Prompt      string                 `json:"prompt,omitempty"`
	Choice      string                 `json:"choice,omitempty"`
	Target      RuntimeTurnTarget      `json:"target,omitempty"`
	Surface     RuntimeTurnSurface     `json:"surface,omitempty"`
	Development RuntimeTurnDevelopment `json:"development,omitempty"`
	Mode        string                 `json:"mode,omitempty"`
	Run         bool                   `json:"run,omitempty"`
	Queue       bool                   `json:"queue,omitempty"`
}

type RuntimeTurnQueueItem struct {
	ItemID string `json:"itemId"`
	// OwnerUserID scopes the item to the user who spoke it. A shared/multi-user
	// agent must never list one tenant's utterances to another — see the
	// postmortem in runtime_queue_store.go.
	OwnerUserID string                 `json:"ownerUserId,omitempty"`
	State       string                 `json:"state"`
	Utterance   string                 `json:"utterance"`
	IntentClass string                 `json:"intentClass,omitempty"`
	Target      RuntimeTurnTarget      `json:"target,omitempty"`
	Surface     RuntimeTurnSurface     `json:"surface,omitempty"`
	Evidence    []RuntimeTurnEvidence  `json:"evidence,omitempty"`
	TaskID      string                 `json:"taskId,omitempty"`
	Session     string                 `json:"session,omitempty"`
	Runner      string                 `json:"runner,omitempty"`
	Reason      string                 `json:"reason,omitempty"`
	Spoken      string                 `json:"spoken,omitempty"`
	Error       string                 `json:"error,omitempty"`
	TestTarget  *RuntimeTurnTestTarget `json:"testTarget,omitempty"`
	CreatedAt   time.Time              `json:"createdAt"`
	UpdatedAt   time.Time              `json:"updatedAt"`
	Meta        map[string]interface{} `json:"meta,omitempty"`
}

// RuntimeTurnTestTarget answers "can the user actually test this yet?".
//
// State is deliberately NOT derived from task status. A finished task means the
// code changed; it does NOT mean anything reloaded on a device. The values are:
//
//	unverified — code work finished, no reload attempted yet (the honest default)
//	delivered  — a reload command reached N live listeners (Listeners > 0)
//	unreachable — reload attempted, ZERO live listeners; nothing is testable
//	failed     — reload attempted and the runtime reported failure
//
// `delivered` is still not proof the app re-rendered — it is proof the command
// was accepted by a live phone, which is the strongest claim the agent can make
// from its own side of the wire. Do not upgrade it to "verified" without
// evidence that came BACK from the device.
type RuntimeTurnTestTarget struct {
	Kind        string    `json:"kind,omitempty"`
	State       string    `json:"state,omitempty"`
	DeviceID    string    `json:"deviceId,omitempty"`
	Detail      string    `json:"detail,omitempty"`
	Listeners   int       `json:"listeners,omitempty"`
	AttemptedAt time.Time `json:"attemptedAt,omitempty"`
}

type RuntimeTurnResponse struct {
	OK             bool                   `json:"ok"`
	TurnID         string                 `json:"turnId,omitempty"`
	State          string                 `json:"state"`
	Spoken         string                 `json:"spoken,omitempty"`
	Haptic         string                 `json:"haptic,omitempty"`
	Glance         map[string]string      `json:"glance,omitempty"`
	Queue          *RuntimeTurnQueueItem  `json:"queue,omitempty"`
	Target         RuntimeTurnTarget      `json:"target,omitempty"`
	TestTarget     *RuntimeTurnTestTarget `json:"testTarget,omitempty"`
	AwaitingChoice bool                   `json:"awaitingChoice,omitempty"`
	Options        []string               `json:"options,omitempty"`
	Panel          map[string]string      `json:"panel,omitempty"`
	Handoff        map[string]string      `json:"handoff,omitempty"`
	Error          string                 `json:"error,omitempty"`
	Code           string                 `json:"code,omitempty"`
	Reason         string                 `json:"reason,omitempty"`
}

type RuntimeTurnListResponse struct {
	OK    bool                   `json:"ok"`
	Items []RuntimeTurnQueueItem `json:"items"`
	Count int                    `json:"count"`
}

// The queue store itself (durable, owner-scoped, evicting) lives in
// runtime_queue_store.go — read the postmortem block at the top of that file
// before changing persistence or list ordering.

func classifyRuntimeTurn(req RuntimeTurnRequest) string {
	if c := strings.ToLower(strings.TrimSpace(req.Development.IntentClass)); c != "" {
		return c
	}
	t := " " + strings.ToLower(strings.TrimSpace(req.Utterance)) + " "
	switch {
	case isRuntimeIdeaUtterance(t):
		return "idea-capture"
	case strings.TrimSpace(req.Choice) != "" || isRuntimeChoiceUtterance(t):
		return "session-turn"
	case strings.Contains(t, " autorun ") || strings.Contains(t, " async ") || strings.Contains(t, " overnight ") || strings.Contains(t, " keep working "):
		return "autorun"
	case strings.Contains(t, " goal ") || strings.Contains(t, " focus "):
		return "goal"
	case containsAnyWord(t, []string{"implement", "build", "make", "code", "edit", "change", "add", "fix", "wire", "create"}):
		return "start-coding"
	case containsAnyWord(t, []string{"why", "what", "status", "summarize", "summarise", "check"}):
		return "analysis"
	default:
		return "idea-capture"
	}
}

func isRuntimeIdeaUtterance(t string) bool {
	if containsAnyWord(t, []string{"idea", "remember", "note", "thought", "maybe"}) {
		return true
	}
	return strings.Contains(t, " idea:") || strings.Contains(t, " note:") || strings.Contains(t, " thought:")
}

func containsAnyWord(haystack string, words []string) bool {
	for _, w := range words {
		if strings.Contains(haystack, " "+w+" ") {
			return true
		}
	}
	return false
}

func isRuntimeChoiceUtterance(t string) bool {
	clean := strings.TrimSpace(strings.Trim(t, ".!"))
	if isTmuxChoiceAnswer(clean) {
		return true
	}
	switch clean {
	case "one", "two", "three", "four", "five", "yes", "no", "confirm", "cancel":
		return true
	default:
		return false
	}
}

func runtimeChoiceFromUtterance(text string) string {
	t := strings.ToLower(strings.TrimSpace(strings.Trim(text, ".!")))
	if isTmuxChoiceAnswer(t) {
		return t
	}
	switch t {
	case "one", "yes", "confirm":
		return "1"
	case "two", "no", "cancel":
		return "2"
	case "three":
		return "3"
	case "four":
		return "4"
	case "five":
		return "5"
	default:
		return ""
	}
}

func runtimeViewportFromSurface(surface RuntimeTurnSurface) *TaskViewport {
	vp := &TaskViewport{
		Surface:      strings.TrimSpace(surface.Class),
		Interaction:  strings.TrimSpace(surface.Interaction),
		VisualBudget: strings.TrimSpace(surface.VisualBudget),
		TTSBudget:    surface.TTSBudget,
		RiskPolicy:   strings.TrimSpace(surface.RiskPolicy),
	}
	if vp.Surface == "" {
		vp.Surface = strings.TrimSpace(surface.ID)
	}
	switch strings.ToLower(vp.Surface) {
	case "watch", "wearable-watch", "wearable-wear":
		vp.Surface = "wearable-watch"
		vp.Voice = true
		vp.STTEnabled = true
		vp.TTSEnabled = true
		if vp.TTSBudget == 0 {
			vp.TTSBudget = 160
		}
		if vp.VisualBudget == "" {
			vp.VisualBudget = "glance"
		}
		if vp.RiskPolicy == "" {
			vp.RiskPolicy = "watch"
		}
	case "car", "car-audio", "carplay", "androidauto":
		vp.Surface = "car-audio"
		vp.Voice = true
		vp.STTEnabled = true
		vp.TTSEnabled = true
		if vp.TTSBudget == 0 {
			vp.TTSBudget = 200
		}
		if vp.VisualBudget == "" {
			vp.VisualBudget = "none"
		}
		if vp.RiskPolicy == "" {
			vp.RiskPolicy = "driving"
		}
	}
	if vp.Interaction == "" && vp.Voice {
		vp.Interaction = "voice"
	}
	return vp
}
