/**
 * policy — the GENERIC company/team AI policy + runtime resolver surface
 * (@yaver/server). This is the spine that lets any app embed Yaver as a
 * policy-driven, multi-runner, multi-provider control plane — the "OpenRouter
 * of coding agents" pattern:
 *
 *   - Runner   = the agent CLI (claude-code / codex / opencode / aider).
 *   - Provider = the model backend the runner talks to (anthropic / openai /
 *                openrouter / gemini / ollama / salad / on-prem vLLM …).
 *                Providers are wrapped via OpenCode BYOK or a custom runner.
 *
 * The policy decides WHICH runner and WHICH provider a given user/role may use,
 * and the resolver projects that policy onto a concrete runtime (device +
 * runner + model + provider + approvals) for a requested unit of work.
 *
 * NOTHING here is app-specific. An app (Talos, carrotbet, …) contributes an
 * `AppProfile` describing its own work kinds + role permissions; the resolver
 * treats `workKind` as an opaque string validated against that profile. The
 * Talos vocabulary (harness-cad, robot-trial, …) is just one profile.
 *
 * Enforcement note: this module RESOLVES policy; it does not enforce it. The
 * server hands the client only what the resolution allows, and the agent (which
 * holds the scoped token) is the authoritative enforcer. Never trust a client
 * to self-limit.
 */

export type TenantComputeProvider =
  | 'hetzner'
  | 'aws'
  | 'gcp'
  | 'azure'
  | 'onprem'
  | 'byo-yaver-device';

export type RuntimeMode = 'dedicated-compute' | 'bring-your-own-yaver' | 'local-only';

export type CredentialMode =
  | 'user-auth-on-runtime'
  | 'company-api-key-on-runtime'
  | 'local-model-on-runtime'
  | 'external-onprem-endpoint';

export type ProviderKeyPolicy = 'company-secret' | 'user-secret' | 'none';

/** A model backend the runner can be pointed at (the "wrap" target). */
export interface ProviderDef {
  /** Stable id, e.g. "openrouter", "gemini", "ollama", "salad", "acme-vllm". */
  id: string;
  label: string;
  /** OpenAI-compatible base URL for OpenRouter/Ollama/Salad/on-prem. */
  baseUrl?: string;
  models: string[];
  keyPolicy: ProviderKeyPolicy;
  /** Whether a key is present on the runtime (never the key itself). */
  keyConfigured?: boolean;
}

/**
 * A unit of work an app supports. Generic — the app names its own kinds.
 * Replaces the hardcoded Talos `workKinds` booleans for new consumers while
 * staying compatible with them (see CompanyAIOptions.workKinds).
 */
export interface WorkKindDef {
  /** Generic key, e.g. "app-code", "harness-cad", "carrotbet-odds". */
  key: string;
  label?: string;
  enabled?: boolean;
  requiredTools?: string[];
  requiredMcp?: string[];
  /** Approval gates this work kind always needs, e.g. "deploy", "robot-motion". */
  approvals?: string[];
  promptHints?: string[];
  artifactKinds?: string[];
  /** Roles permitted to run this kind. Empty/undefined → any member. */
  allowedRoles?: string[];
}

/** Per-role caps that narrow company policy. */
export interface RolePolicy {
  role: string;
  allowedTools?: string[];
  allowedRunners?: string[];
  allowedProviders?: string[];
  allowedWorkKinds?: string[];
}

/**
 * An app's contribution to the generic policy. Registered once per consuming
 * app; the resolver consults it instead of baking app vocabulary into Yaver.
 */
export interface AppProfile {
  /** App slug, e.g. "talos", "carrotbet". */
  app: string;
  workKinds: WorkKindDef[];
  roles?: RolePolicy[];
  /** Provider catalog the app pre-registers (still gated by role + key policy). */
  providers?: ProviderDef[];
}

/**
 * Company/team-level AI policy. Mirrors the Convex `companyAIOptions` shape so
 * the SDK and the dashboard agree on one schema. Configuration only — never
 * secrets (keys live on the runtime / vault / provider store).
 */
export interface CompanyAIOptions {
  enabled: boolean;
  runtime: {
    mode: RuntimeMode;
    defaultProvider: TenantComputeProvider;
    defaultDeviceId?: string;
    fallbackDeviceIds?: string[];
    region?: string;
  };
  convex: {
    deploymentKind: 'dedicated' | 'shared-isolated' | 'external';
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
    credentialMode: CredentialMode;
  };
  /** Provider catalog + default OpenCode agent (build/plan/review). */
  opencode?: {
    providers: ProviderDef[];
    defaultAgent?: string;
  };
  mcp: {
    enabledServers: string[];
    requiredServers: string[];
    toolPolicyByRole?: Array<{ role: string; allowedTools: string[] }>;
  };
  /**
   * Legacy fixed work-kind toggles (kept for the existing dashboard). New apps
   * should prefer `appProfile.workKinds` — the resolver accepts either.
   */
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
  /** Generic app profile — the de-Talos-ified path. Optional for back-compat. */
  appProfile?: AppProfile;
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

export type ResolveSource =
  | 'talos-web' | 'talos-mobile' | 'talos-desktop'
  | 'yaver-web' | 'yaver-mobile' | 'yaver-desktop'
  | 'mcp' | 'api' | string;

export interface ResolveRequest {
  teamId: string;
  /** Generic — any app-defined work-kind key. */
  workKind: string;
  requestedRunner?: string;
  requestedModel?: string;
  requestedProvider?: string;
  requestedDeviceId?: string;
  /** Caller's role hint; the server still authorizes against membership. */
  userRole?: string;
  source?: ResolveSource;
}

/**
 * The single payload the UI, audit log, and the agent dispatch all agree on.
 * Superset of the Convex `resolveForToken` output, with a `provider` block.
 */
export interface ResolvedSession {
  ok: boolean;
  teamId: string;
  role: string;
  source: string;
  workKind: string;
  enabled: boolean;
  workKindEnabled: boolean;
  runtimeReady: boolean;
  runtime: {
    mode: RuntimeMode;
    provider: TenantComputeProvider;
    region?: string;
    deviceId: string | null;
    fallbackDeviceIds: string[];
  };
  convex: CompanyAIOptions['convex'];
  runner: {
    id: string;
    model?: string;
    allowedRunners: string[];
    credentialMode: CredentialMode;
    requireRunnerAuthPerUser: boolean;
    allowUserOverride: boolean;
  };
  /** The resolved model backend the runner should target (BYOK/on-prem). */
  provider?: {
    id: string | null;
    label?: string;
    baseUrl?: string;
    keyPolicy?: ProviderKeyPolicy;
    keyConfigured?: boolean;
    allowedProviders: string[];
  };
  mcp: CompanyAIOptions['mcp'];
  approvals: CompanyAIOptions['approvals'] & { required: string[] };
  dataPolicy: CompanyAIOptions['dataPolicy'];
  promptPolicy: { systemHints: string[]; artifactKinds: string[] };
  nextActions: {
    configureCompanyAI: boolean;
    configureRuntimeDevice: boolean;
    enableWorkKind: boolean;
    reauthRunner: boolean;
    /** Set when the resolved provider needs a key that isn't configured. */
    configureProviderKey?: boolean;
  };
  dispatch: {
    target: string;
    deviceId: string | null;
    createTaskPath: string;
    runnerSwitchPath: string;
    runnerStatusPath: string;
    taskOutputPathTemplate: string;
    /**
     * OAuth-first runner credential flow on the resolved runtime. Yaver wraps
     * Claude Code / Codex / OpenCode via the user's subscription OAuth
     * (`--claudeai` / ChatGPT) — never API keys. A client that finds a runner
     * unauthenticated (statusPath) starts the browser flow on the runtime, or
     * mirrors the owner's local creds (credentialsImportPath). Device-targeted
     * runtimes reach these through the agent peer proxy.
     */
    runnerAuth?: {
      statusPath: string;
      browserStartPath: string;
      browserStatusPath: string;
      browserSubmitCodePath: string;
      browserCancelPath: string;
      credentialsImportPath: string;
    };
  };
}

// ── Pure policy helpers (client-side mirror; server stays authoritative) ──
// These let a UI optimistically render the same choice the resolver will make,
// without a round-trip. They MUST mirror Convex `companyAIOptions.ts`. The
// server's result always wins; never gate security on these.

/** Roles a row's per-role policy permits for a key, or undefined = unrestricted. */
function roleAllowList(
  byRole: Array<{ role: string; allowedTools?: string[]; allowedRunners?: string[]; allowedProviders?: string[] }> | undefined,
  role: string | undefined,
  field: 'allowedRunners' | 'allowedProviders',
): string[] | undefined {
  if (!byRole || !role) return undefined;
  const entry = byRole.find((r) => r.role === role);
  const list = entry?.[field];
  return list && list.length > 0 ? list : undefined;
}

/** Pick the effective runner under company + role policy. */
export function selectRunner(options: CompanyAIOptions, requestedRunner?: string, role?: string): string {
  let allowed = options.runners.allowedRunners.length
    ? options.runners.allowedRunners
    : [options.runners.defaultRunner || 'opencode'];
  const roleCap = roleAllowList(options.appProfile?.roles, role, 'allowedRunners');
  if (roleCap) allowed = allowed.filter((r) => roleCap.includes(r));
  if (allowed.length === 0) allowed = [options.runners.defaultRunner || 'opencode'];
  if (requestedRunner && options.runners.allowUserOverride && allowed.includes(requestedRunner)) return requestedRunner;
  if (allowed.includes(options.runners.defaultRunner)) return options.runners.defaultRunner;
  return allowed[0];
}

/** Pick the effective model backend (BYOK/on-prem provider) under policy. */
export function selectProvider(
  options: CompanyAIOptions,
  requestedProvider?: string,
  role?: string,
): ProviderDef | null {
  const catalog: ProviderDef[] = [
    ...(options.opencode?.providers ?? []),
    ...(options.appProfile?.providers ?? []),
  ];
  if (catalog.length === 0) return null;
  let allowedIds = catalog.map((p) => p.id);
  const roleCap = roleAllowList(options.appProfile?.roles, role, 'allowedProviders');
  if (roleCap) allowedIds = allowedIds.filter((id) => roleCap.includes(id));
  const pickable = catalog.filter((p) => allowedIds.includes(p.id));
  if (pickable.length === 0) return null;
  if (requestedProvider && options.runners.allowUserOverride) {
    const match = pickable.find((p) => p.id === requestedProvider);
    if (match) return match;
  }
  return pickable[0];
}

/** Map a generic work-kind key to its enabled flag, profile-first then legacy. */
export function isWorkKindEnabled(options: CompanyAIOptions, workKind: string): boolean {
  const def = options.appProfile?.workKinds.find((w) => w.key === workKind);
  if (def) return def.enabled !== false;
  const legacy: Record<string, keyof CompanyAIOptions['workKinds']> = {
    'app-code': 'appCode', 'erp-flow': 'erpFlow', convex: 'convex', 'web-ui': 'webUi',
    'harness-cad': 'harnessCad', 'openscad-cad': 'openScadCad', 'robot-trial': 'robotTrial', inspection: 'inspection',
  };
  const key = legacy[workKind];
  return key ? Boolean(options.workKinds[key]) : false;
}

/**
 * Thin client over the Convex `/company-ai/*` HTTP routes. Holds a Yaver bearer
 * (account or scoped session) and reads/writes policy + resolves runtime. Lives
 * server-side in YaverApp, but is safe to use anywhere a valid token exists.
 */
export class YaverPolicyClient {
  readonly convexUrl: string;
  private readonly token: string;

  constructor(token: string, convexUrl: string) {
    this.token = token;
    this.convexUrl = convexUrl.replace(/\/+$/, '');
  }

  /** Read a team's policy (safe defaults when unconfigured). */
  async getOptions(teamId: string): Promise<CompanyAIOptionsResponse> {
    const res = await fetch(`${this.convexUrl}/company-ai/options?teamId=${encodeURIComponent(teamId)}`, {
      headers: { Authorization: `Bearer ${this.token}` },
    });
    if (!res.ok) throw new Error(`/company-ai/options -> HTTP ${res.status}`);
    return res.json();
  }

  /** Write a team's policy. Server enforces admin/owner role. */
  async setOptions(teamId: string, options: CompanyAIOptions): Promise<{ ok: boolean; id?: string; error?: string }> {
    const res = await fetch(`${this.convexUrl}/company-ai/options`, {
      method: 'POST',
      headers: { Authorization: `Bearer ${this.token}`, 'Content-Type': 'application/json' },
      body: JSON.stringify({ teamId, options }),
    });
    if (!res.ok) {
      const text = await res.text().catch(() => '');
      return { ok: false, error: `HTTP ${res.status}: ${text}` };
    }
    return res.json();
  }

  /** Resolve a concrete runtime for a unit of work. Returns no secrets. */
  async resolve(req: ResolveRequest): Promise<ResolvedSession> {
    const res = await fetch(`${this.convexUrl}/company-ai/resolve`, {
      method: 'POST',
      headers: { Authorization: `Bearer ${this.token}`, 'Content-Type': 'application/json' },
      body: JSON.stringify(req),
    });
    if (!res.ok) throw new Error(`/company-ai/resolve -> HTTP ${res.status}`);
    return res.json();
  }
}
