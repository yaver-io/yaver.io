import Constants from "expo-constants";
import { Ionicons, FontAwesome } from "@expo/vector-icons";
import { router, useLocalSearchParams } from "expo-router";
import React, { useEffect, useRef, useState } from "react";
import {
  ActivityIndicator,
  Alert,
  AppState,
  KeyboardAvoidingView,
  Linking,
  Modal,
  Platform,
  Pressable,
  ScrollView,
  StyleSheet,
  Switch,
  Text,
  TextInput,
  View,
} from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import AsyncStorage from "@react-native-async-storage/async-storage";
import * as WebBrowser from "expo-web-browser";
import { OAUTH_REDIRECT } from "../../src/_core/constants";
import { useAuth } from "../../src/context/AuthContext";
import { useDevice } from "../../src/context/DeviceContext";
import { customRelaysKey, customTunnelsKey } from "../../src/context/DeviceContext";
import { AppScreenHeader } from "../../src/components/AppScreenHeader";
import { OpenCodeConfigModal } from "../../src/components/OpenCodeConfigModal";
import { CodingAgentsSection } from "../../src/components/DeviceDetailsModal";
import { YaverAgentSettings } from "../../src/components/YaverAgentSettings";
import BoxInitSection from "../../src/components/BoxInitSection";
import CloudProvidersSection from "../../src/components/CloudProvidersSection";
import HetznerSection from "../../src/components/HetznerSection";
import { useColors, useTheme } from "../../src/context/ThemeContext";
import { deleteAccount as deleteAccountApi, updateProfile, changePassword as changePasswordApi, getUserSettings, saveUserSettings, getDeviceMetrics, getDeviceEvents, type DeviceMetric, type DeviceEvent, getUsageSummary, type UsageSummary, type SpeechProvider, type TtsProvider, type KeyStorage, LOCAL_KEYS, getLocalSecret, saveLocalSecret, deleteLocalSecret, getKeyStoragePreference, saveKeyStoragePreference, loadLocalSpeechConfig, saveLocalSpeechConfig, getAuthConfig, setAccountPassword as setAccountPasswordApi, listAuthIdentities, startLinkIntent, unlinkProvider as unlinkProviderApi, startMergeIntent, cancelMergeIntent, type AuthIdentity, type OAuthProvider, type MergeIntent } from "../../src/lib/auth";
import { SPEECH_PROVIDERS, TTS_PROVIDERS, STT_MODELS, TTS_MODELS, TTS_VOICES, DEFAULT_STT_MODEL, DEFAULT_TTS_MODEL, DEFAULT_TTS_VOICE } from "../../src/lib/speech";
import { clearCache } from "../../src/lib/storage";
import * as ExpoClipboard from "expo-clipboard";
import * as ExpoLinking from "expo-linking";
import { getLogEntries, clearLogEntries, onLogsChanged, LogEntry } from "../../src/lib/logger";
import { quicClient, type AgentStatus, type CapabilitySnapshot, type EnvironmentProfileApplyResult, type GitOAuthStatus, type IncidentEvent, type MachineOnboardingProviderStatus, type RelayServer, type RunnerAuthStatusRow, type TunnelServer } from "../../src/lib/quic";
import { loadTaskVideoSummaryEnabled, saveTaskVideoSummaryEnabled } from "../../src/lib/taskComposerPrefs";
import { useTabletContentStyle } from "../../src/hooks/useTabletContentStyle";
import { OPTIONAL_MORE_TOOLS, normalizeOptionalMoreTools, type OptionalMoreToolId } from "../../src/lib/moreOptionalTools";

WebBrowser.maybeCompleteAuthSession();

const LEGACY_OAUTH_REDIRECT = "yaver:///oauth-callback";

function isOAuthCallbackUrl(url: string): boolean {
  return url.startsWith(OAUTH_REDIRECT) || url.startsWith(LEGACY_OAUTH_REDIRECT);
}

const RUNNER_OPTIONS: ReadonlyArray<{
  runnerId: "claude-code" | "codex" | "opencode";
  name: string;
  description: string;
}> = [
  {
    runnerId: "claude-code",
    name: "Claude Code",
    description: "Anthropic Claude CLI with streaming",
  },
  {
    runnerId: "codex",
    name: "OpenAI Codex",
    description: "OpenAI Codex CLI",
  },
  {
    runnerId: "opencode",
    name: "OpenCode",
    description: "Bring your own provider: OpenRouter, Gemini, GLM, Ollama, and more.",
  },
];

const APP_VERSION = Constants.expoConfig?.version ?? "1.0.0";
const BUILD_NUMBER =
  Constants.expoConfig?.ios?.buildNumber ??
  Constants.expoConfig?.android?.versionCode?.toString() ??
  "1";

type ProviderKeyId = "openai" | "glm" | "anthropic";
type ProviderKeyScope = "phone-local" | "host-vault";
type ProviderKeyState = {
  provider: ProviderKeyId;
  scope: ProviderKeyScope;
  storage: KeyStorage;
  status: "saved" | "synced" | "failed";
  updatedAt: number;
  lastAttemptAt: number;
  lastSuccessAt?: number;
  lastFailureAt?: number;
  lastError?: string;
};

function mergeProviderKeyState(current: ProviderKeyState | undefined, incoming: ProviderKeyState): ProviderKeyState {
  if (!current) return incoming;
  const currentSuccess = current.lastSuccessAt ?? 0;
  const incomingSuccess = incoming.lastSuccessAt ?? 0;
  const currentFailure = current.lastFailureAt ?? 0;
  const incomingFailure = incoming.lastFailureAt ?? 0;
  const latestSuccessAt = Math.max(currentSuccess, incomingSuccess);
  const latestFailureAt = Math.max(currentFailure, incomingFailure);
  const latestUpdated = incoming.updatedAt >= current.updatedAt ? incoming : current;
  const latestSuccessState =
    incomingSuccess >= currentSuccess
      ? incoming
      : current;
  const latestFailureState =
    incomingFailure >= currentFailure
      ? incoming
      : current;
  return {
    ...latestUpdated,
    status: latestSuccessAt > 0 && latestSuccessAt >= latestFailureAt
      ? (latestSuccessState.scope === "host-vault" ? "synced" : "saved")
      : "failed",
    scope: latestSuccessAt >= currentSuccess ? latestSuccessState.scope : current.scope,
    storage: latestSuccessAt >= currentSuccess ? latestSuccessState.storage : current.storage,
    lastAttemptAt: Math.max(current.lastAttemptAt, incoming.lastAttemptAt),
    lastSuccessAt: latestSuccessAt || undefined,
    lastFailureAt: latestFailureAt || undefined,
    lastError: latestFailureAt > latestSuccessAt ? latestFailureState.lastError : undefined,
    updatedAt: Math.max(current.updatedAt, incoming.updatedAt),
  };
}

function providerKeyStatusLabel(state?: ProviderKeyState): string {
  if (!state) return "No status yet";
  const stamp = state.lastSuccessAt ?? state.lastFailureAt ?? state.updatedAt;
  const minutesAgo = stamp > 0 ? Math.max(0, Math.round((Date.now() - stamp) / 60000)) : null;
  const age = minutesAgo == null ? "" : minutesAgo < 1 ? "just now" : `${minutesAgo}m ago`;
  if (state.status === "failed") {
    return `${state.scope === "host-vault" ? "Host vault" : "Phone"} failed${age ? ` ${age}` : ""}${state.lastError ? ` · ${state.lastError}` : ""}`;
  }
  return `${state.scope === "host-vault" ? "Host vault" : "Phone"} ${state.status}${age ? ` ${age}` : ""}`;
}

export default function SettingsScreen() {
  const LEAN_SETTINGS_SURFACE = true;
  const KEEP_SANDBOX_SURFACE = true;
  const SHOW_HOST_NOTIFICATION_CHANNELS = false;
  // "wide" clamp (960pt) instead of "regular" (720pt) — Settings on
  // a 1340pt landscape tablet had 4-5 sub-sections rendering as a
  // narrow phone strip in the middle with empty whitespace on both
  // sides. 960pt still keeps reading lines tight while using the
  // canvas. Phone returns {} from the hook so phone layout unchanged.
  const tabletContent = useTabletContentStyle("wide");
  const { user, token, logout, refreshUser } = useAuth();
  const {
    devices,
    activeDevice,
    connectionStatus,
    disconnect,
    selectDevice,
    refreshDevices,
    multiTargetMode,
    setMultiTargetMode,
    primaryDeviceId,
    setPrimaryDevice,
    secondaryDeviceId,
    setSecondaryDevice,
  } = useDevice();
  const { isDark, toggleTheme } = useTheme();
  const c = useColors();
  const insets = useSafeAreaInsets();
  // Name is "empty" if it equals the email or is blank
  const displayName = user?.name && user.name !== user.email ? user.name : null;
  const [isEditingName, setIsEditingName] = useState(false);
  const [editName, setEditName] = useState(user?.name ?? "");
  const [isSavingName, setIsSavingName] = useState(false);
  const [isClearing, setIsClearing] = useState(false);
  const [isCleaning, setIsCleaning] = useState(false);
  const [deleteConfirm, setDeleteConfirm] = useState("");
  const [deletingAccount, setDeletingAccount] = useState(false);
  const [machineDeleteConfirm, setMachineDeleteConfirm] = useState("");
  const [removingMachine, setRemovingMachine] = useState(false);
  const [enrollingPasskey, setEnrollingPasskey] = useState(false);
  const [passkeyEnrollMessage, setPasskeyEnrollMessage] = useState<string | null>(null);
  const [verifyEmailBusy, setVerifyEmailBusy] = useState(false);
  const [verifyEmailMessage, setVerifyEmailMessage] = useState<string | null>(null);
  // Streaming uninstall progress: mirrors the web AccountsView. Each
  // entry is one step the remote agent emits via /streams/<machine-remove:...>.
  // Cleared when the user starts a fresh removal.
  const [machineRemoveSteps, setMachineRemoveSteps] = useState<
    { step: string; status: string; detail: string; error?: string }[]
  >([]);
  const [identities, setIdentities] = useState<AuthIdentity[]>([]);
  const [linkingProvider, setLinkingProvider] = useState<OAuthProvider | null>(null);
  const [unlinkingProvider, setUnlinkingProvider] = useState<AuthIdentity["provider"] | null>(null);
  const [authError, setAuthError] = useState<string | null>(null);
  const [emailPasswordEnabled, setEmailPasswordEnabled] = useState(false);
  const [newAccountPassword, setNewAccountPassword] = useState("");
  const [confirmAccountPassword, setConfirmAccountPassword] = useState("");
  const [settingAccountPassword, setSettingAccountPassword] = useState(false);
  const [accountPasswordMessage, setAccountPasswordMessage] = useState<string | null>(null);
  const [accountPasswordError, setAccountPasswordError] = useState<string | null>(null);
  const [mergeIntent, setMergeIntent] = useState<MergeIntent | null>(null);
  const [mergeStarting, setMergeStarting] = useState(false);
  const [showLogs, setShowLogs] = useState(false);
  const [logs, setLogs] = useState<LogEntry[]>(getLogEntries());
  const [forceRelay, setForceRelay] = useState(quicClient.forceRelay);
  const [debugLogsEnabled, setDebugLogsEnabled] = useState(false);
  const [showGuide, setShowGuide] = useState(false);
  const [guideSection, setGuideSection] = useState<string | null>(null);
  const [selectedRunner, setSelectedRunner] = useState<string>("claude-code");
  const [agentVersion, setAgentVersion] = useState<string | null>(null);
  const [agentLastPing, setAgentLastPing] = useState<Date | null>(null);
  const [agentStatus, setAgentStatus] = useState<AgentStatus | null>(null);
  const [pingRtt, setPingRtt] = useState<number | null>(null);
  const [isPinging, setIsPinging] = useState(false);
  const [isShuttingDown, setIsShuttingDown] = useState(false);
  const [metrics, setMetrics] = useState<DeviceMetric[]>([]);
  const [events, setEvents] = useState<DeviceEvent[]>([]);
  const [showMetrics, setShowMetrics] = useState(false);
  const [usageSummary, setUsageSummary] = useState<UsageSummary | null>(null);

  // Integrations
  const [showIntegrations, setShowIntegrations] = useState(false);
  const [showFeedbackSDK, setShowFeedbackSDK] = useState(false);
  const [feedbackEnabled, setFeedbackEnabled] = useState(false);
  const [feedbackTrigger, setFeedbackTrigger] = useState<'shake' | 'floating-button' | 'manual'>('floating-button');
  const [feedbackMode, setFeedbackMode] = useState<'live' | 'narrated' | 'batch'>('live');
  const [blackBoxEnabled, setBlackBoxEnabled] = useState(false);
  const [feedbackVoice, setFeedbackVoice] = useState(true);
  const [feedbackButtonColor, setFeedbackButtonColor] = useState("#6366f1");
  const [intgConfig, setIntgConfig] = useState<Record<string, any>>({});
  const [intgLoading, setIntgLoading] = useState(false);
  const [intgSaving, setIntgSaving] = useState(false);

  // Speech settings
  const [speechProvider, setSpeechProvider] = useState<SpeechProvider | null>("on-device");
  const [speechApiKey, setSpeechApiKey] = useState("");
  const [sttModel, setSttModel] = useState(DEFAULT_STT_MODEL);
  const [ttsModel, setTtsModel] = useState(DEFAULT_TTS_MODEL);
  const [ttsVoice, setTtsVoice] = useState(DEFAULT_TTS_VOICE);
  const [openAiApiKey, setOpenAiApiKey] = useState("");
  const [glmApiKey, setGlmApiKey] = useState("");
  const [anthropicApiKey, setAnthropicApiKey] = useState("");
  // OpenCode config full editor — opens a sheet that hits
  // /runner/opencode/config on the connected device.
  const [showOpenCodeConfig, setShowOpenCodeConfig] = useState(false);
  const [openCodeStartInAddProvider, setOpenCodeStartInAddProvider] = useState(false);
  const [mobileCodingProvider, setMobileCodingProvider] = useState<"openai" | "glm">("openai");
  const [ttsEnabled, setTtsEnabled] = useState(false);
  const [ttsProvider, setTtsProvider] = useState<TtsProvider>("device");
  const [ttsTaskMode, setTtsTaskMode] = useState(false);
  const [verbosity, setVerbosity] = useState(10);
  const [showSpeechConfig, setShowSpeechConfig] = useState(false);
  const [isSavingSpeech, setIsSavingSpeech] = useState(false);
  const [isSavingAiProviders, setIsSavingAiProviders] = useState(false);
  const [isSyncingAiVault, setIsSyncingAiVault] = useState(false);
  const [isSyncingRunnerAuth, setIsSyncingRunnerAuth] = useState(false);
  const [runnerAuthRows, setRunnerAuthRows] = useState<RunnerAuthStatusRow[]>([]);
  const [runnerCapabilitySnapshot, setRunnerCapabilitySnapshot] = useState<CapabilitySnapshot | null>(null);
  const [runnerIncidents, setRunnerIncidents] = useState<IncidentEvent[]>([]);
  const [machineOnboardingRows, setMachineOnboardingRows] = useState<MachineOnboardingProviderStatus[]>([]);
  const [machineOnboardingRowsByDevice, setMachineOnboardingRowsByDevice] = useState<Record<string, MachineOnboardingProviderStatus[]>>({});
  const [githubToken, setGithubToken] = useState("");
  const [gitlabToken, setGitlabToken] = useState("");
  const [gitlabHost, setGitlabHost] = useState("gitlab.com");
  const [isApplyingMachineOnboarding, setIsApplyingMachineOnboarding] = useState(false);
  const [removingOnboardingProvider, setRemovingOnboardingProvider] = useState<"github" | "gitlab" | null>(null);
  const [startingGitOAuthProvider, setStartingGitOAuthProvider] = useState<"github" | "gitlab" | null>(null);
  const [gitOAuthFlow, setGitOAuthFlow] = useState<(GitOAuthStatus & { deviceId: string; deviceName: string }) | null>(null);
  const [selectedOnboardingTargetIds, setSelectedOnboardingTargetIds] = useState<string[]>([]);
  const [providerKeyStates, setProviderKeyStates] = useState<Record<string, ProviderKeyState>>({});
  const [showToolchainSync, setShowToolchainSync] = useState(false);
  const [taskVideoSummaryEnabled, setTaskVideoSummaryEnabled] = useState(false);
  const [moreOptionalTools, setMoreOptionalTools] = useState<OptionalMoreToolId[]>([]);
  const [toolchainSourceId, setToolchainSourceId] = useState<string | null>(null);
  const [toolchainSyncGitCredentials, setToolchainSyncGitCredentials] = useState(true);
  const [toolchainSyncProviderKeys, setToolchainSyncProviderKeys] = useState(true);
  const [toolchainSyncPresets, setToolchainSyncPresets] = useState(true);
  const [toolchainSyncFlags, setToolchainSyncFlags] = useState(false);
  const [toolchainSyncEnv, setToolchainSyncEnv] = useState(false);
  const [toolchainSyncMonitors, setToolchainSyncMonitors] = useState(false);
  const [toolchainInstallMissing, setToolchainInstallMissing] = useState(true);
  const [toolchainRemoveMissing, setToolchainRemoveMissing] = useState(false);
  const [isPreviewingToolchainSync, setIsPreviewingToolchainSync] = useState(false);
  const [isApplyingToolchainSync, setIsApplyingToolchainSync] = useState(false);
  const [toolchainPreview, setToolchainPreview] = useState<EnvironmentProfileApplyResult | null>(null);

  // Key storage preference: "local" = device Keychain only, "cloud" = sync to Convex
  const [keyStorage, setKeyStorage] = useState<KeyStorage>("local");

  // Contributor dogfood
  const [dogfoodRepoDir, setDogfoodRepoDir] = useState("");
  const [dogfoodPrompt, setDogfoodPrompt] = useState(
    "Refresh Yaver using Yaver. Use the Go agent for code changes, keep the mobile app loadable in Yaver, and prefer Hermes/mobile-safe workflows.",
  );


  // Test App
  const [showTestApp, setShowTestApp] = useState(false);
  const [testTarget, setTestTarget] = useState<'device' | 'simulator' | null>(null);
  const [testRunning, setTestRunning] = useState(false);
  const [testExecId, setTestExecId] = useState<string | null>(null);
  const [agentLogs, setAgentLogs] = useState<string[]>([]);
  const agentLogsRef = useRef<ScrollView>(null);

  useEffect(() => {
    loadTaskVideoSummaryEnabled()
      .then(setTaskVideoSummaryEnabled)
      .catch(() => {});
  }, []);
  const testAbortRef = useRef<AbortController | null>(null);

  // Container Sandbox
  const [sandboxStatus, setSandboxStatus] = useState<import("../../src/lib/quic").SandboxStatus | null>(null);
  const [sandboxLoading, setSandboxLoading] = useState(false);
  const [sandboxBuilding, setSandboxBuilding] = useState(false);
  const [sandboxSaving, setSandboxSaving] = useState(false);

  const scrollViewRef = useRef<ScrollView>(null);
  // Y-offset of the Account section, captured via onLayout. Lets a
  // deep-link (?linkAccount=1 from the Tasks empty-state "link an
  // existing account" prompt) scroll straight to the sign-in-method
  // buttons instead of dumping the user at the top of Settings.
  const accountSectionY = useRef(0);

  // Relay servers
  const [customRelays, setCustomRelays] = useState<RelayServer[]>([]);
  const [showAddRelay, setShowAddRelay] = useState(false);
  const [newRelayUrl, setNewRelayUrl] = useState("");
  const [newRelayPassword, setNewRelayPassword] = useState("");
  const [newRelayLabel, setNewRelayLabel] = useState("");
  const [testingRelayId, setTestingRelayId] = useState<string | null>(null);
  const [relayTestResults, setRelayTestResults] = useState<Record<string, { ok: boolean; ms?: number; error?: string }>>({});
  const [relaySyncEnabled, setRelaySyncEnabled] = useState(false);

  // Advanced HTTPS endpoints kept for compatibility; not shown in the normal UI.
  const [customTunnels, setCustomTunnels] = useState<TunnelServer[]>([]);
  const [showAddTunnel, setShowAddTunnel] = useState(false);
  const [newTunnelUrl, setNewTunnelUrl] = useState("");
  const [newTunnelCfClientId, setNewTunnelCfClientId] = useState("");
  const [newTunnelCfClientSecret, setNewTunnelCfClientSecret] = useState("");
  const [newTunnelLabel, setNewTunnelLabel] = useState("");
  const [testingTunnelId, setTestingTunnelId] = useState<string | null>(null);
  const [tunnelTestResults, setTunnelTestResults] = useState<Record<string, { ok: boolean; ms?: number; error?: string }>>({});

  const providerBadge = (provider: AuthIdentity["provider"] | OAuthProvider, size: "sm" | "md" = "sm") => {
    const iconSize = size === "sm" ? 14 : 16;
    const circle = {
      width: size === "sm" ? 24 : 28,
      height: size === "sm" ? 24 : 28,
      borderRadius: 999,
      alignItems: "center" as const,
      justifyContent: "center" as const,
      borderWidth: 1,
      borderColor: c.border,
      backgroundColor: c.surfaceMuted,
    };

    switch (provider) {
      case "apple":
        return (
          <View style={circle}>
            <Ionicons name="logo-apple" size={iconSize} color={c.textPrimary} />
          </View>
        );
      case "github":
        return (
          <View style={circle}>
            <Ionicons name="logo-github" size={iconSize} color={c.textPrimary} />
          </View>
        );
      case "google":
        return (
          <View style={circle}>
            <Ionicons name="logo-google" size={iconSize - 1} color="#4285F4" />
          </View>
        );
      case "microsoft":
        return (
          <View style={circle}>
            <FontAwesome name="windows" size={iconSize - 1} color="#2563EB" />
          </View>
        );
      case "gitlab":
        return (
          <View style={circle}>
            <Text style={{ color: "#FC6D26", fontSize: size === "sm" ? 10 : 11, fontWeight: "700" }}>GL</Text>
          </View>
        );
      default:
        return (
          <View style={circle}>
            <Text style={{ color: c.textPrimary, fontSize: size === "sm" ? 10 : 11, fontWeight: "700", textTransform: "uppercase" }}>
              {provider.slice(0, 1)}
            </Text>
          </View>
        );
    }
  };

  // User-scoped storage keys
  const RELAYS_KEY = customRelaysKey(user?.id);
  const TUNNELS_KEY = customTunnelsKey(user?.id);
  const SYNC_KEY = user?.id ? `@yaver/u/${user.id}/relay_sync_enabled` : "@yaver/relay_sync_enabled";
  const DOGFOOD_KEY = user?.id ? `@yaver/u/${user.id}/dogfood_yaver` : "@yaver/dogfood_yaver";
  const PROVIDER_KEY_STATUS_KEY = user?.id ? `@yaver/u/${user.id}/provider_key_status` : "@yaver/provider_key_status";

  // Load custom relay servers and sync preference from AsyncStorage
  useEffect(() => {
    AsyncStorage.getItem(RELAYS_KEY).then((raw) => {
      if (raw) {
        try {
          setCustomRelays(JSON.parse(raw));
        } catch {}
      }
    });
    AsyncStorage.getItem(SYNC_KEY).then((val) => {
      setRelaySyncEnabled(val === "true");
    });
    AsyncStorage.getItem("@yaver/debug_logs_enabled").then((val) => {
      setDebugLogsEnabled(val === "true");
    });
    AsyncStorage.getItem(TUNNELS_KEY).then((raw) => {
      if (raw) {
        try {
          const tunnels = JSON.parse(raw);
          setCustomTunnels(tunnels);
          if (tunnels.length > 0) {
            quicClient.setTunnelServers(tunnels);
          }
        } catch {}
      }
    });
    AsyncStorage.getItem(DOGFOOD_KEY).then((raw) => {
      if (!raw) return;
      try {
        const cfg = JSON.parse(raw);
        if (typeof cfg.repoDir === "string") setDogfoodRepoDir(cfg.repoDir);
        if (typeof cfg.prompt === "string" && cfg.prompt.trim()) setDogfoodPrompt(cfg.prompt);
      } catch {}
    });
    AsyncStorage.getItem(PROVIDER_KEY_STATUS_KEY).then((raw) => {
      if (!raw) return;
      try {
        setProviderKeyStates(JSON.parse(raw));
      } catch {}
    });
    // Load feedback SDK config
    const fbKey = user?.id ? `@yaver/u/${user.id}/feedback_config` : "@yaver/feedback_config";
    AsyncStorage.getItem(fbKey).then((raw) => {
      if (raw) {
        try {
          const cfg = JSON.parse(raw);
          setFeedbackEnabled(cfg.enabled ?? false);
          setFeedbackTrigger(cfg.trigger ?? 'floating-button');
          setFeedbackMode(cfg.feedbackMode ?? 'live');
          setBlackBoxEnabled(cfg.blackBox ?? false);
          setFeedbackVoice(cfg.voiceEnabled ?? true);
          if (cfg.buttonColor) setFeedbackButtonColor(cfg.buttonColor);
        } catch {}
      }
    });
  }, [DOGFOOD_KEY, PROVIDER_KEY_STATUS_KEY, RELAYS_KEY, SYNC_KEY, TUNNELS_KEY, user?.id]);

  const mergeProviderStates = async (incomingStates: ProviderKeyState[], pushToHost = false) => {
    setProviderKeyStates((prev) => {
      const next = { ...prev };
      for (const state of incomingStates) {
        const key = `${state.provider}:${state.scope}`;
        next[key] = mergeProviderKeyState(next[key], state);
      }
      AsyncStorage.setItem(PROVIDER_KEY_STATUS_KEY, JSON.stringify(next)).catch(() => {});
      if (pushToHost && connectionStatus === "connected" && !activeDevice?.isGuest) {
        const items = Object.entries(next).map(([key, value]) => ({
          key,
          value,
          updatedAt: value.updatedAt,
          updatedBy: "mobile",
        }));
        quicClient.syncMerge("provider-keys", items).catch(() => {});
      }
      return next;
    });
  };

  useEffect(() => {
    if (connectionStatus !== "connected" || !!activeDevice?.isGuest) return;
    quicClient.syncList<ProviderKeyState>("provider-keys").then(({ items }) => {
      const incoming = items
        .map((item) => item?.value)
        .filter((value): value is ProviderKeyState => !!value && typeof value.provider === "string" && typeof value.scope === "string");
      if (incoming.length > 0) {
        mergeProviderStates(incoming, false).catch(() => {});
      }
    }).catch(() => {});
  }, [activeDevice?.id, activeDevice?.isGuest, connectionStatus]);

  const saveDogfoodConfig = async (patch: { repoDir?: string; prompt?: string }) => {
    const next = {
      repoDir: patch.repoDir ?? dogfoodRepoDir,
      prompt: patch.prompt ?? dogfoodPrompt,
    };
    await AsyncStorage.setItem(DOGFOOD_KEY, JSON.stringify(next));
  };

  const openDogfoodTask = (mode: "code" | "hermes") => {
    const dir = dogfoodRepoDir.trim();
    if (!dir) {
      Alert.alert("Dogfood Yaver", "Set the Yaver repository path first.");
      return;
    }
    if (connectionStatus !== "connected" || !activeDevice) {
      Alert.alert("Dogfood Yaver", "Connect a Yaver agent first.");
      return;
    }
    const prompt =
      mode === "code"
        ? [
            dogfoodPrompt.trim() || "Refresh Yaver using Yaver.",
            "Repository: Yaver.",
            "Use the connected Go agent as the execution backend.",
            "Keep changes compatible with the mobile app, the Go agent, and contributor dogfooding flows.",
            "If Hermes/mobile refresh is needed later, keep Metro/Hermes steps ready but do not start unnecessary native builds.",
          ].join("\n")
        : [
            "Refresh the Yaver mobile app inside Yaver using the connected Go agent.",
            `Work directory: ${dir}`,
            "Start the JS dev flow needed for Hermes/mobile refresh.",
            "Use Metro/dev-server style workflows only.",
            "Do not run expo run:ios, xcodebuild, gradlew, or other native build/install commands unless explicitly required.",
            "Prepare the app so it can be loaded back into the Yaver mobile shell.",
          ].join("\n");
    router.navigate({
      pathname: "/(tabs)/tasks" as any,
      params: {
        dir,
        prompt,
        runner: selectedRunner || undefined,
        title: mode === "code" ? "Dogfood Yaver" : "Dogfood Hermes",
        openNew: "1",
      },
    } as any);
  };

  const openDogfoodProject = () => {
    const dir = dogfoodRepoDir.trim();
    if (!dir) {
      Alert.alert("Dogfood Yaver", "Set the Yaver repository path first.");
      return;
    }
    router.navigate({ pathname: "/(tabs)/project", params: { dir } } as any);
  };

  const saveCustomRelays = async (relays: RelayServer[]) => {
    setCustomRelays(relays);
    await AsyncStorage.setItem(RELAYS_KEY, JSON.stringify(relays));
    if (relays.length > 0) {
      quicClient.setRelayServers(relays);
    }
    // Sync primary relay to Convex user settings only if cloud sync is enabled
    const syncEnabled = await AsyncStorage.getItem(SYNC_KEY);
    if (token && syncEnabled === "true") {
      const primary = relays.length > 0 ? relays[0] : null;
      saveUserSettings(token, {
        relayUrl: primary?.httpUrl ?? "",
      });
    }
  };

  const handleToggleRelaySync = async (enabled: boolean) => {
    setRelaySyncEnabled(enabled);
    await AsyncStorage.setItem(SYNC_KEY, enabled ? "true" : "false");
    if (enabled && token) {
      const primary = customRelays.length > 0 ? customRelays[0] : null;
      const primaryTunnel = customTunnels.length > 0 ? customTunnels[0] : null;
      saveUserSettings(token, {
        relayUrl: primary?.httpUrl ?? "",
        tunnelUrl: primaryTunnel?.url ?? "",
      });
    } else if (!enabled && token) {
      saveUserSettings(token, { relayUrl: "", tunnelUrl: "" });
    }
  };

  const handleAddRelay = async () => {
    const url = newRelayUrl.trim().replace(/\/+$/, "");
    if (!url) {
      Alert.alert("Relay URL Required", "Enter the relay server's URL before adding it.");
      return;
    }

    // Generate ID from URL hash
    let h = 0;
    for (let i = 0; i < url.length; i++) {
      h = ((h * 31) + url.charCodeAt(i)) >>> 0;
    }
    const id = h.toString(16).slice(0, 8);

    // Check duplicate
    if (customRelays.some((r) => r.httpUrl === url)) {
      Alert.alert("Already Added", "This relay server is already configured.");
      return;
    }

    // Infer QUIC address
    let host = url.replace(/^https?:\/\//, "").replace(/:\d+$/, "").replace(/\/.*$/, "");
    const quicAddr = host + ":4433";

    const relay: RelayServer = {
      id,
      quicAddr,
      httpUrl: url,
      region: newRelayLabel.trim() || "custom",
      priority: customRelays.length + 1,
      password: newRelayPassword.trim() || undefined,
    };

    await saveCustomRelays([...customRelays, relay]);
    setNewRelayUrl("");
    setNewRelayPassword("");
    setNewRelayLabel("");
    setShowAddRelay(false);
  };

  const handleRemoveRelay = (relayId: string) => {
    Alert.alert("Remove Relay", "Remove this relay server?", [
      { text: "Cancel", style: "cancel" },
      {
        text: "Remove",
        style: "destructive",
        onPress: () => saveCustomRelays(customRelays.filter((r) => r.id !== relayId)),
      },
    ]);
  };

  const handleTestRelay = async (relay: RelayServer) => {
    setTestingRelayId(relay.id);
    try {
      const start = Date.now();
      const res = await fetch(relay.httpUrl + "/health", { method: "GET" });
      const ms = Date.now() - start;
      if (res.ok) {
        setRelayTestResults((prev) => ({ ...prev, [relay.id]: { ok: true, ms } }));
      } else {
        setRelayTestResults((prev) => ({ ...prev, [relay.id]: { ok: false, error: `HTTP ${res.status}` } }));
      }
    } catch (e) {
      setRelayTestResults((prev) => ({ ...prev, [relay.id]: { ok: false, error: String(e) } }));
    } finally {
      setTestingRelayId(null);
    }
  };

  const saveCustomTunnels = async (tunnels: TunnelServer[]) => {
    setCustomTunnels(tunnels);
    await AsyncStorage.setItem(TUNNELS_KEY, JSON.stringify(tunnels));
    if (tunnels.length > 0) {
      quicClient.setTunnelServers(tunnels);
    }
  };

  const handleAddTunnel = async () => {
    const url = newTunnelUrl.trim().replace(/\/+$/, "");
    if (!url) {
      Alert.alert("Tunnel URL Required", "Enter the tunnel's URL before adding it.");
      return;
    }
    let h = 0;
    for (let i = 0; i < url.length; i++) {
      h = ((h * 31) + url.charCodeAt(i)) >>> 0;
    }
    const id = h.toString(16).slice(0, 8);
    if (customTunnels.some((t) => t.url === url)) {
      Alert.alert("Already Added", "This tunnel is already configured.");
      return;
    }
    const tunnel: TunnelServer = {
      id,
      url,
      cfAccessClientId: newTunnelCfClientId.trim() || undefined,
      cfAccessClientSecret: newTunnelCfClientSecret.trim() || undefined,
      label: newTunnelLabel.trim() || undefined,
      priority: customTunnels.length + 1,
    };
    await saveCustomTunnels([...customTunnels, tunnel]);
    setNewTunnelUrl("");
    setNewTunnelCfClientId("");
    setNewTunnelCfClientSecret("");
    setNewTunnelLabel("");
    setShowAddTunnel(false);
  };

  const handleRemoveTunnel = (tunnelId: string) => {
    Alert.alert("Remove Endpoint", "Remove this advanced HTTPS endpoint?", [
      { text: "Cancel", style: "cancel" },
      {
        text: "Remove",
        style: "destructive",
        onPress: () => saveCustomTunnels(customTunnels.filter((t) => t.id !== tunnelId)),
      },
    ]);
  };

  const handleTestTunnel = async (tunnel: TunnelServer) => {
    setTestingTunnelId(tunnel.id);
    try {
      const start = Date.now();
      const headers: Record<string, string> = {};
      if (tunnel.cfAccessClientId) {
        headers['CF-Access-Client-Id'] = tunnel.cfAccessClientId;
        headers['CF-Access-Client-Secret'] = tunnel.cfAccessClientSecret || '';
      }
      const res = await fetch(tunnel.url + "/health", { method: "GET", headers });
      const ms = Date.now() - start;
      if (res.ok) {
        setTunnelTestResults((prev) => ({ ...prev, [tunnel.id]: { ok: true, ms } }));
      } else {
        setTunnelTestResults((prev) => ({ ...prev, [tunnel.id]: { ok: false, error: `HTTP ${res.status}` } }));
      }
    } catch (e) {
      setTunnelTestResults((prev) => ({ ...prev, [tunnel.id]: { ok: false, error: String(e) } }));
    } finally {
      setTestingTunnelId(null);
    }
  };

  // Load user settings, runners, and usage from Convex + local secrets
  useEffect(() => {
    if (!token) return;
    // Load key storage preference (cloud setting wins over local)
    getUserSettings(token).then(async (s) => {
      if (s.keyStorage === "cloud" || s.keyStorage === "local") {
        setKeyStorage(s.keyStorage);
        await saveKeyStoragePreference(s.keyStorage);
      } else {
        const localPref = await getKeyStoragePreference();
        setKeyStorage(localPref);
      }
      if (s.forceRelay !== undefined) {
        setForceRelay(s.forceRelay);
        quicClient.setForceRelay(s.forceRelay);
      }
      if (s.runnerId) {
        // Normalize legacy "claude" → "claude-code" so the picker radio
        // matches a row instead of leaving every option unselected for
        // users whose userSettings was saved before the rename.
        setSelectedRunner(s.runnerId === "claude" ? "claude-code" : s.runnerId);
      }
      if (s.ttsEnabled !== undefined) setTtsEnabled(s.ttsEnabled);
      if (s.ttsTaskMode !== undefined) { setTtsTaskMode(s.ttsTaskMode); quicClient.setTtsTaskMode(s.ttsTaskMode); }
      if (s.verbosity !== undefined) setVerbosity(s.verbosity);
      // Speech config is LOCAL ONLY (provider / key / model / voice in
      // SecureStore, never Convex). loadLocalSpeechConfig is the single
      // source of truth — we deliberately ignore s.speechProvider /
      // s.speechApiKey / s.ttsProvider from the cloud settings.
      const sc = await loadLocalSpeechConfig();
      if (sc.sttProvider) setSpeechProvider(sc.sttProvider);
      if (sc.sttModel) setSttModel(sc.sttModel);
      if (sc.ttsProvider) setTtsProvider(sc.ttsProvider);
      if (sc.ttsModel) setTtsModel(sc.ttsModel);
      if (sc.ttsVoice) setTtsVoice(sc.ttsVoice);
      if (sc.apiKey) setSpeechApiKey(sc.apiKey);
      const localOpenAi = await getLocalSecret(LOCAL_KEYS.openAiApiKey);
      if (localOpenAi) {
        setOpenAiApiKey(localOpenAi);
        if (!sc.apiKey && (sc.sttProvider === "openai" || sc.ttsProvider === "openai")) setSpeechApiKey(localOpenAi);
      } else if (typeof s.openAiApiKey === "string") {
        setOpenAiApiKey(s.openAiApiKey);
      }
      const localGlm = await getLocalSecret(LOCAL_KEYS.glmApiKey);
      if (localGlm) setGlmApiKey(localGlm);
      else if (typeof s.glmApiKey === "string") setGlmApiKey(s.glmApiKey);
      const localAnthropic = await getLocalSecret(LOCAL_KEYS.anthropicApiKey);
      if (localAnthropic) setAnthropicApiKey(localAnthropic);
      else if (typeof s.anthropicApiKey === "string") setAnthropicApiKey(s.anthropicApiKey);
      const localMobileProvider = await getLocalSecret(LOCAL_KEYS.mobileCodingProvider);
      if (localMobileProvider === "glm" || localMobileProvider === "openai") {
        setMobileCodingProvider(localMobileProvider);
      } else if (s.mobileCodingProvider === "glm" || s.mobileCodingProvider === "openai") {
        setMobileCodingProvider(s.mobileCodingProvider);
      }
      setMoreOptionalTools(normalizeOptionalMoreTools(s.moreOptionalTools));
    }).catch(() => {
      // Settings unreadable (offline / expired session) — leave the form on
      // its current values instead of resetting it to defaults.
    });
    getUsageSummary(token).then(setUsageSummary);
  }, [token]);

  const toggleMoreOptionalTool = async (id: OptionalMoreToolId, enabled: boolean) => {
    const next = enabled
      ? normalizeOptionalMoreTools([...moreOptionalTools, id])
      : moreOptionalTools.filter((toolId) => toolId !== id);
    setMoreOptionalTools(next);
    if (token) {
      await saveUserSettings(token, { moreOptionalTools: next });
    }
  };

  // Subscribe to live log updates
  useEffect(() => {
    return onLogsChanged(() => setLogs(getLogEntries()));
  }, []);

  // Ping the agent for version when connected
  useEffect(() => {
    if (connectionStatus !== "connected" || !activeDevice) {
      setAgentVersion(null);
      setAgentLastPing(null);
      setAgentStatus(null);
      setSandboxStatus(null);
      return;
    }
    (async () => {
      try {
        const [info, status, sandbox] = await Promise.all([
          quicClient.getInfo(),
          quicClient.getAgentStatus(),
          quicClient.getSandboxStatus(),
        ]);
        if (info) {
          setAgentVersion(info.version || null);
          setAgentLastPing(new Date());
        }
        if (status) setAgentStatus(status);
        if (sandbox) setSandboxStatus(sandbox);
      } catch {
        // Agent unreachable — leave as null
      }
    })();
  }, [connectionStatus, activeDevice]);

  // Ping agent every 10s when connected
  useEffect(() => {
    if (connectionStatus !== "connected") {
      setPingRtt(null);
      return;
    }
    const doPing = async () => {
      const result = await quicClient.ping();
      if (result.ok) setPingRtt(result.rttMs);
    };
    doPing();
    const interval = setInterval(doPing, 10000);
    return () => clearInterval(interval);
  }, [connectionStatus]);

  const handlePing = async () => {
    setIsPinging(true);
    const result = await quicClient.ping();
    setPingRtt(result.ok ? result.rttMs : null);
    setIsPinging(false);
  };

  const handleShutdownAgent = () => {
    Alert.alert(
      "Shutdown Agent",
      "This will stop the Yaver agent on your desktop. You won't be able to send tasks until it's restarted.",
      [
        { text: "Cancel", style: "cancel" },
        {
          text: "Shutdown",
          style: "destructive",
          onPress: async () => {
            setIsShuttingDown(true);
            const ok = await quicClient.shutdownAgent();
            setIsShuttingDown(false);
            if (ok) {
              disconnect();
              Alert.alert("Done", "Agent has been shut down.");
            } else {
              Alert.alert("Couldn't Shut Down Agent", "Yaver couldn't reach the agent to shut it down. Check your connection and try again.");
            }
          },
        },
      ]
    );
  };

  const saveAiProviderSettings = async () => {
    if (!token) return;
    setIsSavingAiProviders(true);
    try {
      const cloudSettings: Record<string, any> = {
        mobileCodingProvider,
      };
      const secrets: Array<[string, string]> = [
        [LOCAL_KEYS.openAiApiKey, openAiApiKey],
        [LOCAL_KEYS.glmApiKey, glmApiKey],
        [LOCAL_KEYS.anthropicApiKey, anthropicApiKey],
        [LOCAL_KEYS.mobileCodingProvider, mobileCodingProvider],
      ];
      if (keyStorage === "cloud") {
        cloudSettings.openAiApiKey = openAiApiKey || "";
        cloudSettings.glmApiKey = glmApiKey || "";
        cloudSettings.anthropicApiKey = anthropicApiKey || "";
        cloudSettings.mobileCodingProvider = mobileCodingProvider;
        for (const [key] of secrets) {
          await deleteLocalSecret(key);
        }
      } else {
        for (const [key, value] of secrets) {
          if (value) await saveLocalSecret(key, value);
          else await deleteLocalSecret(key);
        }
        cloudSettings.openAiApiKey = "";
        cloudSettings.glmApiKey = "";
        cloudSettings.anthropicApiKey = "";
        cloudSettings.mobileCodingProvider = "";
      }
      await saveUserSettings(token, cloudSettings);
      const now = Date.now();
      const statuses: ProviderKeyState[] = [];
      if (openAiApiKey.trim()) {
        statuses.push({ provider: "openai", scope: "phone-local", storage: keyStorage, status: "saved", updatedAt: now, lastAttemptAt: now, lastSuccessAt: now });
      }
      if (glmApiKey.trim()) {
        statuses.push({ provider: "glm", scope: "phone-local", storage: keyStorage, status: "saved", updatedAt: now, lastAttemptAt: now, lastSuccessAt: now });
      }
      if (anthropicApiKey.trim()) {
        statuses.push({ provider: "anthropic", scope: "phone-local", storage: keyStorage, status: "saved", updatedAt: now, lastAttemptAt: now, lastSuccessAt: now });
      }
      if (statuses.length > 0) {
        await mergeProviderStates(statuses, true);
      }
      Alert.alert("Saved", "AI provider settings saved.");
    } catch {
      Alert.alert("Couldn't Save Settings", "Yaver couldn't save your AI provider settings on this device. Try again.");
    } finally {
      setIsSavingAiProviders(false);
    }
  };

  const syncAiProvidersToVault = async () => {
    if (connectionStatus !== "connected") {
      Alert.alert("Connect a device", "Connect to your own Yaver machine to sync provider keys into its vault.");
      return;
    }
    if (activeDevice?.isGuest) {
      Alert.alert("Guest machine", "Vault sync is disabled on guest connections. Connect to your own host machine instead.");
      return;
    }
    setIsSyncingAiVault(true);
    try {
      const entries = [
        { provider: "openai" as const, name: "OPENAI_API_KEY", value: openAiApiKey, notes: "Owner-only AI provider key synced from Yaver mobile." },
        { provider: "glm" as const, name: "GLM_API_KEY", value: glmApiKey, notes: "Owner-only AI provider key synced from Yaver mobile." },
        { provider: "anthropic" as const, name: "ANTHROPIC_API_KEY", value: anthropicApiKey, notes: "Owner-only AI provider key synced from Yaver mobile." },
      ];
      let changed = 0;
      const now = Date.now();
      const statuses: ProviderKeyState[] = [];
      for (const entry of entries) {
        if (!entry.value.trim()) continue;
        try {
          await quicClient.vaultSet({
            name: entry.name,
            category: "api-key",
            value: entry.value.trim(),
            notes: entry.notes,
          });
          statuses.push({
            provider: entry.provider,
            scope: "host-vault",
            storage: keyStorage,
            status: "synced",
            updatedAt: now,
            lastAttemptAt: now,
            lastSuccessAt: now,
          });
          changed += 1;
        } catch (error) {
          const message = error instanceof Error ? error.message : "Sync failed";
          statuses.push({
            provider: entry.provider,
            scope: "host-vault",
            storage: keyStorage,
            status: "failed",
            updatedAt: now,
            lastAttemptAt: now,
            lastFailureAt: now,
            lastError: message,
          });
        }
      }
      if (statuses.length > 0) {
        await mergeProviderStates(statuses, true);
      }
      Alert.alert("Vault synced", changed > 0 ? `Synced ${changed} provider key${changed === 1 ? "" : "s"} to the connected machine vault.` : "No non-empty provider keys to sync.");
    } catch {
      Alert.alert("Vault Sync Failed", "Yaver couldn't sync your provider keys to the connected machine's vault. Check your connection and try again.");
    } finally {
      setIsSyncingAiVault(false);
    }
  };

  const loadRunnerAuthStatus = async () => {
    if (connectionStatus !== "connected" || activeDevice?.isGuest) {
      setRunnerAuthRows([]);
      setRunnerCapabilitySnapshot(null);
      setRunnerIncidents([]);
      setMachineOnboardingRows([]);
      setMachineOnboardingRowsByDevice({});
      return;
    }
    const [rows, snapshot, incidents] = await Promise.all([
      quicClient.runnerAuthStatus(),
      quicClient.capabilitySnapshot(),
      quicClient.incidents({ category: "runner_auth", limit: 8 }),
    ]);
    setRunnerAuthRows(rows);
    setRunnerCapabilitySnapshot(snapshot);
    setRunnerIncidents(incidents);
    await loadMachineOnboardingStatusForTargets();
  };

  const onboardingTargetCandidates = devices.filter(
    (device) => !device.isGuest && (device.online || device.id === activeDevice?.id),
  );
  const selectedOnboardingTargets = onboardingTargetCandidates.filter((device) => selectedOnboardingTargetIds.includes(device.id));
  const resolveOnboardingTargetArg = (deviceId: string) =>
    activeDevice?.id === deviceId ? undefined : deviceId;
  const loadMachineOnboardingStatusForTargets = async (deviceIds: string[] = selectedOnboardingTargetIds) => {
    if (connectionStatus !== "connected" || activeDevice?.isGuest) {
      setMachineOnboardingRows([]);
      setMachineOnboardingRowsByDevice({});
      return;
    }
    const uniqueIds = Array.from(new Set(deviceIds.filter(Boolean)));
    if (uniqueIds.length === 0) {
      setMachineOnboardingRows([]);
      setMachineOnboardingRowsByDevice({});
      return;
    }
    const entries = await Promise.all(
      uniqueIds.map(async (deviceId) => {
        const rows = await quicClient.machineOnboardingStatus(resolveOnboardingTargetArg(deviceId));
        return [deviceId, rows] as const;
      }),
    );
    const next: Record<string, MachineOnboardingProviderStatus[]> = {};
    entries.forEach(([deviceId, rows]) => {
      next[deviceId] = rows;
    });
    setMachineOnboardingRowsByDevice(next);
    setMachineOnboardingRows(next[uniqueIds[0]] || []);
  };

  const startGitOAuthOnboarding = async (provider: "github" | "gitlab") => {
    if (connectionStatus !== "connected") {
      Alert.alert("Connect a device", "Connect to your own Yaver machine to authorize GitHub or GitLab.");
      return;
    }
    if (activeDevice?.isGuest) {
      Alert.alert("Guest machine", "Git authorization is disabled on guest connections. Connect to your own host machine instead.");
      return;
    }
    if (selectedOnboardingTargets.length !== 1) {
      Alert.alert("Pick one machine", "Choose exactly one owned live machine to authorize. You can repeat this for another runtime afterwards.");
      return;
    }
    const device = selectedOnboardingTargets[0];
    const target = resolveOnboardingTargetArg(device.id);
    setStartingGitOAuthProvider(provider);
    try {
      const start = await quicClient.gitOAuthStart(
        provider,
        target,
        provider === "gitlab" ? (gitlabHost.trim() || "gitlab.com") : undefined,
      );
      if (!start.ok || !start.sessionId || !start.userCode || !start.verificationUri) {
        Alert.alert(
          "Couldn't Start Authorization",
          start.error || "The runtime could not start provider authorization. The Yaver OAuth app client ID may not be configured on that runtime yet.",
        );
        return;
      }
      const flow: GitOAuthStatus & { deviceId: string; deviceName: string } = {
        ...start,
        state: "pending",
        deviceId: device.id,
        deviceName: device.name,
      };
      setGitOAuthFlow(flow);
      await ExpoClipboard.setStringAsync(start.userCode).catch(() => {});
      await Linking.openURL(start.verificationUri).catch(() => {});
      Alert.alert("Authorize Git", `Code ${start.userCode} was copied. Approve ${provider} in the browser, then return to Yaver.`);
    } catch (error) {
      const message = error instanceof Error ? error.message : "Failed to start provider authorization.";
      Alert.alert("Authorization Failed", `Yaver couldn't start Git authorization on ${device.name}.\n\n${message}`);
    } finally {
      setStartingGitOAuthProvider(null);
    }
  };

  useEffect(() => {
    if (!gitOAuthFlow || gitOAuthFlow.state !== "pending") return;
    let cancelled = false;
    const intervalMs = Math.max(2, gitOAuthFlow.interval || 5) * 1000;
    const timer = setInterval(async () => {
      if (cancelled) return;
      try {
        const target = activeDevice?.id === gitOAuthFlow.deviceId ? undefined : gitOAuthFlow.deviceId;
        const status = await quicClient.gitOAuthStatus(gitOAuthFlow.sessionId, gitOAuthFlow.provider, target);
        if (cancelled || status.state === "pending") return;
        setGitOAuthFlow((prev) => (
          prev && prev.sessionId === gitOAuthFlow.sessionId
            ? { ...prev, ...status, deviceId: prev.deviceId, deviceName: prev.deviceName }
            : prev
        ));
        if (status.state === "done") {
          await loadMachineOnboardingStatusForTargets([gitOAuthFlow.deviceId]);
          Alert.alert("Git Authorized", `${gitOAuthFlow.provider} is ready on ${gitOAuthFlow.deviceName}${status.username ? ` as ${status.username}` : ""}.`);
        } else {
          Alert.alert("Authorization Didn't Complete", status.error || "The provider authorization expired or was denied. Start it again when you're ready.");
        }
      } catch (error) {
        if (!cancelled) {
          const message = error instanceof Error ? error.message : "Failed to check authorization status.";
          setGitOAuthFlow((prev) => (
            prev && prev.sessionId === gitOAuthFlow.sessionId
              ? { ...prev, state: "error", error: message }
              : prev
          ));
        }
      }
    }, intervalMs);
    return () => {
      cancelled = true;
      clearInterval(timer);
    };
  }, [activeDevice?.id, gitOAuthFlow?.sessionId, gitOAuthFlow?.state, gitOAuthFlow?.interval, gitOAuthFlow?.deviceId, gitOAuthFlow?.provider]);

  const syncAiProvidersToRunners = async () => {
    if (connectionStatus !== "connected") {
      Alert.alert("Connect a device", "Connect to your own Yaver machine to configure runner auth.");
      return;
    }
    if (activeDevice?.isGuest) {
      Alert.alert("Guest machine", "Runner auth is disabled on guest connections. Connect to your own host machine instead.");
      return;
    }
    const jobs = [
      {
        runner: "codex" as const,
        payload: {
          runner: "codex" as const,
          openaiApiKey: openAiApiKey.trim(),
          notes: "Runner auth synced from Yaver mobile.",
        },
      },
      {
        runner: "claude" as const,
        payload: {
          runner: "claude" as const,
          anthropicApiKey: anthropicApiKey.trim(),
          notes: "Runner auth synced from Yaver mobile.",
        },
      },
      {
        runner: "opencode" as const,
        payload: {
          runner: "opencode" as const,
          openaiApiKey: openAiApiKey.trim(),
          anthropicApiKey: anthropicApiKey.trim(),
          glmApiKey: glmApiKey.trim(),
          notes: "Runner auth synced from Yaver mobile.",
        },
      },
    ].filter((job) =>
      Object.entries(job.payload).some(([key, value]) => key !== "runner" && key !== "notes" && typeof value === "string" && value.trim().length > 0),
    );
    if (jobs.length === 0) {
      Alert.alert("No keys", "Add at least one provider key first.");
      return;
    }
    setIsSyncingRunnerAuth(true);
    try {
      const savedRunners: string[] = [];
      for (const job of jobs) {
        const res = await quicClient.runnerAuthSet(job.payload);
        if (!res.ok) {
          throw new Error(res.error || `Failed to configure ${job.runner}`);
        }
        savedRunners.push(job.runner);
      }
      await loadRunnerAuthStatus();
      Alert.alert("Runner auth synced", `Updated ${savedRunners.join(", ")} on the connected machine.`);
    } catch (error) {
      const message = error instanceof Error ? error.message : "Failed to sync runner auth.";
      Alert.alert("Runner Auth Sync Failed", `Yaver couldn't sync runner auth to the connected machine. Check your connection and try again.\n\n${message}`);
    } finally {
      setIsSyncingRunnerAuth(false);
    }
  };

  const applyMachineOnboarding = async () => {
    if (connectionStatus !== "connected") {
      Alert.alert("Connect a device", "Connect to your own Yaver machine to configure OpenAI, GitHub, and GitLab.");
      return;
    }
    if (activeDevice?.isGuest) {
      Alert.alert("Guest machine", "Machine onboarding is disabled on guest connections. Connect to your own host machine instead.");
      return;
    }
    if (!openAiApiKey.trim() && !githubToken.trim() && !gitlabToken.trim()) {
      Alert.alert("No credentials", "Add at least one OpenAI, GitHub, or GitLab credential first.");
      return;
    }
    if (selectedOnboardingTargets.length === 0) {
      Alert.alert("Select machines", "Pick at least one owned live machine first.");
      return;
    }
    setIsApplyingMachineOnboarding(true);
    try {
      const nextRows: Record<string, MachineOnboardingProviderStatus[]> = {};
      const updatedTargets: string[] = [];
      const failures: string[] = [];
      for (const device of selectedOnboardingTargets) {
        const res = await quicClient.machineOnboardingApply({
          openaiApiKey: openAiApiKey.trim(),
          githubToken: githubToken.trim(),
          gitlabToken: gitlabToken.trim(),
          gitlabHost: gitlabHost.trim() || "gitlab.com",
          applyClone: true,
          applyCiToken: true,
          notes: "Saved from Yaver mobile settings.",
        }, resolveOnboardingTargetArg(device.id));
        if (!res.ok) {
          failures.push(`${device.name}: ${res.error || "Failed to apply machine onboarding"}`);
          continue;
        }
        nextRows[device.id] = res.providers;
        if (res.applied.length > 0) {
          updatedTargets.push(`${device.name} (${res.applied.join(", ")})`);
        } else {
          updatedTargets.push(`${device.name} (no changes)`);
        }
      }
      setMachineOnboardingRowsByDevice((prev) => ({ ...prev, ...nextRows }));
      if (selectedOnboardingTargets[0]) {
        setMachineOnboardingRows(nextRows[selectedOnboardingTargets[0].id] || []);
      }
      if (failures.length > 0 && updatedTargets.length === 0) {
        throw new Error(failures.join("\n"));
      }
      Alert.alert(
        failures.length > 0 ? "Applied with warnings" : "Applied",
        [
          updatedTargets.length > 0 ? `Updated: ${updatedTargets.join(", ")}` : "",
          failures.length > 0 ? `Failed: ${failures.join(" | ")}` : "",
        ].filter(Boolean).join("\n"),
      );
    } catch (error) {
      const message = error instanceof Error ? error.message : "Failed to apply machine onboarding.";
      Alert.alert("Onboarding Didn't Apply", `Yaver couldn't apply machine onboarding. Check your connection and try again.\n\n${message}`);
    } finally {
      setIsApplyingMachineOnboarding(false);
    }
  };

  const removeMachineOnboarding = async (provider: "github" | "gitlab") => {
    if (connectionStatus !== "connected") {
      Alert.alert("Connect a device", "Connect to your own Yaver machine to remove GitHub or GitLab credentials.");
      return;
    }
    if (activeDevice?.isGuest) {
      Alert.alert("Guest machine", "Machine onboarding removal is disabled on guest connections. Connect to your own host machine instead.");
      return;
    }
    if (selectedOnboardingTargets.length === 0) {
      Alert.alert("Select machines", "Pick at least one owned live machine first.");
      return;
    }
    setRemovingOnboardingProvider(provider);
    try {
      const nextRows: Record<string, MachineOnboardingProviderStatus[]> = {};
      const removedTargets: string[] = [];
      const failures: string[] = [];
      for (const device of selectedOnboardingTargets) {
        const res = await quicClient.machineOnboardingRemove({
          providers: [provider],
          gitlabHost: provider === "gitlab" ? (gitlabHost.trim() || "gitlab.com") : undefined,
          removeClone: true,
          removeCiToken: true,
        }, resolveOnboardingTargetArg(device.id));
        if (!res.ok) {
          failures.push(`${device.name}: ${res.error || "Failed to remove machine onboarding"}`);
          continue;
        }
        nextRows[device.id] = res.providers;
        removedTargets.push(`${device.name} (${res.removed.length > 0 ? res.removed.join(", ") : "nothing removed"})`);
      }
      setMachineOnboardingRowsByDevice((prev) => ({ ...prev, ...nextRows }));
      if (selectedOnboardingTargets[0]) {
        setMachineOnboardingRows(nextRows[selectedOnboardingTargets[0].id] || []);
      }
      if (failures.length > 0 && removedTargets.length === 0) {
        throw new Error(failures.join("\n"));
      }
      Alert.alert(
        failures.length > 0 ? "Removed with warnings" : "Removed",
        [
          removedTargets.length > 0 ? `Updated: ${removedTargets.join(", ")}` : "",
          failures.length > 0 ? `Failed: ${failures.join(" | ")}` : "",
        ].filter(Boolean).join("\n"),
      );
    } catch (error) {
      const message = error instanceof Error ? error.message : "Failed to remove machine onboarding.";
      Alert.alert("Couldn't Remove Onboarding", `Yaver couldn't remove machine onboarding. Check your connection and try again.\n\n${message}`);
    } finally {
      setRemovingOnboardingProvider(null);
    }
  };

  const toolchainSourceCandidates = devices.filter(
    (device) => !device.isGuest && device.id !== activeDevice?.id,
  );
  const selectedToolchainSource =
    toolchainSourceCandidates.find((device) => device.id === toolchainSourceId) ?? toolchainSourceCandidates[0] ?? null;

  useEffect(() => {
    if (!selectedToolchainSource) {
      setToolchainSourceId(null);
      return;
    }
    if (toolchainSourceId !== selectedToolchainSource.id) {
      setToolchainSourceId(selectedToolchainSource.id);
    }
  }, [selectedToolchainSource?.id]);

  useEffect(() => {
    const allowedIds = new Set(onboardingTargetCandidates.map((device) => device.id));
    setSelectedOnboardingTargetIds((current) => {
      const filtered = current.filter((id) => allowedIds.has(id));
      if (filtered.length > 0) return filtered;
      if (activeDevice && !activeDevice.isGuest && allowedIds.has(activeDevice.id)) return [activeDevice.id];
      const first = onboardingTargetCandidates[0];
      return first ? [first.id] : [];
    });
  }, [activeDevice?.id, activeDevice?.isGuest, onboardingTargetCandidates.map((device) => device.id).join("|")]);

  useEffect(() => {
    if (!showIntegrations) return;
    void loadRunnerAuthStatus();
  }, [showIntegrations, connectionStatus, activeDevice?.id, activeDevice?.isGuest, selectedOnboardingTargetIds.join("|")]);

  const currentToolchainSyncKinds = () => {
    const kinds: string[] = [];
    if (toolchainSyncProviderKeys) kinds.push("provider-keys");
    if (toolchainSyncPresets) kinds.push("presets");
    if (toolchainSyncFlags) kinds.push("flags");
    if (toolchainSyncEnv) kinds.push("env");
    if (toolchainSyncMonitors) kinds.push("monitors");
    return kinds;
  };

  const summarizeToolchainSync = (result: EnvironmentProfileApplyResult) => {
    const lines: string[] = [];
    if ((result.installPlan?.length ?? 0) > 0) {
      lines.push(`Will install: ${result.installPlan!.slice(0, 6).join(", ")}${result.installPlan!.length > 6 ? "…" : ""}`);
    }
    if ((result.installed?.length ?? 0) > 0) {
      lines.push(`Installed: ${result.installed!.slice(0, 6).join(", ")}${result.installed!.length > 6 ? "…" : ""}`);
    }
    if ((result.importedSyncKinds?.length ?? 0) > 0) {
      lines.push(`Synced settings: ${result.importedSyncKinds!.join(", ")}`);
    }
    if ((result.importedGitHosts?.length ?? 0) > 0) {
      lines.push(`Git hosts: ${result.importedGitHosts!.join(", ")}`);
    }
    if ((result.removedGitHosts?.length ?? 0) > 0) {
      lines.push(`Removed Git hosts: ${result.removedGitHosts!.join(", ")}`);
    }
    if ((result.removalPlan?.length ?? 0) > 0) {
      lines.push(`Manual cleanup suggested: ${result.removalPlan!.slice(0, 6).join(", ")}${result.removalPlan!.length > 6 ? "…" : ""}`);
    }
    if ((result.manualSteps?.length ?? 0) > 0) {
      lines.push(`Manual steps: ${result.manualSteps!.slice(0, 2).join(" · ")}`);
    }
    if ((result.notes?.length ?? 0) > 0) {
      lines.push(result.notes![0]);
    }
    return lines;
  };

  const previewToolchainSync = async () => {
    if (connectionStatus !== "connected" || !activeDevice) {
      Alert.alert("Connect a machine", "Connect to the target machine you want to update first.");
      return;
    }
    if (activeDevice.isGuest) {
      Alert.alert("Owner only", "Toolchain Sync is only available on your own machines.");
      return;
    }
    if (!selectedToolchainSource) {
      Alert.alert("No source machine", "Bring another one of your Yaver machines online so it can act as the source toolchain.");
      return;
    }
    if (!selectedToolchainSource.online) {
      Alert.alert("Source offline", "The source machine must be online so the target can read its toolchain profile.");
      return;
    }
    setIsPreviewingToolchainSync(true);
    try {
      const preview = await quicClient.applyToolchainSync({
        sourceDeviceId: selectedToolchainSource.id,
        installMissing: toolchainInstallMissing,
        syncKinds: currentToolchainSyncKinds(),
        includeGitCredentials: toolchainSyncGitCredentials,
        removeMissing: toolchainRemoveMissing,
        dryRun: true,
      });
      setToolchainPreview(preview);
      const summary = summarizeToolchainSync(preview);
      Alert.alert(
        "Toolchain Sync Preview",
        summary.length > 0
          ? summary.join("\n")
          : "No changes needed. The target already matches the selected source toolchain.",
      );
    } catch (error) {
      Alert.alert("Preview failed", error instanceof Error ? error.message : "Toolchain Sync preview failed.");
    } finally {
      setIsPreviewingToolchainSync(false);
    }
  };

  const applyToolchainSyncNow = async () => {
    if (connectionStatus !== "connected" || !activeDevice) {
      Alert.alert("Connect a machine", "Connect to the target machine you want to update first.");
      return;
    }
    if (activeDevice.isGuest) {
      Alert.alert("Owner only", "Toolchain Sync is only available on your own machines.");
      return;
    }
    if (!selectedToolchainSource) {
      Alert.alert("No source machine", "Bring another one of your Yaver machines online so it can act as the source toolchain.");
      return;
    }
    if (!selectedToolchainSource.online) {
      Alert.alert("Source offline", "The source machine must be online so the target can read its toolchain profile.");
      return;
    }
    setIsApplyingToolchainSync(true);
    try {
      const result = await quicClient.applyToolchainSync({
        sourceDeviceId: selectedToolchainSource.id,
        installMissing: toolchainInstallMissing,
        syncKinds: currentToolchainSyncKinds(),
        includeGitCredentials: toolchainSyncGitCredentials,
        removeMissing: toolchainRemoveMissing,
        dryRun: false,
      });
      setToolchainPreview(result);
      const summary = summarizeToolchainSync(result);
      Alert.alert(
        result.status === "partial" ? "Toolchain Sync Finished With Notes" : "Toolchain Sync Finished",
        summary.length > 0 ? summary.join("\n") : "The target machine already matched the selected source toolchain.",
      );
    } catch (error) {
      Alert.alert("Sync failed", error instanceof Error ? error.message : "Toolchain Sync failed.");
    } finally {
      setIsApplyingToolchainSync(false);
    }
  };

  // Fetch device metrics every 60s when connected
  useEffect(() => {
    if (!token || !activeDevice || connectionStatus !== "connected") {
      setMetrics([]);
      setEvents([]);
      return;
    }
    const fetchMetrics = async () => {
      const [m, e] = await Promise.all([
        getDeviceMetrics(token, activeDevice.id),
        getDeviceEvents(token, activeDevice.id),
      ]);
      setMetrics(m);
      setEvents(e);
    };
    fetchMetrics();
    const interval = setInterval(fetchMetrics, 60000);
    return () => clearInterval(interval);
  }, [token, activeDevice, connectionStatus]);


  const handleSaveName = async () => {
    if (!token || !editName.trim()) return;
    setIsSavingName(true);
    try {
      await updateProfile(token, { fullName: editName.trim() });
      await refreshUser();
      setIsEditingName(false);
    } catch {
      Alert.alert("Couldn't Update Name", "Yaver couldn't save your name. Check your connection and try again.");
    } finally {
      setIsSavingName(false);
    }
  };

  const handleSignOut = async () => {
    disconnect();
    await logout();
    router.replace("/login");
  };

  const handleClearCache = () => {
    Alert.alert(
      "Clear Task Cache",
      "This will remove all locally cached tasks and output. Data will be re-fetched from your device on next sync.",
      [
        { text: "Cancel", style: "cancel" },
        {
          text: "Clear",
          style: "destructive",
          onPress: async () => {
            setIsClearing(true);
            try {
              await clearCache();
              Alert.alert("Done", "Task cache has been cleared.");
            } catch {
              Alert.alert("Couldn't Clear Cache", "Yaver couldn't clear the local task cache. Try again.");
            } finally {
              setIsClearing(false);
            }
          },
        },
      ]
    );
  };

  const handleCleanAgent = () => {
    Alert.alert(
      "Clean Up Agent",
      "Remove completed tasks older than 30 days, their images, and old logs from your dev machine.",
      [
        { text: "Cancel", style: "cancel" },
        {
          text: "Clean Up",
          style: "destructive",
          onPress: async () => {
            setIsCleaning(true);
            try {
              const result = await quicClient.cleanAgent(30);
              Alert.alert("Done", `Removed ${result.tasksRemoved} tasks, freed ${(result.bytesFreed / 1024 / 1024).toFixed(1)} MB.`);
            } catch {
              Alert.alert("Cleanup Failed", "Yaver couldn't clean up your dev machine. Check your connection and try again.");
            } finally {
              setIsCleaning(false);
            }
          },
        },
      ]
    );
  };

  const handleDeleteAccount = async () => {
    if (deleteConfirm !== "delete my account") return;
    setDeletingAccount(true);
    const success = await deleteAccountApi();
    if (success) {
      disconnect();
      await logout();
      router.replace("/login");
    } else {
      Alert.alert("Couldn't Delete Account", "Yaver couldn't delete your account. Check your connection and try again.");
      setDeletingAccount(false);
    }
  };

  const handleAddPasskey = async () => {
    setPasskeyEnrollMessage(null);
    setEnrollingPasskey(true);
    try {
      const { getToken } = await import("../../src/lib/auth");
      const token = await getToken();
      if (!token) {
        setPasskeyEnrollMessage("Sign in first.");
        return;
      }
      const { passkeyEnroll, PasskeyCancelled, PasskeyError } = await import("../../src/lib/passkey");
      const { getConvexSiteUrl } = await import("../../src/lib/auth");
      try {
        await passkeyEnroll(getConvexSiteUrl(), token, "iPhone");
        setPasskeyEnrollMessage("✓ Passkey added. Next sign-in: just Face ID.");
      } catch (e: unknown) {
        if (e instanceof PasskeyCancelled) {
          // Silent — user dismissed the platform sheet.
        } else if (e instanceof PasskeyError) {
          setPasskeyEnrollMessage(e.message || "Could not add passkey.");
        } else {
          setPasskeyEnrollMessage(e instanceof Error ? e.message : "Could not add passkey.");
        }
      }
    } finally {
      setEnrollingPasskey(false);
    }
  };

  // Resend the verify-email link for the currently signed-in user.
  // Surfaced as an inline banner when user.emailVerified is false —
  // unlocks email-keyed OAuth auto-linking once the user clicks
  // through the email. Idempotent on the backend (re-uses an existing
  // unconsumed token rather than spamming the inbox).
  const handleRequestVerifyEmail = async () => {
    setVerifyEmailMessage(null);
    setVerifyEmailBusy(true);
    try {
      const { getToken, getConvexSiteUrl } = await import("../../src/lib/auth");
      const t = await getToken();
      if (!t) {
        setVerifyEmailMessage("Sign in first.");
        return;
      }
      const res = await fetch(`${getConvexSiteUrl()}/auth/verify-email/request`, {
        method: "POST",
        headers: { Authorization: `Bearer ${t}`, "Content-Type": "application/json" },
        body: "{}",
      });
      if (!res.ok) {
        setVerifyEmailMessage((await res.text()) || "Could not send verification email.");
        return;
      }
      const data = await res.json();
      if (data.alreadyVerified) {
        setVerifyEmailMessage("✓ This email is already verified.");
        await refreshUser();
      } else if (data.ok) {
        setVerifyEmailMessage("✓ Check your inbox for the verification link.");
      } else if (data.error === "NO_EMAIL_ON_ACCOUNT") {
        setVerifyEmailMessage("This account has no email on file.");
      } else {
        setVerifyEmailMessage(data.error || "Could not send verification email.");
      }
    } catch (e: unknown) {
      setVerifyEmailMessage(e instanceof Error ? e.message : "Network error.");
    } finally {
      setVerifyEmailBusy(false);
    }
  };

  const handleRemoveMachine = async () => {
    if (machineDeleteConfirm !== "delete my machine") return;
    if (connectionStatus !== "connected" || !activeDevice) {
      Alert.alert("Connect a machine", "Connect to your own machine first.");
      return;
    }
    if (activeDevice.isGuest) {
      Alert.alert("Owner only", "Guest/shared machines cannot be permanently removed from the host.");
      return;
    }
    setRemovingMachine(true);
    setMachineRemoveSteps([]);
    try {
      const res = await quicClient.machineRemove(machineDeleteConfirm);
      if (!res?.ok) {
        throw new Error(res?.error || "Failed to remove machine");
      }
      setMachineDeleteConfirm("");
      // Subscribe to the stream the agent returns so the user sees
      // each step land in real time. Old agents (pre-1.99.163) won't
      // include a stream — fall back to the old Alert behavior.
      const streamName: string | undefined = res?.stream;
      if (!streamName) {
        disconnect();
        setTimeout(() => { refreshDevices().catch(() => {}); }, 1500);
        const manualSteps = Array.isArray(res?.manualSteps) ? res.manualSteps.join("\n") : "";
        Alert.alert(
          "Machine removal started",
          manualSteps
            ? `Yaver is being removed from ${activeDevice.name}.\n\nManual package cleanup if needed:\n${manualSteps}`
            : `Yaver is being removed from ${activeDevice.name}.`,
        );
        return;
      }
      // Track the high-water mark so we can recognize a successful
      // uninstall even if the agent process exits before its final
      // `machine_remove_result` event flushes through the SSE buffer.
      // config_dir=ok is the last destructive step — past that point,
      // the box is clean even if the result event was lost.
      let configDirCleared = false;
      let resolved = false;
      const finish = (status: "ok" | "error", detail?: string, errStr?: string) => {
        if (resolved) return;
        resolved = true;
        cleanup();
        disconnect();
        setTimeout(() => { refreshDevices().catch(() => {}); }, 1500);
        Alert.alert(
          status === "error" ? "Removal failed" : "Machine removed",
          status === "error"
            ? (errStr || detail || "See the step list above.")
            : `Yaver has been entirely removed from ${activeDevice.name}.`,
        );
      };
      const cleanup = quicClient.streamLog(
        streamName,
        (evt: any) => {
          if (evt?.type === "machine_remove_step") {
            setMachineRemoveSteps((prev) => [...prev, { step: evt.step, status: evt.status, detail: evt.detail || "", error: evt.error }]);
            if (evt.step === "config_dir" && evt.status === "ok") {
              configDirCleared = true;
            }
          } else if (evt?.type === "machine_remove_result") {
            finish(evt.status === "error" ? "error" : "ok", evt.detail, evt.error);
          }
        },
        () => {
          // Stream closed (agent process exited). If we already saw
          // config_dir=ok this is the expected end-state; otherwise
          // treat as in-flight and let the user retry / refresh.
          if (configDirCleared) {
            finish("ok");
          } else {
            finish("error", "Connection dropped before the agent reported completion. Refresh device list to verify.");
          }
        },
      );
    } catch (error) {
      Alert.alert("Couldn't Remove Machine", `Yaver couldn't remove the machine. Check your connection and try again.\n\n${error instanceof Error ? error.message : "Failed to remove machine."}`);
    } finally {
      setRemovingMachine(false);
    }
  };

  // ── Sign-in methods (link / unlink / merge) ──────────────────────────
  // Loads linked identities on mount and after mutations so the UI stays
  // in sync with Convex. Errors surface inline so the user can retry
  // without leaving the settings screen.
  const refreshIdentities = async () => {
    const { getToken } = await import("../../src/lib/auth");
    const tok = await getToken();
    if (!tok) return;
    try {
      const list = await listAuthIdentities(tok);
      setIdentities(list);
    } catch {
      // keep previous list; user can tap again
    }
  };

  useEffect(() => {
    refreshIdentities();
  }, [user?.id]);

  useEffect(() => {
    void getAuthConfig()
      .then((config) => setEmailPasswordEnabled(config.emailPasswordEnabled))
      .catch(() => setEmailPasswordEnabled(false));
  }, []);

  const hasEmailPassword = identities.some((identity) => identity.provider === "email");

  const handleSetAccountPassword = async () => {
    if (!token) return;
    setAccountPasswordError(null);
    setAccountPasswordMessage(null);
    if (newAccountPassword.length < 8) {
      setAccountPasswordError("Password must be at least 8 characters.");
      return;
    }
    if (newAccountPassword !== confirmAccountPassword) {
      setAccountPasswordError("Passwords do not match.");
      return;
    }
    setSettingAccountPassword(true);
    try {
      await setAccountPasswordApi(token, newAccountPassword);
      setNewAccountPassword("");
      setConfirmAccountPassword("");
      setAccountPasswordMessage("Email/password is linked to this account.");
      await refreshIdentities();
    } catch (e: any) {
      setAccountPasswordError(e?.message || "Could not set password.");
    } finally {
      setSettingAccountPassword(false);
    }
  };

  // Cold-launch OAuth-link success. When the user taps a provider
  // button in the web UI, the callback redirects to
  // `yaver://oauth-callback?linkedProvider=…&linked=1`. That opens
  // the app and routes through `app/oauth-callback.tsx`, which
  // forwards the user to this tab with linkedProvider as a query
  // param. The in-place URL listener below only fires if Settings
  // was already mounted, so this effect closes the gap for the
  // cold-start case.
  const linkedProviderParam = useLocalSearchParams().linkedProvider;
  const handledLinkedProvider = useRef<string | null>(null);
  useEffect(() => {
    if (typeof linkedProviderParam !== "string" || !linkedProviderParam) return;
    if (handledLinkedProvider.current === linkedProviderParam) return;
    handledLinkedProvider.current = linkedProviderParam;
    setLinkingProvider(null);
    refreshIdentities();
    Alert.alert("Linked", `${linkedProviderParam} added to this Yaver account.`);
    // Clear the param so a navigation back here doesn't re-toast.
    try {
      router.setParams({ linkedProvider: "" });
    } catch {
      // best-effort; older expo-router versions may not support this
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [linkedProviderParam]);

  // Deep-link from the Tasks empty-state "link an existing account"
  // prompt (?linkAccount=1). A brand-new account that signed in with a
  // different provider than its other devices lands here with zero
  // machines; scroll straight to the sign-in-method buttons so the fix
  // (link the original provider) is one tap away. onLayout fires before
  // this on mount, so accountSectionY is populated; the timeout is a
  // belt-and-suspenders for slow first layout.
  const linkAccountParam = useLocalSearchParams().linkAccount;
  const handledLinkAccount = useRef(false);
  useEffect(() => {
    if (!linkAccountParam || handledLinkAccount.current) return;
    handledLinkAccount.current = true;
    const t = setTimeout(() => {
      scrollViewRef.current?.scrollTo({ y: Math.max(0, accountSectionY.current - 12), animated: true });
    }, 350);
    try {
      router.setParams({ linkAccount: "" });
    } catch {
      // best-effort
    }
    return () => clearTimeout(t);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [linkAccountParam]);

  // Refresh linked identities when the app returns to the foreground.
  // The OAuth link flow sends the user to Safari / Chrome — when they
  // come back we want the "Sign-In Methods" list to reflect reality
  // without forcing a manual pull-to-refresh.
  useEffect(() => {
    const sub = AppState.addEventListener("change", (next) => {
      if (next === "active") {
        refreshIdentities();
        // If a link was in-flight, clear the spinner state — the user
        // either finished OAuth or bailed, and we'll see the outcome in
        // the refreshed identity list.
        setLinkingProvider(null);
      }
    });
    return () => sub.remove();
    // refreshIdentities is defined in-scope and closes over latest
    // state via the identities setter — dropping it from deps is fine
    // and the lint is suppressed where it'd suggest otherwise.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  // Fast-path: the web OAuth callback redirects to
  //   yaver://oauth-callback?linked=1&linkedProvider=<provider>
  // when the flow was initiated as a link (vs. a fresh sign-in). We
  // listen for that URL shape here and surface a success toast + kick
  // the identity refresh immediately, so the user doesn't have to wait
  // for the next AppState focus fire or tab away and back.
  useEffect(() => {
    const sub = Linking.addEventListener("url", (event) => {
      const url = event.url || "";
      if (!isOAuthCallbackUrl(url)) return;
      if (!/\blinked=1\b/.test(url)) return;
      const parsed = ExpoLinking.parse(url);
      const linkedProvider = (parsed.queryParams?.linkedProvider as string | undefined) || "provider";
      setLinkingProvider(null);
      refreshIdentities();
      Alert.alert("Linked", `${linkedProvider} added to this Yaver account.`);
    });
    return () => sub.remove();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const handleLinkProvider = async (provider: OAuthProvider) => {
    setAuthError(null);
    setLinkingProvider(provider);
    try {
      const { getToken } = await import("../../src/lib/auth");
      const tok = await getToken();
      if (!tok) throw new Error("Not signed in.");
      const intent = await startLinkIntent(tok, provider);
      const returnUrl = OAUTH_REDIRECT;
      const result = await WebBrowser.openAuthSessionAsync(intent.url, returnUrl);

      if (result.type === "cancel" || result.type === "dismiss") {
        return;
      }

      if (result.type === "success" && result.url) {
        const parsed = ExpoLinking.parse(result.url);
        const linkedProvider =
          (parsed.queryParams?.linkedProvider as string | undefined) || provider;
        refreshIdentities();
        Alert.alert("Linked", `${linkedProvider} added to this Yaver account.`);
        return;
      }

      // Fallback: if the platform reports a non-success result but the app
      // returned to the foreground, the AppState/link listeners above will
      // still refresh identities and clear the in-flight state.
    } catch (e: any) {
      setAuthError(e?.message || `Failed to start ${provider} link`);
    } finally {
      setLinkingProvider(null);
    }
  };

  const handleUnlinkProvider = async (provider: AuthIdentity["provider"]) => {
    if (identities.length <= 1) {
      Alert.alert("Can't unlink", "This is your only sign-in method. Add another provider before removing this one.");
      return;
    }
    Alert.alert(
      `Unlink ${provider}?`,
      `You won't be able to sign in with ${provider} afterwards. You'll still be able to sign in with ${identities.length - 1} other method${identities.length - 1 === 1 ? "" : "s"}.`,
      [
        { text: "Cancel", style: "cancel" },
        {
          text: "Unlink",
          style: "destructive",
          onPress: async () => {
            setAuthError(null);
            setUnlinkingProvider(provider);
            try {
              const { getToken } = await import("../../src/lib/auth");
              const tok = await getToken();
              if (!tok) throw new Error("Not signed in.");
              await unlinkProviderApi(tok, provider);
              await refreshIdentities();
            } catch (e: any) {
              setAuthError(e?.message || `Failed to unlink ${provider}`);
            } finally {
              setUnlinkingProvider(null);
            }
          },
        },
      ],
    );
  };

  const handleStartMerge = async () => {
    setAuthError(null);
    setMergeStarting(true);
    try {
      const { getToken } = await import("../../src/lib/auth");
      const tok = await getToken();
      if (!tok) throw new Error("Not signed in.");
      const intent = await startMergeIntent(tok);
      setMergeIntent(intent);
    } catch (e: any) {
      setAuthError(e?.message || "Failed to start merge");
    } finally {
      setMergeStarting(false);
    }
  };

  const handleCancelMerge = async () => {
    if (!mergeIntent) return;
    const { getToken } = await import("../../src/lib/auth");
    const tok = await getToken();
    if (tok) await cancelMergeIntent(tok, mergeIntent.mergeToken);
    setMergeIntent(null);
  };

  const handleOpenMergeUrl = async () => {
    if (mergeIntent) await Linking.openURL(mergeIntent.approvalUrl);
  };

  const [mergeUrlCopied, setMergeUrlCopied] = useState(false);
  const handleCopyMergeUrl = async () => {
    if (!mergeIntent) return;
    try {
      await ExpoClipboard.setStringAsync(mergeIntent.approvalUrl);
      setMergeUrlCopied(true);
      setTimeout(() => setMergeUrlCopied(false), 2000);
    } catch {
      // clipboard unavailable — user can still long-press the URL
    }
  };

  const startTestApp = async (target: 'device' | 'simulator') => {
    setTestTarget(target);
    setTestRunning(true);
    setAgentLogs([]);
    try {
      // Start yaver logs -f via exec on the agent
      const { execId } = await quicClient.startExec("yaver logs -f");
      setTestExecId(execId);

      // Stream output via XHR onprogress (works in React Native)
      const url = `${quicClient.baseUrl}/exec/${execId}/stream`;
      const xhr = new XMLHttpRequest();
      xhr.open("GET", url, true);
      xhr.setRequestHeader("Authorization", `Bearer ${token}`);
      let lastIndex = 0;

      xhr.onprogress = () => {
        const newData = xhr.responseText.slice(lastIndex);
        lastIndex = xhr.responseText.length;
        const lines = newData.split("\n");
        const logLines: string[] = [];
        for (const line of lines) {
          if (line.startsWith("data: ")) {
            const payload = line.slice(6).trim();
            if (!payload) continue;
            try {
              const evt = JSON.parse(payload);
              // Extract raw text from the exec event
              const text = evt.data || evt.output || evt.text || "";
              if (text) logLines.push(text);
            } catch {
              if (payload) logLines.push(payload);
            }
          }
        }
        if (logLines.length > 0) {
          setAgentLogs((prev) => {
            const next = [...prev, ...logLines];
            return next.length > 500 ? next.slice(-500) : next;
          });
        }
      };

      xhr.onerror = () => {
        setAgentLogs((prev) => [...prev, "[error] Connection lost"]);
        setTestRunning(false);
      };

      xhr.onloadend = () => {
        setTestRunning(false);
      };

      // Store xhr ref so we can abort
      testAbortRef.current = { abort: () => xhr.abort() } as AbortController;
      xhr.send();
    } catch (e: any) {
      setAgentLogs((prev) => [...prev, `[error] ${e.message}`]);
      setTestRunning(false);
    }
  };

  const stopTestApp = async () => {
    testAbortRef.current?.abort();
    if (testExecId) {
      try {
        await quicClient.killExec(testExecId);
      } catch {}
    }
    setTestRunning(false);
    setTestExecId(null);
  };

  return (
    <View style={[styles.safeArea, { backgroundColor: c.bg }]}>
      {/* Header */}
      <AppScreenHeader title="Settings" onBack={() => router.navigate("/(tabs)/more" as any)} />
      <KeyboardAvoidingView
        style={{ flex: 1 }}
        behavior={Platform.OS === "ios" ? "padding" : undefined}
        keyboardVerticalOffset={Platform.OS === "ios" ? 90 : 0}
      >
      <ScrollView
        ref={scrollViewRef}
        style={styles.container}
        contentContainerStyle={[styles.scrollContent, tabletContent]}
        keyboardShouldPersistTaps="handled"
        keyboardDismissMode="interactive"
      >
        {/* Machine + voice controls */}
        {/* Per-machine coding agent preference lives before toolchain sync:
            choose the default runner for the connected box and drive remote
            auth for Claude/Codex from the same compact surface. */}
        {connectionStatus === "connected" && activeDevice && !activeDevice.isGuest ? (
          <View style={styles.section}>
            <Text style={[styles.sectionLabel, { color: c.textMuted }]}>
              Coding agent - {activeDevice.name}
            </Text>
            <CodingAgentsSection device={activeDevice} />
          </View>
        ) : null}

        {/* Default device routing — primary + secondary picker. The
            same controls that live as inline pills on each Devices-
            tab card, surfaced here so the user can re-route without
            switching tabs. setPrimaryDevice / setSecondaryDevice
            route through DeviceContext (Convex + watchdog
            propagation), same path the Devices tab uses, so the
            change reflects on every signed-in surface immediately. */}
        {(() => {
          const eligible = devices.filter((d) => !d.isGuest);
          if (eligible.length === 0) return null;
          const primary = eligible.find((d) => d.id === primaryDeviceId) || null;
          const secondary = eligible.find((d) => d.id === secondaryDeviceId) || null;
          // Build the selection sheet — used for both Change Primary
          // and Set/Change Secondary. Displays each non-guest device
          // by name, calls the appropriate setter, surfaces the
          // failure inline. Skips the device that's already in the
          // OTHER role (you can't be primary and secondary at once).
          const pickDevice = (kind: "primary" | "secondary") => {
            const excludeId = kind === "primary" ? secondaryDeviceId : primaryDeviceId;
            const choices = eligible.filter((d) => d.id !== excludeId);
            const buttons: { text: string; onPress?: () => void; style?: "cancel" | "destructive" }[] = [];
            for (const d of choices) {
              const isCurrent = (kind === "primary" ? primaryDeviceId : secondaryDeviceId) === d.id;
              buttons.push({
                text: isCurrent ? `${d.name} (current)` : d.name,
                onPress: async () => {
                  try {
                    if (kind === "primary") {
                      await setPrimaryDevice(d.id);
                    } else {
                      await setSecondaryDevice(d.id);
                    }
                  } catch (e: any) {
                    Alert.alert(
                      "Couldn't Update Routing",
                      `Yaver couldn't save the device choice. Check your connection and try again.${e?.message ? `\n\n${e.message}` : ""}`,
                    );
                  }
                },
              });
            }
            buttons.push({ text: "Cancel", style: "cancel" });
            Alert.alert(
              kind === "primary" ? "Pick primary device" : "Pick secondary device",
              kind === "primary"
                ? "The primary box is what Yaver auto-connects to and routes new tasks at by default."
                : "The secondary box is the watchdog fallback when primary is unreachable.",
              buttons,
            );
          };
          return (
            <View style={styles.section}>
              <Text style={[styles.sectionLabel, { color: c.textMuted }]}>Default routing</Text>
              <View style={[styles.card, { backgroundColor: c.bgCard, borderColor: c.border }]}>
                <View style={styles.aboutRow}>
                  <View style={{ flex: 1 }}>
                    <Text style={[styles.aboutLabel, { color: c.textPrimary }]}>★ Primary</Text>
                    <Text style={[styles.aboutValue, { color: c.textMuted, fontSize: 12, marginTop: 2 }]}>
                      {primary ? primary.name : "(none picked)"}
                    </Text>
                  </View>
                  <Pressable
                    onPress={() => pickDevice("primary")}
                    style={({ pressed }) => [
                      { paddingHorizontal: 12, paddingVertical: 8, borderRadius: 8, backgroundColor: c.accent + "18" },
                      pressed && { opacity: 0.6 },
                    ]}
                  >
                    <Text style={{ color: c.accent, fontWeight: "600", fontSize: 13 }}>
                      {primary ? "Change" : "Set"}
                    </Text>
                  </Pressable>
                </View>
                <View style={[styles.aboutRow, { borderTopWidth: 1, borderTopColor: c.border }]}>
                  <View style={{ flex: 1 }}>
                    <Text style={[styles.aboutLabel, { color: c.textPrimary }]}>☆ Secondary</Text>
                    <Text style={[styles.aboutValue, { color: c.textMuted, fontSize: 12, marginTop: 2 }]}>
                      {secondary ? secondary.name : "(none — watchdog disabled)"}
                    </Text>
                  </View>
                  <View style={{ flexDirection: "row", gap: 8 }}>
                    {secondary ? (
                      <Pressable
                        onPress={async () => {
                          try { await setSecondaryDevice(null); }
                          catch (e: any) { Alert.alert("Couldn't Update Routing", `Yaver couldn't clear the secondary device. Check your connection and try again.${e?.message ? `\n\n${e.message}` : ""}`); }
                        }}
                        style={({ pressed }) => [
                          { paddingHorizontal: 10, paddingVertical: 8, borderRadius: 8, backgroundColor: c.bgCardElevated },
                          pressed && { opacity: 0.6 },
                        ]}
                      >
                        <Text style={{ color: c.textSecondary, fontWeight: "600", fontSize: 13 }}>Unmark</Text>
                      </Pressable>
                    ) : null}
                    <Pressable
                      onPress={() => pickDevice("secondary")}
                      style={({ pressed }) => [
                        { paddingHorizontal: 12, paddingVertical: 8, borderRadius: 8, backgroundColor: c.accent + "18" },
                        pressed && { opacity: 0.6 },
                      ]}
                    >
                      <Text style={{ color: c.accent, fontWeight: "600", fontSize: 13 }}>
                        {secondary ? "Change" : "Add"}
                      </Text>
                    </Pressable>
                  </View>
                </View>
              </View>
            </View>
          );
        })()}

        <View style={styles.section}>
          <Text style={[styles.sectionLabel, { color: c.textMuted }]}>More menu</Text>
          <View style={[styles.card, { backgroundColor: c.bgCard, borderColor: c.border }]}>
            {OPTIONAL_MORE_TOOLS.map((tool, index) => {
              const enabled = moreOptionalTools.includes(tool.id);
              return (
                <View key={tool.id}>
                  {index > 0 ? <View style={[styles.separator, { backgroundColor: c.borderSubtle }]} /> : null}
                  <View style={styles.aboutRow}>
                    <View style={{ flex: 1, paddingRight: 12 }}>
                      <Text style={[styles.aboutLabel, { color: c.textPrimary }]}>{tool.label}</Text>
                      <Text style={{ color: c.textMuted, fontSize: 11, marginTop: 2 }}>
                        {tool.description}
                      </Text>
                    </View>
                    <Switch
                      value={enabled}
                      onValueChange={(value) => {
                        toggleMoreOptionalTool(tool.id, value).catch(() => {
                          setMoreOptionalTools((prev) => (value ? prev.filter((id) => id !== tool.id) : normalizeOptionalMoreTools([...prev, tool.id])));
                          Alert.alert("Couldn't Save Preference", "Yaver couldn't sync this More menu preference. Try again.");
                        });
                      }}
                      trackColor={{ false: c.border, true: c.accent + "66" }}
                      thumbColor={enabled ? c.accent : c.textMuted}
                    />
                  </View>
                </View>
              );
            })}
          </View>
        </View>

        {/* Sign in another device (Apple TV / headless box) by scanning its QR */}
        <View style={styles.section}>
          <Text style={[styles.sectionLabel, { color: c.textMuted }]}>Devices</Text>
          <View style={[styles.card, { backgroundColor: c.bgCard, borderColor: c.border }]}>
            <Pressable
              style={styles.aboutRow}
              onPress={() => router.push("/approve-device")}
            >
              <View style={{ flex: 1 }}>
                <Text style={[styles.aboutLabel, { color: c.textPrimary }]}>Sign in a device</Text>
                <Text style={{ color: c.textMuted, fontSize: 11 }}>
                  Scan the QR on an Apple TV or a headless box (or type its code) to sign it into your account
                </Text>
              </View>
              <Text style={[styles.aboutValue, { color: c.accent }]}>Scan ›</Text>
            </Pressable>
          </View>
        </View>

        {/* People & shared projects */}
        <View style={styles.section}>
          <Text style={[styles.sectionLabel, { color: c.textMuted }]}>Collaborate</Text>
          <View style={[styles.card, { backgroundColor: c.bgCard, borderColor: c.border }]}>
            <Pressable
              style={styles.aboutRow}
              onPress={() => router.push("/connections")}
            >
              <View style={{ flex: 1 }}>
                <Text style={[styles.aboutLabel, { color: c.textPrimary }]}>People & Projects</Text>
                <Text style={{ color: c.textMuted, fontSize: 11 }}>
                  Connect with people, share a repo, invite someone to code with you — your machine or Yaver Cloud
                </Text>
              </View>
              <Text style={[styles.aboutValue, { color: c.accent }]}>Open ›</Text>
            </Pressable>
          </View>
        </View>

        {/* Mobile Sandbox coding */}
        <View style={styles.section}>
          <Text style={[styles.sectionLabel, { color: c.textMuted }]}>Sandbox</Text>
          <View style={[styles.card, { backgroundColor: c.bgCard, borderColor: c.border }]}>
            <Pressable
              style={styles.aboutRow}
              onPress={() => router.push("/sandbox-ai")}
            >
              <View style={{ flex: 1 }}>
                <Text style={[styles.aboutLabel, { color: c.textPrimary }]}>Sandbox AI</Text>
                <Text style={{ color: c.textMuted, fontSize: 11 }}>
                  On-device model or your own Claude / OpenAI / GLM key for phone coding
                </Text>
              </View>
              <Text style={[styles.aboutValue, { color: c.accent }]}>Configure ›</Text>
            </Pressable>

            {Platform.OS === "android" && (
              <>
                <View style={[styles.separator, { backgroundColor: c.borderSubtle }]} />
                <Pressable
                  style={styles.aboutRow}
                  onPress={() => router.push("/local-box")}
                >
                  <View style={{ flex: 1 }}>
                    <Text style={[styles.aboutLabel, { color: c.textPrimary }]}>This phone as a box</Text>
                    <Text style={{ color: c.textMuted, fontSize: 11 }}>
                      Run a Linux userland on-device — terminal, coding agents & Hermes reload, no machine
                    </Text>
                  </View>
                  <Text style={[styles.aboutValue, { color: c.accent }]}>Open ›</Text>
                </Pressable>
              </>
            )}

            <View style={[styles.separator, { backgroundColor: c.borderSubtle }]} />
            <Pressable
              style={styles.aboutRow}
              onPress={() => router.push("/studio")}
            >
              <View style={{ flex: 1 }}>
                <Text style={[styles.aboutLabel, { color: c.textPrimary }]}>Store Studio</Text>
                <Text style={{ color: c.textMuted, fontSize: 11 }}>
                  App Store / Play assets — permission-justification videos & prose for your app
                </Text>
              </View>
              <Text style={[styles.aboutValue, { color: c.accent }]}>Open ›</Text>
            </Pressable>

            <View style={[styles.separator, { backgroundColor: c.borderSubtle }]} />
            <Pressable
              style={styles.aboutRow}
              onPress={() => router.push("/qa")}
            >
              <View style={{ flex: 1 }}>
                <Text style={[styles.aboutLabel, { color: c.textPrimary }]}>App-Test Agent</Text>
                <Text style={{ color: c.textMuted, fontSize: 11 }}>
                  Drive your app on redroid, catch bugs (red box / crash / ANR) — catch-only or fix
                </Text>
              </View>
              <Text style={[styles.aboutValue, { color: c.accent }]}>Open ›</Text>
            </Pressable>

            {Platform.OS === "android" && (
              <>
                <View style={[styles.separator, { backgroundColor: c.borderSubtle }]} />
                <Pressable
                  style={styles.aboutRow}
                  onPress={() => router.push("/local-box")}
                >
                  <View style={{ flex: 1 }}>
                    <Text style={[styles.aboutLabel, { color: c.textPrimary }]}>This phone as a box</Text>
                    <Text style={{ color: c.textMuted, fontSize: 11 }}>
                      Run a Linux userland on-device — terminal, coding agents & Hermes reload, no machine
                    </Text>
                  </View>
                  <Text style={[styles.aboutValue, { color: c.accent }]}>Open ›</Text>
                </Pressable>
              </>
            )}

            <View style={[styles.separator, { backgroundColor: c.borderSubtle }]} />

            <Pressable
              style={styles.aboutRow}
              onPress={() => router.push("/local-models")}
            >
              <View style={{ flex: 1 }}>
                <Text style={[styles.aboutLabel, { color: c.textPrimary }]}>On-device models</Text>
                <Text style={{ color: c.textMuted, fontSize: 11 }}>
                  Download + manage local LLMs (voice helper, coder) — run offline
                </Text>
              </View>
              <Text style={[styles.aboutValue, { color: c.accent }]}>Manage ›</Text>
            </Pressable>

            <View style={[styles.separator, { backgroundColor: c.borderSubtle }]} />

            <Pressable
              style={styles.aboutRow}
              onPress={() => router.push("/assistant")}
            >
              <View style={{ flex: 1 }}>
                <Text style={[styles.aboutLabel, { color: c.textPrimary }]}>Voice assistant</Text>
                <Text style={{ color: c.textMuted, fontSize: 11 }}>
                  Hold-to-talk device control — connect, reconnect, switch agents, run safe actions
                </Text>
              </View>
              <Text style={[styles.aboutValue, { color: c.accent }]}>Open ›</Text>
            </Pressable>
          </View>
        </View>

        {/* Voice Input & TTS */}
        <View style={styles.section}>
          <Text style={[styles.sectionLabel, { color: c.textMuted }]}>Voice</Text>
          <View style={[styles.card, { backgroundColor: c.bgCard, borderColor: c.border }]}>
            <Pressable
              style={styles.aboutRow}
              onPress={() => router.navigate("/voice-config" as any)}
            >
              <View style={{ flex: 1 }}>
                <Text style={[styles.aboutLabel, { color: c.textPrimary }]}>Agent voice loop</Text>
                <Text style={{ color: c.textMuted, fontSize: 11 }}>Deepgram Flux STT + Cartesia Sonic TTS, stored in the agent vault</Text>
              </View>
              <Text style={[styles.aboutValue, { color: c.accent }]}>Configure ›</Text>
            </Pressable>

            <View style={[styles.separator, { backgroundColor: c.borderSubtle }]} />

            <Pressable
              style={styles.aboutRow}
              onPress={() => setShowSpeechConfig(!showSpeechConfig)}
            >
              <Text style={[styles.aboutLabel, { color: c.textPrimary }]}>Speech-to-Text</Text>
              <Text style={[styles.aboutValue, { color: c.accent }]}>
                {speechProvider ? SPEECH_PROVIDERS.find(p => p.id === speechProvider)?.name ?? speechProvider : "Not configured"}
                {" \u25BE"}
              </Text>
            </Pressable>

            {showSpeechConfig && (
              <View style={{ paddingHorizontal: 16, paddingBottom: 12 }}>
                <Text style={[styles.sectionLabel, { color: c.textMuted, marginTop: 4, marginBottom: 8 }]}>Voice Engine</Text>
                <Text style={{ color: c.textMuted, fontSize: 11, marginBottom: 8 }}>
                  This section is for on-phone dictation and local readback. Use Agent voice loop above for Deepgram Flux + Cartesia on tasks and SDK sessions.
                </Text>
                <View style={{ flexDirection: "row", gap: 8, marginBottom: 12 }}>
                  {([
                    { id: "local", title: "Local", sub: "Free", stt: "on-device" as SpeechProvider, tts: "device" as TtsProvider },
                    { id: "openai", title: "OpenAI", sub: "API key", stt: "openai" as SpeechProvider, tts: "openai" as TtsProvider },
                  ] as const).map((engine) => {
                    const selected = speechProvider === engine.stt && ttsProvider === engine.tts;
                    return (
                      <Pressable
                        key={engine.id}
                        onPress={() => {
                          setSpeechProvider(engine.stt);
                          setTtsProvider(engine.tts);
                          if (engine.tts === "openai") setTtsEnabled(true);
                        }}
                        style={{
                          flex: 1,
                          paddingVertical: 10,
                          paddingHorizontal: 12,
                          borderRadius: 8,
                          borderWidth: 1,
                          backgroundColor: selected ? c.accent : c.bg,
                          borderColor: selected ? c.accent : c.border,
                        }}
                      >
                        <Text style={{ color: selected ? "#fff" : c.textPrimary, fontWeight: "600", fontSize: 13 }}>{engine.title}</Text>
                        <Text style={{ color: selected ? "rgba(255,255,255,0.72)" : c.textMuted, fontSize: 11, marginTop: 2 }}>{engine.sub}</Text>
                      </Pressable>
                    );
                  })}
                </View>

                {SPEECH_PROVIDERS.map((provider) => {
                  const selected = speechProvider === provider.id;
                  return (
                    <Pressable
                      key={provider.id}
                      style={({ pressed }) => [
                        {
                          paddingVertical: 10, paddingHorizontal: 12, borderRadius: 8,
                          marginTop: 6, borderWidth: 1,
                          backgroundColor: selected ? c.accent : c.bg,
                          borderColor: selected ? c.accent : c.border,
                        },
                        pressed && { opacity: 0.7 },
                      ]}
                      onPress={() => setSpeechProvider(provider.id)}
                    >
                      <Text style={{ color: selected ? "#fff" : c.textPrimary, fontWeight: "500", fontSize: 14 }}>
                        {provider.name}
                      </Text>
                      <Text style={{ color: selected ? "rgba(255,255,255,0.7)" : c.textMuted, fontSize: 11, marginTop: 2 }}>
                        {provider.description}
                      </Text>
                    </Pressable>
                  );
                })}

                <Pressable
                  style={({ pressed }) => [
                    {
                      paddingVertical: 10, paddingHorizontal: 12, borderRadius: 8,
                      marginTop: 6, borderWidth: 1,
                      backgroundColor: !speechProvider ? c.accent : c.bg,
                      borderColor: !speechProvider ? c.accent : c.border,
                    },
                    pressed && { opacity: 0.7 },
                  ]}
                  onPress={() => setSpeechProvider(null)}
                >
                  <Text style={{ color: !speechProvider ? "#fff" : c.textPrimary, fontWeight: "500", fontSize: 14 }}>
                    Disabled
                  </Text>
                  <Text style={{ color: !speechProvider ? "rgba(255,255,255,0.7)" : c.textMuted, fontSize: 11, marginTop: 2 }}>
                    No voice input - type only
                  </Text>
                </Pressable>

                {(speechProvider && SPEECH_PROVIDERS.find(p => p.id === speechProvider)?.requiresKey) || ttsProvider === "openai" || ttsProvider === "openrouter" ? (
                  <TextInput
                    style={[{
                      borderWidth: 1, borderRadius: 8, paddingVertical: 10, paddingHorizontal: 12,
                      fontSize: 14, marginTop: 10,
                      backgroundColor: c.bg, borderColor: c.border, color: c.textPrimary,
                    }]}
                    placeholder={SPEECH_PROVIDERS.find(p => p.id === speechProvider)?.keyPlaceholder ?? ((ttsProvider === "openrouter") ? "sk-or-..." : "OpenAI API key")}
                    placeholderTextColor={c.textMuted}
                    value={speechApiKey}
                    onChangeText={setSpeechApiKey}
                    autoCapitalize="none"
                    autoCorrect={false}
                    secureTextEntry
                  />
                ) : null}

                {/* STT model — only OpenAI/OpenRouter expose a model
                    choice; on-device/deepgram/assemblyai are fixed. */}
                {speechProvider === "openai" || speechProvider === "openrouter" ? (
                  <View style={{ marginTop: 10 }}>
                    <Text style={[styles.sectionLabel, { color: c.textMuted, marginBottom: 6 }]}>Transcription model</Text>
                    <View style={{ flexDirection: "row", flexWrap: "wrap", gap: 6 }}>
                      {STT_MODELS.map((m) => {
                        const sel = (sttModel || DEFAULT_STT_MODEL) === m.id;
                        return (
                          <Pressable
                            key={m.id}
                            onPress={() => setSttModel(m.id)}
                            style={{
                              paddingVertical: 6, paddingHorizontal: 10, borderRadius: 8, borderWidth: 1,
                              backgroundColor: sel ? c.accent + "30" : c.bgInput,
                              borderColor: sel ? c.accent : c.border,
                            }}
                          >
                            <Text style={{ color: sel ? c.accent : c.textSecondary, fontSize: 12, fontWeight: "600" }}>{m.label}</Text>
                          </Pressable>
                        );
                      })}
                    </View>
                  </View>
                ) : null}

                <Pressable
                  style={({ pressed }) => [
                    {
                      marginTop: 12, paddingVertical: 10, borderRadius: 8,
                      backgroundColor: c.accent, alignItems: "center",
                    },
                    pressed && { opacity: 0.7 },
                    isSavingSpeech && { opacity: 0.5 },
                  ]}
                  onPress={async () => {
                    if (!token) return;
                    setIsSavingSpeech(true);
                    try {
                      // Speech config is LOCAL ONLY — provider, key,
                      // model and voice go to SecureStore via
                      // saveLocalSpeechConfig and are NEVER sent to
                      // Convex. Only the non-speech prefs (ttsEnabled,
                      // verbosity) are synced to the cloud.
                      await saveLocalSpeechConfig({
                        sttProvider: speechProvider ?? "on-device",
                        sttModel: sttModel || DEFAULT_STT_MODEL,
                        ttsProvider,
                        ttsModel: ttsModel || DEFAULT_TTS_MODEL,
                        ttsVoice: ttsVoice || DEFAULT_TTS_VOICE,
                        apiKey: speechApiKey,
                      });
                      await saveUserSettings(token, { ttsEnabled, verbosity });
                      setShowSpeechConfig(false);
                    } catch {}
                    setIsSavingSpeech(false);
                  }}
                  disabled={isSavingSpeech}
                >
                  <Text style={{ color: "#fff", fontWeight: "600", fontSize: 14 }}>
                    {isSavingSpeech ? "Saving..." : "Save"}
                  </Text>
                </Pressable>
              </View>
            )}

            <View style={[styles.separator, { backgroundColor: c.borderSubtle }]} />

            <View style={[styles.aboutRow, { justifyContent: "space-between" }]}>
              <View style={{ flex: 1 }}>
                <Text style={[styles.aboutLabel, { color: c.textPrimary }]}>Read responses aloud</Text>
                <Text style={{ color: c.textMuted, fontSize: 11 }}>{ttsProvider === "openai" ? "Uses OpenAI text-to-speech" : "Uses local device text-to-speech"}</Text>
              </View>
              <Switch
                value={ttsEnabled}
                onValueChange={async (val) => {
                  setTtsEnabled(val);
                  if (token) saveUserSettings(token, { ttsEnabled: val }).catch(() => {});
                }}
                trackColor={{ false: c.border, true: c.accent }}
              />
            </View>

            <View style={[styles.aboutRow, { justifyContent: "space-between" }]}>
              <View style={{ flex: 1 }}>
                <Text style={[styles.aboutLabel, { color: c.textPrimary }]}>Run tasks in TTS mode</Text>
                <Text style={{ color: c.textMuted, fontSize: 11 }}>Agent leads each reply with a short spoken-style summary, then the usual details. Text only — nothing is read aloud unless you also enable “Read responses aloud”.</Text>
              </View>
              <Switch
                value={ttsTaskMode}
                onValueChange={async (val) => {
                  setTtsTaskMode(val);
                  quicClient.setTtsTaskMode(val); // apply to tasks immediately + persist locally
                  if (token) saveUserSettings(token, { ttsTaskMode: val }).catch(() => {});
                }}
                trackColor={{ false: c.border, true: c.accent }}
              />
            </View>

            {ttsEnabled && (
              <View style={{ flexDirection: "row", gap: 8, paddingHorizontal: 16, paddingBottom: 12 }}>
                {TTS_PROVIDERS.map((provider) => {
                  const selected = ttsProvider === provider.id;
                  return (
                    <Pressable
                      key={provider.id}
                      onPress={() => {
                        setTtsProvider(provider.id);
                        // ttsProvider is speech config → local only.
                        void saveLocalSpeechConfig({
                          sttProvider: speechProvider ?? "on-device",
                          sttModel: sttModel || DEFAULT_STT_MODEL,
                          ttsProvider: provider.id,
                          ttsModel: ttsModel || DEFAULT_TTS_MODEL,
                          ttsVoice: ttsVoice || DEFAULT_TTS_VOICE,
                          apiKey: speechApiKey,
                        });
                        if (token) saveUserSettings(token, { ttsEnabled: true }).catch(() => {});
                      }}
                      style={{
                        flex: 1,
                        paddingVertical: 8,
                        paddingHorizontal: 10,
                        borderRadius: 8,
                        borderWidth: 1,
                        backgroundColor: selected ? c.accent + "30" : c.bgInput,
                        borderColor: selected ? c.accent : c.border,
                      }}
                    >
                      <Text style={{ color: selected ? c.accent : c.textSecondary, fontSize: 12, fontWeight: "600" }}>{provider.name}</Text>
                    </Pressable>
                  );
                })}
              </View>
            )}

            {ttsEnabled && (ttsProvider === "openai" || ttsProvider === "openrouter") && (
              <View style={{ paddingHorizontal: 16, paddingBottom: 12, gap: 10 }}>
                <View>
                  <Text style={[styles.sectionLabel, { color: c.textMuted, marginBottom: 6 }]}>Voice model</Text>
                  <View style={{ flexDirection: "row", flexWrap: "wrap", gap: 6 }}>
                    {TTS_MODELS.map((m) => {
                      const sel = (ttsModel || DEFAULT_TTS_MODEL) === m.id;
                      return (
                        <Pressable
                          key={m.id}
                          onPress={() => setTtsModel(m.id)}
                          style={{
                            paddingVertical: 6, paddingHorizontal: 10, borderRadius: 8, borderWidth: 1,
                            backgroundColor: sel ? c.accent + "30" : c.bgInput,
                            borderColor: sel ? c.accent : c.border,
                          }}
                        >
                          <Text style={{ color: sel ? c.accent : c.textSecondary, fontSize: 12, fontWeight: "600" }}>{m.label}</Text>
                        </Pressable>
                      );
                    })}
                  </View>
                </View>
                <View>
                  <Text style={[styles.sectionLabel, { color: c.textMuted, marginBottom: 6 }]}>Voice</Text>
                  <View style={{ flexDirection: "row", flexWrap: "wrap", gap: 6 }}>
                    {TTS_VOICES.map((v) => {
                      const sel = (ttsVoice || DEFAULT_TTS_VOICE) === v;
                      return (
                        <Pressable
                          key={v}
                          onPress={() => setTtsVoice(v)}
                          style={{
                            paddingVertical: 6, paddingHorizontal: 12, borderRadius: 8, borderWidth: 1,
                            backgroundColor: sel ? c.accent + "30" : c.bgInput,
                            borderColor: sel ? c.accent : c.border,
                          }}
                        >
                          <Text style={{ color: sel ? c.accent : c.textSecondary, fontSize: 12, fontWeight: "600" }}>{v}</Text>
                        </Pressable>
                      );
                    })}
                  </View>
                </View>
                <Text style={{ color: c.textMuted, fontSize: 11 }}>
                  Speech provider, key, model and voice are stored only on this device — never synced to the cloud.
                </Text>
              </View>
            )}

            <View style={[styles.separator, { backgroundColor: c.borderSubtle }]} />

            <View style={{ paddingHorizontal: 16, paddingVertical: 12 }}>
              <View style={{ flexDirection: "row", justifyContent: "space-between", alignItems: "center" }}>
                <Text style={[styles.aboutLabel, { color: c.textPrimary }]}>Response detail</Text>
                <Text style={{ color: c.accent, fontWeight: "600", fontSize: 14 }}>{verbosity}/10</Text>
              </View>
              <Text style={{ color: c.textMuted, fontSize: 11, marginTop: 2, marginBottom: 10 }}>
                {verbosity <= 2 ? "Minimal - just confirm what was done"
                  : verbosity <= 4 ? "Brief - summarize in a few sentences"
                  : verbosity <= 6 ? "Moderate - key changes and reasoning"
                  : verbosity <= 8 ? "Detailed - code changes and explanations"
                  : "Full - everything: diffs, reasoning, alternatives"}
              </Text>
              <View style={{ flexDirection: "row", gap: 3 }}>
                {Array.from({ length: 11 }).map((_, i) => (
                  <Pressable
                    key={i}
                    onPress={async () => {
                      setVerbosity(i);
                      if (token) saveUserSettings(token, { verbosity: i }).catch(() => {});
                    }}
                    style={{
                      flex: 1, height: 24, borderRadius: 4,
                      backgroundColor: i <= verbosity ? c.accent : c.bg,
                      borderWidth: 1,
                      borderColor: i <= verbosity ? c.accent : c.border,
                      alignItems: "center", justifyContent: "center",
                    }}
                  >
                    {i === verbosity && (
                      <Text style={{ color: "#fff", fontSize: 8, fontWeight: "700" }}>{i}</Text>
                    )}
                  </Pressable>
                ))}
              </View>
            </View>
          </View>
        </View>

        <View
          style={styles.section}
          onLayout={(e) => { accountSectionY.current = e.nativeEvent.layout.y; }}
        >
          <Text style={[styles.sectionLabel, { color: c.textMuted }]}>Account</Text>
          <View style={[styles.profileCard, { backgroundColor: c.bgCard, borderColor: c.border }]}>
            <View style={[styles.avatar, { backgroundColor: c.accent }]}>
              <Text style={[styles.avatarText, { color: c.textInverse }]}>
                {displayName ? displayName.charAt(0).toUpperCase() : "?"}
              </Text>
            </View>
            <View style={styles.profileInfo}>
              {isEditingName ? (
                <View style={styles.editNameRow}>
                  <TextInput
                    style={[styles.editNameInput, { backgroundColor: c.bgCardElevated, borderColor: c.border, color: c.textPrimary }]}
                    value={editName}
                    onChangeText={setEditName}
                    autoCapitalize="words"
                    autoFocus
                  />
                  <Pressable
                    style={[styles.editNameButton, { backgroundColor: c.accent }]}
                    onPress={handleSaveName}
                    disabled={isSavingName}
                  >
                    <Text style={styles.editNameButtonText}>{isSavingName ? "..." : "Save"}</Text>
                  </Pressable>
                </View>
              ) : (
                <Pressable onPress={() => { setEditName(displayName ?? ""); setIsEditingName(true); }}>
                  <Text style={[styles.profileName, { color: displayName ? c.textPrimary : c.textMuted }]}>
                    {displayName || "Set your name"}
                  </Text>
                </Pressable>
              )}
              <Text style={[styles.profileEmail, { color: c.textMuted }]}>
                {user?.email ?? "No email"}
              </Text>
            </View>
          </View>

          {user?.id ? (
            <Pressable
              onPress={async () => {
                await ExpoClipboard.setStringAsync(user.id);
                Alert.alert(
                  "User ID copied",
                  "Paste this in WhatsApp / iMessage / email so someone can invite you to share their machine.",
                );
              }}
              style={{
                marginTop: 8,
                backgroundColor: c.bgCard,
                borderColor: c.border,
                borderWidth: 1,
                borderRadius: 10,
                padding: 12,
                flexDirection: "row",
                alignItems: "center",
                gap: 10,
              }}
            >
              <View style={{ flex: 1 }}>
                <Text style={{ color: c.textMuted, fontSize: 10, textTransform: "uppercase", fontWeight: "700" }}>
                  Your user ID
                </Text>
                <Text
                  selectable
                  style={{ color: c.textPrimary, fontFamily: Platform.OS === "ios" ? "SF Mono" : "monospace", fontSize: 15, marginTop: 4 }}
                  numberOfLines={1}
                >
                  {user.id}
                </Text>
                <Text style={{ color: c.textMuted, fontSize: 13, marginTop: 4 }}>
                  Share this with a friend so they can invite you without knowing your email.
                </Text>
              </View>
              <View style={{ alignItems: "center", gap: 4 }}>
                <Ionicons name="copy-outline" size={20} color={c.accent} />
                <Text style={{ color: c.accent, fontSize: 10, fontWeight: "700" }}>COPY</Text>
              </View>
            </Pressable>
          ) : null}

          <View
            style={{
              marginTop: 8,
              backgroundColor: c.bgCard,
              borderColor: c.border,
              borderWidth: 1,
              borderRadius: 10,
              padding: 12,
            }}
          >
            <Text style={{ color: c.textMuted, fontSize: 10, textTransform: "uppercase", fontWeight: "700" }}>
              Mobile app version
            </Text>
            <Text style={{ color: c.textPrimary, fontSize: 15, fontWeight: "700", marginTop: 4 }}>
              v{APP_VERSION}
            </Text>
            <Text style={{ color: c.textMuted, fontSize: 11, marginTop: 4 }}>
              Build {BUILD_NUMBER}
            </Text>
          </View>
        </View>

        {/* Developer Profile section removed — survey no longer required */}

        {/* Sign-in methods (link / unlink / merge) */}
        <View style={styles.section}>
          <Text style={[styles.sectionLabel, { color: c.textMuted }]}>Sign-In Methods</Text>
          <View style={[styles.card, { backgroundColor: c.bgCard, borderColor: c.border, padding: 14 }]}>
            <Text style={{ color: c.textMuted, fontSize: 12, lineHeight: 18 }}>
              Link Apple, GitHub, GitLab, Google, or Microsoft to this same Yaver account. Future sign-ins with any linked provider open the same machines and devices.
            </Text>
            {authError && (
              <Text style={{ color: c.error, fontSize: 12, marginTop: 10 }}>{authError}</Text>
            )}
            {identities.length > 0 && (
              <View style={{ marginTop: 12 }}>
                {identities.map((identity) => {
                  const canUnlink = identities.length > 1;
                  return (
                    <View
                      key={`${identity.provider}:${identity.email || "none"}`}
                      style={{
                        flexDirection: "row",
                        alignItems: "center",
                        justifyContent: "space-between",
                        borderTopWidth: 1,
                        borderTopColor: c.border,
                        paddingVertical: 10,
                      }}
                    >
                      <View style={{ flexDirection: "row", alignItems: "center", flex: 1, marginRight: 10, gap: 10 }}>
                        {providerBadge(identity.provider, "sm")}
                        <View style={{ flex: 1 }}>
                        <Text style={{ color: c.textPrimary, fontSize: 14, fontWeight: "600", textTransform: "capitalize" }}>
                          {identity.provider}
                        </Text>
                        <Text style={{ color: c.textMuted, fontSize: 12, marginTop: 2 }} numberOfLines={1}>
                          {identity.email || "No email reported"}
                        </Text>
                        </View>
                      </View>
                      {identity.isPrimary ? (
                        <View style={{ borderWidth: 1, borderColor: c.success, borderRadius: 999, paddingHorizontal: 8, paddingVertical: 3, marginRight: 6 }}>
                          <Text style={{ color: c.success, fontSize: 10, letterSpacing: 0.6 }}>PRIMARY</Text>
                        </View>
                      ) : null}
                      <Pressable
                        onPress={() => handleUnlinkProvider(identity.provider)}
                        disabled={!canUnlink || unlinkingProvider === identity.provider}
                        style={{
                          borderWidth: 1,
                          borderColor: c.border,
                          borderRadius: 999,
                          paddingHorizontal: 10,
                          paddingVertical: 5,
                          opacity: canUnlink ? 1 : 0.35,
                        }}
                      >
                        <Text style={{ color: canUnlink ? c.error : c.textMuted, fontSize: 11, letterSpacing: 0.4 }}>
                          {unlinkingProvider === identity.provider ? "…" : "UNLINK"}
                        </Text>
                      </Pressable>
                    </View>
                  );
                })}
              </View>
            )}
            <View style={{ marginTop: 14, borderTopWidth: 1, borderTopColor: c.border, paddingTop: 14 }}>
              <Text style={{ color: c.textPrimary, fontSize: 14, fontWeight: "700" }}>Email / Password</Text>
              <Text style={{ color: c.textMuted, fontSize: 12, marginTop: 6, lineHeight: 18 }}>
                Add a password credential to this same account for automated web, redroid, and simulator tests. Keep the raw password in the local keychain or GitHub Secrets; Yaver stores only a password hash.
              </Text>
              {!emailPasswordEnabled ? (
                <Text style={{ color: c.textMuted, fontSize: 12, marginTop: 10, lineHeight: 18 }}>
                  Email/password sign-in is closed on this deployment. Open it only for a test window with yaver set emailOauth enable and an allowed-email list. Convex env stores only the gate and allowlist, never the raw password.
                </Text>
              ) : hasEmailPassword ? (
                <Text style={{ color: c.success, fontSize: 12, marginTop: 10, lineHeight: 18 }}>
                  Email/password is linked to this account. Tests can sign in with {user?.email || "this email"} while the flag stays enabled.
                </Text>
              ) : (
                <View style={{ gap: 10, marginTop: 12 }}>
                  <Text style={{ color: c.textMuted, fontSize: 12, lineHeight: 18 }}>
                    This links email/password to the same account. Other users and runners cannot fetch the credential; the server stores only a salted hash.
                  </Text>
                  <TextInput
                    value={newAccountPassword}
                    onChangeText={setNewAccountPassword}
                    placeholder="New automation password"
                    placeholderTextColor={c.textMuted}
                    secureTextEntry
                    autoCapitalize="none"
                    autoCorrect={false}
                    style={{
                      borderWidth: 1,
                      borderColor: c.border,
                      borderRadius: 10,
                      paddingHorizontal: 12,
                      paddingVertical: 11,
                      color: c.textPrimary,
                      backgroundColor: c.bgCardElevated,
                    }}
                  />
                  <TextInput
                    value={confirmAccountPassword}
                    onChangeText={setConfirmAccountPassword}
                    placeholder="Confirm password"
                    placeholderTextColor={c.textMuted}
                    secureTextEntry
                    autoCapitalize="none"
                    autoCorrect={false}
                    style={{
                      borderWidth: 1,
                      borderColor: c.border,
                      borderRadius: 10,
                      paddingHorizontal: 12,
                      paddingVertical: 11,
                      color: c.textPrimary,
                      backgroundColor: c.bgCardElevated,
                    }}
                  />
                  {accountPasswordError ? (
                    <Text style={{ color: c.error, fontSize: 12 }}>{accountPasswordError}</Text>
                  ) : null}
                  {accountPasswordMessage ? (
                    <Text style={{ color: c.success, fontSize: 12 }}>{accountPasswordMessage}</Text>
                  ) : null}
                  <Pressable
                    onPress={handleSetAccountPassword}
                    disabled={settingAccountPassword}
                    style={{
                      borderWidth: 1,
                      borderColor: c.border,
                      borderRadius: 10,
                      paddingVertical: 11,
                      alignItems: "center",
                      justifyContent: "center",
                      opacity: settingAccountPassword ? 0.6 : 1,
                      backgroundColor: c.bgCardElevated,
                    }}
                  >
                    <Text style={{ color: c.textPrimary, fontSize: 13, fontWeight: "700" }}>
                      {settingAccountPassword ? "Saving..." : "Enable email/password on this account"}
                    </Text>
                  </Pressable>
                </View>
              )}
            </View>
            <View style={{ marginTop: 14, flexDirection: "row", flexWrap: "wrap", gap: 8 }}>
              {(["apple", "github", "gitlab", "google", "microsoft"] as const).map((provider) => {
                const already = identities.some((i) => i.provider === provider);
                const disabled = linkingProvider !== null || already;
                return (
                  <Pressable
                    key={provider}
                    onPress={() => handleLinkProvider(provider)}
                    disabled={disabled}
                    style={{
                      flexGrow: 1,
                      borderWidth: 1,
                      borderColor: c.border,
                      borderRadius: 10,
                      paddingVertical: 11,
                      paddingHorizontal: 12,
                      alignItems: "center",
                      justifyContent: "center",
                      flexDirection: "row",
                      gap: 8,
                      opacity: disabled ? 0.45 : 1,
                      backgroundColor: c.bgCardElevated,
                    }}
                  >
                    {providerBadge(provider, "sm")}
                    <Text style={{ color: c.textPrimary, fontSize: 13, fontWeight: "600", textTransform: "capitalize" }}>
                      {already ? `${provider} linked` : linkingProvider === provider ? "Opening…" : `Connect ${provider}`}
                    </Text>
                  </Pressable>
                );
              })}
            </View>
          </View>

          <View style={[styles.card, { backgroundColor: c.bgCard, borderColor: c.border, padding: 14, marginTop: 12 }]}>
            <Text style={{ color: c.textPrimary, fontSize: 15, fontWeight: "600" }}>Merge another account</Text>
            <Text style={{ color: c.textMuted, fontSize: 12, marginTop: 6, lineHeight: 18 }}>
              Accidentally created two Yaver accounts? Merge them into this one. You&apos;ll get an approval URL to open on any browser where the OTHER account is signed in.
            </Text>
            {!mergeIntent ? (
              <Pressable
                onPress={handleStartMerge}
                disabled={mergeStarting}
                style={{
                  marginTop: 12,
                  borderWidth: 1,
                  borderColor: c.border,
                  borderRadius: 10,
                  paddingVertical: 11,
                  alignItems: "center",
                  opacity: mergeStarting ? 0.5 : 1,
                  backgroundColor: c.bgCardElevated,
                }}
              >
                <Text style={{ color: c.textPrimary, fontSize: 13, fontWeight: "600" }}>
                  {mergeStarting ? "Starting…" : "Start merge"}
                </Text>
              </Pressable>
            ) : (
              <View style={{ marginTop: 12 }}>
                <Text style={{ color: c.textMuted, fontSize: 11, letterSpacing: 0.4, textTransform: "uppercase" }}>Approval URL</Text>
                <Text selectable style={{ color: c.textPrimary, fontSize: 12, marginTop: 6, fontFamily: Platform.OS === "ios" ? "Menlo" : "monospace" }}>
                  {mergeIntent.approvalUrl}
                </Text>
                <View style={{ flexDirection: "row", marginTop: 12, gap: 8 }}>
                  <Pressable
                    onPress={handleOpenMergeUrl}
                    style={{
                      flex: 1,
                      borderWidth: 1,
                      borderColor: c.border,
                      borderRadius: 10,
                      paddingVertical: 11,
                      alignItems: "center",
                      backgroundColor: c.bgCardElevated,
                    }}
                  >
                    <Text style={{ color: c.textPrimary, fontSize: 13, fontWeight: "600" }}>Open in browser</Text>
                  </Pressable>
                  <Pressable
                    onPress={handleCopyMergeUrl}
                    style={{
                      flex: 1,
                      borderWidth: 1,
                      borderColor: c.border,
                      borderRadius: 10,
                      paddingVertical: 11,
                      alignItems: "center",
                    }}
                  >
                    <Text style={{ color: c.textPrimary, fontSize: 13, fontWeight: "600" }}>
                      {mergeUrlCopied ? "Copied" : "Copy URL"}
                    </Text>
                  </Pressable>
                  <Pressable
                    onPress={handleCancelMerge}
                    style={{
                      flex: 1,
                      borderWidth: 1,
                      borderColor: c.border,
                      borderRadius: 10,
                      paddingVertical: 11,
                      alignItems: "center",
                    }}
                  >
                    <Text style={{ color: c.error, fontSize: 13, fontWeight: "600" }}>Cancel</Text>
                  </Pressable>
                </View>
                <Text style={{ color: c.textMuted, fontSize: 11, marginTop: 10 }}>
                  Expires {new Date(mergeIntent.expiresAt).toLocaleTimeString()}
                </Text>
              </View>
            )}
          </View>
        </View>

        {/* Email verification — surfaced only when emailVerified is
            false. Clicking the link in the verification email flips
            users.emailVerified=true on the backend, which unlocks
            email-keyed OAuth auto-linking: signing in with Apple /
            Google / etc. that returns the same address links the new
            identity to this account instead of creating a duplicate. */}
        {user && user.emailVerified === false && user.email ? (
          <View style={styles.section}>
            <Text style={[styles.sectionLabel, { color: c.textMuted }]}>Email verification</Text>
            <View style={[styles.card, { backgroundColor: c.bgCard, borderColor: c.border, padding: 14 }]}>
              <Text style={{ color: c.textMuted, fontSize: 12, lineHeight: 18 }}>
                Verify {user.email} so signing in with Apple, Google, GitHub, GitLab, or Microsoft can attach to this account automatically (instead of creating a parallel one).
              </Text>
              {verifyEmailMessage ? (
                <Text
                  style={{
                    color: verifyEmailMessage.startsWith("✓") ? "#10b981" : c.error,
                    fontSize: 12,
                    marginTop: 10,
                  }}
                >
                  {verifyEmailMessage}
                </Text>
              ) : null}
              <Pressable
                style={({ pressed }) => [
                  {
                    marginTop: 12,
                    paddingVertical: 10,
                    paddingHorizontal: 14,
                    borderRadius: 10,
                    borderWidth: 1,
                    borderColor: c.accent + "60",
                    backgroundColor: c.accent + "15",
                    alignItems: "center",
                  },
                  pressed && { opacity: 0.8 },
                  verifyEmailBusy && { opacity: 0.6 },
                ]}
                onPress={handleRequestVerifyEmail}
                disabled={verifyEmailBusy}
              >
                <Text style={{ color: c.accent, fontWeight: "600", fontSize: 14 }}>
                  {verifyEmailBusy ? "Sending…" : "Send verification email"}
                </Text>
              </Pressable>
            </View>
          </View>
        ) : null}

        {/* Passkeys — sign-in fast next time. Lets the currently
            signed-in user (regardless of how they got here — Apple,
            Google, email/password, or another passkey) add a new
            passkey to their account. iCloud Keychain / Google
            Password Manager syncs across the user's devices, so one
            enrollment from this phone surfaces on macOS Safari, Mac
            Yaver, etc. */}
        <View style={styles.section}>
          <Text style={[styles.sectionLabel, { color: c.textMuted }]}>Passkeys</Text>
          <View style={[styles.card, { backgroundColor: c.bgCard, borderColor: c.border, padding: 14 }]}>
            <Text style={{ color: c.textMuted, fontSize: 12, lineHeight: 18 }}>
              Add a passkey to sign in faster next time — no password, just Face ID / Touch ID. You stay signed in everywhere you already are.
            </Text>
            {passkeyEnrollMessage ? (
              <Text
                style={{
                  color: passkeyEnrollMessage.startsWith("✓") ? "#10b981" : c.error,
                  fontSize: 12,
                  marginTop: 10,
                }}
              >
                {passkeyEnrollMessage}
              </Text>
            ) : null}
            <Pressable
              style={({ pressed }) => [
                {
                  marginTop: 12,
                  paddingVertical: 10,
                  paddingHorizontal: 14,
                  borderRadius: 10,
                  borderWidth: 1,
                  borderColor: c.accent + "60",
                  backgroundColor: c.accent + "15",
                  alignItems: "center",
                },
                pressed && { opacity: 0.8 },
                enrollingPasskey && { opacity: 0.6 },
              ]}
              onPress={handleAddPasskey}
              disabled={enrollingPasskey}
            >
              <Text style={{ color: c.accent, fontWeight: "600", fontSize: 14 }}>
                {enrollingPasskey ? "Waiting for passkey..." : "Add a passkey"}
              </Text>
            </Pressable>
          </View>
        </View>

        {/* Security — optional two-factor authentication */}
        <View style={styles.section}>
          <Text style={[styles.sectionLabel, { color: c.textMuted }]}>Security</Text>
          <Pressable
            style={[styles.card, { backgroundColor: c.bgCard, borderColor: c.border, padding: 14 }]}
            onPress={() => router.push("/two-factor-setup")}
          >
            <Text style={{ color: c.textPrimary, fontSize: 15, fontWeight: "600" }}>
              Two-factor authentication
            </Text>
            <Text style={{ color: c.textMuted, fontSize: 12, marginTop: 4 }}>
              Optional. Adds a 6-digit code at sign-in. Works with Microsoft Authenticator, Google Authenticator, 1Password, Authy…
            </Text>
          </Pressable>
        </View>

        {/* Connected device */}
        <View style={styles.section}>
          <Text style={[styles.sectionLabel, { color: c.textMuted }]}>Connected Device</Text>
          {activeDevice ? (
            <View style={[styles.card, { backgroundColor: c.bgCard, borderColor: c.border }]}>
              <View style={styles.deviceRow}>
                <View style={styles.deviceInfo}>
                  <Text style={[styles.deviceName, { color: c.textPrimary }]}>{activeDevice.name}</Text>
                  <Text style={[styles.deviceMeta, { color: c.textMuted }]}>
                    {activeDevice.os} &middot; {activeDevice.host}:{activeDevice.port}
                  </Text>
                </View>
                <View
                  style={[
                    styles.connectionDot,
                    {
                      backgroundColor:
                        connectionStatus === "connected"
                          ? c.success
                          : connectionStatus === "connecting"
                            ? c.warn
                            : connectionStatus === "error"
                              ? c.error
                              : c.textMuted,
                    },
                  ]}
                />
              </View>
              <View style={[styles.deviceDetails, { borderTopColor: c.borderSubtle }]}>
                <View style={styles.detailItem}>
                  <Text style={[styles.detailLabel, { color: c.textMuted }]}>Status</Text>
                  <Text style={[styles.detailValue, { color: c.textPrimary }]}>{connectionStatus}</Text>
                </View>
                <View style={styles.detailItem}>
                  <Text style={[styles.detailLabel, { color: c.textMuted }]}>Mode</Text>
                  <Text style={[styles.detailValue, { color: c.textPrimary }]}>
                    {quicClient.connectionMode || "—"}
                  </Text>
                </View>
                {agentVersion && (
                  <View style={styles.detailItem}>
                    <Text style={[styles.detailLabel, { color: c.textMuted }]}>Agent</Text>
                    <Text style={[styles.detailValue, { color: c.textPrimary }]}>v{agentVersion}</Text>
                  </View>
                )}
                <View style={styles.detailItem}>
                  <Text style={[styles.detailLabel, { color: c.textMuted }]}>Last seen</Text>
                  <Text style={[styles.detailValue, { color: c.textPrimary }]}>
                    {agentLastPing
                      ? agentLastPing.toLocaleTimeString(undefined, { hour: "2-digit", minute: "2-digit" })
                      : activeDevice.lastSeen
                        ? new Date(activeDevice.lastSeen).toLocaleString(undefined, { month: "short", day: "numeric", hour: "2-digit", minute: "2-digit" })
                        : "Unknown"}
                  </Text>
                </View>
              </View>
              {/* Ping + Shutdown row */}
              <View style={[styles.deviceDetails, { borderTopColor: c.borderSubtle }]}>
                <Pressable
                  style={({ pressed }) => [
                    { flexDirection: "row", alignItems: "center", paddingVertical: 4, paddingHorizontal: 8, borderRadius: 6, backgroundColor: c.bgCardElevated },
                    pressed && { opacity: 0.7 },
                  ]}
                  onPress={handlePing}
                  disabled={isPinging}
                >
                  <Text style={{ fontSize: 13, color: c.accent }}>
                    {isPinging ? "Pinging..." : pingRtt !== null ? `${pingRtt}ms` : "Ping"}
                  </Text>
                </Pressable>
                <Pressable
                  style={({ pressed }) => [
                    { paddingVertical: 4, paddingHorizontal: 8, borderRadius: 6, backgroundColor: c.errorBg },
                    pressed && { opacity: 0.7 },
                  ]}
                  onPress={handleShutdownAgent}
                  disabled={isShuttingDown}
                >
                  <Text style={{ fontSize: 13, color: c.error }}>
                    {isShuttingDown ? "Stopping..." : "Shutdown"}
                  </Text>
                </Pressable>
              </View>
              {/* Runner status */}
              {agentStatus && (
                <View style={[styles.deviceDetails, { borderTopColor: c.borderSubtle }]}>
                  <View style={styles.detailItem}>
                    <Text style={[styles.detailLabel, { color: c.textMuted }]}>Runner</Text>
                    <Text style={[styles.detailValue, { color: c.textPrimary }]}>
                      {agentStatus.runner.name}
                    </Text>
                  </View>
                  <View style={styles.detailItem}>
                    <Text style={[styles.detailLabel, { color: c.textMuted }]}>Status</Text>
                    <Text style={[styles.detailValue, {
                      color: agentStatus.runner.installed ? c.success : c.error,
                    }]}>
                      {agentStatus.runner.installed ? "Ready" : "Not found"}
                    </Text>
                  </View>
                  <View style={styles.detailItem}>
                    <Text style={[styles.detailLabel, { color: c.textMuted }]}>Tasks</Text>
                    <Text style={[styles.detailValue, { color: c.textPrimary }]}>
                      {agentStatus.runningTasks}/{agentStatus.totalTasks}
                    </Text>
                  </View>
                </View>
              )}
            </View>
          ) : (
            <View style={[styles.card, { backgroundColor: c.bgCard, borderColor: c.border }]}>
              <Text style={[styles.noDeviceText, { color: c.textMuted }]}>
                No device connected. Go to the Devices tab to connect.
              </Text>
            </View>
          )}
        </View>

        <View style={styles.section}>
          <Pressable
            onPress={() => setShowToolchainSync((prev) => !prev)}
            style={[styles.card, { backgroundColor: c.bgCard, borderColor: c.border }]}
          >
            <View style={styles.themeRow}>
              <View style={{ flex: 1 }}>
                <Text style={[styles.themeLabel, { color: c.textPrimary }]}>Toolchain Sync</Text>
                <Text style={{ fontSize: 11, color: c.textMuted, marginTop: 2 }}>
                  Clone tools, runner setup, provider-key state, and Git integration from one Yaver machine to another. No user files are copied.
                </Text>
              </View>
              <Text style={{ color: c.textMuted }}>{showToolchainSync ? "\u25B2" : "\u25BC"}</Text>
            </View>
          </Pressable>

          {showToolchainSync && (
            <View style={[styles.card, { backgroundColor: c.bgCard, borderColor: c.border, marginTop: 4, padding: 16 }]}>
              {connectionStatus !== "connected" || !activeDevice ? (
                <Text style={{ color: c.textMuted, fontSize: 13 }}>
                  Connect to the target machine first. Toolchain Sync always applies to the machine currently connected in Yaver.
                </Text>
              ) : activeDevice.isGuest ? (
                <Text style={{ color: c.textMuted, fontSize: 13 }}>
                  Toolchain Sync is owner-only. Connect to one of your own machines to use it.
                </Text>
              ) : toolchainSourceCandidates.length === 0 ? (
                <Text style={{ color: c.textMuted, fontSize: 13 }}>
                  No source machine available yet. Bring your development Mac/Linux box online in Yaver, then come back and sync its toolchain into this target.
                </Text>
              ) : (
                <>
                  <Text style={{ color: c.textMuted, fontSize: 11, textTransform: "uppercase", fontWeight: "700" }}>
                    Target
                  </Text>
                  <Text style={{ color: c.textPrimary, fontSize: 14, fontWeight: "600", marginTop: 4 }}>
                    {activeDevice.name}
                  </Text>
                  <Text style={{ color: c.textMuted, fontSize: 12, marginTop: 2 }}>
                    Connected target machine. Changes apply here.
                  </Text>

                  <Text style={{ color: c.textMuted, fontSize: 11, textTransform: "uppercase", fontWeight: "700", marginTop: 16 }}>
                    Source
                  </Text>
                  <View style={{ flexDirection: "row", flexWrap: "wrap", gap: 8, marginTop: 10 }}>
                    {toolchainSourceCandidates.map((device) => {
                      const selected = selectedToolchainSource?.id === device.id;
                      return (
                        <Pressable
                          key={device.id}
                          onPress={() => {
                            setToolchainSourceId(device.id);
                            setToolchainPreview(null);
                          }}
                          style={{
                            paddingHorizontal: 12,
                            paddingVertical: 10,
                            borderRadius: 10,
                            borderWidth: 1,
                            borderColor: selected ? c.accent : c.border,
                            backgroundColor: selected ? `${c.accent}22` : c.bg,
                            minWidth: 150,
                          }}
                        >
                          <Text style={{ color: selected ? c.accent : c.textPrimary, fontWeight: "600", fontSize: 13 }}>
                            {device.name}
                          </Text>
                          <Text style={{ color: c.textMuted, fontSize: 11, marginTop: 3 }}>
                            {device.online ? "Online" : "Offline"} · {device.os || "machine"}
                          </Text>
                        </Pressable>
                      );
                    })}
                  </View>

                  <Text style={{ color: c.textMuted, fontSize: 11, textTransform: "uppercase", fontWeight: "700", marginTop: 18 }}>
                    Options
                  </Text>
                  <View style={{ marginTop: 10, gap: 10 }}>
                    {[
                      {
                        label: "Install missing tools",
                        value: toolchainInstallMissing,
                        onValueChange: (value: boolean) => setToolchainInstallMissing(value),
                        hint: "Apply missing Linux-safe tools and runners onto the target box.",
                      },
                      {
                        label: "Sync Git credentials",
                        value: toolchainSyncGitCredentials,
                        onValueChange: (value: boolean) => setToolchainSyncGitCredentials(value),
                        hint: "Copy saved Git host credentials so clone, pull, push, and deploy flows keep working.",
                      },
                      {
                        label: "Sync provider keys state",
                        value: toolchainSyncProviderKeys,
                        onValueChange: (value: boolean) => setToolchainSyncProviderKeys(value),
                        hint: "Carry Yaver-synced provider-key state between devices.",
                      },
                      {
                        label: "Sync presets",
                        value: toolchainSyncPresets,
                        onValueChange: (value: boolean) => setToolchainSyncPresets(value),
                        hint: "Bring tooling presets and machine-level defaults across.",
                      },
                      {
                        label: "Sync flags",
                        value: toolchainSyncFlags,
                        onValueChange: (value: boolean) => setToolchainSyncFlags(value),
                        hint: "Include feature flags and experimental toggles.",
                      },
                      {
                        label: "Sync env entries",
                        value: toolchainSyncEnv,
                        onValueChange: (value: boolean) => setToolchainSyncEnv(value),
                        hint: "Include synced env-store entries. Use carefully if machines should differ.",
                      },
                      {
                        label: "Sync monitors",
                        value: toolchainSyncMonitors,
                        onValueChange: (value: boolean) => setToolchainSyncMonitors(value),
                        hint: "Bring health monitor definitions across.",
                      },
                      {
                        label: "Remove missing synced items",
                        value: toolchainRemoveMissing,
                        onValueChange: (value: boolean) => setToolchainRemoveMissing(value),
                        hint: "Remove missing Git hosts and show target-only tools that need manual cleanup.",
                      },
                    ].map((item) => (
                      <View key={item.label} style={[styles.themeRow, { alignItems: "flex-start" }]}>
                        <View style={{ flex: 1, paddingRight: 12 }}>
                          <Text style={[styles.themeLabel, { color: c.textPrimary }]}>{item.label}</Text>
                          <Text style={{ color: c.textMuted, fontSize: 11, marginTop: 2 }}>{item.hint}</Text>
                        </View>
                        <Switch value={item.value} onValueChange={item.onValueChange} trackColor={{ true: c.accent }} />
                      </View>
                    ))}
                  </View>

                  <View style={{ flexDirection: "row", gap: 8, marginTop: 16 }}>
                    <Pressable
                      onPress={previewToolchainSync}
                      disabled={isPreviewingToolchainSync || isApplyingToolchainSync}
                      style={({ pressed }) => [
                        {
                          flex: 1,
                          paddingVertical: 10,
                          borderRadius: 8,
                          borderWidth: 1,
                          borderColor: c.border,
                          backgroundColor: c.bg,
                          alignItems: "center",
                          opacity: isPreviewingToolchainSync || isApplyingToolchainSync ? 0.5 : 1,
                        },
                        pressed && { opacity: 0.7 },
                      ]}
                    >
                      <Text style={{ color: c.textPrimary, fontWeight: "600", fontSize: 14 }}>
                        {isPreviewingToolchainSync ? "Previewing..." : "Preview Sync"}
                      </Text>
                    </Pressable>
                    <Pressable
                      onPress={applyToolchainSyncNow}
                      disabled={isApplyingToolchainSync || isPreviewingToolchainSync}
                      style={({ pressed }) => [
                        {
                          flex: 1,
                          paddingVertical: 10,
                          borderRadius: 8,
                          backgroundColor: c.accent,
                          alignItems: "center",
                          opacity: isApplyingToolchainSync || isPreviewingToolchainSync ? 0.5 : 1,
                        },
                        pressed && { opacity: 0.7 },
                      ]}
                    >
                      <Text style={{ color: "#fff", fontWeight: "600", fontSize: 14 }}>
                        {isApplyingToolchainSync ? "Syncing..." : "Apply Sync"}
                      </Text>
                    </Pressable>
                  </View>

                  {toolchainPreview && (
                    <View
                      style={{
                        marginTop: 16,
                        padding: 12,
                        borderRadius: 10,
                        borderWidth: 1,
                        borderColor: c.border,
                        backgroundColor: c.bg,
                        gap: 6,
                      }}
                    >
                      <Text style={{ color: c.textPrimary, fontWeight: "700", fontSize: 13 }}>
                        Last Preview
                      </Text>
                      <Text style={{ color: c.textMuted, fontSize: 12 }}>
                        Status: {toolchainPreview.status} · target {toolchainPreview.targetPlatform}
                      </Text>
                      {(toolchainPreview.installPlan?.length ?? 0) > 0 ? (
                        <Text style={{ color: c.textMuted, fontSize: 12 }}>
                          Install plan: {toolchainPreview.installPlan!.join(", ")}
                        </Text>
                      ) : null}
                      {(toolchainPreview.importedSyncKinds?.length ?? 0) > 0 ? (
                        <Text style={{ color: c.textMuted, fontSize: 12 }}>
                          Sync kinds: {toolchainPreview.importedSyncKinds!.join(", ")}
                        </Text>
                      ) : null}
                      {(toolchainPreview.importedGitHosts?.length ?? 0) > 0 ? (
                        <Text style={{ color: c.textMuted, fontSize: 12 }}>
                          Git hosts: {toolchainPreview.importedGitHosts!.join(", ")}
                        </Text>
                      ) : null}
                      {(toolchainPreview.removalPlan?.length ?? 0) > 0 ? (
                        <Text style={{ color: c.warn, fontSize: 12 }}>
                          Manual cleanup: {toolchainPreview.removalPlan!.join(", ")}
                        </Text>
                      ) : null}
                      {(toolchainPreview.manualSteps?.length ?? 0) > 0 ? (
                        <Text style={{ color: c.warn, fontSize: 12 }}>
                          Manual: {toolchainPreview.manualSteps![0]}
                        </Text>
                      ) : null}
                    </View>
                  )}
                </>
              )}
            </View>
          )}
        </View>

        {/* Feedback SDK */}
        <View style={styles.section}>
          <Pressable
            onPress={() => setShowFeedbackSDK(!showFeedbackSDK)}
            style={[styles.card, { backgroundColor: c.bgCard, borderColor: c.border }]}
          >
            <View style={styles.themeRow}>
              <View style={{ flex: 1 }}>
                <Text style={[styles.themeLabel, { color: c.textPrimary }]}>Feedback SDK</Text>
                <Text style={{ fontSize: 11, color: c.textMuted, marginTop: 2 }}>
                  {feedbackEnabled ? `Enabled (${feedbackTrigger}, ${feedbackMode})` : "Disabled"}
                </Text>
              </View>
              <Text style={{ color: c.textMuted }}>{showFeedbackSDK ? "\u25B2" : "\u25BC"}</Text>
            </View>
          </Pressable>

          {showFeedbackSDK && (
            <View style={[styles.card, { backgroundColor: c.bgCard, borderColor: c.border, marginTop: 4, padding: 16 }]}>
              <View style={[styles.themeRow, { marginBottom: 16 }]}>
                <Text style={[styles.themeLabel, { color: c.textPrimary }]}>Enable Feedback SDK</Text>
                <Switch
                  value={feedbackEnabled}
                  onValueChange={async (val) => {
                    setFeedbackEnabled(val);
                    const fbKey = user?.id ? `@yaver/u/${user.id}/feedback_config` : "@yaver/feedback_config";
                    const cfg = { enabled: val, trigger: feedbackTrigger, feedbackMode, blackBox: blackBoxEnabled, voiceEnabled: feedbackVoice, speechProvider, ttsProvider };
                    await AsyncStorage.setItem(fbKey, JSON.stringify(cfg));
                  }}
                  trackColor={{ true: c.accent }}
                />
              </View>

              {feedbackEnabled && (
                <>
                  <Text style={[styles.sectionLabel, { color: c.textMuted, marginBottom: 8 }]}>Trigger</Text>
                  <View style={{ flexDirection: "row", gap: 8, marginBottom: 16 }}>
                    {(["shake", "floating-button", "manual"] as const).map((t) => (
                      <Pressable
                        key={t}
                        onPress={async () => {
                          setFeedbackTrigger(t);
                          const fbKey = user?.id ? `@yaver/u/${user.id}/feedback_config` : "@yaver/feedback_config";
                          const cfg = { enabled: feedbackEnabled, trigger: t, feedbackMode, blackBox: blackBoxEnabled, voiceEnabled: feedbackVoice, speechProvider, ttsProvider };
                          await AsyncStorage.setItem(fbKey, JSON.stringify(cfg));
                        }}
                        style={{
                          flex: 1, paddingVertical: 8, borderRadius: 8, alignItems: "center" as const,
                          backgroundColor: feedbackTrigger === t ? c.accent + "30" : c.bgInput,
                          borderWidth: 1, borderColor: feedbackTrigger === t ? c.accent : c.border,
                        }}
                      >
                        <Text style={{ fontSize: 12, color: feedbackTrigger === t ? c.accent : c.textSecondary }}>
                          {t === "floating-button" ? "Float" : t === "shake" ? "Shake" : "Manual"}
                        </Text>
                      </Pressable>
                    ))}
                  </View>

                  <Text style={[styles.sectionLabel, { color: c.textMuted, marginBottom: 8 }]}>Mode</Text>
                  <View style={{ flexDirection: "row", gap: 8, marginBottom: 16 }}>
                    {(["live", "narrated", "batch"] as const).map((m) => (
                      <Pressable
                        key={m}
                        onPress={async () => {
                          setFeedbackMode(m);
                          const fbKey = user?.id ? `@yaver/u/${user.id}/feedback_config` : "@yaver/feedback_config";
                          const cfg = { enabled: feedbackEnabled, trigger: feedbackTrigger, feedbackMode: m, blackBox: blackBoxEnabled, voiceEnabled: feedbackVoice, speechProvider, ttsProvider };
                          await AsyncStorage.setItem(fbKey, JSON.stringify(cfg));
                        }}
                        style={{
                          flex: 1, paddingVertical: 8, borderRadius: 8, alignItems: "center" as const,
                          backgroundColor: feedbackMode === m ? c.accent + "30" : c.bgInput,
                          borderWidth: 1, borderColor: feedbackMode === m ? c.accent : c.border,
                        }}
                      >
                        <Text style={{ fontSize: 12, color: feedbackMode === m ? c.accent : c.textSecondary }}>
                          {m.charAt(0).toUpperCase() + m.slice(1)}
                        </Text>
                      </Pressable>
                    ))}
                  </View>

                  <View style={[styles.themeRow, { marginBottom: 16 }]}>
                    <View style={{ flex: 1 }}>
                      <Text style={[styles.themeLabel, { color: c.textPrimary }]}>Black Box Streaming</Text>
                      <Text style={{ fontSize: 11, color: c.textMuted, marginTop: 2 }}>Stream logs, crashes, navigation to agent</Text>
                    </View>
                    <Switch
                      value={blackBoxEnabled}
                      onValueChange={async (val) => {
                        setBlackBoxEnabled(val);
                        const fbKey = user?.id ? `@yaver/u/${user.id}/feedback_config` : "@yaver/feedback_config";
                        const cfg = { enabled: feedbackEnabled, trigger: feedbackTrigger, feedbackMode, blackBox: val, voiceEnabled: feedbackVoice, speechProvider, ttsProvider };
                        await AsyncStorage.setItem(fbKey, JSON.stringify(cfg));
                      }}
                      trackColor={{ true: c.accent }}
                    />
                  </View>

                  <View style={[styles.themeRow, { marginBottom: 8 }]}>
                    <View style={{ flex: 1 }}>
                      <Text style={[styles.themeLabel, { color: c.textPrimary }]}>Voice Input</Text>
                      <Text style={{ fontSize: 11, color: c.textMuted, marginTop: 2 }}>
                        Uses Settings Voice engine: {speechProvider === "openai" || ttsProvider === "openai" ? "OpenAI" : "Local"}
                      </Text>
                    </View>
                    <Switch
                      value={feedbackVoice}
                      onValueChange={async (val) => {
                        setFeedbackVoice(val);
                        const fbKey = user?.id ? `@yaver/u/${user.id}/feedback_config` : "@yaver/feedback_config";
                        const cfg = { enabled: feedbackEnabled, trigger: feedbackTrigger, feedbackMode, blackBox: blackBoxEnabled, voiceEnabled: val, speechProvider, ttsProvider };
                        await AsyncStorage.setItem(fbKey, JSON.stringify(cfg));
                      }}
                      trackColor={{ true: c.accent }}
                    />
                  </View>

                  <Text style={[styles.sectionLabel, { color: c.textMuted, marginTop: 12, marginBottom: 8 }]}>Button Color</Text>
                  <View style={{ flexDirection: "row", gap: 8 }}>
                    {["#6366f1", "#ec4899", "#22c55e", "#f59e0b", "#ef4444", "#8b5cf6", "#1a1a1a", "#333333"].map((color) => (
                      <Pressable
                        key={color}
                        onPress={async () => {
                          setFeedbackButtonColor(color);
                          const fbKey = user?.id ? `@yaver/u/${user.id}/feedback_config` : "@yaver/feedback_config";
                          const raw = await AsyncStorage.getItem(fbKey);
                          const cfg = raw ? JSON.parse(raw) : {};
                          cfg.buttonColor = color;
                          await AsyncStorage.setItem(fbKey, JSON.stringify(cfg));
                        }}
                        style={{
                          width: 32, height: 32, borderRadius: 16,
                          backgroundColor: color, borderWidth: 2,
                          borderColor: feedbackButtonColor === color ? "#fff" : "transparent",
                        }}
                      />
                    ))}
                  </View>

                  <Text style={{ fontSize: 11, color: c.textMuted, marginTop: 12 }}>
                    The debug button appears as a draggable &gt;_ terminal icon.
                    Tap to expand the console. Send tasks, trigger hot reload, or disable the SDK.
                  </Text>
                </>
              )}
            </View>
          )}
        </View>

        {/* Device Metrics */}
        {!LEAN_SETTINGS_SURFACE && activeDevice && connectionStatus === "connected" && (
          <View style={styles.section}>
            <Pressable onPress={() => setShowMetrics(!showMetrics)}>
              <Text style={[styles.sectionLabel, { color: c.textMuted }]}>
                Device Metrics {showMetrics ? "\u2303" : "\u2304"}
              </Text>
            </Pressable>
            {showMetrics && (
              <View style={[styles.card, { backgroundColor: c.bgCard, borderColor: c.border }]}>
                {metrics.length === 0 ? (
                  <Text style={[{ fontSize: 13, color: c.textMuted, textAlign: "center", paddingVertical: 12 }]}>
                    Waiting for metrics... (updates every 60s)
                  </Text>
                ) : (
                  <>
                    {/* CPU Chart */}
                    <Text style={[styles.detailLabel, { color: c.textMuted, marginBottom: 6 }]}>
                      CPU — {metrics.length > 0 ? `${metrics[metrics.length - 1].cpuPercent.toFixed(1)}%` : "—"}
                    </Text>
                    <View style={metricsStyles.chartContainer}>
                      {metrics.slice(-60).map((m, i) => (
                        <View
                          key={i}
                          style={[
                            metricsStyles.bar,
                            {
                              height: `${Math.max(m.cpuPercent, 2)}%` as any,
                              backgroundColor: m.cpuPercent > 80 ? c.error : m.cpuPercent > 50 ? c.warn : c.accent,
                            },
                          ]}
                        />
                      ))}
                    </View>

                    {/* RAM Chart */}
                    <Text style={[styles.detailLabel, { color: c.textMuted, marginBottom: 6, marginTop: 16 }]}>
                      RAM — {metrics.length > 0
                        ? `${(metrics[metrics.length - 1].memoryUsedMb / 1024).toFixed(1)} / ${(metrics[metrics.length - 1].memoryTotalMb / 1024).toFixed(1)} GB`
                        : "—"}
                    </Text>
                    <View style={metricsStyles.chartContainer}>
                      {metrics.slice(-60).map((m, i) => {
                        const pct = m.memoryTotalMb > 0 ? (m.memoryUsedMb / m.memoryTotalMb) * 100 : 0;
                        return (
                          <View
                            key={i}
                            style={[
                              metricsStyles.bar,
                              {
                                height: `${Math.max(pct, 2)}%` as any,
                                backgroundColor: pct > 85 ? c.error : pct > 60 ? c.warn : c.success,
                              },
                            ]}
                          />
                        );
                      })}
                    </View>

                    {/* Time range label */}
                    <View style={metricsStyles.timeLabels}>
                      <Text style={[{ fontSize: 10, color: c.textMuted }]}>-60 min</Text>
                      <Text style={[{ fontSize: 10, color: c.textMuted }]}>now</Text>
                    </View>
                  </>
                )}

                {/* Recent events */}
                {events.length > 0 && (
                  <View style={{ marginTop: 16, borderTopWidth: 1, borderTopColor: c.borderSubtle, paddingTop: 12 }}>
                    <Text style={[styles.detailLabel, { color: c.textMuted, marginBottom: 8 }]}>
                      Recent Events
                    </Text>
                    {events.slice(0, 5).map((e, i) => (
                      <View key={i} style={{ flexDirection: "row", alignItems: "center", marginBottom: 4 }}>
                        <Text style={{ fontSize: 11, color: e.event === "crash" || e.event === "oom" ? c.error : e.event === "restart" ? c.warn : c.success }}>
                          {e.event === "crash" ? "\u26A0" : e.event === "started" ? "\u25B6" : e.event === "restart" ? "\u21BB" : e.event === "stopped" ? "\u25A0" : "\u26A0"}
                        </Text>
                        <Text style={{ fontSize: 11, color: c.textSecondary, marginLeft: 6, flex: 1 }}>
                          {e.event} {e.details ? `— ${e.details}` : ""}
                        </Text>
                        <Text style={{ fontSize: 10, color: c.textMuted }}>
                          {new Date(e.timestamp).toLocaleTimeString(undefined, { hour: "2-digit", minute: "2-digit" })}
                        </Text>
                      </View>
                    ))}
                  </View>
                )}
              </View>
            )}
          </View>
        )}

        {/* Yaver Usage */}
        {!LEAN_SETTINGS_SURFACE && usageSummary && usageSummary.daily.length > 0 && (
          <View style={styles.section}>
            <Text style={[styles.sectionLabel, { color: c.textMuted }]}>Yaver Usage (30 days)</Text>
            <View style={[styles.card, { backgroundColor: c.bgCard, borderColor: c.border }]}>
              <View style={styles.aboutRow}>
                <Text style={[styles.aboutLabel, { color: c.textPrimary }]}>Total Tasks</Text>
                <Text style={[styles.aboutValue, { color: c.accent, fontWeight: "600" }]}>
                  {usageSummary.daily.reduce((sum, d) => sum + d.taskCount, 0)}
                </Text>
              </View>
              <View style={[styles.separator, { backgroundColor: c.borderSubtle }]} />
              <View style={styles.aboutRow}>
                <Text style={[styles.aboutLabel, { color: c.textPrimary }]}>Total Time</Text>
                <Text style={[styles.aboutValue, { color: c.accent, fontWeight: "600" }]}>
                  {usageSummary.totalSeconds >= 3600
                    ? `${(usageSummary.totalSeconds / 3600).toFixed(1)}h`
                    : `${Math.round(usageSummary.totalSeconds / 60)}m`}
                </Text>
              </View>
              {(() => {
                const runners: Record<string, number> = {};
                for (const d of usageSummary.daily) {
                  for (const [r, secs] of Object.entries(d.runners)) {
                    runners[r] = (runners[r] || 0) + secs;
                  }
                }
                const sorted = Object.entries(runners).sort((a, b) => b[1] - a[1]);
                if (sorted.length === 0) return null;
                return sorted.map(([runner, secs]) => (
                  <React.Fragment key={runner}>
                    <View style={[styles.separator, { backgroundColor: c.borderSubtle }]} />
                    <View style={styles.aboutRow}>
                      <Text style={[styles.aboutLabel, { color: c.textPrimary }]}>{runner}</Text>
                      <Text style={[styles.aboutValue, { color: c.textMuted }]}>
                        {secs >= 3600
                          ? `${(secs / 3600).toFixed(1)}h`
                          : `${Math.round(secs / 60)}m`}
                      </Text>
                    </View>
                  </React.Fragment>
                ));
              })()}
            </View>
          </View>
        )}

        {/* AI Runner */}
        <View style={styles.section}>
          <Text style={[styles.sectionLabel, { color: c.textMuted }]}>AI Runner</Text>
          <View style={[styles.card, { backgroundColor: c.bgCard, borderColor: c.border }]}>
            {RUNNER_OPTIONS.map((runner) => {
              const selected = selectedRunner === runner.runnerId;
              return (
                <Pressable
                  key={runner.runnerId}
                  style={[styles.runnerOption, { borderBottomColor: c.borderSubtle }]}
                  onPress={() => {
                    setSelectedRunner(runner.runnerId);
                    if (token) void saveUserSettings(token, { runnerId: runner.runnerId });
                    if (runner.runnerId !== "opencode") return;
                    if (!activeDevice || connectionStatus !== "connected") {
                      Alert.alert(
                        "Connect a machine first",
                        "OpenCode preferences are stored on the connected machine. Connect one, then pick OpenCode again.",
                      );
                      return;
                    }
                    setOpenCodeStartInAddProvider(true);
                    setShowOpenCodeConfig(true);
                  }}
                >
                  <View style={[styles.radioOuter, { borderColor: selected ? c.accent : c.border }]}>
                    {selected && <View style={[styles.radioInner, { backgroundColor: c.accent }]} />}
                  </View>
                  <View style={{ flex: 1 }}>
                    <Text style={[styles.runnerName, { color: c.textPrimary }]}>{runner.name}</Text>
                    <Text style={[styles.runnerDesc, { color: c.textMuted }]}>{runner.description}</Text>
                  </View>
                </Pressable>
              );
            })}
          </View>
        </View>

        {/* Appearance */}
        <View style={styles.section}>
          <Text style={[styles.sectionLabel, { color: c.textMuted }]}>Appearance</Text>
          <View style={[styles.card, { backgroundColor: c.bgCard, borderColor: c.border }]}>
            <View style={styles.themeRow}>
              <Text style={[styles.themeLabel, { color: c.textPrimary }]}>Dark Mode</Text>
              <Switch
                value={isDark}
                onValueChange={toggleTheme}
                trackColor={{ false: c.border, true: c.accent }}
                thumbColor="#ffffff"
              />
            </View>
          </View>
        </View>

        <View style={styles.section}>
          <Text style={[styles.sectionLabel, { color: c.textMuted }]}>Tasks</Text>
          <View style={[styles.card, { backgroundColor: c.bgCard, borderColor: c.border }]}>
            <View style={styles.themeRow}>
              <View style={{ flex: 1, paddingRight: 12 }}>
                <Text style={[styles.themeLabel, { color: c.textPrimary }]}>Record Demo Video</Text>
                <Text style={{ fontSize: 12, color: c.textMuted, marginTop: 3 }}>
                  Automatically record a short demo clip after new tasks finish.
                </Text>
              </View>
              <Switch
                value={taskVideoSummaryEnabled}
                onValueChange={(value) => {
                  setTaskVideoSummaryEnabled(value);
                  void saveTaskVideoSummaryEnabled(value);
                }}
                trackColor={{ false: c.border, true: c.accent }}
                thumbColor="#ffffff"
              />
            </View>
            <View style={[styles.separator, { backgroundColor: c.borderSubtle }]} />
            <View style={styles.themeRow}>
              <View style={{ flex: 1, paddingRight: 12 }}>
                <Text style={[styles.themeLabel, { color: c.textPrimary }]}>Pick machine + agent per task</Text>
                <Text style={{ fontSize: 12, color: c.textMuted, marginTop: 3 }}>
                  When on, the + button asks which machine and coding agent to use. Off = always use the connected device.
                </Text>
              </View>
              <Switch
                value={multiTargetMode}
                onValueChange={(value) => {
                  void setMultiTargetMode(value);
                }}
                trackColor={{ false: c.border, true: c.accent }}
                thumbColor="#ffffff"
              />
            </View>
          </View>
        </View>

        {/* Data management */}
        <View style={styles.section}>
          <Text style={[styles.sectionLabel, { color: c.textMuted }]}>Data</Text>
          <Pressable
            style={({ pressed }) => [
              styles.actionRow,
              { backgroundColor: c.bgCard, borderColor: c.border },
              pressed && styles.actionRowPressed,
            ]}
            onPress={handleClearCache}
            disabled={isClearing}
          >
            <Text style={[styles.actionRowLabel, { color: c.textPrimary }]}>
              {isClearing ? "Clearing..." : "Clear Task Cache"}
            </Text>
            <Text style={[styles.actionRowChevron, { color: c.textMuted }]}>&rsaquo;</Text>
          </Pressable>
          <View style={{ height: 8 }} />
          <Pressable
            style={({ pressed }) => [
              styles.actionRow,
              { backgroundColor: c.bgCard, borderColor: c.border },
              pressed && styles.actionRowPressed,
            ]}
            onPress={handleCleanAgent}
            disabled={isCleaning}
          >
            <Text style={[styles.actionRowLabel, { color: c.textPrimary }]}>
              {isCleaning ? "Cleaning..." : "Clean Up Agent"}
            </Text>
            <Text style={[styles.actionRowChevron, { color: c.textMuted }]}>&rsaquo;</Text>
          </Pressable>
        </View>

        {/* Test App */}
        {!LEAN_SETTINGS_SURFACE && connectionStatus === "connected" && (
        <View style={styles.section}>
          <Text style={[styles.sectionLabel, { color: c.textMuted }]}>Test App</Text>
          {!testRunning && !testTarget ? (
            <>
              <Text style={{ fontSize: 12, color: c.textMuted, marginBottom: 8 }}>
                Run app tests and stream live agent logs. Where should tests run?
              </Text>
              <View style={{ flexDirection: "row", gap: 8 }}>
                <Pressable
                  style={({ pressed }) => [
                    styles.actionRow,
                    { backgroundColor: c.bgCard, borderColor: c.border, flex: 1 },
                    pressed && styles.actionRowPressed,
                  ]}
                  onPress={() => startTestApp("device")}
                >
                  <Text style={[styles.actionRowLabel, { color: c.textPrimary, textAlign: "center" }]}>
                    This Device
                  </Text>
                </Pressable>
                <Pressable
                  style={({ pressed }) => [
                    styles.actionRow,
                    { backgroundColor: c.bgCard, borderColor: c.border, flex: 1 },
                    pressed && styles.actionRowPressed,
                  ]}
                  onPress={() => startTestApp("simulator")}
                >
                  <Text style={[styles.actionRowLabel, { color: c.textPrimary, textAlign: "center" }]}>
                    Simulator
                  </Text>
                </Pressable>
              </View>
            </>
          ) : (
            <>
              <View style={{ flexDirection: "row", alignItems: "center", justifyContent: "space-between", marginBottom: 8 }}>
                <View style={{ flexDirection: "row", alignItems: "center", gap: 6 }}>
                  {testRunning && <ActivityIndicator size="small" color={c.accent} />}
                  <Text style={{ fontSize: 13, fontWeight: "600", color: c.textPrimary }}>
                    {testRunning ? `Streaming logs (${testTarget})` : "Test stopped"}
                  </Text>
                </View>
                <View style={{ flexDirection: "row", gap: 8 }}>
                  {testRunning && (
                    <Pressable
                      onPress={stopTestApp}
                      style={({ pressed }) => [
                        { paddingVertical: 5, paddingHorizontal: 12, borderRadius: 6, backgroundColor: c.error },
                        pressed && { opacity: 0.7 },
                      ]}
                    >
                      <Text style={{ fontSize: 12, fontWeight: "600", color: "#fff" }}>Stop</Text>
                    </Pressable>
                  )}
                  {!testRunning && (
                    <Pressable
                      onPress={() => { setTestTarget(null); setAgentLogs([]); }}
                      style={({ pressed }) => [
                        { paddingVertical: 5, paddingHorizontal: 12, borderRadius: 6, backgroundColor: c.accent },
                        pressed && { opacity: 0.7 },
                      ]}
                    >
                      <Text style={{ fontSize: 12, fontWeight: "600", color: "#fff" }}>Reset</Text>
                    </Pressable>
                  )}
                </View>
              </View>
              {/* Agent console logs */}
              <View style={{
                backgroundColor: "#0d0d0d",
                borderRadius: 8,
                borderWidth: 1,
                borderColor: c.border,
                maxHeight: 300,
                overflow: "hidden",
              }}>
                <ScrollView
                  ref={agentLogsRef}
                  style={{ padding: 10 }}
                  nestedScrollEnabled
                  onContentSizeChange={() => agentLogsRef.current?.scrollToEnd({ animated: false })}
                >
                  {agentLogs.length === 0 ? (
                    <Text style={{ fontFamily: Platform.OS === "ios" ? "Menlo" : "monospace", fontSize: 11, color: "#555" }}>
                      Waiting for agent logs...
                    </Text>
                  ) : (
                    agentLogs.map((line, i) => (
                      <Text
                        key={i}
                        style={{
                          fontFamily: Platform.OS === "ios" ? "Menlo" : "monospace",
                          fontSize: 11,
                          color: line.includes("[error]") || line.includes("ERROR") ? "#ef4444"
                            : line.includes("[warn]") || line.includes("WARN") ? "#eab308"
                            : line.includes("[info]") || line.includes("INFO") ? "#22c55e"
                            : "#9ca3af",
                          lineHeight: 16,
                        }}
                      >
                        {line}
                      </Text>
                    ))
                  )}
                </ScrollView>
              </View>
            </>
          )}
        </View>
        )}

        {/* Container Sandbox */}
        {(KEEP_SANDBOX_SURFACE || !LEAN_SETTINGS_SURFACE) && connectionStatus === "connected" && sandboxStatus && (
        <View style={styles.section}>
          <Text style={[styles.sectionLabel, { color: c.textMuted }]}>Container Sandbox</Text>

          {/* Docker status */}
          <View style={[styles.actionRow, { backgroundColor: c.bgCard, borderColor: c.border, flexDirection: "row", justifyContent: "space-between", alignItems: "center" }]}>
            <Text style={[styles.actionRowLabel, { color: c.textPrimary }]}>Docker</Text>
            <Text style={{ fontSize: 13, color: sandboxStatus.docker ? "#22c55e" : c.textMuted }}>
              {sandboxStatus.docker ? "Available" : "Not found"}
            </Text>
          </View>

          {/* Image status + build */}
          <View style={[styles.actionRow, { backgroundColor: c.bgCard, borderColor: c.border, flexDirection: "row", justifyContent: "space-between", alignItems: "center" }]}>
            <Text style={[styles.actionRowLabel, { color: c.textPrimary }]}>
              Image{sandboxStatus.imageName ? ` (${sandboxStatus.imageName})` : ""}
            </Text>
            {sandboxStatus.imageReady ? (
              <Text style={{ fontSize: 13, color: "#22c55e" }}>Ready</Text>
            ) : sandboxBuilding ? (
              <ActivityIndicator size="small" color={c.accent} />
            ) : (
              <Pressable
                onPress={async () => {
                  setSandboxBuilding(true);
                  await quicClient.buildSandboxImage();
                  // Poll for completion
                  const poll = setInterval(async () => {
                    const s = await quicClient.getSandboxStatus();
                    if (s) {
                      setSandboxStatus(s);
                      if (s.imageReady) {
                        setSandboxBuilding(false);
                        clearInterval(poll);
                      }
                    }
                  }, 5000);
                  // Stop polling after 15 min
                  setTimeout(() => { clearInterval(poll); setSandboxBuilding(false); }, 15 * 60 * 1000);
                }}
                disabled={!sandboxStatus.docker}
                style={({ pressed }) => [
                  { paddingVertical: 5, paddingHorizontal: 12, borderRadius: 6, backgroundColor: sandboxStatus.docker ? c.accent : c.border },
                  pressed && { opacity: 0.7 },
                ]}
              >
                <Text style={{ fontSize: 12, fontWeight: "600", color: "#fff" }}>Build</Text>
              </Pressable>
            )}
          </View>

          {/* Containerize Guests toggle */}
          <View style={{ height: 8 }} />
          <View style={[styles.actionRow, { backgroundColor: c.bgCard, borderColor: c.border, flexDirection: "row", justifyContent: "space-between", alignItems: "center" }]}>
            <View style={{ flex: 1 }}>
              <Text style={[styles.actionRowLabel, { color: c.textPrimary }]}>Containerize Guests</Text>
              <Text style={{ fontSize: 11, color: c.textMuted }}>Run guest tasks in Docker containers</Text>
            </View>
            <Switch
              value={sandboxStatus.containerizeGuests}
              disabled={sandboxSaving || !sandboxStatus.docker}
              onValueChange={async (val) => {
                setSandboxSaving(true);
                const ok = await quicClient.updateSandboxConfig({ containerizeGuests: val });
                if (ok) {
                  const s = await quicClient.getSandboxStatus();
                  if (s) setSandboxStatus(s);
                }
                setSandboxSaving(false);
              }}
              trackColor={{ false: c.border, true: c.accent }}
            />
          </View>

          {/* Containerize Host toggle */}
          <View style={{ height: 8 }} />
          <View style={[styles.actionRow, { backgroundColor: c.bgCard, borderColor: c.border, flexDirection: "row", justifyContent: "space-between", alignItems: "center" }]}>
            <View style={{ flex: 1 }}>
              <Text style={[styles.actionRowLabel, { color: c.textPrimary }]}>Containerize Host</Text>
              <Text style={{ fontSize: 11, color: c.textMuted }}>Run all tasks in Docker containers</Text>
            </View>
            <Switch
              value={sandboxStatus.containerizeHost}
              disabled={sandboxSaving || !sandboxStatus.docker}
              onValueChange={async (val) => {
                setSandboxSaving(true);
                const ok = await quicClient.updateSandboxConfig({ containerizeHost: val });
                if (ok) {
                  const s = await quicClient.getSandboxStatus();
                  if (s) setSandboxStatus(s);
                }
                setSandboxSaving(false);
              }}
              trackColor={{ false: c.border, true: c.accent }}
            />
          </View>

          {/* Network Mode */}
          <View style={{ height: 8 }} />
          <View style={[styles.actionRow, { backgroundColor: c.bgCard, borderColor: c.border }]}>
            <Text style={[styles.actionRowLabel, { color: c.textPrimary, marginBottom: 6 }]}>Network Mode</Text>
            <View style={{ flexDirection: "row", gap: 6 }}>
              {(["host", "bridge", "none"] as const).map((mode) => (
                <Pressable
                  key={mode}
                  onPress={async () => {
                    setSandboxSaving(true);
                    const ok = await quicClient.updateSandboxConfig({ networkMode: mode });
                    if (ok) {
                      const s = await quicClient.getSandboxStatus();
                      if (s) setSandboxStatus(s);
                    }
                    setSandboxSaving(false);
                  }}
                  disabled={sandboxSaving}
                  style={[
                    { paddingVertical: 5, paddingHorizontal: 12, borderRadius: 6, borderWidth: 1 },
                    sandboxStatus.networkMode === mode
                      ? { backgroundColor: c.accent, borderColor: c.accent }
                      : { backgroundColor: "transparent", borderColor: c.border },
                  ]}
                >
                  <Text style={{ fontSize: 12, fontWeight: "600", color: sandboxStatus.networkMode === mode ? "#fff" : c.textPrimary }}>
                    {mode}
                  </Text>
                </Pressable>
              ))}
            </View>
          </View>

          {/* Read-only rootfs toggle */}
          <View style={{ height: 8 }} />
          <View style={[styles.actionRow, { backgroundColor: c.bgCard, borderColor: c.border, flexDirection: "row", justifyContent: "space-between", alignItems: "center" }]}>
            <View style={{ flex: 1 }}>
              <Text style={[styles.actionRowLabel, { color: c.textPrimary }]}>Read-only Root</Text>
              <Text style={{ fontSize: 11, color: c.textMuted }}>Writes only to /workspace and /tmp</Text>
            </View>
            <Switch
              value={sandboxStatus.readOnly ?? false}
              disabled={sandboxSaving}
              onValueChange={async (val) => {
                setSandboxSaving(true);
                const ok = await quicClient.updateSandboxConfig({ readOnly: val });
                if (ok) {
                  const s = await quicClient.getSandboxStatus();
                  if (s) setSandboxStatus(s);
                }
                setSandboxSaving(false);
              }}
              trackColor={{ false: c.border, true: c.accent }}
            />
          </View>

          {/* Resource limits info */}
          {(sandboxStatus.cpuLimit || sandboxStatus.memoryLimit || sandboxStatus.gpuAvailable) && (
            <View style={[styles.actionRow, { backgroundColor: c.bgCard, borderColor: c.border }]}>
              <Text style={[styles.actionRowLabel, { color: c.textMuted, marginBottom: 4 }]}>Resources</Text>
              <Text style={{ fontSize: 12, color: c.textPrimary }}>
                {[
                  sandboxStatus.cpuLimit && `CPU: ${sandboxStatus.cpuLimit}`,
                  sandboxStatus.memoryLimit && `Memory: ${sandboxStatus.memoryLimit}`,
                  sandboxStatus.gpuAvailable && "GPU: Available",
                ].filter(Boolean).join("  |  ")}
              </Text>
            </View>
          )}
        </View>
        )}

        {/* Logs */}
        <View style={styles.section}>
          <Text style={[styles.sectionLabel, { color: c.textMuted }]}>Diagnostics</Text>
          <Pressable
            style={({ pressed }) => [
              styles.actionRow,
              { backgroundColor: c.bgCard, borderColor: c.border },
              pressed && styles.actionRowPressed,
            ]}
            onPress={() => setShowLogs(!showLogs)}
          >
            <Text style={[styles.actionRowLabel, { color: c.textPrimary }]}>
              Connection Logs ({logs.length})
            </Text>
            <Text style={[styles.actionRowChevron, { color: c.textMuted }]}>{showLogs ? "\u2303" : "\u2304"}</Text>
          </Pressable>
          <View style={[styles.card, { backgroundColor: c.bgCard, borderColor: c.border, marginTop: 8 }]}>
            <View style={styles.themeRow}>
              <View style={{ flex: 1 }}>
                <Text style={[styles.themeLabel, { color: c.textPrimary }]}>Force Relay</Text>
                <Text style={[{ fontSize: 12, color: c.textMuted, marginTop: 2 }]}>
                  Skip direct connection, always use relay server
                </Text>
              </View>
              <Switch
                value={forceRelay}
                onValueChange={(v) => {
                  setForceRelay(v);
                  quicClient.setForceRelay(v);
                  if (token) saveUserSettings(token, { forceRelay: v });
                  // If disconnected but have a device, reconnect with new strategy
                  if (activeDevice && !quicClient.isConnected) {
                    selectDevice(activeDevice);
                  }
                }}
                trackColor={{ false: c.border, true: c.accent }}
                thumbColor="#ffffff"
              />
            </View>
          </View>

          <View style={[styles.card, { backgroundColor: c.bgCard, borderColor: c.border, marginTop: 8 }]}>
            <View style={styles.themeRow}>
              <View style={{ flex: 1 }}>
                <Text style={[styles.themeLabel, { color: c.textPrimary }]}>Debug Logs</Text>
                <Text style={[{ fontSize: 12, color: c.textMuted, marginTop: 2 }]}>
                  Send connection diagnostics to Yaver servers for troubleshooting
                </Text>
              </View>
              <Switch
                value={debugLogsEnabled}
                onValueChange={(v) => {
                  setDebugLogsEnabled(v);
                  AsyncStorage.setItem("@yaver/debug_logs_enabled", v ? "true" : "false");
                }}
                trackColor={{ false: c.border, true: c.accent }}
                thumbColor="#ffffff"
              />
            </View>
          </View>

          {/* Relay Servers */}
          <View style={[styles.card, { backgroundColor: c.bgCard, borderColor: c.border, marginTop: 8 }]}>
            <View style={styles.themeRow}>
              <Text style={[styles.themeLabel, { color: c.textPrimary }]}>Relay Servers</Text>
              <Pressable
                style={({ pressed }) => [
                  { paddingVertical: 4, paddingHorizontal: 10, borderRadius: 6, backgroundColor: c.accent },
                  pressed && { opacity: 0.7 },
                ]}
                onPress={() => setShowAddRelay(true)}
              >
                <Text style={{ fontSize: 13, color: "#fff", fontWeight: "600" }}>+ Add</Text>
              </Pressable>
            </View>

            {/* Add Relay Modal */}
            <Modal visible={showAddRelay} animationType="slide" presentationStyle="pageSheet" onRequestClose={() => setShowAddRelay(false)}>
              <KeyboardAvoidingView style={{ flex: 1, backgroundColor: c.bg }} behavior={Platform.OS === "ios" ? "padding" : undefined}>
                <View style={{ flexDirection: "row", justifyContent: "space-between", alignItems: "center", padding: 16, paddingTop: Platform.OS === "ios" ? 56 : 16 }}>
                  <Pressable onPress={() => setShowAddRelay(false)}>
                    <Text style={{ fontSize: 16, color: c.accent }}>Cancel</Text>
                  </Pressable>
                  <Text style={{ fontSize: 17, fontWeight: "600", color: c.textPrimary }}>Add Relay Server</Text>
                  <Pressable onPress={() => { handleAddRelay(); }}>
                    <Text style={{ fontSize: 16, color: c.accent, fontWeight: "600" }}>Add</Text>
                  </Pressable>
                </View>
                <ScrollView style={{ flex: 1 }} contentContainerStyle={{ padding: 16, gap: 12 }} keyboardShouldPersistTaps="handled">
                  <Text style={{ fontSize: 13, color: c.textMuted, marginBottom: 4 }}>
                    Connect to your self-hosted relay server for NAT traversal and roaming.
                  </Text>
                  <TextInput
                    style={[styles.relayInput, { backgroundColor: c.bgCard, borderColor: c.border, color: c.textPrimary }]}
                    placeholder="https://relay.example.com"
                    placeholderTextColor={c.textMuted}
                    value={newRelayUrl}
                    onChangeText={setNewRelayUrl}
                    autoCapitalize="none"
                    autoCorrect={false}
                    keyboardType="url"
                    autoFocus
                  />
                  <TextInput
                    style={[styles.relayInput, { backgroundColor: c.bgCard, borderColor: c.border, color: c.textPrimary }]}
                    placeholder="Password (optional)"
                    placeholderTextColor={c.textMuted}
                    value={newRelayPassword}
                    onChangeText={setNewRelayPassword}
                    autoCapitalize="none"
                    autoCorrect={false}
                    secureTextEntry
                  />
                  <TextInput
                    style={[styles.relayInput, { backgroundColor: c.bgCard, borderColor: c.border, color: c.textPrimary }]}
                    placeholder="Label (optional) e.g. My VPS"
                    placeholderTextColor={c.textMuted}
                    value={newRelayLabel}
                    onChangeText={setNewRelayLabel}
                    autoCapitalize="none"
                  />
                </ScrollView>
              </KeyboardAvoidingView>
            </Modal>

            {customRelays.length === 0 && !showAddRelay && (
              <View style={{ marginTop: 8 }}>
                <Text style={{ fontSize: 12, color: c.textMuted }}>
                  Using default relay servers. Add your own to use a self-hosted relay.
                </Text>
                <Text
                  style={{ fontSize: 12, color: c.accent, marginTop: 4 }}
                  onPress={() => Linking.openURL("https://yaver.io/docs/self-hosting")}
                >
                  Learn more about self-hosting a relay
                </Text>
              </View>
            )}

            {customRelays.map((relay) => {
              const testResult = relayTestResults[relay.id];
              return (
                <View
                  key={relay.id}
                  style={{ marginTop: 12, paddingTop: 12, borderTopWidth: 1, borderTopColor: c.borderSubtle }}
                >
                  <View style={{ flexDirection: "row", alignItems: "center", justifyContent: "space-between" }}>
                    <View style={{ flex: 1 }}>
                      <Text style={{ fontSize: 14, color: c.textPrimary, fontWeight: "500" }}>
                        {relay.region !== "custom" ? relay.region : relay.httpUrl}
                      </Text>
                      {relay.region !== "custom" && (
                        <Text style={{ fontSize: 11, color: c.textMuted, marginTop: 2 }}>{relay.httpUrl}</Text>
                      )}
                    </View>
                    {testResult && (
                      <View style={{
                        width: 8, height: 8, borderRadius: 4, marginRight: 8,
                        backgroundColor: testResult.ok ? c.success : c.error,
                      }} />
                    )}
                  </View>
                  <View style={{ flexDirection: "row", gap: 8, marginTop: 8 }}>
                    <Pressable
                      style={({ pressed }) => [
                        { paddingVertical: 4, paddingHorizontal: 10, borderRadius: 6, backgroundColor: c.bgCardElevated },
                        pressed && { opacity: 0.7 },
                      ]}
                      onPress={() => handleTestRelay(relay)}
                      disabled={testingRelayId === relay.id}
                    >
                      {testingRelayId === relay.id ? (
                        <ActivityIndicator size="small" color={c.accent} />
                      ) : (
                        <Text style={{ fontSize: 12, color: c.accent }}>
                          {testResult ? (testResult.ok ? `OK ${testResult.ms}ms` : "Failed") : "Test"}
                        </Text>
                      )}
                    </Pressable>
                    <Pressable
                      style={({ pressed }) => [
                        { paddingVertical: 4, paddingHorizontal: 10, borderRadius: 6, backgroundColor: c.errorBg },
                        pressed && { opacity: 0.7 },
                      ]}
                      onPress={() => handleRemoveRelay(relay.id)}
                    >
                      <Text style={{ fontSize: 12, color: c.error }}>Remove</Text>
                    </Pressable>
                  </View>
                </View>
              );
            })}

            {/* Sync to cloud toggle */}
            <View style={{ marginTop: 16, paddingTop: 12, borderTopWidth: 1, borderTopColor: c.borderSubtle }}>
              <View style={{ flexDirection: "row", alignItems: "center", justifyContent: "space-between" }}>
                <View style={{ flex: 1, marginRight: 12 }}>
                  <Text style={{ fontSize: 14, color: c.textPrimary, fontWeight: "500" }}>Sync to cloud</Text>
                  <Text style={{ fontSize: 11, color: c.textMuted, marginTop: 2 }}>
                    Sync relay URLs to your account so your devices can connect through Yaver Relay. Passwords and secrets are always stored locally only.
                  </Text>
                </View>
                <Switch
                  value={relaySyncEnabled}
                  onValueChange={handleToggleRelaySync}
                  trackColor={{ false: c.border, true: c.accent }}
                />
              </View>
            </View>
          </View>

          {/* Setup Guide — collapsible */}
          <Pressable
            style={({ pressed }) => [
              styles.actionRow,
              { backgroundColor: c.bgCard, borderColor: c.border, marginTop: 8 },
              pressed && styles.actionRowPressed,
            ]}
            onPress={() => setShowGuide(!showGuide)}
          >
            <Text style={[styles.actionRowLabel, { color: c.textPrimary }]}>Setup Guide</Text>
            <Text style={[styles.actionRowChevron, { color: c.textMuted }]}>{showGuide ? "\u2303" : "\u2304"}</Text>
          </Pressable>

          {showGuide && (
            <View style={[styles.card, { backgroundColor: c.bgCard, borderColor: c.border, marginTop: 4 }]}>
              {/* How connections work */}
              <Pressable onPress={() => setGuideSection(guideSection === "connections" ? null : "connections")}>
                <View style={{ flexDirection: "row", justifyContent: "space-between", alignItems: "center", paddingVertical: 10 }}>
                  <Text style={{ fontSize: 14, fontWeight: "600", color: c.textPrimary }}>How connections work</Text>
                  <Text style={{ color: c.textMuted }}>{guideSection === "connections" ? "\u2303" : "\u2304"}</Text>
                </View>
              </Pressable>
              {guideSection === "connections" && (
                <View style={{ paddingBottom: 12 }}>
                  <Text style={{ fontSize: 12, color: c.textMuted, lineHeight: 18 }}>
                    Yaver tries connections in this order:{"\n\n"}
                    1. Yaver Relay for remote access{"\n"}
                    2. Direct local network when available{"\n\n"}
                    Keep relay enabled for the normal experience. Network transitions are handled automatically while the app reconnects.
                  </Text>
                </View>
              )}

              <View style={{ height: 1, backgroundColor: c.borderSubtle }} />

              {/* Getting started */}
              <Pressable onPress={() => setGuideSection(guideSection === "getting-started" ? null : "getting-started")}>
                <View style={{ flexDirection: "row", justifyContent: "space-between", alignItems: "center", paddingVertical: 10 }}>
                  <Text style={{ fontSize: 14, fontWeight: "600", color: c.textPrimary }}>Getting started</Text>
                  <Text style={{ color: c.textMuted }}>{guideSection === "getting-started" ? "\u2303" : "\u2304"}</Text>
                </View>
              </Pressable>
              {guideSection === "getting-started" && (
                <View style={{ paddingBottom: 12 }}>
                  <Text style={{ fontSize: 12, color: c.textMuted, lineHeight: 18 }}>
                    1. Install the CLI on your dev machine:{"\n\n"}
                  </Text>
                  <Text style={{ fontSize: 11, color: c.textSecondary, fontFamily: "monospace", lineHeight: 18, backgroundColor: c.bgCardElevated, padding: 10, borderRadius: 6, overflow: "hidden" }}>
                    {"npm install -g yaver-cli\n"}
                    {"yaver auth\n"}
                    {"yaver serve"}
                  </Text>
                  <Text style={{ fontSize: 12, color: c.textMuted, lineHeight: 18, marginTop: 8 }}>
                    2. Sign in here with the same account{"\n"}
                    3. Your machine appears automatically{"\n"}
                    4. Tap it to connect, then create a task
                  </Text>
                </View>
              )}

            </View>
          )}

          {showLogs && (
            <View style={[styles.logsContainer, { backgroundColor: c.bgCard, borderColor: c.border }]}>
              <View style={styles.logsActions}>
                <Pressable onPress={() => {
                  const text = logs.map(l =>
                    `${new Date(l.timestamp).toLocaleTimeString()} [${l.level}] ${l.message}`
                  ).join("\n");
                  ExpoClipboard.setStringAsync(text);
                  Alert.alert("Copied", "Logs copied to clipboard.");
                }}>
                  <Text style={[styles.logsActionBtn, { color: c.accent }]}>Copy All</Text>
                </Pressable>
                <Pressable onPress={() => { clearLogEntries(); }}>
                  <Text style={[styles.logsActionBtn, { color: c.error }]}>Clear</Text>
                </Pressable>
              </View>
              <ScrollView style={styles.logsScroll} nestedScrollEnabled>
                {logs.length === 0 ? (
                  <Text style={[styles.logEmpty, { color: c.textMuted }]}>No logs yet.</Text>
                ) : (
                  logs.slice().reverse().map((entry, i) => (
                    <Text key={i} style={[styles.logLine, {
                      color: entry.level === "error" ? c.error : entry.level === "warn" ? "#eab308" : c.textSecondary,
                    }]}>
                      {new Date(entry.timestamp).toLocaleTimeString()} {entry.message}
                    </Text>
                  ))
                )}
              </ScrollView>
            </View>
          )}
        </View>

        {/* Key Storage */}
        {!LEAN_SETTINGS_SURFACE && <View style={styles.section}>
          <Text style={[styles.sectionLabel, { color: c.textMuted }]}>Security</Text>
          <View style={[styles.card, { backgroundColor: c.bgCard, borderColor: c.border }]}>
            <View style={{ paddingHorizontal: 16, paddingVertical: 12 }}>
              <Text style={[styles.aboutLabel, { color: c.textPrimary, marginBottom: 4 }]}>Secret storage</Text>
              <Text style={{ color: c.textMuted, fontSize: 11, marginBottom: 12 }}>
                Where to store API keys, relay passwords, and other secrets
              </Text>
              <View style={{ flexDirection: "row", gap: 8 }}>
                <Pressable
                  style={({ pressed }) => [
                    {
                      flex: 1, paddingVertical: 10, paddingHorizontal: 12,
                      borderRadius: 8, borderWidth: 1, alignItems: "center",
                      backgroundColor: keyStorage === "local" ? c.accent : c.bg,
                      borderColor: keyStorage === "local" ? c.accent : c.border,
                    },
                    pressed && { opacity: 0.7 },
                  ]}
                  onPress={async () => {
                    setKeyStorage("local");
                    await saveKeyStoragePreference("local");
                  }}
                >
                  <Text style={{ color: keyStorage === "local" ? "#fff" : c.textPrimary, fontWeight: "600", fontSize: 13 }}>
                    Device only
                  </Text>
                  <Text style={{ color: keyStorage === "local" ? "rgba(255,255,255,0.7)" : c.textMuted, fontSize: 10, marginTop: 2, textAlign: "center" }}>
                    Keychain / SecureStore
                  </Text>
                </Pressable>
                <Pressable
                  style={({ pressed }) => [
                    {
                      flex: 1, paddingVertical: 10, paddingHorizontal: 12,
                      borderRadius: 8, borderWidth: 1, alignItems: "center",
                      backgroundColor: keyStorage === "cloud" ? c.accent : c.bg,
                      borderColor: keyStorage === "cloud" ? c.accent : c.border,
                    },
                    pressed && { opacity: 0.7 },
                  ]}
                  onPress={async () => {
                    setKeyStorage("cloud");
                    await saveKeyStoragePreference("cloud");
                  }}
                >
                  <Text style={{ color: keyStorage === "cloud" ? "#fff" : c.textPrimary, fontWeight: "600", fontSize: 13 }}>
                    Sync to cloud
                  </Text>
                  <Text style={{ color: keyStorage === "cloud" ? "rgba(255,255,255,0.7)" : c.textMuted, fontSize: 10, marginTop: 2, textAlign: "center" }}>
                    Accessible from all devices
                  </Text>
                </Pressable>
              </View>
            </View>
          </View>
        </View>}

        {/* About Relay Servers */}
        {!LEAN_SETTINGS_SURFACE && <View style={styles.section}>
          <Text style={[styles.sectionLabel, { color: c.textMuted }]}>About Relay Servers</Text>
          <View style={[styles.card, { backgroundColor: c.bgCard, borderColor: c.border, padding: 16 }]}>
            <Text style={{ fontSize: 13, color: c.textSecondary, lineHeight: 19 }}>
              Yaver includes a free shared relay. If you need a dedicated relay or a managed box later, use the web app. Billing is intentionally web-only.
            </Text>
          </View>
        </View>}

        {!LEAN_SETTINGS_SURFACE && <View style={styles.section}>
          <Text style={[styles.sectionLabel, { color: c.textMuted }]}>Dogfood Yaver</Text>
          <View style={[styles.card, { backgroundColor: c.bgCard, borderColor: c.border }]}>
            <Text style={[styles.aboutLabel, { color: c.textPrimary, marginBottom: 4 }]}>
              Develop Yaver with Yaver
            </Text>
            <Text style={{ color: c.textMuted, fontSize: 11, marginBottom: 12, lineHeight: 16 }}>
              Point this at the Yaver monorepo on your dev machine, then launch coding or Hermes refresh through the connected Go agent.
            </Text>

            <Text style={[styles.sectionLabel, { color: c.textMuted, marginBottom: 8 }]}>Repo path</Text>
            <TextInput
              value={dogfoodRepoDir}
              onChangeText={(text) => {
                setDogfoodRepoDir(text);
                saveDogfoodConfig({ repoDir: text }).catch(() => {});
              }}
              placeholder="/Users/you/Workspace/yaver.io"
              placeholderTextColor={c.textMuted}
              autoCapitalize="none"
              autoCorrect={false}
              spellCheck={false}
              style={[styles.editNameInput, { color: c.textPrimary, borderColor: c.border, backgroundColor: c.bg }]}
            />

            <Text style={[styles.sectionLabel, { color: c.textMuted, marginTop: 12, marginBottom: 8 }]}>Bootstrap prompt</Text>
            <TextInput
              value={dogfoodPrompt}
              onChangeText={(text) => {
                setDogfoodPrompt(text);
                saveDogfoodConfig({ prompt: text }).catch(() => {});
              }}
              placeholder="Refresh Yaver using Yaver..."
              placeholderTextColor={c.textMuted}
              multiline
              style={[
                styles.editNameInput,
                {
                  color: c.textPrimary,
                  borderColor: c.border,
                  backgroundColor: c.bg,
                  minHeight: 84,
                  textAlignVertical: "top",
                },
              ]}
            />

            <Text style={{ color: c.textMuted, fontSize: 11, marginTop: 10 }}>
              Backend: {connectionStatus === "connected" && activeDevice ? activeDevice.name : "No connected Go agent"}
            </Text>

            <View style={{ flexDirection: "row", gap: 8, marginTop: 12 }}>
              <Pressable
                style={({ pressed }) => [
                  styles.editNameButton,
                  { flex: 1, backgroundColor: c.accent, alignItems: "center" },
                  pressed && { opacity: 0.7 },
                ]}
                onPress={() => openDogfoodTask("code")}
              >
                <Text style={styles.editNameButtonText}>Open coding loop</Text>
              </Pressable>
              <Pressable
                style={({ pressed }) => [
                  styles.editNameButton,
                  { flex: 1, backgroundColor: c.bg, borderWidth: 1, borderColor: c.border, alignItems: "center" },
                  pressed && { opacity: 0.7 },
                ]}
                onPress={() => openDogfoodTask("hermes")}
              >
                <Text style={[styles.editNameButtonText, { color: c.textPrimary }]}>Kick Hermes refresh</Text>
              </Pressable>
            </View>

            <Pressable
              style={({ pressed }) => [
                {
                  marginTop: 10,
                  paddingVertical: 10,
                  borderRadius: 8,
                  borderWidth: 1,
                  borderColor: c.border,
                  alignItems: "center",
                  backgroundColor: c.bg,
                },
                pressed && { opacity: 0.7 },
              ]}
              onPress={openDogfoodProject}
            >
              <Text style={{ color: c.textPrimary, fontWeight: "600", fontSize: 13 }}>
                Open Yaver workspace
              </Text>
            </Pressable>
          </View>
        </View>}

        {/* About */}
        {!LEAN_SETTINGS_SURFACE && <View style={styles.section}>
          <Text style={[styles.sectionLabel, { color: c.textMuted }]}>About</Text>
          <View style={[styles.linksCard, { backgroundColor: c.bgCard, borderColor: c.border }]}>
            {[
              { label: "Website", onPress: () => Linking.openURL("https://yaver.io").catch(() => {}) },
              { label: "Privacy Policy", onPress: () => router.push("/legal/privacy") },
              { label: "Terms of Service", onPress: () => router.push("/legal/terms") },
              { label: "Contact", onPress: () => Linking.openURL("mailto:kivanc.cakmak@simkab.com").catch(() => {}) },
            ].map((link, i) => (
              <React.Fragment key={link.label}>
                {i > 0 && <View style={[styles.separator, { backgroundColor: c.borderSubtle }]} />}
                <Pressable
                  style={({ pressed }) => [
                    styles.linkRow,
                    pressed && { backgroundColor: c.bgCardElevated },
                  ]}
                  onPress={link.onPress}
                >
                  <Text style={[styles.linkText, { color: c.accent }]}>{link.label}</Text>
                  <Text style={[styles.linkChevron, { color: c.textMuted }]}>&rsaquo;</Text>
                </Pressable>
              </React.Fragment>
            ))}
          </View>
        </View>}

        {/* Integrations */}
        <View style={styles.section}>
          <Pressable
            onPress={async () => {
              setShowIntegrations(!showIntegrations);
              if (!showIntegrations && connectionStatus === "connected") {
                setIntgLoading(true);
                const cfg = await quicClient.getNotificationsConfig();
                if (cfg) setIntgConfig(cfg);
                setIntgLoading(false);
              }
            }}
            style={[styles.card, { backgroundColor: c.bgCard, borderColor: c.border, marginTop: 8 }]}
          >
            <View style={styles.themeRow}>
              <Text style={[styles.themeLabel, { color: c.textPrimary }]}>Integrations</Text>
              <Text style={{ color: c.textMuted }}>{showIntegrations ? "▲" : "▼"}</Text>
            </View>
          </Pressable>

          {showIntegrations && (
            <View style={[styles.card, { backgroundColor: c.bgCard, borderColor: c.border, marginTop: 4, padding: 16 }]}>
              <Text style={{ color: c.textPrimary, fontWeight: "700", fontSize: 15 }}>AI Providers</Text>
              <Text style={{ color: c.textMuted, fontSize: 12, marginTop: 4 }}>
                Save API keys for phone-side vibe coding. OpenAI and GLM work directly on mobile. Claude Code stays a runner on your connected machine.
              </Text>

              {/* Full opencode.json editor — opens a sheet that lets the
                  user pick agents, edit per-provider baseURLs, set
                  per-agent model overrides.
                  Drives /runner/opencode/config on the connected device,
                  same code path the web ToolsView hits. */}
              <Pressable
                onPress={() => setShowOpenCodeConfig(true)}
                style={({ pressed }) => [
                  {
                    flexDirection: "row", alignItems: "center", justifyContent: "space-between",
                    marginTop: 12, padding: 12, borderRadius: 8,
                    borderWidth: 1, borderColor: c.border, backgroundColor: c.bgCardElevated,
                  },
                  pressed && { opacity: 0.7 },
                ]}
              >
                <View style={{ flex: 1 }}>
                  <Text style={{ color: c.textPrimary, fontWeight: "600", fontSize: 13 }}>OpenCode Config</Text>
                  <Text style={{ color: c.textMuted, fontSize: 11, marginTop: 2 }}>
                    Edit opencode.json on the connected device — agents, providers, baseURLs.
                  </Text>
                </View>
                <Text style={{ color: c.textMuted, fontSize: 16 }}>›</Text>
              </Pressable>

              <Text style={{ color: c.textPrimary, fontWeight: "600", fontSize: 13, marginTop: 14 }}>Preferred mobile coding provider</Text>
              <View style={{ flexDirection: "row", gap: 8, marginTop: 8 }}>
                {(["openai", "glm"] as const).map((provider) => {
                  const active = mobileCodingProvider === provider;
                  return (
                    <Pressable
                      key={provider}
                      onPress={() => setMobileCodingProvider(provider)}
                      style={({ pressed }) => [
                        {
                          flex: 1,
                          paddingVertical: 10,
                          borderRadius: 8,
                          borderWidth: 1,
                          alignItems: "center",
                          backgroundColor: active ? c.accent : c.bg,
                          borderColor: active ? c.accent : c.border,
                        },
                        pressed && { opacity: 0.7 },
                      ]}
                    >
                      <Text style={{ color: active ? "#fff" : c.textPrimary, fontWeight: "600", fontSize: 13 }}>
                        {provider === "openai" ? "OpenAI" : "GLM"}
                      </Text>
                    </Pressable>
                  );
                })}
              </View>

              <Text style={{ color: c.textPrimary, fontWeight: "600", fontSize: 13, marginTop: 14 }}>OpenAI</Text>
              <TextInput
                style={[styles.input, { backgroundColor: c.bgInput, color: c.textPrimary, borderColor: c.border }]}
                placeholder="sk-..."
                placeholderTextColor={c.textMuted}
                value={openAiApiKey}
                onChangeText={setOpenAiApiKey}
                autoCapitalize="none"
                autoCorrect={false}
                secureTextEntry
              />
              <Text style={{ color: c.textMuted, fontSize: 11, marginTop: 4 }}>
                {providerKeyStatusLabel(providerKeyStates["openai:host-vault"] || providerKeyStates["openai:phone-local"])}
              </Text>

              <Text style={{ color: c.textPrimary, fontWeight: "600", fontSize: 13, marginTop: 12 }}>GLM / Z.ai</Text>
              <TextInput
                style={[styles.input, { backgroundColor: c.bgInput, color: c.textPrimary, borderColor: c.border }]}
                placeholder="zai_..."
                placeholderTextColor={c.textMuted}
                value={glmApiKey}
                onChangeText={setGlmApiKey}
                autoCapitalize="none"
                autoCorrect={false}
                secureTextEntry
              />
              <Text style={{ color: c.textMuted, fontSize: 11, marginTop: 4 }}>
                {providerKeyStatusLabel(providerKeyStates["glm:host-vault"] || providerKeyStates["glm:phone-local"])}
              </Text>

              <Text style={{ color: c.textPrimary, fontWeight: "600", fontSize: 13, marginTop: 12 }}>Anthropic / Claude API</Text>
              <TextInput
                style={[styles.input, { backgroundColor: c.bgInput, color: c.textPrimary, borderColor: c.border }]}
                placeholder="sk-ant-..."
                placeholderTextColor={c.textMuted}
                value={anthropicApiKey}
                onChangeText={setAnthropicApiKey}
                autoCapitalize="none"
                autoCorrect={false}
                secureTextEntry
              />
              <Text style={{ color: c.textMuted, fontSize: 11, marginTop: 4 }}>
                {providerKeyStatusLabel(providerKeyStates["anthropic:host-vault"] || providerKeyStates["anthropic:phone-local"])}
              </Text>
              <Text style={{ color: c.textMuted, fontSize: 11, marginTop: 6 }}>
                Yaver account signup still uses Apple, Google, Microsoft, or email. OpenAI and Claude are connected as tooling providers, not Yaver login providers.
              </Text>
              <Text style={{ color: c.textMuted, fontSize: 11, marginTop: 6 }}>
                Host vault sync is owner-only. Guest sessions do not get raw provider secrets unless the host explicitly enables host-managed key usage.
              </Text>
              <Text style={{ color: c.textMuted, fontSize: 11, marginTop: 6 }}>
                Shared infra policy: host can share compute only, or compute plus host-managed keys. Guests never receive raw provider values from this screen.
              </Text>

              <View style={{ flexDirection: "row", gap: 8, marginTop: 14 }}>
                <Pressable
                  onPress={saveAiProviderSettings}
                  disabled={isSavingAiProviders}
                  style={({ pressed }) => [
                    {
                      flex: 1,
                      paddingVertical: 10,
                      borderRadius: 8,
                      backgroundColor: c.accent,
                      alignItems: "center",
                    },
                    pressed && { opacity: 0.7 },
                    isSavingAiProviders && { opacity: 0.5 },
                  ]}
                >
                  <Text style={{ color: "#fff", fontWeight: "600", fontSize: 14 }}>
                    {isSavingAiProviders ? "Saving..." : "Save AI Providers"}
                  </Text>
                </Pressable>
                <Pressable
                  onPress={syncAiProvidersToVault}
                  disabled={isSyncingAiVault || connectionStatus !== "connected" || !!activeDevice?.isGuest}
                  style={({ pressed }) => [
                    {
                      flex: 1,
                      paddingVertical: 10,
                      borderRadius: 8,
                      borderWidth: 1,
                      borderColor: c.border,
                      backgroundColor: c.bg,
                      alignItems: "center",
                      opacity: (isSyncingAiVault || connectionStatus !== "connected" || !!activeDevice?.isGuest) ? 0.5 : 1,
                    },
                    pressed && { opacity: 0.7 },
                  ]}
                >
                  <Text style={{ color: c.textPrimary, fontWeight: "600", fontSize: 14 }}>
                    {isSyncingAiVault ? "Syncing..." : "Sync To Host Vault"}
                  </Text>
                </Pressable>
              </View>

              <Pressable
                onPress={syncAiProvidersToRunners}
                disabled={isSyncingRunnerAuth || connectionStatus !== "connected" || !!activeDevice?.isGuest}
                style={({ pressed }) => [
                  {
                    marginTop: 10,
                    paddingVertical: 10,
                    borderRadius: 8,
                    borderWidth: 1,
                    borderColor: c.border,
                    backgroundColor: c.bg,
                    alignItems: "center",
                    opacity: (isSyncingRunnerAuth || connectionStatus !== "connected" || !!activeDevice?.isGuest) ? 0.5 : 1,
                  },
                  pressed && { opacity: 0.7 },
                ]}
              >
                <Text style={{ color: c.textPrimary, fontWeight: "600", fontSize: 14 }}>
                  {isSyncingRunnerAuth ? "Syncing..." : "Sync To Host Runners"}
                </Text>
              </Pressable>

              <View style={{ marginTop: 12, gap: 6 }}>
                {runnerIncidents.length > 0 && (
                  <View style={{ borderWidth: 1, borderColor: "#ef444466", backgroundColor: "#2a0a0a", borderRadius: 10, padding: 10, marginBottom: 6 }}>
                    <Text style={{ color: "#fca5a5", fontWeight: "700", fontSize: 12 }}>
                      Runner blockers
                    </Text>
                    {runnerIncidents.slice(0, 3).map((incident) => (
                      <Text key={incident.id} style={{ color: "#fecaca", fontSize: 11, marginTop: 4 }}>
                        {incident.title}: {incident.userMessage}
                      </Text>
                    ))}
                  </View>
                )}
                {runnerAuthRows.length === 0 ? (
                  <Text style={{ color: c.textMuted, fontSize: 11 }}>
                    {connectionStatus === "connected" && !activeDevice?.isGuest
                      ? "Runner auth status will appear here."
                      : "Connect to your own machine to inspect runner auth."}
                  </Text>
                ) : (
                  runnerAuthRows.map((row) => (
                    <Text key={row.id} style={{ color: row.ready ? c.success : c.textMuted, fontSize: 11 }}>
                      {row.name}: {runnerCapabilitySnapshot?.targets?.[`runner-${row.id}`]?.reason || row.detail || (row.ready ? "ready" : "needs auth")}
                    </Text>
                  ))
                )}
              </View>

              <View style={[styles.separator, { backgroundColor: c.borderSubtle, marginVertical: 16 }]} />

              <YaverAgentSettings connected={connectionStatus === "connected" && !activeDevice?.isGuest} />

              <View style={[styles.separator, { backgroundColor: c.borderSubtle, marginVertical: 16 }]} />

              <BoxInitSection c={c} token={token} />

              <View style={[styles.separator, { backgroundColor: c.borderSubtle, marginVertical: 16 }]} />

              <Text style={{ color: c.textPrimary, fontWeight: "700", fontSize: 15 }}>Remote machine onboarding</Text>
              <Text style={{ color: c.textMuted, fontSize: 12, marginTop: 4 }}>
                Connect GitHub or GitLab directly on an owned runtime so clone, pull, push, and deploy flows work there without pasting a PAT.
              </Text>

              <View style={{
                marginTop: 14,
                borderWidth: 1,
                borderColor: c.border,
                borderRadius: 12,
                backgroundColor: c.bgCardElevated,
                padding: 12,
                gap: 10,
              }}>
                <Text style={{ color: c.textPrimary, fontWeight: "600", fontSize: 13 }}>Step 1 · Connect provider to Yaver</Text>
                <Text style={{ color: c.textMuted, fontSize: 11, lineHeight: 16 }}>
                  Optional for sign-in and account recovery. Runtime authorization below is what grants repo access.
                </Text>
                <View style={{ flexDirection: "row", flexWrap: "wrap", gap: 8 }}>
                  {(["github", "gitlab"] as const).map((provider) => {
                    const linked = identities.some((identity) => identity.provider === provider);
                    const disabled = linkingProvider !== null || linked;
                    return (
                      <Pressable
                        key={provider}
                        onPress={() => handleLinkProvider(provider)}
                        disabled={disabled}
                        style={({ pressed }) => [
                          {
                            flexDirection: "row",
                            alignItems: "center",
                            gap: 8,
                            paddingHorizontal: 12,
                            paddingVertical: 10,
                            borderRadius: 10,
                            borderWidth: 1,
                            borderColor: linked ? c.success : c.border,
                            backgroundColor: linked ? `${c.success}22` : c.bg,
                            opacity: disabled ? 0.7 : 1,
                          },
                          pressed && !disabled && { opacity: 0.8 },
                        ]}
                      >
                        {providerBadge(provider)}
                        <Text style={{ color: linked ? c.success : c.textPrimary, fontWeight: "600", fontSize: 12 }}>
                          {linked ? `${provider} linked` : linkingProvider === provider ? "Opening…" : `Connect ${provider}`}
                        </Text>
                      </Pressable>
                    );
                  })}
                </View>
                {authError ? (
                  <Text style={{ color: c.error, fontSize: 11 }}>
                    {authError}
                  </Text>
                ) : null}
              </View>

              <Text style={{ color: c.textPrimary, fontWeight: "600", fontSize: 13, marginTop: 14 }}>Step 2 · Populate owned live boxes</Text>
              <Text style={{ color: c.textMuted, fontSize: 11, marginTop: 4 }}>
                Pick one runtime for browser authorization, or select one or more runtimes for manual token fallback.
              </Text>
              <View style={{ flexDirection: "row", flexWrap: "wrap", gap: 8, marginTop: 10 }}>
                {onboardingTargetCandidates.length === 0 ? (
                  <Text style={{ color: c.textMuted, fontSize: 11 }}>
                    No owned live machines available yet.
                  </Text>
                ) : (
                  onboardingTargetCandidates.map((device) => {
                    const selected = selectedOnboardingTargetIds.includes(device.id);
                    return (
                      <Pressable
                        key={device.id}
                        onPress={() => {
                          setSelectedOnboardingTargetIds((current) => (
                            current.includes(device.id)
                              ? current.filter((id) => id !== device.id)
                              : [...current, device.id]
                          ));
                        }}
                        style={{
                          paddingHorizontal: 12,
                          paddingVertical: 10,
                          borderRadius: 10,
                          borderWidth: 1,
                          borderColor: selected ? c.accent : c.border,
                          backgroundColor: selected ? `${c.accent}22` : c.bg,
                          minWidth: 150,
                        }}
                      >
                        <Text style={{ color: selected ? c.accent : c.textPrimary, fontWeight: "600", fontSize: 13 }}>
                          {device.name}
                        </Text>
                        <Text style={{ color: c.textMuted, fontSize: 11, marginTop: 3 }}>
                          {device.id === activeDevice?.id ? "connected host" : "via peer proxy"} · {device.os || "machine"}
                        </Text>
                      </Pressable>
                    );
                  })
                )}
              </View>

              <View style={{
                marginTop: 12,
                borderWidth: 1,
                borderColor: c.border,
                borderRadius: 10,
                backgroundColor: c.bgCardElevated,
                padding: 12,
                gap: 10,
              }}>
                <Text style={{ color: c.textPrimary, fontWeight: "600", fontSize: 13 }}>Authorize in browser</Text>
                <View style={{ flexDirection: "row", gap: 8 }}>
                  {(["github", "gitlab"] as const).map((provider) => {
                    const busy = startingGitOAuthProvider === provider;
                    const disabled = !!startingGitOAuthProvider || connectionStatus !== "connected" || !!activeDevice?.isGuest || selectedOnboardingTargets.length !== 1;
                    return (
                      <Pressable
                        key={provider}
                        onPress={() => void startGitOAuthOnboarding(provider)}
                        disabled={disabled}
                        style={({ pressed }) => [
                          {
                            flex: 1,
                            paddingVertical: 10,
                            borderRadius: 8,
                            backgroundColor: c.accent,
                            alignItems: "center",
                            opacity: disabled ? 0.5 : 1,
                          },
                          pressed && !disabled && { opacity: 0.75 },
                        ]}
                      >
                        <Text style={{ color: "#fff", fontWeight: "600", fontSize: 13 }}>
                          {busy ? "Starting..." : `Authorize ${provider}`}
                        </Text>
                      </Pressable>
                    );
                  })}
                </View>
                {gitOAuthFlow ? (
                  <View style={{ borderTopWidth: 1, borderTopColor: c.borderSubtle, paddingTop: 10, gap: 6 }}>
                    <Text style={{ color: c.textPrimary, fontWeight: "600", fontSize: 12 }}>
                      {gitOAuthFlow.provider} on {gitOAuthFlow.deviceName}: {gitOAuthFlow.state}
                    </Text>
                    {gitOAuthFlow.state === "pending" ? (
                      <View style={{ flexDirection: "row", gap: 8 }}>
                        <Pressable
                          onPress={() => Linking.openURL(gitOAuthFlow.verificationUri).catch(() => {})}
                          style={{ flex: 1, paddingVertical: 9, borderRadius: 8, borderWidth: 1, borderColor: c.border, alignItems: "center" }}
                        >
                          <Text style={{ color: c.textPrimary, fontWeight: "600", fontSize: 12 }}>Open browser</Text>
                        </Pressable>
                        <Pressable
                          onPress={() => ExpoClipboard.setStringAsync(gitOAuthFlow.userCode)}
                          style={{ flex: 1, paddingVertical: 9, borderRadius: 8, borderWidth: 1, borderColor: c.border, alignItems: "center" }}
                        >
                          <Text style={{ color: c.textPrimary, fontWeight: "600", fontSize: 12 }}>Copy {gitOAuthFlow.userCode}</Text>
                        </Pressable>
                      </View>
                    ) : (
                      <Text style={{ color: gitOAuthFlow.state === "done" ? c.success : c.error, fontSize: 11 }}>
                        {gitOAuthFlow.state === "done"
                          ? `Ready${gitOAuthFlow.username ? ` as ${gitOAuthFlow.username}` : ""}.`
                          : gitOAuthFlow.error || "Authorization did not complete."}
                      </Text>
                    )}
                  </View>
                ) : null}
              </View>

              <Text style={{ color: c.textPrimary, fontWeight: "600", fontSize: 13, marginTop: 14 }}>Manual token fallback</Text>
              <Text style={{ color: c.textMuted, fontSize: 11, marginTop: 4 }}>
                Use this only when browser authorization is unavailable.
              </Text>

              <Text style={{ color: c.textPrimary, fontWeight: "600", fontSize: 13, marginTop: 14 }}>GitHub token</Text>
              <TextInput
                style={[styles.input, { backgroundColor: c.bgInput, color: c.textPrimary, borderColor: c.border }]}
                placeholder="ghp_..."
                placeholderTextColor={c.textMuted}
                value={githubToken}
                onChangeText={setGithubToken}
                autoCapitalize="none"
                autoCorrect={false}
                secureTextEntry
              />

              <Text style={{ color: c.textPrimary, fontWeight: "600", fontSize: 13, marginTop: 12 }}>GitLab token</Text>
              <TextInput
                style={[styles.input, { backgroundColor: c.bgInput, color: c.textPrimary, borderColor: c.border }]}
                placeholder="glpat-..."
                placeholderTextColor={c.textMuted}
                value={gitlabToken}
                onChangeText={setGitlabToken}
                autoCapitalize="none"
                autoCorrect={false}
                secureTextEntry
              />

              <Text style={{ color: c.textPrimary, fontWeight: "600", fontSize: 13, marginTop: 12 }}>GitLab host</Text>
              <TextInput
                style={[styles.input, { backgroundColor: c.bgInput, color: c.textPrimary, borderColor: c.border }]}
                placeholder="gitlab.com"
                placeholderTextColor={c.textMuted}
                value={gitlabHost}
                onChangeText={setGitlabHost}
                autoCapitalize="none"
                autoCorrect={false}
              />

              <Pressable
                onPress={applyMachineOnboarding}
                disabled={isApplyingMachineOnboarding || connectionStatus !== "connected" || !!activeDevice?.isGuest}
                style={({ pressed }) => [
                  {
                    marginTop: 10,
                    paddingVertical: 10,
                    borderRadius: 8,
                    backgroundColor: c.accent,
                    alignItems: "center",
                    opacity: (isApplyingMachineOnboarding || connectionStatus !== "connected" || !!activeDevice?.isGuest) ? 0.5 : 1,
                  },
                  pressed && { opacity: 0.7 },
                ]}
              >
                <Text style={{ color: "#fff", fontWeight: "600", fontSize: 14 }}>
                  {isApplyingMachineOnboarding ? "Applying..." : `Apply To ${Math.max(1, selectedOnboardingTargetIds.length)} Machine${selectedOnboardingTargetIds.length === 1 ? "" : "s"}`}
                </Text>
              </Pressable>
              <View style={{ flexDirection: "row", gap: 8, marginTop: 8 }}>
                <Pressable
                  onPress={() => void removeMachineOnboarding("github")}
                  disabled={!!removingOnboardingProvider || connectionStatus !== "connected" || !!activeDevice?.isGuest}
                  style={({ pressed }) => [
                    {
                      flex: 1,
                      paddingVertical: 10,
                      borderRadius: 8,
                      borderWidth: 1,
                      borderColor: c.error,
                      alignItems: "center",
                      opacity: (!!removingOnboardingProvider || connectionStatus !== "connected" || !!activeDevice?.isGuest) ? 0.5 : 1,
                    },
                    pressed && { opacity: 0.7 },
                  ]}
                >
                  <Text style={{ color: c.error, fontWeight: "600", fontSize: 13 }}>
                    {removingOnboardingProvider === "github" ? "Removing GitHub..." : "Remove GitHub"}
                  </Text>
                </Pressable>
                <Pressable
                  onPress={() => void removeMachineOnboarding("gitlab")}
                  disabled={!!removingOnboardingProvider || connectionStatus !== "connected" || !!activeDevice?.isGuest}
                  style={({ pressed }) => [
                    {
                      flex: 1,
                      paddingVertical: 10,
                      borderRadius: 8,
                      borderWidth: 1,
                      borderColor: c.error,
                      alignItems: "center",
                      opacity: (!!removingOnboardingProvider || connectionStatus !== "connected" || !!activeDevice?.isGuest) ? 0.5 : 1,
                    },
                    pressed && { opacity: 0.7 },
                  ]}
                >
                  <Text style={{ color: c.error, fontWeight: "600", fontSize: 13 }}>
                    {removingOnboardingProvider === "gitlab" ? "Removing GitLab..." : "Remove GitLab"}
                  </Text>
                </Pressable>
              </View>

              <View style={{ marginTop: 12, gap: 6 }}>
                {selectedOnboardingTargets.length === 0 ? (
                  <Text style={{ color: c.textMuted, fontSize: 11 }}>
                    Select one or more owned machines to inspect onboarding state.
                  </Text>
                ) : Object.keys(machineOnboardingRowsByDevice).length === 0 ? (
                  <Text style={{ color: c.textMuted, fontSize: 11 }}>
                    {connectionStatus === "connected" && !activeDevice?.isGuest
                      ? "OpenAI / GitHub / GitLab status will appear here for the selected machines."
                      : "Connect to your own machine to inspect provider onboarding."}
                  </Text>
                ) : (
                  selectedOnboardingTargets.map((device) => (
                    <View
                      key={device.id}
                      style={{
                        borderWidth: 1,
                        borderColor: c.border,
                        backgroundColor: c.bgCardElevated,
                        borderRadius: 10,
                        padding: 10,
                      }}
                    >
                      <Text style={{ color: c.textPrimary, fontWeight: "700", fontSize: 12 }}>
                        {device.name}
                      </Text>
                      <Text style={{ color: c.textMuted, fontSize: 10, marginTop: 2 }}>
                        {device.id === activeDevice?.id ? "connected host" : "live peer"} · {device.os || "machine"}
                      </Text>
                      <View style={{ marginTop: 8, gap: 6 }}>
                        {(machineOnboardingRowsByDevice[device.id] || []).length === 0 ? (
                          <Text style={{ color: c.textMuted, fontSize: 11 }}>
                            No onboarding status loaded yet.
                          </Text>
                        ) : (
                          (machineOnboardingRowsByDevice[device.id] || []).map((row) => (
                            <Text key={`${device.id}:${row.id}`} style={{ color: row.ready ? c.success : row.configured ? c.accent : c.textMuted, fontSize: 11 }}>
                              {row.name}: {row.detail || row.warning || (row.ready ? "ready" : "not configured")}
                            </Text>
                          ))
                        )}
                      </View>
                    </View>
                  ))
                )}
              </View>

              {!LEAN_SETTINGS_SURFACE && SHOW_HOST_NOTIFICATION_CHANNELS ? (
                <>
                  <View style={[styles.separator, { backgroundColor: c.borderSubtle, marginVertical: 16 }]} />
                  {intgLoading ? (
                    <ActivityIndicator color={c.accent} />
                  ) : connectionStatus !== "connected" ? (
                    <Text style={{ color: c.textMuted, fontSize: 13 }}>Connect to a device to configure host-side notifications.</Text>
                  ) : (
                    <>
                      <Text style={{ color: c.textMuted, fontSize: 12, marginBottom: 12 }}>
                        Get notified when tasks complete. Configure channels below.
                      </Text>
                      <Pressable
                        onPress={async () => {
                          setIntgSaving(true);
                          const ok = await quicClient.saveNotificationsConfig(intgConfig);
                          setIntgSaving(false);
                          Alert.alert(ok ? "Saved" : "Error", ok ? "Integrations saved." : "Failed to save.");
                        }}
                        style={({ pressed }) => [
                          { marginTop: 8, paddingVertical: 10, paddingHorizontal: 16, borderRadius: 8, backgroundColor: c.accent, alignItems: "center" as const },
                          pressed && { opacity: 0.7 },
                        ]}
                      >
                        {intgSaving ? (
                          <ActivityIndicator color="#fff" size="small" />
                        ) : (
                          <Text style={{ color: "#fff", fontWeight: "600", fontSize: 14 }}>Save Integrations</Text>
                        )}
                      </Pressable>
                    </>
                  )}
                </>
              ) : null}
            </View>
          )}
        </View>

        {!LEAN_SETTINGS_SURFACE && <View style={styles.section}>
          <Text style={[styles.sectionLabel, { color: c.textSecondary }]}>Sign-in methods</Text>
          <View style={[styles.card, { backgroundColor: c.bgCard, borderColor: c.border }]}>
            <Text style={{ color: c.textPrimary, fontWeight: "600", fontSize: 15 }}>
              {user?.provider ? `Primary: ${user.provider}` : "Manage linked providers"}
            </Text>
            <Text style={{ color: c.textMuted, fontSize: 13, marginTop: 8, lineHeight: 18 }}>
              Link Apple, Google, and Microsoft to the same Yaver account so any of them opens the same machines and devices. If you accidentally created a split account, linking from the web dashboard will merge it back into this account.
            </Text>
            <Pressable
              onPress={() => Linking.openURL("https://yaver.io/dashboard")}
              style={({ pressed }) => [
                styles.signOutButton,
                { marginTop: 14, backgroundColor: c.bgCardElevated, borderWidth: 1, borderColor: c.border },
                pressed && { opacity: 0.7 },
              ]}
            >
              <Text style={{ color: c.textPrimary, fontWeight: "600" }}>Manage on yaver.io</Text>
            </Pressable>
          </View>
        </View>}

        {/* Change Password (email/password identity only) */}
        {emailPasswordEnabled && hasEmailPassword && (
          <View style={styles.section}>
            <Pressable
              style={({ pressed }) => [
                styles.signOutButton,
                { backgroundColor: c.bgCard, borderWidth: 1, borderColor: c.border },
                pressed && { opacity: 0.7 },
              ]}
              onPress={() => {
                Alert.prompt(
                  "Change Password",
                  "Enter your current password:",
                  [
                    { text: "Cancel", style: "cancel" },
                    {
                      text: "Next",
                      onPress: (currentPw?: string) => {
                        if (!currentPw) return;
                        Alert.prompt(
                          "New Password",
                          "Enter your new password (min 8 characters):",
                          [
                            { text: "Cancel", style: "cancel" },
                            {
                              text: "Change",
                              onPress: async (newPw?: string) => {
                                if (!newPw || newPw.length < 8) {
                                  Alert.alert("Password Too Short", "Your new password must be at least 8 characters.");
                                  return;
                                }
                                try {
                                  await changePasswordApi(token!, currentPw, newPw);
                                  Alert.alert("Success", "Password changed successfully.");
                                } catch (e: any) {
                                  Alert.alert(
                                    "Couldn't Change Password",
                                    `Yaver couldn't change your password. Make sure your current password is correct, then check your connection and try again.${e?.message ? `\n\n${e.message}` : ""}`,
                                  );
                                }
                              },
                            },
                          ],
                          "secure-text"
                        );
                      },
                    },
                  ],
                  "secure-text"
                );
              }}
            >
              <Text style={[styles.signOutText, { color: c.textPrimary }]}>Change Password</Text>
            </Pressable>
          </View>
        )}

        {/* Sign out */}
        <View style={styles.section}>
          <Pressable
            style={({ pressed }) => [
              styles.signOutButton,
              { backgroundColor: c.errorBg },
              pressed && styles.signOutPressed,
            ]}
            onPress={handleSignOut}
          >
            <Text style={[styles.signOutText, { color: c.error }]}>Sign Out</Text>
          </Pressable>
        </View>

        {/* Factory Reset */}
        <View style={styles.section}>
          <Pressable
            style={({ pressed }) => [
              styles.signOutButton,
              { backgroundColor: c.bgCard, borderWidth: 1, borderColor: c.border },
              pressed && { opacity: 0.7 },
            ]}
            onPress={() => {
              Alert.alert(
                "Factory Reset",
                "This will remove all local settings, saved API keys, relay servers, cached data, and speech preferences. Your account will NOT be deleted.\n\nYou will need to sign in again and go through setup.",
                [
                  { text: "Cancel", style: "cancel" },
                  {
                    text: "Reset Everything",
                    style: "destructive",
                    onPress: async () => {
                      try {
                        // Clear local secrets
                        for (const key of Object.values(LOCAL_KEYS)) {
                          await deleteLocalSecret(key);
                        }
                        // Clear key storage preference
                        await deleteLocalSecret("yaver_key_storage_pref");
                        // Clear cached data
                        await clearCache();
                        // Clear AsyncStorage (relays, tunnels, etc.)
                        await AsyncStorage.clear();
                        // Clear cloud settings
                        if (token) {
                          await saveUserSettings(token, {
                            speechProvider: undefined,
                            speechApiKey: undefined,
                            openAiApiKey: undefined,
                            glmApiKey: undefined,
                            mobileCodingProvider: undefined,
                            ttsEnabled: undefined,
                            ttsProvider: undefined,
                            verbosity: undefined,
                            runnerId: undefined,
                            customRunnerCommand: undefined,
                            forceRelay: undefined,
                          });
                        }
                        // Sign out
                        handleSignOut();
                      } catch {
                        Alert.alert("Reset Didn't Finish", "Yaver couldn't fully reset. Check your connection and try again.");
                      }
                    },
                  },
                ]
              );
            }}
          >
            <Text style={[styles.signOutText, { color: c.textSecondary }]}>Factory Reset</Text>
          </Pressable>
        </View>

        {/* Bring your own cloud (Hetzner / DigitalOcean) — connect your
            own provider token, run boxes on your account, pay the
            provider directly. Token stored encrypted on the agent. */}
        <View style={styles.section}>
          <CloudProvidersSection c={c} token={token} />
        </View>

        {/* Phone-DIRECT Hetzner: wire a token once, manage boxes from the
            phone with no paired agent (api.hetzner.cloud called directly;
            token stays in the device keychain). */}
        <View style={styles.section}>
          <HetznerSection c={c} token={token} />
        </View>

        {/* Delete account */}
        <View style={styles.section}>
          <Text style={[styles.sectionLabel, { color: c.error }]}>Danger Zone</Text>
          <View style={[styles.card, { backgroundColor: c.bgCard, borderColor: c.error + "30", marginBottom: 12 }]}>
            <Text style={[styles.dangerDescription, { color: c.textMuted }]}>
              Permanently remove Yaver from the connected host machine. This unregisters the device, removes auto-start, wipes <Text style={{ color: c.textSecondary, fontFamily: "monospace" }}>~/.yaver</Text>, and stops the agent. Your source code repositories are not deleted.
            </Text>
            <Text style={[styles.dangerHint, { color: c.textMuted }]}>
              Type <Text style={{ color: c.textSecondary, fontFamily: "monospace" }}>delete my machine</Text> to confirm:
            </Text>
            <TextInput
              style={[styles.deleteInput, { backgroundColor: c.bgCardElevated, borderColor: machineDeleteConfirm === "delete my machine" ? c.error : c.border, color: c.textPrimary }]}
              value={machineDeleteConfirm}
              onChangeText={setMachineDeleteConfirm}
              placeholder="delete my machine"
              placeholderTextColor={c.textMuted}
              autoCapitalize="none"
              editable={!removingMachine}
            />
            <Pressable
              style={({ pressed }) => [
                styles.deleteAccountButton,
                { borderColor: c.error + "30" },
                machineDeleteConfirm === "delete my machine" && connectionStatus === "connected" && activeDevice && !activeDevice.isGuest
                  ? { backgroundColor: c.error + "15" }
                  : { opacity: 0.3 },
                pressed && machineDeleteConfirm === "delete my machine" && { opacity: 0.7 },
              ]}
              onPress={handleRemoveMachine}
              disabled={machineDeleteConfirm !== "delete my machine" || removingMachine || connectionStatus !== "connected" || !activeDevice || activeDevice.isGuest}
            >
              <Text style={[styles.deleteAccountText, { color: c.error }]}>
                {removingMachine ? "Removing..." : activeDevice?.isGuest ? "Owner machine required" : "Remove Yaver From This Host"}
              </Text>
            </Pressable>
            {machineRemoveSteps.length > 0 ? (
              <View style={{ marginTop: 12, padding: 8, borderRadius: 8, backgroundColor: c.bgCardElevated, borderWidth: 1, borderColor: c.border }}>
                {machineRemoveSteps.map((s, i) => (
                  <View key={i} style={{ flexDirection: "row", gap: 6, paddingVertical: 2 }}>
                    <Text style={{ width: 14, fontFamily: "monospace", fontSize: 11, color: s.status === "ok" ? "#10b981" : s.status === "error" ? c.error : s.status === "skipped" ? c.textMuted : "#f59e0b" }}>
                      {s.status === "ok" ? "✓" : s.status === "error" ? "✗" : s.status === "skipped" ? "—" : "›"}
                    </Text>
                    <Text style={{ minWidth: 96, fontFamily: "monospace", fontSize: 11, color: c.textMuted }}>{s.step}</Text>
                    <Text style={{ flex: 1, fontFamily: "monospace", fontSize: 11, color: s.status === "error" ? c.error : c.textSecondary }}>
                      {s.error ? s.error : s.detail}
                    </Text>
                  </View>
                ))}
              </View>
            ) : null}
          </View>
          <View style={[styles.card, { backgroundColor: c.bgCard, borderColor: c.error + "30" }]}>
            <Text style={[styles.dangerDescription, { color: c.textMuted }]}>
              Permanently delete your account and all associated data. This action cannot be undone.
            </Text>
            <Text style={[styles.dangerHint, { color: c.textMuted }]}>
              Type <Text style={{ color: c.textSecondary, fontFamily: "monospace" }}>delete my account</Text> to confirm:
            </Text>
            <TextInput
              style={[styles.deleteInput, { backgroundColor: c.bgCardElevated, borderColor: deleteConfirm === "delete my account" ? c.error : c.border, color: c.textPrimary }]}
              value={deleteConfirm}
              onChangeText={setDeleteConfirm}
              placeholder="delete my account"
              placeholderTextColor={c.textMuted}
              autoCapitalize="none"
              editable={!deletingAccount}
            />
            <Pressable
              style={({ pressed }) => [
                styles.deleteAccountButton,
                { borderColor: c.error + "30" },
                deleteConfirm === "delete my account"
                  ? { backgroundColor: c.error + "15" }
                  : { opacity: 0.3 },
                pressed && deleteConfirm === "delete my account" && { opacity: 0.7 },
              ]}
              onPress={handleDeleteAccount}
              disabled={deleteConfirm !== "delete my account" || deletingAccount}
            >
              <Text style={[styles.deleteAccountText, { color: c.error }]}>
                {deletingAccount ? "Deleting..." : "Delete My Account"}
              </Text>
            </Pressable>
          </View>
        </View>
      </ScrollView>
      </KeyboardAvoidingView>
      <OpenCodeConfigModal
        visible={showOpenCodeConfig}
        startInAddProvider={openCodeStartInAddProvider}
        onClose={() => {
          setShowOpenCodeConfig(false);
          setOpenCodeStartInAddProvider(false);
        }}
      />
    </View>
  );
}

const styles = StyleSheet.create({
  safeArea: { flex: 1 },
  container: { flex: 1 },
  scrollContent: { padding: 16, paddingBottom: 120 },

  // Tightened from 32/12 → 18/8: with ~18 stacked sections the old
  // spacing made Settings an endless scroll even though every heavy
  // section already collapses by default. Pure-spacing compaction.
  section: { marginBottom: 18 },
  sectionLabel: {
    fontSize: 12,
    fontWeight: "600",
    textTransform: "uppercase",
    letterSpacing: 0.5,
    marginBottom: 8,
  },

  profileCard: {
    flexDirection: "row",
    alignItems: "center",
    borderRadius: 12,
    padding: 16,
    borderWidth: 1,
  },
  avatar: {
    width: 48,
    height: 48,
    borderRadius: 24,
    alignItems: "center",
    justifyContent: "center",
    marginRight: 14,
  },
  avatarText: { fontSize: 20, fontWeight: "700" },
  profileInfo: { flex: 1 },
  profileName: { fontSize: 16, fontWeight: "600" },
  profileEmail: { fontSize: 13, marginTop: 2 },
  editNameRow: { flexDirection: "row", alignItems: "center", gap: 8, flex: 1 },
  editNameInput: {
    flex: 1,
    borderWidth: 1,
    borderRadius: 8,
    paddingVertical: 6,
    paddingHorizontal: 10,
    fontSize: 15,
  },
  editNameButton: {
    borderRadius: 8,
    paddingVertical: 6,
    paddingHorizontal: 12,
  },
  editNameButtonText: { color: "#fff", fontSize: 13, fontWeight: "600" },

  card: {
    borderRadius: 12,
    padding: 13,
    borderWidth: 1,
    marginBottom: 6,
  },

  // Device
  deviceRow: {
    flexDirection: "row",
    alignItems: "center",
    justifyContent: "space-between",
  },
  deviceInfo: { flex: 1 },
  deviceName: { fontSize: 16, fontWeight: "600" },
  deviceMeta: { fontSize: 12, marginTop: 2 },
  connectionDot: {
    width: 10,
    height: 10,
    borderRadius: 5,
    marginLeft: 12,
  },
  deviceDetails: {
    flexDirection: "row",
    marginTop: 14,
    paddingTop: 14,
    borderTopWidth: 1,
    gap: 24,
  },
  detailItem: {},
  detailLabel: { fontSize: 11, marginBottom: 2 },
  detailValue: { fontSize: 13 },
  noDeviceText: { fontSize: 14 },

  // Theme
  themeRow: {
    flexDirection: "row",
    justifyContent: "space-between",
    alignItems: "center",
  },
  themeLabel: { fontSize: 15 },

  // Action row
  actionRow: {
    flexDirection: "row",
    alignItems: "center",
    justifyContent: "space-between",
    borderRadius: 12,
    padding: 16,
    borderWidth: 1,
  },
  actionRowPressed: { opacity: 0.7 },
  actionRowLabel: { fontSize: 15 },
  actionRowChevron: { fontSize: 20 },

  // About
  aboutRow: {
    flexDirection: "row",
    justifyContent: "space-between",
    alignItems: "center",
    paddingVertical: 4,
  },
  aboutLabel: { fontSize: 15 },
  aboutValue: { fontSize: 15 },

  // Links
  linksCard: {
    borderRadius: 12,
    borderWidth: 1,
    overflow: "hidden",
  },
  linkRow: {
    flexDirection: "row",
    alignItems: "center",
    justifyContent: "space-between",
    padding: 16,
  },
  linkText: { fontSize: 15 },
  linkChevron: { fontSize: 20 },

  separator: {
    height: 1,
    marginHorizontal: 16,
  },

  signOutButton: {
    borderRadius: 12,
    padding: 16,
    alignItems: "center",
  },
  signOutPressed: { opacity: 0.7 },
  signOutText: { fontSize: 16, fontWeight: "600" },

  dangerDescription: { fontSize: 13, lineHeight: 19, marginBottom: 12 },
  dangerHint: { fontSize: 12, marginBottom: 8 },
  deleteInput: {
    borderRadius: 8,
    borderWidth: 1,
    padding: 12,
    fontSize: 14,
    marginBottom: 12,
  },
  // Logs
  logsContainer: {
    borderRadius: 12,
    borderWidth: 1,
    marginTop: 8,
    overflow: "hidden",
  },
  logsActions: {
    flexDirection: "row",
    justifyContent: "flex-end",
    gap: 16,
    paddingHorizontal: 12,
    paddingTop: 10,
    paddingBottom: 6,
  },
  logsActionBtn: { fontSize: 13, fontWeight: "600" },
  logsScroll: { maxHeight: 300, paddingHorizontal: 12, paddingBottom: 12 },
  logLine: { fontSize: 11, fontFamily: "monospace", lineHeight: 16, marginBottom: 1 },
  logEmpty: { fontSize: 13, textAlign: "center", paddingVertical: 20 },

  // AI Runner
  runnerOption: {
    flexDirection: "row",
    alignItems: "center",
    paddingVertical: 12,
    gap: 12,
    borderBottomWidth: StyleSheet.hairlineWidth,
  },
  radioOuter: {
    width: 20,
    height: 20,
    borderRadius: 10,
    borderWidth: 2,
    alignItems: "center",
    justifyContent: "center",
  },
  radioInner: {
    width: 10,
    height: 10,
    borderRadius: 5,
  },
  runnerName: {
    fontSize: 15,
    fontWeight: "500",
  },
  runnerDesc: {
    fontSize: 12,
    marginTop: 2,
  },
  customRunnerInput: {
    borderWidth: 1,
    borderRadius: 8,
    paddingVertical: 10,
    paddingHorizontal: 12,
    fontSize: 14,
    fontFamily: "monospace",
    marginTop: 8,
    marginLeft: 32,
  },

  deleteAccountButton: {
    borderRadius: 12,
    borderWidth: 1,
    padding: 14,
    alignItems: "center",
  },
  deleteAccountText: { fontSize: 14, fontWeight: "600" },

  // Relay input
  relayInput: {
    borderWidth: 1,
    borderRadius: 8,
    paddingVertical: 10,
    paddingHorizontal: 12,
    fontSize: 14,
  },
  input: {
    borderWidth: 1,
    borderRadius: 10,
    paddingVertical: 10,
    paddingHorizontal: 14,
    fontSize: 14,
  },
});

const metricsStyles = StyleSheet.create({
  chartContainer: {
    flexDirection: "row",
    alignItems: "flex-end",
    height: 60,
    gap: 1,
    backgroundColor: "rgba(255,255,255,0.03)",
    borderRadius: 6,
    paddingHorizontal: 2,
    paddingVertical: 2,
    overflow: "hidden",
  },
  bar: {
    flex: 1,
    minWidth: 2,
    borderRadius: 1,
  },
  timeLabels: {
    flexDirection: "row",
    justifyContent: "space-between",
    marginTop: 4,
  },
});
