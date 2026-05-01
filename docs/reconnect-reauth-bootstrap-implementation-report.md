# Reconnect, Re-auth, and Bootstrap Implementation Report

Date: 2026-05-01

## Purpose

This report explains:

- what was changed
- why it was changed
- how the new reconnect / re-auth model works
- what each lifecycle state means
- what is still intentionally not solved yet

This is the implementation companion to:

- [docs/reconnect-reauth-bootstrap-audit.md](/Users/kivanccakmak/Workspace/yaver.io/docs/reconnect-reauth-bootstrap-audit.md)

The audit identifies the gaps. This report describes the hardening work that has now been implemented.

## Problem Statement

The reconnect flow had repeated false positives:

- mobile or web could show a device as effectively recoverable
- bootstrap devices could be mislabeled as offline
- relay-only bootstrap reclaim could fail even though the box was physically up
- clients were deriving state from stale device-row metadata instead of one canonical agent truth

The biggest architectural problem was that lifecycle meaning was split across:

- `needsAuth`
- `authExpired`
- `/health`
- `/info`
- stale heartbeat metadata
- relay presence
- client-specific inference

That made reconnect behavior inconsistent between:

- mobile Tasks device list
- mobile Devices list
- web device list
- connected web panels

## Core Design Decision

The fix is protocol-first:

- the Go agent is now the source of truth for reconnect lifecycle
- mobile and web consume that lifecycle contract
- clients only derive the final `connected` state locally, because that is client-session state, not machine state

## Canonical Lifecycle Model

The canonical agent-reported lifecycle states are:

- `bootstrap`
- `yaver-auth-expired`
- `ready-to-connect`

The client-local state remains:

- `connected`

And the client can still fall back to:

- `offline`

when no agent lifecycle could be proven at all.

## State Semantics

### `offline`

Meaning:

- no direct or relay `/info` proof
- no current trustworthy sign that the recovery surface is alive

Expected UX:

- do not imply reconnect or re-auth will work immediately

### `bootstrap`

Meaning:

- bootstrap HTTP server is alive
- agent is not yet usable as a normal Yaver agent
- only recovery / claim flows are valid

Subcases:

- reclaimable bootstrap
- fresh bootstrap

The agent now distinguishes these through lifecycle metadata:

- `supportsOwnerClaim`
- `ownerClaimReady`
- `requiresFirstPair`
- `recoveryMode`

### `yaver-auth-expired`

Meaning:

- normal agent is up
- its own auth is stale
- `/auth/recover` is the correct repair path

### `ready-to-connect`

Meaning:

- machine is not in bootstrap
- auth is not expired
- a normal attach should work

### `connected`

Meaning:

- a specific mobile or web client is actively attached

This remains local to the client because the agent cannot know which UI currently considers itself attached.

## Protocol Changes

### New Agent Lifecycle Contract

Implemented in:

- [desktop/agent/device_lifecycle.go](/Users/kivanccakmak/Workspace/yaver.io/desktop/agent/device_lifecycle.go)

The agent now returns:

- `lifecycleState`
- `lifecycle`

on both bootstrap and normal agent surfaces.

Current shape:

- `state`
- `usable`
- `recoverable`
- `recoveryMode`
- `supportsOwnerClaim`
- `ownerClaimReady`
- `requiresFirstPair`

### Authenticated Agent Surfaces

Updated:

- [desktop/agent/httpserver.go](/Users/kivanccakmak/Workspace/yaver.io/desktop/agent/httpserver.go)

Both `/health` and `/info` now include canonical lifecycle information.

### Bootstrap Agent Surfaces

Updated:

- [desktop/agent/auth_bootstrap.go](/Users/kivanccakmak/Workspace/yaver.io/desktop/agent/auth_bootstrap.go)

Bootstrap `/health` and `/info` now expose the same lifecycle contract, with bootstrap-specific metadata.

## Recovery Path Hardening

### `owner-claim`

Implementation still lives in:

- [desktop/agent/auth_owner_claim.go](/Users/kivanccakmak/Workspace/yaver.io/desktop/agent/auth_owner_claim.go)

But it now has dedicated tests:

- [desktop/agent/auth_owner_claim_test.go](/Users/kivanccakmak/Workspace/yaver.io/desktop/agent/auth_owner_claim_test.go)

The tests cover:

- fresh bootstrap with no `device_id`
- ownership mismatch
- missing active pair session
- successful token splice into the active pair session

This matters because `owner-claim` is the main relay-only bootstrap reclaim path for previously owned devices.

### Lifecycle Tests

Added:

- [desktop/agent/device_lifecycle_test.go](/Users/kivanccakmak/Workspace/yaver.io/desktop/agent/device_lifecycle_test.go)

These tests cover:

- fresh bootstrap
- previously owned bootstrap with reclaim support
- authenticated ready state
- auth-expired state

## Mobile Changes

### Canonical Lifecycle Consumption

Updated:

- [mobile/src/lib/deviceStatus.ts](/Users/kivanccakmak/Workspace/yaver.io/mobile/src/lib/deviceStatus.ts)

Mobile now prefers:

- `info.lifecycle.state`
- then `info.lifecycleState`

before using older fallback hints such as:

- `needsAuth`
- `authExpired`
- stale transport heuristics

### Bootstrap Relay Recovery Bug Fix

Updated:

- [mobile/src/context/DeviceContext.tsx](/Users/kivanccakmak/Workspace/yaver.io/mobile/src/context/DeviceContext.tsx)

Important real bug fixed:

- relay bootstrap probes inside `recoverBootstrapDevice()` were fetching relay `/info` without the required auth headers

That meant:

- the device could be up
- relay path could exist
- but bootstrap recovery would still fail before the correct reclaim flow

That is now fixed.

### Recovery Routing

Mobile recovery now does a fresh lifecycle probe before deciding whether to:

- run bootstrap reclaim flow
- run normal re-auth flow

This is more reliable than blindly trusting the stale device row.

## Web Changes

### Canonical Lifecycle Consumption

Updated:

- [web/lib/agent-client.ts](/Users/kivanccakmak/Workspace/yaver.io/web/lib/agent-client.ts)
- [web/lib/use-devices.ts](/Users/kivanccakmak/Workspace/yaver.io/web/lib/use-devices.ts)
- [web/app/dashboard/page.tsx](/Users/kivanccakmak/Workspace/yaver.io/web/app/dashboard/page.tsx)
- [web/components/dashboard/DevicesView.tsx](/Users/kivanccakmak/Workspace/yaver.io/web/components/dashboard/DevicesView.tsx)

Web now reads lifecycle state from the agent payload first.

### Connected Panels

Connected panels that previously gated off raw `connectedDevice.needsAuth` now use the lifecycle interpretation instead.

This reduces stale-row contamination after recovery or reconnect transitions.

## What Was Intentionally Not Changed

### Bootstrap Is Still Not a Full Agent

Bootstrap mode still only supports escalation surfaces such as:

- `/info`
- pairing endpoints
- `/auth/recover`
- `owner-claim`

It still does not expose the full agent experience.

That is intentional.

### `connected` Is Still Client-Local

The agent does not report `connected`, because that means:

- this exact client UI has an active session

That is not the same thing as machine lifecycle.

### Fresh Bootstrap Is Still Not Equivalent to Reclaimable Bootstrap

This is still an intentional distinction.

If the box has never been properly claimed before, reconnect/re-auth is not enough. First-pair flow is still required.

## Why This Design Is Better

Before:

- clients inferred too much
- bootstrap and auth-expired could be conflated
- `needsAuth` row state could override reality
- relay-only bootstrap reclaim had an avoidable probe bug

After:

- lifecycle comes from the agent first
- bootstrap reclaimability is explicit
- fresh bootstrap is explicit
- owner-claim has real coverage
- mobile relay bootstrap recovery is less brittle

## Remaining Gaps

This work improves correctness, but it does not close every gap from the audit.

Still remaining:

1. Full end-to-end relay-only bootstrap reclaim smoke coverage
2. More explicit handling of stale or merged-wrong device identities
3. Wider client test coverage for CTA routing by lifecycle state
4. Cleanup of all remaining UI copy that still implies bootstrap equals normal usability

## Recommended Next Steps

1. Add an end-to-end relay-only bootstrap reclaim smoke test
2. Add client tests for lifecycle-to-CTA mapping on mobile and web
3. Tighten stale device identity merge behavior
4. Continue replacing raw `needsAuth` UI logic with lifecycle-driven logic wherever still present

## Summary

The main improvement is not cosmetic.

It is that reconnect and re-auth now have a more canonical protocol center:

- the agent tells clients what lifecycle state the box is in
- clients stop guessing first
- bootstrap reclaim vs first-pair is explicit
- `owner-claim` now has dedicated coverage

That should make future fixes more reliable, because the system is moving away from UI-specific heuristics and toward protocol truth.
