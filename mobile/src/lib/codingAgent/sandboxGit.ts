// codingAgent/sandboxGit.ts — version control for a phone-sandbox project, on
// top of isomorphic-git (pure JS, no binary — the path docs/coding-agent-on-
// device.md commits to). PURE w.r.t. the filesystem: every function takes an
// isomorphic-git-shaped `fs` + the project `dir`, so the whole thing is tsx-
// tested against Node's fs in a temp dir. The RN binding (expo-file-system fs
// adapter) lives in gitFsExpo.ts.
//
// WHY git in the sandbox: the agentic coding loop (runner.ts) edits files
// autonomously. Wrapping each run in a before/after checkpoint gives a guaranteed
// rollback point ("Revert this run") and a real history the user can inspect —
// the safety net that makes yolo-mode agentic edits acceptable on a phone.
//
// The repo lives at the PROJECT ROOT (<doc>/phone-projects/<slug>/), so `.git`
// is a sibling of `src/` and commits capture the whole project, not just src/.

import git from "isomorphic-git";

/** Minimal structural type for the isomorphic-git fs client. Node's `fs` and the
 *  expo adapter both satisfy it; we keep it loose (`any`) to avoid coupling to
 *  isomorphic-git's internal FsClient type across versions. */
export type GitFs = any;

export interface SandboxGitOptions {
  fs: GitFs;
  dir: string;
}

/** Commit author — the agent acts as itself; overridable for tests / the user. */
export const DEFAULT_GIT_AUTHOR = { name: "Yaver Agent", email: "agent@yaver.io" } as const;

export type FileStatus = "added" | "modified" | "deleted" | "unmodified";

export interface ChangedFile {
  path: string;
  status: FileStatus;
}

export interface CommitEntry {
  oid: string;
  message: string;
  /** Unix seconds (author timestamp). */
  timestamp: number;
}

/** Initialise the repo if it isn't one yet. Idempotent. Returns true if it
 *  created a fresh repo. */
export async function ensureRepo(o: SandboxGitOptions): Promise<boolean> {
  if (await isRepo(o)) return false;
  await git.init({ fs: o.fs, dir: o.dir, defaultBranch: "main" });
  return true;
}

export async function isRepo(o: SandboxGitOptions): Promise<boolean> {
  try {
    await git.resolveRef({ fs: o.fs, dir: o.dir, ref: "HEAD", depth: 1 });
    return true;
  } catch {
    // resolveRef throws on a repo with no commits too; fall back to checking for
    // the .git dir via findRoot.
    try {
      const root = await git.findRoot({ fs: o.fs, filepath: o.dir });
      return !!root;
    } catch {
      return false;
    }
  }
}

/** Files that differ from HEAD (working tree vs last commit). Excludes
 *  unmodified files. Sorted by path. */
export async function changedFiles(o: SandboxGitOptions): Promise<ChangedFile[]> {
  const matrix = await git.statusMatrix({ fs: o.fs, dir: o.dir });
  const out: ChangedFile[] = [];
  for (const [filepath, head, workdir] of matrix) {
    // [head, workdir]: 0=absent, 1=present-unchanged, 2=present-changed.
    let status: FileStatus;
    if (head === 0 && workdir !== 0) status = "added";
    else if (head === 1 && workdir === 0) status = "deleted";
    else if (head === 1 && workdir === 2) status = "modified";
    else status = "unmodified";
    if (status !== "unmodified") out.push({ path: filepath, status });
  }
  out.sort((a, b) => a.path.localeCompare(b.path));
  return out;
}

/** Stage every change (adds, modifications, AND deletions) and commit. Returns
 *  the new commit oid, or null when there was nothing to commit (so callers can
 *  skip empty checkpoints). */
export async function commitAll(
  o: SandboxGitOptions,
  message: string,
  author: { name: string; email: string } = DEFAULT_GIT_AUTHOR,
): Promise<string | null> {
  const changes = await changedFiles(o);
  if (changes.length === 0) return null;
  for (const ch of changes) {
    if (ch.status === "deleted") {
      await git.remove({ fs: o.fs, dir: o.dir, filepath: ch.path });
    } else {
      await git.add({ fs: o.fs, dir: o.dir, filepath: ch.path });
    }
  }
  return git.commit({ fs: o.fs, dir: o.dir, message, author });
}

/** The current HEAD commit oid, or null when the repo has no commits yet. */
export async function headOid(o: SandboxGitOptions): Promise<string | null> {
  try {
    return await git.resolveRef({ fs: o.fs, dir: o.dir, ref: "HEAD" });
  } catch {
    return null;
  }
}

/** Recent commits, newest first. */
export async function log(o: SandboxGitOptions, depth = 30): Promise<CommitEntry[]> {
  let entries: any[];
  try {
    entries = await git.log({ fs: o.fs, dir: o.dir, depth });
  } catch {
    return []; // no commits yet
  }
  return entries.map((e) => ({
    oid: e.oid,
    message: (e.commit.message ?? "").trim(),
    timestamp: e.commit.author?.timestamp ?? 0,
  }));
}

/** Restore the working tree to a commit (the "Revert this run" action).
 *
 *  We do NOT rely on `git.checkout({force:true})` for this: isomorphic-git's
 *  forced checkout restores added/deleted files but has a long-standing bug
 *  where it does not overwrite the *contents* of a modified tracked file — so a
 *  "revert" would silently leave edited files unchanged. Instead we rewrite each
 *  file from the target commit's blob and delete tracked files absent from it,
 *  using `o.fs.promises` (satisfied by both Node fs and the expo adapter), then
 *  let checkout fix up HEAD/index bookkeeping. */
export async function revertTo(o: SandboxGitOptions, oid: string): Promise<void> {
  const p = o.fs.promises;
  const targetFiles = await git.listFiles({ fs: o.fs, dir: o.dir, ref: oid });
  const targetSet = new Set<string>(targetFiles);

  // Rewrite every file the target commit contains, from its blob.
  for (const filepath of targetFiles) {
    const { blob } = await git.readBlob({ fs: o.fs, dir: o.dir, oid, filepath });
    const full = joinPosix(o.dir, filepath);
    const parent = dirnamePosix(full);
    if (parent) await p.mkdir(parent, { recursive: true }).catch(() => {});
    await p.writeFile(full, blob);
  }

  // Delete currently-tracked files that the target doesn't have.
  let tracked: string[] = [];
  try {
    tracked = await git.listFiles({ fs: o.fs, dir: o.dir }); // index (HEAD)
  } catch {
    tracked = [];
  }
  for (const filepath of tracked) {
    if (!targetSet.has(filepath)) {
      await p.unlink(joinPosix(o.dir, filepath)).catch(() => {});
    }
  }

  // Point HEAD + index at the target so subsequent status reports clean.
  await git.checkout({ fs: o.fs, dir: o.dir, ref: oid, force: true });
}

function joinPosix(a: string, b: string): string {
  if (!a) return b;
  return a.endsWith("/") ? a + b : a + "/" + b;
}

function dirnamePosix(full: string): string {
  const i = full.lastIndexOf("/");
  return i <= 0 ? "" : full.slice(0, i);
}

/**
 * Wrap an agentic run in a before/after checkpoint. Returns the two commit oids
 * so the UI can offer a one-tap revert to `before`. `before` is null when the
 * tree was already clean (nothing to snapshot); `after` is null when the run
 * changed nothing.
 */
export interface RunCheckpoint {
  before: string | null;
  after: string | null;
  changed: ChangedFile[];
}

export async function checkpointBefore(o: SandboxGitOptions, label: string): Promise<string | null> {
  await ensureRepo(o);
  // Snapshot whatever's currently on disk so there's a clean restore point,
  // even if the user hand-edited since the last checkpoint.
  return commitAll(o, `checkpoint: before ${label}`);
}

export async function checkpointAfter(o: SandboxGitOptions, label: string): Promise<RunCheckpoint["after"]> {
  return commitAll(o, `agent: ${label}`);
}
