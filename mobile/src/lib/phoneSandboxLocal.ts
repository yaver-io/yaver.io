import AsyncStorage from "@react-native-async-storage/async-storage";
import * as SQLite from "expo-sqlite";
import type { PhoneAuth, PhoneColumn, PhoneProject, PhoneSchema } from "./phoneProjects";

const META_PREFIX = "@yaver/local_phone_project_meta/";

function metaKey(slug: string): string {
  return `${META_PREFIX}${encodeURIComponent(slug)}`;
}

function dbName(slug: string): string {
  return `yaver-phone-${slug}.db`;
}

function q(name: string): string {
  return `"${String(name).replace(/"/g, '""')}"`;
}

function sqliteType(column: PhoneColumn): string {
  switch (String(column.type).toLowerCase()) {
    case "int":
      return "INTEGER";
    case "bool":
      return "INTEGER";
    case "real":
      return "REAL";
    default:
      return "TEXT";
  }
}

function normalizeValue(value: unknown): unknown {
  if (typeof value === "boolean") return value ? 1 : 0;
  if (value && typeof value === "object") return JSON.stringify(value);
  return value;
}

function bindValues(values: unknown[]): any[] {
  return values as any[];
}

function generateRowId(): string {
  const random = Math.random().toString(36).slice(2, 10);
  return `row_${Date.now().toString(36)}_${random}`;
}

async function openDb(slug: string) {
  return SQLite.openDatabaseAsync(dbName(slug));
}

async function ensureSchema(db: SQLite.SQLiteDatabase, schema: PhoneSchema | null | undefined) {
  if (!schema?.tables?.length) return;
  for (const table of schema.tables) {
    const cols = table.columns.map((column) => {
      const parts = [`${q(column.name)} ${sqliteType(column)}`];
      if (column.primary) parts.push("PRIMARY KEY");
      if (column.required && !column.primary) parts.push("NOT NULL");
      if (column.unique && !column.primary) parts.push("UNIQUE");
      if (column.default) {
        if (column.default === "now") parts.push("DEFAULT CURRENT_TIMESTAMP");
        else if (column.default === "false") parts.push("DEFAULT 0");
        else if (column.default === "true") parts.push("DEFAULT 1");
      }
      return parts.join(" ");
    });
    await db.execAsync(`CREATE TABLE IF NOT EXISTS ${q(table.name)} (${cols.join(", ")})`);
    const pragma = await db.getAllAsync<{ name: string }>(`PRAGMA table_info(${q(table.name)})`);
    const existing = new Set(pragma.map((row) => row.name));
    for (const column of table.columns) {
      if (existing.has(column.name)) continue;
      await db.execAsync(`ALTER TABLE ${q(table.name)} ADD COLUMN ${q(column.name)} ${sqliteType(column)}`);
    }
  }
}

async function seedTable(
  db: SQLite.SQLiteDatabase,
  table: string,
  rows: Array<Record<string, unknown>>,
) {
  for (const row of rows) {
    const working = { ...row };
    if (working.id === undefined || working.id === null || working.id === "") {
      working.id = generateRowId();
    }
    const entries = Object.entries(working);
    const columns = entries.map(([key]) => q(key)).join(", ");
    const values = entries.map(([, value]) => normalizeValue(value));
    const placeholders = entries.map(() => "?").join(", ");
    await db.runAsync(
      `INSERT OR REPLACE INTO ${q(table)} (${columns}) VALUES (${placeholders})`,
      ...bindValues(values),
    );
  }
}

async function ensureUsersFromAuth(db: SQLite.SQLiteDatabase, auth: PhoneAuth | null | undefined) {
  if (!auth?.personas?.length) return;
  for (const persona of auth.personas) {
    try {
      await db.runAsync(
        `INSERT OR IGNORE INTO "users" ("id","email","name") VALUES (?, ?, ?)`,
        persona.id,
        persona.email,
        persona.name ?? "",
      );
    } catch {
      // users table may not exist; that's fine for some schemas
    }
  }
}

export async function ensureLocalPhoneProject(project: PhoneProject): Promise<void> {
  if (!project.slug) return;
  const snapshot: PhoneProject = {
    ...project,
    stats: undefined,
  };
  await AsyncStorage.setItem(metaKey(project.slug), JSON.stringify(snapshot));
  const db = await openDb(project.slug);
  await ensureSchema(db, project.schema);
  await ensureUsersFromAuth(db, project.auth);
  const currentStats = await getLocalPhoneProjectStats(project.slug);
  if ((currentStats.rowCount ?? 0) === 0 && project.seed) {
    for (const [table, rows] of Object.entries(project.seed)) {
      await seedTable(db, table, rows);
    }
  }
}

export async function getLocalPhoneProjectMeta(slug: string): Promise<PhoneProject | null> {
  const raw = await AsyncStorage.getItem(metaKey(slug));
  if (!raw) return null;
  const project = JSON.parse(raw) as PhoneProject;
  const stats = await getLocalPhoneProjectStats(slug);
  return { ...project, stats };
}

export async function listLocalPhoneProjectsMeta(): Promise<PhoneProject[]> {
  const keys = (await AsyncStorage.getAllKeys()).filter((key) => key.startsWith(META_PREFIX));
  const items = await AsyncStorage.multiGet(keys);
  const projects = await Promise.all(
    items.map(async ([, raw]) => {
      if (!raw) return null;
      const project = JSON.parse(raw) as PhoneProject;
      const stats = await getLocalPhoneProjectStats(project.slug);
      return { ...project, stats };
    }),
  );
  return projects.filter(Boolean) as PhoneProject[];
}

export async function deleteLocalPhoneProject(slug: string): Promise<void> {
  await AsyncStorage.removeItem(metaKey(slug));
  const db = await openDb(slug);
  const tables = await db.getAllAsync<{ name: string }>(
    `SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'`,
  );
  for (const table of tables) {
    await db.execAsync(`DROP TABLE IF EXISTS ${q(table.name)}`);
  }
  await db.closeAsync();
}

export async function browseLocalPhoneTable(
  slug: string,
  table: string,
  limit = 100,
): Promise<Array<Record<string, unknown>>> {
  const db = await openDb(slug);
  return db.getAllAsync<Record<string, unknown>>(
    `SELECT * FROM ${q(table)} ORDER BY rowid ASC LIMIT ?`,
    limit,
  );
}

export async function dumpLocalPhoneProjectRows(
  project: PhoneProject,
): Promise<Record<string, Array<Record<string, unknown>>>> {
  const tables = project.schema?.tables?.map((table) => table.name) ?? [];
  const out: Record<string, Array<Record<string, unknown>>> = {};
  for (const table of tables) {
    out[table] = await browseLocalPhoneTable(project.slug, table, 1000);
  }
  return out;
}

export async function insertLocalPhoneRow(
  slug: string,
  table: string,
  doc: Record<string, unknown>,
): Promise<string> {
  const db = await openDb(slug);
  const working = { ...doc };
  if (!working.id) working.id = generateRowId();
  const entries = Object.entries(working);
  const columns = entries.map(([key]) => q(key)).join(", ");
  const placeholders = entries.map(() => "?").join(", ");
  const values = entries.map(([, value]) => normalizeValue(value));
  await db.runAsync(
    `INSERT OR REPLACE INTO ${q(table)} (${columns}) VALUES (${placeholders})`,
    ...bindValues(values),
  );
  return String(working.id);
}

export async function updateLocalPhoneRow(
  slug: string,
  table: string,
  id: string,
  fields: Record<string, unknown>,
): Promise<void> {
  const db = await openDb(slug);
  const entries = Object.entries(fields);
  if (!entries.length) return;
  const assignments = entries.map(([key]) => `${q(key)} = ?`).join(", ");
  const values = entries.map(([, value]) => normalizeValue(value));
  values.push(id);
  await db.runAsync(`UPDATE ${q(table)} SET ${assignments} WHERE "id" = ?`, ...bindValues(values));
}

export async function deleteLocalPhoneRow(
  slug: string,
  table: string,
  id: string,
): Promise<void> {
  const db = await openDb(slug);
  await db.runAsync(`DELETE FROM ${q(table)} WHERE "id" = ?`, id);
}

export async function getLocalPhoneProjectStats(slug: string) {
  const db = await openDb(slug);
  const tables = await db.getAllAsync<{ name: string }>(
    `SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'`,
  );
  let rowCount = 0;
  const perTable: Record<string, number> = {};
  for (const table of tables) {
    const rows = await db.getFirstAsync<{ count: number }>(
      `SELECT COUNT(*) as count FROM ${q(table.name)}`,
    );
    const count = Number(rows?.count ?? 0);
    perTable[table.name] = count;
    rowCount += count;
  }
  return {
    tableCount: tables.length,
    rowCount,
    perTable,
    dbBytes: 0,
  };
}
