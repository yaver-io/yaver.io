package main

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func decodeMCPJSONResult(t *testing.T, result interface{}, out interface{}) {
	t.Helper()
	payload, ok := result.(map[string]interface{})
	if !ok {
		t.Fatalf("unexpected MCP result type %T", result)
	}
	if isErr, _ := payload["isError"].(bool); isErr {
		t.Fatalf("unexpected MCP error result: %#v", result)
	}
	content, ok := payload["content"].([]map[string]interface{})
	if !ok || len(content) == 0 {
		t.Fatalf("missing MCP content: %#v", result)
	}
	text, _ := content[0]["text"].(string)
	if err := json.Unmarshal([]byte(text), out); err != nil {
		t.Fatalf("decode MCP json content: %v\ntext=%s", err, text)
	}
}

func TestMCPPhoneProjectExportIncludesRequestedOptions(t *testing.T) {
	setupPhoneTestHome(t)
	p, err := CreatePhoneProject(PhoneCreateSpec{Name: "MCP Export", Template: "todos"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	handled, result := dispatchPhoneMCP(nil, "phone_project_export", json.RawMessage(
		`{"slug":"`+p.Slug+`","include_data":true,"containerize":true}`))
	if !handled {
		t.Fatal("expected phone_project_export to be handled")
	}
	var out struct {
		Slug         string `json:"slug"`
		Bytes        int    `json:"bytes"`
		IncludeData  bool   `json:"include_data"`
		Containerize bool   `json:"containerize"`
		TarballB64   string `json:"tarball_b64"`
	}
	decodeMCPJSONResult(t, result, &out)
	if out.Slug != p.Slug {
		t.Fatalf("slug mismatch: got %q want %q", out.Slug, p.Slug)
	}
	if !out.IncludeData || !out.Containerize {
		t.Fatalf("expected include_data and containerize to round-trip: %+v", out)
	}
	data, err := decodePhoneTarballB64(out.TarballB64)
	if err != nil {
		t.Fatalf("decode tarball_b64: %v", err)
	}
	for _, name := range []string{"local.db", "Dockerfile", "docker-compose.yml", ".env.example"} {
		if !bundleContainsFile(t, data, name) {
			t.Fatalf("expected exported bundle to include %s", name)
		}
	}
}

func TestMCPPhoneProjectImportRestoresLocalSandbox(t *testing.T) {
	setupPhoneTestHome(t)
	p, err := CreatePhoneProject(PhoneCreateSpec{Name: "MCP Import Source", Template: "todos"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	bundle, err := ExportPhoneProjectWithOptions(p.Slug, PhoneExportOptions{
		IncludeData:  true,
		Containerize: true,
	})
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	_ = DeletePhoneProject(p.Slug)

	handled, result := dispatchPhoneMCP(nil, "phone_project_import", json.RawMessage(
		`{"tarball_b64":"`+encodePhoneTarballB64(bundle)+`","on_conflict":"reject"}`))
	if !handled {
		t.Fatal("expected phone_project_import to be handled")
	}
	var out struct {
		Slug      string        `json:"slug"`
		LocalOnly bool          `json:"local_only"`
		Project   *PhoneProject `json:"project"`
		Stats     *PhoneStats   `json:"stats"`
	}
	decodeMCPJSONResult(t, result, &out)
	if out.Slug == "" || out.Project == nil {
		t.Fatalf("expected imported project details, got %+v", out)
	}
	if !out.LocalOnly {
		t.Fatalf("expected local_only=true, got %+v", out)
	}
	if _, err := LoadPhoneProject(out.Slug); err != nil {
		t.Fatalf("expected imported project to exist locally: %v", err)
	}
}

func TestMCPPhoneProjectPushTriggersRemoteReceive(t *testing.T) {
	setupPhoneTestHome(t)
	src, err := CreatePhoneProject(PhoneCreateSpec{Name: "MCP Push Source", Template: "todos"})
	if err != nil {
		t.Fatalf("create source: %v", err)
	}
	targetSrv := &HTTPServer{token: "target-token"}
	target := httptest.NewServer(http.HandlerFunc(targetSrv.auth(targetSrv.handlePhoneReceive)))
	defer target.Close()

	handled, result := dispatchPhoneMCP(&HTTPServer{}, "phone_project_push", json.RawMessage(
		`{"slug":"`+src.Slug+`","target_base_url":"`+target.URL+`","target_auth_token":"target-token","target_slug":"remote-copy","include_data":true,"containerize":true}`))
	if !handled {
		t.Fatal("expected phone_project_push to be handled")
	}
	var out struct {
		SourceSlug    string `json:"source_slug"`
		TargetSlug    string `json:"target_slug"`
		TargetBaseURL string `json:"target_base_url"`
		BrowseURL     string `json:"browse_url"`
		Pushed        bool   `json:"pushed"`
	}
	decodeMCPJSONResult(t, result, &out)
	if !out.Pushed || out.TargetSlug != "remote-copy" {
		t.Fatalf("unexpected push result: %+v", out)
	}
	if !strings.Contains(out.BrowseURL, "remote-copy") {
		t.Fatalf("expected browse url to point at target slug, got %+v", out)
	}
	remote, err := LoadPhoneProject("remote-copy")
	if err != nil {
		t.Fatalf("expected pushed project to land on target agent home: %v", err)
	}
	if remote.Stats == nil || remote.Stats.PerTable["todos"] < 1 {
		t.Fatalf("expected remote project data to survive push: %+v", remote.Stats)
	}
}

func encodePhoneTarballB64(data []byte) string {
	return base64.StdEncoding.EncodeToString(data)
}

func decodePhoneTarballB64(data string) ([]byte, error) {
	return base64.StdEncoding.DecodeString(data)
}
