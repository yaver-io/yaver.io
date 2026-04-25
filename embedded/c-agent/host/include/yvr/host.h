/* Yaver c-agent — host control plane (vendor side).
 *
 * Vendors include this header in their main app and link against
 * libyvr_cagent_host. It provides the abstraction layer over module
 * loading, hot-swap, dependency-aware lifecycle ordering, and the
 * event bus.
 *
 * The vendor's job is:
 *   1. Build their app as a host that calls into modules through
 *      yvr_host_invoke() instead of direct linkage.
 *   2. Declare which modules + which dependencies via a manifest
 *      (compile-time or AI-generated).
 *   3. Subscribe to lifecycle events with yvr_host_subscribe() so
 *      the host UI / telemetry can react to "module X is being
 *      replaced" without polling.
 *
 * Yaver's job (under the abstraction):
 *   - Verify signatures on incoming module artifacts.
 *   - Walk the dependency graph and quiesce dependents before a
 *     swap so they don't crash.
 *   - Drive on_quiesce / state_save → on_load / state_load /
 *     on_resume on the affected module.
 *   - Broadcast the right events at every step.
 *   - Retry transient errors per the manifest's policy.
 *   - Roll back on failure.
 */

#ifndef YVR_HOST_H
#define YVR_HOST_H

#include <stddef.h>
#include <stdint.h>

#include "event.h"
#include "manifest.h"
#include "module.h"

#ifdef __cplusplus
extern "C" {
#endif

/* Opaque host handle. */
typedef struct yvr_host yvr_host_t;

/* Status codes returned by host operations. Numerically stable. */
typedef enum yvr_host_status {
    YVR_HOST_OK              =  0,
    YVR_HOST_E_INVALID_ARG   = -1,
    YVR_HOST_E_NOT_FOUND     = -2,    /* module name unknown */
    YVR_HOST_E_NOT_READY     = -3,    /* called too early/too late */
    YVR_HOST_E_DEP_CYCLE     = -4,    /* manifest contains a cycle */
    YVR_HOST_E_VERIFY        = -5,    /* signature / hash check failed */
    YVR_HOST_E_LOAD          = -6,    /* dlopen / link failure */
    YVR_HOST_E_QUIESCE       = -7,    /* module refused / timed out quiesce */
    YVR_HOST_E_STATE         = -8,    /* state_save / state_load failed */
    YVR_HOST_E_INTERNAL      = -9
} yvr_host_status_t;

/* ── Lifecycle ───────────────────────────────────────────────── */

/* Initialize a host. `state_dir` is the per-app directory yaver
 * uses for module state, manifest cache, and per-module logs. It
 * is created if missing. Returns NULL on failure (errno set). */
yvr_host_t *yvr_host_init(const char *state_dir);

/* Apply a manifest. Resolves the dependency graph, loads modules
 * in dep order, fires LOADED events as each comes up. Returns
 * YVR_HOST_OK only when ALL modules loaded; on first failure, the
 * already-loaded modules are unloaded in reverse-dep order. */
yvr_host_status_t yvr_host_apply_manifest(yvr_host_t           *host,
                                          const yvr_manifest_t *manifest);

/* Tear down: unload every module in reverse-dep order, free host
 * resources. Idempotent. */
void yvr_host_shutdown(yvr_host_t *host);

/* Register an in-process "native" module — a module whose
 * vtable is statically linked into the vendor binary. Useful for
 * probes baked into the c-agent at compile time, before the
 * dynamic signed-module loader is in place. The vtable and name
 * must remain valid for the host's lifetime; the host does not
 * copy them.
 *
 * Native modules participate in the registry exactly like
 * dlopen-loaded ones: yvr_host_invoke routes calls into the
 * vtable, lifecycle events fire, pause / resume / quiesce all
 * work, dependents see the same event stream.
 *
 * Returns YVR_HOST_OK on success, or:
 *   YVR_HOST_E_INVALID_ARG if any argument is NULL
 *   YVR_HOST_E_INTERNAL    if the registry is out of memory
 *   YVR_HOST_OK            (no error) if a module of the same
 *                          name already exists — the existing
 *                          registration is left untouched. */
yvr_host_status_t yvr_host_register_native(yvr_host_t                 *host,
                                           const char                 *name,
                                           const yvr_module_vtable_t  *vtable);

/* ── Invoking modules ────────────────────────────────────────── */

/* Synchronous RPC call to a module by name. The vendor's app calls
 * this everywhere it would otherwise have a direct function call
 * into a replaceable subsystem.
 *
 * Returns the response buffer (owned by the host; release with
 * yvr_host_free_response when done) and writes its length to
 * *out_response_len. Returns NULL on error and sets
 * *out_status_code to a yvr_host_status_t.
 *
 * Behaviour while the target module is quiesced:
 *   - host queues the call up to manifest's invoke_queue_max
 *   - on resume, queued calls drain in order
 *   - if the queue is full, returns YVR_HOST_E_NOT_READY immediately
 *
 * Behaviour during a replace:
 *   - calls in flight when REPLACING starts complete against the
 *     OLD version
 *   - calls arriving after REPLACING are queued for the NEW version
 *   - if replace fails and the old version is restored, queued
 *     calls drain against the restored old version */
void *yvr_host_invoke(yvr_host_t        *host,
                      const char        *module_name,
                      const char        *method,
                      const void        *request,
                      size_t             request_len,
                      size_t            *out_response_len,
                      yvr_host_status_t *out_status_code);

void yvr_host_free_response(yvr_host_t *host, void *response);

/* ── Per-module control plane ────────────────────────────────── */

/* Pause: stop dispatching invokes to the module. Calls queue.
 * Module is NOT notified through on_quiesce — pause is the
 * external/operator-driven equivalent. Use this when you want to
 * temporarily isolate a misbehaving module without telling it. */
yvr_host_status_t yvr_host_pause_module(yvr_host_t *host, const char *name);

/* Resume after pause. Drains queued invokes. */
yvr_host_status_t yvr_host_resume_module(yvr_host_t *host, const char *name);

/* Quiesce: cooperative idle. Walks dependents first, fires
 * QUIESCED events, calls on_quiesce on the target. Returns when
 * all dependents are quiesced AND the target's on_quiesce returned
 * (or the manifest's quiesce_window_ms expired — in which case
 * status is YVR_HOST_E_QUIESCE and the module is force-paused). */
yvr_host_status_t yvr_host_quiesce_module(yvr_host_t *host, const char *name);

/* Replace: end-to-end hot-swap. Walks dependents, quiesces them,
 * quiesces the target, loads the new artifact, migrates state,
 * fires REPLACED, resumes the target, resumes dependents.
 * Subscribers see this whole sequence as REPLACING → REPLACED.
 * On failure, fires REPLACE_FAILED and restores the previous
 * version atomically. */
yvr_host_status_t yvr_host_replace_module(yvr_host_t *host,
                                          const char *name,
                                          const char *new_artifact);

/* Stop a module entirely. Walks dependents, quiesces them, calls
 * on_unload, removes the module from the host's registry. The
 * dep graph allows starting it again later via apply_manifest. */
yvr_host_status_t yvr_host_stop_module(yvr_host_t *host, const char *name);

/* ── Event subscription (vendor side) ────────────────────────── */

/* Subscribe the vendor's host code to lifecycle events. Pass
 * `module_filter = NULL` to receive events for every module.
 * Returns a subscription id (non-zero on success). */
yvr_subscription_id_t yvr_host_subscribe(yvr_host_t      *host,
                                         const char      *module_filter,
                                         yvr_event_cb_t   cb,
                                         void            *user);

/* Cancel a subscription. Idempotent. */
void yvr_host_unsubscribe(yvr_host_t            *host,
                          yvr_subscription_id_t  id);

/* ── Introspection ───────────────────────────────────────────── */

/* Snapshot of a module's runtime state. */
typedef enum yvr_module_state {
    YVR_MS_UNKNOWN  = 0,
    YVR_MS_LOADING  = 1,
    YVR_MS_ACTIVE   = 2,
    YVR_MS_QUIESCED = 3,
    YVR_MS_PAUSED   = 4,
    YVR_MS_REPLACING= 5,
    YVR_MS_FAILED   = 6,
    YVR_MS_UNLOADED = 7
} yvr_module_state_t;

yvr_module_state_t yvr_host_module_state(yvr_host_t *host, const char *name);

/* Iterate the loaded modules. Returns count; if `out_names` is
 * not NULL and `cap` > 0, fills up to `cap` name pointers. The
 * names are owned by the host and stable for the host's lifetime. */
size_t yvr_host_list_modules(yvr_host_t  *host,
                             const char **out_names,
                             size_t       cap);

#ifdef __cplusplus
}
#endif

#endif /* YVR_HOST_H */
