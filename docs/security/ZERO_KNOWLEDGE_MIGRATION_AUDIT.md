# Zero-knowledge migration audit — 2026-10-04

This is a code-grounded status record, not a claim that migration is complete.

## Proven now

- Relay mesh frames are WireGuard ciphertext; the relay routes without payload
  inspection and now fails closed for missing/different owners on public relay.
- Relay HTTP device access supports Ed25519 request authentication, freshness,
  nonces and same-owner target resolution. This authenticates requests but does
  **not** make reverse-proxied HTTP bodies opaque to the relay.
- The hosted Cloudflare inference Worker is retired and does not consume request
  bodies or credentials.
- Native tenant-owned Hetzner control is limited to list, power-on and graceful
  shutdown. It calls Hetzner directly, stores the token under a stable native
  Keychain/Keystore service, surfaces sanitized errors, waits for asynchronous
  actions, and supports a local authenticated-encryption recovery export.
- The same `hetzner_power` inner contract is implemented by web/desktop/MCP,
  tvOS/visionOS, Android TV, Apple Watch and Wear OS clients. Thin clients hold
  no provider credential. The endpoint rejects the inspectable `/d/...` relay
  path with `secure_transport_required`; remote control currently requires a
  direct TLS/LAN path or Yaver Mesh/WireGuard.
- iOS and Android phones can transfer the Hetzner credential to another trusted
  native phone through the short-lived, account/device-bound encrypted
  credential handoff. The directory stores only public receiver metadata.
- A connected desktop/daemon can now create an ephemeral handoff request and
  accept the phone's NaCl-box ciphertext over direct LAN/TLS or Mesh. Requests
  expire after two minutes, are account/device-bound, single-use and replay
  rejecting; plaintext is written straight to the endpoint's encrypted local
  credential vault and is never returned. Mobile refuses locally before
  sending when its selected route is relay/tunnel.
- Endpoint E2EE v1 primitives and adversarial tests exist in the Go agent.
- Existing-device-signed device authorization rejects server key substitution,
  cross-account certificates and modified membership records.
- Cloud diagnostic ingestion discards arbitrary message/detail/data bodies and
  stores only bounded event classifications. Developer-log reads require an
  authenticated owner.
- PC-to-phone machine access now uses a direct-only, account-bound NaCl handoff.
  The phone creates a reusable Ed25519 SSH identity locally; the PC appends only
  its public key to the local OS account and encrypts its current LAN/private
  overlay addresses, SSH user and port to the phone. The phone stores the
  private key and machine profile with Expo SecureStore's stable service, which
  maps to iOS Keychain and Android Keystore-backed encrypted storage. Convex
  retains only the receiver's public handoff identity. The last-working mobile
  connection cache uses the same secure service, so a successful direct IP or
  tunnel credential is not left in AsyncStorage.
- Agent registration, bootstrap, heartbeat and relay presence paths no longer
  publish `quicHost`, `localIps`, `publicEndpoints` or relay peer addresses.
  Backend writes clear legacy values and public/admin device projections redact
  them. Mesh endpoint arrays are also kept empty. The release migration removes
  values from offline device, pending-claim and mesh rows.

## Blocking plaintext paths

| Area | Current risk | Required migration |
|---|---|---|
| Relay `/d/{device}/...` proxy | Relay can observe HTTP path/body/response | Carry application envelopes over mesh/opaque socket; disable sensitive legacy proxy routes after surface cutover |
| Mobile/web/TV/watch clients | No shared application-E2EE session implementation yet | Bind native crypto core on every surface; verify endpoint-signed membership |
| Historical Convex diagnostic logs | Older rows may contain arbitrary message/detail strings | Purge legacy rows after production backup/retention review |
| Rescue queue | `result` can contain stdout/error text | Store only bounded result code/status; return detail E2EE when online |
| Feedback/artifacts | Feedback body and storage objects may be plaintext | Encrypt manifest/content client-side; random object keys; decrypt only at authorized endpoint |
| Project/deploy activity | Names, slugs, messages and targets reveal semantics | Minimize or encrypt fields; keep only opaque IDs/status/counters |
| Hosted inference deployment residue | Runtime paths are retired, but deployed secrets/KV may remain | Delete deployed secrets/KV after exact inventory |
| Pairing integration | Signed authorization primitive exists but all client flows do not enforce it yet | Require existing-device authorization/QR chain before session-key release on every surface |

## Surface support boundary

| Surface family | Current contract | Credential custody |
|---|---|---|
| iOS / Android phone | Direct Hetzner list/on/shutdown UI | Native Keychain/Keystore; encrypted recovery export |
| Browser web | Disabled for Hetzner control | No provider credential or power action |
| macOS / Windows / Linux desktop | Desktop-shell `hetzner_power` panel or local MCP verb | Encrypted endpoint-local vault; OS-keychain hardening remains platform-specific work |
| tvOS / visionOS / Android TV | Typed `hetzner_power` client adapter | Never stored on the TV/headset |
| Apple Watch / Wear OS | Typed compact client adapter | Never stored on the watch |
| CarPlay / Android Auto | Uses the phone process and its direct provider client; no dedicated destructive driving UI | Phone vault only |
| Xbox / PlayStation / Meta Quest | Not shipped; future clients must reuse the same inner contract and pass platform release gates | Must remain endpoint-only |

Typed adapters are transport capability, not proof that every store build has a
dedicated server list screen. Do not market the console/Quest rows as released,
and do not permit legacy reverse-proxy fallback for infrastructure commands.

## Enforcement landed

`scripts/verify-zero-knowledge-boundary.mjs` and the
`zero-knowledge-boundary` workflow prevent the retired Cloudflare gateway from
reading bodies/credentials, gaining storage bindings, calling providers or
importing endpoint decryptors. Canary tests prove the request body remains
unconsumed. Endpoint and relay adversarial tests cover tamper, replay, expiry,
key substitution, cross-tenant routing and missing-owner failure.

## Production claim gate

Do not publish the zero-knowledge security promise until every blocking row is
closed, production-held legacy provider secrets/KV values are deleted, a packet
capture contains no canary plaintext, and all native surfaces pass shared test
vectors plus a malicious-relay integration suite.
