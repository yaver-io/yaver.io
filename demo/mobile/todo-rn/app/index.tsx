import { useCallback, useEffect, useMemo, useState } from "react";
import {
  FlatList,
  KeyboardAvoidingView,
  Platform,
  Pressable,
  StyleSheet,
  Text,
  TextInput,
  View,
} from "react-native";
import AsyncStorage from "@react-native-async-storage/async-storage";
import { SafeAreaView } from "react-native-safe-area-context";

type Todo = {
  id: string;
  text: string;
  done: boolean;
  createdAt: number;
};

type Filter = "all" | "active" | "completed";

const STORAGE_KEY = "@todo-rn/items/v1";

export default function TodoScreen() {
  const [items, setItems] = useState<Todo[]>([]);
  const [draft, setDraft] = useState("");
  const [filter, setFilter] = useState<Filter>("all");
  const [hydrated, setHydrated] = useState(false);

  // Load on mount. AsyncStorage so the demo state survives
  // hot-reload — without persistence the video shoot has to re-add
  // todos every take.
  useEffect(() => {
    AsyncStorage.getItem(STORAGE_KEY)
      .then((raw) => {
        if (!raw) return;
        try {
          const parsed = JSON.parse(raw);
          if (Array.isArray(parsed)) setItems(parsed as Todo[]);
        } catch {}
      })
      .finally(() => setHydrated(true));
  }, []);

  // Persist after hydration so we don't clobber storage with the
  // initial empty state on first render.
  useEffect(() => {
    if (!hydrated) return;
    AsyncStorage.setItem(STORAGE_KEY, JSON.stringify(items)).catch(() => {});
  }, [items, hydrated]);

  const visible = useMemo(() => {
    switch (filter) {
      case "active":
        return items.filter((t) => !t.done);
      case "completed":
        return items.filter((t) => t.done);
      default:
        return items;
    }
  }, [items, filter]);

  const remaining = items.filter((t) => !t.done).length;

  const add = useCallback(() => {
    const text = draft.trim();
    if (!text) return;
    setItems((prev) => [
      { id: `${Date.now()}-${Math.random().toString(36).slice(2, 8)}`, text, done: false, createdAt: Date.now() },
      ...prev,
    ]);
    setDraft("");
  }, [draft]);

  const toggle = useCallback((id: string) => {
    setItems((prev) => prev.map((t) => (t.id === id ? { ...t, done: !t.done } : t)));
  }, []);

  const remove = useCallback((id: string) => {
    setItems((prev) => prev.filter((t) => t.id !== id));
  }, []);

  const clearCompleted = useCallback(() => {
    setItems((prev) => prev.filter((t) => !t.done));
  }, []);

  return (
    <SafeAreaView style={s.safe} edges={["top", "bottom"]}>
      <KeyboardAvoidingView
        behavior={Platform.OS === "ios" ? "padding" : undefined}
        style={s.flex}
      >
        <View style={s.header}>
          <Text style={s.title}>Todo</Text>
          <Text style={s.subtitle}>
            {remaining === 0 ? "All clear" : `${remaining} left`}
          </Text>
        </View>

        <View style={s.composer}>
          <TextInput
            style={s.input}
            value={draft}
            onChangeText={setDraft}
            placeholder="What needs doing?"
            placeholderTextColor="#64748b"
            returnKeyType="done"
            onSubmitEditing={add}
            autoCorrect={false}
            autoCapitalize="sentences"
          />
          <Pressable
            onPress={add}
            disabled={!draft.trim()}
            style={({ pressed }) => [
              s.addBtn,
              !draft.trim() && s.addBtnDisabled,
              pressed && s.addBtnPressed,
            ]}
          >
            <Text style={s.addBtnText}>Add</Text>
          </Pressable>
        </View>

        <View style={s.filterRow}>
          {(["all", "active", "completed"] as Filter[]).map((f) => (
            <Pressable
              key={f}
              onPress={() => setFilter(f)}
              style={[s.filterChip, filter === f && s.filterChipOn]}
            >
              <Text style={[s.filterChipText, filter === f && s.filterChipTextOn]}>
                {f[0].toUpperCase() + f.slice(1)}
              </Text>
            </Pressable>
          ))}
          <View style={s.flex} />
          {items.some((t) => t.done) ? (
            <Pressable onPress={clearCompleted} style={s.clearBtn}>
              <Text style={s.clearBtnText}>Clear done</Text>
            </Pressable>
          ) : null}
        </View>

        <FlatList
          data={visible}
          keyExtractor={(t) => t.id}
          contentContainerStyle={s.listContent}
          ListEmptyComponent={
            <View style={s.empty}>
              <Text style={s.emptyTitle}>
                {filter === "completed" ? "Nothing done yet" : "Nothing here"}
              </Text>
              <Text style={s.emptyHint}>
                {filter === "completed"
                  ? "Tick a todo to see it here."
                  : "Add your first todo above."}
              </Text>
            </View>
          }
          renderItem={({ item }) => (
            <Pressable onPress={() => toggle(item.id)} style={s.item}>
              <View style={[s.checkbox, item.done && s.checkboxOn]}>
                {item.done ? <Text style={s.checkmark}>✓</Text> : null}
              </View>
              <Text style={[s.itemText, item.done && s.itemTextDone]}>
                {item.text}
              </Text>
              <Pressable
                onPress={() => remove(item.id)}
                hitSlop={10}
                style={s.removeBtn}
              >
                <Text style={s.removeBtnText}>×</Text>
              </Pressable>
            </Pressable>
          )}
        />
      </KeyboardAvoidingView>
    </SafeAreaView>
  );
}

const s = StyleSheet.create({
  safe: { flex: 1, backgroundColor: "#0f172a" },
  flex: { flex: 1 },
  header: {
    paddingHorizontal: 24,
    paddingTop: 16,
    paddingBottom: 12,
  },
  title: { color: "#f8fafc", fontSize: 32, fontWeight: "800", letterSpacing: -0.5 },
  subtitle: { color: "#94a3b8", fontSize: 14, marginTop: 4 },
  composer: {
    flexDirection: "row",
    alignItems: "center",
    paddingHorizontal: 16,
    paddingVertical: 8,
    gap: 8,
  },
  input: {
    flex: 1,
    backgroundColor: "#1e293b",
    color: "#f8fafc",
    paddingHorizontal: 16,
    paddingVertical: 14,
    borderRadius: 12,
    fontSize: 16,
    borderWidth: StyleSheet.hairlineWidth,
    borderColor: "#334155",
  },
  addBtn: {
    paddingHorizontal: 18,
    paddingVertical: 14,
    borderRadius: 12,
    backgroundColor: "#22c55e",
    minWidth: 72,
    alignItems: "center",
  },
  addBtnDisabled: { backgroundColor: "#1e293b" },
  addBtnPressed: { opacity: 0.85 },
  addBtnText: { color: "#0f172a", fontWeight: "700", fontSize: 15 },
  filterRow: {
    flexDirection: "row",
    alignItems: "center",
    paddingHorizontal: 16,
    paddingVertical: 10,
    gap: 8,
  },
  filterChip: {
    paddingHorizontal: 12,
    paddingVertical: 6,
    borderRadius: 999,
    borderWidth: StyleSheet.hairlineWidth,
    borderColor: "#334155",
  },
  filterChipOn: { backgroundColor: "#22c55e22", borderColor: "#22c55e" },
  filterChipText: { color: "#94a3b8", fontSize: 13, fontWeight: "600" },
  filterChipTextOn: { color: "#22c55e" },
  clearBtn: {
    paddingHorizontal: 12,
    paddingVertical: 6,
    borderRadius: 999,
    borderWidth: StyleSheet.hairlineWidth,
    borderColor: "#ef444466",
    backgroundColor: "#ef444411",
  },
  clearBtnText: { color: "#f87171", fontSize: 13, fontWeight: "600" },
  listContent: { paddingHorizontal: 16, paddingBottom: 32 },
  item: {
    flexDirection: "row",
    alignItems: "center",
    backgroundColor: "#1e293b",
    paddingHorizontal: 14,
    paddingVertical: 14,
    borderRadius: 12,
    marginBottom: 8,
    gap: 12,
  },
  checkbox: {
    width: 22,
    height: 22,
    borderRadius: 6,
    borderWidth: 1.5,
    borderColor: "#475569",
    alignItems: "center",
    justifyContent: "center",
  },
  checkboxOn: { backgroundColor: "#22c55e", borderColor: "#22c55e" },
  checkmark: { color: "#0f172a", fontSize: 14, fontWeight: "900" },
  itemText: { color: "#f8fafc", fontSize: 16, flex: 1 },
  itemTextDone: { color: "#64748b", textDecorationLine: "line-through" },
  removeBtn: { paddingHorizontal: 8 },
  removeBtnText: { color: "#64748b", fontSize: 22, fontWeight: "300" },
  empty: {
    paddingTop: 80,
    alignItems: "center",
    paddingHorizontal: 32,
  },
  emptyTitle: { color: "#f8fafc", fontSize: 18, fontWeight: "700", marginBottom: 6 },
  emptyHint: { color: "#94a3b8", fontSize: 14, textAlign: "center" },
});
