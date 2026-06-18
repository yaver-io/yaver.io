import { agentFetch, type AgentDeviceRef } from "./agentRequest";

export type WiFiHotspotMode = "ap" | "apsta";

export type WiFiHotspotConfig = {
  ssid: string;
  password: string;
  channel?: number;
  mode: WiFiHotspotMode;
  interface?: string;
  apInterface?: string;
  upstreamIf?: string;
  upstreamSsid?: string;
  upstreamPass?: string;
  frequency?: "2.4GHz" | "5GHz" | string;
  ipAddress?: string;
  enableDhcp?: boolean;
  dhcpRange?: string;
  enableNat?: boolean;
  countryCode?: string;
};

export type WiFiCapabilities = {
  interface?: string;
  driver?: string;
  supportedModes?: WiFiHotspotMode[];
  supportedBands?: string[];
  supportsAp?: boolean;
  supportsApsta?: boolean;
  supports5GHz?: boolean;
  hardwareSupport?: string;
  isUsbDevice?: boolean;
};

export type WiFiStatus = {
  running?: boolean;
  mode?: WiFiHotspotMode;
  ssid?: string;
  interface?: string;
  ipAddress?: string;
  connectedClients?: number;
  upstreamStatus?: string;
  uptime?: string;
  lastError?: string;
  supportedModes?: WiFiHotspotMode[];
  hardwareSupport?: string;
};

export type WiFiClient = Record<string, unknown> & { mac?: string };
export type WiFiBan = { mac: string; expiry: string };

const WIFI_TIMEOUT_MS = 12000;
const WIFI_START_TIMEOUT_MS = 30000;

async function wifiJSON<T>(
  device: AgentDeviceRef,
  token: string | null,
  path: string,
  init: RequestInit = {},
  timeoutMs = WIFI_TIMEOUT_MS,
): Promise<T> {
  const res = await agentFetch(device, token, path, init, timeoutMs);
  const text = await res.text();
  const json = text ? JSON.parse(text) : {};
  if (!res.ok) throw new Error(json?.error || `HTTP ${res.status}`);
  if (json?.ok === false) throw new Error(json?.error || "Wi-Fi request failed");
  return json as T;
}

export async function wifiCapabilities(device: AgentDeviceRef, token: string | null): Promise<WiFiCapabilities | null> {
  const json = await wifiJSON<{ capabilities?: WiFiCapabilities }>(device, token, "/console/wifi/capabilities");
  return json.capabilities ?? null;
}

export async function wifiStatus(device: AgentDeviceRef, token: string | null): Promise<WiFiStatus | null> {
  const json = await wifiJSON<{ status?: WiFiStatus }>(device, token, "/console/wifi/status");
  return json.status ?? null;
}

export async function wifiStart(device: AgentDeviceRef, token: string | null, config: WiFiHotspotConfig): Promise<WiFiStatus | null> {
  const json = await wifiJSON<{ status?: WiFiStatus }>(
    device,
    token,
    "/console/wifi/start",
    { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(config) },
    WIFI_START_TIMEOUT_MS,
  );
  return json.status ?? null;
}

export async function wifiStop(device: AgentDeviceRef, token: string | null): Promise<WiFiStatus | null> {
  const json = await wifiJSON<{ status?: WiFiStatus }>(
    device,
    token,
    "/console/wifi/stop",
    { method: "POST", headers: { "Content-Type": "application/json" }, body: "{}" },
    WIFI_START_TIMEOUT_MS,
  );
  return json.status ?? null;
}

export async function wifiClients(device: AgentDeviceRef, token: string | null): Promise<WiFiClient[]> {
  const json = await wifiJSON<{ clients?: WiFiClient[] }>(device, token, "/console/wifi/clients");
  return json.clients ?? [];
}

export async function wifiKickClient(device: AgentDeviceRef, token: string | null, mac: string): Promise<void> {
  await wifiJSON(device, token, "/console/wifi/clients/kick", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ mac }),
  });
}

export async function wifiBanClient(device: AgentDeviceRef, token: string | null, mac: string, durationHours = 0): Promise<void> {
  await wifiJSON(device, token, "/console/wifi/clients/ban", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ mac, durationHours }),
  });
}

export async function wifiUnbanClient(device: AgentDeviceRef, token: string | null, mac: string): Promise<void> {
  await wifiJSON(device, token, "/console/wifi/clients/unban", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ mac }),
  });
}

export async function wifiBannedClients(device: AgentDeviceRef, token: string | null): Promise<WiFiBan[]> {
  const json = await wifiJSON<{ bannedClients?: WiFiBan[]; banned_clients?: WiFiBan[] }>(device, token, "/console/wifi/clients/banned");
  return json.bannedClients ?? json.banned_clients ?? [];
}

export async function wifiGetAPSTAConfig(device: AgentDeviceRef, token: string | null): Promise<WiFiHotspotConfig | null> {
  const json = await wifiJSON<{ config?: WiFiHotspotConfig; error?: string }>(device, token, "/console/wifi/apsta-config");
  return json.config ?? null;
}

export async function wifiSetAPSTAConfig(device: AgentDeviceRef, token: string | null, config: WiFiHotspotConfig): Promise<void> {
  await wifiJSON(device, token, "/console/wifi/apsta-config", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(config),
  });
}
