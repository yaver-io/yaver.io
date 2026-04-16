import React, { useCallback, useEffect, useRef, useState } from "react";
import {
  ActivityIndicator,
  Alert,
  FlatList,
  KeyboardAvoidingView,
  Platform,
  Pressable,
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
import { quicClient } from "../src/lib/quic";

// Mobile parity with the web ExecView. Runs shell commands on the
// connected agent via /exec and polls /exec/{id} for output. Nothing
// touches Convex — output streams back over the direct-or-relay
// P2P channel.

type ExecSession = {
  id: string;
  command: string;
  status: "running" | "completed" | "failed";
  stdout: string;
  stderr: string;
  startedAt: string;
  finishedAt?: string;
  exitCode?: number;
  pid?: number;
};

export default function ExecScreen() {
  const c = useColors();
  const insets = useSafeAreaInsets();
  const router = useRouter();
  const { connectionStatus } = useDevice();
  const connected = connectionStatus === "connected";

  const [execs, setExecs] = useState<ExecSession[]>([]);
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const [selected, setSelected] = useState<ExecSession | null>(null);
  const [loading, setLoading] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const [command, setCommand] = useState("");
  const [workDir, setWorkDir] = useState("");
  const [starting, setStarting] = useState(false);
  const outputRef = useRef<ScrollView>(null);

  const loadList = useCallback(async () => {
    if (!connected) return;
    try {
      const list = (await quicClient.listExecs()) as ExecSession[];
      list.sort((a, b) => b.startedAt.localeCompare(a.startedAt));
      setExecs(list);
      if (!selectedId && list.length > 0) setSelectedId(list[0].id);
    } catch (e: any) {
      setErr(e?.message ?? "failed to load");
    } finally {
      setLoading(false);
    }
  }, [connected, selectedId]);

  useEffect(() => {
    setLoading(true);
    void loadList();
    const iv = setInterval(() => void loadList(), 4_000);
    return () => clearInterval(iv);
  }, [loadList]);

  useEffect(() => {
    if (!selectedId) {
      setSelected(null);
      return;
    }
    let cancelled = false;
    const poll = async () => {
      try {
        const snap = (await quicClient.getExec(selectedId)) as ExecSession | null;
        if (!cancelled) setSelected(snap);
      } catch {
        // transient
      }
    };
    void poll();
    const iv = setInterval(poll, 500);
    return () => {
      cancelled = true;
      clearInterval(iv);
    };
  }, [selectedId]);

  async function run() {
    if (!command.trim() || starting) return;
    setStarting(true);
    try {
      const res = await quicClient.startExec(command.trim(), {
        workDir: workDir.trim() || undefined,
      });
      setCommand("");
      setSelectedId(res.execId);
      await loadList();
    } catch (e: any) {
      Alert.alert("Exec", e?.message ?? "failed to start");
    } finally {
      setStarting(false);
    }
  }

  async function kill(id: string) {
    try {
      await quicClient.killExec(id);
      await loadList();
    } catch (e: any) {
      Alert.alert("Exec", e?.message ?? "failed to kill");
    }
  }

  return (
    <KeyboardAvoidingView
      behavior={Platform.OS === "ios" ? "padding" : undefined}
      style={{ flex: 1, backgroundColor: c.bg, paddingTop: insets.top }}
    >
      <View style={[s.header, { borderColor: c.border }]}>
        <Pressable onPress={() => router.back()}>
          <Text style={{ color: c.textMuted, fontSize: 20 }}>{"\u2039"}</Text>
        </Pressable>
        <Text style={[s.title, { color: c.textPrimary }]}>Exec</Text>
        <View style={{ width: 20 }} />
      </View>

      {err ? (
        <View style={[s.err, { borderColor: "#991b1b" }]}>
          <Text style={{ color: "#fecaca" }}>{err}</Text>
        </View>
      ) : null}

      {/* Command form */}
      <View style={[s.form, { backgroundColor: c.bgCard, borderColor: c.border }]}>
        <TextInput
          value={command}
          onChangeText={setCommand}
          placeholder="command (e.g. git status)"
          placeholderTextColor={c.textMuted}
          autoCapitalize="none"
          autoCorrect={false}
          onSubmitEditing={() => void run()}
          style={[s.input, { color: c.textPrimary, borderColor: c.border, fontFamily: "monospace" }]}
        />
        <TextInput
          value={workDir}
          onChangeText={setWorkDir}
          placeholder="workDir (optional)"
          placeholderTextColor={c.textMuted}
          autoCapitalize="none"
          autoCorrect={false}
          style={[s.input, { color: c.textPrimary, borderColor: c.border, marginTop: 6 }]}
        />
        <Pressable
          style={[s.runBtn, { backgroundColor: c.accent, opacity: command.trim() && !starting ? 1 : 0.4 }]}
          disabled={!command.trim() || starting}
          onPress={() => void run()}
        >
          <Text style={{ color: "#fff", fontWeight: "600" }}>{starting ? "Starting…" : "Run"}</Text>
        </Pressable>
      </View>

      {/* Session list */}
      <Text style={{ color: c.textMuted, fontSize: 11, paddingHorizontal: 12, marginTop: 8 }}>
        Recent execs
      </Text>
      <FlatList
        data={execs}
        horizontal
        keyExtractor={(i) => i.id}
        contentContainerStyle={{ paddingHorizontal: 12, paddingVertical: 6, gap: 6 }}
        renderItem={({ item }) => (
          <Pressable
            onPress={() => setSelectedId(item.id)}
            style={{
              borderWidth: 1,
              borderColor: selectedId === item.id ? c.accent : c.border,
              backgroundColor: selectedId === item.id ? `${c.accent}22` : c.bgCard,
              paddingHorizontal: 10,
              paddingVertical: 6,
              borderRadius: 4,
              maxWidth: 220,
            }}
          >
            <View style={{ flexDirection: "row", gap: 4, alignItems: "center" }}>
              <Text
                style={{
                  color:
                    item.status === "running"
                      ? "#fbbf24"
                      : item.status === "completed"
                        ? "#34d399"
                        : "#f87171",
                  fontSize: 10,
                }}
              >
                {item.status}
              </Text>
              {typeof item.exitCode === "number" ? (
                <Text style={{ color: c.textMuted, fontSize: 10 }}>{item.exitCode}</Text>
              ) : null}
            </View>
            <Text
              numberOfLines={1}
              style={{ color: c.textPrimary, fontFamily: "monospace", fontSize: 11 }}
            >
              {item.command}
            </Text>
          </Pressable>
        )}
        ListEmptyComponent={
          loading ? (
            <ActivityIndicator color={c.accent} style={{ paddingHorizontal: 12 }} />
          ) : (
            <Text style={{ color: c.textMuted, padding: 12 }}>
              {connected ? "No execs yet." : "Connect to a device first."}
            </Text>
          )
        }
      />

      {/* Selected exec output */}
      {selected ? (
        <View style={{ flex: 1, margin: 12, marginTop: 4 }}>
          <View style={{ flexDirection: "row", alignItems: "center", gap: 8, marginBottom: 6 }}>
            <Text
              style={{ color: c.textPrimary, fontFamily: "monospace", flexShrink: 1 }}
              numberOfLines={1}
            >
              {selected.command}
            </Text>
            {selected.status === "running" ? (
              <Pressable
                onPress={() => kill(selected.id)}
                style={{
                  backgroundColor: "#3f0a0a",
                  borderWidth: 1,
                  borderColor: "#991b1b",
                  paddingHorizontal: 8,
                  paddingVertical: 3,
                  borderRadius: 4,
                }}
              >
                <Text style={{ color: "#fecaca", fontSize: 11 }}>Kill</Text>
              </Pressable>
            ) : null}
          </View>
          <ScrollView
            ref={outputRef}
            style={{
              flex: 1,
              backgroundColor: "#000000aa",
              borderRadius: 4,
              padding: 8,
            }}
            onContentSizeChange={() => outputRef.current?.scrollToEnd({ animated: false })}
          >
            <Text
              selectable
              style={{ color: c.textPrimary, fontFamily: "monospace", fontSize: 11 }}
            >
              {selected.stdout}
              {selected.stderr ? `\n${selected.stderr}` : ""}
              {selected.status !== "running" && typeof selected.exitCode === "number"
                ? `\n[exit ${selected.exitCode}]`
                : ""}
            </Text>
          </ScrollView>
        </View>
      ) : (
        <View style={{ flex: 1 }} />
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
  form: {
    margin: 12,
    padding: 12,
    borderRadius: 6,
    borderWidth: 1,
  },
  input: {
    borderWidth: 1,
    borderRadius: 4,
    paddingHorizontal: 8,
    paddingVertical: 6,
  },
  runBtn: {
    marginTop: 10,
    paddingVertical: 10,
    borderRadius: 6,
    alignItems: "center",
  },
});
