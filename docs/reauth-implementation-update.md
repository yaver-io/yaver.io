# Yaver Reauth Implementation Update

Date: 2026-05-02

This note captures the reauth / recovery work implemented recently in
the codebase. It is a status update, not a design spec.

## Implemented

### 1. First-pass MCP coverage for remote Yaver reauth

Added MCP tools for remote recovery and related auth flows:

- `recovery_transport_status`
- `recovery_target_status`
- `recovery_target_start`
- `device_reauth_status`
- `device_reauth_start`
- `device_reauth_wait`
- `runner_auth_browser_start`
- `runner_auth_browser_status`
- `runner_auth_browser_submit_code`
- `runner_auth_browser_cancel`

Primary code:

- `desktop/agent/mcp_auth_recovery.go`
- `desktop/agent/mcp_tools.go`
- `desktop/agent/httpserver.go`

What this enabled:

- owned-device reauth through MCP
- explicit-target recovery without local Yaver sign-in
- wrapped runner browser-auth for Claude Code / Codex through MCP

### 2. Alias-aware remote-device resolution

Extended shared device resolution so remote-device selectors can use:

- device id
- device id prefix
- device name
- alias
- `@alias`

Primary code:

- `desktop/agent/main.go`
- `desktop/agent/agent_mesh_remote.go`
- `desktop/agent/mcp_auth_recovery.go`
- `desktop/agent/ssh_alias_test.go`

This matters for:

- `yaver ssh`
- MCP remote recovery
- shared remote proxy flows

### 3. Web + mobile SSH action for alias machines

Added UI actions that expose the alias-aware SSH command instead of
only browser/mobile PTY shell:

- web dashboard device cards now have an `SSH` action that copies the
  command
- mobile device details now have a `Copy SSH` action

The copied command prefers:

- `yaver ssh @alias`

and falls back to:

- `yaver ssh <device-id-prefix>`

Primary code:

- `web/components/dashboard/DevicesView.tsx`
- `mobile/src/components/DeviceDetailsModal.tsx`

### 4. Mobile Tasks screen fixes

Implemented two mobile Tasks-screen fixes:

- wrap mode enabled by default
- fixed the `+` FAB touch/layering issue after secondary connect

Primary code:

- `mobile/app/(tabs)/tasks.tsx`

### 5. Recovery-session foundation for async reauth

Implemented the first real session-bound recovery layer for async
reauth modes.

What exists now:

- new in-memory recovery session registry
- `GET /auth/recover/session`
- `pair` and `device-code` recovery now return:
  - `recovery_id`
  - `wait_token`
  - `next_action`
  - `state`
- `direct` recovery remains synchronous

Primary code:

- `desktop/agent/auth_recover_session.go`
- `desktop/agent/auth_recover.go`
- `desktop/agent/auth_bootstrap.go`
- `desktop/agent/httpserver.go`

State model currently used:

- `started`
- `awaiting_pair_submit`
- `awaiting_browser_oauth`
- `applying_token`
- `recovered`
- `failed`
- `expired`

### 6. MCP waiters moved toward session-based recovery

Updated the MCP wait path so:

- `device_reauth_wait` can now use `recovery_id` + `wait_token`
- `recovery_target_wait` was added for explicit-target recovery

Compatibility behavior:

- old `device_id`-only waiting still exists as a probe-based fallback
- new callers should prefer recovery-session waiting

Primary code:

- `desktop/agent/mcp_auth_recovery.go`
- `desktop/agent/mcp_tools.go`
- `desktop/agent/httpserver.go`

### 7. Test coverage added for the new recovery-session path

Added focused tests covering:

- device-code recovery start returns session fields
- pair recovery returns session fields
- pair recovery session status reaches `recovered`
- alias selector normalization for SSH

Primary code:

- `desktop/agent/auth_recover_test.go`
- `desktop/agent/ssh_alias_test.go`

Verified recently:

```bash
cd desktop/agent
go test . -run 'TestAuthRecover(HostTokenCanStartDeviceCode|PairReturnsRecoverySessionAndStatus|PairReusesExistingWindow)$'
```

## Important current limitations

These are still true after the recent implementation work:

- `device_reauth_wait` still supports an older probe-based fallback;
  not every caller is on session-token waiting yet
- runner browser-auth is not yet fully normalized onto the same
  `recovery_id` / `wait_token` contract
- the recovery session registry is currently in-memory only
- broad package-wide Go test runs in `desktop/agent` still hit
  unrelated pre-existing repo issues outside this reauth work

## Related docs

- `docs/reauth-recovery-report.md`
- `docs/reauth-recommendations.md`
