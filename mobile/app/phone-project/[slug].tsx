import React, { useCallback, useEffect, useMemo, useState } from "react";
import {
  ActivityIndicator,
  Alert,
  FlatList,
  Platform,
  Pressable,
  RefreshControl,
  ScrollView,
  Share,
  StyleSheet,
  Text,
  TextInput,
  View,
} from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { useLocalSearchParams, useRouter } from "expo-router";
import { useColors } from "../../src/context/ThemeContext";
import {
  PhoneBrowseResult,
  PhoneProject,
  browsePhoneTable,
  deletePhoneProject,
  deletePhoneRow,
  getPhoneProject,
  insertPhoneRow,
  listPhoneTables,
  phoneExportUrl,
  promotePhoneProject,
} from "../../src/lib/phoneProjects";

// Promote targets — a curated subset of the 19 SwitchEngine targets that make
// sense for a mini-backend promotion. The agent still exposes all 19 via
// /switch/targets for power users.
const PROMOTE_TARGETS: Array<{ id: string; label: string; sub: string }> = [
  { id: "sqlite-local", label: "SQLite file", sub: "Copy to a real project dir" },
  { id: "sqlite-turso", label: "Turso", sub: "Managed LibSQL on the edge" },
  { id: "postgres-local", label: "Postgres (Docker)", sub: "Local Postgres 16" },
  { id: "supabase-cloud", label: "Supabase Cloud", sub: "Managed Postgres + auth" },
  { id: "postgres-neon", label: "Neon", sub: "Serverless Postgres" },
  { id: "convex-cloud", label: "Convex Cloud", sub: "Reactive backend (AI-rewrite)" },
];

export default function PhoneProjectDetailScreen() {
  const c = useColors();
  const insets = useSafeAreaInsets();
  const router = useRouter();
  const { slug } = useLocalSearchParams<{ slug: string }>();
  const slugStr = String(slug ?? "");

  const [project, setProject] = useState<PhoneProject | null>(null);
  const [tables, setTables] = useState<Array<{ name: string; rowCount?: number }>>([]);
  const [selectedTable, setSelectedTable] = useState<string | null>(null);
  const [rows, setRows] = useState<PhoneBrowseResult["rows"]>([]);
  const [loading, setLoading] = useState(true);
  const [refreshing, setRefreshing] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  const [showInsert, setShowInsert] = useState(false);
  const [insertJSON, setInsertJSON] = useState("{}");
  const [promoting, setPromoting] = useState<string | null>(null);

  const load = useCallback(async () => {
    if (!slugStr) return;
    try {
      setErr(null);
      const [p, ts] = await Promise.all([getPhoneProject(slugStr), listPhoneTables(slugStr)]);
      setProject(p);
      setTables(ts);
      if (!selectedTable && ts.length) setSelectedTable(ts[0].name);
    } catch (e: any) {
      setErr(e?.message ?? "failed to load");
    } finally {
      setLoading(false);
      setRefreshing(false);
    }
  }, [slugStr, selectedTable]);

  const loadRows = useCallback(async () => {
    if (!slugStr || !selectedTable) return;
    try {
      const r = await browsePhoneTable(slugStr, selectedTable);
      setRows(r?.rows ?? []);
    } catch (e: any) {
      setErr(e?.message ?? "browse failed");
    }
  }, [slugStr, selectedTable]);

  useEffect(() => {
    void load();
  }, [load]);

  useEffect(() => {
    void loadRows();
  }, [loadRows]);

  async function doInsert() {
    if (!slugStr || !selectedTable) return;
    try {
      const doc = JSON.parse(insertJSON || "{}");
      if (!doc || typeof doc !== "object") throw new Error("JSON must be an object");
      await insertPhoneRow(slugStr, selectedTable, doc);
      setInsertJSON("{}");
      setShowInsert(false);
      await loadRows();
    } catch (e: any) {
      Alert.alert("Insert failed", e?.message ?? "invalid JSON");
    }
  }

  async function doDeleteRow(id: unknown) {
    if (!slugStr || !selectedTable || !id) return;
    Alert.alert("Delete row?", String(id), [
      { text: "Cancel", style: "cancel" },
      {
        text: "Delete",
        style: "destructive",
        onPress: async () => {
          await deletePhoneRow(slugStr, selectedTable, String(id));
          await loadRows();
        },
      },
    ]);
  }

  async function doExport() {
    const ref = phoneExportUrl(slugStr);
    if (!ref) {
      Alert.alert("Export", "Agent not reachable");
      return;
    }
    // Share the URL — the user's system share sheet can save it, hand it to a
    // HTTP client, etc. The agent streams tgz bytes with proper headers.
    try {
      await Share.share({
        message: `Yaver phone-project export:\n${ref.uri}\n\n(Authenticated URL — paste into curl with X-Relay-Password / Authorization headers.)`,
        url: ref.uri,
      });
    } catch (e: any) {
      Alert.alert("Export", e?.message ?? "share failed");
    }
  }

  async function doPromote(targetID: string, label: string) {
    if (!slugStr) return;
    Alert.alert(
      `Plan migration to ${label}?`,
      "This produces a step-by-step switch plan with a 7-day rollback window. You can run it or keep it as a dry-run.",
      [
        { text: "Cancel", style: "cancel" },
        {
          text: "Dry run",
          onPress: async () => {
            setPromoting(targetID);
            try {
              const r = await promotePhoneProject(slugStr, targetID, { dryRun: true, run: true });
              if (r?.error) Alert.alert("Promote", r.error);
              else Alert.alert("Plan saved", `Switch plan ${r?.state?.id ?? ""} created. Review in Switch history.`);
            } catch (e: any) {
              Alert.alert("Promote", e?.message ?? "failed");
            } finally {
              setPromoting(null);
            }
          },
        },
        {
          text: "Plan",
          onPress: async () => {
            setPromoting(targetID);
            try {
              const r = await promotePhoneProject(slugStr, targetID, {});
              if (r?.error) Alert.alert("Promote", r.error);
              else Alert.alert("Plan saved", `Complexity: ${r?.state?.complexity}. Run it from Switch history when ready.`);
            } catch (e: any) {
              Alert.alert("Promote", e?.message ?? "failed");
            } finally {
              setPromoting(null);
            }
          },
        },
      ],
    );
  }

  async function doDelete() {
    Alert.alert("Delete project?", `This removes ${project?.name ?? slugStr} and its SQLite file.`, [
      { text: "Cancel", style: "cancel" },
      {
        text: "Delete",
        style: "destructive",
        onPress: async () => {
          await deletePhoneProject(slugStr);
          router.back();
        },
      },
    ]);
  }

  const rowIdKey = useMemo(() => {
    if (!rows.length) return "id";
    return Object.keys(rows[0]).find((k) => k === "id") ?? Object.keys(rows[0])[0];
  }, [rows]);

  if (loading) {
    return (
      <View style={[styles.empty, { backgroundColor: c.bg }]}>
        <ActivityIndicator color={c.textMuted} />
      </View>
    );
  }

  if (!project) {
    return (
      <View style={[styles.empty, { backgroundColor: c.bg }]}>
        <Text style={{ color: c.textMuted }}>{err ?? "Project not found"}</Text>
        <Pressable onPress={() => router.back()} style={{ marginTop: 12 }}>
          <Text style={{ color: c.accent }}>Back</Text>
        </Pressable>
      </View>
    );
  }

  return (
    <ScrollView
      style={{ backgroundColor: c.bg }}
      contentContainerStyle={{ paddingTop: insets.top + 8, paddingBottom: 60 + insets.bottom }}
      refreshControl={
        <RefreshControl
          refreshing={refreshing}
          onRefresh={() => {
            setRefreshing(true);
            void load();
          }}
          tintColor={c.textMuted}
        />
      }
    >
      <View style={{ paddingHorizontal: 16 }}>
        <Pressable onPress={() => router.back()}>
          <Text style={{ color: c.accent, marginBottom: 8 }}>‹ Back</Text>
        </Pressable>
        <Text style={[styles.h1, { color: c.textPrimary }]}>{project.name}</Text>
        <Text style={{ color: c.textMuted, fontSize: 12, marginTop: 2 }}>
          {project.slug} · {project.template ?? "custom"}
        </Text>
        {project.stats ? (
          <Text style={{ color: c.textMuted, fontSize: 12, marginTop: 8 }}>
            {project.stats.tableCount} table{project.stats.tableCount === 1 ? "" : "s"} ·{" "}
            {project.stats.rowCount} row{project.stats.rowCount === 1 ? "" : "s"} ·{" "}
            {formatBytes(project.stats.dbBytes)} on disk
          </Text>
        ) : null}

        <View style={{ flexDirection: "row", gap: 8, marginTop: 12 }}>
          <Pressable
            onPress={doExport}
            style={[styles.btnSecondary, { borderColor: c.border, flex: 1 }]}
          >
            <Text style={[styles.btnText, { color: c.textPrimary }]}>Export .tgz</Text>
          </Pressable>
          <Pressable
            onPress={doDelete}
            style={[styles.btnSecondary, { borderColor: "#ff6b6b", flex: 1 }]}
          >
            <Text style={[styles.btnText, { color: "#ff6b6b" }]}>Delete</Text>
          </Pressable>
        </View>
      </View>

      <Text style={[styles.section, { color: c.textPrimary }]}>Tables</Text>
      <ScrollView horizontal showsHorizontalScrollIndicator={false} contentContainerStyle={{ paddingHorizontal: 16 }}>
        {tables.map((t) => (
          <Pressable
            key={t.name}
            onPress={() => setSelectedTable(t.name)}
            style={[
              styles.chip,
              {
                backgroundColor: selectedTable === t.name ? c.accent : c.bgCard,
                borderColor: c.border,
              },
            ]}
          >
            <Text style={{ color: selectedTable === t.name ? c.bg : c.textPrimary, fontWeight: "500" }}>
              {t.name}
            </Text>
            {typeof t.rowCount === "number" ? (
              <Text style={{ color: selectedTable === t.name ? c.bg : c.textMuted, fontSize: 11, marginLeft: 4 }}>
                {t.rowCount}
              </Text>
            ) : null}
          </Pressable>
        ))}
      </ScrollView>

      {selectedTable ? (
        <View style={{ paddingHorizontal: 16, marginTop: 8 }}>
          <View style={{ flexDirection: "row", justifyContent: "space-between", alignItems: "center" }}>
            <Text style={{ color: c.textMuted, fontSize: 13 }}>
              {rows.length} row{rows.length === 1 ? "" : "s"}
            </Text>
            <Pressable
              onPress={() => setShowInsert((v) => !v)}
              style={[styles.btnSecondary, { borderColor: c.border, paddingHorizontal: 12 }]}
            >
              <Text style={{ color: c.textPrimary, fontWeight: "500" }}>{showInsert ? "Cancel" : "+ Insert"}</Text>
            </Pressable>
          </View>
          {showInsert ? (
            <View style={[styles.card, { backgroundColor: c.bgCard, borderColor: c.border, marginTop: 8 }]}>
              <Text style={{ color: c.textMuted, fontSize: 12, marginBottom: 4 }}>Row JSON</Text>
              <TextInput
                value={insertJSON}
                onChangeText={setInsertJSON}
                multiline
                style={[styles.codeInput, { color: c.textPrimary, borderColor: c.border }]}
                autoCapitalize="none"
                autoCorrect={false}
                placeholder='{"id":"x","name":"hello"}'
                placeholderTextColor={c.textMuted}
              />
              <Pressable
                onPress={doInsert}
                style={[styles.btn, { backgroundColor: c.accent, marginTop: 8 }]}
              >
                <Text style={{ color: c.bg, fontWeight: "600" }}>Insert row</Text>
              </Pressable>
            </View>
          ) : null}
          <FlatList
            scrollEnabled={false}
            data={rows}
            keyExtractor={(item, idx) => String(item[rowIdKey] ?? idx)}
            ItemSeparatorComponent={() => <View style={{ height: 6 }} />}
            renderItem={({ item }) => (
              <Pressable
                onLongPress={() => doDeleteRow(item[rowIdKey])}
                style={[styles.card, { backgroundColor: c.bgCard, borderColor: c.border }]}
              >
                {Object.entries(item).map(([k, v]) => (
                  <View key={k} style={styles.kv}>
                    <Text style={[styles.k, { color: c.textMuted }]}>{k}</Text>
                    <Text style={[styles.v, { color: c.textPrimary }]} numberOfLines={2}>
                      {formatValue(v)}
                    </Text>
                  </View>
                ))}
              </Pressable>
            )}
            ListEmptyComponent={
              <Text style={{ color: c.textMuted, textAlign: "center", marginTop: 16 }}>
                No rows yet. Tap + Insert to add one.
              </Text>
            }
            style={{ marginTop: 8 }}
          />
        </View>
      ) : null}

      <Text style={[styles.section, { color: c.textPrimary }]}>Promote</Text>
      <View style={{ paddingHorizontal: 16 }}>
        <Text style={{ color: c.textMuted, fontSize: 13, marginBottom: 8 }}>
          Take this mini-backend to a real target. The switch engine creates a snapshot,
          runs the 7-layer plan, and keeps 7 days of rollback.
        </Text>
        {PROMOTE_TARGETS.map((t) => (
          <Pressable
            key={t.id}
            onPress={() => doPromote(t.id, t.label)}
            disabled={promoting === t.id}
            style={[styles.card, { backgroundColor: c.bgCard, borderColor: c.border, marginBottom: 8 }]}
          >
            <View style={{ flexDirection: "row", justifyContent: "space-between" }}>
              <View style={{ flex: 1 }}>
                <Text style={[styles.promoteLabel, { color: c.textPrimary }]}>{t.label}</Text>
                <Text style={{ color: c.textMuted, fontSize: 12 }}>{t.sub}</Text>
              </View>
              {promoting === t.id ? (
                <ActivityIndicator color={c.accent} />
              ) : (
                <Text style={{ color: c.accent, fontSize: 18 }}>›</Text>
              )}
            </View>
          </Pressable>
        ))}
      </View>
    </ScrollView>
  );
}

function formatValue(v: unknown): string {
  if (v === null || v === undefined) return "—";
  if (typeof v === "object") return JSON.stringify(v);
  return String(v);
}

function formatBytes(n: number): string {
  if (!n) return "0 B";
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`;
  return `${(n / 1024 / 1024).toFixed(1)} MB`;
}

const styles = StyleSheet.create({
  h1: { fontSize: 24, fontWeight: "700" },
  section: {
    fontSize: 13,
    fontWeight: "600",
    marginTop: 20,
    marginBottom: 8,
    paddingHorizontal: 16,
    textTransform: "uppercase",
    letterSpacing: 0.5,
  },
  chip: {
    paddingHorizontal: 12,
    paddingVertical: 8,
    borderRadius: 16,
    borderWidth: 1,
    marginRight: 6,
    flexDirection: "row",
    alignItems: "center",
  },
  card: { borderWidth: 1, borderRadius: 10, padding: 12 },
  kv: { marginBottom: 4 },
  k: { fontSize: 11, textTransform: "uppercase", letterSpacing: 0.5 },
  v: { fontSize: 14 },
  codeInput: {
    borderWidth: 1,
    borderRadius: 8,
    padding: 10,
    fontFamily: Platform.select({ ios: "Menlo", android: "monospace", default: "monospace" }),
    minHeight: 90,
    fontSize: 13,
    textAlignVertical: "top",
  },
  btn: { paddingVertical: 12, borderRadius: 8, alignItems: "center" },
  btnSecondary: { paddingVertical: 10, borderRadius: 8, alignItems: "center", borderWidth: 1 },
  btnText: { fontWeight: "600", fontSize: 14 },
  promoteLabel: { fontWeight: "600", fontSize: 14 },
  empty: { flex: 1, alignItems: "center", justifyContent: "center" },
});
