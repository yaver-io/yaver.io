// cloneToPhone.ts — clone a GitHub repo into a NEW phone-local project, fully on
// device. No remote box: isomorphic-git (over the gitFsExpo adapter) clones into
// <doc>/phone-projects/<slug>/, the exact root the agentic coding loop
// (repoSandboxForSlug) and the git panel (gitContextForSlug) operate on. Auth for
// private repos / push comes from the stored GitHub token; public repos clone
// without one.
//
// Importing this pulls in expo (via gitFsExpo + phoneProjects), so headless tests
// don't import it — its pure inputs (parseRepoSlug / normalizeRepoUrl) are tested
// in githubAuth.test.

import http from "isomorphic-git/http/web";

import { cloneRepo } from "./codingAgent/sandboxGitOps";
import { gitContextForSlug } from "./codingAgent/codingAgentRun";
import { isRepo } from "./codingAgent/sandboxGit";
import { gitHubNetFromStore } from "./githubAuthStore";
import { normalizeRepoUrl, parseRepoSlug } from "./githubAuth";
import { createLocalPhoneProject, type PhoneProject } from "./phoneProjects";

export interface CloneToPhoneResult {
  slug: string;
  project: PhoneProject;
  url: string;
  /** True when no GitHub token was used (public clone). */
  anonymous: boolean;
}

/**
 * Clone `input` (owner/repo or a github.com URL) onto this phone.
 *
 * Shallow by default (depth 1) — full history of a real app is large and slow to
 * materialize through the on-device base64 fs, and editing/committing/pushing all
 * work from a depth-1 working tree. Pass depth:0 for a full clone.
 */
export async function cloneGitRepoToPhone(
  input: string,
  opts: { depth?: number; ref?: string } = {},
): Promise<CloneToPhoneResult> {
  const parsed = parseRepoSlug(input);
  if (!parsed) {
    throw new Error(`Not a GitHub repo: "${input}". Use owner/repo or a github.com URL.`);
  }
  const url = normalizeRepoUrl(input);

  // Register a blank phone project so the repo appears in the project list with a
  // stable slug; the clone fills its tree. (Blank template writes no src/ files,
  // so it won't collide with the cloned tree.)
  const project = await createLocalPhoneProject({ name: parsed.repo, slug: parsed.repo, template: "blank" });
  const slug = project.slug;
  const git = gitContextForSlug(slug);

  // Refuse to clone over an existing repo — re-cloning into a populated .git is a
  // git error and would clobber local work. Caller surfaces a friendly message.
  if (await isRepo(git)) {
    throw new Error(`A project named "${slug}" already has a git repo. Delete it first, or pick another.`);
  }

  // Ensure the project root exists before clone (isomorphic-git writes .git into it).
  await ensureGitDir(git);

  // Auth: stored token if present (required for private repos + push). Public
  // repos clone with no onAuth.
  const net = (await gitHubNetFromStore(http)) ?? { http };
  const anonymous = !net.onAuth;

  const depth = opts.depth === 0 ? undefined : opts.depth ?? 1;
  await cloneRepo(git, net, { url, ref: opts.ref, depth });

  return { slug, project, url, anonymous };
}

/** mkdir -p the repo root through the gitFs (non-recursive mkdir, EEXIST-safe). */
async function ensureGitDir(git: { fs: any; dir: string }): Promise<void> {
  const parts = git.dir.split("/").filter(Boolean);
  let cur = "";
  for (const p of parts) {
    cur += "/" + p;
    try {
      await git.fs.promises.mkdir(cur);
    } catch (e: any) {
      if (e?.code !== "EEXIST") throw e;
    }
  }
}
