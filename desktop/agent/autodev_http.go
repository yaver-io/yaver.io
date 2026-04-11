package main

// autodev_http.go — HTTP endpoints that back the mobile Auto Dev tab
// (mobile/app/(tabs)/autodev.tsx). Thin wrappers over the existing
// loop_cmd.go / loop_exec.go functions: the CLI has the ground truth,
// these handlers just reuse it in a non-interactive shape.
//
// Routes are registered in httpserver.go next to /schedules:
//
//   GET    /autodev/loops                        — list loops for the UI
//   POST   /autodev/loops/<name>/run             — kick one iteration
//   POST   /autodev/loops/<name>/stop            — drop STOP file + mark stopped
//   GET    /autodev/loops/<name>/ideas           — read ideas.json
//   POST   /autodev/loops/<name>/prompt          — set inline prompt
//   POST   /autodev/loops/<name>/prompt/pick     — pick an idea by ID
//
// All handlers return JSON. Errors are {"ok":false,"error":"..."};
// successes mirror the shape of the existing mobile types in
// mobile/src/lib/quic.ts so the UI can consume them without extra
// glue.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// autodevLoopRow is the wire shape consumed by the mobile Auto Dev
// tab. Kept deliberately flat so the TS side doesn't need to reach
// into LoopState internals.
type autodevLoopRow struct {
	ID               string               `json:"id"`
	Name             string               `json:"name"`
	Mode             string               `json:"mode"`
	Status           string               `json:"status"`
	IterationCount   int                  `json:"iterationCount"`
	LastSummary      string               `json:"lastSummary,omitempty"`
	Branch           string               `json:"branch"`
	Tone             string               `json:"tone,omitempty"`
	RadicalnessUI    int                  `json:"radicalnessUi,omitempty"`
	RadicalnessFeats int                  `json:"radicalnessFeatures,omitempty"`
	PromptInline     string               `json:"promptInline,omitempty"`
	CommitsToday     int                  `json:"commitsToday"`
	PatchesToday     int                  `json:"patchesToday"`
	TestflightToday  int                  `json:"testflightToday"`
	LastIterationAt  string               `json:"lastIterationAt,omitempty"`
	Runner           string               `json:"runner,omitempty"`
	// ReleaseTrain is the configured train + live counter so the
	// mobile Auto Dev tab can show "2/3 green, ready after next
	// kick" without computing anything locally.
	ReleaseTrain *autodevReleaseTrainRow `json:"releaseTrain,omitempty"`
	// SessionUsage lists per-runner wall-clock usage inside the
	// current provider window so the mobile tab can surface
	// "Claude: 2h 10m of 5h used today" next to each loop.
	SessionUsage []autodevProviderUsageRow `json:"sessionUsage,omitempty"`
	// TestRoot is populated for auto-test mode loops so the mobile
	// UI can show the spec directory the loop is watching.
	TestRoot string `json:"testRoot,omitempty"`
}

// autodevReleaseTrainRow is the subset of LoopShip.ReleaseTrain +
// counter state the mobile UI needs to render an "armed / waiting
// / paused" badge.
type autodevReleaseTrainRow struct {
	Enabled                 bool   `json:"enabled"`
	N                       int    `json:"n"`
	GreenRunSinceLastDeploy int    `json:"greenRunSinceLastDeploy"`
	Paused                  bool   `json:"paused"`
	Target                  string `json:"target,omitempty"`
	MaxTestFlightPerDay     int    `json:"maxTestFlightPerDay,omitempty"`
}

// autodevProviderUsageRow is a per-runner session-window usage
// summary. Seconds are absolute and unitless; the mobile UI format
// converts to h/m. Empty SessionWindow means "unlimited" (local
// ollama).
type autodevProviderUsageRow struct {
	Runner          string `json:"runner"`
	UsedSeconds     int    `json:"usedSeconds"`
	CapSeconds      int    `json:"capSeconds"`
	SessionWindow   string `json:"sessionWindow"`
	WindowStartedAt string `json:"windowStartedAt,omitempty"`
	OverCap         bool   `json:"overCap"`
}

func loopStateToRow(l *LoopState) autodevLoopRow {
	row := autodevLoopRow{
		ID:               l.ID,
		Name:             l.Spec.Name,
		Mode:             string(l.Spec.Mode),
		Status:           string(l.Status),
		IterationCount:   l.IterationCount,
		LastSummary:      l.LastSummary,
		Branch:           l.Spec.Ship.Branch,
		Tone:             l.Spec.Knobs.Tone,
		RadicalnessUI:    l.Spec.Knobs.RadicalnessUI,
		RadicalnessFeats: l.Spec.Knobs.RadicalnessFeatures,
		PromptInline:     l.PromptInline,
		CommitsToday:     l.CommitsToday,
		PatchesToday:     l.PatchesToday,
		TestflightToday:  l.TestflightToday,
		LastIterationAt:  l.LastIterationAt,
		Runner:           l.Spec.Think.Runner,
	}

	// Auto Test loops carry their spec root so the mobile tab can
	// show "watching yaver-tests/" without parsing the full spec.
	if l.Spec.Mode == LoopModeAutoTest {
		root := l.Spec.Test.Root
		if root == "" {
			root = "yaver-tests"
		}
		row.TestRoot = root
	}

	// Release-train state — only attached when the spec has a
	// real train configured, so the mobile UI can use a null
	// check to decide whether to render the badge.
	if l.Spec.Ship.ReleaseTrain.N > 0 {
		row.ReleaseTrain = &autodevReleaseTrainRow{
			Enabled:                 true,
			N:                       l.Spec.Ship.ReleaseTrain.N,
			GreenRunSinceLastDeploy: l.GreenRunSinceLastDeploy,
			Paused:                  l.Spec.Ship.ReleaseTrain.Paused,
			Target:                  l.Spec.Ship.ReleaseTrain.Target,
			MaxTestFlightPerDay:     l.Spec.Budget.MaxTestFlightPerDay,
		}
	}

	// Session-usage — load the on-disk counter and compute the
	// soft cap for each tracked runner. Read-only; never
	// mutates state.
	if f, err := loadSessionUsage(l.Spec.Name); err == nil && f != nil {
		rollExpiredWindows(f)
		for runner, usage := range f.Providers {
			if usage == nil {
				continue
			}
			used, cap, over := runnerBudgetState(f, runner)
			lim := defaultProviderLimits(runner)
			row.SessionUsage = append(row.SessionUsage, autodevProviderUsageRow{
				Runner:          runner,
				UsedSeconds:     used,
				CapSeconds:      cap,
				SessionWindow:   lim.SessionWindow,
				WindowStartedAt: usage.WindowStartedAt,
				OverCap:         over,
			})
		}
	}

	return row
}

// handleAutodevLoops serves `GET /autodev/loops`.
func (s *HTTPServer) handleAutodevLoops(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, http.StatusMethodNotAllowed, "use GET")
		return
	}
	loops, err := loadLoops()
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	rows := make([]autodevLoopRow, 0, len(loops))
	for _, l := range loops {
		rows = append(rows, loopStateToRow(l))
	}
	jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true, "loops": rows})
}

// handleAutodevLoopAction dispatches `/autodev/loops/<name>/<action>`.
// Any unknown name or action returns 404.
func (s *HTTPServer) handleAutodevLoopAction(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/autodev/loops/")
	if path == "" {
		jsonError(w, http.StatusBadRequest, "missing loop name")
		return
	}
	parts := strings.SplitN(path, "/", 3)
	name := parts[0]
	action := ""
	if len(parts) > 1 {
		action = parts[1]
	}
	subAction := ""
	if len(parts) > 2 {
		subAction = parts[2]
	}

	switch action {
	case "run":
		s.autodevRun(w, r, name)
	case "stop":
		s.autodevStop(w, r, name)
	case "ideas":
		s.autodevIdeas(w, r, name)
	case "prompt":
		if subAction == "pick" {
			s.autodevPromptPick(w, r, name)
			return
		}
		s.autodevPromptSet(w, r, name)
	default:
		jsonError(w, http.StatusNotFound, "unknown action "+action)
	}
}

func (s *HTTPServer) autodevRun(w http.ResponseWriter, r *http.Request, name string) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}
	// Kick in a background goroutine so the HTTP response doesn't
	// have to wait for a potentially minutes-long develop-mode
	// iteration. The mobile UI polls /autodev/loops for status.
	go func() {
		_, _, _ = kickLoopOnce(contextBackground(), name)
	}()
	jsonReply(w, http.StatusAccepted, map[string]interface{}{
		"ok":      true,
		"queued":  true,
		"message": "loop kick queued",
	})
}

func (s *HTTPServer) autodevStop(w http.ResponseWriter, r *http.Request, name string) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}
	_, err := withLoops(func(loops map[string]*LoopState) (bool, error) {
		l, ok := loops[name]
		if !ok {
			return false, fmt.Errorf("loop %q not found", name)
		}
		l.Status = LoopStatusStopped
		loops[name] = l
		return true, nil
	})
	if err != nil {
		jsonError(w, http.StatusNotFound, err.Error())
		return
	}
	// Drop the STOP file as a belt-and-braces signal so any wedged
	// in-flight iteration aborts on its next poll.
	if killPath, kerr := loopKillFilePath(name); kerr == nil {
		_ = os.WriteFile(killPath, []byte(time.Now().UTC().Format(time.RFC3339)+"\n"), 0600)
	}
	jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true})
}

func (s *HTTPServer) autodevIdeas(w http.ResponseWriter, r *http.Request, name string) {
	if r.Method != http.MethodGet {
		jsonError(w, http.StatusMethodNotAllowed, "use GET")
		return
	}
	loops, err := loadLoops()
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	l, ok := loops[name]
	if !ok {
		jsonError(w, http.StatusNotFound, "loop not found")
		return
	}
	ideasPath := l.LastIdeasPath
	if ideasPath == "" {
		base, derr := ConfigDir()
		if derr != nil {
			jsonError(w, http.StatusInternalServerError, derr.Error())
			return
		}
		ideasPath = filepath.Join(base, "loops", name, "ideas.json")
	}
	data, rerr := os.ReadFile(ideasPath)
	if rerr != nil {
		jsonError(w, http.StatusNotFound, "no ideas yet — run ideas mode first")
		return
	}
	// Passing through the raw JSON keeps the handler schema-agnostic;
	// the mobile side decodes to its own IdeaRow shape.
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func (s *HTTPServer) autodevPromptSet(w http.ResponseWriter, r *http.Request, name string) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}
	var body struct {
		Prompt string `json:"prompt"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	prompt := strings.TrimSpace(body.Prompt)

	_, err := withLoops(func(loops map[string]*LoopState) (bool, error) {
		l, ok := loops[name]
		if !ok {
			return false, fmt.Errorf("loop %q not found", name)
		}
		l.PromptInline = prompt
		l.ConsecutiveStuck = 0
		if prompt != "" && (l.Status == LoopStatusPaused || l.Status == LoopStatusStuck ||
			l.Status == LoopStatusNeedsHuman || l.Status == LoopStatusBudgetHit) {
			l.Status = LoopStatusIdle
		}
		loops[name] = l
		return true, nil
	})
	if err != nil {
		jsonError(w, http.StatusNotFound, err.Error())
		return
	}
	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":     true,
		"length": len(prompt),
	})
}

func (s *HTTPServer) autodevPromptPick(w http.ResponseWriter, r *http.Request, name string) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}
	var body struct {
		IdeaID string `json:"ideaId"`
		Source string `json:"source,omitempty"` // optional source loop name (default: name)
		Run    bool   `json:"run,omitempty"`    // kick immediately after pick
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if strings.TrimSpace(body.IdeaID) == "" {
		jsonError(w, http.StatusBadRequest, "ideaId is required")
		return
	}
	sourceName := body.Source
	if sourceName == "" {
		sourceName = name
	}

	prompt, title, perr := pickIdeaPrompt(sourceName, body.IdeaID)
	if perr != nil {
		jsonError(w, http.StatusNotFound, perr.Error())
		return
	}

	_, err := withLoops(func(loops map[string]*LoopState) (bool, error) {
		l, ok := loops[name]
		if !ok {
			return false, fmt.Errorf("loop %q not found", name)
		}
		l.PromptInline = prompt
		l.ConsecutiveStuck = 0
		if l.Status == LoopStatusPaused || l.Status == LoopStatusStuck ||
			l.Status == LoopStatusNeedsHuman || l.Status == LoopStatusBudgetHit {
			l.Status = LoopStatusIdle
		}
		loops[name] = l
		return true, nil
	})
	if err != nil {
		jsonError(w, http.StatusNotFound, err.Error())
		return
	}
	if body.Run {
		go func() {
			_, _, _ = kickLoopOnce(contextBackground(), name)
		}()
	}
	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":    true,
		"title": title,
		"queued": body.Run,
	})
}

// pickIdeaPrompt loads the source loop's ideas.json and returns the
// matching idea's prompt + title. Shared between the CLI
// (loopPromptPick) and the HTTP handler so both sides agree on
// resolution order and error messages.
func pickIdeaPrompt(sourceName, ideaID string) (prompt, title string, err error) {
	loops, lerr := loadLoops()
	if lerr != nil {
		return "", "", lerr
	}
	var ideasPath string
	if src, ok := loops[sourceName]; ok && src.LastIdeasPath != "" {
		ideasPath = src.LastIdeasPath
	} else {
		base, derr := ConfigDir()
		if derr != nil {
			return "", "", derr
		}
		ideasPath = filepath.Join(base, "loops", sourceName, "ideas.json")
	}
	data, rerr := os.ReadFile(ideasPath)
	if rerr != nil {
		return "", "", fmt.Errorf("no ideas.json for loop %q at %s", sourceName, ideasPath)
	}
	var payload struct {
		Ideas []struct {
			ID     string `json:"id"`
			Title  string `json:"title"`
			Prompt string `json:"prompt"`
		} `json:"ideas"`
	}
	if jerr := json.Unmarshal(data, &payload); jerr != nil {
		return "", "", fmt.Errorf("parse ideas.json: %w", jerr)
	}
	for _, it := range payload.Ideas {
		if it.ID == ideaID {
			if strings.TrimSpace(it.Prompt) == "" {
				return "", "", fmt.Errorf("idea %q has no .prompt field — regenerate ideas.json", ideaID)
			}
			return it.Prompt, it.Title, nil
		}
	}
	return "", "", fmt.Errorf("idea %q not found in %s", ideaID, ideasPath)
}
