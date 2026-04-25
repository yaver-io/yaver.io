/* Yaver c-agent — module dependency manifest.
 *
 * The vendor declares (at compile time) the set of modules that
 * make up their app, the dependency edges between them, and per-
 * module policy (state durability, quiesce window, retry budget,
 * resource caps).
 *
 * This struct is the in-memory shape of that declaration. Vendors
 * can build it three ways:
 *
 *   1. Hand-coded in C — straight literals fed to yvr_host_apply_manifest.
 *   2. AI-generated via the build-time `yvr-deps-gen` tool — emits a
 *      YAML manifest from source-tree static analysis, the host
 *      parses it on startup with yvr_manifest_load_yaml().
 *   3. Runtime — yaver itself (driven by the AI brain) ships a new
 *      manifest signed alongside other module artifacts, and the
 *      host re-evaluates the dep graph + does ordered swaps.
 *
 * The vendor never deals with hot-swap orchestration directly —
 * they declare the graph, yaver does the rest.
 */

#ifndef YVR_MANIFEST_H
#define YVR_MANIFEST_H

#include <stddef.h>
#include <stdint.h>

#ifdef __cplusplus
extern "C" {
#endif

/* State policy for a module across reloads. */
typedef enum yvr_state_policy {
    /* Discard module state on every reload (default for stateless
     * modules — telemetry collectors, formatters, etc.). */
    YVR_STATE_NONE         = 0,

    /* Save state via state_save before quiesce, restore via
     * state_load after the new version's on_load. State stays in
     * RAM only; lost on host process restart. */
    YVR_STATE_TRANSIENT    = 1,

    /* Like TRANSIENT but state is also written to per-module
     * state directory (see yvr_module_state_path) so it survives
     * a host process restart. */
    YVR_STATE_PERSISTENT   = 2
} yvr_state_policy_t;

/* Per-module declaration. Names are stable identifiers; the
 * `artifact` is a path or URI the host knows how to load (a local
 * .so for static deployments, a yaver-shipped signed module for
 * AI-driven swaps). */
typedef struct yvr_module_spec {
    const char        *name;            /* required, unique */
    const char        *artifact;        /* required */
    const char       **deps;            /* optional, NULL-terminated array */
    size_t             deps_count;      /* size of deps[] */
    yvr_state_policy_t state_policy;
    uint32_t           quiesce_window_ms;  /* host waits this long for graceful quiesce */
    uint32_t           max_retries;        /* on transient errors */
    uint32_t           invoke_queue_max;   /* invokes queued during quiesce */
    void              *_reserved[4];
} yvr_module_spec_t;

/* Whole-app manifest. */
typedef struct yvr_manifest {
    const char              *vendor;        /* free-form: "myco" */
    const char              *app_name;      /* free-form: "wifi-edge" */
    const char              *version;       /* semver */
    const yvr_module_spec_t *modules;       /* array */
    size_t                   modules_count;
    void                    *_reserved[4];
} yvr_manifest_t;

#ifdef __cplusplus
}
#endif

#endif /* YVR_MANIFEST_H */
