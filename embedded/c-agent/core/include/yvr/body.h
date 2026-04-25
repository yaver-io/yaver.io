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

#include <stdbool.h>
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

/* ── AUTH (brain → device) ──────────────────────────────────── */
/* Challenge frame: brain hands the device a nonce + signed wall
 * clock. Device replies with AUTHRSP carrying its certificate
 * chain and an ECDSA signature over (nonce || signed_now_ms).
 *
 * Keys (CTAP2 order): "v" < "nonce" < "signed_now_ms".
 *
 *   v               (uint)   protocol version
 *   nonce           (bytes)  challenge bytes (≥16, typically 32)
 *   signed_now_ms   (uint)   brain's wall clock; the device adopts
 *                            this as its monotonic-anchored time
 *                            since field RTCs are unreliable
 */
typedef struct yvr_auth {
    uint32_t       protocol_version;
    const uint8_t *nonce;
    size_t         nonce_len;
    uint64_t       signed_now_ms;
} yvr_auth_t;

yvr_status_t yvr_auth_encode(const yvr_auth_t *a,
                             uint8_t          *buf,
                             size_t            cap,
                             size_t           *out_len);

yvr_status_t yvr_auth_decode(const uint8_t *buf,
                             size_t         n,
                             yvr_auth_t    *out);

/* ── AUTHRSP (device → brain) ───────────────────────────────── */
/* Device's response to AUTH. Carries the device's pinned cert
 * chain (DER-encoded), the same nonce echoed back, and an ECDSA
 * P-256 signature over the canonical bytes (nonce || signed_now_ms).
 *
 * Keys (CTAP2 order): "v" < "sig" < "nonce" < "device_cert".
 *
 *   v             (uint)
 *   sig           (bytes)   64-byte ECDSA P-256 signature
 *   nonce         (bytes)   echoed challenge
 *   device_cert   (bytes)   DER-encoded cert chain
 */
typedef struct yvr_authrsp {
    uint32_t       protocol_version;
    const uint8_t *sig;
    size_t         sig_len;
    const uint8_t *nonce;
    size_t         nonce_len;
    const uint8_t *device_cert;
    size_t         device_cert_len;
} yvr_authrsp_t;

yvr_status_t yvr_authrsp_encode(const yvr_authrsp_t *r,
                                uint8_t             *buf,
                                size_t               cap,
                                size_t              *out_len);

yvr_status_t yvr_authrsp_decode(const uint8_t *buf,
                                size_t         n,
                                yvr_authrsp_t *out);

/* ── ATTEST (device → brain) ────────────────────────────────── */
/* Platform attestation: device's arch + libc + kernel + capability
 * allowlist + module-cache budget. Sent once per session right
 * after AUTH so the brain knows what kind of modules it can ship
 * and how much room it has on disk for them.
 *
 * Keys (CTAP2 order):
 *   "v" (0x61) < "arch" (0x64-61) < "libc" (0x64-6c) <
 *   "kernel" (0x66) < "capabilities" (0x6c) <
 *   "ebpf_supported" (0x6e) < "cache_quota_bytes" (0x71)
 *
 *   v                  (uint)
 *   arch               (text)   "aarch64" | "armv7" | "x86_64" | ...
 *   libc               (text)   "musl-1.2" | "glibc-2.36" | ...
 *   kernel             (text)   "5.15.149"
 *   capabilities       (text[]) declared host imports (allowlist)
 *   ebpf_supported     (bool)   Layer-2 availability
 *   cache_quota_bytes  (uint)   module-cache budget on this device
 */
typedef struct yvr_attest {
    uint32_t            protocol_version;
    const char         *arch;
    size_t              arch_len;
    const char         *libc;
    size_t              libc_len;
    const char         *kernel;
    size_t              kernel_len;
    const char *const  *capabilities;        /* array of NUL-terminated strings */
    size_t              capabilities_count;
    bool                ebpf_supported;
    uint64_t            cache_quota_bytes;
} yvr_attest_t;

yvr_status_t yvr_attest_encode(const yvr_attest_t *a,
                               uint8_t            *buf,
                               size_t              cap,
                               size_t             *out_len);

/* yvr_attest_decode reads capabilities into an out-parameter
 * array. The array is caller-owned (size = capabilities_cap); it
 * is filled with pointers into `buf` and the count is written to
 * *out_capabilities_count. If the manifest carries more entries
 * than the array can hold, the decoder writes the first
 * `capabilities_cap` and returns YVR_E_BUFFER_TOO_SMALL with the
 * full count still set in *out_capabilities_count so the caller
 * can resize and retry. Pass NULL + 0 to get just the count. */
yvr_status_t yvr_attest_decode(const uint8_t  *buf,
                               size_t          n,
                               yvr_attest_t   *out,
                               const char    **out_capabilities,
                               size_t         *out_capabilities_lens,
                               size_t          capabilities_cap);

/* ── ERROR ──────────────────────────────────────────────────── */
/* Structured failure carried on any frame type. Keys (CTAP2):
 *   "v" (0x61) < "code" (0x64) < "context" (0x67-63) <
 *   "message" (0x67-6d) < "stream_id" (0x69)
 *
 *   v          (uint)
 *   code       (int)    negative = error, mirrors yvr_status_t
 *   context    (text)   optional vendor-defined detail
 *   message    (text)   human-readable, ASCII or UTF-8
 *   stream_id  (uint)   optional, the stream the error pertains to;
 *                       0 = connection-scoped error
 */
typedef struct yvr_error {
    uint32_t       protocol_version;
    int32_t        code;
    const char    *context;
    size_t         context_len;
    const char    *message;
    size_t         message_len;
    uint32_t       stream_id;
} yvr_error_t;

yvr_status_t yvr_error_encode(const yvr_error_t *e,
                              uint8_t           *buf,
                              size_t             cap,
                              size_t            *out_len);

yvr_status_t yvr_error_decode(const uint8_t *buf,
                              size_t         n,
                              yvr_error_t   *out);

#ifdef __cplusplus
}
#endif

#endif /* YVR_BODY_H */
