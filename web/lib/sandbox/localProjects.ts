"use client";

// localProjects.ts — browser-local backend that mirrors the agentClient phone
// methods (create/list/get/delete/tables/browse/insert/update/deleteRow/
// setSchema/setAuth/setSeed/export). Each mutation opens the project's SQLite
// bytes, applies the change, re-exports, and persists to IndexedDB. The UI
// (PhoneProjectsView) routes here when no agent is connected, and to the agent
// otherwise — same shapes, so the component stays single-path.

import type {
  PhoneAppSpec,
  PhoneAuth,
  PhoneCreateSpec,
  PhoneProject,
  PhoneSchema,
  PhoneSeed,
  PhoneTemplate,
} from "@/lib/agent-client";
import { buildBundle, parseBundle } from "./bundle";
import { applyAuth, applySchema, applySeed, computeStats } from "./schema";
import { SqliteDb } from "./sqlite";
import {
  deleteProject,
  getProject,
  hasProject,
  listProjects,
  putProject,
  type LocalProjectRecord,
} from "./store";
import {
  TEMPLATES,
  templateApp,
  templateAuth,
  templateSchema,
  templateSeed,
} from "./templates";

function slugify(s: string): string {
  return (
    s
      .toLowerCase()
      .replace(/[^a-z0-9]+/g, "-")
      .replace(/^-+|-+$/g, "")
      .slice(0, 64) || "project"
  );
}

async function uniqueSlug(base: string): Promise<string> {
  let slug = slugify(base);
  if (!(await hasProject(slug))) return slug;
  for (let i = 2; i < 1000; i++) {
    const candidate = `${slug}-${i}`;
    if (!(await hasProject(candidate))) return candidate;
  }
  return `${slug}-${Date.now()}`;
}

// Stats are left null here and filled in by callers that open the db.
function recordToProject(rec: LocalProjectRecord): PhoneProject {
  return {
    slug: rec.slug,
    name: rec.name,
    template: rec.template,
    dir: `browser://${rec.slug}`,
    createdAt: rec.createdAt,
    updatedAt: rec.updatedAt,
    schema: rec.schema ?? null,
    auth: rec.auth ?? null,
    seed: rec.seed ?? null,
    stats: null,
  };
}

async function withDb<T>(rec: LocalProjectRecord, fn: (db: SqliteDb) => T): Promise<{ result: T; bytes: Uint8Array }> {
  const db = await SqliteDb.open(rec.db?.length ? rec.db : null);
  try {
    const result = fn(db);
    const bytes = db.export();
    return { result, bytes };
  } finally {
    db.close();
  }
}

export function listLocalTemplates(): PhoneTemplate[] {
  return TEMPLATES;
}

export async function listLocalProjects(): Promise<PhoneProject[]> {
  const recs = await listProjects();
  // Recompute live stats so the list shows accurate table/row counts.
  const out: PhoneProject[] = [];
  for (const rec of recs) {
    const proj = recordToProject(rec);
    try {
      const db = await SqliteDb.open(rec.db?.length ? rec.db : null);
      proj.stats = computeStats(db, rec.db?.length ?? 0);
      db.close();
    } catch {
      /* leave stats null */
    }
    out.push(proj);
  }
  return out;
}

export async function getLocalProject(slug: string): Promise<PhoneProject | null> {
  const rec = await getProject(slug);
  if (!rec) return null;
  const proj = recordToProject(rec);
  try {
    const db = await SqliteDb.open(rec.db?.length ? rec.db : null);
    proj.stats = computeStats(db, rec.db?.length ?? 0);
    db.close();
  } catch {
    /* ignore */
  }
  return proj;
}

export async function createLocalProject(spec: PhoneCreateSpec): Promise<PhoneProject> {
  const slug = await uniqueSlug(spec.slug || spec.name);
  const template = spec.template ?? (spec.schema ? undefined : "crud");
  const schema = spec.schema ?? (template ? templateSchema(template) : { tables: [] });
  const auth = spec.auth ?? (template ? templateAuth(template) : { personas: [] });
  const seed = spec.seed ?? (template ? templateSeed(template) : {});
  const app = spec.app ?? (template ? templateApp(template) : {});

  const db = await SqliteDb.open(null);
  let bytes: Uint8Array;
  try {
    applySchema(db, schema);
    applyAuth(db, auth);
    applySeed(db, seed);
    bytes = db.export();
  } finally {
    db.close();
  }

  const now = new Date().toISOString();
  const rec: LocalProjectRecord = {
    slug,
    name: spec.name,
    template,
    createdAt: now,
    updatedAt: now,
    schema,
    auth,
    seed,
    app,
    db: bytes,
  };
  await putProject(rec);
  return getLocalProject(slug) as Promise<PhoneProject>;
}

export async function deleteLocalProject(slug: string): Promise<void> {
  await deleteProject(slug);
}

/** Schema + app spec for the live preview renderer. */
export async function getLocalAppAndSchema(
  slug: string,
): Promise<{ schema: PhoneSchema; app: PhoneAppSpec } | null> {
  const rec = await getProject(slug);
  if (!rec) return null;
  return { schema: rec.schema ?? { tables: [] }, app: rec.app ?? {} };
}

/** Read the persisted mini-figma design layer (layout + per-node overrides). */
export async function getLocalDesign(slug: string): Promise<PhoneAppSpec["design"]> {
  const rec = await getProject(slug);
  return rec?.app?.design ?? {};
}

/** Persist the design layer onto the app spec. No SQLite change — it rides in
 * app.yaml, so it survives reloads AND ships in the .yaver.tgz on deploy. */
export async function setLocalDesign(slug: string, design: NonNullable<PhoneAppSpec["design"]>): Promise<void> {
  const rec = await getProject(slug);
  if (!rec) throw new Error("project not found");
  rec.app = { ...(rec.app ?? {}), design };
  rec.updatedAt = new Date().toISOString();
  await putProject(rec);
}

export async function listLocalTables(slug: string): Promise<Array<{ name: string; rowCount?: number }>> {
  const rec = await getProject(slug);
  if (!rec) return [];
  const db = await SqliteDb.open(rec.db?.length ? rec.db : null);
  try {
    return db.tableNames().map((name) => ({ name, rowCount: db.rowCount(name) }));
  } finally {
    db.close();
  }
}

export async function browseLocalTable(
  slug: string,
  table: string,
  limit = 50,
  offset = 0,
): Promise<{ rows: Array<Record<string, unknown>>; nextCursor?: string }> {
  const rec = await getProject(slug);
  if (!rec) return { rows: [] };
  const db = await SqliteDb.open(rec.db?.length ? rec.db : null);
  try {
    const rows = db.query(`SELECT * FROM "${table.replace(/"/g, '""')}" LIMIT ? OFFSET ?`, [limit + 1, offset]);
    const hasMore = rows.length > limit;
    return {
      rows: hasMore ? rows.slice(0, limit) : rows,
      nextCursor: hasMore ? String(offset + limit) : undefined,
    };
  } finally {
    db.close();
  }
}

function primaryKeyColumn(db: SqliteDb, table: string): string {
  const pk = db.columns(table).find((c) => c.pk > 0);
  return pk?.name ?? "rowid";
}

async function persistMutation(rec: LocalProjectRecord, bytes: Uint8Array): Promise<void> {
  rec.db = bytes;
  rec.updatedAt = new Date().toISOString();
  await putProject(rec);
}

export async function insertLocalRow(slug: string, table: string, doc: Record<string, unknown>): Promise<void> {
  const rec = await getProject(slug);
  if (!rec) throw new Error("project not found");
  const { bytes } = await withDb(rec, (db) => {
    const okCols = new Set(db.columns(table).map((c) => c.name));
    const cols = Object.keys(doc).filter((k) => okCols.has(k));
    if (!cols.length) throw new Error("no matching columns for insert");
    const quoted = cols.map((c) => `"${c.replace(/"/g, '""')}"`).join(",");
    const placeholders = cols.map(() => "?").join(",");
    const values = cols.map((k) => normalize(doc[k]));
    db.run(`INSERT INTO "${table.replace(/"/g, '""')}" (${quoted}) VALUES (${placeholders})`, values);
  });
  await persistMutation(rec, bytes);
}

export async function updateLocalRow(
  slug: string,
  table: string,
  id: unknown,
  doc: Record<string, unknown>,
): Promise<void> {
  const rec = await getProject(slug);
  if (!rec) throw new Error("project not found");
  const { bytes } = await withDb(rec, (db) => {
    const pk = primaryKeyColumn(db, table);
    const okCols = new Set(db.columns(table).map((c) => c.name));
    const cols = Object.keys(doc).filter((k) => okCols.has(k) && k !== pk);
    if (!cols.length) throw new Error("no updatable columns");
    const setClause = cols.map((c) => `"${c.replace(/"/g, '""')}"=?`).join(",");
    const values = cols.map((k) => normalize(doc[k]));
    db.run(`UPDATE "${table.replace(/"/g, '""')}" SET ${setClause} WHERE "${pk}"=?`, [...values, id]);
  });
  await persistMutation(rec, bytes);
}

export async function deleteLocalRow(slug: string, table: string, id: unknown): Promise<void> {
  const rec = await getProject(slug);
  if (!rec) throw new Error("project not found");
  const { bytes } = await withDb(rec, (db) => {
    const pk = primaryKeyColumn(db, table);
    db.run(`DELETE FROM "${table.replace(/"/g, '""')}" WHERE "${pk}"=?`, [id]);
  });
  await persistMutation(rec, bytes);
}

export async function setLocalSchema(slug: string, schema: PhoneSchema): Promise<void> {
  const rec = await getProject(slug);
  if (!rec) throw new Error("project not found");
  const { bytes } = await withDb(rec, (db) => applySchema(db, schema));
  rec.schema = schema;
  await persistMutation(rec, bytes);
}

export async function setLocalAuth(slug: string, auth: PhoneAuth): Promise<void> {
  const rec = await getProject(slug);
  if (!rec) throw new Error("project not found");
  const { bytes } = await withDb(rec, (db) => applyAuth(db, auth));
  rec.auth = auth;
  await persistMutation(rec, bytes);
}

export async function setLocalSeed(slug: string, seed: PhoneSeed): Promise<void> {
  const rec = await getProject(slug);
  if (!rec) throw new Error("project not found");
  const { bytes } = await withDb(rec, (db) => applySeed(db, seed));
  rec.seed = seed;
  await persistMutation(rec, bytes);
}

/** Build a `.yaver.tgz` blob for download or deploy. */
export async function exportLocalBundle(slug: string, includeData = true): Promise<Uint8Array> {
  const rec = await getProject(slug);
  if (!rec) throw new Error("project not found");
  return buildBundle(rec, { includeData });
}

/** Import a `.yaver.tgz` into browser-local storage (conflict → rename). */
export async function importLocalBundle(bytes: Uint8Array, slugOverride?: string): Promise<PhoneProject> {
  const parsed = await parseBundle(bytes, slugOverride ? { slug: slugOverride } : undefined);
  parsed.slug = await uniqueSlug(parsed.slug);
  await putProject(parsed);
  return getLocalProject(parsed.slug) as Promise<PhoneProject>;
}

function normalize(v: unknown): unknown {
  if (v === null || v === undefined) return null;
  if (typeof v === "boolean") return v ? 1 : 0;
  if (typeof v === "object") return JSON.stringify(v);
  return v;
}
