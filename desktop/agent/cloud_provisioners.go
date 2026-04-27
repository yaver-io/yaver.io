package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// ProvisionResult is returned by every provisioner.
type ProvisionResult struct {
	OK          bool              `json:"ok"`
	Provider    string            `json:"provider"`
	Resource    string            `json:"resource"`
	ID          string            `json:"id,omitempty"`
	ConnectionString string       `json:"connectionString,omitempty"`
	Details     map[string]string `json:"details,omitempty"`
	Notes       string            `json:"notes,omitempty"`
	Manual      string            `json:"manual,omitempty"`
}

type provisioner func(name string, opts map[string]string) (*ProvisionResult, error)

// provisionerRegistry maps a target host to its provisioner function.
// Lean target set (2026-04-28): Supabase + Vercel (export-only escape
// routes). Convex Cloud, Cloudflare Workers, and Yaver Cloud are
// first-class deploy targets but don't need automated provisioning —
// the Convex/CF/Yaver CLIs / dashboards handle creation.
func provisionerRegistry() map[TargetHost]provisioner {
	return map[TargetHost]provisioner{
		HostSupabaseCloud: provisionSupabase,
		HostVercel:        provisionVercel,
	}
}

var provisionHTTP = &http.Client{Timeout: 30 * time.Second}

func doJSON(method, url string, headers map[string]string, body interface{}, out interface{}) error {
	var buf io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		buf = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, url, buf)
	if err != nil {
		return err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := provisionHTTP.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	data, _ := io.ReadAll(res.Body)
	if res.StatusCode >= 400 {
		return fmt.Errorf("%s %s: %d %s", method, url, res.StatusCode, strings.TrimSpace(string(data)))
	}
	if out != nil && len(data) > 0 {
		return json.Unmarshal(data, out)
	}
	return nil
}

// ---- Supabase Cloud ----

func provisionSupabase(name string, opts map[string]string) (*ProvisionResult, error) {
	token := accountField(ProviderSupabase, "token")
	if token == "" {
		return &ProvisionResult{Provider: "supabase", Manual: "Connect Supabase first via /accounts/connect"}, nil
	}
	// Supabase requires organization_id. We can fetch the user's orgs.
	var orgs []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if err := doJSON("GET", "https://api.supabase.com/v1/organizations",
		map[string]string{"Authorization": "Bearer " + token}, nil, &orgs); err != nil {
		return nil, err
	}
	if len(orgs) == 0 {
		return &ProvisionResult{Provider: "supabase", Manual: "No Supabase organization found. Create one at https://supabase.com/dashboard/org/_/general first."}, nil
	}
	orgID := orgs[0].ID
	if v, ok := opts["organization_id"]; ok && v != "" {
		orgID = v
	}
	region := opts["region"]
	if region == "" {
		region = "us-east-1"
	}
	dbPassword := opts["db_password"]
	if dbPassword == "" {
		dbPassword = generatePassword()
	}
	var out struct {
		ID     string `json:"id"`
		Name   string `json:"name"`
		Region string `json:"region"`
	}
	err := doJSON("POST", "https://api.supabase.com/v1/projects",
		map[string]string{"Authorization": "Bearer " + token},
		map[string]interface{}{
			"name":            name,
			"organization_id": orgID,
			"region":          region,
			"db_pass":         dbPassword,
		},
		&out,
	)
	if err != nil {
		return nil, err
	}
	return &ProvisionResult{
		OK: true, Provider: "supabase", Resource: "project", ID: out.ID,
		Details: map[string]string{
			"organization_id": orgID, "region": out.Region, "db_password": dbPassword,
			"dashboard": fmt.Sprintf("https://supabase.com/dashboard/project/%s", out.ID),
		},
		Notes: "Project is provisioning — anon/service role keys available at the dashboard URL. Use `supabase link --project-ref " + out.ID + "` locally.",
	}, nil
}

// ---- Vercel (link + deploy) ----

func provisionVercel(name string, opts map[string]string) (*ProvisionResult, error) {
	token := accountField(ProviderVercel, "token")
	if token == "" {
		return &ProvisionResult{Provider: "vercel", Manual: "Connect Vercel or run `npx vercel login`"}, nil
	}
	// Create a project (idempotent-ish — if it exists we get 409).
	var out struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	err := doJSON("POST", "https://api.vercel.com/v10/projects",
		map[string]string{"Authorization": "Bearer " + token},
		map[string]interface{}{"name": name, "framework": opts["framework"]},
		&out,
	)
	if err != nil && !strings.Contains(err.Error(), "already exists") {
		return nil, err
	}
	return &ProvisionResult{
		OK: true, Provider: "vercel", Resource: "project", ID: out.ID,
		Details: map[string]string{"dashboard": "https://vercel.com/dashboard"},
		Notes:   "Run `vercel link` in the project dir, then `vercel deploy`.",
	}, nil
}

// ---- helpers ----

func generatePassword() string {
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, 24)
	for i := range b {
		n := make([]byte, 1)
		_, _ = randRead(n)
		b[i] = charset[int(n[0])%len(charset)]
	}
	return string(b)
}

// randRead is a tiny shim so we don't pull crypto/rand into accounts.go twice.
func randRead(b []byte) (int, error) {
	return readFromRand(b)
}

// ---- MCP / HTTP ----

func mcpCloudProvision(host, name, optsJSON string) interface{} {
	registry := provisionerRegistry()
	fn, ok := registry[TargetHost(host)]
	if !ok {
		return map[string]interface{}{"error": fmt.Sprintf("no provisioner for host %q", host)}
	}
	opts := map[string]string{}
	for k, v := range parseJSONArgs(optsJSON) {
		opts[k] = fmt.Sprintf("%v", v)
	}
	res, err := fn(name, opts)
	if err != nil {
		return map[string]interface{}{"error": err.Error()}
	}
	return res
}
