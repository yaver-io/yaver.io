// phoneSandboxSource.ts — on-device source-tree storage for a phone
// project. Slice 1 of the phone-first dev stack
// (docs/phone-first-dev-stack.md). The phone today only persists
// schema/auth/seed/SQLite under a project; this module gives each
// project a `<doc>/phone-projects/<slug>/src/` tree where the
// developer's JS/TS source lives, edited by the upcoming code editor
// screen and round-tripped through the export/import pipeline.
//
// Hard rules:
//   1. Only relative posix paths. No "..", no absolute paths, no
//      backslashes (which would let "..\foo" sneak through on a
//      filesystem that treats them as separators).
//   2. UTF-8 text only. Binary asset support is a follow-up.
//   3. Atomic writes — write to <path>.tmp, rename. A crash mid-
//      write leaves the previous file intact.
//   4. Strict slug validation. Slugs come from user input on the
//      phone; we re-validate every entrypoint instead of trusting a
//      caller upstream.
//
// The actual filesystem calls flow through SandboxFsAdapter from
// phoneSandboxFs.ts (interface only — no expo dep). Production RN
// code wires in expoFsAdapter via phoneSandboxSourceDefault.ts; the
// mobile-headless test harness injects an in-memory adapter so this
// module's contract is testable without a real iOS/Android FS.

import type { SandboxFsAdapter } from "./phoneSandboxFs";

const SOURCE_DIR_NAME = "src";

/** Sentinel error: the requested file (or project) does not exist. */
export class SourceFileNotFoundError extends Error {
  readonly slug: string;
  readonly relPath: string;
  constructor(slug: string, relPath: string) {
    super(`source file not found: ${slug}/${relPath}`);
    this.name = "SourceFileNotFoundError";
    this.slug = slug;
    this.relPath = relPath;
  }
}

export function isSourceFileNotFound(err: unknown): err is SourceFileNotFoundError {
  return err instanceof SourceFileNotFoundError;
}

/** Sentinel error: the supplied path is unsafe (traversal, absolute, etc.). */
export class UnsafeSourcePathError extends Error {
  readonly relPath: string;
  constructor(relPath: string, reason: string) {
    super(`unsafe source path ${JSON.stringify(relPath)}: ${reason}`);
    this.name = "UnsafeSourcePathError";
    this.relPath = relPath;
  }
}

export interface SourceFileEntry {
  /** Posix-relative path inside the project's `src/` tree. */
  path: string;
  isDirectory: boolean;
  /** Bytes — populated for files only. Directories report 0. */
  size: number;
  /** Last-modified ISO timestamp; "" when the underlying FS doesn't
   *  expose mtime (some Expo file-system targets don't). */
  modifiedAt: string;
}

const SLUG_RE = /^[a-z0-9-]+$/;

function ensureSlug(slug: string): string {
  if (typeof slug !== "string" || !slug) throw new UnsafeSourcePathError(slug, "empty slug");
  if (!SLUG_RE.test(slug)) {
    throw new UnsafeSourcePathError(slug, "slug must match /^[a-z0-9-]+$/");
  }
  return slug;
}

/** Validate + normalise a relative path. Returns the cleaned path
 *  with no leading "/" and no ".." segments. Throws
 *  UnsafeSourcePathError on any rule violation. */
export function normaliseSourceRelPath(rel: string): string {
  if (typeof rel !== "string") throw new UnsafeSourcePathError(String(rel), "must be a string");
  if (!rel.length) throw new UnsafeSourcePathError(rel, "empty path");
  if (rel.includes("\\")) {
    throw new UnsafeSourcePathError(rel, "backslash not allowed; use posix forward slashes");
  }
  if (rel.includes("\0")) {
    throw new UnsafeSourcePathError(rel, "NUL byte not allowed");
  }
  // Strip leading "./" repeatedly so "./App.tsx" works the same as "App.tsx".
  let cleaned = rel;
  while (cleaned.startsWith("./")) cleaned = cleaned.slice(2);
  if (cleaned.startsWith("/")) {
    throw new UnsafeSourcePathError(rel, "absolute path not allowed");
  }
  // Reject "..", "../", "foo/..", and anything that contains a ".."
  // segment. Path.normalize-style rewriting would silently let
  // "src/../src/App.tsx" through; rejecting any ".." segment outright
  // is safer.
  const segments = cleaned.split("/");
  for (const seg of segments) {
    if (seg === "" && cleaned !== "") {
      // "//" repeated separator
      throw new UnsafeSourcePathError(rel, "empty path segment (double slash)");
    }
    if (seg === "..") {
      throw new UnsafeSourcePathError(rel, "path traversal not allowed");
    }
  }
  return cleaned;
}

/** Build a SourceStore bound to a particular SandboxFsAdapter.
 *  Production callers use the default `phoneSandboxSourceStore`
 *  exported below; tests inject their own adapter. */
export function createSourceStore(fs: SandboxFsAdapter) {
  function projectSourceRoot(slug: string): string {
    ensureSlug(slug);
    const docDir = fs.documentDirectory;
    if (!docDir) {
      throw new Error("phoneSandboxSource: documentDirectory unavailable");
    }
    return `${docDir}phone-projects/${slug}/${SOURCE_DIR_NAME}/`;
  }

  function fileURI(slug: string, rel: string): string {
    return projectSourceRoot(slug) + normaliseSourceRelPath(rel);
  }

  async function readSourceFile(slug: string, relPath: string): Promise<string> {
    const uri = fileURI(slug, relPath);
    const info = await fs.getInfo(uri);
    if (!info.exists || info.isDirectory) {
      throw new SourceFileNotFoundError(slug, relPath);
    }
    return fs.readText(uri);
  }

  async function writeSourceFile(slug: string, relPath: string, content: string): Promise<void> {
    const cleaned = normaliseSourceRelPath(relPath);
    const root = projectSourceRoot(slug);
    const targetUri = root + cleaned;
    const slash = cleaned.lastIndexOf("/");
    if (slash >= 0) {
      await fs.mkdirp(root + cleaned.slice(0, slash) + "/");
    } else {
      await fs.mkdirp(root);
    }
    const tmpUri = `${targetUri}.tmp`;
    await fs.writeText(tmpUri, content);
    try {
      await fs.move({ from: tmpUri, to: targetUri });
    } catch (e) {
      // Make sure no .tmp turd is left behind even if move failed.
      await fs.remove(tmpUri, { idempotent: true });
      throw e;
    }
  }

  async function deleteSourceFile(slug: string, relPath: string): Promise<void> {
    const uri = fileURI(slug, relPath);
    await fs.remove(uri, { idempotent: true });
  }

  async function deleteSourceTree(slug: string): Promise<void> {
    const uri = projectSourceRoot(slug);
    await fs.remove(uri, { idempotent: true });
  }

  async function walk(rootUri: string, relSoFar: string, into: SourceFileEntry[]): Promise<void> {
    const here = rootUri + relSoFar;
    const entries = await fs.readDir(here);
    for (const name of entries) {
      if (name.endsWith(".tmp")) continue; // skip in-flight atomic writes
      const childRel = relSoFar ? `${relSoFar}/${name}` : name;
      const childInfo = await fs.getInfo(rootUri + childRel);
      const modAt = childInfo.modificationTime;
      into.push({
        path: childRel,
        isDirectory: childInfo.isDirectory,
        size: childInfo.isDirectory ? 0 : childInfo.size,
        modifiedAt: modAt > 0 ? new Date(modAt * 1000).toISOString() : "",
      });
      if (childInfo.isDirectory) await walk(rootUri, childRel, into);
    }
  }

  async function listSourceFiles(slug: string): Promise<SourceFileEntry[]> {
    const root = projectSourceRoot(slug);
    const rootInfo = await fs.getInfo(root);
    if (!rootInfo.exists) return [];
    const out: SourceFileEntry[] = [];
    await walk(root, "", out);
    out.sort((a, b) => a.path.localeCompare(b.path));
    return out;
  }

  async function hasSource(slug: string): Promise<boolean> {
    const files = await listSourceFiles(slug);
    return files.some((entry) => !entry.isDirectory);
  }

  return {
    readSourceFile,
    writeSourceFile,
    deleteSourceFile,
    deleteSourceTree,
    listSourceFiles,
    hasSource,
  };
}

// Default production binding lives in phoneSandboxSourceDefault.ts so
// the test harness can import this file without dragging
// expo-file-system through Bun's resolver. RN code imports the named
// helpers from `./phoneSandboxSourceDefault` directly.
