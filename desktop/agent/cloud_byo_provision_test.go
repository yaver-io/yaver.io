package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// Headless full-lifecycle + security tests for BYO Hetzner provisioning
// (cloud_byo_provision.go). Real httptest fake Hetzner (shared
// newFakeHetzner / withRawFakeHetzner harness), no mocks, $0, no real
// account touched — the manager methods take the token as a param so
// the vault is never read or written here.

func TestCloudProvision_DryRunByDefault(t *testing.T) {
	t.Setenv("YAVER_CLOUD_STOPSTART_LIVE", "") // force fail-closed
	res := opsCloudProvisionHandler(OpsContext{}, json.RawMessage(`{"plan":"starter","confirm":true}`))
	if !res.OK {
		t.Fatalf("dry-run should be OK, got %+v", res)
	}
	mm, _ := res.Initial.(map[string]interface{})
	if mm == nil || mm["dryRun"] != true {
		t.Fatalf("expected dryRun:true (no real create without the live flag), got %#v", res.Initial)
	}
}

func TestCloudProvision_RejectsBadRepoUrl(t *testing.T) {
	t.Setenv("YAVER_CLOUD_STOPSTART_LIVE", "1")
	res := opsCloudProvisionHandler(OpsContext{}, json.RawMessage(`{"repoUrl":"foo; rm -rf /","confirm":true}`))
	if res.OK || res.Code != "bad_payload" {
		t.Fatalf("a non-git repoUrl must be rejected (shell-injection guard); got %+v", res)
	}
}

func TestHetznerCreateServerCustom_ImageAndRepoClone(t *testing.T) {
	var gotImage interface{}
	var gotUserData string
	defer withRawFakeHetzner(t, func(w http.ResponseWriter, r *http.Request) {
		var body map[string]interface{}
		_ = json.NewDecoder(r.Body).Decode(&body)
		gotImage = body["image"]
		gotUserData, _ = body["user_data"].(string)
		_, _ = w.Write([]byte(`{"server":{"id":4242,"public_net":{"ipv4":{"ip":"203.0.113.7"}}}}`))
	})()

	m, _ := NewCloudDeployManager(".")
	ip, id, err := m.hetznerCreateServerCustom("tok", "box", "starter", "eu", "99001", "https://github.com/acme/app.git")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if ip != "203.0.113.7" || id != "4242" {
		t.Fatalf("want 203.0.113.7/4242, got %s/%s", ip, id)
	}
	// Prebuilt snapshot id must go as a NUMBER (Hetzner image-by-id).
	if f, ok := gotImage.(float64); !ok || int(f) != 99001 {
		t.Fatalf("image must be numeric 99001, got %#v", gotImage)
	}
	// Repo must be shallow-cloned on first boot. shellQuote leaves a
	// clean URL bare (it quotes only when metacharacters are present);
	// injection is blocked upstream by gitURLRe + quote-when-needed.
	if !strings.Contains(gotUserData, "git clone --depth 1 https://github.com/acme/app.git /root/workspace") {
		t.Fatalf("user_data missing the shallow clone; got:\n%s", gotUserData)
	}
}

func TestHetznerCreateServerCustom_DefaultsUbuntuNoRepo(t *testing.T) {
	var gotImage interface{}
	var gotUserData string
	defer withRawFakeHetzner(t, func(w http.ResponseWriter, r *http.Request) {
		var body map[string]interface{}
		_ = json.NewDecoder(r.Body).Decode(&body)
		gotImage = body["image"]
		gotUserData, _ = body["user_data"].(string)
		_, _ = w.Write([]byte(`{"server":{"id":4242,"public_net":{"ipv4":{"ip":"203.0.113.7"}}}}`))
	})()
	m, _ := NewCloudDeployManager(".")
	if _, _, err := m.hetznerCreateServerCustom("tok", "box", "starter", "eu", "", ""); err != nil {
		t.Fatalf("create: %v", err)
	}
	if s, _ := gotImage.(string); s != "ubuntu-22.04" {
		t.Fatalf("no imageId ⇒ base image ubuntu-22.04, got %#v", gotImage)
	}
	if strings.Contains(gotUserData, "git clone") {
		t.Fatalf("no repoUrl ⇒ no clone line; got:\n%s", gotUserData)
	}
}

// The headline ask: create → stop → reactivate, end to end, $0.
func TestByoLifecycle_CreateStopReactivate(t *testing.T) {
	f := newFakeHetzner(t)
	withFakeHetzner(t, f)
	m, _ := NewCloudDeployManager(".")

	_, id, err := m.hetznerCreateServerCustom("tok", "box", "starter", "eu", "", "")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	snap, err := m.hetznerStopServer("tok", id, "yaver-stop-"+id)
	if err != nil {
		t.Fatalf("stop: %v", err)
	}
	if snap != "1" {
		t.Fatalf("stop should return snapshot image id 1, got %q", snap)
	}
	if _, _, err := m.hetznerStartServer("tok", "box-resumed", "starter", "eu", snap); err != nil {
		t.Fatalf("reactivate: %v", err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.created || !f.snapshot || !f.deleted {
		t.Fatalf("full lifecycle not exercised: created=%v snapshot=%v deleted=%v", f.created, f.snapshot, f.deleted)
	}
}

// SECURITY: a payload must NOT be able to smuggle a Hetzner credential —
// the token only ever comes from the agent's own encrypted vault
// (accountField). The verb schema therefore exposes no token/secret
// field (additionalProperties:false locks it shut).
func TestCloudProvision_SchemaHasNoCredentialField(t *testing.T) {
	opsRegistryMu.RLock()
	spec, ok := opsRegistry["cloud_provision"]
	opsRegistryMu.RUnlock()
	if !ok {
		t.Fatal("cloud_provision verb not registered")
	}
	schema := spec.Schema
	props, _ := schema["properties"].(map[string]interface{})
	for k := range props {
		lk := strings.ToLower(k)
		if strings.Contains(lk, "token") || strings.Contains(lk, "secret") || strings.Contains(lk, "password") || lk == "fields" {
			t.Fatalf("cloud_provision schema exposes a credential field %q — a payload could smuggle a token; tokens must come ONLY from the vault", k)
		}
	}
	if ap, _ := schema["additionalProperties"].(bool); ap {
		t.Fatal("cloud_provision schema must set additionalProperties:false so unknown (token-bearing) fields are rejected")
	}
}

func TestGoldenImageCache_RoundTrip(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if got := readGoldenImage("hetzner"); got != "" {
		t.Fatalf("fresh cache should be empty, got %q", got)
	}
	if err := writeGoldenImage("hetzner", "12345"); err != nil {
		t.Fatalf("writeGoldenImage: %v", err)
	}
	if got := readGoldenImage("hetzner"); got != "12345" {
		t.Fatalf("want 12345, got %q", got)
	}
}

func TestCloudBake_DryRunByDefault(t *testing.T) {
	t.Setenv("YAVER_CLOUD_STOPSTART_LIVE", "")
	res := opsCloudBakeHandler(OpsContext{}, json.RawMessage(`{"serverId":"4242","confirm":true}`))
	mm, _ := res.Initial.(map[string]interface{})
	if !res.OK || mm == nil || mm["dryRun"] != true {
		t.Fatalf("bake must be dry-run without the live flag, got %+v", res)
	}
}

// Bake-once: with a cached golden image, provision prefers it (fast boot)
// instead of ubuntu — visible in the dry-run plan.
func TestCloudProvision_PrefersGoldenImage(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("YAVER_CLOUD_STOPSTART_LIVE", "")
	if err := writeGoldenImage("hetzner", "99777"); err != nil {
		t.Fatalf("seed golden: %v", err)
	}
	res := opsCloudProvisionHandler(OpsContext{}, json.RawMessage(`{"plan":"starter","confirm":true}`))
	mm, _ := res.Initial.(map[string]interface{})
	plan, _ := mm["plan"].(string)
	if !strings.Contains(plan, "99777") || !strings.Contains(plan, "golden") {
		t.Fatalf("provision should prefer the baked golden image 99777; plan=%q", plan)
	}
}

// SECURITY: BYO mutate verbs are owner-only — a scoped guest (or any
// non-owner) must not be able to spend the host's Hetzner.
func TestByoVerbs_AreOwnerOnly(t *testing.T) {
	for _, name := range []string{"cloud_provision", "cloud_snapshots", "cloud_bake", "cloud_reconcile"} {
		opsRegistryMu.RLock()
		spec, ok := opsRegistry[name]
		opsRegistryMu.RUnlock()
		if !ok {
			t.Fatalf("%s verb not registered", name)
		}
		if spec.AllowGuest {
			t.Fatalf("%s must be AllowGuest:false (owner-only) — never let a guest spend the host's Hetzner", name)
		}
	}
}
