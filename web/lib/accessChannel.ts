import { CONVEX_URL } from "@/lib/constants";

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

type AccessCredential = {
  transport: "websocket" | "mqtt";
  endpoint: string;
  clientId: string;
  token: string;
};

async function accountRequest(token: string, path: string, init?: RequestInit): Promise<any> {
  const response = await fetch(`${CONVEX_URL}${path}`, {
    ...init,
    cache: "no-store",
    headers: {
      "Content-Type": "application/json",
      Authorization: `Bearer ${token}`,
      ...(init?.headers || {}),
    },
  });
  const body = await response.json().catch(() => ({}));
  if (!response.ok) {
    throw new Error(body?.error || `Remote access request failed (${response.status})`);
  }
  return body;
}

export async function enableHostedAccessChannel(token: string): Promise<void> {
  await accountRequest(token, "/access-channel/hosted", { method: "POST", body: "{}" });
}

export async function listAccessBrokers(token: string): Promise<AccessBroker[]> {
  const body = await accountRequest(token, "/access-channel/brokers", { method: "GET" });
  return Array.isArray(body?.brokers) ? body.brokers : [];
}

export async function enrollAccessDevice(token: string, brokerId: string, deviceId: string): Promise<void> {
  await accountRequest(token, "/access-channel/enroll", {
    method: "POST",
    body: JSON.stringify({ brokerId, deviceId }),
  });
}

function signalId(prefix: string): string {
  return `${prefix}-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 14)}`;
}

export function buildYaverSignInSignal(clientId: string, targetDeviceId: string, now = Date.now()) {
  return {
    version: 1,
    kind: "requests.auth" as const,
    requestId: signalId("req"),
    targetDeviceId,
    issuerDeviceId: clientId,
    issuedAt: now,
    expiresAt: now + 120_000,
    nonce: signalId("nonce"),
    provider: "yaver" as const,
  };
}

/**
 * Ask a pre-enrolled headless device to begin the existing device-code flow.
 * The Cloudflare Worker receives only this bounded public signal. Account
 * tokens and the resulting device credential stay on the Convex auth path.
 */
export async function requestYaverSignIn(
  token: string,
  brokerId: string,
  targetDeviceId: string,
  timeoutMs = 20_000,
): Promise<void> {
  const grant = await accountRequest(token, "/access-channel/token", {
    method: "POST",
    body: JSON.stringify({ brokerId, role: "controller" }),
  }) as AccessCredential;
  if (grant.transport !== "websocket" || !grant.endpoint || !grant.token || !grant.clientId) {
    throw new Error("Hosted remote access is unavailable for this device.");
  }

  const signal = buildYaverSignInSignal(grant.clientId, targetDeviceId);
  await new Promise<void>((resolve, reject) => {
    const socket = new WebSocket(grant.endpoint, ["yaver-access-v1", `auth.${grant.token}`]);
    let finished = false;
    const finish = (error?: Error) => {
      if (finished) return;
      finished = true;
      clearTimeout(timeout);
      socket.close();
      error ? reject(error) : resolve();
    };
    const timeout = setTimeout(
      () => finish(new Error("The device did not answer the remote sign-in request.")),
      timeoutMs,
    );
    socket.onopen = () => socket.send(JSON.stringify(signal));
    socket.onerror = () => finish(new Error("Could not open the secure remote access channel."));
    socket.onmessage = (event) => {
      try {
        const reply = JSON.parse(String(event.data || "{}"));
        if (reply?.error) return finish(new Error(`Remote access refused: ${reply.error}`));
        if (
          reply?.requestId === signal.requestId &&
          reply?.kind === "events.auth" &&
          reply?.targetDeviceId === targetDeviceId
        ) {
          finish();
        }
      } catch {
        // Ignore frames outside the deliberately tiny access grammar.
      }
    };
  });
}
