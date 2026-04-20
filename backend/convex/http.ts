import { httpRouter } from "convex/server";
import { httpAction } from "./_generated/server";
import { api, internal } from "./_generated/api";
import { sha256Hex } from "./auth";

const http = httpRouter();
const CLOUD_PREVIEW_EMAIL = "kivanc.cakmak@icloud.com";

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

function isCloudPreviewUser(email?: string | null): boolean {
  return (email ?? "").trim() !== "";
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
    console.warn("[lemonsqueezy] LEMONSQUEEZY_WEBHOOK_SECRET not set — webhook signature NOT verified");
    return true;
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
}): Promise<string> {
  const apiKey = lsEnv("API_KEY");
  const storeId = lsEnv("STORE_ID");
  const variantId = lsEnv("YAVER_CLOUD_VARIANT_ID");
  if (!apiKey || !storeId || !variantId) {
    const missing = [
      !apiKey && "API_KEY",
      !storeId && "STORE_ID",
      !variantId && "YAVER_CLOUD_VARIANT_ID",
    ].filter(Boolean).join(", ");
    throw new Error(`Missing Lemon Squeezy configuration: ${missing}`);
  }

  const receiptLink =
    lsEnv("YAVER_CLOUD_RECEIPT_LINK_URL") ||
    lsEnv("CHECKOUT_REDIRECT_URL") ||
    process.env.NEXT_PUBLIC_BASE_URL ||
    "https://yaver.io/pricing";
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
  "/auth/device-code/authorize",
  "/devices/list", "/devices/owner-by-hardware", "/config", "/settings", "/packages",
  "/billing/yaver-cloud/checkout",
  "/billing/yaver-cloud/dev-activate",
  "/guests/invite", "/guests/accept", "/guests/accept-code",
  "/guests/find-by-code", "/guests/revoke", "/guests/list", "/guests/hosts",
  "/guests/allowed", "/guests/config", "/guests/usage",
  "/users/lookup",
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

    const user = await ctx.runQuery(api.auth.lookupEmailUser, {
      email: email.toLowerCase().trim(),
    });

    if (!user || !user.passwordHash) {
      return errorResponse("Invalid email or password", 401);
    }

    const valid = await verifyPassword(password, user.passwordHash);
    if (!valid) {
      return errorResponse("Invalid email or password", 401);
    }

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
      if (e?.message === "Unauthorized") return errorResponse("Unauthorized", 401);
      if (e?.message === "ONLY_IDENTITY") {
        return errorResponse("Refusing to unlink the only sign-in method — add another provider first.", 409);
      }
      if (e?.message === "TOTP_REQUIRED") {
        return errorResponse("TOTP code required (2FA is enabled on this account).", 412);
      }
      if (e?.message === "INVALID_TOTP") {
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
      if (e?.message === "Unauthorized") return errorResponse("Unauthorized", 401);
      if (e?.message === "TOTP_REQUIRED") {
        return errorResponse("TOTP code required (2FA is enabled on this account).", 412);
      }
      if (e?.message === "INVALID_TOTP") {
        return errorResponse("Invalid 2FA code.", 403);
      }
      if (e?.message === "TOO_MANY_PENDING_MERGES") {
        return errorResponse("Too many pending merge intents. Cancel an existing one first.", 429);
      }
      if (e?.message === "MERGE_RATE_LIMIT") {
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
      if (e?.message === "Unauthorized") return errorResponse("Unauthorized", 401);
      if (e?.message === "INVALID_MERGE_TOKEN") return errorResponse("Invalid merge token", 404);
      if (e?.message === "MERGE_ALREADY_RESOLVED") return errorResponse("Merge already completed or cancelled", 409);
      if (e?.message === "MERGE_TOKEN_EXPIRED") return errorResponse("Merge token expired", 410);
      if (e?.message === "CANNOT_MERGE_SELF") return errorResponse("Cannot merge an account into itself", 400);
      if (e?.message === "TARGET_USER_NOT_FOUND") return errorResponse("Target account no longer exists", 404);
      if (e?.message === "TOTP_REQUIRED") {
        return errorResponse("TOTP code required (source account has 2FA enabled).", 412);
      }
      if (e?.message === "INVALID_TOTP") {
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

    const url = new URL(request.url);
    const wantRotation = url.searchParams.get("rotate") !== "0";

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
      hardwareId: body.hardwareId || undefined,
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
    const owner = await ctx.runQuery(api.devices.ownerByHardwareId, {
      hardwareId: body.hardwareId,
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
      return errorResponse(e?.message || "bootstrap failed", 400);
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
    await ctx.runMutation(api.devices.heartbeat, {
      tokenHash,
      deviceId: body.deviceId,
      runners: body.runners,
      quicHost: body.quicHost || undefined,
      // Multi-IP rollout: the agent advertises every reachable IPv4 it has
      // (Wi-Fi LAN, Tailscale 100.x, Ethernet, VPNs) so the mobile connect
      // path can race them in parallel. Older agents don't send the field
      // at all — then undefined is correct and the mutation leaves the
      // stored list untouched.
      localIps: Array.isArray(body.localIps) ? body.localIps : undefined,
      hardwareId: body.hardwareId || undefined,
      deviceClass: body.deviceClass || undefined,
      edgeProfile: body.edgeProfile || undefined,
    });

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

    const devices = await ctx.runQuery(api.devices.listMyDevices, {
      tokenHash,
    });

    return jsonResponse({ devices });
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
    return jsonResponse({
      ok: true,
      settings: settings || { forceRelay: false, runnerId: undefined, customRunnerCommand: undefined, relayUrl: undefined, relayPassword: undefined, tunnelUrl: undefined },
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
      speechApiKey: body.speechApiKey,
      ttsEnabled: body.ttsEnabled,
      verbosity: body.verbosity,
      keyStorage: body.keyStorage,
      // Client sends null to clear the preference, undefined to leave untouched.
      primaryDeviceId: body.primaryDeviceId,
      // Per-subsystem managed toggle. Client sends only the
      // subsystem(s) it's changing; backend merges the patch into
      // the existing record so other subsystems' toggles are
      // preserved. null on any key clears that subsystem.
      managed: body.managed,
    });
    return jsonResponse({ ok: true });
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
      const result = await ctx.runQuery(api.userSettings.validateRelayPassword, { password: body.password });
      if (!result) {
        return jsonResponse({ ok: false }, 401);
      }
      return jsonResponse({ ok: true, userId: result.userId });
    } catch {
      return jsonResponse({ ok: false, error: "internal error" }, 500);
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
        relayServers = JSON.parse(config.relay_servers);
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
        const isCloudPreviewProduct = productType === "yaver-cloud";
        const plan = isCloudPreviewProduct
          ? "yaver-cloud-preview"
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

        // If new subscription, provision the appropriate resource
        if (eventName === "subscription_created") {
          const region = payload.meta?.custom_data?.region || "eu";

          if (productType === "cpu" || productType === "gpu" || isCloudPreviewProduct) {
            // Cloud dev machine — create and provision
            const teamId = payload.meta?.custom_data?.team_id;
            const machineId = await ctx.runMutation(api.cloudMachines.create, {
              userId: user._id,
              machineType: productType === "gpu" ? "gpu" : "cpu",
              teamId,
              region,
              subscriptionId: subId,
            });
            if (isCloudPreviewProduct) {
              await attachPreviewMachineToSharedServer(ctx, machineId, region);
            }
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
        await ctx.runMutation(internal.subscriptions.cancel, { lemonSqueezyId });

        // If expired (past grace period), deprovision the relay
        if (eventName === "subscription_expired") {
          const relay = await ctx.runQuery(internal.managedRelays.getByUserInternal, { userId: user._id });
          if (relay && relay.hetznerServerId && relay.domain) {
            await ctx.scheduler.runAfter(0, internal.provisionRelay.deprovision, {
              relayId: relay._id,
              hetznerServerId: relay.hetznerServerId,
              domain: relay.domain,
            });
          }
        }
        break;
      }

      case "subscription_payment_failed": {
        await ctx.runMutation(internal.subscriptions.upsertFromWebhook, {
          lemonSqueezyId,
          lemonSqueezyCustomerId: customerId,
          userId: user._id,
          plan: "relay-monthly",
          status: "past_due",
          currentPeriodEnd: Date.now(),
        });
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
    if (!isCloudPreviewUser(session.email)) {
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
      const url = await createLemonSqueezyCheckout({
        email: session.email,
        custom: {
          user_email: session.email,
          product_type: "yaver-cloud",
          region,
        },
      });
      return jsonResponse({ url, mode: parseBooleanEnv(lsEnv("SANDBOX"), true) ? "sandbox" : "live" });
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
    if (!isCloudPreviewUser(session.email)) {
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

    const [subscription, relay] = await Promise.all([
      ctx.runQuery(api.subscriptions.getByUser, { userId: userDocId }),
      ctx.runQuery(api.managedRelays.getByUser, { userId: userDocId }),
    ]);

    return jsonResponse({
      subscription: subscription ? {
        plan: subscription.plan,
        status: subscription.status,
        currentPeriodEnd: subscription.currentPeriodEnd,
        cancelledAt: subscription.cancelledAt,
      } : null,
      relay: relay ? {
        status: relay.status,
        domain: relay.domain,
        region: relay.region,
        quicPort: relay.quicPort,
        httpPort: relay.httpPort,
      } : null,
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
- Pricing: free relay included, optional dedicated relay ($10/mo)

Rules:
- Keep answers short (2-4 sentences). Link to docs when relevant.
- If the question is NOT about Yaver, politely say: "I can only help with Yaver-related questions. Check out yaver.io/docs for guides, or yaver.io/faq for common questions."
- Never make up features that don't exist.
- Key links: yaver.io/docs, yaver.io/manuals, yaver.io/download, yaver.io/pricing, yaver.io/manuals/integrations
- Yaver is free and open-source. The managed relay is the only paid option.
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
    if (!email && !userId) {
      return errorResponse("email or userId is required");
    }

    try {
      const result = await ctx.runMutation(api.guests.invite, {
        tokenHash,
        guestEmail: email,
        guestUserId: userId,
        proposedDeviceIds,
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

export default http;
