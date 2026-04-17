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
import { Stack, useRouter } from "expo-router";
import { useColors } from "../src/context/ThemeContext";
import { useDevice, type Device } from "../src/context/DeviceContext";
import { AppBackButton } from "../src/components/AppBackButton";
import { getLocalSecret, LOCAL_KEYS, saveLocalSecret } from "../src/lib/auth";
import { quicClient } from "../src/lib/quic";
import {
  PhoneProject,
  PhonePushTarget,
  PhoneTemplate,
  bindPhoneProjectToCurrentAgent,
  bindPhoneProjectToTarget,
  createLocalPhoneProject,
  createPhoneProject,
  createPhoneProjectAt,
  deletePhoneProject,
  generatePhoneProjectDraftFromPrompt,
  listPhoneProjects,
  listPhoneTemplates,
} from "../src/lib/phoneProjects";

type StartMode = "this-phone" | "current-agent" | "dev-hw" | "yaver-cloud";
type GitMode = "skip" | "later" | "providers-now";
type CodingMode = "phone" | "runner";

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
  const [step, setStep] = useState(0);
  const [codingMode, setCodingMode] = useState<CodingMode>(connected ? "runner" : "phone");
  const [openAiKey, setOpenAiKey] = useState("");

  const [startMode, setStartMode] = useState<StartMode>("this-phone");
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
  useEffect(() => {
    void getLocalSecret(LOCAL_KEYS.openAiApiKey).then((value) => {
      if (value) setOpenAiKey(value);
    });
  }, []);
  useEffect(() => {
    if (!connected && codingMode === "runner") {
      setCodingMode("phone");
    }
  }, [codingMode, connected]);
  useEffect(() => {
    if (!connected && startMode !== "this-phone") {
      setStartMode("this-phone");
    }
  }, [connected, startMode]);

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
    if (codingMode === "phone" && !openAiKey.trim()) {
      Alert.alert("OpenAI key required", "On-phone coding needs your OpenAI API key.");
      return;
    }
    if (codingMode === "runner" && !connected) {
      Alert.alert("Connect a runner", "Remote coding needs a connected Yaver runner.");
      return;
    }
    if (codingMode === "runner" && startMode === "this-phone") {
      Alert.alert(
        "Pick a backend",
        "Remote runner coding starts after you create this project on a Yaver agent, your dev machine, or Yaver Cloud.",
      );
      return;
    }
    setCreating(true);
    try {
      if (codingMode === "phone" && openAiKey.trim()) {
        await saveLocalSecret(LOCAL_KEYS.openAiApiKey, openAiKey.trim());
      }
      const draft =
        codingMode === "phone" && prompt.trim()
          ? await generatePhoneProjectDraftFromPrompt({
              apiKey: openAiKey.trim(),
              name: name.trim(),
              prompt: prompt.trim(),
              template,
            })
          : {};
      const spec = {
        name: name.trim(),
        template: draft.template ?? (prompt.trim() ? undefined : template),
        schema: draft.schema,
        auth: draft.auth,
        seed: draft.seed,
        app: draft.app,
        prompt: prompt.trim() || undefined,
        runner: prompt.trim() && codingMode === "runner" ? runner || undefined : undefined,
      };
      let p: PhoneProject | null = null;

      if (startMode === "this-phone") {
        p = await createLocalPhoneProject(spec);
      } else if (startMode === "current-agent") {
        if (!connected) {
          throw new Error("Connect a Yaver agent first.");
        }
        p = await createPhoneProject(spec);
        if (p) {
          await bindPhoneProjectToCurrentAgent(p.slug, p.slug, activeDevice?.name || "Current Yaver Agent");
        }
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
        await bindPhoneProjectToTarget(p.slug, target, { slug: p.slug, localUrl: "", browseUrl: "", project: p }, selectedDevMachine.name);
      } else {
        const target: PhonePushTarget = { kind: "yaver-cloud", cloudBaseUrl: YAVER_CLOUD_BASE };
        p = await createPhoneProjectAt(target, spec);
        await bindPhoneProjectToTarget(p.slug, target, { slug: p.slug, localUrl: "", browseUrl: "", project: p }, "Yaver Cloud");
      }

      if (!p) throw new Error("target returned no project");
      setName("");
      setPrompt("");
      setRunner(availableRunners[0]?.runnerId ?? "");
      setGitMode("skip");
      setStep(0);
      setShowForm(false);
      await load();
      router.navigate(`/phone-project/${p.slug}` as any);
      if (gitMode !== "skip") {
        Alert.alert(
          "Git is optional",
          connected
            ? "Connect Git Providers when you want Yaver to export or push this monorepo."
            : "Git setup is optional. Connect a Yaver machine later if you want provider-backed export or push.",
        );
        if (gitMode === "providers-now" && connected) {
          router.navigate("/(tabs)/gitproviders" as any);
        }
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
        {!showForm ? (
          <>
            <Pressable
              onPress={() => {
                setStep(0);
                setStartMode("this-phone");
                setShowForm(true);
              }}
              style={[styles.btn, { backgroundColor: c.accent, marginTop: 12 }]}
            >
              <Text style={[styles.btnText, { color: c.bg }]}>+ New mobile app</Text>
            </Pressable>
            <Text style={[styles.muted, { color: c.textMuted, marginTop: 8 }]}>
              {connected ? "Start on your phone or a Yaver backend." : "Runs locally on this phone."}
            </Text>
          </>
        ) : (
          <View style={[styles.card, { backgroundColor: c.bgCard, borderColor: c.border, marginTop: 12 }]}>
            <View style={styles.stepDots}>
              {[0, 1, 2, 3].map((value) => (
                <View
                  key={value}
                  style={[
                    styles.stepDot,
                    {
                      backgroundColor: step === value ? c.accent : c.border,
                    },
                  ]}
                />
              ))}
            </View>

            {step === 0 ? (
              <>
                <Text style={[styles.label, { color: c.textMuted }]}>Project name</Text>
                <TextInput
                  value={name}
                  onChangeText={setName}
                  placeholder="My app"
                  placeholderTextColor={c.textMuted}
                  style={[styles.input, { color: c.textPrimary, borderColor: c.border }]}
                />
                <Text style={[styles.muted, { color: c.textMuted, marginTop: 8 }]}>
                  Start with a simple name. You can describe the app on the next step.
                </Text>
              </>
            ) : null}

            {step === 1 ? (
              <>
                <Text style={[styles.label, { color: c.textMuted }]}>Describe the app</Text>
                <TextInput
                  value={prompt}
                  onChangeText={setPrompt}
                  placeholder="Todo app with login"
                  placeholderTextColor={c.textMuted}
                  multiline
                  style={[styles.input, styles.promptInput, { color: c.textPrimary, borderColor: c.border }]}
                />
                <Text style={[styles.label, { color: c.textMuted, marginTop: 12 }]}>Template</Text>
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
                    <Text style={[styles.muted, { color: c.textMuted }]} numberOfLines={1}>
                      {t.description}
                    </Text>
                  </Pressable>
                ))}
              </>
            ) : null}

            {step === 2 ? (
              <>
                <Text style={[styles.label, { color: c.textMuted }]}>Who will code?</Text>
                {(
                  [
                    {
                      id: "phone" as CodingMode,
                      label: "This phone",
                      sub: "Uses your OpenAI key",
                    },
                    {
                      id: "runner" as CodingMode,
                      label: "Remote runner",
                      sub: connected ? "Use a connected Yaver runner" : "Connect a runner first",
                      disabled: !connected,
                    },
                  ] as const
                ).map((opt) => (
                  <Pressable
                    key={opt.id}
                    onPress={() => !(opt as any).disabled && setCodingMode(opt.id)}
                    style={[
                      styles.templateRow,
                      {
                        backgroundColor: codingMode === opt.id ? c.accent + "22" : "transparent",
                        borderColor: codingMode === opt.id ? c.accent : c.border,
                        opacity: (opt as any).disabled ? 0.5 : 1,
                      },
                    ]}
                  >
                    <Text style={[styles.templateLabel, { color: c.textPrimary }]}>{opt.label}</Text>
                    <Text style={[styles.muted, { color: c.textMuted }]} numberOfLines={1}>{opt.sub}</Text>
                  </Pressable>
                ))}

                {codingMode === "phone" ? (
                  <>
                    <Text style={[styles.label, { color: c.textMuted, marginTop: 12 }]}>OpenAI API key</Text>
                    <TextInput
                      value={openAiKey}
                      onChangeText={setOpenAiKey}
                      placeholder="sk-..."
                      placeholderTextColor={c.textMuted}
                      autoCapitalize="none"
                      autoCorrect={false}
                      spellCheck={false}
                      style={[styles.input, { color: c.textPrimary, borderColor: c.border }]}
                    />
                    <Text style={[styles.muted, { color: c.textMuted, marginTop: 6 }]}>
                      Required for phone-side AI kickoff.
                    </Text>
                  </>
                ) : null}

                {codingMode === "runner" && prompt.trim() ? (
                  <>
                    <Text style={[styles.label, { color: c.textMuted, marginTop: 12 }]}>Runner</Text>
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
                                borderColor: active ? c.accent : c.border,
                                marginRight: 8,
                                marginTop: 8,
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
                  </>
                ) : null}
              </>
            ) : null}

            {step === 3 ? (
              <>
                <Text style={[styles.label, { color: c.textMuted }]}>Where should it live?</Text>
                {(
                  [
                    {
                      id: "this-phone" as StartMode,
                      label: "This phone",
                      sub: "Local SQLite sandbox",
                    },
                    {
                      id: "current-agent" as StartMode,
                      label: "Current Yaver Agent",
                      sub: activeDevice?.name || "Connected agent",
                      disabled: !connected,
                    },
                    {
                      id: "dev-hw" as StartMode,
                      label: "Your dev machine",
                      sub: selectedDevMachine ? selectedDevMachine.name : "Pick a paired machine",
                      disabled: !connected || devMachines.length === 0,
                      onLongPress: pickDevMachine,
                    },
                    {
                      id: "yaver-cloud" as StartMode,
                      label: "Yaver Cloud",
                      sub: "Managed cloud",
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
                    <Text style={[styles.muted, { color: c.textMuted }]} numberOfLines={1}>{opt.sub}</Text>
                  </Pressable>
                ))}

                <Text style={[styles.label, { color: c.textMuted, marginTop: 12 }]}>Git</Text>
                {(
                  [
                    { id: "skip" as GitMode, label: "No Git" },
                    { id: "later" as GitMode, label: "Later" },
                    { id: "providers-now" as GitMode, label: "Connect now", disabled: !connected },
                  ] as const
                ).map((opt) => (
                  <Pressable
                    key={opt.id}
                    onPress={() => !(opt as any).disabled && setGitMode(opt.id)}
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
                  </Pressable>
                ))}
              </>
            ) : null}

            <View style={{ flexDirection: "row", gap: 8, marginTop: 12 }}>
              <Pressable
                onPress={() => {
                  if (step === 0) {
                    setShowForm(false);
                    return;
                  }
                  setStep((prev) => Math.max(0, prev - 1));
                }}
                style={[styles.btnSecondary, { borderColor: c.border, flex: 1 }]}
              >
                <Text style={[styles.btnText, { color: c.textPrimary }]}>{step === 0 ? "Cancel" : "Back"}</Text>
              </Pressable>
              {step < 3 ? (
                <Pressable
                  onPress={() => setStep((prev) => Math.min(3, prev + 1))}
                  style={[styles.btn, { backgroundColor: c.accent, flex: 1 }]}
                >
                  <Text style={[styles.btnText, { color: c.bg }]}>Next</Text>
                </Pressable>
              ) : (
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
              )}
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
      step,
      startMode,
      codingMode,
      openAiKey,
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
      <Stack.Screen
        options={{
          headerShown: true,
          title: "Mobile Sandbox",
          headerShadowVisible: false,
          headerStyle: { backgroundColor: c.bg },
          headerTintColor: c.accent,
          headerTitleStyle: { color: c.textPrimary, fontWeight: "700" },
          headerLeft: () => <AppBackButton onPress={() => router.back()} label="Back" />,
        }}
      />
      <FlatList
        data={projects}
        keyExtractor={(p) => p.slug}
        contentContainerStyle={{ paddingBottom: 80 + insets.bottom, paddingTop: 12 }}
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
  stepDots: {
    flexDirection: "row",
    gap: 8,
    marginBottom: 14,
  },
  stepDot: {
    flex: 1,
    height: 6,
    borderRadius: 999,
  },
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
