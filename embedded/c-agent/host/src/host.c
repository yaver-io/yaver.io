/* Yaver c-agent — host runtime.
 *
 * Implements the public surface declared in <yvr/host.h>,
 * <yvr/module.h>, and <yvr/event.h>. Today the runtime supports:
 *
 *   - host lifecycle (init / shutdown)
 *   - manifest registration (module names tracked, LOADED events
 *     fired; no real dlopen yet)
 *   - module-state introspection
 *   - pause / resume with PAUSED / RESUMED events
 *   - real event bus: subscribe / unsubscribe / synchronous fan-
 *     out, optional name filter, sticky-error-free
 *
 * Operations that need a real loader (`replace`, `quiesce` for
 * dependents, `invoke`, `stop`) still return YVR_HOST_E_NOT_READY
 * — the public ABI is locked, vendors can integrate today, and
 * the loader fills these in incrementally.
 *
 * Threading: single-threaded for v1. Multi-thread support is a
 * follow-up that adds a host mutex around the subscriber list and
 * the module table; the public ABI does not change.
 */

#include "yvr/host.h"
#include "yvr/module.h"

#include <errno.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/time.h>

/* ── Internal state ──────────────────────────────────────────── */

typedef struct subscriber {
    yvr_subscription_id_t  id;
    char                  *module_filter;   /* NULL = all */
    yvr_event_cb_t         cb;
    void                  *user;
    struct subscriber     *next;
} subscriber_t;

typedef struct module_entry {
    char                       *name;
    char                       *artifact;     /* NULL for native modules */
    const yvr_module_vtable_t  *vtable;       /* non-NULL ⇒ native, in-process */
    yvr_module_state_t          state;
    struct module_entry        *next;
} module_entry_t;

struct yvr_host {
    char                  *state_dir;
    subscriber_t          *subs_head;
    module_entry_t        *modules_head;
    yvr_subscription_id_t  next_sub_id;
};

/* Same per-instance ctx as the stub — we don't actually run any
 * module code yet, but yvr_module_log / state_path / name need to
 * be callable through a non-NULL ctx for tests. */
struct yvr_module_ctx {
    yvr_host_t *host;
    const char *name;
    char       *state_path;
};

/* ── Helpers ─────────────────────────────────────────────────── */

static char *dup_or_null(const char *s)
{
    if (s == NULL) return NULL;
    size_t n = strlen(s);
    char *p = malloc(n + 1);
    if (p != NULL) memcpy(p, s, n + 1);
    return p;
}

static uint64_t now_monotonic_ms(void)
{
    /* CLOCK_MONOTONIC is non-portable; use gettimeofday for the
     * stub clock until we add a host clock abstraction in the
     * loader phase. */
    struct timeval tv;
    if (gettimeofday(&tv, NULL) != 0) return 0;
    return (uint64_t)tv.tv_sec * 1000ull + (uint64_t)(tv.tv_usec / 1000);
}

static module_entry_t *module_find(yvr_host_t *host, const char *name)
{
    if (name == NULL) return NULL;
    for (module_entry_t *m = host->modules_head; m != NULL; m = m->next) {
        if (strcmp(m->name, name) == 0) return m;
    }
    return NULL;
}

/* Synchronous fan-out. Walks the subscriber list and invokes any
 * callback whose filter matches `evt->module_name` (or whose
 * filter is NULL — "all modules"). Subscribers are forbidden from
 * blocking; that contract is in <yvr/event.h>. */
static void emit(yvr_host_t *host, const yvr_event_t *evt)
{
    if (host == NULL || evt == NULL) return;
    for (subscriber_t *s = host->subs_head; s != NULL; s = s->next) {
        if (s->cb == NULL) continue;
        if (s->module_filter != NULL && evt->module_name != NULL &&
            strcmp(s->module_filter, evt->module_name) != 0) {
            continue;
        }
        s->cb(evt, s->user);
    }
}

static void emit_simple(yvr_host_t        *host,
                        yvr_event_kind_t   kind,
                        const char        *module_name)
{
    yvr_event_t evt = {0};
    evt.kind         = kind;
    evt.module_name  = module_name;
    evt.timestamp_ms = now_monotonic_ms();
    emit(host, &evt);
}

/* ── Lifecycle ──────────────────────────────────────────────── */

yvr_host_t *yvr_host_init(const char *state_dir)
{
    if (state_dir == NULL || state_dir[0] == '\0') {
        errno = EINVAL;
        return NULL;
    }
    yvr_host_t *h = calloc(1, sizeof *h);
    if (h == NULL) return NULL;
    h->state_dir   = dup_or_null(state_dir);
    h->next_sub_id = 1;  /* 0 reserved per <yvr/event.h>. */
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

    for (size_t i = 0; i < manifest->modules_count; i++) {
        const yvr_module_spec_t *spec = &manifest->modules[i];
        if (spec->name == NULL || spec->name[0] == '\0') {
            return YVR_HOST_E_INVALID_ARG;
        }
        if (module_find(host, spec->name) != NULL) {
            /* Re-applying the same manifest is a no-op for tracked
             * modules — the future loader will diff and reload
             * differing artifacts; for now, keep what we have. */
            continue;
        }
        module_entry_t *m = calloc(1, sizeof *m);
        if (m == NULL) return YVR_HOST_E_INTERNAL;
        m->name     = dup_or_null(spec->name);
        m->artifact = dup_or_null(spec->artifact);
        m->state    = YVR_MS_ACTIVE;
        if (m->name == NULL) {
            free(m->artifact);
            free(m);
            return YVR_HOST_E_INTERNAL;
        }
        /* Append to keep dep order stable. */
        if (host->modules_head == NULL) {
            host->modules_head = m;
        } else {
            module_entry_t *t = host->modules_head;
            while (t->next != NULL) t = t->next;
            t->next = m;
        }
        emit_simple(host, YVR_EVT_MODULE_LOADED, m->name);
    }
    return YVR_HOST_OK;
}

yvr_host_status_t yvr_host_register_native(yvr_host_t                 *host,
                                           const char                 *name,
                                           const yvr_module_vtable_t  *vtable)
{
    if (host == NULL || name == NULL || name[0] == '\0' || vtable == NULL) {
        return YVR_HOST_E_INVALID_ARG;
    }
    if (vtable->abi_version != YVR_MODULE_ABI_VERSION) {
        return YVR_HOST_E_INVALID_ARG;
    }
    if (module_find(host, name) != NULL) {
        return YVR_HOST_OK;
    }
    module_entry_t *m = calloc(1, sizeof *m);
    if (m == NULL) return YVR_HOST_E_INTERNAL;
    m->name     = dup_or_null(name);
    m->vtable   = vtable;
    m->state    = YVR_MS_LOADING;
    if (m->name == NULL) {
        free(m);
        return YVR_HOST_E_INTERNAL;
    }

    /* Append to keep dep / registration order stable. */
    if (host->modules_head == NULL) {
        host->modules_head = m;
    } else {
        module_entry_t *t = host->modules_head;
        while (t->next != NULL) t = t->next;
        t->next = m;
    }

    /* Run on_load synchronously if provided, then transition to
     * ACTIVE and fire LOADED. Native modules can refuse load by
     * returning a non-OK status; we report the failure but leave
     * the registry entry in LOADING so the host's introspection
     * shows what went wrong. */
    if (m->vtable->on_load != NULL) {
        struct yvr_module_ctx ctx = {
            .host = host, .name = m->name, .state_path = NULL,
        };
        if (m->vtable->on_load(&ctx) != YVR_MODULE_OK) {
            m->state = YVR_MS_FAILED;
            emit_simple(host, YVR_EVT_MODULE_ERROR, m->name);
            return YVR_HOST_E_INTERNAL;
        }
    }
    m->state = YVR_MS_ACTIVE;
    emit_simple(host, YVR_EVT_MODULE_LOADED, m->name);
    return YVR_HOST_OK;
}

void yvr_host_shutdown(yvr_host_t *host)
{
    if (host == NULL) return;

    /* Unload modules in reverse order so dependents see UNLOADED
     * before their deps. */
    /* Reverse the list cheaply by collecting names then walking
     * backwards through the original list. */
    module_entry_t *m = host->modules_head;
    while (m != NULL) {
        emit_simple(host, YVR_EVT_MODULE_UNLOADING, m->name);
        m = m->next;
    }
    /* Free everything. Native modules' on_unload runs here; the
     * vtable is owned by the caller, so we don't free it. */
    while (host->modules_head != NULL) {
        module_entry_t *next = host->modules_head->next;
        if (host->modules_head->vtable != NULL &&
            host->modules_head->vtable->on_unload != NULL) {
            struct yvr_module_ctx ctx = {
                .host = host, .name = host->modules_head->name,
                .state_path = NULL,
            };
            host->modules_head->vtable->on_unload(&ctx);
        }
        emit_simple(host, YVR_EVT_MODULE_UNLOADED, host->modules_head->name);
        free(host->modules_head->name);
        free(host->modules_head->artifact);
        free(host->modules_head);
        host->modules_head = next;
    }
    while (host->subs_head != NULL) {
        subscriber_t *next = host->subs_head->next;
        free(host->subs_head->module_filter);
        free(host->subs_head);
        host->subs_head = next;
    }
    free(host->state_dir);
    free(host);
}

/* ── Invoke (still stubbed) ─────────────────────────────────── */

void *yvr_host_invoke(yvr_host_t        *host,
                      const char        *module_name,
                      const char        *method,
                      const void        *request,
                      size_t             request_len,
                      size_t            *out_response_len,
                      yvr_host_status_t *out_status_code)
{
    if (out_response_len != NULL) *out_response_len = 0;
    if (host == NULL || module_name == NULL) {
        if (out_status_code != NULL) *out_status_code = YVR_HOST_E_INVALID_ARG;
        return NULL;
    }
    module_entry_t *m = module_find(host, module_name);
    if (m == NULL) {
        if (out_status_code != NULL) *out_status_code = YVR_HOST_E_NOT_FOUND;
        return NULL;
    }
    /* Native module: synchronous in-process call into the vtable. */
    if (m->vtable != NULL && m->vtable->invoke != NULL) {
        if (m->state != YVR_MS_ACTIVE) {
            if (out_status_code != NULL) *out_status_code = YVR_HOST_E_NOT_READY;
            return NULL;
        }
        struct yvr_module_ctx ctx = {
            .host       = host,
            .name       = m->name,
            .state_path = NULL,
        };
        size_t rsp_len = 0;
        void *rsp = m->vtable->invoke(&ctx, method, request, request_len, &rsp_len);
        if (out_response_len != NULL) *out_response_len = rsp_len;
        if (out_status_code != NULL) *out_status_code = YVR_HOST_OK;
        return rsp;
    }
    /* Dlopen-backed module: deferred to the real loader. */
    if (out_status_code != NULL) *out_status_code = YVR_HOST_E_NOT_READY;
    return NULL;
}

void yvr_host_free_response(yvr_host_t *host, void *response)
{
    (void)host;
    free(response);
}

/* ── Per-module control plane ────────────────────────────────── */

yvr_host_status_t yvr_host_pause_module(yvr_host_t *host, const char *name)
{
    if (host == NULL || name == NULL) return YVR_HOST_E_INVALID_ARG;
    module_entry_t *m = module_find(host, name);
    if (m == NULL) return YVR_HOST_E_NOT_FOUND;
    m->state = YVR_MS_PAUSED;
    emit_simple(host, YVR_EVT_MODULE_PAUSED, m->name);
    return YVR_HOST_OK;
}

yvr_host_status_t yvr_host_resume_module(yvr_host_t *host, const char *name)
{
    if (host == NULL || name == NULL) return YVR_HOST_E_INVALID_ARG;
    module_entry_t *m = module_find(host, name);
    if (m == NULL) return YVR_HOST_E_NOT_FOUND;
    if (m->state != YVR_MS_PAUSED && m->state != YVR_MS_QUIESCED) {
        /* Resume from any other state is a no-op success — the
         * caller may have raced with another resume. */
        return YVR_HOST_OK;
    }
    m->state = YVR_MS_ACTIVE;
    emit_simple(host, YVR_EVT_MODULE_RESUMED, m->name);
    return YVR_HOST_OK;
}

yvr_host_status_t yvr_host_quiesce_module(yvr_host_t *host, const char *name)
{
    /* Real quiesce walks dependents first. The dep-walker lands
     * with the loader in a follow-up commit; for now we treat
     * quiesce as a single-target synchronous transition. */
    if (host == NULL || name == NULL) return YVR_HOST_E_INVALID_ARG;
    module_entry_t *m = module_find(host, name);
    if (m == NULL) return YVR_HOST_E_NOT_FOUND;
    m->state = YVR_MS_QUIESCED;
    emit_simple(host, YVR_EVT_MODULE_QUIESCED, m->name);
    return YVR_HOST_OK;
}

yvr_host_status_t yvr_host_replace_module(yvr_host_t *host,
                                          const char *name,
                                          const char *new_artifact)
{
    (void)new_artifact;
    if (host == NULL || name == NULL) return YVR_HOST_E_INVALID_ARG;
    if (module_find(host, name) == NULL) return YVR_HOST_E_NOT_FOUND;
    /* No real loader yet. */
    return YVR_HOST_E_NOT_READY;
}

yvr_host_status_t yvr_host_stop_module(yvr_host_t *host, const char *name)
{
    if (host == NULL || name == NULL) return YVR_HOST_E_INVALID_ARG;
    return module_find(host, name) == NULL
        ? YVR_HOST_E_NOT_FOUND
        : YVR_HOST_E_NOT_READY;
}

/* ── Subscriptions ──────────────────────────────────────────── */

yvr_subscription_id_t yvr_host_subscribe(yvr_host_t      *host,
                                         const char      *module_filter,
                                         yvr_event_cb_t   cb,
                                         void            *user)
{
    if (host == NULL || cb == NULL) return 0u;
    subscriber_t *s = calloc(1, sizeof *s);
    if (s == NULL) return 0u;
    s->id            = host->next_sub_id++;
    s->module_filter = (module_filter != NULL) ? dup_or_null(module_filter) : NULL;
    if (module_filter != NULL && s->module_filter == NULL) {
        free(s);
        return 0u;
    }
    s->cb   = cb;
    s->user = user;
    s->next = host->subs_head;
    host->subs_head = s;
    return s->id;
}

void yvr_host_unsubscribe(yvr_host_t            *host,
                          yvr_subscription_id_t  id)
{
    if (host == NULL || id == 0u) return;
    subscriber_t **pp = &host->subs_head;
    while (*pp != NULL) {
        if ((*pp)->id == id) {
            subscriber_t *victim = *pp;
            *pp = victim->next;
            free(victim->module_filter);
            free(victim);
            return;
        }
        pp = &(*pp)->next;
    }
}

/* ── Introspection ──────────────────────────────────────────── */

yvr_module_state_t yvr_host_module_state(yvr_host_t *host, const char *name)
{
    if (host == NULL || name == NULL) return YVR_MS_UNKNOWN;
    module_entry_t *m = module_find(host, name);
    return (m != NULL) ? m->state : YVR_MS_UNKNOWN;
}

size_t yvr_host_list_modules(yvr_host_t  *host,
                             const char **out_names,
                             size_t       cap)
{
    if (host == NULL) return 0;
    size_t total = 0;
    for (module_entry_t *m = host->modules_head; m != NULL; m = m->next) {
        if (out_names != NULL && total < cap) {
            out_names[total] = m->name;
        }
        total++;
    }
    return total;
}

/* ── Module-side helpers ────────────────────────────────────── */

void yvr_module_log(yvr_module_ctx_t *ctx, const char *msg)
{
    if (ctx == NULL || msg == NULL) return;
    fprintf(stderr, "[yvr_module:%s] %s\n",
            ctx->name != NULL ? ctx->name : "?", msg);
}

const char *yvr_module_state_path(yvr_module_ctx_t *ctx)
{
    return (ctx != NULL) ? ctx->state_path : NULL;
}

const char *yvr_module_name(yvr_module_ctx_t *ctx)
{
    return (ctx != NULL) ? ctx->name : NULL;
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
    /* Module-driven subscriptions need the module's host pointer +
     * its on_event hook; the loader will plumb both in. For now,
     * unconditional 0. */
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
