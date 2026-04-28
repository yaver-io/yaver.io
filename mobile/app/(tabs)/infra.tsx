import React, { useEffect, useMemo, useRef, useState } from "react";
import { ActivityIndicator, Alert, Modal, Pressable, ScrollView, StyleSheet, Text, TextInput, View } from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { useRouter } from "expo-router";
import { useAuth } from "../../src/context/AuthContext";
import { useColors } from "../../src/context/ThemeContext";
import { useDevice } from "../../src/context/DeviceContext";
import { quicClient, type CapabilitySnapshot, type IncidentEvent, type InfraSummary } from "../../src/lib/quic";
import { listGuests, type GuestInfo } from "../../src/lib/guests";

// Install catalogue metadata — kept tiny, emoji + tagline per tool so
// the install grid reads as "what to add to this machine" instead of
// a raw CLI list. Anything the agent advertises but isn't in the map
// still renders with a neutral gear icon.
const TOOL_META: Record<string, { emoji: string; tagline: string }> = {
  "claude-code": { emoji: "🤖", tagline: "Anthropic's CLI agent — the frontier-quality runner." },
  codex: { emoji: "🧠", tagline: "OpenAI Codex CLI — token-efficient daily driver." },
  opencode: { emoji: "🪄", tagline: "Open-source coding agent — BYOK Anthropic / OpenAI / OpenRouter / GLM / Ollama, or any other provider." },
  docker: { emoji: "🐳", tagline: "Containerise tasks — required for guest isolation + sandbox mode." },
  node: { emoji: "🟢", tagline: "Node.js runtime — required for Expo, Vite, Next.js builds." },
  python: { emoji: "🐍", tagline: "Python 3 — required for ML tooling, some CLIs." },
  go: { emoji: "🐹", tagline: "Go toolchain — needed to rebuild the agent or relay from source." },
  rust: { emoji: "🦀", tagline: "Rust toolchain — some Yaver runners + Hermes compiler." },
  git: { emoji: "🔀", tagline: "Version control — required for every scaffold." },
};

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

type InstallEntry = { name: string; installed: boolean; description: string };

export default function InfraScreen() {
  const c = useColors();
  const router = useRouter();
  const insets = useSafeAreaInsets();
  const { token } = useAuth();
  const { devices, activeDevice } = useDevice();
  const [summary, setSummary] = useState<InfraSummary | null>(null);
  const [capabilitySnapshot, setCapabilitySnapshot] = useState<CapabilitySnapshot | null>(null);
  const [connectivityIncidents, setConnectivityIncidents] = useState<IncidentEvent[]>([]);
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState<string | null>(null);
  const [guests, setGuests] = useState<GuestInfo[]>([]);

  // --- tooling catalogue -------------------------------------------------
  // `target` is the deviceId we want to inspect / install onto. Defaults
  // to the active device; switching it forwards every /install call
  // through /peer/<id>/... so the phone can install onto a paired Mac
  // Mini or Hetzner box without first rebinding to it.
  const [target, setTarget] = useState<string | undefined>(undefined);
  const [catalogue, setCatalogue] = useState<InstallEntry[]>([]);
  const [installingTool, setInstallingTool] = useState<string | null>(null);
  const [installLog, setInstallLog] = useState<string[]>([]);
  const [installResult, setInstallResult] = useState<{ tool: string; status: string } | null>(null);
  const cancelStreamRef = useRef<(() => void) | null>(null);
  // Sudo prompt coming from an in-flight install. When non-null the
  // mobile sheet opens; the user types the password and we POST it
  // back to /install/sudo. Password lives only in component state.
  const [sudoPrompt, setSudoPrompt] = useState<{ tool: string; prompt: string; hint?: string } | null>(null);
  const [sudoPassword, setSudoPassword] = useState("");
  const [sudoSubmitting, setSudoSubmitting] = useState(false);

  async function loadCatalogue() {
    try {
      const entries = await quicClient.listInstallables(target);
      setCatalogue(entries);
    } catch {
      /* ignore — list is best-effort */
    }
  }

  useEffect(() => {
    void loadCatalogue();
    return () => {
      if (cancelStreamRef.current) cancelStreamRef.current();
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [target]);

  async function installTool(tool: string) {
    if (installingTool) return;
    setInstallingTool(tool);
    setInstallLog([]);
    setInstallResult(null);
    const res = await quicClient.installTool(tool, target);
    if (!res.ok) {
      Alert.alert("Install failed to start", res.error || "Unknown error");
      setInstallingTool(null);
      return;
    }
    // SSE stream subscription — always lives on the LOCAL agent's
    // /streams/ path even for remote installs, because the remote
    // agent tees its log lines back through the peer forwarder.
    cancelStreamRef.current?.();
    cancelStreamRef.current = quicClient.subscribeStream(
      res.stream,
      (line) => setInstallLog((prev) => [...prev.slice(-199), line]),
      (status, err) => {
        setInstallResult({ tool, status });
        setInstallingTool(null);
        setSudoPrompt(null);
        if (status !== "ok" && err) {
          Alert.alert(`Install ${tool} failed`, err);
        }
        void loadCatalogue();
      },
      (event) => {
        if (event?.type === "sudo_prompt" && event?.tool === tool) {
          setSudoPrompt({
            tool,
            prompt: event.prompt || "[sudo] password:",
            hint: event.hint,
          });
          setSudoPassword("");
        }
      },
    );
  }

  async function submitSudo() {
    if (!sudoPrompt) return;
    setSudoSubmitting(true);
    try {
      const res = await quicClient.respondInstallSudo(sudoPrompt.tool, sudoPassword, false, target);
      if (!res.ok) {
        Alert.alert("Password not accepted", res.error || "Failed to submit sudo password");
        return;
      }
      // The PTY now gets the password and keeps going. Installer
      // either continues or re-prompts (wrong password) — the stream
      // will fire another sudo_prompt event in the latter case.
      setSudoPrompt(null);
      setSudoPassword("");
    } finally {
      setSudoSubmitting(false);
    }
  }

  async function cancelSudo() {
    if (!sudoPrompt) return;
    await quicClient.respondInstallSudo(sudoPrompt.tool, "", true, target);
    setSudoPrompt(null);
    setSudoPassword("");
  }

  const machineOptions = useMemo(() => {
    // Only online peer agents (desktop-ish) are installable targets.
    // Filter out edge-mobile (phones) because installing the runner
    // CLIs on a phone is a non-starter.
    const desktops = devices.filter(
      (d) => d.online && d.deviceClass !== "edge-mobile",
    );
    return desktops;
  }, [devices]);

  async function refresh() {
    try {
      const [infra, snapshot, incidents] = await Promise.all([
        quicClient.infraSummary(),
        quicClient.capabilitySnapshot(),
        quicClient.incidents({ category: "connectivity", limit: 5 }),
      ]);
      setSummary(infra);
      setCapabilitySnapshot(snapshot);
      setConnectivityIncidents(incidents);
    } catch (e: any) {
      Alert.alert("Infra unavailable", e?.message || "Failed to load infra summary");
    } finally {
      setLoading(false);
    }
  }

  async function refreshGuests() {
    if (!token) return;
    try {
      const list = await listGuests(token);
      setGuests(list);
    } catch {
      /* soft-fail: counts from summary still render */
    }
  }

  useEffect(() => {
    refresh();
    void refreshGuests();
    const iv = setInterval(() => {
      refresh();
      void refreshGuests();
    }, 15000);
    return () => clearInterval(iv);
  }, [token]);

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
          {!capabilitySnapshot?.targets?.["web-preview"]?.enabled && capabilitySnapshot?.targets?.["web-preview"]?.reason ? (
            <View style={[card(c), { gap: 6, borderColor: "#7f1d1d", backgroundColor: "#2b1212" }]}>
              <Text style={{ color: "#fecaca", fontSize: 13, fontWeight: "700" }}>Remote preview blocked</Text>
              <Text style={{ color: "#fca5a5", fontSize: 12 }}>
                {capabilitySnapshot.targets["web-preview"].reason}
              </Text>
              {capabilitySnapshot.targets["web-preview"].suggestedAction ? (
                <Text style={{ color: c.textMuted, fontSize: 11 }}>
                  {capabilitySnapshot.targets["web-preview"].suggestedAction}
                </Text>
              ) : null}
            </View>
          ) : null}

          {connectivityIncidents.length > 0 ? (
            <View style={[card(c), { gap: 8, borderColor: "#7f1d1d", backgroundColor: "#2b1212" }]}>
              <Text style={{ color: "#fecaca", fontSize: 13, fontWeight: "700" }}>Connectivity blockers</Text>
              {connectivityIncidents.map((incident) => (
                <View key={incident.id} style={{ gap: 2 }}>
                  <Text style={{ color: "#fca5a5", fontSize: 12, fontWeight: "700" }}>
                    {incident.title || incident.code}
                  </Text>
                  <Text style={{ color: c.textPrimary, fontSize: 12 }}>
                    {incident.userMessage}
                  </Text>
                  {incident.suggestedAction ? (
                    <Text style={{ color: c.textMuted, fontSize: 11 }}>
                      {incident.suggestedAction}
                    </Text>
                  ) : null}
                </View>
              ))}
            </View>
          ) : null}

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

          {/* Sudo password sheet. Only the install stream can open
              it; the password flows through /install/sudo and never
              through any log stream. See install_registry.go for
              the server-side invariants. */}
          <Modal
            visible={!!sudoPrompt}
            transparent
            animationType="slide"
            onRequestClose={() => void cancelSudo()}
          >
            <View style={{ flex: 1, backgroundColor: "rgba(0,0,0,0.55)", justifyContent: "flex-end" }}>
              <View
                style={{
                  backgroundColor: c.bgCard,
                  borderTopLeftRadius: 22,
                  borderTopRightRadius: 22,
                  padding: 22,
                  paddingBottom: insets.bottom + 22,
                  gap: 12,
                }}
              >
                <Text style={{ color: c.textPrimary, fontSize: 18, fontWeight: "800" }}>
                  Sudo password required
                </Text>
                <Text style={{ color: c.textMuted, fontSize: 13, lineHeight: 19 }}>
                  The install for {sudoPrompt?.tool} is waiting at:
                </Text>
                <Text
                  style={{
                    color: c.textPrimary,
                    fontSize: 13,
                    fontFamily: "Menlo",
                    backgroundColor: "#000",
                    padding: 10,
                    borderRadius: 10,
                  }}
                >
                  {sudoPrompt?.prompt}
                </Text>
                <TextInput
                  value={sudoPassword}
                  onChangeText={setSudoPassword}
                  placeholder="password"
                  placeholderTextColor={c.textMuted}
                  secureTextEntry
                  autoFocus
                  autoCapitalize="none"
                  autoCorrect={false}
                  style={{
                    borderWidth: 1,
                    borderColor: c.border,
                    backgroundColor: c.bg,
                    color: c.textPrimary,
                    borderRadius: 12,
                    paddingHorizontal: 14,
                    paddingVertical: 12,
                    fontSize: 16,
                  }}
                />
                <Text style={{ color: c.textMuted, fontSize: 11, lineHeight: 16 }}>
                  Sent once to this dev machine's stdin. Never stored, never streamed, never passed to any AI coding agent.
                </Text>
                <View style={{ flexDirection: "row", gap: 10 }}>
                  <Pressable
                    onPress={() => void cancelSudo()}
                    disabled={sudoSubmitting}
                    style={[actionBtn(c), { backgroundColor: c.bgCard, borderColor: c.border, borderWidth: 1, flex: 1 }]}
                  >
                    <Text style={{ color: c.textPrimary, fontWeight: "700" }}>Cancel</Text>
                  </Pressable>
                  <Pressable
                    onPress={() => void submitSudo()}
                    disabled={sudoSubmitting || !sudoPassword}
                    style={[
                      actionBtn(c),
                      {
                        backgroundColor: c.accent,
                        flex: 1,
                        opacity: sudoSubmitting || !sudoPassword ? 0.5 : 1,
                      },
                    ]}
                  >
                    <Text style={{ color: "#fff", fontWeight: "800" }}>
                      {sudoSubmitting ? "Sending…" : "Send"}
                    </Text>
                  </Pressable>
                </View>
              </View>
            </View>
          </Modal>

          <Section
            c={c}
            title="Tooling"
            subtitle={
              target
                ? `Installing onto remote peer ${target}`
                : "Install coding agents and local model runtimes on this machine"
            }
          >
            {machineOptions.length > 1 ? (
              <ScrollView
                horizontal
                showsHorizontalScrollIndicator={false}
                contentContainerStyle={{ gap: 8, paddingVertical: 4 }}
              >
                <Pressable
                  onPress={() => setTarget(undefined)}
                  style={[
                    targetChip(c),
                    {
                      borderColor: !target ? c.accent : c.border,
                      backgroundColor: !target ? c.accent + "22" : c.bgCard,
                    },
                  ]}
                >
                  <Text style={{ color: !target ? c.accent : c.textPrimary, fontSize: 12, fontWeight: "700" }}>
                    This machine
                  </Text>
                </Pressable>
                {machineOptions
                  .filter((d) => d.id !== activeDevice?.id)
                  .map((d) => {
                    const selected = target === d.id;
                    return (
                      <Pressable
                        key={d.id}
                        onPress={() => setTarget(d.id)}
                        style={[
                          targetChip(c),
                          {
                            borderColor: selected ? c.accent : c.border,
                            backgroundColor: selected ? c.accent + "22" : c.bgCard,
                          },
                        ]}
                      >
                        <Text style={{ color: selected ? c.accent : c.textPrimary, fontSize: 12, fontWeight: "700" }}>
                          {d.name}
                        </Text>
                      </Pressable>
                    );
                  })}
              </ScrollView>
            ) : null}

            {catalogue.length === 0 ? (
              <Text style={{ color: c.textMuted, fontSize: 12, marginTop: 8 }}>
                No install targets advertised. This agent may be pre-1.98.0.
              </Text>
            ) : (
              <View style={{ gap: 8, marginTop: 10 }}>
                {catalogue.map((entry) => {
                  const meta = TOOL_META[entry.name] ?? {
                    emoji: "⚙️",
                    tagline: entry.description || "",
                  };
                  const isBusy = installingTool === entry.name;
                  return (
                    <View key={entry.name} style={[card(c), { gap: 8 }]}>
                      <View style={{ flexDirection: "row", alignItems: "flex-start", gap: 10 }}>
                        <Text style={{ fontSize: 24 }}>{meta.emoji}</Text>
                        <View style={{ flex: 1 }}>
                          <View style={{ flexDirection: "row", alignItems: "center", gap: 8 }}>
                            <Text style={{ color: c.textPrimary, fontSize: 15, fontWeight: "700" }}>
                              {entry.name}
                            </Text>
                            {entry.installed ? (
                              <View
                                style={{
                                  backgroundColor: "#22c55e22",
                                  paddingHorizontal: 8,
                                  paddingVertical: 2,
                                  borderRadius: 999,
                                }}
                              >
                                <Text style={{ color: "#22c55e", fontSize: 10, fontWeight: "800" }}>
                                  INSTALLED
                                </Text>
                              </View>
                            ) : (
                              <View
                                style={{
                                  backgroundColor: c.textMuted + "22",
                                  paddingHorizontal: 8,
                                  paddingVertical: 2,
                                  borderRadius: 999,
                                }}
                              >
                                <Text style={{ color: c.textMuted, fontSize: 10, fontWeight: "800" }}>
                                  NOT INSTALLED
                                </Text>
                              </View>
                            )}
                          </View>
                          <Text style={{ color: c.textMuted, fontSize: 12, marginTop: 4, lineHeight: 17 }}>
                            {meta.tagline || entry.description}
                          </Text>
                        </View>
                      </View>
                      <Pressable
                        onPress={() => void installTool(entry.name)}
                        disabled={!!installingTool}
                        style={[
                          actionBtn(c),
                          {
                            backgroundColor: entry.installed ? c.bgCard : c.accent,
                            borderWidth: entry.installed ? 1 : 0,
                            borderColor: c.border,
                            opacity: installingTool && !isBusy ? 0.5 : 1,
                          },
                        ]}
                      >
                        <Text
                          style={{
                            color: entry.installed ? c.textPrimary : "#fff",
                            fontWeight: "700",
                            fontSize: 13,
                          }}
                        >
                          {isBusy
                            ? "Installing…"
                            : entry.installed
                              ? "Reinstall / update"
                              : "Install"}
                        </Text>
                      </Pressable>
                    </View>
                  );
                })}
              </View>
            )}

            {installLog.length > 0 ? (
              <View
                style={{
                  marginTop: 12,
                  borderRadius: 12,
                  backgroundColor: "#000",
                  padding: 12,
                  maxHeight: 220,
                }}
              >
                <Text style={{ color: "#94a3b8", fontSize: 10, fontWeight: "800", marginBottom: 6 }}>
                  {installingTool
                    ? `INSTALLING · ${installingTool}`
                    : `LAST RUN · ${installResult?.tool ?? ""}${installResult?.status ? ` · ${installResult.status}` : ""}`}
                </Text>
                <ScrollView>
                  {installLog.slice(-30).map((line, idx) => (
                    <Text
                      key={idx}
                      style={{ color: "#e2e8f0", fontSize: 11, fontFamily: "Menlo", lineHeight: 15 }}
                    >
                      {line}
                    </Text>
                  ))}
                </ScrollView>
              </View>
            ) : null}
          </Section>

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

          <Section c={c} title="Sharing" subtitle="Guest access posture — who has a key to this machine">
            <View style={styles.metricGrid}>
              <Metric c={c} label="Accepted" value={`${summary.sharing.acceptedGuests}`} sub="active guests" />
              <Metric c={c} label="Pending" value={`${summary.sharing.pendingGuests}`} sub="pending invites" />
            </View>
            {guests.length > 0 ? (
              <View style={{ gap: 8, marginTop: 10 }}>
                {guests.slice(0, 12).map((g) => (
                  <View key={g.email} style={[card(c), { gap: 4 }]}>
                    <View style={{ flexDirection: "row", alignItems: "center", gap: 8 }}>
                      <View
                        style={{
                          width: 8,
                          height: 8,
                          borderRadius: 4,
                          backgroundColor:
                            g.status === "accepted"
                              ? "#22c55e"
                              : g.status === "pending"
                                ? "#f59e0b"
                                : c.textMuted,
                        }}
                      />
                      <Text style={{ color: c.textPrimary, fontSize: 13, fontWeight: "700", flex: 1 }} numberOfLines={1}>
                        {g.fullName?.trim() || g.email}
                      </Text>
                      <Text style={{ color: c.textMuted, fontSize: 10, fontWeight: "700", textTransform: "uppercase" }}>
                        {g.status}
                      </Text>
                    </View>
                    {g.fullName ? (
                      <Text style={{ color: c.textMuted, fontSize: 11 }} numberOfLines={1}>
                        {g.email}
                      </Text>
                    ) : null}
                    {g.inviteCode && g.status === "pending" ? (
                      <Text style={{ color: c.accent, fontSize: 11, fontWeight: "700", letterSpacing: 0.6 }}>
                        Code {g.inviteCode}
                      </Text>
                    ) : null}
                    {g.proposedDeviceIds && g.proposedDeviceIds.length > 0 ? (
                      <Text style={{ color: c.textMuted, fontSize: 10 }}>
                        Scoped to {g.proposedDeviceIds.length} device{g.proposedDeviceIds.length === 1 ? "" : "s"}
                      </Text>
                    ) : null}
                  </View>
                ))}
                {guests.length > 12 ? (
                  <Text style={{ color: c.textMuted, fontSize: 11, textAlign: "center" }}>
                    +{guests.length - 12} more
                  </Text>
                ) : null}
              </View>
            ) : (
              <Text style={{ color: c.textMuted, fontSize: 12, marginTop: 8 }}>
                Nobody is sharing this machine yet. Invite from the Guests tab.
              </Text>
            )}
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

function targetChip(c: any) {
  return {
    borderRadius: 999,
    borderWidth: 1,
    paddingHorizontal: 14,
    paddingVertical: 7,
  } as const;
}

const styles = StyleSheet.create({
  container: { flex: 1 },
  header: { flexDirection: "row", alignItems: "center", justifyContent: "space-between", paddingHorizontal: 16, paddingBottom: 12, borderBottomWidth: 1 },
  metricGrid: { flexDirection: "row", gap: 8, flexWrap: "wrap" },
});
