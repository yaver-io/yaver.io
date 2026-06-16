import type { EVChargingIntent, EVEvent, EVParsedQR, EVRouteKind } from "./types";

function id(prefix: string): string {
  return `${prefix}_${Date.now().toString(36)}_${Math.random().toString(36).slice(2, 8)}`;
}

export function makeEVIntent(rawQr: string, parsed: EVParsedQR): EVChargingIntent {
  const now = Date.now();
  const event: EVEvent = {
    at: now,
    type: "qr_captured",
    message: `${parsed.provider === "unknown" ? "Unknown provider" : parsed.provider} QR captured.`,
    data: { confidence: parsed.confidence },
  };
  return {
    id: id("ev"),
    createdAt: now,
    updatedAt: now,
    state: parsed.provider === "unknown" ? "provider_unknown" : "provider_identified",
    provider: parsed.provider,
    rawQr,
    normalizedUrl: parsed.normalizedUrl,
    stationId: parsed.stationId,
    connectorId: parsed.connectorId,
    chargerId: parsed.chargerId,
    socketLabel: parsed.socketLabel,
    approvals: [],
    events: [event, ...parsed.notes.map((message) => ({ at: now, type: "note", message }))],
  };
}

export function setEVRoute(intent: EVChargingIntent, route: EVRouteKind): EVChargingIntent {
  const now = Date.now();
  return {
    ...intent,
    route,
    state: "route_selected",
    updatedAt: now,
    events: [
      ...intent.events,
      { at: now, type: "route_selected", message: `Route selected: ${route}.` },
    ],
  };
}

export function addEVEvent(intent: EVChargingIntent, type: string, message: string, data?: Record<string, unknown>): EVChargingIntent {
  const now = Date.now();
  return {
    ...intent,
    updatedAt: now,
    events: [...intent.events, { at: now, type, message, data }],
  };
}

export function approveEV(intent: EVChargingIntent, kind: "login" | "otp" | "payment" | "start" | "stop", label: string): EVChargingIntent {
  const now = Date.now();
  return {
    ...intent,
    state: kind === "start" ? "awaiting_user_confirmation" : intent.state,
    updatedAt: now,
    approvals: [...intent.approvals, { id: id("approval"), at: now, kind, label, approved: true }],
    events: [...intent.events, { at: now, type: "approval", message: label, data: { kind } }],
  };
}
