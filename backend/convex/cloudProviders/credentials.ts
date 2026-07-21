import { SignJWT, importPKCS8 } from "jose";

/**
 * ─── Provider credential acquisition ────────────────────────────────────────
 *
 * WHY THIS MODULE EXISTS
 *
 * GCP and Azure were configured with `GCP_ACCESS_TOKEN` / `AZURE_BEARER_TOKEN`
 * — raw OAuth **access tokens** pasted into Convex env vars. Those expire in
 * about an hour and nothing refreshed them, so both adapters were
 * broken-by-design in production: they would work for one hour after a human
 * pasted a token and then fail silently-ish forever after.
 *
 * That is not a configuration mistake, it is a missing component. A provider
 * adapter that cannot obtain its own credentials cannot be production-eligible,
 * no matter how correct its API calls are.
 *
 * WHAT REPLACES IT
 *
 * - GCP: a **service-account JWT** (RS256, signed with the SA private key) is
 *   exchanged for an access token at Google's OAuth endpoint. Standard
 *   two-legged OAuth; no user interaction, refreshes forever.
 * - Azure: **client-credentials** flow against Entra ID. Also non-interactive.
 * - AWS: nothing needed here — SigV4 signs each request from long-lived keys.
 *
 * SECRET HANDLING (CLAUDE.md: private material never in the repo)
 *
 * The SA private key and the Azure client secret are PRIVATE — they live in
 * Convex env only, never in a tracked file. This module reads them from env and
 * never logs them. Errors deliberately name the missing VARIABLE, never its
 * value.
 *
 * CACHING
 *
 * Tokens are cached in module scope until shortly before expiry. Convex isolates
 * are recycled, so this is a best-effort warm cache, not a guarantee — the
 * refresh path must therefore be cheap and idempotent, which it is.
 */

type CachedToken = { token: string; expiresAt: number };

// Refresh this long before actual expiry so an in-flight call cannot straddle
// the boundary and fail with a 401 halfway through a provision.
const EXPIRY_SKEW_MS = 5 * 60 * 1000;

const cache = new Map<string, CachedToken>();

function cached(key: string): string | undefined {
  const hit = cache.get(key);
  if (hit && hit.expiresAt - EXPIRY_SKEW_MS > Date.now()) return hit.token;
  return undefined;
}

function store(key: string, token: string, expiresInSeconds: number): string {
  cache.set(key, { token, expiresAt: Date.now() + expiresInSeconds * 1000 });
  return token;
}

export class CredentialError extends Error {
  readonly provider: string;
  constructor(provider: string, message: string) {
    super(message);
    this.name = "CredentialError";
    this.provider = provider;
  }
}

/* ────────────────────────────── GCP ────────────────────────────── */

type GcpServiceAccount = {
  client_email?: string;
  private_key?: string;
  token_uri?: string;
  project_id?: string;
};

function readGcpServiceAccount(env: Record<string, string | undefined>): GcpServiceAccount | undefined {
  const raw = env.GCP_SERVICE_ACCOUNT_JSON;
  if (!raw) return undefined;
  try {
    // Accept both raw JSON and base64, because operators paste both and a
    // "malformed JSON" error for a perfectly valid base64 blob wastes a session.
    const text = raw.trim().startsWith("{") ? raw : new TextDecoder().decode(
      Uint8Array.from(atob(raw.trim()), (c) => c.charCodeAt(0)),
    );
    return JSON.parse(text) as GcpServiceAccount;
  } catch {
    throw new CredentialError("gcp", "GCP_SERVICE_ACCOUNT_JSON is neither valid JSON nor valid base64-encoded JSON");
  }
}

/** The GCP project the service account belongs to, when not set explicitly. */
export function gcpProjectIdFromEnv(env: Record<string, string | undefined> = process.env): string | undefined {
  if (env.GCP_PROJECT_ID) return env.GCP_PROJECT_ID;
  try {
    return readGcpServiceAccount(env)?.project_id;
  } catch {
    return undefined;
  }
}

/**
 * Access token for the Compute API.
 *
 * Prefers a real service account. Falls back to a static `GCP_ACCESS_TOKEN`
 * ONLY for local/manual probing — that path cannot refresh and must never be
 * how production runs.
 */
export async function getGcpAccessToken(
  env: Record<string, string | undefined> = process.env,
): Promise<string> {
  const key = "gcp";
  const hit = cached(key);
  if (hit) return hit;

  const sa = readGcpServiceAccount(env);
  if (!sa) {
    const stat = env.GCP_ACCESS_TOKEN;
    if (stat) return stat; // manual probing only — expires in ~1h, cannot refresh
    throw new CredentialError(
      "gcp",
      "Set GCP_SERVICE_ACCOUNT_JSON (service-account key JSON) in Convex env. " +
        "GCP_ACCESS_TOKEN is accepted only for manual probing: it expires in about an hour and nothing can refresh it.",
    );
  }
  if (!sa.client_email || !sa.private_key) {
    throw new CredentialError("gcp", "GCP_SERVICE_ACCOUNT_JSON is missing client_email or private_key");
  }

  const tokenUri = sa.token_uri || "https://oauth2.googleapis.com/token";
  const now = Math.floor(Date.now() / 1000);
  let assertion: string;
  try {
    // Service-account keys are PKCS#8 PEM. Newlines are frequently mangled to
    // literal "\n" when pasted through a shell or a web form — repair rather
    // than fail, because the resulting crypto error is deeply unhelpful.
    const pem = sa.private_key.includes("\\n")
      ? sa.private_key.replace(/\\n/g, "\n")
      : sa.private_key;
    const pk = await importPKCS8(pem, "RS256");
    assertion = await new SignJWT({
      scope: "https://www.googleapis.com/auth/cloud-platform",
    })
      .setProtectedHeader({ alg: "RS256", typ: "JWT" })
      .setIssuer(sa.client_email)
      .setSubject(sa.client_email)
      .setAudience(tokenUri)
      .setIssuedAt(now)
      .setExpirationTime(now + 3600)
      .sign(pk);
  } catch (e) {
    throw new CredentialError(
      "gcp",
      `Could not sign the service-account JWT — is private_key a valid PKCS#8 PEM? (${e instanceof Error ? e.message : String(e)})`,
    );
  }

  const res = await fetch(tokenUri, {
    method: "POST",
    headers: { "Content-Type": "application/x-www-form-urlencoded" },
    body: new URLSearchParams({
      grant_type: "urn:ietf:params:oauth:grant-type:jwt-bearer",
      assertion,
    }).toString(),
  });
  if (!res.ok) {
    throw new CredentialError("gcp", `GCP token exchange failed: HTTP ${res.status} ${(await res.text()).slice(0, 300)}`);
  }
  const j = (await res.json()) as { access_token?: string; expires_in?: number };
  if (!j.access_token) throw new CredentialError("gcp", "GCP token exchange returned no access_token");
  return store(key, j.access_token, j.expires_in ?? 3600);
}

/* ───────────────────────────── Azure ───────────────────────────── */

/**
 * Access token for ARM via the client-credentials flow.
 *
 * Falls back to a static `AZURE_BEARER_TOKEN` ONLY for manual probing, same
 * caveat as GCP.
 */
export async function getAzureAccessToken(
  env: Record<string, string | undefined> = process.env,
): Promise<string> {
  const key = "azure";
  const hit = cached(key);
  if (hit) return hit;

  const tenantId = env.AZURE_TENANT_ID;
  const clientId = env.AZURE_CLIENT_ID;
  const clientSecret = env.AZURE_CLIENT_SECRET;
  if (!tenantId || !clientId || !clientSecret) {
    const stat = env.AZURE_BEARER_TOKEN;
    if (stat) return stat; // manual probing only — expires, cannot refresh
    throw new CredentialError(
      "azure",
      "Set AZURE_TENANT_ID, AZURE_CLIENT_ID and AZURE_CLIENT_SECRET in Convex env. " +
        "AZURE_BEARER_TOKEN is accepted only for manual probing: it expires and nothing can refresh it.",
    );
  }

  const res = await fetch(`https://login.microsoftonline.com/${encodeURIComponent(tenantId)}/oauth2/v2.0/token`, {
    method: "POST",
    headers: { "Content-Type": "application/x-www-form-urlencoded" },
    body: new URLSearchParams({
      grant_type: "client_credentials",
      client_id: clientId,
      client_secret: clientSecret,
      scope: "https://management.azure.com/.default",
    }).toString(),
  });
  if (!res.ok) {
    throw new CredentialError("azure", `Azure token request failed: HTTP ${res.status} ${(await res.text()).slice(0, 300)}`);
  }
  const j = (await res.json()) as { access_token?: string; expires_in?: number };
  if (!j.access_token) throw new CredentialError("azure", "Azure token request returned no access_token");
  return store(key, j.access_token, j.expires_in ?? 3600);
}

/** True when the provider can obtain a token WITHOUT a human pasting one. */
export function hasRefreshableCredentials(
  provider: "gcp" | "azure",
  env: Record<string, string | undefined> = process.env,
): boolean {
  if (provider === "gcp") return Boolean(env.GCP_SERVICE_ACCOUNT_JSON);
  return Boolean(env.AZURE_TENANT_ID && env.AZURE_CLIENT_ID && env.AZURE_CLIENT_SECRET);
}
