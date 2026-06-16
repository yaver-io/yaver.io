// tvSignIn — device-code (RFC 8628) sign-in for the TV form factor, where
// typing an email + password is painful. The TV acts as the "machine": it
// requests a code, shows a QR + a short code, and polls until an already-signed
// -in phone approves it (app/approve-device.tsx) — then the TV gets a 1-year
// session token and signs itself in.
//
// Same Convex HTTP contract the CLI's `yaver auth` uses
// (desktop/agent/devicecode.go):
//   POST /auth/device-code                         -> {userCode, deviceCode, expiresAt}
//   GET  /auth/device-code/poll?device_code=...    -> {status, token?}
// The phone approves via POST /auth/device-code/authorize (deviceCodeApprove.ts).
//
// fetch pattern mirrors deviceCodeApprove.ts (getConvexSiteUrlSync + plain fetch;
// there is no shared apiFetch helper in this app).

import { getConvexSiteUrlSync as getConvexSiteUrl } from "./backendConfig";

export interface DeviceCodeStart {
  userCode: string;
  deviceCode: string;
  expiresAt: number;
  /** Full verification URL to encode in the QR; opens app/approve-device on a phone. */
  verifyUrl: string;
}

export type PollStatus = "pending" | "authorized" | "expired";
export interface PollResult {
  status: PollStatus;
  token?: string;
}

/** The web/app deep link that routes a scanned QR into the phone approver. */
export function deviceVerifyUrl(userCode: string): string {
  return `https://yaver.io/auth/device?code=${encodeURIComponent(userCode)}`;
}

export async function createTVDeviceCode(machineName: string, platform: string): Promise<DeviceCodeStart> {
  const res = await fetch(`${getConvexSiteUrl()}/auth/device-code`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({
      machineName,
      platform,
      environment: "tv",
    }),
  });
  if (!res.ok) {
    throw new Error(`device-code create failed (${res.status})`);
  }
  const j = (await res.json()) as { userCode: string; deviceCode: string; expiresAt: number };
  return { ...j, verifyUrl: deviceVerifyUrl(j.userCode) };
}

export async function pollTVDeviceCode(deviceCode: string): Promise<PollResult> {
  const res = await fetch(
    `${getConvexSiteUrl()}/auth/device-code/poll?device_code=${encodeURIComponent(deviceCode)}`,
  );
  if (!res.ok) return { status: "pending" };
  return (await res.json()) as PollResult;
}
