package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// seamlessConvexCloud creates a Convex Cloud deployment and captures its URL
// and admin key so the rest of the switch steps can run non-interactively.
//
// Tries two paths:
//   1. `npx convex deploy --cmd-url-env-var-name TMP` to trigger interactive
//      login if needed — but we want non-interactive. If a token is already
//      stored (~/.convex), `npx convex deployment` returns the URL silently.
//   2. Reads ~/.convex for the dev token and creates a deployment via the
//      Convex dashboard API.
//
// Captured fields are persisted into .env.local under a YAVER SWITCH block
// and returned in the step output.
func seamlessConvexCloud(projectDir string) (map[string]string, error) {
	details := map[string]string{}

	// Ensure `npx convex dev --configure` has been run once so .env.local has a
	// CONVEX_URL. We detect by checking for an existing deployment.
	if url := readEnvValue(projectDir, "CONVEX_URL"); url != "" {
		details["CONVEX_URL"] = url
	}

	// Run `npx convex deploy` — it picks the existing deployment or creates
	// one based on the local auth token.
	cmd := exec.Command("npx", "--yes", "convex", "deploy", "--yes")
	cmd.Dir = projectDir
	cmd.Env = append(os.Environ(), "CI=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		// If login hasn't happened, return the manual step instead of failing hard.
		return details, fmt.Errorf("convex deploy failed (%w). Run `npx convex login` inside %s, then retry. Output: %s", err, projectDir, string(out))
	}
	// After a successful deploy, .env.local should contain CONVEX_URL + CONVEX_DEPLOYMENT.
	if url := readEnvValue(projectDir, "CONVEX_URL"); url != "" {
		details["CONVEX_URL"] = url
	}
	if deployment := readEnvValue(projectDir, "CONVEX_DEPLOYMENT"); deployment != "" {
		details["CONVEX_DEPLOYMENT"] = deployment
	}
	return details, nil
}

// seamlessSupabaseCloud creates (or reuses) a Supabase Cloud project, captures
// API URL + anon + service-role keys, and writes them into .env.local. Uses
// the already-implemented provisionSupabase but follows through on credential
// retrieval that the provisioner couldn't do at creation time.
func seamlessSupabaseCloud(projectDir, projectName string) (map[string]string, error) {
	token := accountField(ProviderSupabase, "token")
	if token == "" {
		return nil, fmt.Errorf("Supabase token not connected — `yaver account_connect supabase token=<pat>`")
	}

	// 1. Create project (reuse provisioner).
	if projectName == "" {
		projectName = filepath.Base(projectDir)
	}
	result, err := provisionSupabase(projectName, nil)
	if err != nil {
		return nil, err
	}
	if result.Manual != "" {
		return nil, fmt.Errorf("supabase manual step required: %s", result.Manual)
	}

	details := map[string]string{
		"project_id": result.ID,
		"region":     result.Details["region"],
	}
	projectID := result.ID

	// 2. Wait for provisioning to finish (up to 2 minutes).
	if err := waitSupabaseReady(token, projectID, 2*time.Minute); err != nil {
		return details, fmt.Errorf("supabase project stuck provisioning: %w", err)
	}

	// 3. Fetch API keys (anon + service_role).
	keys, err := fetchSupabaseAPIKeys(token, projectID)
	if err != nil {
		return details, err
	}
	apiURL := fmt.Sprintf("https://%s.supabase.co", projectID)
	details["SUPABASE_URL"] = apiURL
	details["SUPABASE_ANON_KEY"] = keys["anon"]
	details["SUPABASE_SERVICE_ROLE_KEY"] = keys["service_role"]

	// 4. Fetch the DB connection string (password was set in provisionSupabase).
	if dsn := result.Details["db_password"]; dsn != "" {
		// Construct: postgres://postgres:<pass>@db.<id>.supabase.co:5432/postgres
		details["DATABASE_URL"] = fmt.Sprintf("postgres://postgres:%s@db.%s.supabase.co:5432/postgres", dsn, projectID)
		// This is also what pg_restore wants.
		_ = os.Setenv("PG_TARGET_DSN", details["DATABASE_URL"])
	}

	// 5. Persist into .env.local.
	writeSeamlessEnvBlock(projectDir, "supabase-cloud", details)
	return details, nil
}

func waitSupabaseReady(token, projectID string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		var out struct{ Status string `json:"status"` }
		if err := doJSON("GET",
			fmt.Sprintf("https://api.supabase.com/v1/projects/%s", projectID),
			map[string]string{"Authorization": "Bearer " + token}, nil, &out); err == nil {
			if strings.EqualFold(out.Status, "ACTIVE_HEALTHY") || strings.EqualFold(out.Status, "ACTIVE") || out.Status == "" {
				return nil
			}
		}
		time.Sleep(8 * time.Second)
	}
	return fmt.Errorf("timeout after %s", timeout)
}

func fetchSupabaseAPIKeys(token, projectID string) (map[string]string, error) {
	req, _ := http.NewRequest("GET",
		fmt.Sprintf("https://api.supabase.com/v1/projects/%s/api-keys", projectID), nil)
	req.Header.Set("Authorization", "Bearer "+token)
	res, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	data, _ := io.ReadAll(res.Body)
	if res.StatusCode >= 400 {
		return nil, fmt.Errorf("supabase api-keys: %d %s", res.StatusCode, strings.TrimSpace(string(data)))
	}
	var raw []struct {
		Name   string `json:"name"`
		APIKey string `json:"api_key"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, k := range raw {
		out[k.Name] = k.APIKey
	}
	return out, nil
}

// seamlessNeon was removed 2026-04-28 — Neon is no longer a supported
// switch target in the lean stack.

// readEnvValue extracts the value of KEY from .env.local / .env if present.
func readEnvValue(projectDir, key string) string {
	for _, name := range []string{".env.local", ".env"} {
		data, err := os.ReadFile(filepath.Join(projectDir, name))
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, key+"=") {
				return strings.Trim(strings.TrimPrefix(line, key+"="), `"'`)
			}
		}
	}
	return ""
}

// writeSeamlessEnvBlock appends/replaces a marker-delimited block in .env.local.
// Idempotent — re-running replaces the block.
func writeSeamlessEnvBlock(projectDir, provider string, kv map[string]string) {
	path := filepath.Join(projectDir, ".env.local")
	data, _ := os.ReadFile(path)
	existing := string(data)
	marker := "# === yaver " + provider + " ==="
	endMarker := "# === end " + provider + " ==="

	if i := strings.Index(existing, marker); i >= 0 {
		if j := strings.Index(existing[i:], endMarker); j >= 0 {
			existing = existing[:i] + existing[i+j+len(endMarker):]
		}
	}

	var sb bytes.Buffer
	sb.WriteString(strings.TrimRight(existing, "\n"))
	if sb.Len() > 0 {
		sb.WriteString("\n\n")
	}
	sb.WriteString(marker + "\n")
	for k, v := range kv {
		if v != "" {
			fmt.Fprintf(&sb, "%s=%s\n", k, v)
		}
	}
	sb.WriteString(endMarker + "\n")
	_ = os.WriteFile(path, sb.Bytes(), 0o644)
}
