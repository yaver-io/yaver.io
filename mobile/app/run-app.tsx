import React, { useCallback, useEffect, useMemo, useState } from "react";
import { ActivityIndicator, FlatList, Pressable, ScrollView, StyleSheet, Text, View } from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { useLocalSearchParams, useRouter } from "expo-router";
import { useColors } from "../src/context/ThemeContext";
import { AppBackButton } from "../src/components/AppBackButton";
import {
  browsePhoneTable,
  getPhoneProject,
  getPhoneProjectAccess,
  type PhoneAppSpec,
  type PhoneProject,
  type PhoneSchema,
} from "../src/lib/phoneProjects";

// run-app — generic READ-ONLY renderer for a Yaver Serverless app on mobile.
// Mirrors the web RunSharedApp: it reads the project's app.yaml screens + table
// schema and renders an interactive list view backed by the live /data API.
// This is the "USE the app" runtime for serverless-lite projects on mobile
// (distinct from the Hermes path used for full third-party RN code).
//
// Today it renders against the CONNECTED agent (owner/preview). The friend-link
// path (remote host + scoped read-only token from a share) reuses this exact
// renderer; wiring the deep link + remote data source is the next step.

function tablesFor(schema: PhoneSchema | null | undefined, app: PhoneAppSpec | null | undefined): string[] {
  const fromScreens = (app?.screens ?? []).map((s) => s.table).filter((t): t is string => !!t);
  if (fromScreens.length) return Array.from(new Set(fromScreens));
  return (schema?.tables ?? []).map((t) => t.name);
}

export default function RunAppScreen() {
  const { slug } = useLocalSearchParams<{ slug: string }>();
  const slugStr = String(slug ?? "");
  const c = useColors();
  const insets = useSafeAreaInsets();
  const router = useRouter();

  const [project, setProject] = useState<PhoneProject | null>(null);
  const [active, setActive] = useState<string | null>(null);
  const [rows, setRows] = useState<Array<Record<string, unknown>>>([]);
  const [loading, setLoading] = useState(true);
  const [err, setErr] = useState<string | null>(null);

  const tables = useMemo(() => tablesFor(project?.schema, project?.app), [project]);

  const loadRows = useCallback(
    async (table: string) => {
      try {
        const resolved = await getPhoneProjectAccess(slugStr);
        const res = await browsePhoneTable(slugStr, table, "", 100, resolved);
        setRows(res?.rows ?? []);
      } catch (e) {
        setErr(e instanceof Error ? e.message : String(e));
      }
    },
    [slugStr],
  );

  useEffect(() => {
    (async () => {
      try {
        const resolved = await getPhoneProjectAccess(slugStr);
        const p = await getPhoneProject(slugStr, resolved);
        setProject(p);
        const t = tablesFor(p?.schema, p?.app);
        if (t.length) {
          setActive(t[0]);
          await loadRows(t[0]);
        }
      } catch (e) {
        setErr(e instanceof Error ? e.message : String(e));
      } finally {
        setLoading(false);
      }
    })();
  }, [slugStr, loadRows]);

  const screenTitle = useMemo(() => {
    const s = (project?.app?.screens ?? []).find((x) => x.table === active);
    return s?.title || active || project?.name || "App";
  }, [project, active]);

  if (loading) {
    return (
      <View style={[styles.center, { backgroundColor: c.bg }]}>
        <ActivityIndicator color={c.accent} />
      </View>
    );
  }

  return (
    <View style={{ flex: 1, backgroundColor: c.bg, paddingTop: insets.top }}>
      <View style={styles.header}>
        <AppBackButton onPress={() => router.back()} />
        <View style={{ flex: 1 }}>
          <Text style={[styles.title, { color: c.textPrimary }]}>{project?.name || slugStr}</Text>
          <Text style={[styles.sub, { color: c.textMuted }]}>Running on Yaver Serverless · read-only</Text>
        </View>
      </View>

      <ScrollView horizontal showsHorizontalScrollIndicator={false} style={styles.nav} contentContainerStyle={{ gap: 8, paddingHorizontal: 16 }}>
        {tables.map((t) => (
          <Pressable
            key={t}
            onPress={() => {
              setActive(t);
              void loadRows(t);
            }}
            style={[styles.chip, { borderColor: c.border, backgroundColor: active === t ? c.accent : "transparent" }]}
          >
            <Text style={{ color: active === t ? "#fff" : c.textMuted, fontSize: 12 }}>{t}</Text>
          </Pressable>
        ))}
      </ScrollView>

      {err ? (
        <Text style={{ color: "#ef4444", padding: 16 }}>{err}</Text>
      ) : (
        <FlatList
          data={rows}
          keyExtractor={(_, i) => String(i)}
          contentContainerStyle={{ padding: 16, gap: 10 }}
          ListHeaderComponent={<Text style={[styles.screenTitle, { color: c.textPrimary }]}>{screenTitle}</Text>}
          ListEmptyComponent={<Text style={{ color: c.textMuted }}>No rows yet.</Text>}
          renderItem={({ item }) => (
            <View style={[styles.card, { borderColor: c.border, backgroundColor: c.bgCard }]}>
              {Object.entries(item).map(([k, v]) => (
                <View key={k} style={styles.kv}>
                  <Text style={[styles.k, { color: c.textMuted }]}>{k}</Text>
                  <Text style={[styles.v, { color: c.textPrimary }]} numberOfLines={3}>
                    {v === null || v === undefined ? "—" : typeof v === "object" ? JSON.stringify(v) : String(v)}
                  </Text>
                </View>
              ))}
            </View>
          )}
        />
      )}
    </View>
  );
}

const styles = StyleSheet.create({
  center: { flex: 1, alignItems: "center", justifyContent: "center" },
  header: { flexDirection: "row", alignItems: "center", gap: 8, paddingHorizontal: 12, paddingVertical: 10 },
  title: { fontSize: 18, fontWeight: "600" },
  sub: { fontSize: 12 },
  nav: { maxHeight: 44, marginBottom: 4 },
  chip: { borderWidth: 1, borderRadius: 999, paddingHorizontal: 12, paddingVertical: 6 },
  screenTitle: { fontSize: 16, fontWeight: "600", marginBottom: 6 },
  card: { borderWidth: 1, borderRadius: 10, padding: 12, gap: 6 },
  kv: { flexDirection: "row", gap: 8 },
  k: { fontSize: 12, width: 96 },
  v: { fontSize: 13, flex: 1 },
});
