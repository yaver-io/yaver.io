import React, { useEffect, useState } from "react";
import { ActivityIndicator, Alert, Pressable, ScrollView, StyleSheet, Text, TextInput, View } from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { useRouter } from "expo-router";
import { useColors } from "../../src/context/ThemeContext";
import { quicClient } from "../../src/lib/quic";

// Native mobile Console — all Docker/machine ops via RN components. No WebViews.

type Tab = "overview" | "machines" | "containers" | "catalog" | "mailpit" | "s3";

export default function ConsoleScreen() {
  const c = useColors();
  const router = useRouter();
  const insets = useSafeAreaInsets();
  const [tab, setTab] = useState<Tab>("overview");

  return (
    <View style={[styles.container, { backgroundColor: c.bg }]}>
      <View style={[styles.header, { borderBottomColor: c.border, paddingTop: insets.top + 12 }]}>
        <Pressable onPress={() => router.navigate("/(tabs)/more" as any)} style={{ paddingVertical: 8 }}>
          <Text style={{ color: c.accent, fontSize: 15, fontWeight: "600" }}>{"\u2039"} Back</Text>
        </Pressable>
        <Text style={{ fontSize: 17, fontWeight: "700", color: c.textPrimary }}>Console</Text>
        <View style={{ width: 50 }} />
      </View>
      <View style={[styles.tabbar, { backgroundColor: c.surface, borderBottomColor: c.border }]}>
        <ScrollView horizontal showsHorizontalScrollIndicator={false}>
          {(["overview", "machines", "containers", "catalog", "mailpit", "s3"] as Tab[]).map((t) => (
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
  useEffect(() => {
    loadMetrics();
    const i = setInterval(loadMetrics, 5000);
    return () => clearInterval(i);
  }, []);
  async function loadMetrics() {
    try { setM(await call("/console/metrics")); } catch {}
  }
  if (!m) return <ActivityIndicator color={c.accent} />;
  return (
    <View style={{ gap: 10 }}>
      <Card c={c} label="CPU" value={`${(m.cpuPct || 0).toFixed(1)}%`} sub={`${m.cores || 0} cores`} />
      <Card c={c} label="RAM" value={`${(m.ramPct || 0).toFixed(0)}%`} sub={`${fmtBytes(m.ramUsed)} / ${fmtBytes(m.ramTotal)}`} />
      <Card c={c} label="Disk" value={`${(m.diskPct || 0).toFixed(0)}%`} sub={`${fmtBytes(m.diskUsed)} / ${fmtBytes(m.diskTotal)}`} />
      <Card c={c} label="Network" value={`↓ ${fmtBps(m.netRxBps)}`} sub={`↑ ${fmtBps(m.netTxBps)}`} />
      <Text style={{ color: c.textMuted, fontSize: 11, textAlign: "center", marginTop: 8 }}>
        {m.hostname} · {m.os}
      </Text>
    </View>
  );
}

function MachinesTab({ c }: { c: any }) {
  const [list, setList] = useState<any[]>([]);
  const [multiOpen, setMultiOpen] = useState(false);
  useEffect(() => { refresh(); const i = setInterval(refresh, 10000); return () => clearInterval(i); }, []);
  async function refresh() { try { setList((await call("/console/machines")).machines || []); } catch {} }
  return (
    <View style={{ gap: 8 }}>
      <Text style={{ color: c.textMuted, fontSize: 11 }}>Hybrid view — own hardware + cloud VPSes as one list.</Text>
      <Pressable onPress={() => setMultiOpen(!multiOpen)} style={[actionBtn(c), { backgroundColor: c.accent }]}>
        <Text style={{ color: "#fff", fontWeight: "700" }}>{multiOpen ? "Close" : "🌍 Deploy Multi-Region"}</Text>
      </Pressable>
      {multiOpen && <MultiRegionForm c={c} onDone={() => { setMultiOpen(false); refresh(); }} />}
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
            <Pill c={c} text={m.provider || "unknown"} tone="accent" />
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
        <Pressable onPress={() => setShowAll(!showAll)} style={[actionBtn(c), { backgroundColor: c.surface, borderColor: c.border, borderWidth: 1, flex: 1 }]}>
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
                <Pressable onPress={() => act(ct.id, "restart")} style={[actionBtn(c), { backgroundColor: c.surface, borderColor: c.border, borderWidth: 1, flex: 1 }]}>
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

function MultiRegionForm({ c, onDone }: { c: any; onDone: () => void }) {
  const [name, setName] = useState("");
  const [regions, setRegions] = useState("nbg1,fsn1");
  const [domain, setDomain] = useState("");
  const [running, setRunning] = useState(false);
  const [result, setResult] = useState<any>(null);

  async function deploy() {
    if (!name) { Alert.alert("Name required"); return; }
    const regionList = regions.split(",").map((r) => r.trim()).filter(Boolean);
    if (regionList.length < 2) { Alert.alert("Need 2+ regions"); return; }
    Alert.alert(
      "Provision real VPSes?",
      `This will create ${regionList.length} billable Hetzner VPSes in ${regionList.join(", ")}.`,
      [
        { text: "Cancel", style: "cancel" },
        { text: "Provision", style: "destructive", onPress: async () => {
          setRunning(true);
          const r = await call("/multiregion/orchestrate", { method: "POST", body: JSON.stringify({ name, regions: regionList, domain, gitRepo: "" }) });
          setRunning(false);
          setResult(r);
        }},
      ],
    );
  }

  return (
    <View style={{ padding: 12, backgroundColor: c.surface, borderColor: c.border, borderWidth: 1, borderRadius: 8, gap: 8 }}>
      <TextInput value={name} onChangeText={setName} placeholder="deployment name" placeholderTextColor={c.textMuted} autoCapitalize="none" style={[inputStyle(c), { fontFamily: "Menlo", fontSize: 12 }]} />
      <TextInput value={regions} onChangeText={setRegions} placeholder="regions csv (nbg1,fsn1,hel1)" placeholderTextColor={c.textMuted} autoCapitalize="none" style={[inputStyle(c), { fontFamily: "Menlo", fontSize: 12 }]} />
      <TextInput value={domain} onChangeText={setDomain} placeholder="domain (optional)" placeholderTextColor={c.textMuted} autoCapitalize="none" style={[inputStyle(c), { fontFamily: "Menlo", fontSize: 12 }]} />
      <Pressable onPress={deploy} disabled={running} style={[actionBtn(c), { backgroundColor: "#ef4444" }]}>
        {running ? <ActivityIndicator color="#fff" /> : <Text style={{ color: "#fff", fontWeight: "700" }}>Deploy (billable)</Text>}
      </Pressable>
      {result?.error && <Text style={{ color: "#ef4444", fontSize: 11 }}>{result.error}</Text>}
      {result?.orchestrate?.servers?.map((os: any, i: number) => (
        <View key={i} style={[card(c)]}>
          <Text style={{ color: os.status === "ready" ? "#10b981" : "#ef4444", fontSize: 11, fontWeight: "700" }}>{os.status.toUpperCase()}</Text>
          <Text style={{ color: c.textPrimary, fontFamily: "Menlo", fontSize: 11 }}>{os.ip} · {os.region}</Text>
        </View>
      ))}
    </View>
  );
}

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

function inputStyle(c: any) { return { backgroundColor: c.surface, borderColor: c.border, borderWidth: 1, borderRadius: 8, padding: 10, color: c.textPrimary } as const; }

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

function providerIcon(p: string): string {
  switch (p) {
    case "hetzner": return "🖥️";
    case "aws": return "☁️";
    case "gcp": return "🌩️";
    case "local-mac": return "🍎";
    case "yaver-cloud": return "⚡";
    default: return "💻";
  }
}

function fmtBytes(n: number): string {
  if (!n) return "0 B";
  const u = ["B", "KB", "MB", "GB", "TB"]; let i = 0;
  while (n >= 1024 && i < u.length - 1) { n /= 1024; i++; }
  return n.toFixed(1) + " " + u[i];
}
function fmtBps(n: number): string { return fmtBytes(n) + "/s"; }
function card(c: any) { return { backgroundColor: c.surface, borderColor: c.border, borderWidth: 1, borderRadius: 8, padding: 12 } as const; }
function actionBtn(c: any) { return { paddingVertical: 10, paddingHorizontal: 12, borderRadius: 8, alignItems: "center", justifyContent: "center" } as const; }

const styles = StyleSheet.create({
  container: { flex: 1 },
  header: { flexDirection: "row", alignItems: "center", justifyContent: "space-between", paddingHorizontal: 16, paddingBottom: 12, borderBottomWidth: 1 },
  tabbar: { borderBottomWidth: 1 },
});
