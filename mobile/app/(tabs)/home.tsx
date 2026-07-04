import React, { useEffect, useState } from "react";
import { ActivityIndicator, Pressable, ScrollView, StyleSheet, Text, View } from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { useRouter } from "expo-router";
import { AppScreenHeader } from "../../src/components/AppScreenHeader";
import { useColors } from "../../src/context/ThemeContext";
import { useTabletContentStyle } from "../../src/hooks/useTabletContentStyle";
import { quicClient } from "../../src/lib/quic";
import { AgentVoiceButton } from "../../src/components/AgentVoiceButton";
import { SessionStrip } from "../../src/components/SessionStrip";
import { SpatialPreview } from "../../src/components/SpatialPreview";

// Native mobile Home screen — AWS-console-style summary grid + recent activity.
// Uses the agent's /overview/summary endpoint which aggregates machines,
// projects, services, uptime, alerts, and cost in one call.

type Summary = {
  machines: { total: number; online: number; offline: number };
  projects: { total: number; deployed: number; local: number };
  services: { running: number; stopped: number };
  alerts: { active: number; summary: string };
  cost: { monthlyUsd: number; breakdown: { provider: string; monthly: number }[] };
  uptime: { up: number; down: number; pct: number };
  recentActivity: { timestamp: string; icon: string; title: string; detail?: string }[];
};

export default function HomeScreen() {
  const c = useColors();
  const tabletContent = useTabletContentStyle("regular");
  const router = useRouter();
  const insets = useSafeAreaInsets();
  const [s, setS] = useState<Summary | null>(null);
  const [err, setErr] = useState("");

  useEffect(() => {
    refresh();
    const i = setInterval(refresh, 15000);
    return () => clearInterval(i);
  }, []);

  async function refresh() {
    try {
      const res = await fetch(`${quicClient.baseUrl}/overview/summary`, { headers: quicClient.getAuthHeaders() });
      setS(await res.json());
      setErr("");
    } catch (e: any) { setErr(e.message); }
  }

  const greeting = new Date().getHours() < 12 ? "Good morning" : new Date().getHours() < 18 ? "Good afternoon" : "Good evening";

  return (
    <View style={[styles.container, { backgroundColor: c.bg }]}>
      <AppScreenHeader title="Home" onBack={() => router.navigate("/(tabs)/more" as any)} style={{ paddingTop: insets.top + 12 }} />

      <ScrollView contentContainerStyle={[{ padding: 16, paddingBottom: 40 }, tabletContent]}>
        <Text style={{ color: c.textPrimary, fontSize: 22, fontWeight: "700" }}>{greeting}</Text>
        <Text style={{ color: c.textMuted, fontSize: 13, marginTop: 2 }}>Your machines at a glance</Text>

        {/* Active sessions strip — tmux-mirror of the desk workflow.
            Tap a chip to jump to that task. */}
        <View style={{ marginTop: 12, marginHorizontal: -16 }}>
          <SessionStrip
            onPress={(task) => router.navigate({ pathname: "/(tabs)/tasks", params: { focus: task.id } } as any)}
          />
        </View>

        {/* Hands-free agent loop — tap the orb, speak a task, hear the
            result. Routes through the agent's /voice/stream WS:
            Deepgram Flux STT → CreateTaskWithOptions(source="voice-input")
            → Cartesia Sonic-3 TTS readback. */}
        <View style={[card(c), { marginTop: 16, alignItems: "center", paddingVertical: 18, gap: 10 }]}>
          <Text style={{ color: c.textMuted, fontSize: 10, textTransform: "uppercase", fontWeight: "700" }}>
            Hands-Free
          </Text>
          <AgentVoiceButton />
        </View>

        {/* What your glasses / VR / Ray-Ban Web App would see — the
            same /spatial route, rendered inline so you can iterate
            without owning the hardware. */}
        <View style={{ marginTop: 16 }}>
          <SpatialPreview inlineHeight={240} />
        </View>

        {err ? (
          <View style={[card(c), { marginTop: 16, backgroundColor: "#ef444410", borderColor: "#ef4444" }]}>
            <Text style={{ color: "#ef4444", fontSize: 12 }}>{err}</Text>
          </View>
        ) : !s ? (
          <ActivityIndicator color={c.accent} style={{ marginTop: 24 }} />
        ) : (
          <>
            <View style={{ marginTop: 16, gap: 8 }}>
              <View style={{ flexDirection: "row", gap: 8 }}>
                <Card c={c} label="Machines" value={`${s.machines.total}`} sub={`${s.machines.online} online · ${s.machines.offline} off`} tone={s.machines.online > 0 ? "ok" : "warn"} />
                <Card c={c} label="Projects" value={`${s.projects.total}`} sub={`${s.projects.deployed} deployed`} tone="info" />
              </View>
              <View style={{ flexDirection: "row", gap: 8 }}>
                <Card c={c} label="Services" value={`${s.services.running}`} sub={`${s.services.stopped} stopped`} tone="ok" />
                <Card c={c} label="Alerts" value={`${s.alerts.active}`} sub={s.alerts.summary} tone={s.alerts.active > 0 ? "warn" : "ok"} />
              </View>
              <View style={{ flexDirection: "row", gap: 8 }}>
                <Card c={c} label="Cost" value={`$${s.cost.monthlyUsd.toFixed(2)}`} sub="per month" tone="info" />
                <Card c={c} label="Uptime" value={`${s.uptime.pct.toFixed(1)}%`} sub={`${s.uptime.up} monitors`} tone={s.uptime.down === 0 ? "ok" : "warn"} />
              </View>
            </View>

            <Text style={{ color: c.textMuted, fontSize: 10, textTransform: "uppercase", fontWeight: "700", marginTop: 20 }}>Quick Actions</Text>
            <View style={{ flexDirection: "row", gap: 8, flexWrap: "wrap", marginTop: 6 }}>
              <QuickBtn c={c} label="🚀 Deploy" onPress={() => router.navigate("/(tabs)/ops" as any)} />
              <QuickBtn c={c} label="🗄️ Data" onPress={() => router.navigate("/(tabs)/data" as any)} />
              <QuickBtn c={c} label="💻 Console" onPress={() => router.navigate("/(tabs)/console" as any)} />
              <QuickBtn c={c} label="⚙️ CI" onPress={() => router.navigate("/ci" as any)} />
              <QuickBtn c={c} label="📦 Projects" onPress={() => router.navigate("/(tabs)/more" as any)} />
            </View>

            <Text style={{ color: c.textMuted, fontSize: 10, textTransform: "uppercase", fontWeight: "700", marginTop: 20 }}>Recent Activity</Text>
            <View style={{ gap: 6, marginTop: 6 }}>
              {s.recentActivity.length === 0 && <Text style={{ color: c.textMuted, fontSize: 12 }}>No activity yet.</Text>}
              {s.recentActivity.map((a, i) => (
                <View key={i} style={[card(c), { flexDirection: "row", alignItems: "center", gap: 10 }]}>
                  <Text style={{ fontSize: 18 }}>{a.icon}</Text>
                  <View style={{ flex: 1 }}>
                    <Text style={{ color: c.textPrimary, fontSize: 13 }} numberOfLines={1}>{a.title}</Text>
                    <Text style={{ color: c.textMuted, fontSize: 10 }}>{new Date(a.timestamp).toLocaleTimeString()}</Text>
                  </View>
                </View>
              ))}
            </View>

            {s.cost.breakdown.length > 0 && (
              <>
                <Text style={{ color: c.textMuted, fontSize: 10, textTransform: "uppercase", fontWeight: "700", marginTop: 20 }}>Cost Breakdown</Text>
                <View style={{ gap: 4, marginTop: 6 }}>
                  {s.cost.breakdown.map((cb, i) => (
                    <View key={i} style={[card(c), { flexDirection: "row", alignItems: "center" }]}>
                      <Text style={{ color: c.textPrimary, fontSize: 13, flex: 1 }}>{cb.provider}</Text>
                      <Text style={{ color: c.textPrimary, fontFamily: "Menlo", fontSize: 12 }}>${cb.monthly.toFixed(2)}/mo</Text>
                    </View>
                  ))}
                </View>
              </>
            )}
          </>
        )}
      </ScrollView>
    </View>
  );
}

function Card({ c, label, value, sub, tone }: { c: any; label: string; value: string; sub: string; tone: "ok" | "warn" | "info" }) {
  const border = tone === "ok" ? "#10b98144" : tone === "warn" ? "#f59e0b66" : c.border;
  return (
    <View style={{ flex: 1, backgroundColor: c.bgCard, borderColor: border, borderWidth: 1, borderRadius: 10, padding: 12 }}>
      <Text style={{ color: c.textMuted, fontSize: 9, fontWeight: "700", textTransform: "uppercase" }}>{label}</Text>
      <Text style={{ color: c.textPrimary, fontSize: 22, fontWeight: "700", marginTop: 4 }}>{value}</Text>
      <Text style={{ color: c.textMuted, fontSize: 10 }} numberOfLines={1}>{sub}</Text>
    </View>
  );
}

function QuickBtn({ c, label, onPress }: { c: any; label: string; onPress: () => void }) {
  return (
    <Pressable onPress={onPress} style={{ paddingHorizontal: 14, paddingVertical: 10, borderRadius: 8, backgroundColor: c.accent + "20", borderColor: c.accent + "40", borderWidth: 1 }}>
      <Text style={{ color: c.accent, fontSize: 13, fontWeight: "600" }}>{label}</Text>
    </Pressable>
  );
}

function card(c: any) { return { backgroundColor: c.bgCard, borderColor: c.border, borderWidth: 1, borderRadius: 10, padding: 12 } as const; }

const styles = StyleSheet.create({
  container: { flex: 1 },
  header: { flexDirection: "row", alignItems: "center", justifyContent: "space-between", paddingHorizontal: 16, paddingBottom: 12, borderBottomWidth: 1 },
});
