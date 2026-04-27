package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// ProjectBackup describes a single snapshot on disk.
type ProjectBackup struct {
	ID         string    `json:"id"`
	ProjectDir string    `json:"projectDir"`
	Backend    string    `json:"backend"`
	Path       string    `json:"path"`
	Size       int64     `json:"size"`
	CreatedAt  time.Time `json:"createdAt"`
	Kind       string    `json:"kind"` // auto, manual
	Error      string    `json:"error,omitempty"`
	Remote     string    `json:"remote,omitempty"` // r2://, s3://, etc. if synced
}

func backupsDirFor(projectDir string) string {
	return filepath.Join(projectDir, ".yaver", "backups")
}

// CreateBackup dispatches to the correct dump per-backend and writes into
// .yaver/backups. Returns a record (or an error record if it failed).
func CreateBackup(projectDir, kind string) (*ProjectBackup, error) {
	if kind == "" {
		kind = "manual"
	}
	cfg, err := LoadProjectConfig(projectDir)
	if err != nil {
		return nil, err
	}
	dir := backupsDirFor(projectDir)
	_ = os.MkdirAll(dir, 0o755)
	id := fmt.Sprintf("bk_%s", time.Now().UTC().Format("20060102_150405"))
	rec := &ProjectBackup{
		ID: id, ProjectDir: projectDir, Backend: string(cfg.Backend),
		CreatedAt: time.Now(), Kind: kind,
	}

	var path string
	var dumpErr error
	switch cfg.Backend {
	case BackendPostgres, BackendSupabase:
		path, dumpErr = dumpPostgres(projectDir, dir, id)
	case BackendSQLite:
		path, dumpErr = dumpSQLite(projectDir, dir, id)
	case BackendConvex:
		path, dumpErr = dumpConvex(projectDir, dir, id)
	default:
		dumpErr = fmt.Errorf("no backup strategy for backend %q", cfg.Backend)
	}
	if dumpErr != nil {
		rec.Error = dumpErr.Error()
	}
	// Encrypt at rest if enabled for this project.
	if path != "" && dumpErr == nil && IsBackupEncryptionEnabled(projectDir) {
		if encPath, encErr := EncryptBackupFile(path); encErr == nil {
			path = encPath
		}
	}
	rec.Path = path
	if info, err := os.Stat(path); err == nil {
		rec.Size = info.Size()
	}
	persistBackupRecord(rec)
	return rec, dumpErr
}

// ListBackups returns all backup records for a project, newest first.
func ListBackups(projectDir string) []*ProjectBackup {
	dir := backupsDirFor(projectDir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []*ProjectBackup
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		var rec ProjectBackup
		if json.Unmarshal(data, &rec) == nil {
			out = append(out, &rec)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out
}

// RestoreBackup restores a given backup into the same project backend.
func RestoreBackup(projectDir, backupID string) (map[string]interface{}, error) {
	var target *ProjectBackup
	for _, r := range ListBackups(projectDir) {
		if r.ID == backupID {
			target = r
			break
		}
	}
	if target == nil {
		return nil, fmt.Errorf("backup %s not found", backupID)
	}
	if _, err := os.Stat(target.Path); err != nil {
		return nil, fmt.Errorf("backup file missing: %s", target.Path)
	}
	msg := restoreFromSnapshot(projectDir, BackendKind(target.Backend), target.Path)
	return map[string]interface{}{"restored": target.ID, "message": msg}, nil
}

// DeleteBackup removes a backup file + its metadata.
func DeleteBackup(projectDir, backupID string) error {
	var target *ProjectBackup
	for _, r := range ListBackups(projectDir) {
		if r.ID == backupID {
			target = r
			break
		}
	}
	if target == nil {
		return fmt.Errorf("backup %s not found", backupID)
	}
	_ = os.Remove(target.Path)
	_ = os.Remove(filepath.Join(backupsDirFor(projectDir), backupID+".json"))
	return nil
}

func persistBackupRecord(rec *ProjectBackup) {
	data, _ := json.MarshalIndent(rec, "", "  ")
	_ = os.WriteFile(filepath.Join(backupsDirFor(rec.ProjectDir), rec.ID+".json"), data, 0o644)
}

// SyncBackupToRemote ships a backup file to an S3-compatible endpoint using
// the standard `aws s3 cp` CLI (works with AWS, Cloudflare R2, Backblaze B2,
// MinIO). Returns the remote URL on success.
func SyncBackupToRemote(projectDir, backupID, remote string) (string, error) {
	var target *ProjectBackup
	for _, r := range ListBackups(projectDir) {
		if r.ID == backupID {
			target = r
			break
		}
	}
	if target == nil {
		return "", fmt.Errorf("backup %s not found", backupID)
	}
	if !strings.HasPrefix(remote, "s3://") && !strings.HasPrefix(remote, "r2://") && !strings.HasPrefix(remote, "b2://") {
		return "", fmt.Errorf("remote must be s3://bucket/key (or r2://, b2://)")
	}
	dest := remote + "/" + filepath.Base(target.Path)
	dest = strings.Replace(dest, "r2://", "s3://", 1)
	dest = strings.Replace(dest, "b2://", "s3://", 1)
	cmd := exec.Command("aws", "s3", "cp", target.Path, dest)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("aws s3 cp: %w", err)
	}
	target.Remote = dest
	persistBackupRecord(target)
	return dest, nil
}

// ---- Autobackup scheduler ----
// StartAutoBackup runs a daily backup loop for a project directory.
var autoBackupCancels sync.Map // projectDir -> chan struct{}

func StartAutoBackup(projectDir string, everyHours int) {
	if everyHours <= 0 {
		everyHours = 24
	}
	StopAutoBackup(projectDir)
	ch := make(chan struct{})
	autoBackupCancels.Store(projectDir, ch)
	go func() {
		t := time.NewTicker(time.Duration(everyHours) * time.Hour)
		defer t.Stop()
		for {
			select {
			case <-ch:
				return
			case <-t.C:
				_, _ = CreateBackup(projectDir, "auto")
			}
		}
	}()
}

func StopAutoBackup(projectDir string) {
	if v, ok := autoBackupCancels.LoadAndDelete(projectDir); ok {
		if ch, ok := v.(chan struct{}); ok {
			close(ch)
		}
	}
}

// ---- HTTP / MCP ----

func (s *HTTPServer) handleBackupCreate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	rec, err := CreateBackup(s.dirParam(r), "manual")
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{"error": err.Error(), "record": rec})
		return
	}
	writeJSON(w, http.StatusOK, rec)
}

func (s *HTTPServer) handleBackupList(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]interface{}{"backups": ListBackups(s.dirParam(r))})
}

func (s *HTTPServer) handleBackupRestore(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	var b struct{ ID string `json:"id"` }
	_ = json.NewDecoder(r.Body).Decode(&b)
	res, err := RestoreBackup(s.dirParam(r), b.ID)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *HTTPServer) handleBackupDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	var b struct{ ID string `json:"id"` }
	_ = json.NewDecoder(r.Body).Decode(&b)
	if err := DeleteBackup(s.dirParam(r), b.ID); err != nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true})
}

func (s *HTTPServer) handleBackupSync(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	var b struct {
		ID     string `json:"id"`
		Remote string `json:"remote"`
	}
	_ = json.NewDecoder(r.Body).Decode(&b)
	url, err := SyncBackupToRemote(s.dirParam(r), b.ID, b.Remote)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{"error": err.Error(), "output": url})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true, "url": url})
}

func (s *HTTPServer) handleBackupAuto(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	var b struct {
		Enabled    bool `json:"enabled"`
		EveryHours int  `json:"everyHours"`
	}
	_ = json.NewDecoder(r.Body).Decode(&b)
	dir := s.dirParam(r)
	if b.Enabled {
		StartAutoBackup(dir, b.EveryHours)
	} else {
		StopAutoBackup(dir)
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true, "enabled": b.Enabled})
}
