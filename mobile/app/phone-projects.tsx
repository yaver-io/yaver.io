import React, { useCallback, useEffect, useMemo, useRef, useState } from "react";
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
import { useDevice, type Device, type RunnerInfo } from "../src/context/DeviceContext";
import { AppScreenHeader } from "../src/components/AppScreenHeader";
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
  generateClarifyingQuestions,
  listPhoneProjects,
  listPhoneTemplates,
  sharePhoneProject,
  joinPhoneShare,
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
  palette?: "slate" | "zinc" | "blue" | "emerald" | "rose" | "amber" | "violet" | "neutral";
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
  {
    key: "palette",
    title: "Color palette?",
    options: [
      { value: "slate", label: "Slate", sub: "Cool grey-blue" },
      { value: "zinc", label: "Zinc", sub: "Neutral grey" },
      { value: "blue", label: "Blue", sub: "Classic tech" },
      { value: "emerald", label: "Emerald", sub: "Fresh + green" },
      { value: "rose", label: "Rose", sub: "Warm + pink" },
      { value: "amber", label: "Amber", sub: "Yellow + orange" },
      { value: "violet", label: "Violet", sub: "Purple + bold" },
      { value: "neutral", label: "Neutral", sub: "Black & white" },
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
  if (answers.palette) {
    lines.push(`Palette: ${answers.palette}`);
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
  const mobileAiProviderTouchedRef = useRef(false);

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
  // Pre-seed the first survey question ("Where will it run?") to the
  // third option — "Web and mobile" — so the survey opens with a
  // sensible, visible default the user can keep or change.
  const [surveyAnswers, setSurveyAnswers] = useState<SurveyAnswers>({ platform: "both" });
  // Optional logo URL — concatenated into the description prompt so
  // the LLM can use it as a visual reference. We accept any URL the
  // user can paste (CDN, gist, GitHub raw, etc.) — gallery upload is
  // a follow-up that needs an upload pipeline + storage.
  const [logoUrl, setLogoUrl] = useState("");
  // Optional primary-color hex override. Pairs with the survey's
  // palette pick — palette is a named choice, hex is a free-form
  // override for users who already know the exact brand colour.
  // Loose validation only (CSS hex shape); blank means "no override".
  const [primaryHex, setPrimaryHex] = useState("");
  // Optional refinement loop: after the user types a description,
  // they can tap "Refine with AI" to have the LLM check whether
  // 1-3 follow-up questions would meaningfully shape the schema.
  // Answers are appended to the prompt as a [Clarifications] block
  // before generation. The Refine path is purely opt-in — the user
  // can always click Create directly to force-initialise without
  // going through it.
  const [refineLoading, setRefineLoading] = useState(false);
  const [refineQuestions, setRefineQuestions] = useState<Array<{ id: string; title: string; placeholder?: string }>>([]);
  const [refineAnswers, setRefineAnswers] = useState<Record<string, string>>({});
  const [refineUsed, setRefineUsed] = useState(false);
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
  const activeRunnerDevice = useMemo(() => {
    if (activeDevice && !activeDevice.needsAuth) return activeDevice;
    return null;
  }, [activeDevice]);
  // Yaver's three first-class runners — the only ones we surface
  // anywhere in the product. opencode wraps the long tail of
  // providers (Anthropic / OpenAI / OpenRouter / Ollama / GLM /
  // ZAI / …) via its own BYOK config, so users who want a specific
  // model still reach it through opencode rather than yaver
  // shipping a wrapper for every CLI. Reused for both the connected
  // machine and a picked "other online box" so the runner-auth gate
  // is identical across remote targets.
  const runnersForDevice = useCallback((dev: Device | null | undefined) => {
    if (!dev || dev.needsAuth) return [] as RunnerInfo[];
    const RUNNER_WL = new Set(["claude", "claude-code", "codex", "opencode"]);
    return (dev.runners ?? [])
      .filter((item) => RUNNER_WL.has((item.runnerId || "").toLowerCase()))
      .filter((item) => item.status === "running" || item.status === "queued" || item.status === "completed");
  }, []);
  const availableRunners = useMemo(
    () => runnersForDevice(activeRunnerDevice),
    [runnersForDevice, activeRunnerDevice],
  );
  // Runners signed in on the picked "other online box". The dev-hw
  // create path targets selectedDevMachine, so its runner-auth state
  // — not the active device's — is what gates finalization there.
  const devMachineRunners = useMemo(
    () => runnersForDevice(selectedDevMachine),
    [runnersForDevice, selectedDevMachine],
  );
  const runnerChoiceEnabled = !!activeRunnerDevice;
  useEffect(() => {
    // Seed a default runner from whichever remote target is in play —
    // the picked online box wins when dev-hw is selected, otherwise the
    // connected machine's runners.
    const seed = startMode === "dev-hw" ? devMachineRunners : availableRunners;
    if (!runner && seed.length) {
      setRunner(seed[0].runnerId);
    }
  }, [availableRunners, devMachineRunners, startMode, runner]);
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
      if (!mobileAiProviderTouchedRef.current) {
        setMobileAiProvider(savedProvider);
      }
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
    if (!runnerChoiceEnabled && codingMode === "runner") {
      setCodingMode("phone");
    }
  }, [codingMode, runnerChoiceEnabled]);
  useEffect(() => {
    if (!connected && startMode !== "this-phone" && startMode !== "yaver-cloud") {
      setStartMode("this-phone");
    }
  }, [connected, startMode]);
  useEffect(() => {
    if (codingMode === "runner" && runnerChoiceEnabled) {
      setStartMode("current-agent");
    }
  }, [codingMode, runnerChoiceEnabled]);

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
      const raw = e instanceof Error ? e.message : String(e);
      setErr(
        /network|fetch|timeout|econn|offline|unreach/i.test(raw)
          ? "Couldn't reach the server. Check your connection, then pull to retry."
          : `Couldn't load your projects (${raw}).`,
      );
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
        const raw = e instanceof Error ? e.message : "";
        Alert.alert(
          "Import analysis",
          `Analysis failed — falling back to local brief generation.${raw ? `\n\n${raw}` : ""}`,
        );
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
    const brandLines: string[] = [];
    if (logoUrl.trim()) brandLines.push(`Logo URL: ${logoUrl.trim()}`);
    if (primaryHex.trim()) brandLines.push(`Primary color: ${primaryHex.trim()}`);
    const brandParagraph = brandLines.length > 0 ? `[Brand]\n${brandLines.join("\n")}\n` : "";
    // Clarifying-question answers (if the user used the Refine pass
    // and typed answers) get folded in as a [Clarifications] block
    // so the LLM sees them alongside the survey + prose.
    const refineLines = Object.entries(refineAnswers)
      .map(([id, val]) => {
        const q = refineQuestions.find((x) => x.id === id);
        const trimmed = (val || "").trim();
        if (!q || !trimmed) return "";
        return `${q.title} ${trimmed}`;
      })
      .filter(Boolean);
    const refineParagraph = refineLines.length > 0
      ? `[Clarifications]\n${refineLines.join("\n")}\n`
      : "";
    const baseDescription = mergeImportedConversationPrompt(prompt, importedConversation);
    const effectivePrompt = [surveyParagraph, brandParagraph, refineParagraph, baseDescription]
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
    if (codingMode === "runner" && startMode !== "yaver-cloud" && !connected) {
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

      // Real GitHub / GitLab repo creation when the user picked
      // "Configure now" in step 1. Best-effort: we surface the
      // clone URL via Alert when it succeeds and silently skip
      // when the agent is too old (returns null) or no PAT is
      // configured for the chosen provider (the agent returns
      // 412 which becomes a thrown error; we catch + show it).
      // The project is already saved at this point — the repo
      // creation enriches it but doesn't gate it.
      if (gitMode === "providers-now" && connected) {
        try {
          const repo = await quicClient.gitProviderRepoCreate({
            provider: gitProvider,
            name: (repoName.trim() || repoNameSlug || p.slug),
            visibility: repoVisibility,
            description: prompt.trim().slice(0, 200),
            writeSandbox: true,
          });
          if (repo) {
            Alert.alert(
              "Repo created",
              `${repo.fullName} on ${gitProvider}.com${repo.sandboxWritten ? "\n\nyaver.workspace.yaml committed — repo flagged as Yaver-sandbox-aware." : ""}\n\n${repo.cloneUrl}`,
            );
          } else {
            // Agent too old for this endpoint — record the
            // preference and let the user create the repo via the
            // dashboard later.
            console.warn("[phone-projects] git/provider/repo/create unavailable on this agent (older than v1.99.91)");
          }
        } catch (gitErr: any) {
          // Don't kill the project create on a repo-create failure.
          // Most common cause: no PAT set for the chosen provider.
          Alert.alert(
            "Project saved, repo not created",
            gitErr?.message?.includes("412")
              ? `No ${gitProvider} token is set up on this machine. Add one from the dashboard's Git tab, then run the wizard's "Configure now" path again or push from the project later.`
              : (gitErr?.message ?? "Repo creation failed; project itself is saved."),
          );
        }
      }
      setName("");
      setPrompt("");
      setImportedConversation("");
      setRunner(availableRunners[0]?.runnerId ?? "");
      setGitMode("skip");
      setRepoName("");
      setLogoUrl("");
      setRefineQuestions([]);
      setRefineAnswers({});
      setRefineUsed(false);
      setSurveyAnswers({ platform: "both" });
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
      const raw = e instanceof Error ? e.message : "";
      Alert.alert(
        "Phone Backend",
        `Couldn't create the project. Check your connection and try again.${raw ? `\n\n${raw}` : ""}`,
      );
    } finally {
      setCreating(false);
    }
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
            const raw = e instanceof Error ? e.message : "";
            Alert.alert(
              "Phone Backend",
              `Couldn't delete the project. Try again in a moment.${raw ? `\n\n${raw}` : ""}`,
            );
          }
        },
      },
    ]);
  }

  async function share(p: PhoneProject) {
    try {
      const sh = await sharePhoneProject(p.slug);
      const where = sh.hostedConvexUrl
        ? `\n\nFriends see your live backend:\n${sh.hostedConvexUrl}`
        : "";
      Alert.alert(
        "Share with a friend",
        `Code: ${sh.code}\n\nThey open Yaver → "Join by code" and enter it to try "${p.name}".${where}`,
        [{ text: "OK" }],
      );
    } catch (e: any) {
      const raw = e instanceof Error ? e.message : "";
      Alert.alert(
        "Share",
        `Couldn't create a share code. Check your connection and try again.${raw ? `\n\n${raw}` : ""}`,
      );
    }
  }

  function projectActions(p: PhoneProject) {
    Alert.alert(p.name, undefined, [
      { text: "Share with a friend", onPress: () => void share(p) },
      { text: "Delete", style: "destructive", onPress: () => void remove(p) },
      { text: "Cancel", style: "cancel" },
    ]);
  }

  function joinByCode() {
    const go = async (code: string) => {
      const trimmed = (code || "").trim();
      if (!trimmed) return;
      try {
        const sh = await joinPhoneShare(trimmed);
        Alert.alert(
          "Joined",
          `"${sh.name}" is ready.${
            sh.hostedConvexUrl ? `\nBackend: ${sh.hostedConvexUrl}` : ""
          }`,
          [
            {
              text: "Open",
              onPress: () =>
                router.navigate(`/phone-project/${sh.slug}` as any),
            },
            { text: "Later", style: "cancel" },
          ],
        );
        await load();
      } catch (e: any) {
        // Friendly fallback FIRST; raw reason only as trailing detail.
        const raw = e instanceof Error ? e.message : "";
        Alert.alert(
          "Join by code",
          `Couldn't join with that code — it may be invalid or expired. Ask the host to resend.${raw ? `\n\n${raw}` : ""}`,
        );
      }
    };
    if (Platform.OS === "ios" && (Alert as any).prompt) {
      (Alert as any).prompt(
        "Join by code",
        "Enter the code a friend shared with you.",
        [
          { text: "Cancel", style: "cancel" },
          { text: "Join", onPress: (v: string) => void go(v) },
        ],
        "plain-text",
      );
    } else {
      Alert.alert(
        "Join by code",
        "Code entry is available on iOS. On Android, ask the host to push the project to your device.",
      );
    }
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
                setCodingMode("phone");
                setShowForm(true);
              }}
              style={[styles.btn, { backgroundColor: c.accent, marginTop: 12 }]}
            >
              <Text style={[styles.btnText, { color: c.bg }]}>+ New mobile app</Text>
            </Pressable>
            <Text style={[styles.muted, { color: c.textMuted, marginTop: 8 }]}>
              {connected ? "Start on your phone or a Yaver backend." : "Runs locally on this phone."}
            </Text>
            {projects.length > 0 ? (
              <Text style={[styles.muted, { color: c.textMuted, marginTop: 4 }]}>
                Or tap one of your {projects.length === 1 ? "existing project" : `${projects.length} existing projects`} below to open it. Long-press to share.
              </Text>
            ) : null}
            <Pressable
              onPress={() => joinByCode()}
              style={[
                styles.btn,
                { backgroundColor: "transparent", borderWidth: 1, borderColor: c.border, marginTop: 10 },
              ]}
            >
              <Text style={[styles.btnText, { color: c.textPrimary }]}>Join by code</Text>
            </Pressable>
          </>
        ) : (
          <View style={[styles.card, { backgroundColor: c.bgCard, borderColor: c.border, marginTop: 12 }]}>
            <Text style={[styles.stepTitle, { color: c.textPrimary }]}>
              {[
                "1. Name your app",
                "2. Git (optional)",
                "3. Where should it run?",
                "4. Quick survey (optional)",
                "5. Describe the app",
              ][step]}
            </Text>
            <Text style={[styles.stepSubtitle, { color: c.textMuted }]}>
              {[
                "You can change this later.",
                "GitHub or GitLab, public or private. You can skip — Yaver works without git.",
                "Choose this phone, your connected machine, another online box, or Yaver Cloud.",
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
                <Text style={[styles.label, { color: c.textMuted, marginTop: 2 }]}>App name</Text>
                <TextInput
                  value={name}
                  onChangeText={setName}
                  placeholder="My app"
                  placeholderTextColor={c.textMuted}
                  autoFocus
                  returnKeyType="next"
                  style={[
                    styles.input,
                    {
                      color: c.textPrimary,
                      // Filled, inset surface (darker than the card) +
                      // an accent border once there's text so the field
                      // reads unambiguously as an editable input rather
                      // than a label.
                      backgroundColor: c.bg,
                      borderColor: name.trim() ? c.accent : c.border,
                    },
                  ]}
                />
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
                <Text style={[styles.label, { color: c.textMuted }]}>Where should this sandbox run?</Text>
                {(
                  [
                    {
                      id: "this-phone" as StartMode,
                      label: "This phone",
                      sub: "Private local sandbox. Works without a remote box.",
                    },
                    {
                      id: "current-agent" as StartMode,
                      label: "Connected machine",
                      sub: activeRunnerDevice
                        ? `${activeRunnerDevice.name} will build and run it`
                        : "Connect a Yaver machine first",
                    },
                    ...(devMachines.length > 0
                      ? [{
                          id: "dev-hw" as StartMode,
                          label: "Other online box",
                          sub: selectedDevMachine
                            ? `${selectedDevMachine.name} selected`
                            : "Pick a Mac, Linux box, or Pi",
                        }]
                      : []),
                    ...(canUseYaverCloud
                      ? [{
                          id: "yaver-cloud" as StartMode,
                          label: "Yaver Cloud",
                          sub: "Managed machine. No local computer needed.",
                        }]
                      : []),
                  ]
                ).map((opt) => (
                  <Pressable
                    key={opt.id}
                    onPress={() => {
                      setStartMode(opt.id);
                      setCodingMode(opt.id === "this-phone" ? "phone" : "runner");
                    }}
                    style={[
                      styles.choiceCard,
                      {
                        backgroundColor: startMode === opt.id ? c.accent + "22" : "transparent",
                        borderColor: startMode === opt.id ? c.accent : c.border,
                      },
                    ]}
                  >
                    <Text style={[styles.templateLabel, { color: c.textPrimary }]}>{opt.label}</Text>
                    <Text style={[styles.muted, { color: c.textMuted }]} numberOfLines={1}>{opt.sub}</Text>
                  </Pressable>
                ))}

                {startMode === "this-phone" ? (
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
                            onPress={() => {
                              mobileAiProviderTouchedRef.current = true;
                              setMobileAiProvider(provider.id);
                            }}
                            hitSlop={8}
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

                {startMode !== "this-phone" ? (
                  <>
                    <Text style={[styles.label, { color: c.textMuted, marginTop: 12 }]}>Backend</Text>
                    <View style={[styles.reviewCard, { backgroundColor: c.bg, borderColor: c.border, marginTop: 4 }]}>
                      <Text style={[styles.reviewTitle, { color: c.textPrimary }]}>
                        {startMode === "yaver-cloud"
                          ? "Yaver Cloud selected"
                          : startMode === "dev-hw"
                            ? selectedDevMachine
                              ? "Online box selected"
                              : "Pick an online box"
                            : activeRunnerDevice
                              ? "Connected machine ready"
                              : "No machine connected"}
                      </Text>
                      <Text style={[styles.muted, { color: c.textMuted, marginTop: 4 }]}>
                        {startMode === "yaver-cloud"
                          ? "Yaver will create this sandbox on a managed cloud machine."
                          : startMode === "dev-hw"
                            ? selectedDevMachine
                              ? `${selectedDevMachine.name} will own this sandbox.`
                              : "Choose which online box should own this sandbox."
                            : activeRunnerDevice
                              ? `${activeRunnerDevice.name} is connected. This project will be created there.`
                              : "Open Devices to connect a Yaver machine, then come back and select Connected machine."}
                      </Text>
                      <View style={{ flexDirection: "row", gap: 8, marginTop: 10 }}>
                        {startMode === "current-agent" ? (
                          <Pressable
                            onPress={() => router.push("/(tabs)/devices" as any)}
                            style={[styles.btnSecondary, { borderColor: c.border, flex: 1 }]}
                          >
                            <Text style={[styles.btnText, { color: c.textPrimary }]}>
                              {activeRunnerDevice ? "Open Devices" : "Connect machine"}
                            </Text>
                          </Pressable>
                        ) : null}
                        <Pressable
                          onPress={() => {
                            setCodingMode("phone");
                            setStartMode("this-phone");
                          }}
                          style={[styles.btnSecondary, { borderColor: c.border, flex: 1 }]}
                        >
                          <Text style={[styles.btnText, { color: c.textPrimary }]}>Use this phone instead</Text>
                        </Pressable>
                      </View>
                    </View>

                    {/* "Other online box" — list the user's online dev
                        machines inline and let them tap one, rather than
                        bouncing through a native Alert. Selecting a box
                        re-derives its runner-auth state below. */}
                    {startMode === "dev-hw" ? (
                      <>
                        <Text style={[styles.label, { color: c.textMuted, marginTop: 12 }]}>Choose a machine</Text>
                        {devMachines.map((m) => {
                          const active = selectedDevMachineId === m.id;
                          const authed = !m.needsAuth;
                          return (
                            <Pressable
                              key={m.id}
                              onPress={() => setSelectedDevMachineId(m.id)}
                              style={[
                                styles.choiceCard,
                                {
                                  backgroundColor: active ? c.accent + "22" : "transparent",
                                  borderColor: active ? c.accent : c.border,
                                },
                              ]}
                            >
                              <Text style={[styles.templateLabel, { color: c.textPrimary }]}>
                                {m.name}{m.local ? "  (LAN)" : ""}
                              </Text>
                              <Text style={[styles.muted, { color: c.textMuted }]} numberOfLines={1}>
                                {m.os || "machine"}
                                {authed
                                  ? runnersForDevice(m).length > 0
                                    ? ` · ${runnersForDevice(m).length} runner${runnersForDevice(m).length === 1 ? "" : "s"} ready`
                                    : " · no runner signed in"
                                  : " · needs auth"}
                              </Text>
                            </Pressable>
                          );
                        })}
                      </>
                    ) : null}

                    {startMode === "current-agent" || startMode === "dev-hw" ? (
                      <>
                    {(() => {
                      // The runner-auth gate is identical for both remote
                      // targets; only the device + its runner list differ.
                      const runnerDevice = startMode === "dev-hw" ? selectedDevMachine : activeRunnerDevice;
                      const runnerList = startMode === "dev-hw" ? devMachineRunners : availableRunners;
                      return (
                    <>
                    <Text style={[styles.label, { color: c.textMuted, marginTop: 12 }]}>Runner</Text>
                    {runnerList.length === 0 ? (
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
                          {runnerDevice ? "No coding runner is signed in yet" : "Connect a Yaver machine first"}
                        </Text>
                        <Text style={[styles.muted, { color: c.textMuted, marginTop: 4 }]}>
                          {runnerDevice
                            ? `Open Devices, pick ${runnerDevice.name}, and sign in Claude / Codex or configure OpenCode. Come back here once one runner is ready.`
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
                        {runnerList.map((item) => ({
                          id: item.runnerId,
                          label: item.title || item.runnerId,
                        })).map((item) => {
                          const active = runner === item.id;
                          return (
                            <Pressable
                              key={item.id}
                              onPress={() => setRunner(item.id)}
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
                      );
                    })()}
                      </>
                    ) : null}
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
                            // Select only — no auto-advance. Auto-jumping
                            // to the next question on tap hid the selected
                            // state and read as "can't select this option".
                            // The user moves on with "Next question →".
                            setSurveyAnswers((prev) => ({ ...prev, [key]: opt.value as any }));
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
                    <View style={{ flexDirection: "row", justifyContent: "space-between", alignItems: "center", marginTop: 12 }}>
                      {surveyIndex > 0 ? (
                        <Pressable onPress={() => setSurveyIndex(Math.max(0, surveyIndex - 1))} hitSlop={8}>
                          <Text style={{ color: c.textMuted, fontSize: 13 }}>← Previous</Text>
                        </Pressable>
                      ) : (
                        <View />
                      )}
                      {surveyIndex < SURVEY_QUESTIONS.length - 1 ? (
                        <Pressable onPress={() => setSurveyIndex(surveyIndex + 1)} hitSlop={8}>
                          <Text style={{ color: c.accent, fontSize: 13, fontWeight: "600" }}>Next question →</Text>
                        </Pressable>
                      ) : (
                        <Text style={{ color: c.textMuted, fontSize: 12 }}>Last one — tap Next below</Text>
                      )}
                    </View>
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
                <Text style={[styles.label, { color: c.textMuted }]}>Logo (optional)</Text>
                <View style={{ flexDirection: "row", gap: 8 }}>
                  <TextInput
                    value={logoUrl}
                    onChangeText={setLogoUrl}
                    placeholder="https://… or pick from gallery →"
                    placeholderTextColor={c.textMuted}
                    autoCapitalize="none"
                    autoCorrect={false}
                    spellCheck={false}
                    keyboardType="url"
                    style={[styles.input, { color: c.textPrimary, borderColor: c.border, flex: 1 }]}
                  />
                  <Pressable
                    onPress={async () => {
                      try {
                        // Defer the import so we don't add 200 KB
                        // of expo-image-picker overhead to the
                        // initial bundle if the user never opens
                        // the wizard.
                        const ImagePicker = await import("expo-image-picker");
                        const perm = await ImagePicker.requestMediaLibraryPermissionsAsync();
                        if (!perm.granted) {
                          Alert.alert("Photo permission needed", "Allow access from your phone settings to pick a logo.");
                          return;
                        }
                        const result = await ImagePicker.launchImageLibraryAsync({
                          mediaTypes: ImagePicker.MediaTypeOptions.Images,
                          allowsEditing: true,
                          aspect: [1, 1],
                          quality: 0.9,
                          base64: false,
                        });
                        if (!result.canceled && result.assets?.[0]?.uri) {
                          // We store the local file URI (file://...)
                          // and let the LLM pipeline upload+rewrite
                          // it later. For phone-only projects this
                          // is enough; remote-runner projects will
                          // need a future upload step.
                          setLogoUrl(result.assets[0].uri);
                        }
                      } catch (err: any) {
                        const raw = err instanceof Error ? err.message : "";
                        Alert.alert(
                          "Image picker",
                          `Couldn't open the image picker. Try again from the photo library.${raw ? `\n\n${raw}` : ""}`,
                        );
                      }
                    }}
                    style={[
                      styles.btnSecondary,
                      { borderColor: c.border, paddingHorizontal: 14, justifyContent: "center" },
                    ]}
                  >
                    <Text style={[styles.btnText, { color: c.textPrimary }]}>📷</Text>
                  </Pressable>
                </View>
                {logoUrl ? (
                  <Text style={[styles.muted, { color: c.textMuted, marginTop: 4, marginBottom: 12 }]} numberOfLines={1}>
                    {logoUrl.startsWith("file://") ? "Local file selected" : logoUrl}
                  </Text>
                ) : (
                  <Text style={[styles.muted, { color: c.textMuted, marginTop: 4, marginBottom: 12 }]}>
                    Paste a public URL or tap 📷 to pick from your photos.
                  </Text>
                )}
                <Text style={[styles.label, { color: c.textMuted }]}>Primary color (optional)</Text>
                <TextInput
                  value={primaryHex}
                  onChangeText={setPrimaryHex}
                  placeholder="#0066ff — overrides the palette pick"
                  placeholderTextColor={c.textMuted}
                  autoCapitalize="none"
                  autoCorrect={false}
                  spellCheck={false}
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
                {/* Optional clarifying-question pass. The user can
                 * skip it entirely by just clicking Create — that's
                 * the implicit "force initialize" path. Tapping
                 * Refine asks the BYOK LLM whether 1-3 short
                 * follow-up questions would meaningfully shape the
                 * schema; answers are folded into the prompt as a
                 * [Clarifications] block. Failures fall through
                 * silently — never blocks Create. */}
                {codingMode === "phone" && (mobileAiProvider === "glm" ? glmKey.trim() : openAiKey.trim()) ? (
                  <View style={{ marginTop: 12 }}>
                    {refineQuestions.length === 0 ? (
                      <Pressable
                        onPress={async () => {
                          if (!prompt.trim()) {
                            Alert.alert("Need a description", "Type a description first, then refine.");
                            return;
                          }
                          setRefineLoading(true);
                          try {
                            const res = await generateClarifyingQuestions({
                              provider: mobileAiProvider,
                              apiKey: mobileAiProvider === "glm" ? glmKey.trim() : openAiKey.trim(),
                              name: name.trim(),
                              description: prompt.trim(),
                            });
                            setRefineUsed(true);
                            if (res.ready || res.questions.length === 0) {
                              Alert.alert("Looks good", "AI thinks the description is concrete enough — go ahead and Create.");
                            } else {
                              setRefineQuestions(res.questions);
                            }
                          } catch (err: any) {
                            const raw = err instanceof Error ? err.message : "";
                            Alert.alert(
                              "Refine failed",
                              `Couldn't reach the AI to refine your description — you can still Create.${raw ? `\n\n${raw}` : ""}`,
                            );
                          } finally {
                            setRefineLoading(false);
                          }
                        }}
                        disabled={refineLoading}
                        style={[styles.btnSecondary, { borderColor: c.border, opacity: refineLoading ? 0.6 : 1 }]}
                      >
                        {refineLoading ? (
                          <ActivityIndicator color={c.textPrimary} />
                        ) : (
                          <Text style={[styles.btnText, { color: c.textPrimary }]}>
                            {refineUsed ? "Refine again" : "Refine with AI (optional)"}
                          </Text>
                        )}
                      </Pressable>
                    ) : (
                      <View>
                        <View style={{ flexDirection: "row", justifyContent: "space-between", alignItems: "center", marginBottom: 6 }}>
                          <Text style={[styles.label, { color: c.textMuted }]}>
                            AI follow-ups ({Object.keys(refineAnswers).filter((k) => refineAnswers[k]?.trim()).length}/{refineQuestions.length})
                          </Text>
                          <Pressable onPress={() => { setRefineQuestions([]); setRefineAnswers({}); }}>
                            <Text style={{ color: c.accent, fontSize: 12, fontWeight: "600" }}>Force init (skip)</Text>
                          </Pressable>
                        </View>
                        {refineQuestions.map((q) => (
                          <View key={q.id} style={{ marginTop: 8 }}>
                            <Text style={[styles.muted, { color: c.textPrimary, marginBottom: 4 }]}>{q.title}</Text>
                            <TextInput
                              value={refineAnswers[q.id] || ""}
                              onChangeText={(t) => setRefineAnswers((prev) => ({ ...prev, [q.id]: t }))}
                              placeholder={q.placeholder || "Short answer"}
                              placeholderTextColor={c.textMuted}
                              style={[styles.input, { color: c.textPrimary, borderColor: c.border }]}
                            />
                          </View>
                        ))}
                      </View>
                    )}
                  </View>
                ) : null}
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
      activeRunnerDevice,
      applyImportedConversation,
      analyzingImport,
      selectedDevMachine,
      selectedDevMachineId,
      devMachines,
      devMachineRunners,
      runnersForDevice,
      importedConversation,
      importedBrief,
      mobileAiProvider,
      openAiKey,
      glmKey,
      availableRunners,
      runner,
      runnerChoiceEnabled,
    ],
  );

  const wizardFooter = useMemo(() => {
    if (!showForm) return null;
    const nameOk = name.trim().length > 0;
    const placementOk =
      step !== 2 ||
      startMode === "this-phone" ||
      startMode === "yaver-cloud" ||
      (startMode === "current-agent" && !!activeRunnerDevice) ||
      // A picked "other online box" must have an authenticated coding
      // runner before we let the user finalize — otherwise the cross-
      // device create lands on a machine with no runner and the first
      // task fails. The runner card below already routes to Devices.
      (startMode === "dev-hw" && !!selectedDevMachine && devMachineRunners.length > 0);
    const descOk = prompt.trim().length > 0 || importedConversation.trim().length > 0;
    const canAdvance = step === 0 ? nameOk : placementOk;
    const primaryLabel =
      step < 4
        ? !canAdvance && step === 0
          ? "Name required"
          : !canAdvance && step === 2
            ? startMode === "current-agent"
              ? "Connect machine"
              : startMode === "dev-hw"
                ? !selectedDevMachine
                  ? "Choose a machine"
                  : "Sign in a runner"
                : "Choose location"
            : "Next"
        : !descOk
          ? "Description required"
          : "Create sandbox";

    return (
      <View
        style={[
          styles.wizardFooter,
          {
            paddingBottom: Math.max(insets.bottom, 10),
            backgroundColor: c.bg,
            borderTopColor: c.border,
          },
        ]}
      >
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
            disabled={!canAdvance}
            onPress={() => setStep((prev) => Math.min(4, prev + 1))}
            style={[
              styles.btn,
              { backgroundColor: c.accent, flex: 1, opacity: canAdvance ? 1 : 0.4 },
            ]}
          >
            <Text style={[styles.btnText, { color: c.bg }]}>{primaryLabel}</Text>
          </Pressable>
        ) : (
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
              <Text style={[styles.btnText, { color: c.bg }]}>{primaryLabel}</Text>
            )}
          </Pressable>
        )}
      </View>
    );
  }, [
    activeRunnerDevice,
    c,
    creating,
    importedConversation,
    insets.bottom,
    name,
    prompt,
    selectedDevMachine,
    devMachineRunners,
    showForm,
    startMode,
    step,
  ]);

  return (
    <KeyboardAvoidingView
      behavior={Platform.OS === "ios" ? "padding" : undefined}
      keyboardVerticalOffset={Platform.OS === "ios" ? 84 : 0}
      style={{ flex: 1, backgroundColor: c.bg }}
    >
      <AppScreenHeader title="Mobile Sandbox" onBack={() => router.back()} />
      <FlatList
        data={projects}
        keyExtractor={(p) => p.slug}
        keyboardShouldPersistTaps="handled"
        contentContainerStyle={{ paddingBottom: (showForm ? 128 : 80) + insets.bottom, paddingTop: 12 }}
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
              onLongPress={() => projectActions(item)}
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
      {wizardFooter}
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
  wizardFooter: {
    flexDirection: "row",
    gap: 8,
    paddingHorizontal: 16,
    paddingTop: 10,
    borderTopWidth: 1,
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
