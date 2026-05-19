/**
 * Mobile-side mirror of web/lib/transport.ts. Same classifier so
 * the mobile device card + details screen render the same labels.
 *
 * Keep these two files in sync when adding new transport types.
 */

import { chipPalette } from "./chipPalette";

export type TransportPrimary =
  | "yaver-public-relay"
  | "self-hosted-relay"
  | "cloudflare-tunnel"
  | "tailscale"
  | "wsl-nat"
  | "private-lan"
  | "direct-public"
  | "unknown";

export interface TransportInfo {
  primary: TransportPrimary;
  label: string;
  detail: string;
  /** Use the host's tone palette to colour the badge. */
  tone: "emerald" | "blue" | "violet" | "amber" | "slate" | "rose";
  url?: string;
  relayVersion?: string;
}

export interface TransportInput {
  host?: string;
  port?: number;
  localIps?: string[];
  publicEndpoints?: string[];
  tunnelUrl?: string;
  /** Only meaningful for the device the dashboard is CURRENTLY
   *  connected to. For other devices, leave undefined or pass
   *  isActiveDevice=false — otherwise every device card shows
   *  the same transport label as the active one (real bug). */
  activeRelayUrl?: string | null;
  activeTunnelUrl?: string | null;
  isActiveDevice?: boolean;
  platform?: string;
  name?: string;
}

const RFC1918 = [/^10\./, /^192\.168\./, /^172\.(1[6-9]|2\d|3[0-1])\./];
const TAILSCALE_CGNAT = /^100\.(6[4-9]|[7-9]\d|1[0-1]\d|12[0-7])\./;
const WSL_NAT = /^172\.(1[6-9]|2\d|3[0-1])\./;

function isRFC1918(ip: string): boolean { return RFC1918.some((re) => re.test(ip)); }
function isTailscaleIP(ip: string): boolean { return TAILSCALE_CGNAT.test(ip); }
function isCloudflareTunnel(url: string): boolean {
  return /(^https?:\/\/)?[a-z0-9-]+\.(trycloudflare\.com|cfargotunnel\.com)\b/i.test(url);
}
function isYaverPublicRelay(url: string): boolean {
  return /^https?:\/\/(public|relay|free|managed)\.yaver\.io(\/|$)/i.test(url);
}
function looksWindowsLike(name: string): boolean {
  const upper = name.toUpperCase();
  return upper.startsWith("DESKTOP-") || upper.startsWith("LAPTOP-") || upper.startsWith("WIN-");
}
function cleanHostFromUrl(url: string): string {
  try { return new URL(url).host; }
  catch { return url.replace(/^https?:\/\//, "").replace(/\/.*$/, ""); }
}

export function classifyTransport(d: TransportInput): TransportInfo {
  const host = (d.host || "").trim();
  const ips = [host, ...(d.localIps || [])].map((s) => (s || "").trim()).filter(Boolean);

  if (d.activeRelayUrl && d.isActiveDevice !== false) {
    if (isYaverPublicRelay(d.activeRelayUrl)) {
      return {
        primary: "yaver-public-relay", label: "Yaver public relay",
        detail: `via ${cleanHostFromUrl(d.activeRelayUrl)}`,
        tone: "emerald", url: d.activeRelayUrl,
      };
    }
    return {
      primary: "self-hosted-relay", label: "Self-hosted relay",
      detail: `via ${cleanHostFromUrl(d.activeRelayUrl)}`,
      tone: "violet", url: d.activeRelayUrl,
    };
  }

  const tunnel = (d.activeTunnelUrl || d.tunnelUrl || "").trim();
  if (tunnel) {
    return {
      primary: "cloudflare-tunnel",
      label: isCloudflareTunnel(tunnel) ? "Cloudflare tunnel" : "Tunnel",
      detail: `via ${cleanHostFromUrl(tunnel)}`,
      tone: "amber", url: tunnel,
    };
  }

  const tsIp = ips.find(isTailscaleIP);
  if (tsIp) {
    return {
      primary: "tailscale", label: "Tailscale",
      detail: `via ${tsIp}`,
      tone: "blue", url: `http://${tsIp}:${d.port ?? 18080}`,
    };
  }

  // WSL2 NAT requires Windows-shaped hostname AND Linux platform.
  // 172.16-31 IP alone is also Docker bridge networks; without the
  // hostname check, a remote Linux box running Docker false-
  // positives as WSL2.
  const wslIp = ips.find((ip) => WSL_NAT.test(ip));
  const platform = String(d.platform || "").toLowerCase();
  const name = String(d.name || "").trim();
  if (platform === "linux" && looksWindowsLike(name)) {
    return {
      primary: "wsl-nat", label: "WSL2 NAT",
      detail: wslIp ? `via ${wslIp} (Hyper-V)` : "Linux on Windows host",
      tone: "amber", url: wslIp ? `http://${wslIp}:${d.port ?? 18080}` : undefined,
    };
  }

  const lan = ips.find((ip) => isRFC1918(ip) && !isTailscaleIP(ip) && !WSL_NAT.test(ip));
  if (lan) {
    return {
      primary: "private-lan", label: "Private LAN",
      detail: `via ${lan}`,
      tone: "slate", url: `http://${lan}:${d.port ?? 18080}`,
    };
  }

  if (host && /^\d+\.\d+\.\d+\.\d+$/.test(host)) {
    return {
      primary: "direct-public", label: "Public IP",
      detail: `via ${host}`,
      tone: "rose", url: `http://${host}:${d.port ?? 18080}`,
    };
  }
  if ((d.publicEndpoints || []).length) {
    return {
      primary: "direct-public", label: "Public endpoint",
      detail: `via ${d.publicEndpoints![0]}`,
      tone: "rose", url: d.publicEndpoints![0],
    };
  }

  return { primary: "unknown", label: "Unknown", detail: "no transport metadata", tone: "slate" };
}

/** Fetch relay /health for version + tunnel count. Returns null on
 *  any error (offline, CORS, …) so the UI shows "probing…" then
 *  "version unknown" without throwing. */
export async function fetchRelayHealth(
  relayUrl: string,
  signal?: AbortSignal,
): Promise<{ version?: string; tunnels?: number; activeDevices?: number } | null> {
  try {
    const u = new URL(relayUrl);
    const res = await fetch(`${u.origin}/health`, { signal });
    if (!res.ok) return null;
    const j = await res.json();
    return {
      version: typeof j?.version === "string" ? j.version : undefined,
      tunnels: typeof j?.tunnels === "number" ? j.tunnels : undefined,
      activeDevices: typeof j?.activeDevices === "number" ? j.activeDevices : undefined,
    };
  } catch {
    return null;
  }
}

export function transportToneRGB(tone: TransportInfo["tone"], isDark: boolean = true): { bg: string; border: string; text: string } {
  // Delegates to the shared chipPalette so transport badges adapt to
  // light/dark theme. Default isDark=true preserves the prior look for
  // any caller that hasn't been updated yet.
  const p = chipPalette(tone, isDark);
  return { bg: p.bg, border: p.border, text: p.text };
}
