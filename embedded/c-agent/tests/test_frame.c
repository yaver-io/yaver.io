/* Yaver c-agent — frame header codec tests.
 *
 * Hand-rolled minimal test runner so this builds without a test
 * framework dependency. Each test returns 0 on pass, non-zero on
 * fail (the value is the unique failure tag, useful when only the
 * exit code is visible).
 */

#include "yvr/frame.h"
#include "yvr/status.h"

#include <stddef.h>
#include <stdint.h>
#include <stdio.h>
#include <string.h>

static int test_roundtrip(void)
{
    yvr_frame_header_t in = {
        .length    = 0x123456u,
        .type      = (uint8_t)YVR_FRAME_HELLO,
        .flags     = (uint8_t)(YVR_FLAG_END_STREAM | YVR_FLAG_ACK),
        .stream_id = 0xDEADBEEFu,
    };
    uint8_t buf[YVR_FRAME_HEADER_SIZE];
    yvr_status_t s = yvr_frame_header_encode(&in, buf, sizeof(buf));
    if (s != YVR_OK) return 1;

    yvr_frame_header_t out;
    s = yvr_frame_header_decode(buf, sizeof(buf), &out);
    if (s != YVR_OK) return 2;

    if (out.length    != in.length)    return 3;
    if (out.type      != in.type)      return 4;
    if (out.flags     != in.flags)     return 5;
    if (out.stream_id != in.stream_id) return 6;
    return 0;
}

static int test_known_bytes(void)
{
    /* Locks down the wire format so a refactor that flips byte order
     * or field offsets fails loudly. The brain-side encoder (Go) must
     * produce these exact bytes for the same inputs. */
    yvr_frame_header_t in = {
        .length    = 0x000007u,
        .type      = 0x01u,
        .flags     = 0x02u,
        .stream_id = 0x00000003u,
    };
    const uint8_t expected[YVR_FRAME_HEADER_SIZE] = {
        0x00, 0x00, 0x07,        /* length (24-bit BE) */
        0x01,                    /* type */
        0x02,                    /* flags */
        0x00, 0x00, 0x00, 0x03,  /* stream_id (32-bit BE) */
    };
    uint8_t buf[YVR_FRAME_HEADER_SIZE];
    if (yvr_frame_header_encode(&in, buf, sizeof(buf)) != YVR_OK) return 10;
    if (memcmp(buf, expected, sizeof(expected)) != 0) return 11;
    return 0;
}

static int test_max_values(void)
{
    yvr_frame_header_t in = {
        .length    = YVR_FRAME_MAX_PAYLOAD,
        .type      = 0xFFu,
        .flags     = 0xFFu,
        .stream_id = 0xFFFFFFFFu,
    };
    uint8_t buf[YVR_FRAME_HEADER_SIZE];
    if (yvr_frame_header_encode(&in, buf, sizeof(buf)) != YVR_OK) return 20;
    yvr_frame_header_t out;
    if (yvr_frame_header_decode(buf, sizeof(buf), &out) != YVR_OK) return 21;
    if (out.length    != in.length)    return 22;
    if (out.stream_id != in.stream_id) return 23;
    return 0;
}

static int test_payload_too_large(void)
{
    yvr_frame_header_t in = { .length = YVR_FRAME_MAX_PAYLOAD + 1u };
    uint8_t buf[YVR_FRAME_HEADER_SIZE];
    if (yvr_frame_header_encode(&in, buf, sizeof(buf)) != YVR_E_PAYLOAD_TOO_LARGE) {
        return 30;
    }
    return 0;
}

static int test_buffer_too_small(void)
{
    yvr_frame_header_t in = {0};
    uint8_t buf[YVR_FRAME_HEADER_SIZE - 1];
    if (yvr_frame_header_encode(&in, buf, sizeof(buf)) != YVR_E_BUFFER_TOO_SMALL) {
        return 40;
    }
    return 0;
}

static int test_truncated(void)
{
    const uint8_t buf[YVR_FRAME_HEADER_SIZE - 1] = {0};
    yvr_frame_header_t out;
    if (yvr_frame_header_decode(buf, sizeof(buf), &out) != YVR_E_TRUNCATED) {
        return 50;
    }
    return 0;
}

static int test_null_args(void)
{
    uint8_t buf[YVR_FRAME_HEADER_SIZE] = {0};
    yvr_frame_header_t hdr = {0};
    if (yvr_frame_header_encode(NULL, buf, sizeof(buf)) != YVR_E_INVALID_ARG) return 60;
    if (yvr_frame_header_encode(&hdr, NULL, sizeof(buf)) != YVR_E_INVALID_ARG) return 61;
    if (yvr_frame_header_decode(NULL, sizeof(buf), &hdr) != YVR_E_INVALID_ARG) return 62;
    if (yvr_frame_header_decode(buf, sizeof(buf), NULL)  != YVR_E_INVALID_ARG) return 63;
    return 0;
}

static int test_status_str(void)
{
    /* yvr_status_str must never return NULL and must produce
     * stable, printable labels — log lines depend on it. */
    if (yvr_status_str(YVR_OK)            == NULL) return 70;
    if (yvr_status_str(YVR_E_INVALID_ARG) == NULL) return 71;
    if (yvr_status_str((yvr_status_t)999) == NULL) return 72;
    return 0;
}

typedef int (*test_fn_t)(void);
struct test_case {
    const char *name;
    test_fn_t   fn;
};

int main(void)
{
    static const struct test_case tests[] = {
        {"roundtrip",         test_roundtrip},
        {"known_bytes",       test_known_bytes},
        {"max_values",        test_max_values},
        {"payload_too_large", test_payload_too_large},
        {"buffer_too_small",  test_buffer_too_small},
        {"truncated",         test_truncated},
        {"null_args",         test_null_args},
        {"status_str",        test_status_str},
    };
    const size_t n = sizeof(tests) / sizeof(tests[0]);
    int failed = 0;
    for (size_t i = 0; i < n; i++) {
        int rc = tests[i].fn();
        if (rc == 0) {
            printf("PASS  %s\n", tests[i].name);
        } else {
            printf("FAIL  %s (rc=%d)\n", tests[i].name, rc);
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
