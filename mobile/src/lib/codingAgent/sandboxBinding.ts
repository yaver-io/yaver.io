// codingAgent/sandboxBinding.ts — production RN wiring for the agentic coding
// loop. Importing this pulls in the expo-backed source store, so mobile-headless
// tests must NOT import it — they construct an in-memory CodingSandbox directly.
//
// Two jobs:
//   1. sandboxForSlug — adapt the slug-keyed phoneSandboxSource into the
//      slug-scoped CodingSandbox the tools expect (path-safety + atomic writes
//      stay in phoneSandboxSource; we only re-shape the call signatures).
//   2. loadCodingConfig — resolve the config the loop should use: MANAGED
//      (Yaver Gateway, wallet-metered) when Premium managed mode is on, else
//      the BYO GLM key. loadGlmCodingConfig / loadManagedCodingConfig are the
//      two underlying builders.

import {
  LOCAL_KEYS,
  getLocalSecret,
  getToken,
  getManagedCodingEnabled,
} from "../auth";
import { getGatewayUrlSync } from "../backendConfig";
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

/** Build the MANAGED coding config: route the agentic loop through the Yaver
 *  Gateway (captive OpenRouter) authed by the user's SESSION TOKEN — no model
 *  key on the device; Yaver holds the upstream key and meters tokens into the
 *  prepaid wallet. Returns null when there's no token or no gateway origin yet
 *  (managed mode then falls back to BYO). The gateway is OpenAI-compatible at
 *  <origin>/v1/chat/completions; the runner appends /chat/completions, and
 *  model "auto" lets the gateway pick the cheapest-capable upstream — the user
 *  never chooses a model. */
export async function loadManagedCodingConfig(): Promise<CodingAgentConfig | null> {
  const token = (await getToken())?.trim();
  if (!token) return null;
  const override = (await getLocalSecret(LOCAL_KEYS.gatewayUrl))?.trim();
  const origin = (override || getGatewayUrlSync()).replace(/\/+$/, "");
  if (!origin) return null;
  return { provider: "glm", model: "auto", apiKey: token, baseUrl: `${origin}/v1` };
}

/** The config the coding loop should use: managed (gateway, wallet-metered)
 *  when Premium managed mode is enabled AND available, else the BYO GLM key,
 *  else null (the UI then prompts the user to add a key / load credit). */
export async function loadCodingConfig(): Promise<CodingAgentConfig | null> {
  if (await getManagedCodingEnabled()) {
    const managed = await loadManagedCodingConfig();
    if (managed) return managed;
  }
  return loadGlmCodingConfig();
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
