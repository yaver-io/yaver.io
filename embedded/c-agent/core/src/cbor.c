#include "yvr/cbor.h"

#include <string.h>

/* CBOR major types live in the top three bits of the head byte.
 * Additional info lives in the bottom five. */
#define MT_UINT   0u
#define MT_NEGINT 1u
#define MT_BYTES  2u
#define MT_TEXT   3u
#define MT_ARRAY  4u
#define MT_MAP    5u
#define MT_TAG    6u
#define MT_SIMPLE 7u

/* Simple values for major type 7. */
#define SIMPLE_FALSE 20u
#define SIMPLE_TRUE  21u
#define SIMPLE_NULL  22u

/* Initial-byte sentinel for the four "additional follows" widths. */
#define INFO_INLINE_MAX 23u
#define INFO_U8         24u
#define INFO_U16        25u
#define INFO_U32        26u
#define INFO_U64        27u

/* ── Writer internals ────────────────────────────────────────── */

static inline yvr_status_t w_set_err(yvr_cbor_w_t *w, yvr_status_t e)
{
    if (w->err == YVR_OK) {
        w->err = e;
    }
    return w->err;
}

static yvr_status_t w_put(yvr_cbor_w_t *w, const uint8_t *src, size_t n)
{
    if (w->err != YVR_OK) {
        return w->err;
    }
    if (n == 0) {
        return YVR_OK;
    }
    /* size_t overflow guard for paranoid hosts. */
    if (w->len + n < w->len || w->len + n > w->cap) {
        return w_set_err(w, YVR_E_BUFFER_TOO_SMALL);
    }
    memcpy(w->buf + w->len, src, n);
    w->len += n;
    return YVR_OK;
}

static yvr_status_t w_put_byte(yvr_cbor_w_t *w, uint8_t b)
{
    return w_put(w, &b, 1);
}

/* Emit a CBOR head: major type in `mt` (0..7), unsigned `v` carried
 * in the shortest legal encoding. */
static yvr_status_t w_head(yvr_cbor_w_t *w, uint8_t mt, uint64_t v)
{
    if (w->err != YVR_OK) {
        return w->err;
    }
    const uint8_t mt_top = (uint8_t)((mt & 0x07u) << 5);

    if (v <= INFO_INLINE_MAX) {
        return w_put_byte(w, (uint8_t)(mt_top | (uint8_t)v));
    }
    if (v <= 0xFFu) {
        uint8_t buf[2] = { (uint8_t)(mt_top | INFO_U8), (uint8_t)v };
        return w_put(w, buf, sizeof(buf));
    }
    if (v <= 0xFFFFu) {
        uint8_t buf[3] = {
            (uint8_t)(mt_top | INFO_U16),
            (uint8_t)((v >> 8) & 0xFFu),
            (uint8_t)((v >> 0) & 0xFFu),
        };
        return w_put(w, buf, sizeof(buf));
    }
    if (v <= 0xFFFFFFFFu) {
        uint8_t buf[5] = {
            (uint8_t)(mt_top | INFO_U32),
            (uint8_t)((v >> 24) & 0xFFu),
            (uint8_t)((v >> 16) & 0xFFu),
            (uint8_t)((v >>  8) & 0xFFu),
            (uint8_t)((v >>  0) & 0xFFu),
        };
        return w_put(w, buf, sizeof(buf));
    }
    {
        uint8_t buf[9] = {
            (uint8_t)(mt_top | INFO_U64),
            (uint8_t)((v >> 56) & 0xFFu),
            (uint8_t)((v >> 48) & 0xFFu),
            (uint8_t)((v >> 40) & 0xFFu),
            (uint8_t)((v >> 32) & 0xFFu),
            (uint8_t)((v >> 24) & 0xFFu),
            (uint8_t)((v >> 16) & 0xFFu),
            (uint8_t)((v >>  8) & 0xFFu),
            (uint8_t)((v >>  0) & 0xFFu),
        };
        return w_put(w, buf, sizeof(buf));
    }
}

/* ── Writer public API ───────────────────────────────────────── */

void yvr_cbor_w_init(yvr_cbor_w_t *w, uint8_t *buf, size_t cap)
{
    if (w == NULL) {
        return;
    }
    w->buf = buf;
    w->cap = (buf != NULL) ? cap : 0;
    w->len = 0;
    w->err = (buf == NULL && cap > 0) ? YVR_E_INVALID_ARG : YVR_OK;
}

size_t yvr_cbor_w_len(const yvr_cbor_w_t *w)
{
    return (w != NULL) ? w->len : 0;
}

yvr_status_t yvr_cbor_w_status(const yvr_cbor_w_t *w)
{
    return (w != NULL) ? w->err : YVR_E_INVALID_ARG;
}

yvr_status_t yvr_cbor_w_uint(yvr_cbor_w_t *w, uint64_t v)
{
    if (w == NULL) {
        return YVR_E_INVALID_ARG;
    }
    return w_head(w, MT_UINT, v);
}

yvr_status_t yvr_cbor_w_int(yvr_cbor_w_t *w, int64_t v)
{
    if (w == NULL) {
        return YVR_E_INVALID_ARG;
    }
    if (v >= 0) {
        return w_head(w, MT_UINT, (uint64_t)v);
    }
    /* Negative ints encode -(v+1) as unsigned with major type 1.
     * INT64_MIN: -(INT64_MIN+1) = INT64_MAX, fits in uint64_t. */
    uint64_t u = (uint64_t)(-(v + 1));
    return w_head(w, MT_NEGINT, u);
}

yvr_status_t yvr_cbor_w_bytes(yvr_cbor_w_t *w, const uint8_t *p, size_t n)
{
    if (w == NULL) {
        return YVR_E_INVALID_ARG;
    }
    if (p == NULL && n != 0) {
        return w_set_err(w, YVR_E_INVALID_ARG);
    }
    yvr_status_t s = w_head(w, MT_BYTES, (uint64_t)n);
    if (s != YVR_OK) {
        return s;
    }
    return w_put(w, p, n);
}

yvr_status_t yvr_cbor_w_text(yvr_cbor_w_t *w, const char *p, size_t n)
{
    if (w == NULL) {
        return YVR_E_INVALID_ARG;
    }
    if (p == NULL && n != 0) {
        return w_set_err(w, YVR_E_INVALID_ARG);
    }
    yvr_status_t s = w_head(w, MT_TEXT, (uint64_t)n);
    if (s != YVR_OK) {
        return s;
    }
    return w_put(w, (const uint8_t *)p, n);
}

yvr_status_t yvr_cbor_w_array_begin(yvr_cbor_w_t *w, size_t n)
{
    if (w == NULL) {
        return YVR_E_INVALID_ARG;
    }
    return w_head(w, MT_ARRAY, (uint64_t)n);
}

yvr_status_t yvr_cbor_w_map_begin(yvr_cbor_w_t *w, size_t n)
{
    if (w == NULL) {
        return YVR_E_INVALID_ARG;
    }
    return w_head(w, MT_MAP, (uint64_t)n);
}

yvr_status_t yvr_cbor_w_bool(yvr_cbor_w_t *w, bool v)
{
    if (w == NULL) {
        return YVR_E_INVALID_ARG;
    }
    /* Major 7 + simple value 20/21 = 0xF4/0xF5. */
    return w_put_byte(w, v ? 0xF5u : 0xF4u);
}

yvr_status_t yvr_cbor_w_null(yvr_cbor_w_t *w)
{
    if (w == NULL) {
        return YVR_E_INVALID_ARG;
    }
    return w_put_byte(w, 0xF6u);
}

/* ── Reader internals ────────────────────────────────────────── */

static inline yvr_status_t r_set_err(yvr_cbor_r_t *r, yvr_status_t e)
{
    if (r->err == YVR_OK) {
        r->err = e;
    }
    return r->err;
}

static yvr_status_t r_take(yvr_cbor_r_t *r, const uint8_t **out, size_t n)
{
    if (r->err != YVR_OK) {
        return r->err;
    }
    if (r->pos + n < r->pos || r->pos + n > r->cap) {
        return r_set_err(r, YVR_E_TRUNCATED);
    }
    *out = r->buf + r->pos;
    r->pos += n;
    return YVR_OK;
}

static yvr_status_t r_take_byte(yvr_cbor_r_t *r, uint8_t *out)
{
    const uint8_t *p;
    yvr_status_t s = r_take(r, &p, 1);
    if (s != YVR_OK) {
        return s;
    }
    *out = *p;
    return YVR_OK;
}

/* Read one CBOR head; returns major type in *mt and the encoded
 * unsigned value in *v. Rejects indefinite-length and reserved
 * info widths. */
static yvr_status_t r_head(yvr_cbor_r_t *r, uint8_t *mt, uint64_t *v)
{
    uint8_t b;
    yvr_status_t s = r_take_byte(r, &b);
    if (s != YVR_OK) {
        return s;
    }
    *mt = (uint8_t)((b >> 5) & 0x07u);
    uint8_t info = (uint8_t)(b & 0x1Fu);

    if (info <= INFO_INLINE_MAX) {
        *v = (uint64_t)info;
        return YVR_OK;
    }
    if (info == INFO_U8) {
        uint8_t b2;
        s = r_take_byte(r, &b2);
        if (s != YVR_OK) {
            return s;
        }
        *v = (uint64_t)b2;
        return YVR_OK;
    }
    if (info == INFO_U16) {
        const uint8_t *p;
        s = r_take(r, &p, 2);
        if (s != YVR_OK) {
            return s;
        }
        *v = ((uint64_t)p[0] << 8) | (uint64_t)p[1];
        return YVR_OK;
    }
    if (info == INFO_U32) {
        const uint8_t *p;
        s = r_take(r, &p, 4);
        if (s != YVR_OK) {
            return s;
        }
        *v = ((uint64_t)p[0] << 24) | ((uint64_t)p[1] << 16) |
             ((uint64_t)p[2] <<  8) | (uint64_t)p[3];
        return YVR_OK;
    }
    if (info == INFO_U64) {
        const uint8_t *p;
        s = r_take(r, &p, 8);
        if (s != YVR_OK) {
            return s;
        }
        *v = ((uint64_t)p[0] << 56) | ((uint64_t)p[1] << 48) |
             ((uint64_t)p[2] << 40) | ((uint64_t)p[3] << 32) |
             ((uint64_t)p[4] << 24) | ((uint64_t)p[5] << 16) |
             ((uint64_t)p[6] <<  8) | (uint64_t)p[7];
        return YVR_OK;
    }
    /* info 28..30 reserved, 31 indefinite — both rejected. */
    return r_set_err(r, YVR_E_BAD_FRAME);
}

/* ── Reader public API ───────────────────────────────────────── */

void yvr_cbor_r_init(yvr_cbor_r_t *r, const uint8_t *buf, size_t n)
{
    if (r == NULL) {
        return;
    }
    r->buf = buf;
    r->cap = (buf != NULL) ? n : 0;
    r->pos = 0;
    r->err = (buf == NULL && n > 0) ? YVR_E_INVALID_ARG : YVR_OK;
}

yvr_status_t yvr_cbor_r_status(const yvr_cbor_r_t *r)
{
    return (r != NULL) ? r->err : YVR_E_INVALID_ARG;
}

size_t yvr_cbor_r_pos(const yvr_cbor_r_t *r)
{
    return (r != NULL) ? r->pos : 0;
}

yvr_status_t yvr_cbor_r_peek(const yvr_cbor_r_t *r, yvr_cbor_kind_t *out)
{
    if (r == NULL || out == NULL) {
        return YVR_E_INVALID_ARG;
    }
    if (r->err != YVR_OK) {
        return r->err;
    }
    if (r->pos >= r->cap) {
        return YVR_E_TRUNCATED;
    }

    uint8_t b = r->buf[r->pos];
    uint8_t mt = (uint8_t)((b >> 5) & 0x07u);
    if (mt == MT_SIMPLE) {
        if (b == 0xF4u || b == 0xF5u) {
            *out = YVR_CBOR_KIND_BOOL;
            return YVR_OK;
        }
        if (b == 0xF6u) {
            *out = YVR_CBOR_KIND_NULL;
            return YVR_OK;
        }
        return YVR_E_BAD_FRAME;
    }
    if (mt == MT_TAG) {
        /* Tagged values are not in our supported subset. */
        return YVR_E_BAD_FRAME;
    }
    switch (mt) {
    case MT_UINT:   *out = YVR_CBOR_KIND_UINT;  break;
    case MT_NEGINT: *out = YVR_CBOR_KIND_INT;   break;
    case MT_BYTES:  *out = YVR_CBOR_KIND_BYTES; break;
    case MT_TEXT:   *out = YVR_CBOR_KIND_TEXT;  break;
    case MT_ARRAY:  *out = YVR_CBOR_KIND_ARRAY; break;
    case MT_MAP:    *out = YVR_CBOR_KIND_MAP;   break;
    default:        return YVR_E_BAD_FRAME;
    }
    return YVR_OK;
}

yvr_status_t yvr_cbor_r_uint(yvr_cbor_r_t *r, uint64_t *out)
{
    if (r == NULL || out == NULL) {
        return YVR_E_INVALID_ARG;
    }
    uint8_t mt;
    uint64_t v;
    yvr_status_t s = r_head(r, &mt, &v);
    if (s != YVR_OK) {
        return s;
    }
    if (mt != MT_UINT) {
        return r_set_err(r, YVR_E_BAD_FRAME);
    }
    *out = v;
    return YVR_OK;
}

yvr_status_t yvr_cbor_r_int(yvr_cbor_r_t *r, int64_t *out)
{
    if (r == NULL || out == NULL) {
        return YVR_E_INVALID_ARG;
    }
    uint8_t mt;
    uint64_t v;
    yvr_status_t s = r_head(r, &mt, &v);
    if (s != YVR_OK) {
        return s;
    }
    if (mt == MT_UINT) {
        if (v > (uint64_t)INT64_MAX) {
            return r_set_err(r, YVR_E_BAD_FRAME);
        }
        *out = (int64_t)v;
        return YVR_OK;
    }
    if (mt == MT_NEGINT) {
        /* Encoded value n means -(n+1). n must fit in int64_t to
         * avoid two's-complement overflow. */
        if (v > (uint64_t)INT64_MAX) {
            return r_set_err(r, YVR_E_BAD_FRAME);
        }
        *out = -1 - (int64_t)v;
        return YVR_OK;
    }
    return r_set_err(r, YVR_E_BAD_FRAME);
}

static yvr_status_t r_string(yvr_cbor_r_t      *r,
                             uint8_t            expected_mt,
                             const uint8_t    **out_p,
                             size_t            *out_n)
{
    uint8_t mt;
    uint64_t v;
    yvr_status_t s = r_head(r, &mt, &v);
    if (s != YVR_OK) {
        return s;
    }
    if (mt != expected_mt) {
        return r_set_err(r, YVR_E_BAD_FRAME);
    }
    if (v > (uint64_t)SIZE_MAX) {
        return r_set_err(r, YVR_E_BAD_FRAME);
    }
    return r_take(r, out_p, (size_t)v) == YVR_OK
        ? (*out_n = (size_t)v, YVR_OK)
        : r->err;
}

yvr_status_t yvr_cbor_r_bytes(yvr_cbor_r_t       *r,
                              const uint8_t     **out_p,
                              size_t             *out_n)
{
    if (r == NULL || out_p == NULL || out_n == NULL) {
        return YVR_E_INVALID_ARG;
    }
    return r_string(r, MT_BYTES, out_p, out_n);
}

yvr_status_t yvr_cbor_r_text(yvr_cbor_r_t      *r,
                             const char       **out_p,
                             size_t            *out_n)
{
    if (r == NULL || out_p == NULL || out_n == NULL) {
        return YVR_E_INVALID_ARG;
    }
    const uint8_t *bp = NULL;
    yvr_status_t s = r_string(r, MT_TEXT, &bp, out_n);
    if (s != YVR_OK) {
        return s;
    }
    /* Aliasing rule (C11 §6.5/7) permits char-pointer access to
     * any object's bytes; this cast is safe. */
    *out_p = (const char *)bp;
    return YVR_OK;
}

static yvr_status_t r_count(yvr_cbor_r_t *r, uint8_t expected_mt, size_t *out_n)
{
    uint8_t mt;
    uint64_t v;
    yvr_status_t s = r_head(r, &mt, &v);
    if (s != YVR_OK) {
        return s;
    }
    if (mt != expected_mt) {
        return r_set_err(r, YVR_E_BAD_FRAME);
    }
    if (v > (uint64_t)SIZE_MAX) {
        return r_set_err(r, YVR_E_BAD_FRAME);
    }
    *out_n = (size_t)v;
    return YVR_OK;
}

yvr_status_t yvr_cbor_r_array_begin(yvr_cbor_r_t *r, size_t *out_n)
{
    if (r == NULL || out_n == NULL) {
        return YVR_E_INVALID_ARG;
    }
    return r_count(r, MT_ARRAY, out_n);
}

yvr_status_t yvr_cbor_r_map_begin(yvr_cbor_r_t *r, size_t *out_n)
{
    if (r == NULL || out_n == NULL) {
        return YVR_E_INVALID_ARG;
    }
    return r_count(r, MT_MAP, out_n);
}

yvr_status_t yvr_cbor_r_bool(yvr_cbor_r_t *r, bool *out)
{
    if (r == NULL || out == NULL) {
        return YVR_E_INVALID_ARG;
    }
    uint8_t b;
    yvr_status_t s = r_take_byte(r, &b);
    if (s != YVR_OK) {
        return s;
    }
    if (b == 0xF4u) {
        *out = false;
        return YVR_OK;
    }
    if (b == 0xF5u) {
        *out = true;
        return YVR_OK;
    }
    return r_set_err(r, YVR_E_BAD_FRAME);
}

yvr_status_t yvr_cbor_r_null(yvr_cbor_r_t *r)
{
    if (r == NULL) {
        return YVR_E_INVALID_ARG;
    }
    uint8_t b;
    yvr_status_t s = r_take_byte(r, &b);
    if (s != YVR_OK) {
        return s;
    }
    if (b != 0xF6u) {
        return r_set_err(r, YVR_E_BAD_FRAME);
    }
    return YVR_OK;
}

yvr_status_t yvr_cbor_r_skip(yvr_cbor_r_t *r)
{
    if (r == NULL) {
        return YVR_E_INVALID_ARG;
    }
    yvr_cbor_kind_t k;
    yvr_status_t s = yvr_cbor_r_peek(r, &k);
    if (s != YVR_OK) {
        return s;
    }
    switch (k) {
    case YVR_CBOR_KIND_UINT: {
        uint64_t v;
        return yvr_cbor_r_uint(r, &v);
    }
    case YVR_CBOR_KIND_INT: {
        int64_t v;
        return yvr_cbor_r_int(r, &v);
    }
    case YVR_CBOR_KIND_BYTES: {
        const uint8_t *p;
        size_t n;
        return yvr_cbor_r_bytes(r, &p, &n);
    }
    case YVR_CBOR_KIND_TEXT: {
        const char *p;
        size_t n;
        return yvr_cbor_r_text(r, &p, &n);
    }
    case YVR_CBOR_KIND_BOOL: {
        bool v;
        return yvr_cbor_r_bool(r, &v);
    }
    case YVR_CBOR_KIND_NULL:
        return yvr_cbor_r_null(r);
    case YVR_CBOR_KIND_ARRAY: {
        size_t n;
        s = yvr_cbor_r_array_begin(r, &n);
        if (s != YVR_OK) {
            return s;
        }
        for (size_t i = 0; i < n; i++) {
            s = yvr_cbor_r_skip(r);
            if (s != YVR_OK) {
                return s;
            }
        }
        return YVR_OK;
    }
    case YVR_CBOR_KIND_MAP: {
        size_t n;
        s = yvr_cbor_r_map_begin(r, &n);
        if (s != YVR_OK) {
            return s;
        }
        for (size_t i = 0; i < n; i++) {
            s = yvr_cbor_r_skip(r);  /* key */
            if (s != YVR_OK) {
                return s;
            }
            s = yvr_cbor_r_skip(r);  /* value */
            if (s != YVR_OK) {
                return s;
            }
        }
        return YVR_OK;
    }
    case YVR_CBOR_KIND_NONE:
    default:
        return r_set_err(r, YVR_E_BAD_FRAME);
    }
}
