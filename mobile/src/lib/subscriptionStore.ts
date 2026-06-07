// subscriptionStore.ts — RN runtime for the Claude subscription transport.
// Stores the mirrored OAuth credential in SecureStore, refreshes it when it
// expires, and performs the /v1/messages call that bills the user's Max/Pro
// PLAN (never the metered API). Pure request/parse logic lives in
// claudeSubscription.ts (tsx-tested); this file is the I/O shell.
//
// How creds get here:
//   1. Desktop mirror — the agent's runner_auth_mirror flow can POST the
//      verbatim ~/.claude/.credentials.json to the phone; importMirrored() persists it.
//   2. Manual paste — a settings screen can call importMirrored(json).
// Either way we only ever accept subscription tokens (sk-ant-oat-*); a metered
// key is rejected by parseClaudeCredentials so the expensive path is impossible.

import * as SecureStore from "expo-secure-store";

import {
  parseClaudeCredentials,
  serializeClaudeCredentials,
  isExpired,
  buildRefreshRequest,
  parseRefreshResponse,
  buildMessagesRequest,
  assertSubscriptionRequest,
  DEFAULT_ENDPOINTS,
  type ClaudeOAuthCreds,
  type ClaudeOAuthEndpoints,
  type MessagesParams,
} from "./claudeSubscription";

const STORE_KEY = "yaver_claude_subscription_v1";

let memo: ClaudeOAuthCreds | null = null;

/** Persist mirrored/pasted credentials. Returns false (and stores nothing) if
 *  the blob is not a subscription-OAuth credential — the guard against metered
 *  keys sneaking in. */
export async function importMirrored(credentialsJson: string): Promise<boolean> {
  const creds = parseClaudeCredentials(credentialsJson);
  if (!creds) return false;
  await SecureStore.setItemAsync(STORE_KEY, serializeClaudeCredentials(creds));
  memo = creds;
  return true;
}

export async function hasSubscription(): Promise<boolean> {
  return (await load()) != null;
}

export async function clearSubscription(): Promise<void> {
  memo = null;
  await SecureStore.deleteItemAsync(STORE_KEY).catch(() => {});
}

async function load(): Promise<ClaudeOAuthCreds | null> {
  if (memo) return memo;
  const raw = await SecureStore.getItemAsync(STORE_KEY).catch(() => null);
  if (!raw) return null;
  memo = parseClaudeCredentials(raw);
  return memo;
}

async function persist(creds: ClaudeOAuthCreds): Promise<void> {
  memo = creds;
  await SecureStore.setItemAsync(STORE_KEY, serializeClaudeCredentials(creds));
}

/** Refresh the access token if it is expired/near-expiry. No-op when fresh.
 *  Throws (with a re-mirror hint) when there is no refresh token. */
async function ensureFresh(
  creds: ClaudeOAuthCreds,
  ep: ClaudeOAuthEndpoints,
): Promise<ClaudeOAuthCreds> {
  if (!isExpired(creds, Date.now())) return creds;
  const req = buildRefreshRequest(creds, ep);
  const resp = await fetch(req.url, req.init);
  if (!resp.ok) {
    throw new Error(`token refresh failed (${resp.status}) — re-mirror from desktop`);
  }
  const updated = parseRefreshResponse(creds, await resp.json(), Date.now());
  await persist(updated);
  return updated;
}

export interface SendOptions extends Omit<MessagesParams, "creds"> {
  endpoints?: ClaudeOAuthEndpoints;
  signal?: AbortSignal;
}

/** Send a Messages request on the subscription. Refreshes proactively, and on a
 *  401 refreshes once and retries (covers a token that expired server-side
 *  before our local clock said so). Returns the parsed JSON response. */
export async function sendClaudeSubscriptionMessage(opts: SendOptions): Promise<any> {
  const ep = opts.endpoints ?? DEFAULT_ENDPOINTS;
  let creds = await load();
  if (!creds) {
    throw new Error("no Claude subscription on this device — mirror it from your desktop");
  }
  creds = await ensureFresh(creds, ep);

  const doCall = async (c: ClaudeOAuthCreds) => {
    const req = buildMessagesRequest({ ...opts, creds: c }, ep);
    assertSubscriptionRequest(req); // hard guard: never bill the metered API
    return fetch(req.url, { ...req.init, signal: opts.signal });
  };

  let resp = await doCall(creds);
  if (resp.status === 401 && creds.refreshToken) {
    // Force a refresh even though our local expiry looked fine, then retry once.
    const refreshed = await ensureFresh({ ...creds, expiresAt: 0 }, ep);
    resp = await doCall(refreshed);
  }
  if (!resp.ok) {
    const text = await resp.text().catch(() => "");
    throw new Error(`Claude subscription call failed (${resp.status}): ${text.slice(0, 300)}`);
  }
  return resp.json();
}
