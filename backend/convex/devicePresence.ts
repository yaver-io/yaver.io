// Durable device-registry truth shared with every client through
// GET /devices/list. This is computed from fields the query already reads: it
// adds no Convex table, write, subscription, or polling cost.
//
// It deliberately does NOT claim that a particular browser/phone/TV can reach
// the agent. Clients own that verdict by attempting the operation. The backend
// only names what its control plane can prove.

export const HEARTBEAT_STALE_MS = 900_000;

export type ControlPlaneState = "relay-online" | "reporting" | "needs-auth" | "offline";
export type ControlPlaneReason =
  | "relay-live"
  | "heartbeat-fresh-no-relay"
  | "heartbeat-fresh-relay-unknown"
  | "auth-required"
  | "agent-marked-offline"
  | "heartbeat-stale"
  | "never-reported";

export type ControlPlaneStatus = {
  state: ControlPlaneState;
  reasonCode: ControlPlaneReason;
  lastSignalAt: number | null;
  freshUntil: number | null;
  relayPath: "available" | "unavailable" | "unknown";
  suggestedAction: "connect" | "reauth" | "try-connect" | "wake-or-start";
};

export type ControlPlaneInput = {
  isOnline: boolean;
  needsAuth?: boolean;
  lastHeartbeat?: number;
  relayConnected?: boolean;
  lastTunnelEvent?: { online?: boolean; at?: number } | null;
};

export function deriveControlPlaneStatus(input: ControlPlaneInput, now = Date.now()): ControlPlaneStatus {
  const heartbeatAt = input.lastHeartbeat && input.lastHeartbeat > 0 ? input.lastHeartbeat : null;
  const tunnelAt = input.lastTunnelEvent?.at && input.lastTunnelEvent.at > 0
    ? input.lastTunnelEvent.at
    : null;
  const lastSignalAt = Math.max(heartbeatAt ?? 0, tunnelAt ?? 0) || null;
  const heartbeatFresh = Boolean(
    input.isOnline && heartbeatAt && now - heartbeatAt < HEARTBEAT_STALE_MS,
  );
  const tunnelFresh = Boolean(
    input.lastTunnelEvent?.online === true && tunnelAt && now - tunnelAt < HEARTBEAT_STALE_MS,
  );
  const live = heartbeatFresh || tunnelFresh;
  const freshUntil = lastSignalAt ? lastSignalAt + HEARTBEAT_STALE_MS : null;

  if (!live) {
    return {
      state: "offline",
      reasonCode: !lastSignalAt
        ? "never-reported"
        : input.isOnline
          ? "heartbeat-stale"
          : "agent-marked-offline",
      lastSignalAt,
      freshUntil,
      relayPath: "unknown",
      suggestedAction: "wake-or-start",
    };
  }
  if (input.needsAuth) {
    return {
      state: "needs-auth",
      reasonCode: "auth-required",
      lastSignalAt,
      freshUntil,
      relayPath: tunnelFresh || input.relayConnected === true ? "available" : "unknown",
      suggestedAction: "reauth",
    };
  }
  if (tunnelFresh || input.relayConnected === true) {
    return {
      state: "relay-online",
      reasonCode: "relay-live",
      lastSignalAt,
      freshUntil,
      relayPath: "available",
      suggestedAction: "connect",
    };
  }
  if (input.relayConnected === false) {
    return {
      state: "reporting",
      reasonCode: "heartbeat-fresh-no-relay",
      lastSignalAt,
      freshUntil,
      relayPath: "unavailable",
      suggestedAction: "try-connect",
    };
  }
  return {
    state: "reporting",
    reasonCode: "heartbeat-fresh-relay-unknown",
    lastSignalAt,
    freshUntil,
    relayPath: "unknown",
    suggestedAction: "try-connect",
  };
}
