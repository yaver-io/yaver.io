# Remained

## Just Landed

- Guest/resource sharing now has explicit host-approved presets:
  - `machine-only`
  - `machine-with-host-keys`
  - `desktop-control`
  - `desktop-control-with-host-keys`
- Infra grants now store future remote-control capability flags:
  - `allowDesktopControl`
  - `allowBrowserControl`
  - `allowTunnelForward`
- Agent-side guest policy now infers those presets safely and injects them into the guest security prompt.
- MCP `guest_config`, CLI `yaver guests config`, mobile guest config UI, Convex guest config APIs, and docs all understand the new sharing model.

## Next Work

- Build the real remote desktop transport on top of the new policy layer.
  - Start with host-approved tunnel-backed RFB/noVNC or equivalent browser-safe stream path.
  - Require explicit host session approval per desktop session, not just per guest grant.
  - Keep desktop control, browser automation, and raw tunnel access separately revocable.
- Add backend tests for `resourcePreset` validation conflicts and default inference in Convex.
- Add end-to-end guest sharing tests across two devices proving:
  - `machine-only` never exposes host API keys
  - `desktop-control` does not implicitly enable tunnel forwarding
  - device-scoped grants stay device-scoped
- Add web dashboard UI for guest resource sharing.
  - The client types are updated, but there is not yet a dedicated dashboard editor for these new preset fields.
- Wire future remote desktop session creation through the same guest policy checks before exposing any VNC/RFB/WebRTC endpoint.

## Still Dirty Locally

- Uncommitted local files remain outside this commit scope:
  - `desktop/agent/agent_mesh_remote.go`
  - `desktop/agent/agent_mode.go`
  - `desktop/agent/completion.go`
  - `desktop/agent/exec_cmd.go`
  - `desktop/agent/httpserver.go`
  - `desktop/agent/mcp_workspace.go`
  - `desktop/agent/remote_yaver.go`
  - `desktop/agent/session_cmd.go`
  - `desktop/agent/stream_cmd.go`
  - `desktop/agent/tasks.go`
  - `desktop/agent/template.go`
  - `mobile/src/lib/quic.ts`
  - `scripts/test-yaver-to-yaver-local.sh`
  - `web/next-env.d.ts`
- Untracked local files remain outside this commit scope:
  - `desktop/agent/agent_mesh_remote_test.go`
  - `desktop/agent/agent_mode_template_test.go`
  - `desktop/agent/code_cmd.go`
  - `desktop/agent/graph_slice.go`
  - `desktop/agent/graph_slice_test.go`
  - `desktop/agent/template_test.go`

## Verification Run For This Slice

- `go test -run 'TestGuestResourcePresetInference|TestGuestPromptPrefixIncludesResourcePolicies|TestCollectAPIKeysForGuestBlocksHostKeysByDefault|TestTaskEnvStripsSharedSecretEnvForGuests|TestTaskEnvKeepsHostKeysWhenExplicitlyAllowed' ./...` in `desktop/agent`
- `npx tsc --noEmit` in `mobile`
- `npx tsc --noEmit` in `web`
