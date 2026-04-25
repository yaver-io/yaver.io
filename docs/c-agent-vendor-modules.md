# C-Agent Vendor Modules

This document describes the abstraction layer vendors use to make
their device code AI-replaceable at runtime. It is the application-
side companion to [`c-agent-architecture.md`](./c-agent-architecture.md);
the architecture doc explains the runtime, the [domains
doc](./c-agent-domains.md) explains where the runtime is worth
deploying, and this doc explains how a vendor's own binary plugs
into it.

## Status

Part of the c-agent design baseline — an additional Yaver surface,
not a pivot. The dev-machine agent in
[`../desktop/agent/`](../desktop/agent/) remains Yaver's primary
product. The headers + stub library described here exist today
under [`../embedded/c-agent/host/`](../embedded/c-agent/host/) so
that vendor integration work can begin against a stable ABI while
the runtime is fleshed out underneath.

## The model in one sentence

A vendor's app is a host process that runs alongside the c-agent;
the c-agent owns a registry of independently-loadable code units
(*modules*) that the host calls into for replaceable functionality,
and the c-agent — driven by an AI brain in the cloud — can hot-swap
those modules at runtime without crashing the host or its other
modules.

## What vendors do at compile time

Three things, all small:

1. **Link against `libyvr_cagent_host`.** Adds the host runtime to
   the vendor binary. ~15 KB after stripping. See the [README
   integration section](../embedded/c-agent/README.md#toolchain-integration)
   for the concrete invocation.

2. **Replace direct linkage to subsystems with `yvr_host_invoke`.**
   Wherever the vendor has logic they want to be replaceable later,
   they dispatch through the host instead of calling the function
   directly. The dispatched-to code becomes a module.

3. **Declare modules + dependencies in a manifest.** Either
   programmatically as a `yvr_manifest_t` literal, or via an
   AI-generated YAML manifest the host loads at startup. See
   [`../embedded/c-agent/host/include/yvr/manifest.h`](../embedded/c-agent/host/include/yvr/manifest.h).

That's it for the compile-time side. Everything else — loading,
signature verification, dependency-aware lifecycle ordering,
quiescing, state migration, retry, rollback — is yaver's job under
the abstraction.

## What modules look like

A module is a shared object (`.so` / `.dylib`, or static `.a` for
a baked-in default) that exports a single registration symbol:

```c
#include <yvr/module.h>

static yvr_module_status_t my_load   (yvr_module_ctx_t *c) { /* ... */ return YVR_MODULE_OK; }
static void                my_unload (yvr_module_ctx_t *c) { /* ... */ }
static void *              my_invoke (yvr_module_ctx_t *c, const char *method,
                                      const void *req, size_t req_len, size_t *out_len) {
    /* vendor RPC: dispatch on method name, encode response, return buffer */
}

YVR_MODULE_DEFINE("wifi_controller", "1.4.2", &(yvr_module_vtable_t){
    .on_load   = my_load,
    .on_unload = my_unload,
    .invoke    = my_invoke,
    /* optional hot-swap hooks */
    .on_quiesce = my_quiesce,
    .on_resume  = my_resume,
    .state_save = my_state_save,
    .state_load = my_state_load,
    .on_event   = my_on_event,
});
```

The full ABI is in
[`embedded/c-agent/host/include/yvr/module.h`](../embedded/c-agent/host/include/yvr/module.h).
Method names + payload encoding inside `invoke` are entirely
vendor-defined; the host treats both as opaque and just routes.

## What the host code looks like

```c
#include <yvr/host.h>

int main(void) {
    yvr_host_t *host = yvr_host_init("/var/lib/myco");
    if (!host) return 1;

    /* Manifest: which modules + what depends on what. Vendors
     * either hand-write this or run the build-time `yvr-deps-gen`
     * tool that AI-generates it from source-tree analysis. */
    static const char *deps_config[] = { "config_loader" };
    static const yvr_module_spec_t mods[] = {
        { .name = "config_loader",
          .artifact = "/usr/lib/myco/config_loader.so",
          .state_policy = YVR_STATE_PERSISTENT,
          .quiesce_window_ms = 1000 },
        { .name = "telemetry",
          .artifact = "/usr/lib/myco/telemetry.so",
          .deps = deps_config, .deps_count = 1,
          .state_policy = YVR_STATE_TRANSIENT,
          .quiesce_window_ms = 500 },
        { .name = "wifi_controller",
          .artifact = "/usr/lib/myco/wifi_controller.so",
          .deps = deps_config, .deps_count = 1,
          .state_policy = YVR_STATE_PERSISTENT,
          .quiesce_window_ms = 2000 },
    };
    static const yvr_manifest_t manifest = {
        .vendor = "myco", .app_name = "wifi-edge", .version = "1.0.0",
        .modules = mods, .modules_count = sizeof(mods)/sizeof(mods[0]),
    };
    if (yvr_host_apply_manifest(host, &manifest) != YVR_HOST_OK) return 2;

    /* Subscribe to lifecycle events for the UI. */
    yvr_host_subscribe(host, NULL, on_event, ui_state);

    /* Vendor's main loop. Anywhere a replaceable subsystem is
     * called, dispatch through the host. */
    for (;;) {
        size_t out_len = 0;
        yvr_host_status_t st = YVR_HOST_OK;
        void *resp = yvr_host_invoke(host,
                                     "wifi_controller", "scan",
                                     NULL, 0, &out_len, &st);
        /* ... use resp ... */
        yvr_host_free_response(host, resp);
    }

    yvr_host_shutdown(host);
}
```

The entire abstraction surface is in
[`embedded/c-agent/host/include/yvr/host.h`](../embedded/c-agent/host/include/yvr/host.h).

## What yaver does under the abstraction

When the AI brain — driven by an iterative diagnostic loop — decides
to ship a fix, the brain compiles a new version of the affected
module, signs it, and ships it via the same wire protocol used for
diagnostic probes. Yaver's host runtime then does the following,
without any vendor code involvement:

```
brain ──► yaver host
              │
              ├─ verify module signature against pinned roots
              ├─ verify capabilities ⊆ device allowlist
              ├─ resolve dependents: who calls this module?
              │     wifi_controller is called by ui_state
              │
              ├─ broadcast YVR_EVT_MODULE_REPLACING (subscribers
              │   are the vendor host code + every dependent
              │   module + the AI brain)
              │
              ├─ pause invoke routing to wifi_controller
              │   queued invokes hold up to invoke_queue_max
              │
              ├─ on_quiesce(ui_state)        ← dependent first
              │   ui_state stops issuing wifi calls, buffers user
              │   actions, releases scarce resources
              │
              ├─ on_quiesce(wifi_controller) ← target
              │   wifi_controller finishes in-flight scans, unwires
              │   netlink subscriptions
              │
              ├─ state_save(wifi_controller) → blob
              ├─ on_unload(wifi_controller)
              ├─ dlclose() old artifact
              │
              ├─ dlopen() new artifact + verify hash
              ├─ on_load(new wifi_controller)
              ├─ state_load(new wifi_controller, blob)
              ├─ on_resume(wifi_controller)
              │
              ├─ on_resume(ui_state) ← drains buffered actions
              │
              ├─ broadcast YVR_EVT_MODULE_REPLACED
              │
              └─ resume invoke routing; queue drains in order
```

If anything fails — verification, on_load, state_load — yaver fires
`YVR_EVT_MODULE_REPLACE_FAILED`, restores the old version, and
resumes dependents. The vendor's host process never restarts.

## Per-module control plane

Beyond replace, yaver exposes per-module control so an operator
(via mobile) or the AI brain can act on a single module without
touching the rest:

| API | What it does |
|---|---|
| `yvr_host_pause_module` | Hold all invokes to this module. Module not notified — used for external isolation of a misbehaving module. |
| `yvr_host_resume_module` | Drain queued invokes. |
| `yvr_host_quiesce_module` | Cooperative idle: walks dependents first, fires QUIESCED events, calls `on_quiesce` on the target. |
| `yvr_host_replace_module` | End-to-end hot swap (sequence above). |
| `yvr_host_stop_module` | Walk dependents, quiesce them, `on_unload`, remove from registry. |

## Event bus

Every module lifecycle change broadcasts to subscribers. Modules
subscribe by setting `on_event` in their vtable + calling
`yvr_module_subscribe()`; the vendor's host code subscribes via
`yvr_host_subscribe()`.

Event kinds (full list in
[`embedded/c-agent/host/include/yvr/event.h`](../embedded/c-agent/host/include/yvr/event.h)):

```
YVR_EVT_MODULE_LOADED
YVR_EVT_MODULE_REPLACING       ← "yaver: I'm about to replace X"
YVR_EVT_MODULE_REPLACED        ← "yaver: replaced X"
YVR_EVT_MODULE_REPLACE_FAILED  ← "yaver: replacement of X failed; old version restored"
YVR_EVT_MODULE_QUIESCED        ← "yaver: X is now idle"
YVR_EVT_MODULE_RESUMED         ← "yaver: X is back live"
YVR_EVT_MODULE_PAUSED          ← "yaver: X paused externally"
YVR_EVT_MODULE_ERROR           ← "yaver: X reported an error"
YVR_EVT_MODULE_RETRYING        ← "yaver: retrying X (attempt N of M)"
YVR_EVT_MODULE_UNLOADING
YVR_EVT_MODULE_UNLOADED
```

This is the "told what happened" abstraction. Dependents use it to
gracefully degrade during the brief window where their target is
unavailable — route traffic to a fallback, surface a "wifi
temporarily idle" state to the UI, hold network requests in a
queue, log the lifecycle for telemetry, etc. — without vendor code
having to poll or know anything about the swap mechanism.

## State migration across versions

A new version of a module can have a different internal layout from
the old one. The host bridges them:

1. `state_save` on the old version returns a versioned blob.
2. The blob's encoding is vendor-defined (CBOR + a schema version
   integer is the recommended pattern).
3. `state_load` on the new version reads the blob, migrates fields
   it understands, ignores fields it doesn't, and synthesizes
   defaults for fields it added.

The host owns the blob between save and load — it is freed with
the same allocator that produced it (`yvr_module_alloc` /
`yvr_module_free`), and never crosses a process or device boundary
in plaintext.

For modules with `state_policy = YVR_STATE_PERSISTENT`, the blob
is also written to `yvr_module_state_path()` so it survives a host
process restart.

## Dependency manifests — three sources

The `yvr_manifest_t` shape is the same regardless of where it
comes from:

1. **Hand-coded in C.** Vendor writes literals in their `main()`
   like the example above. Best for tiny apps.

2. **AI-generated at compile time** via the `yvr-deps-gen` tool
   (Phase-3 work). The tool reads the vendor's source tree, grep
   for `yvr_host_invoke(host, "X", ...)` calls, builds the
   call-graph implicit dependency edges, emits a YAML manifest the
   host loads at startup. Manual deps + hints can override.

3. **AI-shipped at runtime.** The brain itself produces a new
   manifest signed alongside other module artifacts, and the host
   re-evaluates the dep graph + does ordered swaps to converge
   on the new topology. Useful when the brain decides a refactor
   that splits one module into two is the right fix.

All three resolve to the same in-memory `yvr_manifest_t` and run
through the same `yvr_host_apply_manifest()` entry point.

## Resource caps per module

Each module declares (in its manifest entry):

```
quiesce_window_ms     wait this long for graceful quiesce
max_retries           transient-error retry budget
invoke_queue_max      queued invokes during quiesce / replace
```

A module that exceeds `quiesce_window_ms` is force-paused; one
that exhausts `max_retries` triggers `YVR_EVT_MODULE_ERROR` with a
fatal status and is removed from the registry until manually
restarted. A module whose invoke queue is full returns
`YVR_HOST_E_NOT_READY` to the caller immediately rather than
blocking — callers (other modules or the host) decide whether to
retry.

## Threading model

- Each module instance is single-threaded as seen from its own
  callbacks — `on_load`, `on_unload`, `invoke`, `on_quiesce`,
  `on_resume`, `state_save`, `state_load`, `on_event` are all
  serialized.
- Different modules can be active concurrently on different host
  threads.
- `yvr_host_invoke` is synchronous from the caller's perspective;
  the host serializes calls into the target module instance behind
  the scenes.
- Subscribers — both module `on_event` and host `yvr_event_cb_t` —
  must not block the dispatch thread. If real work is needed,
  copy the event and dispatch onto a vendor-owned queue.

## What is NOT in the abstraction

To keep the trust + scope boundary clean, the vendor host
abstraction explicitly does NOT cover:

- **Process supervision.** This is in-process module supervision
  only. If the vendor wants out-of-process isolation
  (e.g. one module per process for fault containment), the vendor
  writes that with normal OS APIs.
- **Networking and transport.** `yvr_cagent_core` handles the wire
  to the brain; modules don't initiate network calls themselves
  (or if they do, that's vendor-defined business logic, not part
  of the c-agent abstraction).
- **Storage.** `yvr_module_state_path()` returns a directory path;
  what goes in it is vendor-defined.
- **Cross-host messaging.** Modules talk to other modules in the
  same host via `yvr_module_invoke_peer`. Cross-host is a separate
  concern; do it over the c-agent transport.

## Files

- ABI:
  [`embedded/c-agent/host/include/yvr/host.h`](../embedded/c-agent/host/include/yvr/host.h),
  [`module.h`](../embedded/c-agent/host/include/yvr/module.h),
  [`event.h`](../embedded/c-agent/host/include/yvr/event.h),
  [`manifest.h`](../embedded/c-agent/host/include/yvr/manifest.h)
- Stub runtime:
  [`embedded/c-agent/host/src/host_stub.c`](../embedded/c-agent/host/src/host_stub.c)
- Integration examples:
  [`embedded/c-agent/examples/`](../embedded/c-agent/examples/)
- Packaging recipes:
  [`embedded/c-agent/packaging/`](../embedded/c-agent/packaging/)
