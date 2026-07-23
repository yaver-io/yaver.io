# Secure Frictionless Transport Setup

Status: design audit, 2026-07-21.

This document describes how Yaver should make remote machine access feel like
"install app, sign in, start vibing" while keeping the transport secure enough
for an open-source product. It covers local key generation, Yaver-issued device
certificates, Convex-assisted bootstrap, relay, Yaver Mesh, SSH, reverse SSH,
and the user-facing setup surfaces.

As with every architecture document in this repo: code is the source of truth.
Before implementing any route, field, or transport named here, grep the actual
Go/TypeScript code and update this doc if the code has moved.

## Product Goal

The normie path should be:

1. Install Yaver on phone/desktop/cloud workspace.
2. Sign in.
3. Pair a machine with QR/link/passkey approval.
4. Start a project or task.
5. Yaver silently chooses the best transport.

The user should not need to understand:

- SSH keys.
- port forwarding.
- NAT.
- Tailscale routing.
- relay passwords.
- QUIC vs WebRTC vs reverse SSH.
- cloud provider firewall rules.

The debug surface can show:

- `Path: LAN`, `Path: VPN`, `Path: Relay`, `Path: Mesh`, or `Path: Reverse SSH`.
- `tmux: yaver-<task>`.
- `machine: primary`.
- `agent: yaver serve`.
- `runner: codex/claude/opencode`.

But the product contract is Yaver tasks, projects, previews, and serverless
deployments, not raw terminals.

## Non-Goals

- Do not make raw SSH the main product API.
- Do not require users to bring their own VPN.
- Do not upload a user's personal SSH private key to Yaver cloud.
- Do not store provider credentials, relay passwords, private keys, or customer
  hostnames in the public repo.
- Do not let Convex become the data plane for terminal output, app code, files,
  or task-derived text.
- Do not assume one user, one Mac, one home path, or one cloud provider.

## Current Shape To Preserve

Yaver already has the right high-level split:

- Convex is the identity, registry, settings, bootstrap, and metadata plane.
- The Go agent is the authority on the machine.
- Relay is a fallback data path.
- Direct LAN/VPN paths are preferred when reachable.
- Mesh can provide a Yaver-owned overlay path.
- Mobile/web should see structured task state, not scraped terminal text.

The security design should strengthen that split. Convex may help two devices
find and authenticate each other, but the machine's local agent remains the
authority for commands, filesystem access, tmux, project scanning, runners, and
serverless deployment.

## Threat Model

The design must handle these realistic failures:

- A malicious user tries to claim another user's machine.
- A stale paired phone tries to keep access after revocation.
- A relay is compromised or misconfigured.
- Convex is reachable but stale.
- A device row contains old LAN/VPN addresses.
- A reverse tunnel survives after user sign-out.
- A guest invitation is revoked, expired, or hidden.
- An open-source fork reads implementation details and tries to impersonate a
  Yaver client.
- A cloud workspace image leaks build logs.
- A mobile app is lost or restored from backup with old local state.
- A user enables Tailscale, Yaver Mesh, and relay at the same time.

Security target:

- Relay can forward packets, but cannot invent user commands.
- Convex can authorize pair/session metadata, but cannot execute on the machine
  without the agent accepting the same identity.
- A stolen relay token expires quickly.
- A stolen device row is insufficient to connect.
- A compromised guest cannot become owner.
- A raw SSH fallback never bypasses Yaver's task/capability layer unless the
  owner explicitly opens a debug shell.

## Core Principle: Yaver Identity First, Transport Second

Every operation should be authorized against a Yaver session and a machine
capability, independent of the transport used.

Bad model:

```text
If SSH connects, command is allowed.
```

Good model:

```text
Yaver verifies user -> device -> capability -> session -> transport binding.
Then it sends the operation over whichever transport is alive.
```

This means the transport adapter should expose a stable interface:

```ts
interface MachineTransport {
  kind: "quic" | "webrtc" | "relay" | "mesh" | "ssh" | "reverse-ssh";
  health(): Promise<Health>;
  openTaskStream(taskId: string): AsyncIterable<TaskEvent>;
  runTask(req: TaskRequest): Promise<TaskHandle>;
  attachTmux(sessionId: string): AsyncIterable<TmuxFrame>;
  listProjects(req: ProjectScanRequest): Promise<ProjectScanResult>;
  openPort(req: PortForwardRequest): Promise<PortForwardHandle>;
  uploadBundle(req: BundleUploadRequest): Promise<UploadResult>;
}
```

The agent receives a signed Yaver request envelope no matter whether the bytes
arrived over QUIC, relay HTTP, WebRTC data channel, SSH stdio, or reverse SSH.

## Key Hierarchy

Yaver should use four layers of identity.

### 1. Account Identity

Owned by the user login session.

- Managed by Convex/auth provider.
- Used to list machines, settings, guest grants, billing tier, cloud workspace
  rights, and relay entitlements.
- Never enough by itself to execute commands.

### 2. Device Identity

Generated locally on each machine and each phone.

Recommended:

- Ed25519 keypair for signing.
- X25519 or Noise-compatible key agreement key for transport/session sealing.
- Hardware-backed storage when available:
  - iOS Keychain/Secure Enclave where practical.
  - Android Keystore.
  - macOS Keychain.
  - Linux secret service or encrypted Yaver vault fallback.

Convex stores:

- device id.
- public signing key.
- public encryption/agreement key if needed.
- hardware identity hash where available.
- last heartbeat.
- relay/mesh presence metadata.
- capabilities.

Convex must not store:

- private keys.
- SSH private keys.
- raw provider credentials.
- long-lived relay tunnel secrets.
- task text/logs/code bundles beyond intended metadata.

### 3. Yaver Device Certificate

After pairing, Convex/Yaver issues a short, signed certificate binding:

```json
{
  "v": 1,
  "userId": "u_...",
  "deviceId": "d_...",
  "devicePublicKey": "ed25519:...",
  "role": "owner-device",
  "capabilities": ["agent.health", "task.run", "tmux.attach", "project.scan"],
  "issuedAt": 0,
  "expiresAt": 0,
  "issuer": "yaver-control-plane",
  "certId": "cert_..."
}
```

The certificate is not a secret. It is a signed credential. The private device
key is what makes it useful.

Why use a Yaver certificate:

- The agent can validate local requests without calling Convex on every
  command.
- Relay can require a valid device certificate before accepting a tunnel.
- Revocation can be represented as certificate generations / epochs.
- Open-source clients cannot just copy request shapes; they need a real paired
  device key and valid account grant.

### 4. Session Capability Token

Every interactive task gets a short-lived capability token:

```json
{
  "taskId": "task_...",
  "machineId": "d_...",
  "userId": "u_...",
  "transportSessionId": "ts_...",
  "tmuxSessionId": "yaver-...",
  "capabilities": ["task.followup", "task.stop", "tmux.read"],
  "expiresAt": 0,
  "nonce": "..."
}
```

This token should be:

- scoped to one task/thread/session;
- short lived;
- refreshable through the Yaver control plane;
- rejected after guest revoke/sign-out/device detach;
- bound to the transport handshake where possible.

## Pairing Flow

### Local Machine First Run

`yaver serve` first run should:

1. Generate a local device keypair.
2. Generate an agent-local admin token for loopback bootstrap.
3. Print or show a QR code / pairing code.
4. Register a bootstrap-pending row with Convex if the user is signed in.
5. Open outbound relay registration only in bootstrap-pending mode.

No inbound port should be required. If direct LAN works, great. If not, relay
or reverse tunnel can carry the pairing request.

### Phone Pairing

Phone scans QR or opens link:

1. Phone signs in.
2. Phone sends `claim bootstrap device` to Convex with pairing code.
3. Convex verifies code freshness and account.
4. Machine receives claim over:
   - local LAN request,
   - relay pending tunnel,
   - mesh if already present,
   - or reverse tunnel if configured.
5. Machine shows approval prompt or accepts if pairing was initiated locally.
6. Machine signs the claim with local private key.
7. Convex records device public identity and owner relation.
8. Convex issues/mints the device certificate.
9. Machine stores certificate in Yaver vault.

Important: Convex should not be able to silently claim a machine by itself. The
machine must either initiated pairing or approve the claim with local presence.

## Frictionless Setup Modes

### Mode A: Same LAN

Best for home/office.

Path:

```text
Phone -> LAN health probe -> agent
```

Security:

- Still requires Yaver account token + device certificate/capability envelope.
- LAN reachability alone grants nothing.

UX:

```text
Connected to Mac mini
Path: LAN
```

### Mode B: Existing VPN/Tailscale

Best for power users.

Path:

```text
Phone -> VPN IP -> agent HTTP/QUIC
```

Yaver should detect this automatically, but it should not require it. If a VPN
address is repeatedly unroutable, back off and prefer relay instead of hammering
it.

SSH direct can also be enabled here:

```text
Phone/Yaver agent -> SSH over VPN -> remote agent shim
```

But the operation still uses Yaver capability envelopes. SSH is just the pipe.

### Mode C: Yaver Relay

Default for normies and cellular.

Path:

```text
Machine -> outbound relay tunnel
Phone -> relay -> machine
```

Security:

- Machine authenticates to relay with device certificate + short relay tunnel
  credential.
- Phone authenticates to relay with account session + task/session capability.
- Relay checks both sides belong to the same access graph.
- Agent verifies the signed request envelope again before executing.

Relay should not accept bare password-only long-term access as the final design.
Legacy password support can remain during migration, but the target should be
device-signature based relay auth.

## Public Free Relay And Relay Pro

Yaver will likely have two relay products:

- **Yaver Public Free Relay**: shared, best-effort, limited throughput, useful
  for onboarding, light tasks, pairing, diagnostics, and trial users.
- **Yaver Relay Pro**: paid, higher reliability/limits, better region choice,
  longer session budgets, priority capacity, and stronger operational support.

Security must be identical across both tiers. Pro may get more capacity and
better SLOs; it must not get a weaker auth path. Free may be more constrained;
it must not become a less-isolated shared tunnel.

### Relay Security Invariants

These rules apply to both Public Free and Relay Pro:

1. Relay never authorizes commands by itself.
2. Relay never sees or stores private device keys.
3. Relay never stores user SSH private keys.
4. Relay never logs task prompts, terminal output, file contents, API keys,
   environment variables, provider tokens, or raw code bundles.
5. Relay only routes between identities that Convex/control-plane says are
   allowed at that moment.
6. Agent still verifies the signed Yaver request envelope before executing.
7. Every relay tunnel is scoped to one machine identity and one owner/access
   graph.
8. Guest relay sessions are separate from owner relay sessions.
9. Revocation closes or invalidates live sessions, not only future sessions.
10. Public Free and Pro both use the same certificate/signature protocol.

The product may call the free relay "free", but internally it must be treated as
hostile shared infrastructure.

### Public Free Relay

The Public Free Relay should be safe to offer to many unknown users.

Default policy:

- short session lifetimes;
- strict idle timeouts;
- low bandwidth ceilings;
- low concurrent stream limits;
- low max file upload size;
- low max port-forward duration;
- task/event streaming allowed;
- WebRTC signalling allowed;
- pairing/bootstrap allowed;
- raw shell disabled by default;
- reverse SSH disabled by default unless explicitly needed for recovery;
- no arbitrary TCP proxy;
- no cross-account peering;
- aggressive abuse throttles.

Suggested limits:

```text
pairing/bootstrap: allowed, short-lived
health/status: allowed, cheap
task logs/events: allowed, rate-limited
tmux attach: allowed, owner/guest capability scoped
port forward: small number, short idle timeout
file upload: capped size and duration
WebRTC media relay: avoid by default; prefer signalling + direct media
raw shell: owner-only advanced debug, disabled for guests
reverse SSH: recovery-only, short lifetime
```

Public Free must prioritize control traffic over bulk traffic. A user streaming
large logs or uploads should not block another user's pairing or stop-task
request.

### Relay Pro

Relay Pro should improve reliability and usability, not bypass security.

Pro can add:

- higher bandwidth;
- more concurrent streams;
- longer idle timeout;
- reserved/priority capacity;
- better region selection;
- lower latency routing;
- higher file upload cap;
- longer WebRTC session support if needed;
- persistent named relay endpoint for a workspace;
- team/org audit logs;
- custom relay region/pool;
- optional customer-managed relay.

Pro should not add:

- unauthenticated tunnels;
- raw shell by default;
- shared owner keys;
- unlimited arbitrary TCP forwarding;
- guests inheriting owner privileges;
- long-lived bearer tokens without rotation;
- opaque support access into customer machines.

Relay Pro support tooling must be metadata-only by default. If support needs to
inspect a live session, it should require explicit owner approval and produce an
audit event.

### Tier Separation

Free and Pro can run on separate relay pools, but the protocol should be the
same:

```text
client/agent -> relay catalog -> selected relay pool -> signed tunnel/request
```

Tier selection should happen before connection:

```ts
type RelayTier = "public-free" | "relay-pro" | "customer-managed";

interface RelayCandidate {
  id: string;
  tier: RelayTier;
  region: string;
  httpUrl: string;
  quicAddr?: string;
  supportsReverseSsh: boolean;
  supportsWebRtcSignalling: boolean;
  maxStreams: number;
  priority: number;
}
```

The agent and mobile app should not hardcode public relay hostnames. They should
consume a signed relay catalog from the control plane, cache it briefly, and
validate that each relay presents a Yaver relay certificate.

### Relay Catalog Security

Convex/control-plane can publish relay candidates, but clients should verify:

- catalog signature;
- relay certificate chain or pinned Yaver relay public key;
- relay id matches certificate subject/claim;
- relay tier and capabilities are signed data;
- catalog expiry is short;
- stale catalog falls back to cached known-good only briefly.

This prevents a malicious/stale control-plane response from silently redirecting
machines to an attacker-controlled relay.

### Auth Handshake

Target handshake:

```text
agent -> relay:
  device certificate
  signed nonce
  tunnel intent id
  tunnel capability set

phone/web -> relay:
  account session
  device/session certificate
  signed request nonce
  task/session capability token

relay:
  verifies both sides
  checks access graph/tier/quota
  forwards bytes

agent:
  verifies request envelope again
  executes only allowed capability
```

Relay should return explicit error classes:

- `missing_relay_credential`
- `bad_relay_credential`
- `expired_relay_credential`
- `expired_account_session`
- `unknown_device`
- `device_not_connected`
- `tunnel_generation_replaced`
- `quota_exceeded`
- `tier_limit_exceeded`
- `relay_overloaded`
- `capability_denied`

Never collapse these into a generic "auth failed". The app needs to know whether
to refresh credentials, ask the user to sign in, wait, switch relay, or tell the
user the machine is offline.

### Quotas And Abuse Controls

Relay is the easiest public surface to abuse, especially the free tier.

Controls:

- per-user rate limits;
- per-device rate limits;
- per-IP soft limits;
- per-session byte limits;
- per-capability limits;
- max concurrent tunnels per account;
- max concurrent streams per tunnel;
- max idle duration;
- max absolute session duration;
- bounded request body sizes;
- bounded log/event frame sizes;
- backpressure instead of unbounded buffering;
- circuit breakers for noisy accounts/devices;
- automatic downgrade from media/bulk traffic before control traffic suffers.

Control-plane actions like `stop task`, `health`, `revoke guest`, and `close
tunnel` should be in a priority lane that is not starved by bulk file transfer
or app preview traffic.

### Data Minimization

Relay can log:

- relay id;
- user id hash;
- machine id hash;
- session id;
- transport kind;
- bytes in/out;
- duration;
- error class;
- region;
- tier;
- capability class.

Relay must not log:

- prompts;
- model outputs;
- tmux text;
- file names if avoidable;
- source code;
- environment variables;
- API keys;
- SSH keys;
- raw hostnames/IPs in analytics destined for public docs or support exports.

Operational logs may need transient network addresses for abuse mitigation, but
they should be access-controlled, retained briefly, and redacted before product
surfaces or shared debugging bundles.

### Guest Access On Relay

Guest access is where relay bugs become privilege bugs.

Rules:

- guest tunnel/session tokens must be guest-scoped;
- guest must never inherit owner relay credentials;
- guest capabilities must include machine/project/runner restrictions;
- guest revoke closes active relay streams;
- expired guest invite cannot open a new relay session;
- hidden/revoked invitations must not reappear in host UI;
- relay should reject guest stream attach if agent says task/project is outside
  grant scope.

Guest stream attach should require both:

```text
Convex/control-plane grant says guest can access this target
Agent local policy says this task/project is shareable
```

### Reverse SSH In Relay Tiers

Reverse SSH should be treated differently by tier:

Public Free:

- disabled by default;
- enabled only for pairing/recovery or explicit owner debug;
- short lifetime;
- forced-command only;
- no arbitrary remote port;
- no guest raw shell;
- strict byte/time limits.

Relay Pro:

- can be always-available for selected machines;
- can keep longer idle windows;
- can support named workspace endpoints;
- can support more port forwards;
- still forced-command by default;
- raw shell remains explicit owner-only debug.

Customer-managed relay:

- same protocol;
- customer owns relay infrastructure;
- Yaver still recommends forced-command and capability envelopes;
- support tooling should clearly mark it as customer-managed.

### Relay Pro Is Not A Trust Upgrade

Paid relay may be more reliable, but the agent should not trust it more.

The agent should treat:

```text
public-free relay
relay-pro
customer-managed relay
direct LAN
VPN
```

as different byte paths with the same authorization requirement. The only
difference is transport metadata and policy limits.

### Mode D: Yaver Mesh

Best long-term direct path.

Path:

```text
Phone/machine/cloud workspace -> Yaver WireGuard-like mesh -> agent
```

Security:

- Mesh private key generated locally.
- Convex stores public keys, assigned overlay IPs, ACLs, and endpoint metadata.
- ACLs are least-privilege:
  - same user devices by default;
  - guests only for explicitly shared machines/projects;
  - no open subnet routing by default.

Mesh must not shadow Tailscale or other overlays. Route conflict doctor probes
must block unsafe bring-up.

### Mode E: Reverse SSH Through Relay

Use as a fallback/debug transport, not as the product spine.

Path:

```text
Machine -> outbound reverse SSH tunnel -> Yaver relay
Phone -> Yaver API/relay -> reverse tunnel -> agent shim
```

This is valuable because SSH is mature and survives many networks. But raw SSH
must be bounded:

- no owner shell by default;
- no unscoped port forwarding;
- no indefinite sessions;
- no relay-controlled command execution;
- no shared private keys;
- no global `authorized_keys` mutation without a managed marker.

The machine should run a Yaver-managed SSH endpoint or forced-command wrapper:

```text
command="/usr/local/bin/yaver ssh-session --session <id>",no-agent-forwarding,no-X11-forwarding,no-pty ssh-ed25519 ...
```

For normal task execution, the wrapper should expose Yaver verbs:

- `health`
- `run-task`
- `attach-tmux`
- `stop-task`
- `list-projects`
- `open-port`
- `upload-bundle`

Only explicit debug mode should allocate a raw shell, and it should be owner-only
with a visible audit event.

## SSH Key Strategy

### Do Not Use The User's Personal SSH Key By Default

Yaver should not ask a normie for `~/.ssh/id_ed25519`.

Instead:

1. Yaver agent generates a Yaver-managed SSH keypair locally.
2. The private key stays in local Yaver vault/keychain.
3. The public key is installed only into Yaver-managed forced-command access.
4. The key is named and marked:

```text
# yaver-managed: device=<deviceId> created=<timestamp> owner=<userId>
ssh-ed25519 AAAA... yaver-managed-<deviceId>
```

For a cloud workspace, the key can be injected at provisioning time, but still
scoped to the Yaver user/workspace and revocable without touching personal keys.

### Key Rotation

Rotate on:

- user requests "reset access";
- suspected compromise;
- device detached;
- machine ownership transfer;
- guest access revoked where a guest-specific key was used;
- certificate epoch changes.

Rotation should be idempotent:

1. Generate new local key.
2. Install new public key.
3. Test forced command.
4. Mark new key active in local vault.
5. Remove old managed key.
6. Patch Convex metadata with new public fingerprint only.

Never remove unknown user keys.

### Per-Guest SSH

Guest access should not reuse owner SSH material.

Options:

- Prefer no SSH key for guests: route them through Yaver task/session
  capabilities over relay/QUIC.
- If SSH fallback is needed, mint a guest-scoped forced-command key with
  project/machine/task restrictions and expiry.

Guest revocation must:

- revoke Convex guest grant;
- invalidate session capability tokens;
- remove guest forced-command key if installed;
- close active reverse tunnels for that guest;
- stop exposing hidden/revoked invitation rows to the host UI.

## Reverse SSH Design

### Tunnel Establishment

Machine creates outbound connection:

```text
yaver serve
  -> asks Convex for tunnel intent
  -> receives short-lived relay endpoint + tunnel token
  -> opens reverse SSH with device cert proof
  -> relay marks tunnel live
```

Tunnel identity:

```json
{
  "tunnelId": "tun_...",
  "machineId": "d_...",
  "userId": "u_...",
  "certId": "cert_...",
  "allowedTargets": ["agent-control", "tmux", "port-forward"],
  "expiresAt": 0,
  "maxIdleMs": 120000,
  "generation": 12
}
```

### Relay Behavior

Relay should:

- verify device certificate and signed tunnel nonce;
- reject expired tunnel tokens;
- map tunnel to one machine id and user id;
- enforce max idle and max lifetime;
- log metadata only;
- never log command text, file content, API keys, env vars, or terminal output;
- close old tunnel generation when a new one for the same machine becomes live;
- expose `relay presence` separately from `agent health`.

Relay should not:

- hold SSH private keys;
- decide command authorization alone;
- allow arbitrary TCP forwarding by default;
- allow cross-user tunnel discovery.

### Agent Side

The agent should:

- own local private keys;
- verify every Yaver request envelope;
- enforce local policy;
- bind task to tmux session id;
- emit structured task events;
- apply guest/project access filters locally;
- expose diagnostics for each transport.

If reverse SSH is enabled, the agent should maintain:

```json
{
  "reverseSsh": {
    "enabled": true,
    "state": "connected",
    "relayId": "public-free",
    "tunnelId": "tun_...",
    "lastConnectedAt": 0,
    "lastErrorClass": null,
    "forcedCommandInstalled": true,
    "keyFingerprint": "SHA256:..."
  }
}
```

## Transport Selection Algorithm

Yaver should choose the cheapest reliable path, not the fanciest one.

Priority:

1. Existing healthy focused transport.
2. LAN direct.
3. VPN/Tailscale direct.
4. Yaver Mesh direct.
5. WebRTC data channel if already negotiated for preview/session.
6. Relay QUIC/HTTP tunnel.
7. Reverse SSH tunnel.
8. Manual recovery/debug.

The selector should be capability-aware:

| Capability | LAN/VPN | Mesh | Relay | WebRTC | SSH | Reverse SSH |
|---|---:|---:|---:|---:|---:|---:|
| health | yes | yes | yes | yes | yes | yes |
| task run | yes | yes | yes | yes | yes via wrapper | yes via wrapper |
| tmux attach | yes | yes | yes | yes | yes | yes |
| project scan | yes | yes | yes | maybe | yes | yes |
| file upload | yes | yes | yes | yes | sftp/scp/wrapper | wrapper |
| port forward | yes | yes | yes | yes | yes | yes |
| WebRTC preview | signalling only | signalling only | signalling only | yes | no | no |
| Redroid/app streaming | signalling/control | signalling/control | signalling/control | media/data | no | no |
| emergency shell | owner only | owner only | owner only | no | yes | yes |

The selector must also be cost-aware:

- prefer direct paths over relay when stable;
- avoid repeated unroutable VPN candidates;
- stop probing non-primary stale machines in the background;
- keep relay fallback hot only for active/primary/secondary;
- close reverse tunnels when idle;
- sleep/delete cloud workspaces when no active session exists.

## Convex Bootstrap Role

Convex should help with:

- account login;
- device registry;
- pending pairing codes;
- public device keys/certificates;
- relay/mesh server catalog;
- user settings;
- guest grants;
- cloud workspace metadata;
- billing tier and free-trial entitlements;
- transport intents.

Convex should not be used for:

- raw SSH keys;
- terminal output;
- file contents;
- app source bundles;
- provider API credentials;
- long-lived relay tunnel secrets;
- raw task prompts unless explicitly intended by product policy.

### Bootstrap Tables / Fields

Target conceptual records:

```ts
devices: {
  userId,
  deviceId,
  publicKey,
  encryptionPublicKey,
  certificateEpoch,
  hardwareHash?,
  agentVersion?,
  capabilities,
  lastHeartbeat,
  lastRelayPresence?,
  lastMeshPresence?,
}

deviceCertificates: {
  userId,
  deviceId,
  certId,
  publicKey,
  capabilities,
  generation,
  expiresAt,
  revokedAt?,
}

transportIntents: {
  userId,
  deviceId,
  requestedByDeviceId,
  kind: "relay" | "mesh" | "reverse-ssh" | "webrtc",
  sessionId,
  expiresAt,
  consumedAt?,
}

transportSessions: {
  userId,
  deviceId,
  kind,
  sessionId,
  capabilityHash,
  state,
  createdAt,
  expiresAt,
  lastSeenAt,
}
```

Store hashes/fingerprints where possible. Store secrets only if encrypted with a
deployment key and only when there is no local alternative.

## Request Envelope

Every agent operation should be wrapped:

```json
{
  "v": 1,
  "requestId": "req_...",
  "userId": "u_...",
  "deviceId": "d_...",
  "sessionId": "sess_...",
  "capability": "task.run",
  "bodyHash": "sha256:...",
  "issuedAt": 0,
  "expiresAt": 0,
  "nonce": "...",
  "signature": "ed25519:..."
}
```

The signature should be from:

- phone device key for interactive phone commands;
- web session key for web commands;
- cloud workspace control key for managed workspace commands;
- agent key for machine-originated events.

The agent verifies:

1. signature valid;
2. certificate valid;
3. capability allowed;
4. session not expired;
5. nonce not replayed within window;
6. local owner/guest policy allows the target project/runner/task;
7. transport binding matches session if configured.

## User-Facing Setup

### Mobile

Primary screens:

- `Connect a machine`.
- `Start project`.
- `Tasks`.
- `Projects`.
- `Settings > Connections`.

Avoid exposing SSH unless advanced debug is opened.

Recommended copy:

```text
Machine ready
Path: Relay
```

Advanced:

```text
Transport
LAN failed: not reachable from this network
VPN skipped: no route
Relay: connected
Reverse SSH: standby
```

### Desktop

Desktop app should:

- generate keys on first run;
- show QR/link pairing;
- manage local vault;
- show machine health;
- show active tasks/tmux sessions;
- show transport status;
- allow reset access;
- allow optional "Enable SSH fallback";
- allow optional "Enable Yaver Mesh".

Default desktop setup should not ask the user to edit `authorized_keys`.

### CLI

CLI should support:

```text
yaver doctor transport
yaver doctor relay
yaver doctor ssh
yaver doctor mesh
yaver transport status
yaver transport reset
yaver ssh enable --forced-command
yaver ssh disable
yaver mesh up
yaver mesh down
```

Every command must be safe:

- list before destructive changes;
- only remove Yaver-managed keys;
- never remove unknown SSH config;
- print why a probe failed.

## Diagnostics

Every transport must expose real capability probes, not inventory-only checks.

Doctor probes:

| Probe | Real Attempt |
|---|---|
| relay configured | agent registers tunnel and phone probes `/health` through relay |
| relay auth | signed request with current relay credential |
| direct LAN | bounded `/health` probe with current auth headers |
| VPN/Tailscale | bounded `/health`, negative cache unroutable routes |
| mesh | route conflict check + overlay `/health` |
| SSH local key | forced command `yaver ssh-session health` |
| reverse SSH | relay tunnel open + forced command health |
| tmux attach | create/attach/read/stop disposable test session |
| project scan | scan bounded temp fixture, not user home |

Mobile/web should surface the same results. A CLI-only diagnosis does not exist
for a phone-first product.

## Idle And Cost Control

Yaver should treat transports as active resources.

Rules:

- Keep active task transports alive.
- Keep active preview/WebRTC transports alive.
- Keep primary/secondary warm if the user recently used them.
- Close reverse SSH after idle timeout.
- Close relay session after idle timeout unless the machine uses it for
  presence.
- Do not keep cloud compute running just because a tunnel exists.
- For managed workspaces, persist state, then stop/delete compute according to
  provider semantics.

For cloud providers:

- Hetzner: stopped servers still cost money; delete server and keep volume or
  snapshot state.
- AWS/GCP/Azure: stop when possible, but verify disk/IP/NAT costs; avoid
  provider-specific features in the abstraction unless hidden behind adapters.

## Yaver Serverless Implications

Yaver Serverless apps built by users should deploy through the same machine
transport layer.

Use cases:

- user starts a project from mobile;
- Yaver creates RN/TS + Yaver Serverless backend scaffold;
- runner edits/builds/tests;
- app deploys to a user/workspace runtime;
- database can be exported/dumped;
- app can be self-hosted later.

Transport needs:

- upload source bundle;
- execute build;
- stream logs;
- expose dev preview port;
- run migrations;
- dump database;
- deploy or tear down runtime;
- sleep idle compute.

Do not tie Yaver Serverless to Convex internals. Convex is for Yaver product
control plane; user-built apps should use Yaver Serverless primitives and be
exportable/self-hostable.

## Open-Source Safety

Because Yaver is open source:

- protocol security cannot rely on hidden request shapes;
- all privileged actions require signed capabilities;
- all private keys stay local;
- cert issuer keys stay in protected deployment secrets;
- relay credentials are short-lived and scoped;
- provider credentials are never committed;
- examples use placeholder hostnames and aliases;
- tests assert secret redaction for remote URLs and git remotes;
- docs avoid real infra IPs/hostnames.

Implementation guardrails:

- add secret scanners in CI for provider keys, relay passwords, SSH keys;
- add tests that sanitized git remotes never leak tokens into project cache;
- add tests that Yaver-managed SSH cleanup leaves unknown keys untouched;
- add tests that revoked guests disappear from host surfaces and cannot attach
  to existing task streams;
- add tests that reverse SSH forced command rejects unknown capabilities.

## Implementation Plan

### Phase 1: Transport Contract

Create shared transport interfaces:

- `MachineTransport`
- `TransportSelector`
- `TransportCapability`
- `TransportHealth`
- `TransportSession`

Move mobile/web/agent call sites toward:

```text
select transport -> signed Yaver request -> structured response/events
```

Do not let new screens call raw SSH or relay-specific endpoints directly.

### Phase 2: Local Keys And Certificates

Implement:

- local Ed25519 device key generation;
- key storage in vault/keychain;
- public key registration;
- certificate issuance;
- certificate refresh;
- certificate revocation epoch;
- local certificate verification helper in the Go agent.

Add doctor:

```text
yaver doctor identity
```

It should verify "private key can sign", not just "key file exists".

### Phase 3: Relay Device-Signature Auth

Move relay from password-first to certificate/signature-first:

- signed tunnel registration;
- signed phone request;
- session capability validation;
- password support only as migration fallback;
- explicit error classes:
  - missing relay credential;
  - bad relay credential;
  - expired account session;
  - device not connected to relay;
  - relay overloaded;
  - tunnel collision.

### Phase 4: SSH Adapter

Implement local SSH adapter behind the transport interface:

- direct SSH over LAN/VPN only;
- Yaver-managed key generation;
- forced-command install;
- `health` command;
- `tmux attach` command;
- no raw shell by default.

Add:

```text
yaver ssh enable
yaver ssh doctor
yaver ssh disable
```

### Phase 5: Reverse SSH Adapter

Implement reverse SSH only after Phase 4 is safe:

- short-lived tunnel intents;
- relay verifies device certificate;
- forced-command wrapper only;
- idle timeout;
- generation-based replacement of old tunnels;
- explicit owner-only raw shell escape hatch.

### Phase 6: UX Simplification

Mobile:

- show connection status in one line;
- show path in advanced/debug;
- show tmux id at task level;
- expose retry/reset access;
- hide transport choice by default.

Desktop:

- QR pairing;
- local key/cert status;
- optional SSH fallback toggle;
- transport doctor.

Web:

- same status vocabulary as mobile;
- no raw secrets;
- no transport-specific drift.

## Acceptance Criteria

Yaver should be considered ready when:

- A fresh normie user can pair a machine without opening router settings.
- Same-LAN uses direct path automatically.
- Cellular uses relay automatically.
- VPN/Tailscale direct works when available but backs off when unroutable.
- Reverse SSH can recover a machine without granting raw shell by default.
- A revoked guest cannot attach to old task streams.
- Relay compromise alone cannot execute commands.
- Convex compromise alone cannot execute commands without device private keys.
- Machine reset rotates local device credentials and invalidates old sessions.
- Mobile, web, CLI, and MCP all report the same transport status.
- All examples and logs redact secrets, IPs, hostnames, and provider credentials.

## Recommended Product Stance

Use SSH and reverse SSH, but keep them behind Yaver.

The user buys:

```text
Cloud workspace + remote runner + app preview + deploy.
```

They should not buy:

```text
An SSH tutorial.
```

The engineering rule is:

```text
Yaver protocol owns identity, capability, task state, and UX.
Transports only move bytes.
```
