// deviceCodeApprove — phone-side one-tap approval of a remote box's
// `yaver auth` device code.
//
// The friction this removes: a remote/off-LAN dev box (SSH, cloud,
// laptop on another network) can't be silently adopted over the LAN
// beacon, so today it prints a device code + URL and the user has to
// open a BROWSER and sign in AGAIN. But the phone is already signed in.
// If the box's QR / URL routes into the app instead of the browser,
// the phone can authorize the box with one tap using its existing
// session token — no browser, no re-auth, no code typed.
//
// Same Convex HTTP contract the web approver
// (web/app/auth/device) uses, driven by the phone's bearer token:
//   - GET  /auth/device-code/info?user_code=ABCD-1234   (public — machine details)
//   - POST /auth/device-code/authorize                  (Bearer token + {userCode})
//
// authorizeDeviceCode (backend/convex/deviceCode.ts) derives the user
// from the bearer token, marks the code authorized, mints a 1-year
// session, and stashes the token for the box's poller to pick up. The
// box's `yaver auth` loop then finishes on its own within ~5s.
//
// fetch pattern mirrors src/lib/auth.ts::startAccountMerge (getConvexSiteUrl
// + Bearer) — there is no shared apiFetch helper in this app.

import { getConvexSiteUrlSync as getConvexSiteUrl } from "./backendConfig";

export interface DeviceCodeInfo {
  /** Hostname the box reported when it created the code. */
  machineName?: string;
  platform?: string;
  arch?: string;
  shell?: string;
  /** Unix ms when the code expires (codes are 15-min TTL). */
  expiresAt?: number;
  /** Some deployments echo the normalized code back. */
  userCode?: string;
}

/** Normalize a scanned/typed code to the canonical ABCD-1234 shape the
 *  backend stores. Accepts "abcd1234", "ABCD-1234", with stray spaces.
 *  A valid normalized code is 9 chars (4 + hyphen + 4). */
export function normalizeUserCode(raw: string): string {
  const s = (raw || "").trim().toUpperCase().replace(/[^A-Z0-9]/g, "");
  if (s.length === 8) return `${s.slice(0, 4)}-${s.slice(4)}`;
  return s;
}

/** Extract the device-code from a scanned URL or raw code string.
 *  Handles https://yaver.io/auth/device?code=ABCD-1234[&convex=...],
 *  yaver://auth/device?code=..., and a bare "ABCD-1234". Returns "" if
 *  nothing code-shaped is found. */
export function extractUserCode(input: string): string {
  const raw = (input || "").trim();
  if (!raw) return "";
  try {
    const u = new URL(raw);
    const q = u.searchParams.get("code");
    if (q) return normalizeUserCode(q);
  } catch {
    // not a URL — fall through to treat as a bare code
  }
  return normalizeUserCode(raw);
}

/** Fetch the waiting box's details so the approve screen can show
 *  "Approve sign-in on <machine>?" instead of an opaque code. Public
 *  endpoint — no token needed. Returns null on any failure (expired
 *  code, network) so the caller can show a clean "code not found".  */
export async function fetchDeviceCodeInfo(userCode: string): Promise<DeviceCodeInfo | null> {
  const code = normalizeUserCode(userCode);
  if (!code) return null;
  try {
    const res = await fetch(
      `${getConvexSiteUrl()}/auth/device-code/info?user_code=${encodeURIComponent(code)}`,
    );
    if (!res.ok) return null;
    return (await res.json()) as DeviceCodeInfo;
  } catch {
    return null;
  }
}

export interface ApproveResult {
  ok: boolean;
  error?: string;
}

/**
 * Authorize the box's device code using THIS phone's session token.
 * Mirrors the web approver's POST so the backend treats it identically.
 * `token` is the phone's bearer (from useAuth). On success the box's
 * own `yaver auth` poller finishes within ~5s.
 */
export async function approveDeviceCode(userCode: string, token: string): Promise<ApproveResult> {
  const code = normalizeUserCode(userCode);
  if (!code) return { ok: false, error: "That code looks malformed." };
  if (!token) return { ok: false, error: "Sign in on this phone first, then approve." };
  try {
    const res = await fetch(`${getConvexSiteUrl()}/auth/device-code/authorize`, {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        Authorization: `Bearer ${token}`,
      },
      body: JSON.stringify({ userCode: code, convexUrl: getConvexSiteUrl() }),
    });
    if (!res.ok) {
      const detail = await res.text().catch(() => "");
      return { ok: false, error: detail || `Authorization failed (${res.status}).` };
    }
    return { ok: true };
  } catch (err: any) {
    return { ok: false, error: err?.message || "Couldn't authorize the machine." };
  }
}
