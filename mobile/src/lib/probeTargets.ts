/**
 * Pure target-selection for reachability probes. No React Native imports, so
 * it is directly unit-testable (`npx tsx src/lib/probeTargets.test.ts`).
 *
 * Extracted from deviceStatus.ts because the rule below is subtle, was wrong
 * in production, and is exactly the kind of thing that silently regresses.
 */

/**
 * True for an mDNS/Bonjour name (`Something-Mac-mini.local`, trailing dot
 * tolerated). Case-insensitive: Convex stores whatever the agent reported.
 */
export function isMdnsName(host?: string | null): boolean {
  return !!host && /\.local\.?$/i.test(host);
}

/**
 * True for an address that is only reachable from the local network —
 * RFC1918, CGNAT (100.64/10, which is also Tailscale), loopback, link-local,
 * and IPv6 ULA/loopback.
 */
export function isPrivateHost(host?: string | null): boolean {
  if (!host) return false;
  const h = host.trim().toLowerCase().replace(/^\[|\]$/g, "");
  if (h === "localhost" || h === "::1" || h.endsWith(".local")) return true;
  if (/^127\./.test(h)) return true;
  if (/^10\./.test(h)) return true;
  if (/^192\.168\./.test(h)) return true;
  if (/^172\.(1[6-9]|2\d|3[01])\./.test(h)) return true;
  if (/^169\.254\./.test(h)) return true;
  // 100.64.0.0/10 — CGNAT, and where Tailscale hands out 100.x addresses.
  if (/^100\.(6[4-9]|[7-9]\d|1[01]\d|12[0-7])\./.test(h)) return true;
  if (/^f[cd][0-9a-f]{2}:/.test(h)) return true;
  if (/^fe80:/.test(h)) return true;
  return false;
}

/**
 * May we attach the user's session bearer to a probe of this base URL?
 *
 * Only over TLS, or to an address that cannot leave the local network. The
 * direct legs below are PLAINTEXT http://, and for a Yaver-managed cloud box
 * `host` is a PUBLIC Hetzner address — so the old unconditional
 * `Authorization: Bearer <session token>` shipped the user's credential in
 * cleartext across the internet on every 8s probe. A probe is a reachability
 * check; it does not need the caller's identity to be worth that.
 */
export function isCredentialSafeBase(base: string): boolean {
  if (/^https:\/\//i.test(base)) return true;
  const m = /^https?:\/\/(\[[^\]]+\]|[^/:]+)/i.exec(base);
  return isPrivateHost(m?.[1]);
}

/**
 * Direct HTTP bases to probe for a device.
 *
 * `.local` hosts are deliberately EXCLUDED, for two independent reasons:
 *
 *  1. The real connector never dials them. `quic.ts raceDirectCandidates` only
 *     pushes the stored host when `isPrivateIP(host)` is true, so a `.local`
 *     name is not in the connect race at all. A probe leg that gates a connect
 *     must not test an address the connect would never use — it can only
 *     produce false negatives.
 *  2. On iOS, resolving `.local` requires Local Network permission, and before
 *     that permission is granted the lookup does not fail fast — it HANGS,
 *     consuming the entire probe budget.
 *
 * macOS agents report `<Name>-Mac-mini.local` as their hostname, so this was
 * the common case. `lanIps` still carries the routable addresses, and the relay
 * legs are added separately by the caller.
 */
export function buildDirectProbeTargets(args: {
  host?: string | null;
  port?: number | null;
  lanIps?: (string | null | undefined)[] | null;
}): string[] {
  const port = args.port || 18080;
  const hostLegs =
    args.host && !isMdnsName(args.host)
      ? [
          `http://${args.host}:${port}`,
          // Probe the agent's real HTTP port too — a stale/mismatched Convex
          // quicPort shouldn't hide a reachable box.
          `http://${args.host}:18080`,
        ]
      : [];
  const lanLegs = (args.lanIps || [])
    .filter((ip): ip is string => !!ip)
    .flatMap((ip) => [`http://${ip}:${port}`, `http://${ip}:18080`]);
  return Array.from(new Set([...hostLegs, ...lanLegs]));
}
