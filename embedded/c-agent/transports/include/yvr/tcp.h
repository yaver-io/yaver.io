/* Yaver c-agent — TCP transport adapter.
 *
 * Outbound-from-device TCP socket framed with the 9-byte
 * HTTP/2-style header from <yvr/frame.h>. Synchronous send/recv
 * — the c-agent process does its own scheduling, so a blocking
 * fd interface fits the runtime cleanly without bringing in an
 * async loop.
 *
 * POSIX socket implementation; covers Linux, OpenWrt, macOS, and
 * BSD targets without modification. Windows + Zephyr land in
 * follow-up adapters.
 *
 * No TLS yet — the v1 demo runs cleartext over the existing
 * Yaver relay, which already provides transport-layer security.
 * mbedTLS plugs in here when we ship `transports/tls.{h,c}`.
 */

#ifndef YVR_TCP_H
#define YVR_TCP_H

#include <stddef.h>
#include <stdint.h>

/* These live in core/include/yvr/ — use the namespaced form so
 * the preprocessor finds them via the include path rather than
 * the local-directory rule. */
#include "yvr/frame.h"
#include "yvr/status.h"

#ifdef __cplusplus
extern "C" {
#endif

typedef struct yvr_tcp yvr_tcp_t;

/* Open a TCP connection to `host:port`. `connect_timeout_ms`
 * caps the initial handshake; 0 means "no timeout". On failure
 * returns NULL and sets the global errno. */
yvr_tcp_t *yvr_tcp_connect(const char *host,
                           uint16_t    port,
                           uint32_t    connect_timeout_ms);

/* Close + free the connection. Safe with NULL. */
void yvr_tcp_close(yvr_tcp_t *conn);

/* Send one framed message: header followed by `payload_len`
 * bytes of payload. The header's `length` field is overwritten
 * to match `payload_len`; callers don't have to keep them in
 * sync. Returns YVR_OK on success. */
yvr_status_t yvr_tcp_send_frame(yvr_tcp_t                *conn,
                                const yvr_frame_header_t *hdr,
                                const uint8_t            *payload,
                                size_t                    payload_len);

/* Receive one framed message into caller-owned buffers.
 * - `*out_hdr` always written on success.
 * - `payload_buf` (capacity `payload_cap`) receives up to
 *   `hdr.length` bytes; the actual byte count is written to
 *   `*out_payload_len`. If `hdr.length > payload_cap`, the
 *   excess is consumed from the socket and the function returns
 *   YVR_E_BUFFER_TOO_SMALL — the caller can resize and reset
 *   the connection if it wants the next frame to land cleanly.
 * - `recv_timeout_ms` is per-call; 0 means "block until a frame
 *   arrives or the socket dies". */
yvr_status_t yvr_tcp_recv_frame(yvr_tcp_t          *conn,
                                yvr_frame_header_t *out_hdr,
                                uint8_t            *payload_buf,
                                size_t              payload_cap,
                                size_t             *out_payload_len,
                                uint32_t            recv_timeout_ms);

/* Underlying file descriptor — exposed so callers can `select`
 * across multiple connections without duplicating the timeout
 * logic. Returns -1 on a closed/invalid handle. */
int yvr_tcp_fd(const yvr_tcp_t *conn);

#ifdef __cplusplus
}
#endif

#endif /* YVR_TCP_H */
