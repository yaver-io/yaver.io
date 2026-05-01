# Yaver Reauth Recovery Report

## Summary

Yaver's reauth logic is already strong at the transport and token-application level, but the control-plane model is still incomplete. The current system can usually recover a remote box, yet MCP does not expose one coherent, secure, session-based recovery model for Claude Code, Codex, or other headless clients.

The main issue is architectural:

- recovery is currently treated as "call `/auth/recover`, then infer what happened"
- it should instead be treated as "create a scoped recovery capability, then follow that capability to completion"

For an open-source project, this distinction matters. All routes and states should be assumed public knowledge. Recovery safety must come from explicit proof boundaries and narrow capability surfaces, not obscurity.

## Current Problem

There are three different trust models involved in recovery:

1. Owner-authenticated recovery
2. Bootstrap-secret recovery
3. Wrapped-runner auth recovery for Claude Code / Codex

These are partly enforced in server code today, but not fully reflected in MCP and not modeled as one end-to-end recovery UX.

This creates three problems:

### 1. Security Ambiguity

- `/auth/recover` is powerful
- some modes are only safe for verified owner auth: `direct`, `device-code`
- some modes are acceptable for bootstrap-secret callers: `pair`
- MCP clients are currently pushed toward broad state inference from `/health` or `/info`, which is the wrong trust boundary

### 2. Stability Gap

- there is no first-class recovery-session status primitive
- callers can start recovery, but cannot securely and stably wait for completion
- `/info` stops being usable once auth is restored because it becomes protected
- `/health` is too broad and not tied to one recovery attempt

### 3. UX Inconsistency

- mobile has richer recovery orchestration than MCP
- wrapped-runner auth feels separate from Yaver auth
- no-local-bearer recovery is low-level and not a polished MCP workflow
- alias-based box naming was not consistently honored across remote selection paths

## Open-Source Threat Model

Because the project is open source, the design must assume:

- attackers know every recovery route
- attackers know every recovery state branch
- attackers can script probes against exposed boxes
- public HTTP recovery surfaces will be discovered and tested
- bootstrap secrets are the main residual risk when owner auth is unavailable

That means the right design is not a more generic recovery API. It is a narrower, capability-based recovery API.

## Correct Trust Model

Recovery should be split into three explicit security tiers.

### Tier 1: Owner-Authenticated Recovery

Caller has a valid Yaver bearer for the owning account.

Allowed:

- inspect owned devices
- select devices by ID, name, or alias
- start `direct` recovery
- start `device-code` recovery
- start `pair` recovery
- poll recovery-session status
- complete wrapped-runner browser auth flows

This should be the best UX path.

### Tier 2: Bootstrap-Secret Recovery

Caller has only the bootstrap secret.

Allowed:

- start `pair` recovery only
- poll narrow recovery-session status for that exact attempt

Not allowed:

- `direct`
- `device-code`
- broad owned-device discovery
- broad machine inspection
- wrapped-runner auth actions

This path should be intentionally narrower.

### Tier 3: Transport-Only Reachability

Caller can reach the box but has no proof.

Allowed:

- minimal health only

Not allowed:

- start recovery
- poll useful recovery state
- inspect auth posture

This tier should remain nearly useless by design.

## Why `/health` and `/info` Are the Wrong Wait Primitive

Using generic host endpoints as a recovery waiter is attractive but incorrect.

Problems:

- `/info` is authenticated once normal serve mode is restored
- `/health` is too broad and leaks host lifecycle
- neither endpoint is scoped to one recovery attempt
- they answer "what is the box doing?" instead of "did the recovery capability I started complete?"

That means recovery needs its own session object and its own status surface.

## Recommended Fix

Implement session-bound recovery.

### Flow

1. Caller starts recovery
2. Server creates:
   - `recovery_id`
   - `wait_token`
   - `mode`
   - `expires_at`
3. Server returns:
   - `recovery_id`
   - `wait_token`
   - `mode`
   - `expires_at`
   - `next_action`
   - any immediate UX payload like `pairCode` or `deviceCodeUrl`
4. Caller polls recovery-session status using `recovery_id` + `wait_token`
5. Server returns only the status of that recovery attempt

### State Machine

Recommended recovery states:

- `started`
- `awaiting_pair_submit`
- `awaiting_browser_oauth`
- `applying_token`
- `recovered`
- `failed`
- `expired`

This is enough for good UX and avoids leaking unrelated machine state.

## Recovery Session Security Rules

### Required Properties

- `recovery_id` must be random and high entropy
- `wait_token` must be separate from `recovery_id`
- both must be short-lived
- session status must expose only session state, not broad machine state
- session power must be bound to the proof used to create it

### What Status Must Not Expose

Recovery-session status should not expose:

- hostname
- workdir
- runner inventory
- project metadata
- general lifecycle details
- unrelated service health

It should expose only:

- `state`
- `mode`
- `expires_at`
- `next_action`
- sanitized `error_code`
- short user-facing `message`

This makes it a capability status endpoint, not a machine oracle.

## Bootstrap Secret Policy

Bootstrap-secret recovery is useful, but it is the weakest long-term trust mechanism.

Recommended policy:

- generate high-entropy secrets by default
- do not default to human-chosen passphrases
- show once
- store only in secure storage / password manager
- support easy rotation
- log recovery attempts but never log the secret or derivatives
- keep bootstrap-secret power limited to `pair`

Longer-term stronger options:

- device-bound recovery keypairs
- signed recovery challenges
- owner-issued recovery tickets

Those are worthwhile later, but not necessary to fix the current architecture.

## Open-Source Default Posture

For an open-source product, recovery defaults should be tightened.

Recommended default for new installs:

- `require-private-recovery=true`

Preferred transports:

- Tailscale
- private relay
- HTTPS Cloudflare tunnel

Plain public HTTP recovery should be:

- opt-in only
- explicitly warned against
- not the happy path in docs or setup

Reason:

- open-source examples will be copied directly into production environments
- unsafe defaults become real incidents

## MCP UX Plan

There should be two distinct MCP recovery families.

### 1. Owned-Device Recovery

For signed-in owner contexts.

Tools:

- `device_reauth_status`
- `device_reauth_start`
- `device_reauth_wait`

Behavior:

- accept device ID, prefix, name, or alias
- choose safe default mode automatically
- provide one clean flow for Claude Code / Codex

### 2. Explicit-Target Recovery

For no-local-bearer contexts.

Tools:

- `recovery_transport_status`
- `recovery_target_status`
- `recovery_target_start`
- `recovery_target_wait`

Behavior:

- requires exact `target_url`
- no device discovery
- refuses plain public HTTP unless explicitly overridden
- requires `bootstrap_secret` or `bearer_token`
- uses session-bound wait

This keeps good UX where trust already exists, and deliberate friction where trust is weak.

## Wrapped Runner Recovery

Wrapped-runner auth for Claude Code and Codex should follow the same conceptual model as Yaver recovery:

1. start auth session
2. return URL or code
3. optionally accept pasted code
4. poll status
5. finish

This creates one consistent story:

- Yaver reauth is session-based
- runner auth is session-based
- mobile, web, and MCP all drive similar state machines

## Alias Support

Alias-based box selection is good UX in trusted contexts.

Recommendation:

- support aliases in owned-device resolution
- do not use aliases as an unauthenticated discovery surface

That means aliases belong in:

- owner-authenticated MCP tools
- shared owned-device resolver

And not in:

- public explicit-target recovery discovery

## Implementation Plan

### Phase 1: Server Recovery Session Registry

Add an in-memory `recoverySession` registry:

- keyed by random `recovery_id`
- separate `wait_token`
- short TTL, e.g. 10 minutes
- mode
- state
- proof class
- sanitized failure details

### Phase 2: Extend `/auth/recover`

When recovery starts:

- create a recovery session
- return session handles
- update session state during flow progression

For each mode:

- `direct`: mark `applying_token`, then `recovered`
- `pair`: mark `awaiting_pair_submit`, then `applying_token`, then `recovered`
- `device-code`: mark `awaiting_browser_oauth`, then `applying_token`, then `recovered`

### Phase 3: Add Recovery Session Status Endpoint

Add:

- `GET /auth/recover/session`

Requirements:

- `recovery_id`
- `wait_token`

Returns only narrow session payload.

### Phase 4: Rework MCP Wait Tools

Update:

- `device_reauth_wait`

Add:

- `recovery_target_wait`

These should use session status only, not generic `/health` or `/info`.

### Phase 5: Preserve Strict Mode Boundaries

Keep the current server-side proof rules:

- bootstrap secret: `pair` only
- verified host bearer: `direct`, `device-code`, `pair`
- no proof: no recovery powers

### Phase 6: Tighten Defaults

For new installs:

- default to private recovery
- strongly guide users toward Tailscale / relay / HTTPS tunnel

### Phase 7: Documentation

Document the three trust tiers clearly:

1. owner-auth
2. bootstrap-secret
3. transport-only

Make the public docs reflect the safe default path, not the most permissive path.

## Expected Outcome

If implemented this way, Yaver recovery becomes:

- secure enough for an open-source deployment
- stable across all recovery modes
- easy for Claude Code / Codex to drive through MCP
- consistent across mobile, web, and MCP
- explicit about what proof grants what power

The key principle is:

Recovery should be a scoped capability with bounded authority, not a broad machine-state flow inferred from generic endpoints.
