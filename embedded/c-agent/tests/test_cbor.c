/* Yaver c-agent — CBOR codec tests.
 *
 * Vectors are drawn from RFC 8949 Appendix A. The codec only
 * implements the deterministic-encoding subset, so vectors
 * involving floats / bignums / tagged values / indefinite lengths
 * are absent (and the decoder rejects them as YVR_E_BAD_FRAME).
 */

#include "yvr/cbor.h"
#include "yvr/status.h"

#include <stdbool.h>
#include <stddef.h>
#include <stdint.h>
#include <stdio.h>
#include <string.h>

static int eq_bytes(const uint8_t *got, size_t got_n,
                    const uint8_t *exp, size_t exp_n)
{
    return got_n == exp_n && memcmp(got, exp, exp_n) == 0;
}

#define EXP_OK(call)              do { if ((call) != YVR_OK) return rc; } while (0)
#define EXPECT(cond, code)        do { if (!(cond))         return (code); } while (0)

/* ── Encoder vectors ─────────────────────────────────────────── */

struct uint_vec { uint64_t v; const uint8_t *exp; size_t exp_n; };

static int test_w_uint(void)
{
    static const uint8_t b_0[]    = { 0x00 };
    static const uint8_t b_1[]    = { 0x01 };
    static const uint8_t b_10[]   = { 0x0a };
    static const uint8_t b_23[]   = { 0x17 };
    static const uint8_t b_24[]   = { 0x18, 0x18 };
    static const uint8_t b_25[]   = { 0x18, 0x19 };
    static const uint8_t b_100[]  = { 0x18, 0x64 };
    static const uint8_t b_1000[] = { 0x19, 0x03, 0xe8 };
    static const uint8_t b_1m[]   = { 0x1a, 0x00, 0x0f, 0x42, 0x40 };
    static const uint8_t b_1t[]   = { 0x1b, 0x00, 0x00, 0x00, 0xe8, 0xd4, 0xa5, 0x10, 0x00 };
    static const uint8_t b_max[]  = { 0x1b, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff };

    static const struct uint_vec V[] = {
        { 0,                    b_0,    sizeof b_0   },
        { 1,                    b_1,    sizeof b_1   },
        { 10,                   b_10,   sizeof b_10  },
        { 23,                   b_23,   sizeof b_23  },
        { 24,                   b_24,   sizeof b_24  },
        { 25,                   b_25,   sizeof b_25  },
        { 100,                  b_100,  sizeof b_100 },
        { 1000,                 b_1000, sizeof b_1000},
        { 1000000,              b_1m,   sizeof b_1m  },
        { 1000000000000ULL,     b_1t,   sizeof b_1t  },
        { 0xFFFFFFFFFFFFFFFFULL,b_max,  sizeof b_max },
    };
    int rc = 1000;
    for (size_t i = 0; i < sizeof V / sizeof V[0]; i++, rc++) {
        uint8_t buf[16];
        yvr_cbor_w_t w;
        yvr_cbor_w_init(&w, buf, sizeof buf);
        EXP_OK(yvr_cbor_w_uint(&w, V[i].v));
        EXPECT(eq_bytes(buf, yvr_cbor_w_len(&w), V[i].exp, V[i].exp_n), rc);
    }
    return 0;
}

struct int_vec { int64_t v; const uint8_t *exp; size_t exp_n; };

static int test_w_int(void)
{
    static const uint8_t b_neg1[]    = { 0x20 };
    static const uint8_t b_neg10[]   = { 0x29 };
    static const uint8_t b_neg100[]  = { 0x38, 0x63 };
    static const uint8_t b_neg1000[] = { 0x39, 0x03, 0xe7 };
    static const uint8_t b_pos1[]    = { 0x01 };
    static const uint8_t b_min[]     = { 0x3b, 0x7f, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff };

    static const struct int_vec V[] = {
        {   -1,         b_neg1,    sizeof b_neg1    },
        {  -10,         b_neg10,   sizeof b_neg10   },
        { -100,         b_neg100,  sizeof b_neg100  },
        {-1000,         b_neg1000, sizeof b_neg1000 },
        {    1,         b_pos1,    sizeof b_pos1    },
        { INT64_MIN,    b_min,     sizeof b_min     },
    };
    int rc = 1100;
    for (size_t i = 0; i < sizeof V / sizeof V[0]; i++, rc++) {
        uint8_t buf[16];
        yvr_cbor_w_t w;
        yvr_cbor_w_init(&w, buf, sizeof buf);
        EXP_OK(yvr_cbor_w_int(&w, V[i].v));
        EXPECT(eq_bytes(buf, yvr_cbor_w_len(&w), V[i].exp, V[i].exp_n), rc);
    }
    return 0;
}

static int test_w_text(void)
{
    static const struct {
        const char *s;
        size_t      n;
        const uint8_t *exp;
        size_t      exp_n;
    } V[] = {
        { "",     0, (const uint8_t *)"\x60",                  1 },
        { "a",    1, (const uint8_t *)"\x61\x61",              2 },
        { "IETF", 4, (const uint8_t *)"\x64\x49\x45\x54\x46",  5 },
        { "\"\\", 2, (const uint8_t *)"\x62\x22\x5c",          3 },
    };
    int rc = 1200;
    for (size_t i = 0; i < sizeof V / sizeof V[0]; i++, rc++) {
        uint8_t buf[16];
        yvr_cbor_w_t w;
        yvr_cbor_w_init(&w, buf, sizeof buf);
        EXP_OK(yvr_cbor_w_text(&w, V[i].s, V[i].n));
        EXPECT(eq_bytes(buf, yvr_cbor_w_len(&w), V[i].exp, V[i].exp_n), rc);
    }
    return 0;
}

static int test_w_bytes(void)
{
    int rc = 1300;
    {
        uint8_t buf[8];
        yvr_cbor_w_t w;
        yvr_cbor_w_init(&w, buf, sizeof buf);
        EXP_OK(yvr_cbor_w_bytes(&w, NULL, 0));
        const uint8_t exp[] = { 0x40 };
        EXPECT(eq_bytes(buf, yvr_cbor_w_len(&w), exp, sizeof exp), rc);
    }
    rc++;
    {
        uint8_t buf[8];
        yvr_cbor_w_t w;
        yvr_cbor_w_init(&w, buf, sizeof buf);
        const uint8_t in[] = { 0x01, 0x02, 0x03, 0x04 };
        EXP_OK(yvr_cbor_w_bytes(&w, in, sizeof in));
        const uint8_t exp[] = { 0x44, 0x01, 0x02, 0x03, 0x04 };
        EXPECT(eq_bytes(buf, yvr_cbor_w_len(&w), exp, sizeof exp), rc);
    }
    return 0;
}

static int test_w_array_map(void)
{
    int rc = 1400;
    /* [] */
    {
        uint8_t buf[8];
        yvr_cbor_w_t w;
        yvr_cbor_w_init(&w, buf, sizeof buf);
        EXP_OK(yvr_cbor_w_array_begin(&w, 0));
        const uint8_t exp[] = { 0x80 };
        EXPECT(eq_bytes(buf, yvr_cbor_w_len(&w), exp, sizeof exp), rc);
    }
    rc++;
    /* [1, 2, 3] */
    {
        uint8_t buf[8];
        yvr_cbor_w_t w;
        yvr_cbor_w_init(&w, buf, sizeof buf);
        EXP_OK(yvr_cbor_w_array_begin(&w, 3));
        EXP_OK(yvr_cbor_w_uint(&w, 1));
        EXP_OK(yvr_cbor_w_uint(&w, 2));
        EXP_OK(yvr_cbor_w_uint(&w, 3));
        const uint8_t exp[] = { 0x83, 0x01, 0x02, 0x03 };
        EXPECT(eq_bytes(buf, yvr_cbor_w_len(&w), exp, sizeof exp), rc);
    }
    rc++;
    /* {} */
    {
        uint8_t buf[8];
        yvr_cbor_w_t w;
        yvr_cbor_w_init(&w, buf, sizeof buf);
        EXP_OK(yvr_cbor_w_map_begin(&w, 0));
        const uint8_t exp[] = { 0xa0 };
        EXPECT(eq_bytes(buf, yvr_cbor_w_len(&w), exp, sizeof exp), rc);
    }
    rc++;
    /* {1:2, 3:4} */
    {
        uint8_t buf[8];
        yvr_cbor_w_t w;
        yvr_cbor_w_init(&w, buf, sizeof buf);
        EXP_OK(yvr_cbor_w_map_begin(&w, 2));
        EXP_OK(yvr_cbor_w_uint(&w, 1));
        EXP_OK(yvr_cbor_w_uint(&w, 2));
        EXP_OK(yvr_cbor_w_uint(&w, 3));
        EXP_OK(yvr_cbor_w_uint(&w, 4));
        const uint8_t exp[] = { 0xa2, 0x01, 0x02, 0x03, 0x04 };
        EXPECT(eq_bytes(buf, yvr_cbor_w_len(&w), exp, sizeof exp), rc);
    }
    rc++;
    /* {"a":1, "b":[2,3]} — RFC 8949 vector */
    {
        uint8_t buf[16];
        yvr_cbor_w_t w;
        yvr_cbor_w_init(&w, buf, sizeof buf);
        EXP_OK(yvr_cbor_w_map_begin(&w, 2));
        EXP_OK(yvr_cbor_w_text(&w, "a", 1));
        EXP_OK(yvr_cbor_w_uint(&w, 1));
        EXP_OK(yvr_cbor_w_text(&w, "b", 1));
        EXP_OK(yvr_cbor_w_array_begin(&w, 2));
        EXP_OK(yvr_cbor_w_uint(&w, 2));
        EXP_OK(yvr_cbor_w_uint(&w, 3));
        const uint8_t exp[] = {
            0xa2, 0x61, 0x61, 0x01, 0x61, 0x62, 0x82, 0x02, 0x03,
        };
        EXPECT(eq_bytes(buf, yvr_cbor_w_len(&w), exp, sizeof exp), rc);
    }
    return 0;
}

static int test_w_simple(void)
{
    int rc = 1500;
    static const struct { const uint8_t *exp; uint8_t one; } V[] = {
        { (const uint8_t *)"\xf4", 0xf4 },
        { (const uint8_t *)"\xf5", 0xf5 },
        { (const uint8_t *)"\xf6", 0xf6 },
    };
    {
        uint8_t buf[2];
        yvr_cbor_w_t w;
        yvr_cbor_w_init(&w, buf, sizeof buf);
        EXP_OK(yvr_cbor_w_bool(&w, false));
        EXPECT(yvr_cbor_w_len(&w) == 1 && buf[0] == V[0].one, rc);
    }
    rc++;
    {
        uint8_t buf[2];
        yvr_cbor_w_t w;
        yvr_cbor_w_init(&w, buf, sizeof buf);
        EXP_OK(yvr_cbor_w_bool(&w, true));
        EXPECT(yvr_cbor_w_len(&w) == 1 && buf[0] == V[1].one, rc);
    }
    rc++;
    {
        uint8_t buf[2];
        yvr_cbor_w_t w;
        yvr_cbor_w_init(&w, buf, sizeof buf);
        EXP_OK(yvr_cbor_w_null(&w));
        EXPECT(yvr_cbor_w_len(&w) == 1 && buf[0] == V[2].one, rc);
    }
    return 0;
}

/* ── Writer error paths ──────────────────────────────────────── */

static int test_w_buffer_too_small(void)
{
    int rc = 1600;
    uint8_t buf[1];
    yvr_cbor_w_t w;
    yvr_cbor_w_init(&w, buf, sizeof buf);
    EXPECT(yvr_cbor_w_uint(&w, 24) == YVR_E_BUFFER_TOO_SMALL, rc);
    /* Sticky: subsequent calls keep the same error. */
    EXPECT(yvr_cbor_w_uint(&w, 1)  == YVR_E_BUFFER_TOO_SMALL, rc + 1);
    return 0;
}

static int test_w_null_args(void)
{
    int rc = 1700;
    uint8_t buf[8];
    yvr_cbor_w_t w;
    yvr_cbor_w_init(&w, buf, sizeof buf);
    EXPECT(yvr_cbor_w_text(&w, NULL, 1) == YVR_E_INVALID_ARG, rc);
    /* Zero-length NULL is a no-op + a header — explicitly allowed. */
    yvr_cbor_w_init(&w, buf, sizeof buf);
    EXPECT(yvr_cbor_w_bytes(&w, NULL, 0) == YVR_OK, rc + 1);
    return 0;
}

/* ── Decoder ─────────────────────────────────────────────────── */

static int test_r_uint(void)
{
    int rc = 2000;
    static const uint8_t in[] = { 0x18, 0x64 };  /* 100 */
    yvr_cbor_r_t r;
    yvr_cbor_r_init(&r, in, sizeof in);
    uint64_t v = 0;
    EXP_OK(yvr_cbor_r_uint(&r, &v));
    EXPECT(v == 100, rc);
    EXPECT(yvr_cbor_r_pos(&r) == sizeof in, rc + 1);
    return 0;
}

static int test_r_int(void)
{
    int rc = 2100;
    static const uint8_t in[] = { 0x39, 0x03, 0xe7 };  /* -1000 */
    yvr_cbor_r_t r;
    yvr_cbor_r_init(&r, in, sizeof in);
    int64_t v = 0;
    EXP_OK(yvr_cbor_r_int(&r, &v));
    EXPECT(v == -1000, rc);
    return 0;
}

static int test_r_text(void)
{
    int rc = 2200;
    static const uint8_t in[] = { 0x64, 'I', 'E', 'T', 'F' };
    yvr_cbor_r_t r;
    yvr_cbor_r_init(&r, in, sizeof in);
    const char *p;
    size_t n;
    EXP_OK(yvr_cbor_r_text(&r, &p, &n));
    EXPECT(n == 4 && memcmp(p, "IETF", 4) == 0, rc);
    return 0;
}

static int test_r_array(void)
{
    int rc = 2300;
    static const uint8_t in[] = { 0x83, 0x01, 0x02, 0x03 };
    yvr_cbor_r_t r;
    yvr_cbor_r_init(&r, in, sizeof in);
    size_t n = 0;
    EXP_OK(yvr_cbor_r_array_begin(&r, &n));
    EXPECT(n == 3, rc);
    uint64_t a, b, c;
    EXP_OK(yvr_cbor_r_uint(&r, &a));
    EXP_OK(yvr_cbor_r_uint(&r, &b));
    EXP_OK(yvr_cbor_r_uint(&r, &c));
    EXPECT(a == 1 && b == 2 && c == 3, rc + 1);
    return 0;
}

static int test_r_map(void)
{
    int rc = 2400;
    /* {"a":1, "b":[2,3]} */
    static const uint8_t in[] = {
        0xa2, 0x61, 0x61, 0x01, 0x61, 0x62, 0x82, 0x02, 0x03,
    };
    yvr_cbor_r_t r;
    yvr_cbor_r_init(&r, in, sizeof in);
    size_t kv = 0;
    EXP_OK(yvr_cbor_r_map_begin(&r, &kv));
    EXPECT(kv == 2, rc);
    /* k1 = "a", v1 = 1 */
    const char *k;
    size_t kn;
    EXP_OK(yvr_cbor_r_text(&r, &k, &kn));
    EXPECT(kn == 1 && k[0] == 'a', rc + 1);
    uint64_t v1;
    EXP_OK(yvr_cbor_r_uint(&r, &v1));
    EXPECT(v1 == 1, rc + 2);
    /* k2 = "b", v2 = [2,3] */
    EXP_OK(yvr_cbor_r_text(&r, &k, &kn));
    EXPECT(kn == 1 && k[0] == 'b', rc + 3);
    size_t arr_n;
    EXP_OK(yvr_cbor_r_array_begin(&r, &arr_n));
    EXPECT(arr_n == 2, rc + 4);
    uint64_t a, b;
    EXP_OK(yvr_cbor_r_uint(&r, &a));
    EXP_OK(yvr_cbor_r_uint(&r, &b));
    EXPECT(a == 2 && b == 3, rc + 5);
    return 0;
}

static int test_r_bool_null(void)
{
    int rc = 2500;
    static const uint8_t in[] = { 0xf4, 0xf5, 0xf6 };
    yvr_cbor_r_t r;
    yvr_cbor_r_init(&r, in, sizeof in);
    bool b1, b2;
    EXP_OK(yvr_cbor_r_bool(&r, &b1));
    EXP_OK(yvr_cbor_r_bool(&r, &b2));
    EXP_OK(yvr_cbor_r_null(&r));
    EXPECT(b1 == false && b2 == true, rc);
    return 0;
}

static int test_r_peek(void)
{
    int rc = 2600;
    static const uint8_t in[] = { 0x18, 0x64, 0x39, 0x03, 0xe7,
                                  0x40, 0x60, 0x80, 0xa0,
                                  0xf4, 0xf6 };
    yvr_cbor_r_t r;
    yvr_cbor_r_init(&r, in, sizeof in);
    yvr_cbor_kind_t k;
    EXP_OK(yvr_cbor_r_peek(&r, &k));  EXPECT(k == YVR_CBOR_KIND_UINT,  rc);
    { uint64_t v; EXP_OK(yvr_cbor_r_uint(&r, &v)); }
    EXP_OK(yvr_cbor_r_peek(&r, &k));  EXPECT(k == YVR_CBOR_KIND_INT,   rc + 1);
    { int64_t v; EXP_OK(yvr_cbor_r_int(&r, &v)); }
    EXP_OK(yvr_cbor_r_peek(&r, &k));  EXPECT(k == YVR_CBOR_KIND_BYTES, rc + 2);
    { const uint8_t *p; size_t n; EXP_OK(yvr_cbor_r_bytes(&r, &p, &n)); }
    EXP_OK(yvr_cbor_r_peek(&r, &k));  EXPECT(k == YVR_CBOR_KIND_TEXT,  rc + 3);
    { const char *p; size_t n; EXP_OK(yvr_cbor_r_text(&r, &p, &n)); }
    EXP_OK(yvr_cbor_r_peek(&r, &k));  EXPECT(k == YVR_CBOR_KIND_ARRAY, rc + 4);
    { size_t n; EXP_OK(yvr_cbor_r_array_begin(&r, &n)); }
    EXP_OK(yvr_cbor_r_peek(&r, &k));  EXPECT(k == YVR_CBOR_KIND_MAP,   rc + 5);
    { size_t n; EXP_OK(yvr_cbor_r_map_begin(&r, &n)); }
    EXP_OK(yvr_cbor_r_peek(&r, &k));  EXPECT(k == YVR_CBOR_KIND_BOOL,  rc + 6);
    { bool v; EXP_OK(yvr_cbor_r_bool(&r, &v)); }
    EXP_OK(yvr_cbor_r_peek(&r, &k));  EXPECT(k == YVR_CBOR_KIND_NULL,  rc + 7);
    EXP_OK(yvr_cbor_r_null(&r));
    return 0;
}

static int test_r_skip(void)
{
    int rc = 2700;
    /* Outer map: {"keep":42, "drop":[1,[2,3]], "tail":"x"} */
    static const uint8_t in[] = {
        0xa3,
        0x64, 'k','e','e','p',  0x18, 0x2a,
        0x64, 'd','r','o','p',  0x82, 0x01, 0x82, 0x02, 0x03,
        0x64, 't','a','i','l',  0x61, 'x',
    };
    yvr_cbor_r_t r;
    yvr_cbor_r_init(&r, in, sizeof in);
    size_t kv;
    EXP_OK(yvr_cbor_r_map_begin(&r, &kv));
    EXPECT(kv == 3, rc);

    const char *k; size_t kn;
    EXP_OK(yvr_cbor_r_text(&r, &k, &kn));
    EXPECT(kn == 4 && memcmp(k, "keep", 4) == 0, rc + 1);
    uint64_t v;
    EXP_OK(yvr_cbor_r_uint(&r, &v));
    EXPECT(v == 42, rc + 2);

    EXP_OK(yvr_cbor_r_text(&r, &k, &kn));
    EXPECT(kn == 4 && memcmp(k, "drop", 4) == 0, rc + 3);
    EXP_OK(yvr_cbor_r_skip(&r));   /* skips [1,[2,3]] recursively */

    EXP_OK(yvr_cbor_r_text(&r, &k, &kn));
    EXPECT(kn == 4 && memcmp(k, "tail", 4) == 0, rc + 4);
    EXP_OK(yvr_cbor_r_text(&r, &k, &kn));
    EXPECT(kn == 1 && k[0] == 'x', rc + 5);
    EXPECT(yvr_cbor_r_pos(&r) == sizeof in, rc + 6);
    return 0;
}

/* ── Decoder error paths ─────────────────────────────────────── */

static int test_r_truncated(void)
{
    int rc = 2800;
    static const uint8_t in[] = { 0x18 };  /* expects 1 more byte */
    yvr_cbor_r_t r;
    yvr_cbor_r_init(&r, in, sizeof in);
    uint64_t v;
    EXPECT(yvr_cbor_r_uint(&r, &v) == YVR_E_TRUNCATED, rc);
    return 0;
}

static int test_r_indefinite_rejected(void)
{
    int rc = 2900;
    /* Indefinite-length byte string (0x5f) — must be rejected. */
    static const uint8_t in[] = { 0x5f, 0xff };
    yvr_cbor_r_t r;
    yvr_cbor_r_init(&r, in, sizeof in);
    const uint8_t *p;
    size_t n;
    EXPECT(yvr_cbor_r_bytes(&r, &p, &n) == YVR_E_BAD_FRAME, rc);
    return 0;
}

static int test_r_tag_rejected(void)
{
    int rc = 3000;
    /* Tag 0 (epoch text-time string) — outside the supported subset. */
    static const uint8_t in[] = { 0xc0, 0x60 };
    yvr_cbor_r_t r;
    yvr_cbor_r_init(&r, in, sizeof in);
    yvr_cbor_kind_t k;
    EXPECT(yvr_cbor_r_peek(&r, &k) == YVR_E_BAD_FRAME, rc);
    return 0;
}

static int test_r_kind_mismatch(void)
{
    int rc = 3100;
    /* Encoded -1, but caller asks for unsigned. */
    static const uint8_t in[] = { 0x20 };
    yvr_cbor_r_t r;
    yvr_cbor_r_init(&r, in, sizeof in);
    uint64_t v;
    EXPECT(yvr_cbor_r_uint(&r, &v) == YVR_E_BAD_FRAME, rc);
    return 0;
}

/* ── Round-trip ──────────────────────────────────────────────── */

static int test_roundtrip(void)
{
    int rc = 3200;
    /* Encode a small struct: {"v":1, "name":"yvr-c-agent", "now_ms":1700000000000} */
    uint8_t buf[64];
    yvr_cbor_w_t w;
    yvr_cbor_w_init(&w, buf, sizeof buf);
    EXP_OK(yvr_cbor_w_map_begin(&w, 3));
    EXP_OK(yvr_cbor_w_text(&w, "v", 1));
    EXP_OK(yvr_cbor_w_uint(&w, 1));
    EXP_OK(yvr_cbor_w_text(&w, "name", 4));
    EXP_OK(yvr_cbor_w_text(&w, "yvr-c-agent", 11));
    EXP_OK(yvr_cbor_w_text(&w, "now_ms", 6));
    EXP_OK(yvr_cbor_w_uint(&w, 1700000000000ULL));

    /* Decode and check every field. */
    yvr_cbor_r_t r;
    yvr_cbor_r_init(&r, buf, yvr_cbor_w_len(&w));
    size_t kv;
    EXP_OK(yvr_cbor_r_map_begin(&r, &kv));
    EXPECT(kv == 3, rc);

    const char *k; size_t kn;
    uint64_t u;
    const char *s; size_t sn;

    EXP_OK(yvr_cbor_r_text(&r, &k, &kn));
    EXPECT(kn == 1 && k[0] == 'v', rc + 1);
    EXP_OK(yvr_cbor_r_uint(&r, &u));
    EXPECT(u == 1, rc + 2);

    EXP_OK(yvr_cbor_r_text(&r, &k, &kn));
    EXPECT(kn == 4 && memcmp(k, "name", 4) == 0, rc + 3);
    EXP_OK(yvr_cbor_r_text(&r, &s, &sn));
    EXPECT(sn == 11 && memcmp(s, "yvr-c-agent", 11) == 0, rc + 4);

    EXP_OK(yvr_cbor_r_text(&r, &k, &kn));
    EXPECT(kn == 6 && memcmp(k, "now_ms", 6) == 0, rc + 5);
    EXP_OK(yvr_cbor_r_uint(&r, &u));
    EXPECT(u == 1700000000000ULL, rc + 6);

    return 0;
}

/* ── Driver ──────────────────────────────────────────────────── */

typedef int (*tfn)(void);
struct tc { const char *name; tfn fn; };

int main(void)
{
    static const struct tc T[] = {
        { "w_uint",                 test_w_uint                 },
        { "w_int",                  test_w_int                  },
        { "w_text",                 test_w_text                 },
        { "w_bytes",                test_w_bytes                },
        { "w_array_map",            test_w_array_map            },
        { "w_simple",               test_w_simple               },
        { "w_buffer_too_small",     test_w_buffer_too_small     },
        { "w_null_args",            test_w_null_args            },
        { "r_uint",                 test_r_uint                 },
        { "r_int",                  test_r_int                  },
        { "r_text",                 test_r_text                 },
        { "r_array",                test_r_array                },
        { "r_map",                  test_r_map                  },
        { "r_bool_null",            test_r_bool_null            },
        { "r_peek",                 test_r_peek                 },
        { "r_skip",                 test_r_skip                 },
        { "r_truncated",            test_r_truncated            },
        { "r_indefinite_rejected",  test_r_indefinite_rejected  },
        { "r_tag_rejected",         test_r_tag_rejected         },
        { "r_kind_mismatch",        test_r_kind_mismatch        },
        { "roundtrip",              test_roundtrip              },
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
