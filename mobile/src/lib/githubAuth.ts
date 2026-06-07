// githubAuth.ts — PURE helpers for pushing/cloning phone-local sandbox repos to
// GitHub. Builds the isomorphic-git `onAuth` callback + a NetOptions, and
// normalizes repo references. No RN/expo imports → tsx-tested. Token storage
// lives in githubAuthStore.ts (the IO shell).

import type { NetOptions, OnAuth } from "./codingAgent/sandboxGitOps";

/** Build the isomorphic-git auth callback for a GitHub token (classic/fine-
 *  grained PAT or an OAuth access token). GitHub accepts the token as the basic-
 *  auth username; the password is a conventional placeholder. */
export function makeGitHubOnAuth(token: string): OnAuth {
  const t = token.trim();
  return () => ({ username: t, password: "x-oauth-basic" });
}

/** NetOptions for sandboxGitOps.push/pull/clone. `http` is isomorphic-git's web
 *  http client (import http from "isomorphic-git/http/web"). */
export function gitHubNetOptions(token: string, http: unknown, corsProxy?: string): NetOptions {
  return { http: http as any, onAuth: makeGitHubOnAuth(token), corsProxy };
}

/** Parse "owner/repo", "owner/repo.git", or a full GitHub URL into {owner, repo}.
 *  Returns null when it isn't a recognizable GitHub repo reference. */
export function parseRepoSlug(input: string): { owner: string; repo: string } | null {
  const s = input.trim();
  if (!s) return null;
  // Full URL forms: https://github.com/owner/repo(.git), git@github.com:owner/repo(.git)
  const urlMatch = s.match(/github\.com[/:]([^/]+)\/([^/]+?)(?:\.git)?$/i);
  if (urlMatch) return { owner: urlMatch[1], repo: urlMatch[2] };
  // Bare "owner/repo".
  const slug = s.replace(/\.git$/i, "");
  const parts = slug.split("/").filter(Boolean);
  if (parts.length === 2 && !slug.includes("://")) return { owner: parts[0], repo: parts[1] };
  return null;
}

/** Canonical https clone/push URL for a repo reference. Throws on a bad ref. */
export function normalizeRepoUrl(input: string): string {
  const parsed = parseRepoSlug(input);
  if (!parsed) throw new Error(`not a GitHub repo: ${input}`);
  return `https://github.com/${parsed.owner}/${parsed.repo}.git`;
}

/** True for a plausible GitHub token shape (classic ghp_, fine-grained
 *  github_pat_, OAuth gho_, or a 40-hex classic). Loose by design — the real
 *  check is the push succeeding. */
export function looksLikeGitHubToken(token: string): boolean {
  const t = token.trim();
  return (
    /^gh[pousr]_[A-Za-z0-9]{20,}$/.test(t) ||
    /^github_pat_[A-Za-z0-9_]{20,}$/.test(t) ||
    /^[a-f0-9]{40}$/i.test(t)
  );
}
