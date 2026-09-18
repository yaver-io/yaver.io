package main

import (
	"strings"
	"testing"
)

func TestDroidResolveTapTargetPrefersExactResourceID(t *testing.T) {
	nodes := []droidUINode{
		{Text: "Send later", Bounds: "[0,0][100,100]", X: 50, Y: 50, W: 100, H: 100, Enabled: true, Clickable: true},
		{ResourceID: "io.example:id/send", Bounds: "[100,0][200,100]", X: 150, Y: 50, W: 100, H: 100, Enabled: true, Clickable: true},
	}

	got, err := droidResolveTapTarget(nodes, "io.example:id/send")
	if err != nil {
		t.Fatal(err)
	}
	if got.X != 150 || got.Y != 50 {
		t.Fatalf("resolved (%d,%d), want (150,50)", got.X, got.Y)
	}
}

func TestDroidResolveTapTargetPrefersClickableExactLabel(t *testing.T) {
	nodes := []droidUINode{
		{Text: "Continue", X: 50, Y: 50, W: 100, H: 40, Enabled: true},
		{Text: "Continue", X: 50, Y: 120, W: 100, H: 40, Enabled: true, Clickable: true},
	}

	got, err := droidResolveTapTarget(nodes, "continue")
	if err != nil {
		t.Fatal(err)
	}
	if got.Y != 120 {
		t.Fatalf("resolved Y=%d, want clickable node at 120", got.Y)
	}
}

func TestDroidResolveTapTargetRefusesAmbiguousLabel(t *testing.T) {
	nodes := []droidUINode{
		{Text: "Like", X: 50, Y: 50, W: 100, H: 40, Enabled: true, Clickable: true},
		{Text: "Like", X: 50, Y: 150, W: 100, H: 40, Enabled: true, Clickable: true},
	}

	_, err := droidResolveTapTarget(nodes, "Like")
	if err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("error = %v, want an ambiguity refusal", err)
	}
}

func TestDroidResolveTapTargetAllowsDuplicateTreeNodesAtSameCenter(t *testing.T) {
	nodes := []droidUINode{
		{Text: "Next", X: 80, Y: 160, W: 120, H: 48, Enabled: true, Clickable: true},
		{Description: "Next", X: 80, Y: 160, W: 120, H: 48, Enabled: true, Clickable: true},
	}

	got, err := droidResolveTapTarget(nodes, "Next")
	if err != nil {
		t.Fatal(err)
	}
	if got.X != 80 || got.Y != 160 {
		t.Fatalf("resolved (%d,%d), want (80,160)", got.X, got.Y)
	}
}

func TestDroidResolveTapTargetRejectsDisabledAndMissingTargets(t *testing.T) {
	nodes := []droidUINode{{Text: "Submit", X: 50, Y: 50, W: 100, H: 40, Enabled: false, Clickable: true}}
	if _, err := droidResolveTapTarget(nodes, "Submit"); err == nil {
		t.Fatal("disabled node unexpectedly resolved")
	}
	if _, err := droidResolveTapTarget(nodes, ""); err == nil {
		t.Fatal("empty target unexpectedly resolved")
	}
}

func TestDroidMCPToolsExposeStructuredRemoteControl(t *testing.T) {
	wrapper := (&HTTPServer{}).getMCPToolsList().(map[string]interface{})
	tools := wrapper["tools"].([]map[string]interface{})
	wanted := map[string]bool{
		"droid_status":      false,
		"droid_frame":       false,
		"droid_input":       false,
		"droid_ui_elements": false,
		"droid_launch":      false,
	}
	for _, tool := range tools {
		name, _ := tool["name"].(string)
		if _, ok := wanted[name]; !ok {
			continue
		}
		wanted[name] = true
		schema := tool["inputSchema"].(map[string]interface{})
		properties := schema["properties"].(map[string]interface{})
		if _, ok := properties["device_id"]; !ok {
			t.Errorf("%s is not remote-capable: device_id missing", name)
		}
		if name == "droid_input" {
			typeSchema := properties["type"].(map[string]interface{})
			enums := typeSchema["enum"].([]string)
			foundTarget := false
			for _, value := range enums {
				foundTarget = foundTarget || value == "target"
			}
			if !foundTarget {
				t.Error("droid_input does not advertise named target actions")
			}
		}
	}
	for name, found := range wanted {
		if !found {
			t.Errorf("MCP tool %s is missing", name)
		}
	}
}
