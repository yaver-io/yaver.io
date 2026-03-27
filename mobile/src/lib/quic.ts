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
import { cacheTaskList, cacheTaskOutput, getCachedTaskList, getDeletedTaskIds } from "./storage";
import { beaconListener } from "./beacon";
import NetInfo from "@react-native-community/netinfo";
import type { BuildInfo, BuildSummary } from "./builds";

// ── Types ────────────────────────────────────────────────────────────

export type TaskStatus = "queued" | "running" | "completed" | "failed" | "stopped";

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

export interface Task {
  id: string;
  title: string;
  description: string;
  status: TaskStatus;
  output: string[];
  resultText?: string;    // Extracted clean result from Claude
  costUsd?: number;       // Total API cost in USD
  runnerId?: string;      // Which runner executed this task (claude, codex, aider)
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
  isDefault?: boolean;
}

export interface RunnerInfo {
  id: string;
  name: string;
  command: string;
  installed: boolean;
  isDefault: boolean;
  models: ModelInfo[];
}

export interface AgentStatus {
  runner: {
    id: string;
    name: string;
    command: string;
    installed: boolean;
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
export type ConnectionPath = "lan-beacon" | "lan-convex-ip" | "relay" | "cloudflare-tunnel" | null;

export type OutputCallback = (taskId: string, line: string) => void;
export type ConnectionStateCallback = (state: ConnectionState) => void;
export type ConnectionModeCallback = (mode: ConnectionMode) => void;

type EventMap = {
  output: OutputCallback;
  connectionState: ConnectionStateCallback;
  connectionMode: ConnectionModeCallback;
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

export class QuicClient {
  private host: string | null = null;
  private port: number | null = null;
  private token: string | null = null;
  private deviceId: string | null = null;
  private relayServers: RelayServer[] = [];  // all available relay servers
  private activeRelayUrl: string | null = null; // currently working relay base URL
  private activeRelayPassword: string | null = null; // password for the active relay (if any)
  private tunnelServers: TunnelServer[] = [];  // Cloudflare Tunnel endpoints
  private _tunnelUrl: string | null = null;
  private _tunnelHeaders: Record<string, string> = {};
  private _forceRelay = false; // default to direct-first — try LAN/local before relay
  private _connectionState: ConnectionState = "disconnected";
  private pollInterval: ReturnType<typeof setInterval> | null = null;
  private heartbeatInterval: ReturnType<typeof setInterval> | null = null;
  private _consecutiveHeartbeatFailures = 0;

  // Reconnection — max 15 retries, then give up (needs headroom for network transitions)
  private reconnectTimer: ReturnType<typeof setTimeout> | null = null;
  reconnectAttempt = 0;
  private readonly baseBackoffMs = 1000;
  private readonly maxReconnectAttempts = 15;

  private _connectionMode: ConnectionMode = null;
  private _connectionPath: ConnectionPath = null;
  private _networkType: string | null = null; // "wifi" | "cellular" | etc.
  private _connectingInProgress = false; // guard against concurrent attemptConnect calls

  // Relay health tracking
  private _relayHealth: Map<string, { ok: boolean; latencyMs: number; lastChecked: number }> = new Map();

  // Event listeners
  private listeners: { [K in EventName]: Array<EventMap[K]> } = {
    output: [],
    connectionState: [],
    connectionMode: [],
  };

  /** Set relay servers fetched from platform config. */
  setRelayServers(servers: RelayServer[]): void {
    this.relayServers = servers.sort((a, b) => a.priority - b.priority);
  }

  /** Set Cloudflare Tunnel endpoints. */
  setTunnelServers(servers: TunnelServer[]): void {
    this.tunnelServers = servers.sort((a, b) => a.priority - b.priority);
  }

  getTunnelServers(): TunnelServer[] {
    return [...this.tunnelServers];
  }

  get tunnelServerCount(): number {
    return this.tunnelServers.length;
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
  async connect(host: string, port: number, token: string, deviceId: string): Promise<void> {
    this.host = host;
    this.port = port;
    this.token = token;
    this.deviceId = deviceId;
    this.activeRelayUrl = null;
    this.activeRelayPassword = null;
    this.reconnectAttempt = 0;

    await this.attemptConnect();
  }

  /** Close the connection and stop all timers. */
  disconnect(): void {
    this.clearTimers();
    this.setConnectionState("disconnected");
    this.setConnectionMode(null);
    this.host = null;
    this.port = null;
    this.token = null;
    this.deviceId = null;
    this.activeRelayUrl = null;
    this.activeRelayPassword = null;
  }

  // ── Task API ───────────────────────────────────────────────────────

  /** Send a new task to the desktop agent. */
  async sendTask(title: string, description: string, model?: string, runner?: string, customCommand?: string, speechContext?: { inputFromSpeech?: boolean; sttProvider?: string; ttsEnabled?: boolean; ttsProvider?: string; verbosity?: number }, images?: ImageAttachment[]): Promise<Task> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/tasks`, {
      method: "POST",
      headers: { ...this.authHeaders, "Content-Type": "application/json" },
      body: JSON.stringify({
        title,
        description,
        source: "mobile",
        ...(model ? { model } : {}),
        ...(runner ? { runner } : {}),
        ...(customCommand ? { customCommand } : {}),
        ...(speechContext ? { speechContext } : {}),
        ...(images?.length ? { images } : {}),
      }),
    });
    if (!res.ok) {
      let msg = `Failed to create task: ${res.status}`;
      try {
        const errData = await res.json();
        if (errData.error) msg = errData.error;
      } catch {}
      throw new Error(msg);
    }
    const data = await res.json();
    // Agent returns { ok, taskId, status, runnerId }
    return {
      id: data.taskId,
      title,
      description,
      status: data.status,
      runnerId: data.runnerId,
      output: [],
      createdAt: Date.now(),
      updatedAt: Date.now(),
    };
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
        turns: t.turns || undefined,
        tmuxSession: t.tmuxSession || undefined,
        isAdopted: t.isAdopted || false,
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

  // ── Projects (discovery + switching) ────────────────────────────

  /** List discovered projects on the machine. */
  async listProjects(): Promise<{ name: string; path: string; branch?: string; framework?: string; gitRemote?: string; tags?: string[] }[]> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/projects`, {
      headers: this.authHeaders,
    });
    if (!res.ok) throw new Error(`Failed to list projects: ${res.status}`);
    const data = await res.json();
    return data.projects ?? [];
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

  /** Execute a vibing suggestion as a task. */
  async executeVibingSuggestion(prompt: string, projectPath: string): Promise<{ taskId: string }> {
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

  /** Start a new build on the connected agent. */
  async startBuild(platform: string, workDir?: string): Promise<BuildInfo> {
    this.assertConnected();
    const resp = await this.fetchWithTimeout(`${this.baseUrl}/builds`, {
      method: "POST",
      headers: { ...this.authHeaders, "Content-Type": "application/json" },
      body: JSON.stringify({ platform, workDir: workDir || "" }),
    }, 10_000);
    if (!resp.ok) throw new Error(`Start build failed: ${resp.status}`);
    return resp.json();
  }

  /** Cancel a running build. */
  async cancelBuild(id: string): Promise<void> {
    await this.fetchWithTimeout(`${this.baseUrl}/builds/${id}`, {
      method: "DELETE",
      headers: this.authHeaders,
    }, 10_000);
  }

  /** Start a build targeting the current mobile platform. */
  async startBuildForMyPlatform(buildSystem: 'flutter' | 'gradle' | 'rn' | 'expo', workDir?: string): Promise<BuildInfo> {
    const platformMap: Record<string, Record<string, string>> = {
      flutter: { ios: 'flutter-ipa', android: 'flutter-apk' },
      gradle: { android: 'gradle-apk' },
      rn: { ios: 'rn-ios', android: 'rn-android' },
      expo: { ios: 'expo-ios', android: 'expo-android' },
    };
    const platform = platformMap[buildSystem]?.[Platform.OS];
    if (!platform) throw new Error(`${buildSystem} does not support ${Platform.OS}`);
    return this.startBuild(platform, workDir);
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
  async getHealthTargets(): Promise<any[]> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/healthmon`, { headers: this.authHeaders });
    if (!res.ok) return [];
    return res.json();
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
  async checkHealthTarget(id: string): Promise<any> {
    this.assertConnected();
    const res = await fetch(`${this.baseUrl}/healthmon/${id}/check`, {
      method: 'POST',
      headers: this.authHeaders,
    });
    if (!res.ok) throw new Error(`Failed to check health target: ${res.status}`);
    return res.json();
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

  // ── EventEmitter ───────────────────────────────────────────────────

  /** Register a listener for output lines. Returns an unsubscribe function. */
  on(event: "output", callback: OutputCallback): () => void;
  /** Register a listener for connection state changes. */
  on(event: "connectionState", callback: ConnectionStateCallback): () => void;
  /** Register a listener for connection mode changes (direct vs relay). */
  on(event: "connectionMode", callback: ConnectionModeCallback): () => void;
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
    this.reconnectAttempt = 0;
    this.attemptConnect().catch(() => {});
  }

  // ── Connection + reconnection ──────────────────────────────────────

  /** Create a fetch with a manual timeout (AbortSignal.timeout may not exist in Hermes). */
  private fetchWithTimeout(url: string, opts: RequestInit, timeoutMs: number): Promise<Response> {
    const controller = new AbortController();
    const timer = setTimeout(() => controller.abort(), timeoutMs);
    return fetch(url, { ...opts, signal: controller.signal }).finally(() => clearTimeout(timer));
  }

  /** Check if an IP address is private (192.168.x.x, 10.x.x.x, 172.16-31.x.x). */
  private isPrivateIP(host: string): boolean {
    return /^(192\.168\.|10\.|172\.(1[6-9]|2\d|3[01])\.)/.test(host);
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
      // On cellular: skip direct, go straight to relay.

      // 1. Try direct connection first (LAN beacon IP or Convex-known IP)
      if (isWifi && !this._forceRelay) {
        // 1a. Check if device is LAN-discovered via beacon (freshest IP)
        const lanInfo = this.deviceId ? beaconListener.getLocalIP(this.deviceId) : null;
        if (lanInfo) {
          try {
            const directUrl = `http://${lanInfo.ip}:${lanInfo.port}`;
            console.log("[QUIC] Trying LAN-discovered direct:", directUrl);
            const res = await this.fetchWithTimeout(`${directUrl}/health`, {
              headers: this.authHeaders,
            }, 2000);
            if (res.ok) {
              this.activeRelayUrl = null;
              this.setConnectionMode("direct");
              this._connectionPath = "lan-beacon";
              connected = true;
              console.log("[QUIC] Direct connection via LAN beacon succeeded");
            }
          } catch (e) {
            console.log("[QUIC] LAN beacon direct failed:", e);
          }
        }

        // 1b. Try Convex-known IP (if beacon didn't work and IP is private)
        if (!connected && this.host && this.isPrivateIP(this.host)) {
          try {
            const directUrl = `http://${this.host}:${this.port}`;
            console.log("[QUIC] Trying Convex-known direct:", directUrl);
            const res = await this.fetchWithTimeout(`${directUrl}/health`, {
              headers: this.authHeaders,
            }, 2000);
            if (res.ok) {
              this.activeRelayUrl = null;
              this.setConnectionMode("direct");
              this._connectionPath = "lan-convex-ip";
              connected = true;
              console.log("[QUIC] Direct connection via Convex IP succeeded");
            }
          } catch (e) {
            console.log("[QUIC] Convex IP direct failed:", e);
          }
        }
      }

      // 2. Try Cloudflare Tunnels (works through any firewall)
      if (!connected && this.tunnelServers.length > 0) {
        console.log("[QUIC] Trying", this.tunnelServers.length, "Cloudflare Tunnel(s)");
        for (const tunnel of this.tunnelServers) {
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
              this.activeRelayUrl = relay.httpUrl;
              this.activeRelayPassword = relay.password || null;
              this.setConnectionMode("relay");
              this._connectionPath = "relay";
              connected = true;
              console.log("[QUIC] Relay connection succeeded via", relay.id);
              break;
            }
          } catch (e) {
            console.log("[QUIC] Relay", relay.id, "failed:", e);
          }
        }
      }

      if (!connected) {
        throw new Error("Could not reach agent (direct or via relay)");
      }

      this.reconnectAttempt = 0;
      this.setConnectionState("connected");
      this.startPolling();
      // Best-effort vault sync on connect
      this.syncVault();
    } catch (err) {
      this.setConnectionState("error");
      this.scheduleReconnect();
      // Only throw on the initial connect call (attempt 0)
      if (this.reconnectAttempt === 0) {
        this.reconnectAttempt = 1;
        throw err;
      }
    }
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

  private scheduleReconnect(): void {
    if (!this.host || !this.port || !this.token) return;

    // Give up after max retries
    if (this.reconnectAttempt >= this.maxReconnectAttempts) {
      console.log("[QUIC] Max reconnect attempts reached, giving up");
      this.setConnectionState("error");
      return;
    }

    const delay = Math.min(
      this.baseBackoffMs * Math.pow(2, this.reconnectAttempt),
      30_000
    );
    this.reconnectAttempt++;

    this.reconnectTimer = setTimeout(() => {
      this.reconnectTimer = null;
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

  /** Start a dev server on the agent. */
  async startDevServer(opts: { framework?: string; workDir?: string; platform?: string; port?: number }): Promise<DevServerStatus | null> {
    try {
      const res = await fetch(`${this.baseUrl}/dev/start`, {
        method: "POST",
        headers: { ...this.authHeaders, "Content-Type": "application/json" },
        body: JSON.stringify(opts),
      });
      if (!res.ok) return null;
      return await res.json();
    } catch { return null; }
  }

  /** Stop the running dev server. */
  async stopDevServer(): Promise<boolean> {
    try {
      const res = await fetch(`${this.baseUrl}/dev/stop`, {
        method: "POST",
        headers: this.authHeaders,
      });
      return res.ok;
    } catch { return false; }
  }

  /** Trigger hot reload on the running dev server. */
  async reloadDevServer(): Promise<boolean> {
    try {
      const res = await fetch(`${this.baseUrl}/dev/reload`, {
        method: "POST",
        headers: this.authHeaders,
      });
      return res.ok;
    } catch { return false; }
  }

  /** Get the full URL for the dev server bundle (through relay if needed). */
  getDevServerBundleUrl(bundlePath: string): string {
    return `${this.baseUrl}${bundlePath}`;
  }
}

/** Dev server status returned by the agent. */
export interface DevServerStatus {
  framework: string;
  running: boolean;
  port: number;
  bundleUrl: string;
  deepLink?: string;
  devMode?: string;
  startedAt?: string;
  error?: string;
  pid?: number;
  workDir?: string;
  hotReload: boolean;
}

/** Singleton client instance. */
export const quicClient = new QuicClient();
