package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

func newRestoreCmd(dsn string) *exec.Cmd { return exec.Command("psql", dsn) }

// Rollback reverses a completed switch. It restores the git snapshot branch,
// removes Yaver's env additions, and (best-effort) re-imports the data snapshot
// into the original backend. Rollback is only valid while the 7-day TTL holds.
func (e *SwitchEngine) Rollback(projectDir, switchID string) (*SwitchState, error) {
	s, err := e.Load(projectDir, switchID)
	if err != nil {
		return nil, err
	}
	if s.Status == StepFailed || s.Status == StepPending {
		// Partial switches can still be rolled back — log but don't block.
	}
	if s.RollbackExpiresAt != "" {
		if exp, err := time.Parse(time.RFC3339, s.RollbackExpiresAt); err == nil {
			if time.Now().After(exp) {
				return nil, fmt.Errorf("rollback window expired on %s; use `yaver switch cleanup` to remove stale snapshots", exp.Format(time.RFC822))
			}
		}
	}

	var lines []string

	// 1. Restore git branch.
	if s.SnapshotBranch != "" {
		if _, err := runSwitchCmd(projectDir, "git", "rev-parse", "--verify", s.SnapshotBranch); err == nil {
			if out, err := runSwitchCmd(projectDir, "git", "checkout", s.SnapshotBranch); err != nil {
				lines = append(lines, "git checkout failed: "+out)
			} else {
				lines = append(lines, "restored branch "+s.SnapshotBranch)
			}
		}
	}

	// 2. Strip Yaver's env block.
	envPath := filepath.Join(projectDir, ".env.local")
	if data, err := os.ReadFile(envPath); err == nil {
		marker := "# === yaver switch " + s.ID + " ==="
		if idx := strings.Index(string(data), marker); idx >= 0 {
			// Find the start of the marker line and the next blank separator.
			before := string(data[:idx])
			rest := string(data[idx:])
			// Drop the block: marker line + following lines until blank or EOF.
			if nl := strings.Index(rest, "\n\n"); nl >= 0 {
				rest = rest[nl+2:]
			} else {
				rest = ""
			}
			cleaned := strings.TrimRight(before, "\n") + "\n" + rest
			if err := os.WriteFile(envPath, []byte(cleaned), 0o644); err == nil {
				lines = append(lines, "stripped env block from .env.local")
			}
		}
	}

	// 3. Try re-importing the data snapshot into the original backend.
	if s.SnapshotData != "" {
		if _, err := os.Stat(s.SnapshotData); err == nil {
			if msg := restoreFromSnapshot(projectDir, s.FromBackend, s.SnapshotData); msg != "" {
				lines = append(lines, msg)
			}
		}
	}

	// 4. Mark state as rolled back (record, don't delete — history matters).
	s.Status = "rolled-back"
	rollbackEntry := SwitchStep{
		ID: "rollback", Layer: LayerData, Title: "Rollback",
		Action: "rollback", Status: StepDone,
		FinishedAt: time.Now().Format(time.RFC3339),
		Output:     strings.Join(lines, "\n"),
	}
	s.Steps = append(s.Steps, rollbackEntry)
	_ = e.Persist(s)
	return s, nil
}

func restoreFromSnapshot(projectDir string, backend BackendKind, snapshotPath string) string {
	// Auto-decrypt if the snapshot is an encrypted blob.
	if strings.HasSuffix(snapshotPath, ".enc") {
		plain, err := DecryptBackupFile(snapshotPath)
		if err != nil {
			return "decrypt failed: " + err.Error()
		}
		defer os.Remove(plain) // never leave plaintext on disk
		snapshotPath = plain
	}
	switch backend {
	case BackendPostgres, BackendSupabase:
		dsn, _ := resolveDSN(projectDir, backend)
		if dsn == "" {
			return "skipped data restore: no DSN"
		}
		f, err := os.Open(snapshotPath)
		if err != nil {
			return "open snapshot: " + err.Error()
		}
		defer f.Close()
		cmd := newRestoreCmd(dsn)
		cmd.Stdin = f
		if out, err := cmd.CombinedOutput(); err != nil {
			return "restore failed: " + err.Error() + " " + string(out)
		}
		return "restored " + snapshotPath + " → " + dsn
	case BackendSQLite:
		dsn, _ := resolveDSN(projectDir, backend)
		if dsn == "" {
			return "skipped sqlite restore: no DSN"
		}
		data, err := os.ReadFile(snapshotPath)
		if err != nil {
			return "read snapshot: " + err.Error()
		}
		if err := os.WriteFile(dsn, data, 0o644); err != nil {
			return "write sqlite: " + err.Error()
		}
		return "restored " + snapshotPath + " → " + dsn
	case BackendConvex:
		client := NewConvexAdminClient(projectDir)
		data, err := os.ReadFile(snapshotPath)
		if err != nil {
			return "read snapshot: " + err.Error()
		}
		if _, err := client.post("/api/streaming_import", map[string]interface{}{"data": string(data)}, true); err != nil {
			return "convex import: " + err.Error()
		}
		return "restored " + snapshotPath + " → local Convex"
	}
	return "no restore handler for " + string(backend)
}

// Cleanup removes switches whose rollback TTL has expired and their snapshot
// files. Returns a report.
func (e *SwitchEngine) Cleanup(projectDir string) (string, error) {
	hist, err := e.History(projectDir)
	if err != nil {
		return "", err
	}
	now := time.Now()
	var lines []string
	for _, s := range hist {
		if s.RollbackExpiresAt == "" {
			continue
		}
		exp, err := time.Parse(time.RFC3339, s.RollbackExpiresAt)
		if err != nil || exp.After(now) {
			continue
		}
		if s.SnapshotData != "" {
			_ = os.Remove(s.SnapshotData)
		}
		if s.SnapshotBranch != "" {
			_, _ = runSwitchCmd(projectDir, "git", "branch", "-D", s.SnapshotBranch)
		}
		lines = append(lines, fmt.Sprintf("cleaned %s (expired %s)", s.ID, exp.Format("2006-01-02")))
	}
	if len(lines) == 0 {
		return "nothing to clean", nil
	}
	return strings.Join(lines, "\n"), nil
}
