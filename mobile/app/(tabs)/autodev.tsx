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

import React, { useCallback, useEffect, useMemo, useState } from "react";
import {
  Alert,
  FlatList,
  Pressable,
  RefreshControl,
  ScrollView,
  StyleSheet,
  Switch,
  Text,
  TextInput,
  View,
} from "react-native";
import { SafeAreaView } from "react-native-safe-area-context";
import { useLocalSearchParams } from "expo-router";
import { useColors } from "../../src/context/ThemeContext";
import { useDevice } from "../../src/context/DeviceContext";
import {
  quicClient,
  type AutoDevLoop,
  type AutoDevIdeasPayload,
  type RunnerInfo,
} from "../../src/lib/quic";
import { AutodevChat } from "../../src/components/AutodevChat";
import { AutoIdeasPane } from "../../src/components/AutoIdeasPane";

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

type Section = "loops" | "prompts" | "ideas" | "chat" | "queue";

export default function AutoDevScreen() {
  const c = useColors();
  const { connectionStatus } = useDevice();
  const isConnected = connectionStatus === "connected";
  const params = useLocalSearchParams<{ project?: string; path?: string }>();

  const [section, setSection] = useState<Section>("loops");
  const [loops, setLoops] = useState<LoopRow[]>([]);
  const [prompts, setPrompts] = useState<PromptRow[]>([]);
  const [ideas, setIdeas] = useState<IdeaRow[]>([]);
  const [refreshing, setRefreshing] = useState(false);

  // ── Start form state ──────────────────────────────────────────────
  // Everything is pre-filled with sensible defaults so the user can
  // just tap Start. Runners come from GET /agent/runners so the dropdown
  // only lists the runners actually installed on the remote machine —
  // we never show aider to the user if aider isn't there.
  const [showStart, setShowStart] = useState(!!params.path);
  const [runners, setRunners] = useState<RunnerInfo[]>([]);
  const [runnersLoading, setRunnersLoading] = useState(false);
  const [formProject, setFormProject] = useState(params.project ?? "");
  const [formWorkDir, setFormWorkDir] = useState(params.path ?? "");
  const [formHours, setFormHours] = useState("8");
  const [formInfinite, setFormInfinite] = useState(false);
  const [formLoad, setFormLoad] = useState<"lite" | "high">("lite");
  const [formRunner, setFormRunner] = useState<string>("");
  const [formPrompt, setFormPrompt] = useState("");
  const [formDeploy, setFormDeploy] = useState<"auto" | "none" | "testflight" | "playstore" | "both">("auto");
  const [formNoAutotest, setFormNoAutotest] = useState(false);
  const [starting, setStarting] = useState(false);

  // Load available runners from the remote agent once connected. The
  // Go side reports which runners are installed via /agent/runners —
  // we filter out the uninstalled ones so the dropdown only shows
  // things the user can actually pick.
  useEffect(() => {
    if (!isConnected) return;
    let mounted = true;
    setRunnersLoading(true);
    quicClient
      .getRunners()
      .then((list) => {
        if (!mounted) return;
        const installed = list.filter((r) => r.installed);
        setRunners(installed);
        // Default runner: agent-reported default, else first installed.
        const pref = installed.find((r) => r.isDefault) ?? installed[0];
        if (pref && !formRunner) setFormRunner(pref.id);
      })
      .finally(() => mounted && setRunnersLoading(false));
    return () => { mounted = false; };
  }, [isConnected]);

  const canStart = useMemo(() => {
    if (!formWorkDir.trim()) return false;
    if (!formInfinite && !/^\d+$/.test(formHours)) return false;
    return true;
  }, [formWorkDir, formInfinite, formHours]);

  const handleStart = useCallback(async () => {
    if (!canStart || starting) return;
    setStarting(true);
    try {
      const res = await quicClient.autodevStart({
        project: formProject || undefined,
        workDir: formWorkDir,
        hours: formInfinite ? "infinite" : formHours,
        load: formLoad,
        runner: formRunner || undefined,
        prompt: formPrompt || undefined,
        deploy: formDeploy,
        noAutotest: formNoAutotest,
      });
      if (!res.ok) {
        Alert.alert("Start failed", res.error || "Could not start auto dev");
      } else {
        Alert.alert("Started", `Loop ${res.loopName} is running in the background.`);
        setShowStart(false);
        refreshRef.current?.();
      }
    } finally {
      setStarting(false);
    }
  }, [canStart, starting, formProject, formWorkDir, formInfinite, formHours, formLoad, formRunner, formPrompt, formDeploy, formNoAutotest]);

  const refreshRef = React.useRef<(() => void) | undefined>(undefined);

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
    refreshRef.current = refresh;
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
        {(["loops", "chat", "queue", "prompts", "ideas"] as Section[]).map((s) => (
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
              {s === "loops"
                ? "Loops"
                : s === "chat"
                ? "Chat"
                : s === "queue"
                ? "Ideas Queue"
                : s === "prompts"
                ? "Prompts"
                : "Ideas"}
            </Text>
          </Pressable>
        ))}
      </View>

      {section === "loops" && (
        <FlatList
          data={loops}
          keyExtractor={(it) => it.id}
          refreshControl={<RefreshControl refreshing={refreshing} onRefresh={refresh} />}
          ListHeaderComponent={
            <StartForm
              c={c}
              open={showStart}
              onToggle={() => setShowStart((v) => !v)}
              project={formProject}
              setProject={setFormProject}
              workDir={formWorkDir}
              setWorkDir={setFormWorkDir}
              hours={formHours}
              setHours={setFormHours}
              infinite={formInfinite}
              setInfinite={setFormInfinite}
              load={formLoad}
              setLoad={setFormLoad}
              runner={formRunner}
              setRunner={setFormRunner}
              runners={runners}
              runnersLoading={runnersLoading}
              prompt={formPrompt}
              setPrompt={setFormPrompt}
              deploy={formDeploy}
              setDeploy={setFormDeploy}
              noAutotest={formNoAutotest}
              setNoAutotest={setFormNoAutotest}
              canStart={canStart}
              starting={starting}
              onStart={handleStart}
            />
          }
          ListEmptyComponent={
            <View style={styles.empty}>
              <Text style={[styles.emptyTitle, { color: c.textPrimary }]}>
                No loops registered
              </Text>
              <Text style={[styles.emptyBody, { color: c.textSecondary }]}>
                Tap <Text style={{ fontWeight: "700" }}>Start a new loop</Text> above, or register one from the Mac mini:{"\n\n"}
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

      {section === "chat" && (
        (() => {
          // Pick the first running loop (or the most-recent if none).
          const target =
            loops.find((l) => l.status === "running") ?? loops[0];
          if (!target) {
            return (
              <View style={styles.empty}>
                <Text style={[styles.emptyTitle, { color: c.textPrimary }]}>
                  No autodev loops yet.
                </Text>
                <Text style={[styles.emptyBody, { color: c.textSecondary }]}>
                  Start one from the Loops tab — chat will stream here.
                </Text>
              </View>
            );
          }
          return <AutodevChat streamName={`autodev:${target.name}`} />;
        })()
      )}

      {section === "queue" && (
        (() => {
          // workDir resolution: prefer the param the user came in
          // with from the Apps tile; fall back to the form's value.
          const wd = (params.path as string) || formWorkDir;
          const proj =
            (params.project as string) ||
            formProject ||
            (wd ? wd.split("/").filter(Boolean).pop() ?? "" : "");
          if (!wd) {
            return (
              <View style={styles.empty}>
                <Text style={[styles.emptyTitle, { color: c.textPrimary }]}>
                  No project selected.
                </Text>
                <Text style={[styles.emptyBody, { color: c.textSecondary }]}>
                  Open this tab from the Apps tile (so the work_dir is set),
                  or fill in the work dir on the Loops tab's start form.
                </Text>
              </View>
            );
          }
          return <AutoIdeasPane workDir={wd} project={proj} />;
        })()
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

function StartForm(props: {
  c: ReturnType<typeof useColors>;
  open: boolean;
  onToggle: () => void;
  project: string;
  setProject: (v: string) => void;
  workDir: string;
  setWorkDir: (v: string) => void;
  hours: string;
  setHours: (v: string) => void;
  infinite: boolean;
  setInfinite: (v: boolean) => void;
  load: "lite" | "high";
  setLoad: (v: "lite" | "high") => void;
  runner: string;
  setRunner: (v: string) => void;
  runners: RunnerInfo[];
  runnersLoading: boolean;
  prompt: string;
  setPrompt: (v: string) => void;
  deploy: "auto" | "none" | "testflight" | "playstore" | "both";
  setDeploy: (v: "auto" | "none" | "testflight" | "playstore" | "both") => void;
  noAutotest: boolean;
  setNoAutotest: (v: boolean) => void;
  canStart: boolean;
  starting: boolean;
  onStart: () => void;
}) {
  const { c } = props;
  return (
    <View style={[styles.formCard, { borderColor: c.border, backgroundColor: c.bgCard }]}>
      <Pressable onPress={props.onToggle} style={styles.formHeader}>
        <Text style={[styles.formTitle, { color: c.textPrimary }]}>
          {props.open ? "\u25BC" : "\u25B6"} Start a new loop
        </Text>
        <Text style={[styles.formSubtitle, { color: c.textSecondary }]}>
          {props.open ? "Pick parameters, tap Start" : "Tap to expand"}
        </Text>
      </Pressable>
      {props.open && (
        <View style={{ gap: 10, paddingTop: 6 }}>
          <FormField label="Project name" c={c}>
            <TextInput
              style={[styles.input, { color: c.textPrimary, borderColor: c.border }]}
              value={props.project}
              onChangeText={props.setProject}
              placeholder="sfmg"
              placeholderTextColor={c.textMuted}
              autoCapitalize="none"
            />
          </FormField>

          <FormField label="Work dir (absolute path)" c={c}>
            <TextInput
              style={[styles.input, { color: c.textPrimary, borderColor: c.border }]}
              value={props.workDir}
              onChangeText={props.setWorkDir}
              placeholder="/Users/me/Workspace/sfmg"
              placeholderTextColor={c.textMuted}
              autoCapitalize="none"
            />
          </FormField>

          <FormField label="Time limit" c={c}>
            <View style={{ flexDirection: "row", alignItems: "center", gap: 10 }}>
              <TextInput
                style={[styles.input, { color: c.textPrimary, borderColor: c.border, flex: 1, opacity: props.infinite ? 0.4 : 1 }]}
                value={props.infinite ? "" : props.hours}
                editable={!props.infinite}
                onChangeText={props.setHours}
                keyboardType="numeric"
                placeholder="8"
                placeholderTextColor={c.textMuted}
              />
              <Text style={{ color: c.textSecondary, fontSize: 12 }}>hours</Text>
              <View style={{ flexDirection: "row", alignItems: "center", gap: 6 }}>
                <Text style={{ color: c.textSecondary, fontSize: 12 }}>Infinite</Text>
                <Switch value={props.infinite} onValueChange={props.setInfinite} />
              </View>
            </View>
          </FormField>

          <FormField label="Load preset" c={c}>
            <Segmented
              c={c}
              value={props.load}
              options={[
                { v: "lite", label: "Lite" },
                { v: "high", label: "Heavy" },
              ]}
              onChange={(v) => props.setLoad(v as "lite" | "high")}
            />
          </FormField>

          <FormField label="Runner (from this machine)" c={c}>
            {props.runnersLoading ? (
              <Text style={{ color: c.textMuted, fontSize: 12 }}>Loading installed runners…</Text>
            ) : props.runners.length === 0 ? (
              <Text style={{ color: c.textMuted, fontSize: 12 }}>
                No runners installed on the remote agent. Install one (claude, codex, aider, ollama, …) and refresh.
              </Text>
            ) : (
              <View style={{ flexDirection: "row", flexWrap: "wrap", gap: 6 }}>
                {props.runners.map((r) => {
                  const active = props.runner === r.id;
                  return (
                    <Pressable
                      key={r.id}
                      onPress={() => props.setRunner(r.id)}
                      style={[
                        styles.chip,
                        { borderColor: active ? c.tabActive : c.border, backgroundColor: active ? c.tabActive + "22" : "transparent" },
                      ]}
                    >
                      <Text style={[styles.chipText, { color: active ? c.tabActive : c.textSecondary }]}>
                        {r.name}
                      </Text>
                    </Pressable>
                  );
                })}
              </View>
            )}
          </FormField>

          <FormField label="Deploy" c={c}>
            <Segmented
              c={c}
              value={props.deploy}
              options={[
                { v: "auto", label: "Auto" },
                { v: "none", label: "None" },
                { v: "testflight", label: "iOS" },
                { v: "playstore", label: "Android" },
                { v: "both", label: "Both" },
              ]}
              onChange={(v) => props.setDeploy(v as any)}
            />
          </FormField>

          <FormField label="Prompt (optional)" c={c}>
            <TextInput
              style={[styles.input, styles.inputMulti, { color: c.textPrimary, borderColor: c.border }]}
              value={props.prompt}
              onChangeText={props.setPrompt}
              multiline
              numberOfLines={3}
              placeholder="Focus on purchase flow bugs"
              placeholderTextColor={c.textMuted}
            />
          </FormField>

          <View style={{ flexDirection: "row", alignItems: "center", gap: 8 }}>
            <Switch value={!props.noAutotest} onValueChange={(v) => props.setNoAutotest(!v)} />
            <Text style={{ color: c.textSecondary, fontSize: 12 }}>
              Interleave auto-test regression
            </Text>
          </View>

          <Pressable
            onPress={props.onStart}
            disabled={!props.canStart || props.starting}
            style={[
              styles.startBtn,
              { backgroundColor: props.canStart ? "#22c55e" : c.border, opacity: props.starting ? 0.6 : 1 },
            ]}
          >
            <Text style={styles.startBtnText}>
              {props.starting ? "Starting…" : "Start loop"}
            </Text>
          </Pressable>
        </View>
      )}
    </View>
  );
}

function FormField({ label, c, children }: { label: string; c: ReturnType<typeof useColors>; children: React.ReactNode }) {
  return (
    <View style={{ gap: 4 }}>
      <Text style={{ color: c.textSecondary, fontSize: 11, textTransform: "uppercase", letterSpacing: 0.5, fontWeight: "600" }}>
        {label}
      </Text>
      {children}
    </View>
  );
}

function Segmented<T extends string>(props: {
  c: ReturnType<typeof useColors>;
  value: T;
  options: { v: T; label: string }[];
  onChange: (v: T) => void;
}) {
  const { c } = props;
  return (
    <View style={{ flexDirection: "row", flexWrap: "wrap", gap: 6 }}>
      {props.options.map((o) => {
        const active = props.value === o.v;
        return (
          <Pressable
            key={o.v}
            onPress={() => props.onChange(o.v)}
            style={[
              styles.chip,
              { borderColor: active ? c.tabActive : c.border, backgroundColor: active ? c.tabActive + "22" : "transparent" },
            ]}
          >
            <Text style={[styles.chipText, { color: active ? c.tabActive : c.textSecondary }]}>
              {o.label}
            </Text>
          </Pressable>
        );
      })}
    </View>
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
        {row.runner ? ` · ${row.runner}` : ""}
        {row.radicalnessUi != null ? ` · rad ui:${row.radicalnessUi}` : ""}
        {row.tone ? ` · ${row.tone}` : ""}
      </Text>

      {/* Auto Test: show the watched spec directory. */}
      {row.mode === "auto-test" && row.testRoot ? (
        <Text style={[styles.cardMeta, { color: c.textSecondary, marginTop: 2 }]}>
          🧪 {row.testRoot}/
        </Text>
      ) : null}

      {row.lastSummary ? (
        <Text style={[styles.cardSummary, { color: c.textSecondary }]} numberOfLines={2}>
          {row.lastSummary}
        </Text>
      ) : null}

      {/* Release train badge — "2/3 green · armed" or "paused". */}
      {row.releaseTrain && row.releaseTrain.enabled ? (
        <Text
          style={[
            styles.cardMeta,
            {
              marginTop: 6,
              color: row.releaseTrain.paused
                ? "#eab308"
                : row.releaseTrain.greenRunSinceLastDeploy >= row.releaseTrain.n
                  ? "#22c55e"
                  : c.textSecondary,
              fontWeight: "600",
            },
          ]}
        >
          🚂 {row.releaseTrain.paused ? "paused" : "armed"} · {row.releaseTrain.greenRunSinceLastDeploy}/
          {row.releaseTrain.n} green
          {row.releaseTrain.target ? ` → ${row.releaseTrain.target}` : ""}
          {row.releaseTrain.maxTestFlightPerDay
            ? ` · ${row.testflightToday}/${row.releaseTrain.maxTestFlightPerDay} today`
            : ""}
        </Text>
      ) : null}

      {/* Session-limits: one line per tracked runner window. */}
      {row.sessionUsage && row.sessionUsage.length > 0
        ? row.sessionUsage.map((u) => (
            <Text
              key={u.runner}
              style={[
                styles.cardMeta,
                {
                  marginTop: 2,
                  color: u.overCap ? "#ef4444" : c.textSecondary,
                },
              ]}
            >
              ⏱  {u.runner}: {formatDuration(u.usedSeconds)}
              {u.capSeconds > 0 ? ` / ${formatDuration(u.capSeconds)}` : ""}
              {u.sessionWindow ? ` (${u.sessionWindow} window)` : " (unlimited)"}
              {u.overCap ? " · OVER CAP" : ""}
            </Text>
          ))
        : null}
    </View>
  );
}

// formatDuration converts seconds to a short human string
// — 30s / 2m / 1h 20m. Matches the vibe of the commit budgets
// line ("commits=3/5") rather than a raw second count.
function formatDuration(seconds: number): string {
  if (seconds < 60) return `${seconds}s`;
  if (seconds < 3600) return `${Math.floor(seconds / 60)}m`;
  const h = Math.floor(seconds / 3600);
  const m = Math.floor((seconds % 3600) / 60);
  return m > 0 ? `${h}h ${m}m` : `${h}h`;
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
  formCard: {
    marginHorizontal: 16,
    marginTop: 14,
    marginBottom: 4,
    padding: 14,
    borderRadius: 10,
    borderWidth: 1,
    gap: 6,
  },
  formHeader: { paddingVertical: 4 },
  formTitle: { fontSize: 15, fontWeight: "700" },
  formSubtitle: { fontSize: 11, marginTop: 2 },
  input: {
    borderWidth: 1,
    borderRadius: 8,
    paddingHorizontal: 10,
    paddingVertical: 8,
    fontSize: 13,
  },
  inputMulti: {
    minHeight: 60,
    textAlignVertical: "top",
  },
  chip: {
    borderWidth: 1,
    borderRadius: 16,
    paddingHorizontal: 12,
    paddingVertical: 6,
  },
  chipText: { fontSize: 12, fontWeight: "600" },
  startBtn: {
    marginTop: 4,
    paddingVertical: 12,
    borderRadius: 10,
    alignItems: "center",
  },
  startBtnText: { color: "#ffffff", fontWeight: "700", fontSize: 14 },
});
