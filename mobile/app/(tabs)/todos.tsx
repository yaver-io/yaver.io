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
    <Animated.View style={[s.checkbox, { borderColor: "#1a73e8", backgroundColor: "#1a73e8", opacity: anim }]}>
      <View style={s.pulseInner} />
    </Animated.View>
  );
}

export default function TodosScreen() {
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

  const renderPendingItem = ({ item }: { item: Todo }) => (
    <Pressable
      style={s.row}
      onLongPress={() => handleDelete(item.id)}
      delayLongPress={500}
    >
      <Pressable style={s.checkArea} onPress={() => handleToggle(item.id)}>
        {item.agentStatus === "implementing" ? (
          <PulsingCircle />
        ) : (
          <View style={[s.checkbox, { borderColor: "#dadce0" }]} />
        )}
      </Pressable>
      <Text style={s.rowTitle} numberOfLines={2}>{item.title}</Text>
      {!autopilot && isConnected && (
        <Pressable style={s.playBtn} onPress={() => handleRunOne(item)}>
          <Text style={s.playIcon}>{"\u25B6"}</Text>
        </Pressable>
      )}
    </Pressable>
  );

  const renderDoneItem = ({ item }: { item: Todo }) => (
    <Pressable
      style={s.row}
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
            ? { borderColor: "#d93025", backgroundColor: "#d93025" }
            : { borderColor: "#1a73e8", backgroundColor: "#1a73e8" },
        ]}>
          {item.agentStatus === "failed" ? (
            <Text style={s.checkmark}>!</Text>
          ) : (
            <Text style={s.checkmark}>{"\u2713"}</Text>
          )}
        </View>
      </Pressable>
      <Text style={[s.rowTitle, s.rowTitleDone]} numberOfLines={2}>{item.title}</Text>
      {item.taskId && <Text style={s.chevron}>{"\u203A"}</Text>}
    </Pressable>
  );

  return (
    <View style={s.container}>
      {/* Header */}
      <View style={[s.header, { paddingTop: insets.top + 12 }]}>
        <Text style={s.headerTitle}>My Tasks</Text>
        <View style={s.headerRight}>
          {isConnected && pending.length > 0 && (
            <Pressable
              style={[s.autopilotBtn, autopilot && s.autopilotBtnActive, sending && { opacity: 0.5 }]}
              onPress={handleAutopilot}
              disabled={sending}
            >
              <Text style={[s.autopilotIcon, autopilot && s.autopilotIconActive]}>
                {autopilot ? "\u26A1" : "\u26A1"}
              </Text>
              <Text style={[s.autopilotText, autopilot && s.autopilotTextActive]}>
                {autopilot ? "Driving" : "Auto-Drive"}
              </Text>
            </Pressable>
          )}
        </View>
      </View>

      {/* Inline input */}
      {showInput && (
        <View style={s.inputRow}>
          <View style={[s.checkbox, { borderColor: "#dadce0" }]} />
          <TextInput
            ref={inputRef}
            style={s.inputText}
            placeholder="New task"
            placeholderTextColor="#9aa0a6"
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
              <Text style={s.emptyTitle}>No tasks yet</Text>
              <Text style={s.emptySubtitle}>
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
                  style={s.completedHeader}
                  onPress={() => setShowCompleted(!showCompleted)}
                >
                  <Text style={s.completedChevron}>
                    {showCompleted ? "\u25BC" : "\u25B6"}
                  </Text>
                  <Text style={s.completedText}>Completed ({done.length})</Text>
                  <View style={{ flex: 1 }} />
                  <Pressable onPress={handleClearDone} hitSlop={8}>
                    <Text style={s.clearText}>Clear</Text>
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
        style={[s.fab, { bottom: insets.bottom + 16 }]}
        onPress={handleFAB}
      >
        <Text style={s.fabIcon}>+</Text>
      </Pressable>
    </View>
  );
}

const s = StyleSheet.create({
  container: {
    flex: 1,
    backgroundColor: "#ffffff",
  },

  // Header
  header: {
    flexDirection: "row",
    alignItems: "center",
    justifyContent: "space-between",
    paddingHorizontal: 16,
    paddingBottom: 12,
    borderBottomWidth: StyleSheet.hairlineWidth,
    borderBottomColor: "#e0e0e0",
  },
  headerTitle: {
    fontSize: 22,
    fontWeight: "600",
    color: "#202124",
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
    backgroundColor: "#f1f3f4",
    gap: 4,
  },
  autopilotBtnActive: {
    backgroundColor: "#e8f0fe",
  },
  autopilotIcon: {
    fontSize: 13,
  },
  autopilotIconActive: {
    fontSize: 13,
  },
  autopilotText: {
    fontSize: 13,
    fontWeight: "500",
    color: "#5f6368",
  },
  autopilotTextActive: {
    color: "#1a73e8",
    fontWeight: "600",
  },

  // Input row
  inputRow: {
    flexDirection: "row",
    alignItems: "center",
    paddingHorizontal: 16,
    paddingVertical: 14,
    borderBottomWidth: StyleSheet.hairlineWidth,
    borderBottomColor: "#e0e0e0",
    gap: 14,
  },
  inputText: {
    flex: 1,
    fontSize: 16,
    color: "#202124",
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
    borderBottomColor: "#e0e0e0",
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
    color: "#202124",
    fontWeight: "400",
  },
  rowTitleDone: {
    textDecorationLine: "line-through" as const,
    color: "#9aa0a6",
  },
  playBtn: {
    width: 30,
    height: 30,
    borderRadius: 15,
    alignItems: "center",
    justifyContent: "center",
    backgroundColor: "#f1f3f4",
  },
  playIcon: {
    fontSize: 10,
    color: "#5f6368",
  },
  chevron: {
    fontSize: 20,
    color: "#dadce0",
    fontWeight: "300",
  },

  // Completed section
  completedHeader: {
    flexDirection: "row",
    alignItems: "center",
    paddingHorizontal: 16,
    paddingVertical: 14,
    borderBottomWidth: StyleSheet.hairlineWidth,
    borderBottomColor: "#e0e0e0",
    gap: 8,
  },
  completedChevron: {
    fontSize: 10,
    color: "#5f6368",
  },
  completedText: {
    fontSize: 14,
    fontWeight: "500",
    color: "#5f6368",
  },
  clearText: {
    fontSize: 13,
    color: "#1a73e8",
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
    color: "#202124",
    marginBottom: 8,
  },
  emptySubtitle: {
    fontSize: 14,
    textAlign: "center",
    lineHeight: 20,
    color: "#9aa0a6",
  },

  // FAB
  fab: {
    position: "absolute",
    alignSelf: "center",
    width: 56,
    height: 56,
    borderRadius: 16,
    backgroundColor: "#ffffff",
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
    borderColor: "#e0e0e0",
  },
  fabIcon: {
    fontSize: 28,
    color: "#1a73e8",
    fontWeight: "300",
  },
});
