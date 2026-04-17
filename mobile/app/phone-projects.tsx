import React, { useCallback, useEffect, useMemo, useState } from "react";
import {
  ActivityIndicator,
  Alert,
  FlatList,
  KeyboardAvoidingView,
  Platform,
  Pressable,
  RefreshControl,
  ScrollView,
  StyleSheet,
  Text,
  TextInput,
  View,
} from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { useRouter } from "expo-router";
import { useColors } from "../src/context/ThemeContext";
import { useDevice, type Device } from "../src/context/DeviceContext";
import { quicClient } from "../src/lib/quic";
import {
  PhoneProject,
  PhonePushTarget,
  PhoneTemplate,
  createLocalPhoneProject,
  createPhoneProject,
  createPhoneProjectAt,
  deletePhoneProject,
  listPhoneProjects,
  listPhoneTemplates,
} from "../src/lib/phoneProjects";

type StartMode = "this-device" | "dev-hw" | "yaver-cloud";
type GitMode = "skip" | "later" | "providers-now";

const YAVER_CLOUD_BASE = "https://cloud.yaver.io";

function pickDevMachines(all: Device[], currentId: string | undefined): Device[] {
  return all.filter(
    (d) =>
      d.online &&
      !d.isGuest &&
      !d.needsAuth &&
      d.id !== currentId &&
      d.deviceClass !== "edge-mobile",
  );
}

// Phone-first mini-backend list + inline wizard. See MOBILE_WORKER.md §213-419
// and desktop/agent/phone_backend.go.

export default function PhoneProjectsScreen() {
  const c = useColors();
  const insets = useSafeAreaInsets();
  const router = useRouter();
  const { connectionStatus, devices, activeDevice } = useDevice();
  const connected = connectionStatus === "connected";

  const [projects, setProjects] = useState<PhoneProject[]>([]);
  const [templates, setTemplates] = useState<PhoneTemplate[]>([]);
  const [loading, setLoading] = useState(false);
  const [refreshing, setRefreshing] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  const [showForm, setShowForm] = useState(false);
  const [name, setName] = useState("");
  const [template, setTemplate] = useState("todos");
  const [prompt, setPrompt] = useState("");
  const [runner, setRunner] = useState<string>("");
  const [creating, setCreating] = useState(false);
  const [gitMode, setGitMode] = useState<GitMode>("skip");

  // yc.md §Wedge Demo — user picks where the mini-backend lives at creation
  // time. "this-device" is the pragmatic default (lives on whichever agent
  // the phone is currently connected to; in practice that's the user's Mac).
  // The three-tier continuum per MOBILE_WORKER.md §Portability Contract:
  // phone → user's own hardware → Yaver managed cloud.
  const [startMode, setStartMode] = useState<StartMode>("this-device");
  const devMachines = useMemo(
    () => pickDevMachines(devices, activeDevice?.id),
    [devices, activeDevice?.id],
  );
  const [selectedDevMachineId, setSelectedDevMachineId] = useState<string | null>(null);
  useEffect(() => {
    if (!selectedDevMachineId && devMachines.length) {
      setSelectedDevMachineId(devMachines[0].id);
    }
  }, [devMachines, selectedDevMachineId]);
  const selectedDevMachine = useMemo(
    () => devMachines.find((d) => d.id === selectedDevMachineId) ?? null,
    [devMachines, selectedDevMachineId],
  );
  const availableRunners = useMemo(() => {
    const runners = activeDevice?.runners ?? [];
    return runners.filter((item) => item.status === "running" || item.status === "queued" || item.status === "completed");
  }, [activeDevice?.runners]);
  useEffect(() => {
    if (!runner && availableRunners.length) {
      setRunner(availableRunners[0].runnerId);
    }
  }, [availableRunners, runner]);

  const load = useCallback(async () => {
    setErr(null);
    try {
      const [rows, tpls] = await Promise.all([
        listPhoneProjects(),
        templates.length ? Promise.resolve(templates) : listPhoneTemplates(),
      ]);
      setProjects(rows);
      if (!templates.length) setTemplates(tpls);
    } catch (e: any) {
      setErr(e?.message ?? "failed to load");
    } finally {
      setLoading(false);
      setRefreshing(false);
    }
  }, [connected, templates]);

  useEffect(() => {
    setLoading(true);
    void load();
  }, [load]);

  async function create() {
    if (!name.trim()) {
      Alert.alert("Phone Backend", "Project name is required");
      return;
    }
    setCreating(true);
    try {
      const spec = {
        name: name.trim(),
        template: prompt.trim() ? undefined : template,
        prompt: prompt.trim() || undefined,
        runner: prompt.trim() ? runner || undefined : undefined,
      };
      let p: PhoneProject | null = null;
      let where = "";

      if (startMode === "this-device") {
        p = connected ? await createPhoneProject(spec) : await createLocalPhoneProject(spec);
        where = connected ? "on this device" : "in the phone sandbox";
      } else if (startMode === "dev-hw") {
        if (!selectedDevMachine) {
          throw new Error("No dev machine online. Sign in with Yaver on your Mac/Pi/Linux.");
        }
        const relayHttpUrl = quicClient.activeRelayHttpUrl;
        if (!relayHttpUrl) {
          throw new Error(
            "This phone is connected directly to the current agent, not via relay. Cross-device create needs a relay route.",
          );
        }
        const target: PhonePushTarget = {
          kind: "dev-hw",
          deviceId: selectedDevMachine.id,
          relayHttpUrl,
        };
        p = await createPhoneProjectAt(target, spec);
        where = `on ${selectedDevMachine.name}`;
      } else {
        // yaver-cloud
        const target: PhonePushTarget = { kind: "yaver-cloud", cloudBaseUrl: YAVER_CLOUD_BASE };
        p = await createPhoneProjectAt(target, spec);
        where = "on Yaver Cloud";
      }

      if (!p) throw new Error("target returned no project");
      setName("");
      setPrompt("");
      setRunner(availableRunners[0]?.runnerId ?? "");
      setGitMode("skip");
      setShowForm(false);
      // Only refresh the local list — remote projects aren't in the user's
      // connected-agent list, so jumping straight to the detail screen for
      // a local project is the useful UX. For remote, show a toast.
      if (startMode === "this-device") {
        await load();
        router.navigate(`/phone-project/${p.slug}` as any);
        if (!connected && prompt.trim()) {
          Alert.alert(
            "Sandbox project created",
            "The local phone sandbox is ready. Prompt-driven first-draft generation still needs a connected Yaver runner, but you can run the app, shape the schema, and export later from this phone.",
          );
        }
        if (gitMode !== "skip") {
          Alert.alert(
            "Git is optional",
            connected
              ? "This project was created as a monorepo-oriented sandbox workspace. Git setup is optional; when you are ready, open Git Providers and connect the host you want Yaver to push from."
              : "This project was created locally on your phone. Git remains optional. Connect a Yaver dev machine later if you want provider-backed monorepo export and push.",
          );
          if (gitMode === "providers-now" && connected) {
            router.navigate("/(tabs)/gitproviders" as any);
          }
        }
      } else {
        Alert.alert(
          "Created",
          `"${p.name}" is now running ${where}. Slug: ${p.slug}. It'll show up here once you connect to that agent.`,
        );
        await load();
      }
    } catch (e: any) {
      Alert.alert("Phone Backend", e?.message ?? "failed to create");
    } finally {
      setCreating(false);
    }
  }

  function pickDevMachine() {
    if (!devMachines.length) {
      Alert.alert(
        "No dev machines online",
        "Install Yaver on your Mac / Pi / Linux and sign in with the same account.",
      );
      return;
    }
    Alert.alert("Pick a dev machine", "Where should this project live?", [
      ...devMachines.map((d) => ({
        text: `${d.name}${d.local ? " (LAN)" : ""}`,
        onPress: () => setSelectedDevMachineId(d.id),
      })),
      { text: "Cancel", style: "cancel" as const },
    ]);
  }

  async function remove(p: PhoneProject) {
    Alert.alert("Delete?", `Remove "${p.name}"? This deletes the SQLite file and manifest.`, [
      { text: "Cancel", style: "cancel" },
      {
        text: "Delete",
        style: "destructive",
        onPress: async () => {
          try {
            await deletePhoneProject(p.slug);
            await load();
          } catch (e: any) {
            Alert.alert("Phone Backend", e?.message ?? "failed to delete");
          }
        },
      },
    ]);
  }

  const header = useMemo(
    () => (
      <View style={{ paddingHorizontal: 16, paddingTop: 12 }}>
        <Text style={[styles.h1, { color: c.textPrimary }]}>Mobile Sandbox</Text>
        <Text style={[styles.muted, { color: c.textMuted, marginTop: 4 }]}>
          Build from your phone. Run the app against on-device SQLite now, then
          grow it into a monorepo workspace on your own machine or Yaver Cloud later.
        </Text>
        {!connected ? (
          <View style={[styles.card, { backgroundColor: c.bgCard, borderColor: c.border, marginTop: 12 }]}>
            <Text style={[styles.templateLabel, { color: c.textPrimary }]}>Phone-only mode</Text>
            <Text style={[styles.muted, { color: c.textMuted, marginTop: 6 }]}>
              You can create and run local sandbox projects without any active Yaver agent.
              Connect a runner later for prompt generation, remote vibe coding, deploy, export,
              OAuth secret sync, and hosted API-key minting.
            </Text>
          </View>
        ) : null}
        <View style={[styles.card, { backgroundColor: c.bgCard, borderColor: c.border, marginTop: 12 }]}>
          <Text style={[styles.templateLabel, { color: c.textPrimary }]}>Monorepo only</Text>
          <Text style={[styles.muted, { color: c.textMuted, marginTop: 6 }]}>
            New git-connected projects are monorepo-oriented by design. Keep the mobile app,
            portable backend, web/admin surfaces, and deploy/export wiring in one workspace.
            Git is optional; when used, Yaver only supports the monorepo path.
          </Text>
        </View>
        {!showForm ? (
          <Pressable
            onPress={() => setShowForm(true)}
            style={[styles.btn, { backgroundColor: c.accent, marginTop: 12 }]}
          >
            <Text style={[styles.btnText, { color: c.bg }]}>+ New mobile app</Text>
          </Pressable>
        ) : (
          <View style={[styles.card, { backgroundColor: c.bgCard, borderColor: c.border, marginTop: 12 }]}>
            <Text style={[styles.label, { color: c.textMuted }]}>Project name</Text>
            <TextInput
              value={name}
              onChangeText={setName}
              placeholder="My app"
              placeholderTextColor={c.textMuted}
              style={[styles.input, { color: c.textPrimary, borderColor: c.border }]}
            />
            <Text style={[styles.label, { color: c.textMuted, marginTop: 12 }]}>Describe the app (optional)</Text>
            <TextInput
              value={prompt}
              onChangeText={setPrompt}
              placeholder="todo app with login, tags, and a simple dashboard"
              placeholderTextColor={c.textMuted}
              multiline
              style={[styles.input, styles.promptInput, { color: c.textPrimary, borderColor: c.border }]}
            />
            <Text style={[styles.muted, { color: c.textMuted, marginTop: 6 }]}>
              {connected
                ? "Prompt mode generates the first app + backend plan for the mobile sandbox."
                : "Prompt text is saved into the project brief. Connect a runner later to have Yaver generate the first draft from it."}
            </Text>
            {prompt.trim() ? (
              <>
                <Text style={[styles.label, { color: c.textMuted, marginTop: 12 }]}>Coding runner</Text>
                <ScrollView horizontal showsHorizontalScrollIndicator={false}>
                  {(availableRunners.length
                    ? availableRunners.map((item) => ({
                        id: item.runnerId,
                        label: item.title || item.runnerId,
                      }))
                    : [{ id: "codex", label: "Codex" }, { id: "claude", label: "Claude" }, { id: "ollama", label: "Ollama" }]
                  ).map((item) => {
                    const active = runner === item.id;
                    return (
                      <Pressable
                        key={item.id}
                        onPress={() => connected && setRunner(item.id)}
                        style={[
                          styles.modeChip,
                          {
                            backgroundColor: active ? c.accent : c.bgCard,
                            borderColor: c.border,
                            marginRight: 8,
                            marginTop: 8,
                            opacity: connected ? 1 : 0.55,
                          },
                        ]}
                      >
                        <Text style={{ color: active ? c.bg : c.textPrimary, fontWeight: "600" }}>
                          {item.label}
                        </Text>
                      </Pressable>
                    );
                  })}
                </ScrollView>
                <Text style={[styles.muted, { color: c.textMuted, marginTop: 6 }]}>
                  {connected
                    ? "Coding can stay on this device, your dev machine, or Yaver Cloud. Pick the runner you want steering the first draft."
                    : "Runner selection unlocks once a Yaver agent is connected."}
                </Text>
              </>
            ) : null}
            <Text style={[styles.label, { color: c.textMuted, marginTop: 12 }]}>Where should it live?</Text>
            {(
              [
                {
                  id: "this-device" as StartMode,
                  label: "This device",
                  sub: activeDevice?.name ? `→ ${activeDevice.name}` : "The agent you're currently connected to",
                },
                {
                  id: "dev-hw" as StartMode,
                  label: "Your Dev Machine",
                  sub: selectedDevMachine
                    ? `→ ${selectedDevMachine.name}${devMachines.length > 1 ? " (tap to change)" : ""}`
                    : devMachines.length
                      ? "Pick a paired Mac / Pi / Linux box"
                      : connected
                        ? "No other devices paired yet"
                        : "Connect a Yaver device to start here",
                  disabled: !connected || devMachines.length === 0,
                  onLongPress: pickDevMachine,
                },
                {
                  id: "yaver-cloud" as StartMode,
                  label: "Yaver Cloud",
                  sub: YAVER_CLOUD_BASE.replace(/^https?:\/\//, "") + " · managed Hetzner tenant",
                  disabled: !connected,
                },
              ] as const
            ).map((opt) => (
              <Pressable
                key={opt.id}
                onPress={() => {
                  if ((opt as any).disabled) {
                    (opt as any).onLongPress?.();
                    return;
                  }
                  setStartMode(opt.id);
                  if (opt.id === "dev-hw" && devMachines.length > 1) {
                    pickDevMachine();
                  }
                }}
                onLongPress={(opt as any).onLongPress}
                style={[
                  styles.templateRow,
                  {
                    backgroundColor: startMode === opt.id ? c.accent + "22" : "transparent",
                    borderColor: startMode === opt.id ? c.accent : c.border,
                    opacity: (opt as any).disabled ? 0.5 : 1,
                  },
                ]}
              >
                <Text style={[styles.templateLabel, { color: c.textPrimary }]}>{opt.label}</Text>
                <Text style={[styles.muted, { color: c.textMuted }]} numberOfLines={2}>
                  {opt.sub}
                </Text>
              </Pressable>
            ))}

            <Text style={[styles.label, { color: c.textMuted, marginTop: 12 }]}>Template</Text>
            {prompt.trim() ? (
              <Text style={[styles.muted, { color: c.textMuted }]}>
                Prompt mode is active. The template below will be ignored unless the prompt is empty.
              </Text>
            ) : null}
            {templates.map((t) => (
              <Pressable
                key={t.id}
                onPress={() => setTemplate(t.id)}
                style={[
                  styles.templateRow,
                  {
                    backgroundColor: template === t.id ? c.accent + "22" : "transparent",
                    borderColor: template === t.id ? c.accent : c.border,
                  },
                ]}
              >
                <Text style={[styles.templateLabel, { color: c.textPrimary }]}>{t.label}</Text>
                <Text style={[styles.muted, { color: c.textMuted }]} numberOfLines={2}>
                  {t.description}
                </Text>
              </Pressable>
            ))}
            <Text style={[styles.label, { color: c.textMuted, marginTop: 12 }]}>Git integration (optional)</Text>
            {(
              [
                {
                  id: "skip" as GitMode,
                  label: "Skip for now",
                  sub: "Stay local first. You can wire git later.",
                },
                {
                  id: "later" as GitMode,
                  label: "Monorepo later",
                  sub: connected
                    ? "Create now, then export/push this monorepo workspace from your Yaver machine later."
                    : "Create now. Connect a dev machine later to push the monorepo.",
                },
                {
                  id: "providers-now" as GitMode,
                  label: "Open providers",
                  sub: connected
                    ? "Configure GitHub / GitLab / Bitbucket credentials now. Repo setup is still optional."
                    : "Provider setup unlocks once a Yaver dev machine is connected.",
                  disabled: !connected,
                },
              ] as const
            ).map((opt) => (
              <Pressable
                key={opt.id}
                onPress={() => {
                  if ((opt as any).disabled) return;
                  setGitMode(opt.id);
                  if (opt.id === "providers-now" && connected) {
                    router.navigate("/(tabs)/gitproviders" as any);
                  }
                }}
                style={[
                  styles.templateRow,
                  {
                    backgroundColor: gitMode === opt.id ? c.accent + "22" : "transparent",
                    borderColor: gitMode === opt.id ? c.accent : c.border,
                    opacity: (opt as any).disabled ? 0.5 : 1,
                  },
                ]}
              >
                <Text style={[styles.templateLabel, { color: c.textPrimary }]}>{opt.label}</Text>
                <Text style={[styles.muted, { color: c.textMuted }]} numberOfLines={2}>
                  {opt.sub}
                </Text>
              </Pressable>
            ))}
            <Text style={[styles.muted, { color: c.textMuted, marginTop: 6 }]}>
              Git is never required to start. When you do use git, Yaver expects one monorepo workspace rather than split repos.
            </Text>
            <View style={{ flexDirection: "row", gap: 8, marginTop: 12 }}>
              <Pressable
                onPress={() => setShowForm(false)}
                style={[styles.btnSecondary, { borderColor: c.border, flex: 1 }]}
              >
                <Text style={[styles.btnText, { color: c.textPrimary }]}>Cancel</Text>
              </Pressable>
              <Pressable
                onPress={create}
                disabled={creating}
                style={[styles.btn, { backgroundColor: c.accent, flex: 1, opacity: creating ? 0.6 : 1 }]}
              >
                {creating ? (
                  <ActivityIndicator color={c.bg} />
                ) : (
                  <Text style={[styles.btnText, { color: c.bg }]}>Create</Text>
                )}
              </Pressable>
            </View>
          </View>
        )}
        {err ? (
          <Text style={[styles.muted, { color: "#ff6b6b", marginTop: 12 }]}>{err}</Text>
        ) : null}
      </View>
    ),
    [
      c,
      connected,
      creating,
      err,
      name,
      prompt,
      showForm,
      template,
      templates,
      startMode,
      activeDevice,
      selectedDevMachine,
      devMachines.length,
    ],
  );

  return (
    <KeyboardAvoidingView
      behavior={Platform.OS === "ios" ? "padding" : undefined}
      style={{ flex: 1, backgroundColor: c.bg }}
    >
      <FlatList
        data={projects}
        keyExtractor={(p) => p.slug}
        contentContainerStyle={{ paddingBottom: 80 + insets.bottom, paddingTop: insets.top }}
        ListHeaderComponent={header}
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
        ListEmptyComponent={
          loading ? (
            <ActivityIndicator style={{ marginTop: 32 }} color={c.textMuted} />
          ) : (
            <Text style={[styles.muted, { color: c.textMuted, textAlign: "center", marginTop: 32 }]}>
              No phone projects yet.
            </Text>
          )
        }
        renderItem={({ item }) => (
          <View style={{ paddingHorizontal: 16, paddingTop: 12 }}>
            <Pressable
              onPress={() => router.navigate(`/phone-project/${item.slug}` as any)}
              onLongPress={() => remove(item)}
              style={[styles.card, { backgroundColor: c.bgCard, borderColor: c.border }]}
            >
              <Text style={[styles.projectName, { color: c.textPrimary }]}>{item.name}</Text>
              <Text style={[styles.muted, { color: c.textMuted }]} numberOfLines={1}>
                {item.slug}
                {item.template ? ` · ${item.template}` : ""}
              </Text>
              {item.stats ? (
                <Text style={[styles.stats, { color: c.textMuted }]}>
                  {item.stats.tableCount} table{item.stats.tableCount === 1 ? "" : "s"} · {item.stats.rowCount} row
                  {item.stats.rowCount === 1 ? "" : "s"} · {formatBytes(item.stats.dbBytes)}
                </Text>
              ) : null}
            </Pressable>
          </View>
        )}
      />
    </KeyboardAvoidingView>
  );
}

function formatBytes(n: number): string {
  if (!n) return "0 B";
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`;
  return `${(n / 1024 / 1024).toFixed(1)} MB`;
}

const styles = StyleSheet.create({
  h1: { fontSize: 24, fontWeight: "700" },
  muted: { fontSize: 13 },
  btn: { paddingVertical: 12, borderRadius: 8, alignItems: "center" },
  btnSecondary: {
    paddingVertical: 12,
    borderRadius: 8,
    alignItems: "center",
    borderWidth: 1,
  },
  btnText: { fontWeight: "600", fontSize: 15 },
  card: {
    borderWidth: 1,
    borderRadius: 10,
    padding: 14,
  },
  label: { fontSize: 12, fontWeight: "500", marginBottom: 4 },
  input: {
    borderWidth: 1,
    borderRadius: 8,
    padding: 10,
    fontSize: 15,
  },
  promptInput: {
    minHeight: 84,
    textAlignVertical: "top",
  },
  modeChip: {
    paddingHorizontal: 12,
    paddingVertical: 8,
    borderRadius: 999,
    borderWidth: 1,
  },
  templateRow: {
    borderWidth: 1,
    borderRadius: 8,
    padding: 10,
    marginTop: 6,
  },
  templateLabel: { fontWeight: "600", fontSize: 14 },
  projectName: { fontSize: 17, fontWeight: "600" },
  stats: { fontSize: 12, marginTop: 6 },
  empty: { flex: 1, alignItems: "center", justifyContent: "center" },
});
