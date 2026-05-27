package main

// glass_hud.go — typed HUD view payloads + push helpers.
//
// HUD-class glasses (Even G1/G2, Vuzix Z100) can't render a browser
// quad — they render a few lines of monochrome text and read
// notifications out loud. The "PC UI in glasses" experience folds
// the heavy stuff (browser, terminal, email body) onto a spatial
// headset and keeps the HUD as a peripheral status surface fed by
// these typed views:
//
//   terminal_tail    — last N lines of an attached pty / task stream
//   email_subjects   — last N "from + subject" pairs
//   notification     — single push (replaces the wall)
//   voice_overlay    — live ASR partial / final overlay
//
// All four ride on the existing /blackbox/command-stream SSE
// channel that the MentraOS miniapp already subscribes to. We just
// add new `command` names — the miniapp side adds a few cases to
// its handleAgentCommand switch and the round-trip works.

import (
	"strings"
	"time"
)

// HUDTerminalTailView is a snapshot of the last few lines of a
// terminal / task stream, sized for a HUD wall (≤ 6 lines × 60
// chars).
type HUDTerminalTailView struct {
	SessionLabel string   `json:"sessionLabel"`
	Lines        []string `json:"lines"`
}

type HUDEmailSubject struct {
	From    string `json:"from"`
	Subject string `json:"subject"`
	TS      string `json:"ts,omitempty"`
}

type HUDEmailSubjectsView struct {
	Folder string            `json:"folder,omitempty"`
	Items  []HUDEmailSubject `json:"items"`
}

type HUDNotificationView struct {
	Title  string `json:"title"`
	Body   string `json:"body,omitempty"`
	Source string `json:"source,omitempty"`
}

type HUDVoiceOverlayView struct {
	Partial string `json:"partial,omitempty"`
	Final   string `json:"final,omitempty"`
	Latency int    `json:"latencyMs,omitempty"`
}

// hudClamp truncates strings to fit the HUD line budget without
// the caller having to know miniapp internals. 60 chars × 6 lines
// is the Even G1/G2 sweet spot; Z100 has more room but plays nicely
// with the same budget.
const (
	hudMaxLineLen  = 60
	hudMaxLines    = 6
	hudMaxSubjects = 4
)

func hudClampLine(s string) string {
	s = strings.TrimSpace(s)
	runes := []rune(s)
	if len(runes) <= hudMaxLineLen {
		return s
	}
	return string(runes[:hudMaxLineLen-1]) + "…"
}

func hudClampLines(lines []string) []string {
	if len(lines) > hudMaxLines {
		lines = lines[len(lines)-hudMaxLines:]
	}
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		out = append(out, hudClampLine(line))
	}
	return out
}

// BroadcastHUDTerminalTail pushes the last few lines of a terminal
// session to every HUD client currently subscribed. Idempotent: the
// miniapp re-renders the wall on every push, no diffing required.
func BroadcastHUDTerminalTail(blackbox *BlackBoxManager, label string, lines []string) {
	if blackbox == nil {
		return
	}
	blackbox.BroadcastCommand(BlackBoxCommand{
		Command: "glass_terminal_tail",
		Data: map[string]interface{}{
			"sessionLabel": hudClampLine(label),
			"lines":        hudClampLines(lines),
			"ts":           time.Now().UnixMilli(),
		},
	})
}

// BroadcastHUDEmailSubjects sends a list of "from + subject" pairs
// — designed to be glanceable on a low-res monochrome wall.
func BroadcastHUDEmailSubjects(blackbox *BlackBoxManager, folder string, subjects []HUDEmailSubject) {
	if blackbox == nil {
		return
	}
	if len(subjects) > hudMaxSubjects {
		subjects = subjects[:hudMaxSubjects]
	}
	items := make([]map[string]interface{}, 0, len(subjects))
	for _, s := range subjects {
		items = append(items, map[string]interface{}{
			"from":    hudClampLine(s.From),
			"subject": hudClampLine(s.Subject),
			"ts":      s.TS,
		})
	}
	blackbox.BroadcastCommand(BlackBoxCommand{
		Command: "glass_email_subjects",
		Data: map[string]interface{}{
			"folder": folder,
			"items":  items,
			"ts":     time.Now().UnixMilli(),
		},
	})
}

// BroadcastHUDNotification puts a single line on the HUD wall and
// (on speaker-bearing glasses) speaks it via the existing audio
// path that the miniapp's runner_auth_completed handler uses.
func BroadcastHUDNotification(blackbox *BlackBoxManager, title, body, source string) {
	if blackbox == nil {
		return
	}
	blackbox.BroadcastCommand(BlackBoxCommand{
		Command: "glass_notification",
		Data: map[string]interface{}{
			"title":  hudClampLine(title),
			"body":   hudClampLine(body),
			"source": source,
			"ts":     time.Now().UnixMilli(),
		},
	})
}

// BroadcastHUDVoiceOverlay shows the in-flight transcription on the
// HUD so the wearer can see what the agent heard. Partials repaint
// frequently; finals replace the wall for ~1.5s then clear via a
// follow-up empty-payload push.
func BroadcastHUDVoiceOverlay(blackbox *BlackBoxManager, partial, final string, latencyMs int) {
	if blackbox == nil {
		return
	}
	blackbox.BroadcastCommand(BlackBoxCommand{
		Command: "glass_voice_overlay",
		Data: map[string]interface{}{
			"partial":   hudClampLine(partial),
			"final":     hudClampLine(final),
			"latencyMs": latencyMs,
			"ts":        time.Now().UnixMilli(),
		},
	})
}
