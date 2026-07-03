import { httpRouter } from "convex/server";
import { v } from "convex/values";
import { httpAction, internalAction } from "./_generated/server";
import { api, internal } from "./_generated/api";
import { sha256Hex } from "./auth";
import { isOwnerEmail, isOwner } from "./ownerAllowlist";
import { decryptStoredOidcSecret } from "./admin";
import { estimatedHourlyCents, minimumReserveCents } from "./cloudLifecycle";

const http = httpRouter();

// ── Helpers ──────────────────────────────────────────────────────────

function jsonResponse(data: unknown, status = 200): Response {
  return new Response(JSON.stringify(data), {
    status,
    headers: {
      "Content-Type": "application/json",
      "Access-Control-Allow-Origin": "*",
    },
  });
}

function errorResponse(message: string, status = 400): Response {
  return jsonResponse({ error: message }, status);
}

function errorMessageIncludes(err: any, code: string): boolean {
  return String(err?.message || "").includes(code);
}

// Owner / private-preview check. Delegates to the single source of
// truth in ownerAllowlist.ts (env-var allowlist, never hardcoded).
function isCloudPreviewUser(
  email?: string | null,
  userId?: string | null,
): boolean {
  // Email OR userId allowlist — OAuth accounts often have no email,
  // so id-based is the reliable owner check (CLOUD_PREVIEW_OWNER_USER_IDS).
  return isOwner(email, userId);
}

// Who may touch the prepaid-cloud surfaces (balance, credit-pack
// checkout, spin up/down, usage). Owner allowlist is always in; flip
// YAVER_CLOUD_PUBLIC=true to open it to every authenticated user at
// launch. Default (env unset) = owner-only private preview, so this is
// a one-env-flip go-live and the source stays free/fail-closed until
// the owner decides (project_business_model). Money is still protected
// independently: real Hetzner spend gated on HCLOUD_TOKEN, debits gated
// on the prepaid balance — so opening this never lets a stranger spend
// Yaver's money, only their own topped-up credit.
function cloudAccessAllowed(email?: string | null, userId?: string | null): boolean {
  if (isCloudPreviewUser(email, userId)) return true;
  return parseBooleanEnv(process.env.YAVER_CLOUD_PUBLIC, false);
}

function parseBooleanEnv(value: string | undefined, fallback: boolean): boolean {
  if (!value) return fallback;
  const normalized = value.trim().toLowerCase();
  if (normalized === "true" || normalized === "1" || normalized === "yes") return true;
  if (normalized === "false" || normalized === "0" || normalized === "no") return false;
  return fallback;
}

// LemonSqueezy docs use LEMONSQUEEZY_* (no underscore between LEMON+SQUEEZY),
// but some deploys store LEMON_SQUEEZY_* (Convex default in this project).
// Accept either so a rename on one side doesn't silently break checkout.
function lsEnv(suffix: string): string | undefined {
  return process.env["LEMONSQUEEZY_" + suffix] ?? process.env["LEMON_SQUEEZY_" + suffix];
}

// Constant-time HMAC-SHA256 verification for LemonSqueezy webhooks.
// LS signs the raw body with LEMONSQUEEZY_WEBHOOK_SECRET and sends the hex
// digest in the X-Signature header. If no secret is configured we log a
// warning and accept — matches the previous "TODO" behaviour but surfaces it.
async function verifyLemonSqueezySignature(body: string, signatureHeader: string | null): Promise<boolean> {
  const secret = lsEnv("WEBHOOK_SECRET");
  if (!secret) {
    // FAIL CLOSED. The webhook credits wallets + provisions paid boxes;
    // an unsigned-accept here lets anyone who knows the (public, open-
    // source) Convex URL POST a forged order_created to mint unlimited
    // free credit. Reject unless the operator EXPLICITLY opts into
    // unsigned webhooks for LOCAL DEV ONLY via
    // LEMONSQUEEZY_ALLOW_UNSIGNED=true. Default (env unset) = reject.
    if (parseBooleanEnv(lsEnv("ALLOW_UNSIGNED"), false)) {
      console.warn("[lemonsqueezy] WEBHOOK_SECRET unset + ALLOW_UNSIGNED=true — accepting UNSIGNED webhook (DEV ONLY)");
      return true;
    }
    console.error("[lemonsqueezy] WEBHOOK_SECRET not set — rejecting webhook (fail-closed). Set the secret, or LEMONSQUEEZY_ALLOW_UNSIGNED=true for local dev only.");
    return false;
  }
  if (!signatureHeader) return false;
  const key = await crypto.subtle.importKey(
    "raw",
    new TextEncoder().encode(secret),
    { name: "HMAC", hash: "SHA-256" },
    false,
    ["sign"],
  );
  const sigBuf = await crypto.subtle.sign("HMAC", key, new TextEncoder().encode(body));
  const expected = Array.from(new Uint8Array(sigBuf))
    .map((b) => b.toString(16).padStart(2, "0"))
    .join("");
  // Constant-time compare — same length + byte-by-byte xor.
  const a = expected;
  const b = signatureHeader.trim().toLowerCase();
  if (a.length !== b.length) return false;
  let diff = 0;
  for (let i = 0; i < a.length; i++) diff |= a.charCodeAt(i) ^ b.charCodeAt(i);
  return diff === 0;
}

function normalizeCloudHost(baseUrl: string): string {
  try {
    return new URL(baseUrl).host;
  } catch {
    return baseUrl.replace(/^https?:\/\//, "").replace(/\/.*$/, "");
  }
}

async function createLemonSqueezyCheckout(args: {
  email: string;
  custom: Record<string, string>;
  // Defaults to the managed-cloud subscription variant. Credit-pack
  // checkout passes the resolved one-time pack variant id here.
  variantId?: string;
  variantEnvName?: string;
}): Promise<string> {
  const apiKey = lsEnv("API_KEY");
  const storeId = lsEnv("STORE_ID");
  const variantId = args.variantId ?? lsEnv("YAVER_CLOUD_VARIANT_ID");
  if (!apiKey || !storeId || !variantId) {
    const missing = [
      !apiKey && "API_KEY",
      !storeId && "STORE_ID",
      !variantId && (args.variantEnvName ?? "YAVER_CLOUD_VARIANT_ID"),
    ].filter(Boolean).join(", ");
    throw new Error(`Missing Lemon Squeezy configuration: ${missing}`);
  }

  const receiptLink =
    lsEnv("YAVER_CLOUD_RECEIPT_LINK_URL") ||
    lsEnv("CHECKOUT_REDIRECT_URL") ||
    process.env.NEXT_PUBLIC_BASE_URL ||
    "https://yaver.io/";
  const receiptButtonText =
    lsEnv("YAVER_CLOUD_RECEIPT_BUTTON_TEXT") || "Open Yaver";

  const payload = {
    data: {
      type: "checkouts",
      attributes: {
        checkout_data: {
          email: args.email,
          custom: args.custom,
        },
        checkout_options: {
          embed: false,
          media: false,
          logo: true,
        },
        product_options: {
          receipt_button_text: receiptButtonText,
          receipt_link_url: receiptLink,
          redirect_url: receiptLink,
        },
      },
      relationships: {
        store: { data: { type: "stores", id: storeId } },
        variant: { data: { type: "variants", id: variantId } },
      },
    },
  };

  const response = await fetch("https://api.lemonsqueezy.com/v1/checkouts", {
    method: "POST",
    headers: {
      Authorization: `Bearer ${apiKey}`,
      Accept: "application/vnd.api+json",
      "Content-Type": "application/vnd.api+json",
    },
    body: JSON.stringify(payload),
  });
  const raw = await response.text();
  if (!response.ok) {
    throw new Error(`Lemon Squeezy checkout failed (${response.status}): ${raw}`);
  }

  const parsed = JSON.parse(raw);
  const url = parsed?.data?.attributes?.url;
  if (typeof url !== "string" || !url) {
    throw new Error("Lemon Squeezy checkout URL missing");
  }
  return url;
}

// Prepaid credit-pack catalog — OpenAI-style "pick a pack" top-up.
// Each pack is a LemonSqueezy ONE-TIME product; its variant id lives in
// env (LEMONSQUEEZY_CREDIT_PACK_<ID>_VARIANT_ID, e.g. *_P25_VARIANT_ID)
// so packs can be added/repriced without a code change. amountCents is
// the credit added to the wallet (what the buyer pays = the LS price;
// keep them equal — the markup is taken on COMPUTE metering, not here).
const CREDIT_PACKS: Array<{ id: string; cents: number; label: string }> = [
  { id: "p10", cents: 1000, label: "$10" },
  { id: "p25", cents: 2500, label: "$25" },
  { id: "p50", cents: 5000, label: "$50" },
  { id: "p100", cents: 10000, label: "$100" },
];

function creditPackById(id: string) {
  return CREDIT_PACKS.find((p) => p.id === id) || null;
}

function creditPackVariantEnvName(id: string): string {
  return `CREDIT_PACK_${id.toUpperCase()}_VARIANT_ID`;
}

type CloudPurchasePlanId = "cloud-agent" | "cloud-workspace";

function normalizeCloudPurchasePlan(value: unknown): CloudPurchasePlanId {
  const normalized = String(value || "").trim();
  if (normalized === "cloud-workspace") return "cloud-workspace";
  return "cloud-agent";
}

// Resolve a pack from the LemonSqueezy variant id that was ACTUALLY
// purchased (it rides in the signed webhook payload, so a buyer can't
// forge which pack they bought). This is the authoritative resolution —
// never trust pack_id / amount from client-set custom_data. Returns
// null if the variant isn't one of our configured packs.
function creditPackByVariantId(variantId: string | number | undefined | null) {
  if (variantId === undefined || variantId === null) return null;
  const want = String(variantId).trim();
  if (!want) return null;
  for (const p of CREDIT_PACKS) {
    const configured = lsEnv(creditPackVariantEnvName(p.id));
    if (configured && String(configured).trim() === want) return p;
  }
  return null;
}

// Cancel a subscription on LemonSqueezy itself (DELETE = cancel at
// period end). Yaver-initiated decommission / reconcile orphan-sweep
// must do this: cancelling only the Convex row stops Yaver's own
// reconcile, but a real paying customer would keep being billed by
// LemonSqueezy. Best-effort — a missing API key, a non-numeric
// (e2e/test) id, a 404, or a network error is logged and swallowed;
// the Convex row is already cancelled and is the source of truth for
// Yaver's gates. Scheduled from subscriptions.cancelById.
export const cancelLemonSqueezySubscription = internalAction({
  args: { lemonSqueezyId: v.string() },
  handler: async (_ctx, { lemonSqueezyId }) => {
    const apiKey = lsEnv("API_KEY");
    if (!apiKey) {
      console.warn("[lemonsqueezy] subscription cancel skipped — API_KEY not configured");
      return;
    }
    // Real LemonSqueezy subscription ids are numeric strings; e2e
    // fixtures use "e2e-…". Calling the API for a synthetic id just 404s.
    if (!/^[0-9]+$/.test(lemonSqueezyId)) {
      console.log(`[lemonsqueezy] subscription cancel skipped — non-numeric id "${lemonSqueezyId}" (test/dev sub)`);
      return;
    }
    try {
      const resp = await fetch(
        `https://api.lemonsqueezy.com/v1/subscriptions/${lemonSqueezyId}`,
        {
          method: "DELETE",
          headers: {
            Authorization: `Bearer ${apiKey}`,
            Accept: "application/vnd.api+json",
          },
        },
      );
      if (resp.ok || resp.status === 404) {
        console.log(`[lemonsqueezy] subscription ${lemonSqueezyId} cancelled (HTTP ${resp.status})`);
      } else {
        console.error(
          `[lemonsqueezy] subscription ${lemonSqueezyId} cancel returned HTTP ${resp.status}: ${await resp.text()}`,
        );
      }
    } catch (e) {
      console.error(`[lemonsqueezy] subscription ${lemonSqueezyId} cancel failed:`, e);
    }
  },
});

// Swap the billed variant of an existing LemonSqueezy subscription so a
// future renewal charges the new plan's price (Cloud Agent ⇄ Workspace).
// Target variant id comes from env (YAVER_CLOUD_HOSTED_VARIANT_ID /
// YAVER_CLOUD_BYOK_VARIANT_ID) so prices retune without a redeploy.
// Returns {ok}. Best-effort + non-numeric/test ids skipped (mirrors the
// cancel action). Used by plans.changePlan.
export const updateLemonSqueezyVariant = internalAction({
  args: { lemonSqueezyId: v.string(), tier: v.union(v.literal("hosted"), v.literal("byok")) },
  handler: async (_ctx, { lemonSqueezyId, tier }): Promise<{ ok: boolean; reason?: string }> => {
    const apiKey = lsEnv("API_KEY");
    const variantId = lsEnv(tier === "hosted" ? "YAVER_CLOUD_HOSTED_VARIANT_ID" : "YAVER_CLOUD_BYOK_VARIANT_ID");
    if (!apiKey) return { ok: false, reason: "no-api-key" };
    if (!variantId) return { ok: false, reason: "variant-unconfigured" };
    if (!/^[0-9]+$/.test(lemonSqueezyId)) {
      console.log(`[lemonsqueezy] variant swap skipped — non-numeric id "${lemonSqueezyId}" (test/dev sub)`);
      return { ok: true, reason: "test-sub" };
    }
    try {
      const resp = await fetch(`https://api.lemonsqueezy.com/v1/subscriptions/${lemonSqueezyId}`, {
        method: "PATCH",
        headers: {
          Authorization: `Bearer ${apiKey}`,
          Accept: "application/vnd.api+json",
          "Content-Type": "application/vnd.api+json",
        },
        body: JSON.stringify({
          data: {
            type: "subscriptions",
            id: lemonSqueezyId,
            attributes: { variant_id: Number(variantId) },
          },
        }),
      });
      if (resp.ok) return { ok: true };
      console.error(`[lemonsqueezy] variant swap HTTP ${resp.status}: ${await resp.text()}`);
      return { ok: false, reason: `http-${resp.status}` };
    } catch (e) {
      console.error("[lemonsqueezy] variant swap failed:", e);
      return { ok: false, reason: "threw" };
    }
  },
});

async function attachPreviewMachineToSharedServer(
  ctx: { runMutation: (mutation: any, args: any) => Promise<any> },
  machineId: string,
  region: string,
) {
  const baseUrl =
    process.env.YAVER_CLOUD_PREVIEW_BASE_URL ||
    process.env.YAVER_CLOUD_BASE_URL ||
    process.env.YAVER_CLOUD_PUBLIC_BASE_URL ||
    "https://cloud.yaver.io";
  const hostname =
    process.env.YAVER_CLOUD_PREVIEW_HOSTNAME ||
    process.env.YAVER_CLOUD_PUBLIC_HOSTNAME ||
    normalizeCloudHost(baseUrl);
  const serverIp =
    process.env.YAVER_CLOUD_PREVIEW_SERVER_IP ||
    process.env.YAVER_CLOUD_PUBLIC_SERVER_IP;
  const providerId =
    process.env.YAVER_CLOUD_PREVIEW_PROVIDER_ID ||
    `preview-${region}`;

  await ctx.runMutation(api.cloudMachines.updateStatus, {
    machineId,
    status: "active",
    hostname,
    serverIp,
    hetznerServerId: providerId,
  });
}

async function ensurePreviewCloudMachine(
  ctx: {
    runQuery: (query: any, args: any) => Promise<any>;
    runMutation: (mutation: any, args: any) => Promise<any>;
  },
  userDocId: string,
  region: string,
) {
  const machines = await ctx.runQuery(api.cloudMachines.listForUser, { userId: userDocId });
  const existing = Array.isArray(machines)
    ? machines.find((machine) => machine.machineType === "cpu" && machine.status !== "stopped")
    : null;
  if (existing?._id) {
    await attachPreviewMachineToSharedServer(ctx, existing._id, region);
    return existing._id;
  }

  const machineId = await ctx.runMutation(api.cloudMachines.create, {
    userId: userDocId,
    machineType: "cpu",
    region,
  });
  await attachPreviewMachineToSharedServer(ctx, machineId, region);
  return machineId;
}

/** Extract Bearer token from Authorization header, hash it, and validate. */
async function authenticateRequest(
  ctx: { runQuery: (query: any, args: any) => Promise<any> },
  request: Request
): Promise<{
  userDocId: string;
  userId: string;
  email: string;
  fullName: string;
  provider: string;
  avatarUrl?: string;
  surveyCompleted: boolean;
  emailVerified: boolean;
  isOwner: boolean;
} | null> {
  const authHeader = request.headers.get("Authorization");
  if (!authHeader?.startsWith("Bearer ")) return null;

  const token = authHeader.slice(7);
  const tokenHash = await sha256Hex(token);

  const result = await ctx.runQuery(api.auth.validateSession, { tokenHash });
  if (!result) return null;
  return {
    userDocId: String(result.userDocId),
    userId: result.userId,
    email: result.email,
    fullName: result.fullName,
    provider: result.provider,
    avatarUrl: result.avatarUrl,
    // Forward survey state so /auth/validate's response includes it.
    // Without this, mobile's AuthContext sees surveyCompleted=false on
    // every cold start and falls back to /survey — and any hiccup on
    // that fallback (5s timeout, transient 5xx) silently shows the
    // onboarding form to a returning user.
    surveyCompleted: !!result.surveyCompleted,
    // emailVerified gates the "Add OAuth provider via email match" flow
    // and powers Settings UI banners. OAuth-signup users are verified
    // by construction; email + passkey signups start unverified and
    // graduate via /auth/verify-email/confirm.
    emailVerified: result.emailVerified === true,
    // Owner flag drives owner-only hardware-cell visibility on every client
    // (web/mobile/daemon) without shipping any owner identity to the client.
    isOwner: (result as { isOwner?: boolean }).isOwner === true,
  };
}

// ── Password Hashing Helpers (PBKDF2-SHA256) ────────────────────────

async function hashPassword(password: string): Promise<string> {
  const encoder = new TextEncoder();
  const salt = crypto.getRandomValues(new Uint8Array(16));
  const keyMaterial = await crypto.subtle.importKey(
    "raw",
    encoder.encode(password),
    "PBKDF2",
    false,
    ["deriveBits"]
  );
  const hash = await crypto.subtle.deriveBits(
    { name: "PBKDF2", salt, iterations: 100000, hash: "SHA-256" },
    keyMaterial,
    256
  );
  const saltB64 = btoa(String.fromCharCode(...salt));
  const hashB64 = btoa(String.fromCharCode(...new Uint8Array(hash)));
  return `${saltB64}:${hashB64}`;
}

async function verifyPassword(password: string, stored: string): Promise<boolean> {
  const [saltB64, hashB64] = stored.split(":");
  if (!saltB64 || !hashB64) return false;
  const encoder = new TextEncoder();
  const salt = Uint8Array.from(atob(saltB64), (c) => c.charCodeAt(0));
  const keyMaterial = await crypto.subtle.importKey(
    "raw",
    encoder.encode(password),
    "PBKDF2",
    false,
    ["deriveBits"]
  );
  const hash = await crypto.subtle.deriveBits(
    { name: "PBKDF2", salt, iterations: 100000, hash: "SHA-256" },
    keyMaterial,
    256
  );
  const computedB64 = btoa(String.fromCharCode(...new Uint8Array(hash)));
  return computedB64 === hashB64;
}

async function createSessionToken(
  ctx: { runMutation: (m: any, args: any) => Promise<any> },
  userId: any,
  deviceId?: string,
) {
  const tokenBytes = new Uint8Array(32);
  crypto.getRandomValues(tokenBytes);
  const token = Array.from(tokenBytes)
    .map((b) => b.toString(16).padStart(2, "0"))
    .join("");
  const tokenHash = await sha256Hex(token);
  const expiresAt = Date.now() + 365 * 24 * 60 * 60 * 1000;
  await ctx.runMutation(api.auth.createSession, { tokenHash, userId, deviceId, expiresAt });
  return token;
}

// Best-effort per-instance login throttling. Convex may run multiple action
// instances, but this still blocks rapid password spraying against a hot shard.
type LoginAttempt = {
  failures: number;
  resetAt: number;
  lockedUntil: number;
};

const loginAttempts = new Map<string, LoginAttempt>();
const LOGIN_FAILURE_WINDOW_MS = 15 * 60 * 1000;
const LOGIN_LOCK_MS = 15 * 60 * 1000;
const LOGIN_MAX_FAILURES = 5;

function clientIp(request: Request): string {
  const cf = request.headers.get("CF-Connecting-IP");
  if (cf) return cf.trim();
  const forwarded = request.headers.get("X-Forwarded-For");
  if (forwarded) return forwarded.split(",")[0]?.trim() || "unknown";
  return request.headers.get("X-Real-IP")?.trim() || "unknown";
}

function loginAttemptKey(request: Request, email: string): string {
  return `${email.toLowerCase().trim()}|${clientIp(request)}`;
}

function loginLocked(key: string): boolean {
  const entry = loginAttempts.get(key);
  if (!entry) return false;
  const now = Date.now();
  if (entry.lockedUntil > now) return true;
  if (entry.resetAt <= now) loginAttempts.delete(key);
  return false;
}

function recordLoginFailure(key: string) {
  const now = Date.now();
  const current = loginAttempts.get(key);
  const entry: LoginAttempt = current && current.resetAt > now
    ? current
    : { failures: 0, resetAt: now + LOGIN_FAILURE_WINDOW_MS, lockedUntil: 0 };
  entry.failures += 1;
  if (entry.failures >= LOGIN_MAX_FAILURES) {
    entry.lockedUntil = now + LOGIN_LOCK_MS;
  }
  loginAttempts.set(key, entry);
}

function clearLoginFailures(key: string) {
  loginAttempts.delete(key);
}

// ── CORS Preflight for Auth Endpoints ───────────────────────────────
// Browsers send OPTIONS preflight when requests include Authorization headers.

const corsHeaders = {
  "Access-Control-Allow-Origin": "*",
  "Access-Control-Allow-Methods": "GET, POST, OPTIONS",
  "Access-Control-Allow-Headers": "Authorization, Content-Type",
  "Access-Control-Max-Age": "86400",
};

function corsPreflightResponse(): Response {
  return new Response(null, { status: 204, headers: corsHeaders });
}

for (const path of [
  "/auth/validate", "/auth/signup", "/auth/login", "/auth/refresh",
  "/auth/logout", "/auth/update-profile", "/auth/delete-account",
  "/auth/forgot-password", "/auth/reset-password", "/auth/change-password",
  "/auth/verify-totp", "/auth/providers", "/auth/oauth-link/start", "/auth/oauth-link/complete",
  "/auth/test/oauth-signin",
  "/auth/device-code/authorize",
  "/auth/passkey/register/start", "/auth/passkey/register/finish",
  "/auth/passkey/login/start", "/auth/passkey/login/finish",
  "/auth/passkey/signup/start", "/auth/passkey/signup/finish",
  "/auth/passkey/list", "/auth/passkey/remove", "/auth/passkey/check",
  "/auth/email-providers", "/auth/verify-email/request", "/auth/verify-email/confirm",
  "/devices/list", "/devices/owner-by-hardware", "/devices/pending-list", "/devices/pending-claim", "/devices/alias", "/devices/tags", "/devices/select", "/config", "/settings", "/settings/repair-relay", "/packages",
  "/mesh/peers", "/mesh/acls", "/mesh/acls/set", "/mesh/tags", "/mesh/tags/set", "/mesh/node/config", "/mesh/join", "/mesh/leave",
  "/support/invite", "/support/invite/info", "/support/connections", "/support/grant/revoke", "/support/deny-all",
  "/shortcuts", "/shortcuts/delete",
  "/subscription",
  "/billing/yaver-cloud/checkout",
  "/billing/yaver-cloud/balance",
  "/billing/yaver-cloud/provision",
  "/billing/yaver-cloud/start",
  "/billing/yaver-cloud/stop",
  "/billing/yaver-cloud/dev-activate",
  "/billing/yaver-cloud/dev-adopt",
  "/billing/yaver-cloud/dev-deprovision",
  "/billing/yaver-cloud/reconcile",
  "/billing/yaver-cloud/runners-authorized",
  "/billing/yaver-cloud/usage",
  "/billing/credits/checkout",
  "/billing/credits/packs",
  "/billing/status",
  "/billing/portal",
  "/managed/cockpit",
  "/managed/burn",
  "/managed/services",
  "/byo/machines",
  "/gateway/policy", "/gateway/policy/set",
  "/gateway/token/mint", "/gateway/token/revoke", "/gateway/token/rotate",
  "/beta/create-user",
  "/guests/invite", "/guests/accept", "/guests/accept-code",
  "/guests/find-by-code", "/guests/revoke", "/guests/list", "/guests/hosts",
  "/guests/allowed", "/guests/config", "/guests/usage",
  "/connections/request", "/connections/accept", "/connections/remove",
  "/connections/block", "/connections/unblock", "/connections/nickname",
  "/connections/list", "/connections/search", "/connections/suggested",
  "/project-shares/create", "/project-shares/invite", "/project-shares/accept",
  "/project-shares/list", "/project-shares/find-by-code", "/project-shares/set-role",
  "/project-shares/revoke-member", "/project-shares/archive",
  "/host-share/create", "/host-share/invite", "/host-share/join",
  "/host-share/revoke", "/host-share/list", "/host-share/sessions",
  "/host-share/access", "/host-share/touch",
  "/host-share/peer-access",
  "/users/lookup",
  "/agent-rescue/queue", "/agent-rescue/list",
  "/publish-jobs/queue", "/publish-jobs/list",
  "/packages/allocation", "/packages/accept", "/packages/shared",
]) {
  http.route({
    path,
    method: "OPTIONS",
    handler: httpAction(async () => corsPreflightResponse()),
  });
}

// ── Email/Password Auth Endpoints ───────────────────────────────────

/** POST /auth/signup — Email/password signup. */
http.route({
  path: "/auth/signup",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const body = await request.json();
    const { email, fullName, password } = body;

    if (!email || !fullName || !password) {
      return errorResponse("Missing required fields", 400);
    }
    if (password.length < 8) {
      return errorResponse("Password must be at least 8 characters", 400);
    }

    const passwordHash = await hashPassword(password);

    let userId;
    try {
      userId = await ctx.runMutation(api.auth.createEmailUser, {
        email: email.toLowerCase().trim(),
        fullName: fullName.trim(),
        passwordHash,
      });
    } catch (e: any) {
      if (e.message?.includes("EMAIL_EXISTS")) {
        return errorResponse("An account with this email already exists", 409);
      }
      return errorResponse("Signup failed", 500);
    }

    const token = await createSessionToken(ctx, userId);
    // userId returned to the client is the public userId string (stable,
    // shareable). Keep userDocId in its own field for back-compat.
    const publicUser = await ctx.runQuery(api.auth.getUserPublicProfile, { userDocId: userId });
    return jsonResponse({
      token,
      userId: publicUser?.userId ?? String(userId),
      userDocId: String(userId),
    });
  }),
});

/** POST /beta/create-user — OWNER ONLY. One-shot: create an email/password
 *  account (if it doesn't exist) AND seed full invisible beta access
 *  (GLM gateway grant + caps + included hours + hidden box grant +
 *  sharedProject). Reuses the real hashPassword path so the account logs in
 *  normally. Owner-gated by the cloud-preview allowlist (the same gate as
 *  every money route). Body: { email, password, fullName?, sharedProject? }.
 *
 *  NOTE: this provisions ACCESS GRANTS only. The actual coding/push/
 *  isolation experience requires the box-side data-plane (partition +
 *  two-repo push broker), which is not deployed yet — a created user can
 *  sign in and see the Beta surface but cannot push/isolate until that
 *  lands. See beta-invisible-infra-share-design.md. */
http.route({
  path: "/beta/create-user",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) return errorResponse("Unauthorized", 401);
    const session = await ctx.runQuery(api.auth.validateSession, {
      tokenHash: await sha256Hex(authHeader.slice(7)),
    });
    if (!session) return errorResponse("Unauthorized", 401);
    if (!isCloudPreviewUser(session.email, session.userDocId)) {
      return errorResponse("Owner only", 403);
    }

    const body = await request.json();
    const { email, password, fullName, sharedProject } = body ?? {};
    if (!email || !password) return errorResponse("email + password required", 400);
    if (String(password).length < 8) return errorResponse("password must be ≥ 8 chars", 400);
    const normEmail = String(email).toLowerCase().trim();

    let userDocId;
    let created = false;
    const existing = await ctx.runQuery(api.auth.lookupEmailUser, { email: normEmail });
    if (existing) {
      userDocId = existing._id;
    } else {
      const passwordHash = await hashPassword(String(password));
      userDocId = await ctx.runMutation(api.auth.createEmailUser, {
        email: normEmail,
        fullName: String(fullName || "Beta Tester").trim(),
        passwordHash,
      });
      created = true;
    }

    const seed = await ctx.runAction(internal.betaAccess.seedBetaUser, {
      guestUserId: userDocId,
      sharedProject: sharedProject ? String(sharedProject) : undefined,
    });

    return jsonResponse({
      ok: true,
      email: normEmail,
      userDocId: String(userDocId),
      accountCreated: created,
      sharedProject: sharedProject ? String(sharedProject) : null,
      seed,
    });
  }),
});

/** POST /auth/login — Email/password login. */
http.route({
  path: "/auth/login",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const body = await request.json();
    const { email, password } = body;

    if (!email || !password) {
      return errorResponse("Missing email or password", 400);
    }

    const normEmail = email.toLowerCase().trim();
    const attemptKey = loginAttemptKey(request, normEmail);
    if (loginLocked(attemptKey)) {
      return errorResponse("Too many failed attempts. Try again later.", 429);
    }

    const user = await ctx.runQuery(api.auth.lookupEmailUser, {
      email: normEmail,
    });

    if (!user || !user.passwordHash) {
      recordLoginFailure(attemptKey);
      return errorResponse("Invalid email or password", 401);
    }

    const valid = await verifyPassword(password, user.passwordHash);
    if (!valid) {
      recordLoginFailure(attemptKey);
      return errorResponse("Invalid email or password", 401);
    }
    clearLoginFailures(attemptKey);

    // Check if 2FA is enabled
    const fullUser = await ctx.runQuery(api.auth.getUserWithTotp, { userId: user._id });
    if (fullUser?.totpEnabled) {
      const { pendingToken } = await ctx.runMutation(api.totp.createPendingAuth, { userId: user._id });
      return jsonResponse({ requires2fa: true, pendingToken });
    }

    const token = await createSessionToken(ctx, user._id);
    return jsonResponse({ token, userId: user.userId });
  }),
});

// ── Passkey (WebAuthn) Endpoints ───────────────────────────────────
// Strictly additive to the existing email/OAuth flows. The four
// routes below mint the same session token /auth/login does, so all
// downstream auth (validate/refresh/etc.) is unchanged. Origin is
// validated inside the action against an allowlist.

/** POST /auth/passkey/register/start — signed-in user begins enrollment. */
http.route({
  path: "/auth/passkey/register/start",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const session = await authenticateRequest(ctx, request);
    if (!session) return errorResponse("Unauthorized", 401);
    const body = await request.json().catch(() => ({} as any));
    const origin = request.headers.get("Origin") || "";
    try {
      const options = await ctx.runAction(api.passkeys.registerStart, {
        userDocId: session.userDocId as any,
        origin,
        deviceLabel: typeof body?.deviceLabel === "string" ? body.deviceLabel : undefined,
      });
      return jsonResponse({ options });
    } catch (e: any) {
      return errorResponse(e?.message || "registerStart failed", 400);
    }
  }),
});

/** POST /auth/passkey/register/finish — signed-in user completes enrollment. */
http.route({
  path: "/auth/passkey/register/finish",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const session = await authenticateRequest(ctx, request);
    if (!session) return errorResponse("Unauthorized", 401);
    const body = await request.json().catch(() => ({} as any));
    if (!body?.response) return errorResponse("Missing response", 400);
    const origin = request.headers.get("Origin") || "";
    try {
      const result = await ctx.runAction(api.passkeys.registerFinish, {
        userDocId: session.userDocId as any,
        origin,
        response: body.response,
        deviceLabel: typeof body?.deviceLabel === "string" ? body.deviceLabel : undefined,
      });
      return jsonResponse(result);
    } catch (e: any) {
      return errorResponse(e?.message || "registerFinish failed", 400);
    }
  }),
});

/** POST /auth/passkey/signup/start — anonymous; brand-new email + display name.
 *  Returns either { ok:true, options } or { ok:false, error, hasPasskey } so
 *  the client can route email-already-registered users to sign-in instead. */
http.route({
  path: "/auth/passkey/signup/start",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const body = await request.json().catch(() => ({} as any));
    const email = String(body?.email || "").trim();
    const fullName = String(body?.fullName || "").trim();
    if (!email) return errorResponse("Missing email", 400);
    const origin = request.headers.get("Origin") || "";
    try {
      const result = await ctx.runAction(api.passkeys.signupStart, {
        origin,
        email,
        fullName,
      });
      return jsonResponse(result);
    } catch (e: any) {
      return errorResponse(e?.message || "signupStart failed", 400);
    }
  }),
});

/** POST /auth/passkey/signup/finish — anonymous; mints session for the new user. */
http.route({
  path: "/auth/passkey/signup/finish",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const body = await request.json().catch(() => ({} as any));
    if (!body?.response) return errorResponse("Missing response", 400);
    const email = String(body?.email || "").trim();
    const fullName = String(body?.fullName || "").trim();
    if (!email) return errorResponse("Missing email", 400);
    const origin = request.headers.get("Origin") || "";
    try {
      const result = await ctx.runAction(api.passkeys.signupFinish, {
        origin,
        email,
        fullName,
        response: body.response,
        deviceLabel: body?.deviceLabel,
      });
      return jsonResponse(result);
    } catch (e: any) {
      return errorResponse(e?.message || "signupFinish failed", 400);
    }
  }),
});

/** POST /auth/passkey/login/start — anonymous; returns assertion challenge. */
http.route({
  path: "/auth/passkey/login/start",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const origin = request.headers.get("Origin") || "";
    try {
      const options = await ctx.runAction(api.passkeys.loginStart, { origin });
      return jsonResponse({ options });
    } catch (e: any) {
      return errorResponse(e?.message || "loginStart failed", 400);
    }
  }),
});

/** POST /auth/passkey/login/finish — anonymous; mints session on success. */
http.route({
  path: "/auth/passkey/login/finish",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const body = await request.json().catch(() => ({} as any));
    if (!body?.response) return errorResponse("Missing response", 400);
    const origin = request.headers.get("Origin") || "";
    try {
      const result = await ctx.runAction(api.passkeys.loginFinish, {
        origin,
        response: body.response,
      });
      return jsonResponse(result);
    } catch (e: any) {
      return errorResponse(e?.message || "loginFinish failed", 401);
    }
  }),
});

/**
 * GET /auth/email-providers?email=X — anonymous lookup of which sign-in
 * methods are already attached to an email address. Used by mobile / web
 * after a signup collision (EMAIL_EXISTS) so the UI can route the user
 * to "Continue with Apple to link" instead of dead-ending at the error.
 *
 * Returns the same shape regardless of whether the email exists, so
 * timing differences are minimal and an attacker can't enumerate by
 * response shape. Existence + provider hints DO leak — same signal as
 * /auth/login wrong-password vs unknown-user, which is unavoidable.
 */
http.route({
  path: "/auth/email-providers",
  method: "GET",
  handler: httpAction(async (ctx, request) => {
    const url = new URL(request.url);
    const email = (url.searchParams.get("email") || "").trim().toLowerCase();
    const data = await ctx.runQuery(api.auth.lookupExistingProvidersByEmail, { email });
    return jsonResponse(data);
  }),
});

/**
 * POST /auth/verify-email/request — authenticated user asks for a
 * fresh verification email (re-send path or post-signup banner).
 */
http.route({
  path: "/auth/verify-email/request",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) return errorResponse("Unauthorized", 401);
    const tokenHash = await sha256Hex(authHeader.slice(7));
    try {
      const result = await ctx.runMutation(api.auth.requestEmailVerification, { tokenHash });
      return jsonResponse(result);
    } catch (e: any) {
      if (e?.message === "Unauthorized") return errorResponse("Unauthorized", 401);
      return errorResponse(e?.message || "Could not request verification email", 400);
    }
  }),
});

/**
 * POST /auth/verify-email/confirm — anonymous: consume the token from
 * the email link, flip users.emailVerified=true. Body: { token }.
 */
http.route({
  path: "/auth/verify-email/confirm",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const body = await request.json().catch(() => ({} as any));
    const token = String(body?.token || "");
    if (!token) return errorResponse("token required", 400);
    const result = await ctx.runMutation(api.auth.confirmEmailVerification, { token });
    return jsonResponse(result);
  }),
});

/**
 * GET /auth/passkey/check?email=X — anonymous preflight.
 *
 * iOS returns ASAuthorizationError.canceled (1001) for BOTH "user
 * dismissed the sheet" and "no credentials found for this rpId" — the
 * sheet never appears in the no-credentials case. Without a preflight
 * the mobile UI looks dead: tap → spinner → silent revert.
 *
 * Mobile calls this with the email the user typed so it can decide
 * whether to invoke the platform sheet at all. Returns:
 *   { hasPasskey: true,  emailRegistered: true  }  — passkey available
 *   { hasPasskey: false, emailRegistered: true  }  — OAuth/email user
 *                                                    needs to enroll first
 *   { hasPasskey: false, emailRegistered: false }  — brand-new email
 *
 * Reveals "this email is registered" to anonymous callers — but
 * /auth/passkey/signup/start and /auth/login already leak the same
 * signal (EMAIL_EXISTS / wrong-password), so net new surface is zero.
 */
http.route({
  path: "/auth/passkey/check",
  method: "GET",
  handler: httpAction(async (ctx, request) => {
    const url = new URL(request.url);
    const email = (url.searchParams.get("email") || "").trim().toLowerCase();
    if (!email || !email.includes("@")) {
      return jsonResponse({ hasPasskey: false, emailRegistered: false });
    }
    const result = await ctx.runQuery(api.passkeysDb.emailAvailable, { email });
    return jsonResponse({
      hasPasskey: result.available === false && result.hasPasskey === true,
      emailRegistered: result.available === false,
    });
  }),
});

/** GET /auth/passkey/list — signed-in user lists their passkeys. */
http.route({
  path: "/auth/passkey/list",
  method: "GET",
  handler: httpAction(async (ctx, request) => {
    const session = await authenticateRequest(ctx, request);
    if (!session) return errorResponse("Unauthorized", 401);
    const rows = await ctx.runQuery(api.passkeysDb.listForUser, {
      userId: session.userDocId as any,
    });
    return jsonResponse({ passkeys: rows });
  }),
});

/** POST /auth/passkey/remove — signed-in user revokes one of their passkeys. */
http.route({
  path: "/auth/passkey/remove",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const session = await authenticateRequest(ctx, request);
    if (!session) return errorResponse("Unauthorized", 401);
    const body = await request.json().catch(() => ({} as any));
    const credentialId = String(body?.credentialId || "").trim();
    if (!credentialId) return errorResponse("Missing credentialId", 400);
    try {
      const result = await ctx.runMutation(api.passkeysDb.removeCredential, {
        userId: session.userDocId as any,
        credentialId,
      });
      return jsonResponse(result);
    } catch (e: any) {
      return errorResponse(e?.message || "remove failed", 400);
    }
  }),
});

// ── Password Reset Endpoints ────────────────────────────────────────

/** POST /auth/forgot-password — Request a password reset email. */
http.route({
  path: "/auth/forgot-password",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const body = await request.json();
    const { email } = body;

    if (!email) return errorResponse("Email is required", 400);

    // Generate a random reset token
    const tokenBytes = new Uint8Array(32);
    crypto.getRandomValues(tokenBytes);
    const rawToken = Array.from(tokenBytes)
      .map((b) => b.toString(16).padStart(2, "0"))
      .join("");
    const tokenHash = await sha256Hex(rawToken);

    const result = await ctx.runMutation(api.auth.createPasswordReset, {
      email: email.toLowerCase().trim(),
      tokenHash,
    });

    // Send email only if a valid email user was found and not rate-limited
    if (result.sent) {
      const { passwordResetHtml } = await import("./email");
      const resetUrl = `https://yaver.io/auth/reset-password?token=${rawToken}`;
      await ctx.scheduler.runAfter(0, internal.email.send, {
        from: "Yaver <noreply@yaver.io>",
        to: email.toLowerCase().trim(),
        subject: "Reset your Yaver password",
        html: passwordResetHtml(resetUrl),
      });
    }

    // Always return success — don't reveal whether the email exists
    return jsonResponse({ ok: true });
  }),
});

/** POST /auth/reset-password — Set a new password using a reset token. */
http.route({
  path: "/auth/reset-password",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const body = await request.json();
    const { token, password } = body;

    if (!token || !password) {
      return errorResponse("Token and password are required", 400);
    }
    if (password.length < 8) {
      return errorResponse("Password must be at least 8 characters", 400);
    }

    const tokenHash = await sha256Hex(token);
    const newPasswordHash = await hashPassword(password);

    try {
      await ctx.runMutation(api.auth.resetPassword, {
        tokenHash,
        newPasswordHash,
      });
      return jsonResponse({ ok: true });
    } catch (e: any) {
      const msg = e.message || "";
      if (msg.includes("INVALID_TOKEN")) {
        return errorResponse("Invalid or expired reset link", 400);
      }
      if (msg.includes("TOKEN_USED")) {
        return errorResponse("This reset link has already been used", 400);
      }
      if (msg.includes("TOKEN_EXPIRED")) {
        return errorResponse("This reset link has expired", 400);
      }
      return errorResponse("Password reset failed", 500);
    }
  }),
});

/** POST /auth/change-password — Change password while logged in (email users only). */
http.route({
  path: "/auth/change-password",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) {
      return errorResponse("Unauthorized", 401);
    }
    const token = authHeader.slice(7);
    const tokenHash = await sha256Hex(token);

    const user = await ctx.runQuery(api.auth.validateSession, { tokenHash });
    if (!user) return errorResponse("Unauthorized", 401);

    const body = await request.json();
    const { currentPassword, newPassword } = body;

    if (!currentPassword || !newPassword) {
      return errorResponse("Current password and new password are required", 400);
    }
    if (newPassword.length < 8) {
      return errorResponse("New password must be at least 8 characters", 400);
    }

    // Look up the email user to get the password hash
    const emailUser = await ctx.runQuery(api.auth.lookupEmailUser, { email: user.email });
    if (!emailUser || !emailUser.passwordHash) {
      return errorResponse("Password change is only available for email accounts", 400);
    }

    // Verify current password
    const valid = await verifyPassword(currentPassword, emailUser.passwordHash);
    if (!valid) {
      return errorResponse("Current password is incorrect", 401);
    }

    const newPasswordHash = await hashPassword(newPassword);
    await ctx.runMutation(api.auth.changePassword, {
      tokenHash,
      newPasswordHash,
    });

    return jsonResponse({ ok: true });
  }),
});

// ── Survey Endpoints ────────────────────────────────────────────────

/** POST /survey/submit — Submit developer survey (authed). */
http.route({
  path: "/survey/submit",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) {
      return errorResponse("Unauthorized", 401);
    }
    const token = authHeader.slice(7);
    const tokenHash = await sha256Hex(token);

    const body = await request.json();
    try {
      await ctx.runMutation(api.survey.submitSurvey, {
        tokenHash,
        isDeveloper: body.isDeveloper ?? true,
        fullName: body.fullName,
        languages: body.languages,
        experienceLevel: body.experienceLevel,
        role: body.role,
        companySize: body.companySize,
        useCase: body.useCase,
      });
      return jsonResponse({ ok: true });
    } catch {
      return errorResponse("Failed to submit survey", 500);
    }
  }),
});

/** GET /survey — Get survey status (authed). */
http.route({
  path: "/survey",
  method: "GET",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) {
      return errorResponse("Unauthorized", 401);
    }
    const token = authHeader.slice(7);
    const tokenHash = await sha256Hex(token);

    const survey = await ctx.runQuery(api.survey.getSurvey, { tokenHash });
    if (!survey) return errorResponse("Unauthorized", 401);
    return jsonResponse(survey);
  }),
});

// ── Auth Endpoints (called by Next.js API routes) ────────────────────

/** POST /auth/upsert-user — Create or update a user (called from web server). */
http.route({
  path: "/auth/upsert-user",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const body = await request.json();
    const userId = await ctx.runMutation(api.auth.createOrUpdateUser, {
      email: body.email,
      fullName: body.fullName,
      provider: body.provider,
      providerId: body.providerId,
      avatarUrl: body.avatarUrl,
    });
    return jsonResponse({ userId });
  }),
});

/** GET /auth/providers — List linked sign-in methods for current account. */
http.route({
  path: "/auth/providers",
  method: "GET",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) return errorResponse("Unauthorized", 401);
    const tokenHash = await sha256Hex(authHeader.slice(7));
    const identities = await ctx.runQuery(api.auth.listAuthIdentities, { tokenHash });
    if (identities === null) return errorResponse("Unauthorized", 401);
    return jsonResponse({ identities });
  }),
});

/** POST /auth/oauth-link/start — Start linking another OAuth provider to this account. */
http.route({
  path: "/auth/oauth-link/start",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) return errorResponse("Unauthorized", 401);
    const tokenHash = await sha256Hex(authHeader.slice(7));
    const body = await request.json().catch(() => ({}));
    if (!body.provider) return errorResponse("provider required", 400);
    try {
      const result = await ctx.runMutation(api.auth.createOAuthLinkIntent, {
        tokenHash,
        provider: body.provider,
        client: body.client || "web",
        returnTo: body.returnTo || undefined,
      });
      return jsonResponse(result);
    } catch (e: any) {
      if (e?.message === "Unauthorized") return errorResponse("Unauthorized", 401);
      return errorResponse(e?.message || "Failed to start OAuth linking", 400);
    }
  }),
});

/** POST /auth/oauth-link/complete — Complete provider linking/merge using a one-time intent token. */
http.route({
  path: "/auth/oauth-link/complete",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const body = await request.json().catch(() => ({}));
    if (!body.linkToken || !body.provider || !body.providerId || !body.email) {
      return errorResponse("linkToken, provider, providerId, email required", 400);
    }
    try {
      const result = await ctx.runMutation(api.auth.completeOAuthLink, {
        linkToken: body.linkToken,
        provider: body.provider,
        providerId: body.providerId,
        email: body.email,
        fullName: body.fullName || "",
        avatarUrl: body.avatarUrl || undefined,
      });
      return jsonResponse(result);
    } catch (e: any) {
      if (e?.message === "INVALID_LINK_TOKEN") return errorResponse("Invalid or expired link token", 410);
      if (e?.message === "TARGET_USER_NOT_FOUND") return errorResponse("Target user not found", 404);
      if (e?.message === "IDENTITY_ALREADY_LINKED") return errorResponse("Identity already linked to another account", 409);
      return errorResponse(e?.message || "Failed to complete OAuth linking", 400);
    }
  }),
});

/** DELETE /auth/oauth-link/:provider — Unlink an OAuth provider from the current account. */
http.route({
  pathPrefix: "/auth/oauth-link/",
  method: "DELETE",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) return errorResponse("Unauthorized", 401);
    const tokenHash = await sha256Hex(authHeader.slice(7));
    const url = new URL(request.url);
    const provider = url.pathname.replace(/^.*\/auth\/oauth-link\//, "").trim();
    if (!provider || !["google", "microsoft", "apple", "github", "gitlab", "email"].includes(provider)) {
      return errorResponse("unknown provider", 400);
    }
    const body = await request.json().catch(() => ({}));
    try {
      const result = await ctx.runMutation(api.auth.unlinkAuthIdentity, {
        tokenHash,
        provider: provider as "google" | "microsoft" | "apple" | "github" | "gitlab" | "email",
        totpCode: typeof body?.totpCode === "string" ? body.totpCode : undefined,
      });
      if (!result.ok) {
        return errorResponse(result.reason || "not linked", 404);
      }
      return jsonResponse(result);
    } catch (e: any) {
      if (errorMessageIncludes(e, "Unauthorized")) return errorResponse("Unauthorized", 401);
      if (errorMessageIncludes(e, "ONLY_IDENTITY")) {
        return errorResponse("Refusing to unlink the only sign-in method — add another provider first.", 409);
      }
      if (errorMessageIncludes(e, "TOTP_REQUIRED")) {
        return errorResponse("TOTP code required (2FA is enabled on this account).", 412);
      }
      if (errorMessageIncludes(e, "INVALID_TOTP")) {
        return errorResponse("Invalid 2FA code.", 403);
      }
      return errorResponse(e?.message || "Failed to unlink provider", 400);
    }
  }),
});

/** POST /auth/account/merge/start — Target user starts a merge intent. */
http.route({
  path: "/auth/account/merge/start",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) return errorResponse("Unauthorized", 401);
    const tokenHash = await sha256Hex(authHeader.slice(7));
    const body = await request.json().catch(() => ({}));
    try {
      const result = await ctx.runMutation(api.auth.createAccountMergeIntent, {
        tokenHash,
        client: body?.client || undefined,
        totpCode: typeof body?.totpCode === "string" ? body.totpCode : undefined,
      });
      return jsonResponse(result);
    } catch (e: any) {
      if (errorMessageIncludes(e, "Unauthorized")) return errorResponse("Unauthorized", 401);
      if (errorMessageIncludes(e, "TOTP_REQUIRED")) {
        return errorResponse("TOTP code required (2FA is enabled on this account).", 412);
      }
      if (errorMessageIncludes(e, "INVALID_TOTP")) {
        return errorResponse("Invalid 2FA code.", 403);
      }
      if (errorMessageIncludes(e, "TOO_MANY_PENDING_MERGES")) {
        return errorResponse("Too many pending merge intents. Cancel an existing one first.", 429);
      }
      if (errorMessageIncludes(e, "MERGE_RATE_LIMIT")) {
        return errorResponse("Too many merge requests in the last hour. Try again later.", 429);
      }
      return errorResponse(e?.message || "Failed to start merge", 400);
    }
  }),
});

/** GET /auth/account/merge/status?token=... — Public status for approval-page UX. */
http.route({
  path: "/auth/account/merge/status",
  method: "GET",
  handler: httpAction(async (ctx, request) => {
    const url = new URL(request.url);
    const token = url.searchParams.get("token");
    if (!token) return errorResponse("token required", 400);
    const result = await ctx.runQuery(api.auth.getAccountMergeIntentStatus, { token });
    return jsonResponse(result);
  }),
});

/**
 * POST /auth/account/merge/complete
 *
 * Called from a session signed into the SOURCE account (the one that will
 * be merged away). Body: { mergeToken }. The Authorization header carries
 * the source's bearer token.
 */
http.route({
  path: "/auth/account/merge/complete",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) return errorResponse("Unauthorized", 401);
    const sourceTokenHash = await sha256Hex(authHeader.slice(7));
    const body = await request.json().catch(() => ({}));
    if (!body?.mergeToken) return errorResponse("mergeToken required", 400);
    try {
      const result = await ctx.runMutation(api.auth.completeAccountMerge, {
        mergeToken: body.mergeToken,
        sourceTokenHash,
        sourceTotpCode: typeof body?.sourceTotpCode === "string" ? body.sourceTotpCode : undefined,
      });
      return jsonResponse(result);
    } catch (e: any) {
      if (errorMessageIncludes(e, "Unauthorized")) return errorResponse("Unauthorized", 401);
      if (errorMessageIncludes(e, "INVALID_MERGE_TOKEN")) return errorResponse("Invalid merge token", 404);
      if (errorMessageIncludes(e, "MERGE_ALREADY_RESOLVED")) return errorResponse("Merge already completed or cancelled", 409);
      if (errorMessageIncludes(e, "MERGE_TOKEN_EXPIRED")) return errorResponse("Merge token expired", 410);
      if (errorMessageIncludes(e, "CANNOT_MERGE_SELF")) return errorResponse("Cannot merge an account into itself", 400);
      if (errorMessageIncludes(e, "TARGET_USER_NOT_FOUND")) return errorResponse("Target account no longer exists", 404);
      if (errorMessageIncludes(e, "TOTP_REQUIRED")) {
        return errorResponse("TOTP code required (source account has 2FA enabled).", 412);
      }
      if (errorMessageIncludes(e, "INVALID_TOTP")) {
        return errorResponse("Invalid 2FA code.", 403);
      }
      return errorResponse(e?.message || "Merge failed", 400);
    }
  }),
});

/** POST /auth/account/merge/cancel — target cancels a pending merge intent. */
http.route({
  path: "/auth/account/merge/cancel",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) return errorResponse("Unauthorized", 401);
    const tokenHash = await sha256Hex(authHeader.slice(7));
    const body = await request.json().catch(() => ({}));
    if (!body?.mergeToken) return errorResponse("mergeToken required", 400);
    try {
      const result = await ctx.runMutation(api.auth.cancelAccountMergeIntent, {
        tokenHash,
        mergeToken: body.mergeToken,
      });
      return jsonResponse(result);
    } catch (e: any) {
      if (e?.message === "Unauthorized") return errorResponse("Unauthorized", 401);
      return errorResponse(e?.message || "Failed to cancel merge", 400);
    }
  }),
});

/** POST /auth/create-session — Create a session (called from web server). */
http.route({
  path: "/auth/create-session",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const body = await request.json();
    const sessionId = await ctx.runMutation(api.auth.createSession, {
      tokenHash: body.tokenHash,
      userId: body.userId,
      deviceId: body.deviceId,
      expiresAt: body.expiresAt,
    });
    return jsonResponse({ sessionId });
  }),
});

/** POST /auth/test/oauth-signin — Test-only OAuth shortcut for mocked-provider CI. */
http.route({
  path: "/auth/test/oauth-signin",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    if (!parseBooleanEnv(process.env.TEST_MODE_ENABLED, false)) {
      return errorResponse("Not found", 404);
    }

    const body = await request.json().catch(() => ({}));
    if (!body?.provider || !body?.providerId || !body?.email) {
      return errorResponse("provider, providerId, email required", 400);
    }

    const provider = String(body.provider).trim();
    if (!["google", "microsoft", "apple", "github", "gitlab", "email"].includes(provider)) {
      return errorResponse("unknown provider", 400);
    }

    const userId = await ctx.runMutation(api.auth.createOrUpdateUser, {
      email: String(body.email).trim().toLowerCase(),
      fullName: typeof body.fullName === "string" ? body.fullName.trim() : "",
      provider: provider as "google" | "microsoft" | "apple" | "github" | "gitlab" | "email",
      providerId: String(body.providerId).trim(),
      avatarUrl: typeof body.avatarUrl === "string" ? body.avatarUrl : undefined,
    });

    const token = await createSessionToken(ctx, userId, typeof body.deviceId === "string" ? body.deviceId : undefined);
    return jsonResponse({ ok: true, token, userId: String(userId) });
  }),
});

// ── Profile Update Endpoint ──────────────────────────────────────────

/** POST /auth/update-profile — Update user profile (authed). */
http.route({
  path: "/auth/update-profile",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) {
      return errorResponse("Unauthorized", 401);
    }
    const token = authHeader.slice(7);
    const tokenHash = await sha256Hex(token);

    const body = await request.json();
    try {
      await ctx.runMutation(api.auth.updateProfile, {
        tokenHash,
        fullName: body.fullName,
      });
      return jsonResponse({ ok: true });
    } catch {
      return errorResponse("Failed to update profile", 500);
    }
  }),
});

// ── Auth Validation Endpoint ─────────────────────────────────────────

/** GET /auth/validate — Validate bearer token, return user info. */
http.route({
  path: "/auth/validate",
  method: "GET",
  handler: httpAction(async (ctx, request) => {
    const user = await authenticateRequest(ctx, request);
    if (!user) {
      return errorResponse("Unauthorized", 401);
    }

    // Also return team memberships (needed by multi-user agents)
    const authHeader = request.headers.get("Authorization")!;
    const tokenHash = await sha256Hex(authHeader.slice(7));
    const userDocId = await ctx.runQuery(api.auth.getUserDocId, { tokenHash });

    let teams: { teamId: string; role: string }[] = [];
    if (userDocId) {
      const userTeams = await ctx.runQuery(api.teams.getTeamsForUser, { userId: userDocId });
      teams = (userTeams || []).map((t: any) => ({ teamId: t.teamId, role: t.role }));
    }

    return jsonResponse({ user: { ...user, teams } });
  }),
});

// ── Token Refresh ────────────────────────────────────────────────────

/**
 * POST /auth/refresh — Extend session by 1 year + rotate the bearer
 * token so a leaked token only lives until the next daily refresh
 * (~24 h max blast radius). The agent writes the returned `token` back
 * to ~/.yaver/config.json atomically. Older agents that ignore the
 * `token` field still work — they just don't get the rotation benefit.
 *
 * Query param `?rotate=0` disables rotation for tooling that wants a
 * simple "extend only" refresh (rare).
 */
http.route({
  path: "/auth/refresh",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) {
      return errorResponse("Unauthorized", 401);
    }
    const token = authHeader.slice(7);
    const tokenHash = await sha256Hex(token);

    // Rotation is OPT-IN, not default-on. An older agent (<1.99.12)
    // that hits this endpoint calls POST /auth/refresh with no opt-in
    // signal and only reads {ok, expiresAt} from the response — if
    // we rotated unconditionally, the server would move to a new
    // tokenHash while the agent keeps writing the old one to disk,
    // locking that device out on the next call. Require explicit
    // opt-in via ?rotate=1 OR X-Yaver-Rotate-Token: 1 so only agents
    // that know how to persist the new token trigger a rotation.
    const url = new URL(request.url);
    const optInFromQuery = url.searchParams.get("rotate") === "1";
    const optInFromHeader = request.headers.get("X-Yaver-Rotate-Token") === "1";
    const wantRotation = optInFromQuery || optInFromHeader;

    let newToken: string | undefined;
    let newTokenHash: string | undefined;
    if (wantRotation) {
      const tokenBytes = new Uint8Array(32);
      crypto.getRandomValues(tokenBytes);
      newToken = Array.from(tokenBytes)
        .map((b) => b.toString(16).padStart(2, "0"))
        .join("");
      newTokenHash = await sha256Hex(newToken);
    }

    const result = await ctx.runMutation(api.auth.refreshSession, {
      tokenHash,
      newTokenHash,
    });
    if (!result) {
      return errorResponse("Session expired or invalid", 401);
    }
    return jsonResponse({
      ok: true,
      expiresAt: result.expiresAt,
      // Only surface the new token when the backend actually rotated
      // (guarded against hash collision). Otherwise omit so old agents
      // don't accidentally persist an identical value.
      token: result.rotated ? newToken : undefined,
      rotated: !!result.rotated,
    });
  }),
});

// ── Apple Sign-In ────────────────────────────────────────────────────

/** POST /auth/apple-native — Native iOS Apple Sign-In (receives identityToken). */
http.route({
  path: "/auth/apple-native",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const body = await request.json();
    const { identityToken, fullName } = body;

    if (!identityToken) {
      return errorResponse("Missing identityToken", 400);
    }

    // Decode Apple's identity token (JWT) to extract email and sub
    const parts = identityToken.split(".");
    if (parts.length !== 3) {
      return errorResponse("Invalid identityToken format", 400);
    }

    let payload: Record<string, unknown>;
    try {
      const decoded = atob(parts[1].replace(/-/g, "+").replace(/_/g, "/"));
      payload = JSON.parse(decoded);
    } catch {
      return errorResponse("Failed to decode identityToken", 400);
    }

    const email = payload.email as string;
    const sub = payload.sub as string;

    if (!email || !sub) {
      return errorResponse("Token missing email or sub", 400);
    }

    // Upsert user
    const userId = await ctx.runMutation(api.auth.createOrUpdateUser, {
      email: email.toLowerCase(),
      fullName: fullName || "",
      provider: "apple",
      providerId: sub,
    });

    // Check if 2FA is enabled
    const totpCheck = await ctx.runQuery(api.auth.getUserWithTotp, { userId });
    if (totpCheck?.totpEnabled) {
      const { pendingToken } = await ctx.runMutation(api.totp.createPendingAuth, { userId });
      return jsonResponse({ requires2fa: true, pendingToken });
    }

    // Create session
    const tokenBytes = new Uint8Array(32);
    crypto.getRandomValues(tokenBytes);
    const token = Array.from(tokenBytes)
      .map((b) => b.toString(16).padStart(2, "0"))
      .join("");

    const tokenHash = await sha256Hex(token);
    const expiresAt = Date.now() + 365 * 24 * 60 * 60 * 1000; // 30 days

    await ctx.runMutation(api.auth.createSession, {
      tokenHash,
      userId,
      deviceId: body.deviceId || undefined,
      expiresAt,
    });

    return jsonResponse({ token, userId });
  }),
});

/** POST /auth/apple-notifications — Apple sends account events here. */
http.route({
  path: "/auth/apple-notifications",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const body = await request.json();
    console.log("Apple notification received:", JSON.stringify(body));
    return new Response(null, { status: 200 });
  }),
});

// ── Device Endpoints ─────────────────────────────────────────────────

/** POST /devices/register — Register a device (authed). */
http.route({
  path: "/devices/register",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) {
      return errorResponse("Unauthorized", 401);
    }
    const token = authHeader.slice(7);
    const tokenHash = await sha256Hex(token);

    const body = await request.json();
    const deviceId = await ctx.runMutation(api.devices.registerDevice, {
      tokenHash,
      deviceId: body.deviceId,
      name: body.name,
      platform: body.platform,
      deviceClass: body.deviceClass || undefined,
      edgeProfile: body.edgeProfile || undefined,
      publicKey: body.publicKey || undefined,
      quicHost: body.quicHost,
      quicPort: body.quicPort,
      publicEndpoints: Array.isArray(body.publicEndpoints) ? body.publicEndpoints : undefined,
      hardwareId: body.hardwareId || undefined,
      hardwareProfile: body.hardwareProfile || undefined,
      recoveryPosture: body.recoveryPosture || undefined,
      connectionPreferences: Array.isArray(body.connectionPreferences)
        ? body.connectionPreferences
        : body.connectionPreferences === null
          ? []
          : undefined,
      agentVersion: typeof body.agentVersion === "string" ? body.agentVersion : undefined,
    });

    const session = await ctx.runQuery(api.auth.validateSession, { tokenHash });
    if (!session?.userDocId) {
      return errorResponse("Unauthorized", 401);
    }

    await ctx.runMutation(api.auth.deleteSessionsByDeviceId, {
      userId: session.userDocId,
      deviceId: body.deviceId,
    });
    const dedicatedToken = await createSessionToken(ctx, session.userDocId, body.deviceId);

    return jsonResponse({ deviceId, token: dedicatedToken });
  }),
});

/**
 * POST /devices/owner-by-hardware — Look up the owner of a
 * device by stable hardware fingerprint. Used by the agent's
 * /auth/recover handler to verify that a Convex bearer token
 * (presented by a mobile app trying to remote-reauth a box)
 * actually belongs to the same user that originally registered
 * this hardware. The agent makes the call on behalf of the
 * caller; we authenticate the bearer token here and reply with
 * a simple boolean + the device record.
 *
 * Body: { hardwareId: string }
 * Headers: Authorization: Bearer <caller's convex token>
 */
http.route({
  path: "/devices/owner-by-hardware",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) {
      return errorResponse("Unauthorized", 401);
    }
    const caller = await authenticateRequest(ctx, request);
    if (!caller) return errorResponse("Unauthorized", 401);

    const body = await request.json();
    if (!body.hardwareId || typeof body.hardwareId !== "string") {
      return errorResponse("hardwareId required", 400);
    }
    // Use caller-aware lookup so duplicate device rows with the same
    // hardwareId (test fixtures, prior owners, re-claims) don't shadow
    // the row the caller actually owns. The agent's verifyHostToken
    // (auth_recover.go) requires isOwner=true; with .first() on a
    // non-unique index this was deterministically returning whichever
    // row the index encountered first — frequently NOT the caller's,
    // breaking remote re-auth for users whose hardwareId is shared.
    // Guests still correctly fail isOwner here (they don't own any row
    // with this hwid) so security is unchanged.
    const owner = await ctx.runQuery(api.devices.ownerByHardwareIdForCaller, {
      hardwareId: body.hardwareId,
      callerUserId: caller.userDocId,
    });
    if (!owner) {
      return jsonResponse({ ok: true, isOwner: false, deviceFound: false });
    }
    return jsonResponse({
      ok: true,
      isOwner: String(owner.ownerUserId) === caller.userDocId,
      deviceFound: true,
      deviceId: owner.deviceId,
      name: owner.name,
      duplicateCount: owner.duplicateCount,
    });
  }),
});

/** POST /devices/bootstrap — Mark device as in bootstrap mode (no token; uses hardwareId+publicKey). */
http.route({
  path: "/devices/bootstrap",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const body = await request.json().catch(() => null);
    if (!body || !body.deviceId || !body.hardwareId || !body.publicKey) {
      return errorResponse("deviceId, hardwareId, publicKey required", 400);
    }
    try {
      const res = await ctx.runMutation(api.devices.markBootstrap, {
        deviceId: body.deviceId,
        hardwareId: body.hardwareId,
        publicKey: body.publicKey,
        quicHost: body.quicHost || undefined,
        quicPort: body.quicPort || undefined,
      });
      return jsonResponse({ ok: true, userId: res.userId });
    } catch (e: any) {
      // "Device not found" is the structured signal an agent uses to
      // decide whether to fall back to /devices/bootstrap-pending.
      // Return 404 (not 400) so the agent's retry path is unambiguous.
      if (errorMessageIncludes(e, "Device not found")) {
        return errorResponse("Device not found", 404);
      }
      return errorResponse(e?.message || "bootstrap failed", 400);
    }
  }),
});

/** POST /devices/bootstrap-pending — A truly-fresh box (no prior Convex
 *  row) registers itself for later claim by its relay-password-bearing
 *  user. The agent calls this only after /devices/bootstrap returned 404.
 *
 *  Why a separate endpoint: the original /devices/bootstrap requires a
 *  pre-existing devices row keyed by (deviceId, hardwareId, publicKey)
 *  for trust. There is no pre-existing row for a clean install, but we
 *  still need a way to make the box visible to ITS owner — the user
 *  whose relay password is configured on the box. We hash the relay
 *  password server-side and store it on a holding row in
 *  `pendingDeviceClaims`. The dashboard scopes its listing to the
 *  caller's managedRelay password hash, so only the rightful user
 *  ever sees the pending row.
 *
 *  Auth: relay password presence is the proof. We rely on the fact
 *  that the agent could only have registered its tunnel with the relay
 *  by also presenting that password — i.e. the password is already
 *  validated by the relay before this endpoint sees it. The user
 *  proves possession on their end by hashing managedRelays.password
 *  and matching it to relayPasswordHash on the row they want to claim.
 */
http.route({
  path: "/devices/bootstrap-pending",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const body = await request.json().catch(() => null);
    if (
      !body ||
      !body.deviceId ||
      !body.hardwareId ||
      !body.publicKey ||
      !body.relayPassword
    ) {
      return errorResponse(
        "deviceId, hardwareId, publicKey, relayPassword required",
        400,
      );
    }
    try {
      const relayPasswordHash = await sha256Hex(String(body.relayPassword));
      const result = await ctx.runMutation(internal.pendingDeviceClaims.createOrUpdate, {
        deviceId: String(body.deviceId),
        hardwareId: String(body.hardwareId),
        publicKey: String(body.publicKey),
        relayPasswordHash,
        name: typeof body.name === "string" ? body.name : undefined,
        platform: typeof body.platform === "string" ? body.platform : undefined,
        quicHost: typeof body.quicHost === "string" ? body.quicHost : undefined,
        quicPort: typeof body.quicPort === "number" ? body.quicPort : undefined,
        relayLabel: typeof body.relayLabel === "string" ? body.relayLabel : undefined,
      });
      // alreadyClaimed=true means the agent got confused: a real
      // devices row exists for this triple, the agent should have
      // succeeded on /devices/bootstrap. We answer 200 anyway so the
      // agent stops retrying, but flag it so log forwarders can spot
      // misrouted calls.
      return jsonResponse({
        ok: true,
        pending: !result.alreadyClaimed,
        alreadyClaimed: !!result.alreadyClaimed,
      });
    } catch (e: any) {
      return errorResponse(e?.message || "bootstrap-pending failed", 400);
    }
  }),
});

/**
 * POST /devices/provision-attest — zero-touch first-boot attestation.
 *
 * No bearer. The proof is cryptographic: the device signs
 * `provision-attest|<deviceId>|<timestampMs>` with the Ed25519 private key
 * baked into its SD seed at flash time, and presents the one-time
 * claimSecret. Convex verifies both against the public key + secret-hash
 * registered at mint time. Responses:
 *   200 {status:"active", token}   — proofs ok + a human has claimed it
 *   202 {status:"awaiting-claim"}  — proofs ok but unclaimed; keep polling
 *   403 {status:"revoked"}         — ownership reset; stop
 *   401 {status:"bad-*"|"stale"}   — proof failed
 *   404 {status:"not-found"}       — no such provisioned device
 * See provisioning.ts for the full trust model.
 */
http.route({
  path: "/devices/provision-attest",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const body = await request.json().catch(() => null);
    if (
      !body ||
      typeof body.deviceId !== "string" ||
      typeof body.claimSecret !== "string" ||
      typeof body.signature !== "string" ||
      typeof body.timestampMs !== "number"
    ) {
      return errorResponse(
        "deviceId, claimSecret, timestampMs, signature required",
        400,
      );
    }
    let result;
    try {
      result = await ctx.runMutation(internal.provisioning.attest, {
        deviceId: body.deviceId,
        claimSecret: body.claimSecret,
        timestampMs: body.timestampMs,
        signature: body.signature,
      });
    } catch (e: any) {
      return errorResponse(e?.message || "attest failed", 500);
    }
    switch (result.status) {
      case "active":
        return jsonResponse(result, 200);
      case "awaiting-claim":
        return jsonResponse(result, 202);
      case "revoked":
        return jsonResponse(result, 403);
      case "not-found":
        return jsonResponse(result, 404);
      default:
        // bad-secret / bad-signature / stale — don't leak which check
        // failed beyond the status string; all are 401.
        return jsonResponse(result, 401);
    }
  }),
});

/**
 * POST /devices/provision-claim — claim a provisioned device by scanned QR
 * (HTTP variant for CLI / non-Convex-client callers; the mobile/web apps
 * can also call the api.provisioning.claimProvisionedDevice mutation
 * directly). Bearer-authed; body carries the scanned {deviceId,
 * claimSecret} and an optional name.
 */
http.route({
  path: "/devices/provision-claim",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) {
      return errorResponse("Unauthorized", 401);
    }
    const tokenHash = await sha256Hex(authHeader.slice(7));
    const body = await request.json().catch(() => null);
    if (!body || typeof body.deviceId !== "string" || typeof body.claimSecret !== "string") {
      return errorResponse("deviceId, claimSecret required", 400);
    }
    try {
      const result = await ctx.runMutation(api.provisioning.claimProvisionedDevice, {
        tokenHash,
        deviceId: body.deviceId,
        claimSecret: body.claimSecret,
        name: typeof body.name === "string" ? body.name : undefined,
      });
      return jsonResponse(result, 200);
    } catch (e: any) {
      return errorResponse(e?.message || "claim failed", 400);
    }
  }),
});

/**
 * POST /devices/provision-mint — register a flash-time device identity
 * (builder/manufacturer side). Bearer-authed. The CLI generates the
 * Ed25519 keypair + claimSecret locally and sends only the PUBLIC key +
 * the claimSecret HASH; Convex never sees the private key or raw secret.
 */
http.route({
  path: "/devices/provision-mint",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) {
      return errorResponse("Unauthorized", 401);
    }
    const tokenHash = await sha256Hex(authHeader.slice(7));
    const body = await request.json().catch(() => null);
    if (
      !body ||
      typeof body.deviceId !== "string" ||
      typeof body.publicKey !== "string" ||
      typeof body.claimSecretHash !== "string"
    ) {
      return errorResponse("deviceId, publicKey, claimSecretHash required", 400);
    }
    try {
      const result = await ctx.runMutation(api.provisioning.mintProvisionedDevice, {
        tokenHash,
        deviceId: body.deviceId,
        publicKey: body.publicKey,
        claimSecretHash: body.claimSecretHash,
        productId: typeof body.productId === "string" ? body.productId : undefined,
        name: typeof body.name === "string" ? body.name : undefined,
        platform: typeof body.platform === "string" ? body.platform : undefined,
      });
      return jsonResponse(result, 200);
    } catch (e: any) {
      return errorResponse(e?.message || "mint failed", 400);
    }
  }),
});

/** POST /devices/provision-register-product — builder declares a SKU. */
http.route({
  path: "/devices/provision-register-product",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) {
      return errorResponse("Unauthorized", 401);
    }
    const tokenHash = await sha256Hex(authHeader.slice(7));
    const body = await request.json().catch(() => null);
    if (!body || typeof body.productId !== "string" || typeof body.name !== "string") {
      return errorResponse("productId, name required", 400);
    }
    try {
      const result = await ctx.runMutation(api.provisioning.registerProduct, {
        tokenHash,
        productId: body.productId,
        name: body.name,
        vendor: typeof body.vendor === "string" ? body.vendor : undefined,
        defaultServices: Array.isArray(body.defaultServices) ? body.defaultServices : undefined,
      });
      return jsonResponse(result, 200);
    } catch (e: any) {
      return errorResponse(e?.message || "register-product failed", 400);
    }
  }),
});

/** POST /devices/heartbeat — Device heartbeat (authed). */
http.route({
  path: "/devices/heartbeat",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) {
      return errorResponse("Unauthorized", 401);
    }
    const token = authHeader.slice(7);
    const tokenHash = await sha256Hex(token);

    // Validate session at HTTP layer — return 401, not 500
    const user = await authenticateRequest(ctx, request);
    if (!user) {
      return errorResponse("Session expired", 401);
    }

    // Auto-extend session on heartbeat (keeps CLI sessions alive indefinitely)
    await ctx.runMutation(api.auth.refreshSession, { tokenHash }).catch(() => {});

    const body = await request.json();
    const heartbeatResult = await ctx.runMutation(api.devices.heartbeat, {
      tokenHash,
      deviceId: body.deviceId,
      runners: body.runners,
      installedRunnerIds: Array.isArray(body.installedRunnerIds)
        ? body.installedRunnerIds
        : body.installedRunnerIds === null
          ? []
          : undefined,
      // Pass quicHost as-is (including ""). The mutation now treats "" as
      // a deliberate clear (e.g. an upgraded agent retracting a stale
      // Docker-bridge address). Pre-fix `body.quicHost || undefined`
      // collapsed empty-string to undefined, leaving the stale value in
      // the DB forever.
      quicHost: typeof body.quicHost === "string" ? body.quicHost : undefined,
      // Multi-IP rollout: the agent advertises every reachable IPv4 it has
      // (Wi-Fi LAN, Tailscale 100.x, Ethernet, VPNs) so the mobile connect
      // path can race them in parallel. Older agents don't send the field
      // at all — then undefined is correct and the mutation leaves the
      // stored list untouched.
      // Treat both [] and null as deliberate clear. Pre-fix only Array
      // values made it through, so a Go agent sending nil-slice → JSON
      // null was silently ignored, leaving stale Docker-bridge IPs on
      // the device row across upgrades.
      localIps: Array.isArray(body.localIps)
        ? body.localIps
        : body.localIps === null
          ? []
          : undefined,
      publicEndpoints: Array.isArray(body.publicEndpoints)
        ? body.publicEndpoints
        : body.publicEndpoints === null
          ? []
          : undefined,
      hardwareId: body.hardwareId || undefined,
      hardwareProfile: body.hardwareProfile || undefined,
      deviceClass: body.deviceClass || undefined,
      geoRegion: typeof body.geoRegion === "string" ? body.geoRegion : undefined,
      edgeProfile: body.edgeProfile || undefined,
      recoveryPosture: body.recoveryPosture || undefined,
      agentVersion: typeof body.agentVersion === "string" ? body.agentVersion : undefined,
      publishCapabilities: Array.isArray(body.publishCapabilities)
        ? body.publishCapabilities
        : body.publishCapabilities === null
          ? []
          : undefined,
      // Batched CPU/RAM samples folded into the heartbeat (replaces the
      // separate /devices/metrics 60s poll). Validated by the mutation's
      // array schema; only forwarded when it's actually an array.
      metricsSamples: Array.isArray(body.metricsSamples) ? body.metricsSamples : undefined,
    });

    return jsonResponse({
      ok: true,
      connectionPreferences: heartbeatResult?.connectionPreferences ?? [],
      // Tell the agent whether to bother polling the claim endpoints this
      // cycle — a quiet box then makes zero rescue/publish claim calls.
      pendingRescue: heartbeatResult?.pendingRescue ?? false,
      pendingPublish: heartbeatResult?.pendingPublish ?? false,
    });
  }),
});

/** POST /agent-rescue/queue — Web/mobile/CLI: queue a rescue command
 *  for a wedged device. Owner-only. Returns the existing pending row
 *  if one with the same (deviceId, command) is still alive (5-min
 *  TTL) so impatient double-clicks don't pile up restarts. */
http.route({
  path: "/agent-rescue/queue",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) {
      return errorResponse("Unauthorized", 401);
    }
    const token = authHeader.slice(7);
    const tokenHash = await sha256Hex(token);
    const body = await request.json().catch(() => null);
    if (!body || typeof body.deviceId !== "string" || typeof body.command !== "string") {
      return errorResponse("deviceId + command required", 400);
    }
    if (!["restart", "reinstall-latest", "tunnel-reset", "auth-reset"].includes(body.command)) {
      return errorResponse("unknown command", 400);
    }
    try {
      const out = await ctx.runMutation(api.agentRescue.queueRescueCommand, {
        tokenHash,
        deviceId: body.deviceId,
        command: body.command,
        params: body.params,
        sourceSurface: typeof body.sourceSurface === "string" ? body.sourceSurface : undefined,
      });
      return jsonResponse({ ...out, ok: true });
    } catch (e: any) {
      return errorResponse(e?.message || "queue failed", 400);
    }
  }),
});

/** POST /agent-rescue/claim — Agent: pull next pending command for
 *  its own device. Atomically transitions pending → claimed. The
 *  agent calls this from its heartbeat loop; null result is the
 *  steady state (nothing to do). */
http.route({
  path: "/agent-rescue/claim",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) {
      return errorResponse("Unauthorized", 401);
    }
    const token = authHeader.slice(7);
    const tokenHash = await sha256Hex(token);
    const body = await request.json().catch(() => null);
    if (!body || typeof body.deviceId !== "string") {
      return errorResponse("deviceId required", 400);
    }
    try {
      const out = await ctx.runMutation(api.agentRescue.claimNextRescueCommand, {
        tokenHash,
        deviceId: body.deviceId,
      });
      return jsonResponse({ ok: true, command: out });
    } catch (e: any) {
      return errorResponse(e?.message || "claim failed", 400);
    }
  }),
});

/** POST /agent-rescue/report — Agent: report claimed command's
 *  outcome (completed/failed + short result tail). Idempotent —
 *  retries after a network hiccup are safe. */
http.route({
  path: "/agent-rescue/report",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) {
      return errorResponse("Unauthorized", 401);
    }
    const token = authHeader.slice(7);
    const tokenHash = await sha256Hex(token);
    const body = await request.json().catch(() => null);
    if (!body || typeof body.commandId !== "string" || typeof body.status !== "string") {
      return errorResponse("commandId + status required", 400);
    }
    if (!["completed", "failed"].includes(body.status)) {
      return errorResponse("status must be completed|failed", 400);
    }
    try {
      const out = await ctx.runMutation(api.agentRescue.reportRescueResult, {
        tokenHash,
        commandId: body.commandId as any,
        status: body.status,
        result: typeof body.result === "string" ? body.result.slice(0, 2048) : undefined,
      });
      return jsonResponse({ ...out, ok: true });
    } catch (e: any) {
      return errorResponse(e?.message || "report failed", 400);
    }
  }),
});

/** GET /agent-rescue/list?deviceId=... — UI: list recent rescue
 *  commands for a device. Owner-only. */
http.route({
  path: "/agent-rescue/list",
  method: "GET",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) {
      return errorResponse("Unauthorized", 401);
    }
    const token = authHeader.slice(7);
    const tokenHash = await sha256Hex(token);
    const url = new URL(request.url);
    const deviceId = url.searchParams.get("deviceId") || "";
    if (!deviceId) {
      return errorResponse("deviceId required", 400);
    }
    const limitParam = url.searchParams.get("limit");
    const limit = limitParam ? Math.max(1, Math.min(parseInt(limitParam, 10) || 10, 50)) : 10;
    try {
      const rows = await ctx.runQuery(api.agentRescue.listRescueCommandsForDevice, {
        tokenHash,
        deviceId,
        limit,
      });
      return jsonResponse({ ok: true, commands: rows ?? [] });
    } catch (e: any) {
      return errorResponse(e?.message || "list failed", 400);
    }
  }),
});

// ── Publish-job queue (Phase 2 — async "tap Publish, walk away"). ──
// Same shape as /agent-rescue/* above, pairing with publishJobs.ts +
// desktop/agent/publish_worker.go. Privacy: app NAME + targets only,
// never a path or build log.

/** POST /publish-jobs/queue — CLI/mobile/web: enqueue a publish for a
 *  Mac-farm node. Owner-only. Returns the existing job if an
 *  identical one is still in flight. */
http.route({
  path: "/publish-jobs/queue",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) {
      return errorResponse("Unauthorized", 401);
    }
    const token = authHeader.slice(7);
    const tokenHash = await sha256Hex(token);
    const body = await request.json().catch(() => null);
    if (
      !body ||
      typeof body.deviceId !== "string" ||
      typeof body.app !== "string" ||
      typeof body.stack !== "string" ||
      !Array.isArray(body.targets)
    ) {
      return errorResponse("deviceId + app + stack + targets[] required", 400);
    }
    try {
      const out = await ctx.runMutation(api.publishJobs.queuePublishJob, {
        tokenHash,
        deviceId: body.deviceId,
        app: body.app,
        stack: body.stack,
        targets: body.targets.map((t: any) => String(t)),
        sourceSurface:
          typeof body.sourceSurface === "string" ? body.sourceSurface : undefined,
      });
      return jsonResponse({ ...out, ok: true });
    } catch (e: any) {
      return errorResponse(e?.message || "queue failed", 400);
    }
  }),
});

/** POST /publish-jobs/claim — Farm node: pull next queued job for its
 *  own device. Atomic queued → claimed. Called from the heartbeat
 *  loop; null is the steady state. */
http.route({
  path: "/publish-jobs/claim",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) {
      return errorResponse("Unauthorized", 401);
    }
    const token = authHeader.slice(7);
    const tokenHash = await sha256Hex(token);
    const body = await request.json().catch(() => null);
    if (!body || typeof body.deviceId !== "string") {
      return errorResponse("deviceId required", 400);
    }
    try {
      const out = await ctx.runMutation(api.publishJobs.claimNextPublishJob, {
        tokenHash,
        deviceId: body.deviceId,
      });
      return jsonResponse({ ok: true, job: out });
    } catch (e: any) {
      return errorResponse(e?.message || "claim failed", 400);
    }
  }),
});

/** POST /publish-jobs/progress — Farm node: keep a long build alive
 *  (refreshes lastProgressAt; claimed → running). Short message only,
 *  never log output. */
http.route({
  path: "/publish-jobs/progress",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) {
      return errorResponse("Unauthorized", 401);
    }
    const token = authHeader.slice(7);
    const tokenHash = await sha256Hex(token);
    const body = await request.json().catch(() => null);
    if (!body || typeof body.jobId !== "string") {
      return errorResponse("jobId required", 400);
    }
    try {
      const out = await ctx.runMutation(api.publishJobs.reportPublishJobProgress, {
        tokenHash,
        jobId: body.jobId,
        message:
          typeof body.message === "string" ? body.message.slice(0, 200) : undefined,
      });
      return jsonResponse({ ...out, ok: true });
    } catch (e: any) {
      return errorResponse(e?.message || "progress failed", 400);
    }
  }),
});

/** POST /publish-jobs/report — Farm node: terminal outcome
 *  (done|failed + per-target metadata). Idempotent. */
http.route({
  path: "/publish-jobs/report",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) {
      return errorResponse("Unauthorized", 401);
    }
    const token = authHeader.slice(7);
    const tokenHash = await sha256Hex(token);
    const body = await request.json().catch(() => null);
    if (!body || typeof body.jobId !== "string" || typeof body.status !== "string") {
      return errorResponse("jobId + status required", 400);
    }
    if (!["done", "failed"].includes(body.status)) {
      return errorResponse("status must be done|failed", 400);
    }
    try {
      const out = await ctx.runMutation(api.publishJobs.reportPublishJobResult, {
        tokenHash,
        jobId: body.jobId,
        status: body.status,
        result: Array.isArray(body.result) ? body.result : undefined,
        message:
          typeof body.message === "string" ? body.message.slice(0, 500) : undefined,
      });
      return jsonResponse({ ...out, ok: true });
    } catch (e: any) {
      return errorResponse(e?.message || "report failed", 400);
    }
  }),
});

/** GET /publish-jobs/list?deviceId=... — UI/CLI: recent publish jobs
 *  for the caller. Owner-only. */
http.route({
  path: "/publish-jobs/list",
  method: "GET",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) {
      return errorResponse("Unauthorized", 401);
    }
    const token = authHeader.slice(7);
    const tokenHash = await sha256Hex(token);
    const url = new URL(request.url);
    const deviceId = url.searchParams.get("deviceId") || undefined;
    const limitParam = url.searchParams.get("limit");
    const limit = limitParam
      ? Math.max(1, Math.min(parseInt(limitParam, 10) || 20, 100))
      : 20;
    try {
      const rows = await ctx.runQuery(api.publishJobs.listPublishJobsForOwner, {
        tokenHash,
        deviceId,
        limit,
      });
      return jsonResponse({ ok: true, jobs: rows ?? [] });
    } catch (e: any) {
      return errorResponse(e?.message || "list failed", 400);
    }
  }),
});

/** POST /devices/report-version — Owner-side seed for agentVersion.
 *
 * The dashboard probes /info on a device it can reach, observes the
 * `version` string, and pushes it here so Convex has the value even
 * before the agent itself is upgraded to a build that sends it in
 * register/heartbeat.
 */
http.route({
  path: "/devices/report-version",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) {
      return errorResponse("Unauthorized", 401);
    }
    const token = authHeader.slice(7);
    const tokenHash = await sha256Hex(token);
    const body = await request.json().catch(() => null);
    if (
      !body ||
      typeof body.deviceId !== "string" ||
      typeof body.agentVersion !== "string" ||
      !body.agentVersion.trim()
    ) {
      return errorResponse("deviceId + agentVersion required", 400);
    }
    try {
      await ctx.runMutation(api.devices.reportAgentVersion, {
        tokenHash,
        deviceId: body.deviceId,
        agentVersion: body.agentVersion,
      });
    } catch (e: any) {
      return errorResponse(e?.message || "report failed", 400);
    }
    return jsonResponse({ ok: true });
  }),
});

/** POST /devices/presence — Relay-driven tunnel-up/down notifier.
 *
 * Called by the Yaver relay binary when a device's QUIC tunnel opens or
 * closes. Lets the reactive Convex UI flip online/offline within ~100ms
 * of the event, without waiting for the device's 30s heartbeat.
 *
 * Auth is a shared secret header (X-Relay-Secret) matched against
 * `platformConfig["relay_presence_secret"]`. The relay is trusted
 * platform infrastructure; this is distinct from user session auth.
 * Dropping the platformConfig entry disables the endpoint (every call
 * returns 401). Rotation = write a new entry + update the relay's
 * CONVEX_PRESENCE_SECRET env var.
 */
http.route({
  path: "/devices/presence",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const provided = request.headers.get("X-Relay-Secret") ?? "";
    if (!provided) return errorResponse("Missing X-Relay-Secret", 401);
    const expected = await ctx.runQuery(api.platformConfig.get, {
      key: "relay_presence_secret",
    });
    if (!expected) {
      // Feature not enabled on this deployment — opt in by writing the
      // secret via `npx convex run platformConfig:set --key=relay_presence_secret --value=<secret>`.
      return errorResponse("Presence endpoint disabled", 404);
    }
    if (provided !== expected) {
      return errorResponse("Bad relay secret", 401);
    }
    const body = await request.json().catch(() => ({}));
    if (!body?.deviceId || typeof body.deviceId !== "string") {
      return errorResponse("deviceId required", 400);
    }
    await ctx.runMutation(api.devices.presenceUpdate, {
      deviceId: body.deviceId,
      online: body.online === true,
      peerAddr: typeof body.peerAddr === "string" ? body.peerAddr : undefined,
      connectedAt: typeof body.connectedAt === "number" ? body.connectedAt : undefined,
      durationSec: typeof body.durationSec === "number" ? body.durationSec : undefined,
      // Relay-auto-provisioned <id>.dev.yaver.io URL. Stored in
      // device.publicEndpoints so the dashboard's transport
      // classifier picks it instantly (no waiting for next agent
      // heartbeat). Only sent on online=true presence pushes.
      assignedUrl: typeof body.assignedUrl === "string" ? body.assignedUrl : undefined,
    });
    return jsonResponse({ ok: true });
  }),
});

/** POST /devices/placement/recommend — Recommend edge vs infra placement for a task. */
http.route({
  path: "/devices/placement/recommend",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) {
      return errorResponse("Unauthorized", 401);
    }
    const token = authHeader.slice(7);
    const tokenHash = await sha256Hex(token);
    const body = await request.json().catch(() => null);
    if (!body?.taskKind || typeof body.taskKind !== "string") {
      return errorResponse("taskKind required", 400);
    }
    try {
      const recommendation = await ctx.runQuery((api as any).devices.recommendTaskPlacement, {
        tokenHash,
        taskKind: body.taskKind,
      });
      return jsonResponse(recommendation);
    } catch (e: any) {
      return errorResponse(e?.message || "could not recommend placement", 400);
    }
  }),
});

/** GET /devices/list — List user's devices (authed). */
http.route({
  path: "/devices/list",
  method: "GET",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) {
      return errorResponse("Unauthorized", 401);
    }
    const token = authHeader.slice(7);
    const tokenHash = await sha256Hex(token);

    // listMyDevices THROWS "Unauthorized" when the session token is
    // stale/rotated/revoked. An uncaught throw in an httpAction returns
    // HTTP 500 — which clients (mobile, web) treat as a transient server
    // error and silently swallow, leaving the user "signed in" but with
    // an empty device list and no hint to re-auth (a registered Mac goes
    // invisible on the phone). Map the auth throw to a real 401 so every
    // client can route it through its token-refresh / sign-in recovery.
    let devices;
    try {
      devices = await ctx.runQuery(api.devices.listMyDevices, { tokenHash });
    } catch (err) {
      const msg = err instanceof Error ? err.message : String(err);
      if (msg.includes("Unauthorized")) {
        return errorResponse("Unauthorized", 401);
      }
      throw err;
    }

    return jsonResponse({ devices });
  }),
});

// --- Yaver Mesh console routes (web/mobile) — session-token-hash auth ---

http.route({
  path: "/mesh/peers",
  method: "GET",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) return errorResponse("Unauthorized", 401);
    const tokenHash = await sha256Hex(authHeader.slice(7));
    try {
      const res = await ctx.runQuery(api.mesh.listMeshPeersWeb, { tokenHash });
      return jsonResponse(res);
    } catch (err) {
      const msg = err instanceof Error ? err.message : String(err);
      return errorResponse(msg.includes("Unauthorized") ? "Unauthorized" : msg, msg.includes("Unauthorized") ? 401 : 500);
    }
  }),
});

http.route({
  path: "/mesh/acls",
  method: "GET",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) return errorResponse("Unauthorized", 401);
    const tokenHash = await sha256Hex(authHeader.slice(7));
    try {
      const rules = await ctx.runQuery(api.mesh.listMeshAclsWeb, { tokenHash });
      return jsonResponse({ rules });
    } catch (err) {
      const msg = err instanceof Error ? err.message : String(err);
      return errorResponse(msg.includes("Unauthorized") ? "Unauthorized" : msg, msg.includes("Unauthorized") ? 401 : 500);
    }
  }),
});

http.route({
  path: "/mesh/acls/set",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) return errorResponse("Unauthorized", 401);
    const tokenHash = await sha256Hex(authHeader.slice(7));
    const body = await request.json().catch(() => ({}));
    try {
      await ctx.runMutation(api.mesh.setMeshAclsWeb, { tokenHash, rules: body.rules ?? [] });
      return jsonResponse({ ok: true });
    } catch (err) {
      const msg = err instanceof Error ? err.message : String(err);
      return errorResponse(msg.includes("Unauthorized") ? "Unauthorized" : msg, msg.includes("Unauthorized") ? 401 : 500);
    }
  }),
});

http.route({
  path: "/mesh/tags",
  method: "GET",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) return errorResponse("Unauthorized", 401);
    const tokenHash = await sha256Hex(authHeader.slice(7));
    try {
      const tags = await ctx.runQuery(api.mesh.listMeshTagsWeb, { tokenHash });
      return jsonResponse({ tags });
    } catch (err) {
      const msg = err instanceof Error ? err.message : String(err);
      return errorResponse(msg.includes("Unauthorized") ? "Unauthorized" : msg, msg.includes("Unauthorized") ? 401 : 500);
    }
  }),
});

http.route({
  path: "/mesh/tags/set",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) return errorResponse("Unauthorized", 401);
    const tokenHash = await sha256Hex(authHeader.slice(7));
    const body = await request.json().catch(() => ({}));
    try {
      await ctx.runMutation(api.mesh.tagDeviceWeb, { tokenHash, deviceId: body.deviceId ?? "", tags: body.tags ?? [] });
      return jsonResponse({ ok: true });
    } catch (err) {
      const msg = err instanceof Error ? err.message : String(err);
      return errorResponse(msg.includes("Unauthorized") ? "Unauthorized" : msg, msg.includes("Unauthorized") ? 401 : 500);
    }
  }),
});

http.route({
  path: "/mesh/join",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) return errorResponse("Unauthorized", 401);
    const tokenHash = await sha256Hex(authHeader.slice(7));
    const body = await request.json().catch(() => ({}));
    try {
      const res = await ctx.runMutation(api.mesh.joinMeshWeb, {
        tokenHash,
        deviceId: body.deviceId ?? "",
        wgPublicKey: body.wgPublicKey ?? "",
        endpoints: body.endpoints ?? [],
        meshIPv6: body.meshIPv6,
        advertisedRoutes: body.advertisedRoutes,
        isExitNode: body.isExitNode,
      });
      return jsonResponse(res);
    } catch (err) {
      const msg = err instanceof Error ? err.message : String(err);
      return errorResponse(msg, msg.includes("Unauthorized") ? 401 : 500);
    }
  }),
});

http.route({
  path: "/mesh/leave",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) return errorResponse("Unauthorized", 401);
    const tokenHash = await sha256Hex(authHeader.slice(7));
    const body = await request.json().catch(() => ({}));
    try {
      await ctx.runMutation(api.mesh.leaveMeshWeb, { tokenHash, deviceId: body.deviceId ?? "" });
      return jsonResponse({ ok: true });
    } catch (err) {
      const msg = err instanceof Error ? err.message : String(err);
      return errorResponse(msg, msg.includes("Unauthorized") ? 401 : 500);
    }
  }),
});

// --- Yaver Support Link routes (web/landing) ---

http.route({
  path: "/support/invite",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) return errorResponse("Unauthorized", 401);
    const tokenHash = await sha256Hex(authHeader.slice(7));
    const body = await request.json().catch(() => ({}));
    try {
      const res = await ctx.runMutation(api.support_link.createSupportInviteWeb, {
        tokenHash,
        offerTerminal: body.offerTerminal,
        offerDesktopControl: body.offerDesktopControl,
        defaultTtlHours: body.defaultTtlHours,
        label: body.label,
        singleUse: body.singleUse,
      });
      return jsonResponse(res);
    } catch (err) {
      const msg = err instanceof Error ? err.message : String(err);
      return errorResponse(msg, msg.includes("Unauthorized") ? 401 : 500);
    }
  }),
});

// Public (no auth) — the landing/consent page reads invite metadata before the
// friend signs in. Returns only the inviter's display identity + offered scope.
http.route({
  path: "/support/invite/info",
  method: "GET",
  handler: httpAction(async (ctx, request) => {
    const code = new URL(request.url).searchParams.get("code") ?? "";
    if (!code) return errorResponse("code required", 400);
    const res = await ctx.runQuery(api.support_link.getSupportInviteInfo, { code });
    return jsonResponse(res);
  }),
});

http.route({
  path: "/support/connections",
  method: "GET",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) return errorResponse("Unauthorized", 401);
    const tokenHash = await sha256Hex(authHeader.slice(7));
    try {
      const res = await ctx.runQuery(api.support_link.listSupportConnectionsWeb, { tokenHash });
      return jsonResponse(res);
    } catch (err) {
      const msg = err instanceof Error ? err.message : String(err);
      return errorResponse(msg, msg.includes("Unauthorized") ? 401 : 500);
    }
  }),
});

http.route({
  path: "/support/grant/revoke",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) return errorResponse("Unauthorized", 401);
    const tokenHash = await sha256Hex(authHeader.slice(7));
    const body = await request.json().catch(() => ({}));
    try {
      await ctx.runMutation(api.support_link.revokeSupportGrantWeb, { tokenHash, grantId: body.grantId });
      return jsonResponse({ ok: true });
    } catch (err) {
      const msg = err instanceof Error ? err.message : String(err);
      const code = msg.includes("Unauthorized") ? 401 : msg.includes("Forbidden") ? 403 : 500;
      return errorResponse(msg, code);
    }
  }),
});

http.route({
  path: "/support/deny-all",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) return errorResponse("Unauthorized", 401);
    const tokenHash = await sha256Hex(authHeader.slice(7));
    try {
      const res = await ctx.runMutation(api.support_link.denyAllSupportWeb, { tokenHash });
      return jsonResponse(res);
    } catch (err) {
      const msg = err instanceof Error ? err.message : String(err);
      return errorResponse(msg, msg.includes("Unauthorized") ? 401 : 500);
    }
  }),
});

http.route({
  path: "/mesh/node/config",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) return errorResponse("Unauthorized", 401);
    const tokenHash = await sha256Hex(authHeader.slice(7));
    const body = await request.json().catch(() => ({}));
    try {
      await ctx.runMutation(api.mesh.setMeshNodeConfigWeb, {
        tokenHash,
        deviceId: body.deviceId ?? "",
        wantEnabled: body.wantEnabled,
        wantExitNode: body.wantExitNode,
        wantUseExitNode: body.wantUseExitNode,
        wantRoutes: body.wantRoutes,
      });
      return jsonResponse({ ok: true });
    } catch (err) {
      const msg = err instanceof Error ? err.message : String(err);
      const code = msg.includes("Unauthorized") ? 401 : msg.includes("Forbidden") ? 403 : 500;
      return errorResponse(msg, code);
    }
  }),
});

/** GET /devices/pending-list — list bootstrap-pending claims that came
 *  in via the caller's managed-relay password. Returns [] when the
 *  user has no managed relay or no pending claims. The dashboard polls
 *  this alongside /devices/list and surfaces "pending" rows so a
 *  freshly-installed remote box becomes claimable in one tap. */
http.route({
  path: "/devices/pending-list",
  method: "GET",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) {
      return errorResponse("Unauthorized", 401);
    }
    const token = authHeader.slice(7);
    const tokenHash = await sha256Hex(token);
    const result = await ctx.runQuery(api.pendingDeviceClaims.listForUser, {
      tokenHash,
    });
    return jsonResponse(result);
  }),
});

/** POST /devices/pending-claim — claim a bootstrap-pending box. The
 *  agent's relay-password hash must match the caller's managedRelay
 *  password hash; on success, a real `devices` row is created with
 *  needsAuth=true so the existing reauth/owner-claim UI can take over
 *  and finish the pairing handshake. */
http.route({
  path: "/devices/pending-claim",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) {
      return errorResponse("Unauthorized", 401);
    }
    const token = authHeader.slice(7);
    const tokenHash = await sha256Hex(token);
    const body = await request.json().catch(() => null);
    if (!body || !body.deviceId) {
      return errorResponse("deviceId required", 400);
    }
    try {
      const result = await ctx.runMutation(api.pendingDeviceClaims.claim, {
        tokenHash,
        deviceId: String(body.deviceId),
        name: typeof body.name === "string" ? body.name : undefined,
      });
      return jsonResponse(result);
    } catch (e: any) {
      const msg = e?.message || "claim failed";
      // Map known shapes to clean status codes so the dashboard can
      // distinguish 404 (no row) from 403 (relay-password mismatch)
      // from 409 (already owned by another user).
      if (errorMessageIncludes(e, "no pending claim")) return errorResponse(msg, 404);
      if (errorMessageIncludes(e, "another user")) return errorResponse(msg, 409);
      if (errorMessageIncludes(e, "Unauthorized")) return errorResponse(msg, 401);
      if (errorMessageIncludes(e, "relay-password mismatch")) return errorResponse(msg, 403);
      if (errorMessageIncludes(e, "no managed relay")) return errorResponse(msg, 403);
      return errorResponse(msg, 400);
    }
  }),
});

/** POST /devices/offline — Mark device offline (authed). */
http.route({
  path: "/devices/offline",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) {
      return errorResponse("Unauthorized", 401);
    }
    const token = authHeader.slice(7);
    const tokenHash = await sha256Hex(token);

    const body = await request.json();
    await ctx.runMutation(api.devices.markOffline, {
      tokenHash,
      deviceId: body.deviceId,
    });

    return jsonResponse({ ok: true });
  }),
});

/** POST /devices/remove — Remove a device (authed). */
http.route({
  path: "/devices/remove",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) {
      return errorResponse("Unauthorized", 401);
    }
    const token = authHeader.slice(7);
    const tokenHash = await sha256Hex(token);

    const body = await request.json();
    await ctx.runMutation(api.devices.removeDevice, {
      tokenHash,
      deviceId: body.deviceId,
    });

    return jsonResponse({ ok: true });
  }),
});

/** POST /devices/alias — Set or clear a device alias (authed).
 *  Body: { deviceId, alias?: string }. Pass alias: "" or omit to
 *  clear. Aliases are scoped to the caller's user and must be unique
 *  across that user's devices — used by `yaver ssh <alias>`, the
 *  dashboard device list, and the mobile app. */
http.route({
  path: "/devices/alias",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) {
      return errorResponse("Unauthorized", 401);
    }
    const token = authHeader.slice(7);
    const tokenHash = await sha256Hex(token);

    const body = await request.json().catch(() => null);
    if (!body || typeof body.deviceId !== "string" || !body.deviceId.trim()) {
      return errorResponse("deviceId required", 400);
    }
    try {
      const result = await ctx.runMutation(api.devices.setDeviceAlias, {
        tokenHash,
        deviceId: body.deviceId,
        alias: typeof body.alias === "string" ? body.alias : undefined,
      });
      return jsonResponse(result);
    } catch (e: any) {
      const msg = e?.message || "alias update failed";
      if (errorMessageIncludes(e, "Unauthorized")) return errorResponse(msg, 401);
      if (errorMessageIncludes(e, "Device not found")) return errorResponse(msg, 404);
      if (errorMessageIncludes(e, "alias already used")) return errorResponse(msg, 409);
      if (errorMessageIncludes(e, "alias invalid")) return errorResponse(msg, 400);
      return errorResponse(msg, 400);
    }
  }),
});

// Fleet tags — set/add/remove labels on a device for selector-based ops.
http.route({
  path: "/devices/tags",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) return errorResponse("Unauthorized", 401);
    const tokenHash = await sha256Hex(authHeader.slice(7));
    const body = await request.json().catch(() => null);
    if (!body || typeof body.deviceId !== "string" || !body.deviceId.trim()) {
      return errorResponse("deviceId required", 400);
    }
    try {
      const result = await ctx.runMutation(api.devices.setDeviceTags, {
        tokenHash,
        deviceId: body.deviceId,
        tags: Array.isArray(body.tags) ? body.tags : undefined,
        add: Array.isArray(body.add) ? body.add : undefined,
        remove: Array.isArray(body.remove) ? body.remove : undefined,
      });
      return jsonResponse(result);
    } catch (e: any) {
      const msg = e?.message || "tag update failed";
      if (errorMessageIncludes(e, "Unauthorized")) return errorResponse(msg, 401);
      if (errorMessageIncludes(e, "Device not found")) return errorResponse(msg, 404);
      return errorResponse(msg, 400);
    }
  }),
});

// Fleet selector — resolve the caller's devices matching tags/platform/online.
// The Fleet SDK calls this to turn `select({tags,...})` into machines.
http.route({
  path: "/devices/select",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) return errorResponse("Unauthorized", 401);
    const tokenHash = await sha256Hex(authHeader.slice(7));
    const body = await request.json().catch(() => ({}));
    try {
      const result = await ctx.runQuery(api.devices.selectDevices, {
        tokenHash,
        tags: Array.isArray(body.tags) ? body.tags : undefined,
        match: body.match === "any" ? "any" : body.match === "all" ? "all" : undefined,
        platform: typeof body.platform === "string" ? body.platform : undefined,
        online: typeof body.online === "boolean" ? body.online : undefined,
      });
      return jsonResponse(result);
    } catch (e: any) {
      const msg = e?.message || "select failed";
      if (errorMessageIncludes(e, "Unauthorized")) return errorResponse(msg, 401);
      return errorResponse(msg, 400);
    }
  }),
});

// ── Device Metrics & Events ──────────────────────────────────────────

/** POST /devices/metrics — Report CPU/RAM metrics (authed, called by agent every 60s). */
http.route({
  path: "/devices/metrics",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) {
      return errorResponse("Unauthorized", 401);
    }

    // Validate session at HTTP layer — return 401, not 500
    const user = await authenticateRequest(ctx, request);
    if (!user) {
      return errorResponse("Session expired", 401);
    }

    const token = authHeader.slice(7);
    const tokenHash = await sha256Hex(token);

    const body = await request.json();
    try {
      await ctx.runMutation(api.deviceMetrics.report, {
        tokenHash,
        deviceId: body.deviceId,
        cpuPercent: body.cpuPercent,
        memoryUsedMb: body.memoryUsedMb,
        memoryTotalMb: body.memoryTotalMb,
      });
      return jsonResponse({ ok: true });
    } catch (e: any) {
      return errorResponse(e.message || "Failed to report metrics", 500);
    }
  }),
});

/** GET /devices/metrics?deviceId=xxx — Get metrics for a device (authed). */
http.route({
  path: "/devices/metrics",
  method: "GET",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) {
      return errorResponse("Unauthorized", 401);
    }
    const token = authHeader.slice(7);
    const tokenHash = await sha256Hex(token);

    const url = new URL(request.url);
    const deviceId = url.searchParams.get("deviceId");
    if (!deviceId) return errorResponse("deviceId required", 400);

    const metrics = await ctx.runQuery(api.deviceMetrics.getMetrics, {
      tokenHash,
      deviceId,
    });
    return jsonResponse({ metrics });
  }),
});

/** POST /devices/event — Record a device event (crash, restart, etc.) (authed). */
http.route({
  path: "/devices/event",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) {
      return errorResponse("Unauthorized", 401);
    }
    const token = authHeader.slice(7);
    const tokenHash = await sha256Hex(token);

    const body = await request.json();
    try {
      await ctx.runMutation(api.deviceEvents.record, {
        tokenHash,
        deviceId: body.deviceId,
        event: body.event,
        details: body.details,
      });
      return jsonResponse({ ok: true });
    } catch (e: any) {
      return errorResponse(e.message || "Failed to record event", 500);
    }
  }),
});

/** GET /devices/events?deviceId=xxx — Get recent events for a device (authed). */
http.route({
  path: "/devices/events",
  method: "GET",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) {
      return errorResponse("Unauthorized", 401);
    }
    const token = authHeader.slice(7);
    const tokenHash = await sha256Hex(token);

    const url = new URL(request.url);
    const deviceId = url.searchParams.get("deviceId");
    if (!deviceId) return errorResponse("deviceId required", 400);

    const events = await ctx.runQuery(api.deviceEvents.getEvents, {
      tokenHash,
      deviceId,
    });
    return jsonResponse({ events });
  }),
});

/** POST /usage/record — Record runner usage when a task finishes (authed). */
http.route({
  path: "/usage/record",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) {
      return errorResponse("Unauthorized", 401);
    }
    const token = authHeader.slice(7);
    const tokenHash = await sha256Hex(token);

    const body = await request.json();
    try {
      await ctx.runMutation(api.runnerUsage.record, {
        tokenHash,
        deviceId: body.deviceId,
        taskId: body.taskId,
        runner: body.runner,
        model: body.model,
        durationSec: body.durationSec,
        startedAt: body.startedAt,
        finishedAt: body.finishedAt,
        source: body.source,
      });
      return jsonResponse({ ok: true });
    } catch (e: any) {
      return errorResponse(e.message || "Failed to record usage", 500);
    }
  }),
});

/** GET /usage — Get usage summary with daily aggregation (authed). */
http.route({
  path: "/usage",
  method: "GET",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) {
      return errorResponse("Unauthorized", 401);
    }
    const token = authHeader.slice(7);
    const tokenHash = await sha256Hex(token);

    const url = new URL(request.url);
    const since = url.searchParams.get("since");

    const usage = await ctx.runQuery(api.runnerUsage.getUsage, {
      tokenHash,
      since: since ? parseInt(since) : undefined,
    });
    return jsonResponse(usage);
  }),
});

/** POST /devices/runner-down — Set runner down/up flag (authed). */
http.route({
  path: "/devices/runner-down",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) {
      return errorResponse("Unauthorized", 401);
    }
    const token = authHeader.slice(7);
    const tokenHash = await sha256Hex(token);

    const body = await request.json();
    try {
      await ctx.runMutation(api.devices.setRunnerDown, {
        tokenHash,
        deviceId: body.deviceId,
        runnerDown: body.runnerDown,
      });
      return jsonResponse({ ok: true });
    } catch (e: any) {
      return errorResponse(e.message || "Failed to update runner status", 500);
    }
  }),
});

// ── Logout (current session only — other devices stay signed in) ─────

/** POST /auth/logout — Delete the current session only.
 *  Mobile signout does NOT kill CLI or other device sessions. */
http.route({
  path: "/auth/logout",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) {
      return errorResponse("Unauthorized", 401);
    }
    const token = authHeader.slice(7);
    const tokenHash = await sha256Hex(token);

    try {
      await ctx.runMutation(api.auth.deleteSession, { tokenHash });
      return jsonResponse({ ok: true });
    } catch {
      return errorResponse("Failed to logout", 500);
    }
  }),
});

/** POST /auth/logout-all — Delete ALL sessions (sign out everywhere).
 *  Only for explicit "sign out all devices" action. */
http.route({
  path: "/auth/logout-all",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) {
      return errorResponse("Unauthorized", 401);
    }
    const token = authHeader.slice(7);
    const tokenHash = await sha256Hex(token);

    try {
      await ctx.runMutation(api.auth.deleteAllSessions, { tokenHash });
      return jsonResponse({ ok: true });
    } catch {
      return errorResponse("Failed to logout", 500);
    }
  }),
});

// ── Account Deletion ────────────────────────────────────────────────

/** POST /auth/delete-account — Delete user account and all data (authed). */
http.route({
  path: "/auth/delete-account",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) {
      return errorResponse("Unauthorized", 401);
    }
    const token = authHeader.slice(7);
    const tokenHash = await sha256Hex(token);

    try {
      await ctx.runMutation(api.auth.deleteAccount, { tokenHash });
      return jsonResponse({ ok: true });
    } catch {
      return errorResponse("Failed to delete account", 500);
    }
  }),
});

// ── Auth Logging (unauthenticated — for debugging OAuth) ───────────

/** POST /auth/log — Log an auth event (unauthenticated, called from web OAuth flow). */
http.route({
  path: "/auth/log",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    try {
      const body = await request.json();
      await ctx.runMutation(api.authLogs.writeLog, {
        level: body.level || "info",
        provider: body.provider || "unknown",
        step: body.step || "unknown",
        message: body.message || "",
        details: body.details ? String(body.details).slice(0, 2000) : undefined,
      });
      return jsonResponse({ ok: true });
    } catch (e) {
      console.error("Auth log error:", e);
      return jsonResponse({ ok: false }, 500);
    }
  }),
});

// ── User Settings ───────────────────────────────────────────────────

http.route({
  path: "/settings",
  method: "GET",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) return errorResponse("Unauthorized", 401);
    const tokenHash = await sha256Hex(authHeader.slice(7));
    const settings = await ctx.runQuery(api.userSettings.getByToken, { tokenHash });
    const safeSettings = settings ? { ...settings, speechApiKey: undefined } : null;
    return jsonResponse({
      ok: true,
      settings: safeSettings || { forceRelay: false, runnerId: undefined, customRunnerCommand: undefined, relayUrl: undefined, relayPassword: undefined, tunnelUrl: undefined },
    });
  }),
});

http.route({
  path: "/settings",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) return errorResponse("Unauthorized", 401);
    const tokenHash = await sha256Hex(authHeader.slice(7));
    const body = await request.json();
    await ctx.runMutation(api.userSettings.setByToken, {
      tokenHash,
      forceRelay: body.forceRelay,
      runnerId: body.runnerId,
      customRunnerCommand: body.customRunnerCommand,
      relayUrl: body.relayUrl,
      relayPassword: body.relayPassword,
      tunnelUrl: body.tunnelUrl,
      speechProvider: body.speechProvider,
      ttsEnabled: body.ttsEnabled,
      ttsProvider: body.ttsProvider,
      verbosity: body.verbosity,
      keyStorage: body.keyStorage,
      multiTargetMode: body.multiTargetMode,
      moreOptionalTools: body.moreOptionalTools,
      // Client sends null to clear the preference, undefined to leave untouched.
      primaryDeviceId: body.primaryDeviceId,
      secondaryDeviceId: body.secondaryDeviceId,
      // Per-device coding agent — forwarded to the mutation's
      // primaryRunnerByDevice merge logic. Without this forward the
      // field was silently dropped at the HTTP boundary, every
      // Confirm click no-op'd, and the sidebar kept falling back
      // to whichever runner was first in device.runners[]
      // (= Claude Code on a Linux box that happens to have it
      // installed).
      primaryRunnerForDevice: body.primaryRunnerForDevice,
      // Per-subsystem managed toggle. Client sends only the
      // subsystem(s) it's changing; backend merges the patch into
      // the existing record so other subsystems' toggles are
      // preserved. null on any key clears that subsystem.
      managed: body.managed,
    });
    return jsonResponse({ ok: true });
  }),
});

// ── User Shortcuts (mobile Shortcuts tab) ────────────────────────────
// One-tap action chains. See backend/convex/shortcuts.ts for the privacy
// contract: deviceId + slug + flags + labels only, never paths/prompts.

http.route({
  path: "/shortcuts",
  method: "GET",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) return errorResponse("Unauthorized", 401);
    const tokenHash = await sha256Hex(authHeader.slice(7));
    const shortcuts = await ctx.runQuery(api.shortcuts.listByToken, { tokenHash });
    return jsonResponse({ ok: true, shortcuts });
  }),
});

http.route({
  path: "/shortcuts",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) return errorResponse("Unauthorized", 401);
    const tokenHash = await sha256Hex(authHeader.slice(7));
    const body = await request.json();
    if (!body?.name || !Array.isArray(body?.steps)) {
      return errorResponse("name and steps[] are required", 400);
    }
    const id = await ctx.runMutation(api.shortcuts.upsertByToken, {
      tokenHash,
      id: body.id,
      name: body.name,
      icon: body.icon,
      color: body.color,
      order: body.order,
      steps: body.steps,
    });
    return jsonResponse({ ok: true, id });
  }),
});

http.route({
  path: "/shortcuts/delete",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) return errorResponse("Unauthorized", 401);
    const tokenHash = await sha256Hex(authHeader.slice(7));
    const body = await request.json();
    if (!body?.id) return errorResponse("id is required", 400);
    await ctx.runMutation(api.shortcuts.deleteByToken, { tokenHash, id: body.id });
    return jsonResponse({ ok: true });
  }),
});

/** POST /settings/repair-relay — Re-sync caller's relayPassword with the
 *  current platform-managed value. Used by the web dashboard as a last-ditch
 *  recovery step when the relay keeps returning 401 "invalid relay password".
 *  Never generates new secrets — only re-copies what every synced user has. */
http.route({
  path: "/settings/repair-relay",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) return errorResponse("Unauthorized", 401);
    const tokenHash = await sha256Hex(authHeader.slice(7));
    const result = await ctx.runMutation(api.userSettings.repairRelayPassword, { tokenHash });
    if (!result.ok) return errorResponse(result.reason || "repair failed", 401);
    return jsonResponse(result);
  }),
});

// ── Relay Password Validation ────────────────────────────────────────

/** POST /relay/validate — Relay servers call this to validate per-user passwords. No user auth required (relay auth). */
http.route({
  path: "/relay/validate",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    try {
      const body = await request.json();
      if (!body.password || typeof body.password !== "string") {
        return jsonResponse({ ok: false, error: "password required" }, 400);
      }
      const tokenHash =
        typeof body.token === "string" && body.token
          ? await sha256Hex(body.token)
          : undefined;
      const result = await ctx.runQuery(api.userSettings.validateRelayPassword, {
        password: body.password,
        deviceId: typeof body.deviceId === "string" ? body.deviceId : undefined,
        action: typeof body.action === "string" ? body.action : undefined,
        tokenHash,
      });
      if (!result) {
        return jsonResponse({ ok: false }, 401);
      }
      return jsonResponse({ ok: true, userId: result.userId });
    } catch {
      return jsonResponse({ ok: false, error: "internal error" }, 500);
    }
  }),
});

// ── Push token registration (device-auth approval channel, P2) ──────

/** POST /push/register — a signed-in phone registers its push token so a
 *  remote box's re-auth can ring it for Face ID approval. Authed. */
http.route({
  path: "/push/register",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) {
      return errorResponse("Unauthorized", 401);
    }
    const tokenHash = await sha256Hex(authHeader.slice(7));
    const body = await request.json().catch(() => ({}));
    if (!body.installId || !body.pushToken) {
      return errorResponse("installId and pushToken required", 400);
    }
    try {
      await ctx.runMutation(api.pushNotifications.registerPushToken, {
        tokenHash,
        installId: String(body.installId),
        pushToken: String(body.pushToken),
        transport: String(body.transport || "expo"),
        platform: String(body.platform || "unknown"),
      });
      return jsonResponse({ ok: true });
    } catch (e: any) {
      if (e?.message === "Unauthorized") return errorResponse("Unauthorized", 401);
      return errorResponse(e?.message || "Failed to register push token", 400);
    }
  }),
});

// ── Mobile Stream Logs ──────────────────────────────────────────────

http.route({
  path: "/mobile/log",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    try {
      const body = await request.json();
      // Best-effort user identification
      let userId: string | undefined;
      const user = await authenticateRequest(ctx, request);
      if (user) userId = user.userId;

      await ctx.runMutation(api.mobileStreamLogs.writeLog, {
        userId,
        platform: body.platform || "unknown",
        appVersion: body.appVersion || "unknown",
        buildNumber: body.buildNumber || "unknown",
        level: body.level || "info",
        step: body.step || "unknown",
        message: body.message || "",
        details: body.details ? String(body.details).slice(0, 2000) : undefined,
      });
      return jsonResponse({ ok: true });
    } catch (e) {
      console.error("Mobile log error:", e);
      return jsonResponse({ ok: false }, 500);
    }
  }),
});

// ── Developer Logs (developer-only debugging) ──────────────────────

/** POST /dev/log — Write a developer log (only accepted from developer emails). */
http.route({
  path: "/dev/log",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    try {
      const body = await request.json();
      // Best-effort user identification
      let email: string | undefined = body.email;
      let userId: string | undefined;
      const user = await authenticateRequest(ctx, request);
      if (user) {
        email = user.email;
        userId = user.userId;
      }

      await ctx.runMutation(api.developerLogs.writeLog, {
        email,
        userId,
        source: body.source || "agent",
        level: body.level || "info",
        tag: body.tag || "general",
        message: body.message || "",
        data: body.data ? String(body.data).slice(0, 8000) : undefined,
      });
      return jsonResponse({ ok: true });
    } catch (e) {
      console.error("Dev log error:", e);
      return jsonResponse({ ok: false }, 500);
    }
  }),
});

/** GET /dev/logs — Read developer logs (no auth — dev-only data). */
http.route({
  path: "/dev/logs",
  method: "GET",
  handler: httpAction(async (ctx, request) => {
    const url = new URL(request.url);
    const limit = parseInt(url.searchParams.get("limit") || "50");
    const email = url.searchParams.get("email") || undefined;
    const logs = await ctx.runQuery(api.developerLogs.getLogs, { limit, email });
    return jsonResponse({ logs });
  }),
});

// ── TOTP 2FA Endpoints ──────────────────────────────────────────────

/** POST /auth/totp/setup — Generate TOTP secret (authenticated). */
http.route({
  path: "/auth/totp/setup",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) return errorResponse("Unauthorized", 401);
    const tokenHash = await sha256Hex(authHeader.slice(7));

    try {
      const result = await ctx.runMutation(api.totp.setupTotp, { tokenHash });
      return jsonResponse(result);
    } catch (e: any) {
      return errorResponse(e.message || "Failed to setup TOTP", 400);
    }
  }),
});

/** POST /auth/totp/enable — Verify code and enable 2FA (authenticated). */
http.route({
  path: "/auth/totp/enable",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) return errorResponse("Unauthorized", 401);
    const tokenHash = await sha256Hex(authHeader.slice(7));

    const body = await request.json();
    if (!body.code) return errorResponse("code required", 400);

    try {
      const result = await ctx.runMutation(api.totp.verifyAndEnableTotp, {
        tokenHash,
        code: body.code,
      });
      return jsonResponse(result);
    } catch (e: any) {
      if (e.message === "INVALID_CODE") return errorResponse("Invalid verification code", 401);
      return errorResponse(e.message || "Failed to enable TOTP", 400);
    }
  }),
});

/** POST /auth/totp/disable — Disable 2FA (authenticated, requires TOTP code). */
http.route({
  path: "/auth/totp/disable",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) return errorResponse("Unauthorized", 401);
    const tokenHash = await sha256Hex(authHeader.slice(7));

    const body = await request.json();
    if (!body.code) return errorResponse("code required", 400);

    try {
      await ctx.runMutation(api.totp.disableTotp, { tokenHash, code: body.code });
      return jsonResponse({ ok: true });
    } catch (e: any) {
      if (e.message === "INVALID_CODE") return errorResponse("Invalid verification code", 401);
      return errorResponse(e.message || "Failed to disable TOTP", 400);
    }
  }),
});

/** GET /auth/totp/status — Get 2FA status (authenticated). */
http.route({
  path: "/auth/totp/status",
  method: "GET",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) return errorResponse("Unauthorized", 401);
    const tokenHash = await sha256Hex(authHeader.slice(7));

    const status = await ctx.runQuery(api.totp.getTotpStatus, { tokenHash });
    if (!status) return errorResponse("Unauthorized", 401);
    return jsonResponse(status);
  }),
});

/** POST /auth/totp/check-user — Check if a user has 2FA enabled (server-to-server, takes userId). */
http.route({
  path: "/auth/totp/check-user",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const body = await request.json();
    if (!body.userId) return errorResponse("userId required", 400);
    const result = await ctx.runQuery(api.auth.getUserWithTotp, { userId: body.userId });
    return jsonResponse({ totpEnabled: result?.totpEnabled ?? false });
  }),
});

/** POST /auth/totp/create-pending — Create a pending auth for 2FA (server-to-server, takes userId). */
http.route({
  path: "/auth/totp/create-pending",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const body = await request.json();
    if (!body.userId) return errorResponse("userId required", 400);
    const result = await ctx.runMutation(api.totp.createPendingAuth, { userId: body.userId });
    return jsonResponse(result);
  }),
});

/** POST /auth/verify-totp — Verify TOTP for pending auth, get session token (unauthenticated). */
http.route({
  path: "/auth/verify-totp",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const body = await request.json();
    if (!body.pendingToken || !body.code) {
      return errorResponse("pendingToken and code required", 400);
    }

    try {
      const result = await ctx.runMutation(api.totp.verifyTotpForLogin, {
        pendingToken: body.pendingToken,
        code: body.code,
      });
      return jsonResponse(result);
    } catch (e: any) {
      if (e.message === "INVALID_CODE") return errorResponse("Invalid code", 401);
      if (e.message === "INVALID_PENDING") return errorResponse("Invalid or expired session", 404);
      if (e.message === "PENDING_EXPIRED") return errorResponse("Session expired, please login again", 410);
      if (e.message === "TOO_MANY_ATTEMPTS") return errorResponse("Too many attempts, please login again", 429);
      return errorResponse(e.message || "Verification failed", 400);
    }
  }),
});

// ── Device Code Auth (Headless) ─────────────────────────────────────

/** POST /auth/device-code — Create a new device code for headless auth (unauthenticated). */
http.route({
  path: "/auth/device-code",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const body = await request.json().catch(() => ({}));
    const result = await ctx.runMutation(api.deviceCode.createDeviceCode, {
      machineName: typeof body?.machineName === "string" ? body.machineName : undefined,
      platform: typeof body?.platform === "string" ? body.platform : undefined,
      arch: typeof body?.arch === "string" ? body.arch : undefined,
      shell: typeof body?.shell === "string" ? body.shell : undefined,
      environment: typeof body?.environment === "string" ? body.environment : undefined,
      runtimeVersion: typeof body?.runtimeVersion === "string" ? body.runtimeVersion : undefined,
      preferredProvider: typeof body?.preferredProvider === "string" ? body.preferredProvider : undefined,
      isWsl: typeof body?.isWsl === "boolean" ? body.isWsl : undefined,
    });
    return jsonResponse(result);
  }),
});

/** GET /auth/device-code/poll — Poll device code status (unauthenticated, called by CLI). */
http.route({
  path: "/auth/device-code/poll",
  method: "GET",
  handler: httpAction(async (ctx, request) => {
    const url = new URL(request.url);
    const deviceCode = url.searchParams.get("device_code");
    if (!deviceCode) {
      return errorResponse("device_code required", 400);
    }
    const result = await ctx.runMutation(api.deviceCode.pollDeviceCode, { deviceCode });
    return jsonResponse(result);
  }),
});

/** GET /auth/device-code/info — Public machine info for a waiting device code. */
http.route({
  path: "/auth/device-code/info",
  method: "GET",
  handler: httpAction(async (ctx, request) => {
    const url = new URL(request.url);
    const userCode = (url.searchParams.get("user_code") || "").toUpperCase().trim();
    if (!userCode) {
      return errorResponse("user_code required", 400);
    }
    const result = await ctx.runQuery(api.deviceCode.getDeviceCodeInfo, { userCode });
    if (!result) {
      return errorResponse("Not found", 404);
    }
    return jsonResponse(result);
  }),
});

/** POST /auth/device-code/authorize — Authorize a device code (authenticated). */
http.route({
  path: "/auth/device-code/authorize",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const user = await authenticateRequest(ctx, request);
    if (!user) {
      return errorResponse("Unauthorized", 401);
    }

    const body = await request.json();
    const { userCode } = body;
    if (!userCode) {
      return errorResponse("userCode required", 400);
    }

    // Look up the user's _id from the session
    const authHeader = request.headers.get("Authorization")!;
    const token = authHeader.slice(7);
    const tokenHash = await sha256Hex(token);
    const session = await ctx.runQuery(api.auth.validateSession, { tokenHash });
    if (!session) {
      return errorResponse("Unauthorized", 401);
    }

    // We need the user's document _id for the mutation. Get it via a dedicated query.
    const userDoc = await ctx.runQuery(api.auth.getUserDocId, { tokenHash });
    if (!userDoc) {
      return errorResponse("User not found", 404);
    }

    try {
      await ctx.runMutation(api.deviceCode.authorizeDeviceCode, {
        userCode: userCode.toUpperCase().trim(),
        userId: userDoc,
      });
      return jsonResponse({ ok: true });
    } catch (e: any) {
      if (e.message === "INVALID_CODE") return errorResponse("Invalid code", 404);
      if (e.message === "CODE_EXPIRED") return errorResponse("Code expired", 410);
      if (e.message === "CODE_ALREADY_USED") return errorResponse("Code already used", 409);
      return errorResponse("Failed to authorize", 500);
    }
  }),
});

// ── Download Endpoints ──────────────────────────────────────────────

/** GET /downloads/list — List all available downloads (public, no auth). */
http.route({
  path: "/downloads/list",
  method: "GET",
  handler: httpAction(async (ctx) => {
    const downloads = await ctx.runQuery(api.downloads.listDownloads, {});
    return new Response(JSON.stringify({ downloads }), {
      status: 200,
      headers: {
        "Content-Type": "application/json",
        "Access-Control-Allow-Origin": "*",
      },
    });
  }),
});

// ── Platform Config ──────────────────────────────────────────────────

/** GET /config — Public platform config (relay servers, runners, models). No auth required. */
http.route({
  path: "/config",
  method: "GET",
  handler: httpAction(async (ctx) => {
    const [config, runners, models] = await Promise.all([
      ctx.runQuery(api.platformConfig.getClientConfig, {}),
      ctx.runQuery(api.aiRunners.list, {}),
      ctx.runQuery(api.aiModels.list, {}),
    ]);
    // Parse relay_servers from JSON string to array for client convenience
    let relayServers: unknown[] = [];
    if (config.relay_servers) {
      try {
        const parsed = JSON.parse(config.relay_servers);
        relayServers = Array.isArray(parsed)
          ? parsed.map((server) => {
              if (!server || typeof server !== "object" || Array.isArray(server)) return server;
              const { password: _password, ...publicServer } = server as Record<string, unknown>;
              return publicServer;
            })
          : [];
      } catch {
        // ignore parse errors
      }
    }
    return new Response(
      JSON.stringify({
        relayServers,
        runners,
        models,
        cliVersion: config.cli_version || null,
        mobileVersion: config.mobile_version || null,
        relayVersion: config.relay_version || null,
        webVersion: config.web_version || null,
        backendVersion: config.backend_version || null,
      }),
      {
        status: 200,
        headers: {
          "Content-Type": "application/json",
          "Access-Control-Allow-Origin": "*",
          // Cache for 5 minutes — config doesn't change often
          "Cache-Control": "public, max-age=300",
        },
      }
    );
  }),
});

// ── Public install catalogue ────────────────────────────────────────

/** GET /packages — public tool catalogue for Go agent + mobile/web UIs.
 * Returns the whole packageRegistry table sorted by (kind, sortOrder).
 * Nothing in here is user-specific or sensitive (see packages.ts). */
http.route({
  path: "/packages",
  method: "GET",
  handler: httpAction(async (ctx) => {
    const rows = await ctx.runQuery(api.packages.list, {});
    return new Response(JSON.stringify(rows), {
      status: 200,
      headers: {
        "Content-Type": "application/json",
        "Access-Control-Allow-Origin": "*",
        // 10 minutes — the catalogue changes rarely, and the agent
        // refreshes on its own 6h schedule anyway.
        "Cache-Control": "public, max-age=600",
      },
    });
  }),
});

// ── AI Runners ──────────────────────────────────────────────────────

/** GET /runners — List all AI runners (public, no auth). */
http.route({
  path: "/runners",
  method: "GET",
  handler: httpAction(async (ctx) => {
    const runners = await ctx.runQuery(api.aiRunners.list, {});
    return jsonResponse({ runners });
  }),
});

/** POST /runners/seed — Seed predefined AI runners (idempotent, no auth). */
http.route({
  path: "/runners/seed",
  method: "POST",
  handler: httpAction(async (ctx) => {
    await ctx.runMutation(api.aiRunners.seed, {});
    return jsonResponse({ ok: true });
  }),
});

// ── AI Models ────────────────────────────────────────────────────────

/** GET /models — List all AI models (public, no auth). */
http.route({
  path: "/models",
  method: "GET",
  handler: httpAction(async (ctx) => {
    const models = await ctx.runQuery(api.aiModels.list, {});
    return jsonResponse({ models });
  }),
});

/** POST /models/seed — Seed predefined AI models (idempotent, no auth). */
http.route({
  path: "/models/seed",
  method: "POST",
  handler: httpAction(async (ctx) => {
    await ctx.runMutation(api.aiModels.seed, {});
    return jsonResponse({ ok: true });
  }),
});

// ── Subscription & Managed Relay ─────────────────────────────────────

/** Generate a random relay password. */
function generateRelayPassword(): string {
  const chars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789";
  let result = "";
  for (let i = 0; i < 32; i++) {
    result += chars.charAt(Math.floor(Math.random() * chars.length));
  }
  return result;
}

/** POST /webhooks/lemonsqueezy — LemonSqueezy webhook (no auth — validated by signature). */
http.route({
  path: "/webhooks/lemonsqueezy",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    // Verify webhook signature (HMAC-SHA256 over the raw body using
    // LEMONSQUEEZY_WEBHOOK_SECRET). Without verification, anyone could
    // POST a forged "subscription_created" to provision hardware on your
    // Hetzner account.
    const signature = request.headers.get("x-signature");
    const body = await request.text();
    if (!(await verifyLemonSqueezySignature(body, signature))) {
      return errorResponse("Invalid signature", 401);
    }

    let payload;
    try {
      payload = JSON.parse(body);
    } catch {
      return errorResponse("Invalid JSON", 400);
    }

    const eventName = payload.meta?.event_name;
    const data = payload.data?.attributes;

    if (!eventName || !data) {
      return errorResponse("Invalid payload", 400);
    }

    // Extract user email from custom data or customer email
    const userEmail = payload.meta?.custom_data?.user_email || data.user_email;
    if (!userEmail) {
      return errorResponse("No user email", 400);
    }

    // Find user by email
    const user = await ctx.runQuery(internal.auth.getUserByEmail, { email: userEmail });
    if (!user) {
      return errorResponse("User not found", 404);
    }

    const lemonSqueezyId = String(payload.data.id);
    const customerId = String(data.customer_id);

    switch (eventName) {
      case "subscription_created":
      case "subscription_updated":
      case "subscription_resumed": {
        const productType = payload.meta?.custom_data?.product_type || "relay";
        const machineType = payload.meta?.custom_data?.machine_type === "gpu" ? "gpu" : "cpu";
        const cloudPlanId = normalizeCloudPurchasePlan(payload.meta?.custom_data?.plan_id);
        const isCloudPreviewProduct = productType === "yaver-cloud";
        const plan = isCloudPreviewProduct
          ? cloudPlanId
          : data.variant_name?.includes("yearly")
            ? "relay-yearly"
            : "relay-monthly";
        const status = data.status === "active" ? "active" : data.status === "past_due" ? "past_due" : "active";
        const periodEnd = new Date(data.renews_at || data.ends_at).getTime();

        const subId = await ctx.runMutation(internal.subscriptions.upsertFromWebhook, {
          lemonSqueezyId,
          lemonSqueezyCustomerId: customerId,
          userId: user._id,
          plan,
          status,
          currentPeriodEnd: periodEnd,
        });

        // Apply plan entitlements (included active-hours + gateway
        // inference policy + monthly wallet budget) for managed-cloud
        // products on EVERY active create/update/resume — idempotent per
        // billing period, so monthly renewals refresh the allowance and a
        // re-delivered webhook never double-credits. Relay subs are
        // unaffected. tier mirrors the provision branch below
        // (custom_data.tier="hosted" ⇒ managed AI on; absent ⇒ byok).
        const isManagedProduct =
          productType === "cpu" || productType === "gpu" || isCloudPreviewProduct;
        if (isManagedProduct && status === "active") {
          const tier = payload.meta?.custom_data?.tier === "hosted" ? "hosted" : "byok";
          await ctx.scheduler.runAfter(0, internal.plans.applyPlanEntitlements, {
            userId: user._id,
            subscriptionId: lemonSqueezyId,
            tier,
            plan,
          });
        }

        // If new subscription, provision the appropriate resource
        if (eventName === "subscription_created") {
          const region = payload.meta?.custom_data?.region || "eu";

          if (productType === "cpu" || productType === "gpu" || isCloudPreviewProduct) {
            // Cloud dev machine — create and provision
            const teamId = payload.meta?.custom_data?.team_id;
            await ctx.runMutation(api.cloudMachines.ensureForSubscription, {
              userId: user._id,
              machineType: productType === "gpu" ? "gpu" : machineType,
              teamId,
              region,
              subscriptionId: subId,
              // Hosted SKU opts in via checkout custom_data.tier="hosted"
              // (SANDBOX_HOSTED_HANDOFF.md §5). Absent ⇒ byok, so the
              // current single SKU is unchanged until a hosted variant
              // sets it. createCloudMachine already accepts `tier`.
              tier:
                payload.meta?.custom_data?.tier === "hosted"
                  ? "hosted"
                  : "byok",
            });
            // SECURITY (per-tenant isolation): do NOT attach a PAID
            // subscription to the shared preview box.
            // ensureForSubscription → createCloudMachine already
            // schedules internal.cloudMachines.provision, which spins a
            // DEDICATED per-tenant Hetzner box (its own cloud-init, its
            // own per-user 1-year session token, behind the fail-closed
            // canProvisionManaged gate). Repointing real buyers at one
            // shared host let any buyer SSH / open a shell into another
            // buyer's source tree — a code-leak that breaks Yaver's
            // privacy contract. The shared box is retained ONLY for the
            // owner-gated, no-Hetzner-spend /billing/yaver-cloud/
            // dev-activate path (ensurePreviewCloudMachine). Never
            // re-add a shared-server attach on this paid webhook path.
            // isCloudPreviewProduct still selects the `plan` label above.
          } else {
            // Managed relay (default)
            const password = generateRelayPassword();
            const relayId = await ctx.runMutation(internal.managedRelays.create, {
              userId: user._id,
              subscriptionId: subId,
              region,
              password,
            });

            // Trigger automated provisioning (Hetzner + Cloudflare + SSL)
            await ctx.scheduler.runAfter(0, internal.provisionRelay.provision, {
              userId: user._id,
              subscriptionId: subId,
              relayId,
              region,
              password,
            });
          }
        }
        break;
      }

      case "subscription_cancelled":
      case "subscription_expired": {
        const sub = await ctx.runMutation(internal.subscriptions.cancel, { lemonSqueezyId });

        // Revoke managed-inference entitlement immediately (disable the
        // gateway policy) so a lapsed subscriber can't keep spending our
        // gateway key on leftover balance. Box teardown happens below.
        await ctx.scheduler.runAfter(0, internal.plans.revokePlanEntitlements, {
          userId: user._id,
        });

        // Tear the box down on BOTH cancel and expiry. A managed box
        // costs Yaver money every hour it runs; the moment the user
        // cancels we stop that spend (deprovision snapshots first, so
        // a resubscribe can be restored from the snapshot). Previously
        // only `subscription_expired` deprovisioned, leaving a paid
        // box running through the rest of a cancelled period.
        if (sub && (eventName === "subscription_expired" || eventName === "subscription_cancelled")) {
          const [relays, machines] = await Promise.all([
            ctx.runQuery(internal.managedRelays.listBySubscription, { subscriptionId: sub }),
            ctx.runQuery(internal.cloudMachines.listBySubscription, { subscriptionId: sub }),
          ]);

          for (const relay of relays) {
            if (relay.hetznerServerId && relay.domain) {
              await ctx.scheduler.runAfter(0, internal.provisionRelay.deprovision, {
                relayId: relay._id,
                hetznerServerId: relay.hetznerServerId,
                domain: relay.domain,
              });
            }
          }

          for (const machine of machines) {
            if (machine.status !== "stopped" && machine.status !== "stopping") {
              await ctx.runMutation(api.cloudMachines.deprovision, {
                machineId: machine._id,
              });
            }
          }
        }
        break;
      }

      case "subscription_payment_failed": {
        const productType = payload.meta?.custom_data?.product_type || "relay";
        const machineType = payload.meta?.custom_data?.machine_type === "gpu" ? "gpu" : "cpu";
        const cloudPlanId = normalizeCloudPurchasePlan(payload.meta?.custom_data?.plan_id);
        await ctx.runMutation(internal.subscriptions.upsertFromWebhook, {
          lemonSqueezyId,
          lemonSqueezyCustomerId: customerId,
          userId: user._id,
          plan: productType === "relay" ? "relay-monthly" : productType === "yaver-cloud" ? cloudPlanId : `yaver-cloud-${machineType}`,
          status: "past_due",
          currentPeriodEnd: Date.now(),
        });
        break;
      }

      // Prepaid credit-pack purchase (OpenAI-style top-up). A one-time
      // order, NOT a subscription. Idempotent on the order id (LS
      // re-delivers webhooks). SECURITY (public repo — buyers must not
      // be able to mint more than they paid):
      //  1. Resolve the pack from the SIGNED variant id actually
      //     purchased (data.first_order_item.variant_id) — never from
      //     client-set custom_data.pack_id, which a buyer controls.
      //  2. Refunded/non-paid orders never credit.
      //  3. Cross-check the amount actually paid (signed subtotal/total)
      //     against the catalog price — reject a mismatch rather than
      //     credit a bigger pack than was charged.
      case "order_created": {
        if (data.refunded === true) break;
        if (data.status && data.status !== "paid") break;
        const item = (data.first_order_item || {}) as {
          variant_id?: number | string;
          price?: number;
        };
        const pack = creditPackByVariantId(item.variant_id);
        if (!pack) {
          // Not one of our credit packs (or variant env not configured) —
          // ignore silently so unrelated store orders never touch wallets.
          break;
        }
        // Amount actually paid, in cents, from the signed payload.
        // Prefer the line-item price (pre-tax); fall back to subtotal.
        const paidCents = Number(
          item.price ?? data.subtotal ?? data.total ?? 0,
        );
        if (!Number.isFinite(paidCents) || paidCents + 1 < pack.cents) {
          // Paid less than the pack's catalog price — refuse to credit
          // the full pack. (+1c slack for rounding.) Loud, never silent.
          console.error(
            `[credits] order ${lemonSqueezyId} pack ${pack.id}: paid ${paidCents}c < catalog ${pack.cents}c — NOT credited (possible tamper)`,
          );
          break;
        }
        const r = await ctx.runMutation(internal.cloudLifecycle.topUpForOrder, {
          userId: user._id,
          orderId: lemonSqueezyId,
          amountCents: pack.cents,
          source: "lemonsqueezy",
          packId: pack.id,
        });
        console.log(
          `[credits] order ${lemonSqueezyId} pack ${pack.id} (${pack.cents}c, paid ${paidCents}c) → user ${user._id}: ${r.credited ? "credited" : "duplicate-noop"}, balance ${r.balanceCents}c`,
        );
        break;
      }
    }

    return jsonResponse({ ok: true });
  }),
});

/** POST /billing/yaver-cloud/checkout — create authenticated Lemon Squeezy sandbox checkout. */
http.route({
  path: "/billing/yaver-cloud/checkout",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const session = await authenticateRequest(ctx, request);
    if (!session) return errorResponse("Unauthorized", 401);
    if (!isCloudPreviewUser(session.email, session.userDocId)) {
      return errorResponse("Yaver Cloud is private-preview only on this account", 403);
    }

    let body: { region?: string; machineType?: string; planId?: string } = {};
    try {
      body = await request.json();
    } catch {
      // allow empty body
    }
    const region = (body.region ?? "eu").trim() || "eu";
    const planId = normalizeCloudPurchasePlan(body.planId);

    // Map the purchased plan to its entitlement tier AND the LS variant to
    // charge. cloud-agent = $19 included-model = hosted; cloud-workspace =
    // $9 BYO = byok. The webhook grants hosted entitlements ONLY when
    // custom_data.tier === "hosted", so the tier MUST be passed here — without
    // it every purchase silently fell back to byok regardless of plan.
    const tier: "hosted" | "byok" = planId === "cloud-agent" ? "hosted" : "byok";
    // hosted MUST have its own variant — never fall back to the byok-priced
    // default, or a $19 plan would be sold for $9 (silent mis-bill). byok may
    // fall back to the legacy single SKU (which is byok-priced).
    let variantId: string | undefined;
    if (tier === "hosted") {
      variantId = lsEnv("YAVER_CLOUD_HOSTED_VARIANT_ID");
      if (!variantId) {
        return errorResponse(
          "The Cloud Agent ($19) plan isn't available yet — its LemonSqueezy variant is not configured (set LEMONSQUEEZY_YAVER_CLOUD_HOSTED_VARIANT_ID). Use Cloud Workspace ($9) for now.",
          503,
        );
      }
    } else {
      variantId = lsEnv("YAVER_CLOUD_BYOK_VARIANT_ID") ?? lsEnv("YAVER_CLOUD_VARIANT_ID");
    }

    try {
      const url = await createLemonSqueezyCheckout({
        email: session.email,
        variantId,
        variantEnvName: tier === "hosted" ? "YAVER_CLOUD_HOSTED_VARIANT_ID" : "YAVER_CLOUD_BYOK_VARIANT_ID",
        custom: {
          user_email: session.email,
          product_type: "yaver-cloud",
          plan_id: planId,
          tier,
          machine_type: body.machineType === "gpu" ? "gpu" : "cpu",
          region,
        },
      });
      return jsonResponse({ url, planId, tier, mode: parseBooleanEnv(lsEnv("SANDBOX"), true) ? "sandbox" : "live" });
    } catch (error) {
      const message = error instanceof Error ? error.message : String(error);
      return errorResponse(message, 500);
    }
  }),
});

/** GET /billing/credits/packs — prepaid credit-pack catalog (the
 *  "pick a pack" top-up options). Public to any authed user; the UI
 *  renders these as $10/$25/$50/$100 buttons. */
http.route({
  path: "/billing/credits/packs",
  method: "GET",
  handler: httpAction(async (ctx, request) => {
    const session = await authenticateRequest(ctx, request);
    if (!session) return errorResponse("Unauthorized", 401);
    return jsonResponse({
      ok: true,
      currency: "usd",
      packs: CREDIT_PACKS.map((p) => ({ id: p.id, cents: p.cents, label: p.label })),
    });
  }),
});

/** POST /billing/credits/checkout — create a LemonSqueezy ONE-TIME
 *  checkout for a prepaid credit pack. On payment, the order_created
 *  webhook credits the wallet (idempotent). Body: { packId }. */
http.route({
  path: "/billing/credits/checkout",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const session = await authenticateRequest(ctx, request);
    if (!session) return errorResponse("Unauthorized", 401);
    if (!cloudAccessAllowed(session.email, session.userDocId)) {
      return errorResponse("Yaver Cloud is private-preview only on this account", 403);
    }
    let body: { packId?: string } = {};
    try {
      body = await request.json();
    } catch {
      // allow empty body — fall through to validation
    }
    const packId = String(body.packId || "").trim();
    const pack = creditPackById(packId);
    if (!pack) {
      return errorResponse(
        `Unknown credit pack "${packId}". Valid: ${CREDIT_PACKS.map((p) => p.id).join(", ")}`,
        400,
      );
    }
    const variantEnvName = creditPackVariantEnvName(pack.id);
    const variantId = lsEnv(variantEnvName);
    if (!variantId) {
      return errorResponse(
        `Credit pack ${pack.id} is not configured (set LEMONSQUEEZY_${variantEnvName})`,
        503,
      );
    }
    try {
      const url = await createLemonSqueezyCheckout({
        email: session.email,
        variantId,
        variantEnvName,
        custom: {
          user_email: session.email,
          product_type: "credit-pack",
          pack_id: pack.id,
          credit_cents: String(pack.cents),
        },
      });
      return jsonResponse({
        url,
        packId: pack.id,
        cents: pack.cents,
        mode: parseBooleanEnv(lsEnv("SANDBOX"), true) ? "sandbox" : "live",
      });
    } catch (error) {
      const message = error instanceof Error ? error.message : String(error);
      return errorResponse(message, 500);
    }
  }),
});

/** POST /billing/yaver-cloud/dev-activate — bypass checkout and attach preview machine for testing. */
http.route({
  path: "/billing/yaver-cloud/dev-activate",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const session = await authenticateRequest(ctx, request);
    if (!session) return errorResponse("Unauthorized", 401);
    if (!isCloudPreviewUser(session.email, session.userDocId)) {
      return errorResponse("Yaver Cloud is private-preview only on this account", 403);
    }

    let body: { region?: string } = {};
    try {
      body = await request.json();
    } catch {
      // allow empty body
    }
    const region = (body.region ?? "eu").trim() || "eu";

    try {
      const machineId = await ensurePreviewCloudMachine(ctx, session.userDocId, region);
      return jsonResponse({ ok: true, machineId, mode: "dev-bypass" });
    } catch (error) {
      const message = error instanceof Error ? error.message : String(error);
      return errorResponse(message, 500);
    }
  }),
});

/** POST /billing/yaver-cloud/dev-adopt — owner-only: register an
 *  EXISTING Hetzner box as a managed machine (imitates a managed
 *  purchase) without provisioning a new server or LemonSqueezy. */
http.route({
  path: "/billing/yaver-cloud/dev-adopt",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const session = await authenticateRequest(ctx, request);
    if (!session) return errorResponse("Unauthorized", 401);
    if (!isCloudPreviewUser(session.email, session.userDocId)) {
      return errorResponse("Owner-only (private preview) on this account", 403);
    }
    let body: { hetznerServerId?: string; region?: string; serverIp?: string; hostname?: string; deviceId?: string } = {};
    try {
      body = await request.json();
    } catch {
      return errorResponse("Invalid JSON", 400);
    }
    const hetznerServerId = (body.hetznerServerId ?? "").toString().trim();
    if (!hetznerServerId) return errorResponse("hetznerServerId is required", 400);
    try {
      const machineId = await ctx.runMutation(internal.cloudMachines.adoptExisting, {
        userId: session.userDocId as any,
        hetznerServerId,
        region: (body.region ?? "eu").trim() || "eu",
        serverIp: body.serverIp,
        hostname: body.hostname,
        deviceId: (body.deviceId ?? "").toString().trim() || undefined,
      });
      return jsonResponse({ ok: true, machineId, origin: "managed", mode: "dev-adopt" });
    } catch (error) {
      return errorResponse(error instanceof Error ? error.message : String(error), 500);
    }
  }),
});

/** POST /billing/yaver-cloud/dev-deprovision — owner-only: tear down
 *  a managed machine the caller owns (snapshot+delete via the managed
 *  destroy path). The web "Decommission" button for managed boxes. */
/** POST /billing/yaver-cloud/runners-authorized — flip a managed
 *  box's runnersAuthorized once its coding-agent OAuth is in place
 *  (via the existing yaver CLI/MCP runner-auth flow today; the web
 *  one-click flow is the tracked #9). Owner-gated, scoped to a
 *  machine the caller owns. Drives the UI Unauthorized→ready state.
 *  project_managed_cloud_onboarding_gap. */
http.route({
  path: "/billing/yaver-cloud/runners-authorized",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const session = await authenticateRequest(ctx, request);
    if (!session) return errorResponse("Unauthorized", 401);
    if (!isCloudPreviewUser(session.email, session.userDocId)) {
      return errorResponse("Owner-only (private preview) on this account", 403);
    }
    const body = await request.json().catch(() => ({}));
    const machineId = String(body.machineId ?? "").trim();
    if (!machineId) return errorResponse("machineId is required", 400);
    const machine = await ctx.runQuery(internal.cloudMachines.getInternal, {
      machineId: machineId as any,
    });
    if (!machine) return errorResponse("Machine not found", 404);
    if (machine.userId !== session.userDocId) {
      return errorResponse("Not your machine", 403);
    }
    const authorized = body.authorized === false ? false : true;
    await ctx.runMutation(internal.cloudMachines.setPhase, {
      machineId: machineId as any,
      phase: authorized ? "ready" : "authorizing-runners",
      progress: authorized ? 100 : 90,
      runnersAuthorized: authorized,
    });
    return jsonResponse({ ok: true, runnersAuthorized: authorized });
  }),
});

/** POST /billing/yaver-cloud/reconcile — self-heal: "I paid but have
 *  no box". Re-provisions the caller's active managed subscription(s)
 *  if no live box exists. Idempotent (no-op when a healthy box is
 *  already there). project_managed_cloud_onboarding_gap (recovery). */
http.route({
  path: "/billing/yaver-cloud/reconcile",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const session = await authenticateRequest(ctx, request);
    if (!session) return errorResponse("Unauthorized", 401);
    if (!isCloudPreviewUser(session.email, session.userDocId)) {
      return errorResponse("Owner-only (private preview) on this account", 403);
    }
    const r = await ctx.runAction(internal.cloudMachines.reconcileSubscriptions, {
      onlyUserId: session.userDocId as any,
    });
    return jsonResponse({ ok: true, ...r });
  }),
});

http.route({
  path: "/billing/yaver-cloud/dev-deprovision",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const session = await authenticateRequest(ctx, request);
    if (!session) return errorResponse("Unauthorized", 401);
    // Open to any cloud-access user — they can only ever decommission a
    // box they own (the per-machine ownership check below enforces it),
    // and a prepaid user must be able to tear down their own box.
    if (!cloudAccessAllowed(session.email, session.userDocId)) {
      return errorResponse("Yaver Cloud is private-preview only on this account", 403);
    }
    let body: { machineId?: string } = {};
    try {
      body = await request.json();
    } catch {
      return errorResponse("Invalid JSON", 400);
    }
    const machineId = (body.machineId ?? "").toString().trim();
    if (!machineId) return errorResponse("machineId is required", 400);
    const machine = await ctx.runQuery(internal.cloudMachines.getInternal, { machineId: machineId as any });
    if (!machine) return errorResponse("Machine not found", 404);
    if (String(machine.userId) !== String(session.userDocId)) {
      return errorResponse("Not your machine", 403);
    }
    await ctx.runMutation(api.cloudMachines.deprovision, { machineId: machineId as any });
    return jsonResponse({ ok: true, machineId, mode: "dev-deprovision", note: "snapshot+delete scheduled" });
  }),
});

/** GET /billing/yaver-cloud/balance — prepaid wallet read. Open to any
 *  cloud-access user; reading a zero balance is harmless. */
http.route({
  path: "/billing/yaver-cloud/balance",
  method: "GET",
  handler: httpAction(async (ctx, request) => {
    const session = await authenticateRequest(ctx, request);
    if (!session) return errorResponse("Unauthorized", 401);
    if (!cloudAccessAllowed(session.email, session.userDocId)) {
      return errorResponse("Yaver Cloud is private-preview only on this account", 403);
    }
    const wallet = await ctx.runQuery(internal.cloudLifecycle.getWallet, {
      userId: session.userDocId as any,
    });
    // Surface the per-SKU running rate + low-balance flag the wallet UI
    // shows ("~$X/hr running", "Add credit" nudge). cpu is the default
    // SKU; the floor is the safe minimum reserve for it.
    const hourlyCents = estimatedHourlyCents("cpu");
    const reservedCents = minimumReserveCents("cpu");
    // Included-this-month fuel gauges: the plan's included active-hours
    // (X of 40h left) and the managed-AI day's spend vs cap. Lets the UI
    // show what the flat price already covers before the wallet is touched.
    const allowance = await ctx.runQuery(internal.cloudLifecycle.getAllowance, {
      userId: session.userDocId as any,
      machineType: "cpu",
    });
    const pol = await ctx.runQuery(internal.gatewayPolicy.getAuthContext, {
      userId: session.userDocId as any,
    });
    const orKey = await ctx.runQuery(internal.openrouterKeys.getByUser, {
      userId: session.userDocId as any,
    });
    return jsonResponse({
      ok: true,
      ...wallet,
      prepaidBalanceCents: wallet.balanceCents,
      estimatedHourlyCents: hourlyCents,
      reservedCents,
      lowBalance: wallet.balanceCents <= reservedCents,
      allowance: {
        plan: allowance.plan,
        includedSeconds: allowance.includedSeconds,
        usedSeconds: allowance.usedSeconds,
        remainingSeconds: allowance.remainingSeconds,
      },
      inference: {
        enabled: pol.enabled,
        dailyCapCents: pol.dailyCapCents,
        spentTodayCents: pol.spentTodayCents,
      },
      // Per-user OpenRouter credit (managed inference). Read from the
      // stored mirror — no live OpenRouter call on this hot path. `limit`
      // is our COGS budget (margin already removed), so it reads lower than
      // the retail AI wallet by design. null when the seat has no key (byok).
      openrouterCredit: orKey
        ? {
            limitCents: orKey.limitCents,
            usageCents: orKey.usageCents ?? 0,
            remainingCents: Math.max(0, orKey.limitCents - (orKey.usageCents ?? 0)),
            status: orKey.status,
          }
        : null,
    });
  }),
});

/** GET /billing/status — buyer-side plan snapshot for the yaver_billing_status
 *  MCP tool (and any client): subscribed? which tier? active-hours + wallet
 *  left? Combines the subscription row with the included-allowance, wallet, and
 *  managed-inference gauges. Available to any authed user (no preview gate) so
 *  a prospective buyer can always see "no plan yet". */
http.route({
  path: "/billing/status",
  method: "GET",
  handler: httpAction(async (ctx, request) => {
    const session = await authenticateRequest(ctx, request);
    if (!session) return errorResponse("Unauthorized", 401);
    const sub = await ctx.runQuery(api.subscriptions.getByUser, {
      userId: session.userDocId as any,
    });
    const allowance = await ctx.runQuery(internal.cloudLifecycle.getAllowance, {
      userId: session.userDocId as any,
      machineType: "cpu",
    });
    const wallet = await ctx.runQuery(internal.cloudLifecycle.getWallet, {
      userId: session.userDocId as any,
    });
    const pol = await ctx.runQuery(internal.gatewayPolicy.getAuthContext, {
      userId: session.userDocId as any,
    });
    const subscribed = !!sub && (sub.status === "active" || sub.status === "past_due");
    // Managed inference on ⇒ hosted ($19 Agent); else the allowance plan
    // (byok/beta) or byok when subscribed without a recorded plan.
    const tier = pol.enabled ? "hosted" : (allowance.plan || (subscribed ? "byok" : null));
    return jsonResponse({
      ok: true,
      subscribed,
      tier,
      subscriptionStatus: sub?.status ?? null,
      currentPeriodEnd: sub?.currentPeriodEnd ?? null,
      cancelledAt: sub?.cancelledAt ?? null,
      includedHoursLeft: Math.round((allowance.remainingSeconds / 3600) * 10) / 10,
      walletCents: wallet.balanceCents,
      managedInference: pol.enabled === true,
    });
  }),
});

/** GET /billing/portal — the LemonSqueezy customer-portal URL for the user's
 *  active subscription (update payment / change plan / cancel). Fetched live
 *  from the LS API so it's always current; portalUrl is null when the user has
 *  no LS subscription yet, or a non-numeric (test) id, or LS isn't configured —
 *  callers fall back to the dashboard. */
http.route({
  path: "/billing/portal",
  method: "GET",
  handler: httpAction(async (ctx, request) => {
    const session = await authenticateRequest(ctx, request);
    if (!session) return errorResponse("Unauthorized", 401);
    const sub = await ctx.runQuery(api.subscriptions.getByUser, {
      userId: session.userDocId as any,
    });
    const lsId = sub?.lemonSqueezyId;
    const apiKey = lsEnv("API_KEY");
    if (!lsId || !apiKey) {
      return jsonResponse({ ok: true, portalUrl: null, reason: lsId ? "billing not configured" : "no active subscription" });
    }
    try {
      const resp = await fetch(`https://api.lemonsqueezy.com/v1/subscriptions/${lsId}`, {
        headers: { Authorization: `Bearer ${apiKey}`, Accept: "application/vnd.api+json" },
      });
      if (!resp.ok) {
        return jsonResponse({ ok: true, portalUrl: null, reason: `lemonsqueezy ${resp.status}` });
      }
      const data = await resp.json();
      const urls = data?.data?.attributes?.urls || {};
      return jsonResponse({
        ok: true,
        portalUrl: urls.customer_portal ?? null,
        updatePaymentUrl: urls.update_payment_method ?? null,
      });
    } catch (e) {
      return jsonResponse({ ok: true, portalUrl: null, reason: String(e) });
    }
  }),
});

/** POST /billing/yaver-cloud/change-plan — in-app Cloud Agent ⇄ Cloud
 *  Workspace switch. Body: {plan:"cloud-agent"|"cloud-workspace"}. Scoped
 *  to the caller's OWN active managed subscription (a user can never move
 *  another account). Downgrade applies immediately (managed AI off now);
 *  upgrade requires the LemonSqueezy variant swap to succeed first. */
http.route({
  path: "/billing/yaver-cloud/change-plan",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const session = await authenticateRequest(ctx, request);
    if (!session) return errorResponse("Unauthorized", 401);
    let body: any;
    try {
      body = await request.json();
    } catch {
      return errorResponse("Bad JSON", 400);
    }
    const targetPlan = body?.plan;
    if (targetPlan !== "cloud-agent" && targetPlan !== "cloud-workspace") {
      return errorResponse("plan must be 'cloud-agent' or 'cloud-workspace'", 400);
    }
    const sub = await ctx.runQuery(api.subscriptions.getByUser, {
      userId: session.userDocId as any,
    });
    if (!sub || sub.status !== "active") {
      return errorResponse("No active managed subscription to change", 400);
    }
    if (sub.plan !== "cloud-agent" && sub.plan !== "cloud-workspace") {
      return errorResponse("Current subscription is not a managed Cloud plan", 400);
    }
    if (sub.plan === targetPlan) {
      return jsonResponse({ ok: true, plan: targetPlan, unchanged: true });
    }
    const result = await ctx.runAction(internal.plans.changePlan, {
      userId: session.userDocId as any,
      lemonSqueezyId: sub.lemonSqueezyId ?? undefined,
      targetPlan,
    });
    if (!result.ok) {
      // Upgrade refused because billing isn't wired — surface honestly.
      return jsonResponse(
        { ok: false, reason: result.reason ?? "change-failed", plan: sub.plan },
        409,
      );
    }
    return jsonResponse({ ok: true, plan: targetPlan, tier: result.tier, billingSynced: result.billingSynced });
  }),
});

/** GET /billing/yaver-cloud/usage — recent wallet activity (metering
 *  ticks + top-ups) for the mobile/web Wallet ledger. */
http.route({
  path: "/billing/yaver-cloud/usage",
  method: "GET",
  handler: httpAction(async (ctx, request) => {
    const session = await authenticateRequest(ctx, request);
    if (!session) return errorResponse("Unauthorized", 401);
    if (!cloudAccessAllowed(session.email, session.userDocId)) {
      return errorResponse("Yaver Cloud is private-preview only on this account", 403);
    }
    const recent = await ctx.runQuery(internal.cloudLifecycle.getRecentUsage, {
      userId: session.userDocId as any,
      limit: 20,
    });
    return jsonResponse({ ok: true, ...recent });
  }),
});

// ── À-la-carte managed-service capability shelf ──────────────────────
// The web/mobile "build cockpit" (docs/yaver-normie-concierge-fair-
// metering.md). Each capability (Hermes reload, managed backend/web,
// always-on agent box, inference gateway, App-Store publish) is an
// independent per-user opt-in stored in userSettings.managedServices.
// Open to ANY authed user — the normie must SEE the shelf from t=0,
// before he has cloud access or any balance. Turning a switch on only
// marks intent; REAL billing still requires the global meter flag +
// wallet balance (the gate lives in managedMeter.recordManagedUsage).
// Session-scoped: a client only ever reads/writes its own row.

/** GET /managed/services — the caller's capability opt-in set. */
http.route({
  path: "/managed/services",
  method: "GET",
  handler: httpAction(async (ctx, request) => {
    const session = await authenticateRequest(ctx, request);
    if (!session) return errorResponse("Unauthorized", 401);
    const out = await ctx.runQuery(internal.managedServices.getServicesForUser, {
      userId: session.userDocId as any,
    });
    return jsonResponse({ ok: true, ...out });
  }),
});

/** POST /managed/services — toggle ONE capability {service, enabled}. */
http.route({
  path: "/managed/services",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const session = await authenticateRequest(ctx, request);
    if (!session) return errorResponse("Unauthorized", 401);
    let body: { service?: string; enabled?: boolean } = {};
    try {
      body = await request.json();
    } catch {
      return errorResponse("Bad body", 400);
    }
    if (!body.service || typeof body.enabled !== "boolean") {
      return errorResponse("service (string) and enabled (boolean) required", 400);
    }
    try {
      const out = await ctx.runMutation(internal.managedServices.setServiceForUser, {
        userId: session.userDocId as any,
        service: body.service,
        enabled: body.enabled,
      });
      return jsonResponse(out);
    } catch (e) {
      return errorResponse(e instanceof Error ? e.message : String(e), 400);
    }
  }),
});

/** GET /managed/cockpit?days=7 — wallet balance + enabled capabilities +
 *  burn-rate / days-left estimate. One fetch powers the shelf header. */
http.route({
  path: "/managed/cockpit",
  method: "GET",
  handler: httpAction(async (ctx, request) => {
    const session = await authenticateRequest(ctx, request);
    if (!session) return errorResponse("Unauthorized", 401);
    const days = Number(new URL(request.url).searchParams.get("days")) || undefined;
    const out = await ctx.runQuery(internal.managedServices.cockpitSummaryForUser, {
      userId: session.userDocId as any,
      days,
    });
    return jsonResponse({ ok: true, ...out });
  }),
});

/** GET /managed/burn?days=7 — honest per-capability spend breakdown. */
http.route({
  path: "/managed/burn",
  method: "GET",
  handler: httpAction(async (ctx, request) => {
    const session = await authenticateRequest(ctx, request);
    if (!session) return errorResponse("Unauthorized", 401);
    const days = Number(new URL(request.url).searchParams.get("days")) || undefined;
    const out = await ctx.runQuery(internal.managedServices.burnBreakdownForUser, {
      userId: session.userDocId as any,
      days,
    });
    return jsonResponse({ ok: true, ...out });
  }),
});

/** GET /byo/machines — the user's bring-your-own cloud boxes' lifecycle
 *  state (alive/sleeping/deleted + timestamps). Available to ANY authed
 *  user (BYO is the free, run-on-your-own-account plane). Session-scoped:
 *  only ever returns the caller's own rows; holds no credentials. */
http.route({
  path: "/byo/machines",
  method: "GET",
  handler: httpAction(async (ctx, request) => {
    const session = await authenticateRequest(ctx, request);
    if (!session) return errorResponse("Unauthorized", 401);
    const machines = await ctx.runQuery(internal.byoMachines.listForUserInternal, {
      userId: session.userDocId as any,
    });
    return jsonResponse({ ok: true, machines });
  }),
});

// POST /byo/provision-init — mint a BYO box bootstrap (device credential +
// self-bootstrapping cloud-init) so the PHONE can create the server on the
// user's own Hetzner account. No Hetzner token here: the phone holds it and
// calls api.hetzner.cloud directly. Returns the cloud-init user_data the
// phone bakes into the server. project: phone-direct hcloud provision-to-vibe.
http.route({
  path: "/byo/provision-init",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const session = await authenticateRequest(ctx, request);
    if (!session) return errorResponse("Unauthorized", 401);
    let body: { machineType?: string; region?: string } = {};
    try {
      body = await request.json();
    } catch {
      /* defaults below */
    }
    const machineType = body.machineType === "gpu" ? "gpu" : "cpu";
    const region = (body.region ?? "eu").trim() === "us" ? "us" : "eu";
    // Anti-fan-out: cap BYO rows per user the same way managed does.
    const MAX = Number(process.env.YAVER_CLOUD_MAX_MACHINES_PER_USER) || 10;
    const existing = await ctx.runQuery(api.cloudMachines.listForUser, { userId: session.userDocId as any });
    const live = Array.isArray(existing) ? existing.filter((m: any) => m.status && m.status !== "stopped").length : 0;
    if (live >= MAX) {
      return jsonResponse({ ok: false, error: `Machine limit reached (${MAX}).` }, 409);
    }
    try {
      const out = await ctx.runAction(internal.cloudMachines.mintByoBootstrap, {
        userId: session.userDocId as any,
        machineType,
        region,
      });
      return jsonResponse({ ok: true, ...out });
    } catch (e) {
      return jsonResponse({ ok: false, error: e instanceof Error ? e.message : String(e) }, 500);
    }
  }),
});

// POST /byo/provision-complete — the phone reports the Hetzner id/ip it just
// created so the row can manage it later. The box itself also self-registers
// as a device (via the baked session) and beacons phases over X-Machine-Token.
http.route({
  path: "/byo/provision-complete",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const session = await authenticateRequest(ctx, request);
    if (!session) return errorResponse("Unauthorized", 401);
    let body: { machineId?: string; hetznerServerId?: string; serverIp?: string } = {};
    try {
      body = await request.json();
    } catch {
      return errorResponse("Bad body", 400);
    }
    if (!body.machineId || !body.hetznerServerId) {
      return errorResponse("machineId + hetznerServerId required", 400);
    }
    // Ownership check: the row must belong to the caller.
    const machine = await ctx.runQuery(api.cloudMachines.get, { machineId: body.machineId as any }).catch(() => null);
    if (!machine || String(machine.userId) !== String(session.userDocId)) {
      return errorResponse("Not found", 404);
    }
    await ctx.runMutation(internal.cloudMachines.setProvisioned, {
      machineId: body.machineId as any,
      hetznerServerId: body.hetznerServerId,
      serverIp: body.serverIp ?? "",
      hostname: body.serverIp ?? "",
    });
    return jsonResponse({ ok: true });
  }),
});

/** POST /billing/yaver-cloud/provision — prepaid spin-up. Create a new
 *  managed box funded by the wallet (NO subscription). Balance-gated:
 *  402 if the wallet can't cover the safe reserve for this SKU. The
 *  real Hetzner create is still gated on HCLOUD_TOKEN inside
 *  cloudMachines.provision (fail-closed in prod). Body:
 *  { machineType?: "cpu"|"gpu", region?: "eu"|"us", planId?: "cloud-agent"|"cloud-workspace" }. */
http.route({
  path: "/billing/yaver-cloud/provision",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const session = await authenticateRequest(ctx, request);
    if (!session) return errorResponse("Unauthorized", 401);
    if (!cloudAccessAllowed(session.email, session.userDocId)) {
      return errorResponse("Yaver Cloud is private-preview only on this account", 403);
    }
    let body: { machineType?: string; region?: string; planId?: string } = {};
    try {
      body = await request.json();
    } catch {
      // allow empty body — defaults below
    }
    const machineType = body.machineType === "gpu" ? "gpu" : "cpu";
    const region = (body.region ?? "eu").trim() === "us" ? "us" : "eu";
    const planId = normalizeCloudPurchasePlan(body.planId);

    // Anti-fan-out (public repo — a buyer must not turn credit for ONE
    // box into N boxes by firing concurrent provisions). Count the
    // user's existing non-stopped managed boxes and require the wallet
    // to cover the reserve for ALL of them + the new one. Also cap the
    // absolute count. This bounds the TOCTOU window of canStart (which
    // only checks a single-box reserve) to nothing meaningful.
    const MAX_MACHINES = Number(process.env.YAVER_CLOUD_MAX_MACHINES_PER_USER) || 10;
    const existing = await ctx.runQuery(api.cloudMachines.listForUser, {
      userId: session.userDocId as any,
    });
    const liveCount = Array.isArray(existing)
      ? existing.filter((m: any) => m.status && m.status !== "stopped").length
      : 0;
    if (liveCount >= MAX_MACHINES) {
      return jsonResponse({
        ok: false,
        error: `Machine limit reached (${MAX_MACHINES}). Decommission one first.`,
      }, 409);
    }
    const gate = await ctx.runQuery(internal.cloudLifecycle.canStart, {
      userId: session.userDocId as any,
      machineType,
    });
    // Reserve must cover every live box plus this one — not just one.
    const requiredCents = gate.requiredCents * (liveCount + 1);
    if (!gate.ok || gate.balanceCents < requiredCents) {
      return jsonResponse({
        ok: false,
        error: "Insufficient prepaid balance — add credit to spin up a box",
        balanceCents: gate.balanceCents,
        requiredCents,
      }, 402);
    }

    try {
      const machineId = await ctx.runMutation(api.cloudMachines.create, {
        userId: session.userDocId as any,
        machineType,
        region,
        // No subscriptionId: this is a prepaid (wallet-funded) box.
        tier: "byok",
      });
      return jsonResponse({ ok: true, machineId, machineType, region, planId, mode: "prepaid" });
    } catch (error) {
      return errorResponse(error instanceof Error ? error.message : String(error), 500);
    }
  }),
});

/** POST /billing/yaver-cloud/topup-dev — owner-dev prepaid credit stub.
 *  This is deliberately owner-gated and uses the P0 ledger primitive; no
 *  LemonSqueezy charge is created until the real prepaid checkout lands. */
http.route({
  path: "/billing/yaver-cloud/topup-dev",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const session = await authenticateRequest(ctx, request);
    if (!session) return errorResponse("Unauthorized", 401);
    if (!isCloudPreviewUser(session.email, session.userDocId)) {
      return errorResponse("Owner-only (private preview) on this account", 403);
    }
    const body = await request.json().catch(() => ({}));
    const amountCents = Number(body.amountCents ?? 1000);
    if (!Number.isFinite(amountCents) || amountCents <= 0 || amountCents > 100_000) {
      return errorResponse("amountCents must be between 1 and 100000", 400);
    }
    const result = await ctx.runMutation(internal.cloudLifecycle.topUp, {
      userId: session.userDocId as any,
      amountCents: Math.round(amountCents),
    });
    return jsonResponse({ ok: true, mode: "owner-dev", ...result });
  }),
});

/** POST /billing/yaver-cloud/stop — owner-only PAUSE: snapshot the box,
 *  then DELETE the Hetzner server (a powered-off server still bills
 *  full price; only delete stops the charge). Status → "paused",
 *  snapshot id kept for resume. Real Hetzner calls when HCLOUD_TOKEN is
 *  set; fail-closed dry-run state transition otherwise. */
http.route({
  path: "/billing/yaver-cloud/stop",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const session = await authenticateRequest(ctx, request);
    if (!session) return errorResponse("Unauthorized", 401);
    if (!cloudAccessAllowed(session.email, session.userDocId)) {
      return errorResponse("Yaver Cloud is private-preview only on this account", 403);
    }
    const body = await request.json().catch(() => ({}));
    const machineId = String(body.machineId ?? "").trim();
    if (!machineId) return errorResponse("machineId is required", 400);
    const machine = await ctx.runQuery(internal.cloudMachines.getInternal, {
      machineId: machineId as any,
    });
    if (!machine) return errorResponse("Machine not found", 404);
    if (String(machine.userId) !== String(session.userDocId)) {
      return errorResponse("Not your machine", 403);
    }
    // Idempotent: already parked.
    if (machine.status === "paused" || machine.status === "stopped" || machine.status === "stopping") {
      return jsonResponse({ ok: true, machineId, status: machine.status, dryRun: true });
    }
    // P3: delegate to the real, Hetzner-integrated lifecycle (P2).
    // It is FAIL-CLOSED dry-run when HCLOUD_TOKEN is unset (prod
    // default — no real spend); real snapshot+delete only when an
    // owner deliberately sets the platform token.
    const r = await ctx.runAction(internal.cloudLifecycle.pauseMachine, {
      machineId: machineId as any,
    });
    return jsonResponse({ machineId, ...r }, r.ok ? 200 : 409);
  }),
});

/** POST /billing/yaver-cloud/start — owner-only RESUME: recreate the
 *  Hetzner server from the pause snapshot, re-point DNS, status →
 *  "active". Balance-gated against the prepaid reserve (402 below it).
 *  Real Hetzner calls when HCLOUD_TOKEN is set; dry-run otherwise. */
http.route({
  path: "/billing/yaver-cloud/start",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const session = await authenticateRequest(ctx, request);
    if (!session) return errorResponse("Unauthorized", 401);
    if (!cloudAccessAllowed(session.email, session.userDocId)) {
      return errorResponse("Yaver Cloud is private-preview only on this account", 403);
    }
    const body = await request.json().catch(() => ({}));
    const machineId = String(body.machineId ?? "").trim();
    if (!machineId) return errorResponse("machineId is required", 400);
    const machine = await ctx.runQuery(internal.cloudMachines.getInternal, {
      machineId: machineId as any,
    });
    if (!machine) return errorResponse("Machine not found", 404);
    if (String(machine.userId) !== String(session.userDocId)) {
      return errorResponse("Not your machine", 403);
    }
    if (machine.status === "active") {
      return jsonResponse({ ok: true, machineId, status: "active", dryRun: true });
    }
    // Keep the explicit 402 balance contract mobile/web depend on.
    const gate = await ctx.runQuery(internal.cloudLifecycle.canStart, {
      userId: session.userDocId as any,
      machineType: String(machine.machineType || "cpu"),
    });
    if (!gate.ok) {
      return jsonResponse({
        ok: false,
        error: "Insufficient prepaid balance",
        balanceCents: gate.balanceCents,
        requiredCents: gate.requiredCents,
      }, 402);
    }
    // P3: delegate to the real, Hetzner-integrated lifecycle (P2) —
    // recreate-from-snapshot, fail-closed dry-run when HCLOUD_TOKEN
    // is unset (prod default; no real spend).
    const r = await ctx.runAction(internal.cloudLifecycle.resumeMachine, {
      machineId: machineId as any,
    });
    if (r.ok) {
      await ctx.runMutation(internal.cloudMachines.setPhase, {
        machineId: machineId as any,
        phase: machine.runnersAuthorized === false ? "authorizing-runners" : "ready",
        progress: machine.runnersAuthorized === false ? 90 : 100,
      });
    }
    return jsonResponse(
      { machineId, balanceCents: gate.balanceCents, requiredCents: gate.requiredCents, ...r },
      r.ok ? 200 : 409,
    );
  }),
});

/** GET /subscription — Get subscription and managed relay status (authenticated). */
http.route({
  path: "/subscription",
  method: "GET",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) return errorResponse("Unauthorized", 401);
    const tokenHash = await sha256Hex(authHeader.slice(7));

    const session = await ctx.runQuery(api.auth.validateSession, { tokenHash });
    if (!session) return errorResponse("Unauthorized", 401);

    // Get user doc to get _id
    const userDocId = await ctx.runQuery(api.auth.getUserDocId, { tokenHash });
    if (!userDocId) return errorResponse("User not found", 404);

    const [subscription, relay, machines, wallet, beta] = await Promise.all([
      ctx.runQuery(api.subscriptions.getByUser, { userId: userDocId }),
      ctx.runQuery(api.managedRelays.getByUser, { userId: userDocId }),
      ctx.runQuery(api.cloudMachines.listForUser, { userId: userDocId }),
      ctx.runQuery(internal.cloudLifecycle.getWallet, { userId: userDocId }),
      ctx.runQuery(internal.betaAccess.getBetaStatus, { userId: userDocId }),
    ]);

    return jsonResponse({
      // Owner-allowlist flag (server is the source of truth — the
      // CLOUD_PREVIEW_OWNER_EMAIL gate, never a hardcoded name). The
      // web hides the entire managed-cloud panel/billing for
      // non-owners; this is COSMETIC only — every money-spending
      // route is independently 403'd by isCloudPreviewUser and
      // provisioning is fail-closed behind canProvisionManaged, so a
      // non-owner reading this open-source code still cannot spend
      // Yaver's Hetzner. Private preview until LemonSqueezy is fully
      // integrated; owner-only purchases for now.
      cloudPreviewOwner: isCloudPreviewUser(session.email, session.userDocId),
      // True when this account may use the prepaid-cloud surfaces — the
      // owner allowlist OR the YAVER_CLOUD_PUBLIC launch flag. Mobile/web
      // render the wallet + machine controls when this is true; still
      // cosmetic (every money route is independently gated server-side).
      cloudAccess: cloudAccessAllowed(session.email, session.userDocId),
      subscription: subscription ? {
        plan: subscription.plan,
        status: subscription.status,
        currentPeriodEnd: subscription.currentPeriodEnd,
        cancelledAt: subscription.cancelledAt,
      } : null,
      prepaidBalanceCents: wallet.balanceCents,
      currency: wallet.currency,
      balance: wallet,
      // Beta entitlement (invisible-infra-share). When isBeta, clients
      // render the Beta workspace view (project + vibe box) and the "Beta"
      // badge — never the infra/guest/device details (those stay hidden).
      beta,
      relay: relay ? {
        status: relay.status,
        domain: relay.domain,
        region: relay.region,
        quicPort: relay.quicPort,
        httpPort: relay.httpPort,
      } : null,
      machines: Array.isArray(machines)
        ? machines.map((machine) => ({
            id: String(machine._id),
            machineType: machine.machineType,
            status: machine.status,
            hostname: machine.hostname,
            serverIp: machine.serverIp,
            region: machine.region,
            errorMessage: machine.errorMessage,
            subscriptionId: machine.subscriptionId ? String(machine.subscriptionId) : null,
            // Surfaced so the web Recycle/Remove dialog can resolve the
            // exact cloud resource of a managed box from stored state —
            // the user never has to recall it. Still an exact id (never
            // fuzzy-matched); the dialog matches on deviceId/ip. The UI
            // stays provider-neutral; `provider` tells the agent facade
            // which API to call. hetznerServerId kept for back-compat.
            provider: machine.provider ?? "hetzner",
            cloudResourceId: machine.cloudResourceId ?? machine.hetznerServerId ?? null,
            hetznerServerId: machine.hetznerServerId ?? null,
            deviceId: machine.deviceId ?? null,
            // First-class onboarding: web/mobile render an
            // initializing state + progress bar + "Authorize runners"
            // from these (project_managed_cloud_onboarding_gap).
            provisionPhase: machine.provisionPhase ?? null,
            provisionProgress:
              typeof machine.provisionProgress === "number"
                ? machine.provisionProgress
                : null,
            // Short curated failure label the box itself beaconed
            // (phase="error"); drives the synthetic "Setting up" card's
            // failure state + recovery hint in web/mobile.
            provisionError: machine.provisionError ?? null,
            // "golden" ⇒ fast boot from a prebuilt snapshot; "vanilla" ⇒
            // ubuntu-24.04 with a 3–5 min first-boot build. Lets the card
            // show the right "setting up" expectation.
            bootImageSource: machine.bootImageSource ?? null,
            runnersAuthorized: machine.runnersAuthorized ?? false,
          }))
        : [],
    });
  }),
});

// --- Teams (shared machines, multi-user) ---

/** GET /teams — List teams for the authenticated user. */
http.route({
  path: "/teams",
  method: "GET",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) return errorResponse("Unauthorized", 401);
    const tokenHash = await sha256Hex(authHeader.slice(7));

    const session = await ctx.runQuery(api.auth.validateSession, { tokenHash });
    if (!session) return errorResponse("Unauthorized", 401);

    const userDocId = await ctx.runQuery(api.auth.getUserDocId, { tokenHash });
    if (!userDocId) return errorResponse("User not found", 404);

    const teams = await ctx.runQuery(api.teams.getTeamsForUser, { userId: userDocId });
    return jsonResponse({ teams });
  }),
});

/** POST /teams — Create a team. */
http.route({
  path: "/teams",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) return errorResponse("Unauthorized", 401);
    const tokenHash = await sha256Hex(authHeader.slice(7));

    const session = await ctx.runQuery(api.auth.validateSession, { tokenHash });
    if (!session) return errorResponse("Unauthorized", 401);

    const userDocId = await ctx.runQuery(api.auth.getUserDocId, { tokenHash });
    if (!userDocId) return errorResponse("User not found", 404);

    const body = await request.json();
    const teamId = await ctx.runMutation(api.teams.create, {
      name: body.name || "My Team",
      ownerId: userDocId,
      plan: body.plan || "cpu",
      maxMembers: body.maxMembers || 10,
    });

    return jsonResponse({ teamId });
  }),
});

/** POST /teams/members — Add a member to a team. */
http.route({
  path: "/teams/members",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) return errorResponse("Unauthorized", 401);
    const tokenHash = await sha256Hex(authHeader.slice(7));

    const session = await ctx.runQuery(api.auth.validateSession, { tokenHash });
    if (!session) return errorResponse("Unauthorized", 401);

    const userDocId = await ctx.runQuery(api.auth.getUserDocId, { tokenHash });
    if (!userDocId) return errorResponse("User not found", 404);

    const body = await request.json();
    if (!body.teamId || !body.email) return errorResponse("teamId and email required", 400);

    // Verify caller is team admin
    const team = await ctx.runQuery(api.teams.getByTeamId, { teamId: body.teamId });
    if (!team) return errorResponse("Team not found", 404);

    const isMember = await ctx.runQuery(api.teams.isMember, { teamId: body.teamId, userId: userDocId });
    if (!isMember) return errorResponse("Not a team member", 403);

    try {
      const result = await ctx.runMutation(api.teams.addMember, {
        teamId: body.teamId,
        userEmail: body.email,
        role: body.role || "member",
        invitedBy: userDocId,
      });
      return jsonResponse(result);
    } catch (e: unknown) {
      const msg = e instanceof Error ? e.message : String(e);
      return errorResponse(msg, 400);
    }
  }),
});

/** GET /teams/members?teamId=xxx — List members of a team. */
http.route({
  path: "/teams/members",
  method: "GET",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) return errorResponse("Unauthorized", 401);
    const tokenHash = await sha256Hex(authHeader.slice(7));

    const session = await ctx.runQuery(api.auth.validateSession, { tokenHash });
    if (!session) return errorResponse("Unauthorized", 401);

    const url = new URL(request.url);
    const teamId = url.searchParams.get("teamId");
    if (!teamId) return errorResponse("teamId required", 400);

    const members = await ctx.runQuery(api.teams.listMembers, { teamId });
    return jsonResponse({ members });
  }),
});

/** GET /teams/validate?teamId=xxx — Check if authenticated user is a team member.
 *  Used by the multi-user agent to validate team access. */
http.route({
  path: "/teams/validate",
  method: "GET",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) return errorResponse("Unauthorized", 401);
    const tokenHash = await sha256Hex(authHeader.slice(7));

    const session = await ctx.runQuery(api.auth.validateSession, { tokenHash });
    if (!session) return errorResponse("Unauthorized", 401);

    const userDocId = await ctx.runQuery(api.auth.getUserDocId, { tokenHash });
    if (!userDocId) return errorResponse("User not found", 404);

    const url = new URL(request.url);
    const teamId = url.searchParams.get("teamId");
    if (!teamId) return errorResponse("teamId required", 400);

    const isMember = await ctx.runQuery(api.teams.isMember, { teamId, userId: userDocId });
    return jsonResponse({ isMember, teamId, userId: session.userId });
  }),
});

// --- Company AI Options (Talos/Yaver mode tenant policy) ---

/** GET /company-ai/options?teamId=team_xxx — Read company AI policy.
 *  Returns safe defaults when the team has not configured AI yet. */
http.route({
  path: "/company-ai/options",
  method: "GET",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) return errorResponse("Unauthorized", 401);
    const tokenHash = await sha256Hex(authHeader.slice(7));

    const url = new URL(request.url);
    const teamId = url.searchParams.get("teamId");
    if (!teamId) return errorResponse("teamId required", 400);

    const result = await ctx.runQuery(api.companyAIOptions.getByToken, { tokenHash, teamId });
    if (!result) return errorResponse("Team not found or access denied", 404);
    return jsonResponse({ ok: true, ...result });
  }),
});

/** POST /company-ai/options — Update company AI policy.
 *  Only team admins may write. Body: {teamId, options}. */
http.route({
  path: "/company-ai/options",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) return errorResponse("Unauthorized", 401);
    const tokenHash = await sha256Hex(authHeader.slice(7));

    const body = await request.json();
    if (!body?.teamId || !body?.options) {
      return errorResponse("teamId and options are required", 400);
    }

    try {
      const id = await ctx.runMutation(api.companyAIOptions.setByToken, {
        tokenHash,
        teamId: body.teamId,
        options: body.options,
      });
      return jsonResponse({ ok: true, id });
    } catch (e: any) {
      return errorResponse(e?.message || "Failed to update company AI options", 403);
    }
  }),
});

/** POST /company-ai/resolve — Resolve Talos/Yaver mode runtime for a work kind.
 *  Returns no secrets; clients use the selected Yaver device + existing agent endpoints. */
http.route({
  path: "/company-ai/resolve",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) return errorResponse("Unauthorized", 401);
    const tokenHash = await sha256Hex(authHeader.slice(7));

    const body = await request.json();
    if (!body?.teamId || !body?.workKind) {
      return errorResponse("teamId and workKind are required", 400);
    }

    const result = await ctx.runQuery(api.companyAIOptions.resolveForToken, {
      tokenHash,
      teamId: body.teamId,
      workKind: body.workKind,
      requestedRunner: body.requestedRunner,
      requestedModel: body.requestedModel,
      requestedProvider: body.requestedProvider,
      requestedDeviceId: body.requestedDeviceId,
      source: body.source,
    });
    if (!result) return errorResponse("Team not found or access denied", 404);
    return jsonResponse(result);
  }),
});

// --- Cloud Machines ---

/** GET /machines — List cloud machines for the authenticated user. */
http.route({
  path: "/machines",
  method: "GET",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) return errorResponse("Unauthorized", 401);
    const tokenHash = await sha256Hex(authHeader.slice(7));

    const session = await ctx.runQuery(api.auth.validateSession, { tokenHash });
    if (!session) return errorResponse("Unauthorized", 401);

    const userDocId = await ctx.runQuery(api.auth.getUserDocId, { tokenHash });
    if (!userDocId) return errorResponse("User not found", 404);

    const machines = await ctx.runQuery(api.cloudMachines.listForUser, { userId: userDocId });
    return jsonResponse({ machines });
  }),
});

/** POST /machines — Create a cloud machine (called by webhook or admin). */
http.route({
  path: "/machines",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) return errorResponse("Unauthorized", 401);
    const tokenHash = await sha256Hex(authHeader.slice(7));

    const session = await ctx.runQuery(api.auth.validateSession, { tokenHash });
    if (!session) return errorResponse("Unauthorized", 401);

    const userDocId = await ctx.runQuery(api.auth.getUserDocId, { tokenHash });
    if (!userDocId) return errorResponse("User not found", 404);

    const body = await request.json();
    const machineId = await ctx.runMutation(api.cloudMachines.create, {
      userId: userDocId,
      machineType: body.machineType || "cpu",
      teamId: body.teamId,
      region: body.region || "eu",
      repoUrl: body.repoUrl,
      sshPublicKey: body.sshPublicKey,
    });

    return jsonResponse({ machineId });
  }),
});

// --- Chat assistant (landing page help bot) ---

http.route({
  path: "/chat",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const OPENROUTER_API_KEY = process.env.OPENROUTER_API_KEY;
    if (!OPENROUTER_API_KEY) {
      return jsonResponse({ error: "Chat not configured" }, 503);
    }

    let body;
    try {
      body = await request.json();
    } catch {
      return jsonResponse({ error: "Invalid JSON" }, 400);
    }

    const userMessage = body.message?.trim();
    if (!userMessage || userMessage.length > 500) {
      return jsonResponse({ error: "Message required (max 500 chars)" }, 400);
    }

    const systemPrompt = `You are Yaver's help assistant on the yaver.io website. Yaver is a free, open-source P2P tool that lets developers control AI coding agents (Claude Code, Codex, Aider, Ollama, etc.) from their phone, desktop, or any terminal.

Your ONLY purpose is to help users with Yaver-related questions:
- How to install and set up Yaver (CLI, mobile app, desktop GUI)
- How to connect devices, set up relay servers, use Tailscale
- How to use features: tasks, exec/RPC, session transfer, scheduling, notifications
- How to integrate: Telegram bot, Discord, Slack, CI/CD webhooks, MCP tools
- How to use SDKs (Go, Python, JS/TS, Flutter/Dart)
- How to self-host a relay server

Rules:
- Keep answers short (2-4 sentences). Link to docs when relevant.
- If the question is NOT about Yaver, politely say: "I can only help with Yaver-related questions. Check out yaver.io/docs for guides, or yaver.io/faq for common questions."
- Never make up features that don't exist.
- Key links: yaver.io/docs, yaver.io/manuals, yaver.io/download, yaver.io/manuals/integrations
- Yaver's open-source stack is free to self-host. Optional Yaver Cloud is web-billed managed infrastructure: saved cloud workspaces, private relay, and auto-stop. The mobile app controls already-owned machines and does not sell managed cloud inside the app stores.
- Privacy-first: code never leaves the developer's machines. Relay is pass-through, encrypted.`;

    try {
      const resp = await fetch("https://openrouter.ai/api/v1/chat/completions", {
        method: "POST",
        headers: {
          "Authorization": `Bearer ${OPENROUTER_API_KEY}`,
          "Content-Type": "application/json",
          "HTTP-Referer": "https://yaver.io",
          "X-Title": "Yaver Help",
        },
        body: JSON.stringify({
          model: "meta-llama/llama-4-scout",
          messages: [
            { role: "system", content: systemPrompt },
            ...(body.history || []).slice(-4),
            { role: "user", content: userMessage },
          ],
          max_tokens: 300,
          temperature: 0.3,
        }),
      });

      if (!resp.ok) {
        return jsonResponse({ error: "AI service unavailable" }, 502);
      }

      const data = await resp.json() as any;
      const reply = data.choices?.[0]?.message?.content || "Sorry, I couldn't generate a response.";

      return new Response(JSON.stringify({ ok: true, reply }), {
        status: 200,
        headers: {
          "Content-Type": "application/json",
          "Access-Control-Allow-Origin": "*",
        },
      });
    } catch {
      return jsonResponse({ error: "Chat error" }, 500);
    }
  }),
});

// CORS preflight for /chat
http.route({
  path: "/chat",
  method: "OPTIONS",
  handler: httpAction(async () => {
    return new Response(null, {
      status: 204,
      headers: {
        "Access-Control-Allow-Origin": "*",
        "Access-Control-Allow-Methods": "POST, OPTIONS",
        "Access-Control-Allow-Headers": "Content-Type",
      },
    });
  }),
});

// ── SDK Tokens ──────────────────────────────────────────────────────

/**
 * POST /sdk/token — Create an SDK token for the Feedback SDK.
 * Requires a valid CLI session token (Bearer auth).
 * Body: { label?, scopes?, allowedCIDRs?, expiresInMs? }
 * Returns: { token, expiresAt }
 */
http.route({
  path: "/sdk/token",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const user = await authenticateRequest(ctx, request);
    if (!user) return errorResponse("Unauthorized", 401);

    const tokenBytes = new Uint8Array(32);
    crypto.getRandomValues(tokenBytes);
    const sdkToken = Array.from(tokenBytes)
      .map((b) => b.toString(16).padStart(2, "0"))
      .join("");
    const sdkTokenHash = await sha256Hex(sdkToken);

    const authHeader = request.headers.get("Authorization")!;
    const sessionTokenHash = await sha256Hex(authHeader.slice(7));

    let label: string | undefined;
    let scopes: string[] | undefined;
    let allowedCIDRs: string[] | undefined;
    let expiresInMs: number | undefined;
    try {
      const body = await request.json();
      label = body.label;
      scopes = body.scopes;
      allowedCIDRs = body.allowedCIDRs;
      expiresInMs = body.expiresInMs;
    } catch {
      // No body — use defaults
    }

    const result = await ctx.runMutation(api.auth.createSdkToken, {
      tokenHash: sdkTokenHash,
      sessionTokenHash,
      label,
      scopes,
      allowedCIDRs,
      expiresInMs,
    });

    return jsonResponse({ token: sdkToken, expiresAt: result.expiresAt });
  }),
});

http.route({
  path: "/sdk/token",
  method: "OPTIONS",
  handler: httpAction(async () => {
    return new Response(null, {
      status: 204,
      headers: {
        "Access-Control-Allow-Origin": "*",
        "Access-Control-Allow-Methods": "POST, OPTIONS",
        "Access-Control-Allow-Headers": "Authorization, Content-Type",
      },
    });
  }),
});

/**
 * GET /sdk/token/validate — Validate an SDK token (used by agent auth middleware).
 * Returns: { user: { userId, email, fullName, provider, scopes, allowedCIDRs } }
 */
http.route({
  path: "/sdk/token/validate",
  method: "GET",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) {
      return errorResponse("Unauthorized", 401);
    }
    const token = authHeader.slice(7);
    const tokenHash = await sha256Hex(token);

    const result = await ctx.runQuery(api.auth.validateSdkToken, { tokenHash });
    if (!result) {
      return errorResponse("Invalid or expired SDK token", 401);
    }

    return jsonResponse({ user: result });
  }),
});

/**
 * POST /guests/sdk-token — Mint a delegated Feedback SDK token for a repo-scoped
 * guest grant. Bearer auth: guest's normal session token.
 * Body: { hostUserId, targetDeviceId }
 */
http.route({
  path: "/guests/sdk-token",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const user = await authenticateRequest(ctx, request);
    if (!user) return errorResponse("Unauthorized", 401);

    let body: { hostUserId?: string; targetDeviceId?: string };
    try {
      body = await request.json();
    } catch {
      return errorResponse("Invalid JSON", 400);
    }
    if (!body.hostUserId) return errorResponse("hostUserId is required");
    if (!body.targetDeviceId) return errorResponse("targetDeviceId is required");

    const authHeader = request.headers.get("Authorization")!;
    const guestTokenHash = await sha256Hex(authHeader.slice(7));

    const tokenBytes = new Uint8Array(32);
    crypto.getRandomValues(tokenBytes);
    const sdkToken = Array.from(tokenBytes)
      .map((b) => b.toString(16).padStart(2, "0"))
      .join("");
    const sdkTokenHash = await sha256Hex(sdkToken);

    const result = await ctx.runMutation((api as any).guests.mintGuestFeedbackSdkToken, {
      guestTokenHash,
      sdkTokenHash,
      hostUserId: body.hostUserId,
      targetDeviceId: body.targetDeviceId,
    });

    return jsonResponse({
      token: sdkToken,
      expiresAt: result.expiresAt,
      allowedProjects: result.allowedProjects ?? [],
      sourceSurface: "feedback-sdk",
    });
  }),
});

http.route({
  path: "/guests/sdk-token",
  method: "OPTIONS",
  handler: httpAction(async () => {
    return new Response(null, {
      status: 204,
      headers: {
        "Access-Control-Allow-Origin": "*",
        "Access-Control-Allow-Methods": "POST, OPTIONS",
        "Access-Control-Allow-Headers": "Authorization, Content-Type",
      },
    });
  }),
});

/**
 * POST /sdk/token/rotate — Rotate an SDK token.
 * Bearer auth: current SDK token.
 * Returns: { token, expiresAt } (new token; old valid for 5min grace).
 */
http.route({
  path: "/sdk/token/rotate",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) {
      return errorResponse("Unauthorized", 401);
    }
    const currentToken = authHeader.slice(7);
    const currentTokenHash = await sha256Hex(currentToken);

    // Generate new token
    const tokenBytes = new Uint8Array(32);
    crypto.getRandomValues(tokenBytes);
    const newToken = Array.from(tokenBytes)
      .map((b) => b.toString(16).padStart(2, "0"))
      .join("");
    const newTokenHash = await sha256Hex(newToken);

    const result = await ctx.runMutation(api.auth.rotateSdkToken, {
      currentTokenHash,
      newTokenHash,
    });

    return jsonResponse({ token: newToken, expiresAt: result.expiresAt });
  }),
});

http.route({
  path: "/sdk/token/rotate",
  method: "OPTIONS",
  handler: httpAction(async () => {
    return new Response(null, {
      status: 204,
      headers: {
        "Access-Control-Allow-Origin": "*",
        "Access-Control-Allow-Methods": "POST, OPTIONS",
        "Access-Control-Allow-Headers": "Authorization, Content-Type",
      },
    });
  }),
});

/**
 * POST /sdk/token/revoke — Revoke an SDK token.
 * Bearer auth: CLI session token. Body: { sdkToken }
 */
http.route({
  path: "/sdk/token/revoke",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const user = await authenticateRequest(ctx, request);
    if (!user) return errorResponse("Unauthorized", 401);

    const authHeader = request.headers.get("Authorization")!;
    const sessionTokenHash = await sha256Hex(authHeader.slice(7));

    let sdkToken: string;
    try {
      const body = await request.json();
      sdkToken = body.sdkToken;
    } catch {
      return errorResponse("Missing sdkToken in body", 400);
    }

    const sdkTokenHash = await sha256Hex(sdkToken);
    await ctx.runMutation(api.auth.revokeSdkToken, {
      sessionTokenHash,
      sdkTokenHash,
    });

    return jsonResponse({ ok: true });
  }),
});

// ── Security Events ─────────────────────────────────────────────────

/** POST /security/event — Report a security event from the agent. */
http.route({
  path: "/security/event",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) {
      return errorResponse("Unauthorized", 401);
    }
    const token = authHeader.slice(7);
    const tokenHash = await sha256Hex(token);

    let body: { eventType: string; details: string };
    try {
      body = await request.json();
    } catch {
      return errorResponse("Invalid body", 400);
    }

    await ctx.runMutation(api.auth.reportSecurityEvent, {
      tokenHash,
      eventType: body.eventType,
      details: body.details,
    });

    return jsonResponse({ ok: true });
  }),
});

/** GET /security/events — List recent security events for the authenticated user. */
http.route({
  path: "/security/events",
  method: "GET",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) {
      return errorResponse("Unauthorized", 401);
    }
    const token = authHeader.slice(7);
    const tokenHash = await sha256Hex(token);

    const events = await ctx.runQuery(api.auth.listSecurityEvents, { tokenHash });
    return jsonResponse({ events });
  }),
});

// ── Guest Access ────────────────────────────────────────────────────

/** POST /guests/invite — Invite a guest by email. Host only. */
http.route({
  path: "/guests/invite",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) {
      return errorResponse("Unauthorized", 401);
    }
    const token = authHeader.slice(7);
    const tokenHash = await sha256Hex(token);

    const body = await request.json();
    const email = typeof body.email === "string" ? body.email : undefined;
    const userId = typeof body.userId === "string" ? body.userId : undefined;
    const proposedDeviceIds = Array.isArray(body.deviceIds) ? body.deviceIds.map(String) : undefined;
    const scope =
      body.scope === "full" || body.scope === "feedback-only" || body.scope === "sdk-project"
        ? body.scope
        : undefined;
    const allowedProjects = Array.isArray(body.allowedProjects)
      ? body.allowedProjects.map(String)
      : undefined;
    if (!email && !userId) {
      return errorResponse("email or userId is required");
    }

    try {
      const result = await ctx.runMutation(api.guests.invite, {
        tokenHash,
        guestEmail: email,
        guestUserId: userId,
        proposedDeviceIds,
        scope,
        allowedProjects,
      });
      return jsonResponse({
        ok: true,
        inviteCode: result.inviteCode,
        guestRegistered: result.guestRegistered,
        guestUserId: result.guestUserId,
        guestEmail: result.guestEmail,
      });
    } catch (e: any) {
      return errorResponse(e.message || "Failed to invite guest", 400);
    }
  }),
});

/** POST /guests/accept — Accept a pending invitation by email match. Guest only. */
http.route({
  path: "/guests/accept",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) {
      return errorResponse("Unauthorized", 401);
    }
    const token = authHeader.slice(7);
    const tokenHash = await sha256Hex(token);

    const body = await request.json();
    if (!body.hostUserId) return errorResponse("hostUserId is required");
    const approvedDeviceIds = Array.isArray(body.approvedDeviceIds)
      ? body.approvedDeviceIds.map(String)
      : undefined;

    try {
      await ctx.runMutation(api.guests.accept, {
        tokenHash,
        hostUserId: body.hostUserId,
        approvedDeviceIds,
      });
      return jsonResponse({ ok: true });
    } catch (e: any) {
      return errorResponse(e.message || "Failed to accept invitation", 400);
    }
  }),
});

/** POST /guests/accept-code — Accept invitation via 6-char code. Works with any OAuth email. */
http.route({
  path: "/guests/accept-code",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) {
      return errorResponse("Unauthorized", 401);
    }
    const token = authHeader.slice(7);
    const tokenHash = await sha256Hex(token);

    const body = await request.json();
    if (!body.code) return errorResponse("code is required");
    const approvedDeviceIds = Array.isArray(body.approvedDeviceIds)
      ? body.approvedDeviceIds.map(String)
      : undefined;

    try {
      const result = await ctx.runMutation(api.guests.acceptByCode, {
        tokenHash,
        inviteCode: body.code,
        approvedDeviceIds,
      });
      return jsonResponse(result);
    } catch (e: any) {
      return errorResponse(e.message || "Failed to accept invitation", 400);
    }
  }),
});

/** GET /guests/find-by-code?code=XXXX — Preview invitation before accepting. */
http.route({
  path: "/guests/find-by-code",
  method: "GET",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) {
      return errorResponse("Unauthorized", 401);
    }
    const token = authHeader.slice(7);
    const tokenHash = await sha256Hex(token);

    const url = new URL(request.url);
    const code = url.searchParams.get("code") ?? "";
    if (!code) return errorResponse("code is required");

    const info = await ctx.runQuery(api.guests.findByCode, { tokenHash, inviteCode: code });
    if (!info) return errorResponse("Invite not found or expired", 404);
    return jsonResponse(info);
  }),
});

/** GET /users/lookup?userId=XXXX — Resolve a public user id to a display profile. */
http.route({
  path: "/users/lookup",
  method: "GET",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) {
      return errorResponse("Unauthorized", 401);
    }
    const token = authHeader.slice(7);
    const tokenHash = await sha256Hex(token);

    const url = new URL(request.url);
    const userId = url.searchParams.get("userId") ?? "";
    if (!userId) return errorResponse("userId is required");

    const user = await ctx.runQuery(api.guests.lookupPublicUser, { tokenHash, userId });
    if (!user) return errorResponse("User not found", 404);
    return jsonResponse(user);
  }),
});

/** POST /guests/revoke — Revoke guest access. Host only. */
http.route({
  path: "/guests/revoke",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) {
      return errorResponse("Unauthorized", 401);
    }
    const token = authHeader.slice(7);
    const tokenHash = await sha256Hex(token);

    const body = await request.json();
    const email = typeof body.email === "string" ? body.email : undefined;
    const userId = typeof body.userId === "string" ? body.userId : undefined;
    if (!email && !userId) return errorResponse("email or userId is required");

    try {
      await ctx.runMutation(api.guests.revoke, {
        tokenHash,
        guestEmail: email,
        guestUserId: userId,
      });
      return jsonResponse({ ok: true });
    } catch (e: any) {
      return errorResponse(e.message || "Failed to revoke guest", 400);
    }
  }),
});

/** GET /guests/list — List all guests (host perspective). */
http.route({
  path: "/guests/list",
  method: "GET",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) {
      return errorResponse("Unauthorized", 401);
    }
    const token = authHeader.slice(7);
    const tokenHash = await sha256Hex(token);

    const guests = await ctx.runQuery(api.guests.listGuests, { tokenHash });
    return jsonResponse({ guests });
  }),
});

/** GET /guests/hosts — List hosts who granted access (guest perspective). */
http.route({
  path: "/guests/hosts",
  method: "GET",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) {
      return errorResponse("Unauthorized", 401);
    }
    const token = authHeader.slice(7);
    const tokenHash = await sha256Hex(token);

    const hosts = await ctx.runQuery(api.guests.listHosts, { tokenHash });
    return jsonResponse(hosts);
  }),
});

/** GET /guests/allowed — Get approved guest userIds (agent calls this). */
http.route({
  path: "/guests/allowed",
  method: "GET",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) {
      return errorResponse("Unauthorized", 401);
    }
    const token = authHeader.slice(7);
    const tokenHash = await sha256Hex(token);

    const url = new URL(request.url);
    const deviceId = url.searchParams.get("deviceId") ?? undefined;

    const guestUserIds = await ctx.runQuery(api.guests.getGuestUserIds, { tokenHash, deviceId });
    return jsonResponse({ guestUserIds });
  }),
});

/** GET /guests/config — Get guest config(s). Query param: ?email=foo@bar.com (optional). */
http.route({
  path: "/guests/config",
  method: "GET",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) {
      return errorResponse("Unauthorized", 401);
    }
    const token = authHeader.slice(7);
    const tokenHash = await sha256Hex(token);

    const url = new URL(request.url);
    const email = url.searchParams.get("email") || undefined;

    const configs = await ctx.runQuery(api.guests.getGuestConfig, { tokenHash, guestEmail: email });
    return jsonResponse({ configs });
  }),
});

/** POST /guests/config — Update guest config. Host only. */
http.route({
  path: "/guests/config",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) {
      return errorResponse("Unauthorized", 401);
    }
    const token = authHeader.slice(7);
    const tokenHash = await sha256Hex(token);

    const body = await request.json();
    if (!body.email) return errorResponse("email is required");

    try {
      await ctx.runMutation(api.guests.updateGuestConfig, {
        tokenHash,
        guestEmail: body.email,
        scope:
          body.scope === "full" || body.scope === "feedback-only" || body.scope === "sdk-project"
            ? body.scope
            : undefined,
        dailyTokenLimit: body.dailyTokenLimit,
        allowedRunners: body.allowedRunners,
        usageMode: body.usageMode,
        shareAllDevices: body.shareAllDevices,
        deviceIds: body.deviceIds,
        shareAllMachines: body.shareAllMachines,
        machineIds: body.machineIds,
        resourcePreset: body.resourcePreset,
        useHostApiKeys: body.useHostApiKeys,
        allowGuestProvidedApiKeys: body.allowGuestProvidedApiKeys,
        allowDesktopControl: body.allowDesktopControl,
        allowBrowserControl: body.allowBrowserControl,
        allowTunnelForward: body.allowTunnelForward,
        requireIsolation: body.requireIsolation,
        cpuLimitPercent: body.cpuLimitPercent,
        ramLimitMb: body.ramLimitMb,
        priorityMode: body.priorityMode,
        schedule: body.schedule,
        allowedProjects: Array.isArray(body.allowedProjects) ? body.allowedProjects.map(String) : undefined,
      });
      return jsonResponse({ ok: true });
    } catch (e: any) {
      return errorResponse(e.message || "Failed to update guest config", 400);
    }
  }),
});

/** POST /guests/usage — Record guest usage (agent reports). */
http.route({
  path: "/guests/usage",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) {
      return errorResponse("Unauthorized", 401);
    }
    const token = authHeader.slice(7);
    const tokenHash = await sha256Hex(token);

    const body = await request.json();
    if (!body.guestUserId || !body.secondsUsed) {
      return errorResponse("guestUserId and secondsUsed are required");
    }

    try {
      await ctx.runMutation(api.guests.recordGuestUsage, {
        tokenHash,
        guestUserId: body.guestUserId,
        secondsUsed: body.secondsUsed,
        date: body.date || new Date().toISOString().slice(0, 10),
      });
      return jsonResponse({ ok: true });
    } catch (e: any) {
      return errorResponse(e.message || "Failed to record usage", 400);
    }
  }),
});

/** GET /guests/usage — Get guest usage. Query param: ?date=2026-04-06 (optional). */
http.route({
  path: "/guests/usage",
  method: "GET",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) {
      return errorResponse("Unauthorized", 401);
    }
    const token = authHeader.slice(7);
    const tokenHash = await sha256Hex(token);

    const url = new URL(request.url);
    const date = url.searchParams.get("date") || undefined;

    const usage = await ctx.runQuery(api.guests.getGuestUsage, { tokenHash, date });
    return jsonResponse({ usage });
  }),
});

// ── Connections (social graph) ──────────────────────────────────────

const connectionsApi = (api as any).connections;

/** Helper: extract bearer → tokenHash, or null. */
async function bearerHash(request: Request): Promise<string | null> {
  const authHeader = request.headers.get("Authorization");
  if (!authHeader?.startsWith("Bearer ")) return null;
  return await sha256Hex(authHeader.slice(7));
}

/** POST /connections/request — send (or auto-accept) a connection request. */
http.route({
  path: "/connections/request",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const tokenHash = await bearerHash(request);
    if (!tokenHash) return errorResponse("Unauthorized", 401);
    const body = await request.json();
    try {
      const result = await ctx.runMutation(connectionsApi.request, {
        tokenHash,
        peerUserId: typeof body.peerUserId === "string" ? body.peerUserId : undefined,
        peerEmail: typeof body.peerEmail === "string" ? body.peerEmail : undefined,
        nickname: typeof body.nickname === "string" ? body.nickname : undefined,
        source: typeof body.source === "string" ? body.source : undefined,
      });
      return jsonResponse(result);
    } catch (e: any) {
      return errorResponse(e.message || "Failed to send request", 400);
    }
  }),
});

/** POST /connections/accept — accept an incoming request. */
http.route({
  path: "/connections/accept",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const tokenHash = await bearerHash(request);
    if (!tokenHash) return errorResponse("Unauthorized", 401);
    const body = await request.json();
    if (!body.peerUserId) return errorResponse("peerUserId is required");
    try {
      await ctx.runMutation(connectionsApi.accept, { tokenHash, peerUserId: String(body.peerUserId) });
      return jsonResponse({ ok: true });
    } catch (e: any) {
      return errorResponse(e.message || "Failed to accept", 400);
    }
  }),
});

/** POST /connections/remove — decline / cancel / unfriend. */
http.route({
  path: "/connections/remove",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const tokenHash = await bearerHash(request);
    if (!tokenHash) return errorResponse("Unauthorized", 401);
    const body = await request.json();
    if (!body.peerUserId) return errorResponse("peerUserId is required");
    try {
      await ctx.runMutation(connectionsApi.remove, { tokenHash, peerUserId: String(body.peerUserId) });
      return jsonResponse({ ok: true });
    } catch (e: any) {
      return errorResponse(e.message || "Failed to remove", 400);
    }
  }),
});

/** POST /connections/block — block a peer. */
http.route({
  path: "/connections/block",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const tokenHash = await bearerHash(request);
    if (!tokenHash) return errorResponse("Unauthorized", 401);
    const body = await request.json();
    if (!body.peerUserId) return errorResponse("peerUserId is required");
    try {
      await ctx.runMutation(connectionsApi.block, { tokenHash, peerUserId: String(body.peerUserId) });
      return jsonResponse({ ok: true });
    } catch (e: any) {
      return errorResponse(e.message || "Failed to block", 400);
    }
  }),
});

/** POST /connections/unblock — unblock a peer. */
http.route({
  path: "/connections/unblock",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const tokenHash = await bearerHash(request);
    if (!tokenHash) return errorResponse("Unauthorized", 401);
    const body = await request.json();
    if (!body.peerUserId) return errorResponse("peerUserId is required");
    try {
      await ctx.runMutation(connectionsApi.unblock, { tokenHash, peerUserId: String(body.peerUserId) });
      return jsonResponse({ ok: true });
    } catch (e: any) {
      return errorResponse(e.message || "Failed to unblock", 400);
    }
  }),
});

/** POST /connections/nickname — set a private nickname. */
http.route({
  path: "/connections/nickname",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const tokenHash = await bearerHash(request);
    if (!tokenHash) return errorResponse("Unauthorized", 401);
    const body = await request.json();
    if (!body.peerUserId) return errorResponse("peerUserId is required");
    try {
      await ctx.runMutation(connectionsApi.setNickname, {
        tokenHash,
        peerUserId: String(body.peerUserId),
        nickname: typeof body.nickname === "string" ? body.nickname : "",
      });
      return jsonResponse({ ok: true });
    } catch (e: any) {
      return errorResponse(e.message || "Failed to set nickname", 400);
    }
  }),
});

/** GET /connections/list?status= — list my connections. */
http.route({
  path: "/connections/list",
  method: "GET",
  handler: httpAction(async (ctx, request) => {
    const tokenHash = await bearerHash(request);
    if (!tokenHash) return errorResponse("Unauthorized", 401);
    const url = new URL(request.url);
    const status = url.searchParams.get("status");
    const result = await ctx.runQuery(connectionsApi.list, {
      tokenHash,
      status: status === "accepted" || status === "pending" || status === "blocked" ? status : undefined,
    });
    return jsonResponse(result);
  }),
});

/** GET /connections/search?query= — find a user by userId or email. */
http.route({
  path: "/connections/search",
  method: "GET",
  handler: httpAction(async (ctx, request) => {
    const tokenHash = await bearerHash(request);
    if (!tokenHash) return errorResponse("Unauthorized", 401);
    const url = new URL(request.url);
    const query = url.searchParams.get("query") ?? "";
    if (!query) return errorResponse("query is required");
    const result = await ctx.runQuery(connectionsApi.search, { tokenHash, query });
    if (!result) return errorResponse("User not found", 404);
    return jsonResponse(result);
  }),
});

/** GET /connections/suggested — people you already collaborate with. */
http.route({
  path: "/connections/suggested",
  method: "GET",
  handler: httpAction(async (ctx, request) => {
    const tokenHash = await bearerHash(request);
    if (!tokenHash) return errorResponse("Unauthorized", 401);
    const suggestions = await ctx.runQuery(connectionsApi.suggested, { tokenHash });
    return jsonResponse({ suggestions });
  }),
});

// ── Project shares ──────────────────────────────────────────────────

const projectSharesApi = (api as any).projectShares;

/** POST /project-shares/create — create a shared project. Owner. */
http.route({
  path: "/project-shares/create",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const tokenHash = await bearerHash(request);
    if (!tokenHash) return errorResponse("Unauthorized", 401);
    const body = await request.json();
    const hostKind = body.hostKind === "managed-cloud" ? "managed-cloud" : "owner-device";
    try {
      const result = await ctx.runMutation(projectSharesApi.create, {
        tokenHash,
        slug: String(body.slug ?? ""),
        repoUrl: String(body.repoUrl ?? ""),
        defaultBranch: typeof body.defaultBranch === "string" ? body.defaultBranch : undefined,
        hostKind,
        hostDeviceId: typeof body.hostDeviceId === "string" ? body.hostDeviceId : undefined,
        hostMachineId: typeof body.hostMachineId === "string" ? body.hostMachineId : undefined,
        payer: body.payer === "invitee" ? "invitee" : body.payer === "owner" ? "owner" : undefined,
      });
      return jsonResponse(result);
    } catch (e: any) {
      return errorResponse(e.message || "Failed to create project", 400);
    }
  }),
});

/** POST /project-shares/invite — invite a person to a project. Owner. */
http.route({
  path: "/project-shares/invite",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const tokenHash = await bearerHash(request);
    if (!tokenHash) return errorResponse("Unauthorized", 401);
    const body = await request.json();
    if (!body.shareId) return errorResponse("shareId is required");
    const role = body.role === "dev" || body.role === "normie" || body.role === "viewer" ? body.role : undefined;
    try {
      const result = await ctx.runMutation(projectSharesApi.invite, {
        tokenHash,
        shareId: body.shareId,
        peerUserId: typeof body.peerUserId === "string" ? body.peerUserId : undefined,
        peerEmail: typeof body.peerEmail === "string" ? body.peerEmail : undefined,
        role,
      });
      return jsonResponse(result);
    } catch (e: any) {
      return errorResponse(e.message || "Failed to invite", 400);
    }
  }),
});

/** POST /project-shares/accept — accept a project invite by code. */
http.route({
  path: "/project-shares/accept",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const tokenHash = await bearerHash(request);
    if (!tokenHash) return errorResponse("Unauthorized", 401);
    const body = await request.json();
    if (!body.shareCode) return errorResponse("shareCode is required");
    try {
      const result = await ctx.runMutation(projectSharesApi.accept, {
        tokenHash,
        shareCode: String(body.shareCode),
      });
      return jsonResponse(result);
    } catch (e: any) {
      return errorResponse(e.message || "Failed to accept", 400);
    }
  }),
});

/** GET /project-shares/list — my owned + joined projects. */
http.route({
  path: "/project-shares/list",
  method: "GET",
  handler: httpAction(async (ctx, request) => {
    const tokenHash = await bearerHash(request);
    if (!tokenHash) return errorResponse("Unauthorized", 401);
    const result = await ctx.runQuery(projectSharesApi.listMine, { tokenHash });
    return jsonResponse(result);
  }),
});

/** GET /project-shares/find-by-code?code= — preview before accepting. */
http.route({
  path: "/project-shares/find-by-code",
  method: "GET",
  handler: httpAction(async (ctx, request) => {
    const tokenHash = await bearerHash(request);
    if (!tokenHash) return errorResponse("Unauthorized", 401);
    const url = new URL(request.url);
    const code = url.searchParams.get("code") ?? "";
    if (!code) return errorResponse("code is required");
    const info = await ctx.runQuery(projectSharesApi.findByCode, { tokenHash, shareCode: code });
    if (!info) return errorResponse("Project not found", 404);
    return jsonResponse(info);
  }),
});

/** POST /project-shares/set-role — change a member's role. Owner. */
http.route({
  path: "/project-shares/set-role",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const tokenHash = await bearerHash(request);
    if (!tokenHash) return errorResponse("Unauthorized", 401);
    const body = await request.json();
    if (!body.shareId || !body.memberUserId) return errorResponse("shareId and memberUserId are required");
    const role = body.role === "dev" || body.role === "normie" || body.role === "viewer" ? body.role : null;
    if (!role) return errorResponse("valid role is required");
    try {
      await ctx.runMutation(projectSharesApi.setRole, {
        tokenHash,
        shareId: body.shareId,
        memberUserId: String(body.memberUserId),
        role,
      });
      return jsonResponse({ ok: true });
    } catch (e: any) {
      return errorResponse(e.message || "Failed to set role", 400);
    }
  }),
});

/** POST /project-shares/revoke-member — remove a member. Owner. */
http.route({
  path: "/project-shares/revoke-member",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const tokenHash = await bearerHash(request);
    if (!tokenHash) return errorResponse("Unauthorized", 401);
    const body = await request.json();
    if (!body.shareId || !body.memberUserId) return errorResponse("shareId and memberUserId are required");
    try {
      await ctx.runMutation(projectSharesApi.revokeMember, {
        tokenHash,
        shareId: body.shareId,
        memberUserId: String(body.memberUserId),
      });
      return jsonResponse({ ok: true });
    } catch (e: any) {
      return errorResponse(e.message || "Failed to revoke member", 400);
    }
  }),
});

/** POST /project-shares/archive — archive a project. Owner. */
http.route({
  path: "/project-shares/archive",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const tokenHash = await bearerHash(request);
    if (!tokenHash) return errorResponse("Unauthorized", 401);
    const body = await request.json();
    if (!body.shareId) return errorResponse("shareId is required");
    try {
      await ctx.runMutation(projectSharesApi.archive, { tokenHash, shareId: body.shareId });
      return jsonResponse({ ok: true });
    } catch (e: any) {
      return errorResponse(e.message || "Failed to archive", 400);
    }
  }),
});

// ── Host-Share Leases ───────────────────────────────────────────────

const hostShareApi = (api as any).hostShare;

/** POST /host-share/create — Create a host-backed coding invite. */
http.route({
  path: "/host-share/create",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) return errorResponse("Unauthorized", 401);
    const tokenHash = await sha256Hex(authHeader.slice(7));
    const body = await request.json();

    try {
      const result = await ctx.runMutation(hostShareApi.createInvite, {
        tokenHash,
        guestEmail: typeof body.guestEmail === "string" ? body.guestEmail : undefined,
        guestUserId: typeof body.guestUserId === "string" ? body.guestUserId : undefined,
        label: typeof body.label === "string" ? body.label : undefined,
        hostDeviceId: typeof body.hostDeviceId === "string" ? body.hostDeviceId : undefined,
        inviteTtlMinutes: typeof body.inviteTtlMinutes === "number" ? body.inviteTtlMinutes : undefined,
        sessionTtlMinutes: typeof body.sessionTtlMinutes === "number" ? body.sessionTtlMinutes : undefined,
        idleTimeoutMinutes: typeof body.idleTimeoutMinutes === "number" ? body.idleTimeoutMinutes : undefined,
        toolingPreset: typeof body.toolingPreset === "string" ? body.toolingPreset : undefined,
        resourcePreset: typeof body.resourcePreset === "string" ? body.resourcePreset : undefined,
        allowInfra: typeof body.allowInfra === "boolean" ? body.allowInfra : undefined,
        allowTerminal: typeof body.allowTerminal === "boolean" ? body.allowTerminal : undefined,
        allowTunnel: typeof body.allowTunnel === "boolean" ? body.allowTunnel : undefined,
        useHostAgentTools: typeof body.useHostAgentTools === "boolean" ? body.useHostAgentTools : undefined,
        useHostInfra: typeof body.useHostInfra === "boolean" ? body.useHostInfra : undefined,
        allowedRunners: Array.isArray(body.allowedRunners) ? body.allowedRunners.map(String) : undefined,
        allowedProjects: Array.isArray(body.allowedProjects) ? body.allowedProjects.map(String) : undefined,
      });
      return jsonResponse(result);
    } catch (e: any) {
      return errorResponse(e.message || "Failed to create host-share invite", 400);
    }
  }),
});

/** GET /host-share/invite?code=XXXX — Preview a host-share invite. */
http.route({
  path: "/host-share/invite",
  method: "GET",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) return errorResponse("Unauthorized", 401);
    const tokenHash = await sha256Hex(authHeader.slice(7));
    const url = new URL(request.url);
    const inviteCode = url.searchParams.get("code") ?? "";
    if (!inviteCode) return errorResponse("code is required");
    try {
      const invite = await ctx.runQuery(hostShareApi.findInviteByCode, { tokenHash, inviteCode });
      if (!invite) return errorResponse("Invite not found or expired", 404);
      return jsonResponse(invite);
    } catch (e: any) {
      return errorResponse(e.message || "Failed to load host-share invite", 400);
    }
  }),
});

/** POST /host-share/join — Redeem a host-share invite by code. */
http.route({
  path: "/host-share/join",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) return errorResponse("Unauthorized", 401);
    const tokenHash = await sha256Hex(authHeader.slice(7));
    const body = await request.json();
    if (typeof body.code !== "string" || !body.code.trim()) return errorResponse("code is required");
    try {
      const result = await ctx.runMutation(hostShareApi.joinByCode, {
        tokenHash,
        inviteCode: body.code,
        guestDeviceId: typeof body.guestDeviceId === "string" ? body.guestDeviceId : undefined,
      });
      return jsonResponse(result);
    } catch (e: any) {
      return errorResponse(e.message || "Failed to join host-share session", 400);
    }
  }),
});

/** POST /host-share/revoke — Revoke a host-share invite by code. */
http.route({
  path: "/host-share/revoke",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) return errorResponse("Unauthorized", 401);
    const tokenHash = await sha256Hex(authHeader.slice(7));
    const body = await request.json();
    if (typeof body.code !== "string" || !body.code.trim()) return errorResponse("code is required");
    try {
      const result = await ctx.runMutation(hostShareApi.revokeInvite, {
        tokenHash,
        inviteCode: body.code,
      });
      return jsonResponse(result);
    } catch (e: any) {
      return errorResponse(e.message || "Failed to revoke host-share invite", 400);
    }
  }),
});

/** GET /host-share/list?role=host|guest — List invites for the caller. */
http.route({
  path: "/host-share/list",
  method: "GET",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) return errorResponse("Unauthorized", 401);
    const tokenHash = await sha256Hex(authHeader.slice(7));
    const role = (new URL(request.url).searchParams.get("role") ?? "host") as "host" | "guest";
    const invites = await ctx.runQuery(hostShareApi.listInvites, { tokenHash, role });
    return jsonResponse({ invites });
  }),
});

/** GET /host-share/sessions?role=host|guest — List active sessions for the caller. */
http.route({
  path: "/host-share/sessions",
  method: "GET",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) return errorResponse("Unauthorized", 401);
    const tokenHash = await sha256Hex(authHeader.slice(7));
    const role = (new URL(request.url).searchParams.get("role") ?? "host") as "host" | "guest";
    const sessions = await ctx.runQuery(hostShareApi.listSessions, { tokenHash, role });
    return jsonResponse({ sessions });
  }),
});

/** POST /host-share/end — End an active host-share session by session id. */
http.route({
  path: "/host-share/end",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) return errorResponse("Unauthorized", 401);
    const tokenHash = await sha256Hex(authHeader.slice(7));
    const body = await request.json();
    if (typeof body.sessionId !== "string" || !body.sessionId.trim()) {
      return errorResponse("sessionId is required");
    }
    try {
      const result = await ctx.runMutation(hostShareApi.endSession, {
        tokenHash,
        sessionId: body.sessionId,
      });
      return jsonResponse(result);
    } catch (e: any) {
      return errorResponse(e.message || "Failed to end host-share session", 400);
    }
  }),
});

/** GET /host-share/access?guestUserId=...&deviceId=... — Resolve active host-share access on this host/device. */
http.route({
  path: "/host-share/access",
  method: "GET",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) return errorResponse("Unauthorized", 401);
    const tokenHash = await sha256Hex(authHeader.slice(7));
    const url = new URL(request.url);
    const guestUserId = url.searchParams.get("guestUserId") ?? "";
    const deviceId = url.searchParams.get("deviceId") ?? undefined;
    if (!guestUserId) return errorResponse("guestUserId is required");
    try {
      const access = await ctx.runQuery(hostShareApi.getAccessForHostDevice, {
        tokenHash,
        guestUserId,
        deviceId,
      });
      if (!access) return jsonResponse({ access: null });
      return jsonResponse({ access });
    } catch (e: any) {
      return errorResponse(e.message || "Failed to resolve host-share access", 400);
    }
  }),
});

/** POST /host-share/touch — Refresh last-activity timestamp for an active session. */
http.route({
  path: "/host-share/touch",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) return errorResponse("Unauthorized", 401);
    const tokenHash = await sha256Hex(authHeader.slice(7));
    const body = await request.json();
    if (typeof body.sessionId !== "string" || !body.sessionId.trim()) return errorResponse("sessionId is required");
    try {
      const result = await ctx.runMutation(hostShareApi.touchSessionActivity, {
        tokenHash,
        sessionId: body.sessionId,
      });
      return jsonResponse(result);
    } catch (e: any) {
      return errorResponse(e.message || "Failed to touch host-share session", 400);
    }
  }),
});

/** GET /host-share/peer-access?hostUserId=...&deviceId=... — Resolve active host-share access on guest-owned agent for the host caller. */
http.route({
  path: "/host-share/peer-access",
  method: "GET",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) return errorResponse("Unauthorized", 401);
    const tokenHash = await sha256Hex(authHeader.slice(7));
    const url = new URL(request.url);
    const hostUserId = url.searchParams.get("hostUserId") ?? "";
    const deviceId = url.searchParams.get("deviceId") ?? undefined;
    if (!hostUserId) return errorResponse("hostUserId is required");
    try {
      const access = await ctx.runQuery(hostShareApi.getAccessForGuestDevice, {
        tokenHash,
        hostUserId,
        deviceId,
      });
      if (!access) return jsonResponse({ access: null });
      return jsonResponse({ access });
    } catch (e: any) {
      return errorResponse(e.message || "Failed to resolve host-share peer access", 400);
    }
  }),
});

// ─── Machine-side TLS reconciler endpoints ─────────────────────────
//
// Called by /usr/local/bin/yaver-tls-reconciler (installed by the
// cloudMachines.provision cloud-init). Auth is a long-lived per-machine
// token whose SHA-256 hash lives on cloudMachines.machineTokenHash.

async function authenticateMachineRequest(
  ctx: { runQuery: (q: any, args: any) => Promise<any> },
  request: Request,
  machineIdRaw: string | null,
): Promise<{ ok: true; machine: any } | { ok: false; status: number; error: string }> {
  const token = request.headers.get("x-machine-token");
  if (!token) return { ok: false, status: 401, error: "Missing X-Machine-Token" };
  if (!machineIdRaw) return { ok: false, status: 400, error: "Missing machineId" };
  let machine: any;
  try {
    machine = await ctx.runQuery(internal.cloudMachines.getInternal, { machineId: machineIdRaw as any });
  } catch {
    return { ok: false, status: 400, error: "Invalid machineId" };
  }
  if (!machine) return { ok: false, status: 404, error: "Unknown machine" };
  if (!machine.machineTokenHash) return { ok: false, status: 409, error: "Machine not yet provisioned" };
  const hash = await sha256Hex(token);
  if (hash !== machine.machineTokenHash) {
    return { ok: false, status: 403, error: "Bad machine token" };
  }
  return { ok: true, machine };
}

/** GET /machine/pending-tls?machineId=... — list verified userDomains routed to this machine. */
http.route({
  path: "/machine/pending-tls",
  method: "GET",
  handler: httpAction(async (ctx, request) => {
    const url = new URL(request.url);
    const auth = await authenticateMachineRequest(ctx, request, url.searchParams.get("machineId"));
    if (!auth.ok) return errorResponse(auth.error, auth.status);
    const rows = await ctx.runQuery(internal.userDomains.listPendingTLSForMachine, {
      machineId: auth.machine._id,
    });
    return jsonResponse({
      domains: rows.map((r: any) => ({ domain: r.domain, domainId: r._id.toString() })),
    });
  }),
});

/** POST /machine/phase — box cloud-init reports its onboarding phase
 *  (installing-docker / pulling-image / registering) so web/mobile
 *  show a real progress bar. Machine-token authed; privacy-safe
 *  (label + percent only). project_managed_cloud_onboarding_gap. */
http.route({
  path: "/machine/phase",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    // Accept query params (cloud-init posts these — no JSON body, to
    // keep the runcmd YAML list-form quote-safe) OR a JSON body.
    const qs = new URL(request.url).searchParams;
    const body = await request.json().catch(() => ({} as any));
    const machineId = qs.get("machineId") ?? body.machineId ?? null;
    const auth = await authenticateMachineRequest(ctx, request, machineId);
    if (!auth.ok) return errorResponse(auth.error, auth.status);
    const phase = String(qs.get("phase") ?? body.phase ?? "").trim();
    // Default progress per phase so the box only has to send a label.
    const PCT: Record<string, number> = {
      "installing-docker": 45,
      "pulling-image": 60,
      "starting-agent": 75,
      "registering": 85,
    };
    // "error" is the box's own failure beacon (end-of-cloud-init health
    // probe failed). It carries a short curated label, no progress.
    if (phase === "error") {
      const errLabel = String(qs.get("error") ?? body.error ?? "")
        .trim()
        .slice(0, 200);
      await ctx.runMutation(internal.cloudMachines.setPhase, {
        machineId: auth.machine._id,
        phase: "error",
        error: errLabel || "provisioning failed",
      });
      return jsonResponse({ ok: true });
    }
    if (!phase || !(phase in PCT)) {
      return errorResponse("Unknown or missing phase", 400);
    }
    const progress =
      typeof body.progress === "number" ? body.progress : PCT[phase];
    await ctx.runMutation(internal.cloudMachines.setPhase, {
      machineId: auth.machine._id,
      phase,
      progress,
    });
    return jsonResponse({ ok: true });
  }),
});

/** POST /machine/activity — the box agent pings this when it does real
 *  work (task run / exec / interactive session) so idle auto-shutdown
 *  (cloudLifecycle.idleSweep) doesn't pause a box that's actually in use.
 *  Machine-token authed (same as /machine/phase). Throttled server-side
 *  (touchActivity only writes when the prior stamp is >60s old). */
http.route({
  path: "/machine/activity",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const qs = new URL(request.url).searchParams;
    const body = await request.json().catch(() => ({} as any));
    const machineId = qs.get("machineId") ?? body.machineId ?? null;
    const auth = await authenticateMachineRequest(ctx, request, machineId);
    if (!auth.ok) return errorResponse(auth.error, auth.status);
    await ctx.runMutation(internal.cloudMachines.touchActivity, {
      machineId: auth.machine._id,
    });
    return jsonResponse({ ok: true });
  }),
});

/** POST /machine/tls-issued — reconciler reports a successful cert issue. */
http.route({
  path: "/machine/tls-issued",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const body = await request.json().catch(() => ({}));
    const auth = await authenticateMachineRequest(ctx, request, body.machineId ?? null);
    if (!auth.ok) return errorResponse(auth.error, auth.status);
    const domain = String(body.domain ?? "").trim().toLowerCase();
    if (!domain) return errorResponse("Missing domain", 400);
    const row = await ctx.runQuery(api.userDomains.getByDomain, { domain });
    if (!row) return errorResponse("Unknown domain", 404);
    if (row.targetId !== auth.machine._id.toString()) {
      return errorResponse("Domain not routed to this machine", 403);
    }
    await ctx.runMutation(internal.userDomains.markTLSIssued, { domainId: row._id });
    return jsonResponse({ ok: true });
  }),
});

/** POST /machine/tls-error — reconciler reports a certbot failure. */
http.route({
  path: "/machine/tls-error",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const body = await request.json().catch(() => ({}));
    const auth = await authenticateMachineRequest(ctx, request, body.machineId ?? null);
    if (!auth.ok) return errorResponse(auth.error, auth.status);
    const domain = String(body.domain ?? "").trim().toLowerCase();
    if (!domain) return errorResponse("Missing domain", 400);
    const row = await ctx.runQuery(api.userDomains.getByDomain, { domain });
    if (!row) return errorResponse("Unknown domain", 404);
    if (row.targetId !== auth.machine._id.toString()) {
      return errorResponse("Domain not routed to this machine", 403);
    }
    await ctx.runMutation(internal.userDomains.setStatus, {
      domainId: row._id,
      status: "error",
      errorMessage: String(body.error ?? "TLS issue failed"),
    });
    return jsonResponse({ ok: true });
  }),
});

// ── Yaver Gateway (inference arbitrage) trust boundary ──────────────
// The gateway (Cloudflare Worker / relay) holds upstream provider keys
// server-side and resells per-token inference into the prepaid wallet.
// Two routes: /gateway/authorize (user bearer → userId + balance, so the
// gateway never sees a key — "the wallet IS the key") and /gateway/meter
// (gateway-secret → debit). Both fail-closed pre-launch. See
// docs/yaver-gateway-spec.md and managedMeter.ts.

/** POST /gateway/authorize — resolve a user's bearer session token to
 *  {userId, balanceCents, allow} before the gateway streams inference.
 *  Inference floor = any positive balance; per-request + per-hour
 *  ceilings (gateway-side) bound a single burst. No snapshot reserve
 *  (that's a compute-box concern, not tokens). */
http.route({
  path: "/gateway/authorize",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    // Two ways to authenticate to the gateway:
    //   1. A scoped gateway token (operator-minted, inference-only) — the
    //      safe key path for free-tier tenants. Resolves to a userId but
    //      grants NOTHING outside the gateway.
    //   2. A normal Yaver session token (owner/phone) — existing behavior,
    //      still gated by cloudAccessAllowed.
    const authHeader = request.headers.get("Authorization") ?? "";
    const bearer = authHeader.startsWith("Bearer ") ? authHeader.slice(7) : "";
    if (!bearer) return errorResponse("Unauthorized", 401);

    let userId: string | null = null;
    let scoped = false;
    const tokHash = await sha256Hex(bearer);
    const gw = await ctx.runQuery(internal.gatewayTokens.resolveInternal, { tokenHash: tokHash });
    if (gw) {
      userId = gw.userId;
      scoped = true;
    } else {
      const session = await authenticateRequest(ctx, request);
      if (!session) return errorResponse("Unauthorized", 401);
      // Session path keeps the private-preview gate; the scoped-token path
      // does not (the operator minting the token IS the authorization).
      if (!cloudAccessAllowed(session.email, session.userDocId)) {
        return errorResponse("Yaver Premium is private-preview only on this account", 403);
      }
      userId = session.userDocId as any;
    }

    const wallet = await ctx.runQuery(internal.cloudLifecycle.getWallet, {
      userId: userId as any,
    });
    // Per-user limits — operator-set, user-immutable (gatewayPolicy).
    const pol = await ctx.runQuery(internal.gatewayPolicy.getAuthContext, {
      userId: userId as any,
    });

    const dailyExceeded =
      (pol.dailyCapCents ?? 0) > 0 && pol.spentTodayCents >= (pol.dailyCapCents ?? 0);
    const allow = pol.enabled && wallet.balanceCents > 0 && !dailyExceeded;

    // Inference is meaningful activity — keep the user's managed box warm
    // so idle auto-shutdown doesn't pause it mid-session. Fire-and-forget
    // (no added latency); throttled write inside touchActivityForUser.
    if (allow) {
      await ctx.scheduler.runAfter(0, internal.cloudMachines.touchActivityForUser, {
        userId: userId as any,
      });
    }

    return jsonResponse({
      ok: true,
      userId,
      scoped,
      balanceCents: wallet.balanceCents,
      allow,
      // Reason helps the Worker/log distinguish a deny cause (non-secret).
      reason: !pol.enabled
        ? "disabled"
        : wallet.balanceCents <= 0
          ? "insufficient_balance"
          : dailyExceeded
            ? "daily_cap"
            : "ok",
      // Per-user ceilings the Worker enforces (0 = fall back to env default).
      limits: {
        maxTokensPerRequest: pol.maxTokensPerRequest ?? 0,
        maxCentsPerRequest: pol.maxCentsPerRequest ?? 0,
        hourlyCapCents: pol.hourlyCapCents ?? 0,
        dailyCapCents: pol.dailyCapCents ?? 0,
        spentTodayCents: pol.spentTodayCents,
      },
    });
  }),
});

// ── Yaver Gateway: operator-only management (policy + scoped tokens) ──
// All gated by isOwner (ownerAllowlist env). A tenant has NO route here —
// they cannot read/raise their own caps or mint themselves a token.

/** POST /gateway/policy/set — operator sets a user's limits. Body:
 *  { targetUserId, enabled?, dailyCapCents?, hourlyCapCents?,
 *    maxTokensPerRequest?, maxCentsPerRequest?, freeGrantCents?, note? } */
http.route({
  path: "/gateway/policy/set",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const session = await authenticateRequest(ctx, request);
    if (!session) return errorResponse("Unauthorized", 401);
    if (!isOwner(session.email, session.userDocId)) return errorResponse("Operator only", 403);
    let body: any = {};
    try {
      body = await request.json();
    } catch {
      return errorResponse("Bad JSON", 400);
    }
    if (!body.targetUserId) return errorResponse("targetUserId required", 400);
    const res = await ctx.runMutation(internal.gatewayPolicy.setPolicyInternal, {
      userId: body.targetUserId as any,
      enabled: typeof body.enabled === "boolean" ? body.enabled : undefined,
      dailyCapCents: typeof body.dailyCapCents === "number" ? body.dailyCapCents : undefined,
      hourlyCapCents: typeof body.hourlyCapCents === "number" ? body.hourlyCapCents : undefined,
      maxTokensPerRequest:
        typeof body.maxTokensPerRequest === "number" ? body.maxTokensPerRequest : undefined,
      maxCentsPerRequest:
        typeof body.maxCentsPerRequest === "number" ? body.maxCentsPerRequest : undefined,
      freeGrantCents: typeof body.freeGrantCents === "number" ? body.freeGrantCents : undefined,
      note: typeof body.note === "string" ? body.note : undefined,
      setBy: String(session.userDocId),
    });
    return jsonResponse(res);
  }),
});

/** GET /gateway/policy?userId= — operator reads a user's policy. */
http.route({
  path: "/gateway/policy",
  method: "GET",
  handler: httpAction(async (ctx, request) => {
    const session = await authenticateRequest(ctx, request);
    if (!session) return errorResponse("Unauthorized", 401);
    if (!isOwner(session.email, session.userDocId)) return errorResponse("Operator only", 403);
    const target = new URL(request.url).searchParams.get("userId");
    if (!target) return errorResponse("userId required", 400);
    const policy = await ctx.runQuery(internal.gatewayPolicy.getPolicyInternal, {
      userId: target as any,
    });
    const tokens = await ctx.runQuery(internal.gatewayTokens.listForUserInternal, {
      userId: target as any,
    });
    return jsonResponse({ ok: true, policy, tokens });
  }),
});

/** POST /gateway/token/mint — operator mints a scoped inference token for a
 *  user. Returns the RAW token ONCE (only the hash is stored). Body:
 *  { targetUserId, label?, expiresAt? } */
http.route({
  path: "/gateway/token/mint",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const session = await authenticateRequest(ctx, request);
    if (!session) return errorResponse("Unauthorized", 401);
    if (!isOwner(session.email, session.userDocId)) return errorResponse("Operator only", 403);
    let body: any = {};
    try {
      body = await request.json();
    } catch {
      return errorResponse("Bad JSON", 400);
    }
    if (!body.targetUserId) return errorResponse("targetUserId required", 400);
    // Raw token: 32 random bytes, prefixed so it's recognizable in logs/env.
    const buf = new Uint8Array(32);
    crypto.getRandomValues(buf);
    const raw =
      "ygw_" + Array.from(buf).map((b) => b.toString(16).padStart(2, "0")).join("");
    const tokenHash = await sha256Hex(raw);
    const res = await ctx.runMutation(internal.gatewayTokens.mintInternal, {
      userId: body.targetUserId as any,
      tokenHash,
      scope: "inference",
      label: typeof body.label === "string" ? body.label : undefined,
      createdBy: String(session.userDocId),
      expiresAt: typeof body.expiresAt === "number" ? body.expiresAt : undefined,
    });
    // raw is returned ONCE — the operator stores it (it's the OPENAI_API_KEY
    // they bake into the tenant's runner). It is never retrievable again.
    return jsonResponse({ ...res, token: raw });
  }),
});

/** POST /gateway/token/revoke — operator revokes one token by id. */
http.route({
  path: "/gateway/token/revoke",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const session = await authenticateRequest(ctx, request);
    if (!session) return errorResponse("Unauthorized", 401);
    if (!isOwner(session.email, session.userDocId)) return errorResponse("Operator only", 403);
    let body: any = {};
    try {
      body = await request.json();
    } catch {
      return errorResponse("Bad JSON", 400);
    }
    if (!body.tokenId) return errorResponse("tokenId required", 400);
    const res = await ctx.runMutation(internal.gatewayTokens.revokeInternal, {
      tokenId: body.tokenId as any,
    });
    return jsonResponse(res);
  }),
});

/** POST /gateway/token/rotate — key rotation as a protection mechanism:
 *  revoke ALL of a user's existing tokens and mint a fresh one. Body:
 *  { targetUserId, label? } → returns the new raw token once. */
http.route({
  path: "/gateway/token/rotate",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const session = await authenticateRequest(ctx, request);
    if (!session) return errorResponse("Unauthorized", 401);
    if (!isOwner(session.email, session.userDocId)) return errorResponse("Operator only", 403);
    let body: any = {};
    try {
      body = await request.json();
    } catch {
      return errorResponse("Bad JSON", 400);
    }
    if (!body.targetUserId) return errorResponse("targetUserId required", 400);
    await ctx.runMutation(internal.gatewayTokens.revokeAllForUserInternal, {
      userId: body.targetUserId as any,
    });
    const buf = new Uint8Array(32);
    crypto.getRandomValues(buf);
    const raw =
      "ygw_" + Array.from(buf).map((b) => b.toString(16).padStart(2, "0")).join("");
    const tokenHash = await sha256Hex(raw);
    const res = await ctx.runMutation(internal.gatewayTokens.mintInternal, {
      userId: body.targetUserId as any,
      tokenHash,
      scope: "inference",
      label: typeof body.label === "string" ? body.label : "rotated",
      createdBy: String(session.userDocId),
    });
    return jsonResponse({ ...res, token: raw, rotated: true });
  }),
});

/** POST /gateway/meter — gateway-authenticated (GATEWAY_SHARED_SECRET).
 *  The gateway reports RAW upstream token cost (providerCostCents); Convex
 *  applies markup(kind) and debits the wallet via recordManagedUsage. The
 *  body asserts an arbitrary userId + cost, so it must be gateway-secret
 *  authed, never user-bearer. Body: { userId, kind, provider, model, unit,
 *  quantity, providerCostCents, ref? }. */
http.route({
  path: "/gateway/meter",
  method: "POST",
  handler: httpAction(async (ctx, req) => {
    const authHeader = req.headers.get("authorization") ?? "";
    const token = authHeader.startsWith("Bearer ") ? authHeader.slice(7) : "";
    const check = await ctx.runAction(internal.gatewaySecret.verify, { token });
    if (!check.secretConfigured) return errorResponse("GATEWAY_SHARED_SECRET not set", 500);
    if (!check.ok) return errorResponse("Unauthorized", 401);
    let body: any;
    try {
      body = await req.json();
    } catch {
      return errorResponse("Bad JSON", 400);
    }
    const { userId, kind, provider, unit, quantity, providerCostCents, model, ref, dryRun } =
      body ?? {};
    if (!userId || typeof providerCostCents !== "number") {
      return errorResponse("Missing userId or providerCostCents", 400);
    }
    const result = await ctx.runMutation(internal.managedMeter.recordManagedUsage, {
      userId: userId as any,
      kind: String(kind ?? "inference"),
      provider: String(provider ?? "unknown"),
      unit: String(unit ?? "token"),
      quantity: Number(quantity ?? 0),
      providerCostCents,
      model: model ? String(model) : undefined,
      ref: ref ? String(ref) : undefined,
      // Gateway may force dryRun; otherwise inherit the global launch flag.
      dryRun:
        typeof dryRun === "boolean"
          ? dryRun
          : !parseBooleanEnv(process.env.YAVER_MANAGED_METER_LIVE, false),
    });
    return jsonResponse({ ok: true, ...result });
  }),
});

// External cron runner — Hetzner systemd timers POST { name } here instead
// of using Convex built-in crons. Bearer auth via CRON_TRIGGER_SECRET env
// (verified through internal.cronSecret.verify for httpAction-runtime parity).
const runCron = httpAction(async (ctx, req) => {
  const authHeader = req.headers.get("authorization") ?? "";
  const token = authHeader.startsWith("Bearer ") ? authHeader.slice(7) : "";
  const check = await ctx.runAction(internal.cronSecret.verify, { token });
  if (!check.secretConfigured) {
    return new Response("CRON_TRIGGER_SECRET not set", { status: 500 });
  }
  if (!check.ok) {
    return new Response("Unauthorized", { status: 401 });
  }
  let body: { name?: string };
  try {
    body = await req.json();
  } catch {
    return new Response("Bad JSON", { status: 400 });
  }
  const name = body.name;
  switch (name) {
    case "pruneAuthLogs":
      await ctx.scheduler.runAfter(0, internal.cleanup.pruneAuthLogs, {});
      break;
    case "pruneMobileStreamLogs":
      await ctx.scheduler.runAfter(0, internal.cleanup.pruneMobileStreamLogs, {});
      break;
    case "pruneDeveloperLogs":
      await ctx.scheduler.runAfter(0, internal.cleanup.pruneDeveloperLogs, {});
      break;
    case "pruneDeviceEvents":
      await ctx.scheduler.runAfter(0, internal.cleanup.pruneDeviceEvents, {});
      break;
    case "pruneExpiredSessions":
      await ctx.scheduler.runAfter(0, internal.cleanup.pruneExpiredSessions, {});
      break;
    case "cloudMeter":
      // Managed-cloud prepaid meter (P2). Same external-Hetzner-timer
      // pattern as the prune jobs (no Convex crons.interval, per
      // crons.ts). dryRun:true while in private preview so it never
      // drains a real wallet pre-launch; set YAVER_CLOUD_METER_LIVE=true
      // (Convex env, owner decision per project_business_model) to flip
      // it live — a one-env-flip go-live, no code change. The real
      // Hetzner spend is independently gated by HCLOUD_TOKEN inside
      // pause/resume — this only meters the wallet ledger.
      await ctx.scheduler.runAfter(0, internal.cloudLifecycle.meterTick, {
        intervalSeconds: 3600,
        dryRun: !parseBooleanEnv(process.env.YAVER_CLOUD_METER_LIVE, false),
      });
      break;
    case "sweepStalePendingClaims":
      // Daily sweep so unclaimed bootstrap-pending rows don't pile up
      // when a fresh box joins the relay and the user never visits
      // their dashboard. 24h is enough for a real claim while still
      // bounding the table.
      await ctx.scheduler.runAfter(0, internal.pendingDeviceClaims.sweepStale, {});
      break;
    case "cloudIdleSweep":
      // Idle auto-shutdown (P1.4): pause active managed boxes with no
      // meaningful activity past the threshold so we never bill Hetzner
      // hours nobody is using. DEFAULT OFF (YAVER_CLOUD_IDLE_ENABLE) until
      // the box agent reports activity via /machine/activity; even on, the
      // pause is HCLOUD_TOKEN/dryRun fail-closed. Schedule this on the
      // Hetzner cron timers like the others (every 10–15 min is plenty).
      await ctx.scheduler.runAfter(0, internal.cloudLifecycle.idleSweep, {
        enabled: parseBooleanEnv(process.env.YAVER_CLOUD_IDLE_ENABLE, false),
        idleMinutes: Number(process.env.YAVER_CLOUD_IDLE_MINUTES) || 45,
        dryRun: !parseBooleanEnv(process.env.YAVER_CLOUD_METER_LIVE, false),
      });
      break;
    case "reconcileManagedSubscriptions":
      // RECOVERY: re-provision any active managed subscription with no
      // live box (paid → no cloud resource). Idempotent. Schedule it
      // on the Hetzner cron timers like the others (hourly is plenty).
      await ctx.scheduler.runAfter(0, internal.cloudMachines.reconcileSubscriptions, {});
      break;
    default:
      return new Response(`Unknown cron: ${name}`, { status: 404 });
  }
  return new Response(JSON.stringify({ ok: true, scheduled: name }), {
    status: 200,
    headers: { "Content-Type": "application/json" },
  });
});

http.route({ path: "/crons/run", method: "POST", handler: runCron });

// ── Org admin console (/admin/*) ─────────────────────────────────────
//
// Gate: a caller is an admin if (a) the request carries a valid Bearer
// session, AND (b) the session user's email/userId is in the env-var
// owner allowlist (ownerAllowlist.ts). The platformRole DB flag is the
// future authoritative source — see admin.ts comment. Every admin route
// goes through requireAdminRequest below so the gate lives in one place.

async function requireAdminRequest(
  ctx: { runQuery: (query: any, args: any) => Promise<any> },
  request: Request,
): Promise<
  | { ok: true; user: NonNullable<Awaited<ReturnType<typeof authenticateRequest>>> }
  | { ok: false; response: Response }
> {
  const user = await authenticateRequest(ctx, request);
  if (!user) {
    return { ok: false, response: errorResponse("Unauthorized", 401) };
  }

  // Three-source gate, evaluated in order:
  //   1) env-var owner allowlist (solo-dev bootstrap; ALWAYS allowed).
  //   2) users.platformRole === "admin" (post-promotion path).
  // The env-var path is permanent — it stays as the escape hatch so
  // the solo dev can never lock themselves out by misconfiguring a
  // policy. Schema-promoted admins are additionally subject to the
  // MFA gate below if org policy turns it on.
  const isAllowlistAdmin = isOwner(user.email, user.userId);
  let isSchemaAdmin = false;
  if (!isAllowlistAdmin) {
    try {
      const userDoc = await ctx.runQuery(api.auth.getUserByDocId, {
        userDocId: user.userDocId,
      });
      isSchemaAdmin = userDoc?.platformRole === "admin";
    } catch {
      // getUserByDocId may not exist on every deployment until the
      // generated API regenerates — be permissive on lookup failure
      // so the env-var allowlist still works.
      isSchemaAdmin = false;
    }
  }
  if (!isAllowlistAdmin && !isSchemaAdmin) {
    return {
      ok: false,
      response: errorResponse(
        "Forbidden — caller is not a platform admin",
        403,
      ),
    };
  }

  // MFA-for-admins enforcement. Reads org policy; absence ⇒ disabled.
  // Solo-dev safety: env-var bootstrap admins are exempt unconditionally
  // (the policy could otherwise lock everyone out). Only schema-promoted
  // admins fall through to this check.
  if (!isAllowlistAdmin) {
    try {
      const policy = await ctx.runQuery(api.admin.getOrgPolicy, {});
      if (policy?.requireMfaForAdmins) {
        const userDoc = await ctx.runQuery(api.auth.getUserByDocId, {
          userDocId: user.userDocId,
        });
        if (!userDoc?.totpEnabled) {
          return {
            ok: false,
            response: errorResponse(
              "MFA required for platform admins — enroll TOTP in your account settings first.",
              403,
            ),
          };
        }
      }
    } catch {
      // Policy lookup failure should not lock out a schema admin who
      // is otherwise valid.
    }
  }

  return { ok: true, user };
}

http.route({
  path: "/admin/overview",
  method: "GET",
  handler: httpAction(async (ctx, request) => {
    const gate = await requireAdminRequest(ctx, request);
    if (!gate.ok) return gate.response;
    const [counts, recent] = await Promise.all([
      ctx.runQuery(api.admin.dashboardCounts, {}),
      ctx.runQuery(api.admin.recentAuditEvents, { limit: 5 }),
    ]);
    return jsonResponse({ ...counts, recent });
  }),
});

http.route({
  path: "/admin/devices",
  method: "GET",
  handler: httpAction(async (ctx, request) => {
    const gate = await requireAdminRequest(ctx, request);
    if (!gate.ok) return gate.response;
    const rows = await ctx.runQuery(api.admin.fleetDevices, {});
    return jsonResponse({ rows });
  }),
});

http.route({
  path: "/admin/sessions",
  method: "GET",
  handler: httpAction(async (ctx, request) => {
    const gate = await requireAdminRequest(ctx, request);
    if (!gate.ok) return gate.response;
    const rows = await ctx.runQuery(api.admin.activeSessionsForAdmin, {});
    return jsonResponse({ rows });
  }),
});

http.route({
  path: "/admin/mesh",
  method: "GET",
  handler: httpAction(async (ctx, request) => {
    const gate = await requireAdminRequest(ctx, request);
    if (!gate.ok) return gate.response;
    const data = await ctx.runQuery(api.admin.fleetMesh, {});
    return jsonResponse(data);
  }),
});

http.route({
  path: "/admin/users",
  method: "GET",
  handler: httpAction(async (ctx, request) => {
    const gate = await requireAdminRequest(ctx, request);
    if (!gate.ok) return gate.response;
    const rows = await ctx.runQuery(api.admin.allUsersForAdmin, {});
    return jsonResponse({ rows });
  }),
});

http.route({
  path: "/admin/audit",
  method: "GET",
  handler: httpAction(async (ctx, request) => {
    const gate = await requireAdminRequest(ctx, request);
    if (!gate.ok) return gate.response;
    const url = new URL(request.url);
    const params = url.searchParams;
    const numOrUndef = (key: string): number | undefined => {
      const v = params.get(key);
      if (v === null || v === "") return undefined;
      const n = Number(v);
      return Number.isFinite(n) ? n : undefined;
    };
    const strOrUndef = (key: string): string | undefined => {
      const v = params.get(key);
      return v === null || v === "" ? undefined : v;
    };
    const result = await ctx.runQuery(api.admin.mergedAuditFeed, {
      limit: numOrUndef("limit") ?? 50,
      cursor: numOrUndef("cursor"),
      actorEmail: strOrUndef("actor"),
      eventType: strOrUndef("event"),
      sinceMs: numOrUndef("since"),
      untilMs: numOrUndef("until"),
    });
    return jsonResponse(result);
  }),
});

// Identity check for the layout role guard. Both gates are consulted
// — env-var allowlist (solo-dev bootstrap) and users.platformRole
// (post-promotion). The layout uses isAdmin to decide whether to
// render the admin chrome or redirect to /dashboard.
http.route({
  path: "/admin/identity",
  method: "GET",
  handler: httpAction(async (ctx, request) => {
    const user = await authenticateRequest(ctx, request);
    if (!user) return errorResponse("Unauthorized", 401);
    let schemaAdmin = false;
    try {
      const doc = await ctx.runQuery(api.auth.getUserByDocId, {
        userDocId: user.userDocId as any,
      });
      schemaAdmin = doc?.platformRole === "admin";
    } catch {}
    const allowlistAdmin = isOwner(user.email, user.userId);
    return jsonResponse({
      isAdmin: allowlistAdmin || schemaAdmin,
      via: allowlistAdmin ? "allowlist" : schemaAdmin ? "platformRole" : null,
      email: user.email,
      userId: user.userId,
    });
  }),
});

// CORS preflights for every /admin/* path. The dashboard fetches from
// CONVEX_URL with Authorization: Bearer, so the preflight has to
// answer for that header. The user dashboard already advertises the
// permissive header set, so we mirror it here.
http.route({
  pathPrefix: "/admin/",
  method: "OPTIONS",
  handler: httpAction(async () => {
    return new Response(null, {
      status: 204,
      headers: {
        "Access-Control-Allow-Origin": "*",
        "Access-Control-Allow-Methods": "GET, POST, PUT, DELETE, OPTIONS",
        "Access-Control-Allow-Headers": "Authorization, Content-Type",
        "Access-Control-Max-Age": "86400",
      },
    });
  }),
});

// ── Admin action POSTs ───────────────────────────────────────────────
//
// Every action POST goes through requireAdminRequest. Body is JSON;
// the caller's userDocId is passed to mutations so audit rows record
// who-did-what. Soft-fails (target already gone, etc.) return ok=true
// with a flag rather than a 404, so the UI can stay congruent.

async function parseJsonBody(req: Request): Promise<any> {
  try {
    const text = await req.text();
    if (!text) return {};
    return JSON.parse(text);
  } catch {
    return null;
  }
}

async function getCallerDocId(
  ctx: { runQuery: (q: any, args: any) => Promise<any> },
  request: Request,
): Promise<any | null> {
  // Convex `Id<"users">` is a branded string at the type level. The
  // generated API accepts it as-is when returned from a query, so we
  // pass it straight through with no runtime work — the `any` here
  // sidesteps the brand mismatch without losing safety (the mutation
  // arg schemas re-validate at the convex boundary).
  const authHeader = request.headers.get("Authorization");
  if (!authHeader?.startsWith("Bearer ")) return null;
  const tokenHash = await sha256Hex(authHeader.slice(7));
  return await ctx.runQuery(api.auth.getUserDocId, { tokenHash });
}

// ── Devices ──────────────────────────────────────────────────────────

http.route({
  path: "/admin/devices/rescue",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const gate = await requireAdminRequest(ctx, request);
    if (!gate.ok) return gate.response;
    const body = await parseJsonBody(request);
    if (!body?.deviceDocId || !body?.command) {
      return errorResponse("deviceDocId and command required", 400);
    }
    const callerDocId = await getCallerDocId(ctx, request);
    if (!callerDocId) return errorResponse("Unauthorized", 401);
    try {
      const result = await ctx.runMutation(api.admin.queueAgentRescue, {
        deviceDocId: body.deviceDocId,
        command: body.command,
        callerDocId,
      });
      return jsonResponse(result);
    } catch (err: any) {
      return errorResponse(String(err?.message || err), 400);
    }
  }),
});

http.route({
  path: "/admin/devices/revoke",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const gate = await requireAdminRequest(ctx, request);
    if (!gate.ok) return gate.response;
    const body = await parseJsonBody(request);
    if (!body?.deviceDocId) return errorResponse("deviceDocId required", 400);
    const callerDocId = await getCallerDocId(ctx, request);
    if (!callerDocId) return errorResponse("Unauthorized", 401);
    try {
      const result = await ctx.runMutation(api.admin.revokeDevice, {
        deviceDocId: body.deviceDocId,
        callerDocId,
      });
      return jsonResponse(result);
    } catch (err: any) {
      return errorResponse(String(err?.message || err), 400);
    }
  }),
});

// ── Sessions ─────────────────────────────────────────────────────────

http.route({
  path: "/admin/sessions/revoke",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const gate = await requireAdminRequest(ctx, request);
    if (!gate.ok) return gate.response;
    const body = await parseJsonBody(request);
    if (!body?.sessionDocId) {
      return errorResponse("sessionDocId required", 400);
    }
    const callerDocId = await getCallerDocId(ctx, request);
    if (!callerDocId) return errorResponse("Unauthorized", 401);
    try {
      const result = await ctx.runMutation(api.admin.revokeSession, {
        sessionDocId: body.sessionDocId,
        callerDocId,
      });
      return jsonResponse(result);
    } catch (err: any) {
      return errorResponse(String(err?.message || err), 400);
    }
  }),
});

// ── Users ────────────────────────────────────────────────────────────

http.route({
  path: "/admin/users/promote",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const gate = await requireAdminRequest(ctx, request);
    if (!gate.ok) return gate.response;
    const body = await parseJsonBody(request);
    if (!body?.targetEmail) return errorResponse("targetEmail required", 400);
    const callerDocId = await getCallerDocId(ctx, request);
    if (!callerDocId) return errorResponse("Unauthorized", 401);
    try {
      const result = await ctx.runMutation(api.admin.promoteToAdmin, {
        targetEmail: body.targetEmail,
        callerDocId,
      });
      return jsonResponse(result);
    } catch (err: any) {
      return errorResponse(String(err?.message || err), 400);
    }
  }),
});

http.route({
  path: "/admin/users/demote",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const gate = await requireAdminRequest(ctx, request);
    if (!gate.ok) return gate.response;
    const body = await parseJsonBody(request);
    if (!body?.targetDocId) return errorResponse("targetDocId required", 400);
    const callerDocId = await getCallerDocId(ctx, request);
    if (!callerDocId) return errorResponse("Unauthorized", 401);
    try {
      const result = await ctx.runMutation(api.admin.demoteFromAdmin, {
        targetDocId: body.targetDocId,
        callerDocId,
      });
      return jsonResponse(result);
    } catch (err: any) {
      return errorResponse(String(err?.message || err), 400);
    }
  }),
});

http.route({
  path: "/admin/users/sign-out",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const gate = await requireAdminRequest(ctx, request);
    if (!gate.ok) return gate.response;
    const body = await parseJsonBody(request);
    if (!body?.targetDocId) return errorResponse("targetDocId required", 400);
    const callerDocId = await getCallerDocId(ctx, request);
    if (!callerDocId) return errorResponse("Unauthorized", 401);
    try {
      const result = await ctx.runMutation(api.admin.signOutUserAllSessions, {
        targetDocId: body.targetDocId,
        callerDocId,
      });
      return jsonResponse(result);
    } catch (err: any) {
      return errorResponse(String(err?.message || err), 400);
    }
  }),
});

http.route({
  path: "/admin/users/export",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const gate = await requireAdminRequest(ctx, request);
    if (!gate.ok) return gate.response;
    const body = await parseJsonBody(request);
    if (!body?.targetDocId) return errorResponse("targetDocId required", 400);
    try {
      const bundle = await ctx.runQuery(api.admin.exportUserBundleById, {
        targetDocId: body.targetDocId,
      });
      if (!bundle) return errorResponse("User not found", 404);
      return new Response(JSON.stringify(bundle, null, 2), {
        status: 200,
        headers: {
          "Content-Type": "application/json",
          "Content-Disposition": `attachment; filename=\"yaver-user-${body.targetDocId}.json\"`,
          "Access-Control-Allow-Origin": "*",
        },
      });
    } catch (err: any) {
      return errorResponse(String(err?.message || err), 400);
    }
  }),
});

http.route({
  path: "/admin/users/delete",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const gate = await requireAdminRequest(ctx, request);
    if (!gate.ok) return gate.response;
    const body = await parseJsonBody(request);
    if (!body?.targetDocId) return errorResponse("targetDocId required", 400);
    const callerDocId = await getCallerDocId(ctx, request);
    if (!callerDocId) return errorResponse("Unauthorized", 401);
    try {
      const result = await ctx.runMutation(api.admin.deleteUserCascade, {
        targetDocId: body.targetDocId,
        callerDocId,
      });
      return jsonResponse(result);
    } catch (err: any) {
      return errorResponse(String(err?.message || err), 400);
    }
  }),
});

// ── Org policy ───────────────────────────────────────────────────────

http.route({
  path: "/admin/policy",
  method: "GET",
  handler: httpAction(async (ctx, request) => {
    const gate = await requireAdminRequest(ctx, request);
    if (!gate.ok) return gate.response;
    const policy = await ctx.runQuery(api.admin.getOrgPolicy, {});
    return jsonResponse({ policy });
  }),
});

http.route({
  path: "/admin/policy",
  method: "PUT",
  handler: httpAction(async (ctx, request) => {
    const gate = await requireAdminRequest(ctx, request);
    if (!gate.ok) return gate.response;
    const body = await parseJsonBody(request);
    if (!body) return errorResponse("JSON body required", 400);
    const callerDocId = await getCallerDocId(ctx, request);
    if (!callerDocId) return errorResponse("Unauthorized", 401);
    try {
      const result = await ctx.runMutation(api.admin.setOrgPolicy, {
        callerDocId,
        enforceRelay: body.enforceRelay,
        allowedRunners: body.allowedRunners,
        allowedProviders: body.allowedProviders,
        idleTimeoutMin: body.idleTimeoutMin,
        auditRetentionDays: body.auditRetentionDays,
        requireMfaForAdmins: body.requireMfaForAdmins,
      });
      return jsonResponse(result);
    } catch (err: any) {
      return errorResponse(String(err?.message || err), 400);
    }
  }),
});

// ── OIDC config ──────────────────────────────────────────────────────

// Public, no auth — used by the /auth sign-in page to decide whether
// to render the "Sign in with company SSO" button. Returns the SAFE
// shape (no secret, no internal endpoints), only what the button needs:
// {enabled, issuerUrl, label}. Returns {enabled:false} for the
// solo-dev / default case where no OIDC has been configured.
http.route({
  path: "/auth/oidc/info",
  method: "GET",
  handler: httpAction(async (ctx) => {
    const cfg = await ctx.runQuery(api.admin.getOidcConfig, {});
    if (!cfg || !cfg.enabled) {
      return jsonResponse({ enabled: false });
    }
    let label = cfg.issuerUrl;
    try {
      label = new URL(cfg.issuerUrl).host;
    } catch {}
    return jsonResponse({ enabled: true, label });
  }),
});

http.route({
  path: "/admin/sso/oidc",
  method: "GET",
  handler: httpAction(async (ctx, request) => {
    const gate = await requireAdminRequest(ctx, request);
    if (!gate.ok) return gate.response;
    const cfg = await ctx.runQuery(api.admin.getOidcConfig, {});
    return jsonResponse({ config: cfg });
  }),
});

/** Resolve an OIDC issuer's .well-known/openid-configuration. Returns
 *  the four endpoints we need + a normalized status. Used by both
 *  Test-connection and Save. */
async function discoverOidc(issuerUrl: string): Promise<{
  ok: boolean;
  status: string;
  endpoints?: {
    authorizationEndpoint: string;
    tokenEndpoint: string;
    userinfoEndpoint: string;
    jwksUri: string;
  };
}> {
  try {
    const trimmed = issuerUrl.trim().replace(/\/$/, "");
    const wellKnownUrl = `${trimmed}/.well-known/openid-configuration`;
    const res = await fetch(wellKnownUrl, {
      headers: { Accept: "application/json" },
      signal: AbortSignal.timeout(10_000),
    });
    if (!res.ok) {
      return { ok: false, status: `${wellKnownUrl} returned ${res.status}` };
    }
    const json: any = await res.json();
    const required = ["authorization_endpoint", "token_endpoint", "userinfo_endpoint", "jwks_uri"];
    for (const key of required) {
      if (!json[key] || typeof json[key] !== "string") {
        return { ok: false, status: `${wellKnownUrl} response missing ${key}` };
      }
    }
    return {
      ok: true,
      status: `OK — discovered ${json.issuer ?? trimmed}`,
      endpoints: {
        authorizationEndpoint: json.authorization_endpoint,
        tokenEndpoint: json.token_endpoint,
        userinfoEndpoint: json.userinfo_endpoint,
        jwksUri: json.jwks_uri,
      },
    };
  } catch (err: any) {
    return { ok: false, status: String(err?.message || err) };
  }
}

http.route({
  path: "/admin/sso/oidc/test",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const gate = await requireAdminRequest(ctx, request);
    if (!gate.ok) return gate.response;
    const body = await parseJsonBody(request);
    if (!body?.issuerUrl) return errorResponse("issuerUrl required", 400);
    const result = await discoverOidc(body.issuerUrl);
    return jsonResponse(result);
  }),
});

http.route({
  path: "/admin/sso/oidc",
  method: "PUT",
  handler: httpAction(async (ctx, request) => {
    const gate = await requireAdminRequest(ctx, request);
    if (!gate.ok) return gate.response;
    const body = await parseJsonBody(request);
    if (!body?.issuerUrl || !body?.clientId) {
      return errorResponse("issuerUrl and clientId required", 400);
    }
    const callerDocId = await getCallerDocId(ctx, request);
    if (!callerDocId) return errorResponse("Unauthorized", 401);
    // Discover endpoints inside Save so the saved config is always
    // current. If discovery fails AND we have no prior discovery,
    // refuse — better to surface the bad config than save a useless
    // row.
    const discovery = await discoverOidc(body.issuerUrl);
    if (!discovery.ok) {
      const existing = await ctx.runQuery(api.admin.getOidcConfig, {});
      if (!existing || !existing.authorizationEndpoint) {
        return errorResponse(
          `Discovery failed: ${discovery.status}. Fix the issuer URL or test from the Test button first.`,
          400,
        );
      }
    }
    try {
      const result = await ctx.runMutation(api.admin.setOidcConfig, {
        callerDocId,
        enabled: body.enabled === true,
        issuerUrl: body.issuerUrl,
        clientId: body.clientId,
        clientSecret: body.clientSecret ?? "",
        tenant: body.tenant ?? undefined,
        discovered: discovery.ok ? discovery.endpoints : undefined,
      });
      return jsonResponse({ ...result, discovered: discovery.ok });
    } catch (err: any) {
      return errorResponse(String(err?.message || err), 400);
    }
  }),
});

http.route({
  path: "/admin/sso/oidc",
  method: "DELETE",
  handler: httpAction(async (ctx, request) => {
    const gate = await requireAdminRequest(ctx, request);
    if (!gate.ok) return gate.response;
    const callerDocId = await getCallerDocId(ctx, request);
    if (!callerDocId) return errorResponse("Unauthorized", 401);
    try {
      const result = await ctx.runMutation(api.admin.clearOidcConfig, {
        callerDocId,
      });
      return jsonResponse(result);
    } catch (err: any) {
      return errorResponse(String(err?.message || err), 400);
    }
  }),
});

// ── OIDC sign-in flow ────────────────────────────────────────────────
//
// The "company SSO" button on /auth → /auth/oidc/start (this server)
// → 302 to IdP authorize endpoint with PKCE + state. After the user
// authenticates the IdP redirects to /auth/oidc/callback (this
// server) which verifies state, exchanges code for tokens, fetches
// userinfo, enforces tenant restriction, upserts the user, mints a
// Yaver session, and redirects to the web client with the session
// token in the URL (same shape as the existing OAuth callbacks).

function base64Url(bytes: Uint8Array): string {
  let s = "";
  for (let i = 0; i < bytes.length; i++) s += String.fromCharCode(bytes[i]);
  return btoa(s).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
}

function randomBase64Url(byteLen: number): string {
  const buf = new Uint8Array(byteLen);
  crypto.getRandomValues(buf);
  return base64Url(buf);
}

async function pkceChallenge(verifier: string): Promise<string> {
  const hash = await crypto.subtle.digest(
    "SHA-256",
    new TextEncoder().encode(verifier),
  );
  return base64Url(new Uint8Array(hash));
}

/** The web base URL the OIDC callback redirects back to with the
 *  session token. Configured via WEB_BASE_URL env; falls back to
 *  the request origin so a self-host deployment Just Works without
 *  setting an env var. */
function resolveWebBaseUrl(request: Request): string {
  const env = (process.env.WEB_BASE_URL || "").trim().replace(/\/$/, "");
  if (env) return env;
  try {
    const u = new URL(request.url);
    return `${u.protocol}//${u.host}`;
  } catch {
    return "";
  }
}

http.route({
  path: "/auth/oidc/start",
  method: "GET",
  handler: httpAction(async (ctx, request) => {
    const cfg = await ctx.runQuery(api.admin.getOidcConfig, {});
    if (!cfg || !cfg.enabled || !cfg.authorizationEndpoint) {
      return errorResponse(
        "OIDC is not configured on this deployment.",
        404,
      );
    }
    const url = new URL(request.url);
    const returnTo = url.searchParams.get("return_to") || undefined;

    const state = randomBase64Url(24);
    const verifier = randomBase64Url(48);
    const challenge = await pkceChallenge(verifier);
    const nonce = randomBase64Url(16);

    await ctx.runMutation(api.admin.startOidcAttempt, {
      state,
      codeVerifier: verifier,
      nonce,
      returnTo,
    });

    const webBase = resolveWebBaseUrl(request);
    const callbackUrl = `${webBase}/auth/oidc/callback`;
    const params = new URLSearchParams({
      response_type: "code",
      client_id: cfg.clientId,
      redirect_uri: callbackUrl,
      scope: "openid email profile",
      state,
      nonce,
      code_challenge: challenge,
      code_challenge_method: "S256",
    });
    const authorizeUrl = `${cfg.authorizationEndpoint}?${params.toString()}`;
    return new Response(null, {
      status: 302,
      headers: { Location: authorizeUrl },
    });
  }),
});

http.route({
  path: "/auth/oidc/callback",
  method: "GET",
  handler: httpAction(async (ctx, request) => {
    const url = new URL(request.url);
    const code = url.searchParams.get("code");
    const state = url.searchParams.get("state");
    const errParam = url.searchParams.get("error");
    const webBase = resolveWebBaseUrl(request);

    function redirectToAuth(query: Record<string, string>) {
      const sp = new URLSearchParams(query);
      return new Response(null, {
        status: 302,
        headers: { Location: `${webBase}/auth?${sp.toString()}` },
      });
    }

    if (errParam) return redirectToAuth({ oidc_error: errParam });
    if (!code || !state) return redirectToAuth({ oidc_error: "missing_params" });

    const cfg = await ctx.runQuery(api.admin.getOidcConfigRaw, {});
    if (!cfg || !cfg.enabled || !cfg.tokenEndpoint || !cfg.userinfoEndpoint) {
      return redirectToAuth({ oidc_error: "config_missing" });
    }

    const attempt = await ctx.runMutation(api.admin.consumeOidcAttempt, { state });
    if (!attempt) return redirectToAuth({ oidc_error: "invalid_state" });

    // Exchange code → tokens.
    let clientSecret = "";
    try {
      clientSecret = await decryptStoredOidcSecret(cfg.clientSecretEnc);
    } catch (err: any) {
      console.error("[oidc] secret decrypt failed:", err);
      return redirectToAuth({ oidc_error: "secret_decrypt_failed" });
    }

    const callbackUrl = `${webBase}/auth/oidc/callback`;
    const tokenForm = new URLSearchParams({
      grant_type: "authorization_code",
      code,
      redirect_uri: callbackUrl,
      client_id: cfg.clientId,
      client_secret: clientSecret,
      code_verifier: attempt.codeVerifier,
    });

    let tokens: any;
    try {
      const tokenRes = await fetch(cfg.tokenEndpoint, {
        method: "POST",
        headers: {
          "Content-Type": "application/x-www-form-urlencoded",
          Accept: "application/json",
        },
        body: tokenForm.toString(),
        signal: AbortSignal.timeout(15_000),
      });
      if (!tokenRes.ok) {
        const detail = await tokenRes.text().catch(() => "");
        console.error("[oidc] token endpoint", tokenRes.status, detail);
        return redirectToAuth({ oidc_error: "token_exchange_failed" });
      }
      tokens = await tokenRes.json();
    } catch (err: any) {
      console.error("[oidc] token fetch:", err);
      return redirectToAuth({ oidc_error: "token_fetch_error" });
    }

    if (!tokens?.access_token) {
      return redirectToAuth({ oidc_error: "no_access_token" });
    }

    // Fetch userinfo. We rely on userinfo for identity (email, sub)
    // rather than parsing the ID token here — keeps the implementation
    // small and works against any compliant OIDC server. Tenant
    // restriction is enforced after userinfo so an unfederated user
    // never makes it to the upsert.
    let userinfo: any;
    try {
      const uiRes = await fetch(cfg.userinfoEndpoint, {
        headers: { Authorization: `Bearer ${tokens.access_token}` },
        signal: AbortSignal.timeout(10_000),
      });
      if (!uiRes.ok) {
        return redirectToAuth({ oidc_error: "userinfo_failed" });
      }
      userinfo = await uiRes.json();
    } catch (err) {
      return redirectToAuth({ oidc_error: "userinfo_error" });
    }

    const sub = String(userinfo?.sub || "").trim();
    const email = String(userinfo?.email || "").trim().toLowerCase();
    const name = String(userinfo?.name || userinfo?.preferred_username || email || "").trim();
    const picture = userinfo?.picture ? String(userinfo.picture) : undefined;
    if (!sub) return redirectToAuth({ oidc_error: "userinfo_no_sub" });

    // Tenant restriction. Empty tenant string ⇒ no restriction. Two
    // common shapes: email-domain match (eng.example.com matches
    // alice@eng.example.com) and exact tenant id match (Azure AD's
    // `tid` claim or hosted-domain `hd`).
    const tenant = (cfg.tenant ?? "").trim().toLowerCase();
    if (tenant) {
      const emailDomain = email.split("@")[1] || "";
      const hd = String(userinfo?.hd || "").toLowerCase();
      const tid = String(userinfo?.tid || "").toLowerCase();
      const matched = emailDomain === tenant || hd === tenant || tid === tenant;
      if (!matched) {
        return redirectToAuth({ oidc_error: "tenant_denied" });
      }
    }

    if (!email) return redirectToAuth({ oidc_error: "no_email" });

    // Upsert + session mint.
    const upserted = await ctx.runMutation(api.admin.upsertOidcUser, {
      issuer: cfg.issuerUrl,
      sub,
      email,
      name: name || undefined,
      avatarUrl: picture,
    });

    // Mint a Yaver session via the existing helper. The raw token
    // returned here is what the web client persists, mirroring the
    // other OAuth callbacks.
    const rawToken = randomBase64Url(32);
    const tokenHash = await sha256Hex(rawToken);
    const expiresAt = Date.now() + 365 * 24 * 60 * 60 * 1000;
    await ctx.runMutation(api.auth.createSession, {
      tokenHash,
      userId: upserted.userDocId,
      expiresAt,
    });

    // Hand the token back to /auth on the web client. The page already
    // knows how to consume `token=…` in the URL fragment for its
    // existing OAuth flows.
    const finalParams = new URLSearchParams({
      token: rawToken,
      provider: "oidc",
    });
    const returnTo = attempt.returnTo || "";
    if (returnTo) finalParams.set("return_to", returnTo);
    return new Response(null, {
      status: 302,
      headers: {
        Location: `${webBase}/auth#${finalParams.toString()}`,
      },
    });
  }),
});

// ── Task Packages — runner share/accept (docs/yaver-task-packages.md) ──

/** POST /packages/allocation — look up a shared Task Package by invite code,
 *  for the runner's consent screen. Public (the code is the secret); returns
 *  only consent-safe fields (package name, domains, schedule, willNot, dataShown). */
http.route({
  path: "/packages/allocation",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const body = await request.json().catch(() => null);
    const code = typeof body?.code === "string" ? body.code.trim() : "";
    if (!code) return errorResponse("need { code }", 400);
    const alloc = await ctx.runQuery(api.taskPackages.allocationByCode, { code });
    if (!alloc) return errorResponse("not found", 404);
    return jsonResponse(alloc);
  }),
});

/** POST /packages/accept — the runner accepts a shared package under consent.
 *  Materializes the scoped grant + activates the allocation. Auth: Bearer. */
http.route({
  path: "/packages/accept",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) return errorResponse("Unauthorized", 401);
    const tokenHash = await sha256Hex(authHeader.slice(7));
    const body = await request.json().catch(() => null);
    if (!body?.code || !body?.deviceId) return errorResponse("need { code, deviceId }", 400);
    try {
      const out = await ctx.runMutation(api.taskPackages.acceptAllocation, {
        tokenHash,
        code: String(body.code),
        deviceId: String(body.deviceId),
        wifiOnly: typeof body.wifiOnly === "boolean" ? body.wifiOnly : undefined,
        chargingOnly: typeof body.chargingOnly === "boolean" ? body.chargingOnly : undefined,
      });
      return jsonResponse({ ...out, ok: true });
    } catch (e: any) {
      return errorResponse(e?.message || "accept failed", 400);
    }
  }),
});

/** POST /packages/shared — list the packages shared WITH the caller (runner). */
http.route({
  path: "/packages/shared",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) return errorResponse("Unauthorized", 401);
    const tokenHash = await sha256Hex(authHeader.slice(7));
    try {
      const rows = await ctx.runQuery(api.taskPackages.sharedWithMe, { tokenHash });
      return jsonResponse({ allocations: rows });
    } catch (e: any) {
      return errorResponse(e?.message || "list failed", 400);
    }
  }),
});

// POST /beta/inference-token — a beta user exchanges their session for a scoped
// inference token (ygw_) + the gateway URL, so the mobile/web client can use
// managed (keyless) GLM without ever holding the upstream key. Gated on beta
// status; the raw token is returned ONCE (only its hash is stored).
http.route({
  path: "/beta/inference-token",
  method: "OPTIONS",
  handler: httpAction(async () => corsPreflightResponse()),
});
http.route({
  path: "/beta/inference-token",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const session = await authenticateRequest(ctx, request);
    if (!session) return errorResponse("Unauthorized", 401);
    const beta = await ctx.runQuery(internal.betaAccess.getBetaStatus, {
      userId: session.userDocId as any,
    });
    if (!beta?.isBeta || !beta?.aiEnabled) {
      return errorResponse("Not a beta user", 403);
    }
    const raw =
      "ygw_" +
      crypto.randomUUID().replace(/-/g, "") +
      crypto.randomUUID().replace(/-/g, "");
    const tokenHash = await sha256Hex(raw);
    await ctx.runMutation(internal.gatewayTokens.mintInternal, {
      userId: session.userDocId as any,
      tokenHash,
      scope: "inference",
      label: "beta-client",
    });
    return jsonResponse({
      token: raw,
      gatewayUrl: process.env.GATEWAY_PUBLIC_URL ?? "",
    });
  }),
});

// POST /beta/consent — the authenticated user approves a pre-seeded beta invite
// (consent to managed AI + the shared box). The real grant is created only here.
http.route({
  path: "/beta/consent",
  method: "OPTIONS",
  handler: httpAction(async () => corsPreflightResponse()),
});
http.route({
  path: "/beta/consent",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const session = await authenticateRequest(ctx, request);
    if (!session) return errorResponse("Unauthorized", 401);
    const result = await ctx.runAction(internal.betaAccess.acceptBetaInvite, {
      userId: session.userDocId as any,
    });
    if (!result.ok) return errorResponse(result.reason ?? "No pending invite", 404);
    return jsonResponse({ ok: true });
  }),
});

export default http;
