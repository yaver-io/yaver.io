/**
 * Browser-compatible agent client for P2P communication with the desktop agent.
 *
 * Mirrors the mobile QuicClient API but runs in the browser using fetch().
 * Uses HTTP as the transport (same fallback path as mobile).
 * Supports relay-first connection strategy with direct fallback.
 */

import { getYaverCloudBaseUrl } from "@/lib/yaver-cloud";
import { CONVEX_URL } from "@/lib/constants";
import { decodeCloudWorkspaceRequiredError } from "@/lib/cloud-workspace-required";
import { classifyRelayLimit, explainRelayDeny } from "./relayDeny";
import { resolveUsableModel } from "./runnerModelCompat";
import { ParkedTurnError, type ParkedTurnRejection } from "./parkedTurn";
import { isUsablePublicEndpoint } from "./endpoints";
import { planReconnect } from "./reconnectLadder";
import webPkg from "../package.json";
import type { TaskFailureWire } from "./runnerFailure";
import type { TaskPresentationMessage } from "./_core/taskPresentation";
import { agentHttpBase } from "./_core/endpoints";

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

export type TaskStatus = "queued" | "running" | "ready" | "review" | "completed" | "failed" | "stopped";

export interface ConversationTurn {
  role: "user" | "assistant";
  content: string;
  timestamp: string;
  hidden?: boolean;
}

export type ProjectStartGitProvider = "yaver-git" | "github" | "gitlab";

export interface ProjectStartResult {
  ok: boolean;
  directory: string;
  gitProvider: ProjectStartGitProvider;
  palette: string;
  task: Task;
}

export interface PendingFollowUp {
  input: string;
  images?: unknown[];
  options?: Record<string, unknown>;
}

/** Wire shape for POST /screen-context. Mirrors the Go `ScreenContext`
 *  (desktop/agent/screen_context.go), which re-clamps every field on receipt. */
export interface ScreenContextReport {
  workDir: string;
  route?: string;
  title?: string;
  heading?: string;
  controls?: string[];
  component?: string;
  lane?: string;
}

/** Wire shape for POST /dom-inspect. Mirrors the Go `DomElement`
 *  (desktop/agent/dom_inspect.go); the agent re-clamps every field on receipt. */
export interface DomElementReport {
  workDir: string;
  selector?: string;
  tag?: string;
  id?: string;
  classes?: string;
  text?: string;
  html?: string;
  css?: string;
  rect?: string;
  shot?: string;
  lane?: string;
}

/** Wire shape for POST /dom-inspect/items. Mirrors the Go `DomItems`. */
export interface DomItemsReport {
  workDir: string;
  items?: { selector?: string; tag?: string; id?: string; classes?: string; text?: string; rect?: string }[];
}

export interface Task {
  id: string;
  title: string;
  description: string;
  status: TaskStatus;
  source?: string;
  deviceId?: string;
	runnerId?: string;
	transport?: "acp" | "cli-pty" | string;
	transportReason?: string;
  model?: string;
  reasoningEffort?: string;
  output: string[];
  /** Tail of the runner's RAW stdout (ANSI + TUI bytes, ungroomed) from
   *  `GET /tasks/{id}` — seeds the opencode terminal view. `rawOffset` is
   *  the byte length of the FULL retained tail, the cursor for
   *  `streamTaskOutput({ rawSince })` resume. */
  rawOutput?: string;
  rawOffset?: number;
  resultText?: string;
  /** Human-facing semantic runner narrative; raw runner evidence is separate. */
  presentation?: TaskPresentationMessage[];
  costUsd?: number;
  turns?: ConversationTurn[];
  pendingFollowUps?: PendingFollowUp[];
  createdAt: number;
  updatedAt: number;
  finishedAt?: number;
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
  /** Task-proof package (docs/audits/task-proof-showcase-audit-2026-07.md §9):
   *  when proof capture ran for this task the agent stamps proofStatus onto
   *  the task JSON and `GET /tasks/{id}/proof` (getTaskProof below) returns
   *  the full TaskProof. commitSha/commitSubject/commitBranch/diffShortstat
   *  are the evidence footer collected at completion; feedbackId links a
   *  feedback-fix task back to the report that spawned it. */
  proofStatus?: "capturing" | "ready" | "failed";
  proofUrl?: string;
  commitSha?: string;
  commitSubject?: string;
  commitBranch?: string;
  diffShortstat?: string;
  feedbackId?: string;
  placementId?: string;
  placementLane?: string;
  placementReason?: string;
  placementCreditLabel?: string;
  pendingCloudBlockedAction?: string;
  pendingCloudBlockedReason?: string;
  pendingCloudExpiresAt?: number;
  pendingCloudTargetDeviceId?: string | null;
  /** tmux session driving this task (`tmux attach -t <tmuxSession>`). Surfaced
   *  in the task UI on every surface (mobile/web/tvOS/car/AR-VR) so the running
   *  session is always identifiable. tmuxSessionId is tmux's internal id ("$1");
   *  tmuxSession is the human name ("yaver-<task>"). */
  tmuxSessionId?: string;
  tmuxSession?: string;
  sessionId?: string;
  hostKind?: "terminal_tmux" | "desktop_gui" | "runner_process";
  executionSession?: TaskExecutionIdentity;
  sessionSettings?: ClientSessionSettings;
  /** Structured task capability gap from POST /tasks. Missing runner/toolchain
   *  preflight failures carry the same routed object as preview gaps, so the
   *  Vibing surface can render Install + streamed retry instead of prose. */
  capabilityGap?: unknown;
  failure?: TaskFailureWire | null;
}

export interface TaskExecutionIdentity {
  yaverSessionId: string;
  taskId: string;
  remoteBoxId?: string;
  runnerName?: string;
  runnerId?: string;
  runnerSessionId?: string;
  hostKind?: "terminal_tmux" | "desktop_gui" | "runner_process";
  startedFrom?: string;
  startedFromSurface?: string;
  initialSurface?: string;
  sessionStartedAt: string;
  lastSurface?: string;
  lastActiveAt: string;
  firstUserMessageAt?: string;
  firstAgentResponseAt?: string;
  lastUserMessageAt?: string;
  lastAgentResponseAt?: string;
  sessionSettings?: ClientSessionSettings;
  deletedAt?: string;
  resumable: boolean;
  tmuxSession?: string;
  tmuxSessionId?: string;
  tmuxWindowIndex?: string;
  tmuxWindowName?: string;
  tmuxPaneIndex?: string;
  tmuxPaneId?: string;
}

export interface ClientSessionSettings {
  appName: string;
  appVersion: string;
  buildNumber: string;
  surface: string;
  clientSurface: string;
  platform: string;
  deviceClass: "phone" | "tablet" | "desktop" | "tv" | "car" | "watch" | "xr" | "browser";
  lane: "yaver-native" | "browser" | "hermes" | "webrtc";
  runtimeMode: "native" | "dogfood" | "yaver-hosted-dogfood";
  dogfood: boolean;
  usageMode: "chat-only" | "reload-only" | "reload-and-chat";
  chatEnabled: boolean;
  renderEnabled: boolean;
  revision?: number;
  updatedAt?: string;
}

export function browserSessionSettings(
  usageMode: ClientSessionSettings["usageMode"] = "chat-only",
): ClientSessionSettings {
  const ua = typeof navigator === "undefined" ? "" : navigator.userAgent;
  const platform = /Android/i.test(ua) ? "android"
    : /iPhone|iPad|iPod/i.test(ua) ? "ios"
      : /Macintosh|Mac OS X/i.test(ua) ? "macos"
        : /Windows/i.test(ua) ? "windows"
          : /Linux/i.test(ua) ? "linux" : "web";
  const deviceClass = /iPad/i.test(ua) ? "tablet"
    : platform === "ios" || platform === "android" ? "phone"
      : platform === "web" ? "browser" : "desktop";
  return {
    appName: "Yaver web",
    appVersion: process.env.NEXT_PUBLIC_APP_VERSION ?? "",
    buildNumber: process.env.NEXT_PUBLIC_APP_BUILD ?? "",
    surface: "yaver-web-dashboard",
    clientSurface: "yaver-web-dashboard",
    platform,
    deviceClass,
    lane: "browser",
    runtimeMode: "native",
    dogfood: false,
    usageMode,
    chatEnabled: usageMode !== "reload-only",
    renderEnabled: usageMode !== "chat-only",
  };
}

export interface RemoteProject {
  name: string;
  path: string;
  branch?: string;
  framework?: string;
  frameworks?: string[];
  stack?: string;
  stacks?: string[];
  surfaces?: string[];
  testSurfaces?: string[];
  backend?: string;
  services?: string[];
  hosting?: string[];
  role?: string;
  executionMode?: string;
  primarySurface?: string;
  gitRemote?: string;
  tags?: string[];
}

/** Wire shape of `GET /tasks/{id}/proof` → `{ok:true, proof:{…}}`. Media URLs
 *  are absolute agent routes behind the same bearer/relay auth as every other
 *  agent call — never fetch them bare (audit B8); use clipId with
 *  vibeClipRequest/vibeClipPosterRequest or the /d/<deviceId>/ proxy. */
export interface TaskProof {
  taskId: string;
  status: "capturing" | "ready" | "failed";
  failedReason?: string;
  failedRoute?: string;
  lane?: string;
  clipId?: string;
  videoUrl?: string;
  posterUrl?: string;
  commitSha?: string;
  commitSubject?: string;
  commitBranch?: string;
  diffShortstat?: string;
  summaryMarkdown?: string;
  durationSec?: number;
  costUsd?: number;
  createdAt?: string;
}

export interface FeedbackWorkAgentConfig {
  ok?: boolean;
  enabled: boolean;
  running: boolean;
  intervalSeconds?: number;
  workerId?: string;
  projectSlug?: string;
  createProviderIssues: boolean;
  runtimeReason?: string;
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
  /** The runner's own CLI says a credential is here. LOCAL evidence — it
   *  cannot see a server-side revocation. Agent 1.99.384+. */
  authPresent?: boolean;
  authVerified?: boolean;
  /** Epoch ms of the last time the PROVIDER spoke about this credential —
   *  a completed turn, a completed OAuth, or a rejection. Freshness of the
   *  VERDICT, which is not the same as `checkedAt` (freshness of the ROW). */
  authVerifiedAt?: number;
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

export type TenantComputeProvider =
  | "hetzner"
  | "aws"
  | "gcp"
  | "azure"
  | "onprem"
  | "byo-yaver-device";

export interface CompanyAIOptions {
  enabled: boolean;
  runtime: {
    mode: "dedicated-compute" | "bring-your-own-yaver" | "local-only";
    defaultProvider: TenantComputeProvider;
    defaultDeviceId?: string;
    fallbackDeviceIds?: string[];
    region?: string;
  };
  convex: {
    deploymentKind: "dedicated" | "shared-isolated" | "external";
    deploymentName?: string;
    siteUrl?: string;
    envName: string;
  };
  runners: {
    defaultRunner: string;
    allowedRunners: string[];
    defaultModelByRunner?: Array<{ runner: string; model: string }>;
    allowUserOverride: boolean;
    requireRunnerAuthPerUser: boolean;
    credentialMode:
      | "user-auth-on-runtime"
      | "company-api-key-on-runtime"
      | "local-model-on-runtime"
      | "external-onprem-endpoint";
  };
  opencode?: {
    providers: Array<{
      id: string;
      label: string;
      baseUrl?: string;
      models: string[];
      keyPolicy: "company-secret" | "user-secret" | "none";
      keyConfigured?: boolean;
    }>;
    defaultAgent?: string;
  };
  mcp: {
    enabledServers: string[];
    requiredServers: string[];
    toolPolicyByRole?: Array<{ role: string; allowedTools: string[] }>;
  };
  workKinds: {
    appCode: boolean;
    erpFlow: boolean;
    convex: boolean;
    webUi: boolean;
    harnessCad: boolean;
    openScadCad: boolean;
    robotTrial: boolean;
    inspection: boolean;
  };
  approvals: {
    requireApprovalForProductionWrites: boolean;
    requireApprovalForDeploy: boolean;
    requireApprovalForRobotMotion: boolean;
    requireApprovalForSecretsAccess: boolean;
  };
  dataPolicy: {
    allowCustomerDataInPrompts: boolean;
    allowScreenshotsInPrompts: boolean;
    allowTelemetryInPrompts: boolean;
    redactPII: boolean;
    retentionDays: number;
  };
  createdAt?: number;
  updatedAt?: number;
}

export interface CompanyAIOptionsResponse {
  ok: boolean;
  teamId: string;
  role: string;
  options: CompanyAIOptions;
  canEdit: boolean;
}

export interface TeamSummary {
  teamId: string;
  name: string;
  role?: string;
  plan?: string;
  maxMembers?: number;
}

// ── Companion compute (yaver.companion.yaml) ─────────────────────────
export interface CompanionDetectItem {
  kind: string; // "cron" | "service" | "note"
  name: string;
  reason: string;
  status: string; // "detected" | "proposed-missing-endpoint" | "note"
  endpoint?: string;
  schedule?: string;
  confidence: number;
}
export interface CompanionDetectResult {
  items: CompanionDetectItem[];
  manifestYaml: string;
}
export interface CompanionCronStatus {
  name: string;
  schedule: string;
  scheduleId?: string;
  status: string;
  lastOutcome?: string;
  nextRunAt?: string;
  lastRunAt?: string;
  proposed?: boolean;
}
export interface CompanionSvcStatus {
  name: string;
  durable: boolean;
  unit?: string;
  running: boolean;
}
export interface CompanionStatus {
  project: string;
  enabled: boolean;
  crons: CompanionCronStatus[];
  services: CompanionSvcStatus[];
  warnings?: string[];
}
export interface CompanionProjectSummary {
  project: string;
  repoDir: string;
  enabled: boolean;
  cronCount: number;
  svcCount: number;
  updatedAt: string;
}
export interface MicroserviceWrapRequest {
  repo: string;
  project?: string;
  name?: string;
  command?: string;
  workdir?: string;
  port?: number;
  env_vault?: string;
  env_file?: string;
  durable?: boolean;
  write?: boolean;
  arm?: boolean;
  overwrite?: boolean;
  use_shell?: boolean;
  ai_wrap?: boolean;
  ai_work_kind?: string;
  base_url_from?: string;
  health_url?: string;
  schedule_cron?: string;
}
export interface MicroserviceWrapResult extends CompanionDetectResult {
  ok: boolean;
  repo: string;
  project: string;
  manifestPath: string;
  existing: boolean;
  written: boolean;
  armed: boolean;
  status?: CompanionStatus;
  warnings?: string[];
  next?: string[];
}

// One step in the store-onboarding concierge (mirrors Go setup_guide.go,
// served by the agent GET /stores). automation: auto | assisted | manual;
// status: done | todo | action | blocked | unknown.
export interface StoreTask {
  id: string;
  platform: "apple" | "google" | "both";
  title: string;
  summary: string;
  automation: "auto" | "assisted" | "manual";
  routeUrl?: string;
  steps?: string[];
  needsSecret?: string[];
  dependsOn?: string[];
  yaverCmd?: string;
  status: "done" | "todo" | "action" | "blocked" | "unknown";
}

// Capability/permissions plan (mirrors Go capabilities.go, GET /capabilities).
export interface CapabilityFinding {
  id: string;
  title: string;
  detected: boolean;
  matchedSignals?: string[];
  iosPlistUsage?: Record<string, string>;
  iosEntitlements?: string[];
  androidPermissions?: string[];
  consoleForms?: string[];
  notes?: string;
}
export interface ManifestPlan {
  capabilities: CapabilityFinding[];
  iosPlistUsage: Record<string, string>;
  iosEntitlements: string[];
  androidPermissions: string[];
  consoleForms: string[];
  needsUsageStrings: string[];
}

// Canonical store listing (mirrors Go store_listing.go, GET /listing).
export interface DataCollection {
  category: string;
  appleType: string;
  googleType: string;
  purposes: string[];
  linkedToUser: boolean;
  usedForTracking: boolean;
  source: string;
}
export interface StoreListing {
  appName: string;
  subtitle: string;
  bundleId: string;
  packageName: string;
  version: string;
  description: string;
  keywords?: string[];
  whatsNew?: string;
  privacy: DataCollection[];
  consoleForms: string[];
  screenshots: { platform: string; deviceClass: string; width: number; height: number; minCount: number }[];
  derivation: { detectedCapabilities: string[]; sdks: string[]; notes: string[] };
}

// One "ready to ship?" verdict (mirrors Go publish_status.go, /publish/status).
export interface PublishCheck {
  name: string;
  ok: boolean;
  blocker: boolean;
  detail: string;
}
export interface PublishReadiness {
  checks: PublishCheck[];
  ready: boolean;
  blockers: string[];
}

export type CompanyAIWorkKind =
  | "app-code"
  | "erp-flow"
  | "convex"
  | "web-ui"
  | "harness-cad"
  | "openscad-cad"
  | "robot-trial"
  | "inspection";

export interface CompanyAIResolvedRuntime {
  ok: boolean;
  teamId: string;
  role: string;
  source: string;
  workKind: CompanyAIWorkKind;
  enabled: boolean;
  workKindEnabled: boolean;
  runtimeReady: boolean;
  runtime: {
    mode: CompanyAIOptions["runtime"]["mode"];
    provider: TenantComputeProvider;
    region?: string;
    deviceId: string | null;
    fallbackDeviceIds: string[];
  };
  convex: CompanyAIOptions["convex"];
  runner: {
    id: string;
    model?: string;
    allowedRunners: string[];
    credentialMode: CompanyAIOptions["runners"]["credentialMode"];
    requireRunnerAuthPerUser: boolean;
    allowUserOverride: boolean;
  };
  mcp: CompanyAIOptions["mcp"];
  approvals: CompanyAIOptions["approvals"] & { required: string[] };
  dataPolicy: CompanyAIOptions["dataPolicy"];
  promptPolicy: {
    systemHints: string[];
    artifactKinds: string[];
  };
  nextActions: {
    configureCompanyAI: boolean;
    configureRuntimeDevice: boolean;
    enableWorkKind: boolean;
    reauthRunner: boolean;
  };
  dispatch: {
    target: "yaver-device" | "unresolved";
    deviceId: string | null;
    createTaskPath: string;
    runnerSwitchPath: string;
    runnerStatusPath: string;
    taskOutputPathTemplate: string;
  };
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
  // The agent's own surface answer (workspace_http.go) — derived from the
  // workspace manifest + stack + project config files (Info.plist /
  // AndroidManifest.xml), not from what a framework string can express. The
  // web MUST consume these instead of re-deriving from framework names,
  // otherwise tvos/watchos/visionos/wear-os apps collapse into
  // "Backend & tooling" with no vibe surface (2026-08-13).
  surfaces?: string[];
  testSurfaces?: string[];
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
  surface?: string;
  displaySurface?: string;
  viewport?: RemoteRuntimeViewport;
  checks?: Array<{
    id: string;
    label: string;
    ok: boolean;
    reason?: string;
  }>;
}

export interface RemoteRuntimeViewport {
  label?: string;
  width: number;
  height: number;
}

export interface RemoteRuntimeCapabilities {
  workDir: string;
  framework: string;
  executionMode: "rn-hermes" | "web-webview" | "native-webrtc" | "unsupported";
  primarySurface: "hermes" | "webview" | "webrtc" | "none";
  remoteRuntimeEligible: boolean;
  feedbackSdkCompatible: boolean;
  feedbackSdkNote?: string;
  // "client-shake-remote-sim" (RN sim stream: phone/web shake → remote sim
  // injection → app's own SDK overlay → streams back) | "in-app-sdk" (native).
  feedbackSurface?: string;
  feedbackControlProtocol?: string;
  supportedTransports?: string[];
  currentHostClass?: string;
  cached?: boolean;
  cachedAt?: string;
  probeDurationMs?: number;
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
  displaySurface?: string;
  viewport?: RemoteRuntimeViewport;
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

export interface DogfoodSourceStatus {
  ok: boolean;
  ready: boolean;
  code: string;
  path?: string;
  branch?: string;
  message: string;
  remedy?: string;
}

export interface ProjectPreviewCapability {
  id: string;
  label: string;
  supported: boolean;
  primary?: boolean;
  reason?: string;
}

export interface ProjectPreviewCapabilities {
  framework: string;
  selfDevelopment: boolean;
  options: ProjectPreviewCapability[];
  reason?: string;
}

export interface RemoteRuntimeStopResult {
  ok: boolean;
  type?: "stopped" | string;
  topic?: "remote-runtime/stop" | string;
  sessionId: string;
  status?: "stopped" | string;
  stopped?: boolean;
  previouslyRunning?: boolean;
  verified?: boolean;
  message?: string;
}

export interface TmuxPaneSummary {
  sessionName?: string;
  sessionId?: string;
  windowIndex?: string;
  windowName?: string;
  paneIndex?: string;
  paneId?: string;
  pid?: number;
  agent?: string;
  agentConfirmed?: boolean;
  inputMode?: "interactive" | "task-followup";
  sessionKind?: "task" | "autorun" | "runner" | "other";
  origin?: "yaver-task" | "yaver-autorun" | "yaver-runner" | "manual";
  startedAt?: string;
  runnerHint?: string;
  projectHint?: string;
  taskIdHint?: string;
  taskId?: string;
  preview?: string;
}

export interface TmuxSessionSummary {
  name: string;
  id?: string;
  windows?: number;
  created?: string;
  attached?: boolean;
  relationship?: "adopted" | "forked-by-yaver" | "unrelated" | string;
  agentType?: string;
  mainPid?: number;
  windowIndex?: string;
  windowName?: string;
  paneIndex?: string;
  paneId?: string;
  panePreview?: string;
  taskId?: string;
  sessionKind?: "task" | "autorun" | "runner" | "other";
  origin?: "yaver-task" | "yaver-autorun" | "yaver-runner" | "manual";
  startedAt?: string;
  runnerHint?: string;
  projectHint?: string;
  taskIdHint?: string;
  inputMode?: "interactive" | "task-followup";
  panes?: TmuxPaneSummary[];
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
  providerName?: string;
  lifecycle?: "active" | "legacy";
  source?: string;
  isDefault?: boolean;
  defaultReasoningEffort?: "none" | "low" | "medium" | "high" | "xhigh" | "max" | "ultra";
  supportedReasoningEfforts?: Array<{
    reasoningEffort: "none" | "low" | "medium" | "high" | "xhigh" | "max" | "ultra";
    description?: string;
  }>;
}

export interface TaskRunnerControlCatalog {
  ok: boolean;
  schema: number;
  taskId: string;
  runnerId: string;
  model?: string;
  reasoningEffort?: "none" | "low" | "medium" | "high" | "xhigh" | "max" | "ultra";
  modelSource?: string;
  isAdopted?: boolean;
  models: ModelInfo[];
  controls?: Array<{ id: "model" | "exit"; command: "/model" | "/exit"; label: string; description: string; kind: string; destructive?: boolean }>;
}

export interface TaskRunnerControlResult {
  ok: boolean;
  taskId?: string;
  control?: "model" | "exit";
  model?: string;
  reasoningEffort?: "none" | "low" | "medium" | "high" | "xhigh" | "max" | "ultra";
  status?: TaskStatus;
  verified?: boolean;
  alreadyExited?: boolean;
  error?: string;
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
  description?: string;
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
  environmentKeys?: string[];
  documentationUrl?: string;
  isBuiltin?: boolean;
  source?: string;
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
  /** The runner's own CLI says a credential is here. LOCAL evidence — it
   *  cannot see a server-side revocation. Agent 1.99.384+. */
  authPresent?: boolean;
  authVerified?: boolean;
  /** Epoch ms of the last time the PROVIDER spoke about this credential —
   *  a completed turn, a completed OAuth, or a rejection. Freshness of the
   *  VERDICT, which is not the same as `checkedAt` (freshness of the ROW). */
  authVerifiedAt?: number;
  authSource?: string;
  warning?: string;
  error?: string;
  path?: string;
  detail?: string;
  /** First line of `<bin> --version` (e.g. "Claude Code 2.1.126",
   *  "codex-cli 0.122.0", "1.4.0"). Populated by agent 1.99.147+. */
  version?: string;
}

export interface DevelopmentDoctorFix {
  kind: "install" | "configure" | "open-url";
  label: string;
  method?: string;
  path?: string;
  stream?: string;
  tab?: string;
  url?: string;
}

export interface DevelopmentDoctorCheck {
  id?: string;
  name: string;
  status: "pass" | "warn" | "fail";
  detail: string;
  section: string;
  fix?: DevelopmentDoctorFix;
}

export interface DevelopmentDoctorReport {
  ok: boolean;
  checks: DevelopmentDoctorCheck[];
}

export interface RunnerBrowserAuthSession {
  id: string;
  runner: "claude" | "codex";
  method: string;
  /** See mobile/src/lib/quic.ts — account_not_eligible is a SUCCESSFUL
   *  sign-in against an account with no active plan. Never render it as a
   *  login failure; the retry it implies is the one thing that already worked. */
  status: "starting" | "awaiting_browser" | "verifying" | "completed" | "failed" | "cancelled" | "account_not_eligible";
  openUrl?: string;
  callbackPort?: number;
  code?: string;
  detail?: string;
  authConfigured?: boolean;
  authVerified?: boolean;
  authSource?: string;
  error?: string;
  startedAt: number;
  updatedAt: number;
  completedAt?: number;
  /** When the spawned CLI last wrote ANY output line (unix ms). Surfaces
   *  render "CLI is alive — last output Ns ago" from it instead of an
   *  undifferentiated spinner; the agent has carried it since the
   *  remained.md P0 contract, the web type just never declared it. */
  lastOutputAt?: number;
}

/** True when a runner browser-auth session can no longer change on its
 *  own: completed, failed, cancelled, or account_not_eligible. The last
 *  one is the easy one to get wrong — it is a SUCCESSFUL sign-in against
 *  an account with no eligible plan, so polls must STOP (nothing further
 *  will arrive) and the UI must render the verbatim verdict instead of
 *  an in-progress spinner. Mirrors runnerBrowserAuthTerminal in the Go
 *  agent (runner_auth_browser_http.go). */
export function isRunnerBrowserAuthTerminal(status: RunnerBrowserAuthSession["status"] | string | undefined): boolean {
  return status === "completed" || status === "failed" || status === "cancelled" || status === "account_not_eligible";
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
  checkedAt?: number;
  failure?: {
    kind?: string;
    code?: string;
    title?: string;
    reason?: string;
    remedy?: string;
    runnerId?: string;
    model?: string;
    probe?: string;
    detectedAt?: number | string | Date;
    fix?: {
      type?: string;
      runnerId?: string;
      testAfter?: boolean;
    };
  };
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

/**
 * ForgeKind is the git host family. Named "forge", not "provider": in this
 * codebase `provider` already means OAuth login identity, IaaS vendor, TTS
 * engine, and LLM vendor depending on the file. It is a closed union on
 * purpose — the older `GitProviderStatusRow.provider` is a bare `string`,
 * which is why nothing could ever switch on it exhaustively.
 */
export type ForgeKind = "github" | "gitlab";

/** ForgeRole is the provider-neutral permission level (agent maps it to
 *  GitHub's pull/push/admin and GitLab's 10/20/30/40/50). */
export type ForgeRole = "read" | "triage" | "write" | "maintain" | "admin";

export interface ForgeMember {
  username: string;
  name?: string;
  role: ForgeRole;
  /** What the forge itself calls this role ("push", "developer") — shown so
   *  the UI never has to pretend the two vocabularies are the same. */
  nativeRole?: string;
  /** "active" | "pending" — pending means invited but not yet accepted. */
  state?: string;
  avatarUrl?: string;
  profileUrl?: string;
  id?: string | number;
}

export interface ForgeInvite {
  username?: string;
  email?: string;
  role: ForgeRole;
  /** invited | added | already_member. "added" means they already had access
   *  and NO invitation email went out — see the agent's forge_github.go. */
  state: string;
  inviteId?: string | number;
  url?: string;
  message?: string;
}

/** Common envelope on every forge verb result. `via` reports whether the call
 *  went through the gh/glab CLI or direct REST — the first thing worth knowing
 *  when a call unexpectedly 403s. */
export interface ForgeResultBase {
  repo: string;
  host: string;
  kind: ForgeKind;
  via: string;
}

export interface ForgeMembersResult extends ForgeResultBase {
  members: ForgeMember[];
  count: number;
}

export interface ForgeInviteResult extends ForgeResultBase {
  user: string;
  role: ForgeRole;
  invite: ForgeInvite;
}

/** Identifies which repo a forge verb should act on. All fields optional:
 *  the agent resolves explicit repo > directory > cwd. */
export interface ForgeTarget {
  repo?: string;
  directory?: string;
  host?: string;
  kind?: ForgeKind;
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

export interface ManagedGitBackupMeta {
  path: string;
  target?: string;
  sizeBytes: number;
  commit?: string;
  createdAt: string;
}

export interface ManagedGitExternalBackupMeta {
  targetKind: "local-folder" | "shared-storage" | "dropbox" | string;
  targetId?: string;
  path: string;
  sizeBytes: number;
  commit?: string;
  createdAt: string;
}

export interface ManagedGitMirrorMeta {
  provider: "github" | "gitlab" | string;
  host: string;
  fullName: string;
  cloneUrl: string;
  visibility: string;
  lastPushAt?: string;
}

export interface ManagedGitProjectMeta {
  repoId: string;
  enabled: boolean;
  visibility: "private" | "unlisted" | "public" | string;
  defaultBranch: string;
  barePath: string;
  workDir: string;
  lastCommit?: string;
  lastBackup?: ManagedGitBackupMeta | null;
  externalBackups?: ManagedGitExternalBackupMeta[];
  mirrors?: ManagedGitMirrorMeta[];
  createdAt: string;
  updatedAt: string;
}

export interface ManagedGitRelaySourceFilePatch {
  path: string;
  content: string;
}

export interface ManagedGitRelaySourcePlanResult {
  ok: boolean;
  repoId?: string;
  branch?: string;
  baseBranch?: string;
  mode: "apply_patch" | "prepare_only" | "compute_required" | string;
  relayEligible: boolean;
  canApply: boolean;
  filesPlanned?: string[];
  commitMessage?: string;
  reasons?: string[];
}

export interface ManagedGitRelaySourceBranchResult {
  ok: boolean;
  repoId: string;
  branch: string;
  baseBranch: string;
  commit: string;
}

export interface ManagedGitRelaySourceApplyResult extends ManagedGitRelaySourceBranchResult {
  filesChanged: string[];
  mirrorsPushed?: string[];
  providerBranches?: Array<{
    providerKind: string;
    providerHost: string;
    providerRepo: string;
    providerBranch: string;
    providerBranchUrl?: string;
    providerAuthMode: string;
    providerAuthStatus: string;
  }>;
  noop?: boolean;
}

export interface ManagedGitRelaySourceWorkResult {
  ok: boolean;
  intent?: unknown;
  plan?: ManagedGitRelaySourcePlanResult;
  prepare?: ManagedGitRelaySourceBranchResult;
  apply?: ManagedGitRelaySourceApplyResult;
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
  reasoningEffort?: "none" | "low" | "medium" | "high" | "xhigh" | "max" | "ultra";
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
  taskId?: string;
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

export interface InfraCapabilities {
  terminal: boolean;
  mcp: boolean;
  devServices: boolean;
  systemServices: boolean;
  agentShutdown: boolean;
  hostReboot: boolean;
}

/** One row of the power capability report — the agent's answer to "what would
 *  this action ACTUALLY do on this machine?".
 *
 *  `available` is the ONLY field that may enable a control. A container reports
 *  host_reboot as unavailable even when it is running as root, because there is
 *  no host to reboot from inside one; `means` says so in words the user reads.
 *  Never re-derive availability on the client — the agent probed the box, the
 *  browser did not. */
export interface PowerAction {
  id: "host_reboot" | "agent_restart" | "agent_shutdown";
  label: string;
  available: boolean;
  destructive: boolean;
  scope: "machine" | "agent" | "none";
  /** What this action really does on THIS machine, in one sentence. */
  means: string;
  /** What it destroys. Rendered in the confirm dialog. */
  loses?: string[];
  /** Why it is unavailable. Empty when available. */
  reason?: string;
  /** The specific fix — a command or a named flow, never "check your config". */
  remedy?: string;
  /** The command the agent would run. This is the dry-run answer. */
  command?: string;
  /** Bounded recovery expectation, in seconds. */
  etaSeconds?: number;
  /** The achievable action to offer instead when this one is unavailable. */
  alternative?: PowerAction["id"];
}

export interface PowerReport {
  facts: {
    goos: string;
    isRoot: boolean;
    passwordlessSudo: boolean;
    container?: string;
    wslVersion?: number;
    serviceManager?: string;
    agentUser?: string;
  };
  actions: PowerAction[];
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
  sandbox: SandboxStatus;
  capabilities: InfraCapabilities;
  rebootGrant?: {
    canReboot: boolean;
    needsSudo: boolean;
    agentUser: string;
    grantHint?: string;
    granted: boolean;
    checkedAt: number;
  };
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

export interface MobilePlatformSurface {
  id: string;
  label: string;
  family: string;
  surface: string;
  status: string;
  buildSupported: boolean;
  submitSupported: boolean;
  managedCloud: string;
  requiredHost: string;
  storeTarget?: string;
  deployTarget?: string;
  script?: string;
  scriptPresent?: boolean;
  queueTargets?: string[];
  notes?: string[];
  limitations?: string[];
}

export interface MobilePlatformMatrixReport {
  devicePlatform: string;
  deviceArch: string;
  surfaces: MobilePlatformSurface[];
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
  enabledMode?: "off" | "host";
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
  recommendedMode?: "host";
  recommendedReason?: string;
  quickstartAvailable?: boolean;
}

export interface SandboxConfig {
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

export type OutputCallback = (taskId: string, line: string) => void;
export type ConnectionStateCallback = (state: ConnectionState) => void;

type EventMap = {
  output: OutputCallback;
  connectionState: ConnectionStateCallback;
};

type EventName = keyof EventMap;

// ── Client ───────────────────────────────────────────────────────────

export interface McpServer {
  name: string;
  url: string;
  enabled: boolean;
  hasAuth?: boolean;
  toolCount?: number;
}

export interface McpServerInput {
  name: string;
  url: string;
  auth_token?: string;
  enabled?: boolean;
}

export type CreateTaskParams = {
  title: string;
  description: string;
  userPrompt?: string;
  runner?: string;
  model?: string;
  reasoningEffort?: string;
  mode?: string;
  customCommand?: string;
  projectName?: string;
  workDir?: string;
  projectDir?: string;
  mcpServers?: string[];
  videoEnabled?: boolean;
  videoSource?: "browser" | "sim-ios" | "sim-android" | "phone" | "";
  askMode?: boolean;
  allowLocalFallback?: boolean;
  /** Runner/render split: git identity + push policy for the runner box's
   *  ensure-clone + converge (send only when a split is active). */
  gitRemote?: string;
  gitBranch?: string;
  autoPush?: "never" | "ask" | "always" | "";
  /** Yaver goal-mode objective (opencode goal plugin). When set, the task
   *  runs as a persistent goal the opencode runner keeps working toward
   *  across turns (create_goal + idle auto-continue) until complete with
   *  evidence, blocked, or a safety limit. Empty = one-shot task. Only the
   *  opencode runner honors it; other runners ignore the field. Surfaces
   *  set this when the composer input is `/goal <objective>`. */
  goal?: string;
  /** Whether the runner sees Yaver's own `yaver mcp` doorway. Defaults
   *  true (absent = include). A surface sets false when the user explicitly
   *  deselects the `yaver` chip, so the runner gets ONLY the external MCPs
   *  in mcpServers — possibly none. */
  includeYaverMcp?: boolean;
  sessionStartedFrom?: "tasks" | "vibing" | "new-application" | "mobile-workspace";
  sessionSettings?: ClientSessionSettings;
};

export function buildCreateTaskBody(params: CreateTaskParams): Record<string, unknown> {
  // LAST LINE OF DEFENCE FOR THE MODEL (2026-08-02).
  //
  // Re-ordering the picker catalogues fixed the DEFAULT, but the model is also
  // a stored per-device setting — so a `gpt-5.4` saved before that fix kept
  // being dispatched at a ChatGPT-account Codex login that can never run it,
  // and the live dashboard still showed `MODEL / gpt-5.4` afterwards. Caught by
  // e2e/vibing-truth-loop.mjs against the real account, not by reading code.
  //
  // This is the single funnel every web task dispatch goes through, so the
  // coercion belongs here: whatever any surface believes, the request that
  // leaves the browser cannot carry a model we have WATCHED this runner refuse.
  //
  // Only observed refusals are rewritten (see runnerModelCompat) — an unknown
  // model is passed through untouched, so this can never silently override a
  // deliberate choice we have no evidence against.
  const resolvedModel = resolveUsableModel(params.runner ?? "", params.model ?? "").model ?? params.model ?? "";
  return {
    title: params.title,
    description: params.description,
    userPrompt: params.userPrompt ?? "",
    runner: params.runner ?? "",
    model: resolvedModel,
    reasoningEffort: params.reasoningEffort ?? "",
    mode: params.mode ?? "",
    customCommand: params.customCommand ?? "",
    projectName: params.projectName ?? "",
    workDir: params.workDir ?? "",
    projectDir: params.projectDir ?? "",
    mcpServers: params.mcpServers ?? [],
    videoEnabled: params.videoEnabled ?? false,
    videoSource: params.videoSource ?? "",
    askMode: params.askMode ?? false,
    allowLocalFallback: params.allowLocalFallback ?? false,
    // Runner/render split: the project's git identity + push policy, so a
    // runner box without the source can ensure-clone before spawn and
    // converge back through git afterwards (agent task_ensure_clone.go).
    gitRemote: params.gitRemote ?? "",
    gitBranch: params.gitBranch ?? "",
    autoPush: params.autoPush ?? "",
    goal: params.goal ?? "",
    // New conversations are No MCP unless the surface carries an explicit
    // user choice (or an enabled Use latest preference).
    includeYaverMcp: params.includeYaverMcp ?? false,
    source: "web",
    sessionStartedFrom: params.sessionStartedFrom ?? "tasks",
    sessionSettings: params.sessionSettings ?? browserSessionSettings(),
  };
}

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

  // ── Machine-role routing (runner/render split) ─────────────────────
  // Optional slicing from userSettings.machineRolesByProject: AI tasks run
  // on the "runner" box while dev servers / previews / remote-runtime
  // sessions stay on the "render" box. The dashboard sets these after
  // loading /settings; whichever role matches the connected device is a
  // no-op, so the single-box default is byte-identical to before. Routing
  // rides the relay device path (`/d/<deviceId>/…`): the same bearer token
  // authenticates the same user on every box they own, and each box
  // enforces auth per request — a forged route grants nothing.
  private _runnerRouteDeviceId: string | null = null;
  private _renderRouteDeviceId: string | null = null;

  setMachineRoleRoutes(routes: { runnerDeviceId?: string | null; renderDeviceId?: string | null }): void {
    this._runnerRouteDeviceId = routes.runnerDeviceId || null;
    this._renderRouteDeviceId = routes.renderDeviceId || null;
  }

  /** Effective runner device /tasks/* traffic routes to, or null when tasks
   *  run on the connected device (single-box default). */
  get taskRouteDeviceId(): string | null {
    const id = this._runnerRouteDeviceId;
    return id && id !== this.deviceId ? id : null;
  }

  /** Effective render device /dev/* + preview traffic routes to, or null
   *  when the connected device renders (single-box default). */
  get renderRouteDeviceId(): string | null {
    const id = this._renderRouteDeviceId;
    return id && id !== this.deviceId ? id : null;
  }

  private roleBase(deviceId: string | null, role: "AI runner" | "render"): string {
    if (!deviceId) return this.baseUrl;
    // Same-origin relay proxy wins over the raw relay URL when the dashboard
    // itself is served from the proxy host (https://yaver.io). The proxy
    // injects X-Relay-Password server-side and self-heals missing/invalid
    // passwords via /settings/repair-relay, while the raw
    // `https://public.yaver.io/d/<id>` form sends only a bearer token and
    // 401s when the relay password is stale — the exact class that made
    // stream reattach retry the SAME dead URL five times (2026-08-09 audit:
    // "LIVE OUTPUT LOST … could not be picked back up after 5 attempts").
    // Every (re)subscribe rebuilds this URL, so the retry ladder gets a fresh,
    // working candidate per attempt instead of the dead relay leg.
    if (deviceId && this.sameOriginProxyUsable) {
      return `/d/${deviceId}`;
    }
    if (this._activeRelayUrl) return `${this._activeRelayUrl}/d/${deviceId}`;
    // Primary transport is direct/tunnel (localhost, Tailscale, mesh). Cross-
    // device role traffic still rides the relay: the relay authorizes each
    // /d/<id>/ request against the caller's per-user password with backend
    // ownership scope (relay/server.go handleProxy → validateRelayAccessE),
    // so the same credential reaches every box the user owns — on the free
    // relay and Relay Pro identically (entitlement resolves per request).
    const fallback = this.relayServers[0];
    if (fallback?.httpUrl) return `${fallback.httpUrl.replace(/\/+$/, "")}/d/${deviceId}`;
    // Named refusal — never a silent fallback onto the wrong box.
    throw new Error(
      `Your configured ${role} machine (${deviceId.slice(0, 8)}…) is only reachable over a relay, ` +
      `but this session has no relay configured and is connected ${this._activeTunnelUrl ? "through a direct tunnel" : "directly"} to another machine. ` +
      `Nothing was sent to the wrong box — sign in again to refresh the relay list, or clear the machine-roles split in Settings.`,
    );
  }

  /** True when the dashboard is served from the host that runs the /d/<id>
   *  same-origin relay proxy (yaver.io). Cross-origin deployments (a local
   *  dev server on :3000, a self-hosted dashboard) fall through to the raw
   *  relay URL. */
  private get sameOriginProxyUsable(): boolean {
    if (typeof window === "undefined") return false;
    try {
      const host = window.location.hostname;
      return host === "yaver.io" || host.endsWith(".yaver.io");
    } catch {
      return false;
    }
  }

  /** Relay password usable for role-routed cross-device requests: the active
   *  transport's when connected via relay, else the highest-priority
   *  configured relay's (the with/without-Tailscale case — primary transport
   *  direct, role traffic via relay). */
  private get routingRelayPassword(): string | null {
    return this.activeRelayPassword ?? this.relayServers.find((r) => r.password)?.password ?? null;
  }

  /** Base URL for task dispatch/stream — the runner box when a machine-role
   *  split is active, else the connected box. Throws a named error rather
   *  than silently addressing the wrong machine. */
  private get taskBaseUrl(): string {
    return this.roleBase(this.taskRouteDeviceId, "AI runner");
  }

  /** Base URL for dev-server / preview / remote-runtime calls — the render
   *  box when a machine-role split is active, else the connected box. */
  private get devBaseUrl(): string {
    return this.roleBase(this.renderRouteDeviceId, "render");
  }

  /** Null-returning variant for URL getters used during React render —
   *  a throw there would crash the tree instead of showing "no preview". */
  private get devBaseUrlOrNull(): string | null {
    try { return this.devBaseUrl; } catch { return null; }
  }

  /** deviceId for same-origin `/d/<id>/…` preview-proxy URLs (iframes). */
  private get devProxyDeviceId(): string | null {
    return this.renderRouteDeviceId ?? this.deviceId;
  }
  private _connectionState: ConnectionState = "disconnected";
  private pollInterval: ReturnType<typeof setInterval> | null = null;
  private _lastConnectDiagnostics: ConnectAttemptDiagnostic[] = [];

  // Reconnection
  private reconnectTimer: ReturnType<typeof setTimeout> | null = null;
  private reconnectAttempt = 0;
  private readonly maxReconnectAttempt = 8;
  private readonly baseBackoffMs = 1000;
  // Last connect failure, preserved through the ladder so the give-up (and
  // any terminal verdict) can state the CAUSE instead of a bare count
  // (audit gap T2 — the web ladder used to stop silently at 8).
  private _lastConnectError: string | null = null;
  // Repair rung fired once per failure streak (mobile quic.ts parity);
  // reset on every successful connect.
  private reconnectRepairAttempted = false;
  // Topology-refresh rung: re-pulls relay list + passwords from Convex so a
  // relay restart / box move doesn't strand the ladder on stale coordinates.
  // Wired by the dashboard shell (which owns the Convex fetch).
  private topologyRefreshHook: (() => Promise<void>) | null = null;

  /** Last connect failure (or named give-up / terminal verdict). Readable by
   *  UI surfaces so "not connected" can say why. */
  get lastConnectError(): string | null {
    return this._lastConnectError;
  }

  setTopologyRefreshHook(hook: (() => Promise<void>) | null): void {
    this.topologyRefreshHook = hook;
  }

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
    // Known-dead endpoint shapes (<uuid>.yaver.io — no DNS; *.dev.yaver.io —
    // no cert) are dropped by the ONE shared predicate (lib/endpoints.ts)
    // before attemptConnect() ever dials them.
    this.tunnelCandidates = Array.from(new Set((opts?.tunnelUrls || []).map((url) => String(url || "").trim()).filter(Boolean))).filter(isUsablePublicEndpoint);
    this.reconnectAttempt = 0;
    this.reconnectRepairAttempted = false;
    this._lastConnectError = null;

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

  // ── Screen context ─────────────────────────────────────────────────
  //
  // Forwarding half of "the agent knows which screen you're looking at". The
  // probe the agent injects into the preview posts its observation to this
  // window (it cannot call the agent itself — the /dev/ preview route is
  // unauthenticated by design); we relay it over the session's own authed
  // channel. See web/lib/screenContext.ts and desktop/agent/screen_context.go.

  /** Report the screen currently rendered in the preview for `workDir`. */
  async reportScreenContext(ctx: ScreenContextReport): Promise<void> {
    if (!this.isConnected || !ctx.workDir) return;
    // Deliberately swallow failures. This is advisory context: it must never
    // sit in the critical path of the prompt the user is trying to send, and a
    // toast about a failed screen report would be pure noise.
    try {
      await fetch(`${this.taskBaseUrl}/screen-context`, {
        method: "POST",
        headers: { ...this.authHeaders, "Content-Type": "application/json" },
        body: JSON.stringify(ctx),
      });
    } catch {
      /* advisory only */
    }
  }

  /** Drop what we already reported. Called when the user opts out or the
   *  preview closes, so "off" means the agent is not holding their screen —
   *  not that it holds it and promises not to look. */
  async clearScreenContext(workDir: string): Promise<void> {
    if (!this.isConnected || !workDir) return;
    try {
      await fetch(`${this.taskBaseUrl}/screen-context?workDir=${encodeURIComponent(workDir)}`, {
        method: "DELETE",
        headers: { ...this.authHeaders },
      });
    } catch {
      /* advisory only */
    }
  }

  // ── DOM mode ───────────────────────────────────────────────────────
  //
  // Forwarding half of "the element the user clicked in the preview". The
  // probe the agent injects into the preview posts the selected element to
  // this window (it cannot call the agent itself — the /dev/ preview route is
  // unauthenticated by design); we relay it over the session's own authed
  // channel. See web/lib/domInspect.ts and desktop/agent/dom_inspect.go.

  /** Report the element the user clicked in the preview for `workDir`. */
  async reportDomInspect(el: DomElementReport): Promise<void> {
    if (!this.isConnected || !el.workDir) return;
    // Deliberately swallow failures: same advisory discipline as
    // reportScreenContext. A failed element report must not block the prompt.
    try {
      await fetch(`${this.taskBaseUrl}/dom-inspect`, {
        method: "POST",
        headers: { ...this.authHeaders, "Content-Type": "application/json" },
        body: JSON.stringify(el),
      });
    } catch {
      /* advisory only */
    }
  }

  /** Drop the element we already reported. Called when DOM mode is switched
   *  off, so "off" means the agent is not holding the element — not that it
   *  holds it and promises not to look. */
  async clearDomInspect(workDir: string): Promise<void> {
    if (!this.isConnected || !workDir) return;
    try {
      await fetch(`${this.taskBaseUrl}/dom-inspect?workDir=${encodeURIComponent(workDir)}`, {
        method: "DELETE",
        headers: { ...this.authHeaders },
      });
    } catch {
      /* advisory only */
    }
  }

  /** Report the interactive-items inventory for `workDir` (from the probe's
   *  `yaver-dom-items-list` answer). */
  async reportDomItems(items: DomItemsReport): Promise<void> {
    if (!this.isConnected || !items.workDir) return;
    try {
      await fetch(`${this.taskBaseUrl}/dom-inspect/items`, {
        method: "POST",
        headers: { ...this.authHeaders, "Content-Type": "application/json" },
        body: JSON.stringify(items),
      });
    } catch {
      /* advisory only */
    }
  }

  /** Fetch the pickable interactive-items inventory for `workDir`, or null
   *  when none is fresh. */
  async domItems(workDir: string): Promise<{ items: NonNullable<DomItemsReport["items"]>; capturedAt?: number } | null> {
    if (!this.isConnected || !workDir) return null;
    try {
      const res = await fetch(`${this.taskBaseUrl}/dom-inspect/items?workDir=${encodeURIComponent(workDir)}`, {
        method: "GET",
        headers: { ...this.authHeaders },
      });
      if (!res.ok) return null;
      const data = await res.json();
      if (!data?.present || !Array.isArray(data.items)) return null;
      return { items: data.items, capturedAt: data.capturedAt };
    } catch {
      return null;
    }
  }

  // ── Task API ───────────────────────────────────────────────────────

  async sendTask(title: string, description: string, opts?: { runner?: string; model?: string; reasoningEffort?: "none" | "low" | "medium" | "high" | "xhigh" | "max" | "ultra" }): Promise<Task> {
    this.assertConnected();
    const body: Record<string, unknown> = { title, description, source: "web" };
    if (opts?.runner) body.runner = opts.runner;
    if (opts?.model) body.model = opts.model;
    if (opts?.reasoningEffort) body.reasoningEffort = opts.reasoningEffort;
    const res = await this.fetchWithTimeout(`${this.taskBaseUrl}/tasks`, {
      method: "POST",
      headers: { ...this.authHeaders, "Content-Type": "application/json" },
      body: JSON.stringify(body),
    }, 30_000).catch((err: any) => {
      if (err?.name === "AbortError") {
        throw new Error(
          "Timed out waiting for the selected machine to accept the task after 30s. Your prompt was not cleared — check that machine's route and retry.",
        );
      }
      throw err;
    });
    if (!res.ok) {
      const cloudRequired = await decodeCloudWorkspaceRequiredError(res);
      if (cloudRequired) throw cloudRequired;
      const err: any = new Error(await responseErrorMessage(res, `Failed to create task: ${res.status}`));
      try {
        const data = await res.clone().json();
        err.capabilityGap = data?.capabilityGap;
        err.errorSummary = data?.errorSummary;
      } catch {}
      throw err;
    }
    const data = await res.json().catch(() => ({}));
    return {
      id: data.taskId,
      title,
      description,
      status: data.status,
      runnerId: data.runnerId || opts?.runner,
      model: data.model || opts?.model,
      output: [],
      capabilityGap: data.capabilityGap,
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
    reasoningEffort?: string;
    /** Runner-specific subcommand selector. Currently honored by
     *  opencode where it maps to `--agent <mode>` (build / plan /
     *  any custom agent the user has defined in opencode.json).
     *  Other runners ignore it. */
    mode?: string;
    customCommand?: string;
    projectName?: string;
    workDir?: string;
    projectDir?: string;
    mcpServers?: string[];
    /** Toggle the post-completion video summary. When true, after
     *  the task finishes the agent records a short MP4 demonstration
     *  via vibe-preview (sim/emulator MP4 for mobile, browser frame
     *  burst for web). Result lands as Task.videoClipId; UI renders a
     *  "▶ Watch demo" button. */
    videoEnabled?: boolean;
    /** Override the auto-detected source: browser | sim-ios | sim-android
     *  | phone. Empty = let the agent infer from workDir. */
    videoSource?: "browser" | "sim-ios" | "sim-android" | "phone" | "";
    /** Run as a grounded deep question-answer (repo analysis + file:line
     *  cites, escalate-on-breadth, explain-first with a confirm gate)
     *  instead of a work run. The console sets this when the typed input is
     *  a natural-language question rather than a build instruction. */
    askMode?: boolean;
    /** Internal handoff guard: true only when posting a client-held task body
     *  to the assigned Cloud Workspace after placement/activation already
     *  selected that target. Prevents the target agent from re-deferring the
     *  same task back into another pending-cloud placeholder. */
    allowLocalFallback?: boolean;
    /** Runner/render split: git identity + push policy for ensure-clone +
     *  converge on the runner box. Send only when a split is active. */
    gitRemote?: string;
    gitBranch?: string;
    autoPush?: "never" | "ask" | "always" | "";
    /** Yaver goal-mode objective (opencode goal plugin). Set when the
     *  composer input was `/goal <objective>`; empty for one-shot tasks. */
    goal?: string;
    /** Whether the runner sees Yaver's own `yaver mcp` doorway (default
     *  true). Set false when the user deselects the `yaver` chip. */
    includeYaverMcp?: boolean;
  }): Promise<Task> {
    this.assertConnected();
    // The create chain must be BOUNDED — a hung agent /tasks route used to
    // leave the web dashboard's send button stuck on "…" forever (mobile's
    // sendTask already carries a 30s timeout for the same reason). 30s is
    // generous for task acceptance; the stream does the long tail.
    const res = await this.fetchWithTimeout(
      `${this.taskBaseUrl}/tasks`,
      {
        method: "POST",
        headers: { ...this.authHeaders, "Content-Type": "application/json" },
        body: JSON.stringify(buildCreateTaskBody(params)),
      },
      30_000,
    ).catch((err: any) => {
      if (err?.name === "AbortError") {
        throw new Error(
          "Timed out waiting for the machine to accept the task after 30s. The box may be busy or unreachable — check its status dot and try again.",
        );
      }
      throw err;
    });
    if (!res.ok) {
      const cloudRequired = await decodeCloudWorkspaceRequiredError(res);
      if (cloudRequired) throw cloudRequired;
      const err: any = new Error(await responseErrorMessage(res, `Failed to create task: ${res.status}`));
      try {
        const data = await res.clone().json();
        err.capabilityGap = data?.capabilityGap;
        err.errorSummary = data?.errorSummary;
      } catch {}
      throw err;
    }
    const data = await res.json().catch(() => ({}));
    const task = await this.getTask(data.taskId);
    return { ...task, capabilityGap: data.capabilityGap || task.capabilityGap };
  }

  /** Initialize a project and atomically begin its hidden Developing kickoff.
   *  This route is shared by web, Electron, TV, spatial, and native clients. */
  async startProject(params: {
    name: string;
    gitProvider?: ProjectStartGitProvider;
    palette?: string;
  }): Promise<ProjectStartResult> {
    this.assertConnected();
    const res = await this.fetchWithTimeout(
      `${this.taskBaseUrl}/project/start`,
      {
        method: "POST",
        headers: { ...this.authHeaders, "Content-Type": "application/json" },
        body: JSON.stringify({
          name: params.name,
          gitProvider: params.gitProvider || "yaver-git",
          palette: params.palette || "ocean",
        }),
      },
      60_000,
    );
    if (!res.ok) {
      throw new Error(await responseErrorMessage(res, `Could not start project: ${res.status}`));
    }
    return res.json() as Promise<ProjectStartResult>;
  }

  async listTasks(limit?: number): Promise<Task[]> {
    if (!this.isConnected) {
      return this.getCachedTasks();
    }
    try {
      const url = limit ? `${this.taskBaseUrl}/tasks?limit=${limit}` : `${this.taskBaseUrl}/tasks`;
      const res = await fetch(url, {
        headers: this.authHeaders,
      });
      if (!res.ok) throw new Error(`Failed to list tasks: ${res.status}`);
      const data = await res.json();
      const rawTasks = data.tasks || [];
      const deviceId = this.taskRouteDeviceId ?? this.deviceId ?? undefined;
      const tasks: Task[] = rawTasks.map((t: any) => ({
        id: t.id,
        title: t.title,
        description: t.description,
        status: t.status,
        deviceId,
        runnerId: t.runnerId || undefined,
        model: t.model || undefined,
        reasoningEffort: t.reasoningEffort || undefined,
        output: typeof t.output === "string" && t.output
          ? t.output.split("\n").filter((l: string) => l)
          : Array.isArray(t.output) ? t.output : [],
        resultText: t.resultText || undefined,
        rawOutput: typeof t.rawOutput === "string" ? t.rawOutput : undefined,
        rawOffset: typeof t.rawOffset === "number" ? t.rawOffset : undefined,
        presentation: Array.isArray(t.presentation) ? t.presentation : undefined,
        failure: t.failure || undefined,
        costUsd: t.costUsd || undefined,
        turns: t.turns || undefined,
        pendingFollowUps: Array.isArray(t.pendingFollowUps) ? t.pendingFollowUps : undefined,
        createdAt: t.createdAt ? new Date(t.createdAt).getTime() : Date.now(),
        updatedAt: t.finishedAt
          ? new Date(t.finishedAt).getTime()
          : t.startedAt
            ? new Date(t.startedAt).getTime()
            : t.createdAt ? new Date(t.createdAt).getTime() : Date.now(),
        deviceName: t.deviceName || this.host || undefined,
        tmuxSessionId: t.tmuxSessionId || undefined,
        tmuxSession: t.tmuxSession || undefined,
        sessionId: t.sessionId || undefined,
        hostKind: t.hostKind || t.executionSession?.hostKind || undefined,
        executionSession: t.executionSession || undefined,
        sessionSettings: t.sessionSettings || undefined,
        capabilityGap: t.capabilityGap || undefined,
        // Video + proof fields ride the same task JSON. These were silently
        // dropped by this mapper before, which made videoStatus undefined on
        // web forever — the "▶ Watch demo" chip could never appear.
        videoEnabled: t.videoEnabled || undefined,
        videoSource: t.videoSource || undefined,
        videoClipId: t.videoClipId || undefined,
        videoStatus: t.videoStatus || undefined,
        proofStatus: t.proofStatus || undefined,
        proofUrl: t.proofUrl || undefined,
        commitSha: t.commitSha || undefined,
        commitSubject: t.commitSubject || undefined,
        commitBranch: t.commitBranch || undefined,
        diffShortstat: t.diffShortstat || undefined,
        feedbackId: t.feedbackId || undefined,
      }));
      this.cacheTasks(tasks);
      return tasks;
    } catch {
      return this.getCachedTasks();
    }
  }

  async getTask(taskId: string): Promise<Task> {
    this.assertConnected();
    const res = await fetch(`${this.taskBaseUrl}/tasks/${taskId}`, {
      headers: this.authHeaders,
    });
    if (!res.ok) throw new Error(`Failed to get task: ${res.status}`);
    const data = await res.json();
    const t = data.task || data;
    const deviceId = this.taskRouteDeviceId ?? this.deviceId ?? undefined;
    return {
      id: t.id,
      title: t.title,
      description: t.description,
      status: t.status,
      deviceId,
      runnerId: t.runnerId || undefined,
      model: t.model || undefined,
      reasoningEffort: t.reasoningEffort || undefined,
      output: typeof t.output === "string" && t.output
        ? t.output.split("\n").filter((l: string) => l)
        : Array.isArray(t.output) ? t.output : [],
      rawOutput: typeof t.rawOutput === "string" ? t.rawOutput : undefined,
      rawOffset: typeof t.rawOffset === "number" ? t.rawOffset : undefined,
      resultText: t.resultText || undefined,
      presentation: Array.isArray(t.presentation) ? t.presentation : undefined,
      failure: t.failure || undefined,
      costUsd: t.costUsd || undefined,
      turns: t.turns || undefined,
      pendingFollowUps: Array.isArray(t.pendingFollowUps) ? t.pendingFollowUps : undefined,
      createdAt: t.createdAt ? new Date(t.createdAt).getTime() : Date.now(),
      updatedAt: t.finishedAt
        ? new Date(t.finishedAt).getTime()
        : t.startedAt
          ? new Date(t.startedAt).getTime()
          : t.createdAt ? new Date(t.createdAt).getTime() : Date.now(),
      deviceName: t.deviceName || this.host || undefined,
      tmuxSessionId: t.tmuxSessionId || undefined,
      tmuxSession: t.tmuxSession || undefined,
      sessionId: t.sessionId || undefined,
      hostKind: t.hostKind || t.executionSession?.hostKind || undefined,
      executionSession: t.executionSession || undefined,
      sessionSettings: t.sessionSettings || undefined,
      capabilityGap: t.capabilityGap || undefined,
      // Same video/proof passthrough as listTasks — keep both mappers in sync.
      videoEnabled: t.videoEnabled || undefined,
      videoSource: t.videoSource || undefined,
      videoClipId: t.videoClipId || undefined,
      videoStatus: t.videoStatus || undefined,
      proofStatus: t.proofStatus || undefined,
      proofUrl: t.proofUrl || undefined,
      commitSha: t.commitSha || undefined,
      commitSubject: t.commitSubject || undefined,
      commitBranch: t.commitBranch || undefined,
      diffShortstat: t.diffShortstat || undefined,
      feedbackId: t.feedbackId || undefined,
    };
  }

  async getTaskRunnerControls(taskId: string): Promise<TaskRunnerControlCatalog> {
    this.assertConnected();
    const res = await this.fetchWithTimeout(`${this.taskBaseUrl}/tasks/${encodeURIComponent(taskId)}/control`, {
      headers: this.authHeaders,
    }, 10_000);
    const payload = await res.json().catch(() => ({}));
    if (!res.ok || payload?.ok === false) throw new Error(payload?.error || (res.status === 404
      ? "Task controls are unavailable on this machine. Update its Yaver agent, reconnect, and try /model or /exit again."
      : `Could not load runner controls (${res.status})`));
    return payload as TaskRunnerControlCatalog;
  }

  async applyTaskRunnerControl(
    taskId: string,
    input: { control: "model"; model: string; reasoningEffort?: "none" | "low" | "medium" | "high" | "xhigh" | "max" | "ultra" } | { control: "exit"; confirmed: true },
  ): Promise<TaskRunnerControlResult> {
    this.assertConnected();
    const res = await this.fetchWithTimeout(`${this.taskBaseUrl}/tasks/${encodeURIComponent(taskId)}/control`, {
      method: "POST",
      headers: { ...this.authHeaders, "Content-Type": "application/json" },
      body: JSON.stringify(input),
    }, input.control === "exit" ? 20_000 : 10_000);
    const payload = await res.json().catch(() => ({})) as TaskRunnerControlResult;
    if (!res.ok || !payload.ok) throw new Error(payload.error || (res.status === 404
      ? "Task controls are unavailable on this machine. Update its Yaver agent, reconnect, and try again."
      : `Runner control failed (${res.status})`));
    return payload;
  }

  async stopTask(taskId: string): Promise<void> {
    this.assertConnected();
    const res = await this.fetchWithTimeout(
      `${this.taskBaseUrl}/tasks/${taskId}/stop`,
      { method: "POST", headers: this.authHeaders },
      15_000,
    ).catch((err: any) => {
      if (err?.name === "AbortError") throw new Error("Timed out stopping the task after 15s.");
      throw err;
    });
    if (!res.ok) throw new Error(`Failed to stop task: ${res.status}`);
  }

  async continueTask(taskId: string, input: string, mode?: string, sessionSettings: ClientSessionSettings = browserSessionSettings()): Promise<TaskExecutionIdentity> {
    this.assertConnected();
    const res = await this.fetchWithTimeout(
      `${this.taskBaseUrl}/tasks/${taskId}/continue`,
      {
        method: "POST",
        headers: { ...this.authHeaders, "Content-Type": "application/json" },
        body: JSON.stringify({ input, ...(mode ? { mode } : {}), sessionSettings }),
      },
      30_000,
    ).catch((err: any) => {
      if (err?.name === "AbortError") {
        throw new Error("Timed out sending the follow-up after 30s — the machine may be busy.");
      }
      throw err;
    });
    // 409 = the agent kept the prompt instead of spending it on a runner it
    // already knows cannot serve it. Not a failure — a promise. Keyed off the
    // structured `code`, never a regex on the sentence.
    if (res.status === 409) {
      let parked: ParkedTurnRejection | null = null;
      try {
        parked = (await res.json()) as ParkedTurnRejection;
      } catch {
        // fall through to the generic error below
      }
      if (parked?.parked) throw new ParkedTurnError(parked);
      if (parked?.error) throw new Error(parked.error);
    }
    if (!res.ok) {
      throw new Error(await responseErrorMessage(res, `Failed to continue task: ${res.status}`));
    }
    const data = await res.json().catch(() => null) as {
      taskId?: string;
      sameTask?: boolean;
      executionSession?: TaskExecutionIdentity;
    } | null;
    const identity = data?.executionSession;
    if (!data || data.taskId !== taskId || data.sameTask === false || identity?.taskId !== taskId) {
      throw new Error("The agent did not confirm the same task and runner session for this follow-up.");
    }
    return identity;
  }

  async updateTaskSessionSettings(taskId: string, sessionSettings: ClientSessionSettings): Promise<void> {
    this.assertConnected();
    const res = await this.fetchWithTimeout(
      `${this.taskBaseUrl}/tasks/${taskId}/session-settings`,
      {
        method: "PATCH",
        headers: { ...this.authHeaders, "Content-Type": "application/json" },
        body: JSON.stringify({ sessionSettings }),
      },
      15_000,
    );
    if (!res.ok) throw new Error(`Failed to update session settings: ${res.status}`);
  }

  async completeTask(taskId: string): Promise<void> {
    this.assertConnected();
    const res = await fetch(`${this.taskBaseUrl}/tasks/${taskId}/complete`, {
      method: "POST",
      headers: this.authHeaders,
    });
    if (!res.ok) throw new Error(`Failed to complete task: ${res.status}`);
  }

  /** Task-proof package for a completed task (`GET /tasks/{id}/proof`).
   *  Returns null when no proof exists for the task (agent answers 404
   *  `{ok:false}`) — the caller falls back to the task-level video fields.
   *  Rides taskBaseUrl like every other task method so a machine-role split
   *  asks the runner box that actually produced the proof. */
  async getTaskProof(taskId: string): Promise<TaskProof | null> {
    this.assertConnected();
    const res = await fetch(`${this.taskBaseUrl}/tasks/${taskId}/proof`, {
      headers: this.authHeaders,
    });
    if (res.status === 404) return null;
    if (!res.ok) throw new Error(`Failed to get task proof: ${res.status}`);
    const data = await res.json().catch(() => null);
    if (!data?.ok || !data?.proof) return null;
    return data.proof as TaskProof;
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
    args: { runner: string; model?: string; mode?: string; input: string; contextWords?: number; allowLocalFallback?: boolean; projectDir?: string; mcpServers?: string[]; includeYaverMcp?: boolean },
  ): Promise<{ taskId: string; runnerId: string; parentTaskId: string; contextWordsUsed: number }> {
    this.assertConnected();
    const res = await this.fetchWithTimeout(
      `${this.taskBaseUrl}/tasks/${taskId}/fork`,
      {
        method: "POST",
        headers: { ...this.authHeaders, "Content-Type": "application/json" },
        body: JSON.stringify({
          runner: args.runner,
          model: args.model ?? "",
          mode: args.mode ?? "",
          input: args.input,
          contextWords: args.contextWords,
          allowLocalFallback: args.allowLocalFallback ?? false,
          projectDir: args.projectDir ?? "",
          // Omitted fork scope inherits the parent. Explicit []/false means
          // the user selected No MCP.
          ...(args.mcpServers !== undefined ? { mcpServers: args.mcpServers } : {}),
          ...(args.includeYaverMcp !== undefined ? { includeYaverMcp: args.includeYaverMcp } : {}),
        }),
      },
      30_000,
    ).catch((err: any) => {
      if (err?.name === "AbortError") {
        throw new Error("Timed out forking the task after 30s — the machine may be busy.");
      }
      throw err;
    });
    if (!res.ok) {
      const cloudRequired = await decodeCloudWorkspaceRequiredError(res);
      if (cloudRequired) throw cloudRequired;
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
    const res = await fetch(`${this.taskBaseUrl}/tasks/${taskId}`, {
      method: "DELETE",
      headers: this.authHeaders,
    });
    if (!res.ok) throw new Error(`Failed to delete task: ${res.status}`);
  }

  async stopAllTasks(): Promise<number> {
    this.assertConnected();
    const res = await fetch(`${this.taskBaseUrl}/tasks/stop-all`, {
      method: "POST",
      headers: this.authHeaders,
    });
    if (!res.ok) throw new Error(`Failed to stop all: ${res.status}`);
    const data = await res.json();
    return data.stopped || 0;
  }

  async deleteAllTasks(): Promise<number> {
    this.assertConnected();
    const res = await fetch(`${this.taskBaseUrl}/tasks`, {
      method: "DELETE",
      headers: this.authHeaders,
    });
    if (!res.ok) throw new Error(`Failed to delete all: ${res.status}`);
    const data = await res.json();
    return data.deleted || 0;
  }

  // ── External MCP servers (desktop/agent/mcp_external.go) ──────────
  // Register your own private MCPs or anyone's public ones; the agent merges
  // their tools and forwards "<name>__<tool>" calls.

  async listMcpServers(): Promise<McpServer[]> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/mcp/servers`, { headers: this.authHeaders });
    if (!res.ok) throw new Error(`Failed to list MCP servers: ${res.status}`);
    const data = await res.json();
    return (data.servers ?? []) as McpServer[];
  }

  async saveMcpServer(s: McpServerInput): Promise<void> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/mcp/servers`, {
      method: "POST",
      headers: { ...this.authHeaders, "Content-Type": "application/json" },
      body: JSON.stringify(s),
    });
    if (!res.ok) throw new Error(`Failed to save MCP server: ${res.status}`);
  }

  async testMcpServer(s: McpServerInput): Promise<{ ok: boolean; toolCount?: number; error?: string }> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/mcp/servers?test=1`, {
      method: "POST",
      headers: { ...this.authHeaders, "Content-Type": "application/json" },
      body: JSON.stringify(s),
    });
    if (!res.ok) throw new Error(`Test failed: ${res.status}`);
    return res.json();
  }

  async deleteMcpServer(name: string): Promise<void> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/mcp/servers?name=${encodeURIComponent(name)}`, {
      method: "DELETE",
      headers: this.authHeaders,
    });
    if (!res.ok) throw new Error(`Failed to delete MCP server: ${res.status}`);
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

  /** Live deploy board from the box's autorun store (GET /autoruns/deploy-status).
   *  Powers the web DeployStatusView, mirroring the mobile screen. Returns an
   *  empty board on an older agent that lacks the endpoint (never throws hard). */
  async getDeployStatus(): Promise<{
    targets: {
      target: string;
      deploying: boolean;
      holder?: string;
      build?: string;
      stage?: string;
      startedAt?: number;
      elapsedSecs?: number;
      uploadsToday: number;
      quota: number;
    }[];
    at: number;
  }> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/autoruns/deploy-status`, {
      headers: this.authHeaders,
    });
    if (!res.ok) throw new Error(`deploy-status: ${res.status}`);
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

  /**
   * getOpsVerbs lists every ops verb the connected agent registered, each
   * with its JSON-Schema payload. This is the same `/ops/verbs` feed the
   * `ops_verbs` MCP tool exposes to agents — here it drives the generic
   * schema-driven ToolPanelView so a verb gets a native form without a
   * hand-written panel. `payload` is a JSON Schema (may be undefined for
   * param-less verbs).
   */
  async getOpsVerbs(): Promise<
    Array<{ name: string; description?: string; streaming?: boolean; payload?: any }>
  > {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/ops/verbs`, { headers: this.authHeaders });
    if (!res.ok) throw new Error(`Failed to list ops verbs: ${res.status}`);
    const data = await res.json().catch(() => ({}));
    return Array.isArray(data?.verbs) ? data.verbs : [];
  }

  async getAgentUpdateStatus(): Promise<AgentUpdateStatus> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/agent/update`, {
      headers: this.authHeaders,
    });
    if (!res.ok) throw new Error(`Failed to get update status: ${res.status}`);
    return res.json();
  }

  /**
   * getVisionStatus — GET /vision/status on the connected agent: which vision
   * LLM providers are configured (key presence only, never key material),
   * whether free on-device OCR is available, and the active provider/model
   * override. Backs the VisionSettingsCard.
   */
  async getVisionStatus(): Promise<{
    ok?: boolean;
    providers_configured?: string[];
    active_provider?: string;
    model_override?: string;
    free_ocr?: boolean;
    free_ocr_note?: string;
    mac_ui_snapshot_available?: boolean;
    set_hint?: string;
  }> {
    this.assertConnected();
    const res = await this.agentFetch("/vision/status", { headers: this.authHeaders });
    if (!res.ok) throw new Error(`Failed to get vision status: ${res.status}`);
    return res.json().catch(() => ({}));
  }

  /**
   * setVisionKey — PUT /vision/key on the connected agent: store (or clear) a
   * vision-LLM provider key in ~/.yaver/config.json vision_keys, the shared
   * seam read by the MCP vision tools, `yaver vision`, QA and ghost vision.
   */
  async setVisionKey(
    provider: string,
    key: string,
    clear = false,
  ): Promise<{ ok?: boolean; provider?: string; stored?: boolean; note?: string }> {
    this.assertConnected();
    const res = await this.agentFetch("/vision/key", {
      method: "PUT",
      headers: { ...this.authHeaders, "Content-Type": "application/json" },
      body: JSON.stringify({ provider, key, clear }),
    });
    const data = await res.json().catch(() => ({}));
    if (!res.ok) throw new Error(data?.error || `Failed to set vision key: ${res.status}`);
    return data;
  }

  /**
   * getOpenCodeConfig — GET /runner/opencode/config on the connected agent
   * (or /peer/<target>/runner/opencode/config for another owned machine):
   * the merged opencode config summary (default model, build/plan models,
   * providers, available models). Backs the OpenCodeModelCard.
   */
  async getOpenCodeConfig(target?: string): Promise<OpenCodeConfigSummary | null> {
    if (!this.isConnected) return null;
    try {
      const res = await this.agentFetch(this.opencodeConfigPath(target), { headers: this.authHeaders });
      if (!res.ok) return null;
      const data = await res.json();
      return data?.config || null;
    } catch {
      return null;
    }
  }

  async getOpenCodeCatalog(provider?: string, target?: string): Promise<OpenCodeProviderSummary[]> {
    if (!this.isConnected) return [];
    try {
      const suffix = provider ? `?provider=${encodeURIComponent(provider)}` : "";
      const base = target
        ? `${this.baseUrl}/peer/${encodeURIComponent(target)}/runner/opencode/catalog${suffix}`
        : `${this.baseUrl}/runner/opencode/catalog${suffix}`;
      const res = await fetch(base, { headers: this.authHeaders });
      if (!res.ok) return [];
      const data = await res.json().catch(() => ({}));
      return Array.isArray(data?.providers) ? data.providers : [];
    } catch {
      return [];
    }
  }

  /**
   * saveOpenCodeConfig — POST /runner/opencode/config: patch the opencode
   * config (default/build/plan model, smallModel, defaultAgent, providers)
   * on the connected agent or a peer device. The web/mobile "change the
   * coding model" seam — e.g. point a machine at deepseek-v4-flash instead
   * of its current default.
   */
  async saveOpenCodeConfig(
    patch: {
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
        models?: Record<string, unknown>;
        delete?: boolean;
      }>;
    },
    target?: string,
  ): Promise<{ ok: boolean; config?: OpenCodeConfigSummary; error?: string }> {
    if (!this.isConnected) return { ok: false, error: "not connected" };
    try {
      const res = await this.agentFetch(this.opencodeConfigPath(target), {
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

  /** /peer/<target>/... proxy path; self-targeting drops the prefix
   *  (mirrors mobile quicClient.peerEndpoint — the agent rejects
   *  self-targeted peer calls with errProxyLocal). */
  private opencodeConfigPath(target?: string): string {
    const t = (target || "").trim();
    if (!t) return "/runner/opencode/config";
    return `/peer/${encodeURIComponent(t)}/runner/opencode/config`;
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
    const res = await fetch(`${this.taskBaseUrl}/agent/runners`, {
      headers: this.authHeaders,
    });
    if (!res.ok) throw new Error(`Failed to get runners: ${res.status}`);
    const data = await res.json();
    return data.runners || [];
  }

  // startRunnerBrowserAuth was removed on purpose: it returned `data.session`
  // blind, which is undefined when the agent answers `action:"noop"` (already
  // signed in), and every consumer crashed on `session.openUrl`. Use
  // runnerBrowserAuthStart below and handle the noop/reuse envelope.

  async getRunnerBrowserAuthStatus(sessionId: string, target?: string): Promise<RunnerBrowserAuthSession> {
    this.assertConnected();
    const base = target
      ? `${this.baseUrl}/peer/${encodeURIComponent(target)}/runner-auth/browser/status`
      : `${this.taskBaseUrl}/runner-auth/browser/status`;
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
      : `${this.taskBaseUrl}/runner-auth/browser/cancel`;
    const url = `${base}?id=${encodeURIComponent(sessionId)}`;
    await fetch(url, { method: "POST", headers: this.authHeaders }).catch(() => {});
  }

  /**
   * Forward a user-pasted authentication code to the running CLI's
   * stdin. Used by the Claude plan-OAuth flow where the user signs
   * in on claude.ai, copies the one-shot code/token, and pastes it
   * back here. The agent fire-and-forgets the code into the spawned
   * `claude auth login --claudeai` process; nothing is persisted.
   *
   * Privacy: the code is only ever held in memory on the host (the
   * machine running the spawned CLI), never on Convex, never on the
   * bus, never in any log. The owner-authenticated agent route enforces this.
   */
  async submitRunnerBrowserAuthCode(sessionId: string, code: string, target?: string): Promise<RunnerBrowserAuthSession> {
    this.assertConnected();
    const base = target
      ? `${this.baseUrl}/peer/${encodeURIComponent(target)}/runner-auth/browser/submit-code`
      : `${this.taskBaseUrl}/runner-auth/browser/submit-code`;
    const url = `${base}?id=${encodeURIComponent(sessionId)}`;
    const res = await fetch(url, {
      method: "POST",
      headers: { ...this.authHeaders, "Content-Type": "application/json" },
      body: JSON.stringify({ session_id: sessionId, code }),
    });
    const data = await res.json().catch(() => ({}));
    if (!res.ok) {
      throw new Error(data?.error || `submitRunnerBrowserAuthCode ${res.status}`);
    }
    return data.session as RunnerBrowserAuthSession;
  }

  async submitRunnerBrowserAuthCallback(sessionId: string, callbackUrl: string, target?: string): Promise<RunnerBrowserAuthSession> {
    this.assertConnected();
    const base = target
      ? `${this.baseUrl}/peer/${encodeURIComponent(target)}/runner-auth/browser/submit-callback`
      : `${this.taskBaseUrl}/runner-auth/browser/submit-callback`;
    const url = `${base}?id=${encodeURIComponent(sessionId)}`;
    const res = await fetch(url, {
      method: "POST",
      headers: { ...this.authHeaders, "Content-Type": "application/json" },
      body: JSON.stringify({ callback_url: callbackUrl }),
    });
    const data = await res.json().catch(() => ({}));
    if (!res.ok) {
      throw new Error(data?.error || `submitRunnerBrowserAuthCallback ${res.status}`);
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
  async testRunner(runner: string, opts?: { prompt?: string; model?: string; timeoutMs?: number }): Promise<RunnerTestResult> {
    this.assertConnected();
    const timeoutMs = Math.max(1_000, Math.min(opts?.timeoutMs || 25_000, 125_000));
    const res = await this.fetchWithTimeout(
      `${this.taskBaseUrl}/agent/runners/test`,
      {
        method: "POST",
        headers: this.authHeaders,
        body: JSON.stringify({ runner, prompt: opts?.prompt, model: opts?.model, timeoutMs }),
      },
      timeoutMs + 2_500,
    );
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
      : `${this.taskBaseUrl}/runner-auth/status`;
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
      : `${this.taskBaseUrl}/runner-auth/set`;
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

  /**
   * Start (or decline to start) a runner sign-in on the agent.
   *
   * `trigger` and `confirm` are the caller's honesty declaration, and the agent
   * enforces them. Starting a sign-in is DESTRUCTIVE — it reaps any live
   * session for that runner, burns a PKCE flow, and for claude can replace a
   * working credential. On 2026-07-27 the user was shown sign-in dialogs
   * repeatedly for runners that were fine.
   *
   *   trigger: "auto"      — a machine decided (a gate, a chip, a modal that
   *                          started a session merely because it opened). The
   *                          agent will REFUSE on a healthy runner.
   *   trigger: "explicit"  — the user tapped Sign in. Answered, not obeyed: on
   *                          a healthy runner the agent returns
   *                          action:"noop" + reauthable:true.
   *   confirm: true        — the user was told it already looks signed in and
   *                          chose to sign in anyway (switching accounts). The
   *                          only path that may reap.
   *
   * `action:"reuse"` returns the session already in flight, so a phone and a
   * browser asking at the same moment converge on ONE flow.
   */
  async runnerBrowserAuthStart(
    params: {
      runner: "claude" | "codex";
      waitSeconds?: number;
      trigger?: "auto" | "explicit" | "confirmed";
      confirm?: boolean;
    },
    target?: string,
  ): Promise<{
    ok: boolean;
    session?: RunnerBrowserAuthSession;
    action?: "start" | "reuse" | "noop";
    reason?: string;
    reauthable?: boolean;
    error?: string;
  }> {
    this.assertConnected();
    const base = target
      ? `${this.baseUrl}/peer/${encodeURIComponent(target)}/runner-auth/browser/start`
      : `${this.taskBaseUrl}/runner-auth/browser/start`;
    const res = await fetch(base, {
      method: "POST",
      headers: { ...this.authHeaders, "Content-Type": "application/json" },
      body: JSON.stringify({
        runner: params.runner,
        wait_seconds: params.waitSeconds ?? 5,
        trigger: params.trigger ?? "explicit",
        confirm: params.confirm ?? false,
      }),
    });
    const data = await res.json().catch(() => ({}));
    if (!res.ok) return { ok: false, error: data?.error || `HTTP ${res.status}` };
    return {
      ok: true,
      session: data?.session,
      action: data?.action,
      reason: data?.reason,
      reauthable: data?.reauthable,
    };
  }

  async runnerBrowserAuthStatus(
    id: string,
    target?: string,
  ): Promise<{ ok: boolean; session?: RunnerBrowserAuthSession; error?: string }> {
    this.assertConnected();
    const base = target
      ? `${this.baseUrl}/peer/${encodeURIComponent(target)}/runner-auth/browser/status?id=${encodeURIComponent(id)}`
      : `${this.taskBaseUrl}/runner-auth/browser/status?id=${encodeURIComponent(id)}`;
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
      : `${this.taskBaseUrl}/runner-auth/browser/cancel?id=${encodeURIComponent(id)}`;
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
      body: JSON.stringify({ runnerId }),
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
    template?: "full" | "ship" | "ask";
    maxParallel?: number;
    preferredDevice?: string;
    allowedDevices?: string[];
    allowedRunners?: string[];
    // Cost-aware duo/trio routing: 0/undefined = default (single-model),
    // 2 = duo (claude-code + glm), 3 = trio (claude-code + codex + glm).
    hybridDegree?: number;
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
        hybridDegree: params.hybridDegree ?? 0,
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

  /** Fetch a single agent-graph run's current state (nodes + statuses +
   *  summaries). Used to poll a deep ask graph's investigate → answer →
   *  verify progress in the web console. Returns null if not found. */
  async getAgentGraph(id: string): Promise<AgentGraphRun | null> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/agent/graphs/${encodeURIComponent(id)}`, {
      headers: this.authHeaders,
    });
    if (!res.ok) return null;
    const data = await res.json().catch(() => ({}));
    return (data?.run as AgentGraphRun) ?? null;
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
    onLine: (line: string, offset?: number) => void,
    onEvent?: (event: { type: string; [k: string]: unknown }) => void,
    opts?: {
      /**
       * Byte offset into the task's transcript this caller already holds.
       * The agent replays only the remainder (`?since=`), so a reattach after
       * a dropped stream neither duplicates the scrollback nor loses what
       * arrived while we were away. Omit on a first subscribe.
       */
      since?: number;
  /**
   * Byte offset into the task's RAW stdout tail (ANSI + TUI, ungroomed)
   * this caller already rendered (`?rawSince=`). The agent answers with a
   * `raw_replay` frame (full snapshot when 0/absent) followed by live
   * `raw` frames, so the opencode terminal view reattaches without
   * re-rendering bytes it already drew. Omit for byte-for-byte old
   * behaviour (no raw frames at all on this stream).
   */
  rawSince?: number;
  /**
   * Receives RAW runner stdout (ANSI + TUI, ungroomed — the opencode
   * console lane) as it arrives: `{type:"raw_replay", text}` for the
   * snapshot, then `{type:"raw", text}` per chunk. Distinct from onLine
   * (the groomed transcript). Without a consumer the transport still
   * accepts rawSince but nothing surfaces the raw bytes — the web-chat
   * parity gap that mobile's LiveConsoleSection fills (audit
   * docs/audits/webui-chat-vibing-gui-2026-08-12.md §2). When absent,
   * raw frames are still passed to onEvent for callers that want the
   * terminal frame but render raw bytes themselves.
   */
  onRaw?: (event: { type: "raw" | "raw_replay"; text?: string; offset?: number }) => void;
  /**
       * How the stream ENDED. This used to be a bare `catch {}` commented
       * "Silent best-effort stream; callers usually poll task status too" —
       * but the poll goes over the SAME dead transport, so a relay bounce
       * mid-render froze the vibing transcript with nothing to show for it.
       * Classify with taskStreamRecovery.classifyStreamEnd.
       */
      onEnd?: (info: { sawDone: boolean; cancelled: boolean; error?: string }) => void;
    },
  ): () => void {
    const controller = new AbortController();
    const since = Number(opts?.since || 0);
    const rawSince = Number(opts?.rawSince || 0);
    const qs: string[] = [];
    if (since > 0) qs.push(`since=${encodeURIComponent(String(Math.floor(since)))}`);
    if (rawSince > 0) qs.push(`rawSince=${encodeURIComponent(String(Math.floor(rawSince)))}`);
    const url = qs.length > 0
      ? `${this.taskBaseUrl}/tasks/${taskId}/output?${qs.join("&")}`
      : `${this.taskBaseUrl}/tasks/${taskId}/output`;
    let sawDone = false;
    let cancelled = false;
    let endReported = false;
    // Exactly-once terminal report — a caller that reattaches on each call
    // would otherwise open duplicate streams for one dead connection.
    const reportStreamEnd = (error?: string) => {
      if (endReported) return;
      endReported = true;
      try {
        opts?.onEnd?.({ sawDone, cancelled, error });
      } catch {
        // a broken listener must not take the transport down
      }
    };
    (async () => {
      try {
        const res = await fetch(url, {
          method: "GET",
          headers: { ...this.authHeaders, Accept: "text/event-stream" },
          signal: controller.signal,
        });
        const reader = res.body?.getReader();
        if (!reader) {
          reportStreamEnd(`stream did not open (HTTP ${res.status})`);
          return;
        }
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
                // `offset` is the agent's AUTHORITATIVE byte cursor. Pass it
                // on: the caller's own `String(chunk).length` is UTF-16 code
                // units, which only equals the byte length for ASCII, and
                // `?since=` is sliced in bytes. See
                // desktop/agent/stream_cursor.go.
                onLine(String(event.text), typeof event.offset === "number" ? event.offset : undefined);
              } else if ((event?.type === "raw" || event?.type === "raw_replay") && opts?.onRaw) {
                // The RAW runner stdout lane (opencode console, ANSI + TUI).
                // Deliberately routed to a dedicated callback, never to onLine:
                // onLine feeds the groomed transcript bubble, and raw bytes
                // would double-render with different text (groomed vs not).
                // raw_replay is the reattach snapshot; raw is live chunks.
                opts.onRaw({
                  type: event.type,
                  text: typeof event.text === "string" ? event.text : undefined,
                  offset: typeof event.offset === "number" ? event.offset : undefined,
                });
              } else {
                // Record the terminal frame even when the caller passed no
                // onEvent — it is what separates "the task finished" from
                // "the stream died", and getting that wrong reattaches forever.
                if (event?.type === "done") sawDone = true;
                if (onEvent) onEvent(event);
              }
            } catch {
              // Ignore malformed frames.
            }
          }
        }
        // A clean EOF on a stream that should never close is what a severed
        // relay tunnel looks like. Report it; `sawDone` is the only thing
        // that makes it benign.
        reportStreamEnd(sawDone ? undefined : "stream closed before the task finished");
      } catch (error) {
        reportStreamEnd(
          cancelled
            ? undefined
            : error instanceof Error
              ? error.message
              : "stream connection failed",
        );
      }
    })();
    return () => {
      cancelled = true;
      controller.abort();
    };
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
      const res = await fetch(`${this.taskBaseUrl}/tasks/${taskId}/answer`, {
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
    screenshot?: string; // F3 handoff: base64 PNG region
    step?: string;       // F3 handoff step type
    createdAtMs: number;
    timeoutSec: number;
  } | null> {
    try {
      const res = await fetch(`${this.taskBaseUrl}/tasks/${taskId}/question`, {
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

  // ── Netcapture (wire-observe & deep-analysis) ──────────────────────────

  /** POST /netcapture/start — begin a network (tcpdump) or serial capture. */
  async netcaptureStart(opts: {
    kind?: "net" | "serial";
    iface?: string;
    filter?: string;
    device?: string;
    baud?: number;
    decoder?: string;
    capturePayload?: boolean;
  }): Promise<{ ok: boolean; session: string; stream: string; warning?: string }> {
    const res = await fetch(`${this.baseUrl}/netcapture/start`, {
      method: "POST",
      headers: { ...this.authHeaders, "Content-Type": "application/json" },
      body: JSON.stringify(opts),
    });
    return res.json();
  }

  /** POST /netcapture/stop — stop a session, returns the final analysis. */
  async netcaptureStop(session: string): Promise<any> {
    const res = await fetch(`${this.baseUrl}/netcapture/stop`, {
      method: "POST",
      headers: { ...this.authHeaders, "Content-Type": "application/json" },
      body: JSON.stringify({ session }),
    });
    return res.json();
  }

  /** GET /netcapture/analysis?session=… — full structured deep-analysis. */
  async netcaptureAnalysis(session: string): Promise<any> {
    const res = await fetch(
      `${this.baseUrl}/netcapture/analysis?session=${encodeURIComponent(session)}`,
      { headers: this.authHeaders },
    );
    return res.json();
  }

  /** GET /netcapture/status — list active capture sessions. */
  async netcaptureStatus(): Promise<any> {
    const res = await fetch(`${this.baseUrl}/netcapture/status`, {
      headers: this.authHeaders,
    });
    return res.json();
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
    return agentHttpBase(this.host, this.port);
  }

  private activeRelayPassword: string | null = null;

  private get authHeaders(): Record<string, string> {
    // X-Yaver-Surface is advisory session provenance, never authorization.
    // Agent + relay CORS explicitly allow it so web participates in the same
    // initial/last-surface contract as native clients.
    const h: Record<string, string> = {
      Authorization: `Bearer ${this.token}`,
      "X-Yaver-Surface": "web",
    };
    // Carry the relay password whenever the relay is the active transport OR
    // a machine-role route may send this request through a relay despite a
    // direct/tunnel primary transport (Tailscale case). The agent's CORS
    // allow-list includes X-Relay-Password (httpserver.go), and a box that
    // receives it directly simply ignores it.
    const rolesActive = !!(this.taskRouteDeviceId || this.renderRouteDeviceId);
    const pw = this._activeRelayUrl ? this.activeRelayPassword : rolesActive ? this.routingRelayPassword : null;
    if (pw) {
      h["X-Relay-Password"] = pw;
    }
    return h;
  }

  /** Per-tenant relay plan + usage — the settings "Plan & usage" card.
   *  Answered by the relay's /my/bandwidth: the caller's Convex-verified plan
   *  plus usage rows scoped to the caller's OWN devices (never another
   *  tenant's). Returns null when this session isn't relay-connected; the
   *  password stays private to this class. */
  async fetchMyRelayUsage(): Promise<{
    plan: string;
    isPaid: boolean;
    unmetered: boolean;
    devices: Array<{ deviceId: string; usedMb: number; limitMb: number; isPaid: boolean; unmetered?: boolean }>;
  } | null> {
    if (!this._activeRelayUrl || !this.activeRelayPassword) return null;
    const base = this._activeRelayUrl.replace(/\/+$/, "");
    const res = await fetch(`${base}/my/bandwidth`, {
      headers: { "X-Relay-Password": this.activeRelayPassword },
      cache: "no-store",
    });
    if (!res.ok) throw new Error(`usage fetch failed: HTTP ${res.status}`);
    return res.json();
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

  /**
   * Build an authed MJPEG stream URL for the Remote Desktop screen feed,
   * suitable for an <img src>. A plain <img> can't carry the bearer/relay
   * headers, so we mint a path-scoped browser-session token and append it
   * (plus the relay password) as query params — both of which the relay and
   * agent accept (the agent's auth middleware promotes ?browser_session=,
   * the relay validates ?__rp=). `path` defaults to the live MJPEG stream;
   * pass "/rd/frame.jpg" for a single still.
   */
  async remoteDesktopStreamUrl(path: "/rd/stream" | "/rd/frame.jpg" = "/rd/stream"): Promise<string> {
    const token = await this.issueBrowserSession("/rd/");
    const base = `${this.baseUrl}${path}`;
    let url = `${base}?browser_session=${encodeURIComponent(token)}`;
    if (this._activeRelayUrl && this.activeRelayPassword) {
      url += `&__rp=${encodeURIComponent(this.activeRelayPassword)}`;
    }
    return url;
  }

  /**
   * Authed MJPEG/still URL for the home capture-card feed (own non-protected
   * source; Yaver streams whatever the card provides, as-is). Same path-scoped
   * browser-session + relay-password scheme as remoteDesktopStreamUrl. Pass
   * "/capture/frame.jpg" for a single still.
   */
  async captureStreamUrl(path: "/capture/stream" | "/capture/frame.jpg" = "/capture/stream"): Promise<string> {
    const token = await this.issueBrowserSession("/capture/");
    let url = `${this.baseUrl}${path}?browser_session=${encodeURIComponent(token)}`;
    if (this._activeRelayUrl && this.activeRelayPassword) {
      url += `&__rp=${encodeURIComponent(this.activeRelayPassword)}`;
    }
    return url;
  }

  /** Authed SSE URL for the Apple TV now-playing delta stream, for an
   *  EventSource (which can't carry headers). Path-scoped browser-session +
   *  relay-password, same scheme as captureStreamUrl. */
  async nowPlayingStreamUrl(): Promise<string> {
    const token = await this.issueBrowserSession("/appletv/");
    let url = `${this.baseUrl}/appletv/nowplaying/stream?browser_session=${encodeURIComponent(token)}`;
    if (this._activeRelayUrl && this.activeRelayPassword) {
      url += `&__rp=${encodeURIComponent(this.activeRelayPassword)}`;
    }
    return url;
  }

  private async issueBrowserSession(pathPrefix: string, base?: string): Promise<string> {
    this.assertConnected();
    // Browser-session tokens are minted and validated by the SAME box — a
    // role-routed caller must mint on the box it will talk to.
    const res = await fetch(`${base ?? this.baseUrl}/auth/browser-session`, {
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
      const base = agentHttpBase(this.host, this.port);
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
   *  Only the owner of the device per Convex /devices/list can reset.
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
        // 401/403 — bearer or ownership issue. Don't keep retrying, the
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
   *  via custom tunnel or LAN when the relay was degraded,
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
      push(`${agentHttpBase(opts.host, port)}/auth/pair/owner-claim`, `direct ${opts.host}`);
    }
    for (const ip of opts.lanIps || []) {
      if (!ip) continue;
      push(`${agentHttpBase(ip, port)}/auth/pair/owner-claim`, `lan ${ip}`);
    }
    // Tunnel + public endpoints — filtered through the ONE shared known-dead
    // predicate (lib/endpoints.ts) so owner-claim doesn't dial <uuid>.yaver.io
    // (no DNS) or *.dev.yaver.io (no cert) endpoints.
    if (opts.tunnelUrl && isUsablePublicEndpoint(opts.tunnelUrl)) {
      push(`${opts.tunnelUrl.replace(/\/+$/, "")}/auth/pair/owner-claim`, `tunnel ${opts.tunnelUrl}`);
    }
    for (const endpoint of opts.publicEndpoints || []) {
      if (!endpoint || !isUsablePublicEndpoint(endpoint)) continue;
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

  private async tryRelayFallback(): Promise<boolean> {
    if (!this.deviceId || this.relayServers.length === 0) return false;
    for (const relay of this.relayServers) {
      const relayDeviceUrl = `${relay.httpUrl}/d/${this.deviceId}`;
      const headers: Record<string, string> = { ...this.authHeaders };
      if (relay.password) headers["X-Relay-Password"] = relay.password;
      const diag = await this.probeHealth(relayDeviceUrl, headers, 8000, "relay", relay.id);
      if (diag.ok) {
        this._activeRelayUrl = relay.httpUrl;
        this.activeRelayPassword = relay.password || null;
        this._activeTunnelUrl = null;
        this.setConnectionState("connected");
        return true;
      }
    }
    return false;
  }

  private async fetchAgentPath(path: string, init: RequestInit = {}): Promise<Response> {
    const request = () => fetch(`${this.baseUrl}${path}`, init);
    try {
      return await request();
    } catch (err) {
      if (!this._activeRelayUrl && this.relayServers.length > 0) {
        const switched = await this.tryRelayFallback();
        if (switched) return request();
      }
      throw err;
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
      // ONE shared predicate (lib/endpoints.ts): skips <id>.dev.yaver.io
      // (wildcard cert not provisioned — TLS handshake failure) AND stale
      // <uuid>.yaver.io rows (no wildcard *.yaver.io DNS — NXDOMAIN spam).
      // This used to be a private regex that drifted from DevicesView's copy.
      .filter(isUsablePublicEndpoint)) {
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

    const directUrl = agentHttpBase(opts.host, opts.port);
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
      if (!connected && this.host && this.port) {
        const directUrl = agentHttpBase(this.host, this.port);
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
        // Pick the most informative error, most-terminal first:
        // 1. device_mismatch (audit R3) — the box belongs to a DIFFERENT
        //    account; no retry, repair, or auth on this side can change the
        //    verdict, so it must never render as generic unreachability.
        const denyMsg = diagnostics
          .map((d) => explainRelayDeny(d.error))
          .find((m): m is string => !!m);
        if (denyMsg) {
          throw new Error(denyMsg);
        }
        // 2. "auth expired" over raw transport errors so the UI can guide
        //    the user to `yaver auth` on the box.
        const authExpired = diagnostics.some((d) => d.authExpired);
        if (authExpired) {
          throw new Error("Agent reached, but its Convex session is expired — run `yaver auth` on the remote device");
        }
        // 3. Relay limit verdicts (audit R13/R14) carry their compact named
        //    explanation — reset behavior + unmetered alternatives — instead
        //    of the relay's raw string.
        const relayLimit = diagnostics.find((d) => d.status === 429 || d.status === 413 || d.status === 503);
        if (relayLimit?.error) {
          const card = classifyRelayLimit(relayLimit.error);
          throw new Error(card ? `${card.title} — ${card.detail}` : relayLimit.error);
        }
        throw new Error("Could not reach agent (direct, tunnel, or relay)");
      }

      this.reconnectAttempt = 0;
      this.reconnectRepairAttempted = false;
      this._lastConnectError = null;
      this.setConnectionState("connected");
      this.startPolling();
    } catch (err) {
      this._lastConnectDiagnostics = diagnostics;
      this._lastConnectError = err instanceof Error ? err.message : String(err);
      this.setConnectionState("error");
      // Read this BEFORE scheduleReconnect(), which increments reconnectAttempt.
      // Reading it after meant the check below was never true, so the first
      // failure was swallowed: connect() resolved, callers believed they were
      // connected, and the next call surfaced assertConnected()'s internal
      // "not connected — call connect() first" instead of the real reason.
      const isFirstAttempt = this.reconnectAttempt === 0;
      this.scheduleReconnect();
      if (isFirstAttempt) throw err;
    }
  }

  private scheduleReconnect(): void {
    if (!this.host || !this.port || !this.token) return;

    // Policy lives in reconnectLadder.ts (pure, tested); this executes it.
    const plan = planReconnect({
      attempt: this.reconnectAttempt,
      maxAttempts: this.maxReconnectAttempt,
      lastCause: this._lastConnectError,
      repairAttemptedThisStreak: this.reconnectRepairAttempted,
    });

    if (plan.action === "stop-terminal" || plan.action === "give-up") {
      // The ladder used to stop SILENTLY here (audit gap T2). State the
      // verdict where consumers can read it; connection state is already
      // "error" from attemptConnect.
      this._lastConnectError = plan.message;
      console.warn("[AgentClient] reconnect stopped:", plan.message);
      return;
    }

    if (plan.repairRelay) {
      // Repair rung (mobile parity): refresh the per-user relay password
      // once per failure streak, so the next attempt runs with fresh creds.
      this.reconnectRepairAttempted = true;
      void this.repairRelayPassword().catch(() => {});
    }
    if (plan.refreshTopology && this.topologyRefreshHook) {
      // Topology rung (mobile parity): re-pull relay list + device
      // coordinates so a relay restart doesn't loop us on stale state.
      void this.topologyRefreshHook().catch(() => {});
    }

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
        const res = await fetch(`${this.taskBaseUrl}/tasks?limit=5`, {
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

  async sandboxQuickstart(mode: "host", buildImage = true): Promise<{ ok: boolean; message?: string; sandbox?: SandboxStatus; error?: string }> {
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

  async listProjects(refresh = false): Promise<RemoteProject[]> {
    this.assertConnected();
    const suffix = refresh ? "?refresh=1" : "";
    const res = await fetch(`${this.baseUrl}/projects${suffix}`, { headers: this.authHeaders });
    const data = await res.json().catch(() => ({}));
    if (!res.ok) throw new Error(data?.error || `Failed to load projects: HTTP ${res.status}`);
    return data.projects ?? [];
  }

  async setWorkDir(path: string): Promise<string> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/work-dir`, {
      method: "POST",
      headers: { ...this.authHeaders, "Content-Type": "application/json" },
      body: JSON.stringify({ path }),
    });
    const data = await res.json().catch(() => ({})) as { workDir?: string; error?: string };
    if (!res.ok) throw new Error(data.error || `Failed to select project: ${res.status}`);
    return data.workDir || path;
  }

  /** Project list from the RENDER machine when a runner/render split is
   *  active, else the connected box. The project PICKER must offer the
   *  render box's projects: Load Targets probes the render box, so a path
   *  from the AI/connected box (e.g. /root/Workspace/yaver.io on the Linux
   *  runner) is nonsense there — the probe dies with "workspace manifest:
   *  no workspace manifest at /root/Workspace/yaver.io" even though the
   *  connection is fine (2026-08-12: "Runtime target probe failed … this
   *  sucked too" — inventory from one machine, operation on another).
   *  Same /projects shape as listProjects, routed via devBaseUrl. */
  async listRenderProjects(): Promise<Array<{
    name: string;
    path: string;
    branch?: string;
    framework?: string;
    frameworks?: string[];
    stack?: string;
    surfaces?: string[];
    testSurfaces?: string[];
    backend?: string;
    services?: string[];
    hosting?: string[];
    role?: string;
    executionMode?: string;
    primarySurface?: string;
    gitRemote?: string;
    tags?: string[];
  }>> {
    this.assertConnected();
    const res = await fetch(`${this.devBaseUrl}/projects`, { headers: this.authHeaders });
    const data = await res.json().catch(() => ({}));
    if (!res.ok) throw new Error(data?.error || `Failed to load render projects: HTTP ${res.status}`);
    return data.projects ?? [];
  }

  async listWorkspaceRepos(source: "connected" | "render" = "connected"): Promise<Array<{
    name: string;
    path: string;
    branch?: string;
    remote?: string;
    lastCommit?: string;
    dirty?: boolean;
    stack?: {
      type?: string;
      frameworks?: string[];
      services?: string[];
      actions?: string[];
    };
  }>> {
    this.assertConnected();
    const base = source === "render" ? this.devBaseUrl : this.baseUrl;
    const res = await fetch(`${base}/repos/list`, { headers: this.authHeaders });
    if (!res.ok) return [];
    const data = await res.json().catch(() => []);
    return Array.isArray(data) ? data as Array<{
      name: string;
      path: string;
      branch?: string;
      remote?: string;
      lastCommit?: string;
      dirty?: boolean;
      stack?: {
        type?: string;
        frameworks?: string[];
        services?: string[];
        actions?: string[];
      };
    }> : [];
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
  async listProjectsByCapability(
    capability: "web" | "mobile" | "all",
    source: "connected" | "render" = "connected",
  ): Promise<Array<{
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
    const base = source === "render" ? this.devBaseUrl : this.baseUrl;
    const res = await fetch(`${base}${path}`, { headers: this.authHeaders });
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

  async getDogfoodSourceStatus(workDir?: string): Promise<DogfoodSourceStatus> {
    this.assertConnected();
    const query = workDir ? `?workDir=${encodeURIComponent(workDir)}` : "";
    const res = await fetch(`${this.devBaseUrl}/dogfood/source/status${query}`, { headers: this.authHeaders });
    const data = await res.json().catch(() => ({}));
    if (!res.ok) throw new Error(data?.error || `Dogfood source probe failed: HTTP ${res.status}`);
    return data as DogfoodSourceStatus;
  }

  async prepareDogfoodCheckout(workDir: string): Promise<{ ok: boolean; code?: string; error?: string; remedy?: string }> {
    this.assertConnected();
    const res = await fetch(`${this.devBaseUrl}/attach/prepare`, {
      method: "POST",
      headers: { ...this.authHeaders, "Content-Type": "application/json" },
      body: JSON.stringify({ workDir }),
    });
    const data = await res.json().catch(() => ({}));
    if (!res.ok) throw new Error(data?.error || data?.remedy || `Dogfood checkout preparation failed: HTTP ${res.status}`);
    return data;
  }

  async getProjectPreviewCapabilities(
    workDir: string,
    framework: string,
    surface: "web" | "desktop-gui",
    probe = true,
  ): Promise<ProjectPreviewCapabilities> {
    this.assertConnected();
    const params = new URLSearchParams({ workDir, framework, surface, probe: probe ? "true" : "false" });
    const res = await this.fetchWithTimeout(
      `${this.devBaseUrl}/project/preview-capabilities?${params.toString()}`,
      { headers: this.authHeaders },
      90_000,
    );
    const data = await res.json().catch(() => ({}));
    if (!res.ok) throw new Error(data?.error || `Dogfood method probe failed: HTTP ${res.status}`);
    return data as ProjectPreviewCapabilities;
  }

  async getRemoteRuntimeCapabilities(workDir: string, framework: string, refresh = false): Promise<RemoteRuntimeCapabilities> {
    this.assertConnected();
    // devBaseUrl is a RELATIVE same-origin path (`/d/<deviceId>`) when the
    // dashboard is served from yaver.io, so `new URL(...)` here threw
    // "cannot be parsed as a URL" — the runtime-target probe died before it
    // ever reached the box (2026-08-12, "Runtime target probe failed" with a
    // green connection check: inventory says OK, operation never happened).
    // fetch() resolves relative paths against the page origin — the same
    // pattern every sibling remote-runtime method below already uses.
    //
    // refresh=1 bypasses the agent's 2-minute caps cache. An EXPLICIT user
    // click on Load Targets must probe the box, not replay inventory —
    // otherwise a freshly-landed target (e.g. a just-installed tvOS runtime,
    // or this session's browser-framework specials) stays invisible for the
    // whole TTL (2026-08-13).
    const params = new URLSearchParams({ workDir, framework });
    if (refresh) params.set("refresh", "1");
    const res = await this.fetchWithTimeout(
      `${this.devBaseUrl}/remote-runtime/capabilities?${params.toString()}`,
      { headers: this.authHeaders },
      90_000,
    );
    if (!res.ok) throw new Error(await responseErrorMessage(res, `Failed to load remote runtime capabilities: HTTP ${res.status}`));
    return res.json();
  }

  async prepareRemoteRuntimeBrowserLane(workDir: string, framework: string): Promise<void> {
    const ready = (status: Awaited<ReturnType<AgentClient["getDevServerStatus"]>>): boolean => {
      if (!status?.running || status.error) return false;
      if (status.workDir && workDir && status.workDir !== workDir) return false;
      const fw = framework.trim().toLowerCase();
      return fw === "expo" || fw === "react-native"
        ? Number(status.webPort || 0) > 0
        : Number(status.port || 0) > 0;
    };
    let status = await this.getDevServerStatus();
    if (!ready(status)) {
      await this.startDevServer({ framework, workDir, platform: "web" });
    }
    const deadline = Date.now() + 150_000;
    let siblingStartAttempted = false;
    while (Date.now() < deadline) {
      status = await this.getDevServerStatus();
      if (ready(status)) return;
      if ((framework.trim().toLowerCase() === "expo" || framework.trim().toLowerCase() === "react-native") && status?.running && !siblingStartAttempted) {
        siblingStartAttempted = true;
        await this.startWebPreview();
      }
      if (status?.error && !status.serving) {
        throw new Error(`Browser runtime could not start: ${status.error}`);
      }
      await new Promise((resolve) => window.setTimeout(resolve, 600));
    }
    throw new Error("Browser runtime did not expose a usable web port within 150 seconds.");
  }

  async startRemoteRuntimeSession(workDir: string, framework: string, targetId: string, transportMode?: string): Promise<RemoteRuntimeSession> {
    this.assertConnected();
    if (targetId === "browser-window") {
      await this.prepareRemoteRuntimeBrowserLane(workDir, framework);
    }
    let clientId = "web-anonymous";
    if (typeof window !== "undefined") {
      const generated = `web-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 10)}`;
      try {
        const key = "yaver.remoteRuntime.clientId";
        clientId = window.localStorage.getItem(key) || generated;
        window.localStorage.setItem(key, clientId);
      } catch {
        clientId = generated;
      }
    }
    const res = await fetch(`${this.devBaseUrl}/remote-runtime/sessions`, {
      method: "POST",
      headers: { ...this.authHeaders, "Content-Type": "application/json" },
      body: JSON.stringify({ workDir, framework, targetId, transportMode, clientId, surface: "web" }),
    });
    const data = await res.json().catch(() => ({}));
    if (!res.ok) throw new Error(data?.error || `Failed to start remote runtime session: HTTP ${res.status}`);
    if (targetId === "browser-window" && ["waiting-for-dev-server", "attach-failed", "navigate-failed", "failed"].includes(String(data?.status))) {
      throw new Error(data?.note || `Browser runtime session failed with status ${data?.status}.`);
    }
    return data as RemoteRuntimeSession;
  }

  async sendRemoteRuntimeCommand(
    sessionId: string,
    // "shake" injects a hardware shake into the remote sim so the guest app's own
    // feedback SDK fires — the web "Shake" button path (no phone needed).
    // "run-guest" builds+launches the RN app into the booted sim.
    command: "launch-feedback" | "shake" | "run-guest" | "boot",
    source: string = "web",
    workDir?: string,
  ): Promise<{ ok: boolean; note?: string; protocol?: string; injected?: boolean; status?: string }> {
    this.assertConnected();
    const res = await fetch(`${this.devBaseUrl}/remote-runtime/sessions/${encodeURIComponent(sessionId)}/command`, {
      method: "POST",
      headers: { ...this.authHeaders, "Content-Type": "application/json" },
      body: JSON.stringify({ command, source, ...(workDir ? { workDir } : {}) }),
    });
    const data = await res.json().catch(() => ({}));
    if (!res.ok) throw new Error(data?.error || `Failed to send remote runtime command: HTTP ${res.status}`);
    return data;
  }

  async getRemoteRuntimeSession(sessionId: string): Promise<RemoteRuntimeSession> {
    this.assertConnected();
    const res = await fetch(`${this.devBaseUrl}/remote-runtime/sessions/${encodeURIComponent(sessionId)}`, { headers: this.authHeaders });
    const data = await res.json().catch(() => ({}));
    if (!res.ok) throw new Error(data?.error || `Failed to load remote runtime session: HTTP ${res.status}`);
    return data as RemoteRuntimeSession;
  }

  async closeRemoteRuntimeSession(sessionId: string): Promise<RemoteRuntimeStopResult> {
    this.assertConnected();
    const res = await fetch(`${this.devBaseUrl}/remote-runtime/sessions/${encodeURIComponent(sessionId)}`, {
      method: "DELETE",
      headers: this.authHeaders,
    });
    const data = await res.json().catch(() => ({}));
    if (!res.ok) throw new Error(data?.error || `Failed to close remote runtime session: HTTP ${res.status}`);
    return data as RemoteRuntimeStopResult;
  }

  async fetchRemoteRuntimeTurnCredentials(): Promise<{ iceServers: RTCIceServer[]; ttlSeconds: number }> {
    this.assertConnected();
    const res = await fetch(`${this.devBaseUrl}/remote-runtime/turn-credentials`, {
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
    const res = await fetch(`${this.devBaseUrl}/remote-runtime/sessions/${encodeURIComponent(sessionId)}/webrtc/offer`, {
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
    const res = await fetch(`${this.devBaseUrl}/remote-runtime/sessions/${encodeURIComponent(sessionId)}/frame?ts=${Date.now()}`, {
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
    const res = await fetch(`${this.devBaseUrl}/remote-runtime/sessions/${encodeURIComponent(sessionId)}/control`, {
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
    /** Structured capability gap behind `error` (a missing toolchain) — the
     *  same object the /dev/events error frame carries, polled here for the
     *  case where the stream was torn down when the failure happened. Parse
     *  with @/lib/capabilityGap; never regex the error string. */
    capabilityGap?: unknown;
  } | null> {
    this.assertConnected();
    try {
      // Role-routed when a render split is active (devBaseUrl); the plain
      // fetchAgentPath relay-fallback only applies to the connected box.
      const res = this.renderRouteDeviceId
        ? await fetch(`${this.devBaseUrl}/dev/status`, { headers: this.authHeaders })
        : await this.fetchAgentPath(`/dev/status`, { headers: this.authHeaders });
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
        error: `${err instanceof Error ? err.message : String(err)} (${this.baseUrl || "no agent route"})`,
      };
    }
  }

  async getDevServerTarget(): Promise<DevTargetPreference | null> {
    this.assertConnected();
    try {
      const res = await fetch(`${this.devBaseUrl}/dev/target`, { headers: this.authHeaders });
      if (!res.ok) return null;
      return res.json();
    } catch { return null; }
  }

  async setDevServerTarget(target: DevTargetPreference): Promise<DevTargetPreference | null> {
    this.assertConnected();
    try {
      const res = await fetch(`${this.devBaseUrl}/dev/target`, {
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
      const res = await fetch(`${this.devBaseUrl}/vibing/preview/start`, {
        method: "POST",
        headers: {
          ...this.authHeaders,
          "Content-Type": "application/json",
          "X-Yaver-NetMode": opts.netMode ?? "relay-wifi",
        },
        body: JSON.stringify({ ...opts, mode: opts.mode ?? "live" }),
      });
      if (!res.ok) {
        // The refusal is the informative part, and this used to discard it: a
        // 409 "another surface already has this project" arrived, `null` came
        // out, and the caller printed "is Chrome/Chromium installed?" — a
        // confidently wrong diagnosis of a lock. Carry the agent's named cause
        // and its route out as a throw so the panel can render the takeover.
        const data = await res.json().catch(() => ({} as Record<string, unknown>));
        const err = new Error(
          typeof data?.error === "string" && data.error ? data.error : `start preview failed (${res.status})`,
        ) as Error & { code?: string; capabilityGap?: unknown; status?: number };
        err.status = res.status;
        if (typeof data?.code === "string") err.code = data.code;
        if (data?.capabilityGap) err.capabilityGap = data.capabilityGap;
        throw err;
      }
      const data = await res.json();
      return data?.session ?? null;
    } catch (err) {
      // A transport failure is still `null` — the caller's existing shape. Only
      // a REFUSAL the agent explained propagates, so no existing call site
      // starts throwing on a dropped connection.
      if (err instanceof Error && ((err as { code?: string }).code || (err as { capabilityGap?: unknown }).capabilityGap)) throw err;
      return null;
    }
  }

  /** Invoke a CapabilityGap's route AS GIVEN — method, path, and the body the
   *  agent pre-filled. A `GapFix` is a route, so a surface must be able to press
   *  it without knowing which failure produced it; every per-failure wrapper we
   *  write instead is one more place the next remedy will not reach. Throws with
   *  the agent's own sentence so the caller can say what failed. */
  async invokeGapFix(method: string, path: string, body?: Record<string, unknown> | null): Promise<void> {
    this.assertConnected();
    const verb = (method || "POST").toUpperCase();
    const res = await fetch(`${this.devBaseUrl}${path}`, {
      method: verb,
      headers: { ...this.authHeaders, "Content-Type": "application/json" },
      body: verb === "GET" || verb === "HEAD" ? undefined : JSON.stringify(body ?? {}),
    });
    if (!res.ok) {
      const data = await res.json().catch(() => ({} as Record<string, unknown>));
      throw new Error(typeof data?.error === "string" && data.error ? data.error : `${verb} ${path} failed (${res.status})`);
    }
  }

  async stopVibePreview(project: string): Promise<boolean> {
    this.assertConnected();
    try {
      const res = await fetch(`${this.devBaseUrl}/vibing/preview/stop`, {
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
      const res = await fetch(`${this.devBaseUrl}/vibing/preview/status`, { headers: this.authHeaders });
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
      const res = await fetch(`${this.devBaseUrl}/vibing/preview/clip/start`, {
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
        `${this.devBaseUrl}/vibing/preview/clips?project=${encodeURIComponent(project)}`,
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
      url: `${this.devBaseUrl}/vibing/preview/frames/${encodeURIComponent(hash)}?project=${encodeURIComponent(project)}`,
      headers: this.authHeaders,
    };
  }

  vibeClipRequest(clipId: string): { url: string; headers: Record<string, string> } | null {
    if (!this.baseUrl) return null;
    return {
      url: `${this.devBaseUrl}/vibing/preview/clip/${encodeURIComponent(clipId)}`,
      headers: this.authHeaders,
    };
  }

  /** Authed request tuple for a clip's poster JPEG. Same fetch→blob shim as
   *  vibeClipRequest — the poster route is authSDK, so a bare
   *  `<img src>` 401s over the relay (audit B8). */
  vibeClipPosterRequest(clipId: string): { url: string; headers: Record<string, string> } | null {
    if (!this.baseUrl) return null;
    return {
      url: `${this.devBaseUrl}/vibing/preview/clip/${encodeURIComponent(clipId)}/poster`,
      headers: this.authHeaders,
    };
  }

  /** Same-origin `/d/<deviceId>/…` proxy URL for a proof clip, suitable for a
   *  plain `<video src>`: the Next.js route (`app/d/[deviceId]/[[...path]]`)
   *  is cookie-authed and streams the body through untouched, so the browser
   *  gets real Range/seek instead of a fully-buffered blob. Uses the runner
   *  box when a machine-role split is active (the proof was produced there),
   *  else the connected device. Returns null when no deviceId is known —
   *  callers fall back to the vibeClipRequest fetch→blob shim. */
  taskProofClipProxyUrl(clipId: string): string | null {
    const id = this.taskRouteDeviceId ?? this.deviceId;
    if (!id) return null;
    return `/d/${encodeURIComponent(id)}/vibing/preview/clip/${encodeURIComponent(clipId)}`;
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
          `${this.devBaseUrl}/vibing/preview/events?project=${encodeURIComponent(project)}`,
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
    const res = await fetch(`${this.devBaseUrl}/dev/start`, {
      method: "POST",
      headers: { ...this.authHeaders, "Content-Type": "application/json" },
      body: JSON.stringify(body),
    });
    const data: any = await res.json().catch(() => ({}));
    if (!res.ok) {
      // 412 = missing toolchain, a STRUCTURED refusal (missingTools /
      // installEndpoint / helpHint). The mobile app renders it as a one-tap
      // install; the web surface must at least NAME the remedy instead of
      // letting the bare error line time out into "still no preview".
      if (res.status === 412 && Array.isArray(data?.missingTools) && data.missingTools.length > 0) {
        const remedy = data?.installable
          ? ` Yaver can install ${data.missingTools.join(", ")} on that machine — ${data?.helpHint || `POST ${data?.installEndpoint || "/install/<tool>"} and retry`}.`
          : ` Install ${data.missingTools.join(", ")} on that machine, then retry.`;
        const err: any = new Error((data?.error || "Cannot start dev server: toolchain missing.") + remedy);
        err.missingTools = data.missingTools;
        err.installEndpoint = data?.installEndpoint;
        err.installable = data?.installable === true;
        // The typed route (agent 1.99.380+), forwarded verbatim. Everything
        // above flattens the structured refusal back into a sentence no view
        // branches on — which is exactly why web had no install affordance
        // anywhere. Callers parse this with @/lib/capabilityGap instead.
        err.capabilityGap = data?.capabilityGap;
        throw err;
      }
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

  async getWorkspaceApps(
    kind?: string | string[],
    root?: string,
    source: "connected" | "render" = "connected",
  ): Promise<WorkspaceAppView[]> {
    this.assertConnected();
    const params = new URLSearchParams();
    if (kind) params.set("kind", Array.isArray(kind) ? kind.join(",") : kind);
    if (root) params.set("root", root);
    const query = params.toString() ? `?${params.toString()}` : "";
    const base = source === "render" ? this.devBaseUrl : this.baseUrl;
    const res = await fetch(`${base}/workspace/apps${query}`, { headers: this.authHeaders });
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
    topic?: string;
    message?: string;
    error?: string;
  }> {
    this.assertConnected();
    const res = await fetch(`${this.devBaseUrl}/dev/stop`, { method: "POST", headers: this.authHeaders });
    const data = await res.json().catch(() => ({}));
    if (!res.ok) {
      throw new Error(data?.message || data?.error || "Failed to stop serving preview");
    }
    return data;
  }

  /** Reload the running preview.
   *
   *  Fast/full contract (agent 1.99.374+):
   *   - "fast" (default) — the framework's cheapest refresh: Metro/Expo
   *     built-in reload, Flutter "r" hot reload, existing bundle re-served
   *     when fresh. POST /dev/reload {mode:"fast"}.
   *   - "full" — framework-level restart: Flutter "R" hot restart; when the
   *     active lane is the static web bundle, the agent also forces an async
   *     re-export (warm cache — never a cold start). POST /dev/reload
   *     {mode:"full"}.
   *  Legacy modes stay supported: "dev" is fast; "bundle" rebuilds the
   *  Hermes bundle via /dev/reload-app. Older agents ignore the unknown
   *  mode field and behave exactly as before — backward compatible. */
  async reloadDevServer(opts?: { mode?: "dev" | "bundle" | "fast" | "full" }): Promise<{
    ok?: boolean;
    nativeChangesDetected?: boolean;
    nativeChanges?: Array<{ path?: string; reason?: string }>;
    changeClass?: string;
    status?: string;
    bundleUrl?: string;
    moduleName?: string;
    reloadMode?: string;
    webBundleRebuildKicked?: boolean;
    error?: string;
  }> {
    this.assertConnected();
    const mode = opts?.mode ?? "fast";
    if (mode !== "bundle") {
      const wireMode = mode === "full" ? "full" : "fast";
      const res = await fetch(`${this.devBaseUrl}/dev/reload`, {
        method: "POST",
        headers: { ...this.authHeaders, "Content-Type": "application/json" },
        body: JSON.stringify({ mode: wireMode }),
      });
      const data = await res.json().catch(() => ({}));
      if (!res.ok) {
        throw new Error(data?.error || "Failed to reload dev server");
      }
      return data;
    }

    const res = await fetch(`${this.devBaseUrl}/dev/reload-app`, {
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
    const res = await fetch(`${this.devBaseUrl}/dev/web-preview/start`, { method: "POST", headers: this.authHeaders });
    const data = await res.json().catch(() => ({}));
    if (!res.ok) throw new Error(data?.error || "Failed to start Expo Web preview");
    return data;
  }

  async stopWebPreview(): Promise<{ ok: boolean }> {
    this.assertConnected();
    const res = await fetch(`${this.devBaseUrl}/dev/web-preview/stop`, { method: "POST", headers: this.authHeaders });
    const data = await res.json().catch(() => ({}));
    if (!res.ok) throw new Error(data?.error || "Failed to stop Expo Web preview");
    return data;
  }

  /** URL the browser iframe points at for the Expo Web sibling. Only
   *  meaningful when devStatus.webPort > 0. Mirrors devPreviewUrl
   *  shape (relay-proxied vs direct) but hits /dev-web/ instead. */
  get devWebPreviewUrl(): string | null {
    if (!this.baseUrl) return null;
    // Render-route active: always the same-origin /d/<renderId>/ proxy — it
    // forwards server-side via the relay with the password injected, so it
    // works regardless of THIS session's primary transport (relay, Tailscale
    // tunnel, or localhost direct).
    if (this.renderRouteDeviceId) {
      return `/d/${encodeURIComponent(this.renderRouteDeviceId)}/dev-web/`;
    }
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
    // Render-route active: same-origin proxy to the render box (see
    // devWebPreviewUrl — transport-independent by construction).
    if (this.renderRouteDeviceId) {
      return `/d/${encodeURIComponent(this.renderRouteDeviceId)}/dev/web-bundle/`;
    }
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

  webBundlePreviewUrl(agentBundleUrl?: string | null): string | null {
    const base = this.devWebBundleUrl;
    if (!base) return null;
    if (!agentBundleUrl) return base;
    try {
      const parsed = new URL(agentBundleUrl, "http://agent.local");
      if (!parsed.pathname.startsWith("/dev/web-bundle")) return base;
      const suffix = parsed.pathname.replace(/^\/dev\/web-bundle\/?/, "");
      const baseWithSuffix = suffix ? `${base.replace(/\/$/, "")}/${suffix}` : base;
      return `${baseWithSuffix}${parsed.search}${parsed.hash}`;
    } catch {
      return base;
    }
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
    /** Fast/full reload contract (agent 1.99.374+):
     *  "fast" re-serves the existing bundle when it is provably fresh
     *  (built commit == HEAD, tracked tree clean) and otherwise rebuilds
     *  with the warm persistent Metro cache; "full" always re-exports
     *  (still warm cache — never a cold start). Older agents ignore the
     *  field and always rebuild. Default (undefined) = always rebuild. */
    mode?: "fast" | "full";
  }): Promise<{
    ok: boolean;
    bundleUrl: string;
    size: number;
    fileCount: number;
    /** True when the agent re-served an existing fresh bundle (mode:"fast"). */
    reused?: boolean;
    error?: string;
    output?: string;
  }> {
    if (!this.baseUrl) throw new Error("not connected");
    const res = await this.fetchWithTimeout(`${this.devBaseUrl}/dev/build-native`, {
      method: "POST",
      headers: { ...this.authHeaders, "Content-Type": "application/json" },
      body: JSON.stringify({
        target: opts.target ?? "web-js-bundle",
        mode: opts.mode ?? undefined,
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
      reused: obj.reused === true,
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
    bundleUrl?: string;
  }> {
    if (!this.baseUrl) return { built: false };
    try {
      const res = await this.fetchWithTimeout(`${this.devBaseUrl}/dev/web-bundle/info`, {
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
        bundleUrl: typeof body.bundleUrl === "string" ? body.bundleUrl : undefined,
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
      await fetch(`${this.devBaseUrl}/dev/web-bundle/ack`, {
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
      await fetch(`${this.devBaseUrl}/dev/web-bundle/error`, {
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
    // Render-route active: same-origin proxy to the render box, independent
    // of this session's primary transport (relay / Tailscale / direct).
    if (this.renderRouteDeviceId) {
      return `/d/${encodeURIComponent(this.renderRouteDeviceId)}/dev/`;
    }
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
    // Live-reload events come from the box that actually renders. Null (not
    // a throw) when the render box is unreachable — this getter runs during
    // React render.
    const base = this.devBaseUrlOrNull;
    if (!base) return null;
    return this.appendStreamAuth(`${base}/dev/events`);
  }

  /** SSE URL for the agent-update progress stream — same query-
   *  param auth pattern. */
  get agentUpdateStreamUrl(): string | null {
    if (!this.baseUrl) return null;
    return this.appendStreamAuth(`${this.baseUrl}/streams/agent-update`);
  }

  /** Relay password for query-param stream auth (`__rp=`): the active
   *  transport's when relay-connected, else — when a machine-role route can
   *  send the stream through a relay despite a direct/tunnel primary
   *  transport — the configured fallback relay's. The relay strips `__rp`
   *  before forwarding; a directly-reached agent ignores it. */
  private get streamRelayPassword(): string | null {
    if (this._activeRelayUrl) return this.activeRelayPassword;
    return this.taskRouteDeviceId || this.renderRouteDeviceId ? this.routingRelayPassword : null;
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
      if (this.streamRelayPassword) {
        params.push(`__rp=${encodeURIComponent(this.streamRelayPassword)}`);
      }
      const join = url.includes("?") ? "&" : "?";
      return params.length ? `${url}${join}${params.join("&")}` : url;
    }
    if (this.token) u.searchParams.set("token", this.token);
    if (this.streamRelayPassword) {
      u.searchParams.set("__rp", this.streamRelayPassword);
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
        // Also refresh the cached relayServers passwords: the reconnect
        // ladder's next attemptConnect() reads relay.password from THAT
        // list, so repairing only activeRelayPassword left the ladder
        // retrying with the stale credential it just proved dead.
        for (const cached of this.relayServers) {
          const fresh = relays.find((r) => r.httpUrl === cached.httpUrl);
          if (fresh?.password) cached.password = fresh.password;
        }
      }
      return { ok: true };
    } catch (err) {
      return { ok: false, error: err instanceof Error ? err.message : String(err) };
    }
  }

  async getCompanyAIOptions(teamId: string): Promise<CompanyAIOptionsResponse> {
    if (!this.token) throw new Error("not signed in");
    const url = new URL(`${CONVEX_URL}/company-ai/options`);
    url.searchParams.set("teamId", teamId);
    const res = await fetch(url.toString(), {
      headers: { Authorization: `Bearer ${this.token}` },
      cache: "no-store",
    });
    const data = await res.json().catch(() => ({}));
    if (!res.ok) {
      throw new Error(data?.error || `company-ai/options ${res.status}`);
    }
    return data as CompanyAIOptionsResponse;
  }

  async saveCompanyAIOptions(teamId: string, options: CompanyAIOptions): Promise<{ ok: boolean; id?: string; error?: string }> {
    if (!this.token) throw new Error("not signed in");
    const res = await fetch(`${CONVEX_URL}/company-ai/options`, {
      method: "POST",
      headers: {
        Authorization: `Bearer ${this.token}`,
        "Content-Type": "application/json",
      },
      body: JSON.stringify({ teamId, options }),
    });
    const data = await res.json().catch(() => ({}));
    if (!res.ok) {
      return { ok: false, error: data?.error || `company-ai/options ${res.status}` };
    }
    return data as { ok: boolean; id?: string };
  }

  async resolveCompanyAIRuntime(params: {
    teamId: string;
    workKind: CompanyAIWorkKind;
    requestedRunner?: string;
    requestedModel?: string;
    requestedDeviceId?: string;
    source?: "talos-web" | "talos-mobile" | "talos-desktop" | "yaver-web" | "yaver-mobile" | "yaver-desktop" | "mcp" | "api";
  }): Promise<CompanyAIResolvedRuntime> {
    if (!this.token) throw new Error("not signed in");
    const res = await fetch(`${CONVEX_URL}/company-ai/resolve`, {
      method: "POST",
      headers: {
        Authorization: `Bearer ${this.token}`,
        "Content-Type": "application/json",
      },
      cache: "no-store",
      body: JSON.stringify(params),
    });
    const data = await res.json().catch(() => ({}));
    if (!res.ok) {
      throw new Error(data?.error || `company-ai/resolve ${res.status}`);
    }
    return data as CompanyAIResolvedRuntime;
  }

  async listTeams(): Promise<TeamSummary[]> {
    if (!this.token) throw new Error("not signed in");
    const res = await fetch(`${CONVEX_URL}/teams`, {
      headers: { Authorization: `Bearer ${this.token}` },
      cache: "no-store",
    });
    const data = await res.json().catch(() => ({}));
    if (!res.ok) {
      throw new Error(data?.error || `teams ${res.status}`);
    }
    return Array.isArray(data?.teams) ? data.teams as TeamSummary[] : [];
  }

  // ── Companion compute (yaver.companion.yaml) ───────────────────────
  // P2P against the connected agent — never Convex. Status/detection flow
  // straight from the box that runs the crons/services.

  /** Housekeeping feed from the agent's custodian (wardens + failure playbook).
   *  See desktop/agent/custodian.go — the layer's whole point is being VISIBLE,
   *  so a surface that cannot render it makes it worthless. */
  async getCustodianStatus(): Promise<{
    wardens: Array<{ name: string; everySec: number; lastSwept?: string; neverRun: boolean }>;
    recent: Array<{
      warden: string; subject: string; problem: string; action: string;
      outcome: "fixed" | "spared" | "needs-human" | "needs-runner";
      remedy?: string; at: string;
    }>;
    counts: Record<string, number>;
    sweeping: boolean;
  }> {
    const res = await this.agentFetch("/custodian/status");
    const d = await res.json().catch(() => ({}));
    return {
      wardens: Array.isArray(d?.wardens) ? d.wardens : [],
      recent: Array.isArray(d?.recent) ? d.recent : [],
      counts: d?.counts && typeof d.counts === "object" ? d.counts : {},
      sweeping: !!d?.sweeping,
    };
  }

  /** Run every warden now and return what they found. Synchronous by design:
   *  the user pressed a button and is waiting for an answer, and "started" is
   *  not an answer. */
  async sweepCustodian(): Promise<{ swept: number; summary: string; findings: any[] }> {
    const res = await this.agentFetch("/custodian/sweep", { method: "POST" });
    const d = await res.json().catch(() => ({}));
    return {
      swept: typeof d?.swept === "number" ? d.swept : 0,
      summary: typeof d?.summary === "string" ? d.summary : "",
      findings: Array.isArray(d?.findings) ? d.findings : [],
    };
  }

  async companionListProjects(): Promise<CompanionProjectSummary[]> {
    const res = await this.agentFetch("/companion/list");
    const data = await res.json().catch(() => ({}));
    return Array.isArray(data?.projects) ? (data.projects as CompanionProjectSummary[]) : [];
  }

  // Store-onboarding concierge catalogue + best-effort status (agent is the
  // single source of truth; the UI only renders + routes to the official
  // Apple/Google URLs each task carries).
  async getStores(): Promise<StoreTask[]> {
    const res = await this.agentFetch("/stores");
    const data = await res.json().catch(() => ({}));
    return Array.isArray(data?.tasks) ? (data.tasks as StoreTask[]) : [];
  }

  // Required permissions/capabilities inferred from the project's code.
  async getCapabilities(path?: string): Promise<ManifestPlan | null> {
    const q = path ? `?path=${encodeURIComponent(path)}` : "";
    const res = await this.agentFetch(`/capabilities${q}`);
    return (await res.json().catch(() => null)) as ManifestPlan | null;
  }

  // Canonical store listing derived from code (identity + truthful privacy).
  async getListing(path?: string): Promise<StoreListing | null> {
    const q = path ? `?path=${encodeURIComponent(path)}` : "";
    const res = await this.agentFetch(`/listing${q}`);
    return (await res.json().catch(() => null)) as StoreListing | null;
  }

  // One "ready to ship?" verdict aggregating every publish check.
  async getPublishStatus(path?: string): Promise<PublishReadiness | null> {
    const q = path ? `?path=${encodeURIComponent(path)}` : "";
    const res = await this.agentFetch(`/publish/status${q}`);
    return (await res.json().catch(() => null)) as PublishReadiness | null;
  }

  async companionDetect(repo: string): Promise<CompanionDetectResult> {
    const res = await this.agentFetch(`/companion/detect?repo=${encodeURIComponent(repo)}`);
    const data = await res.json().catch(() => ({}));
    if (data?.error) throw new Error(data.error);
    return { items: data?.items ?? [], manifestYaml: data?.manifestYaml ?? "" };
  }

  async companionGetManifest(repo: string): Promise<{ exists: boolean; manifestYaml?: string }> {
    const res = await this.agentFetch(`/companion/manifest?repo=${encodeURIComponent(repo)}`);
    return (await res.json().catch(() => ({ exists: false }))) as { exists: boolean; manifestYaml?: string };
  }

  async companionWriteManifest(repo: string, manifestYaml: string): Promise<{ ok?: boolean; error?: string; path?: string }> {
    const res = await this.agentFetch("/companion/manifest", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ repo, manifestYaml }),
    });
    return (await res.json().catch(() => ({}))) as { ok?: boolean; error?: string; path?: string };
  }

  async companionUp(repo: string): Promise<CompanionStatus> {
    const res = await this.agentFetch("/companion/up", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ repo }),
    });
    const data = await res.json().catch(() => ({}));
    if (data?.error) throw new Error(data.error);
    return data.status as CompanionStatus;
  }

  async companionDown(project: string): Promise<void> {
    const res = await this.agentFetch("/companion/down", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ project }),
    });
    const data = await res.json().catch(() => ({}));
    if (data?.error) throw new Error(data.error);
  }

  async companionStatus(project: string): Promise<CompanionStatus> {
    const res = await this.agentFetch(`/companion/status?project=${encodeURIComponent(project)}`);
    const data = await res.json().catch(() => ({}));
    if (data?.error) throw new Error(data.error);
    return data.status as CompanionStatus;
  }

  async microserviceDetect(repo: string, project?: string): Promise<MicroserviceWrapResult> {
    const q = new URLSearchParams({ repo });
    if (project) q.set("project", project);
    const res = await this.agentFetch(`/microservices/detect?${q.toString()}`);
    const data = await res.json().catch(() => ({}));
    if (data?.error) throw new Error(data.error);
    return data as MicroserviceWrapResult;
  }

  async microserviceWrap(req: MicroserviceWrapRequest): Promise<MicroserviceWrapResult> {
    const res = await this.agentFetch("/microservices/wrap", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(req),
    });
    const data = await res.json().catch(() => ({}));
    if (data?.error) throw new Error(data.error);
    return data as MicroserviceWrapResult;
  }

  async microserviceStatus(project: string): Promise<CompanionStatus> {
    const res = await this.agentFetch(`/microservices/status?project=${encodeURIComponent(project)}`);
    const data = await res.json().catch(() => ({}));
    if (data?.error) throw new Error(data.error);
    return (data.status ?? data) as CompanionStatus;
  }

  async microserviceDown(project: string): Promise<void> {
    const res = await this.agentFetch("/microservices/down", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ project }),
    });
    const data = await res.json().catch(() => ({}));
    if (data?.error) throw new Error(data.error);
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

  async mobilePlatformMatrix(args?: { directory?: string }): Promise<MobilePlatformMatrixReport> {
    this.assertConnected();
    const params = new URLSearchParams();
    if (args?.directory) params.set("directory", args.directory);
    const qs = params.toString();
    const res = await fetch(
      `${this.baseUrl}/mobile/platform-matrix${qs ? `?${qs}` : ""}`,
      { headers: this.authHeaders },
    );
    if (!res.ok) throw new Error(`mobilePlatformMatrix ${res.status}`);
    const data = await res.json();
    const surfaces = Array.isArray(data?.surfaces) ? data.surfaces : [];
    return {
      devicePlatform: String(data?.device_platform ?? ""),
      deviceArch: String(data?.device_arch ?? ""),
      surfaces: surfaces.map((s: any) => ({
        id: String(s?.id ?? ""),
        label: String(s?.label ?? ""),
        family: String(s?.family ?? ""),
        surface: String(s?.surface ?? ""),
        status: String(s?.status ?? ""),
        buildSupported: !!s?.build_supported,
        submitSupported: !!s?.submit_supported,
        managedCloud: String(s?.managed_cloud ?? ""),
        requiredHost: String(s?.required_host ?? ""),
        storeTarget: s?.store_target ? String(s.store_target) : undefined,
        deployTarget: s?.deploy_target ? String(s.deploy_target) : undefined,
        script: s?.script ? String(s.script) : undefined,
        scriptPresent: typeof s?.script_present === "boolean" ? s.script_present : undefined,
        queueTargets: Array.isArray(s?.queue_targets) ? s.queue_targets.map(String) : undefined,
        notes: Array.isArray(s?.notes) ? s.notes.map(String) : undefined,
        limitations: Array.isArray(s?.limitations) ? s.limitations.map(String) : undefined,
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

  async infraPower(
    action: "agent_shutdown" | "host_reboot" | "agent_restart",
    target?: string,
  ): Promise<any> {
    this.assertConnected();
    const res = await fetch(this.infraPowerUrl(target), {
      method: "POST",
      headers: { ...this.authHeaders, "Content-Type": "application/json" },
      body: JSON.stringify({ action, confirm: true }),
    });
    const data = await res.json().catch(() => ({}));
    if (!res.ok) {
      // The agent refuses with reason + remedy in one sentence. Surface it
      // verbatim: a generic "request failed" would throw away the only text
      // that tells the user what to do instead.
      throw new Error(data?.error || `power action failed: ${res.status}`);
    }
    return data;
  }

  /** Read-only dry run: what power actions this machine can ACTUALLY perform.
   *  Asking must never require agreeing to do anything, so this is a GET and
   *  takes no confirm. Call it before rendering any power control. */
  async infraPowerReport(target?: string): Promise<PowerReport> {
    this.assertConnected();
    const res = await fetch(this.infraPowerUrl(target), { headers: this.authHeaders });
    const data = await res.json().catch(() => ({}));
    if (!res.ok) throw new Error(data?.error || `power report failed: ${res.status}`);
    return data as PowerReport;
  }

  private infraPowerUrl(target?: string): string {
    return target
      ? `${this.baseUrl}/peer/${encodeURIComponent(target)}/infra/power`
      : `${this.baseUrl}/infra/power`;
  }

  // Grant (or revoke) host-reboot with the owner's sudo password. Sent once over
  // the authenticated agent channel for a single `sudo -S` on the box; never
  // stored. Installs a scoped sudoers rule (reboot binaries only), not root.
  async infraRebootGrant(password: string, revoke = false): Promise<{ ok: boolean; canReboot: boolean }> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/infra/reboot-grant`, {
      method: "POST",
      headers: { ...this.authHeaders, "Content-Type": "application/json" },
      body: JSON.stringify({ password, revoke }),
    });
    const data = await res.json().catch(() => ({}));
    if (!res.ok) throw new Error(data?.error || `reboot grant failed: ${res.status}`);
    return data;
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
  async terminalWsUrl(
    cwd?: string,
    opts?: { launch?: "claude" | "codex" | "opencode"; tmuxSession?: string },
  ): Promise<string> {
    if (opts?.tmuxSession) {
      // Task tmux sessions live on the box that runs tasks — the runner box
      // when a machine-role split is active.
      const base = this.taskBaseUrl;
      const token = await this.issueBrowserSession("/ws/runner", base);
      const q = new URLSearchParams({
        browser_session: token,
        name: opts.tmuxSession,
      });
      return this.appendRelayPwToWs(`${base.replace(/^http/, "ws")}/ws/runner?${q.toString()}`);
    }
    const token = await this.issueBrowserSession("/ws/terminal");
    const c = cwd ? `&cwd=${encodeURIComponent(cwd)}` : "";
    const launch = opts?.launch ? `&launch=${encodeURIComponent(opts.launch)}` : "";
    return this.appendRelayPwToWs(`${this.baseUrl.replace(/^http/, "ws")}/ws/terminal?browser_session=${encodeURIComponent(token)}${c}${launch}`);
  }

  async listTmuxSessions(): Promise<TmuxSessionSummary[]> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/tmux/sessions`, { headers: this.authHeaders });
    const data = await res.json().catch(() => ({}));
    if (!res.ok) throw new Error(data?.error || `Failed to list Yaver sessions: HTTP ${res.status}`);
    return Array.isArray(data?.sessions) ? data.sessions : [];
  }

  async listRunnerSessions(): Promise<
    Array<{ name: string; runner: string; command?: string; confirmed: boolean; attached?: boolean }>
  > {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/runner/sessions`, { headers: this.authHeaders });
    const data = await res.json().catch(() => ({}));
    if (!res.ok) throw new Error(data?.error || `Failed to list runner sessions: HTTP ${res.status}`);
    return Array.isArray(data?.sessions) ? data.sessions : [];
  }

  /** Deterministic safe sync of a remote repo: pull --rebase --autostash
   *  against origin/<branch> then push (never force). Aborts on conflict. */
  async gitSyncRemote(workDir: string): Promise<{
    ok: boolean;
    branch?: string;
    hash?: string;
    actions?: string[];
    rebased?: boolean;
    pushed?: boolean;
    requiresAgent?: boolean;
    conflicts?: string[];
    error?: string;
    output?: string;
  }> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/git/sync-remote`, {
      method: "POST",
      headers: { ...this.authHeaders, "Content-Type": "application/json" },
      body: JSON.stringify({ workDir }),
    });
    const data = await res.json().catch(() => ({}));
    return data;
  }

  async adoptTmuxSession(session: string, pane?: string): Promise<{ taskId: string; session: string; pane?: string }> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/tmux/adopt`, {
      method: "POST",
      headers: { ...this.authHeaders, "Content-Type": "application/json" },
      body: JSON.stringify({ session, ...(pane ? { pane } : {}) }),
    });
    const data = await res.json().catch(() => ({}));
    if (!res.ok) throw new Error(data?.error || `Failed to join Yaver session: HTTP ${res.status}`);
    return data;
  }

  async detachTmuxTask(taskId: string): Promise<void> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/tmux/detach`, {
      method: "POST",
      headers: { ...this.authHeaders, "Content-Type": "application/json" },
      body: JSON.stringify({ taskId }),
    });
    const data = await res.json().catch(() => ({}));
    if (!res.ok) throw new Error(data?.error || `Failed to detach Yaver session: HTTP ${res.status}`);
  }

  async closeTmuxTask(taskId: string): Promise<void> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/tmux/close`, {
      method: "POST",
      headers: { ...this.authHeaders, "Content-Type": "application/json" },
      body: JSON.stringify({ taskId }),
    });
    const data = await res.json().catch(() => ({}));
    if (!res.ok) throw new Error(data?.error || `Failed to close Yaver session: HTTP ${res.status}`);
  }

  private appendRelayPwToWs(url: string): string {
    const pw = this.streamRelayPassword;
    if (!pw) return url;
    const join = url.includes("?") ? "&" : "?";
    return `${url}${join}__rp=${encodeURIComponent(pw)}`;
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

  // ── Feedback work local worker config ─────────────────────────────

  async getFeedbackWorkConfig(): Promise<FeedbackWorkAgentConfig> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/feedback-work/config`, { headers: this.authHeaders });
    const data = await res.json().catch(() => ({}));
    if (!res.ok) throw new Error(data?.error || `feedback work config: HTTP ${res.status}`);
    return {
      enabled: !!data.enabled,
      running: !!data.running,
      intervalSeconds: typeof data.intervalSeconds === "number" ? data.intervalSeconds : undefined,
      workerId: typeof data.workerId === "string" ? data.workerId : undefined,
      projectSlug: typeof data.projectSlug === "string" ? data.projectSlug : undefined,
      createProviderIssues: !!data.createProviderIssues,
      runtimeReason: typeof data.runtimeReason === "string" ? data.runtimeReason : undefined,
      ok: data.ok !== false,
    };
  }

  async updateFeedbackWorkConfig(patch: Partial<FeedbackWorkAgentConfig>): Promise<FeedbackWorkAgentConfig> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/feedback-work/config`, {
      method: "PATCH",
      headers: { ...this.authHeaders, "Content-Type": "application/json" },
      body: JSON.stringify(patch),
    });
    const data = await res.json().catch(() => ({}));
    if (!res.ok) throw new Error(data?.error || `feedback work config update: HTTP ${res.status}`);
    return {
      enabled: !!data.enabled,
      running: !!data.running,
      intervalSeconds: typeof data.intervalSeconds === "number" ? data.intervalSeconds : undefined,
      workerId: typeof data.workerId === "string" ? data.workerId : undefined,
      projectSlug: typeof data.projectSlug === "string" ? data.projectSlug : undefined,
      createProviderIssues: !!data.createProviderIssues,
      runtimeReason: typeof data.runtimeReason === "string" ? data.runtimeReason : undefined,
      ok: data.ok !== false,
    };
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

  async developmentDoctor(): Promise<DevelopmentDoctorReport> {
    const res = await this.agentFetch("/agent/doctor", { cache: "no-store" });
    const data = await res.json().catch(() => ({}));
    if (!res.ok) throw new Error(data?.error || `Development Doctor failed: HTTP ${res.status}`);
    return {
      ok: data?.ok === true,
      checks: Array.isArray(data?.checks) ? data.checks : [],
    };
  }

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

  async gitStatus(workDir: string, target?: string): Promise<GitStatusRow> {
    this.assertConnected();
    const res = await fetch(this.peerOrLocalUrl(target, `/git/status?workDir=${encodeURIComponent(workDir)}`), {
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

  async managedGitStatus(args: { slug?: string; workDir?: string }): Promise<ManagedGitProjectMeta | null> {
    this.assertConnected();
    const qs = new URLSearchParams();
    if (args.slug) qs.set("slug", args.slug);
    if (args.workDir) qs.set("workDir", args.workDir);
    const res = await fetch(`${this.baseUrl}/managed-git/status?${qs.toString()}`, {
      headers: this.authHeaders,
    });
    if (res.status === 404) return null;
    const data = await res.json().catch(() => ({}));
    if (!res.ok) throw new Error(data?.error || `managedGitStatus ${res.status}`);
    return data as ManagedGitProjectMeta;
  }

  async managedGitEnable(args: {
    slug?: string;
    workDir?: string;
    name?: string;
    visibility?: "private" | "unlisted" | "public";
  }): Promise<ManagedGitProjectMeta> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/managed-git/enable`, {
      method: "POST",
      headers: { ...this.authHeaders, "Content-Type": "application/json" },
      body: JSON.stringify(args),
    });
    const data = await res.json().catch(() => ({}));
    if (!res.ok) throw new Error(data?.error || `managedGitEnable ${res.status}`);
    return data as ManagedGitProjectMeta;
  }

  async managedGitBackupRun(args: { slug?: string; workDir?: string }): Promise<{ ok: boolean; backup: ManagedGitBackupMeta }> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/managed-git/backup/run`, {
      method: "POST",
      headers: { ...this.authHeaders, "Content-Type": "application/json" },
      body: JSON.stringify(args),
    });
    const data = await res.json().catch(() => ({}));
    if (!res.ok) throw new Error(data?.error || `managedGitBackupRun ${res.status}`);
    return data as { ok: boolean; backup: ManagedGitBackupMeta };
  }

  async managedGitRelaySourcePlan(args: {
    slug?: string;
    workDir?: string;
    branch?: string;
    baseBranch?: string;
    title?: string;
    prompt?: string;
    files?: ManagedGitRelaySourceFilePatch[];
  }): Promise<ManagedGitRelaySourcePlanResult> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/managed-git/relay-source/plan`, {
      method: "POST",
      headers: { ...this.authHeaders, "Content-Type": "application/json" },
      body: JSON.stringify(args),
    });
    const data = await res.json().catch(() => ({}));
    if (!res.ok) throw new Error(data?.error || `managedGitRelaySourcePlan ${res.status}`);
    return data as ManagedGitRelaySourcePlanResult;
  }

  async managedGitRelaySourceWorkOnce(args: {
    slug?: string;
    workDir?: string;
    intentId?: string;
    localTaskId?: string;
    branch?: string;
    baseBranch?: string;
    relayId?: string;
    title?: string;
    prompt?: string;
    message?: string;
    files?: ManagedGitRelaySourceFilePatch[];
  }): Promise<ManagedGitRelaySourceWorkResult> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/managed-git/relay-source/work-once`, {
      method: "POST",
      headers: { ...this.authHeaders, "Content-Type": "application/json" },
      body: JSON.stringify(args),
    });
    const data = await res.json().catch(() => ({}));
    if (!res.ok) throw new Error(data?.error || `managedGitRelaySourceWorkOnce ${res.status}`);
    return data as ManagedGitRelaySourceWorkResult;
  }

  async managedGitBackupCopy(args: {
    slug?: string;
    workDir?: string;
    targetKind?: "local-folder" | "shared-storage" | "dropbox";
    targetId?: string;
    destPath?: string;
  }): Promise<{ ok: boolean; backup: ManagedGitBackupMeta | ManagedGitExternalBackupMeta }> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/managed-git/backup/copy`, {
      method: "POST",
      headers: { ...this.authHeaders, "Content-Type": "application/json" },
      body: JSON.stringify(args),
    });
    const data = await res.json().catch(() => ({}));
    if (!res.ok) throw new Error(data?.error || `managedGitBackupCopy ${res.status}`);
    return data as { ok: boolean; backup: ManagedGitBackupMeta | ManagedGitExternalBackupMeta };
  }

  async managedGitDropboxStatus(): Promise<{ connected: boolean; accountId?: string; scope?: string; updatedAt?: number }> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/managed-git/dropbox/status`, { headers: this.authHeaders });
    const data = await res.json().catch(() => ({}));
    if (!res.ok) throw new Error(data?.error || `managedGitDropboxStatus ${res.status}`);
    return data;
  }

  async managedGitDropboxOAuthStart(args: { redirectUri?: string } = {}): Promise<{ sessionId: string; authUrl: string; redirectUri?: string; expiresAt?: string }> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/managed-git/dropbox/oauth/start`, {
      method: "POST",
      headers: { ...this.authHeaders, "Content-Type": "application/json" },
      body: JSON.stringify(args),
    });
    const data = await res.json().catch(() => ({}));
    if (!res.ok) throw new Error(data?.error || `managedGitDropboxOAuthStart ${res.status}`);
    return data;
  }

  async managedGitDropboxOAuthSubmit(args: { sessionId: string; code: string }): Promise<{ ok: boolean; accountId?: string }> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/managed-git/dropbox/oauth/submit`, {
      method: "POST",
      headers: { ...this.authHeaders, "Content-Type": "application/json" },
      body: JSON.stringify(args),
    });
    const data = await res.json().catch(() => ({}));
    if (!res.ok) throw new Error(data?.error || `managedGitDropboxOAuthSubmit ${res.status}`);
    return data;
  }

  async managedGitMirrorConnect(args: {
    slug?: string;
    workDir?: string;
    provider: "github" | "gitlab";
    host?: string;
    repoName?: string;
    visibility?: "private" | "public";
    description?: string;
  }): Promise<{ ok: boolean; mirror: ManagedGitMirrorMeta }> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/managed-git/mirrors/connect`, {
      method: "POST",
      headers: { ...this.authHeaders, "Content-Type": "application/json" },
      body: JSON.stringify(args),
    });
    const data = await res.json().catch(() => ({}));
    if (!res.ok) throw new Error(data?.error || `managedGitMirrorConnect ${res.status}`);
    return data as { ok: boolean; mirror: ManagedGitMirrorMeta };
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
  /**
   * gitMembers lists everyone with access to a repo, on GitHub or GitLab.
   *
   * These three go through the ops layer rather than a bespoke /git/provider/*
   * route: the agent registers them as neutral verbs (forge_surface.go), so the
   * schema and the handler cannot drift apart, and every surface — web, mobile,
   * tvOS, CLI, MCP — calls the identical verb instead of each re-deriving
   * GitHub-vs-GitLab. Pending invitations come back in the list with
   * state:"pending"; without them, invite-then-list reads as a silent failure.
   */
  async gitMembers(t: ForgeTarget = {}): Promise<ForgeMembersResult> {
    const res = await this.callOps("git_members", { ...t });
    if (res?.error || res?.ok === false) throw new Error(res?.error || "git_members failed");
    return res.initial as ForgeMembersResult;
  }

  /**
   * gitMemberInvite grants someone access. `role` is neutral (read|triage|
   * write|maintain|admin) and the agent maps it per forge. Defaults to write.
   *
   * Read the returned invite.state rather than assuming success means an email
   * went out: GitHub replies "added" when the user already had access, and no
   * invitation is sent in that case.
   */
  async gitMemberInvite(
    user: string,
    role: ForgeRole = "write",
    t: ForgeTarget = {},
  ): Promise<ForgeInviteResult> {
    const res = await this.callOps("git_member_invite", { user, role, ...t });
    if (res?.error || res?.ok === false) throw new Error(res?.error || "git_member_invite failed");
    return res.initial as ForgeInviteResult;
  }

  /** gitMemberRemove revokes a member's access. */
  async gitMemberRemove(user: string, t: ForgeTarget = {}): Promise<ForgeResultBase> {
    const res = await this.callOps("git_member_remove", { user, ...t });
    if (res?.error || res?.ok === false) throw new Error(res?.error || "git_member_remove failed");
    return res.initial as ForgeResultBase;
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

  async listRepos(target?: string): Promise<Array<{
    name: string;
    path: string;
    branch?: string;
    remote?: string;
    lastCommit?: string;
    dirty?: boolean;
  }>> {
    this.assertConnected();
    const res = await fetch(this.peerOrLocalUrl(target, "/repos/list"), { headers: this.authHeaders });
    if (!res.ok) throw new Error(`repos/list ${res.status}`);
    const data = await res.json().catch(() => []);
    return Array.isArray(data) ? data : [];
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

  /** Read the mini-figma design layer (layout + per-node overrides) for an
   * agent-hosted project. The design rides in app.yaml, so it ships on deploy. */
  async getPhoneDesign(slug: string): Promise<PhoneDesign | null> {
    if (!this.isConnected || !this.baseUrl) return null;
    const res = await fetch(`${this.baseUrl}/phone/projects/design?slug=${encodeURIComponent(slug)}`, {
      headers: this.authHeaders,
    });
    if (!res.ok) return null;
    return (await res.json()) as PhoneDesign;
  }

  /** Apply design patches over the relay (same shape as the local sandbox / MCP).
   * Returns the new design layer. */
  async patchPhoneDesign(slug: string, patches: PhoneDesignPatch[]): Promise<PhoneDesign | null> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/phone/projects/design`, {
      method: "POST",
      headers: { ...this.authHeaders, "Content-Type": "application/json" },
      body: JSON.stringify({ slug, patches }),
    });
    if (!res.ok) return null;
    return (await res.json()) as PhoneDesign;
  }

  /** Replace the whole design layer over the relay (used by the design studio's
   * snapshot save). */
  async setPhoneDesign(slug: string, design: PhoneDesign): Promise<PhoneDesign | null> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/phone/projects/design`, {
      method: "POST",
      headers: { ...this.authHeaders, "Content-Type": "application/json" },
      body: JSON.stringify({ slug, design }),
    });
    if (!res.ok) return null;
    return (await res.json()) as PhoneDesign;
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

  async getPhoneWebInstallStatus(slug: string): Promise<PhoneWebInstallStatus> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/phone/projects/install/status?slug=${encodeURIComponent(slug)}`, { headers: this.authHeaders });
    const data = await res.json().catch(() => ({}));
    if (!res.ok) throw new Error(data?.error || `HTTP ${res.status}`);
    return data as PhoneWebInstallStatus;
  }

  async publishPhoneWebApp(slug: string, brand?: PhoneAppBrand): Promise<PhoneWebInstallStatus> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/phone/projects/install/publish`, {
      method: "POST",
      headers: { ...this.authHeaders, "Content-Type": "application/json" },
      body: JSON.stringify({ slug, brand }),
    });
    const data = await res.json().catch(() => ({}));
    if (!res.ok) throw new Error(data?.message || data?.error || `HTTP ${res.status}`);
    return data as PhoneWebInstallStatus;
  }

  async rollbackPhoneWebApp(slug: string): Promise<PhoneWebInstallStatus> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/phone/projects/install/rollback`, {
      method: "POST",
      headers: { ...this.authHeaders, "Content-Type": "application/json" },
      body: JSON.stringify({ slug }),
    });
    const data = await res.json().catch(() => ({}));
    if (!res.ok) throw new Error(data?.error || `HTTP ${res.status}`);
    return data as PhoneWebInstallStatus;
  }

  async listPhoneWebEnrollments(slug: string): Promise<PhoneWebEnrollment[]> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/phone/projects/install/enrollments?slug=${encodeURIComponent(slug)}`, { headers: this.authHeaders });
    const data = await res.json().catch(() => ({}));
    if (!res.ok) throw new Error(data?.error || `HTTP ${res.status}`);
    return Array.isArray(data?.enrollments) ? data.enrollments : [];
  }

  async approvePhoneWebEnrollment(slug: string, code: string): Promise<void> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/phone/projects/install/approve`, {
      method: "POST",
      headers: { ...this.authHeaders, "Content-Type": "application/json" },
      body: JSON.stringify({ slug, code }),
    });
    const data = await res.json().catch(() => ({}));
    if (!res.ok) throw new Error(data?.error || `HTTP ${res.status}`);
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

  /** Exact authenticated route currently used for this agent. Public project
   * app paths can be appended to it (including the relay /d/<device> prefix). */
  get activeBaseUrl(): string | null {
    return this.baseUrl || null;
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
    const result = JSON.parse(text) as PhonePushResult;
    if (!result.appUrl?.startsWith("/apps/")) {
      throw new Error("The target accepted the bundle but did not prove a runnable Home Screen app. Update the target's Yaver agent, then retry.");
    }
    return result;
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
/** Per-node design overrides, keyed by the widget node id stamped via
 * data-ynode (e.g. "quickadd", "title"). reorderable/swipeDelete are END-USER
 * (runtime) affordances the builder opts into — "yaver draggable mode or not". */
export interface PhoneNodeUi {
  hidden?: boolean;
  marginTop?: number;
  title?: string;
  reorderable?: boolean;
  swipeDelete?: boolean;
  /** Turn a list node into a kanban board grouped by this column. End users
   * drag cards between columns; the move persists by updating the column. */
  board?: { groupBy: string };
}
/** The mini-figma design layer for an app: top-to-bottom widget order + per-node
 * overrides. Lives inside the app spec so it persists and ships in the bundle. */
export interface PhoneDesign {
  layout?: string[];
  ui?: Record<string, PhoneNodeUi>;
}
/** One structured edit to the design layer — the lingua franca shared by the
 * overlay, the inspector, AI prompting, and the relay/MCP path. */
export type PhoneDesignPatch =
  | { op: "set"; nodeId: string; props: PhoneNodeUi }
  | { op: "move"; nodeId: string; beforeId: string | null }
  | { op: "enable"; nodeId: string; affordance: "reorder" | "swipe" };
export interface PhoneAppSpec {
  summary?: string;
  primaryEntity?: string;
  screens?: PhoneScreenSpec[];
  brand?: PhoneAppBrand;
  design?: PhoneDesign;
}
export interface PhoneAppBrand {
  displayName?: string;
  icon?: "spark" | "check" | "note" | "grid" | "heart" | "bolt" | "leaf" | "rocket";
  palette?: string;
  primaryColor?: string;
  secondaryColor?: string;
}
export interface PhoneWebInstallStatus {
  published: boolean;
  appPath?: string;
  activeRelease?: string;
  previousRelease?: string;
  publishedAt?: string;
  canRollback: boolean;
  brand: PhoneAppBrand;
  pendingEnrollments: number;
  installations: number;
}
export interface PhoneWebEnrollment {
  id: string;
  code: string;
  createdAt: string;
}
export interface PhoneProject {
  slug: string;
  name: string;
  template?: string;
  dir: string;
  createdAt: string;
  updatedAt: string;
  managedGit?: ManagedGitProjectMeta | null;
  schema?: PhoneSchema | null;
  auth?: PhoneAuth | null;
  seed?: PhoneSeed | null;
  app?: PhoneAppSpec | null;
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
  appUrl: string;
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
  private relayServers: RelayServer[] = [];
  private topologyRefreshHook: (() => Promise<void>) | null = null;
  private listeners = new Set<() => void>();
  private clientUnsubs = new Map<string, Array<() => void>>();

  /** Get-or-create the per-device client. */
  get(deviceId: string): AgentClient {
    let c = this.clients.get(deviceId);
    if (!c) {
      c = new AgentClient();
      if (this.relayServers.length > 0) c.setRelayServers(this.relayServers.map((r) => ({ ...r })));
      if (this.topologyRefreshHook) c.setTopologyRefreshHook(this.topologyRefreshHook);
      this.clients.set(deviceId, c);
      this.clientUnsubs.set(deviceId, [c.on("connectionState", () => this.notify())]);
      this.notify();
    }
    return c;
  }

  /** Read an existing device client without creating one. Safe during React
   * render, where get()'s membership notification would be a state update. */
  peek(deviceId: string): AgentClient | undefined {
    return this.clients.get(deviceId);
  }

  /** True if a client already exists for this deviceId (i.e. it's been used). */
  has(deviceId: string): boolean {
    return this.clients.has(deviceId);
  }

  /** Currently-tracked device IDs (one per pool entry). */
  keys(): string[] {
    return [...this.clients.keys()];
  }

  /** IDs of every device whose pooled client is currently connected. */
  connectedDeviceIds(): string[] {
    const out: string[] = [];
    for (const [deviceId, client] of this.clients) {
      if (client.isConnected) out.push(deviceId);
    }
    return out;
  }

  /** Keep all existing and future pooled clients on the same relay topology as
   *  the focused singleton. Without this, background clients are born with an
   *  empty relay list and the web dashboard falls back to one-at-a-time
   *  connectivity even though the relay supports `/d/<deviceId>` multiplexing. */
  setRelayServersOnAll(servers: RelayServer[]): void {
    this.relayServers = servers.map((r) => ({ ...r }));
    for (const client of this.clients.values()) {
      client.setRelayServers(this.relayServers.map((r) => ({ ...r })));
    }
  }

  setTopologyRefreshHook(hook: (() => Promise<void>) | null): void {
    this.topologyRefreshHook = hook;
    for (const client of this.clients.values()) {
      client.setTopologyRefreshHook(hook);
    }
  }

  /** Drop one device from the pool, disconnecting it cleanly. */
  forget(deviceId: string): void {
    const c = this.clients.get(deviceId);
    if (!c) return;
    try { c.disconnect(); } catch { /* tearing down anyway */ }
    this.clients.delete(deviceId);
    const unsubs = this.clientUnsubs.get(deviceId) || [];
    for (const unsub of unsubs) {
      try { unsub(); } catch { /* ignore */ }
    }
    this.clientUnsubs.delete(deviceId);
    this.notify();
  }

  /** Disconnect every pool entry. Use on sign-out or before pool reset. */
  disconnectAll(): void {
    for (const c of this.clients.values()) {
      try { c.disconnect(); } catch { /* ignore */ }
    }
    this.clients.clear();
    for (const unsubs of this.clientUnsubs.values()) {
      for (const unsub of unsubs) {
        try { unsub(); } catch { /* ignore */ }
      }
    }
    this.clientUnsubs.clear();
    this.notify();
  }

  /** Subscribe to membership/state changes without coupling callers to
   *  individual AgentClient instances. */
  subscribe(callback: () => void): () => void {
    this.listeners.add(callback);
    return () => {
      this.listeners.delete(callback);
    };
  }

  private notify(): void {
    for (const callback of this.listeners) {
      try { callback(); } catch { /* ignore */ }
    }
  }
}

/** Process-wide pool shared across the dashboard. */
export const agentClientPool = new AgentClientPool();

/** Ask a device to update its agent WITHOUT needing to reach it.
 *
 *  Pairs with backend/convex/devices.ts::requestAgentUpdate. The request
 *  lands on the device's Convex row and the agent applies it on its next
 *  heartbeat. Unlike AgentClient.triggerAgentUpdate(), which POSTs to the
 *  box and so needs a live connection, this works on a box that is
 *  offline, asleep, or simply unreachable from this browser.
 *
 *  Deliberately a free function rather than an AgentClient method: the
 *  caller that needs it most is the one whose connect() just failed, and
 *  hanging it off a client instance would imply a connection state that
 *  is irrelevant here. All it needs is the user's Convex token.
 *
 *  `version` defaults to "latest" server-side. Note the agent can only
 *  install latest today — it rejects a pinned version rather than
 *  silently substituting one.
 */
export async function requestAgentUpdateViaConvex(
  token: string,
  deviceId: string,
  version?: string,
): Promise<{ requestedVersion: string }> {
  // Use the same-origin route first so browser CORS/preflight drift on the
  // Convex HTTP endpoint cannot strand web-only remote updates. The route
  // forwards server-side to the same Convex mutation.
  const res = await fetch(`/api/devices/request-update`, {
    method: "POST",
    headers: {
      Authorization: `Bearer ${token}`,
      "Content-Type": "application/json",
    },
    body: JSON.stringify({ deviceId, ...(version ? { version } : {}) }),
  });
  const data = await res.json().catch(() => ({}));
  if (!res.ok) {
    throw new Error(data?.error || `update request HTTP ${res.status}`);
  }
  return {
    requestedVersion: typeof data?.requestedVersion === "string" ? data.requestedVersion : "latest",
  };
}
