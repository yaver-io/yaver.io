// codingAgent/sandboxGitOps.ts — the FULL git surface for on-device coding,
// on top of isomorphic-git + the gitFsExpo adapter. sandboxGit.ts covers the
// safety-net basics (init / changedFiles / commitAll / log / revert /
// checkpoints); this adds everything an agent (or the user) needs to actually
// work like git: branches, merge with conflict markers, conflict listing +
// resolution, file diffs, remotes, and push/pull/clone/fetch with auth.
//
// PURE w.r.t. the filesystem (takes the same {fs, dir} as sandboxGit) and pure
// w.r.t. the network (the http client + onAuth are injected), so the local-graph
// ops (branch/merge/conflict/diff) are tsx-tested against REAL isomorphic-git via
// the in-memory backend, and the network ops stay swappable for tests.

import git from "isomorphic-git";

import {
  DEFAULT_GIT_AUTHOR,
  changedFiles,
  checkpointBefore,
  checkpointAfter,
  type ChangedFile,
  type SandboxGitOptions,
} from "./sandboxGit";

export type GitAuthor = { name: string; email: string };

// ── Branches ──────────────────────────────────────────────────────────

export async function currentBranch(o: SandboxGitOptions): Promise<string | null> {
  const b = (await git.currentBranch({ fs: o.fs, dir: o.dir, fullname: false })) as string | undefined;
  return b ?? null;
}

export async function listBranches(o: SandboxGitOptions): Promise<string[]> {
  return git.listBranches({ fs: o.fs, dir: o.dir });
}

/** Create a branch at HEAD. `checkout` switches the working tree to it. */
export async function createBranch(
  o: SandboxGitOptions,
  name: string,
  opts: { checkout?: boolean } = {},
): Promise<void> {
  await git.branch({ fs: o.fs, dir: o.dir, ref: name, checkout: !!opts.checkout });
}

/** Switch the working tree to an existing branch (or any ref). */
export async function switchBranch(o: SandboxGitOptions, name: string): Promise<void> {
  await git.checkout({ fs: o.fs, dir: o.dir, ref: name });
}

export async function deleteBranch(o: SandboxGitOptions, name: string): Promise<void> {
  await git.deleteBranch({ fs: o.fs, dir: o.dir, ref: name });
}

// ── Merge + conflicts ─────────────────────────────────────────────────

export type MergeStatus = "fast-forward" | "merged" | "already-merged" | "conflict";

export interface MergeResult {
  status: MergeStatus;
  /** Resulting commit oid for ff / clean merge; undefined on conflict. */
  oid?: string;
  /** Conflicting file paths (working tree now carries conflict markers). */
  conflicts?: string[];
  /** The merged-in commit oid — pass to completeMerge() as the 2nd parent so the
   *  resolved merge commit has correct history (isomorphic-git doesn't reliably
   *  write MERGE_HEAD on a conflict). */
  theirsOid?: string;
}

/**
 * Merge `theirs` into the current branch. On a conflict isomorphic-git writes
 * conflict markers into the working tree and we return the conflicting paths
 * (status: "conflict"); the caller resolves each via resolveConflict() then
 * completeMerge(). A clean 3-way or fast-forward auto-commits.
 */
export async function mergeBranch(
  o: SandboxGitOptions,
  theirs: string,
  author: GitAuthor = DEFAULT_GIT_AUTHOR,
): Promise<MergeResult> {
  const theirsOid = (await safeResolve(o, theirs)) ?? undefined;
  try {
    const r = await git.merge({
      fs: o.fs,
      dir: o.dir,
      theirs,
      author,
      abortOnConflict: false, // leave markers we can resolve instead of throwing early
    });
    if (r.alreadyMerged) return { status: "already-merged", oid: r.oid, theirsOid };
    if (r.fastForward) return { status: "fast-forward", oid: r.oid, theirsOid };
    return { status: "merged", oid: r.oid, theirsOid };
  } catch (e: any) {
    if (e?.code === "MergeConflictError" || e?.name === "MergeConflictError") {
      // isomorphic-git puts the conflicting filepaths on e.data.
      const conflicts: string[] = Array.isArray(e.data) ? e.data : e.data?.filepaths ?? [];
      return { status: "conflict", conflicts, theirsOid };
    }
    throw e;
  }
}

/** Files with unresolved conflict markers (scans the working tree). */
export async function listConflicts(o: SandboxGitOptions): Promise<string[]> {
  const matrix = await git.statusMatrix({ fs: o.fs, dir: o.dir });
  const out: string[] = [];
  for (const row of matrix) {
    const filepath = row[0] as string;
    try {
      const content = (await o.fs.promises.readFile(`${o.dir}/${filepath}`, { encoding: "utf8" })) as string;
      if (hasConflictMarkers(content)) out.push(filepath);
    } catch {
      // unreadable/binary — skip
    }
  }
  return out.sort();
}

/** Write the resolved content for a conflicted file and stage it. */
export async function resolveConflict(o: SandboxGitOptions, filepath: string, content: string): Promise<void> {
  await o.fs.promises.writeFile(`${o.dir}/${filepath}`, content, { encoding: "utf8" });
  await git.add({ fs: o.fs, dir: o.dir, filepath });
}

/** Commit a resolved merge as a real merge commit (two parents: HEAD + the
 *  merged-in oid). Pass `theirsOid` from the MergeResult; we fall back to
 *  MERGE_HEAD if omitted. Returns the commit oid. */
export async function completeMerge(
  o: SandboxGitOptions,
  message: string,
  opts: { author?: GitAuthor; theirsOid?: string } = {},
): Promise<string> {
  const head = await safeResolve(o, "HEAD");
  const theirs = opts.theirsOid ?? (await safeResolve(o, "MERGE_HEAD")) ?? undefined;
  const parents = [head, theirs].filter((x): x is string => !!x);
  const oid = await git.commit({
    fs: o.fs,
    dir: o.dir,
    message,
    author: opts.author ?? DEFAULT_GIT_AUTHOR,
    parent: parents.length ? parents : undefined,
  });
  await o.fs.promises.unlink(`${o.dir}/.git/MERGE_HEAD`).catch(() => {});
  return oid;
}

export async function abortMerge(o: SandboxGitOptions): Promise<void> {
  await git.abortMerge({ fs: o.fs, dir: o.dir });
}

async function safeResolve(o: SandboxGitOptions, ref: string): Promise<string | null> {
  try {
    return await git.resolveRef({ fs: o.fs, dir: o.dir, ref });
  } catch {
    return null;
  }
}

// ── Conflict-marker helpers (pure) ────────────────────────────────────

const MARK_START = "<<<<<<<";
const MARK_MID = "=======";
const MARK_END = ">>>>>>>";

export function hasConflictMarkers(content: string): boolean {
  return content.includes(MARK_START) && content.includes(MARK_MID) && content.includes(MARK_END);
}

export interface ConflictRegion {
  ours: string;
  theirs: string;
  oursLabel: string;
  theirsLabel: string;
}

/** Parse the `<<<<<<< / ======= / >>>>>>>` regions out of a conflicted file so a
 *  UI (or the agent) can present ours/theirs side by side. */
export function parseConflictRegions(content: string): ConflictRegion[] {
  const lines = content.split("\n");
  const out: ConflictRegion[] = [];
  let i = 0;
  while (i < lines.length) {
    if (lines[i].startsWith(MARK_START)) {
      const oursLabel = lines[i].slice(MARK_START.length).trim();
      const ours: string[] = [];
      const theirs: string[] = [];
      let theirsLabel = "";
      i++;
      while (i < lines.length && !lines[i].startsWith(MARK_MID)) ours.push(lines[i++]);
      i++; // skip =======
      while (i < lines.length && !lines[i].startsWith(MARK_END)) theirs.push(lines[i++]);
      if (i < lines.length) theirsLabel = lines[i].slice(MARK_END.length).trim();
      i++; // skip >>>>>>>
      out.push({ ours: ours.join("\n"), theirs: theirs.join("\n"), oursLabel, theirsLabel });
    } else {
      i++;
    }
  }
  return out;
}

/** Resolve every region by choosing one side — the simplest agentic resolution.
 *  `pick` is "ours" | "theirs"; for finer control, edit + resolveConflict directly. */
export function resolveAllRegions(content: string, pick: "ours" | "theirs"): string {
  const lines = content.split("\n");
  const out: string[] = [];
  let i = 0;
  while (i < lines.length) {
    if (lines[i].startsWith(MARK_START)) {
      const ours: string[] = [];
      const theirs: string[] = [];
      i++;
      while (i < lines.length && !lines[i].startsWith(MARK_MID)) ours.push(lines[i++]);
      i++;
      while (i < lines.length && !lines[i].startsWith(MARK_END)) theirs.push(lines[i++]);
      i++;
      out.push(...(pick === "ours" ? ours : theirs));
    } else {
      out.push(lines[i++]);
    }
  }
  return out.join("\n");
}

// ── Agentic run safety net ────────────────────────────────────────────

export interface CheckpointedRun<T> {
  result: T;
  /** Commit oid snapshotting the tree BEFORE the run (revert target), or null
   *  if the tree was already clean. */
  before: string | null;
  /** Commit oid capturing the run's output, or null if it changed nothing. */
  after: string | null;
  /** What the run changed (captured before the after-commit). */
  changed: ChangedFile[];
}

/**
 * Bracket an agentic run in before/after git checkpoints so any autonomous edit
 * is one tap from revert. `before` is committed first (clean restore point),
 * then `run()` does its work (which may itself call git tools), then we record
 * what changed and commit the `after` snapshot. The before-commit is taken even
 * if run() throws, so a crashed run is still recoverable.
 */
export async function runWithCheckpoints<T>(
  o: SandboxGitOptions,
  label: string,
  run: () => Promise<T>,
): Promise<CheckpointedRun<T>> {
  const before = await checkpointBefore(o, label);
  let result: T;
  try {
    result = await run();
  } catch (e) {
    // before-checkpoint already captured a restore point; surface the failure.
    throw e;
  }
  const changed = await changedFiles(o);
  const after = await checkpointAfter(o, label);
  return { result, before, after, changed };
}

// ── Diff ──────────────────────────────────────────────────────────────

export interface FileDiff {
  path: string;
  status: "added" | "modified" | "deleted";
}

/** Working-tree changes vs a ref (default HEAD), file-level. */
export async function diffStatus(o: SandboxGitOptions): Promise<FileDiff[]> {
  const matrix = await git.statusMatrix({ fs: o.fs, dir: o.dir });
  const out: FileDiff[] = [];
  for (const [filepath, head, workdir] of matrix as Array<[string, number, number, number]>) {
    if (head === 1 && workdir === 1) continue; // unmodified
    if (head === 0 && workdir !== 0) out.push({ path: filepath, status: "added" });
    else if (head === 1 && workdir === 0) out.push({ path: filepath, status: "deleted" });
    else if (head === 1 && workdir === 2) out.push({ path: filepath, status: "modified" });
  }
  return out.sort((a, b) => a.path.localeCompare(b.path));
}

export interface LineDiff {
  /** "+" added, "-" removed, " " context. */
  op: "+" | "-" | " ";
  text: string;
}

/** Line-level diff of a file: its committed (HEAD) version vs the working tree.
 *  A compact LCS diff — enough for an agent to reason about or a UI to render. */
export async function fileLineDiff(o: SandboxGitOptions, filepath: string, ref = "HEAD"): Promise<LineDiff[]> {
  const oldText = await readBlobAt(o, ref, filepath);
  const newText = await readWorking(o, filepath);
  return lineDiff(oldText ?? "", newText ?? "");
}

async function readWorking(o: SandboxGitOptions, filepath: string): Promise<string | null> {
  try {
    return (await o.fs.promises.readFile(`${o.dir}/${filepath}`, { encoding: "utf8" })) as string;
  } catch {
    return null;
  }
}

async function readBlobAt(o: SandboxGitOptions, ref: string, filepath: string): Promise<string | null> {
  try {
    const oid = await git.resolveRef({ fs: o.fs, dir: o.dir, ref });
    const { blob } = await git.readBlob({ fs: o.fs, dir: o.dir, oid, filepath });
    return new TextDecoder().decode(blob);
  } catch {
    return null;
  }
}

/** Minimal LCS line diff. */
export function lineDiff(a: string, b: string): LineDiff[] {
  const A = a.length ? a.split("\n") : [];
  const B = b.length ? b.split("\n") : [];
  const n = A.length;
  const m = B.length;
  // LCS table.
  const lcs: number[][] = Array.from({ length: n + 1 }, () => new Array(m + 1).fill(0));
  for (let i = n - 1; i >= 0; i--) {
    for (let j = m - 1; j >= 0; j--) {
      lcs[i][j] = A[i] === B[j] ? lcs[i + 1][j + 1] + 1 : Math.max(lcs[i + 1][j], lcs[i][j + 1]);
    }
  }
  const out: LineDiff[] = [];
  let i = 0;
  let j = 0;
  while (i < n && j < m) {
    if (A[i] === B[j]) {
      out.push({ op: " ", text: A[i] });
      i++;
      j++;
    } else if (lcs[i + 1][j] >= lcs[i][j + 1]) {
      out.push({ op: "-", text: A[i++] });
    } else {
      out.push({ op: "+", text: B[j++] });
    }
  }
  while (i < n) out.push({ op: "-", text: A[i++] });
  while (j < m) out.push({ op: "+", text: B[j++] });
  return out;
}

// ── Remotes + network (http + onAuth injected) ────────────────────────

export type GitHttp = any; // isomorphic-git/http/web (or /node in tests)
export type OnAuth = (url: string) => { username?: string; password?: string } | void;

export interface NetOptions {
  http: GitHttp;
  onAuth?: OnAuth;
  corsProxy?: string;
}

export async function addRemote(o: SandboxGitOptions, remote: string, url: string): Promise<void> {
  await git.addRemote({ fs: o.fs, dir: o.dir, remote, url, force: true });
}

export async function listRemotes(o: SandboxGitOptions): Promise<Array<{ remote: string; url: string }>> {
  return git.listRemotes({ fs: o.fs, dir: o.dir });
}

export interface PushResult {
  ok: boolean;
  error?: string;
}

export async function push(
  o: SandboxGitOptions,
  net: NetOptions,
  opts: { remote?: string; ref?: string; force?: boolean } = {},
): Promise<PushResult> {
  try {
    const res: any = await git.push({
      fs: o.fs,
      http: net.http,
      dir: o.dir,
      remote: opts.remote ?? "origin",
      ref: opts.ref,
      force: opts.force,
      onAuth: net.onAuth,
      corsProxy: net.corsProxy,
    });
    // isomorphic-git's push result reports per-ref errors in `errors`.
    if (res?.ok === false || (Array.isArray(res?.errors) && res.errors.length)) {
      return { ok: false, error: (res?.errors ?? ["push rejected"]).join("; ") };
    }
    return { ok: true };
  } catch (e: any) {
    return { ok: false, error: String(e?.message ?? e) };
  }
}

export async function pull(
  o: SandboxGitOptions,
  net: NetOptions,
  opts: { remote?: string; ref?: string; author?: GitAuthor } = {},
): Promise<void> {
  await git.pull({
    fs: o.fs,
    http: net.http,
    dir: o.dir,
    remote: opts.remote ?? "origin",
    ref: opts.ref,
    author: opts.author ?? DEFAULT_GIT_AUTHOR,
    onAuth: net.onAuth,
    corsProxy: net.corsProxy,
    singleBranch: true,
  });
}

export async function fetchRemote(
  o: SandboxGitOptions,
  net: NetOptions,
  opts: { remote?: string } = {},
): Promise<void> {
  await git.fetch({
    fs: o.fs,
    http: net.http,
    dir: o.dir,
    remote: opts.remote ?? "origin",
    onAuth: net.onAuth,
    corsProxy: net.corsProxy,
    singleBranch: true,
  });
}

export async function cloneRepo(
  o: SandboxGitOptions,
  net: NetOptions,
  opts: { url: string; ref?: string; depth?: number },
): Promise<void> {
  await git.clone({
    fs: o.fs,
    http: net.http,
    dir: o.dir,
    url: opts.url,
    ref: opts.ref,
    depth: opts.depth,
    singleBranch: true,
    onAuth: net.onAuth,
    corsProxy: net.corsProxy,
  });
}
