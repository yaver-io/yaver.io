# Firmware Architecture Reference for IoT Manufacturers

This document describes how to structure the firmware of an IoT
device so it works well with Yaver's c-agent runtime and the
AI-driven troubleshooting loop that uses it. It is the
implementation-side companion to
[`../../docs/c-agent-architecture.md`](../../docs/c-agent-architecture.md)
and [`../../docs/c-agent-vendor-modules.md`](../../docs/c-agent-vendor-modules.md):
the architecture doc explains the runtime, the vendor-modules doc
explains the vendor-facing abstraction, this doc explains the
firmware-engineering practices that make the abstraction safe and
useful in real devices.

## Status — read before designing around this

> **Alpha-grade. Test-network use only.** This architecture exists
> to enable AI-driven, iterative, *trial* troubleshooting on
> non-production fleets — engineering bench gear, lab testbeds,
> dev / staging networks, opt-in alpha customer sites that have
> agreed to a research-grade trust model. Auto-generating fix
> methodologies on running customer devices is research, not a
> shipped capability.
>
> **Not for safety-critical control loops, not for regulated
> infrastructure, not for production fleet rollouts** without an
> explicit and independent security review of the deployed
> configuration. The same is true for any architecture that lets
> a remote system load executable code into a device — the design
> below mitigates the obvious failure modes, but the trust
> envelope still needs to be one a manufacturer is comfortable
> defending.

The remainder of this document assumes a manufacturer who is
intentionally trying to enable AI-assisted remote troubleshooting
on devices they own end-to-end (test labs, alpha customers,
internal fleets), and is willing to spend firmware budget on
process independence, capability gating, and signed-code loading
to make that possible.

## What the AI is going to do to your device

The c-agent + brain loop interacts with a device's firmware in two
distinct phases. Both phases ship signed code modules to the
device using the same wire protocol; they differ in *what they
ship* and *what those modules are allowed to do*.

### Phase A — Monitoring

The brain has heard a symptom report and is hypothesizing. It
ships **monitoring modules** — read-only probes that observe state
without changing anything material on the device. Examples:

- snapshot the kernel ringbuffer
- tap a tracepoint via eBPF
- read filtered netlink station info
- collect 30 seconds of TCP retransmit counters
- scrape a vendor control-iface for the last hour of events

Monitoring modules are sandboxed (WASM by default; eBPF for
kernel-side data) and capability-gated to read-only host imports.
The brain iterates: probe, read result, refine, probe again, until
it has enough evidence to form a fix hypothesis.

**Firmware impact during Phase A**: minimal. Modules attach,
snapshot, return, detach. No state of the running app changes.
The c-agent's resource caps (memory, CPU, stream-bytes) prevent
even an aggressive probe from starving the host.

### Phase B — Replacement

The brain has a fix in mind and has gotten operator approval. It
ships **replacement modules** that overwrite a specific subsystem
of the running app. Examples:

- replace the rate-control module with a tweaked version
- replace the DHCP retry policy with a shorter-backoff variant
- replace the captive-portal redirect handler with one that
  honours a new HSTS rule
- replace the OCPP message parser with a more tolerant version

This is where the firmware design matters. A replacement module
is loaded into a process where another version of itself was just
running. Its memory needs to land on top of the previous version
cleanly, its dependents must not crash during the swap, and its
state must migrate forward (or be intentionally discarded).

The rest of this document is about firmware practices that make
this possible.

## The first principle: process independence

Every replaceable subsystem must be designed so that its absence,
malfunction, or quiescing **cannot crash the rest of the
firmware**. This is not the same as "no shared state" — it is the
weaker but still strict requirement: *the rest of the firmware
must observe the subsystem as either present-and-responsive,
present-and-idle, or absent, and must continue functioning
sensibly in any of those three states.*

Concretely:

1. **No direct function calls between subsystems.** Replaceable
   subsystems are reached only through `yvr_host_invoke()`. The
   call site sees a `yvr_host_status_t` return code and decides
   what to do if the call fails or returns
   `YVR_HOST_E_NOT_READY`. There is no scenario where a missing
   module is a missing symbol.

2. **No shared mutable globals.** Two modules that need to share
   state share it through the host's invoke mechanism, not
   through a `.bss` global. A module that's reloaded loses its
   `.bss`; if a second module was reading that `.bss`, the second
   module just crashed.

3. **No held pointers across the module boundary.** A module that
   returns a buffer from `invoke()` may be replaced before its
   caller is done reading the buffer. Always copy at the
   boundary.

4. **No assumption of process identity.** The same module may be
   running in-process today and out-of-process tomorrow. Modules
   that need wall clock, persistent state, or shared memory get
   them through the host helpers
   (`yvr_module_state_path`, `yvr_module_alloc`,
   `yvr_module_subscribe`), not through global file descriptors.

5. **Dependents react to lifecycle events.** When yaver fires
   `YVR_EVT_MODULE_QUIESCED` for a module you depend on, your
   subscriber should switch to a fallback path or buffer
   outbound work. When `YVR_EVT_MODULE_RESUMED` fires, drain the
   buffer. This is the difference between "wifi temporarily
   idle" appearing in the UI for 200 ms and the user-facing app
   crashing.

## Memory model

Hot-swap requires that a module's code + data can be evicted and
overwritten. This imposes constraints that conventional
embedded-firmware design ignores.

### Code memory

- **Replaceable modules must reside in writable, executable
  pages** (`PROT_READ | PROT_WRITE | PROT_EXEC` on Linux, or the
  equivalent on the device's MMU). The default `dlopen` path on
  glibc + musl already produces this; you only need to think
  about it on RTOS targets that XIP from flash.
- **Do not lock module pages with `mlock` or pin them with
  vendor-specific cache controls.** Locked pages cannot be
  overwritten by a fresh `dlopen`.
- **Position-independent code.** All modules compile with `-fPIC`
  / `-fPIE` so they can be loaded at different addresses on
  successive loads. The CMake template
  ([`core/CMakeLists.txt`](core/CMakeLists.txt)) sets this for
  c-agent's own libraries; your own module recipes should do the
  same.
- **No relocations against module-local data from outside the
  module.** Every cross-module reference goes through the host's
  invoke mechanism, which is symbol-stable across reloads.

### Data memory

- **Per-module arena allocators.** Each module instance gets a
  bump arena (or a small heap of its own) at `on_load` time. All
  module-internal allocation comes from this arena. On
  `on_unload`, the host frees the arena in one shot. No
  per-allocation `free` chain to leak.
- **Buffers crossing the module/host boundary must use
  `yvr_module_alloc()`.** That allocator is owned by the host;
  the host frees the buffer after the call. If you `malloc` and
  return that pointer, the host's `free` against a different
  allocator will at best leak and at worst crash.
- **State migration via `state_save` / `state_load`, never via
  in-memory pointers.** The new version of a module sees a
  serialized blob, not a struct from the old version. Use a
  vendor-defined serialization (CBOR + a schema-version integer
  is the recommended pattern); the new `state_load` reads
  fields it understands, ignores fields it doesn't, synthesizes
  defaults for fields it added.
- **Persistent storage in
  [`yvr_module_state_path()`](host/include/yvr/module.h).**
  Survives module reload AND host process restart for modules
  declaring `state_policy = YVR_STATE_PERSISTENT`.

### Predictable peak working set

Per-module manifest entries declare:

```
quiesce_window_ms   how long the host waits for graceful quiesce
max_runtime_ms      per-invoke runtime cap
max_memory_pages    WASM linear-memory cap (64 KB pages)
max_output_bytes    per-invoke result cap
max_stream_bytes    cumulative stream cap during long-running ops
invoke_queue_max    queued invokes during quiesce / replace
```

These are enforced by the runtime; module code that exceeds them
traps cleanly. Pick numbers that fit the device's actual budget,
not aspirational ones — a Tier-2 (64 MB RAM) CPE's per-module
default should land near 256 KB linear memory and 5 ms
per-invoke. Diagnostic probes are usually one to two orders of
magnitude smaller than replacement modules.

## Process model recommendations

There are three viable in-firmware deployments, in increasing
isolation:

### A — Single host with dynamic modules (default for resource-constrained)

```
┌──────────────────────────────────────────────┐
│            vendor host process               │
│  ┌────────────┐ ┌────────────┐ ┌──────────┐  │
│  │  module A  │ │  module B  │ │  ...     │  │
│  │  (.so)     │ │  (.so)     │ │          │  │
│  └────────────┘ └────────────┘ └──────────┘  │
│            ↑       host runtime              │
│            │       (libyvr_cagent_host)      │
│            ↓                                 │
│  ┌────────────────────────────────────────┐  │
│  │           c-agent transport            │  │
│  │           (libyvr_cagent_core)         │  │
│  └────────────────────────────────────────┘  │
└──────────────────────────────────────────────┘
                     │
                     ▼
                  brain
```

Cheapest, but a fault in one module that escapes its sandbox can
take the whole host down. Acceptable when modules are WASM
(verified, OOM-bounded, can't escape) or when the trust profile
of all modules is the same. **This is the right starting point
for v1 deployment** because it minimizes surface area.

### B — Multi-host with one process per module group

```
   vendor host A                vendor host B
   (network stack,              (telemetry, control,
    Wi-Fi, mesh)                 audit, UI bridge)
        │                              │
        └──────────┬───────────────────┘
                   ▼
              c-agent IPC
                   │
                   ▼
              brain
```

Two or more host processes each run a coherent group of modules.
A crash in host A doesn't take host B down. Use this when:

- one group of modules is materially riskier than another (vendor
  blob driver, third-party network stack, experimental control
  logic);
- different groups have different security requirements (e.g.
  a "safety" group must not link against modules that take
  signed code from the cloud);
- different groups need different priorities, schedulers, or
  resource caps (real-time control vs. best-effort telemetry).

Cost: more memory (one libyvr_cagent_host per process), more
inter-process plumbing. Worth it when the failure-isolation
requirement is firm.

### C — Hybrid: critical recovery in-process, dynamic modules per-process

The recovery path (heartbeat, enroll, time-sync, factory-reset,
crash-report-upload, kill-running-module) lives in a small,
hand-audited host that is deliberately **never replaceable from
the cloud**. Riskier dynamic modules run in a separate child
process supervised by the recovery host. If the child crashes,
the recovery host stays up and reports the failure; the brain
ships a replacement and the recovery host respawns the child.

This is the recommended shape once a deployment moves past v1.

## What firmware MUST do

Every firmware that integrates the c-agent abstraction must
implement, at minimum:

1. **A signed-code trust root.** Pin the Yaver root (and
   per-tenant intermediate, and OEM root for Layer-3 native
   modules) at flash provisioning time. No code path may load a
   module without verifying its signature against a pinned root.

2. **A capability allowlist.** The set of host imports your
   firmware exposes to modules — what they're allowed to read,
   what they can write to, which kernel APIs are reachable. This
   is the safety boundary; nothing outside it is callable from a
   module.

3. **`on_quiesce` for every replaceable module.** Drain in-flight
   work, release scarce resources, do NOT exit the process,
   return promptly (under `quiesce_window_ms`). Without this,
   replacement is unsafe.

4. **`state_save` / `state_load` for any module with
   `YVR_STATE_TRANSIENT` or `_PERSISTENT`.** Versioned, additive
   schema, ignore-unknown-fields semantics on read.

5. **Subscribe to events from any module you depend on.** When
   the target you depend on goes through QUIESCED → REPLACING →
   REPLACED → RESUMED, your code reacts: pause issuing requests,
   buffer outbound work, drain on resume.

6. **Resource caps in every manifest entry.** Numbers that
   reflect the device's actual budget. Assume the brain will
   eventually try to ship a probe that pushes against every cap.

7. **A boot counter in NVRAM.** Increments before every network
   open, resets to 0 on a clean enrollment. After 5 consecutive
   failures, disable c-agent for one hour. Without this, a
   misbehaving module can brick a fleet rollout.

8. **Crash-dump capture.** Modules can crash. The host process
   must not. If it does, write a small dump
   (registers + 8 KB of stack) to NVRAM and upload on next
   session. Without crash telemetry from real devices, debugging
   is impossible.

9. **Watchdog cooperation.** If your device has a hardware
   watchdog, do NOT make the c-agent or any module responsible
   for petting it. The host process pets the watchdog
   independently of any module; a hung module gets quiesced /
   killed without the device rebooting.

## What firmware MUST NOT do

1. **Do not hold pointers across module boundaries.** Buffers
   returned from `yvr_host_invoke` are owned by the called
   module; copy before reuse. Pointers stored in the host that
   reference module-internal memory are crash bait the next
   time that module is replaced.

2. **Do not block in lifecycle callbacks.** `on_quiesce`,
   `on_resume`, `on_event` must return promptly. If you need to
   do real work (flush a queue, fsync a file), dispatch to a
   vendor-owned worker thread and return immediately.

3. **Do not maintain mutable global state shared between
   modules.** If a global is needed, give one module ownership
   and let the others reach it through `yvr_host_invoke`.

4. **Do not self-restart the host process.** Yaver owns process
   lifecycle. If a module needs the host to come back up, it
   reports an error; yaver decides whether to restart or roll
   back.

5. **Do not read from / write to module memory after
   `on_unload`.** All buffers from that module are invalid the
   moment `on_unload` returns. The host's allocator handles
   buffers it received earlier; module-local memory is gone.

6. **Do not rely on dlopen() side effects.** The runtime may
   load Layer-1 modules through `wasm3` (no dlopen), Layer-2
   through `bpf()` (no dlopen), Layer-3 through `dlopen` only.
   Module code that assumes a `dlopen` path won't be portable
   across the layers.

7. **Do not assume strict ordering between subscribers.** The
   event bus delivers in causal order (REPLACING before
   REPLACED) but does not guarantee inter-subscriber ordering.
   Don't write code where module A depends on having seen the
   event before module B did.

## Recommended firmware boot sequence

```
1. CPU comes up, kernel loads, init runs.
2. Host process starts, links libyvr_cagent_host + core.
3. Host pins trust roots from flash.
4. Host opens NVRAM, reads boot counter.
   - if counter > 5 since last clean enrollment: log + delay 1h
   - else: increment counter, continue
5. Host opens transport(s) — direct LAN + relay (+ tunnel).
6. Host completes HELLO + AUTH + ATTEST exchange with brain.
7. Host receives + applies manifest (compiled-in default OR
   AI-shipped current version, whichever is fresher).
8. Host loads modules in topological dep order; each module's
   on_load runs in turn, fires LOADED events.
9. Host enters steady state: heartbeat loop, event dispatch,
   invoke routing.
10. On clean steady state for >5 min: reset boot counter.
11. On enroll-fail / signature-fail: do NOT fall through to
    "load the last good manifest from cache without verifying"
    — refuse to load anything and surface a needs-auth state.
```

## Recommended pattern: the replaceable subsystem

To make a subsystem AI-replaceable, structure it as a module:

```c
/* my_subsystem.c — compiled to my_subsystem.so */
#include <yvr/module.h>

typedef struct {
    /* Per-instance state. Initialized in on_load, freed in
     * on_unload. Migrated across versions via state_save /
     * state_load. */
    int      counter;
    uint64_t last_event_ts_ms;
    /* ... */
} subsys_state_t;

static yvr_module_status_t my_load(yvr_module_ctx_t *ctx)
{
    subsys_state_t *s = yvr_module_alloc(ctx, sizeof *s);
    if (s == NULL) return YVR_MODULE_E_FATAL;
    /* attach: open netlink, register tracepoints, ... */
    /* stash s where the rest of this module can find it
     * (a static for the simplest case; or a context registry
     * keyed by ctx pointer for multi-instance modules) */
    return YVR_MODULE_OK;
}

static yvr_module_status_t my_quiesce(yvr_module_ctx_t *ctx)
{
    /* drain queues, release scarce resources, but keep the
     * subsys_state_t alive so state_save can serialize it */
    return YVR_MODULE_OK;
}

static void *my_state_save(yvr_module_ctx_t *ctx, size_t *out_len)
{
    /* serialize subsys_state_t to a CBOR blob; return a buffer
     * allocated with yvr_module_alloc(ctx, ...) */
}

static yvr_module_status_t my_state_load(yvr_module_ctx_t *ctx,
                                         const void *blob,
                                         size_t blob_len)
{
    /* deserialize; tolerate missing fields (defaults) and
     * unknown fields (skip) */
}

static yvr_module_status_t my_resume(yvr_module_ctx_t *ctx)
{
    /* re-attach scarce resources released during quiesce */
}

static void *my_invoke(yvr_module_ctx_t *ctx, const char *method,
                       const void *req, size_t req_len, size_t *out_len)
{
    /* dispatch on method name; encode response with
     * yvr_module_alloc, return buffer */
}

static void my_unload(yvr_module_ctx_t *ctx)
{
    /* tear down; free everything; this is the last call */
}

YVR_MODULE_DEFINE("my_subsystem", "1.0.0", &(yvr_module_vtable_t){
    .on_load    = my_load,
    .on_quiesce = my_quiesce,
    .state_save = my_state_save,
    .state_load = my_state_load,
    .on_resume  = my_resume,
    .invoke     = my_invoke,
    .on_unload  = my_unload,
});
```

The same source produces both the version compiled into the v1
firmware (built into the manifest's static module list) and any
later version the brain ships as a replacement artifact. The
firmware does not need to know which version it has; the host
runtime handles that.

## Recommended pattern: the dependent

A module (or vendor host code) that calls into a replaceable
subsystem subscribes to that subsystem's events and reacts:

```c
static void on_event(yvr_module_ctx_t *ctx, const yvr_event_t *evt)
{
    if (strcmp(evt->module_name, "my_subsystem") != 0) return;
    switch (evt->kind) {
    case YVR_EVT_MODULE_QUIESCED:
    case YVR_EVT_MODULE_REPLACING:
        /* stop initiating calls to my_subsystem; buffer outbound
         * work (or surface a "temporarily idle" state to the UI) */
        break;
    case YVR_EVT_MODULE_REPLACED:
    case YVR_EVT_MODULE_RESUMED:
        /* drain buffered work; UI back to normal */
        break;
    case YVR_EVT_MODULE_REPLACE_FAILED:
    case YVR_EVT_MODULE_ERROR:
        /* old version is back (REPLACE_FAILED) or module is dead
         * (ERROR with fatal status). Decide whether to fall back
         * to a different subsystem or surface a hard error. */
        break;
    default:
        break;
    }
}
```

Subscribers are wired up in `on_load` via `yvr_module_subscribe`
(for modules) or `yvr_host_subscribe` (for vendor host code).

## Resource accounting during the AI loop

The brain's iterative loop bounds itself: ≤ 20 iterations or 10
minutes per incident, whichever hits first. From a firmware
perspective this is the relevant load profile:

- **Steady state**: 0 brain-shipped modules running. Native
  modules (compiled into the firmware) handle 100% of traffic.
- **Active incident**: 1–3 monitoring modules attached at any
  given time, each running for a few seconds, returning small
  results. Negligible memory + CPU pressure. Replacement modules
  start arriving in iterations 10–20 if the brain converges on a
  fix.
- **Replacement event**: ~30–500 ms window during which one
  subsystem is paused or quiesced. Dependents see a single
  module unavailable. Total system effect should be no greater
  than a routine service-restart on the host.

A device that can survive `systemctl restart` of any one
subsystem can survive a c-agent replace of that subsystem.

## What this enables on a test network

On an alpha / test deployment, the manufacturer can:

- Reproduce a customer-visible bug on bench hardware.
- Hand the brain a free-text symptom description + the device.
- Watch the brain author probes, observe results, refine.
- Watch the brain author a candidate fix, ship it, run it.
- Compare device behaviour before vs. after.
- Either accept the AI-generated patch (after engineering
  review) or annotate why it didn't work and let the brain
  iterate.

The output of a successful loop is:

- A signed module artifact (the fix).
- A reproduction harness (the probes that found it).
- A complete audit trail (every iteration's source + result).

That artifact set goes into the manufacturer's normal release
pipeline for engineering review — same as a fix authored by a
human, just with the diff already written and the failure
already characterized. The c-agent infrastructure is what lets
this happen on remote hardware in seconds instead of "ship the
broken router back to the lab" in days.

## What this does NOT enable

- **Production rollouts of AI-authored code without engineering
  review.** Even a green AI-generated patch goes through the
  manufacturer's normal release process before landing on
  customer fleets.
- **Remote modification of safety-critical control loops.** If a
  subsystem participates in a safety case (motor control,
  brake monitor, ground-fault interruption), it must NOT be a
  replaceable module. Its capability set must NOT be reachable
  from any AI-authored module. The certified parts of the
  firmware live in Layer 0 (firmware-time, hand-audited) and
  are never replaced from the cloud.
- **Customer-data extraction at scale.** Every module call is
  capability-gated; redaction runs at the device boundary; the
  audit trail records every read. The trust model is "a
  firmware engineer with the brain's keys could see what the
  brain saw" — not "the brain reads everything."
- **Replacing the c-agent runtime itself.** Layer 0 is
  immutable from the network. Updates happen through the
  manufacturer's normal firmware-update path.

## Design checklist

Before integrating, walk through this list:

- [ ] Trust roots pinned in flash at provisioning.
- [ ] Capability allowlist defined; nothing outside it reachable.
- [ ] Every replaceable subsystem is structured as a module
      with `on_load`, `on_quiesce`, `on_resume`, `on_unload`,
      `invoke`, `state_save`, `state_load`.
- [ ] Every dependent subscribes to the relevant lifecycle
      events and degrades gracefully.
- [ ] Per-module resource caps are realistic for the device's
      memory + CPU budget.
- [ ] Per-module arena allocators replace the global heap for
      module-internal allocation.
- [ ] No shared mutable globals between modules; cross-module
      state lives in invokes or in `yvr_module_state_path`.
- [ ] NVRAM boot counter + safe-mode-after-N-failures path is
      implemented.
- [ ] Watchdog is owned by the host process, not the modules.
- [ ] Host-process crash dumps land in NVRAM and upload on next
      session.
- [ ] Safety-critical paths are explicitly OUTSIDE the
      replaceable surface (Layer 0 only).
- [ ] Deployment is a test / alpha / dev fleet, not a production
      rollout.
- [ ] Engineering review of AI-generated artifacts is wired
      into the release process before production deploy.

## Files

- ABI to integrate against:
  [`host/include/yvr/`](host/include/yvr/)
- Working examples of toolchain integration:
  [`examples/`](examples/)
- Buildroot / OpenWrt / Yocto recipe skeletons:
  [`packaging/`](packaging/)
- Architecture overview:
  [`../../docs/c-agent-architecture.md`](../../docs/c-agent-architecture.md)
- Vendor abstraction reference:
  [`../../docs/c-agent-vendor-modules.md`](../../docs/c-agent-vendor-modules.md)
- Application surface across IoT verticals:
  [`../../docs/c-agent-domains.md`](../../docs/c-agent-domains.md)
