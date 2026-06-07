// deviceAgentFetch.ts — build a request context (base URL + auth headers) for
// talking to a *remote* device's Yaver agent HTTP server from the phone.
//
// The same relay-proxy / direct-host fallback this encodes already lived
// privately inside app/(tabs)/devices.tsx (buildDeviceRequestContext, used for
// /info + /projects). Mesh control (meshControl.ts) needs the identical path to
// hit /mesh/up, /mesh/status, /agent/self-heal on any box, so the pattern is
// promoted here as a single exported helper instead of being copy-pasted.
//
// Resolution order mirrors quic.ts's own candidate ordering: a relay (browser-
// reachable, password-gated) wins because the phone usually can't reach the
// box's LAN IP directly; only when there's no relay do we fall back to the raw
// host:port (works on the same LAN).

import { Platform } from "react-native";
import { quicClient } from "./quic";

export type DeviceAgentContext = {
  baseUrl: string;
  headers: Record<string, string>;
};

/** Returns the base URL + headers to reach `device`'s agent, or null when we
 *  have no token or no usable route (no relay and no host). Callers append the
 *  agent path, e.g. `fetch(`${ctx.baseUrl}/mesh/up`, { headers: ctx.headers })`. */
export function deviceAgentContext(
  device: { id: string; host?: string; port?: number },
  token: string | null,
): DeviceAgentContext | null {
  if (!token) return null;
  const relay = quicClient.getRelayServers()[0];
  if (relay?.httpUrl) {
    const headers: Record<string, string> = {
      Authorization: `Bearer ${token}`,
      "X-Client-Platform": Platform.OS,
    };
    if (relay.password) headers["X-Relay-Password"] = relay.password;
    return {
      baseUrl: `${relay.httpUrl}/d/${encodeURIComponent(device.id)}`,
      headers,
    };
  }
  if (!device.host) return null;
  return {
    baseUrl: `http://${device.host}:${device.port || 18080}`,
    headers: {
      Authorization: `Bearer ${token}`,
      "X-Client-Platform": Platform.OS,
    },
  };
}
