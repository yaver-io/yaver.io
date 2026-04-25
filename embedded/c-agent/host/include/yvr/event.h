/* Yaver c-agent — host event bus.
 *
 * Yaver runs an in-process event bus that broadcasts module
 * lifecycle changes to anyone who subscribes. Other modules
 * subscribe through <yvr/module.h> (their on_event hook); the
 * vendor's host code subscribes through <yvr/host.h>.
 *
 * The bus is the "told what happened" abstraction: when yaver does
 * anything to a module — about to replace it, replaced it, hit an
 * error and is retrying, paused it for diagnostics — every
 * subscriber learns about it without polling. Dependents use this
 * to gracefully degrade (route traffic elsewhere, hold requests in
 * a queue, surface a "wifi temporarily idle" state to the UI)
 * during the brief window where the module is unavailable.
 *
 * Events are delivered synchronously, in the order they occurred,
 * on the host's event-dispatch thread. Subscribers must not block;
 * if they need to do real work, copy what they need and dispatch
 * to their own queue.
 */

#ifndef YVR_EVENT_H
#define YVR_EVENT_H

#include <stddef.h>
#include <stdint.h>

#ifdef __cplusplus
extern "C" {
#endif

/* Event kinds. Numerically stable; new kinds append. */
typedef enum yvr_event_kind {
    /* First-time load completed. Subscribers can begin invoking. */
    YVR_EVT_MODULE_LOADED         = 1,

    /* Yaver is about to replace the module. Dependents should
     * stop initiating new invokes and prepare to be paused. The
     * module itself receives this through on_quiesce, not here. */
    YVR_EVT_MODULE_REPLACING      = 2,

    /* Replacement succeeded. New version is live. */
    YVR_EVT_MODULE_REPLACED       = 3,

    /* Replacement failed. Old version was restored — subscribers
     * can resume calling immediately. error_code / error_message
     * carry the failure reason. */
    YVR_EVT_MODULE_REPLACE_FAILED = 4,

    /* Module entered idle state via host pause/quiesce. invokes
     * issued during this window are queued by the host (or
     * rejected with NOT_READY if they exceed the queue cap). */
    YVR_EVT_MODULE_QUIESCED       = 5,

    /* Module is back to active. Queued invokes are draining. */
    YVR_EVT_MODULE_RESUMED        = 6,

    /* Host externally paused the module (operator action, dep
     * walk during another swap). */
    YVR_EVT_MODULE_PAUSED         = 7,

    /* Module returned an error from invoke or a lifecycle
     * callback. Recoverable failures fire with the next RETRYING
     * event; fatal failures are followed by UNLOADED. */
    YVR_EVT_MODULE_ERROR          = 8,

    /* Host is retrying after a transient error. attempt counts
     * up from 1; max_attempts is the configured ceiling. */
    YVR_EVT_MODULE_RETRYING       = 9,

    /* Module is being torn down. on_unload is in flight. */
    YVR_EVT_MODULE_UNLOADING      = 10,

    /* Module is gone. No further events with this name until a
     * fresh load. */
    YVR_EVT_MODULE_UNLOADED       = 11
} yvr_event_kind_t;

/* Event payload. All fields are owned by the host during the
 * callback; subscribers must copy any they want to retain. */
typedef struct yvr_event {
    yvr_event_kind_t kind;
    const char      *module_name;       /* always set */
    const char      *from_version;      /* set on REPLACING / REPLACED */
    const char      *to_version;        /* set on REPLACING / REPLACED */
    int32_t          error_code;        /* set on ERROR / REPLACE_FAILED */
    const char      *error_message;     /* set on ERROR / REPLACE_FAILED, may be NULL */
    uint32_t         attempt;           /* set on RETRYING (1-based) */
    uint32_t         max_attempts;      /* set on RETRYING */
    uint64_t         timestamp_ms;      /* host monotonic clock at emission */
    void            *_reserved[4];      /* layout-stable padding */
} yvr_event_t;

/* Subscriber callback signature — same shape from host or module
 * entry points. `user` is the pointer the subscriber registered. */
typedef void (*yvr_event_cb_t)(const yvr_event_t *event, void *user);

/* Subscription identifier returned by subscribe APIs. Opaque, but
 * stable for the lifetime of the host. 0 is reserved for "no
 * subscription" so callers can zero-initialize fields. */
typedef uint32_t yvr_subscription_id_t;

#ifdef __cplusplus
}
#endif

#endif /* YVR_EVENT_H */
