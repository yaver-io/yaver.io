/* Yaver c-agent — wifi_client_count probe.
 *
 * Answers "how many Wi-Fi clients are connected to this device,
 * per radio?" by reading the kernel's nl80211 station table via
 * the `iw dev <iface> station dump` command.
 *
 * popen() is used instead of libnl + nl80211 directly because:
 *   - libnl3-genl adds ~200 KB of code for one syscall pattern
 *   - the `iw` binary ships in nearly every OpenWrt build today
 *   - the parsing target is well-defined and stable across
 *     `iw` versions
 *
 * Phase 1+ replaces this with a proper nl80211 client when libnl
 * is already linked for other capabilities. Until then, the
 * popen path is the simplest thing that demonstrates the loop
 * end-to-end.
 *
 * Portable: on platforms without `iw` (macOS dev boxes, embedded
 * RTOS targets), popen() simply returns no stations and the
 * probe reports zero clients. The parser is platform-independent
 * and can be unit-tested anywhere.
 */

#include "yvr/probes.h"

#include "yvr/cbor.h"
#include "yvr/host.h"
#include "yvr/module.h"

#include <stdbool.h>
#include <stddef.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

/* Candidate interface names to probe. Customers with non-default
 * naming (mt76's `phy0-ap0`, hostapd-managed `ath0`) get an
 * empty result for missing names; future versions can read the
 * list from /sys/class/net or from a vendor manifest. */
static const char *const CANDIDATE_IFACES[] = {
    "wlan0", "wlan1", "wlan2", "wlan3",
};
#define CANDIDATE_IFACES_N \
    (sizeof CANDIDATE_IFACES / sizeof CANDIDATE_IFACES[0])

/* ── Parser ─────────────────────────────────────────────────── */

size_t yvr_probe_count_stations_in_iw_output(const char *output, size_t len)
{
    if (output == NULL || len == 0) {
        return 0;
    }
    /* Count occurrences of "Station " that appear at the start
     * of a line. Real `iw station dump` output looks like:
     *
     *   Station 11:22:33:44:55:66 (on wlan0)
     *           inactive time:    240 ms
     *           rx bytes:         1234567
     *           ...
     *   Station aa:bb:cc:dd:ee:ff (on wlan0)
     *           ...
     *
     * Anything else with a "Station " substring inside a value
     * (vendor element, SSID name, etc.) won't match because we
     * only count line-aligned hits. */
    static const char NEEDLE[] = "Station ";
    const size_t NEEDLE_LEN = sizeof NEEDLE - 1;

    size_t count = 0;
    bool at_line_start = true;

    for (size_t i = 0; i < len; i++) {
        if (at_line_start && i + NEEDLE_LEN <= len &&
            memcmp(output + i, NEEDLE, NEEDLE_LEN) == 0) {
            count++;
        }
        at_line_start = (output[i] == '\n');
    }
    return count;
}

/* ── I/O — runs `iw dev <iface> station dump` ─────────────────
 *
 * Returns the station count for the interface, or SIZE_MAX if
 * the command produced no usable output (interface absent, `iw`
 * not installed, permission denied). */
#define COUNT_ERROR ((size_t)-1)

static size_t count_stations_for_iface(const char *iface)
{
    char cmd[128];
    int n = snprintf(cmd, sizeof cmd,
                     "iw dev %s station dump 2>/dev/null", iface);
    if (n < 0 || (size_t)n >= sizeof cmd) {
        return COUNT_ERROR;
    }

    FILE *fp = popen(cmd, "r");
    if (fp == NULL) {
        return COUNT_ERROR;
    }

    /* Buffer caps the read at 64 KB — enough for thousands of
     * stations, but bounded so a runaway command can't OOM the
     * agent. */
    enum { BUF_CAP = 64 * 1024 };
    char  *buf  = malloc(BUF_CAP);
    if (buf == NULL) {
        pclose(fp);
        return COUNT_ERROR;
    }
    size_t got = 0;

    char chunk[2048];
    while (fgets(chunk, sizeof chunk, fp) != NULL) {
        size_t cl = strlen(chunk);
        if (got + cl > BUF_CAP) {
            cl = BUF_CAP - got;
        }
        if (cl == 0) {
            break;
        }
        memcpy(buf + got, chunk, cl);
        got += cl;
        if (got >= BUF_CAP) {
            break;
        }
    }
    int rc = pclose(fp);

    /* `iw` exits non-zero when the interface doesn't exist; we
     * don't treat that as an error in the probe sense — the
     * iface simply isn't present, count is undefined. The brain
     * sees an absence, not a probe failure. */
    if (rc != 0) {
        free(buf);
        return COUNT_ERROR;
    }
    size_t count = yvr_probe_count_stations_in_iw_output(buf, got);
    free(buf);
    return count;
}

/* ── Module vtable ──────────────────────────────────────────── */

static void *probe_invoke(yvr_module_ctx_t *ctx,
                          const char       *method,
                          const void       *request,
                          size_t            request_len,
                          size_t           *out_response_len)
{
    (void)ctx;
    (void)method;
    (void)request;
    (void)request_len;

    /* Phase 1: probe each candidate interface. */
    size_t counts[CANDIDATE_IFACES_N];
    size_t live      = 0;
    size_t total     = 0;
    for (size_t i = 0; i < CANDIDATE_IFACES_N; i++) {
        counts[i] = count_stations_for_iface(CANDIDATE_IFACES[i]);
        if (counts[i] != COUNT_ERROR) {
            live++;
            total += counts[i];
        }
    }

    /* Phase 2: encode CBOR. Buffer sized for ~100 ifaces × ~30 B
     * each + frame overhead — generous for any plausible AP. */
    enum { OUT_CAP = 4096 };
    uint8_t *out = malloc(OUT_CAP);
    if (out == NULL) {
        if (out_response_len != NULL) *out_response_len = 0;
        return NULL;
    }
    yvr_cbor_w_t w;
    yvr_cbor_w_init(&w, out, OUT_CAP);

    /* Outer map. CTAP2 deterministic order: "total" (head 0x65)
     * < "radios" (head 0x66). */
    yvr_cbor_w_map_begin(&w, 2);

    yvr_cbor_w_text(&w, "total", 5);
    yvr_cbor_w_uint(&w, (uint64_t)total);

    yvr_cbor_w_text(&w, "radios", 6);
    yvr_cbor_w_array_begin(&w, live);
    for (size_t i = 0; i < CANDIDATE_IFACES_N; i++) {
        if (counts[i] == COUNT_ERROR) {
            continue;
        }
        /* Inner map. CTAP2 order: "iface" (head 0x65) <
         * "client_count" (head 0x6c). */
        yvr_cbor_w_map_begin(&w, 2);
        yvr_cbor_w_text(&w, "iface", 5);
        yvr_cbor_w_text(&w, CANDIDATE_IFACES[i],
                        strlen(CANDIDATE_IFACES[i]));
        yvr_cbor_w_text(&w, "client_count", 12);
        yvr_cbor_w_uint(&w, (uint64_t)counts[i]);
    }

    if (yvr_cbor_w_status(&w) != YVR_OK) {
        free(out);
        if (out_response_len != NULL) *out_response_len = 0;
        return NULL;
    }
    if (out_response_len != NULL) {
        *out_response_len = yvr_cbor_w_len(&w);
    }
    return out;
}

static yvr_module_vtable_t WIFI_CLIENT_COUNT_VTABLE = {
    .abi_version = YVR_MODULE_ABI_VERSION,
    .name        = "wifi_client_count",
    .version     = "0.0.1",
    .invoke      = probe_invoke,
};

yvr_host_status_t yvr_register_wifi_client_count_probe(yvr_host_t *host)
{
    return yvr_host_register_native(host,
                                    "wifi_client_count",
                                    &WIFI_CLIENT_COUNT_VTABLE);
}
