// Connection / Network — one screen for the phone's connectivity AND the
// connected Yaver runner's network. Phone-side (WiFi/IP/LAN/internet) is read
// locally via netinfo + fetch probes; the runner's network is pulled over the
// live MCP transport (network_interfaces / ip_route / ping / dns_lookup) via
// callMcpDirect, so you can see both ends of the link from your phone.
import React, { useCallback, useEffect, useState } from "react";
import { ActivityIndicator, Pressable, RefreshControl, ScrollView, StyleSheet, Text, View } from "react-native";
import { useRouter } from "expo-router";
import { AppScreenHeader } from "../src/components/AppScreenHeader";
import { useColors } from "../src/context/ThemeContext";
import type { ThemeColors } from "../src/constants/colors";
import { useDevice } from "../src/context/DeviceContext";
import { useAuth } from "../src/context/AuthContext";
import { isDeviceAsleep, useMachineLifecycle } from "../src/lib/wakeMachine";
import WakeProgress from "../src/components/WakeProgress";
import { quicClient } from "../src/lib/quic";
import { callMcpDirect } from "../src/lib/yaverMcpDirect";
import { getConvexSiteUrl } from "../src/lib/auth";
import {
  getDeviceNetwork,
  netInfoAvailable,
  runInternetProbes,
  runNetDoctor,
  type DeviceNetwork,
  type InternetProbe,
  type NetDoctorReport,
  type NetLayer,
  type NetLayerStatus,
} from "../src/lib/netdiag";

type RunnerNet = {
  interfaces?: string;
  route?: string;
  ping?: string;
  dns?: string;
  publicIp?: string;
  wifi?: string;
  speed?: string;
  error?: string;
};

// Pull a human string out of whatever shape a tool handler returned.
function toText(r: { ok: boolean; result?: unknown; error?: string }): string {
  if (!r.ok) return `error: ${r.error ?? "failed"}`;
  const v = r.result as any;
  if (v == null) return "—";
  if (typeof v === "string") return v.trim() || "—";
  if (typeof v === "object") {
    return (
      v.output ?? v.records ?? v.result ?? v.ip ?? v.text ?? JSON.stringify(v, null, 2)
    );
  }
  return String(v);
}

// ---- Yaver Connectivity (Doctor) ----------------------------------------
// Phone-DIRECT probes that work even when no device is connected (the exact
// broken state where the MCP-based runner diagnostics above are useless).
// Answers: is the backend reachable? is my token valid? can I reach the
// relay? how many of my devices are online? — the four things that would
// have made the "signed-in but zero devices" bug obvious at a glance.
type ProbeResult = { label: string; ok: boolean; detail: string; ms?: number };
type YaverDoctorReport = {
  backend: ProbeResult;
  auth: ProbeResult;
  relay: ProbeResult[];
  devices: { total: number; online: number; offline: number };
  tailscaleHint: string;
};

async function timedFetch(
  url: string,
  init?: RequestInit,
  timeoutMs = 8000,
): Promise<{ status: number; ms: number; body?: any; error?: string }> {
  const controller = new AbortController();
  const t = setTimeout(() => controller.abort(), timeoutMs);
  const start = Date.now();
  try {
    const res = await fetch(url, { ...init, signal: controller.signal });
    const ms = Date.now() - start;
    let body: any;
    try {
      body = await res.json();
    } catch {
      /* non-JSON body is fine — we only need the status */
    }
    return { status: res.status, ms, body };
  } catch (e: any) {
    return {
      status: 0,
      ms: Date.now() - start,
      error: e?.name === "AbortError" ? `timeout after ${timeoutMs}ms` : String(e?.message || e),
    };
  } finally {
    clearTimeout(t);
  }
}

async function runYaverDoctor(
  token: string | null,
  devices: any[],
  relays: { httpUrl?: string }[],
): Promise<YaverDoctorReport> {
  const site = getConvexSiteUrl();
  let backend: ProbeResult;
  let auth: ProbeResult;
  if (!token) {
    backend = { label: "Backend", ok: false, detail: "Can't verify — no auth token on this device." };
    auth = { label: "Auth token", ok: false, detail: "Missing — app is signed in without a bearer. Sign out and back in." };
  } else {
    // /devices/list is the exact call the app depends on — probing it tests
    // backend reachability AND token validity in one round-trip.
    const r = await timedFetch(`${site}/devices/list?_=${Date.now()}`, {
      headers: { Authorization: `Bearer ${token}`, "Cache-Control": "no-cache, no-store", Connection: "close" },
    });
    if (r.status === 200) {
      const arr = Array.isArray(r.body?.devices) ? r.body.devices : Array.isArray(r.body) ? r.body : [];
      backend = { label: "Backend", ok: true, detail: `Reachable — returned ${arr.length} device(s)`, ms: r.ms };
      auth = { label: "Auth token", ok: true, detail: "Valid — backend accepted the bearer.", ms: r.ms };
    } else if (r.status === 401 || r.status === 403) {
      backend = { label: "Backend", ok: true, detail: `Reachable (HTTP ${r.status})`, ms: r.ms };
      auth = { label: "Auth token", ok: false, detail: `Rejected (HTTP ${r.status}) — expired/revoked. Sign out and back in.`, ms: r.ms };
    } else if (r.status === 0) {
      backend = { label: "Backend", ok: false, detail: `Unreachable — ${r.error}`, ms: r.ms };
      auth = { label: "Auth token", ok: false, detail: "Could not verify (backend unreachable)." };
    } else {
      backend = { label: "Backend", ok: false, detail: `Unexpected HTTP ${r.status}`, ms: r.ms };
      auth = { label: "Auth token", ok: false, detail: `Backend returned ${r.status}.` };
    }
  }
  // Relay reachability — fall back to the public relay when the account has
  // none configured (so the row is never empty).
  const relayUrls = relays.map((r) => r.httpUrl).filter(Boolean) as string[];
  if (relayUrls.length === 0) relayUrls.push("https://public.yaver.io");
  const relay: ProbeResult[] = await Promise.all(
    relayUrls.map(async (u) => {
      const host = u.replace(/^https?:\/\//, "");
      const r = await timedFetch(u, { method: "GET" }, 6000);
      if (r.status > 0) return { label: host, ok: r.status < 500, detail: `HTTP ${r.status}`, ms: r.ms };
      return { label: host, ok: false, detail: r.error || "unreachable", ms: r.ms };
    }),
  );
  const online = devices.filter((d) => d?.online).length;
  const tsDevices = devices.filter(
    (d) => Array.isArray(d?.lanIps) && d.lanIps.some((ip: string) => typeof ip === "string" && ip.startsWith("100.")),
  ).length;
  return {
    backend,
    auth,
    relay,
    devices: { total: devices.length, online, offline: devices.length - online },
    tailscaleHint:
      tsDevices > 0
        ? `${tsDevices} device(s) advertise a Tailscale (100.x) address — reachable only while this phone's Tailscale is connected. On cellular without Tailscale, connect over the relay instead.`
        : "No Tailscale-addressed devices detected. On cellular without Tailscale, devices connect over the relay.",
  };
}

function DoctorRow({ c, s, p }: { c: ThemeColors; s: any; p: ProbeResult }) {
  const color = p.ok ? c.success : c.error;
  return (
    <View style={s.row}>
      <Text style={[s.rowLabel, { color: c.textSecondary }]}>{p.label}</Text>
      <View style={{ flexDirection: "row", alignItems: "center", gap: 8, flexShrink: 1, justifyContent: "flex-end" }}>
        <View style={[s.dot, { backgroundColor: color }]} />
        <Text style={{ fontSize: 12.5, fontWeight: "600", color, flexShrink: 1, textAlign: "right" }}>
          {p.detail}{p.ms != null ? ` · ${p.ms}ms` : ""}
        </Text>
      </View>
    </View>
  );
}

export default function ConnectionScreen() {
  const c = useColors();
  const s = makeStyles(c);
  const router = useRouter();
  const dev = useDevice() as any;
  const { token } = useAuth();
  const connectionStatus: string = dev?.connectionStatus ?? "disconnected";
  const activeDevice = dev?.activeDevice ?? null;
  const lastError: string | null = dev?.lastError ?? null;
  const connected = connectionStatus === "connected";

  // A managed box that auto-off'd (self-park after idle) reports
  // machineStatus "paused"/"stopped" and has no live endpoint — that's why
  // the runner reads DISCONNECTED with a dead host. Detect it so we can
  // explain "asleep, not broken" and offer a one-tap Wake instead of a
  // bare error + `http://null:null`.
  const devicesPool: any[] = dev?.devices ?? [];
  // Keep a live ref so the doctor probe reads the freshest device list
  // without forcing loadAll to re-create on every render.
  const devicesRef = React.useRef(devicesPool);
  devicesRef.current = devicesPool;
  const primaryDeviceId: string | null = dev?.primaryDeviceId ?? null;
  const sleepingDevice = React.useMemo(() => {
    if (activeDevice && isDeviceAsleep(activeDevice)) return activeDevice;
    const prim = devicesPool.find((d) => d?.id === primaryDeviceId);
    if (prim && isDeviceAsleep(prim)) return prim;
    return devicesPool.find((d) => isDeviceAsleep(d)) ?? null;
  }, [devicesPool, activeDevice, primaryDeviceId]);

  // Managed box that's a candidate for a park (close-down) action: the
  // connected active box if it's managed. Wake targets the sleeping box;
  // park targets the connected managed box.
  const managedActive = activeDevice?.managed ? activeDevice : null;
  const lifecycle = useMachineLifecycle({
    token,
    device: (sleepingDevice ?? managedActive) as any,
    deviceReachable: connected,
    onTick: dev?.refreshDevices,
  });
  const running = lifecycle.direction !== null || lifecycle.phase === "error";

  const [device, setDevice] = useState<DeviceNetwork | null>(null);
  const [internet, setInternet] = useState<InternetProbe | null>(null);
  const [doctor, setDoctor] = useState<NetDoctorReport | null>(null);
  const [runnerDoctor, setRunnerDoctor] = useState<any | null>(null);
  const [runner, setRunner] = useState<RunnerNet | null>(null);
  const [yaverDoc, setYaverDoc] = useState<YaverDoctorReport | null>(null);
  const [loading, setLoading] = useState(true);
  const [probing, setProbing] = useState(false);

  const loadAll = useCallback(async () => {
    setProbing(true);
    const [d, net, doc] = await Promise.all([
      getDeviceNetwork().catch(() => null),
      runInternetProbes(true).catch(() => null),
      runNetDoctor(true).catch(() => null),
    ]);
    setDevice(d);
    setInternet(net);
    setDoctor(doc);

    // Yaver connectivity doctor — phone-direct, works with no device connected.
    // Read devices from a ref so this callback stays stable (devicesPool is a
    // fresh array each render; depending on it would re-run the effect forever).
    runYaverDoctor(token, devicesRef.current, quicClient.relayServersSnapshot ?? [])
      .then(setYaverDoc)
      .catch(() => setYaverDoc(null));

    // Runner-side network — only if a runner is actually connected.
    if (connected && quicClient.baseUrl) {
      const rn: RunnerNet = {};
      // Deep layered diagnosis of the runner's own connectivity.
      callMcpDirect("net_doctor", {})
        .then((r) => setRunnerDoctor(r.ok ? r.result : null))
        .catch(() => setRunnerDoctor(null));
      try {
        const [ifaces, route, ping, dns, pub, wifi, speed] = await Promise.all([
          callMcpDirect("network_interfaces", {}),
          callMcpDirect("ip_route", {}),
          callMcpDirect("ping", { host: "1.1.1.1", count: 3 }),
          callMcpDirect("dns_lookup", { host: "cloudflare.com", type: "A" }),
          callMcpDirect("public_ip", {}),
          callMcpDirect("wifi_info", {}),
          callMcpDirect("speed_test", {}),
        ]);
        rn.interfaces = toText(ifaces);
        rn.route = toText(route);
        rn.ping = toText(ping);
        rn.dns = toText(dns);
        rn.publicIp = toText(pub);
        rn.wifi = toText(wifi);
        rn.speed = toText(speed);
      } catch (e: any) {
        rn.error = e?.message ?? String(e);
      }
      setRunner(rn);
    } else {
      setRunner(null);
      setRunnerDoctor(null);
    }
    setProbing(false);
    setLoading(false);
  }, [connected, token]);

  useEffect(() => {
    loadAll();
  }, [loadAll]);

  const statusColors = (status: string) => {
    switch (status) {
      case "ok":
      case "connected":
        return { text: c.success, bg: c.successBg, dot: c.success };
      case "degraded":
      case "connecting":
        return { text: c.warn, bg: c.warnBg, dot: c.warn };
      case "down":
      case "error":
      case "disconnected":
        return { text: c.error, bg: c.errorBg, dot: c.error };
      default:
        return { text: c.textMuted, bg: c.bgCardElevated, dot: c.textMuted };
    }
  };

  const Row = ({ label, value, valueColor }: { label: string; value: React.ReactNode; valueColor?: string }) => (
    <>
      <View style={s.divider} />
      <View style={s.row}>
        <Text style={[s.rowLabel, { color: c.textSecondary }]}>{label}</Text>
        {typeof value === "string" || typeof value === "number" ? (
          <Text style={[s.rowValue, { color: valueColor || c.textPrimary }]}>{value}</Text>
        ) : (
          value
        )}
      </View>
    </>
  );

  const Mono = ({ text }: { text?: string }) =>
    text ? (
      <Text style={[s.mono, { color: c.textSecondary }]} selectable>
        {text.length > 1600 ? text.slice(0, 1600) + "\n…" : text}
      </Text>
    ) : null;

  const layerColor = (st: NetLayerStatus | string) => {
    switch (st) {
      case "ok":
        return c.success;
      case "warn":
        return c.warn;
      case "fail":
        return c.error;
      default:
        return c.textMuted;
    }
  };
  const layerGlyph = (st: NetLayerStatus | string) =>
    st === "ok" ? "✓" : st === "warn" ? "!" : st === "fail" ? "✗" : "·";

  // Renders a net_doctor-shaped report (phone OR runner). Loosely typed so the
  // runner's snake_case JSON (root_cause) and the phone's camelCase both work.
  const DoctorCard = ({ rep }: { rep: any }) => {
    if (!rep) return null;
    const overall: string = rep.status ?? "unknown";
    const layers: NetLayer[] = rep.layers ?? [];
    const remediation: string[] = rep.remediation ?? [];
    return (
      <View style={[s.card, { backgroundColor: c.bgCard, borderColor: c.border }]}>
        <View style={[s.verdict, { backgroundColor: statusColors(overall === "fail" ? "down" : overall === "warn" ? "degraded" : "ok").bg }]}>
          <Text style={{ fontSize: 13, fontWeight: "700", color: layerColor(overall) }}>
            {overall === "ok" ? "✓ Online" : overall === "warn" ? "! Degraded" : overall === "fail" ? "✗ Problem found" : "Diagnosing…"}
          </Text>
          {!!rep.verdict && <Text style={{ fontSize: 12, color: c.textSecondary, marginTop: 3 }}>{rep.verdict}</Text>}
        </View>
        {layers.map((l) => (
          <View key={l.name} style={s.layerRow}>
            <Text style={{ color: layerColor(l.status), fontWeight: "700", width: 16, fontSize: 13 }}>{layerGlyph(l.status)}</Text>
            <View style={{ flex: 1 }}>
              <Text style={{ fontSize: 12.5, fontWeight: "600", color: c.textPrimary }}>{l.title}</Text>
              <Text style={{ fontSize: 12, color: c.textSecondary, marginTop: 1 }}>{l.detail}</Text>
              {!!l.hint && (l.status === "fail" || l.status === "warn") && (
                <Text style={{ fontSize: 12, color: c.warn, marginTop: 2 }}>→ {l.hint}</Text>
              )}
            </View>
          </View>
        ))}
        {remediation.length > 0 && (
          <View style={[s.fixBox, { borderColor: c.border }]}>
            <Text style={[s.subhead, { color: c.textMuted, marginTop: 0 }]}>WHAT TO DO</Text>
            {remediation.map((r, i) => (
              <Text key={i} style={{ fontSize: 12.5, color: c.textPrimary, marginTop: 4 }}>• {r}</Text>
            ))}
          </View>
        )}
      </View>
    );
  };

  const wifi = device?.type === "wifi";

  // Headline = phone's internet status (the thing a user means by "is my net ok").
  const headline = internet?.status ?? (loading ? "unknown" : "down");

  if (loading) {
    return (
      <View style={{ flex: 1, backgroundColor: c.bg }}>
        <AppScreenHeader title="Connection / Network" onBack={() => router.back()} />
        <ActivityIndicator color={c.textMuted} style={{ marginTop: 48 }} />
        <Text style={{ textAlign: "center", color: c.textMuted, marginTop: 12, fontSize: 13 }}>
          Testing connectivity…
        </Text>
      </View>
    );
  }

  return (
    <View style={{ flex: 1, backgroundColor: c.bg }}>
      <AppScreenHeader title="Connection / Network" onBack={() => router.back()} />
      <ScrollView
        contentContainerStyle={s.content}
        refreshControl={<RefreshControl refreshing={probing} onRefresh={loadAll} tintColor={c.textMuted} />}
      >
        {/* Headline */}
        <View style={[s.hero, { backgroundColor: statusColors(headline).bg }]}>
          <View style={[s.heroDot, { backgroundColor: statusColors(headline).dot }]} />
          <View style={{ flex: 1 }}>
            <Text style={[s.heroTitle, { color: statusColors(headline).text }]}>
              {headline === "ok"
                ? "Internet OK"
                : headline === "degraded"
                ? "Connectivity degraded"
                : headline === "down"
                ? "No internet"
                : "Unknown"}
            </Text>
            {internet && <Text style={{ color: c.textMuted, fontSize: 12, marginTop: 2 }}>{internet.summary}</Text>}
          </View>
        </View>

        {/* Deep troubleshoot — phone */}
        <Text style={[s.section, { color: c.textMuted }]}>TROUBLESHOOT (THIS PHONE)</Text>
        {doctor ? (
          <DoctorCard rep={doctor} />
        ) : (
          <View style={[s.card, { backgroundColor: c.bgCard, borderColor: c.border }]}>
            <Text style={{ fontSize: 13, color: c.textMuted, paddingVertical: 8 }}>Running layered diagnosis…</Text>
          </View>
        )}

        {/* Yaver connectivity doctor — phone-direct, works with no device connected */}
        <Text style={[s.section, { color: c.textMuted }]}>YAVER CONNECTIVITY</Text>
        <View style={[s.card, { backgroundColor: c.bgCard, borderColor: c.border }]}>
          {yaverDoc ? (
            <>
              <DoctorRow c={c} s={s} p={yaverDoc.backend} />
              <DoctorRow c={c} s={s} p={yaverDoc.auth} />
              <View style={s.row}>
                <Text style={[s.rowLabel, { color: c.textSecondary }]}>Devices</Text>
                <Text style={[s.rowValue, { color: c.textPrimary }]}>
                  {yaverDoc.devices.online}/{yaverDoc.devices.total} online
                </Text>
              </View>
              {yaverDoc.relay.map((r, i) => (
                <DoctorRow key={i} c={c} s={s} p={{ ...r, label: `Relay · ${r.label}` }} />
              ))}
              <Text style={{ fontSize: 12, color: c.textMuted, marginTop: 8, lineHeight: 17 }}>
                {yaverDoc.tailscaleHint}
              </Text>
            </>
          ) : (
            <Text style={{ fontSize: 13, color: c.textMuted, paddingVertical: 8 }}>Probing backend, auth & relay…</Text>
          )}
        </View>

        {/* This phone */}
        <Text style={[s.section, { color: c.textMuted }]}>THIS PHONE / LOCAL NETWORK</Text>
        <View style={[s.card, { backgroundColor: c.bgCard, borderColor: c.border }]}>
          {device ? (
            <>
              <View style={s.row}>
                <Text style={[s.rowLabel, { color: c.textSecondary }]}>Connection</Text>
                <Text style={[s.rowValue, { color: c.textPrimary }]}>{device.type}</Text>
              </View>
              <Row
                label="Internet reachable"
                value={
                  <View style={{ flexDirection: "row", alignItems: "center", gap: 8 }}>
                    <View style={[s.dot, { backgroundColor: device.isInternetReachable ? c.success : device.isInternetReachable === false ? c.error : c.textMuted }]} />
                    <Text style={{ fontSize: 13, fontWeight: "600", color: device.isInternetReachable ? c.success : device.isInternetReachable === false ? c.error : c.textMuted }}>
                      {device.isInternetReachable == null ? "Unknown" : device.isInternetReachable ? "Yes" : "No"}
                    </Text>
                  </View>
                }
              />
              {wifi && device.ssid != null && <Row label="WiFi (SSID)" value={device.ssid || "hidden"} />}
              {wifi && device.strength != null && <Row label="Signal" value={`${device.strength}%`} />}
              {wifi && device.frequency != null && <Row label="Frequency" value={`${device.frequency} MHz`} />}
              {wifi && device.linkSpeedMbps != null && <Row label="Link speed" value={`${device.linkSpeedMbps} Mbps`} />}
              {device.ipAddress != null && <Row label="IP address (DHCP)" value={device.ipAddress || "—"} />}
              {device.subnet != null && <Row label="Subnet mask" value={device.subnet || "—"} />}
              {device.gateway != null && <Row label="Gateway (derived)" value={device.gateway} />}
              {device.type === "cellular" && device.cellularGeneration != null && <Row label="Cellular" value={String(device.cellularGeneration).toUpperCase()} />}
              {device.type === "cellular" && device.carrier != null && <Row label="Carrier" value={device.carrier || "—"} />}
            </>
          ) : (
            <Text style={{ fontSize: 13, color: c.textMuted, paddingVertical: 8 }}>
              {netInfoAvailable() ? "Could not read device network info" : "Device network details require an app update."}
            </Text>
          )}
        </View>

        {/* Internet */}
        <Text style={[s.section, { color: c.textMuted }]}>INTERNET</Text>
        <View style={[s.card, { backgroundColor: c.bgCard, borderColor: c.border }]}>
          {internet ? (
            <>
              <View style={s.row}>
                <Text style={[s.rowLabel, { color: c.textSecondary }]}>Status</Text>
                <View style={[s.badge, { backgroundColor: statusColors(internet.status).bg }]}>
                  <Text style={{ fontSize: 12, fontWeight: "700", color: statusColors(internet.status).text }}>{internet.status.toUpperCase()}</Text>
                </View>
              </View>
              {internet.latencyMs != null && <Row label="Latency (1.1.1.1)" value={`${internet.latencyMs} ms`} />}
              {internet.dnsOverheadMs != null && <Row label="DNS overhead" value={`+${internet.dnsOverheadMs} ms`} />}
              {internet.throughputMbps != null && <Row label="Download" value={`${internet.throughputMbps.toFixed(1)} Mbps`} />}
              {internet.publicIp && <Row label="Public IP" value={internet.publicIp} />}
              {internet.location && <Row label="Location" value={internet.location} />}
            </>
          ) : (
            <Text style={{ fontSize: 13, color: c.error, paddingVertical: 8 }}>Internet unreachable</Text>
          )}
        </View>

        {/* Yaver runner connection */}
        <Text style={[s.section, { color: c.textMuted }]}>YAVER RUNNER</Text>
        <View style={[s.card, { backgroundColor: c.bgCard, borderColor: c.border }]}>
          <View style={s.row}>
            <Text style={[s.rowLabel, { color: c.textSecondary }]}>Status</Text>
            <View style={[s.badge, { backgroundColor: statusColors(connectionStatus).bg }]}>
              <Text style={{ fontSize: 12, fontWeight: "700", color: statusColors(connectionStatus).text }}>{connectionStatus.toUpperCase()}</Text>
            </View>
          </View>
          {activeDevice?.name && <Row label="Device" value={activeDevice.name} />}
          {quicClient.connectionMode && <Row label="Transport" value={String(quicClient.connectionMode)} />}
          {quicClient.networkType && <Row label="Path network" value={String(quicClient.networkType)} />}
          {/* A stale `http://null:null` (no active device / paused box) is noise —
              only show a real endpoint. */}
          {quicClient.baseUrl && !/null/i.test(String(quicClient.baseUrl)) && (
            <Row label="Endpoint" value={quicClient.baseUrl} />
          )}
          {lastError && !running && <Row label="Last error" value={lastError} valueColor={c.error} />}

          {/* Live wake/park ladder — full variant with labelled steps + a
              network line. Shows whenever a run is in flight (from here or
              any other surface). */}
          {running && <WakeProgress state={lifecycle} />}

          {/* Asleep + not mid-run → offer Wake. */}
          {!running && !connected && sleepingDevice && (
            <View style={{ paddingTop: 10, gap: 8 }}>
              <Text style={{ fontSize: 13, color: c.textSecondary, lineHeight: 18 }}>
                {sleepingDevice.name || "Your box"} is asleep — it auto-off'd after idle to save cost
                (nothing is broken). Wake it to reconnect.
              </Text>
              <Pressable
                onPress={lifecycle.wake}
                disabled={lifecycle.busy}
                style={{
                  alignSelf: "flex-start",
                  paddingHorizontal: 16,
                  paddingVertical: 9,
                  borderRadius: 10,
                  borderWidth: 1,
                  borderColor: c.accent,
                  backgroundColor: c.accentSoft,
                  opacity: lifecycle.busy ? 0.6 : 1,
                }}
              >
                <Text style={{ color: c.accent, fontWeight: "700", fontSize: 14 }}>Wake box</Text>
              </Pressable>
            </View>
          )}

          {/* Connected managed box → offer Park (close down) with the same
              cool progress treatment on the way down. */}
          {!running && connected && managedActive && (
            <View style={{ paddingTop: 10, gap: 8 }}>
              <Text style={{ fontSize: 12, color: c.textMuted, lineHeight: 16 }}>
                Done for now? Park {managedActive.name || "your box"} to stop the meter — it snapshots,
                powers down, and wakes back in ~1–2 min next time.
              </Text>
              <Pressable
                onPress={lifecycle.park}
                disabled={lifecycle.busy}
                style={{
                  alignSelf: "flex-start",
                  paddingHorizontal: 14,
                  paddingVertical: 8,
                  borderRadius: 10,
                  borderWidth: 1,
                  borderColor: c.border,
                  backgroundColor: c.bgInput,
                  opacity: lifecycle.busy ? 0.6 : 1,
                }}
              >
                <Text style={{ color: c.textSecondary, fontWeight: "700", fontSize: 13 }}>⏸ Park box</Text>
              </Pressable>
            </View>
          )}
        </View>

        {/* Deep troubleshoot — runner */}
        {connected && (
          <>
            <Text style={[s.section, { color: c.textMuted }]}>TROUBLESHOOT (RUNNER)</Text>
            {runnerDoctor ? (
              <DoctorCard rep={runnerDoctor} />
            ) : (
              <View style={[s.card, { backgroundColor: c.bgCard, borderColor: c.border }]}>
                <View style={{ flexDirection: "row", alignItems: "center", gap: 8, paddingVertical: 8 }}>
                  <ActivityIndicator color={c.textMuted} size="small" />
                  <Text style={{ fontSize: 13, color: c.textMuted }}>Diagnosing runner connectivity…</Text>
                </View>
              </View>
            )}
          </>
        )}

        {/* Runner network (over MCP) */}
        {connected && (
          <>
            <Text style={[s.section, { color: c.textMuted }]}>RUNNER NETWORK (over MCP)</Text>
            <View style={[s.card, { backgroundColor: c.bgCard, borderColor: c.border }]}>
              {runner ? (
                runner.error ? (
                  <Text style={{ fontSize: 13, color: c.error, paddingVertical: 8 }}>{runner.error}</Text>
                ) : (
                  <>
                    {runner.publicIp && <Row label="Public IP" value={runner.publicIp} />}
                    {runner.speed && (<><Text style={[s.subhead, { color: c.textMuted }]}>SPEED TEST</Text><Mono text={runner.speed} /></>)}
                    {runner.wifi && (<><Text style={[s.subhead, { color: c.textMuted }]}>WIFI</Text><Mono text={runner.wifi} /></>)}
                    {runner.ping && (<><Text style={[s.subhead, { color: c.textMuted }]}>PING 1.1.1.1</Text><Mono text={runner.ping} /></>)}
                    {runner.dns && (<><Text style={[s.subhead, { color: c.textMuted }]}>DNS cloudflare.com</Text><Mono text={runner.dns} /></>)}
                    {runner.interfaces && (<><Text style={[s.subhead, { color: c.textMuted }]}>INTERFACES</Text><Mono text={runner.interfaces} /></>)}
                    {runner.route && (<><Text style={[s.subhead, { color: c.textMuted }]}>ROUTES</Text><Mono text={runner.route} /></>)}
                  </>
                )
              ) : (
                <View style={{ flexDirection: "row", alignItems: "center", gap: 8, paddingVertical: 8 }}>
                  <ActivityIndicator color={c.textMuted} size="small" />
                  <Text style={{ fontSize: 13, color: c.textMuted }}>Querying runner…</Text>
                </View>
              )}
            </View>
          </>
        )}

        <Text style={{ fontSize: 11, color: c.textMuted, textAlign: "center", marginTop: 8 }}>
          Phone tests run on this device; runner network is read over the live MCP link.
        </Text>
      </ScrollView>
    </View>
  );
}

function makeStyles(c: ThemeColors) {
  return StyleSheet.create({
    content: { padding: 16, gap: 12, paddingBottom: 40 },
    hero: { flexDirection: "row", alignItems: "center", gap: 12, borderRadius: 14, padding: 16 },
    heroDot: { width: 14, height: 14, borderRadius: 7 },
    heroTitle: { fontSize: 17, fontWeight: "700" },
    section: { fontSize: 12, fontWeight: "600", textTransform: "uppercase", letterSpacing: 0.8, paddingHorizontal: 4, marginTop: 4 },
    subhead: { fontSize: 11, fontWeight: "600", textTransform: "uppercase", letterSpacing: 0.6, marginTop: 10, marginBottom: 4 },
    card: { borderWidth: 1, borderRadius: 14, padding: 16 },
    row: { flexDirection: "row", justifyContent: "space-between", alignItems: "center", paddingVertical: 10 },
    rowLabel: { fontSize: 13, fontWeight: "500" },
    rowValue: { fontSize: 13, maxWidth: "62%", textAlign: "right" },
    divider: { height: StyleSheet.hairlineWidth, backgroundColor: "rgba(127,127,127,0.2)" },
    badge: { paddingHorizontal: 10, paddingVertical: 4, borderRadius: 8 },
    verdict: { borderRadius: 10, padding: 12, marginBottom: 6 },
    layerRow: { flexDirection: "row", alignItems: "flex-start", gap: 6, paddingVertical: 7 },
    fixBox: { borderTopWidth: StyleSheet.hairlineWidth, marginTop: 8, paddingTop: 8 },
    dot: { width: 10, height: 10, borderRadius: 5 },
    mono: { fontSize: 11, fontFamily: "Menlo", lineHeight: 15 },
  });
}
