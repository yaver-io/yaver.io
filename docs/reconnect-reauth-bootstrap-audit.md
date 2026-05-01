# Reconnect, Re-auth, and Bootstrap Audit

Date: 2026-05-01

## Scope

This audit covers Yaver's device recovery path across:

- Go agent protocol and handlers
- bootstrap HTTP server behavior
- mobile app reconnect / re-auth UX and state derivation
- web UI reconnect / re-auth UX and state derivation
- relay-assisted recovery paths

This is a coverage and gap audit, not a marketing summary. The goal is to identify where reconnect and re-auth are actually reliable, where they are only inferred, and where protocol or test coverage is still missing.

## Executive Summary

The reconnect story is improved, but it is not yet robust enough to call solved.

The biggest issue is that the system currently mixes:

- agent-reported facts
- client-side lifecycle inference
- relay-specific constraints
- bootstrap-only auth endpoints
- ownership semantics
- pair-session timing

That combination means mobile and web can still label a device as recoverable even when the protocol path that would make it recoverable is missing or under-tested.

The most important conclusions are:

1. `owner-claim` is now a critical recovery path, but it is under-tested.
2. bootstrap mode is only sufficient for auth escalation, not for general agent usability.
3. a truly fresh bootstrap box is not the same as a reclaimable bootstrap box.
4. mobile and web still derive lifecycle states partly heuristically instead of from one canonical agent contract.
5. relay-only bootstrap recovery still depends on assumptions that are not guaranteed in all environments.

## Desired Lifecycle Model

The intended product model is now a five-state lifecycle:

- `offline`
- `bootstrap`
- `yaver-auth-expired`
- `ready-to-connect`
- `connected`

This is the right model. The problem is not the labels. The problem is whether those labels are backed by enough protocol truth.

## State Definitions

### `offline`

Meaning:

- device is not reachable at all
- no direct HTTP proof
- no relay-backed HTTP proof
- no current auth escalation path

Expected UX:

- show as off / unreachable
- do not imply reconnect can work immediately

### `bootstrap`

Meaning:

- agent HTTP bootstrap server is alive
- agent has no usable auth token
- agent reports bootstrap or `needsAuth`
- device may still be reclaimable from mobile/web

Expected UX:

- never show this as plain offline
- reconnect should mean reclaim/pair first, not normal attach

### `yaver-auth-expired`

Meaning:

- agent identity still exists
- token is invalid or expired
- `/auth/recover` path should be usable

Expected UX:

- show as recoverable
- reconnect action should trigger re-auth, not first-time pair

### `ready-to-connect`

Meaning:

- auth is valid or likely valid
- agent should be attachable without a recovery step

Expected UX:

- normal connect path

Audit caution:

This state currently still includes heuristic inference in some clients. That makes it weaker than the other states.

### `connected`

Meaning:

- Yaver session is live and attached

Expected UX:

- open workspace / use agent

## Architecture Under Audit

### Go Agent Bootstrap Surface

Bootstrap mode is implemented as a narrow auth server in:

- [desktop/agent/auth_bootstrap.go](/Users/kivanccakmak/Workspace/yaver.io/desktop/agent/auth_bootstrap.go)

The bootstrap server currently mounts:

- `/health`
- `/auth/pair/info`
- `/auth/pair/session`
- `/auth/pair/submit`
- `/auth/pair/encrypted`
- `/info`
- `/auth/recover`
- `/auth/pair/owner-claim`

This is enough for auth escalation. It is not a general-purpose agent surface.

### Go Agent Recovery / Claim Handlers

Relevant handlers:

- [desktop/agent/auth_recover.go](/Users/kivanccakmak/Workspace/yaver.io/desktop/agent/auth_recover.go)
- [desktop/agent/auth_owner_claim.go](/Users/kivanccakmak/Workspace/yaver.io/desktop/agent/auth_owner_claim.go)

### Mobile Recovery Logic

Relevant code:

- [mobile/src/lib/deviceStatus.ts](/Users/kivanccakmak/Workspace/yaver.io/mobile/src/lib/deviceStatus.ts)
- [mobile/src/context/DeviceContext.tsx](/Users/kivanccakmak/Workspace/yaver.io/mobile/src/context/DeviceContext.tsx)
- [mobile/src/lib/quic.ts](/Users/kivanccakmak/Workspace/yaver.io/mobile/src/lib/quic.ts)
- [mobile/app/(tabs)/tasks.tsx](/Users/kivanccakmak/Workspace/yaver.io/mobile/app/(tabs)/tasks.tsx)
- [mobile/app/(tabs)/devices.tsx](/Users/kivanccakmak/Workspace/yaver.io/mobile/app/(tabs)/devices.tsx)

### Web Recovery Logic

Relevant code:

- [web/lib/agent-client.ts](/Users/kivanccakmak/Workspace/yaver.io/web/lib/agent-client.ts)
- [web/app/dashboard/page.tsx](/Users/kivanccakmak/Workspace/yaver.io/web/app/dashboard/page.tsx)
- [web/components/dashboard/DevicesView.tsx](/Users/kivanccakmak/Workspace/yaver.io/web/components/dashboard/DevicesView.tsx)
- [web/lib/use-devices.ts](/Users/kivanccakmak/Workspace/yaver.io/web/lib/use-devices.ts)

## Current Protocol Reality

### What Bootstrap Mode Can Do

Bootstrap mode can currently support:

- liveness proof
- pair-session discovery
- passkey pairing
- encrypted pairing
- recovery handoff
- owner-claim reclaim

Bootstrap mode cannot currently support normal agent usage such as:

- projects
- runners
- workspace
- vault
- general API browsing

So when the user says "the device is up", that does not mean "the agent is usable". It only means "the bootstrap escalator is alive".

That distinction is correct technically, but the product needs to reflect it consistently.

## Recovery Paths by Case

### Case A: Auth Expired, Agent Identity Intact

Primary path:

- `/auth/recover`

This is the healthiest recovery path in the current design.

Strengths:

- strong handler support
- existing tests
- clear distinction between direct, pair, and device-code modes

Coverage:

- [desktop/agent/auth_recover_test.go](/Users/kivanccakmak/Workspace/yaver.io/desktop/agent/auth_recover_test.go)

Assessment:

- relatively solid compared with the rest of the recovery system

### Case B: Bootstrap Box, Previously Owned, Reachable Directly

Primary path:

- direct `/info`
- passkey or encrypted pair

This can work well when the phone can directly reach the bootstrap HTTP server and passkey or public-key-based pairing is available.

Assessment:

- workable
- still depends on direct reachability and known pairing material

### Case C: Bootstrap Box, Previously Owned, Relay-Only Reachability

Primary path:

- relay `/info`
- owner claim over relay

This is now a critical path for real-world remote recovery.

Problem:

- relay `/info` intentionally does not expose passkey
- so relay-only recovery often depends on `owner-claim`

Assessment:

- important
- under-tested
- current biggest practical risk

### Case D: Truly Fresh Bootstrap Box

Primary path:

- first-time pairing

This is not the same thing as reclaiming a previously owned device.

In [desktop/agent/auth_owner_claim.go](/Users/kivanccakmak/Workspace/yaver.io/desktop/agent/auth_owner_claim.go), `owner-claim` rejects cases with no `device_id`, and it also depends on ownership checks against Convex.

Assessment:

- should not be presented as equivalent to normal re-auth
- needs separate product wording and flow

### Case E: Device Row Is Stale or Merged Wrong

Primary path:

- depends on the client resolving the correct logical device identity first

Problem:

- reconnect and relay routing are device-id keyed
- stale or duplicate rows can make recovery target the wrong logical box

Assessment:

- subtle but dangerous
- likely contributor to "box is up but reconnect still fails"

## Findings

### 1. `owner-claim` is under-tested relative to its importance

`owner-claim` is the critical fallback when:

- the box is in bootstrap
- relay reachability works
- passkey is intentionally hidden
- mobile/web must reclaim the device without factory-resetting it

Implementation:

- [desktop/agent/auth_owner_claim.go](/Users/kivanccakmak/Workspace/yaver.io/desktop/agent/auth_owner_claim.go)

I did not find a dedicated handler test suite for this file.

That is a major gap because `owner-claim` now sits on a critical practical path for remote recovery.

Missing test scenarios include:

- no `device_id`
- device not present in owned devices result
- guest/shared token rejection
- no active pair session
- transient pair-session rotation
- success path over relay-backed reclaim

Severity:

- high

### 2. Fresh bootstrap and reclaimable bootstrap are different protocol states

The current product language tends to group all bootstrap devices together, but the protocol does not.

Reclaimable bootstrap:

- box previously belonged to the user
- device identity exists
- Convex ownership can be verified
- `owner-claim` may work

Fresh bootstrap:

- no durable ownership relationship yet
- may have no usable `device_id`
- requires first-time pair flow

If the UI does not distinguish these cases clearly enough, users will keep seeing "recover" actions that cannot succeed.

Severity:

- high

### 3. Lifecycle state is still partly inferred in clients instead of reported canonically by the agent

In mobile:

- [mobile/src/lib/deviceStatus.ts](/Users/kivanccakmak/Workspace/yaver.io/mobile/src/lib/deviceStatus.ts)

In web:

- [web/app/dashboard/page.tsx](/Users/kivanccakmak/Workspace/yaver.io/web/app/dashboard/page.tsx)
- [web/components/dashboard/DevicesView.tsx](/Users/kivanccakmak/Workspace/yaver.io/web/components/dashboard/DevicesView.tsx)

The new five-state model is good, but some states still depend on:

- stale heartbeat metadata
- online flags
- local inference
- "probably reachable" assumptions

That means the same physical device can still be described differently by different clients or at different times.

Severity:

- high

### 4. Bootstrap mode is only an escalation surface, not a usable Yaver state

The bootstrap HTTP server does what it was designed to do. It is intentionally narrow.

But product expectations can drift toward:

- if the box is up, the app should more or less work

That is not the current protocol.

Bootstrap today means:

- Yaver is not usable yet
- only recovery and claim operations are usable

This must be reflected explicitly in UI states and CTA behavior.

Severity:

- medium

### 5. Relay-only bootstrap recovery depends on assumptions that are not always true

Because passkeys are intentionally hidden over relay, relay-only bootstrap recovery depends on:

- known public key for encrypted pairing, or
- successful owner-claim

If both are unavailable, the box is not truly offline, but reconnect still fails.

That means:

- not offline
- not actually recoverable through the currently available UI path

This is exactly the class of ambiguity that needs a clearer lifecycle contract.

Severity:

- medium

### 6. Stale device rows can still cause reconnect to hit the wrong logical identity

Relevant dedupe code:

- [mobile/src/context/DeviceContext.tsx](/Users/kivanccakmak/Workspace/yaver.io/mobile/src/context/DeviceContext.tsx)
- [web/lib/use-devices.ts](/Users/kivanccakmak/Workspace/yaver.io/web/lib/use-devices.ts)

These merges are trying to help, but reconnect still depends heavily on `deviceId`.

If the wrong stale row survives, both:

- relay `/d/:id/...`
- owner-claim

can fail for reasons that look like transport or auth failures when the real issue is identity mismatch.

Severity:

- medium

### 7. Coverage is asymmetrical across the recovery stack

Current strong areas:

- `/auth/recover` tests
- bootstrap passkey secrecy tests

Current weak areas:

- `owner-claim`
- full lifecycle matrix tests
- mobile/web state-machine tests
- stale-row identity resolution tests

This asymmetry is the main systemic reason repeated "fixes" have still failed in practice.

Severity:

- medium

## Coverage Review

### Good Coverage

- [desktop/agent/auth_recover_test.go](/Users/kivanccakmak/Workspace/yaver.io/desktop/agent/auth_recover_test.go)
- [desktop/agent/bootstrap_security_test.go](/Users/kivanccakmak/Workspace/yaver.io/desktop/agent/bootstrap_security_test.go)

These give reasonable confidence in:

- recover handler policy behavior
- bootstrap passkey visibility rules
- relay/public suppression of sensitive pair material

### Missing or Insufficient Coverage

- dedicated `owner-claim` handler tests
- canonical lifecycle-state tests at the agent boundary
- mobile `Tasks` state/action matrix tests
- mobile `Devices` state/action matrix tests
- web device CTA state/action matrix tests
- stale device-row merge tests for reconnect targeting
- relay-only bootstrap reclaim smoke tests

## Protocol Gaps

### Gap 1: No canonical lifecycle response contract

Today, lifecycle state is reconstructed from a mix of:

- `needsAuth`
- `authExpired`
- reachability probes
- heartbeat freshness
- device row metadata

The agent should expose a canonical lifecycle contract that clients consume directly.

Recommended direction:

- extend `/info`
- or add a narrow lifecycle endpoint

With fields like:

- `lifecycleState`
- `recoveryMode`
- `recoverable`
- `claimable`
- `requiresFirstPair`
- `supportsOwnerClaim`

### Gap 2: `owner-claim` depends on state that is not surfaced clearly enough

`owner-claim` success depends on:

- valid `device_id`
- ownership match in Convex
- active pair session

These are real prerequisites, but clients do not currently see them as explicit recoverability facts.

### Gap 3: Reconnect and re-auth are still too intertwined in client logic

The desired model should be:

- state comes from agent truth
- client only maps state to CTA

Instead, clients still contain significant recovery logic and interpretation.

That increases drift and inconsistency.

## Recommended Hardening Plan

### Phase 1: Protocol Truth

Implement a canonical lifecycle contract in the Go agent.

Requirements:

- lifecycle must be agent-reported, not inferred separately by each client
- bootstrap reclaimable and bootstrap fresh must be distinct
- relay-only recoverability should be explicit

### Phase 2: Owner-Claim Test Coverage

Add dedicated handler tests for:

- missing `device_id`
- not owned
- guest/shared token
- no pair session
- pair session available
- relay claim success

This is the highest-value immediate test investment.

### Phase 3: Client Matrix Tests

For both mobile and web, test:

- `offline`
- `bootstrap`
- `yaver-auth-expired`
- `ready-to-connect`
- `connected`

Each state should prove:

- badge copy
- CTA label
- CTA action
- fallback behavior

### Phase 4: Identity Robustness

Strengthen stale-row handling so reconnect targets the correct logical device more deterministically.

### Phase 5: Relay-Only Smoke Coverage

Add one realistic automated scenario for:

- previously owned box
- bootstrap mode
- relay reachable
- direct unreachable
- owner-claim succeeds

That is the failure mode that has repeatedly surfaced in practice.

## Recommended Product Semantics

### What Mobile and Web Should Say

`offline`

- machine is not reachable
- no recovery path currently available

`bootstrap`

- machine is up
- Yaver is waiting to be reclaimed or paired

`yaver-auth-expired`

- machine is up
- Yaver needs re-authentication

`ready-to-connect`

- machine is up
- Yaver is ready to attach

`connected`

- active Yaver session

### Important Distinction

Bootstrap should never be described as offline.

But bootstrap also should not imply full usability. It should explicitly mean:

- "the recovery surface is alive"

not:

- "the agent is already usable"

## Conclusion

The reconnect and re-auth architecture is directionally correct, but not yet fully trustworthy.

The main unresolved issues are:

- too much lifecycle inference in clients
- under-tested `owner-claim`
- unclear separation between reclaimable bootstrap and fresh bootstrap
- relay-only recovery that depends on assumptions instead of explicit protocol truth

If the goal is to stop repeating "fixed but not actually fixed" iterations, the next step should not be another UI patch.

The next step should be:

1. make lifecycle state canonical at the agent layer
2. add `owner-claim` coverage
3. add a full recovery matrix for mobile and web
4. add one relay-only bootstrap reclaim smoke test

That is the smallest path to making this area actually reliable.
