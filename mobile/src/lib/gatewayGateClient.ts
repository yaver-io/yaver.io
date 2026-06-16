// gatewayGateClient — lists and resolves the gateway Auth Broker's pending
// HUMAN GATES on one of your boxes, over the agent HTTP routes via the live
// connection (direct LAN / Tailscale / tunnel / relay, whichever connect()
// negotiated). A human gate is where the broker paused its automation for you:
// an OAuth re-consent, a 2FA/OTP, a captcha, a KYC upload, a payment confirm,
// etc. (see gateway_broker.go — the resumable PendingGate, milestone M-G3).
//
//   GET  /gateway/gate                 → { gates: [...] }   (pending gates)
//   POST /gateway/gate/{id}/resolve    → { ok } body {action, value?}
//
// The route may not be live on an older daemon (the broker lands in slices);
// callers treat a 404 as "no gates / not supported yet" rather than an error,
// so this screen degrades cleanly while the backend catches up.

import { quicClient } from "./quic";

/** GatewayGate is one pending human gate. Fields are best-effort — only `id`
 *  is guaranteed; the rest drive richer rendering when the agent supplies
 *  them. `interactive` (when present) overrides the step-based heuristic in
 *  gatewayGateFormat.needsRemoteView. */
export type GatewayGate = {
  id: string;
  step?: string; // login | two_factor | captcha | kyc_upload | payment_confirm | region_confirm | tap_relay | push_approval
  connector?: string;
  service?: string;
  title?: string;
  prompt?: string;
  url?: string; // the page the box is parked on (informational)
  createdAt?: number; // epoch ms
  interactive?: boolean; // explicit "needs live view" flag
  // For an interactive co-browse gate the agent may hand back the same
  // frame/input paths browser_interactive uses, so the screen can poll frames
  // and post input without re-deriving them.
  framePath?: string;
  inputPath?: string;
};

export type GatewayGatesResult = {
  gates: GatewayGate[];
  supported: boolean; // false when the route 404s (daemon too old / broker off)
  error?: string;
};

export type GateResolveAction = "approve" | "deny" | "submit" | "done";

/** listGates fetches pending human gates from a device. A 404 → supported:false
 *  (route not shipped yet) with an empty list; other failures surface as an
 *  error but still return an empty list so the UI stays usable. */
export async function listGates(deviceId: string): Promise<GatewayGatesResult> {
  if (!deviceId) return { gates: [], supported: false, error: "no device selected" };
  try {
    const res = await quicClient.agentRequest(deviceId, "/gateway/gate", undefined, 12000);
    if (res.status === 404) return { gates: [], supported: false };
    if (!res.ok) return { gates: [], supported: true, error: `gate list ${res.status}` };
    const data = await res.json().catch(() => ({}));
    const gates: GatewayGate[] = Array.isArray(data?.gates)
      ? data.gates
      : Array.isArray(data)
      ? data
      : [];
    return { gates, supported: true };
  } catch (e: any) {
    return { gates: [], supported: true, error: e?.message || "couldn't list gates" };
  }
}

/** resolveGate resolves a gate by id with an action (and optional value, e.g.
 *  an OTP code the user typed). Returns ok:false with an error string on
 *  failure so the caller can show it inline. */
export async function resolveGate(
  deviceId: string,
  gateId: string,
  action: GateResolveAction,
  value?: string,
): Promise<{ ok: boolean; error?: string }> {
  if (!deviceId || !gateId) return { ok: false, error: "missing device or gate id" };
  try {
    const res = await quicClient.agentRequest(
      deviceId,
      `/gateway/gate/${encodeURIComponent(gateId)}/resolve`,
      {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(value != null ? { action, value } : { action }),
      },
      15000,
    );
    if (res.status === 404) return { ok: false, error: "gate no longer pending" };
    if (!res.ok) {
      const data = await res.json().catch(() => ({}));
      return { ok: false, error: data?.error || `resolve ${res.status}` };
    }
    return { ok: true };
  } catch (e: any) {
    return { ok: false, error: e?.message || "couldn't resolve gate" };
  }
}
