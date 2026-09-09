import React, { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  ActivityIndicator,
  Alert,
  FlatList,
  KeyboardAvoidingView,
  Linking,
  Platform,
  Pressable,
  RefreshControl,
  ScrollView,
  StyleSheet,
  Text,
  TextInput,
  View,
} from "react-native";
import * as Clipboard from "expo-clipboard";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { useRouter } from "expo-router";
import { useColors } from "../src/context/ThemeContext";
import { DEFAULT_MODEL_BY_RUNNER, useDevice, type Device, type RunnerInfo } from "../src/context/DeviceContext";
import { AppScreenHeader } from "../src/components/AppScreenHeader";
import RunnerAuthModal from "../src/components/RunnerAuthModal";
import { OpenCodeConfigModal } from "../src/components/OpenCodeConfigModal";
import { useAuth } from "../src/context/AuthContext";
import { getLocalSecret, getUserSettings, LOCAL_KEYS, saveLocalSecret } from "../src/lib/auth";
import { isCloudPreviewUser } from "../src/lib/cloudPreview";
import { HIDE_PAID_UI } from "../src/lib/launchFlags";
import { buildImportedConversationBrief, mergeImportedConversationPrompt } from "../src/lib/conversationImport";
import { getManagedSubscription } from "../src/lib/subscription";
import { getYaverCloudBaseUrl } from "../src/lib/yaverCloud";
import { connectionManager } from "../src/lib/connectionManager";
import { workspaceClientRoute } from "../src/lib/workspaceClientRoute";
import { quicClient, type MobileWorkspaceStatus, type RunnerInfo as DiscoveredRunnerInfo } from "../src/lib/quic";
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
  managedGitMirrorAt,
} from "../src/lib/phoneProjects";

type StartMode = "this-phone" | "current-agent" | "dev-hw" | "yaver-cloud";
type GitMode = "yaver-managed" | "skip" | "providers-now";
type CodingMode = "phone" | "runner";
type MobileAiProvider = "openai" | "glm";
type GitProvider = "github" | "gitlab";
type RepoVisibility = "private" | "public";
type GitIntegrationState = "checking" | "connected" | "clone-only" | "not-connected" | "unavailable";
type WorkspaceStatusFailure = "agent-upgrade-required" | "unreachable";

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

// Canva-style brand palettes: tap a palette to set primary + secondary at once,
// or tap an individual swatch. No hex typing required.
const SWATCHES = [
  "#6C5CE7", "#0066FF", "#00B894", "#00CEC9", "#0984E3",
  "#E17055", "#D63031", "#E84393", "#FD79A8", "#FDCB6E",
  "#F39C12", "#2D3436", "#636E72", "#1ABC9C", "#9B59B6",
];
const PALETTES: { name: string; primary: string; secondary: string }[] = [
  { name: "Indigo", primary: "#6C5CE7", secondary: "#A29BFE" },
  { name: "Ocean", primary: "#0066FF", secondary: "#00CEC9" },
  { name: "Forest", primary: "#00B894", secondary: "#55EFC4" },
  { name: "Sunset", primary: "#E17055", secondary: "#FDCB6E" },
  { name: "Berry", primary: "#E84393", secondary: "#FD79A8" },
  { name: "Slate", primary: "#2D3436", secondary: "#636E72" },
];
const HOME_ICON_PRESETS = [
  { id: "spark", label: "Spark", glyph: "✦" },
  { id: "check", label: "Check", glyph: "✓" },
  { id: "note", label: "Notes", glyph: "▤" },
  { id: "grid", label: "Grid", glyph: "▦" },
  { id: "heart", label: "Heart", glyph: "♥" },
  { id: "bolt", label: "Bolt", glyph: "ϟ" },
  { id: "leaf", label: "Leaf", glyph: "◒" },
  { id: "rocket", label: "Launch", glyph: "↑" },
] as const;

const SURVEY_QUESTIONS: Array<{
  key: keyof SurveyAnswers;
  title: string;
  options: Array<{ value: string; label: string; sub?: string }>;
}> = [
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
  const {
    connectionStatus,
    connectedDeviceIds,
    devices,
    activeDevice,
    primaryDeviceId,
    setPrimaryDevice,
    primaryRunnerByDevice,
    primaryModelByDevice,
    primaryModeByDevice,
    primaryProviderByDevice,
    setPrimaryRunnerForDevice,
  } = useDevice();
  const connected = connectionStatus === "connected";
  const canUseCloudPreview = isCloudPreviewUser(user?.email);
  const [hasManagedCloud, setHasManagedCloud] = useState(false);
  // HN-LAUNCH-HIDE-PAID: hide the managed "Yaver Cloud" start-mode option
  // (Yaver-billed box). Flip HIDE_PAID_UI in src/lib/launchFlags.ts to restore.
  const canUseYaverCloud = !HIDE_PAID_UI && (canUseCloudPreview || hasManagedCloud);

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
  const [model, setModel] = useState<string>("");
  const [creating, setCreating] = useState(false);
  // Live "starting" checklist shown while a project spins up, so creation feels
  // real: AI connectivity (GLM/OpenAI pong) → runtime → generate → init.
  const [createSteps, setCreateSteps] = useState<
    { key: string; label: string; status: "pending" | "running" | "done" | "skipped" }[]
  >([]);
  const markStep = (key: string, status: "pending" | "running" | "done" | "skipped") =>
    setCreateSteps((prev) => prev.map((s) => (s.key === key ? { ...s, status } : s)));
  const [gitMode, setGitMode] = useState<GitMode>("yaver-managed");
  const [step, setStep] = useState(0);
  const [codingMode, setCodingMode] = useState<CodingMode>("runner");
  const [mobileAiProvider, setMobileAiProvider] = useState<MobileAiProvider>("openai");
  const [openAiKey, setOpenAiKey] = useState("");
  const [glmKey, setGlmKey] = useState("");
  const mobileAiProviderTouchedRef = useRef(false);
  const placementTouchedRef = useRef(false);
  const runnerDeviceRef = useRef<string | null>(null);

  const [startMode, setStartMode] = useState<StartMode>("current-agent");
  // Step 1 — Git config (optional). gitMode === "skip" means the
  // user explicitly bypassed git setup; in that case the
  // gitProvider/repoVisibility/repoName fields are ignored at create
  // time. Repo name auto-fills from the slug of the project name so
  // the user rarely has to type it.
  const [gitProvider, setGitProvider] = useState<GitProvider>("github");
  const [gitIntegrations, setGitIntegrations] = useState<Record<GitProvider, GitIntegrationState>>({
    github: "checking",
    gitlab: "checking",
  });
  const [workspaceStatus, setWorkspaceStatus] = useState<MobileWorkspaceStatus | null>(null);
  const [workspaceStatusFailure, setWorkspaceStatusFailure] = useState<WorkspaceStatusFailure | null>(null);
  const [discoveredRunners, setDiscoveredRunners] = useState<DiscoveredRunnerInfo[]>([]);
  const [workspaceStatusLoading, setWorkspaceStatusLoading] = useState(false);
  const [workspaceAgentUpdate, setWorkspaceAgentUpdate] = useState<
    { kind: "idle" } | { kind: "updating"; detail: string } | { kind: "failed"; detail: string }
  >({ kind: "idle" });
  const workspaceAgentUpdateStreamRef = useRef<(() => void) | null>(null);
  const [runnerInstall, setRunnerInstall] = useState<{ runner: string; line: string } | null>(null);
  const [runnerAuthModalRunner, setRunnerAuthModalRunner] = useState<string | null>(null);
  const [openCodeConfigVisible, setOpenCodeConfigVisible] = useState(false);
  const [startingGitOAuth, setStartingGitOAuth] = useState<GitProvider | null>(null);
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
  // Stack is predetermined (React Native + TypeScript + Yaver Serverless),
  // so the optional survey starts empty and asks only product questions.
  const [surveyAnswers, setSurveyAnswers] = useState<SurveyAnswers>({});
  // Optional logo URL — concatenated into the description prompt so
  // the LLM can use it as a visual reference. We accept any URL the
  // user can paste (CDN, gist, GitHub raw, etc.) — gallery upload is
  // a follow-up that needs an upload pipeline + storage.
  const [logoUrl, setLogoUrl] = useState("");
  const [homeIcon, setHomeIcon] = useState<(typeof HOME_ICON_PRESETS)[number]["id"]>("spark");
  // Optional primary-color hex override. Pairs with the survey's
  // palette pick — palette is a named choice, hex is a free-form
  // override for users who already know the exact brand colour.
  // Loose validation only (CSS hex shape); blank means "no override".
  const [primaryHex, setPrimaryHex] = useState("#6C5CE7");
  // Canva-style secondary/accent colour, chosen via swatches (no hex typing).
  const [secondaryHex, setSecondaryHex] = useState("#A29BFE");
  // "Setting up your project" checklist (step 4) — GLM pong + connect, run on
  // entry so the project feels like it's spinning up before the user describes it.
  const [setupSteps, setSetupSteps] = useState<
    { key: string; label: string; status: "pending" | "running" | "done" | "skipped" }[]
  >([]);
  const setupRanRef = useRef(false);
  const markSetup = (key: string, status: "pending" | "running" | "done" | "skipped") =>
    setSetupSteps((prev) => prev.map((s) => (s.key === key ? { ...s, status } : s)));
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
  const preferredDevice = useMemo(
    () => devices.find((device) =>
      device.id === primaryDeviceId &&
      device.online &&
      !device.needsAuth &&
      device.deviceClass !== "edge-mobile",
    ) ?? null,
    [devices, primaryDeviceId],
  );
  const [selectedDevMachineId, setSelectedDevMachineId] = useState<string | null>(null);
  // Run the visible setup narration without touching phone-local provider
  // secrets. Coding credentials live on the selected remote box and are
  // audited by /mobile-workspace/status plus the real runner probe on Next.
  const runSetup = useCallback(() => {
    if (setupRanRef.current) return;
    setupRanRef.current = true;
    const runtimeLabel =
      startMode === "yaver-cloud" ? "Connecting Yaver Cloud"
      : "Connecting remote dev runner";
    setSetupSteps([
      { key: "ai", label: "Verifying remote OpenCode provider", status: "pending" },
      { key: "runtime", label: runtimeLabel, status: "pending" },
    ]);
    void (async () => {
      markSetup("ai", "running");
      markSetup("ai", workspaceStatus?.openCode.ready ? "done" : "skipped");
      markSetup("runtime", "running");
      await new Promise((r) => setTimeout(r, 600));
      markSetup("runtime", "done");
    })();
  }, [startMode, workspaceStatus?.openCode.ready]);
  useEffect(() => {
    if (step === 4) runSetup();
    else setupRanRef.current = false;
  }, [step, runSetup]);
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
  const selectedRunnerDevice = startMode === "dev-hw" ? selectedDevMachine : activeRunnerDevice;
  const selectedRunnerConnected = !!selectedRunnerDevice && connectedDeviceIds.includes(selectedRunnerDevice.id);
  // A selected box that already has its own pooled connection must be asked
  // directly. Routing it through whichever unrelated box happens to be
  // focused makes Mobile Workspace depend on a second machine and is
  // especially brittle in RN-web, where expected background peer failures can
  // otherwise mask a healthy primary Ubuntu box. Keep the peer-proxy fallback
  // only for an online selection whose own pool connection is not ready yet.
  const workspaceRoute = workspaceClientRoute(
    selectedRunnerDevice?.id,
    activeDevice?.id,
    connectedDeviceIds,
  );
  const selectedWorkspaceClient = useMemo(
    () => selectedRunnerDevice && workspaceRoute.useSelectedClient
      ? connectionManager.clientFor(selectedRunnerDevice.id)
      : quicClient,
    [selectedRunnerDevice, workspaceRoute.useSelectedClient],
  );
  const selectedWorkspaceTarget = workspaceRoute.peerTarget;
  const selectedRunnerList = startMode === "dev-hw" ? devMachineRunners : availableRunners;
  const runnerChoiceEnabled = !!activeRunnerDevice;
  useEffect(() => {
    // Seed a default runner from whichever remote target is in play —
    // the picked online box wins when dev-hw is selected, otherwise the
    // connected machine's runners.
    const deviceId = selectedRunnerDevice?.id ?? null;
    if (!deviceId || selectedRunnerList.length === 0) return;
    const targetChanged = runnerDeviceRef.current !== deviceId;
    const currentStillValid = selectedRunnerList.some((item) => item.runnerId === runner);
    if (targetChanged || !currentStillValid) {
      const preferred = primaryRunnerByDevice[deviceId];
      const next = selectedRunnerList.find((item) => item.runnerId === preferred)
        ?? selectedRunnerList.find((item) => item.runnerId.toLowerCase() === "opencode")
        ?? selectedRunnerList[0];
      setRunner(next.runnerId);
      runnerDeviceRef.current = deviceId;
    }
  }, [primaryRunnerByDevice, runner, selectedRunnerDevice?.id, selectedRunnerList]);

  useEffect(() => {
    if (!showForm || placementTouchedRef.current) return;
    const target = preferredDevice ?? activeRunnerDevice ?? devMachines[0] ?? null;
    if (!target) return;
    setCodingMode("runner");
    if (target.id === activeRunnerDevice?.id) {
      setStartMode("current-agent");
    } else {
      setSelectedDevMachineId(target.id);
      setStartMode("dev-hw");
    }
  }, [activeRunnerDevice, devMachines, preferredDevice, showForm]);

  const loadWorkspaceReadiness = useCallback(async () => {
    if (!selectedRunnerDevice) {
      setWorkspaceStatus(null);
      setWorkspaceStatusFailure(null);
      setDiscoveredRunners([]);
      return;
    }
    setWorkspaceStatusLoading(true);
    setGitIntegrations({ github: "checking", gitlab: "checking" });
    try {
      const [probe, runnerInventory, legacyRunnerStatus] = await Promise.all([
        selectedWorkspaceClient.mobileWorkspaceStatusProbe(selectedWorkspaceTarget),
        selectedWorkspaceClient.getRunnersForTarget(selectedWorkspaceTarget),
        selectedWorkspaceClient.runnerAuthStatusOrNull(selectedWorkspaceTarget),
      ]);
      const status = probe.status;
      // Some older relay/peer coordinators flatten a target's 404 into a 502.
      // Prove the legacy runner operation on the same target: if it answers but
      // the aggregate route does not, the box is reachable and needs an update.
      const failure = !status && legacyRunnerStatus !== null
        ? "agent-upgrade-required"
        : probe.reason ?? null;
      setWorkspaceStatus(status);
      setWorkspaceStatusFailure(failure);
      setDiscoveredRunners(runnerInventory ?? []);
      if (status) {
        const providerState = (provider: GitProvider): GitIntegrationState => {
          const gate = status.gitProviders.find((item) => item.id === provider);
          if (!gate) return "not-connected";
          return gate.ready ? "connected" : gate.configured ? "clone-only" : "not-connected";
        };
        setGitIntegrations({ github: providerState("github"), gitlab: providerState("gitlab") });
      } else {
        setGitIntegrations({ github: "unavailable", gitlab: "unavailable" });
      }
    } catch {
      setWorkspaceStatus(null);
      setWorkspaceStatusFailure("unreachable");
      setDiscoveredRunners([]);
      setGitIntegrations({ github: "unavailable", gitlab: "unavailable" });
    } finally {
      setWorkspaceStatusLoading(false);
    }
  // A selected device can hydrate before its browser/native transport has
  // finished connecting. Recreate the probe when that transport transitions
  // so an early honest "unreachable" verdict self-clears instead of sticking
  // until the user manually taps Retry.
  }, [connected, selectedRunnerConnected, selectedRunnerDevice, selectedWorkspaceClient, selectedWorkspaceTarget]);

  useEffect(() => () => {
    workspaceAgentUpdateStreamRef.current?.();
    workspaceAgentUpdateStreamRef.current = null;
  }, []);

  const updateWorkspaceAgent = useCallback(async () => {
    if (!selectedRunnerDevice || workspaceAgentUpdate.kind === "updating") return;
    workspaceAgentUpdateStreamRef.current?.();
    workspaceAgentUpdateStreamRef.current = null;
    setWorkspaceAgentUpdate({ kind: "updating", detail: "Preparing agent update…" });
    try {
      const result = await selectedWorkspaceClient.triggerAgentUpdate(selectedWorkspaceTarget);
      if (!result.ok) throw new Error(result.error || "The remote box refused the update.");
      if (result.started === false) {
        setWorkspaceAgentUpdate({
          kind: "failed",
          detail: result.message || "This box reports that it is current, but its Mobile Workspace route is still missing.",
        });
        return;
      }
      if (!selectedWorkspaceTarget) {
        workspaceAgentUpdateStreamRef.current = selectedWorkspaceClient.streamAgentUpdate((event) => {
          if (event.type !== "progress") return;
          const detail = String(event.text || event.phase || "Updating Yaver agent…").trim();
          if (detail) setWorkspaceAgentUpdate({ kind: "updating", detail });
        });
      }
      const deadline = Date.now() + 90_000;
      while (Date.now() < deadline) {
        await new Promise((resolve) => setTimeout(resolve, 2500));
        const probe = await selectedWorkspaceClient.mobileWorkspaceStatusProbe(selectedWorkspaceTarget);
        if (probe.status) {
          workspaceAgentUpdateStreamRef.current?.();
          workspaceAgentUpdateStreamRef.current = null;
          setWorkspaceAgentUpdate({ kind: "idle" });
          await loadWorkspaceReadiness();
          return;
        }
      }
      throw new Error("The agent did not come back with Mobile Workspace support within 90 seconds.");
    } catch (error) {
      setWorkspaceAgentUpdate({
        kind: "failed",
        detail: error instanceof Error ? error.message : "Agent update failed. Try again.",
      });
    } finally {
      workspaceAgentUpdateStreamRef.current?.();
      workspaceAgentUpdateStreamRef.current = null;
    }
  }, [loadWorkspaceReadiness, selectedRunnerDevice, selectedWorkspaceClient, selectedWorkspaceTarget, workspaceAgentUpdate.kind]);

  useEffect(() => {
    if (!showForm || (step !== 1 && step !== 2) || !selectedRunnerDevice) return;
    void loadWorkspaceReadiness();
  }, [loadWorkspaceReadiness, selectedRunnerDevice, showForm, step]);

  useEffect(() => {
    if (!selectedRunnerDevice || !workspaceStatus) return;
    const ready = workspaceStatus.runners.filter((item) => item.ready);
    if (ready.length === 0) {
      setRunner("");
      setModel("");
      return;
    }
    const normalizedCurrent = runner === "claude-code" ? "claude" : runner;
    if (ready.some((item) => item.id === normalizedCurrent)) return;
    const saved = primaryRunnerByDevice[selectedRunnerDevice.id];
    const next = ready.find((item) => item.id === saved)
      ?? ready.find((item) => item.id === "opencode")
      ?? ready[0];
    setRunner(next.id);
  }, [primaryRunnerByDevice, runner, selectedRunnerDevice, workspaceStatus]);

  useEffect(() => {
    if (!runner || !selectedRunnerDevice) return;
    const normalized = runner === "claude-code" ? "claude" : runner;
    const inventory = discoveredRunners.find((item) => item.id === normalized);
    const saved = primaryRunnerByDevice[selectedRunnerDevice.id] === normalized
      ? primaryModelByDevice[selectedRunnerDevice.id]
      : "";
    const discoveredDefault = inventory?.models.find((item) => item.isDefault)?.id || inventory?.models[0]?.id;
    setModel(saved || discoveredDefault || DEFAULT_MODEL_BY_RUNNER[normalized] || "");
  }, [discoveredRunners, primaryModelByDevice, primaryRunnerByDevice, runner, selectedRunnerDevice]);

  const configureWorkspaceRunner = useCallback(async (runnerId: string, code?: string) => {
    if (code === "mobile_workspace.runner.not_installed") {
      if (!selectedRunnerDevice || runnerInstall) return;
      setRunnerInstall({ runner: runnerId, line: `Starting ${runnerId} installer…` });
      try {
        const result = await selectedWorkspaceClient.installRunner(runnerId, {
          target: selectedWorkspaceTarget,
          onProgress: (line) => {
            const trimmed = line.trim();
            if (trimmed) setRunnerInstall({ runner: runnerId, line: trimmed.slice(0, 120) });
          },
        });
        if (!result.ok) throw new Error(result.error || `${runnerId} installation failed.`);
        await loadWorkspaceReadiness();
        if (runnerId === "opencode") setOpenCodeConfigVisible(true);
        else setRunnerAuthModalRunner(runnerId);
      } catch (error) {
        Alert.alert("Runner installation failed", error instanceof Error ? error.message : "Try again from this remote-box setup.");
      } finally {
        setRunnerInstall(null);
      }
      return;
    }
    if (runnerId === "opencode") {
      setOpenCodeConfigVisible(true);
      return;
    }
    setRunnerAuthModalRunner(runnerId);
  }, [loadWorkspaceReadiness, runnerInstall, selectedRunnerDevice, selectedWorkspaceClient, selectedWorkspaceTarget]);

  const testWorkspaceRunner = useCallback(async (runnerId: string) => {
    if (!selectedRunnerDevice) return;
    const inventory = discoveredRunners.find((item) => item.id === runnerId);
    const advertisedDefault = inventory?.models.find((item) => item.isDefault)?.id
      || inventory?.models[0]?.id
      || "";
    const probeModel = runnerId === runner
      ? model
      : advertisedDefault || DEFAULT_MODEL_BY_RUNNER[runnerId] || "";
    try {
      const response = await selectedWorkspaceClient.agentRequest(
        selectedRunnerDevice.id,
        "/agent/runners/test",
        {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ runner: runnerId, model: probeModel, timeoutMs: 75000 }),
        },
        80000,
      );
      const result = await response.json().catch(() => ({}));
      if (!response.ok || result?.ok !== true) {
        if (result?.needsAuth) void configureWorkspaceRunner(runnerId);
        Alert.alert("Runner test failed", result?.error || `The remote ${runnerId} probe did not complete.`);
        return;
      }
      await loadWorkspaceReadiness();
      Alert.alert("Runner ready", `${runnerId} answered through ${probeModel || "its selected model"} on ${selectedRunnerDevice.name}.`);
    } catch (error) {
      Alert.alert("Runner test failed", error instanceof Error ? error.message : "The remote runner could not be tested.");
    }
  }, [configureWorkspaceRunner, discoveredRunners, loadWorkspaceReadiness, model, runner, selectedRunnerDevice, selectedWorkspaceClient]);

  const configureGitProvider = useCallback(async (provider: GitProvider) => {
    if (!selectedRunnerDevice || startingGitOAuth) return;
    setStartingGitOAuth(provider);
    try {
      const start = await selectedWorkspaceClient.gitOAuthStart(provider, selectedWorkspaceTarget);
      if (!start.ok || !start.sessionId || !start.userCode || !start.verificationUri) {
        throw new Error(start.error || `${provider} sign-in could not start on the remote box.`);
      }
      await Clipboard.setStringAsync(start.userCode).catch(() => {});
      Alert.alert(
        `Configure ${provider === "github" ? "GitHub" : "GitLab"}`,
        `Code ${start.userCode} was copied. Complete sign-in in the browser; the credential stays on ${selectedRunnerDevice.name}.`,
        [
          { text: "Later", style: "cancel" },
          { text: "Open browser", onPress: () => void Linking.openURL(start.verificationUri) },
        ],
      );
      void Linking.openURL(start.verificationUri).catch(() => {});
      const intervalMs = Math.max(2, start.interval || 5) * 1000;
      const deadline = start.expiresAt || Date.now() + 10 * 60 * 1000;
      while (Date.now() < deadline) {
        await new Promise((resolve) => setTimeout(resolve, intervalMs));
        const status = await selectedWorkspaceClient.gitOAuthStatus(start.sessionId, provider, selectedWorkspaceTarget);
        if (status.state === "pending") continue;
        if (status.state === "done") {
          await loadWorkspaceReadiness();
          Alert.alert("Git ready", `${provider === "github" ? "GitHub" : "GitLab"} is configured on ${selectedRunnerDevice.name}.`);
          return;
        }
        throw new Error(status.error || `${provider} authorization ${status.state}.`);
      }
      throw new Error(`${provider} authorization expired.`);
    } catch (error) {
      Alert.alert("Git configuration failed", error instanceof Error ? error.message : "Try again from the remote-box settings.");
    } finally {
      setStartingGitOAuth(null);
    }
  }, [loadWorkspaceReadiness, selectedRunnerDevice, selectedWorkspaceClient, selectedWorkspaceTarget, startingGitOAuth]);
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
    if (!connected && startMode === "dev-hw" && devMachines.length === 0) {
      setStartMode("current-agent");
    }
  }, [connected, devMachines.length, startMode]);

  const persistPrimaryTaskTarget = useCallback(async (): Promise<boolean> => {
    if (startMode !== "current-agent" && startMode !== "dev-hw") return true;
    if (!selectedRunnerDevice || !runner) return false;
    const info = selectedRunnerList.find((item) => item.runnerId === runner);
    const isOpenCode = runner.toLowerCase() === "opencode";
    const previousRunner = primaryRunnerByDevice[selectedRunnerDevice.id];
    const effectiveModel = model || info?.model || "";
    const inferredProvider = effectiveModel.includes("/") ? effectiveModel.split("/", 1)[0] : undefined;
    try {
      setWorkspaceStatusLoading(true);
      // Persisting a preference is not permission to start a runner. The old
      // preflight called the generation-test endpoint here, opening a real model
      // session and spent tokens just for tapping Next. Readiness is shown by
      // the status row; the explicit Test action remains available to users
      // who intentionally want a generation probe.
      await setPrimaryDevice(selectedRunnerDevice.id);
      await setPrimaryRunnerForDevice(
        selectedRunnerDevice.id,
        runner,
        model || info?.model || (previousRunner === runner ? primaryModelByDevice[selectedRunnerDevice.id] : undefined),
        isOpenCode && previousRunner === runner ? primaryModeByDevice[selectedRunnerDevice.id] : null,
        isOpenCode
          ? inferredProvider || (previousRunner === runner ? primaryProviderByDevice[selectedRunnerDevice.id] : undefined)
          : null,
      );
      return true;
    } catch (error) {
      Alert.alert(
        "Couldn't save primary task target",
        error instanceof Error ? error.message : "Check your connection and try again.",
      );
      return false;
    } finally {
      setWorkspaceStatusLoading(false);
    }
  }, [
    primaryModeByDevice,
    primaryModelByDevice,
    primaryProviderByDevice,
    primaryRunnerByDevice,
    runner,
    model,
    selectedRunnerDevice,
    selectedRunnerList,
    setPrimaryDevice,
    setPrimaryRunnerForDevice,
    startMode,
  ]);

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
        const target = selectedRunnerDevice?.id === activeDevice?.id ? undefined : selectedRunnerDevice?.id;
        const plan = await quicClient.analyzeConversationImport({
          url: importedBrief.sourceUrl,
          content: importedConversation,
          title: importedBrief.title,
          runner: runner || undefined,
          model: model || undefined,
          mode: runner === "opencode" ? primaryModeByDevice[selectedRunnerDevice?.id || ""] || "build" : undefined,
        }, target);
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
  }, [activeDevice?.id, connected, importedBrief, importedConversation, model, name, primaryModeByDevice, runner, selectedRunnerDevice]);

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
    if (secondaryHex.trim()) brandLines.push(`Secondary color: ${secondaryHex.trim()}`);
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
    // AI/runtime connectivity already ran on the "Setting up your project" step.
    // The create-time checklist covers the build phase: generate + init.
    const onPhoneGen = codingMode === "phone" && !!effectivePrompt.trim();
    setCreateSteps([
      { key: "gen", label: "Generating starter files", status: onPhoneGen ? "pending" : "skipped" },
      { key: "init", label: "Initializing project", status: "pending" },
    ]);
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
      if (onPhoneGen) markStep("gen", "running");
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
      if (onPhoneGen) markStep("gen", "done");
      markStep("init", "running");
      const finalName = name.trim() || importedBrief?.suggestedName || "Imported Project";
      const chosenPalette = PALETTES.find(
        (pal) => pal.primary === primaryHex && pal.secondary === secondaryHex,
      );
      const spec = {
        name: finalName,
        template: draft.template ?? (prompt.trim() ? undefined : template),
        schema: draft.schema,
        auth: draft.auth,
        seed: draft.seed,
        app: {
          ...(draft.app ?? {}),
          brand: {
            displayName: finalName,
            icon: homeIcon,
            palette: chosenPalette?.name.toLowerCase() ?? "custom",
            primaryColor: primaryHex,
            secondaryColor: secondaryHex,
          },
        },
        prompt: effectivePrompt || undefined,
        runner: effectivePrompt && codingMode === "runner" ? runner || undefined : undefined,
        model: effectivePrompt && codingMode === "runner" ? model || undefined : undefined,
        mode: effectivePrompt && codingMode === "runner" && runner === "opencode"
          ? primaryModeByDevice[selectedRunnerDevice?.id || ""] || "build"
          : undefined,
        provider: effectivePrompt && codingMode === "runner" && runner === "opencode" && model.includes("/")
          ? model.split("/", 1)[0]
          : undefined,
        importUrl: !effectivePrompt && importedConversation.trim() ? importedBrief?.sourceUrl : undefined,
        importContent: !effectivePrompt && importedConversation.trim() ? importedConversation.trim() : undefined,
        importTitle: !effectivePrompt && importedConversation.trim() ? importedBrief?.title : undefined,
        managedGit:
          startMode !== "this-phone" && gitMode !== "skip"
            ? { enabled: true, visibility: repoVisibility }
            : undefined,
      };
      let p: PhoneProject | null = null;
      let createdTarget: PhonePushTarget | null = null;

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
        createdTarget = target;
        p = await createPhoneProjectAt(target, spec);
        await bindPhoneProjectToTarget(p.slug, target, { slug: p.slug, localUrl: "", browseUrl: "", project: p }, selectedDevMachine.name);
      } else {
        const cloudAuthToken = (await getLocalSecret(LOCAL_KEYS.yaverCloudToken)) ?? token ?? undefined;
        const target: PhonePushTarget = {
          kind: "yaver-cloud",
          cloudBaseUrl: YAVER_CLOUD_BASE,
          cloudAuthToken,
        };
        createdTarget = target;
        p = await createPhoneProjectAt(target, spec);
        await bindPhoneProjectToTarget(p.slug, target, { slug: p.slug, localUrl: "", browseUrl: "", project: p }, "Yaver Cloud");
      }

      if (!p) throw new Error("target returned no project");
      markStep("init", "done");
      // brief beat so the user sees the full checklist complete before nav
      await new Promise((r) => setTimeout(r, 600));

      // GitHub / GitLab is now a mirror on top of Yaver Managed Git.
      // Best-effort: the project is already saved, versioned, and
      // checkpointed even if provider auth is missing.
      if (gitMode === "providers-now" && connected) {
        try {
          let repo: any = null;
          if (p.managedGit?.enabled) {
            const mirrorArgs = {
              slug: p.slug,
              provider: gitProvider,
              repoName: (repoName.trim() || repoNameSlug || p.slug),
              visibility: repoVisibility,
              description: prompt.trim().slice(0, 200),
            } as const;
            const mirrored = createdTarget
              ? await managedGitMirrorAt(createdTarget, mirrorArgs)
              : await quicClient.managedGitMirrorConnect(mirrorArgs);
            repo = mirrored?.mirror
              ? { fullName: mirrored.mirror.fullName, cloneUrl: mirrored.mirror.cloneUrl, sandboxWritten: false }
              : null;
          } else {
            repo = await quicClient.gitProviderRepoCreate({
              provider: gitProvider,
              name: (repoName.trim() || repoNameSlug || p.slug),
              visibility: repoVisibility,
              description: prompt.trim().slice(0, 200),
              writeSandbox: true,
            });
          }
          if (repo) {
            Alert.alert(
              "Mirror created",
              `${repo.fullName} on ${gitProvider}.com${repo.sandboxWritten ? "\n\nyaver.workspace.yaml committed — repo registered as a Mobile Workspace." : ""}\n\n${repo.cloneUrl}`,
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
            "Project saved, mirror not created",
            gitErr?.message?.includes("412")
              ? `No ${gitProvider} token is set up on this machine. Add one from the dashboard's Git tab, then run the wizard's "Configure now" path again or push from the project later.`
              : (gitErr?.message ?? "Mirror creation failed; project itself is saved."),
          );
        }
      }
      setName("");
      setPrompt("");
      setImportedConversation("");
      setRunner(availableRunners[0]?.runnerId ?? "");
      setGitMode("yaver-managed");
      setRepoName("");
      setLogoUrl("");
      setRefineQuestions([]);
      setRefineAnswers({});
      setRefineUsed(false);
      setSurveyAnswers({});
      setSurveyIndex(0);
      setSurveySkipped(false);
      setStep(0);
      setShowForm(false);
      await load();
      router.navigate(`/phone-project/${p.slug}` as any);
      if (gitMode !== "skip") {
        Alert.alert(
          "Git is ready",
          gitMode === "providers-now"
            ? "Yaver saved the project in managed git and tried to create the external mirror."
            : "Yaver saved the project in managed git. You can add GitHub, GitLab, Dropbox, or your own computer as backups later.",
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

  function projectActions(p: PhoneProject) {
    Alert.alert(p.name, undefined, [
      { text: "Delete", style: "destructive", onPress: () => void remove(p) },
      { text: "Cancel", style: "cancel" },
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
                placementTouchedRef.current = false;
                runnerDeviceRef.current = null;
                setStartMode("current-agent");
                setCodingMode("runner");
                setShowForm(true);
              }}
              style={[styles.btn, { backgroundColor: c.accent, marginTop: 12 }]}
            >
              <Text style={[styles.btnText, { color: c.bg }]}>+ New mobile app</Text>
            </Pressable>
            <Pressable
              onPress={() => router.push("/repo-coding" as any)}
              style={[
                styles.btn,
                { backgroundColor: "transparent", borderWidth: 1, borderColor: c.border, marginTop: 10 },
              ]}
            >
              <Text style={[styles.btnText, { color: c.textPrimary }]}>Clone a GitHub repo & code with AI</Text>
            </Pressable>
            <Text style={[styles.muted, { color: c.textMuted, marginTop: 8 }]}>
              {connected
                ? "Your primary remote device handles vibing, Yaver Serverless, and rendering."
                : "Connect a remote development device to create a Mobile Workspace."}
            </Text>
            {projects.length > 0 ? (
              <Text style={[styles.muted, { color: c.textMuted, marginTop: 4 }]}>
                Or tap one of your {projects.length === 1 ? "existing project" : `${projects.length} existing projects`} below to open it. Long-press to delete.
              </Text>
            ) : null}
          </>
        ) : (
          <View style={[styles.card, { backgroundColor: c.bgCard, borderColor: c.border, marginTop: 12 }]}>
            <Text style={[styles.stepTitle, { color: c.textPrimary }]}>
              {[
                "1. Name your app",
                "2. Development target",
                "3. Git provider",
                "4. Quick survey (optional)",
                "5. Setting up your project",
                "6. Look & Home Screen icon",
                "7. Describe the app",
              ][step]}
            </Text>
            <Text style={[styles.stepSubtitle, { color: c.textMuted }]}>
              {[
                "You can change this later.",
                "Your existing primary device and runner are selected. Next makes this the default for tasks.",
                "Yaver Git is built in. GitHub and GitLab show their live integration status.",
                "Five quick multiple-choice questions. Skip if you'd rather just type.",
                "Getting things ready — checking AI and your runtime.",
                "Choose the icon and palette your sandbox will carry into its browser shortcut.",
                "Required. Tell Yaver what you're building, in your own words.",
              ][step]}
            </Text>
            <View style={styles.stepDots}>
              {[0, 1, 2, 3, 4, 5, 6].map((value) => (
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

            {step === 2 ? (
              <>
                {/* Yaver Managed Git is the default. Provider setup is
                 * an optional mirror/export path, not a prerequisite. */}
                <View style={{ flexDirection: "row", gap: 8, marginTop: 4 }}>
                  {[
                    { id: "yaver-managed" as GitMode, label: "Yaver Git · Ready" },
                    { id: "providers-now" as GitMode, label: "Mirror now" },
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
                        <Text
                          numberOfLines={1}
                          adjustsFontSizeToFit
                          style={{ color: active ? c.bg : c.textPrimary, fontWeight: "600", textAlign: "center" }}
                        >
                          {opt.label}
                        </Text>
                      </Pressable>
                    );
                  })}
                </View>
                {/* Skip — a subtle full-width option at the bottom, not an equal third */}
                <Pressable
                  onPress={() => setGitMode("skip")}
                  style={{
                    marginTop: 8,
                    paddingVertical: 9,
                    alignItems: "center",
                    borderRadius: 10,
                    borderWidth: 1,
                    borderColor: gitMode === "skip" ? c.accent : c.border,
                    backgroundColor: gitMode === "skip" ? c.accent : "transparent",
                  }}
                >
                  <Text
                    style={{
                      color: gitMode === "skip" ? c.bg : c.textMuted,
                      fontSize: 13,
                      fontWeight: gitMode === "skip" ? "600" : "400",
                    }}
                  >
                    Skip for now
                  </Text>
                </Pressable>
                {gitMode === "yaver-managed" ? (
                  <>
                    <Text style={[styles.label, { color: c.textMuted, marginTop: 14 }]}>Visibility</Text>
                    <View style={{ flexDirection: "row", gap: 8 }}>
                      {([
                        { id: "private" as RepoVisibility, label: "Private", sub: "Only you" },
                        { id: "public" as RepoVisibility, label: "Public", sub: "Anyone can read later" },
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
                    <Text style={[styles.muted, { color: c.textMuted, marginTop: 10 }]}>
                      Yaver will save versions automatically and can back them up to your computer, Dropbox, GitHub, or GitLab later.
                    </Text>
                  </>
                ) : gitMode === "providers-now" ? (
                  <>
                    <Text style={[styles.label, { color: c.textMuted, marginTop: 14 }]}>Provider</Text>
                    <View style={{ flexDirection: "row", gap: 8 }}>
                      {([
                        {
                          id: "github" as GitProvider,
                          label: `GitHub · ${gitIntegrations.github === "connected" ? "Connected" : gitIntegrations.github === "clone-only" ? "Clone ready" : gitIntegrations.github === "not-connected" ? "Not connected" : gitIntegrations.github === "checking" ? "Checking…" : "Unavailable"}`,
                        },
                        {
                          id: "gitlab" as GitProvider,
                          label: `GitLab · ${gitIntegrations.gitlab === "connected" ? "Connected" : gitIntegrations.gitlab === "clone-only" ? "Clone ready" : gitIntegrations.gitlab === "not-connected" ? "Not connected" : gitIntegrations.gitlab === "checking" ? "Checking…" : "Unavailable"}`,
                        },
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
                    {gitIntegrations[gitProvider] !== "connected" ? (
                      <Pressable
                        onPress={() => void configureGitProvider(gitProvider)}
                        disabled={startingGitOAuth !== null || gitIntegrations[gitProvider] === "checking"}
                        style={[styles.btnSecondary, { borderColor: c.border, marginTop: 10, opacity: startingGitOAuth || gitIntegrations[gitProvider] === "checking" ? 0.55 : 1 }]}
                      >
                        {startingGitOAuth === gitProvider ? (
                          <ActivityIndicator color={c.textMuted} />
                        ) : (
                          <Text style={[styles.btnText, { color: c.textPrimary }]}>Configure {gitProvider === "github" ? "GitHub" : "GitLab"} on remote box</Text>
                        )}
                      </Pressable>
                    ) : null}
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
                      Yaver creates its own private repo first, then mirrors this repo to {gitProvider}.com. Leave blank to use {repoNameSlug || "the project slug"}.
                    </Text>
                  </>
                ) : (
                  <Text style={[styles.muted, { color: c.textMuted, marginTop: 12 }]}>
                    No managed git for now. You can turn on Yaver Managed Git or connect a provider later from the project.
                  </Text>
                )}
              </>
            ) : null}

            {step === 1 ? (
              <>
                <Text style={[styles.label, { color: c.textMuted }]}>Where should this workspace run?</Text>
                {(
                  [
                    {
                      id: "current-agent" as StartMode,
                      label: activeRunnerDevice && primaryDeviceId === activeRunnerDevice.id
                        ? "Primary device · Recommended"
                        : "Connected machine",
                      sub: activeRunnerDevice
                        ? `${activeRunnerDevice.name} will handle vibing and rendering${primaryRunnerByDevice[activeRunnerDevice.id] ? ` · ${primaryRunnerByDevice[activeRunnerDevice.id]}${primaryModelByDevice[activeRunnerDevice.id] ? ` · ${primaryModelByDevice[activeRunnerDevice.id]}` : ""}` : ""}`
                        : "Connect a Yaver machine first",
                    },
                    ...(devMachines.length > 0
                      ? [{
                          id: "dev-hw" as StartMode,
                          label: selectedDevMachine && primaryDeviceId === selectedDevMachine.id
                            ? "Primary device · Recommended"
                            : "Other online box",
                          sub: selectedDevMachine
                            ? `${selectedDevMachine.name} will handle vibing and rendering${primaryRunnerByDevice[selectedDevMachine.id] ? ` · ${primaryRunnerByDevice[selectedDevMachine.id]}${primaryModelByDevice[selectedDevMachine.id] ? ` · ${primaryModelByDevice[selectedDevMachine.id]}` : ""}` : ""}`
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
                      placementTouchedRef.current = true;
                      setStartMode(opt.id);
                      setCodingMode("runner");
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

                {(
                  <>
                    <Text style={[styles.label, { color: c.textMuted, marginTop: 12 }]}>Yaver Serverless</Text>
                    <View style={[styles.reviewCard, { backgroundColor: c.bg, borderColor: c.border, marginTop: 4 }]}>
                      <Text style={[styles.reviewTitle, { color: c.textPrimary }]}>
                        {startMode === "yaver-cloud"
                          ? "Yaver Cloud selected · SQLite-first"
                          : startMode === "dev-hw"
                            ? selectedDevMachine
                              ? "Online box selected · SQLite-first"
                              : "Pick an online box"
                            : activeRunnerDevice
                              ? "Connected machine ready · SQLite-first"
                              : "No machine connected"}
                      </Text>
                      <Text style={[styles.muted, { color: c.textMuted, marginTop: 4 }]}>
                        {startMode === "yaver-cloud"
                          ? "Yaver Serverless will create this portable workspace on a managed cloud machine."
                          : startMode === "dev-hw"
                            ? selectedDevMachine
                              ? `${selectedDevMachine.name} will own this portable Yaver Serverless workspace.`
                              : "Choose which online box should own this workspace."
                            : activeRunnerDevice
                              ? `${activeRunnerDevice.name} is connected. This Yaver Serverless project will be created there.`
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
                              onPress={() => {
                                placementTouchedRef.current = true;
                                runnerDeviceRef.current = null;
                                setSelectedDevMachineId(m.id);
                              }}
                              style={[
                                styles.choiceCard,
                                {
                                  backgroundColor: active ? c.accent + "22" : "transparent",
                                  borderColor: active ? c.accent : c.border,
                                },
                              ]}
                            >
                              <Text style={[styles.templateLabel, { color: c.textPrimary }]}>
                                {m.name}{m.local ? "  (LAN)" : ""}{primaryDeviceId === m.id ? "  · Primary" : ""}
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
                    {!runnerDevice ? (
                      <View style={[styles.reviewCard, { backgroundColor: c.bg, borderColor: c.border, marginTop: 4 }]}>
                        <Text style={[styles.reviewTitle, { color: c.textPrimary }]}>
                          Connect a Yaver machine first
                        </Text>
                        <Text style={[styles.muted, { color: c.textMuted, marginTop: 4 }]}>
                          Pair a remote development box, then return here. The box will handle both vibing and rendering.
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
                      <>
                        {workspaceStatusLoading ? <ActivityIndicator color={c.textMuted} style={{ marginTop: 10 }} /> : null}
                        {!workspaceStatusLoading && !workspaceStatus ? (
                          <View
                            accessibilityRole="alert"
                            style={[styles.reviewCard, { backgroundColor: c.bg, borderColor: c.border, marginTop: 8 }]}
                          >
                            <Text style={[styles.reviewTitle, { color: c.textPrimary }]}>
                              {workspaceStatusFailure === "agent-upgrade-required"
                                ? "Agent update required"
                                : "Couldn't audit this remote box"}
                            </Text>
                            <Text style={[styles.muted, { color: c.textMuted, marginTop: 4 }]}>
                              {workspaceStatusFailure === "agent-upgrade-required"
                                ? "This box is reachable, but its Yaver agent predates Mobile Workspace readiness checks. Update it here, then setup resumes automatically."
                                : "The box did not answer the readiness probe. Its runner and provider state has not been changed."}
                            </Text>
                            {workspaceAgentUpdate.kind !== "idle" ? (
                              <Text
                                accessibilityLiveRegion="polite"
                                style={{
                                  color: workspaceAgentUpdate.kind === "failed" ? "#ef4444" : c.textMuted,
                                  fontSize: 11,
                                  marginTop: 8,
                                }}
                              >
                                {workspaceAgentUpdate.detail}
                              </Text>
                            ) : null}
                            <Pressable
                              onPress={() => workspaceStatusFailure === "agent-upgrade-required"
                                ? void updateWorkspaceAgent()
                                : void loadWorkspaceReadiness()}
                              disabled={workspaceAgentUpdate.kind === "updating"}
                              style={[
                                styles.btnSecondary,
                                {
                                  borderColor: c.border,
                                  marginTop: 10,
                                  opacity: workspaceAgentUpdate.kind === "updating" ? 0.55 : 1,
                                },
                              ]}
                            >
                              <Text style={[styles.btnText, { color: c.textPrimary }]}>
                                {workspaceAgentUpdate.kind === "updating"
                                  ? "Updating agent…"
                                  : workspaceStatusFailure === "agent-upgrade-required"
                                    ? workspaceAgentUpdate.kind === "failed" ? "Retry agent update →" : "Update Yaver agent →"
                                    : "Retry readiness check →"}
                              </Text>
                            </Pressable>
                          </View>
                        ) : workspaceStatus ? (["opencode", "claude", "codex"] as const).map((runnerId) => {
                          const gate = workspaceStatus?.runners.find((item) => item.id === runnerId);
                          const inventory = discoveredRunners.find((item) => item.id === runnerId);
                          const active = runner === runnerId || (runner === "claude-code" && runnerId === "claude");
                          const saved = primaryRunnerByDevice[runnerDevice.id] === runnerId;
                          const recommended = runnerId === "opencode";
                          const installing = runnerInstall?.runner === runnerId;
                          const label = runnerId === "opencode" ? "OpenCode" : runnerId === "claude" ? "Claude Code" : "Codex";
                          const ready = gate?.ready === true;
                          const statusLabel = ready
                            ? "Ready"
                            : gate?.configured
                              ? "Needs verification"
                              : inventory?.installed || gate
                                ? "Not configured"
                                : "Not installed";
                          return (
                            <Pressable
                              key={runnerId}
                              onPress={() => ready ? setRunner(runnerId) : void configureWorkspaceRunner(runnerId, gate?.code)}
                              disabled={runnerInstall !== null}
                              style={[
                                styles.choiceCard,
                                {
                                  backgroundColor: active ? c.accent + "22" : c.bgCard,
                                  borderColor: active ? c.accent : c.border,
                                  marginTop: 8,
                                },
                              ]}
                            >
                              <Text style={{ color: c.textPrimary, fontWeight: "600" }}>
                                {label}
                                {saved ? " · Preferred" : recommended ? " · Recommended" : ""}
                              </Text>
                              <Text style={{ color: ready ? c.success : c.textMuted, fontSize: 11, marginTop: 3 }}>
                                {statusLabel}{gate?.detail ? ` · ${gate.detail}` : ""}
                              </Text>
                              {installing ? (
                                <View style={{ flexDirection: "row", alignItems: "center", gap: 8, marginTop: 8 }}>
                                  <ActivityIndicator size="small" color={c.accent} />
                                  <Text numberOfLines={2} style={{ color: c.textMuted, fontSize: 11, flex: 1 }}>
                                    {runnerInstall.line}
                                  </Text>
                                </View>
                              ) : null}
                              {!installing && !ready && gate?.configured ? (
                                <Pressable onPress={() => void testWorkspaceRunner(runnerId)} style={{ marginTop: 8 }}>
                                  <Text style={{ color: c.accent, fontWeight: "600", fontSize: 12 }}>Test on remote box →</Text>
                                </Pressable>
                              ) : !installing && !ready ? (
                                <Text style={{ color: c.accent, fontWeight: "600", fontSize: 12, marginTop: 8 }}>
                                  {gate?.action?.label || (inventory?.installed ? `Configure ${label}` : `Install ${label}`)} →
                                </Text>
                              ) : null}
                            </Pressable>
                          );
                        }) : null}
                        {workspaceStatus && runner ? (() => {
                          const normalizedRunner = runner === "claude-code" ? "claude" : runner;
                          const inventory = discoveredRunners.find((item) => item.id === normalizedRunner);
                          if (!inventory?.models?.length) return null;
                          return (
                            <>
                              <Text style={[styles.label, { color: c.textMuted, marginTop: 12 }]}>Model</Text>
                              <ScrollView horizontal showsHorizontalScrollIndicator={false}>
                                {inventory.models.map((item) => {
                                  const activeModel = model === item.id;
                                  return (
                                    <Pressable
                                      key={item.id}
                                      onPress={() => setModel(item.id)}
                                      style={[
                                        styles.modeChip,
                                        {
                                          backgroundColor: activeModel ? c.accent : c.bgCard,
                                          borderColor: activeModel ? c.accent : c.border,
                                          marginRight: 8,
                                          marginTop: 8,
                                        },
                                      ]}
                                    >
                                      <Text style={{ color: activeModel ? c.bg : c.textPrimary, fontWeight: "600" }}>{item.name}</Text>
                                      <Text style={{ color: activeModel ? c.bg : c.textMuted, fontSize: 11, marginTop: 2 }}>{item.id}</Text>
                                    </Pressable>
                                  );
                                })}
                              </ScrollView>
                            </>
                          );
                        })() : null}
                      </>
                    )}
                    </>
                      );
                    })()}
                      </>
                    ) : null}
                  </>
                )}
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
              <>
                <View style={[styles.reviewCard, { backgroundColor: c.bg, borderColor: c.border }]}>
                  <Text style={[styles.reviewTitle, { color: c.textPrimary }]}>Setting up your project</Text>
                  {(setupSteps.length
                    ? setupSteps
                    : [
                        { key: "ai", label: "Connecting to AI", status: "pending" as const },
                        { key: "runtime", label: "Preparing runtime", status: "pending" as const },
                      ]
                  )
                    .filter((s) => s.status !== "skipped")
                    .map((s) => (
                      <View key={s.key} style={{ flexDirection: "row", alignItems: "center", paddingVertical: 6 }}>
                        <Text style={{ width: 26, fontSize: 16 }}>
                          {s.status === "done" ? "✅" : s.status === "running" ? "⏳" : "○"}
                        </Text>
                        <Text style={{ color: s.status === "done" ? c.textPrimary : c.textMuted, fontSize: 14 }}>
                          {s.label}
                          {s.key === "ai" && s.status === "done" ? " — pong ✓" : ""}
                        </Text>
                      </View>
                    ))}
                  <Text style={[styles.muted, { color: c.textMuted, marginTop: 8 }]}>
                    {setupSteps.length > 0 && setupSteps.every((s) => s.status === "done" || s.status === "skipped")
                      ? "Ready — tap Next to add branding."
                      : "Hang tight…"}
                  </Text>
                </View>
              </>
            ) : null}

            {step === 5 ? (
              <>
                {!surveySkipped && Object.keys(surveyAnswers).length > 0 ? (
                  <View style={[styles.reviewCard, { backgroundColor: c.bg, borderColor: c.border, marginBottom: 12 }]}>
                    <Text style={[styles.reviewTitle, { color: c.textPrimary }]}>From your survey</Text>
                    <Text style={[styles.muted, { color: c.textMuted, marginTop: 4 }]}>
                      {buildSurveyParagraph(surveyAnswers).replace(/^\[Survey\]\n/, "")}
                    </Text>
                  </View>
                ) : null}
                <View style={{ flexDirection: "row", alignItems: "center", gap: 14, marginBottom: 16 }}>
                  <View
                    style={{
                      width: 68,
                      height: 68,
                      borderRadius: 18,
                      alignItems: "center",
                      justifyContent: "center",
                      backgroundColor: primaryHex,
                      borderWidth: 5,
                      borderColor: secondaryHex,
                    }}
                  >
                    <Text style={{ color: "#fff", fontSize: 32, fontWeight: "800" }}>
                      {HOME_ICON_PRESETS.find((item) => item.id === homeIcon)?.glyph ?? "✦"}
                    </Text>
                  </View>
                  <View style={{ flex: 1 }}>
                    <Text style={[styles.reviewTitle, { color: c.textPrimary }]}>
                      {name.trim() || "My app"}
                    </Text>
                    <Text style={[styles.muted, { color: c.textMuted, marginTop: 4 }]}>
                      Preview of the shortcut users can add to iPhone or Android after publishing.
                    </Text>
                  </View>
                </View>
                <Text style={[styles.label, { color: c.textMuted }]}>Choose an icon</Text>
                <View style={{ flexDirection: "row", flexWrap: "wrap", gap: 8, marginBottom: 16 }}>
                  {HOME_ICON_PRESETS.map((item) => {
                    const selected = item.id === homeIcon;
                    return (
                      <Pressable
                        key={item.id}
                        accessibilityRole="button"
                        accessibilityLabel={item.label}
                        accessibilityState={{ selected }}
                        onPress={() => setHomeIcon(item.id)}
                        style={{
                          width: 48,
                          height: 48,
                          borderRadius: 13,
                          alignItems: "center",
                          justifyContent: "center",
                          borderWidth: selected ? 2 : 1,
                          borderColor: selected ? c.accent : c.border,
                          backgroundColor: selected ? `${c.accent}22` : c.bg,
                        }}
                      >
                        <Text style={{ color: c.textPrimary, fontSize: 24, fontWeight: "700" }}>{item.glyph}</Text>
                      </Pressable>
                    );
                  })}
                </View>
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
                <Text style={[styles.label, { color: c.textMuted, marginTop: 4 }]}>Colour palette</Text>
                <View style={{ flexDirection: "row", flexWrap: "wrap", gap: 8, marginBottom: 14 }}>
                  {PALETTES.map((pal) => {
                    const sel = primaryHex === pal.primary && secondaryHex === pal.secondary;
                    return (
                      <Pressable
                        key={pal.name}
                        onPress={() => { setPrimaryHex(pal.primary); setSecondaryHex(pal.secondary); }}
                        style={{
                          borderWidth: 2,
                          borderColor: sel ? c.accent : c.border,
                          borderRadius: 12,
                          paddingVertical: 6,
                          paddingHorizontal: 8,
                          flexDirection: "row",
                          alignItems: "center",
                          gap: 6,
                          backgroundColor: c.bg,
                        }}
                      >
                        <View style={{ width: 18, height: 18, borderRadius: 5, backgroundColor: pal.primary }} />
                        <View style={{ width: 18, height: 18, borderRadius: 5, backgroundColor: pal.secondary }} />
                        <Text style={{ color: c.textPrimary, fontSize: 12, fontWeight: "600" }}>{pal.name}</Text>
                      </Pressable>
                    );
                  })}
                </View>
                <Text style={[styles.label, { color: c.textMuted }]}>Primary colour</Text>
                <View style={{ flexDirection: "row", flexWrap: "wrap", gap: 8, marginBottom: 14 }}>
                  {SWATCHES.map((hex) => (
                    <Pressable
                      key={`p${hex}`}
                      onPress={() => setPrimaryHex(hex)}
                      style={{
                        width: 32, height: 32, borderRadius: 9, backgroundColor: hex,
                        borderWidth: primaryHex === hex ? 3 : 1,
                        borderColor: primaryHex === hex ? c.textPrimary : c.border,
                      }}
                    />
                  ))}
                </View>
                <Text style={[styles.label, { color: c.textMuted }]}>Secondary colour</Text>
                <View style={{ flexDirection: "row", flexWrap: "wrap", gap: 8, marginBottom: 6 }}>
                  {SWATCHES.map((hex) => (
                    <Pressable
                      key={`s${hex}`}
                      onPress={() => setSecondaryHex(hex)}
                      style={{
                        width: 32, height: 32, borderRadius: 9, backgroundColor: hex,
                        borderWidth: secondaryHex === hex ? 3 : 1,
                        borderColor: secondaryHex === hex ? c.textPrimary : c.border,
                      }}
                    />
                  ))}
                </View>
                {primaryHex || secondaryHex ? (
                  <View style={{ flexDirection: "row", alignItems: "center", gap: 8, marginTop: 8 }}>
                    <Text style={{ color: c.textMuted, fontSize: 12 }}>Selected:</Text>
                    {primaryHex ? <View style={{ width: 22, height: 22, borderRadius: 6, backgroundColor: primaryHex }} /> : null}
                    {secondaryHex ? <View style={{ width: 22, height: 22, borderRadius: 6, backgroundColor: secondaryHex }} /> : null}
                  </View>
                ) : null}
              </>
            ) : null}

            {step === 6 ? (
              <>
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
                    {runner ? `${runner}${model ? ` · ${model}` : ""}` : "Remote runner"} ·{" "}
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
      selectedRunnerDevice,
      devMachines,
      devMachineRunners,
      runnersForDevice,
      importedConversation,
      importedBrief,
      mobileAiProvider,
      openAiKey,
      glmKey,
      availableRunners,
      gitIntegrations,
      configureGitProvider,
      configureWorkspaceRunner,
      discoveredRunners,
      model,
      primaryDeviceId,
      primaryRunnerByDevice,
      runner,
      runnerChoiceEnabled,
      startingGitOAuth,
      setupSteps,
      testWorkspaceRunner,
      loadWorkspaceReadiness,
      updateWorkspaceAgent,
      workspaceAgentUpdate,
      workspaceStatus,
      workspaceStatusFailure,
      workspaceStatusLoading,
      primaryHex,
      secondaryHex,
      logoUrl,
      surveyAnswers,
      surveySkipped,
      surveyIndex,
      refineQuestions,
      refineAnswers,
      refineLoading,
      refineUsed,
    ],
  );

  const wizardFooter = useMemo(() => {
    if (!showForm) return null;
    const nameOk = name.trim().length > 0;
    const normalizedRunner = runner === "claude-code" ? "claude" : runner;
    const selectedRunnerReady = !!workspaceStatus?.runners.find((item) => item.id === normalizedRunner)?.ready;
    const placementOk = step !== 1 || startMode === "yaver-cloud" || (
      !!selectedRunnerDevice && !!runner && selectedRunnerReady && !!model
    );
    const descOk = prompt.trim().length > 0 || importedConversation.trim().length > 0;
    const canAdvance = step === 0 ? nameOk : placementOk;
    const primaryLabel =
      workspaceStatusLoading && step === 1
        ? "Testing runner…"
        : step < 6
        ? !canAdvance && step === 0
          ? "Name required"
          : !canAdvance && step === 1
            ? !selectedRunnerDevice
              ? "Connect machine"
              : workspaceStatusFailure === "agent-upgrade-required"
                ? "Update agent above"
                : !workspaceStatus
                  ? "Retry check above"
              : !runner
                ? "Choose a runner"
                : !selectedRunnerReady
                  ? "Configure runner"
                  : "Choose a model"
            : "Next"
        : !descOk
          ? "Description required"
          : "Create workspace";

    return (
      <>
      {creating && createSteps.length > 0 ? (
        <View
          style={{
            paddingHorizontal: 16,
            paddingTop: 12,
            paddingBottom: 8,
            backgroundColor: c.bg,
            borderTopColor: c.border,
            borderTopWidth: 1,
          }}
        >
          <Text style={{ color: c.textSecondary, fontSize: 12, fontWeight: "700", marginBottom: 8, letterSpacing: 0.5 }}>
            SETTING UP YOUR PROJECT
          </Text>
          {createSteps
            .filter((s) => s.status !== "skipped")
            .map((s) => (
              <View key={s.key} style={{ flexDirection: "row", alignItems: "center", paddingVertical: 5 }}>
                <Text style={{ width: 24, fontSize: 15 }}>
                  {s.status === "done" ? "✅" : s.status === "running" ? "⏳" : "○"}
                </Text>
                <Text style={{ color: s.status === "done" ? c.textPrimary : c.textSecondary, fontSize: 14 }}>
                  {s.label}
                  {s.key === "ai" && s.status === "done" ? " — pong ✓" : ""}
                </Text>
              </View>
            ))}
        </View>
      ) : null}
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
        {step < 6 ? (
          <Pressable
            disabled={!canAdvance || workspaceStatusLoading}
            onPress={async () => {
              if (step === 1 && !(await persistPrimaryTaskTarget())) return;
              const next = Math.min(6, step + 1);
              if (next === 4) runSetup();
              setStep(next);
            }}
            style={[
              styles.btn,
              { backgroundColor: c.accent, flex: 1, opacity: canAdvance && !workspaceStatusLoading ? 1 : 0.4 },
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
      </>
    );
  }, [
    activeRunnerDevice,
    c,
    creating,
    createSteps,
    runSetup,
    importedConversation,
    insets.bottom,
    name,
    model,
    prompt,
    persistPrimaryTaskTarget,
    selectedDevMachine,
    devMachineRunners,
    selectedRunnerDevice,
    runner,
    showForm,
    startMode,
    step,
    workspaceStatus,
    workspaceStatusFailure,
    workspaceStatusLoading,
  ]);

  return (
    <KeyboardAvoidingView
      behavior={Platform.OS === "ios" ? "padding" : undefined}
      keyboardVerticalOffset={Platform.OS === "ios" ? 84 : 0}
      style={{ flex: 1, backgroundColor: c.bg }}
    >
      <AppScreenHeader title="Mobile Workspace" onBack={() => router.back()} />
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
      <RunnerAuthModal
        visible={runnerAuthModalRunner !== null}
        runner={runnerAuthModalRunner || "codex"}
        deviceName={selectedRunnerDevice?.name || "remote box"}
        target={selectedRunnerDevice?.id === activeDevice?.id ? undefined : selectedRunnerDevice?.id}
        onClose={() => setRunnerAuthModalRunner(null)}
        onCompleted={() => void loadWorkspaceReadiness()}
      />
      <OpenCodeConfigModal
        visible={openCodeConfigVisible}
        target={selectedRunnerDevice?.id === activeDevice?.id ? undefined : selectedRunnerDevice?.id}
        startInAddProvider={!workspaceStatus?.openCode.configured}
        onClose={() => {
          setOpenCodeConfigVisible(false);
          void loadWorkspaceReadiness();
        }}
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
