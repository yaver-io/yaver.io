/* Yaver c-agent — TCP transport loopback tests.
 *
 * Spins a tiny server thread on 127.0.0.1, runs the c-agent's
 * TCP client against it, exercises frame send + recv +
 * truncation handling. POSIX-only test (skipped at CMake level
 * if the platform lacks BSD sockets).
 */

#include "yvr/frame.h"
#include "yvr/status.h"
#include "yvr/tcp.h"

#include <netinet/in.h>
#include <pthread.h>
#include <stdbool.h>
#include <stddef.h>
#include <stdint.h>
#include <stdio.h>
#include <string.h>
#include <sys/socket.h>
#include <unistd.h>

#define EXP_OK(call) do { if ((call) != YVR_OK) return rc; } while (0)
#define EXPECT(c, code) do { if (!(c)) return (code); } while (0)

/* ── Test server thread ─────────────────────────────────────── */

typedef struct {
    int     listen_fd;
    uint16_t port;
    /* What to do when a client connects — set per test. */
    enum {
        SERVER_ECHO_FIRST_FRAME,
        SERVER_SEND_OVERSIZED_FRAME,
    } behavior;
    /* Output: did we successfully serve one client? */
    bool served;
} server_ctx_t;

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

static void *server_run(void *arg)
{
    server_ctx_t *ctx = arg;
    int cfd = accept(ctx->listen_fd, NULL, NULL);
    if (cfd < 0) return NULL;

    if (ctx->behavior == SERVER_ECHO_FIRST_FRAME) {
        uint8_t hb[YVR_FRAME_HEADER_SIZE];
        if (read_n(cfd, hb, sizeof hb) == 0) {
            yvr_frame_header_t in;
            if (yvr_frame_header_decode(hb, sizeof hb, &in) == YVR_OK) {
                /* Drain the payload so we read the whole frame. */
                uint8_t payload[256] = {0};
                size_t want = (in.length < sizeof payload) ? in.length : sizeof payload;
                if (in.length > 0 && read_n(cfd, payload, want) != 0) {
                    /* fall through to close */
                }
                /* Echo back: same payload, type + 1, ACK flag set. */
                yvr_frame_header_t out = in;
                out.type  = (uint8_t)(in.type + 1);
                out.flags = (uint8_t)(in.flags | YVR_FLAG_ACK);
                out.length = (uint32_t)want;
                uint8_t ob[YVR_FRAME_HEADER_SIZE];
                if (yvr_frame_header_encode(&out, ob, sizeof ob) == YVR_OK) {
                    if (write_n(cfd, ob, sizeof ob) == 0 &&
                        (want == 0 || write_n(cfd, payload, want) == 0)) {
                        ctx->served = true;
                    }
                }
            }
        }
    } else if (ctx->behavior == SERVER_SEND_OVERSIZED_FRAME) {
        /* Send a frame whose declared length is larger than the
         * client's buffer. Lets us verify that the client drains
         * the excess and still returns BUFFER_TOO_SMALL cleanly. */
        const size_t payload_len = 200;
        uint8_t payload[200];
        for (size_t i = 0; i < payload_len; i++) payload[i] = (uint8_t)(i & 0xFF);

        yvr_frame_header_t hdr = {
            .length    = payload_len,
            .type      = (uint8_t)YVR_FRAME_HEARTBEAT,
            .flags     = 0,
            .stream_id = 1,
        };
        uint8_t hb[YVR_FRAME_HEADER_SIZE];
        if (yvr_frame_header_encode(&hdr, hb, sizeof hb) == YVR_OK &&
            write_n(cfd, hb, sizeof hb) == 0 &&
            write_n(cfd, payload, payload_len) == 0) {
            ctx->served = true;
        }
    }

    close(cfd);
    return NULL;
}

static int start_server(server_ctx_t *ctx)
{
    int fd = socket(AF_INET, SOCK_STREAM, 0);
    if (fd < 0) return -1;
    int one = 1;
    setsockopt(fd, SOL_SOCKET, SO_REUSEADDR, &one, sizeof one);

    struct sockaddr_in sin = {0};
    sin.sin_family      = AF_INET;
    sin.sin_addr.s_addr = htonl(INADDR_LOOPBACK);
    sin.sin_port        = 0;
    if (bind(fd, (struct sockaddr *)&sin, sizeof sin) != 0) { close(fd); return -1; }
    if (listen(fd, 1) != 0)                                 { close(fd); return -1; }

    socklen_t slen = sizeof sin;
    if (getsockname(fd, (struct sockaddr *)&sin, &slen) != 0) { close(fd); return -1; }
    ctx->listen_fd = fd;
    ctx->port      = ntohs(sin.sin_port);
    return 0;
}

/* ── Tests ───────────────────────────────────────────────────── */

static int test_connect_send_recv(void)
{
    int rc = 100;
    server_ctx_t ctx = { .behavior = SERVER_ECHO_FIRST_FRAME };
    EXPECT(start_server(&ctx) == 0, rc);

    pthread_t tid;
    EXPECT(pthread_create(&tid, NULL, server_run, &ctx) == 0, rc + 1);

    yvr_tcp_t *conn = yvr_tcp_connect("127.0.0.1", ctx.port, 1000);
    EXPECT(conn != NULL, rc + 2);

    yvr_frame_header_t out_hdr = {
        .length    = 4,
        .type      = (uint8_t)YVR_FRAME_HELLO,
        .flags     = 0,
        .stream_id = 7,
    };
    const uint8_t out_payload[4] = { 0x11, 0x22, 0x33, 0x44 };
    EXP_OK(yvr_tcp_send_frame(conn, &out_hdr, out_payload, sizeof out_payload));

    yvr_frame_header_t in_hdr;
    uint8_t in_payload[16] = {0};
    size_t  in_len = 0;
    EXP_OK(yvr_tcp_recv_frame(conn, &in_hdr, in_payload, sizeof in_payload, &in_len, 1000));

    EXPECT(in_hdr.type      == (uint8_t)(YVR_FRAME_HELLO + 1), rc + 3);
    EXPECT(in_hdr.flags     == YVR_FLAG_ACK,                   rc + 4);
    EXPECT(in_hdr.stream_id == 7,                              rc + 5);
    EXPECT(in_len == sizeof out_payload,                       rc + 6);
    EXPECT(memcmp(in_payload, out_payload, in_len) == 0,       rc + 7);

    yvr_tcp_close(conn);
    pthread_join(tid, NULL);
    close(ctx.listen_fd);
    EXPECT(ctx.served, rc + 8);
    return 0;
}

static int test_connect_refused(void)
{
    int rc = 200;
    /* Pick a port unlikely to have anyone listening. We don't
     * truly know it's free, but 1 is reserved (tcpmux) and
     * almost never bound on a Mac/Linux dev box, so connect
     * fails fast. */
    yvr_tcp_t *conn = yvr_tcp_connect("127.0.0.1", 1, 500);
    EXPECT(conn == NULL, rc);
    return 0;
}

static int test_recv_buffer_too_small(void)
{
    int rc = 300;
    server_ctx_t ctx = { .behavior = SERVER_SEND_OVERSIZED_FRAME };
    EXPECT(start_server(&ctx) == 0, rc);

    pthread_t tid;
    EXPECT(pthread_create(&tid, NULL, server_run, &ctx) == 0, rc + 1);

    yvr_tcp_t *conn = yvr_tcp_connect("127.0.0.1", ctx.port, 1000);
    EXPECT(conn != NULL, rc + 2);

    yvr_frame_header_t hdr;
    uint8_t small_buf[32];
    size_t  in_len = 0;
    yvr_status_t s = yvr_tcp_recv_frame(conn, &hdr, small_buf, sizeof small_buf,
                                         &in_len, 1000);
    EXPECT(s == YVR_E_BUFFER_TOO_SMALL, rc + 3);
    EXPECT(hdr.length == 200,           rc + 4);
    EXPECT(in_len == sizeof small_buf,  rc + 5);

    yvr_tcp_close(conn);
    pthread_join(tid, NULL);
    close(ctx.listen_fd);
    return 0;
}

/* ── Driver ──────────────────────────────────────────────────── */

typedef int (*tfn)(void);
struct tc { const char *name; tfn fn; };

int main(void)
{
    static const struct tc T[] = {
        { "connect_send_recv",       test_connect_send_recv      },
        { "connect_refused",         test_connect_refused        },
        { "recv_buffer_too_small",   test_recv_buffer_too_small  },
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
