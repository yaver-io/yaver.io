# Host-Backed Guest Coding Sessions

## Goal

Define a Yaver mode where:

- the **host** already has Yaver installed on their machine
- the host also has premium/local coding tools on that machine such as `claude`, `codex`, `aider`, browsers, build tools, emulators, SDKs, and stronger CPU/RAM
- the **guest** installs only the Yaver Go agent
- the host generates a **share link / share code** from the Yaver agent
- the guest accepts that link/code in their own Yaver agent
- the guest gets a **terminal-first coding experience** that feels close to Claude Code / Codex
- the actual execution uses the **host's infra and host's installed runners/tools**
- the guest can vibe-code their own repo or a brokered shared repo without needing to install Claude/Codex locally

This is not the same as the current guest-sharing model.

Current guest-sharing is mostly "let someone use parts of my machine under policy".

This spec defines a stronger and more productized mode:

**host-backed guest coding sessions**

Where the host becomes a remote execution substrate for the guest's coding workflow.

## Current Implementation Slice

The repo now has a concrete minimum viable slice of this model:

- host-share invite lifecycle
- host-share runtime admission on the host
- borrowed workspace management
- guest-driven repo attach via `yaver host-share attach-repo`
- guest/host repo sync via `yaver host-share sync-repo --to-host|--from-host`
- host-controlled immediate session stop via `yaver host-share end <session-id>`
- remote transport candidate resolution using direct addresses, public HTTPS endpoints, and relays

This means the "guest keeps the repo on their own machine but borrows the
host's Codex/tools" flow is now real in the codebase.

Important current caveats:

- the guest repo root must already be discovered locally by Yaver
- sync-back is still text-oriented and skips non-UTF-8 files
- terminal access is still a powerful host capability and should remain policy-gated
- this is a practical host-share slice, not yet a separate `borrowed-session` runtime product

Current host control knobs:

- invite creation supports `--session-ttl-min` and `--idle-timeout-min`
- the host can revoke the invite entirely
- the host can end a live session immediately by session id

## Short Product Statement

Yaver should support:

> "I have Claude Code / Codex / build tools / strong hardware on my MacBook or Mac mini. My friend only installs Yaver. I generate a link or code. They join from their own machine. They get a terminal-style coding UI, but their prompts and repo operations run on my machine, using my tools and infra, under my permission policy and time limits."

## Target UX

### Host flow

Host runs a command like:

```bash
yaver host-share create \
  --mode borrowed-runner \
  --tools claude,codex \
  --infra cpu,ram,build,devserver \
  --repo-mode guest-repo \
  --session-ttl 8h \
  --idle-timeout 30m \
  --cpu 60 \
  --rammb 8192
```

The agent returns:

- a short share code, example: `K7M4QZ`
- a share link, example: `yaver://join/K7M4QZ` or `https://yaver.io/join/K7M4QZ`
- a human-readable summary of the granted capabilities

### Guest flow

Guest installs only Yaver:

```bash
yaver auth
yaver host-share join K7M4QZ
yaver host-share attach-repo --session <session-id> --path ~/code/my-repo
```

or:

```bash
yaver host-share join https://yaver.io/join/K7M4QZ
yaver host-share sync-repo --session <session-id> --to-host
yaver host-share sync-repo --session <session-id> --from-host
```

Then the guest gets a session-oriented terminal UX, for example:

```bash
yaver code --borrowed
```

or:

```bash
yaver shell
```

Inside that terminal UX, the guest should feel like they are using a coding agent directly:

- ask for code changes
- run builds
- stream tool output
- inspect diffs
- run dev loops
- use Claude/Codex if the host allowed them

But all of that is actually brokered through the host machine.

## Exact Model

### What the guest has locally

- Yaver agent
- optional local editor
- optional local git
- optional local repo checkout
- no need for `claude`, `codex`, Apple tooling, Android tooling, Docker, or heavy local models

### What the host provides

- runner binaries and credentials
- CPU/RAM
- optional Docker/container runtime
- optional browsers/emulators/build chains
- optional dev server and reverse-expose capability
- optional host-managed API keys

### What the guest should perceive

- "I am using Yaver as my coding terminal"
- "I can code from my own machine"
- "Heavy execution runs elsewhere"
- "The host has granted me certain tools and limits"

## Non-Goals

This mode should **not** mean:

- raw owner-equivalent shell access to the host
- unrestricted `/exec`
- unrestricted `/ws/terminal`
- unrestricted vault or secret access
- unrestricted access to the host's home directory
- unrestricted access to all host repos
- direct use of host git credentials by default
- turning the current `guests` feature into a giant owner bypass

## Why This Is A Separate Product Surface

The repo already has guest-sharing, but that model is not enough.

Existing code shows:

- guest scopes are enforced by path allowlists in `desktop/agent/guest_scope.go`
- guest access is filtered in auth middleware in `desktop/agent/httpserver.go`
- guest task execution is project-scoped and strips dangerous fields in `desktop/agent/httpserver.go`
- reverse reachability already exists through the relay layer
- remote same-owner MCP proxying already exists

That is a strong base, but it is still centered on:

- host-owned resources
- host-selected projects
- HTTP surfaces
- "safe teammate / end-user" access

It is not yet a clean tenancy model for:

- guest-owned coding sessions
- guest-owned repos
- terminal-grade interaction
- host-backed runners as a service

This feature should therefore ship as a separate surface, not as a few new booleans under `yaver guests config`.

## Core Concept: Borrowed Runner Session

Introduce a first-class entity:

`BorrowedRunnerSession`

This represents:

- one host
- one guest
- one active lease
- one repo/workspace mode
- one tool policy
- one resource policy
- one network policy
- one terminal/chat stream

It is the core unit for lifecycle, audit, revocation, and reconnection.

## Session Modes

### 1. Guest Repo Mode

The guest is working on their own repo.

Recommended behavior:

- guest selects a local repo
- Yaver creates a **brokered mirrored workspace** on the host
- sync guest repo -> host workspace
- host runners operate only inside that mirrored workspace
- outputs, diffs, and events stream back to the guest

This is the primary mode for the user's requested scenario.

### 2. Host Repo Mode

The host allows the guest to work on a host-owned repo.

Recommended behavior:

- host allowlists one or more repos
- guest session is pinned to one repo
- all work remains inside that repo boundary

This overlaps with the current guest project-scoping model and can reuse parts of it.

### 3. Shared Scratch Mode

No durable repo.

Useful for:

- pair debugging
- trying prompts
- temporary experiments
- generating patches or snippets

## Repo Strategy

### Recommended approach: mirrored workspace

Do not run the guest's coding session against a live remote-mounted filesystem.

Instead:

1. guest chooses repo
2. guest agent computes repo identity and sync contract
3. host agent creates a session workspace, for example:

```text
~/.yaver/borrowed-runners/<session-id>/workspace
```

4. guest agent pushes initial snapshot
5. host agent applies incremental updates
6. runners operate only in that workspace
7. results are sent back as:
   - streamed terminal output
   - structured file changes
   - patch sets
   - optional synced file writes

### Why mirrored workspace is better

- safer than exposing arbitrary host or guest filesystems
- resumable after disconnects
- easier to isolate in Docker
- easier to audit
- compatible with relay transport
- easier to enforce TTL and cleanup

## Permission Model

Permissions should be explicit and composable.

### A. Tool permissions

Examples:

- `runner:claude`
- `runner:codex`
- `runner:aider`
- `runner:browser`
- `runner:testkit`
- `runner:build-ios`
- `runner:build-android`
- `runner:deploy-web`

### B. Infra permissions

Examples:

- `infra:cpu`
- `infra:ram`
- `infra:container`
- `infra:devserver`
- `infra:build`
- `infra:artifact-store`

### C. Repo permissions

Examples:

- `repo:guest-mirror`
- `repo:host-allowlisted`
- `repo:read-only`
- `repo:write`
- `repo:commit`
- `repo:push`

### D. Network permissions

Examples:

- `network:none`
- `network:egress-approved`
- `network:devserver-only`
- `network:tunnel-allowlisted`
- `network:public-expose-disabled`

### E. Secret permissions

Examples:

- `secrets:none`
- `secrets:host-managed-runtime-only`
- `secrets:guest-supplied-session-only`
- `secrets:no-raw-readback`

## Time Model

This feature needs four different timers.

### 1. Invite TTL

How long the link/code remains redeemable.

Examples:

- 15 minutes
- 1 hour
- 24 hours

### 2. Session TTL

Maximum lifetime of the borrowed runner session.

Examples:

- 1 hour
- 8 hours
- 24 hours

### 3. Idle Timeout

If terminal/chat/task activity stops, suspend or end the session.

Examples:

- 10 minutes
- 30 minutes
- 2 hours

### 4. Revocation Grace

How long already-issued short-lived tokens are tolerated after host revokes.

Target:

- short enough to feel immediate
- long enough to avoid stream corruption

Suggested initial target:

- 15 to 60 seconds

## Install And Autoserve Permission Strategy

The borrowed-runner product will fail in practice if Yaver waits to ask
for permissions only when a feature is first used.

That works for expert users.
It fails for the requested "host is wise, guest is dummy" setup.

For this mode, Yaver should prefer:

**ask early, cache capability state, degrade clearly later**

instead of:

**surprise-fail later when a deep feature finally needs permission**

### Principle

On initial install, first auth, or first interactive autoserve, Yaver
should proactively walk the user through permissions and prerequisites
that may be needed later for:

- remote execution
- terminal sessions
- screen capture
- browser automation
- desktop control
- microphone / voice
- dev server exposure
- relay / tunnel networking
- Docker isolation
- long-running background service mode

The user should be able to skip, but Yaver must then record:

- what was requested
- what was granted
- what is still missing
- which future features will degrade

### Why this matters for the requested scenario

In this host-backed sharing model:

- the host is likely technical
- the guest is likely not
- the guest should not need to reason about OS permissions or networking
- the host should not get halfway through a shared coding session and then hit
  an avoidable "screen recording not granted", "daemon not backgrounded",
  "Docker unavailable", or "relay not configured" failure

So Yaver should front-load the painful parts.

### Permission buckets

#### 1. OS interaction permissions

Examples:

- Accessibility
- Screen Recording
- Automation / Apple Events
- Microphone
- Notifications
- Background task / login item / service install

Current repo already has a lightweight macOS onboarding checklist in
`desktop/agent/macos_permissions.go`.

For borrowed-runner mode, expand that idea into a more explicit
capability audit and onboarding flow.

#### 2. Runtime/tool prerequisites

Examples:

- Docker installed and usable
- Node present for dev flows
- git present
- runner binaries present: `claude`, `codex`, `aider`
- browser runtime available if browser automation is granted
- Xcode/Android toolchain present if build permissions are granted

#### 3. Network prerequisites

Examples:

- same-LAN discovery healthy
- relay config healthy
- Tailscale present and logged in if enabled
- Cloudflare Tunnel configured if enabled
- UDP buffer tuning on WSL2 if needed
- firewall exceptions where relevant

#### 4. Service-mode prerequisites

Examples:

- systemd service on Linux
- launch agent / login item on macOS
- scheduled task / service on Windows
- keep-awake or power policy hints for host machines that should stay reachable

### New onboarding commands

Suggested commands:

```bash
yaver permissions audit
yaver permissions grant-all-safe
yaver autoserve doctor
yaver host-share prepare
```

### `yaver host-share prepare`

This should be the "wise host" command.

It should:

1. detect the host OS
2. audit all relevant permissions and prerequisites
3. explain what future borrowed-runner sessions may need
4. offer to open/fix/install what it can
5. persist a capability manifest
6. warn clearly about what remains degraded

Example:

```bash
yaver host-share prepare --for borrowed-runner
```

Possible output:

```text
Host Share Preparation

OS permissions
- Accessibility: missing
- Screen Recording: granted
- Automation: missing
- Microphone: granted

Runtime
- Docker: available
- codex: installed
- claude: installed
- browser automation: missing browser runtime

Networking
- Same LAN: healthy
- Yaver relay: healthy
- Tailscale: not configured
- Cloudflare Tunnel: not configured

Service mode
- Auto-start on login: disabled
- Background agent health: healthy

Result
- Borrowed coding sessions: supported
- Terminal-backed sessions: supported
- Browser automation: degraded
- Desktop control: degraded
```

### Autoserve behavior

On first `yaver serve`, `yaver auth`, or any future autoserve path,
Yaver should:

- detect whether the machine looks like a likely host machine
- if interactive, offer the full host-share prep checklist
- if non-interactive, print a concise deferred warning and a one-command fix

Yaver should not require every permission to start serving.

It should:

- ask early
- remember results
- avoid nagging repeatedly
- but keep the capability map visible via `yaver status`, `yaver doctor`,
  and `yaver host-share prepare`

### Safe defaults

For non-expert users, the default installer/autoserve flow should try to
prepare for likely future needs even if the feature is not used immediately.

Examples:

- offer system service install
- offer relay setup or free-relay enrollment
- offer macOS permission walkthrough
- offer Docker install guidance
- offer Tailscale / Cloudflare Tunnel detection but not require them

## Connectivity And Transport Plan

This borrowed-runner mode must work across all common connectivity cases:

- same LAN
- Tailscale
- Cloudflare Tunnel
- Yaver self-hosted relay
- Yaver free relay
- mixed / degraded environments such as WSL2 or NAT-heavy networks

The guest should not need to understand these distinctions.

The host may understand them, but Yaver should still automate the choice.

### Product rule

The guest connects to a **session**, not to a transport.

Yaver chooses the transport.

### Transport priority order

Recommended order:

1. same LAN direct
2. Tailscale direct
3. Cloudflare Tunnel HTTPS path
4. configured Yaver relay
5. Yaver free relay

This order should be capability-aware.

For example:

- same LAN may be best for low latency and dev server work
- Tailscale may be best when both sides are already on the tailnet
- Cloudflare Tunnel may be best for stable HTTPS exposure
- relay may be best when there is no other viable path

### Transport abstraction

Introduce a session-facing resolver:

`SessionTransportResolver`

Inputs:

- host device info
- guest device info
- host capability manifest
- guest capability manifest
- session needs

Session needs examples:

- PTY stream only
- repo sync + PTY
- repo sync + PTY + dev server preview
- build artifact transfer
- browser automation

Output:

- selected transport
- fallback chain
- health state
- reconnect strategy

## Transport Matrix

### 1. Same LAN

Expected:

- best latency
- best for dev-server and preview flows
- best for live terminal responsiveness

Requirements:

- both machines reachable on local network
- local HTTP/TLS path healthy
- host agent running

Fallback:

- if discovery fails or network changes, move to Tailscale / Cloudflare / relay

### 2. Tailscale

Expected:

- stable direct overlay
- excellent for always-on host boxes
- avoids some relay costs

Requirements:

- both machines on same tailnet
- Tailscale reachable and authenticated

Fallback:

- if Tailscale unavailable, fall through to Cloudflare Tunnel or relay

### 3. Cloudflare Tunnel

Expected:

- stable HTTPS exposure
- useful when inbound reachability is otherwise hard
- good for control plane and stream paths

Requirements:

- tunnel configured on host
- hostname healthy
- TLS valid

Caveat:

- not every raw bidirectional or low-latency stream should assume Cloudflare
  is ideal; session resolver should use it when it fits the stream model

### 4. Yaver self-hosted relay

Expected:

- full Yaver-native fallback
- host-controlled
- suitable default when the host is advanced enough to run their own relay

Requirements:

- relay config valid
- QUIC path healthy
- password/cert setup healthy

### 5. Yaver free relay

Expected:

- zero-knowledge-friendly fallback from the user's point of view
- easiest path for non-experts
- best default for "just make it work"

Requirements:

- free relay enrollment or default public relay availability
- host outbound connectivity

### WSL2 and hostile networks

The current repo already documents WSL2 relay failure modes and fixes in
`docs/wsl2-relay-troubleshooting.md`.

Borrowed-runner mode should treat this as a first-class transport risk.

That means:

- run transport diagnostics during host-share prep
- auto-apply WSL2 network tuning where supported
- clearly mark a host as "LAN-only", "relay-healthy", or "degraded"
- avoid letting the host create a borrowed-runner invite that promises
  off-LAN reliability when the machine only works on LAN

## Host-As-Expert / Guest-As-Novice Plan

The requested scenario assumes:

- host is the expert
- guest knows very little
- guest should not fail because of concepts, permissions, or networking

Yaver should explicitly optimize for that asymmetry.

### Host responsibilities

The host should be the one who:

- enables permissions
- selects allowed tools
- sets limits
- confirms transport readiness
- chooses whether host secrets can be used at runtime
- chooses whether guest repo mirroring is allowed

### Guest responsibilities

The guest should only need to:

- install Yaver
- authenticate
- paste a code or open a link
- choose a repo if the session allows guest-repo mode
- start coding

### Product implication

Do not put network choice, permission complexity, or secret policy in the guest flow.

The guest UI should say:

- what was granted
- what is currently available
- what is unavailable because the host disabled it

But it should not expect the guest to fix the host.

## Implementation Plan

### Phase A: host prep and capability audit

Ship:

- `yaver host-share prepare`
- capability manifest persisted locally
- permission and prerequisite audit
- transport health classification

Why first:

- prevents bad invites
- makes later session failures much rarer

### Phase B: invite and join protocol

Ship:

- `host-share create`
- `host-share join`
- invite TTL
- session TTL
- policy snapshot

### Phase C: borrowed-session runtime

Ship:

- session lifecycle manager
- terminal-first session stream
- host-backed runner broker
- reconnect and idle timeout behavior

### Phase D: transport resolver

Ship:

- direct / Tailscale / Cloudflare / relay / free-relay selection
- fallback order
- health signals in status and doctor

### Phase E: mirrored guest repo

Ship:

- guest snapshot upload
- incremental sync
- host-side mirrored workspace
- patch/writeback flow

### Phase F: richer borrowed capabilities

Ship optional grants for:

- browser automation
- dev servers
- builds
- artifact delivery
- controlled local tunnel forwarding

## Summary Plan

High-level plan:

1. Add a **host preparation** step so Yaver asks for likely-needed permissions
   and prerequisites early, especially during install / first auth / autoserve.
2. Add a **capability manifest** so the host machine knows what it can actually
   promise before generating a share code/link.
3. Add a **borrowed-session** runtime so the guest uses a terminal-first coding
   experience while execution happens on the host's runners and infra.
4. Add a **transport resolver** that automatically picks same-LAN, Tailscale,
   Cloudflare Tunnel, self-hosted relay, or free relay based on health and fit.
5. Keep the **host as the expert** and the **guest as the novice**: the host
   configures permissions and limits; the guest just installs Yaver, joins, and codes.
6. Make every shared session **policy-bounded** with TTLs, idle timeout,
   repo boundaries, resource caps, and no raw host ownership transfer.

## Transport Model

### Reuse the existing Yaver relay path

The current relay architecture is already suitable:

- outbound connection from the host
- no inbound port opening on host
- direct-first, relay-fallback behavior
- reverse path via relay to `/d/{deviceId}/...`

That means the borrowed runner session should use:

- direct when available
- relay when direct is unavailable

without inventing a second transport stack.

### Do not expose raw reverse tunnels by default

Generic reverse tunnel features should remain opt-in and policy-gated.

For this borrowed-runner mode, the transport should be **session-brokered**, not raw.

Good:

- repo sync stream
- PTY stream
- task event stream
- devserver preview stream

Bad as default:

- arbitrary localhost forwarding
- arbitrary host socket access
- arbitrary SOCKS-style proxying

## Terminal UX

The guest experience must feel terminal-first.

### Required properties

- full-screen or near-full-screen terminal feel
- streamed runner output
- clear current runner identity
- explicit host-backed banner
- command latency that feels interactive
- reconnect support
- visible session limits

### Suggested UX shape

On session start:

```text
Connected to host-backed Yaver session
Host: kivan-macbook
Runners: claude, codex
Infra: CPU, RAM, Docker, dev server
Repo mode: guest mirror
TTL: 7h41m remaining
Idle timeout: 30m
```

Then the guest can:

- type prompts
- switch allowed runner
- inspect status
- open a shell-like panel
- watch diffs and build output

### Important distinction

This should not pretend to be a raw shell first.

It is better modeled as:

- an **agent terminal**
- with optional shell sub-capabilities

rather than:

- a generic remote terminal where the user happens to run an agent

## Runner Execution Model

### Host-backed runner orchestration

When the guest selects `claude` or `codex`:

1. guest submits prompt/event
2. host validates policy
3. host starts runner inside session workspace
4. host streams output back
5. host records artifacts, diffs, exit status, cost hints

### Allowed runner set

The host should be able to grant:

- one runner only
- a subset
- all installed runners

Examples:

- `--tools codex`
- `--tools claude,codex`
- `--tools all-coding-runners`

### Host default vs guest-selected

Host policy should expose:

- default runner
- allowed runner list
- fallback runner

If the guest selects a disallowed runner, Yaver rejects before execution.

## Security Model

### Principle

This mode is **host-backed, guest-operated, policy-bounded**.

The guest is not the host.
The guest is not a same-owner peer.
The guest is not a trusted raw-shell principal.

### Hard boundaries

The guest must not get direct access to:

- host home directory
- host vault
- host raw API keys
- host SSH keys
- host git credentials
- host tmux/session inventory
- arbitrary host repos
- arbitrary local services

### Required enforcement layers

#### Layer 1: session policy gate

Every command/event/tool call must be checked against the session lease.

#### Layer 2: workspace boundary

Execution must be pinned to:

- guest mirrored workspace
- or explicit host allowlisted repo

#### Layer 3: runtime isolation

Guest-backed coding runs should default to:

- containerized execution
- no host home mount
- no host credential mount
- minimal env injection

#### Layer 4: network policy

Default deny for arbitrary tunneling and host-local service reachability.

#### Layer 5: audit and revocation

All session actions should be attributable to:

- host user id
- guest user id
- session id
- proxied-by device id
- selected runner

## Secrets Model

There are three secret classes.

### 1. Host secrets

Examples:

- host Claude/Codex auth
- Apple credentials
- Play Store credentials
- Cloudflare credentials
- host vault values

Rules:

- never readable by the guest
- may be usable only through approved execution paths
- no raw reveal

### 2. Guest secrets

Examples:

- guest OpenAI key
- guest npm token
- guest repo token

Rules:

- session-scoped injection only
- optional host refusal policy
- destroyed on session end

### 3. Session ephemeral secrets

Examples:

- repo sync token
- PTY reconnect token
- artifact stream token

Rules:

- short-lived
- revocable
- tied to one session

## Share Link / Share Code Design

### Host-side creation

New CLI:

```bash
yaver host-share create
```

Suggested output:

```json
{
  "ok": true,
  "shareCode": "K7M4QZ",
  "shareLink": "https://yaver.io/join/K7M4QZ",
  "mode": "borrowed-runner",
  "expiresAt": "2026-04-22T18:45:00Z",
  "sessionPolicy": {
    "allowedRunners": ["claude", "codex"],
    "cpuLimitPercent": 60,
    "ramLimitMb": 8192,
    "sessionTtlSec": 28800,
    "idleTimeoutSec": 1800
  }
}
```

### Guest-side redemption

New CLI:

```bash
yaver host-share join <code-or-link>
```

On success:

- store host association
- mint short-lived session bootstrap token
- show policy summary
- start session handshake

## Data Model Additions

### New backend entities

#### `borrowedRunnerInvites`

Fields:

- `hostUserId`
- `hostDeviceId`
- `inviteCode`
- `inviteLinkId`
- `mode`
- `allowedRunners`
- `allowedInfra`
- `repoMode`
- `sessionTtlSec`
- `idleTimeoutSec`
- `cpuLimitPercent`
- `ramLimitMb`
- `networkPolicy`
- `secretPolicy`
- `status`
- `expiresAt`
- `redeemedByGuestUserId?`
- `createdAt`

#### `borrowedRunnerSessions`

Fields:

- `sessionId`
- `hostUserId`
- `hostDeviceId`
- `guestUserId`
- `inviteId`
- `status`
- `workspaceMode`
- `workspacePath`
- `selectedRunner`
- `policySnapshot`
- `startedAt`
- `lastActivityAt`
- `expiresAt`
- `endedAt?`
- `endReason?`

#### `borrowedRunnerAudit`

Append-only log:

- `sessionId`
- `actor`
- `action`
- `runner`
- `target`
- `metadata`
- `createdAt`

## Agent Changes

### Host agent

Host agent needs new components:

#### 1. ShareManager

Responsible for:

- creating invites
- redeeming bootstrap requests
- revoking invites
- expiring invites

#### 2. BorrowedSessionManager

Responsible for:

- session lifecycle
- heartbeat
- idle timeout
- TTL enforcement
- reconnect tokens
- cleanup

#### 3. BrokeredWorkspaceManager

Responsible for:

- creating mirrored workspaces
- applying guest sync deltas
- cleanup after session end
- exposing safe file-change streams

#### 4. BorrowedRunnerBroker

Responsible for:

- allowed runner dispatch
- resource limits
- prompt stream plumbing
- cost/event reporting

### Guest agent

Guest agent needs:

#### 1. ShareJoinClient

- redeem code/link
- fetch host policy
- create session

#### 2. RepoMirrorClient

- snapshot local repo
- send incremental updates
- receive patch/diff/writeback events

#### 3. TerminalClient

- stream PTY-like bytes or structured runner events
- reconnect after relay disruptions
- show limits and status

## API Surface

Suggested host-side endpoints:

- `POST /host-share/create`
- `POST /host-share/revoke`
- `GET /host-share/list`
- `POST /host-share/redeem`
- `POST /borrowed-session/start`
- `POST /borrowed-session/heartbeat`
- `POST /borrowed-session/end`
- `GET /borrowed-session/status`
- `POST /borrowed-session/repo/snapshot`
- `POST /borrowed-session/repo/delta`
- `GET /borrowed-session/stream`
- `POST /borrowed-session/prompt`
- `POST /borrowed-session/runner/switch`

Suggested guest CLI:

- `yaver host-share create`
- `yaver host-share list`
- `yaver host-share revoke`
- `yaver host-share join`
- `yaver borrowed-session status`
- `yaver borrowed-session end`
- `yaver code --borrowed`

## Relationship To Existing Code

### Can be reused

- guest invitation backend shape in `backend/convex/guests.ts`
- relay transport
- cross-device proxy primitives
- runner selection logic
- task execution isolation fields
- terminal websocket machinery
- guest resource policy concepts

### Should not be reused blindly

- current guest path-prefix allowlist as the main product model
- raw owner-auth `/exec` and `/ws/terminal`
- same-owner MCP remote proxy as the guest execution path

Reason:

Those were built for either:

- safe guest sharing of existing host surfaces
- or same-owner machine-to-machine routing

This feature needs stronger tenancy and session identity.

## Phased Rollout

### Phase 1: Borrowed runner over host-owned repo

Ship the smallest valuable version:

- host creates invite
- guest joins
- host grants `claude` and/or `codex`
- repo is host-owned and allowlisted
- terminal-style prompt stream works
- session TTL + idle timeout work
- no guest repo mirroring yet

Why first:

- lower risk
- reuses current project scoping
- proves terminal UX

### Phase 2: Guest repo mirrored workspace

Add the real requested flow:

- guest picks local repo
- host creates mirrored workspace
- sync and execution happen there
- host-backed runner edits mirrored repo

This is the milestone that fully matches the user request.

### Phase 3: Brokered dev servers and previews

Add:

- host dev server start/stop
- preview stream
- limited expose support
- artifact fetch

### Phase 4: Build and publish extensions

Add optional grants for:

- iOS builds
- Android builds
- web deploys
- artifact signing/upload

## Recommended Defaults

For safety, default borrowed-runner invites should be:

- `repoMode=guest-repo-mirror`
- `allowedRunners=[codex]` or explicit host selection
- `sessionTtl=4h`
- `idleTimeout=20m`
- `requireIsolation=true`
- `hostSecrets=runtime-only`
- `guestSecrets=disabled`
- `network=egress-approved`
- `publicExpose=false`
- `rawTerminal=false`
- `shellSubcommands=brokered-only`

## Open Questions

### 1. Is the session chat-native or shell-native?

Recommendation:

- chat/agent-native first
- shell as a bounded sub-capability

### 2. Should guest writeback be automatic?

Recommendation:

- no
- start with explicit patch/file writeback
- optional auto-sync later

### 3. Can guest use host Claude/Codex credentials directly?

Recommendation:

- operationally yes, through brokered execution
- semantically no, never expose or export those credentials

### 4. Should this reuse `guests` or get a new noun?

Recommendation:

Use a new noun and command surface.

Suggested naming candidates:

- `host-share`
- `borrowed-runner`
- `host-backed-session`
- `remote-seat`

Best current fit:

- `host-share` for invite lifecycle
- `borrowed-session` for runtime lifecycle

## Final Recommendation

Implement this as:

1. a new **host-share** invitation model
2. a new **borrowed-session** runtime
3. a **brokered mirrored workspace** for guest repos
4. a **terminal-first guest UX**
5. strict policy gates around runners, infra, secrets, repo scope, and timeouts

Do not implement it as:

- "guest full scope plus `/exec`"
- "just let guest open `/ws/terminal`"
- "same-owner MCP proxy but with guest tokens"

That would be fast to prototype and wrong to ship.

The correct product is:

**host-backed borrowed coding sessions**

where Yaver lets one developer lend:

- their machine
- their coding runners
- their build stack
- their infra budget

to another developer,

without transferring raw host ownership.
