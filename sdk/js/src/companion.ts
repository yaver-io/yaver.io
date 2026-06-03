// companion.ts — thin client for the agent's companion-compute surface
// (yaver.companion.yaml: crons + workers for serverless projects). Standalone
// on purpose: it talks to a single resolved agent baseURL with a bearer token
// and does not depend on policy.ts / acl.ts / connect.ts, so it composes with
// whatever transport the caller already resolved.

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
  manifest: unknown;
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

export interface CompanionClientOptions {
  /** Resolved agent base URL, e.g. https://box:18443 or a relay /d/<id> prefix. */
  baseURL: string;
  /** Bearer token for the agent. */
  token: string;
  /** Optional custom fetch (Node < 18, tests). Defaults to global fetch. */
  fetchImpl?: typeof fetch;
}

export class CompanionClient {
  private baseURL: string;
  private token: string;
  private fetchImpl: typeof fetch;

  constructor(opts: CompanionClientOptions) {
    this.baseURL = opts.baseURL.replace(/\/$/, '');
    this.token = opts.token;
    this.fetchImpl = opts.fetchImpl ?? fetch;
  }

  private async req(path: string, init: RequestInit = {}): Promise<any> {
    const res = await this.fetchImpl(`${this.baseURL}${path}`, {
      ...init,
      headers: {
        Authorization: `Bearer ${this.token}`,
        ...(init.body ? { 'Content-Type': 'application/json' } : {}),
        ...(init.headers as Record<string, string> | undefined),
      },
    });
    const data = await res.json().catch(() => ({}));
    if (data && data.error) throw new Error(data.error);
    if (!res.ok) throw new Error(`companion ${path} ${res.status}`);
    return data;
  }

  /** List all companion projects armed on this agent. */
  async list(): Promise<CompanionProjectSummary[]> {
    const data = await this.req('/companion/list');
    return Array.isArray(data.projects) ? data.projects : [];
  }

  /** Scan a serverless repo and propose a manifest. Read-only. */
  async detect(repo: string): Promise<CompanionDetectResult> {
    return (await this.req(`/companion/detect?repo=${encodeURIComponent(repo)}`)) as CompanionDetectResult;
  }

  /** Read the repo's current yaver.companion.yaml, if any. */
  async getManifest(repo: string): Promise<{ exists: boolean; manifestYaml?: string }> {
    return await this.req(`/companion/manifest?repo=${encodeURIComponent(repo)}`);
  }

  /** Write a confirmed manifest to the repo (explicit user action). */
  async writeManifest(repo: string, manifestYaml: string): Promise<{ ok?: boolean; path?: string }> {
    return await this.req('/companion/manifest', {
      method: 'POST',
      body: JSON.stringify({ repo, manifestYaml }),
    });
  }

  /** Arm the manifest at repo (idempotent, reboot-durable). */
  async up(repo: string): Promise<CompanionStatus> {
    const data = await this.req('/companion/up', { method: 'POST', body: JSON.stringify({ repo }) });
    return data.status as CompanionStatus;
  }

  /** Disarm a companion project. */
  async down(project: string): Promise<void> {
    await this.req('/companion/down', { method: 'POST', body: JSON.stringify({ project }) });
  }

  /** Live status for a companion project. */
  async status(project: string): Promise<CompanionStatus> {
    const data = await this.req(`/companion/status?project=${encodeURIComponent(project)}`);
    return data.status as CompanionStatus;
  }
}
