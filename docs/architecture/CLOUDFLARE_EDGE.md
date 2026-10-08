# Cloudflare Edge control plane

> Code is authoritative. Verify every route in `cloudflare/edge/src/index.ts`,
> every schema claim in `cloudflare/edge/migrations/`, and every agent transport
> claim in `desktop/agent/main.go` before relying on this document.

## Product boundary

Yaver Edge is the default account-scoped route from any Yaver client surface to
an agent on a user-owned computer or VPS. The user installs Yaver and signs in;
they do not receive Cloudflare credentials, create Cloudflare tunnels, or manage
another tenant's connectivity.

Cloudflare is transport and minimal identity/routing infrastructure. It does
not become the project database. Projects, task content, terminal output, file
paths, prompts, model responses, recordings, and tunnel payloads are not stored
in D1. Agent-local metadata such as projects remains in the agent's local
SQLite store.

Convex remains in the migration path for third-party OAuth. Removing its old
device, project, relay, or user tables is a later destructive migration and is
not part of this edge deployment.

## Supported use cases

- A Mac, Windows, Linux, Raspberry Pi, or user-owned VPS runs `yaver serve` and
  opens an outbound authenticated WebSocket to `edge.yaver.io`.
- Desktop, CLI, web, mobile, browser lane, Hermes, scrcpy, WebRTC signalling,
  and other Yaver surfaces keep their existing agent HTTP/SSE/WebSocket API.
- Direct LAN, a user-owned relay, VPN, or another private network remains
  supported. Yaver Edge is the default managed fallback, not a lock-in.
- Free and Relay Pro use the same tenant authorization boundary. A paid plan is
  an entitlement and capacity distinction, never permission to cross tenants.

Raw QUIC does not pass through a Cloudflare Worker WebSocket. Native clients may
still prefer direct QUIC when reachable; the Cloudflare path multiplexes the
existing HTTP, SSE, and WebSocket operations over an outbound WebSocket. Do not
market the managed HTTP tunnel as end-to-end encrypted: Cloudflare and the
Worker transport are inside that TLS trust boundary, while the target agent
still performs its own bearer/device authorization.

## Free-device entitlement

The launch default is two **owned** devices per free account:

```toml
[vars]
FREE_OWNED_DEVICE_LIMIT = "2"
```

The value is a bounded non-negative integer. Missing, malformed, negative, or
unreasonably large values fail closed to the code default of `2`. A value of
`0` intentionally disables new free-device registration. Relay Pro has no
owned-device cap at the edge layer; later traffic, session, or transfer quotas
must use separate settings.

The registration write enforces the count inside one SQLite statement so two
concurrent registrations cannot both take the last slot. These rules apply:

- Re-registering the same device, owner, and public key always succeeds. This
  keeps existing agents recoverable even if the configured cap is lowered.
- A device shared with a member or guest does not consume that recipient's
  allowance. Only `devices.owner_user_id` is counted.
- A device ID owned by another account returns an opaque conflict and can never
  be adopted or modified.
- Changing a registered device's public key is rejected instead of silently
  replacing trust material.
- A limit denial is HTTP `403` with stable code
  `free_device_limit_reached`, the effective entitlement, and a route to the
  web Billing surface.

Changing `FREE_OWNED_DEVICE_LIMIT` requires an edge configuration deploy but no
database migration, endpoint change, or client release:

```bash
./deploy/deploy.sh edge
```

## Tenant and credential isolation

- Users authenticate with Yaver bearer sessions; no Cloudflare API token,
  account ID, tunnel credential, or Worker secret is sent to an agent/client.
- `DEVICE_ID_HMAC_KEY` stays in Cloudflare's secret store. Durable Object names
  are HMAC-derived so raw device IDs are not exposed as object names.
- Every proxy request resolves the caller's owner/member/guest grant in D1.
- The Durable Object checks the expected device owner independently.
- The target Go agent is the final authorization boundary for its operations.
- Browser credentials remain headers; tokens are never placed in URLs.
- CORS reflects the authenticated caller origin and never uses `*` on an
  authenticated route.

The D1 schema stores users, hashed session tokens, public device routing
identity, explicit device access grants, and subscription entitlement. Review
`cloudflare/edge/migrations/0001_identity_and_routing.sql` before changing that
boundary.

## Stable API

The edge intentionally preserves existing route shapes:

- `GET /health` and `GET /healthz`
- `GET /auth/validate`
- `POST /auth/refresh`
- `POST /devices/register`
- `GET /devices/list`
- `GET /presence?ids=...`
- `GET /agent/tunnel/ws`
- `METHOD /d/{deviceId}/{existing-agent-path}`

`/auth/validate`, `/devices/register`, and `/devices/list` expose the effective
device entitlement so clients can explain the cap before or after a denial.

## Deployment and verification

Use only the canonical deploy entry point:

```bash
./deploy/deploy.sh edge
```

That path installs from the lockfile, audits dependencies, runs tests and the
TypeScript checker, verifies the HMAC secret exists without reading it, applies
D1 migrations, deploys the Worker, and probes the real `/health` operation.

Before a wider rollout, verify:

1. `/health` reports `storesUserContent: false`.
2. The current account validates and reports the expected device entitlement.
3. An existing agent reconnects after lowering the configured cap.
4. A new device below the cap registers; the next one receives the named 403.
5. A second account cannot list, probe, proxy, or register over another
   account's device ID.
6. HTTP, SSE, and binary WebSocket traffic reaches the same agent endpoints.
7. Direct LAN and user-owned relay paths still work when managed edge is absent.

Do not remove the legacy free relay or delete Convex data until released clients
and signed agents have converged on this route and those checks pass from real
user surfaces. Do not treat a healthy Worker as proof that an installed agent is
connected; probe the actual device operation.

## Cost-control knobs

The Worker has explicit CPU and subrequest ceilings, presence queries are
bounded, and tunnel Durable Objects use hibernating WebSockets. Device count is
only an entitlement knob. Add independently tuneable transfer/session limits
after measuring real traffic; keep them visible in the entitlement response and
return a stable reason code instead of silently throttling or generating an
unbounded overage.
