"use client";

// store.ts — IndexedDB persistence for browser-local sandbox projects.
//
// One record per project, keyed by slug. The record carries the project
// metadata (mirroring PhoneProject) plus the serialized SQLite file bytes and
// any custom source files used by the preview bundler. The SQLite bytes ARE the
// canonical `data/app.sqlite` payload, so nothing is lost moving to a bundle.

import type {
  PhoneAppSpec,
  PhoneAuth,
  PhoneSchema,
  PhoneSeed,
} from "@/lib/agent-client";

export interface LocalProjectRecord {
  slug: string;
  name: string;
  template?: string;
  createdAt: string;
  updatedAt: string;
  schema?: PhoneSchema | null;
  auth?: PhoneAuth | null;
  seed?: PhoneSeed | null;
  app?: PhoneAppSpec | null;
  /** Optional custom source files (path -> content) for the preview bundler. */
  src?: Record<string, string>;
  /** Serialized SQLite database file — the canonical bundle payload. */
  db: Uint8Array;
}

const DB_NAME = "yaver-sandbox";
const DB_VERSION = 1;
const STORE = "projects";

let dbPromise: Promise<IDBDatabase> | null = null;

function openDb(): Promise<IDBDatabase> {
  if (dbPromise) return dbPromise;
  if (typeof indexedDB === "undefined") {
    return Promise.reject(new Error("IndexedDB unavailable (server render?)"));
  }
  dbPromise = new Promise((resolve, reject) => {
    const req = indexedDB.open(DB_NAME, DB_VERSION);
    req.onupgradeneeded = () => {
      const db = req.result;
      if (!db.objectStoreNames.contains(STORE)) {
        db.createObjectStore(STORE, { keyPath: "slug" });
      }
    };
    req.onsuccess = () => resolve(req.result);
    req.onerror = () => reject(req.error ?? new Error("indexedDB open failed"));
  });
  return dbPromise;
}

function tx<T>(mode: IDBTransactionMode, fn: (store: IDBObjectStore) => IDBRequest<T>): Promise<T> {
  return openDb().then(
    (db) =>
      new Promise<T>((resolve, reject) => {
        const t = db.transaction(STORE, mode);
        const req = fn(t.objectStore(STORE));
        req.onsuccess = () => resolve(req.result);
        req.onerror = () => reject(req.error ?? new Error("indexedDB request failed"));
      }),
  );
}

export async function putProject(rec: LocalProjectRecord): Promise<void> {
  await tx("readwrite", (s) => s.put(rec));
}

export async function getProject(slug: string): Promise<LocalProjectRecord | null> {
  const rec = await tx<LocalProjectRecord | undefined>("readonly", (s) => s.get(slug));
  return rec ?? null;
}

export async function deleteProject(slug: string): Promise<void> {
  await tx("readwrite", (s) => s.delete(slug));
}

export async function listProjects(): Promise<LocalProjectRecord[]> {
  const all = await tx<LocalProjectRecord[]>("readonly", (s) => s.getAll());
  return (all ?? []).sort((a, b) => (a.updatedAt < b.updatedAt ? 1 : -1));
}

export async function hasProject(slug: string): Promise<boolean> {
  const key = await tx<IDBValidKey | undefined>("readonly", (s) => s.getKey(slug));
  return key !== undefined;
}
