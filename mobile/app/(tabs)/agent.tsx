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
import { useLocalSearchParams, useRouter } from "expo-router";
import { AppScreenHeader } from "../../src/components/AppScreenHeader";
import { useColors } from "../../src/context/ThemeContext";
import { useDevice } from "../../src/context/DeviceContext";
import { quicClient, type AgentGraphRun, type MachineInfo, type RunnerInfo } from "../../src/lib/quic";
import { describeConnectionStatus } from "../../src/lib/connection";
import { useResponsiveLayout } from "../../src/hooks/useResponsiveLayout";
import { useTabletContentStyle } from "../../src/hooks/useTabletContentStyle";

export default function AgentModeScreen() {
  const c = useColors();
  const router = useRouter();
  const { connectionStatus } = useDevice();
  const params = useLocalSearchParams<{ project?: string; path?: string }>();
  const isConnected = connectionStatus === "connected";
  const layout = useResponsiveLayout();
  // Landscape tablet: split the builder form (left) from the live
  // run list (right) so both are visible at once — the natural
  // "configure ↔ watch" cockpit. Portrait/phone stay single-column.
  const twoPane = layout.layoutClass === "tablet-landscape";
  const tabletContent = useTabletContentStyle("regular");

  const [runs, setRuns] = useState<AgentGraphRun[]>([]);
  const [refreshing, setRefreshing] = useState(false);
  const [starting, setStarting] = useState(false);
  const [runners, setRunners] = useState<RunnerInfo[]>([]);
  const [machines, setMachines] = useState<MachineInfo[]>([]);

  const [name, setName] = useState(params.project ? `${params.project}-agent` : "");
  const [workDir, setWorkDir] = useState(params.path ?? "");
  const [prompt, setPrompt] = useState("");
  const [runner, setRunner] = useState("");
  const [selectedDevices, setSelectedDevices] = useState<string[]>([]);
  const [template, setTemplate] = useState<"full" | "ship">("full");
  const [maxParallel, setMaxParallel] = useState("2");
  // Cost mode: 0 = single-model (your plan), 2 = duo (Claude Code + GLM),
  // 3 = trio (Claude Code + Codex + GLM). Independent slices spread across the
  // lanes; coherence stays on the flat plans, overflow spills to cheap GLM.
  const [hybridDegree, setHybridDegree] = useState(0);

  const refresh = useCallback(async () => {
    if (!isConnected) return;
    setRefreshing(true);
    try {
      const [graphs, runnerResult, machineInventory] = await Promise.all([
        quicClient.agentGraphs(),
        quicClient.getRunnersState(),
        quicClient.consoleMachines(),
      ]);
      setRuns(graphs);
      const installed = runnerResult.state === "loaded" ? runnerResult.runners.filter((r) => r.installed) : [];
      if (runnerResult.state === "loaded") {
        setRunners(installed);
      }
      setMachines((machineInventory.machines || []).filter((m) => m.isOnline));
      if (!runner && runnerResult.state === "loaded") {
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
        preferredDevice: selectedDevices.length == 1 ? selectedDevices[0] : undefined,
        allowedDevices: selectedDevices,
        hybridDegree: hybridDegree || undefined,
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
  }, [workDir, prompt, name, runner, template, maxParallel, selectedDevices, hybridDegree, refresh]);

  const intro = (
    <>
      <Text style={[styles.title, { color: c.textPrimary }]}>Agent Mode</Text>
      <Text style={[styles.subtitle, { color: c.textSecondary }]}>
        Dependency-aware orchestration across chat, autoideas, and autotest.
      </Text>
    </>
  );

  const configCard = (
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

          <Text style={[styles.label, { color: c.textPrimary }]}>Cost Mode</Text>
          <View style={styles.segmentRow}>
            {([[0, "Single"], [2, "Duo"], [3, "Trio"]] as [number, string][]).map(([deg, label]) => (
              <Pressable
                key={deg}
                onPress={() => setHybridDegree(deg)}
                style={[styles.segment, { borderColor: c.border, backgroundColor: hybridDegree === deg ? c.accent : c.bg }]}
              >
                <Text style={{ color: hybridDegree === deg ? "#fff" : c.textPrimary, fontWeight: "600" }}>{label}</Text>
              </Pressable>
            ))}
          </View>
          <Text style={[styles.subtitle, { color: c.textMuted, marginTop: 4 }]}>
            Single = your subscription plan only. Duo = Claude Code + GLM. Trio = Claude Code + Codex + GLM. Coherence-critical work stays on the flat plans; parallel overflow spills to the cheap GLM apikey lane.
          </Text>

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
                  onPress={() => setSelectedDevices([])}
                  style={[styles.segment, { borderColor: c.border, backgroundColor: selectedDevices.length === 0 ? c.accent : c.bg }]}
                >
                  <Text style={{ color: selectedDevices.length === 0 ? "#fff" : c.textPrimary, fontWeight: "600" }}>
                    Auto
                  </Text>
                </Pressable>
                {machines.slice(0, 6).map((m) => (
                  <Pressable
                    key={m.deviceId}
                    onPress={() => setSelectedDevices((current) => current.includes(m.deviceId) ? current.filter((id) => id !== m.deviceId) : [...current, m.deviceId])}
                    style={[styles.segment, { borderColor: c.border, backgroundColor: selectedDevices.includes(m.deviceId) ? c.accent : c.bg }]}
                  >
                    <Text style={{ color: selectedDevices.includes(m.deviceId) ? "#fff" : c.textPrimary, fontWeight: "600" }}>
                      {m.name}
                    </Text>
                  </Pressable>
                ))}
              </View>
              <Text style={[styles.helper, { color: c.textSecondary }]}>
                Select one or several machines. Yaver will load-balance across the selected pool, respect runner caps for Claude/Codex, and use machine signatures for TestFlight, Android, local-LLM, and deploy decisions.
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
  );

  const runsList = (
    <>
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
                    try {
                      await quicClient.stopAgentGraph(run.id);
                      refresh();
                    } catch (e) {
                      Alert.alert(
                        "Stop Failed",
                        `${e instanceof Error ? e.message : String(e)}\n\nYaver ${describeConnectionStatus(connectionStatus)}. The graph may keep running — retry once reconnected.`,
                      );
                    }
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
    </>
  );

  const runsEmpty = (
    <View style={styles.runsEmpty}>
      <Text style={[styles.subtitle, { color: c.textMuted, textAlign: "center" }]}>
        No agent graphs running yet. Configure one on the left and tap Start.
      </Text>
    </View>
  );

  return (
    <SafeAreaView style={[styles.root, { backgroundColor: c.bg }]} edges={[]}>
      <AppScreenHeader title="Agent Mode" onBack={() => router.navigate("/(tabs)/more" as any)} />
      {twoPane ? (
        <View style={styles.twoPane}>
          <ScrollView
            style={[styles.pane, { borderRightWidth: 1, borderRightColor: c.border }]}
            contentContainerStyle={styles.content}
            refreshControl={<RefreshControl refreshing={refreshing} onRefresh={refresh} />}
          >
            {intro}
            {configCard}
          </ScrollView>
          <ScrollView
            style={styles.pane}
            contentContainerStyle={styles.content}
            refreshControl={<RefreshControl refreshing={refreshing} onRefresh={refresh} />}
          >
            {runs.length ? runsList : runsEmpty}
          </ScrollView>
        </View>
      ) : (
        <ScrollView
          refreshControl={<RefreshControl refreshing={refreshing} onRefresh={refresh} />}
          contentContainerStyle={[styles.content, tabletContent]}
        >
          {intro}
          {configCard}
          {runsList}
        </ScrollView>
      )}
    </SafeAreaView>
  );
}

const styles = StyleSheet.create({
  root: { flex: 1 },
  content: { padding: 16, gap: 14 },
  twoPane: { flex: 1, flexDirection: "row" },
  pane: { flex: 1 },
  runsEmpty: { padding: 32, alignItems: "center" },
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
