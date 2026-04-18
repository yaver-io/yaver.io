package main

// mcp_remote_proxy_test.go — unit tests for the MCP remote-proxy helpers.
//
// We exercise the three safety rails separately from the HTTP path so
// these tests stay fast and deterministic:
//
//   1. refuseRemoteLayer4 blocks device_id on every Layer-4 tool.
//   2. refuseRemoteLayer4 is a no-op for non-Layer-4 tools.
//   3. proxyToDevice returns errProxyLocal when device_id is empty.
//
// End-to-end HTTP proxying against two running agents is covered by the
// existing agent_mesh_remote_test.go / morning_p2p_test.go suite; we don't
// duplicate that plumbing here.

import (
	"context"
	"errors"
	"testing"
)

func TestRefuseRemoteLayer4_BlocksAllLayer4Tools(t *testing.T) {
	t.Parallel()
	for tool := range layer4Tools {
		tool := tool
		t.Run(tool, func(t *testing.T) {
			t.Parallel()
			if err := refuseRemoteLayer4(tool, "some-device-id"); !errors.Is(err, errLayer4Remote) {
				t.Fatalf("layer-4 tool %q must refuse device_id, got err=%v", tool, err)
			}
		})
	}
}

func TestRefuseRemoteLayer4_AllowsEmptyDeviceID(t *testing.T) {
	t.Parallel()
	for tool := range layer4Tools {
		if err := refuseRemoteLayer4(tool, ""); err != nil {
			t.Fatalf("layer-4 tool %q with empty device_id must be allowed (local), got %v", tool, err)
		}
		if err := refuseRemoteLayer4(tool, "   "); err != nil {
			t.Fatalf("layer-4 tool %q with whitespace device_id must be allowed (local), got %v", tool, err)
		}
	}
}

func TestRefuseRemoteLayer4_IgnoresNonLayer4Tools(t *testing.T) {
	t.Parallel()
	nonLayer4 := []string{
		"mobile_project_build",
		"list_machines",
		"dev_reload",
		"xcodebuild_archive",
		"git_clone",
		"exec",
	}
	for _, tool := range nonLayer4 {
		if err := refuseRemoteLayer4(tool, "mac-mini-device-id"); err != nil {
			t.Fatalf("non-Layer-4 tool %q should allow device_id, got %v", tool, err)
		}
	}
}

func TestProxyToDevice_EmptyDeviceID_ReturnsErrProxyLocal(t *testing.T) {
	t.Parallel()
	_, _, err := proxyToDevice(context.Background(), "mobile_project_build", "", "POST", "/whatever", nil)
	if !errors.Is(err, errProxyLocal) {
		t.Fatalf("empty device_id must return errProxyLocal, got %v", err)
	}
	// Whitespace-only should behave identically.
	_, _, err = proxyToDevice(context.Background(), "mobile_project_build", "   ", "POST", "/whatever", nil)
	if !errors.Is(err, errProxyLocal) {
		t.Fatalf("whitespace device_id must return errProxyLocal, got %v", err)
	}
}

func TestProxyToDevice_Layer4Remote_ReturnsErrLayer4Remote(t *testing.T) {
	t.Parallel()
	_, _, err := proxyToDevice(context.Background(), "vault_get", "some-remote-device", "POST", "/vault/get", nil)
	if !errors.Is(err, errLayer4Remote) {
		t.Fatalf("vault_get with device_id must return errLayer4Remote, got %v", err)
	}
}

func TestProxyToDeviceJSON_Layer4Remote_PropagatesError(t *testing.T) {
	t.Parallel()
	_, err := proxyToDeviceJSON(context.Background(), "sdk_token_create", "some-remote-device", "POST", "/sdk/token", map[string]any{"label": "x"})
	if !errors.Is(err, errLayer4Remote) {
		t.Fatalf("expected errLayer4Remote, got %v", err)
	}
}

func TestLayer4Tools_NotEmpty(t *testing.T) {
	t.Parallel()
	// Guard against accidental deletion of the whole map — if someone
	// empties it, every secret tool becomes remote-executable. Fail loud.
	if len(layer4Tools) < 8 {
		t.Fatalf("layer4Tools must cover at least vault_*, sdk_token_*, env_*; got %d entries", len(layer4Tools))
	}
}
