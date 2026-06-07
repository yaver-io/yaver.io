// claudeSubscription.ts — PURE + RN-free (tsx-tested) core for talking to
// Claude on the user's SUBSCRIPTION (Claude Max / Pro) via OAuth, exactly like
// the `claude` CLI does — NOT the metered `api.anthropic.com` API key.
//
// WHY THIS EXISTS (the cost reason):
//   On iOS we cannot run the claude/codex binary (no exec, no JIT → no Node).
//   The fallback is a Hermes-native agent loop. If that loop called the metered
//   Messages API with an `x-api-key`, every on-phone edit would bill per-token
//   on TOP of the flat Max/Pro plan the user already pays for. That is exactly
//   what the user said NOT to do. Instead we reuse the SAME OAuth access token
//   the desktop `claude` CLI uses (mirrored device-to-device via
//   runner_auth_mirror.go), so on-phone Claude draws from the subscription, at
//   no marginal cost.
//
// CREDENTIAL SHAPE (verbatim from ~/.claude/.credentials.json, confirmed in
// desktop/agent/runner_auth_mirror.go):
//   { "claudeAiOauth": { "accessToken": "sk-ant-oat-...",
//                        "refreshToken": "sk-ant-ort-...",
//                        "expiresAt": <ms-since-epoch>,
//                        "scopes": ["user:inference", ...] } }
//
// TRANSPORT DIFFERENCE vs metered (llmAnthropic.ts):
//   metered:      header  x-api-key: sk-ant-api-...
//   subscription: header  Authorization: Bearer sk-ant-oat-...
//                 header  anthropic-beta: oauth-2025-04-20
//                 system  MUST lead with the Claude Code identity block, or
//                         Anthropic rejects the OAuth token (it is scoped to
//                         the Claude Code client, not arbitrary callers).
//
// This module is pure: no fetch, no SecureStore, no RN. It builds the requests
// and parses the responses; subscriptionStore.ts performs the I/O.

/** Public Claude Code OAuth parameters. These are NOT secrets — they ship in
 *  the open-source claude CLI. Re-verify against the current claude-code
 *  release if refresh ever 400s (Anthropic may rotate the beta tag). All
 *  overridable via ClaudeOAuthEndpoints so we never hardcode a moving target
 *  into call sites. */
export const CLAUDE_CODE_OAUTH_CLIENT_ID = "9d1c250a-e61b-44d9-88ed-5944d1962f5e";
export const CLAUDE_OAUTH_TOKEN_URL = "https://console.anthropic.com/v1/oauth/token";
export const ANTHROPIC_MESSAGES_URL = "https://api.anthropic.com/v1/messages";
export const ANTHROPIC_OAUTH_BETA = "oauth-2025-04-20";
export const ANTHROPIC_VERSION = "2023-06-01";

/** The identity Anthropic requires as the FIRST system block for OAuth
 *  (subscription) requests. Without it the Bearer token is refused. This is the
 *  same string the claude CLI sends. */
export const CLAUDE_CODE_SYSTEM_IDENTITY =
  "You are Claude Code, Anthropic's official CLI for Claude.";

export interface ClaudeOAuthEndpoints {
  clientId: string;
  tokenUrl: string;
  messagesUrl: string;
  beta: string;
  version: string;
}

export const DEFAULT_ENDPOINTS: ClaudeOAuthEndpoints = {
  clientId: CLAUDE_CODE_OAUTH_CLIENT_ID,
  tokenUrl: CLAUDE_OAUTH_TOKEN_URL,
  messagesUrl: ANTHROPIC_MESSAGES_URL,
  beta: ANTHROPIC_OAUTH_BETA,
  version: ANTHROPIC_VERSION,
};

export interface ClaudeOAuthCreds {
  accessToken: string;
  refreshToken?: string;
  /** ms since epoch; undefined = unknown (treat as needs-refresh if we have a
   *  refresh token, else best-effort use). */
  expiresAt?: number;
  scopes?: string[];
}

/** Parse the mirrored ~/.claude/.credentials.json (string or already-parsed
 *  object) into ClaudeOAuthCreds. Returns null if it isn't a subscription-OAuth
 *  credential (e.g. someone fed us an API key file). Defensive: tolerates the
 *  raw oauth object at top level too. */
export function parseClaudeCredentials(input: string | Record<string, any>): ClaudeOAuthCreds | null {
  let obj: Record<string, any>;
  if (typeof input === "string") {
    try {
      obj = JSON.parse(input);
    } catch {
      return null;
    }
  } else {
    obj = input || {};
  }
  const oauth = obj.claudeAiOauth ?? obj.claudeAiOAuth ?? (obj.accessToken ? obj : null);
  if (!oauth || typeof oauth.accessToken !== "string" || !oauth.accessToken) {
    return null;
  }
  // Subscription tokens are sk-ant-oat-*; reject metered keys so we never burn
  // API credit by accident.
  if (oauth.accessToken.startsWith("sk-ant-api")) {
    return null;
  }
  return {
    accessToken: oauth.accessToken,
    refreshToken: typeof oauth.refreshToken === "string" ? oauth.refreshToken : undefined,
    expiresAt: typeof oauth.expiresAt === "number" ? oauth.expiresAt : undefined,
    scopes: Array.isArray(oauth.scopes) ? oauth.scopes.filter((s: any) => typeof s === "string") : undefined,
  };
}

/** Serialize back to the on-disk/SecureStore shape so a refreshed token round-
 *  trips identically to what the CLI writes. */
export function serializeClaudeCredentials(creds: ClaudeOAuthCreds): string {
  return JSON.stringify({
    claudeAiOauth: {
      accessToken: creds.accessToken,
      refreshToken: creds.refreshToken,
      expiresAt: creds.expiresAt,
      scopes: creds.scopes,
    },
  });
}

/** True when the access token is expired or within `skewMs` of expiring.
 *  Unknown expiry → not-expired (let the call try; a 401 triggers refresh). */
export function isExpired(creds: ClaudeOAuthCreds, nowMs: number, skewMs = 60_000): boolean {
  if (typeof creds.expiresAt !== "number") return false;
  return nowMs >= creds.expiresAt - skewMs;
}

export interface HttpRequest {
  url: string;
  init: {
    method: string;
    headers: Record<string, string>;
    body: string;
  };
}

/** Build the OAuth refresh request (grant_type=refresh_token). Throws if there
 *  is no refresh token — the caller must then re-mirror from the desktop. */
export function buildRefreshRequest(
  creds: ClaudeOAuthCreds,
  ep: ClaudeOAuthEndpoints = DEFAULT_ENDPOINTS,
): HttpRequest {
  if (!creds.refreshToken) {
    throw new Error("no refresh token — re-mirror credentials from the desktop");
  }
  return {
    url: ep.tokenUrl,
    init: {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        grant_type: "refresh_token",
        refresh_token: creds.refreshToken,
        client_id: ep.clientId,
      }),
    },
  };
}

/** Parse a refresh response into fresh creds, preserving the old refresh token
 *  when the server doesn't rotate it. */
export function parseRefreshResponse(
  prev: ClaudeOAuthCreds,
  body: string | Record<string, any>,
  nowMs: number,
): ClaudeOAuthCreds {
  const obj = typeof body === "string" ? JSON.parse(body) : body;
  const access = obj.access_token ?? obj.accessToken;
  if (typeof access !== "string" || !access) {
    throw new Error("refresh response missing access_token");
  }
  const expiresInSec = typeof obj.expires_in === "number" ? obj.expires_in : undefined;
  return {
    accessToken: access,
    refreshToken: obj.refresh_token ?? obj.refreshToken ?? prev.refreshToken,
    expiresAt: expiresInSec != null ? nowMs + expiresInSec * 1000 : prev.expiresAt,
    scopes: prev.scopes,
  };
}

/** Anthropic system blocks. OAuth requires the identity block FIRST. */
export type SystemBlock = { type: "text"; text: string };

/** Prepend the Claude Code identity so the OAuth token is accepted. If the
 *  caller's system already starts with the identity we don't double it. */
export function withClaudeCodeIdentity(userSystem?: string): SystemBlock[] {
  const blocks: SystemBlock[] = [{ type: "text", text: CLAUDE_CODE_SYSTEM_IDENTITY }];
  const extra = (userSystem ?? "").trim();
  if (extra && extra !== CLAUDE_CODE_SYSTEM_IDENTITY) {
    blocks.push({ type: "text", text: extra });
  }
  return blocks;
}

export interface MessagesParams {
  creds: ClaudeOAuthCreds;
  model: string;
  /** plain user-facing system prompt; identity is injected automatically */
  system?: string;
  messages: Array<{ role: "user" | "assistant"; content: any }>;
  tools?: any[];
  toolChoice?: any;
  maxTokens?: number;
  stream?: boolean;
}

/** Build the /v1/messages request that bills against the SUBSCRIPTION (Bearer +
 *  oauth beta + identity), never the metered API key. */
export function buildMessagesRequest(
  p: MessagesParams,
  ep: ClaudeOAuthEndpoints = DEFAULT_ENDPOINTS,
): HttpRequest {
  const headers: Record<string, string> = {
    "Content-Type": "application/json",
    Authorization: `Bearer ${p.creds.accessToken}`,
    "anthropic-version": ep.version,
    "anthropic-beta": ep.beta,
  };
  const body: Record<string, any> = {
    model: p.model,
    max_tokens: p.maxTokens ?? 4096,
    system: withClaudeCodeIdentity(p.system),
    messages: p.messages,
    stream: !!p.stream,
  };
  if (p.tools && p.tools.length) body.tools = p.tools;
  if (p.toolChoice) body.tool_choice = p.toolChoice;
  return { url: ep.messagesUrl, init: { method: "POST", headers, body: JSON.stringify(body) } };
}

/** Guard: assert a request will NOT bill the metered API. Used by the store and
 *  in tests so a regression to x-api-key is caught loudly. */
export function assertSubscriptionRequest(req: HttpRequest): void {
  const h = req.init.headers;
  if (h["x-api-key"] || h["X-Api-Key"]) {
    throw new Error("refusing metered request: x-api-key present (would bill API, not plan)");
  }
  if (!String(h.Authorization || "").startsWith("Bearer ")) {
    throw new Error("subscription request must use Authorization: Bearer <oauth token>");
  }
}
