package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The hosted tier introduces exactly one secret: the self-hosted
// Convex admin key. The privacy contract for it is simple and
// absolute — it lives ONLY in /etc/yaver/convex-selfhosted.json on the
// tenant's own box (root-only, written by Phase 1 cloud-init). It must
// never be:
//   - baked into the JS bundle handed to friends (Phase 3 keystone),
//   - templated into the generated deploy script,
//   - sent to central Convex.
// These tests pin those invariants. Live-box e2e is deliberately
// out of scope here (real cx42 spend) — see the Phase 5 task.

const sentinelAdminKey = "convex-selfhosted-ADMINKEY-SENTINEL-do-not-leak"

func writeHostedCred(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	f := filepath.Join(dir, "convex-selfhosted.json")
	body := `{"url":"https://box42.cloud.yaver.io/_convex-api","adminKey":"` + sentinelAdminKey + `"}`
	if err := os.WriteFile(f, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return f
}

// The Hermes/dev bundle env must carry the URL (so the app + friends'
// Hermes copies reach the backend) but NEVER the admin key — a guest
// app with the admin key could rewrite the host's whole database.
func TestHostedConvexBuildEnv_NeverLeaksAdminKey(t *testing.T) {
	t.Setenv("CONVEX_SELFHOSTED_FILE", writeHostedCred(t))

	env := hostedConvexBuildEnv(t.TempDir())
	if len(env) == 0 {
		t.Fatal("expected the URL to be injected from the hosted cred file")
	}
	joined := strings.Join(env, "\n")
	if !strings.Contains(joined, "https://box42.cloud.yaver.io/_convex-api") {
		t.Errorf("URL missing from bundle env: %v", env)
	}
	if strings.Contains(joined, sentinelAdminKey) {
		t.Fatalf("ADMIN KEY LEAKED into bundle env: %v", env)
	}
	for _, kv := range env {
		if strings.HasPrefix(kv, "EXPO_PUBLIC_") &&
			strings.Contains(strings.ToLower(kv), "admin") {
			t.Fatalf("admin-shaped value baked into public bundle env: %s", kv)
		}
	}
}

// The generated convex:selfhosted deploy script must resolve the admin
// key at RUNTIME from the on-box root-only file — never inline it (a
// script is logged, streamed to the UI, and may be persisted).
func TestSelfHostedDeployScript_AdminKeyResolvedAtRuntimeOnly(t *testing.T) {
	script, err := GenerateDeployScript(DeployScriptSpec{
		App: "myapp", Stack: "convex", Target: "selfhosted", Path: "/srv/yaver/workspace",
	})
	if err != nil {
		t.Fatalf("GenerateDeployScript(selfhosted): %v", err)
	}
	if strings.Contains(script, sentinelAdminKey) {
		t.Fatal("admin key literal found in deploy script — must be runtime-resolved")
	}
	// It must read the key from the on-box file at runtime, not bake it.
	for _, want := range []string{
		"/etc/yaver/convex-selfhosted.json",
		"jq -r .adminKey",
		"CONVEX_SELF_HOSTED_ADMIN_KEY",
	} {
		if !strings.Contains(script, want) {
			t.Errorf("script should resolve the key at runtime via %q:\n%s", want, script)
		}
	}
	// The key must never be a script template variable (only the file
	// path + jq extraction may appear).
	if strings.Contains(script, "{{.AdminKey}}") || strings.Contains(script, "{{ .AdminKey }}") {
		t.Error("admin key must not be a deploy-template variable")
	}
}
