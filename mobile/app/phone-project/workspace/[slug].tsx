import React, { useEffect, useMemo, useState } from "react";
import { Alert, Pressable, RefreshControl, ScrollView, StyleSheet, Text, View } from "react-native";
import { useLocalSearchParams, useRouter } from "expo-router";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { useColors } from "../../../src/context/ThemeContext";
import { AppBackButton } from "../../../src/components/AppBackButton";
import { getPhoneProject, getPhoneProjectAccess, type PhoneProject, type PhoneProjectAccess } from "../../../src/lib/phoneProjects";

type WorkspaceTab = "app" | "backend" | "code";

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
      access?.kind === "current-agent"
        ? "on the currently connected Yaver agent"
        : access?.kind === "dev-hw"
          ? "on the user's remote dev machine through Yaver"
          : access?.kind === "yaver-cloud"
            ? "on Yaver Cloud"
            : "in the mobile sandbox";
    return [
      `You are coding for the Yaver mobile sandbox project "${project.name}".`,
      `Primary entity: ${primaryEntity}.`,
      `The sandbox runtime is SQLite-based and should stay exportable to remote Linux/macOS hardware or cloud later.`,
      `Coding is happening ${location}.`,
      `Work like a solo developer using a monorepo-style mobile/web/backend workspace, but prioritize the mobile sandbox experience first.`,
      `Show concrete progress in the task stream and suggest the next follow-up prompt.`,
    ].join("\n");
  }, [access?.kind, primaryEntity, project]);

  function openCodeLoop() {
    if (!project?.dir) {
      Alert.alert(
        "Coding loop unavailable",
        "Phone-local projects run in-app first. Export or bind this project to a Yaver agent or Yaver Cloud before opening the coding loop.",
      );
      return;
    }
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
          Use the task stream after this project is on a backend with a real workspace directory.
        </Text>
        {project?.dir ? (
          <>
            <Text style={[styles.panelMeta, { color: c.textMuted }]}>
              Backend: {access?.label ?? "Connected backend"}
            </Text>
            <Pressable onPress={openCodeLoop} style={[styles.primaryBtn, { backgroundColor: c.accent }]}>
              <Text style={{ color: c.bg, fontWeight: "700" }}>Open coding loop</Text>
            </Pressable>
          </>
        ) : (
          <>
            <Text style={[styles.panelMeta, { color: c.textMuted }]}>
              Phone-local init is ready. Remote coding starts after export or backend binding.
            </Text>
            <Pressable
              onPress={() => router.navigate(`/phone-project/${slugStr}` as any)}
              style={[styles.secondaryBtn, { borderColor: c.border }]}
            >
              <Text style={{ color: c.textPrimary, fontWeight: "700" }}>Open backend controls</Text>
            </Pressable>
          </>
        )}
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
            Active backend: {access?.label ?? "This phone"}
          </Text>
          <Text style={{ color: c.textMuted, fontSize: 12, marginTop: 2 }}>
            Export path: phone → Yaver agent → Yaver Cloud
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
});
