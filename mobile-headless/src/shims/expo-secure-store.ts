// Shim for expo-secure-store.
//
// The mobile app uses it for one thing: holding the auth token. In a
// headless Node/Bun context "secure" is a matter of file perms —
// 0600 on a file inside the harness's data dir is as good as it
// reasonably gets without integrating OS keychains. Tests can also
// short-circuit this by calling `MobileClient.signIn({token})`.

import * as fs from "node:fs";
import * as path from "node:path";
import { dataDir } from "./storage-paths.js";

function filePath(): string {
  return path.join(dataDir(), "secure.json");
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
  fs.writeFileSync(filePath(), JSON.stringify(data), { mode: 0o600 });
  try { fs.chmodSync(filePath(), 0o600); } catch { /* ok on windows */ }
}

export async function getItemAsync(key: string, _options?: any): Promise<string | null> {
  const all = readAll();
  return key in all ? all[key] : null;
}

export async function setItemAsync(key: string, value: string, _options?: any): Promise<void> {
  const all = readAll();
  all[key] = value;
  writeAll(all);
}

export async function deleteItemAsync(key: string, _options?: any): Promise<void> {
  const all = readAll();
  delete all[key];
  writeAll(all);
}

export async function isAvailableAsync(): Promise<boolean> {
  return true;
}

// Some flows import the WHEN_UNLOCKED accessibility constant — leave
// a bag of sentinel values so dereferencing them doesn't blow up.
export const WHEN_UNLOCKED = "WHEN_UNLOCKED";
export const WHEN_UNLOCKED_THIS_DEVICE_ONLY = "WHEN_UNLOCKED_THIS_DEVICE_ONLY";
export const AFTER_FIRST_UNLOCK = "AFTER_FIRST_UNLOCK";
export const AFTER_FIRST_UNLOCK_THIS_DEVICE_ONLY = "AFTER_FIRST_UNLOCK_THIS_DEVICE_ONLY";
export const ALWAYS = "ALWAYS";
export const ALWAYS_THIS_DEVICE_ONLY = "ALWAYS_THIS_DEVICE_ONLY";

export default {
  getItemAsync, setItemAsync, deleteItemAsync, isAvailableAsync,
  WHEN_UNLOCKED, WHEN_UNLOCKED_THIS_DEVICE_ONLY,
  AFTER_FIRST_UNLOCK, AFTER_FIRST_UNLOCK_THIS_DEVICE_ONLY,
  ALWAYS, ALWAYS_THIS_DEVICE_ONLY,
};
