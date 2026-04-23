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
import { useAuth } from "../src/context/AuthContext";
import { getLocalSecret, getUserSettings, LOCAL_KEYS, saveLocalSecret } from "../src/lib/auth";
import { isCloudPreviewUser } from "../src/lib/cloudPreview";
import { buildImportedConversationBrief, mergeImportedConversationPrompt } from "../src/lib/conversationImport";
import { getManagedSubscription } from "../src/lib/subscription";
import { getYaverCloudBaseUrl } from "../src/lib/yaverCloud";
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
type MobileAiProvider = "openai" | "glm";

const YAVER_CLOUD_BASE = getYaverCloudBaseUrl();

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
  const { token, user } = useAuth();
  const { connectionStatus, devices, activeDevice } = useDevice();
  const connected = connectionStatus === "connected";
  const canUseCloudPreview = isCloudPreviewUser(user?.email);
  const [hasManagedCloud, setHasManagedCloud] = useState(false);
  const canUseYaverCloud = canUseCloudPreview || hasManagedCloud;

  const [projects, setProjects] = useState<PhoneProject[]>([]);
  const [templates, setTemplates] = useState<PhoneTemplate[]>([]);
  const [loading, setLoading] = useState(false);
  const [refreshing, setRefreshing] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  const [showForm, setShowForm] = useState(false);
  const [name, setName] = useState("");
  const [template, setTemplate] = useState("todos");
  const [prompt, setPrompt] = useState("");
  const [importedConversation, setImportedConversation] = useState("");
  const [analyzingImport, setAnalyzingImport] = useState(false);
  const [runner, setRunner] = useState<string>("");
  const [creating, setCreating] = useState(false);
  const [gitMode, setGitMode] = useState<GitMode>("skip");
  const [step, setStep] = useState(0);
  const [codingMode, setCodingMode] = useState<CodingMode>(connected ? "runner" : "phone");
  const [mobileAiProvider, setMobileAiProvider] = useState<MobileAiProvider>("openai");
  const [openAiKey, setOpenAiKey] = useState("");
  const [glmKey, setGlmKey] = useState("");

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
  const importedBrief = useMemo(
    () => (importedConversation.trim() ? buildImportedConversationBrief(importedConversation) : null),
    [importedConversation],
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
    let cancelled = false;
    const loadMobileAi = async () => {
      const [localOpenAi, localGlm, localProvider, cloud] = await Promise.all([
        getLocalSecret(LOCAL_KEYS.openAiApiKey),
        getLocalSecret(LOCAL_KEYS.glmApiKey),
        getLocalSecret(LOCAL_KEYS.mobileCodingProvider),
        token ? getUserSettings(token).catch(() => ({})) : Promise.resolve({}),
      ]);
      if (cancelled) return;
      if (localOpenAi) setOpenAiKey(localOpenAi);
      else if (typeof (cloud as any).openAiApiKey === "string") setOpenAiKey((cloud as any).openAiApiKey);
      if (localGlm) setGlmKey(localGlm);
      else if (typeof (cloud as any).glmApiKey === "string") setGlmKey((cloud as any).glmApiKey);
      const savedProvider =
        localProvider === "glm" || localProvider === "openai"
          ? localProvider
          : (cloud as any).mobileCodingProvider === "glm"
            ? "glm"
            : "openai";
      setMobileAiProvider(savedProvider);
    };
    void loadMobileAi();
    return () => {
      cancelled = true;
    };
  }, [token]);
  useEffect(() => {
    let cancelled = false;
    if (!token) {
      setHasManagedCloud(false);
      return;
    }
    void (async () => {
      const summary = await getManagedSubscription(token);
      if (cancelled || !summary) return;
      const hasMachine = Array.isArray(summary.machines)
        && summary.machines.some((machine) => machine.status !== "stopped");
      const hasSubscription = !!summary.subscription;
      setHasManagedCloud(hasMachine || hasSubscription);
    })();
    return () => {
      cancelled = true;
    };
  }, [token]);
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

  const applyImportedConversation = useCallback(async () => {
    if (!importedBrief) {
      Alert.alert("Import conversation", "Paste a Claude, ChatGPT, or Codex thread first.");
      return;
    }
    if (connected) {
      setAnalyzingImport(true);
      try {
        const plan = await quicClient.analyzeConversationImport({
          url: importedBrief.sourceUrl,
          content: importedConversation,
          title: importedBrief.title,
          runner: runner || undefined,
        });
        if (!plan) {
          throw new Error("Analysis failed");
        }
        if (!name.trim() && plan.suggestedName) {
          setName(plan.suggestedName);
        }
        setPrompt(plan.generatedPrompt);
        return;
      } catch (e: any) {
        Alert.alert("Import analysis", e?.message ?? "Analysis failed. Falling back to local brief generation.");
      } finally {
        setAnalyzingImport(false);
      }
    }
    if (!name.trim() && importedBrief.suggestedName) {
      setName(importedBrief.suggestedName);
    }
    setPrompt((prev) => mergeImportedConversationPrompt(prev, importedConversation));
  }, [connected, importedBrief, importedConversation, name, runner]);

  async function create() {
    if (!name.trim() && !importedBrief?.suggestedName) {
      Alert.alert("Phone Backend", "Project name is required");
      return;
    }
    const effectivePrompt = mergeImportedConversationPrompt(prompt, importedConversation);
    const activePhoneKey = mobileAiProvider === "glm" ? glmKey.trim() : openAiKey.trim();
    if (codingMode === "phone" && effectivePrompt.trim() && !activePhoneKey) {
      Alert.alert(
        `${mobileAiProvider === "glm" ? "GLM" : "OpenAI"} key required`,
        `On-phone prompt or thread import needs your ${mobileAiProvider === "glm" ? "GLM" : "OpenAI"} API key.`,
      );
      return;
    }
    if (codingMode === "runner" && !connected) {
      Alert.alert("Connect a runner", "Remote coding needs a connected Yaver runner.");
      return;
    }
    if (codingMode === "runner" && startMode === "this-phone") {
      Alert.alert(
        "Pick a backend",
        "Remote runner coding starts after you create this project on a Yaver agent or your dev machine.",
      );
      return;
    }
    setCreating(true);
    try {
      if (codingMode === "phone" && effectivePrompt.trim() && openAiKey.trim()) {
        await saveLocalSecret(LOCAL_KEYS.openAiApiKey, openAiKey.trim());
      }
      if (codingMode === "phone" && effectivePrompt.trim() && glmKey.trim()) {
        await saveLocalSecret(LOCAL_KEYS.glmApiKey, glmKey.trim());
      }
      if (codingMode === "phone" && effectivePrompt.trim()) {
        await saveLocalSecret(LOCAL_KEYS.mobileCodingProvider, mobileAiProvider);
      }
      const draft =
        codingMode === "phone" && effectivePrompt.trim()
          ? await generatePhoneProjectDraftFromPrompt({
              provider: mobileAiProvider,
              apiKey: activePhoneKey,
              name: name.trim(),
              prompt: effectivePrompt,
              template,
            })
          : {};
      const spec = {
        name: name.trim() || importedBrief?.suggestedName || "Imported Project",
        template: draft.template ?? (prompt.trim() ? undefined : template),
        schema: draft.schema,
        auth: draft.auth,
        seed: draft.seed,
        app: draft.app,
        prompt: effectivePrompt || undefined,
        runner: effectivePrompt && codingMode === "runner" ? runner || undefined : undefined,
        importUrl: !effectivePrompt && importedConversation.trim() ? importedBrief?.sourceUrl : undefined,
        importContent: !effectivePrompt && importedConversation.trim() ? importedConversation.trim() : undefined,
        importTitle: !effectivePrompt && importedConversation.trim() ? importedBrief?.title : undefined,
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
        const cloudAuthToken = (await getLocalSecret(LOCAL_KEYS.yaverCloudToken)) ?? token ?? undefined;
        const target: PhonePushTarget = {
          kind: "yaver-cloud",
          cloudBaseUrl: YAVER_CLOUD_BASE,
          cloudAuthToken,
        };
        p = await createPhoneProjectAt(target, spec);
        await bindPhoneProjectToTarget(p.slug, target, { slug: p.slug, localUrl: "", browseUrl: "", project: p }, "Yaver Cloud");
      }

      if (!p) throw new Error("target returned no project");
      setName("");
      setPrompt("");
      setImportedConversation("");
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
            <Text style={[styles.stepTitle, { color: c.textPrimary }]}>
              {["Name your app", "Describe the app", "Who will code?", "Where should it live?", "Git setup"][step]}
            </Text>
            <Text style={[styles.stepSubtitle, { color: c.textMuted }]}>
              {[
                "Start with a short project name.",
                "Add a short brief and pick a starting template.",
                "Choose phone-side kickoff or a remote runner.",
                "Pick the backend location for the sandbox.",
                "Git is optional. Connect it now or skip it.",
              ][step]}
            </Text>
            <View style={styles.stepDots}>
              {[0, 1, 2, 3, 4].map((value) => (
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
                  placeholder="A simple app idea is enough. Example: Todo app with login"
                  placeholderTextColor={c.textMuted}
                  multiline
                  style={[styles.input, styles.promptInput, { color: c.textPrimary, borderColor: c.border }]}
                />
                <Text style={[styles.label, { color: c.textMuted, marginTop: 12 }]}>Template</Text>
                <ScrollView horizontal showsHorizontalScrollIndicator={false} contentContainerStyle={{ paddingTop: 6 }}>
                  {templates.map((t) => (
                    <Pressable
                      key={t.id}
                      onPress={() => setTemplate(t.id)}
                      style={[
                        styles.choiceCard,
                        {
                          width: 168,
                          marginRight: 10,
                          backgroundColor: template === t.id ? c.accent + "18" : c.bg,
                          borderColor: template === t.id ? c.accent : c.border,
                        },
                      ]}
                    >
                      <Text style={[styles.templateLabel, { color: c.textPrimary }]}>{t.label}</Text>
                      <Text style={[styles.muted, { color: c.textMuted }]} numberOfLines={3}>
                        {t.description}
                      </Text>
                    </Pressable>
                  ))}
                </ScrollView>
                <View style={[styles.importCard, { backgroundColor: c.bg, borderColor: c.border }]}>
                  <Text style={[styles.label, { color: c.textMuted }]}>Add conversation or share URL (optional)</Text>
                  <TextInput
                    value={importedConversation}
                    onChangeText={setImportedConversation}
                    placeholder="Optional: paste a Claude/ChatGPT/Codex share URL or copied conversation."
                    placeholderTextColor={c.textMuted}
                    multiline
                    style={[styles.input, styles.importInput, { color: c.textPrimary, borderColor: c.border }]}
                  />
                  {importedBrief ? (
                    <View style={styles.importMetaRow}>
                      <View style={[styles.importPill, { backgroundColor: c.accent + "18", borderColor: c.accent + "33" }]}>
                        <Text style={[styles.importPillText, { color: c.textPrimary }]}>{importedBrief.sourceLabel}</Text>
                      </View>
                      <Text style={[styles.muted, { color: c.textMuted, flex: 1 }]}>
                        {importedBrief.title || `${importedBrief.charCount} chars imported`}
                      </Text>
                    </View>
                  ) : (
                    <Text style={[styles.muted, { color: c.textMuted, marginTop: 8 }]}>
                      Start with just your own app brief, or add a conversation/share URL if you want Yaver to infer the technical plan from it.
                    </Text>
                  )}
                  <Pressable
                    onPress={() => void applyImportedConversation()}
                    disabled={analyzingImport}
                    style={[styles.btnSecondary, { borderColor: c.border, marginTop: 10, opacity: analyzingImport ? 0.6 : 1 }]}
                  >
                    {analyzingImport ? (
                      <ActivityIndicator color={c.textPrimary} />
                    ) : (
                      <Text style={[styles.btnText, { color: c.textPrimary }]}>Analyze thread and generate technical plan</Text>
                    )}
                  </Pressable>
                </View>
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
                      styles.choiceCard,
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
                    <Text style={[styles.label, { color: c.textMuted, marginTop: 12 }]}>AI provider</Text>
                    <View style={{ flexDirection: "row", gap: 8, marginTop: 4 }}>
                      {([
                        { id: "openai" as MobileAiProvider, label: "OpenAI" },
                        { id: "glm" as MobileAiProvider, label: "GLM" },
                      ]).map((provider) => {
                        const active = mobileAiProvider === provider.id;
                        return (
                          <Pressable
                            key={provider.id}
                            onPress={() => setMobileAiProvider(provider.id)}
                            style={[
                              styles.modeChip,
                              {
                                backgroundColor: active ? c.accent : c.bgCard,
                                borderColor: active ? c.accent : c.border,
                              },
                            ]}
                          >
                            <Text style={{ color: active ? c.bg : c.textPrimary, fontWeight: "600" }}>
                              {provider.label}
                            </Text>
                          </Pressable>
                        );
                      })}
                    </View>
                    <Text style={[styles.label, { color: c.textMuted, marginTop: 12 }]}>
                      {mobileAiProvider === "glm" ? "GLM API key" : "OpenAI API key"}
                    </Text>
                    <TextInput
                      value={mobileAiProvider === "glm" ? glmKey : openAiKey}
                      onChangeText={mobileAiProvider === "glm" ? setGlmKey : setOpenAiKey}
                      placeholder={mobileAiProvider === "glm" ? "zai_..." : "sk-..."}
                      placeholderTextColor={c.textMuted}
                      autoCapitalize="none"
                      autoCorrect={false}
                      spellCheck={false}
                      style={[styles.input, { color: c.textPrimary, borderColor: c.border }]}
                    />
                    <Text style={[styles.muted, { color: c.textMuted, marginTop: 6 }]}>
                      Only needed when you want Yaver to turn a prompt or imported thread into the first draft. Pure template starts work without it.
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
                    ...(canUseYaverCloud
                      ? [{
                          id: "yaver-cloud" as StartMode,
                          label: "Yaver Cloud",
                          sub: canUseCloudPreview ? "Private preview" : "Managed machine",
                          disabled: !connected,
                        }]
                      : []),
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
                      styles.choiceCard,
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
                <Text style={[styles.muted, { color: c.textMuted, marginTop: 8 }]}>
                  Phone-first works immediately. Agent, machine, and cloud targets can take over later for remote coding, app runs/tests on a dev box, and live mobile viewing of that box.
                </Text>
              </>
            ) : null}

            {step === 4 ? (
              <>
                <Text style={[styles.label, { color: c.textMuted }]}>Git</Text>
                {(
                  [
                    { id: "skip" as GitMode, label: "Skip Git", sub: "Create the sandbox first" },
                    { id: "later" as GitMode, label: "Later", sub: "Connect providers when ready" },
                    { id: "providers-now" as GitMode, label: "Connect now", sub: "Open Git Providers after create", disabled: !connected },
                  ] as const
                ).map((opt) => (
                  <Pressable
                    key={opt.id}
                    onPress={() => !(opt as any).disabled && setGitMode(opt.id)}
                    style={[
                      styles.choiceCard,
                      {
                        backgroundColor: gitMode === opt.id ? c.accent + "22" : "transparent",
                        borderColor: gitMode === opt.id ? c.accent : c.border,
                        opacity: (opt as any).disabled ? 0.5 : 1,
                      },
                    ]}
                  >
                    <Text style={[styles.templateLabel, { color: c.textPrimary }]}>{opt.label}</Text>
                    <Text style={[styles.muted, { color: c.textMuted }]}>{opt.sub}</Text>
                  </Pressable>
                ))}
                <View style={[styles.reviewCard, { backgroundColor: c.bg, borderColor: c.border }]}>
                  <Text style={[styles.reviewTitle, { color: c.textPrimary }]}>Ready to create</Text>
                  <Text style={[styles.muted, { color: c.textMuted }]}>
                    {name.trim() || "Untitled"} · {codingMode === "phone" ? "Phone AI" : "Remote runner"} ·{" "}
                    {startMode === "this-phone"
                      ? "This phone"
                      : startMode === "current-agent"
                        ? "Current agent"
                        : startMode === "dev-hw"
                          ? (selectedDevMachine?.name || "Dev machine")
                          : "Yaver Cloud"}
                  </Text>
                </View>
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
              {step < 4 ? (
                <Pressable
                  onPress={() => setStep((prev) => Math.min(4, prev + 1))}
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
      canUseYaverCloud,
      codingMode,
      openAiKey,
      activeDevice,
      applyImportedConversation,
      analyzingImport,
      selectedDevMachine,
      devMachines.length,
      importedConversation,
      importedBrief,
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
  stepTitle: {
    fontSize: 22,
    fontWeight: "700",
    letterSpacing: -0.3,
  },
  stepSubtitle: {
    fontSize: 13,
    lineHeight: 18,
    marginTop: 6,
  },
  stepDots: {
    flexDirection: "row",
    gap: 8,
    marginTop: 14,
    marginBottom: 18,
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
  importCard: {
    borderWidth: 1,
    borderRadius: 12,
    padding: 12,
    marginTop: 12,
  },
  importInput: {
    minHeight: 110,
    textAlignVertical: "top",
  },
  importMetaRow: {
    flexDirection: "row",
    alignItems: "center",
    gap: 8,
    marginTop: 8,
  },
  importPill: {
    borderWidth: 1,
    borderRadius: 999,
    paddingHorizontal: 10,
    paddingVertical: 5,
  },
  importPillText: {
    fontSize: 12,
    fontWeight: "600",
  },
  choiceCard: {
    borderWidth: 1,
    borderRadius: 12,
    padding: 12,
    marginTop: 8,
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
  reviewCard: {
    borderWidth: 1,
    borderRadius: 12,
    padding: 12,
    marginTop: 14,
  },
  reviewTitle: {
    fontSize: 14,
    fontWeight: "700",
    marginBottom: 4,
  },
  projectName: { fontSize: 17, fontWeight: "600" },
  stats: { fontSize: 12, marginTop: 6 },
  empty: { flex: 1, alignItems: "center", justifyContent: "center" },
});
