# Yaver Physical Computer Runtime — Architecture and TODO

Status: design handoff for a Yaver-owned implementation. Code remains the
source of truth; re-audit current routes and types before development.

## Goal

Generalize the Raspberry Pi + UVC HDMI capture + M5StickS3 USB HID pattern into
a Yaver physical-computer runtime. OpenCode is the first runner, especially for
software-development work, but the target is generic computer use: browsers,
Linux and Windows applications, terminals, IDEs, installers, long-running
operations, and other user-authorized tasks.

This is a separate Yaver product stack. It owns its Go agent, M5 firmware,
identity, pairing, protocols, UI, release artifacts, credentials, and support
policy. It must not depend on or expose another product's branding, services,
MCP, firmware, credentials, endpoints, or release channel.

## User-owned deployment models

Yaver must not require one fixed SaaS topology. A user can place the runner and
coordination services in infrastructure they control:

| Model | Runner/control | Edge node and target |
|---|---|---|
| Fully on-prem | User workstation or local server | Pi beside the physical PC |
| User VPS + on-prem edge | User-owned VPS | Pi beside the physical PC |
| Mixed local | Runner on a local machine | Pi and PC on the same LAN |
| Remote compute target | User VPS/on-prem server | Software runtime on that host; physical KVM optional |

For a physical PC, the Pi and capture card necessarily remain near that PC.
The runner, relay, task store, and model gateway may run on-prem or in the
user's VPS. NAT traversal and relay are transport choices, not ownership
changes. Yaver should call the controlled component an `edge node`, `worker`,
or `target`, not expose legacy master/slave terminology in the product UI.

```text
Yaver client
    -> user-selected Yaver control/relay (on-prem or user VPS)
    -> OpenCode-backed runner (on-prem or user VPS)
    <-> authenticated bidirectional session
    <-> Yaver Go agent on local Raspberry Pi
          | UVC HDMI frames
          + Wi-Fi/BLE -> Yaver M5StickS3 -> USB HID -> physical PC
```

## Hardware kit

- Raspberry Pi 4/5 with supported power and storage.
- UVC-compatible HDMI capture card.
- M5StickS3 running Yaver-owned composite keyboard/mouse firmware.
- HDMI cable from the controlled PC to the capture card.
- Data-capable USB cable from M5 directly to the controlled PC.
- Network for the Pi and 2.4 GHz Wi-Fi for the M5.

The base topology needs no USB hub. BLE is for nearby commissioning; the
authenticated runtime channel uses Wi-Fi. Proximity alone never grants input.

## Runtime contract

Create a first-class `physical-pc-kvm` target:

```text
video: UVC/V4L2 -> single-owner media fan-out
input: M5 USB HID -> exclusive short lease
control: Yaver Go agent
runner: OpenCode first, runner-neutral contract
media: WebRTC preferred; fresh JPEG/MJPEG diagnostic fallback
```

Pi and M5 are independently enrolled devices assigned to one opaque target.
Automatic pairing is allowed only for exactly one fresh authenticated M5 with
the same assignment. Zero candidates waits; multiple candidates block. Binding
is persisted, revocable, and rechecked before every control session.

Input readiness requires a nonce round trip plus USB HID enumeration and local
physical arm. Capture readiness requires a newly decoded frame within the
freshness limit. Process liveness, MQTT connection, and a cached screenshot are
not readiness.

Commands remain typed and bounded: status, discover, session open/heartbeat/
close, key tap, bounded text, relative mouse movement, named click/wheel, and
release-all. Timeout, disconnect, local disarm, runner stop, or reboot releases
all held controls. Sensitive text never enters logs.

## Bidirectional observe/action channel

```text
Pi -> runner: screenshot/keyframe, frame id/time/geometry, capture and HID
              readiness, action acknowledgement, semantic event, error
runner -> Pi: lease heartbeat, typed HID action, confirmation, pause, stop,
              release-all, request-next-frame
```

Each action references the frame and geometry that produced it and carries
session, target, sequence, idempotency, and expiry data. Transport acceptance
does not prove visible success. The runner waits for a strictly newer frame and
performs task-appropriate verification. Stale frames/actions are discarded;
queue depth, message size, frame rate, action rate, and in-flight work are
bounded. Reconnect resumes from acknowledged state and never replays an old
input burst.

The contract is transport-independent. LAN direct, a user-owned VPS relay, and
WebRTC data channels must enforce identical authentication, ordering, lease,
freshness, audit, and fail-closed release semantics.

## OBS interoperability

OBS compatibility is an output of the shared media path, not a competing
capture system. One supervised component owns `/dev/video*` and fans frames to
Yaver WebRTC, screenshots/evidence, and optional OBS output. OBS on the Pi must
consume a shared local source (or be explicitly selected through a typed
single-owner adapter), because many UVC cards cannot be opened twice.

Provide an authenticated OBS Browser/Media Source and one tested low-latency
Pi-to-Windows profile, preferably WebRTC/WHIP or SRT; RTMP can be an optional
compatibility profile. Observer sessions are private by default, independently
revocable, visibly indicated, and cryptographically unable to send HID input.
Stream credentials and destinations never enter Git, prompts, logs, screenshots,
or task summaries.

## Authorization boundary

The runtime operates a computer the user explicitly enrolled. It does not
bypass an OS login, OAuth, MFA, CAPTCHA, consent, licensing, or application
authorization. It can operate an already-authorized interactive session like a
local keyboard and mouse. Risky actions require the same confirmation whether
the runner is local, on a VPS, or connected through a relay.

## Client and task experience

Existing Yaver surfaces should show live video when supported, one quiet
readiness line, runner ownership, local-arm guidance, semantic progress,
takeover, pause/stop/release-all, confirmations, bounded evidence, artifacts,
and a final summary. Small clients remain capability-honest and may be view or
approval only.

Final results include `outcome`, concise `summary`, redacted semantic `actions`,
selected evidence frame hashes/timestamps, artifacts, independent
`verification`, and an optional `nextAction`. Development summaries additionally
include changed files/commit, tests, build/run result, and remaining blockers.
Continuous video does not belong in durable task records.

## Implementation TODO

### 0. Audit and protocol

- [ ] Re-grep current `/capture/*`, `/stream/*`, `/remote-runtime/*`, runner,
  OpenCode, device routing, relay, task, and WebRTC code paths.
- [ ] Define a versioned `physical-pc-kvm` protocol, stable errors, schemas,
  bounds, leases, frame freshness, attestation, and downgrade policy.
- [ ] Define explicit fully-on-prem, user-VPS, and mixed configuration profiles
  with the same security behavior and no vendor-hosted dependency assumption.

### 1. Yaver M5StickS3 firmware

- [ ] Build Yaver-owned composite CDC/keyboard/mouse firmware and identifiers.
- [ ] Add physical arm/lock, visible status, nonce probe, leased session,
  watchdog release-all, dedupe, bounded queue, and text redaction.
- [ ] Add nearby BLE commissioning with paced USB fallback.
- [ ] Add guarded complete-image builds, partition verification, checksums,
  signed OTA manifest, rollback, and hardware tests.

### 2. Raspberry Pi Go agent

- [ ] Add stable `/dev/v4l/by-id` discovery and operation-level UVC probe.
- [ ] Implement single-owner capture and fan-out to freshness, WebRTC,
  screenshots, evidence, and OBS output.
- [ ] Implement authenticated M5 discovery, bind/rebind/revoke, ambiguity
  rejection, exclusive leases, and typed HID actions.
- [ ] Implement correlated frames/actions, acknowledgements, idempotency,
  expiry, backpressure, bounded reconnect, and fail-closed release.
- [ ] Add `yaver kvm status|discover|pair|unpair|doctor|release-all` and bounded
  MCP/runner tools without exposing MQTT topics or raw USB reports.
- [ ] Report capture and input readiness independently in health/inventory.

### 3. OpenCode runner

- [ ] Register the physical PC as a remote-runtime target, not a raw shell.
- [ ] Give OpenCode bounded observe/action operations and current-frame input.
- [ ] Require each visual action to reference its source frame and wait for a
  newer frame before judging success.
- [ ] Add time/action/no-change budgets, confirmations, semantic narration,
  summaries, and immediate user takeover.
- [ ] Prefer authorized DOM/browser automation when available; explicitly fall
  back to the physical KVM when the real PC surface is required.

### 4. Clients, self-hosting, and OBS

- [ ] Add the physical-PC panel to web/desktop/mobile using existing session
  and WebRTC contracts; provide status/approval-only behavior elsewhere.
- [ ] Add onboarding for on-prem, user-VPS, and mixed deployments, including
  TLS, firewall, NAT/relay, rotation, backup, update, and recovery checks.
- [ ] Add observer-only session creation/revocation and active-stream indicator.
- [ ] Test OBS on Pi consuming shared media and OBS on Windows receiving the
  selected protocol; prove observer credentials cannot invoke input.

### 5. Verification and release

- [ ] Unit/headless tests for every verb, error, bound, lease, stale-frame,
  dedupe, ordering, pairing, ambiguity, and reconnect case.
- [ ] Hardware-in-loop tests for frame loss, M5 USB loss, broker/relay loss,
  runner crash, Pi/M5 reboot, stuck keys, user stop, and resolution changes.
- [ ] Linux matrix: X11, Wayland apps, Chromium, terminal, IDE, suspend/resume.
- [ ] Windows matrix: scaling, layouts, UAC secure-desktop limitations, sleep,
  reboot, focus changes, and OBS receiver.
- [ ] Closed-loop browser, development, and generic desktop cases with visible
  evidence plus independent task-appropriate verification.
- [ ] Keep artifacts prerelease until physical tests pass; require signed
  firmware and reproducible agent builds for general availability.

## First vertical slice

Use a fully user-owned Linux setup: runner on a local machine or user VPS, Pi
beside the PC, and a local test website requiring no third-party account. Show
fresh video, acquire a five-second input lease, open Chromium via HID, submit a
known value, wait for a newer frame, verify through a typed test endpoint, and
return a structured summary. Repeat with capture unplugged, M5 USB unplugged,
two candidates, expired lease, runner crash, relay loss, and user takeover.
Every failure must be visible and fail closed before broader rollout.
