import React, { useEffect, useState } from "react";
import { ActivityIndicator, Alert, Pressable, ScrollView, StyleSheet, Text, TextInput, View } from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { useLocalSearchParams, useRouter } from "expo-router";
import { useColors } from "../../src/context/ThemeContext";
import { quicClient } from "../../src/lib/quic";

// Per-project detail screen. Single view that shows everything scoped to one
// project — env switcher, backend status, recent deploys, services, domains,
// with quick actions to deploy, snapshot, open data.
//
// Entered via /(tabs)/project?dir=/abs/path. When no dir is passed, prompts
// the user to type one (defaults to agent cwd).

export default function ProjectDetailScreen() {
  const c = useColors();
  const router = useRouter();
  const insets = useSafeAreaInsets();
  const params = useLocalSearchParams<{ dir?: string }>();
  const [dir, setDir] = useState<string>(typeof params.dir === "string" ? params.dir : "");
  const [status, setStatus] = useState<any>(null);
  const [env, setEnv] = useState<{ active: string; envs: string[] } | null>(null);
  const [deploys, setDeploys] = useState<any[]>([]);
  const [domains, setDomains] = useState<any[]>([]);

  const q = dir ? `?directory=${encodeURIComponent(dir)}` : "";

  async function loadAll() {
    try {
      const [st, e, d, dom] = await Promise.all([
        call(`/backend/status${q}`),
        call(`/project/env/list${q}`),
        call(`/deploy/list${q}`),
        call("/domains/list"),
      ]);
      setStatus(st);
      setEnv(e);
      setDeploys(d.deploys || []);
      setDomains(dom.domains || []);
    } catch {}
  }

  useEffect(() => { loadAll(); }, [dir]);

  async function switchEnv(name: string) {
    await call(`/project/env/switch${q}`, { method: "POST", body: JSON.stringify({ name }) });
    loadAll();
  }

  async function deploy() {
    const p = await call(`/deploy/preview${q}`);
    const msg = [
      `Branch: ${p.branch || "?"}`,
      p.dirty ? `⚠ ${p.dirtyFiles?.length} uncommitted` : null,
      `Env: ${p.activeEnv}`,
      p.ciConfigured ? `CI: ${p.ciSteps} steps` : "CI: none",
      p.migrator ? `Migrations: ${p.migrator}` : null,
      p.healthcheck ? `Healthcheck: ${p.healthcheck}` : "No healthcheck",
      ...(p.warnings || []).map((w: string) => `⚠ ${w}`),
    ].filter(Boolean).join("\n");
    Alert.alert("Pre-deploy check", msg, [
      { text: "Cancel", style: "cancel" },
      { text: "Deploy", style: "destructive", onPress: async () => {
        const r = await call(`/deploy/run${q}`, { method: "POST" });
        Alert.alert(r.status || "done", r.error || "");
        loadAll();
      }},
    ]);
  }

  async function snapshot() {
    const r = await call(`/backups/create${q}`, { method: "POST" });
    Alert.alert(r.error ? "Failed" : "Snapshot created", r.error || r.path || "");
  }

  const slug = dir.split("/").pop() || "project";

  return (
    <View style={[styles.container, { backgroundColor: c.bg }]}>
      <View style={[styles.header, { borderBottomColor: c.border, paddingTop: insets.top + 12 }]}>
        <Pressable onPress={() => router.back()} style={{ paddingVertical: 8 }}>
          <Text style={{ color: c.accent, fontSize: 15, fontWeight: "600" }}>{"\u2039"} Back</Text>
        </Pressable>
        <Text style={{ fontSize: 17, fontWeight: "700", color: c.textPrimary }} numberOfLines={1}>{slug}</Text>
        <View style={{ width: 50 }} />
      </View>

      <ScrollView contentContainerStyle={{ padding: 16, paddingBottom: 40, gap: 12 }}>
        <TextInput value={dir} onChangeText={setDir} placeholder="project directory" placeholderTextColor={c.textMuted}
          autoCapitalize="none" style={[inputStyle(c), { fontFamily: "Menlo", fontSize: 12 }]} />

        {status && (
          <View style={[card(c), { flexDirection: "row", alignItems: "center", gap: 8 }]}>
            <View style={{ width: 8, height: 8, borderRadius: 4, backgroundColor: status.running ? "#10b981" : "#ef4444" }} />
            <Text style={{ color: c.accent, fontSize: 10, fontWeight: "700", textTransform: "uppercase" }}>{status.kind || "?"}</Text>
            <Text style={{ color: c.textPrimary, fontSize: 12, flex: 1 }} numberOfLines={1}>{status.url || "—"}</Text>
          </View>
        )}

        {env && (
          <View style={[card(c), { gap: 6 }]}>
            <Text style={{ color: c.textMuted, fontSize: 10, fontWeight: "700", textTransform: "uppercase" }}>Environment</Text>
            <View style={{ flexDirection: "row", gap: 6, flexWrap: "wrap" }}>
              {env.envs.map((n) => {
                const a = env.active === n;
                return (
                  <Pressable key={n} onPress={() => switchEnv(n)}
                    style={{ paddingHorizontal: 12, paddingVertical: 6, borderRadius: 8, backgroundColor: a ? c.accent + "30" : c.bg, borderWidth: 1, borderColor: a ? c.accent : c.border }}>
                    <Text style={{ color: a ? c.accent : c.textMuted, fontSize: 12, fontWeight: "700" }}>{n}{a ? " ✓" : ""}</Text>
                  </Pressable>
                );
              })}
            </View>
          </View>
        )}

        <View style={{ flexDirection: "row", gap: 8 }}>
          <Pressable onPress={deploy} style={[actionBtn(c), { backgroundColor: c.accent, flex: 1 }]}>
            <Text style={{ color: "#fff", fontWeight: "700" }}>🚀 Deploy</Text>
          </Pressable>
          <Pressable onPress={snapshot} style={[actionBtn(c), { backgroundColor: c.surface, borderColor: c.border, borderWidth: 1, flex: 1 }]}>
            <Text style={{ color: c.textPrimary, fontWeight: "700" }}>📸 Snapshot</Text>
          </Pressable>
          <Pressable onPress={() => router.navigate({ pathname: "/(tabs)/data", params: { dir } } as any)} style={[actionBtn(c), { backgroundColor: c.surface, borderColor: c.border, borderWidth: 1, flex: 1 }]}>
            <Text style={{ color: c.textPrimary, fontWeight: "700" }}>🗄️ Data</Text>
          </Pressable>
        </View>

        <Section c={c} title="Recent deploys">
          {deploys.length === 0 && <Text style={{ color: c.textMuted, fontSize: 12 }}>No deploys.</Text>}
          {deploys.slice(0, 5).map((d) => (
            <View key={d.id} style={[card(c), { flexDirection: "row", alignItems: "center", gap: 6 }]}>
              <Text style={{ color: d.status === "success" ? "#10b981" : "#ef4444", fontSize: 10, fontWeight: "700" }}>{d.status?.toUpperCase()}</Text>
              <Text style={{ color: c.textMuted, fontFamily: "Menlo", fontSize: 10 }}>{(d.commit || "").slice(0, 8)}</Text>
              <Text style={{ color: c.textPrimary, fontSize: 11, flex: 1 }} numberOfLines={1}>{d.message || ""}</Text>
            </View>
          ))}
        </Section>

        <Section c={c} title="Domains">
          {domains.length === 0 && <Text style={{ color: c.textMuted, fontSize: 12 }}>None attached.</Text>}
          {domains.map((d: any) => (
            <View key={d.id} style={[card(c), { flexDirection: "row", gap: 8, alignItems: "center" }]}>
              <Text style={{ color: c.textPrimary, fontFamily: "Menlo", fontSize: 12 }}>{d.domain}</Text>
              <Text style={{ color: c.textMuted, fontSize: 11, flex: 1 }}>→ {d.upstream}</Text>
            </View>
          ))}
        </Section>
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

function Section({ c, title, children }: { c: any; title: string; children: React.ReactNode }) {
  return (
    <View style={{ gap: 6 }}>
      <Text style={{ color: c.textMuted, fontSize: 10, fontWeight: "700", textTransform: "uppercase", marginTop: 6 }}>{title}</Text>
      {children}
    </View>
  );
}

function card(c: any) { return { backgroundColor: c.surface, borderColor: c.border, borderWidth: 1, borderRadius: 8, padding: 10 } as const; }
function actionBtn(c: any) { return { paddingVertical: 10, borderRadius: 8, alignItems: "center", justifyContent: "center" } as const; }
function inputStyle(c: any) { return { backgroundColor: c.surface, borderColor: c.border, borderWidth: 1, borderRadius: 8, padding: 10, color: c.textPrimary } as const; }

const styles = StyleSheet.create({
  container: { flex: 1 },
  header: { flexDirection: "row", alignItems: "center", justifyContent: "space-between", paddingHorizontal: 16, paddingBottom: 12, borderBottomWidth: 1 },
});
