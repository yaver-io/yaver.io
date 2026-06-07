// githubAuthStore.ts — RN IO shell for the GitHub token (SecureStore via
// auth.ts's local-secret helpers). Pure logic (onAuth/url parsing) is in
// githubAuth.ts. Importing this pulls in expo-secure-store; headless tests use
// githubAuth.ts directly.

import { LOCAL_KEYS, getLocalSecret, saveLocalSecret, deleteLocalSecret } from "./auth";
import { gitHubNetOptions } from "./githubAuth";
import type { NetOptions } from "./codingAgent/sandboxGitOps";

export async function saveGitHubToken(token: string): Promise<void> {
  await saveLocalSecret(LOCAL_KEYS.githubToken, token.trim());
}

export async function loadGitHubToken(): Promise<string | null> {
  const t = (await getLocalSecret(LOCAL_KEYS.githubToken))?.trim();
  return t || null;
}

export async function clearGitHubToken(): Promise<void> {
  await deleteLocalSecret(LOCAL_KEYS.githubToken);
}

export async function hasGitHubToken(): Promise<boolean> {
  return (await loadGitHubToken()) != null;
}

/**
 * Build NetOptions for sandboxGitOps push/pull/clone from the stored token, or
 * null when none is set (the UI then prompts the user to add one). `http` is the
 * isomorphic-git web http client — passed in so this module stays free of that
 * import until a network op actually runs.
 */
export async function gitHubNetFromStore(http: unknown, corsProxy?: string): Promise<NetOptions | null> {
  const token = await loadGitHubToken();
  if (!token) return null;
  return gitHubNetOptions(token, http, corsProxy);
}
