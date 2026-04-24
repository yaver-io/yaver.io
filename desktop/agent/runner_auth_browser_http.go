package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"
)

type runnerBrowserAuthStartRequest struct {
	Runner string `json:"runner"`
}

type runnerBrowserAuthSession struct {
	ID             string `json:"id"`
	Runner         string `json:"runner"`
	Method         string `json:"method"`
	Status         string `json:"status"`
	OpenURL        string `json:"openUrl,omitempty"`
	Code           string `json:"code,omitempty"`
	Detail         string `json:"detail,omitempty"`
	AuthConfigured bool   `json:"authConfigured,omitempty"`
	AuthSource     string `json:"authSource,omitempty"`
	Error          string `json:"error,omitempty"`
	StartedAt      int64  `json:"startedAt"`
	UpdatedAt      int64  `json:"updatedAt"`
	CompletedAt    int64  `json:"completedAt,omitempty"`
}

type runnerBrowserAuthSessionState struct {
	mu sync.Mutex
	runnerBrowserAuthSession
	cmd    *exec.Cmd
	cancel context.CancelFunc
}

var (
	runnerBrowserAuthSessions sync.Map
	urlPattern                = regexp.MustCompile(`https://[^\s]+`)
	codexCodePattern          = regexp.MustCompile(`\b[A-Z0-9]{4,}-[A-Z0-9]{4,}\b`)
	ansiPattern               = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]`)
)

func normalizeBrowserAuthLine(line string) string {
	line = strings.ReplaceAll(line, "\r", "")
	line = ansiPattern.ReplaceAllString(line, "")
	line = strings.TrimSpace(line)
	if strings.Contains(line, "\x1b]8;") {
		line = strings.ReplaceAll(line, "\x1b]8;;", "")
		line = strings.ReplaceAll(line, "\x1b]8;", "")
		line = strings.ReplaceAll(line, "\a", "")
	}
	return strings.TrimSpace(line)
}

func newRunnerBrowserAuthSession(runner string) *runnerBrowserAuthSessionState {
	now := time.Now().UnixMilli()
	id := fmt.Sprintf("%s-%d", runner, now)
	return &runnerBrowserAuthSessionState{
		runnerBrowserAuthSession: runnerBrowserAuthSession{
			ID:        id,
			Runner:    runner,
			Status:    "starting",
			StartedAt: now,
			UpdatedAt: now,
		},
	}
}

func (s *runnerBrowserAuthSessionState) snapshot() runnerBrowserAuthSession {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.runnerBrowserAuthSession
	refreshRunnerBrowserAuthSnapshot(&out)
	return out
}

func refreshRunnerBrowserAuthSnapshot(out *runnerBrowserAuthSession) {
	rows, err := collectRunnerAuthStatusRows()
	if err != nil {
		return
	}
	row := runnerStatusRowFor(rows, out.Runner)
	out.AuthConfigured = row.AuthConfigured
	out.AuthSource = row.AuthSource
	if out.Status == "completed" && strings.TrimSpace(out.Detail) == "" {
		if row.AuthConfigured && row.AuthSource != "" {
			out.Detail = "Authenticated via " + row.AuthSource
		} else {
			out.Detail = "Authentication completed on the remote machine."
		}
	}
}

func (s *runnerBrowserAuthSessionState) update(fn func(*runnerBrowserAuthSession)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fn(&s.runnerBrowserAuthSession)
	s.runnerBrowserAuthSession.UpdatedAt = time.Now().UnixMilli()
}

func runnerBrowserAuthCommand(runner string) (method string, cmd *exec.Cmd, err error) {
	switch normalizeRunnerAuthName(runner) {
	case "codex":
		cmd = exec.Command("codex", "login", "--device-auth")
		cmd.Env = append(cmd.Environ(), "CI=1", "NO_COLOR=1", "TERM=dumb")
		return "device-auth", cmd, nil
	case "claude":
		cmd = exec.Command("claude", "auth", "login", "--console")
		cmd.Env = append(cmd.Environ(), "CI=1", "NO_COLOR=1", "TERM=dumb")
		return "oauth", cmd, nil
	default:
		return "", nil, fmt.Errorf("unsupported runner %q (want claude or codex)", runner)
	}
}

func scanRunnerBrowserAuthOutput(sess *runnerBrowserAuthSessionState, reader io.Reader) {
	scanner := bufio.NewScanner(reader)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 512*1024)
	for scanner.Scan() {
		line := normalizeBrowserAuthLine(scanner.Text())
		if line == "" {
			continue
		}
		sess.update(func(state *runnerBrowserAuthSession) {
			state.Detail = line
			if state.OpenURL == "" {
				if url := urlPattern.FindString(line); url != "" {
					state.OpenURL = strings.TrimRight(url, ".)")
					if state.Status == "starting" {
						state.Status = "awaiting_browser"
					}
				}
			}
			if state.Runner == "codex" && state.Code == "" {
				if code := codexCodePattern.FindString(line); code != "" {
					state.Code = code
					state.Status = "awaiting_browser"
				}
			}
		})
	}
}

func startRunnerBrowserAuthSession(runner string, onTerminal func()) (*runnerBrowserAuthSessionState, error) {
	runner = normalizeRunnerAuthName(runner)
	sess := newRunnerBrowserAuthSession(runner)
	method, cmd, err := runnerBrowserAuthCommand(runner)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	cmd = exec.CommandContext(ctx, cmd.Path, cmd.Args[1:]...)
	cmd.Env = append(cmd.Environ(), "CI=1", "NO_COLOR=1", "TERM=dumb")
	sess.Method = method
	sess.cmd = cmd
	sess.cancel = cancel

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("stderr pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		cancel()
		return nil, fmt.Errorf("start %s auth: %w", runner, err)
	}

	go scanRunnerBrowserAuthOutput(sess, stdout)
	go scanRunnerBrowserAuthOutput(sess, stderr)
	recordRunnerBrowserAuthOperation(sess.snapshot())
	go func() {
		err := cmd.Wait()
		sess.update(func(state *runnerBrowserAuthSession) {
			state.CompletedAt = time.Now().UnixMilli()
			if err == nil {
				state.Status = "completed"
			} else if ctx.Err() == context.Canceled {
				state.Status = "cancelled"
				if state.Detail == "" {
					state.Detail = "Authentication flow cancelled."
				}
			} else {
				state.Status = "failed"
				state.Error = strings.TrimSpace(err.Error())
				if state.Detail == "" {
					state.Detail = state.Error
				}
			}
			refreshRunnerBrowserAuthSnapshot(state)
		})
		snap := sess.snapshot()
		recordRunnerBrowserAuthOperation(snap)
		recordRunnerBrowserAuthIncident(snap)
		// Heartbeat-kick on terminal so the pill on yaver.io / mobile
		// flips from "sign in" to "ready" within a second, instead of
		// waiting up to 30 s for the next ticker.
		if onTerminal != nil {
			onTerminal()
		}
	}()

	runnerBrowserAuthSessions.Store(sess.ID, sess)
	return sess, nil
}

func recordRunnerBrowserAuthOperation(sess runnerBrowserAuthSession) {
	GlobalOperationStore().Upsert(OperationState{
		ID:        sess.ID,
		Kind:      "runner_browser_auth",
		Status:    sess.Status,
		Message:   sess.Detail,
		StartedAt: sess.StartedAt,
		DeviceID:  "local",
		Metadata: map[string]interface{}{
			"runner":         sess.Runner,
			"method":         sess.Method,
			"authConfigured": sess.AuthConfigured,
			"authSource":     sess.AuthSource,
			"openUrl":        sess.OpenURL,
			"code":           sess.Code,
			"error":          sess.Error,
		},
	})
}

func recordRunnerBrowserAuthIncident(sess runnerBrowserAuthSession) {
	switch sess.Status {
	case "failed":
		GlobalIncidentStore().Append(IncidentEvent{
			Timestamp:       sess.UpdatedAt,
			Severity:        IncidentSeverityError,
			Category:        "runner_auth",
			Code:            "runner.browser_auth.failed",
			Source:          "runner-auth/browser",
			Title:           "Runner browser auth failed",
			UserMessage:     firstNonEmptyBrowserAuth(sess.Detail, sess.Error, "Authentication failed on the remote machine."),
			TechnicalInfo:   sess.Error,
			OperationID:     sess.ID,
			LogsAvailable:   false,
			Recoverable:     true,
			CorrelationID:   sess.ID,
			SuggestedAction: "Retry the browser auth flow or configure the runner with credentials directly.",
			Metadata: map[string]interface{}{
				"runner": sess.Runner,
				"method": sess.Method,
			},
		})
	case "cancelled":
		GlobalIncidentStore().Append(IncidentEvent{
			Timestamp:       sess.UpdatedAt,
			Severity:        IncidentSeverityWarn,
			Category:        "runner_auth",
			Code:            "runner.browser_auth.cancelled",
			Source:          "runner-auth/browser",
			Title:           "Runner browser auth cancelled",
			UserMessage:     firstNonEmptyBrowserAuth(sess.Detail, "Authentication flow cancelled."),
			OperationID:     sess.ID,
			LogsAvailable:   false,
			Recoverable:     true,
			CorrelationID:   sess.ID,
			SuggestedAction: "Start the browser auth flow again when you are ready to finish sign-in.",
			Metadata: map[string]interface{}{
				"runner": sess.Runner,
				"method": sess.Method,
			},
		})
	}
}

func firstNonEmptyBrowserAuth(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func lookupRunnerBrowserAuthSession(id string) (*runnerBrowserAuthSessionState, bool) {
	v, ok := runnerBrowserAuthSessions.Load(strings.TrimSpace(id))
	if !ok {
		return nil, false
	}
	sess, ok := v.(*runnerBrowserAuthSessionState)
	return sess, ok
}

func (s *HTTPServer) handleRunnerBrowserAuthStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}
	var req runnerBrowserAuthStartRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid json body")
		return
	}
	sess, err := startRunnerBrowserAuthSession(req.Runner, s.TriggerHeartbeat)
	if err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	jsonReply(w, http.StatusOK, map[string]any{
		"ok":      true,
		"session": sess.snapshot(),
	})
}

func (s *HTTPServer) handleRunnerBrowserAuthStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, http.StatusMethodNotAllowed, "use GET")
		return
	}
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	if id == "" {
		jsonError(w, http.StatusBadRequest, "missing id")
		return
	}
	sess, ok := lookupRunnerBrowserAuthSession(id)
	if !ok {
		jsonError(w, http.StatusNotFound, "auth session not found")
		return
	}
	jsonReply(w, http.StatusOK, map[string]any{
		"ok":      true,
		"session": func() runnerBrowserAuthSession {
			snap := sess.snapshot()
			recordRunnerBrowserAuthOperation(snap)
			return snap
		}(),
	})
}

func (s *HTTPServer) handleRunnerBrowserAuthCancel(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	if id == "" {
		jsonError(w, http.StatusBadRequest, "missing id")
		return
	}
	sess, ok := lookupRunnerBrowserAuthSession(id)
	if !ok {
		jsonError(w, http.StatusNotFound, "auth session not found")
		return
	}
	if sess.cancel != nil {
		sess.cancel()
	}
	sess.update(func(state *runnerBrowserAuthSession) {
		state.Status = "cancelled"
		if state.Detail == "" {
			state.Detail = "Authentication flow cancelled."
		}
		state.CompletedAt = time.Now().UnixMilli()
	})
	snap := sess.snapshot()
	recordRunnerBrowserAuthOperation(snap)
	recordRunnerBrowserAuthIncident(snap)
	jsonReply(w, http.StatusOK, map[string]any{
		"ok":      true,
		"session": snap,
	})
}
