import AsyncStorage from "@react-native-async-storage/async-storage";
import { quicClient } from "./quic";
import { getYaverCloudBaseUrl } from "./yaverCloud";
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

// Friends-preview share: a host mints `code`; a friend resolves it and
// Hermes-loads `bundleUrl`, pointed at `hostedConvexUrl` (the host's
// own backend). Mirrors the agent's /phone/projects/share|join.
export interface PhoneShare {
  code: string;
  slug: string;
  name: string;
  hostedConvexUrl?: string;
  bundleUrl: string;
  createdAt: string;
  expiresAt: string;
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
  importUrl?: string;
  importContent?: string;
  importTitle?: string;
}

export type PhonePromoteTarget = string;

export type PhoneBackendKind = "local" | "current-agent" | "dev-hw" | "yaver-cloud" | "custom";

export interface PhoneProjectAccess {
  sourceSlug: string;
  slug: string;
  kind: PhoneBackendKind;
  label: string;
  baseUrl?: string;
  boundAt?: string;
}

export interface ProjectRuntimeProviderInput {
  provider: string;
  label?: string;
  fields?: Record<string, string>;
}

export interface ProjectRuntimePhonePromotion {
  slug: string;
  target: string;
  run?: boolean;
  dryRun?: boolean;
}

export interface ProjectRuntimeApplyRequest {
  name?: string;
  phoneSlug?: string;
  backend?: string;
  stack?: string;
  auth?: string;
  runtime?: Record<string, unknown>;
  placement?: Record<string, unknown>;
  jobs?: unknown[];
  domains?: unknown[];
  env?: Record<string, string>;
  providers?: ProjectRuntimeProviderInput[];
  phonePromotions?: ProjectRuntimePhonePromotion[];
  runManifestApply?: boolean;
  dryRun?: boolean;
}

export interface ProjectRuntimeProviderRequirement {
  provider: string;
  label?: string;
  authType?: string;
  fields?: string[];
  credentialRef?: string;
  requiredBy?: string[];
  connected: boolean;
  authSource?: string;
  warning?: string;
}

export interface ProjectRuntimeExportPlan {
  name: string;
  source: string;
  kind?: string;
  provider?: string;
  target?: string;
  app?: string;
  projectSlug?: string;
  credentialRef?: string;
  machineRole?: string;
  reason?: string;
  providerReady: boolean;
  providerAuthSource?: string;
  warning?: string;
}

export interface ProjectRuntimeSummary {
  projectDir: string;
  manifest?: Record<string, unknown>;
  providerRequirements?: ProjectRuntimeProviderRequirement[];
  exportPlans?: ProjectRuntimeExportPlan[];
  warnings?: string[];
}

export interface ProjectRuntimeApplyResponse {
  ok?: boolean;
  actions?: Array<{ kind: string; target?: string; details?: string }>;
  manifestSaved?: boolean;
  accountsApplied?: string[];
  manifestApply?: { steps?: string[]; diff?: string[]; error?: string };
  phoneSwitches?: Array<Record<string, unknown>>;
  summary?: ProjectRuntimeSummary;
  error?: string;
}

const LOCAL_PHONE_TEMPLATES: PhoneTemplate[] = [
  { id: "blank", label: "Blank", description: "Empty project — define your own schema." },
  { id: "crud", label: "Generic CRUD", description: "users + items table with a few personas." },
  { id: "todos", label: "Todos", description: "users + todos with seeded tasks." },
  { id: "notes", label: "Notes", description: "users + notes with a starter entry." },
];

function slugify(value: string): string {
  return value
    .trim()
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "");
}

function cloneSchema(schema: PhoneSchema | null | undefined): PhoneSchema | undefined {
  return schema ? JSON.parse(JSON.stringify(schema)) as PhoneSchema : undefined;
}

function cloneAuth(auth: PhoneAuth | null | undefined): PhoneAuth | undefined {
  return auth ? JSON.parse(JSON.stringify(auth)) as PhoneAuth : undefined;
}

function cloneSeed(seed: PhoneSeed | null | undefined): PhoneSeed | undefined {
  return seed ? JSON.parse(JSON.stringify(seed)) as PhoneSeed : undefined;
}

function cloneApp(app: PhoneAppSpec | null | undefined): PhoneAppSpec | undefined {
  return app ? JSON.parse(JSON.stringify(app)) as PhoneAppSpec : undefined;
}

function localTemplateSchema(name?: string): PhoneSchema | undefined {
  switch (name) {
    case "todos":
      return {
        tables: [
          {
            name: "users",
            columns: [
              { name: "id", type: "text", primary: true },
              { name: "email", type: "text", required: true, unique: true },
              { name: "name", type: "text" },
            ],
          },
          {
            name: "todos",
            columns: [
              { name: "id", type: "text", primary: true, default: "uuid" },
              { name: "title", type: "text", required: true },
              { name: "done", type: "bool", default: "false" },
              { name: "owner_id", type: "text" },
              { name: "created_at", type: "timestamp", default: "now" },
            ],
            indexes: [{ columns: ["owner_id"] }, { columns: ["done"] }],
          },
        ],
        relations: [{ from: "todos.owner_id", to: "users.id", onDelete: "cascade" }],
      };
    case "notes":
      return {
        tables: [
          {
            name: "users",
            columns: [
              { name: "id", type: "text", primary: true },
              { name: "email", type: "text", required: true, unique: true },
              { name: "name", type: "text" },
            ],
          },
          {
            name: "notes",
            columns: [
              { name: "id", type: "text", primary: true, default: "uuid" },
              { name: "title", type: "text", required: true },
              { name: "body", type: "text" },
              { name: "owner_id", type: "text" },
              { name: "created_at", type: "timestamp", default: "now" },
              { name: "updated_at", type: "timestamp", default: "now" },
            ],
            indexes: [{ columns: ["owner_id"] }],
          },
        ],
      };
    case "blank":
      return { tables: [] };
    case "crud":
    default:
      return {
        tables: [
          {
            name: "users",
            columns: [
              { name: "id", type: "text", primary: true },
              { name: "email", type: "text", required: true, unique: true },
              { name: "name", type: "text" },
            ],
          },
          {
            name: "items",
            columns: [
              { name: "id", type: "text", primary: true, default: "uuid" },
              { name: "name", type: "text", required: true },
              { name: "description", type: "text" },
              { name: "owner_id", type: "text" },
              { name: "created_at", type: "timestamp", default: "now" },
            ],
          },
        ],
      };
  }
}

function localTemplateAuth(name?: string): PhoneAuth | undefined {
  if (name === "blank") return { personas: [] };
  return {
    personas: [
      { id: "alice", email: "alice@example.com", name: "Alice" },
      { id: "bob", email: "bob@example.com", name: "Bob" },
    ],
  };
}

function localTemplateSeed(name?: string): PhoneSeed | undefined {
  switch (name) {
    case "todos":
      return {
        todos: [
          { id: "t1", title: "Buy milk", done: false, owner_id: "alice" },
          { id: "t2", title: "Learn Yaver", done: true, owner_id: "alice" },
          { id: "t3", title: "Ship mini-backend", done: false, owner_id: "bob" },
        ],
      };
    case "notes":
      return {
        notes: [{ id: "n1", title: "Welcome", body: "This is a starter note.", owner_id: "alice" }],
      };
    case "crud":
      return {
        items: [{ id: "i1", name: "Example", description: "Edit or delete this row.", owner_id: "alice" }],
      };
    default:
      return undefined;
  }
}

function localTemplateApp(name?: string): PhoneAppSpec | undefined {
  switch (name) {
    case "todos":
      return {
        summary: "Simple shared todo list with a quick capture flow.",
        primaryEntity: "todos",
        screens: [
          {
            id: "todo_list",
            title: "Todos",
            kind: "list",
            table: "todos",
            emptyState: "No tasks yet. Add one from your phone.",
            actions: [
              { label: "Add task", kind: "create", table: "todos" },
              { label: "Toggle done", kind: "update", table: "todos" },
            ],
          },
        ],
      };
    case "notes":
      return {
        summary: "Lightweight notes app with a notes list and editor.",
        primaryEntity: "notes",
        screens: [
          {
            id: "notes_list",
            title: "Notes",
            kind: "list",
            table: "notes",
            emptyState: "Start with a quick note.",
            actions: [
              { label: "New note", kind: "create", table: "notes" },
              { label: "Open note", kind: "navigate", target: "note_detail" },
            ],
          },
        ],
      };
    case "blank":
      return {
        summary: "Blank app. Define screens after shaping the schema.",
      };
    case "crud":
    default:
      return {
        summary: "Generic CRUD app with a collection list and editor.",
        primaryEntity: "items",
        screens: [
          {
            id: "items_list",
            title: "Items",
            kind: "list",
            table: "items",
            emptyState: "Create the first item.",
            actions: [
              { label: "Create item", kind: "create", table: "items" },
              { label: "View item", kind: "navigate", target: "item_detail" },
            ],
          },
        ],
      };
  }
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
  return quicClient.publicAuthHeaders();
}

function baseUrl(): string | null {
  return quicClient.isConnected && quicClient.baseUrl ? quicClient.baseUrl : null;
}

async function fetchWithTimeout(url: string, init?: RequestInit, timeoutMs = 8000): Promise<Response> {
  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(), timeoutMs);
  try {
    return await fetch(url, { ...init, signal: controller.signal });
  } finally {
    clearTimeout(timer);
  }
}

async function get<T>(path: string): Promise<T | null> {
  const h = headers();
  const url = baseUrl();
  if (!h || !url) return null;
  try {
    const res = await fetchWithTimeout(`${url}${path}`, { headers: h });
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
    const res = await fetchWithTimeout(`${url}${path}`, {
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
    const res = await fetchWithTimeout(`${base}${path}`, { headers: h });
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
    const res = await fetchWithTimeout(`${base}${path}`, {
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

export interface PhoneProjectDraft {
  template?: string;
  schema?: PhoneSchema;
  auth?: PhoneAuth;
  seed?: PhoneSeed;
  app?: PhoneAppSpec;
}

function extractJsonObject(text: string): string {
  const fenced = text.match(/```(?:json)?\s*([\s\S]*?)```/i);
  if (fenced?.[1]) return fenced[1].trim();
  const start = text.indexOf("{");
  const end = text.lastIndexOf("}");
  if (start >= 0 && end > start) return text.slice(start, end + 1);
  return text.trim();
}

function sanitizePhoneProjectDraft(value: unknown): PhoneProjectDraft {
  if (!value || typeof value !== "object") return {};
  const raw = value as Record<string, unknown>;
  const next: PhoneProjectDraft = {};
  if (typeof raw.template === "string" && raw.template.trim()) {
    next.template = raw.template.trim();
  }
  if (raw.schema && typeof raw.schema === "object") {
    next.schema = cloneSchema(raw.schema as PhoneSchema);
  }
  if (raw.auth && typeof raw.auth === "object") {
    next.auth = cloneAuth(raw.auth as PhoneAuth);
  }
  if (raw.seed && typeof raw.seed === "object") {
    next.seed = cloneSeed(raw.seed as PhoneSeed);
  }
  if (raw.app && typeof raw.app === "object") {
    next.app = cloneApp(raw.app as PhoneAppSpec);
  }
  return next;
}

export async function generatePhoneProjectDraftFromPrompt(args: {
  provider?: "openai" | "glm";
  apiKey: string;
  name: string;
  prompt: string;
  template?: string;
}): Promise<PhoneProjectDraft> {
  const key = args.apiKey.trim();
  const prompt = args.prompt.trim();
  const provider = args.provider === "glm" ? "glm" : "openai";
  if (!key) {
    throw new Error(provider === "glm" ? "GLM API key is required" : "OpenAI API key is required");
  }
  if (!prompt) return {};
  const endpoint =
    provider === "glm"
      ? "https://api.z.ai/api/coding/paas/v4/chat/completions"
      : "https://api.openai.com/v1/chat/completions";
  const model = provider === "glm" ? "glm-4.6" : "gpt-4.1-mini";
  const providerName = provider === "glm" ? "GLM" : "OpenAI";
  const res = await fetch(endpoint, {
    method: "POST",
    headers: {
      Authorization: `Bearer ${key}`,
      "Content-Type": "application/json",
    },
    body: JSON.stringify({
      model,
      temperature: 0.3,
      response_format: { type: "json_object" },
      messages: [
        {
          role: "system",
          content:
            "Return compact JSON only. Design a simple mobile-first monorepo-ready app skeleton for a local SQLite sandbox. Use tables with basic primary keys, lightweight personas, small seed data, and app screens that are easy to run from a phone. Prefer clear, minimal schemas.",
        },
        {
          role: "user",
          content: [
            `Project name: ${args.name.trim()}.`,
            `Fallback template: ${args.template ?? "todos"}.`,
            `Prompt: ${prompt}`,
            'Return JSON with optional keys: "template", "schema", "auth", "seed", "app".',
            'The "schema" must use { tables: [{ name, columns, indexes? }], relations? }.',
            'The "auth" must use { personas: [{ id, email, name?, role? }] }.',
            'The "app" may use { summary, primaryEntity, screens: [{ id, title, kind, table?, emptyState?, actions? }] }.',
            "Keep the output small and practical.",
          ].join("\n"),
        },
      ],
    }),
  });
  const text = await res.text().catch(() => "");
  if (!res.ok) {
    throw new Error(text || `${providerName} HTTP ${res.status}`);
  }
  const json = JSON.parse(text) as {
    choices?: Array<{ message?: { content?: string | Array<{ type?: string; text?: string }> } }>;
  };
  const content = json.choices?.[0]?.message?.content;
  const rawText = Array.isArray(content)
    ? content
        .map((item) => (typeof item?.text === "string" ? item.text : ""))
        .join("")
    : typeof content === "string"
      ? content
      : "";
  const parsed = JSON.parse(extractJsonObject(rawText || "{}")) as unknown;
  return sanitizePhoneProjectDraft(parsed);
}

// Reuses the same BYOK chat-completions endpoint that
// generatePhoneProjectDraftFromPrompt hits. Asks the LLM to inspect
// the user's survey + description and decide whether the project
// has enough fidelity to skeleton, or whether a few short-answer
// follow-up questions would meaningfully shape the schema. Returns
// `{ ready: true }` when the description is rich enough to proceed
// straight to generation; otherwise returns up to 3 short-answer
// questions the wizard can render. The user can always click
// "Force initialize" in the UI to bypass this and proceed anyway.
export async function generateClarifyingQuestions(args: {
  provider?: "openai" | "glm";
  apiKey: string;
  name: string;
  description: string;
}): Promise<{ ready: boolean; questions: Array<{ id: string; title: string; placeholder?: string }> }> {
  const key = args.apiKey.trim();
  const description = args.description.trim();
  const provider = args.provider === "glm" ? "glm" : "openai";
  if (!key || !description) {
    return { ready: true, questions: [] };
  }
  const endpoint =
    provider === "glm"
      ? "https://api.z.ai/api/coding/paas/v4/chat/completions"
      : "https://api.openai.com/v1/chat/completions";
  const model = provider === "glm" ? "glm-4.6" : "gpt-4.1-mini";
  const res = await fetch(endpoint, {
    method: "POST",
    headers: {
      Authorization: `Bearer ${key}`,
      "Content-Type": "application/json",
    },
    body: JSON.stringify({
      model,
      temperature: 0.2,
      response_format: { type: "json_object" },
      messages: [
        {
          role: "system",
          content:
            'Return compact JSON only. Decide whether a brief description is concrete enough to scaffold a small mobile-first SQLite sandbox. If it is, return {"ready": true, "questions": []}. Otherwise return up to 3 short, specific follow-up questions the user can answer in one short sentence each. Each question must materially shape the schema, screens, or auth — do not ask cosmetic or rhetorical things. Format: {"ready": false, "questions": [{"id": "q1", "title": "…", "placeholder": "short example answer"}]}.',
        },
        {
          role: "user",
          content: `Project: ${args.name.trim()}\nDescription:\n${description}`,
        },
      ],
    }),
  });
  const text = await res.text().catch(() => "");
  if (!res.ok) {
    // Don't block on a refine failure — pretend the description is
    // good enough so the wizard can proceed.
    return { ready: true, questions: [] };
  }
  try {
    const json = JSON.parse(text) as {
      choices?: Array<{ message?: { content?: string } }>;
    };
    const raw = json.choices?.[0]?.message?.content || "{}";
    const parsed = JSON.parse(extractJsonObject(raw)) as {
      ready?: boolean;
      questions?: Array<{ id?: string; title?: string; placeholder?: string }>;
    };
    const questions = Array.isArray(parsed.questions)
      ? parsed.questions
          .map((q, i) => ({
            id: typeof q?.id === "string" && q.id ? q.id : `q${i + 1}`,
            title: typeof q?.title === "string" ? q.title : "",
            placeholder: typeof q?.placeholder === "string" ? q.placeholder : undefined,
          }))
          .filter((q) => q.title.trim().length > 0)
          .slice(0, 3)
      : [];
    return { ready: parsed.ready === true || questions.length === 0, questions };
  } catch {
    return { ready: true, questions: [] };
  }
}

export async function getPhoneProjectAccess(sourceSlug: string): Promise<PhoneProjectAccess> {
  try {
    const raw = await AsyncStorage.getItem(bindingKey(sourceSlug));
    if (!raw) {
      return { sourceSlug, slug: sourceSlug, kind: "local", label: "This phone" };
    }
    const parsed = JSON.parse(raw) as StoredPhoneProjectBinding;
    if (!parsed?.baseUrl || !parsed?.slug || !parsed?.kind) {
      return { sourceSlug, slug: sourceSlug, kind: "local", label: "This phone" };
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
    return { sourceSlug, slug: sourceSlug, kind: "local", label: "This phone" };
  }
}

export async function bindPhoneProjectToCurrentAgent(
  sourceSlug: string,
  slug = sourceSlug,
  label = "Current Yaver Agent",
): Promise<void> {
  const url = baseUrl();
  if (!url) throw new Error("no agent connected");
  const value: StoredPhoneProjectBinding = {
    version: 1,
    slug,
    kind: "current-agent",
    label,
    baseUrl: url,
    boundAt: new Date().toISOString(),
  };
  await AsyncStorage.setItem(bindingKey(sourceSlug), JSON.stringify(value));
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

// sharePhoneProject mints a friends-preview join code on the connected
// agent. ttlMinutes ≤ 0 ⇒ agent default (24h).
export async function sharePhoneProject(
  slug: string,
  ttlMinutes = 0,
): Promise<PhoneShare> {
  const r = await post<PhoneShare>("/phone/projects/share", {
    slug,
    ttlMinutes,
  });
  if (!r) throw new Error("Not connected to a Yaver agent.");
  return r;
}

// joinPhoneShare resolves a code → {slug, hostedConvexUrl, bundleUrl}.
// The caller then fetches bundleUrl and Hermes-loads it against
// hostedConvexUrl (the host's live backend).
export async function joinPhoneShare(code: string): Promise<PhoneShare> {
  const r = await get<PhoneShare & { error?: string }>(
    `/phone/projects/join?code=${encodeURIComponent(code.trim())}`,
  );
  if (!r) throw new Error("Invalid or expired code (or not connected).");
  if (r.error) throw new Error(r.error);
  return r;
}

export async function listPhoneTemplates(): Promise<PhoneTemplate[]> {
  const r = await get<{ templates: PhoneTemplate[] }>(
    "/phone/projects/templates",
  );
  return r?.templates?.length ? r.templates : LOCAL_PHONE_TEMPLATES;
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

export async function createLocalPhoneProject(spec: PhoneCreateSpec): Promise<PhoneProject> {
  const name = spec.name.trim();
  if (!name) throw new Error("project name is required");
  const slug = slugify(spec.slug || name);
  if (!slug) throw new Error("project name is required");
  const template = spec.template || "crud";
  const now = new Date().toISOString();
  const app = cloneApp(spec.app) ?? localTemplateApp(template);
  if (spec.prompt?.trim()) {
    const promptSummary = `Kickoff prompt: ${spec.prompt.trim()}`;
    if (app?.summary) app.summary = `${app.summary} ${promptSummary}`;
  }
  const project: PhoneProject = {
    slug,
    name,
    template,
    dir: "",
    createdAt: now,
    updatedAt: now,
    schema: cloneSchema(spec.schema) ?? localTemplateSchema(template) ?? { tables: [] },
    auth: cloneAuth(spec.auth) ?? localTemplateAuth(template) ?? { personas: [] },
    seed: cloneSeed(spec.seed) ?? localTemplateSeed(template),
    app,
  };
  await ensureLocalPhoneProject(project);
  const hydrated = await getLocalPhoneProjectMeta(project.slug);
  return hydrated ?? project;
}

/** Create a new phone-backend project on a *different* agent than the one
 *  we're currently connected to. Used by the "Create project" flow in
 *  mobile/app/phone-projects.tsx when the user picks [Your Dev Machine] or
 *  [Yaver Cloud] as the start-point (roadmap §Wedge Demo). Goes through the
 *  same auth headers as the local agent, with an optional override token for
 *  managed-cloud or custom targets that need a different bearer.
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
  const overrideToken =
    target.kind === "yaver-cloud"
      ? target.cloudAuthToken
      : target.kind === "custom"
        ? target.authToken
        : undefined;
  const res = await fetch(`${base}/phone/projects/create`, {
    method: "POST",
    headers: {
      ...h,
      ...(overrideToken ? { Authorization: `Bearer ${overrideToken}` } : {}),
      "Content-Type": "application/json",
    },
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
      return (target.cloudBaseUrl ?? getYaverCloudBaseUrl()).replace(/\/$/, "");
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

export async function getProjectRuntimeSummary(directory?: string): Promise<ProjectRuntimeSummary | null> {
  const suffix = directory ? `?directory=${encodeURIComponent(directory)}` : "";
  return get<ProjectRuntimeSummary>(`/project/runtime${suffix}`);
}

export async function applyProjectRuntime(
  req: ProjectRuntimeApplyRequest,
  opts: { directory?: string } = {},
): Promise<ProjectRuntimeApplyResponse | null> {
  const suffix = opts.directory ? `?directory=${encodeURIComponent(opts.directory)}` : "";
  return post<ProjectRuntimeApplyResponse>(`/project/runtime/apply${suffix}`, req);
}

export interface PhoneRuntimeDeployRequest {
  slug: string;
  includeData?: boolean;
  runManifestApply?: boolean;
  dryRun?: boolean;
  providers?: ProjectRuntimeProviderInput[];
  exports?: Array<
    | { kind: "convex"; run?: boolean; dryRun?: boolean }
    | { kind: "cloudflare-workers"; run?: boolean; dryRun?: boolean }
    | { kind: "dev-hw"; deviceId: string; relayHttpUrl: string; onConflict?: "reject" | "rename" | "overwrite" }
    | { kind: "yaver-cloud"; cloudBaseUrl?: string; cloudAuthToken?: string; onConflict?: "reject" | "rename" | "overwrite" }
    | { kind: "custom"; baseUrl: string; authToken?: string; onConflict?: "reject" | "rename" | "overwrite" }
  >;
}

export interface PhoneRuntimeDeployResult {
  runtime?: ProjectRuntimeApplyResponse | null;
  pushes: Array<{ kind: "dev-hw" | "yaver-cloud" | "custom"; result: PhonePushResult }>;
  promotes: Array<{ kind: "convex" | "cloudflare-workers"; result: PromoteResult | null }>;
}

export async function deployPhoneProjectRuntime(req: PhoneRuntimeDeployRequest): Promise<PhoneRuntimeDeployResult> {
  const out: PhoneRuntimeDeployResult = { pushes: [], promotes: [] };
  const exports = req.exports ?? [];
  const phonePromotions: ProjectRuntimePhonePromotion[] = [];
  for (const item of exports) {
    if (item.kind === "convex") {
      phonePromotions.push({ slug: req.slug, target: "convex-cloud", run: item.run, dryRun: item.dryRun ?? req.dryRun });
    } else if (item.kind === "cloudflare-workers") {
      phonePromotions.push({ slug: req.slug, target: "cloudflare-workers", run: item.run, dryRun: item.dryRun ?? req.dryRun });
    }
  }
  if (phonePromotions.length || req.providers?.length || req.runManifestApply) {
    out.runtime = await applyProjectRuntime({
      phoneSlug: req.slug,
      providers: req.providers,
      phonePromotions,
      runManifestApply: req.runManifestApply,
      dryRun: req.dryRun,
    });
  }
  for (const item of exports) {
    if (item.kind === "dev-hw") {
      const result = await pushPhoneProject(req.slug, {
        kind: "dev-hw",
        deviceId: item.deviceId,
        relayHttpUrl: item.relayHttpUrl,
      }, {
        includeData: req.includeData,
        onConflict: item.onConflict,
      });
      out.pushes.push({ kind: "dev-hw", result });
    } else if (item.kind === "yaver-cloud") {
      const result = await pushPhoneProject(req.slug, {
        kind: "yaver-cloud",
        cloudBaseUrl: item.cloudBaseUrl,
        cloudAuthToken: item.cloudAuthToken,
      }, {
        includeData: req.includeData,
        containerize: true,
        onConflict: item.onConflict,
      });
      out.pushes.push({ kind: "yaver-cloud", result });
    } else if (item.kind === "custom") {
      const result = await pushPhoneProject(req.slug, {
        kind: "custom",
        baseUrl: item.baseUrl,
        authToken: item.authToken,
      }, {
        includeData: req.includeData,
        containerize: true,
        onConflict: item.onConflict,
      });
      out.pushes.push({ kind: "custom", result });
    }
  }
  for (const item of exports) {
    if (item.kind === "convex") {
      out.promotes.push({ kind: "convex", result: await promotePhoneProject(req.slug, "convex-cloud", { run: !!item.run, dryRun: item.dryRun ?? !!req.dryRun }) });
    } else if (item.kind === "cloudflare-workers") {
      out.promotes.push({ kind: "cloudflare-workers", result: await promotePhoneProject(req.slug, "cloudflare-workers", { run: !!item.run, dryRun: item.dryRun ?? !!req.dryRun }) });
    }
  }
  return out;
}

// ---- Push (export-and-receive) to a dev machine or Yaver cloud ----

export type PhonePushTarget =
  | { kind: "dev-hw"; deviceId: string; relayHttpUrl: string }
  // Optional auth override for legacy/shared-tenant cloud paths. Dedicated
  // managed machines should usually accept the caller's normal Yaver session.
  | { kind: "yaver-cloud"; cloudBaseUrl?: string; cloudAuthToken?: string }
  | { kind: "custom"; baseUrl: string; authToken?: string };

// Raised when the cloud target requires an active managed cloud entitlement.
//
// IMPORTANT — App Store / Play Store compliance: the mobile app MUST NOT
// initiate a paid transaction, auto-open a checkout URL, or present a purchase
// CTA for managed cloud. Web and CLI may use the checkoutUrl; store mobile
// builds should only show a neutral entitlement state and let users retry
// after their account already has an active machine.
export class PhonePushPaymentRequired extends Error {
  constructor(public checkoutUrl: string | null, message: string) {
    super(message);
    this.name = "PhonePushPaymentRequired";
  }
}

export interface PhonePushResult {
  slug: string;
  localUrl: string;
  browseUrl: string;
  project: PhoneProject;
}

const DEFAULT_YAVER_CLOUD_BASE = getYaverCloudBaseUrl();

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
 * - `yaver-cloud` — Yaver's managed cloud tenant. Same endpoint, different
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
  if (target.kind === "yaver-cloud" || opts.containerize) exportParams.set("containerize", "true");
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

  // Swap auth header when pushing with an explicit override token. This is
  // mainly for legacy/shared-tenant cloud paths and custom targets.
  const overrideToken =
    target.kind === "yaver-cloud"
      ? target.cloudAuthToken
      : target.kind === "custom"
        ? target.authToken
        : undefined;
  const pushHeaders: Record<string, string> = { ...h };
  if (overrideToken) pushHeaders["Authorization"] = `Bearer ${overrideToken}`;

  // Pre-flight: ping the target's /health so we fail fast (and with a
  // useful message) when the target is offline. Big bundle uploads that
  // die half-way through a dead connection are the #1 user complaint.
  try {
    const probe = await fetch(`${targetBase}/health`, { method: "GET" });
    if (!probe.ok && probe.status !== 401) {
      throw new Error(`target health check failed: ${probe.status}`);
    }
  } catch (e) {
    throw new Error(
      `target ${targetBase} is not reachable (${e instanceof Error ? e.message : String(e)})`,
    );
  }

  const receiveRes = await fetch(`${targetBase}/phone/projects/receive`, {
    method: "POST",
    // Do NOT set Content-Type — let fetch set the multipart boundary.
    headers: pushHeaders,
    body: form as unknown as BodyInit,
  });
  if (!receiveRes.ok) {
    const body = await receiveRes.text().catch(() => "");
    // The cloud agent returns 402 Payment Required when a push is rejected
    // for entitlement reasons. Preserve checkoutUrl for web/CLI callers only;
    // mobile UI must not open or display it.
    if (receiveRes.status === 402) {
      let checkoutUrl: string | null = null;
      try { checkoutUrl = JSON.parse(body).checkoutUrl ?? null; } catch { /* ignore */ }
      throw new PhonePushPaymentRequired(
        checkoutUrl,
        "this cloud tenant requires an active managed cloud machine on the account",
      );
    }
    if (receiveRes.status === 401 || receiveRes.status === 403) {
      const hint = target.kind === "yaver-cloud"
        ? " (pass cloudAuthToken if this cloud target requires an override token)"
        : "";
      throw new Error(`receive failed: ${receiveRes.status} ${body}${hint}`);
    }
    throw new Error(`receive failed: ${receiveRes.status} ${body}`);
  }
  const json = (await receiveRes.json()) as PhonePushResult;
  return json;
}
