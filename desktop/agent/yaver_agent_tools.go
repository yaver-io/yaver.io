package main

// yaver_agent_tools.go — aggregated audit endpoint consumed by the
// mobile-embedded yaver-agent.
//
// The mobile yaver-agent is a tiny LLM that runs inside the phone for
// control-plane tasks (auth, status, primary management). To answer
// "what's the state of this device?" naturally, it needs ONE call that
// returns:
//
//   - Yaver-level lifecycle (bootstrap / yaver-auth-expired / ready)
//   - Each supported runner's auth state (claude / codex / opencode)
//   - A short, ordered list of recommended next actions
//
// Doing that as a single endpoint instead of three round trips keeps
// the LLM tool-loop tight and avoids fan-out bugs (one bad fetch hides
// the rest of the story).
//
// The endpoint is read-only and reports only this device's state. To
// audit a remote box, the mobile agent calls the same endpoint via
// proxyToDeviceJSON (relay-tunneled).

import (
	"net/http"
	"os"
	"strings"
)

// YaverAgentRunnerAudit is one runner's state in the audit response.
type YaverAgentRunnerAudit struct {
	ID             string `json:"id"`             // claude / codex / opencode
	Name           string `json:"name"`           // human label
	Installed      bool   `json:"installed"`      // binary on PATH
	Ready          bool   `json:"ready"`          // can run a task right now
	AuthConfigured bool   `json:"authConfigured"` // creds detected
	AuthSource     string `json:"authSource,omitempty"`
	Warning        string `json:"warning,omitempty"`
	Error          string `json:"error,omitempty"`
}

// YaverAgentRecommendation is one suggested next action surfaced to
// the LLM. The LLM picks which Action to invoke; the strings are
// human-readable so they double as default UI copy.
type YaverAgentRecommendation struct {
	Kind     string `json:"kind"` // yaver_auth_required | runner_auth_required | configured
	Target   string `json:"target,omitempty"`
	Severity string `json:"severity"` // info | warn | error
	Title    string `json:"title"`
	Body     string `json:"body"`
	Action   string `json:"action,omitempty"` // tool name the agent should call
}

// YaverAgentReadiness is the first-boot readiness contract used by
// managed-cloud provisioning. State is deliberately coarse so Convex/web/mobile
// can branch without parsing prose.
type YaverAgentReadiness struct {
	State   string   `json:"state"` // ready | needs-reauth
	Reasons []string `json:"reasons,omitempty"`
	Vault   string   `json:"vault"`  // open | missing | locked
	Runner  string   `json:"runner"` // ready | needs-reauth
	Git     string   `json:"git"`    // ready | needs-reauth
}

// YaverAgentDeviceAudit is the full audit response.
type YaverAgentDeviceAudit struct {
	DeviceID        string                     `json:"deviceId,omitempty"`
	LifecycleState  string                     `json:"lifecycleState"` // mirrors AgentLifecycleInfo.State
	Usable          bool                       `json:"usable"`
	NeedsAuth       bool                       `json:"needsAuth"` // true when LifecycleState != ready-to-connect
	Readiness       YaverAgentReadiness        `json:"readiness"`
	Runners         []YaverAgentRunnerAudit    `json:"runners"`
	Recommendations []YaverAgentRecommendation `json:"recommendations"`
}

// runnerAuditOrder fixes the runner order in the response so the LLM
// can rely on positional reasoning ("the second runner is codex").
var runnerAuditOrder = []string{"claude", "codex", "opencode"}

// runnerLabel returns the human-friendly name we'd put in UI copy.
func runnerLabel(id string) string {
	switch id {
	case "claude":
		return "Claude Code"
	case "codex":
		return "Codex"
	case "opencode":
		return "OpenCode"
	default:
		return id
	}
}

// buildYaverAgentDeviceAudit assembles the audit response from
// existing Yaver helpers. Called by the HTTP handler and by tests.
func (s *HTTPServer) buildYaverAgentDeviceAudit(workDir string) YaverAgentDeviceAudit {
	lc := s.lifecycleInfo()
	out := YaverAgentDeviceAudit{
		LifecycleState: string(lc.State),
		Usable:         lc.Usable,
		NeedsAuth:      lc.State != AgentLifecycleReadyToConnect,
		Runners:        make([]YaverAgentRunnerAudit, 0, len(runnerAuditOrder)),
	}
	out.DeviceID = strings.TrimSpace(s.deviceID)

	for _, id := range runnerAuditOrder {
		runnerCfg := GetRunnerConfig(id)
		audit := YaverAgentRunnerAudit{
			ID:   id,
			Name: runnerLabel(id),
		}
		if err := CheckRunnerBinary(runnerCfg.Command); err != nil {
			audit.Installed = false
			audit.Ready = false
			audit.Error = err.Error()
		} else {
			audit.Installed = true
			status := DetectRunnerRuntimeStatus(runnerCfg, workDir)
			audit.Ready = status.Ready
			audit.AuthConfigured = status.AuthConfigured
			audit.AuthSource = status.AuthSource
			audit.Warning = status.Warning
			audit.Error = status.Error
		}
		out.Runners = append(out.Runners, audit)
	}

	out.Readiness = s.buildYaverAgentReadiness(out.Runners)
	out.Recommendations = recommendNextActions(out)
	return out
}

func (s *HTTPServer) buildYaverAgentReadiness(runners []YaverAgentRunnerAudit) YaverAgentReadiness {
	out := YaverAgentReadiness{
		State:  "ready",
		Vault:  probeYaverAgentVaultReadiness(s),
		Runner: "needs-reauth",
		Git:    "needs-reauth",
	}
	for _, runner := range runners {
		if runner.Installed && runner.Ready && runner.AuthConfigured {
			out.Runner = "ready"
			break
		}
	}
	if out.Vault == "locked" {
		out.Reasons = append(out.Reasons, "vault")
	}
	if out.Runner != "ready" {
		out.Reasons = append(out.Reasons, "runner")
	}
	if machineOnboardingGitReady(collectMachineOnboardingStatus()) {
		out.Git = "ready"
	} else {
		out.Reasons = append(out.Reasons, "git")
	}
	if len(out.Reasons) > 0 {
		out.State = "needs-reauth"
	}
	return out
}

func probeYaverAgentVaultReadiness(s *HTTPServer) string {
	if s != nil && s.vaultStore != nil {
		return "open"
	}
	if currentRuntimeVaultStore() != nil {
		return "open"
	}
	path, err := VaultPath()
	if err != nil {
		return "locked"
	}
	if _, statErr := os.Stat(path); statErr != nil {
		if os.IsNotExist(statErr) {
			return "missing"
		}
		return "locked"
	}
	return "locked"
}

func machineOnboardingGitReady(status machineOnboardingStatus) bool {
	for _, p := range status.Providers {
		if (p.ID == "github" || p.ID == "gitlab") && p.CloneReady {
			return true
		}
	}
	return false
}

// recommendNextActions produces an ordered list of suggestions. The
// rules are intentionally simple so the LLM can quote them verbatim:
//
//  1. If the device itself is not yaver-authenticated, that is the
//     blocker — emit a single yaver_auth_required recommendation.
//  2. Otherwise, for each runner that is installed but not authed,
//     emit a runner_auth_required recommendation.
//  3. If everything is fine, emit one "configured" info note so the
//     LLM has something concrete to say back.
func recommendNextActions(audit YaverAgentDeviceAudit) []YaverAgentRecommendation {
	out := []YaverAgentRecommendation{}

	if audit.NeedsAuth {
		title := "Yaver is not authenticated on this device"
		body := "The agent is running but no Yaver session is active. " +
			"Sign in here first; runner auth comes after."
		switch audit.LifecycleState {
		case string(AgentLifecycleBootstrap):
			body = "This device is in bootstrap mode — it has no Yaver " +
				"session yet. Pair it from the phone or run `yaver auth`."
		case string(AgentLifecycleAuthExpired):
			body = "This device's Yaver token expired or was wiped. " +
				"Trigger a headless re-auth and complete it on your phone browser."
		}
		out = append(out, YaverAgentRecommendation{
			Kind:     "yaver_auth_required",
			Severity: "error",
			Title:    title,
			Body:     body,
			Action:   "yaver.start_auth",
		})
		return out
	}

	for _, reason := range audit.Readiness.Reasons {
		switch reason {
		case "vault":
			out = append(out, YaverAgentRecommendation{
				Kind:     "vault_reauth_required",
				Target:   "vault",
				Severity: "warn",
				Title:    "Vault is locked",
				Body:     "The encrypted Yaver vault exists but could not be opened. Re-auth or provide the vault passphrase before first use.",
				Action:   "yaver.start_auth",
			})
		case "git":
			out = append(out, YaverAgentRecommendation{
				Kind:     "git_auth_required",
				Target:   "git",
				Severity: "warn",
				Title:    "Git credentials are missing",
				Body:     "This machine needs GitHub or GitLab clone credentials before it can pull private projects.",
				Action:   "git.connect",
			})
		}
	}

	for _, r := range audit.Runners {
		if !r.Installed {
			continue
		}
		if r.AuthConfigured && r.Ready {
			continue
		}
		// We have an installed-but-not-ready runner. Pick the most
		// useful copy depending on whether auth is missing or there's
		// a runtime blocker (e.g. codex namespace prereqs).
		title := r.Name + " is installed but not authenticated"
		body := r.Error
		if strings.TrimSpace(body) == "" {
			body = r.Name + " can't run yet — check its auth state."
		}
		action := "runner.start_auth"
		if !r.AuthConfigured {
			action = "runner.start_auth"
		} else if !r.Ready && r.Error != "" {
			// Auth is fine but there's a runtime blocker — different
			// fix path (sandbox prereqs etc), so different action.
			action = "runner.diagnose"
			title = r.Name + " is authenticated but not ready"
		}
		out = append(out, YaverAgentRecommendation{
			Kind:     "runner_auth_required",
			Target:   r.ID,
			Severity: "warn",
			Title:    title,
			Body:     body,
			Action:   action,
		})
	}

	if len(out) == 0 {
		out = append(out, YaverAgentRecommendation{
			Kind:     "configured",
			Severity: "info",
			Title:    "All set",
			Body:     "Yaver and at least one runner are authenticated on this device.",
		})
	}
	return out
}

// handleYaverAgentDeviceAudit serves GET /yaver-agent/audit. The
// optional ?workDir= query param scopes runner readiness checks to a
// specific project (some runner statuses depend on workDir, e.g.
// codex bwrap permission probes).
func (s *HTTPServer) handleYaverAgentDeviceAudit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonReply(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	workDir := strings.TrimSpace(r.URL.Query().Get("workDir"))
	audit := s.buildYaverAgentDeviceAudit(workDir)
	jsonReply(w, http.StatusOK, audit)
}
