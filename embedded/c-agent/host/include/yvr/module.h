/* Yaver c-agent — vendor module ABI.
 *
 * A "module" is a unit of vendor code that the c-agent runtime can
 * load, replace, pause, resume, and tear down independently of the
 * vendor's host binary. Vendors structure replaceable subsystems as
 * modules so that an AI-driven troubleshooting loop can hot-swap
 * specific components — and only those components — without
 * crashing the host process or its other dependents.
 *
 * This header is the stable C ABI vendors export FROM their module
 * artifacts (.so / .dylib / linkable .a). The companion runtime side
 * (load, quiesce, swap, dep ordering) is in <yvr/host.h>.
 *
 * Compatibility rules:
 *   - YVR_MODULE_ABI_VERSION is checked at every module load. A
 *     module compiled against ABI N must not load against host ABI
 *     M < N.
 *   - The vtable struct is APPEND-ONLY. New fields go at the end;
 *     existing fields keep their offset forever. Older modules that
 *     don't fill in new fields are still loadable.
 *   - Status code values are stable. Existing codes never change.
 */

#ifndef YVR_MODULE_H
#define YVR_MODULE_H

#include <stddef.h>
#include <stdint.h>

#include "event.h"

#ifdef __cplusplus
extern "C" {
#endif

#define YVR_MODULE_ABI_VERSION 1u

/* The symbol every module artifact must export. The host calls it
 * with the host's own ABI version; the module returns its vtable
 * (or NULL to refuse the load — host then keeps the previous
 * version, if any). */
#define YVR_MODULE_REGISTER_SYMBOL "yvr_module_register"

/* Opaque per-instance context. The host owns it. Never freed by the
 * module; never embedded in module data. Use the accessor functions
 * declared at the bottom of this header to interact with it. */
typedef struct yvr_module_ctx yvr_module_ctx_t;

/* Status codes returned by lifecycle callbacks. Keep numerically
 * stable. Module-specific failures should pick a generic error here
 * and put the human-readable detail in yvr_module_log(). */
typedef enum yvr_module_status {
    YVR_MODULE_OK              =  0,
    YVR_MODULE_E_BAD_INPUT     = -1,
    YVR_MODULE_E_NOT_READY     = -2,
    YVR_MODULE_E_TRANSIENT     = -3,  /* host may retry */
    YVR_MODULE_E_FATAL         = -4   /* host must not retry */
} yvr_module_status_t;

/* Lifecycle vtable. The host calls these on a single thread per
 * module instance; modules do not need to synchronize against each
 * other on the same instance. */
typedef struct yvr_module_vtable {
    /* ── Identity ────────────────────────────────────────────────
     * Filled in by YVR_MODULE_DEFINE; never modified afterwards. */
    uint32_t    abi_version;        /* must equal YVR_MODULE_ABI_VERSION */
    const char *name;               /* stable module identifier */
    const char *version;            /* semver string from build */

    /* ── Required callbacks ──────────────────────────────────────
     *
     * on_load is invoked exactly once after artifact load. The
     * module should allocate resources, restore state via
     * state_load if state was migrated from a previous version, and
     * be ready to handle invoke() calls when on_load returns OK.
     *
     * on_unload is invoked exactly once before the artifact is
     * dlclose()'d. By return, the module must have released
     * everything it allocated since on_load. */
    yvr_module_status_t (*on_load)  (yvr_module_ctx_t *ctx);
    void                (*on_unload)(yvr_module_ctx_t *ctx);

    /* invoke is the vendor-defined RPC entry. The host routes calls
     * from `yvr_host_invoke()` (and from AI-generated probes) to
     * this function. Method names + payload encoding are entirely
     * vendor-defined; the host treats both as opaque. The returned
     * buffer is owned by the module and must remain valid until the
     * NEXT call to invoke() on the same instance. The host copies
     * before forwarding to the caller, so module memory is not
     * shared across the call boundary. */
    void *(*invoke)(yvr_module_ctx_t *ctx,
                    const char       *method,
                    const void       *request,
                    size_t            request_len,
                    size_t           *out_response_len);

    /* ── Optional hot-swap callbacks ─────────────────────────────
     *
     * NULL is permitted for modules that don't need the hook. The
     * host treats NULL as a no-op success and proceeds.
     *
     * on_quiesce is called when the host is preparing to replace
     * THIS module or one of its DEPENDENCIES. The module should
     * stop initiating outbound calls, finish or abort in-flight
     * work, and release scarce resources (file descriptors, locked
     * caches). It MUST NOT exit the process. After quiesce, the
     * host stops routing invoke() calls to this module — pending
     * calls block at the host level, dependents see a clean
     * "module idle" status until on_resume.
     *
     * on_resume is called after the swap completes. The module
     * should reacquire any released resources and start handling
     * invoke() calls normally again.
     *
     * state_save / state_load handle state migration across a
     * version bump. on_quiesce is always called first; then
     * state_save returns a serialized blob the host hands to the
     * NEW version's state_load after on_load. The host owns the
     * blob: it is freed with the same allocator that produced it,
     * so modules must use yvr_module_alloc() (declared below) to
     * allocate the returned buffer. NULL state_save is treated as
     * "no state to migrate." */
    yvr_module_status_t (*on_quiesce)(yvr_module_ctx_t *ctx);
    yvr_module_status_t (*on_resume) (yvr_module_ctx_t *ctx);
    void *              (*state_save)(yvr_module_ctx_t *ctx, size_t *out_len);
    yvr_module_status_t (*state_load)(yvr_module_ctx_t *ctx,
                                      const void       *state,
                                      size_t            state_len);

    /* on_event is called for every event the module is subscribed
     * to — its own lifecycle events (LOADED, QUIESCED, etc.) and
     * any events for OTHER modules it subscribed to via
     * yvr_module_subscribe(). NULL = module doesn't care; host
     * skips the dispatch path for it.
     *
     * Same threading rules as the rest of the vtable: synchronous,
     * single-threaded per module instance. Do not block. */
    void (*on_event)(yvr_module_ctx_t *ctx, const yvr_event_t *event);

    /* ── Reserved ─────────────────────────────────────────────────
     * Append new fields below this line. Do not reorder. */
    void *_reserved[8];
} yvr_module_vtable_t;

/* Module entry point signature. Every module artifact MUST export a
 * symbol named YVR_MODULE_REGISTER_SYMBOL with this signature.
 * Returning NULL aborts load (host keeps the previous version). */
typedef const yvr_module_vtable_t *(*yvr_module_register_fn)(uint32_t host_abi_version);

/* Convenience macro to declare and export a module's registration
 * function. The vtable parameter must be a static struct. Use as:
 *
 *     static yvr_module_status_t my_load(yvr_module_ctx_t *c) { ... }
 *     ...
 *     YVR_MODULE_DEFINE("wifi_controller", "1.4.2", &(yvr_module_vtable_t){
 *         .on_load   = my_load,
 *         .on_unload = my_unload,
 *         .invoke    = my_invoke,
 *     });
 */
#define YVR_MODULE_DEFINE(name_str, version_str, vtable_ptr)             \
    const yvr_module_vtable_t *yvr_module_register(uint32_t host_abi)    \
    {                                                                    \
        if (host_abi < YVR_MODULE_ABI_VERSION) {                         \
            return (const yvr_module_vtable_t *)0;                       \
        }                                                                \
        (vtable_ptr)->abi_version = YVR_MODULE_ABI_VERSION;              \
        (vtable_ptr)->name        = (name_str);                          \
        (vtable_ptr)->version     = (version_str);                       \
        return (vtable_ptr);                                             \
    }

/* ── Helpers exported BY the host runtime, callable BY modules ─── */

/* Log a line tagged with the module's name. Safe to call from any
 * lifecycle callback. */
void yvr_module_log(yvr_module_ctx_t *ctx, const char *msg);

/* Persistent per-module state directory. Survives module reload and
 * host process restart. Returned pointer is owned by the host;
 * never NULL during an active lifecycle callback. */
const char *yvr_module_state_path(yvr_module_ctx_t *ctx);

/* The module's name, as registered. Useful for log scoping when a
 * single .so is loaded under multiple names. */
const char *yvr_module_name(yvr_module_ctx_t *ctx);

/* Allocate a buffer the host will eventually free (e.g. the return
 * value of state_save). Modules must use this rather than malloc
 * because the host's allocator may differ on embedded targets.
 * Returns NULL on OOM. Paired free is automatic — modules do NOT
 * call yvr_module_free on a buffer they returned to the host. */
void *yvr_module_alloc(yvr_module_ctx_t *ctx, size_t n);
void  yvr_module_free (yvr_module_ctx_t *ctx, void *p);

/* Subscribe THIS module to lifecycle events for `target_name`. The
 * subscription stays alive until on_unload is called or the
 * subscription is explicitly cancelled with yvr_module_unsubscribe.
 * Pass NULL for `target_name` to receive events for every module
 * (including this one). Events arrive via the on_event hook in the
 * vtable. */
yvr_subscription_id_t yvr_module_subscribe(yvr_module_ctx_t *ctx,
                                           const char       *target_name);

/* Cancel a subscription previously returned by yvr_module_subscribe.
 * Idempotent — unknown ids are ignored. */
void yvr_module_unsubscribe(yvr_module_ctx_t      *ctx,
                            yvr_subscription_id_t  id);

/* Synchronously invoke another module by name from within this
 * module's invoke()/on_event() callback. Same shape as the host-
 * side yvr_host_invoke — see <yvr/host.h>. The response buffer is
 * owned by the called module; copy before returning. */
void *yvr_module_invoke_peer(yvr_module_ctx_t *ctx,
                             const char       *target_name,
                             const char       *method,
                             const void       *request,
                             size_t            request_len,
                             size_t           *out_response_len);

#ifdef __cplusplus
}
#endif

#endif /* YVR_MODULE_H */
