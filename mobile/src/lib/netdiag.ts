// netdiag.ts — on-phone network diagnostics for the Connection screen.
// ===========================================================================
// Two signal sources:
//   1. Device / local-network info from @react-native-community/netinfo
//      (already a dependency) — WiFi SSID/signal, IP, subnet, derived gateway,
//      cellular generation/carrier. Loaded defensively so a JS-only bundle on
//      an older native binary degrades gracefully instead of crashing.
//   2. Internet probes via fetch() — latency, DNS overhead, public IP +
//      location, throughput — measured FROM THE PHONE (no agent/runner needed).
//
// The connected runner's network (interfaces, routes, ping, dns) is a separate
// concern handled in the screen via callMcpDirect → the runner's MCP tools.
// ===========================================================================

// --- Defensive NetInfo load -------------------------------------------------
let NetInfoMod: any = null;
try {
  // eslint-disable-next-line @typescript-eslint/no-var-requires
  NetInfoMod = require("@react-native-community/netinfo").default;
} catch {
  NetInfoMod = null;
}

export const netInfoAvailable = (): boolean => !!NetInfoMod;

export type DeviceNetwork = {
  type: string; // wifi | cellular | ethernet | vpn | none | unknown
  isConnected: boolean | null;
  isInternetReachable: boolean | null;
  ssid?: string | null;
  bssid?: string | null;
  strength?: number | null; // 0..100
  frequency?: number | null; // MHz
  ipAddress?: string | null;
  subnet?: string | null;
  gateway?: string | null; // derived best-effort
  linkSpeedMbps?: number | null;
  cellularGeneration?: string | null; // 2g/3g/4g/5g
  carrier?: string | null;
};

function deriveGateway(ip?: string | null): string | null {
  if (!ip || ip.indexOf(".") < 0) return null;
  const parts = ip.split(".");
  if (parts.length !== 4) return null;
  return `${parts[0]}.${parts[1]}.${parts[2]}.1`;
}

export async function getDeviceNetwork(): Promise<DeviceNetwork | null> {
  if (!NetInfoMod) return null;
  const st = await NetInfoMod.fetch();
  const d = st?.details ?? {};
  const ip = d.ipAddress ?? null;
  return {
    type: st?.type ?? "unknown",
    isConnected: st?.isConnected ?? null,
    isInternetReachable: st?.isInternetReachable ?? null,
    ssid: d.ssid ?? null,
    bssid: d.bssid ?? null,
    strength: d.strength ?? null,
    frequency: d.frequency ?? null,
    ipAddress: ip,
    subnet: d.subnet ?? null,
    gateway: deriveGateway(ip),
    linkSpeedMbps: d.linkSpeed ?? null,
    cellularGeneration: d.cellularGeneration ?? null,
    carrier: d.carrier ?? null,
  };
}

// --- Pure-fetch internet probes ---------------------------------------------

async function fetchTimed(
  url: string,
  timeoutMs: number,
): Promise<{ ok: boolean; ms: number; body?: string }> {
  const ctrl = new AbortController();
  const timer = setTimeout(() => ctrl.abort(), timeoutMs);
  const t0 = Date.now();
  try {
    const res = await fetch(url, { signal: ctrl.signal, cache: "no-store" as any });
    const ms = Date.now() - t0;
    const body = await res.text();
    return { ok: res.ok, ms, body };
  } catch {
    return { ok: false, ms: Date.now() - t0 };
  } finally {
    clearTimeout(timer);
  }
}

export type InternetProbe = {
  status: "ok" | "degraded" | "down";
  summary: string;
  reachable: boolean;
  latencyMs: number | null;
  dnsOverheadMs: number | null;
  publicIp: string | null;
  location: string | null;
  throughputMbps: number | null;
};

export async function runInternetProbes(withThroughput = true): Promise<InternetProbe> {
  // Latency via Cloudflare trace by IP (DNS excluded).
  const byIp = await fetchTimed("https://1.1.1.1/cdn-cgi/trace", 6000);
  const reachable = byIp.ok;
  const latencyMs = byIp.ok ? byIp.ms : null;

  let publicIp: string | null = null;
  let location: string | null = null;
  if (byIp.body) {
    const ipM = byIp.body.match(/(?:^|\n)ip=([^\n]+)/);
    const locM = byIp.body.match(/(?:^|\n)loc=([^\n]+)/);
    if (ipM) publicIp = ipM[1].trim();
    if (locM) location = locM[1].trim();
  }

  // DNS overhead: same endpoint by hostname (forces a resolve) minus IP timing.
  let dnsOverheadMs: number | null = null;
  const byHost = await fetchTimed("https://cloudflare.com/cdn-cgi/trace", 6000);
  if (byHost.ok && byIp.ok) dnsOverheadMs = Math.max(0, byHost.ms - byIp.ms);

  // Throughput: small download to keep mobile data modest.
  let throughputMbps: number | null = null;
  if (withThroughput && reachable) {
    const bytes = 3000000; // 3 MB
    const t0 = Date.now();
    const dl = await fetchTimed(`https://speed.cloudflare.com/__down?bytes=${bytes}`, 25000);
    if (dl.ok) {
      const secs = (Date.now() - t0) / 1000;
      const got = dl.body ? dl.body.length : bytes;
      if (secs > 0) throughputMbps = (got * 8) / 1000000 / secs;
    }
  }

  const issues: string[] = [];
  let status: InternetProbe["status"] = "ok";
  if (!reachable) {
    status = "down";
    issues.push("cannot reach the internet");
  } else {
    if (latencyMs != null && latencyMs > 300) {
      status = "degraded";
      issues.push(`high latency (${latencyMs}ms)`);
    }
    if (dnsOverheadMs != null && dnsOverheadMs > 400) {
      status = "degraded";
      issues.push(`slow DNS (+${dnsOverheadMs}ms)`);
    }
    if (throughputMbps != null && throughputMbps < 2) {
      status = "degraded";
      issues.push(`low throughput (${throughputMbps.toFixed(1)} Mbps)`);
    }
  }
  let summary: string;
  if (status === "down") summary = "Internet appears down";
  else if (status === "degraded") summary = "Degraded: " + issues.join("; ");
  else {
    const parts: string[] = [];
    if (latencyMs != null) parts.push(`${latencyMs}ms`);
    if (throughputMbps != null) parts.push(`${throughputMbps.toFixed(1)} Mbps`);
    summary = parts.length ? `OK (${parts.join(", ")})` : "OK";
  }

  return { status, summary, reachable, latencyMs, dnsOverheadMs, publicIp, location, throughputMbps };
}
