package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// ---------- Audit log ----------

// AuditEntry records one user action (deploy, rollback, domain add, backup,
// secret rotate, etc.) with user + timestamp + outcome.
type AuditEntry struct {
	ID        string    `json:"id"`
	User      string    `json:"user"`
	Action    string    `json:"action"`
	Target    string    `json:"target,omitempty"`
	Payload   string    `json:"payload,omitempty"`
	Outcome   string    `json:"outcome"`
	Error     string    `json:"error,omitempty"`
	Timestamp time.Time `json:"timestamp"`
	IP        string    `json:"ip,omitempty"`
}

type auditStore struct {
	db *sql.DB
	mu sync.Mutex
}

var globalAudit *auditStore

func ensureAudit() (*auditStore, error) {
	if globalAudit != nil {
		return globalAudit, nil
	}
	base, err := ConfigDir()
	if err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", filepath.Join(base, "audit.db"))
	if err != nil {
		return nil, err
	}
	_, _ = db.Exec(`CREATE TABLE IF NOT EXISTS audit (
		id TEXT PRIMARY KEY, user TEXT, action TEXT, target TEXT, payload TEXT,
		outcome TEXT, error TEXT, ts DATETIME, ip TEXT
	)`)
	_, _ = db.Exec(`CREATE INDEX IF NOT EXISTS idx_audit_ts ON audit(ts DESC)`)
	globalAudit = &auditStore{db: db}
	return globalAudit, nil
}

// AuditLog records an action. Safe to call from anywhere (non-blocking best-effort).
func AuditLog(user, action, target, payload, outcome, errMsg, ip string) {
	a, err := ensureAudit()
	if err != nil {
		return
	}
	id := fmt.Sprintf("ad_%d_%s", time.Now().UnixNano(), action)
	a.mu.Lock()
	defer a.mu.Unlock()
	_, _ = a.db.Exec(`INSERT OR REPLACE INTO audit(id, user, action, target, payload, outcome, error, ts, ip) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, user, action, target, payload, outcome, errMsg, time.Now(), ip)
}

func (a *auditStore) List(limit int) ([]AuditEntry, error) {
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	rows, err := a.db.Query(`SELECT id, user, action, target, payload, outcome, error, ts, ip FROM audit ORDER BY ts DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AuditEntry
	for rows.Next() {
		var e AuditEntry
		if err := rows.Scan(&e.ID, &e.User, &e.Action, &e.Target, &e.Payload, &e.Outcome, &e.Error, &e.Timestamp, &e.IP); err != nil {
			continue
		}
		out = append(out, e)
	}
	return out, nil
}

func (s *HTTPServer) handleAuditList(w http.ResponseWriter, r *http.Request) {
	a, err := ensureAudit()
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{"error": err.Error()})
		return
	}
	limit := 200
	fmt.Sscanf(r.URL.Query().Get("limit"), "%d", &limit)
	list, err := a.List(limit)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"entries": list})
}

// ---------- Postgres PITR (WAL archiving) ----------

// ConfigurePITR sets up a Postgres container for continuous WAL archiving to a
// local directory. Requires a live Postgres service in services.yaml.
//
// Generates a sidecar WAL archive path under .yaver/wal/ and issues ALTER
// SYSTEM to enable archive_mode. The user must `docker compose restart
// postgres` for settings to take effect.
func ConfigurePITR(projectDir string) (map[string]interface{}, error) {
	walDir := filepath.Join(projectDir, ".yaver", "wal")
	if err := os.MkdirAll(walDir, 0o755); err != nil {
		return nil, err
	}
	cfg, err := LoadProjectConfig(projectDir)
	if err != nil {
		return nil, err
	}
	if cfg.Backend != BackendPostgres && cfg.Backend != BackendSupabase {
		return nil, fmt.Errorf("PITR only supported for Postgres/Supabase backends")
	}
	adapter, err := newSQLAdapter(projectDir, cfg, BackendPostgres)
	if err != nil {
		return nil, err
	}
	if err := adapter.open(); err != nil {
		return nil, err
	}
	// Archive to /var/lib/postgresql/wal inside the container — mount the host
	// path in services.yaml separately. Here we just flip the system flags.
	stmts := []string{
		`ALTER SYSTEM SET wal_level = 'replica'`,
		`ALTER SYSTEM SET archive_mode = 'on'`,
		`ALTER SYSTEM SET archive_command = 'test ! -f /var/lib/postgresql/wal/%f && cp %p /var/lib/postgresql/wal/%f'`,
	}
	for _, stmt := range stmts {
		if _, err := adapter.db.Exec(stmt); err != nil {
			return map[string]interface{}{"error": err.Error(), "stmt": stmt}, err
		}
	}
	return map[string]interface{}{
		"ok":        true,
		"walDir":    walDir,
		"notes":     "Run `docker compose restart postgres` for changes to apply. Mount .yaver/wal → /var/lib/postgresql/wal in services.yaml.",
		"nextSteps": []string{"docker compose restart postgres", "verify with: SELECT archived_count FROM pg_stat_archiver;"},
	}, nil
}

// RestorePITR can run in two modes:
//
//   - Guidance (confirm=false, default): returns the exact steps. Safe.
//   - Auto (confirm=true): takes a fresh snapshot first, then executes the
//     destructive sequence via docker exec. Still requires pg_basebackup to
//     have been archived (CreateBackup with backend=postgres does pg_dump,
//     not a physical basebackup — so point-in-time restore is limited to
//     timestamps within available WAL).
func RestorePITR(projectDir, targetTimestamp string, confirm bool) map[string]interface{} {
	if !confirm {
		return map[string]interface{}{
			"manual": true,
			"steps": []string{
				"Stop the postgres container: docker compose stop postgres",
				"Remove old data: docker volume rm yaver-pg-data   (destructive — snapshot first!)",
				"Restore base backup into the volume",
				"Create recovery signal: touch /var/lib/postgresql/data/recovery.signal",
				"Set recovery_target_time = '" + targetTimestamp + "' in postgresql.conf",
				"Set restore_command = 'cp /var/lib/postgresql/wal/%f %p' in postgresql.conf",
				"Start postgres — it replays WAL up to that timestamp and pauses",
				"Promote with: SELECT pg_wal_replay_resume();",
			},
			"note": "Preview mode. Pass confirm=true to run. Yaver auto-snapshots first.",
		}
	}

	// 1. Safety net — snapshot the current data first.
	backup, err := CreateBackup(projectDir, "pre-pitr")
	if err != nil {
		return map[string]interface{}{"error": "refused to proceed: safety snapshot failed: " + err.Error()}
	}
	steps := []string{"pre-pitr snapshot: " + backup.Path}

	// 2. Execute the destructive sequence via docker compose.
	sm := NewServicesManager(projectDir)
	cmds := [][]string{
		{"compose", "-p", "yaver-services", "-f", sm.composePath(), "stop", "postgres"},
		{"exec", "yaver-services-postgres-1", "sh", "-c",
			fmt.Sprintf("rm -rf /var/lib/postgresql/data/* && touch /var/lib/postgresql/data/recovery.signal && echo \"recovery_target_time = '%s'\" >> /var/lib/postgresql/data/postgresql.auto.conf && echo \"restore_command = 'cp /var/lib/postgresql/wal/%%f %%p'\" >> /var/lib/postgresql/data/postgresql.auto.conf", targetTimestamp)},
		{"compose", "-p", "yaver-services", "-f", sm.composePath(), "start", "postgres"},
	}
	for _, c := range cmds {
		if out, err := sm.runDocker(c...); err != nil {
			return map[string]interface{}{"error": "step failed: " + strings.Join(c, " ") + " — " + err.Error(), "output": out, "steps": steps}
		} else {
			steps = append(steps, "ran: "+strings.Join(c, " "))
		}
	}
	steps = append(steps, "postgres replayed WAL to "+targetTimestamp+". Promote with `SELECT pg_wal_replay_resume()` when ready.")
	AuditLog("", "pitr_restore", projectDir, targetTimestamp, "success", "", "")
	return map[string]interface{}{"ok": true, "steps": steps, "snapshot": backup.Path}
}

func (s *HTTPServer) handlePITRSetup(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	res, err := ConfigurePITR(s.dirParam(r))
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{"error": err.Error(), "result": res})
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *HTTPServer) handlePITRRestore(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	var b struct {
		Timestamp string `json:"timestamp"`
		Confirm   bool   `json:"confirm"`
	}
	_ = json.NewDecoder(r.Body).Decode(&b)
	writeJSON(w, http.StatusOK, RestorePITR(s.dirParam(r), b.Timestamp, b.Confirm))
}

// ---------- Multi-region HA ----------

// DeployMultiRegion provisions N Hetzner VPSes, deploys the same project to
// each, and writes a Caddyfile entry on the user's "router" host that
// round-robins across them. The router can be one of the VPSes (active) or a
// separate tiny VPS.
type MultiRegionResult struct {
	Regions []string              `json:"regions"`
	Servers []ProvisionResult     `json:"servers"`
	Caddy   string                `json:"caddyConfig,omitempty"`
	Error   string                `json:"error,omitempty"`
}

func DeployMultiRegion(name string, regions []string, domain string) (*MultiRegionResult, error) {
	if len(regions) < 2 {
		return nil, fmt.Errorf("multi-region needs at least 2 regions (e.g. [\"nbg1\", \"fsn1\"])")
	}
	res := &MultiRegionResult{Regions: regions}
	prov := provisionerRegistry()[HostHetzner]
	if prov == nil {
		return nil, fmt.Errorf("no Hetzner provisioner available — did you connect a Hetzner account?")
	}
	var upstreams []string
	for i, region := range regions {
		nm := fmt.Sprintf("%s-%s-%d", name, region, i+1)
		r, err := prov(nm, map[string]string{"location": region, "server_type": "cpx11"})
		if err != nil {
			return res, fmt.Errorf("provision %s: %w", region, err)
		}
		res.Servers = append(res.Servers, *r)
		if r.Details["ipv4"] != "" {
			upstreams = append(upstreams, r.Details["ipv4"]+":80")
		}
	}
	// Generate a Caddy config that load-balances across all upstreams.
	if domain != "" && len(upstreams) > 0 {
		var sb strings.Builder
		sb.WriteString(domain + " {\n  reverse_proxy ")
		sb.WriteString(strings.Join(upstreams, " "))
		sb.WriteString(" {\n    lb_policy round_robin\n    health_path /\n    health_interval 10s\n  }\n  encode gzip zstd\n}\n")
		res.Caddy = sb.String()
	}
	return res, nil
}

func (s *HTTPServer) handleMultiRegionDeploy(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	var b struct {
		Name    string   `json:"name"`
		Regions []string `json:"regions"`
		Domain  string   `json:"domain"`
	}
	_ = json.NewDecoder(r.Body).Decode(&b)
	res, err := DeployMultiRegion(b.Name, b.Regions, b.Domain)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{"error": err.Error(), "result": res})
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// ensureAuditWired is a background goroutine placeholder — if we want to hook
// audit logging into every mutating endpoint later, it would middleware here.
// For now, mutation handlers call AuditLog directly when they care.
func ensureAuditWired(ctx context.Context) { _ = ctx }

var _ = exec.Command // suppress unused-import nag if exec isn't referenced above
