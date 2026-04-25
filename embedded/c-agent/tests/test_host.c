/* Yaver c-agent — host runtime tests.
 *
 * Covers the real (non-stub) parts of host.c: subscribe / emit /
 * unsubscribe with name-filter, manifest registration, pause /
 * resume / quiesce state transitions + their corresponding events,
 * and the introspection accessors.
 */

#include "yvr/event.h"
#include "yvr/host.h"
#include "yvr/manifest.h"

#include <stdbool.h>
#include <stddef.h>
#include <stdint.h>
#include <stdio.h>
#include <string.h>

#define EXPECT(cond, code) do { if (!(cond)) return (code); } while (0)

/* ── Fixture ─────────────────────────────────────────────────── */

typedef struct {
    yvr_event_kind_t kind;
    char             module_name[64];
} captured_evt_t;

#define CAP_MAX 64

typedef struct {
    captured_evt_t evts[CAP_MAX];
    size_t         count;
} capture_t;

static void on_event(const yvr_event_t *evt, void *user)
{
    capture_t *c = user;
    if (c->count >= CAP_MAX) return;
    c->evts[c->count].kind = evt->kind;
    if (evt->module_name != NULL) {
        size_t n = strlen(evt->module_name);
        if (n >= sizeof c->evts[0].module_name) n = sizeof c->evts[0].module_name - 1;
        memcpy(c->evts[c->count].module_name, evt->module_name, n);
        c->evts[c->count].module_name[n] = '\0';
    } else {
        c->evts[c->count].module_name[0] = '\0';
    }
    c->count++;
}

static int contains_event(const capture_t *c, yvr_event_kind_t k, const char *name)
{
    for (size_t i = 0; i < c->count; i++) {
        if (c->evts[i].kind == k && strcmp(c->evts[i].module_name, name) == 0) {
            return 1;
        }
    }
    return 0;
}

static yvr_manifest_t make_manifest(const yvr_module_spec_t *m, size_t n)
{
    return (yvr_manifest_t){
        .vendor       = "test",
        .app_name     = "host-test",
        .version      = "0.0.1",
        .modules      = m,
        .modules_count= n,
    };
}

/* ── Tests ───────────────────────────────────────────────────── */

static int test_init_shutdown(void)
{
    int rc = 100;
    yvr_host_t *h = yvr_host_init("/tmp/yvr-host-test");
    EXPECT(h != NULL, rc);
    yvr_host_shutdown(h);
    /* shutdown of NULL is a no-op. */
    yvr_host_shutdown(NULL);
    return 0;
}

static int test_apply_manifest_fires_loaded(void)
{
    int rc = 200;
    yvr_host_t *h = yvr_host_init("/tmp/yvr-host-test");
    EXPECT(h != NULL, rc);

    capture_t cap = {0};
    yvr_subscription_id_t sid = yvr_host_subscribe(h, NULL, on_event, &cap);
    EXPECT(sid != 0, rc + 1);

    static const yvr_module_spec_t mods[] = {
        { .name = "alpha",   .artifact = "/dev/null" },
        { .name = "beta",    .artifact = "/dev/null" },
        { .name = "gamma",   .artifact = "/dev/null" },
    };
    yvr_manifest_t mf = make_manifest(mods, 3);
    EXPECT(yvr_host_apply_manifest(h, &mf) == YVR_HOST_OK, rc + 2);

    EXPECT(contains_event(&cap, YVR_EVT_MODULE_LOADED, "alpha"), rc + 3);
    EXPECT(contains_event(&cap, YVR_EVT_MODULE_LOADED, "beta"),  rc + 4);
    EXPECT(contains_event(&cap, YVR_EVT_MODULE_LOADED, "gamma"), rc + 5);
    EXPECT(cap.count == 3, rc + 6);

    yvr_host_shutdown(h);
    return 0;
}

static int test_module_state_after_apply(void)
{
    int rc = 300;
    yvr_host_t *h = yvr_host_init("/tmp/yvr-host-test");
    static const yvr_module_spec_t mods[] = {
        { .name = "alpha", .artifact = "/dev/null" },
    };
    yvr_manifest_t mf = make_manifest(mods, 1);
    EXPECT(yvr_host_apply_manifest(h, &mf) == YVR_HOST_OK, rc);
    EXPECT(yvr_host_module_state(h, "alpha")  == YVR_MS_ACTIVE,  rc + 1);
    EXPECT(yvr_host_module_state(h, "absent") == YVR_MS_UNKNOWN, rc + 2);
    yvr_host_shutdown(h);
    return 0;
}

static int test_pause_resume_quiesce(void)
{
    int rc = 400;
    yvr_host_t *h = yvr_host_init("/tmp/yvr-host-test");
    static const yvr_module_spec_t mods[] = {
        { .name = "alpha", .artifact = "/dev/null" },
    };
    yvr_manifest_t mf = make_manifest(mods, 1);
    yvr_host_apply_manifest(h, &mf);

    capture_t cap = {0};
    yvr_host_subscribe(h, NULL, on_event, &cap);

    EXPECT(yvr_host_pause_module(h, "alpha")  == YVR_HOST_OK, rc);
    EXPECT(yvr_host_module_state(h, "alpha")  == YVR_MS_PAUSED, rc + 1);
    EXPECT(contains_event(&cap, YVR_EVT_MODULE_PAUSED, "alpha"), rc + 2);

    EXPECT(yvr_host_resume_module(h, "alpha") == YVR_HOST_OK, rc + 3);
    EXPECT(yvr_host_module_state(h, "alpha")  == YVR_MS_ACTIVE, rc + 4);
    EXPECT(contains_event(&cap, YVR_EVT_MODULE_RESUMED, "alpha"), rc + 5);

    EXPECT(yvr_host_quiesce_module(h, "alpha") == YVR_HOST_OK, rc + 6);
    EXPECT(yvr_host_module_state(h, "alpha")   == YVR_MS_QUIESCED, rc + 7);
    EXPECT(contains_event(&cap, YVR_EVT_MODULE_QUIESCED, "alpha"), rc + 8);

    /* Resume from QUIESCED is allowed. */
    EXPECT(yvr_host_resume_module(h, "alpha") == YVR_HOST_OK, rc + 9);
    EXPECT(yvr_host_module_state(h, "alpha")  == YVR_MS_ACTIVE, rc + 10);

    /* Pause / resume of unknown module returns NOT_FOUND. */
    EXPECT(yvr_host_pause_module(h, "ghost")  == YVR_HOST_E_NOT_FOUND, rc + 11);
    EXPECT(yvr_host_resume_module(h, "ghost") == YVR_HOST_E_NOT_FOUND, rc + 12);

    yvr_host_shutdown(h);
    return 0;
}

static int test_subscribe_filter(void)
{
    int rc = 500;
    yvr_host_t *h = yvr_host_init("/tmp/yvr-host-test");

    /* Subscriber A: filter "alpha" only. Subscriber B: all. */
    capture_t cap_a = {0};
    capture_t cap_b = {0};
    yvr_host_subscribe(h, "alpha", on_event, &cap_a);
    yvr_host_subscribe(h, NULL,    on_event, &cap_b);

    static const yvr_module_spec_t mods[] = {
        { .name = "alpha", .artifact = "/dev/null" },
        { .name = "beta",  .artifact = "/dev/null" },
    };
    yvr_manifest_t mf = make_manifest(mods, 2);
    yvr_host_apply_manifest(h, &mf);

    /* A saw only the alpha LOADED. B saw both. */
    EXPECT(cap_a.count == 1, rc);
    EXPECT(strcmp(cap_a.evts[0].module_name, "alpha") == 0, rc + 1);
    EXPECT(cap_b.count == 2, rc + 2);

    /* Pausing beta hits B but not A (filter). */
    yvr_host_pause_module(h, "beta");
    EXPECT(cap_a.count == 1, rc + 3);
    EXPECT(cap_b.count == 3, rc + 4);
    EXPECT(cap_b.evts[2].kind == YVR_EVT_MODULE_PAUSED, rc + 5);

    yvr_host_shutdown(h);
    return 0;
}

static int test_unsubscribe(void)
{
    int rc = 600;
    yvr_host_t *h = yvr_host_init("/tmp/yvr-host-test");
    capture_t cap = {0};
    yvr_subscription_id_t sid = yvr_host_subscribe(h, NULL, on_event, &cap);
    EXPECT(sid != 0, rc);

    static const yvr_module_spec_t mods[] = {
        { .name = "alpha", .artifact = "/dev/null" },
    };
    yvr_manifest_t mf = make_manifest(mods, 1);
    yvr_host_apply_manifest(h, &mf);
    EXPECT(cap.count == 1, rc + 1);

    yvr_host_unsubscribe(h, sid);
    yvr_host_pause_module(h, "alpha");
    /* No new callback after unsubscribe. */
    EXPECT(cap.count == 1, rc + 2);

    /* Idempotent: unsubscribe of unknown id is a no-op. */
    yvr_host_unsubscribe(h, 9999);
    yvr_host_unsubscribe(h, sid);

    yvr_host_shutdown(h);
    return 0;
}

static int test_list_modules(void)
{
    int rc = 700;
    yvr_host_t *h = yvr_host_init("/tmp/yvr-host-test");
    static const yvr_module_spec_t mods[] = {
        { .name = "alpha", .artifact = "/dev/null" },
        { .name = "beta",  .artifact = "/dev/null" },
        { .name = "gamma", .artifact = "/dev/null" },
    };
    yvr_manifest_t mf = make_manifest(mods, 3);
    yvr_host_apply_manifest(h, &mf);

    /* Count without buffer. */
    EXPECT(yvr_host_list_modules(h, NULL, 0) == 3, rc);

    /* Fetch with a small buffer. */
    const char *names[2] = {0};
    size_t total = yvr_host_list_modules(h, names, 2);
    EXPECT(total == 3, rc + 1);
    EXPECT(strcmp(names[0], "alpha") == 0, rc + 2);
    EXPECT(strcmp(names[1], "beta")  == 0, rc + 3);

    yvr_host_shutdown(h);
    return 0;
}

static int test_invoke_returns_typed_errors(void)
{
    int rc = 800;
    yvr_host_t *h = yvr_host_init("/tmp/yvr-host-test");

    yvr_host_status_t code = YVR_HOST_OK;
    size_t out_len = 0;
    void *resp = yvr_host_invoke(h, "absent", "method", NULL, 0, &out_len, &code);
    EXPECT(resp == NULL, rc);
    EXPECT(code == YVR_HOST_E_NOT_FOUND, rc + 1);

    /* Known module → NOT_READY (no real loader yet). */
    static const yvr_module_spec_t mods[] = {
        { .name = "alpha", .artifact = "/dev/null" },
    };
    yvr_manifest_t mf = make_manifest(mods, 1);
    yvr_host_apply_manifest(h, &mf);

    code = YVR_HOST_OK;
    resp = yvr_host_invoke(h, "alpha", "method", NULL, 0, &out_len, &code);
    EXPECT(resp == NULL, rc + 2);
    EXPECT(code == YVR_HOST_E_NOT_READY, rc + 3);

    yvr_host_shutdown(h);
    return 0;
}

/* ── Driver ──────────────────────────────────────────────────── */

typedef int (*tfn)(void);
struct tc { const char *name; tfn fn; };

int main(void)
{
    static const struct tc T[] = {
        { "init_shutdown",                test_init_shutdown                },
        { "apply_manifest_fires_loaded",  test_apply_manifest_fires_loaded  },
        { "module_state_after_apply",     test_module_state_after_apply     },
        { "pause_resume_quiesce",         test_pause_resume_quiesce         },
        { "subscribe_filter",             test_subscribe_filter             },
        { "unsubscribe",                  test_unsubscribe                  },
        { "list_modules",                 test_list_modules                 },
        { "invoke_returns_typed_errors",  test_invoke_returns_typed_errors  },
    };
    const size_t n = sizeof T / sizeof T[0];
    int failed = 0;
    for (size_t i = 0; i < n; i++) {
        int rc = T[i].fn();
        if (rc == 0) {
            printf("PASS  %s\n", T[i].name);
        } else {
            printf("FAIL  %s (rc=%d)\n", T[i].name, rc);
            failed++;
        }
    }
    if (failed != 0) {
        printf("\n%d/%zu test(s) failed\n", failed, n);
        return 1;
    }
    printf("\n%zu/%zu tests passed.\n", n, n);
    return 0;
}
