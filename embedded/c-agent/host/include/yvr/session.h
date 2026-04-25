/* Yaver c-agent — device-side session loop.
 *
 * The session is what makes the c-agent process actually do
 * something on the wire. It glues together:
 *   - yvr_tcp (or any framed transport) for the wire
 *   - yvr_hello + yvr_tool_rsp + ... for body codecs
 *   - yvr_host for module dispatch
 *
 * One call drives the whole protocol from the device side: send
 * HELLO, receive brain HELLO, then loop on INVOKE → host dispatch
 * → TOOL_RSP until the connection closes or an error occurs.
 *
 * Out-of-band frames (HEARTBEAT, EVENT, ERROR) are handled in
 * the loop: HEARTBEATs get an ACK echo; everything else is
 * logged + dropped for v1.
 *
 * v1 routing is simple: INVOKE.method names the registered
 * native module to dispatch to. The tool_hash is opaque (the
 * device echoes it back in TOOL_RSP for caller correlation).
 * Future versions will use tool_hash as the canonical module
 * identifier once the signed-module loader is in.
 */

#ifndef YVR_SESSION_H
#define YVR_SESSION_H

#include <stdint.h>

#include "host.h"
#include "yvr/status.h"

#ifdef __cplusplus
extern "C" {
#endif

typedef struct yvr_session yvr_session_t;

/* Connect to a brain at `host:port` and return an unrun session.
 * Caller invokes yvr_session_run(s, host) to drive the protocol;
 * yvr_session_close(s) shuts it down. NULL on connect failure
 * (errno set). */
yvr_session_t *yvr_session_open(const char *brain_host,
                                uint16_t    brain_port,
                                uint32_t    connect_timeout_ms);

/* Wrap an already-connected fd as a session. Useful for
 * socketpair() tests, inetd-style spawn, or transports that
 * yield an fd through some other path. The session owns the fd
 * after this call — close() runs when yvr_session_close is
 * called. */
yvr_session_t *yvr_session_wrap_fd(int fd);

/* Run the session against `host`. Sends HELLO, expects HELLO
 * back, then loops on INVOKE → dispatch → TOOL_RSP until the
 * connection ends or an error occurs.
 *
 * Returns:
 *   YVR_OK             — peer closed cleanly after HELLO
 *   YVR_E_BAD_FRAME    — peer sent a malformed body / wrong type
 *   YVR_E_TRUNCATED    — connection died mid-frame
 *   YVR_E_INTERNAL     — host_invoke / encoder failure
 *
 * Blocking; runs forever until peer closes or error. The caller
 * typically spawns this in its own thread. */
yvr_status_t yvr_session_run(yvr_session_t *s, yvr_host_t *host);

/* Tear down the session — closes the underlying connection,
 * frees session state. Idempotent; safe with NULL. */
void yvr_session_close(yvr_session_t *s);

#ifdef __cplusplus
}
#endif

#endif /* YVR_SESSION_H */
