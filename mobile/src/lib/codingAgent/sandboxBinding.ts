// codingAgent/sandboxBinding.ts — production RN wiring for the agentic coding
// loop. Importing this pulls in the expo-backed source store, so mobile-headless
// tests must NOT import it — they construct an in-memory CodingSandbox directly.
//
// Two jobs:
//   1. sandboxForSlug — adapt the slug-keyed phoneSandboxSource into the
//      slug-scoped CodingSandbox the tools expect (path-safety + atomic writes
//      stay in phoneSandboxSource; we only re-shape the call signatures).
//   2. loadGlmCodingConfig — read the BYO GLM key from SecureStore and build the
//      GLM-default config. The cheap standalone path from the design doc.

import { LOCAL_KEYS, getLocalSecret } from "../auth";
import {
  readSourceFile,
  writeSourceFile,
  deleteSourceFile,
  listSourceFiles,
} from "../phoneSandboxSourceDefault";
import type { CodingSandbox } from "./sandboxTools";
import { defaultCodingAgentConfig, type CodingAgentConfig } from "./runner";
import { createExpoGitFs, gitDirForSlug } from "./gitFsExpo";
import { ensureRepo, type SandboxGitOptions } from "./sandboxGit";
import { addRemote, listRemotes, type NetOptions } from "./sandboxGitOps";
import { gitNetFromStore } from "../gitProviderStore";

/** Bind the global source store to ONE project slug, exposing exactly the
 *  capability surface the coding tools need. The slug is closed over here so
 *  tool args can never name another project. */
export function sandboxForSlug(slug: string): CodingSandbox {
  return {
    readFile: (path) => readSourceFile(slug, path),
    listFiles: () => listSourceFiles(slug),
    writeFile: (path, content) => writeSourceFile(slug, path, content),
    deleteFile: (path) => deleteSourceFile(slug, path),
  };
}

/** Build the GLM coding config from the stored BYO key, or null when no key is
 *  set (the UI then prompts the user to add one — same slot the single-shot GLM
 *  backend uses, so one key powers both paths). */
export async function loadGlmCodingConfig(): Promise<CodingAgentConfig | null> {
  const key = (await getLocalSecret(LOCAL_KEYS.glmApiKey))?.trim();
  return key ? defaultCodingAgentConfig(key) : null;
}

/** Git context ({fs, dir}) for a slug's on-device repo — the expo-backed
 *  isomorphic-git fs (gitFsExpo) rooted at the document directory, pointed at the
 *  project root. Feeds sandboxGit checkpoints/revert and makeGitTools. */
export function gitForSlug(slug: string): SandboxGitOptions {
  return { fs: createExpoGitFs(), dir: gitDirForSlug(slug) };
}

/** The isomorphic-git web http client (fetch-based; runs in Hermes). Lazily
 *  required so non-network code paths don't pull it in. Used for all on-device
 *  GitHub/GitLab/Bitbucket clone/push/pull — no dev box involved. */
function webGitHttp(): unknown {
  // eslint-disable-next-line @typescript-eslint/no-var-requires
  return require("isomorphic-git/http/web");
}

/** The configured "origin" URL for a slug's repo, or null when no remote set. */
export async function getSlugRemoteUrl(slug: string): Promise<string | null> {
  try {
    const remotes = await listRemotes(gitForSlug(slug));
    return remotes.find((r) => r.remote === "origin")?.url ?? remotes[0]?.url ?? null;
  } catch {
    return null;
  }
}

/** Point a slug's repo at a remote (creates the repo if needed). */
export async function setSlugRemote(slug: string, url: string): Promise<void> {
  const git = gitForSlug(slug);
  await ensureRepo(git);
  await addRemote(git, "origin", url);
}

/** NetOptions (http + per-host PAT auth) for a slug's remote, resolved from the
 *  stored git credentials — or null when no remote is set or no matching token
 *  is saved. Drives sandboxGitOps push/pull/fetch and enables the agent's
 *  git_push tool. */
export async function gitNetForSlug(slug: string): Promise<NetOptions | null> {
  const url = await getSlugRemoteUrl(slug);
  if (!url) return null;
  return gitNetFromStore(url, webGitHttp());
}
