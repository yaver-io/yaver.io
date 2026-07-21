# Out-of-band SSH channel — native client + relay-forwarding implementation plan

Execution plan for the phases that need a native build env / test box, so they can
be built cleanly in a focused session. The **agent-side foundation they plug into
is already built, tested, and on `main`** (see `ROBUST_TRANSPORT_SSH_QUIC.md`):

- `ssh_session_cmd.go` — forced-command verb whitelist (`sshSessionRoute`) + local
  dispatch. `ssh_managed_keys.go` / `ssh_keygen.go` — `# yaver-managed` key
  lifecycle + frictionless ed25519 keygen. `ssh_control_server.go` — embedded
  public-key-only SSH server (the box end), closed-loop tested.
  `ssh_reverse_tunnel.go` — native-vs-reverse selection + autossh-grade bounded
  supervisor. `doctor_transport.go` — `/doctor/transport` self-diagnosis.

## Phase A — relay-side reverse-SSH forwarding (Go; needs `yaver-test-ephemeral`)

Goal: the phone reaches the box's embedded SSH server (`127.0.0.1:2222` on the box)
**through the existing QUIC relay tunnel** — no new SSH bastion, no per-device
bastion port (multi-tenancy stays deviceId-routed, §4a of the transport doc).

Relay (`relay/server.go`): the relay already forwards phone→box by
`tunnel.conn.OpenStreamSync` (see the `/d/<deviceId>/` proxy). Add a **stream tag**
so a stream can target the box's SSH port instead of its HTTP port:
1. New phone-facing entrypoint (reuse the QUIC listener, not a new port): a
   CONNECT-style request `POST /d/<deviceId>/_ssh` (authorized exactly like the
   HTTP proxy — `authorizeProxyViaSig`, same-owner/access-graph).
2. On authorize, `OpenStreamSync` to the box tunnel and write a 1-byte tag
   (`0x02` = ssh, existing HTTP path is `0x01`/untagged) before piping bytes
   both ways. Metadata only in logs (deviceId hash, bytes) — never payload.

Agent relay client (`desktop/agent`, `runRelayTunnel` accept loop): when an
accepted stream carries the `ssh` tag, dial `127.0.0.1:<YAVER_SSH_CONTROL_ADDR>`
and splice — otherwise the existing HTTP forward. Add a pure `routeRelayStreamTag(tag byte) streamTarget`
(testable) so the routing decision is unit-tested; splice with a fake SSH server
+ a loopback relay for the closed loop.

Security: the box still does public-key auth (Phase 2 keys) on that stream — the
relay only piped bytes; a hostile tenant's stream reaches the box's SSH port but
fails the handshake (§4d). Test: unauthorized key over the forwarded path is
refused end-to-end.

## Phase B — closed-loop test through the mac mini (real network)

With Phase A + `YAVER_SSH_CONTROL=1` on the mini:
1. `writeDeviceKeyPair` on this Mac → `applyManagedKey(mini)` installs the pubkey.
2. Over the relay `_ssh` path (off-tailnet) AND over Tailscale direct (on-tailnet),
   run `{"verb":"health"}` and `{"verb":"doctor-transport"}`; assert JSON back.
3. Revoke → assert the next handshake is refused. Rotate → assert new key works,
   old refused. This is the "closed loop with reverse ssh + regular ssh through
   mac mini" test. Script it under `scripts/` (metered-aware, mini via Tailscale).

## Phase C — native iOS client (`YaverSSHControl` TurboModule)

Language/deps: **SwiftNIO SSH** (`swift-nio-ssh`, Apache-2.0, pure Swift — no
libssh2 C vendoring). Add via SPM in the force-tracked overlay area.

Files (force-track like the other `mobile/ios/Yaver/*` overlays, because
`expo prebuild --clean` regenerates `ios/`):
- `mobile/ios/Yaver/YaverSSHControl.swift` — the module. Key in the **Secure
  Enclave** (`SecKeyCreateRandomKey` with `kSecAttrTokenIDSecureEnclave`,
  `.privateKeyUsage`; P-256 since SE is P-256 only — the agent's
  `authorizedManagedKeysChecker` already accepts any `ssh.PublicKey`, so register
  the SE key's SSH-wire public form). Connect via NIO to the resolved leg
  (Tailscale addr, or the relay `_ssh` CONNECT tunnel bridged to a local socket),
  **pin the host key** (`ensureSSHControlHostKey` fingerprint from Convex),
  keepalive `ServerAliveInterval=10`, `exec` the JSON verb, return stdout.
- `YaverSSHControl.m` — RN bridge (`RCT_EXTERN_MODULE`): `connect(host,port,...)`,
  `exec(json) -> Promise<string>`, `onEvent` emitter, `disconnect()`.
- Register in `AppDelegate`/`RCTAppDependencyProvider` (mirror `YaverBundleLoader`).
- **Background:** register for APNs **silent push**; on push, wake + reconnect +
  drain events. Entitlement: `aps-environment`, `UIBackgroundModes: remote-notification`.
JS: `mobile/src/lib/sshControl.ts` wraps `NativeModules.YaverSSHControl`; the
selector (below) consumes it.

## Phase D — Android native client

`native-webrtc`-style Kotlin module using **sshj** (Apache-2.0) or Apache MINA
SSHD client. Key in **Android Keystore** (`KeyGenParameterSpec`, StrongBox where
available). Same connect/exec/keepalive/host-pin contract as iOS. Background via
FCM data message + a bounded WorkManager reconnect.

## Phase E — mobile transport selector + tmux-id UI (all surfaces)

- `MachineTransport` interface + kinds `quic | relay | ssh-control | ssh-task`
  (design doc §Core Principle). The selector uses `ssh-control` for liveness truth
  + seamless data-path re-attach + the (bounded) self-heal trigger.
- **tmux id in the task UI on every surface** (`tasks.tsx` shows
  `task.tmuxSessionId` — already on the Task model, `tasks.go:936`): RN surfaces
  (mobile/tablet/car/glass) via the shared task detail; **web** (`web/…/tasks`),
  **tvOS** (`tvos/YaverTV`), **watch/Wear** each render the same id string.
  NOTE: the RN `tasks.tsx` edit is gated until the current concurrent session's
  uncommitted edits there are committed (avoid sweeping their work).

## Phase F — deploy (LAST, per user)

Only after A–E are perfect: relay binary (`relay-deploy-binary.yml`, bump
`versions.json.relay`) → agent (`gh workflow run release-cli.yml`, bump `cli`) →
mobile TestFlight (native module → `deploy-testflight.sh`). One converged deploy
per target. Enable `YAVER_SSH_CONTROL` by default only once the native clients ship.
