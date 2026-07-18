/**
 * Pure classification of direct-connect probe failures. No React Native
 * imports, so it is directly unit-testable (`npx tsx src/lib/directProbeFailure.test.ts`).
 *
 * Extracted from quic.ts for the same reason probeTargets.ts was extracted from
 * deviceStatus.ts: the rule is subtle, it was wrong in production, and importing
 * quic.ts drags in React Native so the test could not run at all.
 */

/** Plain-language cause for a failed direct-connect probe.
 *
 *  Exists because the three failures below are indistinguishable in the UI but
 *  need OPPOSITE responses, and conflating them cost us a long-standing "my
 *  phone won't reach my Mac" bug:
 *
 *   • blocked   — the OS refused before any packet left the phone. Retrying is
 *                 pointless; the leg can never work until config changes.
 *   • timeout   — plausibly reachable, just not right now. Retrying is correct.
 *   • refused   — host is up, nothing listening on that port. Agent is down.
 *
 *  The blocked case is the subtle one: iOS ATS rejects cleartext http:// with
 *  NSURLErrorAppTransportSecurityRequiresSecureConnection (-1022), and Android
 *  release builds reject it via the cleartext policy. Both surface as a generic
 *  fetch rejection, so a permanently-impossible leg looked exactly like a box
 *  that was merely asleep — and the reconnect ladder retried it forever. */
export function describeDirectProbeFailure(e: any): string {
  const msg = String(e?.message ?? e ?? "");
  const name = String(e?.name ?? "");
  const code = e?.code ?? e?.userInfo?.NSURLErrorFailingURLStringErrorKey ?? "";
  if (/-1022|App ?Transport ?Security|cleartext|CLEARTEXT/i.test(msg + code)) {
    return "blocked by the OS before leaving the phone (cleartext HTTP not permitted to this address) — this leg cannot succeed until app transport config allows it";
  }
  if (name === "AbortError" || /abort|timed? ?out/i.test(msg)) {
    return "timed out (no answer within the probe budget)";
  }
  if (/refused|ECONNREFUSED/i.test(msg)) {
    return "connection refused (host reachable, agent not listening on this port)";
  }
  if (/unreachable|ENETUNREACH|EHOSTUNREACH/i.test(msg)) {
    return "network unreachable (no route from this phone to that address)";
  }
  return msg || "unknown error";
}

