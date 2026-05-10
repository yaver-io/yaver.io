// Web platform stub for phoneSandboxLocal.
//
// The native (ios/android) implementation in phoneSandboxLocal.ts uses
// expo-sqlite, whose web shim imports `./wa-sqlite/wa-sqlite.wasm` —
// Metro's web bundler can't resolve `.wasm` modules without a custom
// transformer, so the whole web export (target=web-js-bundle) fails to
// build any time phoneSandboxLocal is in the import graph (via
// phoneProjects → app/phone-project/run/[slug].tsx etc).
//
// Metro picks this file at bundle time when platform=web (".web.ts"
// extension), so mobile builds resolve to phoneSandboxLocal.ts unchanged.
// The phone-projects feature is mobile-first; surfacing it in the web
// dashboard preview is not on the roadmap, so a stub is enough.
//
// Each stub mirrors its native counterpart's signature and either:
//   - returns an empty / zeroed result (read paths), so the route can
//     render an "empty state" instead of crashing, or
//   - throws a clear, explainable error (write paths), so accidental
//     calls from a web preview surface a message instead of failing
//     silently against a non-existent SQLite database.

import AsyncStorage from "@react-native-async-storage/async-storage";
import type { PhoneProject } from "./phoneProjects";

const META_PREFIX = "@yaver/local_phone_project_meta/";

function metaKey(slug: string): string {
  return `${META_PREFIX}${encodeURIComponent(slug)}`;
}

function unsupported(op: string): Error {
  return new Error(
    `phoneSandboxLocal.${op} is not available in the web preview. ` +
      `Open the project in the Yaver mobile app to read or modify its sandbox database.`,
  );
}

const emptyStats = {
  tableCount: 0,
  rowCount: 0,
  perTable: {} as Record<string, number>,
  dbBytes: 0,
};

export async function ensureLocalPhoneProject(project: PhoneProject): Promise<void> {
  if (!project.slug) return;
  // Persist the metadata snapshot so the web dashboard can list /
  // navigate phone projects even though the SQLite half is unavailable.
  const snapshot: PhoneProject = { ...project, stats: undefined };
  await AsyncStorage.setItem(metaKey(project.slug), JSON.stringify(snapshot));
}

export async function getLocalPhoneProjectMeta(slug: string): Promise<PhoneProject | null> {
  const raw = await AsyncStorage.getItem(metaKey(slug));
  if (!raw) return null;
  const project = JSON.parse(raw) as PhoneProject;
  return { ...project, stats: emptyStats };
}

export async function listLocalPhoneProjectsMeta(): Promise<PhoneProject[]> {
  const keys = (await AsyncStorage.getAllKeys()).filter((key) => key.startsWith(META_PREFIX));
  const items = await AsyncStorage.multiGet(keys);
  const projects: PhoneProject[] = [];
  for (const [, raw] of items) {
    if (!raw) continue;
    const project = JSON.parse(raw) as PhoneProject;
    projects.push({ ...project, stats: emptyStats });
  }
  return projects;
}

export async function deleteLocalPhoneProject(slug: string): Promise<void> {
  await AsyncStorage.removeItem(metaKey(slug));
}

export async function browseLocalPhoneTable(
  _slug: string,
  _table: string,
  _limit = 100,
): Promise<Array<Record<string, unknown>>> {
  return [];
}

export async function dumpLocalPhoneProjectRows(
  _project: PhoneProject,
): Promise<Record<string, Array<Record<string, unknown>>>> {
  return {};
}

export async function insertLocalPhoneRow(
  _slug: string,
  _table: string,
  _doc: Record<string, unknown>,
): Promise<string> {
  throw unsupported("insertLocalPhoneRow");
}

export async function updateLocalPhoneRow(
  _slug: string,
  _table: string,
  _id: string,
  _fields: Record<string, unknown>,
): Promise<void> {
  throw unsupported("updateLocalPhoneRow");
}

export async function deleteLocalPhoneRow(
  _slug: string,
  _table: string,
  _id: string,
): Promise<void> {
  throw unsupported("deleteLocalPhoneRow");
}

export async function getLocalPhoneProjectStats(_slug: string) {
  return emptyStats;
}
