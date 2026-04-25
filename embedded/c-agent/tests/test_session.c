/* Yaver c-agent — end-to-end session loop test.
 *
 * Uses socketpair() to create two connected stream sockets in
 * one process. One side runs yvr_session_run (the device).
 * The other side plays the brain — sends HELLO, receives the
 * device's HELLO, sends an INVOKE for a registered native
 * probe, validates the TOOL_RSP, then closes its fd.
 *
 * The device's session_run sees EOF on the closed fd and
 * returns YVR_OK; the test joins the brain thread, asserts
 * its outcome, and exits.
 *
 * No `iw`, no Moonraker, no real network — purely the wire
 * loop running between two threads in one process.
 */

#include "yvr/body.h"
#include "yvr/cbor.h"
#include "yvr/frame.h"
#include "yvr/host.h"
#include "yvr/module.h"
#include "yvr/session.h"
#include "yvr/status.h"
#include "yvr/tcp.h"

#include <pthread.h>
#include <stdbool.h>
#include <stddef.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/socket.h>
#include <unistd.h>

#define EXP_OK(call)        do { if ((call) != YVR_OK) return rc; } while (0)
#define EXPECT(c, code)     do { if (!(c))             return (code); } while (0)

/* ── Echo-reverse native probe (same shape as the host test) ─ */

static void *echo_reverse_invoke(yvr_module_ctx_t *ctx,
                                 const char       *method,
                                 const void       *req,
                                 size_t            req_len,
                                 size_t           *out_len)
{
    (void)ctx;
    (void)method;
    if (req == NULL || req_len == 0) {
        *out_len = 0;
        return NULL;
    }
    uint8_t *rsp = malloc(req_len);
    if (rsp == NULL) {
        *out_len = 0;
        return NULL;
    }
    const uint8_t *src = req;
    for (size_t i = 0; i < req_len; i++) {
        rsp[i] = src[req_len - 1 - i];
    }
    *out_len = req_len;
    return rsp;
}

static yvr_module_vtable_t ECHO_VT = {
    .abi_version = YVR_MODULE_ABI_VERSION,
    .name        = "echo_reverse",
    .version     = "0.0.1",
    .invoke      = echo_reverse_invoke,
};

/* ── Brain side (runs in a thread) ──────────────────────────── */

typedef struct {
    int  fd;
    bool succeeded;
} brain_ctx_t;

/* Fixed-size raw helpers because we can't reuse yvr_tcp here:
 * the brain side wants to drive the wire directly to validate
 * the framing without going through the session loop. */
static int read_n(int fd, uint8_t *p, size_t n)
{
    size_t got = 0;
    while (got < n) {
        ssize_t r = recv(fd, p + got, n - got, 0);
        if (r <= 0) return -1;
        got += (size_t)r;
    }
    return 0;
}

static int write_n(int fd, const uint8_t *p, size_t n)
{
    size_t sent = 0;
    while (sent < n) {
        ssize_t w = send(fd, p + sent, n - sent, 0);
        if (w <= 0) return -1;
        sent += (size_t)w;
    }
    return 0;
}

static int recv_frame(int                  fd,
                      yvr_frame_header_t  *hdr,
                      uint8_t             *payload,
                      size_t               payload_cap,
                      size_t              *payload_len)
{
    uint8_t hb[YVR_FRAME_HEADER_SIZE];
    if (read_n(fd, hb, sizeof hb) != 0) return -1;
    if (yvr_frame_header_decode(hb, sizeof hb, hdr) != YVR_OK) return -1;
    if (hdr->length > payload_cap) return -1;
    if (hdr->length > 0 && read_n(fd, payload, hdr->length) != 0) return -1;
    *payload_len = hdr->length;
    return 0;
}

static int send_frame(int                       fd,
                      const yvr_frame_header_t *hdr_in,
                      const uint8_t            *payload,
                      size_t                    payload_len)
{
    yvr_frame_header_t hdr = *hdr_in;
    hdr.length = (uint32_t)payload_len;
    uint8_t hb[YVR_FRAME_HEADER_SIZE];
    if (yvr_frame_header_encode(&hdr, hb, sizeof hb) != YVR_OK) return -1;
    if (write_n(fd, hb, sizeof hb) != 0) return -1;
    if (payload_len > 0 && write_n(fd, payload, payload_len) != 0) return -1;
    return 0;
}

static void *brain_thread(void *arg)
{
    brain_ctx_t *ctx = arg;
    ctx->succeeded = false;

    yvr_frame_header_t hdr;
    uint8_t            buf[8192];
    size_t             buf_len = 0;

    /* 1. Receive device HELLO. */
    if (recv_frame(ctx->fd, &hdr, buf, sizeof buf, &buf_len) != 0) {
        return NULL;
    }
    if (hdr.type != (uint8_t)YVR_FRAME_HELLO) return NULL;
    yvr_hello_t device_hello;
    if (yvr_hello_decode(buf, buf_len, &device_hello) != YVR_OK) return NULL;
    if (device_hello.role_len != 6 ||
        memcmp(device_hello.role, "device", 6) != 0) {
        return NULL;
    }

    /* 2. Send brain HELLO. */
    yvr_hello_t brain_hello = {
        .protocol_version  = YVR_PROTOCOL_VERSION,
        .role              = "brain",
        .role_len          = 5,
        .agent_version     = "test-brain/0.1",
        .agent_version_len = 14,
    };
    size_t hello_len = 0;
    if (yvr_hello_encode(&brain_hello, buf, sizeof buf, &hello_len) != YVR_OK) {
        return NULL;
    }
    yvr_frame_header_t hello_hdr = { .type = (uint8_t)YVR_FRAME_HELLO };
    if (send_frame(ctx->fd, &hello_hdr, buf, hello_len) != 0) return NULL;

    /* 3. Send INVOKE. The "method" field names the registered
     *    native module (v1 routing is module-name-based). */
    uint8_t  hash[8] = { 0xAB, 0xAB, 0xAB, 0xAB, 0xAB, 0xAB, 0xAB, 0xAB };
    uint8_t  args[5] = { 0x01, 0x02, 0x03, 0x04, 0x05 };
    yvr_invoke_t invoke = {
        .protocol_version = YVR_PROTOCOL_VERSION,
        .tool_hash        = hash,
        .tool_hash_len    = sizeof hash,
        .method           = "echo_reverse",
        .method_len       = 12,
        .args             = args,
        .args_len         = sizeof args,
    };
    size_t invoke_len = 0;
    if (yvr_invoke_encode(&invoke, buf, sizeof buf, &invoke_len) != YVR_OK) {
        return NULL;
    }
    yvr_frame_header_t invoke_hdr = {
        .type      = (uint8_t)YVR_FRAME_INVOKE,
        .stream_id = 1,
    };
    if (send_frame(ctx->fd, &invoke_hdr, buf, invoke_len) != 0) return NULL;

    /* 4. Receive TOOL_RSP and verify the echo-reversed bytes. */
    if (recv_frame(ctx->fd, &hdr, buf, sizeof buf, &buf_len) != 0) {
        return NULL;
    }
    if (hdr.type != (uint8_t)YVR_FRAME_TOOL_RSP) return NULL;
    if (hdr.stream_id != 1) return NULL;

    yvr_tool_rsp_t rsp;
    if (yvr_tool_rsp_decode(buf, buf_len, &rsp) != YVR_OK) return NULL;
    if (rsp.status != 0) return NULL;
    if (rsp.tool_hash_len != sizeof hash ||
        memcmp(rsp.tool_hash, hash, sizeof hash) != 0) {
        return NULL;
    }
    if (rsp.result_len != sizeof args) return NULL;
    /* Result is the args reversed by echo_reverse. */
    static const uint8_t expected[5] = { 0x05, 0x04, 0x03, 0x02, 0x01 };
    if (memcmp(rsp.result, expected, sizeof expected) != 0) return NULL;

    /* 5. Close our fd. The device side will see EOF and the
     *    session_run call will return cleanly. */
    close(ctx->fd);
    ctx->fd = -1;
    ctx->succeeded = true;
    return NULL;
}

/* ── Tests ───────────────────────────────────────────────────── */

static int test_session_end_to_end(void)
{
    int rc = 100;

    int fds[2];
    EXPECT(socketpair(AF_UNIX, SOCK_STREAM, 0, fds) == 0, rc);

    /* Device side. */
    yvr_host_t *host = yvr_host_init("/tmp/yvr-session-test");
    EXPECT(host != NULL, rc + 1);
    EXPECT(yvr_host_register_native(host, "echo_reverse", &ECHO_VT) == YVR_HOST_OK, rc + 2);

    yvr_session_t *sess = yvr_session_wrap_fd(fds[1]);
    EXPECT(sess != NULL, rc + 3);

    /* Brain side, in a thread. */
    brain_ctx_t bctx = { .fd = fds[0], .succeeded = false };
    pthread_t   tid;
    EXPECT(pthread_create(&tid, NULL, brain_thread, &bctx) == 0, rc + 4);

    /* Run the device session. Blocks until brain closes fds[0],
     * then returns YVR_OK on EOF. */
    yvr_status_t run_rc = yvr_session_run(sess, host);
    EXPECT(run_rc == YVR_OK, rc + 5);

    pthread_join(tid, NULL);
    EXPECT(bctx.succeeded, rc + 6);

    yvr_session_close(sess);
    yvr_host_shutdown(host);
    return 0;
}

/* ── Driver ──────────────────────────────────────────────────── */

typedef int (*tfn)(void);
struct tc { const char *name; tfn fn; };

int main(void)
{
    static const struct tc T[] = {
        { "session_end_to_end", test_session_end_to_end },
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
