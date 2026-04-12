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
func provisionerRegistry() map[TargetHost]provisioner {
	return map[TargetHost]provisioner{
		HostNeon:          provisionNeon,
		HostSupabaseCloud: provisionSupabase,
		HostTurso:         provisionTurso,
		HostCloudflareD1:  provisionCloudflareD1,
		HostVercel:        provisionVercel,
		HostHetzner:       provisionHetzner,
		HostRailway:       provisionRailway,
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

// ---- Neon ----

func provisionNeon(name string, opts map[string]string) (*ProvisionResult, error) {
	token := accountField(ProviderNeon, "token")
	if token == "" {
		return &ProvisionResult{Provider: "neon", Manual: "Connect Neon first: POST /accounts/connect with provider=neon token=<key>"}, nil
	}
	var out struct {
		Project struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"project"`
		ConnectionUris []struct {
			ConnectionUri string `json:"connection_uri"`
		} `json:"connection_uris"`
	}
	err := doJSON("POST", "https://console.neon.tech/api/v2/projects",
		map[string]string{"Authorization": "Bearer " + token, "Accept": "application/json"},
		map[string]interface{}{"project": map[string]interface{}{"name": name}},
		&out,
	)
	if err != nil {
		return nil, err
	}
	dsn := ""
	if len(out.ConnectionUris) > 0 {
		dsn = out.ConnectionUris[0].ConnectionUri
	}
	return &ProvisionResult{
		OK: true, Provider: "neon", Resource: "project",
		ID: out.Project.ID, ConnectionString: dsn,
	}, nil
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

// ---- Turso ----

func provisionTurso(name string, opts map[string]string) (*ProvisionResult, error) {
	token := accountField(ProviderTurso, "token")
	if token == "" {
		return &ProvisionResult{Provider: "turso", Manual: "Connect Turso via /accounts/connect (token from `turso auth token`)"}, nil
	}
	// Need organization slug too (usually the username).
	org := opts["organization"]
	if org == "" {
		// Best-effort: GET /v1/organizations returns list.
		var list struct {
			Organizations []struct{ Slug string `json:"slug"` } `json:"organizations"`
		}
		if err := doJSON("GET", "https://api.turso.tech/v1/organizations",
			map[string]string{"Authorization": "Bearer " + token}, nil, &list); err == nil && len(list.Organizations) > 0 {
			org = list.Organizations[0].Slug
		}
	}
	if org == "" {
		return &ProvisionResult{Provider: "turso", Manual: "Could not resolve Turso organization. Run `turso org list` and pass it as opts.organization"}, nil
	}
	var out struct {
		Database struct {
			Name     string `json:"Name"`
			Hostname string `json:"Hostname"`
		} `json:"database"`
	}
	err := doJSON("POST", fmt.Sprintf("https://api.turso.tech/v1/organizations/%s/databases", org),
		map[string]string{"Authorization": "Bearer " + token},
		map[string]interface{}{"name": name, "group": "default"},
		&out,
	)
	if err != nil {
		return nil, err
	}
	return &ProvisionResult{
		OK: true, Provider: "turso", Resource: "database",
		ID:               out.Database.Name,
		ConnectionString: "libsql://" + out.Database.Hostname,
		Details:          map[string]string{"organization": org, "hostname": out.Database.Hostname},
		Notes:            "Generate an auth token with `turso db tokens create " + out.Database.Name + "`",
	}, nil
}

// ---- Cloudflare D1 ----

func provisionCloudflareD1(name string, opts map[string]string) (*ProvisionResult, error) {
	token := accountField(ProviderCloudflare, "token")
	account := accountField(ProviderCloudflare, "accountId")
	if token == "" || account == "" {
		return &ProvisionResult{Provider: "cloudflare", Manual: "Connect Cloudflare with token + accountId"}, nil
	}
	var out struct {
		Result struct {
			UUID string `json:"uuid"`
			Name string `json:"name"`
		} `json:"result"`
		Success bool `json:"success"`
	}
	err := doJSON("POST", fmt.Sprintf("https://api.cloudflare.com/client/v4/accounts/%s/d1/database", account),
		map[string]string{"Authorization": "Bearer " + token},
		map[string]interface{}{"name": name},
		&out,
	)
	if err != nil {
		return nil, err
	}
	return &ProvisionResult{
		OK: out.Success, Provider: "cloudflare", Resource: "d1",
		ID: out.Result.UUID,
		Notes: "Add to wrangler.toml:\n[[d1_databases]]\nbinding = \"DB\"\ndatabase_name = \"" + out.Result.Name + "\"\ndatabase_id = \"" + out.Result.UUID + "\"",
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

// ---- Hetzner (provision a CPX21 VPS) ----

func provisionHetzner(name string, opts map[string]string) (*ProvisionResult, error) {
	token := accountField(ProviderHetzner, "token")
	if token == "" {
		return &ProvisionResult{Provider: "hetzner", Manual: "Connect Hetzner via /accounts/connect"}, nil
	}
	serverType := opts["server_type"]
	if serverType == "" {
		serverType = "cpx11" // smallest ARM is cax11 — pick AMD baseline
	}
	location := opts["location"]
	if location == "" {
		location = "nbg1"
	}
	image := opts["image"]
	if image == "" {
		image = "ubuntu-24.04"
	}
	var out struct {
		Server struct {
			ID         int    `json:"id"`
			Name       string `json:"name"`
			PublicNet  struct {
				IPv4 struct{ IP string `json:"ip"` } `json:"ipv4"`
			} `json:"public_net"`
			Status string `json:"status"`
		} `json:"server"`
		RootPassword string `json:"root_password"`
	}
	err := doJSON("POST", "https://api.hetzner.cloud/v1/servers",
		map[string]string{"Authorization": "Bearer " + token},
		map[string]interface{}{
			"name":        name,
			"server_type": serverType,
			"location":    location,
			"image":       image,
			"start_after_create": true,
		},
		&out,
	)
	if err != nil {
		return nil, err
	}
	return &ProvisionResult{
		OK: true, Provider: "hetzner", Resource: "server",
		ID: fmt.Sprintf("%d", out.Server.ID),
		Details: map[string]string{
			"name":          out.Server.Name,
			"ipv4":          out.Server.PublicNet.IPv4.IP,
			"root_password": out.RootPassword,
			"status":        out.Server.Status,
		},
		Notes: "SSH: ssh root@" + out.Server.PublicNet.IPv4.IP + "   — then `curl -fsSL yaver.io/install | bash` to install the agent",
	}, nil
}

// ---- Railway ----

func provisionRailway(name string, opts map[string]string) (*ProvisionResult, error) {
	token := accountField(ProviderRailway, "token")
	if token == "" {
		return &ProvisionResult{Provider: "railway", Manual: "Connect Railway or run `railway login`"}, nil
	}
	// Railway uses GraphQL. Minimal project creation.
	query := `mutation projectCreate($name: String!) { projectCreate(input: {name: $name}) { id name } }`
	var out struct {
		Data struct {
			ProjectCreate struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"projectCreate"`
		} `json:"data"`
	}
	err := doJSON("POST", "https://backboard.railway.app/graphql/v2",
		map[string]string{"Authorization": "Bearer " + token},
		map[string]interface{}{"query": query, "variables": map[string]interface{}{"name": name}},
		&out,
	)
	if err != nil {
		return nil, err
	}
	return &ProvisionResult{
		OK: true, Provider: "railway", Resource: "project",
		ID:    out.Data.ProjectCreate.ID,
		Notes: "Run `railway link " + out.Data.ProjectCreate.ID + "` then `railway up`",
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
