import React, { useCallback, useEffect, useState } from "react";
import {
  ActivityIndicator,
  FlatList,
  Pressable,
  RefreshControl,
  ScrollView,
  StyleSheet,
  Text,
  View,
} from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { useRouter } from "expo-router";
import { useColors } from "../../src/context/ThemeContext";
import { useDevice } from "../../src/context/DeviceContext";
import { quicClient } from "../../src/lib/quic";

// Read-only file browser for the "I want to peek at a repo from
// my couch" use case. Scoped server-side to the agent's
// discovered project roots — we can't escape the sandbox.

interface FileRoot {
  id: string;
  name: string;
  path: string;
}

interface FileEntry {
  name: string;
  path: string;
  isDir: boolean;
  size: number;
  mtime: number;
}

export default function FilesScreen() {
  const c = useColors();
  const insets = useSafeAreaInsets();
  const router = useRouter();
  const { connectionStatus } = useDevice();
  const connected = connectionStatus === "connected";

  const [roots, setRoots] = useState<FileRoot[]>([]);
  const [currentRoot, setCurrentRoot] = useState<FileRoot | null>(null);
  const [currentPath, setCurrentPath] = useState("");
  const [entries, setEntries] = useState<FileEntry[]>([]);
  const [fileContent, setFileContent] = useState<string | null>(null);
  const [binary, setBinary] = useState(false);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const loadRoots = useCallback(async () => {
    if (!connected) return;
    setLoading(true);
    setError(null);
    try {
      const res = await fetch(`${quicClient.baseUrl}/files/roots`, {
        headers: quicClient.getAuthHeaders(),
      });
      if (!res.ok) throw new Error(`HTTP ${res.status}`);
      const data = await res.json();
      setRoots(data.roots || []);
    } catch (e: any) {
      setError(e?.message ?? "failed to load roots");
    } finally {
      setLoading(false);
    }
  }, [connected]);

  const loadDirectory = useCallback(
    async (root: FileRoot, path: string) => {
      setLoading(true);
      setError(null);
      setFileContent(null);
      setBinary(false);
      try {
        const url = `${quicClient.baseUrl}/files/list?root=${encodeURIComponent(root.id)}&path=${encodeURIComponent(path)}`;
        const res = await fetch(url, { headers: quicClient.getAuthHeaders() });
        if (!res.ok) throw new Error(`HTTP ${res.status}`);
        const data = await res.json();
        setEntries(data.entries || []);
        setCurrentRoot(root);
        setCurrentPath(path);
      } catch (e: any) {
        setError(e?.message ?? "failed to list");
      } finally {
        setLoading(false);
      }
    },
    [],
  );

  const openFile = useCallback(async (root: FileRoot, path: string) => {
    setLoading(true);
    setError(null);
    try {
      const url = `${quicClient.baseUrl}/files/read?root=${encodeURIComponent(root.id)}&path=${encodeURIComponent(path)}`;
      const res = await fetch(url, { headers: quicClient.getAuthHeaders() });
      if (!res.ok) throw new Error(`HTTP ${res.status}`);
      const data = await res.json();
      if (data.binary) {
        setBinary(true);
        setFileContent(null);
      } else {
        setFileContent(data.content ?? "");
        setBinary(false);
      }
    } catch (e: any) {
      setError(e?.message ?? "failed to read");
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    loadRoots();
  }, [loadRoots]);

  const up = useCallback(() => {
    if (fileContent != null || binary) {
      setFileContent(null);
      setBinary(false);
      return;
    }
    if (currentPath === "") {
      setCurrentRoot(null);
      setEntries([]);
      return;
    }
    const parent = currentPath.split("/").slice(0, -1).join("/");
    if (currentRoot) loadDirectory(currentRoot, parent);
  }, [fileContent, binary, currentPath, currentRoot, loadDirectory]);

  const goHome = useCallback(() => {
    setCurrentRoot(null);
    setCurrentPath("");
    setEntries([]);
    setFileContent(null);
    setBinary(false);
  }, []);

  return (
    <View style={[styles.container, { backgroundColor: c.bg }]}>
      <View style={[styles.header, { borderBottomColor: c.border, paddingTop: insets.top + 12 }]}>
        <Pressable onPress={() => router.navigate("/(tabs)/more" as any)} style={{ paddingVertical: 8 }}>
          <Text style={{ color: c.accent, fontSize: 15, fontWeight: "600" }}>{"\u2039"} Back</Text>
        </Pressable>
        <Text style={{ fontSize: 17, fontWeight: "700", color: c.textPrimary }}>Files</Text>
        <Pressable onPress={goHome} style={{ paddingVertical: 8 }}>
          <Text style={{ color: c.accent, fontSize: 15, fontWeight: "600" }}>Home</Text>
        </Pressable>
      </View>

      {currentRoot ? (
        <View style={[styles.crumbs, { borderColor: c.border }]}>
          <Pressable onPress={up}>
            <Text style={[styles.crumb, { color: c.accent }]}>↑</Text>
          </Pressable>
          <Text style={[styles.crumbPath, { color: c.textMuted }]} numberOfLines={1}>
            {currentRoot.name}/{currentPath}
          </Text>
        </View>
      ) : null}

      {error ? (
        <View style={styles.errorBar}>
          <Text style={{ color: "#ef4444" }}>{error}</Text>
        </View>
      ) : null}

      {loading ? (
        <ActivityIndicator style={{ marginTop: 24 }} />
      ) : fileContent != null ? (
        <ScrollView
          style={styles.fileScroll}
          contentContainerStyle={{ padding: 12, paddingBottom: 80 }}
        >
          <Text style={[styles.code, { color: c.textPrimary }]}>{fileContent}</Text>
        </ScrollView>
      ) : binary ? (
        <View style={styles.centered}>
          <Text style={[styles.body, { color: c.textMuted }]}>(binary file — cannot preview)</Text>
        </View>
      ) : currentRoot ? (
        <FlatList
          data={entries}
          keyExtractor={(e) => e.path}
          refreshControl={
            <RefreshControl refreshing={loading} onRefresh={() => loadDirectory(currentRoot, currentPath)} />
          }
          renderItem={({ item }) => (
            <Pressable
              onPress={() =>
                item.isDir
                  ? loadDirectory(currentRoot, item.path)
                  : openFile(currentRoot, item.path)
              }
              style={[styles.row, { borderColor: c.border }]}
            >
              <Text style={[styles.name, { color: c.textPrimary }]} numberOfLines={1}>
                {item.isDir ? "📁" : "📄"} {item.name}
              </Text>
              {!item.isDir ? (
                <Text style={[styles.meta, { color: c.textMuted }]}>{humanSize(item.size)}</Text>
              ) : null}
            </Pressable>
          )}
          ListEmptyComponent={
            <Text style={[styles.body, { color: c.textMuted, padding: 24 }]}>(empty)</Text>
          }
        />
      ) : (
        <FlatList
          data={roots}
          keyExtractor={(r) => r.id}
          refreshControl={<RefreshControl refreshing={loading} onRefresh={loadRoots} />}
          renderItem={({ item }) => (
            <Pressable
              onPress={() => loadDirectory(item, "")}
              style={[styles.row, { borderColor: c.border }]}
            >
              <Text style={[styles.name, { color: c.textPrimary }]}>📁 {item.name}</Text>
              <Text style={[styles.meta, { color: c.textMuted }]} numberOfLines={1}>
                {item.path}
              </Text>
            </Pressable>
          )}
          ListEmptyComponent={
            <Text style={[styles.body, { color: c.textMuted, padding: 24 }]}>
              No project roots discovered. Run `yaver discover` on the agent.
            </Text>
          }
        />
      )}
    </View>
  );
}

function humanSize(n: number): string {
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`;
  if (n < 1024 * 1024 * 1024) return `${(n / 1024 / 1024).toFixed(1)} MB`;
  return `${(n / 1024 / 1024 / 1024).toFixed(1)} GB`;
}

const styles = StyleSheet.create({
  container: { flex: 1 },
  header: {
    flexDirection: "row",
    justifyContent: "space-between",
    alignItems: "center",
    paddingHorizontal: 16,
    paddingBottom: 12,
    borderBottomWidth: 1,
  },
  crumbs: {
    flexDirection: "row",
    alignItems: "center",
    paddingHorizontal: 16,
    paddingVertical: 8,
    borderBottomWidth: 1,
  },
  crumb: { fontSize: 18, marginRight: 12 },
  crumbPath: { flex: 1, fontSize: 13 },
  row: {
    flexDirection: "row",
    justifyContent: "space-between",
    alignItems: "center",
    paddingHorizontal: 16,
    paddingVertical: 14,
    borderBottomWidth: 1,
  },
  name: { fontSize: 15, flex: 1 },
  meta: { fontSize: 12, marginLeft: 12 },
  errorBar: { padding: 12, backgroundColor: "#fee2e2" },
  body: { fontSize: 14 },
  centered: { alignItems: "center", justifyContent: "center", flex: 1 },
  fileScroll: { flex: 1 },
  code: { fontFamily: "Menlo", fontSize: 12, lineHeight: 16 },
});
