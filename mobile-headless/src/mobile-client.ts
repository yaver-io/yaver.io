// MobileClient — pure-Node surrogate of the Yaver mobile app.
//
// Why not import mobile/src/lib/quic.ts directly: that file pulls in
// `react-native` for `Platform.OS`, and Bun's runtime plugins can't
// reliably redirect bare specifiers (only file paths). So instead
// we reimplement the HTTP + SSE contract here, keeping the method
// names identical to what the mobile app calls. A drift test under
// test/drift.test.ts diffs the two surfaces and fails the build if
// a mobile-lib method gains signature that this file doesn't mirror.
//
// The contract — not the code — is what we're testing.

import { buildNativeBuildRequest } from "../../mobile/src/lib/nativeBuild";

export interface MobileClientOptions {
  dataDir?: string;
  convexUrl?: string;
  authToken?: string;
  platform?: "ios" | "android";
  deviceName?: string;
  /** Mirror iOS App Transport Security on `recoveryTargetsForDevice`.
   * When true (or env YMH_ATS=1), candidate URLs that would be blocked
   * by ATS on iPhone (plain HTTP to non-RFC1918 hosts) are filtered out.
   * Useful when reproducing iPhone-only failures in a Node test. */
  atsAware?: boolean;
  /** Override the agent base URL. When absent, MobileClient still
   *  works for Convex-only calls (listDevices, etc.) but agent
   *  endpoints need `connect()` first. */
  agentBaseUrl?: string;
}

type Device = {
  id: string;
  name: string;
  host?: string;
  port?: number;
  online?: boolean;
  needsAuth?: boolean;
  deviceClass?: string;
  os?: string;
  /** Every reachable IPv4 the agent reported in heartbeat — Wi-Fi LAN,
   *  Tailscale 100.x, Ethernet. Mobile races them in parallel during
   *  connect. Present only on agents >= the multi-IP rollout; older
   *  agents have it undefined. */
  lanIps?: string[];
  publicUrl?: string;
  tunnelUrl?: string;
  publicKey?: string;
  /** Populated for shared/guest devices only. */
  isGuest?: boolean;
  hostName?: string;
  hostEmail?: string;
  lastHeartbeat?: number;
  runners?: Array<{ id?: string; runnerId?: string; status?: string }>;
  installedRunnerIds?: string[];
};

export type HeadlessDeviceStatusProbe = {
  reachable: boolean;
  codingReady?: boolean;
  codingRunners?: Array<{ id?: string; ready?: boolean }>;
};

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

export interface PhoneProject {
  slug: string;
  name: string;
  template?: string;
  dir: string;
  createdAt: string;
  updatedAt: string;
  schema?: Record<string, unknown> | null;
  auth?: Record<string, unknown> | null;
  seed?: Record<string, unknown> | null;
  app?: Record<string, unknown> | null;
  stats?: Record<string, unknown> | null;
}

export interface PhoneCreateSpec {
  slug?: string;
  name: string;
  template?: string;
  schema?: Record<string, unknown>;
  auth?: Record<string, unknown>;
  seed?: Record<string, unknown>;
  app?: Record<string, unknown>;
  prompt?: string;
  runner?: string;
  importUrl?: string;
  importContent?: string;
  importTitle?: string;
}

export interface HeadlessPhoneTarget {
  baseUrl: string;
  authToken?: string;
}

export interface PhonePushResult {
  slug: string;
  localUrl: string;
  browseUrl: string;
  project: PhoneProject;
}

export interface VibingSuggestion {
  id: string;
  icon: string;
  label: string;
  desc: string;
  category: string;
  prompt: string;
  priority: number;
  reasoning?: string;
}

export interface VibingState {
  project: string;
  path: string;
  framework?: string;
  suggestions: VibingSuggestion[];
  quickActions: VibingSuggestion[];
  history: string[];
  generatedAt?: string;
}

export interface VibingExecuteRequest {
  prompt: string;
  projectPath?: string;
  projectName?: string;
  bundleId?: string;
}

export interface VibingExecuteResult {
  ok?: boolean;
  taskId?: string;
  runtimeDeploy?: unknown;
  message?: string;
 }

export interface BootstrapTodoDeployOptions {
  name: string;
  prompt?: string;
  target: HeadlessPhoneTarget;
  slug?: string;
  template?: string;
  includeData?: boolean;
  containerize?: boolean;
  onConflict?: "reject" | "rename" | "overwrite";
}

export interface BootstrapTodoDeployResult {
  localProject: PhoneProject;
  remote: PhonePushResult;
}

export interface RepoCloneResult {
  ok: boolean;
  path: string;
  output: string;
}

export interface RemoteBootstrapRepoOptions {
  repoUrl: string;
  branch?: string;
  targetDir?: string;
  feedbackPlatform?: "expo" | "react-native" | "flutter" | "web";
  ciTargets?: string[];
}

export interface RemoteBootstrapRepoResult {
  clone: RepoCloneResult;
  feedbackInstall: ExecSession;
  ciRuns: Array<{ target: string; exec: ExecSession }>;
}

export interface HeadlessRecoveryResult {
  ok: boolean;
  mode?: "direct" | "pair" | "device-code";
  pairCode?: string;
  pairSubmitUrl?: string;
  deviceCodeUrl?: string;
  userCode?: string;
  expiresAt?: string;
  error?: string;
  targetUrl?: string;
  submitted?: boolean;
}

export class MobileClient {
  readonly opts: Required<Omit<MobileClientOptions, "agentBaseUrl">> & { agentBaseUrl?: string };
  private agentBaseUrl?: string;

  constructor(opts: MobileClientOptions = {}) {
    this.opts = {
      dataDir: opts.dataDir ?? process.env.YMH_DATA_DIR ?? "",
      convexUrl: opts.convexUrl ?? process.env.YMH_CONVEX_URL ?? "https://perceptive-minnow-557.eu-west-1.convex.site",
      authToken: opts.authToken ?? process.env.YMH_AUTH_TOKEN ?? "",
      platform: opts.platform ?? "ios",
      deviceName: opts.deviceName ?? "mobile-headless",
      atsAware: opts.atsAware ?? process.env.YMH_ATS === "1",
    };
    this.agentBaseUrl = opts.agentBaseUrl ?? process.env.YMH_AGENT_URL;
  }

  // ── Auth ──────────────────────────────────────────────────────
  async signIn(params: { token?: string; email?: string; password?: string }): Promise<void> {
    if (params.token) { this.opts.authToken = params.token; return; }
    if (params.email && params.password) {
      const res = await fetch(this.opts.convexUrl + "/auth/login", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ email: params.email, password: params.password }),
      });
      if (!res.ok) throw new Error(`sign-in failed: HTTP ${res.status}`);
      const body = await res.json() as { token: string };
      this.opts.authToken = body.token;
      return;
    }
    throw new Error("signIn needs a token OR email+password");
  }

  async signOut(): Promise<void> {
    this.opts.authToken = "";
  }

  // ── Devices ────────────────────────────────────────────────────
  async listDevices(): Promise<Device[]> {
    const res = await fetch(this.opts.convexUrl + "/devices/list", { headers: this.authHeaders() });
    if (!res.ok) throw new Error(`listDevices: HTTP ${res.status}`);
    const body = await res.json() as { devices: any[] };
    // Normalise the Convex shape (quicHost / localIps / lastHeartbeat) into
    // the contract the real mobile app consumes (host / lanIps / lastSeen).
    // Callers use this to validate the multi-IP heartbeat is reaching
    // Convex end-to-end — if agents are running the new binary and the
    // schema is deployed, `lanIps` will be a non-empty array.
    return (body.devices ?? []).map((d) => ({
      id: d.deviceId || d.id,
      name: d.name,
      host: d.quicHost || d.host,
      port: d.quicPort || d.port,
      online: d.isOnline ?? d.online ?? false,
      needsAuth: d.needsAuth ?? false,
      deviceClass: d.deviceClass,
      os: d.platform || d.os,
      lanIps: Array.isArray(d.localIps) ? d.localIps : undefined,
      publicUrl: typeof d.publicUrl === "string" ? d.publicUrl : undefined,
      tunnelUrl: typeof d.tunnelUrl === "string" ? d.tunnelUrl : undefined,
      publicKey: typeof d.publicKey === "string" ? d.publicKey : undefined,
      isGuest: d.isGuest ?? false,
      hostName: d.hostName,
      hostEmail: d.hostEmail,
      lastHeartbeat: d.lastHeartbeat,
    }));
  }

  async recoverDeviceAuth(
    deviceId: string,
    opts: {
      bootstrapSecret?: string;
      mode?: "auto" | "direct" | "pair" | "device-code";
    } = {},
  ): Promise<HeadlessRecoveryResult> {
    if (!this.opts.authToken) {
      throw new Error("recoverDeviceAuth requires a signed-in mobile-headless session");
    }
    const devices = await this.listDevices();
    const device = devices.find((d) => d.id === deviceId);
    if (!device) throw new Error(`device not found: ${deviceId}`);

    const targets = await this.recoveryTargetsForDevice(device);
    if (targets.length === 0) {
      return { ok: false, error: "no reachable recovery targets for device" };
    }

    const requestedMode = opts.mode ?? "auto";
    let lastError = "network error";

    const tryRecover = async (
      target: { baseUrl: string; headers: Record<string, string> },
      mode: "direct" | "pair" | "device-code",
      secret?: string,
    ): Promise<HeadlessRecoveryResult | null> => {
      const res = await fetch(`${target.baseUrl}/auth/recover`, {
        method: "POST",
        headers: { ...target.headers, "Content-Type": "application/json" },
        body: JSON.stringify(secret ? { mode, secret } : { mode }),
      });
      const body = await safeJson(res);
      if (!res.ok) {
        const error = body?.error ?? `HTTP ${res.status}`;
        lastError = String(error);
        return {
          ok: false,
          mode,
          error: String(error),
          targetUrl: target.baseUrl,
        };
      }
      return {
        ok: true,
        mode: body?.mode ?? mode,
        pairCode: body?.pairCode,
        pairSubmitUrl: body?.pairSubmitUrl,
        deviceCodeUrl: body?.deviceCodeUrl,
        userCode: body?.userCode,
        expiresAt: body?.expiresAt,
        targetUrl: target.baseUrl,
      };
    };

    const submitPairToken = async (
      targetUrl: string,
      pairCode: string,
    ): Promise<{ ok: boolean; error?: string }> => {
      const res = await fetch(`${targetUrl}/auth/pair/submit?code=${encodeURIComponent(pairCode)}`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          token: this.opts.authToken,
          convexSiteUrl: this.opts.convexUrl,
          userId: "",
        }),
      });
      if (!res.ok) {
        const body = await safeJson(res);
        return { ok: false, error: body?.error ?? `pair submit HTTP ${res.status}` };
      }
      return { ok: true };
    };

    for (const target of targets) {
      if (requestedMode === "auto" || requestedMode === "direct") {
        const direct = await tryRecover(target, "direct");
        if (direct?.ok) {
          return direct;
        }
        const msg = String(direct?.error ?? "").toLowerCase();
        const modeUnsupported =
          msg.includes("mode must") || msg.includes("invalid mode") || msg.includes("direct");
        if (requestedMode === "direct") {
          continue;
        }
        if (direct && !modeUnsupported) {
          continue;
        }
      }

      if (requestedMode === "auto" || requestedMode === "pair") {
        const pair = await tryRecover(target, "pair");
        if (pair?.ok && pair.pairCode) {
          const submit = await submitPairToken(target.baseUrl, pair.pairCode);
          if (submit.ok) {
            return { ...pair, submitted: true };
          }
          lastError = submit.error ?? lastError;
        }
        if (requestedMode === "pair") {
          continue;
        }
      }

      if (requestedMode === "auto" && opts.bootstrapSecret) {
        const secretPair = await tryRecover(target, "pair", opts.bootstrapSecret);
        if (secretPair?.ok && secretPair.pairCode) {
          const submit = await submitPairToken(target.baseUrl, secretPair.pairCode);
          if (submit.ok) {
            return { ...secretPair, submitted: true };
          }
          lastError = submit.error ?? lastError;
        }
      }

      if (requestedMode === "auto" || requestedMode === "device-code") {
        const deviceCode = await tryRecover(target, "device-code");
        if (deviceCode?.ok) {
          return deviceCode;
        }
      }
    }

    return { ok: false, error: lastError };
  }

  /** Bind the client to a specific device's agent HTTP endpoint.
   *  Pass either a deviceId (which we resolve via listDevices) or
   *  explicit host/port. */
  async connect(deviceIdOrOpts: string | { host: string; port?: number }): Promise<void> {
    if (typeof deviceIdOrOpts === "object") {
      const p = deviceIdOrOpts.port ?? 18080;
      this.agentBaseUrl = `http://${deviceIdOrOpts.host}:${p}`;
      return;
    }
    const devices = await this.listDevices();
    const d = devices.find((x) => x.id === deviceIdOrOpts);
    if (!d) throw new Error("device not found: " + deviceIdOrOpts);
    const port = d.port ?? 18080;
    this.agentBaseUrl = `http://${d.host ?? "127.0.0.1"}:${port}`;
  }

  /** Alternative — point directly at a base URL. Used by tests that
   *  spin up a local mock agent. */
  useAgentBaseUrl(url: string) { this.agentBaseUrl = url; }

  async infraSummary(target?: string): Promise<any> {
    return (await this.raw.get(this.peerPath(target, "/infra/summary"))).body;
  }

  // ── Exec (remote command execution) ───────────────────────────
  async startExec(command: string, opts?: ExecOptions): Promise<{ execId: string; pid: number }> {
    const body: Record<string, unknown> = { command };
    if (opts?.workDir) body.workDir = opts.workDir;
    if (opts?.timeout) body.timeout = opts.timeout;
    if (opts?.env) body.env = opts.env;
    const r = await this.raw.post("/exec", body);
    if (r.status >= 400) {
      throw new Error(r.body?.error ?? `startExec: HTTP ${r.status}`);
    }
    if (!r.body?.ok || !r.body?.execId) {
      throw new Error("startExec: malformed response");
    }
    return { execId: r.body.execId, pid: r.body.pid ?? 0 };
  }

  async getExec(execId: string): Promise<ExecSession> {
    const r = await this.raw.get(`/exec/${encodeURIComponent(execId)}`);
    if (r.status >= 400) {
      throw new Error(r.body?.error ?? `getExec: HTTP ${r.status}`);
    }
    return r.body?.exec;
  }

  async listExecs(): Promise<ExecSession[]> {
    const r = await this.raw.get("/exec");
    if (r.status >= 400) {
      throw new Error(r.body?.error ?? `listExecs: HTTP ${r.status}`);
    }
    return r.body?.execs ?? [];
  }

  async waitForExec(execId: string, opts: { timeoutMs?: number; pollMs?: number } = {}): Promise<ExecSession> {
    const deadline = Date.now() + (opts.timeoutMs ?? 5 * 60_000);
    const pollMs = opts.pollMs ?? 1000;
    while (true) {
      const exec = await this.getExec(execId);
      if (exec.status === "completed" || exec.status === "failed" || exec.status === "killed") {
        return exec;
      }
      if (Date.now() > deadline) {
        throw new Error(`waitForExec timed out after ${opts.timeoutMs ?? 5 * 60_000}ms`);
      }
      await new Promise((resolve) => setTimeout(resolve, pollMs));
    }
  }

  async getRunners(): Promise<any[]> {
    const r = await this.raw.get("/agent/runners");
    return r.body?.runners ?? [];
  }

  // ── Installer catalogue ────────────────────────────────────────
  async listInstallables(target?: string): Promise<any[]> {
    const r = await this.raw.get(this.peerPath(target, "/install/list"));
    return Array.isArray(r.body) ? r.body : [];
  }

  async *installTool(tool: string, opts?: { target?: string }): AsyncIterable<{ kind: "line" | "result" | "sudo_prompt" | "event"; text?: string; status?: string; error?: string; prompt?: string; raw?: any }> {
    const r = await this.raw.post(this.peerPath(opts?.target, `/install/${encodeURIComponent(tool)}`));
    if (r.status >= 400) {
      yield { kind: "result", status: "error", error: r.body?.error ?? `HTTP ${r.status}` };
      return;
    }
    const streamName: string = r.body?.stream ?? `install:${tool}`;
    for await (const frame of this.streamEvents(streamName)) {
      if (frame.type === "line") yield { kind: "line", text: frame.text };
      else if (frame.type === "sudo_prompt") yield { kind: "sudo_prompt", prompt: frame.prompt };
      else if (frame.type === "result") { yield { kind: "result", status: frame.status, error: frame.error }; return; }
      else yield { kind: "event", raw: frame };
    }
  }

  async respondSudo(tool: string, password: string, opts?: { target?: string }): Promise<{ ok: boolean; error?: string }> {
    const r = await this.raw.post(this.peerPath(opts?.target, "/install/sudo"), { tool, password });
    if (r.status >= 400) return { ok: false, error: r.body?.error ?? `HTTP ${r.status}` };
    return { ok: true };
  }

  async cancelSudo(tool: string, opts?: { target?: string }): Promise<void> {
    await this.raw.post(this.peerPath(opts?.target, "/install/sudo"), { tool, cancel: true });
  }

  // ── Primary device (auto-connect target) ──────────────────────
  /** Read the user's preferred device for auto-connect. null means "no
   *  preference set" — single-device users auto-connect regardless,
   *  multi-device users without a primary are left to pick manually. */
  async getPrimaryDevice(): Promise<string | null> {
    const res = await fetch(this.opts.convexUrl + "/settings", { headers: this.authHeaders() });
    if (!res.ok) throw new Error(`getPrimaryDevice: HTTP ${res.status}`);
    const body = await res.json() as { settings?: { primaryDeviceId?: string | null } };
    return body.settings?.primaryDeviceId ?? null;
  }

  /** Persist the preferred device. Pass null to clear the preference.
   *  Any field omitted from the body leaves other settings untouched —
   *  `primaryDeviceId: null` is the explicit "clear" sentinel Convex
   *  recognises. */
  async setPrimaryDevice(deviceId: string | null): Promise<void> {
    const res = await fetch(this.opts.convexUrl + "/settings", {
      method: "POST",
      headers: { ...this.authHeaders(), "Content-Type": "application/json" },
      body: JSON.stringify({ primaryDeviceId: deviceId }),
    });
    if (!res.ok) throw new Error(`setPrimaryDevice: HTTP ${res.status}`);
  }

  // ── Parallel connect race (mirrors quic.ts::raceDirectCandidates) ─
  /** Probe every reachable IP for a device in parallel — beacon (if passed)
   *  + every lanIps entry + the Convex-stored quicHost — and resolve with
   *  the first `/health` 200 within the per-probe budget. This mirrors
   *  what the real mobile quic.ts does during `connect()` on the phone so
   *  tests can validate that a device is reachable via Tailscale, LAN,
   *  etc. without running the RN app. Returns `null` if no path answers. */
  async raceDevicePaths(
    device: Pick<Device, "id" | "host" | "port" | "lanIps">,
    opts: { beaconIp?: string; beaconPort?: number; perProbeMs?: number } = {},
  ): Promise<{ ip: string; port: number; path: "lan-beacon" | "lan-heartbeat" | "lan-tailscale" | "lan-convex-ip"; rttMs: number } | null> {
    type Candidate = { ip: string; port: number; path: "lan-beacon" | "lan-heartbeat" | "lan-tailscale" | "lan-convex-ip" };
    const seen = new Set<string>();
    const out: Candidate[] = [];
    const port = device.port ?? 18080;
    const push = (ip: string, p: number, pathLabel: Candidate["path"]) => {
      if (!ip || !p) return;
      const key = `${ip}:${p}`;
      if (seen.has(key)) return;
      seen.add(key);
      out.push({ ip, port: p, path: pathLabel });
    };
    if (opts.beaconIp) push(opts.beaconIp, opts.beaconPort ?? port, "lan-beacon");
    for (const ip of device.lanIps ?? []) {
      const isTailscale = /^100\./.test(ip) && (() => {
        const second = parseInt(ip.split(".")[1] ?? "0", 10);
        return second >= 64 && second <= 127;
      })();
      push(ip, port, isTailscale ? "lan-tailscale" : "lan-heartbeat");
    }
    if (device.host) push(device.host, port, "lan-convex-ip");
    if (out.length === 0) return null;
    const budget = opts.perProbeMs ?? 2500;
    const probes = out.map(async (c) => {
      const ctrl = new AbortController();
      const t = setTimeout(() => ctrl.abort(), budget);
      const start = Date.now();
      try {
        const res = await fetch(`http://${c.ip}:${c.port}/health`, {
          headers: this.authHeaders(),
          signal: ctrl.signal,
        });
        clearTimeout(t);
        if (!res.ok) throw new Error(`status ${res.status}`);
        return { ip: c.ip, port: c.port, path: c.path, rttMs: Date.now() - start };
      } catch (e) {
        clearTimeout(t);
        throw e;
      }
    });
    try {
      return await Promise.any(probes);
    } catch {
      return null;
    }
  }

  // ── Auto-connect decision (mirrors DeviceContext.tsx rule) ────
  /** Apply the same ranking the real mobile app uses after probing every
   * owned device: reachable only, coding-ready first, then sticky →
   * primary → secondary → any reachable by name. Guests are never auto-picked.
   * When `probes` is omitted this falls back to the Convex `online` flag so
   * the CLI remains useful without performing live network probes. */
  pickAutoConnectTarget(
    devices: Device[],
    primaryDeviceId: string | null,
    opts?: {
      secondaryDeviceId?: string | null;
      stickyDeviceId?: string | null;
      probes?: Record<string, HeadlessDeviceStatusProbe | null | undefined>;
    },
  ): Device | null {
    const candidates = devices.filter((d) => !d.isGuest);
    const priorityIds = [opts?.stickyDeviceId, primaryDeviceId, opts?.secondaryDeviceId];
    const ranked = candidates
      .map((device) => ({
        device,
        rank: this.autoConnectRank(device, opts?.probes?.[device.id], priorityIds, opts?.probes !== undefined),
      }))
      .filter((row) => row.rank >= 0)
      .sort((a, b) => {
        if (b.rank !== a.rank) return b.rank - a.rank;
        return a.device.name.localeCompare(b.device.name);
      });
    return ranked[0]?.device ?? null;
  }

  private autoConnectRank(
    device: Device,
    probe: HeadlessDeviceStatusProbe | null | undefined,
    priorityIds: Array<string | null | undefined>,
    hasExplicitProbeSet: boolean,
  ): number {
    const reachable = hasExplicitProbeSet ? probe?.reachable === true : device.online === true;
    if (!reachable) return -1;
    const priority = priorityIds.findIndex((id) => !!id && id === device.id);
    const priorityScore = priority >= 0 ? 100 - priority * 10 : 10;
    const codingScore = probe?.codingReady || this.deviceRunnerReadyFromHeartbeat(device) ? 1_000 : 0;
    return codingScore + priorityScore;
  }

  private deviceRunnerReadyFromHeartbeat(device: Device): boolean {
    const runnerIds = new Set(["claude", "claude-code", "codex", "opencode", "glm"]);
    for (const runner of device.runners || []) {
      const id = String(runner.runnerId || runner.id || "").trim().toLowerCase();
      const status = String(runner.status || "").trim().toLowerCase();
      if (runnerIds.has(id) && (status === "ready" || status === "running" || status === "idle")) return true;
    }
    return (device.installedRunnerIds || []).some((id) => runnerIds.has(String(id).trim().toLowerCase()));
  }

  // ── Real-time relay presence ──────────────────────────────────
  /** Ask a relay server which of the given deviceIds currently have an
   *  active QUIC tunnel. Authoritative — no heartbeat lag. Unknown ids
   *  report `{online: false}` indistinguishably from "exists but
   *  offline", so the endpoint is safe without auth. */
  async relayPresence(relayHttpUrl: string, deviceIds: string[]): Promise<Record<string, { deviceId: string; online: boolean; since?: string; uptimeSec?: number }>> {
    if (deviceIds.length === 0) return {};
    const url = `${relayHttpUrl.replace(/\/$/, "")}/presence?ids=${encodeURIComponent(deviceIds.join(","))}`;
    const res = await fetch(url);
    if (!res.ok) throw new Error(`relayPresence: HTTP ${res.status}`);
    const body = await res.json() as { ok: boolean; devices?: Record<string, any> };
    return body.devices ?? {};
  }

  // ── Grand MCP: ops ────────────────────────────────────────────
  /** List registered ops verbs with their payload schemas. */
  async opsVerbs(): Promise<Array<{
    name: string;
    description: string;
    streaming: boolean;
    allowGuest: boolean;
    payload: Record<string, unknown>;
  }>> {
    const r = await this.raw.get("/ops/verbs");
    if (r.status >= 400) throw new Error(`opsVerbs: HTTP ${r.status}`);
    return r.body?.verbs ?? [];
  }

  /** Invoke a single ops verb. Returns the structured OpsResult — agents
   *  inspect `ok`, `streamId`, `initial`, `error`, `code` to branch.
   *  Setting `machine` to anything other than "local" currently returns
   *  `code:"remote_not_implemented"` — remote routing lands in a
   *  follow-up. */
  async ops(
    verb: string,
    payload?: unknown,
    machine: string = "local",
  ): Promise<{
    ok: boolean;
    streamId?: string;
    initial?: any;
    error?: string;
    code?: string;
  }> {
    const r = await this.raw.post("/ops", { verb, machine, payload });
    if (r.status >= 500) throw new Error(`ops: HTTP ${r.status}`);
    return r.body ?? { ok: false, error: `HTTP ${r.status}`, code: "http_error" };
  }

  // ── Monorepo detection (mirrors mobile/src/lib/quic.ts QuicClient) ──
  /** GET /projects/monorepo — classify the framework composition of a directory.
   *  Mirrors mobile QuicClient.detectMonorepo so the drift test stays green. */
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
    const params = new URLSearchParams();
    if (dir) params.set("dir", dir);
    if (maxDepth) params.set("maxDepth", String(maxDepth));
    const qs = params.toString();
    const r = await this.raw.get(`/projects/monorepo${qs ? "?" + qs : ""}`);
    if (r.status >= 400) {
      throw new Error(r.body?.error ?? `detectMonorepo: HTTP ${r.status}`);
    }
    return r.body;
  }

  /** POST /builds with a friendly native platform alias.
   *  Mirrors mobile QuicClient.startNativeBuild. */
  async startNativeBuild(
    platform: "iosNative" | "androidNative" | "flutter",
    target: "device" | "simulator" | "testflight" | "playstore" | "local" | "apk" | "aab" | "ipa" = "device",
    workDir?: string,
    extras?: { scheme?: string; flavor?: string; installOnDevice?: boolean; args?: string[] },
  ): Promise<{ id: string; platform: string; status: string; command?: string; workDir?: string }> {
    const args: string[] = [];
    if (platform === "iosNative" && extras?.scheme) args.push(extras.scheme);
    if (platform === "androidNative" && extras?.flavor) args.push(extras.flavor);
    if (extras?.args?.length) args.push(...extras.args);
    const installOnDevice = extras?.installOnDevice ?? (target === "device" || target === "simulator");
    const r = await this.raw.post("/builds", {
      platform,
      target,
      workDir: workDir ?? "",
      args,
      installOnDevice,
    });
    if (r.status >= 400) {
      throw new Error(r.body?.error ?? `startNativeBuild: HTTP ${r.status}`);
    }
    return r.body;
  }

  // ── Wizard ─────────────────────────────────────────────────────
  readonly wizard = {
    start: async () => (await this.raw.post("/project/wizard/start")).body,
    questions: async () => (await this.raw.get("/project/wizard/questions")).body,
    answer: async (sessionId: string, questionId: string, answer: string) =>
      (await this.raw.post("/project/wizard/answer", { sessionId, questionId, answer })).body,
    generate: async (sessionId: string, parentDir?: string) =>
      (await this.raw.post("/project/wizard/generate", { sessionId, parentDir })).body,
  };

  // ── Vibing ─────────────────────────────────────────────────────
  readonly vibing = {
    state: async (query: string): Promise<VibingState> => {
      const r = await this.raw.get(`/vibing?query=${encodeURIComponent(query)}`);
      if (r.status >= 400) throw new Error(r.body?.error ?? `getVibingState: HTTP ${r.status}`);
      return r.body as VibingState;
    },
    execute: async (body: VibingExecuteRequest): Promise<VibingExecuteResult> => {
      const r = await this.raw.post("/vibing/execute", body);
      if (r.status >= 400) throw new Error(r.body?.error ?? `executeVibing: HTTP ${r.status}`);
      return r.body as VibingExecuteResult;
    },
  };

  // ── Guests (via Convex HTTP) ──────────────────────────────────
  readonly guests = {
    list: async () => {
      const res = await fetch(this.opts.convexUrl + "/guests/list", { headers: this.authHeaders() });
      if (!res.ok) throw new Error("listGuests: HTTP " + res.status);
      const body = await res.json() as { guests: any[] };
      return body.guests ?? [];
    },
    invite: async (
      target:
        | string
        | {
            email?: string;
            userId?: string;
            deviceIds?: string[];
            scope?: "full" | "feedback-only" | "sdk-project";
            allowedProjects?: string[];
          },
    ) => {
      const body = typeof target === "string" ? { email: target } : target;
      const res = await fetch(this.opts.convexUrl + "/guests/invite", {
        method: "POST",
        headers: { ...this.authHeaders(), "Content-Type": "application/json" },
        body: JSON.stringify(body),
      });
      if (!res.ok) throw new Error("invite: HTTP " + res.status);
      return res.json();
    },
  };

  // ── Dev server ────────────────────────────────────────────────
  readonly devServer = {
    status: async () => (await this.raw.get("/dev/status")).body,
    start: async (framework: string, workDir: string) =>
      (await this.raw.post("/dev/start", { framework, workDir })).body,
    stop: async () => (await this.raw.post("/dev/stop")).body,
    reload: async () => (await this.raw.post("/dev/reload")).body,
    /**
     * Trigger the agent's Hermes bytecode build for the active project and
     * return the structured result. Mirrors the call the real mobile app
     * makes from DevPreview / Hot Reload before loading a bundle.
     *
     * `opts.timeoutMs` defaults to 12 minutes — slightly more than the
     * agent's combined Metro (8 min) + hermesc (3 min) caps so the agent
     * gets to surface its own structured "timedOut" response first. A
     * client abort fires only when the agent is unreachable or wedged
     * past its own caps; the rejection's `name` is "AbortError".
     */
    buildNative: async (
      platform: "ios" | "android",
      opts?: {
        timeoutMs?: number;
        signal?: AbortSignal;
        // Stateless contract: agent ≥ 1.99.187 requires the caller to
        // pin which guest project to bundle. Headless tests should
        // always pass one of these so the agent never falls back to
        // whatever dev server happens to be running on the box.
        projectPath?: string;
        projectName?: string;
        bundleId?: string;
      },
    ) => {
      const externalSignal = opts?.signal;
      const ctrl = new AbortController();
      const onAbort = () => ctrl.abort();
      if (externalSignal) {
        if (externalSignal.aborted) ctrl.abort();
        else externalSignal.addEventListener("abort", onAbort, { once: true });
      }
      const timeoutMs = opts?.timeoutMs ?? 12 * 60_000;
      const timer = setTimeout(() => ctrl.abort(), timeoutMs);
      try {
        const r = await this.raw.post(
          "/dev/build-native",
          buildNativeBuildRequest(
            platform,
            {
              consumerVersion: "mobile-headless",
              consumerBuild: "headless",
              consumerSdkVersion: "headless",
              consumerHermesBCVersion: 96,
            },
            opts && (opts.projectPath || opts.projectName || opts.bundleId)
              ? {
                  projectPath: opts.projectPath,
                  projectName: opts.projectName,
                  bundleId: opts.bundleId,
                }
              : undefined,
          ),
          { signal: ctrl.signal },
        );
        return { status: r.status, body: r.body };
      } finally {
        clearTimeout(timer);
        if (externalSignal) externalSignal.removeEventListener("abort", onAbort);
      }
    },
  };

  // ── Repos ──────────────────────────────────────────────────────
  readonly repos = {
    clone: async (url: string, dir?: string, branch?: string): Promise<RepoCloneResult> => {
      const r = await this.raw.post("/repos/clone", { url, dir, branch });
      if (r.status >= 400) throw new Error(r.body?.error ?? `cloneRepo: HTTP ${r.status}`);
      return r.body as RepoCloneResult;
    },
    list: async (): Promise<Array<{ name: string; path: string; branch: string; remote: string; dirty: boolean }>> => {
      const r = await this.raw.get("/repos/list");
      if (r.status >= 400) throw new Error(r.body?.error ?? `listRepos: HTTP ${r.status}`);
      return r.body ?? [];
    },
    bootstrapRemote: async (opts: RemoteBootstrapRepoOptions): Promise<RemoteBootstrapRepoResult> => {
      const clone = await this.repos.clone(opts.repoUrl, opts.targetDir, opts.branch);
      const feedbackPlatform = opts.feedbackPlatform ?? "react-native";
      const ciTargets = opts.ciTargets?.length ? opts.ciTargets : ["hermes", "feedback"];

      const feedbackInstallStart = await this.startExec(
        `yaver sdk add feedback --platform ${feedbackPlatform}`,
        { workDir: clone.path, timeout: 10 * 60_000 },
      );
      const feedbackInstall = await this.waitForExec(feedbackInstallStart.execId, { timeoutMs: 10 * 60_000 });
      if (feedbackInstall.status !== "completed" || (feedbackInstall.exitCode ?? 0) !== 0) {
        throw new Error(feedbackInstall.stderr || feedbackInstall.stdout || "feedback SDK bootstrap failed");
      }

      const ciRuns: Array<{ target: string; exec: ExecSession }> = [];
      for (const target of ciTargets) {
        const started = await this.startExec(
          `yaver ci add ${target} --force`,
          { workDir: clone.path, timeout: 10 * 60_000 },
        );
        const exec = await this.waitForExec(started.execId, { timeoutMs: 10 * 60_000 });
        if (exec.status !== "completed" || (exec.exitCode ?? 0) !== 0) {
          throw new Error(exec.stderr || exec.stdout || `ci bootstrap failed for ${target}`);
        }
        ciRuns.push({ target, exec });
      }

      return { clone, feedbackInstall, ciRuns };
    },
  };

  // ── Phone projects ────────────────────────────────────────────
  readonly phoneProjects = {
    list: async (): Promise<PhoneProject[]> => {
      const r = await this.raw.get("/phone/projects/list");
      if (r.status >= 400) throw new Error(r.body?.error ?? `listPhoneProjects: HTTP ${r.status}`);
      return r.body?.projects ?? [];
    },
    get: async (slug: string): Promise<PhoneProject | null> => {
      const r = await this.raw.get(`/phone/projects/get?slug=${encodeURIComponent(slug)}`);
      if (r.status >= 400) throw new Error(r.body?.error ?? `getPhoneProject: HTTP ${r.status}`);
      return r.body ?? null;
    },
    create: async (spec: PhoneCreateSpec): Promise<PhoneProject> => {
      const r = await this.raw.post("/phone/projects/create", spec);
      if (r.status >= 400) throw new Error(r.body?.error ?? `createPhoneProject: HTTP ${r.status}`);
      return r.body as PhoneProject;
    },
    createAt: async (target: HeadlessPhoneTarget, spec: PhoneCreateSpec): Promise<PhoneProject> => {
      const res = await fetch(this.absoluteUrl(target.baseUrl, "/phone/projects/create"), {
        method: "POST",
        headers: {
          ...this.authForTarget(target.authToken),
          "Content-Type": "application/json",
        },
        body: JSON.stringify(spec),
      });
      const body = await safeJson(res);
      if (!res.ok) throw new Error(body?.error ?? `createPhoneProjectAt: HTTP ${res.status}`);
      return body as PhoneProject;
    },
    export: async (
      slug: string,
      opts: { includeData?: boolean; containerize?: boolean } = {},
    ): Promise<{ size: number; contentType: string; body: Uint8Array }> => {
      const q = new URLSearchParams({ slug });
      if (opts.includeData) q.set("includeData", "true");
      if (opts.containerize) q.set("containerize", "true");
      const res = await fetch(this.agentURL(`/phone/projects/export?${q.toString()}`), {
        headers: this.authHeaders(),
      });
      if (!res.ok) {
        const body = await res.text().catch(() => "");
        throw new Error(body || `exportPhoneProject: HTTP ${res.status}`);
      }
      const buf = new Uint8Array(await res.arrayBuffer());
      return {
        size: buf.byteLength,
        contentType: res.headers.get("content-type") ?? "application/octet-stream",
        body: buf,
      };
    },
    push: async (
      slug: string,
      target: HeadlessPhoneTarget,
      opts: {
        onConflict?: "reject" | "rename" | "overwrite";
        skipSeed?: boolean;
        includeData?: boolean;
        containerize?: boolean;
      } = {},
    ): Promise<PhonePushResult> => {
      const exported = await this.phoneProjects.export(slug, {
        includeData: opts.includeData,
        containerize: opts.containerize,
      });
      const bundleBuffer = exported.body.buffer.slice(
        exported.body.byteOffset,
        exported.body.byteOffset + exported.body.byteLength,
      ) as ArrayBuffer;
      const form = new FormData();
      form.append(
        "bundle",
        new Blob([bundleBuffer], { type: exported.contentType }),
        `${slug}.tgz`,
      );
      if (opts.onConflict) form.append("onConflict", opts.onConflict);
      if (opts.skipSeed) form.append("skipSeed", "true");
      const res = await fetch(this.absoluteUrl(target.baseUrl, "/phone/projects/receive"), {
        method: "POST",
        headers: this.authForTarget(target.authToken),
        body: form,
      });
      const body = await safeJson(res);
      if (!res.ok) throw new Error(body?.error ?? `pushPhoneProject: HTTP ${res.status}`);
      return body as PhonePushResult;
    },
    bootstrapTodoDeploy: async (
      opts: BootstrapTodoDeployOptions,
    ): Promise<BootstrapTodoDeployResult> => {
      const localProject = await this.phoneProjects.create({
        name: opts.name,
        slug: opts.slug,
        template: opts.template ?? "todos",
        prompt: opts.prompt,
      });
      const remote = await this.phoneProjects.push(localProject.slug, opts.target, {
        includeData: opts.includeData,
        containerize: opts.containerize ?? true,
        onConflict: opts.onConflict ?? "rename",
      });
      return { localProject, remote };
    },
  };

  // ── Raw escape hatch ──────────────────────────────────────────
  // The optional `opts.signal` lets callers wire their own timeout to a
  // request that may legitimately take minutes (e.g. /dev/build-native).
  // Without this, a hung agent leaves the fetch open forever and there's
  // no way to surface a real "build timed out" error to the caller.
  readonly raw = {
    get: async (p: string, opts?: { signal?: AbortSignal }) => {
      const res = await fetch(this.agentURL(p), { headers: this.authHeaders(), signal: opts?.signal });
      return { status: res.status, body: await safeJson(res) };
    },
    post: async (p: string, body?: any, opts?: { signal?: AbortSignal }) => {
      const res = await fetch(this.agentURL(p), {
        method: "POST",
        headers: { ...this.authHeaders(), "Content-Type": "application/json" },
        body: body !== undefined ? JSON.stringify(body) : undefined,
        signal: opts?.signal,
      });
      return { status: res.status, body: await safeJson(res) };
    },
  };

  /** Low-level SSE subscription against `/streams/<name>`. */
  async *streamEvents(streamName: string): AsyncIterable<any> {
    const res = await fetch(this.agentURL(`/streams/${encodeURIComponent(streamName)}`), { headers: this.authHeaders() });
    if (!res.ok || !res.body) return;
    const reader = res.body.getReader();
    const decoder = new TextDecoder("utf-8");
    let buf = "";
    while (true) {
      const { value, done } = await reader.read();
      if (done) return;
      buf += decoder.decode(value, { stream: true });
      let idx: number;
      while ((idx = buf.indexOf("\n\n")) >= 0) {
        const chunk = buf.slice(0, idx).trim();
        buf = buf.slice(idx + 2);
        if (!chunk.startsWith("data:")) continue;
        try {
          const ev = JSON.parse(chunk.slice(5).trim());
          yield ev;
          if (ev.type === "result") return;
        } catch { /* keepalive */ }
      }
    }
  }

  // ── helpers ────────────────────────────────────────────────────
  private authHeaders(): Record<string, string> {
    return this.opts.authToken ? { Authorization: "Bearer " + this.opts.authToken } : {};
  }
  private agentURL(p: string): string {
    const base = this.agentBaseUrl ?? "http://127.0.0.1:18080";
    return base.replace(/\/$/, "") + (p.startsWith("/") ? p : "/" + p);
  }
  private absoluteUrl(base: string, p: string): string {
    return base.replace(/\/$/, "") + (p.startsWith("/") ? p : "/" + p);
  }
  private authForTarget(token?: string): Record<string, string> {
    return token ? { Authorization: "Bearer " + token } : this.authHeaders();
  }
  private peerPath(target: string | undefined, p: string): string {
    if (!target) return p;
    return `/peer/${encodeURIComponent(target)}${p}`;
  }

  private async recoveryTargetsForDevice(
    device: Device,
  ): Promise<Array<{ baseUrl: string; headers: Record<string, string> }>> {
    const out: Array<{ baseUrl: string; headers: Record<string, string> }> = [];
    const seen = new Set<string>();
    // ATS-aware filter — mirrors iOS App Transport Security so headless
    // tests reproduce iPhone behavior. ATS blocks plain HTTP requests
    // to non-local addresses (-1022) when NSAllowsArbitraryLoads is
    // false. The Yaver mobile Info.plist sets NSAllowsLocalNetworking=true
    // (so RFC1918 / loopback HTTP is allowed) but otherwise enforces ATS.
    // Set YMH_ATS=1 (or pass atsAware:true via constructor) to enable the
    // same filter in mobile-headless and reproduce the iOS connection
    // shortlist exactly.
    const atsAware = this.opts.atsAware === true ||
      process.env.YMH_ATS === "1" ||
      process.env.YMH_ATS === "true";
    const isAtsAllowed = (rawUrl: string): boolean => {
      if (!atsAware) return true;
      try {
        const u = new URL(rawUrl);
        if (u.protocol === "https:") return true;
        // HTTP allowed only for RFC1918 / loopback / link-local. Anything
        // else (public IP, public hostname, .local mDNS) — iOS rejects
        // with -1022 unless NSAllowsArbitraryLoads is true.
        const host = u.hostname;
        if (host === "localhost" || host === "127.0.0.1" || host === "::1") return true;
        // RFC1918 IPv4
        const m4 = host.match(/^(\d{1,3})\.(\d{1,3})\.(\d{1,3})\.(\d{1,3})$/);
        if (m4) {
          const [a, b] = [Number(m4[1]), Number(m4[2])];
          if (a === 10) return true;
          if (a === 172 && b >= 16 && b <= 31) return true;
          if (a === 192 && b === 168) return true;
          if (a === 169 && b === 254) return true; // link-local
          // Non-RFC1918 IPv4 over HTTP → ATS blocks
          return false;
        }
        // Hostname that ends in .local — mDNS, allowed by NSAllowsLocalNetworking
        if (host.endsWith(".local")) return true;
        // Any other public hostname over HTTP → ATS blocks
        return false;
      } catch {
        return true;
      }
    };
    const push = (baseUrl: string | undefined, headers: Record<string, string>) => {
      const normalized = (baseUrl ?? "").replace(/\/+$/, "");
      if (!normalized || seen.has(normalized)) return;
      if (!isAtsAllowed(normalized)) return; // skip ATS-blocked candidates
      seen.add(normalized);
      out.push({ baseUrl: normalized, headers });
    };

    const authHeaders = this.authHeaders();
    if (this.agentBaseUrl) {
      push(this.agentBaseUrl, authHeaders);
    }
    if (device.host) {
      push(`http://${device.host}:${device.port ?? 18080}`, authHeaders);
    }
    for (const ip of device.lanIps ?? []) {
      push(`http://${ip}:${device.port ?? 18080}`, authHeaders);
    }
    if (device.publicUrl) push(device.publicUrl, authHeaders);
    if (device.tunnelUrl) push(device.tunnelUrl, authHeaders);

    const relayInfo = await this.fetchRelayInfo();
    for (const relay of relayInfo.servers) {
      if (!relay.httpUrl) continue;
      const headers: Record<string, string> = { ...authHeaders };
      const password = relay.password ?? relayInfo.userRelayPassword;
      if (password) headers["X-Relay-Password"] = password;
      push(`${relay.httpUrl.replace(/\/$/, "")}/d/${device.id}`, headers);
    }
    return out;
  }

  private async fetchRelayInfo(): Promise<{
    servers: Array<{ httpUrl?: string; password?: string; priority?: number }>;
    userRelayPassword?: string;
  }> {
    const base = this.opts.convexUrl.replace(/\/$/, "");
    const servers: Array<{ httpUrl?: string; password?: string; priority?: number }> = [];
    const [configRes, settingsRes] = await Promise.allSettled([
      fetch(`${base}/config`),
      fetch(`${base}/settings`, { headers: this.authHeaders() }),
    ]);

    if (configRes.status === "fulfilled" && configRes.value.ok) {
      const data = await safeJson(configRes.value);
      for (const relay of data?.relayServers ?? []) {
        if (typeof relay?.httpUrl === "string" && relay.httpUrl) {
          servers.push({
            httpUrl: relay.httpUrl,
            priority: typeof relay.priority === "number" ? relay.priority : 0,
          });
        }
      }
    }

    let userRelayPassword: string | undefined;
    if (settingsRes.status === "fulfilled" && settingsRes.value.ok) {
      const data = await safeJson(settingsRes.value);
      userRelayPassword =
        (typeof data?.settings?.relayPassword === "string" && data.settings.relayPassword) ||
        (typeof data?.relayPassword === "string" && data.relayPassword) ||
        undefined;
      const userRelayUrl =
        (typeof data?.settings?.relayUrl === "string" && data.settings.relayUrl) ||
        (typeof data?.relayUrl === "string" && data.relayUrl) ||
        undefined;
      if (userRelayUrl) {
        servers.unshift({
          httpUrl: userRelayUrl,
          password: userRelayPassword,
          priority: -1,
        });
      }
    }

    servers.sort((a, b) => (a.priority ?? 0) - (b.priority ?? 0));
    return { servers, userRelayPassword };
  }

  // ── P2P bus ───────────────────────────────────────────────────
  //
  // Mobile-specific usage pattern: subscribe only while the app is
  // foregrounded (iOS/Android both kill long-lived sockets in
  // background). When backgrounded, fall back to periodic Convex
  // /devices/list polling (what listDevices() already does).
  //
  // The SSE stream matches what web-headless sees and what a Go
  // bus peer emits — one shared wire format.

  /** Subscribe to the connected agent's P2P bus event stream.
   *  `prefix` filters to a topic prefix (e.g. "peer" for presence).
   *  Returns an unsubscribe function that aborts the underlying
   *  fetch; call it when the app backgrounds. */
  subscribeBusEvents(opts: {
    prefix?: string;
    onEvent: (evt: {
      id: string;
      topic: string;
      publisher: string;
      publishedAt: number;
      ttl?: number;
      qos: number;
      payload?: unknown;
    }) => void;
    onError?: (err: Error) => void;
  }): () => void {
    const url = new URL(this.agentURL("/bus/events"));
    if (opts.prefix) url.searchParams.set("prefix", opts.prefix);
    const ctrl = new AbortController();
    (async () => {
      try {
        const res = await fetch(url.toString(), {
          headers: { ...this.authHeaders(), Accept: "text/event-stream" },
          signal: ctrl.signal,
        });
        if (!res.ok || !res.body) throw new Error(`bus/events: HTTP ${res.status}`);
        const reader = (res.body as any).getReader();
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
              opts.onEvent(JSON.parse(line.slice("data: ".length)));
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

  async getBusStatus(): Promise<any> {
    try {
      const res = await fetch(this.agentURL("/bus/status"), { headers: this.authHeaders() });
      if (!res.ok) return null;
      return await safeJson(res);
    } catch {
      return null;
    }
  }
}

async function safeJson(res: Response): Promise<any> {
  try { return await res.json(); } catch { return null; }
}

export default MobileClient;
