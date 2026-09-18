// cloneToPhone.ts — clone a GitHub/GitLab repo into a NEW phone-local project, fully on
// device. No remote box: isomorphic-git (over the gitFsExpo adapter) clones into
// <doc>/phone-projects/<slug>/, the exact root the agentic coding loop
// (repoSandboxForSlug) and the git panel (gitContextForSlug) operate on. Auth for
// private repos / push comes from the stored provider token; public repos clone
// without one.
//
// Importing this pulls in expo (via gitFsExpo + phoneProjects), so headless tests
// don't import it — its pure inputs (parseRepoSlug / normalizeRepoUrl) are tested
// in githubAuth.test.

import http from "isomorphic-git/http/web";
import { Buffer } from "buffer";

import { cloneRepo } from "./codingAgent/sandboxGitOps";
import { gitContextForSlug } from "./codingAgent/codingAgentRun";
import { isRepo } from "./codingAgent/sandboxGit";
import { detectGitProvider, normalizeGitUrl, repoLabelFromUrl } from "./gitProviderAuth";
import { gitNetFromStore } from "./gitProviderStore";
import { createLocalPhoneProject, slugifyPhoneProject, type PhoneProject } from "./phoneProjects";
import { deleteLocalPhoneProject, listLocalPhoneProjectsMeta } from "./phoneSandboxLocal";

export interface CloneToPhoneResult {
  slug: string;
  project: PhoneProject;
  url: string;
  provider: "github" | "gitlab" | "bitbucket" | "generic";
  /** True when no provider token was used (public clone). */
  anonymous: boolean;
}

/**
 * Clone `input` (GitHub owner/repo or a full GitHub/GitLab URL) onto this phone.
 *
 * Shallow by default (depth 1) — full history of a real app is large and slow to
 * materialize through the on-device base64 fs, and editing/committing/pushing all
 * work from a depth-1 working tree. Pass depth:0 for a full clone.
 */
export async function cloneGitRepoToPhone(
  input: string,
  opts: { depth?: number; ref?: string } = {},
): Promise<CloneToPhoneResult> {
  // isomorphic-git's smart-HTTP packet parser expects Node's Buffer global.
  // Hermes does not provide it, even though the npm implementation is bundled.
  // Install the scoped polyfill at the network boundary before the first clone.
  if (!(globalThis as any).Buffer) (globalThis as any).Buffer = Buffer;
  const url = normalizeGitUrl(input);
  const provider = detectGitProvider(url);
  if (provider !== "github" && provider !== "gitlab") {
    throw new Error(`Unsupported repository provider for "${input}". Use GitHub or GitLab.`);
  }
  const label = repoLabelFromUrl(url);
  const repoName = label.split("/").filter(Boolean).pop() || "repo";
  const requestedSlug = slugifyPhoneProject(repoName);
  const git = gitContextForSlug(requestedSlug);

  const existing = await listLocalPhoneProjectsMeta();
  if (existing.some((project) => project.slug === requestedSlug) || await isRepo(git)) {
    throw new Error(`A project named "${repoName}" already exists on this phone. Open it from Projects instead.`);
  }

  // Register a blank phone project so the repo appears in the project list with a
  // stable slug; the clone fills its tree. (Blank template writes no src/ files,
  // so it won't collide with the cloned tree.)
  const project = await createLocalPhoneProject({ name: repoName, slug: repoName, template: "blank" });
  const slug = project.slug;

  // Ensure the project root exists before clone (isomorphic-git writes .git into it).
  await ensureGitDir(git);

  // Auth: provider-specific token if present (required for private repos +
  // push). Public repositories clone without onAuth.
  const net = (await gitNetFromStore(url, http)) ?? { http };
  const anonymous = !net.onAuth;

  const depth = opts.depth === 0 ? undefined : opts.depth ?? 1;
  try {
    await cloneRepo(git, net, { url, ref: opts.ref, depth });
  } catch (error) {
    // The metadata row and exact sandbox were created solely for this clone.
    // Remove them on failure so a network/auth error cannot leave a phantom
    // project that blocks the next attempt.
    await deleteLocalPhoneProject(slug).catch(() => undefined);
    throw error;
  }

  return { slug, project, url, provider, anonymous };
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
