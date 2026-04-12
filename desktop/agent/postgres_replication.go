package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// ReplicationConfig describes a primary→replica streaming replication setup.
type ReplicationConfig struct {
	PrimaryHost     string `json:"primaryHost"`
	PrimaryPort     int    `json:"primaryPort"`
	ReplicaHost     string `json:"replicaHost"`
	ReplicaPort     int    `json:"replicaPort"`
	ReplicationUser string `json:"replicationUser"`
	SlotName        string `json:"slotName"`
	ApplicationName string `json:"applicationName"`
}

// ConfigurePrimary runs the ALTER SYSTEM statements needed on the primary so
// replicas can stream from it. The user must docker-restart postgres for these
// to take effect (WAL config changes need a restart, not just reload).
//
// Follow-up: ConfigureReplica (below) does the base_backup + recovery dance
// inside the replica container.
func ConfigurePrimary(projectDir string, slotName, replicationUser, replicationPassword string) (map[string]interface{}, error) {
	if slotName == "" {
		slotName = "yaver_slot"
	}
	if replicationUser == "" {
		replicationUser = "replicator"
	}
	cfg, err := LoadProjectConfig(projectDir)
	if err != nil {
		return nil, err
	}
	if cfg.Backend != BackendPostgres && cfg.Backend != BackendSupabase {
		return nil, fmt.Errorf("replication only supported on Postgres/Supabase backends")
	}
	a, err := newSQLAdapter(projectDir, cfg, BackendPostgres)
	if err != nil {
		return nil, err
	}
	if err := a.open(); err != nil {
		return nil, err
	}

	stmts := []string{
		`ALTER SYSTEM SET wal_level = 'replica'`,
		`ALTER SYSTEM SET max_wal_senders = 10`,
		`ALTER SYSTEM SET max_replication_slots = 10`,
		`ALTER SYSTEM SET wal_keep_size = '1GB'`,
		`ALTER SYSTEM SET hot_standby = 'on'`,
	}
	for _, s := range stmts {
		if _, err := a.db.Exec(s); err != nil {
			return map[string]interface{}{"error": err.Error(), "stmt": s}, err
		}
	}

	// Create a replication role and a physical replication slot. Idempotent —
	// both CREATE ROLE and pg_create_physical_replication_slot errors on
	// already-exists are non-fatal.
	pw := replicationPassword
	if pw == "" {
		pw = generatePassword()
	}
	createRole := fmt.Sprintf(`DO $$ BEGIN
		CREATE ROLE %s WITH REPLICATION LOGIN PASSWORD '%s';
	EXCEPTION WHEN duplicate_object THEN
		ALTER ROLE %s WITH PASSWORD '%s';
	END $$`, replicationUser, pw, replicationUser, pw)
	if _, err := a.db.Exec(createRole); err != nil {
		return map[string]interface{}{"error": err.Error(), "stmt": "CREATE ROLE"}, err
	}
	slotQ := fmt.Sprintf(`SELECT pg_create_physical_replication_slot('%s')`, slotName)
	// Ignore already-exists (PG code 42710 / similar) — probe and continue.
	_, _ = a.db.Exec(slotQ)

	// pg_hba entry — the user must add `host replication <user> 0.0.0.0/0 md5`
	// to pg_hba.conf inside the container. We can't edit that file directly
	// without docker exec. Return the command to run.
	return map[string]interface{}{
		"ok":              true,
		"slotName":        slotName,
		"replicationUser": replicationUser,
		"replicationPass": pw,
		"nextSteps": []string{
			"Add to pg_hba.conf (inside the postgres container):",
			"  host replication " + replicationUser + " 0.0.0.0/0 md5",
			"Easiest: docker exec yaver-services-postgres-1 sh -c \"echo 'host replication " + replicationUser + " 0.0.0.0/0 md5' >> /var/lib/postgresql/data/pg_hba.conf && pg_ctl reload\"",
			"Then restart the container: docker restart yaver-services-postgres-1",
			"",
			"Now call ConfigureReplica on the secondary machine to wire the replica.",
		},
	}, nil
}

// ConfigureReplica sets up the replica. Two modes:
//
//   - confirm=false: guidance only (safe default)
//   - confirm=true: actually wipes the replica volume and runs pg_basebackup
//     to stream a fresh copy, then starts the replica attached to the slot.
//
// The replica must already be a configured service in services.yaml (preset
// "postgres-replica"). If missing, we add it.
func ConfigureReplica(projectDir, primaryHost string, primaryPort int, replicationUser, replicationPassword, slotName string, confirm bool) map[string]interface{} {
	if primaryPort == 0 {
		primaryPort = 5432
	}
	if slotName == "" {
		slotName = "yaver_slot"
	}

	if !confirm {
		return map[string]interface{}{
			"primaryConnInfo": fmt.Sprintf("host=%s port=%d user=%s password=%s", primaryHost, primaryPort, replicationUser, replicationPassword),
			"steps": []string{
				"Preview mode (pass confirm=true to run):",
				"1. Ensure postgres-replica service is in services.yaml",
				"2. Stop replica (if running) + wipe its volume",
				"3. pg_basebackup -h " + primaryHost + " -p " + fmt.Sprint(primaryPort) +
					" -U " + replicationUser + " -D /var/lib/postgresql/data -R -S " + slotName + " -X stream",
				"4. Start replica — it streams from the primary's slot",
			},
		}
	}

	// 1. Ensure the preset exists.
	sm := NewServicesManager(projectDir)
	if _, err := sm.Add("postgres-replica", nil); err != nil {
		// Not fatal — maybe already added.
	}

	// 2. Stop + wipe the replica.
	stopCmds := [][]string{
		{"compose", "-p", "yaver-services", "-f", sm.composePath(), "stop", "postgres-replica"},
		{"volume", "rm", "-f", "yaver-pg-replica-data"},
	}
	var steps []string
	for _, c := range stopCmds {
		out, err := sm.runDocker(c...)
		steps = append(steps, fmt.Sprintf("%s → %s", strings.Join(c, " "), strings.TrimSpace(out)))
		_ = err // best-effort
	}

	// 3. Run pg_basebackup in a fresh postgres container, writing into the volume.
	runBackup := []string{"run", "--rm",
		"-v", "yaver-pg-replica-data:/var/lib/postgresql/data",
		"-e", "PGPASSWORD=" + replicationPassword,
		"--entrypoint", "sh",
		"postgres:16",
		"-c", fmt.Sprintf("rm -rf /var/lib/postgresql/data/* && pg_basebackup -h %s -p %d -U %s -D /var/lib/postgresql/data -R -S %s -X stream -Fp",
			primaryHost, primaryPort, replicationUser, slotName),
	}
	if out, err := sm.runDocker(runBackup...); err != nil {
		return map[string]interface{}{"error": "pg_basebackup failed: " + err.Error(), "output": out, "steps": steps}
	} else {
		steps = append(steps, "pg_basebackup complete")
	}

	// 4. Start the replica.
	if msg, err := sm.Start("postgres-replica"); err != nil {
		return map[string]interface{}{"error": "start replica: " + err.Error(), "output": msg, "steps": steps}
	} else {
		steps = append(steps, "replica started: "+msg)
	}
	AuditLog("", "replica_setup", projectDir, primaryHost, "success", "", "")
	return map[string]interface{}{"ok": true, "steps": steps}
}

// ---- HTTP ----

func (s *HTTPServer) handleReplicaSetup(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	var b struct {
		Role                 string `json:"role"` // "primary" or "replica"
		SlotName             string `json:"slotName"`
		ReplicationUser      string `json:"replicationUser"`
		ReplicationPassword  string `json:"replicationPassword"`
		PrimaryHost          string `json:"primaryHost"`
		PrimaryPort          int    `json:"primaryPort"`
		Confirm              bool   `json:"confirm"`
	}
	_ = json.NewDecoder(r.Body).Decode(&b)
	switch b.Role {
	case "primary":
		res, err := ConfigurePrimary(s.dirParam(r), b.SlotName, b.ReplicationUser, b.ReplicationPassword)
		if err != nil {
			writeJSON(w, http.StatusOK, map[string]interface{}{"error": err.Error(), "result": res})
			return
		}
		writeJSON(w, http.StatusOK, res)
	case "replica":
		res := ConfigureReplica(s.dirParam(r), b.PrimaryHost, b.PrimaryPort, b.ReplicationUser, b.ReplicationPassword, b.SlotName, b.Confirm)
		writeJSON(w, http.StatusOK, res)
	default:
		jsonError(w, http.StatusBadRequest, "role must be 'primary' or 'replica'")
	}
}
