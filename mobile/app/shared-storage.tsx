import React, { useCallback, useEffect, useState } from "react";
import {
  ActivityIndicator,
  FlatList,
  Pressable,
  RefreshControl,
  ScrollView,
  StyleSheet,
  Text,
  TextInput,
  View,
} from "react-native";
import { useRouter } from "expo-router";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { useColors } from "../src/context/ThemeContext";
import { quicClient } from "../src/lib/quic";

type SharedProfile = {
  id: string;
  name: string;
  type: string;
  available: boolean;
  status?: string;
  resolvedLocation?: string;
};

type SharedEntry = {
  name: string;
  path: string;
  isDir: boolean;
  size: number;
  mtime?: number;
};

type SharedHit = {
  profileId: string;
  profileName: string;
  path: string;
  matchType: string;
  snippet?: string;
  size?: number;
};

export default function SharedStorageScreen() {
  const c = useColors();
  const router = useRouter();
  const insets = useSafeAreaInsets();
  const [profiles, setProfiles] = useState<SharedProfile[]>([]);
  const [currentProfile, setCurrentProfile] = useState<SharedProfile | null>(null);
  const [currentPath, setCurrentPath] = useState("");
  const [entries, setEntries] = useState<SharedEntry[]>([]);
  const [query, setQuery] = useState("");
  const [hits, setHits] = useState<SharedHit[]>([]);
  const [fileName, setFileName] = useState("");
  const [fileContent, setFileContent] = useState<string | null>(null);
  const [binary, setBinary] = useState(false);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const loadProfiles = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const res = await fetch(`${quicClient.baseUrl}/shared-storage/profiles`, {
        headers: quicClient.getAuthHeaders(),
      });
      if (!res.ok) throw new Error(`HTTP ${res.status}`);
      const data = await res.json();
      setProfiles(data.profiles || []);
    } catch (e: any) {
      setError(e?.message ?? "failed to load profiles");
    } finally {
      setLoading(false);
    }
  }, []);

  const openDirectory = useCallback(async (profile: SharedProfile, path: string) => {
    setLoading(true);
    setError(null);
    setFileContent(null);
    setBinary(false);
    setHits([]);
    try {
      const url = `${quicClient.baseUrl}/shared-storage/list?id=${encodeURIComponent(profile.id)}&path=${encodeURIComponent(path)}`;
      const res = await fetch(url, { headers: quicClient.getAuthHeaders() });
      if (!res.ok) throw new Error(`HTTP ${res.status}`);
      const data = await res.json();
      setCurrentProfile(profile);
      setCurrentPath(path);
      setEntries(data.entries || []);
    } catch (e: any) {
      setError(e?.message ?? "failed to list");
    } finally {
      setLoading(false);
    }
  }, []);

  const openFile = useCallback(async (profile: SharedProfile, path: string) => {
    setLoading(true);
    setError(null);
    setFileName(path.split("/").pop() || path);
    try {
      const url = `${quicClient.baseUrl}/shared-storage/read?id=${encodeURIComponent(profile.id)}&path=${encodeURIComponent(path)}`;
      const res = await fetch(url, { headers: quicClient.getAuthHeaders() });
      if (!res.ok) throw new Error(`HTTP ${res.status}`);
      const data = await res.json();
      if (data.binary) {
        setBinary(true);
        setFileContent(null);
      } else {
        setBinary(false);
        setFileContent(data.content ?? "");
      }
    } catch (e: any) {
      setError(e?.message ?? "failed to read");
    } finally {
      setLoading(false);
    }
  }, []);

  const runSearch = useCallback(async () => {
    if (!currentProfile || !query.trim()) {
      setHits([]);
      return;
    }
    setLoading(true);
    setError(null);
    try {
      const url = `${quicClient.baseUrl}/shared-storage/search?id=${encodeURIComponent(currentProfile.id)}&path=${encodeURIComponent(currentPath)}&q=${encodeURIComponent(query.trim())}&limit=30`;
      const res = await fetch(url, { headers: quicClient.getAuthHeaders() });
      if (!res.ok) throw new Error(`HTTP ${res.status}`);
      const data = await res.json();
      setHits(data.hits || []);
    } catch (e: any) {
      setError(e?.message ?? "failed to search");
    } finally {
      setLoading(false);
    }
  }, [currentProfile, currentPath, query]);

  const goUp = useCallback(() => {
    if (fileContent != null || binary) {
      setFileContent(null);
      setBinary(false);
      return;
    }
    if (!currentProfile) return;
    const parent = currentPath.split("/").slice(0, -1).join("/");
    if (currentPath === "") {
      setCurrentProfile(null);
      setEntries([]);
      setHits([]);
      return;
    }
    openDirectory(currentProfile, parent);
  }, [binary, currentPath, currentProfile, fileContent, openDirectory]);

  useEffect(() => {
    loadProfiles();
  }, [loadProfiles]);

  return (
    <View style={[styles.container, { backgroundColor: c.bg }]}>
      <View style={[styles.header, { borderBottomColor: c.border, paddingTop: insets.top + 12 }]}>
        <Pressable onPress={() => router.back()} style={{ paddingVertical: 8 }}>
          <Text style={{ color: c.accent, fontSize: 15, fontWeight: "600" }}>{"\u2039"} Back</Text>
        </Pressable>
        <Text style={{ fontSize: 17, fontWeight: "700", color: c.textPrimary }}>Shared Storage</Text>
        <Pressable onPress={() => { setCurrentProfile(null); setCurrentPath(""); setEntries([]); setHits([]); setFileContent(null); setBinary(false); }} style={{ paddingVertical: 8 }}>
          <Text style={{ color: c.accent, fontSize: 15, fontWeight: "600" }}>Home</Text>
        </Pressable>
      </View>

      {currentProfile ? (
        <>
          <View style={[styles.crumbs, { borderBottomColor: c.border, backgroundColor: c.bgCard }]}>
            <Pressable onPress={goUp} style={styles.upBtn}>
              <Text style={[styles.upBtnIcon, { color: c.accent }]}>{"\u2190"}</Text>
            </Pressable>
            <View style={{ flex: 1 }}>
              <Text style={[styles.crumbProject, { color: c.textPrimary }]} numberOfLines={1}>{currentProfile.name}</Text>
              <Text style={[styles.crumbPath, { color: c.textMuted }]} numberOfLines={1}>{currentPath || "root"}</Text>
            </View>
          </View>

          <View style={[styles.searchRow, { borderBottomColor: c.border, backgroundColor: c.bgCard }]}>
            <TextInput
              value={query}
              onChangeText={setQuery}
              placeholder="Search documents in this tree"
              placeholderTextColor={c.textMuted}
              style={[styles.searchInput, { borderColor: c.border, color: c.textPrimary, backgroundColor: c.bg }]}
            />
            <Pressable onPress={runSearch} style={[styles.searchBtn, { backgroundColor: c.accent }]}>
              <Text style={{ color: "#fff", fontWeight: "700" }}>Go</Text>
            </Pressable>
          </View>
        </>
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
        <ScrollView contentContainerStyle={{ padding: 14, paddingBottom: 80 }}>
          <Text style={[styles.fileTitle, { color: c.textPrimary }]}>{fileName}</Text>
          <Text style={[styles.code, { color: c.textPrimary }]}>{fileContent}</Text>
        </ScrollView>
      ) : binary ? (
        <View style={styles.centered}>
          <Text style={{ fontSize: 48 }}>{"\u{1F4E6}"}</Text>
          <Text style={{ color: c.textMuted, marginTop: 12 }}>Binary file preview not available</Text>
        </View>
      ) : currentProfile ? (
        <FlatList
          data={hits.length > 0 ? hits : entries}
          keyExtractor={(item: any) => item.path}
          refreshControl={<RefreshControl refreshing={loading} onRefresh={() => openDirectory(currentProfile, currentPath)} tintColor={c.textMuted} />}
          renderItem={({ item }: { item: any }) => (
            <Pressable
              onPress={() => item.matchType ? openFile(currentProfile, item.path) : item.isDir ? openDirectory(currentProfile, item.path) : openFile(currentProfile, item.path)}
              style={({ pressed }) => [styles.row, { borderBottomColor: c.border, backgroundColor: pressed ? c.bgCard : "transparent" }]}
            >
              <View style={[styles.iconWrap, { backgroundColor: item.isDir ? "#818cf822" : c.bgCard }]}>
                <Text style={{ fontSize: 18 }}>{item.isDir ? "\u{1F4C1}" : "\u{1F4C4}"}</Text>
              </View>
              <View style={{ flex: 1, marginRight: 12 }}>
                <Text style={[styles.name, { color: c.textPrimary }]} numberOfLines={1}>{item.name || item.path.split("/").pop()}</Text>
                <Text style={[styles.meta, { color: c.textMuted }]} numberOfLines={2}>
                  {item.matchType ? `${item.matchType} \u00B7 ${item.path}${item.snippet ? ` \u00B7 ${item.snippet}` : ""}` : `${item.path}${item.size ? ` \u00B7 ${humanSize(item.size)}` : ""}`}
                </Text>
              </View>
              <Text style={[styles.chevron, { color: c.textMuted }]}>{"\u203A"}</Text>
            </Pressable>
          )}
          ListEmptyComponent={<View style={styles.centered}><Text style={{ color: c.textMuted }}>No files or matches</Text></View>}
        />
      ) : (
        <FlatList
          data={profiles}
          keyExtractor={(item) => item.id}
          refreshControl={<RefreshControl refreshing={loading} onRefresh={loadProfiles} tintColor={c.textMuted} />}
          contentContainerStyle={{ padding: 12 }}
          renderItem={({ item }) => (
            <Pressable
              onPress={() => openDirectory(item, "")}
              style={({ pressed }) => [styles.card, { backgroundColor: pressed ? c.bgCard : c.bgCard, borderColor: c.border }]}
            >
              <Text style={[styles.icon, { color: c.textMuted }]}>{item.type === "storagebox" ? "\u{1F5C4}" : item.type === "smb" ? "\u{1F4E1}" : item.type === "webdav" ? "\u{1F310}" : "\u{1F4C2}"}</Text>
              <View style={{ flex: 1 }}>
                <Text style={[styles.label, { color: c.textPrimary }]}>{item.name}</Text>
                <Text style={[styles.desc, { color: c.textMuted }]} numberOfLines={2}>{item.resolvedLocation || item.status || item.type}</Text>
              </View>
              <Text style={{ color: c.textMuted, fontSize: 16 }}>{"\u203A"}</Text>
            </Pressable>
          )}
          ListEmptyComponent={<View style={styles.centered}><Text style={{ color: c.textMuted }}>No shared storage profiles configured yet.</Text></View>}
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
    paddingHorizontal: 14,
    paddingVertical: 10,
    borderBottomWidth: 1,
    gap: 12,
  },
  searchRow: {
    flexDirection: "row",
    gap: 8,
    alignItems: "center",
    paddingHorizontal: 12,
    paddingVertical: 10,
    borderBottomWidth: 1,
  },
  searchInput: {
    flex: 1,
    borderWidth: 1,
    borderRadius: 10,
    paddingHorizontal: 12,
    paddingVertical: 10,
    fontSize: 14,
  },
  searchBtn: {
    borderRadius: 10,
    paddingHorizontal: 14,
    paddingVertical: 10,
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
  centered: { alignItems: "center", justifyContent: "center", paddingVertical: 60, flex: 1 },
  card: {
    flexDirection: "row",
    alignItems: "center",
    padding: 14,
    borderRadius: 14,
    borderWidth: 1,
    marginBottom: 10,
  },
  icon: { fontSize: 18, width: 28, textAlign: "center", marginRight: 10 },
  label: { fontSize: 16, fontWeight: "700" },
  desc: { fontSize: 11, marginTop: 2 },
  fileTitle: { fontSize: 15, fontWeight: "700", marginBottom: 10 },
  code: { fontFamily: "Menlo", fontSize: 12, lineHeight: 17 },
});
