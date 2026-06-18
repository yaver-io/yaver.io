# AI Architecture Notes

This file is the shortest useful map of the real Yaver runtime architecture for future AI/code agents.

It is intentionally biased toward the code paths that matter when the product is under stress:

- remote dev machine rebooted
- desktop agent came back under `systemd` / auto-start
- mobile app must still discover and reach it
- desktop auth may be stale or missing
- the phone must be able to recover ownership without SSH

Read this before changing auth, bootstrap, relay, pairing, or mobile device-selection code.

## High-Level System

Yaver has four runtime surfaces:

1. `desktop/agent/`
   The Go daemon and CLI. This is the real control plane on the developer machine.
2. `mobile/`
   The React Native mobile app. It discovers devices, chooses direct vs relay vs tunnel transport, and talks to the agent over HTTP.
3. `backend/convex/`
   Auth, session validation, device registry, user settings, relay/tunnel metadata, guest access, platform config.
4. `relay/`
   QUIC relay server. The desktop agent creates outbound tunnels; the phone makes short-lived HTTP requests through relay URLs.

The intended product shape is:

- desktop agent is always up
- HTTP server is always up
- at least one reachability path stays usable even if agent auth is stale
- mobile app is the recovery tool, not SSH

## Desktop Agent Lifecycle

Primary entrypoint:

- `desktop/agent/main.go`

Main serve path:

- `runServe()` in `desktop/agent/main.go`

Important behavior in `runServe()`:

1. Ensures autostart on first `yaver serve`
   - `ensureAutoStart(...)`
   - Linux target is a user `systemd` unit
2. If config has no auth token, it does not exit
   - it enters bootstrap mode via `runBootstrapServe(...)`
3. If config has a token but validation fails, it still starts
   - local HTTP remains up
   - heartbeat may fail
   - relay/tunnel startup still proceeds from local config
4. Starts:
   - HTTP server
   - optional QUIC server
   - beacon
   - heartbeat loop
   - relay manager
   - background discovery/monitoring helpers

This means the codebase already wants “serve anyway, recover later” rather than “hard fail on auth.”

## Persisted Machine Identity

Primary config:

- `desktop/agent/config.go`
- `~/.yaver/config.json`

Fields that matter for recovery:

- `auth_token`
- `device_id`
- `convex_site_url`
- `relay_password`
- `relay_servers[]`
- `cloudflare_tunnels[]`
- `bootstrap_secret_hash`

Machine identity also depends on:

- hardware ID: `HardwareID()`
- device X25519 keypair: `LoadOrGenerateKeys()`

Those identities are what let a rebooted machine be recognized as the same machine even before it has a valid current auth session.

## HTTP Surface Split

Implemented in:

- `desktop/agent/httpserver.go`
- `desktop/agent/auth_bootstrap.go`
- `desktop/agent/auth_recover.go`
- `desktop/agent/auth_pair.go`

There are three distinct agent HTTP states.

### 1. Normal authenticated serve

Mounted by `HTTPServer.Start(...)` in `desktop/agent/httpserver.go`.

Public endpoints:

- `/health`
- `/auth/pair/info`
- `/auth/pair/submit`
- `/auth/recover`

Auth-wrapped endpoints include:

- `/info`
- `/tasks`
- `/agent/status`
- most of the product surface

Important detail:

- `/health` is public and includes `authExpired: true` when heartbeat detects expired/revoked auth
- `/info` is not public in normal mode

### 2. Bootstrap mode: no auth token in config

Implemented in:

- `desktop/agent/auth_bootstrap.go`

Bootstrap mode is entered by:

- `needsBootstrap(...)` from `runServe()`

Bootstrap server exposes only a minimal surface:

- `/health`
- `/info`
- `/auth/pair/info`
- `/auth/pair/submit`
- `/auth/pair/encrypted`
- `/auth/recover`

Important bootstrap semantics:

- machine generates a 6-char passkey via `StartPairingSession(...)`
- machine can broadcast beacon hints:
  - `na:true`
  - passkey
  - device public key
- successful token push saves config and re-execs into normal `yaver serve`

### 3. Auth-expired normal serve

This is the failure mode that matters most for remote vibe coding.

Behavior today:

- agent still runs
- `/health` remains reachable
- heartbeat sets `authExpired=true`
- most authenticated endpoints return 401
- relay/tunnel may still remain usable because connectivity is not tied to current Convex auth validation

This is not full bootstrap mode, but it is supposed to be recoverable from the phone.

## Reachability Paths

The mobile app and desktop agent support multiple transports.

### Direct LAN

- desktop beacon: `startBeacon(...)` in `desktop/agent/main.go`
- mobile listener: `mobile/src/lib/beacon.ts`
- mobile selection logic: `mobile/src/context/DeviceContext.tsx`

Direct LAN is preferred on Wi-Fi.

### Tailscale

- desktop detection only, not management:
  - `desktop/agent/tailscale.go`

Tailscale addresses are folded into candidate URLs and status reporting. Yaver does not run `tailscale up`; it only reads local state.

### Cloudflare Tunnel

- tunnel config in desktop config + mobile settings
- mobile transport support in `mobile/src/lib/quic.ts`

### Yaver Relay

Desktop side:

- startup in `desktop/agent/main.go`
- `relayManager`
- `runRelayTunnel(...)`

Mobile side:

- transport logic in `mobile/src/lib/quic.ts`

Convex side:

- relay URLs/passwords flow through user settings and platform config

Key product assumption:

- relay path must keep working even if the desktop agent’s Convex auth session is stale
- otherwise remote phone control is dead exactly when the user needs it

## Device Registry and Bootstrap Presence

Primary backend code:

- `backend/convex/devices.ts`

Important mutations/queries:

- `registerDevice`
- `heartbeat`
- `markOffline`
- `markBootstrap`
- `clearBootstrap`
- `listMyDevices`
- `owner-by-hardware`

Important semantics:

1. Normal authed device registration stores:
   - `deviceId`
   - `userId`
   - `publicKey`
   - `hardwareId`
   - current host/port
2. Bootstrap mode can re-mark the device as online with `needsAuth=true`
   - authenticated by `(deviceId, hardwareId, publicKey)`
   - does not require current session token
3. Mobile app can use `needsAuth` from Convex to know a machine is alive but needs re-auth

This is the key bridge between “machine rebooted” and “phone can reclaim it.”

## Pairing and Token Push

Primary code:

- `desktop/agent/auth_pair.go`
- `desktop/agent/auth_bootstrap.go`
- `mobile/src/lib/pairDevice.ts`
- `mobile/src/lib/encryptedPair.ts`
- `mobile/src/context/DeviceContext.tsx`

There are two pairing styles.

### Passkey pairing

Flow:

1. target machine starts pairing session
2. target exposes `/auth/pair/info` and `/auth/pair/submit?code=...`
3. phone submits:
   - token
   - convex site URL
   - optional user ID

This is intentionally unauthenticated except for possession of the short-lived code.

### Encrypted pairing

Preferred flow when the phone already knows the device public key from Convex:

1. phone encrypts token to device X25519 public key
2. POST to `/auth/pair/encrypted`
3. desktop decrypts locally and stores token

This is the modern no-plaintext-on-LAN path.

## Remote Auth Recovery

Primary code:

- `desktop/agent/auth_recover.go`
- `mobile/src/lib/quic.ts`

Recovery endpoint:

- `POST /auth/recover`

Supported server-side auth modes:

1. Host-token mode
   - caller sends its own mobile Bearer session token
   - agent asks Convex `POST /devices/owner-by-hardware`
   - recovery allowed only if caller is the registered owner of this machine
2. Shared-secret mode
   - caller sends `secret`
   - agent verifies against `bootstrap_secret_hash`

Supported recovery actions:

1. `mode: "pair"`
   - starts one-shot pair window
   - returns pair code and submit URL
2. `mode: "device-code"`
   - starts new Convex device-code login flow
   - returns verification URL and user code

The intended “no game over” path for a signed-in phone is:

1. phone can still reach `/health`
2. phone sees `authExpired: true`
3. phone calls `/auth/recover` using its own Bearer token
4. agent opens pair session
5. phone immediately POSTs its token back via pair submit
6. agent resumes normal authenticated behavior

## Mobile Discovery and Connection Logic

Primary code:

- `mobile/src/lib/quic.ts`
- `mobile/src/context/DeviceContext.tsx`

Transport strategy in `quic.ts`:

1. Wi-Fi:
   - LAN beacon IP
   - Convex-known LAN IP
   - Cloudflare tunnel
   - relay
2. Cellular:
   - skip direct LAN
   - tunnel / relay

The phone probes `/health` to establish reachability and caches:

- connection mode
- connection path
- whether `authExpired` is set

`DeviceContext.tsx` also contains auto-pair logic for:

- beacon-discovered bootstrap devices
- relay-probed devices that report `needsAuth`
- direct `/info` probe for active device bootstrap detection

## Important Current Gap

As of 2026-04-16, the server-side recovery path is more complete than the mobile-side use of it.

Specifically:

- `desktop/agent/auth_recover.go` already supports host-token recovery
- `mobile/src/lib/quic.ts` method `recoverAgent(...)` only posts `{secret, mode}`
- it does not send the mobile Bearer token
- it is not wired into an automatic “agent auth expired, recover now” flow

Practical effect:

- the codebase has the pieces for seamless remote recovery
- but normal signed-in-phone recovery is not fully productized
- users can still land in a “remote machine is reachable but unauthenticated” dead state unless bootstrap auto-pair or manual secret-based recovery happens to save them

This is the most important architecture issue around remote reboot resilience.

## Boot/Reboot Story

The intended boot story is already visible in code:

1. user once runs `yaver serve`
2. agent installs auto-start
3. on reboot, OS starts agent
4. if token exists and is still good:
   - normal serve
   - register device
   - start relay
   - phone vibes normally
5. if token is missing:
   - bootstrap serve
   - beacon + relay bootstrap reachability
   - phone adopts machine
6. if token exists but is expired/revoked:
   - normal serve continues in degraded state
   - `/health` remains live
   - relay/tunnel should remain live
   - phone should recover auth remotely

The last bullet is the most critical reliability guarantee for solo remote use.

## File Map For Future Work

If changing reboot/auth/recovery behavior, start here:

- `desktop/agent/main.go`
- `desktop/agent/httpserver.go`
- `desktop/agent/auth_bootstrap.go`
- `desktop/agent/auth_pair.go`
- `desktop/agent/auth_recover.go`
- `desktop/agent/tailscale.go`
- `backend/convex/devices.ts`
- `backend/convex/http.ts`
- `mobile/src/lib/quic.ts`
- `mobile/src/context/DeviceContext.tsx`
- `mobile/src/lib/pairDevice.ts`
- `mobile/src/lib/encryptedPair.ts`

## Invariants To Preserve

Do not break these:

1. `yaver serve` must not hard-fail just because auth is missing or stale.
2. Public `/health` must remain available in every serve mode.
3. A rebooted machine must have some reachability path before auth is repaired.
4. Mobile recovery must not require SSH or local terminal access.
5. Pair/recovery flows must be one-shot and time-bounded.
6. Tailscale is a mobile recovery path, not a browser-dashboard path. Web recovery needs relay or HTTPS tunnel reachability.

Current ingress policy for `/auth/recover`:

- Default: open on the agent's main HTTP listener.
- Optional hardening: `require_private_recovery_transport=true` in config, or `yaver serve --recovery-policy=private`.
- In private-only mode, direct public HTTP ingress is rejected.
- Allowed private-only paths are:
  loopback / LAN
  Tailscale for mobile callers
  private relay
  HTTPS Cloudflare Tunnel
6. Device identity must remain tied to stable machine identity, not just the current token.
7. Relay reachability must not assume the desktop’s Convex session is currently valid.

## Suggested Reading Order

For a new AI/code agent:

1. this file
2. `CLAUDE.md`
3. `desktop/agent/main.go`
4. `desktop/agent/auth_bootstrap.go`
5. `desktop/agent/auth_recover.go`
6. `mobile/src/context/DeviceContext.tsx`
7. `mobile/src/lib/quic.ts`
8. `backend/convex/devices.ts`
