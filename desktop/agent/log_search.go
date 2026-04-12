package main

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// LogIndex keeps a rolling SQLite FTS5 index of recent container log lines so
// the developer can grep across every service at once.
type LogIndex struct {
	db   *sql.DB
	mu   sync.Mutex
	tail map[string]context.CancelFunc // service name → cancel fn for its tailer
}

var globalLogIndex *LogIndex

func ensureLogIndex() (*LogIndex, error) {
	if globalLogIndex != nil {
		return globalLogIndex, nil
	}
	base, err := ConfigDir()
	if err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", filepath.Join(base, "logs.db"))
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(`CREATE VIRTUAL TABLE IF NOT EXISTS log_lines USING fts5(service UNINDEXED, ts UNINDEXED, line)`); err != nil {
		return nil, err
	}
	globalLogIndex = &LogIndex{db: db, tail: map[string]context.CancelFunc{}}
	return globalLogIndex, nil
}

// Ingest pushes a single log line into the index.
func (l *LogIndex) Ingest(service, line string) {
	if strings.TrimSpace(line) == "" {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	_, _ = l.db.Exec(`INSERT INTO log_lines(service, ts, line) VALUES (?, ?, ?)`,
		service, time.Now().UTC().Format(time.RFC3339Nano), line)
	// Cap at 200k rows to keep the DB bounded.
	_, _ = l.db.Exec(`DELETE FROM log_lines WHERE rowid IN (SELECT rowid FROM log_lines ORDER BY rowid LIMIT max(0, (SELECT count(*) FROM log_lines) - 200000))`)
}

// Search runs an FTS5 MATCH query and returns the latest hits.
type LogHit struct {
	Service string `json:"service"`
	TS      string `json:"ts"`
	Line    string `json:"line"`
}

func (l *LogIndex) Search(q string, services []string, limit int) ([]LogHit, error) {
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	args := []interface{}{q}
	where := `log_lines MATCH ?`
	if len(services) > 0 {
		placeholders := make([]string, len(services))
		for i, s := range services {
			placeholders[i] = "?"
			args = append(args, s)
		}
		where += " AND service IN (" + strings.Join(placeholders, ",") + ")"
	}
	args = append(args, limit)
	rows, err := l.db.Query(`SELECT service, ts, line FROM log_lines WHERE `+where+` ORDER BY rowid DESC LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []LogHit
	for rows.Next() {
		var h LogHit
		if err := rows.Scan(&h.Service, &h.TS, &h.Line); err != nil {
			continue
		}
		out = append(out, h)
	}
	return out, nil
}

// StartTailing attaches to a service's docker logs stream and feeds the index.
// Idempotent: calling twice for the same service only starts one tailer.
func (l *LogIndex) StartTailing(service string, composePath string) {
	l.mu.Lock()
	if _, ok := l.tail[service]; ok {
		l.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	l.tail[service] = cancel
	l.mu.Unlock()

	go func() {
		cmd := exec.CommandContext(ctx, "docker", "compose", "-p", "yaver-services",
			"-f", composePath, "logs", "-f", "--no-log-prefix", "--no-color", "--tail", "20", service)
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			return
		}
		cmd.Stderr = cmd.Stdout
		if err := cmd.Start(); err != nil {
			return
		}
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 64*1024), 1024*1024)
		for scanner.Scan() {
			l.Ingest(service, scanner.Text())
		}
	}()
}

func (l *LogIndex) StopTailing(service string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if cancel, ok := l.tail[service]; ok {
		cancel()
		delete(l.tail, service)
	}
}

// StartTailingAll attaches tailers for every configured Docker service in a project.
func StartTailingAllLogs(projectDir string) error {
	idx, err := ensureLogIndex()
	if err != nil {
		return err
	}
	sm := NewServicesManager(projectDir)
	cfg, err := sm.LoadConfig()
	if err != nil {
		return err
	}
	for name, svc := range cfg.Services {
		if svc.Binary != "" {
			continue
		}
		idx.StartTailing(name, sm.composePath())
	}
	return nil
}

// ---- HTTP / MCP ----

func (s *HTTPServer) handleLogSearch(w http.ResponseWriter, r *http.Request) {
	idx, err := ensureLogIndex()
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{"error": err.Error()})
		return
	}
	// Opportunistically start tailing this project's services so the first
	// search after agent restart works without the user clicking "Start indexer".
	go func() {
		dir := s.dirParam(r)
		if dir != "" {
			_ = StartTailingAllLogs(dir)
		}
	}()
	q := r.URL.Query()
	query := q.Get("q")
	if query == "" {
		query = "*"
	}
	var services []string
	if s := q.Get("services"); s != "" {
		services = strings.Split(s, ",")
	}
	limit := 200
	fmt.Sscanf(q.Get("limit"), "%d", &limit)
	hits, err := idx.Search(query, services, limit)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"hits": hits, "count": len(hits)})
}

func (s *HTTPServer) handleLogIndexStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	var b struct{ Project string `json:"project"` }
	_ = json.NewDecoder(r.Body).Decode(&b)
	dir := b.Project
	if dir == "" {
		dir = s.dirParam(r)
	}
	if err := StartTailingAllLogs(dir); err != nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true})
}

func mcpLogSearch(q string, servicesCSV string, limit int) interface{} {
	idx, err := ensureLogIndex()
	if err != nil {
		return map[string]interface{}{"error": err.Error()}
	}
	var services []string
	if servicesCSV != "" {
		services = strings.Split(servicesCSV, ",")
	}
	if q == "" {
		q = "*"
	}
	hits, err := idx.Search(q, services, limit)
	if err != nil {
		return map[string]interface{}{"error": err.Error()}
	}
	return map[string]interface{}{"hits": hits, "count": len(hits)}
}
