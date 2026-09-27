package main

import "testing"

func TestMcpToolIsOwnerOnly(t *testing.T) {
	ownerOnly := []string{
		"robot_status", "robot_camera", "arm_movej", "arm_demo_start",
		"jig_scad", "circuit_simulate", "circuit_plot", "printer_print",
		"cad_render", "screw_cell_analytics", "appletv_remote_key",
		"appletv_now_playing", "capture_stream",
		"deploy_all", "deploy_run", "deploy_rollback", "cf_deploy",
		"mobile_platform_deploy", "fly_deploy", "railway_deploy",
		"pscale_deploy", "publish_run", "publish_submit", "publish_upload",
		"push_ios", "push_android",
	}
	for _, n := range ownerOnly {
		if !mcpToolIsOwnerOnly(n) {
			t.Errorf("expected %q to be owner-only", n)
		}
	}
	// These must stay PUBLIC — never caught by the hardware/deploy prefixes.
	public := []string{
		"create_task", "code_dev", "mobile_deploy_to_phone", "git_info",
		"vault_env", "ev_charging", "ev_networks",
		"hue_lights", "govee_control", "shelly_status", "sonos_discover",
		"ha_states", "mqtt_publish", "yaver_lazy_setup", "browser_open",
		"dogfood_status", "dogfood_rerender", "dogfood_reload",
	}
	for _, n := range public {
		if mcpToolIsOwnerOnly(n) {
			t.Errorf("expected %q to stay public (not owner-only)", n)
		}
	}
}

func TestFilterOwnerOnlyTools(t *testing.T) {
	tools := []map[string]interface{}{
		{"name": "create_task"},
		{"name": "robot_status"},
		{"name": "hue_lights"},
		{"name": "arm_movej"},
		{"name": "ev_charging"},
		{"name": "circuit_plot"},
		{"name": "mobile_deploy_to_phone"},
		{"name": "deploy_all"},
		{"name": "mobile_platform_deploy"},
		{"name": "publish_run"},
		{"name": "dogfood_status"},
	}

	// Owner sees everything.
	if got := filterOwnerOnlyTools(tools, true); len(got) != len(tools) {
		t.Fatalf("owner should see all %d tools, got %d", len(tools), len(got))
	}

	// Non-owner: hardware + deploy tools dropped; the rest kept.
	got := filterOwnerOnlyTools(tools, false)
	names := map[string]bool{}
	for _, tl := range got {
		names[tl["name"].(string)] = true
	}
	for _, hidden := range []string{"robot_status", "arm_movej", "circuit_plot", "deploy_all", "mobile_platform_deploy", "publish_run"} {
		if names[hidden] {
			t.Errorf("non-owner should NOT see %q", hidden)
		}
	}
	for _, shown := range []string{"create_task", "hue_lights", "ev_charging", "mobile_deploy_to_phone", "dogfood_status"} {
		if !names[shown] {
			t.Errorf("non-owner SHOULD see %q", shown)
		}
	}
}

func TestMcpToolDeniedByOwnerGate(t *testing.T) {
	// Public tool is never gated, regardless of owner verdict cache.
	if mcpToolDeniedByOwnerGate("create_task") != nil {
		t.Error("public tool must never be owner-gated")
	}
}
