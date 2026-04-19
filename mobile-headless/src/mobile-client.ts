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

export interface MobileClientOptions {
  dataDir?: string;
  convexUrl?: string;
  authToken?: string;
  platform?: "ios" | "android";
  deviceName?: string;
  /** Override the agent base URL. When absent, MobileClient still
   *  works for Convex-only calls (listDevices, etc.) but agent
   *  endpoints need `connect()` first. */
  agentBaseUrl?: string;
}

type Device = { id: string; name: string; host?: string; port?: number; online?: boolean; deviceClass?: string; os?: string };

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
    };
    this.agentBaseUrl = opts.agentBaseUrl;
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
    const body = await res.json() as { devices: Device[] };
    return body.devices ?? [];
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

  // ── Wizard ─────────────────────────────────────────────────────
  readonly wizard = {
    start: async () => (await this.raw.post("/project/wizard/start")).body,
    questions: async () => (await this.raw.get("/project/wizard/questions")).body,
    answer: async (sessionId: string, questionId: string, answer: string) =>
      (await this.raw.post("/project/wizard/answer", { sessionId, questionId, answer })).body,
    generate: async (sessionId: string, parentDir?: string) =>
      (await this.raw.post("/project/wizard/generate", { sessionId, parentDir })).body,
  };

  // ── Guests (via Convex HTTP) ──────────────────────────────────
  readonly guests = {
    list: async () => {
      const res = await fetch(this.opts.convexUrl + "/guests/list", { headers: this.authHeaders() });
      if (!res.ok) throw new Error("listGuests: HTTP " + res.status);
      const body = await res.json() as { guests: any[] };
      return body.guests ?? [];
    },
    invite: async (email: string) => {
      const res = await fetch(this.opts.convexUrl + "/guests/invite", {
        method: "POST",
        headers: { ...this.authHeaders(), "Content-Type": "application/json" },
        body: JSON.stringify({ email }),
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
  };

  // ── Raw escape hatch ──────────────────────────────────────────
  readonly raw = {
    get: async (p: string) => {
      const res = await fetch(this.agentURL(p), { headers: this.authHeaders() });
      return { status: res.status, body: await safeJson(res) };
    },
    post: async (p: string, body?: any) => {
      const res = await fetch(this.agentURL(p), {
        method: "POST",
        headers: { ...this.authHeaders(), "Content-Type": "application/json" },
        body: body !== undefined ? JSON.stringify(body) : undefined,
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
  private peerPath(target: string | undefined, p: string): string {
    if (!target) return p;
    return `/peer/${encodeURIComponent(target)}${p}`;
  }
}

async function safeJson(res: Response): Promise<any> {
  try { return await res.json(); } catch { return null; }
}

export default MobileClient;
