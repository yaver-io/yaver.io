package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"
)

// ── Test App Session ──────────────────────────────────────────────
// Autonomous testing: the Feedback SDK triggers a test session,
// the agent creates a task to test the app, and tracks fixes.

// TestFix represents a single fix applied during a test session.
type TestFix struct {
	ID          string `json:"id"`
	File        string `json:"file"`
	Line        int    `json:"line,omitempty"`
	Description string `json:"description"`
	Error       string `json:"error,omitempty"`
	Diff        string `json:"diff,omitempty"`
	Timestamp   string `json:"timestamp"`
	Verified    bool   `json:"verified,omitempty"`
}

// TestAppSession tracks an autonomous test session.
type TestAppSession struct {
	mu               sync.RWMutex
	Active           bool      `json:"active"`
	SessionID        string    `json:"sessionId,omitempty"`
	TaskID           string    `json:"taskId,omitempty"`
	CurrentScreen    string    `json:"currentScreen,omitempty"`
	ScreensDiscovered int      `json:"screensDiscovered"`
	ScreensTested    int       `json:"screensTested"`
	ErrorsFound      int       `json:"errorsFound"`
	Fixes            []TestFix `json:"fixes"`
	StartedAt        string    `json:"startedAt,omitempty"`
	ElapsedSeconds   float64   `json:"elapsedSeconds,omitempty"`
	Status           string    `json:"status"`
	startTime        time.Time
}

func (s *TestAppSession) toJSON() map[string]interface{} {
	s.mu.RLock()
	defer s.mu.RUnlock()

	elapsed := float64(0)
	if s.Active && !s.startTime.IsZero() {
		elapsed = time.Since(s.startTime).Seconds()
	} else {
		elapsed = s.ElapsedSeconds
	}

	fixes := s.Fixes
	if fixes == nil {
		fixes = []TestFix{}
	}

	return map[string]interface{}{
		"active":            s.Active,
		"sessionId":         s.SessionID,
		"currentScreen":     s.CurrentScreen,
		"screensDiscovered": s.ScreensDiscovered,
		"screensTested":     s.ScreensTested,
		"errorsFound":       s.ErrorsFound,
		"fixes":             fixes,
		"startedAt":         s.StartedAt,
		"elapsedSeconds":    elapsed,
		"status":            s.Status,
	}
}

// AddFix records a fix applied during the session.
func (s *TestAppSession) AddFix(fix TestFix) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Fixes = append(s.Fixes, fix)
}

// ── HTTP Handlers ─────────────────────────────────────────────────

// handleTestAppStart starts an autonomous test session.
// POST /test-app/start { "source": "feedback-sdk" }
func (s *HTTPServer) handleTestAppStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonReply(w, http.StatusMethodNotAllowed, map[string]string{"error": "POST only"})
		return
	}

	var req struct {
		Source string `json:"source"`
	}
	if r.Body != nil {
		json.NewDecoder(r.Body).Decode(&req)
	}

	sessionID := fmt.Sprintf("test-%d", time.Now().UnixMilli())
	now := time.Now()

	session := &TestAppSession{
		Active:    true,
		SessionID: sessionID,
		StartedAt: now.UTC().Format(time.RFC3339),
		Status:    "starting",
		Fixes:     []TestFix{},
		startTime: now,
	}

	// Store session on the HTTP server
	s.testAppSession.Store(sessionID, session)
	s.activeTestAppSession.Store("current", session)

	log.Printf("[test-app] session %s started (source: %s)", sessionID, req.Source)

	// Create a task for the AI agent to run autonomous testing
	if s.taskMgr != nil {
		// Build context from black box if available
		bbContext := ""
		if s.blackboxMgr != nil {
			// Try to get the most recent session's context
			sessions := s.blackboxMgr.ListSessions()
			if len(sessions) > 0 {
				if deviceID, ok := sessions[0]["deviceId"].(string); ok {
					if sess := s.blackboxMgr.GetSession(deviceID); sess != nil {
						bbContext = sess.GenerateBlackBoxContext(100)
					}
				}
			}
		}

		prompt := "Autonomous app test session. " +
			"Navigate through all screens of the running app, interact with UI elements, " +
			"find crashes, errors, and visual issues. For each issue found, write a fix " +
			"and hot reload. Report each fix with the file, line, description, and diff.\n\n"
		if bbContext != "" {
			prompt += "Black box context (recent app events):\n" + bbContext + "\n\n"
		}
		prompt += "When done, summarize: screens tested, errors found, fixes applied."

		task, err := s.taskMgr.CreateTask(
			"Autonomous App Test",
			prompt,
			"",          // model
			"test-app",  // source
			"",          // runner
			"",          // custom command
			nil,         // images
			nil,         // speech context
		)
		if err == nil {
			session.mu.Lock()
			session.TaskID = task.ID
			session.Status = "running"
			session.mu.Unlock()
			log.Printf("[test-app] created task %s for session %s", task.ID, sessionID)

			// Monitor the task in background — update session when task completes
			go s.monitorTestAppTask(sessionID, task.ID)
		} else {
			log.Printf("[test-app] failed to create task: %v", err)
			session.mu.Lock()
			session.Status = "error: failed to create task"
			session.Active = false
			session.mu.Unlock()
		}
	} else {
		session.mu.Lock()
		session.Status = "error: task manager not available"
		session.Active = false
		session.mu.Unlock()
	}

	jsonReply(w, http.StatusOK, map[string]interface{}{
		"sessionId": sessionID,
		"status":    "started",
	})
}

// handleTestAppStop stops the current test session.
// POST /test-app/stop
func (s *HTTPServer) handleTestAppStop(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonReply(w, http.StatusMethodNotAllowed, map[string]string{"error": "POST only"})
		return
	}

	val, ok := s.activeTestAppSession.Load("current")
	if !ok {
		jsonReply(w, http.StatusOK, map[string]string{"status": "no active session"})
		return
	}

	session := val.(*TestAppSession)
	session.mu.Lock()
	session.Active = false
	session.Status = "stopped"
	if !session.startTime.IsZero() {
		session.ElapsedSeconds = time.Since(session.startTime).Seconds()
	}
	taskID := session.TaskID
	session.mu.Unlock()

	// Stop the underlying task if still running
	if taskID != "" && s.taskMgr != nil {
		s.taskMgr.StopTask(taskID)
	}

	log.Printf("[test-app] session stopped")
	jsonReply(w, http.StatusOK, session.toJSON())
}

// handleTestAppStatus returns the current test session state.
// GET /test-app/status
func (s *HTTPServer) handleTestAppStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonReply(w, http.StatusMethodNotAllowed, map[string]string{"error": "GET only"})
		return
	}

	val, ok := s.activeTestAppSession.Load("current")
	if !ok {
		// No session — return empty inactive state
		jsonReply(w, http.StatusOK, map[string]interface{}{
			"active":            false,
			"screensDiscovered": 0,
			"screensTested":     0,
			"errorsFound":       0,
			"fixes":             []TestFix{},
			"status":            "idle",
		})
		return
	}

	session := val.(*TestAppSession)
	jsonReply(w, http.StatusOK, session.toJSON())
}

// monitorTestAppTask watches a task and updates the test session when it completes.
func (s *HTTPServer) monitorTestAppTask(sessionID, taskID string) {
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		val, ok := s.testAppSession.Load(sessionID)
		if !ok {
			return
		}
		session := val.(*TestAppSession)

		session.mu.RLock()
		active := session.Active
		session.mu.RUnlock()
		if !active {
			return
		}

		// Check task status
		if s.taskMgr == nil {
			return
		}
		task, ok := s.taskMgr.GetTask(taskID)
		if !ok || task == nil {
			session.mu.Lock()
			session.Active = false
			session.Status = "completed"
			session.ElapsedSeconds = time.Since(session.startTime).Seconds()
			session.mu.Unlock()
			return
		}

		if task.Status == TaskStatusFinished || task.Status == TaskStatusFailed || task.Status == TaskStatusStopped {
			session.mu.Lock()
			session.Active = false
			session.Status = string(task.Status)
			session.ElapsedSeconds = time.Since(session.startTime).Seconds()
			session.mu.Unlock()
			log.Printf("[test-app] session %s finished: %s", sessionID, task.Status)
			return
		}

		// Parse task output for fix reports (agent outputs structured JSON lines)
		if task.Output != "" {
			parseFixes(session, task.Output)
		}
	}
}

// parseFixes scans task output for structured fix reports.
// The agent is expected to output lines like:
// {"yaver_fix": {"file": "src/App.tsx", "line": 42, "description": "...", "error": "...", "diff": "..."}}
func parseFixes(session *TestAppSession, output string) {
	session.mu.Lock()
	defer session.mu.Unlock()

	existingIDs := make(map[string]bool)
	for _, f := range session.Fixes {
		existingIDs[f.ID] = true
	}

	for _, line := range splitLines(output) {
		if len(line) < 2 || line[0] != '{' {
			continue
		}
		var wrapper struct {
			YaverFix *struct {
				File        string `json:"file"`
				Line        int    `json:"line,omitempty"`
				Description string `json:"description"`
				Error       string `json:"error,omitempty"`
				Diff        string `json:"diff,omitempty"`
			} `json:"yaver_fix"`
			YaverScreen *struct {
				Name string `json:"name"`
			} `json:"yaver_screen"`
			YaverError *struct {
				Message string `json:"message"`
			} `json:"yaver_error"`
		}
		if err := json.Unmarshal([]byte(line), &wrapper); err != nil {
			continue
		}

		if wrapper.YaverFix != nil {
			fix := TestFix{
				ID:          fmt.Sprintf("fix-%d", len(session.Fixes)+1),
				File:        wrapper.YaverFix.File,
				Line:        wrapper.YaverFix.Line,
				Description: wrapper.YaverFix.Description,
				Error:       wrapper.YaverFix.Error,
				Diff:        wrapper.YaverFix.Diff,
				Timestamp:   time.Now().UTC().Format(time.RFC3339),
			}
			if !existingIDs[fix.ID] {
				session.Fixes = append(session.Fixes, fix)
				existingIDs[fix.ID] = true
			}
		}
		if wrapper.YaverScreen != nil {
			session.ScreensDiscovered++
			session.CurrentScreen = wrapper.YaverScreen.Name
		}
		if wrapper.YaverError != nil {
			session.ErrorsFound++
		}
	}
}

func splitLines(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			lines = append(lines, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}
