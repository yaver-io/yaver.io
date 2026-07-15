# Yaver Deep Security Analysis - 2026-07-13

## Scope

Threat model: attacker can create a normal Yaver account, read all public source
code and git history, reverse engineer released clients, sniff their own traffic,
probe public Yaver/relay endpoints, and attempt IDOR/cross-account access. This
review focuses on account isolation, relay isolation, device ownership, guest /
host-share scoping, SDK tokens, recovery/pairing, and managed-machine blast
radius.

This is source review, not a full dynamic pentest. Markdown docs were treated as
context only; code was used as source of truth.

## Executive Summary

Yaver already has several strong controls:

- Convex device registration and heartbeat enforce `device.userId === session.user._id`.
- The official relay validates per-user relay passwords through Convex for
  `register` and `proxy` actions.
- Relay tunnel hijack by duplicate device ID is blocked with refuse-on-collision.
- Relay admin/status/presence routes are auth-gated.
- Agent owner endpoints are bearer-authenticated; non-owner tokens fall into
  guest or host-share checks.
- Guest header spoofing is stripped before server-stamped guest headers are
  applied.
- Host-share file `rootPath` is now checked against allowed projects.
- Machine-scoped session tokens exist and some HTTP spend/account routes call
  `requireFullScope`.

Main remaining risks:

1. Relay `/d/{deviceId}` still has a shared password fallback and query-string
   password support.
2. Relay prefix routing authorizes one device ID but may route to another
   connected tunnel sharing that prefix.
3. Machine-scoped tokens are recorded, but enforcement is incomplete across many
   Convex mutations that call only `validateSessionInternal`.
4. Host-share path matching and SDK scope path matching use raw prefix checks in
   places where segment-aware matching is safer.
5. Device signing keys can be replaced by any full owner session during normal
   device registration; after session theft, relay signer identity can be
   rotated.

## P0/P1 Findings

### P1: Relay Password Fallback Remains High-Value

Files:

- `relay/server.go:1147-1155`
- `relay/server.go:1167-1185`
- `backend/convex/userSettings.ts:622-660`

`/d/{deviceId}/...` accepts `X-Relay-Password` and also `?__rp=`. On the public
relay, Convex checks that the password owner owns the requested `deviceId`, which
prevents a random account from directly proxying to another user's device.

The problem is blast radius: the relay password is account-wide and must exist on
clients/agents. If leaked via logs, URLs, browser history, screenshots, or a
compromised user's own machine, it enables relay forwarding to all devices owned
by that user until rotation. Agent auth still protects many endpoints, but public
pairing/recovery/dev-preview-style surfaces become reachable for probing.

Recommended fix:

- Make device-signature auth mandatory for public relay proxy traffic.
- Disable `?__rp=` on the public relay by default; keep it behind an explicit
  self-hosted/dev flag only.
- Where fallback is still needed, use per-device relay credentials instead of
  one per-user password.
- Add telemetry for password-auth usage and a migration deadline.

### P1: Relay Prefix Routing Can Authorize One Device But Route Another

Files:

- `relay/server.go:1141`
- `relay/server.go:1168`
- `relay/server.go:1235-1250`

`handleProxy` validates relay access using the path device ID. If exact lookup
fails, it searches connected tunnels by `strings.HasPrefix(id, deviceID)`.

That means authorization is for `deviceID` from the URL, but routing may go to a
different connected tunnel whose ID merely shares that prefix. In Convex-backed
official relay mode, `/relay/validate` checks ownership of the requested
`deviceId`, not the eventual matched tunnel ID.

Exploit preconditions:

- Attacker must own a device ID that is a prefix of another connected device ID,
  or must guess/use a colliding prefix that Convex treats as their own device.
- The target tunnel exact ID is not used in the request.

Impact:

- Potential cross-device or cross-account relay routing if a prefix collision is
  achievable.
- At minimum, ambiguous routing and difficult-to-audit authorization.

Recommended fix:

- Remove prefix routing from public relay `/d/` entirely.
- If short IDs are required for UX, resolve aliases/prefixes in Convex/mobile
  before calling the relay, and authorize the exact resolved `deviceId`.
- Add a regression test: user A owns `abcdefgh`, user B owns
  `abcdefghZZZ`; a request authorized for A's ID must never route to B's tunnel.

### P1: Machine-Scoped Token Enforcement Is Incomplete

Files:

- `backend/convex/auth.ts:549-576`
- `backend/convex/http.ts:459-475`
- `backend/convex/deviceCode.ts:248-260`
- Many Convex mutations call `validateSessionInternal` directly.

Machine-scoped tokens are recorded as `scope: "machine"` and HTTP helper
`requireFullScope` exists. However, many Convex mutations and HTTP routes only
call `validateSessionInternal` and then treat the result as a normal owner
session.

If a managed box is compromised, a machine token should only operate on that box:
heartbeat, activity, own resources, and tightly scoped reporting. It should not
be able to create SDK tokens, invite guests, change auth state, list all devices,
modify settings, or touch account-level/project-share/cloud-control surfaces.

Recommended fix:

- Add helper functions in Convex:
  - `requireFullSession(session)` for account-level mutations.
  - `requireMachineOwnDevice(session, deviceId)` for machine-safe mutations.
- Audit every `validateSessionInternal` caller.
- Default-deny machine scope everywhere, then allowlist exact machine routes:
  device heartbeat/register for own `deviceId`, activity reporting, self-park,
  and narrowly required provisioning callbacks.
- Add tests where a machine token fails on guests, settings, SDK token creation,
  project shares, cloud spend, and device listing of other devices.

### P1: Device Signing Public Key Is Mutable By Full Session

Files:

- `backend/convex/devices.ts:565-567`
- `backend/convex/devices.ts:576-624`
- `backend/convex/devices.ts:2121-2154`

Device registration accepts `signPublicKey` and patches it onto an existing
device row when the caller has a valid owner session. That is convenient for key
rotation, but after a stolen full session token the attacker can register/update
the victim's device row with a new signing public key, then use signed relay
requests as an accepted signer for that device.

This is not cross-account by itself; it is a post-session-theft persistence and
relay-control amplifier.

Recommended fix:

- Treat signing-key rotation as a sensitive event.
- Require proof from the old device key when replacing `signPublicKey`, or require
  a short-lived recovery/reauth flow with notification.
- Record key version, previous-key grace, and security event.
- For managed/machine-scoped tokens, allow only the device itself to report its
  own key and never another device ID.

## P2 Findings

### P2: Host-Share Path Allowlist Uses Prefix Matching

Files:

- `desktop/agent/httpserver.go:1581-1599`
- `desktop/agent/httpserver.go:1697-1710`

`isHostShareAllowedPath` checks `strings.HasPrefix(path, prefix)`. This is looser
than the guest matcher, which has segment-aware and exact-only logic. A future
route such as `/agent/runners/test` or `/files/read-secret` can accidentally
inherit host-share access from `/agent/runners` or `/files/read`.

Recommended fix:

- Reuse the segment-aware guest matcher for host-share.
- Mark `/info`, `/agent/status`, `/agent/runners`, `/files/roots`,
  `/files/list`, `/files/read`, and `/files/raw` exact-only unless subpaths are
  intentionally needed.
- Add tests for sibling collisions: `/agent/runners/test`,
  `/files/read-secret`, `/ops-extra`.

### P2: SDK Scope Path Matching Uses Prefix Matching

Files:

- `desktop/agent/httpserver.go:1601-1627`
- `desktop/agent/httpserver.go:1629-1641`

`pathAllowedByScopes` also uses raw prefix checks. Example: a scope allowing
`/runner-auth/status` also allows `/runner-auth/status-anything` if such a route
is later introduced.

Recommended fix:

- Convert SDK scope path matching to the same segment-aware helper.
- Add exact-only behavior for non-subtree entries.
- Add route-collision tests for every scope in `scopePathPrefixes`.

### P2: Host-Share Allowed Projects Use Basename/String Equality Only

Files:

- `desktop/agent/httpserver.go:1712-1732`
- `desktop/agent/files_browser.go:490-520`

The new host-share `rootPath` containment fix is good, but
`hostShareCanAccessProject` matches allowed projects by basename or exact string.
If two projects share a basename, an allowed slug can unintentionally authorize
the wrong root.

Recommended fix:

- Store and enforce stable project IDs or canonical absolute roots for
  host-share grants on the host.
- If using names/slugs, require mapping through the agent's discovered project
  registry rather than matching arbitrary basenames.
- Add a regression test with two `sample-app` directories in different parents.

### P2: Relay Password Cache Key Includes Secret Material

Files:

- `relay/server.go:370-385`

The in-memory cache key concatenates action, device ID, password, and token. This
is not directly exposed, but it increases accidental leak risk if maps are dumped
in panic/debug tooling.

Recommended fix:

- Store SHA-256/HMAC of password/token in cache keys instead of raw values.
- Keep raw secret values out of any fmt/log/error path.

## Confirmed Good Controls

### Device Ownership

Files:

- `backend/convex/devices.ts:576-589`
- `backend/convex/devices.ts:916-927`
- `backend/convex/devices.ts:1276-1298`

Device register/update rejects updating another user's existing `deviceId`.
Heartbeat requires the device row to belong to the session user. Device listing
queries by the current session user's ID.

### Relay Register/Proxy Ownership Validation

Files:

- `relay/server.go:351-385`
- `backend/convex/userSettings.ts:639-660`

Official relay mode validates registration with both relay password and session
token. Proxy mode validates that the relay-password owner owns the target
`deviceId`.

### Agent Guest Header Spoofing Protection

Files:

- `desktop/agent/httpserver.go`, `stripGuestRequestHeaders`
- `desktop/agent/httpserver.go:1644-1695`

Guest/SDK headers are stripped and re-stamped from server-resolved state before
downstream handlers consume them.

### Host-Share RootPath Escape Fix

Files:

- `desktop/agent/files_browser.go:502-510`

Caller-supplied absolute `rootPath` is checked against host-share allowed
projects before read/write/mkdir/delete handlers accept it.

### Pairing/Recovery Controls

Files:

- `desktop/agent/auth_pair.go`
- `desktop/agent/auth_recover.go`

Pairing uses short-lived one-shot codes and rate limiting. Recovery distinguishes
host-token mode from bootstrap-secret mode and blocks direct/device-code recovery
unless host ownership is verified.

## Fix Order

1. Remove public relay prefix routing and add exact-device regression tests.
2. Disable public relay `?__rp=` and migrate clients to signed relay requests.
3. Enforce machine-scope default-deny across Convex mutations.
4. Convert host-share and SDK path allowlists to segment-aware matching.
5. Harden signing-key rotation with old-key proof or reauth.
6. Replace basename project authorization with canonical project IDs/roots.
7. Hash relay secret cache keys.

## Suggested Test Harness Additions

- Relay cross-account prefix collision test.
- Relay password fallback disabled test for public-relay mode.
- Machine token negative tests for:
  - `auth.createSdkToken`
  - `guests.invite`
  - `userSettings.setByToken`
  - `devices.listMyDevices`
  - project-share create/invite
  - cloud/billing endpoints
- Host-share path collision tests.
- SDK scope path collision tests.
- Sign-key rotation test requiring old-key proof or reauth.

