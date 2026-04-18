import React, { useCallback, useEffect, useState } from "react";
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
import { SafeAreaView } from "react-native-safe-area-context";
import { useRouter } from "expo-router";
import { useVideoPlayer, VideoView } from "expo-video";
import { useColors } from "../src/context/ThemeContext";
import { AppBackButton } from "../src/components/AppBackButton";
import {
  morningGetRun,
  morningListRuns,
  morningRollback,
  morningVideoRequest,
  quicClient,
  type MorningRunSummary,
  type MorningTaskHighlight,
} from "../src/lib/quic";

// morning.tsx — mobile match-report. Vertical card stack of everything
// that shipped overnight. Tapping Rollback triggers a git-revert on
// the agent through the same relay/QUIC channel as any other /tasks
// call. Videos byte-range through the same path with a Bearer header,
// so nothing sensitive ever leaves the phone in the clear.

export default function MorningScreen() {
  const c = useColors();
  const router = useRouter();
  const [runs, setRuns] = useState<MorningRunSummary[]>([]);
  const [selected, setSelected] = useState<MorningRunSummary | null>(null);
  const [loading, setLoading] = useState(true);
  const [refreshing, setRefreshing] = useState(false);
  const [rolling, setRolling] = useState<string | null>(null);

  const refresh = useCallback(async () => {
    if (!quicClient.isConnected) {
      setRuns([]);
      setSelected(null);
      setLoading(false);
      return;
    }
    const list = await morningListRuns(20);
    setRuns(list);
    if (list.length > 0) {
      const detail = await morningGetRun(list[0].runId);
      setSelected(detail);
    }
    setLoading(false);
  }, []);

  useEffect(() => {
    refresh();
  }, [refresh]);

  const pickRun = async (run: MorningRunSummary) => {
    const detail = await morningGetRun(run.runId);
    setSelected(detail ?? run);
  };

  const handleRollback = async (task: MorningTaskHighlight) => {
    if (!selected) return;
    setRolling(task.taskId);
    const result = await morningRollback(selected.runId, task.taskId);
    setRolling(null);
    if (!result.ok) {
      Alert.alert("Rollback failed", result.error ?? "unknown");
      return;
    }
    const detail = await morningGetRun(selected.runId);
    if (detail) setSelected(detail);
  };

  return (
    <SafeAreaView style={[styles.safe, { backgroundColor: c.bg }]}>
      <View style={[styles.header, { borderBottomColor: c.border }]}>
        <View style={styles.headerTopRow}>
          <AppBackButton onPress={() => router.back()} />
          <View style={styles.headerSpacer} />
        </View>
        <Text style={[styles.title, { color: c.textPrimary }]}>☀ Morning</Text>
        <Text style={[styles.sub, { color: c.textMuted }]}>
          Match report for overnight autodev runs
        </Text>
      </View>

      <View style={[styles.runList, { borderBottomColor: c.border }]}>
        <FlatList
          horizontal
          data={runs}
          keyExtractor={(r) => r.runId}
          contentContainerStyle={{ paddingHorizontal: 12, paddingVertical: 8 }}
          renderItem={({ item }) => (
            <Pressable
              onPress={() => pickRun(item)}
              style={[
                styles.runPill,
                {
                  borderColor: selected?.runId === item.runId ? c.accent : c.border,
                  backgroundColor: selected?.runId === item.runId ? c.accent + "22" : c.bgCard,
                },
              ]}
            >
              <Text style={[styles.runPillTitle, { color: c.textPrimary }]} numberOfLines={1}>
                {item.project || item.runId}
              </Text>
              <Text style={[styles.runPillMeta, { color: c.textMuted }]}>
                {item.stats.tasksShipped}/{item.stats.tasksTotal} · {formatCost(item.stats.totalCostUsd)}
              </Text>
            </Pressable>
          )}
          ListEmptyComponent={() => (
            <Text style={{ color: c.textMuted, fontSize: 13 }}>No runs yet.</Text>
          )}
        />
      </View>

      {loading ? (
        <View style={styles.center}>
          <ActivityIndicator color={c.accent} />
        </View>
      ) : !selected ? (
        <View style={styles.center}>
          <Text style={[styles.emptyTitle, { color: c.textPrimary }]}>
            {quicClient.isConnected ? "No overnight run to show" : "Connect a device"}
          </Text>
          <Text style={[styles.emptyBody, { color: c.textMuted }]}>
            {quicClient.isConnected
              ? "Run `yaver autodev --morning` on your dev machine before bed to see a match report here in the morning."
              : "Select a dev machine from the Devices tab first."}
          </Text>
        </View>
      ) : (
        <ScrollView
          refreshControl={<RefreshControl
            refreshing={refreshing}
            onRefresh={async () => {
              setRefreshing(true);
              await refresh();
              setRefreshing(false);
            }}
          />}
          contentContainerStyle={{ padding: 16 }}
        >
          <RunHeader run={selected} />
          {selected.tasks.map((task) => (
            <TaskCard
              key={task.taskId}
              runId={selected.runId}
              task={task}
              rolling={rolling === task.taskId}
              onRollback={() => handleRollback(task)}
            />
          ))}
        </ScrollView>
      )}
    </SafeAreaView>
  );
}

function RunHeader({ run }: { run: MorningRunSummary }) {
  const c = useColors();
  return (
    <View style={{ marginBottom: 16 }}>
      <Text style={[styles.runTitle, { color: c.textPrimary }]}>{run.project || run.runId}</Text>
      <Text style={{ color: c.textMuted, fontSize: 12, marginTop: 2 }}>
        {formatStarted(run.startedAt)} · {run.stats.totalMinutes}m · {run.stats.tasksShipped} shipped · {run.stats.tasksFailed} failed · {run.stats.tasksRolledBack} rolled-back · {formatCost(run.stats.totalCostUsd)}
      </Text>
    </View>
  );
}

function TaskCard({
  runId,
  task,
  rolling,
  onRollback,
}: {
  runId: string;
  task: MorningTaskHighlight;
  rolling: boolean;
  onRollback: () => void;
}) {
  const c = useColors();
  const chip = chipColor(task.status);

  return (
    <View style={[styles.card, { backgroundColor: c.bgCard, borderColor: c.border }]}>
      <View style={styles.cardHeader}>
        <View style={[styles.chip, { backgroundColor: chip.bg, borderColor: chip.border }]}>
          <Text style={[styles.chipText, { color: chip.text }]}>{task.status.toUpperCase()}</Text>
        </View>
        {typeof task.costUsd === "number" && (
          <Text style={{ color: c.textMuted, fontSize: 11 }}>${task.costUsd.toFixed(3)}</Text>
        )}
      </View>

      <TaskVideo runId={runId} task={task} />

      <View style={{ padding: 12, gap: 6 }}>
        <Text style={[styles.cardTitle, { color: c.textPrimary }]}>{task.title}</Text>
        {task.oneLineSummary && (
          <Text style={{ color: c.textSecondary, fontSize: 12 }}>{task.oneLineSummary}</Text>
        )}
        <Text style={{ color: c.textMuted, fontSize: 11 }}>
          {(task.filesChanged ?? 0)} files · +{task.linesAdded ?? 0} / -{task.linesRemoved ?? 0}
          {task.headSha ? ` · ${task.headSha.slice(0, 8)}` : ""}
        </Text>
        {task.rolledBackAt && (
          <Text style={{ color: "#f59e0b", fontSize: 11 }}>
            rolled back · {task.revertSha?.slice(0, 8) ?? ""}
          </Text>
        )}
        {task.failureNote && (
          <Text style={{ color: "#ef4444", fontSize: 11 }}>{task.failureNote}</Text>
        )}
      </View>

      <View style={[styles.cardFooter, { borderTopColor: c.border }]}>
        <Pressable
          onPress={onRollback}
          disabled={task.status === "rolled-back" || !(task.commitShas && task.commitShas.length) || rolling}
          style={({ pressed }) => [
            styles.rollbackBtn,
            {
              backgroundColor: "#ef444422",
              borderColor: "#ef444466",
              opacity: pressed ? 0.6 : task.status === "rolled-back" || !task.commitShas?.length || rolling ? 0.4 : 1,
            },
          ]}
        >
          <Text style={styles.rollbackText}>
            {rolling ? "Rolling back…" : "Rollback"}
          </Text>
        </Pressable>
      </View>
    </View>
  );
}

function TaskVideo({ runId, task }: { runId: string; task: MorningTaskHighlight }) {
  const c = useColors();
  const req = task.hasVideo ? morningVideoRequest(runId, task.taskId) : null;
  const player = useVideoPlayer(req ? { uri: req.uri, headers: req.headers } : null, (p) => {
    p.muted = true;
    p.loop = false;
  });
  if (!req) {
    return (
      <View style={[styles.noVideo, { backgroundColor: c.bgCardElevated ?? "#000", borderBottomColor: c.border }]}>
        <Text style={{ color: c.textMuted, fontSize: 12 }}>no video</Text>
      </View>
    );
  }
  return (
    <VideoView
      style={styles.video}
      player={player}
      contentFit="contain"
      allowsFullscreen
      nativeControls
    />
  );
}

function chipColor(status: string) {
  switch (status) {
    case "shipped":
      return { bg: "#22c55e22", border: "#22c55e66", text: "#22c55e" };
    case "failed":
      return { bg: "#ef444422", border: "#ef444466", text: "#ef4444" };
    case "rolled-back":
      return { bg: "#f59e0b22", border: "#f59e0b66", text: "#f59e0b" };
    default:
      return { bg: "#64748b22", border: "#64748b66", text: "#94a3b8" };
  }
}

function formatCost(n?: number): string {
  if (!n || n <= 0) return "$0";
  return `$${n.toFixed(2)}`;
}

function formatStarted(iso?: string): string {
  if (!iso) return "";
  const d = new Date(iso);
  if (isNaN(d.valueOf())) return iso;
  return d.toLocaleString(undefined, { dateStyle: "short", timeStyle: "short" });
}

const styles = StyleSheet.create({
  safe: { flex: 1 },
  header: { paddingHorizontal: 16, paddingVertical: 10, borderBottomWidth: 1 },
  headerTopRow: { flexDirection: "row", alignItems: "center", justifyContent: "space-between", marginBottom: 4 },
  headerSpacer: { width: 40 },
  title: { fontSize: 20, fontWeight: "700" },
  sub: { fontSize: 12, marginTop: 2 },
  runList: { borderBottomWidth: 1 },
  runPill: {
    marginRight: 8,
    paddingHorizontal: 12,
    paddingVertical: 6,
    borderWidth: 1,
    borderRadius: 12,
    minWidth: 140,
  },
  runPillTitle: { fontSize: 13, fontWeight: "600" },
  runPillMeta: { fontSize: 11, marginTop: 2 },
  center: { flex: 1, alignItems: "center", justifyContent: "center", padding: 24 },
  emptyTitle: { fontSize: 17, fontWeight: "700", textAlign: "center" },
  emptyBody: { fontSize: 13, textAlign: "center", marginTop: 8, lineHeight: 19 },
  runTitle: { fontSize: 18, fontWeight: "700" },
  card: {
    borderWidth: 1,
    borderRadius: 14,
    marginBottom: 16,
    overflow: "hidden",
  },
  cardHeader: {
    flexDirection: "row",
    alignItems: "center",
    justifyContent: "space-between",
    paddingHorizontal: 10,
    paddingVertical: 6,
  },
  chip: {
    borderWidth: 1,
    borderRadius: 6,
    paddingHorizontal: 6,
    paddingVertical: 2,
  },
  chipText: { fontSize: 9, fontWeight: "700", letterSpacing: 1 },
  video: { width: "100%", aspectRatio: 16 / 9, backgroundColor: "#000" },
  noVideo: {
    aspectRatio: 16 / 9,
    alignItems: "center",
    justifyContent: "center",
    borderBottomWidth: 1,
  },
  cardTitle: { fontSize: 15, fontWeight: "600" },
  cardFooter: {
    paddingHorizontal: 12,
    paddingVertical: 8,
    borderTopWidth: 1,
    flexDirection: "row",
    justifyContent: "flex-end",
  },
  rollbackBtn: {
    borderWidth: 1,
    paddingHorizontal: 14,
    paddingVertical: 6,
    borderRadius: 8,
  },
  rollbackText: { color: "#ef4444", fontSize: 12, fontWeight: "600" },
});
