"use client";

// sqlite.ts — a thin, browser-local SQLite engine built on sql.js (WASM).
//
// SqliteDb wraps an in-memory sql.js database. db.export() returns the bytes of
// a *real* SQLite file — this is exactly the `data/app.sqlite` payload the Yaver
// Serverless bundle expects, so a project edited in the browser produces a
// byte-compatible, zero-loss bundle for `/phone/projects/receive`.

import { loadSqlJs, type SqlJsDatabase } from "./cdn";

export class SqliteDb {
  private constructor(private db: SqlJsDatabase) {}

  /** Open an existing SQLite file (bytes) or create a fresh empty database. */
  static async open(bytes?: Uint8Array | null): Promise<SqliteDb> {
    const SQL = await loadSqlJs();
    return new SqliteDb(new SQL.Database(bytes && bytes.length ? bytes : null));
  }

  /** Execute one or more statements with no result rows. */
  run(sql: string, params?: unknown[] | Record<string, unknown>): void {
    this.db.run(sql, params);
  }

  /** Run a query and return rows as plain objects. */
  query(sql: string, params?: unknown[] | Record<string, unknown>): Array<Record<string, unknown>> {
    const stmt = this.db.prepare(sql, params);
    const out: Array<Record<string, unknown>> = [];
    try {
      while (stmt.step()) out.push(stmt.getAsObject());
    } finally {
      stmt.free();
    }
    return out;
  }

  /** Single scalar helper (first column of first row), or undefined. */
  scalar(sql: string, params?: unknown[] | Record<string, unknown>): unknown {
    const rows = this.query(sql, params);
    if (!rows.length) return undefined;
    const first = rows[0];
    const keys = Object.keys(first);
    return keys.length ? first[keys[0]] : undefined;
  }

  /** List user tables (excludes sqlite_* internal tables). */
  tableNames(): string[] {
    const rows = this.query(
      "SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name",
    );
    return rows.map((r) => String(r.name));
  }

  rowCount(table: string): number {
    const n = this.scalar(`SELECT COUNT(*) AS n FROM "${table.replace(/"/g, '""')}"`);
    return typeof n === "number" ? n : Number(n ?? 0);
  }

  /** PRAGMA table_info for schema introspection. */
  columns(table: string): Array<{ name: string; type: string; notnull: number; pk: number; dflt_value: unknown }> {
    return this.query(`PRAGMA table_info("${table.replace(/"/g, '""')}")`).map((r) => ({
      name: String(r.name),
      type: String(r.type ?? ""),
      notnull: Number(r.notnull ?? 0),
      pk: Number(r.pk ?? 0),
      dflt_value: r.dflt_value ?? null,
    }));
  }

  /** Serialize to SQLite file bytes — the canonical bundle payload. */
  export(): Uint8Array {
    return this.db.export();
  }

  close(): void {
    this.db.close();
  }
}
