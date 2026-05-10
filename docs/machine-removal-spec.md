# Machine Removal Spec

## Goal

Add a single owned-machine removal flow that is available from:

- the Go agent binary over authenticated HTTP
- MCP clients
- the web dashboard
- the mobile app

The flow must stay out of high-frequency UI surfaces. Permanent machine removal belongs in Settings / Danger Zone, not in the main device or infra surfaces.

## Non-Goals

- deleting source code repositories on the host
- deleting the user's Yaver account
- collapsing self-hosted host removal and managed-cloud destroy into one action

## Intent Split

Yaver must keep these actions distinct:

1. `Forget this machine`
Removes the device from the user's account/device list only.

2. `Remove Yaver from this host`
Owned self-hosted machine flow. Unregisters the device, removes auto-start, wipes `~/.yaver`, and stops the agent.

3. `Destroy managed cloud machine`
Managed Yaver Cloud flow. Deletes the provider VM/DNS and marks the cloud machine row stopped.

## UX Placement

### Web

- Place host removal in dashboard settings/account surfaces only.
- Do not surface permanent removal in the main device picker or the main infra hero area.

### Mobile

- Place host removal in `Settings > Danger Zone`.
- Do not use long-press on the device list for permanent host removal.

## Confirmation Rules

Permanent host removal requires:

- `confirm=true`
- typed phrase exactly equal to `delete my machine`

The UI must explain:

- Yaver data and services are removed
- the device is unregistered
- repositories are not deleted
- package-manager binary cleanup may still be manual

## Agent Contract

### HTTP

`POST /machine/remove`

Request:

```json
{
  "confirm": true,
  "phrase": "delete my machine"
}
```

Success response:

```json
{
  "ok": true,
  "action": "machine_remove",
  "phase": "scheduled",
  "manualSteps": [
    "npm uninstall -g yaver-cli",
    "rm /path/to/current/binary"
  ]
}
```

Error cases:

- `400 confirm=true required`
- `400 phrase must equal "delete my machine"`

### MCP

Tool name: `machine_remove`

Input:

```json
{
  "confirm": true,
  "phrase": "delete my machine"
}
```

Semantics match the HTTP endpoint.

## Host Removal Sequence

After the HTTP/MCP call returns success, the agent performs removal asynchronously:

1. unregister device from Convex using the saved auth token and device id
2. if unregister fails, best-effort mark the device offline
3. remove launchd/systemd/scheduled-task auto-start
4. remove `~/.yaver`
5. shut the agent down

This sequence intentionally does not delete repositories.

## Current Code Hooks

- device unregister: `backend/convex/http.ts` and `backend/convex/devices.ts`
- existing local uninstall/wipe primitives: `desktop/agent/main.go`, `desktop/agent/auth.go`, `desktop/agent/process_unix.go`
- mobile settings danger zone: `mobile/app/(tabs)/settings.tsx`
- web dashboard account/settings surface: `web/components/dashboard/AccountsView.tsx`

## Follow-Up Work

- add separate Settings UI for managed cloud destroy using the existing `cloudMachines.deprovision` backend path
- add richer streamed progress events for machine removal
- unify uninstall service naming drift (`yaver` vs `yaver-agent`) behind one shared helper
