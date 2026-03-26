import React, { useCallback, useEffect, useState } from "react";
import {
  Alert,
  FlatList,
  Keyboard,
  KeyboardAvoidingView,
  Platform,
  Pressable,
  StyleSheet,
  Text,
  TextInput,
  View,
} from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { useRouter } from "expo-router";
import { useColors } from "../../src/context/ThemeContext";
import { useDevice } from "../../src/context/DeviceContext";
import { quicClient } from "../../src/lib/quic";
import { getTodos, saveTodos, Todo } from "../../src/lib/storage";

function uuid() {
  return Math.random().toString(36).slice(2) + Date.now().toString(36);
}

export default function TodosScreen() {
  const c = useColors();
  const insets = useSafeAreaInsets();
  const router = useRouter();
  const { connectionStatus } = useDevice();
  const isConnected = connectionStatus === "connected";

  const [todos, setTodos] = useState<Todo[]>([]);
  const [newText, setNewText] = useState("");
  const [sending, setSending] = useState(false);

  useEffect(() => {
    getTodos().then(setTodos);
  }, []);

  const persist = useCallback(async (updated: Todo[]) => {
    setTodos(updated);
    await saveTodos(updated);
  }, []);

  const handleAdd = useCallback(async () => {
    const title = newText.trim();
    if (!title) return;
    const t: Todo = {
      id: uuid(),
      projectId: "_flat",
      title,
      done: false,
      createdAt: Date.now(),
    };
    await persist([t, ...todos]);
    setNewText("");
    Keyboard.dismiss();
  }, [newText, todos, persist]);

  const handleToggle = useCallback(async (id: string) => {
    await persist(todos.map(t => t.id === id ? { ...t, done: !t.done } : t));
  }, [todos, persist]);

  const handleDelete = useCallback(async (id: string) => {
    await persist(todos.filter(t => t.id !== id));
  }, [todos, persist]);

  const handleRunOne = useCallback(async (todo: Todo) => {
    if (!isConnected) {
      Alert.alert("Not connected", "Connect to a device first.");
      return;
    }
    try {
      await quicClient.sendTask(todo.title, "");
      await persist(todos.map(t => t.id === todo.id ? { ...t, done: true } : t));
      router.navigate("/(tabs)/tasks");
    } catch (e: any) {
      Alert.alert("Error", e?.message || "Failed");
    }
  }, [isConnected, todos, persist, router]);

  const handleRunAll = useCallback(async () => {
    const pending = todos.filter(t => !t.done);
    if (pending.length === 0) return;
    if (!isConnected) {
      Alert.alert("Not connected", "Connect to a device first.");
      return;
    }

    Alert.alert(
      "Run All",
      `Send ${pending.length} tasks to the agent? They'll run sequentially — you can close the app and come back later.`,
      [
        { text: "Cancel", style: "cancel" },
        {
          text: `Run ${pending.length}`,
          onPress: async () => {
            setSending(true);
            for (const todo of pending) {
              try {
                await quicClient.sendTask(todo.title, "");
              } catch {}
            }
            await persist(todos.map(t => ({ ...t, done: true })));
            setSending(false);
            router.navigate("/(tabs)/tasks");
          },
        },
      ]
    );
  }, [todos, isConnected, persist, router]);

  const handleClearDone = useCallback(async () => {
    await persist(todos.filter(t => !t.done));
  }, [todos, persist]);

  const pending = todos.filter(t => !t.done);
  const done = todos.filter(t => t.done);

  return (
    <View style={[s.safe, { backgroundColor: c.bg }]}>
      {/* Header */}
      <View style={[s.header, { paddingTop: insets.top + 8, borderBottomColor: c.border }]}>
        <Pressable onPress={() => router.back()} style={{ paddingVertical: 8 }}>
          <Text style={{ color: c.accent, fontSize: 15, fontWeight: "600" }}>{"\u2039"} Back</Text>
        </Pressable>
        <Text style={[s.headerTitle, { color: c.textPrimary }]}>Todos</Text>
        <View style={{ width: 50 }} />
      </View>

      {/* Input */}
      <KeyboardAvoidingView behavior={Platform.OS === "ios" ? "padding" : undefined}>
        <View style={[s.inputRow, { borderColor: c.border }]}>
          <TextInput
            style={[s.input, { color: c.textPrimary }]}
            placeholder="Add a task..."
            placeholderTextColor={c.textMuted}
            value={newText}
            onChangeText={setNewText}
            onSubmitEditing={handleAdd}
            returnKeyType="done"
          />
          <Pressable
            style={[s.addBtn, { backgroundColor: c.accent }, !newText.trim() && { opacity: 0.3 }]}
            onPress={handleAdd}
            disabled={!newText.trim()}
          >
            <Text style={s.addBtnText}>+</Text>
          </Pressable>
        </View>
      </KeyboardAvoidingView>

      {/* Run All bar */}
      {pending.length > 0 && isConnected && (
        <Pressable
          style={[s.runAllBar, sending && { opacity: 0.5 }]}
          onPress={handleRunAll}
          disabled={sending}
        >
          <Text style={s.runAllIcon}>{"\u{1F319}"}</Text>
          <View style={{ flex: 1 }}>
            <Text style={s.runAllText}>Run All ({pending.length})</Text>
            <Text style={s.runAllSub}>Queue all tasks — agent works while you sleep</Text>
          </View>
          <Text style={s.runAllArrow}>{"\u203A"}</Text>
        </Pressable>
      )}

      {/* List */}
      <FlatList
        data={[...pending, ...done]}
        keyExtractor={t => t.id}
        contentContainerStyle={s.listContent}
        ListEmptyComponent={
          <View style={s.empty}>
            <Text style={[s.emptyIcon, { color: c.textMuted }]}>{"\u{1F4DD}"}</Text>
            <Text style={[s.emptyTitle, { color: c.textPrimary }]}>No tasks yet</Text>
            <Text style={[s.emptySubtitle, { color: c.textMuted }]}>
              Write tasks, then "Run All" to send them to your agent.{"\n"}Go to sleep. Review diffs in the morning.
            </Text>
          </View>
        }
        renderItem={({ item }) => (
          <View style={[s.todoRow, { backgroundColor: c.bgCard, borderColor: c.border }]}>
            <Pressable style={s.todoCheck} onPress={() => handleToggle(item.id)}>
              <View style={[
                s.checkbox,
                { borderColor: item.done ? c.success || "#22c55e" : c.border },
                item.done && { backgroundColor: c.success || "#22c55e" },
              ]}>
                {item.done && <Text style={s.checkmark}>{"\u2713"}</Text>}
              </View>
            </Pressable>
            <Pressable style={s.todoBody} onLongPress={() => handleDelete(item.id)}>
              <Text style={[s.todoTitle, { color: item.done ? c.textMuted : c.textPrimary }, item.done && s.todoTitleDone]}>
                {item.title}
              </Text>
            </Pressable>
            {!item.done && (
              <Pressable
                style={[s.runBtn, { backgroundColor: isConnected ? c.accent + "22" : c.bgCardElevated }]}
                onPress={() => handleRunOne(item)}
              >
                <Text style={[s.runBtnText, { color: isConnected ? c.accent : c.textMuted }]}>{"\u25B6"}</Text>
              </Pressable>
            )}
          </View>
        )}
      />

      {/* Clear done */}
      {done.length > 0 && (
        <Pressable style={[s.clearBar, { borderColor: c.border }]} onPress={handleClearDone}>
          <Text style={{ color: c.textMuted, fontSize: 12 }}>Clear {done.length} done</Text>
        </Pressable>
      )}
    </View>
  );
}

const s = StyleSheet.create({
  safe: { flex: 1 },
  header: { flexDirection: "row", alignItems: "center", justifyContent: "space-between", paddingHorizontal: 16, paddingBottom: 10, borderBottomWidth: 1 },
  headerTitle: { fontSize: 17, fontWeight: "700" },
  inputRow: {
    flexDirection: "row",
    alignItems: "center",
    marginHorizontal: 16,
    marginTop: 12,
    marginBottom: 4,
    borderWidth: 1,
    borderRadius: 10,
    paddingLeft: 14,
    gap: 8,
  },
  input: { flex: 1, fontSize: 15, paddingVertical: 12 },
  addBtn: { width: 40, height: 40, borderRadius: 8, alignItems: "center", justifyContent: "center", marginRight: 4 },
  addBtnText: { color: "#fff", fontSize: 22, fontWeight: "300" },

  runAllBar: {
    flexDirection: "row",
    alignItems: "center",
    marginHorizontal: 16,
    marginTop: 8,
    marginBottom: 4,
    padding: 12,
    borderRadius: 10,
    backgroundColor: "#0f1a2e",
    borderWidth: 1,
    borderColor: "#1a2e4a",
    gap: 10,
  },
  runAllIcon: { fontSize: 20 },
  runAllText: { color: "#60a5fa", fontSize: 14, fontWeight: "700" },
  runAllSub: { color: "#60a5fa88", fontSize: 11, marginTop: 1 },
  runAllArrow: { color: "#60a5fa", fontSize: 20 },

  listContent: { padding: 16, paddingBottom: 80 },
  empty: { paddingTop: 60, alignItems: "center", paddingHorizontal: 20 },
  emptyIcon: { fontSize: 40, marginBottom: 12 },
  emptyTitle: { fontSize: 18, fontWeight: "700", marginBottom: 8 },
  emptySubtitle: { fontSize: 13, textAlign: "center", lineHeight: 20 },

  todoRow: { flexDirection: "row", alignItems: "center", borderRadius: 10, borderWidth: 1, marginBottom: 8, padding: 12 },
  todoCheck: { padding: 4, marginRight: 10 },
  checkbox: { width: 22, height: 22, borderRadius: 6, borderWidth: 2, alignItems: "center", justifyContent: "center" },
  checkmark: { color: "#fff", fontSize: 13, fontWeight: "700" },
  todoBody: { flex: 1, marginRight: 8 },
  todoTitle: { fontSize: 15, fontWeight: "500" },
  todoTitleDone: { textDecorationLine: "line-through" as const, opacity: 0.5 },
  runBtn: { width: 34, height: 34, borderRadius: 8, alignItems: "center", justifyContent: "center" },
  runBtnText: { fontSize: 13, fontWeight: "700" },

  clearBar: { alignItems: "center", paddingVertical: 10, borderTopWidth: 1 },
});
