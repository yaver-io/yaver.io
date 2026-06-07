// codingAgent/repoSandbox.ts — a CodingSandbox scoped to a project's WHOLE repo
// tree (root = gitDirForSlug), not just its src/. This is what a cloned repo
// needs: the agent must read/edit package.json, app.json, convex/, app/, src/ —
// everything a real dev touches — and the user explicitly wants the on-phone
// agent to handle the Convex backend, which lives in convex/ beside src/.
//
// sandboxBinding.ts's sandboxForSlug is src/-only (right for in-app SQLite
// projects authored inside Yaver). For git-cloned projects we root the file
// tools at the SAME directory the git tools + checkpoints use (gitDirForSlug),
// so file edits and git see one identical tree. Both run over the gitFsExpo
// adapter, so there is exactly one on-device filesystem abstraction.
//
// PURE factory makeRepoSandbox(fs, dir) over the isomorphic-git-style fs, so it
// is tsx-tested against an in-memory backend (repoSandbox.test.mts). The .git
// directory is never listed, read, or written through these tools — git state is
// only ever touched via the git tools.

import type { CodingSandbox, CodingSandboxEntry } from "./sandboxTools";
import { createExpoGitFs, gitDirForSlug } from "./gitFsExpo";
import { normaliseSourceRelPath } from "../phoneSandboxSource";

/** The slice of an isomorphic-git FsClient this sandbox uses. createExpoGitFs()
 *  (and makeGitFs in tests) both satisfy it. */
export interface RepoFs {
  promises: {
    readFile(path: string, options?: string | { encoding?: string }): Promise<string | Uint8Array>;
    writeFile(path: string, data: string, options?: string | { encoding?: string }): Promise<void>;
    unlink(path: string): Promise<void>;
    readdir(path: string): Promise<string[]>;
    mkdir(path: string): Promise<void>;
    stat(path: string): Promise<{ size: number; isDirectory(): boolean }>;
  };
}

/** Validate a relative path with the same rules as the src/ store (no "..", no
 *  absolute, no backslash/NUL/double-slash) AND forbid reaching into .git. */
function safeRepoRel(rel: string): string {
  const cleaned = normaliseSourceRelPath(rel);
  if (cleaned === ".git" || cleaned.startsWith(".git/")) {
    throw new Error(`path inside .git is not editable: ${rel}`);
  }
  return cleaned;
}

/** mkdir -p for a directory under the repo root (gitFs.mkdir is non-recursive
 *  and throws EEXIST when a segment already exists). */
async function ensureDir(fs: RepoFs, absDir: string): Promise<void> {
  const parts = absDir.split("/").filter(Boolean);
  let cur = "";
  for (const p of parts) {
    cur += "/" + p;
    try {
      await fs.promises.mkdir(cur);
    } catch (e: any) {
      if (e?.code !== "EEXIST") throw e;
    }
  }
}

async function walk(fs: RepoFs, root: string, rel: string, out: CodingSandboxEntry[]): Promise<void> {
  const here = rel ? `${root}/${rel}` : root;
  let names: string[];
  try {
    names = await fs.promises.readdir(here);
  } catch {
    return; // missing/unreadable dir — nothing to list here
  }
  for (const name of names) {
    if (name === ".git") continue; // never expose git internals to the tools
    if (name.endsWith(".tmp")) continue; // in-flight atomic writes
    const childRel = rel ? `${rel}/${name}` : name;
    let st: { size: number; isDirectory(): boolean };
    try {
      st = await fs.promises.stat(`${root}/${childRel}`);
    } catch {
      continue; // raced deletion — skip
    }
    const isDirectory = st.isDirectory();
    out.push({ path: childRel, isDirectory, size: isDirectory ? 0 : st.size ?? 0 });
    if (isDirectory) await walk(fs, root, childRel, out);
  }
}

/** Build a whole-repo CodingSandbox over an fs rooted at `dir` (a POSIX virtual
 *  path like "/phone-projects/<slug>"). */
export function makeRepoSandbox(fs: RepoFs, dir: string): CodingSandbox {
  const root = dir.replace(/\/+$/, "");
  return {
    async readFile(path: string): Promise<string> {
      const r = safeRepoRel(path);
      const data = await fs.promises.readFile(`${root}/${r}`, "utf8");
      return typeof data === "string" ? data : new TextDecoder().decode(data);
    },
    async listFiles(): Promise<CodingSandboxEntry[]> {
      const out: CodingSandboxEntry[] = [];
      await walk(fs, root, "", out);
      out.sort((a, b) => a.path.localeCompare(b.path));
      return out;
    },
    async writeFile(path: string, content: string): Promise<void> {
      const r = safeRepoRel(path);
      const slash = r.lastIndexOf("/");
      if (slash >= 0) await ensureDir(fs, `${root}/${r.slice(0, slash)}`);
      else await ensureDir(fs, root);
      await fs.promises.writeFile(`${root}/${r}`, content, "utf8");
    },
    async deleteFile(path: string): Promise<void> {
      const r = safeRepoRel(path);
      try {
        await fs.promises.unlink(`${root}/${r}`);
      } catch (e: any) {
        if (e?.code !== "ENOENT") throw e; // idempotent: deleting a missing file is fine
      }
    },
  };
}

/** Production: a whole-repo CodingSandbox for a phone-local project slug, rooted
 *  at the SAME dir as gitContextForSlug — file tools and git operate on one tree. */
export function repoSandboxForSlug(slug: string): CodingSandbox {
  return makeRepoSandbox(createExpoGitFs() as unknown as RepoFs, gitDirForSlug(slug));
}
