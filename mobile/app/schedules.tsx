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
import { quicClient, type ScheduledTask } from "../src/lib/quic";

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
  const [title, setTitle] = useState("");
  const [description, setDescription] = useState("");
  const [mode, setMode] = useState<Mode>("cron");
  const [cron, setCron] = useState("0 9 * * 1-5");
  const [intervalMin, setIntervalMin] = useState("60");
  const [runner, setRunner] = useState("");

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
      setTitle("");
      setDescription("");
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
    </View>
  );

  return (
    <KeyboardAvoidingView
      behavior={Platform.OS === "ios" ? "padding" : undefined}
      style={{ flex: 1, backgroundColor: c.bg, paddingTop: insets.top }}
    >
      <View style={[s.header, { borderColor: c.border }]}>
        <Pressable onPress={() => router.back()}>
          <Text style={{ color: c.textMuted, fontSize: 20 }}>{"\u2039"}</Text>
        </Pressable>
        <Text style={[s.title, { color: c.textPrimary }]}>Schedules</Text>
        <Pressable onPress={() => setShowForm((v) => !v)}>
          <Text style={{ color: c.accent, fontSize: 18 }}>{showForm ? "\u00D7" : "+"}</Text>
        </Pressable>
      </View>

      {err ? (
        <View style={[s.err, { borderColor: "#991b1b" }]}>
          <Text style={{ color: "#fecaca" }}>{err}</Text>
        </View>
      ) : null}

      {showForm ? (
        <ScrollView
          style={{ maxHeight: 360 }}
          contentContainerStyle={[s.form, { backgroundColor: c.bgCard, borderColor: c.border }]}
        >
          <Text style={{ color: c.textMuted, fontSize: 11 }}>Title</Text>
          <TextInput
            value={title}
            onChangeText={setTitle}
            placeholder="Daily deploy check"
            placeholderTextColor={c.textMuted}
            style={[s.input, { color: c.textPrimary, borderColor: c.border }]}
          />
          <Text style={{ color: c.textMuted, fontSize: 11, marginTop: 8 }}>Runner (optional)</Text>
          <TextInput
            value={runner}
            onChangeText={setRunner}
            placeholder="claude-code / aider / codex"
            placeholderTextColor={c.textMuted}
            autoCapitalize="none"
            autoCorrect={false}
            style={[s.input, { color: c.textPrimary, borderColor: c.border }]}
          />
          <Text style={{ color: c.textMuted, fontSize: 11, marginTop: 8 }}>Description / prompt</Text>
          <TextInput
            value={description}
            onChangeText={setDescription}
            placeholder="what should the runner do"
            placeholderTextColor={c.textMuted}
            multiline
            style={[s.input, { color: c.textPrimary, borderColor: c.border, minHeight: 48 }]}
          />
          <Text style={{ color: c.textMuted, fontSize: 11, marginTop: 8 }}>Mode</Text>
          <View style={{ flexDirection: "row", gap: 6, marginTop: 4 }}>
            {(["cron", "once", "interval"] as Mode[]).map((m) => (
              <Pressable
                key={m}
                onPress={() => setMode(m)}
                style={{
                  borderWidth: 1,
                  borderColor: mode === m ? c.accent : c.border,
                  backgroundColor: mode === m ? `${c.accent}22` : "transparent",
                  paddingHorizontal: 8,
                  paddingVertical: 4,
                  borderRadius: 4,
                }}
              >
                <Text style={{ color: mode === m ? c.accent : c.textMuted, fontSize: 11 }}>
                  {m === "cron" ? "Cron" : m === "once" ? "One-shot" : "Interval"}
                </Text>
              </Pressable>
            ))}
          </View>
          {mode === "cron" ? (
            <>
              <Text style={{ color: c.textMuted, fontSize: 11, marginTop: 8 }}>Cron expression</Text>
              <TextInput
                value={cron}
                onChangeText={setCron}
                placeholder="0 9 * * 1-5"
                placeholderTextColor={c.textMuted}
                autoCapitalize="none"
                autoCorrect={false}
                style={[s.input, { color: c.textPrimary, borderColor: c.border, fontFamily: "monospace" }]}
              />
            </>
          ) : null}
          {mode === "interval" ? (
            <>
              <Text style={{ color: c.textMuted, fontSize: 11, marginTop: 8 }}>Every N minutes</Text>
              <TextInput
                value={intervalMin}
                onChangeText={setIntervalMin}
                keyboardType="numeric"
                placeholder="60"
                placeholderTextColor={c.textMuted}
                style={[s.input, { color: c.textPrimary, borderColor: c.border }]}
              />
            </>
          ) : null}
          {mode === "once" ? (
            <Text style={{ color: c.textMuted, fontSize: 11, marginTop: 8 }}>
              One-shot scheduling with a specific date/time is easier from the web dashboard.
            </Text>
          ) : null}
          <Pressable style={[s.saveBtn, { backgroundColor: c.accent }]} onPress={create}>
            <Text style={{ color: "#fff", fontWeight: "600" }}>Create</Text>
          </Pressable>
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
            <Text style={{ color: c.textMuted, padding: 16, textAlign: "center" }}>
              {connected ? "No schedules yet. Tap + to add one." : "Connect to a device to manage schedules."}
            </Text>
          }
        />
      )}
    </KeyboardAvoidingView>
  );
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
