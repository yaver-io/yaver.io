package main

import (
	"crypto/sha1"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// ErrorEvent is a single ingested error instance.
type ErrorEvent struct {
	ID          string                 `json:"id"`
	Fingerprint string                 `json:"fingerprint"`
	Project     string                 `json:"project"`
	Env         string                 `json:"env,omitempty"`
	Message     string                 `json:"message"`
	Stack       string                 `json:"stack,omitempty"`
	URL         string                 `json:"url,omitempty"`
	UserID      string                 `json:"userId,omitempty"`
	Context     map[string]interface{} `json:"context,omitempty"`
	ReceivedAt  time.Time              `json:"receivedAt"`
}

// ErrorGroup is the deduped aggregate shown to the developer.
type ErrorGroup struct {
	Fingerprint string    `json:"fingerprint"`
	Project     string    `json:"project,omitempty"`
	Message     string    `json:"message"`
	Count       int       `json:"count"`
	FirstSeen   time.Time `json:"firstSeen"`
	LastSeen    time.Time `json:"lastSeen"`
	LastStack   string    `json:"lastStack,omitempty"`
	LastURL     string    `json:"lastUrl,omitempty"`
	LastUser    string    `json:"lastUser,omitempty"`
	Resolved    bool      `json:"resolved"`
}

type errorTracker struct {
	db *sql.DB
	mu sync.Mutex
}

var globalErrorTracker *errorTracker

func ensureErrorTracker() (*errorTracker, error) {
	if globalErrorTracker != nil {
		return globalErrorTracker, nil
	}
	base, err := ConfigDir()
	if err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", filepath.Join(base, "errors.db"))
	if err != nil {
		return nil, err
	}
	_, _ = db.Exec(`CREATE TABLE IF NOT EXISTS errors (
		id TEXT PRIMARY KEY, fingerprint TEXT, project TEXT, env TEXT,
		message TEXT, stack TEXT, url TEXT, user_id TEXT, context TEXT,
		received_at DATETIME
	)`)
	_, _ = db.Exec(`CREATE INDEX IF NOT EXISTS idx_err_fp ON errors(fingerprint)`)
	_, _ = db.Exec(`CREATE INDEX IF NOT EXISTS idx_err_received ON errors(received_at DESC)`)
	_, _ = db.Exec(`CREATE TABLE IF NOT EXISTS error_groups (
		fingerprint TEXT PRIMARY KEY, resolved INTEGER DEFAULT 0
	)`)
	globalErrorTracker = &errorTracker{db: db}
	return globalErrorTracker, nil
}

func fingerprintError(message, stack string) string {
	h := sha1.New()
	// Use first 3 stack frames + message (minus dynamic bits like line numbers).
	parts := strings.Split(stack, "\n")
	if len(parts) > 3 {
		parts = parts[:3]
	}
	h.Write([]byte(message))
	h.Write([]byte{0})
	h.Write([]byte(strings.Join(parts, "\n")))
	return hex.EncodeToString(h.Sum(nil))[:16]
}

func (t *errorTracker) Ingest(ev *ErrorEvent) error {
	if ev.Message == "" {
		return fmt.Errorf("message required")
	}
	if ev.ReceivedAt.IsZero() {
		ev.ReceivedAt = time.Now()
	}
	if ev.Fingerprint == "" {
		ev.Fingerprint = fingerprintError(ev.Message, ev.Stack)
	}
	if ev.ID == "" {
		ev.ID = fmt.Sprintf("err_%d_%s", ev.ReceivedAt.UnixNano(), ev.Fingerprint[:6])
	}
	ctx, _ := json.Marshal(ev.Context)
	t.mu.Lock()
	defer t.mu.Unlock()
	_, err := t.db.Exec(`INSERT OR REPLACE INTO errors(id, fingerprint, project, env, message, stack, url, user_id, context, received_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		ev.ID, ev.Fingerprint, ev.Project, ev.Env, ev.Message, ev.Stack, ev.URL, ev.UserID, string(ctx), ev.ReceivedAt)
	if err != nil {
		return err
	}
	_, _ = t.db.Exec(`INSERT OR IGNORE INTO error_groups(fingerprint) VALUES (?)`, ev.Fingerprint)
	return nil
}

// ListGroups returns grouped error issues newest-last-seen first.
func (t *errorTracker) ListGroups(project string, limit int) ([]ErrorGroup, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	where := ""
	args := []interface{}{}
	if project != "" {
		where = "WHERE e.project = ?"
		args = append(args, project)
	}
	args = append(args, limit)
	q := fmt.Sprintf(`
		SELECT e.fingerprint, e.project, MAX(e.message), COUNT(*), MIN(e.received_at), MAX(e.received_at),
			(SELECT stack FROM errors WHERE fingerprint = e.fingerprint ORDER BY received_at DESC LIMIT 1),
			(SELECT url FROM errors WHERE fingerprint = e.fingerprint ORDER BY received_at DESC LIMIT 1),
			(SELECT user_id FROM errors WHERE fingerprint = e.fingerprint ORDER BY received_at DESC LIMIT 1),
			COALESCE(g.resolved, 0)
		FROM errors e
		LEFT JOIN error_groups g ON e.fingerprint = g.fingerprint
		%s
		GROUP BY e.fingerprint ORDER BY MAX(e.received_at) DESC LIMIT ?`, where)
	rows, err := t.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ErrorGroup
	for rows.Next() {
		var g ErrorGroup
		var resolved int
		if err := rows.Scan(&g.Fingerprint, &g.Project, &g.Message, &g.Count, &g.FirstSeen, &g.LastSeen, &g.LastStack, &g.LastURL, &g.LastUser, &resolved); err != nil {
			continue
		}
		g.Resolved = resolved == 1
		out = append(out, g)
	}
	return out, nil
}

func (t *errorTracker) Resolve(fp string, resolved bool) error {
	var v int
	if resolved {
		v = 1
	}
	_, err := t.db.Exec(`INSERT OR REPLACE INTO error_groups(fingerprint, resolved) VALUES (?, ?)`, fp, v)
	return err
}

func (t *errorTracker) Instances(fp string, limit int) ([]ErrorEvent, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := t.db.Query(`SELECT id, fingerprint, project, env, message, stack, url, user_id, context, received_at FROM errors WHERE fingerprint = ? ORDER BY received_at DESC LIMIT ?`, fp, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ErrorEvent
	for rows.Next() {
		var ev ErrorEvent
		var ctx string
		if err := rows.Scan(&ev.ID, &ev.Fingerprint, &ev.Project, &ev.Env, &ev.Message, &ev.Stack, &ev.URL, &ev.UserID, &ctx, &ev.ReceivedAt); err != nil {
			continue
		}
		if ctx != "" {
			_ = json.Unmarshal([]byte(ctx), &ev.Context)
		}
		out = append(out, ev)
	}
	return out, nil
}

// ---- HTTP handlers ----

func (s *HTTPServer) handleErrorIngest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	t, err := ensureErrorTracker()
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{"error": err.Error()})
		return
	}
	body, _ := io.ReadAll(r.Body)
	// Support both single event and batch.
	var single ErrorEvent
	if err := json.Unmarshal(body, &single); err == nil && single.Message != "" {
		if err := t.Ingest(&single); err != nil {
			writeJSON(w, http.StatusOK, map[string]interface{}{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true, "id": single.ID, "fingerprint": single.Fingerprint})
		return
	}
	var batch []*ErrorEvent
	if err := json.Unmarshal(body, &batch); err != nil {
		jsonError(w, http.StatusBadRequest, "expected ErrorEvent or []ErrorEvent")
		return
	}
	for _, ev := range batch {
		_ = t.Ingest(ev)
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true, "count": len(batch)})
}

func (s *HTTPServer) handleErrorGroups(w http.ResponseWriter, r *http.Request) {
	t, err := ensureErrorTracker()
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{"error": err.Error()})
		return
	}
	project := r.URL.Query().Get("project")
	limit := 100
	fmt.Sscanf(r.URL.Query().Get("limit"), "%d", &limit)
	groups, err := t.ListGroups(project, limit)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"groups": groups})
}

func (s *HTTPServer) handleErrorInstances(w http.ResponseWriter, r *http.Request) {
	t, err := ensureErrorTracker()
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{"error": err.Error()})
		return
	}
	fp := r.URL.Query().Get("fingerprint")
	if fp == "" {
		jsonError(w, http.StatusBadRequest, "fingerprint required")
		return
	}
	inst, err := t.Instances(fp, 50)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"instances": inst})
}

func (s *HTTPServer) handleErrorResolve(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	t, err := ensureErrorTracker()
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{"error": err.Error()})
		return
	}
	var b struct {
		Fingerprint string `json:"fingerprint"`
		Resolved    bool   `json:"resolved"`
	}
	_ = json.NewDecoder(r.Body).Decode(&b)
	if err := t.Resolve(b.Fingerprint, b.Resolved); err != nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true})
}
