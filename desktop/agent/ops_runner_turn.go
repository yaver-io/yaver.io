package main

// ops_runner_turn.go — verbs "runner_turn" and "runner_sessions": drive and list
// the live tmux-persisted coding-runner PTY sessions (claude / codex / opencode /
// glm) on a box, from the `ops` grand-tool.
//
// NOTE: distinct from the `runner` verb in ops_runner.go, which manages RunnerJob
// / sandbox / agent-session objects. These two verbs are about the interactive
// TUI sessions started by `yaver <runner> --machine=<box>` (runner_pty.go) — the
// ones a human talks to.
//
// Why as ops verbs and not just HTTP routes: the low-friction front door is Yaver
// added as an MCP connector in the phone's Claude app (docs/yaver-phone-mcp-
// connector.md). A connector reaches a box through the `ops` tool, never a raw
// HTTP path. So "tell codex on my box to fix sfmg auth, read me one sentence
// back" has to be one ops call:
//
//	ops(machine="primary", verb="runner_turn",
//	    payload={runner:"codex", text:"...", surface:"voice"})
//
// The turn reuses executeRunnerSessionTurn (runner_session_turn.go) — the same
// tmux hazard handling the watch/car/TV surfaces already depend on. The only
// thing added here is a spoken, code-free one-sentence summary for eyes-free
// surfaces, built from the existing watch summarizer (watch_risk.go) so the
// no-code / clamp guarantees are shared, not re-derived.

import (
	"encoding/json"
	"net/http"
	"strings"
)

func init() {
	registerOpsVerb(opsVerbSpec{
		Name:        "runner_turn",
		Description: "Drive a live coding-runner session (claude/codex/opencode/glm) by ONE turn: type a prompt (`text`) or answer a menu (`choice`), wait, then return a short spoken summary + the pane tail. Target a remote box with machine=<deviceId|primary>; call runner_sessions first to see what's live (start one with `yaver <runner> --machine=<box>`). Set surface=voice|car|watch for a terse eyes-free summary with no raw pane. This is the verb a phone/car voice surface calls over MCP.",
		Schema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"session": map[string]interface{}{
					"type":        "string",
					"description": "tmux session name (e.g. \"yaver-codex\"). Optional when `runner` is given or exactly one session is live.",
				},
				"runner": map[string]interface{}{
					"type":        "string",
					"description": "runner id: claude, codex, opencode, or glm. Resolves to that runner's canonical session on the box.",
				},
				"text": map[string]interface{}{
					"type":        "string",
					"description": "A prompt to type and submit. Mutually exclusive with `choice`.",
				},
				"choice": map[string]interface{}{
					"type":        "string",
					"description": "A bare menu option number to answer a menu the session is showing. Mutually exclusive with `text`. Never appends Enter.",
				},
				"waitMs": map[string]interface{}{
					"type":        "integer",
					"description": "How long (ms) to let the runner react before reading the pane back. A watch wants this short; a car can wait longer. Default 6000, max 120000.",
				},
				"surface": map[string]interface{}{
					"type":        "string",
					"enum":        []string{"voice", "car", "watch", "tv", "screen"},
					"description": "Rendering hint. voice/car/watch → return only the spoken one-sentence summary (no raw pane, no code). screen/tv (or unset) → also include the pane tail.",
				},
			},
			"additionalProperties": false,
		},
		Handler:    opsRunnerTurnHandler,
		Streaming:  false,
		AllowGuest: false, // owner-only: this types into an authenticated coding session
	})

	registerOpsVerb(opsVerbSpec{
		Name:        "runner_sessions",
		Description: "List the live coding-runner sessions (tmux-persisted) on this machine: name, runner, whether attached. Read-only. Use before runner_turn to see what's running, or to answer \"what's going on on my box\". Target a remote box with machine=<deviceId|primary>.",
		Schema: map[string]interface{}{
			"type":                 "object",
			"properties":           map[string]interface{}{},
			"additionalProperties": false,
		},
		Handler:    opsRunnerSessionsHandler,
		Streaming:  false,
		AllowGuest: false,
	})
}

type opsRunnerTurnPayload struct {
	Session string `json:"session"`
	Runner  string `json:"runner"`
	Text    string `json:"text"`
	Choice  string `json:"choice"`
	WaitMs  int    `json:"waitMs"`
	Surface string `json:"surface"`
}

func opsRunnerTurnHandler(_ OpsContext, payload json.RawMessage) OpsResult {
	// Driving a runner is real work — pin the idle clock so the auto-park
	// (park_check.go) never scales this box to zero mid-session.
	touchParkActivity()

	var p opsRunnerTurnPayload
	if len(payload) > 0 {
		if err := json.Unmarshal(payload, &p); err != nil {
			return OpsResult{OK: false, Code: "bad_payload", Error: "invalid payload: " + err.Error()}
		}
	}

	reply, status := executeRunnerSessionTurn(runnerSessionTurnRequest{
		Session: p.Session,
		Runner:  p.Runner,
		Text:    p.Text,
		Choice:  p.Choice,
		WaitMs:  p.WaitMs,
	})

	spoken := summarizeRunnerTurnForSpeech(reply)
	out := map[string]interface{}{
		"session":        reply.Session,
		"runner":         reply.Runner,
		"awaitingChoice": reply.AwaitingChoice,
		"spoken":         spoken,
	}
	if reply.Sent != "" {
		out["sent"] = reply.Sent
	}
	if len(reply.Options) > 0 {
		out["options"] = reply.Options
	}
	// Keep the raw pane off eyes-free surfaces — the spoken line is the whole
	// point there, and a pane can carry code/paths the summarizer deliberately
	// refuses. Screen/TV callers (or unset) still get it for a full render.
	if !isVoiceSurface(p.Surface) && reply.Pane != "" {
		out["pane"] = reply.Pane
	}

	// A 200 with the pane landed (even on a chained menu) is a successful turn:
	// the caller loops with `choice`. Only the validation / not-found / conflict
	// / tmux-error statuses are failures the caller must fix.
	if status == http.StatusOK && reply.OK {
		return OpsResult{OK: true, Initial: out}
	}
	if reply.Error != "" {
		out["detail"] = reply.Error
	}
	return OpsResult{OK: false, Code: runnerTurnErrorCode(status), Error: firstNonEmptyStr(reply.Error, "runner turn failed"), Initial: out}
}

func opsRunnerSessionsHandler(_ OpsContext, _ json.RawMessage) OpsResult {
	sessions := listRunnerPTYSessions()
	list := make([]map[string]interface{}, 0, len(sessions))
	for _, s := range sessions {
		list = append(list, map[string]interface{}{
			"name":     s.Name,
			"runner":   s.Runner,
			"attached": s.Attached,
		})
	}
	return OpsResult{OK: true, Initial: map[string]interface{}{
		"sessions": list,
		"count":    len(list),
	}}
}

// runnerTurnErrorCode maps the HTTP status executeRunnerSessionTurn returns onto
// a stable ops error code, so an agent can branch: "awaiting" tells it to answer
// a menu, "bad_payload" tells it to fix the call.
func runnerTurnErrorCode(status int) string {
	switch status {
	case http.StatusBadRequest:
		return "bad_payload"
	case http.StatusNotFound:
		return "not_found"
	case http.StatusConflict:
		return "awaiting" // pane is on a menu, or a choice/prompt was sent at the wrong moment
	default:
		return "internal"
	}
}

// summarizeRunnerTurnForSpeech renders a runner turn as one short, code-free
// sentence for an eyes-free surface. It reuses watchFirstStatusClause /
// watchClampSentence (watch_risk.go) so the "never speak code, always clamp"
// guarantees are the same ones the watch and car already ship — this only
// supplies a runner-appropriate lead.
func summarizeRunnerTurnForSpeech(reply runnerSessionTurnResponse) string {
	if reply.AwaitingChoice {
		lead := "It's waiting for a choice."
		if len(reply.Options) > 0 {
			opts := reply.Options
			if len(opts) > 4 {
				opts = opts[:4]
			}
			return watchClampSentence(lead + " Options: " + strings.Join(opts, ", ") + ".")
		}
		return watchClampSentence(lead)
	}
	if !reply.OK && reply.Error != "" {
		if clause := watchFirstStatusClause(reply.Error); clause != "" {
			return watchClampSentence("That didn't go through. " + clause)
		}
		return watchClampSentence("That didn't go through.")
	}
	lead := "Working."
	if reply.Sent == "choice" {
		lead = "Answered."
	}
	if clause := watchFirstStatusClause(reply.Pane); clause != "" {
		return watchClampSentence(lead + " " + clause)
	}
	return watchClampSentence(lead + " Nothing readable back yet — check the session.")
}

// isVoiceSurface reports whether a surface hint means "eyes-free": speak a
// sentence, don't ship a pane. Unset / screen / tv → false.
func isVoiceSurface(surface string) bool {
	switch strings.ToLower(strings.TrimSpace(surface)) {
	case "voice", "car", "watch", "glass", "wear", "wearable":
		return true
	default:
		return false
	}
}
