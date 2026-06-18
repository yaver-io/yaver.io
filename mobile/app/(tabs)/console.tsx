import React, { useEffect, useMemo, useState } from "react";
import { ActivityIndicator, Alert, Pressable, ScrollView, StyleSheet, Text, TextInput, View } from "react-native";
import Svg, { Polyline } from "react-native-svg";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { useRouter } from "expo-router";
import { AppScreenHeader } from "../../src/components/AppScreenHeader";
import { useAuth } from "../../src/context/AuthContext";
import { useDevice, type Device } from "../../src/context/DeviceContext";
import { useColors } from "../../src/context/ThemeContext";
import { quicClient } from "../../src/lib/quic";
import {
  wifiBanClient,
  wifiBannedClients,
  wifiCapabilities,
  wifiClients,
  wifiGetAPSTAConfig,
  wifiKickClient,
  wifiSetAPSTAConfig,
  wifiStart,
  wifiStatus,
  wifiStop,
  wifiUnbanClient,
  type WiFiBan,
  type WiFiCapabilities,
  type WiFiClient,
  type WiFiStatus,
} from "../../src/lib/wifiControl";

// Native mobile Console — all Docker/machine ops via RN components. No WebViews.

type Tab = "overview" | "machines" | "containers" | "catalog" | "mailpit" | "s3" | "wifi";
type WiFiTab = "hotspot" | "apsta" | "clients" | "diagnostics";

export default function ConsoleScreen() {
  const c = useColors();
  const router = useRouter();
  const insets = useSafeAreaInsets();
  const [tab, setTab] = useState<Tab>("overview");

  return (
    <View style={[styles.container, { backgroundColor: c.bg }]}>
      <AppScreenHeader title="Console" onBack={() => router.navigate("/(tabs)/more" as any)} style={{ paddingTop: insets.top + 12 }} />
      <View style={[styles.tabbar, { backgroundColor: c.bgCard, borderBottomColor: c.border }]}>
        <ScrollView horizontal showsHorizontalScrollIndicator={false}>
          {(["overview", "machines", "containers", "catalog", "mailpit", "s3", "wifi"] as Tab[]).map((t) => (
            <Pressable key={t} onPress={() => setTab(t)} style={{ paddingHorizontal: 12, paddingVertical: 10 }}>
              <Text style={{ fontSize: 13, fontWeight: "600", textTransform: "uppercase", color: tab === t ? c.accent : c.textMuted }}>{t}</Text>
            </Pressable>
          ))}
        </ScrollView>
      </View>
      <ScrollView contentContainerStyle={{ padding: 16, paddingBottom: 32 }}>
        {tab === "overview" && <OverviewTab c={c} />}
        {tab === "machines" && <MachinesTab c={c} />}
        {tab === "containers" && <ContainersTab c={c} />}
        {tab === "catalog" && <CatalogTab c={c} />}
        {tab === "mailpit" && <MailpitTab c={c} />}
        {tab === "s3" && <S3Tab c={c} />}
        {tab === "wifi" && <WiFiTabScreen c={c} />}
      </ScrollView>
    </View>
  );
}

async function call(path: string, init: RequestInit = {}): Promise<any> {
  const res = await fetch(`${quicClient.baseUrl}${path}`, {
    ...init,
    headers: { ...quicClient.getAuthHeaders(), "Content-Type": "application/json", ...(init.headers || {}) },
  });
  return res.json();
}

function OverviewTab({ c }: { c: any }) {
  const [m, setM] = useState<any>(null);
  const [history, setHistory] = useState<any[]>([]);
  const [window, setWindow] = useState("1h");
  useEffect(() => {
    loadMetrics();
    loadHistory();
    const i = setInterval(loadMetrics, 5000);
    const j = setInterval(loadHistory, 30000);
    return () => { clearInterval(i); clearInterval(j); };
  }, [window]);
  async function loadMetrics() { try { setM(await call("/console/metrics")); } catch {} }
  async function loadHistory() {
    try {
      const r = await call(`/console/metrics/history?window=${encodeURIComponent(window)}`);
      setHistory(r.samples || []);
    } catch {}
  }
  if (!m) return <ActivityIndicator color={c.accent} />;
  const cpuSeries = history.slice().reverse().map((s: any) => s.cpuPct || 0);
  const ramSeries = history.slice().reverse().map((s: any) => s.ramPct || 0);
  return (
    <View style={{ gap: 10 }}>
      <Card c={c} label="CPU" value={`${(m.cpuPct || 0).toFixed(1)}%`} sub={`${m.cores || 0} cores`} />
      <Card c={c} label="RAM" value={`${(m.ramPct || 0).toFixed(0)}%`} sub={`${fmtBytes(m.ramUsed)} / ${fmtBytes(m.ramTotal)}`} />
      <Card c={c} label="Disk" value={`${(m.diskPct || 0).toFixed(0)}%`} sub={`${fmtBytes(m.diskUsed)} / ${fmtBytes(m.diskTotal)}`} />
      <Card c={c} label="Network" value={`↓ ${fmtBps(m.netRxBps)}`} sub={`↑ ${fmtBps(m.netTxBps)}`} />
      <View style={{ flexDirection: "row", gap: 6, marginTop: 8, flexWrap: "wrap" }}>
        {["15m", "1h", "6h", "24h", "7d"].map((w) => (
          <Pressable key={w} onPress={() => setWindow(w)}
            style={{ paddingHorizontal: 10, paddingVertical: 6, borderRadius: 6, backgroundColor: window === w ? c.accent + "30" : c.bgCard, borderWidth: 1, borderColor: window === w ? c.accent : c.border }}>
            <Text style={{ color: window === w ? c.accent : c.textMuted, fontSize: 11, fontWeight: "600" }}>{w}</Text>
          </Pressable>
        ))}
      </View>
      <Sparkline c={c} title="CPU history" series={cpuSeries} color="#818cf8" />
      <Sparkline c={c} title="RAM history" series={ramSeries} color="#34d399" />
      <Text style={{ color: c.textMuted, fontSize: 10, textAlign: "center" }}>{history.length} samples · 7-day retention</Text>
      <Text style={{ color: c.textMuted, fontSize: 11, textAlign: "center", marginTop: 8 }}>
        {m.hostname} · {m.os}
      </Text>
    </View>
  );
}

function Sparkline({ c, title, series, color }: { c: any; title: string; series: number[]; color: string }) {
  if (series.length === 0) {
    return (
      <View style={[card(c)]}>
        <Text style={{ color: c.textMuted, fontSize: 10 }}>{title}</Text>
        <Text style={{ color: c.textMuted, fontSize: 11, textAlign: "center", paddingVertical: 20 }}>no data yet</Text>
      </View>
    );
  }
  const w = 300, h = 80;
  const n = series.length;
  const max = Math.max(...series, 100);
  const points = series.map((v, i) => `${(i / Math.max(1, n - 1)) * w},${h - (v / max) * h}`).join(" ");
  const latest = series[n - 1];
  const avg = series.reduce((a, b) => a + b, 0) / n;
  const peak = Math.max(...series);
  return (
    <View style={[card(c)]}>
      <View style={{ flexDirection: "row", alignItems: "center", gap: 8, flexWrap: "wrap" }}>
        <Text style={{ color: c.textMuted, fontSize: 10, textTransform: "uppercase", fontWeight: "700" }}>{title}</Text>
        <Text style={{ color: c.textPrimary, fontSize: 11, marginLeft: "auto" }}>now <Text style={{ fontFamily: "Menlo" }}>{latest.toFixed(1)}%</Text></Text>
        <Text style={{ color: c.textMuted, fontSize: 10 }}>avg {avg.toFixed(1)}%</Text>
        <Text style={{ color: c.textMuted, fontSize: 10 }}>max {peak.toFixed(1)}%</Text>
      </View>
      <Svg width="100%" height={80} viewBox={`0 0 ${w} ${h}`} preserveAspectRatio="none" style={{ marginTop: 6 }}>
        <Polyline points={points} fill="none" stroke={color} strokeWidth={1.5} />
      </Svg>
    </View>
  );
}

function MachinesTab({ c }: { c: any }) {
  const [list, setList] = useState<any[]>([]);
  useEffect(() => { refresh(); const i = setInterval(refresh, 10000); return () => clearInterval(i); }, []);
  async function refresh() { try { setList((await call("/console/machines")).machines || []); } catch {} }
  // Read-only on mobile, like WhatsApp Web: you see and connect to
  // machines that already exist. Provisioning / teardown / billing all
  // live on the web dashboard — never in-app (App Store 3.1.3 + keeps
  // the phone a thin companion).
  return (
    <View style={{ gap: 8 }}>
      <Text style={{ color: c.textMuted, fontSize: 11 }}>Your machines — own hardware and cloud boxes as one list. Add or remove machines from the web dashboard.</Text>
      {list.map((m) => (
        <View key={m.deviceId} style={[card(c), m.isLocal && { borderColor: c.accent, borderWidth: 2 }]}>
          <View style={{ flexDirection: "row", gap: 8, alignItems: "center" }}>
            <Text style={{ fontSize: 20 }}>{providerIcon(m.provider)}</Text>
            <View style={{ flex: 1 }}>
              <Text style={{ color: c.textPrimary, fontWeight: "700" }}>{m.name}</Text>
              <Text style={{ color: c.textMuted, fontSize: 11 }} numberOfLines={1}>{m.platform}</Text>
            </View>
            <View style={{ width: 8, height: 8, borderRadius: 4, backgroundColor: m.isOnline ? "#10b981" : "#ef4444" }} />
          </View>
          <View style={{ flexDirection: "row", gap: 6, flexWrap: "wrap", marginTop: 6 }}>
            {m.isLocal && <Pill c={c} text="this machine" tone="accent" />}
            {m.arch && <Pill c={c} text={m.arch} tone="muted" />}
            {m.cost && <Pill c={c} text={m.cost} tone="muted" />}
          </View>
          {m.uptime > 0 && (
            <Text style={{ color: c.textMuted, fontSize: 10, marginTop: 4 }}>
              uptime: {Math.floor(m.uptime / 86400)}d {Math.floor((m.uptime % 86400) / 3600)}h
            </Text>
          )}
        </View>
      ))}
    </View>
  );
}

function ContainersTab({ c }: { c: any }) {
  const [list, setList] = useState<any[]>([]);
  const [showAll, setShowAll] = useState(false);
  useEffect(() => { refresh(); }, [showAll]);
  async function refresh() {
    try { setList((await call(`/console/containers${showAll ? "?all=1" : ""}`)).containers || []); } catch {}
  }
  async function act(id: string, action: string) {
    const r = await call("/console/containers/action", { method: "POST", body: JSON.stringify({ id, action }) });
    if (r.error) Alert.alert("Action failed", r.error);
    refresh();
  }
  async function prune() {
    Alert.alert("Prune?", "Remove stopped containers, dangling images, unused volumes.", [
      { text: "Cancel", style: "cancel" },
      { text: "Prune", style: "destructive", onPress: async () => {
        const r = await call("/console/prune", { method: "POST" });
        Alert.alert("Pruned", JSON.stringify(r));
        refresh();
      }},
    ]);
  }
  return (
    <View style={{ gap: 8 }}>
      <View style={{ flexDirection: "row", gap: 8 }}>
        <Pressable onPress={() => setShowAll(!showAll)} style={[actionBtn(c), { backgroundColor: c.bgCard, borderColor: c.border, borderWidth: 1, flex: 1 }]}>
          <Text style={{ color: c.textPrimary, fontSize: 12 }}>{showAll ? "✓ " : ""}include stopped</Text>
        </Pressable>
        <Pressable onPress={prune} style={[actionBtn(c), { backgroundColor: "#f59e0b20", paddingHorizontal: 14 }]}>
          <Text style={{ color: "#f59e0b", fontSize: 12 }}>Prune</Text>
        </Pressable>
      </View>
      {list.map((ct) => (
        <View key={ct.id} style={[card(c)]}>
          <View style={{ flexDirection: "row", gap: 8, alignItems: "center" }}>
            <Pill c={c} text={ct.state} tone={ct.state === "running" ? "ok" : "muted"} />
            <Text style={{ color: c.textPrimary, fontFamily: "Menlo", fontSize: 12, flex: 1 }} numberOfLines={1}>{ct.name}</Text>
          </View>
          <Text style={{ color: c.textMuted, fontFamily: "Menlo", fontSize: 10, marginTop: 2 }} numberOfLines={1}>{ct.image}</Text>
          {ct.ports?.filter((p: any) => p.public).length > 0 && (
            <Text style={{ color: c.textMuted, fontSize: 10, marginTop: 2 }}>
              {ct.ports.filter((p: any) => p.public).map((p: any) => `${p.public}→${p.private}`).join(", ")}
            </Text>
          )}
          <View style={{ flexDirection: "row", gap: 6, marginTop: 8 }}>
            {ct.state === "running" ? (
              <>
                <Pressable onPress={() => act(ct.id, "restart")} style={[actionBtn(c), { backgroundColor: c.bgCard, borderColor: c.border, borderWidth: 1, flex: 1 }]}>
                  <Text style={{ color: c.accent, fontSize: 12 }}>↻ Restart</Text>
                </Pressable>
                <Pressable onPress={() => act(ct.id, "stop")} style={[actionBtn(c), { backgroundColor: "#ef444420", flex: 1 }]}>
                  <Text style={{ color: "#ef4444", fontSize: 12 }}>⏹ Stop</Text>
                </Pressable>
              </>
            ) : (
              <Pressable onPress={() => act(ct.id, "start")} style={[actionBtn(c), { backgroundColor: "#10b98120", flex: 1 }]}>
                <Text style={{ color: "#10b981", fontSize: 12 }}>▶ Start</Text>
              </Pressable>
            )}
          </View>
        </View>
      ))}
    </View>
  );
}

function CatalogTab({ c }: { c: any }) {
  const [categories, setCategories] = useState<Record<string, any[]>>({});
  useEffect(() => { (async () => setCategories((await call("/console/catalog")).categories || {}))(); }, []);
  async function install(id: string) {
    Alert.alert("Install?", `Install ${id} service?`, [
      { text: "Cancel", style: "cancel" },
      { text: "Install", onPress: async () => {
        const r = await call("/console/catalog/install", { method: "POST", body: JSON.stringify({ id, fields: {} }) });
        Alert.alert(r.error ? "Failed" : "Installed", r.error || r.started || "");
      }},
    ]);
  }
  return (
    <View style={{ gap: 10 }}>
      <Text style={{ color: c.textMuted, fontSize: 11 }}>One-click installable services. Each becomes a Docker container or binary.</Text>
      {Object.entries(categories).map(([cat, list]) => (
        <View key={cat} style={{ gap: 6 }}>
          <Text style={{ color: c.accent, fontSize: 11, fontWeight: "700", textTransform: "uppercase", marginTop: 6 }}>{cat}</Text>
          {list.map((e: any) => (
            <Pressable key={e.id} onPress={() => install(e.id)} style={[card(c)]}>
              <Text style={{ color: c.textPrimary, fontWeight: "700" }}>{e.name}</Text>
              <Text style={{ color: c.textMuted, fontSize: 11 }} numberOfLines={2}>{e.description}</Text>
            </Pressable>
          ))}
        </View>
      ))}
    </View>
  );
}

// MultiRegionForm removed from mobile: provisioning billable cloud
// boxes from the phone is web-dashboard-only (App Store 3.1.3 — no
// in-app purchase of digital infra — and the phone stays a thin
// WhatsApp-Web-style companion to machines that already exist).

function MailpitTab({ c }: { c: any }) {
  const [list, setList] = useState<any[]>([]);
  const [total, setTotal] = useState(0);
  const [selected, setSelected] = useState<any>(null);
  async function refresh() { try { const r = await call("/mail/list"); setList(r.messages || []); setTotal(r.total || 0); } catch {} }
  useEffect(() => { refresh(); const i = setInterval(refresh, 5000); return () => clearInterval(i); }, []);
  async function del(id: string) {
    await call("/mail/delete", { method: "POST", body: JSON.stringify({ ids: [id] }) });
    setSelected(null); refresh();
  }
  async function loadDetail(id: string) {
    const r = await call(`/mail/message?id=${encodeURIComponent(id)}`);
    setSelected(r);
  }
  if (selected) {
    return (
      <View style={{ gap: 8 }}>
        <Pressable onPress={() => setSelected(null)}>
          <Text style={{ color: c.accent, fontSize: 13 }}>← Back</Text>
        </Pressable>
        <Text style={{ color: c.textPrimary, fontSize: 15, fontWeight: "700" }}>{selected.Subject || "(no subject)"}</Text>
        <Text style={{ color: c.textMuted, fontSize: 11 }}>From: {selected.From?.Address}</Text>
        <Text style={{ color: c.textMuted, fontSize: 11 }}>To: {(selected.To || []).map((t: any) => t.Address).join(", ")}</Text>
        <View style={[card(c), { marginTop: 6 }]}>
          <Text style={{ color: c.textPrimary, fontSize: 12 }}>{selected.Text || selected.HTML || "(empty)"}</Text>
        </View>
        <Pressable onPress={() => del(selected.ID)} style={[actionBtn(c), { backgroundColor: "#ef444420" }]}>
          <Text style={{ color: "#ef4444" }}>Delete</Text>
        </Pressable>
      </View>
    );
  }
  return (
    <View style={{ gap: 6 }}>
      <Text style={{ color: c.textMuted, fontSize: 11 }}>Dev mail caught by Mailpit ({total}) · /mail/list via agent</Text>
      {list.length === 0 && <Text style={{ color: c.textMuted, textAlign: "center", padding: 20 }}>No messages.</Text>}
      {list.map((m) => (
        <Pressable key={m.ID} onPress={() => loadDetail(m.ID)} style={[card(c)]}>
          <Text style={{ color: c.textPrimary, fontWeight: m.Read ? "400" : "700", fontSize: 13 }} numberOfLines={1}>
            {m.Subject || "(no subject)"}
          </Text>
          <Text style={{ color: c.textMuted, fontSize: 11 }} numberOfLines={1}>{m.From?.Address} → {(m.To?.[0]?.Address) || "?"}</Text>
          <Text style={{ color: c.textMuted, fontSize: 10, marginTop: 2 }}>{(m.Created || "").slice(0, 19)}</Text>
        </Pressable>
      ))}
    </View>
  );
}

function S3Tab({ c }: { c: any }) {
  const [bucket, setBucket] = useState("yaver");
  const [files, setFiles] = useState<any[]>([]);
  const [error, setError] = useState("");
  async function load() {
    const r = await call(`/objects/list?bucket=${encodeURIComponent(bucket)}`);
    setFiles(r.files || []);
    setError(r.error || "");
  }
  useEffect(() => { load(); }, []);
  return (
    <View style={{ gap: 8 }}>
      <Text style={{ color: c.textMuted, fontSize: 11 }}>S3-compatible storage (MinIO local default). SigV4 for AWS/R2/B2.</Text>
      <View style={{ flexDirection: "row", gap: 8 }}>
        <TextInput value={bucket} onChangeText={setBucket} placeholder="bucket"
          placeholderTextColor={c.textMuted}
          style={[inputStyle(c), { flex: 1, fontFamily: "Menlo", fontSize: 12 }]} />
        <Pressable onPress={load} style={[actionBtn(c), { backgroundColor: c.accent, paddingHorizontal: 16 }]}>
          <Text style={{ color: "#fff", fontWeight: "700" }}>List</Text>
        </Pressable>
      </View>
      {error && <Text style={{ color: "#ef4444", fontSize: 11 }}>{error}</Text>}
      {files.map((f) => (
        <View key={f.key} style={[card(c), { flexDirection: "row", alignItems: "center" }]}>
          <Text style={{ color: c.textPrimary, fontSize: 12, flex: 1 }} numberOfLines={1}>{f.key}</Text>
          <Text style={{ color: c.textMuted, fontSize: 11 }}>{fmtBytes(f.size)}</Text>
        </View>
      ))}
    </View>
  );
}

function WiFiTabScreen({ c }: { c: any }) {
  const { token } = useAuth();
  const { devices, activeDevice, primaryDeviceId } = useDevice();
  const onlineDevices = useMemo(() => devices.filter((d) => d.online !== false && !d.needsAuth), [devices]);
  const [deviceId, setDeviceId] = useState("");
  const [wifiTab, setWiFiTab] = useState<WiFiTab>("hotspot");
  const [caps, setCaps] = useState<WiFiCapabilities | null>(null);
  const [status, setStatus] = useState<WiFiStatus | null>(null);
  const [meshCaps, setMeshCaps] = useState<any>(null);
  const [meshStatus, setMeshStatus] = useState<any>(null);
  const [clients, setClients] = useState<WiFiClient[]>([]);
  const [banned, setBanned] = useState<WiFiBan[]>([]);
  const [busy, setBusy] = useState(false);
  const [ssid, setSsid] = useState("YaverHotspot");
  const [password, setPassword] = useState("yaver1234");
  const [iface, setIface] = useState("");
  const [apIface, setApIface] = useState("");
  const [upstreamIf, setUpstreamIf] = useState("");
  const [upstreamSsid, setUpstreamSsid] = useState("");
  const [upstreamPass, setUpstreamPass] = useState("");
  const [clientMac, setClientMac] = useState("");
  const [banHours, setBanHours] = useState("0");
  const [message, setMessage] = useState("");

  const selectedDevice = useMemo(() => {
    const preferred = deviceId || activeDevice?.id || primaryDeviceId || onlineDevices[0]?.id || devices[0]?.id || "";
    return devices.find((d) => d.id === preferred) || onlineDevices[0] || devices[0] || null;
  }, [activeDevice?.id, deviceId, devices, onlineDevices, primaryDeviceId]);

  useEffect(() => {
    if (!deviceId && selectedDevice?.id) setDeviceId(selectedDevice.id);
  }, [deviceId, selectedDevice?.id]);

  function target(): Device {
    if (!selectedDevice) throw new Error("Pick an online Yaver machine first");
    return selectedDevice;
  }

  async function refresh() {
    try {
      const d = target();
      const [c1, s1, list, bans, saved] = await Promise.all([
        wifiCapabilities(d, token),
        wifiStatus(d, token),
        wifiClients(d, token).catch(() => []),
        wifiBannedClients(d, token).catch(() => []),
        wifiGetAPSTAConfig(d, token).catch(() => null),
      ]);
      setCaps(c1);
      setStatus(s1);
      setClients(list);
      setBanned(bans);
      setMeshCaps(null);
      setMeshStatus(null);
      if (!iface && c1?.interface) setIface(c1.interface);
      if (saved) {
        if (!ssid && saved.ssid) setSsid(saved.ssid);
        if (!upstreamSsid && saved.upstreamSsid) setUpstreamSsid(saved.upstreamSsid);
        if (!upstreamIf && saved.upstreamIf) setUpstreamIf(saved.upstreamIf);
        if (!apIface && saved.apInterface) setApIface(saved.apInterface);
      }
      setMessage("");
    } catch (err: any) {
      setMessage(err?.message || "WiFi refresh failed");
    }
  }
  useEffect(() => { refresh(); const i = setInterval(refresh, 5000); return () => clearInterval(i); }, [selectedDevice?.id, token]);

  async function start(mode: "ap" | "apsta") {
    setBusy(true);
    setMessage("");
    try {
      const d = target();
      const body: any = {
        ssid,
        password,
        mode,
        interface: iface || caps?.interface || "",
        apInterface: apIface || undefined,
        upstreamIf: upstreamIf || undefined,
        channel: 6,
        frequency: "2.4GHz",
        enableDhcp: true,
        enableNat: true,
        countryCode: "US",
      };
      if (mode === "apsta") {
        body.upstreamSsid = upstreamSsid;
        body.upstreamPass = upstreamPass;
      }
      if (mode === "apsta") await wifiSetAPSTAConfig(d, token, body);
      await wifiStart(d, token, body);
      setMessage(`${mode.toUpperCase()} start requested`);
      await refresh();
    } catch (err: any) {
      setMessage(err?.message || "WiFi start failed");
    } finally {
      setBusy(false);
    }
  }
  async function stop() {
    setBusy(true);
    try {
      await wifiStop(target(), token);
      setMessage("stopped");
      await refresh();
    } catch (err: any) {
      setMessage(err?.message || "WiFi stop failed");
    } finally {
      setBusy(false);
    }
  }

  async function clientAction(action: "kick" | "ban" | "unban", mac = clientMac) {
    if (!mac.trim()) {
      setMessage("MAC address required");
      return;
    }
    setBusy(true);
    try {
      const d = target();
      if (action === "kick") await wifiKickClient(d, token, mac.trim());
      if (action === "ban") await wifiBanClient(d, token, mac.trim(), Number.parseInt(banHours, 10) || 0);
      if (action === "unban") await wifiUnbanClient(d, token, mac.trim());
      setClientMac("");
      setMessage(action === "kick" ? "client kicked" : action === "ban" ? "client banned" : "client unbanned");
      await refresh();
    } catch (err: any) {
      setMessage(err?.message || `client ${action} failed`);
    } finally {
      setBusy(false);
    }
  }

  const modeText = caps?.supportedModes?.join(", ") || "unknown";
  return (
    <View style={{ gap: 10 }}>
      <View>
        <Text style={{ color: c.textMuted, fontSize: 11, marginBottom: 6 }}>Machine</Text>
        <View style={{ flexDirection: "row", gap: 8, flexWrap: "wrap" }}>
          {(onlineDevices.length ? onlineDevices : devices).map((d) => {
            const active = d.id === selectedDevice?.id;
            return (
              <Pressable key={d.id} onPress={() => setDeviceId(d.id)} style={[actionBtn(c), { backgroundColor: active ? c.accent + "25" : c.bgCard, borderColor: active ? c.accent : c.border, borderWidth: 1 }]}>
                <Text style={{ color: active ? c.accent : c.textPrimary, fontSize: 12, fontWeight: "700" }}>{d.alias ? `@${d.alias}` : d.name}</Text>
                <Text style={{ color: d.online ? "#10b981" : c.textMuted, fontSize: 10 }}>{d.online ? "online" : "offline"} · {d.os || "agent"}</Text>
              </Pressable>
            );
          })}
          {devices.length === 0 && <Text style={{ color: c.textMuted, fontSize: 12 }}>No Yaver machines found.</Text>}
        </View>
      </View>

      <View style={{ flexDirection: "row", gap: 6, flexWrap: "wrap" }}>
        {(["hotspot", "apsta", "clients", "diagnostics"] as WiFiTab[]).map((t) => (
          <Pressable key={t} onPress={() => setWiFiTab(t)} style={[actionBtn(c), { backgroundColor: wifiTab === t ? c.accent + "30" : c.bgCard, borderColor: wifiTab === t ? c.accent : c.border, borderWidth: 1 }]}>
            <Text style={{ color: wifiTab === t ? c.accent : c.textMuted, fontSize: 11, fontWeight: "700", textTransform: "uppercase" }}>{t}</Text>
          </Pressable>
        ))}
      </View>

      <View style={[card(c)]}>
        <Text style={{ color: c.textPrimary, fontWeight: "700" }}>{status?.running ? "WiFi running" : "WiFi stopped"}</Text>
        <Text style={{ color: c.textMuted, fontSize: 11 }}>mode {status?.mode || "none"} · interface {status?.interface || caps?.interface || "unknown"} · modes {modeText}</Text>
        {status?.ssid ? <Text style={{ color: c.textMuted, fontSize: 11 }}>ssid {status.ssid} · clients {status.connectedClients || 0} · upstream {status.upstreamStatus || "n/a"}</Text> : null}
        <Text style={{ color: c.textMuted, fontSize: 11 }}>hardware {caps?.hardwareSupport || status?.hardwareSupport || "unknown"} · driver {caps?.driver || "unknown"}</Text>
        {message ? <Text style={{ color: message.includes("failed") || message.includes("requires") || message.includes("error") ? "#ef4444" : c.textMuted, fontSize: 11, marginTop: 6 }}>{message}</Text> : null}
      </View>

      {(wifiTab === "hotspot" || wifiTab === "apsta") && (
        <View style={{ gap: 8 }}>
          <TextInput value={ssid} onChangeText={setSsid} placeholder="SSID" placeholderTextColor={c.textMuted} style={inputStyle(c)} />
          <TextInput value={password} onChangeText={setPassword} placeholder="Password" placeholderTextColor={c.textMuted} secureTextEntry style={inputStyle(c)} />
          <TextInput value={iface} onChangeText={setIface} placeholder={caps?.interface || "WiFi interface"} placeholderTextColor={c.textMuted} style={inputStyle(c)} />
          {wifiTab === "apsta" && (
            <>
              <TextInput value={apIface} onChangeText={setApIface} placeholder="AP interface (optional, e.g. wlan0ap)" placeholderTextColor={c.textMuted} style={inputStyle(c)} />
              <TextInput value={upstreamIf} onChangeText={setUpstreamIf} placeholder="Upstream interface (optional)" placeholderTextColor={c.textMuted} style={inputStyle(c)} />
              <TextInput value={upstreamSsid} onChangeText={setUpstreamSsid} placeholder="Upstream SSID" placeholderTextColor={c.textMuted} style={inputStyle(c)} />
              <TextInput value={upstreamPass} onChangeText={setUpstreamPass} placeholder="Upstream password" placeholderTextColor={c.textMuted} secureTextEntry style={inputStyle(c)} />
            </>
          )}
          <View style={{ flexDirection: "row", gap: 8 }}>
            <Pressable disabled={busy} onPress={() => start(wifiTab === "apsta" ? "apsta" : "ap")} style={[actionBtn(c), { backgroundColor: c.accent, flex: 1, opacity: busy ? 0.6 : 1 }]}>
              <Text style={{ color: "#fff", fontWeight: "700" }}>Start</Text>
            </Pressable>
            <Pressable disabled={busy} onPress={stop} style={[actionBtn(c), { backgroundColor: "#ef444420", flex: 1, opacity: busy ? 0.6 : 1 }]}>
              <Text style={{ color: "#ef4444", fontWeight: "700" }}>Stop</Text>
            </Pressable>
          </View>
        </View>
      )}

      {wifiTab === "clients" && (
        <View style={{ gap: 8 }}>
          <View style={[card(c)]}>
            <Text style={{ color: c.textPrimary, fontWeight: "700" }}>{clients.length || status?.connectedClients || 0} clients</Text>
            <Text style={{ color: c.textMuted, fontSize: 11 }}>hostapd-backed Linux hotspots expose connected station controls.</Text>
          </View>
          <View style={{ flexDirection: "row", gap: 8 }}>
            <TextInput value={clientMac} onChangeText={setClientMac} placeholder="client MAC" placeholderTextColor={c.textMuted} autoCapitalize="none" style={[inputStyle(c), { flex: 1 }]} />
            <TextInput value={banHours} onChangeText={setBanHours} placeholder="hours" placeholderTextColor={c.textMuted} keyboardType="number-pad" style={[inputStyle(c), { width: 74 }]} />
          </View>
          <View style={{ flexDirection: "row", gap: 8 }}>
            <Pressable disabled={busy} onPress={() => clientAction("kick")} style={[actionBtn(c), { backgroundColor: c.bgCard, borderColor: c.border, borderWidth: 1, flex: 1 }]}>
              <Text style={{ color: c.accent, fontWeight: "700" }}>Kick</Text>
            </Pressable>
            <Pressable disabled={busy} onPress={() => clientAction("ban")} style={[actionBtn(c), { backgroundColor: "#ef444420", flex: 1 }]}>
              <Text style={{ color: "#ef4444", fontWeight: "700" }}>Ban</Text>
            </Pressable>
          </View>
          {clients.map((client) => {
            const mac = String(client.mac || "");
            return (
              <View key={mac || JSON.stringify(client)} style={[card(c), { gap: 4 }]}>
                <Text style={{ color: c.textPrimary, fontFamily: "Menlo", fontSize: 12 }}>{mac || "unknown client"}</Text>
                <Text style={{ color: c.textMuted, fontSize: 10 }} numberOfLines={2}>{Object.entries(client).filter(([k]) => k !== "mac").map(([k, v]) => `${k}=${String(v)}`).join(" · ") || "connected"}</Text>
                {mac ? (
                  <View style={{ flexDirection: "row", gap: 8 }}>
                    <Pressable disabled={busy} onPress={() => clientAction("kick", mac)} style={[actionBtn(c), { paddingVertical: 6, backgroundColor: c.bgCard, borderColor: c.border, borderWidth: 1 }]}>
                      <Text style={{ color: c.accent, fontSize: 11, fontWeight: "700" }}>Kick</Text>
                    </Pressable>
                    <Pressable disabled={busy} onPress={() => clientAction("ban", mac)} style={[actionBtn(c), { paddingVertical: 6, backgroundColor: "#ef444420" }]}>
                      <Text style={{ color: "#ef4444", fontSize: 11, fontWeight: "700" }}>Ban</Text>
                    </Pressable>
                  </View>
                ) : null}
              </View>
            );
          })}
          {banned.length > 0 && (
            <View style={[card(c), { gap: 6 }]}>
              <Text style={{ color: c.textPrimary, fontWeight: "700" }}>Banned</Text>
              {banned.map((ban) => (
                <View key={ban.mac} style={{ flexDirection: "row", alignItems: "center", gap: 8 }}>
                  <Text style={{ color: c.textMuted, fontFamily: "Menlo", fontSize: 11, flex: 1 }}>{ban.mac} · {ban.expiry}</Text>
                  <Pressable disabled={busy} onPress={() => clientAction("unban", ban.mac)} style={[actionBtn(c), { paddingVertical: 6, backgroundColor: c.bgCard, borderColor: c.border, borderWidth: 1 }]}>
                    <Text style={{ color: c.accent, fontSize: 11, fontWeight: "700" }}>Unban</Text>
                  </Pressable>
                </View>
              ))}
            </View>
          )}
        </View>
      )}

      {wifiTab === "diagnostics" && (
        <View style={{ gap: 8 }}>
          <View style={[card(c)]}>
            <Text style={{ color: c.textPrimary, fontWeight: "700" }}>Mesh</Text>
            <Text style={{ color: c.textMuted, fontSize: 11 }}>wpa_supplicant {meshCaps?.hasWpaSupplicant ? "yes" : "no"} · iw {meshCaps?.hasIw ? "yes" : "no"} · 802.11s {meshCaps?.supportsMeshPoint ? "yes" : "no"}</Text>
            <Text style={{ color: c.textMuted, fontSize: 11 }}>backend {meshStatus?.backend || meshCaps?.recommendedBackend || "none"} · peers {(meshStatus?.peers || []).length}</Text>
          </View>
          <Pressable onPress={refresh} style={[actionBtn(c), { backgroundColor: c.bgCard, borderColor: c.border, borderWidth: 1 }]}>
            <Text style={{ color: c.accent, fontWeight: "700" }}>Refresh</Text>
          </Pressable>
        </View>
      )}
    </View>
  );
}

function inputStyle(c: any) { return { backgroundColor: c.bgCard, borderColor: c.border, borderWidth: 1, borderRadius: 8, padding: 10, color: c.textPrimary } as const; }

function Card({ c, label, value, sub }: { c: any; label: string; value: string; sub: string }) {
  return (
    <View style={[card(c)]}>
      <Text style={{ color: c.textMuted, fontSize: 10, textTransform: "uppercase", fontWeight: "700" }}>{label}</Text>
      <Text style={{ color: c.textPrimary, fontSize: 22, fontWeight: "700", marginTop: 4 }}>{value}</Text>
      <Text style={{ color: c.textMuted, fontSize: 11 }}>{sub}</Text>
    </View>
  );
}

function Pill({ c, text, tone }: { c: any; text: string; tone: "ok" | "muted" | "accent" }) {
  const bg = tone === "ok" ? "#10b98120" : tone === "accent" ? c.accent + "20" : c.border;
  const color = tone === "ok" ? "#10b981" : tone === "accent" ? c.accent : c.textMuted;
  return (
    <View style={{ paddingHorizontal: 8, paddingVertical: 2, borderRadius: 4, backgroundColor: bg }}>
      <Text style={{ color, fontSize: 9, textTransform: "uppercase", fontWeight: "700" }}>{text}</Text>
    </View>
  );
}

// Provider-agnostic on purpose: the phone never names the underlying
// IaaS. Local hardware vs a cloud box is the only distinction shown.
function providerIcon(p: string): string {
  switch (p) {
    case "local-mac": return "🍎";
    case "yaver-cloud": return "⚡";
    default: return "🖥️";
  }
}

function fmtBytes(n: number): string {
  if (!n) return "0 B";
  const u = ["B", "KB", "MB", "GB", "TB"]; let i = 0;
  while (n >= 1024 && i < u.length - 1) { n /= 1024; i++; }
  return n.toFixed(1) + " " + u[i];
}
function fmtBps(n: number): string { return fmtBytes(n) + "/s"; }
function card(c: any) { return { backgroundColor: c.bgCard, borderColor: c.border, borderWidth: 1, borderRadius: 8, padding: 12 } as const; }
function actionBtn(c: any) { return { paddingVertical: 10, paddingHorizontal: 12, borderRadius: 8, alignItems: "center", justifyContent: "center" } as const; }

const styles = StyleSheet.create({
  container: { flex: 1 },
  header: { flexDirection: "row", alignItems: "center", justifyContent: "space-between", paddingHorizontal: 16, paddingBottom: 12, borderBottomWidth: 1 },
  tabbar: { borderBottomWidth: 1 },
});
