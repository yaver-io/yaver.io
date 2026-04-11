// Auto Dev tab — M8 scaffolding (autonomous test → fix → deploy loops).
//
// See docs/roadmap_ci_solo_developer_lower_costs.md, section "Autonomous
// loops: the agent as a second developer". This screen is read-only UI
// for now; wiring to the agent's `yaver loop ...` subcommands goes over
// the existing quicClient transport once the Go side exposes loop HTTP
// endpoints. The layout matches the three-section shape from the doc:
//
//   1. Active loops — one row per registered loop, with status + stop
//   2. Prompt library — CRUD for dev-authored feature prompts
//   3. Ideas queue — multi-select from agent-proposed feature ideas
//
// Kill-switch is always reachable as a sticky header button, matching
// the "stop from anywhere" rule in M8's safety rails.

import React, { useCallback, useEffect, useState } from "react";
import {
  FlatList,
  Pressable,
  RefreshControl,
  ScrollView,
  StyleSheet,
  Text,
  View,
} from "react-native";
import { SafeAreaView } from "react-native-safe-area-context";
import { useColors } from "../../src/context/ThemeContext";
import { useDevice } from "../../src/context/DeviceContext";
import {
  quicClient,
  type AutoDevLoop,
  type AutoDevIdeasPayload,
} from "../../src/lib/quic";

type LoopRow = AutoDevLoop;
type LoopStatus = LoopRow["status"];

type PromptRow = {
  id: string;
  name: string;
  mode: LoopRow["mode"];
  bodyPreview: string;
  active: boolean;
};

type IdeaRow = {
  id: string;
  title: string;
  description: string;
  prompt: string;
  effort?: "small" | "medium" | "large";
  radicalness?: number;
};

type Section = "loops" | "prompts" | "ideas";

export default function AutoDevScreen() {
  const c = useColors();
  const { connectionStatus } = useDevice();
  const isConnected = connectionStatus === "connected";

  const [section, setSection] = useState<Section>("loops");
  const [loops, setLoops] = useState<LoopRow[]>([]);
  const [prompts, setPrompts] = useState<PromptRow[]>([]);
  const [ideas, setIdeas] = useState<IdeaRow[]>([]);
  const [refreshing, setRefreshing] = useState(false);

  const refresh = useCallback(async () => {
    if (!isConnected) return;
    setRefreshing(true);
    try {
      const list = await quicClient.autodevLoops();
      setLoops(list);

      // Prompt library mirrors the inline prompts devs have stashed
      // on each loop. When a loop has an active PromptInline we show
      // it as an "active" row; if not, we drop a placeholder so the
      // dev can tell the loop exists but isn't pinned to a prompt.
      setPrompts(
        list.map((l) => ({
          id: l.id,
          name: l.name,
          mode: l.mode,
          bodyPreview:
            l.promptInline?.slice(0, 120) ?? "(no inline prompt set)",
          active: !!l.promptInline,
        })),
      );

      // Ideas are per-loop — pull the first ideas-mode loop we see.
      const ideasLoop = list.find((l) => l.mode === "ideas");
      if (ideasLoop) {
        const payload = await quicClient.autodevIdeas(ideasLoop.name);
        if (payload && payload.ideas) {
          setIdeas(
            payload.ideas.map((it) => ({
              id: it.id,
              title: it.title,
              description: it.description ?? "",
              prompt: it.prompt,
              effort: it.effort,
              radicalness: it.radicalness,
            })),
          );
        } else {
          setIdeas([]);
        }
      } else {
        setIdeas([]);
      }
    } finally {
      setRefreshing(false);
    }
  }, [isConnected]);

  useEffect(() => {
    refresh();
  }, [refresh]);

  const stopAll = useCallback(async () => {
    await Promise.all(loops.map((l) => quicClient.autodevStop(l.name)));
    refresh();
  }, [loops, refresh]);

  return (
    <SafeAreaView style={[styles.root, { backgroundColor: c.bg }]} edges={["top"]}>
      <View style={[styles.stickyHeader, { borderBottomColor: c.border }]}>
        <View style={{ flex: 1 }}>
          <Text style={[styles.title, { color: c.textPrimary }]}>Auto Dev</Text>
          <Text style={[styles.subtitle, { color: c.textSecondary }]}>
            Autonomous test → fix → deploy loops (M8)
          </Text>
        </View>
        <Pressable
          accessibilityLabel="Stop all auto-dev loops"
          onPress={stopAll}
          style={[styles.stopAll, { backgroundColor: "#ef4444" }]}
        >
          <Text style={styles.stopAllText}>Stop All</Text>
        </Pressable>
      </View>

      <View style={[styles.tabs, { borderBottomColor: c.border }]}>
        {(["loops", "prompts", "ideas"] as Section[]).map((s) => (
          <Pressable key={s} onPress={() => setSection(s)} style={styles.tabBtn}>
            <Text
              style={[
                styles.tabText,
                {
                  color: section === s ? c.textPrimary : c.textSecondary,
                  borderBottomColor: section === s ? c.tabActive : "transparent",
                },
              ]}
            >
              {s === "loops" ? "Loops" : s === "prompts" ? "Prompts" : "Ideas"}
            </Text>
          </Pressable>
        ))}
      </View>

      {section === "loops" && (
        <FlatList
          data={loops}
          keyExtractor={(it) => it.id}
          refreshControl={<RefreshControl refreshing={refreshing} onRefresh={refresh} />}
          ListEmptyComponent={
            <View style={styles.empty}>
              <Text style={[styles.emptyTitle, { color: c.textPrimary }]}>
                No loops registered
              </Text>
              <Text style={[styles.emptyBody, { color: c.textSecondary }]}>
                Register one from the Mac mini:{"\n\n"}
                <Text style={{ fontFamily: "Courier" }}>
                  yaver loop add ./sfmg-autofix.loop.yaml
                </Text>
                {"\n\n"}
                Then pull-to-refresh here.
              </Text>
            </View>
          }
          renderItem={({ item }) => <LoopCard row={item} />}
        />
      )}

      {section === "prompts" && (
        <ScrollView
          refreshControl={<RefreshControl refreshing={refreshing} onRefresh={refresh} />}
        >
          <View style={styles.empty}>
            <Text style={[styles.emptyTitle, { color: c.textPrimary }]}>
              Prompt library is empty
            </Text>
            <Text style={[styles.emptyBody, { color: c.textSecondary }]}>
              Prompts live under <Text style={{ fontFamily: "Courier" }}>.yaver/prompts/</Text> in
              each project. The mobile CRUD editor wires up once the Go side exposes the
              autodev HTTP endpoints.
            </Text>
          </View>
        </ScrollView>
      )}

      {section === "ideas" && (
        <ScrollView
          refreshControl={<RefreshControl refreshing={refreshing} onRefresh={refresh} />}
        >
          <View style={styles.empty}>
            <Text style={[styles.emptyTitle, { color: c.textPrimary }]}>No ideas yet</Text>
            <Text style={[styles.emptyBody, { color: c.textSecondary }]}>
              The Ideas loop runs daily at noon by default. Once it publishes its first list
              you can tick the items you want and tap <Text style={{ fontWeight: "700" }}>Kick</Text>.
              Each selection becomes a develop-mode loop queued for the next active window.
            </Text>
          </View>
        </ScrollView>
      )}
    </SafeAreaView>
  );
}

function LoopCard({ row }: { row: LoopRow }) {
  const c = useColors();
  const statusColor: Record<LoopStatus, string> = {
    idle: c.textSecondary,
    running: "#22c55e",
    paused: "#eab308",
    stopped: c.textSecondary,
    stuck: "#eab308",
    budget_hit: "#eab308",
    needs_human: "#ef4444",
  };
  return (
    <View style={[styles.card, { borderColor: c.border }]}>
      <View style={styles.cardHeader}>
        <Text style={[styles.cardName, { color: c.textPrimary }]}>{row.name}</Text>
        <Text style={[styles.cardStatus, { color: statusColor[row.status] }]}>
          {row.status}
        </Text>
      </View>
      <Text style={[styles.cardMeta, { color: c.textSecondary }]}>
        {row.mode} · branch={row.branch} · iter {row.iterationCount}
        {row.radicalnessUi != null ? ` · rad ui:${row.radicalnessUi}` : ""}
        {row.tone ? ` · ${row.tone}` : ""}
      </Text>
      {row.lastSummary ? (
        <Text style={[styles.cardSummary, { color: c.textSecondary }]} numberOfLines={2}>
          {row.lastSummary}
        </Text>
      ) : null}
    </View>
  );
}

const styles = StyleSheet.create({
  root: { flex: 1 },
  stickyHeader: {
    flexDirection: "row",
    alignItems: "center",
    paddingHorizontal: 16,
    paddingVertical: 12,
    borderBottomWidth: 1,
  },
  title: { fontSize: 20, fontWeight: "700" },
  subtitle: { fontSize: 12, marginTop: 2 },
  stopAll: {
    paddingHorizontal: 14,
    paddingVertical: 8,
    borderRadius: 8,
  },
  stopAllText: { color: "#ffffff", fontWeight: "700", fontSize: 13 },
  tabs: {
    flexDirection: "row",
    borderBottomWidth: 1,
  },
  tabBtn: { paddingHorizontal: 16, paddingVertical: 12 },
  tabText: {
    fontSize: 14,
    fontWeight: "600",
    paddingBottom: 8,
    borderBottomWidth: 2,
  },
  empty: { padding: 24 },
  emptyTitle: { fontSize: 16, fontWeight: "700", marginBottom: 8 },
  emptyBody: { fontSize: 13, lineHeight: 20 },
  card: {
    marginHorizontal: 16,
    marginTop: 12,
    padding: 14,
    borderRadius: 10,
    borderWidth: 1,
  },
  cardHeader: { flexDirection: "row", justifyContent: "space-between", alignItems: "center" },
  cardName: { fontSize: 15, fontWeight: "700" },
  cardStatus: { fontSize: 12, fontWeight: "700", textTransform: "uppercase" },
  cardMeta: { fontSize: 12, marginTop: 6 },
  cardSummary: { fontSize: 12, marginTop: 6 },
});
