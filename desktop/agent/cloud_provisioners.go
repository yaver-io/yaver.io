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
	// OrphanSnapshots: snapshots left on the cloud account after a
	// server delete (e.g. an opted-in "snapshot first" recovery image,
	// or pre-resize backups). Hetzner never auto-deletes these — they
	// bill until removed. Surfaced so the Remove flow can never
	// SILENTLY orphan a paid image. Empty/omitted when none.
	OrphanSnapshots []HetznerSnapshotInfo `json:"orphanSnapshots,omitempty"`
}

type provisioner func(name string, opts map[string]string) (*ProvisionResult, error)

// provisionerRegistry maps a target host to its provisioner function.
// Supabase + Vercel are export-only escape routes. Hetzner is the
// managed-cloud box target — re-added 2026-05-17 for programmatic
// add/remove from web+mobile (docs/managed-cloud-host-lifecycle.md);
// the API client always existed (cloud_deploy.go), it was just
// unwired 2026-04-28. Convex/CF/Yaver Cloud need no auto-provision.
func provisionerRegistry() map[TargetHost]provisioner {
	return map[TargetHost]provisioner{
		HostSupabaseCloud: provisionSupabase,
		HostVercel:        provisionVercel,
		HostHetzner:       provisionHetzner,
		HostHetznerRobot:  provisionHetznerRobot,
		// GPU-rental orchestration (gpu_rental.go): Salad = hourly GPU
		// container group; DeepInfra = serverless inference binding (no
		// machine). Both mint application-runtime inference config.
		HostSalad:     provisionSalad,
		HostDeepInfra: provisionDeepInfra,
	}
}

// provisionHetzner creates a managed Hetzner box. Token comes from
// the vault-backed accounts store (accountField) — never from the
// request payload or Convex, per the privacy contract. The numeric
// server id is returned so cloud_destroy can snapshot+delete it.
func provisionHetzner(name string, opts map[string]string) (*ProvisionResult, error) {
	token := accountField(ProviderHetzner, "token")
	if token == "" {
		return &ProvisionResult{Provider: "hetzner", Manual: "Connect Hetzner first via /accounts/connect (Console → Security → API Tokens)."}, nil
	}
	plan := opts["plan"]
	if plan == "" {
		plan = "starter"
	}
	region := opts["region"]
	if region == "" {
		region = "eu"
	}
	workDir := opts["workDir"]
	if workDir == "" {
		workDir = "."
	}
	m, err := NewCloudDeployManager(workDir)
	if err != nil {
		return nil, err
	}
	ip, id, err := m.hetznerCreateServer(token, name, plan, region)
	if err != nil {
		return nil, err
	}
	return &ProvisionResult{
		OK: true, Provider: "hetzner", Resource: "server", ID: id,
		Details: map[string]string{"ip": ip, "plan": plan, "region": region, "name": name},
		Notes:   "Box provisioning; cloud-init brings up the yaver agent — it will appear as a pending device to claim. Decommission via cloud_destroy (snapshots first).",
	}, nil
}

// mcpCloudDestroy snapshots then deletes a managed Hetzner box.
// confirm=true is mandatory; snapshot-before-delete is mandatory
// (opts.skipSnapshot=true is the only, explicit, override). Token is
// vault-backed. Self-destruct prevention (don't delete the box you
// orchestrate from) is enforced one layer up in the recycle
// orchestration (Phase B) — this primitive just destroys an id.
func mcpCloudDestroy(host, id, optsJSON string) interface{} {
	if TargetHost(host) != HostHetzner {
		return map[string]interface{}{"error": fmt.Sprintf("cloud_destroy supports host %q only (got %q)", HostHetzner, host)}
	}
	if strings.TrimSpace(id) == "" {
		return map[string]interface{}{"error": "id (hetzner numeric server id) is required"}
	}
	opts := map[string]string{}
	for k, v := range parseJSONArgs(optsJSON) {
		opts[k] = fmt.Sprintf("%v", v)
	}
	if opts["confirm"] != "true" {
		return map[string]interface{}{"error": "destroy requires confirm=true"}
	}
	token := accountField(ProviderHetzner, "token")
	if token == "" {
		return map[string]interface{}{"error": "Hetzner not connected — /accounts/connect first"}
	}
	m, err := NewCloudDeployManager(".")
	if err != nil {
		return map[string]interface{}{"error": err.Error()}
	}
	if opts["skipSnapshot"] != "true" {
		label := fmt.Sprintf("yaver-predelete-%s-%d", id, time.Now().Unix())
		if serr := m.hetznerSnapshotServer(token, id, label); serr != nil {
			return map[string]interface{}{"error": "snapshot failed — NOT deleting (recover-safety): " + serr.Error()}
		}
	}
	if derr := m.hetznerDeleteServer(token, id); derr != nil {
		return map[string]interface{}{"error": derr.Error()}
	}
	// Surface (never silently leave) snapshots the delete orphaned —
	// the opted-in recovery image above and any pre-resize backups.
	// Best-effort: a list failure must not turn a successful delete
	// into an error.
	notes := "server deleted"
	if opts["skipSnapshot"] != "true" {
		notes = "snapshot taken, server deleted"
	}
	orphans, lerr := m.hetznerSnapshotsForServer(token, id)
	if lerr == nil && len(orphans) > 0 {
		return &ProvisionResult{OK: true, Provider: "hetzner", Resource: "server", ID: id, Notes: notes, OrphanSnapshots: orphans}
	}
	return &ProvisionResult{OK: true, Provider: "hetzner", Resource: "server", ID: id, Notes: notes}
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
