import { httpRouter } from "convex/server";
import { httpAction } from "./_generated/server";
import { api, internal } from "./_generated/api";
import { sha256Hex } from "./auth";

const http = httpRouter();

// ── Helpers ──────────────────────────────────────────────────────────

function jsonResponse(data: unknown, status = 200): Response {
  return new Response(JSON.stringify(data), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

function errorResponse(message: string, status = 400): Response {
  return jsonResponse({ error: message }, status);
}

/** Extract Bearer token from Authorization header, hash it, and validate. */
async function authenticateRequest(
  ctx: { runQuery: (query: any, args: any) => Promise<any> },
  request: Request
): Promise<{
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

  return await ctx.runQuery(api.auth.validateSession, { tokenHash });
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

async function createSessionToken(ctx: { runMutation: (m: any, args: any) => Promise<any> }, userId: any) {
  const tokenBytes = new Uint8Array(32);
  crypto.getRandomValues(tokenBytes);
  const token = Array.from(tokenBytes)
    .map((b) => b.toString(16).padStart(2, "0"))
    .join("");
  const tokenHash = await sha256Hex(token);
  const expiresAt = Date.now() + 365 * 24 * 60 * 60 * 1000;
  await ctx.runMutation(api.auth.createSession, { tokenHash, userId, expiresAt });
  return token;
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
    return jsonResponse({ token, userId: String(userId) });
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

/** POST /auth/create-session — Create a session (called from web server). */
http.route({
  path: "/auth/create-session",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const body = await request.json();
    const sessionId = await ctx.runMutation(api.auth.createSession, {
      tokenHash: body.tokenHash,
      userId: body.userId,
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

/** POST /auth/refresh — Extend session by 30 days. Returns new expiresAt. */
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

    const result = await ctx.runMutation(api.auth.refreshSession, { tokenHash });
    if (!result) {
      return errorResponse("Session expired or invalid", 401);
    }
    return jsonResponse({ ok: true, expiresAt: result.expiresAt });
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
      publicKey: body.publicKey || undefined,
      quicHost: body.quicHost,
      quicPort: body.quicPort,
    });

    return jsonResponse({ deviceId });
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
    });

    return jsonResponse({ ok: true });
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
  handler: httpAction(async (ctx) => {
    const result = await ctx.runMutation(api.deviceCode.createDeviceCode, {});
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
    // Verify webhook signature
    const signature = request.headers.get("x-signature");
    const body = await request.text();

    // TODO: Verify HMAC signature with LEMONSQUEEZY_WEBHOOK_SECRET env var
    // For now, check basic structure

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
        const plan = data.variant_name?.includes("yearly") ? "relay-yearly" : "relay-monthly";
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
          const productType = payload.meta?.custom_data?.product_type || "relay";
          const region = payload.meta?.custom_data?.region || "eu";

          if (productType === "cpu" || productType === "gpu") {
            // Cloud dev machine — create and provision
            const teamId = payload.meta?.custom_data?.team_id;
            await ctx.runMutation(api.cloudMachines.create, {
              userId: user._id,
              machineType: productType,
              teamId,
              region,
              subscriptionId: subId,
            });
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

export default http;
