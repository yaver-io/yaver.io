import React, { useEffect, useState } from "react";
import { ActivityIndicator, Alert, Pressable, ScrollView, StyleSheet, Text, View } from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { useRouter } from "expo-router";
import { useColors } from "../../src/context/ThemeContext";
import { quicClient, type InfraSummary } from "../../src/lib/quic";

function fmtBytes(n?: number) {
  if (!n) return "0 B";
  const units = ["B", "KB", "MB", "GB", "TB"];
  let value = n;
  let i = 0;
  while (value >= 1024 && i < units.length - 1) {
    value /= 1024;
    i++;
  }
  return `${value.toFixed(value >= 10 || i === 0 ? 0 : 1)} ${units[i]}`;
}

function fmtUptime(seconds?: number) {
  if (!seconds) return "0m";
  const days = Math.floor(seconds / 86400);
  const hours = Math.floor((seconds % 86400) / 3600);
  const mins = Math.floor((seconds % 3600) / 60);
  if (days > 0) return `${days}d ${hours}h`;
  if (hours > 0) return `${hours}h ${mins}m`;
  return `${mins}m`;
}

export default function InfraScreen() {
  const c = useColors();
  const router = useRouter();
  const insets = useSafeAreaInsets();
  const [summary, setSummary] = useState<InfraSummary | null>(null);
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState<string | null>(null);

  async function refresh() {
    try {
      setSummary(await quicClient.infraSummary());
    } catch (e: any) {
      Alert.alert("Infra unavailable", e?.message || "Failed to load infra summary");
    } finally {
      setLoading(false);
    }
  }

  useEffect(() => {
    refresh();
    const iv = setInterval(refresh, 15000);
    return () => clearInterval(iv);
  }, []);

  async function serviceAction(name: string, action: "start" | "stop" | "restart") {
    setBusy(`${name}:${action}`);
    try {
      await quicClient.infraServiceAction("dev", name, action);
      await refresh();
    } finally {
      setBusy(null);
    }
  }

  async function powerAction(action: "agent_shutdown" | "host_reboot") {
    Alert.alert(
      action === "host_reboot" ? "Reboot host?" : "Stop Yaver agent?",
      action === "host_reboot" ? "This will reboot the remote machine." : "This will stop the remote Yaver agent.",
      [
        { text: "Cancel", style: "cancel" },
        {
          text: action === "host_reboot" ? "Reboot" : "Stop",
          style: "destructive",
          onPress: async () => {
            setBusy(action);
            try {
              await quicClient.infraPower(action);
              if (action !== "agent_shutdown") await refresh();
            } catch (e: any) {
              Alert.alert("Action failed", e?.message || "Unknown error");
            } finally {
              setBusy(null);
            }
          },
        },
      ],
    );
  }

  async function enableContainers(mode: "guests" | "host") {
    setBusy(`sandbox:${mode}`);
    try {
      const res = await quicClient.sandboxQuickstart(mode, true);
      if (!res.ok) {
        Alert.alert("Container setup failed", res.error || "Could not enable containerization");
        return;
      }
      if (res.message) {
        Alert.alert("Containerization", res.message);
      }
      await refresh();
    } finally {
      setBusy(null);
    }
  }

  return (
    <View style={[styles.container, { backgroundColor: c.bg }]}>
      <View style={[styles.header, { borderBottomColor: c.border, paddingTop: insets.top + 12 }]}>
        <Pressable onPress={() => router.navigate("/(tabs)/more" as any)} style={{ paddingVertical: 8 }}>
          <Text style={{ color: c.accent, fontSize: 15, fontWeight: "600" }}>{"\u2039"} Back</Text>
        </Pressable>
        <Text style={{ fontSize: 17, fontWeight: "700", color: c.textPrimary }}>Infra</Text>
        <View style={{ width: 50 }} />
      </View>

      {loading && !summary ? (
        <View style={{ flex: 1, alignItems: "center", justifyContent: "center" }}>
          <ActivityIndicator color={c.accent} />
        </View>
      ) : !summary ? (
        <View style={{ flex: 1, alignItems: "center", justifyContent: "center", padding: 24 }}>
          <Text style={{ color: c.textMuted, textAlign: "center" }}>No active infra summary yet.</Text>
        </View>
      ) : (
        <ScrollView contentContainerStyle={{ padding: 16, paddingBottom: 32, gap: 12 }}>
          <View style={[card(c), { gap: 10 }]}>
            <View style={{ flexDirection: "row", alignItems: "center", gap: 8 }}>
              <View style={{ width: 8, height: 8, borderRadius: 4, backgroundColor: summary.machine.isOnline ? "#22c55e" : "#ef4444" }} />
              <Text style={{ color: c.textPrimary, fontSize: 20, fontWeight: "700" }}>{summary.machine.name}</Text>
            </View>
            <Text style={{ color: c.textMuted, fontSize: 12 }}>{summary.machine.platform}{summary.machine.arch ? ` · ${summary.machine.arch}` : ""}</Text>
            <View style={{ flexDirection: "row", gap: 8 }}>
              <Pressable onPress={() => router.navigate("/(tabs)/terminal" as any)} style={[actionBtn(c), { backgroundColor: c.accent, flex: 1 }]}>
                <Text style={{ color: "#fff", fontWeight: "700" }}>Terminal</Text>
              </Pressable>
              <Pressable onPress={() => powerAction("agent_shutdown")} disabled={!!busy} style={[actionBtn(c), { backgroundColor: "#f59e0b22", flex: 1, opacity: busy ? 0.6 : 1 }]}>
                <Text style={{ color: "#f59e0b", fontWeight: "700" }}>Stop agent</Text>
              </Pressable>
              <Pressable onPress={() => powerAction("host_reboot")} disabled={!!busy || !summary.capabilities.hostReboot} style={[actionBtn(c), { backgroundColor: "#ef444422", flex: 1, opacity: busy || !summary.capabilities.hostReboot ? 0.6 : 1 }]}>
                <Text style={{ color: "#ef4444", fontWeight: "700" }}>Reboot</Text>
              </Pressable>
            </View>
          </View>

          <View style={styles.metricGrid}>
            <Metric c={c} label="CPU" value={`${(summary.metrics?.cpuPct || 0).toFixed(1)}%`} sub={`${summary.metrics?.cores || 0} cores`} />
            <Metric c={c} label="RAM" value={`${(summary.metrics?.ramPct || 0).toFixed(0)}%`} sub={`${fmtBytes(summary.metrics?.ramUsed)} / ${fmtBytes(summary.metrics?.ramTotal)}`} />
            <Metric c={c} label="Disk" value={`${(summary.metrics?.diskPct || 0).toFixed(0)}%`} sub={`${fmtBytes(summary.metrics?.diskUsed)} / ${fmtBytes(summary.metrics?.diskTotal)}`} />
            <Metric c={c} label="Uptime" value={fmtUptime(summary.metrics?.uptime)} sub={summary.metrics?.hostname || summary.machine.deviceId} />
          </View>

          <Section c={c} title="Services" subtitle="Managed dev services">
            {(summary.devServices || []).length === 0 ? (
              <Text style={{ color: c.textMuted, fontSize: 12 }}>No dev services configured.</Text>
            ) : (
              (summary.devServices || []).map((svc) => (
                <View key={svc.name} style={[card(c), { gap: 8, marginTop: 8 }]}>
                  <View style={{ flexDirection: "row", alignItems: "center", gap: 8 }}>
                    <View style={{ width: 8, height: 8, borderRadius: 4, backgroundColor: svc.running ? "#22c55e" : c.textMuted }} />
                    <Text style={{ color: c.textPrimary, fontSize: 14, fontWeight: "700", flex: 1 }}>{svc.name}</Text>
                    <Text style={{ color: c.textMuted, fontSize: 11 }}>{svc.health}</Text>
                  </View>
                  <Text style={{ color: c.textMuted, fontSize: 11 }}>{svc.image || "binary service"} {svc.port ? `· port ${svc.port}` : ""} {svc.memory ? `· ${svc.memory}` : ""}</Text>
                  <View style={{ flexDirection: "row", gap: 8 }}>
                    <Pressable onPress={() => serviceAction(svc.name, svc.running ? "restart" : "start")} disabled={!!busy} style={[actionBtn(c), { backgroundColor: c.accent + "22", flex: 1, opacity: busy ? 0.6 : 1 }]}>
                      <Text style={{ color: c.accent, fontWeight: "700" }}>{svc.running ? "Restart" : "Start"}</Text>
                    </Pressable>
                    <Pressable onPress={() => serviceAction(svc.name, "stop")} disabled={!!busy || !svc.running} style={[actionBtn(c), { backgroundColor: c.bg, borderWidth: 1, borderColor: c.border, flex: 1, opacity: busy || !svc.running ? 0.6 : 1 }]}>
                      <Text style={{ color: c.textPrimary, fontWeight: "700" }}>Stop</Text>
                    </Pressable>
                  </View>
                </View>
              ))
            )}
          </Section>

          <Section c={c} title="Relay" subtitle="Configured relay endpoints">
            {(summary.relays || []).length === 0 ? (
              <Text style={{ color: c.textMuted, fontSize: 12 }}>No relay endpoints configured.</Text>
            ) : (
              (summary.relays || []).map((relay) => (
                <View key={`${relay.source}:${relay.id}`} style={[card(c), { gap: 4, marginTop: 8 }]}>
                  <Text style={{ color: c.textPrimary, fontSize: 13, fontWeight: "700" }}>{relay.label || relay.id}</Text>
                  <Text style={{ color: c.textMuted, fontSize: 11 }}>{relay.httpUrl || relay.quicAddr}</Text>
                  <Text style={{ color: c.textMuted, fontSize: 10 }}>{relay.source}{relay.region ? ` · ${relay.region}` : ""}</Text>
                </View>
              ))
            )}
          </Section>

          <Section c={c} title="Sharing" subtitle="Guest access posture">
            <View style={styles.metricGrid}>
              <Metric c={c} label="Accepted" value={`${summary.sharing.acceptedGuests}`} sub="active guests" />
              <Metric c={c} label="Pending" value={`${summary.sharing.pendingGuests}`} sub="pending invites" />
            </View>
            <Pressable onPress={() => router.navigate("/(tabs)/guests" as any)} style={[actionBtn(c), { backgroundColor: c.bgCard, borderColor: c.border, borderWidth: 1, marginTop: 8 }]}>
              <Text style={{ color: c.textPrimary, fontWeight: "700" }}>Open guest controls</Text>
            </Pressable>
          </Section>

          <Section c={c} title="Containerization" subtitle="Whether remote Yaver tasks run directly on the host or inside Docker">
            <View style={styles.metricGrid}>
              <Metric
                c={c}
                label="Mode"
                value={
                  summary.sandbox.enabledMode === "host"
                    ? "All tasks"
                    : summary.sandbox.enabledMode === "guests"
                      ? "Guests only"
                      : "Direct host"
                }
                sub={
                  summary.sandbox.enabledMode === "host"
                    ? "all agent tasks isolated"
                    : summary.sandbox.enabledMode === "guests"
                      ? "shared infra isolated"
                      : "tasks run on host"
                }
              />
              <Metric
                c={c}
                label="Image"
                value={summary.sandbox.imageReady ? "Ready" : "Not built"}
                sub={summary.sandbox.imageName || "yaver-sandbox"}
              />
            </View>
            <View style={[card(c), { gap: 6, marginTop: 8 }]}>
              <Text style={{ color: c.textPrimary, fontSize: 13, fontWeight: "700" }}>
                Docker {summary.sandbox.docker ? "available" : "not found"}
              </Text>
              <Text style={{ color: c.textMuted, fontSize: 11 }}>
                {summary.sandbox.enabledMode === "off"
                  ? "Remote dev tasks are currently running directly on the host."
                  : `Yaver is configured to containerize ${summary.sandbox.enabledMode === "host" ? "all tasks" : "guest-triggered tasks"} on this machine.`}
              </Text>
              {!!summary.sandbox.recommendedReason && (
                <Text style={{ color: c.textMuted, fontSize: 11 }}>
                  Recommended: {summary.sandbox.recommendedReason}
                </Text>
              )}
            </View>
            <View style={{ flexDirection: "row", gap: 8, marginTop: 8 }}>
              <Pressable
                onPress={() => enableContainers("guests")}
                disabled={!!busy || !summary.sandbox.docker}
                style={[actionBtn(c), { backgroundColor: c.accent + "22", flex: 1, opacity: busy || !summary.sandbox.docker ? 0.6 : 1 }]}
              >
                <Text style={{ color: c.accent, fontWeight: "700" }}>Enable guest isolation</Text>
              </Pressable>
              <Pressable
                onPress={() => enableContainers("host")}
                disabled={!!busy || !summary.sandbox.docker}
                style={[actionBtn(c), { backgroundColor: "#8b5cf622", flex: 1, opacity: busy || !summary.sandbox.docker ? 0.6 : 1 }]}
              >
                <Text style={{ color: "#8b5cf6", fontWeight: "700" }}>Containerize all tasks</Text>
              </Pressable>
            </View>
            {!summary.sandbox.imageReady && summary.sandbox.docker && (
              <Pressable
                onPress={async () => {
                  setBusy("sandbox:build");
                  try {
                    await quicClient.buildSandboxImage();
                    Alert.alert("Sandbox build started", "Yaver is building the container image in the background.");
                    await refresh();
                  } finally {
                    setBusy(null);
                  }
                }}
                disabled={!!busy}
                style={[actionBtn(c), { backgroundColor: c.bgCard, borderColor: c.border, borderWidth: 1, marginTop: 8, opacity: busy ? 0.6 : 1 }]}
              >
                <Text style={{ color: c.textPrimary, fontWeight: "700" }}>Build sandbox image now</Text>
              </Pressable>
            )}
            <Pressable onPress={() => router.navigate("/(tabs)/settings" as any)} style={[actionBtn(c), { backgroundColor: c.bgCard, borderColor: c.border, borderWidth: 1, marginTop: 8 }]}>
              <Text style={{ color: c.textPrimary, fontWeight: "700" }}>Open advanced sandbox settings</Text>
            </Pressable>
          </Section>

          <Section c={c} title="Network" subtitle="Interfaces visible to the agent">
            {(summary.network || []).slice(0, 8).map((iface) => (
              <View key={iface.name} style={[card(c), { gap: 4, marginTop: 8 }]}>
                <Text style={{ color: c.textPrimary, fontSize: 13, fontWeight: "700" }}>{iface.name}</Text>
                <Text style={{ color: c.textMuted, fontSize: 10 }}>{iface.flags}</Text>
                <Text style={{ color: c.textMuted, fontSize: 11 }}>{(iface.addresses || []).join(" · ") || "no addresses"}</Text>
              </View>
            ))}
          </Section>
        </ScrollView>
      )}
    </View>
  );
}

function Section({ c, title, subtitle, children }: { c: any; title: string; subtitle: string; children: React.ReactNode }) {
  return (
    <View style={[card(c), { gap: 6 }]}>
      <Text style={{ color: c.textPrimary, fontSize: 16, fontWeight: "700" }}>{title}</Text>
      <Text style={{ color: c.textMuted, fontSize: 11 }}>{subtitle}</Text>
      {children}
    </View>
  );
}

function Metric({ c, label, value, sub }: { c: any; label: string; value: string; sub: string }) {
  return (
    <View style={[card(c), { flex: 1, minWidth: "47%" }]}>
      <Text style={{ color: c.textMuted, fontSize: 10, textTransform: "uppercase", fontWeight: "700" }}>{label}</Text>
      <Text style={{ color: c.textPrimary, fontSize: 20, fontWeight: "700", marginTop: 6 }}>{value}</Text>
      <Text style={{ color: c.textMuted, fontSize: 11, marginTop: 4 }}>{sub}</Text>
    </View>
  );
}

function card(c: any) {
  return { backgroundColor: c.bgCard, borderColor: c.border, borderWidth: 1, borderRadius: 12, padding: 12 } as const;
}

function actionBtn(c: any) {
  return { borderRadius: 10, paddingVertical: 10, paddingHorizontal: 12, alignItems: "center", justifyContent: "center" } as const;
}

const styles = StyleSheet.create({
  container: { flex: 1 },
  header: { flexDirection: "row", alignItems: "center", justifyContent: "space-between", paddingHorizontal: 16, paddingBottom: 12, borderBottomWidth: 1 },
  metricGrid: { flexDirection: "row", gap: 8, flexWrap: "wrap" },
});
