/* Yaver c-agent — klipper_status probe.
 *
 * Answers "what's my Klipper printer doing right now?" by
 * querying Moonraker's REST API (http://127.0.0.1:7125 by
 * default) for a bundle of useful printer objects, and
 * returning the raw JSON response in a CBOR envelope.
 *
 * Why curl + popen instead of a hand-rolled HTTP client:
 *   - curl ships on every Klipper SBC distribution we care
 *     about (Raspberry Pi OS, Mainsail OS, FluiddPi)
 *   - HTTP/1.1 is annoying to write defensively in C
 *   - Moonraker only listens on 127.0.0.1 in the default
 *     install — local-only TCP, no security headache
 *
 * Why raw JSON instead of parsing it on the device:
 *   - JSON parsing in C is large + error-prone
 *   - The brain is already JSON-fluent (Go's encoding/json)
 *   - The probe stays small and the same wire shape works
 *     for every future Moonraker query — we never have to
 *     re-ship the device when the brain wants new fields
 *
 * Portable: on hosts without curl, popen returns a bad fd or
 * pclose returns nonzero. The probe still produces a valid
 * CBOR document — `reachable: false`, `http_status: 0`,
 * `response: <empty>`. The brain's downstream parser doesn't
 * have to special-case unsupported platforms.
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

#define MOONRAKER_DEFAULT_HOST "127.0.0.1"
#define MOONRAKER_DEFAULT_PORT 7125

/* The default query bundles the most operator-useful objects.
 * Everything else (history, files, system_info) gets its own
 * probe in a follow-up. */
#define MOONRAKER_DEFAULT_PATH \
    "/printer/objects/query?print_stats&toolhead&extruder&heater_bed&virtual_sdcard"

/* Sentinel curl appends after the response body via -w. Chosen so
 * it can't appear in any plausible JSON response. The parser
 * splits on the LAST occurrence so a malicious server can't
 * truncate the body by including the sentinel in its output. */
#define HTTP_END_SENTINEL "__YVR_HTTP_END__"

bool yvr_klipper_parse_curl_output(const char  *output,
                                   size_t       output_len,
                                   const char **out_body,
                                   size_t      *out_body_len,
                                   int         *out_status)
{
    if (output == NULL || out_body == NULL || out_body_len == NULL ||
        out_status == NULL) {
        return false;
    }
    *out_status = 0;

    static const char NEEDLE[] = "\n" HTTP_END_SENTINEL;
    const size_t NEEDLE_LEN = sizeof NEEDLE - 1;

    if (output_len < NEEDLE_LEN) {
        return false;
    }

    /* Find the LAST occurrence of NEEDLE in [0, output_len). */
    const char *hit = NULL;
    if (output_len >= NEEDLE_LEN) {
        for (size_t i = output_len - NEEDLE_LEN + 1; i > 0; i--) {
            const size_t pos = i - 1;
            if (memcmp(output + pos, NEEDLE, NEEDLE_LEN) == 0) {
                hit = output + pos;
                break;
            }
        }
    }
    if (hit == NULL) {
        return false;
    }

    /* Body is everything before the sentinel. */
    *out_body = output;
    *out_body_len = (size_t)(hit - output);

    /* Status is right after the sentinel; atoi stops at the next
     * non-digit (typically the trailing newline). */
    const char *status_str = hit + NEEDLE_LEN;
    size_t remaining = output_len - (size_t)(status_str - output);
    if (remaining == 0) {
        return false;
    }
    *out_status = atoi(status_str);
    return true;
}

/* Build the curl invocation. Returns 0 on snprintf overflow. */
static int build_curl_cmd(char *cmd, size_t cap,
                          const char *host, uint16_t port,
                          const char *path)
{
    int n = snprintf(cmd, cap,
                     "curl -s -w '\\n%s%%{http_code}\\n' "
                     "--max-time 5 "
                     "'http://%s:%u%s' 2>/dev/null",
                     HTTP_END_SENTINEL,
                     host, (unsigned)port, path);
    return (n > 0 && (size_t)n < cap) ? n : 0;
}

/* Runs curl, captures the raw output (body + sentinel + status)
 * into out_buf. On success, also splits into body + status:
 * out_buf is in-place rewritten to contain only the body bytes
 * (NUL-terminated for caller convenience), and out_status holds
 * the parsed HTTP code.
 *
 * Returns the body length in bytes. Zero means "unreachable" —
 * out_status is also 0 in that case. */
static size_t fetch_moonraker(const char *host, uint16_t port,
                              const char *path,
                              char *out_buf, size_t out_cap,
                              int *out_status)
{
    *out_status = 0;
    if (out_cap == 0) {
        return 0;
    }

    char cmd[768];
    if (build_curl_cmd(cmd, sizeof cmd, host, port, path) == 0) {
        return 0;
    }

    FILE *fp = popen(cmd, "r");
    if (fp == NULL) {
        return 0;
    }

    size_t got = 0;
    char chunk[4096];
    while (fgets(chunk, sizeof chunk, fp) != NULL) {
        size_t cl = strlen(chunk);
        if (got + cl > out_cap - 1) {  /* leave 1 byte for NUL */
            cl = (out_cap - 1) - got;
        }
        if (cl == 0) {
            break;
        }
        memcpy(out_buf + got, chunk, cl);
        got += cl;
        if (got >= out_cap - 1) {
            break;
        }
    }
    int rc = pclose(fp);

    if (rc != 0) {
        return 0;
    }

    const char *body = NULL;
    size_t      body_len = 0;
    int         status = 0;
    if (!yvr_klipper_parse_curl_output(out_buf, got, &body, &body_len, &status)) {
        return 0;
    }

    /* Move body to the start of the buffer + NUL-terminate. */
    if (body != out_buf && body_len > 0) {
        memmove(out_buf, body, body_len);
    }
    out_buf[body_len] = '\0';
    *out_status = status;
    return body_len;
}

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

    /* HTTP response buffer. Klipper status responses are typically
     * 1–4 KB; cap generously at 64 KB so a misbehaving Moonraker
     * can't OOM the agent. */
    enum { HTTP_CAP = 64 * 1024 };
    char *http_buf = malloc(HTTP_CAP);
    if (http_buf == NULL) {
        if (out_response_len != NULL) *out_response_len = 0;
        return NULL;
    }

    int    http_status = 0;
    size_t body_len = fetch_moonraker(MOONRAKER_DEFAULT_HOST,
                                      MOONRAKER_DEFAULT_PORT,
                                      MOONRAKER_DEFAULT_PATH,
                                      http_buf, HTTP_CAP, &http_status);

    bool reachable = (http_status > 0);

    /* CBOR envelope. Map size = 4: endpoint + response +
     * reachable + http_status. */
    enum { OUT_CAP = 80 * 1024 };
    uint8_t *out = malloc(OUT_CAP);
    if (out == NULL) {
        free(http_buf);
        if (out_response_len != NULL) *out_response_len = 0;
        return NULL;
    }

    yvr_cbor_w_t w;
    yvr_cbor_w_init(&w, out, OUT_CAP);

    /* CTAP2 deterministic key order:
     *   "endpoint"    head 0x68 (length 8)
     *   "response"    head 0x68 (length 8)  — same first byte;
     *                                         second byte 'r' < 'e'
     *                                         no — 'e' (0x65) < 'r' (0x72)
     *                                         so endpoint < response
     *   "reachable"   head 0x69 (length 9)
     *   "http_status" head 0x6b (length 11)
     */
    yvr_cbor_w_map_begin(&w, 4);

    yvr_cbor_w_text(&w, "endpoint", 8);
    yvr_cbor_w_text(&w, MOONRAKER_DEFAULT_PATH,
                    strlen(MOONRAKER_DEFAULT_PATH));

    yvr_cbor_w_text(&w, "response", 8);
    yvr_cbor_w_bytes(&w, (const uint8_t *)http_buf, body_len);

    yvr_cbor_w_text(&w, "reachable", 9);
    yvr_cbor_w_bool(&w, reachable);

    yvr_cbor_w_text(&w, "http_status", 11);
    yvr_cbor_w_uint(&w, (uint64_t)http_status);

    free(http_buf);

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

static yvr_module_vtable_t KLIPPER_STATUS_VTABLE = {
    .abi_version = YVR_MODULE_ABI_VERSION,
    .name        = "klipper_status",
    .version     = "0.0.1",
    .invoke      = probe_invoke,
};

yvr_host_status_t yvr_register_klipper_status_probe(yvr_host_t *host)
{
    return yvr_host_register_native(host,
                                    "klipper_status",
                                    &KLIPPER_STATUS_VTABLE);
}
