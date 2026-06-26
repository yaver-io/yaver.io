package main

import (
	"context"
	"testing"
)

// mobile_deploy_to_phone must be advertised in the tools/list so agents can
// discover the one-shot deploy verb.
func TestMobileDeployToolRegistered(t *testing.T) {
	wrapper, ok := (&HTTPServer{}).getMCPToolsList().(map[string]interface{})
	if !ok {
		t.Fatalf("getMCPToolsList did not return a map wrapper")
	}
	tools, ok := wrapper["tools"].([]map[string]interface{})
	if !ok {
		t.Fatalf("tools key is not []map[string]interface{}")
	}
	found := false
	for _, tool := range tools {
		if name, _ := tool["name"].(string); name == "mobile_deploy_to_phone" {
			found = true
			// Sanity: it must declare an input schema with the key knobs.
			schema, _ := tool["inputSchema"].(map[string]interface{})
			props, _ := schema["properties"].(map[string]interface{})
			for _, want := range []string{"directory", "device_id", "platform", "plan_only"} {
				if _, ok := props[want]; !ok {
					t.Fatalf("mobile_deploy_to_phone missing input property %q", want)
				}
			}
			break
		}
	}
	if !found {
		t.Fatal("mobile_deploy_to_phone not found in getMCPToolsList()")
	}
}

// When pointed at a directory with no RN/Expo project, the chain must stop at
// the doctor step with an honest blocker — never claim success.
func TestMobileDeployNoProject(t *testing.T) {
	s := &HTTPServer{}
	res := s.mobileDeployToPhone(context.Background(), mobileDeployToPhoneArgs{
		Directory: t.TempDir(),
	})
	if res.OK || res.Done {
		t.Fatalf("expected failure for non-RN dir, got ok=%v done=%v", res.OK, res.Done)
	}
	if len(res.Steps) == 0 || res.Steps[0].Step != "doctor" || res.Steps[0].OK {
		t.Fatalf("expected a failed doctor step first, got %+v", res.Steps)
	}
	if res.NextAction == "" {
		t.Fatal("expected a next_action sentence for the human")
	}
}

// reloadError must distinguish a failed reload payload from a successful one.
func TestReloadErrorClassification(t *testing.T) {
	if got := reloadError(map[string]interface{}{"ok": true}); got != "" {
		t.Fatalf("ok reload should yield no error, got %q", got)
	}
	if got := reloadError(map[string]interface{}{"ok": false, "error": "boom"}); got != "boom" {
		t.Fatalf("failed reload should surface its error, got %q", got)
	}
	if got := reloadError(map[string]interface{}{"ok": false}); got == "" {
		t.Fatal("failed reload without an error string should still report a failure")
	}
}

// doctorWantsPrepare reads the doctor's ordered nextActions to decide whether
// dependency install is needed — verify it handles the native slice shape.
func TestDoctorWantsPrepare(t *testing.T) {
	yes := map[string]interface{}{
		"nextActions": []map[string]string{{"tool": "mobile_project_prepare", "reason": "x"}},
	}
	if !doctorWantsPrepare(yes) {
		t.Fatal("expected doctorWantsPrepare=true when prepare is in nextActions")
	}
	no := map[string]interface{}{
		"nextActions": []map[string]string{{"tool": "mobile_project_build", "reason": "x"}},
	}
	if doctorWantsPrepare(no) {
		t.Fatal("expected doctorWantsPrepare=false when prepare is absent")
	}
}
