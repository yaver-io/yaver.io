/* Yaver c-agent — device-side session loop implementation.
 *
 * Glues the wire layer (frame + body codecs) to the host runtime
 * (module registry + invoke). Stays small + straight-line: send
 * HELLO, receive HELLO, loop on INVOKE / heartbeat / unknown.
 *
 * Memory: every receive uses one fixed-size scratch buffer
 * shared across iterations. Module results allocated by the
 * host are freed via yvr_host_free_response after we encode
 * the TOOL_RSP, so we never accumulate response state across
 * iterations.
 */

#include "yvr/session.h"

#include "yvr/body.h"
#include "yvr/frame.h"
#include "yvr/host.h"
#include "yvr/tcp.h"

#include <errno.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>

#define SESSION_AGENT_VERSION "yvr-cagent/0.0.1"
#define SESSION_RECV_CAP      (16u * 1024u)
#define SESSION_RSP_CAP       (80u * 1024u)
#define SESSION_RECV_TIMEOUT  0u  /* block until a frame arrives */

struct yvr_session {
    yvr_tcp_t *conn;
};

/* ── Lifecycle ──────────────────────────────────────────────── */

yvr_session_t *yvr_session_open(const char *brain_host,
                                uint16_t    brain_port,
                                uint32_t    connect_timeout_ms)
{
    if (brain_host == NULL || brain_host[0] == '\0' || brain_port == 0) {
        errno = EINVAL;
        return NULL;
    }
    yvr_tcp_t *conn = yvr_tcp_connect(brain_host, brain_port, connect_timeout_ms);
    if (conn == NULL) {
        return NULL;
    }
    yvr_session_t *s = calloc(1, sizeof *s);
    if (s == NULL) {
        int e = errno;
        yvr_tcp_close(conn);
        errno = e;
        return NULL;
    }
    s->conn = conn;
    return s;
}

yvr_session_t *yvr_session_wrap_fd(int fd)
{
    yvr_tcp_t *conn = yvr_tcp_wrap_fd(fd);
    if (conn == NULL) {
        return NULL;
    }
    yvr_session_t *s = calloc(1, sizeof *s);
    if (s == NULL) {
        int e = errno;
        yvr_tcp_close(conn);
        errno = e;
        return NULL;
    }
    s->conn = conn;
    return s;
}

void yvr_session_close(yvr_session_t *s)
{
    if (s == NULL) {
        return;
    }
    if (s->conn != NULL) {
        yvr_tcp_close(s->conn);
    }
    free(s);
}

/* ── Internal helpers ───────────────────────────────────────── */

/* Send a TOOL_RSP carrying the given fields. Allocates an
 * encode buffer on the stack (fast path) — module results
 * larger than SESSION_RSP_CAP yield a truncated TOOL_RSP with
 * status=-1; the brain decides whether to retry with a smaller
 * scope. */
static yvr_status_t send_tool_rsp(yvr_session_t           *s,
                                  uint32_t                 stream_id,
                                  const uint8_t           *tool_hash,
                                  size_t                   tool_hash_len,
                                  int32_t                  status,
                                  const uint8_t           *result,
                                  size_t                   result_len,
                                  const char              *error_msg)
{
    yvr_tool_rsp_t rsp = {
        .protocol_version = YVR_PROTOCOL_VERSION,
        .tool_hash        = tool_hash,
        .tool_hash_len    = tool_hash_len,
        .status           = status,
        .result           = result,
        .result_len       = result_len,
    };
    if (error_msg != NULL) {
        rsp.error     = error_msg;
        rsp.error_len = strlen(error_msg);
    }

    uint8_t buf[SESSION_RSP_CAP];
    size_t  body_len = 0;
    yvr_status_t rc = yvr_tool_rsp_encode(&rsp, buf, sizeof buf, &body_len);
    if (rc != YVR_OK) {
        /* Encode failed (typically buffer too small for an
         * oversized result). Rebuild without the result field
         * + a non-zero status so the brain at least sees a
         * well-formed error response. */
        if (rc == YVR_E_BUFFER_TOO_SMALL) {
            yvr_tool_rsp_t small = {
                .protocol_version = YVR_PROTOCOL_VERSION,
                .tool_hash        = tool_hash,
                .tool_hash_len    = tool_hash_len,
                .status           = -1,
                .error            = "result oversized",
                .error_len        = 16,
            };
            rc = yvr_tool_rsp_encode(&small, buf, sizeof buf, &body_len);
        }
        if (rc != YVR_OK) {
            return rc;
        }
    }

    yvr_frame_header_t hdr = {
        .type      = (uint8_t)YVR_FRAME_TOOL_RSP,
        .stream_id = stream_id,
    };
    return yvr_tcp_send_frame(s->conn, &hdr, buf, body_len);
}

/* Echo a HEARTBEAT back to the peer with the ACK flag set so
 * the brain can prove the device is still draining frames. */
static yvr_status_t echo_heartbeat(yvr_session_t            *s,
                                   const yvr_frame_header_t *in_hdr,
                                   const uint8_t            *payload,
                                   size_t                    payload_len)
{
    yvr_frame_header_t hdr = {
        .type      = (uint8_t)YVR_FRAME_HEARTBEAT,
        .flags     = (uint8_t)YVR_FLAG_ACK,
        .stream_id = in_hdr->stream_id,
    };
    return yvr_tcp_send_frame(s->conn, &hdr, payload, payload_len);
}

/* Handle one INVOKE frame: decode, look up the module by name
 * (INVOKE.method names the module in v1), call host_invoke,
 * encode TOOL_RSP, send. Per-call lifecycle — the response
 * buffer is freed before this returns. */
static yvr_status_t handle_invoke(yvr_session_t            *s,
                                  yvr_host_t               *host,
                                  const yvr_frame_header_t *in_hdr,
                                  const uint8_t            *payload,
                                  size_t                    payload_len)
{
    yvr_invoke_t req;
    yvr_status_t rc = yvr_invoke_decode(payload, payload_len, &req);
    if (rc != YVR_OK) {
        return send_tool_rsp(s, in_hdr->stream_id,
                             NULL, 0, -1, NULL, 0,
                             "malformed INVOKE body");
    }

    /* Method is the module name in v1. NUL-terminate it for the
     * host's char-pointer API. */
    char method_buf[128];
    if (req.method_len >= sizeof method_buf) {
        return send_tool_rsp(s, in_hdr->stream_id,
                             req.tool_hash, req.tool_hash_len,
                             -1, NULL, 0,
                             "method name too long");
    }
    memcpy(method_buf, req.method, req.method_len);
    method_buf[req.method_len] = '\0';

    size_t              rsp_len    = 0;
    yvr_host_status_t   rsp_status = YVR_HOST_OK;
    void *rsp = yvr_host_invoke(host, method_buf, "",
                                req.args, req.args_len,
                                &rsp_len, &rsp_status);

    int32_t       wire_status = 0;
    const char   *err_msg     = NULL;
    if (rsp_status != YVR_HOST_OK) {
        wire_status = (int32_t)rsp_status;
        switch (rsp_status) {
        case YVR_HOST_E_NOT_FOUND: err_msg = "module not found";  break;
        case YVR_HOST_E_NOT_READY: err_msg = "module not ready";  break;
        case YVR_HOST_E_INVALID_ARG: err_msg = "invalid argument"; break;
        default: err_msg = "host invoke failed"; break;
        }
    }

    yvr_status_t send_rc = send_tool_rsp(s, in_hdr->stream_id,
                                         req.tool_hash, req.tool_hash_len,
                                         wire_status,
                                         (const uint8_t *)rsp, rsp_len,
                                         err_msg);

    if (rsp != NULL) {
        yvr_host_free_response(host, rsp);
    }
    return send_rc;
}

/* ── HELLO exchange ─────────────────────────────────────────── */

static yvr_status_t exchange_hello(yvr_session_t *s)
{
    /* Send our HELLO. */
    yvr_hello_t mine = {
        .protocol_version  = YVR_PROTOCOL_VERSION,
        .role              = "device",
        .role_len          = 6,
        .agent_version     = SESSION_AGENT_VERSION,
        .agent_version_len = sizeof(SESSION_AGENT_VERSION) - 1,
    };
    uint8_t buf[256];
    size_t  body_len = 0;
    yvr_status_t rc = yvr_hello_encode(&mine, buf, sizeof buf, &body_len);
    if (rc != YVR_OK) {
        return rc;
    }
    yvr_frame_header_t hdr = { .type = (uint8_t)YVR_FRAME_HELLO };
    rc = yvr_tcp_send_frame(s->conn, &hdr, buf, body_len);
    if (rc != YVR_OK) {
        return rc;
    }

    /* Read peer's HELLO. */
    uint8_t  recv_buf[1024];
    size_t   recv_len = 0;
    yvr_frame_header_t in_hdr;
    rc = yvr_tcp_recv_frame(s->conn, &in_hdr, recv_buf, sizeof recv_buf,
                            &recv_len, SESSION_RECV_TIMEOUT);
    if (rc != YVR_OK) {
        return rc;
    }
    if (in_hdr.type != (uint8_t)YVR_FRAME_HELLO) {
        return YVR_E_BAD_FRAME;
    }
    yvr_hello_t peer;
    rc = yvr_hello_decode(recv_buf, recv_len, &peer);
    if (rc != YVR_OK) {
        return rc;
    }
    if (peer.role_len != 5 || memcmp(peer.role, "brain", 5) != 0) {
        return YVR_E_BAD_FRAME;
    }
    return YVR_OK;
}

/* ── Main run loop ──────────────────────────────────────────── */

yvr_status_t yvr_session_run(yvr_session_t *s, yvr_host_t *host)
{
    if (s == NULL || s->conn == NULL || host == NULL) {
        return YVR_E_INVALID_ARG;
    }

    yvr_status_t rc = exchange_hello(s);
    if (rc != YVR_OK) {
        return rc;
    }

    /* Single recv buffer reused per iteration. Frames larger than
     * SESSION_RECV_CAP are drained + dropped (BUFFER_TOO_SMALL on
     * the codec) — same behaviour as the C TCP transport's
     * oversized-payload handling. */
    uint8_t recv_buf[SESSION_RECV_CAP];

    for (;;) {
        yvr_frame_header_t hdr;
        size_t             recv_len = 0;
        rc = yvr_tcp_recv_frame(s->conn, &hdr, recv_buf, sizeof recv_buf,
                                &recv_len, SESSION_RECV_TIMEOUT);
        if (rc == YVR_E_TRUNCATED) {
            /* Peer closed cleanly. Treat as clean end-of-session. */
            return YVR_OK;
        }
        if (rc != YVR_OK) {
            return rc;
        }

        switch ((yvr_frame_type_t)hdr.type) {
        case YVR_FRAME_INVOKE:
            rc = handle_invoke(s, host, &hdr, recv_buf, recv_len);
            if (rc != YVR_OK) {
                return rc;
            }
            break;

        case YVR_FRAME_HEARTBEAT:
            rc = echo_heartbeat(s, &hdr, recv_buf, recv_len);
            if (rc != YVR_OK) {
                return rc;
            }
            break;

        case YVR_FRAME_HELLO:
            /* Duplicate HELLO after the initial exchange is a
             * protocol violation. End the session. */
            return YVR_E_BAD_FRAME;

        default:
            /* Unknown / unsupported type — drop quietly. The
             * brain may ship frames we don't yet handle (KILL,
             * MODULE) once those features land; ignoring them
             * for v1 is forward-compatible. */
            break;
        }
    }
}
