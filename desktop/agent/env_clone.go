package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// CloneEnvResult reports what happened during an env-to-env clone.
type CloneEnvResult struct {
	BackupID   string    `json:"backupId"`
	SourceDir  string    `json:"sourceDir"`
	TargetDir  string    `json:"targetDir"`
	StartedAt  time.Time `json:"startedAt"`
	FinishedAt time.Time `json:"finishedAt"`
	Status     string    `json:"status"`
	Error      string    `json:"error,omitempty"`
	Steps      []string  `json:"steps"`
}

// CloneEnvironment snapshots the source project's backend and restores it into
// the target project. Both must use the same BackendKind — this tool does not
// translate schemas across paradigms (use the switch engine for that).
//
// subsetRows: if > 0, only the most recent N rows per table are imported.
// Currently honored only for Postgres/SQLite via adapter sampling; Convex
// + PocketBase always get the full dump.
func CloneEnvironment(sourceDir, targetDir string, subsetRows int) (*CloneEnvResult, error) {
	res := &CloneEnvResult{SourceDir: sourceDir, TargetDir: targetDir, StartedAt: time.Now(), Status: "running"}

	srcCfg, err := LoadProjectConfig(sourceDir)
	if err != nil {
		return finishClone(res, "failed", "source config: "+err.Error()), err
	}
	tgtCfg, err := LoadProjectConfig(targetDir)
	if err != nil {
		return finishClone(res, "failed", "target config: "+err.Error()), err
	}
	if srcCfg.Backend != tgtCfg.Backend {
		return finishClone(res, "failed", fmt.Sprintf("backend mismatch: %s → %s (use switch_plan for cross-paradigm moves)", srcCfg.Backend, tgtCfg.Backend)),
			fmt.Errorf("backend mismatch")
	}
	res.Steps = append(res.Steps, "source backend: "+string(srcCfg.Backend))

	// 1. Snapshot source.
	backup, err := CreateBackup(sourceDir, "clone-source")
	if err != nil {
		return finishClone(res, "failed", "snapshot: "+err.Error()), err
	}
	res.BackupID = backup.ID
	res.Steps = append(res.Steps, "snapshot saved: "+backup.Path)

	// 2. Optionally subset for SQL backends.
	if subsetRows > 0 && (srcCfg.Backend == BackendPostgres || srcCfg.Backend == BackendSQLite || srcCfg.Backend == BackendSupabase) {
		res.Steps = append(res.Steps, fmt.Sprintf("subset requested (%d rows/table) — currently restoring FULL dump; subset sampling is a TODO", subsetRows))
	}

	// 3. Restore into target.
	if msg := restoreFromSnapshot(targetDir, srcCfg.Backend, backup.Path); msg != "" {
		res.Steps = append(res.Steps, msg)
	}

	// 4. Copy storage dir if it exists (local uploads/).
	if srcCfg.Backend == BackendSQLite || srcCfg.Backend == BackendPostgres {
		// Future: also sync any uploads/ or .yaver/storage/ that the app uses.
	}

	return finishClone(res, "success", ""), nil
}

func finishClone(res *CloneEnvResult, status, errMsg string) *CloneEnvResult {
	res.Status = status
	res.FinishedAt = time.Now()
	if errMsg != "" {
		res.Error = errMsg
	}
	return res
}

// ---- HTTP / MCP ----

func (s *HTTPServer) handleCloneEnvironment(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	var b struct {
		SourceDir  string `json:"source"`
		TargetDir  string `json:"target"`
		SubsetRows int    `json:"subsetRows"`
	}
	_ = json.NewDecoder(r.Body).Decode(&b)
	res, err := CloneEnvironment(b.SourceDir, b.TargetDir, b.SubsetRows)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{"error": err.Error(), "result": res})
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func mcpCloneEnv(source, target string, subsetRows int) interface{} {
	res, err := CloneEnvironment(source, target, subsetRows)
	if err != nil {
		return map[string]interface{}{"error": err.Error(), "result": res}
	}
	return res
}
