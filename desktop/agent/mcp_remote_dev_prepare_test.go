package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRemoteDevPrepareRequestDefaults(t *testing.T) {
	req := parseRemoteDevPrepareRequest(json.RawMessage(`{
		"targetDeviceId": "  magara  ",
		"repoUrl": "  https://github.com/example/app.git  ",
		"branch": " main ",
		"dir": " /home/kivi/Workspace/app ",
		"mobileDirectory": " mobile "
	}`))

	if !req.ConfigureCode {
		t.Fatal("ConfigureCode should default to true")
	}
	if !req.Verify {
		t.Fatal("Verify should default to true")
	}
	if req.TargetDeviceID != "magara" || req.RepoURL != "https://github.com/example/app.git" || req.Branch != "main" {
		t.Fatalf("expected trimmed string fields, got %#v", req)
	}
	if req.Dir != "/home/kivi/Workspace/app" || req.MobileDirectory != "mobile" {
		t.Fatalf("expected trimmed paths, got %#v", req)
	}
}

func TestRemoteDevPrepareRequestExplicitFalse(t *testing.T) {
	req := parseRemoteDevPrepareRequest(json.RawMessage(`{
		"configureCode": false,
		"verify": false
	}`))
	if req.ConfigureCode {
		t.Fatal("ConfigureCode should honor explicit false")
	}
	if req.Verify {
		t.Fatal("Verify should honor explicit false")
	}
}

func TestMCPRemoteDevelopmentToolSchemas(t *testing.T) {
	payload := (&HTTPServer{}).getMCPToolsList().(map[string]interface{})
	tools := payload["tools"].([]map[string]interface{})

	remotePrepare := findMCPToolForTest(t, tools, "remote_dev_prepare")
	props := mcpToolPropertiesForTest(t, remotePrepare)
	for _, key := range []string{"targetDeviceId", "repoUrl", "branch", "dir", "configureCode", "prepareMobile", "mobileDirectory", "verify", "dryRun"} {
		if _, ok := props[key]; !ok {
			t.Fatalf("remote_dev_prepare missing property %q", key)
		}
	}

	execTool := findMCPToolForTest(t, tools, "exec_command")
	if _, ok := mcpToolPropertiesForTest(t, execTool)["device_id"]; !ok {
		t.Fatal("exec_command missing device_id property")
	}

	listMachines := findMCPToolForTest(t, tools, "list_machines")
	if got := listMachines["description"]; !strings.Contains(got.(string), "agent_machine_inventory") {
		t.Fatalf("list_machines should document its compatibility alias, got %q", got)
	}

	createTask := findMCPToolForTest(t, tools, "create_task")
	createProps := mcpToolPropertiesForTest(t, createTask)
	for _, key := range []string{"device_id", "prompt", "work_dir", "placement_kind"} {
		if _, ok := createProps[key]; !ok {
			t.Fatalf("create_task missing property %q", key)
		}
	}
	for _, hidden := range []string{"sandbox_run", "sandbox_status", "sandbox_config", "sandbox_quickstart"} {
		for _, tool := range tools {
			if tool["name"] == hidden {
				t.Fatalf("%s should be hidden while app development is remote-box-first", hidden)
			}
		}
	}

	for _, name := range []string{"mobile_project_status", "mobile_project_prepare", "mobile_project_build"} {
		tool := findMCPToolForTest(t, tools, name)
		if _, ok := mcpToolPropertiesForTest(t, tool)["device_id"]; !ok {
			t.Fatalf("%s missing device_id property", name)
		}
	}
}

func TestFormatExecSnapshot(t *testing.T) {
	got := formatExecSnapshot(map[string]any{
		"status":   "completed",
		"exitCode": float64(0),
		"stdout":   "ok\n",
		"stderr":   "warn\n",
	})
	for _, want := range []string{"Status: completed", "Exit code: 0", "--- stdout ---", "ok", "--- stderr ---", "warn"} {
		if !strings.Contains(got, want) {
			t.Fatalf("formatted snapshot missing %q in:\n%s", want, got)
		}
	}
}

func findMCPToolForTest(t *testing.T, tools []map[string]interface{}, name string) map[string]interface{} {
	t.Helper()
	for _, tool := range tools {
		if tool["name"] == name {
			return tool
		}
	}
	t.Fatalf("tool %q not found", name)
	return nil
}

func mcpToolPropertiesForTest(t *testing.T, tool map[string]interface{}) map[string]interface{} {
	t.Helper()
	schema, ok := tool["inputSchema"].(map[string]interface{})
	if !ok {
		t.Fatalf("tool %v missing inputSchema", tool["name"])
	}
	props, ok := schema["properties"].(map[string]interface{})
	if !ok {
		t.Fatalf("tool %v missing inputSchema.properties", tool["name"])
	}
	return props
}
