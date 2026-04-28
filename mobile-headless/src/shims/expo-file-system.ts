// Shim for expo-file-system.
//
// Backed by Node `fs` rooted at $YMH_DATA_DIR/expo-fs/. Just enough
// surface for the mobile lib's phoneSandboxSource module. URIs use
// the file:// scheme so call sites that concatenate strings (the
// real expo-file-system pattern) work unchanged.

import * as fs from "node:fs";
import * as fsp from "node:fs/promises";
import * as path from "node:path";
import * as url from "node:url";
import { dataDir } from "./storage-paths.js";

const ROOT_DIR_NAME = "expo-fs";
const DOC_DIR_NAME = "doc";

export const EncodingType = {
  UTF8: "utf8" as const,
  Base64: "base64" as const,
};

function rootFsPath(): string {
  return path.join(dataDir(), ROOT_DIR_NAME);
}

function docDirPath(): string {
  return path.join(rootFsPath(), DOC_DIR_NAME);
}

function ensureRootSync(): void {
  fs.mkdirSync(docDirPath(), { recursive: true });
}

function uriToPath(uri: string): string {
  // Accept file:// URIs and bare paths. The mobile code always
  // passes file:// (because documentDirectory is a file:// URI).
  if (uri.startsWith("file://")) {
    return url.fileURLToPath(uri);
  }
  return uri;
}

function pathToUri(p: string): string {
  return url.pathToFileURL(p).toString() + (p.endsWith("/") ? "" : ""); // pathToFileURL handles trailing slash via the input
}

/** documentDirectory is a getter in the real expo-file-system; we
 *  match that so call sites that read it once at module load time
 *  see a stable URI. The trailing slash matches Expo's contract. */
export const documentDirectory = (() => {
  ensureRootSync();
  // pathToFileURL strips a trailing slash; add it back so caller
  // concatenation (`${documentDirectory}phone-projects/...`) works.
  let uri = pathToUri(docDirPath());
  if (!uri.endsWith("/")) uri += "/";
  return uri;
})();

export const cacheDirectory = (() => {
  const cache = path.join(rootFsPath(), "cache");
  fs.mkdirSync(cache, { recursive: true });
  let uri = pathToUri(cache);
  if (!uri.endsWith("/")) uri += "/";
  return uri;
})();

export interface FileInfo {
  uri: string;
  exists: boolean;
  isDirectory?: boolean;
  size?: number;
  modificationTime?: number;
}

export async function getInfoAsync(uri: string): Promise<FileInfo> {
  const p = uriToPath(uri);
  try {
    const st = await fsp.stat(p);
    return {
      uri,
      exists: true,
      isDirectory: st.isDirectory(),
      size: st.size,
      modificationTime: Math.floor(st.mtimeMs / 1000),
    };
  } catch (e: any) {
    if (e?.code === "ENOENT") return { uri, exists: false };
    throw e;
  }
}

export async function readAsStringAsync(
  uri: string,
  opts?: { encoding?: "utf8" | "base64" },
): Promise<string> {
  const p = uriToPath(uri);
  const enc = opts?.encoding ?? "utf8";
  if (enc === "base64") {
    const buf = await fsp.readFile(p);
    return buf.toString("base64");
  }
  return fsp.readFile(p, "utf8");
}

export async function writeAsStringAsync(
  uri: string,
  content: string,
  opts?: { encoding?: "utf8" | "base64" },
): Promise<void> {
  const p = uriToPath(uri);
  await fsp.mkdir(path.dirname(p), { recursive: true });
  const enc = opts?.encoding ?? "utf8";
  if (enc === "base64") {
    await fsp.writeFile(p, Buffer.from(content, "base64"));
    return;
  }
  await fsp.writeFile(p, content, "utf8");
}

export async function deleteAsync(uri: string, opts?: { idempotent?: boolean }): Promise<void> {
  const p = uriToPath(uri);
  try {
    await fsp.rm(p, { recursive: true, force: !!opts?.idempotent });
  } catch (e: any) {
    if (opts?.idempotent && e?.code === "ENOENT") return;
    throw e;
  }
}

export async function makeDirectoryAsync(
  uri: string,
  opts?: { intermediates?: boolean },
): Promise<void> {
  const p = uriToPath(uri);
  await fsp.mkdir(p, { recursive: !!opts?.intermediates });
}

export async function readDirectoryAsync(uri: string): Promise<string[]> {
  const p = uriToPath(uri);
  try {
    return await fsp.readdir(p);
  } catch (e: any) {
    if (e?.code === "ENOENT") return [];
    throw e;
  }
}

export async function moveAsync(opts: { from: string; to: string }): Promise<void> {
  const from = uriToPath(opts.from);
  const to = uriToPath(opts.to);
  await fsp.mkdir(path.dirname(to), { recursive: true });
  await fsp.rename(from, to);
}

export async function copyAsync(opts: { from: string; to: string }): Promise<void> {
  const from = uriToPath(opts.from);
  const to = uriToPath(opts.to);
  await fsp.mkdir(path.dirname(to), { recursive: true });
  await fsp.copyFile(from, to);
}

// Default export — some callers do `import FS from "expo-file-system"`
// even though the real package exports named only.
export default {
  EncodingType,
  documentDirectory,
  cacheDirectory,
  getInfoAsync,
  readAsStringAsync,
  writeAsStringAsync,
  deleteAsync,
  makeDirectoryAsync,
  readDirectoryAsync,
  moveAsync,
  copyAsync,
};
