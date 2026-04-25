/* Yaver c-agent — status codes.
 *
 * One stable enum, append-only. Codes <= 0 are errors; YVR_OK = 0.
 * yvr_status_str() returns a short, human-readable label for logs;
 * never NULL.
 */

#ifndef YVR_STATUS_H
#define YVR_STATUS_H

#ifdef __cplusplus
extern "C" {
#endif

typedef enum yvr_status {
    YVR_OK                  =  0,
    YVR_E_INVALID_ARG       = -1,
    YVR_E_BUFFER_TOO_SMALL  = -2,
    YVR_E_PAYLOAD_TOO_LARGE = -3,
    YVR_E_TRUNCATED         = -4,
    YVR_E_BAD_FRAME         = -5,
    YVR_E_INTERNAL          = -6
} yvr_status_t;

/* Returns a short label for `s`. Never NULL. Buffers are static; do
 * not free, do not modify. */
const char *yvr_status_str(yvr_status_t s);

#ifdef __cplusplus
}
#endif

#endif /* YVR_STATUS_H */
