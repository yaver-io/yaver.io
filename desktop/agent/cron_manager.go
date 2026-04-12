package main

import (
	"encoding/json"
	"fmt"
	"net/http"
)

// CreateScheduledJob writes a new scheduled job using the project's backend:
//   - Postgres/Supabase: pg_cron.schedule('name', 'cron', 'SQL')
//   - Convex: cron.ts additions (write-only, user must deploy)
//   - Others: stored in .yaver/cron.yaml and fired by a local ticker (future)
func CreateScheduledJob(projectDir, name, schedule, target string) (interface{}, error) {
	cfg, err := LoadProjectConfig(projectDir)
	if err != nil {
		return nil, err
	}
	switch cfg.Backend {
	case BackendPostgres, BackendSupabase:
		adapter, err := newSQLAdapter(projectDir, cfg, BackendPostgres)
		if err != nil {
			return nil, err
		}
		if err := adapter.open(); err != nil {
			return nil, err
		}
		q := fmt.Sprintf(`SELECT cron.schedule(%s, %s, %s)`,
			sqlLit(name), sqlLit(schedule), sqlLit(target))
		if _, err := adapter.db.Exec(q); err != nil {
			return nil, fmt.Errorf("pg_cron schedule: %w (is pg_cron installed? `CREATE EXTENSION pg_cron;`)", err)
		}
		return map[string]interface{}{"backend": "pg_cron", "name": name, "schedule": schedule}, nil
	case BackendConvex:
		return map[string]interface{}{
			"backend": "convex",
			"note": "Add this to convex/crons.ts and run `npx convex deploy`:",
			"snippet": fmt.Sprintf(`
import { cronJobs } from "convex/server";
import { internal } from "./_generated/api";

const crons = cronJobs();
crons.cron("%s", "%s", internal.%s);
export default crons;
`, name, schedule, target),
		}, nil
	}
	return nil, fmt.Errorf("no scheduler for backend %q — use native Postgres (pg_cron) or Convex", cfg.Backend)
}

// DeleteScheduledJob removes a pg_cron entry by name.
func DeleteScheduledJob(projectDir, name string) error {
	cfg, err := LoadProjectConfig(projectDir)
	if err != nil {
		return err
	}
	if cfg.Backend != BackendPostgres && cfg.Backend != BackendSupabase {
		return fmt.Errorf("only pg_cron deletions supported (for Convex, edit crons.ts and deploy)")
	}
	adapter, err := newSQLAdapter(projectDir, cfg, BackendPostgres)
	if err != nil {
		return err
	}
	if err := adapter.open(); err != nil {
		return err
	}
	_, err = adapter.db.Exec(fmt.Sprintf(`SELECT cron.unschedule(%s)`, sqlLit(name)))
	return err
}

func sqlLit(s string) string {
	return "'" + replaceAll(s, "'", "''") + "'"
}

func replaceAll(s, old, new string) string {
	out := ""
	for {
		i := indexStr(s, old)
		if i < 0 {
			return out + s
		}
		out += s[:i] + new
		s = s[i+len(old):]
	}
}

func indexStr(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

// ---- HTTP / MCP ----

func (s *HTTPServer) handleCronCreate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	var b struct {
		Name     string `json:"name"`
		Schedule string `json:"schedule"`
		Target   string `json:"target"`
	}
	_ = json.NewDecoder(r.Body).Decode(&b)
	res, err := CreateScheduledJob(s.dirParam(r), b.Name, b.Schedule, b.Target)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *HTTPServer) handleCronDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	var b struct{ Name string `json:"name"` }
	_ = json.NewDecoder(r.Body).Decode(&b)
	if err := DeleteScheduledJob(s.dirParam(r), b.Name); err != nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true})
}
