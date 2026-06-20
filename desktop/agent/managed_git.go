package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type ManagedGitCreateOptions struct {
	Enabled    bool   `json:"enabled" yaml:"enabled"`
	Visibility string `json:"visibility,omitempty" yaml:"visibility,omitempty"` // private|unlisted|public
}

type ManagedGitProjectMeta struct {
	RepoID          string                         `json:"repoId" yaml:"repoId"`
	Enabled         bool                           `json:"enabled" yaml:"enabled"`
	Visibility      string                         `json:"visibility" yaml:"visibility"`
	DefaultBranch   string                         `json:"defaultBranch" yaml:"defaultBranch"`
	BarePath        string                         `json:"barePath" yaml:"barePath"`
	WorkDir         string                         `json:"workDir" yaml:"workDir"`
	LastCommit      string                         `json:"lastCommit,omitempty" yaml:"lastCommit,omitempty"`
	LastBackup      *ManagedGitBackupMeta          `json:"lastBackup,omitempty" yaml:"lastBackup,omitempty"`
	ExternalBackups []ManagedGitExternalBackupMeta `json:"externalBackups,omitempty" yaml:"externalBackups,omitempty"`
	Mirrors         []ManagedGitMirrorMeta         `json:"mirrors,omitempty" yaml:"mirrors,omitempty"`
	CreatedAt       string                         `json:"createdAt" yaml:"createdAt"`
	UpdatedAt       string                         `json:"updatedAt" yaml:"updatedAt"`
}

type ManagedGitBackupMeta struct {
	Path      string `json:"path" yaml:"path"`
	Target    string `json:"target,omitempty" yaml:"target,omitempty"`
	SizeBytes int64  `json:"sizeBytes" yaml:"sizeBytes"`
	Commit    string `json:"commit,omitempty" yaml:"commit,omitempty"`
	CreatedAt string `json:"createdAt" yaml:"createdAt"`
}

type ManagedGitExternalBackupMeta struct {
	TargetKind string `json:"targetKind" yaml:"targetKind"`
	TargetID   string `json:"targetId,omitempty" yaml:"targetId,omitempty"`
	Path       string `json:"path" yaml:"path"`
	SizeBytes  int64  `json:"sizeBytes" yaml:"sizeBytes"`
	Commit     string `json:"commit,omitempty" yaml:"commit,omitempty"`
	CreatedAt  string `json:"createdAt" yaml:"createdAt"`
}

type ManagedGitMirrorMeta struct {
	Provider   string `json:"provider" yaml:"provider"`
	Host       string `json:"host" yaml:"host"`
	FullName   string `json:"fullName" yaml:"fullName"`
	CloneURL   string `json:"cloneUrl" yaml:"cloneUrl"`
	Visibility string `json:"visibility" yaml:"visibility"`
	LastPushAt string `json:"lastPushAt,omitempty" yaml:"lastPushAt,omitempty"`
}

func managedGitRoot() (string, error) {
	cfg, err := ConfigDir()
	if err != nil {
		return "", err
	}
	root := filepath.Join(cfg, "managed-git")
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", err
	}
	return root, nil
}

func managedGitReposRoot() (string, error) {
	root, err := managedGitRoot()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(root, "repos")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return dir, nil
}

func managedGitBackupsRoot() (string, error) {
	root, err := managedGitRoot()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(root, "backups")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return dir, nil
}

func managedGitMetaPath(workDir string) string {
	return filepath.Join(workDir, ".yaver", "managed-git.yaml")
}

func normalizeManagedGitVisibility(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "public":
		return "public"
	case "unlisted":
		return "unlisted"
	default:
		return "private"
	}
}

func EnsureManagedGitForProject(workDir, slug, name string, opts *ManagedGitCreateOptions) (*ManagedGitProjectMeta, error) {
	if opts == nil || !opts.Enabled {
		return nil, nil
	}
	if _, err := osexec.LookPath("git"); err != nil {
		return nil, fmt.Errorf("git not found on PATH")
	}
	if existing, err := LoadManagedGitMeta(workDir); err == nil && existing.Enabled {
		return existing, nil
	}
	if err := os.MkdirAll(filepath.Join(workDir, ".yaver"), 0o700); err != nil {
		return nil, err
	}
	reposRoot, err := managedGitReposRoot()
	if err != nil {
		return nil, err
	}
	repoID := Slugify(slug)
	if repoID == "" {
		repoID = Slugify(name)
	}
	if repoID == "" {
		return nil, fmt.Errorf("repo id required")
	}
	barePath := filepath.Join(reposRoot, repoID+".git")
	if _, err := os.Stat(barePath); os.IsNotExist(err) {
		if out, err := managedGitCmd("", "init", "--bare", barePath); err != nil {
			return nil, fmt.Errorf("init bare: %s: %w", out, err)
		}
	}
	if _, err := os.Stat(filepath.Join(workDir, ".git")); os.IsNotExist(err) {
		if out, err := managedGitCmd(workDir, "init", "-b", "main"); err != nil {
			if out2, err2 := managedGitCmd(workDir, "init"); err2 != nil {
				return nil, fmt.Errorf("init worktree: %s / %s: %w", out, out2, err2)
			}
			_, _ = managedGitCmd(workDir, "checkout", "-B", "main")
		}
	}
	_, _ = managedGitCmd(workDir, "remote", "remove", "origin")
	if out, err := managedGitCmd(workDir, "remote", "add", "origin", barePath); err != nil {
		return nil, fmt.Errorf("add managed remote: %s: %w", out, err)
	}
	meta := &ManagedGitProjectMeta{
		RepoID:        repoID,
		Enabled:       true,
		Visibility:    normalizeManagedGitVisibility(opts.Visibility),
		DefaultBranch: "main",
		BarePath:      barePath,
		WorkDir:       workDir,
		CreatedAt:     time.Now().UTC().Format(time.RFC3339),
		UpdatedAt:     time.Now().UTC().Format(time.RFC3339),
	}
	if err := SaveManagedGitMeta(workDir, meta); err != nil {
		return nil, err
	}
	commit, err := ManagedGitCheckpoint(workDir, "yaver: create managed repo")
	if err != nil {
		return nil, err
	}
	meta.LastCommit = commit
	if err := SaveManagedGitMeta(workDir, meta); err != nil {
		return nil, err
	}
	return meta, nil
}

func LoadManagedGitMeta(workDir string) (*ManagedGitProjectMeta, error) {
	data, err := os.ReadFile(managedGitMetaPath(workDir))
	if err != nil {
		return nil, err
	}
	var meta ManagedGitProjectMeta
	if err := yaml.Unmarshal(data, &meta); err != nil {
		return nil, err
	}
	if meta.WorkDir == "" {
		meta.WorkDir = workDir
	}
	return &meta, nil
}

func SaveManagedGitMeta(workDir string, meta *ManagedGitProjectMeta) error {
	meta.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	data, err := yaml.Marshal(meta)
	if err != nil {
		return err
	}
	return os.WriteFile(managedGitMetaPath(workDir), data, 0o600)
}

func ManagedGitCheckpoint(workDir, message string) (string, error) {
	if strings.TrimSpace(message) == "" {
		message = "yaver: checkpoint"
	}
	if out, err := managedGitCmd(workDir, "add", "-A"); err != nil {
		return "", fmt.Errorf("git add: %s: %w", out, err)
	}
	status, err := managedGitCmd(workDir, "status", "--porcelain")
	if err != nil {
		return "", fmt.Errorf("git status: %s: %w", status, err)
	}
	if strings.TrimSpace(status) != "" {
		_, _ = managedGitCmd(workDir, "config", "user.name", "Yaver Agent")
		_, _ = managedGitCmd(workDir, "config", "user.email", "agent@yaver.io")
		if out, err := managedGitCmd(workDir, "commit", "-m", message); err != nil {
			return "", fmt.Errorf("git commit: %s: %w", out, err)
		}
	}
	if out, err := managedGitCmd(workDir, "push", "-u", "origin", "HEAD:main"); err != nil {
		return "", fmt.Errorf("git push managed origin: %s: %w", out, err)
	}
	commit, err := managedGitCmd(workDir, "rev-parse", "HEAD")
	if err != nil {
		return "", fmt.Errorf("git rev-parse: %s: %w", commit, err)
	}
	commit = strings.TrimSpace(commit)
	if meta, err := LoadManagedGitMeta(workDir); err == nil {
		meta.LastCommit = commit
		_ = SaveManagedGitMeta(workDir, meta)
	}
	return commit, nil
}

func ManagedGitBackup(workDir string) (*ManagedGitBackupMeta, error) {
	meta, err := LoadManagedGitMeta(workDir)
	if err != nil {
		return nil, err
	}
	if _, err := ManagedGitCheckpoint(workDir, "yaver: backup checkpoint"); err != nil {
		return nil, err
	}
	root, err := managedGitBackupsRoot()
	if err != nil {
		return nil, err
	}
	repoDir := filepath.Join(root, meta.RepoID)
	if err := os.MkdirAll(repoDir, 0o700); err != nil {
		return nil, err
	}
	stamp := time.Now().UTC().Format("20060102T150405Z")
	path := filepath.Join(repoDir, stamp+".bundle")
	if out, err := managedGitCmd(workDir, "bundle", "create", path, "--all"); err != nil {
		return nil, fmt.Errorf("git bundle: %s: %w", out, err)
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	commit, _ := managedGitCmd(workDir, "rev-parse", "HEAD")
	backup := &ManagedGitBackupMeta{
		Path:      path,
		SizeBytes: info.Size(),
		Commit:    strings.TrimSpace(commit),
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}
	meta.LastBackup = backup
	if err := SaveManagedGitMeta(workDir, meta); err != nil {
		return nil, err
	}
	return backup, nil
}

func ManagedGitBackupToTarget(workDir, targetKind, targetID, destPath string) (*ManagedGitExternalBackupMeta, error) {
	backup, err := ManagedGitBackup(workDir)
	if err != nil {
		return nil, err
	}
	targetKind = strings.ToLower(strings.TrimSpace(targetKind))
	if targetKind == "" {
		targetKind = "local-folder"
	}
	var root string
	switch targetKind {
	case "dropbox":
		meta, err := LoadManagedGitMeta(workDir)
		if err != nil {
			return nil, err
		}
		out, err := uploadManagedGitBackupToDropbox(backup.Path, meta.RepoID)
		if err != nil {
			return nil, err
		}
		out.Commit = backup.Commit
		meta.ExternalBackups = append(meta.ExternalBackups, *out)
		_ = SaveManagedGitMeta(workDir, meta)
		return out, nil
	case "local-folder":
		root = strings.TrimSpace(destPath)
		if root == "" {
			return nil, fmt.Errorf("destPath required for local-folder backup")
		}
	case "shared-storage":
		if strings.TrimSpace(targetID) == "" {
			return nil, fmt.Errorf("targetId required for shared-storage backup")
		}
		profile, err := getSharedStorageProfile(targetID)
		if err != nil {
			return nil, err
		}
		if profile.ReadOnly {
			return nil, fmt.Errorf("shared storage profile is read-only")
		}
		switch profile.Type {
		case "local", "storagebox":
			root = sharedStorageResolvedPath(*profile)
		default:
			return nil, fmt.Errorf("shared storage type %q is not writable by managed git yet", profile.Type)
		}
		if strings.TrimSpace(destPath) != "" {
			root = filepath.Join(root, filepath.Clean(destPath))
		}
	default:
		return nil, fmt.Errorf("unsupported backup target %q", targetKind)
	}
	if root == "" {
		return nil, fmt.Errorf("backup target path resolved empty")
	}
	meta, err := LoadManagedGitMeta(workDir)
	if err != nil {
		return nil, err
	}
	destDir := filepath.Join(root, "YaverBackups", meta.RepoID)
	if err := os.MkdirAll(destDir, 0o700); err != nil {
		return nil, err
	}
	dest := filepath.Join(destDir, filepath.Base(backup.Path))
	if err := managedGitCopyFile(backup.Path, dest, 0o600); err != nil {
		return nil, err
	}
	latest := filepath.Join(destDir, "latest.bundle")
	_ = managedGitCopyFile(backup.Path, latest, 0o600)
	info, err := os.Stat(dest)
	if err != nil {
		return nil, err
	}
	out := &ManagedGitExternalBackupMeta{
		TargetKind: targetKind,
		TargetID:   targetID,
		Path:       dest,
		SizeBytes:  info.Size(),
		Commit:     backup.Commit,
		CreatedAt:  time.Now().UTC().Format(time.RFC3339),
	}
	meta.ExternalBackups = append(meta.ExternalBackups, *out)
	_ = SaveManagedGitMeta(workDir, meta)
	return out, nil
}

func managedGitCopyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := dst + ".tmp"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, dst)
}

func ManagedGitRestoreBundle(workDir, bundlePath string) (string, error) {
	bundlePath = strings.TrimSpace(bundlePath)
	if bundlePath == "" {
		return "", fmt.Errorf("bundlePath required")
	}
	if _, err := os.Stat(bundlePath); err != nil {
		return "", err
	}
	meta, err := LoadManagedGitMeta(workDir)
	if err != nil {
		return "", err
	}
	branch := meta.DefaultBranch
	if branch == "" {
		branch = "main"
	}
	backupRef := "refs/heads/yaver-restore-" + time.Now().UTC().Format("20060102T150405Z")
	if out, err := managedGitCmd(workDir, "fetch", bundlePath, "refs/heads/"+branch+":"+backupRef); err != nil {
		return "", fmt.Errorf("fetch bundle: %s: %w", out, err)
	}
	if out, err := managedGitCmd(workDir, "checkout", "-B", branch, backupRef); err != nil {
		return "", fmt.Errorf("checkout restored bundle: %s: %w", out, err)
	}
	if out, err := managedGitCmd(workDir, "reset", "--hard", "HEAD"); err != nil {
		return "", fmt.Errorf("reset restored bundle: %s: %w", out, err)
	}
	if out, err := managedGitCmd(workDir, "push", "-u", "origin", "HEAD:"+branch); err != nil {
		return "", fmt.Errorf("push restored state: %s: %w", out, err)
	}
	commit, err := managedGitCmd(workDir, "rev-parse", "HEAD")
	if err != nil {
		return "", fmt.Errorf("rev-parse restored state: %s: %w", commit, err)
	}
	meta.LastCommit = strings.TrimSpace(commit)
	_ = SaveManagedGitMeta(workDir, meta)
	return meta.LastCommit, nil
}

func ManagedGitMirrorToProvider(workDir, provider, host, repoName, visibility, description string) (*ManagedGitMirrorMeta, error) {
	meta, err := LoadManagedGitMeta(workDir)
	if err != nil {
		return nil, err
	}
	provider = strings.ToLower(strings.TrimSpace(provider))
	if provider != "github" && provider != "gitlab" {
		return nil, fmt.Errorf("provider must be github or gitlab")
	}
	if host == "" {
		host = provider + ".com"
	}
	if repoName == "" {
		repoName = meta.RepoID
	}
	providers, err := loadGitProviders()
	if err != nil {
		return nil, err
	}
	var token, username string
	for _, p := range providers {
		if p.Provider == provider && p.Host == host {
			token = p.Token
			username = p.Username
			break
		}
	}
	if token == "" {
		return nil, fmt.Errorf("no %s token configured on this machine", provider)
	}
	private := normalizeManagedGitVisibility(visibility) != "public"
	var cloneURL, fullName string
	var sshURL string
	switch provider {
	case "github":
		cloneURL, sshURL, fullName, err = createRepoOnGitHub(token, repoName, description, private)
	case "gitlab":
		cloneURL, sshURL, fullName, err = createRepoOnGitLab(host, token, repoName, description, private)
	}
	if err != nil && !strings.Contains(strings.ToLower(err.Error()), "already_exists") && !strings.Contains(strings.ToLower(err.Error()), "name already exists") {
		return nil, err
	}
	if fullName == "" {
		fullName = repoName
	}
	pushURL := cloneURL
	if pushURL == "" {
		switch provider {
		case "github":
			pushURL = fmt.Sprintf("https://%s/%s.git", host, fullName)
		case "gitlab":
			pushURL = fmt.Sprintf("https://%s/%s.git", host, fullName)
		}
	}
	credentialed := credentialedGitURL(provider, host, username, token, fullName)
	if credentialed == "" {
		credentialed = pushURL
	}
	if out, err := managedGitCmd(workDir, "push", credentialed, "HEAD:main"); err != nil {
		safeOut := strings.ReplaceAll(out, token, "<redacted>")
		return nil, fmt.Errorf("mirror push: %s: %w", safeOut, err)
	}
	mirror := ManagedGitMirrorMeta{
		Provider:   provider,
		Host:       host,
		FullName:   fullName,
		CloneURL:   pushURL,
		Visibility: normalizeManagedGitVisibility(visibility),
		LastPushAt: time.Now().UTC().Format(time.RFC3339),
	}
	_ = sshURL
	meta.Mirrors = upsertManagedGitMirror(meta.Mirrors, mirror)
	if err := SaveManagedGitMeta(workDir, meta); err != nil {
		return nil, err
	}
	return &mirror, nil
}

func upsertManagedGitMirror(items []ManagedGitMirrorMeta, next ManagedGitMirrorMeta) []ManagedGitMirrorMeta {
	for i := range items {
		if items[i].Provider == next.Provider && items[i].Host == next.Host && items[i].FullName == next.FullName {
			items[i] = next
			return items
		}
	}
	return append(items, next)
}

func credentialedGitURL(provider, host, username, token, fullName string) string {
	switch provider {
	case "github":
		if username == "" {
			username = "x-access-token"
		}
		return fmt.Sprintf("https://%s:%s@%s/%s.git", username, token, host, fullName)
	case "gitlab":
		return fmt.Sprintf("https://oauth2:%s@%s/%s.git", token, host, fullName)
	}
	return ""
}

func managedGitCmd(dir string, args ...string) (string, error) {
	cmd := osexec.Command("git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func (s *HTTPServer) registerManagedGitRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/managed-git/enable", s.auth(s.handleManagedGitEnable))
	mux.HandleFunc("/managed-git/status", s.auth(s.handleManagedGitStatus))
	mux.HandleFunc("/managed-git/checkpoint", s.auth(s.handleManagedGitCheckpoint))
	mux.HandleFunc("/managed-git/backup/run", s.auth(s.handleManagedGitBackupRun))
	mux.HandleFunc("/managed-git/backup/copy", s.auth(s.handleManagedGitBackupCopy))
	mux.HandleFunc("/managed-git/backup/download", s.auth(s.handleManagedGitBackupDownload))
	mux.HandleFunc("/managed-git/backup/receive", s.auth(s.handleManagedGitBackupReceive))
	mux.HandleFunc("/managed-git/backup/restore", s.auth(s.handleManagedGitBackupRestore))
	mux.HandleFunc("/managed-git/mirrors/connect", s.auth(s.handleManagedGitMirrorConnect))
	mux.HandleFunc("/managed-git/visibility", s.auth(s.handleManagedGitVisibility))
	mux.HandleFunc("/managed-git/dropbox/oauth/start", s.auth(s.handleManagedGitDropboxOAuthStart))
	mux.HandleFunc("/managed-git/dropbox/oauth/submit", s.auth(s.handleManagedGitDropboxOAuthSubmit))
	mux.HandleFunc("/managed-git/dropbox/status", s.auth(s.handleManagedGitDropboxStatus))
}

func (s *HTTPServer) handleManagedGitEnable(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	var body struct {
		Slug       string `json:"slug"`
		WorkDir    string `json:"workDir"`
		Name       string `json:"name"`
		Visibility string `json:"visibility"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil && err != io.EOF {
		jsonError(w, http.StatusBadRequest, "invalid json: "+err.Error())
		return
	}
	workDir, err := managedGitWorkDir(body.Slug, body.WorkDir)
	if err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	slug := body.Slug
	if slug == "" {
		slug = filepath.Base(workDir)
	}
	meta, err := EnsureManagedGitForProject(workDir, slug, body.Name, &ManagedGitCreateOptions{
		Enabled:    true,
		Visibility: body.Visibility,
	})
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if p, err := LoadPhoneProject(slug); err == nil {
		p.ManagedGit = meta
		_ = savePhoneMeta(p)
	}
	jsonReply(w, http.StatusOK, meta)
}

func (s *HTTPServer) handleManagedGitStatus(w http.ResponseWriter, r *http.Request) {
	workDir, err := managedGitWorkDirFromRequest(r)
	if err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	meta, err := LoadManagedGitMeta(workDir)
	if err != nil {
		jsonError(w, http.StatusNotFound, err.Error())
		return
	}
	jsonReply(w, http.StatusOK, meta)
}

func (s *HTTPServer) handleManagedGitCheckpoint(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	var body struct {
		Slug    string `json:"slug"`
		WorkDir string `json:"workDir"`
		Message string `json:"message"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	workDir, err := managedGitWorkDir(body.Slug, body.WorkDir)
	if err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	commit, err := ManagedGitCheckpoint(workDir, body.Message)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true, "commit": commit})
}

func (s *HTTPServer) handleManagedGitBackupRun(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	var body struct {
		Slug    string `json:"slug"`
		WorkDir string `json:"workDir"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	workDir, err := managedGitWorkDir(body.Slug, body.WorkDir)
	if err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	backup, err := ManagedGitBackup(workDir)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true, "backup": backup})
}

func (s *HTTPServer) handleManagedGitBackupCopy(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	var body struct {
		Slug       string `json:"slug"`
		WorkDir    string `json:"workDir"`
		TargetKind string `json:"targetKind"`
		TargetID   string `json:"targetId"`
		DestPath   string `json:"destPath"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil && err != io.EOF {
		jsonError(w, http.StatusBadRequest, "invalid json: "+err.Error())
		return
	}
	workDir, err := managedGitWorkDir(body.Slug, body.WorkDir)
	if err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	backup, err := ManagedGitBackupToTarget(workDir, body.TargetKind, body.TargetID, body.DestPath)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true, "backup": backup})
}

func (s *HTTPServer) handleManagedGitBackupDownload(w http.ResponseWriter, r *http.Request) {
	workDir, err := managedGitWorkDirFromRequest(r)
	if err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	backup, err := ManagedGitBackup(workDir)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filepath.Base(backup.Path)+`"`)
	w.Header().Set("X-Yaver-Commit", backup.Commit)
	http.ServeFile(w, r, backup.Path)
}

func (s *HTTPServer) handleManagedGitBackupReceive(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	repoID := Slugify(r.URL.Query().Get("repoId"))
	if repoID == "" {
		repoID = "received"
	}
	root, err := managedGitBackupsRoot()
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	dir := filepath.Join(root, "received", repoID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	name := time.Now().UTC().Format("20060102T150405Z") + ".bundle"
	path := filepath.Join(dir, name)
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	n, copyErr := io.Copy(f, http.MaxBytesReader(w, r.Body, 512<<20))
	closeErr := f.Close()
	if copyErr != nil {
		jsonError(w, http.StatusBadRequest, copyErr.Error())
		return
	}
	if closeErr != nil {
		jsonError(w, http.StatusInternalServerError, closeErr.Error())
		return
	}
	if err := os.Rename(tmp, path); err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	latest := filepath.Join(dir, "latest.bundle")
	_ = managedGitCopyFile(path, latest, 0o600)
	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":        true,
		"repoId":    repoID,
		"path":      path,
		"sizeBytes": n,
	})
}

func (s *HTTPServer) handleManagedGitBackupRestore(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	var body struct {
		Slug       string `json:"slug"`
		WorkDir    string `json:"workDir"`
		BundlePath string `json:"bundlePath"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil && err != io.EOF {
		jsonError(w, http.StatusBadRequest, "invalid json: "+err.Error())
		return
	}
	workDir, err := managedGitWorkDir(body.Slug, body.WorkDir)
	if err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	commit, err := ManagedGitRestoreBundle(workDir, body.BundlePath)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true, "commit": commit})
}

func (s *HTTPServer) handleManagedGitMirrorConnect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	var body struct {
		Slug        string `json:"slug"`
		WorkDir     string `json:"workDir"`
		Provider    string `json:"provider"`
		Host        string `json:"host"`
		RepoName    string `json:"repoName"`
		Visibility  string `json:"visibility"`
		Description string `json:"description"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil && err != io.EOF {
		jsonError(w, http.StatusBadRequest, "invalid json: "+err.Error())
		return
	}
	workDir, err := managedGitWorkDir(body.Slug, body.WorkDir)
	if err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	mirror, err := ManagedGitMirrorToProvider(workDir, body.Provider, body.Host, body.RepoName, body.Visibility, body.Description)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true, "mirror": mirror})
}

func (s *HTTPServer) handleManagedGitVisibility(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	var body struct {
		Slug       string `json:"slug"`
		WorkDir    string `json:"workDir"`
		Visibility string `json:"visibility"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	workDir, err := managedGitWorkDir(body.Slug, body.WorkDir)
	if err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	meta, err := LoadManagedGitMeta(workDir)
	if err != nil {
		jsonError(w, http.StatusNotFound, err.Error())
		return
	}
	meta.Visibility = normalizeManagedGitVisibility(body.Visibility)
	if err := SaveManagedGitMeta(workDir, meta); err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	jsonReply(w, http.StatusOK, meta)
}

func (s *HTTPServer) handleManagedGitDropboxOAuthStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	var body struct {
		RedirectURI string `json:"redirectUri"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	sess, err := startDropboxOAuth(body.RedirectURI)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":          true,
		"sessionId":   sess.ID,
		"authUrl":     sess.AuthURL,
		"redirectUri": sess.RedirectURI,
		"expiresAt":   sess.ExpiresAt.Format(time.RFC3339),
	})
}

func (s *HTTPServer) handleManagedGitDropboxOAuthSubmit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	var body struct {
		SessionID string `json:"sessionId"`
		Code      string `json:"code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil && err != io.EOF {
		jsonError(w, http.StatusBadRequest, "invalid json: "+err.Error())
		return
	}
	rec, err := submitDropboxOAuthCode(body.SessionID, body.Code)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":        true,
		"accountId": rec.AccountID,
		"scope":     rec.Scope,
		"expiresAt": rec.ExpiresAt,
	})
}

func (s *HTTPServer) handleManagedGitDropboxStatus(w http.ResponseWriter, r *http.Request) {
	rec, err := loadDropboxToken()
	if err != nil {
		jsonReply(w, http.StatusOK, map[string]interface{}{"connected": false})
		return
	}
	jsonReply(w, http.StatusOK, map[string]interface{}{
		"connected": true,
		"accountId": rec.AccountID,
		"scope":     rec.Scope,
		"expiresAt": rec.ExpiresAt,
		"updatedAt": rec.UpdatedAt,
	})
}

func managedGitWorkDirFromRequest(r *http.Request) (string, error) {
	return managedGitWorkDir(r.URL.Query().Get("slug"), r.URL.Query().Get("workDir"))
}

func managedGitWorkDir(slug, workDir string) (string, error) {
	if strings.TrimSpace(workDir) != "" {
		return filepath.Abs(workDir)
	}
	if strings.TrimSpace(slug) == "" {
		return "", fmt.Errorf("slug or workDir required")
	}
	return PhoneProjectDir(slug)
}

func mcpManagedGitStatus(slug, workDir string) interface{} {
	dir, err := managedGitWorkDir(slug, workDir)
	if err != nil {
		return map[string]interface{}{"error": err.Error()}
	}
	meta, err := LoadManagedGitMeta(dir)
	if err != nil {
		return map[string]interface{}{"error": err.Error()}
	}
	return meta
}

func mcpManagedGitEnable(slug, workDir, name, visibility string) interface{} {
	dir, err := managedGitWorkDir(slug, workDir)
	if err != nil {
		return map[string]interface{}{"error": err.Error()}
	}
	if slug == "" {
		slug = filepath.Base(dir)
	}
	meta, err := EnsureManagedGitForProject(dir, slug, name, &ManagedGitCreateOptions{Enabled: true, Visibility: visibility})
	if err != nil {
		return map[string]interface{}{"error": err.Error()}
	}
	if p, err := LoadPhoneProject(slug); err == nil {
		p.ManagedGit = meta
		_ = savePhoneMeta(p)
	}
	return meta
}

func mcpManagedGitCheckpoint(slug, workDir, message string) interface{} {
	dir, err := managedGitWorkDir(slug, workDir)
	if err != nil {
		return map[string]interface{}{"error": err.Error()}
	}
	commit, err := ManagedGitCheckpoint(dir, message)
	if err != nil {
		return map[string]interface{}{"error": err.Error()}
	}
	return map[string]interface{}{"ok": true, "commit": commit}
}

func mcpManagedGitBackup(slug, workDir, targetKind, targetID, destPath string) interface{} {
	dir, err := managedGitWorkDir(slug, workDir)
	if err != nil {
		return map[string]interface{}{"error": err.Error()}
	}
	if strings.TrimSpace(targetKind) != "" || strings.TrimSpace(destPath) != "" || strings.TrimSpace(targetID) != "" {
		backup, err := ManagedGitBackupToTarget(dir, targetKind, targetID, destPath)
		if err != nil {
			return map[string]interface{}{"error": err.Error()}
		}
		return map[string]interface{}{"ok": true, "backup": backup}
	}
	backup, err := ManagedGitBackup(dir)
	if err != nil {
		return map[string]interface{}{"error": err.Error()}
	}
	return map[string]interface{}{"ok": true, "backup": backup}
}

func mcpManagedGitRestore(slug, workDir, bundlePath string) interface{} {
	dir, err := managedGitWorkDir(slug, workDir)
	if err != nil {
		return map[string]interface{}{"error": err.Error()}
	}
	commit, err := ManagedGitRestoreBundle(dir, bundlePath)
	if err != nil {
		return map[string]interface{}{"error": err.Error()}
	}
	return map[string]interface{}{"ok": true, "commit": commit}
}

func mcpManagedGitMirror(slug, workDir, provider, host, repoName, visibility, description string) interface{} {
	dir, err := managedGitWorkDir(slug, workDir)
	if err != nil {
		return map[string]interface{}{"error": err.Error()}
	}
	mirror, err := ManagedGitMirrorToProvider(dir, provider, host, repoName, visibility, description)
	if err != nil {
		return map[string]interface{}{"error": err.Error()}
	}
	return map[string]interface{}{"ok": true, "mirror": mirror}
}
