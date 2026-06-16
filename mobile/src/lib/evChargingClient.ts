// evChargingClient — drives Yaver's EV-charging DISCOVERY verbs over the mesh,
// addressed by device, with YOUR bearer + machine:"local" (LAN-first, relay
// fallback). Transport mirrors circuitClient/printerClient/armClient.
//
//   - ev_charging        — nearby stations from OpenChargeMap (live)
//   - ev_networks        — curated network directory by country (static)
//   - ev_connector_types — connector taxonomy + vehicle presets (static)
//
// DISCOVERY ONLY: the agent verb never starts/stops a charge session (that's a
// proprietary OCPP concern behind a private overlay). This client just finds
// stations and reasons about connectors/networks. Default vehicle/connector
// for a Togg/MG ZS EV in Turkey is CCS2 — see EV_DEFAULTS in evChargingFormat.

import AsyncStorage from "@react-native-async-storage/async-storage";
import { quicClient } from "./quic";

const EV_DEVICE_KEY = "yaver.ev.deviceId";
const AGENT_PORT = 18080;

export type EVTarget = { id: string; lanIps?: string[]; host?: string; port?: number };

// Shapes mirror desktop/agent/ev_charging.go (snake_case JSON).
export type EVConnector = {
  type: string;
  type_id?: string;
  power_kw?: number;
  current?: string;
  count?: number;
};

export type EVStation = {
  name: string;
  operator?: string;
  network?: string;
  address?: string;
  town?: string;
  country?: string;
  lat: number;
  lon: number;
  distance_km?: number;
  connectors?: EVConnector[];
  max_power_kw?: number;
  status_hint?: string;
  deep_link?: string;
  source?: string;
};

export type EVChargingResult = {
  source?: string;
  keyless?: boolean;
  count?: number;
  radius_km?: number;
  stations?: EVStation[];
  note?: string;
  // Policy Guard: a 403/429/451 from upstream surfaces as a structured block.
  blocked?: boolean;
  status_code?: number;
  detail?: string;
  error?: string;
};

export type EVNetwork = { id: string; name: string; country: string; note?: string };
export type EVNetworksResult = { count?: number; networks?: EVNetwork[]; note?: string; error?: string };

export type EVConnectorType = {
  id: string;
  name: string;
  current: string;
  region: string;
  max_power_kw: number;
  note?: string;
};
export type EVVehiclePreset = {
  id: string;
  name: string;
  connectors: string[];
  prefer_min_kw: number;
  note?: string;
};
export type EVConnectorTypesResult = {
  connector_types?: EVConnectorType[];
  vehicle_presets?: EVVehiclePreset[];
  note?: string;
  error?: string;
};

export type EVChargingQuery = {
  lat: number;
  lon: number;
  radius?: number;
  connector_type?: string;
  network?: string;
  country?: string;
  min_power_kw?: number;
};

export async function getEvDeviceId(): Promise<string> {
  return (await AsyncStorage.getItem(EV_DEVICE_KEY)) || "";
}
export async function setEvDeviceId(id: string): Promise<void> {
  await AsyncStorage.setItem(EV_DEVICE_KEY, id.trim());
}

async function lanAttempt(host: string, port: number, body: string, timeoutMs: number): Promise<any | null> {
  const ctrl = new AbortController();
  const timer = setTimeout(() => ctrl.abort(), Math.min(timeoutMs, 12000));
  try {
    const res = await fetch(`http://${host}:${port}/ops`, {
      method: "POST",
      headers: { ...quicClient.getAuthHeaders(), "Content-Type": "application/json" },
      body,
      signal: ctrl.signal,
    });
    const data = await res.json().catch(() => ({}));
    if (res.ok || data?.error) return data;
    return null;
  } catch {
    return null;
  } finally {
    clearTimeout(timer);
  }
}

async function evOps<T = any>(target: EVTarget | undefined, verb: string, payload: Record<string, unknown>, timeoutMs = 30000): Promise<T> {
  if (!target?.id) return { error: "pick a device first" } as unknown as T;
  const body = JSON.stringify({ verb, payload, machine: "local" });
  const port = target.port || AGENT_PORT;
  const hosts = [...(target.lanIps || []), ...(target.host ? [target.host] : [])].filter(Boolean);
  for (const h of hosts) {
    const data = await lanAttempt(h, port, body, timeoutMs);
    if (data) return ((data as any)?.initial ?? data) as T;
  }
  const data = await quicClient.callOpsOnDevice(target.id, verb, payload, timeoutMs);
  return ((data as any)?.initial ?? data) as T;
}

export const evChargingClient = {
  charging: (t: EVTarget, q: EVChargingQuery) =>
    evOps<EVChargingResult>(t, "ev_charging", q as Record<string, unknown>, 30000),
  networks: (t: EVTarget, country?: string) =>
    evOps<EVNetworksResult>(t, "ev_networks", country ? { country } : {}, 15000),
  connectorTypes: (t: EVTarget) =>
    evOps<EVConnectorTypesResult>(t, "ev_connector_types", {}, 15000),
};
