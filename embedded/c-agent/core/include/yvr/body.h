/* Yaver c-agent — Phase-0 frame body codecs.
 *
 * The wire is HTTP/2-style framing (yvr/frame.h) wrapping CBOR-
 * encoded bodies (yvr/cbor.h). This header carries the body
 * schemas the c-agent runtime parses + emits during session
 * setup and steady-state liveness.
 *
 * Rules followed by every body in this header:
 *   1. CBOR map at top level — never bare value.
 *   2. Keys are short text strings, emitted in CTAP2 deterministic
 *      order (RFC 8949 §4.2.1: bytewise-lexicographic over their
 *      CBOR-encoded form).
 *   3. Schema evolution = append-only fields. Older decoders skip
 *      unknown keys (yvr_cbor_r_skip) rather than reject — this is
 *      what lets a brain shipped after a device was last upgraded
 *      send richer payloads without bricking the device.
 *   4. Strings returned by decoders alias the input buffer; copy
 *      before reusing the buffer.
 */

#ifndef YVR_BODY_H
#define YVR_BODY_H

#include <stddef.h>
#include <stdint.h>

#include "status.h"

#ifdef __cplusplus
extern "C" {
#endif

/* Current protocol version. Bumped when the body schemas in this
 * header change in a way that breaks older decoders. The encoder
 * always emits this value; the decoder reads whatever value is in
 * the payload and lets the caller decide whether to refuse. */
#define YVR_PROTOCOL_VERSION 1u

/* ── HELLO ───────────────────────────────────────────────────── */
/* The first frame on every session. Each peer sends its own HELLO;
 * neither side proceeds until both have arrived. Carries:
 *   v             (uint)  protocol version
 *   role          (text)  "brain" | "device"
 *   agent_version (text)  optional — implementation identifier
 *                         (e.g. "yvr-cagent/0.0.1")
 *
 * Keys are emitted in CTAP2 order: "v" (head 0x61) < "role"
 * (head 0x64) < "agent_version" (head 0x6d). */
typedef struct yvr_hello {
    uint32_t       protocol_version;
    const char    *role;
    size_t         role_len;
    const char    *agent_version;       /* may be NULL */
    size_t         agent_version_len;
} yvr_hello_t;

/* Encode `h` into `buf` (capacity `cap`); writes the byte count to
 * `*out_len`. Caller-provided buffer; no allocation. */
yvr_status_t yvr_hello_encode(const yvr_hello_t *h,
                              uint8_t           *buf,
                              size_t             cap,
                              size_t            *out_len);

/* Decode a HELLO body. The strings in `out` alias `buf`; do not
 * use after `buf` is freed. Unknown fields in the payload are
 * skipped silently. */
yvr_status_t yvr_hello_decode(const uint8_t *buf,
                              size_t         n,
                              yvr_hello_t   *out);

/* ── HEARTBEAT ───────────────────────────────────────────────── */
/* Periodic liveness ping. Brain → device carries a signed
 * `signed_now()` so the device can correct its wall clock without
 * trusting an unsigned local RTC. (Signature is added in Phase 0b
 * once the key story lands; for now `signature` may be empty.)
 *
 * Keys (CTAP2 order): "v" < "now_ms" < "signature".
 *
 *   v          (uint)  protocol version
 *   now_ms     (uint)  current time, milliseconds since epoch
 *   signature  (bytes) optional — ECDSA over (v, now_ms)
 */
typedef struct yvr_heartbeat {
    uint32_t       protocol_version;
    uint64_t       now_ms;
    const uint8_t *signature;       /* may be NULL */
    size_t         signature_len;
} yvr_heartbeat_t;

yvr_status_t yvr_heartbeat_encode(const yvr_heartbeat_t *h,
                                  uint8_t               *buf,
                                  size_t                 cap,
                                  size_t                *out_len);

yvr_status_t yvr_heartbeat_decode(const uint8_t   *buf,
                                  size_t           n,
                                  yvr_heartbeat_t *out);

#ifdef __cplusplus
}
#endif

#endif /* YVR_BODY_H */
