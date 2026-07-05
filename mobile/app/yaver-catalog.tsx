import React, { useMemo } from "react";
import { Pressable, ScrollView, StyleSheet, Text, View } from "react-native";
import { SafeAreaView } from "react-native-safe-area-context";
import { useLocalSearchParams, useRouter } from "expo-router";
import { Ionicons } from "@expo/vector-icons";

import { useColors } from "../src/context/ThemeContext";
import {
  yaverNativeAppsForSurface,
  yaverNativeCompanionAppsForSurface,
  yaverNativePrimaryAppsForSurface,
  type YaverNativeCatalogApp,
  type YaverNativeSurface,
} from "../src/lib/yaverNativeCatalog";

const SURFACES = new Set<YaverNativeSurface>([
  "web",
  "ios",
  "android",
  "tablet",
  "tvos",
  "android-tv",
  "watch",
  "car",
  "visionos",
  "xr",
  "remote-runner",
  "mcp",
]);

function normalizeSurface(value: unknown): YaverNativeSurface {
  const raw = typeof value === "string" ? value : "";
  return SURFACES.has(raw as YaverNativeSurface) ? (raw as YaverNativeSurface) : "ios";
}

export default function YaverCatalogScreen() {
  const c = useColors();
  const router = useRouter();
  const params = useLocalSearchParams<{ surface?: string }>();
  const surface = normalizeSurface(params.surface);
  const primary = useMemo(() => yaverNativePrimaryAppsForSurface(surface), [surface]);
  const companion = useMemo(() => yaverNativeCompanionAppsForSurface(surface), [surface]);
  const all = useMemo(() => yaverNativeAppsForSurface(surface), [surface]);

  return (
    <SafeAreaView style={[styles.safe, { backgroundColor: c.bg }]}>
      <ScrollView contentContainerStyle={styles.scroll}>
        <View style={styles.header}>
          <Text style={[styles.title, { color: c.textPrimary }]}>Yaver Catalog</Text>
          <Text style={[styles.subtitle, { color: c.textSecondary }]}>
            {surface} · {all.length} Yaver-native app{all.length === 1 ? "" : "s"}
          </Text>
        </View>

        {primary.map((app) => (
          <CatalogRow key={app.id} app={app} c={c} onPress={() => router.push((app.route ?? "/remote-runtime") as any)} />
        ))}

        {companion.length > 0 ? (
          <View style={styles.section}>
            <Text style={[styles.sectionTitle, { color: c.textMuted }]}>Companion on this surface</Text>
            {companion.map((app) => (
              <CatalogRow key={app.id} app={app} c={c} onPress={() => router.push((app.route ?? "/assistant") as any)} companion />
            ))}
          </View>
        ) : null}
      </ScrollView>
    </SafeAreaView>
  );
}

function CatalogRow({
  app,
  c,
  companion,
  onPress,
}: {
  app: YaverNativeCatalogApp;
  c: any;
  companion?: boolean;
  onPress: () => void;
}) {
  const icon = app.kind === "game" ? "game-controller-outline" : app.kind === "devtool" ? "construct-outline" : "sparkles-outline";
  return (
    <Pressable
      focusable
      onPress={onPress}
      style={({ pressed, focused }: any) => [
        styles.row,
        { backgroundColor: focused ? c.accent : c.bgCard, borderColor: focused ? c.accent : c.border, opacity: pressed ? 0.88 : 1 },
      ]}
    >
      {({ focused }: any) => (
        <>
          <Ionicons name={icon as any} size={30} color={focused ? c.textInverse : c.accent} />
          <View style={styles.rowText}>
            <Text style={[styles.rowTitle, { color: focused ? c.textInverse : c.textPrimary }]} numberOfLines={1}>
              {app.title}
            </Text>
            <Text style={[styles.rowSub, { color: focused ? c.textInverse : c.textSecondary }]} numberOfLines={2}>
              {companion ? "Approval, voice, status, and handoff companion. " : ""}{app.subtitle}
            </Text>
            <Text style={[styles.meta, { color: focused ? c.textInverse : c.textMuted }]} numberOfLines={1}>
              {app.manifestFile} · {app.auth.provider} · {app.status}
            </Text>
          </View>
          <Ionicons name="chevron-forward" size={24} color={focused ? c.textInverse : c.textMuted} />
        </>
      )}
    </Pressable>
  );
}

const styles = StyleSheet.create({
  safe: { flex: 1 },
  scroll: { padding: 20, gap: 12 },
  header: { marginBottom: 8 },
  title: { fontSize: 30, fontWeight: "800" },
  subtitle: { fontSize: 14, marginTop: 4 },
  section: { marginTop: 12, gap: 12 },
  sectionTitle: { fontSize: 12, fontWeight: "700", textTransform: "uppercase" },
  row: {
    minHeight: 112,
    borderRadius: 8,
    borderWidth: 1,
    padding: 14,
    flexDirection: "row",
    alignItems: "center",
    gap: 14,
  },
  rowText: { flex: 1, gap: 4 },
  rowTitle: { fontSize: 18, fontWeight: "800" },
  rowSub: { fontSize: 14, lineHeight: 19 },
  meta: { fontSize: 12, marginTop: 2 },
});
