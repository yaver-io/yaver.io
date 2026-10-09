// Yaver Access Channel account API. The hosted WebSocket or optional BYO MQTT
// broker receives only short-lived capability JWTs and public rendezvous
// signals. Yaver/OAuth tokens stay on this HTTPS connection.

import { getConvexSiteUrlSync } from "./backendConfig";

export type AccessBroker = {
  brokerId: string;
  name: string;
  endpoint: string;
  deployment: "yaver-hosted" | "byo";
  transport: "websocket" | "mqtt";
  caFingerprint: string | null;
  enabled: boolean;
  createdAt: number;
  deviceIds: string[];
};

async function accountRequest(token: string, path: string, init?: RequestInit): Promise<any> {
  const res = await fetch(`${getConvexSiteUrlSync()}${path}`, {
    ...init,
    headers: {
      "Content-Type": "application/json",
      Authorization: `Bearer ${token}`,
      ...(init?.headers || {}),
    },
  });
  const body = await res.json().catch(() => ({}));
  if (!res.ok) throw new Error(body?.error || `Access Channel request failed (${res.status})`);
  return body;
}

export async function enableHostedAccessChannel(token: string): Promise<void> {
  await accountRequest(token, "/access-channel/hosted", { method: "POST", body: "{}" });
}

export async function listAccessBrokers(token: string): Promise<AccessBroker[]> {
  const body = await accountRequest(token, "/access-channel/brokers", { method: "GET" });
  return Array.isArray(body?.brokers) ? body.brokers : [];
}

export async function registerAccessBroker(
  token: string,
  input: { name: string; endpoint: string; caFingerprint?: string },
): Promise<AccessBroker> {
  return accountRequest(token, "/access-channel/brokers", {
    method: "POST",
    body: JSON.stringify(input),
  });
}

export async function enrollAccessDevice(token: string, brokerId: string, deviceId: string): Promise<void> {
  await accountRequest(token, "/access-channel/enroll", {
    method: "POST",
    body: JSON.stringify({ brokerId, deviceId }),
  });
}

export async function revokeAccessDevice(token: string, brokerId: string, deviceId: string): Promise<void> {
  await accountRequest(token, "/access-channel/revoke", {
    method: "POST",
    body: JSON.stringify({ brokerId, deviceId }),
  });
}

type AccessCredential = {
  transport: "websocket" | "mqtt";
  endpoint: string;
  clientId: string;
  token: string;
};

function signalId(prefix: string): string {
  return `${prefix}-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 14)}`;
}

/**
 * Ask an already-enrolled headless device to start Yaver's existing one-tap
 * device-code recovery. The Access Channel carries only the request/ack; the
 * approval code and minted account session remain on Convex's auth path.
 */
export async function requestYaverSignIn(token: string, brokerId: string, targetDeviceId: string): Promise<void> {
  const grant = await accountRequest(token, "/access-channel/token", {
    method: "POST",
    body: JSON.stringify({ brokerId, role: "controller" }),
  }) as AccessCredential;
  if (grant.transport !== "websocket" || !grant.endpoint || !grant.token || !grant.clientId) {
    throw new Error("Hosted access is unavailable for this device.");
  }
  const requestId = signalId("req");
  const issuedAt = Date.now();
  const signal = {
    version: 1,
    kind: "requests.auth",
    requestId,
    targetDeviceId,
    issuerDeviceId: grant.clientId,
    issuedAt,
    expiresAt: issuedAt + 120_000,
    nonce: signalId("nonce"),
    provider: "yaver",
  };
  await new Promise<void>((resolve, reject) => {
    const socket = new WebSocket(grant.endpoint, ["yaver-access-v1", `auth.${grant.token}`]);
    const timeout = setTimeout(() => {
      socket.close();
      reject(new Error("The device did not answer the access request."));
    }, 20_000);
    const finish = (error?: Error) => {
      clearTimeout(timeout);
      socket.close();
      error ? reject(error) : resolve();
    };
    socket.onopen = () => socket.send(JSON.stringify(signal));
    socket.onerror = () => finish(new Error("Could not open the secure access channel."));
    socket.onmessage = (event) => {
      try {
        const reply = JSON.parse(String(event.data || "{}"));
        if (reply?.error) return finish(new Error(`Access request refused: ${reply.error}`));
        if (reply?.requestId === requestId && reply?.kind === "events.auth" && reply?.targetDeviceId === targetDeviceId) finish();
      } catch { /* ignore non-protocol frames */ }
    };
  });
}
