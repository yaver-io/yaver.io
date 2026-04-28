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
import { quicClient, type AutoDevLoop, type RunnerInfo } from "../../src/lib/quic";
import { describeConnectionStatus } from "../../src/lib/connection";
import { AutodevChat } from "../../src/components/AutodevChat";
import { AutoIdeasPane } from "../../src/components/AutoIdeasPane";

type LoopRow = AutoDevLoop;
type LoopStatus = LoopRow["status"];
type Section = "live" | "queue" | "setup";

export default function AutoDevScreen() {
  const c = useColors();
  const { connectionStatus } = useDevice();
  const isConnected = connectionStatus === "connected";
  const params = useLocalSearchParams<{ project?: string; path?: string }>();

  const [section, setSection] = useState<Section>("live");
  const [loops, setLoops] = useState<LoopRow[]>([]);
  const [refreshing, setRefreshing] = useState(false);

  // ── Start form state ──────────────────────────────────────────────
  // Everything is pre-filled with sensible defaults so the user can
  // just tap Start. Runners come from GET /agent/runners so the dropdown
  // only lists runners actually installed on the remote machine, and
  // we whitelist to the three first-class agents (claude / codex /
  // opencode) regardless of what else is on PATH.
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
  const [formMorningSummary, setFormMorningSummary] = useState(true);
  const [formMorningVideo, setFormMorningVideo] = useState(true);
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
  const activeLoop = useMemo(
    () =>
      loops.find((l) => l.status === "running") ??
      loops.find((l) => l.status === "needs_human") ??
      loops.find((l) => l.status === "stuck") ??
      loops[0],
    [loops],
  );
  const workingLoopCount = useMemo(
    () => loops.filter((l) => l.status === "running" || l.status === "paused").length,
    [loops],
  );

  const handleStart = useCallback(async () => {
    if (!canStart || starting) return;
    if (!isConnected) {
      Alert.alert(
        "Dev Machine Offline",
        `Yaver ${describeConnectionStatus(connectionStatus)}. Reconnect before starting Auto Dev.`,
      );
      return;
    }
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
        createSummary: formMorningSummary,
        createVideo: formMorningVideo,
      });
      if (!res.ok) {
        const rawErr = res.error || "Could not start auto dev";
        const looksLikeConnection = /unreachable|401|403|network|offline|timeout|ECONN/i.test(rawErr);
        Alert.alert(
          "Auto Dev Didn't Start",
          looksLikeConnection
            ? `${rawErr}\n\nYaver ${describeConnectionStatus(connectionStatus)}.`
            : `${rawErr}\n\nCheck the runner is installed on the dev machine, the project path exists, and the deploy target is supported.`,
        );
      } else {
        Alert.alert("Started", `Loop ${res.loopName} is running in the background.`);
        setShowStart(false);
        setSection("live");
        refreshRef.current?.();
      }
    } catch (e) {
      const err = e instanceof Error ? e.message : String(e);
      Alert.alert(
        "Auto Dev Request Failed",
        `${err}\n\nYaver ${describeConnectionStatus(connectionStatus)}.`,
      );
    } finally {
      setStarting(false);
    }
  }, [canStart, starting, isConnected, connectionStatus, formProject, formWorkDir, formInfinite, formHours, formLoad, formRunner, formPrompt, formDeploy, formNoAutotest, formMorningSummary, formMorningVideo]);

  const refreshRef = React.useRef<(() => void) | undefined>(undefined);

  const refresh = useCallback(async () => {
    if (!isConnected) return;
    setRefreshing(true);
    try {
      const list = await quicClient.autodevLoops();
      setLoops(list);
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
            Your remote machine planning, coding, testing, and shipping in the background.
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
        {(["live", "queue", "setup"] as Section[]).map((s) => (
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
              {s === "live" ? "Live" : s === "queue" ? "Ideas" : "Setup"}
            </Text>
          </Pressable>
        ))}
      </View>

      {section === "live" && (
        <FlatList
          data={loops}
          keyExtractor={(it) => it.id}
          refreshControl={<RefreshControl refreshing={refreshing} onRefresh={refresh} />}
          ListHeaderComponent={
            <View>
              <LiveHero
                loop={activeLoop}
                workingLoopCount={workingLoopCount}
                totalLoops={loops.length}
                onOpenSetup={() => setSection("setup")}
                onOpenIdeas={() => setSection("queue")}
              />
              {activeLoop ? (
                <View style={styles.chatCard}>
                  <Text style={[styles.chatTitle, { color: c.textPrimary }]}>
                    Live session
                  </Text>
                  <Text style={[styles.chatSubtitle, { color: c.textSecondary }]}>
                    Watch {activeLoop.name} as if you were attached to the machine.
                  </Text>
                  <View style={styles.chatFrame}>
                    <AutodevChat streamName={`autodev:${activeLoop.name}`} />
                  </View>
                </View>
              ) : null}
              {loops.length > 0 ? (
                <Text style={[styles.sectionLabel, { color: c.textSecondary }]}>
                  All loops
                </Text>
              ) : null}
            </View>
          }
          ListEmptyComponent={
            <View style={styles.empty}>
              <Text style={[styles.emptyTitle, { color: c.textPrimary }]}>
                No active auto-dev session
              </Text>
              <Text style={[styles.emptyBody, { color: c.textSecondary }]}>
                Start from Setup, or pick generated ideas first and let the machine begin from that queue.
              </Text>
            </View>
          }
          renderItem={({ item }) => (
            <LoopCard
              row={item}
              isActive={item.name === activeLoop?.name}
              onWatch={() => setSection("live")}
              onStop={async () => {
                await quicClient.autodevStop(item.name);
                refresh();
              }}
            />
          )}
        />
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
          return (
            <AutoIdeasPane
              workDir={wd}
              project={proj}
              onStarted={() => {
                setSection("live");
                refresh();
              }}
            />
          );
        })()
      )}

      {section === "setup" && (
        <ScrollView refreshControl={<RefreshControl refreshing={refreshing} onRefresh={refresh} />}>
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
            morningSummary={formMorningSummary}
            setMorningSummary={setFormMorningSummary}
            morningVideo={formMorningVideo}
            setMorningVideo={setFormMorningVideo}
            canStart={canStart}
            starting={starting}
            onStart={handleStart}
          />
          <View style={styles.empty}>
            <Text style={[styles.emptyTitle, { color: c.textPrimary }]}>
              Starts like a paired coding session
            </Text>
            <Text style={[styles.emptyBody, { color: c.textSecondary }]}>
              Pick the repo, runner, and goal. Once started, Live shows the machine thinking,
              editing, testing, and reporting progress instead of raw JSON.
            </Text>
          </View>
        </ScrollView>
      )}
    </SafeAreaView>
  );
}

function LiveHero(props: {
  loop?: LoopRow;
  workingLoopCount: number;
  totalLoops: number;
  onOpenSetup: () => void;
  onOpenIdeas: () => void;
}) {
  const c = useColors();
  const statusTone =
    props.loop?.status === "running"
      ? "#22c55e"
      : props.loop?.status === "needs_human"
        ? "#ef4444"
        : "#eab308";
  return (
    <View style={[styles.heroCard, { borderColor: c.border, backgroundColor: c.bgCard }]}>
      <Text style={[styles.heroEyebrow, { color: c.textSecondary }]}>
        MACHINE STATUS
      </Text>
      <Text style={[styles.heroTitle, { color: c.textPrimary }]}>
        {props.loop ? `${props.loop.name} is ${props.loop.status.replace("_", " ")}` : "Ready to start"}
      </Text>
      <Text style={[styles.heroBody, { color: c.textSecondary }]}>
        {props.loop?.lastSummary ||
          "Start a loop or promote generated ideas into implementation. The live transcript will appear here."}
      </Text>
      <View style={styles.heroStats}>
        <View style={[styles.heroStat, { borderColor: c.border }]}>
          <Text style={[styles.heroStatLabel, { color: c.textMuted }]}>Working</Text>
          <Text style={[styles.heroStatValue, { color: statusTone }]}>{props.workingLoopCount}</Text>
        </View>
        <View style={[styles.heroStat, { borderColor: c.border }]}>
          <Text style={[styles.heroStatLabel, { color: c.textMuted }]}>Total loops</Text>
          <Text style={[styles.heroStatValue, { color: c.textPrimary }]}>{props.totalLoops}</Text>
        </View>
        <View style={[styles.heroStat, { borderColor: c.border }]}>
          <Text style={[styles.heroStatLabel, { color: c.textMuted }]}>Runner</Text>
          <Text style={[styles.heroStatValue, { color: c.textPrimary }]}>
            {props.loop?.runner || "auto"}
          </Text>
        </View>
      </View>
      <View style={styles.heroActions}>
        <Pressable
          onPress={props.onOpenIdeas}
          style={[styles.secondaryBtn, { borderColor: c.border, backgroundColor: c.bgCardElevated }]}
        >
          <Text style={[styles.secondaryBtnText, { color: c.textPrimary }]}>Open ideas</Text>
        </Pressable>
        <Pressable
          onPress={props.onOpenSetup}
          style={[styles.secondaryBtn, { borderColor: c.border, backgroundColor: c.bgCardElevated }]}
        >
          <Text style={[styles.secondaryBtnText, { color: c.textPrimary }]}>New loop</Text>
        </Pressable>
      </View>
    </View>
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
  morningSummary: boolean;
  setMorningSummary: (v: boolean) => void;
  morningVideo: boolean;
  setMorningVideo: (v: boolean) => void;
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
            ) : (() => {
              // Yaver's three first-class runners. Same allowlist
              // used by tasks.tsx and phone-projects.tsx.
              const RUNNER_WL = new Set(["claude", "claude-code", "codex", "opencode"]);
              const allowed = props.runners.filter((r) => RUNNER_WL.has((r.id || "").toLowerCase()));
              return allowed.length === 0 ? (
                <Text style={{ color: c.textMuted, fontSize: 12 }}>
                  No runners installed on the remote agent. Install one (claude, codex, opencode) and refresh.
                </Text>
              ) : (
              <View style={{ flexDirection: "row", flexWrap: "wrap", gap: 6 }}>
                {allowed.map((r) => {
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
              );
            })()}
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

          <View style={{ flexDirection: "row", alignItems: "center", gap: 8 }}>
            <Switch value={props.morningSummary} onValueChange={props.setMorningSummary} />
            <Text style={{ color: c.textSecondary, fontSize: 12, flex: 1 }}>
              ☀ Morning summary — per-kick match report
            </Text>
          </View>

          <View style={{ flexDirection: "row", alignItems: "center", gap: 8 }}>
            <Switch
              value={props.morningVideo}
              onValueChange={props.setMorningVideo}
              disabled={!props.morningSummary}
            />
            <Text
              style={{
                color: props.morningSummary ? c.textSecondary : c.textMuted,
                fontSize: 12,
                flex: 1,
              }}
            >
              Video of the finished product (iOS Simulator or Android emulator — skipped if neither is running)
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

function LoopCard({
  row,
  isActive,
  onWatch,
  onStop,
}: {
  row: LoopRow;
  isActive: boolean;
  onWatch: () => void;
  onStop: () => void;
}) {
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
    <View
      style={[
        styles.card,
        { borderColor: isActive ? c.tabActive : c.border, backgroundColor: isActive ? c.bgCard : "transparent" },
      ]}
    >
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

      <View style={styles.cardActions}>
        <Pressable
          onPress={onWatch}
          style={[styles.smallBtn, { borderColor: c.border, backgroundColor: c.bgCardElevated }]}
        >
          <Text style={[styles.smallBtnText, { color: c.textPrimary }]}>Watch live</Text>
        </Pressable>
        <Pressable
          onPress={onStop}
          style={[styles.smallBtn, { borderColor: "#ef4444", backgroundColor: "#ef444411" }]}
        >
          <Text style={[styles.smallBtnText, { color: "#ef4444" }]}>Stop</Text>
        </Pressable>
      </View>
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
  heroCard: {
    marginHorizontal: 16,
    marginTop: 14,
    padding: 16,
    borderRadius: 14,
    borderWidth: 1,
    gap: 10,
  },
  heroEyebrow: {
    fontSize: 11,
    fontWeight: "700",
    letterSpacing: 0.8,
  },
  heroTitle: { fontSize: 20, fontWeight: "800" },
  heroBody: { fontSize: 13, lineHeight: 19 },
  heroStats: {
    flexDirection: "row",
    gap: 8,
    flexWrap: "wrap",
  },
  heroStat: {
    minWidth: 96,
    borderWidth: 1,
    borderRadius: 12,
    paddingHorizontal: 12,
    paddingVertical: 10,
  },
  heroStatLabel: {
    fontSize: 10,
    textTransform: "uppercase",
    marginBottom: 4,
  },
  heroStatValue: {
    fontSize: 16,
    fontWeight: "800",
  },
  heroActions: {
    flexDirection: "row",
    gap: 8,
    flexWrap: "wrap",
  },
  secondaryBtn: {
    paddingHorizontal: 12,
    paddingVertical: 10,
    borderRadius: 10,
    borderWidth: 1,
  },
  secondaryBtnText: {
    fontSize: 13,
    fontWeight: "700",
  },
  chatCard: {
    marginHorizontal: 16,
    marginTop: 12,
    gap: 8,
  },
  chatTitle: { fontSize: 16, fontWeight: "700" },
  chatSubtitle: { fontSize: 12 },
  chatFrame: {
    height: 320,
    overflow: "hidden",
    borderRadius: 14,
  },
  sectionLabel: {
    marginHorizontal: 16,
    marginTop: 16,
    marginBottom: 4,
    fontSize: 11,
    fontWeight: "700",
    letterSpacing: 0.7,
    textTransform: "uppercase",
  },
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
  cardActions: {
    flexDirection: "row",
    gap: 8,
    marginTop: 10,
  },
  smallBtn: {
    paddingHorizontal: 10,
    paddingVertical: 8,
    borderRadius: 9,
    borderWidth: 1,
  },
  smallBtnText: {
    fontSize: 12,
    fontWeight: "700",
  },
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
