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
 *  Exists because the failures below are indistinguishable in the UI but
 *  need OPPOSITE responses, and conflating them cost us a long-standing "my
 *  phone won't reach my Mac" bug:
 *
 *   • blocked    — the OS refused before any packet left the phone. Retrying
 *                  is pointless; the leg can never work until config changes.
 *   • unroutable — no route exists from this phone's current network to that
 *                  address (audit §2, 2026-07-19). RN fetch of a Tailscale
 *                  100.x address from a phone that is not on the tailnet
 *                  surfaces as "Network request failed" INSTANTLY, not on
 *                  timeout. Structurally identical to blocked from the ladder's
 *                  point of view: negative-cache it and stop racing.
 *   • timeout    — plausibly reachable, just not right now. Retry is correct.
 *   • refused    — host is up, nothing listening on that port. Agent is down.
 *
 *  The blocked case: iOS ATS rejects cleartext http:// with
 *  NSURLErrorAppTransportSecurityRequiresSecureConnection (-1022), and Android
 *  release builds reject via the cleartext policy. Both surface as a generic
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
  // A bare "Network request failed" WITHOUT a timeout or refused signal is
  // what RN fetch produces on an unroutable destination: the OS immediately
  // returned ENETUNREACH-shaped state that RN normalises. Distinguish it here
  // so callers can negative-cache the leg per network.
  if (/Network request failed/i.test(msg)) {
    return "unroutable (no route from this phone to that address right now — negative-cached until the network changes)";
  }
  return msg || "unknown error";
}

/**
 * True when a probe failure means "there is no route from THIS network to
 * that address right now". Callers use this to negative-cache the leg for the
 * lifetime of the current network identity so the ladder stops racing legs
 * that never have a chance.
 *
 * Deliberately conservative: unknown errors are NOT unroutable. Over-claiming
 * would silence legs that might work after a transient dip; under-claiming
 * (the pre-2026-07-19 behaviour) let impossible legs run forever.
 */
export function isUnroutableFailure(e: any): boolean {
  const msg = String(e?.message ?? e ?? "");
  const name = String(e?.name ?? "");
  const code = e?.code ?? "";
  // Abort/timeout are transient — never negative-cache them.
  if (name === "AbortError" || /abort|timed? ?out/i.test(msg)) return false;
  // OS-blocked is a distinct category with a different remedy (config change).
  if (/-1022|App ?Transport ?Security|cleartext|CLEARTEXT/i.test(msg + code)) return false;
  // Genuine EHOSTUNREACH / ENETUNREACH.
  if (/unreachable|ENETUNREACH|EHOSTUNREACH/i.test(msg)) return true;
  // RN's generic "Network request failed" — an instant reject, not a timeout.
  if (/Network request failed/i.test(msg)) return true;
  return false;
}

