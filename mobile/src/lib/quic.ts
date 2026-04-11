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
export type ConnectionPath = "lan-beacon" | "lan-convex-ip" | "lan-beacon-upgrade" | "relay" | "cloudflare-tunnel" | null;

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
  // `reconnectAttempt` is the 1-indexed number of the attempt currently in
  // progress or just completed. 0 means idle (connected or never started).
  private reconnectTimer: ReturnType<typeof setTimeout> | null = null;
  private _reconnectAttempt = 0;
  private _reconnectStopped = false;
  private readonly baseBackoffMs = 1000;
  private readonly _maxReconnectAttempts = 15;

  private _connectionMode: ConnectionMode = null;
  private _connectionPath: ConnectionPath = null;
  private _networkType: string | null = null; // "wifi" | "cellular" | etc.
  private _connectingInProgress = false; // guard against concurrent attemptConnect calls
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
    this._reconnectStopped = false;
    this.setReconnectAttempt(1);

    await this.attemptConnect();
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
              const healthData = await res.json().catch(() => ({}));
              this.agentAuthExpired = !!healthData.authExpired;
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
              const healthData = await res.json().catch(() => ({}));
              this.agentAuthExpired = !!healthData.authExpired;
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
          } catch (e) {
            console.log("[QUIC] Relay", relay.id, "failed:", e);
          }
        }
      }

      if (!connected) {
        throw new Error("Could not reach agent (direct or via relay)");
      }

      this.setReconnectAttempt(0);
      this.setConnectionState("connected");
      this.startPolling();
      // Best-effort vault sync on connect
      this.syncVault();
    } catch {
      this.setConnectionState("error");
      this.scheduleReconnect();
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

    // Give up after max retries — attempt `_maxReconnectAttempts` just failed.
    if (this._reconnectAttempt >= this._maxReconnectAttempts) {
      console.log("[QUIC] Max reconnect attempts reached, giving up");
      this.setConnectionState("error");
      return;
    }

    // Exponential backoff indexed by the attempt that just failed (1, 2, 4, 8… capped).
    const delay = Math.min(
      this.baseBackoffMs * Math.pow(2, this._reconnectAttempt - 1),
      30_000
    );

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

  // ---- Auto Dev (M8) -----------------------------------------------------

  /** Fetch all registered Auto Dev loops. */
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

  /** Kick one iteration of a loop. Returns immediately — the kick
   *  runs in the background on the agent; poll autodevLoops() for
   *  status updates. */
  async autodevRun(name: string): Promise<{ ok: boolean; reason?: string }> {
    try {
      const res = await fetch(
        `${this.baseUrl}/autodev/loops/${encodeURIComponent(name)}/run`,
        { method: "POST", headers: this.authHeaders },
      );
      if (res.ok || res.status === 202) return { ok: true };
      return { ok: false, reason: `HTTP ${res.status}` };
    } catch (e: any) {
      return { ok: false, reason: e?.message ?? "network error" };
    }
  }

  /** Stop a loop — drops the STOP file and marks it stopped. */
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

  /** Read a loop's latest ideas.json. Returns an empty list if the
   *  ideas loop has not been run yet. */
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

  /** Set a loop's runtime inline prompt. Pass an empty string to
   *  clear the override. */
  async autodevSetPrompt(name: string, prompt: string): Promise<boolean> {
    try {
      const res = await fetch(
        `${this.baseUrl}/autodev/loops/${encodeURIComponent(name)}/prompt`,
        {
          method: "POST",
          headers: { ...this.authHeaders, "Content-Type": "application/json" },
          body: JSON.stringify({ prompt }),
        },
      );
      return res.ok;
    } catch {
      return false;
    }
  }

  /** Pick an idea by ID and stash its prompt as the target loop's
   *  inline prompt. Optionally kick immediately. */
  async autodevPickIdea(
    name: string,
    ideaId: string,
    opts: { source?: string; run?: boolean } = {},
  ): Promise<{ ok: boolean; title?: string; reason?: string }> {
    try {
      const res = await fetch(
        `${this.baseUrl}/autodev/loops/${encodeURIComponent(name)}/prompt/pick`,
        {
          method: "POST",
          headers: { ...this.authHeaders, "Content-Type": "application/json" },
          body: JSON.stringify({
            ideaId,
            source: opts.source,
            run: opts.run ?? false,
          }),
        },
      );
      if (!res.ok) return { ok: false, reason: `HTTP ${res.status}` };
      const data = await res.json();
      return { ok: true, title: data.title };
    } catch (e: any) {
      return { ok: false, reason: e?.message ?? "network error" };
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

/** Auto Dev loop row — wire shape of GET /autodev/loops. */
export interface AutoDevLoop {
  id: string;
  name: string;
  mode: "fix" | "auto-fix" | "develop" | "ideas";
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
  lastIterationAt?: string;
}

/** Shape of a loop's ideas.json — the runner writes this, the
 *  mobile tab reads it verbatim. */
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
  containerizeGuests: boolean;
  containerizeHost: boolean;
  docker: boolean;
  imageReady: boolean;
  imageName?: string;
  gpuAvailable?: boolean;
  networkMode?: string;
  readOnly?: boolean;
  cpuLimit?: string;
  memoryLimit?: string;
}

/** Container sandbox config fields for updates. */
export interface SandboxConfig {
  containerizeGuests: boolean;
  containerizeHost: boolean;
  cpuLimit: string;
  memoryLimit: string;
  networkMode: "host" | "bridge" | "none";
  readOnly: boolean;
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
