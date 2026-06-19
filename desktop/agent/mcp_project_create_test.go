package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMCPSelfHostedProjectCreateGeneratesFullMonorepo(t *testing.T) {
	t.Setenv("YAVER_DISABLE_WIZARD_AUTOINIT", "1")

	parent, err := os.MkdirTemp("", "yaver-mcp-project-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(parent) })

	raw := json.RawMessage(`{
		"name": "Pocket CRM",
		"slug": "pocket-crm",
		"description": "A mobile-first CRM for small teams.",
		"domain": "",
		"gitProvider": "none",
		"parentDir": "` + parent + `"
	}`)
	result := (&HTTPServer{}).mcpProjectNewQuick(raw)

	content, ok := result.(map[string]interface{})["content"].([]map[string]interface{})
	if !ok || len(content) == 0 {
		t.Fatalf("unexpected MCP result shape: %#v", result)
	}
	if isErr, _ := result.(map[string]interface{})["isError"].(bool); isErr {
		t.Fatalf("mcpProjectNewQuick returned error: %v", content[0]["text"])
	}
	var generated ProjectGenerationResult
	if err := json.Unmarshal([]byte(content[0]["text"].(string)), &generated); err != nil {
		t.Fatalf("unmarshal generated result: %v\n%s", err, content[0]["text"])
	}
	if !generated.OK {
		t.Fatalf("expected ok result: %#v", generated)
	}

	expectedFiles := []string{
		"package.json",
		"apps/web/package.json",
		"apps/web/wrangler.toml",
		"apps/landing/index.html",
		"apps/mobile/app.json",
		"apps/mobile/App.tsx",
		"backend/package.json",
		"backend/convex/schema.ts",
		"packages/shared/index.ts",
		".yaver/config.yaml",
		".yaver/services.yaml",
		"legal/app-review.md",
	}
	for _, rel := range expectedFiles {
		if _, err := os.Stat(filepath.Join(generated.Directory, rel)); err != nil {
			t.Fatalf("expected generated file %s: %v", rel, err)
		}
	}

	wrangler, err := os.ReadFile(filepath.Join(generated.Directory, "apps/web/wrangler.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(wrangler), "workers_dev = true") {
		t.Fatalf("domain-less Cloudflare starter should use workers_dev=true:\n%s", wrangler)
	}

	mobileApp, err := os.ReadFile(filepath.Join(generated.Directory, "apps/mobile/app.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(mobileApp), `"bundleIdentifier": "com.myco.pocketcrm"`) {
		t.Fatalf("default iOS bundle id missing:\n%s", mobileApp)
	}
	if !strings.Contains(string(mobileApp), `"package": "com.myco.pocketcrm"`) {
		t.Fatalf("default Android package missing:\n%s", mobileApp)
	}

	if generated.YaverOnboarding == nil {
		t.Fatalf("expected yaverOnboarding guidance")
	}
	stack, _ := generated.YaverOnboarding["stack"].(map[string]interface{})
	if stack["backend"] != "backend/convex local dev and hosted Convex deploy" {
		t.Fatalf("unexpected onboarding stack: %#v", generated.YaverOnboarding)
	}
	if len(generated.NextSteps) == 0 || !strings.Contains(generated.NextSteps[0], "Self-hosted first") {
		t.Fatalf("expected self-hosted-first next step, got %#v", generated.NextSteps)
	}
}
