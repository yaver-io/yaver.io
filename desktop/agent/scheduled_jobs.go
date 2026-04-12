package main

import (
	"encoding/json"
	"net/http"
	"os"
)

// ScheduledJob is a universal scheduled task (cron / Convex scheduled function / pg_cron).
type ScheduledJob struct {
	Name      string `json:"name"`
	Kind      string `json:"kind"` // cron, once, recurring
	NextRun   string `json:"nextRun,omitempty"`
	LastRun   string `json:"lastRun,omitempty"`
	Status    string `json:"status,omitempty"`
	Schedule  string `json:"schedule,omitempty"`
	Target    string `json:"target,omitempty"`
}

func ListScheduledJobs(projectDir string) ([]ScheduledJob, string, error) {
	cfg, err := LoadProjectConfig(projectDir)
	if err != nil {
		return nil, "", err
	}
	switch cfg.Backend {
	case BackendConvex:
		return listConvexScheduled(projectDir)
	case BackendPostgres, BackendSupabase:
		return listPgCron(projectDir, cfg)
	}
	return nil, string(cfg.Backend), nil
}

func listConvexScheduled(projectDir string) ([]ScheduledJob, string, error) {
	client := NewConvexAdminClient(projectDir)
	data, err := client.Query("yaver_admin:listScheduledJobs", nil)
	if err != nil {
		return nil, "convex", err
	}
	var env struct {
		Value []map[string]interface{} `json:"value"`
	}
	if err := json.Unmarshal(data, &env); err != nil {
		return nil, "convex", err
	}
	var out []ScheduledJob
	for _, j := range env.Value {
		job := ScheduledJob{Kind: "convex", Status: "scheduled"}
		if n, ok := j["name"].(string); ok {
			job.Name = n
		}
		if t, ok := j["scheduledTime"].(float64); ok {
			job.NextRun = toRFC3339(int64(t))
		}
		if state, ok := j["state"].(map[string]interface{}); ok {
			if kind, ok := state["kind"].(string); ok {
				job.Status = kind
			}
		}
		out = append(out, job)
	}
	return out, "convex", nil
}

func listPgCron(projectDir string, cfg *YaverProjectConfig) ([]ScheduledJob, string, error) {
	adapter, err := newSQLAdapter(projectDir, cfg, BackendPostgres)
	if err != nil {
		return nil, "postgres", err
	}
	if err := adapter.open(); err != nil {
		return nil, "postgres", err
	}
	rows, err := adapter.db.Query(`SELECT jobid::text, jobname, schedule, command, active::text FROM cron.job`)
	if err != nil {
		// pg_cron not installed — return empty, not error.
		return nil, "postgres", nil
	}
	defer rows.Close()
	var out []ScheduledJob
	for rows.Next() {
		var id, name, sched, cmd, active string
		if err := rows.Scan(&id, &name, &sched, &cmd, &active); err != nil {
			continue
		}
		out = append(out, ScheduledJob{
			Name: name, Kind: "cron", Schedule: sched, Target: cmd, Status: active,
		})
	}
	return out, "postgres", nil
}

func toRFC3339(ms int64) string {
	if ms <= 0 {
		return ""
	}
	// Convex timestamps are ms since epoch.
	return (toTime(ms / 1000)).Format("2006-01-02T15:04:05Z07:00")
}

// ---- MCP / HTTP ----

func mcpJobsList(dir string) interface{} {
	if dir == "" {
		dir, _ = os.Getwd()
	}
	jobs, source, err := ListScheduledJobs(dir)
	if err != nil {
		return map[string]interface{}{"error": err.Error(), "source": source}
	}
	return map[string]interface{}{"source": source, "jobs": jobs}
}

func (s *HTTPServer) handleJobsList(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, mcpJobsList(s.dirParam(r)))
}
