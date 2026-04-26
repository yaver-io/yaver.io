/**
 * Browser-compatible agent client for P2P communication with the desktop agent.
 *
 * Mirrors the mobile QuicClient API but runs in the browser using fetch().
 * Uses HTTP as the transport (same fallback path as mobile).
 * Supports relay-first connection strategy with direct fallback.
 */

import { getYaverCloudBaseUrl } from "@/lib/yaver-cloud";

// ── Types ────────────────────────────────────────────────────────────

export type TaskStatus = "queued" | "running" | "completed" | "failed" | "stopped";

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

// Hybrid Mode — mirror of Go structs in desktop/agent/hybrid.go.
export interface HybridRunRequest {
  planner?: string;
  implementer?: string;
  model?: string;
  baseUrl?: string;
  workDir: string;
  prompt: string;
  maxSubtasks?: number;
  timeoutSec?: number;
}

export interface HybridSubtask {
  title: string;
  files: string[];
  prompt: string;
}

export interface HybridStepResult {
  subtask: HybridSubtask;
  status: "ok" | "error" | "skipped";
  output?: string;
  error?: string;
  durationMs: number;
}

export interface HybridPlanResult {
  spec: HybridRunRequest;
  subtasks: HybridSubtask[];
  planOutput?: string;
}

export interface HybridReport {
  spec: HybridRunRequest;
  subtasks: HybridSubtask[];
  results: HybridStepResult[];
  planOutput?: string;
  planError?: string;
  replanned?: boolean;
  retries?: number;
  startedAt: string;
  finishedAt: string;
  ok: boolean;
  failedSteps: number;
}

// HybridEvent mirrors HybridEvent in desktop/agent/hybrid.go. Consumed
// by the SSE /hybrid/stream subscriber so the UI can render live.
export interface HybridEvent {
  type:
    | "plan_started"
    | "plan_done"
    | "subtask_started"
    | "subtask_done"
    | "replan_started"
    | "replan_done"
    | "run_done"
    | "error";
  at?: string;
  message?: string;
  index?: number;
  total?: number;
  subtask?: HybridSubtask;
  result?: HybridStepResult;
  plan?: HybridSubtask[];
  report?: HybridReport;
  retry?: number;
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

export type DevServerKind = "web" | "mobile" | "hybrid";

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
  models?: OpenCodeModelSummary[];
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
    kind: "chat" | "autodev" | "autoideas" | "autotest";
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

export interface AutoDevReleaseTrain {
  enabled: boolean;
  n: number;
  greenRunSinceLastDeploy: number;
  paused: boolean;
  target?: string;
  maxTestFlightPerDay?: number;
}

export interface AutoDevProviderUsage {
  runner: string;
  usedSeconds: number;
  capSeconds: number;
  sessionWindow: string;
  windowStartedAt?: string;
  overCap: boolean;
}

export interface AutoDevLoop {
  id: string;
  name: string;
  mode: "fix" | "auto-fix" | "develop" | "ideas" | "auto-test";
  status:
    | "idle"
    | "running"
    | "paused"
    | "stopped"
    | "stuck"
    | "budget_hit"
    | "needs_human";
  iterationCount: number;
  lastSummary?: string;
  branch: string;
  tone?: string;
  radicalnessUi?: number;
  radicalnessFeatures?: number;
  promptInline?: string;
  commitsToday: number;
  patchesToday: number;
  testflightToday: number;
  lastIterationAt?: string;
  runner?: string;
  releaseTrain?: AutoDevReleaseTrain;
  sessionUsage?: AutoDevProviderUsage[];
  testRoot?: string;
}

export interface AutoDevIdeasPayload {
  generated_at?: string;
  loop_name?: string;
  persona?: string;
  ideas: Array<{
    id: string;
    title: string;
    description?: string;
    prompt: string;
    radicalness?: number;
    effort?: "small" | "medium" | "large";
    whyPersona?: string;
    whyNot?: string;
  }>;
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
  private activeRelayUrl: string | null = null;
  private tunnelCandidates: string[] = [];
  private activeTunnelUrl: string | null = null;
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

  // ── Relay server config ────────────────────────────────────────────

  /** Set relay servers fetched from platform config. Sorted by priority. */
  setRelayServers(servers: RelayServer[]): void {
    this.relayServers = servers.sort((a, b) => a.priority - b.priority);
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
    this.activeRelayUrl = null;
    this.activeTunnelUrl = null;
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
    this.activeRelayUrl = null;
    this.activeTunnelUrl = null;
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
      const data = await res.json().catch(() => ({}));
      throw new Error(data?.error || `Failed to create task: ${res.status}`);
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
        source: "web",
      }),
    });
    const data = await res.json().catch(() => ({}));
    if (!res.ok) {
      throw new Error(data?.error || `Failed to create task: ${res.status}`);
    }
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

  async deleteTask(taskId: string): Promise<void> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/tasks/${taskId}`, {
      method: "DELETE",
      headers: this.authHeaders,
    });
    if (!res.ok) throw new Error(`Failed to delete task: ${res.status}`);
  }

  // ── Hybrid Mode API ─────────────────────────────────────────────────
  // Pair an expensive planner (Claude/Codex) with a cheap local
  // implementer (aider + Ollama/Qwen) to cut API cost ~20x on feature
  // work. Endpoints defined in desktop/agent/hybrid_http.go.

  async hybridPlan(req: HybridRunRequest): Promise<HybridPlanResult> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/hybrid/plan`, {
      method: "POST",
      headers: { ...this.authHeaders, "Content-Type": "application/json" },
      body: JSON.stringify(req),
    });
    if (!res.ok) {
      const body = await res.text().catch(() => "");
      throw new Error(`hybrid/plan ${res.status}: ${body}`);
    }
    return res.json();
  }

  async hybridRun(req: HybridRunRequest): Promise<HybridReport> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/hybrid/run`, {
      method: "POST",
      headers: { ...this.authHeaders, "Content-Type": "application/json" },
      body: JSON.stringify(req),
    });
    // hybrid/run always returns a JSON body; keep it even on 4xx/5xx
    // so the UI can render the partial report.
    const data = await res.json().catch(() => ({}));
    if (!res.ok && !data?.subtasks) {
      throw new Error(data?.error || `hybrid/run ${res.status}`);
    }
    return data;
  }

  /**
   * Stream a hybrid run over SSE. Resolves when the server emits
   * `run_done` (or `error`); rejects on network failure. Use this
   * instead of hybridRun when the UI needs live progress — the
   * callback fires once per event (plan_started, subtask_started,
   * subtask_done, etc).
   *
   * Implementation detail: EventSource does not support POST or
   * custom headers, so we do the SSE parse by hand on a fetch stream.
   * This is the same pattern Claude Code uses for its own
   * `--output-format stream-json` parser.
   */
  async hybridStream(
    req: HybridRunRequest,
    onEvent: (ev: HybridEvent) => void,
  ): Promise<HybridReport | undefined> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/hybrid/stream`, {
      method: "POST",
      headers: {
        ...this.authHeaders,
        "Content-Type": "application/json",
        Accept: "text/event-stream",
      },
      body: JSON.stringify(req),
    });
    if (!res.ok || !res.body) {
      const body = await res.text().catch(() => "");
      throw new Error(`hybrid/stream ${res.status}: ${body}`);
    }
    const reader = res.body.getReader();
    const decoder = new TextDecoder();
    let buf = "";
    let final: HybridReport | undefined;
    // Parser: SSE frames are separated by a blank line; each frame
    // has one or more `data:`-prefixed lines (we only emit one) and
    // optional `:`-prefixed heartbeat comment lines we ignore.
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
          const ev: HybridEvent = JSON.parse(dataLines.join("\n"));
          onEvent(ev);
          if (ev.type === "run_done") final = ev.report;
        } catch {
          // malformed frame — drop silently; heartbeat/noise.
        }
      }
    }
    return final;
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
   */
  async startRunnerBrowserAuth(runner: string): Promise<RunnerBrowserAuthSession> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/runner-auth/browser/start`, {
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

  async getRunnerBrowserAuthStatus(sessionId: string): Promise<RunnerBrowserAuthSession> {
    this.assertConnected();
    const url = new URL(`${this.baseUrl}/runner-auth/browser/status`);
    url.searchParams.set("id", sessionId);
    const res = await fetch(url.toString(), { headers: this.authHeaders });
    if (!res.ok) {
      const body = await res.text().catch(() => "");
      throw new Error(`getRunnerBrowserAuthStatus ${res.status}: ${body || res.statusText}`);
    }
    const data = await res.json();
    return data.session as RunnerBrowserAuthSession;
  }

  async cancelRunnerBrowserAuth(sessionId: string): Promise<void> {
    this.assertConnected();
    const url = new URL(`${this.baseUrl}/runner-auth/browser/cancel`);
    url.searchParams.set("id", sessionId);
    await fetch(url.toString(), { method: "POST", headers: this.authHeaders }).catch(() => {});
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
  async submitRunnerBrowserAuthCode(sessionId: string, code: string): Promise<RunnerBrowserAuthSession> {
    this.assertConnected();
    const url = new URL(`${this.baseUrl}/runner-auth/browser/submit-code`);
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

  streamTaskOutput(taskId: string, onLine: (line: string) => void): () => void {
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
              if (event?.type === "output" && event.text) onLine(String(event.text));
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
   * Subscribe to a daemon-hosted log stream (e.g. "autodev:sfmg-autodev").
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
  streamLog(streamName: string, onEvent: (ev: any) => void): () => void {
    const controller = new AbortController();
    const url = `${this.baseUrl}/streams/${encodeURIComponent(streamName)}`;
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
      }
    })();
    return () => controller.abort();
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

  /** POST /autoideas/select — picks → autodev kick */
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

  async autodevStart(params: {
    project?: string;
    workDir: string;
    hours?: string;
    load?: string;
    prompt?: string;
    deploy?: string;
    runner?: string;
    branch?: string;
    target?: string;
    remainedPath?: string;
    remainedContent?: string;
    noAutotest?: boolean;
    maxIterations?: number;
    // Morning match-report toggles. Undefined = agent default (both
    // on). Pass false to opt out; pass true to be explicit. Video is
    // advisory — the agent skips capture gracefully when no iOS sim /
    // Android emu is available.
    createSummary?: boolean;
    createVideo?: boolean;
  }): Promise<{ ok: boolean; loopName?: string; workDir?: string; hours?: string; deploy?: string; error?: string }> {
    try {
      const res = await fetch(`${this.baseUrl}/autodev/start`, {
        method: "POST",
        headers: { ...this.authHeaders, "Content-Type": "application/json" },
        body: JSON.stringify({
          project: params.project ?? "",
          work_dir: params.workDir,
          hours: params.hours ?? "",
          load: params.load ?? "",
          prompt: params.prompt ?? "",
          deploy: params.deploy ?? "",
          runner: params.runner ?? "",
          branch: params.branch ?? "",
          target: params.target ?? "",
          remained_path: params.remainedPath ?? "",
          remained_content: params.remainedContent ?? "",
          no_autotest: params.noAutotest ?? false,
          max_iterations: params.maxIterations ?? 0,
          ...(params.createSummary !== undefined && { create_summary: params.createSummary }),
          ...(params.createVideo !== undefined && { create_video: params.createVideo }),
        }),
      });
      if (!res.ok && res.status !== 202) {
        const text = await res.text().catch(() => "");
        return { ok: false, error: text || `HTTP ${res.status}` };
      }
      const data = await res.json();
      return {
        ok: true,
        loopName: data.loop_name,
        workDir: data.work_dir,
        hours: data.hours,
        deploy: data.deploy,
      };
    } catch (e: any) {
      return { ok: false, error: e?.message ?? "network error" };
    }
  }

  async autodevLoops(): Promise<AutoDevLoop[]> {
    try {
      const res = await fetch(`${this.baseUrl}/autodev/loops`, {
        headers: this.authHeaders,
      });
      if (!res.ok) return [];
      const data = await res.json();
      return data.loops || [];
    } catch {
      return [];
    }
  }

  async autodevStop(name: string): Promise<boolean> {
    try {
      const res = await fetch(
        `${this.baseUrl}/autodev/loops/${encodeURIComponent(name)}/stop`,
        { method: "POST", headers: this.authHeaders },
      );
      return res.ok;
    } catch {
      return false;
    }
  }

  async autodevIdeas(name: string): Promise<AutoDevIdeasPayload | null> {
    try {
      const res = await fetch(
        `${this.baseUrl}/autodev/loops/${encodeURIComponent(name)}/ideas`,
        { headers: this.authHeaders },
      );
      if (!res.ok) return null;
      return await res.json();
    } catch {
      return null;
    }
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
    if (this.activeRelayUrl && this.deviceId) {
      return `${this.activeRelayUrl}/d/${this.deviceId}`;
    }
    if (this.activeTunnelUrl) {
      return this.activeTunnelUrl.replace(/\/+$/, "");
    }
    return `http://${this.host}:${this.port}`;
  }

  private activeRelayPassword: string | null = null;

  private get authHeaders(): Record<string, string> {
    const h: Record<string, string> = { Authorization: `Bearer ${this.token}` };
    if (this.activeRelayUrl && this.activeRelayPassword) {
      h["X-Relay-Password"] = this.activeRelayPassword;
    }
    return h;
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
    // Direct LAN path last (usually blocked by HTTPS → HTTP on a web origin,
    // but works for same-machine/Electron or if the user opens yaver://… over HTTP).
    if (this.host && this.port) {
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

  private async probeHealth(
    url: string,
    headers: Record<string, string>,
    timeoutMs: number,
    path: "relay" | "tunnel" | "direct",
    relayId?: string,
  ): Promise<ConnectAttemptDiagnostic> {
    const started = Date.now();
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
        diag.error = `HTTP ${res.status}`;
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

    for (const tunnelUrl of (opts.tunnelUrls || []).map((u) => String(u || "").trim()).filter(Boolean)) {
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
    this.activeRelayUrl = null;
    this.activeTunnelUrl = null;
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
            this.activeRelayUrl = relay.httpUrl;
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
            this.activeRelayUrl = null;
            this.activeRelayPassword = null;
            this.activeTunnelUrl = tunnelUrl.replace(/\/+$/, "");
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
          this.activeRelayUrl = null;
          this.activeRelayPassword = null;
          this.activeTunnelUrl = null;
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

  async listProjects(): Promise<{ name: string; path: string; branch?: string; framework?: string; tags?: string[] }[]> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/projects`, { headers: this.authHeaders });
    const data = await res.json().catch(() => ({}));
    if (!res.ok) throw new Error(data?.error || `Failed to load projects: HTTP ${res.status}`);
    return data.projects ?? [];
  }

  async getProjectActions(query: string): Promise<{ project: string; path: string; actions: { label: string; target: string; type: string; framework?: string; platform?: string; command?: string; icon?: string; supported?: boolean; reason?: string }[] }> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/projects/actions?query=${encodeURIComponent(query)}`, { headers: this.authHeaders });
    if (!res.ok) throw new Error("Failed to get project actions");
    return res.json();
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
  }): Promise<void> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/dev/start`, {
      method: "POST",
      headers: { ...this.authHeaders, "Content-Type": "application/json" },
      body: JSON.stringify(opts),
    });
    if (!res.ok) {
      const body = await res.json().catch(() => ({}));
      throw new Error(body?.error || `Failed to start dev server (HTTP ${res.status})`);
    }
  }

  // ── Workspace manifest (monorepo) ────────────────────────────────

  async getWorkspace(): Promise<WorkspaceResponse | null> {
    this.assertConnected();
    try {
      const res = await fetch(`${this.baseUrl}/workspace`, { headers: this.authHeaders });
      if (!res.ok) return null;
      return res.json();
    } catch { return null; }
  }

  async getWorkspaceApps(kind?: string | string[]): Promise<WorkspaceAppView[]> {
    this.assertConnected();
    const query = kind
      ? `?kind=${encodeURIComponent(Array.isArray(kind) ? kind.join(",") : kind)}`
      : "";
    const res = await fetch(`${this.baseUrl}/workspace/apps${query}`, { headers: this.authHeaders });
    const data = await res.json().catch(() => ({}));
    if (!res.ok) throw new Error(data?.error || `Failed to load workspace apps: HTTP ${res.status}`);
    return Array.isArray(data?.apps) ? data.apps : [];
  }

  async stopDevServer(): Promise<{
    ok?: boolean;
    stoppedServing?: boolean;
    previouslyServing?: boolean;
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

  async reloadDevServer(): Promise<{
    ok?: boolean;
    nativeChangesDetected?: boolean;
    nativeChanges?: Array<{ path?: string; reason?: string }>;
    changeClass?: string;
    error?: string;
  }> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/dev/reload`, { method: "POST", headers: this.authHeaders });
    const data = await res.json().catch(() => ({}));
    if (!res.ok) {
      throw new Error(data?.error || "Failed to reload dev server");
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
    // Same relay-proxy rewrite pattern as devPreviewUrl.
    const relayMatch = this.baseUrl.match(/\/d\/([^/]+)/);
    if (!relayMatch) return `${this.baseUrl}/dev-web/`;
    return `/api/relay/d/${relayMatch[1]}/dev-web/`;
  }

  get devPreviewUrl(): string | null {
    if (!this.baseUrl) return null;
    // In the browser, route relay-backed previews through our own
    // same-origin proxy so the iframe does not depend on relay query-param
    // auth. That proxy injects X-Relay-Password server-side.
    if (this.activeRelayUrl) {
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
    const u = new URL(url);
    if (this.token) u.searchParams.set("token", this.token);
    if (this.activeRelayUrl && this.activeRelayPassword) {
      u.searchParams.set("__rp", this.activeRelayPassword);
    }
    return u.toString();
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
  // browser never has to put the real bearer token into a URL.
  async metricsWsUrl(): Promise<string> {
    const token = await this.issueBrowserSession("/ws/metrics");
    return `${this.baseUrl.replace(/^http/, "ws")}/ws/metrics?browser_session=${encodeURIComponent(token)}`;
  }
  async containerLogsWsUrl(id: string): Promise<string> {
    const token = await this.issueBrowserSession("/ws/logs");
    return `${this.baseUrl.replace(/^http/, "ws")}/ws/logs?id=${encodeURIComponent(id)}&browser_session=${encodeURIComponent(token)}`;
  }
  async terminalWsUrl(cwd?: string): Promise<string> {
    const token = await this.issueBrowserSession("/ws/terminal");
    const c = cwd ? `&cwd=${encodeURIComponent(cwd)}` : "";
    return `${this.baseUrl.replace(/^http/, "ws")}/ws/terminal?browser_session=${encodeURIComponent(token)}${c}`;
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
  async gitProviderStatus(): Promise<GitProviderStatusRow[]> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/git/provider/status`, { headers: this.authHeaders });
    const data = await res.json().catch(() => ({}));
    return Array.isArray(data?.providers) ? data.providers : [];
  }
  async gitProviderDetect(): Promise<GitProviderStatusRow[]> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/git/provider/detect`, { headers: this.authHeaders });
    const data = await res.json().catch(() => ({}));
    if (!res.ok) throw new Error(data?.error || `git/provider/detect ${res.status}`);
    return Array.isArray(data?.providers) ? data.providers : [];
  }
  async gitProviderSetup(params: {
    provider: "github" | "gitlab";
    token: string;
  }): Promise<{ ok: boolean; username?: string; host?: string; provider?: string; error?: string }> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/git/provider/setup`, {
      method: "POST",
      headers: { ...this.authHeaders, "Content-Type": "application/json" },
      body: JSON.stringify(params),
    });
    return res.json();
  }
  async gitProviderRepos(host: string): Promise<GitRemoteRepo[]> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/git/provider/repos?host=${encodeURIComponent(host)}&per_page=100`, {
      headers: this.authHeaders,
    });
    const data = await res.json().catch(() => ({}));
    if (!res.ok) throw new Error(data?.error || `git/provider/repos ${res.status}`);
    return Array.isArray(data?.repos) ? data.repos : [];
  }
  async gitProviderRemove(host: string): Promise<void> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/git/provider/${encodeURIComponent(host)}`, {
      method: "DELETE",
      headers: this.authHeaders,
    });
    if (!res.ok) throw new Error(`git/provider/${host} ${res.status}`);
  }
  async cloneRepo(url: string): Promise<any> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/repos/clone`, {
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

  // ── Morning match-report ───────────────────────────────────────────
  //
  // These methods go through the same relay-aware base URL as everything
  // else, so a yaver-to-yaver viewer on a paired Mac hits the same
  // endpoints the mobile app does. The match-report UI renders only
  // what the agent serves; there is no client-side enrichment.

  async listMorningRuns(limit = 20): Promise<MorningRunSummary[]> {
    if (!this.isConnected || !this.baseUrl) return [];
    try {
      const res = await fetch(`${this.baseUrl}/morning/runs?limit=${limit}`, {
        headers: this.authHeaders,
      });
      if (!res.ok) return [];
      const data = await res.json();
      return Array.isArray(data?.runs) ? data.runs : [];
    } catch {
      return [];
    }
  }

  async getMorningRun(runId: string): Promise<MorningRunSummary | null> {
    if (!this.isConnected || !this.baseUrl) return null;
    try {
      const res = await fetch(`${this.baseUrl}/morning/runs/${encodeURIComponent(runId)}`, {
        headers: this.authHeaders,
      });
      if (!res.ok) return null;
      const data = await res.json();
      return (data?.run as MorningRunSummary) ?? null;
    } catch {
      return null;
    }
  }

  async rollbackMorningTask(runId: string, taskId: string): Promise<{ ok: boolean; revertSha?: string; error?: string }> {
    if (!this.isConnected || !this.baseUrl) {
      return { ok: false, error: "not connected" };
    }
    try {
      const res = await fetch(
        `${this.baseUrl}/morning/runs/${encodeURIComponent(runId)}/tasks/${encodeURIComponent(taskId)}/rollback`,
        { method: "POST", headers: this.authHeaders }
      );
      const data = await res.json().catch(() => ({}));
      if (!res.ok) return { ok: false, error: data?.error ?? `HTTP ${res.status}` };
      return { ok: true, revertSha: data?.revertSha };
    } catch (e: unknown) {
      return { ok: false, error: e instanceof Error ? e.message : "rollback failed" };
    }
  }

  /**
   * Absolute URL the `<video>` element can point its `src` at. The
   * element issues byte-range requests directly; the browser is good
   * at this and doesn't need us to stream manually.
   *
   * Returns null when not connected — the caller should hide the
   * video layer and render the card's diff panel instead.
   */
  morningVideoUrl(runId: string, taskId: string): string | null {
    if (!this.baseUrl) return null;
    return `${this.baseUrl}/recordings/${encodeURIComponent(runId)}/${encodeURIComponent(taskId)}/video.mp4`;
  }

  async recordingDrivers(): Promise<RecordingDriverStatus[]> {
    if (!this.isConnected || !this.baseUrl) return [];
    try {
      const res = await fetch(`${this.baseUrl}/morning/drivers`, { headers: this.authHeaders });
      if (!res.ok) return [];
      const data = await res.json();
      const raw = data?.drivers ?? {};
      return Object.values(raw) as RecordingDriverStatus[];
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
    return this.activeRelayUrl;
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

// ── Morning match-report types ─────────────────────────────────────────
// Mirror the Go structs in morning.go. Clients render whatever fields
// exist; the agent is authoritative for which are populated.

export type MorningTaskStatus = "shipped" | "failed" | "skipped" | "rolled-back";

export interface MorningTaskHighlight {
  taskId: string;
  runnerId?: string;
  title: string;
  oneLineSummary?: string;
  status: MorningTaskStatus;
  startedAt: string;
  finishedAt: string;
  costUsd?: number;
  baseSha?: string;
  headSha?: string;
  commitShas?: string[];
  workDir?: string;
  filesChanged?: number;
  linesAdded?: number;
  linesRemoved?: number;
  hasVideo: boolean;
  videoDurationMs?: number;
  videoSizeBytes?: number;
  rolledBackAt?: string;
  revertSha?: string;
  failureNote?: string;
}

export interface MorningRunStats {
  tasksShipped: number;
  tasksFailed: number;
  tasksRolledBack: number;
  tasksTotal: number;
  totalCostUsd: number;
  totalMinutes: number;
}

export interface MorningRunSummary {
  runId: string;
  project: string;
  workDir: string;
  startedAt: string;
  finishedAt?: string;
  tasks: MorningTaskHighlight[];
  stats: MorningRunStats;
  note?: string;
}

export interface RecordingDriverStatus {
  driver: string;
  target: string;
  available: boolean;
  reason?: string;
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
