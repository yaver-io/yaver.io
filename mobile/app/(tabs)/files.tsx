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
        <View style={[styles.crumbs, { borderBottomColor: c.border, backgroundColor: c.bgCard }]}>
          <Pressable onPress={up} style={styles.upBtn}>
            <Text style={[styles.upBtnIcon, { color: c.accent }]}>{"\u2190"}</Text>
          </Pressable>
          <View style={{ flex: 1 }}>
            <Text style={[styles.crumbProject, { color: c.textPrimary }]} numberOfLines={1}>
              {currentRoot.name}
            </Text>
            {currentPath ? (
              <Text style={[styles.crumbPath, { color: c.textMuted }]} numberOfLines={1}>
                {currentPath.split("/").join("  /  ")}
              </Text>
            ) : (
              <Text style={[styles.crumbPath, { color: c.textMuted }]}>root</Text>
            )}
          </View>
        </View>
      ) : null}

      {error ? (
        <View style={[styles.errorBar, { backgroundColor: "#fee2e2" }]}>
          <Text style={{ color: "#dc2626", fontSize: 13, fontWeight: "500" }}>{error}</Text>
        </View>
      ) : null}

      {loading ? (
        <View style={styles.centered}>
          <ActivityIndicator color={c.accent} />
        </View>
      ) : fileContent != null ? (
        <ScrollView
          style={styles.fileScroll}
          contentContainerStyle={{ padding: 14, paddingBottom: 80 }}
        >
          <Text style={[styles.code, { color: c.textPrimary }]}>{fileContent}</Text>
        </ScrollView>
      ) : binary ? (
        <View style={styles.centered}>
          <Text style={{ fontSize: 48 }}>{"\u{1F4E6}"}</Text>
          <Text style={[styles.emptyText, { color: c.textMuted, marginTop: 12 }]}>Binary file — cannot preview</Text>
        </View>
      ) : currentRoot ? (
        <FlatList
          data={[...entries].sort((a, b) => {
            if (a.isDir !== b.isDir) return a.isDir ? -1 : 1;
            return a.name.localeCompare(b.name);
          })}
          keyExtractor={(e) => e.path}
          refreshControl={
            <RefreshControl refreshing={loading} onRefresh={() => loadDirectory(currentRoot, currentPath)} tintColor={c.textMuted} />
          }
          contentContainerStyle={{ paddingVertical: 6 }}
          renderItem={({ item }) => (
            <Pressable
              onPress={() =>
                item.isDir
                  ? loadDirectory(currentRoot, item.path)
                  : openFile(currentRoot, item.path)
              }
              style={({ pressed }) => [
                styles.row,
                { borderBottomColor: c.border, backgroundColor: pressed ? c.bgCard : "transparent" },
              ]}
            >
              <View style={[styles.iconWrap, { backgroundColor: item.isDir ? "#818cf822" : c.bgCard }]}>
                <Text style={{ fontSize: 18 }}>{fileEmoji(item)}</Text>
              </View>
              <View style={{ flex: 1, marginRight: 12 }}>
                <Text style={[styles.name, { color: c.textPrimary }]} numberOfLines={1}>
                  {item.name}
                </Text>
                <Text style={[styles.meta, { color: c.textMuted }]} numberOfLines={1}>
                  {item.isDir ? "Folder" : humanSize(item.size)} {item.mtime ? `\u00B7 ${relativeTime(item.mtime)}` : ""}
                </Text>
              </View>
              <Text style={[styles.chevron, { color: c.textMuted }]}>{"\u203A"}</Text>
            </Pressable>
          )}
          ListEmptyComponent={
            <View style={styles.centered}>
              <Text style={{ fontSize: 42 }}>{"\u{1F4C2}"}</Text>
              <Text style={[styles.emptyText, { color: c.textMuted, marginTop: 10 }]}>Empty folder</Text>
            </View>
          }
        />
      ) : (
        <FlatList
          data={roots}
          keyExtractor={(r) => r.id}
          refreshControl={<RefreshControl refreshing={loading} onRefresh={loadRoots} tintColor={c.textMuted} />}
          contentContainerStyle={{ padding: 12 }}
          renderItem={({ item }) => (
            <Pressable
              onPress={() => loadDirectory(item, "")}
              style={({ pressed }) => [
                styles.projectCard,
                { backgroundColor: pressed ? c.bgCardElevated || c.bg : c.bgCard, borderColor: c.border },
              ]}
            >
              <View style={[styles.projectIcon, { backgroundColor: "#818cf822" }]}>
                <Text style={{ fontSize: 22 }}>{"\u{1F4C1}"}</Text>
              </View>
              <View style={{ flex: 1 }}>
                <Text style={[styles.projectName, { color: c.textPrimary }]} numberOfLines={1}>
                  {item.name}
                </Text>
                <Text style={[styles.projectPath, { color: c.textMuted }]} numberOfLines={1}>
                  {item.path}
                </Text>
              </View>
              <Text style={[styles.chevron, { color: c.textMuted }]}>{"\u203A"}</Text>
            </Pressable>
          )}
          ListEmptyComponent={
            <View style={styles.centered}>
              <Text style={{ fontSize: 42 }}>{"\u{1F50D}"}</Text>
              <Text style={[styles.emptyText, { color: c.textMuted, marginTop: 10, textAlign: "center", paddingHorizontal: 40 }]}>
                No projects discovered yet. The agent scans your home directory automatically.
              </Text>
            </View>
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

function relativeTime(ts: number): string {
  const now = Date.now() / 1000;
  const diff = now - ts;
  if (diff < 60) return "just now";
  if (diff < 3600) return `${Math.floor(diff / 60)}m ago`;
  if (diff < 86400) return `${Math.floor(diff / 3600)}h ago`;
  if (diff < 86400 * 7) return `${Math.floor(diff / 86400)}d ago`;
  const d = new Date(ts * 1000);
  return d.toLocaleDateString([], { month: "short", day: "numeric" });
}

function fileEmoji(item: FileEntry): string {
  if (item.isDir) return "\u{1F4C1}";
  const ext = item.name.toLowerCase().split(".").pop() || "";
  const map: Record<string, string> = {
    ts: "\u{1F4D8}", tsx: "\u{1F4D8}", js: "\u{1F4D9}", jsx: "\u{1F4D9}",
    json: "\u{1F4DC}", md: "\u{1F4DD}", yml: "\u2699", yaml: "\u2699",
    go: "\u{1F43A}", rs: "\u{1F980}", py: "\u{1F40D}", rb: "\u{1F48E}",
    swift: "\u{1F9A2}", kt: "\u{1F536}", java: "\u2615",
    sh: "\u{1F4BB}", env: "\u{1F510}", lock: "\u{1F512}",
    png: "\u{1F5BC}", jpg: "\u{1F5BC}", jpeg: "\u{1F5BC}", gif: "\u{1F5BC}", svg: "\u{1F5BC}",
    mp4: "\u{1F3AC}", mov: "\u{1F3AC}",
    zip: "\u{1F4E6}", tar: "\u{1F4E6}", gz: "\u{1F4E6}",
  };
  return map[ext] || "\u{1F4C4}";
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
    paddingHorizontal: 14,
    paddingVertical: 10,
    borderBottomWidth: 1,
    gap: 12,
  },
  upBtn: { width: 34, height: 34, borderRadius: 17, alignItems: "center", justifyContent: "center" },
  upBtnIcon: { fontSize: 20, fontWeight: "600" },
  crumbProject: { fontSize: 15, fontWeight: "700" },
  crumbPath: { fontSize: 11, marginTop: 2 },
  row: {
    flexDirection: "row",
    alignItems: "center",
    paddingHorizontal: 14,
    paddingVertical: 11,
    borderBottomWidth: StyleSheet.hairlineWidth,
  },
  iconWrap: {
    width: 36, height: 36, borderRadius: 10,
    alignItems: "center", justifyContent: "center",
    marginRight: 12,
  },
  name: { fontSize: 15, fontWeight: "500" },
  meta: { fontSize: 11, marginTop: 2 },
  chevron: { fontSize: 20, fontWeight: "300" },
  errorBar: { padding: 12 },
  emptyText: { fontSize: 14 },
  centered: { alignItems: "center", justifyContent: "center", paddingVertical: 60, flex: 1 },
  fileScroll: { flex: 1 },
  code: { fontFamily: "Menlo", fontSize: 12, lineHeight: 17 },
  projectCard: {
    flexDirection: "row",
    alignItems: "center",
    padding: 14,
    borderRadius: 14,
    borderWidth: 1,
    marginBottom: 10,
  },
  projectIcon: {
    width: 44, height: 44, borderRadius: 12,
    alignItems: "center", justifyContent: "center",
    marginRight: 12,
  },
  projectName: { fontSize: 16, fontWeight: "700" },
  projectPath: { fontSize: 11, marginTop: 2 },
});
