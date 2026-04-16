/**
 * Browser-compatible agent client for P2P communication with the desktop agent.
 *
 * Mirrors the mobile QuicClient API but runs in the browser using fetch().
 * Uses HTTP as the transport (same fallback path as mobile).
 * Supports relay-first connection strategy with direct fallback.
 */

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
  output: string[];
  resultText?: string;
  costUsd?: number;
  turns?: ConversationTurn[];
  createdAt: number;
  updatedAt: number;
  deviceName?: string;
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

export interface AgentInfo {
  hostname: string;
  version: string;
  workDir: string;
  voiceInputEnabled?: boolean;
  voiceProvider?: string;
  sttProvider?: string;
}

export interface VoiceStatus {
  voiceInputEnabled: boolean;
  s2sProvider?: string;
  s2sReady?: boolean;
  sttProvider?: string;
  sttReady?: boolean;
  providers?: Array<{ id: string; name: string; type: string; ready: boolean }>;
}

export interface Runner {
  id: string;
  name: string;
  installed: boolean;
  active: boolean;
  models?: string[];
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

class AgentClient {
  private host: string | null = null;
  private port: number | null = null;
  private token: string | null = null;
  private deviceId: string | null = null;
  private relayServers: RelayServer[] = [];
  private activeRelayUrl: string | null = null;
  private _connectionState: ConnectionState = "disconnected";
  private pollInterval: ReturnType<typeof setInterval> | null = null;

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

  // ── Connection lifecycle ───────────────────────────────────────────

  async connect(host: string, port: number, token: string, deviceId?: string): Promise<void> {
    this.host = host;
    this.port = port;
    this.token = token;
    this.deviceId = deviceId ?? null;
    this.activeRelayUrl = null;
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

  async sendTask(title: string, description: string): Promise<Task> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/tasks`, {
      method: "POST",
      headers: { ...this.authHeaders, "Content-Type": "application/json" },
      body: JSON.stringify({ title, description }),
    });
    if (!res.ok) throw new Error(`Failed to create task: ${res.status}`);
    const data = await res.json();
    return {
      id: data.taskId,
      title,
      description,
      status: data.status,
      output: [],
      createdAt: Date.now(),
      updatedAt: Date.now(),
    };
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

  async getRunners(): Promise<Runner[]> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/agent/runners`, {
      headers: this.authHeaders,
    });
    if (!res.ok) throw new Error(`Failed to get runners: ${res.status}`);
    const data = await res.json();
    return data.runners || [];
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
    const url = `${this.baseUrl}/tasks/${taskId}/output`;
    const es = new EventSource(url);
    // EventSource doesn't support custom headers, so we fall back to
    // polling for auth-protected endpoints. Use the existing poll mechanism
    // but also try SSE for unauthenticated or relay-proxied streams.
    es.onmessage = (e) => {
      if (e.data) onLine(e.data);
    };
    es.onerror = () => {
      es.close();
    };
    return () => es.close();
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

  private async attemptConnect(): Promise<void> {
    this.setConnectionState("connecting");
    this.activeRelayUrl = null;
    try {
      let connected = false;

      // Strategy: relay-first (more reliable across networks),
      // with direct fallback for same-network connections.

      // 1. Try relay servers first (when deviceId and relays are available)
      if (this.deviceId && this.relayServers.length > 0) {
        for (const relay of this.relayServers) {
          try {
            const relayDeviceUrl = `${relay.httpUrl}/d/${this.deviceId}`;
            const relayHeaders: Record<string, string> = { ...this.authHeaders };
            if (relay.password) relayHeaders["X-Relay-Password"] = relay.password;
            const res = await this.fetchWithTimeout(`${relayDeviceUrl}/health`, {
              headers: relayHeaders,
            }, 8000);
            if (res.ok) {
              this.activeRelayUrl = relay.httpUrl;
              this.activeRelayPassword = relay.password || null;
              connected = true;
              console.log("[AgentClient] Relay connection succeeded via", relay.id);
              break;
            }
          } catch (e) {
            console.log("[AgentClient] Relay", relay.id, "failed:", e);
          }
        }
      }

      // 2. Try direct connection as fallback
      if (!connected) {
        try {
          const directUrl = `http://${this.host}:${this.port}`;
          const res = await this.fetchWithTimeout(`${directUrl}/health`, {
            headers: this.authHeaders,
          }, 5000);
          if (res.ok) {
            this.activeRelayUrl = null;
            connected = true;
            console.log("[AgentClient] Direct connection succeeded");
          }
        } catch (e) {
          console.log("[AgentClient] Direct failed:", e);
        }
      }

      if (!connected) {
        throw new Error("Could not reach agent (direct or via relay)");
      }

      this.reconnectAttempt = 0;
      this.setConnectionState("connected");
      this.startPolling();
    } catch (err) {
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
    const lastOutputLen = new Map<string, number>();

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
          const prevLen = lastOutputLen.get(t.id) || 0;
          if (output.length > prevLen) {
            const newText = output.slice(prevLen);
            const lines = newText.split("\n").filter((l: string) => l);
            for (const line of lines) {
              this.emit("output", t.id, line);
            }
            lastOutputLen.set(t.id, output.length);
          }
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

  async inviteGuest(email: string): Promise<{ inviteCode: string; guestRegistered: boolean }> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/guests/invite`, {
      method: "POST",
      headers: { ...this.authHeaders, "Content-Type": "application/json" },
      body: JSON.stringify({ email }),
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
    if (!res.ok) return [];
    const data = await res.json();
    return data.projects ?? [];
  }

  async getProjectActions(query: string): Promise<{ project: string; path: string; actions: { label: string; target: string; type: string; framework?: string; platform?: string; command?: string }[] }> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/projects/actions?query=${encodeURIComponent(query)}`, { headers: this.authHeaders });
    if (!res.ok) throw new Error("Failed to get project actions");
    return res.json();
  }

  // ── Dev Server ────────────────────────────────────────────────────

  async getDevServerStatus(): Promise<{ running: boolean; framework?: string; workDir?: string; port?: number } | null> {
    this.assertConnected();
    try {
      const res = await fetch(`${this.baseUrl}/dev/status`, { headers: this.authHeaders });
      if (!res.ok) return null;
      return res.json();
    } catch { return null; }
  }

  async startDevServer(opts: { framework: string; workDir: string; platform?: string }): Promise<void> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/dev/start`, {
      method: "POST",
      headers: { ...this.authHeaders, "Content-Type": "application/json" },
      body: JSON.stringify(opts),
    });
    if (!res.ok) throw new Error("Failed to start dev server");
  }

  async stopDevServer(): Promise<void> {
    this.assertConnected();
    await fetch(`${this.baseUrl}/dev/stop`, { method: "POST", headers: this.authHeaders });
  }

  async reloadDevServer(): Promise<void> {
    this.assertConnected();
    await fetch(`${this.baseUrl}/dev/reload`, { method: "POST", headers: this.authHeaders });
  }

  get devPreviewUrl(): string | null {
    if (!this.baseUrl) return null;
    return `${this.baseUrl}/dev/`;
  }

  /** Get the SSE events URL for dev server live reload. */
  get devEventsUrl(): string | null {
    if (!this.baseUrl) return null;
    return `${this.baseUrl}/dev/events`;
  }

  /** Get auth headers for direct fetch calls (SSE, etc). */
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

  async infraSummary(): Promise<InfraSummary> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/infra/summary`, { headers: this.authHeaders });
    return res.json();
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

  async gitPull(workDir: string): Promise<{ ok: string; message: string }> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/git/pull?workDir=${encodeURIComponent(workDir)}`, {
      method: "POST",
      headers: this.authHeaders,
    });
    return res.json();
  }

  async gitStatus(workDir: string): Promise<unknown> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/git/status?workDir=${encodeURIComponent(workDir)}`, {
      headers: this.authHeaders,
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

/** Singleton client instance. */
export const agentClient = new AgentClient();
