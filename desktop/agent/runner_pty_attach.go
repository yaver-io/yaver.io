package main

// runner_pty_attach.go — attach a PTY to a tmux session THIS AGENT DID NOT
// START. The other half of runner_pty.go, which can only ever spawn.
//
// Why this file exists (2026-07-22 audit, docs/architecture/TMUX_VIBE_SESSIONS_AUDIT.md):
//
// The phone has shipped an "Open terminal" button on autorun cards since Epic 7
// (mobile/app/autoruns.tsx) and a /shell?session=<name> deep link behind it
// (mobile/app/shell.tsx). Both build `/ws/runner?name=<session>` with NO
// `runner` parameter — there is no runner to name, the session is the user's
// own. handleRunnerPTYWS opened with:
//
//	runnerID := normalizeRunnerID(q.Get("runner"))
//	if runnerID == "" || !IsSupportedRunner(runnerID) { fail }
//
// normalizeRunnerID("") returns "", so EVERY one of those taps died with
// `unsupported runner ""`. A button, a route and a handler all existed; the
// operation had never once worked. Textbook false green — which is why the
// doctor probe for this feature attempts the attach rather than checking that
// the route is registered.
//
// Attaching is not the same problem as spawning, and two tmux facts shape it:
//
//  1. A second client attached to the same session SHARES that session's
//     current window. Phone and desktop would fight over what is on screen.
//     `new-session -t <target>` puts us in the same session GROUP instead:
//     same windows, INDEPENDENT current window, and killing it leaves the
//     original untouched. That is why this is a grouped session and not a
//     plain `attach-session`.
//
//  2. tmux sizes a window to its clients. This box runs `window-size latest`
//     (tmux 3.5a, probed), so the most recently active client wins — a phone
//     attaching would resize the DESKTOP's window down to phone dimensions
//     mid-session. `-f ignore-size` tells tmux to leave this client out of
//     that negotiation entirely. Never drop that flag to "fix" a rendering
//     complaint: it trades the phone's layout for the user's real terminal.

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"strings"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

// attachTmuxSessionPTY serves WS /ws/runner?name=<session> when no runner is
// named: it attaches to a session that already exists rather than starting one.
//
// The caller has already upgraded the connection and must not write to it.
func (s *HTTPServer) attachTmuxSessionPTY(conn *websocket.Conn, r *http.Request) {
	q := r.URL.Query()
	raw := strings.TrimSpace(q.Get("name"))

	if !tmuxAvailable() {
		runnerPTYFail(conn, "tmux is not installed on this machine — attaching to a session needs it. "+TmuxInstallHint())
		return
	}

	target := sanitizeTmuxSessionName(raw)
	if target == "" {
		// Be specific about WHICH rule bit: a name that merely contains a dot
		// reads as a legal tmux name to the user, and "invalid" alone sends
		// them looking for a session that is sitting right there.
		runnerPTYFail(conn, fmt.Sprintf(
			"tmux session name %q cannot be targeted safely — only letters, digits, - and _ are accepted (tmux treats . and : as window/pane separators). Rename the session, or open it from the Tasks list, which targets by pane id.", raw))
		return
	}
	if !tmuxSessionExists(target) {
		runnerPTYFail(conn, fmt.Sprintf(
			"tmux session %q is not running on this machine — it may have exited since the task list was fetched. Pull the task list to refresh.", target))
		return
	}

	// A grouped session per attach, so two phones (or a phone and the glass
	// surface) each get their own view of the same windows.
	mirror := "yvatt-" + uuid.New().String()[:8]

	args := []string{"new-session", "-t", target, "-s", mirror, "-f", "ignore-size"}
	cmd := exec.Command(tmuxCmdName(), args...)
	cmd.Env = append(os.Environ(), "TERM="+safePTYTermName(q.Get("term")))

	ts, err := s.newTerminalSession(cmd, nil, "", "", "")
	if err != nil {
		runnerPTYFail(conn, "pty start failed: "+err.Error())
		return
	}

	// The grouped session outlives the PTY unless we say otherwise: tmux keeps
	// it as a detached session, and the user accumulates one yvatt-* per phone
	// tap until `tmux ls` is unreadable. Reap on close.
	//
	// Killing the MIRROR never touches the target. Grouped sessions share
	// windows by reference; kill-session on a group member destroys only that
	// member's session, and the windows survive as long as another session
	// holds them. Verified against tmux 3.5a.
	ts.onClose = func() {
		if out, kerr := exec.Command(tmuxCmdName(), "kill-session", "-t", mirror).CombinedOutput(); kerr != nil {
			// Not fatal — it is already gone in the common case.
			log.Printf("[tmux-attach] reap mirror %s: %v: %s", mirror, kerr, strings.TrimSpace(string(out)))
		}
	}

	log.Printf("[tmux-attach] attached to session %q via mirror %q", target, mirror)

	s.pumpRunnerPTY(conn, ts, false, map[string]any{
		"type":        "runner_pty",
		"mode":        "attach",
		"tmuxSession": target,
		"tmuxMirror":  mirror,
	})
}
