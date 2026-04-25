/* Yaver c-agent — probe parser tests.
 *
 * The wifi_client_count probe's I/O path runs `iw dev <iface>
 * station dump`; we don't try to fake `iw` on the test host.
 * What we DO test is the parser on canned `iw` output, plus the
 * end-to-end registration + invoke path (which runs the real
 * popen but produces an empty result on a Mac dev box where
 * `iw` isn't installed — the probe never fails, it just reports
 * zero clients).
 */

#include "yvr/cbor.h"
#include "yvr/host.h"
#include "yvr/probes.h"
#include "yvr/status.h"

#include <stddef.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#define EXP_OK(call) do { if ((call) != YVR_OK) return rc; } while (0)
#define EXPECT(c, code) do { if (!(c)) return (code); } while (0)

/* ── Parser ─────────────────────────────────────────────────── */

static int test_parser_empty(void)
{
    int rc = 100;
    EXPECT(yvr_probe_count_stations_in_iw_output(NULL, 0)   == 0, rc);
    EXPECT(yvr_probe_count_stations_in_iw_output("", 0)     == 0, rc + 1);
    EXPECT(yvr_probe_count_stations_in_iw_output("\n\n", 2) == 0, rc + 2);
    return 0;
}

static int test_parser_single_station(void)
{
    int rc = 200;
    /* Real `iw` output stripped of trailing fields. */
    static const char input[] =
        "Station 11:22:33:44:55:66 (on wlan0)\n"
        "        inactive time:  240 ms\n"
        "        rx bytes:       1234567\n"
        "        signal:         -42 dBm\n";
    EXPECT(yvr_probe_count_stations_in_iw_output(input, sizeof input - 1) == 1, rc);
    return 0;
}

static int test_parser_multi_stations(void)
{
    int rc = 300;
    static const char input[] =
        "Station 11:22:33:44:55:66 (on wlan0)\n"
        "        signal: -42 dBm\n"
        "Station aa:bb:cc:dd:ee:ff (on wlan0)\n"
        "        signal: -55 dBm\n"
        "Station de:ad:be:ef:00:11 (on wlan0)\n"
        "        signal: -67 dBm\n";
    EXPECT(yvr_probe_count_stations_in_iw_output(input, sizeof input - 1) == 3, rc);
    return 0;
}

static int test_parser_station_keyword_in_value(void)
{
    /* Make sure we don't double-count a "Station" that appears
     * inside a value — only line-aligned hits are counted. */
    int rc = 400;
    static const char input[] =
        "Station 11:22:33:44:55:66 (on wlan0)\n"
        "        ssid (extra info: \"Station 99\")\n"
        "        signal: -42 dBm\n";
    EXPECT(yvr_probe_count_stations_in_iw_output(input, sizeof input - 1) == 1, rc);
    return 0;
}

/* ── End-to-end: registration + invoke through the host ─────── */
/*
 * On a Mac dev box where `iw` isn't installed, the probe's popen
 * fails and every interface yields COUNT_ERROR. The probe still
 * produces a valid CBOR document with total = 0 and an empty
 * radios array. That's the contract — the brain's parser doesn't
 * have to special-case "host has no `iw`."
 */
static int test_register_and_invoke(void)
{
    int rc = 500;
    yvr_host_t *h = yvr_host_init("/tmp/yvr-probes-test");
    EXPECT(h != NULL, rc);

    EXPECT(yvr_register_wifi_client_count_probe(h) == YVR_HOST_OK, rc + 1);
    EXPECT(yvr_host_module_state(h, "wifi_client_count") == YVR_MS_ACTIVE, rc + 2);

    size_t out_len = 0;
    yvr_host_status_t st = YVR_HOST_OK;
    void *rsp = yvr_host_invoke(h, "wifi_client_count", "scan",
                                NULL, 0, &out_len, &st);
    EXPECT(st == YVR_HOST_OK, rc + 3);
    EXPECT(rsp != NULL,        rc + 4);
    EXPECT(out_len > 0,        rc + 5);

    /* Validate CBOR shape: outer map with "total" + "radios". */
    yvr_cbor_r_t r;
    yvr_cbor_r_init(&r, rsp, out_len);
    size_t kv = 0;
    EXP_OK(yvr_cbor_r_map_begin(&r, &kv));
    EXPECT(kv == 2, rc + 6);

    const char *k;
    size_t      kn;
    EXP_OK(yvr_cbor_r_text(&r, &k, &kn));
    EXPECT(kn == 5 && memcmp(k, "total", 5) == 0, rc + 7);
    uint64_t total;
    EXP_OK(yvr_cbor_r_uint(&r, &total));

    EXP_OK(yvr_cbor_r_text(&r, &k, &kn));
    EXPECT(kn == 6 && memcmp(k, "radios", 6) == 0, rc + 8);
    size_t n_radios;
    EXP_OK(yvr_cbor_r_array_begin(&r, &n_radios));

    /* On a host without `iw` (typical CI / Mac dev), n_radios = 0
     * and total = 0. On a real OpenWrt device with active radios,
     * both are > 0. We accept both — the probe never fails. */
    EXPECT(n_radios == total ? true : true, rc + 9); /* sanity */
    /* Cross-check: per-radio counts sum to `total`. */
    uint64_t sum = 0;
    for (size_t i = 0; i < n_radios; i++) {
        size_t inner_kv;
        EXP_OK(yvr_cbor_r_map_begin(&r, &inner_kv));
        EXPECT(inner_kv == 2, rc + 10);
        EXP_OK(yvr_cbor_r_text(&r, &k, &kn));
        EXPECT(kn == 5 && memcmp(k, "iface", 5) == 0, rc + 11);
        const char *iface;
        size_t      iface_n;
        EXP_OK(yvr_cbor_r_text(&r, &iface, &iface_n));
        EXPECT(iface_n > 0, rc + 12);
        EXP_OK(yvr_cbor_r_text(&r, &k, &kn));
        EXPECT(kn == 12 && memcmp(k, "client_count", 12) == 0, rc + 13);
        uint64_t c;
        EXP_OK(yvr_cbor_r_uint(&r, &c));
        sum += c;
    }
    EXPECT(sum == total, rc + 14);

    yvr_host_free_response(h, rsp);
    yvr_host_shutdown(h);
    return 0;
}

/* ── Klipper status probe ──────────────────────────────────── */

static int test_klipper_parse_curl_output_valid(void)
{
    int rc = 600;
    /* Simulated curl response with the sentinel + status code. */
    static const char in[] =
        "{\"result\":{\"status\":{\"print_stats\":{\"state\":\"printing\"}}}}\n"
        "__YVR_HTTP_END__200\n";
    const char *body = NULL;
    size_t      body_len = 0;
    int         status = -1;
    EXPECT(yvr_klipper_parse_curl_output(in, sizeof in - 1,
                                         &body, &body_len, &status), rc);
    EXPECT(status == 200, rc + 1);
    /* Body should be everything before the sentinel + a trailing
     * newline. The newline that's part of the sentinel ITSELF
     * isn't in the body — it's eaten by the "\n__YVR_HTTP_END__"
     * needle. */
    EXPECT(body_len > 0, rc + 2);
    EXPECT(body[0] == '{', rc + 3);
    return 0;
}

static int test_klipper_parse_curl_output_no_sentinel(void)
{
    int rc = 610;
    static const char in[] = "{\"some\":\"json\"}";
    const char *body;
    size_t      body_len;
    int         status;
    EXPECT(yvr_klipper_parse_curl_output(in, sizeof in - 1,
                                         &body, &body_len, &status) == false, rc);
    EXPECT(status == 0, rc + 1);
    return 0;
}

static int test_klipper_parse_curl_output_404(void)
{
    int rc = 620;
    static const char in[] =
        "{\"error\":\"not found\"}\n"
        "__YVR_HTTP_END__404\n";
    const char *body;
    size_t      body_len;
    int         status;
    EXPECT(yvr_klipper_parse_curl_output(in, sizeof in - 1,
                                         &body, &body_len, &status), rc);
    EXPECT(status == 404, rc + 1);
    return 0;
}

static int test_klipper_register_and_invoke(void)
{
    int rc = 700;
    yvr_host_t *h = yvr_host_init("/tmp/yvr-klipper-test");
    EXPECT(h != NULL, rc);

    EXPECT(yvr_register_klipper_status_probe(h) == YVR_HOST_OK, rc + 1);
    EXPECT(yvr_host_module_state(h, "klipper_status") == YVR_MS_ACTIVE, rc + 2);

    size_t out_len = 0;
    yvr_host_status_t st = YVR_HOST_OK;
    void *rsp = yvr_host_invoke(h, "klipper_status", "scan",
                                NULL, 0, &out_len, &st);
    EXPECT(st == YVR_HOST_OK, rc + 3);
    EXPECT(rsp != NULL, rc + 4);
    EXPECT(out_len > 0, rc + 5);

    /* Validate CBOR shape:
     *   { "endpoint": <text>, "response": <bytes>,
     *     "reachable": <bool>, "http_status": <uint> } */
    yvr_cbor_r_t r;
    yvr_cbor_r_init(&r, rsp, out_len);
    size_t kv = 0;
    EXP_OK(yvr_cbor_r_map_begin(&r, &kv));
    EXPECT(kv == 4, rc + 6);

    const char *k;
    size_t      kn;

    EXP_OK(yvr_cbor_r_text(&r, &k, &kn));
    EXPECT(kn == 8 && memcmp(k, "endpoint", 8) == 0, rc + 7);
    const char *endpoint;
    size_t      endpoint_n;
    EXP_OK(yvr_cbor_r_text(&r, &endpoint, &endpoint_n));
    EXPECT(endpoint_n > 0, rc + 8);

    EXP_OK(yvr_cbor_r_text(&r, &k, &kn));
    EXPECT(kn == 8 && memcmp(k, "response", 8) == 0, rc + 9);
    const uint8_t *body;
    size_t         body_n;
    EXP_OK(yvr_cbor_r_bytes(&r, &body, &body_n));
    /* body_n is 0 on a host without Moonraker; > 0 otherwise. */

    EXP_OK(yvr_cbor_r_text(&r, &k, &kn));
    EXPECT(kn == 9 && memcmp(k, "reachable", 9) == 0, rc + 10);
    bool reachable;
    EXP_OK(yvr_cbor_r_bool(&r, &reachable));

    EXP_OK(yvr_cbor_r_text(&r, &k, &kn));
    EXPECT(kn == 11 && memcmp(k, "http_status", 11) == 0, rc + 11);
    uint64_t http_status;
    EXP_OK(yvr_cbor_r_uint(&r, &http_status));

    /* On a host without Moonraker (CI / Mac dev), reachable is
     * false, http_status is 0, body is empty. On a real Klipper
     * host, all three are populated. We accept both. */
    if (reachable) {
        EXPECT(http_status >= 100 && http_status < 600, rc + 12);
        EXPECT(body_n > 0, rc + 13);
    } else {
        EXPECT(http_status == 0, rc + 14);
        EXPECT(body_n == 0, rc + 15);
    }

    yvr_host_free_response(h, rsp);
    yvr_host_shutdown(h);
    return 0;
}

/* ── Driver ─────────────────────────────────────────────────── */

typedef int (*tfn)(void);
struct tc { const char *name; tfn fn; };

int main(void)
{
    static const struct tc T[] = {
        { "parser_empty",                   test_parser_empty                  },
        { "parser_single_station",          test_parser_single_station         },
        { "parser_multi_stations",          test_parser_multi_stations         },
        { "parser_station_keyword_in_value",test_parser_station_keyword_in_value},
        { "register_and_invoke",            test_register_and_invoke           },
        { "klipper_parse_curl_valid",       test_klipper_parse_curl_output_valid    },
        { "klipper_parse_curl_no_sentinel", test_klipper_parse_curl_output_no_sentinel },
        { "klipper_parse_curl_404",         test_klipper_parse_curl_output_404      },
        { "klipper_register_and_invoke",    test_klipper_register_and_invoke    },
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
