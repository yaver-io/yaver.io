// codingAgent/gitFsExpo.ts — the RN filesystem adapter that lets isomorphic-git
// run on the phone over expo-file-system. This is the missing piece that makes
// sandboxGit.ts (checkpoints / history / revert) actually work on-device.
//
// isomorphic-git wants a POSIX-ish `{ promises: {...} }` FsClient: binary
// read/write, stat with mode + size + mtime, and ENOENT/EEXIST/ENOTDIR error
// CODES (it branches on err.code). expo-file-system gives none of that natively
// — only string/base64 read/write + getInfoAsync — so this adapter bridges the
// gap: base64 ⇄ bytes for binary git objects, and existence pre-checks that
// raise errors carrying the right `.code`.
//
// Structured as a pure factory `makeGitFs(backend)` over a minimal expo-shaped
// backend, so it's tested against REAL isomorphic-git with an in-memory backend
// (gitFsExpo.test.mts) — no expo runtime needed. The default `gitFsExpo` binds
// the real expo-file-system.

/** The slice of expo-file-system this adapter needs. Both the real module and
 *  the in-memory test backend satisfy it. URIs are opaque file:// strings. */
export interface ExpoFsBackend {
  getInfoAsync(uri: string): Promise<{ exists: boolean; isDirectory?: boolean; size?: number; modificationTime?: number }>;
  readAsStringAsync(uri: string, opts: { encoding: string }): Promise<string>;
  writeAsStringAsync(uri: string, content: string, opts: { encoding: string }): Promise<void>;
  deleteAsync(uri: string, opts?: { idempotent?: boolean }): Promise<void>;
  makeDirectoryAsync(uri: string, opts?: { intermediates?: boolean }): Promise<void>;
  readDirectoryAsync(uri: string): Promise<string[]>;
  EncodingType?: { UTF8?: string; Base64?: string };
}

function err(code: string, path: string, syscall: string): NodeJS.ErrnoException {
  const e = new Error(`${code}: ${syscall} '${path}'`) as NodeJS.ErrnoException;
  e.code = code;
  e.path = path;
  e.syscall = syscall;
  return e;
}

interface Stat {
  type: "file" | "dir" | "symlink";
  mode: number;
  size: number;
  ino: number;
  mtimeMs: number;
  ctimeMs: number;
  uid: number;
  gid: number;
  dev: number;
  isFile(): boolean;
  isDirectory(): boolean;
  isSymbolicLink(): boolean;
}

/** Resolve "."/".."/"" and collapse "//" in a POSIX path → "/a/b" form. */
function normalizePosix(p: string): string {
  const out: string[] = [];
  for (const seg of p.split("/")) {
    if (seg === "" || seg === ".") continue;
    if (seg === "..") out.pop();
    else out.push(seg);
  }
  return "/" + out.join("/");
}

/** Parent dir URI, or null when `path` is at/above the scheme authority root
 *  (e.g. "file:///x" → "file:///"? no — its parent is the authority root, which
 *  we treat as always-present and return null for, so callers don't force a
 *  spurious ENOENT). Safe against scheme roots like file:// / mem://. */
function parentUri(path: string): string | null {
  const t = path.replace(/\/+$/, "");
  const schemeEnd = t.indexOf("://");
  const slash = t.lastIndexOf("/");
  if (schemeEnd < 0 || slash <= schemeEnd + 2) return null;
  return t.slice(0, slash);
}

/** Stable-ish inode from the path so ig's stat cache compares consistently. */
function inoOf(path: string): number {
  let h = 0;
  for (let i = 0; i < path.length; i++) h = (h * 31 + path.charCodeAt(i)) | 0;
  return Math.abs(h);
}

/**
 * @param backend the expo-file-system slice
 * @param baseUri the expo root URI (e.g. FileSystem.documentDirectory,
 *   "file:///.../Documents/"). isomorphic-git operates on clean POSIX paths in a
 *   virtual root (it collapses "//" and strips schemes, so we MUST NOT feed it
 *   "file:///..."); this adapter maps those POSIX paths under baseUri. Callers
 *   pass sandboxGit a logical dir like "/phone-projects/<slug>".
 */
export function makeGitFs(backend: ExpoFsBackend, baseUri: string) {
  const UTF8 = backend.EncodingType?.UTF8 ?? "utf8";
  const BASE64 = backend.EncodingType?.Base64 ?? "base64";
  const base = baseUri.endsWith("/") ? baseUri : baseUri + "/";
  /** POSIX virtual path (what ig passes) → expo file:// URI. ig builds paths by
   *  string-templating `${dir}/${fullpath}`, so they can contain "." / "//" /
   *  ".." segments a real fs would resolve but expo won't — normalize first. */
  const toUri = (p: string): string => base + normalizePosix(p).replace(/^\/+/, "");

  function encof(options?: string | { encoding?: string }): string | undefined {
    if (typeof options === "string") return options;
    return options?.encoding;
  }

  async function stat(path: string, syscall: string): Promise<Stat> {
    const uri = toUri(path);
    const info = await backend.getInfoAsync(uri);
    if (!info.exists) throw err("ENOENT", path, syscall);
    const isDir = !!info.isDirectory;
    const mtimeMs = (info.modificationTime ?? 0) * 1000;
    return {
      type: isDir ? "dir" : "file",
      mode: isDir ? 0o40000 : 0o100644,
      size: info.size ?? 0,
      ino: inoOf(uri),
      mtimeMs,
      ctimeMs: mtimeMs,
      uid: 1,
      gid: 1,
      dev: 1,
      isFile: () => !isDir,
      isDirectory: () => isDir,
      isSymbolicLink: () => false,
    };
  }

  const promises = {
    async readFile(path: string, options?: string | { encoding?: string }): Promise<string | Uint8Array> {
      const uri = toUri(path);
      const info = await backend.getInfoAsync(uri);
      if (!info.exists) throw err("ENOENT", path, "open");
      if (info.isDirectory) throw err("EISDIR", path, "read");
      if (encof(options) === "utf8") {
        return backend.readAsStringAsync(uri, { encoding: UTF8 });
      }
      const b64 = await backend.readAsStringAsync(uri, { encoding: BASE64 });
      return base64ToBytes(b64);
    },

    async writeFile(path: string, data: string | Uint8Array, options?: string | { encoding?: string }): Promise<void> {
      const uri = toUri(path);
      // isomorphic-git relies on write() failing with ENOENT when the parent dir
      // is missing, so it can mkdir the parent and retry. expo's raw error has no
      // code, so pre-check and raise a coded ENOENT.
      const parent = parentUri(uri);
      if (parent && !(await backend.getInfoAsync(parent)).exists) {
        throw err("ENOENT", path, "open");
      }
      if (typeof data === "string" && encof(options) !== "base64") {
        await backend.writeAsStringAsync(uri, data, { encoding: UTF8 });
        return;
      }
      const bytes = typeof data === "string" ? base64ToBytes(data) : data;
      await backend.writeAsStringAsync(uri, bytesToBase64(bytes), { encoding: BASE64 });
    },

    async unlink(path: string): Promise<void> {
      const uri = toUri(path);
      const info = await backend.getInfoAsync(uri);
      if (!info.exists) throw err("ENOENT", path, "unlink");
      await backend.deleteAsync(uri, { idempotent: true });
    },

    async readdir(path: string): Promise<string[]> {
      const uri = toUri(path);
      const info = await backend.getInfoAsync(uri);
      if (!info.exists) throw err("ENOENT", path, "scandir");
      if (!info.isDirectory) throw err("ENOTDIR", path, "scandir");
      return backend.readDirectoryAsync(uri);
    },

    async mkdir(path: string): Promise<void> {
      const uri = toUri(path);
      const info = await backend.getInfoAsync(uri);
      if (info.exists) throw err("EEXIST", path, "mkdir");
      // intermediates:false so a missing parent surfaces as ENOENT (ig creates
      // parents itself); expo's raw error doesn't carry a code, so map it.
      try {
        await backend.makeDirectoryAsync(uri, { intermediates: false });
      } catch (e) {
        const parent = parentUri(uri);
        if (parent && !(await backend.getInfoAsync(parent)).exists) {
          throw err("ENOENT", path, "mkdir");
        }
        throw e;
      }
    },

    async rmdir(path: string): Promise<void> {
      const uri = toUri(path);
      const info = await backend.getInfoAsync(uri);
      if (!info.exists) throw err("ENOENT", path, "rmdir");
      if (!info.isDirectory) throw err("ENOTDIR", path, "rmdir");
      await backend.deleteAsync(uri, { idempotent: true });
    },

    stat(path: string): Promise<Stat> {
      return stat(path, "stat");
    },

    lstat(path: string): Promise<Stat> {
      return stat(path, "lstat");
    },

    // expo-file-system has no symlink support. Sandbox RN projects don't ship
    // symlinks, so ig never calls these in practice; fail loudly if it does.
    async readlink(path: string): Promise<string> {
      throw err("EINVAL", path, "readlink");
    },
    async symlink(_target: string, path: string): Promise<void> {
      throw err("EPERM", path, "symlink");
    },
    // ig calls chmod after writeFile to set the exec bit; expo can't, so no-op.
    async chmod(_path: string, _mode: number): Promise<void> {},
  };

  return { promises };
}

// ── base64 ⇄ bytes (no Buffer/atob dependency; RN-safe) ────────────────

const B64 = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/";

export function bytesToBase64(bytes: Uint8Array): string {
  let out = "";
  let i = 0;
  for (; i + 2 < bytes.length; i += 3) {
    const n = (bytes[i] << 16) | (bytes[i + 1] << 8) | bytes[i + 2];
    out += B64[(n >> 18) & 63] + B64[(n >> 12) & 63] + B64[(n >> 6) & 63] + B64[n & 63];
  }
  const rem = bytes.length - i;
  if (rem === 1) {
    const n = bytes[i] << 16;
    out += B64[(n >> 18) & 63] + B64[(n >> 12) & 63] + "==";
  } else if (rem === 2) {
    const n = (bytes[i] << 16) | (bytes[i + 1] << 8);
    out += B64[(n >> 18) & 63] + B64[(n >> 12) & 63] + B64[(n >> 6) & 63] + "=";
  }
  return out;
}

export function base64ToBytes(b64: string): Uint8Array {
  // Keep '=' so groups stay a multiple of 4; only drop whitespace/newlines.
  const s = b64.replace(/[^A-Za-z0-9+/=]/g, "");
  const pad = s.endsWith("==") ? 2 : s.endsWith("=") ? 1 : 0;
  const outLen = Math.max(0, (s.length / 4) * 3 - pad);
  const out = new Uint8Array(outLen);
  const idx = (c: string) => {
    const i = B64.indexOf(c);
    return i < 0 ? 0 : i; // '=' (and stray chars) → 0
  };
  let o = 0;
  for (let i = 0; i < s.length; i += 4) {
    const n = (idx(s[i]) << 18) | (idx(s[i + 1]) << 12) | (idx(s[i + 2]) << 6) | idx(s[i + 3]);
    if (o < outLen) out[o++] = (n >> 16) & 255;
    if (o < outLen) out[o++] = (n >> 8) & 255;
    if (o < outLen) out[o++] = n & 255;
  }
  return out;
}

/** Production: the GitFs over the real expo-file-system, rooted at the app's
 *  document directory. Pass sandboxGit a POSIX `dir` UNDER this root, e.g.
 *  "/phone-projects/<slug>". Importing this pulls in expo-file-system, so keep
 *  it out of headless tests (use makeGitFs there). */
export function createExpoGitFs() {
  // Lazy require so tests that import the pure helpers don't drag in expo.
  const FileSystem = require("expo-file-system") as ExpoFsBackend & { documentDirectory?: string };
  const root = FileSystem.documentDirectory;
  if (!root) throw new Error("createExpoGitFs: documentDirectory unavailable on this platform");
  return makeGitFs(FileSystem, root);
}

/** The POSIX virtual `dir` for a sandbox project's git repo, under the document
 *  root that createExpoGitFs is rooted at. Keep in sync with phoneSandboxSource's
 *  on-disk layout (<doc>/phone-projects/<slug>/). */
export function gitDirForSlug(slug: string): string {
  return `/phone-projects/${slug}`;
}
