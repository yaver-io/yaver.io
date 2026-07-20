import React, { useCallback, useEffect, useState } from "react";
import {
  ActivityIndicator,
  Alert,
  FlatList,
  KeyboardAvoidingView,
  Platform,
  Pressable,
  RefreshControl,
  ScrollView,
  StyleSheet,
  Text,
  TextInput,
  View,
} from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { useRouter } from "expo-router";
import { useColors } from "../src/context/ThemeContext";
import { useDevice } from "../src/context/DeviceContext";
import { quicClient, type ScheduledTask, type RunnerInfo } from "../src/lib/quic";
import { AppBackButton } from "../src/components/AppBackButton";

// Mobile UI over desktop/agent/scheduler.go. Three scheduling modes:
// cron, one-shot (runAt), or fixed interval (repeatInterval). All
// fires on the host machine; Convex never holds any of this.

type Mode = "cron" | "once" | "interval";

export default function SchedulesScreen() {
  const c = useColors();
  const insets = useSafeAreaInsets();
  const router = useRouter();
  const { connectionStatus } = useDevice();
  const connected = connectionStatus === "connected";

  const [schedules, setSchedules] = useState<ScheduledTask[]>([]);
  const [loading, setLoading] = useState(false);
  const [refreshing, setRefreshing] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  const [showForm, setShowForm] = useState(false);
  const [step, setStep] = useState<1 | 2>(1);
  const [expanded, setExpanded] = useState<Record<string, boolean>>({});
  const [title, setTitle] = useState("");
  const [description, setDescription] = useState("");
  const [mode, setMode] = useState<Mode>("cron");
  const [cron, setCron] = useState("0 9 * * 1-5");
  const [intervalMin, setIntervalMin] = useState("60");
  const [runner, setRunner] = useState("");

  // Cron builder state — friendlier than raw expression.
  type CronFreq = "minutes" | "hourly" | "daily" | "weekdays" | "weekly" | "custom";
  const [cronFreq, setCronFreq] = useState<CronFreq>("weekdays");
  const [cronEveryMin, setCronEveryMin] = useState("15");
  const [cronHour, setCronHour] = useState("9");
  const [cronMinute, setCronMinute] = useState("0");
  const [cronWeekday, setCronWeekday] = useState("1"); // 0=Sun..6=Sat
  const [cronCustom, setCronCustom] = useState("0 9 * * 1-5");

  const [runners, setRunners] = useState<RunnerInfo[]>([]);
  const [runnerOpen, setRunnerOpen] = useState(false);
  const [runnerQuery, setRunnerQuery] = useState("");

  const load = useCallback(async () => {
    if (!connected) return;
    setErr(null);
    try {
      const rows = await quicClient.listSchedules();
      rows.sort((a, b) => a.createdAt.localeCompare(b.createdAt));
      setSchedules(rows);
    } catch (e: any) {
      setErr(e?.message ?? "failed to load");
    } finally {
      setLoading(false);
      setRefreshing(false);
    }
  }, [connected]);

  useEffect(() => {
    setLoading(true);
    void load();
    const handle = setInterval(() => {
      void load();
    }, 15_000);
    return () => clearInterval(handle);
  }, [load]);

  useEffect(() => {
    if (!connected) return;
    quicClient
      .getRunnersState()
      .then((result) => {
        if (result.state === "loaded") setRunners(result.runners.filter((r) => r.installed));
      })
      .catch(() => {});
  }, [connected]);

  // Keep `cron` in sync with the friendly builder.
  useEffect(() => {
    setCron(buildCron({ cronFreq, cronEveryMin, cronHour, cronMinute, cronWeekday, cronCustom }));
  }, [cronFreq, cronEveryMin, cronHour, cronMinute, cronWeekday, cronCustom]);

  function resetForm() {
    setStep(1);
    setTitle("");
    setDescription("");
    setRunner("");
    setRunnerQuery("");
    setRunnerOpen(false);
  }

  async function create() {
    if (!title.trim()) {
      Alert.alert("Schedules", "Title is required");
      return;
    }
    try {
      const spec: Partial<ScheduledTask> & { title: string } = {
        title: title.trim(),
        description: description.trim() || undefined,
        runner: runner.trim() || undefined,
      };
      if (mode === "cron") spec.cron = cron.trim();
      else if (mode === "interval") {
        const n = Number.parseInt(intervalMin, 10);
        if (Number.isFinite(n) && n > 0) spec.repeatInterval = n;
      }
      await quicClient.createSchedule(spec);
      resetForm();
      setShowForm(false);
      await load();
    } catch (e: any) {
      Alert.alert("Schedules", e?.message ?? "failed to create");
    }
  }

  async function remove(s: ScheduledTask) {
    Alert.alert("Delete?", `Remove "${s.title}"?`, [
      { text: "Cancel", style: "cancel" },
      {
        text: "Delete",
        style: "destructive",
        onPress: async () => {
          try {
            await quicClient.deleteSchedule(s.id);
            await load();
          } catch (e: any) {
            Alert.alert("Schedules", e?.message ?? "failed to delete");
          }
        },
      },
    ]);
  }

  async function toggle(sch: ScheduledTask) {
    try {
      if (sch.status === "paused") await quicClient.resumeSchedule(sch.id);
      else await quicClient.pauseSchedule(sch.id);
      await load();
    } catch (e: any) {
      Alert.alert("Schedules", e?.message ?? "failed to update");
    }
  }

  async function runNow(sch: ScheduledTask) {
    try {
      await quicClient.runScheduleNow(sch.id);
      await load();
    } catch (e: any) {
      Alert.alert("Schedules", e?.message ?? "failed to run");
    }
  }

  const renderItem = ({ item }: { item: ScheduledTask }) => (
    <View style={[s.row, { backgroundColor: c.bgCard, borderColor: c.border }]}>
      <View style={{ flexDirection: "row", alignItems: "center", gap: 6 }}>
        <Text style={{ color: c.textPrimary, flexShrink: 1 }} numberOfLines={1}>
          {item.title}
        </Text>
        <Text
          style={{
            color: item.status === "paused" ? c.textMuted : c.accent,
            fontSize: 10,
            borderWidth: 1,
            borderColor: c.border,
            paddingHorizontal: 4,
            paddingVertical: 1,
            borderRadius: 2,
          }}
        >
          {item.status}
        </Text>
      </View>
      <View style={{ marginTop: 4 }}>
        {item.cron ? (
          <Text style={{ color: c.textMuted, fontSize: 11, fontFamily: "monospace" }}>
            cron: {item.cron}
          </Text>
        ) : null}
        {item.runAt ? (
          <Text style={{ color: c.textMuted, fontSize: 11 }}>runAt: {item.runAt}</Text>
        ) : null}
        {item.repeatInterval ? (
          <Text style={{ color: c.textMuted, fontSize: 11 }}>every {item.repeatInterval} min</Text>
        ) : null}
        {item.runner ? (
          <Text style={{ color: c.textMuted, fontSize: 11 }}>runner: {item.runner}</Text>
        ) : null}
        <Text style={{ color: c.textMuted, fontSize: 11 }}>runs: {item.runCount}</Text>
        {item.nextRunAt ? (
          <Text style={{ color: c.textMuted, fontSize: 11 }}>next: {item.nextRunAt}</Text>
        ) : null}
      </View>
      <View style={{ flexDirection: "row", gap: 8, marginTop: 8 }}>
        <Pressable
          style={[s.btn, { backgroundColor: "#052e16", borderColor: "#166534" }]}
          onPress={() => runNow(item)}
        >
          <Text style={{ color: "#bbf7d0" }}>Run now</Text>
        </Pressable>
        <Pressable
          style={[s.btn, { backgroundColor: c.bg, borderColor: c.border }]}
          onPress={() => toggle(item)}
        >
          <Text style={{ color: c.textPrimary }}>{item.status === "paused" ? "Resume" : "Pause"}</Text>
        </Pressable>
        <Pressable
          style={[s.btn, { backgroundColor: "#3f0a0a", borderColor: "#991b1b" }]}
          onPress={() => remove(item)}
        >
          <Text style={{ color: "#fecaca" }}>Delete</Text>
        </Pressable>
      </View>
      {item.history && item.history.length > 0 ? (
        <>
          <Pressable
            onPress={() =>
              setExpanded((prev) => ({ ...prev, [item.id]: !prev[item.id] }))
            }
            style={{ marginTop: 8 }}
          >
            <Text style={{ color: c.textMuted, fontSize: 11 }}>
              {expanded[item.id] ? "Hide" : "Show"} history ({item.history.length})
            </Text>
          </Pressable>
          {expanded[item.id] ? (
            <View
              style={{
                marginTop: 6,
                marginLeft: 6,
                paddingLeft: 8,
                borderLeftWidth: 1,
                borderLeftColor: c.border,
              }}
            >
              {[...item.history]
                .reverse()
                .slice(0, 10)
                .map((h) => {
                  const color =
                    h.status === "completed" || h.status === "finished"
                      ? "#6ee7b7"
                      : h.status === "failed"
                        ? "#fca5a5"
                        : c.textMuted;
                  const dur = h.durationMs
                    ? h.durationMs > 1000
                      ? `${(h.durationMs / 1000).toFixed(1)}s`
                      : `${h.durationMs}ms`
                    : "";
                  return (
                    <View
                      key={`${item.id}-${h.taskId}`}
                      style={{ flexDirection: "row", gap: 6, paddingVertical: 2 }}
                    >
                      <Text style={{ color, fontSize: 10 }}>●</Text>
                      <Text style={{ color: c.textMuted, fontSize: 10, flexShrink: 1 }}>
                        {h.startedAt}
                      </Text>
                      {dur ? (
                        <Text style={{ color: c.textMuted, fontSize: 10 }}>{dur}</Text>
                      ) : null}
                      {typeof h.costUsd === "number" && h.costUsd > 0 ? (
                        <Text style={{ color: c.textMuted, fontSize: 10 }}>
                          ${h.costUsd.toFixed(3)}
                        </Text>
                      ) : null}
                    </View>
                  );
                })}
            </View>
          ) : null}
        </>
      ) : null}
    </View>
  );

  return (
    <KeyboardAvoidingView
      behavior={Platform.OS === "ios" ? "padding" : undefined}
      style={{ flex: 1, backgroundColor: c.bg, paddingTop: insets.top }}
    >
      <View style={[s.header, { borderColor: c.border }]}>
        <AppBackButton onPress={() => router.back()} />
        <Text style={[s.title, { color: c.textPrimary }]}>Schedules</Text>
        <Pressable
          onPress={() => {
            if (showForm) resetForm();
            setShowForm((v) => !v);
          }}
          hitSlop={12}
          style={{
            width: 32,
            height: 32,
            borderRadius: 16,
            alignItems: "center",
            justifyContent: "center",
            backgroundColor: showForm ? "transparent" : `${c.accent}22`,
            borderWidth: 1,
            borderColor: c.accent,
          }}
        >
          <Text style={{ color: c.accent, fontSize: 18, fontWeight: "600", lineHeight: 20 }}>
            {showForm ? "\u00D7" : "+"}
          </Text>
        </Pressable>
      </View>

      {err ? (
        <View style={[s.err, { borderColor: "#991b1b" }]}>
          <Text style={{ color: "#fecaca" }}>{err}</Text>
        </View>
      ) : null}

      {showForm ? (
        <ScrollView
          style={{ maxHeight: 460 }}
          keyboardShouldPersistTaps="handled"
          contentContainerStyle={[s.form, { backgroundColor: c.bgCard, borderColor: c.border }]}
        >
          {/* Step indicator */}
          <View style={{ flexDirection: "row", gap: 6, marginBottom: 10 }}>
            {[1, 2].map((i) => (
              <View
                key={i}
                style={{
                  flex: 1,
                  height: 3,
                  borderRadius: 2,
                  backgroundColor: step >= (i as 1 | 2) ? c.accent : c.border,
                }}
              />
            ))}
          </View>
          <Text style={{ color: c.textMuted, fontSize: 11, marginBottom: 8 }}>
            Step {step} of 2 — {step === 1 ? "Details" : "Schedule"}
          </Text>

          {step === 1 ? (
            <>
              <Text style={{ color: c.textMuted, fontSize: 11 }}>Title</Text>
              <TextInput
                value={title}
                onChangeText={setTitle}
                placeholder="Daily deploy check"
                placeholderTextColor={c.textMuted}
                style={[s.input, { color: c.textPrimary, borderColor: c.border }]}
              />

              <Text style={{ color: c.textMuted, fontSize: 11, marginTop: 10 }}>Runner (optional)</Text>
              <Pressable
                onPress={() => setRunnerOpen((v) => !v)}
                style={[
                  s.input,
                  {
                    borderColor: c.border,
                    flexDirection: "row",
                    alignItems: "center",
                    justifyContent: "space-between",
                    paddingVertical: 10,
                  },
                ]}
              >
                <Text style={{ color: runner ? c.textPrimary : c.textMuted }}>
                  {runner || "Select or type a runner"}
                </Text>
                <Text style={{ color: c.textMuted }}>{runnerOpen ? "▴" : "▾"}</Text>
              </Pressable>
              {runnerOpen ? (
                <View
                  style={{
                    marginTop: 4,
                    borderWidth: 1,
                    borderColor: c.border,
                    borderRadius: 4,
                    overflow: "hidden",
                  }}
                >
                  <TextInput
                    value={runnerQuery}
                    onChangeText={setRunnerQuery}
                    placeholder="Search runners…"
                    placeholderTextColor={c.textMuted}
                    autoCapitalize="none"
                    autoCorrect={false}
                    style={{
                      color: c.textPrimary,
                      paddingHorizontal: 8,
                      paddingVertical: 8,
                      borderBottomWidth: StyleSheet.hairlineWidth,
                      borderBottomColor: c.border,
                    }}
                  />
                  <ScrollView style={{ maxHeight: 160 }} keyboardShouldPersistTaps="handled">
                    {filterRunners(runners, runnerQuery).map((r) => (
                      <Pressable
                        key={r.id}
                        onPress={() => {
                          setRunner(r.id);
                          setRunnerOpen(false);
                          setRunnerQuery("");
                        }}
                        style={{
                          paddingHorizontal: 10,
                          paddingVertical: 8,
                          borderBottomWidth: StyleSheet.hairlineWidth,
                          borderBottomColor: c.border,
                        }}
                      >
                        <Text style={{ color: c.textPrimary, fontSize: 13 }}>{r.name || r.id}</Text>
                        <Text style={{ color: c.textMuted, fontSize: 10 }}>{r.id}</Text>
                      </Pressable>
                    ))}
                    {runnerQuery.trim() && !runners.some((r) => r.id === runnerQuery.trim()) ? (
                      <Pressable
                        onPress={() => {
                          setRunner(runnerQuery.trim());
                          setRunnerOpen(false);
                          setRunnerQuery("");
                        }}
                        style={{ paddingHorizontal: 10, paddingVertical: 8 }}
                      >
                        <Text style={{ color: c.accent, fontSize: 12 }}>
                          Use custom: "{runnerQuery.trim()}"
                        </Text>
                      </Pressable>
                    ) : null}
                    {filterRunners(runners, runnerQuery).length === 0 && !runnerQuery.trim() ? (
                      <Text style={{ color: c.textMuted, padding: 10, fontSize: 12 }}>
                        No runners discovered. Type one above.
                      </Text>
                    ) : null}
                  </ScrollView>
                </View>
              ) : null}

              <Text style={{ color: c.textMuted, fontSize: 11, marginTop: 10 }}>Description / prompt</Text>
              <TextInput
                value={description}
                onChangeText={setDescription}
                placeholder="What should the runner do when this fires?"
                placeholderTextColor={c.textMuted}
                multiline
                textAlignVertical="top"
                style={[
                  s.input,
                  { color: c.textPrimary, borderColor: c.border, minHeight: 120, paddingTop: 8 },
                ]}
              />

              <Pressable
                style={[s.saveBtn, { backgroundColor: c.accent, opacity: title.trim() ? 1 : 0.5 }]}
                onPress={() => {
                  if (!title.trim()) {
                    Alert.alert("Schedules", "Title is required");
                    return;
                  }
                  setStep(2);
                }}
              >
                <Text style={{ color: "#fff", fontWeight: "600" }}>Next</Text>
              </Pressable>
            </>
          ) : (
            <>
              <Text style={{ color: c.textMuted, fontSize: 11 }}>Mode</Text>
              <View style={{ flexDirection: "row", gap: 6, marginTop: 4 }}>
                {(["cron", "once", "interval"] as Mode[]).map((m) => (
                  <Pressable
                    key={m}
                    onPress={() => setMode(m)}
                    style={{
                      borderWidth: 1,
                      borderColor: mode === m ? c.accent : c.border,
                      backgroundColor: mode === m ? `${c.accent}22` : "transparent",
                      paddingHorizontal: 10,
                      paddingVertical: 6,
                      borderRadius: 4,
                    }}
                  >
                    <Text style={{ color: mode === m ? c.accent : c.textMuted, fontSize: 12 }}>
                      {m === "cron" ? "Cron" : m === "once" ? "One-shot" : "Interval"}
                    </Text>
                  </Pressable>
                ))}
              </View>

              {mode === "cron" ? (
                <View style={{ marginTop: 12 }}>
                  <Text style={{ color: c.textMuted, fontSize: 11 }}>Frequency</Text>
                  <View style={{ flexDirection: "row", flexWrap: "wrap", gap: 6, marginTop: 4 }}>
                    {([
                      ["minutes", "Every N min"],
                      ["hourly", "Hourly"],
                      ["daily", "Daily"],
                      ["weekdays", "Weekdays"],
                      ["weekly", "Weekly"],
                      ["custom", "Custom"],
                    ] as Array<[CronFreq, string]>).map(([k, label]) => (
                      <Pressable
                        key={k}
                        onPress={() => setCronFreq(k)}
                        style={{
                          borderWidth: 1,
                          borderColor: cronFreq === k ? c.accent : c.border,
                          backgroundColor: cronFreq === k ? `${c.accent}22` : "transparent",
                          paddingHorizontal: 10,
                          paddingVertical: 6,
                          borderRadius: 4,
                        }}
                      >
                        <Text style={{ color: cronFreq === k ? c.accent : c.textMuted, fontSize: 12 }}>
                          {label}
                        </Text>
                      </Pressable>
                    ))}
                  </View>

                  {cronFreq === "minutes" ? (
                    <View style={{ marginTop: 10 }}>
                      <Text style={{ color: c.textMuted, fontSize: 11 }}>Every N minutes</Text>
                      <TextInput
                        value={cronEveryMin}
                        onChangeText={setCronEveryMin}
                        keyboardType="numeric"
                        placeholder="15"
                        placeholderTextColor={c.textMuted}
                        style={[s.input, { color: c.textPrimary, borderColor: c.border }]}
                      />
                    </View>
                  ) : null}

                  {cronFreq === "hourly" ? (
                    <View style={{ marginTop: 10 }}>
                      <Text style={{ color: c.textMuted, fontSize: 11 }}>At minute</Text>
                      <TextInput
                        value={cronMinute}
                        onChangeText={setCronMinute}
                        keyboardType="numeric"
                        placeholder="0"
                        placeholderTextColor={c.textMuted}
                        style={[s.input, { color: c.textPrimary, borderColor: c.border }]}
                      />
                    </View>
                  ) : null}

                  {(cronFreq === "daily" || cronFreq === "weekdays" || cronFreq === "weekly") ? (
                    <View style={{ flexDirection: "row", gap: 10, marginTop: 10 }}>
                      <View style={{ flex: 1 }}>
                        <Text style={{ color: c.textMuted, fontSize: 11 }}>Hour (0–23)</Text>
                        <TextInput
                          value={cronHour}
                          onChangeText={setCronHour}
                          keyboardType="numeric"
                          placeholder="9"
                          placeholderTextColor={c.textMuted}
                          style={[s.input, { color: c.textPrimary, borderColor: c.border }]}
                        />
                      </View>
                      <View style={{ flex: 1 }}>
                        <Text style={{ color: c.textMuted, fontSize: 11 }}>Minute</Text>
                        <TextInput
                          value={cronMinute}
                          onChangeText={setCronMinute}
                          keyboardType="numeric"
                          placeholder="0"
                          placeholderTextColor={c.textMuted}
                          style={[s.input, { color: c.textPrimary, borderColor: c.border }]}
                        />
                      </View>
                    </View>
                  ) : null}

                  {cronFreq === "weekly" ? (
                    <View style={{ marginTop: 10 }}>
                      <Text style={{ color: c.textMuted, fontSize: 11 }}>Day</Text>
                      <View style={{ flexDirection: "row", gap: 6, marginTop: 4 }}>
                        {["Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"].map((d, i) => (
                          <Pressable
                            key={d}
                            onPress={() => setCronWeekday(String(i))}
                            style={{
                              borderWidth: 1,
                              borderColor: cronWeekday === String(i) ? c.accent : c.border,
                              backgroundColor: cronWeekday === String(i) ? `${c.accent}22` : "transparent",
                              paddingHorizontal: 8,
                              paddingVertical: 6,
                              borderRadius: 4,
                            }}
                          >
                            <Text
                              style={{
                                color: cronWeekday === String(i) ? c.accent : c.textMuted,
                                fontSize: 11,
                              }}
                            >
                              {d}
                            </Text>
                          </Pressable>
                        ))}
                      </View>
                    </View>
                  ) : null}

                  {cronFreq === "custom" ? (
                    <View style={{ marginTop: 10 }}>
                      <Text style={{ color: c.textMuted, fontSize: 11 }}>Cron expression</Text>
                      <TextInput
                        value={cronCustom}
                        onChangeText={setCronCustom}
                        placeholder="0 9 * * 1-5"
                        placeholderTextColor={c.textMuted}
                        autoCapitalize="none"
                        autoCorrect={false}
                        style={[
                          s.input,
                          { color: c.textPrimary, borderColor: c.border, fontFamily: "monospace" },
                        ]}
                      />
                    </View>
                  ) : null}

                  {/* Preview */}
                  <View
                    style={{
                      marginTop: 12,
                      padding: 10,
                      borderRadius: 6,
                      borderWidth: 1,
                      borderColor: c.border,
                      backgroundColor: c.bg,
                    }}
                  >
                    <Text style={{ color: c.textMuted, fontSize: 10, marginBottom: 4 }}>
                      Expression
                    </Text>
                    <Text
                      style={{
                        color: c.textPrimary,
                        fontFamily: "monospace",
                        fontSize: 13,
                      }}
                    >
                      {cron || "(invalid)"}
                    </Text>
                    <Text style={{ color: c.textMuted, fontSize: 11, marginTop: 6 }}>
                      {describeCron(cron)}
                    </Text>
                  </View>
                </View>
              ) : null}

              {mode === "interval" ? (
                <View style={{ marginTop: 10 }}>
                  <Text style={{ color: c.textMuted, fontSize: 11 }}>Every N minutes</Text>
                  <TextInput
                    value={intervalMin}
                    onChangeText={setIntervalMin}
                    keyboardType="numeric"
                    placeholder="60"
                    placeholderTextColor={c.textMuted}
                    style={[s.input, { color: c.textPrimary, borderColor: c.border }]}
                  />
                </View>
              ) : null}

              {mode === "once" ? (
                <Text style={{ color: c.textMuted, fontSize: 12, marginTop: 10 }}>
                  One-shot scheduling with a specific date/time is easier from the web dashboard.
                </Text>
              ) : null}

              <View style={{ flexDirection: "row", gap: 8, marginTop: 14 }}>
                <Pressable
                  style={[
                    s.saveBtn,
                    {
                      backgroundColor: "transparent",
                      borderWidth: 1,
                      borderColor: c.border,
                      flex: 1,
                      marginTop: 0,
                    },
                  ]}
                  onPress={() => setStep(1)}
                >
                  <Text style={{ color: c.textPrimary, fontWeight: "600" }}>Back</Text>
                </Pressable>
                <Pressable
                  style={[
                    s.saveBtn,
                    { backgroundColor: c.accent, flex: 2, marginTop: 0 },
                  ]}
                  onPress={create}
                >
                  <Text style={{ color: "#fff", fontWeight: "600" }}>Create</Text>
                </Pressable>
              </View>
            </>
          )}
        </ScrollView>
      ) : null}

      {loading ? (
        <ActivityIndicator style={{ marginTop: 24 }} color={c.accent} />
      ) : (
        <FlatList
          data={schedules}
          keyExtractor={(i) => i.id}
          renderItem={renderItem}
          contentContainerStyle={{ padding: 12, paddingBottom: insets.bottom + 24 }}
          refreshControl={
            <RefreshControl
              refreshing={refreshing}
              onRefresh={() => {
                setRefreshing(true);
                void load();
              }}
              tintColor={c.accent}
            />
          }
          ListEmptyComponent={
            connected ? (
              <View style={{ alignItems: "center", paddingHorizontal: 24, paddingTop: 48 }}>
                <Text
                  style={{
                    color: c.textMuted,
                    textAlign: "center",
                    marginBottom: 16,
                    fontSize: 14,
                  }}
                >
                  No schedules yet.
                </Text>
                <Pressable
                  onPress={() => {
                    resetForm();
                    setShowForm(true);
                  }}
                  style={{
                    backgroundColor: c.accent,
                    paddingHorizontal: 20,
                    paddingVertical: 12,
                    borderRadius: 8,
                    flexDirection: "row",
                    alignItems: "center",
                    gap: 8,
                  }}
                >
                  <Text style={{ color: "#fff", fontSize: 18, fontWeight: "600" }}>+</Text>
                  <Text style={{ color: "#fff", fontWeight: "600" }}>New schedule</Text>
                </Pressable>
              </View>
            ) : (
              <Text style={{ color: c.textMuted, padding: 16, textAlign: "center" }}>
                Connect to a device to manage schedules.
              </Text>
            )
          }
        />
      )}
    </KeyboardAvoidingView>
  );
}

function filterRunners(runners: RunnerInfo[], q: string): RunnerInfo[] {
  const query = q.trim().toLowerCase();
  if (!query) return runners;
  return runners.filter(
    (r) =>
      r.id.toLowerCase().includes(query) ||
      (r.name || "").toLowerCase().includes(query),
  );
}

function clampInt(raw: string, lo: number, hi: number, fallback: number): number {
  const n = Number.parseInt(raw, 10);
  if (!Number.isFinite(n)) return fallback;
  return Math.max(lo, Math.min(hi, n));
}

function buildCron(p: {
  cronFreq: "minutes" | "hourly" | "daily" | "weekdays" | "weekly" | "custom";
  cronEveryMin: string;
  cronHour: string;
  cronMinute: string;
  cronWeekday: string;
  cronCustom: string;
}): string {
  switch (p.cronFreq) {
    case "minutes": {
      const n = clampInt(p.cronEveryMin, 1, 59, 15);
      return `*/${n} * * * *`;
    }
    case "hourly":
      return `${clampInt(p.cronMinute, 0, 59, 0)} * * * *`;
    case "daily":
      return `${clampInt(p.cronMinute, 0, 59, 0)} ${clampInt(p.cronHour, 0, 23, 9)} * * *`;
    case "weekdays":
      return `${clampInt(p.cronMinute, 0, 59, 0)} ${clampInt(p.cronHour, 0, 23, 9)} * * 1-5`;
    case "weekly":
      return `${clampInt(p.cronMinute, 0, 59, 0)} ${clampInt(p.cronHour, 0, 23, 9)} * * ${clampInt(p.cronWeekday, 0, 6, 1)}`;
    case "custom":
      return p.cronCustom.trim();
  }
}

function describeCron(expr: string): string {
  const parts = expr.trim().split(/\s+/);
  if (parts.length !== 5) return "Custom expression — will be validated by the agent.";
  const [min, hr, dom, mon, dow] = parts;
  const dayNames = ["Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"];
  const pad2 = (x: string) => {
    const n = Number.parseInt(x, 10);
    return Number.isFinite(n) ? String(n).padStart(2, "0") : x;
  };
  // Every N minutes
  const stepMin = min.match(/^\*\/(\d+)$/);
  if (stepMin && hr === "*" && dom === "*" && mon === "*" && dow === "*") {
    return `Every ${stepMin[1]} minutes.`;
  }
  if (min === "*" && hr === "*" && dom === "*" && mon === "*" && dow === "*") return "Every minute.";
  if (hr === "*" && dom === "*" && mon === "*" && dow === "*") return `Every hour at :${pad2(min)}.`;
  const time = `${pad2(hr)}:${pad2(min)}`;
  if (dom === "*" && mon === "*" && dow === "*") return `Every day at ${time}.`;
  if (dom === "*" && mon === "*" && dow === "1-5") return `Weekdays at ${time}.`;
  if (dom === "*" && mon === "*" && /^[0-6]$/.test(dow)) {
    return `Every ${dayNames[Number.parseInt(dow, 10)]} at ${time}.`;
  }
  return `At ${time} (cron: ${expr}).`;
}

const s = StyleSheet.create({
  header: {
    flexDirection: "row",
    alignItems: "center",
    justifyContent: "space-between",
    padding: 12,
    borderBottomWidth: StyleSheet.hairlineWidth,
  },
  title: { fontSize: 17, fontWeight: "600" },
  err: { margin: 12, padding: 8, borderRadius: 6, borderWidth: 1 },
  form: { padding: 12, borderRadius: 6, borderWidth: 1, margin: 12 },
  input: {
    borderWidth: 1,
    borderRadius: 4,
    paddingHorizontal: 8,
    paddingVertical: 6,
    marginTop: 4,
  },
  saveBtn: {
    marginTop: 12,
    paddingVertical: 10,
    borderRadius: 6,
    alignItems: "center",
  },
  row: {
    padding: 12,
    borderRadius: 6,
    borderWidth: 1,
    marginBottom: 8,
  },
  btn: {
    paddingHorizontal: 10,
    paddingVertical: 6,
    borderRadius: 4,
    borderWidth: 1,
  },
});
