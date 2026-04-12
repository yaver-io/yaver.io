package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// Environment switcher: a project can have multiple .env files under
// .yaver/envs/<name>.env — "local" (default), "staging", "production".
// Switching makes .env.local a copy of the chosen env, writes the active
// name to .yaver/active-env, and can swap which deploy target is used.

const defaultEnvName = "local"

func envsDir(projectDir string) string { return filepath.Join(projectDir, ".yaver", "envs") }
func activeEnvFile(projectDir string) string {
	return filepath.Join(projectDir, ".yaver", "active-env")
}

// ListEnvs returns every env file under .yaver/envs/. Always includes "local"
// even if the file is missing (falls back to the project's .env.local).
func ListEnvs(projectDir string) []string {
	seen := map[string]bool{defaultEnvName: true}
	out := []string{defaultEnvName}
	dir := envsDir(projectDir)
	if entries, err := os.ReadDir(dir); err == nil {
		for _, e := range entries {
			n := strings.TrimSuffix(e.Name(), ".env")
			if !seen[n] {
				out = append(out, n)
				seen[n] = true
			}
		}
	}
	// Stable order with common envs first.
	priority := map[string]int{"local": 0, "staging": 1, "production": 2}
	sortStable(out, func(a, b string) bool {
		pa, ok := priority[a]
		if !ok {
			pa = 10
		}
		pb, ok := priority[b]
		if !ok {
			pb = 10
		}
		if pa != pb {
			return pa < pb
		}
		return a < b
	})
	return out
}

func sortStable(arr []string, less func(a, b string) bool) {
	for i := 1; i < len(arr); i++ {
		for j := i; j > 0 && less(arr[j], arr[j-1]); j-- {
			arr[j], arr[j-1] = arr[j-1], arr[j]
		}
	}
}

// ActiveEnv returns the currently-active env name, defaulting to "local".
func ActiveEnv(projectDir string) string {
	data, err := os.ReadFile(activeEnvFile(projectDir))
	if err != nil || len(data) == 0 {
		return defaultEnvName
	}
	return strings.TrimSpace(string(data))
}

// SwitchEnv swaps .env.local to match the chosen environment and records the
// choice in .yaver/active-env.  Mode matters:
//   - if .yaver/envs/<name>.env exists → copy its contents over .env.local
//   - else if name == "local" → leave .env.local untouched (it IS local)
//   - else → error
//
// The previous .env.local is preserved at .yaver/envs/local.env if missing so
// the user never loses their pristine local config.
func SwitchEnv(projectDir, name string) (map[string]interface{}, error) {
	if name == "" {
		return nil, fmt.Errorf("env name required")
	}
	envPath := filepath.Join(projectDir, ".env.local")
	targetFile := filepath.Join(envsDir(projectDir), name+".env")

	// Ensure local snapshot exists.
	localSnap := filepath.Join(envsDir(projectDir), "local.env")
	if _, err := os.Stat(localSnap); os.IsNotExist(err) {
		if data, err := os.ReadFile(envPath); err == nil {
			_ = os.MkdirAll(envsDir(projectDir), 0o755)
			_ = os.WriteFile(localSnap, data, 0o600)
		}
	}

	if name == "local" {
		// Restore from snapshot if one exists, else leave current file.
		if data, err := os.ReadFile(localSnap); err == nil {
			if err := os.WriteFile(envPath, data, 0o600); err != nil {
				return nil, fmt.Errorf("restore local: %w", err)
			}
		}
	} else {
		data, err := os.ReadFile(targetFile)
		if err != nil {
			return nil, fmt.Errorf("env %q not defined — create %s", name, targetFile)
		}
		if err := os.WriteFile(envPath, data, 0o600); err != nil {
			return nil, fmt.Errorf("write .env.local: %w", err)
		}
	}

	_ = os.MkdirAll(filepath.Dir(activeEnvFile(projectDir)), 0o755)
	if err := os.WriteFile(activeEnvFile(projectDir), []byte(name), 0o644); err != nil {
		return nil, err
	}
	AuditLog("", "env_switch", projectDir, name, "success", "", "")
	return map[string]interface{}{"active": name, "envs": ListEnvs(projectDir)}, nil
}

// SaveEnv writes an env file (creating the envs dir if needed). Use this to
// populate .yaver/envs/staging.env or .yaver/envs/production.env before the
// user can switch to it.
func SaveEnv(projectDir, name, body string) error {
	if name == "local" {
		// Writing "local" just updates .env.local + the snapshot.
		_ = os.MkdirAll(envsDir(projectDir), 0o755)
		_ = os.WriteFile(filepath.Join(envsDir(projectDir), "local.env"), []byte(body), 0o600)
		return os.WriteFile(filepath.Join(projectDir, ".env.local"), []byte(body), 0o600)
	}
	_ = os.MkdirAll(envsDir(projectDir), 0o755)
	return os.WriteFile(filepath.Join(envsDir(projectDir), name+".env"), []byte(body), 0o600)
}

// LoadEnv returns the contents of an env file. For "local" it returns the
// current .env.local, not the snapshot.
func LoadEnv(projectDir, name string) (string, error) {
	if name == "local" {
		data, err := os.ReadFile(filepath.Join(projectDir, ".env.local"))
		if err != nil {
			return "", err
		}
		return string(data), nil
	}
	data, err := os.ReadFile(filepath.Join(envsDir(projectDir), name+".env"))
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// DeleteEnv removes a non-local env file.
func DeleteEnv(projectDir, name string) error {
	if name == "local" {
		return fmt.Errorf("cannot delete the local env")
	}
	path := filepath.Join(envsDir(projectDir), name+".env")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil
	}
	return os.Remove(path)
}

// ---- HTTP ----

func (s *HTTPServer) handleProjectEnvList(w http.ResponseWriter, r *http.Request) {
	dir := s.dirParam(r)
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"active": ActiveEnv(dir),
		"envs":   ListEnvs(dir),
	})
}

func (s *HTTPServer) handleProjectEnvSwitch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	var b struct{ Name string `json:"name"` }
	_ = json.NewDecoder(r.Body).Decode(&b)
	res, err := SwitchEnv(s.dirParam(r), b.Name)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *HTTPServer) handleProjectEnvSave(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	var b struct {
		Name string `json:"name"`
		Body string `json:"body"`
	}
	_ = json.NewDecoder(r.Body).Decode(&b)
	if err := SaveEnv(s.dirParam(r), b.Name, b.Body); err != nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true})
}

func (s *HTTPServer) handleProjectEnvLoad(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	if name == "" {
		jsonError(w, http.StatusBadRequest, "name required")
		return
	}
	body, err := LoadEnv(s.dirParam(r), name)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"name": name, "body": body})
}

func (s *HTTPServer) handleProjectEnvDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	var b struct{ Name string `json:"name"` }
	_ = json.NewDecoder(r.Body).Decode(&b)
	if err := DeleteEnv(s.dirParam(r), b.Name); err != nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true})
}
