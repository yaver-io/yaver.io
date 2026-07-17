package main

// health_deep.go — P8 install + self-healing hardening.
//
// A single HTTP endpoint (/health/deep) + one MCP verb (yaver_health_deep)
// that returns *actionable* health for the pieces most likely to
// silently stall a runner:
//
//   agent            — HTTP mux is answering (implicit — the handler ran)
//   tmux             — binary on PATH, at least one session enumerable
//   runner_keeper    — configured, per-session mode + last-activity age
//   remote_runtime   — session count + WebRTC pump alive count
//   convex_reachable — best-effort optional (skipped when offline)
//
// Recovery hints are graduated (nudge → restart runner → resign
// binary → reinstall) and reported in the response so the caller can
// decide what to do rather than the agent silently taking destructive
// action. Any actual restart is out of scope for this handler; the
// existing SIGKILL / resign-macos-adhoc paths handle that.
//
// This is P8's minimum: closed-loop *visibility*. Automated recovery
// wiring beyond the nudges the P7 keeper already does is follow-on.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"strings"
	"time"
)

// DeepHealthReport is what /health/deep + yaver_health_deep return.
type DeepHealthReport struct {
	OK               bool                     `json:"ok"`
	GeneratedAt      string                   `json:"generatedAt"`
	Agent            DeepHealthComponent      `json:"agent"`
	Tmux             DeepHealthComponent      `json:"tmux"`
	RunnerKeeper     DeepHealthComponent      `json:"runnerKeeper"`
	RemoteRuntime    DeepHealthComponent      `json:"remoteRuntime"`
	Sessions         []DeepHealthSession      `json:"sessions,omitempty"`
	RecoveryHints    []string                 `json:"recoveryHints,omitempty"`
	Notes            []string                 `json:"notes,omitempty"`
}

// DeepHealthComponent is a small green/yellow/red view of one subsystem.
type DeepHealthComponent struct {
	Status string `json:"status"` // ok | degraded | down
	Detail string `json:"detail,omitempty"`
}

// DeepHealthSession describes one supervised runner session.
type DeepHealthSession struct {
	SessionName       string `json:"sessionName"`
	Mode              string `json:"mode"`
	LastActivity      string `json:"lastActivity,omitempty"`
	LastActivityAgeS  int    `json:"lastActivityAgeSeconds"`
	QueuedCount       int    `json:"queuedCount"`
	NudgesTotal       int    `json:"nudgesTotal"`
	Status            string `json:"status"` // ok | idle | stalled | draining
	RecoveryHint      string `json:"recoveryHint,omitempty"`
}

// composeDeepHealth builds the report. Split from the HTTP handler
// so the MCP verb can call it directly without going through the
// mux (and tests can drive it deterministically).
func composeDeepHealth(s *HTTPServer, now time.Time) DeepHealthReport {
	r := DeepHealthReport{
		OK:          true,
		GeneratedAt: now.UTC().Format(time.RFC3339),
		Agent:       DeepHealthComponent{Status: "ok", Detail: "HTTP mux answered"},
	}

	// tmux availability + session count. Resolved via tmuxBin, not $PATH: the
	// daemon's PATH omits /opt/homebrew/bin, so a PATH-only probe reports an
	// installed tmux as missing and sends the reader chasing an install hint
	// they have already followed.
	if tmux := tmuxBin(); tmux == "" {
		r.Tmux = DeepHealthComponent{Status: "down", Detail: "tmux is not installed — runner sessions cannot be supervised"}
		r.RecoveryHints = append(r.RecoveryHints, TmuxInstallHint())
		r.OK = false
	} else {
		out, _ := exec.Command(tmux, "ls").CombinedOutput()
		count := 0
		for _, line := range strings.Split(string(out), "\n") {
			if strings.TrimSpace(line) != "" {
				count++
			}
		}
		r.Tmux = DeepHealthComponent{Status: "ok", Detail: fmt.Sprintf("%d session(s) visible", count)}
	}

	// Runner keeper — per-session mode + activity age.
	if s == nil || s.runnerKeeper == nil {
		r.RunnerKeeper = DeepHealthComponent{Status: "down", Detail: "not initialised on this agent"}
		r.Notes = append(r.Notes, "call any runner_* MCP verb once to lazily initialise the keeper")
	} else {
		states := s.runnerKeeper.AllStates()
		r.RunnerKeeper = DeepHealthComponent{Status: "ok", Detail: fmt.Sprintf("%d session(s) supervised", len(states))}
		for _, st := range states {
			row := DeepHealthSession{
				SessionName:  st.SessionName,
				Mode:         string(st.Mode),
				LastActivity: st.LastActivity,
				QueuedCount:  st.QueuedCount,
				NudgesTotal:  st.NudgesTotal,
				Status:       "ok",
			}
			if st.LastActivity != "" {
				if last, err := time.Parse(time.RFC3339, st.LastActivity); err == nil {
					age := now.Sub(last)
					row.LastActivityAgeS = int(age.Seconds())
					switch {
					case age > 15*time.Minute:
						row.Status = "stalled"
						row.RecoveryHint = "pane hasn't moved in 15+ minutes; consider a manual runner_attach or restart"
					case age > 5*time.Minute && st.Mode == KeeperModeAuto:
						row.Status = "idle"
					case age > 90*time.Second && st.QueuedCount > 0:
						row.Status = "draining"
					}
				}
			}
			r.Sessions = append(r.Sessions, row)
		}
	}

	// Remote-runtime sessions — count + pump liveness.
	if s == nil || s.remoteRuntimeMgr == nil {
		r.RemoteRuntime = DeepHealthComponent{Status: "ok", Detail: "no active sessions"}
	} else {
		list := s.remoteRuntimeMgr.List()
		liveCount := 0
		for _, sess := range list {
			if live, ok := s.remoteRuntimeMgr.getLive(sess.ID); ok && live != nil {
				liveCount++
			}
		}
		r.RemoteRuntime = DeepHealthComponent{Status: "ok",
			Detail: fmt.Sprintf("%d session(s), %d with live pumps", len(list), liveCount)}
	}

	return r
}

// handleHealthDeep is the HTTP surface. GET only; no auth required
// beyond the standard s.auth wrapper the mux applies elsewhere.
func (s *HTTPServer) handleHealthDeep(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, http.StatusMethodNotAllowed, "use GET")
		return
	}
	report := composeDeepHealth(s, time.Now())
	buf, _ := json.MarshalIndent(report, "", "  ")
	w.Header().Set("Content-Type", "application/json")
	if !report.OK {
		w.WriteHeader(http.StatusServiceUnavailable)
	}
	_, _ = w.Write(buf)
}
