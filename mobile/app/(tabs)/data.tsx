import React, { useEffect, useState } from "react";
import { ActivityIndicator, Alert, Pressable, ScrollView, StyleSheet, Text, TextInput, View } from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { useRouter } from "expo-router";
import { useColors } from "../../src/context/ThemeContext";
import { quicClient } from "../../src/lib/quic";

// Native mobile database browser. No WebViews — every tab calls the agent's
// /backend/* or /storage/* endpoints directly and renders with RN components.
// Works for Convex / Postgres / Supabase / SQLite / PocketBase / Appwrite via
// the unified BackendAdapter layer on the agent side.

type Tab = "tables" | "browse" | "query" | "schema" | "storage" | "jobs";

export default function DataScreen() {
  const c = useColors();
  const router = useRouter();
  const insets = useSafeAreaInsets();
  const [tab, setTab] = useState<Tab>("tables");
  const [directory, setDirectory] = useState("");
  const [status, setStatus] = useState<any>(null);
  const [selectedTable, setSelectedTable] = useState<string | null>(null);

  useEffect(() => { loadStatus(); }, [directory]);
  async function loadStatus() {
    try { setStatus(await call(`/backend/status${dirQ(directory)}`)); } catch {}
  }

  return (
    <View style={[styles.container, { backgroundColor: c.bg }]}>
      <View style={[styles.header, { borderBottomColor: c.border, paddingTop: insets.top + 12 }]}>
        <Pressable onPress={() => router.navigate("/(tabs)/more" as any)} style={{ paddingVertical: 8 }}>
          <Text style={{ color: c.accent, fontSize: 15, fontWeight: "600" }}>{"\u2039"} Back</Text>
        </Pressable>
        <Text style={{ fontSize: 17, fontWeight: "700", color: c.textPrimary }}>Data</Text>
        <View style={{ width: 50 }} />
      </View>

      <View style={[styles.tabbar, { backgroundColor: c.surface, borderBottomColor: c.border }]}>
        <ScrollView horizontal showsHorizontalScrollIndicator={false}>
          {(["tables", "browse", "query", "schema", "storage", "jobs"] as Tab[]).map((t) => (
            <Pressable key={t} onPress={() => setTab(t)} style={{ paddingHorizontal: 12, paddingVertical: 10 }}>
              <Text style={{ fontSize: 13, fontWeight: "600", textTransform: "uppercase", color: tab === t ? c.accent : c.textMuted }}>{t}</Text>
            </Pressable>
          ))}
        </ScrollView>
      </View>

      <ScrollView contentContainerStyle={{ padding: 16, paddingBottom: 32 }}>
        <View style={{ marginBottom: 12 }}>
          <TextInput value={directory} onChangeText={setDirectory} placeholder="project dir (blank = cwd)"
            placeholderTextColor={c.textMuted}
            style={[inputStyle(c), { fontFamily: "Menlo", fontSize: 12 }]} />
        </View>

        <StatusBanner c={c} status={status} />

        {tab === "tables" && <TablesTab c={c} dir={directory} onPick={(t) => { setSelectedTable(t); setTab("browse"); }} />}
        {tab === "browse" && <BrowseTab c={c} dir={directory} table={selectedTable} />}
        {tab === "query" && <QueryTab c={c} dir={directory} kind={status?.kind} />}
        {tab === "schema" && <SchemaTab c={c} dir={directory} />}
        {tab === "storage" && <StorageTab c={c} dir={directory} />}
        {tab === "jobs" && <JobsTab c={c} dir={directory} />}
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
function dirQ(dir: string) { return dir ? `?directory=${encodeURIComponent(dir)}` : ""; }

function StatusBanner({ c, status }: { c: any; status: any }) {
  if (!status) return <Text style={{ color: c.textMuted, fontSize: 11, marginBottom: 10 }}>Checking…</Text>;
  const running = !!status.running;
  return (
    <View style={[{ backgroundColor: c.surface, borderColor: c.border, borderWidth: 1, borderRadius: 8, padding: 10, marginBottom: 12 }]}>
      <View style={{ flexDirection: "row", alignItems: "center", gap: 8 }}>
        <View style={{ width: 8, height: 8, borderRadius: 4, backgroundColor: running ? "#10b981" : "#ef4444" }} />
        <Text style={{ color: c.accent, fontSize: 11, fontWeight: "700", textTransform: "uppercase" }}>{status.kind || "unknown"}</Text>
        <Text style={{ color: c.textPrimary, fontFamily: "Menlo", fontSize: 11, flex: 1 }} numberOfLines={1}>{status.url}</Text>
      </View>
      {status.error && <Text style={{ color: "#ef4444", fontSize: 10, marginTop: 4 }}>{status.error}</Text>}
      {status.hint && <Text style={{ color: "#f59e0b", fontSize: 10, marginTop: 4 }}>{status.hint}</Text>}
    </View>
  );
}

function TablesTab({ c, dir, onPick }: { c: any; dir: string; onPick: (table: string) => void }) {
  const [tables, setTables] = useState<any[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  useEffect(() => { load(); }, [dir]);
  async function load() {
    setLoading(true);
    const r = await call(`/backend/tables${dirQ(dir)}`);
    setTables(r.tables || []);
    setError(r.error || "");
    setLoading(false);
  }
  if (loading) return <ActivityIndicator color={c.accent} />;
  return (
    <View style={{ gap: 4 }}>
      {error && <Text style={{ color: "#ef4444", fontSize: 11 }}>{error}</Text>}
      {tables.length === 0 && !error && <Text style={{ color: c.textMuted, fontSize: 12 }}>No tables found.</Text>}
      {tables.map((t) => (
        <Pressable key={t.name} onPress={() => onPick(t.name)} style={[card(c), { flexDirection: "row", alignItems: "center" }]}>
          <Text style={{ color: c.textPrimary, fontFamily: "Menlo", fontSize: 13, flex: 1 }}>{t.name}</Text>
          {t.rowCount != null && <Text style={{ color: c.textMuted, fontSize: 11 }}>{t.rowCount}</Text>}
          <Text style={{ color: c.accent, fontSize: 14, marginLeft: 8 }}>{"›"}</Text>
        </Pressable>
      ))}
    </View>
  );
}

function BrowseTab({ c, dir, table }: { c: any; dir: string; table: string | null }) {
  const [rows, setRows] = useState<any[]>([]);
  const [cursor, setCursor] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);

  async function load(reset = false) {
    if (!table) return;
    setLoading(true);
    const params = new URLSearchParams({ table, limit: "50" });
    if (dir) params.set("directory", dir);
    if (!reset && cursor) params.set("cursor", cursor);
    const r = await call(`/backend/browse?${params}`);
    setRows(reset ? (r.rows || []) : [...rows, ...(r.rows || [])]);
    setCursor(r.nextCursor || null);
    setLoading(false);
  }

  useEffect(() => { if (table) { setRows([]); setCursor(null); load(true); } }, [table, dir]);

  if (!table) return <Text style={{ color: c.textMuted }}>Pick a table from the Tables tab.</Text>;

  return (
    <View style={{ gap: 8 }}>
      <Text style={{ color: c.accent, fontSize: 13, fontWeight: "700" }}>{table} ({rows.length} loaded)</Text>
      {rows.map((row, i) => (
        <View key={(row.id || row._id || i) + "-" + i} style={[card(c)]}>
          <Text style={{ color: c.textPrimary, fontFamily: "Menlo", fontSize: 10 }}>
            {JSON.stringify(row, null, 2)}
          </Text>
        </View>
      ))}
      {cursor && (
        <Pressable onPress={() => load()} disabled={loading} style={[actionBtn(c), { backgroundColor: c.surface, borderColor: c.border, borderWidth: 1 }]}>
          <Text style={{ color: c.textPrimary, fontSize: 13 }}>{loading ? "Loading…" : "Load more"}</Text>
        </Pressable>
      )}
    </View>
  );
}

function QueryTab({ c, dir, kind }: { c: any; dir: string; kind?: string }) {
  const [q, setQ] = useState("");
  const [args, setArgs] = useState("{}");
  const [result, setResult] = useState("");
  const [running, setRunning] = useState(false);

  const placeholder = kind === "convex" ? "module:function (e.g. messages:list)"
    : kind === "pocketbase" || kind === "appwrite" ? "REST path (e.g. collections/users/records)"
    : "SQL (SELECT * FROM users LIMIT 10)";

  async function run() {
    setRunning(true);
    try {
      const parsed = args.trim() ? JSON.parse(args) : {};
      const r = await call(`/backend/query${dirQ(dir)}`, {
        method: "POST",
        body: JSON.stringify({ query: q, args: parsed }),
      });
      setResult(JSON.stringify(r, null, 2));
    } catch (e: any) {
      setResult("Error: " + e.message);
    }
    setRunning(false);
  }

  return (
    <View style={{ gap: 10 }}>
      <TextInput value={q} onChangeText={setQ} placeholder={placeholder} placeholderTextColor={c.textMuted}
        multiline numberOfLines={4} autoCapitalize="none"
        style={[inputStyle(c), { height: 90, textAlignVertical: "top", fontFamily: "Menlo", fontSize: 12 }]} />
      {kind === "convex" && (
        <TextInput value={args} onChangeText={setArgs} placeholder="args (JSON)" placeholderTextColor={c.textMuted}
          multiline numberOfLines={3} autoCapitalize="none"
          style={[inputStyle(c), { height: 70, textAlignVertical: "top", fontFamily: "Menlo", fontSize: 12 }]} />
      )}
      <Pressable onPress={run} disabled={running || !q} style={[actionBtn(c), { backgroundColor: c.accent }]}>
        {running ? <ActivityIndicator color="#fff" /> : <Text style={{ color: "#fff", fontWeight: "700" }}>Run</Text>}
      </Pressable>
      {result !== "" && (
        <View style={[card(c)]}>
          <Text style={{ color: c.textPrimary, fontFamily: "Menlo", fontSize: 10 }}>{result}</Text>
        </View>
      )}
    </View>
  );
}

function SchemaTab({ c, dir }: { c: any; dir: string }) {
  const [data, setData] = useState<any>(null);
  useEffect(() => { (async () => setData(await call(`/backend/schema${dirQ(dir)}`)))(); }, [dir]);
  if (!data) return <ActivityIndicator color={c.accent} />;
  if (data.error) return <Text style={{ color: "#ef4444", fontSize: 11 }}>{data.error}</Text>;
  return (
    <View style={{ gap: 8 }}>
      <Text style={{ color: c.textMuted, fontSize: 11 }}>Source: {data.source}</Text>
      {(data.tables || []).map((t: any) => (
        <View key={t.name} style={[card(c)]}>
          <Text style={{ color: c.accent, fontWeight: "700", fontFamily: "Menlo", fontSize: 13, marginBottom: 4 }}>{t.name}</Text>
          {(t.columns || []).map((col: any, i: number) => (
            <View key={i} style={{ flexDirection: "row", gap: 8 }}>
              <Text style={{ color: c.textPrimary, fontFamily: "Menlo", fontSize: 11 }}>{col.name}</Text>
              <Text style={{ color: c.textMuted, fontFamily: "Menlo", fontSize: 11, flex: 1 }}>{col.type}</Text>
              {col.primaryKey && <Text style={{ color: "#f59e0b", fontSize: 10 }}>PK</Text>}
            </View>
          ))}
        </View>
      ))}
    </View>
  );
}

function StorageTab({ c, dir }: { c: any; dir: string }) {
  const [data, setData] = useState<any>(null);
  useEffect(() => { (async () => setData(await call(`/storage/list${dirQ(dir)}`)))(); }, [dir]);
  if (!data) return <ActivityIndicator color={c.accent} />;
  return (
    <View style={{ gap: 4 }}>
      <Text style={{ color: c.textMuted, fontSize: 11 }}>Source: {data.source}</Text>
      {data.error && <Text style={{ color: "#ef4444", fontSize: 11 }}>{data.error}</Text>}
      {(data.files || []).map((f: any, i: number) => (
        <View key={i} style={[card(c), { flexDirection: "row", alignItems: "center", gap: 8 }]}>
          <Text style={{ color: c.textPrimary, fontSize: 12, flex: 1 }} numberOfLines={1}>{f.name}</Text>
          <Text style={{ color: c.textMuted, fontSize: 11 }}>{fmtBytes(f.size)}</Text>
        </View>
      ))}
    </View>
  );
}

function JobsTab({ c, dir }: { c: any; dir: string }) {
  const [data, setData] = useState<any>(null);
  useEffect(() => { (async () => setData(await call(`/jobs/list${dirQ(dir)}`)))(); }, [dir]);
  if (!data) return <ActivityIndicator color={c.accent} />;
  return (
    <View style={{ gap: 4 }}>
      <Text style={{ color: c.textMuted, fontSize: 11 }}>Source: {data.source}</Text>
      {(!data.jobs || data.jobs.length === 0) && <Text style={{ color: c.textMuted }}>No scheduled jobs.</Text>}
      {(data.jobs || []).map((j: any, i: number) => (
        <View key={i} style={[card(c)]}>
          <View style={{ flexDirection: "row", gap: 6 }}>
            <Text style={{ color: c.accent, fontFamily: "Menlo", fontSize: 12, fontWeight: "700" }}>{j.name}</Text>
            <Text style={{ color: c.textMuted, fontSize: 11 }}>{j.kind}</Text>
            {j.schedule && <Text style={{ color: c.textPrimary, fontFamily: "Menlo", fontSize: 11 }}>{j.schedule}</Text>}
          </View>
          {j.target && <Text style={{ color: c.textMuted, fontFamily: "Menlo", fontSize: 10, marginTop: 4 }} numberOfLines={2}>{j.target}</Text>}
        </View>
      ))}
    </View>
  );
}

function fmtBytes(n: number): string {
  if (!n) return "0 B";
  const units = ["B", "KB", "MB", "GB"]; let i = 0;
  while (n >= 1024 && i < units.length - 1) { n /= 1024; i++; }
  return `${n.toFixed(1)} ${units[i]}`;
}
function card(c: any) { return { backgroundColor: c.surface, borderColor: c.border, borderWidth: 1, borderRadius: 8, padding: 12 } as const; }
function actionBtn(c: any) { return { paddingVertical: 12, borderRadius: 8, alignItems: "center", justifyContent: "center" } as const; }
function inputStyle(c: any) { return { backgroundColor: c.surface, borderColor: c.border, borderWidth: 1, borderRadius: 8, padding: 10, color: c.textPrimary } as const; }

const styles = StyleSheet.create({
  container: { flex: 1 },
  header: { flexDirection: "row", alignItems: "center", justifyContent: "space-between", paddingHorizontal: 16, paddingBottom: 12, borderBottomWidth: 1 },
  tabbar: { borderBottomWidth: 1 },
});
