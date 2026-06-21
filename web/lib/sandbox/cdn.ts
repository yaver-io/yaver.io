"use client";

// cdn.ts — runtime loaders for the browser sandbox's heavy libraries.
//
// sql.js (SQLite-in-WASM), fflate (gzip), and esbuild-wasm are loaded from a
// pinned CDN via <script> injection rather than bundled. This keeps the `web/`
// Cloudflare worker bundle under the 15 MB cap (esbuild.wasm alone is ~10 MB)
// and avoids an npm install on the build machine. The libraries expose UMD
// globals (window.initSqlJs / window.fflate / window.esbuild) which we wrap in
// thin typed handles. All three are standard public CDN assets — no secrets,
// no third-party data, just static library files.

// Pinned versions. Bump deliberately; the .wasm URL must match the JS version.
const SQLJS_VERSION = "1.12.0";
const FFLATE_VERSION = "0.8.2";
const ESBUILD_VERSION = "0.24.2";

const SQLJS_JS = `https://cdn.jsdelivr.net/npm/sql.js@${SQLJS_VERSION}/dist/sql-wasm.js`;
const SQLJS_WASM = `https://cdn.jsdelivr.net/npm/sql.js@${SQLJS_VERSION}/dist/sql-wasm.wasm`;
const FFLATE_JS = `https://cdn.jsdelivr.net/npm/fflate@${FFLATE_VERSION}/umd/index.js`;
const ESBUILD_JS = `https://cdn.jsdelivr.net/npm/esbuild-wasm@${ESBUILD_VERSION}/lib/browser.min.js`;
const ESBUILD_WASM = `https://cdn.jsdelivr.net/npm/esbuild-wasm@${ESBUILD_VERSION}/esbuild.wasm`;

// ── Minimal typed surfaces for the parts of each library we use ──────────────

export interface SqlJsStatement {
  step(): boolean;
  getAsObject(): Record<string, unknown>;
  bind(params?: unknown[] | Record<string, unknown>): boolean;
  free(): boolean;
}
export interface SqlJsDatabase {
  run(sql: string, params?: unknown[] | Record<string, unknown>): SqlJsDatabase;
  exec(sql: string): Array<{ columns: string[]; values: unknown[][] }>;
  prepare(sql: string, params?: unknown[] | Record<string, unknown>): SqlJsStatement;
  export(): Uint8Array;
  close(): void;
}
export interface SqlJsStatic {
  Database: new (data?: Uint8Array | null) => SqlJsDatabase;
}
type InitSqlJs = (config?: { locateFile?: (file: string) => string }) => Promise<SqlJsStatic>;

export interface Fflate {
  gzipSync(data: Uint8Array, opts?: { level?: number }): Uint8Array;
  gunzipSync(data: Uint8Array): Uint8Array;
}

export interface EsbuildStatic {
  initialize(opts: { wasmURL: string; worker?: boolean }): Promise<void>;
  build(opts: Record<string, unknown>): Promise<{ outputFiles?: Array<{ text: string; path: string }> }>;
  transform(input: string, opts?: Record<string, unknown>): Promise<{ code: string }>;
}

declare global {
  interface Window {
    initSqlJs?: InitSqlJs;
    fflate?: Fflate;
    esbuild?: EsbuildStatic;
  }
}

// ── Script injection (once per URL) ─────────────────────────────────────────

const scriptCache = new Map<string, Promise<void>>();

function loadScript(url: string): Promise<void> {
  if (typeof document === "undefined") {
    return Promise.reject(new Error("sandbox libraries can only load in the browser"));
  }
  const existing = scriptCache.get(url);
  if (existing) return existing;
  const p = new Promise<void>((resolve, reject) => {
    const el = document.createElement("script");
    el.src = url;
    el.async = true;
    el.crossOrigin = "anonymous";
    el.onload = () => resolve();
    el.onerror = () => reject(new Error(`failed to load ${url}`));
    document.head.appendChild(el);
  });
  scriptCache.set(url, p);
  return p;
}

// ── Public loaders ──────────────────────────────────────────────────────────

let sqlJsPromise: Promise<SqlJsStatic> | null = null;
export function loadSqlJs(): Promise<SqlJsStatic> {
  if (sqlJsPromise) return sqlJsPromise;
  sqlJsPromise = (async () => {
    await loadScript(SQLJS_JS);
    const init = window.initSqlJs;
    if (!init) throw new Error("sql.js did not register window.initSqlJs");
    return init({ locateFile: () => SQLJS_WASM });
  })();
  return sqlJsPromise;
}

let fflatePromise: Promise<Fflate> | null = null;
export function loadFflate(): Promise<Fflate> {
  if (fflatePromise) return fflatePromise;
  fflatePromise = (async () => {
    await loadScript(FFLATE_JS);
    const f = window.fflate;
    if (!f) throw new Error("fflate did not register window.fflate");
    return f;
  })();
  return fflatePromise;
}

let esbuildPromise: Promise<EsbuildStatic> | null = null;
export function loadEsbuild(): Promise<EsbuildStatic> {
  if (esbuildPromise) return esbuildPromise;
  esbuildPromise = (async () => {
    await loadScript(ESBUILD_JS);
    const e = window.esbuild;
    if (!e) throw new Error("esbuild-wasm did not register window.esbuild");
    await e.initialize({ wasmURL: ESBUILD_WASM, worker: true });
    return e;
  })();
  return esbuildPromise;
}
