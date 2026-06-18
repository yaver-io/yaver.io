import { deviceAgentContext } from "./deviceAgentFetch";
import { connectionManager } from "./connectionManager";

export type AgentDeviceRef = { id: string; host?: string; port?: number };

export async function agentFetch(
  device: AgentDeviceRef,
  token: string | null,
  path: string,
  init: RequestInit = {},
  timeoutMs = 10000,
): Promise<Response> {
  const client = connectionManager.clientFor(device.id);
  if (client?.isConnected) {
    return client.agentRequest(device.id, path, init, timeoutMs);
  }
  const ctx = deviceAgentContext(device, token);
  if (!ctx) throw new Error("unreachable");
  return fetch(`${ctx.baseUrl}${path}`, {
    ...init,
    headers: { ...ctx.headers, ...((init.headers as Record<string, string>) || {}) },
    signal: AbortSignal.timeout(timeoutMs),
  });
}
