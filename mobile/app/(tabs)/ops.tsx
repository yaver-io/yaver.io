import React, { useEffect, useState } from "react";
import { ActivityIndicator, Alert, Pressable, ScrollView, StyleSheet, Text, TextInput, View } from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { useRouter } from "expo-router";
import { useColors } from "../../src/context/ThemeContext";
import { quicClient } from "../../src/lib/quic";

type Tab = "deploy" | "backups" | "uptime" | "domains" | "rotate";

export default function OpsScreen() {
  const c = useColors();
  const router = useRouter();
  const insets = useSafeAreaInsets();
  const [tab, setTab] = useState<Tab>("deploy");
  const [directory, setDirectory] = useState("");

  return (
    <View style={[styles.container, { backgroundColor: c.bg }]}>
      <View style={[styles.header, { borderBottomColor: c.border, paddingTop: insets.top + 12 }]}>
        <Pressable onPress={() => router.navigate("/(tabs)/more" as any)} style={{ paddingVertical: 8 }}>
          <Text style={{ color: c.accent, fontSize: 15, fontWeight: "600" }}>{"\u2039"} Back</Text>
        </Pressable>
        <Text style={{ fontSize: 17, fontWeight: "700", color: c.textPrimary }}>Ops</Text>
        <View style={{ width: 50 }} />
      </View>
      <View style={[styles.tabbar, { backgroundColor: c.surface, borderBottomColor: c.border }]}>
        <ScrollView horizontal showsHorizontalScrollIndicator={false} contentContainerStyle={{ paddingHorizontal: 12 }}>
          {(["deploy", "backups", "uptime", "domains", "rotate"] as Tab[]).map((t) => (
            <Pressable key={t} onPress={() => setTab(t)} style={{ paddingHorizontal: 10, paddingVertical: 10 }}>
              <Text style={{
                fontSize: 13, fontWeight: "600", textTransform: "uppercase",
                color: tab === t ? c.accent : c.textMuted,
              }}>{t}</Text>
            </Pressable>
          ))}
        </ScrollView>
      </View>
      <ScrollView contentContainerStyle={{ padding: 16, paddingBottom: 32 }}>
        <View style={{ marginBottom: 12 }}>
          <Text style={{ fontSize: 11, color: c.textMuted, marginBottom: 4, textTransform: "uppercase" }}>Project dir</Text>
          <TextInput value={directory} onChangeText={setDirectory} placeholder="blank = agent cwd"
            placeholderTextColor={c.textMuted}
            style={{ backgroundColor: c.surface, borderColor: c.border, borderWidth: 1, borderRadius: 8, padding: 10, color: c.textPrimary, fontFamily: "Menlo", fontSize: 12 }} />
        </View>
        {tab === "deploy" && <DeployTab c={c} dir={directory} />}
        {tab === "backups" && <BackupsTab c={c} dir={directory} />}
        {tab === "uptime" && <UptimeTab c={c} />}
        {tab === "domains" && <DomainsTab c={c} />}
        {tab === "rotate" && <RotateTab c={c} dir={directory} />}
      </ScrollView>
    </View>
  );
}

async function callAgent(path: string, init: RequestInit = {}): Promise<any> {
  const res = await fetch(`${quicClient.baseUrl}${path}`, {
    ...init,
    headers: { ...quicClient.getAuthHeaders(), "Content-Type": "application/json", ...(init.headers || {}) },
  });
  return res.json();
}

function DeployTab({ c, dir }: { c: any; dir: string }) {
  const [list, setList] = useState<any[]>([]);
  const [running, setRunning] = useState(false);
  const q = dir ? `?directory=${encodeURIComponent(dir)}` : "";
  async function refresh() { try { const r = await callAgent(`/deploy/list${q}`); setList(r.deploys || []); } catch {} }
  useEffect(() => { refresh(); }, [dir]);
  async function deploy() {
    setRunning(true);
    const r = await callAgent(`/deploy/run${q}`, { method: "POST" });
    setRunning(false);
    Alert.alert(r.status === "success" ? "✅ Deployed" : r.status, r.message || r.error || "");
    refresh();
  }
  async function rollback(id: string) {
    const r = await callAgent(`/deploy/rollback${q}`, { method: "POST", body: JSON.stringify({ id }) });
    Alert.alert(r.status || "done", r.error || "Rolled back");
    refresh();
  }
  return (
    <View style={{ gap: 10 }}>
      <Pressable onPress={deploy} style={[actionBtn(c), { backgroundColor: c.accent }]} disabled={running}>
        {running ? <ActivityIndicator color="#fff" /> : <Text style={{ color: "#fff", fontWeight: "700" }}>🚀 Deploy now</Text>}
      </Pressable>
      {list.map((d) => (
        <View key={d.id} style={[card(c)]}>
          <View style={{ flexDirection: "row", gap: 8, alignItems: "center" }}>
            <Text style={{ color: statusColor(d.status), fontSize: 10, fontWeight: "700", textTransform: "uppercase" }}>{d.status}</Text>
            <Text style={{ color: c.textMuted, fontFamily: "Menlo", fontSize: 11 }}>{(d.commit || "").slice(0, 8)}</Text>
            <Text style={{ color: c.textPrimary, fontSize: 13, flex: 1 }} numberOfLines={1}>{d.message || "(no message)"}</Text>
          </View>
          <Text style={{ color: c.textMuted, fontSize: 11, marginTop: 4 }}>{d.duration}</Text>
          {d.status === "success" && (
            <Pressable onPress={() => rollback(d.id)} style={{ marginTop: 6 }}>
              <Text style={{ color: "#f59e0b", fontSize: 12 }}>Rollback</Text>
            </Pressable>
          )}
        </View>
      ))}
    </View>
  );
}

function BackupsTab({ c, dir }: { c: any; dir: string }) {
  const [list, setList] = useState<any[]>([]);
  const q = dir ? `?directory=${encodeURIComponent(dir)}` : "";
  async function refresh() { try { const r = await callAgent(`/backups/list${q}`); setList(r.backups || []); } catch {} }
  useEffect(() => { refresh(); }, [dir]);
  async function snap() {
    const r = await callAgent(`/backups/create${q}`, { method: "POST" });
    Alert.alert(r.error ? "Failed" : "Snapshot created", r.error || r.path || "");
    refresh();
  }
  async function restore(id: string) {
    Alert.alert("Restore?", "Current data will be overwritten.", [
      { text: "Cancel", style: "cancel" },
      { text: "Restore", style: "destructive", onPress: async () => {
        const r = await callAgent(`/backups/restore${q}`, { method: "POST", body: JSON.stringify({ id }) });
        Alert.alert(r.error ? "Failed" : "Restored", r.error || r.message || "");
      }},
    ]);
  }
  return (
    <View style={{ gap: 10 }}>
      <Pressable onPress={snap} style={[actionBtn(c), { backgroundColor: c.accent }]}>
        <Text style={{ color: "#fff", fontWeight: "700" }}>📸 Snapshot now</Text>
      </Pressable>
      {list.map((b) => (
        <View key={b.id} style={[card(c)]}>
          <Text style={{ color: c.textPrimary, fontFamily: "Menlo", fontSize: 12 }}>{b.id}</Text>
          <Text style={{ color: c.textMuted, fontSize: 11 }}>{b.backend} · {fmtBytes(b.size)}</Text>
          <Pressable onPress={() => restore(b.id)} style={{ marginTop: 6 }}>
            <Text style={{ color: "#10b981", fontSize: 12 }}>Restore</Text>
          </Pressable>
        </View>
      ))}
    </View>
  );
}

function UptimeTab({ c }: { c: any }) {
  const [list, setList] = useState<any[]>([]);
  const [url, setUrl] = useState("");
  async function refresh() { try { const r = await callAgent("/uptime/list"); setList(r.monitors || []); } catch {} }
  useEffect(() => { refresh(); const i = setInterval(refresh, 10000); return () => clearInterval(i); }, []);
  async function add() {
    if (!url) return;
    await callAgent("/uptime/add", { method: "POST", body: JSON.stringify({ url, intervalSeconds: 60, alertOnDown: true }) });
    setUrl(""); refresh();
  }
  async function rem(id: string) {
    await callAgent("/uptime/remove", { method: "POST", body: JSON.stringify({ id }) });
    refresh();
  }
  return (
    <View style={{ gap: 10 }}>
      <TextInput value={url} onChangeText={setUrl} placeholder="https://myapp.com/health"
        placeholderTextColor={c.textMuted} autoCapitalize="none" keyboardType="url"
        style={{ backgroundColor: c.surface, borderColor: c.border, borderWidth: 1, borderRadius: 8, padding: 10, color: c.textPrimary, fontFamily: "Menlo", fontSize: 12 }} />
      <Pressable onPress={add} style={[actionBtn(c), { backgroundColor: c.accent }]}>
        <Text style={{ color: "#fff", fontWeight: "700" }}>+ Monitor</Text>
      </Pressable>
      {list.map((m) => (
        <View key={m.id} style={[card(c)]}>
          <View style={{ flexDirection: "row", gap: 8, alignItems: "center" }}>
            <View style={{ width: 8, height: 8, borderRadius: 4, backgroundColor: m.status === "up" ? "#10b981" : m.status === "down" ? "#ef4444" : c.textMuted }} />
            <Text style={{ color: c.textPrimary, flex: 1, fontSize: 13 }} numberOfLines={1}>{m.name || m.url}</Text>
            <Text style={{ color: c.textMuted, fontSize: 11 }}>{m.lastLatencyMs}ms</Text>
          </View>
          <Pressable onPress={() => rem(m.id)} style={{ marginTop: 6 }}>
            <Text style={{ color: "#ef4444", fontSize: 12 }}>Remove</Text>
          </Pressable>
        </View>
      ))}
    </View>
  );
}

function DomainsTab({ c }: { c: any }) {
  const [list, setList] = useState<any[]>([]);
  const [domain, setDomain] = useState(""); const [upstream, setUpstream] = useState("");
  async function refresh() { try { const r = await callAgent("/domains/list"); setList(r.domains || []); } catch {} }
  useEffect(() => { refresh(); }, []);
  async function add() {
    if (!domain || !upstream) return;
    const r = await callAgent("/domains/add", { method: "POST", body: JSON.stringify({ domain, upstream }) });
    if (r.error) Alert.alert("Failed", r.error); else { setDomain(""); setUpstream(""); refresh(); }
  }
  async function rem(d: string) {
    await callAgent("/domains/remove", { method: "POST", body: JSON.stringify({ domain: d }) });
    refresh();
  }
  return (
    <View style={{ gap: 10 }}>
      <TextInput value={domain} onChangeText={setDomain} placeholder="app.example.com" placeholderTextColor={c.textMuted}
        autoCapitalize="none" style={inputStyle(c)} />
      <TextInput value={upstream} onChangeText={setUpstream} placeholder="localhost:3000" placeholderTextColor={c.textMuted}
        autoCapitalize="none" style={inputStyle(c)} />
      <Pressable onPress={add} style={[actionBtn(c), { backgroundColor: c.accent }]}>
        <Text style={{ color: "#fff", fontWeight: "700" }}>+ Domain</Text>
      </Pressable>
      {list.map((r: any) => (
        <View key={r.id} style={[card(c)]}>
          <Text style={{ color: c.textPrimary, fontFamily: "Menlo", fontSize: 13 }}>{r.domain}</Text>
          <Text style={{ color: c.textMuted, fontSize: 11 }}>→ {r.upstream}</Text>
          <Pressable onPress={() => rem(r.domain)} style={{ marginTop: 6 }}>
            <Text style={{ color: "#ef4444", fontSize: 12 }}>Remove</Text>
          </Pressable>
        </View>
      ))}
    </View>
  );
}

function RotateTab({ c, dir }: { c: any; dir: string }) {
  const [key, setKey] = useState("");
  const [res, setRes] = useState<any>(null);
  const q = dir ? `?directory=${encodeURIComponent(dir)}` : "";
  async function rotate() {
    if (!key) return;
    const r = await callAgent(`/secrets/rotate${q}`, { method: "POST", body: JSON.stringify({ key }) });
    setRes(r);
  }
  return (
    <View style={{ gap: 10 }}>
      <TextInput value={key} onChangeText={setKey} placeholder="POSTGRES_PASSWORD" placeholderTextColor={c.textMuted}
        autoCapitalize="none" style={inputStyle(c)} />
      <Pressable onPress={rotate} style={[actionBtn(c), { backgroundColor: c.accent }]}>
        <Text style={{ color: "#fff", fontWeight: "700" }}>🔑 Rotate & restart</Text>
      </Pressable>
      {res && (
        <View style={[card(c)]}>
          <Text style={{ color: c.textPrimary, fontSize: 11 }}>Patched: {(res.filesPatched || []).join(", ")}</Text>
          <Text style={{ color: c.textPrimary, fontSize: 11 }}>Restarted: {(res.servicesRestarted || []).join(", ")}</Text>
        </View>
      )}
    </View>
  );
}

function fmtBytes(n: number): string {
  if (!n) return "0 B";
  const units = ["B", "KB", "MB", "GB"];
  let i = 0; while (n >= 1024 && i < units.length - 1) { n /= 1024; i++; }
  return `${n.toFixed(1)} ${units[i]}`;
}

function statusColor(s: string): string {
  if (s === "success") return "#10b981";
  if (s === "failed") return "#ef4444";
  if (s === "rolled-back") return "#f59e0b";
  return "#737373";
}

function card(c: any) {
  return { backgroundColor: c.surface, borderColor: c.border, borderWidth: 1, borderRadius: 8, padding: 12 } as const;
}
function actionBtn(c: any) {
  return { paddingVertical: 12, borderRadius: 8, alignItems: "center", justifyContent: "center" } as const;
}
function inputStyle(c: any) {
  return { backgroundColor: c.surface, borderColor: c.border, borderWidth: 1, borderRadius: 8, padding: 10, color: c.textPrimary, fontFamily: "Menlo", fontSize: 12 } as const;
}

const styles = StyleSheet.create({
  container: { flex: 1 },
  header: { flexDirection: "row", alignItems: "center", justifyContent: "space-between", paddingHorizontal: 16, paddingBottom: 12, borderBottomWidth: 1 },
  tabbar: { borderBottomWidth: 1 },
});
