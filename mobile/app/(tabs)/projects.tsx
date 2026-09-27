import React, { useCallback, useEffect, useState } from "react";
import { ActivityIndicator, Alert, Platform, Pressable, RefreshControl, ScrollView, StyleSheet, Text, View } from "react-native";
import { router } from "expo-router";
import { SafeAreaView } from "react-native-safe-area-context";
import { useCloudStudio } from "../../src/context/CloudStudioContext";
import { useDevice } from "../../src/context/DeviceContext";
import { useColors } from "../../src/context/ThemeContext";
import { quicClient, type RemoteProject } from "../../src/lib/quic";
import { setPendingVibingProject } from "../../src/lib/vibingStore";
import { useResponsiveLayout } from "../../src/hooks/useResponsiveLayout";
import type { PhoneProject } from "../../src/lib/phoneProjects";
import { listLocalPhoneProjectsMeta } from "../../src/lib/phoneSandboxLocal";
import { discoverConnectedProviderProjects, type ProviderProject } from "../../src/lib/gitProviderProjects";
import { cloneGitRepoToPhone } from "../../src/lib/cloneToPhone";
import { ENABLE_CLOUD_WORKSPACE_UI } from "../../src/lib/launchFlags";

const IS_TV = Boolean((Platform as typeof Platform & { isTV?: boolean }).isTV);

export default function ProjectsScreen() {
  const c = useColors();
  const { activeDevice, connectionStatus, codingMode } = useDevice();
  const cloud = useCloudStudio();
  const layout = useResponsiveLayout();
  const legacyTvRunner = IS_TV
    && activeDevice?.name.trim().toLowerCase().replace(/\.local$/, "") === "ubuntu-4gb-hel1-1"
    && !activeDevice.cloudWorkspaceId;
  const [localProjects, setLocalProjects] = useState<RemoteProject[]>([]);
  const [localLoading, setLocalLoading] = useState(false);
  const [creatingRepositoryId, setCreatingRepositoryId] = useState<string | null>(null);
  const [validating, setValidating] = useState(false);
  const [validationSummary, setValidationSummary] = useState<string | null>(null);
  const [phoneProjects, setPhoneProjects] = useState<PhoneProject[]>([]);
  const [providerProjects, setProviderProjects] = useState<ProviderProject[]>([]);
  const [phoneLoading, setPhoneLoading] = useState(false);
  const [phoneError, setPhoneError] = useState<string | null>(null);
  const [cloningProjectId, setCloningProjectId] = useState<string | null>(null);

  const loadPhoneProjects = useCallback(async () => {
    if (codingMode !== "local-only") return;
    setPhoneLoading(true);
    setPhoneError(null);
    try {
      const [local, discovery] = await Promise.all([
        listLocalPhoneProjectsMeta().catch(() => [] as PhoneProject[]),
        discoverConnectedProviderProjects(),
      ]);
      setPhoneProjects(local);
      setProviderProjects(discovery.projects);
      setPhoneError(discovery.errors.join("\n") || null);
    } catch (error) {
      setPhoneError(error instanceof Error ? error.message : "Could not list projects stored on this phone.");
    } finally {
      setPhoneLoading(false);
    }
  }, [codingMode]);

  const loadLocal = useCallback(async () => {
    if ((ENABLE_CLOUD_WORKSPACE_UI && IS_TV && !legacyTvRunner) || !activeDevice || connectionStatus !== "connected") {
      setLocalProjects([]);
      return;
    }
    setLocalLoading(true);
    try {
      setLocalProjects(await quicClient.listProjects(true));
    } finally {
      setLocalLoading(false);
    }
  }, [activeDevice, connectionStatus, legacyTvRunner]);

  useEffect(() => {
    loadLocal().catch(() => {});
  }, [loadLocal]);

  useEffect(() => {
    loadPhoneProjects().catch(() => {});
  }, [loadPhoneProjects]);

  const cloneProviderProject = async (project: ProviderProject) => {
    setCloningProjectId(project.id);
    try {
      const cloned = await cloneGitRepoToPhone(project.cloneUrl, { ref: project.defaultBranch });
      await loadPhoneProjects();
      router.push({ pathname: "/(tabs)/tasks", params: { openNew: "1", phoneCheckout: cloned.slug } } as any);
    } catch (error) {
      Alert.alert("Clone failed", error instanceof Error ? error.message : "Could not clone this repository to the phone.");
    } finally {
      setCloningProjectId(null);
    }
  };

  const createSession = async (repositoryId: string, baseRef?: string) => {
    setCreatingRepositoryId(repositoryId);
    try {
      await cloud.createProjectSession(repositoryId, baseRef);
      router.push("/tasks");
    } catch (error) {
      Alert.alert("Project Session", error instanceof Error ? error.message : "Could not create the Project Session");
    } finally {
      setCreatingRepositoryId(null);
    }
  };

  const runTests = async () => {
    if (!cloud.activeProjectSession || validating) return;
    setValidating(true);
    setValidationSummary(null);
    try {
      let run = await quicClient.startValidationRun(cloud.activeProjectSession.projectSessionId, "test");
      for (let attempt = 0; attempt < 120 && (run.status === "queued" || run.status === "running"); attempt++) {
        await new Promise((resolve) => setTimeout(resolve, 1000));
        run = await quicClient.getValidationRun(cloud.activeProjectSession.projectSessionId, run.validationRunId);
      }
      const summary = run.status === "passed" ? "Tests passed" : run.status === "failed" ? "Tests failed" : `Tests ${run.status}`;
      setValidationSummary(summary);
      Alert.alert(summary, run.output?.trim().slice(-1200) || "No test output was returned.");
    } catch (error) {
      const message = error instanceof Error ? error.message : "Tests could not run";
      setValidationSummary(message);
      Alert.alert("Tests unavailable", message);
    } finally {
      setValidating(false);
    }
  };

  if (ENABLE_CLOUD_WORKSPACE_UI && IS_TV && !legacyTvRunner) {
    const accessReady = cloud.status?.access.status === "active";
    const gitReady = cloud.status?.gitConnections.some((connection) => connection.status === "ready") ?? false;
    const workspaceReady = cloud.status?.workspaces.some((workspace) => workspace.state === "ready") ?? false;
    const runnerReady = activeDevice?.deviceKind === "cloud-runner" && connectionStatus === "connected";
    const prerequisiteMessage = !accessReady
      ? "Cloud Studio access is unavailable for this account."
      : !gitReady
        ? "A ready Git Connection is required."
        : !workspaceReady
          ? "The selected legacy hosted runner is not ready. Choose your own machine or VPS."
          : !runnerReady
            ? "Connect to the assigned Cloud Runner."
            : null;

    return (
      <SafeAreaView style={[styles.safe, { backgroundColor: c.bg }]} edges={["bottom"]}>
        <ScrollView
          contentContainerStyle={styles.container}
          refreshControl={<RefreshControl refreshing={cloud.loading} onRefresh={cloud.refresh} tintColor={c.accent} />}
        >
          <Text style={[styles.title, { color: c.textPrimary }]}>Cloud Studio Projects</Text>
          <Text style={[styles.subtitle, { color: c.textSecondary }]}>Git repositories run in isolated Project Sessions on your Cloud Runner.</Text>

          {cloud.loading && !cloud.status ? (
            <ActivityIndicator style={{ marginTop: 40 }} size="large" color={c.accent} />
          ) : prerequisiteMessage ? (
            <View style={[styles.notice, { backgroundColor: c.bgCard, borderColor: c.border }]}>
              <Text style={[styles.noticeTitle, { color: c.textPrimary }]}>Setup required</Text>
              <Text style={[styles.noticeText, { color: c.textSecondary }]}>{prerequisiteMessage}</Text>
            </View>
          ) : cloud.loading && cloud.repositories.length === 0 ? (
            <ActivityIndicator style={{ marginTop: 40 }} size="large" color={c.accent} />
          ) : (
            <>
              {cloud.activeProjectSession ? (
                <View style={[styles.activeCard, { backgroundColor: c.accent + "18", borderColor: c.accent }]}>
                  <View style={{ flex: 1 }}>
                    <Text style={[styles.sectionLabel, { color: c.accent }]}>ACTIVE PROJECT SESSION</Text>
                    <Text style={[styles.cardName, { color: c.textPrimary }]}>{cloud.activeProjectSession.repositoryName}</Text>
                    <Text style={[styles.cardPath, { color: c.textSecondary }]}>{cloud.activeProjectSession.reviewBranch}</Text>
                  </View>
                  <View style={styles.actions}>
                    <Pressable style={({ focused }) => [styles.action, { borderColor: c.border }, focused && styles.focused]} onPress={runTests} disabled={validating}>
                      <Text style={[styles.actionText, { color: c.textPrimary }]}>{validating ? "Testing…" : "Run tests"}</Text>
                    </Pressable>
                    <Pressable style={({ focused }) => [styles.action, { borderColor: c.border }, focused && styles.focused]} onPress={() => router.push("/tasks")}>
                      <Text style={[styles.actionText, { color: c.textPrimary }]}>Open</Text>
                    </Pressable>
                  </View>
                </View>
              ) : null}
              {validationSummary ? <Text style={[styles.validationSummary, { color: validationSummary === "Tests passed" ? c.success : c.warn }]}>{validationSummary}</Text> : null}

              {cloud.projectSessions.filter((session) => session.status === "ready" && session.projectSessionId !== cloud.activeProjectSession?.projectSessionId).length > 0 ? (
                <>
                  <Text style={[styles.sectionTitle, { color: c.textPrimary }]}>Ready sessions</Text>
                  {cloud.projectSessions.filter((session) => session.status === "ready" && session.projectSessionId !== cloud.activeProjectSession?.projectSessionId).map((session) => (
                    <Pressable key={session.projectSessionId} style={({ focused }) => [styles.card, { backgroundColor: c.bgCard, borderColor: c.border }, focused && styles.focused]} onPress={() => cloud.selectProjectSession(session)}>
                      <Text style={styles.cardIcon}>◉</Text>
                      <View style={{ flex: 1 }}>
                        <Text style={[styles.cardName, { color: c.textPrimary }]}>{session.repositoryName}</Text>
                        <Text style={[styles.cardPath, { color: c.textSecondary }]}>{session.reviewBranch}</Text>
                      </View>
                      <Text style={[styles.cardBranch, { color: c.accent }]}>Select</Text>
                    </Pressable>
                  ))}
                </>
              ) : null}

              <Text style={[styles.sectionTitle, { color: c.textPrimary }]}>Repositories</Text>
              {cloud.repositories.length === 0 ? (
                <Text style={[styles.empty, { color: c.textMuted }]}>No repositories are available to this Cloud Runner.</Text>
              ) : cloud.repositories.map((repository) => (
                <Pressable
                  key={repository.repositoryId}
                  disabled={creatingRepositoryId !== null}
                  style={({ focused }) => [styles.card, { backgroundColor: c.bgCard, borderColor: c.border }, focused && styles.focused]}
                  onPress={() => createSession(repository.repositoryId, repository.defaultRef)}
                >
                  <Text style={styles.cardIcon}>⌘</Text>
                  <View style={{ flex: 1 }}>
                    <Text style={[styles.cardName, { color: c.textPrimary }]}>{repository.name}</Text>
                    <Text style={[styles.cardPath, { color: c.textSecondary }]}>{repository.defaultRef || "Default branch"}</Text>
                  </View>
                  {creatingRepositoryId === repository.repositoryId
                    ? <ActivityIndicator color={c.accent} />
                    : <Text style={[styles.cardBranch, { color: c.accent }]}>New session</Text>}
                </Pressable>
              ))}
            </>
          )}
          {cloud.error ? <Text style={[styles.error, { color: c.error }]}>{cloud.error}</Text> : null}
        </ScrollView>
      </SafeAreaView>
    );
  }

  if (!IS_TV && codingMode === "local-only") {
    const localSlugs = new Set(phoneProjects.map((project) => project.slug.toLowerCase()));
    const unclonedProviderProjects = providerProjects.filter((project) => !localSlugs.has(project.name.toLowerCase()));
    return (
      <SafeAreaView style={[styles.safe, { backgroundColor: c.bg }]} edges={["bottom"]}>
        <ScrollView
          contentContainerStyle={[styles.container, { padding: 24 }]}
          refreshControl={<RefreshControl refreshing={phoneLoading} onRefresh={loadPhoneProjects} tintColor={c.accent} />}
        >
          <Text style={[styles.title, { color: c.textPrimary, fontSize: 32 }]}>Projects</Text>
          <Text style={[styles.subtitle, { color: c.textSecondary, fontSize: 15, marginBottom: 20 }]}>No remote box · checkouts and connected Git providers on this phone</Text>

          <View style={[styles.notice, { backgroundColor: c.bgCard, borderColor: c.border, padding: 16, marginBottom: 18 }]}>
            <Text style={[styles.noticeTitle, { color: c.textPrimary, fontSize: 17 }]}>Phone-local workspace</Text>
            <Text style={[styles.noticeText, { color: c.textSecondary, fontSize: 13 }]}>DeepSeek can audit and edit cloned repositories. Git status, diff, commit, and push are available; builds, tests, shells, previews, and deploys require another execution target.</Text>
          </View>

          <Text style={[styles.sectionTitle, { color: c.textPrimary, fontSize: 20 }]}>On this phone</Text>
          {phoneProjects.length === 0 ? (
            <Text style={[styles.empty, { color: c.textMuted, fontSize: 14 }]}>No checkout yet. Clone one from GitHub or GitLab below.</Text>
          ) : phoneProjects.map((project) => (
            <Pressable
              key={`phone:${project.slug}`}
              style={[styles.card, { backgroundColor: c.bgCard, borderColor: c.border, padding: 16 }]}
              onPress={() => router.push({ pathname: "/(tabs)/tasks", params: { openNew: "1", phoneCheckout: project.slug } } as any)}
            >
              <Text style={[styles.cardIcon, { fontSize: 24 }]}>📱</Text>
              <View style={{ flex: 1 }}>
                <Text style={[styles.cardName, { color: c.textPrimary, fontSize: 17 }]}>{project.name}</Text>
                <Text style={[styles.cardPath, { color: c.textSecondary, fontSize: 12 }]}>{project.slug}</Text>
              </View>
              <Text style={[styles.cardBranch, { color: c.accent, fontSize: 13 }]}>Vibe</Text>
            </Pressable>
          ))}

          <Text style={[styles.sectionTitle, { color: c.textPrimary, fontSize: 20 }]}>GitHub & GitLab</Text>
          {phoneLoading && providerProjects.length === 0 ? (
            <ActivityIndicator style={{ marginTop: 20 }} color={c.accent} />
          ) : providerProjects.length === 0 ? (
            <View>
              <Text style={[styles.empty, { color: c.textMuted, fontSize: 14 }]}>No connected provider projects found.</Text>
              <Pressable onPress={() => router.push("/(tabs)/settings" as any)} style={{ paddingVertical: 12 }}>
                <Text style={{ color: c.accent, fontWeight: "700" }}>Connect GitHub or GitLab in Settings →</Text>
              </Pressable>
            </View>
          ) : unclonedProviderProjects.length === 0 ? (
            <Text style={[styles.empty, { color: c.textMuted, fontSize: 14 }]}>All discovered projects are already on this phone.</Text>
          ) : unclonedProviderProjects.map((project) => (
            <Pressable
              key={project.id}
              disabled={cloningProjectId !== null}
              style={[styles.card, { backgroundColor: c.bgCard, borderColor: c.border, padding: 16, opacity: cloningProjectId && cloningProjectId !== project.id ? 0.55 : 1 }]}
              onPress={() => { void cloneProviderProject(project); }}
            >
              <Text style={[styles.cardIcon, { fontSize: 24 }]}>{project.provider === "github" ? "◉" : "◆"}</Text>
              <View style={{ flex: 1 }}>
                <Text style={[styles.cardName, { color: c.textPrimary, fontSize: 17 }]} numberOfLines={1}>{project.fullName}</Text>
                <Text style={[styles.cardPath, { color: c.textSecondary, fontSize: 12 }]}>{project.provider === "github" ? "GitHub" : "GitLab"} · {project.defaultBranch} · {project.isPrivate ? "private" : "public"}</Text>
              </View>
              {cloningProjectId === project.id ? <ActivityIndicator color={c.accent} /> : <Text style={[styles.cardBranch, { color: c.accent, fontSize: 13 }]}>Clone</Text>}
            </Pressable>
          ))}
          {phoneError ? <Text style={[styles.error, { color: c.error, fontSize: 13 }]}>{phoneError}</Text> : null}
        </ScrollView>
      </SafeAreaView>
    );
  }

  return (
    <SafeAreaView style={[styles.safe, { backgroundColor: c.bg }]} edges={["bottom"]}>
      <ScrollView contentContainerStyle={styles.container} refreshControl={<RefreshControl refreshing={localLoading} onRefresh={loadLocal} tintColor={c.accent} />}>
        <Text style={[styles.title, { color: c.textPrimary }]}>{legacyTvRunner ? "Runner Projects" : "Projects"}</Text>
        <Text style={[styles.subtitle, { color: c.textSecondary }]}>{activeDevice ? `Repositories on ${activeDevice.name}` : "Connect a device to list projects"}</Text>
        {localLoading && localProjects.length === 0 ? <ActivityIndicator style={{ marginTop: 32 }} size="large" color={c.accent} /> : localProjects.length === 0 ? (
          <Text style={[styles.empty, { color: c.textMuted }]}>{activeDevice ? "No projects discovered yet." : "Connect a device to see its projects."}</Text>
        ) : localProjects.map((project) => (
          <Pressable key={project.path} style={({ focused }) => [styles.card, { backgroundColor: c.bgCard, borderColor: c.border }, focused && styles.focused]} onPress={() => {
            quicClient.setWorkDir(project.path).catch(() => {});
            if (layout.isTablet) {
              router.push({ pathname: "/vibe-studio", params: { project: project.path } });
              return;
            }
            setPendingVibingProject(project.path);
            router.push("/vibing");
          }}>
            <Text style={styles.cardIcon}>📁</Text>
            <View style={{ flex: 1 }}>
              <Text style={[styles.cardName, { color: c.textPrimary }]} numberOfLines={1}>{project.name}</Text>
              <Text style={[styles.cardPath, { color: c.textSecondary }]} numberOfLines={1}>{project.path}</Text>
            </View>
            {project.branch ? <Text style={[styles.cardBranch, { color: c.accent }]}>{project.branch}</Text> : null}
          </Pressable>
        ))}
      </ScrollView>
    </SafeAreaView>
  );
}

const styles = StyleSheet.create({
  safe: { flex: 1 },
  container: { padding: 48, paddingBottom: 80 },
  title: { fontSize: 48, fontWeight: "800" },
  subtitle: { fontSize: 20, marginTop: 4, marginBottom: 28 },
  sectionTitle: { fontSize: 26, fontWeight: "700", marginTop: 26, marginBottom: 12 },
  sectionLabel: { fontSize: 13, fontWeight: "800", marginBottom: 5 },
  notice: { borderWidth: 2, borderRadius: 16, padding: 24, marginTop: 8 },
  noticeTitle: { fontSize: 26, fontWeight: "700" },
  noticeText: { fontSize: 18, marginTop: 8 },
  empty: { fontSize: 18, marginTop: 8 },
  error: { fontSize: 16, marginTop: 18 },
  activeCard: { flexDirection: "row", alignItems: "center", borderRadius: 16, borderWidth: 2, padding: 22, gap: 16 },
  card: { flexDirection: "row", alignItems: "center", borderRadius: 16, borderWidth: 2, padding: 22, marginBottom: 14, gap: 16 },
  cardIcon: { fontSize: 32 },
  cardName: { fontSize: 26, fontWeight: "700" },
  cardPath: { fontSize: 16, marginTop: 4 },
  cardBranch: { fontSize: 16, fontWeight: "600" },
  action: { borderWidth: 2, borderRadius: 12, paddingHorizontal: 20, paddingVertical: 12 },
  actions: { flexDirection: "row", gap: 10 },
  actionText: { fontSize: 18, fontWeight: "700" },
  validationSummary: { fontSize: 16, fontWeight: "600", marginTop: 10 },
  focused: { transform: [{ scale: 1.02 }], opacity: 0.92 },
});
