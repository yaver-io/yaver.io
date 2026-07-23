import { useCallback, useEffect, useMemo, useState } from "react";
import { FlatList, Pressable, StyleSheet, Text, TextInput, View } from "react-native";
import { SafeAreaView } from "react-native-safe-area-context";
import { YaverServerlessTodoClient } from "../../../packages/js-client/src/index.js";

type Todo = { id: string; title: string; done: boolean; createdAt: string };

export default function App() {
  const [baseUrl, setBaseUrl] = useState("http://127.0.0.1:18080");
  const [slug, setSlug] = useState("yaver-serverless-todo");
  const [token, setToken] = useState("");
  const [draft, setDraft] = useState("");
  const [todos, setTodos] = useState<Todo[]>([]);
  const [error, setError] = useState("");
  const client = useMemo(() => new YaverServerlessTodoClient({ baseUrl, slug, token }), [baseUrl, slug, token]);

  const refresh = useCallback(async () => {
    try {
      setError("");
      setTodos(await client.listTodos());
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    }
  }, [client]);

  useEffect(() => {
    refresh();
  }, [refresh]);

  async function add() {
    const title = draft.trim();
    if (!title) return;
    setDraft("");
    await client.createTodo(title).then(refresh).catch((err: Error) => setError(err.message));
  }

  return (
    <SafeAreaView style={s.safe}>
      <View style={s.page}>
        <View style={s.header}>
          <Text style={s.title}>Yaver Serverless Todo</Text>
          <Pressable style={s.refresh} onPress={refresh}><Text style={s.refreshText}>Refresh</Text></Pressable>
        </View>
        <View style={s.settings}>
          <TextInput style={s.input} value={baseUrl} onChangeText={setBaseUrl} autoCapitalize="none" />
          <TextInput style={s.input} value={slug} onChangeText={setSlug} autoCapitalize="none" />
          <TextInput style={s.input} value={token} onChangeText={setToken} placeholder="pp_ project token" secureTextEntry autoCapitalize="none" />
        </View>
        {error ? <Text style={s.error}>{error}</Text> : null}
        <View style={s.composer}>
          <TextInput style={[s.input, s.draft]} value={draft} onChangeText={setDraft} placeholder="What needs doing?" onSubmitEditing={add} />
          <Pressable style={s.add} onPress={add}><Text style={s.addText}>Add</Text></Pressable>
        </View>
        <FlatList
          data={todos}
          keyExtractor={(item) => item.id}
          renderItem={({ item }) => (
            <View style={s.item}>
              <Pressable style={[s.check, item.done && s.checkOn]} onPress={() => client.setTodoDone(item.id, !item.done).then(refresh)}>
                <Text style={s.checkText}>{item.done ? "✓" : ""}</Text>
              </Pressable>
              <Text style={[s.itemText, item.done && s.done]}>{item.title}</Text>
              <Pressable onPress={() => client.deleteTodo(item.id).then(refresh)}>
                <Text style={s.delete}>Delete</Text>
              </Pressable>
            </View>
          )}
          ListEmptyComponent={<Text style={s.empty}>No serverless todos yet.</Text>}
        />
      </View>
    </SafeAreaView>
  );
}

const s = StyleSheet.create({
  safe: { flex: 1, backgroundColor: "#f7f8fb" },
  page: { flex: 1, padding: 18 },
  header: { flexDirection: "row", alignItems: "center", justifyContent: "space-between", marginBottom: 14 },
  title: { color: "#162033", fontSize: 26, fontWeight: "800", flex: 1 },
  refresh: { backgroundColor: "#0f8b8d", borderRadius: 8, paddingHorizontal: 14, paddingVertical: 10 },
  refreshText: { color: "white", fontWeight: "700" },
  settings: { gap: 8, marginBottom: 12 },
  input: { minHeight: 44, borderWidth: 1, borderColor: "#d9e0ea", borderRadius: 8, backgroundColor: "white", paddingHorizontal: 12 },
  error: { color: "#c2410c", marginBottom: 10 },
  composer: { flexDirection: "row", gap: 8, marginBottom: 14 },
  draft: { flex: 1 },
  add: { minWidth: 70, borderRadius: 8, backgroundColor: "#0f8b8d", alignItems: "center", justifyContent: "center" },
  addText: { color: "white", fontWeight: "800" },
  item: { minHeight: 56, flexDirection: "row", alignItems: "center", gap: 12, padding: 12, borderWidth: 1, borderColor: "#d9e0ea", borderRadius: 8, backgroundColor: "white", marginBottom: 8 },
  check: { width: 28, height: 28, borderRadius: 14, borderWidth: 1, borderColor: "#d9e0ea", alignItems: "center", justifyContent: "center" },
  checkOn: { backgroundColor: "#0f8b8d", borderColor: "#0f8b8d" },
  checkText: { color: "white", fontWeight: "900" },
  itemText: { flex: 1, color: "#162033", fontSize: 16 },
  done: { color: "#667085", textDecorationLine: "line-through" },
  delete: { color: "#c2410c", fontWeight: "700" },
  empty: { color: "#667085", padding: 24, textAlign: "center" },
});
