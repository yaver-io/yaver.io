/**
 * Yaver Backend client — the runtime surface that a third-party React Native,
 * web, or Node.js app uses to read/write the developer's phone-backend
 * project data.
 *
 * Separate from YaverClient (which talks to the agent control plane).
 * Separate from yaver-cli / yaver-sdk's AI agent RPC.
 *
 * Usage from any JS/TS app the developer ships to their end users:
 *
 * ```ts
 * import { createYaverBackendClient } from 'yaver-sdk';
 *
 * const yaver = createYaverBackendClient({
 *   baseUrl: 'https://cloud.yaver.io',
 *   slug:    'my-todo-app',
 *   apiKey:  process.env.YAVER_API_KEY!, // pp_my-todo-app_<hex>
 * });
 *
 * const todos = await yaver.collection<Todo>('todos').list();
 * await yaver.collection('todos').insert({ id: '42', title: 'write tests', done: false });
 * await yaver.collection('todos').update('42', { done: true });
 * await yaver.collection('todos').remove('42');
 * ```
 *
 * Auth + transport:
 *   - Bearer <apiKey> in Authorization header (preferred).
 *   - ?api_key=<apiKey> fallback for env where headers can't be set easily.
 *   - CORS is permissive by default; the agent echoes the request origin.
 *
 * Scope: each API key authorises exactly ONE project. The agent rejects
 * cross-project reads (403) even if the caller guesses another slug.
 */

export interface YaverBackendClientOptions {
  /** Base URL of the agent — e.g. `https://cloud.yaver.io`, `http://localhost:18080`, or a relay `${relay}/d/${deviceId}`. */
  baseUrl: string;
  /** Project slug this client talks to. */
  slug: string;
  /** API key minted on the phone-project detail screen. Starts with `pp_<slug>_`. */
  apiKey: string;
  /** Optional override — useful for testing or to swap in a node-fetch shim. */
  fetchImpl?: typeof fetch;
  /** Request timeout in ms. Default 30_000. */
  timeoutMs?: number;
}

export interface YaverCollection<Row = Record<string, unknown>> {
  /** Paginated list. Default limit is 50; cursor is opaque. */
  list(opts?: { cursor?: string; limit?: number }): Promise<{ rows: Row[]; nextCursor?: string }>;
  /** Fetch a single row by `id`. Returns null when the row isn't found. */
  get(id: string): Promise<Row | null>;
  /** Insert a new row. Returns the server-assigned id. */
  insert(row: Partial<Row> | Record<string, unknown>): Promise<string>;
  /** Partial update by id. */
  update(id: string, fields: Partial<Row> | Record<string, unknown>): Promise<void>;
  /** Remove a row by id. */
  remove(id: string): Promise<void>;
}

export interface YaverBackendClient {
  /** The resolved base URL — useful for logs / debugging. */
  baseUrl: string;
  /** The project slug this client is scoped to. */
  slug: string;
  /** Typed accessor for one collection/table. */
  collection<Row = Record<string, unknown>>(name: string): YaverCollection<Row>;
  /** Raw fetch helper so users can call unsupported endpoints without bypassing auth. */
  fetch(path: string, init?: RequestInit): Promise<Response>;
}

/**
 * Creates a Yaver Backend client. Pure — no side effects, no module-level
 * globals — so it's safe to construct per-request in a serverless env.
 */
export function createYaverBackendClient(opts: YaverBackendClientOptions): YaverBackendClient {
  if (!opts.baseUrl) throw new Error("yaver: baseUrl required");
  if (!opts.slug) throw new Error("yaver: slug required");
  if (!opts.apiKey) throw new Error("yaver: apiKey required");
  if (!opts.apiKey.startsWith("pp_")) {
    throw new Error("yaver: apiKey must start with pp_<slug>_ — mint one in the Yaver app under the project's API Keys tab");
  }

  const baseUrl = opts.baseUrl.replace(/\/+$/, "");
  const slug = opts.slug;
  const apiKey = opts.apiKey;
  const timeoutMs = opts.timeoutMs ?? 30_000;
  const fetchImpl = opts.fetchImpl ?? (globalThis as { fetch: typeof fetch }).fetch;
  if (!fetchImpl) {
    throw new Error("yaver: no global fetch found — pass fetchImpl in options (Node 18+ has fetch built-in)");
  }

  async function rawFetch(path: string, init: RequestInit = {}): Promise<Response> {
    const ctrl = new AbortController();
    const timer = setTimeout(() => ctrl.abort(), timeoutMs);
    try {
      const headers: Record<string, string> = {
        Authorization: `Bearer ${apiKey}`,
        ...(init.headers as Record<string, string> | undefined),
      };
      // Content-Type is only meaningful when there's a body.
      if (init.body && !headers["Content-Type"]) {
        headers["Content-Type"] = "application/json";
      }
      return await fetchImpl(baseUrl + path, {
        ...init,
        headers,
        signal: ctrl.signal,
      });
    } finally {
      clearTimeout(timer);
    }
  }

  async function json<T>(path: string, init: RequestInit = {}): Promise<T> {
    const res = await rawFetch(path, init);
    const text = await res.text().catch(() => "");
    if (!res.ok) {
      let msg = `HTTP ${res.status}`;
      try {
        const parsed = JSON.parse(text) as { error?: string };
        if (parsed.error) msg = parsed.error;
      } catch {
        if (text) msg = text;
      }
      throw new YaverBackendError(msg, res.status);
    }
    return text ? (JSON.parse(text) as T) : (undefined as unknown as T);
  }

  function collection<Row>(name: string): YaverCollection<Row> {
    const coll = encodeURIComponent(name);
    const prefix = `/data/${encodeURIComponent(slug)}/${coll}`;
    return {
      async list(opts: { cursor?: string; limit?: number } = {}) {
        const params = new URLSearchParams();
        if (opts.cursor) params.set("cursor", opts.cursor);
        if (opts.limit) params.set("limit", String(opts.limit));
        const qs = params.toString() ? `?${params.toString()}` : "";
        const r = await json<{ rows: Row[]; nextCursor?: string }>(`${prefix}${qs}`);
        return { rows: r.rows ?? [], nextCursor: r.nextCursor };
      },
      async get(id) {
        try {
          return await json<Row>(`${prefix}/${encodeURIComponent(id)}`);
        } catch (e) {
          if (e instanceof YaverBackendError && e.status === 404) return null;
          throw e;
        }
      },
      async insert(row) {
        const r = await json<{ id: string }>(prefix, {
          method: "POST",
          body: JSON.stringify(row),
        });
        return r.id;
      },
      async update(id, fields) {
        await json<{ ok: boolean }>(`${prefix}/${encodeURIComponent(id)}`, {
          method: "PATCH",
          body: JSON.stringify(fields),
        });
      },
      async remove(id) {
        await json<{ ok: boolean }>(`${prefix}/${encodeURIComponent(id)}`, {
          method: "DELETE",
        });
      },
    };
  }

  return {
    baseUrl,
    slug,
    collection,
    fetch: rawFetch,
  };
}

/** Non-2xx responses throw this. Callers can branch on `.status`. */
export class YaverBackendError extends Error {
  status: number;
  constructor(message: string, status: number) {
    super(message);
    this.name = "YaverBackendError";
    this.status = status;
  }
}
