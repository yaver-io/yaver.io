// gitProviderStore.ts — RN IO shell for git provider credentials (SecureStore via
// auth.ts). GitHub reuses the existing LOCAL_KEYS.githubToken slot (so the
// parallel session's githubAuthStore stays consistent); GitLab/Bitbucket/generic
// add their own slots. Pure auth logic lives in gitProviderAuth.ts.
//
// `gitNetFromStore(url, http)` is the one call the agent + UI use to turn a
// remote URL into NetOptions for sandboxGitOps push/pull/clone — entirely
// on-device, no dev box.

import { LOCAL_KEYS, getLocalSecret, saveLocalSecret, deleteLocalSecret } from "./auth";
import {
  detectGitProvider,
  gitNetOptions,
  type GitProvider,
} from "./gitProviderAuth";
import type { NetOptions } from "./codingAgent/sandboxGitOps";

const SLOT: Record<Exclude<GitProvider, "generic">, string> = {
  github: LOCAL_KEYS.githubToken,
  gitlab: LOCAL_KEYS.gitlabToken,
  bitbucket: LOCAL_KEYS.bitbucketToken,
};

export interface GenericGitConfig {
  /** Host substring used to match remote URLs, e.g. "git.mycorp.io". */
  host: string;
  username?: string;
  token: string;
}

// ── Per-provider tokens (github/gitlab/bitbucket) ──────────────────────

export async function saveProviderToken(provider: Exclude<GitProvider, "generic">, token: string): Promise<void> {
  const t = token.trim();
  if (t) await saveLocalSecret(SLOT[provider], t);
  else await deleteLocalSecret(SLOT[provider]);
}

export async function loadProviderToken(provider: Exclude<GitProvider, "generic">): Promise<string | null> {
  const t = (await getLocalSecret(SLOT[provider]))?.trim();
  return t || null;
}

// ── Generic / self-hosted ({host, username, token} as JSON) ────────────

export async function saveGenericGit(cfg: GenericGitConfig | null): Promise<void> {
  if (cfg && cfg.host.trim() && cfg.token.trim()) {
    await saveLocalSecret(
      LOCAL_KEYS.gitGenericConfig,
      JSON.stringify({ host: cfg.host.trim(), username: cfg.username?.trim() || undefined, token: cfg.token.trim() }),
    );
  } else {
    await deleteLocalSecret(LOCAL_KEYS.gitGenericConfig);
  }
}

export async function loadGenericGit(): Promise<GenericGitConfig | null> {
  const raw = await getLocalSecret(LOCAL_KEYS.gitGenericConfig);
  if (!raw) return null;
  try {
    const o = JSON.parse(raw);
    if (typeof o?.host === "string" && typeof o?.token === "string") return o as GenericGitConfig;
  } catch {
    /* corrupt → treat as unset */
  }
  return null;
}

// ── Status (for the settings screen) ───────────────────────────────────

export interface GitCredStatus {
  github: boolean;
  gitlab: boolean;
  bitbucket: boolean;
  generic: GenericGitConfig | null;
}

export async function loadGitCredStatus(): Promise<GitCredStatus> {
  const [github, gitlab, bitbucket, generic] = await Promise.all([
    loadProviderToken("github"),
    loadProviderToken("gitlab"),
    loadProviderToken("bitbucket"),
    loadGenericGit(),
  ]);
  return { github: !!github, gitlab: !!gitlab, bitbucket: !!bitbucket, generic };
}

// ── Resolve creds for a remote URL → NetOptions ────────────────────────

/** Find the token + username for a remote URL. For generic, the stored host
 *  must appear in the URL. Returns null when nothing matches. */
export async function tokenForUrl(
  url: string,
): Promise<{ provider: GitProvider; token: string; username?: string } | null> {
  const provider = detectGitProvider(url);
  if (provider !== "generic") {
    const token = await loadProviderToken(provider);
    return token ? { provider, token } : await genericFallback(url);
  }
  return genericFallback(url);
}

async function genericFallback(
  url: string,
): Promise<{ provider: GitProvider; token: string; username?: string } | null> {
  const g = await loadGenericGit();
  if (g && g.host && url.toLowerCase().includes(g.host.toLowerCase())) {
    return { provider: "generic", token: g.token, username: g.username };
  }
  return null;
}

/** Build NetOptions for a remote URL from stored creds, or null when none match.
 *  `http` is isomorphic-git's web client; pass it in so this module doesn't drag
 *  that import in until a network op runs. */
export async function gitNetFromStore(url: string, http: unknown, corsProxy?: string): Promise<NetOptions | null> {
  const found = await tokenForUrl(url);
  if (!found) return null;
  return gitNetOptions(found.provider, found.token, http, found.username, corsProxy);
}
