/* POSIX TCP transport for Yaver c-agent.
 *
 * Synchronous send/recv with per-call timeouts. The receive path
 * reads the 9-byte header first, then the payload — incomplete
 * partial reads are looped until the requested byte count is
 * satisfied or the deadline elapses.
 *
 * The transport is deliberately stateless beyond the socket
 * fd: no per-connection buffering, no scheduling, no
 * multiplexing. Higher layers (the framed-RPC dispatcher, the
 * tunnel client) own those concerns.
 */

#include "yvr/tcp.h"

#include <errno.h>
#include <fcntl.h>
#include <netdb.h>
#include <netinet/in.h>
#include <netinet/tcp.h>
#include <poll.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/socket.h>
#include <sys/time.h>
#include <unistd.h>

struct yvr_tcp {
    int fd;
};

/* ── Helpers ─────────────────────────────────────────────────── */

static int set_nonblock(int fd, int on)
{
    int flags = fcntl(fd, F_GETFL, 0);
    if (flags < 0) return -1;
    if (on) flags |=  O_NONBLOCK;
    else    flags &= ~O_NONBLOCK;
    return fcntl(fd, F_SETFL, flags);
}

static int wait_io(int fd, short events, uint32_t timeout_ms)
{
    /* timeout_ms == 0 means "block forever" in our API; map to
     * poll's -1 sentinel. */
    int t = (timeout_ms == 0) ? -1 : (int)timeout_ms;
    struct pollfd p = { .fd = fd, .events = events };
    for (;;) {
        int rc = poll(&p, 1, t);
        if (rc < 0 && errno == EINTR) continue;
        return rc;
    }
}

/* Read exactly `n` bytes into `buf`, looping over partial reads.
 * Returns YVR_OK on success, YVR_E_TRUNCATED on EOF before n,
 * YVR_E_INTERNAL on socket error or timeout. */
static yvr_status_t read_full(int fd, uint8_t *buf, size_t n, uint32_t timeout_ms)
{
    size_t got = 0;
    while (got < n) {
        int io = wait_io(fd, POLLIN, timeout_ms);
        if (io <= 0) return YVR_E_INTERNAL;
        ssize_t r = recv(fd, buf + got, n - got, 0);
        if (r > 0)              { got += (size_t)r; continue; }
        if (r == 0)             { return YVR_E_TRUNCATED; }
        if (errno == EINTR)     { continue; }
        if (errno == EAGAIN || errno == EWOULDBLOCK) { continue; }
        return YVR_E_INTERNAL;
    }
    return YVR_OK;
}

static yvr_status_t write_full(int fd, const uint8_t *buf, size_t n, uint32_t timeout_ms)
{
    size_t sent = 0;
    while (sent < n) {
        int io = wait_io(fd, POLLOUT, timeout_ms);
        if (io <= 0) return YVR_E_INTERNAL;
        ssize_t w = send(fd, buf + sent, n - sent,
#ifdef MSG_NOSIGNAL
                         MSG_NOSIGNAL
#else
                         0
#endif
        );
        if (w > 0)              { sent += (size_t)w; continue; }
        if (errno == EINTR)     { continue; }
        if (errno == EAGAIN || errno == EWOULDBLOCK) { continue; }
        return YVR_E_INTERNAL;
    }
    return YVR_OK;
}

static yvr_status_t discard_bytes(int fd, size_t n, uint32_t timeout_ms)
{
    uint8_t scratch[1024];
    while (n > 0) {
        size_t chunk = (n > sizeof scratch) ? sizeof scratch : n;
        yvr_status_t s = read_full(fd, scratch, chunk, timeout_ms);
        if (s != YVR_OK) return s;
        n -= chunk;
    }
    return YVR_OK;
}

/* ── Public API ─────────────────────────────────────────────── */

yvr_tcp_t *yvr_tcp_connect(const char *host,
                           uint16_t    port,
                           uint32_t    connect_timeout_ms)
{
    if (host == NULL || port == 0) {
        errno = EINVAL;
        return NULL;
    }

    char port_str[8];
    snprintf(port_str, sizeof port_str, "%u", (unsigned)port);

    struct addrinfo hints = {
        .ai_family   = AF_UNSPEC,
        .ai_socktype = SOCK_STREAM,
    };
    struct addrinfo *res = NULL;
    int gai = getaddrinfo(host, port_str, &hints, &res);
    if (gai != 0 || res == NULL) {
        errno = EHOSTUNREACH;
        return NULL;
    }

    int fd = -1;
    int saved_errno = ECONNREFUSED;
    for (struct addrinfo *p = res; p != NULL; p = p->ai_next) {
        fd = socket(p->ai_family, p->ai_socktype, p->ai_protocol);
        if (fd < 0) continue;
        if (set_nonblock(fd, 1) < 0) { close(fd); fd = -1; continue; }

        int rc = connect(fd, p->ai_addr, p->ai_addrlen);
        if (rc == 0) {
            /* Already connected — rare on a fresh socket. */
            break;
        }
        if (errno != EINPROGRESS) {
            saved_errno = errno;
            close(fd);
            fd = -1;
            continue;
        }
        int io = wait_io(fd, POLLOUT, connect_timeout_ms);
        if (io <= 0) {
            saved_errno = (io == 0) ? ETIMEDOUT : errno;
            close(fd);
            fd = -1;
            continue;
        }
        int sockerr = 0;
        socklen_t slen = sizeof sockerr;
        if (getsockopt(fd, SOL_SOCKET, SO_ERROR, &sockerr, &slen) < 0 ||
            sockerr != 0) {
            saved_errno = (sockerr != 0) ? sockerr : errno;
            close(fd);
            fd = -1;
            continue;
        }
        break;
    }
    freeaddrinfo(res);
    if (fd < 0) {
        errno = saved_errno;
        return NULL;
    }

    /* Restore blocking mode + reasonable defaults. We use poll()
     * around the syscalls anyway, so blocking sockets are fine. */
    if (set_nonblock(fd, 0) < 0) {
        int e = errno;
        close(fd);
        errno = e;
        return NULL;
    }
    int one = 1;
    (void)setsockopt(fd, IPPROTO_TCP, TCP_NODELAY, &one, sizeof one);
#ifdef SO_NOSIGPIPE
    (void)setsockopt(fd, SOL_SOCKET, SO_NOSIGPIPE, &one, sizeof one);
#endif

    yvr_tcp_t *c = calloc(1, sizeof *c);
    if (c == NULL) {
        int e = errno;
        close(fd);
        errno = e;
        return NULL;
    }
    c->fd = fd;
    return c;
}

void yvr_tcp_close(yvr_tcp_t *conn)
{
    if (conn == NULL) return;
    if (conn->fd >= 0) close(conn->fd);
    free(conn);
}

int yvr_tcp_fd(const yvr_tcp_t *conn)
{
    return (conn != NULL) ? conn->fd : -1;
}

yvr_status_t yvr_tcp_send_frame(yvr_tcp_t                *conn,
                                const yvr_frame_header_t *hdr,
                                const uint8_t            *payload,
                                size_t                    payload_len)
{
    if (conn == NULL || hdr == NULL) return YVR_E_INVALID_ARG;
    if (payload == NULL && payload_len != 0) return YVR_E_INVALID_ARG;
    if (payload_len > YVR_FRAME_MAX_PAYLOAD) return YVR_E_PAYLOAD_TOO_LARGE;

    /* Sync the header's length to the actual payload size so
     * callers don't have to think about it. */
    yvr_frame_header_t fixed = *hdr;
    fixed.length = (uint32_t)payload_len;

    uint8_t hb[YVR_FRAME_HEADER_SIZE];
    yvr_status_t s = yvr_frame_header_encode(&fixed, hb, sizeof hb);
    if (s != YVR_OK) return s;

    s = write_full(conn->fd, hb, sizeof hb, 5000);
    if (s != YVR_OK) return s;
    if (payload_len > 0) {
        s = write_full(conn->fd, payload, payload_len, 5000);
    }
    return s;
}

yvr_status_t yvr_tcp_recv_frame(yvr_tcp_t          *conn,
                                yvr_frame_header_t *out_hdr,
                                uint8_t            *payload_buf,
                                size_t              payload_cap,
                                size_t             *out_payload_len,
                                uint32_t            recv_timeout_ms)
{
    if (conn == NULL || out_hdr == NULL || out_payload_len == NULL) {
        return YVR_E_INVALID_ARG;
    }
    if (payload_buf == NULL && payload_cap != 0) {
        return YVR_E_INVALID_ARG;
    }

    uint8_t hb[YVR_FRAME_HEADER_SIZE];
    yvr_status_t s = read_full(conn->fd, hb, sizeof hb, recv_timeout_ms);
    if (s != YVR_OK) return s;
    s = yvr_frame_header_decode(hb, sizeof hb, out_hdr);
    if (s != YVR_OK) return s;

    size_t want = (size_t)out_hdr->length;
    if (want > payload_cap) {
        /* Read what fits; drain the rest so the next frame lands
         * cleanly. Caller still gets the buffer-too-small signal. */
        if (payload_cap > 0) {
            s = read_full(conn->fd, payload_buf, payload_cap, recv_timeout_ms);
            if (s != YVR_OK) return s;
        }
        s = discard_bytes(conn->fd, want - payload_cap, recv_timeout_ms);
        *out_payload_len = payload_cap;
        return (s == YVR_OK) ? YVR_E_BUFFER_TOO_SMALL : s;
    }

    if (want > 0) {
        s = read_full(conn->fd, payload_buf, want, recv_timeout_ms);
        if (s != YVR_OK) return s;
    }
    *out_payload_len = want;
    return YVR_OK;
}
