package main

// runner_session_turn.go — one call that drives a live coding session.
//
//	POST /runner/session/turn  {session?, runner?, text?, choice?, waitMs?}
//
// This is the endpoint a WATCH, CAR, or TV speaks. Those surfaces have no
// terminal, no PTY, and often no screen worth reading a TUI on. What they have
// is a sentence ("keep developing this in the ubuntu session") and a need to
// hear back one sentence. Everything below exists to make that safe.
//
// It is deliberately NOT /ws/runner. That endpoint hands you a raw pane and
// assumes a terminal emulator on the other end — right for the phone's xterm,
// useless on a wrist. Here the agent owns the terminal semantics: which session
// you meant, whether the pane can accept a prompt at all, how a given runner
// submits, and what came back.
//
// The three hazards this endpoint exists to absorb, each learned the hard way
// against a real box (see tmux.go):
//
//  1. A pane showing a menu will treat your prompt's Enter as a selection.
//     A prompt sent while codex offered "1. Update now" installed an update and
//     killed the session. So: never type a prompt into a menu — report the
//     options and let the caller answer.
//  2. A menu digit confirms by itself. Appending Enter to it answers the NEXT
//     modal, sight unseen, and claude's next modal has "No, exit" as option 1.
//     So: choices go through `choice`, which sends the key and nothing else.
//  3. Menus chain. Answering one reveals another, with different numbering.
//     So: every reply carries the pane's current state, and a caller loops.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"strings"
	"time"
)

type runnerSessionTurnRequest struct {
	// Session names the tmux session directly ("yaver-codex"). Optional when
	// Runner is given, or when exactly one runner session is live.
	Session string `json:"session"`
	// Runner resolves to this box's canonical session for that runner.
	Runner string `json:"runner"`
	// Text is a prompt to type and submit. Mutually exclusive with Choice.
	Text string `json:"text"`
	// Choice answers a menu the pane is showing (a bare option number).
	Choice string `json:"choice"`
	// WaitMs is how long to let the runner react before reading the pane back.
	// Zero picks a default; a watch wants this short, a car can wait longer.
	WaitMs int `json:"waitMs"`
}

type runnerSessionTurnResponse struct {
	OK      bool   `json:"ok"`
	Session string `json:"session"`
	Runner  string `json:"runner,omitempty"`
	// Sent reports what we actually delivered: "prompt" or "choice".
	Sent string `json:"sent,omitempty"`
	// AwaitingChoice is true when the pane is (still) on a menu. A caller must
	// answer with `choice` before any prompt will be accepted.
	AwaitingChoice bool     `json:"awaitingChoice"`
	Options        []string `json:"options,omitempty"`
	// Pane is the visible tail — enough for a TV to render and for a watch to
	// summarize, without shipping a whole scrollback over a cellular link.
	Pane  string `json:"pane,omitempty"`
	Error string `json:"error,omitempty"`
}

const (
	runnerTurnDefaultWaitMs = 6000
	runnerTurnMaxWaitMs     = 120000
	runnerTurnPaneLines     = 24
)

// resolveRunnerSession picks the tmux session a turn is aimed at. Explicit name
// wins; then the runner's canonical session; then, if the box has exactly one
// runner session live, that one — the case a voice surface actually hits, where
// the user said "the ubuntu session" and meant "the only one".
func resolveRunnerSession(name, runner string) (string, string, error) {
	sessions := listRunnerPTYSessions()

	if n := sanitizeTmuxSessionName(strings.TrimSpace(name)); n != "" {
		for _, s := range sessions {
			if s.Name == n {
				return s.Name, s.Runner, nil
			}
		}
		return "", "", fmt.Errorf("no live session named %q on this machine", n)
	}

	if r := normalizeRunnerID(runner); r != "" {
		for _, s := range sessions {
			if s.Runner == r {
				return s.Name, s.Runner, nil
			}
		}
		return "", "", fmt.Errorf("no live %s session on this machine — start one with `yaver %s --machine=<this box>`", r, r)
	}

	switch len(sessions) {
	case 0:
		return "", "", fmt.Errorf("no live runner sessions on this machine")
	case 1:
		return sessions[0].Name, sessions[0].Runner, nil
	default:
		names := make([]string, 0, len(sessions))
		for _, s := range sessions {
			names = append(names, s.Name)
		}
		return "", "", fmt.Errorf("several runner sessions are live (%s) — name the one you mean", strings.Join(names, ", "))
	}
}

// capturePaneTail returns the last n non-empty lines the pane is showing.
func capturePaneTail(sessionName string, n int) string {
	out, err := exec.Command("tmux", "capture-pane", "-p", "-t", sessionName).Output()
	if err != nil {
		return ""
	}
	all := strings.Split(string(out), "\n")
	kept := make([]string, 0, n)
	for i := len(all) - 1; i >= 0 && len(kept) < n; i-- {
		if line := strings.TrimRight(all[i], " \t"); strings.TrimSpace(line) != "" {
			kept = append(kept, line)
		}
	}
	for i, j := 0, len(kept)-1; i < j; i, j = i+1, j-1 {
		kept[i], kept[j] = kept[j], kept[i]
	}
	return strings.Join(kept, "\n")
}

func (s *HTTPServer) handleRunnerSessionTurn(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}
	var req runnerSessionTurnRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid json body")
		return
	}
	text := strings.TrimSpace(req.Text)
	choice := strings.TrimSpace(req.Choice)
	if (text == "") == (choice == "") {
		jsonError(w, http.StatusBadRequest, "send exactly one of `text` (a prompt) or `choice` (a menu option number)")
		return
	}
	if choice != "" && !isTmuxChoiceAnswer(choice) {
		jsonError(w, http.StatusBadRequest, "`choice` must be a bare option number")
		return
	}

	sessionName, runnerID, err := resolveRunnerSession(req.Session, req.Runner)
	if err != nil {
		jsonError(w, http.StatusNotFound, err.Error())
		return
	}

	reply := runnerSessionTurnResponse{Session: sessionName, Runner: runnerID}

	// Read the pane only once it has stopped redrawing. A TUI that is mid-paint
	// can show neither the menu it is about to render nor the prompt it just
	// cleared, and either misreading sends the wrong keystroke.
	awaiting, options := settleAndInspectPane(sessionName)

	if choice != "" {
		// Symmetry matters as much as the prompt guard. A digit sent at a
		// prompt is not a no-op: it is typed into the composer as literal text
		// and silently prefixes whatever the user says next
		// ("2reply with exactly ..."). Refuse it.
		if !awaiting {
			reply.Pane = capturePaneTail(sessionName, runnerTurnPaneLines)
			reply.Error = "session is not showing a menu — send `text`, not `choice`"
			jsonReply(w, http.StatusConflict, reply)
			return
		}
		// The digit confirms on its own; no Enter, ever. See tmux.go.
		if err := sendTmuxKey(sessionName, choice); err != nil {
			jsonError(w, http.StatusInternalServerError, err.Error())
			return
		}
		reply.Sent = "choice"
	} else {
		// A prompt must never be typed into a menu: the Enter that submits it
		// would pick whichever option is highlighted.
		if awaiting {
			reply.AwaitingChoice = true
			reply.Options = options
			reply.Pane = capturePaneTail(sessionName, runnerTurnPaneLines)
			reply.Error = "session is waiting on a choice — answer it with `choice` before sending a prompt"
			jsonReply(w, http.StatusConflict, reply)
			return
		}
		if err := sendTmuxLine(sessionName, text); err != nil {
			jsonError(w, http.StatusInternalServerError, err.Error())
			return
		}
		reply.Sent = "prompt"
	}

	wait := req.WaitMs
	if wait <= 0 {
		wait = runnerTurnDefaultWaitMs
	}
	if wait > runnerTurnMaxWaitMs {
		wait = runnerTurnMaxWaitMs
	}
	time.Sleep(time.Duration(wait) * time.Millisecond)

	// Menus chain: answering one can reveal another with different numbering.
	// Always report where the pane landed so the caller can loop rather than
	// guess — and settle again first, because the next modal may still be
	// painting when the wait elapses.
	if nowAwaiting, nowOptions := settleAndInspectPane(sessionName); nowAwaiting {
		reply.AwaitingChoice = true
		reply.Options = nowOptions
	}
	reply.Pane = capturePaneTail(sessionName, runnerTurnPaneLines)
	reply.OK = true
	jsonReply(w, http.StatusOK, reply)
}

// settleAndInspectPane waits for the pane to stop changing, then reports
// whether it is showing a menu.
//
// Without this the menu check races the runner's redraw: a modal that appears
// 200 ms after a keypress reads as "no menu", and the next prompt gets typed
// into it. Two identical consecutive captures is a good-enough definition of
// settled for a TUI; the ceiling keeps a chatty spinner from stalling the turn.
func settleAndInspectPane(sessionName string) (bool, []string) {
	var last string
	deadline := time.Now().Add(paneSettleCeiling)
	for time.Now().Before(deadline) {
		cur := capturePaneTail(sessionName, runnerTurnPaneLines)
		if cur == last {
			break
		}
		last = cur
		time.Sleep(paneSettleInterval)
	}
	return tmuxPaneAwaitingChoice(sessionName)
}

const (
	paneSettleInterval = 120 * time.Millisecond
	paneSettleCeiling  = 1500 * time.Millisecond
)
