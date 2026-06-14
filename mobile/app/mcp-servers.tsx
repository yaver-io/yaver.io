// Custom MCP Servers — add your own private MCPs (e.g. a yaver-bet on Hetzner) or
// anyone's public MCP, and use their tools from Yaver. CRUD against the agent's
// /mcp/servers registry (see src/lib/mcpServers.ts). Routed at /mcp-servers.
import React, { useCallback, useEffect, useState } from "react";
import {
  ActivityIndicator,
  Alert,
  KeyboardAvoidingView,
  Platform,
  Pressable,
  RefreshControl,
  ScrollView,
  Switch,
  Text,
  TextInput,
  View,
} from "react-native";
import { Stack, useRouter } from "expo-router";
import { useColors } from "../src/context/ThemeContext";
import {
  deleteMcpServer,
  listMcpServers,
  saveMcpServer,
  testMcpServer,
  type McpServer,
} from "../src/lib/mcpServers";

export default function McpServersScreen() {
  const c = useColors();
  const router = useRouter();
  const [servers, setServers] = useState<McpServer[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  // add/edit form
  const [editing, setEditing] = useState(false);
  const [origName, setOrigName] = useState<string | null>(null);
  const [name, setName] = useState("");
  const [url, setUrl] = useState("");
  const [token, setToken] = useState("");
  const [enabled, setEnabled] = useState(true);
  const [busy, setBusy] = useState(false);
  const [testMsg, setTestMsg] = useState<string | null>(null);

  const load = useCallback(async () => {
    setError(null);
    try {
      setServers(await listMcpServers());
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    load();
  }, [load]);

  const resetForm = () => {
    setEditing(false);
    setOrigName(null);
    setName("");
    setUrl("");
    setToken("");
    setEnabled(true);
    setTestMsg(null);
  };

  const openAdd = () => {
    resetForm();
    setEditing(true);
  };

  const openEdit = (s: McpServer) => {
    setOrigName(s.name);
    setName(s.name);
    setUrl(s.url);
    setToken("");
    setEnabled(s.enabled);
    setTestMsg(s.hasAuth ? "auth token kept (leave blank to keep)" : null);
    setEditing(true);
  };

  const onTest = async () => {
    if (!url.trim()) return;
    setBusy(true);
    setTestMsg("testing…");
    try {
      const r = await testMcpServer({ name: name.trim(), url: url.trim(), auth_token: token || undefined });
      setTestMsg(r.ok ? `ok — ${r.toolCount ?? 0} tools` : `failed: ${r.error ?? "unreachable"}`);
    } catch (e) {
      setTestMsg(`failed: ${e instanceof Error ? e.message : String(e)}`);
    } finally {
      setBusy(false);
    }
  };

  const onSave = async () => {
    if (!name.trim() || !url.trim()) {
      Alert.alert("Missing fields", "Name and URL are required.");
      return;
    }
    setBusy(true);
    try {
      // rename = delete old then upsert new
      if (origName && origName !== name.trim()) {
        await deleteMcpServer(origName);
      }
      await saveMcpServer({
        name: name.trim(),
        url: url.trim(),
        auth_token: token || undefined,
        enabled,
      });
      resetForm();
      await load();
    } catch (e) {
      Alert.alert("Save failed", e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  };

  const onToggle = async (s: McpServer) => {
    try {
      await saveMcpServer({ name: s.name, url: s.url, enabled: !s.enabled });
      await load();
    } catch (e) {
      Alert.alert("Update failed", e instanceof Error ? e.message : String(e));
    }
  };

  const onDelete = (s: McpServer) => {
    Alert.alert("Remove server", `Remove "${s.name}"?`, [
      { text: "Cancel", style: "cancel" },
      {
        text: "Remove",
        style: "destructive",
        onPress: async () => {
          try {
            await deleteMcpServer(s.name);
            await load();
          } catch (e) {
            Alert.alert("Delete failed", e instanceof Error ? e.message : String(e));
          }
        },
      },
    ]);
  };

  const inputStyle = {
    backgroundColor: c.bgCard,
    borderColor: c.border,
    borderWidth: 1,
    borderRadius: 8,
    color: c.textPrimary,
    paddingHorizontal: 12,
    paddingVertical: 10,
    fontSize: 15,
  } as const;

  const btn = (bg: string) =>
    ({ paddingVertical: 8, paddingHorizontal: 14, borderRadius: 8, backgroundColor: bg }) as const;

  return (
    <View style={{ flex: 1, backgroundColor: c.bg }}>
      <Stack.Screen options={{ title: "MCP Servers", headerBackTitle: "Back" }} />
      <KeyboardAvoidingView style={{ flex: 1 }} behavior={Platform.OS === "ios" ? "padding" : undefined}>
        <ScrollView
          contentContainerStyle={{ padding: 16, gap: 12 }}
          refreshControl={<RefreshControl refreshing={loading} onRefresh={load} tintColor={c.accent} />}
        >
          <Text style={{ color: c.textMuted, fontSize: 13, lineHeight: 18 }}>
            Connect any remote MCP server — your own private ones or someone else's public ones. Their tools become
            usable from Yaver, namespaced as <Text style={{ color: c.textPrimary }}>name__tool</Text>.
          </Text>

          {error && (
            <View style={{ backgroundColor: c.errorBg, padding: 12, borderRadius: 8 }}>
              <Text style={{ color: c.error, fontSize: 13 }}>{error}</Text>
            </View>
          )}

          {/* form */}
          {editing ? (
            <View style={{ backgroundColor: c.bgCard, borderColor: c.border, borderWidth: 1, borderRadius: 12, padding: 14, gap: 10 }}>
              <Text style={{ color: c.textPrimary, fontWeight: "600", fontSize: 15 }}>
                {origName ? "Edit server" : "Add server"}
              </Text>
              <TextInput style={inputStyle} placeholder="Name (e.g. yaverbet)" placeholderTextColor={c.textMuted} autoCapitalize="none" value={name} onChangeText={setName} />
              <TextInput style={inputStyle} placeholder="URL (https://host/mcp)" placeholderTextColor={c.textMuted} autoCapitalize="none" keyboardType="url" value={url} onChangeText={setUrl} />
              <TextInput style={inputStyle} placeholder="Bearer token (optional)" placeholderTextColor={c.textMuted} autoCapitalize="none" secureTextEntry value={token} onChangeText={setToken} />
              <View style={{ flexDirection: "row", alignItems: "center", justifyContent: "space-between" }}>
                <Text style={{ color: c.textPrimary, fontSize: 14 }}>Enabled</Text>
                <Switch value={enabled} onValueChange={setEnabled} />
              </View>
              {testMsg && <Text style={{ color: c.textMuted, fontSize: 12 }}>{testMsg}</Text>}
              <View style={{ flexDirection: "row", gap: 8, marginTop: 4 }}>
                <Pressable style={btn(c.bgCard)} onPress={onTest} disabled={busy}>
                  <Text style={{ color: c.accent, fontSize: 14, fontWeight: "600" }}>Test</Text>
                </Pressable>
                <Pressable style={[btn(c.accent), { flex: 1, alignItems: "center" }]} onPress={onSave} disabled={busy}>
                  <Text style={{ color: "#fff", fontSize: 14, fontWeight: "600" }}>{busy ? "Saving…" : "Save"}</Text>
                </Pressable>
                <Pressable style={btn(c.bgCard)} onPress={resetForm} disabled={busy}>
                  <Text style={{ color: c.textMuted, fontSize: 14 }}>Cancel</Text>
                </Pressable>
              </View>
            </View>
          ) : (
            <Pressable style={[btn(c.accent), { alignItems: "center" }]} onPress={openAdd}>
              <Text style={{ color: "#fff", fontSize: 15, fontWeight: "600" }}>+ Add MCP Server</Text>
            </Pressable>
          )}

          {/* list */}
          {loading && servers.length === 0 ? (
            <ActivityIndicator color={c.accent} style={{ marginTop: 24 }} />
          ) : servers.length === 0 ? (
            <Text style={{ color: c.textMuted, fontSize: 13, textAlign: "center", marginTop: 24 }}>
              No MCP servers yet.
            </Text>
          ) : (
            servers.map((s) => (
              <View key={s.name} style={{ backgroundColor: c.bgCard, borderColor: c.border, borderWidth: 1, borderRadius: 12, padding: 14 }}>
                <View style={{ flexDirection: "row", alignItems: "center", justifyContent: "space-between" }}>
                  <View style={{ flex: 1 }}>
                    <Text style={{ color: c.textPrimary, fontSize: 15, fontWeight: "600" }}>{s.name}</Text>
                    <Text style={{ color: c.textMuted, fontSize: 12, marginTop: 2 }} numberOfLines={1}>
                      {s.url}
                    </Text>
                    <Text style={{ color: c.textMuted, fontSize: 11, marginTop: 2 }}>
                      {(s.toolCount ?? 0)} tools{s.hasAuth ? " · auth" : ""}
                    </Text>
                  </View>
                  <Switch value={s.enabled} onValueChange={() => onToggle(s)} />
                </View>
                <View style={{ flexDirection: "row", gap: 8, marginTop: 10 }}>
                  <Pressable style={btn(c.bgCard)} onPress={() => openEdit(s)}>
                    <Text style={{ color: c.accent, fontSize: 13 }}>Edit</Text>
                  </Pressable>
                  <Pressable style={btn(c.errorBg)} onPress={() => onDelete(s)}>
                    <Text style={{ color: c.error, fontSize: 13 }}>Remove</Text>
                  </Pressable>
                </View>
              </View>
            ))
          )}
        </ScrollView>
      </KeyboardAvoidingView>
    </View>
  );
}
