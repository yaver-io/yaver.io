package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMicroserviceDetectProposesWorkerManifest(t *testing.T) {
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "package.json"), []byte(`{
  "scripts": {
    "worker": "node worker.js",
    "dev": "vite --host 0.0.0.0"
  }
}`), 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := MicroserviceDetect(repo, "billing-worker")
	if err != nil {
		t.Fatalf("detect: %v", err)
	}
	if res.Project != "billing-worker" {
		t.Fatalf("project = %q", res.Project)
	}
	if len(res.Manifest.Services) != 1 {
		t.Fatalf("services = %+v", res.Manifest.Services)
	}
	svc := res.Manifest.Services[0]
	if svc.Name != "worker" || svc.Command != "node" || strings.Join(svc.Args, " ") != "worker.js" {
		t.Fatalf("worker service wrong: %+v", svc)
	}
	if !svc.Durable {
		t.Fatalf("detected microservice should default durable")
	}
	if !strings.Contains(res.ManifestYAML, "yaver.companion.yaml") && !strings.Contains(res.ManifestYAML, "worker") {
		t.Fatalf("manifest yaml missing worker:\n%s", res.ManifestYAML)
	}
}

func TestMicroserviceWrapWritesExplicitDurableCompanion(t *testing.T) {
	repo := t.TempDir()
	req := MicroserviceWrapRequest{
		Repo:       repo,
		Project:    "api",
		Name:       "queue",
		Command:    "npm run worker",
		Port:       9090,
		EnvFile:    ".env",
		EnvVault:   "api",
		Write:      true,
		Overwrite:  true,
		AIWrap:     true,
		AIWorkKind: "analysis",
	}
	res, err := MicroserviceWrap(nil, req)
	if err != nil {
		t.Fatalf("wrap: %v", err)
	}
	if !res.Written {
		t.Fatalf("expected written result")
	}
	path := filepath.Join(repo, CompanionManifestName)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	text := string(data)
	for _, want := range []string{
		"project: api",
		"name: queue",
		"command: npm",
		"- run",
		"- worker",
		"durable: true",
		"vault: api",
		"file: .env",
		"ai_wrapper:",
		"work_kind: analysis",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("manifest missing %q:\n%s", want, text)
		}
	}
}

func TestMicroserviceMCPToolsAndDispatch(t *testing.T) {
	repo := t.TempDir()
	toolsAny := (&HTTPServer{}).getMCPToolsList()
	toolResult, ok := toolsAny.(map[string]interface{})
	if !ok {
		t.Fatalf("tools list type = %T", toolsAny)
	}
	rawTools, ok := toolResult["tools"].([]map[string]interface{})
	if !ok {
		t.Fatalf("tools payload type = %T (%+v)", toolResult["tools"], toolResult)
	}
	var found bool
	for _, tool := range rawTools {
		if tool["name"] == "microservice_wrap" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("microservice_wrap tool not registered")
	}

	args := map[string]interface{}{
		"repo":      repo,
		"project":   "api",
		"name":      "worker",
		"command":   "node worker.js",
		"write":     true,
		"overwrite": true,
	}
	raw, _ := json.Marshal(args)
	handled, result := dispatchMicroserviceMCP(nil, "microservice_wrap", raw)
	if !handled {
		t.Fatalf("microservice_wrap not handled")
	}
	var payload struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	b, _ := json.Marshal(result)
	if err := json.Unmarshal(b, &payload); err != nil {
		t.Fatalf("decode result: %v\n%s", err, string(b))
	}
	if payload.IsError {
		t.Fatalf("unexpected MCP error: %+v", payload)
	}
	if _, err := os.Stat(filepath.Join(repo, CompanionManifestName)); err != nil {
		t.Fatalf("manifest not written through MCP dispatch: %v", err)
	}
}

func TestMicroserviceHTTPWrapRouteWritesManifest(t *testing.T) {
	repo := t.TempDir()
	body := `{"repo":` + strconvQuote(repo) + `,"project":"api","name":"worker","command":"node worker.js","write":true,"overwrite":true}`
	req := httptest.NewRequest(http.MethodPost, "/microservices/wrap", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()

	(&HTTPServer{}).handleMicroserviceWrap(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var out MicroserviceWrapResult
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode response: %v\n%s", err, rec.Body.String())
	}
	if !out.OK || !out.Written {
		t.Fatalf("unexpected response: %+v", out)
	}
	if _, err := os.Stat(filepath.Join(repo, CompanionManifestName)); err != nil {
		t.Fatalf("manifest not written through HTTP handler: %v", err)
	}
}

func strconvQuote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
