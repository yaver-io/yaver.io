import React, { useEffect, useMemo, useState } from "react";
import { Pressable, RefreshControl, ScrollView, StyleSheet, Text, View } from "react-native";
import { useLocalSearchParams, useRouter } from "expo-router";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { useColors } from "../../../src/context/ThemeContext";
import { AppBackButton } from "../../../src/components/AppBackButton";
import { getPhoneProject, getPhoneProjectAccess, type PhoneProject, type PhoneProjectAccess } from "../../../src/lib/phoneProjects";

type WorkspaceTab = "app" | "backend" | "code";
type CodingMode = "mobile" | "remote-dev" | "yaver-cloud";

export default function PhoneProjectWorkspaceScreen() {
  const c = useColors();
  const insets = useSafeAreaInsets();
  const router = useRouter();
  const { slug } = useLocalSearchParams<{ slug: string }>();
  const slugStr = String(slug ?? "");

  const [project, setProject] = useState<PhoneProject | null>(null);
  const [access, setAccess] = useState<PhoneProjectAccess | null>(null);
  const [refreshing, setRefreshing] = useState(false);
  const [tab, setTab] = useState<WorkspaceTab>("app");
  const [codingMode, setCodingMode] = useState<CodingMode>("mobile");

  async function load() {
    if (!slugStr) return;
    const resolved = await getPhoneProjectAccess(slugStr);
    setAccess(resolved);
    const value = await getPhoneProject(slugStr, resolved);
    setProject(value);
  }

  useEffect(() => {
    void load();
  }, [slugStr]);

  const primaryEntity = useMemo(
    () =>
      project?.app?.primaryEntity ||
      project?.schema?.tables?.find((table) => table.name !== "users")?.name ||
      project?.schema?.tables?.[0]?.name ||
      "items",
    [project],
  );

  const workspacePrompt = useMemo(() => {
    if (!project?.dir) return "";
    const location =
      codingMode === "mobile"
        ? "inside the mobile phone sandbox"
        : codingMode === "remote-dev"
          ? "on the user's remote dev machine through Yaver"
          : "on Yaver Cloud";
    return [
      `You are coding for the Yaver mobile sandbox project "${project.name}".`,
      `Primary entity: ${primaryEntity}.`,
      `The sandbox runtime is SQLite-based and should stay exportable to remote Linux/macOS hardware or cloud later.`,
      `Coding is happening ${location}.`,
      `Work like a solo developer using a monorepo-style mobile/web/backend workspace, but prioritize the mobile sandbox experience first.`,
      `Show concrete progress in the task stream and suggest the next follow-up prompt.`,
    ].join("\n");
  }, [codingMode, primaryEntity, project]);

  function openCodeLoop() {
    if (!project?.dir) return;
    router.navigate({
      pathname: "/(tabs)/tasks" as any,
      params: {
        dir: project.dir,
        prompt: workspacePrompt,
        title: `Vibe ${project.name}`,
        openNew: "1",
      },
    });
  }

  const tabBody =
    tab === "app" ? (
      <View style={[styles.panel, { backgroundColor: c.bgCard, borderColor: c.border }]}>
        <Text style={[styles.panelTitle, { color: c.textPrimary }]}>Mobile app slice</Text>
        <Text style={[styles.panelText, { color: c.textMuted }]}>
          Run the SQLite-backed sandbox app locally on the phone. This is the main product surface.
        </Text>
        <Pressable
          onPress={() => router.navigate(`/phone-project/run/${slugStr}` as any)}
          style={[styles.primaryBtn, { backgroundColor: c.accent }]}
        >
          <Text style={{ color: c.bg, fontWeight: "700" }}>Open sandbox app</Text>
        </Pressable>
      </View>
    ) : tab === "backend" ? (
      <View style={[styles.panel, { backgroundColor: c.bgCard, borderColor: c.border }]}>
        <Text style={[styles.panelTitle, { color: c.textPrimary }]}>Backend slice</Text>
        <Text style={[styles.panelText, { color: c.textMuted }]}>
          Schema, auth personas, seed data, and deploy/export controls for the portable backend.
        </Text>
        <Text style={[styles.panelMeta, { color: c.textMuted }]}>
          {project?.schema?.tables?.length ?? 0} tables · primary entity {primaryEntity}
        </Text>
        <Pressable
          onPress={() => router.navigate(`/phone-project/${slugStr}` as any)}
          style={[styles.secondaryBtn, { borderColor: c.border }]}
        >
          <Text style={{ color: c.textPrimary, fontWeight: "700" }}>Open backend controls</Text>
        </Pressable>
      </View>
    ) : (
      <View style={[styles.panel, { backgroundColor: c.bgCard, borderColor: c.border }]}>
        <Text style={[styles.panelTitle, { color: c.textPrimary }]}>Code slice</Text>
        <Text style={[styles.panelText, { color: c.textMuted }]}>
          Use the live task stream like Claude Code / Codex. Same project, same workspace, different runner location.
        </Text>
        <Text style={[styles.panelMeta, { color: c.textMuted }]}>
          Coding mode: {codingMode === "mobile" ? "on-phone" : codingMode === "remote-dev" ? "remote dev" : "Yaver Cloud"}
        </Text>
        <View style={styles.modeRow}>
          {[
            { id: "mobile" as CodingMode, label: "On-phone" },
            { id: "remote-dev" as CodingMode, label: "Remote dev" },
            { id: "yaver-cloud" as CodingMode, label: "Yaver Cloud" },
          ].map((mode) => {
            const active = codingMode === mode.id;
            return (
              <Pressable
                key={mode.id}
                onPress={() => setCodingMode(mode.id)}
                style={[
                  styles.modeChip,
                  {
                    backgroundColor: active ? c.accent : c.bg,
                    borderColor: c.border,
                  },
                ]}
              >
                <Text style={{ color: active ? c.bg : c.textPrimary, fontWeight: "700" }}>
                  {mode.label}
                </Text>
              </Pressable>
            );
          })}
        </View>
        <Pressable onPress={openCodeLoop} style={[styles.primaryBtn, { backgroundColor: c.accent }]}>
          <Text style={{ color: c.bg, fontWeight: "700" }}>Open coding loop</Text>
        </Pressable>
      </View>
    );

  return (
    <ScrollView
      style={{ backgroundColor: c.bg }}
      contentContainerStyle={{ paddingTop: insets.top + 8, paddingBottom: insets.bottom + 40 }}
      refreshControl={
        <RefreshControl
          refreshing={refreshing}
          onRefresh={() => {
            setRefreshing(true);
            void load().finally(() => setRefreshing(false));
          }}
          tintColor={c.textMuted}
        />
      }
    >
      <View style={{ paddingHorizontal: 16 }}>
        <AppBackButton onPress={() => router.back()} style={{ marginBottom: 8 }} />
        <Text style={[styles.title, { color: c.textPrimary }]}>{project?.name ?? slugStr}</Text>
        <Text style={{ color: c.textMuted, marginTop: 4 }}>
          Mobile-first sandbox workspace. App, backend, and code stay together.
        </Text>
        <View style={[styles.summary, { backgroundColor: c.bgCard, borderColor: c.border }]}>
          <Text style={{ color: c.textPrimary, fontWeight: "700" }}>
            Local runtime: on-device SQLite
          </Text>
          <Text style={{ color: c.textMuted, fontSize: 12, marginTop: 4 }}>
            Active backend: {access?.label ?? "On-device SQLite"}
          </Text>
          <Text style={{ color: c.textMuted, fontSize: 12, marginTop: 2 }}>
            Export path: developer hardware → Yaver Cloud → optional Convex/Supabase reassurance
          </Text>
        </View>
        <View style={styles.tabRow}>
          {[
            { id: "app" as WorkspaceTab, label: "App" },
            { id: "backend" as WorkspaceTab, label: "Backend" },
            { id: "code" as WorkspaceTab, label: "Code" },
          ].map((item) => {
            const active = tab === item.id;
            return (
              <Pressable
                key={item.id}
                onPress={() => setTab(item.id)}
                style={[
                  styles.tabChip,
                  {
                    backgroundColor: active ? c.accent : c.bgCard,
                    borderColor: c.border,
                  },
                ]}
              >
                <Text style={{ color: active ? c.bg : c.textPrimary, fontWeight: "700" }}>
                  {item.label}
                </Text>
              </Pressable>
            );
          })}
        </View>
        {tabBody}
      </View>
    </ScrollView>
  );
}

const styles = StyleSheet.create({
  title: { fontSize: 28, fontWeight: "700" },
  summary: {
    borderWidth: 1,
    borderRadius: 14,
    padding: 14,
    marginTop: 14,
  },
  tabRow: {
    flexDirection: "row",
    gap: 8,
    marginTop: 16,
  },
  tabChip: {
    borderWidth: 1,
    borderRadius: 999,
    paddingHorizontal: 14,
    paddingVertical: 8,
  },
  panel: {
    borderWidth: 1,
    borderRadius: 14,
    padding: 14,
    marginTop: 16,
  },
  panelTitle: { fontSize: 18, fontWeight: "700" },
  panelText: { fontSize: 13, marginTop: 6, lineHeight: 18 },
  panelMeta: { fontSize: 12, marginTop: 10 },
  primaryBtn: {
    borderRadius: 12,
    paddingVertical: 14,
    alignItems: "center",
    marginTop: 14,
  },
  secondaryBtn: {
    borderWidth: 1,
    borderRadius: 12,
    paddingVertical: 14,
    alignItems: "center",
    marginTop: 14,
  },
  modeRow: {
    flexDirection: "row",
    gap: 8,
    marginTop: 12,
    flexWrap: "wrap",
  },
  modeChip: {
    borderWidth: 1,
    borderRadius: 999,
    paddingHorizontal: 12,
    paddingVertical: 8,
  },
});
