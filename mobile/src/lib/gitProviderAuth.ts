// gitProviderAuth.ts — PURE multi-provider git auth (GitHub / GitLab / Bitbucket
// / generic self-hosted). Builds the isomorphic-git `onAuth` callback + NetOptions
// and normalizes repo URLs, so phone-local sandbox repos can push/pull/clone
// DIRECTLY FROM THE PHONE (no dev box) over HTTPS. No RN/expo imports → tsx-tested.
//
// GitHub behaviour is delegated to githubAuth.ts so it stays byte-identical to
// what the parallel session shipped; this module adds GitLab/Bitbucket/generic
// and a single "by URL" entry point the store + UI use.
//
// Token storage is in gitProviderStore.ts (the IO shell).

import type { NetOptions, OnAuth } from "./codingAgent/sandboxGitOps";
import { makeGitHubOnAuth, normalizeRepoUrl as normalizeGitHubUrl } from "./githubAuth";

export type GitProvider = "github" | "gitlab" | "bitbucket" | "generic";

export const GIT_PROVIDERS: ReadonlyArray<{ id: GitProvider; label: string; tokenHint: string; example: string }> = [
  { id: "github", label: "GitHub", tokenHint: "Personal access token (ghp_… / github_pat_…)", example: "owner/repo or https://github.com/owner/repo.git" },
  { id: "gitlab", label: "GitLab", tokenHint: "Personal/project access token (glpat-…)", example: "https://gitlab.com/group/repo.git" },
  { id: "bitbucket", label: "Bitbucket", tokenHint: "App password / access token", example: "https://bitbucket.org/workspace/repo.git" },
  { id: "generic", label: "Self-hosted", tokenHint: "Token (+ host & username)", example: "https://git.mycorp.io/team/repo.git" },
] as const;

/** Detect the provider family from a clone/remote URL. Self-hosted GitLab/Gitea
 *  commonly contain "gitlab"/"gitea"; everything unrecognised is "generic". */
export function detectGitProvider(url: string): GitProvider {
  const u = (url || "").toLowerCase();
  if (u.includes("github.com") || u.includes("github.")) return "github";
  if (u.includes("gitlab") || u.includes("gitea")) return "gitlab";
  if (u.includes("bitbucket")) return "bitbucket";
  return "generic";
}

/**
 * Build the isomorphic-git auth callback for a token against a provider. PAT
 * basic-auth pairing differs per host:
 *   - GitHub:    username = token, password "x-oauth-basic"  (via githubAuth)
 *   - GitLab:    username "oauth2",       password = token
 *   - Bitbucket: username "x-token-auth", password = token
 *   - generic:   username (or token),     password = token
 * An explicit `username` always wins (self-hosted basic auth, deploy tokens).
 */
export function makeGitOnAuth(provider: GitProvider, token: string, username?: string): OnAuth {
  const t = (token || "").trim();
  if (username) {
    const u = username.trim();
    return () => ({ username: u, password: t });
  }
  switch (provider) {
    case "github":
      return makeGitHubOnAuth(t);
    case "gitlab":
      return () => ({ username: "oauth2", password: t });
    case "bitbucket":
      return () => ({ username: "x-token-auth", password: t });
    default:
      return () => ({ username: t, password: t });
  }
}

/** onAuth that picks the provider from the URL it's called with. Handy when one
 *  repo's remote host isn't known until isomorphic-git invokes the callback. */
export function makeOnAuthForUrl(token: string, username?: string): OnAuth {
  const t = (token || "").trim();
  return (url: string) => makeGitOnAuth(detectGitProvider(url), t, username)(url);
}

/** NetOptions for sandboxGitOps.push/pull/clone/fetch. `http` is isomorphic-git's
 *  web http client (`import http from "isomorphic-git/http/web"`). */
export function gitNetOptions(
  provider: GitProvider,
  token: string,
  http: unknown,
  username?: string,
  corsProxy?: string,
): NetOptions {
  return { http: http as any, onAuth: makeGitOnAuth(provider, token, username), corsProxy };
}

/** Normalize a repo reference to an https clone/push URL.
 *   - GitHub: "owner/repo" → https://github.com/owner/repo.git (via githubAuth).
 *   - Otherwise: require a full URL; normalize the scheme + .git suffix. */
export function normalizeGitUrl(input: string, provider?: GitProvider): string {
  const s = (input || "").trim();
  if (!s) throw new Error("empty git url");
  // Bare "owner/repo" (no scheme/host) is GitHub shorthand by convention.
  const bareOwnerRepo = !s.includes("://") && !s.includes("@") && /^[^/]+\/[^/]+$/.test(s.replace(/\.git$/i, ""));
  if (bareOwnerRepo && (provider ?? "github") === "github") {
    return normalizeGitHubUrl(s);
  }
  // git@host:owner/repo(.git) → https://host/owner/repo.git
  const ssh = s.match(/^git@([^:]+):(.+?)(?:\.git)?$/i);
  if (ssh) return `https://${ssh[1]}/${ssh[2]}.git`;
  if (!/^https?:\/\//i.test(s)) throw new Error(`not a full git url: ${input}`);
  return s.replace(/\.git$/i, "") + ".git";
}

/** Owner/group + repo name from a URL, for display ("group/repo"). */
export function repoLabelFromUrl(url: string): string {
  const m = (url || "").match(/[/:]([^/]+\/[^/]+?)(?:\.git)?$/);
  return m ? m[1] : url;
}
