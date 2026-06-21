"use client";

// bundle.ts — build and parse the Yaver Serverless `.yaver.tgz` bundle entirely
// in the browser. The produced bundle is byte/shape-compatible with
// desktop/agent/phone_backend.go (collectPhoneExportFiles / ImportPhoneProject):
// tar entries named `<slug>/<path>`, gzip-wrapped, with `data/app.sqlite` as the
// canonical payload. YAML files are emitted as JSON (a valid YAML subset whose
// keys already match the Go yaml struct tags), so deploying to a real agent's
// `/phone/projects/receive` works with no extra translation.

import type {
  PhoneAppSpec,
  PhoneAuth,
  PhoneSchema,
  PhoneSeed,
} from "@/lib/agent-client";
import { loadFflate } from "./cdn";
import { computeStats, introspectSchema, schemaToDDL } from "./schema";
import { SqliteDb } from "./sqlite";
import type { LocalProjectRecord } from "./store";
import { createTar, extractTar, type TarEntry } from "./tar";

const encoder = new TextEncoder();
const decoder = new TextDecoder();

function buildManifest(rec: LocalProjectRecord, includeData: boolean): unknown {
  const schema = rec.schema ?? { tables: [] };
  const tables = (schema.tables ?? []).map((t) => ({
    name: t.name,
    columns: t.columns.map((c) => c.name),
  }));
  const routes = (schema.tables ?? []).flatMap((t) => [
    { method: "GET", path: `/data/${t.name}` },
    { method: "POST", path: `/data/${t.name}` },
    { method: "GET", path: `/data/${t.name}/:id` },
    { method: "PATCH", path: `/data/${t.name}/:id` },
    { method: "DELETE", path: `/data/${t.name}/:id` },
  ]);
  return {
    version: 1,
    runtime: "yaver-serverless-lite",
    name: rec.name,
    slug: rec.slug,
    database: { engine: "sqlite", file: "data/app.sqlite", schema: "schema.yaml" },
    auth: { mode: "local", config: "auth.yaml" },
    api: { basePath: `/p/${rec.slug}`, dataPath: `/data/${rec.slug}`, routes },
    placements: ["mobile-sandbox", "self-hosted", "yaver-managed-cloud"],
    export: { includesData: includeData, secrets: "excluded-by-default" },
    tables,
  };
}

/** Build a `.yaver.tgz` (gzip-wrapped tar) for a browser-local project. */
export async function buildBundle(
  rec: LocalProjectRecord,
  opts?: { includeData?: boolean },
): Promise<Uint8Array> {
  const includeData = opts?.includeData ?? true;
  const entries: TarEntry[] = [];
  const add = (name: string, content: string | Uint8Array, mode = 0o644) =>
    entries.push({
      name: `${rec.slug}/${name}`,
      data: typeof content === "string" ? encoder.encode(content) : content,
      mode,
    });

  const schema = rec.schema ?? { tables: [] };
  add("yaver.serverless.yaml", JSON.stringify(buildManifest(rec, includeData), null, 2));
  add(".yaver/project.yaml", JSON.stringify({ slug: rec.slug, name: rec.name, runtime: "yaver-serverless-lite" }, null, 2));
  add(".yaver/config.yaml", JSON.stringify({ backend: "sqlite", file: "data/app.sqlite" }, null, 2));
  add("schema.yaml", JSON.stringify(schema, null, 2));
  add("auth.yaml", JSON.stringify(rec.auth ?? { personas: [] }, null, 2));
  add("seed.json", JSON.stringify(rec.seed ?? {}, null, 2));
  add("app.yaml", JSON.stringify(rec.app ?? {}, null, 2));
  if (includeData && rec.db?.length) {
    add("data/app.sqlite", rec.db, 0o600);
    add("local.db", rec.db, 0o600); // legacy compatibility
  }
  const ddl = schemaToDDL(schema);
  if (ddl) add("schema.sql", ddl);
  add("README.md", `# ${rec.name}\n\nYaver Serverless Lite project (slug: \`${rec.slug}\`).\nBuilt in the Yaver web sandbox.\n`);

  const tar = createTar(entries);
  const fflate = await loadFflate();
  return fflate.gzipSync(tar, { level: 6 });
}

function tryJson<T>(bytes: Uint8Array | undefined): T | null {
  if (!bytes || !bytes.length) return null;
  try {
    return JSON.parse(decoder.decode(bytes)) as T;
  } catch {
    return null; // real YAML (from the Go agent) — caller falls back.
  }
}

// Mirrors the slugify intent of desktop/agent Slugify (lowercase, hyphenated).
function slugify(s: string): string {
  return s
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "")
    .slice(0, 64) || "project";
}

function humanize(slug: string): string {
  return slug.replace(/[-_]+/g, " ").replace(/\b\w/g, (c) => c.toUpperCase());
}

/**
 * Parse a `.yaver.tgz` into a browser-local project record. Accepts bundles
 * produced by this module (JSON-as-YAML) or by the Go agent (real YAML) — for
 * the latter, schema/auth/app fall back to SQLite introspection / defaults.
 */
export async function parseBundle(
  bytes: Uint8Array,
  opts?: { slug?: string },
): Promise<LocalProjectRecord> {
  if (!bytes?.length) throw new Error("empty bundle");
  if (bytes[0] !== 0x1f || bytes[1] !== 0x8b) {
    throw new Error("unsupported bundle format (expected .tgz / gzip)");
  }
  const fflate = await loadFflate();
  const tar = fflate.gunzipSync(bytes);
  const entries = extractTar(tar);

  // Strip the single top-level dir, matching addPart in phone_backend.go.
  const parts = new Map<string, Uint8Array>();
  let topDir = "";
  for (const e of entries) {
    if (e.name.startsWith("/") || e.name.includes("..")) {
      throw new Error(`unsafe bundle entry: ${e.name}`);
    }
    const idx = e.name.indexOf("/");
    if (idx <= 0) {
      parts.set(e.name, e.data);
      continue;
    }
    if (!topDir) topDir = e.name.slice(0, idx);
    parts.set(e.name.slice(idx + 1), e.data);
  }
  if (!topDir && !opts?.slug) throw new Error("bundle missing top-level directory");
  const slug = slugify(opts?.slug || topDir);

  const dbBytes = parts.get("data/app.sqlite") ?? parts.get("local.db") ?? null;
  const db = await SqliteDb.open(dbBytes && dbBytes.length ? dbBytes : null);
  try {
    const schema = tryJson<PhoneSchema>(parts.get("schema.yaml")) ?? introspectSchema(db);
    const auth = tryJson<PhoneAuth>(parts.get("auth.yaml")) ?? { personas: [] };
    const seed = tryJson<PhoneSeed>(parts.get("seed.json")) ?? {};
    const app = tryJson<PhoneAppSpec>(parts.get("app.yaml")) ?? {};
    const manifest = tryJson<{ name?: string }>(parts.get("yaver.serverless.yaml"));
    const proj = tryJson<{ name?: string }>(parts.get(".yaver/project.yaml"));
    const name = manifest?.name || proj?.name || humanize(slug);

    const exported = db.export();
    void computeStats(db, exported.length); // validates the db reads
    const now = new Date().toISOString();
    return { slug, name, createdAt: now, updatedAt: now, schema, auth, seed, app, db: exported };
  } finally {
    db.close();
  }
}
