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

/* ── AUTH ───────────────────────────────────────────────────── */

static int test_auth_roundtrip(void)
{
    int rc = 300;
    uint8_t nonce[32];
    for (size_t i = 0; i < sizeof nonce; i++) nonce[i] = (uint8_t)(0xa0 + i);

    yvr_auth_t in = {
        .protocol_version = YVR_PROTOCOL_VERSION,
        .nonce            = nonce,
        .nonce_len        = sizeof nonce,
        .signed_now_ms    = 1700000000123ULL,
    };
    uint8_t buf[128];
    size_t  n = 0;
    EXP_OK(yvr_auth_encode(&in, buf, sizeof buf, &n));

    yvr_auth_t out;
    EXP_OK(yvr_auth_decode(buf, n, &out));
    EXPECT(out.protocol_version == YVR_PROTOCOL_VERSION,            rc);
    EXPECT(out.nonce_len == sizeof nonce,                           rc + 1);
    EXPECT(memcmp(out.nonce, nonce, sizeof nonce) == 0,             rc + 2);
    EXPECT(out.signed_now_ms == 1700000000123ULL,                   rc + 3);
    return 0;
}

static int test_auth_rejects_missing_nonce(void)
{
    int rc = 310;
    static const uint8_t in[] = {
        0xa1, 0x61, 'v', 0x01,
    };
    yvr_auth_t out;
    EXPECT(yvr_auth_decode(in, sizeof in, &out) == YVR_E_BAD_FRAME, rc);
    return 0;
}

/* ── AUTHRSP ────────────────────────────────────────────────── */

static int test_authrsp_roundtrip(void)
{
    int rc = 400;
    uint8_t sig[64];
    uint8_t nonce[32];
    uint8_t cert[128];
    for (size_t i = 0; i < sizeof sig;   i++) sig[i]   = (uint8_t)i;
    for (size_t i = 0; i < sizeof nonce; i++) nonce[i] = (uint8_t)(0xa0 + i);
    for (size_t i = 0; i < sizeof cert;  i++) cert[i]  = (uint8_t)(0x30 + i);

    yvr_authrsp_t in = {
        .protocol_version = 1,
        .sig              = sig,   .sig_len         = sizeof sig,
        .nonce            = nonce, .nonce_len       = sizeof nonce,
        .device_cert      = cert,  .device_cert_len = sizeof cert,
    };
    uint8_t buf[512];
    size_t  n = 0;
    EXP_OK(yvr_authrsp_encode(&in, buf, sizeof buf, &n));

    yvr_authrsp_t out;
    EXP_OK(yvr_authrsp_decode(buf, n, &out));
    EXPECT(out.sig_len         == sizeof sig    && memcmp(out.sig,         sig,   sizeof sig)   == 0, rc);
    EXPECT(out.nonce_len       == sizeof nonce  && memcmp(out.nonce,       nonce, sizeof nonce) == 0, rc + 1);
    EXPECT(out.device_cert_len == sizeof cert   && memcmp(out.device_cert, cert,  sizeof cert)  == 0, rc + 2);
    return 0;
}

/* ── ATTEST ─────────────────────────────────────────────────── */

static int test_attest_roundtrip(void)
{
    int rc = 500;
    static const char *caps[] = {
        "fs.read.logs",
        "fs.read.config",
        "nl80211.read.station",
    };
    yvr_attest_t in = {
        .protocol_version    = 1,
        .arch                = "aarch64",  .arch_len   = 7,
        .libc                = "musl-1.2", .libc_len   = 8,
        .kernel              = "5.15.149", .kernel_len = 8,
        .capabilities        = caps,
        .capabilities_count  = sizeof caps / sizeof caps[0],
        .ebpf_supported      = true,
        .cache_quota_bytes   = 4ULL * 1024ULL * 1024ULL,
    };
    uint8_t buf[256];
    size_t  n = 0;
    EXP_OK(yvr_attest_encode(&in, buf, sizeof buf, &n));

    yvr_attest_t out;
    const char *out_caps[8];
    size_t      out_caps_lens[8];
    EXP_OK(yvr_attest_decode(buf, n, &out, out_caps, out_caps_lens, 8));

    EXPECT(out.arch_len == 7  && memcmp(out.arch, "aarch64", 7) == 0,     rc);
    EXPECT(out.libc_len == 8  && memcmp(out.libc, "musl-1.2", 8) == 0,    rc + 1);
    EXPECT(out.kernel_len== 8 && memcmp(out.kernel, "5.15.149", 8) == 0,  rc + 2);
    EXPECT(out.capabilities_count == 3,                                   rc + 3);
    EXPECT(out_caps_lens[0] == 12 && memcmp(out_caps[0], "fs.read.logs", 12) == 0, rc + 4);
    EXPECT(out_caps_lens[2] == 20 && memcmp(out_caps[2], "nl80211.read.station", 20) == 0, rc + 5);
    EXPECT(out.ebpf_supported == true,                                    rc + 6);
    EXPECT(out.cache_quota_bytes == 4ULL * 1024ULL * 1024ULL,             rc + 7);
    return 0;
}

static int test_attest_capabilities_overflow(void)
{
    int rc = 510;
    /* Two caps but only room for one in the output array. */
    static const char *caps[] = { "a", "b" };
    yvr_attest_t in = {
        .protocol_version    = 1,
        .arch                = "x", .arch_len   = 1,
        .libc                = "y", .libc_len   = 1,
        .kernel              = "z", .kernel_len = 1,
        .capabilities        = caps,
        .capabilities_count  = 2,
    };
    uint8_t buf[128];
    size_t  n = 0;
    EXP_OK(yvr_attest_encode(&in, buf, sizeof buf, &n));

    yvr_attest_t out;
    const char *out_caps[1];
    size_t      out_caps_lens[1];
    yvr_status_t s = yvr_attest_decode(buf, n, &out, out_caps, out_caps_lens, 1);
    EXPECT(s == YVR_E_BUFFER_TOO_SMALL, rc);
    /* Caller can still see the full count to resize. */
    EXPECT(out.capabilities_count == 2, rc + 1);
    EXPECT(out_caps_lens[0] == 1 && memcmp(out_caps[0], "a", 1) == 0, rc + 2);
    return 0;
}

/* ── ERROR ──────────────────────────────────────────────────── */

static int test_error_roundtrip_full(void)
{
    int rc = 600;
    yvr_error_t in = {
        .protocol_version = 1,
        .code             = -42,
        .context          = "hostapd ctrl-iface returned 'FAIL'",
        .context_len      = 33,
        .message          = "wifi probe timeout",
        .message_len      = 18,
        .stream_id        = 7,
    };
    uint8_t buf[128];
    size_t  n = 0;
    EXP_OK(yvr_error_encode(&in, buf, sizeof buf, &n));

    yvr_error_t out;
    EXP_OK(yvr_error_decode(buf, n, &out));
    EXPECT(out.code == -42,        rc);
    EXPECT(out.message_len == 18 && memcmp(out.message, "wifi probe timeout", 18) == 0, rc + 1);
    EXPECT(out.context_len == 33,  rc + 2);
    EXPECT(out.stream_id == 7,     rc + 3);
    return 0;
}

static int test_error_roundtrip_minimal(void)
{
    int rc = 610;
    yvr_error_t in = {
        .protocol_version = 1,
        .code             = -1,
        .message          = "x",
        .message_len      = 1,
    };
    uint8_t buf[64];
    size_t  n = 0;
    EXP_OK(yvr_error_encode(&in, buf, sizeof buf, &n));

    yvr_error_t out;
    EXP_OK(yvr_error_decode(buf, n, &out));
    EXPECT(out.code == -1,            rc);
    EXPECT(out.message_len == 1,      rc + 1);
    EXPECT(out.context == NULL,       rc + 2);
    EXPECT(out.stream_id == 0,        rc + 3);
    return 0;
}

/* ── INVOKE ─────────────────────────────────────────────────── */

static int test_invoke_roundtrip_minimal(void)
{
    int rc = 700;
    uint8_t hash[32];
    for (size_t i = 0; i < sizeof hash; i++) hash[i] = (uint8_t)(0xab + i);
    uint8_t args[16];
    for (size_t i = 0; i < sizeof args; i++) args[i] = (uint8_t)(0xc0 + i);

    yvr_invoke_t in = {
        .protocol_version = 1,
        .tool_hash = hash, .tool_hash_len = sizeof hash,
        .method = "wifi_client_count", .method_len = 17,
        .args = args, .args_len = sizeof args,
    };
    uint8_t buf[128];
    size_t n = 0;
    EXP_OK(yvr_invoke_encode(&in, buf, sizeof buf, &n));

    yvr_invoke_t out;
    EXP_OK(yvr_invoke_decode(buf, n, &out));
    EXPECT(out.tool_hash_len == sizeof hash, rc);
    EXPECT(memcmp(out.tool_hash, hash, sizeof hash) == 0, rc + 1);
    EXPECT(out.method_len == 17, rc + 2);
    EXPECT(memcmp(out.method, "wifi_client_count", 17) == 0, rc + 3);
    EXPECT(out.args_len == sizeof args, rc + 4);
    EXPECT(memcmp(out.args, args, sizeof args) == 0, rc + 5);
    EXPECT(out.approval == NULL && out.approval_len == 0, rc + 6);
    return 0;
}

static int test_invoke_roundtrip_with_approval(void)
{
    int rc = 710;
    uint8_t hash[32]; uint8_t args[8]; uint8_t apv[64];
    for (size_t i = 0; i < sizeof hash; i++) hash[i] = (uint8_t)i;
    for (size_t i = 0; i < sizeof args; i++) args[i] = (uint8_t)(0x10 + i);
    for (size_t i = 0; i < sizeof apv;  i++) apv[i]  = (uint8_t)(0x80 + i);

    yvr_invoke_t in = {
        .protocol_version = 1,
        .tool_hash = hash, .tool_hash_len = sizeof hash,
        .method = "restart_service", .method_len = 15,
        .args = args, .args_len = sizeof args,
        .approval = apv, .approval_len = sizeof apv,
    };
    uint8_t buf[256];
    size_t n = 0;
    EXP_OK(yvr_invoke_encode(&in, buf, sizeof buf, &n));

    yvr_invoke_t out;
    EXP_OK(yvr_invoke_decode(buf, n, &out));
    EXPECT(out.approval_len == sizeof apv, rc);
    EXPECT(memcmp(out.approval, apv, sizeof apv) == 0, rc + 1);
    return 0;
}

static int test_invoke_skips_unknown(void)
{
    int rc = 720;
    /* Hand-rolled CBOR: {"v":1, "args":h'', "future":42, "method":"x", "tool_hash":h'aa'}
     *
     * CBOR-key order: "v"(0x61) < "args"(0x64) < "future"(0x66) <
     * "method"(0x66) < "tool_hash"(0x69). "future" and "method"
     * tie on first byte (both 0x66, length 6); compare second
     * byte: "future" 'f' (0x66) vs "method" 'm' (0x6d). 'f'<'m'
     * → "future" first.
     */
    static const uint8_t in[] = {
        0xa5,
        0x61, 'v',                              0x01,
        0x64, 'a','r','g','s',                  0x40,
        0x66, 'f','u','t','u','r','e',          0x18, 0x2a,
        0x66, 'm','e','t','h','o','d',          0x61, 'x',
        0x69, 't','o','o','l','_','h','a','s','h', 0x41, 0xaa,
    };
    yvr_invoke_t out;
    EXP_OK(yvr_invoke_decode(in, sizeof in, &out));
    EXPECT(out.method_len == 1 && out.method[0] == 'x', rc);
    EXPECT(out.tool_hash_len == 1 && out.tool_hash[0] == 0xaa, rc + 1);
    return 0;
}

/* ── TOOL_RSP ───────────────────────────────────────────────── */

static int test_tool_rsp_roundtrip_ok(void)
{
    int rc = 800;
    uint8_t hash[32];
    uint8_t result[16];
    for (size_t i = 0; i < sizeof hash;   i++) hash[i]   = (uint8_t)(0xab + i);
    for (size_t i = 0; i < sizeof result; i++) result[i] = (uint8_t)(0xd0 + i);

    yvr_tool_rsp_t in = {
        .protocol_version = 1,
        .result = result, .result_len = sizeof result,
        .status = 0,
        .tool_hash = hash, .tool_hash_len = sizeof hash,
        .duration_ms = 1234,
    };
    uint8_t buf[128];
    size_t  n = 0;
    EXP_OK(yvr_tool_rsp_encode(&in, buf, sizeof buf, &n));

    yvr_tool_rsp_t out;
    EXP_OK(yvr_tool_rsp_decode(buf, n, &out));
    EXPECT(out.status == 0, rc);
    EXPECT(out.result_len == sizeof result, rc + 1);
    EXPECT(memcmp(out.result, result, sizeof result) == 0, rc + 2);
    EXPECT(out.duration_ms == 1234, rc + 3);
    EXPECT(out.error == NULL && out.error_len == 0, rc + 4);
    return 0;
}

static int test_tool_rsp_roundtrip_error(void)
{
    int rc = 810;
    uint8_t hash[32] = {0};
    yvr_tool_rsp_t in = {
        .protocol_version = 1,
        .error = "module trapped: out of memory", .error_len = 29,
        .result = NULL, .result_len = 0,
        .status = -2,
        .tool_hash = hash, .tool_hash_len = sizeof hash,
    };
    uint8_t buf[128];
    size_t  n = 0;
    EXP_OK(yvr_tool_rsp_encode(&in, buf, sizeof buf, &n));

    yvr_tool_rsp_t out;
    EXP_OK(yvr_tool_rsp_decode(buf, n, &out));
    EXPECT(out.status == -2, rc);
    EXPECT(out.result_len == 0, rc + 1);
    EXPECT(out.error_len == 29, rc + 2);
    EXPECT(memcmp(out.error, "module trapped: out of memory", 29) == 0, rc + 3);
    return 0;
}

/* ── STREAM_CHUNK ───────────────────────────────────────────── */

static int test_stream_chunk_roundtrip(void)
{
    int rc = 900;
    uint8_t data[64];
    for (size_t i = 0; i < sizeof data; i++) data[i] = (uint8_t)i;

    yvr_stream_chunk_t in = {
        .protocol_version = 1,
        .seq = 17,
        .data = data, .data_len = sizeof data,
        .stream_id = 0xDEADBEEF,
        .end_stream = false,
    };
    uint8_t buf[128];
    size_t  n = 0;
    EXP_OK(yvr_stream_chunk_encode(&in, buf, sizeof buf, &n));

    yvr_stream_chunk_t out;
    EXP_OK(yvr_stream_chunk_decode(buf, n, &out));
    EXPECT(out.seq == 17,                    rc);
    EXPECT(out.data_len == sizeof data,      rc + 1);
    EXPECT(memcmp(out.data, data, sizeof data) == 0, rc + 2);
    EXPECT(out.stream_id == 0xDEADBEEF,      rc + 3);
    EXPECT(out.end_stream == false,          rc + 4);
    return 0;
}

static int test_stream_chunk_end(void)
{
    int rc = 910;
    yvr_stream_chunk_t in = {
        .protocol_version = 1,
        .seq = 999,
        .data = NULL, .data_len = 0,
        .stream_id = 1,
        .end_stream = true,
    };
    uint8_t buf[64];
    size_t  n = 0;
    EXP_OK(yvr_stream_chunk_encode(&in, buf, sizeof buf, &n));

    yvr_stream_chunk_t out;
    EXP_OK(yvr_stream_chunk_decode(buf, n, &out));
    EXPECT(out.end_stream == true, rc);
    EXPECT(out.data_len == 0,      rc + 1);
    return 0;
}

/* ── NEED ───────────────────────────────────────────────────── */

static int test_need_roundtrip(void)
{
    int rc = 1000;
    uint8_t hash[32];
    for (size_t i = 0; i < sizeof hash; i++) hash[i] = (uint8_t)(0x10 + i);

    yvr_need_t in = {
        .protocol_version = 1,
        .tool_hash = hash, .tool_hash_len = sizeof hash,
    };
    uint8_t buf[64];
    size_t  n = 0;
    EXP_OK(yvr_need_encode(&in, buf, sizeof buf, &n));

    yvr_need_t out;
    EXP_OK(yvr_need_decode(buf, n, &out));
    EXPECT(out.protocol_version == 1, rc);
    EXPECT(out.tool_hash_len == sizeof hash, rc + 1);
    EXPECT(memcmp(out.tool_hash, hash, sizeof hash) == 0, rc + 2);
    return 0;
}

/* ── MODULE ─────────────────────────────────────────────────── */

static int test_module_body_roundtrip(void)
{
    int rc = 1100;
    uint8_t wasm[256];
    uint8_t desc[64];
    for (size_t i = 0; i < sizeof wasm; i++) wasm[i] = (uint8_t)(i & 0xFF);
    for (size_t i = 0; i < sizeof desc; i++) desc[i] = (uint8_t)(0x40 + i);

    yvr_module_body_t in = {
        .protocol_version = 1,
        .wasm = wasm, .wasm_len = sizeof wasm,
        .descriptor = desc, .descriptor_len = sizeof desc,
    };
    uint8_t buf[512];
    size_t  n = 0;
    EXP_OK(yvr_module_body_encode(&in, buf, sizeof buf, &n));

    yvr_module_body_t out;
    EXP_OK(yvr_module_body_decode(buf, n, &out));
    EXPECT(out.wasm_len == sizeof wasm, rc);
    EXPECT(memcmp(out.wasm, wasm, sizeof wasm) == 0, rc + 1);
    EXPECT(out.descriptor_len == sizeof desc, rc + 2);
    EXPECT(memcmp(out.descriptor, desc, sizeof desc) == 0, rc + 3);
    return 0;
}

static int test_module_body_rejects_missing(void)
{
    int rc = 1110;
    /* Map missing wasm field. */
    static const uint8_t in[] = {
        0xa2,
        0x61, 'v',                                              0x01,
        0x6a, 'd','e','s','c','r','i','p','t','o','r',          0x41, 0x42,
    };
    yvr_module_body_t out;
    EXPECT(yvr_module_body_decode(in, sizeof in, &out) == YVR_E_BAD_FRAME, rc);
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
        { "auth_roundtrip",                  test_auth_roundtrip                 },
        { "auth_rejects_missing_nonce",      test_auth_rejects_missing_nonce     },
        { "authrsp_roundtrip",               test_authrsp_roundtrip              },
        { "attest_roundtrip",                test_attest_roundtrip               },
        { "attest_capabilities_overflow",    test_attest_capabilities_overflow   },
        { "error_roundtrip_full",            test_error_roundtrip_full           },
        { "error_roundtrip_minimal",         test_error_roundtrip_minimal        },
        { "invoke_roundtrip_minimal",        test_invoke_roundtrip_minimal       },
        { "invoke_roundtrip_with_approval",  test_invoke_roundtrip_with_approval },
        { "invoke_skips_unknown",            test_invoke_skips_unknown           },
        { "tool_rsp_roundtrip_ok",           test_tool_rsp_roundtrip_ok          },
        { "tool_rsp_roundtrip_error",        test_tool_rsp_roundtrip_error       },
        { "stream_chunk_roundtrip",          test_stream_chunk_roundtrip         },
        { "stream_chunk_end",                test_stream_chunk_end               },
        { "need_roundtrip",                  test_need_roundtrip                 },
        { "module_body_roundtrip",           test_module_body_roundtrip          },
        { "module_body_rejects_missing",     test_module_body_rejects_missing    },
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
