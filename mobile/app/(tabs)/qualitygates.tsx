import React, { useCallback, useEffect, useRef, useState } from "react";
import {
  ActivityIndicator,
  Alert,
  FlatList,
  Pressable,
  RefreshControl,
  ScrollView,
  StyleSheet,
  Text,
  View,
} from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { useRouter } from "expo-router";
import { AppScreenHeader } from "../../src/components/AppScreenHeader";
import { useColors } from "../../src/context/ThemeContext";
import { useDevice } from "../../src/context/DeviceContext";
import { quicClient } from "../../src/lib/quic";
import { useResponsiveLayout } from "../../src/hooks/useResponsiveLayout";
import { useTabletContentStyle } from "../../src/hooks/useTabletContentStyle";

// ── Types ────────────────────────────────────────────────────────────

interface QualityCheck {
  type: string;
  available: boolean;
  command: string;
  framework: string;
}

interface QualityResult {
  id: string;
  type: string;
  status: string; // "running" | "passed" | "warning" | "failed"
  duration?: number;
  output?: string;
  passed?: boolean;
  exitCode?: number;
  startedAt?: string;
  issues?: number;
}

const TYPE_LABELS: Record<string, string> = {
  test: "Test",
  lint: "Lint",
  typecheck: "TypeCheck",
  format: "Format",
};

const STATUS_ICONS: Record<string, string> = {
  running: "\u25CB",
  queued: "\u25CB",
  passed: "\u2713",
  warning: "\u26A0",
  failed: "\u2717",
};

const STATUS_COLORS: Record<string, string> = {
  passed: "#22c55e",
  warning: "#f59e0b",
  failed: "#ef4444",
};

// ── Screen ───────────────────────────────────────────────────────────

export default function QualityGatesScreen() {
  const c = useColors();
  const insets = useSafeAreaInsets();
  const layout = useResponsiveLayout();
  const tabletContent = useTabletContentStyle("wide");
  const router = useRouter();
  const { connectionStatus } = useDevice();
  const connected = connectionStatus === "connected";

  const [checks, setChecks] = useState<QualityCheck[]>([]);
  const [results, setResults] = useState<QualityResult[]>([]);
  const [loading, setLoading] = useState(true);
  const [refreshing, setRefreshing] = useState(false);
  const [runningTypes, setRunningTypes] = useState<Set<string>>(new Set());
  const [expandedResult, setExpandedResult] = useState<string | null>(null);
  const pollRef = useRef<ReturnType<typeof setInterval> | null>(null);

  const loadData = useCallback(async () => {
    try {
      const [detectedChecks, existingResults] = await Promise.all([
        quicClient.detectQualityChecks(),
        quicClient.getQualityResults(),
      ]);
      setChecks(detectedChecks || []);
      setResults(existingResults || []);
    } catch {
      // silent
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    if (!connected) return;
    loadData();
  }, [connected, loadData]);

  // Poll while checks are running
  useEffect(() => {
    if (runningTypes.size > 0) {
      pollRef.current = setInterval(async () => {
        try {
          const r = await quicClient.getQualityResults();
          setResults(r);
          const stillRunning = new Set<string>();
          for (const type of runningTypes) {
            const result = r.find((res: QualityResult) => res.type === type);
            if (result && (result.status === "running" || result.status === "queued")) {
              stillRunning.add(type);
            }
          }
          setRunningTypes(stillRunning);
        } catch {}
      }, 3000);
    }
    return () => {
      if (pollRef.current) clearInterval(pollRef.current);
    };
  }, [runningTypes]);

  const onRefresh = useCallback(async () => {
    setRefreshing(true);
    await loadData();
    setRefreshing(false);
  }, [loadData]);

  const handleRunCheck = useCallback(async (type: string) => {
    try {
      setRunningTypes((prev) => new Set(prev).add(type));
      await quicClient.runQualityCheck(type);
    } catch (e) {
      setRunningTypes((prev) => {
        const next = new Set(prev);
        next.delete(type);
        return next;
      });
      Alert.alert("Error", e instanceof Error ? e.message : "Failed to run check");
    }
  }, []);

  const handleRunAll = useCallback(async () => {
    try {
      const available = checks.filter((ch) => ch.available);
      setRunningTypes(new Set(available.map((ch) => ch.type)));
      await quicClient.runAllQualityChecks();
    } catch (e) {
      setRunningTypes(new Set());
      Alert.alert("Error", e instanceof Error ? e.message : "Failed to run checks");
    }
  }, [checks]);

  const availableChecks = checks.filter((ch) => ch.available);
  const useSplitPane = layout.layoutClass === "tablet-landscape";
  const expanded = expandedResult ? results.find((r) => r.id === expandedResult) : null;

  const renderResult = ({ item: r }: { item: QualityResult }) => {
    const passed = r.status === "passed" || (r.exitCode === 0 && r.status === "completed");
    const isRunning = r.status === "running" || r.status === "queued";
    const isWarning = r.status === "warning";
    const statusKey = isRunning ? "running" : isWarning ? "warning" : passed ? "passed" : "failed";
    const statusIcon = STATUS_ICONS[statusKey] || "\u25CB";
    const statusColor = isRunning ? c.textMuted : STATUS_COLORS[statusKey] || "#ef4444";
    const isExpanded = expandedResult === r.id;

    return (
      <View>
        <Pressable
          style={[st.resultCard, { backgroundColor: c.bgCard, borderColor: c.border }]}
          onPress={() => setExpandedResult(isExpanded ? null : r.id)}
        >
          <View style={st.resultRow}>
            <Text style={{ color: statusColor, fontSize: 18, fontWeight: "700", width: 28 }}>
              {statusIcon}
            </Text>
            <View style={{ flex: 1 }}>
              <Text style={{ color: c.textPrimary, fontSize: 15, fontWeight: "600" }}>
                {TYPE_LABELS[r.type] || r.type}
              </Text>
              {r.issues != null && r.issues > 0 && (
                <Text style={{ color: statusColor, fontSize: 12, marginTop: 2 }}>
                  {r.issues} issue{r.issues !== 1 ? "s" : ""}
                </Text>
              )}
            </View>
            {r.duration != null && (
              <Text style={{ color: c.textMuted, fontSize: 12 }}>
                {(r.duration / 1000).toFixed(1)}s
              </Text>
            )}
            <Text style={{ color: c.textMuted, fontSize: 14, marginLeft: 8 }}>
              {isExpanded ? "\u2304" : "\u203A"}
            </Text>
          </View>
        </Pressable>
        {!useSplitPane && isExpanded && r.output && (
          <ScrollView
            style={[st.outputBox, { backgroundColor: c.bg, borderColor: c.border }]}
            nestedScrollEnabled
          >
            <Text style={{ color: c.textMuted, fontSize: 11, fontFamily: "Courier" }}>
              {r.output}
            </Text>
          </ScrollView>
        )}
      </View>
    );
  };

  const ListHeader = () => (
    <View style={{ gap: 10, marginBottom: 8 }}>
      {/* Action buttons */}
      <View style={{ flexDirection: "row", flexWrap: "wrap", gap: 8 }}>
        {availableChecks.length > 1 && (
          <Pressable style={[st.btn, { backgroundColor: c.accent }]} onPress={handleRunAll}>
            <Text style={[st.btnText, { color: "#fff" }]}>Run All</Text>
          </Pressable>
        )}
        {availableChecks.map((ch) => (
          <Pressable
            key={ch.type}
            style={[st.btn, { backgroundColor: c.bgCard, borderWidth: 1, borderColor: c.border }]}
            onPress={() => handleRunCheck(ch.type)}
            disabled={runningTypes.has(ch.type)}
          >
            {runningTypes.has(ch.type) ? (
              <ActivityIndicator size="small" color={c.accent} />
            ) : (
              <Text style={[st.btnText, { color: c.textPrimary }]}>
                {TYPE_LABELS[ch.type] || ch.type}
              </Text>
            )}
          </Pressable>
        ))}
      </View>

      {availableChecks.length === 0 && !loading && (
        <Text style={{ color: c.textMuted, fontSize: 13, paddingVertical: 4 }}>
          No quality checks detected for this project.
        </Text>
      )}
    </View>
  );

  const ListEmpty = () =>
    !loading && results.length === 0 ? (
      <View style={{ paddingVertical: 40, alignItems: "center" }}>
        <Text style={{ color: c.textMuted, fontSize: 40, marginBottom: 12 }}>{"\u2714"}</Text>
        <Text style={{ color: c.textMuted, fontSize: 14, textAlign: "center" }}>
          No results yet.{"\n"}Run a quality check to get started.
        </Text>
      </View>
    ) : null;

  return (
    <View style={[st.container, { backgroundColor: c.bg, paddingTop: insets.top }]}>
      <AppScreenHeader title="Quality Gates" onBack={() => router.navigate("/(tabs)/more" as any)} />

      {!connected ? (
        <View style={{ flex: 1, justifyContent: "center", alignItems: "center", padding: 20 }}>
          <Text style={{ color: c.textMuted, fontSize: 14, textAlign: "center" }}>
            Connect to a device to use Quality Gates.
          </Text>
          <Pressable
            onPress={() => router.navigate("/(tabs)/devices" as any)}
            style={({ pressed }) => [{ marginTop: 16, paddingHorizontal: 16, paddingVertical: 10, borderRadius: 10, backgroundColor: c.accent + "1A" }, pressed && { opacity: 0.6 }]}
          >
            <Text style={{ color: c.accent, fontSize: 14, fontWeight: "600" }}>Go to Devices</Text>
          </Pressable>
        </View>
      ) : loading ? (
        <View style={{ flex: 1, justifyContent: "center", alignItems: "center" }}>
          <ActivityIndicator color={c.accent} />
        </View>
      ) : useSplitPane ? (
        <View style={[st.splitShell, tabletContent]}>
          <FlatList
            style={[st.resultsPane, { borderColor: c.border }]}
            data={results.slice(0, 20)}
            keyExtractor={(item) => item.id}
            renderItem={renderResult}
            ListHeaderComponent={ListHeader}
            ListEmptyComponent={ListEmpty}
            contentContainerStyle={st.splitListContent}
            refreshControl={
              <RefreshControl refreshing={refreshing} onRefresh={onRefresh} tintColor={c.accent} />
            }
          />
          <View style={[st.outputPane, { backgroundColor: c.bgCard, borderColor: c.border }]}>
            <Text style={{ color: c.textPrimary, fontSize: 15, fontWeight: "600", marginBottom: 8 }}>
              {expanded ? `${TYPE_LABELS[expanded.type] || expanded.type} output` : "Output"}
            </Text>
            {expanded?.output ? (
              <ScrollView style={[st.outputPaneScroll, { backgroundColor: c.bg, borderColor: c.border }]}>
                <Text style={{ color: c.textMuted, fontSize: 12, fontFamily: "Courier" }}>
                  {expanded.output}
                </Text>
              </ScrollView>
            ) : (
              <View style={st.outputPaneEmpty}>
                <Text style={{ color: c.textMuted, fontSize: 13, textAlign: "center" }}>
                  Select a result with output to inspect logs.
                </Text>
              </View>
            )}
          </View>
        </View>
      ) : (
        <FlatList
          data={results.slice(0, 20)}
          keyExtractor={(item) => item.id}
          renderItem={renderResult}
          ListHeaderComponent={ListHeader}
          ListEmptyComponent={ListEmpty}
          contentContainerStyle={[{ padding: 16, gap: 10 }, tabletContent]}
          refreshControl={
            <RefreshControl refreshing={refreshing} onRefresh={onRefresh} tintColor={c.accent} />
          }
        />
      )}
    </View>
  );
}

// ── Styles ───────────────────────────────────────────────────────────

const st = StyleSheet.create({
  container: { flex: 1 },
  header: {
    flexDirection: "row",
    alignItems: "center",
    justifyContent: "space-between",
    paddingHorizontal: 16,
    paddingVertical: 12,
    borderBottomWidth: StyleSheet.hairlineWidth,
  },
  headerTitle: { fontSize: 17, fontWeight: "700" },
  resultCard: {
    borderRadius: 12,
    padding: 14,
    borderWidth: 1,
  },
  resultRow: {
    flexDirection: "row",
    alignItems: "center",
    gap: 8,
  },
  outputBox: {
    maxHeight: 250,
    borderWidth: 1,
    borderRadius: 8,
    padding: 10,
    marginTop: 4,
  },
  splitShell: {
    flex: 1,
    flexDirection: "row",
    gap: 14,
    paddingHorizontal: 16,
    paddingVertical: 12,
  },
  resultsPane: {
    flex: 1,
    borderWidth: 1,
    borderRadius: 12,
  },
  splitListContent: {
    padding: 12,
    gap: 10,
  },
  outputPane: {
    width: 400,
    borderWidth: 1,
    borderRadius: 12,
    padding: 14,
  },
  outputPaneScroll: {
    flex: 1,
    borderWidth: 1,
    borderRadius: 8,
    padding: 10,
  },
  outputPaneEmpty: {
    flex: 1,
    justifyContent: "center",
    alignItems: "center",
    padding: 20,
  },
  btn: {
    paddingHorizontal: 14,
    paddingVertical: 8,
    borderRadius: 8,
    alignItems: "center",
    justifyContent: "center",
    minWidth: 60,
  },
  btnText: { fontSize: 13, fontWeight: "600" },
});
