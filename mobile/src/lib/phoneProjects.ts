import { quicClient } from "./quic";

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
}

export type PhonePromoteTarget = string;

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

export async function listPhoneProjects(): Promise<PhoneProject[]> {
  const r = await get<{ projects?: PhoneProject[]; error?: string }>(
    "/phone/projects/list",
  );
  if (!r) return [];
  if (r.error) throw new Error(r.error);
  return r.projects ?? [];
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
  return r;
}

export async function getPhoneProject(slug: string): Promise<PhoneProject | null> {
  return get<PhoneProject>(`/phone/projects/get?slug=${encodeURIComponent(slug)}`);
}

export async function deletePhoneProject(slug: string): Promise<boolean> {
  const r = await post<{ ok?: boolean }>("/phone/projects/delete", { slug });
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
  const r = await get<{ tables?: Array<{ name: string; rowCount?: number }>; error?: string }>(
    `/phone/projects/tables?slug=${encodeURIComponent(slug)}`,
  );
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
): Promise<PhoneBrowseResult | null> {
  const params = new URLSearchParams({
    slug,
    table,
    cursor,
    limit: String(limit),
  });
  return get<PhoneBrowseResult>(`/phone/projects/browse?${params.toString()}`);
}

export async function insertPhoneRow(
  slug: string,
  table: string,
  doc: Record<string, unknown>,
): Promise<string | null> {
  const r = await post<{ id: string }>("/phone/projects/insert", { slug, table, doc });
  return r?.id ?? null;
}

export async function updatePhoneRow(
  slug: string,
  table: string,
  id: string,
  fields: Record<string, unknown>,
): Promise<boolean> {
  const r = await post<{ ok?: boolean }>("/phone/projects/update", { slug, table, id, fields });
  return !!r?.ok;
}

export async function deletePhoneRow(slug: string, table: string, id: string): Promise<boolean> {
  const r = await post<{ ok?: boolean }>("/phone/projects/delete-row", { slug, table, id });
  return !!r?.ok;
}

export async function queryPhoneProject(
  slug: string,
  query: string,
  args?: Record<string, unknown>,
): Promise<unknown> {
  const r = await post<{ result: unknown }>("/phone/projects/query", { slug, query, args });
  return r?.result ?? null;
}

export function phoneExportUrl(slug: string): { uri: string; headers: Record<string, string> } | null {
  const h = headers();
  const url = baseUrl();
  if (!h || !url) return null;
  return {
    uri: `${url}/phone/projects/export?slug=${encodeURIComponent(slug)}`,
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
  opts: { onConflict?: "reject" | "rename" | "overwrite"; skipSeed?: boolean } = {},
): Promise<PhonePushResult> {
  const h = headers();
  const srcBase = baseUrl();
  if (!h || !srcBase) {
    throw new Error("no source agent connected");
  }

  // 1. Pull the bundle from the source (the phone-backend we're connected to).
  const exportRes = await fetch(
    `${srcBase}/phone/projects/export?slug=${encodeURIComponent(slug)}`,
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
