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
type GitProvider = "github" | "gitlab";
type RepoVisibility = "private" | "public";

// Survey is the optional Step 3. Questions are intentionally short
// + multiple-choice so the user can finish in 30 s on a phone, and
// each answer maps to a paragraph that gets concatenated into the
// description prompt sent to the LLM. Skipping the survey is a
// first-class option — if the user just types a description, the
// flow still works.
type SurveyAnswers = {
  platform?: "web" | "mobile" | "both";
  audience?: "myself" | "friends" | "customers" | "public";
  auth?: "none" | "apple" | "google" | "email";
  persistence?: "persist" | "ephemeral";
  theme?: "minimal" | "playful" | "professional";
};

const SURVEY_QUESTIONS: Array<{
  key: keyof SurveyAnswers;
  title: string;
  options: Array<{ value: string; label: string; sub?: string }>;
}> = [
  {
    key: "platform",
    title: "Where will it run?",
    options: [
      { value: "web", label: "Web only", sub: "Browser, no app store" },
      { value: "mobile", label: "Mobile only", sub: "iOS + Android" },
      { value: "both", label: "Web and mobile", sub: "Both ship together" },
    ],
  },
  {
    key: "audience",
    title: "Who's the user?",
    options: [
      { value: "myself", label: "Just me", sub: "Personal tool" },
      { value: "friends", label: "Friends or team", sub: "Small group" },
      { value: "customers", label: "Paying customers", sub: "Public + billing" },
      { value: "public", label: "Anyone", sub: "Public, free" },
    ],
  },
  {
    key: "auth",
    title: "How do users sign in?",
    options: [
      { value: "none", label: "No sign-in", sub: "Anonymous use" },
      { value: "apple", label: "Apple", sub: "Sign in with Apple" },
      { value: "google", label: "Google", sub: "Sign in with Google" },
      { value: "email", label: "Email + password", sub: "Classic" },
    ],
  },
  {
    key: "persistence",
    title: "Does it save data between sessions?",
    options: [
      { value: "persist", label: "Yes, remember everything", sub: "DB-backed" },
      { value: "ephemeral", label: "No, ephemeral only", sub: "Resets on reload" },
    ],
  },
  {
    key: "theme",
    title: "Visual style?",
    options: [
      { value: "minimal", label: "Clean & minimal", sub: "Functional" },
      { value: "playful", label: "Playful & colorful", sub: "Game-like" },
      { value: "professional", label: "Professional & dark", sub: "Pro tool" },
    ],
  },
];

function buildSurveyParagraph(answers: SurveyAnswers): string {
  if (!answers || Object.keys(answers).length === 0) return "";
  const lines: string[] = [];
  if (answers.platform) {
    lines.push(`Target: ${answers.platform === "both" ? "web + mobile" : answers.platform}`);
  }
  if (answers.audience) {
    lines.push(`Users: ${answers.audience}`);
  }
  if (answers.auth) {
    lines.push(`Auth: ${answers.auth === "none" ? "none / anonymous" : answers.auth}`);
  }
  if (answers.persistence) {
    lines.push(
      `Data: ${answers.persistence === "persist" ? "persist between sessions" : "ephemeral, no DB"}`,
    );
  }
  if (answers.theme) {
    lines.push(`Style: ${answers.theme}`);
  }
  return lines.length > 0 ? `[Survey]\n${lines.join("\n")}\n` : "";
}

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
  // Step 1 — Git config (optional). gitMode === "skip" means the
  // user explicitly bypassed git setup; in that case the
  // gitProvider/repoVisibility/repoName fields are ignored at create
  // time. Repo name auto-fills from the slug of the project name so
  // the user rarely has to type it.
  const [gitProvider, setGitProvider] = useState<GitProvider>("github");
  const [repoVisibility, setRepoVisibility] = useState<RepoVisibility>("private");
  const [repoName, setRepoName] = useState<string>("");
  const repoNameSlug = useMemo(() => {
    return name
      .toLowerCase()
      .trim()
      .replace(/[^a-z0-9]+/g, "-")
      .replace(/^-+|-+$/g, "");
  }, [name]);
  // Step 3 — Survey (skippable). When skipped, surveyAnswers stays
  // empty and the description prompt is the user's text alone.
  const [surveyIndex, setSurveyIndex] = useState(0);
  const [surveySkipped, setSurveySkipped] = useState(false);
  const [surveyAnswers, setSurveyAnswers] = useState<SurveyAnswers>({});
  // Optional logo URL — concatenated into the description prompt so
  // the LLM can use it as a visual reference. We accept any URL the
  // user can paste (CDN, gist, GitHub raw, etc.) — gallery upload is
  // a follow-up that needs an upload pipeline + storage.
  const [logoUrl, setLogoUrl] = useState("");
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
    // Only the three vibing-grade runners surface in the mobile UI.
    // Aider / aider-ollama / ollama are still installable on the
    // agent for advanced users from the CLI, but they're hidden here
    // because they don't fit the chat-style flow this app drives —
    // local Ollama models hallucinate file paths and Aider's
    // streaming format is too noisy for the mobile transcript.
    const RUNNER_WL = new Set(["claude", "claude-code", "codex", "opencode"]);
    const runners = activeDevice?.runners ?? [];
    return runners
      .filter((item) => RUNNER_WL.has((item.runnerId || "").toLowerCase()))
      .filter((item) => item.status === "running" || item.status === "queued" || item.status === "completed");
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
    // Survey answers (when not skipped) get prepended to the prompt
    // as a structured "[Survey]\nKey: value\n…" header so the LLM
    // wrapper has both the user's prose AND the multiple-choice
    // intent in one blob. Logo URL (when set) joins as a separate
    // [Brand] line so the LLM can fetch and reference it.
    const surveyParagraph = surveySkipped ? "" : buildSurveyParagraph(surveyAnswers);
    const brandParagraph = logoUrl.trim() ? `[Brand]\nLogo URL: ${logoUrl.trim()}\n` : "";
    const baseDescription = mergeImportedConversationPrompt(prompt, importedConversation);
    const effectivePrompt = [surveyParagraph, brandParagraph, baseDescription]
      .filter(Boolean)
      .join("\n");
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
      setRepoName("");
      setLogoUrl("");
      setSurveyAnswers({});
      setSurveyIndex(0);
      setSurveySkipped(false);
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
              {[
                "1. Name your app",
                "2. Git (optional)",
                "3. Who will code?",
                "4. Quick survey (optional)",
                "5. Describe the app",
              ][step]}
            </Text>
            <Text style={[styles.stepSubtitle, { color: c.textMuted }]}>
              {[
                "A short project name. We slugify it for git, paths, and the SQLite file.",
                "GitHub or GitLab, public or private. You can skip — Yaver works without git.",
                "Phone-side LLM (BYOK) or a remote Yaver runner you've signed in.",
                "Five quick multiple-choice questions. Skip if you'd rather just type.",
                "Required. Tell Yaver what you're building, in your own words.",
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
                {/* Git is optional. Skip path sets gitMode === "skip"
                 * and the create() flow ignores the provider /
                 * visibility / repo-name fields. The reasoned default
                 * is GitHub + private, which is what most solo
                 * developers want. */}
                <View style={{ flexDirection: "row", gap: 8, marginTop: 4 }}>
                  {[
                    { id: "skip" as GitMode, label: "Skip git" },
                    { id: "providers-now" as GitMode, label: "Configure now" },
                  ].map((opt) => {
                    const active = gitMode === opt.id;
                    return (
                      <Pressable
                        key={opt.id}
                        onPress={() => setGitMode(opt.id)}
                        style={[
                          styles.modeChip,
                          {
                            backgroundColor: active ? c.accent : c.bgCard,
                            borderColor: active ? c.accent : c.border,
                            flex: 1,
                          },
                        ]}
                      >
                        <Text style={{ color: active ? c.bg : c.textPrimary, fontWeight: "600", textAlign: "center" }}>
                          {opt.label}
                        </Text>
                      </Pressable>
                    );
                  })}
                </View>
                {gitMode !== "skip" ? (
                  <>
                    <Text style={[styles.label, { color: c.textMuted, marginTop: 14 }]}>Provider</Text>
                    <View style={{ flexDirection: "row", gap: 8 }}>
                      {([
                        { id: "github" as GitProvider, label: "GitHub" },
                        { id: "gitlab" as GitProvider, label: "GitLab" },
                      ]).map((opt) => {
                        const active = gitProvider === opt.id;
                        return (
                          <Pressable
                            key={opt.id}
                            onPress={() => setGitProvider(opt.id)}
                            style={[
                              styles.modeChip,
                              {
                                backgroundColor: active ? c.accent : c.bgCard,
                                borderColor: active ? c.accent : c.border,
                                flex: 1,
                              },
                            ]}
                          >
                            <Text style={{ color: active ? c.bg : c.textPrimary, fontWeight: "600", textAlign: "center" }}>
                              {opt.label}
                            </Text>
                          </Pressable>
                        );
                      })}
                    </View>
                    <Text style={[styles.label, { color: c.textMuted, marginTop: 12 }]}>Visibility</Text>
                    <View style={{ flexDirection: "row", gap: 8 }}>
                      {([
                        { id: "private" as RepoVisibility, label: "Private", sub: "Only you" },
                        { id: "public" as RepoVisibility, label: "Public", sub: "Anyone can read" },
                      ]).map((opt) => {
                        const active = repoVisibility === opt.id;
                        return (
                          <Pressable
                            key={opt.id}
                            onPress={() => setRepoVisibility(opt.id)}
                            style={[
                              styles.choiceCard,
                              {
                                backgroundColor: active ? c.accent + "22" : "transparent",
                                borderColor: active ? c.accent : c.border,
                                flex: 1,
                              },
                            ]}
                          >
                            <Text style={[styles.templateLabel, { color: c.textPrimary }]}>{opt.label}</Text>
                            <Text style={[styles.muted, { color: c.textMuted }]}>{opt.sub}</Text>
                          </Pressable>
                        );
                      })}
                    </View>
                    <Text style={[styles.label, { color: c.textMuted, marginTop: 12 }]}>Repo name</Text>
                    <TextInput
                      value={repoName || repoNameSlug}
                      onChangeText={setRepoName}
                      placeholder={repoNameSlug || "my-app"}
                      placeholderTextColor={c.textMuted}
                      autoCapitalize="none"
                      autoCorrect={false}
                      style={[styles.input, { color: c.textPrimary, borderColor: c.border }]}
                    />
                    <Text style={[styles.muted, { color: c.textMuted, marginTop: 6 }]}>
                      Defaults to {repoNameSlug || "the slug of your project name"}. Leave blank to use that.
                    </Text>
                  </>
                ) : (
                  <Text style={[styles.muted, { color: c.textMuted, marginTop: 12 }]}>
                    No git for now. You can connect a provider later from the project's Git tab.
                  </Text>
                )}
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

                {codingMode === "runner" ? (
                  <>
                    <Text style={[styles.label, { color: c.textMuted, marginTop: 12 }]}>Runner</Text>
                    {availableRunners.length === 0 ? (
                      // No authed runner on the picked machine —
                      // surface an actionable provisioning hint
                      // instead of letting the user pick a runner
                      // that'll fail at task creation. The Devices
                      // tab is where the existing RunnerAuthModal +
                      // device-auth flow lives, so we route there
                      // rather than reimplementing it inside this
                      // wizard.
                      <View style={[styles.reviewCard, { backgroundColor: c.bg, borderColor: c.border, marginTop: 4 }]}>
                        <Text style={[styles.reviewTitle, { color: c.textPrimary }]}>
                          {connected ? "No coding runner is signed in yet" : "Connect a Yaver machine first"}
                        </Text>
                        <Text style={[styles.muted, { color: c.textMuted, marginTop: 4 }]}>
                          {connected
                            ? "Open Devices, pick this machine, and sign in Claude / Codex (browser device-auth, no SSH) or paste a GLM API key for OpenCode. Come back here once one runner is ready."
                            : "Pair a Yaver machine from the Devices tab, then return here. Phone-side coding works without one."}
                        </Text>
                        <Pressable
                          onPress={() => router.push("/(tabs)/devices" as any)}
                          style={[styles.btnSecondary, { borderColor: c.border, marginTop: 10 }]}
                        >
                          <Text style={[styles.btnText, { color: c.textPrimary }]}>
                            Open Devices →
                          </Text>
                        </Pressable>
                      </View>
                    ) : (
                      <ScrollView horizontal showsHorizontalScrollIndicator={false}>
                        {availableRunners.map((item) => ({
                          id: item.runnerId,
                          label: item.title || item.runnerId,
                        })).map((item) => {
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
                    )}
                  </>
                ) : null}
              </>
            ) : null}

            {step === 3 ? (
              // Survey is optional and one-question-at-a-time. The
              // surveyIndex tracks which question is shown; the user
              // can skip from any point. surveyAnswers are
              // concatenated into the description prompt at create
              // time via buildSurveyParagraph(). When surveySkipped
              // is true we render a "Survey skipped" hint and the
              // Next button just advances to the description step.
              <>
                {!surveySkipped && surveyIndex < SURVEY_QUESTIONS.length ? (
                  <>
                    <View style={{ flexDirection: "row", justifyContent: "space-between", marginBottom: 6 }}>
                      <Text style={[styles.label, { color: c.textMuted }]}>
                        Question {surveyIndex + 1} of {SURVEY_QUESTIONS.length}
                      </Text>
                      <Pressable onPress={() => setSurveySkipped(true)}>
                        <Text style={{ color: c.accent, fontSize: 12, fontWeight: "600" }}>Skip survey</Text>
                      </Pressable>
                    </View>
                    <Text style={[styles.stepTitle, { color: c.textPrimary, marginBottom: 8 }]}>
                      {SURVEY_QUESTIONS[surveyIndex].title}
                    </Text>
                    {SURVEY_QUESTIONS[surveyIndex].options.map((opt) => {
                      const key = SURVEY_QUESTIONS[surveyIndex].key;
                      const active = (surveyAnswers as any)[key] === opt.value;
                      return (
                        <Pressable
                          key={opt.value}
                          onPress={() => {
                            // Record + auto-advance. Last question
                            // doesn't auto-advance; the user has to
                            // tap Next so they can review.
                            setSurveyAnswers((prev) => ({ ...prev, [key]: opt.value as any }));
                            if (surveyIndex < SURVEY_QUESTIONS.length - 1) {
                              setSurveyIndex(surveyIndex + 1);
                            }
                          }}
                          style={[
                            styles.choiceCard,
                            {
                              backgroundColor: active ? c.accent + "22" : "transparent",
                              borderColor: active ? c.accent : c.border,
                            },
                          ]}
                        >
                          <Text style={[styles.templateLabel, { color: c.textPrimary }]}>{opt.label}</Text>
                          {opt.sub ? (
                            <Text style={[styles.muted, { color: c.textMuted }]}>{opt.sub}</Text>
                          ) : null}
                        </Pressable>
                      );
                    })}
                    {surveyIndex > 0 ? (
                      <Pressable
                        onPress={() => setSurveyIndex(Math.max(0, surveyIndex - 1))}
                        style={{ marginTop: 8 }}
                      >
                        <Text style={{ color: c.textMuted, fontSize: 12 }}>← Previous question</Text>
                      </Pressable>
                    ) : null}
                  </>
                ) : (
                  <View style={[styles.reviewCard, { backgroundColor: c.bg, borderColor: c.border }]}>
                    <Text style={[styles.reviewTitle, { color: c.textPrimary }]}>
                      {surveySkipped ? "Survey skipped" : "Survey done"}
                    </Text>
                    <Text style={[styles.muted, { color: c.textMuted, marginTop: 4 }]}>
                      {surveySkipped
                        ? "No survey answers will be added to your prompt."
                        : "Your answers will be folded into the description as a header on the next step."}
                    </Text>
                    {!surveySkipped ? (
                      <Pressable
                        onPress={() => setSurveyIndex(0)}
                        style={{ marginTop: 8 }}
                      >
                        <Text style={{ color: c.accent, fontSize: 12, fontWeight: "600" }}>Edit answers</Text>
                      </Pressable>
                    ) : (
                      <Pressable
                        onPress={() => {
                          setSurveySkipped(false);
                          setSurveyIndex(0);
                        }}
                        style={{ marginTop: 8 }}
                      >
                        <Text style={{ color: c.accent, fontSize: 12, fontWeight: "600" }}>Take the survey</Text>
                      </Pressable>
                    )}
                  </View>
                )}
              </>
            ) : null}

            {step === 4 ? (
              // Description is mandatory — the create() flow gates on
              // a non-empty prompt OR a non-empty importedConversation
              // brief, so the user can't ship a no-context skeleton.
              // Survey answers (if taken) appear above the textarea
              // as a plain-text recap, and get prepended to the
              // prompt at submit time via buildSurveyParagraph().
              <>
                {!surveySkipped && Object.keys(surveyAnswers).length > 0 ? (
                  <View style={[styles.reviewCard, { backgroundColor: c.bg, borderColor: c.border, marginBottom: 12 }]}>
                    <Text style={[styles.reviewTitle, { color: c.textPrimary }]}>From your survey</Text>
                    <Text style={[styles.muted, { color: c.textMuted, marginTop: 4 }]}>
                      {buildSurveyParagraph(surveyAnswers).replace(/^\[Survey\]\n/, "")}
                    </Text>
                  </View>
                ) : null}
                <Text style={[styles.label, { color: c.textMuted }]}>Logo URL (optional)</Text>
                <TextInput
                  value={logoUrl}
                  onChangeText={setLogoUrl}
                  placeholder="https://… raw image URL Yaver can fetch"
                  placeholderTextColor={c.textMuted}
                  autoCapitalize="none"
                  autoCorrect={false}
                  spellCheck={false}
                  keyboardType="url"
                  style={[styles.input, { color: c.textPrimary, borderColor: c.border, marginBottom: 12 }]}
                />
                <Text style={[styles.label, { color: c.textMuted }]}>Describe the app *</Text>
                <TextInput
                  value={prompt}
                  onChangeText={setPrompt}
                  placeholder={`Tell Yaver what you're building, in your own words. Required.

Example: "Browser-based checkers with a tiny lobby. Two friends paste a 4-letter code into the same URL and play. Persistent across reloads for 24 h. No accounts. Looks playful and colourful."`}
                  placeholderTextColor={c.textMuted}
                  multiline
                  style={[styles.input, styles.promptInput, { color: c.textPrimary, borderColor: c.border, minHeight: 180 }]}
                />
                <Text style={[styles.muted, { color: c.textMuted, marginTop: 6 }]}>
                  Yaver will use this (plus the survey, if you took it) to draft the schema, seed data, and a starter UI.
                </Text>
                <View style={[styles.reviewCard, { backgroundColor: c.bg, borderColor: c.border, marginTop: 12 }]}>
                  <Text style={[styles.reviewTitle, { color: c.textPrimary }]}>Ready to create</Text>
                  <Text style={[styles.muted, { color: c.textMuted }]}>
                    {name.trim() || "Untitled"} ·{" "}
                    {gitMode === "skip"
                      ? "no git"
                      : `${gitProvider} (${repoVisibility})`} ·{" "}
                    {codingMode === "phone" ? `Phone AI (${mobileAiProvider.toUpperCase()})` : "Remote runner"} ·{" "}
                    {surveySkipped || Object.keys(surveyAnswers).length === 0
                      ? "no survey"
                      : `${Object.keys(surveyAnswers).length}-Q survey`}
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
              {step < 4 ? (() => {
                // Per-step validation. Mandatory steps: 0 (name) and
                // 4 (description, gated separately on the Create
                // button). 1 (git), 2 (LLM placement), 3 (survey)
                // all have safe defaults so Next is always
                // available there.
                const nameOk = name.trim().length > 0;
                const canAdvance = step === 0 ? nameOk : true;
                return (
                  <Pressable
                    disabled={!canAdvance}
                    onPress={() => setStep((prev) => Math.min(4, prev + 1))}
                    style={[
                      styles.btn,
                      { backgroundColor: c.accent, flex: 1, opacity: canAdvance ? 1 : 0.4 },
                    ]}
                  >
                    <Text style={[styles.btnText, { color: c.bg }]}>
                      {!canAdvance && step === 0 ? "Name required" : "Next"}
                    </Text>
                  </Pressable>
                );
              })() : (() => {
                const descOk = prompt.trim().length > 0;
                return (
                  <Pressable
                    onPress={create}
                    disabled={creating || !descOk}
                    style={[
                      styles.btn,
                      { backgroundColor: c.accent, flex: 1, opacity: creating || !descOk ? 0.4 : 1 },
                    ]}
                  >
                    {creating ? (
                      <ActivityIndicator color={c.bg} />
                    ) : (
                      <Text style={[styles.btnText, { color: c.bg }]}>
                        {!descOk ? "Description required" : "Create"}
                      </Text>
                    )}
                  </Pressable>
                );
              })()}
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
