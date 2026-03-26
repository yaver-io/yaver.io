import React, { useCallback, useEffect, useRef, useState } from "react";
import {
  Alert,
  Animated,
  Easing,
  FlatList,
  Keyboard,
  Platform,
  Pressable,
  StyleSheet,
  Text,
  TextInput,
  View,
} from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { useRouter } from "expo-router";
import { useDevice } from "../../src/context/DeviceContext";
import { useColors } from "../../src/context/ThemeContext";
import { quicClient } from "../../src/lib/quic";
import { getTodos, saveTodos, Todo } from "../../src/lib/storage";

function uuid() {
  return Math.random().toString(36).slice(2) + Date.now().toString(36);
}

// Pulsing circle for "implementing" status
function PulsingCircle() {
  const anim = useRef(new Animated.Value(0.3)).current;
  useEffect(() => {
    const pulse = Animated.loop(
      Animated.sequence([
        Animated.timing(anim, { toValue: 1, duration: 800, easing: Easing.inOut(Easing.ease), useNativeDriver: true }),
        Animated.timing(anim, { toValue: 0.3, duration: 800, easing: Easing.inOut(Easing.ease), useNativeDriver: true }),
      ])
    );
    pulse.start();
    return () => pulse.stop();
  }, [anim]);
  return (
    <Animated.View style={[s.checkbox, { borderColor: "#6366f1", backgroundColor: "#6366f1", opacity: anim }]}>
      <View style={s.pulseInner} />
    </Animated.View>
  );
}

export default function TodosScreen() {
  const c = useColors();
  const insets = useSafeAreaInsets();
  const router = useRouter();
  const { connectionStatus } = useDevice();
  const isConnected = connectionStatus === "connected";
  const inputRef = useRef<TextInput>(null);

  const [todos, setTodos] = useState<Todo[]>([]);
  const [newText, setNewText] = useState("");
  const [showInput, setShowInput] = useState(false);
  const [showCompleted, setShowCompleted] = useState(false);
  const [autopilot, setAutopilot] = useState(false);
  const [sending, setSending] = useState(false);
  const [editingId, setEditingId] = useState<string | null>(null);
  const [editText, setEditText] = useState("");

  useEffect(() => {
    getTodos().then(setTodos);
  }, []);

  // When connected: fetch autopilot state and auto-sync pending todos to agent
  useEffect(() => {
    if (!isConnected) return;
    quicClient.getAutopilot().then(setAutopilot).catch(() => {});

    // Auto-sync: push any local pending todos to the agent that haven't been sent yet
    const unsyncedPending = todos.filter(t => !t.done && !t.agentItemId);
    if (unsyncedPending.length > 0) {
      (async () => {
        let updated = [...todos];
        for (const todo of unsyncedPending) {
          try {
            const res = await quicClient.addTodoItem(todo.title);
            if (res?.id) {
              updated = updated.map(t =>
                t.id === todo.id ? { ...t, agentItemId: res.id, agentStatus: "pending" as const } : t
              );
            }
          } catch {}
        }
        persist(updated);
      })();
    }
  }, [isConnected]); // eslint-disable-line react-hooks/exhaustive-deps

  // Poll agent for todo statuses when any item is implementing
  useEffect(() => {
    const hasImplementing = todos.some(t => t.agentStatus === "implementing");
    if (!hasImplementing || !isConnected) return;

    const interval = setInterval(async () => {
      try {
        const items = await quicClient.listTodoItems();
        setTodos(prev => {
          const updated = prev.map(t => {
            if (!t.agentItemId) return t;
            const agentItem = items.find((i: any) => i.id === t.agentItemId);
            if (!agentItem) return t;
            const isDone = agentItem.status === "done";
            const isFailed = agentItem.status === "failed";
            return {
              ...t,
              agentStatus: agentItem.status as Todo["agentStatus"],
              done: isDone || isFailed ? true : t.done,
              taskId: agentItem.taskId || t.taskId,
            };
          });
          saveTodos(updated);
          return updated;
        });
      } catch {}
    }, 5000);

    return () => clearInterval(interval);
  }, [todos, isConnected]);

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
    setShowInput(false);
  }, [newText, todos, persist]);

  const handleToggle = useCallback(async (id: string) => {
    await persist(todos.map(t => t.id === id ? { ...t, done: !t.done } : t));
  }, [todos, persist]);

  const handleDelete = useCallback(async (id: string) => {
    Alert.alert("Delete task?", "", [
      { text: "Cancel", style: "cancel" },
      { text: "Delete", style: "destructive", onPress: () => persist(todos.filter(t => t.id !== id)) },
    ]);
  }, [todos, persist]);

  const handleEdit = useCallback((todo: Todo) => {
    setEditingId(todo.id);
    setEditText(todo.title);
  }, []);

  const handleSaveEdit = useCallback(async () => {
    if (!editingId) return;
    const trimmed = editText.trim();
    if (trimmed) {
      await persist(todos.map(t => t.id === editingId ? { ...t, title: trimmed } : t));
    }
    setEditingId(null);
    setEditText("");
  }, [editingId, editText, todos, persist]);

  const handleRunOne = useCallback(async (todo: Todo) => {
    if (!isConnected) {
      Alert.alert("Not connected", "Connect to a device first.");
      return;
    }
    try {
      const result = await quicClient.sendTask(todo.title, "");
      await persist(todos.map(t => t.id === todo.id ? { ...t, done: true, taskId: result?.id } : t));
    } catch (e: any) {
      Alert.alert("Error", e?.message || "Failed");
    }
  }, [isConnected, todos, persist]);

  const handleAutopilot = useCallback(async () => {
    if (!isConnected) {
      Alert.alert("Not connected", "Connect to a device first.");
      return;
    }

    const pending = todos.filter(t => !t.done);
    const newState = !autopilot;

    if (newState && pending.length === 0) {
      Alert.alert("No tasks", "Add some tasks first.");
      return;
    }

    if (newState) {
      Alert.alert(
        "Auto-Drive",
        `Send ${pending.length} tasks to the agent? It will work through them automatically.`,
        [
          { text: "Cancel", style: "cancel" },
          {
            text: "Start",
            onPress: async () => {
              setSending(true);
              try {
                // Send all pending todos to agent
                for (const todo of pending) {
                  try {
                    const res = await quicClient.addTodoItem(todo.title);
                    if (res?.id) {
                      await persist(todos.map(t =>
                        t.id === todo.id ? { ...t, agentItemId: res.id, agentStatus: "pending" as const } : t
                      ));
                    }
                  } catch {}
                }
                // Enable autopilot — agent starts processing
                await quicClient.setAutopilot(true);
                setAutopilot(true);
              } catch (e: any) {
                Alert.alert("Error", e?.message || "Failed to enable auto-drive");
              }
              setSending(false);
            },
          },
        ]
      );
    } else {
      try {
        await quicClient.setAutopilot(false);
        setAutopilot(false);
      } catch {}
    }
  }, [autopilot, isConnected, todos, persist]);

  const handleClearDone = useCallback(async () => {
    await persist(todos.filter(t => !t.done));
  }, [todos, persist]);

  const handleFAB = useCallback(() => {
    setShowInput(true);
    setTimeout(() => inputRef.current?.focus(), 100);
  }, []);

  const pending = todos.filter(t => !t.done);
  const done = todos.filter(t => t.done);

  const renderPendingItem = ({ item }: { item: Todo }) => {
    const isEditing = editingId === item.id;
    return (
      <Pressable
        style={[s.row, { borderBottomColor: c.border }]}
        onPress={() => !isEditing && handleEdit(item)}
        onLongPress={() => handleDelete(item.id)}
        delayLongPress={500}
      >
        <Pressable style={s.checkArea} onPress={() => handleToggle(item.id)}>
          {item.agentStatus === "implementing" ? (
            <PulsingCircle />
          ) : (
            <View style={[s.checkbox, { borderColor: c.textMuted }]} />
          )}
        </Pressable>
        <View style={{ flex: 1 }}>
          {isEditing ? (
            <TextInput
              style={[s.rowTitle, { color: c.textPrimary, padding: 0 }]}
              value={editText}
              onChangeText={setEditText}
              onSubmitEditing={handleSaveEdit}
              onBlur={handleSaveEdit}
              autoFocus
              returnKeyType="done"
              multiline={false}
            />
          ) : (
            <Text style={[s.rowTitle, { color: c.textPrimary }]} numberOfLines={2}>{item.title}</Text>
          )}
          {/* Status chips */}
          {item.agentStatus && item.agentStatus !== "pending" && (
            <View style={{ flexDirection: "row", gap: 6, marginTop: 4 }}>
              <View style={[s.statusChip, {
                backgroundColor: item.agentStatus === "implementing" ? "#6366f122" :
                  item.agentStatus === "done" ? "#22c55e22" : "#ef444422"
              }]}>
                <Text style={[s.statusChipText, {
                  color: item.agentStatus === "implementing" ? "#6366f1" :
                    item.agentStatus === "done" ? "#22c55e" : "#ef4444"
                }]}>
                  {item.agentStatus === "implementing" ? "Working..." :
                    item.agentStatus === "done" ? "Done" : "Failed"}
                </Text>
              </View>
              {item.taskId && (
                <Pressable
                  style={[s.statusChip, { backgroundColor: c.bgCardElevated }]}
                  onPress={() => router.push({ pathname: "/(tabs)/tasks", params: { taskId: item.taskId } })}
                >
                  <Text style={[s.statusChipText, { color: c.accent }]}>View task {"\u203A"}</Text>
                </Pressable>
              )}
            </View>
          )}
        </View>
        {!autopilot && isConnected && !isEditing && !item.agentStatus && (
          <Pressable style={[s.implementBtn, { backgroundColor: c.accent + "18" }]} onPress={() => handleRunOne(item)}>
            <Text style={[s.implementText, { color: c.accent }]}>Implement</Text>
          </Pressable>
        )}
      </Pressable>
    );
  };

  const renderDoneItem = ({ item }: { item: Todo }) => (
    <Pressable
      style={[s.row, { borderBottomColor: c.border }]}
      onPress={() => {
        if (item.taskId) {
          router.push({ pathname: "/(tabs)/tasks", params: { taskId: item.taskId } });
        }
      }}
      onLongPress={() => handleDelete(item.id)}
      delayLongPress={500}
    >
      <Pressable style={s.checkArea} onPress={() => handleToggle(item.id)}>
        <View style={[
          s.checkbox,
          item.agentStatus === "failed"
            ? { borderColor: "#ef4444", backgroundColor: "#ef4444" }
            : { borderColor: "#6366f1", backgroundColor: "#6366f1" },
        ]}>
          {item.agentStatus === "failed" ? (
            <Text style={s.checkmark}>!</Text>
          ) : (
            <Text style={s.checkmark}>{"\u2713"}</Text>
          )}
        </View>
      </Pressable>
      <Text style={[s.rowTitle, s.rowTitleDone, { color: c.textMuted }]} numberOfLines={2}>{item.title}</Text>
      {item.taskId && <Text style={[s.chevron, { color: c.textMuted }]}>{"\u203A"}</Text>}
    </Pressable>
  );

  return (
    <View style={[s.container, { backgroundColor: c.bg }]}>
      {/* Header */}
      <View style={[s.header, { paddingTop: insets.top + 12, borderBottomColor: c.border }]}>
        <Pressable onPress={() => router.back()} style={{ paddingVertical: 4 }}>
          <Text style={{ color: c.accent, fontSize: 15, fontWeight: "600" }}>{"\u2039"} Back</Text>
        </Pressable>
        <Text style={[s.headerTitle, { color: c.textPrimary }]}>Todos</Text>
        <View style={s.headerRight}>
          {isConnected && pending.length > 0 && (
            <Pressable
              style={[s.autopilotBtn, { backgroundColor: c.bgCardElevated }, autopilot && { backgroundColor: "#6366f122" }, sending && { opacity: 0.5 }]}
              onPress={handleAutopilot}
              disabled={sending}
            >
              <Text style={s.autopilotIcon}>
                {"\u26A1"}
              </Text>
              <Text style={[s.autopilotText, { color: c.textMuted }, autopilot && { color: "#6366f1", fontWeight: "600" }]}>
                {autopilot ? "Driving" : "Auto-Drive"}
              </Text>
            </Pressable>
          )}
        </View>
      </View>

      {/* Inline input */}
      {showInput && (
        <View style={[s.inputRow, { borderBottomColor: c.border }]}>
          <View style={[s.checkbox, { borderColor: c.textMuted }]} />
          <TextInput
            ref={inputRef}
            style={[s.inputText, { color: c.textPrimary }]}
            placeholder="New task"
            placeholderTextColor={c.textMuted}
            value={newText}
            onChangeText={setNewText}
            onSubmitEditing={handleAdd}
            onBlur={() => { if (!newText.trim()) setShowInput(false); }}
            returnKeyType="done"
            autoFocus
          />
        </View>
      )}

      {/* Pending items */}
      <FlatList
        data={pending}
        keyExtractor={t => t.id}
        renderItem={renderPendingItem}
        contentContainerStyle={s.listContent}
        keyboardShouldPersistTaps="handled"
        ListEmptyComponent={
          !showInput ? (
            <View style={s.empty}>
              <Text style={[s.emptyTitle, { color: c.textPrimary }]}>No tasks yet</Text>
              <Text style={[s.emptySubtitle, { color: c.textMuted }]}>
                Tap + to add tasks.{"\n"}Hit Auto-Drive and go to sleep.
              </Text>
            </View>
          ) : null
        }
        ListFooterComponent={
          <>
            {/* Completed section */}
            {done.length > 0 && (
              <>
                <Pressable
                  style={[s.completedHeader, { borderBottomColor: c.border }]}
                  onPress={() => setShowCompleted(!showCompleted)}
                >
                  <Text style={[s.completedChevron, { color: c.textMuted }]}>
                    {showCompleted ? "\u25BC" : "\u25B6"}
                  </Text>
                  <Text style={[s.completedText, { color: c.textMuted }]}>Completed ({done.length})</Text>
                  <View style={{ flex: 1 }} />
                  <Pressable onPress={handleClearDone} hitSlop={8}>
                    <Text style={[s.clearText, { color: c.accent }]}>Clear</Text>
                  </Pressable>
                </Pressable>
                {showCompleted && done.map(item => (
                  <View key={item.id}>{renderDoneItem({ item })}</View>
                ))}
              </>
            )}
          </>
        }
      />

      {/* FAB */}
      <Pressable
        style={[s.fab, { bottom: insets.bottom + 16, backgroundColor: c.bgCard, borderColor: c.border }]}
        onPress={handleFAB}
      >
        <Text style={[s.fabIcon, { color: c.accent }]}>+</Text>
      </Pressable>
    </View>
  );
}

const s = StyleSheet.create({
  container: {
    flex: 1,
  },

  // Header
  header: {
    flexDirection: "row",
    alignItems: "center",
    justifyContent: "space-between",
    paddingHorizontal: 16,
    paddingBottom: 12,
    borderBottomWidth: StyleSheet.hairlineWidth,
  },
  headerTitle: {
    fontSize: 17,
    fontWeight: "700",
  },
  headerRight: {
    flexDirection: "row",
    alignItems: "center",
    gap: 8,
  },

  // Autopilot button
  autopilotBtn: {
    flexDirection: "row",
    alignItems: "center",
    paddingHorizontal: 12,
    paddingVertical: 6,
    borderRadius: 16,
    gap: 4,
  },
  autopilotIcon: {
    fontSize: 13,
  },
  autopilotText: {
    fontSize: 13,
    fontWeight: "500",
  },

  // Input row
  inputRow: {
    flexDirection: "row",
    alignItems: "center",
    paddingHorizontal: 16,
    paddingVertical: 14,
    borderBottomWidth: StyleSheet.hairlineWidth,
    gap: 14,
  },
  inputText: {
    flex: 1,
    fontSize: 16,
    padding: 0,
  },

  // List
  listContent: {
    paddingBottom: 100,
  },

  // Row
  row: {
    flexDirection: "row",
    alignItems: "center",
    paddingHorizontal: 16,
    paddingVertical: 14,
    borderBottomWidth: StyleSheet.hairlineWidth,
    gap: 14,
  },
  checkArea: {
    padding: 2,
  },
  checkbox: {
    width: 22,
    height: 22,
    borderRadius: 11,
    borderWidth: 2,
    alignItems: "center",
    justifyContent: "center",
  },
  pulseInner: {
    width: 8,
    height: 8,
    borderRadius: 4,
    backgroundColor: "#ffffff",
  },
  checkmark: {
    color: "#ffffff",
    fontSize: 12,
    fontWeight: "700",
  },
  rowTitle: {
    flex: 1,
    fontSize: 16,
    fontWeight: "400",
  },
  rowTitleDone: {
    textDecorationLine: "line-through" as const,
  },
  implementBtn: {
    paddingHorizontal: 10,
    paddingVertical: 5,
    borderRadius: 12,
  },
  implementText: {
    fontSize: 12,
    fontWeight: "600",
  },
  statusChip: {
    paddingHorizontal: 8,
    paddingVertical: 2,
    borderRadius: 8,
  },
  statusChipText: {
    fontSize: 11,
    fontWeight: "500",
  },
  chevron: {
    fontSize: 20,
    fontWeight: "300",
  },

  // Completed section
  completedHeader: {
    flexDirection: "row",
    alignItems: "center",
    paddingHorizontal: 16,
    paddingVertical: 14,
    borderBottomWidth: StyleSheet.hairlineWidth,
    gap: 8,
  },
  completedChevron: {
    fontSize: 10,
  },
  completedText: {
    fontSize: 14,
    fontWeight: "500",
  },
  clearText: {
    fontSize: 13,
  },

  // Empty state
  empty: {
    paddingTop: 80,
    alignItems: "center",
    paddingHorizontal: 32,
  },
  emptyTitle: {
    fontSize: 16,
    fontWeight: "600",
    marginBottom: 8,
  },
  emptySubtitle: {
    fontSize: 14,
    textAlign: "center",
    lineHeight: 20,
  },

  // FAB
  fab: {
    position: "absolute",
    alignSelf: "center",
    width: 56,
    height: 56,
    borderRadius: 16,
    alignItems: "center",
    justifyContent: "center",
    ...Platform.select({
      ios: {
        shadowColor: "#000",
        shadowOffset: { width: 0, height: 2 },
        shadowOpacity: 0.25,
        shadowRadius: 8,
      },
      android: {
        elevation: 6,
      },
    }),
    borderWidth: StyleSheet.hairlineWidth,
  },
  fabIcon: {
    fontSize: 28,
    fontWeight: "300",
  },
});
