/* Yaver c-agent — CBOR codec (RFC 8949 subset).
 *
 * The c-agent wire encodes every frame body in CBOR. This header
 * exposes a deterministic-encoding subset (CTAP2 profile, RFC 8949
 * §4.2.1) that's small enough for an embedded budget but rich
 * enough to encode every Phase-0 frame (HELLO, AUTH, ATTEST,
 * HEARTBEAT, ERROR) and every diagnostic probe payload.
 *
 * What's in:
 *   - unsigned integers (major type 0), shortest encoding always
 *   - negative integers (major type 1)
 *   - byte strings (major type 2), definite length
 *   - text strings (major type 3), definite length, UTF-8 not validated
 *   - arrays (major type 4), definite length
 *   - maps (major type 5), definite length, CALLER orders keys
 *   - booleans (major type 7, simples 20 / 21)
 *   - null (major type 7, simple 22)
 *
 * What's out:
 *   - tagged values (major type 6) — round-trip ignored, not emitted
 *   - indefinite-length items — rejected on read, never emitted
 *   - half / single / double floats — not yet needed; add when a
 *     frame body or tool result actually carries one
 *   - bignums, decimal fractions, big floats
 *
 * Map keys: the caller is responsible for emitting keys in
 * bytewise-lexicographic order of their CBOR-encoded form (CTAP2
 * § 7). Frame-body codecs in the same library do this; tool-result
 * encoders SHOULD do it too so device output is reproducible.
 *
 * No allocation, no I/O, no global state. Both encoder and decoder
 * are stack-allocatable. Buffers are caller-owned and never copied.
 */

#ifndef YVR_CBOR_H
#define YVR_CBOR_H

#include <stdbool.h>
#include <stddef.h>
#include <stdint.h>

#include "status.h"

#ifdef __cplusplus
extern "C" {
#endif

/* ── Encoder ─────────────────────────────────────────────────── */

typedef struct yvr_cbor_w {
    uint8_t      *buf;
    size_t        cap;
    size_t        len;
    /* sticky error: once set, every subsequent write is a no-op
     * that returns the same code. The caller can chain dozens of
     * w_* calls and check status once at the end. */
    yvr_status_t  err;
} yvr_cbor_w_t;

/* Initialize a writer over `buf` with capacity `cap`. */
void yvr_cbor_w_init(yvr_cbor_w_t *w, uint8_t *buf, size_t cap);

/* Bytes encoded so far. Equal to the length you'd send on the
 * wire. Includes header + payload bytes. */
size_t yvr_cbor_w_len(const yvr_cbor_w_t *w);

/* Current sticky error (YVR_OK if no error). */
yvr_status_t yvr_cbor_w_status(const yvr_cbor_w_t *w);

yvr_status_t yvr_cbor_w_uint        (yvr_cbor_w_t *w, uint64_t v);
yvr_status_t yvr_cbor_w_int         (yvr_cbor_w_t *w, int64_t  v);
yvr_status_t yvr_cbor_w_bytes       (yvr_cbor_w_t *w, const uint8_t *p, size_t n);
yvr_status_t yvr_cbor_w_text        (yvr_cbor_w_t *w, const char    *p, size_t n);
yvr_status_t yvr_cbor_w_array_begin (yvr_cbor_w_t *w, size_t n);
yvr_status_t yvr_cbor_w_map_begin   (yvr_cbor_w_t *w, size_t n);
yvr_status_t yvr_cbor_w_bool        (yvr_cbor_w_t *w, bool   v);
yvr_status_t yvr_cbor_w_null        (yvr_cbor_w_t *w);

/* ── Decoder ─────────────────────────────────────────────────── */

/* Public kind values. 0 is reserved so a zero-initialized variable
 * never matches a valid kind. */
typedef enum yvr_cbor_kind {
    YVR_CBOR_KIND_NONE  = 0,
    YVR_CBOR_KIND_UINT  = 1,
    YVR_CBOR_KIND_INT   = 2,    /* negative integer */
    YVR_CBOR_KIND_BYTES = 3,
    YVR_CBOR_KIND_TEXT  = 4,
    YVR_CBOR_KIND_ARRAY = 5,
    YVR_CBOR_KIND_MAP   = 6,
    YVR_CBOR_KIND_BOOL  = 7,
    YVR_CBOR_KIND_NULL  = 8
} yvr_cbor_kind_t;

typedef struct yvr_cbor_r {
    const uint8_t *buf;
    size_t         cap;
    size_t         pos;
    yvr_status_t   err;
} yvr_cbor_r_t;

/* Initialize a reader over `buf` (length `n`). The buffer must
 * outlive every pointer the reader returns through bytes/text. */
void yvr_cbor_r_init(yvr_cbor_r_t *r, const uint8_t *buf, size_t n);

yvr_status_t yvr_cbor_r_status(const yvr_cbor_r_t *r);
size_t       yvr_cbor_r_pos   (const yvr_cbor_r_t *r);

/* Look at the next item's kind without consuming it. Returns
 * YVR_E_TRUNCATED if no bytes remain. */
yvr_status_t yvr_cbor_r_peek(const yvr_cbor_r_t *r, yvr_cbor_kind_t *out);

/* Each consumes one item; fails YVR_E_BAD_FRAME if the actual
 * kind differs from what the call expects. The pointer returned
 * by `bytes` and `text` aliases the input buffer — copy if the
 * reader's input might be mutated or freed. */
yvr_status_t yvr_cbor_r_uint        (yvr_cbor_r_t *r, uint64_t *out);
yvr_status_t yvr_cbor_r_int         (yvr_cbor_r_t *r, int64_t  *out);
yvr_status_t yvr_cbor_r_bytes       (yvr_cbor_r_t *r, const uint8_t **out_p, size_t *out_n);
yvr_status_t yvr_cbor_r_text        (yvr_cbor_r_t *r, const char    **out_p, size_t *out_n);
yvr_status_t yvr_cbor_r_array_begin (yvr_cbor_r_t *r, size_t *out_n);
yvr_status_t yvr_cbor_r_map_begin   (yvr_cbor_r_t *r, size_t *out_n);
yvr_status_t yvr_cbor_r_bool        (yvr_cbor_r_t *r, bool *out);
yvr_status_t yvr_cbor_r_null        (yvr_cbor_r_t *r);

/* Skip exactly one item (recursive for arrays + maps). Used by
 * frame-body decoders to ignore unknown-but-well-formed fields
 * without aborting — important for forward compatibility. */
yvr_status_t yvr_cbor_r_skip(yvr_cbor_r_t *r);

#ifdef __cplusplus
}
#endif

#endif /* YVR_CBOR_H */
