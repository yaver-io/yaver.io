"use client";

// schema.ts — mirrors desktop/agent/phone_backend.go schema/auth/seed logic so
// a browser-built SQLite file is byte-and-shape compatible with what the Go
// agent produces. Keep these in sync: column type map (allowedColumnTypes),
// DDL assembly (sqliteColumnDDL), index naming (idx_<table>_<i>), the users
// persona mirror (ApplyPhoneAuth), and seed insert mode (INSERT OR REPLACE).

import type {
  PhoneAuth,
  PhoneColumn,
  PhoneSchema,
  PhoneSeed,
  PhoneStats,
  PhoneTable,
} from "@/lib/agent-client";
import type { SqliteDb } from "./sqlite";

// Mirrors allowedColumnTypes in phone_backend.go:616.
const COLUMN_TYPES: Record<string, string> = {
  text: "TEXT",
  string: "TEXT",
  int: "INTEGER",
  integer: "INTEGER",
  bool: "INTEGER",
  boolean: "INTEGER",
  real: "REAL",
  float: "REAL",
  timestamp: "TEXT",
  json: "TEXT",
  uuid: "TEXT",
};

function quoteIdent(name: string): string {
  return `"${name.replace(/"/g, '""')}"`;
}

// Mirrors sqliteLiteral in phone_backend.go:658.
function sqliteLiteral(v: string): string {
  if (v === "") return "''";
  if (/^-?\d+$/.test(v)) return v;
  if (v === "true") return "1";
  if (v === "false") return "0";
  return `'${v.replace(/'/g, "''")}'`;
}

// Mirrors sqliteColumnDDL in phone_backend.go:630.
function columnDDL(c: PhoneColumn): string {
  const sqlType = COLUMN_TYPES[(c.type || "").toLowerCase()];
  if (!sqlType) {
    throw new Error(
      `unsupported column type "${c.type}" (allowed: text,int,bool,real,timestamp,json,uuid)`,
    );
  }
  const parts: string[] = [quoteIdent(c.name), sqlType];
  if (c.primary) parts.push("PRIMARY KEY");
  if (c.required && !c.primary) parts.push("NOT NULL");
  if (c.unique && !c.primary) parts.push("UNIQUE");
  if (c.default) {
    switch (c.default.toLowerCase()) {
      case "uuid":
        parts.push("DEFAULT (lower(hex(randomblob(16))))");
        break;
      case "now":
        parts.push("DEFAULT CURRENT_TIMESTAMP");
        break;
      default:
        parts.push("DEFAULT " + sqliteLiteral(c.default));
    }
  }
  return parts.join(" ");
}

/** Additively create tables + columns + indexes to match the schema. */
export function applySchema(db: SqliteDb, schema: PhoneSchema): void {
  db.run("PRAGMA foreign_keys = ON");
  const existing = new Set(db.tableNames());
  for (const t of schema.tables) {
    if (!t.name || !t.columns?.length) {
      throw new Error(`table "${t.name}" missing columns`);
    }
    if (!existing.has(t.name)) {
      const cols = t.columns.map(columnDDL).join(", ");
      db.run(`CREATE TABLE ${quoteIdent(t.name)} (${cols})`);
    } else {
      const have = new Set(db.columns(t.name).map((c) => c.name));
      for (const c of t.columns) {
        if (have.has(c.name)) continue;
        db.run(`ALTER TABLE ${quoteIdent(t.name)} ADD COLUMN ${columnDDL(c)}`);
      }
    }
    (t.indexes ?? []).forEach((idx, i) => {
      if (!idx.columns?.length) return;
      const unique = idx.unique ? "UNIQUE " : "";
      const name = `idx_${t.name}_${i}`;
      const cols = idx.columns.map(quoteIdent).join(",");
      db.run(`CREATE ${unique}INDEX IF NOT EXISTS ${quoteIdent(name)} ON ${quoteIdent(t.name)} (${cols})`);
    });
  }
}

/** Mirror personas into a `users` table when one exists (phone_backend.go:793). */
export function applyAuth(db: SqliteDb, auth: PhoneAuth): void {
  if (!db.tableNames().includes("users")) return;
  for (const p of auth.personas ?? []) {
    db.run(`INSERT OR IGNORE INTO "users" (id,email,name) VALUES (?,?,?)`, [
      p.id,
      p.email,
      p.name ?? null,
    ]);
  }
}

/** Apply seed rows with INSERT OR REPLACE, dropping unknown columns. */
export function applySeed(db: SqliteDb, seed: PhoneSeed): void {
  const tables = new Set(db.tableNames());
  for (const [table, rows] of Object.entries(seed)) {
    if (!tables.has(table)) {
      throw new Error(`seed: table "${table}" does not exist — apply schema first`);
    }
    const okCols = new Set(db.columns(table).map((c) => c.name));
    for (const row of rows) {
      const cols = Object.keys(row).filter((k) => okCols.has(k));
      if (!cols.length) continue;
      const placeholders = cols.map(() => "?").join(",");
      const quoted = cols.map(quoteIdent).join(",");
      const values = cols.map((k) => normalizeValue(row[k]));
      db.run(
        `INSERT OR REPLACE INTO ${quoteIdent(table)} (${quoted}) VALUES (${placeholders})`,
        values,
      );
    }
  }
}

function normalizeValue(v: unknown): unknown {
  if (v === null || v === undefined) return null;
  if (typeof v === "boolean") return v ? 1 : 0;
  if (typeof v === "object") return JSON.stringify(v);
  return v;
}

/** Generate informational `schema.sql` DDL text (sqlite dialect). */
export function schemaToDDL(schema: PhoneSchema): string {
  const out: string[] = [];
  for (const t of schema.tables ?? []) {
    if (!t.name || !t.columns?.length) continue;
    const cols = t.columns.map((c) => "  " + columnDDL(c)).join(",\n");
    out.push(`CREATE TABLE IF NOT EXISTS ${quoteIdent(t.name)} (\n${cols}\n);`);
  }
  return out.join("\n\n");
}

// ── Introspection (for import / stats) ───────────────────────────────────────

/** Reconstruct a PhoneSchema from a live SQLite db (used on bundle import). */
export function introspectSchema(db: SqliteDb): PhoneSchema {
  const tables: PhoneTable[] = db.tableNames().map((name) => ({
    name,
    columns: db.columns(name).map((c) => ({
      name: c.name,
      type: introspectType(c.type),
      primary: c.pk > 0 || undefined,
      required: c.notnull > 0 || undefined,
    })),
  }));
  return { tables };
}

function introspectType(sqlType: string): string {
  const t = sqlType.toUpperCase();
  if (t.includes("INT")) return "int";
  if (t.includes("REAL") || t.includes("FLOA") || t.includes("DOUB")) return "real";
  return "text";
}

export function computeStats(db: SqliteDb, dbBytes: number): PhoneStats {
  const names = db.tableNames();
  const perTable: Record<string, number> = {};
  let rowCount = 0;
  for (const n of names) {
    const c = db.rowCount(n);
    perTable[n] = c;
    rowCount += c;
  }
  return { tableCount: names.length, rowCount, perTable, dbBytes };
}
