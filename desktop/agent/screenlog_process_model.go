package main

// screenlog_process_model.go — the "ProcessModel" pipeline: turn a recorded
// screenlog session into a structured model of WHAT the operator did and HOW
// the machinery/tools were used. This is the analysis half of the "ghost"
// (docs/yaver-talos-ghost-erp-migration.md): observe → UNDERSTAND → replicate.
//
// Division of labour (deliberate):
//   - Go (here) does the DETERMINISTIC heavy lifting: segment the session into
//     task EPISODES, attribute frames + input events to each, sample keyframes.
//     Free, local, exact — no LLM.
//   - The RUNNER (the MCP client's claude-code/codex, never a headless P-mode
//     spawn) does the SEMANTIC lift: read the keyframes + the literal keystroke
//     trace per episode and fill in intent / fields / decision-rules /
//     exceptions / machinery — then call screenlog_process_model_save.
//
// The resulting ProcessModel is one artifact with three uses: an ERP-migration
// spec, a computer-use replay script, and Talos knowledge (SOPs / machine use).

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// ProcessEpisode is one contiguous task episode — a run of activity in one
// system/screen, bounded by app-changes and idle gaps. The deterministic
// fields are filled by Go; the semantic fields are filled by the runner.
type ProcessEpisode struct {
	Index       int      `json:"index"`
	StartMs     int64    `json:"startMs"`
	EndMs       int64    `json:"endMs"`
	DurationSec int      `json:"durationSec"`
	System      string   `json:"system"`           // deterministic: active app/process
	Screen      string   `json:"screen,omitempty"` // deterministic: window title
	Frames      int      `json:"frames"`
	Keyframes   []string `json:"keyframes,omitempty"` // sampled frame filenames (evidence)
	Events      int      `json:"events"`
	Keystrokes  int      `json:"keystrokes,omitempty"`
	Clicks      int      `json:"clicks,omitempty"`

	// --- semantic (runner-filled; omitempty so the skeleton is clean) ---
	Intent        string   `json:"intent,omitempty"`
	FieldsTouched []string `json:"fieldsTouched,omitempty"`
	Sequence      []string `json:"sequence,omitempty"`
	DecisionRules []string `json:"decisionRules,omitempty"`
	Exceptions    []string `json:"exceptions,omitempty"`
	Confidence    float64  `json:"confidence,omitempty"`
}

// MachineryUse captures observed operation of a machine/tool ("learn how
// machinery is used"). Runner-filled from the visual + activity evidence.
type MachineryUse struct {
	Machine     string            `json:"machine"`
	ObservedUse string            `json:"observedUse,omitempty"`
	Params      map[string]string `json:"params,omitempty"`
	Confidence  float64           `json:"confidence,omitempty"`
}

// ProcessModel is the saved artifact for a session.
type ProcessModel struct {
	SessionID string           `json:"sessionId"`
	Host      string           `json:"host,omitempty"`
	Role      string           `json:"role,omitempty"`   // runner-inferred operator role
	System    string           `json:"system,omitempty"` // dominant system (e.g. "Logo Tiger ERP")
	FromMs    int64            `json:"fromMs"`
	ToMs      int64            `json:"toMs"`
	Episodes  []ProcessEpisode `json:"episodes"`
	Machinery []MachineryUse   `json:"machinery,omitempty"`
	Summary   string           `json:"summary,omitempty"`
	CreatedAt int64            `json:"createdAt"`
}

// buildProcessEpisodes segments a session into task episodes (deterministic).
// Consecutive active samples on the same system merge; an idle sample (or a
// system change) closes the current episode.
func buildProcessEpisodes(sess *ScreenlogSession, idleGapMs, maxAttrMs int64) []ProcessEpisode {
	samples := screenlogToSamples(sess, idleGapMs, maxAttrMs)

	// 1. Merge into raw episode windows (no enrichment yet).
	type win struct {
		start, end int64
		system     string
		screen     string
	}
	var wins []win
	for _, s := range samples {
		if s.Idle {
			continue // idle closes the run
		}
		if n := len(wins); n > 0 && wins[n-1].system == s.Category &&
			s.Start-wins[n-1].end <= idleGapMs {
			wins[n-1].end = s.End
			if s.Label != "" {
				wins[n-1].screen = s.Label // keep the latest non-empty title
			}
			continue
		}
		wins = append(wins, win{start: s.Start, end: s.End, system: s.Category, screen: s.Label})
	}

	// 2. Pre-read input events once; bucket by episode window below.
	events, _ := readInputEvents(sess.ID, 0)

	// 3. Enrich each window into an episode.
	eps := make([]ProcessEpisode, 0, len(wins))
	for i, w := range wins {
		ep := ProcessEpisode{
			Index:       i,
			StartMs:     w.start,
			EndMs:       w.end,
			DurationSec: int((w.end - w.start) / 1000),
			System:      w.system,
			Screen:      w.screen,
		}
		// frames in window + keyframe sampling (evidence)
		var inWindow []ScreenlogFrame
		for _, f := range sess.Frames {
			if f.CapturedAt >= w.start && f.CapturedAt <= w.end {
				inWindow = append(inWindow, f)
			}
		}
		ep.Frames = len(inWindow)
		for _, kf := range sampleFramesEvenly(inWindow, 3) {
			if kf.File != "" {
				ep.Keyframes = append(ep.Keyframes, kf.File)
			}
		}
		// input events in window
		for _, e := range events {
			if e.T < w.start || e.T > w.end {
				continue
			}
			ep.Events++
			switch e.Type {
			case "keydown", "key", "text":
				ep.Keystrokes++
			case "click", "mousedown":
				ep.Clicks++
			}
		}
		eps = append(eps, ep)
	}
	return eps
}

// sampleFramesEvenly picks up to n evenly-spaced frames from a slice (a
// window-local variant of sampleKeyframes which operates on a whole session).
func sampleFramesEvenly(frames []ScreenlogFrame, n int) []ScreenlogFrame {
	if n <= 0 || len(frames) == 0 {
		return nil
	}
	if n >= len(frames) {
		return frames
	}
	out := make([]ScreenlogFrame, 0, n)
	step := float64(len(frames)-1) / float64(n-1)
	for i := 0; i < n; i++ {
		idx := int(float64(i)*step + 0.5)
		if idx >= len(frames) {
			idx = len(frames) - 1
		}
		out = append(out, frames[idx])
	}
	return out
}

// buildProcessSkeleton produces the deterministic ProcessModel skeleton for a
// session (episodes only; semantic fields empty, to be filled by the runner).
func buildProcessSkeleton(id string) (*ProcessModel, *ScreenlogSession, error) {
	sess, err := loadScreenlogSession(id)
	if err != nil {
		return nil, nil, fmt.Errorf("session not found: %s", id)
	}
	idleGapMs := int64(600) * 1000
	maxAttrMs := int64(sess.Config.IntervalSec*4) * 1000
	if maxAttrMs < 10000 {
		maxAttrMs = 10000
	}
	eps := buildProcessEpisodes(sess, idleGapMs, maxAttrMs)
	from, to := int64(0), int64(0)
	if len(sess.Frames) > 0 {
		from = sess.Frames[0].CapturedAt
		to = sess.Frames[len(sess.Frames)-1].CapturedAt
	}
	pm := &ProcessModel{
		SessionID: sess.ID,
		Host:      sess.Host,
		FromMs:    from,
		ToMs:      to,
		Episodes:  eps,
	}
	return pm, sess, nil
}

// processModelPrompt is the instruction handed to the runner. It analyses the
// attached keyframes + the per-episode skeleton and emits a completed
// ProcessModel, then saves it via screenlog_process_model_save.
func processModelPrompt(id string) string {
	return "You are the Yaver ghost analysing a screen recording to learn how an " +
		"operator uses their system and machinery (for risk-free ERP migration + SOP capture).\n" +
		"Below is a deterministic skeleton of task EPISODES (already segmented by Go) plus " +
		"sampled keyframe images and per-episode keystroke/click counts.\n\n" +
		"For EACH episode infer, from the images + counts:\n" +
		"  • system  — the application/ERP in use (e.g. \"Logo Tiger ERP\", \"Excel\")\n" +
		"  • intent  — the task being performed (e.g. \"enter sales order\")\n" +
		"  • fieldsTouched, sequence — the concrete UI steps/fields, in order\n" +
		"  • decisionRules — any conditional/tribal rules you can see (\"if customer ACME → 5% discount\")\n" +
		"  • exceptions — deviations/error handling\n" +
		"  • confidence — 0..1\n" +
		"Also fill model-level role, system, summary, and machinery[] (any machine/tool use observed: " +
		"machine, observedUse, params).\n\n" +
		"Then call screenlog_process_model_save with id=\"" + id + "\" and the completed model. " +
		"Be precise; prefer text/labels over guesses; leave fields empty when unsure rather than inventing."
}

// --- persistence -----------------------------------------------------------

func processModelPath(id string) (string, error) {
	dir, err := screenlogSessionDir(id)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "process_model.json"), nil
}

func saveProcessModel(pm *ProcessModel) error {
	if pm.CreatedAt == 0 {
		pm.CreatedAt = time.Now().UnixMilli()
	}
	p, err := processModelPath(pm.SessionID)
	if err != nil {
		return err
	}
	data, _ := json.MarshalIndent(pm, "", "  ")
	return os.WriteFile(p, data, 0o600)
}

func loadProcessModel(id string) (*ProcessModel, error) {
	p, err := processModelPath(id)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	var pm ProcessModel
	if err := json.Unmarshal(data, &pm); err != nil {
		return nil, err
	}
	return &pm, nil
}

// --- live view -------------------------------------------------------------

// screenlogLatestFrame returns the newest frame of the most-recent session for
// a near-real-time "live" view (security-cam style). It does not capture on
// demand — it reflects the running recorder's latest kept frame, so the live
// view advances at the capture cadence. Returns (sessionID, frame, ok).
func screenlogLatestFrame() (string, ScreenlogFrame, bool) {
	// Prefer the active session if one is recording.
	screenlogMu.Lock()
	active := screenlogActive
	screenlogMu.Unlock()
	if active != nil {
		active.mu.Lock()
		n := len(active.session.Frames)
		id := active.session.ID
		var fr ScreenlogFrame
		if n > 0 {
			fr = active.session.Frames[n-1]
		}
		active.mu.Unlock()
		if n > 0 {
			return id, fr, true
		}
	}
	// Otherwise fall back to the newest stored session's last frame.
	sessions, err := listScreenlogSessions()
	if err != nil || len(sessions) == 0 {
		return "", ScreenlogFrame{}, false
	}
	sort.Slice(sessions, func(i, j int) bool { return sessions[i].StartedAt > sessions[j].StartedAt })
	for _, s := range sessions {
		full, err := loadScreenlogSession(s.ID)
		if err != nil || len(full.Frames) == 0 {
			continue
		}
		return full.ID, full.Frames[len(full.Frames)-1], true
	}
	return "", ScreenlogFrame{}, false
}

// --- HTTP handlers ---------------------------------------------------------

// handleScreenlogProcessModel is GET/POST /screenlog/process-model?id=<id>.
//
//	GET            → deterministic skeleton + the runner analysis prompt
//	GET &saved=1   → the saved (runner-completed) ProcessModel
//	POST {model}   → save a runner-produced ProcessModel
func (s *HTTPServer) handleScreenlogProcessModel(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	if id == "" {
		jsonError(w, http.StatusBadRequest, "id required")
		return
	}
	switch r.Method {
	case http.MethodGet:
		if r.URL.Query().Get("saved") == "1" {
			pm, err := loadProcessModel(id)
			if err != nil {
				jsonError(w, http.StatusNotFound, "no saved process model for "+id)
				return
			}
			jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true, "processModel": pm})
			return
		}
		pm, _, err := buildProcessSkeleton(id)
		if err != nil {
			jsonError(w, http.StatusNotFound, err.Error())
			return
		}
		jsonReply(w, http.StatusOK, map[string]interface{}{
			"ok": true, "skeleton": pm, "prompt": processModelPrompt(id),
			"hint": "fill the episode semantics from the keyframes (screenlog_frames sample:N), then POST the completed model",
		})
	case http.MethodPost:
		var pm ProcessModel
		if err := json.NewDecoder(r.Body).Decode(&pm); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid ProcessModel JSON")
			return
		}
		pm.SessionID = id
		if err := saveProcessModel(&pm); err != nil {
			jsonError(w, http.StatusInternalServerError, err.Error())
			return
		}
		appendScreenlogAudit(screenlogAuditEntry{Action: "process-model", Session: id, Note: "saved"})
		jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true, "processModel": &pm})
	default:
		jsonError(w, http.StatusMethodNotAllowed, "use GET or POST")
	}
}

// handleScreenlogLive serves the newest frame of the active/most-recent session
// for a near-real-time view from web/mobile (security-cam "live" mode):
//
//	GET            → the latest frame image bytes (image/jpeg|png)
//	GET &meta=1    → JSON {running, sessionId, frame} so the client can poll +
//	                 know whether recording is active and how fresh the frame is
func (s *HTTPServer) handleScreenlogLive(w http.ResponseWriter, r *http.Request) {
	sid, fr, ok := screenlogLatestFrame()
	running := false
	screenlogMu.Lock()
	running = screenlogActive != nil
	screenlogMu.Unlock()

	if r.URL.Query().Get("meta") == "1" {
		jsonReply(w, http.StatusOK, map[string]interface{}{
			"ok": true, "running": running, "sessionId": sid, "frame": fr, "has": ok,
		})
		return
	}
	if !ok || fr.File == "" {
		jsonError(w, http.StatusNotFound, "no frame yet")
		return
	}
	dir, derr := screenlogSessionDir(sid)
	if derr != nil {
		jsonError(w, http.StatusInternalServerError, derr.Error())
		return
	}
	// no-store so the live poller always sees the freshest frame
	w.Header().Set("Cache-Control", "no-store, must-revalidate")
	http.ServeFile(w, r, filepath.Join(dir, filepath.Base(fr.File)))
}
