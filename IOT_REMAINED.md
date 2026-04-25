# IOT_REMAINED.md — Yaver IoT / c-agent Subsystem

Purpose, architecture, current state, and what's left to ship for
the IoT troubleshooting subsystem (c-agent + brain + firmware
distribution). Treat this as the working tracker for the
embedded/c-agent/ + brain-side dev pipeline.

> **Status: alpha. Test-network use only.** The c-agent runtime
> + ABI are stable enough to integrate against; everything down-
> stream (signing, wasm runtime, firmware images, brain service,
> mobile UI, Hetzner deploys) is still in flight. Production
> deploys need an independent security review.

## 1. Purpose

Build an AI-driven troubleshooting platform for IoT devices that
ships as open-source firmware + a paid cloud brain. The brain
writes diagnostic + remediation code per incident, ships it
signed, the device runs it, and the result comes back in seconds.
Same loop fixes a Klipper print-fail, a Wi-Fi router dropout, a
PX4 drone log anomaly, or a CNC missed-step.

**Wedge: Klipper 3D printers.** Largest active community of any
target class, established willingness to pay ($9.99/mo Obico
precedent), open firmware, technical buyers, real per-incident
pain.

**After Klipper**: drones (PX4/ArduPilot), then OpenWrt routers,
then CNC (LinuxCNC), then SDR. The architecture validates across
all of these — see [`docs/c-agent-domains.md`](docs/c-agent-domains.md).

**Customer tiers**:

- **B2C enthusiasts** ($9.99/mo): one or two devices, paid via
  Stripe. Klipper-first launch.
- **R&D / academic / corporate labs** ($99–499/mo per lab): mixed
  multi-device fleets, annual contracts, real ARPU. Better
  unit economics than pure consumer.
- **OEM licensing** (later): a manufacturer ships c-agent in
  their firmware + co-brands the cloud-brain access. Per-unit
  royalty.

Open-source license: the c-agent runtime + probes are designed
to be Apache-2.0 / FSL-permissive so vendors can include them
without compliance friction. Paid layer is the managed brain,
not the device-side code.

## 2. Architecture in one screen

```
┌──────────────┐  HTTPS  ┌────────────────────┐  TCP/QUIC  ┌────────────────┐
│   Mobile     │◄───────►│   Cloud Brain      │◄──────────►│  Device        │
│ (operator)   │         │ (LLM + retrieval + │            │  (c-agent +    │
│              │         │  build/sign farm + │            │   probes +     │
│              │         │  module registry)  │            │   wasm/eBPF)   │
└──────────────┘         └────────────────────┘            └────────────────┘
```

The four planes:

1. **Device plane** — `embedded/c-agent/` runtime (this repo).
2. **Brain plane** — cloud worker, LLM, retrieval, build/sign
   pipeline, incident memory.
3. **Mobile plane** — operator console + approval surface.
4. **Control plane** — Convex auth + relay + module registry +
   audit (extends Yaver's existing control plane).

The iterative loop:

```
   plan ──► author ──► build/sign ──► ship ──► run ──► observe
     ▲                                                     │
     │                                                     │
     └────────────── refine hypothesis ────────────────────┘

Bounded: 20 iterations or 10 minutes per incident.
Each iteration is signed + immutable + replayable.
```

Full architecture references:

- Runtime: [`docs/c-agent-architecture.md`](docs/c-agent-architecture.md)
- Vendor abstraction: [`docs/c-agent-vendor-modules.md`](docs/c-agent-vendor-modules.md)
- Application surfaces: [`docs/c-agent-domains.md`](docs/c-agent-domains.md)
- Robotics path: [`docs/c-agent-robotics-guide.md`](docs/c-agent-robotics-guide.md)
- IoT manufacturer reference:
  [`embedded/c-agent/FIRMWARE_ARCHITECTURE.md`](embedded/c-agent/FIRMWARE_ARCHITECTURE.md)
- End-to-end demo recipe:
  [`embedded/c-agent/DEMO.md`](embedded/c-agent/DEMO.md)

## 3. What's implemented today

### 3.1 Device-side runtime (`embedded/c-agent/`)

Wire layer (`core/`):

- 9-byte HTTP/2-style frame header (`frame.h`, `frame.c`).
- CBOR codec, deterministic CTAP2 subset (`cbor.h`, `cbor.c`).
- All 11 Phase-0 + module-management body codecs (`body.h`,
  `body.c`): HELLO · AUTH · AUTHRSP · ATTEST · HEARTBEAT ·
  ERROR · INVOKE · TOOL_RSP · STREAM_CHUNK · NEED · MODULE.

Vendor abstraction (`host/`):

- Module ABI (`module.h`): vtable with `on_load` / `on_quiesce`
  / `on_resume` / `on_unload` / `invoke` / `state_save` /
  `state_load` / `on_event`. Versioned. Append-only.
- Host control plane (`host.h`): `init` / `apply_manifest` /
  `invoke` / `pause` / `resume` / `quiesce` / `replace` / `stop`
  + event subscription.
- Manifest (`manifest.h`): vendor declares modules + deps + state
  policy + caps. Same struct used by hand-coded, AI-generated,
  and runtime-shipped manifests.
- Event bus (`event.h`): synchronous fan-out, subscribe with
  optional name filter, ten event kinds for module lifecycle.
- Native module registry: `yvr_host_register_native()` lets
  vendors compile probes directly into the c-agent binary (no
  dlopen needed for v1).
- Session loop (`session.h`, `session.c`): glues TCP transport
  + body codecs + host dispatch into one `yvr_session_run()`
  call. Sends HELLO, expects HELLO, loops on INVOKE → host →
  TOOL_RSP. Echoes HEARTBEAT. Drops unknown frame types.

Transport (`transports/`):

- POSIX TCP adapter (`tcp.h`, `tcp.c`): outbound connect,
  framed send/recv, oversized-payload draining, per-call
  timeouts, `wrap_fd` for socketpair tests.

Built-in probes (`probes/`):

- `wifi_client_count` (`probes/src/wifi_client_count.c`): reads
  `iw dev <iface> station dump` for wlan0..wlan3, returns CBOR
  per-radio counts + total.
- `klipper_status` (`probes/src/klipper_status.c`): queries
  Moonraker `/printer/objects/query?...`, returns raw JSON in a
  CBOR envelope so the brain parses on its side.
- Probes register at compile time via
  `yvr_register_<probe>_probe(host)`.

Toolchain integration:

- CMake install + find_package export.
- pkg-config (`yaver-cagent.pc`, relocatable).
- ncurses-style shell config (`yaver-cagent-config`).
- Three working example consumers in `examples/` (cmake-find-
  package, pkg-config, plain-makefile).
- Buildroot / OpenWrt / Yocto packaging skeletons in
  `packaging/`.

Tests: 7 suites, 81 tests, all green under debug + ASAN/UBSan +
release-with-Werror:

- `frame` — wire frame header (8 tests).
- `cbor` — RFC 8949 deterministic subset (21 tests).
- `body` — all 11 frame bodies (29 tests).
- `host` — manifest + event bus + native registry (9 tests).
- `tcp` — loopback connect/send/recv/oversized (3 tests).
- `probes` — parsers + register-and-invoke (9 tests).
- `session` — end-to-end socketpair brain ↔ device protocol
  loop (1 test, the most important).

### 3.4 Repo reality check (verified on 2026-04-25)

This section is the "code beats doc" snapshot for the current tree.
It reflects what is actually present in the repo right now, not the
intended architecture.

Present on disk:

- `embedded/c-agent/` runtime/library tree with `core/`, `host/`,
  `transports/`, `probes/`, `tests/`, `examples/`, `packaging/`,
  and `brain/`.
- Packaging skeletons for Buildroot, OpenWrt, and Yocto under
  `embedded/c-agent/packaging/`.
- Three consumer examples under `embedded/c-agent/examples/`.
- The brain-side Go package under `embedded/c-agent/brain/`.
- Documentation for architecture, domains, vendor modules, firmware
  architecture, demo recipe, and robotics path.

Not present yet:

- No standalone device CLI / daemon entrypoint such as
  `embedded/c-agent/cli/cagent.c`.
- No separate `iot-brain/` service tree yet; only the reusable Go
  codec/session package exists today.
- No firmware-image build scripts or board-specific image assets for
  Klipper distribution.
- No signed-module pipeline, TLS transport, wasm runtime, or eBPF
  loader implementation in the repo yet.
- No web/mobile product surface wired specifically for the IoT
  troubleshooting flow.

Verified green on 2026-04-25:

- `cd embedded/c-agent && ctest --test-dir build --output-on-failure`
  passed: 7/7 suites.
- `cd embedded/c-agent/brain && go test ./...` passed.

Build/export details verified from CMake:

- Static libraries currently exported: `yvr_cagent_core`,
  `yvr_cagent_host`, `yvr_cagent_probes`, and on POSIX,
  `yvr_cagent_tcp`.
- Public CMake aliases: `yaver::cagent_core`,
  `yaver::cagent_host`, `yaver::cagent_probes`, and on POSIX,
  `yaver::cagent_tcp`.

### 3.5 Documentation drift to fix

The code has moved ahead of some c-agent docs. These are doc issues,
not runtime issues, but they should be tracked because they will
mislead the next implementation pass.

- `embedded/c-agent/README.md` still says the host runtime is a stub
  and lists CBOR/body codecs/event dispatch as future work. That is no
  longer true in the current tree.
- `embedded/c-agent/README.md` "What ships" omits the TCP transport
  and built-in probe library that the current CMake exports.
- `embedded/c-agent/brain/README.md` still says the Go side only
  covers a subset of frame bodies; the repo now has all 11 body codecs
  plus the session orchestrator.
- Before shipping Slice A/B/C, update those READMEs so a new engineer
  can build the correct mental model from the package-local docs
  without needing to inspect tests first.

### 3.2 Brain-side Go codec (`embedded/c-agent/brain/`)

Pure-Go module that mirrors the C wire layer byte-for-byte:

- Frame header encode/decode (`frame.go`).
- CBOR codec, same CTAP2 subset (`cbor.go`).
- All 11 body codecs (`body.go`): HELLO, AUTH, AUTHRSP, ATTEST,
  HEARTBEAT, ERROR, INVOKE, TOOL_RSP, STREAM_CHUNK, NEED,
  ModuleBody.
- TCP transport (`transport.go`): Dial / Wrap / SendFrame /
  RecvFrame, mirrors the C side's oversized-payload semantics.
- Session orchestrator (`session.go`): drives the brain side of
  a connection — `HandleHello`, synchronous `Invoke`, periodic
  `SendHeartbeat`, event subscription via `OnEvent`.
- 47 tests passing including `TestParity_*MatchesC` byte-vector
  comparisons against the C side.

### 3.3 Documentation

- [`docs/c-agent-architecture.md`](docs/c-agent-architecture.md)
  — runtime model, three execution layers, iterative loop, wire
  protocol, security, phasing.
- [`docs/c-agent-domains.md`](docs/c-agent-domains.md) —
  per-device-class application surface (13 vertical sections).
- [`docs/c-agent-vendor-modules.md`](docs/c-agent-vendor-modules.md)
  — abstraction layer reference for integrators.
- [`docs/c-agent-robotics-guide.md`](docs/c-agent-robotics-guide.md)
  — video + LLM + Yaver iteration for robotics.
- [`embedded/c-agent/FIRMWARE_ARCHITECTURE.md`](embedded/c-agent/FIRMWARE_ARCHITECTURE.md)
  — IoT manufacturer reference.
- [`embedded/c-agent/DEMO.md`](embedded/c-agent/DEMO.md) —
  Hetzner + OpenWrt + mobile end-to-end recipe.
- [`embedded/c-agent/README.md`](embedded/c-agent/README.md) +
  [`embedded/c-agent/brain/README.md`](embedded/c-agent/brain/README.md)
  — install + build instructions for both sides.

## 4. What's remained — checklist

Grouped by phase. Phases overlap; pick the next item by current
priority + dependency, not strictly top-to-bottom.

### Phase 1 — make it demoable

- [ ] **C-agent CLI binary** (`cli/cagent.c`): parse argv for
      brain host/port + state-dir, init host, register probes
      (compile-time list), open session, run loop. ~2 hours.
- [ ] **Cross-language integration test**: Go test that spawns
      the C binary as a subprocess + plays the brain side, ex-
      ercises real cross-language wire compatibility (not just
      byte-vector parity). ~4 hours.
- [ ] **Klipper firmware image build pipeline**: produce
      `yaver-klipper-image-v1.img.xz` for Raspberry Pi 4 / BTT
      Pi 1.2 / Manta M8P / Mellow Fly SHT. Bundles Mainsail OS
      + yaver-cagent as systemd service, pre-registers Klipper
      probes. ~3 days.
- [ ] **Klipper firmware distribution page** at
      `yaver.io/firmwares/klipper`: download links, install
      instructions, supported boards table. ~half day.
- [ ] **Hetzner CX22 deployment**: brain server stub running
      the Go session orchestrator behind a public DNS name, so
      a flashed device can connect to a real brain. ~1 day.
- [ ] **Mobile "Ask Device" surface**: input box + answer area,
      wired to brain's `POST /ask` endpoint. Reuses existing
      Yaver auth + device list. ~1 day.

### Phase 2 — real signing + module loading

- [ ] **Module signature verify** (ed25519 or P-256). Pick a
      crypto lib: monocypher (small, ~6 KB) or libsodium (well-
      known, ~50 KB) or mbedTLS (already needed for TLS later).
      ~2 days.
- [ ] **Trust root pinning**: per-tenant root cert baked at
      provisioning, stored in `~/.yaver/cert.pin`. Boot-time
      verification that incoming module signatures chain to it.
      ~1 day.
- [ ] **Revocation Bloom filter**: pulled from brain on session
      start. Module loads check `hash ∉ revoked`. ~half day.
- [ ] **TLS 1.3 transport adapter**: mbedTLS bindings, sits
      next to TCP transport. Brain side uses Go crypto/tls
      stdlib. ~3–5 days.
- [ ] **wasm3 integration**: dynamic module loader for Layer 1.
      Capability binding via WASI host-imports. The big
      Phase-1 milestone. ~5–7 days.
- [ ] **Capability allowlist enforcement**: each module's
      declared capabilities bind only the allowed host imports
      at instantiation. Strict reject on attempted misuse. ~2
      days (after wasm3).
- [ ] **Per-tool resource caps**: `setrlimit` on max_runtime_ms,
      max_memory_pages, max_output_bytes per invocation. ~1 day.
- [ ] **eBPF program loading** (Layer 2): libbpf-skeleton-style
      load, BPF verifier signs off, attach to tracepoints/
      kprobes. ~3 days.
- [ ] **NVRAM boot counter + safe-mode**: 5 consecutive
      enrollment failures → c-agent disables itself for 1 hour.
      Standard fleet-rollout safety. ~half day.

### Phase 3 — real brain service (`iot-brain/`)

- [ ] **Go service skeleton**: TCP listener, accept c-agent
      connections, drive `brain.Session` per connection. ~1
      day.
- [ ] **Convex integration**: device registry, session
      authentication, audit log. Reuse existing
      `desktop/agent/` patterns. ~2 days.
- [ ] **Question → probe routing** (v1): hardcoded keyword
      mapping ("clients" → `wifi_client_count`, "temp" →
      `klipper_status` extract). ~half day.
- [ ] **LLM coordinator**: Claude / Codex / Aider for the
      reasoning step. Reuse existing yaver runner machinery.
      ~3 days.
- [ ] **Module build pipeline**: cross-compile Rust source to
      wasm32-wasi, sign, publish to module registry. ~3 days.
- [ ] **Pattern library**: curate successful modules for reuse
      across incidents. Convex table + retrieval. ~2 days.
- [ ] **Operator approval gate**: APPROVAL_REQ → mobile push →
      operator signs token → brain ships INVOKE with approval.
      ~2 days.
- [ ] **Per-incident audit trail**: every iteration's source +
      result + reasoning logged for replay + accountability.
      ~1 day.
- [ ] **Iteration budget enforcement**: 20 iterations / 10
      min per incident, configurable. ~half day.

### Phase 4 — production readiness

- [ ] **Multi-device per account**: one Yaver user → many
      flashed devices with independent state. ~2 days.
- [ ] **Lab-tier billing**: per-organization seat management +
      device-count metering. Stripe integration. ~3 days.
- [ ] **SSO via SAML** for the lab tier. ~2 days.
- [ ] **Audit log retention + export** (compliance). ~2 days.
- [ ] **Per-device health dashboard**: brain shows fleet
      status, recent incidents, cost-per-incident. ~3 days.
- [ ] **Crash-dump capture from the c-agent itself**: small
      coredump in NVRAM, uploaded next session. ~1 day.
- [ ] **CI matrix for cross-compile**: musl × glibc × {amd64,
      aarch64, armv7, mipsel}. GitHub Actions runner pool.
      ~2 days.
- [ ] **Fuzzing infrastructure**: AFL++ + honggfuzz on every
      parser (frame, CBOR, body codecs). ~2 days.
- [ ] **Production-grade signing infra**: HSM-backed root +
      per-tenant intermediates + key rotation playbook. ~1
      week.
- [ ] **Open-source release announce**: Hacker News + Hackaday
      + Klipper Discord post. ~half day.

### Phase 5 — additional verticals

After Klipper proves the loop:

- [ ] **PX4 / ArduPilot drone probes**: post-flight ULog
      analysis, mavlink stream tap, ESC desync detection. ~2
      weeks.
- [ ] **OpenWrt router probes**: hostapd ctrl-iface, nl80211
      station dump (already partial), DHCP lease state, DNS
      query log. ~1 week.
- [ ] **LinuxCNC probes**: HAL pin reads, missed-step counter,
      RT latency histogram. ~2 weeks.
- [ ] **SDR probes**: USRP/HackRF underrun counters, FFT
      moments, USB throughput. ~1 week.
- [ ] **Robotic-arm path** (Parol 6 wire-harness):
      [`docs/c-agent-robotics-guide.md`](docs/c-agent-robotics-guide.md).
      Deferred until Klipper validates the loop.

### Phase 6 — research-y / aspirational

- [ ] **VLA model integration** (OpenVLA / π0): vision-
      language-action models for the robotics path.
- [ ] **Sim-to-real for robotics**: MuJoCo / Isaac sim
      pre-validation before real-hardware trials.
- [ ] **Federated brain across tenants**: shared
      pattern-library improvements while keeping per-tenant
      module signatures isolated.
- [ ] **Self-improving probes**: brain rewrites probes based
      on which fields it ended up using.
- [ ] **Edge-side LLM** for fully-disconnected operation
      (probably Llama-3 8B or smaller on a Jetson Orin Nano).

## 5. Open architecture questions

These are listed in
[`docs/c-agent-architecture.md`](docs/c-agent-architecture.md)
§14 with working answers. Reproduced here for the tracker:

| Q | Answered? | Working answer |
|---|---|---|
| Binary or JSON wire format? | yes | CBOR inside HTTP/2-style frames |
| Phone-bridged E2E encryption? | yes | E2E; phone sees ciphertext only |
| Approval enforcement plane? | yes | Both: mobile UX + device verifies signature |
| Redaction location? | yes | Device side |
| Connector hosting model? | yes | Customer-managed default, Yaver-hosted optional |
| Relay framed-stream mode? | yes | Generalize relay around frame-stream |
| Distro baseline for v1? | yes | OpenWrt 22.03 + Yocto Kirkstone |
| Iteration budget? | yes | 20 iters or 10 min, operator-extendable |
| Module language for v1? | yes | Rust → wasm32-wasi (primary), Zig fallback |
| Crypto library for signing? | **open** | mbedTLS / libsodium / monocypher — pick when Phase 2 starts |
| WASM runtime: wasm3 vs wasmtime-tiny? | **open** | wasm3 is smaller (~64 KB) but slower; pick when Phase 2 starts |
| Brain service hosting model? | **open** | Yaver-hosted vs customer-managed-on-Hetzner; both supported |

## 6. Wedge plan — Klipper specifics

**Why Klipper**: 100k+ active users, GPLv3 firmware, technical
buyers, established subscription willingness ($9.99/mo Obico
precedent), open Moonraker REST API, every common SBC supported.

**4-week first end-to-end run**:

- Week 1: bring-up + capability layer wrapping Moonraker.
- Week 2: hand-coded script-policy + 10 manual demo trials.
- Week 3: behavior-cloning ACT/diffusion policy from 40 demos.
- Week 4: LLM coordinator + mobile "Train this Robot" surface.

(Robotics-path version of the same approach, applied to Klipper:
hand-coded probes → AI-authored probes → AI-authored fixes.)

**Pricing**:
- Free: open-source firmware, manual troubleshooting via a
  community Discord-style channel.
- $9.99/mo: cloud brain, 10 troubleshooting sessions/month
  included, $0.99/extra session.
- Lab tier ($99–499/mo): mixed multi-device, annual
  contract, SSO + audit + admin dashboard.

**First customer milestone**: 100 paying customers at $9.99/mo
within 6 months of launch. $1k MRR. If unreachable, kill or
pivot.

**Distribution**:
- `yaver.io/firmwares/klipper` page with downloads + install
  guides.
- Launch posts: r/klippers, Voron Discord, Hackaday article,
  Mainsail Telegram.
- Goal: 10,000 downloads in 90 days, 1% paid conversion = 100
  customers.

## 7. File quick-reference

| Path | What |
|---|---|
| `embedded/c-agent/core/` | Wire layer (frame, CBOR, body codecs) |
| `embedded/c-agent/host/` | Module ABI + runtime + session loop |
| `embedded/c-agent/transports/` | TCP transport (POSIX) |
| `embedded/c-agent/probes/` | Built-in probes (wifi_client_count, klipper_status) |
| `embedded/c-agent/cmake/` | Install templates (.pc, Config.cmake, sh-config) |
| `embedded/c-agent/examples/` | Three integration consumers |
| `embedded/c-agent/packaging/` | Buildroot / OpenWrt / Yocto recipes |
| `embedded/c-agent/tests/` | 7 ctest suites |
| `embedded/c-agent/brain/` | Pure-Go brain-side codec + session |
| `docs/c-agent-architecture.md` | Runtime architecture |
| `docs/c-agent-vendor-modules.md` | Vendor abstraction reference |
| `docs/c-agent-domains.md` | Per-device-class application surface |
| `docs/c-agent-robotics-guide.md` | Video + LLM + Yaver for robotics |
| `embedded/c-agent/FIRMWARE_ARCHITECTURE.md` | IoT manufacturer reference |
| `embedded/c-agent/DEMO.md` | Hetzner + OpenWrt + mobile recipe |
| `IOT_REMAINED.md` | This file (working tracker) |

## 8. Test count snapshot

```
C side (device):    7 suites · 81 tests
                     debug + ASAN + release-Werror all green

  frame    8   wire frame header
  cbor    21   RFC 8949 deterministic subset
  body    29   all 11 body types
  host     9   manifest + event bus + native registry
  tcp      3   loopback connect/send/recv/oversized
  probes   9   wifi_client_count + klipper_status
  session  1   end-to-end HELLO + INVOKE + TOOL_RSP
                via socketpair, full protocol loop

Go side (brain):   47 tests
                    Cross-language byte-vector parity
                    Round-trip stability
                    TCP loopback + full HELLO encode→decode
                    Session HELLO + Invoke + Heartbeat
```

## 9. How to validate progress

After implementing each Phase-1 item, the following should still
be true:

- All 7 C suites green (`./build.sh debug && ./build.sh asan`).
- All 47 Go tests green (`cd embedded/c-agent/brain && go test ./...`).
- Install + 3 example consumers still work
  (`cmake --install build --prefix /tmp/yvr-X` then run each
  `examples/*/`).
- Zero touches to `desktop/agent/`, `backend/`, `web/`,
  `mobile/`, `relay/` from the c-agent work.

After Phase-1 completion, the following should be true:

- A Klipper user can flash `yaver-klipper-image-v1`, register
  through the mobile app, and ask "what's my hotend temp?"
  through Yaver. Round-trip < 5 seconds.

After Phase-2 completion:

- The brain can ship signed wasm modules to the device, the
  device verifies + caches + executes them, and the same
  Klipper user gets answers to questions whose probe the brain
  authored on demand.

After Phase-3 completion:

- The full iterative loop works on a real incident: brain
  authors probe → ships → device runs → brain refines →
  authors fix → operator approves → device applies → brain
  verifies. Real Klipper bug fixed by the loop on bench
  hardware.

## 10. Suggested implementation slices

These are the next repo-local chunks that produce visible forward
progress without needing the whole product stack at once.

### Slice A — standalone c-agent binary

Goal: turn the library into a runnable device process.

Files likely to add:

- `embedded/c-agent/cli/cagent.c`
- `embedded/c-agent/cli/CMakeLists.txt`
- optional `embedded/c-agent/cli/README.md`

Acceptance:

- `cagent --brain-host 127.0.0.1 --brain-port 7777 --state-dir /tmp/yvr`
  starts, registers built-in probes, opens a session, and cleanly
  exits on transport failure.
- Links the current exported libraries directly:
  `yvr_cagent_core` + `yvr_cagent_host` + `yvr_cagent_probes`
  (+ `yvr_cagent_tcp` on POSIX).
- CMake installs the binary alongside the existing library artifacts.
- No regressions in existing ctest suites.

### Slice B — real cross-language integration test

Goal: prove the shipped C binary and Go brain package interoperate
over a real subprocess boundary, not just byte-vector parity.

Files likely to add:

- `embedded/c-agent/brain/integration_test.go`
- test helper scripts or fixtures only if strictly necessary

Acceptance:

- Go test spawns the compiled `cagent` binary.
- Test performs HELLO handshake, invokes at least one built-in probe,
  receives `TOOL_RSP`, and validates decoded payload fields.
- Preferred first target: `wifi_client_count`, because it has a small,
  stable output surface and does not require a live Moonraker server.
- Test failure output is readable enough to debug wire mismatches.

### Slice C — minimal brain service skeleton

Goal: create the first runnable host process for real device sessions.

Files likely to add:

- `iot-brain/` module root
- `iot-brain/cmd/iot-brain/main.go`
- `iot-brain/internal/...` only if needed after the first pass

Acceptance:

- Listens on TCP.
- Accepts one or more c-agent connections.
- Wraps each connection in `brain.Session`.
- Exposes one hardcoded invoke path for demo questions such as
  "wifi clients" or "hotend temp".
- Persists no state yet; this slice is for protocol bring-up only.

This slice does not need Convex, mobile UI, or LLM coordination yet.
It only needs to be enough to support a bench demo.

## 11. Status / next move

**Current state**: device-side runtime is functionally complete
for read-only probes; brain-side codec + session orchestrator
are complete. End-to-end socketpair test proves the wire works.
What's missing is the actual product wrapper — a CLI binary, a
firmware image, a brain service, a mobile UI.

**Suggested next slice**: do Slices A and B before the firmware
image work. The firmware pipeline is important, but without a
runnable c-agent binary and a subprocess integration test, it
would bake an unproven process wrapper into an image too early.

Recommended order:

1. Standalone c-agent binary.
2. Cross-language integration test against that binary.
3. Minimal brain service skeleton.
4. Then Klipper image build/distribution.

After that, Phase 2's signing + wasm3 integration is the
biggest single remaining unknown. Once that lands, the
architecture's full claim — "the brain ships custom code, the
device runs it safely, the loop iterates" — becomes
demonstrable end-to-end on real bench hardware.

Everything past Phase 3 is product polish + market expansion;
the core technical risk is concentrated in Phases 1 + 2.
