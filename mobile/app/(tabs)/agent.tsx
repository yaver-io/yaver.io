import React, { useCallback, useEffect, useState } from "react";
import {
  Alert,
  Pressable,
  RefreshControl,
  ScrollView,
  StyleSheet,
  Text,
  TextInput,
  View,
} from "react-native";
import { SafeAreaView } from "react-native-safe-area-context";
import { useLocalSearchParams } from "expo-router";
import { useColors } from "../../src/context/ThemeContext";
import { useDevice } from "../../src/context/DeviceContext";
import { quicClient, type AgentGraphRun, type MachineInfo, type RunnerInfo } from "../../src/lib/quic";

export default function AgentModeScreen() {
  const c = useColors();
  const { connectionStatus } = useDevice();
  const params = useLocalSearchParams<{ project?: string; path?: string }>();
  const isConnected = connectionStatus === "connected";

  const [runs, setRuns] = useState<AgentGraphRun[]>([]);
  const [refreshing, setRefreshing] = useState(false);
  const [starting, setStarting] = useState(false);
  const [runners, setRunners] = useState<RunnerInfo[]>([]);
  const [machines, setMachines] = useState<MachineInfo[]>([]);

  const [name, setName] = useState(params.project ? `${params.project}-agent` : "");
  const [workDir, setWorkDir] = useState(params.path ?? "");
  const [prompt, setPrompt] = useState("");
  const [runner, setRunner] = useState("");
  const [preferredDevice, setPreferredDevice] = useState("");
  const [template, setTemplate] = useState<"full" | "ship">("full");
  const [maxParallel, setMaxParallel] = useState("2");

  const refresh = useCallback(async () => {
    if (!isConnected) return;
    setRefreshing(true);
    try {
      const [graphs, availableRunners, machineInventory] = await Promise.all([
        quicClient.agentGraphs(),
        quicClient.getRunners(),
        quicClient.consoleMachines(),
      ]);
      setRuns(graphs);
      const installed = availableRunners.filter((r) => r.installed);
      setRunners(installed);
      setMachines((machineInventory.machines || []).filter((m) => m.isOnline));
      if (!runner) {
        const preferred = installed.find((r) => r.isDefault) ?? installed[0];
        if (preferred) setRunner(preferred.id);
      }
    } finally {
      setRefreshing(false);
    }
  }, [isConnected, runner]);

  useEffect(() => {
    refresh();
    if (!isConnected) return;
    const interval = setInterval(refresh, 3000);
    return () => clearInterval(interval);
  }, [isConnected, refresh]);

  const handleStart = useCallback(async () => {
    if (!workDir.trim() || !prompt.trim()) {
      Alert.alert("Missing fields", "Work dir and prompt are required.");
      return;
    }
    setStarting(true);
    try {
      const res = await quicClient.createAgentGraph({
        name: name || undefined,
        workDir,
        prompt,
        runner: runner || undefined,
        template,
        maxParallel: Math.max(1, parseInt(maxParallel || "2", 10) || 2),
        preferredDevice: preferredDevice || undefined,
      });
      if (!res.ok) {
        Alert.alert("Start failed", res.error || "Could not create agent graph");
        return;
      }
      setPrompt("");
      await refresh();
    } finally {
      setStarting(false);
    }
  }, [workDir, prompt, name, runner, template, maxParallel, preferredDevice, refresh]);

  return (
    <SafeAreaView style={[styles.root, { backgroundColor: c.bg }]} edges={["top"]}>
      <ScrollView
        refreshControl={<RefreshControl refreshing={refreshing} onRefresh={refresh} />}
        contentContainerStyle={styles.content}
      >
        <Text style={[styles.title, { color: c.textPrimary }]}>Agent Mode</Text>
        <Text style={[styles.subtitle, { color: c.textSecondary }]}>
          Dependency-aware orchestration across chat, autoideas, autodev, and autotest.
        </Text>

        <View style={[styles.card, { backgroundColor: c.bgCard, borderColor: c.border }]}>
          <Text style={[styles.label, { color: c.textPrimary }]}>Name</Text>
          <TextInput value={name} onChangeText={setName} placeholder="my-project-agent" placeholderTextColor={c.textMuted} style={[styles.input, { color: c.textPrimary, borderColor: c.border }]} />

          <Text style={[styles.label, { color: c.textPrimary }]}>Work Dir</Text>
          <TextInput value={workDir} onChangeText={setWorkDir} placeholder="/abs/path/to/project" placeholderTextColor={c.textMuted} style={[styles.input, { color: c.textPrimary, borderColor: c.border }]} />

          <Text style={[styles.label, { color: c.textPrimary }]}>Goal</Text>
          <TextInput
            value={prompt}
            onChangeText={setPrompt}
            placeholder="Ship onboarding, wire billing, and leave the app green."
            placeholderTextColor={c.textMuted}
            multiline
            style={[styles.textarea, { color: c.textPrimary, borderColor: c.border }]}
          />

          <View style={styles.row}>
            <View style={styles.half}>
              <Text style={[styles.label, { color: c.textPrimary }]}>Template</Text>
              <View style={styles.segmentRow}>
                {(["full", "ship"] as const).map((value) => (
                  <Pressable
                    key={value}
                    onPress={() => setTemplate(value)}
                    style={[styles.segment, { borderColor: c.border, backgroundColor: template === value ? c.accent : c.bg }]}
                  >
                    <Text style={{ color: template === value ? "#fff" : c.textPrimary, fontWeight: "600" }}>
                      {value}
                    </Text>
                  </Pressable>
                ))}
              </View>
            </View>
            <View style={styles.half}>
              <Text style={[styles.label, { color: c.textPrimary }]}>Max Parallel</Text>
              <TextInput value={maxParallel} onChangeText={setMaxParallel} keyboardType="number-pad" style={[styles.input, { color: c.textPrimary, borderColor: c.border }]} />
            </View>
          </View>

          {!!runners.length && (
            <>
              <Text style={[styles.label, { color: c.textPrimary }]}>Runner</Text>
              <View style={styles.segmentWrap}>
                <Pressable
                  onPress={() => setRunner("")}
                  style={[styles.segment, { borderColor: c.border, backgroundColor: runner === "" ? c.accent : c.bg }]}
                >
                  <Text style={{ color: runner === "" ? "#fff" : c.textPrimary, fontWeight: "600" }}>
                    Auto
                  </Text>
                </Pressable>
                {runners.slice(0, 4).map((r) => (
                  <Pressable
                    key={r.id}
                    onPress={() => setRunner(r.id)}
                    style={[styles.segment, { borderColor: c.border, backgroundColor: runner === r.id ? c.accent : c.bg }]}
                  >
                    <Text style={{ color: runner === r.id ? "#fff" : c.textPrimary, fontWeight: "600" }}>
                      {r.name}
                    </Text>
                  </Pressable>
                ))}
              </View>
            </>
          )}

          {!!machines.length && (
            <>
              <Text style={[styles.label, { color: c.textPrimary }]}>Machine</Text>
              <View style={styles.segmentWrap}>
                <Pressable
                  onPress={() => setPreferredDevice("")}
                  style={[styles.segment, { borderColor: c.border, backgroundColor: preferredDevice === "" ? c.accent : c.bg }]}
                >
                  <Text style={{ color: preferredDevice === "" ? "#fff" : c.textPrimary, fontWeight: "600" }}>
                    Auto
                  </Text>
                </Pressable>
                {machines.slice(0, 6).map((m) => (
                  <Pressable
                    key={m.deviceId}
                    onPress={() => setPreferredDevice(m.deviceId)}
                    style={[styles.segment, { borderColor: c.border, backgroundColor: preferredDevice === m.deviceId ? c.accent : c.bg }]}
                  >
                    <Text style={{ color: preferredDevice === m.deviceId ? "#fff" : c.textPrimary, fontWeight: "600" }}>
                      {m.name}
                    </Text>
                  </Pressable>
                ))}
              </View>
              <Text style={[styles.helper, { color: c.textSecondary }]}>
                Auto placement prefers planning on stronger Claude-ready machines, cheaper runners for bulk edits, macOS for TestFlight, and Android-capable hosts for Play Store work.
              </Text>
            </>
          )}

          <Pressable
            onPress={handleStart}
            disabled={starting}
            style={[styles.primaryBtn, { backgroundColor: c.accent, opacity: starting ? 0.7 : 1 }]}
          >
            <Text style={styles.primaryBtnText}>{starting ? "Starting..." : "Start Agent Graph"}</Text>
          </Pressable>
        </View>

        {runs.map((run) => (
          <View key={run.id} style={[styles.card, { backgroundColor: c.bgCard, borderColor: c.border }]}>
            <View style={styles.headerRow}>
              <View style={{ flex: 1 }}>
                <Text style={[styles.runTitle, { color: c.textPrimary }]}>{run.name}</Text>
                <Text style={[styles.meta, { color: c.textSecondary }]}>
                  {run.status} • {run.nodes.length} nodes • parallel {run.maxParallel}
                </Text>
              </View>
              {(run.status === "running" || run.status === "queued") && (
                <Pressable
                  onPress={async () => {
                    await quicClient.stopAgentGraph(run.id);
                    refresh();
                  }}
                  style={[styles.stopBtn, { borderColor: c.border }]}
                >
                  <Text style={{ color: c.textPrimary, fontWeight: "700" }}>Stop</Text>
                </Pressable>
              )}
            </View>
            {!!run.summary && <Text style={[styles.summary, { color: c.textSecondary }]}>{run.summary}</Text>}
            {run.nodes.map((node) => (
              <View key={node.spec.id} style={[styles.nodeRow, { borderTopColor: c.border }]}>
                <View style={{ flex: 1 }}>
                  <Text style={[styles.nodeTitle, { color: c.textPrimary }]}>
                    {node.spec.title} <Text style={{ color: c.textMuted }}>({node.spec.kind})</Text>
                  </Text>
                  <Text style={[styles.meta, { color: c.textSecondary }]}>
                    {node.status}
                    {node.spec.dependsOn?.length ? ` • deps ${node.spec.dependsOn.join(", ")}` : ""}
                  </Text>
                  {!!node.placement && (
                    <Text style={[styles.meta, { color: c.textSecondary }]}>
                      {node.placement.deviceName || node.placement.deviceId}
                      {node.placement.runner ? ` • ${node.placement.runner}` : ""}
                    </Text>
                  )}
                  {!!node.summary && <Text style={[styles.nodeSummary, { color: c.textSecondary }]}>{node.summary}</Text>}
                  {!!node.error && <Text style={[styles.nodeError, { color: "#ef4444" }]}>{node.error}</Text>}
                  {!!node.placement?.reason && <Text style={[styles.helper, { color: c.textMuted }]}>{node.placement.reason}</Text>}
                </View>
              </View>
            ))}
          </View>
        ))}
      </ScrollView>
    </SafeAreaView>
  );
}

const styles = StyleSheet.create({
  root: { flex: 1 },
  content: { padding: 16, gap: 14 },
  title: { fontSize: 28, fontWeight: "800" },
  subtitle: { fontSize: 14, lineHeight: 20 },
  card: { borderWidth: 1, borderRadius: 16, padding: 14, gap: 10 },
  label: { fontSize: 13, fontWeight: "700" },
  helper: { fontSize: 12, lineHeight: 18 },
  input: { borderWidth: 1, borderRadius: 12, paddingHorizontal: 12, paddingVertical: 10, fontSize: 14 },
  textarea: { borderWidth: 1, borderRadius: 12, paddingHorizontal: 12, paddingVertical: 10, minHeight: 110, fontSize: 14, textAlignVertical: "top" },
  row: { flexDirection: "row", gap: 10 },
  half: { flex: 1 },
  segmentRow: { flexDirection: "row", gap: 8 },
  segmentWrap: { flexDirection: "row", flexWrap: "wrap", gap: 8 },
  segment: { borderWidth: 1, borderRadius: 999, paddingHorizontal: 12, paddingVertical: 8 },
  primaryBtn: { borderRadius: 12, paddingVertical: 14, alignItems: "center", marginTop: 4 },
  primaryBtnText: { color: "#fff", fontWeight: "800", fontSize: 15 },
  headerRow: { flexDirection: "row", gap: 12, alignItems: "center" },
  runTitle: { fontSize: 17, fontWeight: "800" },
  meta: { fontSize: 12 },
  summary: { fontSize: 13, lineHeight: 18 },
  stopBtn: { borderWidth: 1, borderRadius: 999, paddingHorizontal: 12, paddingVertical: 8 },
  nodeRow: { borderTopWidth: 1, paddingTop: 10 },
  nodeTitle: { fontSize: 14, fontWeight: "700" },
  nodeSummary: { fontSize: 13, lineHeight: 18, marginTop: 4 },
  nodeError: { fontSize: 12, marginTop: 4 },
});
