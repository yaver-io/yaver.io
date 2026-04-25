/* Yaver c-agent — Phase-0 frame body codec tests. */

#include "yvr/body.h"
#include "yvr/status.h"

#include <stdbool.h>
#include <stddef.h>
#include <stdint.h>
#include <stdio.h>
#include <string.h>

#define EXP_OK(call)        do { if ((call) != YVR_OK) return rc; } while (0)
#define EXPECT(c, code)     do { if (!(c))             return (code); } while (0)

/* ── HELLO ──────────────────────────────────────────────────── */

static int test_hello_roundtrip_full(void)
{
    int rc = 100;
    uint8_t buf[64];
    size_t  n = 0;

    yvr_hello_t in = {
        .protocol_version  = YVR_PROTOCOL_VERSION,
        .role              = "device",
        .role_len          = 6,
        .agent_version     = "yvr-cagent/0.0.1",
        .agent_version_len = 16,
    };
    EXP_OK(yvr_hello_encode(&in, buf, sizeof buf, &n));
    EXPECT(n > 0 && n <= sizeof buf, rc);

    yvr_hello_t out;
    EXP_OK(yvr_hello_decode(buf, n, &out));
    EXPECT(out.protocol_version == YVR_PROTOCOL_VERSION, rc + 1);
    EXPECT(out.role_len == 6 && memcmp(out.role, "device", 6) == 0, rc + 2);
    EXPECT(out.agent_version_len == 16 &&
           memcmp(out.agent_version, "yvr-cagent/0.0.1", 16) == 0, rc + 3);
    return 0;
}

static int test_hello_roundtrip_minimal(void)
{
    int rc = 110;
    uint8_t buf[32];
    size_t  n = 0;

    yvr_hello_t in = {
        .protocol_version = YVR_PROTOCOL_VERSION,
        .role             = "brain",
        .role_len         = 5,
        /* agent_version intentionally NULL */
    };
    EXP_OK(yvr_hello_encode(&in, buf, sizeof buf, &n));

    yvr_hello_t out;
    EXP_OK(yvr_hello_decode(buf, n, &out));
    EXPECT(out.protocol_version == YVR_PROTOCOL_VERSION, rc);
    EXPECT(out.role_len == 5 && memcmp(out.role, "brain", 5) == 0, rc + 1);
    EXPECT(out.agent_version == NULL && out.agent_version_len == 0, rc + 2);
    return 0;
}

static int test_hello_known_bytes(void)
{
    /* Locks the wire format. CTAP2 deterministic order: keys
     * sorted by their CBOR-encoded form, length-first.
     *
     *   {"v": 1, "role": "brain"}
     *
     *   a2                       # map(2)
     *   61 76                    # "v"
     *   01                       # 1
     *   64 72 6f 6c 65           # "role"
     *   65 62 72 61 69 6e        # "brain"
     */
    int rc = 120;
    uint8_t buf[16];
    size_t  n = 0;
    yvr_hello_t in = {
        .protocol_version = 1,
        .role             = "brain",
        .role_len         = 5,
    };
    EXP_OK(yvr_hello_encode(&in, buf, sizeof buf, &n));
    static const uint8_t exp[] = {
        0xa2,
        0x61, 'v',
        0x01,
        0x64, 'r','o','l','e',
        0x65, 'b','r','a','i','n',
    };
    EXPECT(n == sizeof exp && memcmp(buf, exp, sizeof exp) == 0, rc);
    return 0;
}

static int test_hello_skips_unknown_fields(void)
{
    /* Forward-compat: a HELLO carrying an extra "future" field
     * decodes successfully, with the extra field silently skipped.
     *
     *   {"v": 1, "role": "device", "future": 42}
     */
    int rc = 130;
    static const uint8_t in[] = {
        0xa3,
        0x61, 'v',                          0x01,
        0x64, 'r','o','l','e',              0x66, 'd','e','v','i','c','e',
        0x66, 'f','u','t','u','r','e',      0x18, 0x2a,
    };
    yvr_hello_t out;
    EXP_OK(yvr_hello_decode(in, sizeof in, &out));
    EXPECT(out.protocol_version == 1, rc);
    EXPECT(out.role_len == 6 && memcmp(out.role, "device", 6) == 0, rc + 1);
    return 0;
}

static int test_hello_rejects_missing_required(void)
{
    int rc = 140;
    /* Map with only "v" — missing "role". */
    static const uint8_t in[] = { 0xa1, 0x61, 'v', 0x01 };
    yvr_hello_t out;
    EXPECT(yvr_hello_decode(in, sizeof in, &out) == YVR_E_BAD_FRAME, rc);
    return 0;
}

static int test_hello_buffer_too_small(void)
{
    int rc = 150;
    uint8_t small[4];
    size_t  n;
    yvr_hello_t in = {
        .protocol_version = 1,
        .role             = "device",
        .role_len         = 6,
    };
    EXPECT(yvr_hello_encode(&in, small, sizeof small, &n) == YVR_E_BUFFER_TOO_SMALL, rc);
    return 0;
}

/* ── HEARTBEAT ──────────────────────────────────────────────── */

static int test_heartbeat_roundtrip_minimal(void)
{
    int rc = 200;
    uint8_t buf[32];
    size_t  n = 0;

    yvr_heartbeat_t in = {
        .protocol_version = YVR_PROTOCOL_VERSION,
        .now_ms           = 1700000000000ULL,
    };
    EXP_OK(yvr_heartbeat_encode(&in, buf, sizeof buf, &n));

    yvr_heartbeat_t out;
    EXP_OK(yvr_heartbeat_decode(buf, n, &out));
    EXPECT(out.protocol_version == YVR_PROTOCOL_VERSION, rc);
    EXPECT(out.now_ms == 1700000000000ULL, rc + 1);
    EXPECT(out.signature == NULL && out.signature_len == 0, rc + 2);
    return 0;
}

static int test_heartbeat_roundtrip_signed(void)
{
    int rc = 210;
    uint8_t buf[128];
    size_t  n = 0;

    /* 64-byte ECDSA-shaped signature (placeholder bytes; the
     * codec doesn't verify, just transports). */
    uint8_t sig[64];
    for (size_t i = 0; i < sizeof sig; i++) sig[i] = (uint8_t)i;

    yvr_heartbeat_t in = {
        .protocol_version = 1,
        .now_ms           = 1700000000123ULL,
        .signature        = sig,
        .signature_len    = sizeof sig,
    };
    EXP_OK(yvr_heartbeat_encode(&in, buf, sizeof buf, &n));

    yvr_heartbeat_t out;
    EXP_OK(yvr_heartbeat_decode(buf, n, &out));
    EXPECT(out.now_ms == 1700000000123ULL, rc);
    EXPECT(out.signature_len == sizeof sig, rc + 1);
    EXPECT(memcmp(out.signature, sig, sizeof sig) == 0, rc + 2);
    return 0;
}

static int test_heartbeat_known_bytes(void)
{
    /* {"v": 1, "now_ms": 1000}
     *
     *   a2                          # map(2)
     *   61 76                       # "v"
     *   01                          # 1
     *   66 6e 6f 77 5f 6d 73        # "now_ms"
     *   19 03 e8                    # 1000
     */
    int rc = 220;
    uint8_t buf[24];
    size_t  n = 0;
    yvr_heartbeat_t in = {
        .protocol_version = 1,
        .now_ms           = 1000,
    };
    EXP_OK(yvr_heartbeat_encode(&in, buf, sizeof buf, &n));
    static const uint8_t exp[] = {
        0xa2,
        0x61, 'v',                              0x01,
        0x66, 'n','o','w','_','m','s',          0x19, 0x03, 0xe8,
    };
    EXPECT(n == sizeof exp && memcmp(buf, exp, sizeof exp) == 0, rc);
    return 0;
}

static int test_heartbeat_skips_unknown(void)
{
    /* {"v":1, "future":42, "now_ms":2000}
     *
     * Map keys must be in CTAP2 order: "v"(0x61) < "future"(0x66) <
     * "now_ms"(0x66 with longer text). Within length 6 keys,
     * bytewise compare picks "future"<"now_ms" because 'f'<'n'. */
    int rc = 230;
    static const uint8_t in[] = {
        0xa3,
        0x61, 'v',                              0x01,
        0x66, 'f','u','t','u','r','e',          0x18, 0x2a,
        0x66, 'n','o','w','_','m','s',          0x19, 0x07, 0xd0,
    };
    yvr_heartbeat_t out;
    EXP_OK(yvr_heartbeat_decode(in, sizeof in, &out));
    EXPECT(out.now_ms == 2000, rc);
    return 0;
}

static int test_heartbeat_rejects_missing_now_ms(void)
{
    int rc = 240;
    /* Only "v". */
    static const uint8_t in[] = { 0xa1, 0x61, 'v', 0x01 };
    yvr_heartbeat_t out;
    EXPECT(yvr_heartbeat_decode(in, sizeof in, &out) == YVR_E_BAD_FRAME, rc);
    return 0;
}

/* ── Driver ──────────────────────────────────────────────────── */

typedef int (*tfn)(void);
struct tc { const char *name; tfn fn; };

int main(void)
{
    static const struct tc T[] = {
        { "hello_roundtrip_full",            test_hello_roundtrip_full          },
        { "hello_roundtrip_minimal",         test_hello_roundtrip_minimal       },
        { "hello_known_bytes",               test_hello_known_bytes             },
        { "hello_skips_unknown_fields",      test_hello_skips_unknown_fields    },
        { "hello_rejects_missing_required",  test_hello_rejects_missing_required},
        { "hello_buffer_too_small",          test_hello_buffer_too_small        },
        { "heartbeat_roundtrip_minimal",     test_heartbeat_roundtrip_minimal   },
        { "heartbeat_roundtrip_signed",      test_heartbeat_roundtrip_signed    },
        { "heartbeat_known_bytes",           test_heartbeat_known_bytes         },
        { "heartbeat_skips_unknown",         test_heartbeat_skips_unknown       },
        { "heartbeat_rejects_missing_now_ms",test_heartbeat_rejects_missing_now_ms},
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
