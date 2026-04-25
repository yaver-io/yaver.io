/* Yaver c-agent — built-in probe registrars.
 *
 * Each function in this header registers one diagnostic probe as a
 * native module on a host. Vendors call the registrars in their
 * c-agent's main() to bake probes into the binary at compile time.
 * The brain invokes them by name through the standard
 * yvr_host_invoke() path; the device runs the C function in-process
 * and returns a CBOR-encoded result.
 *
 * Probes here are deliberately simple — they encode one well-known
 * piece of device state, return early on failure, and don't do
 * anything that could brick the host. They're the demo / reference
 * probes; richer probes ship as dynamic modules in Phase 1+ once
 * the wasm runtime is in.
 */

#ifndef YVR_PROBES_H
#define YVR_PROBES_H

#include <stdbool.h>
#include <stddef.h>

#include "yvr/host.h"

#ifdef __cplusplus
extern "C" {
#endif

/* Register the wifi_client_count probe.
 *
 *   method (ignored)
 *   request (ignored)
 *
 *   response (CBOR map):
 *     {
 *       "total":  <uint>          # sum across all interfaces
 *       "radios": [
 *         { "iface": <text>, "client_count": <uint> },
 *         ...
 *       ]
 *     }
 *
 * On Linux + OpenWrt: shells out to `iw dev <iface> station dump`
 * for each candidate interface ("wlan0", "wlan1", ...).
 *
 * On platforms without `iw` (macOS dev box, etc.): returns the
 * same shape with an empty radios array and total = 0. The probe
 * never fails — it always returns a valid CBOR document so the
 * brain's downstream parser doesn't have to special-case
 * unsupported platforms.
 */
yvr_host_status_t yvr_register_wifi_client_count_probe(yvr_host_t *host);

/* ── Parser, exposed for unit tests ─────────────────────────────
 *
 * Counts "Station " occurrences at line starts in the given iw
 * output buffer. Pure function — no I/O, no allocation, no global
 * state. Returns 0 on NULL / zero-length input. */
size_t yvr_probe_count_stations_in_iw_output(const char *output, size_t len);

/* Register the klipper_status probe.
 *
 *   method (ignored for v1)
 *   request (ignored)
 *
 *   response (CBOR map):
 *     {
 *       "endpoint":    <text>     # the Moonraker URL the probe hit
 *       "response":    <bytes>    # raw HTTP response body (JSON)
 *       "reachable":   <bool>     # whether the HTTP request succeeded
 *       "http_status": <uint>     # HTTP status code; 0 if unreachable
 *     }
 *
 * Shell-out via curl — works on every Klipper SBC (curl ships in
 * basically every distro). The brain parses the raw JSON in
 * `response`; this layer stays JSON-free.
 *
 * Portable: on hosts without curl or without Moonraker running,
 * the probe returns the same CBOR shape with `reachable: false`
 * and an empty `response` body. The probe never fails. */
yvr_host_status_t yvr_register_klipper_status_probe(yvr_host_t *host);

/* ── Parser, exposed for unit tests ─────────────────────────────
 *
 * Splits a curl response (with our sentinel suffix) into body +
 * HTTP status code. The sentinel is a literal "\n__YVR_HTTP_END__"
 * we append via curl's -w flag — this is what lets us avoid
 * parsing HTTP headers ourselves.
 *
 * Returns true on success. On false, *out_body and *out_body_len
 * are unchanged; *out_status is set to 0. */
bool yvr_klipper_parse_curl_output(const char  *output,
                                   size_t       output_len,
                                   const char **out_body,
                                   size_t      *out_body_len,
                                   int         *out_status);

#ifdef __cplusplus
}
#endif

#endif /* YVR_PROBES_H */
