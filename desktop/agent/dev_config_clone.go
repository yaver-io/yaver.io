package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type DetectedDevConfig struct {
	Key   string `json:"key"`
	Kind  string `json:"kind"`
	Path  string `json:"path"`
	Files int    `json:"files,omitempty"`
	Size  int64  `json:"size,omitempty"`
}

type DevConfigBundleItem struct {
	Key      string `json:"key"`
	Kind     string `json:"kind"`
	RelPath  string `json:"relPath"`
	Mode     uint32 `json:"mode"`
	Contents string `json:"contentsBase64"`
}

type DevConfigBundle struct {
	GeneratedAt string                `json:"generatedAt"`
	SourceHome  string                `json:"sourceHome,omitempty"`
	Items       []DevConfigBundleItem `json:"items"`
	Skipped     []string              `json:"skipped,omitempty"`
}

type devConfigCandidate struct {
	key  string
	kind string
	rel  string
	dir  bool
}

const (
	devConfigMaxFileBytes  = 256 * 1024
	devConfigMaxTotalBytes = 4 * 1024 * 1024
	devConfigMaxFiles      = 256
)

func devConfigCandidates() []devConfigCandidate {
	return []devConfigCandidate{
		{key: "vimrc", kind: "vim", rel: ".vimrc"},
		{key: "gvimrc", kind: "vim", rel: ".gvimrc"},
		{key: "vim-config", kind: "vim", rel: ".vim", dir: true},
		{key: "nvim-config", kind: "nvim", rel: ".config/nvim", dir: true},
		{key: "tmux", kind: "tmux", rel: ".tmux.conf"},
		{key: "tmux-config", kind: "tmux", rel: ".config/tmux", dir: true},
		{key: "zshrc", kind: "shell", rel: ".zshrc"},
		{key: "bashrc", kind: "shell", rel: ".bashrc"},
		{key: "bash-profile", kind: "shell", rel: ".bash_profile"},
		{key: "profile", kind: "shell", rel: ".profile"},
		{key: "inputrc", kind: "shell", rel: ".inputrc"},
		{key: "oh-my-zsh-custom", kind: "shell", rel: ".oh-my-zsh/custom", dir: true},
		{key: "starship", kind: "prompt", rel: ".config/starship.toml"},
		{key: "i3", kind: "window-manager", rel: ".config/i3", dir: true},
		{key: "i3-legacy", kind: "window-manager", rel: ".i3", dir: true},
		{key: "sway", kind: "window-manager", rel: ".config/sway", dir: true},
		{key: "alacritty", kind: "terminal", rel: ".config/alacritty", dir: true},
		{key: "kitty", kind: "terminal", rel: ".config/kitty", dir: true},
		{key: "wezterm", kind: "terminal", rel: ".config/wezterm", dir: true},
		{key: "gitconfig", kind: "git", rel: ".gitconfig"},
		{key: "gitignore-global", kind: "git", rel: ".gitignore_global"},
		{key: "codex", kind: "ai-runner", rel: ".codex", dir: true},
		{key: "claude", kind: "ai-runner", rel: ".claude", dir: true},
		{key: "opencode", kind: "ai-runner", rel: ".config/opencode", dir: true},
	}
}

func discoverDevConfigs() []DetectedDevConfig {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return nil
	}
	var out []DetectedDevConfig
	for _, c := range devConfigCandidates() {
		path := filepath.Join(home, filepath.FromSlash(c.rel))
		info, err := os.Stat(path)
		if err != nil {
			continue
		}
		item := DetectedDevConfig{Key: c.key, Kind: c.kind, Path: path}
		if info.IsDir() {
			files, size := countDevConfigDir(path)
			item.Files = files
			item.Size = size
		} else {
			item.Files = 1
			item.Size = info.Size()
		}
		out = append(out, item)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		return out[i].Key < out[j].Key
	})
	return out
}

func countDevConfigDir(root string) (int, int64) {
	files := 0
	var size int64
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || path == root {
			return nil
		}
		if shouldSkipDevConfigPath(path, d.IsDir()) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		files++
		size += info.Size()
		return nil
	})
	return files, size
}

func shouldSkipDevConfigPath(path string, isDir bool) bool {
	base := strings.ToLower(filepath.Base(path))
	lower := strings.ToLower(filepath.ToSlash(path))
	if isDir {
		switch base {
		case ".git", "node_modules", "vendor", "target", "dist", "build", "plugged", "bundle":
			return true
		}
	}
	secretWords := []string{"secret", "token", "apikey", "api_key", "credential", "credentials", "oauth", "session", "cookie", "keychain", "id_rsa", "id_ed25519", ".env"}
	for _, word := range secretWords {
		if strings.Contains(base, word) || strings.Contains(lower, "/"+word) {
			return true
		}
	}
	return false
}

func buildDevConfigBundle(keys []string) DevConfigBundle {
	home, err := os.UserHomeDir()
	bundle := DevConfigBundle{GeneratedAt: time.Now().UTC().Format(time.RFC3339)}
	if err != nil || home == "" {
		bundle.Skipped = append(bundle.Skipped, "home directory unavailable")
		return bundle
	}
	bundle.SourceHome = home
	allowedKeys := map[string]bool{}
	for _, key := range keys {
		key = strings.TrimSpace(key)
		if key != "" {
			allowedKeys[key] = true
		}
	}
	var total int64
	for _, c := range devConfigCandidates() {
		if len(allowedKeys) > 0 && !allowedKeys[c.key] {
			continue
		}
		abs := filepath.Join(home, filepath.FromSlash(c.rel))
		info, err := os.Stat(abs)
		if err != nil {
			continue
		}
		if info.IsDir() {
			_ = filepath.WalkDir(abs, func(path string, d os.DirEntry, err error) error {
				if err != nil || path == abs {
					return nil
				}
				if shouldSkipDevConfigPath(path, d.IsDir()) {
					if d.IsDir() {
						return filepath.SkipDir
					}
					bundle.Skipped = append(bundle.Skipped, trimHomePath(home, path))
					return nil
				}
				if d.IsDir() {
					return nil
				}
				total = appendDevConfigFile(&bundle, home, c, path, total)
				if len(bundle.Items) >= devConfigMaxFiles || total >= devConfigMaxTotalBytes {
					return filepath.SkipAll
				}
				return nil
			})
			continue
		}
		if shouldSkipDevConfigPath(abs, false) {
			bundle.Skipped = append(bundle.Skipped, trimHomePath(home, abs))
			continue
		}
		total = appendDevConfigFile(&bundle, home, c, abs, total)
	}
	return bundle
}

func appendDevConfigFile(bundle *DevConfigBundle, home string, c devConfigCandidate, path string, total int64) int64 {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return total
	}
	if info.Size() > devConfigMaxFileBytes {
		bundle.Skipped = append(bundle.Skipped, trimHomePath(home, path)+" too large")
		return total
	}
	if total+info.Size() > devConfigMaxTotalBytes {
		bundle.Skipped = append(bundle.Skipped, "config bundle size limit reached")
		return total
	}
	data, err := os.ReadFile(path)
	if err != nil {
		bundle.Skipped = append(bundle.Skipped, trimHomePath(home, path)+" read failed")
		return total
	}
	if looksLikeSecretConfig(data) {
		bundle.Skipped = append(bundle.Skipped, trimHomePath(home, path)+" looked secret-bearing")
		return total
	}
	rel, err := filepath.Rel(home, path)
	if err != nil {
		return total
	}
	bundle.Items = append(bundle.Items, DevConfigBundleItem{
		Key:      c.key,
		Kind:     c.kind,
		RelPath:  filepath.ToSlash(rel),
		Mode:     uint32(info.Mode().Perm()),
		Contents: base64.StdEncoding.EncodeToString(data),
	})
	return total + info.Size()
}

func looksLikeSecretConfig(data []byte) bool {
	if len(data) == 0 {
		return false
	}
	s := strings.ToLower(string(data))
	patterns := []string{"api_key=", "apikey=", "api key", "secret_key", "access_token", "refresh_token", "bearer ", "authorization:", "ghp_", "ghs_", "github_pat_", "glpat-", "sk-ant-", "sk-proj-", "xoxb-", "xoxp-", "xapp-", "akia", "eyj", "vc_", "-----begin private key-----", "-----begin openssh private key-----", "-----begin rsa private key-----", "-----begin ec private key-----"}
	for _, p := range patterns {
		if strings.Contains(s, p) {
			return true
		}
	}
	return false
}

func trimHomePath(home, path string) string {
	if rel, err := filepath.Rel(home, path); err == nil {
		return "~/" + filepath.ToSlash(rel)
	}
	return path
}

func applyDevConfigBundle(bundle DevConfigBundle, dryRun bool) (map[string]any, error) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return nil, fmt.Errorf("home directory unavailable")
	}
	applied := []string{}
	backedUp := []string{}
	for _, item := range bundle.Items {
		rel := filepath.Clean(filepath.FromSlash(item.RelPath))
		if rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
			return nil, fmt.Errorf("unsafe config path: %s", item.RelPath)
		}
		target := filepath.Join(home, rel)
		data, err := base64.StdEncoding.DecodeString(item.Contents)
		if err != nil {
			return nil, fmt.Errorf("decode %s: %w", item.RelPath, err)
		}
		if dryRun {
			applied = append(applied, item.RelPath)
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return nil, err
		}
		if _, err := os.Stat(target); err == nil {
			backup := target + ".yaver-backup-" + time.Now().UTC().Format("20060102150405.000000000")
			if err := os.Rename(target, backup); err != nil {
				return nil, err
			}
			backedUp = append(backedUp, trimHomePath(home, backup))
		}
		mode := os.FileMode(item.Mode)
		if mode == 0 {
			mode = 0o600
		}
		if err := os.WriteFile(target, data, mode); err != nil {
			return nil, err
		}
		applied = append(applied, item.RelPath)
	}
	return map[string]any{"ok": true, "applied": applied, "backedUp": backedUp, "skipped": bundle.Skipped, "dryRun": dryRun}, nil
}

func (s *HTTPServer) handleDevConfigBundle(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}
	var body struct {
		Keys []string `json:"keys"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	jsonReply(w, http.StatusOK, map[string]any{"ok": true, "bundle": buildDevConfigBundle(body.Keys)})
}

func (s *HTTPServer) handleDevConfigApply(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}
	var body struct {
		Bundle DevConfigBundle `json:"bundle"`
		DryRun bool            `json:"dryRun"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	out, err := applyDevConfigBundle(body.Bundle, body.DryRun)
	if err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	jsonReply(w, http.StatusOK, out)
}
