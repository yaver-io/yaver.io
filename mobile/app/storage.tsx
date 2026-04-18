import React, { useCallback, useEffect, useState } from "react";
import {
  ActivityIndicator,
  Alert,
  FlatList,
  Pressable,
  RefreshControl,
  ScrollView,
  StyleSheet,
  Text,
  View,
} from "react-native";
import * as Linking from "expo-linking";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { useRouter } from "expo-router";
import * as DocumentPicker from "expo-document-picker";
import Markdown from "react-native-markdown-display";
import { useColors } from "../src/context/ThemeContext";
import { useDevice } from "../src/context/DeviceContext";
import { quicClient } from "../src/lib/quic";
import { AppBackButton } from "../src/components/AppBackButton";

// Mobile parity with web StorageView — three sub-tabs over the three
// owner-local storage surfaces on the agent:
//
//   files   — read-only project browser       /files/*
//   shared  — mounted NAS / SMB / S3 / Azure  /shared-storage/*
//   blobs   — simple object store             /blobs/*
//
// Each tab talks straight to the agent over P2P. Nothing about what
// the user is storing (filenames, bytes, bucket lists) ever reaches
// Convex — it stays between this phone and the host.

type Tab = "files" | "shared" | "blobs";

export default function StorageScreen() {
  const c = useColors();
  const insets = useSafeAreaInsets();
  const router = useRouter();
  const [tab, setTab] = useState<Tab>("files");

  return (
    <View style={{ flex: 1, backgroundColor: c.bg, paddingTop: insets.top }}>
      <View style={[s.header, { borderColor: c.border }]}>
        <AppBackButton onPress={() => router.back()} />
        <Text style={[s.title, { color: c.textPrimary }]}>Storage</Text>
        <View style={{ width: 20 }} />
      </View>

      <View style={[s.tabBar, { borderColor: c.border }]}>
        {(["files", "shared", "blobs"] as Tab[]).map((t) => (
          <Pressable key={t} onPress={() => setTab(t)} style={{ flex: 1, paddingVertical: 10, alignItems: "center" }}>
            <Text
              style={{
                color: tab === t ? c.accent : c.textMuted,
                fontWeight: tab === t ? "700" : "400",
                fontSize: 13,
              }}
            >
              {t === "files" ? "Files" : t === "shared" ? "Shared" : "Blobs"}
            </Text>
            {tab === t ? (
              <View style={{ height: 2, width: "40%", backgroundColor: c.accent, marginTop: 4 }} />
            ) : null}
          </Pressable>
        ))}
      </View>

      {tab === "files" && <FilesTab />}
      {tab === "shared" && <SharedTab />}
      {tab === "blobs" && <BlobsTab />}
    </View>
  );
}

// ── Files ────────────────────────────────────────────────────────────

type FileRoot = { id: string; name: string; path: string };
type FileEntry = { name: string; path: string; isDir?: boolean; size?: number };
type FilePreview = { path: string; content: string };

function FilesTab() {
  const c = useColors();
  const { connectionStatus } = useDevice();
  const connected = connectionStatus === "connected";
  const [roots, setRoots] = useState<FileRoot[]>([]);
  const [activeRoot, setActiveRoot] = useState<string | null>(null);
  const [path, setPath] = useState("");
  const [entries, setEntries] = useState<FileEntry[]>([]);
  const [loading, setLoading] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const [preview, setPreview] = useState<FilePreview | null>(null);
  const [showRootOverview, setShowRootOverview] = useState(false);

  const loadRoots = useCallback(async () => {
    if (!connected) return;
    try {
      setErr(null);
      const data = (await quicClient.filesRoots()) as { roots: FileRoot[] };
      setRoots(data.roots || []);
      if ((data.roots || []).length > 0 && !activeRoot) setActiveRoot(data.roots[0].id);
    } catch (e: any) {
      setErr(e?.message ?? "failed");
    }
  }, [connected, activeRoot]);

  const loadDir = useCallback(async () => {
    if (showRootOverview) {
      setEntries(
        roots.map((root) => ({
          name: root.name,
          path: root.id,
          isDir: true,
        })),
      );
      setLoading(false);
      return;
    }
    if (!activeRoot) return;
    setLoading(true);
    try {
      const data: any = await quicClient.filesList(activeRoot, path);
      const raw: any[] = data.entries || data.files || [];
      setEntries(raw.map((e) => ({ name: e.name, path: e.path ?? e.name, isDir: Boolean(e.isDir || e.type === "dir"), size: e.size })));
    } catch (e: any) {
      setErr(e?.message ?? "failed");
    } finally {
      setLoading(false);
    }
  }, [activeRoot, path, roots, showRootOverview]);

  useEffect(() => {
    void loadRoots();
  }, [loadRoots]);
  useEffect(() => {
    void loadDir();
  }, [loadDir]);

  async function open(entry: FileEntry) {
    if (showRootOverview) {
      setActiveRoot(entry.path);
      setPath("");
      setPreview(null);
      setShowRootOverview(false);
      return;
    }
    if (entry.isDir) {
      setPath(entry.path);
      setPreview(null);
      return;
    }
    if (!activeRoot) return;
    try {
      const data: any = await quicClient.filesRead(activeRoot, entry.path);
      setPreview({
        path: entry.path,
        content: typeof data?.content === "string" ? data.content : JSON.stringify(data, null, 2),
      });
    } catch (e: any) {
      setErr(e?.message ?? "failed");
    }
  }

  function up() {
    if (!activeRoot) return;
    if (!path) {
      setShowRootOverview(true);
      setPreview(null);
      return;
    }
    const segs = path.split("/").filter(Boolean);
    segs.pop();
    setPath(segs.join("/"));
    setPreview(null);
  }

  const activeRootName = roots.find((root) => root.id === activeRoot)?.name ?? "";
  const canGoUp = showRootOverview || !!activeRoot;
  const locationLabel = showRootOverview
    ? "/"
    : `/${activeRootName}${path ? `/${path}` : ""}`;

  return (
    <View style={{ flex: 1, padding: 12 }}>
      {err ? (
        <View style={[s.err, { borderColor: "#991b1b" }]}>
          <Text style={{ color: "#fecaca" }}>{err}</Text>
        </View>
      ) : null}
      <View style={{ flexDirection: "row", gap: 6, marginBottom: 6 }}>
        <ScrollView horizontal showsHorizontalScrollIndicator={false}>
          {roots.map((r) => (
            <Pressable
              key={r.id}
              onPress={() => {
                setActiveRoot(r.id);
                setPath("");
                setPreview(null);
                setShowRootOverview(false);
              }}
              style={{
                borderWidth: 1,
                borderColor: activeRoot === r.id ? c.accent : c.border,
                backgroundColor: activeRoot === r.id ? `${c.accent}22` : "transparent",
                paddingHorizontal: 8,
                paddingVertical: 4,
                borderRadius: 4,
                marginRight: 4,
              }}
            >
              <Text style={{ color: activeRoot === r.id ? c.accent : c.textPrimary, fontSize: 11 }}>
                {r.name}
              </Text>
            </Pressable>
          ))}
        </ScrollView>
      </View>
      <View style={{ flexDirection: "row", alignItems: "center", gap: 8, marginBottom: 6 }}>
        <Pressable
          onPress={up}
          disabled={!canGoUp}
          style={{ opacity: canGoUp ? 1 : 0.4, backgroundColor: c.bgCard, borderWidth: 1, borderColor: c.border, paddingHorizontal: 10, paddingVertical: 4, borderRadius: 4 }}
        >
          <Text style={{ color: c.textPrimary }}>↑</Text>
        </Pressable>
        <Text style={{ color: c.textMuted, flexShrink: 1, fontFamily: "monospace", fontSize: 11 }} numberOfLines={1}>
          {locationLabel}
        </Text>
      </View>
      {loading ? (
        <ActivityIndicator color={c.accent} />
      ) : preview ? (
        <ScrollView style={{ flex: 1, backgroundColor: "#000000aa", borderRadius: 4, padding: 8 }}>
          <Pressable onPress={() => setPreview(null)} style={{ marginBottom: 8 }}>
            <Text style={{ color: c.accent, fontSize: 11 }}>← back to list</Text>
          </Pressable>
          {isMarkdownPath(preview.path) ? (
            <Markdown
              style={markdownStyles(c)}
              onLinkPress={(url) => {
                Linking.openURL(url).catch(() => {});
                return false;
              }}
            >
              {preview.content}
            </Markdown>
          ) : (
            <Text selectable style={{ color: c.textPrimary, fontFamily: "monospace", fontSize: 11 }}>
              {preview.content}
            </Text>
          )}
        </ScrollView>
      ) : (
        <FlatList
          data={entries}
          keyExtractor={(e) => e.path}
          renderItem={({ item }) => (
            <Pressable
              onPress={() => open(item)}
              style={{
                flexDirection: "row",
                alignItems: "center",
                paddingHorizontal: 4,
                paddingVertical: 6,
                borderBottomWidth: StyleSheet.hairlineWidth,
                borderColor: c.border,
              }}
            >
              <Text style={{ color: c.textPrimary, fontSize: 16, marginRight: 8 }}>
                {item.isDir ? "📁" : "📄"}
              </Text>
              <Text style={{ color: c.textPrimary, flex: 1 }} numberOfLines={1}>
                {item.name}
              </Text>
              {!item.isDir && typeof item.size === "number" ? (
                <Text style={{ color: c.textMuted, fontSize: 10 }}>{item.size}B</Text>
              ) : null}
            </Pressable>
          )}
          ListEmptyComponent={<Text style={{ color: c.textMuted, padding: 16 }}>Empty.</Text>}
        />
      )}
    </View>
  );
}

function isMarkdownPath(path: string): boolean {
  const lower = path.toLowerCase();
  return lower.endsWith(".md") || lower.endsWith(".mdx") || lower.endsWith(".markdown");
}

function markdownStyles(c: any) {
  return {
    body: { color: c.textPrimary, fontSize: 14, lineHeight: 21 },
    heading1: { color: c.textPrimary, fontSize: 24, fontWeight: "700", marginTop: 12, marginBottom: 8 },
    heading2: { color: c.textPrimary, fontSize: 20, fontWeight: "700", marginTop: 14, marginBottom: 6 },
    heading3: { color: c.textPrimary, fontSize: 17, fontWeight: "700", marginTop: 12, marginBottom: 4 },
    paragraph: { color: c.textPrimary, fontSize: 14, lineHeight: 21, marginTop: 4, marginBottom: 8 },
    strong: { fontWeight: "700", color: c.textPrimary },
    em: { fontStyle: "italic", color: c.textPrimary },
    code_inline: { fontFamily: "Menlo", fontSize: 12, backgroundColor: c.bgCard, color: c.accent, paddingHorizontal: 4, borderRadius: 4 },
    code_block: { fontFamily: "Menlo", fontSize: 11, backgroundColor: c.bgCard, color: c.textPrimary, padding: 10, borderRadius: 8 },
    fence: { fontFamily: "Menlo", fontSize: 11, backgroundColor: c.bgCard, color: c.textPrimary, padding: 10, borderRadius: 8, marginVertical: 6 },
    link: { color: c.accent, textDecorationLine: "underline" },
    blockquote: { borderLeftWidth: 3, borderLeftColor: c.accent, paddingLeft: 10, marginVertical: 8, color: c.textMuted },
    bullet_list: { marginVertical: 4 },
    ordered_list: { marginVertical: 4 },
    list_item: { color: c.textPrimary, marginVertical: 2 },
    hr: { backgroundColor: c.border, height: 1, marginVertical: 12 },
  } as any;
}

// ── Shared storage ──────────────────────────────────────────────────

type StorageProfile = { id: string; label?: string; type?: string };

function SharedTab() {
  const c = useColors();
  const [profiles, setProfiles] = useState<StorageProfile[]>([]);
  const [active, setActive] = useState<string | null>(null);
  const [path, setPath] = useState("");
  const [entries, setEntries] = useState<FileEntry[]>([]);
  const [loading, setLoading] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  const loadProfiles = useCallback(async () => {
    try {
      const data: any = await quicClient.sharedStorageProfiles();
      if (!data) return;
      const rows: any[] = Array.isArray(data?.profiles) ? data.profiles : Array.isArray(data) ? data : [];
      setProfiles(rows.map((r) => ({ id: r.id, label: r.label, type: r.type })));
      if (rows.length > 0 && !active) setActive(rows[0].id);
    } catch (e: any) {
      setErr(e?.message ?? "failed");
    }
  }, [active]);

  const loadDir = useCallback(async () => {
    if (!active) return;
    setLoading(true);
    try {
      const data: any = await quicClient.sharedStorageList(active, path);
      if (!data) return;
      const raw: any[] = Array.isArray(data?.entries) ? data.entries : [];
      setEntries(raw.map((e) => ({ name: e.name, path: e.path ?? e.name, isDir: Boolean(e.isDir || e.type === "dir"), size: e.size })));
    } catch (e: any) {
      setErr(e?.message ?? "failed");
    } finally {
      setLoading(false);
    }
  }, [active, path]);

  useEffect(() => {
    void loadProfiles();
  }, [loadProfiles]);
  useEffect(() => {
    void loadDir();
  }, [loadDir]);

  if (profiles.length === 0) {
    return (
      <View style={{ flex: 1, padding: 12 }}>
        <Text style={{ color: c.textMuted }}>
          No shared-storage profiles. Add one from the agent (NAS / SMB / S3 / Azure) to list it here.
        </Text>
      </View>
    );
  }

  return (
    <View style={{ flex: 1, padding: 12 }}>
      {err ? (
        <View style={[s.err, { borderColor: "#991b1b" }]}>
          <Text style={{ color: "#fecaca" }}>{err}</Text>
        </View>
      ) : null}
      <ScrollView horizontal showsHorizontalScrollIndicator={false} style={{ marginBottom: 6 }}>
        {profiles.map((p) => (
          <Pressable
            key={p.id}
            onPress={() => {
              setActive(p.id);
              setPath("");
            }}
            style={{
              borderWidth: 1,
              borderColor: active === p.id ? c.accent : c.border,
              backgroundColor: active === p.id ? `${c.accent}22` : "transparent",
              paddingHorizontal: 10,
              paddingVertical: 4,
              borderRadius: 4,
              marginRight: 4,
            }}
          >
            <Text style={{ color: active === p.id ? c.accent : c.textPrimary, fontSize: 11 }}>
              {p.label ?? p.id}
            </Text>
          </Pressable>
        ))}
      </ScrollView>
      <Text style={{ color: c.textMuted, marginBottom: 6, fontFamily: "monospace", fontSize: 11 }}>
        /{path || ""}
      </Text>
      {loading ? (
        <ActivityIndicator color={c.accent} />
      ) : (
        <FlatList
          data={entries}
          keyExtractor={(e) => e.path}
          renderItem={({ item }) => (
            <Pressable
              onPress={() => {
                if (item.isDir) setPath(item.path);
              }}
              style={{
                flexDirection: "row",
                alignItems: "center",
                paddingHorizontal: 4,
                paddingVertical: 6,
                borderBottomWidth: StyleSheet.hairlineWidth,
                borderColor: c.border,
              }}
            >
              <Text style={{ color: c.textPrimary, fontSize: 16, marginRight: 8 }}>
                {item.isDir ? "📁" : "📄"}
              </Text>
              <Text style={{ color: c.textPrimary, flex: 1 }} numberOfLines={1}>
                {item.name}
              </Text>
            </Pressable>
          )}
          ListEmptyComponent={<Text style={{ color: c.textMuted, padding: 16 }}>Empty.</Text>}
        />
      )}
    </View>
  );
}

// ── Blobs ────────────────────────────────────────────────────────────

function BlobsTab() {
  const c = useColors();
  const [buckets, setBuckets] = useState<string[]>([]);
  const [active, setActive] = useState<string | null>(null);
  const [keys, setKeys] = useState<{ key: string; size?: number; contentType?: string }[]>([]);
  const [err, setErr] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);
  const [refreshing, setRefreshing] = useState(false);
  const [uploading, setUploading] = useState(false);

  const loadBuckets = useCallback(async () => {
    try {
      const data: any = await quicClient.blobsListBuckets();
      setBuckets(data?.buckets ?? []);
      if ((data?.buckets ?? []).length > 0 && !active) setActive(data.buckets[0]);
    } catch (e: any) {
      setErr(e?.message ?? "failed");
    }
  }, [active]);

  const loadKeys = useCallback(async (bucket: string) => {
    setLoading(true);
    try {
      const data: any = await quicClient.blobsListKeys(bucket);
      setKeys(data?.keys ?? []);
    } catch (e: any) {
      setErr(e?.message ?? "failed");
    } finally {
      setLoading(false);
      setRefreshing(false);
    }
  }, []);

  useEffect(() => {
    void loadBuckets();
  }, [loadBuckets]);

  useEffect(() => {
    if (active) void loadKeys(active);
  }, [active, loadKeys]);

  async function pickAndUpload() {
    if (!active) {
      Alert.alert("Blobs", "Select a bucket first (or create one via the web UI).");
      return;
    }
    try {
      const pick = await DocumentPicker.getDocumentAsync({ multiple: true, copyToCacheDirectory: true });
      if (pick.canceled || !pick.assets || pick.assets.length === 0) return;
      setUploading(true);
      for (const asset of pick.assets) {
        const file = await fetch(asset.uri).then((r) => r.blob());
        const headers: Record<string, string> = { "Content-Type": asset.mimeType || "application/octet-stream" };
        Object.assign(headers, (quicClient as any).authHeaders ?? {});
        const res = await fetch(
          `${(quicClient as any).baseUrl}/blobs/${encodeURIComponent(active)}/${encodeURIComponent(asset.name ?? "upload.bin")}`,
          { method: "PUT", headers, body: file },
        );
        if (!res.ok) {
          throw new Error(`upload ${asset.name}: HTTP ${res.status}`);
        }
      }
      await loadKeys(active);
    } catch (e: any) {
      Alert.alert("Blobs", e?.message ?? "upload failed");
    } finally {
      setUploading(false);
    }
  }

  async function remove(k: string) {
    if (!active) return;
    Alert.alert("Delete?", `Remove ${active}/${k}?`, [
      { text: "Cancel", style: "cancel" },
      {
        text: "Delete",
        style: "destructive",
        onPress: async () => {
          try {
            await quicClient.blobsDelete(active, k);
            await loadKeys(active);
          } catch (e: any) {
            Alert.alert("Blobs", e?.message ?? "failed to delete");
          }
        },
      },
    ]);
  }

  return (
    <View style={{ flex: 1, padding: 12 }}>
      {err ? (
        <View style={[s.err, { borderColor: "#991b1b" }]}>
          <Text style={{ color: "#fecaca" }}>{err}</Text>
        </View>
      ) : null}
      <View style={{ flexDirection: "row", gap: 6, alignItems: "center", marginBottom: 8 }}>
        <ScrollView horizontal showsHorizontalScrollIndicator={false} style={{ flex: 1 }}>
          {buckets.length === 0 ? (
            <Text style={{ color: c.textMuted, padding: 4 }}>
              No buckets — upload to create one.
            </Text>
          ) : null}
          {buckets.map((b) => (
            <Pressable
              key={b}
              onPress={() => setActive(b)}
              style={{
                borderWidth: 1,
                borderColor: active === b ? c.accent : c.border,
                backgroundColor: active === b ? `${c.accent}22` : "transparent",
                paddingHorizontal: 10,
                paddingVertical: 4,
                borderRadius: 4,
                marginRight: 4,
              }}
            >
              <Text style={{ color: active === b ? c.accent : c.textPrimary, fontSize: 11 }}>{b}</Text>
            </Pressable>
          ))}
        </ScrollView>
        <Pressable
          onPress={pickAndUpload}
          disabled={uploading}
          style={{
            backgroundColor: c.accent,
            opacity: uploading ? 0.5 : 1,
            paddingHorizontal: 10,
            paddingVertical: 6,
            borderRadius: 4,
          }}
        >
          <Text style={{ color: "#fff", fontWeight: "600", fontSize: 12 }}>
            {uploading ? "Uploading…" : "Upload"}
          </Text>
        </Pressable>
      </View>
      {loading ? (
        <ActivityIndicator color={c.accent} />
      ) : (
        <FlatList
          data={keys}
          keyExtractor={(k) => k.key}
          refreshControl={
            <RefreshControl
              refreshing={refreshing}
              onRefresh={() => {
                setRefreshing(true);
                if (active) void loadKeys(active);
              }}
              tintColor={c.accent}
            />
          }
          renderItem={({ item }) => (
            <View
              style={{
                flexDirection: "row",
                alignItems: "center",
                paddingHorizontal: 4,
                paddingVertical: 6,
                borderBottomWidth: StyleSheet.hairlineWidth,
                borderColor: c.border,
              }}
            >
              <Text style={{ color: c.textPrimary, flex: 1, fontFamily: "monospace", fontSize: 11 }} numberOfLines={1}>
                {item.key}
              </Text>
              {typeof item.size === "number" ? (
                <Text style={{ color: c.textMuted, fontSize: 10, marginHorizontal: 6 }}>{item.size}B</Text>
              ) : null}
              <Pressable
                onPress={() => remove(item.key)}
                style={{
                  backgroundColor: "#3f0a0a",
                  borderWidth: 1,
                  borderColor: "#991b1b",
                  paddingHorizontal: 6,
                  paddingVertical: 2,
                  borderRadius: 2,
                }}
              >
                <Text style={{ color: "#fecaca", fontSize: 10 }}>Delete</Text>
              </Pressable>
            </View>
          )}
          ListEmptyComponent={
            <Text style={{ color: c.textMuted, padding: 16, textAlign: "center" }}>No keys.</Text>
          }
        />
      )}
    </View>
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
  tabBar: {
    flexDirection: "row",
    borderBottomWidth: StyleSheet.hairlineWidth,
  },
  err: {
    padding: 8,
    borderWidth: 1,
    borderRadius: 6,
    marginBottom: 8,
  },
});
