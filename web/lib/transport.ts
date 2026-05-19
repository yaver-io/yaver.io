/**
 * Classify how a device is reachable so the UI can show "Yaver public
 * relay v0.1.9 ✓" or "Tailscale 100.64.x.x" etc. instead of just an
 * opaque IP. Pure function over {Device + activeRelayUrl}; no I/O.
 *
 * Mirrored in mobile/src/lib/transport.ts so the mobile device card
 * + details screen render the same classification. Keep these two
 * files in sync — the regex / range checks live here.
 */

export type TransportPrimary =
  | "yaver-public-relay"   // public.yaver.io (or any subdomain that resolves to a Yaver-managed relay)
  | "self-hosted-relay"    // user-deployed relay (custom URL)
  | "cloudflare-tunnel"    // *.trycloudflare.com / *.cfargotunnel.com / cf-access (X-Forwarded-Host)
  | "tailscale"            // 100.64/10 CGNAT range
  | "wsl-nat"              // 172.16-31/12 + Windows-shaped hostname
  | "private-lan"          // RFC1918 reachable directly
  | "direct-public"        // public IP, no tunnel / relay
  | "unknown";

export interface TransportInfo {
  primary: TransportPrimary;
  /** Short, human-readable label for badge UI ("Yaver public relay"). */
  label: string;
  /** Longer description suitable for a details panel ("via public.yaver.io · v0.1.9"). */
  detail: string;
  /** Tailwind palette name → caller decides exact class. */
  tone: "emerald" | "blue" | "violet" | "amber" | "slate" | "rose";
  /** Best-effort URL of the current transport (relay HTTPS URL, tunnel URL, …). */
  url?: string;
  /** Version of the relay (if known and reachable). Populated by callers
   *  via fetchRelayVersion(); the classifier itself doesn't network. */
  relayVersion?: string;
}

export interface TransportInput {
  host?: string;
  port?: number;
  localIps?: string[];
  publicEndpoints?: string[];
  tunnelUrl?: string;
  /** Set when the dashboard's WebClient connected via relay — pass
   *  agentClient.activeRelayUrl here. Drives the public-vs-self-hosted
   *  distinction.
   *
   *  IMPORTANT: only meaningful for the device the dashboard is
   *  CURRENTLY CONNECTED TO. If you're classifying transport for
   *  *other* devices in the user's account, leave this undefined —
   *  the dashboard isn't connected to them via that URL. Pass
   *  isActiveDevice=false for non-current devices.
   */
  activeRelayUrl?: string | null;
  /** Same idea but for tunnel-via-Cloudflare-Access etc. */
  activeTunnelUrl?: string | null;
  /** True if THIS device is the one the dashboard is currently
   *  connected to. Without this, every device in the list would
   *  inherit the active connection's transport label, which is
   *  wrong — see the original bug where a remote Linux box showed
   *  "Tailscale" because the dashboard happened to reach the
   *  current device via Tailscale. */
  isActiveDevice?: boolean;
  /** "ios"/"linux"/"darwin"/"windows"/"android" — used to spot WSL2. */
  platform?: string;
  /** Hostname — used to spot WSL2 (DESKTOP-…/LAPTOP-…/WIN-…). */
  name?: string;
}

const RFC1918 = [
  /^10\./,
  /^192\.168\./,
  /^172\.(1[6-9]|2\d|3[0-1])\./,
];
const TAILSCALE_CGNAT = /^100\.(6[4-9]|[7-9]\d|1[0-1]\d|12[0-7])\./;
const WSL_NAT = /^172\.(1[6-9]|2\d|3[0-1])\./;

function isRFC1918(ip: string): boolean {
  return RFC1918.some((re) => re.test(ip));
}
function isTailscaleIP(ip: string): boolean {
  return TAILSCALE_CGNAT.test(ip);
}
function isCloudflareTunnel(url: string): boolean {
  return /(^https?:\/\/)?[a-z0-9-]+\.(trycloudflare\.com|cfargotunnel\.com)\b/i.test(url);
}
function isYaverPublicRelay(url: string): boolean {
  // Match any subdomain of yaver.io OR explicitly the public-free
  // relay endpoint. Self-hosters whose CNAME points at *.yaver.io
  // would still classify as "yaver-public" — that's intentional;
  // they're using Yaver-managed infra.
  return /^https?:\/\/(public|relay|free|managed)\.yaver\.io(\/|$)/i.test(url);
}
function looksWindowsLike(name: string): boolean {
  const upper = name.toUpperCase();
  return (
    upper.startsWith("DESKTOP-") ||
    upper.startsWith("LAPTOP-") ||
    upper.startsWith("WIN-")
  );
}

/**
 * Classify the transport for a device. Order of precedence:
 *   1. activeRelayUrl set by the WebClient        → relay (yaver-public OR self-hosted)
 *   2. activeTunnelUrl / device.tunnelUrl         → cloudflare-tunnel
 *   3. host / localIps Tailscale CGNAT IP         → tailscale
 *   4. host RFC1918 + Windows hostname / WSL NAT  → wsl-nat
 *   5. host RFC1918                                → private-lan
 *   6. host is a non-private IP and reachable      → direct-public
 *   7. fallback                                    → unknown
 */
export function classifyTransport(d: TransportInput): TransportInfo {
  const host = (d.host || "").trim();
  const ips = [host, ...(d.localIps || [])].map((s) => (s || "").trim()).filter(Boolean);

  // 1. Relay path — ONLY meaningful for the device the dashboard
  // is currently connected to. For other devices in the list,
  // activeRelayUrl describes a different connection (the active
  // device), not theirs.
  if (d.activeRelayUrl && d.isActiveDevice !== false) {
    if (isYaverPublicRelay(d.activeRelayUrl)) {
      return {
        primary: "yaver-public-relay",
        label: "Yaver public relay",
        detail: `via ${cleanHostFromUrl(d.activeRelayUrl)}`,
        tone: "emerald",
        url: d.activeRelayUrl,
      };
    }
    return {
      primary: "self-hosted-relay",
      label: "Self-hosted relay",
      detail: `via ${cleanHostFromUrl(d.activeRelayUrl)}`,
      tone: "violet",
      url: d.activeRelayUrl,
    };
  }

  // 2. Cloudflare / generic tunnel
  const tunnel = (d.activeTunnelUrl || d.tunnelUrl || "").trim();
  if (tunnel) {
    if (isCloudflareTunnel(tunnel)) {
      return {
        primary: "cloudflare-tunnel",
        label: "Cloudflare tunnel",
        detail: `via ${cleanHostFromUrl(tunnel)}`,
        tone: "amber",
        url: tunnel,
      };
    }
    return {
      primary: "cloudflare-tunnel",
      label: "Tunnel",
      detail: `via ${cleanHostFromUrl(tunnel)}`,
      tone: "amber",
      url: tunnel,
    };
  }

  // 3. Tailscale
  const tsIp = ips.find(isTailscaleIP);
  if (tsIp) {
    return {
      primary: "tailscale",
      label: "Tailscale",
      detail: `via ${tsIp}`,
      tone: "blue",
      url: `http://${tsIp}:${d.port ?? 18080}`,
    };
  }

  // 4. WSL2 NAT — require BOTH the 172.16-31 IP AND a Windows-
  // shaped hostname. The IP alone is also Docker bridge networks
  // (172.17/16, 172.18/16, …), so a remote Linux box running
  // Docker would false-positive as WSL2. Only WSL2's Hyper-V
  // bridge AND a DESKTOP-/LAPTOP-/WIN- hostname combination is
  // a high-confidence signal.
  const wslIp = ips.find((ip) => WSL_NAT.test(ip));
  const platform = String(d.platform || "").toLowerCase();
  const name = String(d.name || "").trim();
  if (platform === "linux" && looksWindowsLike(name)) {
    return {
      primary: "wsl-nat",
      label: "WSL2 NAT",
      detail: wslIp ? `via ${wslIp} (Hyper-V)` : "Linux on Windows host",
      tone: "amber",
      url: wslIp ? `http://${wslIp}:${d.port ?? 18080}` : undefined,
    };
  }

  // 5. Private LAN
  const lan = ips.find((ip) => isRFC1918(ip) && !isTailscaleIP(ip) && !WSL_NAT.test(ip));
  if (lan) {
    return {
      primary: "private-lan",
      label: "Private LAN",
      detail: `via ${lan}`,
      tone: "slate",
      url: `http://${lan}:${d.port ?? 18080}`,
    };
  }

  // 6. Direct public IP
  if (host && /^\d+\.\d+\.\d+\.\d+$/.test(host)) {
    return {
      primary: "direct-public",
      label: "Public IP",
      detail: `via ${host}`,
      tone: "rose",
      url: `http://${host}:${d.port ?? 18080}`,
    };
  }
  if ((d.publicEndpoints || []).length) {
    return {
      primary: "direct-public",
      label: "Public endpoint",
      detail: `via ${d.publicEndpoints![0]}`,
      tone: "rose",
      url: d.publicEndpoints![0],
    };
  }

  return {
    primary: "unknown",
    label: "Unknown",
    detail: "no transport metadata",
    tone: "slate",
  };
}

function cleanHostFromUrl(url: string): string {
  try {
    const u = new URL(url);
    return u.host;
  } catch {
    return url.replace(/^https?:\/\//, "").replace(/\/.*$/, "");
  }
}

/** Best-effort fetch of relay version + tunnel count from {relayUrl}/health.
 *  Returns null on any failure; UI should fall back to "version unknown". */
export async function fetchRelayHealth(relayUrl: string, signal?: AbortSignal): Promise<{ version?: string; tunnels?: number; activeDevices?: number } | null> {
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
