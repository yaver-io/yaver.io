package main

import (
	"encoding/json"
	"fmt"
	"net/http"
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

// ConfigureReplica wires up a replica container to stream from the primary.
// This is a guidance endpoint: we can't run a base_backup inside a container we
// don't control, so we return the exact docker exec commands the user runs on
// the replica machine.
func ConfigureReplica(primaryHost string, primaryPort int, replicationUser, replicationPassword, slotName string) map[string]interface{} {
	if primaryPort == 0 {
		primaryPort = 5432
	}
	if slotName == "" {
		slotName = "yaver_slot"
	}
	conn := fmt.Sprintf("host=%s port=%d user=%s password=%s", primaryHost, primaryPort, replicationUser, replicationPassword)

	return map[string]interface{}{
		"primaryConnInfo": conn,
		"steps": []string{
			"On the replica machine, stop postgres if running: docker stop <replica-container>",
			"Wipe its data volume: docker volume rm yaver-pg-replica-data",
			"Start an empty replica container and exec in. Then:",
			"  pg_basebackup -h " + primaryHost + " -p " + fmt.Sprint(primaryPort) +
				" -U " + replicationUser + " -D /var/lib/postgresql/data -R -S " + slotName + " -X stream",
			"  (Prompts for password: " + replicationPassword + ")",
			"pg_basebackup -R writes standby.signal + postgresql.auto.conf with primary_conninfo.",
			"Start the replica — it attaches to slot '" + slotName + "' and streams.",
			"",
			"Verify: SELECT client_addr, state, sync_state FROM pg_stat_replication;  (on primary)",
		},
		"notes": "Yaver returns the commands instead of auto-running because base_backup is destructive to the replica volume. Run explicitly once to avoid footguns.",
	}
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
		res := ConfigureReplica(b.PrimaryHost, b.PrimaryPort, b.ReplicationUser, b.ReplicationPassword, b.SlotName)
		writeJSON(w, http.StatusOK, res)
	default:
		jsonError(w, http.StatusBadRequest, "role must be 'primary' or 'replica'")
	}
}
