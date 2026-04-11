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

import { CONVEX_SITE_URL } from "./constants";

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
        convexSiteUrl: CONVEX_SITE_URL,
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
