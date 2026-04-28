// projectStore.ts — TS twin of desktop/agent/projectstore.go.
//
// Defines the ProjectStore contract that every runtime tier must
// satisfy and ships the agent-tier implementation (HTTP). The
// phone-sandbox-tier implementation lives in projectStoreSandbox.ts
// so it can pull in expo-sqlite without breaking the path-aliased
// mobile-headless build (which does not have a SQLite shim).
//
// Self-hosted and Yaver Cloud both run the same agent binary, so a
// single agentStore implementation backs both — the only thing that
// differs is the baseUrl. That mirrors the "self-hosted and cloud
// same" simplification documented in
// docs/yaver-code-deploy-integration.md.

import type {
  PhoneAppSpec,
  PhoneAuth,
  PhoneProject,
  PhoneSchema,
  PhoneSeed,
  PhoneStats,
} from "./phoneProjects";

/** Tier label every ProjectStore stamps onto its ProjectMeta. */
export type ProjectTier = "agent" | "repo" | "phone-sandbox";

/** Conflict policy mirrors the existing /phone/projects/receive
 *  contract on the agent side. Keep the strings stable — they go
 *  on the wire as form fields. */
export type ConflictPolicy = "" | "reject" | "rename" | "overwrite";

export interface ProjectMeta {
  slug: string;
  name: string;
  template?: string;
  createdAt?: string;
  updatedAt?: string;
  tier: ProjectTier;
}

/** Project is the canonical in-memory representation. Mirrors the Go
 *  struct from desktop/agent/projectstore.go field-for-field so a TS
 *  client can shuttle a project between tiers without losing data. */
export interface Project {
  slug: string;
  name: string;
  template?: string;
  createdAt?: string;
  updatedAt?: string;
  schema?: PhoneSchema | null;
  auth?: PhoneAuth | null;
  seed?: PhoneSeed | null;
  app?: PhoneAppSpec | null;
  /** Live counts — agent tier only. */
  stats?: PhoneStats | null;
}

export interface WriteOptions {
  onConflict?: ConflictPolicy;
  skipSeed?: boolean;
  /** Whether to ship live-data alongside the declarative bundle. */
  includeData?: boolean;
}

export interface ProjectStore {
  list(): Promise<ProjectMeta[]>;
  read(slug: string): Promise<Project>;
  write(p: Project, opts?: WriteOptions): Promise<ProjectMeta>;
}

/** ProjectNotFoundError is the canonical sentinel for "no project
 *  with this slug." HTTP transports translate to 404; UI surfaces
 *  translate to clear "no such project" copy. */
export class ProjectNotFoundError extends Error {
  readonly slug: string;
  constructor(slug: string) {
    super(`project not found: ${slug}`);
    this.name = "ProjectNotFoundError";
    this.slug = slug;
  }
}

export function isProjectNotFound(err: unknown): err is ProjectNotFoundError {
  return err instanceof ProjectNotFoundError;
}

// ---- Agent tier (HTTP) ----------------------------------------------

export interface AgentStoreOptions {
  /** Trailing slash optional; the store strips it. */
  baseUrl: string;
  /** Auth + relay headers — the caller is responsible for wiring
   *  Authorization, X-Relay-Password, etc. */
  headers?: Record<string, string>;
  /** Override the fetch implementation. Defaults to globalThis.fetch
   *  so the same code runs in RN, browser, Node, and Bun. */
  fetchImpl?: typeof fetch;
}

/** agentStore returns a ProjectStore that talks to a running yaver
 *  agent's /phone/projects/* endpoints. Both self-hosted and
 *  Yaver Cloud are reached the same way; only the baseUrl differs. */
export function agentStore(opts: AgentStoreOptions): ProjectStore {
  const base = opts.baseUrl.replace(/\/+$/, "");
  const f = opts.fetchImpl ?? fetch;
  const headers = opts.headers ?? {};

  async function fetchJSON(path: string, init?: RequestInit): Promise<unknown> {
    const res = await f(base + path, {
      ...init,
      headers: { ...headers, ...(init?.headers ?? {}) },
    });
    if (res.status === 404) {
      // Caller wraps this with ProjectNotFoundError once it knows the
      // slug. Don't throw the typed error here — list() also hits 404
      // when the agent is too old to expose /phone/projects/list.
      throw Object.assign(new Error(`HTTP 404: ${path}`), { status: 404 });
    }
    if (!res.ok) {
      const body = await res.text().catch(() => "");
      throw new Error(`HTTP ${res.status} ${path}: ${body.slice(0, 200)}`);
    }
    return res.json();
  }

  return {
    async list(): Promise<ProjectMeta[]> {
      const body = (await fetchJSON("/phone/projects/list")) as { projects?: PhoneProject[] } | null;
      const arr = Array.isArray(body?.projects) ? body!.projects! : [];
      return arr.map((p) => ({
        slug: p.slug,
        name: p.name,
        template: p.template,
        createdAt: p.createdAt,
        updatedAt: p.updatedAt,
        tier: "agent",
      }));
    },

    async read(slug: string): Promise<Project> {
      let body: unknown;
      try {
        body = await fetchJSON(`/phone/projects/get?slug=${encodeURIComponent(slug)}`);
      } catch (e: any) {
        if (e?.status === 404) throw new ProjectNotFoundError(slug);
        throw e;
      }
      if (!body || typeof body !== "object") throw new ProjectNotFoundError(slug);
      const p = body as PhoneProject;
      return {
        slug: p.slug,
        name: p.name,
        template: p.template,
        createdAt: p.createdAt,
        updatedAt: p.updatedAt,
        schema: p.schema ?? null,
        auth: p.auth ?? null,
        seed: p.seed ?? null,
        app: p.app ?? null,
        stats: p.stats ?? null,
      };
    },

    async write(p: Project, opts?: WriteOptions): Promise<ProjectMeta> {
      // The agent's /phone/projects/create accepts a PhoneCreateSpec.
      // Map our Project into that shape.
      const spec = {
        slug: p.slug,
        name: p.name,
        template: p.template ?? "",
        schema: p.schema ?? undefined,
        auth: p.auth ?? undefined,
        seed: opts?.skipSeed ? undefined : p.seed ?? undefined,
        app: p.app ?? undefined,
      };
      // Conflict handling on the agent's create endpoint isn't as
      // rich as /phone/projects/receive — we use the latter when the
      // caller asks for non-default conflict semantics. For now,
      // POST /create is the simple path; the upcoming wireMobilePush
      // helper covers the receive-with-bundle flow separately.
      const body = (await fetchJSON("/phone/projects/create", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(spec),
      })) as PhoneProject;
      return {
        slug: body.slug,
        name: body.name,
        template: body.template,
        createdAt: body.createdAt,
        updatedAt: body.updatedAt,
        tier: "agent",
      };
    },
  };
}

// ---- Convenience: pull from agent into any other store --------------

/** pullFromAgent reads a project off a remote agent and writes it
 *  into a destination store (typically the phone sandbox). This is
 *  the missing direction the design doc calls out: today the phone
 *  is push-only; this primitive closes the loop. */
export async function pullFromAgent(
  slug: string,
  agentOpts: AgentStoreOptions,
  dest: ProjectStore,
  writeOpts?: WriteOptions,
): Promise<ProjectMeta> {
  const src = agentStore(agentOpts);
  const project = await src.read(slug);
  return dest.write(project, writeOpts);
}
