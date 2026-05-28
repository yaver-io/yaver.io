/**
 * Browser-compatible agent client for P2P communication with the desktop agent.
 *
 * Mirrors the mobile QuicClient API but runs in the browser using fetch().
 * Uses HTTP as the transport (same fallback path as mobile).
 * Supports relay-first connection strategy with direct fallback.
 */

import { getYaverCloudBaseUrl } from "@/lib/yaver-cloud";
import { CONVEX_URL } from "@/lib/constants";
import webPkg from "../package.json";

// X-Yaver-Caller surface identifier sent on every agent request.
// Format: "<surface>/<version>" — agent v1.99.71+ logs + threads it
// onto SSE events so the dashboard CONSOLE can attribute each phase
// event back to the originating client.
const YAVER_CALLER_ID = `web-dashboard/${(webPkg as { version?: string }).version ?? "unknown"}`;

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

// ── Types ────────────────────────────────────────────────────────────

export type TaskStatus = "queued" | "running" | "review" | "completed" | "failed" | "stopped";

export interface ConversationTurn {
  role: "user" | "assistant";
  content: string;
  timestamp: string;
}

export interface Task {
  id: string;
  title: string;
  description: string;
  status: TaskStatus;
  runnerId?: string;
  output: string[];
  resultText?: string;
  costUsd?: number;
  turns?: ConversationTurn[];
  createdAt: number;
  updatedAt: number;
  deviceName?: string;
  /** Video summary: when the task was created with videoEnabled, the
   *  agent records a clip after completion. videoClipId is populated
   *  once recording is queued; videoStatus reflects recording state
   *  ("queued" | "recording" | "ready" | "failed" | "stale"). The UI
   *  shows a "▶ Watch demo" button when videoStatus="ready". */
  videoEnabled?: boolean;
  videoSource?: "browser" | "sim-ios" | "sim-android" | "phone";
  videoClipId?: string;
  videoStatus?: "queued" | "recording" | "ready" | "failed" | "stale";
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

export interface SyncItem<T = any> {
  key: string;
  value?: T;
  updatedAt: number;
  updatedBy: string;
  deleted?: boolean;
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

export interface AgentInfo {
  hostname: string;
  version: string;
  workDir: string;
  voiceInputEnabled?: boolean;
  voiceProvider?: string;
  sttProvider?: string;
}

export interface AgentUpdateStatus {
  currentVersion: string;
  latestVersion?: string;
  updateAvailable: boolean;
  autoUpdateEnabled: boolean;
  repo: string;
  updating: boolean;
}

export interface DevTargetPreference {
  targetDeviceId?: string;
  targetDeviceName?: string;
  targetDeviceClass?: string;
}

export type DevServerKind = "web" | "mobile";

/** One app from the monorepo workspace manifest, as returned by /workspace/apps. */
export interface WorkspaceAppView {
  name: string;
  path: string;
  absPath?: string;
  stack?: string;
  kind?: DevServerKind;
  framework?: string;
  depends?: string[];
  env?: string[];
  envMissing?: string[];
  provider?: Record<string, string>;
  exists: boolean;
}

export interface WorkspaceResponse {
  ok: boolean;
  root: string;
  path: string;
  manifest?: unknown;
  apps?: WorkspaceAppView[];
}

/**
 * Per-attempt diagnostic captured during connect(). Lets the dashboard show
 * WHY each relay / direct path failed instead of a single flat error line.
 */
export interface ReauthAttemptDiagnostic {
  path: string;
  step: "direct" | "pair";
  ok: boolean;
  status?: number;
  error?: string;
}

export interface ConnectAttemptDiagnostic {
  path: "relay" | "tunnel" | "direct";
  relayId?: string;
  ok: boolean;
  status?: number;
  authExpired?: boolean;
  error?: string;
  durationMs?: number;
}

export interface DeviceProbeInfo {
  hostname?: string;
  version?: string;
  platform?: string;
  workDir?: string;
  mode?: string;
  autoStart?: string;
  authExpired?: boolean;
  lifecycleState?: "bootstrap" | "yaver-auth-expired" | "ready-to-connect";
  lifecycle?: {
    state?: "bootstrap" | "yaver-auth-expired" | "ready-to-connect";
    usable?: boolean;
    recoverable?: boolean;
    recoveryMode?: string;
    supportsOwnerClaim?: boolean;
    ownerClaimReady?: boolean;
    requiresFirstPair?: boolean;
  };
  runtime?: Record<string, unknown>;
  system?: Record<string, unknown>;
}

export interface DeviceStatusProbe {
  ok: boolean;
  authExpired?: boolean;
  path?: "relay" | "tunnel" | "direct";
  relayId?: string;
  checkedAt: string;
  error?: string;
  diagnostics: ConnectAttemptDiagnostic[];
  info?: DeviceProbeInfo | null;
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
  // deviceDims is populated by the agent on Attach via
  // ProbeDeviceDims (adb shell wm size / xcrun simctl screenshot).
  // The viewer uses these to scale pointer coordinates back to
  // device space so a 4K monitor and a laptop send identical taps.
  deviceDims?: {
    width: number;
    height: number;
    scale?: number;
    rotation?: "portrait" | "landscape";
  };
}

// Vault entries — mirrors VaultEntry / VaultEntrySummary in vault.go.
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

// Yaver Agent (control-plane LLM) types — mirrors yaver_agent_config.go.
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

// Matches ExecSession.Snapshot() in desktop/agent/exec.go.
export interface ExecSnapshot {
  id: string;
  command: string;
  status: "running" | "completed" | "failed";
  stdout: string;
  stderr: string;
  startedAt: string;
  finishedAt?: string;
  exitCode?: number;
  pid?: number;
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

export interface VoiceStatus {
  voiceInputEnabled: boolean;
  s2sProvider?: string;
  s2sReady?: boolean;
  sttProvider?: string;
  sttReady?: boolean;
  providers?: Array<{ id: string; name: string; type: string; ready: boolean }>;
}

export interface ModelInfo {
  id: string;
  name: string;
  description?: string;
  provider?: string;
  source?: string;
  isDefault?: boolean;
}

export interface Runner {
  id: string;
  name: string;
  installed: boolean;
  active: boolean;
  isDefault?: boolean;
  ready?: boolean;
  authConfigured?: boolean;
  authSource?: string;
  warning?: string;
  error?: string;
  supportsBrowserAuth?: boolean;
  supportsModelSelection?: boolean;
  modelSource?: string;
  models?: ModelInfo[];
}

export interface OpenCodeModelSummary {
  id: string;
  name: string;
  provider?: string;
  isDefault?: boolean;
  source?: string;
}

export interface OpenCodeProviderSummary {
  id: string;
  name?: string;
  baseUrl?: string;
  /** True when this provider already has a non-empty `options.apiKey`
   *  set in the agent's opencode.json. The agent never returns the
   *  key value over the wire — only this boolean — so the UI can show
   *  "✓ Key configured · Change" instead of forcing the user to paste
   *  the key every time they pick this provider chip. P2P: comes
   *  straight from /runner/opencode/config, never round-tripped via
   *  Convex. */
  hasApiKey?: boolean;
  models?: OpenCodeModelSummary[];
}

export interface OpenCodeAgentSummary {
  /** "build", "plan", or any custom agent name from opencode.json */
  name: string;
  /** Per-agent model override (e.g. "anthropic/claude-sonnet-4-6"). Empty
   *  means the agent inherits the default model. */
  model?: string;
  description?: string;
  /** True for build + plan; false for user-defined custom agents. */
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
  models?: OpenCodeModelSummary[];
  /** Full list of agent entries — built-ins (build, plan) plus any
   *  custom agents the user has defined under `agent.<name>` in
   *  opencode.json. The chat composer dropdown reads this so custom
   *  agents aren't a hidden CLI-only feature. */
  agents?: OpenCodeAgentSummary[];
  /** Actionable misconfigurations the agent caught — provider with no
   *  baseUrl, model pointing at a missing provider id, etc. UI renders
   *  these as warning banners with fixit hints. */
  diagnostics?: string[];
}

// RunnerBrowserAuthSession is defined below — single source of truth.

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
  /** First line of `<bin> --version` (e.g. "Claude Code 2.1.126",
   *  "codex-cli 0.122.0", "1.4.0"). Populated by agent 1.99.147+. */
  version?: string;
}

export interface RunnerBrowserAuthSession {
  id: string;
  runner: "claude" | "codex";
  method: string;
  status: "starting" | "awaiting_browser" | "completed" | "failed" | "cancelled";
  openUrl?: string;
  code?: string;
  detail?: string;
  authConfigured?: boolean;
  authSource?: string;
  error?: string;
  startedAt: number;
  updatedAt: number;
  completedAt?: number;
}

/**
 * Wire shape for `POST /agent/runners/test`. Mirrors the Go agent's
 * `runnerTestResult` (see desktop/agent/runner_test_http.go). `ok`
 * answers "did this runner just work"; `needsAuth + supportsBrowserAuth`
 * is what the UI uses to auto-pop the headless login flow.
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

export interface GitProviderStatusRow {
  host: string;
  provider: string;
  username: string;
  avatarUrl?: string;
  hasSsh: boolean;
  setupAt: string;
}

export interface GitRemoteRepo {
  id?: string | number;
  name: string;
  fullName: string;
  description?: string;
  private?: boolean;
  language?: string;
  cloneUrl?: string;
  sshUrl?: string;
}

export interface GitCommitRow {
  hash: string;
  shortHash: string;
  message: string;
  author: string;
  date: string;
  filesChanged: number;
}

export interface GitStatusRow {
  branch?: string;
  ahead?: number;
  behind?: number;
  clean?: boolean;
  staged?: Array<{ path: string }>;
  modified?: Array<{ path: string }>;
  untracked?: Array<{ path: string }>;
}

export interface GitActionResult {
  ok?: string;
  hash?: string;
  branch?: string;
  message?: string;
  error?: string;
}

export interface GitBranchRow {
  name: string;
  current: boolean;
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

export interface AgentGraphNode {
  spec: {
    id: string;
    title: string;
    kind: "chat" | "autoideas";
    prompt?: string;
    dependsOn?: string[];
    runner?: string;
    model?: string;
    preferredDevice?: string;
    allowedRunners?: string[];
    workDir?: string;
  };
  status: "pending" | "running" | "completed" | "failed" | "blocked" | "stopped";
  summary?: string;
  error?: string;
  placement?: AgentNodePlacement;
  sliceContract?: TaskSliceContract;
}

export interface AgentGraphRun {
  id: string;
  name: string;
  workDir: string;
  status: "queued" | "running" | "completed" | "failed" | "stopped";
  maxParallel: number;
  summary?: string;
  nodes: AgentGraphNode[];
}

export interface MachineRunnerCapability {
  id: string;
  name: string;
  installed: boolean;
  ready: boolean;
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
  provider?: string;
  currentWorkDir?: string;
  capabilities?: MachineCapabilities;
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
  packageManagers?: string[];
  binaries?: { name: string; path: string; manager?: string }[];
}

export interface TailscaleStatus {
  running: boolean;
  backendState?: string;
  self?: {
    hostName?: string;
    tailAddr?: string;
    tags?: string[];
    addrs?: string[];
  };
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

// Per-target deploy capability — yes/no + structured Reason +
// per-tool / per-secret detail rows. Mirrors the agent's
// DeployCapability struct in desktop/agent/deploy_capabilities.go.
// Used by the dashboard to render disabled buttons with precise
// "missing xcodebuild" / "APP_STORE_KEY_PATH file not found"
// rationale instead of letting the user click and silently fail.
export interface DeployCapabilityTool {
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
}
export interface DeployCapabilitySecret {
  name: string;
  found: boolean;
  source?: string;
  project?: string;
  pathValid?: boolean;
  pathError?: string;
}
export interface DeployCapability {
  target: string;
  stack?: string;
  canDeploy: boolean;
  platformLock?: string;
  tools?: DeployCapabilityTool[];
  secrets?: DeployCapabilitySecret[];
  missingTools?: string[];
  missingSecrets?: string[];
  warnings?: string[];
  reason?: string;
  ciAlternative?: string;
  vaultProject?: string;
}
export interface DeployCapabilitiesReport {
  deviceId: string;
  platform: string;
  arch: string;
  isWsl: boolean;
  targets: DeployCapability[];
}

// Outbound P2P vault sync result. The agent walks the user's
// device list and pulls newer entries from each online peer; the
// dashboard's "Try syncing from peer" button surfaces the per-peer
// counts so the user sees which device contributed which secrets.
export interface VaultPeerSyncResult {
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
}

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

export interface SandboxConfig {
  containerizeGuests?: boolean;
  containerizeHost?: boolean;
  cpuLimit?: string;
  memoryLimit?: string;
  networkMode?: "host" | "bridge" | "none";
  readOnly?: boolean;
  extraMounts?: string[];
}

export type ConnectionState = "disconnected" | "connecting" | "connected" | "error";

export interface RelayServer {
  id: string;
  quicAddr: string;
  httpUrl: string;
  region: string;
  priority: number;
  password?: string;
}

export interface GuestConfigEntry {
  guestUserId: string;
  guestEmail: string;
  guestName: string;
  scope?: "full" | "feedback-only" | "sdk-project";
  dailyTokenLimit?: number;
  allowedRunners?: string[];
  usageMode?: string;
  schedule?: { startHour: number; endHour: number; timezone?: string };
  shareAllDevices?: boolean;
  deviceIds?: string[];
  shareAllMachines?: boolean;
  machineIds?: string[];
  resourcePreset?: string;
  useHostApiKeys?: boolean;
  allowGuestProvidedApiKeys?: boolean;
  allowDesktopControl?: boolean;
  allowBrowserControl?: boolean;
  allowTunnelForward?: boolean;
  requireIsolation?: boolean;
  cpuLimitPercent?: number;
  ramLimitMb?: number;
  priorityMode?: string;
  allowedProjects?: string[];
  allowedSharedStorage?: string[];
}

export interface GuestUsageEntry {
  guestEmail: string;
  guestName: string;
  date: string;
  secondsUsed: number;
}

export interface GuestInfo {
  email: string;
  status: "pending" | "accepted" | "revoked" | "expired";
  fullName?: string;
  createdAt: number;
  expiresAt?: number;
  acceptedAt?: number;
  revokedAt?: number;
}

export type OutputCallback = (taskId: string, line: string) => void;
export type ConnectionStateCallback = (state: ConnectionState) => void;

type EventMap = {
  output: OutputCallback;
  connectionState: ConnectionStateCallback;
};

type EventName = keyof EventMap;

// ── Client ───────────────────────────────────────────────────────────

export class AgentClient {
  private host: string | null = null;
  private port: number | null = null;
  private token: string | null = null;
  private deviceId: string | null = null;
  private relayServers: RelayServer[] = [];
  // Exposed read-only via the activeRelayUrl / activeTunnelUrl
  // getters below — DevicesView.tsx + transport.ts read them so the
  // UI can render "via public.yaver.io v0.1.9" badges.
  private _activeRelayUrl: string | null = null;
  private tunnelCandidates: string[] = [];
  private _activeTunnelUrl: string | null = null;
  get activeRelayUrl(): string | null { return this._activeRelayUrl; }
  get activeTunnelUrl(): string | null { return this._activeTunnelUrl; }
  private _connectionState: ConnectionState = "disconnected";
  private pollInterval: ReturnType<typeof setInterval> | null = null;
  private _lastConnectDiagnostics: ConnectAttemptDiagnostic[] = [];

  // Reconnection
  private reconnectTimer: ReturnType<typeof setTimeout> | null = null;
  private reconnectAttempt = 0;
  private readonly maxReconnectAttempt = 8;
  private readonly baseBackoffMs = 1000;

  // Browser network event listeners
  private onlineHandler: (() => void) | null = null;
  private networkChangeHandler: (() => void) | null = null;

  // Event listeners
  private listeners: { [K in EventName]: Array<EventMap[K]> } = {
    output: [],
    connectionState: [],
  };

  // ── Public getters ─────────────────────────────────────────────────

  get isConnected(): boolean {
    return this._connectionState === "connected";
  }

  get connectionState(): ConnectionState {
    return this._connectionState;
  }

  /** deviceId the client is currently bound to. Lets callers verify
   *  that an action they're about to dispatch (e.g. /agent/update)
   *  will hit the device they think it will, instead of the last
   *  workspace the user happened to open. */
  get connectedDeviceId(): string | null {
    return this.deviceId;
  }

  // ── Relay server config ────────────────────────────────────────────

  /** Set relay servers fetched from platform config. Sorted by priority.
   *  Also persists the per-user relay password to localStorage so other
   *  dashboard surfaces (notably /pair) can read it without going through
   *  the AgentClient instance. The /pair page is on the same origin but a
   *  different React tree; without this it can't see the password and
   *  any `?__rp=` round-trip 401s. */
  setRelayServers(servers: RelayServer[]): void {
    this.relayServers = servers.sort((a, b) => a.priority - b.priority);
    if (typeof window !== "undefined") {
      const userPw = servers.find((s) => s.password)?.password;
      if (userPw) {
        try { window.localStorage.setItem("yaver:userRelayPassword", userPw); } catch { /* quota / private mode */ }
      }
    }
  }

  /** Read-only view of currently configured relay servers. The dashboard
   *  renders the count in diagnostics so the user can tell when the
   *  reason "web can't reach the agent" is "no relay wired up yet". */
  get configuredRelayServers(): ReadonlyArray<RelayServer> {
    return this.relayServers;
  }

  // ── Connection lifecycle ───────────────────────────────────────────

  async connect(
    host: string,
    port: number,
    token: string,
    deviceId?: string,
    opts?: { tunnelUrls?: string[] },
  ): Promise<void> {
    this.host = host;
    this.port = port;
    this.token = token;
    this.deviceId = deviceId ?? null;
    this._activeRelayUrl = null;
    this._activeTunnelUrl = null;
    this.tunnelCandidates = Array.from(new Set((opts?.tunnelUrls || []).map((url) => String(url || "").trim()).filter(Boolean)));
    this.reconnectAttempt = 0;

    this.setupNetworkListeners();
    await this.attemptConnect();
  }

  disconnect(): void {
    this.clearTimers();
    this.teardownNetworkListeners();
    this.setConnectionState("disconnected");
    this.host = null;
    this.port = null;
    this.token = null;
    this.deviceId = null;
    this._activeRelayUrl = null;
    this._activeTunnelUrl = null;
    this.tunnelCandidates = [];
  }

  /**
   * Force an immediate reconnection attempt (e.g. on network change).
   * Resets backoff so the first retry is instant.
   */
  triggerReconnect(): void {
    if (!this.host || !this.port || !this.token) return;
    if (this._connectionState === "connected") {
      // Re-probe: the current path may be dead after a network switch.
      this.clearTimers();
      this.reconnectAttempt = 0;
      this.attemptConnect().catch(() => {});
      return;
    }
    // Cancel any pending backoff timer and reconnect immediately
    if (this.reconnectTimer) {
      clearTimeout(this.reconnectTimer);
      this.reconnectTimer = null;
    }
    this.reconnectAttempt = 0;
    this.attemptConnect().catch(() => {});
  }

  // ── Task API ───────────────────────────────────────────────────────

  async sendTask(title: string, description: string, opts?: { runner?: string; model?: string }): Promise<Task> {
    this.assertConnected();
    const body: Record<string, unknown> = { title, description, source: "web" };
    if (opts?.runner) body.runner = opts.runner;
    if (opts?.model) body.model = opts.model;
    const res = await fetch(`${this.baseUrl}/tasks`, {
      method: "POST",
      headers: { ...this.authHeaders, "Content-Type": "application/json" },
      body: JSON.stringify(body),
    });
    if (!res.ok) {
      throw new Error(await responseErrorMessage(res, `Failed to create task: ${res.status}`));
    }
    const data = await res.json();
    return {
      id: data.taskId,
      title,
      description,
      status: data.status,
      runnerId: data.runnerId || opts?.runner,
      output: [],
      createdAt: Date.now(),
      updatedAt: Date.now(),
    };
  }

  async createTask(params: {
    title: string;
    description: string;
    userPrompt?: string;
    runner?: string;
    model?: string;
    /** Runner-specific subcommand selector. Currently honored by
     *  opencode where it maps to `--agent <mode>` (build / plan /
     *  any custom agent the user has defined in opencode.json).
     *  Other runners ignore it. */
    mode?: string;
    customCommand?: string;
    projectName?: string;
    workDir?: string;
    /** Toggle the post-completion video summary. When true, after
     *  the task finishes the agent records a short MP4 demonstration
     *  via vibe-preview (sim/emulator MP4 for mobile, browser frame
     *  burst for web). Result lands as Task.videoClipId; UI renders a
     *  "▶ Watch demo" button. */
    videoEnabled?: boolean;
    /** Override the auto-detected source: browser | sim-ios | sim-android
     *  | phone. Empty = let the agent infer from workDir. */
    videoSource?: "browser" | "sim-ios" | "sim-android" | "phone" | "";
  }): Promise<Task> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/tasks`, {
      method: "POST",
      headers: { ...this.authHeaders, "Content-Type": "application/json" },
      body: JSON.stringify({
        title: params.title,
        description: params.description,
        userPrompt: params.userPrompt ?? "",
        runner: params.runner ?? "",
        model: params.model ?? "",
        mode: params.mode ?? "",
        customCommand: params.customCommand ?? "",
        projectName: params.projectName ?? "",
        workDir: params.workDir ?? "",
        videoEnabled: params.videoEnabled ?? false,
        videoSource: params.videoSource ?? "",
        source: "web",
      }),
    });
    if (!res.ok) {
      throw new Error(await responseErrorMessage(res, `Failed to create task: ${res.status}`));
    }
    const data = await res.json().catch(() => ({}));
    return this.getTask(data.taskId);
  }

  async listTasks(limit?: number): Promise<Task[]> {
    if (!this.isConnected) {
      return this.getCachedTasks();
    }
    try {
      const url = limit ? `${this.baseUrl}/tasks?limit=${limit}` : `${this.baseUrl}/tasks`;
      const res = await fetch(url, {
        headers: this.authHeaders,
      });
      if (!res.ok) throw new Error(`Failed to list tasks: ${res.status}`);
      const data = await res.json();
      const rawTasks = data.tasks || [];
      const tasks: Task[] = rawTasks.map((t: any) => ({
        id: t.id,
        title: t.title,
        description: t.description,
        status: t.status,
        runnerId: t.runnerId || undefined,
        output: typeof t.output === "string" && t.output
          ? t.output.split("\n").filter((l: string) => l)
          : Array.isArray(t.output) ? t.output : [],
        resultText: t.resultText || undefined,
        costUsd: t.costUsd || undefined,
        turns: t.turns || undefined,
        createdAt: t.createdAt ? new Date(t.createdAt).getTime() : Date.now(),
        updatedAt: t.finishedAt
          ? new Date(t.finishedAt).getTime()
          : t.startedAt
            ? new Date(t.startedAt).getTime()
            : t.createdAt ? new Date(t.createdAt).getTime() : Date.now(),
        deviceName: this.host ?? undefined,
      }));
      this.cacheTasks(tasks);
      return tasks;
    } catch {
      return this.getCachedTasks();
    }
  }

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
      runnerId: t.runnerId || undefined,
      output: typeof t.output === "string" && t.output
        ? t.output.split("\n").filter((l: string) => l)
        : Array.isArray(t.output) ? t.output : [],
      resultText: t.resultText || undefined,
      costUsd: t.costUsd || undefined,
      turns: t.turns || undefined,
      createdAt: t.createdAt ? new Date(t.createdAt).getTime() : Date.now(),
      updatedAt: t.finishedAt
        ? new Date(t.finishedAt).getTime()
        : t.startedAt
          ? new Date(t.startedAt).getTime()
          : t.createdAt ? new Date(t.createdAt).getTime() : Date.now(),
      deviceName: this.host ?? undefined,
    };
  }

  async stopTask(taskId: string): Promise<void> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/tasks/${taskId}/stop`, {
      method: "POST",
      headers: this.authHeaders,
    });
    if (!res.ok) throw new Error(`Failed to stop task: ${res.status}`);
  }

  async continueTask(taskId: string, input: string): Promise<void> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/tasks/${taskId}/continue`, {
      method: "POST",
      headers: { ...this.authHeaders, "Content-Type": "application/json" },
      body: JSON.stringify({ input }),
    });
    if (!res.ok) throw new Error(`Failed to continue task: ${res.status}`);
  }

  async completeTask(taskId: string): Promise<void> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/tasks/${taskId}/complete`, {
      method: "POST",
      headers: this.authHeaders,
    });
    if (!res.ok) throw new Error(`Failed to complete task: ${res.status}`);
  }

  /**
   * Fork an existing task to a different runner/model/mode with bounded
   * recent-context handoff. Use when the user changes the agent picker
   * mid-conversation — this preserves the parent task immutable and
   * spawns a child with the new runner that gets a clipped excerpt of
   * the chat as context. See task_fork.go on the agent side.
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

  async deleteTask(taskId: string): Promise<void> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/tasks/${taskId}`, {
      method: "DELETE",
      headers: this.authHeaders,
    });
    if (!res.ok) throw new Error(`Failed to delete task: ${res.status}`);
  }

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

  // ── Agent Info ────────────────────────────────────────────────────

  async getInfo(): Promise<AgentInfo> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/info`, {
      headers: this.authHeaders,
    });
    if (!res.ok) throw new Error(`Failed to get info: ${res.status}`);
    return res.json();
  }

  /** Classify the connected agent's working directory into one of
   *  mobile / web / backend / generic — used by the workspace route
   *  to pick the right default pane set. Returns generic on any
   *  failure (older agent that lacks the endpoint). */
  async getProjectKind(opts: { dir?: string } = {}): Promise<{
    kind: "mobile" | "web" | "backend" | "generic";
    workDir: string;
    frameworks: string[];
    hasManifest: boolean;
    /** True when this is the silent fallback (404 / fetch error). UI
     *  should render a visible "older agent" indicator instead of
     *  treating it as a real "generic" project. */
    degraded?: boolean;
  }> {
    this.assertConnected();
    try {
      const url = new URL(`${this.baseUrl}/project/kind`);
      if (opts.dir) url.searchParams.set("dir", opts.dir);
      const res = await fetch(url.toString(), { headers: this.authHeaders });
      if (!res.ok) {
        if (res.status === 404) {
          console.warn(`getProjectKind: agent at ${this.baseUrl} missing /project/kind — needs upgrade`);
        }
        throw new Error(`HTTP ${res.status}`);
      }
      const j = await res.json();
      return {
        kind: (j.kind ?? "generic") as "mobile" | "web" | "backend" | "generic",
        workDir: j.workDir ?? "",
        frameworks: Array.isArray(j.frameworks) ? j.frameworks : [],
        hasManifest: !!j.hasManifest,
      };
    } catch {
      return { kind: "generic", workDir: "", frameworks: [], hasManifest: false, degraded: true };
    }
  }

  /**
   * callOps invokes an agent ops verb (provision / destroy / recycle /
   * …) on the *connected* agent. The agent owns all the safety guards
   * (e.g. recycle's no-self-destruct + snapshot-before-delete) — the
   * UI is a thin trigger, never re-implements them. Returns the raw
   * OpsResult ({ ok, error, initial }). Destructive verbs honour a
   * dry-run: call with payload.confirm=false to get the plan back.
   */
  async callOps(
    verb: string,
    payload: Record<string, unknown>,
  ): Promise<{ ok?: boolean; error?: string; code?: string; initial?: any }> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/ops`, {
      method: "POST",
      headers: { ...this.authHeaders, "Content-Type": "application/json" },
      body: JSON.stringify({ verb, payload }),
    });
    const data = await res.json().catch(() => ({}));
    if (!res.ok) throw new Error(data?.error || `ops ${verb} failed: ${res.status}`);
    return data;
  }

  async getAgentUpdateStatus(): Promise<AgentUpdateStatus> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/agent/update`, {
      headers: this.authHeaders,
    });
    if (!res.ok) throw new Error(`Failed to get update status: ${res.status}`);
    return res.json();
  }

  async triggerAgentUpdate(): Promise<{
    ok?: boolean;
    started?: boolean;
    message?: string;
    currentVersion?: string;
    latestVersion?: string;
    updateAvailable?: boolean;
  }> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/agent/update`, {
      method: "POST",
      headers: { ...this.authHeaders, "Content-Type": "application/json" },
      body: "{}",
    });
    const data = await res.json().catch(() => ({}));
    if (!res.ok) throw new Error(data.error || `Failed to trigger update: ${res.status}`);
    return data;
  }

  async getRunners(): Promise<Runner[]> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/agent/runners`, {
      headers: this.authHeaders,
    });
    if (!res.ok) throw new Error(`Failed to get runners: ${res.status}`);
    const data = await res.json();
    return data.runners || [];
  }

  /**
   * Kick off remote browser-style auth for a runner on the connected agent
   * (codex `--device-auth`, claude `auth login --console`). Returns a session
   * id that callers poll via getRunnerBrowserAuthStatus to grab the URL +
   * code the user needs to complete in their browser.
   *
   * Pass `target` to drive the OAuth flow on a peer device the connected
   * agent owns (routes via `/peer/<id>/runner-auth/browser/*`). This is
   * how the dashboard signs the user into claude/codex on a *different*
   * machine than the one the dashboard is connected to — the same code
   * path mobile uses from DeviceDetailsModal.
   */
  async startRunnerBrowserAuth(runner: string, target?: string): Promise<RunnerBrowserAuthSession> {
    this.assertConnected();
    const url = target
      ? `${this.baseUrl}/peer/${encodeURIComponent(target)}/runner-auth/browser/start`
      : `${this.baseUrl}/runner-auth/browser/start`;
    const res = await fetch(url, {
      method: "POST",
      headers: this.authHeaders,
      body: JSON.stringify({ runner }),
    });
    if (!res.ok) {
      const body = await res.text().catch(() => "");
      throw new Error(`startRunnerBrowserAuth(${runner}) ${res.status}: ${body || res.statusText}`);
    }
    const data = await res.json();
    return data.session as RunnerBrowserAuthSession;
  }

  async getRunnerBrowserAuthStatus(sessionId: string, target?: string): Promise<RunnerBrowserAuthSession> {
    this.assertConnected();
    const base = target
      ? `${this.baseUrl}/peer/${encodeURIComponent(target)}/runner-auth/browser/status`
      : `${this.baseUrl}/runner-auth/browser/status`;
    const url = `${base}?id=${encodeURIComponent(sessionId)}`;
    const res = await fetch(url, { headers: this.authHeaders });
    if (!res.ok) {
      const body = await res.text().catch(() => "");
      throw new Error(`getRunnerBrowserAuthStatus ${res.status}: ${body || res.statusText}`);
    }
    const data = await res.json();
    return data.session as RunnerBrowserAuthSession;
  }

  async cancelRunnerBrowserAuth(sessionId: string, target?: string): Promise<void> {
    this.assertConnected();
    const base = target
      ? `${this.baseUrl}/peer/${encodeURIComponent(target)}/runner-auth/browser/cancel`
      : `${this.baseUrl}/runner-auth/browser/cancel`;
    const url = `${base}?id=${encodeURIComponent(sessionId)}`;
    await fetch(url, { method: "POST", headers: this.authHeaders }).catch(() => {});
  }

  /**
   * Forward a user-pasted authentication code to the running CLI's
   * stdin. Used by the Claude device-auth flow where the user signs
   * in on platform.claude.com, copies the long token, and pastes it
   * back here. The agent fire-and-forgets the code into the spawned
   * `claude auth login --console` process; nothing is persisted.
   *
   * Privacy: the code is only ever held in memory on the host (the
   * machine running the spawned CLI), never on Convex, never on the
   * bus, never in any log. Do not call from a context where the
   * caller could be a guest — the agent's authSDK middleware enforces
   * that, but this comment is the second line of defence.
   */
  async submitRunnerBrowserAuthCode(sessionId: string, code: string, target?: string): Promise<RunnerBrowserAuthSession> {
    this.assertConnected();
    const base = target
      ? `${this.baseUrl}/peer/${encodeURIComponent(target)}/runner-auth/browser/submit-code`
      : `${this.baseUrl}/runner-auth/browser/submit-code`;
    const url = `${base}?id=${encodeURIComponent(sessionId)}`;
    const res = await fetch(url, {
      method: "POST",
      headers: { ...this.authHeaders, "Content-Type": "application/json" },
      body: JSON.stringify({ code }),
    });
    const data = await res.json().catch(() => ({}));
    if (!res.ok) {
      throw new Error(data?.error || `submitRunnerBrowserAuthCode ${res.status}`);
    }
    return data.session as RunnerBrowserAuthSession;
  }

  /**
   * Run a small probe through the named runner's CLI on the connected
   * agent and return a structured pass/fail. Used by the device-card
   * "Test" button to answer "is claude actually working on this
   * machine right now" without leaving the dashboard.
   *
   * Return shape matches the Go agent's runnerTestResult — see
   * desktop/agent/runner_test_http.go. `needsAuth + supportsBrowserAuth`
   * are the signal callers use to auto-trigger the headless login flow.
   */
  async testRunner(runner: string, opts?: { prompt?: string; model?: string }): Promise<RunnerTestResult> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/agent/runners/test`, {
      method: "POST",
      headers: this.authHeaders,
      body: JSON.stringify({ runner, prompt: opts?.prompt, model: opts?.model }),
    });
    if (!res.ok) {
      const body = await res.text().catch(() => "");
      throw new Error(`testRunner(${runner}) ${res.status}: ${body || res.statusText}`);
    }
    return (await res.json()) as RunnerTestResult;
  }

  async getToolchainSyncProfile(): Promise<EnvironmentProfile> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/agent/toolchain-sync/profile`, {
      headers: this.authHeaders,
    });
    const data = await res.json().catch(() => ({}));
    if (!res.ok) throw new Error(data?.error || `Failed to get environment profile: ${res.status}`);
    return data.profile as EnvironmentProfile;
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
    if (!res.ok) throw new Error(data?.error || `Failed to apply environment profile: ${res.status}`);
    return data as EnvironmentProfileApplyResult;
  }

  async getEnvironmentProfile(): Promise<EnvironmentProfile> {
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

  /**
   * Fetch the installable catalogue from GET /install/list. When
   * `target` is set, the call is forwarded to a paired peer via
   * /peer/<id>/install/list so the web dashboard can inspect and
   * install onto any machine in the mesh, not just the directly
   * connected one.
   */
  async listInstallables(
    target?: string,
  ): Promise<{ name: string; installed: boolean; description: string }[]> {
    this.assertConnected();
    const base = target
      ? `${this.baseUrl}/peer/${encodeURIComponent(target)}/install/list`
      : `${this.baseUrl}/install/list`;
    const res = await fetch(base, { headers: this.authHeaders });
    if (!res.ok) return [];
    return res.json();
  }

  /**
   * Trigger an install. Returns the SSE stream name to subscribe to
   * with streamLog() for live progress. `target` forwards to a peer.
   */
  async installTool(
    tool: string,
    target?: string,
  ): Promise<{ ok: boolean; tool: string; stream: string; error?: string }> {
    this.assertConnected();
    const base = target
      ? `${this.baseUrl}/peer/${encodeURIComponent(target)}/install/${encodeURIComponent(tool)}`
      : `${this.baseUrl}/install/${encodeURIComponent(tool)}`;
    const res = await fetch(base, { method: "POST", headers: this.authHeaders });
    const data = await res.json().catch(() => ({}));
    if (!res.ok) {
      return { ok: false, tool, stream: "", error: data.error || `HTTP ${res.status}` };
    }
    return { ok: true, tool: data.tool || tool, stream: data.stream || `install:${tool}` };
  }

  /**
   * Install a coding-agent runner (claude / codex / opencode) on
   * the connected agent (or a peer when `target` is set). Thin
   * wrapper around installTool + streamLog so the Devices view can
   * show live progress without each caller re-implementing the SSE
   * subscribe + result-event dance.
   *
   * Returns once the install:<runner> stream emits a terminal
   * `{type:"result", status:"ok"|"error"}` event, or on a network
   * failure starting the request. `onProgress` receives every
   * progress line (including npm output and the agent's own
   * "Starting install: <runner>" header).
   *
   * The same agent endpoint that powers `yaver install <runner>` —
   * /install/<runner> with /peer/<id> proxy for cross-device — so
   * a fresh box (Pi, ARM cloud, mac without brew) gets node
   * auto-provisioned into ~/.yaver/runtimes/node before the
   * `npm install -g` runs. See ensureRunnerInstalledStream in
   * desktop/agent/install_cmd.go.
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
          // Progress lines arrive as {type:"log", text:"…"} or as
          // raw strings (legacy). Forward both.
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

  async runnerAuthStatus(target?: string): Promise<RunnerAuthStatusRow[]> {
    this.assertConnected();
    const base = target
      ? `${this.baseUrl}/peer/${encodeURIComponent(target)}/runner-auth/status`
      : `${this.baseUrl}/runner-auth/status`;
    const res = await fetch(base, { headers: this.authHeaders });
    if (!res.ok) throw new Error(`Failed to get runner auth status: ${res.status}`);
    const data = await res.json().catch(() => ({}));
    return Array.isArray(data?.runners) ? data.runners : [];
  }

  async runnerAuthSet(
    params: RunnerAuthSetParams,
    target?: string,
  ): Promise<{ ok: boolean; saved: string[]; runners: RunnerAuthStatusRow[]; error?: string }> {
    this.assertConnected();
    const base = target
      ? `${this.baseUrl}/peer/${encodeURIComponent(target)}/runner-auth/set`
      : `${this.baseUrl}/runner-auth/set`;
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

  async runnerBrowserAuthStart(
    params: { runner: "claude" | "codex" },
    target?: string,
  ): Promise<{ ok: boolean; session?: RunnerBrowserAuthSession; error?: string }> {
    this.assertConnected();
    const base = target
      ? `${this.baseUrl}/peer/${encodeURIComponent(target)}/runner-auth/browser/start`
      : `${this.baseUrl}/runner-auth/browser/start`;
    const res = await fetch(base, {
      method: "POST",
      headers: { ...this.authHeaders, "Content-Type": "application/json" },
      body: JSON.stringify({ runner: params.runner }),
    });
    const data = await res.json().catch(() => ({}));
    if (!res.ok) return { ok: false, error: data?.error || `HTTP ${res.status}` };
    return { ok: true, session: data?.session };
  }

  async runnerBrowserAuthStatus(
    id: string,
    target?: string,
  ): Promise<{ ok: boolean; session?: RunnerBrowserAuthSession; error?: string }> {
    this.assertConnected();
    const base = target
      ? `${this.baseUrl}/peer/${encodeURIComponent(target)}/runner-auth/browser/status?id=${encodeURIComponent(id)}`
      : `${this.baseUrl}/runner-auth/browser/status?id=${encodeURIComponent(id)}`;
    const res = await fetch(base, { headers: this.authHeaders });
    const data = await res.json().catch(() => ({}));
    if (!res.ok) return { ok: false, error: data?.error || `HTTP ${res.status}` };
    return { ok: true, session: data?.session };
  }

  async runnerBrowserAuthCancel(
    id: string,
    target?: string,
  ): Promise<{ ok: boolean; session?: RunnerBrowserAuthSession; error?: string }> {
    this.assertConnected();
    const base = target
      ? `${this.baseUrl}/peer/${encodeURIComponent(target)}/runner-auth/browser/cancel?id=${encodeURIComponent(id)}`
      : `${this.baseUrl}/runner-auth/browser/cancel?id=${encodeURIComponent(id)}`;
    const res = await fetch(base, { method: "POST", headers: this.authHeaders });
    const data = await res.json().catch(() => ({}));
    if (!res.ok) return { ok: false, error: data?.error || `HTTP ${res.status}` };
    return { ok: true, session: data?.session };
  }

  async openCodeConfig(target?: string): Promise<OpenCodeConfigSummary> {
    this.assertConnected();
    const base = target
      ? `${this.baseUrl}/peer/${encodeURIComponent(target)}/runner/opencode/config`
      : `${this.baseUrl}/runner/opencode/config`;
    const res = await fetch(base, { headers: this.authHeaders });
    const data = await res.json().catch(() => ({}));
    if (!res.ok) {
      throw new Error(data?.error || `openCodeConfig ${res.status}`);
    }
    return (data?.config || {}) as OpenCodeConfigSummary;
  }

  async saveOpenCodeConfig(
    patch: {
      defaultAgent?: string;
      model?: string;
      smallModel?: string;
      buildModel?: string;
      planModel?: string;
      /** Optional provider upserts. Each entry creates or merges a
       *  provider entry in opencode.json. Common case: setting an
       *  Ollama provider's baseUrl to a Tailscale-reachable address.
       *  Pass `delete: true` on an entry to remove it entirely. */
      providers?: Array<{
        id: string;
        name?: string;
        baseUrl?: string;
        apiKey?: string;
        models?: Record<string, unknown>;
        delete?: boolean;
      }>;
    },
    target?: string,
  ): Promise<{ ok: boolean; config?: OpenCodeConfigSummary; error?: string }> {
    this.assertConnected();
    const base = target
      ? `${this.baseUrl}/peer/${encodeURIComponent(target)}/runner/opencode/config`
      : `${this.baseUrl}/runner/opencode/config`;
    const res = await fetch(base, {
      method: "POST",
      headers: { ...this.authHeaders, "Content-Type": "application/json" },
      body: JSON.stringify(patch),
    });
    const data = await res.json().catch(() => ({}));
    if (!res.ok) return { ok: false, error: data?.error || `HTTP ${res.status}` };
    return { ok: true, config: data?.config as OpenCodeConfigSummary };
  }

  async machineOnboardingStatus(target?: string): Promise<MachineOnboardingProviderStatus[]> {
    this.assertConnected();
    const base = target
      ? `${this.baseUrl}/peer/${encodeURIComponent(target)}/machine/onboarding/status`
      : `${this.baseUrl}/machine/onboarding/status`;
    const res = await fetch(base, { headers: this.authHeaders });
    if (!res.ok) throw new Error(`Failed to get machine onboarding status: ${res.status}`);
    const data = await res.json().catch(() => ({}));
    return Array.isArray(data?.providers) ? data.providers : [];
  }

  async machineOnboardingApply(
    params: MachineOnboardingApplyParams,
    target?: string,
  ): Promise<{ ok: boolean; applied: string[]; providers: MachineOnboardingProviderStatus[]; error?: string }> {
    this.assertConnected();
    const base = target
      ? `${this.baseUrl}/peer/${encodeURIComponent(target)}/machine/onboarding/apply`
      : `${this.baseUrl}/machine/onboarding/apply`;
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
    const base = target
      ? `${this.baseUrl}/peer/${encodeURIComponent(target)}/machine/onboarding/remove`
      : `${this.baseUrl}/machine/onboarding/remove`;
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


  async switchRunner(runnerId: string): Promise<void> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/agent/runner/switch`, {
      method: "POST",
      headers: { ...this.authHeaders, "Content-Type": "application/json" },
      body: JSON.stringify({ runner: runnerId }),
    });
    if (!res.ok) throw new Error(`Failed to switch runner: ${res.status}`);
  }

  async agentGraphs(): Promise<AgentGraphRun[]> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/agent/graphs`, {
      headers: this.authHeaders,
    });
    if (!res.ok) throw new Error(`Failed to get agent graphs: ${res.status}`);
    const data = await res.json();
    return data.runs || [];
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
    this.assertConnected();
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
  }

  async stopAgentGraph(id: string): Promise<boolean> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/agent/graphs/${encodeURIComponent(id)}/stop`, {
      method: "POST",
      headers: this.authHeaders,
    });
    return res.ok;
  }

  // ── Voice ────────────────────────────────────────────────────────

  async getVoiceStatus(): Promise<VoiceStatus> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/voice/status`, {
      headers: this.authHeaders,
    });
    if (!res.ok) throw new Error(`Failed to get voice status: ${res.status}`);
    return res.json();
  }

  async transcribeVoice(audioBlob: Blob): Promise<{ ok: boolean; text?: string; provider?: string }> {
    this.assertConnected();
    const formData = new FormData();
    formData.append("audio", audioBlob, "recording.webm");
    const res = await fetch(`${this.baseUrl}/voice/transcribe`, {
      method: "POST",
      headers: this.authHeaders,
      body: formData,
    });
    if (!res.ok) throw new Error(`Transcription failed: ${res.status}`);
    return res.json();
  }

  // ── SSE Task Output Stream ───────────────────────────────────────

  /**
   * Stream task output via SSE.
   *
   * Event types currently emitted by the daemon:
   *   - {type:"output", text} — text chunk; routed to onLine
   *   - {type:"done", status} — terminal status; surfaced to onEvent
   *   - {type:"agent_question", question} — runner is asking the
   *     human via the yaver_ask_user MCP tool. Routed to onEvent.
   *     Reply with answerTaskQuestion(taskId, question.id, answer).
   *   - {type:"agent_answered", questionId, answer} — another device
   *     answered first; close any open sheet.
   *   - {type:"agent_question_cancelled", questionId, reason}
   *
   * Unknown event types are ignored. Old callers that only pass
   * onLine continue to work unchanged.
   */
  streamTaskOutput(
    taskId: string,
    onLine: (line: string) => void,
    onEvent?: (event: { type: string; [k: string]: unknown }) => void,
  ): () => void {
    const controller = new AbortController();
    const url = `${this.baseUrl}/tasks/${taskId}/output`;
    (async () => {
      try {
        const res = await fetch(url, {
          method: "GET",
          headers: { ...this.authHeaders, Accept: "text/event-stream" },
          signal: controller.signal,
        });
        const reader = res.body?.getReader();
        if (!reader) return;
        const decoder = new TextDecoder();
        let buf = "";
        while (true) {
          const { done, value } = await reader.read();
          if (done) break;
          buf += decoder.decode(value, { stream: true });
          let idx: number;
          while ((idx = buf.indexOf("\n\n")) >= 0) {
            const frame = buf.slice(0, idx);
            buf = buf.slice(idx + 2);
            const dataLines = frame
              .split("\n")
              .filter((line) => line.startsWith("data:"))
              .map((line) => line.slice(5).trimStart());
            if (dataLines.length === 0) continue;
            try {
              const event = JSON.parse(dataLines.join("\n"));
              if (event?.type === "output" && event.text) {
                onLine(String(event.text));
              } else if (onEvent) {
                onEvent(event);
              }
            } catch {
              // Ignore malformed frames.
            }
          }
        }
      } catch {
        // Silent best-effort stream; callers usually poll task status too.
      }
    })();
    return () => controller.abort();
  }

  /**
   * POST the human's answer for a pending agent_question. The daemon
   * resolves the parked /tasks/{id}/question handler so the runner's
   * `yaver_ask_user` MCP call returns. Idempotent (a second call with
   * the same questionId returns ok:false).
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
   * GET /tasks/{id}/question — peek the currently-pending question
   * for a task without re-subscribing to SSE. Returns null when none
   * is in flight.
   */
  async getPendingTaskQuestion(taskId: string): Promise<{
    id: string;
    taskId: string;
    prompt: string;
    kind: "text" | "choice" | "secret";
    choices?: string[];
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

  /**
   * Subscribe to a daemon-hosted log stream.
   * Yields one parsed structured event per onEvent call. Backwards-
   * compatible with legacy "line" frames. Returns an abort function.
   *
   * Event shapes (`type`):
   *   yaver_say     {text}
   *   runner_action {runner, tool, detail}
   *   runner_text   {runner, text}
   *   runner_result {runner, status, duration_ms, cost_usd}
   *   line          {text}                    — legacy
   *
   * Uses fetch-based SSE so the auth header survives (unlike
   * EventSource which can't carry custom headers in the browser).
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
        // Caller hooks here when the stream ends for ANY reason except
        // an explicit abort — used by the uninstall flow to detect
        // "agent exited mid-stream" and decide whether to show success
        // (last destructive step landed) or error (dropped early).
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

  /** GET /autoideas/file?work_dir=…&output=… */
  async autoideasFile(
    workDir: string,
    output = "ideas.md",
  ): Promise<{
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
    output?: string;
    engine?: string;
    max_batches?: number;
    tick?: number;
  }): Promise<{ ok: boolean; loop_name?: string; stream_name?: string; error?: string }> {
    const res = await fetch(`${this.baseUrl}/autoideas/start`, {
      method: "POST",
      headers: { ...this.authHeaders, "Content-Type": "application/json" },
      body: JSON.stringify(body),
    });
    return await res.json();
  }

  /** POST /autoideas/select — picks → kick */
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
  }): Promise<{ ok: boolean; loop_name?: string; stream_name?: string; error?: string }> {
    const res = await fetch(`${this.baseUrl}/autoideas/select`, {
      method: "POST",
      headers: { ...this.authHeaders, "Content-Type": "application/json" },
      body: JSON.stringify(body),
    });
    return await res.json();
  }

  // ── EventEmitter ───────────────────────────────────────────────────

  on(event: "output", callback: OutputCallback): () => void;
  on(event: "connectionState", callback: ConnectionStateCallback): () => void;
  on<E extends EventName>(event: E, callback: EventMap[E]): () => void {
    (this.listeners[event] as Array<EventMap[E]>).push(callback);
    return () => {
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      const arr = this.listeners[event] as any[];
      (this.listeners as any)[event] = arr.filter((cb: any) => cb !== callback);
    };
  }

  onOutput(callback: OutputCallback): () => void {
    return this.on("output", callback);
  }

  // ── Private helpers ────────────────────────────────────────────────

  private get baseUrl(): string {
    if (this._activeRelayUrl && this.deviceId) {
      return `${this._activeRelayUrl}/d/${this.deviceId}`;
    }
    if (this._activeTunnelUrl) {
      return this._activeTunnelUrl.replace(/\/+$/, "");
    }
    // Defensive: when host/port haven't been populated (early
    // dashboard render before connect()), template substitution
    // produces "http://null:null" which is a TRUTHY string but a
    // SYNTACTICALLY-VALID-LOOKING URL. Callers like devEventsUrl
    // then build "http://null:null/dev/events" which `new URL()`
    // rejects ("Invalid URL") and `new EventSource()` rejects too
    // ("Failed to construct 'EventSource'"). Returning "" turns
    // every downstream `if (!this.baseUrl) return null` into a
    // proper null and the EventSource never gets constructed.
    if (!this.host || !this.port) return "";
    return `http://${this.host}:${this.port}`;
  }

  private activeRelayPassword: string | null = null;

  private get authHeaders(): Record<string, string> {
    // X-Yaver-Caller as a custom header would trigger a CORS preflight
    // on every request, and neither the relay (≤ v0.1.15) nor the agent
    // (≤ v1.99.71) list X-Yaver-Caller in Access-Control-Allow-Headers
    // yet — so the browser blocks the actual request and the dashboard
    // sees "Load failed" everywhere. Until both sides ship the matching
    // CORS update, we attribute via the ?caller= query param on SSE URLs
    // only (where it's always allowed since EventSource never preflights).
    // Non-SSE requests stay anonymous to the agent's logs for one
    // release; the next agent + relay rev re-enables the header.
    const h: Record<string, string> = {
      Authorization: `Bearer ${this.token}`,
    };
    if (this._activeRelayUrl && this.activeRelayPassword) {
      h["X-Relay-Password"] = this.activeRelayPassword;
    }
    return h;
  }

  /**
   * Fetch an arbitrary agent path with the active auth headers + base
   * URL applied. Use this instead of `(agentClient as any).baseUrl` /
   * `(agentClient as any).authHeaders` from external components — the
   * cast bypasses the class's lifecycle/connection guarantees and
   * breaks on every internal refactor.
   *
   * `path` must start with "/". Returns the raw Response so callers
   * decide how to consume the body.
   */
  async agentFetch(path: string, init: RequestInit = {}): Promise<Response> {
    this.assertConnected();
    const headers = { ...this.authHeaders, ...(init.headers ?? {}) };
    return fetch(`${this.baseUrl}${path}`, { ...init, headers });
  }

  /** Build a URL on the active agent without exposing baseUrl. Useful
   *  for <img src>, <video src>, anchor hrefs, etc., where the asset
   *  is fetched by the browser and not by us — auth headers don't
   *  apply, only the base URL does. */
  agentAssetUrl(path: string): string {
    this.assertConnected();
    return `${this.baseUrl}${path}`;
  }

  private async issueBrowserSession(pathPrefix: string): Promise<string> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/auth/browser-session`, {
      method: "POST",
      headers: { ...this.authHeaders, "Content-Type": "application/json" },
      body: JSON.stringify({ pathPrefix }),
    });
    if (!res.ok) {
      throw new Error(`Failed to issue browser session (${res.status})`);
    }
    const data = await res.json();
    if (!data?.token) {
      throw new Error("Browser session response missing token");
    }
    return data.token;
  }

  private assertConnected(): void {
    if (!this.isConnected) {
      throw new Error("AgentClient is not connected. Call connect() first.");
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
    if (this.reconnectTimer) {
      clearTimeout(this.reconnectTimer);
      this.reconnectTimer = null;
    }
  }

  // ── Browser network event listeners ────────────────────────────────

  private setupNetworkListeners(): void {
    if (typeof window === "undefined") return;

    this.onlineHandler = () => {
      console.log("[AgentClient] Browser came online — triggering reconnect");
      this.triggerReconnect();
    };
    window.addEventListener("online", this.onlineHandler);

    // Network Information API (Chrome/Edge) — detect WiFi/cellular switch
    const nav = navigator as any;
    if (nav.connection) {
      this.networkChangeHandler = () => {
        console.log("[AgentClient] Network change detected — triggering reconnect");
        this.triggerReconnect();
      };
      nav.connection.addEventListener("change", this.networkChangeHandler);
    }
  }

  private teardownNetworkListeners(): void {
    if (typeof window === "undefined") return;

    if (this.onlineHandler) {
      window.removeEventListener("online", this.onlineHandler);
      this.onlineHandler = null;
    }
    if (this.networkChangeHandler) {
      const nav = navigator as any;
      if (nav.connection) {
        nav.connection.removeEventListener("change", this.networkChangeHandler);
      }
      this.networkChangeHandler = null;
    }
  }

  // ── Fetch with timeout ─────────────────────────────────────────────

  private fetchWithTimeout(url: string, opts: RequestInit, timeoutMs: number): Promise<Response> {
    const controller = new AbortController();
    const timer = setTimeout(() => controller.abort(), timeoutMs);
    return fetch(url, { ...opts, signal: controller.signal }).finally(() => clearTimeout(timer));
  }

  // ── Connection + reconnection ──────────────────────────────────────

  /** Diagnostics from the most recent connect() attempt. UI reads this to
   *  explain a failed connection: per-path status codes, authExpired flag
   *  pulled from the agent's unauthenticated /health, and the raw error. */
  get lastConnectDiagnostics(): ConnectAttemptDiagnostic[] {
    return this._lastConnectDiagnostics.slice();
  }

  /**
   * Web-side re-auth of a remote agent whose own Convex session has expired
   * (or just can't be reached at the bearer level). Tries every configured
   * relay in priority order and falls back through two agent contracts:
   *
   *   1. POST /auth/recover {mode:"direct"} with Bearer=<user Convex token>.
   *      New agents (0d44623a+) accept the bearer straight as their new
   *      auth token after /devices/owner-by-hardware confirms ownership.
   *
   *   2. If the agent is older and returns 400 "mode must be 'pair' or
   *      'device-code'", fall back to POST /auth/recover {mode:"pair"}
   *      → take back pairCode → POST /auth/pair/submit?code=<...> with
   *      {token, convexSiteUrl}. Same end result.
   *
   * Also tries the LAN direct path LAST so if the user is actually on the
   * same network we still recover. Each attempt is captured in the returned
   * `diagnostics` array for the UI to render row-by-row.
   */
  async reauthAgent(opts: {
    deviceId: string;
    hostSessionToken: string;
    convexSiteUrl?: string;
  }): Promise<{
    ok: boolean;
    mode?: "direct" | "pair";
    via?: string;
    error?: string;
    diagnostics: ReauthAttemptDiagnostic[];
  }> {
    const diagnostics: ReauthAttemptDiagnostic[] = [];
    const tryOne = async (pathLabel: string, baseUrl: string, password?: string) => {
      const headers: Record<string, string> = {
        Authorization: `Bearer ${opts.hostSessionToken}`,
        "Content-Type": "application/json",
      };
      if (password) headers["X-Relay-Password"] = password;

      // 1. Direct mode
      const directDiag: ReauthAttemptDiagnostic = { path: pathLabel, step: "direct", ok: false };
      try {
        const res = await this.fetchWithTimeout(
          `${baseUrl}/auth/recover`,
          { method: "POST", headers, body: JSON.stringify({ mode: "direct" }) },
          10_000,
        );
        directDiag.status = res.status;
        if (res.ok) {
          directDiag.ok = true;
          diagnostics.push(directDiag);
          return { ok: true as const, mode: "direct" as const, via: pathLabel };
        }
        let body: any = null;
        try { body = await res.clone().json(); } catch {}
        directDiag.error = (body && body.error) || `HTTP ${res.status}`;
        diagnostics.push(directDiag);
        // Only fall through to pair mode for "mode not supported" errors from older agents.
        const msg = String(directDiag.error || "").toLowerCase();
        const modeUnsupported =
          (res.status === 400 || res.status === 501) &&
          (msg.includes("mode must") || msg.includes("direct") || msg.includes("invalid mode"));
        if (!modeUnsupported) {
          return null; // real failure, don't retry via pair
        }
      } catch (e: any) {
        directDiag.error = e?.message || "network error";
        diagnostics.push(directDiag);
        // Transport-level failure, don't try pair on same baseUrl.
        return null;
      }

      // 2. Pair-mode fallback.
      const pairDiag: ReauthAttemptDiagnostic = { path: pathLabel, step: "pair", ok: false };
      try {
        const res = await this.fetchWithTimeout(
          `${baseUrl}/auth/recover`,
          { method: "POST", headers, body: JSON.stringify({ mode: "pair" }) },
          10_000,
        );
        pairDiag.status = res.status;
        if (!res.ok) {
          let body: any = null;
          try { body = await res.clone().json(); } catch {}
          pairDiag.error = (body && body.error) || `HTTP ${res.status}`;
          diagnostics.push(pairDiag);
          return null;
        }
        const pairInfo = await res.json();
        const pairCode = pairInfo?.pairCode;
        if (!pairCode) {
          pairDiag.error = "agent did not return pairCode";
          diagnostics.push(pairDiag);
          return null;
        }

        // Submit the caller's token to /auth/pair/submit?code=PAIRCODE.
        const submitRes = await this.fetchWithTimeout(
          `${baseUrl}/auth/pair/submit?code=${encodeURIComponent(pairCode)}`,
          {
            method: "POST",
            headers: { ...headers, Authorization: headers.Authorization },
            body: JSON.stringify({
              token: opts.hostSessionToken,
              convexSiteUrl: opts.convexSiteUrl || "",
            }),
          },
          10_000,
        );
        pairDiag.status = submitRes.status;
        if (!submitRes.ok) {
          let body: any = null;
          try { body = await submitRes.clone().json(); } catch {}
          pairDiag.error = (body && body.error) || `pair/submit HTTP ${submitRes.status}`;
          diagnostics.push(pairDiag);
          return null;
        }
        pairDiag.ok = true;
        diagnostics.push(pairDiag);
        return { ok: true as const, mode: "pair" as const, via: pathLabel };
      } catch (e: any) {
        pairDiag.error = e?.message || "network error";
        diagnostics.push(pairDiag);
        return null;
      }
    };

    // Relay paths first.
    for (const relay of this.relayServers) {
      const base = `${relay.httpUrl}/d/${opts.deviceId}`;
      const result = await tryOne(`relay · ${relay.id}`, base, relay.password || undefined);
      if (result?.ok) return { ok: true, mode: result.mode, via: result.via, diagnostics };
    }
    // Direct LAN path last (always blocked by HTTPS → HTTP on a web origin).
    // Skip the attempt entirely when on https — the browser logs a noisy
    // mixed-content error and the result is the same. Still try when on
    // http (Electron / dev / yaver://… over HTTP).
    if (
      this.host &&
      this.port &&
      (typeof window === "undefined" || window.location.protocol !== "https:")
    ) {
      const base = `http://${this.host}:${this.port}`;
      const result = await tryOne("direct", base);
      if (result?.ok) return { ok: true, mode: result.mode, via: result.via, diagnostics };
    }

    return {
      ok: false,
      error:
        diagnostics.length === 0
          ? "no relays configured and no direct path"
          : "all transports failed",
      diagnostics,
    };
  }

  /** @deprecated — kept as a thin shim; call reauthAgent instead. */
  async reauthDirect(opts: {
    deviceId: string;
    hostSessionToken: string;
    convexSiteUrl?: string;
  }): Promise<{ ok: true } | { ok: false; status?: number; error: string }> {
    const r = await this.reauthAgent(opts);
    if (r.ok) return { ok: true };
    return { ok: false, error: r.error || "reauth failed" };
  }

  /** Factory-reset a remote device's agent auth. The agent verifies
   *  ownership against Convex (NOT against its local auth_token, which
   *  is the thing being reset), so this works even when the agent's
   *  local token is for a different user — which is exactly the case
   *  the dashboard's regular AUTH/recover flow can't handle.
   *
   *  Only the OWNER of the device per Convex /devices/list can reset.
   *  Guests get 403 server-side (the host has to do it).
   */
  async factoryResetDeviceAuth(
    deviceId: string,
  ): Promise<{ ok: true; via: string } | { ok: false; status?: number; error: string }> {
    if (!this.token) return { ok: false, error: "not signed in" };
    const userBearer = this.token;
    const tryOne = async (
      label: string,
      base: string,
      relayPassword?: string,
    ): Promise<{ ok: true } | null> => {
      const url = `${base}/auth/factory-reset` + (relayPassword ? `?__rp=${encodeURIComponent(relayPassword)}` : "");
      try {
        const res = await this.fetchWithTimeout(url, {
          method: "POST",
          headers: { Authorization: `Bearer ${userBearer}` },
        }, 12000);
        if (res.ok) return { ok: true };
        // 401/403 — bearer issue or guest. Don't keep retrying, the
        // next relay won't change the verdict.
        if (res.status === 401 || res.status === 403) {
          const body = await res.text().catch(() => "");
          throw new Error(`${label}: HTTP ${res.status} ${body.slice(0, 120)}`);
        }
        return null;
      } catch (e: unknown) {
        const msg = e instanceof Error ? e.message : String(e);
        if (msg.startsWith("relay") || msg.startsWith("direct") || msg.startsWith("tunnel")) throw e;
        return null;
      }
    };

    // Walk the same relay list connect() does. We don't require
    // self.deviceId to match — caller can reset any device they own.
    // Relay list is populated via setRelayServers() at app startup;
    // if it's empty here something earlier in the connect flow
    // never ran and the dashboard would already be in a bad state.
    if (this.relayServers.length === 0) {
      return { ok: false, error: "no relay servers configured" };
    }
    try {
      for (const relay of this.relayServers) {
        const base = `${relay.httpUrl}/d/${deviceId}`;
        const r = await tryOne(`relay · ${relay.id}`, base, relay.password || undefined);
        if (r?.ok) return { ok: true, via: relay.id };
      }
    } catch (e: unknown) {
      return { ok: false, error: e instanceof Error ? e.message : String(e) };
    }
    return { ok: false, error: "no relay path reached the device" };
  }

  /** One-click pair for a device in bootstrap mode. Hits the
   *  agent's /auth/pair/owner-claim with the user's bearer; the
   *  agent verifies ownership via Convex round-trip and splices
   *  the bearer into the active pair session. No URL composition,
   *  no passkey copy-paste, no expiry races on the user.
   *
   *  Tries relays first (most reliable for off-LAN reach), then
   *  any device-specific transport hints the caller passes —
   *  direct host, tunnelUrl, publicEndpoints. The previous
   *  relay-only version broke reclaim for boxes reachable only
   *  via Cloudflare tunnel or LAN when the relay was degraded,
   *  even though the agent itself was up.
   *
   *  Use case: user clicks "Pair Device" / "Reclaim" on a card
   *  whose state shows bootstrap or needsAuth=true.
   */
  async ownerClaimDevice(
    deviceId: string,
    opts: {
      host?: string;
      port?: number;
      lanIps?: string[];
      tunnelUrl?: string;
      publicEndpoints?: string[];
    } = {},
  ): Promise<{ ok: true; via: string; host?: string } | { ok: false; status?: number; error: string }> {
    if (!this.token) return { ok: false, error: "not signed in" };
    const userBearer = this.token;

    type Target = { url: string; label: string };
    const seen = new Set<string>();
    const targets: Target[] = [];
    const push = (url: string | null | undefined, label: string) => {
      const normalized = (url || "").replace(/\/+$/, "");
      if (!normalized || seen.has(normalized)) return;
      seen.add(normalized);
      targets.push({ url: normalized, label });
    };

    // Relay first.
    for (const relay of this.relayServers) {
      const url = `${relay.httpUrl}/d/${deviceId}/auth/pair/owner-claim`
        + (relay.password ? `?__rp=${encodeURIComponent(relay.password)}` : "");
      push(url, `relay ${relay.id || relay.httpUrl}`);
    }
    // Direct host + LAN IPs.
    const port = opts.port || 18080;
    if (opts.host) {
      push(`http://${opts.host}:${port}/auth/pair/owner-claim`, `direct ${opts.host}`);
    }
    for (const ip of opts.lanIps || []) {
      if (!ip) continue;
      push(`http://${ip}:${port}/auth/pair/owner-claim`, `lan ${ip}`);
    }
    // Tunnel + public endpoints.
    if (opts.tunnelUrl) {
      push(`${opts.tunnelUrl.replace(/\/+$/, "")}/auth/pair/owner-claim`, `tunnel ${opts.tunnelUrl}`);
    }
    for (const endpoint of opts.publicEndpoints || []) {
      if (!endpoint) continue;
      push(`${endpoint.replace(/\/+$/, "")}/auth/pair/owner-claim`, `public ${endpoint}`);
    }

    if (targets.length === 0) {
      return { ok: false, error: "no transport configured for owner-claim" };
    }

    let lastError = "no transport reached the device";
    let lastStatus: number | undefined;
    for (const target of targets) {
      try {
        const res = await this.fetchWithTimeout(target.url, {
          method: "POST",
          headers: {
            Authorization: `Bearer ${userBearer}`,
            "Content-Type": "application/json",
          },
          body: JSON.stringify({}),
        }, 12000);
        if (res.ok) {
          const data = await res.json().catch(() => ({} as Record<string, unknown>));
          return {
            ok: true,
            via: target.label,
            host: typeof data.host === "string" ? data.host : undefined,
          };
        }
        // Terminal: agent reached us, parsed, rejected. Won't change
        // across transports.
        if (res.status === 401 || res.status === 403 || res.status === 409) {
          const text = await res.text().catch(() => "");
          return { ok: false, status: res.status, error: text.slice(0, 200) || `HTTP ${res.status}` };
        }
        lastError = `HTTP ${res.status} on ${target.label}`;
        lastStatus = res.status;
      } catch (e: unknown) {
        lastError = e instanceof Error ? e.message : String(e);
      }
    }
    return { ok: false, status: lastStatus, error: lastError };
  }

  private async probeHealth(
    url: string,
    headers: Record<string, string>,
    timeoutMs: number,
    path: "relay" | "tunnel" | "direct",
    relayId?: string,
  ): Promise<ConnectAttemptDiagnostic> {
    const started = Date.now();
    // Browsers block fetch from https:// origins to http:// targets
    // (mixed content). Don't even try — the browser logs a noisy
    // error and the result is the same. Return a clean diagnostic
    // so the caller still records that we considered this path.
    if (
      typeof window !== "undefined" &&
      window.location.protocol === "https:" &&
      /^http:\/\//i.test(url)
    ) {
      return {
        path,
        relayId,
        ok: false,
        error: "blocked: browser refuses http:// from https:// origin",
        durationMs: Date.now() - started,
      };
    }
    try {
      const res = await this.fetchWithTimeout(`${url}/health`, { headers }, timeoutMs);
      const diag: ConnectAttemptDiagnostic = {
        path,
        relayId,
        ok: res.ok,
        status: res.status,
        durationMs: Date.now() - started,
      };
      // /health is unauthenticated on the agent. When the agent's OWN Convex
      // session has gone stale it sets authExpired:true in the body — that's
      // how we tell "box is up but needs `yaver auth`" from "box offline".
      try {
        const body = await res.clone().json();
        if (body && typeof body === "object" && body.authExpired === true) {
          diag.authExpired = true;
        }
      } catch {}
      if (!res.ok && !diag.error) {
        diag.error = await responseErrorMessage(res, `HTTP ${res.status}`);
      }
      return diag;
    } catch (e: any) {
      return {
        path,
        relayId,
        ok: false,
        error: e?.name === "AbortError" ? "timeout" : e?.message || "network error",
        durationMs: Date.now() - started,
      };
    }
  }

  private async probeInfoAt(
    url: string,
    headers: Record<string, string>,
    timeoutMs: number,
  ): Promise<DeviceProbeInfo | null> {
    if (
      typeof window !== "undefined" &&
      window.location.protocol === "https:" &&
      /^http:\/\//i.test(url)
    ) {
      // Mixed content — would be blocked. See probeHealth.
      return null;
    }
    try {
      const res = await this.fetchWithTimeout(`${url}/info`, { headers }, timeoutMs);
      if (!res.ok) return null;
      const data = await res.json().catch(() => null);
      if (!data || typeof data !== "object") return null;
      return data as DeviceProbeInfo;
    } catch {
      return null;
    }
  }

  async probeDeviceStatus(opts: {
    host: string;
    port: number;
    token: string;
    deviceId?: string;
    tunnelUrls?: string[];
  }): Promise<DeviceStatusProbe> {
    const diagnostics: ConnectAttemptDiagnostic[] = [];
    const checkedAt = new Date().toISOString();
    const baseHeaders: Record<string, string> = { Authorization: `Bearer ${opts.token}` };

    if (opts.deviceId && this.relayServers.length > 0) {
      for (const relay of this.relayServers) {
        const relayHeaders: Record<string, string> = { ...baseHeaders };
        if (relay.password) relayHeaders["X-Relay-Password"] = relay.password;
        const relayDeviceUrl = `${relay.httpUrl}/d/${opts.deviceId}`;
        const diag = await this.probeHealth(relayDeviceUrl, relayHeaders, 8000, "relay", relay.id);
        diagnostics.push(diag);
        if (diag.ok) {
          const info = await this.probeInfoAt(relayDeviceUrl, relayHeaders, 8000);
          return {
            ok: true,
            path: "relay",
            relayId: relay.id,
            checkedAt,
            diagnostics,
            info,
          };
        }
      }
    }

    for (const tunnelUrl of (opts.tunnelUrls || [])
      .map((u) => String(u || "").trim())
      .filter(Boolean)
      // Skip <id>.dev.yaver.io URLs while the wildcard cert isn't
      // wired (Cloudflare universal SSL only covers *.yaver.io,
      // one level deep). Probing them fails at TLS handshake and
      // floods the console with mixed-content / "access control
      // checks" errors. See web 1.1.72 + the dev-yaver-io comment
      // in DevicesView.tsx::isUsablePublicEndpoint.
      .filter((u) => !/^https?:\/\/[^/]+\.dev\.yaver\.io(\/|$)/i.test(u))) {
      const normalized = tunnelUrl.replace(/\/+$/, "");
      const diag = await this.probeHealth(normalized, baseHeaders, 8000, "tunnel");
      diagnostics.push(diag);
      if (diag.ok) {
        const info = await this.probeInfoAt(normalized, baseHeaders, 8000);
        return {
          ok: true,
          path: "tunnel",
          checkedAt,
          diagnostics,
          info,
        };
      }
    }

    const directUrl = `http://${opts.host}:${opts.port}`;
    const directDiag = await this.probeHealth(directUrl, baseHeaders, 5000, "direct");
    diagnostics.push(directDiag);
    if (directDiag.ok) {
      const info = await this.probeInfoAt(directUrl, baseHeaders, 5000);
      return {
        ok: true,
        path: "direct",
        checkedAt,
        diagnostics,
        info,
      };
    }

    if (diagnostics.some((d) => d.authExpired)) {
      return {
        ok: false,
        authExpired: true,
        checkedAt,
        error: "Agent reached, but its session is expired",
        diagnostics,
      };
    }

    return {
      ok: false,
      checkedAt,
      error: diagnostics.find((d) => d.error)?.error || "Could not reach agent",
      diagnostics,
    };
  }

  private async attemptConnect(): Promise<void> {
    this.setConnectionState("connecting");
    this._activeRelayUrl = null;
    this._activeTunnelUrl = null;
    const diagnostics: ConnectAttemptDiagnostic[] = [];
    try {
      let connected = false;

      // Strategy: relay-first (more reliable across networks),
      // with direct fallback for same-network connections.

      // 1. Try relay servers first (when deviceId and relays are available)
      if (this.deviceId && this.relayServers.length > 0) {
        for (const relay of this.relayServers) {
          const relayDeviceUrl = `${relay.httpUrl}/d/${this.deviceId}`;
          const relayHeaders: Record<string, string> = { ...this.authHeaders };
          if (relay.password) relayHeaders["X-Relay-Password"] = relay.password;
          const diag = await this.probeHealth(relayDeviceUrl, relayHeaders, 8000, "relay", relay.id);
          diagnostics.push(diag);
          if (diag.ok) {
            this._activeRelayUrl = relay.httpUrl;
            this.activeRelayPassword = relay.password || null;
            connected = true;
            console.log("[AgentClient] Relay connection succeeded via", relay.id);
            break;
          }
          console.log("[AgentClient] Relay", relay.id, "failed:", diag.error || diag.status);
        }
      }

      // 2. Try direct connection as fallback
      if (!connected && this.tunnelCandidates.length > 0) {
        for (const tunnelUrl of this.tunnelCandidates) {
          const diag = await this.probeHealth(
            tunnelUrl.replace(/\/+$/, ""),
            this.authHeaders,
            8000,
            "tunnel",
          );
          diagnostics.push(diag);
          if (diag.ok) {
            this._activeRelayUrl = null;
            this.activeRelayPassword = null;
            this._activeTunnelUrl = tunnelUrl.replace(/\/+$/, "");
            connected = true;
            console.log("[AgentClient] Tunnel connection succeeded via", tunnelUrl);
            break;
          }
          console.log("[AgentClient] Tunnel", tunnelUrl, "failed:", diag.error || diag.status);
        }
      }

      // 3. Try direct connection as fallback
      if (!connected) {
        const directUrl = `http://${this.host}:${this.port}`;
        const diag = await this.probeHealth(directUrl, this.authHeaders, 5000, "direct");
        diagnostics.push(diag);
        if (diag.ok) {
          this._activeRelayUrl = null;
          this.activeRelayPassword = null;
          this._activeTunnelUrl = null;
          connected = true;
          console.log("[AgentClient] Direct connection succeeded");
        } else {
          console.log("[AgentClient] Direct failed:", diag.error || diag.status);
        }
      }

      this._lastConnectDiagnostics = diagnostics;

      if (!connected) {
        // Pick the most informative error: prefer "auth expired" over raw transport
        // errors so the UI can guide the user to `yaver auth` on the box.
        const authExpired = diagnostics.some((d) => d.authExpired);
        if (authExpired) {
          throw new Error("Agent reached, but its Convex session is expired — run `yaver auth` on the remote device");
        }
        const relayLimit = diagnostics.find((d) => d.status === 429 || d.status === 413 || d.status === 503);
        if (relayLimit?.error) {
          throw new Error(relayLimit.error);
        }
        throw new Error("Could not reach agent (direct, tunnel, or relay)");
      }

      this.reconnectAttempt = 0;
      this.setConnectionState("connected");
      this.startPolling();
    } catch (err) {
      this._lastConnectDiagnostics = diagnostics;
      this.setConnectionState("error");
      this.scheduleReconnect();
      if (this.reconnectAttempt === 0) {
        this.reconnectAttempt = 1;
        throw err;
      }
    }
  }

  private scheduleReconnect(): void {
    if (!this.host || !this.port || !this.token) return;
    if (this.reconnectAttempt >= this.maxReconnectAttempt) return;

    const delay = Math.min(
      this.baseBackoffMs * Math.pow(2, this.reconnectAttempt),
      30_000,
    );
    this.reconnectAttempt++;

    this.reconnectTimer = setTimeout(() => {
      this.reconnectTimer = null;
      this.attemptConnect().catch(() => {
        // Reconnection failure is handled inside attemptConnect.
      });
    }, delay);
  }

  private startPolling(): void {
    if (this.pollInterval) return;
    // Track how much of each task's output we've already emitted as complete
    // lines. We can't key on length alone because a poll can land mid-line —
    // if we emit the partial head now, the rest of the line arrives on the
    // next poll and gets emitted as its own "line", chopping words in half
    // (classic "Workspace/carrotbet" → "Wor" + "kspace/carrotbet" bug).
    // Instead, only emit up to the last '\n' and remember where we stopped.
    const emittedUpTo = new Map<string, number>();

    this.pollInterval = setInterval(async () => {
      try {
        // Only fetch recent tasks (limit=5) to keep payload small through relay
        const res = await fetch(`${this.baseUrl}/tasks?limit=5`, {
          headers: this.authHeaders,
        });
        if (!res.ok) return;
        const data = await res.json();
        const rawTasks = data.tasks || [];
        for (const t of rawTasks) {
          if (t.status !== "running" && t.status !== "completed") continue;
          const output = typeof t.output === "string" ? t.output : "";
          const prev = emittedUpTo.get(t.id) || 0;
          if (output.length <= prev) continue;
          const tail = output.slice(prev);
          // For a completed task we can safely flush everything (including any
          // final line without a trailing newline). For a running task we only
          // flush up to the last newline; the partial tail waits for more data.
          let flush: string;
          let advance: number;
          if (t.status === "completed") {
            flush = tail;
            advance = output.length;
          } else {
            const lastNl = tail.lastIndexOf("\n");
            if (lastNl < 0) continue;           // nothing complete yet
            flush = tail.slice(0, lastNl);      // without the trailing \n
            advance = prev + lastNl + 1;        // consume through the \n
          }
          const lines = flush.split("\n").filter((l: string) => l);
          for (const line of lines) {
            this.emit("output", t.id, line);
          }
          emittedUpTo.set(t.id, advance);
        }
      } catch {
        this.setConnectionState("error");
        this.clearTimers();
        this.scheduleReconnect();
      }
    }, 3000);
  }

  // ── Guest config ──────────────────────────────────────────────────

  async getGuestConfigs(): Promise<GuestConfigEntry[]> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/guests/config`, {
      headers: this.authHeaders,
    });
    if (!res.ok) return [];
    const data = await res.json();
    return data.configs || [];
  }

  async updateGuestConfig(config: {
    email: string;
    scope?: "full" | "feedback-only" | "sdk-project";
    dailyTokenLimit?: number;
    allowedRunners?: string[];
    usageMode?: string;
    schedule?: { startHour: number; endHour: number; timezone?: string };
    shareAllDevices?: boolean;
    deviceIds?: string[];
    shareAllMachines?: boolean;
    machineIds?: string[];
    resourcePreset?: string;
    useHostApiKeys?: boolean;
    allowGuestProvidedApiKeys?: boolean;
    allowDesktopControl?: boolean;
    allowBrowserControl?: boolean;
    allowTunnelForward?: boolean;
    requireIsolation?: boolean;
    cpuLimitPercent?: number;
    ramLimitMb?: number;
    priorityMode?: string;
    allowedProjects?: string[];
    allowedSharedStorage?: string[];
  }): Promise<void> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/guests/config`, {
      method: "POST",
      headers: { ...this.authHeaders, "Content-Type": "application/json" },
      body: JSON.stringify(config),
    });
    if (!res.ok) {
      const data = await res.json().catch(() => ({}));
      throw new Error(data.error || "Failed to update config");
    }
  }

  async getGuestUsage(date?: string): Promise<GuestUsageEntry[]> {
    this.assertConnected();
    const url = date
      ? `${this.baseUrl}/guests/usage?date=${encodeURIComponent(date)}`
      : `${this.baseUrl}/guests/usage`;
    const res = await fetch(url, { headers: this.authHeaders });
    if (!res.ok) return [];
    const data = await res.json();
    return data.usage || [];
  }

  async getGuestList(): Promise<GuestInfo[]> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/guests`, {
      headers: this.authHeaders,
    });
    if (!res.ok) return [];
    const data = await res.json();
    return data.guests || [];
  }

  async inviteGuest(target: {
    email?: string;
    userId?: string;
    deviceIds?: string[];
    scope?: "full" | "feedback-only" | "sdk-project";
    allowedProjects?: string[];
  } | string): Promise<{ inviteCode: string; guestRegistered: boolean }> {
    this.assertConnected();
    const body =
      typeof target === "string"
        ? { email: target }
        : {
            email: target.email,
            userId: target.userId,
            deviceIds: target.deviceIds,
            scope: target.scope,
            allowedProjects: target.allowedProjects,
          };
    const res = await fetch(`${this.baseUrl}/guests/invite`, {
      method: "POST",
      headers: { ...this.authHeaders, "Content-Type": "application/json" },
      body: JSON.stringify(body),
    });
    if (!res.ok) {
      const data = await res.json().catch(() => ({}));
      throw new Error(data.error || "Failed to invite");
    }
    return res.json();
  }

  async revokeGuest(email: string): Promise<void> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/guests/revoke`, {
      method: "POST",
      headers: { ...this.authHeaders, "Content-Type": "application/json" },
      body: JSON.stringify({ email }),
    });
    if (!res.ok) {
      const data = await res.json().catch(() => ({}));
      throw new Error(data.error || "Failed to revoke");
    }
  }

  // ── Container Sandbox ──────────────────────────────────────────────

  async getSandboxStatus(): Promise<SandboxStatus | null> {
    this.assertConnected();
    try {
      const res = await fetch(`${this.baseUrl}/sandbox/status`, {
        headers: this.authHeaders,
      });
      if (!res.ok) return null;
      return await res.json();
    } catch { return null; }
  }

  async updateSandboxConfig(config: SandboxConfig): Promise<void> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/sandbox/config`, {
      method: "POST",
      headers: { ...this.authHeaders, "Content-Type": "application/json" },
      body: JSON.stringify(config),
    });
    if (!res.ok) {
      const data = await res.json().catch(() => ({}));
      throw new Error(data.error || "Failed to update sandbox config");
    }
  }

  async buildSandboxImage(): Promise<void> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/sandbox/build`, {
      method: "POST",
      headers: this.authHeaders,
    });
    if (!res.ok) {
      const data = await res.json().catch(() => ({}));
      throw new Error(data.error || "Failed to start sandbox build");
    }
  }

  async sandboxQuickstart(mode: "guests" | "host", buildImage = true): Promise<{ ok: boolean; message?: string; sandbox?: SandboxStatus; error?: string }> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/sandbox/quickstart`, {
      method: "POST",
      headers: { ...this.authHeaders, "Content-Type": "application/json" },
      body: JSON.stringify({ mode, buildImage }),
    });
    const data = await res.json().catch(() => ({}));
    if (!res.ok) return { ok: false, error: data?.error || `HTTP ${res.status}` };
    return { ok: true, message: data?.message, sandbox: data?.sandbox };
  }

  // ── Projects ───────────────────────────────────────────────────────

  async listProjects(): Promise<{ name: string; path: string; branch?: string; framework?: string; executionMode?: string; primarySurface?: string; tags?: string[] }[]> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/projects`, { headers: this.authHeaders });
    const data = await res.json().catch(() => ({}));
    if (!res.ok) throw new Error(data?.error || `Failed to load projects: HTTP ${res.status}`);
    return data.projects ?? [];
  }

  /** Capability-filtered project list. Agent v1.99.75+ exposes
   *  `/projects/web` and `/projects/all` alongside the existing
   *  `/projects/mobile`. Each project carries `webCapable` and
   *  `mobileCapable` flags so the dashboard can populate the Web App
   *  tab and Mobile App tab independently — and a single project
   *  (e.g. an Expo app with `react-native-web` in deps) shows up in
   *  both lists.
   *
   *  Returns full MobileProject records: name, path, framework,
   *  capability flags, monorepoRoot/monorepoApp lineage. */
  async listProjectsByCapability(capability: "web" | "mobile" | "all"): Promise<Array<{
    name: string;
    path: string;
    framework: string;
    executionMode?: string;
    primarySurface?: string;
    sdkVersion?: string;
    hasDevBuild?: boolean;
    branch?: string;
    remote?: string;
    size?: string;
    webCapable?: boolean;
    mobileCapable?: boolean;
    monorepoRoot?: string;
    monorepoApp?: string;
  }>> {
    this.assertConnected();
    const path = capability === "web" ? "/projects/web" : capability === "all" ? "/projects/all" : "/projects/mobile";
    const res = await fetch(`${this.baseUrl}${path}`, { headers: this.authHeaders });
    if (!res.ok) return [];
    const data = (await res.json().catch(() => ({}))) as { projects?: unknown };
    return Array.isArray(data?.projects) ? (data.projects as Array<Record<string, unknown>>).map((p) => ({
      name: String(p.name ?? ""),
      path: String(p.path ?? ""),
      framework: String(p.framework ?? ""),
      executionMode: typeof p.executionMode === "string" ? p.executionMode : undefined,
      primarySurface: typeof p.primarySurface === "string" ? p.primarySurface : undefined,
      sdkVersion: typeof p.sdkVersion === "string" ? p.sdkVersion : undefined,
      hasDevBuild: typeof p.hasDevBuild === "boolean" ? p.hasDevBuild : undefined,
      branch: typeof p.branch === "string" ? p.branch : undefined,
      remote: typeof p.remote === "string" ? p.remote : undefined,
      size: typeof p.size === "string" ? p.size : undefined,
      webCapable: typeof p.webCapable === "boolean" ? p.webCapable : undefined,
      mobileCapable: typeof p.mobileCapable === "boolean" ? p.mobileCapable : undefined,
      monorepoRoot: typeof p.monorepoRoot === "string" ? p.monorepoRoot : undefined,
      monorepoApp: typeof p.monorepoApp === "string" ? p.monorepoApp : undefined,
    })) : [];
  }

  async getProjectActions(query: string): Promise<{ project: string; path: string; actions: { label: string; target: string; type: string; framework?: string; platform?: string; command?: string; icon?: string; supported?: boolean; reason?: string }[] }> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/projects/actions?query=${encodeURIComponent(query)}`, { headers: this.authHeaders });
    if (!res.ok) throw new Error("Failed to get project actions");
    return res.json();
  }

  async getRemoteRuntimeCapabilities(workDir: string, framework: string): Promise<RemoteRuntimeCapabilities> {
    this.assertConnected();
    const url = new URL(`${this.baseUrl}/remote-runtime/capabilities`);
    url.searchParams.set("workDir", workDir);
    url.searchParams.set("framework", framework);
    const res = await fetch(url.toString(), { headers: this.authHeaders });
    if (!res.ok) throw new Error(`Failed to load remote runtime capabilities: HTTP ${res.status}`);
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
    if (!res.ok) throw new Error(data?.error || `Failed to start remote runtime session: HTTP ${res.status}`);
    return data as RemoteRuntimeSession;
  }

  async sendRemoteRuntimeCommand(sessionId: string, command: "launch-feedback", source: string = "web"): Promise<{ ok: boolean; note?: string; protocol?: string }> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/remote-runtime/sessions/${encodeURIComponent(sessionId)}/command`, {
      method: "POST",
      headers: { ...this.authHeaders, "Content-Type": "application/json" },
      body: JSON.stringify({ command, source }),
    });
    const data = await res.json().catch(() => ({}));
    if (!res.ok) throw new Error(data?.error || `Failed to send remote runtime command: HTTP ${res.status}`);
    return data;
  }

  async getRemoteRuntimeSession(sessionId: string): Promise<RemoteRuntimeSession> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/remote-runtime/sessions/${encodeURIComponent(sessionId)}`, { headers: this.authHeaders });
    const data = await res.json().catch(() => ({}));
    if (!res.ok) throw new Error(data?.error || `Failed to load remote runtime session: HTTP ${res.status}`);
    return data as RemoteRuntimeSession;
  }

  async closeRemoteRuntimeSession(sessionId: string): Promise<void> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/remote-runtime/sessions/${encodeURIComponent(sessionId)}`, {
      method: "DELETE",
      headers: this.authHeaders,
    });
    const data = await res.json().catch(() => ({}));
    if (!res.ok) throw new Error(data?.error || `Failed to close remote runtime session: HTTP ${res.status}`);
  }

  async fetchRemoteRuntimeTurnCredentials(): Promise<{ iceServers: RTCIceServer[]; ttlSeconds: number }> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/remote-runtime/turn-credentials`, {
      headers: this.authHeaders,
      cache: "no-store",
    });
    const data = await res.json().catch(() => ({}));
    if (!res.ok) {
      throw new Error(data?.error || `Failed to fetch TURN credentials: HTTP ${res.status}`);
    }
    // The agent always returns at least a STUN entry; TURN is added
    // when YAVER_TURN_URL + the relay's TURN secret are both set on
    // the agent host.
    return {
      iceServers: Array.isArray(data?.iceServers) ? (data.iceServers as RTCIceServer[]) : [],
      ttlSeconds: Number(data?.ttlSeconds) || 60,
    };
  }

  async createRemoteRuntimeWebRTCAnswer(sessionId: string, offer: { sdp?: string; type?: string }): Promise<{ session: RemoteRuntimeSession; answer: { sdp?: string; type?: string }; transport?: string; note?: string }> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/remote-runtime/sessions/${encodeURIComponent(sessionId)}/webrtc/offer`, {
      method: "POST",
      headers: { ...this.authHeaders, "Content-Type": "application/json" },
      body: JSON.stringify({ sdp: offer.sdp || "", type: offer.type || "offer" }),
    });
    const data = await res.json().catch(() => ({}));
    if (!res.ok) throw new Error(data?.error || `Failed to negotiate remote runtime WebRTC: HTTP ${res.status}`);
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
      throw new Error(data?.error || `Failed to fetch remote runtime frame: HTTP ${res.status}`);
    }
    return await res.blob();
  }

  async sendRemoteRuntimeControl(sessionId: string, body: { action: "tap" | "swipe" | "text" | "back" | "home" | "key"; x?: number; y?: number; x2?: number; y2?: number; durationMs?: number; text?: string; key?: string }): Promise<RemoteRuntimeSession> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/remote-runtime/sessions/${encodeURIComponent(sessionId)}/control`, {
      method: "POST",
      headers: { ...this.authHeaders, "Content-Type": "application/json" },
      body: JSON.stringify(body),
    });
    const data = await res.json().catch(() => ({}));
    if (!res.ok) throw new Error(data?.error || `Failed to send remote runtime control: HTTP ${res.status}`);
    return (data?.session || data) as RemoteRuntimeSession;
  }

  async getPublishConfig(dir?: string): Promise<{ config: unknown; exists: boolean; path: string }> {
    this.assertConnected();
    const params = dir ? `?dir=${encodeURIComponent(dir)}` : "";
    const res = await fetch(`${this.baseUrl}/publish/config${params}`, { headers: this.authHeaders });
    if (!res.ok) throw new Error("Failed to get publish config");
    return res.json();
  }

  async savePublishConfig(dir: string, config: unknown): Promise<{ ok: boolean; path: string }> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/publish/config`, {
      method: "POST",
      headers: { ...this.authHeaders, "Content-Type": "application/json" },
      body: JSON.stringify({ dir, config }),
    });
    if (!res.ok) throw new Error("Failed to save publish config");
    return res.json();
  }

  async startPublish(dir: string, target: string, allowGitHubFallback = false): Promise<{
    id: string;
    targetId: string;
    status: string;
    provider: string;
  }> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/publish/run`, {
      method: "POST",
      headers: { ...this.authHeaders, "Content-Type": "application/json" },
      body: JSON.stringify({ dir, target, allowGitHubFallback }),
    });
    if (!res.ok) throw new Error("Failed to start publish");
    return res.json();
  }

  async listPublishes(): Promise<Array<{ id: string; targetId: string; status: string; provider: string }>> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/publish/runs`, { headers: this.authHeaders });
    if (!res.ok) return [];
    return res.json();
  }

  async getPublish(id: string): Promise<unknown> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/publish/runs/${encodeURIComponent(id)}`, { headers: this.authHeaders });
    if (!res.ok) throw new Error("Failed to fetch publish run");
    return res.json();
  }

  // ── Dev Server ────────────────────────────────────────────────────

  async getDevServerStatus(): Promise<{
    running: boolean;
    serving?: boolean;
    servingLabel?: string;
    stopActionLabel?: string;
    framework?: string;
    workDir?: string;
    port?: number;
    /** Expo only — Metro's devMode: "dev-client" (default) or "web" */
    devMode?: string;
    /** Expo parallel web preview port (sibling of Metro). Non-zero
     *  when a browser iframe preview is running through /dev-web/*. */
    webPort?: number;
    targetDeviceId?: string;
    targetDeviceName?: string;
    targetDeviceClass?: string;
    /** Set when the agent could not be reached or rejected the call.
     *  Distinguishes "agent says not running" from "we don't know". */
    error?: string;
    /** HTTP status when the call returned a response. Useful for the
     *  dashboard to surface 401 → re-auth, 5xx → infra. */
    httpStatus?: number;
  } | null> {
    this.assertConnected();
    try {
      const res = await fetch(`${this.baseUrl}/dev/status`, { headers: this.authHeaders });
      if (!res.ok) {
        let body: any = null;
        try { body = await res.json(); } catch { body = null; }
        return {
          running: false,
          error: body?.error || `HTTP ${res.status}`,
          httpStatus: res.status,
        };
      }
      return res.json();
    } catch (err) {
      return {
        running: false,
        error: err instanceof Error ? err.message : String(err),
      };
    }
  }

  async getDevServerTarget(): Promise<DevTargetPreference | null> {
    this.assertConnected();
    try {
      const res = await fetch(`${this.baseUrl}/dev/target`, { headers: this.authHeaders });
      if (!res.ok) return null;
      return res.json();
    } catch { return null; }
  }

  async setDevServerTarget(target: DevTargetPreference): Promise<DevTargetPreference | null> {
    this.assertConnected();
    try {
      const res = await fetch(`${this.baseUrl}/dev/target`, {
        method: "POST",
        headers: { ...this.authHeaders, "Content-Type": "application/json" },
        body: JSON.stringify(target),
      });
      if (!res.ok) return null;
      return res.json();
    } catch { return null; }
  }

  async getMobileWorkerPreviewSession(): Promise<MobileWorkerPreviewSession | null> {
    this.assertConnected();
    try {
      const res = await fetch(`${this.baseUrl}/mobile-workers/preview-session`, { headers: this.authHeaders });
      if (!res.ok) return null;
      return res.json();
    } catch { return null; }
  }

  async sendMobileWorkerPreviewCommand(command: string, data?: Record<string, unknown>): Promise<boolean> {
    this.assertConnected();
    try {
      const res = await fetch(`${this.baseUrl}/mobile-workers/preview-session/command`, {
        method: "POST",
        headers: { ...this.authHeaders, "Content-Type": "application/json" },
        body: JSON.stringify({ command, data }),
      });
      return res.ok;
    } catch { return false; }
  }

  // ── Vibe Preview (live screenshot/video stream of dev server) ──────
  // See docs/vibe-preview-streaming.md and desktop/agent/vibe_preview.go.

  async startVibePreview(opts: {
    project: string;
    targetUrl: string;
    mode?: "live" | "change-only" | "summary-only";
    profile?: string;
    netMode?: "direct" | "relay-wifi" | "relay-cell";
  }): Promise<{ id: string; project: string; profile?: { fps: number; name: string } } | null> {
    this.assertConnected();
    try {
      const res = await fetch(`${this.baseUrl}/vibing/preview/start`, {
        method: "POST",
        headers: {
          ...this.authHeaders,
          "Content-Type": "application/json",
          "X-Yaver-NetMode": opts.netMode ?? "relay-wifi",
        },
        body: JSON.stringify({ ...opts, mode: opts.mode ?? "live" }),
      });
      if (!res.ok) return null;
      const data = await res.json();
      return data?.session ?? null;
    } catch { return null; }
  }

  async stopVibePreview(project: string): Promise<boolean> {
    this.assertConnected();
    try {
      const res = await fetch(`${this.baseUrl}/vibing/preview/stop`, {
        method: "POST",
        headers: { ...this.authHeaders, "Content-Type": "application/json" },
        body: JSON.stringify({ project }),
      });
      return res.ok;
    } catch { return false; }
  }

  async listVibePreviewSessions(): Promise<Array<{ project: string; profile: { fps: number; name: string }; mode: string }>> {
    this.assertConnected();
    try {
      const res = await fetch(`${this.baseUrl}/vibing/preview/status`, { headers: this.authHeaders });
      if (!res.ok) return [];
      const data = await res.json();
      return Array.isArray(data?.sessions) ? data.sessions : [];
    } catch { return []; }
  }

  async startVibeClip(opts: {
    project: string;
    source?: "browser" | "sim-ios" | "sim-android" | "phone";
    durationMaxSec?: number;
  }): Promise<{ id: string; status: string } | null> {
    this.assertConnected();
    try {
      const res = await fetch(`${this.baseUrl}/vibing/preview/clip/start`, {
        method: "POST",
        headers: { ...this.authHeaders, "Content-Type": "application/json" },
        body: JSON.stringify(opts),
      });
      if (!res.ok) return null;
      const data = await res.json();
      return data?.clip ?? null;
    } catch { return null; }
  }

  async listVibeClips(project: string): Promise<Array<{ id: string; status: string; durationSec?: number; source: string }>> {
    this.assertConnected();
    try {
      const res = await fetch(
        `${this.baseUrl}/vibing/preview/clips?project=${encodeURIComponent(project)}`,
        { headers: this.authHeaders },
      );
      if (!res.ok) return [];
      const data = await res.json();
      return Array.isArray(data?.clips) ? data.clips : [];
    } catch { return []; }
  }

  /** Returns a tuple (url, headers). The view builds an <img> / <video>
   *  src with the URL and adds the headers via a fetch+blob shim, since
   *  browsers don't pass custom headers to <img src>. For relay-routed
   *  paths the auth lives in the URL via the relay's session cookie. */
  vibeFrameRequest(project: string, hash: string): { url: string; headers: Record<string, string> } | null {
    if (!this.baseUrl) return null;
    return {
      url: `${this.baseUrl}/vibing/preview/frames/${encodeURIComponent(hash)}?project=${encodeURIComponent(project)}`,
      headers: this.authHeaders,
    };
  }

  vibeClipRequest(clipId: string): { url: string; headers: Record<string, string> } | null {
    if (!this.baseUrl) return null;
    return {
      url: `${this.baseUrl}/vibing/preview/clip/${encodeURIComponent(clipId)}`,
      headers: this.authHeaders,
    };
  }

  /** Open an SSE subscription. The browser EventSource API can't carry
   *  custom auth headers, so we use fetch+ReadableStream and parse SSE
   *  framing manually (same pattern the mobile client uses). */
  subscribeVibePreviewEvents(
    project: string,
    onEvent: (ev: any) => void,
    onError?: (err: unknown) => void,
  ): () => void {
    const ctrl = new AbortController();
    void (async () => {
      try {
        const res = await fetch(
          `${this.baseUrl}/vibing/preview/events?project=${encodeURIComponent(project)}`,
          { headers: { ...this.authHeaders, Accept: "text/event-stream" }, signal: ctrl.signal },
        );
        if (!res.ok || !res.body) {
          onError?.(new Error(`vibe-preview events: HTTP ${res.status}`));
          return;
        }
        const reader = res.body.getReader();
        const decoder = new TextDecoder();
        let buffer = "";
        while (!ctrl.signal.aborted) {
          const { done, value } = await reader.read();
          if (done) break;
          buffer += decoder.decode(value, { stream: true });
          let idx;
          while ((idx = buffer.indexOf("\n\n")) >= 0) {
            const chunk = buffer.slice(0, idx);
            buffer = buffer.slice(idx + 2);
            const dataLines = chunk
              .split("\n")
              .filter((l) => l.startsWith("data:"))
              .map((l) => l.slice(5).trimStart());
            if (dataLines.length === 0) continue;
            try { onEvent(JSON.parse(dataLines.join("\n"))); } catch { /* ping */ }
          }
        }
      } catch (err) {
        if (!ctrl.signal.aborted) onError?.(err);
      }
    })();
    return () => ctrl.abort();
  }

  async startDevServer(opts: {
    framework?: string;
    workDir?: string;
    projectName?: string;
    app?: string;      // workspace manifest app name (monorepo)
    surface?: "web-reload" | "hot-reload";
    root?: string;     // workspace root override
    platform?: string;
    targetDeviceId?: string;
    targetDeviceName?: string;
    targetDeviceClass?: string;
  }): Promise<{
    ok: boolean;
    /** When the agent (v1.99.80+) detects a mobile-only framework
     *  invoked by the Web UI on `surface=web-reload`, it doesn't
     *  reject the start — it tells us to use the static bundle path
     *  instead. UI sees `mode === "static-bundle"`, polls the bundle
     *  info, and either renders the existing build or kicks off
     *  `buildWebJSBundle()`. Older agents return `mode === undefined`
     *  and a 400 in the legacy "mobile-only" branch. */
    mode?: "static-bundle" | "dev-server";
    bundleUrl?: string;
    bundleReady?: boolean;
    bundleHint?: string;
  }> {
    this.assertConnected();
    // `caller: "web-ui"` — explicit identity tag (agent reads it to
    // route mobile-only projects through the static-bundle path
    // instead of returning the legacy 400 "mobile-only" error).
    const body = { ...opts, caller: "web-ui" };
    const res = await fetch(`${this.baseUrl}/dev/start`, {
      method: "POST",
      headers: { ...this.authHeaders, "Content-Type": "application/json" },
      body: JSON.stringify(body),
    });
    const data: any = await res.json().catch(() => ({}));
    if (!res.ok) {
      throw new Error(data?.error || `Failed to start dev server (HTTP ${res.status})`);
    }
    return {
      ok: true,
      mode: typeof data?.mode === "string" ? data.mode : undefined,
      bundleUrl: typeof data?.bundleUrl === "string" ? data.bundleUrl : undefined,
      bundleReady: data?.bundleReady === true,
      bundleHint: typeof data?.bundleHint === "string" ? data.bundleHint : undefined,
    };
  }

  // ── Workspace manifest (monorepo) ────────────────────────────────

  async getWorkspace(root?: string): Promise<WorkspaceResponse | null> {
    this.assertConnected();
    try {
      const query = root ? `?root=${encodeURIComponent(root)}` : "";
      const res = await fetch(`${this.baseUrl}/workspace${query}`, { headers: this.authHeaders });
      if (!res.ok) return null;
      return res.json();
    } catch { return null; }
  }

  async getWorkspaceApps(kind?: string | string[], root?: string): Promise<WorkspaceAppView[]> {
    this.assertConnected();
    const params = new URLSearchParams();
    if (kind) params.set("kind", Array.isArray(kind) ? kind.join(",") : kind);
    if (root) params.set("root", root);
    const query = params.toString() ? `?${params.toString()}` : "";
    const res = await fetch(`${this.baseUrl}/workspace/apps${query}`, { headers: this.authHeaders });
    const data = await res.json().catch(() => ({}));
    if (!res.ok) throw new Error(data?.error || `Failed to load workspace apps: HTTP ${res.status}`);
    return Array.isArray(data?.apps) ? data.apps : [];
  }

  async stopDevServer(): Promise<{
    ok?: boolean;
    stoppedServing?: boolean;
    previouslyServing?: boolean;
    /** agent 1.99.93+: true when the subprocess actually exited within 7s of SIGINT/SIGKILL. */
    verified?: boolean;
    /** agent 1.99.93+: number of in-flight /dev/build-native runs cancelled. */
    buildsCancelled?: number;
    framework?: string;
    kind?: string;
    workDir?: string;
    message?: string;
    error?: string;
  }> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/dev/stop`, { method: "POST", headers: this.authHeaders });
    const data = await res.json().catch(() => ({}));
    if (!res.ok) {
      throw new Error(data?.message || data?.error || "Failed to stop serving preview");
    }
    return data;
  }

  async reloadDevServer(opts?: { mode?: "dev" | "bundle" }): Promise<{
    ok?: boolean;
    nativeChangesDetected?: boolean;
    nativeChanges?: Array<{ path?: string; reason?: string }>;
    changeClass?: string;
    status?: string;
    bundleUrl?: string;
    moduleName?: string;
    error?: string;
  }> {
    this.assertConnected();
    const mode = opts?.mode ?? "dev";
    if (mode === "dev") {
      const res = await fetch(`${this.baseUrl}/dev/reload`, { method: "POST", headers: this.authHeaders });
      const data = await res.json().catch(() => ({}));
      if (!res.ok) {
        throw new Error(data?.error || "Failed to reload dev server");
      }
      return data;
    }

    const res = await fetch(`${this.baseUrl}/dev/reload-app`, {
      method: "POST",
      headers: { ...this.authHeaders, "Content-Type": "application/json" },
      body: JSON.stringify({ mode: "bundle" }),
    });
    const data = await res.json().catch(() => ({}));
    if (!res.ok) {
      throw new Error(data?.error || "Failed to rebuild bundle for reload");
    }
    return data;
  }

  // Spin up a sibling Expo Web process alongside Metro so the browser
  // iframe can render RN apps without killing the phone's Hermes push
  // path. Only valid when the active dev server is Expo.
  async startWebPreview(): Promise<{ ok: boolean; port: number; webUrl: string }> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/dev/web-preview/start`, { method: "POST", headers: this.authHeaders });
    const data = await res.json().catch(() => ({}));
    if (!res.ok) throw new Error(data?.error || "Failed to start Expo Web preview");
    return data;
  }

  async stopWebPreview(): Promise<{ ok: boolean }> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/dev/web-preview/stop`, { method: "POST", headers: this.authHeaders });
    const data = await res.json().catch(() => ({}));
    if (!res.ok) throw new Error(data?.error || "Failed to stop Expo Web preview");
    return data;
  }

  /** URL the browser iframe points at for the Expo Web sibling. Only
   *  meaningful when devStatus.webPort > 0. Mirrors devPreviewUrl
   *  shape (relay-proxied vs direct) but hits /dev-web/ instead. */
  get devWebPreviewUrl(): string | null {
    if (!this.baseUrl) return null;
    const direct = this.baseUrl.startsWith("http://127.0.0.1") || this.baseUrl.startsWith("http://localhost");
    if (direct) return `${this.baseUrl}/dev-web/`;
    // Same-origin proxy at /d/<deviceId>/[[...path]]/route.ts. The
    // earlier /api/relay/ prefix had no Next.js handler and silently
    // fell through to a 404 page when the iframe tried to load it.
    if (this._activeRelayUrl && this.deviceId) {
      return `/d/${encodeURIComponent(this.deviceId)}/dev-web/`;
    }
    return `${this.baseUrl}/dev-web/`;
  }

  /** URL for the most-recently-built static web bundle (target=web-js-bundle).
   *  Mirrors devWebPreviewUrl's relay-proxy rewriting so the iframe loads
   *  through our same-origin proxy. The /dev/web-bundle/ endpoint is
   *  unauthenticated on the agent (the iframe needs to load without
   *  cooperation from the dashboard's bearer token); the relay still
   *  enforces password gating via `__rp=`. Agent v1.99.74+ injects
   *  `<base href="/dev/web-bundle/">` into served index.html so the
   *  bundle's absolute asset paths resolve through the relay-prefixed
   *  origin. */
  get devWebBundleUrl(): string | null {
    if (!this.baseUrl) return null;
    const direct = this.baseUrl.startsWith("http://127.0.0.1") || this.baseUrl.startsWith("http://localhost");
    if (direct) return `${this.baseUrl}/dev/web-bundle/`;
    // Same-origin proxy via /d/<deviceId>/[[...path]] — that Next.js
    // route is the one that actually exists and forwards to the relay
    // with X-Relay-Password injected server-side. /api/relay/* has no
    // handler; using it caused the iframe to 404 with Yaver's branded
    // "page could not be found" page (which was very confusing).
    if (this._activeRelayUrl && this.deviceId) {
      return `/d/${encodeURIComponent(this.deviceId)}/dev/web-bundle/`;
    }
    return `${this.baseUrl}/dev/web-bundle/`;
  }

  /** Compile a static web bundle on the agent (target=web-js-bundle).
   *  Resolves to the agent's response when the build completes; rejects
   *  with the bundler tail on failure. The dashboard renders SSE
   *  webview/build + webview/transport events for live progress while
   *  this is in flight — caller doesn't have to do its own polling.
   *
   *  Pair with `ackWebBundleLoaded()` once the iframe fires `onload`
   *  and `reportWebBundleError()` if the iframe surfaces a JS error,
   *  so the agent can drive the transport tracker through phase
   *  delivered/error. */
  async buildWebJSBundle(opts: {
    projectName?: string;
    projectPath?: string;
    /** Defaults to the recommended `web-js-bundle` target. Pass
     *  "web-hermes-wasm" to request the experimental Hermes-WASM
     *  runner — same Metro bundle, hermesc-compiled HBC, served
     *  alongside a runner HTML that loads hermes.wasm in the browser.
     *  Best-effort: the upstream Hermes WASM runner JS isn't shipped
     *  yet, so the experimental target surfaces a clear status pane
     *  instead of full execution. The protocol half is wired so the
     *  experimental render can be filled in without protocol churn. */
    target?: "web-js-bundle" | "web-hermes-wasm";
  }): Promise<{
    ok: boolean;
    bundleUrl: string;
    size: number;
    fileCount: number;
    error?: string;
    output?: string;
  }> {
    if (!this.baseUrl) throw new Error("not connected");
    const res = await this.fetchWithTimeout(`${this.baseUrl}/dev/build-native`, {
      method: "POST",
      headers: { ...this.authHeaders, "Content-Type": "application/json" },
      body: JSON.stringify({
        target: opts.target ?? "web-js-bundle",
        projectName: opts.projectName ?? undefined,
        projectPath: opts.projectPath ?? undefined,
        caller: "web-ui",
        // Compat baseline. Mirrors mobile's HBC manifest contract —
        // the agent's preflight rejects builds where the project's
        // installed react drifts off this range, instead of letting
        // the iframe white-screen on React error #527.
        clientVersion: YAVER_CALLER_ID,
        expectReact: "^19.0.0",
        expectReactDom: "^19.0.0",
      }),
    }, 240_000);
    if (!res.ok) {
      return {
        ok: false,
        bundleUrl: "",
        size: 0,
        fileCount: 0,
        error: await responseErrorMessage(res, `HTTP ${res.status}`),
      };
    }
    const body: unknown = await res.json().catch(() => ({}));
    const obj = (body && typeof body === "object" ? body : {}) as Record<string, unknown>;
    if (!res.ok || obj.status !== "ok") {
      return {
        ok: false,
        bundleUrl: "",
        size: 0,
        fileCount: 0,
        error: typeof obj.error === "string" ? obj.error : `HTTP ${res.status}`,
        output: typeof obj.output === "string" ? obj.output : undefined,
      };
    }
    return {
      ok: true,
      bundleUrl: typeof obj.bundleUrl === "string" ? obj.bundleUrl : "/dev/web-bundle/",
      size: typeof obj.size === "number" ? obj.size : 0,
      fileCount: typeof obj.fileCount === "number" ? obj.fileCount : 0,
    };
  }

  /** GET /dev/web-bundle/info — metadata about the most recently
   *  built static web bundle (target=web-js-bundle). Returns built:false
   *  if nothing's been built yet. The dashboard polls this on mount so
   *  any pre-existing bundle (e.g. one built via curl, MCP, or a prior
   *  session) auto-renders in the iframe without requiring the user to
   *  click "Build & render static bundle" first. */
  async getWebBundleInfo(): Promise<{
    built: boolean;
    target?: string;
    indexFile?: string;
    size?: number;
    fileCount?: number;
    builtAt?: string;
    caller?: string;
    /** Source project root the bundle was built from. Lets the
     *  dashboard tell whether the on-disk bundle belongs to the
     *  user's selected project before promoting a stale build to
     *  the iframe — see WebReloadView's failed→ready guard. */
    workDir?: string;
    buildDir?: string;
  }> {
    if (!this.baseUrl) return { built: false };
    try {
      const res = await this.fetchWithTimeout(`${this.baseUrl}/dev/web-bundle/info`, {
        headers: this.authHeaders,
      }, 5_000);
      if (!res.ok) return { built: false };
      const body = (await res.json()) as Record<string, unknown>;
      if (body?.built !== true) return { built: false };
      return {
        built: true,
        target: typeof body.target === "string" ? body.target : undefined,
        indexFile: typeof body.indexFile === "string" ? body.indexFile : undefined,
        size: typeof body.size === "number" ? body.size : undefined,
        fileCount: typeof body.fileCount === "number" ? body.fileCount : undefined,
        builtAt: typeof body.builtAt === "string" ? body.builtAt : undefined,
        caller: typeof body.caller === "string" ? body.caller : undefined,
        workDir: typeof body.workDir === "string" ? body.workDir : undefined,
        buildDir: typeof body.buildDir === "string" ? body.buildDir : undefined,
      };
    } catch {
      return { built: false };
    }
  }

  /** POST /dev/web-bundle/ack — iframe finished loading; transport
   *  tracker transitions to phase=delivered. */
  async ackWebBundleLoaded(msToLoad: number): Promise<void> {
    if (!this.baseUrl) return;
    try {
      await fetch(`${this.baseUrl}/dev/web-bundle/ack`, {
        method: "POST",
        headers: { ...this.authHeaders, "Content-Type": "application/json" },
        body: JSON.stringify({ ms_to_load: msToLoad }),
      });
    } catch {
      // Best-effort; if the ack fails the dashboard already knows
      // the iframe loaded.
    }
  }

  /** POST /dev/web-bundle/error — iframe surfaced a JS init error;
   *  transport tracker transitions to phase=error. */
  async reportWebBundleError(message: string, stack?: string, source?: string): Promise<void> {
    if (!this.baseUrl) return;
    try {
      await fetch(`${this.baseUrl}/dev/web-bundle/error`, {
        method: "POST",
        headers: { ...this.authHeaders, "Content-Type": "application/json" },
        body: JSON.stringify({ message, stack, source }),
      });
    } catch {
      // Best-effort.
    }
  }

  get devPreviewUrl(): string | null {
    if (!this.baseUrl) return null;
    // In the browser, route relay-backed previews through our own
    // same-origin proxy so the iframe does not depend on relay query-param
    // auth. That proxy injects X-Relay-Password server-side.
    if (this._activeRelayUrl) {
      if (!this.deviceId) return null;
      return `/d/${encodeURIComponent(this.deviceId)}/dev/`;
    }
    return `${this.baseUrl}/dev/`;
  }

  /** Get the SSE events URL for dev server live reload.
   *
   *  Returns a URL with auth baked into the query string so the
   *  browser's native EventSource API can drive it — EventSource
   *  doesn't support custom headers, but it sails through Safari's
   *  cross-origin SSE handling that fetch+stream stalls on
   *  indefinitely. The relay accepts ?__rp=<password> at
   *  relay/server.go:681; the agent accepts ?token=<bearer> at
   *  desktop/agent/httpserver.go:1534. Both already work for the
   *  iframe preview path; we're now using them for the event
   *  stream too.
   *
   *  Token + password ride over HTTPS (yaver.io is TLS end-to-end),
   *  same as the iframe's __rp=. They never appear in clear text
   *  on disk or in nginx logs because we use ?__rp= which the relay
   *  strips before forwarding to the agent. */
  get devEventsUrl(): string | null {
    if (!this.baseUrl) return null;
    return this.appendStreamAuth(`${this.baseUrl}/dev/events`);
  }

  /** SSE URL for the agent-update progress stream — same query-
   *  param auth pattern. */
  get agentUpdateStreamUrl(): string | null {
    if (!this.baseUrl) return null;
    return this.appendStreamAuth(`${this.baseUrl}/streams/agent-update`);
  }

  private appendStreamAuth(url: string): string {
    // Defensive: when called before connect() has populated host/port/relay,
    // baseUrl can produce strings like `http://undefined:undefined/dev/events`
    // and `new URL()` throws synchronously, crashing the dashboard render.
    // Fall back to manual querystring concat — the resulting URL still
    // won't actually fetch anything until connect lands, but at least
    // React keeps rendering and the auto-reconnect loop runs to fix it.
    let u: URL;
    try {
      u = new URL(url);
    } catch {
      const params: string[] = [];
      if (this.token) params.push(`token=${encodeURIComponent(this.token)}`);
      if (this._activeRelayUrl && this.activeRelayPassword) {
        params.push(`__rp=${encodeURIComponent(this.activeRelayPassword)}`);
      }
      const join = url.includes("?") ? "&" : "?";
      return params.length ? `${url}${join}${params.join("&")}` : url;
    }
    if (this.token) u.searchParams.set("token", this.token);
    if (this._activeRelayUrl && this.activeRelayPassword) {
      u.searchParams.set("__rp", this.activeRelayPassword);
    }
    // EventSource can't set custom headers, so we pass the caller
    // surface as ?caller= and the agent treats it equivalently to
    // X-Yaver-Caller. Lets dev/events emissions show "[web-dashboard]"
    // attribution on every SSE frame.
    u.searchParams.set("caller", YAVER_CALLER_ID);
    return u.toString();
  }

  /**
   * Force-refresh the relay password for the current user from Convex
   * + re-pull the relayServers list. Mirrors what the Cloudflare
   * Worker proxy does at /d/<id>/* on 401: when the cached
   * activeRelayPassword goes stale (relay-side rotation, fresh user
   * with no password row, etc.), call /settings/repair-relay to have
   * Convex regenerate it, then re-fetch /config to update our local
   * relayServers cache. After this returns, this.activeRelayPassword
   * is fresh and EventSource / fetch can be retried.
   */
  async repairRelayPassword(): Promise<{ ok: boolean; error?: string }> {
    if (!this.token) return { ok: false, error: "not signed in" };
    try {
      const repairRes = await fetch(`${CONVEX_URL}/settings/repair-relay`, {
        method: "POST",
        headers: {
          Authorization: `Bearer ${this.token}`,
          "Content-Type": "application/json",
        },
        body: "{}",
      });
      if (!repairRes.ok) {
        return { ok: false, error: `repair-relay ${repairRes.status}` };
      }
      // Pull the freshly-rotated password back from /config so
      // activeRelayPassword reflects it on the next stream attempt.
      const cfgRes = await fetch(`${CONVEX_URL}/config`, {
        headers: { Authorization: `Bearer ${this.token}` },
        cache: "no-store",
      });
      if (cfgRes.ok) {
        const cfg = await cfgRes.json().catch(() => ({}));
        const relays: Array<{ httpUrl?: string; password?: string; id?: string }> =
          Array.isArray(cfg?.relayServers) ? cfg.relayServers : [];
        // Update password on the matching relay we're already
        // connected to. Don't switch relays here.
        for (const relay of relays) {
          if (relay.httpUrl === this._activeRelayUrl) {
            this.activeRelayPassword = relay.password || null;
            break;
          }
        }
      }
      return { ok: true };
    } catch (err) {
      return { ok: false, error: err instanceof Error ? err.message : String(err) };
    }
  }

  /** Get auth headers for direct fetch calls (non-SSE). */
  getAuthHeaders(): Record<string, string> {
    return this.authHeaders;
  }

  // ── Todos ─────────────────────────────────────────────────────────

  async listTodos(): Promise<{ id: string; description: string; status: string }[]> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/todolist`, { headers: this.authHeaders });
    if (!res.ok) return [];
    const data = await res.json();
    return data.items ?? [];
  }

  async addTodo(description: string): Promise<void> {
    this.assertConnected();
    await fetch(`${this.baseUrl}/todolist`, {
      method: "POST",
      headers: { ...this.authHeaders, "Content-Type": "application/json" },
      body: JSON.stringify({ description, source: "web" }),
    });
  }

  async deleteTodo(id: string): Promise<void> {
    this.assertConnected();
    await fetch(`${this.baseUrl}/todolist/${id}`, { method: "DELETE", headers: this.authHeaders });
  }

  async todoCount(): Promise<number> {
    this.assertConnected();
    try {
      const res = await fetch(`${this.baseUrl}/todolist/count`, { headers: this.authHeaders });
      if (!res.ok) return 0;
      const data = await res.json();
      return data.count ?? 0;
    } catch { return 0; }
  }

  // ── Monorepo detection ─────────────────────────────────────────────

  /** Classify the framework composition of a directory on the connected agent.
   *  Mirrors the mobile QuicClient.detectMonorepo / agent's DetectMonorepo. */
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
    const res = await fetch(`${this.baseUrl}/projects/monorepo${qs ? '?' + qs : ''}`, {
      headers: this.authHeaders,
    });
    if (!res.ok) {
      let msg = `Monorepo detect failed: ${res.status}`;
      try { const err = await res.json(); if (err?.error) msg = err.error; } catch { /* keep status */ }
      throw new Error(msg);
    }
    return res.json();
  }

  /** Trigger a native build (iosNative / androidNative / flutter) on the connected agent.
   *  Mirrors the mobile QuicClient.startNativeBuild. */
  async startNativeBuild(
    platform: 'iosNative' | 'androidNative' | 'flutter',
    target: 'device' | 'simulator' | 'testflight' | 'playstore' | 'local' | 'apk' | 'aab' | 'ipa' = 'device',
    workDir?: string,
    extras?: { scheme?: string; flavor?: string; installOnDevice?: boolean; args?: string[] },
  ): Promise<{ id: string; platform: string; status: string; command?: string; workDir?: string }> {
    this.assertConnected();
    const args: string[] = [];
    if (platform === 'iosNative' && extras?.scheme) args.push(extras.scheme);
    if (platform === 'androidNative' && extras?.flavor) args.push(extras.flavor);
    if (extras?.args?.length) args.push(...extras.args);
    const installOnDevice = extras?.installOnDevice ?? (target === 'device' || target === 'simulator');

    const res = await fetch(`${this.baseUrl}/builds`, {
      method: 'POST',
      headers: { ...this.authHeaders, 'Content-Type': 'application/json' },
      body: JSON.stringify({ platform, target, workDir: workDir || '', args, installOnDevice }),
    });
    if (!res.ok) {
      let msg = `Native build failed: ${res.status}`;
      try { const err = await res.json(); if (err?.error) msg = err.error; } catch { /* keep status */ }
      throw new Error(msg);
    }
    return res.json();
  }

  // ── Builds ────────────────────────────────────────────────────────

  async listBuilds(): Promise<{ id: string; platform: string; status: string; startedAt?: number; artifactName?: string }[]> {
    this.assertConnected();
    try {
      const res = await fetch(`${this.baseUrl}/builds`, { headers: this.authHeaders });
      if (!res.ok) return [];
      const data = await res.json();
      return Array.isArray(data) ? data : [];
    } catch { return []; }
  }

  async getBuild(id: string): Promise<unknown> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/builds/${id}`, { headers: this.authHeaders });
    return res.json();
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
    this.assertConnected();
    try {
      const res = await fetch(`${this.baseUrl}/unity/runs`, { headers: this.authHeaders });
      if (!res.ok) return [];
      const data = await res.json();
      return Array.isArray(data) ? data : [];
    } catch {
      return [];
    }
  }

  // ── Universal backend (any adapter) ──────────────────────────────

  async backendStatus(directory?: string): Promise<{ kind: string; url: string; running: boolean; error?: string; hint?: string; version?: string }> {
    this.assertConnected();
    const q = directory ? `?directory=${encodeURIComponent(directory)}` : "";
    const res = await fetch(`${this.baseUrl}/backend/status${q}`, { headers: this.authHeaders });
    return res.json();
  }

  async backendTables(directory?: string): Promise<{ backend?: string; tables?: { name: string; rowCount?: number; kind?: string }[]; error?: string }> {
    this.assertConnected();
    const q = directory ? `?directory=${encodeURIComponent(directory)}` : "";
    const res = await fetch(`${this.baseUrl}/backend/tables${q}`, { headers: this.authHeaders });
    return res.json();
  }

  async backendBrowse(table: string, opts: { cursor?: string; limit?: number; directory?: string } = {}): Promise<{ rows: any[]; nextCursor?: string; error?: string }> {
    this.assertConnected();
    const p = new URLSearchParams({ table });
    if (opts.cursor) p.set("cursor", opts.cursor);
    if (opts.limit) p.set("limit", String(opts.limit));
    if (opts.directory) p.set("directory", opts.directory);
    const res = await fetch(`${this.baseUrl}/backend/browse?${p}`, { headers: this.authHeaders });
    return res.json();
  }

  async backendQuery(query: string, args: Record<string, unknown> = {}, directory?: string): Promise<any> {
    this.assertConnected();
    const q = directory ? `?directory=${encodeURIComponent(directory)}` : "";
    const res = await fetch(`${this.baseUrl}/backend/query${q}`, {
      method: "POST",
      headers: { ...this.authHeaders, "Content-Type": "application/json" },
      body: JSON.stringify({ query, args }),
    });
    return res.json();
  }

  async backendInsert(table: string, doc: Record<string, unknown>, directory?: string): Promise<any> {
    this.assertConnected();
    const q = directory ? `?directory=${encodeURIComponent(directory)}` : "";
    const res = await fetch(`${this.baseUrl}/backend/insert${q}`, {
      method: "POST",
      headers: { ...this.authHeaders, "Content-Type": "application/json" },
      body: JSON.stringify({ table, doc }),
    });
    return res.json();
  }

  async backendUpdate(table: string, id: string, fields: Record<string, unknown>, directory?: string): Promise<any> {
    this.assertConnected();
    const q = directory ? `?directory=${encodeURIComponent(directory)}` : "";
    const res = await fetch(`${this.baseUrl}/backend/update${q}`, {
      method: "POST",
      headers: { ...this.authHeaders, "Content-Type": "application/json" },
      body: JSON.stringify({ table, id, fields }),
    });
    return res.json();
  }

  async backendDelete(table: string, id: string, directory?: string): Promise<any> {
    this.assertConnected();
    const q = directory ? `?directory=${encodeURIComponent(directory)}` : "";
    const res = await fetch(`${this.baseUrl}/backend/delete${q}`, {
      method: "POST",
      headers: { ...this.authHeaders, "Content-Type": "application/json" },
      body: JSON.stringify({ table, id }),
    });
    return res.json();
  }

  // ── Yaver Console (Docker + metrics + catalog) ───────────────────

  async consoleContainers(includeAll = false): Promise<{ containers?: any[]; error?: string }> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/console/containers${includeAll ? "?all=1" : ""}`, { headers: this.authHeaders });
    return res.json();
  }

  async consoleContainerAction(id: string, action: string): Promise<any> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/console/containers/action`, {
      method: "POST",
      headers: { ...this.authHeaders, "Content-Type": "application/json" },
      body: JSON.stringify({ id, action }),
    });
    return res.json();
  }

  async consoleContainerStats(id: string): Promise<any> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/console/containers/stats?id=${encodeURIComponent(id)}`, { headers: this.authHeaders });
    return res.json();
  }

  async consoleImages(): Promise<any> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/console/images`, { headers: this.authHeaders });
    return res.json();
  }

  async consolePrune(): Promise<any> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/console/prune`, { method: "POST", headers: this.authHeaders });
    return res.json();
  }

  // ── Ops: deploy / backups / domains / logs / errors / cron / uptime / clone ──

  async deployPreview(directory?: string): Promise<any> {
    this.assertConnected();
    const q = directory ? `?directory=${encodeURIComponent(directory)}` : "";
    const res = await fetch(`${this.baseUrl}/deploy/preview${q}`, { headers: this.authHeaders });
    return res.json();
  }

  async deployRun(directory?: string): Promise<any> {
    this.assertConnected();
    const q = directory ? `?directory=${encodeURIComponent(directory)}` : "";
    const res = await fetch(`${this.baseUrl}/deploy/run${q}`, { method: "POST", headers: this.authHeaders });
    return res.json();
  }
  async deployList(directory?: string): Promise<{ deploys: any[] }> {
    this.assertConnected();
    const q = directory ? `?directory=${encodeURIComponent(directory)}` : "";
    const res = await fetch(`${this.baseUrl}/deploy/list${q}`, { headers: this.authHeaders });
    return res.json();
  }
  async deployRollback(id: string, directory?: string): Promise<any> {
    this.assertConnected();
    const q = directory ? `?directory=${encodeURIComponent(directory)}` : "";
    const res = await fetch(`${this.baseUrl}/deploy/rollback${q}`, { method: "POST", headers: { ...this.authHeaders, "Content-Type": "application/json" }, body: JSON.stringify({ id }) });
    return res.json();
  }
  async deployConfigGet(directory?: string): Promise<any> {
    this.assertConnected();
    const q = directory ? `?directory=${encodeURIComponent(directory)}` : "";
    const res = await fetch(`${this.baseUrl}/deploy/config${q}`, { headers: this.authHeaders });
    return res.json();
  }
  async deployConfigSet(cfg: any, directory?: string): Promise<any> {
    this.assertConnected();
    const q = directory ? `?directory=${encodeURIComponent(directory)}` : "";
    const res = await fetch(`${this.baseUrl}/deploy/config${q}`, { method: "POST", headers: { ...this.authHeaders, "Content-Type": "application/json" }, body: JSON.stringify(cfg) });
    return res.json();
  }

  async backupCreate(directory?: string): Promise<any> {
    this.assertConnected();
    const q = directory ? `?directory=${encodeURIComponent(directory)}` : "";
    const res = await fetch(`${this.baseUrl}/backups/create${q}`, { method: "POST", headers: this.authHeaders });
    return res.json();
  }
  async backupList(directory?: string): Promise<{ backups: any[] }> {
    this.assertConnected();
    const q = directory ? `?directory=${encodeURIComponent(directory)}` : "";
    const res = await fetch(`${this.baseUrl}/backups/list${q}`, { headers: this.authHeaders });
    return res.json();
  }
  async backupRestore(id: string, directory?: string): Promise<any> {
    this.assertConnected();
    const q = directory ? `?directory=${encodeURIComponent(directory)}` : "";
    const res = await fetch(`${this.baseUrl}/backups/restore${q}`, { method: "POST", headers: { ...this.authHeaders, "Content-Type": "application/json" }, body: JSON.stringify({ id }) });
    return res.json();
  }
  async backupDelete(id: string, directory?: string): Promise<any> {
    this.assertConnected();
    const q = directory ? `?directory=${encodeURIComponent(directory)}` : "";
    const res = await fetch(`${this.baseUrl}/backups/delete${q}`, { method: "POST", headers: { ...this.authHeaders, "Content-Type": "application/json" }, body: JSON.stringify({ id }) });
    return res.json();
  }
  async backupAuto(enabled: boolean, everyHours: number, directory?: string): Promise<any> {
    this.assertConnected();
    const q = directory ? `?directory=${encodeURIComponent(directory)}` : "";
    const res = await fetch(`${this.baseUrl}/backups/auto${q}`, { method: "POST", headers: { ...this.authHeaders, "Content-Type": "application/json" }, body: JSON.stringify({ enabled, everyHours }) });
    return res.json();
  }

  async domainList(): Promise<{ domains: any[] }> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/domains/list`, { headers: this.authHeaders });
    return res.json();
  }
  async domainAdd(domain: string, upstream: string, staticPath?: string): Promise<any> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/domains/add`, { method: "POST", headers: { ...this.authHeaders, "Content-Type": "application/json" }, body: JSON.stringify({ domain, upstream, static: staticPath }) });
    return res.json();
  }
  async domainRemove(domain: string): Promise<any> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/domains/remove`, { method: "POST", headers: { ...this.authHeaders, "Content-Type": "application/json" }, body: JSON.stringify({ domain }) });
    return res.json();
  }

  async logSearch(q: string, services?: string, limit = 200): Promise<{ hits: any[]; count: number }> {
    this.assertConnected();
    const p = new URLSearchParams({ q, limit: String(limit) });
    if (services) p.set("services", services);
    const res = await fetch(`${this.baseUrl}/logs/search?${p}`, { headers: this.authHeaders });
    return res.json();
  }
  async logIndexStart(project?: string): Promise<any> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/logs/index/start`, { method: "POST", headers: { ...this.authHeaders, "Content-Type": "application/json" }, body: JSON.stringify({ project }) });
    return res.json();
  }

  async errorGroups(project?: string): Promise<{ groups: any[] }> {
    this.assertConnected();
    const p = new URLSearchParams();
    if (project) p.set("project", project);
    const res = await fetch(`${this.baseUrl}/errors/groups?${p}`, { headers: this.authHeaders });
    return res.json();
  }
  async errorInstances(fingerprint: string): Promise<{ instances: any[] }> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/errors/instances?fingerprint=${encodeURIComponent(fingerprint)}`, { headers: this.authHeaders });
    return res.json();
  }
  async errorResolve(fingerprint: string, resolved: boolean): Promise<any> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/errors/resolve`, { method: "POST", headers: { ...this.authHeaders, "Content-Type": "application/json" }, body: JSON.stringify({ fingerprint, resolved }) });
    return res.json();
  }

  async envClone(source: string, target: string, subsetRows = 0): Promise<any> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/env/clone`, { method: "POST", headers: { ...this.authHeaders, "Content-Type": "application/json" }, body: JSON.stringify({ source, target, subsetRows }) });
    return res.json();
  }

  async cronCreate(name: string, schedule: string, target: string, directory?: string): Promise<any> {
    this.assertConnected();
    const q = directory ? `?directory=${encodeURIComponent(directory)}` : "";
    const res = await fetch(`${this.baseUrl}/cron/create${q}`, { method: "POST", headers: { ...this.authHeaders, "Content-Type": "application/json" }, body: JSON.stringify({ name, schedule, target }) });
    return res.json();
  }
  async cronDelete(name: string, directory?: string): Promise<any> {
    this.assertConnected();
    const q = directory ? `?directory=${encodeURIComponent(directory)}` : "";
    const res = await fetch(`${this.baseUrl}/cron/delete${q}`, { method: "POST", headers: { ...this.authHeaders, "Content-Type": "application/json" }, body: JSON.stringify({ name }) });
    return res.json();
  }

  async uptimeList(): Promise<{ monitors: any[] }> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/uptime/list`, { headers: this.authHeaders });
    return res.json();
  }
  async uptimeAdd(monitor: any): Promise<any> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/uptime/add`, { method: "POST", headers: { ...this.authHeaders, "Content-Type": "application/json" }, body: JSON.stringify(monitor) });
    return res.json();
  }
  async uptimeRemove(id: string): Promise<any> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/uptime/remove`, { method: "POST", headers: { ...this.authHeaders, "Content-Type": "application/json" }, body: JSON.stringify({ id }) });
    return res.json();
  }

  // ── CI runner / alerts / metrics history / provider rotation / studio ──

  async ciRun(directory?: string): Promise<any> {
    this.assertConnected();
    const q = directory ? `?directory=${encodeURIComponent(directory)}` : "";
    const res = await fetch(`${this.baseUrl}/ci/run${q}`, { method: "POST", headers: this.authHeaders });
    return res.json();
  }
  async ciList(directory?: string): Promise<any> {
    this.assertConnected();
    const q = directory ? `?directory=${encodeURIComponent(directory)}` : "";
    const res = await fetch(`${this.baseUrl}/ci/list${q}`, { headers: this.authHeaders });
    return res.json();
  }
  async ciConfigGet(directory?: string): Promise<any> {
    this.assertConnected();
    const q = directory ? `?directory=${encodeURIComponent(directory)}` : "";
    const res = await fetch(`${this.baseUrl}/ci/config${q}`, { headers: this.authHeaders });
    return res.json();
  }
  async ciConfigSet(cfg: any, directory?: string): Promise<any> {
    this.assertConnected();
    const q = directory ? `?directory=${encodeURIComponent(directory)}` : "";
    const res = await fetch(`${this.baseUrl}/ci/config${q}`, { method: "POST", headers: { ...this.authHeaders, "Content-Type": "application/json" }, body: JSON.stringify(cfg) });
    return res.json();
  }

  async alertList(): Promise<{ alerts: any[] }> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/alerts/list`, { headers: this.authHeaders });
    return res.json();
  }
  async alertAdd(alert: any): Promise<any> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/alerts/add`, { method: "POST", headers: { ...this.authHeaders, "Content-Type": "application/json" }, body: JSON.stringify(alert) });
    return res.json();
  }
  async alertRemove(id: string): Promise<any> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/alerts/remove`, { method: "POST", headers: { ...this.authHeaders, "Content-Type": "application/json" }, body: JSON.stringify({ id }) });
    return res.json();
  }

  async metricsHistory(window = "1h"): Promise<{ samples: any[]; window: string }> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/console/metrics/history?window=${encodeURIComponent(window)}`, { headers: this.authHeaders });
    return res.json();
  }

  async backupEncryptionGet(directory?: string): Promise<{ enabled: boolean }> {
    this.assertConnected();
    const q = directory ? `?directory=${encodeURIComponent(directory)}` : "";
    const res = await fetch(`${this.baseUrl}/backups/encryption${q}`, { headers: this.authHeaders });
    return res.json();
  }
  async backupEncryptionSet(enabled: boolean, directory?: string): Promise<any> {
    this.assertConnected();
    const q = directory ? `?directory=${encodeURIComponent(directory)}` : "";
    const res = await fetch(`${this.baseUrl}/backups/encryption${q}`, { method: "POST", headers: { ...this.authHeaders, "Content-Type": "application/json" }, body: JSON.stringify({ enabled }) });
    return res.json();
  }

  async providerRotate(provider: string, opts: Record<string, string>): Promise<any> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/provider/rotate`, {
      method: "POST",
      headers: { ...this.authHeaders, "Content-Type": "application/json" },
      body: JSON.stringify({ provider, action: "rotate", opts }),
    });
    return res.json();
  }

  async studioList(): Promise<{ studios: any[] }> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/studios`, { headers: this.authHeaders });
    return res.json();
  }

  /** URL that proxies a local studio through the agent using a short-lived browser session token. */
  async studioProxyUrl(id: string): Promise<string> {
    const token = await this.issueBrowserSession(`/proxy/${encodeURIComponent(id)}/`);
    return `${this.baseUrl}/proxy/${encodeURIComponent(id)}/?browser_session=${encodeURIComponent(token)}`;
  }

  // ── Environment switcher + Overview summary ──────────────────────

  async overviewSummary(): Promise<any> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/overview/summary`, { headers: this.authHeaders });
    return res.json();
  }

  async projectEnvList(directory?: string): Promise<{ active: string; envs: string[] }> {
    this.assertConnected();
    const q = directory ? `?directory=${encodeURIComponent(directory)}` : "";
    const res = await fetch(`${this.baseUrl}/project/env/list${q}`, { headers: this.authHeaders });
    return res.json();
  }

  async projectEnvSwitch(name: string, directory?: string): Promise<any> {
    this.assertConnected();
    const q = directory ? `?directory=${encodeURIComponent(directory)}` : "";
    const res = await fetch(`${this.baseUrl}/project/env/switch${q}`, {
      method: "POST",
      headers: { ...this.authHeaders, "Content-Type": "application/json" },
      body: JSON.stringify({ name }),
    });
    return res.json();
  }

  async projectEnvSave(name: string, body: string, directory?: string): Promise<any> {
    this.assertConnected();
    const q = directory ? `?directory=${encodeURIComponent(directory)}` : "";
    const res = await fetch(`${this.baseUrl}/project/env/save${q}`, {
      method: "POST",
      headers: { ...this.authHeaders, "Content-Type": "application/json" },
      body: JSON.stringify({ name, body }),
    });
    return res.json();
  }

  async projectEnvLoad(name: string, directory?: string): Promise<{ name: string; body: string; error?: string }> {
    this.assertConnected();
    const p = new URLSearchParams({ name });
    if (directory) p.set("directory", directory);
    const res = await fetch(`${this.baseUrl}/project/env/load?${p}`, { headers: this.authHeaders });
    return res.json();
  }

  async multiRegionOrchestrate(name: string, regions: string[], domain: string, gitRepo: string, directory?: string): Promise<any> {
    this.assertConnected();
    const q = directory ? `?directory=${encodeURIComponent(directory)}` : "";
    const res = await fetch(`${this.baseUrl}/multiregion/orchestrate${q}`, {
      method: "POST",
      headers: { ...this.authHeaders, "Content-Type": "application/json" },
      body: JSON.stringify({ name, regions, domain, gitRepo }),
    });
    return res.json();
  }

  async consoleMachines(): Promise<{ machines: MachineInfo[] }> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/console/machines`, { headers: this.authHeaders });
    return res.json();
  }

  async infraSummary(target?: string): Promise<InfraSummary> {
    this.assertConnected();
    const base = target
      ? `${this.baseUrl}/peer/${encodeURIComponent(target)}/infra/summary`
      : `${this.baseUrl}/infra/summary`;
    const res = await fetch(base, { headers: this.authHeaders });
    return res.json();
  }

  async capabilitySnapshot(): Promise<CapabilitySnapshot> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/capabilities/snapshot`, { headers: this.authHeaders });
    const data = await res.json();
    return data.snapshot as CapabilitySnapshot;
  }

  /** Per-target deploy capability — yes/no + structured Reason + the
   *  per-tool / per-secret detail rows the dashboard needs to render
   *  "Deploy to TestFlight" with the right disabled state.
   *  Mirrors mobile's quicClient.deployCapabilities — keeping the
   *  shape identical means the same UI component can render against
   *  either client. */
  async deployCapabilities(args?: { target?: string; project?: string }): Promise<DeployCapabilitiesReport> {
    this.assertConnected();
    const params = new URLSearchParams();
    if (args?.target) params.set("target", args.target);
    if (args?.project) params.set("project", args.project);
    const qs = params.toString();
    const res = await fetch(
      `${this.baseUrl}/deploy/capabilities${qs ? `?${qs}` : ""}`,
      { headers: this.authHeaders },
    );
    if (!res.ok) throw new Error(`deployCapabilities ${res.status}`);
    const data = await res.json();
    const targets = Array.isArray(data?.targets) ? data.targets : [];
    return {
      deviceId: String(data?.device_id ?? ""),
      platform: String(data?.platform ?? ""),
      arch: String(data?.arch ?? ""),
      isWsl: !!data?.is_wsl,
      targets: targets.map((t: any) => ({
        target: String(t?.target ?? ""),
        stack: t?.stack ? String(t.stack) : undefined,
        canDeploy: !!t?.can_deploy,
        platformLock: t?.platform_lock ? String(t.platform_lock) : undefined,
        tools: Array.isArray(t?.tools)
          ? t.tools.map((tool: any) => ({
              name: String(tool?.name ?? ""),
              required: !!tool?.required,
              found: !!tool?.found,
              path: tool?.path ? String(tool.path) : undefined,
              version: tool?.version ? String(tool.version) : undefined,
              installHint: tool?.install_hint ? String(tool.install_hint) : undefined,
              deepValid: typeof tool?.deep_valid === "boolean" ? tool.deep_valid : undefined,
              deepError: tool?.deep_error ? String(tool.deep_error) : undefined,
              platformSkipped: !!tool?.platform_skipped,
              skipReason: tool?.skip_reason ? String(tool.skip_reason) : undefined,
            }))
          : undefined,
        secrets: Array.isArray(t?.secrets)
          ? t.secrets.map((s: any) => ({
              name: String(s?.name ?? ""),
              found: !!s?.found,
              source: s?.source ? String(s.source) : undefined,
              project: s?.project ? String(s.project) : undefined,
              pathValid: typeof s?.path_valid === "boolean" ? s.path_valid : undefined,
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

  /** Outbound P2P vault sync. Counterpart of mobile's
   *  vaultPeerSync — wired to /vault/peer-sync on the agent. */
  async vaultPeerSync(args?: { from?: string }): Promise<VaultPeerSyncResult> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/vault/peer-sync`, {
      method: "POST",
      headers: { ...this.authHeaders, "Content-Type": "application/json" },
      body: JSON.stringify({ from: args?.from ?? "" }),
    });
    if (!res.ok) throw new Error(`vaultPeerSync ${res.status}`);
    const data = await res.json();
    const results = Array.isArray(data?.results) ? data.results : [];
    const totals = data?.totals ?? {};
    return {
      peers: Array.isArray(data?.peers) ? data.peers.map(String) : [],
      results: results.map((r: any) => ({
        peer: String(r?.peer ?? ""),
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

  async incidents(opts: {
    category?: string;
    severity?: string;
    code?: string;
    device?: string;
    projectPath?: string;
    includeResolved?: boolean;
    limit?: number;
  } = {}): Promise<IncidentEvent[]> {
    this.assertConnected();
    const p = new URLSearchParams();
    if (opts.category) p.set("category", opts.category);
    if (opts.severity) p.set("severity", opts.severity);
    if (opts.code) p.set("code", opts.code);
    if (opts.device) p.set("device", opts.device);
    if (opts.projectPath) p.set("projectPath", opts.projectPath);
    if (opts.includeResolved) p.set("include_resolved", "1");
    if (opts.limit) p.set("limit", String(opts.limit));
    const res = await fetch(`${this.baseUrl}/incidents?${p.toString()}`, { headers: this.authHeaders });
    const data = await res.json();
    return (data.incidents ?? []) as IncidentEvent[];
  }

  async incidentSummary(): Promise<IncidentSummary> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/incidents/summary`, { headers: this.authHeaders });
    const data = await res.json();
    return data.summary as IncidentSummary;
  }

  async operations(opts: {
    kind?: string;
    status?: string;
    device?: string;
    projectPath?: string;
    limit?: number;
  } = {}): Promise<OperationState[]> {
    this.assertConnected();
    const p = new URLSearchParams();
    if (opts.kind) p.set("kind", opts.kind);
    if (opts.status) p.set("status", opts.status);
    if (opts.device) p.set("device", opts.device);
    if (opts.projectPath) p.set("projectPath", opts.projectPath);
    if (opts.limit) p.set("limit", String(opts.limit));
    const res = await fetch(`${this.baseUrl}/operations?${p.toString()}`, { headers: this.authHeaders });
    const data = await res.json();
    return (data.operations ?? []) as OperationState[];
  }

  async tailscaleStatus(): Promise<TailscaleStatus> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/machine/tailscale`, { headers: this.authHeaders });
    const data = await res.json();
    return data?.status || { running: false };
  }

  async infraServiceAction(scope: "dev" | "system", name: string, action: "start" | "stop" | "restart" | "status"): Promise<any> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/infra/services/action`, {
      method: "POST",
      headers: { ...this.authHeaders, "Content-Type": "application/json" },
      body: JSON.stringify({ scope, name, action }),
    });
    return res.json();
  }

  async infraPower(action: "agent_shutdown" | "host_reboot"): Promise<any> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/infra/power`, {
      method: "POST",
      headers: { ...this.authHeaders, "Content-Type": "application/json" },
      body: JSON.stringify({ action, confirm: true }),
    });
    return res.json();
  }

  async machineRemove(phrase: string): Promise<any> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/machine/remove`, {
      method: "POST",
      headers: { ...this.authHeaders, "Content-Type": "application/json" },
      body: JSON.stringify({ confirm: true, phrase }),
    });
    return res.json();
  }

  async consoleMetricsSnapshot(): Promise<any> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/console/metrics`, { headers: this.authHeaders });
    return res.json();
  }

  async consoleCatalog(): Promise<any> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/console/catalog`, { headers: this.authHeaders });
    return res.json();
  }

  async consoleCatalogInstall(id: string, fields: Record<string, string>, directory?: string): Promise<any> {
    this.assertConnected();
    const q = directory ? `?directory=${encodeURIComponent(directory)}` : "";
    const res = await fetch(`${this.baseUrl}/console/catalog/install${q}`, {
      method: "POST",
      headers: { ...this.authHeaders, "Content-Type": "application/json" },
      body: JSON.stringify({ id, fields }),
    });
    return res.json();
  }

  // WebSocket URL builders — issue short-lived browser session tokens so the
  // browser never has to put the real bearer token into a URL. When the
  // dashboard is talking to the agent through the relay, we also append
  // `__rp=<password>` because browsers can't set custom headers on WS
  // upgrades and the relay's password gate (relay/server.go:953) rejects
  // the upgrade with 401 before it ever reaches the agent.
  async metricsWsUrl(): Promise<string> {
    const token = await this.issueBrowserSession("/ws/metrics");
    return this.appendRelayPwToWs(`${this.baseUrl.replace(/^http/, "ws")}/ws/metrics?browser_session=${encodeURIComponent(token)}`);
  }
  async containerLogsWsUrl(id: string): Promise<string> {
    const token = await this.issueBrowserSession("/ws/logs");
    return this.appendRelayPwToWs(`${this.baseUrl.replace(/^http/, "ws")}/ws/logs?id=${encodeURIComponent(id)}&browser_session=${encodeURIComponent(token)}`);
  }
  async terminalWsUrl(cwd?: string): Promise<string> {
    const token = await this.issueBrowserSession("/ws/terminal");
    const c = cwd ? `&cwd=${encodeURIComponent(cwd)}` : "";
    return this.appendRelayPwToWs(`${this.baseUrl.replace(/^http/, "ws")}/ws/terminal?browser_session=${encodeURIComponent(token)}${c}`);
  }

  private appendRelayPwToWs(url: string): string {
    if (!this._activeRelayUrl || !this.activeRelayPassword) return url;
    const join = url.includes("?") ? "&" : "?";
    return `${url}${join}__rp=${encodeURIComponent(this.activeRelayPassword)}`;
  }

  // ── Vault (secrets stored encrypted on host disk) ─────────────────
  //
  // GET /vault/list returns summaries — never values. Use vaultGet
  // to reveal one at a time (audit trail lives on the host).

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
      method: "POST",
      headers: { ...this.authHeaders, "Content-Type": "application/json" },
      body: JSON.stringify(entry),
    });
    if (!res.ok) throw new Error(`vault set: HTTP ${res.status}`);
  }

  async vaultDelete(name: string): Promise<void> {
    this.assertConnected();
    const res = await fetch(
      `${this.baseUrl}/vault/delete?name=${encodeURIComponent(name)}`,
      { method: "DELETE", headers: this.authHeaders },
    );
    if (!res.ok) throw new Error(`vault delete: HTTP ${res.status}`);
  }

  // ── Yaver Agent (mobile-embedded control-plane LLM) provider config ─

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
      method: "POST",
      headers: { ...this.authHeaders, "Content-Type": "application/json" },
      body: JSON.stringify(req),
    });
    if (!res.ok) {
      const data = await res.json().catch(() => null);
      throw new Error(data?.error || `yaver-agent config set: HTTP ${res.status}`);
    }
    return res.json();
  }

  // ── API keys (SDK-token registry with labels + usage) ─────────────

  async apiKeyList(): Promise<APIKeyRecord[]> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/apikeys`, { headers: this.authHeaders });
    if (!res.ok) throw new Error(`apikey list: HTTP ${res.status}`);
    const data = await res.json();
    return Array.isArray(data?.keys) ? data.keys : [];
  }

  // Returns the raw token once — the server never exposes it again.
  async apiKeyCreate(opts: { label: string; scopes?: string[]; expiresInMs?: number; allowedCIDRs?: string[] }): Promise<{ token: string; tokenHash: string; label: string; scopes?: string[] }> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/apikeys`, {
      method: "POST",
      headers: { ...this.authHeaders, "Content-Type": "application/json" },
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
      { method: "DELETE", headers: this.authHeaders },
    );
    if (!res.ok) throw new Error(`apikey disable: HTTP ${res.status}`);
  }

  // ── Exec (compute: run commands, poll / stream output) ────────────
  //
  // Mirrors the shape already in mobile/src/lib/quic.ts so UI code
  // can be written against the same interface on both surfaces.

  async listExecs(): Promise<ExecSnapshot[]> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/exec`, { headers: this.authHeaders });
    if (!res.ok) throw new Error(`exec list: HTTP ${res.status}`);
    const data = await res.json();
    return Array.isArray(data?.execs) ? data.execs : [];
  }

  async startExec(opts: { command: string; workDir?: string; shell?: string; timeout?: number; env?: Record<string, string> }): Promise<{ execId: string; pid?: number }> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/exec`, {
      method: "POST",
      headers: { ...this.authHeaders, "Content-Type": "application/json" },
      body: JSON.stringify(opts),
    });
    const data = await res.json();
    if (!res.ok) throw new Error(data?.error || `exec start: HTTP ${res.status}`);
    return data;
  }

  async getExec(execId: string): Promise<ExecSnapshot | null> {
    this.assertConnected();
    const res = await fetch(
      `${this.baseUrl}/exec/${encodeURIComponent(execId)}`,
      { headers: this.authHeaders },
    );
    if (res.status === 404) return null;
    if (!res.ok) throw new Error(`exec get: HTTP ${res.status}`);
    const data = await res.json();
    return data?.exec ?? null;
  }

  async killExec(execId: string): Promise<void> {
    this.assertConnected();
    const res = await fetch(
      `${this.baseUrl}/exec/${encodeURIComponent(execId)}`,
      { method: "DELETE", headers: this.authHeaders },
    );
    if (!res.ok) throw new Error(`exec kill: HTTP ${res.status}`);
  }

  async sendExecInput(execId: string, input: string): Promise<void> {
    this.assertConnected();
    const res = await fetch(
      `${this.baseUrl}/exec/${encodeURIComponent(execId)}/input`,
      {
        method: "POST",
        headers: { ...this.authHeaders, "Content-Type": "application/json" },
        body: JSON.stringify({ input }),
      },
    );
    if (!res.ok) throw new Error(`exec input: HTTP ${res.status}`);
  }

  // ── Schedules (one-shot + cron + repeat interval) ───────────────

  async listSchedules(): Promise<ScheduledTask[]> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/schedules`, { headers: this.authHeaders });
    if (!res.ok) throw new Error(`schedules list: HTTP ${res.status}`);
    const data = await res.json();
    return Array.isArray(data?.schedules) ? data.schedules : [];
  }

  // Pass a partial ScheduledTask — server fills in id/createdAt/status.
  async createSchedule(
    spec: Omit<Partial<ScheduledTask>, "id" | "createdAt" | "status" | "runCount"> & { title: string },
  ): Promise<ScheduledTask> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/schedules`, {
      method: "POST",
      headers: { ...this.authHeaders, "Content-Type": "application/json" },
      body: JSON.stringify(spec),
    });
    const data = await res.json();
    if (!res.ok) throw new Error(data?.error || `schedule create: HTTP ${res.status}`);
    return data.schedule;
  }

  async getSchedule(id: string): Promise<ScheduledTask | null> {
    this.assertConnected();
    const res = await fetch(
      `${this.baseUrl}/schedules/${encodeURIComponent(id)}`,
      { headers: this.authHeaders },
    );
    if (res.status === 404) return null;
    if (!res.ok) throw new Error(`schedule get: HTTP ${res.status}`);
    const data = await res.json();
    return data?.schedule ?? null;
  }

  async deleteSchedule(id: string): Promise<void> {
    this.assertConnected();
    const res = await fetch(
      `${this.baseUrl}/schedules/${encodeURIComponent(id)}`,
      { method: "DELETE", headers: this.authHeaders },
    );
    if (!res.ok) throw new Error(`schedule delete: HTTP ${res.status}`);
  }

  async pauseSchedule(id: string): Promise<void> {
    this.assertConnected();
    const res = await fetch(
      `${this.baseUrl}/schedules/${encodeURIComponent(id)}/pause`,
      { method: "POST", headers: this.authHeaders },
    );
    if (!res.ok) throw new Error(`schedule pause: HTTP ${res.status}`);
  }

  async resumeSchedule(id: string): Promise<void> {
    this.assertConnected();
    const res = await fetch(
      `${this.baseUrl}/schedules/${encodeURIComponent(id)}/resume`,
      { method: "POST", headers: this.authHeaders },
    );
    if (!res.ok) throw new Error(`schedule resume: HTTP ${res.status}`);
  }

  // Fire a scheduled task immediately without altering its cadence.
  async runScheduleNow(id: string): Promise<void> {
    this.assertConnected();
    const res = await fetch(
      `${this.baseUrl}/schedules/${encodeURIComponent(id)}/run-now`,
      { method: "POST", headers: this.authHeaders },
    );
    if (!res.ok) throw new Error(`schedule run-now: HTTP ${res.status}`);
  }

  async signalExec(execId: string, signal: string): Promise<void> {
    this.assertConnected();
    const res = await fetch(
      `${this.baseUrl}/exec/${encodeURIComponent(execId)}/signal`,
      {
        method: "POST",
        headers: { ...this.authHeaders, "Content-Type": "application/json" },
        body: JSON.stringify({ signal }),
      },
    );
    if (!res.ok) throw new Error(`exec signal: HTTP ${res.status}`);
  }

  // ── Blobs (simple key-value object storage on the host) ───────────

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
    if (opts.limit) q.set("limit", String(opts.limit));
    if (opts.after) q.set("after", opts.after);
    const suffix = q.toString() ? `?${q.toString()}` : "";
    const res = await fetch(
      `${this.baseUrl}/blobs/${encodeURIComponent(bucket)}${suffix}`,
      { headers: this.authHeaders },
    );
    if (!res.ok) throw new Error(`blobs list: HTTP ${res.status}`);
    const data = await res.json();
    // Server returns both `keys` (preferred) and `items` (back-compat).
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
      { method: "DELETE", headers: this.authHeaders },
    );
    if (!res.ok) throw new Error(`blob delete: HTTP ${res.status}`);
  }

  // Generate a time-limited HMAC-signed URL anyone can open (the
  // agent's /blobs/public handler verifies the signature). TTL in
  // seconds; default 300 (5 min). Returned URL is fully qualified
  // against the agent's base — careful when sharing if the agent is
  // on a LAN-only IP.
  async blobsSignUrl(bucket: string, key: string, ttlSeconds = 300): Promise<{ url: string; expiresIn: number }> {
    this.assertConnected();
    const p = new URLSearchParams({ ttl: String(ttlSeconds) });
    const res = await fetch(
      `${this.baseUrl}/blobs/url/${encodeURIComponent(bucket)}/${encodeURIComponent(key)}?${p}`,
      { headers: this.authHeaders },
    );
    if (!res.ok) throw new Error(`blob sign: HTTP ${res.status}`);
    return res.json();
  }

  // Authenticated fetch + trigger a browser download. Bytes stay
  // between agent and this tab — no redirect or public URL generated
  // unless the caller explicitly uses blobsSignUrl.
  async blobsDownload(bucket: string, key: string): Promise<void> {
    this.assertConnected();
    const res = await fetch(
      `${this.baseUrl}/blobs/${encodeURIComponent(bucket)}/${encodeURIComponent(key)}`,
      { headers: this.authHeaders },
    );
    if (!res.ok) throw new Error(`blob download: HTTP ${res.status}`);
    const blob = await res.blob();
    const url = URL.createObjectURL(blob);
    const a = document.createElement("a");
    a.href = url;
    a.download = key.split("/").pop() || key;
    document.body.appendChild(a);
    a.click();
    a.remove();
    // Free the object URL after the click has kicked off.
    setTimeout(() => URL.revokeObjectURL(url), 5_000);
  }

  // PUT a File straight into a bucket. The agent persists it to
  // ~/.yaver/blobs/<bucket>/<key> and returns metadata. Bytes never
  // transit Convex. `onProgress` receives (loaded, total) pairs so
  // the caller can draw a progress bar. Falls back to XHR because
  // fetch() has no upload-progress event in any browser today.
  async blobsUpload(
    bucket: string,
    key: string,
    file: File,
    onProgress?: (loaded: number, total: number) => void,
  ): Promise<{ key: string; size?: number; contentType?: string }> {
    this.assertConnected();
    const url = `${this.baseUrl}/blobs/${encodeURIComponent(bucket)}/${encodeURIComponent(key)}`;

    if (!onProgress) {
      const res = await fetch(url, {
        method: "PUT",
        headers: {
          ...this.authHeaders,
          "Content-Type": file.type || "application/octet-stream",
        },
        body: file,
      });
      const data = await res.json();
      if (!res.ok) throw new Error(data?.error || `blob upload: HTTP ${res.status}`);
      return data.blob;
    }

    // Progress-aware path: XHR is the only cross-browser way to get
    // upload.onprogress events. We still respect the same auth +
    // content-type contract.
    return new Promise((resolve, reject) => {
      const xhr = new XMLHttpRequest();
      xhr.open("PUT", url);
      for (const [k, v] of Object.entries(this.authHeaders)) {
        xhr.setRequestHeader(k, v);
      }
      xhr.setRequestHeader("Content-Type", file.type || "application/octet-stream");
      xhr.upload.onprogress = (e) => {
        if (e.lengthComputable) onProgress(e.loaded, e.total);
      };
      xhr.onload = () => {
        try {
          const data = JSON.parse(xhr.responseText || "{}");
          if (xhr.status >= 200 && xhr.status < 300) {
            resolve(data.blob);
          } else {
            reject(new Error(data?.error || `blob upload: HTTP ${xhr.status}`));
          }
        } catch {
          reject(new Error(`blob upload: HTTP ${xhr.status}`));
        }
      };
      xhr.onerror = () => reject(new Error("blob upload: network error"));
      xhr.send(file);
    });
  }

  // ── Files (read-only project browser) ─────────────────────────────

  async filesRoots(): Promise<{ roots: { id: string; name: string; path: string }[] }> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/files/roots`, { headers: this.authHeaders });
    if (!res.ok) throw new Error(`files roots: HTTP ${res.status}`);
    return res.json();
  }

  async filesList(root: string, path = ""): Promise<any> {
    this.assertConnected();
    const p = new URLSearchParams({ root });
    if (path) p.set("path", path);
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

  async analyzeConversationImport(body: {
    url?: string;
    content?: string;
    title?: string;
    runner?: string;
    workDir?: string;
  }): Promise<ConversationImportPlan> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/imports/conversation/plan`, {
      method: "POST",
      headers: { ...this.authHeaders, "Content-Type": "application/json" },
      body: JSON.stringify(body),
    });
    const data = await res.json().catch(() => ({}));
    if (!res.ok) throw new Error(data?.error || `HTTP ${res.status}`);
    return data as ConversationImportPlan;
  }

  // ── Schema / storage / jobs / logs SSE ───────────────────────────

  async backendSchema(directory?: string): Promise<any> {
    this.assertConnected();
    const q = directory ? `?directory=${encodeURIComponent(directory)}` : "";
    const res = await fetch(`${this.baseUrl}/backend/schema${q}`, { headers: this.authHeaders });
    return res.json();
  }

  async storageList(bucket?: string, directory?: string): Promise<any> {
    this.assertConnected();
    const p = new URLSearchParams();
    if (bucket) p.set("bucket", bucket);
    if (directory) p.set("directory", directory);
    const res = await fetch(`${this.baseUrl}/storage/list?${p}`, { headers: this.authHeaders });
    return res.json();
  }

  async sharedStorageProfiles(): Promise<any> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/shared-storage/profiles`, { headers: this.authHeaders });
    return res.json();
  }

  async sharedStorageUpsert(profile: Record<string, any>): Promise<any> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/shared-storage/profiles`, {
      method: "POST",
      headers: { ...this.authHeaders, "Content-Type": "application/json" },
      body: JSON.stringify(profile),
    });
    return res.json();
  }

  async sharedStorageDelete(id: string): Promise<any> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/shared-storage/profile/delete`, {
      method: "POST",
      headers: { ...this.authHeaders, "Content-Type": "application/json" },
      body: JSON.stringify({ id }),
    });
    return res.json();
  }

  async sharedStorageList(id: string, path = ""): Promise<any> {
    this.assertConnected();
    const p = new URLSearchParams();
    p.set("id", id);
    if (path) p.set("path", path);
    const res = await fetch(`${this.baseUrl}/shared-storage/list?${p}`, { headers: this.authHeaders });
    return res.json();
  }

  async sharedStorageRead(id: string, path: string): Promise<any> {
    this.assertConnected();
    const p = new URLSearchParams({ id, path });
    const res = await fetch(`${this.baseUrl}/shared-storage/read?${p}`, { headers: this.authHeaders });
    return res.json();
  }

  sharedStorageRawUrl(id: string, path: string): string {
    const p = new URLSearchParams({ id, path });
    return `${this.baseUrl}/shared-storage/raw?${p.toString()}`;
  }

  async sharedStorageSearch(query: string, opts: { id?: string; path?: string; limit?: number } = {}): Promise<any> {
    this.assertConnected();
    const p = new URLSearchParams({ q: query });
    if (opts.id) p.set("id", opts.id);
    if (opts.path) p.set("path", opts.path);
    if (opts.limit) p.set("limit", String(opts.limit));
    const res = await fetch(`${this.baseUrl}/shared-storage/search?${p}`, { headers: this.authHeaders });
    return res.json();
  }

  async jobsList(directory?: string): Promise<any> {
    this.assertConnected();
    const q = directory ? `?directory=${encodeURIComponent(directory)}` : "";
    const res = await fetch(`${this.baseUrl}/jobs/list${q}`, { headers: this.authHeaders });
    return res.json();
  }

  async switchCost(directory?: string): Promise<any> {
    this.assertConnected();
    const q = directory ? `?directory=${encodeURIComponent(directory)}` : "";
    const res = await fetch(`${this.baseUrl}/switch/cost${q}`, { headers: this.authHeaders });
    return res.json();
  }

  logsSseUrl(service: string, tail = 50): string {
    return `${this.baseUrl}/logs/stream?service=${encodeURIComponent(service)}&tail=${tail}`;
  }

  // ── Accounts (cloud provider credentials) ────────────────────────

  async accountsList(): Promise<{ accounts: any[]; providers: any[] }> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/accounts`, { headers: this.authHeaders });
    return res.json();
  }

  async accountConnect(provider: string, label: string, fields: Record<string, string>): Promise<any> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/accounts/connect`, {
      method: "POST",
      headers: { ...this.authHeaders, "Content-Type": "application/json" },
      body: JSON.stringify({ provider, label, fields }),
    });
    return res.json();
  }

  async accountDisconnect(provider: string): Promise<any> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/accounts/disconnect`, {
      method: "POST",
      headers: { ...this.authHeaders, "Content-Type": "application/json" },
      body: JSON.stringify({ provider }),
    });
    return res.json();
  }

  // ── Switch engine ────────────────────────────────────────────────

  async switchTargets(): Promise<{ targets: any[] }> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/switch/targets`, { headers: this.authHeaders });
    return res.json();
  }

  async switchPlan(target: string, opts: { dryRun?: boolean; directory?: string } = {}): Promise<any> {
    this.assertConnected();
    const q = opts.directory ? `?directory=${encodeURIComponent(opts.directory)}` : "";
    const res = await fetch(`${this.baseUrl}/switch/plan${q}`, {
      method: "POST",
      headers: { ...this.authHeaders, "Content-Type": "application/json" },
      body: JSON.stringify({ target, dryRun: !!opts.dryRun }),
    });
    return res.json();
  }

  async switchRun(id: string, directory?: string): Promise<any> {
    this.assertConnected();
    const q = directory ? `?directory=${encodeURIComponent(directory)}` : "";
    const res = await fetch(`${this.baseUrl}/switch/run${q}`, {
      method: "POST",
      headers: { ...this.authHeaders, "Content-Type": "application/json" },
      body: JSON.stringify({ id }),
    });
    return res.json();
  }

  async switchRollback(id: string, directory?: string): Promise<any> {
    this.assertConnected();
    const q = directory ? `?directory=${encodeURIComponent(directory)}` : "";
    const res = await fetch(`${this.baseUrl}/switch/rollback${q}`, {
      method: "POST",
      headers: { ...this.authHeaders, "Content-Type": "application/json" },
      body: JSON.stringify({ id }),
    });
    return res.json();
  }

  async switchHistory(directory?: string): Promise<{ switches: any[] }> {
    this.assertConnected();
    const q = directory ? `?directory=${encodeURIComponent(directory)}` : "";
    const res = await fetch(`${this.baseUrl}/switch/history${q}`, { headers: this.authHeaders });
    return res.json();
  }

  async switchCleanup(directory?: string): Promise<any> {
    this.assertConnected();
    const q = directory ? `?directory=${encodeURIComponent(directory)}` : "";
    const res = await fetch(`${this.baseUrl}/switch/cleanup${q}`, { method: "POST", headers: this.authHeaders });
    return res.json();
  }

  async projectRuntime(directory?: string): Promise<ProjectRuntimeSummary> {
    this.assertConnected();
    const q = directory ? `?directory=${encodeURIComponent(directory)}` : "";
    const res = await fetch(`${this.baseUrl}/project/runtime${q}`, { headers: this.authHeaders });
    return res.json();
  }

  async projectRuntimeApply(
    req: ProjectRuntimeApplyRequest,
    directory?: string,
  ): Promise<ProjectRuntimeApplyResponse> {
    this.assertConnected();
    const q = directory ? `?directory=${encodeURIComponent(directory)}` : "";
    const res = await fetch(`${this.baseUrl}/project/runtime/apply${q}`, {
      method: "POST",
      headers: { ...this.authHeaders, "Content-Type": "application/json" },
      body: JSON.stringify(req),
    });
    return res.json();
  }

  // ── Cloud emulators ──────────────────────────────────────────────

  async cloudEmuStatus(directory?: string): Promise<{ emulators: { name: string; provider: string; running: boolean; port: number; health: string }[] }> {
    this.assertConnected();
    const q = directory ? `?directory=${encodeURIComponent(directory)}` : "";
    const res = await fetch(`${this.baseUrl}/cloud/emu/status${q}`, { headers: this.authHeaders });
    return res.json();
  }

  async cloudEmuStart(provider: string, services: string[] = [], directory?: string): Promise<any> {
    this.assertConnected();
    const q = directory ? `?directory=${encodeURIComponent(directory)}` : "";
    const res = await fetch(`${this.baseUrl}/cloud/emu/start${q}`, {
      method: "POST",
      headers: { ...this.authHeaders, "Content-Type": "application/json" },
      body: JSON.stringify({ provider, services }),
    });
    return res.json();
  }

  async cloudEmuStop(provider: string, services: string[] = [], directory?: string): Promise<any> {
    this.assertConnected();
    const q = directory ? `?directory=${encodeURIComponent(directory)}` : "";
    const res = await fetch(`${this.baseUrl}/cloud/emu/stop${q}`, {
      method: "POST",
      headers: { ...this.authHeaders, "Content-Type": "application/json" },
      body: JSON.stringify({ provider, services }),
    });
    return res.json();
  }

  async cloudEmuConfig(provider: string): Promise<any> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/cloud/emu/config?provider=${encodeURIComponent(provider)}`, { headers: this.authHeaders });
    return res.json();
  }

  // ── Convex local backend ─────────────────────────────────────────

  async convexStatus(directory?: string): Promise<{ url: string; running: boolean; error?: string; hint?: string }> {
    this.assertConnected();
    const q = directory ? `?directory=${encodeURIComponent(directory)}` : "";
    const res = await fetch(`${this.baseUrl}/convex/status${q}`, { headers: this.authHeaders });
    return res.json();
  }

  async convexTables(directory?: string): Promise<any> {
    this.assertConnected();
    const q = directory ? `?directory=${encodeURIComponent(directory)}` : "";
    const res = await fetch(`${this.baseUrl}/convex/tables${q}`, { headers: this.authHeaders });
    return res.json();
  }

  async convexBrowse(table: string, opts: { cursor?: string; limit?: number; directory?: string } = {}): Promise<any> {
    this.assertConnected();
    const p = new URLSearchParams({ table });
    if (opts.cursor) p.set("cursor", opts.cursor);
    if (opts.limit) p.set("limit", String(opts.limit));
    if (opts.directory) p.set("directory", opts.directory);
    const res = await fetch(`${this.baseUrl}/convex/browse?${p}`, { headers: this.authHeaders });
    return res.json();
  }

  async convexCall(
    kind: "query" | "mutate" | "action",
    fn: string,
    args: Record<string, unknown> = {},
    directory?: string,
  ): Promise<any> {
    this.assertConnected();
    const q = directory ? `?directory=${encodeURIComponent(directory)}` : "";
    const res = await fetch(`${this.baseUrl}/convex/${kind}${q}`, {
      method: "POST",
      headers: { ...this.authHeaders, "Content-Type": "application/json" },
      body: JSON.stringify({ function: fn, args }),
    });
    return res.json();
  }

  async convexSchema(directory?: string): Promise<{ path?: string; schema?: string; error?: string }> {
    this.assertConnected();
    const q = directory ? `?directory=${encodeURIComponent(directory)}` : "";
    const res = await fetch(`${this.baseUrl}/convex/schema${q}`, { headers: this.authHeaders });
    return res.json();
  }

  async convexInstallHelper(directory: string): Promise<any> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/convex/install-helper?directory=${encodeURIComponent(directory)}`, {
      method: "POST",
      headers: this.authHeaders,
    });
    return res.json();
  }

  // ── Health Monitoring ─────────────────────────────────────────────

  async listHealthTargets(): Promise<{ id: string; url: string; name?: string; status?: string; responseTime?: number }[]> {
    this.assertConnected();
    try {
      const res = await fetch(`${this.baseUrl}/healthmon`, { headers: this.authHeaders });
      if (!res.ok) return [];
      const data = await res.json();
      return data.targets ?? data ?? [];
    } catch { return []; }
  }

  async addHealthTarget(target: { url: string; name?: string }): Promise<void> {
    this.assertConnected();
    await fetch(`${this.baseUrl}/healthmon`, {
      method: "POST",
      headers: { ...this.authHeaders, "Content-Type": "application/json" },
      body: JSON.stringify(target),
    });
  }

  async deleteHealthTarget(id: string): Promise<void> {
    this.assertConnected();
    await fetch(`${this.baseUrl}/healthmon/${id}`, { method: "DELETE", headers: this.authHeaders });
  }

  // ── Machine health (disk + SMART + peer heartbeat) ──────────────

  async machineHealth(): Promise<{
    hostname: string;
    os: string;
    updatedAt: string;
    filesystems: { mount: string; totalGb: number; usedGb: number; freeGb: number; usedPct: number; device?: string; fsType?: string }[];
    drives: { device: string; model?: string; health: "passed" | "failing" | "unknown"; temperatureC?: number; powerOnHours?: number }[];
    alerts?: string[];
  } | null> {
    this.assertConnected();
    try {
      const res = await fetch(`${this.baseUrl}/machine/health`, { headers: this.authHeaders });
      if (!res.ok) return null;
      const data = await res.json();
      return data.health ?? null;
    } catch { return null; }
  }

  async machinePeers(): Promise<{ deviceId: string; name?: string; lastSeen: string; state: "online" | "stale" | "offline" }[]> {
    this.assertConnected();
    try {
      const res = await fetch(`${this.baseUrl}/machine/peers`, { headers: this.authHeaders });
      if (!res.ok) return [];
      const data = await res.json();
      return data.peers ?? [];
    } catch { return []; }
  }

  // ── Quality Gates ─────────────────────────────────────────────────

  async listQualityGates(): Promise<{ id?: string; type?: string; name?: string; status?: string }[]> {
    this.assertConnected();
    try {
      const res = await fetch(`${this.baseUrl}/quality`, { headers: this.authHeaders });
      if (!res.ok) return [];
      const data = await res.json();
      return data.checks ?? data ?? [];
    } catch { return []; }
  }

  async runQualityGate(id: string): Promise<void> {
    this.assertConnected();
    await fetch(`${this.baseUrl}/quality/${id}/run`, { method: "POST", headers: this.authHeaders });
  }

  async runAllQualityGates(): Promise<void> {
    this.assertConnected();
    await fetch(`${this.baseUrl}/quality/run-all`, { method: "POST", headers: this.authHeaders });
  }

  // ── Git ───────────────────────────────────────────────────────────

  async gitPull(workDir: string): Promise<GitActionResult> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/git/pull?workDir=${encodeURIComponent(workDir)}`, {
      method: "POST",
      headers: this.authHeaders,
    });
    return res.json();
  }

  async gitPush(workDir: string): Promise<GitActionResult> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/git/push?workDir=${encodeURIComponent(workDir)}`, {
      method: "POST",
      headers: this.authHeaders,
    });
    return res.json();
  }

  async gitCommit(workDir: string, message: string, files?: string[]): Promise<GitActionResult> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/git/commit?workDir=${encodeURIComponent(workDir)}`, {
      method: "POST",
      headers: { ...this.authHeaders, "Content-Type": "application/json" },
      body: JSON.stringify({ message, files: files ?? [] }),
    });
    return res.json();
  }

  // Single-shot commit + push (+ auto-rebase if the remote moved). The
  // server stages everything (git add -A), commits, pushes, and on a
  // non-fast-forward push tries `git fetch` + `git rebase origin/<branch>`
  // before pushing again. If the rebase introduces conflicts the server
  // aborts it and returns requiresAgent=true with the conflicted files —
  // the caller is expected to delegate to a coding agent at that point.
  async gitCommitPush(opts: { workDir: string; message?: string; allowAutoRebase?: boolean }): Promise<{
    ok: boolean;
    branch?: string;
    hash?: string;
    actions?: string[];
    pushed?: boolean;
    nothingToCommit?: boolean;
    rebased?: boolean;
    requiresAgent?: boolean;
    conflicts?: string[];
    error?: string;
    output?: string;
  }> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/git/commit-push`, {
      method: "POST",
      headers: { ...this.authHeaders, "Content-Type": "application/json" },
      body: JSON.stringify(opts),
    });
    return res.json();
  }

  async gitStash(workDir: string): Promise<GitActionResult> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/git/stash?workDir=${encodeURIComponent(workDir)}`, {
      method: "POST",
      headers: this.authHeaders,
    });
    return res.json();
  }

  async gitStashPop(workDir: string): Promise<GitActionResult> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/git/stash-pop?workDir=${encodeURIComponent(workDir)}`, {
      method: "POST",
      headers: this.authHeaders,
    });
    return res.json();
  }

  async gitRevert(workDir: string, hash: string): Promise<GitActionResult> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/git/revert?workDir=${encodeURIComponent(workDir)}`, {
      method: "POST",
      headers: { ...this.authHeaders, "Content-Type": "application/json" },
      body: JSON.stringify({ hash }),
    });
    return res.json();
  }

  async gitCheckout(workDir: string, branch: string): Promise<GitActionResult> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/git/checkout?workDir=${encodeURIComponent(workDir)}`, {
      method: "POST",
      headers: { ...this.authHeaders, "Content-Type": "application/json" },
      body: JSON.stringify({ branch }),
    });
    return res.json();
  }

  async gitStatus(workDir: string): Promise<GitStatusRow> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/git/status?workDir=${encodeURIComponent(workDir)}`, {
      headers: this.authHeaders,
    });
    return res.json();
  }

  async gitBranches(workDir: string): Promise<GitBranchRow[]> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/git/branches?workDir=${encodeURIComponent(workDir)}`, {
      headers: this.authHeaders,
    });
    return res.json();
  }

  async gitDiff(workDir: string, file?: string): Promise<{ diff: string; error?: string }> {
    this.assertConnected();
    const q = file ? `&file=${encodeURIComponent(file)}` : "";
    const res = await fetch(`${this.baseUrl}/git/diff?workDir=${encodeURIComponent(workDir)}${q}`, {
      headers: this.authHeaders,
    });
    return res.json();
  }

  async gitLog(workDir: string, limit = 10): Promise<GitCommitRow[]> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/git/log?workDir=${encodeURIComponent(workDir)}&limit=${encodeURIComponent(String(limit))}`, {
      headers: this.authHeaders,
    });
    return res.json();
  }
  // Build an agent endpoint URL, peer-proxying when `target` is a remote
  // deviceId. Mirrors machineOnboardingApply's pattern (line ~2001) and
  // relies on the agent's generic /peer/<id>/<path> handler so the
  // git/provider/* endpoints don't need their own peer awareness.
  private peerOrLocalUrl(target: string | undefined, path: string): string {
    if (!target) return `${this.baseUrl}${path}`;
    return `${this.baseUrl}/peer/${encodeURIComponent(target)}${path}`;
  }
  async gitProviderStatus(target?: string): Promise<GitProviderStatusRow[]> {
    this.assertConnected();
    const res = await fetch(this.peerOrLocalUrl(target, "/git/provider/status"), { headers: this.authHeaders });
    const data = await res.json().catch(() => ({}));
    return Array.isArray(data?.providers) ? data.providers : [];
  }
  async gitProviderDetect(target?: string): Promise<GitProviderStatusRow[]> {
    this.assertConnected();
    const res = await fetch(this.peerOrLocalUrl(target, "/git/provider/detect"), { headers: this.authHeaders });
    const data = await res.json().catch(() => ({}));
    if (!res.ok) throw new Error(data?.error || `git/provider/detect ${res.status}`);
    return Array.isArray(data?.providers) ? data.providers : [];
  }
  async gitProviderSetup(params: {
    provider: "github" | "gitlab";
    token: string;
  }, target?: string): Promise<{ ok: boolean; username?: string; host?: string; provider?: string; error?: string }> {
    this.assertConnected();
    const res = await fetch(this.peerOrLocalUrl(target, "/git/provider/setup"), {
      method: "POST",
      headers: { ...this.authHeaders, "Content-Type": "application/json" },
      body: JSON.stringify(params),
    });
    return res.json();
  }
  async gitProviderRepos(host: string, target?: string): Promise<GitRemoteRepo[]> {
    this.assertConnected();
    const res = await fetch(
      this.peerOrLocalUrl(target, `/git/provider/repos?host=${encodeURIComponent(host)}&per_page=100`),
      { headers: this.authHeaders },
    );
    const data = await res.json().catch(() => ({}));
    if (!res.ok) throw new Error(data?.error || `git/provider/repos ${res.status}`);
    return Array.isArray(data?.repos) ? data.repos : [];
  }
  async gitProviderRemove(host: string, target?: string): Promise<void> {
    this.assertConnected();
    const res = await fetch(this.peerOrLocalUrl(target, `/git/provider/${encodeURIComponent(host)}`), {
      method: "DELETE",
      headers: this.authHeaders,
    });
    if (!res.ok) throw new Error(`git/provider/${host} ${res.status}`);
  }
  // Device Flow (RFC 8628) for GitHub/GitLab. The agent runs the
  // state machine — UI just kicks it off and polls. Both routes are
  // peer-routable via /peer/<id>/.
  async gitOAuthStart(
    params: { provider: "github" | "gitlab"; host?: string },
    target?: string,
  ): Promise<{
    ok: boolean;
    error?: string;
    session_id?: string;
    provider?: string;
    host?: string;
    user_code?: string;
    verification_uri?: string;
    interval?: number;
    expires_at?: number;
    byo_client?: boolean;
  }> {
    this.assertConnected();
    const res = await fetch(this.peerOrLocalUrl(target, "/git/provider/oauth/start"), {
      method: "POST",
      headers: { ...this.authHeaders, "Content-Type": "application/json" },
      body: JSON.stringify(params),
    });
    return res.json().catch(() => ({ ok: false, error: `oauth/start ${res.status}` }));
  }
  async gitOAuthStatus(
    sessionId: string,
    target?: string,
  ): Promise<{
    ok: boolean;
    error?: string;
    state?: "pending" | "done" | "error" | "expired" | "unknown";
    session_id?: string;
    provider?: string;
    host?: string;
    user_code?: string;
    verification_uri?: string;
    interval?: number;
    expires_at?: number;
    username?: string;
    byo_client?: boolean;
  }> {
    this.assertConnected();
    const res = await fetch(
      this.peerOrLocalUrl(target, `/git/provider/oauth/status?session=${encodeURIComponent(sessionId)}`),
      { headers: this.authHeaders },
    );
    return res.json().catch(() => ({ ok: false, state: "unknown", error: `oauth/status ${res.status}` }));
  }
  async cloneRepo(url: string, target?: string): Promise<any> {
    this.assertConnected();
    const res = await fetch(this.peerOrLocalUrl(target, "/repos/clone"), {
      method: "POST",
      headers: { ...this.authHeaders, "Content-Type": "application/json" },
      body: JSON.stringify({ url, autoInit: true }),
    });
    return res.json();
  }

  // ── Password Management ───────────────────────────────────────────

  async changePassword(currentPassword: string, newPassword: string): Promise<void> {
    const res = await fetch(`${this.baseUrl}/auth/change-password`, {
      method: "POST",
      headers: { ...this.authHeaders, "Content-Type": "application/json" },
      body: JSON.stringify({ currentPassword, newPassword }),
    });
    if (!res.ok) {
      const data = await res.json().catch(() => ({}));
      throw new Error(data.error || "Failed to change password");
    }
  }

  // ── Local cache (localStorage) ─────────────────────────────────────

  private cacheTasks(tasks: Task[]): void {
    try {
      localStorage.setItem("yaver_cached_tasks", JSON.stringify(tasks));
    } catch {
      // localStorage may be unavailable.
    }
  }

  private getCachedTasks(): Task[] {
    try {
      const raw = localStorage.getItem("yaver_cached_tasks");
      return raw ? (JSON.parse(raw) as Task[]) : [];
    } catch {
      return [];
    }
  }

  // ── Phone-first mini backend ───────────────────────────────────────
  //
  // Mirrors desktop/agent/phone_backend_http.go. Each phone project is a
  // SQLite-backed Yaver project stored at ~/.yaver/phone-projects/<slug>/.
  // Promotion reuses the 19-target switch engine.

  async listPhoneProjects(): Promise<PhoneProject[]> {
    if (!this.isConnected || !this.baseUrl) return [];
    const res = await fetch(`${this.baseUrl}/phone/projects/list`, { headers: this.authHeaders });
    if (!res.ok) return [];
    const data = await res.json();
    return Array.isArray(data?.projects) ? data.projects : [];
  }

  async listPhoneTemplates(): Promise<PhoneTemplate[]> {
    if (!this.isConnected || !this.baseUrl) return [];
    const res = await fetch(`${this.baseUrl}/phone/projects/templates`, { headers: this.authHeaders });
    if (!res.ok) return [];
    const data = await res.json();
    return Array.isArray(data?.templates) ? data.templates : [];
  }

  async createPhoneProject(spec: PhoneCreateSpec): Promise<PhoneProject> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/phone/projects/create`, {
      method: "POST",
      headers: { ...this.authHeaders, "Content-Type": "application/json" },
      body: JSON.stringify(spec),
    });
    const data = await res.json().catch(() => ({}));
    if (!res.ok) throw new Error(data?.error || `HTTP ${res.status}`);
    return data as PhoneProject;
  }

  async getPhoneProject(slug: string): Promise<PhoneProject | null> {
    if (!this.isConnected || !this.baseUrl) return null;
    const res = await fetch(
      `${this.baseUrl}/phone/projects/get?slug=${encodeURIComponent(slug)}`,
      { headers: this.authHeaders },
    );
    if (!res.ok) return null;
    return (await res.json()) as PhoneProject;
  }

  async deletePhoneProject(slug: string): Promise<boolean> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/phone/projects/delete`, {
      method: "POST",
      headers: { ...this.authHeaders, "Content-Type": "application/json" },
      body: JSON.stringify({ slug }),
    });
    if (!res.ok) return false;
    const data = await res.json();
    return !!data?.ok;
  }

  async listPhoneTables(slug: string): Promise<Array<{ name: string; rowCount?: number }>> {
    if (!this.isConnected || !this.baseUrl) return [];
    const res = await fetch(
      `${this.baseUrl}/phone/projects/tables?slug=${encodeURIComponent(slug)}`,
      { headers: this.authHeaders },
    );
    if (!res.ok) return [];
    const data = await res.json();
    return Array.isArray(data?.tables) ? data.tables : [];
  }

  async browsePhoneTable(slug: string, table: string, cursor = "", limit = 50): Promise<{ rows: Array<Record<string, unknown>>; nextCursor?: string }> {
    if (!this.isConnected || !this.baseUrl) return { rows: [] };
    const params = new URLSearchParams({ slug, table, cursor, limit: String(limit) });
    const res = await fetch(
      `${this.baseUrl}/phone/projects/browse?${params.toString()}`,
      { headers: this.authHeaders },
    );
    if (!res.ok) return { rows: [] };
    return (await res.json()) as { rows: Array<Record<string, unknown>>; nextCursor?: string };
  }

  async insertPhoneRow(slug: string, table: string, doc: Record<string, unknown>): Promise<string | null> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/phone/projects/insert`, {
      method: "POST",
      headers: { ...this.authHeaders, "Content-Type": "application/json" },
      body: JSON.stringify({ slug, table, doc }),
    });
    if (!res.ok) return null;
    const data = await res.json();
    return data?.id ?? null;
  }

  async updatePhoneRow(slug: string, table: string, id: string, fields: Record<string, unknown>): Promise<boolean> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/phone/projects/update`, {
      method: "POST",
      headers: { ...this.authHeaders, "Content-Type": "application/json" },
      body: JSON.stringify({ slug, table, id, fields }),
    });
    return res.ok;
  }

  async deletePhoneRow(slug: string, table: string, id: string): Promise<boolean> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/phone/projects/delete-row`, {
      method: "POST",
      headers: { ...this.authHeaders, "Content-Type": "application/json" },
      body: JSON.stringify({ slug, table, id }),
    });
    return res.ok;
  }

  async setPhoneSchema(slug: string, schema: PhoneSchema): Promise<PhoneProject | null> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/phone/projects/schema`, {
      method: "POST",
      headers: { ...this.authHeaders, "Content-Type": "application/json" },
      body: JSON.stringify({ slug, schema }),
    });
    if (!res.ok) return null;
    return (await res.json()) as PhoneProject;
  }

  async setPhoneAuth(slug: string, auth: PhoneAuth): Promise<boolean> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/phone/projects/auth`, {
      method: "POST",
      headers: { ...this.authHeaders, "Content-Type": "application/json" },
      body: JSON.stringify({ slug, auth }),
    });
    return res.ok;
  }

  async setPhoneSeed(slug: string, seed: PhoneSeed): Promise<boolean> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/phone/projects/seed`, {
      method: "POST",
      headers: { ...this.authHeaders, "Content-Type": "application/json" },
      body: JSON.stringify({ slug, seed }),
    });
    return res.ok;
  }

  /** Returns a blob of the tgz export so callers can .click() to download. */
  async exportPhoneProjectBlob(slug: string, includeData = false, containerize = false): Promise<Blob | null> {
    if (!this.isConnected || !this.baseUrl) return null;
    const params = new URLSearchParams({ slug });
    if (includeData) params.set("includeData", "true");
    if (containerize) params.set("containerize", "true");
    const res = await fetch(
      `${this.baseUrl}/phone/projects/export?${params.toString()}`,
      { headers: this.authHeaders },
    );
    if (!res.ok) return null;
    return await res.blob();
  }

  /** Relay we're currently routed through, if any. The web dashboard is
   *  always relay-routed (browsers can't talk to localhost:18080 directly)
   *  so this is usually populated — but we still guard it. */
  get activeRelayHttpUrl(): string | null {
    return this._activeRelayUrl;
  }

  /** Pull the project .tgz from the currently-connected agent and POST it to
   *  the target's /phone/projects/receive. Mirrors mobile's pushPhoneProject
   *  so mobile + web share the wedge-demo contract. */
  async pushPhoneProject(
    slug: string,
    target: PhonePushTarget,
    opts: { onConflict?: "reject" | "rename" | "overwrite"; skipSeed?: boolean; includeData?: boolean; containerize?: boolean } = {},
  ): Promise<PhonePushResult> {
    this.assertConnected();
    const blob = await this.exportPhoneProjectBlob(
      slug,
      opts.includeData,
      target.kind === "yaver-cloud" ? true : !!opts.containerize,
    );
    if (!blob) throw new Error("export failed — agent not reachable");

    const form = new FormData();
    form.append("bundle", blob, `${slug}.tgz`);
    if (opts.onConflict) form.append("onConflict", opts.onConflict);
    if (opts.skipSeed) form.append("skipSeed", "true");

    const base = resolvePhonePushBase(target);
    const overrideToken =
      target.kind === "yaver-cloud"
        ? target.cloudAuthToken
        : target.kind === "custom"
          ? target.authToken
          : undefined;
    const res = await fetch(`${base}/phone/projects/receive`, {
      method: "POST",
      headers: overrideToken
        ? { ...this.authHeaders, Authorization: `Bearer ${overrideToken}` }
        : this.authHeaders, // let fetch set the multipart boundary
      body: form,
    });
    const text = await res.text().catch(() => "");
    if (!res.ok) throw new Error(text || `HTTP ${res.status}`);
    return JSON.parse(text) as PhonePushResult;
  }

  async promotePhoneProject(slug: string, target: string, opts: { run?: boolean; dryRun?: boolean } = {}): Promise<PhonePromoteResult> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/phone/projects/promote`, {
      method: "POST",
      headers: { ...this.authHeaders, "Content-Type": "application/json" },
      body: JSON.stringify({ slug, target, run: !!opts.run, dryRun: !!opts.dryRun }),
    });
    const data = await res.json().catch(() => ({}));
    if (!res.ok) throw new Error(data?.error || `HTTP ${res.status}`);
    return data as PhonePromoteResult;
  }

  async deployPhoneProjectRuntime(req: PhoneRuntimeDeployRequest): Promise<PhoneRuntimeDeployResult> {
    const out: PhoneRuntimeDeployResult = { pushes: [], promotes: [] };
    const exports = req.exports ?? [];
    const phonePromotions: ProjectRuntimePhonePromotion[] = [];
    for (const item of exports) {
      if (item.kind === "convex") {
        phonePromotions.push({ slug: req.slug, target: "convex-cloud", run: item.run, dryRun: item.dryRun ?? req.dryRun });
      } else if (item.kind === "cloudflare-workers") {
        phonePromotions.push({ slug: req.slug, target: "cloudflare-workers", run: item.run, dryRun: item.dryRun ?? req.dryRun });
      }
    }
    if (phonePromotions.length || req.providers?.length || req.runManifestApply) {
      out.runtime = await this.projectRuntimeApply({
        phoneSlug: req.slug,
        providers: req.providers,
        phonePromotions,
        runManifestApply: req.runManifestApply,
        dryRun: req.dryRun,
      });
    }
    for (const item of exports) {
      if (item.kind === "dev-hw") {
        const result = await this.pushPhoneProject(req.slug, {
          kind: "dev-hw",
          deviceId: item.deviceId,
          relayHttpUrl: item.relayHttpUrl,
        }, {
          includeData: req.includeData,
          onConflict: item.onConflict,
        });
        out.pushes.push({ kind: "dev-hw", result });
    } else if (item.kind === "yaver-cloud") {
      const result = await this.pushPhoneProject(req.slug, {
        kind: "yaver-cloud",
        cloudBaseUrl: item.cloudBaseUrl,
        cloudAuthToken: item.cloudAuthToken,
        }, {
          includeData: req.includeData,
          containerize: true,
          onConflict: item.onConflict,
      });
      out.pushes.push({ kind: "yaver-cloud", result });
    } else if (item.kind === "custom") {
      const result = await this.pushPhoneProject(req.slug, {
        kind: "custom",
        baseUrl: item.baseUrl,
        authToken: item.authToken,
      }, {
        includeData: req.includeData,
        containerize: true,
        onConflict: item.onConflict,
      });
      out.pushes.push({ kind: "custom", result });
    }
  }
    for (const item of exports) {
      if (item.kind === "convex") {
        out.promotes.push({ kind: "convex", result: await this.promotePhoneProject(req.slug, "convex-cloud", { run: !!item.run, dryRun: item.dryRun ?? !!req.dryRun }) });
      } else if (item.kind === "cloudflare-workers") {
        out.promotes.push({ kind: "cloudflare-workers", result: await this.promotePhoneProject(req.slug, "cloudflare-workers", { run: !!item.run, dryRun: item.dryRun ?? !!req.dryRun }) });
      }
    }
    return out;
  }

  // ── Rescue command queue ─────────────────────────────────────────
  //
  // Convex-backed control channel for wedged remote agents. The
  // dashboard's normal /agent/* path goes through the relay; if the
  // tunnel is down, queueing a rescue command via Convex still works
  // because the agent's heartbeat runs on a separate network path.
  // Pairs with backend/convex/agentRescue.ts and
  // desktop/agent/rescue.go.

  /** Queue a rescue command for one of the user's devices. The
   *  agent will pick it up on its next heartbeat (~30 s). Returns
   *  the existing pending row when one of the same kind is still
   *  alive (5-min TTL) so impatient double-clicks dedupe. */
  async queueRescueCommand(
    deviceId: string,
    command: "restart" | "reinstall-latest" | "tunnel-reset" | "auth-reset",
    params?: { version?: string },
  ): Promise<{ commandId: string; deduped: boolean }> {
    if (!this.token) throw new Error("not signed in");
    const res = await fetch(`${CONVEX_URL}/agent-rescue/queue`, {
      method: "POST",
      headers: {
        Authorization: `Bearer ${this.token}`,
        "Content-Type": "application/json",
      },
      body: JSON.stringify({
        deviceId,
        command,
        params,
        sourceSurface: "web",
      }),
    });
    const data = await res.json().catch(() => ({}));
    if (!res.ok) {
      throw new Error(data?.error || `rescue queue HTTP ${res.status}`);
    }
    return {
      commandId: typeof data?.commandId === "string" ? data.commandId : "",
      deduped: data?.deduped === true,
    };
  }

  /** Poll the rescue history for a device. UI subscribes by polling
   *  every few seconds while a command is still pending/claimed —
   *  cheap enough that we don't bother with a Convex live query. */
  async listRescueCommands(
    deviceId: string,
    limit?: number,
  ): Promise<Array<{
    _id: string;
    command: string;
    params?: { version?: string };
    status: "pending" | "claimed" | "completed" | "failed" | "expired";
    result?: string;
    createdAt: number;
    claimedAt?: number;
    completedAt?: number;
    sourceSurface?: string;
  }>> {
    if (!this.token) return [];
    const url = new URL(`${CONVEX_URL}/agent-rescue/list`);
    url.searchParams.set("deviceId", deviceId);
    if (typeof limit === "number" && limit > 0) {
      url.searchParams.set("limit", String(limit));
    }
    const res = await fetch(url.toString(), {
      headers: { Authorization: `Bearer ${this.token}` },
      cache: "no-store",
    });
    if (!res.ok) return [];
    const data = await res.json().catch(() => ({}));
    return Array.isArray(data?.commands) ? data.commands : [];
  }
}

// ── Phone-first mini backend types (mirror desktop/agent/phone_backend.go) ──

export interface PhoneColumn {
  name: string;
  type: string;
  primary?: boolean;
  required?: boolean;
  unique?: boolean;
  default?: string;
}
export interface PhoneIndex {
  columns: string[];
  unique?: boolean;
}
export interface PhoneTable {
  name: string;
  columns: PhoneColumn[];
  indexes?: PhoneIndex[];
}
export interface PhoneRelation {
  from: string;
  to: string;
  onDelete?: string;
}
export interface PhoneSchema {
  tables: PhoneTable[];
  relations?: PhoneRelation[];
}
export interface PhonePersona {
  id: string;
  email: string;
  name?: string;
  role?: string;
}
export interface PhoneAuth {
  personas: PhonePersona[];
}
export type PhoneSeed = Record<string, Array<Record<string, unknown>>>;
export interface PhoneStats {
  tableCount: number;
  rowCount: number;
  perTable: Record<string, number>;
  dbBytes: number;
}
export interface PhoneScreenAction {
  label: string;
  kind: string;
  target?: string;
  table?: string;
  description?: string;
}
export interface PhoneScreenSpec {
  id: string;
  title: string;
  kind: string;
  table?: string;
  emptyState?: string;
  actions?: PhoneScreenAction[];
}
export interface PhoneAppSpec {
  summary?: string;
  primaryEntity?: string;
  screens?: PhoneScreenSpec[];
}
export interface PhoneProject {
  slug: string;
  name: string;
  template?: string;
  dir: string;
  createdAt: string;
  updatedAt: string;
  schema?: PhoneSchema | null;
  auth?: PhoneAuth | null;
  seed?: PhoneSeed | null;
  stats?: PhoneStats | null;
}
export interface PhoneTemplate {
  id: string;
  label: string;
  description: string;
}
export interface PhoneCreateSpec {
  slug?: string;
  name: string;
  template?: string;
  schema?: PhoneSchema;
  auth?: PhoneAuth;
  seed?: PhoneSeed;
  app?: PhoneAppSpec;
  prompt?: string;
  runner?: string;
  importUrl?: string;
  importContent?: string;
  importTitle?: string;
}
export interface PhonePromoteResult {
  state?: {
    id: string;
    fromBackend: string;
    to: string;
    complexity: string;
    status: string;
    steps: Array<{ id: string; title: string; status: string; error?: string }>;
    rollbackExpiresAt?: string;
  };
  error?: string;
}

export interface ProjectRuntimeProviderInput {
  provider: string;
  label?: string;
  fields?: Record<string, string>;
}

export interface ProjectRuntimePhonePromotion {
  slug: string;
  target: string;
  run?: boolean;
  dryRun?: boolean;
}

export interface ProjectRuntimeApplyRequest {
  name?: string;
  phoneSlug?: string;
  backend?: string;
  stack?: string;
  auth?: string;
  runtime?: Record<string, unknown>;
  placement?: Record<string, unknown>;
  jobs?: unknown[];
  domains?: unknown[];
  env?: Record<string, string>;
  providers?: ProjectRuntimeProviderInput[];
  phonePromotions?: ProjectRuntimePhonePromotion[];
  runManifestApply?: boolean;
  dryRun?: boolean;
}

export interface ProjectRuntimeResolvedAssignment {
  name: string;
  role: string;
  reason?: string;
  machine?: { deviceID?: string; name?: string; provider?: string } | null;
}

export interface ProjectRuntimeProviderRequirement {
  provider: string;
  label?: string;
  authType?: string;
  fields?: string[];
  credentialRef?: string;
  requiredBy?: string[];
  connected: boolean;
  authSource?: string;
  warning?: string;
}

export interface ProjectRuntimeExportPlan {
  name: string;
  source: string;
  kind?: string;
  provider?: string;
  target?: string;
  app?: string;
  projectSlug?: string;
  credentialRef?: string;
  machineRole?: string;
  reason?: string;
  providerReady: boolean;
  providerAuthSource?: string;
  warning?: string;
}

export interface ProjectRuntimeSummary {
  projectDir: string;
  manifest?: Record<string, unknown>;
  resolvedAssignments?: ProjectRuntimeResolvedAssignment[];
  providerRequirements?: ProjectRuntimeProviderRequirement[];
  exportPlans?: ProjectRuntimeExportPlan[];
  warnings?: string[];
}

export interface ProjectRuntimeApplyResponse {
  ok?: boolean;
  actions?: Array<{ kind: string; target?: string; details?: string }>;
  manifestSaved?: boolean;
  accountsApplied?: string[];
  manifestApply?: { steps?: string[]; diff?: string[]; error?: string };
  phoneSwitches?: Array<Record<string, unknown>>;
  summary?: ProjectRuntimeSummary;
  error?: string;
}

export interface PhoneRuntimeDeployRequest {
  slug: string;
  includeData?: boolean;
  runManifestApply?: boolean;
  dryRun?: boolean;
  providers?: ProjectRuntimeProviderInput[];
  exports?: Array<
    | { kind: "convex"; run?: boolean; dryRun?: boolean }
    | { kind: "cloudflare-workers"; run?: boolean; dryRun?: boolean }
    | { kind: "dev-hw"; deviceId: string; relayHttpUrl: string; onConflict?: "reject" | "rename" | "overwrite" }
    | { kind: "yaver-cloud"; cloudBaseUrl?: string; cloudAuthToken?: string; onConflict?: "reject" | "rename" | "overwrite" }
    | { kind: "custom"; baseUrl: string; authToken?: string; onConflict?: "reject" | "rename" | "overwrite" }
  >;
}

export interface PhoneRuntimeDeployResult {
  runtime?: ProjectRuntimeApplyResponse;
  pushes: Array<{ kind: "dev-hw" | "yaver-cloud" | "custom"; result: PhonePushResult }>;
  promotes: Array<{ kind: "convex" | "cloudflare-workers"; result: PhonePromoteResult }>;
}

// ── Deploy target shapes (mirror mobile/src/lib/phoneProjects.ts) ──

export type PhonePushTarget =
  | { kind: "dev-hw"; deviceId: string; relayHttpUrl: string }
  | { kind: "yaver-cloud"; cloudBaseUrl?: string; cloudAuthToken?: string }
  | { kind: "custom"; baseUrl: string; authToken?: string };

export interface PhonePushResult {
  slug: string;
  localUrl: string;
  browseUrl: string;
  project: PhoneProject;
}

const DEFAULT_YAVER_CLOUD_BASE = getYaverCloudBaseUrl();

function resolvePhonePushBase(target: PhonePushTarget): string {
  switch (target.kind) {
    case "dev-hw":
      return `${target.relayHttpUrl.replace(/\/$/, "")}/d/${target.deviceId}`;
    case "yaver-cloud":
      return (target.cloudBaseUrl ?? DEFAULT_YAVER_CLOUD_BASE).replace(/\/$/, "");
    case "custom":
      return target.baseUrl.replace(/\/$/, "");
  }
}

/** Singleton client instance. */
export const agentClient = new AgentClient();

/**
 * AgentClientPool — one independent AgentClient per device.
 *
 * Background. The original `agentClient` singleton holds the active
 * connection state (host, port, deviceId, relay URL, output listeners,
 * polling timers, reconnect backoff). That works for "switch between
 * machines" but breaks the moment we want N machines authed and
 * streaming concurrently — switching between them tears down state we
 * still want for the previous machine.
 *
 * Pool semantics:
 * - `get(deviceId)` returns the AgentClient instance for that device,
 *   creating it lazily. Each instance has its own listeners + connection
 *   state, so subscribing to `output` on instance A never receives chunks
 *   from instance B.
 * - `disconnectAll()` is a clean teardown for sign-out / browser refresh.
 * - The legacy `agentClient` singleton stays exported and untouched, so
 *   existing pages keep working while new multi-tab UI gradually moves to
 *   `agentClientPool.get(deviceId)`.
 *
 * Auth model. Auth is per-user, not per-device — every client in the pool
 * uses the same Convex Bearer token, and the relay password is shared too.
 * The pool just multiplexes per-device transport state; it doesn't try to
 * juggle multiple identities.
 */
export class AgentClientPool {
  private clients = new Map<string, AgentClient>();

  /** Get-or-create the per-device client. */
  get(deviceId: string): AgentClient {
    let c = this.clients.get(deviceId);
    if (!c) {
      c = new AgentClient();
      this.clients.set(deviceId, c);
    }
    return c;
  }

  /** True if a client already exists for this deviceId (i.e. it's been used). */
  has(deviceId: string): boolean {
    return this.clients.has(deviceId);
  }

  /** Currently-tracked device IDs (one per pool entry). */
  keys(): string[] {
    return [...this.clients.keys()];
  }

  /** Drop one device from the pool, disconnecting it cleanly. */
  forget(deviceId: string): void {
    const c = this.clients.get(deviceId);
    if (!c) return;
    try { c.disconnect(); } catch { /* tearing down anyway */ }
    this.clients.delete(deviceId);
  }

  /** Disconnect every pool entry. Use on sign-out or before pool reset. */
  disconnectAll(): void {
    for (const c of this.clients.values()) {
      try { c.disconnect(); } catch { /* ignore */ }
    }
    this.clients.clear();
  }
}

/** Process-wide pool shared across the dashboard. */
export const agentClientPool = new AgentClientPool();
