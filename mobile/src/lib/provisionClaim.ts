// provisionClaim.ts — buyer-side helpers for zero-touch (DPP-style) device
// provisioning. The user scans the QR printed on a Yaver-powered box's label
// (a `yaver://provision/v1?...` URI) and this turns that into ownership:
// parse the payload, then POST /devices/provision-claim with their bearer.
//
// Mirrors the agent's ParseProvisionQR (desktop/agent/provision.go) and the
// claimProvisionedDevice mutation (backend/convex/provisioning.ts). The QR
// carries only the public key + a one-time claim secret — possession of the
// physical label is the authorization to claim. Keep this file's parser pure
// (no I/O) so it stays unit-testable.

import { getConvexSiteUrlSync } from "./backendConfig";

export interface ProvisionClaim {
  deviceId: string;
  claimSecret: string;
  productId?: string;
  model?: string;
  convexSiteUrl?: string;
  publicKeyB64Url?: string;
}

// parseProvisionQR decodes a scanned `yaver://provision/v1?...` string.
// Returns null for anything that isn't a Yaver provision QR (so the camera
// scanner can ignore unrelated codes). RN's URL parsing is unreliable for
// custom schemes, so we parse the query by hand.
export function parseProvisionQR(raw: string): ProvisionClaim | null {
  const s = (raw || "").trim();
  // Accept yaver://provision/v1?... (and tolerate a missing /v1).
  const m = s.match(/^yaver:\/\/provision(?:\/v\d+)?\?(.*)$/i);
  if (!m) return null;
  const params: Record<string, string> = {};
  for (const pair of m[1].split("&")) {
    if (!pair) continue;
    const eq = pair.indexOf("=");
    const k = eq >= 0 ? pair.slice(0, eq) : pair;
    const v = eq >= 0 ? pair.slice(eq + 1) : "";
    try {
      params[decodeURIComponent(k)] = decodeURIComponent(v.replace(/\+/g, "%20"));
    } catch {
      params[k] = v;
    }
  }
  const deviceId = (params.d || "").trim();
  const claimSecret = (params.s || "").trim();
  if (!deviceId || !claimSecret) return null;
  return {
    deviceId,
    claimSecret,
    productId: params.p || undefined,
    model: params.m || undefined,
    convexSiteUrl: params.u || undefined,
    publicKeyB64Url: params.k || undefined,
  };
}

export interface ClaimResult {
  ok: boolean;
  deviceId: string;
  model?: string | null;
  alreadyActive?: boolean;
  error?: string;
}

// claimProvisionedDevice binds ownership of a scanned device to the signed-in
// user. The box self-credentials on its next boot (or immediately, if already
// waiting). Uses the QR's own convex URL when present so a box provisioned
// against a self-hosted backend still claims correctly.
export async function claimProvisionedDevice(
  token: string,
  claim: ProvisionClaim,
  name?: string,
): Promise<ClaimResult> {
  const base = (claim.convexSiteUrl || getConvexSiteUrlSync()).replace(/\/$/, "");
  try {
    const res = await fetch(`${base}/devices/provision-claim`, {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        Authorization: `Bearer ${token}`,
      },
      body: JSON.stringify({
        deviceId: claim.deviceId,
        claimSecret: claim.claimSecret,
        name: name?.trim() || undefined,
      }),
    });
    const data = (await res.json().catch(() => ({}))) as Record<string, unknown>;
    if (!res.ok) {
      return {
        ok: false,
        deviceId: claim.deviceId,
        error: (data.error as string) || `claim failed (${res.status})`,
      };
    }
    return {
      ok: true,
      deviceId: (data.deviceId as string) || claim.deviceId,
      model: (data.model as string) ?? claim.model ?? null,
      alreadyActive: Boolean(data.alreadyActive),
    };
  } catch (e: unknown) {
    return {
      ok: false,
      deviceId: claim.deviceId,
      error: e instanceof Error ? e.message : "network error",
    };
  }
}
