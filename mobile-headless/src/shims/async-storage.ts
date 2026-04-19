// Shim for @react-native-async-storage/async-storage.
//
// Backed by a single JSON file at `$YMH_DATA_DIR/storage.json`
// (default: ~/.yaver/mobile-headless/<runId>/storage.json). One
// harness instance → one file, so parallel test runs don't collide.

import * as fs from "node:fs";
import * as path from "node:path";
import { dataDir } from "./storage-paths.js";

function filePath(): string {
  return path.join(dataDir(), "storage.json");
}

function readAll(): Record<string, string> {
  try {
    return JSON.parse(fs.readFileSync(filePath(), "utf8"));
  } catch {
    return {};
  }
}

function writeAll(data: Record<string, string>) {
  fs.mkdirSync(path.dirname(filePath()), { recursive: true });
  fs.writeFileSync(filePath(), JSON.stringify(data));
}

export async function getItem(key: string): Promise<string | null> {
  const all = readAll();
  return key in all ? all[key] : null;
}

export async function setItem(key: string, value: string): Promise<void> {
  const all = readAll();
  all[key] = value;
  writeAll(all);
}

export async function removeItem(key: string): Promise<void> {
  const all = readAll();
  delete all[key];
  writeAll(all);
}

export async function clear(): Promise<void> {
  writeAll({});
}

export async function getAllKeys(): Promise<string[]> {
  return Object.keys(readAll());
}

export async function multiGet(keys: string[]): Promise<[string, string | null][]> {
  const all = readAll();
  return keys.map((k) => [k, k in all ? all[k] : null]);
}

export async function multiSet(pairs: [string, string][]): Promise<void> {
  const all = readAll();
  for (const [k, v] of pairs) all[k] = v;
  writeAll(all);
}

export async function multiRemove(keys: string[]): Promise<void> {
  const all = readAll();
  for (const k of keys) delete all[k];
  writeAll(all);
}

export default { getItem, setItem, removeItem, clear, getAllKeys, multiGet, multiSet, multiRemove };
