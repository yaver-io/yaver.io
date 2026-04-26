// WebClient — pure-Node surrogate of the Yaver web dashboard.
//
// Same contract as web/lib/agent-client.ts but with the browser-only
// bits (localStorage, window.open, iframe, postMessage, SSE via
// EventSource) swapped for Node-friendly equivalents. The HTTP +
// Convex surface is identical, so anything the web UI can do to a
// Yaver agent, this client can do from a script / CI runner / MCP
// tool call.
//
// Focus vs. yaver-mobile-headless:
//   - mobile = beacon discovery, Apple auth, phone-project mgmt,
//     bundle push.
//   - web    = dev-server preview (Vite / Next / Expo / Flutter),
//     webview reload URL composition, vibing task dispatch,
//     reconnect-and-fix recovery, /settings/repair-relay.
//
// If you add a method here, keep the name identical to the web
// agentClient so `grep -R methodName` across web/ and web-headless/
// lands on the same two files.

const DEFAULT_CONVEX_URL =
  process.env.YAVER_CONVEX_URL ||
  "https://perceptive-minnow-557.eu-west-1.convex.site";

export type DevServerFramework = "expo" | "react-native" | "flutter" | "vite" | "nextjs" | "next";

export interface RelayServer {
  id: string;
  httpUrl: string;
  quicAddr?: string;
  region?: string;
  priority: number;
  password?: string;
}

export interface RelayConfig {
  relayServers: RelayServer[];
  userRelayPassword?: string;
}

export interface DeviceRow {
  // Convex /devices/list emits `deviceId` (not `id`) and `isOnline`
  // (not `online`). Don't add aliases — the agent + web client must
  // match the actual JSON shape, not what feels nice in TS.
  deviceId: string;
  name: string;
  host?: string;
  port?: number;
  isOnline?: boolean;
  isGuest?: boolean;
  hostName?: string;
  hostEmail?: string;
  lastHeartbeat?: number;
  lanIps?: string[];
  publicUrl?: string;
  tunnelUrl?: string;
}

export interface ConnectDiagnostic {
  path: "relay" | "direct" | "tunnel";
  relayId?: string;
  ok: boolean;
  status?: number;
  error?: string;
  durationMs: number;
  authExpired?: boolean;
}

export interface ConnectResult {
  ok: boolean;
  via: "relay" | "direct" | "tunnel" | null;
  relayId?: string;
  diagnostics: ConnectDiagnostic[];
}

export type DevServerKind = "web" | "mobile" | "hybrid";

export interface DevServerStatus {
  running: boolean;
  framework?: string;
  kind?: DevServerKind;
  workDir?: string;
  port?: number;
  targetDeviceName?: string;
}

export interface StartDevServerOpts {
  framework?: DevServerFramework;
  workDir?: string;
  app?: string;
  root?: string;
  /** Surface the caller is starting *from*. The agent uses this to
   *  gate mobile-only apps out of the Web Reload tab and web-only
   *  apps out of Hot Reload. Omit for back-compat (no gate). */
  surface?: "web-reload" | "hot-reload";
  platform?: "web" | "ios" | "android";
  targetDeviceId?: string;
  targetDeviceName?: string;
}

/** One app from the monorepo workspace manifest, as returned by
 *  /workspace/apps. Mirrors WorkspaceAppView in the agent. */
export interface WorkspaceApp {
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
  apps?: WorkspaceApp[];
}

export interface Task {
  id: string;
  title: string;
  description?: string;
  status: "running" | "completed" | "failed" | "queued" | string;
  costUsd?: number;
  createdAt?: number;
  updatedAt?: number;
}

/** One event on the Yaver P2P bus. Mirrors desktop/agent/bus.go. */
export interface BusEvent {
  id: string;
  topic: string;
  publisher: string;
  publishedAt: number;
  ttl?: number;
  qos: 0 | 1;
  payload?: unknown;
}

export interface BusStatus {
  enabled: boolean;
  bus?: {
    deviceId: string;
    userId: string;
    running: boolean;
    published: number;
    received: number;
    dupes: number;
    transports: string[];
    retainedCount: number;
    subscriptionCount: number;
  };
  leader?: {
    self: string;
    leader: string;
    amLeader: boolean;
    alivePeers: Array<{ deviceId: string; hostname?: string; lastSeenAt?: number }>;
  };
}

export interface CreateTaskOpts {
  title: string;
  description: string;
  runner?: string;
  model?: string;
  customCommand?: string;
  projectName?: string;
  workDir?: string;
}

export interface WebClientOptions {
  convexUrl?: string;
  token?: string;
  /** Optional override — skip relay probing and talk directly. */
  agentBaseUrl?: string;
  /** Seed relay servers (e.g. from a saved config). When absent, we
   *  read `/config` during connect(). */
  relayServers?: RelayServer[];
  /** Per-user relay password override. When absent, we read it from
   *  `/settings` with the supplied token during connect(). */
  userRelayPassword?: string;
  /** Tunnel URLs (Cloudflare Access / ngrok / Tailscale funnel) to
   *  probe as "direct" candidates before falling back to relay. */
  tunnelUrls?: string[];
}

export class WebClient {
  readonly convexUrl: string;
  private token: string | null;
  private relayServers: RelayServer[];
  private userRelayPassword: string | null;
  private tunnelUrls: string[];
  private deviceId: string | null = null;
  private activeRelayUrl: string | null = null;
  private activeRelayPassword: string | null = null;
  private activeTunnelUrl: string | null = null;
  private directHost: string | null = null;
  private directPort: number | null = null;

  constructor(opts: WebClientOptions = {}) {
    this.convexUrl = (opts.convexUrl || DEFAULT_CONVEX_URL).replace(/\/+$/, "");
    this.token = opts.token || null;
    this.relayServers = [...(opts.relayServers || [])].sort(
      (a, b) => a.priority - b.priority,
    );
    this.userRelayPassword = opts.userRelayPassword || null;
    this.tunnelUrls = (opts.tunnelUrls || [])
      .map((u) => u.replace(/\/+$/, ""))
      .filter(Boolean);
    if (opts.agentBaseUrl) {
      const u = new URL(opts.agentBaseUrl);
      this.directHost = u.hostname;
      this.directPort = Number(u.port || (u.protocol === "https:" ? 443 : 80));
    }
  }

  get isAuthed() {
    return !!this.token;
  }

  get connectionMode(): "relay" | "tunnel" | "direct" | "none" {
    if (this.activeRelayUrl) return "relay";
    if (this.activeTunnelUrl) return "tunnel";
    if (this.directHost) return "direct";
    return "none";
  }

  /** Base URL the agent is reachable at, or null if we haven't
   *  completed a probe yet. */
  get baseUrl(): string | null {
    if (this.activeRelayUrl && this.deviceId) {
      return `${this.activeRelayUrl}/d/${this.deviceId}`;
    }
    if (this.activeTunnelUrl) return this.activeTunnelUrl;
    if (this.directHost) {
      const protocol = this.directPort === 443 ? "https" : "http";
      return `${protocol}://${this.directHost}:${this.directPort}`;
    }
    return null;
  }

  // ── Auth ──────────────────────────────────────────────────────────

  setToken(token: string | null) {
    this.token = token;
  }

  /** OAuth-free email/password signup against Convex. Returns the
   *  bearer token on success. Mirrors web's /auth/signup flow. */
  async signUp(opts: { email: string; password: string; fullName?: string }): Promise<string> {
    const res = await this.convexCall("POST", "/auth/signup", {
      email: opts.email,
      password: opts.password,
      fullName: opts.fullName || "",
    });
    if (!res.token) throw new Error(`signup: no token in response`);
    this.token = res.token;
    return res.token;
  }

  async signIn(opts: { email: string; password: string }): Promise<string> {
    const res = await this.convexCall("POST", "/auth/login", {
      email: opts.email,
      password: opts.password,
    });
    if (!res.token) throw new Error(`login: no token in response`);
    this.token = res.token;
    return res.token;
  }

  async signOut(): Promise<void> {
    if (!this.token) return;
    try {
      await this.convexCall("POST", "/auth/logout");
    } catch {
      /* ignore */
    }
    this.token = null;
  }

  async whoami(): Promise<{ id: string; email?: string; fullName?: string } | null> {
    if (!this.token) return null;
    try {
      return await this.convexCall("GET", "/auth/me");
    } catch {
      return null;
    }
  }

  // ── Device + relay discovery ───────────────────────────────────

  async listDevices(): Promise<DeviceRow[]> {
    const res = await this.convexCall("GET", "/devices/list");
    return (res?.devices || []) as DeviceRow[];
  }

  async refreshRelayConfig(): Promise<RelayConfig> {
    const cfg = await this.convexCall("GET", "/config");
    this.relayServers = (cfg?.relayServers || []).sort(
      (a: RelayServer, b: RelayServer) => a.priority - b.priority,
    );

    if (this.token) {
      try {
        const sd = await this.convexCall("GET", "/settings");
        const pw = sd?.settings?.relayPassword || sd?.relayPassword;
        if (pw) {
          this.userRelayPassword = pw;
          // Per-user password overrides whatever was in platform config.
          this.relayServers = this.relayServers.map((r) => ({ ...r, password: pw }));
        }
      } catch {
        /* non-fatal */
      }
    }

    return {
      relayServers: this.relayServers,
      userRelayPassword: this.userRelayPassword || undefined,
    };
  }

  /** The recovery step we built for the web dashboard: if the iframe
   *  preview keeps hitting "invalid relay password", re-sync the
   *  user's userSettings.relayPassword with the current platform
   *  default. Returns the repair result from Convex. */
  async repairRelay(): Promise<{ repaired: boolean; reason: string }> {
    if (!this.token) throw new Error("repairRelay: not authed");
    const body = await this.convexCall("POST", "/settings/repair-relay");
    // Also re-read config so the next probe uses the freshly-synced password.
    await this.refreshRelayConfig();
    return { repaired: !!body?.repaired, reason: body?.reason || "" };
  }

  // ── Connection (relay-first, tunnel, direct) ───────────────────

  /** Connect to a device by id. Mirrors agentClient.connect in web. */
  async connect(deviceId: string): Promise<ConnectResult> {
    this.deviceId = deviceId;
    this.activeRelayUrl = null;
    this.activeRelayPassword = null;
    this.activeTunnelUrl = null;
    const diagnostics: ConnectDiagnostic[] = [];

    if (this.relayServers.length === 0) {
      await this.refreshRelayConfig();
    }

    // 1. Relay servers.
    for (const relay of this.relayServers) {
      const url = `${relay.httpUrl}/d/${deviceId}`;
      const headers: Record<string, string> = this.authHeaders();
      if (relay.password) headers["X-Relay-Password"] = relay.password;
      const diag = await probeHealth(url, headers, 8000, "relay", relay.id);
      diagnostics.push(diag);
      if (diag.ok) {
        this.activeRelayUrl = relay.httpUrl;
        this.activeRelayPassword = relay.password || null;
        return { ok: true, via: "relay", relayId: relay.id, diagnostics };
      }
    }

    // 2. Tunnel candidates.
    for (const tunnelUrl of this.tunnelUrls) {
      const diag = await probeHealth(tunnelUrl, this.authHeaders(), 8000, "tunnel");
      diagnostics.push(diag);
      if (diag.ok) {
        this.activeTunnelUrl = tunnelUrl;
        return { ok: true, via: "tunnel", diagnostics };
      }
    }

    // 3. Direct — requires directHost set via agentBaseUrl.
    if (this.directHost) {
      const directUrl = `http://${this.directHost}:${this.directPort}`;
      const diag = await probeHealth(directUrl, this.authHeaders(), 5000, "direct");
      diagnostics.push(diag);
      if (diag.ok) {
        return { ok: true, via: "direct", diagnostics };
      }
    }

    return { ok: false, via: null, diagnostics };
  }

  /** Hand the box a fresh owner session without requiring physical access.
   *  Mirrors web/lib/agent-client.ts::reauthAgent. Drives the agent's
   *  /auth/recover endpoint directly through the relay (and direct LAN
   *  as a last resort). Tries `mode: "direct"` first; falls back to
   *  `mode: "pair"` + /auth/pair/submit for older agents that reject
   *  direct mode.
   *
   *  Note: there is NO Convex /auth/reauth-agent endpoint — the earlier
   *  implementation that called it always failed with "No matching
   *  routes found". The real path is agent-side.
   */
  async reauthAgent(opts: { deviceId: string }): Promise<{
    ok: boolean;
    via?: string;
    mode?: "direct" | "pair";
    error?: string;
    diagnostics: Array<{ path: string; step: string; ok: boolean; status?: number; error?: string }>;
  }> {
    if (!this.token) throw new Error("reauthAgent: not authed");
    if (this.relayServers.length === 0) {
      await this.refreshRelayConfig().catch(() => {});
    }

    const diagnostics: Array<{ path: string; step: string; ok: boolean; status?: number; error?: string }> = [];

    const tryOne = async (
      pathLabel: string,
      baseUrl: string,
      relayPassword?: string,
    ) => {
      const headers: Record<string, string> = {
        Authorization: `Bearer ${this.token}`,
        "Content-Type": "application/json",
      };
      if (relayPassword) headers["X-Relay-Password"] = relayPassword;

      const fetchTimeout = async (url: string, init: RequestInit, timeoutMs: number) => {
        const ctrl = new AbortController();
        const t = setTimeout(() => ctrl.abort(), timeoutMs);
        try {
          return await fetch(url, { ...init, signal: ctrl.signal });
        } finally {
          clearTimeout(t);
        }
      };

      // 1. Direct mode — agent saves the caller's bearer as its new token
      //    after Convex /devices/owner-by-hardware confirms ownership.
      const directDiag = { path: pathLabel, step: "direct", ok: false } as {
        path: string; step: string; ok: boolean; status?: number; error?: string;
      };
      try {
        const res = await fetchTimeout(
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
        try { body = await res.clone().json(); } catch { /* */ }
        directDiag.error = (body && body.error) || `HTTP ${res.status}`;
        diagnostics.push(directDiag);
        const msg = String(directDiag.error || "").toLowerCase();
        const modeUnsupported =
          (res.status === 400 || res.status === 501) &&
          (msg.includes("mode must") || msg.includes("direct") || msg.includes("invalid mode"));
        if (!modeUnsupported) return null;
      } catch (e: any) {
        directDiag.error = e?.message || "network error";
        diagnostics.push(directDiag);
        return null;
      }

      // 2. Pair-mode fallback for older agents.
      const pairDiag = { path: pathLabel, step: "pair", ok: false } as {
        path: string; step: string; ok: boolean; status?: number; error?: string;
      };
      try {
        const res = await fetchTimeout(
          `${baseUrl}/auth/recover`,
          { method: "POST", headers, body: JSON.stringify({ mode: "pair" }) },
          10_000,
        );
        pairDiag.status = res.status;
        if (!res.ok) {
          let body: any = null;
          try { body = await res.clone().json(); } catch { /* */ }
          pairDiag.error = (body && body.error) || `HTTP ${res.status}`;
          diagnostics.push(pairDiag);
          return null;
        }
        const pairInfo: any = await res.json();
        const pairCode = pairInfo?.pairCode;
        if (!pairCode) {
          pairDiag.error = "agent did not return pairCode";
          diagnostics.push(pairDiag);
          return null;
        }
        const submitRes = await fetchTimeout(
          `${baseUrl}/auth/pair/submit?code=${encodeURIComponent(pairCode)}`,
          {
            method: "POST",
            headers,
            body: JSON.stringify({ token: this.token, convexSiteUrl: this.convexUrl }),
          },
          10_000,
        );
        pairDiag.status = submitRes.status;
        if (!submitRes.ok) {
          let body: any = null;
          try { body = await submitRes.clone().json(); } catch { /* */ }
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

    // Relays first (priority order).
    for (const relay of this.relayServers) {
      const base = `${relay.httpUrl.replace(/\/+$/, "")}/d/${opts.deviceId}`;
      const result = await tryOne(`relay · ${relay.id}`, base, relay.password || undefined);
      if (result?.ok) return { ok: true, mode: result.mode, via: result.via, diagnostics };
    }
    // Tunnel candidates.
    for (const tunnelUrl of this.tunnelUrls) {
      const result = await tryOne(`tunnel`, tunnelUrl.replace(/\/+$/, ""));
      if (result?.ok) return { ok: true, mode: result.mode, via: result.via, diagnostics };
    }
    // Direct LAN last.
    if (this.directHost) {
      const protocol = this.directPort === 443 ? "https" : "http";
      const base = `${protocol}://${this.directHost}:${this.directPort}`;
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

  disconnect() {
    this.deviceId = null;
    this.activeRelayUrl = null;
    this.activeRelayPassword = null;
    this.activeTunnelUrl = null;
  }

  // ── Dev server (Hot Reload + Web Reload) ───────────────────────

  async getDevServerStatus(): Promise<DevServerStatus | null> {
    const res = await this.agentFetch("GET", "/dev/status");
    return res;
  }

  async startDevServer(opts: StartDevServerOpts): Promise<void> {
    await this.agentFetch("POST", "/dev/start", opts);
  }

  async reloadDevServer(): Promise<{ ok: boolean; framework?: string }> {
    return this.agentFetch("POST", "/dev/reload");
  }

  async stopDevServer(): Promise<void> {
    await this.agentFetch("POST", "/dev/stop");
  }

  // ── Workspace manifest (monorepo) ──────────────────────────────

  /** Full manifest + resolved root path. Used by clients that need
   *  the whole picture (shared env, primary device hint, etc.). */
  async getWorkspace(): Promise<WorkspaceResponse> {
    return this.agentFetch("GET", "/workspace");
  }

  /** Apps projection, optionally filtered by kind ("web", "mobile",
   *  "hybrid", or comma-separated). Default returns all apps. */
  async getWorkspaceApps(kind?: string | string[]): Promise<WorkspaceApp[]> {
    const filter = Array.isArray(kind) ? kind.join(",") : kind;
    const qs = filter ? `?kind=${encodeURIComponent(filter)}` : "";
    const res = await this.agentFetch("GET", `/workspace/apps${qs}`);
    return (res?.apps || []) as WorkspaceApp[];
  }

  /** URL the web dashboard iframes into. Returns null when we're
   *  routing via relay but haven't populated activeRelayPassword yet
   *  — matches the fix we put into web/lib/agent-client.ts. */
  get devPreviewUrl(): string | null {
    if (!this.baseUrl) return null;
    if (this.activeRelayUrl) {
      if (!this.activeRelayPassword) return null;
      return `${this.baseUrl}/dev/?__rp=${encodeURIComponent(this.activeRelayPassword)}`;
    }
    return `${this.baseUrl}/dev/`;
  }

  get devEventsUrl(): string | null {
    if (!this.baseUrl) return null;
    return `${this.baseUrl}/dev/events`;
  }

  /** Post a hot-reload signal via /dev/reload-app. Matches mobile
   *  app's shake "reload app" gesture. mode="dev" → JS HMR,
   *  mode="bundle" → push a fresh Hermes bundle. */
  async reloadApp(mode: "dev" | "bundle" = "dev"): Promise<void> {
    await this.agentFetch("POST", "/dev/reload-app", { mode });
  }

  // ── Tasks (vibing composer in the web UI) ──────────────────────

  async createTask(opts: CreateTaskOpts): Promise<Task> {
    const res = await this.agentFetch("POST", "/tasks", {
      title: opts.title,
      description: opts.description,
      runner: opts.runner || "",
      model: opts.model || "",
      customCommand: opts.customCommand || "",
      projectName: opts.projectName || "",
      workDir: opts.workDir || "",
      source: "web-headless",
    });
    // Agent returns { taskId }, fetch the full row.
    if (res?.taskId) {
      return this.getTask(res.taskId);
    }
    return res as Task;
  }

  async getTask(taskId: string): Promise<Task> {
    const res = await this.agentFetch("GET", `/tasks/${encodeURIComponent(taskId)}`);
    return res as Task;
  }

  async listTasks(limit?: number): Promise<Task[]> {
    const path = limit ? `/tasks?limit=${limit}` : "/tasks";
    const res = await this.agentFetch("GET", path);
    return res?.tasks || [];
  }

  async continueTask(taskId: string, prompt: string): Promise<void> {
    await this.agentFetch("POST", `/tasks/${encodeURIComponent(taskId)}/continue`, { prompt });
  }

  async stopTask(taskId: string): Promise<void> {
    await this.agentFetch("POST", `/tasks/${encodeURIComponent(taskId)}/stop`);
  }

  // ── P2P bus ────────────────────────────────────────────────────

  /** Subscribe to the connected agent's bus event stream. Returns
   *  an unsubscribe function. Use prefix to filter (e.g. "peer" for
   *  presence-only). Designed for Node/Bun scripts; the underlying
   *  SSE format is identical to what /bus/events exposes, so mobile
   *  and browser clients can consume the same stream via EventSource. */
  subscribeBusEvents(opts: {
    prefix?: string;
    onEvent: (evt: BusEvent) => void;
    onError?: (err: Error) => void;
  }): () => void {
    if (!this.baseUrl) {
      throw new Error("subscribeBusEvents: not connected. Call connect(deviceId) first.");
    }
    const url = new URL(`${this.baseUrl}/bus/events`);
    if (opts.prefix) url.searchParams.set("prefix", opts.prefix);
    const ctrl = new AbortController();
    (async () => {
      try {
        const res = await fetch(url.toString(), {
          headers: { ...this.authHeaders(), Accept: "text/event-stream" },
          signal: ctrl.signal,
        });
        if (!res.ok || !res.body) throw new Error(`bus/events: HTTP ${res.status}`);
        const reader = res.body.getReader();
        const decoder = new TextDecoder();
        let buf = "";
        while (true) {
          const { done, value } = await reader.read();
          if (done) return;
          buf += decoder.decode(value, { stream: true });
          for (;;) {
            const nl = buf.indexOf("\n");
            if (nl < 0) break;
            const line = buf.slice(0, nl).replace(/\r$/, "");
            buf = buf.slice(nl + 1);
            if (!line.startsWith("data: ")) continue;
            try {
              opts.onEvent(JSON.parse(line.slice("data: ".length)) as BusEvent);
            } catch {
              /* skip malformed frame */
            }
          }
        }
      } catch (err: any) {
        if (err?.name === "AbortError") return;
        opts.onError?.(err);
      }
    })();
    return () => ctrl.abort();
  }

  async publishBusEvent(
    topic: string,
    payload: unknown,
    opts: { retainSec?: number; qos?: 0 | 1 } = {},
  ): Promise<{ id: string; topic: string }> {
    const body: Record<string, unknown> = { topic, payload };
    if (opts.retainSec !== undefined) body.retainSec = opts.retainSec;
    if (opts.qos !== undefined) body.qos = opts.qos;
    return this.agentFetch("POST", "/bus/publish", body);
  }

  async getBusStatus(): Promise<BusStatus | null> {
    try {
      return await this.agentFetch("GET", "/bus/status");
    } catch {
      return null;
    }
  }

  // ── Recovery: Reconnect & Fix (matches PreviewPane handler) ────

  /** The full recovery sequence the web PreviewPane's
   *  "Reconnect & Fix" button runs. Streams step-by-step log entries
   *  via the `log` callback so a CLI / MCP caller can stream them
   *  back to the user. Never throws — returns a report. */
  async reconnectAndFix(opts: {
    deviceId: string;
    log?: (line: string) => void;
    stopDev?: boolean;
    clearCache?: boolean;
  }): Promise<{
    ok: boolean;
    steps: Array<{ step: string; ok: boolean; detail?: string }>;
  }> {
    const log = opts.log || (() => {});
    const steps: Array<{ step: string; ok: boolean; detail?: string }> = [];
    const add = (step: string, ok: boolean, detail?: string) => {
      steps.push({ step, ok, detail });
      log(`${ok ? "✓" : "✗"} ${step}${detail ? " — " + detail : ""}`);
    };

    // 1. Health probe.
    log("→ checking agent health…");
    try {
      const info = await this.agentFetch("GET", "/info");
      add("agent health", true, `v${info?.version || "?"}`);
    } catch (e: any) {
      add("agent health", false, e?.message || String(e));
      log("→ reconnecting…");
      const r = await this.connect(opts.deviceId);
      add("reconnect", r.ok, r.via || undefined);
      if (!r.ok) {
        log("→ repairing user relay password…");
        try {
          const rep = await this.repairRelay();
          add("repair relay password", rep.repaired, rep.reason);
          if (rep.repaired) {
            const r2 = await this.connect(opts.deviceId);
            add("reconnect after repair", r2.ok, r2.via || undefined);
          }
        } catch (err: any) {
          add("repair relay password", false, err?.message || String(err));
        }
      }
    }

    // 2. Dev server stop + clear cache + restart.
    const status = await this.getDevServerStatus().catch(() => null);
    if (opts.stopDev !== false && status?.running) {
      log("→ stopping dev server…");
      try {
        await this.stopDevServer();
        add("stop dev server", true);
      } catch (e: any) {
        add("stop dev server", false, e?.message || String(e));
      }

      if (opts.clearCache !== false && status.workDir) {
        log("→ clearing metro / expo caches on agent…");
        try {
          await this.agentFetch("POST", "/exec", {
            command:
              "rm -rf node_modules/.cache .expo/web/cache .metro-cache /tmp/metro-* 2>/dev/null || true; echo cleared",
            workDir: status.workDir,
            timeout: 30,
          });
          add("clear caches", true);
        } catch (e: any) {
          add("clear caches", false, e?.message || String(e));
        }
      }

      if (status.framework && status.workDir) {
        log(`→ restarting dev server (${status.framework})…`);
        try {
          await this.startDevServer({
            framework: status.framework as DevServerFramework,
            workDir: status.workDir,
          });
          add("restart dev server", true);
        } catch (e: any) {
          add("restart dev server", false, e?.message || String(e));
        }
      }
    }

    const ok = steps.every((s) => s.ok);
    return { ok, steps };
  }

  // ── Low-level HTTP ─────────────────────────────────────────────

  private authHeaders(): Record<string, string> {
    const h: Record<string, string> = {};
    if (this.token) h["Authorization"] = `Bearer ${this.token}`;
    if (this.activeRelayUrl && this.activeRelayPassword) {
      h["X-Relay-Password"] = this.activeRelayPassword;
    }
    return h;
  }

  private async convexCall(
    method: "GET" | "POST" | "DELETE",
    path: string,
    body?: unknown,
  ): Promise<any> {
    const url = `${this.convexUrl}${path}`;
    const headers: Record<string, string> = { Accept: "application/json" };
    if (this.token) headers["Authorization"] = `Bearer ${this.token}`;
    if (body !== undefined) headers["Content-Type"] = "application/json";
    const res = await fetch(url, {
      method,
      headers,
      body: body !== undefined ? JSON.stringify(body) : undefined,
    });
    if (!res.ok) {
      let msg = `HTTP ${res.status} ${method} ${path}`;
      try {
        const err = await res.json();
        if (err?.error) msg = `${msg}: ${err.error}`;
      } catch {
        /* ignore */
      }
      throw new Error(msg);
    }
    if (res.status === 204) return null;
    return res.json().catch(() => null);
  }

  private async agentFetch(
    method: "GET" | "POST" | "DELETE",
    path: string,
    body?: unknown,
  ): Promise<any> {
    if (!this.baseUrl) {
      throw new Error(`agentFetch: not connected. Call connect(deviceId) first.`);
    }
    const url = `${this.baseUrl}${path}`;
    const headers: Record<string, string> = { ...this.authHeaders(), Accept: "application/json" };
    if (body !== undefined) headers["Content-Type"] = "application/json";
    const res = await fetch(url, {
      method,
      headers,
      body: body !== undefined ? JSON.stringify(body) : undefined,
    });
    if (!res.ok) {
      let msg = `HTTP ${res.status} ${method} ${path}`;
      try {
        const err = await res.json();
        if (err?.error) msg = `${msg}: ${err.error}`;
      } catch {
        /* ignore */
      }
      throw new Error(msg);
    }
    if (res.status === 204) return null;
    return res.json().catch(() => null);
  }
}

async function probeHealth(
  url: string,
  headers: Record<string, string>,
  timeoutMs: number,
  path: "relay" | "direct" | "tunnel",
  relayId?: string,
): Promise<ConnectDiagnostic> {
  const started = Date.now();
  const ctrl = new AbortController();
  const timer = setTimeout(() => ctrl.abort(), timeoutMs);
  try {
    const res = await fetch(`${url.replace(/\/+$/, "")}/health`, {
      headers,
      signal: ctrl.signal,
    });
    const diag: ConnectDiagnostic = {
      path,
      relayId,
      ok: res.ok,
      status: res.status,
      durationMs: Date.now() - started,
    };
    try {
      const body = await res.clone().json();
      if (body?.authExpired === true) diag.authExpired = true;
    } catch {
      /* ignore */
    }
    if (!res.ok) diag.error = `HTTP ${res.status}`;
    return diag;
  } catch (e: any) {
    return {
      path,
      relayId,
      ok: false,
      error: e?.name === "AbortError" ? "timeout" : e?.message || "network error",
      durationMs: Date.now() - started,
    };
  } finally {
    clearTimeout(timer);
  }
}

export default WebClient;
