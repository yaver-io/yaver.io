# C-Agent Architecture

## Status

C-agent is an **additional Yaver surface, not a pivot**. Yaver's
primary product remains the dev-machine agent in
[`desktop/agent/`](../desktop/agent/) — Claude Code, Codex, Aider, and
similar tooling for software development. The c-agent runtime described
here is a separate, narrower deployment shape that lives in its own
subdirectory (`embedded/c-agent/` if and when implemented), shares the
existing control plane (Convex auth, relay transport, mobile
orchestration), and is enabled per-deployment.

This document exists so that when c-agent work happens it has a
coherent technical reference. Treat it as a design baseline for an
opt-in side project, not a roadmap commitment. Build effort on the
dev-machine agent takes priority.

## What this document covers

The architecture for using Yaver to troubleshoot IoT, embedded,
industrial, and appliance-class devices — anything from home routers
and EV chargers to elevators, signage players, kiosks, POS terminals,
set-top boxes, building controllers, and Linux-based gateways of any
kind.

The defining property of the design is that the device-side runtime is
**deliberately small and tool-agnostic**. It does not ship a fixed
catalog of diagnostic tools. Instead it loads, sandboxes, and runs
*signed code modules* shipped from a cloud reasoning plane on demand.
The cloud plane writes new diagnostic and remediation code per incident,
compiles it, signs it, ships it, observes the result, and iterates until
the problem is understood and resolved.

This architecture is the natural home for an LLM-driven troubleshooting
loop: the model writes code against a small, well-defined device ABI,
runs it inside a sandbox on the real device, observes the result, and
refines its hypothesis on the next iteration — the same outer shape as
Yaver's existing autodev loop, with a remote sandboxed device as the
workspace instead of a local repo.

Relevant existing code that this design extends:

- [`desktop/agent/httpserver.go`](../desktop/agent/httpserver.go)
- [`desktop/agent/auth_bootstrap.go`](../desktop/agent/auth_bootstrap.go)
- [`desktop/agent/tasks.go`](../desktop/agent/tasks.go)
- [`relay/server.go`](../relay/server.go)
- [`relay/protocol.go`](../relay/protocol.go)
- [`mobile/src/lib/quic.ts`](../mobile/src/lib/quic.ts)
- [`mobile/src/context/DeviceContext.tsx`](../mobile/src/context/DeviceContext.tsx)
- [`backend/convex/devices.ts`](../backend/convex/devices.ts)
- [`AI_ARCH.md`](architecture/AI_ARCH.md)

## 1. Goal

Make Yaver useful for diagnosing and fixing real-world failures on
connected devices that are operationally hard to reach: in-field,
behind NAT, owned by an operator with limited shell access, often
running constrained Linux on heterogeneous hardware.

The design is shaped by three observations.

1. **Static tool catalogs do not cover the failure surface.** Real IoT
   incidents look like "Wi-Fi keeps dropping at 21:00 every night,"
   "device reboots after 18 hours," "network throughput collapses when
   guest SSID is enabled," "captive portal works on Android but not
   iOS." These do not map to a fixed set of canned diagnostics. They
   map to "an experienced engineer would look at X, then Y, then Z,
   write a small probe, observe the answer, and iterate."

2. **Big agents do not belong on the device.** Frontier LLM execution,
   multi-gigabyte retrieval indexes, and incident memory have no place
   on a 64 MB-RAM router. They belong in a cloud reasoning plane that
   the device can be reached from. The device's job is to provide
   privileged, well-isolated access to its own state.

3. **Iteration is the actual product.** A troubleshooting system that
   produces one report from one snapshot of evidence is a glorified
   logging tool. A troubleshooting system that loops — observe,
   hypothesize, write code, run it, observe again, refine — is what
   replaces an experienced field engineer.

## 2. System shape

There are four planes:

```
┌─────────────────────┐     ┌─────────────────────┐
│   Mobile (Phone)    │     │  Cloud Brain        │
│   - operator UX     │     │  - LLM execution    │
│   - approvals       │◄───►│  - module authoring │
│   - issue intake    │     │  - retrieval        │
│   - bridge mode     │     │  - incident memory  │
└──────────┬──────────┘     └──────────┬──────────┘
           │                           │
           │           ▲                │
           │           │                │
           ▼           │                ▼
        ┌──────────────┴──────────────┐
        │   Build / Sign Pipeline     │
        │   - cross-compile farm      │
        │   - WASM / eBPF toolchain   │
        │   - module signing (HSM)    │
        │   - per-tenant module cache │
        └──────────────┬──────────────┘
                       │
                       │   signed modules
                       ▼
              ┌──────────────────┐
              │  Device runtime  │
              │  (c-agent)       │
              │  - tiny loader   │
              │  - sandbox       │
              │  - capability    │
              │    enforcement   │
              └──────────────────┘
```

- **Cloud brain** — runs the LLM workflow + retrieval + incident memory.
  Authors diagnostic and remediation modules per incident. Connects to
  the build pipeline to get them signed and available. Drives the
  iteration loop.
- **Build / sign pipeline** — turns module source into signed,
  capability-tagged artifacts the device will accept. Per-tenant signing
  keys live here.
- **Mobile** — operator console, issue intake, approval gating, bridge
  for non-IP transports.
- **Device runtime (c-agent)** — small loader that verifies signatures,
  binds capabilities, sandboxes execution, and streams results back.
  Does *not* contain the diagnostic logic itself.

## 3. The iterative diagnostic loop

This is the central control flow. Everything else exists to make this
loop fast, safe, and recoverable.

```
                  ┌─────────────────────────────────────┐
                  │  Operator describes symptom         │
                  │  (mobile, free text + structured)   │
                  └──────────────────┬──────────────────┘
                                     ▼
                  ┌─────────────────────────────────────┐
                  │  Brain plans first probe(s)         │
                  │  - reads device fingerprint         │
                  │  - reads incident memory            │
                  │  - retrieves relevant docs          │
                  │  - decides what evidence to fetch   │
                  └──────────────────┬──────────────────┘
                                     ▼
                  ┌─────────────────────────────────────┐
                  │  Brain authors diagnostic module    │
                  │  - writes Rust / Zig / C source     │
                  │  - declares required capabilities   │
                  │  - emits result schema              │
                  └──────────────────┬──────────────────┘
                                     ▼
                  ┌─────────────────────────────────────┐
                  │  Build pipeline                     │
                  │  - compile to wasm32-wasi (or BPF)  │
                  │  - sign with tenant key             │
                  │  - publish to module registry       │
                  └──────────────────┬──────────────────┘
                                     ▼
                  ┌─────────────────────────────────────┐
                  │  Brain → device: INVOKE module@hash │
                  └──────────────────┬──────────────────┘
                                     ▼
                  ┌─────────────────────────────────────┐
                  │  Device                             │
                  │  - cache hit? skip fetch            │
                  │  - else: fetch + verify signature   │
                  │  - bind capabilities                │
                  │  - run in sandbox                   │
                  │  - stream result back               │
                  └──────────────────┬──────────────────┘
                                     ▼
                  ┌─────────────────────────────────────┐
                  │  Brain interprets result            │
                  │  - refines hypothesis               │
                  │  - decides next probe OR            │
                  │  - has enough evidence              │
                  └──────────┬──────────────────────────┘
                             │
                ┌────────────┴────────────┐
                │                         │
            need more                 ready to fix
                │                         │
                └─────loop─────►          ▼
                  (back to plan) ┌────────────────────────────┐
                                 │  Brain authors             │
                                 │  remediation module        │
                                 │  + rollback module         │
                                 └────────────┬───────────────┘
                                              ▼
                                 ┌────────────────────────────┐
                                 │  Mobile approval gate      │
                                 │  - operator sees diff      │
                                 │  - sees expected effect    │
                                 │  - signs approval token    │
                                 └────────────┬───────────────┘
                                              ▼
                                 ┌────────────────────────────┐
                                 │  Device executes           │
                                 │  remediation               │
                                 └────────────┬───────────────┘
                                              ▼
                                 ┌────────────────────────────┐
                                 │  Brain authors verification│
                                 │  module                    │
                                 │  - same probes as before   │
                                 │  - asserts symptom gone    │
                                 └────────────┬───────────────┘
                                              ▼
                                 ┌────────────────────────────┐
                                 │  Verified? close incident. │
                                 │  Not verified? loop again. │
                                 └────────────────────────────┘
```

Every iteration produces:

- A signed module (immutable, hash-addressed)
- Its inputs (CBOR)
- Its outputs (CBOR + optional stream chunks)
- Its capability set
- The brain's reasoning for invoking it

This sequence is the audit trail. The incident report is generated
mechanically from it.

### 3.1 Why iteration matters more than tool count

A static catalog of 200 typed tools can't troubleshoot an incident
whose answer requires correlating Wi-Fi station-info with TCP retransmit
timestamps and a custom systemd journal grep — unless the catalog
designer happened to anticipate that combination. The iterative model
sidesteps the problem: the brain *writes the correlation code* for this
incident, runs it, and discards it (or promotes it into a reusable
module if the pattern recurs — see §10).

### 3.2 Iteration budget

The loop is bounded. Each iteration costs:

- Brain LLM tokens (compile-time)
- Build pipeline CPU + sign latency (~1–10 s)
- Network transfer to device (depends on link)
- Device execution time (capability-capped per module)

Default budget: **20 iterations or 10 minutes** per incident, whichever
hits first. Operator can extend. This avoids runaway loops and gives
the operator a clear signal when the brain is not converging.

## 4. Device runtime (c-agent)

### 4.1 Footprint targets

Three tiers:

| Tier | Static binary | RSS at idle | Boot time | Use case |
|---|---|---|---|---|
| Tier 1 | ≤ 1.5 MB | ≤ 4 MB  | ≤ 200 ms | Modern Linux appliances, 128 MB+ RAM |
| Tier 2 | ≤ 600 KB | ≤ 2 MB  | ≤ 100 ms | Legacy CPE, 64 MB RAM, OpenWrt-class |
| Tier 3 | ≤ 200 KB lib | ≤ 1 MB  | ≤  50 ms | OEM-embedded `.a` linked into vendor binary |

Tier 1 is the v1 target. Tier 2 is the realistic Phase-2 target. Tier 3
is a follow-up shape, not a starting commitment.

### 4.2 Three execution layers

The runtime carries three different ways to execute brain-shipped code,
each with a different trust profile.

```
┌──────────────────────────────────────────────────────────────────┐
│                        device runtime                            │
│                                                                  │
│  Layer 0: built-in (firmware-time, hand-audited)                 │
│    • transport, crypto, signature verify, module loader          │
│    • recovery tools that must work even if everything else fails │
│      (heartbeat, enroll, time-sync, factory-reset, list-modules, │
│       crash-report-upload, kill-running-module)                  │
│    • ~120 KB                                                     │
│                                                                  │
│  Layer 1: WASM modules (default)                                 │
│    • wasm3 (~64 KB) or wasmtime-tiny (~250 KB)                   │
│    • WASI-style host imports per capability allowlist            │
│    • memory-bounded, CPU-bounded, syscall-isolated               │
│    • brain ships .wasm; one build per tool, runs on every arch   │
│    • 90 %+ of tools live here                                    │
│                                                                  │
│  Layer 2: eBPF programs (kernel observability)                   │
│    • verifier guarantees safety; cannot crash kernel             │
│    • taps mac80211, tcp, syscall hooks, driver tracepoints       │
│    • brain ships compiled BPF objects + libbpf-style skeleton    │
│    • used when a probe genuinely needs kernel-side data          │
│                                                                  │
│  Layer 3: native .so (escape hatch — vendor extension)           │
│    • forked child + seccomp-bpf + setrlimit                      │
│    • requires Yaver signature AND OEM/operator signature         │
│    • disabled by default; per-deployment opt-in                  │
│    • only used for vendor-blob integration                       │
└──────────────────────────────────────────────────────────────────┘
```

The boundary between Layer 0 and the others is critical: anything that
can brick the device or cut off its recovery path lives in Layer 0,
shipped with firmware, hand-audited, and never replaced from cloud.
Everything that can be re-shipped should be.

### 4.3 Why WASM as the default

| Property | Native `.so` | WASM |
|---|---|---|
| Per-arch compile | musl/glibc × ~7 ABIs | one `.wasm`, runs everywhere |
| Memory safety | manual C discipline | verifier-enforced |
| OOM containment | fork + setrlimit | linear memory cap, hard |
| CPU containment | fork + cgroup | fuel/instruction counter |
| Syscall exposure | full process access | only declared host imports |
| Crash blast radius | can take agent down | trap → unload module |
| Cold start | dlopen ~5 ms | wasm3 init ~10–30 ms |
| Steady-state speed | 1× | ~0.3–0.5× |
| Module size | 30–500 KB | 5–80 KB |

The 2× steady-state penalty is invisible for diagnostic probes that run
for a few hundred milliseconds. The portability and safety wins are
decisive: brain compiles once per tool, ships the same artifact to
every device, regardless of MIPS / ARM / aarch64 / x86_64.

### 4.4 Capability model

A WASM module has access to *exactly* the host imports declared in its
signed descriptor. The set is the safety boundary; nothing outside it
is callable from the module, regardless of what the WASM bytecode tries.

Capability families are namespaced and grow append-only:

```
nl80211.*          read-only netlink wireless info
nl80211.write.*    set-channel, set-tx-power, ...
hostapd.read       hostapd ctrl-iface read commands
hostapd.write      hostapd ctrl-iface write commands
ubus.read          ubus call X get* on OpenWrt
ubus.write         ubus call X set*
fs.read.logs       /var/log/**, /tmp/log/** read-only
fs.read.config     /etc/config/**, /etc/**.conf read-only
fs.read.sys        /sys/** read-only
fs.read.proc       /proc/** read-only
procfs.processes   /proc/[pid]/... read-only
netlink.route      AF_NETLINK NETLINK_ROUTE
netlink.bpf        bpf() syscall (Layer-2 helper)
exec.shell         spawn a shell (NEVER granted to dynamic modules
                   without explicit operator approval)
service.read       systemd / procd service status
service.restart    systemd restart UNIT (high-risk)
service.stop       systemd stop UNIT (high-risk)
reboot             reboot device (dangerous)
config.patch       apply a validated config delta (dangerous)
```

Each module's descriptor lists the exact capabilities it needs. A
module that asks for more than its declared set at runtime traps. A
module that *fails* to ask for the right capability gets a clear
"capability not granted" error — useful debug output to the brain.

A capability is enforced as: the corresponding WASI host import is only
bound into the module's import table at instantiation time *if the
module's signed descriptor lists that capability*. Nothing more.

### 4.5 Resource caps per module invocation

Every module declares (and the descriptor signs):

```
max_runtime_ms       e.g. 5_000
max_memory_pages     wasm pages of 64 KB each, e.g. 8 (= 512 KB)
max_output_bytes     hard cap on result CBOR, e.g. 64 KB
max_stream_bytes     hard cap on streamed bytes, e.g. 4 MB
max_log_lines        cap on log output, e.g. 1_000
```

The runtime enforces these via wasm3's instruction counter (or
wasmtime's fuel mechanism) and the WASI memory limit. A module that
exceeds any cap is trapped; partial output up to the cap is returned to
the brain with `truncated: true`.

This is what protects a 64 MB-RAM device from an exuberant
`tail_log /var/log/messages` over a 4 GB log file.

## 5. Module lifecycle

### 5.1 Authoring

The brain writes module source per incident. Source language for v1:

- **Rust → wasm32-wasi** for most diagnostics. Strict bounds, easy
  cross-compile, mature WASI support.
- **Zig → wasm32-wasi** for size-critical or no-allocator modules.
- **C / libbpf** for Layer-2 eBPF programs.

The brain's working tree per incident:

```
incidents/<incident-id>/
  iteration-001/
    probe/
      Cargo.toml
      src/main.rs
    descriptor.cbor
    plan.md            # brain's stated hypothesis + intent
    result.cbor        # filled in after run
  iteration-002/
    ...
```

Each iteration is immutable. The brain references prior iterations'
results when authoring the next one.

### 5.2 Build

A single CI farm cross-compiles WASM (one target) plus eBPF (one target
per kernel ABI baseline). For Layer 3 native escape-hatch modules,
multiple per-arch builds happen here too — but Layer 3 is rare and
scope-bounded.

```
build pipeline
  ├── rustc --target=wasm32-wasi -O    → probe.wasm
  ├── strip / wasm-opt -Os             → probe.wasm (optimized)
  ├── compute blake3(wasm || schema)   → tool_hash
  ├── build descriptor:
  │     name, hash, schema_in, schema_out,
  │     capabilities, resource caps, expires_at
  ├── sign descriptor with HSM-backed
  │   per-tenant key                   → descriptor.cbor + sig
  └── publish (tenant, name, hash)     → module registry
```

### 5.3 Distribution

The device fetches modules on demand:

1. Brain sends `INVOKE { tool_hash, args }` to device.
2. If `tool_hash` is in the device's local LRU cache → skip to step 5.
3. Device sends `NEED { tool_hash }` to brain.
4. Brain streams the signed descriptor + WASM bytes back.
5. Device verifies signature chain, expiry, revocation, capability
   allowlist, and module size against cache budget.
6. Cache write: `/var/cache/yaver/m/<hash>.{wasm,cbor}`.
7. Module loaded into wasm3, host imports bound per capability
   declaration, invoked.

LRU eviction happens when cache exceeds the device's declared
`cache_quota_bytes` (sent at enrollment). Frequently used modules can
be marked `pin_in_cache: true` in the descriptor and survive eviction
until explicitly purged.

### 5.4 Execution

Runtime steps for a single invocation:

1. Allocate per-invocation arena (single bump allocator, ~512 KB).
2. Set up wasm3 instance with the cached module bytes.
3. Bind only the host imports declared in the descriptor's capability
   list.
4. Configure resource caps: instruction limit, memory limit, stream
   bytes limit.
5. Call `_start` (WASI entry).
6. Module reads CBOR-encoded args from a host-provided buffer.
7. Module calls host imports as needed, computes its result.
8. Module writes a CBOR-encoded result into a host-provided buffer.
9. For long-running probes: module emits stream chunks via a host
   import; runtime forwards each chunk as a `STREAM_CHUNK` frame to
   the brain.
10. Module exits. Runtime sends `TOOL_RSP { hash, status, result }`.
11. Arena freed; wasm3 instance dropped.

If anything traps (capability denied, OOM, instruction limit),
the runtime captures the trap reason and ships a structured error.
The agent itself does not crash.

## 6. Wire protocol

Frame format borrowed from HTTP/2 framing — fixed 9-byte header,
arbitrary payload, well understood:

```
+---------+--------+--------+----------------+
| length  | type   | flags  |   stream_id    |
| 24 bits | 8 bits | 8 bits |   32 bits      |
+---------+--------+--------+----------------+
   length     payload bytes (max 16 MB; truncate above)
   type       see frame types below
   flags      END_STREAM=0x01, ACK=0x02, COMPRESSED=0x04
   stream_id  0 for connection-scoped; odd from initiator,
              even from responder; reuse forbidden after CLOSE
```

Frame types:

| Code | Type | Direction | Purpose |
|---|---|---|---|
| 0x01 | HELLO | both | version + features |
| 0x02 | AUTH | brain→device | challenge |
| 0x03 | AUTHRSP | device→brain | response |
| 0x04 | ATTEST | device→brain | arch, libc, kernel, capabilities |
| 0x05 | INVOKE | brain→device | run module by hash + args |
| 0x06 | TOOL_RSP | device→brain | module final result |
| 0x07 | NEED | device→brain | module hash not in cache |
| 0x08 | MODULE | brain→device | signed module bytes + descriptor |
| 0x09 | STREAM_CHUNK | device→brain | partial output during run |
| 0x0A | EVENT | device→brain | unsolicited event from a long-running probe |
| 0x0B | HEARTBEAT | both | liveness + signed time |
| 0x0C | APPROVAL_REQ | brain→mobile | high-risk action proposal |
| 0x0D | APPROVAL_RSP | mobile→brain | signed approval token |
| 0x0E | ERROR | any | structured failure |
| 0x0F | WINDOW_UPDATE | both | flow control |
| 0x10 | KILL | brain→device | cancel a running module |

Bodies are CBOR. Stream chunks may be opaque bytes. JSON is exposed
only behind a `--json` debug switch; production is binary.

Stream-level flow control (HTTP/2 `WINDOW_UPDATE`-style) is mandatory
on slow links. Initial window 16 KB per stream, 256 KB connection-wide
on TCP/WS; 1 KB per stream, 4 KB connection-wide on BLE/serial.

On reconnect, a `STREAM_RESUME` extension replays from the last
acknowledged offset for streams that were mid-flight.

## 7. Security

### 7.1 Trust roots

- `yvr-root-<year>` — Yaver root signing key, HSM-backed, rotated
  annually with overlap.
- Per-tenant intermediate — each operator gets their own intermediate
  key, signed by `yvr-root`. Operators sign their own tenant-private
  modules without going through Yaver.
- Device cert — issued at enrollment, signed by `yvr-root` (or tenant
  intermediate for white-label deploys).
- OEM root — optional; signs Layer-3 native escape-hatch modules.
  Layer 3 invocation requires both Yaver and OEM signatures.

### 7.2 Module load checks

In order, before any module bytecode runs:

1. Signature chain valid up to a pinned root.
2. `expires_at > signed_now()` — `signed_now` is provided fresh by the
   brain at session start, *not* the local wall clock (which may be
   wrong on field devices with dead RTCs).
3. Tool hash not in tenant's revocation Bloom filter.
4. All declared capabilities are in the device's allowlist.
5. Module size + cache space available.
6. Module bytes hash matches `tool_hash` exactly.

Only then: load, bind imports, invoke.

### 7.3 Approval signing for risky actions

Modules tagged with risk class `low-write`, `high-write`, or
`dangerous` cannot be invoked without an `approval` token in the
`INVOKE` frame:

```
approval = {
  ver:           1,
  session_id:    <uuid>,
  tool_call_id:  <uuid>,
  tool_hash:     <32 bytes>,
  args_sha256:   <32 bytes of CBOR(args)>,
  not_before:    <unix>,
  not_after:     <unix>,
  operator_kid:  <key id>,
  sig:           <ECDSA P-256 over all-of-the-above>,
}
```

The operator's mobile device signs the token after presenting the
proposal to the operator. The device verifies `sig` against the
operator's pinned public key (delivered at session establishment),
checks freshness, and **re-hashes the args** at execution time so the
brain cannot substitute parameters after approval.

Without re-hashing, "operator approves a `restart_service` call" is
trust-on-first-call: the brain could later swap in `reboot`.

### 7.4 Revocation

Per-tenant revoked-hash list, fetched on every session start as a Bloom
filter (~2 KB for 10 K revocations at 1 % FP rate). False positives
trigger a re-fetch from the brain, which authoritatively confirms or
denies. False negatives are not possible.

Revocation is critical because cloud-shipped code without revocation is
indefinite execution privilege.

### 7.5 Redaction at the device boundary

Module results are filtered through `/etc/yaver/redact.d/*.cbor`
before leaving the device. Each redaction file declares regex
patterns + which capability families they apply to:

```
{ pattern: "wpa_psk=[^ ]+", replace: "wpa_psk=<redacted>",
  apply_to: ["fs.read.config", "hostapd.read"] }
```

OEMs ship redaction packs for vendor-specific secrets. Operators can
add their own. Default packs cover Wi-Fi PSKs, LAN passphrases, OAuth
tokens, private keys, MAC-address-as-secret patterns.

The device is the only party that knows what is sensitive in its own
config files; redaction therefore lives there. Once the data leaves the
device, it is treated as exposed.

### 7.6 Crash recovery

Layer-0 carries a 1-byte boot counter in `/etc/yaver/state.nv`:

- Increment before opening the network on boot.
- Reset to 0 after a successful enrollment / heartbeat.
- If the counter exceeds 5 between resets → agent disables itself for
  1 hour, regardless of any other state.

This is the difference between a fleet rollout that recovers from a
bad module and one that requires a truck-roll.

## 8. Brain-side build pipeline

### 8.1 Toolchain

- `rustc --target=wasm32-wasi` for Rust modules.
- `zig cc --target=wasm32-wasi` for Zig / C modules.
- `clang -target bpf -O2 -g -c` for eBPF programs.
- `zig cc` cross-compilers for Layer-3 native modules (when needed).

A single Linux build host with these toolchains is sufficient for the
whole platform. No per-arch compile farm needed for Layer 1.

### 8.2 Module dev experience

A diagnostic module is a tiny Rust crate:

```
incidents/<id>/iteration-N/probe/
  Cargo.toml
  src/main.rs              # the actual probe
  descriptor.toml          # declared capabilities + caps
```

`Cargo.toml`:

```toml
[package]
name = "wifi_steering_history"
version = "0.0.1"
edition = "2021"

[dependencies]
yaver-cagent-sdk = { path = "../../../sdk/rust" }
serde = { version = "1", features = ["derive"] }
ciborium = "0.2"
```

`src/main.rs`:

```rust
use yaver_cagent_sdk::{capability, host};

#[derive(serde::Deserialize)]
struct Args {
  iface: String,
  since_seconds: u32,
}

#[derive(serde::Serialize)]
struct StaEvent {
  ts:        u64,
  mac:       String,
  event:     String,
  rssi_dbm:  i32,
  bss_from:  String,
  bss_to:    String,
}

#[no_mangle]
pub fn _start() {
  let args: Args = host::read_args();
  let events = capability::hostapd::read_steering_events(
    &args.iface, args.since_seconds,
  );
  host::write_result(&events);
}
```

The brain authors files of this shape per incident, hands them to the
build pipeline, and gets back a signed `tool_hash`.

### 8.3 SDK ergonomics

The SDK (`sdk/rust`, mirrored in `sdk/zig`) exposes:

- typed wrappers over each capability family
- `host::read_args()` / `host::write_result()` / `host::stream_chunk()`
- a logging macro that emits `EVENT` frames during execution
- structured error returns the brain can interpret

This makes the brain's authoring output mostly business logic. The
boilerplate is library code.

## 9. Mobile orchestration

The mobile app owns:

- Issue intake (operator describes symptom; structured fields where
  helpful, free-text otherwise).
- Device discovery and pairing (existing Yaver discovery code).
- Transport selection — direct IP, relay, BLE bridge, serial bridge.
- Bridge mode — phone forwards opaque encrypted frames between brain
  and device when device has no IP path. Phone never sees plaintext.
- Live incident view — current hypothesis, evidence collected so far,
  modules invoked, time spent, iterations remaining in budget.
- Approval gate for risky actions — shows the proposed module diff,
  expected effect, rollback plan, and signs the approval token on
  operator confirmation.
- Final incident report rendering.

Bridge mode is end-to-end encrypted between brain and device. The phone
sees ciphertext only. This avoids regulatory complications around
operator-mediated data exposure and matches Yaver's existing privacy
posture.

## 10. Pattern library — module reuse over time

Every iteration of every incident produces a signed, immutable module.
After enough incidents, recurring patterns emerge: "fetch last 24 h of
station-disconnect events for iface X," "tail journal grepped for
`oom-killer`," "compare current routing table with last-good snapshot."

The brain promotes high-reuse modules into a curated library. A library
module is just a regular signed module with metadata:

```
lib_module {
  curated:        true,
  parent_hash:    <hash of the iteration that birthed it>,
  promoted_at:    <unix>,
  promoted_by:    <reasoning chain>,
  use_count:      <int>,
  last_used:      <unix>,
}
```

When the brain plans iteration 1 of a new incident, it queries the
library first, ranked by symptom similarity. A library hit means
near-zero cold start, a known-good capability set, and a tested
result schema.

The library is the long-term flywheel: incidents make modules; modules
make future incidents faster.

This is not a vendor-supplied tool catalog — it is a per-tenant /
per-fleet emergent collection. Two operators of different device fleets
can have completely different libraries that both get better over time.

## 11. Failure modes and recovery

What can go wrong, and what the runtime does.

| Failure | Detection | Response |
|---|---|---|
| Module hangs | instruction limit exceeded | trap, return partial output, mark hung |
| Module OOMs | wasm memory cap hit | trap, return error |
| Module exits with error | normal trap | return structured error to brain |
| Module emits malformed CBOR | parse failure | return parse error to brain |
| Capability denied | host import call rejected | return cap-denied error to brain |
| Signature invalid | load-time check fails | log incident, refuse module |
| Cache full | LRU evict before write | evict; if still full, reject |
| Disk full | cache write fails | run from memory, do not persist |
| Brain unreachable mid-incident | session timeout | return transport-down event; retry |
| Phone closes app mid-incident | session orphaned | brain sees timeout, can resume |
| Device reboots mid-incident | session orphaned | resume on reconnect if within window |
| Agent itself crashes | crash counter increments | next boot may auto-disable for 1 h |
| Layer-3 native module crashes | child process dies | parent agent unaffected |
| Operator approval expires | expiry check at execute | reject; brain re-asks |

The agent is designed so no single dynamic-module failure can break
recovery. Layer-0 always survives.

## 12. Session model

A troubleshooting session is a first-class object stored in the control
plane.

```
session {
  id:                  <uuid>
  operator_user_id:    <id>
  device_id:           <id>
  brain_worker_id:     <id>
  transport_mode:      "direct" | "relay" | "phone-bridge"
  issue:               { summary, symptoms[], severity }
  device_fingerprint:  { arch, libc, kernel, caps[], cache_budget }
  iterations: [
    {
      n:               1,
      kind:            "probe" | "remediate" | "verify" | "rollback"
      tool_hash:       <hash>,
      args:            <cbor>,
      result:          <cbor>,
      stream_summary:  <text>,
      duration_ms:     <int>,
      capabilities:    [ ... ],
      reasoning:       <text from brain>,
      approval:        <approval token if any>,
    },
    ...
  ],
  hypotheses:          [ { summary, confidence, supporting_iters[] } ],
  status:              "created" | "collecting" | "analyzing" |
                       "awaiting-approval" | "remediating" |
                       "verifying" | "completed" | "failed" |
                       "aborted",
  final_diagnosis:     <text>,
  final_report_md:     <markdown>,
}
```

The session is the audit trail. Replay is mechanical: re-fetch each
iteration's signed module by hash and re-run with the recorded args
against a captured device snapshot.

## 13. Phased build

### Phase 0 — protocol skeleton (~3 weeks)

- Frame parser, CBOR codec, TLS 1.3 handshake.
- HELLO, AUTH, ATTEST, HEARTBEAT, ERROR.
- No modules, no capabilities yet.
- AFL-fuzzed parsers, ASAN-clean, valgrind-clean.

Exit: agent on a Linux qemu-armhf VM accepts a session from a brain
worker, completes auth, and answers HEARTBEAT.

### Phase 1 — wasm runtime + module loading (~3 weeks)

- wasm3 integrated.
- INVOKE, TOOL_RSP, NEED, MODULE frames.
- Signature verification + descriptor parsing.
- Capability binding — initial set: `fs.read.logs`, `fs.read.config`,
  `procfs.processes`, `netlink.route`, `nl80211.read`.
- Module cache + LRU eviction.

Exit: brain ships a Rust-authored "list active routes" probe;
device runs it and streams the result back. End-to-end loop closed
with one tool.

### Phase 2 — first real diagnostic surface (~3 weeks)

- ~15 capability families wired (the Linux + Wi-Fi list in §4.4).
- Redaction packs.
- Stream chunk flow control.
- WINDOW_UPDATE frames.
- KILL frame for cancellation.
- Resource caps enforced.

Exit: brain can write any of a wide range of probes — `tail_log`,
`get_journal`, `wifi_sta_list`, `mesh_link_metrics`, etc. — and run
them against a real OpenWrt VM.

### Phase 3 — iterative loop (~4 weeks)

- Brain-side autodev-style loop with the device session as workspace.
- Per-iteration source workspace + module build pipeline.
- Per-tenant signing keys.
- Module registry.
- Incident memory + library promotion path.

Exit: brain takes a free-text incident, runs a 5–10 iteration
diagnostic loop on a real device, and produces a final markdown report
that cites real evidence by module hash.

### Phase 4 — controlled remediation (~3 weeks)

- Risk classes wired in module descriptors.
- Approval token signing on mobile.
- `service.restart`, `cycle_iface`, `apply_config_patch`,
  `renew_dhcp`, `reload_service` capabilities.
- Rollback module pattern (every remediation pairs with a rollback).

Exit: brain can propose a fix, mobile approves with a signed token,
device executes, brain verifies. End-to-end "diagnose + fix + verify"
on a real incident.

### Phase 5 — non-IP transports (~3 weeks)

- Serial transport adapter (COBS-framed).
- BLE GATT transport adapter (chunked, throttled).
- Phone-bridge mode (E2E encrypted; phone sees ciphertext).
- Slow-link STREAM_RESUME.

Exit: same `INVOKE` works against a device reachable only over BLE
or serial via a phone bridge.

### Phase 6 — eBPF surface (~3 weeks)

- Layer-2 module shape: ELF BPF object + skeleton metadata.
- BPF verifier integration.
- A handful of reference probes: TCP retransmits, syscall counts,
  packet flow.

Exit: a probe that needs kernel-side data is shippable as eBPF instead
of WASM.

### Phase 7 — Layer-3 native escape hatch (~3 weeks; optional)

- Forked-child execution model.
- seccomp-bpf + setrlimit.
- Dual-signature requirement (Yaver + OEM/operator).
- Disabled by default.

Exit: vendor blob integration possible without compromising the
default-safe posture.

## 14. Open questions

| Q | Working answer |
|---|---|
| Binary or JSON wire format? | CBOR inside HTTP/2-style frames; JSON only behind a `--json` debug switch. |
| Phone-bridged E2E? | Yes. Phone sees ciphertext between brain and device. |
| Approval enforcement plane? | Both: mobile UX gates the operator; device verifies the signature. Defense in depth. |
| Redaction location? | Device side. Only the device knows what's sensitive in its own config. |
| Connector hosting? | Customer-managed worker by default; Yaver-hosted worker is a separate path. |
| Relay framed-stream mode? | Generalize. Add a frame-stream upgrade alongside the existing HTTP proxy. |
| Distro baseline? | OpenWrt 22.03 (musl 1.2, kernel 5.15) and Yocto Kirkstone for v1. |
| Iteration budget? | 20 iterations or 10 minutes per incident, operator-extendable. |
| Module language for v1? | Rust → wasm32-wasi, with Zig → wasm32-wasi as a compatible alternative for size-critical modules. |

## 15. What the device runtime does *not* do

To keep the trust boundary clean, the device runtime explicitly does
not contain:

- An LLM, retrieval index, or any reasoning logic.
- A general-purpose shell server.
- A static catalog of more than ~8 built-in tools.
- A persistent store of past incidents.
- Operator credentials beyond the per-session pubkey delivered at
  session establishment.
- Any logic that can self-modify Layer 0 from the network.

Everything else lives in the brain, the build pipeline, or the mobile
orchestrator.
