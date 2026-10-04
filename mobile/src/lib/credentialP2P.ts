export interface CredentialP2PEndpoint {
  id: string;
  name?: string;
  host: string;
  port: number;
  lanIps?: string[];
}

export interface CredentialP2PClient {
  connectionMode: "direct" | "relay" | "tunnel" | null;
  connect(
    host: string,
    port: number,
    token: string,
    deviceId: string,
    lanIps?: string[],
    sessionTunnels?: unknown,
    connectionPreferences?: Array<{ kind: string; active: boolean; preferred: boolean; source?: string }>,
  ): Promise<void>;
  disconnect(): void;
}

export const CREDENTIAL_P2P_PREFERENCES = [
  { kind: "direct-lan", active: true, preferred: true, source: "credential-handoff" },
  { kind: "tailscale", active: true, preferred: true, source: "credential-handoff" },
  { kind: "headscale", active: true, preferred: true, source: "credential-handoff" },
  { kind: "own-vpn", active: true, preferred: true, source: "credential-handoff" },
] as const;

export async function runIsolatedCredentialP2P<T>(args: {
  endpoint: CredentialP2PEndpoint;
  authToken: string;
  createClient: () => CredentialP2PClient;
  operation: (client: CredentialP2PClient) => Promise<T>;
}): Promise<T> {
  const { endpoint, authToken, createClient, operation } = args;
  if (!authToken.trim()) throw new Error("Your Yaver session expired. Sign in again before transferring credentials.");
  const client = createClient();
  try {
    try {
      await client.connect(
        endpoint.host,
        endpoint.port,
        authToken,
        endpoint.id,
        endpoint.lanIps,
        undefined,
        [...CREDENTIAL_P2P_PREFERENCES],
      );
    } catch {
      throw new Error(
        `Couldn't open a separate secure P2P channel to ${endpoint.name || "the selected device"}. Keep the normal Yaver connection open and put both devices on the same LAN, Tailscale, Headscale, or private VPN.`,
      );
    }
    if (client.connectionMode !== "direct") {
      throw new Error("Credential transfer requires a direct LAN, Tailscale, Headscale, or private-VPN connection.");
    }
    return await operation(client);
  } finally {
    client.disconnect();
  }
}
