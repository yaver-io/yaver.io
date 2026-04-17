import AsyncStorage from "@react-native-async-storage/async-storage";
import { quicClient } from "./quic";
import {
  browseLocalPhoneTable,
  deleteLocalPhoneProject,
  deleteLocalPhoneRow,
  dumpLocalPhoneProjectRows,
  ensureLocalPhoneProject,
  getLocalPhoneProjectMeta,
  insertLocalPhoneRow,
  listLocalPhoneProjectsMeta,
  updateLocalPhoneRow,
} from "./phoneSandboxLocal";

// Mirrors desktop/agent/phone_backend.go. Keep these types in sync.

export type PhoneColumnType =
  | "text"
  | "int"
  | "bool"
  | "real"
  | "timestamp"
  | "json"
  | "uuid";

export interface PhoneColumn {
  name: string;
  type: PhoneColumnType | string;
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
}

export type PhonePromoteTarget = string;

export type PhoneBackendKind = "local" | "dev-hw" | "yaver-cloud" | "custom";

export interface PhoneProjectAccess {
  sourceSlug: string;
  slug: string;
  kind: PhoneBackendKind;
  label: string;
  baseUrl?: string;
  boundAt?: string;
}

async function syncLocalPhoneProjectToConnectedAgent(slug: string): Promise<void> {
  const local = await getLocalPhoneProjectMeta(slug);
  if (!local) return;
  const rowsByTable = await dumpLocalPhoneProjectRows(local);
  await post<{ ok?: boolean }>("/phone/projects/delete", { slug }).catch(() => null);
  const created = await createPhoneProject({
    slug: local.slug,
    name: local.name,
    template: local.template,
    schema: local.schema ?? undefined,
    auth: local.auth ?? undefined,
    seed: local.seed ?? undefined,
    app: local.app ?? undefined,
  });
  if (!created) return;
  for (const [table, rows] of Object.entries(rowsByTable)) {
    for (const row of rows) {
      await post<{ id: string }>("/phone/projects/insert", { slug, table, doc: row });
    }
  }
}

interface StoredPhoneProjectBinding {
  version: 1;
  slug: string;
  kind: Exclude<PhoneBackendKind, "local">;
  label: string;
  baseUrl: string;
  boundAt: string;
}

const PHONE_BACKEND_BINDING_PREFIX = "@yaver/phone_backend_binding/";

function headers(): Record<string, string> | null {
  if (!quicClient.isConnected) return null;
  return quicClient.morningAuthHeaders();
}

function baseUrl(): string | null {
  return quicClient.isConnected && quicClient.baseUrl ? quicClient.baseUrl : null;
}

async function get<T>(path: string): Promise<T | null> {
  const h = headers();
  const url = baseUrl();
  if (!h || !url) return null;
  try {
    const res = await fetch(`${url}${path}`, { headers: h });
    if (!res.ok) return null;
    return (await res.json()) as T;
  } catch {
    return null;
  }
}

async function post<T>(path: string, body: unknown): Promise<T | null> {
  const h = headers();
  const url = baseUrl();
  if (!h || !url) return null;
  try {
    const res = await fetch(`${url}${path}`, {
      method: "POST",
      headers: { ...h, "Content-Type": "application/json" },
      body: JSON.stringify(body),
    });
    if (!res.ok) {
      const text = await res.text().catch(() => "");
      throw new Error(text || `HTTP ${res.status}`);
    }
    return (await res.json()) as T;
  } catch (err) {
    throw err;
  }
}

async function getFromBase<T>(base: string, path: string): Promise<T | null> {
  const h = headers();
  if (!h) return null;
  try {
    const res = await fetch(`${base}${path}`, { headers: h });
    if (!res.ok) {
      const text = await res.text().catch(() => "");
      throw new Error(text || `HTTP ${res.status}`);
    }
    return (await res.json()) as T;
  } catch (err) {
    throw err;
  }
}

async function postToBase<T>(base: string, path: string, body: unknown): Promise<T | null> {
  const h = headers();
  if (!h) return null;
  try {
    const res = await fetch(`${base}${path}`, {
      method: "POST",
      headers: { ...h, "Content-Type": "application/json" },
      body: JSON.stringify(body),
    });
    if (!res.ok) {
      const text = await res.text().catch(() => "");
      throw new Error(text || `HTTP ${res.status}`);
    }
    return (await res.json()) as T;
  } catch (err) {
    throw err;
  }
}

function bindingKey(sourceSlug: string): string {
  return `${PHONE_BACKEND_BINDING_PREFIX}${encodeURIComponent(sourceSlug)}`;
}

export async function getPhoneProjectAccess(sourceSlug: string): Promise<PhoneProjectAccess> {
  try {
    const raw = await AsyncStorage.getItem(bindingKey(sourceSlug));
    if (!raw) {
      return { sourceSlug, slug: sourceSlug, kind: "local", label: "On-device SQLite" };
    }
    const parsed = JSON.parse(raw) as StoredPhoneProjectBinding;
    if (!parsed?.baseUrl || !parsed?.slug || !parsed?.kind) {
      return { sourceSlug, slug: sourceSlug, kind: "local", label: "On-device SQLite" };
    }
    return {
      sourceSlug,
      slug: parsed.slug,
      kind: parsed.kind,
      label: parsed.label,
      baseUrl: parsed.baseUrl,
      boundAt: parsed.boundAt,
    };
  } catch {
    return { sourceSlug, slug: sourceSlug, kind: "local", label: "On-device SQLite" };
  }
}

export async function bindPhoneProjectToTarget(
  sourceSlug: string,
  target: PhonePushTarget,
  result: PhonePushResult,
  label: string,
): Promise<void> {
  const baseUrl = resolveTargetBase(target);
  const value: StoredPhoneProjectBinding = {
    version: 1,
    slug: result.slug,
    kind: target.kind,
    label,
    baseUrl,
    boundAt: new Date().toISOString(),
  };
  await AsyncStorage.setItem(bindingKey(sourceSlug), JSON.stringify(value));
}

export async function clearPhoneProjectBinding(sourceSlug: string): Promise<void> {
  await AsyncStorage.removeItem(bindingKey(sourceSlug));
}

export async function listPhoneProjects(): Promise<PhoneProject[]> {
  const local = await listLocalPhoneProjectsMeta().catch(() => []);
  const r = await get<{ projects?: PhoneProject[]; error?: string }>(
    "/phone/projects/list",
  );
  if (!r) return local;
  if (r.error) throw new Error(r.error);
  const remote = r.projects ?? [];
  const merged = new Map<string, PhoneProject>();
  local.forEach((project) => merged.set(project.slug, project));
  remote.forEach((project) => merged.set(project.slug, project));
  return Array.from(merged.values());
}

export async function listPhoneTemplates(): Promise<PhoneTemplate[]> {
  const r = await get<{ templates: PhoneTemplate[] }>(
    "/phone/projects/templates",
  );
  return r?.templates ?? [];
}

export async function createPhoneProject(
  spec: PhoneCreateSpec,
): Promise<PhoneProject | null> {
  const r = await post<PhoneProject>("/phone/projects/create", spec);
  if (r) {
    await ensureLocalPhoneProject(r).catch(() => undefined);
  }
  return r;
}

/** Create a new phone-backend project on a *different* agent than the one
 *  we're currently connected to. Used by the "Create project" flow in
 *  mobile/app/phone-projects.tsx when the user picks [Your Dev Machine] or
 *  [Yaver Cloud] as the start-point (yc.md §Wedge Demo). Goes through the
 *  same auth headers as the local agent; the three tiers in the Yaver-native
 *  continuum share the same owner-token model.
 *
 *  Use `pushPhoneProject` when you already have a local project and want to
 *  replicate it — this is the greenfield case. */
export async function createPhoneProjectAt(
  target: PhonePushTarget,
  spec: PhoneCreateSpec,
): Promise<PhoneProject> {
  const h = headers();
  if (!h) throw new Error("no source agent connected");
  const base = resolvePhonePushTargetBase(target);
  const res = await fetch(`${base}/phone/projects/create`, {
    method: "POST",
    headers: { ...h, "Content-Type": "application/json" },
    body: JSON.stringify(spec),
  });
  const body = await res.text().catch(() => "");
  if (!res.ok) {
    throw new Error(body || `HTTP ${res.status}`);
  }
  return JSON.parse(body) as PhoneProject;
}

function resolvePhonePushTargetBase(target: PhonePushTarget): string {
  switch (target.kind) {
    case "dev-hw":
      return `${target.relayHttpUrl.replace(/\/$/, "")}/d/${target.deviceId}`;
    case "yaver-cloud":
      return (target.cloudBaseUrl ?? "https://cloud.yaver.io").replace(/\/$/, "");
    case "custom":
      return target.baseUrl.replace(/\/$/, "");
  }
}

export async function getPhoneProject(
  slug: string,
  access?: PhoneProjectAccess | null,
): Promise<PhoneProject | null> {
  if (!access || access.kind === "local") {
    const local = await getLocalPhoneProjectMeta(slug).catch(() => null);
    if (local) return local;
  }
  const effectiveSlug = access?.slug ?? slug;
  const path = `/phone/projects/get?slug=${encodeURIComponent(effectiveSlug)}`;
  if (access?.baseUrl) {
    return getFromBase<PhoneProject>(access.baseUrl, path);
  }
  const project = await get<PhoneProject>(path);
  if (project && (!access || access.kind === "local")) {
    await ensureLocalPhoneProject(project).catch(() => undefined);
  }
  return project;
}

export async function deletePhoneProject(
  slug: string,
  access?: PhoneProjectAccess | null,
): Promise<boolean> {
  if (!access || access.kind === "local") {
    await deleteLocalPhoneProject(slug).catch(() => undefined);
  }
  const effectiveSlug = access?.slug ?? slug;
  const r = access?.baseUrl
    ? await postToBase<{ ok?: boolean }>(access.baseUrl, "/phone/projects/delete", {
        slug: effectiveSlug,
      })
    : await post<{ ok?: boolean }>("/phone/projects/delete", { slug: effectiveSlug });
  return !!r?.ok;
}

export async function setPhoneSchema(
  slug: string,
  schema: PhoneSchema,
): Promise<PhoneProject | null> {
  return post<PhoneProject>("/phone/projects/schema", { slug, schema });
}

export async function setPhoneAuth(slug: string, auth: PhoneAuth): Promise<boolean> {
  const r = await post<{ ok?: boolean }>("/phone/projects/auth", { slug, auth });
  return !!r?.ok;
}

export async function setPhoneSeed(slug: string, seed: PhoneSeed): Promise<boolean> {
  const r = await post<{ ok?: boolean }>("/phone/projects/seed", { slug, seed });
  return !!r?.ok;
}

export async function listPhoneTables(slug: string): Promise<Array<{ name: string; rowCount?: number }>> {
  return listPhoneTablesAt(slug);
}

export async function listPhoneTablesAt(
  slug: string,
  access?: PhoneProjectAccess | null,
): Promise<Array<{ name: string; rowCount?: number }>> {
  const effectiveSlug = access?.slug ?? slug;
  const path = `/phone/projects/tables?slug=${encodeURIComponent(effectiveSlug)}`;
  const r = access?.baseUrl
    ? await getFromBase<{ tables?: Array<{ name: string; rowCount?: number }>; error?: string }>(access.baseUrl, path)
    : await get<{ tables?: Array<{ name: string; rowCount?: number }>; error?: string }>(path);
  if (!r) return [];
  if (r.error) throw new Error(r.error);
  return r.tables ?? [];
}

export interface PhoneBrowseResult {
  rows: Array<Record<string, unknown>>;
  nextCursor?: string;
  total?: number;
}

export async function browsePhoneTable(
  slug: string,
  table: string,
  cursor = "",
  limit = 50,
  access?: PhoneProjectAccess | null,
): Promise<PhoneBrowseResult | null> {
  if (!access || access.kind === "local") {
    const rows = await browseLocalPhoneTable(slug, table, limit).catch(() => null);
    if (rows) return { rows, total: rows.length };
  }
  const params = new URLSearchParams({
    slug: access?.slug ?? slug,
    table,
    cursor,
    limit: String(limit),
  });
  const path = `/phone/projects/browse?${params.toString()}`;
  return access?.baseUrl
    ? getFromBase<PhoneBrowseResult>(access.baseUrl, path)
    : get<PhoneBrowseResult>(path);
}

export async function insertPhoneRow(
  slug: string,
  table: string,
  doc: Record<string, unknown>,
  access?: PhoneProjectAccess | null,
): Promise<string | null> {
  if (!access || access.kind === "local") {
    const id = await insertLocalPhoneRow(slug, table, doc).catch(() => null);
    if (id) return id;
  }
  const effectiveSlug = access?.slug ?? slug;
  const r = access?.baseUrl
    ? await postToBase<{ id: string }>(access.baseUrl, "/phone/projects/insert", {
        slug: effectiveSlug,
        table,
        doc,
      })
    : await post<{ id: string }>("/phone/projects/insert", { slug: effectiveSlug, table, doc });
  return r?.id ?? null;
}

export async function updatePhoneRow(
  slug: string,
  table: string,
  id: string,
  fields: Record<string, unknown>,
  access?: PhoneProjectAccess | null,
): Promise<boolean> {
  if (!access || access.kind === "local") {
    const ok = await updateLocalPhoneRow(slug, table, id, fields)
      .then(() => true)
      .catch(() => false);
    if (ok) return true;
  }
  const effectiveSlug = access?.slug ?? slug;
  const r = access?.baseUrl
    ? await postToBase<{ ok?: boolean }>(access.baseUrl, "/phone/projects/update", {
        slug: effectiveSlug,
        table,
        id,
        fields,
      })
    : await post<{ ok?: boolean }>("/phone/projects/update", {
        slug: effectiveSlug,
        table,
        id,
        fields,
      });
  return !!r?.ok;
}

export async function deletePhoneRow(
  slug: string,
  table: string,
  id: string,
  access?: PhoneProjectAccess | null,
): Promise<boolean> {
  if (!access || access.kind === "local") {
    const ok = await deleteLocalPhoneRow(slug, table, id)
      .then(() => true)
      .catch(() => false);
    if (ok) return true;
  }
  const effectiveSlug = access?.slug ?? slug;
  const r = access?.baseUrl
    ? await postToBase<{ ok?: boolean }>(access.baseUrl, "/phone/projects/delete-row", {
        slug: effectiveSlug,
        table,
        id,
      })
    : await post<{ ok?: boolean }>("/phone/projects/delete-row", {
        slug: effectiveSlug,
        table,
        id,
      });
  return !!r?.ok;
}

export async function queryPhoneProject(
  slug: string,
  query: string,
  args?: Record<string, unknown>,
  access?: PhoneProjectAccess | null,
): Promise<unknown> {
  const effectiveSlug = access?.slug ?? slug;
  const r = access?.baseUrl
    ? await postToBase<{ result: unknown }>(access.baseUrl, "/phone/projects/query", {
        slug: effectiveSlug,
        query,
        args,
      })
    : await post<{ result: unknown }>("/phone/projects/query", { slug: effectiveSlug, query, args });
  return r?.result ?? null;
}

// ---- OAuth provider config (per-project) ----
//
// Mirrors desktop/agent/phone_oauth.go. The developer's OWN OAuth
// credentials for Sign in with Apple / Google / Microsoft so end-users of
// THEIR app can sign in. Secrets are stored in plaintext in the project
// directory (0600 file perms) and travel with the push/promote path.

export interface PhoneOAuthApple {
  teamId?: string;
  servicesId?: string;
  keyId?: string;
  privateKey?: string;
  redirectURIs?: string[];
}

export interface PhoneOAuthGoogle {
  clientId?: string;
  clientSecret?: string;
  redirectURIs?: string[];
}

export interface PhoneOAuthMicrosoft {
  tenantId?: string;
  clientId?: string;
  clientSecret?: string;
  redirectURIs?: string[];
}

export interface PhoneOAuthConfig {
  apple?: PhoneOAuthApple;
  google?: PhoneOAuthGoogle;
  microsoft?: PhoneOAuthMicrosoft;
}

export interface PhoneOAuthStatus {
  apple: boolean;
  google: boolean;
  microsoft: boolean;
}

export interface PhoneOAuthResponse {
  config: PhoneOAuthConfig;
  status: PhoneOAuthStatus;
}

// ---- Per-project API tokens (pair with desktop/agent/phone_tokens.go) ----

export interface PhoneProjectTokenSummary {
  id: string;
  label: string;
  createdAt: string;
  lastUsed?: string;
}

export interface PhoneProjectTokenMint {
  token: PhoneProjectTokenSummary;
  raw: string;
}

export async function listPhoneProjectTokens(slug: string): Promise<PhoneProjectTokenSummary[]> {
  const r = await get<{ tokens?: PhoneProjectTokenSummary[]; error?: string }>(
    `/phone/projects/tokens?slug=${encodeURIComponent(slug)}`,
  );
  if (!r) return [];
  if (r.error) throw new Error(r.error);
  return r.tokens ?? [];
}

export async function mintPhoneProjectToken(
  slug: string,
  label: string,
): Promise<PhoneProjectTokenMint | null> {
  return post<PhoneProjectTokenMint>("/phone/projects/tokens", { slug, label });
}

export async function revokePhoneProjectToken(slug: string, tokenId: string): Promise<boolean> {
  const h = headers();
  const url = baseUrl();
  if (!h || !url) return false;
  const params = new URLSearchParams({ slug, tokenId });
  const res = await fetch(
    `${url}/phone/projects/tokens?${params.toString()}`,
    { method: "DELETE", headers: h },
  );
  return res.ok;
}

export async function getPhoneOAuth(slug: string): Promise<PhoneOAuthResponse | null> {
  return get<PhoneOAuthResponse>(`/phone/projects/oauth?slug=${encodeURIComponent(slug)}`);
}

/** Merge a partial patch into the project's OAuth config. Omitted providers
 *  are left alone; an explicit empty object clears a provider. */
export async function setPhoneOAuth(
  slug: string,
  patch: PhoneOAuthConfig,
): Promise<PhoneOAuthResponse | null> {
  return post<PhoneOAuthResponse>("/phone/projects/oauth", { slug, config: patch });
}

// ---- Cloudflare DNS helpers (pair with desktop/agent/cloudflare_dns.go) ----
//
// All three endpoints take the user's Cloudflare API token via X-CF-Token —
// the agent never persists it. Mobile caches the token in the vault on the
// phone side so the user pastes it once.

export interface CFZone {
  id: string;
  name: string;
  status?: string;
}

export interface CFRecord {
  id: string;
  type: string;
  name: string;
  content: string;
  ttl?: number;
  proxied?: boolean;
  comment?: string;
}

export interface CFRecordInput {
  type: string;
  name: string;
  content: string;
  ttl?: number;
  proxied?: boolean;
  comment?: string;
}

export interface CFTokenStatus {
  valid: boolean;
  status?: string;
  message?: string;
}

function cfHeaders(token: string): Record<string, string> | null {
  const base = headers();
  if (!base) return null;
  return { ...base, "X-CF-Token": token };
}

export async function verifyCloudflareToken(token: string): Promise<CFTokenStatus | null> {
  const url = baseUrl();
  const h = cfHeaders(token);
  if (!url || !h) return null;
  try {
    const res = await fetch(`${url}/dns/cloudflare/verify`, {
      method: "POST",
      headers: { ...h, "Content-Type": "application/json" },
      body: JSON.stringify({}),
    });
    if (!res.ok) return { valid: false, message: `HTTP ${res.status}` };
    return (await res.json()) as CFTokenStatus;
  } catch (e) {
    return { valid: false, message: String(e) };
  }
}

export async function listCloudflareZones(token: string): Promise<CFZone[]> {
  const url = baseUrl();
  const h = cfHeaders(token);
  if (!url || !h) return [];
  const res = await fetch(`${url}/dns/cloudflare/zones`, { headers: h });
  if (!res.ok) throw new Error(await res.text().catch(() => `HTTP ${res.status}`));
  const data = (await res.json()) as { zones?: CFZone[] };
  return data.zones ?? [];
}

export async function listCloudflareRecords(token: string, zoneId: string): Promise<CFRecord[]> {
  const url = baseUrl();
  const h = cfHeaders(token);
  if (!url || !h) return [];
  const params = new URLSearchParams({ zoneId });
  const res = await fetch(`${url}/dns/cloudflare/records?${params}`, { headers: h });
  if (!res.ok) throw new Error(await res.text().catch(() => `HTTP ${res.status}`));
  const data = (await res.json()) as { records?: CFRecord[] };
  return data.records ?? [];
}

export async function createCloudflareRecord(token: string, zoneId: string, record: CFRecordInput): Promise<CFRecord> {
  const url = baseUrl();
  const h = cfHeaders(token);
  if (!url || !h) throw new Error("agent not reachable");
  const res = await fetch(`${url}/dns/cloudflare/records`, {
    method: "POST",
    headers: { ...h, "Content-Type": "application/json" },
    body: JSON.stringify({ zoneId, record }),
  });
  const text = await res.text().catch(() => "");
  if (!res.ok) throw new Error(text || `HTTP ${res.status}`);
  const body = JSON.parse(text) as { record: CFRecord };
  return body.record;
}

export async function deleteCloudflareRecord(token: string, zoneId: string, recordId: string): Promise<boolean> {
  const url = baseUrl();
  const h = cfHeaders(token);
  if (!url || !h) return false;
  const params = new URLSearchParams({ zoneId, recordId });
  const res = await fetch(`${url}/dns/cloudflare/records?${params}`, { method: "DELETE", headers: h });
  return res.ok;
}

// ---- Cost guardrails (pair with desktop/agent/phone_cost.go) ----
//
// The agent refuses to export OR receive a bundle over its configured cap
// (default 50 MB). The mobile UI fetches the cap + the per-target cost
// advisories so the user sees what a tap will cost BEFORE they commit.
// Same shape on both sides to keep drift low.

export type PhoneDeployTargetKind =
  | "this-device"
  | "dev-hw"
  | "yaver-cloud"
  | "cloudflare-workers"
  | "custom";

export interface PhoneDeployCostHint {
  targetKind: PhoneDeployTargetKind;
  label: string;
  free: string;
  overage: string;
  budget: string;
  advice: string;
}

export interface PhoneDeployCostHints {
  hints: PhoneDeployCostHint[];
  bundleCapBytes: number;
  bundleCapMB: number;
}

export async function phoneDeployCostHints(): Promise<PhoneDeployCostHints | null> {
  return get<PhoneDeployCostHints>("/phone/projects/cost-hint");
}

/** Fetch the project's bundle size (without downloading the body) via a
 *  HEAD request. Falls back to a GET-and-measure if HEAD isn't supported.
 *  Used by the mobile Deploy confirm so the user sees "About to upload
 *  1.2 MB to cloud.yaver.io" before they commit. */
export async function phoneBundleSize(slug: string, opts: { includeData?: boolean } = {}): Promise<number | null> {
  const h = headers();
  const url = baseUrl();
  if (!h || !url) return null;
  await syncLocalPhoneProjectToConnectedAgent(slug).catch(() => undefined);
  const q = new URLSearchParams({ slug });
  if (opts.includeData) q.set("includeData", "true");
  const target = `${url}/phone/projects/export?${q.toString()}`;
  try {
    const head = await fetch(target, { method: "HEAD", headers: h });
    const cl = head.headers.get("content-length");
    if (cl) {
      const n = Number(cl);
      if (Number.isFinite(n) && n > 0) return n;
    }
    // Fallback — cheap on this pipeline since bundles are tiny.
    const res = await fetch(target, { headers: h });
    if (!res.ok) return null;
    const buf = await res.arrayBuffer();
    return buf.byteLength;
  } catch {
    return null;
  }
}

// ---- Escape routes (pair with desktop/agent/phone_escape.go) ----
//
// Trust signal, not headline feature. Curated "I'm on X, get me to Y" rows
// that power the Advanced collapsible on the phone-project detail screen.

export interface EscapeRoute {
  id: string;
  fromBackend: string;
  fromLabel: string;
  toTargetId: string;
  toLabel: string;
  label: string;
  blurb: string;
  complexity?: "trivial" | "easy" | "medium" | "hard" | "";
  highlight?: boolean;
}

export async function listEscapeRoutes(opts: { from?: string; to?: string } = {}): Promise<EscapeRoute[]> {
  const params = new URLSearchParams();
  if (opts.from) params.set("from", opts.from);
  if (opts.to) params.set("to", opts.to);
  const r = await get<{ routes?: EscapeRoute[] }>(
    `/escape/routes${params.toString() ? "?" + params.toString() : ""}`,
  );
  return r?.routes ?? [];
}

export interface EscapePlanResult {
  route?: EscapeRoute;
  state?: {
    id: string;
    fromBackend: string;
    to: string;
    complexity: string;
    status: string;
    steps: Array<{ id: string; title: string; status: string; error?: string }>;
    rollbackExpiresAt?: string;
  };
  warning?: string;
  error?: string;
}

export async function planEscapeRoute(
  routeId: string,
  projectDir: string,
  opts: { run?: boolean; dryRun?: boolean } = {},
): Promise<EscapePlanResult | null> {
  return post<EscapePlanResult>("/escape/plan", {
    routeId,
    projectDir,
    run: !!opts.run,
    dryRun: !!opts.dryRun,
  });
}

export async function preparePhoneProjectExport(
  slug: string,
  access?: PhoneProjectAccess | null,
  opts: { includeData?: boolean; containerize?: boolean } = {},
): Promise<{ uri: string; headers: Record<string, string> } | null> {
  const h = headers();
  const url = access?.baseUrl ?? baseUrl();
  const effectiveSlug = access?.slug ?? slug;
  if (!h || !url) return null;
  if (!access || access.kind === "local") {
    await syncLocalPhoneProjectToConnectedAgent(slug).catch(() => undefined);
  }
  const q = new URLSearchParams({ slug: effectiveSlug });
  if (opts.includeData) q.set("includeData", "true");
  if (opts.containerize) q.set("containerize", "true");
  return {
    uri: `${url}/phone/projects/export?${q.toString()}`,
    headers: h,
  };
}

export interface PromoteResult {
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

export async function promotePhoneProject(
  slug: string,
  target: PhonePromoteTarget,
  opts: { run?: boolean; dryRun?: boolean } = {},
): Promise<PromoteResult | null> {
  return post<PromoteResult>("/phone/projects/promote", {
    slug,
    target,
    run: !!opts.run,
    dryRun: !!opts.dryRun,
  });
}

// ---- Push (export-and-receive) to a dev machine or Yaver cloud ----

export type PhonePushTarget =
  | { kind: "dev-hw"; deviceId: string; relayHttpUrl: string }
  | { kind: "yaver-cloud"; cloudBaseUrl?: string }
  | { kind: "custom"; baseUrl: string };

export interface PhonePushResult {
  slug: string;
  localUrl: string;
  browseUrl: string;
  project: PhoneProject;
}

const DEFAULT_YAVER_CLOUD_BASE = "https://cloud.yaver.io";

function resolveTargetBase(target: PhonePushTarget): string {
  switch (target.kind) {
    case "dev-hw":
      return `${target.relayHttpUrl.replace(/\/$/, "")}/d/${target.deviceId}`;
    case "yaver-cloud":
      return (target.cloudBaseUrl ?? DEFAULT_YAVER_CLOUD_BASE).replace(/\/$/, "");
    case "custom":
      return target.baseUrl.replace(/\/$/, "");
  }
}

/**
 * Export the given phone project from the locally connected agent and push
 * the resulting .tgz to `target`. Target agent's /phone/projects/receive
 * materialises it on its side and returns the new project handle.
 *
 * - `dev-hw` — another of the user's machines running `yaver serve`. Goes
 *   through the same relay we're already talking to.
 * - `yaver-cloud` — Yaver's managed Hetzner tenant. Same endpoint, different
 *   base URL. Paid tier.
 */
export async function pushPhoneProject(
  slug: string,
  target: PhonePushTarget,
  opts: { onConflict?: "reject" | "rename" | "overwrite"; skipSeed?: boolean; includeData?: boolean; containerize?: boolean } = {},
): Promise<PhonePushResult> {
  const h = headers();
  const srcBase = baseUrl();
  if (!h || !srcBase) {
    throw new Error("no source agent connected");
  }
  await syncLocalPhoneProjectToConnectedAgent(slug).catch(() => undefined);

  // 1. Pull the bundle from the source (the phone-backend we're connected to).
  const exportParams = new URLSearchParams({ slug });
  if (opts.includeData) exportParams.set("includeData", "true");
  if (opts.containerize) exportParams.set("containerize", "true");
  const exportRes = await fetch(
    `${srcBase}/phone/projects/export?${exportParams.toString()}`,
    { headers: h },
  );
  if (!exportRes.ok) {
    const body = await exportRes.text().catch(() => "");
    throw new Error(`export failed: ${exportRes.status} ${body}`);
  }
  const bundle = await exportRes.blob();

  // 2. POST to the target's /phone/projects/receive.
  const form = new FormData();
  // React Native FormData accepts a {uri,type,name} object, but we already
  // have an in-memory Blob from the export fetch — use it directly.
  form.append("bundle", bundle as unknown as Blob, `${slug}.tgz`);
  if (opts.onConflict) form.append("onConflict", opts.onConflict);
  if (opts.skipSeed) form.append("skipSeed", "true");

  const targetBase = resolveTargetBase(target);
  const receiveRes = await fetch(`${targetBase}/phone/projects/receive`, {
    method: "POST",
    // Do NOT set Content-Type — let fetch set the multipart boundary.
    headers: h,
    body: form as unknown as BodyInit,
  });
  if (!receiveRes.ok) {
    const body = await receiveRes.text().catch(() => "");
    throw new Error(`receive failed: ${receiveRes.status} ${body}`);
  }
  const json = (await receiveRes.json()) as PhonePushResult;
  return json;
}
