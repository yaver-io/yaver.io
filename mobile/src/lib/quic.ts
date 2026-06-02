/**
 * QUIC client for P2P communication with the desktop agent.
 *
 * This is a placeholder implementation that uses HTTP as a fallback
 * transport until a native QUIC module is available for React Native.
 * The public API mirrors what the real QUIC transport will expose.
 *
 * Improvements over the initial version:
 * - EventEmitter-style output streaming with typed events
 * - Automatic reconnection with exponential backoff
 * - Observable connection state (disconnected | connecting | connected | error)
 * - Local task + output cache via AsyncStorage for offline / P2P sync
 */

import { Platform } from "react-native";
import AsyncStorage from "@react-native-async-storage/async-storage";
import {
  loadConnectionCache,
  persistConnectionCache,
  probeBaseFor,
  probeHeadersFor,
  type ConnectionCacheEntry,
  type ConnectionMode as CacheConnectionMode,
} from "./connectionCache";
import { cacheTaskList, cacheTaskOutput, getCachedTaskList, getDeletedTaskIds } from "./storage";
import { beaconListener } from "./beacon";
import NetInfo from "@react-native-community/netinfo";
import type { BuildInfo, BuildSummary } from "./builds";

// ── Types ────────────────────────────────────────────────────────────

export type TaskStatus = "queued" | "running" | "review" | "completed" | "failed" | "stopped";

// ── Vault + API key types (mirrors desktop/agent/vault.go + apikeys.go) ──
export type VaultCategory = "api-key" | "signing-key" | "ssh-key" | "git-credential" | "custom";

export interface VaultEntrySummary {
  name: string;
  category: VaultCategory;
  notes?: string;
  createdAt?: string;
  updatedAt?: string;
}

export interface VaultEntry extends VaultEntrySummary {
  value: string;
}

// ── Yaver Agent (control-plane LLM) types — mirrors yaver_agent_config.go ─

export type YaverAgentProviderId = "glm" | "anthropic" | "openai" | "openrouter";

export interface YaverAgentConfig {
  provider: YaverAgentProviderId | "";
  model: string;
  baseUrl?: string;
  hasApiKey: boolean;
  updatedAt?: number;
}

export interface YaverAgentProviderDefault {
  provider: YaverAgentProviderId;
  model: string;
  baseUrl?: string;
  label: string;
  note?: string;
}

export interface YaverAgentSetRequest {
  provider: YaverAgentProviderId;
  model?: string;
  baseUrl?: string;
  /** "" clears the stored key; omit to leave existing untouched. */
  apiKey?: string;
}

// /yaver-agent/audit response — mirrors yaver_agent_tools.go.

export interface YaverAgentRunnerAudit {
  id: string;
  name: string;
  installed: boolean;
  ready: boolean;
  authConfigured: boolean;
  authSource?: string;
  warning?: string;
  error?: string;
}

export interface YaverAgentRecommendation {
  kind: "yaver_auth_required" | "runner_auth_required" | "configured";
  target?: string;
  severity: "info" | "warn" | "error";
  title: string;
  body: string;
  /** Tool name the embedded LLM should call to act on this recommendation. */
  action?: string;
}

export interface YaverAgentDeviceAudit {
  deviceId?: string;
  lifecycleState: string; // bootstrap | yaver-auth-expired | ready-to-connect
  usable: boolean;
  needsAuth: boolean;
  runners: YaverAgentRunnerAudit[];
  recommendations: YaverAgentRecommendation[];
}

export interface APIKeyRecord {
  tokenHash: string;
  label: string;
  createdAt?: string;
  lastUsedAt?: string;
  usageCount?: number;
  rateLimitPerMin?: number;
  disabled?: boolean;
  scopes?: string[];
}

export interface SyncItem<T = any> {
  key: string;
  value?: T;
  updatedAt: number;
  updatedBy: string;
  deleted?: boolean;
}

export interface EnvironmentProjectSummary {
  path: string;
  branch?: string;
}

export interface EnvironmentRunnerSummary {
  id: string;
  name: string;
  command: string;
  installed: boolean;
  ready: boolean;
  authConfigured?: boolean;
  authSource?: string;
  warning?: string;
  error?: string;
}

export interface EnvironmentSyncSummary {
  kind: string;
  count: number;
}

export interface ToolchainGitCredentialSummary {
  host: string;
  username?: string;
  hasToken: boolean;
}

export interface EnvironmentProfile {
  generatedAt: string;
  sourceDeviceId?: string;
  hostname?: string;
  platform: string;
  arch: string;
  workDir?: string;
  discoveredProjects?: EnvironmentProjectSummary[];
  binaries?: { name: string; path: string; manager?: string }[];
  runners?: EnvironmentRunnerSummary[];
  syncKinds?: EnvironmentSyncSummary[];
  gitCredentials?: ToolchainGitCredentialSummary[];
}

export interface EnvironmentProfileApplyResult {
  ok: boolean;
  status: string;
  sourcePlatform?: string;
  targetPlatform: string;
  installPlan?: string[];
  installed?: string[];
  alreadyPresent?: string[];
  importedSyncKinds?: string[];
  manualSteps?: string[];
  projectHints?: string[];
  notes?: string[];
  removalPlan?: string[];
  removed?: string[];
  importedGitHosts?: string[];
  removedGitHosts?: string[];
}

// Matches desktop/agent/scheduler.go::ScheduledTask.
export interface ScheduledTask {
  id: string;
  title: string;
  description?: string;
  model?: string;
  runner?: string;
  customCommand?: string;
  runAt?: string;
  cron?: string;
  repeatInterval?: number;
  status: "scheduled" | "running" | "completed" | "failed" | "paused";
  lastRunAt?: string;
  lastTaskId?: string;
  nextRunAt?: string;
  runCount: number;
  maxRuns?: number;
  createdAt: string;
  history?: { taskId: string; status: string; startedAt: string; durationMs: number; costUsd?: number }[];
}

export interface ImageAttachment {
  base64: string;       // base64 encoded image data (no data URI prefix)
  mimeType: string;     // "image/jpeg" or "image/png"
  filename: string;     // e.g. "photo_001.jpg"
}

export interface ConversationTurn {
  role: "user" | "assistant";
  content: string;
  timestamp: string;
}

export interface HealthMonitorPing {
  status: string;
  responseMs: number;
  time: string;
  statusCode?: number;
  error?: string;
}

export interface HealthMonitorTarget {
  id: string;
  url: string;
  label?: string;
  status?: string;
  statusCode?: number;
  responseMs?: number;
  uptimePercent?: number;
  lastChecked?: string;
  history?: HealthMonitorPing[];
}

function normalizeHealthTarget(raw: any): HealthMonitorTarget {
  const history = Array.isArray(raw?.history)
    ? raw.history.map((entry: any) => ({
        status: entry?.status ?? "unknown",
        responseMs: typeof entry?.responseMs === "number" ? entry.responseMs : 0,
        time: entry?.time ?? entry?.checkedAt ?? "",
        statusCode: typeof entry?.statusCode === "number" ? entry.statusCode : undefined,
        error: typeof entry?.error === "string" ? entry.error : undefined,
      }))
    : undefined;

  return {
    id: String(raw?.id ?? raw?.targetId ?? ""),
    url: String(raw?.url ?? ""),
    label: typeof raw?.label === "string" ? raw.label : undefined,
    status: typeof raw?.status === "string" ? raw.status : undefined,
    statusCode: typeof raw?.statusCode === "number" ? raw.statusCode : undefined,
    responseMs: typeof raw?.responseMs === "number" ? raw.responseMs : undefined,
    uptimePercent: typeof raw?.uptimePercent === "number" ? raw.uptimePercent : undefined,
    lastChecked: raw?.lastChecked ?? raw?.checkedAt,
    history,
  };
}

export interface ConversationImportPlan {
  sourceLabel: string;
  sourceUrl?: string;
  fetchedUrl?: string;
  detectedTitle?: string;
  suggestedName?: string;
  normalizedText: string;
  productGoal: string;
  userProblem?: string;
  summary?: string;
  researchTopics?: string[];
  surfaces?: string[];
  technicalPlan?: string[];
  dataFlow?: string[];
  mvpScope?: string[];
  risks?: string[];
  assumptions?: string[];
  nextPrompt?: string;
  generatedPrompt: string;
}

// Mirrors desktop/agent/opencode_config.go OpenCodeConfigSummary.
export interface OpenCodeProviderSummary {
  id: string;
  name?: string;
  baseUrl?: string;
  models?: Array<{ id: string; name?: string }>;
}
export interface OpenCodeAgentSummary {
  name: string;
  model?: string;
  description?: string;
  isBuiltin?: boolean;
}
export interface OpenCodeConfigSummary {
  path: string;
  exists: boolean;
  defaultAgent?: string;
  model?: string;
  smallModel?: string;
  buildModel?: string;
  planModel?: string;
  providers?: OpenCodeProviderSummary[];
  models?: Array<{ id: string; name?: string; provider?: string }>;
  agents?: OpenCodeAgentSummary[];
  diagnostics?: string[];
}

export interface Task {
  id: string;
  title: string;
  description: string;
  status: TaskStatus;
  output: string[];
  resultText?: string;    // Extracted clean result from Claude
  costUsd?: number;       // Total API cost in USD
  inputTokens?: number;   // Tokens sent (prompt + cache reads + cache creation)
  outputTokens?: number;  // Tokens generated by the model
  runnerId?: string;      // Which runner executed this task (claude, codex, opencode)
  /** Model id the task was actually launched with (e.g.
   *  "claude-opus-4-7", "gpt-5.5"). Sourced from the agent's
   *  Task.Model JSON field — authoritative. UI surfaces should use
   *  this over the picker's `selectedModel` so a task that ran on a
   *  different device than the user is currently focused on doesn't
   *  show the wrong model label in its history card. */
  model?: string;
  source?: string;        // Task origin: "mobile", "mcp", "cli", "vibing", "vibing-cache", "todolist"
  turns?: ConversationTurn[];  // Full conversation history
  createdAt: number;
  updatedAt: number;
  /** Name of the device this task is executing on. */
  deviceName?: string;
  /** Tmux session name (only set for adopted sessions). */
  tmuxSession?: string;
  /** True if this task was adopted from an existing tmux session. */
  isAdopted?: boolean;
  /** Chain ID if this task is part of a sequential chain. */
  chainId?: string;
  /** 0-based position in the chain. */
  chainOrder?: number;
  /** Whether auto-retry is enabled for this task. */
  autoRetry?: boolean;
  /** Number of auto-retries so far. */
  autoRetryCount?: number;
  /** Max auto-retries allowed. */
  autoRetryMax?: number;
  /** Video summary: when the task was created with videoEnabled, the
   *  agent records a clip after completion via vibe-preview. The UI
   *  shows a "▶ Watch demo" button when videoStatus = "ready". */
  videoEnabled?: boolean;
  videoSource?: "browser" | "sim-ios" | "sim-android" | "phone";
  videoClipId?: string;
  videoStatus?: "queued" | "recording" | "ready" | "failed" | "stale";
}

export type AgentGraphStatus = "queued" | "running" | "completed" | "failed" | "stopped";
export type AgentNodeStatus = "pending" | "running" | "completed" | "failed" | "blocked" | "stopped";
export type AgentNodeKind = "chat" | "autoideas" | "autotest";

export interface AgentGraphNode {
  spec: {
    id: string;
    title: string;
    kind: AgentNodeKind;
    prompt?: string;
    dependsOn?: string[];
    runner?: string;
    model?: string;
    workDir?: string;
    project?: string;
    preferredDevice?: string;
    allowedDevices?: string[];
    allowedRunners?: string[];
  };
  status: AgentNodeStatus;
  taskId?: string;
  summary?: string;
  error?: string;
  startedAt?: string;
  finishedAt?: string;
  placement?: AgentNodePlacement;
  sliceContract?: TaskSliceContract;
}

export interface AgentGraphRun {
  id: string;
  name: string;
  workDir: string;
  template?: string;
  prompt?: string;
  runner?: string;
  model?: string;
  maxParallel: number;
  status: AgentGraphStatus;
  summary?: string;
  createdAt: string;
  startedAt?: string;
  finishedAt?: string;
  nodes: AgentGraphNode[];
}

export interface AgentNodePlacement {
  deviceId: string;
  deviceName?: string;
  runner?: string;
  model?: string;
  reason?: string;
}

export interface TaskSliceContract {
  runId?: string;
  nodeId?: string;
  deviceId?: string;
  deviceName?: string;
  sourceWorkDir?: string;
  effectiveWorkDir?: string;
  gitRemote?: string;
  gitBranch?: string;
  gitCommit?: string;
  isolationMode?: string;
}

export interface MachineRunnerCapability {
  id: string;
  name: string;
  installed: boolean;
  ready: boolean;
  authConfigured?: boolean;
  authSource?: string;
  warning?: string;
  error?: string;
}

export interface MachineCapabilities {
  supportsIos?: boolean;
  supportsAndroid?: boolean;
  supportsDocker?: boolean;
  supportsLocalLlm?: boolean;
  supportsTestFlight?: boolean;
  supportsPlayStore?: boolean;
  lowPower?: boolean;
  maxTaskSlots?: number;
  profile?: {
    path?: string;
    summary?: string;
    tags?: string[];
    signatures?: string[];
    preferredFor?: string[];
  };
  runners?: MachineRunnerCapability[];
}

export interface MachineInfo {
  deviceId: string;
  name: string;
  platform: string;
  os?: string;
  arch?: string;
  isLocal: boolean;
  isOnline: boolean;
  isShared?: boolean;
  hostName?: string;
  hostEmail?: string;
  provider?: string;
  currentWorkDir?: string;
  capabilities?: MachineCapabilities;
}

/**
 * One event off the agent's P2P bus. Mirrors `desktop/agent/bus.go`
 * + the headless clients so a single wire format covers RN, Node,
 * and the Go peers themselves. Topics are slash-separated; presence
 * lives at `peer/{deviceId}/online` | `ping` | `offline`.
 */
export interface BusEvent {
  id: string;
  topic: string;
  publisher: string;
  publishedAt: number;
  ttl?: number;
  qos: 0 | 1;
  payload?: unknown;
}

export interface InfraNetworkInterface {
  name: string;
  mac?: string;
  flags?: string;
  addresses?: string[];
}

export interface InfraRelaySummary {
  id: string;
  label?: string;
  httpUrl?: string;
  quicAddr?: string;
  region?: string;
  source: string;
  passwordRequired: boolean;
}

export interface InfraSharingSummary {
  isShared: boolean;
  accessScope?: string;
  pendingGuests: number;
  acceptedGuests: number;
}

export interface InfraCapabilities {
  terminal: boolean;
  mcp: boolean;
  devServices: boolean;
  systemServices: boolean;
  agentShutdown: boolean;
  hostReboot: boolean;
}

export interface InfraSummary {
  machine: MachineInfo;
  metrics?: {
    cpuPct?: number;
    ramUsed?: number;
    ramTotal?: number;
    ramPct?: number;
    diskUsed?: number;
    diskTotal?: number;
    diskPct?: number;
    netRxBps?: number;
    netTxBps?: number;
    uptime?: number;
    hostname?: string;
    os?: string;
    cores?: number;
  };
  devServices?: Array<{
    name: string;
    running: boolean;
    port: number;
    image?: string;
    container?: string;
    health: string;
    uptime?: string;
    memory?: string;
  }>;
  network?: InfraNetworkInterface[];
  relays?: InfraRelaySummary[];
  sharing: InfraSharingSummary;
  sandbox: SandboxStatus;
  capabilities: InfraCapabilities;
}

export interface IncidentEvent {
  id: string;
  timestamp: number;
  severity: "info" | "warn" | "error" | "fatal";
  category: string;
  code: string;
  source: string;
  title: string;
  userMessage: string;
  technicalInfo?: string;
  suggestedAction?: string;
  operationId?: string;
  deviceId?: string;
  projectPath?: string;
  target?: string;
  logsAvailable: boolean;
  logRefs?: string[];
  correlationId?: string;
  recoverable: boolean;
  metadata?: Record<string, unknown>;
  resolved?: boolean;
  resolvedAt?: number;
  resolutionNote?: string;
}

export interface IncidentSummary {
  total: number;
  open: number;
  resolved: number;
  byCategory: Record<string, number>;
  bySeverity: Record<string, number>;
  topReasonCodes?: string[];
  lastIncidentAt?: number;
}

export interface OperationState {
  id: string;
  kind: string;
  status: string;
  phase?: string;
  message?: string;
  progress?: number;
  deviceId?: string;
  projectPath?: string;
  startedAt: number;
  updatedAt: number;
  incidentIds?: string[];
  metadata?: Record<string, unknown>;
}

export interface CapabilityTargetReadiness {
  enabled: boolean;
  reasonCode?: string;
  reason?: string;
  suggestedAction?: string;
  notes?: string[];
}

export interface CapabilitySnapshot {
  generatedAt: string;
  machine: MachineInfo;
  infra: InfraSummary;
  connectivity: {
    directAvailable: boolean;
    relayConfigured: boolean;
    tunnelConfigured: boolean;
    tailscaleAvailable: boolean;
  };
  targets: Record<string, CapabilityTargetReadiness>;
}

export interface RemoteRuntimeTarget {
  id: string;
  label: string;
  platform: string;
  runtimeHostClass?: string;
  enabled: boolean;
  reason?: string;
  hostOs?: string;
  requiredCli?: string;
}

export interface RemoteRuntimeCapabilities {
  workDir: string;
  framework: string;
  executionMode: "rn-hermes" | "web-webview" | "native-webrtc" | "unsupported";
  primarySurface: "hermes" | "webview" | "webrtc" | "none";
  remoteRuntimeEligible: boolean;
  feedbackSdkCompatible: boolean;
  feedbackSdkNote?: string;
  feedbackControlProtocol?: string;
  supportedTransports?: string[];
  currentHostClass?: string;
  targets: RemoteRuntimeTarget[];
}

export interface RemoteRuntimeSession {
  id: string;
  workDir: string;
  framework: string;
  executionMode: "rn-hermes" | "web-webview" | "native-webrtc" | "unsupported";
  targetId: string;
  targetLabel: string;
  platform?: string;
  deviceId?: string;
  runtimeHostClass?: string;
  transportMode?: string;
  frameTransport?: string;
  status: string;
  lastCommand?: string;
  createdAt: string;
  updatedAt: string;
  note?: string;
}

export interface TmuxSession {
  name: string;
  windows: number;
  created: string;
  attached: boolean;
  relationship: "adopted" | "forked-by-yaver" | "unrelated";
  agentType?: string;
  mainPid?: number;
  panePreview?: string;
  taskId?: string;
}

export interface ModelInfo {
  id: string;
  name: string;
  description?: string;
  provider?: string;
  source?: string;
  isDefault?: boolean;
}

export interface RunnerInfo {
  id: string;
  name: string;
  command: string;
  installed: boolean;
  ready?: boolean;
  authConfigured?: boolean;
  authSource?: string;
  warning?: string;
  error?: string;
  supportsBrowserAuth?: boolean;
  supportsModelSelection?: boolean;
  modelSource?: string;
  isDefault: boolean;
  models: ModelInfo[];
}

/**
 * Wire shape for `POST /agent/runners/test`. Mirrors the Go agent's
 * `runnerTestResult` (see desktop/agent/runner_test_http.go) and the
 * web's RunnerTestResult so the mobile app sees identical data.
 */
export interface RunnerTestResult {
  ok: boolean;
  runner: string;
  /** Which check fired: "binary" / "auth" / "subprocess" / "daemon". */
  probe?: string;
  needsAuth?: boolean;
  supportsBrowserAuth?: boolean;
  output?: string;
  error?: string;
  durationMs: number;
  model?: string;
}

export interface RunnerAuthStatusRow {
  id: string;
  name: string;
  installed: boolean;
  ready: boolean;
  authConfigured: boolean;
  authSource?: string;
  warning?: string;
  error?: string;
  path?: string;
  detail?: string;
  needsAuth?: boolean;
  /** First line of `<bin> --version` output captured by the agent at
   *  status-collection time (e.g. "Claude Code 2.1.126", "codex-cli
   *  0.122.0", "1.4.0"). Empty if probe failed or agent is older
   *  than 1.99.147. */
  version?: string;
}

export interface RunnerAuthSetupParams {
  runner: "claude" | "claude-code" | "codex" | "opencode";
  openaiApiKey?: string;
  anthropicApiKey?: string;
  anthropicAuthToken?: string;
  claudeCodeOauthToken?: string;
  glmApiKey?: string;
  zaiApiKey?: string;
  notes?: string;
  installIfMissing?: boolean;
  codexLogin?: boolean;
  setupMcp?: boolean;
  /** Mobile wizard compat: install the CLI now and return a
   *  non-terminal "auth still required" result instead of HTTP 400
   *  when Claude/Codex end up installed but unauthenticated. */
  allowInstallOnly?: boolean;
}

/**
 * Mirrors desktop/agent/runner_auth_browser_http.go's session shape.
 * Used by all four /runner-auth/browser/* endpoints to track an
 * in-flight OAuth handshake on a remote runner CLI (claude / codex).
 */
export interface RunnerBrowserAuthSession {
  id: string;
  runner: string;
  /** Mirrors the states emitted by the agent in
   *  desktop/agent/runner_auth_browser_http.go (starting →
   *  awaiting_browser → verifying → completed | cancelled | failed). */
  status: "starting" | "awaiting_browser" | "verifying" | "completed" | "failed" | "cancelled";
  method?: string;
  openUrl?: string;
  code?: string;
  detail?: string;
  error?: string;
  authConfigured?: boolean;
  authSource?: string;
  startedAt?: number;
  updatedAt?: number;
  completedAt?: number;
}

/** Unwrap the `{ok:true, session: {...}}` envelope the agent returns from
 *  /runner-auth/browser/{start,status,submit-code}. The mobile RunnerAuthModal
 *  expects a flat session shape — without this unwrap, `session.status` and
 *  `session.id` come back undefined, the modal stays stuck on "Waiting for
 *  the verification URL…" and polling URLs lose their `id` query param so
 *  the agent answers 400 forever. Tolerates older agents that returned the
 *  flat shape directly so a stale daemon still works. */
export function unwrapRunnerBrowserAuthEnvelope(raw: any): RunnerBrowserAuthSession {
  if (raw && typeof raw === "object" && raw.session && typeof raw.session === "object") {
    return raw.session as RunnerBrowserAuthSession;
  }
  return raw as RunnerBrowserAuthSession;
}

export interface RunnerAuthSetParams {
  runner: "claude" | "claude-code" | "codex" | "opencode";
  openaiApiKey?: string;
  anthropicApiKey?: string;
  anthropicAuthToken?: string;
  claudeCodeOauthToken?: string;
  glmApiKey?: string;
  zaiApiKey?: string;
  notes?: string;
}

export interface MachineOnboardingProviderStatus {
  id: "openai" | "github" | "gitlab" | string;
  name: string;
  ready: boolean;
  configured: boolean;
  cloneReady?: boolean;
  ciReady?: boolean;
  authSource?: string;
  cloneSource?: string;
  ciSource?: string;
  username?: string;
  host?: string;
  detail?: string;
  warning?: string;
}

export interface MachineOnboardingApplyParams {
  openaiApiKey?: string;
  githubToken?: string;
  gitlabToken?: string;
  gitlabHost?: string;
  applyClone?: boolean;
  applyCiToken?: boolean;
  notes?: string;
}

export interface AgentStatus {
  runner: {
    id: string;
    name: string;
    command: string;
    installed: boolean;
    ready?: boolean;
    authConfigured?: boolean;
    authSource?: string;
    warning?: string;
    error?: string;
  };
  runningTasks: number;
  totalTasks: number;
  runnerProcesses: Array<{ pid: number; command: string }>;
  system: {
    hostname: string;
    os: string;
    arch: string;
    memoryMb?: number;
  };
}

// ── Exec types ─────────────────────────────────────────────────────

export type ExecStatus = "running" | "completed" | "failed" | "killed";

export interface ExecSession {
  id: string;
  command: string;
  status: ExecStatus;
  exitCode?: number;
  stdout: string;
  stderr: string;
  pid?: number;
  startedAt: string;
  finishedAt?: string;
}

export interface ExecOptions {
  workDir?: string;
  timeout?: number;
  env?: Record<string, string>;
}

export type ConnectionState = "disconnected" | "connecting" | "connected" | "error";
export type ConnectionMode = "direct" | "relay" | "tunnel" | null;
/** How the connection was established — tracked for diagnostics and faster reconnection. */
export type ConnectionPath = "lan-beacon" | "lan-convex-ip" | "lan-beacon-upgrade" | "lan-heartbeat" | "lan-tailscale" | "lan-cached" | "relay" | "cloudflare-tunnel" | null;

export type OutputCallback = (taskId: string, line: string) => void;
export type ConnectionStateCallback = (state: ConnectionState) => void;
export type ConnectionModeCallback = (mode: ConnectionMode) => void;
export type ReconnectAttemptCallback = (attempt: number) => void;

type EventMap = {
  output: OutputCallback;
  connectionState: ConnectionStateCallback;
  connectionMode: ConnectionModeCallback;
  reconnectAttempt: ReconnectAttemptCallback;
};

type EventName = keyof EventMap;

// ── Client ───────────────────────────────────────────────────────────

export interface RelayServer {
  id: string;
  quicAddr: string;
  httpUrl: string;  // e.g. "https://connect.yaver.io"
  region: string;
  priority: number;
  password?: string;
}

export interface TunnelServer {
  id: string;
  url: string;  // e.g. "https://my-tunnel.example.com"
  cfAccessClientId?: string;
  cfAccessClientSecret?: string;
  label?: string;
  priority: number;
}

function relayStatusHint(status: number): string {
  if (status === 429) return "Yaver relay is rate limiting this connection. Wait a moment and try again.";
  if (status === 413) return "This request is larger than the relay allows. Reduce the upload size or use a direct/tunnel path.";
  if (status === 503) return "Yaver relay is temporarily overloaded. Try again shortly or switch to another transport.";
  if (status === 401) return "Relay authentication failed. Check the relay password or sign in again.";
  return `HTTP ${status}`;
}

async function responseErrorMessage(res: Response, fallback?: string): Promise<string> {
  const base = fallback || relayStatusHint(res.status);
  try {
    const data = await res.clone().json();
    const detail =
      typeof data?.message === "string" ? data.message :
      typeof data?.error === "string" ? data.error :
      "";
    if (detail) {
      const hint = relayStatusHint(res.status);
      return hint === `HTTP ${res.status}` ? detail : `${hint} ${detail}`;
    }
  } catch {}
  try {
    const text = await res.clone().text();
    if (text.trim()) return `${base}: ${text.trim().slice(0, 240)}`;
  } catch {}
  return base;
}

// Per-call speech state attached to a task. `ttsMode` is the user's
// "run tasks in TTS mode" setting (agent leads replies with a spoken-style
// summary); the rest describe live STT/TTS state for voice-initiated tasks.
export type SpeechContextInput = {
  inputFromSpeech?: boolean;
  sttProvider?: string;
  ttsEnabled?: boolean;
  ttsProvider?: string;
  ttsMode?: boolean;
  verbosity?: number;
};

// AsyncStorage key mirroring the "run tasks in TTS mode" setting so the
// QuicClient applies it before the Settings screen is ever opened.
const TTS_TASK_MODE_KEY = "tts_task_mode";

export class QuicClient {
  private host: string | null = null;
  private port: number | null = null;
  private token: string | null = null;
  private deviceId: string | null = null;
  private relayServers: RelayServer[] = [];  // all available relay servers
  private activeRelayUrl: string | null = null; // currently working relay base URL
  private activeRelayPassword: string | null = null; // password for the active relay (if any)
  private tunnelServers: TunnelServer[] = [];  // Cloudflare Tunnel endpoints
  private sessionTunnelServers: TunnelServer[] = [];  // selected-device tunnel hint
  private _tunnelUrl: string | null = null;
  private _tunnelHeaders: Record<string, string> = {};
  private _forceRelay = false; // default to direct-first — try LAN/local before relay
  // "Run tasks in TTS mode" user setting. When on, every task this client
  // creates carries speechContext.ttsMode so the agent leads its reply with
  // a spoken-style summary (text only; prompt shaping lives agent-side).
  // Mirrored to AsyncStorage so it applies even before Settings is opened.
  private _ttsTaskMode = false;
  private _connectionState: ConnectionState = "disconnected";
  private pollInterval: ReturnType<typeof setInterval> | null = null;
  private heartbeatInterval: ReturnType<typeof setInterval> | null = null;
  private _consecutiveHeartbeatFailures = 0;

  // Reconnection — short burst, then give up. 15 attempts produced a
  // ~2-minute silent spinning loop that ate battery and made the UI feel
  // broken; 3 attempts (≈7s) was too brittle on intermittent WiFi. At 5
  // attempts the backoff ladder is 1s, 2s, 4s, 8s, 16s (≈31s total) —
  // enough to ride out a DNS blip or relay reboot, still short enough to
  // surface a real outage. When the app goes to background we pause the
  // loop entirely (battery + no network events will be delivered anyway)
  // and resume from attempt 1 when we return to foreground.
  // `reconnectAttempt` is the 1-indexed number of the attempt currently in
  // progress or just completed. 0 means idle (connected or never started).
  private reconnectTimer: ReturnType<typeof setTimeout> | null = null;
  private _reconnectAttempt = 0;
  private _reconnectStopped = false;
  private _isForeground = true;
  private readonly baseBackoffMs = 1000;
  private readonly _maxReconnectAttempts = 5;
  // True once we've successfully reached this device's agent at least
  // once — either earlier this session OR persisted from a prior session
  // via connectionCache.hadSuccess. Drives the reconnect policy: a device
  // that has ever talked to us retries forever (capped backoff), since
  // "stuck CONNECTING with no further attempts" is the worst UX. A device
  // we've never reached still gives up at _maxReconnectAttempts so we
  // don't drain battery probing a wrong host the user just typo'd.
  private _hadSuccessfulConnect = false;

  private _connectionMode: ConnectionMode = null;
  private _connectionPath: ConnectionPath = null;
  private _networkType: string | null = null; // "wifi" | "cellular" | etc.
  private _connectingInProgress = false; // guard against concurrent attemptConnect calls
  private _lastTransportError: string | null = null;
  // Extra LAN/Tailscale/Ethernet IPs that the agent advertised in heartbeat.
  // Raced in parallel against the beacon IP and the primary host so the
  // session attaches via whichever address the phone can actually route to
  // (e.g. Tailscale 100.x when on cellular, plain Wi-Fi when same LAN).
  private _lanIps: string[] = [];
  agentAuthExpired = false; // true when agent's session with Convex has expired

  // Relay health tracking
  private _relayHealth: Map<string, { ok: boolean; latencyMs: number; lastChecked: number }> = new Map();

  // Event listeners
  private listeners: { [K in EventName]: Array<EventMap[K]> } = {
    output: [],
    connectionState: [],
    connectionMode: [],
    reconnectAttempt: [],
  };

  /** 1-indexed number of the current reconnect attempt. 0 = idle. */
  get reconnectAttempt(): number {
    return this._reconnectAttempt;
  }

  get maxReconnectAttempts(): number {
    return this._maxReconnectAttempts;
  }

  /** True when the user has asked us to stop trying to reconnect. */
  get reconnectStopped(): boolean {
    return this._reconnectStopped;
  }

  /** Set relay servers fetched from platform config. */
  setRelayServers(servers: RelayServer[]): void {
    this.relayServers = servers.sort((a, b) => a.priority - b.priority);
  }

  /** Set Cloudflare Tunnel endpoints. */
  setTunnelServers(servers: TunnelServer[]): void {
    this.tunnelServers = servers.sort((a, b) => a.priority - b.priority);
  }

  setSessionTunnelServers(servers?: TunnelServer[] | null): void {
    this.sessionTunnelServers = [...(servers || [])].sort((a, b) => a.priority - b.priority);
  }

  private get effectiveTunnelServers(): TunnelServer[] {
    if (this.sessionTunnelServers.length > 0) return this.sessionTunnelServers;
    return this.tunnelServers;
  }

  getTunnelServers(): TunnelServer[] {
    return [...this.effectiveTunnelServers];
  }

  get tunnelServerCount(): number {
    return this.effectiveTunnelServers.length;
  }

  /** Seed reachability metadata for recovery flows without requiring a
   *  successful attached session first. */
  primeTarget(
    host: string,
    port: number,
    token: string,
    deviceId: string,
    lanIps?: string[],
    sessionTunnels?: TunnelServer[],
  ): void {
    const isReattach = this.deviceId === deviceId;
    this.host = host;
    this.port = port;
    this.token = token;
    this.deviceId = deviceId;
    this._lanIps = Array.isArray(lanIps)
      ? lanIps.filter((s) => typeof s === "string" && s.length > 0)
      : [];
    this.setSessionTunnelServers(sessionTunnels);
    // Hydrate the "had successful connect" flag from the persisted cache
    // so the indefinite-retry policy survives an app restart. Switching
    // to a different deviceId resets the in-memory flag — we only trust
    // a hadSuccess=true entry that matches the device we're priming for.
    if (!isReattach) {
      this._hadSuccessfulConnect = false;
      void loadConnectionCache(deviceId).then((cached) => {
        if (cached?.hadSuccess && this.deviceId === deviceId) {
          this._hadSuccessfulConnect = true;
        }
      });
    }
  }

  /** Snapshot of the configured relay servers (highest priority first). Used
   *  by DeviceContext to hit /presence on the primary relay for real-time
   *  tunnel-up state, which is more accurate than Convex heartbeat lag. */
  get relayServersSnapshot(): RelayServer[] {
    return [...this.relayServers];
  }

  // ── Public getters ─────────────────────────────────────────────────

  get isConnected(): boolean {
    return this._connectionState === "connected";
  }

  get connectionState(): ConnectionState {
    return this._connectionState;
  }

  get connectionMode(): ConnectionMode {
    return this._connectionMode;
  }

  /** How the current connection was established (for diagnostics). */
  get connectionPath(): ConnectionPath {
    return this._connectionPath;
  }

  /** Last detected network type ("wifi", "cellular", etc.). */
  get networkType(): string | null {
    return this._networkType;
  }

  get relayServerCount(): number {
    return this.relayServers.length;
  }

  getRelayServers(): RelayServer[] {
    return [...this.relayServers];
  }

  /** Public accessor for the resolved base URL (direct, relay, or tunnel). */
  get baseUrl(): string {
    // Cloudflare Tunnel — direct HTTPS to tunnel URL (no relay proxy path)
    if (this._tunnelUrl) {
      return this._tunnelUrl;
    }
    // Use active relay if we're going through a relay server
    if (this.activeRelayUrl) {
      return `${this.activeRelayUrl}/d/${this.deviceId}`;
    }
    // Direct connection (same network / Tailscale)
    return `http://${this.host}:${this.port}`;
  }

  /** Public accessor for auth headers (for use by builds, vault, etc.). */
  getAuthHeaders(): Record<string, string> {
    return this.authHeaders;
  }

  /** Public accessor for the deviceId this client is currently attached
   *  to. Used by the connection manager to keep a per-device pool, and
   *  by peer-aware callers that want to compare a target id against
   *  the live attachment without having to thread it through. */
  get attachedDeviceId(): string | null {
    return this.deviceId;
  }

  /** Build a peer-aware URL. When `target` is empty OR equals the
   *  device this client is already attached to, drops the
   *  `/peer/<id>/` prefix entirely — otherwise the agent's peer proxy
   *  rejects self-targeting calls with errProxyLocal (HTTP 400) and the
   *  caller silently sees an empty/error response.
   *
   *  That bug is what made the wizard's "Pick a coding agent" pane
   *  report "Not installed on this device" for runners that were
   *  actually present on the box, when the user had auto-attached to
   *  the only-online device. Centralising the rule here keeps every
   *  peer-aware getter from re-introducing the same bug. */
  private peerEndpoint(target: string | undefined, suffix: string): string {
    const path = suffix.startsWith("/") ? suffix : `/${suffix}`;
    const t = (target || "").trim();
    if (!t || t === this.deviceId) {
      return `${this.baseUrl}${path}`;
    }
    return `${this.baseUrl}/peer/${encodeURIComponent(t)}${path}`;
  }

  /** Relay base URL we're currently routed through, if any. Needed by the
   *  "Deploy to another of my devices" flow (pushPhoneProject's `dev-hw`
   *  target) so the push can walk the same relay back to the sibling agent
   *  instead of trying the phone's LAN-local IP. Null when we have a direct
   *  connection (Tailscale, same Wi-Fi, Cloudflare tunnel). */
  get activeRelayHttpUrl(): string | null {
    return this.activeRelayUrl;
  }

  /** Read-only accessors used by the UI's transport classifier. Same shape
   *  as the web AgentClient. The mobile QuicClient owns a bunch of
   *  private state for connect/reconnect; these getters just publish
   *  the bits the device-card UI needs. */
  get activeRelayBaseUrl(): string | null { return this.activeRelayUrl; }
  get activeTunnelBaseUrl(): string | null { return this._tunnelUrl; }
  /** The relay password the JS quicClient is actively using for the
   *  in-flight relay connection. Set per-server in setRelayServers /
   *  during reconnect — NOT the `settings.relayPassword` Convex field
   *  (which is empty for accounts that didn't customise the relay).
   *  Native panes (YaverFeedbackPane / YaverAgentsPane) need to mirror
   *  this exact value into UserDefaults so their /tasks + runner-auth
   *  POSTs carry the same X-Relay-Password the JS task path already
   *  ships with. */
  get activeRelayPasswordValue(): string | null { return this.activeRelayPassword; }

  /** Reachability candidates for recovery. Keep the successful target URL so
   *  /auth/pair/submit can follow the same path instead of falling back to a
   *  stale relay URL. */
  private recoveryTargets(): Array<{ baseUrl: string; headers: Record<string, string> }> {
    // Recovery posts the user's bearer to the agent (mode=direct hands it
    // over as the agent's new session token). Prefer transports where the
    // bearer is end-to-end encrypted on the wire over plain-HTTP direct
    // paths — relay (HTTPS to relay → QUIC to agent) and HTTPS tunnels are
    // safe even on hostile WiFi, while http://lan-ip:18080 leaks the
    // bearer if anyone is sniffing the network. Order:
    //   1. Currently-active baseUrl — already chosen by the connection
    //      manager based on the user's network + forceRelay preference
    //   2. Relays (encrypted, password-gated)
    //   3. HTTPS Cloudflare/private tunnels
    //   4. LAN-beacon-discovered IP (private RFC1918, can't leak to public)
    //   5. Convex-stored host:port — last resort; may be a public IP, so
    //      try only after every encrypted+private path failed
    // The agent's classifyRecoveryIngress (recovery_transport.go) is the
    // authoritative gate; this ordering is defense-in-depth on the client.
    const seen = new Set<string>();
    const targets: Array<{ baseUrl: string; headers: Record<string, string> }> = [];
    const push = (baseUrl: string | null | undefined, headers: Record<string, string>) => {
      const normalized = (baseUrl || "").replace(/\/+$/, "");
      if (!normalized || seen.has(normalized)) return;
      seen.add(normalized);
      targets.push({ baseUrl: normalized, headers });
    };

    push(this.baseUrl, this.authHeaders);

    if (this.deviceId) {
      for (const relay of this.relayServers) {
        const headers: Record<string, string> = {
          Authorization: `Bearer ${this.token}`,
          "X-Client-Platform": Platform.OS,
        };
        if (relay.password) {
          headers["X-Relay-Password"] = relay.password;
        }
        push(`${relay.httpUrl}/d/${this.deviceId}`, headers);
      }
    }

    for (const tunnel of this.effectiveTunnelServers) {
      const headers: Record<string, string> = {
        Authorization: `Bearer ${this.token}`,
        "X-Client-Platform": Platform.OS,
      };
      if (tunnel.cfAccessClientId) {
        headers["CF-Access-Client-Id"] = tunnel.cfAccessClientId;
        headers["CF-Access-Client-Secret"] = tunnel.cfAccessClientSecret || "";
      }
      push(tunnel.url, headers);
    }

    const lanInfo = this.deviceId ? beaconListener.getLocalIP(this.deviceId) : null;
    if (lanInfo) {
      push(`http://${lanInfo.ip}:${lanInfo.port}`, {
        Authorization: `Bearer ${this.token}`,
        "X-Client-Platform": Platform.OS,
      });
    }

    if (this.host && this.port) {
      push(`http://${this.host}:${this.port}`, {
        Authorization: `Bearer ${this.token}`,
        "X-Client-Platform": Platform.OS,
      });
    }

    return targets;
  }

  /** Get health status for all relay servers. */
  getRelayHealth(): Array<{ id: string; url: string; ok: boolean; latencyMs: number; lastChecked: number }> {
    return this.relayServers.map(r => {
      const h = this._relayHealth.get(r.httpUrl);
      return {
        id: r.id,
        url: r.httpUrl,
        ok: h?.ok ?? false,
        latencyMs: h?.latencyMs ?? -1,
        lastChecked: h?.lastChecked ?? 0,
      };
    });
  }

  get forceRelay(): boolean {
    return this._forceRelay;
  }

  setForceRelay(value: boolean): void {
    if (this._forceRelay === value) return;
    this._forceRelay = value;
    if (!this.host) return;
    if (this._connectionState === "connected") {
      // Seamlessly switch connection mode without dropping existing connection
      console.log("[QUIC] Force relay changed to", value, "— switching mode...");
      this.switchConnectionMode(value);
    } else {
      // Not connected — trigger full reconnect with new strategy
      console.log("[QUIC] Force relay changed to", value, "— triggering reconnect...");
      this.fullReconnect();
    }
  }

  /** Switch between direct and relay without dropping the connection. */
  private async switchConnectionMode(useRelay: boolean): Promise<void> {
    try {
      if (useRelay) {
        // Try relay servers
        for (const relay of this.relayServers) {
          try {
            const relayDeviceUrl = `${relay.httpUrl}/d/${this.deviceId}`;
            const probeHeaders: Record<string, string> = { ...this.authHeaders };
            if (relay.password) {
              probeHeaders['X-Relay-Password'] = relay.password;
            }
            const res = await this.fetchWithTimeout(`${relayDeviceUrl}/health`, {
              headers: probeHeaders,
            }, 8000);
            if (res.ok) {
              this.activeRelayUrl = relay.httpUrl;
              this.activeRelayPassword = relay.password || null;
              this.setConnectionMode("relay");
              console.log("[QUIC] Switched to relay:", relay.id);
              return;
            }
          } catch (e) {
            console.log("[QUIC] Relay", relay.id, "unreachable:", e);
          }
        }
        console.warn("[QUIC] No relay available — staying on current mode");
      } else {
        // Switch to direct — only if host is reachable
        try {
          const directUrl = `http://${this.host}:${this.port}`;
          const res = await this.fetchWithTimeout(`${directUrl}/health`, {
            headers: this.authHeaders,
          }, 5000);
          if (res.ok) {
            this.activeRelayUrl = null;
            this.activeRelayPassword = null;
            this.setConnectionMode("direct");
            console.log("[QUIC] Switched to direct");
            return;
          }
        } catch (e) {
          console.log("[QUIC] Direct unreachable:", e);
        }
        console.warn("[QUIC] Direct unavailable — staying on relay");
      }
    } catch (e) {
      console.warn("[QUIC] Mode switch failed:", e);
    }
  }

  // ── Connection lifecycle ───────────────────────────────────────────

  /**
   * Establish a connection to the desktop agent.
   * Tries direct connection first, then relay servers in priority order.
   */
  async connect(host: string, port: number, token: string, deviceId: string, lanIps?: string[], sessionTunnels?: TunnelServer[]): Promise<void> {
    void this.hydrateTtsTaskMode(); // apply the persisted "TTS mode" flag to tasks created this session
    this.primeTarget(host, port, token, deviceId, lanIps, sessionTunnels);
    this.activeRelayUrl = null;
    this.activeRelayPassword = null;
    this._reconnectStopped = false;
    this.setReconnectAttempt(1);

    await this.attemptConnect();

    // _doAttemptConnect swallows path failures and flips state to "error"
    // so the background reconnect loop can keep trying. The public connect()
    // contract is different: callers (selectDevice) need to know up-front
    // whether the initial attempt actually reached the agent — otherwise the
    // UI logs "[connect-success] via null" and shows a fake green badge while
    // every subsequent request fails. Surface the failure as a thrown error
    // so the caller can show the real reason and stop the reconnect chatter.
    if (this._connectionState !== "connected") {
      throw new Error(this._lastTransportError || "Could not reach agent (direct, tunnel, or via relay)");
    }
  }

  /**
   * Replace the bearer token used for every subsequent request without
   * tearing down the connection. Called when Convex rotates the session
   * token in response to `/auth/refresh` — the existing TCP/QUIC path,
   * relay password, and active device remain; only the Authorization
   * header changes. Ignores no-op and empty updates so we don't race
   * with the AuthContext bootstrap. Callers must have already persisted
   * the new token to the Keychain before calling this.
   */
  setToken(token: string): void {
    if (!token) return;
    if (this.token === token) return;
    this.token = token;
  }

  /** Close the connection and stop all timers. */
  disconnect(): void {
    this.clearTimers();
    this.setConnectionState("disconnected");
    this.setConnectionMode(null);
    this.setReconnectAttempt(0);
    this._reconnectStopped = false;
    this.host = null;
    this.port = null;
    this.token = null;
    this.deviceId = null;
    this._lanIps = [];
    this.activeRelayUrl = null;
    this.activeRelayPassword = null;
    this.sessionTunnelServers = [];
    this._tunnelUrl = null;
    this._tunnelHeaders = {};
    // Drop the in-memory "had successful connect" flag — primeTarget
    // re-hydrates it from the persisted cache on the next attach. The
    // cache itself is intentionally preserved across disconnect so a
    // sign-out/sign-in round-trip doesn't lose the fast-path.
    this._hadSuccessfulConnect = false;
  }

  // ── OpenCode config API ────────────────────────────────────────────
  // Mirrors web/lib/agent-client.ts shape. Used by the mobile
  // OpenCode Config modal to read + edit opencode.json on the
  // connected device. All fields are optional in the patch body —
  // omit a key to leave it unchanged.

  async getOpenCodeConfig(target?: string): Promise<OpenCodeConfigSummary | null> {
    if (!this.isConnected && !this.hasConnectionInfo) return null;
    try {
      const url = this.peerEndpoint(target, "/runner/opencode/config");
      const res = await fetch(url, { headers: this.authHeaders });
      if (!res.ok) return null;
      const data = await res.json();
      return (data?.config || null) as OpenCodeConfigSummary | null;
    } catch {
      return null;
    }
  }

  async saveOpenCodeConfig(patch: {
    defaultAgent?: string;
    model?: string;
    smallModel?: string;
    buildModel?: string;
    planModel?: string;
    providers?: Array<{
      id: string;
      name?: string;
      baseUrl?: string;
      apiKey?: string;
      delete?: boolean;
    }>;
  }): Promise<{ ok: boolean; config?: OpenCodeConfigSummary; error?: string }> {
    if (!this.isConnected && !this.hasConnectionInfo) return { ok: false, error: "not connected" };
    try {
      const res = await fetch(`${this.baseUrl}/runner/opencode/config`, {
        method: "POST",
        headers: { ...this.authHeaders, "Content-Type": "application/json" },
        body: JSON.stringify(patch),
      });
      const data = await res.json().catch(() => ({}));
      if (!res.ok) return { ok: false, error: data?.error || `HTTP ${res.status}` };
      return { ok: true, config: data?.config };
    } catch (err) {
      return { ok: false, error: err instanceof Error ? err.message : String(err) };
    }
  }

  // ── Task API ───────────────────────────────────────────────────────

  /**
   * Send a new task to the desktop agent.
   *
   * `codeMode` toggles the agent's prompt-wrapping path:
   *
   *   - `false` (default): source="mobile" → mobile-style task
   *     wrapping. Picks up yaverDevServerContext (Hermes / dev-server
   *     hot-reload context), video summary support, mobile-source
   *     guards. This is the existing `yaver go`-style wrapping.
   *
   *   - `true`: source="mobile-code" → terminal-style task wrapping
   *     (yaverWrapperCapabilityContext). Same backend code path —
   *     same /tasks endpoint, same TaskManager.CreateTask — but the
   *     agent shapes the prompt the way `yaver code` does:
   *     no markdown headings by default, no canned bullet framing,
   *     no fenced blocks unless asked. Used when the user explicitly
   *     wants the runner to behave like a CLI coding session.
   *
   * Both modes are non-destructive: they share TaskManager, /tasks
   * HTTP, the runner pool, and the same Task type. The toggle only
   * changes which prompt-prefix the agent injects.
   */
  async sendTask(title: string, description: string, model?: string, runner?: string, customCommand?: string, speechContext?: SpeechContextInput, images?: ImageAttachment[], workDir?: string, mode?: string, video?: { enabled?: boolean; source?: "browser" | "sim-ios" | "sim-android" | "phone" }, codeMode?: boolean): Promise<Task> {
    this.assertConnected();
    // Hard 30s timeout — without it, a stale relay tunnel (e.g. after a
    // failed device-switch attempt) makes this POST hang forever and
    // the FAB Submit button gets stuck on "Sending…". 30s is generous
    // even for image-heavy tasks; the relay caps non-SSE proxies at
    // ~25s, so anything longer is the connection itself, not the work.
    const sc = this.withTtsMode(speechContext);
    const res = await this.fetchWithTimeout(`${this.baseUrl}/tasks`, {
      method: "POST",
      headers: { ...this.authHeaders, "Content-Type": "application/json" },
      body: JSON.stringify({
        title,
        description,
        // codeMode flips the agent's prompt-wrapping. See the doc
        // comment on sendTask above for the wrapping difference.
        source: codeMode ? "mobile-code" : "mobile",
        ...(model ? { model } : {}),
        ...(runner ? { runner } : {}),
        ...(mode ? { mode } : {}),
        ...(customCommand ? { customCommand } : {}),
        ...(sc ? { speechContext: sc } : {}),
        ...(images?.length ? { images } : {}),
        ...(workDir ? { workDir } : {}),
        ...(video?.enabled ? { videoEnabled: true } : {}),
        ...(video?.source ? { videoSource: video.source } : {}),
      }),
    }, 30000);
    if (!res.ok) {
      throw new Error(await responseErrorMessage(res, `Failed to create task: ${res.status}`));
    }
    const data = await res.json();
    // Agent returns { ok, taskId, status, runnerId, model }
    return {
      id: data.taskId,
      title,
      description,
      status: data.status,
      runnerId: data.runnerId,
      model: typeof data.model === "string" && data.model.trim() ? data.model.trim() : undefined,
      output: [],
      createdAt: Date.now(),
      updatedAt: Date.now(),
    };
  }

  /**
   * Fan-out a shared-screenshot bug report to ONE specific device as a
   * feedback task, without switching the active client.
   *
   * `peerEndpoint` collapses to `${baseUrl}/tasks` when `deviceId` is the
   * already-attached device, and to `${baseUrl}/peer/<id>/tasks` (the
   * agent's re-signed peer proxy, httpserver.go::handlePeerProxy) for any
   * other of the user's devices — so the WhatsApp-style "send to N
   * machines" picker just loops over this. `source: "native-guest-shake"`
   * makes the remote agent run it through vibingifyFeedbackTaskBody
   * (project resolved from its last-loaded guest / workspace, ready
   * runner picked, runner auto-started, auto-reload on commit).
   */
  async createFeedbackTaskOnDevice(
    deviceId: string,
    opts: { title: string; images?: ImageAttachment[] },
  ): Promise<{ id: string; status?: string; deviceName?: string }> {
    this.assertConnected();
    const url = this.peerEndpoint(deviceId, "/tasks");
    const res = await this.fetchWithTimeout(url, {
      method: "POST",
      headers: { ...this.authHeaders, "Content-Type": "application/json" },
      body: JSON.stringify({
        title: opts.title,
        source: "native-guest-shake",
        ...(opts.images?.length ? { images: opts.images } : {}),
      }),
    }, 30000);
    if (!res.ok) {
      let msg = `Failed to send to device: ${res.status}`;
      try {
        const err = await res.json();
        if (err?.error) msg = err.error;
      } catch {}
      throw new Error(msg);
    }
    const data = await res.json();
    return { id: data.taskId ?? data.id, status: data.status, deviceName: data.deviceName };
  }

  /**
   * Ask the Go agent to craft a recovery prompt for a known failure kind and
   * hand it to the wrapped AI agent (Claude Code / Codex / Aider / …).
   *
   * This is the preferred UX when the user hits something the AI can actually
   * fix — missing node, failing build, broken pods, Flutter device not found.
   * The mobile app does NOT build the prompt itself; the agent knows the
   * wrapped runner, the workdir, and the project context.
   */
  async recover(ctx: {
    kind:
      | "hermes-build-failed"
      | "metro-not-starting"
      | "flutter-flush-failed"
      | "flutter-device-missing"
      | "swift-build-failed"
      | "swift-install-failed"
      | "kotlin-build-failed"
      | "kotlin-install-failed"
      | "apk-download-failed"
      | "missing-runtime"
      | "deps-install-failed"
      | "dev-compat-missing-tools"
      | "generic";
    framework?: string;
    workDir?: string;
    platform?: string;
    project?: string;
    error?: string;
    tool?: string;
    hint?: string;
    userGoal?: string;
  }): Promise<{ taskId: string; title: string }> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/recover`, {
      method: "POST",
      headers: { ...this.authHeaders, "Content-Type": "application/json" },
      body: JSON.stringify({ ...ctx, surface: "mobile" }),
    });
    if (!res.ok) {
      let msg = `Failed to queue recovery task: ${res.status}`;
      try {
        const data = await res.json();
        if (data?.error) msg = data.error;
      } catch {}
      throw new Error(msg);
    }
    const data = await res.json();
    return { taskId: data.taskId, title: data.title };
  }

  /**
   * Subscribe to the dev server's SSE event stream (/dev/events).
   * onEvent fires once per event ({type, framework, logLine, message, ...}).
   * Returns an unsubscribe function that aborts the stream.
   *
   * The server emits "log" on every Metro / Expo / Flutter subprocess
   * line, plus "starting" / "ready" / "error" / "stopped" lifecycle
   * events. The caller is expected to keep only the tail (e.g. last
   * 100 lines) — this helper does no buffering.
   */
  subscribeDevEvents(onEvent: (ev: { type: string; framework?: string; logLine?: string; message?: string; bundleUrl?: string; deepLink?: string; timestamp?: string }) => void): () => void {
    if (!this.isConnected) return () => {};
    const controller = new AbortController();
    (async () => {
      try {
        const res = await fetch(`${this.baseUrl}/dev/events`, {
          headers: { ...this.authHeaders, Accept: "text/event-stream" },
          signal: controller.signal,
        });
        if (!res.ok || !res.body) return;
        const reader = (res.body as ReadableStream<Uint8Array>).getReader();
        const decoder = new TextDecoder();
        let buf = "";
        for (;;) {
          const { value, done } = await reader.read();
          if (done) break;
          buf += decoder.decode(value, { stream: true });
          let idx: number;
          while ((idx = buf.indexOf("\n\n")) >= 0) {
            const frame = buf.slice(0, idx);
            buf = buf.slice(idx + 2);
            const dataLines = frame
              .split("\n")
              .filter((l) => l.startsWith("data:"))
              .map((l) => l.slice(5).trimStart());
            if (dataLines.length === 0) continue;
            try {
              onEvent(JSON.parse(dataLines.join("\n")));
            } catch {
              // drop malformed frames
            }
          }
        }
      } catch {
        // aborted or connection dropped — the caller re-subscribes on its own cadence
      }
    })();
    return () => controller.abort();
  }

  /**
   * Subscribe to the connected agent's P2P bus event stream.
   * Topics like `peer/{id}/online`, `peer/{id}/ping`,
   * `peer/{id}/offline` carry sub-minute peer presence — subscribe
   * with `prefix="peer"` to track which devices are alive in the
   * mesh without polling Convex. Returns an unsubscribe function;
   * call it when the app backgrounds (iOS/Android kill long-lived
   * sockets within seconds of suspend).
   *
   * The wire format mirrors `web-headless`/`mobile-headless`
   * `subscribeBusEvents` so Node/Bun smoke tests consume the same
   * stream as the RN app.
   */
  subscribeBusEvents(opts: {
    prefix?: string;
    onEvent: (evt: BusEvent) => void;
    onError?: (err: Error) => void;
  }): () => void {
    if (!this.isConnected) return () => {};
    const url = opts.prefix
      ? `${this.baseUrl}/bus/events?prefix=${encodeURIComponent(opts.prefix)}`
      : `${this.baseUrl}/bus/events`;
    const controller = new AbortController();
    (async () => {
      try {
        const res = await fetch(url, {
          headers: { ...this.authHeaders, Accept: "text/event-stream" },
          signal: controller.signal,
        });
        if (!res.ok || !res.body) {
          opts.onError?.(new Error(`bus/events: HTTP ${res.status}`));
          return;
        }
        const reader = (res.body as ReadableStream<Uint8Array>).getReader();
        const decoder = new TextDecoder();
        let buf = "";
        for (;;) {
          const { value, done } = await reader.read();
          if (done) break;
          buf += decoder.decode(value, { stream: true });
          let idx: number;
          while ((idx = buf.indexOf("\n\n")) >= 0) {
            const frame = buf.slice(0, idx);
            buf = buf.slice(idx + 2);
            const dataLines = frame
              .split("\n")
              .filter((l) => l.startsWith("data:"))
              .map((l) => l.slice(5).trimStart());
            if (dataLines.length === 0) continue;
            try {
              opts.onEvent(JSON.parse(dataLines.join("\n")) as BusEvent);
            } catch {
              // drop malformed frames
            }
          }
        }
      } catch (err: any) {
        if (err?.name !== "AbortError") {
          opts.onError?.(err);
        }
      }
    })();
    return () => controller.abort();
  }

  /** List all tasks from the desktop agent, falling back to cache on failure. */
  async listTasks(): Promise<Task[]> {
    if (!this.isConnected) {
      // Return cached data when offline
      return getCachedTaskList();
    }
    try {
      const res = await fetch(`${this.baseUrl}/tasks`, {
        headers: this.authHeaders,
      });
      if (!res.ok) throw new Error(`Failed to list tasks: ${res.status}`);
      const data = await res.json();
      // Agent returns { ok, tasks: [...] } with output as a string
      const rawTasks = data.tasks || [];
      const tasks: Task[] = rawTasks.map((t: any) => ({
        id: t.id,
        title: t.title,
        description: t.description,
        status: t.status,
        runnerId: t.runnerId || undefined,
        model: typeof t.model === "string" && t.model.trim() ? t.model.trim() : undefined,
        output: typeof t.output === "string" && t.output
          ? t.output.split("\n")
          : Array.isArray(t.output) ? t.output : [],
        createdAt: t.createdAt ? new Date(t.createdAt).getTime() : Date.now(),
        updatedAt: t.finishedAt
          ? new Date(t.finishedAt).getTime()
          : t.startedAt
            ? new Date(t.startedAt).getTime()
            : t.createdAt ? new Date(t.createdAt).getTime() : Date.now(),
        deviceName: this.host ?? undefined,
        resultText: t.resultText || undefined,
        costUsd: t.costUsd || undefined,
        inputTokens: typeof t.inputTokens === "number" ? t.inputTokens : undefined,
        outputTokens: typeof t.outputTokens === "number" ? t.outputTokens : undefined,
        turns: t.turns || undefined,
        tmuxSession: t.tmuxSession || undefined,
        isAdopted: t.isAdopted || false,
        chainId: t.chainId || undefined,
        chainOrder: t.chainOrder ?? undefined,
        autoRetry: t.autoRetry || false,
        autoRetryCount: t.autoRetryCount || 0,
        autoRetryMax: t.autoRetryMax || 0,
      }));
      // Filter out tasks the user previously deleted
      const deletedIds = await getDeletedTaskIds();
      const filtered = deletedIds.size > 0 ? tasks.filter(t => !deletedIds.has(t.id)) : tasks;
      // Persist to local cache for offline access
      cacheTaskList(filtered);
      return filtered;
    } catch {
      // Network error — serve from cache
      return getCachedTaskList();
    }
  }

  /** Get a single task by ID. */
  async getTask(taskId: string): Promise<Task> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/tasks/${taskId}`, {
      headers: this.authHeaders,
    });
    if (!res.ok) throw new Error(`Failed to get task: ${res.status}`);
    const data = await res.json();
    const t = data.task || data;
    return {
      id: t.id,
      title: t.title,
      description: t.description,
      status: t.status,
      output: typeof t.output === "string" && t.output
        ? t.output.split("\n").filter((l: string) => l)
        : Array.isArray(t.output) ? t.output : [],
      createdAt: t.createdAt ? new Date(t.createdAt).getTime() : Date.now(),
      updatedAt: t.finishedAt
        ? new Date(t.finishedAt).getTime()
        : t.startedAt
          ? new Date(t.startedAt).getTime()
          : t.createdAt ? new Date(t.createdAt).getTime() : Date.now(),
      deviceName: this.host ?? undefined,
      resultText: t.resultText || undefined,
      costUsd: t.costUsd || undefined,
      inputTokens: typeof t.inputTokens === "number" ? t.inputTokens : undefined,
      outputTokens: typeof t.outputTokens === "number" ? t.outputTokens : undefined,
      turns: t.turns || undefined,
      tmuxSession: t.tmuxSession || undefined,
      isAdopted: t.isAdopted || false,
    };
  }

  /** Stop a running task (kills the process). */
  async stopTask(taskId: string): Promise<void> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/tasks/${taskId}/stop`, {
      method: "POST",
      headers: this.authHeaders,
    });
    if (!res.ok) throw new Error(`Failed to stop task: ${res.status}`);
  }

  /** Mark a reviewed task complete. */
  async completeTask(taskId: string): Promise<void> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/tasks/${taskId}/complete`, {
      method: "POST",
      headers: this.authHeaders,
    });
    if (!res.ok) throw new Error(`Failed to complete task: ${res.status}`);
  }

  /** Gracefully exit a running task by sending the runner's exit command (e.g. /exit for Claude). */
  async exitTask(taskId: string): Promise<void> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/tasks/${taskId}/exit`, {
      method: "POST",
      headers: this.authHeaders,
    });
    if (!res.ok) throw new Error(`Failed to exit task: ${res.status}`);
  }

  /** Resume a task with a follow-up prompt. */
  async continueTask(taskId: string, input: string, images?: ImageAttachment[]): Promise<void> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/tasks/${taskId}/continue`, {
      method: "POST",
      headers: { ...this.authHeaders, "Content-Type": "application/json" },
      body: JSON.stringify({ input, ...(images?.length ? { images } : {}) }),
    });
    if (!res.ok) throw new Error(`Failed to continue task: ${res.status}`);
  }

  /**
   * Fork an existing task to a different runner/model/mode with bounded
   * recent-context handoff. Use this when the user changes the agent
   * picker mid-conversation — preserves the parent task immutable
   * (Claude/Codex/OpenCode don't share session formats) while spawning
   * a child with a clipped excerpt of recent turns + the latest result
   * tail as context. Server side: desktop/agent/task_fork.go.
   */
  async forkTask(
    taskId: string,
    args: { runner: string; model?: string; mode?: string; input: string; contextWords?: number },
  ): Promise<{ taskId: string; runnerId: string; parentTaskId: string; contextWordsUsed: number }> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/tasks/${taskId}/fork`, {
      method: "POST",
      headers: { ...this.authHeaders, "Content-Type": "application/json" },
      body: JSON.stringify({
        runner: args.runner,
        model: args.model ?? "",
        mode: args.mode ?? "",
        input: args.input,
        contextWords: args.contextWords,
      }),
    });
    if (!res.ok) {
      const text = await res.text().catch(() => "");
      throw new Error(`Failed to fork task: ${res.status} ${text}`);
    }
    const json = await res.json();
    return {
      taskId: json.taskId,
      runnerId: json.runnerId,
      parentTaskId: json.parentTaskId,
      contextWordsUsed: json.contextWordsUsed ?? 0,
    };
  }

  /** Delete a completed or failed task. */
  async deleteTask(taskId: string): Promise<void> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/tasks/${taskId}`, {
      method: "DELETE",
      headers: this.authHeaders,
    });
    if (!res.ok) throw new Error(`Failed to delete task: ${res.status}`);
  }

  /** Stop all running tasks. */
  async stopAllTasks(): Promise<number> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/tasks/stop-all`, {
      method: "POST",
      headers: this.authHeaders,
    });
    if (!res.ok) throw new Error(`Failed to stop all: ${res.status}`);
    const data = await res.json();
    return data.stopped || 0;
  }

  // ── Chained Tasks ─────────────────────────────────────────────────

  /** Create a chain of tasks that execute sequentially. */
  async createChain(tasks: { title: string; description?: string }[], options?: { model?: string; runner?: string; autoRetry?: boolean }): Promise<{ chainId: string; tasks: string[]; count: number }> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/chain`, {
      method: "POST",
      headers: { ...this.authHeaders, "Content-Type": "application/json" },
      body: JSON.stringify({
        tasks,
        source: "mobile",
        ...(options?.model ? { model: options.model } : {}),
        ...(options?.runner ? { runner: options.runner } : {}),
        ...(options?.autoRetry ? { autoRetry: true } : {}),
        ...(this._ttsTaskMode ? { speechContext: { ttsMode: true } } : {}),
      }),
    });
    if (!res.ok) {
      let msg = `Failed to create chain: ${res.status}`;
      try { const e = await res.json(); if (e.error) msg = e.error; } catch {}
      throw new Error(msg);
    }
    return res.json();
  }

  /** Get the status of a task chain. */
  async getChainStatus(chainId: string): Promise<{ chainId: string; status: string; tasks: any[] }> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/chain/${chainId}`, {
      headers: this.authHeaders,
    });
    if (!res.ok) throw new Error(`Failed to get chain: ${res.status}`);
    return res.json();
  }

  // ── Deploy (Ship It) ────────────────────────────────────────────

  /** Get available deploy targets for the current project. */
  async getDeployTargets(): Promise<{ targets: { id: string; name: string; command: string }[]; workDir: string }> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/deploy`, {
      headers: this.authHeaders,
    });
    if (!res.ok) throw new Error(`Failed to get deploy targets: ${res.status}`);
    return res.json();
  }

  /** Trigger a deploy. Pass target ID or omit for auto-detect. */
  async deploy(target?: string): Promise<{ taskId: string; target: string }> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/deploy`, {
      method: "POST",
      headers: { ...this.authHeaders, "Content-Type": "application/json" },
      body: JSON.stringify(target ? { target } : {}),
    });
    if (!res.ok) {
      let msg = `Deploy failed: ${res.status}`;
      try { const e = await res.json(); if (e.error) msg = e.error; } catch {}
      throw new Error(msg);
    }
    return res.json();
  }

  // ── Summary ──────────────────────────────────────────────────────

  /** Get a summary of task activity for the last N hours (default 24). */
  async getSummary(hours?: number): Promise<{ summary: any; text: string }> {
    this.assertConnected();
    const q = hours ? `?hours=${hours}` : "";
    const res = await fetch(`${this.baseUrl}/summary${q}`, {
      headers: this.authHeaders,
    });
    if (!res.ok) throw new Error(`Failed to get summary: ${res.status}`);
    return res.json();
  }

  // ── SSE Task Stream ──────────────────────────────────────────────

  /**
   * Stream task output via SSE. Returns an abort function.
   *
   * Event types currently emitted by the daemon (handleTaskByID/streamOutput):
   *   - {type:"output", text} — text chunk; routed to onData
   *   - {type:"done", status} — terminal state; routed to onDone
   *   - {type:"agent_question", question} — runner is asking the human;
   *     routed to onEvent. The UI should render a sheet and POST the
   *     answer to /tasks/{id}/answer (see answerTaskQuestion below).
   *   - {type:"agent_answered", questionId, answer} — another device on
   *     the same account answered first; routed to onEvent so this UI
   *     can close its sheet without sending a duplicate.
   *   - {type:"agent_question_cancelled", questionId, reason} — task
   *     stopped or question expired; close the sheet.
   *
   * Unknown event types are silently dropped — the daemon may emit
   * new ones in the future without a client bump.
   */
  streamTaskOutput(
    taskId: string,
    onData: (text: string) => void,
    onDone?: (status: string) => void,
    onEvent?: (event: { type: string; [k: string]: unknown }) => void,
  ): () => void {
    // XMLHttpRequest with onprogress instead of fetch().body.getReader()
    // because RN-iOS's fetch streams response bodies live (NSURLSession)
    // but RN-Android's fetch buffers the entire body before delivering
    // it. For an SSE stream that never closes, the Android side would
    // sit forever with no output — which is exactly what happens for
    // claude-code "Hello" tasks on the Samsung tablet today. XHR's
    // onprogress callback fires as data arrives on both platforms.
    // Same workaround that settings.tsx already uses for `yaver logs -f`.
    const url = `${this.baseUrl}/tasks/${taskId}/output`;
    const xhr = new XMLHttpRequest();
    let lastIndex = 0;
    let aborted = false;

    const flushBuffer = (chunk: string) => {
      const lines = chunk.split("\n");
      for (const line of lines) {
        if (!line.startsWith("data: ")) continue;
        try {
          const evt = JSON.parse(line.slice(6));
          if (evt.type === "output" && evt.text) {
            onData(evt.text);
          } else if (evt.type === "done" && onDone) {
            onDone(evt.status);
          } else if (onEvent) {
            onEvent(evt);
          }
        } catch {
          // ignore malformed chunk
        }
      }
    };

    xhr.open("POST", url, true);
    for (const [k, v] of Object.entries(this.authHeaders)) {
      xhr.setRequestHeader(k, v as string);
    }
    xhr.onprogress = () => {
      if (aborted) return;
      const text = xhr.responseText || "";
      // Carry an incomplete final line forward — split only at the last
      // \n so a chunk that ends mid-event doesn't drop the leftover.
      const lastNewline = text.lastIndexOf("\n", text.length - 1);
      if (lastNewline <= lastIndex) return;
      const ready = text.slice(lastIndex, lastNewline);
      lastIndex = lastNewline + 1;
      flushBuffer(ready);
    };
    xhr.onerror = () => {
      // network error or abort — silent (matches the previous behavior)
    };
    xhr.onloadend = () => {
      if (aborted) return;
      // Drain any trailing buffer that arrived without a terminating \n.
      const tail = (xhr.responseText || "").slice(lastIndex);
      if (tail) flushBuffer(tail);
    };
    try {
      xhr.send();
    } catch {
      // ignore — onerror will fire if the send itself was rejected
    }

    return () => {
      aborted = true;
      try {
        xhr.abort();
      } catch {
        // ignore
      }
    };
  }

  /**
   * Answer the currently-pending agent_question for a task. The
   * daemon resolves the parked /tasks/{id}/question handler so the
   * runner's MCP `yaver_ask_user` call returns with this answer.
   * Returns {ok:true} on success, {ok:false, error} on failure
   * (e.g. the question already expired or was answered by another
   * device). Idempotent: a second call with the same questionId
   * returns ok:false with a 404-style error.
   */
  async answerTaskQuestion(
    taskId: string,
    questionId: string,
    answer: string,
  ): Promise<{ ok: boolean; error?: string }> {
    try {
      const res = await fetch(`${this.baseUrl}/tasks/${taskId}/answer`, {
        method: "POST",
        headers: { ...this.authHeaders, "Content-Type": "application/json" },
        body: JSON.stringify({ questionId, answer }),
      });
      if (!res.ok) {
        const txt = await res.text().catch(() => "");
        return { ok: false, error: txt || `HTTP ${res.status}` };
      }
      return { ok: true };
    } catch (err) {
      return { ok: false, error: err instanceof Error ? err.message : String(err) };
    }
  }

  /**
   * Fetch the currently-pending agent_question for a task, if any.
   * Used by a late-joining UI that opened the task page after the
   * SSE event already fired. Returns null on 404 (no pending) or
   * on transport error.
   */
  async getPendingTaskQuestion(taskId: string): Promise<{
    id: string;
    taskId: string;
    prompt: string;
    header?: string;
    kind: "text" | "choice" | "secret";
    choices?: string[];
    multi?: boolean;
    vaultHint?: string;
    createdAtMs: number;
    timeoutSec: number;
  } | null> {
    try {
      const res = await fetch(`${this.baseUrl}/tasks/${taskId}/question`, {
        method: "GET",
        headers: this.authHeaders,
      });
      if (!res.ok) return null;
      const body = await res.json();
      return body?.question ?? null;
    } catch {
      return null;
    }
  }

  // ── SSE Log Stream (loop chat events) ───────────────────────────

  /**
   * Subscribe to a daemon-hosted log stream (e.g. "autodev:sfmg" —
   * the "autodev:" prefix is the legacy namespace still used by
   * autoideas/autoinit streams).
   * Yields one parsed structured event per onEvent call. Backwards-
   * compatible with legacy "line" frames (rendered as a runner_text-ish
   * shape with no runner). Returns an abort function.
   *
   * Event shapes (`type`):
   *   yaver_say     {text}
   *   runner_action {runner, tool, detail}
   *   runner_text   {runner, text}
   *   runner_result {runner, status, duration_ms, cost_usd}
   *   line          {text}                    — legacy
   */
  streamLog(
    streamName: string,
    onEvent: (ev: any) => void,
    onClose?: () => void,
  ): () => void {
    const controller = new AbortController();
    const url = `${this.baseUrl}/streams/${encodeURIComponent(streamName)}`;
    let aborted = false;
    (async () => {
      try {
        const res = await fetch(url, {
          method: "GET",
          headers: { ...this.authHeaders, Accept: "text/event-stream" },
          signal: controller.signal,
        });
        if (!res.ok || !res.body) return;
        const reader = res.body.getReader();
        const decoder = new TextDecoder();
        let buffer = "";
        while (true) {
          const { done, value } = await reader.read();
          if (done) break;
          buffer += decoder.decode(value, { stream: true });
          const lines = buffer.split("\n");
          buffer = lines.pop() || "";
          for (const line of lines) {
            if (!line.startsWith("data: ")) continue;
            try {
              onEvent(JSON.parse(line.slice(6)));
            } catch {
              // ignore malformed frame
            }
          }
        }
      } catch {
        // aborted or network error
      } finally {
        // Notify the caller the stream ended for ANY reason except an
        // explicit abort — this lets uninstall flows distinguish "user
        // navigated away" (don't show success) from "agent exited"
        // (show success when the last destructive step landed).
        if (!aborted && onClose) {
          try { onClose(); } catch { /* ignore */ }
        }
      }
    })();
    return () => {
      aborted = true;
      controller.abort();
    };
  }

  // ── Autoinit + Autoideas (cached project context + idea queue) ──

  /** GET /autoinit/status?work_dir=… */
  async autoinitStatus(workDir: string): Promise<{
    done: boolean;
    path: string;
    bytes: number;
    updated_at?: string;
    has_generated_section: boolean;
    has_history_section: boolean;
  }> {
    const url = `${this.baseUrl}/autoinit/status?work_dir=${encodeURIComponent(workDir)}`;
    const res = await fetch(url, { headers: this.authHeaders });
    return await res.json();
  }

  /** POST /autoinit/start */
  async autoinitStart(body: {
    work_dir: string;
    project?: string;
    prompt?: string;
    engine?: string;
    runner?: string;
    output?: string;
    force?: boolean;
  }): Promise<any> {
    const res = await fetch(`${this.baseUrl}/autoinit/start`, {
      method: "POST",
      headers: { ...this.authHeaders, "Content-Type": "application/json" },
      body: JSON.stringify(body),
    });
    return await res.json();
  }

  /** GET /autoideas/file?work_dir=…&output=… */
  async autoideasFile(workDir: string, output = "ideas.md"): Promise<{
    ok: boolean;
    items: { line: number; checked: boolean; title: string }[];
    raw: string;
    path: string;
  }> {
    const url = `${this.baseUrl}/autoideas/file?work_dir=${encodeURIComponent(workDir)}&output=${encodeURIComponent(output)}`;
    const res = await fetch(url, { headers: this.authHeaders });
    return await res.json();
  }

  /** POST /autoideas/start */
  async autoideasStart(body: {
    work_dir: string;
    project?: string;
    hours?: string;
    load?: string;
    prompt?: string;
    harden?: string;
    engine?: string;
    output?: string;
    max_batches?: number;
    tick?: number;
  }): Promise<any> {
    const res = await fetch(`${this.baseUrl}/autoideas/start`, {
      method: "POST",
      headers: { ...this.authHeaders, "Content-Type": "application/json" },
      body: JSON.stringify(body),
    });
    return await res.json();
  }

  /** POST /autoideas/select — turns picked lines into an implementation run */
  async autoideasSelect(body: {
    work_dir: string;
    output?: string;
    project?: string;
    lines: number[];
    engine?: string;
    hours?: string;
    load?: string;
    auto_branch?: boolean;
    deploy?: string;
  }): Promise<any> {
    const res = await fetch(`${this.baseUrl}/autoideas/select`, {
      method: "POST",
      headers: { ...this.authHeaders, "Content-Type": "application/json" },
      body: JSON.stringify(body),
    });
    return await res.json();
  }

  // ── Projects (discovery + switching) ────────────────────────────

  /** List discovered projects on the machine, with discovery-state metadata. */
  async listProjectsDetailed(): Promise<{
    projects: { name: string; path: string; branch?: string; framework?: string; executionMode?: string; primarySurface?: string; gitRemote?: string; tags?: string[] }[];
    discovery?: {
      status?: "idle" | "discovering" | "partial" | "ready";
      discovering?: boolean;
      partiallyReady?: boolean;
      lastStartedAt?: string;
      lastCompletedAt?: string;
      lastError?: string;
    };
  }> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/projects`, {
      headers: this.authHeaders,
    });
    if (!res.ok) throw new Error(`Failed to list projects: ${res.status}`);
    const data = await res.json();
    return {
      projects: data.projects ?? [],
      discovery: data.discovery,
    };
  }

  /** List repo-level projects (monorepo roots + standalone repos) so the
   *  mobile UI can offer full-stack vibe-coding scoped to /Workspace/<repo>
   *  rather than a per-framework subdir. /projects/mobile returns mobile
   *  sub-apps only — for monorepos like yaver.io that means tasks always
   *  scope to mobile/ and never the Go agent / web / cli code.
   *  Distinct from listRepos() (further below) which is the git status
   *  enumerator over /repos/list.
   */
  async listWorkspaceRepos(): Promise<{
    repos: { name: string; path: string; branch?: string; framework?: string; gitRemote?: string; tags?: string[]; isMonorepo?: boolean; subframeworks?: string[] }[];
  }> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/projects`, {
      headers: this.authHeaders,
    });
    if (!res.ok) throw new Error(`Failed to list repos: ${res.status}`);
    const data = await res.json();
    const projects = Array.isArray(data.projects) ? data.projects : [];
    return {
      repos: projects.map((p: any) => ({
        name: p?.name ?? "",
        path: p?.path ?? "",
        branch: p?.branch,
        framework: p?.framework,
        gitRemote: p?.gitRemote,
        tags: Array.isArray(p?.tags) ? p.tags : [],
        isMonorepo: !!p?.isMonorepo || p?.framework === "monorepo",
        subframeworks: Array.isArray(p?.subframeworks) ? p.subframeworks : [],
      })),
    };
  }

  /** List mobile-capable projects discovered by the agent's framework-aware scanner. */
  async listMobileProjectsDetailed(): Promise<{
    projects: { name: string; path: string; branch?: string; framework?: string; executionMode?: string; primarySurface?: string; tags?: string[] }[];
    discovery?: {
      status?: "idle" | "discovering" | "partial" | "ready";
      discovering?: boolean;
      partiallyReady?: boolean;
      lastCompletedAt?: string;
    };
  }> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/projects/mobile`, {
      headers: this.authHeaders,
    });
    if (!res.ok) throw new Error(`Failed to list mobile projects: ${res.status}`);
    const data = await res.json();
    const projects = Array.isArray(data.projects) ? data.projects : [];
    return {
      projects: projects.map((p: any) => {
        const framework = typeof p?.framework === "string" ? p.framework : "";
        const tags = new Set<string>();
        if (framework) tags.add(framework);
        if (p?.mobileCapable) tags.add("mobile");
        if (typeof p?.primarySurface === "string" && p.primarySurface) tags.add(p.primarySurface);
        if (typeof p?.executionMode === "string" && p.executionMode) tags.add(p.executionMode);
        return {
          name: p?.name ?? "",
          path: p?.path ?? "",
          branch: p?.branch,
          framework,
          executionMode: p?.executionMode,
          primarySurface: p?.primarySurface,
          tags: Array.from(tags),
        };
      }),
      discovery: {
        status: data.scanning ? "discovering" : (data.scannedAt ? "ready" : "idle"),
        discovering: !!data.scanning,
        partiallyReady: !!data.scanning && projects.length > 0,
        lastCompletedAt: data.scannedAt,
      },
    };
  }

  /** List discovered projects on the machine. */
  async listProjects(): Promise<{ name: string; path: string; branch?: string; framework?: string; executionMode?: string; primarySurface?: string; gitRemote?: string; tags?: string[] }[]> {
    const data = await this.listProjectsDetailed();
    return data.projects;
  }

  /** Trigger a fresh machine-wide repo discovery. */
  async refreshProjects(): Promise<{ ok?: boolean; message?: string }> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/projects/refresh`, {
      method: "POST",
      headers: this.authHeaders,
    });
    if (!res.ok) throw new Error(`Failed to refresh projects: ${res.status}`);
    return res.json();
  }

  /** Trigger a fresh mobile-project scan for the Hot Reload tab. */
  async refreshMobileProjects(): Promise<{ ok?: boolean; message?: string }> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/projects/mobile`, {
      method: "POST",
      headers: this.authHeaders,
    });
    if (!res.ok) throw new Error(`Failed to refresh mobile projects: ${res.status}`);
    return res.json();
  }

  /** Switch agent to a different project (by fuzzy name or path). Optionally start dev server. */
  async switchProject(query: string, startDev: boolean = false): Promise<{
    path: string;
    project: { name: string; gitBranch?: string; framework?: string };
    devServer?: { running: boolean; framework?: string };
  }> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/projects/switch`, {
      method: 'POST',
      headers: { ...this.authHeaders, 'Content-Type': 'application/json' },
      body: JSON.stringify({ query, startDev }),
    });
    if (!res.ok) {
      const text = await res.text().catch(() => '');
      throw new Error(`Failed to switch project: ${text}`);
    }
    return res.json();
  }

  /** Get available actions for a project (deploy, hot reload, build, etc). */
  async getProjectActions(query: string): Promise<{
    project: string;
    path: string;
    actions: { label: string; target: string; type: string; framework?: string; platform?: string; command?: string; icon?: string }[];
  }> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/projects/actions?query=${encodeURIComponent(query)}`, {
      headers: this.authHeaders,
    });
    if (!res.ok) throw new Error(`Failed to get project actions: ${res.status}`);
    return res.json();
  }

  async getProjectActionsByPath(path: string): Promise<{
    project: string;
    path: string;
    actions: { label: string; target: string; type: string; framework?: string; platform?: string; command?: string; icon?: string }[];
  }> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/projects/actions?path=${encodeURIComponent(path)}`, {
      headers: this.authHeaders,
    });
    if (!res.ok) throw new Error(`Failed to get project actions: ${res.status}`);
    return res.json();
  }

  async getRemoteRuntimeCapabilities(workDir: string, framework: string): Promise<RemoteRuntimeCapabilities> {
    this.assertConnected();
    const url = `${this.baseUrl}/remote-runtime/capabilities?workDir=${encodeURIComponent(workDir)}&framework=${encodeURIComponent(framework)}`;
    const res = await fetch(url, { headers: this.authHeaders });
    if (!res.ok) throw new Error(`Failed to get remote runtime capabilities: ${res.status}`);
    return res.json();
  }

  async startRemoteRuntimeSession(workDir: string, framework: string, targetId: string, transportMode?: string): Promise<RemoteRuntimeSession> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/remote-runtime/sessions`, {
      method: "POST",
      headers: { ...this.authHeaders, "Content-Type": "application/json" },
      body: JSON.stringify({ workDir, framework, targetId, transportMode }),
    });
    const data = await res.json().catch(() => ({}));
    if (!res.ok) throw new Error(data?.error || `Failed to start remote runtime session: ${res.status}`);
    return data as RemoteRuntimeSession;
  }

  async sendRemoteRuntimeCommand(sessionId: string, command: "launch-feedback", source: string = "mobile"): Promise<{ ok: boolean; note?: string; protocol?: string }> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/remote-runtime/sessions/${encodeURIComponent(sessionId)}/command`, {
      method: "POST",
      headers: { ...this.authHeaders, "Content-Type": "application/json" },
      body: JSON.stringify({ command, source }),
    });
    const data = await res.json().catch(() => ({}));
    if (!res.ok) throw new Error(data?.error || `Failed to send remote runtime command: ${res.status}`);
    return data;
  }

  async getRemoteRuntimeSession(sessionId: string): Promise<RemoteRuntimeSession> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/remote-runtime/sessions/${encodeURIComponent(sessionId)}`, {
      headers: this.authHeaders,
    });
    const data = await res.json().catch(() => ({}));
    if (!res.ok) throw new Error(data?.error || `Failed to load remote runtime session: ${res.status}`);
    return data as RemoteRuntimeSession;
  }

  async closeRemoteRuntimeSession(sessionId: string): Promise<void> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/remote-runtime/sessions/${encodeURIComponent(sessionId)}`, {
      method: "DELETE",
      headers: this.authHeaders,
    });
    const data = await res.json().catch(() => ({}));
    if (!res.ok) throw new Error(data?.error || `Failed to close remote runtime session: ${res.status}`);
  }

  async createRemoteRuntimeWebRTCAnswer(sessionId: string, offer: { sdp?: string; type?: string }): Promise<{ session: RemoteRuntimeSession; answer: { sdp?: string; type?: string }; transport?: string; note?: string }> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/remote-runtime/sessions/${encodeURIComponent(sessionId)}/webrtc/offer`, {
      method: "POST",
      headers: { ...this.authHeaders, "Content-Type": "application/json" },
      body: JSON.stringify({ sdp: offer.sdp || "", type: offer.type || "offer" }),
    });
    const data = await res.json().catch(() => ({}));
    if (!res.ok) throw new Error(data?.error || `Failed to negotiate remote runtime WebRTC: ${res.status}`);
    return data as { session: RemoteRuntimeSession; answer: { sdp?: string; type?: string }; transport?: string; note?: string };
  }

  async fetchRemoteRuntimeFrame(sessionId: string): Promise<Blob> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/remote-runtime/sessions/${encodeURIComponent(sessionId)}/frame?ts=${Date.now()}`, {
      headers: this.authHeaders,
      cache: "no-store",
    });
    if (!res.ok) {
      const data = await res.json().catch(() => ({}));
      throw new Error(data?.error || `Failed to fetch remote runtime frame: ${res.status}`);
    }
    return await res.blob();
  }

  async sendRemoteRuntimeControl(sessionId: string, body: { action: "tap" | "text" | "back" | "home"; x?: number; y?: number; text?: string; key?: string }): Promise<RemoteRuntimeSession> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/remote-runtime/sessions/${encodeURIComponent(sessionId)}/control`, {
      method: "POST",
      headers: { ...this.authHeaders, "Content-Type": "application/json" },
      body: JSON.stringify(body),
    });
    const data = await res.json().catch(() => ({}));
    if (!res.ok) throw new Error(data?.error || `Failed to send remote runtime control: ${res.status}`);
    return (data?.session || data) as RemoteRuntimeSession;
  }

  // ── Vibing (AI pair programming widget) ─────────────────────────

  /** Get vibing state: AI-generated suggestions, quick actions, history for a project. */
  async getVibingState(query: string): Promise<{
    project: string;
    path: string;
    framework?: string;
    suggestions: { id: string; icon: string; label: string; desc: string; category: string; prompt: string; priority: number }[];
    quickActions: { id: string; icon: string; label: string; desc: string; category: string; prompt: string; priority: number }[];
    history: string[];
  }> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/vibing?query=${encodeURIComponent(query)}`, {
      headers: this.authHeaders,
    });
    if (!res.ok) throw new Error(`Failed to get vibing state: ${res.status}`);
    return res.json();
  }

  /** Execute a vibing suggestion as a task or structured runtime action. */
  async executeVibingSuggestion(prompt: string, projectPath: string): Promise<{ taskId?: string; runtimeDeploy?: any; message?: string }> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/vibing/execute`, {
      method: 'POST',
      headers: { ...this.authHeaders, 'Content-Type': 'application/json' },
      body: JSON.stringify({ prompt, projectPath }),
    });
    if (!res.ok) throw new Error(`Failed to execute: ${res.status}`);
    return res.json();
  }

  // ── Todo List (queued bug reports for batch implementation) ──────

  /** Get the count of pending todo items. */
  async todoCount(): Promise<number> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/todolist/count`, {
      headers: this.authHeaders,
    });
    if (!res.ok) return 0;
    const data = await res.json();
    return data.count ?? 0;
  }

  /** List all todo items. */
  async listTodoItems(): Promise<{ id: string; description: string; status: string; numScreenshots: number; hasAudio: boolean; createdAt: string; taskId?: string }[]> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/todolist`, {
      headers: this.authHeaders,
    });
    if (!res.ok) throw new Error(`Failed to list todo items: ${res.status}`);
    const data = await res.json();
    return data.items ?? [];
  }

  /** Add a todo item (text description + optional screenshots). */
  async addTodoItem(description: string, source: string = 'mobile'): Promise<{ id: string; count: number }> {
    this.assertConnected();
    const formData = new FormData();
    formData.append('metadata', JSON.stringify({ description, source }));
    const res = await fetch(`${this.baseUrl}/todolist`, {
      method: 'POST',
      headers: { Authorization: `Bearer ${this.token}` },
      body: formData,
    });
    if (!res.ok) throw new Error(`Failed to add todo item: ${res.status}`);
    return res.json();
  }

  /** Remove a todo item. */
  async removeTodoItem(id: string): Promise<void> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/todolist/${id}`, {
      method: 'DELETE',
      headers: this.authHeaders,
    });
    if (!res.ok) throw new Error(`Failed to remove todo item: ${res.status}`);
  }

  /** Implement all pending todo items as a batch. */
  async implementAllTodos(): Promise<{ taskId: string; itemCount: number }> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/todolist/implement-all`, {
      method: 'POST',
      headers: { ...this.authHeaders, 'Content-Type': 'application/json' },
    });
    if (!res.ok) throw new Error(`Failed to implement all: ${res.status}`);
    return res.json();
  }

  /** Implement a single todo item. */
  async implementTodoItem(id: string): Promise<{ taskId: string }> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/todolist/${id}/implement`, {
      method: 'POST',
      headers: { ...this.authHeaders, 'Content-Type': 'application/json' },
    });
    if (!res.ok) throw new Error(`Failed to implement todo: ${res.status}`);
    return res.json();
  }

  /** Toggle auto-consume mode. */
  async setAutoConsume(enabled: boolean): Promise<void> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/todolist/auto-consume`, {
      method: 'POST',
      headers: { ...this.authHeaders, 'Content-Type': 'application/json' },
      body: JSON.stringify({ enabled }),
    });
    if (!res.ok) throw new Error(`Failed to set auto-consume: ${res.status}`);
  }

  /** Get autopilot (auto-driving) mode status. */
  async getAutopilot(): Promise<boolean> {
    this.assertConnected();
    try {
      const res = await fetch(`${this.baseUrl}/autopilot`, {
        headers: this.authHeaders,
      });
      if (!res.ok) return false;
      const data = await res.json();
      return data.enabled ?? false;
    } catch {
      return false;
    }
  }

  /** Toggle autopilot (auto-driving) mode. */
  async setAutopilot(enabled: boolean): Promise<void> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/autopilot`, {
      method: 'POST',
      headers: { ...this.authHeaders, 'Content-Type': 'application/json' },
      body: JSON.stringify({ enabled }),
    });
    if (!res.ok) throw new Error(`Failed to set autopilot: ${res.status}`);
  }

  /** Smart chat: auto-classifies message as todo item, continuation, or immediate action. */
  async smartChat(message: string, source: string = 'mobile'): Promise<{
    intent: 'todo' | 'action' | 'continuation';
    todoItemId?: string;
    taskId?: string;
    todoCount?: number;
    project?: string;
    acted: boolean;
  }> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/todolist/classify`, {
      method: 'POST',
      headers: { ...this.authHeaders, 'Content-Type': 'application/json' },
      body: JSON.stringify({ message, source, autoAct: true }),
    });
    if (!res.ok) throw new Error(`Failed to classify: ${res.status}`);
    return res.json();
  }

  /** Get full agent info including project, dev server, todo/task stats. */
  async agentInfo(): Promise<{
    hostname: string;
    version: string;
    workDir: string;
    project: { name: string; path: string; gitBranch?: string; framework?: string };
    devServer?: { running: boolean; framework?: string };
    todoCount: number;
    todoTotal: number;
    todoDone: number;
    todoImplementing: number;
    autoConsume: boolean;
    taskStats: { total: number; done: number; running: number; failed: number };
  }> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/info`, {
      headers: this.authHeaders,
    });
    if (!res.ok) throw new Error(`Failed to get agent info: ${res.status}`);
    return res.json();
  }

  /** Clear all todo items. */
  async clearTodoList(): Promise<number> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/todolist`, {
      method: 'DELETE',
      headers: this.authHeaders,
    });
    if (!res.ok) throw new Error(`Failed to clear todo list: ${res.status}`);
    const data = await res.json();
    return data.cleared ?? 0;
  }

  // ── Exec (remote command execution) ─────────────────────────────

  /** Start a command on the remote agent. */
  async startExec(command: string, opts?: ExecOptions): Promise<{ execId: string; pid: number }> {
    this.assertConnected();
    const body: Record<string, unknown> = { command };
    if (opts?.workDir) body.workDir = opts.workDir;
    if (opts?.timeout) body.timeout = opts.timeout;
    if (opts?.env) body.env = opts.env;
    const res = await fetch(`${this.baseUrl}/exec`, {
      method: "POST",
      headers: { ...this.authHeaders, "Content-Type": "application/json" },
      body: JSON.stringify(body),
    });
    if (!res.ok) throw new Error(`Failed to start exec: ${res.status}`);
    const data = await res.json();
    if (!data.ok) throw new Error(data.error || "Failed to start exec");
    return { execId: data.execId, pid: data.pid };
  }

  /** Get exec session details. */
  async getExec(execId: string): Promise<ExecSession> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/exec/${execId}`, {
      headers: this.authHeaders,
    });
    if (!res.ok) throw new Error(`Failed to get exec: ${res.status}`);
    const data = await res.json();
    return data.exec;
  }

  /** List all exec sessions. */
  async listExecs(): Promise<ExecSession[]> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/exec`, {
      headers: this.authHeaders,
    });
    if (!res.ok) throw new Error(`Failed to list execs: ${res.status}`);
    const data = await res.json();
    return data.execs || [];
  }

  /** Wait until an exec session reaches a terminal state. */
  async waitForExec(execId: string, opts?: { timeoutMs?: number; pollMs?: number }): Promise<ExecSession> {
    const deadline = Date.now() + (opts?.timeoutMs ?? 5 * 60_000);
    const pollMs = opts?.pollMs ?? 1000;
    while (true) {
      const exec = await this.getExec(execId);
      if (exec.status === "completed" || exec.status === "failed" || exec.status === "killed") {
        return exec;
      }
      if (Date.now() > deadline) throw new Error("Timed out waiting for remote exec");
      await new Promise((resolve) => setTimeout(resolve, pollMs));
    }
  }

  /** Send stdin input to a running exec session. */
  async sendExecInput(execId: string, input: string): Promise<void> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/exec/${execId}/input`, {
      method: "POST",
      headers: { ...this.authHeaders, "Content-Type": "application/json" },
      body: JSON.stringify({ input }),
    });
    if (!res.ok) throw new Error(`Failed to send exec input: ${res.status}`);
  }

  /** Send a signal to a running exec session. */
  async signalExec(execId: string, signal: string): Promise<void> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/exec/${execId}/signal`, {
      method: "POST",
      headers: { ...this.authHeaders, "Content-Type": "application/json" },
      body: JSON.stringify({ signal }),
    });
    if (!res.ok) throw new Error(`Failed to signal exec: ${res.status}`);
  }

  /** Kill and remove an exec session. */
  async killExec(execId: string): Promise<void> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/exec/${execId}`, {
      method: "DELETE",
      headers: this.authHeaders,
    });
    if (!res.ok) throw new Error(`Failed to kill exec: ${res.status}`);
  }

  /** Get agent info (hostname, version, workDir). */
  async getInfo(): Promise<{ hostname: string; version: string; workDir: string } | null> {
    if (!this.isConnected && !this.hasConnectionInfo) return null;
    try {
      const res = await fetch(`${this.baseUrl}/info`, {
        headers: this.authHeaders,
      });
      if (!res.ok) return null;
      const data = await res.json();
      return {
        hostname: data.hostname || "",
        version: data.version || "",
        workDir: data.workDir || "",
      };
    } catch {
      return null;
    }
  }

  /** Get notification/integration config from agent. */
  async getNotificationsConfig(): Promise<Record<string, any> | null> {
    if (!this.isConnected && !this.hasConnectionInfo) return null;
    try {
      const res = await fetch(`${this.baseUrl}/notifications/config`, {
        headers: this.authHeaders,
      });
      if (!res.ok) return null;
      const data = await res.json();
      return data.config ?? null;
    } catch { return null; }
  }

  /** Save notification/integration config to agent. */
  async saveNotificationsConfig(config: Record<string, any>): Promise<boolean> {
    try {
      const res = await fetch(`${this.baseUrl}/notifications/config`, {
        method: "POST",
        headers: { ...this.authHeaders, "Content-Type": "application/json" },
        body: JSON.stringify(config),
      });
      return res.ok;
    } catch { return false; }
  }

  /** Test a notification channel. */
  async testNotification(channel: string): Promise<string> {
    try {
      const res = await fetch(`${this.baseUrl}/notifications/test`, {
        method: "POST",
        headers: { ...this.authHeaders, "Content-Type": "application/json" },
        body: JSON.stringify({ channel }),
      });
      if (!res.ok) return "Failed";
      const data = await res.json();
      return data.result ?? "Sent";
    } catch { return "Failed"; }
  }

  /** Get detailed agent status (runner health, processes, system info). */
  async getAgentStatus(): Promise<AgentStatus | null> {
    if (!this.isConnected && !this.hasConnectionInfo) return null;
    try {
      const res = await fetch(`${this.baseUrl}/agent/status`, {
        headers: this.authHeaders,
      });
      if (!res.ok) return null;
      const data = await res.json();
      return data.status || null;
    } catch {
      return null;
    }
  }

  /** Get available runners from the agent with install status. */
  async getRunners(): Promise<RunnerInfo[]> {
    if (!this.isConnected && !this.hasConnectionInfo) return [];
    try {
      const res = await fetch(`${this.baseUrl}/agent/runners`, {
        headers: this.authHeaders,
      });
      if (!res.ok) return [];
      const data = await res.json();
      return data.runners || [];
    } catch {
      return [];
    }
  }

  /**
   * Run a tiny probe through the named runner's CLI on the connected
   * agent. Mirrors the web's AgentClient.testRunner — see
   * desktop/agent/runner_test_http.go for the wire contract. The mobile
   * device card uses this for its per-LLM "Test" button; on a
   * `needsAuth + supportsBrowserAuth` result it falls through to the
   * existing /runner-auth/browser/start flow.
   */
  async testRunner(
    runner: string,
    opts?: { prompt?: string; model?: string },
  ): Promise<RunnerTestResult> {
    if (!this.isConnected && !this.hasConnectionInfo) {
      throw new Error("agent not reachable");
    }
    const res = await fetch(`${this.baseUrl}/agent/runners/test`, {
      method: "POST",
      headers: { ...this.authHeaders, "Content-Type": "application/json" },
      body: JSON.stringify({ runner, prompt: opts?.prompt, model: opts?.model }),
    });
    if (!res.ok) {
      const body = await res.text().catch(() => "");
      throw new Error(`testRunner(${runner}) ${res.status}: ${body || res.statusText}`);
    }
    return (await res.json()) as RunnerTestResult;
  }

  /**
   * Start a browser-based OAuth flow for a runner CLI (claude / codex)
   * on the connected agent OR on a peer device routed via /peer/<id>.
   * Mirrors the web AgentClient.startRunnerBrowserAuth — see
   * desktop/agent/runner_auth_browser_http.go.
   *
   * Returns a session record with `id`, `openUrl`, optional `code`,
   * and `status`. Caller polls /runner-auth/browser/status until it
   * flips to "completed" / "failed" / "cancelled". For Claude Code
   * specifically the user must paste the callback code back via
   * submitRunnerBrowserAuthCode().
   */
  async startRunnerBrowserAuth(
    runner: string,
    target?: string,
  ): Promise<RunnerBrowserAuthSession> {
    this.assertConnected();
    const base = this.peerEndpoint(target, "/runner-auth/browser/start");
    const res = await fetch(base, {
      method: "POST",
      headers: { ...this.authHeaders, "Content-Type": "application/json" },
      body: JSON.stringify({ runner }),
    });
    const data = await res.json().catch(() => ({}));
    if (!res.ok) {
      throw new Error(data?.error || `startRunnerBrowserAuth ${res.status}`);
    }
    return unwrapRunnerBrowserAuthEnvelope(data);
  }

  async getRunnerBrowserAuthStatus(
    sessionId: string,
    target?: string,
  ): Promise<RunnerBrowserAuthSession> {
    if (!this.isConnected && !this.hasConnectionInfo) {
      throw new Error("agent not reachable");
    }
    const base = this.peerEndpoint(target, "/runner-auth/browser/status");
    const url = new URL(base);
    url.searchParams.set("id", sessionId);
    const res = await fetch(url.toString(), { headers: this.authHeaders });
    const data = await res.json().catch(() => ({}));
    if (!res.ok) {
      throw new Error(data?.error || `getRunnerBrowserAuthStatus ${res.status}`);
    }
    return unwrapRunnerBrowserAuthEnvelope(data);
  }

  async cancelRunnerBrowserAuth(sessionId: string, target?: string): Promise<void> {
    if (!this.isConnected && !this.hasConnectionInfo) return;
    const base = this.peerEndpoint(target, "/runner-auth/browser/cancel");
    const url = new URL(base);
    url.searchParams.set("id", sessionId);
    await fetch(url.toString(), { method: "POST", headers: this.authHeaders }).catch(() => {});
  }

  async submitRunnerBrowserAuthCode(
    sessionId: string,
    code: string,
    target?: string,
  ): Promise<RunnerBrowserAuthSession> {
    this.assertConnected();
    if (!sessionId) {
      throw new Error("submitRunnerBrowserAuthCode requires sessionId");
    }
    const base = this.peerEndpoint(target, "/runner-auth/browser/submit-code");
    // Agent's handleRunnerBrowserAuthSubmitCode reads `id` from the URL
    // query string — only `code` comes from the JSON body. Putting id in
    // the body alone made the agent answer 400 "missing id" on every
    // Claude paste-back attempt.
    const url = new URL(base);
    url.searchParams.set("id", sessionId);
    const res = await fetch(url.toString(), {
      method: "POST",
      headers: { ...this.authHeaders, "Content-Type": "application/json" },
      body: JSON.stringify({ code }),
    });
    const data = await res.json().catch(() => ({}));
    if (!res.ok) {
      throw new Error(data?.error || `submitRunnerBrowserAuthCode ${res.status}`);
    }
    return unwrapRunnerBrowserAuthEnvelope(data);
  }

  async runnerAuthStatus(target?: string): Promise<RunnerAuthStatusRow[]> {
    if (!this.isConnected && !this.hasConnectionInfo) return [];
    try {
      const base = this.peerEndpoint(target, "/runner-auth/status");
      const res = await fetch(base, { headers: this.authHeaders });
      if (!res.ok) return [];
      const data = await res.json().catch(() => ({}));
      return Array.isArray(data?.runners) ? data.runners : [];
    } catch {
      return [];
    }
  }

  /** Variant of runnerAuthStatus that distinguishes "the agent legitimately
   *  reported zero runners" (empty array) from "we couldn't reach the agent"
   *  (null). Callers that need to render "Couldn't audit" instead of falsely
   *  showing every runner as Not Installed should use this. The original
   *  runnerAuthStatus stays for back-compat with sites that just want a list. */
  async runnerAuthStatusOrNull(target?: string): Promise<RunnerAuthStatusRow[] | null> {
    if (!this.isConnected && !this.hasConnectionInfo) return null;
    try {
      const base = this.peerEndpoint(target, "/runner-auth/status");
      const res = await fetch(base, { headers: this.authHeaders });
      if (!res.ok) return null;
      const data = await res.json().catch(() => null);
      if (!data) return null;
      return Array.isArray(data?.runners) ? data.runners : [];
    } catch {
      return null;
    }
  }

  async runnerAuthSet(
    params: RunnerAuthSetParams,
    target?: string,
  ): Promise<{ ok: boolean; saved: string[]; runners: RunnerAuthStatusRow[]; error?: string }> {
    this.assertConnected();
    const base = this.peerEndpoint(target, "/runner-auth/set");
    const res = await fetch(base, {
      method: "POST",
      headers: { ...this.authHeaders, "Content-Type": "application/json" },
      body: JSON.stringify({
        runner: params.runner,
        openai_api_key: params.openaiApiKey,
        anthropic_api_key: params.anthropicApiKey,
        anthropic_auth_token: params.anthropicAuthToken,
        claude_code_oauth_token: params.claudeCodeOauthToken,
        glm_api_key: params.glmApiKey,
        zai_api_key: params.zaiApiKey,
        notes: params.notes,
      }),
    });
    const data = await res.json().catch(() => ({}));
    if (!res.ok) {
      return { ok: false, saved: [], runners: [], error: data?.error || `HTTP ${res.status}` };
    }
    return {
      ok: true,
      saved: Array.isArray(data?.saved) ? data.saved : [],
      runners: Array.isArray(data?.runners) ? data.runners : [],
    };
  }

  async runnerAuthSetup(
    params: RunnerAuthSetupParams,
    target?: string,
  ): Promise<{
    ok: boolean;
    runner: string;
    installed: boolean;
    installAttempt?: boolean;
    ready: boolean;
    authConfigured: boolean;
    authSource?: string;
    detail?: string;
    warning?: string;
    notes?: string[];
    error?: string;
  }> {
    this.assertConnected();
    const base = this.peerEndpoint(target, "/runner-auth/setup");
    const res = await fetch(base, {
      method: "POST",
      headers: { ...this.authHeaders, "Content-Type": "application/json" },
      body: JSON.stringify({
        runner: params.runner,
        openai_api_key: params.openaiApiKey,
        anthropic_api_key: params.anthropicApiKey,
        anthropic_auth_token: params.anthropicAuthToken,
        claude_code_oauth_token: params.claudeCodeOauthToken,
        glm_api_key: params.glmApiKey,
        zai_api_key: params.zaiApiKey,
        notes: params.notes,
        install_if_missing: params.installIfMissing,
        codex_login: params.codexLogin,
        setup_mcp: params.setupMcp,
        allow_install_only: params.allowInstallOnly,
      }),
    });
    const data = await res.json().catch(() => ({}));
    if (!res.ok) {
      return {
        ok: false,
        runner: String(data?.runner || params.runner || ""),
        installed: data?.installed === true,
        installAttempt: data?.installAttempt === true,
        ready: data?.ready === true,
        authConfigured: data?.authConfigured === true,
        authSource: typeof data?.authSource === "string" ? data.authSource : undefined,
        detail: typeof data?.detail === "string" ? data.detail : undefined,
        warning: typeof data?.warning === "string" ? data.warning : undefined,
        notes: Array.isArray(data?.notes) ? data.notes : undefined,
        error: data?.error || `HTTP ${res.status}`,
      };
    }
    return {
      ok: data?.ok === true,
      runner: String(data?.runner || params.runner || ""),
      installed: data?.installed === true,
      installAttempt: data?.installAttempt === true,
      ready: data?.ready === true,
      authConfigured: data?.authConfigured === true,
      authSource: typeof data?.authSource === "string" ? data.authSource : undefined,
      detail: typeof data?.detail === "string" ? data.detail : undefined,
      warning: typeof data?.warning === "string" ? data.warning : undefined,
      notes: Array.isArray(data?.notes) ? data.notes : undefined,
    };
  }

  async machineOnboardingStatus(target?: string): Promise<MachineOnboardingProviderStatus[]> {
    if (!this.isConnected && !this.hasConnectionInfo) return [];
    try {
      const base = this.peerEndpoint(target, "/machine/onboarding/status");
      const res = await fetch(base, { headers: this.authHeaders });
      if (!res.ok) return [];
      const data = await res.json().catch(() => ({}));
      return Array.isArray(data?.providers) ? data.providers : [];
    } catch {
      return [];
    }
  }

  async machineOnboardingApply(
    params: MachineOnboardingApplyParams,
    target?: string,
  ): Promise<{ ok: boolean; applied: string[]; providers: MachineOnboardingProviderStatus[]; error?: string }> {
    this.assertConnected();
    const base = this.peerEndpoint(target, "/machine/onboarding/apply");
    const res = await fetch(base, {
      method: "POST",
      headers: { ...this.authHeaders, "Content-Type": "application/json" },
      body: JSON.stringify({
        openai_api_key: params.openaiApiKey,
        github_token: params.githubToken,
        gitlab_token: params.gitlabToken,
        gitlab_host: params.gitlabHost,
        apply_clone: params.applyClone,
        apply_ci_token: params.applyCiToken,
        notes: params.notes,
      }),
    });
    const data = await res.json().catch(() => ({}));
    if (!res.ok) {
      return { ok: false, applied: [], providers: [], error: data?.error || `HTTP ${res.status}` };
    }
    return {
      ok: true,
      applied: Array.isArray(data?.applied) ? data.applied : [],
      providers: Array.isArray(data?.providers) ? data.providers : [],
    };
  }

  async machineOnboardingRemove(
    params: {
      providers: Array<"github" | "gitlab">;
      gitlabHost?: string;
      removeClone?: boolean;
      removeCiToken?: boolean;
    },
    target?: string,
  ): Promise<{ ok: boolean; removed: string[]; providers: MachineOnboardingProviderStatus[]; error?: string }> {
    this.assertConnected();
    const base = this.peerEndpoint(target, "/machine/onboarding/remove");
    const res = await fetch(base, {
      method: "POST",
      headers: { ...this.authHeaders, "Content-Type": "application/json" },
      body: JSON.stringify({
        providers: params.providers,
        gitlab_host: params.gitlabHost,
        remove_clone: params.removeClone,
        remove_ci_token: params.removeCiToken,
      }),
    });
    const data = await res.json().catch(() => ({}));
    if (!res.ok) {
      return { ok: false, removed: [], providers: [], error: data?.error || `HTTP ${res.status}` };
    }
    return {
      ok: true,
      removed: Array.isArray(data?.removed) ? data.removed : [],
      providers: Array.isArray(data?.providers) ? data.providers : [],
    };
  }

  async getToolchainSyncProfile(): Promise<EnvironmentProfile | null> {
    if (!this.isConnected && !this.hasConnectionInfo) return null;
    try {
      const res = await fetch(`${this.baseUrl}/agent/toolchain-sync/profile`, {
        headers: this.authHeaders,
      });
      if (!res.ok) return null;
      const data = await res.json().catch(() => ({}));
      return (data?.profile as EnvironmentProfile) || null;
    } catch {
      return null;
    }
  }

  async applyToolchainSync(params: {
    profile?: EnvironmentProfile;
    sourceDeviceId?: string;
    installMissing?: boolean;
    syncKinds?: string[];
    syncPayload?: Record<string, SyncItem[]>;
    includeGitCredentials?: boolean;
    gitCredentials?: { host: string; username?: string; token: string }[];
    removeMissing?: boolean;
    dryRun?: boolean;
  }): Promise<EnvironmentProfileApplyResult> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/agent/toolchain-sync/apply`, {
      method: "POST",
      headers: { ...this.authHeaders, "Content-Type": "application/json" },
      body: JSON.stringify({
        profile: params.profile,
        sourceDeviceId: params.sourceDeviceId ?? "",
        installMissing: !!params.installMissing,
        syncKinds: params.syncKinds ?? [],
        syncPayload: params.syncPayload ?? {},
        includeGitCredentials: !!params.includeGitCredentials,
        gitCredentials: params.gitCredentials ?? [],
        removeMissing: !!params.removeMissing,
        dryRun: params.dryRun !== false,
      }),
    });
    const data = await res.json().catch(() => ({}));
    if (!res.ok) throw new Error(data?.error || `apply environment profile: HTTP ${res.status}`);
    return data as EnvironmentProfileApplyResult;
  }

  async getEnvironmentProfile(): Promise<EnvironmentProfile | null> {
    return this.getToolchainSyncProfile();
  }

  async applyEnvironmentProfile(params: {
    profile?: EnvironmentProfile;
    sourceDeviceId?: string;
    installMissing?: boolean;
    syncKinds?: string[];
    syncPayload?: Record<string, SyncItem[]>;
    includeGitCredentials?: boolean;
    gitCredentials?: { host: string; username?: string; token: string }[];
    removeMissing?: boolean;
    dryRun?: boolean;
  }): Promise<EnvironmentProfileApplyResult> {
    return this.applyToolchainSync(params);
  }

  /** Ping the agent and return round-trip time in milliseconds. */
  async ping(): Promise<{ ok: boolean; rttMs: number; hostname?: string; version?: string; timedOut?: boolean }> {
    if (!this.isConnected && !this.hasConnectionInfo) {
      return { ok: false, rttMs: -1 };
    }
    const start = Date.now();
    const controller = new AbortController();
    const timeout = setTimeout(() => controller.abort(), 5000);
    try {
      const res = await fetch(`${this.baseUrl}/health`, {
        headers: this.authHeaders,
        signal: controller.signal,
      });
      clearTimeout(timeout);
      const rttMs = Date.now() - start;
      if (!res.ok) return { ok: false, rttMs };
      const data = await res.json();
      return {
        ok: true,
        rttMs,
        hostname: data.hostname,
        version: data.version,
      };
    } catch {
      clearTimeout(timeout);
      const elapsed = Date.now() - start;
      return { ok: false, rttMs: elapsed, timedOut: elapsed >= 5000 };
    }
  }

  /** Shutdown the yaver agent remotely. */
  async shutdownAgent(): Promise<boolean> {
    if (!this.isConnected && !this.hasConnectionInfo) return false;
    try {
      const res = await fetch(`${this.baseUrl}/agent/shutdown`, {
        method: "POST",
        headers: this.authHeaders,
      });
      return res.ok;
    } catch {
      return false;
    }
  }

  async infraSummary(): Promise<InfraSummary> {
    if (!this.isConnected && !this.hasConnectionInfo) throw new Error("Not connected");
    const res = await fetch(`${this.baseUrl}/infra/summary`, { headers: this.authHeaders });
    if (!res.ok) throw new Error(`Failed to fetch infra summary: ${res.status}`);
    return res.json();
  }

  async capabilitySnapshot(target?: string): Promise<CapabilitySnapshot | null> {
    if (!this.isConnected && !this.hasConnectionInfo) return null;
    try {
      const base = this.peerEndpoint(target, "/capabilities/snapshot");
      const res = await fetch(base, { headers: this.authHeaders });
      if (!res.ok) return null;
      const data = await res.json();
      return (data?.snapshot as CapabilitySnapshot) ?? null;
    } catch {
      return null;
    }
  }

  async infraServiceAction(scope: "dev" | "system", name: string, action: "start" | "stop" | "restart" | "status"): Promise<any> {
    if (!this.isConnected && !this.hasConnectionInfo) throw new Error("Not connected");
    const res = await fetch(`${this.baseUrl}/infra/services/action`, {
      method: "POST",
      headers: { ...this.authHeaders, "Content-Type": "application/json" },
      body: JSON.stringify({ scope, name, action }),
    });
    return res.json();
  }

  async infraPower(action: "agent_shutdown" | "host_reboot"): Promise<any> {
    if (!this.isConnected && !this.hasConnectionInfo) throw new Error("Not connected");
    const res = await fetch(`${this.baseUrl}/infra/power`, {
      method: "POST",
      headers: { ...this.authHeaders, "Content-Type": "application/json" },
      body: JSON.stringify({ action, confirm: true }),
    });
    return res.json();
  }

  async machineRemove(phrase: string): Promise<any> {
    if (!this.isConnected && !this.hasConnectionInfo) throw new Error("Not connected");
    const res = await fetch(`${this.baseUrl}/machine/remove`, {
      method: "POST",
      headers: { ...this.authHeaders, "Content-Type": "application/json" },
      body: JSON.stringify({ confirm: true, phrase }),
    });
    return res.json();
  }

  /** Clean up old tasks, images, and logs on the desktop agent. */
  async cleanAgent(days: number = 30): Promise<{ tasksRemoved: number; imagesRemoved: number; bytesFreed: number }> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/agent/clean`, {
      method: "POST",
      headers: { ...this.authHeaders, "Content-Type": "application/json" },
      body: JSON.stringify({ days }),
    });
    if (!res.ok) throw new Error(`Failed to clean agent: ${res.status}`);
    const data = await res.json();
    return data.result;
  }

  /** Restart the runner on the desktop agent (e.g. after all crash retries exhausted). */
  async restartRunner(): Promise<boolean> {
    if (!this.isConnected && !this.hasConnectionInfo) return false;
    try {
      const res = await fetch(`${this.baseUrl}/agent/runner/restart`, {
        method: "POST",
        headers: this.authHeaders,
      });
      return res.ok;
    } catch {
      return false;
    }
  }

  /** Switch the runner on the desktop agent. Returns error message if runner not found. */
  async switchRunner(runnerId: string): Promise<{ ok: boolean; runner?: string; error?: string }> {
    if (!this.isConnected && !this.hasConnectionInfo) return { ok: false, error: "Not connected" };
    try {
      const res = await fetch(`${this.baseUrl}/agent/runner/switch`, {
        method: "POST",
        headers: { ...this.authHeaders, "Content-Type": "application/json" },
        body: JSON.stringify({ runnerId }),
      });
      const data = await res.json();
      if (!res.ok) return { ok: false, error: data.error || `HTTP ${res.status}` };
      return { ok: true, runner: data.runner };
    } catch (e) {
      return { ok: false, error: e instanceof Error ? e.message : "Unknown error" };
    }
  }

  /** Delete all finished tasks. */
  async deleteAllTasks(): Promise<number> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/tasks`, {
      method: "DELETE",
      headers: this.authHeaders,
    });
    if (!res.ok) throw new Error(`Failed to delete all: ${res.status}`);
    const data = await res.json();
    return data.deleted || 0;
  }

  // ── Tmux Session Management ─────────────────────────────────────────

  /** List all tmux sessions on the connected machine. */
  async listTmuxSessions(): Promise<TmuxSession[]> {
    if (!this.isConnected && !this.hasConnectionInfo) return [];
    try {
      const res = await fetch(`${this.baseUrl}/tmux/sessions`, {
        headers: this.authHeaders,
      });
      if (!res.ok) return [];
      const data = await res.json();
      return data.sessions || [];
    } catch {
      return [];
    }
  }

  /** Adopt a tmux session as a Yaver task. Returns the created task. */
  async adoptTmuxSession(sessionName: string): Promise<{ taskId: string; session: string }> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/tmux/adopt`, {
      method: "POST",
      headers: { ...this.authHeaders, "Content-Type": "application/json" },
      body: JSON.stringify({ session: sessionName }),
    });
    if (!res.ok) {
      const data = await res.json().catch(() => ({}));
      throw new Error(data.error || `Failed to adopt session: ${res.status}`);
    }
    return res.json();
  }

  /** Detach an adopted tmux session (stop monitoring, session keeps running). */
  async detachTmuxSession(taskId: string): Promise<void> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/tmux/detach`, {
      method: "POST",
      headers: { ...this.authHeaders, "Content-Type": "application/json" },
      body: JSON.stringify({ taskId }),
    });
    if (!res.ok) {
      const data = await res.json().catch(() => ({}));
      throw new Error(data.error || `Failed to detach: ${res.status}`);
    }
  }

  /** Send keyboard input to an adopted tmux session. */
  async sendTmuxInput(taskId: string, input: string): Promise<void> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/tmux/input`, {
      method: "POST",
      headers: { ...this.authHeaders, "Content-Type": "application/json" },
      body: JSON.stringify({ taskId, input }),
    });
    if (!res.ok) {
      const data = await res.json().catch(() => ({}));
      throw new Error(data.error || `Failed to send input: ${res.status}`);
    }
  }

  // ── Monorepo detection ─────────────────────────────────────────────

  /** Classify the framework composition of a directory on the connected agent.
   *  Returns the same Monorepo shape the agent's DetectMonorepo emits — list of
   *  DetectedProject with framework (flutter | expo | react-native | next | vite |
   *  unity | iosNative | androidNative | swift-package | gradle-jvm) plus tags.
   *  When `dir` is omitted, classifies the agent's current work directory. */
  async detectMonorepo(dir?: string, maxDepth?: number): Promise<{
    root: string;
    gitBranch?: string;
    gitRemote?: string;
    projects: Array<{
      name: string;
      path: string;
      relPath: string;
      framework: string;
      tags?: string[];
      hasTests: boolean;
      hasGit: boolean;
      manifest?: string;
    }>;
    isMonorepo: boolean;
    hasManifest: boolean;
    frameworks: string[];
  }> {
    this.assertConnected();
    const params = new URLSearchParams();
    if (dir) params.set('dir', dir);
    if (maxDepth) params.set('maxDepth', String(maxDepth));
    const qs = params.toString();
    const url = `${this.baseUrl}/projects/monorepo${qs ? '?' + qs : ''}`;
    const resp = await this.fetchWithTimeout(url, { headers: this.authHeaders }, 15_000);
    if (!resp.ok) {
      let msg = `Monorepo detect failed: ${resp.status}`;
      try { const err = await resp.json(); if (err?.error) msg = err.error; } catch { /* keep status */ }
      throw new Error(msg);
    }
    return resp.json();
  }

  // ── Builds ────────────────────────────────────────────────────────

  /** List all builds on the connected agent. */
  async listBuilds(): Promise<BuildSummary[]> {
    const resp = await this.fetchWithTimeout(`${this.baseUrl}/builds`, {
      headers: this.authHeaders,
    }, 10_000);
    if (!resp.ok) return [];
    return resp.json();
  }

  /** Get detailed info for a specific build. */
  async getBuild(id: string): Promise<BuildInfo | null> {
    const resp = await this.fetchWithTimeout(`${this.baseUrl}/builds/${id}`, {
      headers: this.authHeaders,
    }, 10_000);
    if (!resp.ok) return null;
    return resp.json();
  }

  /** Get the URL for downloading a build artifact. */
  getArtifactUrl(buildId: string): string {
    return `${this.baseUrl}/builds/${buildId}/artifact`;
  }

  async listUnityRuns(): Promise<{
    ok: boolean;
    status?: string;
    stage?: string;
    projectPath?: string;
    mode?: string;
    buildTarget?: string;
    executeMethod?: string;
    outputPath?: string;
    executablePath?: string;
    logPath?: string;
    resultsPath?: string;
    summary?: string;
    artifacts?: string[];
    nextAction?: string;
    command?: string[];
  }[]> {
    const resp = await this.fetchWithTimeout(`${this.baseUrl}/unity/runs`, {
      headers: this.authHeaders,
    }, 10_000);
    if (!resp.ok) return [];
    return resp.json();
  }

  /** Start a new build on the connected agent. */
  async startBuild(platform: string, workDir?: string, installOnDevice?: boolean): Promise<BuildInfo> {
    this.assertConnected();
    const resp = await this.fetchWithTimeout(`${this.baseUrl}/builds`, {
      method: "POST",
      headers: { ...this.authHeaders, "Content-Type": "application/json" },
      body: JSON.stringify({ platform, workDir: workDir || "", installOnDevice: installOnDevice || false }),
    }, 10_000);
    if (!resp.ok) throw new Error(`Start build failed: ${resp.status}`);
    return resp.json();
  }

  /** Start a native iOS Swift / native Android Kotlin / Flutter build on the connected
   *  agent. The agent resolves (platform, target) to a concrete BuildPlatform and runs
   *  xcodebuild / gradle / flutter, optionally installing on a connected device.
   *  Use this for non-RN apps; for React Native + Hermes use mobileProjectBuild() / dev_start. */
  async startNativeBuild(
    platform: 'iosNative' | 'androidNative' | 'flutter',
    target: 'device' | 'simulator' | 'testflight' | 'playstore' | 'local' | 'apk' | 'aab' | 'ipa' = 'device',
    workDir?: string,
    extras?: { scheme?: string; flavor?: string; installOnDevice?: boolean; args?: string[] },
  ): Promise<BuildInfo> {
    this.assertConnected();
    const args: string[] = [];
    if (platform === 'iosNative' && extras?.scheme) args.push(extras.scheme);
    if (platform === 'androidNative' && extras?.flavor) args.push(extras.flavor);
    if (extras?.args?.length) args.push(...extras.args);

    const installOnDevice = extras?.installOnDevice
      ?? (target === 'device' || target === 'simulator');

    const resp = await this.fetchWithTimeout(`${this.baseUrl}/builds`, {
      method: "POST",
      headers: { ...this.authHeaders, "Content-Type": "application/json" },
      body: JSON.stringify({
        platform,
        target,
        workDir: workDir || "",
        args,
        installOnDevice,
      }),
    }, 15_000);
    if (!resp.ok) {
      let msg = `Native build failed: ${resp.status}`;
      try { const err = await resp.json(); if (err?.error) msg = err.error; } catch { /* keep status */ }
      throw new Error(msg);
    }
    return resp.json();
  }

  /** Cancel a running build. */
  async cancelBuild(id: string): Promise<void> {
    await this.fetchWithTimeout(`${this.baseUrl}/builds/${id}`, {
      method: "DELETE",
      headers: this.authHeaders,
    }, 10_000);
  }

  /** Start a build targeting the current mobile platform.
   *  On iOS with direct LAN connection, automatically uses device install
   *  (builds and installs directly via xcrun devicectl). */
  async startBuildForMyPlatform(buildSystem: 'flutter' | 'gradle' | 'rn' | 'expo' | 'xcode', workDir?: string): Promise<BuildInfo> {
    // iOS + direct WiFi → build & install directly on device
    if (Platform.OS === 'ios' && this._connectionMode === 'direct') {
      return this.startBuild('xcode-device-install', workDir, true);
    }

    const platformMap: Record<string, Record<string, string>> = {
      flutter: { ios: 'flutter-ipa', android: 'flutter-apk' },
      gradle: { android: 'gradle-apk' },
      xcode: { ios: 'xcode-ipa' },
      rn: { ios: 'rn-ios', android: 'rn-android' },
      expo: { ios: 'expo-ios', android: 'expo-android' },
    };
    const platform = platformMap[buildSystem]?.[Platform.OS];
    if (!platform) throw new Error(`${buildSystem} does not support ${Platform.OS}`);
    return this.startBuild(platform, workDir);
  }

  async getPublishConfig(dir?: string): Promise<{ config: unknown; exists: boolean; path: string } | null> {
    const params = dir ? `?dir=${encodeURIComponent(dir)}` : "";
    const resp = await this.fetchWithTimeout(`${this.baseUrl}/publish/config${params}`, {
      headers: this.authHeaders,
    }, 10_000);
    if (!resp.ok) return null;
    return resp.json();
  }

  async savePublishConfig(dir: string, config: unknown): Promise<boolean> {
    const resp = await this.fetchWithTimeout(`${this.baseUrl}/publish/config`, {
      method: "POST",
      headers: { ...this.authHeaders, "Content-Type": "application/json" },
      body: JSON.stringify({ dir, config }),
    }, 10_000);
    return resp.ok;
  }

  async startPublish(dir: string, target: string, allowGitHubFallback = false): Promise<{
    id: string;
    targetId: string;
    status: string;
    provider: string;
  } | null> {
    const resp = await this.fetchWithTimeout(`${this.baseUrl}/publish/run`, {
      method: "POST",
      headers: { ...this.authHeaders, "Content-Type": "application/json" },
      body: JSON.stringify({ dir, target, allowGitHubFallback }),
    }, 10_000);
    if (!resp.ok) return null;
    return resp.json();
  }

  async listPublishes(): Promise<Array<{ id: string; targetId: string; status: string; provider: string }>> {
    const resp = await this.fetchWithTimeout(`${this.baseUrl}/publish/runs`, {
      headers: this.authHeaders,
    }, 10_000);
    if (!resp.ok) return [];
    return resp.json();
  }

  async getPublish(id: string): Promise<unknown | null> {
    const resp = await this.fetchWithTimeout(`${this.baseUrl}/publish/runs/${encodeURIComponent(id)}`, {
      headers: this.authHeaders,
    }, 10_000);
    if (!resp.ok) return null;
    return resp.json();
  }

  /** Sync vault entries from the connected agent and cache locally. */
  async syncVault(): Promise<void> {
    try {
      const resp = await this.fetchWithTimeout(`${this.baseUrl}/vault/list`, {
        headers: this.authHeaders,
      }, 10_000);
      if (resp.ok) {
        const entries = await resp.json();
        // Cache vault entries locally
        await AsyncStorage.setItem('vault_cache', JSON.stringify(entries));
      }
    } catch {
      // Silent fail — vault sync is best-effort
    }
  }

  // ── Vault CRUD (POST /vault/set, GET /vault/get, DELETE /vault/delete) ──

  async vaultList(): Promise<VaultEntrySummary[]> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/vault/list`, { headers: this.authHeaders });
    if (!res.ok) throw new Error(`vault list: HTTP ${res.status}`);
    const data = await res.json();
    return Array.isArray(data) ? data : [];
  }

  async vaultGet(name: string): Promise<VaultEntry> {
    this.assertConnected();
    const res = await fetch(
      `${this.baseUrl}/vault/get?name=${encodeURIComponent(name)}`,
      { headers: this.authHeaders },
    );
    if (!res.ok) throw new Error(`vault get: HTTP ${res.status}`);
    return res.json();
  }

  async vaultSet(entry: VaultEntry): Promise<void> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/vault/set`, {
      method: 'POST',
      headers: { ...this.authHeaders, 'Content-Type': 'application/json' },
      body: JSON.stringify(entry),
    });
    if (!res.ok) throw new Error(`vault set: HTTP ${res.status}`);
  }

  async vaultDelete(name: string): Promise<void> {
    this.assertConnected();
    const res = await fetch(
      `${this.baseUrl}/vault/delete?name=${encodeURIComponent(name)}`,
      { method: 'DELETE', headers: this.authHeaders },
    );
    if (!res.ok) throw new Error(`vault delete: HTTP ${res.status}`);
  }

  // ── Yaver Agent (mobile-embedded control-plane LLM) provider config ──

  async yaverAgentConfigGet(): Promise<{
    config: YaverAgentConfig;
    providers: YaverAgentProviderId[];
    defaults: YaverAgentProviderDefault[];
  }> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/yaver-agent/config`, { headers: this.authHeaders });
    if (!res.ok) throw new Error(`yaver-agent config get: HTTP ${res.status}`);
    return res.json();
  }

  async yaverAgentConfigSet(req: YaverAgentSetRequest): Promise<{
    config: YaverAgentConfig;
    providers: YaverAgentProviderId[];
    defaults: YaverAgentProviderDefault[];
  }> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/yaver-agent/config`, {
      method: 'POST',
      headers: { ...this.authHeaders, 'Content-Type': 'application/json' },
      body: JSON.stringify(req),
    });
    if (!res.ok) {
      const data = await res.json().catch(() => null);
      throw new Error(data?.error || `yaver-agent config set: HTTP ${res.status}`);
    }
    return res.json();
  }

  /**
   * GET /yaver-agent/audit on the connected device. Returns aggregated
   * lifecycle + per-runner auth state + ordered recommendations the
   * embedded LLM can act on without making N round trips itself.
   */
  async yaverAgentAudit(opts?: { workDir?: string }): Promise<YaverAgentDeviceAudit> {
    this.assertConnected();
    const qs = opts?.workDir ? `?workDir=${encodeURIComponent(opts.workDir)}` : '';
    const res = await fetch(`${this.baseUrl}/yaver-agent/audit${qs}`, {
      headers: this.authHeaders,
    });
    if (!res.ok) throw new Error(`yaver-agent audit: HTTP ${res.status}`);
    return res.json();
  }

  async syncList<T = any>(kind: string, since = 0): Promise<{ items: SyncItem<T>[]; latestAt: number }> {
    this.assertConnected();
    const res = await fetch(
      `${this.baseUrl}/sync/${encodeURIComponent(kind)}?since=${encodeURIComponent(String(since))}`,
      { headers: this.authHeaders },
    );
    if (!res.ok) throw new Error(`sync list: HTTP ${res.status}`);
    const data = await res.json();
    return {
      items: Array.isArray(data?.items) ? data.items : [],
      latestAt: typeof data?.latestAt === "number" ? data.latestAt : 0,
    };
  }

  async syncMerge<T = any>(kind: string, items: SyncItem<T>[]): Promise<{ applied: number; latestAt: number }> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/sync/${encodeURIComponent(kind)}`, {
      method: "POST",
      headers: { ...this.authHeaders, "Content-Type": "application/json" },
      body: JSON.stringify({ items }),
    });
    if (!res.ok) throw new Error(`sync merge: HTTP ${res.status}`);
    const data = await res.json();
    return {
      applied: typeof data?.applied === "number" ? data.applied : 0,
      latestAt: typeof data?.latestAt === "number" ? data.latestAt : 0,
    };
  }

  // ── API keys (labeled SDK tokens, local registry) ──

  async apiKeyList(): Promise<APIKeyRecord[]> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/apikeys`, { headers: this.authHeaders });
    if (!res.ok) throw new Error(`apikey list: HTTP ${res.status}`);
    const data = await res.json();
    return Array.isArray(data?.keys) ? data.keys : [];
  }

  async apiKeyCreate(opts: { label: string; scopes?: string[]; expiresInMs?: number; allowedCIDRs?: string[] }): Promise<{ token: string; tokenHash: string; label: string }> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/apikeys`, {
      method: 'POST',
      headers: { ...this.authHeaders, 'Content-Type': 'application/json' },
      body: JSON.stringify(opts),
    });
    const data = await res.json();
    if (!res.ok) throw new Error(data?.error || `apikey create: HTTP ${res.status}`);
    return data;
  }

  async apiKeyDisable(idOrLabel: string): Promise<void> {
    this.assertConnected();
    const res = await fetch(
      `${this.baseUrl}/apikeys?id=${encodeURIComponent(idOrLabel)}`,
      { method: 'DELETE', headers: this.authHeaders },
    );
    if (!res.ok) throw new Error(`apikey disable: HTTP ${res.status}`);
  }

  // ── Schedules (cron / runAt / repeatInterval) ──────────────────────

  async listSchedules(): Promise<ScheduledTask[]> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/schedules`, { headers: this.authHeaders });
    if (!res.ok) throw new Error(`schedules list: HTTP ${res.status}`);
    const data = await res.json();
    return Array.isArray(data?.schedules) ? data.schedules : [];
  }

  async createSchedule(spec: Partial<ScheduledTask> & { title: string }): Promise<ScheduledTask> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/schedules`, {
      method: 'POST',
      headers: { ...this.authHeaders, 'Content-Type': 'application/json' },
      body: JSON.stringify(spec),
    });
    const data = await res.json();
    if (!res.ok) throw new Error(data?.error || `schedule create: HTTP ${res.status}`);
    return data.schedule;
  }

  async deleteSchedule(id: string): Promise<void> {
    this.assertConnected();
    const res = await fetch(
      `${this.baseUrl}/schedules/${encodeURIComponent(id)}`,
      { method: 'DELETE', headers: this.authHeaders },
    );
    if (!res.ok) throw new Error(`schedule delete: HTTP ${res.status}`);
  }

  async pauseSchedule(id: string): Promise<void> {
    this.assertConnected();
    const res = await fetch(
      `${this.baseUrl}/schedules/${encodeURIComponent(id)}/pause`,
      { method: 'POST', headers: this.authHeaders },
    );
    if (!res.ok) throw new Error(`schedule pause: HTTP ${res.status}`);
  }

  async resumeSchedule(id: string): Promise<void> {
    this.assertConnected();
    const res = await fetch(
      `${this.baseUrl}/schedules/${encodeURIComponent(id)}/resume`,
      { method: 'POST', headers: this.authHeaders },
    );
    if (!res.ok) throw new Error(`schedule resume: HTTP ${res.status}`);
  }

  async runScheduleNow(id: string): Promise<void> {
    this.assertConnected();
    const res = await fetch(
      `${this.baseUrl}/schedules/${encodeURIComponent(id)}/run-now`,
      { method: 'POST', headers: this.authHeaders },
    );
    if (!res.ok) throw new Error(`schedule run-now: HTTP ${res.status}`);
  }

  // ── Accounts (cloud-provider credentials — stored on host only) ──

  async accountsList(): Promise<{ accounts: any[]; providers: any[] }> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/accounts`, { headers: this.authHeaders });
    if (!res.ok) throw new Error(`accounts list: HTTP ${res.status}`);
    return res.json();
  }

  async accountConnect(provider: string, label: string, fields: Record<string, string>): Promise<any> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/accounts/connect`, {
      method: 'POST',
      headers: { ...this.authHeaders, 'Content-Type': 'application/json' },
      body: JSON.stringify({ provider, label, fields }),
    });
    const data = await res.json();
    if (!res.ok) throw new Error(data?.error || `account connect: HTTP ${res.status}`);
    return data;
  }

  async accountDisconnect(provider: string): Promise<void> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/accounts/disconnect`, {
      method: 'POST',
      headers: { ...this.authHeaders, 'Content-Type': 'application/json' },
      body: JSON.stringify({ provider }),
    });
    if (!res.ok) throw new Error(`account disconnect: HTTP ${res.status}`);
  }

  // ── Files (read-only project browser) ──

  async filesRoots(): Promise<{ roots: { id: string; name: string; path: string }[] }> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/files/roots`, { headers: this.authHeaders });
    if (!res.ok) throw new Error(`files roots: HTTP ${res.status}`);
    return res.json();
  }

  async filesList(root: string, path = ''): Promise<any> {
    this.assertConnected();
    const p = new URLSearchParams({ root });
    if (path) p.set('path', path);
    const res = await fetch(`${this.baseUrl}/files/list?${p}`, { headers: this.authHeaders });
    if (!res.ok) throw new Error(`files list: HTTP ${res.status}`);
    return res.json();
  }

  async filesRead(root: string, path: string): Promise<any> {
    this.assertConnected();
    const p = new URLSearchParams({ root, path });
    const res = await fetch(`${this.baseUrl}/files/read?${p}`, { headers: this.authHeaders });
    if (!res.ok) throw new Error(`files read: HTTP ${res.status}`);
    return res.json();
  }

  // ── Shared storage (NAS / SMB / S3 / Azure) ──

  async sharedStorageProfiles(): Promise<any> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/shared-storage/profiles`, { headers: this.authHeaders });
    if (!res.ok) throw new Error(`shared storage profiles: HTTP ${res.status}`);
    return res.json();
  }

  async sharedStorageList(id: string, path = ''): Promise<any> {
    this.assertConnected();
    const p = new URLSearchParams({ id });
    if (path) p.set('path', path);
    const res = await fetch(`${this.baseUrl}/shared-storage/list?${p}`, { headers: this.authHeaders });
    if (!res.ok) throw new Error(`shared storage list: HTTP ${res.status}`);
    return res.json();
  }

  // ── Blobs (simple key-value object store on the host) ──

  async blobsListBuckets(): Promise<{ buckets: string[] }> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/blobs`, { headers: this.authHeaders });
    if (!res.ok) throw new Error(`blobs list: HTTP ${res.status}`);
    return res.json();
  }

  async blobsListKeys(
    bucket: string,
    opts: { limit?: number; after?: string } = {},
  ): Promise<{
    keys: { key: string; size?: number; contentType?: string; uploadedAt?: string }[];
    nextCursor?: string;
    total?: number;
  }> {
    this.assertConnected();
    const q = new URLSearchParams();
    if (opts.limit) q.set('limit', String(opts.limit));
    if (opts.after) q.set('after', opts.after);
    const suffix = q.toString() ? `?${q.toString()}` : '';
    const res = await fetch(
      `${this.baseUrl}/blobs/${encodeURIComponent(bucket)}${suffix}`,
      { headers: this.authHeaders },
    );
    if (!res.ok) throw new Error(`blob keys: HTTP ${res.status}`);
    const data = await res.json();
    return {
      keys: data.keys ?? data.items ?? [],
      nextCursor: data.nextCursor || undefined,
      total: data.total,
    };
  }

  async blobsDelete(bucket: string, key: string): Promise<void> {
    this.assertConnected();
    const res = await fetch(
      `${this.baseUrl}/blobs/${encodeURIComponent(bucket)}/${encodeURIComponent(key)}`,
      { method: 'DELETE', headers: this.authHeaders },
    );
    if (!res.ok) throw new Error(`blob delete: HTTP ${res.status}`);
  }

  // ── Quality Gates ──────────────────────────────────────────────────

  /** Detect available quality checks for a project. */
  async detectQualityChecks(workDir?: string): Promise<{type: string; available: boolean; command: string; framework: string}[]> {
    this.assertConnected();
    const params = workDir ? `?workDir=${encodeURIComponent(workDir)}` : '';
    const res = await fetch(`${this.baseUrl}/quality/detect${params}`, { headers: this.authHeaders });
    if (!res.ok) throw new Error(`Failed to detect quality checks: ${res.status}`);
    return res.json();
  }

  /** Run a single quality check. */
  async runQualityCheck(type: string, workDir?: string): Promise<{id: string; type: string; status: string}> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/quality/run`, {
      method: 'POST',
      headers: { ...this.authHeaders, 'Content-Type': 'application/json' },
      body: JSON.stringify({ type, workDir }),
    });
    if (!res.ok) throw new Error(`Failed to run quality check: ${res.status}`);
    return res.json();
  }

  /** Run all available quality checks. */
  async runAllQualityChecks(workDir?: string): Promise<{id: string; type: string; status: string}[]> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/quality/run-all`, {
      method: 'POST',
      headers: { ...this.authHeaders, 'Content-Type': 'application/json' },
      body: JSON.stringify({ workDir }),
    });
    if (!res.ok) throw new Error(`Failed to run quality checks: ${res.status}`);
    return res.json();
  }

  /** Get all quality check results. */
  async getQualityResults(): Promise<any[]> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/quality/results`, { headers: this.authHeaders });
    if (!res.ok) return [];
    return res.json();
  }

  /** Get a single quality check result by ID. */
  async getQualityResult(id: string): Promise<any> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/quality/results/${id}`, { headers: this.authHeaders });
    if (!res.ok) throw new Error(`Failed to get quality result: ${res.status}`);
    return res.json();
  }

  // ── Health Monitor ────────────────────────────────────────────────

  /** Get all health monitoring targets with current status. */
  async getHealthTargets(): Promise<HealthMonitorTarget[]> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/healthmon`, { headers: this.authHeaders });
    if (!res.ok) return [];
    const data = await res.json();
    return Array.isArray(data) ? data.map(normalizeHealthTarget) : [];
  }

  /** Add a new health monitoring target. */
  async addHealthTarget(url: string, label?: string, interval?: number): Promise<any> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/healthmon`, {
      method: 'POST',
      headers: { ...this.authHeaders, 'Content-Type': 'application/json' },
      body: JSON.stringify({ url, label, interval: interval || 60 }),
    });
    if (!res.ok) throw new Error(`Failed to add health target: ${res.status}`);
    return res.json();
  }

  /** Remove a health monitoring target. */
  async removeHealthTarget(id: string): Promise<void> {
    this.assertConnected();
    await fetch(`${this.baseUrl}/healthmon/${id}`, { method: 'DELETE', headers: this.authHeaders });
  }

  /** Force an immediate health check on a target. */
  async checkHealthTarget(id: string): Promise<HealthMonitorTarget> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/healthmon/${id}/check`, {
      method: 'POST',
      headers: this.authHeaders,
    });
    if (!res.ok) throw new Error(`Failed to check health target: ${res.status}`);
    return normalizeHealthTarget(await res.json());
  }

  // ── Git Operations ────────────────────────────────────────────────

  /** Get git status for a project. */
  async gitStatus(workDir?: string): Promise<{branch: string; ahead: number; behind: number; clean: boolean; staged: any[]; modified: any[]; untracked: any[]}> {
    this.assertConnected();
    const params = workDir ? `?workDir=${encodeURIComponent(workDir)}` : '';
    const res = await fetch(`${this.baseUrl}/git/status${params}`, { headers: this.authHeaders });
    if (!res.ok) throw new Error(`Failed to get git status: ${res.status}`);
    return res.json();
  }

  /** Get git log for a project. */
  async gitLog(workDir?: string, limit?: number): Promise<{hash: string; shortHash: string; message: string; author: string; date: string}[]> {
    this.assertConnected();
    const params = new URLSearchParams();
    if (workDir) params.set('workDir', workDir);
    if (limit) params.set('limit', String(limit));
    const q = params.toString() ? `?${params}` : '';
    const res = await fetch(`${this.baseUrl}/git/log${q}`, { headers: this.authHeaders });
    if (!res.ok) return [];
    return res.json();
  }

  /** Get git diff for a project. */
  async gitDiff(workDir?: string, file?: string): Promise<{diff: string}> {
    this.assertConnected();
    const params = new URLSearchParams();
    if (workDir) params.set('workDir', workDir);
    if (file) params.set('file', file);
    const q = params.toString() ? `?${params}` : '';
    const res = await fetch(`${this.baseUrl}/git/diff${q}`, { headers: this.authHeaders });
    if (!res.ok) throw new Error(`Failed to get git diff: ${res.status}`);
    return res.json();
  }

  /** List git branches for a project. */
  async gitBranches(workDir?: string): Promise<{name: string; current: boolean; remote?: string}[]> {
    this.assertConnected();
    const params = workDir ? `?workDir=${encodeURIComponent(workDir)}` : '';
    const res = await fetch(`${this.baseUrl}/git/branches${params}`, { headers: this.authHeaders });
    if (!res.ok) return [];
    return res.json();
  }

  /** Create a git commit. */
  async gitCommit(message: string, files?: string[], workDir?: string): Promise<{hash: string; message: string}> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/git/commit`, {
      method: 'POST',
      headers: { ...this.authHeaders, 'Content-Type': 'application/json' },
      body: JSON.stringify({ message, files, workDir }),
    });
    if (!res.ok) throw new Error(`Failed to commit: ${res.status}`);
    return res.json();
  }

  /** Push to remote. */
  async gitPush(workDir?: string): Promise<{success: boolean; output: string}> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/git/push`, {
      method: 'POST',
      headers: { ...this.authHeaders, 'Content-Type': 'application/json' },
      body: JSON.stringify({ workDir }),
    });
    if (!res.ok) throw new Error(`Failed to push: ${res.status}`);
    return res.json();
  }

  /** Pull from remote. */
  async gitPull(workDir?: string): Promise<{success: boolean; output: string}> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/git/pull`, {
      method: 'POST',
      headers: { ...this.authHeaders, 'Content-Type': 'application/json' },
      body: JSON.stringify({ workDir }),
    });
    if (!res.ok) throw new Error(`Failed to pull: ${res.status}`);
    return res.json();
  }

  /** Checkout a branch. */
  async gitCheckout(branch: string, workDir?: string): Promise<{success: boolean}> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/git/checkout`, {
      method: 'POST',
      headers: { ...this.authHeaders, 'Content-Type': 'application/json' },
      body: JSON.stringify({ branch, workDir }),
    });
    if (!res.ok) throw new Error(`Failed to checkout: ${res.status}`);
    return res.json();
  }

  /** Stash changes. */
  async gitStash(workDir?: string): Promise<{success: boolean; output: string}> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/git/stash`, {
      method: 'POST',
      headers: { ...this.authHeaders, 'Content-Type': 'application/json' },
      body: JSON.stringify({ workDir }),
    });
    if (!res.ok) throw new Error(`Failed to stash: ${res.status}`);
    return res.json();
  }

  /** Fetch the canonical deploy-token catalogue from the agent.
   *  Used by the mobile + web sandbox export onboarding screens to
   *  render the list of secrets the user needs in their vault. */
  async deployTokensCatalogue(): Promise<{
    targets: Array<{
      id: string;
      label: string;
      description: string;
      fields: Array<{
        name: string;
        label: string;
        hint: string;
        generateUrl: string;
        kind: 'secret' | 'json' | 'file';
        canVerify: boolean;
        pairs?: string[];
      }>;
    }>;
  }> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/deploy/tokens/catalogue`, { headers: this.authHeaders });
    if (!res.ok) throw new Error(`deployTokensCatalogue ${res.status}`);
    const data = await res.json();
    return { targets: Array.isArray(data?.targets) ? data.targets : [] };
  }

  /** Per-device, per-target deploy capability snapshot from the agent.
   *  Composes the platform lock (e.g. TestFlight is darwin-only) with
   *  RunBuildDoctor's tools+vault-secrets readiness probe — gives the
   *  UI a single yes/no per target plus a structured Reason for the
   *  greyed-out case. The mobile deploy-tokens screen + phone-project
   *  Ship picker call this on the agent the user has selected, so a
   *  Linux box can't be offered "Deploy to TestFlight" only to fail
   *  inside xcodebuild.
   *  Pass `target` to scope to one; omit for the full matrix. */
  async deployCapabilities(args?: {
    target?: string;
    project?: string;
  }): Promise<{
    deviceId: string;
    platform: string;
    arch: string;
    isWsl: boolean;
    targets: Array<{
      target: string;
      stack?: string;
      canDeploy: boolean;
      platformLock?: string;
      tools?: Array<{
        name: string;
        required: boolean;
        found: boolean;
        path?: string;
        version?: string;
        installHint?: string;
        deepValid?: boolean;
        deepError?: string;
        platformSkipped?: boolean;
        skipReason?: string;
      }>;
      secrets?: Array<{
        name: string;
        found: boolean;
        source?: string;
        project?: string;
        pathValid?: boolean;
        pathError?: string;
      }>;
      missingTools?: string[];
      missingSecrets?: string[];
      warnings?: string[];
      reason?: string;
      ciAlternative?: string;
      vaultProject?: string;
    }>;
  }> {
    this.assertConnected();
    const params = new URLSearchParams();
    if (args?.target) params.set('target', args.target);
    if (args?.project) params.set('project', args.project);
    const qs = params.toString();
    const res = await fetch(
      `${this.baseUrl}/deploy/capabilities${qs ? `?${qs}` : ''}`,
      { headers: this.authHeaders },
    );
    if (!res.ok) throw new Error(`deployCapabilities ${res.status}`);
    const data = await res.json();
    // Server emits snake_case; normalise to camel for the UI without
    // pulling in a runtime case-conversion library.
    const targets = Array.isArray(data?.targets) ? data.targets : [];
    return {
      deviceId: String(data?.device_id ?? ''),
      platform: String(data?.platform ?? ''),
      arch: String(data?.arch ?? ''),
      isWsl: !!data?.is_wsl,
      targets: targets.map((t: any) => ({
        target: String(t?.target ?? ''),
        stack: t?.stack ? String(t.stack) : undefined,
        canDeploy: !!t?.can_deploy,
        platformLock: t?.platform_lock ? String(t.platform_lock) : undefined,
        tools: Array.isArray(t?.tools)
          ? t.tools.map((tool: any) => ({
              name: String(tool?.name ?? ''),
              required: !!tool?.required,
              found: !!tool?.found,
              path: tool?.path ? String(tool.path) : undefined,
              version: tool?.version ? String(tool.version) : undefined,
              installHint: tool?.install_hint ? String(tool.install_hint) : undefined,
              deepValid: typeof tool?.deep_valid === 'boolean' ? tool.deep_valid : undefined,
              deepError: tool?.deep_error ? String(tool.deep_error) : undefined,
              platformSkipped: !!tool?.platform_skipped,
              skipReason: tool?.skip_reason ? String(tool.skip_reason) : undefined,
            }))
          : undefined,
        secrets: Array.isArray(t?.secrets)
          ? t.secrets.map((s: any) => ({
              name: String(s?.name ?? ''),
              found: !!s?.found,
              source: s?.source ? String(s.source) : undefined,
              project: s?.project ? String(s.project) : undefined,
              pathValid: typeof s?.path_valid === 'boolean' ? s.path_valid : undefined,
              pathError: s?.path_error ? String(s.path_error) : undefined,
            }))
          : undefined,
        missingTools: Array.isArray(t?.missing_tools) ? t.missing_tools.map(String) : undefined,
        missingSecrets: Array.isArray(t?.missing_secrets) ? t.missing_secrets.map(String) : undefined,
        warnings: Array.isArray(t?.warnings) ? t.warnings.map(String) : undefined,
        reason: t?.reason ? String(t.reason) : undefined,
        ciAlternative: t?.ci_alternative ? String(t.ci_alternative) : undefined,
        vaultProject: t?.vault_project ? String(t.vault_project) : undefined,
      })),
    };
  }

  /** Trigger an outbound P2P vault sync against another device the
   *  user owns. Surfaces "Try syncing from peer" buttons on the
   *  deploy-tokens / capabilities UIs without shelling out to a
   *  terminal. Pass `from` to sync against a single peer; omit to
   *  fan out across every online peer. Returns per-peer counts so
   *  the UI can show "pulled 4 secrets from your Mac mini". */
  async vaultPeerSync(args?: { from?: string }): Promise<{
    peers: string[];
    results: Array<{
      peer: string;
      pulled: number;
      supersededLocal: number;
      pushed: number;
      rejected: number;
      durationMs: number;
      error?: string;
    }>;
    totals: { pulled: number; pushed: number; rejected: number; supersededLocal: number };
    note?: string;
  }> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/vault/peer-sync`, {
      method: 'POST',
      headers: { ...this.authHeaders, 'Content-Type': 'application/json' },
      body: JSON.stringify({ from: args?.from ?? '' }),
    });
    if (!res.ok) throw new Error(`vaultPeerSync ${res.status}`);
    const data = await res.json();
    const results = Array.isArray(data?.results) ? data.results : [];
    const totals = data?.totals ?? {};
    return {
      peers: Array.isArray(data?.peers) ? data.peers.map(String) : [],
      results: results.map((r: any) => ({
        peer: String(r?.peer ?? ''),
        pulled: Number(r?.pulled ?? 0),
        supersededLocal: Number(r?.superseded_local ?? 0),
        pushed: Number(r?.pushed ?? 0),
        rejected: Number(r?.rejected ?? 0),
        durationMs: Number(r?.duration_ms ?? 0),
        error: r?.error ? String(r.error) : undefined,
      })),
      totals: {
        pulled: Number(totals?.pulled ?? 0),
        pushed: Number(totals?.pushed ?? 0),
        rejected: Number(totals?.rejected ?? 0),
        supersededLocal: Number(totals?.superseded_local ?? 0),
      },
      note: data?.note ? String(data.note) : undefined,
    };
  }

  /** Per-project status: which deploy-token fields are filled in
   *  the agent's vault. Never returns the values themselves —
   *  only `set: bool` + `updatedAt` per field. */
  async deployTokensStatus(project: string): Promise<{
    targets: Array<{
      id: string;
      label: string;
      ready: boolean;
      total: number;
      filled: number;
      fields: Array<{ name: string; set: boolean; updatedAt?: number }>;
    }>;
  }> {
    this.assertConnected();
    const res = await fetch(
      `${this.baseUrl}/deploy/tokens/status?project=${encodeURIComponent(project)}`,
      { headers: this.authHeaders },
    );
    if (!res.ok) throw new Error(`deployTokensStatus ${res.status}`);
    const data = await res.json();
    return { targets: Array.isArray(data?.targets) ? data.targets : [] };
  }

  /** Save one or many deploy-token values into the per-project
   *  vault, optionally verifying each via its provider catalogue
   *  entry. Returns per-field saved/verify status. */
  async deployTokensSave(args: {
    project: string;
    tokens: Record<string, string>;
    verifyAs?: Record<string, string>;
  }): Promise<{
    results: Record<string, { saved: boolean; reason?: string; verify?: string; verifyDetail?: string; verifyReason?: string }>;
  }> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/deploy/tokens/save`, {
      method: 'POST',
      headers: { ...this.authHeaders, 'Content-Type': 'application/json' },
      body: JSON.stringify({
        project: args.project,
        tokens: args.tokens,
        verifyAs: args.verifyAs || {},
      }),
    });
    if (!res.ok) {
      const text = await res.text().catch(() => '');
      throw new Error(`deployTokensSave ${res.status}: ${text}`);
    }
    const data = await res.json();
    return { results: data?.results || {} };
  }

  /** Create a brand-new GitHub or GitLab repo on the user's behalf
   *  using a previously-stored PAT (set via /git/provider/setup).
   *  When writeSandbox is true the agent also seeds a starter
   *  yaver.workspace.yaml that flags the repo as Yaver-mobile-
   *  sandbox-aware. Returns the clone URL the caller can record on
   *  the project. Used by the phone-projects wizard's
   *  "Configure now" git path. */
  async gitProviderRepoCreate(args: {
    provider: 'github' | 'gitlab';
    host?: string;
    name: string;
    visibility: 'private' | 'public';
    description?: string;
    writeSandbox?: boolean;
  }): Promise<{
    cloneUrl: string;
    sshUrl: string;
    fullName: string;
    sandboxWritten: boolean;
  } | null> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/git/provider/repo/create`, {
      method: 'POST',
      headers: { ...this.authHeaders, 'Content-Type': 'application/json' },
      body: JSON.stringify({
        provider: args.provider,
        host: args.host,
        name: args.name,
        visibility: args.visibility,
        description: args.description,
        writeSandbox: args.writeSandbox !== false,
      }),
    });
    // 404 = agent is older than the build that added this endpoint.
    // We return null instead of throwing so the wizard can fall
    // through to "preference recorded, configure later" without
    // failing the whole project create.
    if (res.status === 404) return null;
    if (!res.ok) {
      const text = await res.text().catch(() => '');
      throw new Error(`gitProviderRepoCreate ${res.status}: ${text}`);
    }
    const data = await res.json();
    return {
      cloneUrl: typeof data.cloneUrl === 'string' ? data.cloneUrl : '',
      sshUrl: typeof data.sshUrl === 'string' ? data.sshUrl : '',
      fullName: typeof data.fullName === 'string' ? data.fullName : '',
      sandboxWritten: !!data.sandboxWritten,
    };
  }

  /** Pop stashed changes. */
  async gitStashPop(workDir?: string): Promise<{success: boolean; output: string}> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/git/stash-pop`, {
      method: 'POST',
      headers: { ...this.authHeaders, 'Content-Type': 'application/json' },
      body: JSON.stringify({ workDir }),
    });
    if (!res.ok) throw new Error(`Failed to pop stash: ${res.status}`);
    return res.json();
  }

  /** Revert a commit by hash. */
  async gitRevert(hash: string, workDir?: string): Promise<{success: boolean; output: string}> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/git/revert`, {
      method: 'POST',
      headers: { ...this.authHeaders, 'Content-Type': 'application/json' },
      body: JSON.stringify({ hash, workDir }),
    });
    if (!res.ok) throw new Error(`Failed to revert: ${res.status}`);
    return res.json();
  }

  // ── Repo Sync ─────────────────────────────────────────────────────

  /** Clone a repo to the dev machine. */
  async cloneRepo(url: string, dir?: string, branch?: string): Promise<{ok: boolean; path: string; output: string}> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/repos/clone`, {
      method: 'POST',
      headers: { ...this.authHeaders, 'Content-Type': 'application/json' },
      body: JSON.stringify({ url, dir, branch }),
    });
    if (!res.ok) {
      const err = await res.json().catch(() => ({ error: `HTTP ${res.status}` }));
      throw new Error(err.error || `Clone failed: ${res.status}`);
    }
    return res.json();
  }

  /** Pull latest in a repo directory. */
  async pullRepo(workDir?: string): Promise<{ok: boolean; output: string; branch: string}> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/repos/pull`, {
      method: 'POST',
      headers: { ...this.authHeaders, 'Content-Type': 'application/json' },
      body: JSON.stringify({ workDir }),
    });
    if (!res.ok) {
      const err = await res.json().catch(() => ({ error: `HTTP ${res.status}` }));
      throw new Error(err.error || `Pull failed: ${res.status}`);
    }
    return res.json();
  }

  /** Delete a repo directory from the remote machine. This removes source code from that box. */
  async deleteRepo(path: string): Promise<{ok: boolean; path: string}> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/repos/delete`, {
      method: 'POST',
      headers: { ...this.authHeaders, 'Content-Type': 'application/json' },
      body: JSON.stringify({ path }),
    });
    if (!res.ok) {
      const err = await res.json().catch(() => ({ error: `HTTP ${res.status}` }));
      throw new Error(err.error || `Delete failed: ${res.status}`);
    }
    return res.json();
  }

  /** List repos on dev machine. */
  async listRepos(): Promise<{name: string; path: string; branch: string; remote: string; lastCommit: string; dirty: boolean}[]> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/repos/list`, { headers: this.authHeaders });
    if (!res.ok) throw new Error(`Failed to list repos: ${res.status}`);
    return res.json();
  }

  /** Store git credential (PAT) on the dev machine. */
  async setRepoCredential(host: string, token: string, username?: string): Promise<{ok: boolean}> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/repos/credentials`, {
      method: 'POST',
      headers: { ...this.authHeaders, 'Content-Type': 'application/json' },
      body: JSON.stringify({ host, token, username }),
    });
    if (!res.ok) throw new Error(`Failed to set credential: ${res.status}`);
    return res.json();
  }

  /** List configured credential hosts (tokens are never returned). */
  async listRepoCredentials(): Promise<{host: string; username: string; hasToken: boolean}[]> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/repos/credentials`, { headers: this.authHeaders });
    if (!res.ok) throw new Error(`Failed to list credentials: ${res.status}`);
    return res.json();
  }

  /** Remove a credential for a host. */
  async removeRepoCredential(host: string): Promise<void> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/repos/credentials/${encodeURIComponent(host)}`, {
      method: 'DELETE',
      headers: this.authHeaders,
    });
    if (!res.ok) throw new Error(`Failed to remove credential: ${res.status}`);
  }

  /** Scaffold a starter monorepo workspace manifest from the current repo. */
  async workspaceScaffold(root?: string): Promise<{ yaml: string; detected: any[]; hint?: string } | null> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/ops`, {
      method: "POST",
      headers: { ...this.authHeaders, "Content-Type": "application/json" },
      body: JSON.stringify({ verb: "workspace", machine: "local", payload: { op: "scaffold", root } }),
    });
    if (!res.ok) throw new Error(`Failed to scaffold workspace: ${res.status}`);
    const data = await res.json();
    if (!data.ok) throw new Error(data.error || "workspace scaffold failed");
    return data.initial ?? null;
  }

  /** Run the workspace init engine against a repo's yaver.workspace.yaml. */
  async workspaceInit(opts: { root?: string; dryRun?: boolean; force?: boolean; onlyApp?: string } = {}): Promise<{ counts?: Record<string, number>; actions?: any[] } | null> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/ops`, {
      method: "POST",
      headers: { ...this.authHeaders, "Content-Type": "application/json" },
      body: JSON.stringify({
        verb: "workspace",
        machine: "local",
        payload: {
          op: "init",
          root: opts.root,
          dryRun: !!opts.dryRun,
          force: !!opts.force,
          onlyApp: opts.onlyApp,
        },
      }),
    });
    if (!res.ok) throw new Error(`Failed to init workspace: ${res.status}`);
    const data = await res.json();
    if (!data.ok) throw new Error(data.error || "workspace init failed");
    return data.initial ?? null;
  }

  /** Read workspace status for a repo with yaver.workspace.yaml. */
  async workspaceStatus(root?: string): Promise<{ name?: string; status?: any[] } | null> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/ops`, {
      method: "POST",
      headers: { ...this.authHeaders, "Content-Type": "application/json" },
      body: JSON.stringify({ verb: "workspace", machine: "local", payload: { op: "status", root } }),
    });
    if (!res.ok) throw new Error(`Failed to fetch workspace status: ${res.status}`);
    const data = await res.json();
    if (!data.ok) throw new Error(data.error || "workspace status failed");
    return data.initial ?? null;
  }

  // ── EventEmitter ───────────────────────────────────────────────────

  /** Register a listener for output lines. Returns an unsubscribe function. */
  on(event: "output", callback: OutputCallback): () => void;
  /** Register a listener for connection state changes. */
  on(event: "connectionState", callback: ConnectionStateCallback): () => void;
  /** Register a listener for connection mode changes (direct vs relay). */
  on(event: "connectionMode", callback: ConnectionModeCallback): () => void;
  /** Register a listener for reconnect attempt counter changes. */
  on(event: "reconnectAttempt", callback: ReconnectAttemptCallback): () => void;
  on<E extends EventName>(event: E, callback: EventMap[E]): () => void {
    (this.listeners[event] as Array<EventMap[E]>).push(callback);
    return () => {
      const arr = this.listeners[event] as Array<EventMap[E]>;
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      (this.listeners as any)[event] = arr.filter((cb) => cb !== callback);
    };
  }

  /**
   * Legacy helper — identical to `on("output", callback)`.
   * Kept for backward compatibility with existing code.
   */
  onOutput(callback: OutputCallback): () => void {
    return this.on("output", callback);
  }

  /** Public wrapper around the internal auth-header builder. Used by
   *  free helpers (phone backend, etc.) that need the same bearer +
   *  relay-password combo a regular task call uses but live outside
   *  the class. */
  publicAuthHeaders(): Record<string, string> {
    return this.authHeaders;
  }

  // ── TTS task mode ──────────────────────────────────────────────────

  /** Set the "run tasks in TTS mode" preference and mirror it to storage. */
  setTtsTaskMode(on: boolean): void {
    this._ttsTaskMode = !!on;
    AsyncStorage.setItem(TTS_TASK_MODE_KEY, on ? "1" : "0").catch(() => {});
  }

  /** Load the persisted TTS-task-mode flag (called on connect so it applies
   *  even before the Settings screen is ever opened). */
  async hydrateTtsTaskMode(): Promise<void> {
    try {
      const v = await AsyncStorage.getItem(TTS_TASK_MODE_KEY);
      if (v !== null) this._ttsTaskMode = v === "1";
    } catch {
      // best-effort; default stays false
    }
  }

  /** Merge the client-level ttsMode flag into a per-call speechContext.
   *  Returns undefined only when there's nothing to send. */
  private withTtsMode(sc?: SpeechContextInput): SpeechContextInput | undefined {
    if (!this._ttsTaskMode) return sc;
    return { ...(sc || {}), ttsMode: true };
  }

  // ── Private helpers ────────────────────────────────────────────────

  private get authHeaders(): Record<string, string> {
    const headers: Record<string, string> = {
      Authorization: `Bearer ${this.token}`,
      'X-Client-Platform': Platform.OS, // 'ios' or 'android'
    };
    if (this.activeRelayUrl && this.activeRelayPassword) {
      headers['X-Relay-Password'] = this.activeRelayPassword;
    }
    if (this._tunnelUrl && this._tunnelHeaders) {
      Object.assign(headers, this._tunnelHeaders);
    }
    return headers;
  }

  /** True when we have enough info to attempt API calls (even during reconnection). */
  private get hasConnectionInfo(): boolean {
    return !!(this.host && this.port && this.token);
  }

  private assertConnected(): void {
    if (!this.isConnected && !this.hasConnectionInfo) {
      throw new Error("QuicClient is not connected. Call connect() first.");
    }
  }

  private setConnectionState(state: ConnectionState): void {
    if (this._connectionState === state) return;
    this._connectionState = state;
    for (const cb of this.listeners.connectionState) {
      try {
        cb(state);
      } catch {
        // Listener errors should not break the client.
      }
    }
  }

  private setConnectionMode(mode: ConnectionMode): void {
    if (this._connectionMode === mode) return;
    this._connectionMode = mode;
    for (const cb of this.listeners.connectionMode) {
      try {
        cb(mode);
      } catch {
        // Listener errors should not break the client.
      }
    }
  }

  private setReconnectAttempt(n: number): void {
    if (this._reconnectAttempt === n) return;
    this._reconnectAttempt = n;
    for (const cb of this.listeners.reconnectAttempt) {
      try {
        cb(n);
      } catch {
        // Listener errors should not break the client.
      }
    }
  }

  /**
   * User-initiated: stop the reconnection loop. The client stays in "error"
   * state until the user explicitly triggers a new connect.
   */
  stopReconnect(): void {
    if (this.reconnectTimer) {
      clearTimeout(this.reconnectTimer);
      this.reconnectTimer = null;
    }
    this._reconnectStopped = true;
    this.setConnectionState("error");
  }

  /**
   * Called by the host React tree on AppState transitions. Going to
   * background cancels the pending backoff timer so we don't wake the
   * radio while the app is suspended; returning to foreground resumes
   * the loop from attempt 1 if we were mid-reconnect or in an error
   * state. Idempotent.
   */
  setForegroundState(isForeground: boolean): void {
    if (this._isForeground === isForeground) return;
    this._isForeground = isForeground;
    if (!isForeground) {
      // Backgrounded — cancel the pending backoff. Do NOT mark as
      // stopped (that's the user-initiated signal) so we resume
      // automatically on foreground.
      if (this.reconnectTimer) {
        clearTimeout(this.reconnectTimer);
        this.reconnectTimer = null;
      }
      return;
    }
    // Foregrounded — if we need to reconnect, do it now with a fresh
    // attempt budget. DeviceContext's AppState handler also calls
    // triggerReconnect separately; these are idempotent.
    if (!this.host || !this.port || !this.token) return;
    if (this._reconnectStopped) return;
    if (this._connectionState === "connected") return;
    if (this._connectingInProgress) return;
    this.setReconnectAttempt(1);
    this.attemptConnect().catch(() => {});
  }

  private emit(event: "output", taskId: string, line: string): void {
    for (const cb of this.listeners.output) {
      try {
        cb(taskId, line);
      } catch {
        // Listener errors should not break the client.
      }
    }
  }

  private clearTimers(): void {
    if (this.pollInterval) {
      clearInterval(this.pollInterval);
      this.pollInterval = null;
    }
    if (this.heartbeatInterval) {
      clearInterval(this.heartbeatInterval);
      this.heartbeatInterval = null;
    }
    if (this.reconnectTimer) {
      clearTimeout(this.reconnectTimer);
      this.reconnectTimer = null;
    }
  }

  /**
   * Full reconnect: clears stale relay state, resets attempts, and re-probes
   * all relay paths from scratch. Use this when the network path has changed
   * (e.g. WiFi → cellular) and the current activeRelayUrl is likely stale.
   */
  fullReconnect(): void {
    if (!this.host || !this.port || !this.token) return;
    console.log("[QUIC] Full reconnect — clearing stale relay and re-probing all paths");
    this.clearTimers();
    this.activeRelayUrl = null;
    this.activeRelayPassword = null;
    this._tunnelUrl = null;
    this._tunnelHeaders = {};
    this._reconnectStopped = false;
    this.setReconnectAttempt(1);
    this.attemptConnect().catch(() => {});
  }

  // ── Connection + reconnection ──────────────────────────────────────

  /** Create a fetch with a manual timeout (AbortSignal.timeout may not exist in Hermes). */
  private fetchWithTimeout(url: string, opts: RequestInit, timeoutMs: number): Promise<Response> {
    const controller = new AbortController();
    const timer = setTimeout(() => controller.abort(), timeoutMs);
    return fetch(url, { ...opts, signal: controller.signal }).finally(() => clearTimeout(timer));
  }

  /** Check if an IP address is direct-routable without the relay:
   * RFC1918 private LAN/VPN ranges plus Tailscale/headscale CGNAT. */
  private isPrivateIP(host: string): boolean {
    return /^(192\.168\.|10\.|172\.(1[6-9]|2\d|3[01])\.)/.test(host) || this.isTailscaleIP(host);
  }

  /** Tailscale CGNAT range (100.64.0.0/10). Only relevant when both ends are
   *  on the same tailnet — but the cost of probing it is bounded by the
   *  parallel race budget, so we let it ride. */
  private isTailscaleIP(host: string): boolean {
    if (!/^100\./.test(host)) return false;
    const second = parseInt(host.split(".")[1] ?? "0", 10);
    return second >= 64 && second <= 127;
  }

  /** Race every direct-connection candidate in parallel. Resolves with the
   *  first /health 200 within the per-probe budget, null if none answer.
   *  Cancels losers via AbortController so we never leak sockets.
   *
   *  Order of candidates is informational only — they all fire at once. We
   *  surface the matched path label (lan-beacon / lan-tailscale / lan-convex-ip)
   *  in the connection log so the user sees how the session attached.
   */
  private async raceDirectCandidates(opts: { includeStoredHost?: boolean } = {}): Promise<{
    ip: string;
    port: number;
    path: ConnectionPath;
    authExpired: boolean;
  } | null> {
    type Candidate = { ip: string; port: number; path: ConnectionPath };
    const seen = new Set<string>();
    const candidates: Candidate[] = [];
    const push = (ip: string, port: number, path: ConnectionPath) => {
      if (!ip || !port) return;
      const key = `${ip}:${port}`;
      if (seen.has(key)) return;
      seen.add(key);
      candidates.push({ ip, port, path });
    };

    // Beacon first — freshest signal, tells us the agent is on this LAN now.
    const lanInfo = this.deviceId ? beaconListener.getLocalIP(this.deviceId) : null;
    if (lanInfo) push(lanInfo.ip, lanInfo.port, "lan-beacon");

    // Heartbeat-advertised IPs from Convex. Port is whatever the agent is
    // listening on (same port for every interface — single HTTP server).
    const port = this.port ?? 18080;
    for (const ip of this._lanIps) {
      // Tag Tailscale IPs distinctly so the log shows which path actually won.
      const path: ConnectionPath = this.isTailscaleIP(ip) ? "lan-tailscale" : "lan-heartbeat";
      push(ip, port, path);
    }

    // Convex-stored primary IP last — kept for backwards-compat with agents
    // that haven't upgraded to localIps yet. May be stale.
    if (opts.includeStoredHost !== false && this.host && this.isPrivateIP(this.host) && this.port) {
      push(this.host, this.port, "lan-convex-ip");
    }

    if (candidates.length === 0) return null;
    console.log(`[QUIC] Racing ${candidates.length} direct candidate(s):`,
      candidates.map((c) => `${c.path}=${c.ip}:${c.port}`).join(", "));

    const controllers: AbortController[] = [];
    const probe = (cand: Candidate, idx: number): Promise<{
      ip: string; port: number; path: ConnectionPath; authExpired: boolean;
    }> => {
      const ctrl = new AbortController();
      controllers[idx] = ctrl;
      const url = `http://${cand.ip}:${cand.port}/health`;
      const timer = setTimeout(() => ctrl.abort(), 2500);
      return fetch(url, { headers: this.authHeaders, signal: ctrl.signal })
        .then(async (res) => {
          clearTimeout(timer);
          if (!res.ok) throw new Error(`status ${res.status}`);
          const body = await res.json().catch(() => ({}));
          return { ip: cand.ip, port: cand.port, path: cand.path, authExpired: !!body.authExpired };
        })
        .catch((e) => {
          clearTimeout(timer);
          throw e;
        });
    };

    try {
      const winner = await Promise.any(candidates.map((c, i) => probe(c, i)));
      // Cancel every other in-flight probe — they're losers now.
      for (let i = 0; i < controllers.length; i++) {
        const c = controllers[i];
        if (!c) continue;
        if (winner.ip !== candidates[i].ip || winner.port !== candidates[i].port) {
          try { c.abort(); } catch {}
        }
      }
      return winner;
    } catch {
      // All failed — Promise.any rejects with AggregateError; the relay
      // path will be tried next by the caller.
      return null;
    }
  }

  private async attemptConnect(): Promise<void> {
    // Prevent concurrent connection attempts (poll failure + NetInfo can race)
    if (this._connectingInProgress) {
      console.log("[QUIC] attemptConnect already in progress, skipping");
      return;
    }
    this._connectingInProgress = true;
    try {
      await this._doAttemptConnect();
    } finally {
      this._connectingInProgress = false;
    }
  }

  private async _doAttemptConnect(): Promise<void> {
    this.setConnectionState("connecting");
    this._lastTransportError = null;
    this.activeRelayUrl = null;
    this.activeRelayPassword = null;
    this._tunnelUrl = null;
    this._tunnelHeaders = {};
    this.setConnectionMode(null);
    this._connectionPath = null;
    try {
      let connected = false;

      // Check if we're on WiFi (direct connection possible) or cellular (relay only)
      const netState = await NetInfo.fetch();
      const isWifi = netState.type === "wifi" || netState.type === "ethernet";
      this._networkType = netState.type;

      // Strategy: direct-first on WiFi (lowest latency), relay-fallback.
      // On cellular, still probe heartbeat-advertised localIps: VPN
      // routes (Tailscale/headscale/WireGuard) can be reachable even
      // though NetInfo reports the underlying carrier network.

      // 0. Cached fast-path — if a previous session for this device
      //    finished a successful connect, we have its exact transport
      //    coordinates persisted (host:port for direct, full URL +
      //    headers for tunnel/relay). Probe that endpoint with a tight
      //    2s timeout. If it answers /health, we're done in one round-
      //    trip without waiting for the candidate race. If it doesn't,
      //    fall through to the normal flow — the cached path may have
      //    rotated. Direct-mode entries are still probed with a tight
      //    timeout on cellular because VPN/tailnet addresses often look
      //    like plain private IPs but route perfectly over the cellular
      //    underlay.
      if (this.deviceId) {
        const cached = await loadConnectionCache(this.deviceId);
        if (cached && this.cachedPathStillUsable(cached, isWifi)) {
          const winner = await this.probeCachedPath(cached);
          if (winner) {
            this.applyCachedPath(cached, winner.authExpired);
            connected = true;
            console.log(`[QUIC] Cached fast-path (${cached.mode}) — skipped candidate race`);
          }
        }
      }

      // 1. Try direct connection first — race every direct candidate IP in
      //    parallel. Candidates: LAN beacon (freshest), heartbeat-advertised
      //    localIps (Wi-Fi + Tailscale + Ethernet), then the Convex-stored
      //    primary host. Whoever returns 200 from /health first wins; the
      //    others are abandoned. This collapses the old serial 2s+2s
      //    waterfall into a single ~2s window and survives stale Convex IPs
      //    because the freshest signal wins, not the first-tried one.
      if (!connected && !this._forceRelay && (isWifi || this._lanIps.length > 0)) {
        const winner = await this.raceDirectCandidates({ includeStoredHost: isWifi });
        if (winner) {
          this.host = winner.ip;
          this.port = winner.port;
          this.activeRelayUrl = null;
          this.agentAuthExpired = winner.authExpired;
          this.setConnectionMode("direct");
          this._connectionPath = winner.path;
          connected = true;
          console.log(`[QUIC] Direct via ${winner.path}: ${winner.ip}:${winner.port}`);
        }
      }

      // 2. Try Cloudflare Tunnels (works through any firewall)
      const tunnels = this.effectiveTunnelServers;
      if (!connected && tunnels.length > 0) {
        console.log("[QUIC] Trying", tunnels.length, "Cloudflare Tunnel(s)");
        for (const tunnel of tunnels) {
          try {
            console.log("[QUIC] Trying tunnel:", tunnel.label || tunnel.url);
            const probeHeaders: Record<string, string> = { ...this.authHeaders };
            if (tunnel.cfAccessClientId) {
              probeHeaders['CF-Access-Client-Id'] = tunnel.cfAccessClientId;
              probeHeaders['CF-Access-Client-Secret'] = tunnel.cfAccessClientSecret || '';
            }
            const res = await this.fetchWithTimeout(`${tunnel.url}/health`, {
              headers: probeHeaders,
            }, 8000);
            if (res.ok) {
              const healthData = await res.json().catch(() => ({}));
              this.agentAuthExpired = !!healthData.authExpired;
              // Tunnel works like a direct connection — no relay proxy path needed
              this.activeRelayUrl = null;
              this.activeRelayPassword = null;
              // Override host/port to use tunnel URL for subsequent requests
              this._tunnelUrl = tunnel.url;
              this._tunnelHeaders = {};
              if (tunnel.cfAccessClientId) {
                this._tunnelHeaders['CF-Access-Client-Id'] = tunnel.cfAccessClientId;
                this._tunnelHeaders['CF-Access-Client-Secret'] = tunnel.cfAccessClientSecret || '';
              }
              this.setConnectionMode("tunnel");
              this._connectionPath = "cloudflare-tunnel";
              connected = true;
              console.log("[QUIC] Cloudflare Tunnel connection succeeded:", tunnel.label || tunnel.url);
              break;
            }
            this._lastTransportError = await responseErrorMessage(res, `Tunnel ${tunnel.label || tunnel.url} returned HTTP ${res.status}`);
          } catch (e) {
            console.log("[QUIC] Tunnel", tunnel.label || tunnel.url, "failed:", e);
          }
        }
      }

      // 3. Try relay servers (fallback for cellular, or when direct failed)
      if (!connected && this.deviceId && this.relayServers.length > 0) {
        console.log("[QUIC] Trying", this.relayServers.length, "relay server(s)");
        for (const relay of this.relayServers) {
          try {
            const relayDeviceUrl = `${relay.httpUrl}/d/${this.deviceId}`;
            console.log("[QUIC] Trying relay:", relay.id, relayDeviceUrl);
            const probeHeaders: Record<string, string> = { Authorization: `Bearer ${this.token}` };
            if (relay.password) {
              probeHeaders['X-Relay-Password'] = relay.password;
            }
            const res = await this.fetchWithTimeout(`${relayDeviceUrl}/health`, {
              headers: probeHeaders,
            }, 8000);
            if (res.ok) {
              const healthData = await res.json().catch(() => ({}));
              this.agentAuthExpired = !!healthData.authExpired;
              this.activeRelayUrl = relay.httpUrl;
              this.activeRelayPassword = relay.password || null;
              this.setConnectionMode("relay");
              this._connectionPath = "relay";
              connected = true;
              console.log("[QUIC] Relay connection succeeded via", relay.id);
              break;
            }
            this._lastTransportError = await responseErrorMessage(res, `Relay ${relay.id} returned HTTP ${res.status}`);
          } catch (e) {
            console.log("[QUIC] Relay", relay.id, "failed:", e);
          }
        }
      }

      if (!connected) {
        throw new Error(this._lastTransportError || "Could not reach agent (direct or via relay)");
      }

      this.setReconnectAttempt(0);
      this.setConnectionState("connected");
      this._hadSuccessfulConnect = true;
      this.startPolling();
      // Persist the exact coordinates that worked so the next session can
      // try them first instead of waiting through the candidate race
      // again. Best-effort — losing the cache is fine, it just costs us
      // one extra round-trip on the following connect.
      void this.persistConnectionSnapshot();
      // Best-effort vault sync on connect
      this.syncVault();
    } catch {
      this.setConnectionState("error");
      this.scheduleReconnect();
    }
  }

  /** True when the cached path could plausibly route from our current
   *  network. */
  private cachedPathStillUsable(cached: ConnectionCacheEntry, isWifi: boolean): boolean {
    if (cached.mode === "direct") {
      // Direct does not mean "same Wi-Fi" anymore. A successful direct
      // cache entry may be a Tailscale/headscale/WireGuard address that
      // routes over cellular. Keep the probe budget tight instead of
      // suppressing the only working VPN path.
      return isWifi || !!cached.host;
    }
    return true;
  }

  /** Probes the cached endpoint with /health. Returns null on timeout
   *  or non-200. authExpired surfaces the agent's "I have a token but
   *  Convex rejected it" signal so the UI can prompt re-auth without
   *  treating the connection as dead. */
  private async probeCachedPath(
    cached: ConnectionCacheEntry
  ): Promise<{ authExpired: boolean } | null> {
    const base = probeBaseFor(cached);
    if (!base) return null;
    const headers: Record<string, string> = {
      ...probeHeadersFor(cached),
      ...this.authHeaders,
    };
    try {
      const res = await this.fetchWithTimeout(`${base}/health`, { headers }, 2500);
      if (!res.ok) return null;
      const body = await res.json().catch(() => ({} as { authExpired?: boolean }));
      return { authExpired: !!body.authExpired };
    } catch {
      return null;
    }
  }

  /** Mirror the post-success state setters from the candidate race so
   *  baseUrl + connection mode reflect the cached path. Keeps the rest
   *  of the client (poller, exec, etc.) oblivious to the fast-path. */
  private applyCachedPath(cached: ConnectionCacheEntry, authExpired: boolean): void {
    this.agentAuthExpired = authExpired;
    if (cached.mode === "direct" && cached.host && cached.port) {
      this.host = cached.host;
      this.port = cached.port;
      this.activeRelayUrl = null;
      this.activeRelayPassword = null;
      this._tunnelUrl = null;
      this._tunnelHeaders = {};
      this.setConnectionMode("direct");
      this._connectionPath = "lan-cached";
      return;
    }
    if (cached.mode === "tunnel" && cached.tunnelUrl) {
      this.activeRelayUrl = null;
      this.activeRelayPassword = null;
      this._tunnelUrl = cached.tunnelUrl;
      this._tunnelHeaders = cached.tunnelHeaders ? { ...cached.tunnelHeaders } : {};
      this.setConnectionMode("tunnel");
      this._connectionPath = "cloudflare-tunnel";
      return;
    }
    if (cached.mode === "relay" && cached.relayUrl) {
      this.activeRelayUrl = cached.relayUrl;
      this.activeRelayPassword = cached.relayPassword || null;
      this._tunnelUrl = null;
      this._tunnelHeaders = {};
      this.setConnectionMode("relay");
      this._connectionPath = "relay";
    }
  }

  /** Captures the just-succeeded transport into AsyncStorage. Mode is
   *  derived from which of {tunnelUrl, activeRelayUrl, host:port} is
   *  populated — same precedence as `get baseUrl()`. */
  private async persistConnectionSnapshot(): Promise<void> {
    if (!this.deviceId) return;
    let mode: CacheConnectionMode;
    let entry: ConnectionCacheEntry;
    if (this._tunnelUrl) {
      mode = "tunnel";
      entry = {
        v: 1,
        deviceId: this.deviceId,
        mode,
        tunnelUrl: this._tunnelUrl,
        tunnelHeaders: { ...this._tunnelHeaders },
        ts: Date.now(),
        hadSuccess: true,
      };
    } else if (this.activeRelayUrl) {
      mode = "relay";
      entry = {
        v: 1,
        deviceId: this.deviceId,
        mode,
        relayUrl: this.activeRelayUrl,
        relayPassword: this.activeRelayPassword || undefined,
        ts: Date.now(),
        hadSuccess: true,
      };
    } else if (this.host && this.port) {
      mode = "direct";
      entry = {
        v: 1,
        deviceId: this.deviceId,
        mode,
        host: this.host,
        port: this.port,
        ts: Date.now(),
        hadSuccess: true,
      };
    } else {
      return;
    }
    await persistConnectionCache(entry);
  }

  /**
   * Force an immediate reconnection attempt (e.g. on network change).
   * Resets backoff so the first retry is instant.
   */
  triggerReconnect(): void {
    if (!this.host || !this.port || !this.token) return;
    // Already connected — nothing to do
    if (this._connectionState === "connected") {
      // Still worth re-probing: the current path may be dead after a network switch.
      // Clear polling so attemptConnect can restart it on the new path.
      this.clearTimers();
      this._reconnectStopped = false;
      this.setReconnectAttempt(1);
      this.attemptConnect().catch(() => {});
      return;
    }
    // Cancel any pending backoff timer and reconnect immediately
    if (this.reconnectTimer) {
      clearTimeout(this.reconnectTimer);
      this.reconnectTimer = null;
    }
    this._reconnectStopped = false;
    this.setReconnectAttempt(1);
    this.attemptConnect().catch(() => {});
  }

  private scheduleReconnect(): void {
    if (!this.host || !this.port || !this.token) return;
    if (this._reconnectStopped) return;

    // Paused while the app is in the background — setForegroundState(true)
    // will resume from attempt 1 when we come back. The current attempt
    // counter is preserved in case foreground returns quickly and we want
    // to pick up where we left off.
    if (!this._isForeground) {
      if (this.reconnectTimer) {
        clearTimeout(this.reconnectTimer);
        this.reconnectTimer = null;
      }
      return;
    }

    // Give-up policy:
    //   never-connected device → stop after _maxReconnectAttempts so a
    //     wrong-host typo doesn't drain battery probing nothing
    //   previously-connected device (this session OR persisted from a
    //     prior session) → keep retrying forever with capped backoff,
    //     because "stuck CONNECTING with no further attempts" is the
    //     worst UX for a remote primary the user only reaches from
    //     their phone. The agent is almost certainly transient-down.
    if (this._reconnectAttempt >= this._maxReconnectAttempts) {
      if (!this._hadSuccessfulConnect) {
        console.log("[QUIC] Max reconnect attempts reached for never-connected device, giving up");
        this.setConnectionState("error");
        return;
      }
      console.log(`[QUIC] Reconnect attempt ${this._reconnectAttempt} for previously-reachable device — keeping at it`);
    }

    // Exponential backoff indexed by the attempt that just failed (1, 2, 4, 8… capped),
    // plus 0-500ms jitter to avoid thundering-herd reconnects when the relay flaps.
    const baseDelay = Math.min(
      this.baseBackoffMs * Math.pow(2, this._reconnectAttempt - 1),
      30_000
    );
    const delay = baseDelay + Math.floor(Math.random() * 500);

    this.reconnectTimer = setTimeout(() => {
      this.reconnectTimer = null;
      if (this._reconnectStopped) return;
      this.setReconnectAttempt(this._reconnectAttempt + 1);
      this.attemptConnect().catch(() => {
        // Reconnection failure is handled inside attemptConnect.
      });
    }, delay);
  }

  /**
   * Poll the agent's task list for status updates.
   * This is a temporary mechanism; the real QUIC transport will push
   * output over a dedicated unidirectional stream.
   */
  private startPolling(): void {
    if (this.pollInterval) return;
    // Track last known output lengths to detect new output
    const lastOutputLen = new Map<string, number>();

    this.pollInterval = setInterval(async () => {
      try {
        const res = await fetch(`${this.baseUrl}/tasks`, {
          headers: this.authHeaders,
        });
        if (!res.ok) {
          console.log("[QUIC] Poll /tasks failed:", res.status);
          return;
        }
        const data = await res.json();
        const rawTasks = data.tasks || [];
        for (const t of rawTasks) {
          if (t.status !== "running" && t.status !== "completed") continue;
          const output = typeof t.output === "string" ? t.output : "";
          const prevLen = lastOutputLen.get(t.id) || 0;
          if (output.length > prevLen) {
            const newText = output.slice(prevLen);
            const lines = newText.split("\n").filter((l: string) => l);
            console.log(`[QUIC] Poll: task ${t.id} has ${lines.length} new line(s), total=${output.length}`);
            for (const line of lines) {
              this.emit("output", t.id, line);
            }
            lastOutputLen.set(t.id, output.length);
            cacheTaskOutput(t.id, lines);
          }
        }
      } catch {
        // Poll failure is handled by the heartbeat — don't reconnect from here
        console.log("[QUIC] Poll /tasks failed — heartbeat will handle reconnection");
      }
    }, 3000);

    // Start heartbeat: pings /health every 15s to detect data path failure
    this.startHeartbeat();
  }

  /**
   * Heartbeat: pings the agent's /health endpoint every 15s.
   * On 2 consecutive failures:
   * - If on direct connection and relay servers are available, try relay fallback
   * - Otherwise trigger full reconnect
   */
  private startHeartbeat(): void {
    if (this.heartbeatInterval) return;
    this._consecutiveHeartbeatFailures = 0;

    this.heartbeatInterval = setInterval(async () => {
      // Upgrade check: if on relay/tunnel but beacon discovered device on LAN, try switching to direct
      if (this._connectionMode !== "direct" && this.deviceId) {
        const lanInfo = beaconListener.getLocalIP(this.deviceId);
        if (lanInfo) {
          try {
            const directUrl = `http://${lanInfo.ip}:${lanInfo.port}`;
            console.log("[QUIC] Beacon found device on LAN — trying upgrade to direct:", directUrl);
            const probeRes = await this.fetchWithTimeout(`${directUrl}/health`, {
              headers: this.authHeaders,
            }, 2000);
            if (probeRes.ok) {
              // Switch to direct — update host/port so baseUrl getter returns the LAN address
              this.host = lanInfo.ip;
              this.port = lanInfo.port;
              this.activeRelayUrl = null;
              this.activeRelayPassword = null;
              this._tunnelUrl = null;
              this._tunnelHeaders = {};
              this.setConnectionMode("direct");
              this._connectionPath = "lan-beacon-upgrade";
              this._consecutiveHeartbeatFailures = 0;
              console.log("[QUIC] Upgraded to direct connection via LAN beacon");
              return;
            }
          } catch {
            // Direct probe failed — stay on current path
          }
        }
      }

      try {
        const res = await this.fetchWithTimeout(`${this.baseUrl}/health`, {
          headers: this.authHeaders,
        }, 10000);
        if (res.ok) {
          this._consecutiveHeartbeatFailures = 0;
          return;
        }
        this._consecutiveHeartbeatFailures++;
      } catch {
        this._consecutiveHeartbeatFailures++;
      }

      console.log(`[QUIC] Heartbeat failed (${this._consecutiveHeartbeatFailures} consecutive)`);

      if (this._consecutiveHeartbeatFailures >= 2) {
        // Data path is broken — try relay fallback if on direct and relays exist
        if (this._connectionMode === "direct" && this.relayServers.length > 0) {
          console.log("[QUIC] Direct path down — attempting relay fallback...");
          const switched = await this.tryRelayFallback();
          if (switched) {
            this._consecutiveHeartbeatFailures = 0;
            return;
          }
        }
        // No relay available or relay also failed — full reconnect
        this.setConnectionState("error");
        this.fullReconnect();
      }
    }, 15000);

    // Also check relay health periodically (every 60s)
    this.checkRelayHealth();
  }

  /**
   * Try to switch from current (broken) path to a relay server.
   * Returns true if successfully switched.
   */
  private async tryRelayFallback(): Promise<boolean> {
    for (const relay of this.relayServers) {
      try {
        const relayDeviceUrl = `${relay.httpUrl}/d/${this.deviceId}`;
        const probeHeaders: Record<string, string> = { Authorization: `Bearer ${this.token}` };
        if (relay.password) {
          probeHeaders['X-Relay-Password'] = relay.password;
        }
        const res = await this.fetchWithTimeout(`${relayDeviceUrl}/health`, {
          headers: probeHeaders,
        }, 8000);
        if (res.ok) {
          this.activeRelayUrl = relay.httpUrl;
          this.activeRelayPassword = relay.password || null;
          this.setConnectionMode("relay");
          this._connectionPath = "relay";
          this.setConnectionState("connected");
          console.log("[QUIC] Relay fallback succeeded via", relay.id);
          return true;
        }
        this._lastTransportError = await responseErrorMessage(res, `Relay ${relay.id} returned HTTP ${res.status}`);
      } catch (e) {
        console.log("[QUIC] Relay fallback", relay.id, "failed:", e);
      }
    }
    return false;
  }

  /** Ping each relay server's /health to track availability. */
  private async checkRelayHealth(): Promise<void> {
    const client = { timeout: 8000 };
    for (const relay of this.relayServers) {
      try {
        const start = Date.now();
        const res = await this.fetchWithTimeout(`${relay.httpUrl}/health`, {}, client.timeout);
        const latencyMs = Date.now() - start;
        this._relayHealth.set(relay.httpUrl, {
          ok: res.ok,
          latencyMs,
          lastChecked: Date.now(),
        });
      } catch {
        this._relayHealth.set(relay.httpUrl, {
          ok: false,
          latencyMs: -1,
          lastChecked: Date.now(),
        });
      }
    }
  }

  // ─── Dev Server (proxied dev preview) ───────────────────────────────

  /** Get dev server status from the agent. */
  async getDevServerStatus(): Promise<DevServerStatus | null> {
    if (!this.isConnected && !this.hasConnectionInfo) return null;
    try {
      const res = await fetch(`${this.baseUrl}/dev/status`, {
        headers: this.authHeaders,
      });
      if (!res.ok) return null;
      return await res.json();
    } catch { return null; }
  }

  /** Get the persisted dev preview target from the agent. */
  async getDevServerTarget(): Promise<DevTargetPreference | null> {
    if (!this.isConnected && !this.hasConnectionInfo) return null;
    try {
      const res = await fetch(`${this.baseUrl}/dev/target`, {
        headers: this.authHeaders,
      });
      if (!res.ok) return null;
      return await res.json();
    } catch { return null; }
  }

  async getDevCompatibility(workDir: string, availableModules: string[]): Promise<DevCompatibilityStatus | null> {
    if (!this.isConnected && !this.hasConnectionInfo) return null;
    try {
      const res = await fetch(`${this.baseUrl}/dev/compatibility`, {
        method: "POST",
        headers: { ...this.authHeaders, "Content-Type": "application/json" },
        body: JSON.stringify({ workDir, availableModules }),
      });
      if (!res.ok) return null;
      return await res.json();
    } catch { return null; }
  }

  /** Persist the dev preview target on the agent. */
  async setDevServerTarget(target: DevTargetPreference): Promise<DevTargetPreference | null> {
    try {
      const res = await fetch(`${this.baseUrl}/dev/target`, {
        method: "POST",
        headers: { ...this.authHeaders, "Content-Type": "application/json" },
        body: JSON.stringify(target),
      });
      if (!res.ok) return null;
      return await res.json();
    } catch { return null; }
  }

  /** Get the status of the selected mobile-worker preview session. */
  async getMobileWorkerPreviewSession(): Promise<MobileWorkerPreviewSession | null> {
    if (!this.isConnected && !this.hasConnectionInfo) return null;
    try {
      const res = await fetch(`${this.baseUrl}/mobile-workers/preview-session`, {
        headers: this.authHeaders,
      });
      if (!res.ok) return null;
      return await res.json();
    } catch { return null; }
  }

  /** Send a targeted command to the selected mobile preview worker. */
  async sendMobileWorkerPreviewCommand(command: string, data?: Record<string, unknown>): Promise<boolean> {
    try {
      const res = await fetch(`${this.baseUrl}/mobile-workers/preview-session/command`, {
        method: "POST",
        headers: { ...this.authHeaders, "Content-Type": "application/json" },
        body: JSON.stringify({ command, data }),
      });
      return res.ok;
    } catch { return false; }
  }

  /** Push a batch of blackbox events for a mobile worker session. */
  async pushBlackBoxEvents(deviceId: string, events: Array<Record<string, unknown>>, appName = "Yaver"): Promise<boolean> {
    if (!this.isConnected && !this.hasConnectionInfo) return false;
    try {
      const res = await fetch(`${this.baseUrl}/blackbox/events`, {
        method: "POST",
        headers: {
          ...this.authHeaders,
          "Content-Type": "application/json",
          "X-Device-ID": deviceId,
          "X-Platform": Platform.OS,
          "X-App-Name": appName,
        },
        body: JSON.stringify(events),
      });
      return res.ok;
    } catch { return false; }
  }

  /** Subscribe to commands for a mobile worker over the existing blackbox SSE path. */
  streamBlackBoxCommands(
    deviceId: string,
    onCommand: (event: BlackBoxCommandEnvelope) => void,
    appName = "Yaver",
  ): () => void {
    const controller = new AbortController();
    const run = async () => {
      try {
        const res = await fetch(`${this.baseUrl}/blackbox/command-stream?device=${encodeURIComponent(deviceId)}`, {
          headers: {
            ...this.authHeaders,
            Accept: "text/event-stream",
            "X-Device-ID": deviceId,
            "X-Platform": Platform.OS,
            "X-App-Name": appName,
          },
          signal: controller.signal,
        });
        const reader = res.body?.getReader();
        if (!reader) return;
        const decoder = new TextDecoder();
        let incomplete = "";
        while (true) {
          const { done, value } = await reader.read();
          if (done) break;
          const text = incomplete + decoder.decode(value, { stream: true });
          const lines = text.split("\n");
          incomplete = lines.pop() || "";
          for (const line of lines) {
            if (!line.startsWith("data: ")) continue;
            try {
              onCommand(JSON.parse(line.slice(6)));
            } catch {}
          }
        }
      } catch {}
    };
    run();
    return () => controller.abort();
  }

  /** Start a dev server on the agent. */
  async startDevServer(opts: {
    framework?: string;
    workDir?: string;
    platform?: string;
    port?: number;
    targetDeviceId?: string;
    targetDeviceName?: string;
    targetDeviceClass?: string;
  }): Promise<DevServerStatus | null> {
    // `caller: "mobile"` — explicit identity tag. The agent reads it
    // and constrains itself to the Hermes / native bundle path: it
    // will never pivot to a static web bundle for a mobile caller,
    // even if the project also happens to have a web target.
    const body = { ...opts, caller: "mobile" };
    const res = await fetch(`${this.baseUrl}/dev/start`, {
      method: "POST",
      headers: { ...this.authHeaders, "Content-Type": "application/json" },
      body: JSON.stringify(body),
    });
    // Agent returns 412 with structured payload when a runtime
    // dependency is missing on the dev box (e.g. no Node on a fresh
    // Linux machine). Throw a typed error the caller can render as a
    // one-tap "Install Node" CTA via /install/node.
    if (res.status === 412) {
      const detail = await res.json().catch(() => ({}));
      const err = new Error(detail.error || "Missing runtime on dev machine") as Error & {
        kind?: "missing-runtime";
        missingTools?: string[];
        installEndpoint?: string;
        installable?: boolean;
        helpHint?: string;
      };
      err.kind = "missing-runtime";
      err.missingTools = detail.missingTools || [];
      err.installEndpoint = detail.installEndpoint || "";
      err.installable = !!detail.installable;
      err.helpHint = detail.helpHint || "";
      throw err;
    }
    if (!res.ok) return null;
    try {
      return await res.json();
    } catch {
      return null;
    }
  }

  /**
   * Fetch the install catalogue (tool name + installed flag + one-line
   * description) from GET /install/list on the connected agent.
   *
   * `target`, when set, forwards the call to the given paired device
   * via /peer/<id>/install/list so the mobile app can enumerate what
   * a remote machine has installed.
   */
  async listInstallables(
    target?: string,
  ): Promise<{ name: string; installed: boolean; description: string }[]> {
    this.assertConnected();
    const base = this.peerEndpoint(target, "/install/list");
    const res = await fetch(base, { headers: this.authHeaders });
    if (!res.ok) return [];
    return res.json();
  }

  /**
   * Trigger a dependency install on the dev machine via
   * POST /install/<tool>. Returns the SSE stream name to subscribe to
   * for live progress (use subscribeStream() below).
   *
   * `target` forwards the call to a paired device via
   * /peer/<id>/install/<tool>, making phones able to install tools on
   * a different machine than they're directly connected to (the
   * "cross-machine install" story).
   */
  async installTool(
    tool: string,
    target?: string,
  ): Promise<{ ok: boolean; tool: string; stream: string; error?: string }> {
    this.assertConnected();
    const base = this.peerEndpoint(target, `/install/${encodeURIComponent(tool)}`);
    const res = await fetch(base, {
      method: "POST",
      headers: this.authHeaders,
    });
    const data = await res.json().catch(() => ({}));
    if (!res.ok) {
      return { ok: false, tool, stream: "", error: data.error || `HTTP ${res.status}` };
    }
    return { ok: true, tool: data.tool || tool, stream: data.stream || `install:${tool}` };
  }

  /**
   * Install a coding-agent runner (claude / codex / opencode) on
   * the connected device (or a peer when `target` is set). Thin
   * wrapper around installTool + streamLog so the mobile
   * CodingAgentsSection install button can show live progress
   * without re-implementing the SSE subscribe + result-event dance.
   *
   * Returns once the install:<runner> stream emits a terminal
   * `{type:"result", status:"ok"|"error"}` event. `onProgress`
   * receives every progress line (npm output + agent headers).
   *
   * Same agent endpoint as `yaver install <runner>` — /install/<runner>
   * with the /peer/<id> proxy — so fresh boxes (Pi, ARM cloud, mac
   * without brew) get node auto-provisioned into
   * ~/.yaver/runtimes/node before `npm install -g`. See
   * ensureRunnerInstalledStream in desktop/agent/install_cmd.go.
   */
  async installRunner(
    runnerId: string,
    opts?: { target?: string; onProgress?: (line: string) => void },
  ): Promise<{ ok: boolean; runnerId: string; error?: string }> {
    const target = opts?.target;
    const onProgress = opts?.onProgress;
    const started = await this.installTool(runnerId, target);
    if (!started.ok) {
      return { ok: false, runnerId, error: started.error || "install failed to start" };
    }
    return await new Promise((resolve) => {
      let settled = false;
      const finish = (result: { ok: boolean; runnerId: string; error?: string }) => {
        if (settled) return;
        settled = true;
        try { unsub(); } catch { /* ignore */ }
        resolve(result);
      };
      const unsub = this.streamLog(
        started.stream,
        (ev: any) => {
          if (!ev || typeof ev !== "object") return;
          if (typeof ev.text === "string" && onProgress) {
            onProgress(ev.text);
          } else if (typeof ev.line === "string" && onProgress) {
            onProgress(ev.line);
          }
          if (ev.type === "result") {
            if (ev.status === "ok") {
              finish({ ok: true, runnerId });
            } else {
              finish({
                ok: false,
                runnerId,
                error: typeof ev.error === "string" ? ev.error : "install failed",
              });
            }
          }
        },
        () => finish({ ok: false, runnerId, error: "install stream closed before completion" }),
      );
    });
  }

  /**
   * Trigger an agent self-update on the connected device (or a peer
   * when `target` is set). The agent fetches the matching binary
   * from its configured GitHub release URL, codesigns it on macOS,
   * replaces itself, and asks systemd / launchd to restart with the
   * new binary. Returns the agent's pre-flight decision:
   *   started=true     → update is now running (poll /info for completion)
   *   started=false    → agent thinks it is already on the latest
   * Reuses /agent/update and the /peer/<id> proxy that all other
   * device-targeted helpers already go through (installTool /
   * installRunner / runnerAuthCredentialsImport).
   */
  async triggerAgentUpdate(
    target?: string,
  ): Promise<{
    ok?: boolean;
    started?: boolean;
    message?: string;
    currentVersion?: string;
    latestVersion?: string;
    error?: string;
  }> {
    this.assertConnected();
    const base = this.peerEndpoint(target, "/agent/update");
    const res = await fetch(base, {
      method: "POST",
      headers: { ...this.authHeaders, "Content-Type": "application/json" },
      body: "{}",
    });
    const data = await res.json().catch(() => ({}));
    if (!res.ok) {
      return { ok: false, error: data?.error || `HTTP ${res.status}` };
    }
    return { ok: true, ...data };
  }

  /**
   * Fetch agent update status + advertised latest version. Used by
   * the device card to render a "v1.99.36 → v1.99.221" hint before
   * the user opens the update sheet.
   */
  async getAgentUpdateStatus(
    target?: string,
  ): Promise<{
    currentVersion?: string;
    latestVersion?: string;
    updateAvailable?: boolean;
    error?: string;
  } | null> {
    if (!this.isConnected && !this.hasConnectionInfo) return null;
    try {
      const base = this.peerEndpoint(target, "/agent/update");
      const res = await fetch(base, { headers: this.authHeaders });
      if (!res.ok) return null;
      return await res.json();
    } catch {
      return null;
    }
  }

  /**
   * /info on a peer (or on the connected device when target is
   * omitted). Used by the agent-update poll loop to detect when
   * the restarted process reports the new version.
   */
  async getInfoFor(
    target?: string,
  ): Promise<{ hostname: string; version: string; workDir: string } | null> {
    if (!this.isConnected && !this.hasConnectionInfo) return null;
    try {
      const base = this.peerEndpoint(target, "/info");
      const res = await fetch(base, { headers: this.authHeaders });
      if (!res.ok) return null;
      const data = await res.json();
      return {
        hostname: data.hostname || "",
        version: data.version || "",
        workDir: data.workDir || "",
      };
    } catch {
      return null;
    }
  }

  /**
   * Respond to an in-flight install that asked for a sudo password.
   * The agent wrote `{"type":"sudo_prompt", ...}` to the install
   * stream; the UI showed a secure sheet; the answer flows back
   * through this endpoint. Setting `cancel: true` sends ^C instead.
   */
  async respondInstallSudo(
    tool: string,
    password: string,
    cancel = false,
    target?: string,
  ): Promise<{ ok: boolean; error?: string }> {
    this.assertConnected();
    const base = this.peerEndpoint(target, "/install/sudo");
    const res = await fetch(base, {
      method: "POST",
      headers: { ...this.authHeaders, "Content-Type": "application/json" },
      body: JSON.stringify({ tool, password, cancel }),
    });
    const data = await res.json().catch(() => ({}));
    if (!res.ok) return { ok: false, error: data.error || `HTTP ${res.status}` };
    return { ok: true };
  }

  /**
   * Subscribe to a named log stream over SSE. Calls onLine for each
   * `{type:"line",text}` event and onResult for the terminal
   * `{type:"result",status}` event. Returns a cancel function.
   *
   * `onEvent` — if set — is called for every other structured event
   * on the stream, including `{type:"sudo_prompt", prompt}` which
   * the install catalogue uses to ask the phone for a password.
   */
  subscribeStream(
    name: string,
    onLine: (text: string) => void,
    onResult?: (status: string, error?: string) => void,
    onEvent?: (event: any) => void,
  ): () => void {
    const ctrl = new AbortController();
    const url = `${this.baseUrl}/streams/${encodeURIComponent(name)}`;
    const auth = this.authHeaders;
    (async () => {
      try {
        const res = await fetch(url, { headers: auth, signal: ctrl.signal });
        if (!res.ok || !res.body) return;
        const reader = res.body.getReader();
        const decoder = new TextDecoder("utf-8");
        let buf = "";
        while (true) {
          const { value, done } = await reader.read();
          if (done) break;
          buf += decoder.decode(value, { stream: true });
          let idx;
          while ((idx = buf.indexOf("\n\n")) >= 0) {
            const chunk = buf.slice(0, idx).trim();
            buf = buf.slice(idx + 2);
            if (!chunk.startsWith("data:")) continue;
            const payload = chunk.slice(5).trim();
            try {
              const ev = JSON.parse(payload);
              if (ev.type === "line" && typeof ev.text === "string") {
                onLine(ev.text);
              } else if (ev.type === "result" && onResult) {
                onResult(ev.status || "", ev.error);
              } else if (onEvent) {
                onEvent(ev);
              }
            } catch {
              // ignore non-JSON SSE comments / keepalives
            }
          }
        }
      } catch {
        // network drop / cancel — caller should treat as ended
      }
    })();
    return () => ctrl.abort();
  }

  /** Stop serving the active preview/dev server.
   *  Agent (1.99.93+) returns `verified` (true once subprocess is down)
   *  and `buildsCancelled` (count of in-flight Hermes builds aborted).
   *  Older agents just return `{ok, stoppedServing, previouslyServing}`. */
  async stopDevServer(): Promise<{
    ok?: boolean;
    stoppedServing?: boolean;
    previouslyServing?: boolean;
    verified?: boolean;
    buildsCancelled?: number;
    framework?: string;
    workDir?: string;
    message?: string;
    error?: string;
  } | null> {
    try {
      const res = await fetch(`${this.baseUrl}/dev/stop`, {
        method: "POST",
        headers: this.authHeaders,
      });
      const data = await res.json().catch(() => ({}));
      if (!res.ok) return { ok: false, error: data.error || data.message || `HTTP ${res.status}` };
      return data;
    } catch {
      return { ok: false, error: "network error" };
    }
  }

  /**
   * Trigger a reload on the connected agent.
   *
   * Default behaviour is **always rebuild**: we hit /dev/reload-app with
   * `mode: "bundle"` so the agent recompiles a fresh Hermes bytecode
   * bundle and pushes it to the device via BlackBox SSE. That path
   * works regardless of whether Metro is currently alive on the Mac —
   * which is the common case when the user is vibe-coding from the
   * phone while an AI agent edits files remotely.
   *
   * Pass `{ mode: "dev" }` to force the old Metro-HMR path for callers
   * who know Metro is running. On any 4xx/5xx from /dev/reload we fall
   * through to /dev/reload-app bundle mode so the user never sees a
   * "connection refused to 127.0.0.1:8081" Go error.
   */
  async reloadDevServer(opts?: { mode?: "dev" | "bundle" }): Promise<boolean> {
    const mode = opts?.mode ?? "bundle";
    try {
      if (mode === "dev") {
        const primary = await fetch(`${this.baseUrl}/dev/reload`, {
          method: "POST",
          headers: this.authHeaders,
        });
        if (primary.ok) return true;
        // Metro dead — fall through to bundle rebuild below.
      }
      const res = await fetch(`${this.baseUrl}/dev/reload-app`, {
        method: "POST",
        headers: { ...this.authHeaders, "Content-Type": "application/json" },
        body: JSON.stringify({ mode: "bundle" }),
      });
      if (!res.ok) {
        console.warn("[QUIC] Hermes reload failed:", await responseErrorMessage(res, `HTTP ${res.status}`));
      }
      return res.ok;
    } catch { return false; }
  }

  /** Get the full URL for the dev server bundle (through relay if needed). */
  getDevServerBundleUrl(bundlePath: string): string {
    return `${this.baseUrl}${bundlePath}`;
  }

  // ── Container Sandbox ───────────────────────────────────────────────

  /** Get container sandbox status from agent. */
  async getSandboxStatus(): Promise<SandboxStatus | null> {
    if (!this.isConnected && !this.hasConnectionInfo) return null;
    try {
      const res = await fetch(`${this.baseUrl}/sandbox/status`, {
        headers: this.authHeaders,
      });
      if (!res.ok) return null;
      return await res.json();
    } catch { return null; }
  }

  /** One-step containerization setup for shared infra or full host isolation. */
  async sandboxQuickstart(mode: "guests" | "host", buildImage = true): Promise<{ ok: boolean; message?: string; sandbox?: SandboxStatus; error?: string }> {
    try {
      const res = await fetch(`${this.baseUrl}/sandbox/quickstart`, {
        method: "POST",
        headers: { ...this.authHeaders, "Content-Type": "application/json" },
        body: JSON.stringify({ mode, buildImage }),
      });
      const data = await res.json().catch(() => ({}));
      if (!res.ok) return { ok: false, error: data?.error || `HTTP ${res.status}` };
      return { ok: true, message: data?.message, sandbox: data?.sandbox };
    } catch (e: any) {
      return { ok: false, error: e?.message || "network error" };
    }
  }

  /** Update container sandbox config on agent. Changes are persisted. */
  async updateSandboxConfig(config: Partial<SandboxConfig>): Promise<boolean> {
    try {
      const res = await fetch(`${this.baseUrl}/sandbox/config`, {
        method: "POST",
        headers: { ...this.authHeaders, "Content-Type": "application/json" },
        body: JSON.stringify(config),
      });
      return res.ok;
    } catch { return false; }
  }

  /** Trigger sandbox Docker image build on agent. Returns immediately; poll status. */
  async buildSandboxImage(): Promise<boolean> {
    try {
      const res = await fetch(`${this.baseUrl}/sandbox/build`, {
        method: "POST",
        headers: this.authHeaders,
      });
      return res.ok;
    } catch { return false; }
  }

  // ── yaver-test-sdk (embedded local CI runner) ───────────────────────
  // Drives the agent's chromedp-backed runner over the existing P2P
  // transport. Specs live in the user's repo at yaver-tests/*.test.yaml,
  // results live on the agent's disk; nothing here ever talks to Convex.

  /** List the spec files the agent would run. */
  async testkitListSpecs(root?: string): Promise<TestkitSpec[]> {
    try {
      const url = root
        ? `${this.baseUrl}/testkit/specs?root=${encodeURIComponent(root)}`
        : `${this.baseUrl}/testkit/specs`;
      const res = await fetch(url, { headers: this.authHeaders });
      if (!res.ok) throw new Error(`status ${res.status}`);
      const data = await res.json();
      return data.specs || [];
    } catch {
      return [];
    }
  }

  /** Get the current run status (running flag + last suite). */
  async testkitRunStatus(): Promise<TestkitRunStatus | null> {
    try {
      const res = await fetch(`${this.baseUrl}/testkit/run`, {
        headers: this.authHeaders,
      });
      if (!res.ok) return null;
      return await res.json();
    } catch {
      return null;
    }
  }

  /** Kick off a new run. Returns false if another run is already in progress. */
  async testkitStartRun(opts: TestkitRunOpts = {}): Promise<{ ok: boolean; reason?: string }> {
    try {
      const res = await fetch(`${this.baseUrl}/testkit/run`, {
        method: "POST",
        headers: { ...this.authHeaders, "Content-Type": "application/json" },
        body: JSON.stringify(opts),
      });
      if (res.ok || res.status === 202) return { ok: true };
      const text = await res.text();
      return { ok: false, reason: text || `HTTP ${res.status}` };
    } catch (e: any) {
      return { ok: false, reason: e?.message ?? "network error" };
    }
  }

  /** Local run history (most recent 50 entries). */
  async testkitHistory(root?: string): Promise<TestkitHistoryEntry[]> {
    try {
      const url = root
        ? `${this.baseUrl}/testkit/history?root=${encodeURIComponent(root)}`
        : `${this.baseUrl}/testkit/history`;
      const res = await fetch(url, { headers: this.authHeaders });
      if (!res.ok) return [];
      const data = await res.json();
      return data.entries || [];
    } catch {
      return [];
    }
  }

  /** Per-spec failure ratios over the last 100 runs. */
  async testkitFlakeReport(root?: string): Promise<TestkitFlakeStats[]> {
    try {
      const url = root
        ? `${this.baseUrl}/testkit/flake?root=${encodeURIComponent(root)}`
        : `${this.baseUrl}/testkit/flake`;
      const res = await fetch(url, { headers: this.authHeaders });
      if (!res.ok) return [];
      const data = await res.json();
      return data.stats || [];
    } catch {
      return [];
    }
  }

  /** Failure-only notifications from the local stream. Mobile polls this. */
  async testkitNotifications(root?: string): Promise<TestkitNotification[]> {
    try {
      const url = root
        ? `${this.baseUrl}/testkit/notifications?root=${encodeURIComponent(root)}`
        : `${this.baseUrl}/testkit/notifications`;
      const res = await fetch(url, { headers: this.authHeaders });
      if (!res.ok) return [];
      const data = await res.json();
      return data.notifications || [];
    } catch {
      return [];
    }
  }

  /** Local pass markers — "this SHA already passed locally." */
  async testkitMarkers(root?: string): Promise<TestkitPassMarker[]> {
    try {
      const url = root
        ? `${this.baseUrl}/testkit/markers?root=${encodeURIComponent(root)}`
        : `${this.baseUrl}/testkit/markers`;
      const res = await fetch(url, { headers: this.authHeaders });
      if (!res.ok) return [];
      const data = await res.json();
      return data.markers || [];
    } catch {
      return [];
    }
  }

  /** Connected USB devices the agent can drive. */
  async testkitDevices(): Promise<TestkitUSBDevice[]> {
    try {
      const res = await fetch(`${this.baseUrl}/testkit/devices`, {
        headers: this.authHeaders,
      });
      if (!res.ok) return [];
      const data = await res.json();
      return data.devices || [];
    } catch {
      return [];
    }
  }

  /** Local CI integration install state (chrome, adb, xcode, etc). */
  async testkitIntegrations(): Promise<TestkitIntegration[]> {
    try {
      const res = await fetch(`${this.baseUrl}/testkit/integrations`, {
        headers: this.authHeaders,
      });
      if (!res.ok) return [];
      const data = await res.json();
      return data.integrations || [];
    } catch {
      return [];
    }
  }

  /** Recent autofixes the autonomous loop has applied. */
  async testkitAutoFix(root?: string): Promise<TestkitAutoFix[]> {
    try {
      const url = root
        ? `${this.baseUrl}/testkit/autofix?root=${encodeURIComponent(root)}`
        : `${this.baseUrl}/testkit/autofix`;
      const res = await fetch(url, { headers: this.authHeaders });
      if (!res.ok) return [];
      const data = await res.json();
      return data.autofixes || [];
    } catch {
      return [];
    }
  }

  /** Roll back a previously-applied autofix. */
  async testkitAutoFixUndo(id: string): Promise<boolean> {
    try {
      const res = await fetch(`${this.baseUrl}/testkit/autofix/${encodeURIComponent(id)}/undo`, {
        method: "POST",
        headers: { ...this.authHeaders, "Content-Type": "application/json" },
        body: JSON.stringify({ by: "mobile" }),
      });
      return res.ok;
    } catch {
      return false;
    }
  }

  /** Resolve an artifact path on the agent into a fetchable URL.
   *  Used by the screenshot viewer modal — pass the absolute path
   *  the runner reported (e.g. spec.steps[i].screenshot) and you get
   *  back a URL the <Image> component can hit directly. */
  testkitArtifactUrl(path: string, root?: string): string {
    const params = new URLSearchParams({ path });
    if (root) params.set("root", root);
    return `${this.baseUrl}/testkit/artifact?${params.toString()}`;
  }

  /** List the PNG frames in a screencast directory (written by
   *  testkit.FlushFrames on step failure). Returns absolute frame
   *  paths + fps so the FrameSequencePlayer can play them back via
   *  testkitArtifactUrl. */
  async testkitFrames(
    dir: string,
    root?: string,
  ): Promise<TestkitFrameList | null> {
    try {
      const params = new URLSearchParams({ dir });
      if (root) params.set("root", root);
      const res = await fetch(
        `${this.baseUrl}/testkit/frames?${params.toString()}`,
        { headers: this.authHeaders },
      );
      if (!res.ok) return null;
      return await res.json();
    } catch {
      return null;
    }
  }

  /** Headers the mobile <Image> component must include when pulling
   *  artifacts from the agent. Image already accepts a `headers`
   *  prop on iOS / Android. */
  get testkitArtifactHeaders(): Record<string, string> {
    return this.authHeaders;
  }

  // ---- Releases (self-hosted OTA) ---------------------------------------

  /** List every release in a channel with rollout percent. */
  async releasesList(channel: string = "production"): Promise<ReleaseManifest | null> {
    try {
      const res = await fetch(
        `${this.baseUrl}/releases/list?channel=${encodeURIComponent(channel)}`,
        { headers: this.authHeaders },
      );
      if (!res.ok) return null;
      const data = await res.json();
      return data.manifest ?? null;
    } catch {
      return null;
    }
  }

  /** Ask what this device should run on cold start. */
  async releasesLatest(
    channel: string = "production",
    deviceId?: string,
  ): Promise<ReleaseLatest | null> {
    try {
      const params = new URLSearchParams({ channel });
      if (deviceId) params.set("device", deviceId);
      const res = await fetch(
        `${this.baseUrl}/releases/latest?${params.toString()}`,
        { headers: this.authHeaders },
      );
      if (!res.ok) return null;
      return await res.json();
    } catch {
      return null;
    }
  }

  /** Rollback the channel to a previously-published semver. */
  async releasesRollback(channel: string, semver: string): Promise<boolean> {
    try {
      const res = await fetch(`${this.baseUrl}/exec`, {
        method: "POST",
        headers: { ...this.authHeaders, "Content-Type": "application/json" },
        body: JSON.stringify({
          command: `yaver release rollback ${channel} ${semver}`,
        }),
      });
      return res.ok;
    } catch {
      return false;
    }
  }

  /** Set the rollout percentage for a channel. */
  async releasesRollout(channel: string, percent: number): Promise<boolean> {
    try {
      const res = await fetch(`${this.baseUrl}/exec`, {
        method: "POST",
        headers: { ...this.authHeaders, "Content-Type": "application/json" },
        body: JSON.stringify({
          command: `yaver release rollout ${channel} ${percent}`,
        }),
      });
      return res.ok;
    } catch {
      return false;
    }
  }

  // ---- Errors (cross-device aggregation) -------------------------------

  /** List errors with header stats. */
  async errorsList(includeResolved: boolean = false): Promise<ErrorsListResponse | null> {
    try {
      const url = includeResolved
        ? `${this.baseUrl}/errors?include_resolved=1`
        : `${this.baseUrl}/errors`;
      const res = await fetch(url, { headers: this.authHeaders });
      if (!res.ok) return null;
      return await res.json();
    } catch {
      return null;
    }
  }

  async incidents(opts: {
    category?: string;
    severity?: string;
    code?: string;
    device?: string;
    projectPath?: string;
    includeResolved?: boolean;
    limit?: number;
  } = {}): Promise<IncidentEvent[]> {
    if (!this.isConnected && !this.hasConnectionInfo) return [];
    try {
      const p = new URLSearchParams();
      if (opts.category) p.set("category", opts.category);
      if (opts.severity) p.set("severity", opts.severity);
      if (opts.code) p.set("code", opts.code);
      if (opts.device) p.set("device", opts.device);
      if (opts.projectPath) p.set("projectPath", opts.projectPath);
      if (opts.includeResolved) p.set("include_resolved", "1");
      if (opts.limit) p.set("limit", String(opts.limit));
      const res = await fetch(`${this.baseUrl}/incidents?${p.toString()}`, { headers: this.authHeaders });
      if (!res.ok) return [];
      const data = await res.json();
      return (data?.incidents as IncidentEvent[]) ?? [];
    } catch {
      return [];
    }
  }

  async incidentSummary(): Promise<IncidentSummary | null> {
    if (!this.isConnected && !this.hasConnectionInfo) return null;
    try {
      const res = await fetch(`${this.baseUrl}/incidents/summary`, { headers: this.authHeaders });
      if (!res.ok) return null;
      const data = await res.json();
      return (data?.summary as IncidentSummary) ?? null;
    } catch {
      return null;
    }
  }

  async operations(opts: {
    kind?: string;
    status?: string;
    device?: string;
    projectPath?: string;
    limit?: number;
  } = {}): Promise<OperationState[]> {
    if (!this.isConnected && !this.hasConnectionInfo) return [];
    try {
      const p = new URLSearchParams();
      if (opts.kind) p.set("kind", opts.kind);
      if (opts.status) p.set("status", opts.status);
      if (opts.device) p.set("device", opts.device);
      if (opts.projectPath) p.set("projectPath", opts.projectPath);
      if (opts.limit) p.set("limit", String(opts.limit));
      const res = await fetch(`${this.baseUrl}/operations?${p.toString()}`, { headers: this.authHeaders });
      if (!res.ok) return [];
      const data = await res.json();
      return (data?.operations as OperationState[]) ?? [];
    } catch {
      return [];
    }
  }

  /** Mark an error as resolved with an optional one-liner note. */
  async errorResolve(fingerprint: string, note?: string): Promise<boolean> {
    try {
      const res = await fetch(`${this.baseUrl}/errors/resolve`, {
        method: "POST",
        headers: { ...this.authHeaders, "Content-Type": "application/json" },
        body: JSON.stringify({ fingerprint, note }),
      });
      return res.ok;
    } catch {
      return false;
    }
  }

  /** Reopen a previously-resolved error. */
  async errorReopen(fingerprint: string): Promise<boolean> {
    try {
      const res = await fetch(`${this.baseUrl}/errors/reopen`, {
        method: "POST",
        headers: { ...this.authHeaders, "Content-Type": "application/json" },
        body: JSON.stringify({ fingerprint }),
      });
      return res.ok;
    } catch {
      return false;
    }
  }

  // ---- Uptime monitors (U1) ----------------------------------------------

  async monitorsList(): Promise<YaverMonitor[]> {
    try {
      const res = await fetch(`${this.baseUrl}/monitors`, { headers: this.authHeaders });
      if (!res.ok) return [];
      const data = await res.json();
      return data.monitors ?? [];
    } catch {
      return [];
    }
  }

  async monitorsAdd(input: {
    url: string;
    name?: string;
    interval?: string;
    method?: string;
  }): Promise<boolean> {
    try {
      const res = await fetch(`${this.baseUrl}/monitors`, {
        method: "POST",
        headers: { ...this.authHeaders, "Content-Type": "application/json" },
        body: JSON.stringify(input),
      });
      return res.ok;
    } catch {
      return false;
    }
  }

  async monitorsRemove(id: string): Promise<boolean> {
    try {
      const res = await fetch(
        `${this.baseUrl}/monitors/${encodeURIComponent(id)}`,
        { method: "DELETE", headers: this.authHeaders },
      );
      return res.ok;
    } catch {
      return false;
    }
  }

  async monitorsPause(id: string, paused: boolean): Promise<boolean> {
    try {
      const action = paused ? "pause" : "resume";
      const res = await fetch(
        `${this.baseUrl}/monitors/${encodeURIComponent(id)}/${action}`,
        { method: "POST", headers: this.authHeaders },
      );
      return res.ok;
    } catch {
      return false;
    }
  }

  async monitorsCheck(id: string): Promise<MonitorCheck | null> {
    try {
      const res = await fetch(
        `${this.baseUrl}/monitors/${encodeURIComponent(id)}/check`,
        { method: "POST", headers: this.authHeaders },
      );
      if (!res.ok) return null;
      const data = await res.json();
      return data.check ?? null;
    } catch {
      return null;
    }
  }

  // ---- Analytics ingest (A1) ---------------------------------------------

  async analyticsEvents(since?: number, limit: number = 100): Promise<TrackEvent[]> {
    try {
      const params = new URLSearchParams({ limit: String(limit) });
      if (since) params.set("since", String(since));
      const res = await fetch(
        `${this.baseUrl}/analytics/events?${params.toString()}`,
        { headers: this.authHeaders },
      );
      if (!res.ok) return [];
      const data = await res.json();
      return data.events ?? [];
    } catch {
      return [];
    }
  }

  analyticsCSVUrl(): string {
    return `${this.baseUrl}/analytics/events.csv`;
  }

  // ---- Feature flags (F1) ------------------------------------------------

  async flagsList(): Promise<YaverFlag[]> {
    try {
      const res = await fetch(`${this.baseUrl}/flags`, { headers: this.authHeaders });
      if (!res.ok) return [];
      const data = await res.json();
      return data.flags ?? [];
    } catch {
      return [];
    }
  }

  async flagsSet(flag: YaverFlag): Promise<boolean> {
    try {
      const res = await fetch(`${this.baseUrl}/flags`, {
        method: "POST",
        headers: { ...this.authHeaders, "Content-Type": "application/json" },
        body: JSON.stringify(flag),
      });
      return res.ok;
    } catch {
      return false;
    }
  }

  async flagsDelete(key: string): Promise<boolean> {
    try {
      const res = await fetch(`${this.baseUrl}/flags/delete`, {
        method: "POST",
        headers: { ...this.authHeaders, "Content-Type": "application/json" },
        body: JSON.stringify({ key }),
      });
      return res.ok;
    } catch {
      return false;
    }
  }

  async flagsOverride(key: string, userId: string, value: string, clear: boolean = false): Promise<boolean> {
    try {
      const res = await fetch(`${this.baseUrl}/flags/override`, {
        method: "POST",
        headers: { ...this.authHeaders, "Content-Type": "application/json" },
        body: JSON.stringify({ key, userId, value, clear }),
      });
      return res.ok;
    } catch {
      return false;
    }
  }

  async flagsEval(userId: string): Promise<Record<string, unknown> | null> {
    try {
      const res = await fetch(
        `${this.baseUrl}/flags/eval?userId=${encodeURIComponent(userId)}`,
        { headers: this.authHeaders },
      );
      if (!res.ok) return null;
      const data = await res.json();
      return data.flags ?? null;
    } catch {
      return null;
    }
  }

  // ---- Machine health (disk + SMART + peers) ----------------------------

  async machineHealth(): Promise<MachineHealth | null> {
    try {
      const res = await fetch(`${this.baseUrl}/machine/health`, {
        headers: this.authHeaders,
      });
      if (!res.ok) return null;
      const data = await res.json();
      return data.health ?? null;
    } catch {
      return null;
    }
  }

  async machinePeers(): Promise<PeerState[]> {
    try {
      const res = await fetch(`${this.baseUrl}/machine/peers`, {
        headers: this.authHeaders,
      });
      if (!res.ok) return [];
      const data = await res.json();
      return data.peers ?? [];
    } catch {
      return [];
    }
  }

  // ---- Clips (screen recording) ----------------------------------------

  async clipStart(body: { title?: string; description?: string; targets?: string[] }): Promise<any | null> {
    try {
      const res = await fetch(`${this.baseUrl}/clips/start`, {
        method: "POST",
        headers: { ...this.authHeaders, "Content-Type": "application/json" },
        body: JSON.stringify(body),
      });
      if (!res.ok) return null;
      return await res.json();
    } catch { return null; }
  }

  async clipStop(): Promise<any | null> {
    try {
      const res = await fetch(`${this.baseUrl}/clips/stop`, { method: "POST", headers: this.authHeaders });
      if (!res.ok) return null;
      return await res.json();
    } catch { return null; }
  }

  async clipList(): Promise<any[]> {
    try {
      const res = await fetch(`${this.baseUrl}/clips/list`, { headers: this.authHeaders });
      if (!res.ok) return [];
      return (await res.json()).sessions || [];
    } catch { return []; }
  }

  async clipUploadMobileScreen(sessionId: string, fileUri: string): Promise<any | null> {
    try {
      const fileContent = await fetch(fileUri);
      const blob = await fileContent.blob();
      const res = await fetch(`${this.baseUrl}/clips/upload/${sessionId}?kind=mobile-screen`, {
        method: "POST",
        headers: { ...this.authHeaders, "Content-Type": "video/mp4" },
        body: blob,
      });
      if (!res.ok) return null;
      return await res.json();
    } catch { return null; }
  }

  async clipMerge(sessionId: string): Promise<any | null> {
    try {
      const res = await fetch(`${this.baseUrl}/clips/merge/${sessionId}`, {
        method: "POST",
        headers: this.authHeaders,
      });
      if (!res.ok) return null;
      return await res.json();
    } catch { return null; }
  }

  clipPrivateVideoRequest(sessionId: string, filename?: string): { uri: string; headers: Record<string, string> } | null {
    if (!this.baseUrl) return null;
    const leaf = filename || "agent-screen.mp4";
    return {
      uri: `${this.baseUrl}/clips/private/${encodeURIComponent(sessionId)}/${encodeURIComponent(leaf)}`,
      headers: this.authHeaders,
    };
  }

  // ---- Chat (live visitor widget) --------------------------------------

  async chatConversations(): Promise<any[]> {
    try {
      const res = await fetch(`${this.baseUrl}/chat/conversations`, { headers: this.authHeaders });
      if (!res.ok) return [];
      return (await res.json()).conversations || [];
    } catch { return []; }
  }

  async chatHistory(vid: string): Promise<any[]> {
    try {
      const res = await fetch(`${this.baseUrl}/chat/messages?vid=${encodeURIComponent(vid)}`, { headers: this.authHeaders });
      if (!res.ok) return [];
      return (await res.json()).messages || [];
    } catch { return []; }
  }

  async chatReply(vid: string, text: string): Promise<boolean> {
    try {
      const res = await fetch(`${this.baseUrl}/chat/reply`, {
        method: "POST",
        headers: { ...this.authHeaders, "Content-Type": "application/json" },
        body: JSON.stringify({ vid, text }),
      });
      return res.ok;
    } catch { return false; }
  }

  // ---- A/B experiments -------------------------------------------------

  async abExperiments(): Promise<any[]> {
    try {
      const res = await fetch(`${this.baseUrl}/ab/experiments`, { headers: this.authHeaders });
      if (!res.ok) return [];
      return (await res.json()).experiments || [];
    } catch { return []; }
  }

  async abResults(key: string): Promise<Record<string, any> | null> {
    try {
      const res = await fetch(`${this.baseUrl}/ab/results?key=${encodeURIComponent(key)}`, { headers: this.authHeaders });
      if (!res.ok) return null;
      return (await res.json()).results ?? {};
    } catch { return null; }
  }

  // ---- Invoices --------------------------------------------------------

  async invoicesList(): Promise<any[]> {
    try {
      const res = await fetch(`${this.baseUrl}/invoices`, { headers: this.authHeaders });
      if (!res.ok) return [];
      return (await res.json()).invoices || [];
    } catch { return []; }
  }

  async customersList(): Promise<any[]> {
    try {
      const res = await fetch(`${this.baseUrl}/customers`, { headers: this.authHeaders });
      if (!res.ok) return [];
      return (await res.json()).customers || [];
    } catch { return []; }
  }

  // ---- Affiliates ------------------------------------------------------

  async affiliatesList(): Promise<any[]> {
    try {
      const res = await fetch(`${this.baseUrl}/affiliates`, { headers: this.authHeaders });
      if (!res.ok) return [];
      return (await res.json()).affiliates || [];
    } catch { return []; }
  }

  // ---- Asciinema -------------------------------------------------------

  async asciinemaList(): Promise<any[]> {
    try {
      const res = await fetch(`${this.baseUrl}/asciinema`, { headers: this.authHeaders });
      if (!res.ok) return [];
      return (await res.json()).casts || [];
    } catch { return []; }
  }

  // ---- Shortener --------------------------------------------------------

  async shortList(): Promise<any[]> {
    try {
      const res = await fetch(`${this.baseUrl}/shortener`, { headers: this.authHeaders });
      if (!res.ok) return [];
      return (await res.json()).links || [];
    } catch { return []; }
  }

  async shortCreate(body: { url: string; code?: string; label?: string }): Promise<any | null> {
    try {
      const res = await fetch(`${this.baseUrl}/shortener`, {
        method: "POST",
        headers: { ...this.authHeaders, "Content-Type": "application/json" },
        body: JSON.stringify(body),
      });
      if (!res.ok) return null;
      return (await res.json()).link;
    } catch { return null; }
  }

  async shortDelete(code: string): Promise<boolean> {
    try {
      const res = await fetch(`${this.baseUrl}/shortener?code=${encodeURIComponent(code)}`, {
        method: "DELETE", headers: this.authHeaders,
      });
      return res.ok;
    } catch { return false; }
  }

  // ---- Waitlist ---------------------------------------------------------

  async waitlistList(): Promise<{ entries: any[]; total: number } | null> {
    try {
      const res = await fetch(`${this.baseUrl}/waitlist`, { headers: this.authHeaders });
      if (!res.ok) return null;
      return await res.json();
    } catch { return null; }
  }

  async waitlistLeaderboard(): Promise<any[]> {
    try {
      const res = await fetch(`${this.baseUrl}/waitlist/leaderboard`);
      if (!res.ok) return [];
      return (await res.json()).leaderboard || [];
    } catch { return []; }
  }

  async waitlistDelete(email: string): Promise<boolean> {
    try {
      const res = await fetch(`${this.baseUrl}/waitlist?email=${encodeURIComponent(email)}`, {
        method: "DELETE", headers: this.authHeaders,
      });
      return res.ok;
    } catch { return false; }
  }

  // ---- Docs site --------------------------------------------------------

  async docsList(): Promise<{ tree: any[]; config: any } | null> {
    try {
      const res = await fetch(`${this.baseUrl}/docs/_json`, { headers: this.authHeaders });
      if (!res.ok) return null;
      return await res.json();
    } catch { return null; }
  }

  async docsConfig(body: { path: string; title?: string; theme?: string }): Promise<boolean> {
    try {
      const res = await fetch(`${this.baseUrl}/docs/config`, {
        method: "POST",
        headers: { ...this.authHeaders, "Content-Type": "application/json" },
        body: JSON.stringify(body),
      });
      return res.ok;
    } catch { return false; }
  }

  // ---- Meetings ---------------------------------------------------------

  async meetingsList(): Promise<any[]> {
    try {
      const res = await fetch(`${this.baseUrl}/meetings`, { headers: this.authHeaders });
      if (!res.ok) return [];
      return (await res.json()).eventTypes || [];
    } catch { return []; }
  }

  async meetingsCreate(body: any): Promise<any | null> {
    try {
      const res = await fetch(`${this.baseUrl}/meetings`, {
        method: "POST",
        headers: { ...this.authHeaders, "Content-Type": "application/json" },
        body: JSON.stringify(body),
      });
      if (!res.ok) return null;
      return (await res.json()).eventType;
    } catch { return null; }
  }

  async meetingBookings(): Promise<any[]> {
    try {
      const res = await fetch(`${this.baseUrl}/bookings`, { headers: this.authHeaders });
      if (!res.ok) return [];
      return (await res.json()).bookings || [];
    } catch { return []; }
  }

  // ---- Newsletter compose (from git activity) --------------------------

  async newsletterCompose(opts: { repo: string; sinceDays?: number; includePrs?: boolean; includeIssues?: boolean; subject?: string; instructions?: string; execute?: boolean; saveDraft?: boolean }): Promise<{ subject: string; draft: string; prompt: string; activity: any; campaignId?: string } | null> {
    try {
      const res = await fetch(`${this.baseUrl}/newsletter/compose`, {
        method: "POST",
        headers: { ...this.authHeaders, "Content-Type": "application/json" },
        body: JSON.stringify(opts),
      });
      if (!res.ok) return null;
      return await res.json();
    } catch { return null; }
  }

  // ---- PDF render -------------------------------------------------------

  async pdfRender(body: { html?: string; url?: string; format?: string; landscape?: boolean; printBackground?: boolean }): Promise<string | null> {
    try {
      const res = await fetch(`${this.baseUrl}/pdf/render`, {
        method: "POST",
        headers: { ...this.authHeaders, "Content-Type": "application/json" },
        body: JSON.stringify(body),
      });
      if (!res.ok) return null;
      // Return a data URL so the RN Image/WebView can render it.
      const blob = await res.blob();
      return await new Promise<string>((resolve, reject) => {
        const r = new FileReader();
        r.onload = () => resolve(String(r.result));
        r.onerror = reject;
        r.readAsDataURL(blob);
      });
    } catch { return null; }
  }

  // ---- Image optimizer (URL helper) ------------------------------------

  imgOptimizeUrl(opts: { src: string; root?: string; w?: number; h?: number; fmt?: string; q?: number }): string {
    const p = new URLSearchParams();
    p.set("src", opts.src);
    if (opts.root) p.set("root", opts.root);
    if (opts.w) p.set("w", String(opts.w));
    if (opts.h) p.set("h", String(opts.h));
    if (opts.fmt) p.set("fmt", opts.fmt);
    if (opts.q) p.set("q", String(opts.q));
    return `${this.baseUrl}/img?${p.toString()}`;
  }

  // ---- Self-hosted OAuth provider admin --------------------------------

  async oauthClients(): Promise<any[]> {
    try {
      const res = await fetch(`${this.baseUrl}/oauth/clients`, { headers: this.authHeaders });
      if (!res.ok) return [];
      return (await res.json()).clients || [];
    } catch { return []; }
  }

  async oauthClientCreate(body: { name: string; redirectUris: string[]; scopes?: string[] }): Promise<{ client_id: string; client_secret: string } | null> {
    try {
      const res = await fetch(`${this.baseUrl}/oauth/clients`, {
        method: "POST",
        headers: { ...this.authHeaders, "Content-Type": "application/json" },
        body: JSON.stringify(body),
      });
      if (!res.ok) return null;
      return await res.json();
    } catch { return null; }
  }

  async oauthUsers(): Promise<any[]> {
    try {
      const res = await fetch(`${this.baseUrl}/oauth/users`, { headers: this.authHeaders });
      if (!res.ok) return [];
      return (await res.json()).users || [];
    } catch { return []; }
  }

  async oauthUserCreate(body: { email: string; password: string; name?: string }): Promise<boolean> {
    try {
      const res = await fetch(`${this.baseUrl}/oauth/users`, {
        method: "POST",
        headers: { ...this.authHeaders, "Content-Type": "application/json" },
        body: JSON.stringify(body),
      });
      return res.ok;
    } catch { return false; }
  }

  // ---- Mail (Gmail / O365) ---------------------------------------------

  async mailInbox(opts: { provider?: "gmail" | "o365"; folder?: string; limit?: number; onlyPersonal?: boolean } = {}): Promise<{ messages: MailMessage[]; counts: Record<string, number> } | null> {
    try {
      const p = new URLSearchParams();
      if (opts.provider) p.set("provider", opts.provider);
      if (opts.folder) p.set("folder", opts.folder);
      if (opts.limit) p.set("limit", String(opts.limit));
      if (opts.onlyPersonal) p.set("onlyPersonal", "true");
      const res = await fetch(`${this.baseUrl}/mail/inbox?${p.toString()}`, { headers: this.authHeaders });
      if (!res.ok) return null;
      return await res.json();
    } catch { return null; }
  }

  async mailDraft(id: string, instructions?: string, provider?: string, execute: boolean = true): Promise<{ prompt: string; target: MailMessage; draft?: string } | null> {
    try {
      const res = await fetch(`${this.baseUrl}/mail/draft`, {
        method: "POST",
        headers: { ...this.authHeaders, "Content-Type": "application/json" },
        body: JSON.stringify({ id, instructions, provider, execute }),
      });
      if (!res.ok) return null;
      return await res.json();
    } catch { return null; }
  }

  async mailSend(body: { to: string[]; subject: string; body?: string; htmlBody?: string }): Promise<boolean> {
    try {
      const res = await fetch(`${this.baseUrl}/mail/send`, {
        method: "POST",
        headers: { ...this.authHeaders, "Content-Type": "application/json" },
        body: JSON.stringify(body),
      });
      return res.ok;
    } catch { return false; }
  }

  async mailConnectStart(provider: "gmail" | "o365"): Promise<{ sessionId: string; authUrl: string } | { error: string } | null> {
    try {
      const res = await fetch(`${this.baseUrl}/mail/onboard/start`, {
        method: "POST",
        headers: { ...this.authHeaders, "Content-Type": "application/json" },
        body: JSON.stringify({ provider }),
      });
      if (!res.ok) {
        const body = await res.json().catch(() => ({}));
        return { error: body?.error || `HTTP ${res.status}` } as any;
      }
      return await res.json();
    } catch (e: any) { return { error: e?.message || "network error" } as any; }
  }

  async mailConnectStatus(sessionId: string): Promise<{ session: any; ready: boolean } | null> {
    try {
      const res = await fetch(`${this.baseUrl}/mail/onboard/status?id=${encodeURIComponent(sessionId)}`, {
        headers: this.authHeaders,
      });
      if (!res.ok) return null;
      return await res.json();
    } catch { return null; }
  }

  // ---- Forms -----------------------------------------------------------

  async formsList(): Promise<any[]> {
    try {
      const res = await fetch(`${this.baseUrl}/forms`, { headers: this.authHeaders });
      if (!res.ok) return [];
      const data = await res.json();
      return data.forms || [];
    } catch { return []; }
  }

  async formCreate(body: { name: string; notifyEmail?: string; honeypotField?: string; rateLimitPerHour?: number }): Promise<any | null> {
    try {
      const res = await fetch(`${this.baseUrl}/forms`, {
        method: "POST",
        headers: { ...this.authHeaders, "Content-Type": "application/json" },
        body: JSON.stringify(body),
      });
      if (!res.ok) return null;
      return (await res.json()).form;
    } catch { return null; }
  }

  async formSubmissions(id: string): Promise<any[]> {
    try {
      const res = await fetch(`${this.baseUrl}/forms/${id}/submissions`, { headers: this.authHeaders });
      if (!res.ok) return [];
      return (await res.json()).submissions || [];
    } catch { return []; }
  }

  // ---- Newsletter -------------------------------------------------------

  async newsletterSubscribers(): Promise<{ subscribers: any[]; count: any } | null> {
    try {
      const res = await fetch(`${this.baseUrl}/newsletter/subscribers`, { headers: this.authHeaders });
      if (!res.ok) return null;
      return await res.json();
    } catch { return null; }
  }

  async newsletterCampaigns(): Promise<any[]> {
    try {
      const res = await fetch(`${this.baseUrl}/newsletter/campaigns`, { headers: this.authHeaders });
      if (!res.ok) return [];
      return (await res.json()).campaigns || [];
    } catch { return []; }
  }

  async newsletterCreate(body: { subject: string; body: string; htmlBody?: string }): Promise<any | null> {
    try {
      const res = await fetch(`${this.baseUrl}/newsletter/campaigns`, {
        method: "POST",
        headers: { ...this.authHeaders, "Content-Type": "application/json" },
        body: JSON.stringify(body),
      });
      if (!res.ok) return null;
      return (await res.json()).campaign;
    } catch { return null; }
  }

  async newsletterSend(id: string): Promise<boolean> {
    try {
      const res = await fetch(`${this.baseUrl}/newsletter/campaigns/${id}/send`, {
        method: "POST",
        headers: this.authHeaders,
      });
      return res.ok;
    } catch { return false; }
  }

  // ---- Job queue --------------------------------------------------------

  async jobsList(): Promise<{ queue: any[]; dlq: any[] } | null> {
    try {
      const res = await fetch(`${this.baseUrl}/jobs`, { headers: this.authHeaders });
      if (!res.ok) return null;
      return await res.json();
    } catch { return null; }
  }

  async jobRetry(id: string): Promise<boolean> {
    try {
      const res = await fetch(`${this.baseUrl}/jobs/${id}/retry`, { method: "POST", headers: this.authHeaders });
      return res.ok;
    } catch { return false; }
  }

  async jobCancel(id: string): Promise<boolean> {
    try {
      const res = await fetch(`${this.baseUrl}/jobs/${id}/cancel`, { method: "POST", headers: this.authHeaders });
      return res.ok;
    } catch { return false; }
  }

  // ---- Project wizard (fullstack generator) -----------------------------

  async wizardStart(): Promise<WizardStartResponse | null> {
    try {
      const res = await fetch(`${this.baseUrl}/project/wizard/start`, {
        method: "POST",
        headers: this.authHeaders,
      });
      if (!res.ok) return null;
      return (await res.json()) as WizardStartResponse;
    } catch {
      return null;
    }
  }

  async wizardAnswer(
    sessionId: string,
    questionId: string,
    answer: string,
  ): Promise<WizardStartResponse | null> {
    try {
      const res = await fetch(`${this.baseUrl}/project/wizard/answer`, {
        method: "POST",
        headers: { ...this.authHeaders, "Content-Type": "application/json" },
        body: JSON.stringify({ sessionId, questionId, answer }),
      });
      if (!res.ok) return null;
      return (await res.json()) as WizardStartResponse;
    } catch {
      return null;
    }
  }

  async wizardGenerate(
    sessionId: string,
    parentDir?: string,
  ): Promise<WizardGenerateResult | null> {
    try {
      const res = await fetch(`${this.baseUrl}/project/wizard/generate`, {
        method: "POST",
        headers: { ...this.authHeaders, "Content-Type": "application/json" },
        body: JSON.stringify({ sessionId, parentDir }),
      });
      if (!res.ok) return null;
      return (await res.json()) as WizardGenerateResult;
    } catch {
      return null;
    }
  }

  async wizardQuestions(): Promise<WizardQuestion[] | null> {
    try {
      const res = await fetch(`${this.baseUrl}/project/wizard/questions`, {
        headers: this.authHeaders,
      });
      if (!res.ok) return null;
      const data = await res.json();
      return Array.isArray(data?.questions) ? (data.questions as WizardQuestion[]) : null;
    } catch {
      return null;
    }
  }

  async analyzeConversationImport(body: {
    url?: string;
    content?: string;
    title?: string;
    runner?: string;
    workDir?: string;
  }): Promise<ConversationImportPlan | null> {
    try {
      const res = await fetch(`${this.baseUrl}/imports/conversation/plan`, {
        method: "POST",
        headers: { ...this.authHeaders, "Content-Type": "application/json" },
        body: JSON.stringify(body),
      });
      if (!res.ok) return null;
      return (await res.json()) as ConversationImportPlan;
    } catch {
      return null;
    }
  }

  // ---- Unauthenticated recovery -----------------------------------------
  //
  // Call this when every authenticated request to the agent returns 401 and
  // the user is outside the LAN. The agent must still be reachable over some
  // transport (Tailscale / Cloudflare Tunnel / yaver relay) — the recovery
  // endpoint is auth-free but the connectivity layer is not. The bootstrap
  // secret is what we're actually trusting here, so we keep it in the mobile
  // keychain (see DeviceContext) rather than over the wire every call.

  /** callOps invokes an agent ops verb (recycle / provision / …) on
   *  the active connection. Mirrors web AgentClient.callOps. The
   *  agent owns every safety guard (recycle's no-self-destruct +
   *  snapshot-before-delete); the app is a thin trigger. Destructive
   *  verbs honour a dry-run: pass payload.confirm=false for the plan. */
  async callOps(
    verb: string,
    payload: Record<string, unknown>,
  ): Promise<{ ok?: boolean; error?: string; code?: string; initial?: any }> {
    if (!this.baseUrl) {
      return { ok: false, error: "no active agent connection — open the device first" };
    }
    try {
      const res = await fetch(`${this.baseUrl}/ops`, {
        method: "POST",
        headers: { ...this.authHeaders, "Content-Type": "application/json" },
        body: JSON.stringify({ verb, payload }),
      });
      const data = await res.json().catch(() => ({}));
      if (!res.ok) return { ok: false, error: data?.error || `ops ${verb} failed: ${res.status}` };
      return data;
    } catch (e: any) {
      return { ok: false, error: e?.message || String(e) };
    }
  }

  /** Factory-reset a remote device's agent auth. Mirrors the web
   *  AgentClient.factoryResetDeviceAuth — agent verifies ownership
   *  via Convex round-trip in its handler (NOT against its local
   *  auth_token), so this works even when the agent has someone
   *  else's session token, which is the case the AUTH/Recover flow
   *  cannot fix. Owner-only on the agent side; guests get 403.
   *
   *  Walks the same relay list connect() does and POSTs to the
   *  first reachable one. The user's bearer is what authenticates
   *  the request — Convex looks up which devices that bearer owns.
   */
  async factoryResetDeviceAuth(
    deviceId: string,
  ): Promise<{ ok: true; via: string } | { ok: false; error: string }> {
    if (!this.token) return { ok: false, error: "not signed in" };
    if (!deviceId) return { ok: false, error: "missing deviceId" };
    const userBearer = this.token;
    const relayList = [...this.relayServers];
    if (relayList.length === 0) return { ok: false, error: "no relay servers configured" };

    let lastError = "no relay reached the device";
    for (const relay of relayList) {
      const url = `${relay.httpUrl}/d/${deviceId}/auth/factory-reset` +
        (relay.password ? `?__rp=${encodeURIComponent(relay.password)}` : "");
      try {
        const res = await this.fetchWithTimeout(url, {
          method: "POST",
          headers: {
            Authorization: `Bearer ${userBearer}`,
            "X-Client-Platform": Platform.OS,
          },
        }, 12000);
        if (res.ok) {
          return { ok: true, via: relay.id || relay.httpUrl };
        }
        // 401/403 — bearer issue or guest. Stop walking; next relay
        // can't change the verdict.
        if (res.status === 401 || res.status === 403) {
          let body = "";
          try { body = (await res.json())?.error || ""; } catch {
            try { body = await res.text(); } catch {}
          }
          return { ok: false, error: `${res.status}: ${body || "forbidden"}` };
        }
        lastError = `${res.status} on relay ${relay.id || relay.httpUrl}`;
      } catch (e: unknown) {
        lastError = e instanceof Error ? e.message : String(e);
        // network error → try next relay
      }
    }
    return { ok: false, error: lastError };
  }

  /** Ask the currently-active agent (watchdog) to SSH-recover another
   *  owned device that's gone dark. Routes through the live baseUrl —
   *  no relay enumeration — because we're addressing the watchdog
   *  itself, not the wedged target. The watchdog runs idempotent
   *  service-restart commands (systemctl / launchctl / nohup) and
   *  returns a one-line outcome. Owner-token auth on the watchdog
   *  side; guests get 403.
   */
  async recoverPeer(
    targetDeviceId: string,
  ): Promise<{ ok: true; outcome: string } | { ok: false; error: string }> {
    if (!this.token) return { ok: false, error: "not signed in" };
    if (!targetDeviceId) return { ok: false, error: "missing target deviceId" };
    if (!this.baseUrl) return { ok: false, error: "no active connection — pick a watchdog device first" };
    try {
      const res = await this.fetchWithTimeout(`${this.baseUrl}/machine/peers/recover`, {
        method: "POST",
        headers: {
          ...this.authHeaders,
          "Content-Type": "application/json",
        },
        body: JSON.stringify({ deviceId: targetDeviceId }),
      }, 130000); // 2-min recovery script + 10s wiggle
      let body: any = null;
      try { body = await res.json(); } catch {}
      if (!res.ok) {
        return { ok: false, error: body?.outcome || body?.error || `HTTP ${res.status}` };
      }
      return { ok: true, outcome: body?.outcome || "ok" };
    } catch (e: unknown) {
      return { ok: false, error: e instanceof Error ? e.message : String(e) };
    }
  }

  /** Ask a remote device's agent to re-detect its hardware profile and push
   *  it to Convex now (bypassing the agent's 24h heartbeat gate). Used by
   *  DeviceDetailsModal when a device row is missing CPU/RAM/etc — typically
   *  because the agent was upgraded from a build that pre-dated the
   *  hardwareProfile feature, so the profile was never sent.
   *
   *  Walks LAN beacon → tunnel → relay to find a working transport. The
   *  agent answers immediately with the freshly detected profile; the
   *  Convex row updates a moment later via the kicked heartbeat, which
   *  is what the modal actually re-renders against. */
  async refreshDeviceHardware(
    deviceId: string,
  ): Promise<{ ok: true; via: string } | { ok: false; error: string }> {
    if (!this.token) return { ok: false, error: "not signed in" };
    if (!deviceId) return { ok: false, error: "missing deviceId" };
    const userBearer = this.token;
    const baseHeaders: Record<string, string> = {
      Authorization: `Bearer ${userBearer}`,
      "X-Client-Platform": Platform.OS,
    };

    const targets: Array<{ url: string; headers: Record<string, string>; label: string }> = [];
    const seen = new Set<string>();
    const push = (url: string, headers: Record<string, string>, label: string) => {
      if (!url || seen.has(url)) return;
      seen.add(url);
      targets.push({ url, headers, label });
    };

    const lanInfo = beaconListener.getLocalIP(deviceId);
    if (lanInfo) {
      push(`http://${lanInfo.ip}:${lanInfo.port}/hardware/refresh`, baseHeaders, `lan ${lanInfo.ip}`);
    }
    if (this.deviceId === deviceId && this.host && this.port) {
      push(`http://${this.host}:${this.port}/hardware/refresh`, baseHeaders, `direct ${this.host}`);
    }
    for (const tunnel of this.effectiveTunnelServers) {
      const headers: Record<string, string> = { ...baseHeaders };
      if (tunnel.cfAccessClientId) {
        headers["CF-Access-Client-Id"] = tunnel.cfAccessClientId;
        headers["CF-Access-Client-Secret"] = tunnel.cfAccessClientSecret || "";
      }
      push(`${tunnel.url.replace(/\/+$/, "")}/hardware/refresh`, headers, `tunnel ${tunnel.url}`);
    }
    for (const relay of this.relayServers) {
      const headers: Record<string, string> = { ...baseHeaders };
      if (relay.password) headers["X-Relay-Password"] = relay.password;
      const url = `${relay.httpUrl}/d/${deviceId}/hardware/refresh` +
        (relay.password ? `?__rp=${encodeURIComponent(relay.password)}` : "");
      push(url, headers, `relay ${relay.id || relay.httpUrl}`);
    }

    if (targets.length === 0) return { ok: false, error: "no transport for device" };

    let lastError = "no transport reached the device";
    for (const t of targets) {
      try {
        const res = await this.fetchWithTimeout(t.url, {
          method: "POST",
          headers: t.headers,
        }, 8000);
        if (res.ok) {
          return { ok: true, via: t.label };
        }
        if (res.status === 401 || res.status === 403) {
          let body = "";
          try { body = (await res.json())?.error || ""; } catch {
            try { body = await res.text(); } catch {}
          }
          return { ok: false, error: `${res.status}: ${body || "forbidden"}` };
        }
        if (res.status === 404) {
          // Agent build is too old to expose /hardware/refresh — no point
          // walking further; every transport hits the same agent. The user
          // needs to upgrade the agent before this endpoint exists.
          return { ok: false, error: "agent build is too old — upgrade with `npm i -g yaver-cli@latest`" };
        }
        lastError = `${res.status} on ${t.label}`;
      } catch (e: unknown) {
        lastError = e instanceof Error ? e.message : String(e);
      }
    }
    return { ok: false, error: lastError };
  }

  /** One-click pair for a device in bootstrap mode. Hits
   *  /auth/pair/owner-claim — agent verifies ownership via
   *  Convex round-trip and splices the bearer into the active
   *  pair session. No URL paste, no passkey, no expiry race.
   *
   *  Tries every transport the device exposes: relay, direct LAN,
   *  Cloudflare/ngrok tunnel, public endpoints. Previous version
   *  was relay-only, which broke reclaim for boxes reachable only
   *  via tunnel or LAN when the relay was degraded — even though
   *  the agent itself was up. Mirror of the web AgentClient's
   *  ownerClaimDevice.
   *
   *  When `opts` carries device-specific transport hints (host,
   *  lanIps, tunnelUrl, publicEndpoints) we try them too. The
   *  caller — typically DeviceContext.recoverDeviceAuth — has a
   *  Device record with those fields; without them we fall back
   *  to relay-only. */
  async ownerClaimDevice(
    deviceId: string,
    opts: {
      host?: string;
      port?: number;
      lanIps?: string[];
      tunnelUrl?: string;
      publicEndpoints?: string[];
    } = {},
  ): Promise<{ ok: true; via: string; host?: string } | { ok: false; error: string }> {
    if (!this.token) return { ok: false, error: "not signed in" };
    if (!deviceId) return { ok: false, error: "missing deviceId" };
    const userBearer = this.token;

    type Target = { url: string; label: string; headers: Record<string, string> };
    const seen = new Set<string>();
    const targets: Target[] = [];
    const push = (url: string | null | undefined, label: string, headers: Record<string, string>) => {
      const normalized = (url || "").replace(/\/+$/, "");
      if (!normalized || seen.has(normalized)) return;
      seen.add(normalized);
      targets.push({ url: normalized, label, headers });
    };

    const baseHeaders: Record<string, string> = {
      Authorization: `Bearer ${userBearer}`,
      "Content-Type": "application/json",
      "X-Client-Platform": Platform.OS,
    };

    // Relay first — works through arbitrary NATs. Most reliable for
    // a remote box.
    for (const relay of this.relayServers) {
      const url = `${relay.httpUrl}/d/${deviceId}/auth/pair/owner-claim` +
        (relay.password ? `?__rp=${encodeURIComponent(relay.password)}` : "");
      push(url, `relay ${relay.id || relay.httpUrl}`, baseHeaders);
    }

    // Direct LAN host + LAN IPs (LAN/home-network reach).
    const port = opts.port || 18080;
    if (opts.host) {
      push(`http://${opts.host}:${port}/auth/pair/owner-claim`, `direct ${opts.host}`, baseHeaders);
    }
    for (const ip of opts.lanIps || []) {
      if (!ip) continue;
      push(`http://${ip}:${port}/auth/pair/owner-claim`, `lan ${ip}`, baseHeaders);
    }

    // Cloudflare/ngrok tunnel and public endpoints (off-LAN reach
    // when relay is degraded).
    if (opts.tunnelUrl) {
      push(`${opts.tunnelUrl.replace(/\/+$/, "")}/auth/pair/owner-claim`, `tunnel ${opts.tunnelUrl}`, baseHeaders);
    }
    for (const endpoint of opts.publicEndpoints || []) {
      if (!endpoint) continue;
      push(`${endpoint.replace(/\/+$/, "")}/auth/pair/owner-claim`, `public ${endpoint}`, baseHeaders);
    }

    if (targets.length === 0) {
      return { ok: false, error: "no transport configured for owner-claim" };
    }

    let lastError = "no transport reached the device";
    for (const target of targets) {
      try {
        const res = await this.fetchWithTimeout(target.url, {
          method: "POST",
          headers: target.headers,
          body: JSON.stringify({}),
        }, 12000);
        if (res.ok) {
          let host: string | undefined;
          try { host = (await res.json())?.host; } catch {}
          return { ok: true, via: target.label, host };
        }
        // The agent reached us, parsed the request, and rejected for a
        // reason that won't change across transports. Fail fast — trying
        // another path against the same agent will return the same code.
        if (res.status === 401 || res.status === 403 || res.status === 409) {
          let body = "";
          try { body = (await res.json())?.error || ""; } catch {
            try { body = await res.text(); } catch {}
          }
          return { ok: false, error: `${res.status}: ${body || "rejected"}` };
        }
        lastError = `${res.status} on ${target.label}`;
      } catch (e: unknown) {
        lastError = e instanceof Error ? e.message : String(e);
      }
    }
    return { ok: false, error: lastError };
  }

  async recoverAgent(
    secret?: string,
    mode: "pair" | "device-code" | "direct" = "pair",
  ): Promise<RecoveryResult | null> {
    // mode=direct hands this client's already-authenticated Bearer to
    // the remote agent as its new token in a single round-trip.
    // Requires the agent to verify the caller as host (same Convex
    // userId owns the device). Used when mobile is signed-in and the
    // remote agent is in auth-expired — skips the pair-session dance
    // entirely. Falls back to pair / device-code if direct is rejected.
    const body = JSON.stringify(secret ? { secret, mode } : { mode });
    let lastError = "network error";
    for (const target of this.recoveryTargets()) {
      try {
        const res = await this.fetchWithTimeout(`${target.baseUrl}/auth/recover`, {
          method: "POST",
          headers: { ...target.headers, "Content-Type": "application/json" },
          body,
        }, 8000);
        if (!res.ok) {
          let message = `HTTP ${res.status}`;
          try {
            const data = await res.json();
            if (typeof data?.error === "string" && data.error.trim()) {
              message = data.error.trim();
            }
          } catch {
            try {
              const text = await res.text();
              if (text.trim()) {
                message = text.trim();
              }
            } catch {}
          }
          // Statuses where the agent has spoken and retrying another
          // target hits the SAME agent through a different path — so
          // the 429/409/403 just repeats and burns through the rate
          // budget on what looks (to the agent) like a flood from one
          // user. Stop iterating; surface the agent's verdict directly.
          //   429 — rate-limited (recoveryLimiter, 5s per IP)
          //   409 — agent-auth healthy, recovery not allowed
          //   403 — host-token check failed / forbidden mode
          if (res.status === 429 || res.status === 409 || res.status === 403) {
            return {
              ok: false,
              error: message,
              rateLimited: res.status === 429,
              alreadyHealthy: res.status === 409,
            } as RecoveryResult;
          }
          lastError = message;
          continue;
        }
        const raw = (await res.json()) as RecoveryResult & {
          recovery_id?: string;
          wait_token?: string;
        };
        // The Go side mixes camelCase (pairCode, deviceCodeUrl, …) and
        // snake_case (recovery_id, wait_token, next_action) in the same
        // /auth/recover response. Normalize the snake_case ones we actually
        // consume so callers don't have to know.
        const data: RecoveryResult = {
          ...raw,
          recoveryId: raw.recoveryId ?? raw.recovery_id,
          waitToken: raw.waitToken ?? raw.wait_token,
        };
        return { ...data, targetUrl: target.baseUrl };
      } catch (e: any) {
        lastError = e?.message ?? "network error";
      }
    }
    return { ok: false, error: lastError } as RecoveryResult;
  }

  /** Poll a recovery session started by recoverAgent. Mirrors
   *  GET /auth/recover/session?id=&wait_token=. Safe to call repeatedly —
   *  this endpoint is NOT subject to the 5s rate limit on /auth/recover.
   *  Returns null when no recovery target was reachable; returns
   *  {ok:false, error} when the agent answered with an error code. */
  async recoverSessionStatus(
    recoveryId: string,
    waitToken: string,
  ): Promise<RecoverySessionStatus | null> {
    if (!recoveryId || !waitToken) {
      return { ok: false, error: "recoveryId and waitToken are required" };
    }
    const qs = `?id=${encodeURIComponent(recoveryId)}&wait_token=${encodeURIComponent(waitToken)}`;
    let lastError = "network error";
    for (const target of this.recoveryTargets()) {
      try {
        const res = await this.fetchWithTimeout(
          `${target.baseUrl}/auth/recover/session${qs}`,
          { method: "GET", headers: target.headers },
          6000,
        );
        if (!res.ok) {
          let message = `HTTP ${res.status}`;
          try {
            const data = await res.json();
            if (typeof data?.error === "string" && data.error.trim()) {
              message = data.error.trim();
            }
          } catch {}
          // 404 = session unknown on this agent. Don't fan out across
          // every transport — the session is bound to the agent that
          // issued it, and another transport hits the same daemon.
          if (res.status === 404) {
            return { ok: false, error: message };
          }
          lastError = message;
          continue;
        }
        const raw = (await res.json()) as {
          ok?: boolean;
          state?: string;
          next_action?: string;
          browser_url?: string;
          user_code?: string;
          pair_code?: string;
          mode?: string;
          expires_at?: string;
          updated_at?: string;
          error?: string;
        };
        return {
          ok: raw.ok !== false,
          state: raw.state,
          nextAction: raw.next_action,
          browserUrl: raw.browser_url,
          userCode: raw.user_code,
          pairCode: raw.pair_code,
          mode: raw.mode,
          expiresAt: raw.expires_at,
          updatedAt: raw.updated_at,
          error: raw.error || undefined,
        };
      } catch (e: any) {
        lastError = e?.message ?? "network error";
      }
    }
    return { ok: false, error: lastError };
  }

  // ---- Log aggregation (E cross-device) ---------------------------------

  async logsSearch(
    opts: { q?: string; level?: string; device?: string; since?: number; limit?: number } = {},
  ): Promise<LogEntry[]> {
    try {
      const params = new URLSearchParams();
      if (opts.q) params.set("q", opts.q);
      if (opts.level) params.set("level", opts.level);
      if (opts.device) params.set("device", opts.device);
      if (opts.since) params.set("since", String(opts.since));
      params.set("limit", String(opts.limit ?? 200));
      const res = await fetch(
        `${this.baseUrl}/logs/search?${params.toString()}`,
        { headers: this.authHeaders },
      );
      if (!res.ok) return [];
      const data = await res.json();
      return data.entries ?? [];
    } catch {
      return [];
    }
  }

  async agentGraphs(): Promise<AgentGraphRun[]> {
    try {
      const res = await fetch(`${this.baseUrl}/agent/graphs`, {
        headers: this.authHeaders,
      });
      if (!res.ok) return [];
      const data = await res.json();
      return data.runs || [];
    } catch {
      return [];
    }
  }

  async createAgentGraph(params: {
    name?: string;
    workDir: string;
    prompt: string;
    runner?: string;
    model?: string;
    template?: "full" | "ship";
    maxParallel?: number;
    preferredDevice?: string;
    allowedDevices?: string[];
    allowedRunners?: string[];
  }): Promise<{ ok: boolean; run?: AgentGraphRun; error?: string }> {
    try {
      const res = await fetch(`${this.baseUrl}/agent/graphs`, {
        method: "POST",
        headers: { ...this.authHeaders, "Content-Type": "application/json" },
        body: JSON.stringify({
          name: params.name ?? "",
          workDir: params.workDir,
          prompt: params.prompt,
          runner: params.runner ?? "",
          model: params.model ?? "",
          template: params.template ?? "full",
          maxParallel: params.maxParallel ?? 2,
          preferredDevice: params.preferredDevice ?? "",
          allowedDevices: params.allowedDevices ?? [],
          allowedRunners: params.allowedRunners ?? [],
        }),
      });
      const data = await res.json().catch(() => ({}));
      if (!res.ok) return { ok: false, error: data?.error || `HTTP ${res.status}` };
      return { ok: true, run: data.run };
    } catch (e: any) {
      return { ok: false, error: e?.message ?? "network error" };
    }
  }

  async stopAgentGraph(id: string): Promise<boolean> {
    try {
      const res = await fetch(`${this.baseUrl}/agent/graphs/${encodeURIComponent(id)}/stop`, {
        method: "POST",
        headers: this.authHeaders,
      });
      return res.ok;
    } catch {
      return false;
    }
  }

  async consoleMachines(): Promise<{ machines: MachineInfo[] }> {
    try {
      const res = await fetch(`${this.baseUrl}/console/machines`, {
        headers: this.authHeaders,
      });
      if (!res.ok) return { machines: [] };
      return res.json();
    } catch {
      return { machines: [] };
    }
  }
}

/** Response shape of GET /testkit/frames — one entry per PNG in a
 *  screencast directory. Frames are absolute paths the player feeds
 *  back into testkitArtifactUrl. */
export interface TestkitFrameList {
  frames: string[];
  fps: number;
  count: number;
}

/** Self-hosted OTA manifest. */
export interface ReleaseManifest {
  channel: string;
  latest?: string;
  rolloutPercent: number;
  releases: ReleaseEntry[];
  updatedAt: string;
}

export interface ReleaseEntry {
  semver: string;
  size: number;
  md5: string;
  hermesBcVersion: number;
  publishedAt: string;
  commit?: string;
  notes?: string;
}

/** Response shape of /releases/latest. */
export interface ReleaseLatest {
  ok: boolean;
  channel: string;
  semver?: string;
  size?: number;
  md5?: string;
  hermesBcVersion?: number;
  publishedAt?: string;
  bundleUrl?: string;
  rolloutPercent: number;
  inRollout: boolean;
  reason?: string;
  previous?: ReleaseEntry;
}

/** Cross-device error aggregation record. */
export interface ErrorRecord {
  fingerprint: string;
  message: string;
  firstFrame?: string;
  stack?: string[];
  firstSeenAt: string;
  lastSeenAt: string;
  count: number;
  deviceIds: string[];
  fatal?: boolean;
  resolved?: boolean;
  resolvedAt?: string;
  resolvedNote?: string;
  recent?: ErrorSample[];
}

export interface ErrorSample {
  deviceId: string;
  timestamp: number;
  message: string;
  route?: string;
  source?: string;
  metadata?: Record<string, string>;
}

export interface ErrorsListResponse {
  ok: boolean;
  errors: ErrorRecord[];
  stats: {
    open: number;
    resolved: number;
    openLast24h: number;
    totalDistinct: number;
  };
}

/** Uptime monitor — one URL check. */
export interface YaverMonitor {
  id: string;
  name?: string;
  url: string;
  interval: string;
  method?: string;
  paused?: boolean;
  state: "up" | "down" | "unknown";
  streak: number;
  history?: MonitorCheck[];
  createdAt: string;
  lastCheckAt?: string;
  checkSsl?: boolean;
  sslWarnDays?: number;
  sslExpiresAt?: string;
  sslDaysLeft?: number;
  sslAlertedAt?: string;
}

export interface MonitorCheck {
  at: string;
  status: number;
  durationMs: number;
  err?: string;
  ok: boolean;
}

/** One track() event persisted in the analytics ledger. */
export interface TrackEvent {
  name: string;
  deviceId?: string;
  timestamp: number;
  route?: string;
  props?: Record<string, string>;
}

/** Machine health snapshot (disk + SMART). */
export interface MachineHealth {
  hostname: string;
  os: string;
  updatedAt: string;
  filesystems: DiskSpaceEntry[];
  drives: SMARTDrive[];
  alerts?: string[];
}

export interface DiskSpaceEntry {
  mount: string;
  totalGb: number;
  usedGb: number;
  freeGb: number;
  usedPct: number;
  device?: string;
  fsType?: string;
  checkedAt: string;
}

export interface SMARTDrive {
  device: string;
  model?: string;
  serial?: string;
  health: "passed" | "failing" | "unknown";
  temperatureC?: number;
  powerOnHours?: number;
  checkedAt: string;
}

/** Peer heartbeat state from /machine/peers. */
export interface PeerState {
  deviceId: string;
  name?: string;
  lastSeen: string;
  observedAt: string;
  state: "online" | "stale" | "offline";
  alertedAt?: string;
  staleSince?: string;
}

/** Cross-device log entry. */
export interface LogEntry {
  deviceId: string;
  level: string;
  message: string;
  source?: string;
  route?: string;
  timestamp: number;
}

/** Mail message shape shared across Gmail / O365 in the agent. */
export interface MailMessage {
  id: string;
  threadId?: string;
  from: string;
  fromName?: string;
  to?: string[];
  cc?: string[];
  subject: string;
  snippet?: string;
  body?: string;
  bodyHtml?: string;
  date: string;
  classification: "personal" | "transactional" | "marketing" | "bulk";
  score: number;
  threadReplies?: number;
  hasUnsubscribe?: boolean;
  provider: "gmail" | "o365";
}

/** One step in the project wizard — the UI renders whichever
 *  control the `kind` field says to render. */
export interface WizardQuestion {
  id: string;
  kind: "text" | "choice" | "bool" | "color" | "confirm" | "done";
  prompt: string;
  help?: string;
  default?: string;
  choices?: string[];
  required?: boolean;
}

export interface WizardSession {
  id: string;
  answers: Record<string, string>;
  done: boolean;
  generatedPath?: string;
}

export interface WizardStartResponse {
  ok: boolean;
  session: WizardSession;
  question: WizardQuestion | null;
}

export interface WizardGenerateResult {
  ok: boolean;
  directory: string;
  files: string[];
  nextSteps: string[];
}

/** Result of POST /auth/recover — agent is unauthenticated but reachable
 *  via Tailscale / Cloudflare Tunnel / yaver relay. */
export interface RecoveryResult {
  ok: boolean;
  mode?: "pair" | "device-code";
  pairCode?: string;
  pairSubmitUrl?: string;
  deviceCodeUrl?: string;
  userCode?: string;
  expiresAt?: string;
  error?: string;
  targetUrl?: string;
  /** Returned by mode=device-code (and pair). Identifier the caller can
   *  use to poll /auth/recover/session for completion without re-hitting
   *  the rate-limited POST endpoint. */
  recoveryId?: string;
  waitToken?: string;
  /** Set when /auth/recover returned 429. Outer recoverDeviceAuth uses
   *  this to bail out instead of falling back to pair / bootstrap-secret /
   *  device-code modes (which all hit the SAME endpoint and would just
   *  re-trigger the same 5s rate limit, producing the user-facing
   *  "too many recovery attempts" alert from one tap). */
  rateLimited?: boolean;
  /** Set when /auth/recover returned 409 (agent-auth healthy). Caller
   *  should treat this as success — no recovery needed. */
  alreadyHealthy?: boolean;
}

/** GET /auth/recover/session?id=&wait_token= response shape. Mirrors
 *  recoverySessionPayload in desktop/agent/auth_recover_session.go. */
export interface RecoverySessionStatus {
  ok: boolean;
  /** started | awaiting_browser_oauth | applying_token | recovered |
   *  expired | failed */
  state?: string;
  nextAction?: string;
  browserUrl?: string;
  userCode?: string;
  pairCode?: string;
  mode?: string;
  expiresAt?: string;
  updatedAt?: string;
  error?: string;
}

/** Feature flag — one entry in the ledger. */
export interface YaverFlag {
  key: string;
  description?: string;
  type: "bool" | "string";
  defaultBool?: boolean;
  defaultString?: string;
  rolloutPercent: number;
  stringVariant?: string;
  overrides?: Record<string, string>;
  updatedAt?: string;
}

export interface TestkitUSBDevice {
  Platform: "ios" | "android";
  UDID: string;
  Name: string;
  OS: string;
}

export interface TestkitIntegration {
  name: string;
  description: string;
  installed: boolean;
  hint: string;
}

export interface TestkitAutoFix {
  id: string;
  state: "applied" | "rolled_back" | "skipped";
  created_at: string;
  undone_at?: string;
  spec_name: string;
  spec_path: string;
  strategy: string;
  description: string;
  notes?: string;
  confidence?: number;
  old_value?: string;
  new_value?: string;
}

export interface TestkitNotification {
  id: string;
  kind: "test_failed" | "test_recovered";
  spec_name: string;
  spec_path: string;
  error?: string;
  screenshot?: string;
  git_sha?: string;
  git_branch?: string;
  created_at: string;
}

export interface TestkitPassMarker {
  sha: string;
  branch?: string;
  passed_at: string;
  host_os: string;
  total: number;
  duration_s: number;
}

export interface TestkitSpec {
  name: string;
  path: string;
  target: "web" | "ios-sim" | "android-emu" | "device";
  url?: string;
  step_count: number;
}

export interface TestkitRunOpts {
  root?: string;
  concurrency?: number;
  retries?: number;
  headful?: boolean;
  update_snapshots?: boolean;
  ac_power_only?: boolean;
  max_load?: number;
}

export interface TestkitRunStatus {
  running: boolean;
  root: string;
  started_at?: string;
  last_suite?: TestkitSuite;
}

export interface TestkitSuite {
  started_at: string;
  finished_at: string;
  duration_ms: number;
  total: number;
  passed: number;
  failed: number;
  results: TestkitSuiteResult[];
}

export interface TestkitSuiteResult {
  name: string;
  path: string;
  target: string;
  passed: boolean;
  duration_ms: number;
  error?: string;
  steps: TestkitSuiteStep[];
}

export interface TestkitSuiteStep {
  index: number;
  phase: string;
  description: string;
  duration_ms: number;
  error?: string;
  screenshot?: string;
}

export interface TestkitHistoryEntry {
  started_at: string;
  finished_at: string;
  duration_ms: number;
  total: number;
  passed: number;
  failed: number;
  flaky_count: number;
  git_sha?: string;
  git_branch?: string;
  host_os: string;
  specs: TestkitHistorySpec[];
}

export interface TestkitHistorySpec {
  name: string;
  path: string;
  target: string;
  passed: boolean;
  flaky?: boolean;
  attempt: number;
  duration_ms: number;
  error?: string;
}

export interface TestkitFlakeStats {
  name: string;
  path: string;
  total: number;
  passed: number;
  failed: number;
  flaky: number;
}

/** Container sandbox status returned by the agent. */
export interface SandboxStatus {
  ok: boolean;
  enabledMode?: "off" | "guests" | "host";
  containerizeGuests: boolean;
  containerizeHost: boolean;
  docker: boolean;
  imageReady: boolean;
  imageName?: string;
  dockerPath?: string;
  gpuAvailable?: boolean;
  networkMode?: string;
  readOnly?: boolean;
  cpuLimit?: string;
  memoryLimit?: string;
  extraMounts?: string[];
  recommendedMode?: "guests" | "host";
  recommendedReason?: string;
  quickstartAvailable?: boolean;
}

/** Container sandbox config fields for updates. */
export interface SandboxConfig {
  containerizeGuests: boolean;
  containerizeHost: boolean;
  cpuLimit: string;
  memoryLimit: string;
  networkMode: "host" | "bridge" | "none";
  readOnly: boolean;
  extraMounts?: string[];
}

/** Dev server status returned by the agent. */
export interface DevServerStatus {
  framework: string;
  running: boolean;
  serving?: boolean;
  servingLabel?: string;
  stopActionLabel?: string;
  building?: boolean;
  port: number;
  bundleUrl: string;
  deepLink?: string;
  devMode?: string;
  startedAt?: string;
  error?: string;
  pid?: number;
  workDir?: string;
  hotReload: boolean;
  targetDeviceId?: string;
  targetDeviceName?: string;
  targetDeviceClass?: string;
  iosInstallMethod?: string;
  iosInstallReason?: string;
}

export interface DevTargetPreference {
  targetDeviceId?: string;
  targetDeviceName?: string;
  targetDeviceClass?: string;
}

export interface DevCompatibilityStatus {
  compatible: boolean;
  missingModules: string[];
  availableModules?: string[];
  warnings?: string[];
  errors?: string[];
  projectReactNative?: string;
  sdkReactNative?: string;
  needsYaverCLI?: boolean;
  needsFeedbackSDK?: boolean;
  recommendedFlow?: string;
  guidance?: string;
  buildState?: "needs_build" | "building" | "ready" | "build_failed";
  canBuildInYaver?: boolean;
  lastBuildAt?: string;
  lastBuildFailedAt?: string;
  lastBuildError?: string;
  compiledBundleSize?: number;
  compiledModuleName?: string;
  packageManager?: string;
  dependenciesInstalled?: boolean;
  needsDependencyInstall?: boolean;
  canAutoInstallDependencies?: boolean;
  missingLocalTools?: string[];
  hermesCompiler?: string;
  hermesCompilerError?: string;
}

export interface MobileWorkerPreviewSession {
  hasTarget: boolean;
  targetDeviceId?: string;
  targetDeviceName?: string;
  targetDeviceClass?: string;
  workerOnline: boolean;
  workerPlatform?: string;
  workerAppName?: string;
  workerStartedAt?: string;
  workerEventCount?: number;
  devServerRunning: boolean;
  framework?: string;
  workDir?: string;
  targetCommandScope?: string;
}

export interface BlackBoxCommandEnvelope {
  type?: string;
  command?: {
    command?: string;
    data?: Record<string, unknown>;
  };
  message?: string;
}

// Fresh-instance factory. Used by the multi-device connection manager
// (`./connectionManager.ts`) so each device the user has signed into
// can keep its own QUIC connection in parallel — the same factory the
// `mobile-headless` harness uses to give each test run an isolated
// auth state.
export function createQuicClient(): QuicClient {
  return new QuicClient();
}

// Stable instance the legacy `quicClient` Proxy falls back to before
// the connection manager has been wired (or after a full sign-out
// when there's nothing focused). Holds the relay servers / token the
// app set during boot — once a device is focused, those settings have
// already been fanned out to every per-device client by
// `connectionManager.setRelayServersOnAll` etc., so the fallback's
// copy is just defensive.
const fallbackQuicClient = new QuicClient();

// The connection manager registers itself here at module load time so
// `quicClient.foo()` resolves to whichever per-device QuicClient is
// currently focused. Late-bound to avoid a circular import — quic.ts
// must not pull connectionManager.ts (which already pulls quic.ts).
let activeClientResolver: () => QuicClient = () => fallbackQuicClient;

/** Wire the legacy `quicClient` Proxy to a per-call resolver. Called
 *  once by the connection manager during its module init. Don't call
 *  this from app code — it's an internal seam. */
export function setQuicClientResolver(fn: () => QuicClient): void {
  activeClientResolver = fn;
}

/** Legacy singleton handle. Retained as a Proxy so the 15+ existing
 *  imports (`import { quicClient } from "./quic"`) keep working
 *  without code churn — every property access is delegated to the
 *  currently-focused QuicClient owned by the connection manager.
 *  New code that genuinely needs to talk to a specific (possibly
 *  non-focused) device should import `connectionManager` and call
 *  `connectionManager.clientFor(deviceId)` instead. */
export const quicClient: QuicClient = new Proxy({} as QuicClient, {
  get(_target, prop) {
    const target = activeClientResolver();
    const value = (target as any)[prop];
    return typeof value === "function" ? value.bind(target) : value;
  },
  set(_target, prop, value) {
    const target = activeClientResolver();
    (target as any)[prop] = value;
    return true;
  },
  has(_target, prop) {
    return prop in (activeClientResolver() as any);
  },
});
