/* Yaver c-agent — host runtime (stub).
 *
 * This file provides definitions for every symbol declared in
 * <yvr/host.h>, <yvr/module.h>, and <yvr/event.h> so the public ABI
 * compiles and links cleanly today. The behaviour is deliberately
 * minimal — control APIs return YVR_HOST_E_NOT_READY, the event bus
 * accepts subscriptions but never fires, modules can be registered
 * but cannot be replaced, etc.
 *
 * The point of the stub is to lock down the ABI so vendors can
 * start integrating against this header set today, while the real
 * loader / dep walker / quiesce machinery is built underneath.
 *
 * Replace incrementally with real implementations:
 *   1. dlopen + signature verify (host_load.c)
 *   2. dep graph + topo sort (host_graph.c)
 *   3. quiesce + state migration (host_lifecycle.c)
 *   4. event dispatch (host_events.c)
 *   5. invoke routing + queueing (host_invoke.c)
 */

#include "yvr/host.h"
#include "yvr/module.h"

#include <errno.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

struct yvr_host {
    char *state_dir;
    /* The real host will hold: module table, event subscribers,
     * dep graph, dispatch queues, lock state, allocator pair. */
};

struct yvr_module_ctx {
    yvr_host_t *host;
    const char *name;
    char       *state_path;
    /* Real ctx holds the module's vtable, .so handle, current
     * state machine position, owned allocations, queue head. */
};

/* ── Lifecycle ──────────────────────────────────────────────── */

yvr_host_t *yvr_host_init(const char *state_dir)
{
    if (state_dir == NULL || state_dir[0] == '\0') {
        errno = EINVAL;
        return NULL;
    }
    yvr_host_t *h = calloc(1, sizeof(*h));
    if (h == NULL) return NULL;
    h->state_dir = strdup(state_dir);
    if (h->state_dir == NULL) {
        free(h);
        return NULL;
    }
    return h;
}

yvr_host_status_t yvr_host_apply_manifest(yvr_host_t           *host,
                                          const yvr_manifest_t *manifest)
{
    if (host == NULL || manifest == NULL) return YVR_HOST_E_INVALID_ARG;
    /* Real implementation: validate, topo-sort, dlopen each module
     * in dep order, fire LOADED events. Stub silently accepts. */
    return YVR_HOST_OK;
}

void yvr_host_shutdown(yvr_host_t *host)
{
    if (host == NULL) return;
    free(host->state_dir);
    free(host);
}

/* ── Invoke ─────────────────────────────────────────────────── */

void *yvr_host_invoke(yvr_host_t        *host,
                      const char        *module_name,
                      const char        *method,
                      const void        *request,
                      size_t             request_len,
                      size_t            *out_response_len,
                      yvr_host_status_t *out_status_code)
{
    (void)host; (void)module_name; (void)method;
    (void)request; (void)request_len;
    if (out_response_len != NULL) *out_response_len = 0;
    if (out_status_code  != NULL) *out_status_code  = YVR_HOST_E_NOT_FOUND;
    return NULL;
}

void yvr_host_free_response(yvr_host_t *host, void *response)
{
    (void)host;
    free(response);
}

/* ── Per-module control plane (stub: not-ready) ─────────────── */

yvr_host_status_t yvr_host_pause_module(yvr_host_t *host, const char *name)
{
    (void)host; (void)name;
    return YVR_HOST_E_NOT_READY;
}

yvr_host_status_t yvr_host_resume_module(yvr_host_t *host, const char *name)
{
    (void)host; (void)name;
    return YVR_HOST_E_NOT_READY;
}

yvr_host_status_t yvr_host_quiesce_module(yvr_host_t *host, const char *name)
{
    (void)host; (void)name;
    return YVR_HOST_E_NOT_READY;
}

yvr_host_status_t yvr_host_replace_module(yvr_host_t *host,
                                          const char *name,
                                          const char *new_artifact)
{
    (void)host; (void)name; (void)new_artifact;
    return YVR_HOST_E_NOT_READY;
}

yvr_host_status_t yvr_host_stop_module(yvr_host_t *host, const char *name)
{
    (void)host; (void)name;
    return YVR_HOST_E_NOT_READY;
}

/* ── Subscriptions ──────────────────────────────────────────── */

yvr_subscription_id_t yvr_host_subscribe(yvr_host_t      *host,
                                         const char      *module_filter,
                                         yvr_event_cb_t   cb,
                                         void            *user)
{
    (void)host; (void)module_filter; (void)cb; (void)user;
    /* Stub: always returns 0 (no-subscription sentinel). Real impl
     * will register the callback in an event-bus table. */
    return 0u;
}

void yvr_host_unsubscribe(yvr_host_t            *host,
                          yvr_subscription_id_t  id)
{
    (void)host; (void)id;
}

/* ── Introspection ──────────────────────────────────────────── */

yvr_module_state_t yvr_host_module_state(yvr_host_t *host, const char *name)
{
    (void)host; (void)name;
    return YVR_MS_UNKNOWN;
}

size_t yvr_host_list_modules(yvr_host_t  *host,
                             const char **out_names,
                             size_t       cap)
{
    (void)host; (void)out_names; (void)cap;
    return 0u;
}

/* ── Module-side helpers (stub) ─────────────────────────────── */

void yvr_module_log(yvr_module_ctx_t *ctx, const char *msg)
{
    if (ctx == NULL || msg == NULL) return;
    fprintf(stderr, "[yvr_module:%s] %s\n",
            ctx->name != NULL ? ctx->name : "?", msg);
}

const char *yvr_module_state_path(yvr_module_ctx_t *ctx)
{
    return ctx != NULL ? ctx->state_path : NULL;
}

const char *yvr_module_name(yvr_module_ctx_t *ctx)
{
    return ctx != NULL ? ctx->name : NULL;
}

void *yvr_module_alloc(yvr_module_ctx_t *ctx, size_t n)
{
    (void)ctx;
    return malloc(n);
}

void yvr_module_free(yvr_module_ctx_t *ctx, void *p)
{
    (void)ctx;
    free(p);
}

yvr_subscription_id_t yvr_module_subscribe(yvr_module_ctx_t *ctx,
                                           const char       *target_name)
{
    (void)ctx; (void)target_name;
    return 0u;
}

void yvr_module_unsubscribe(yvr_module_ctx_t      *ctx,
                            yvr_subscription_id_t  id)
{
    (void)ctx; (void)id;
}

void *yvr_module_invoke_peer(yvr_module_ctx_t *ctx,
                             const char       *target_name,
                             const char       *method,
                             const void       *request,
                             size_t            request_len,
                             size_t           *out_response_len)
{
    (void)ctx; (void)target_name; (void)method;
    (void)request; (void)request_len;
    if (out_response_len != NULL) *out_response_len = 0;
    return NULL;
}
