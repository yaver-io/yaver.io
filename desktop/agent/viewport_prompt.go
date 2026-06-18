package main

// viewport_prompt.go — turns a TaskViewport into a one-line prompt
// hint that Claude can act on.
//
// Design principle: ONE line, MAX two sentences. Claude treats every
// extra token of system prompt as noise; we want to nudge response
// shape, not lecture. Bad: "You are talking to a user with a small
// screen who is also using voice. Please ensure your responses..." 50
// tokens wasted before the user's prompt even starts. Good: "[Display:
// glasses HUD, voice readback — ≤80 chars, no markdown.]" 18 tokens.
//
// Tested coverage: viewport_prompt_test.go.

import (
	"fmt"
	"net/http"
	"strings"
)

// mergeClientVoiceHints augments a viewport with the request's
// X-Yaver-Surface / X-Yaver-Voice headers and (as a last resort) the task
// source, without overriding fields already set from the body. It also accepts
// X-Yaver-Interaction / X-Yaver-Visual-Budget / X-Yaver-Risk-Policy for thin
// surfaces such as car/watch/TV/MCP. This is the
// header fallback for clients that don't send a speechContext body (CLI,
// web). X-Yaver-Voice is a CSV of "stt"/"tts" (or "none"). Returns vp
// (allocating one if a hint is present); nil only when there were no hints.
func mergeClientVoiceHints(r *http.Request, vp *TaskViewport, source string) *TaskViewport {
	if r == nil {
		return vp
	}
	surface := strings.TrimSpace(r.Header.Get("X-Yaver-Surface"))
	interaction := strings.TrimSpace(r.Header.Get("X-Yaver-Interaction"))
	visualBudget := strings.TrimSpace(r.Header.Get("X-Yaver-Visual-Budget"))
	riskPolicy := strings.TrimSpace(r.Header.Get("X-Yaver-Risk-Policy"))
	voice := strings.ToLower(strings.TrimSpace(r.Header.Get("X-Yaver-Voice")))
	ttsMode := strings.EqualFold(strings.TrimSpace(r.Header.Get("X-Yaver-TTS-Mode")), "1") ||
		strings.EqualFold(strings.TrimSpace(r.Header.Get("X-Yaver-TTS-Mode")), "true")
	if surface == "" && interaction == "" && visualBudget == "" && riskPolicy == "" && voice == "" && !ttsMode {
		return vp
	}
	if vp == nil {
		vp = &TaskViewport{}
	}
	if ttsMode {
		vp.TTSMode = true
	}
	if vp.Surface == "" {
		if surface != "" {
			vp.Surface = surface
		} else if source != "" {
			vp.Surface = source // best-effort: "cli" / "web" / "mobile"
		}
	}
	if vp.Interaction == "" {
		vp.Interaction = interaction
	}
	if vp.VisualBudget == "" {
		vp.VisualBudget = visualBudget
	}
	if vp.RiskPolicy == "" {
		vp.RiskPolicy = riskPolicy
	}
	if voice != "" && voice != "none" {
		for _, tok := range strings.Split(voice, ",") {
			switch strings.TrimSpace(tok) {
			case "stt":
				vp.STTEnabled = true
			case "tts":
				vp.TTSEnabled = true
			}
		}
	}
	return vp
}

// formatViewportHint returns a prompt-suffix string (leading \n) that
// nudges Claude toward the right response shape for this user's
// surface. Empty viewport → empty string.
func formatViewportHint(vp *TaskViewport) string {
	if vp == nil {
		return ""
	}
	parts := []string{}

	// Surface-driven shape.
	if shape := surfaceShape(vp.Surface, vp.PaneCols, vp.PaneRows); shape != "" {
		parts = append(parts, shape)
	}
	if shape := interactionShape(vp.Interaction); shape != "" {
		parts = append(parts, shape)
	}
	if shape := visualBudgetShape(vp.VisualBudget); shape != "" {
		parts = append(parts, shape)
	}
	if shape := riskPolicyShape(vp.RiskPolicy); shape != "" {
		parts = append(parts, shape)
	}
	// Multi-pane awareness — user has N parallel sessions visible.
	if vp.PaneCount >= 2 {
		parts = append(parts, fmt.Sprintf("user has %d parallel Claude sessions visible — be specific about file paths so they stay legible across panes", vp.PaneCount))
	}
	// TTS mode (user setting): lead with a spoken summary, normal body
	// after. Takes precedence over the readback budget below because it
	// shapes the whole reply, not just a headline. Text-only — no audio.
	if vp.TTSMode {
		parts = append(parts, "TTS mode is on — begin your reply with a single line that starts with `TTS: ` "+
			"holding a 1-2 sentence spoken-friendly summary of the outcome (plain text, no markdown, no code, "+
			"expand symbols and paths into words, short sentences). After that line, continue with your normal "+
			"formatted response for on-screen reading")
	} else if vp.TTSEnabled || vp.Voice {
		// Voice readback budgeting. TTSEnabled is the explicit signal from the
		// client's speechContext / X-Yaver-Voice header; Voice (origin-was-STT)
		// is kept as a back-compat trigger so older clients still get budgeted.
		budget := vp.TTSBudget
		if budget == 0 {
			budget = 280 // Cartesia clip default
		}
		parts = append(parts, fmt.Sprintf("voice readback enabled — keep the spoken headline under %d chars; details may follow on screen", budget))
	}
	// User can reply by voice — nudge a clean spoken-friendly closing
	// question instead of a wall of options when input is needed.
	if vp.STTEnabled {
		parts = append(parts, "user may reply by voice — if you need input, end with one short spoken-friendly question")
	}

	if len(parts) == 0 {
		return ""
	}
	return "\n[Display: " + strings.Join(parts, "; ") + ".]"
}

// surfaceShape converts the surface enum + optional pane geometry into
// a brief shape hint. Goal: small, scannable, no jargon Claude has to
// guess at.
func surfaceShape(surface string, cols, rows int) string {
	switch surface {
	case "":
		// No hint — fall back to pane geometry if present
		if cols > 0 && rows > 0 {
			return fmt.Sprintf("pane %dx%d (cols x rows) — fit output to this size", cols, rows)
		}
		return ""
	case "mobile", "mobile-phone":
		return "phone-screen single column ~50 chars wide — short lines, minimal headers"
	case "mobile-tablet":
		return "tablet ~80 chars wide — section headers OK, keep paragraphs short"
	case "web-desktop":
		return "desktop browser — full markdown OK"
	case "web-spatial-hud":
		return "spatial HUD ~600x600 — terse glanceable text, max 4 lines per block"
	case "web-spatial-vr":
		return "VR headset (Quest/Vision Pro) tmux-style floating panes — moderate detail, code blocks fine"
	case "glasses-mentra-live":
		return "audio-only smart glasses (no display) — speak the answer in one sentence, NO code blocks"
	case "glasses-mentra-display":
		return "monocular glasses HUD ~40 chars wide — one-line headline plus the file path that changed; nothing else"
	case "glasses-ray-ban":
		return "Meta Ray-Ban Display 600x600 monocular — terse glanceable text, max 3 lines"
	case "wearable-watch", "wearable-wear":
		// Smartwatch (Apple Watch / Wear OS) — the thinnest surface: one
		// spoken/glanced sentence, never code. See
		// docs/yaver-smartwatch-voice-terminal.md.
		return "smartwatch wrist screen — ONE short sentence answer only, no code, no diffs, no lists; if detail is needed say it's on the phone"
	case "car", "car-audio", "car-android-auto", "car-carplay":
		return "car surface — driving-safe one spoken status sentence, no code, no diffs, no logs; hand off details to phone"
	case "tv", "tv-living-room", "tv-android", "tv-apple":
		return "TV shared-room display — large glanceable status, no secrets by default, D-pad friendly labels"
	case "mcp":
		return "MCP agent caller — return a concise human summary plus structured details an agent can branch on"
	case "cli":
		return "terminal — feel free to use ANSI colors and full detail"
	default:
		// Unknown surface — pass through verbatim so user can debug
		return fmt.Sprintf("surface=%q (no specific shape hint yet)", surface)
	}
}

func interactionShape(interaction string) string {
	switch strings.ToLower(strings.TrimSpace(interaction)) {
	case "":
		return ""
	case "voice":
		return "voice interaction — prefer short spoken sentences and avoid dense option lists"
	case "dpad":
		return "D-pad interaction — expose a small number of clearly named choices"
	case "approval":
		return "approval interaction — state the action, risk, and approve/deny choice plainly"
	case "stream":
		return "stream viewer — summarize current state before detailed observations"
	case "touch":
		return "touch interaction — compact sections and obvious next action"
	case "keyboard":
		return "keyboard interaction — full text input available"
	default:
		return fmt.Sprintf("interaction=%q", interaction)
	}
}

func visualBudgetShape(budget string) string {
	switch strings.ToLower(strings.TrimSpace(budget)) {
	case "":
		return ""
	case "none":
		return "no visual budget — audio/status only"
	case "glance":
		return "glance visual budget — one headline and one next action"
	case "panel":
		return "panel visual budget — short sections, no walls of text"
	case "full":
		return "full visual budget — detailed markdown is acceptable"
	default:
		return fmt.Sprintf("visualBudget=%q", budget)
	}
}

func riskPolicyShape(policy string) string {
	switch strings.ToLower(strings.TrimSpace(policy)) {
	case "":
		return ""
	case "driving":
		return "driving policy — never ask the user to read while driving; risky actions need explicit confirmation"
	case "watch":
		return "watch policy — approve/deny only for low or medium risk; high risk should hand off to phone"
	case "shared-tv":
		return "shared TV policy — do not reveal secrets, tokens, private paths, or sensitive account data"
	case "mcp":
		return "MCP policy — do not bypass human approval gates; surface pending approvals as structured state"
	case "normal":
		return ""
	default:
		return fmt.Sprintf("riskPolicy=%q", policy)
	}
}
