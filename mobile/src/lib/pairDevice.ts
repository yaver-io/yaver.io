// pairDevice.ts — submit this phone's auth token to a headless
// target that's running `yaver auth pair`.
//
// Flow:
//   1. Target prints a 6-char passkey and its reachable URLs.
//   2. User opens Yaver mobile → More → Pair a device.
//   3. User enters the passkey + target URL.
//   4. Mobile POSTs {token, convexSiteUrl, userId} to
//      {targetUrl}/auth/pair/submit?code=XXXXXX.
//   5. Target saves the token and goes online.
//
// The passkey is the secret — the endpoint is unauthenticated
// on purpose while a pairing session is open.

import { getConvexSiteUrl } from "./auth";

export interface PairSubmitArgs {
  code: string;
  targetUrl: string;
  token: string;
  userId?: string;
}

export interface PairSubmitResult {
  ok: boolean;
  host?: string;
  error?: string;
}

function normalizeTargetUrl(input: string): string {
  let url = input.trim();
  if (!url) return "";
  if (!/^https?:\/\//i.test(url)) {
    url = "http://" + url;
  }
  // If the user typed a bare host / IP with no port, default to
  // the agent port so they don't have to remember it.
  try {
    const parsed = new URL(url);
    if (!parsed.port && parsed.protocol === "http:") {
      parsed.port = "18080";
    }
    return parsed.toString().replace(/\/+$/, "");
  } catch {
    return "";
  }
}

function normalizeCode(input: string): string {
  return input.trim().toUpperCase().replace(/[^A-Z0-9]/g, "");
}

// PairURLPayload is the parsed contents of a canonical pair URL
// (https://yaver.io/pair?sid=…). Mirrors the agent-side buildPairURL
// in desktop/agent/pair_url.go. Every field except sid is optional —
// the URL is purely a locator.
export interface PairURLPayload {
  sid: string;
  mode: "pair" | "bootstrap" | "recovery" | string;
  host?: string;
  target?: string;
  exp?: number; // unix seconds
  code?: string; // optional fallback passkey
}

const PAIR_PATHS = ["/pair", "/auth/pair"];

/**
 * parsePairUrl recognises:
 *   - https://yaver.io/pair?...           (canonical hosted)
 *   - https://<self-hosted>/pair?...      (custom WebBaseURL)
 *   - yaver://pair?...                    (deep-link variant)
 *
 * Returns null when the URL is not a pair URL, so callers can plug it
 * into a generic deep-link handler without an extra type check.
 *
 * The QR/URL layer is purely additive — the existing manual passkey +
 * `yaver auth send` flow never goes through this function, so an
 * unknown shape just falls back to "ignore, this isn't ours".
 */
export function parsePairUrl(input: string): PairURLPayload | null {
  if (!input) return null;
  const raw = input.trim();
  if (!raw) return null;
  let parsed: URL;
  try {
    // The URL constructor needs a scheme; bare `pair?sid=…` is
    // intentionally not accepted to avoid mis-routing arbitrary
    // pasted strings.
    parsed = new URL(raw);
  } catch {
    return null;
  }
  const proto = parsed.protocol.toLowerCase();
  const path = parsed.pathname || "/";
  const isHttp = proto === "http:" || proto === "https:";
  const isYaverDeepLink = proto === "yaver:";
  if (isHttp) {
    if (!PAIR_PATHS.some((p) => path === p || path.startsWith(p + "/"))) {
      return null;
    }
  } else if (isYaverDeepLink) {
    // yaver://pair?... — host is "pair", path can be empty.
    const host = (parsed.host || "").toLowerCase();
    if (host !== "pair") return null;
  } else {
    return null;
  }

  // sid is the only mandatory field; back-compat with `code` for any
  // older URL someone might have copied off a wiki.
  const sid = (parsed.searchParams.get("sid") || parsed.searchParams.get("code") || "").trim();
  if (!sid) return null;

  const expRaw = parsed.searchParams.get("exp");
  const expNum = expRaw ? Number(expRaw) : NaN;

  return {
    sid,
    mode: (parsed.searchParams.get("mode") || "pair").toLowerCase(),
    host: parsed.searchParams.get("host") || undefined,
    target: parsed.searchParams.get("target") || undefined,
    exp: Number.isFinite(expNum) ? expNum : undefined,
    code: parsed.searchParams.get("code") || undefined,
  };
}

// PairSessionMetadata mirrors the GET /auth/pair/session response from
// the agent. Used after a QR scan / deep link so the mobile UI can
// confirm the device summary before submitting a token.
export interface PairSessionMetadata {
  ok: boolean;
  sessionId?: string;
  hostname?: string;
  expiresAt?: string;
  canDirectSubmit?: boolean;
  targetUrls?: string[];
  error?: string;
}

/**
 * fetchPairSession calls the new /auth/pair/session endpoint on the
 * target. Older agents (pre-Slice-A) won't have it; in that case
 * callers should fall back to fetchPairInfo (which has been around
 * forever).
 */
export async function fetchPairSession(
  targetUrl: string,
  sid?: string,
): Promise<PairSessionMetadata> {
  const url = normalizeTargetUrl(targetUrl);
  if (!url) return { ok: false, error: "Invalid target URL" };
  let endpoint = `${url}/auth/pair/session`;
  if (sid) endpoint += `?sid=${encodeURIComponent(sid)}`;
  try {
    const res = await fetch(endpoint, { method: "GET" });
    if (res.status === 404) {
      return { ok: false, error: "Target is not in pairing mode (or has a different session)" };
    }
    if (!res.ok) {
      return { ok: false, error: `Target rejected pair-session (HTTP ${res.status})` };
    }
    const data = (await res.json()) as PairSessionMetadata;
    return { ...data, ok: true };
  } catch (e: any) {
    return { ok: false, error: e?.message ?? "Could not reach target" };
  }
}

/**
 * Check whether the target is currently in pairing mode.
 * Returns the host name + expiry if so.
 */
export async function fetchPairInfo(
  targetUrl: string,
): Promise<{ ok: boolean; host?: string; expiresAt?: string; error?: string }> {
  const url = normalizeTargetUrl(targetUrl);
  if (!url) return { ok: false, error: "Invalid target URL" };
  try {
    const res = await fetch(`${url}/auth/pair/info`, { method: "GET" });
    if (res.status === 404) {
      return { ok: false, error: "Target is not in pairing mode — run `yaver auth pair` there first" };
    }
    if (!res.ok) {
      return { ok: false, error: `Target rejected pair-info (HTTP ${res.status})` };
    }
    const data = (await res.json()) as { host?: string; expiresAt?: string };
    return { ok: true, host: data.host, expiresAt: data.expiresAt };
  } catch (e: any) {
    return { ok: false, error: e?.message ?? "Could not reach target" };
  }
}

/**
 * Submit this phone's auth token to the target device.
 */
export async function submitPair(args: PairSubmitArgs): Promise<PairSubmitResult> {
  const code = normalizeCode(args.code);
  const url = normalizeTargetUrl(args.targetUrl);
  if (code.length !== 6) {
    return { ok: false, error: "Passkey must be 6 characters" };
  }
  if (!url) {
    return { ok: false, error: "Invalid target URL" };
  }
  if (!args.token) {
    return { ok: false, error: "Not signed in on this phone" };
  }
  try {
    const res = await fetch(`${url}/auth/pair/submit?code=${encodeURIComponent(code)}`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        token: args.token,
        convexSiteUrl: getConvexSiteUrl(),
        userId: args.userId ?? "",
      }),
    });
    if (!res.ok) {
      let msg = `Target rejected submit (HTTP ${res.status})`;
      try {
        const data = await res.json();
        if (data?.error) msg = data.error;
      } catch {}
      return { ok: false, error: msg };
    }
    const data = (await res.json()) as { host?: string };
    return { ok: true, host: data?.host };
  } catch (e: any) {
    return { ok: false, error: e?.message ?? "Network error" };
  }
}
