import { v } from "convex/values";
import { mutation, query } from "./_generated/server";

/**
 * Admin utilities. The cleanup helpers below are callable from
 * scripts only. The fleet / audit / counts queries below them ARE
 * exposed via HTTP in http.ts under /admin/* — gated by
 * isOwnerEmail || isOwnerUserId today, future-gated by a
 * users.platformRole === "admin" field once that schema migration
 * lands. The gate is enforced in the HTTP layer, NOT here, so these
 * queries stay easy to reason about (no implicit auth state).
 *
 * Schema migration TODO (next pass):
 *   users: { ..., platformRole: v.optional(v.union(v.literal("admin"))) }
 *   index "by_platformRole" so requireAdmin can look up first-admin
 *   atomically instead of scanning. promoteToAdmin below is the
 *   bootstrap mutation that sets it.
 */

/** List all users. */
export const listAllUsers = query({
  args: {},
  handler: async (ctx) => {
    return await ctx.db.query("users").collect();
  },
});

/** List all sessions. */
export const listAllSessions = query({
  args: {},
  handler: async (ctx) => {
    return await ctx.db.query("sessions").collect();
  },
});

/** List all devices. */
export const listAllDevices = query({
  args: {},
  handler: async (ctx) => {
    return await ctx.db.query("devices").collect();
  },
});

/** List recent auth logs. */
export const listAuthLogs = query({
  args: {},
  handler: async (ctx) => {
    return await ctx.db
      .query("authLogs")
      .withIndex("by_createdAt")
      .order("desc")
      .take(100);
  },
});

/** Find all users by email. Returns array of user documents. */
export const getUsersByEmail = query({
  args: { email: v.string() },
  handler: async (ctx, args) => {
    return await ctx.db
      .query("users")
      .withIndex("by_email", (q) => q.eq("email", args.email))
      .collect();
  },
});

/** Export one user's account bundle for migration between deployments. */
export const exportUserBundleByEmail = query({
  args: { email: v.string() },
  handler: async (ctx, args) => {
    const user = await ctx.db
      .query("users")
      .withIndex("by_email", (q) => q.eq("email", args.email))
      .unique();
    if (!user) return null;

    const settings = await ctx.db
      .query("userSettings")
      .withIndex("by_userId", (q) => q.eq("userId", user._id))
      .first();

    const devices = await ctx.db
      .query("devices")
      .withIndex("by_userId", (q) => q.eq("userId", user._id))
      .collect();

    return { user, settings, devices };
  },
});

/** Import or update one user's account bundle from another deployment. */
export const importUserBundle = mutation({
  args: {
    user: v.object({
      email: v.string(),
      fullName: v.string(),
      provider: v.union(
        v.literal("google"),
        v.literal("microsoft"),
        v.literal("apple"),
        v.literal("github"),
        v.literal("gitlab"),
        v.literal("email"),
        v.literal("passkey"),
        v.literal("oidc"),
      ),
      providerId: v.string(),
      userId: v.string(),
      createdAt: v.number(),
      surveyCompleted: v.optional(v.boolean()),
      avatarUrl: v.optional(v.string()),
      passwordHash: v.optional(v.string()),
      totpSecret: v.optional(v.string()),
      totpEnabled: v.optional(v.boolean()),
      totpRecoveryCodes: v.optional(v.string()),
    }),
    settings: v.optional(v.object({
      forceRelay: v.optional(v.boolean()),
      runnerId: v.optional(v.string()),
      customRunnerCommand: v.optional(v.string()),
      relayUrl: v.optional(v.string()),
      relayPassword: v.optional(v.string()),
      tunnelUrl: v.optional(v.string()),
      speechProvider: v.optional(v.string()),
      ttsEnabled: v.optional(v.boolean()),
      ttsProvider: v.optional(v.string()),
      verbosity: v.optional(v.number()),
      keyStorage: v.optional(v.string()),
    })),
    devices: v.array(v.object({
      deviceId: v.string(),
      name: v.string(),
      platform: v.union(v.literal("macos"), v.literal("windows"), v.literal("linux"), v.literal("android"), v.literal("ios")),
      quicHost: v.string(),
      quicPort: v.number(),
      isOnline: v.boolean(),
      lastHeartbeat: v.number(),
      createdAt: v.number(),
      deviceClass: v.optional(v.union(v.literal("desktop"), v.literal("edge-mobile"), v.literal("server"))),
      edgeProfile: v.optional(v.any()),
      publicKey: v.optional(v.string()),
      runnerDown: v.optional(v.boolean()),
      runners: v.optional(v.any()),
      needsAuth: v.optional(v.boolean()),
      hardwareId: v.optional(v.string()),
    })),
  },
  handler: async (ctx, args) => {
    const existingByProvider = await ctx.db
      .query("users")
      .withIndex("by_provider", (q) => q.eq("provider", args.user.provider).eq("providerId", args.user.providerId))
      .unique();
    const existingByEmail = await ctx.db
      .query("users")
      .withIndex("by_email", (q) => q.eq("email", args.user.email))
      .unique();

    const existing = existingByProvider || existingByEmail;
    let userDocId = existing?._id;

    const userPatch = {
      email: args.user.email,
      fullName: args.user.fullName,
      provider: args.user.provider,
      providerId: args.user.providerId,
      userId: args.user.userId,
      createdAt: args.user.createdAt,
      surveyCompleted: args.user.surveyCompleted,
      avatarUrl: args.user.avatarUrl,
      passwordHash: args.user.passwordHash,
      totpSecret: args.user.totpSecret,
      totpEnabled: args.user.totpEnabled,
      totpRecoveryCodes: args.user.totpRecoveryCodes,
    };

    if (userDocId) {
      await ctx.db.patch(userDocId, userPatch);
    } else {
      userDocId = await ctx.db.insert("users", userPatch);
    }

    if (args.settings) {
      const existingSettings = await ctx.db
        .query("userSettings")
        .withIndex("by_userId", (q) => q.eq("userId", userDocId!))
        .first();
      const settingsPatch = {
        userId: userDocId!,
        ...args.settings,
      };
      if (existingSettings) {
        await ctx.db.patch(existingSettings._id, settingsPatch);
      } else {
        await ctx.db.insert("userSettings", settingsPatch);
      }
    }

    let importedDevices = 0;
    for (const device of args.devices) {
      const existingDevice = await ctx.db
        .query("devices")
        .withIndex("by_deviceId", (q) => q.eq("deviceId", device.deviceId))
        .unique();
      const devicePatch = {
        userId: userDocId!,
        ...device,
      };
      if (existingDevice) {
        await ctx.db.patch(existingDevice._id, devicePatch);
      } else {
        await ctx.db.insert("devices", devicePatch);
      }
      importedDevices += 1;
    }

    return {
      userId: userDocId,
      email: args.user.email,
      importedDevices,
      hasSettings: !!args.settings,
    };
  },
});

/** Delete ALL user data from the system — every table that holds user/device/session state. */
export const deleteAllUserData = mutation({
  args: {},
  handler: async (ctx) => {
    const tables = [
      "users",
      "sessions",
      "devices",
      "userSettings",
      "developerSurveys",
      "runnerUsage",
      "dailyTaskCounts",
      "deviceMetrics",
      "deviceEvents",
      "passwordResets",
      "pendingAuth",
      "authLogs",
      "developerLogs",
      "deviceCodes",
      "downloads",
      "guestInvitations",
      "guestAccess",
      "guestUsage",
      "sdkTokens",
      "securityEvents",
      "mobileStreamLogs",
      "teams",
      "teamMembers",
      "subscriptions",
      "managedRelays",
      "cloudMachines",
    ] as const;

    const counts: Record<string, number> = {};
    for (const table of tables) {
      const docs = await ctx.db.query(table).collect();
      for (const doc of docs) {
        await ctx.db.delete(doc._id);
      }
      counts[table] = docs.length;
    }

    return counts;
  },
});

/** Delete all rows from a single table (paginated to stay within limits). */
export const clearTable = mutation({
  args: { table: v.string() },
  handler: async (ctx, args) => {
    const docs = await ctx.db.query(args.table as any).take(500);
    for (const doc of docs) {
      await ctx.db.delete(doc._id);
    }
    return { table: args.table, deleted: docs.length, hasMore: docs.length === 500 };
  },
});

/** Inspect one active guest grant by host+guest email. CLI/dashboard only. */
export const getActiveGuestGrantByEmails = query({
  args: {
    hostEmail: v.string(),
    guestEmail: v.string(),
  },
  handler: async (ctx, args) => {
    const host = await ctx.db
      .query("users")
      .withIndex("by_email", (q) => q.eq("email", args.hostEmail.trim().toLowerCase()))
      .unique();
    const guest = await ctx.db
      .query("users")
      .withIndex("by_email", (q) => q.eq("email", args.guestEmail.trim().toLowerCase()))
      .unique();
    if (!host || !guest) {
      return {
        hostFound: !!host,
        guestFound: !!guest,
        guestAccess: null,
        infraGrant: null,
      };
    }

    const guestAccess = await ctx.db
      .query("guestAccess")
      .withIndex("by_host_guest", (q) => q.eq("hostUserId", host._id).eq("guestUserId", guest._id))
      .filter((q) => q.eq(q.field("revokedAt"), undefined))
      .first();

    const infraGrant = await ctx.db
      .query("infraAccessGrants")
      .withIndex("by_host_guest", (q) => q.eq("hostUserId", host._id).eq("guestUserId", guest._id))
      .filter((q) => q.neq(q.field("status"), "revoked"))
      .first();

    return {
      hostFound: true,
      guestFound: true,
      hostUserId: host._id,
      guestUserId: guest._id,
      guestAccess,
      infraGrant,
    };
  },
});

/** Patch one active guest grant in place by host+guest email. CLI/dashboard only. */
export const patchActiveGuestGrantByEmails = mutation({
  args: {
    hostEmail: v.string(),
    guestEmail: v.string(),
    scope: v.optional(v.union(v.literal("full"), v.literal("feedback-only"), v.literal("sdk-project"))),
    allowedProjects: v.optional(v.array(v.string())),
  },
  handler: async (ctx, args) => {
    const host = await ctx.db
      .query("users")
      .withIndex("by_email", (q) => q.eq("email", args.hostEmail.trim().toLowerCase()))
      .unique();
    if (!host) throw new Error("Host user not found");

    const guest = await ctx.db
      .query("users")
      .withIndex("by_email", (q) => q.eq("email", args.guestEmail.trim().toLowerCase()))
      .unique();
    if (!guest) throw new Error("Guest user not found");

    const guestAccess = await ctx.db
      .query("guestAccess")
      .withIndex("by_host_guest", (q) => q.eq("hostUserId", host._id).eq("guestUserId", guest._id))
      .filter((q) => q.eq(q.field("revokedAt"), undefined))
      .first();
    if (!guestAccess) throw new Error("Active guestAccess row not found");

    const patch: Record<string, unknown> = {};
    if (args.scope !== undefined) patch.scope = args.scope;
    if (args.allowedProjects !== undefined) {
      const cleaned = args.allowedProjects.map((s) => s.trim()).filter(Boolean);
      patch.allowedProjects = cleaned.length > 0 ? cleaned : undefined;
    }
    if (Object.keys(patch).length === 0) {
      throw new Error("Nothing to patch");
    }

    await ctx.db.patch(guestAccess._id, patch);

    const updatedGuestAccess = await ctx.db.get(guestAccess._id);
    const infraGrant = await ctx.db
      .query("infraAccessGrants")
      .withIndex("by_host_guest", (q) => q.eq("hostUserId", host._id).eq("guestUserId", guest._id))
      .filter((q) => q.neq(q.field("status"), "revoked"))
      .first();

    return {
      ok: true,
      hostEmail: host.email,
      guestEmail: guest.email,
      guestAccess: updatedGuestAccess,
      infraGrant,
    };
  },
});

/** Delete a user and ALL their data by user _id. */
export const deleteUserData = mutation({
  args: { userId: v.id("users") },
  handler: async (ctx, args) => {
    const user = await ctx.db.get(args.userId);
    if (!user) throw new Error("User not found");

    const counts: Record<string, number> = {};

    // Delete from all user-scoped tables
    const tables = ["sessions", "devices", "userSettings", "developerSurveys", "runnerUsage", "dailyTaskCounts", "deviceMetrics", "deviceEvents"] as const;
    for (const table of tables) {
      const docs = await ctx.db.query(table).collect();
      const userDocs = docs.filter((d: any) => d.userId === args.userId);
      for (const doc of userDocs) {
        await ctx.db.delete(doc._id);
      }
      counts[table] = userDocs.length;
    }

    // Delete the user
    await ctx.db.delete(args.userId);

    return {
      email: user.email,
      ...counts,
    };
  },
});

// ── Org admin console queries ────────────────────────────────────────
//
// Everything below this line backs the /admin web route. The HTTP
// surface enforces the admin gate (env-var owner allowlist for now);
// these queries assume the caller has already passed it.

/** Aggregated counts + fleet-health alerts for the Overview tab. */
export const dashboardCounts = query({
  args: {},
  handler: async (ctx) => {
    const now = Date.now();
    const SEVEN_DAYS = 7 * 24 * 60 * 60 * 1000;
    const THIRTY_DAYS = 30 * 24 * 60 * 60 * 1000;

    const users = await ctx.db.query("users").collect();
    const devices = await ctx.db.query("devices").collect();
    const sessions = await ctx.db.query("sessions").collect();
    const teams = await ctx.db.query("teams").collect();

    const activeSessions = sessions.filter(
      (s: any) => !s.expiresAt || s.expiresAt > now,
    );
    const staleDevices = devices.filter(
      (d: any) => (d.lastHeartbeat ?? 0) < now - SEVEN_DAYS,
    );
    const usersWithoutMfa = users.filter((u: any) => !u.totpEnabled).length;

    // 30-day activity sparkline — day-bucketed userActivity counts.
    const since = now - THIRTY_DAYS;
    const recentActivity = await ctx.db
      .query("userActivity")
      .filter((q) => q.gte(q.field("timestamp"), since))
      .collect();
    const buckets: number[] = new Array(30).fill(0);
    for (const row of recentActivity) {
      const dayIdx = Math.floor((row.timestamp - since) / (24 * 60 * 60 * 1000));
      if (dayIdx >= 0 && dayIdx < 30) buckets[dayIdx] += 1;
    }

    return {
      counts: {
        users: users.length,
        devices: devices.length,
        activeSessions: activeSessions.length,
        teams: teams.length,
      },
      alerts: {
        staleDevices: staleDevices.length,
        usersWithoutMfa,
      },
      sparkline: buckets,
    };
  },
});

/** Recent merged audit feed (userActivity + securityEvents) for the
 *  Overview "last 5" card. The Audit page below uses mergedAuditFeed
 *  with a paginate cursor. */
export const recentAuditEvents = query({
  args: { limit: v.optional(v.number()) },
  handler: async (ctx, args) => {
    const limit = Math.min(Math.max(args.limit ?? 5, 1), 50);
    const activity = await ctx.db
      .query("userActivity")
      .order("desc")
      .take(limit);
    const security = await ctx.db
      .query("securityEvents")
      .order("desc")
      .take(limit);

    // Hydrate email for actor display.
    const userIds = new Set<string>([
      ...activity.map((a: any) => String(a.userId)),
      ...security.map((s: any) => String(s.userId)),
    ]);
    const emailById = new Map<string, string>();
    for (const id of userIds) {
      const u = await ctx.db.get(id as any);
      if (u && (u as any).email) emailById.set(id, (u as any).email);
    }

    const merged: Array<{
      timestamp: number;
      kind: "activity" | "security";
      actor: string;
      action: string;
      target: string;
      outcome: string;
    }> = [];
    for (const row of activity as any[]) {
      merged.push({
        timestamp: row.timestamp,
        kind: "activity",
        actor: emailById.get(String(row.userId)) ?? "(unknown)",
        action: row.action,
        target: row.target ?? row.deviceId ?? "",
        outcome: row.outcome,
      });
    }
    for (const row of security as any[]) {
      merged.push({
        timestamp: row.createdAt ?? row._creationTime,
        kind: "security",
        actor: emailById.get(String(row.userId)) ?? "(unknown)",
        action: row.eventType,
        target: "",
        outcome: row.read ? "read" : "unread",
      });
    }
    merged.sort((a, b) => b.timestamp - a.timestamp);
    return merged.slice(0, limit);
  },
});

/** Merged audit feed with filters + cursor pagination — backs the
 *  Audit tab. Cursor is the timestamp of the last seen row; pass
 *  cursor=undefined for the first page. */
export const mergedAuditFeed = query({
  args: {
    limit: v.optional(v.number()),
    cursor: v.optional(v.number()),
    actorEmail: v.optional(v.string()),
    eventType: v.optional(v.string()),
    sinceMs: v.optional(v.number()),
    untilMs: v.optional(v.number()),
  },
  handler: async (ctx, args) => {
    const limit = Math.min(Math.max(args.limit ?? 50, 1), 500);
    const cursor = args.cursor ?? Number.MAX_SAFE_INTEGER;
    const since = args.sinceMs ?? 0;
    const until = Math.min(args.untilMs ?? Number.MAX_SAFE_INTEGER, cursor);

    // Pull a generous window from both tables, then merge + trim.
    // For the data scales this admin console will see in practice
    // (a few thousand rows/day), in-memory merge is cheap and
    // avoids a custom index.
    const activity = await ctx.db
      .query("userActivity")
      .filter((q) =>
        q.and(
          q.gte(q.field("timestamp"), since),
          q.lte(q.field("timestamp"), until),
        ),
      )
      .order("desc")
      .take(limit * 2);

    const security = await ctx.db
      .query("securityEvents")
      .filter((q) =>
        q.and(
          q.gte(q.field("createdAt"), since),
          q.lte(q.field("createdAt"), until),
        ),
      )
      .order("desc")
      .take(limit * 2);

    const userIds = new Set<string>([
      ...activity.map((a: any) => String(a.userId)),
      ...security.map((s: any) => String(s.userId)),
    ]);
    const emailById = new Map<string, string>();
    for (const id of userIds) {
      const u = await ctx.db.get(id as any);
      if (u && (u as any).email) emailById.set(id, (u as any).email);
    }

    type Row = {
      timestamp: number;
      kind: "activity" | "security";
      actor: string;
      actorId: string;
      action: string;
      target: string;
      outcome: string;
      details: string;
    };
    const merged: Row[] = [];
    for (const row of activity as any[]) {
      const actorEmail = emailById.get(String(row.userId)) ?? "(unknown)";
      if (args.actorEmail && actorEmail !== args.actorEmail) continue;
      if (args.eventType && row.action !== args.eventType) continue;
      merged.push({
        timestamp: row.timestamp,
        kind: "activity",
        actor: actorEmail,
        actorId: String(row.userId),
        action: row.action,
        target: row.target ?? row.deviceId ?? "",
        outcome: row.outcome,
        details: row.error ?? "",
      });
    }
    for (const row of security as any[]) {
      const actorEmail = emailById.get(String(row.userId)) ?? "(unknown)";
      if (args.actorEmail && actorEmail !== args.actorEmail) continue;
      if (args.eventType && row.eventType !== args.eventType) continue;
      merged.push({
        timestamp: row.createdAt ?? row._creationTime,
        kind: "security",
        actor: actorEmail,
        actorId: String(row.userId),
        action: row.eventType,
        target: "",
        outcome: row.read ? "read" : "unread",
        details: row.details ?? "",
      });
    }
    merged.sort((a, b) => b.timestamp - a.timestamp);
    const page = merged.slice(0, limit);
    const nextCursor = page.length === limit ? page[page.length - 1].timestamp - 1 : null;
    return { rows: page, nextCursor };
  },
});

/** All devices with the owner email joined — backs the Devices tab. */
export const fleetDevices = query({
  args: {},
  handler: async (ctx) => {
    const devices = await ctx.db.query("devices").collect();
    const ownerIds = new Set(devices.map((d: any) => String(d.userId)));
    const emailById = new Map<string, string>();
    for (const id of ownerIds) {
      const u = await ctx.db.get(id as any);
      if (u && (u as any).email) emailById.set(id, (u as any).email);
    }
    const now = Date.now();
    return devices
      .map((d: any) => ({
        _id: String(d._id),
        deviceId: d.deviceId,
        name: d.name,
        alias: d.alias ?? null,
        ownerEmail: emailById.get(String(d.userId)) ?? "(unknown)",
        ownerId: String(d.userId),
        platform: d.platform,
        agentVersion: d.agentVersion ?? null,
        lastHeartbeat: d.lastHeartbeat ?? 0,
        isOnline:
          (d.lastHeartbeat ?? 0) > now - 5 * 60 * 1000 && !d.runnerDown,
        runnerDown: d.runnerDown === true,
        needsAuth: d.needsAuth === true,
        publicEndpoints: d.publicEndpoints ?? [],
        tunnelUrl: d.tunnelUrl ?? null,
      }))
      .sort((a, b) => b.lastHeartbeat - a.lastHeartbeat);
  },
});

/** Active sessions with user email + device alias joined. */
export const activeSessionsForAdmin = query({
  args: {},
  handler: async (ctx) => {
    const now = Date.now();
    const sessions = await ctx.db.query("sessions").collect();
    const active = sessions.filter(
      (s: any) => !s.expiresAt || s.expiresAt > now,
    );
    const userIds = new Set(active.map((s: any) => String(s.userId)));
    const emailById = new Map<string, string>();
    for (const id of userIds) {
      const u = await ctx.db.get(id as any);
      if (u && (u as any).email) emailById.set(id, (u as any).email);
    }
    return active
      .map((s: any) => ({
        _id: String(s._id),
        email: emailById.get(String(s.userId)) ?? "(unknown)",
        userId: String(s.userId),
        deviceId: s.deviceId ?? null,
        surface: s.surface ?? "web",
        createdAt: s._creationTime ?? 0,
        lastRefreshAt: s.lastRefreshAt ?? s._creationTime ?? 0,
        expiresAt: s.expiresAt ?? 0,
      }))
      .sort((a, b) => b.lastRefreshAt - a.lastRefreshAt);
  },
});

/** All users with derived fields (mfa status, team count, last seen).
 *  Used by the Users tab (and scaffolded as the structured response
 *  even though the UI ships paginated/searched next pass). */
export const allUsersForAdmin = query({
  args: {},
  handler: async (ctx) => {
    const users = await ctx.db.query("users").collect();
    const teamMembers = await ctx.db.query("teamMembers").collect();
    const sessions = await ctx.db.query("sessions").collect();

    const teamsByUser = new Map<string, number>();
    for (const m of teamMembers as any[]) {
      const k = String(m.userId);
      teamsByUser.set(k, (teamsByUser.get(k) ?? 0) + 1);
    }
    const lastSeenByUser = new Map<string, number>();
    for (const s of sessions as any[]) {
      const k = String(s.userId);
      const t = s.lastRefreshAt ?? s._creationTime ?? 0;
      if (t > (lastSeenByUser.get(k) ?? 0)) lastSeenByUser.set(k, t);
    }

    return users
      .map((u: any) => ({
        _id: String(u._id),
        email: u.email,
        fullName: u.fullName,
        provider: u.provider,
        avatarUrl: u.avatarUrl ?? null,
        mfaEnabled: !!u.totpEnabled,
        emailVerified: !!u.emailVerified,
        teamCount: teamsByUser.get(String(u._id)) ?? 0,
        lastSeenAt: lastSeenByUser.get(String(u._id)) ?? 0,
        createdAt: u.createdAt,
        // platformRole reads from the (not-yet-migrated) field; once
        // the schema migration ships this will become authoritative.
        platformRole: (u as any).platformRole ?? null,
      }))
      .sort((a, b) => b.lastSeenAt - a.lastSeenAt);
  },
});

// ── Admin-action helpers (audit + crypto) ────────────────────────────

/** Append a row to securityEvents for any admin-initiated action. The
 *  HTTP gate already validated the caller; this just records who-did-
 *  what-to-whom so the Audit tab + SIEM export catches it. Failures
 *  here MUST NOT block the action — admin work continues even if the
 *  audit write itself fails (we surface the failure to the operator
 *  separately via an additional log line). */
async function logAdminAction(
  ctx: { db: any },
  callerDocId: any,
  eventType: string,
  details: Record<string, unknown>,
): Promise<void> {
  try {
    await ctx.db.insert("securityEvents", {
      userId: callerDocId,
      eventType,
      details: JSON.stringify(details).slice(0, 4096),
      read: false,
      createdAt: Date.now(),
    });
  } catch (err) {
    console.error("[admin] audit-log write failed:", err);
  }
}

function b64Decode(s: string): Uint8Array {
  const bin = atob(s);
  const out = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i);
  return out;
}

function b64Encode(bytes: Uint8Array): string {
  let s = "";
  for (let i = 0; i < bytes.length; i++) s += String.fromCharCode(bytes[i]);
  return btoa(s);
}

function getOidcKeyMaterial(): Uint8Array {
  const raw = process.env.OIDC_SECRET_ENCRYPTION_KEY || "";
  if (!raw) {
    throw new Error(
      "OIDC_SECRET_ENCRYPTION_KEY is not set on the Convex deployment. " +
        "Generate a 32-byte key (`openssl rand -base64 32`) and set it: " +
        "`npx convex env set OIDC_SECRET_ENCRYPTION_KEY <base64>`",
    );
  }
  const key = b64Decode(raw);
  if (key.length !== 32) {
    throw new Error(
      `OIDC_SECRET_ENCRYPTION_KEY must decode to 32 bytes (got ${key.length}). ` +
        "Use `openssl rand -base64 32`.",
    );
  }
  return key;
}

async function encryptOidcSecret(plaintext: string): Promise<string> {
  const keyMat = getOidcKeyMaterial();
  const key = await crypto.subtle.importKey(
    "raw",
    keyMat.buffer as ArrayBuffer,
    { name: "AES-GCM" },
    false,
    ["encrypt"],
  );
  const iv = crypto.getRandomValues(new Uint8Array(12));
  const cipher = await crypto.subtle.encrypt(
    { name: "AES-GCM", iv },
    key,
    new TextEncoder().encode(plaintext),
  );
  const combined = new Uint8Array(iv.length + cipher.byteLength);
  combined.set(iv, 0);
  combined.set(new Uint8Array(cipher), iv.length);
  return b64Encode(combined);
}

async function decryptOidcSecret(blob: string): Promise<string> {
  const combined = b64Decode(blob);
  if (combined.length < 13) throw new Error("OIDC ciphertext truncated");
  const iv = combined.slice(0, 12);
  const cipher = combined.slice(12);
  const keyMat = getOidcKeyMaterial();
  const key = await crypto.subtle.importKey(
    "raw",
    keyMat.buffer as ArrayBuffer,
    { name: "AES-GCM" },
    false,
    ["decrypt"],
  );
  const plain = await crypto.subtle.decrypt(
    { name: "AES-GCM", iv },
    key,
    cipher,
  );
  return new TextDecoder().decode(plain);
}

// Exported for the OIDC callback handler in http.ts — the only other
// place that needs to decrypt the stored client secret.
export async function decryptStoredOidcSecret(blob: string): Promise<string> {
  return decryptOidcSecret(blob);
}

// ── User actions ─────────────────────────────────────────────────────

/** Mark a user as platform admin. The HTTP gate ensures the caller is
 *  themselves an admin (env-var allowlist or platformRole === "admin");
 *  the schema migration that adds the column has shipped, so this is
 *  now the authoritative promotion path. */
export const promoteToAdmin = mutation({
  args: {
    targetEmail: v.string(),
    callerDocId: v.id("users"),
  },
  handler: async (ctx, args) => {
    const target = await ctx.db
      .query("users")
      .withIndex("by_email", (q) => q.eq("email", args.targetEmail))
      .unique();
    if (!target) {
      throw new Error(`No user with email ${args.targetEmail}`);
    }
    if (target.platformRole === "admin") {
      return { ok: true, alreadyAdmin: true, userDocId: String(target._id) };
    }
    await ctx.db.patch(target._id, { platformRole: "admin" });
    await logAdminAction(ctx, args.callerDocId, "admin.promote", {
      targetUserDocId: String(target._id),
      targetEmail: target.email,
    });
    return { ok: true, userDocId: String(target._id) };
  },
});

/** Revoke the admin role. Refuses if the target is the only remaining
 *  admin and there is no env-var bootstrap admin to take over. */
export const demoteFromAdmin = mutation({
  args: {
    targetDocId: v.id("users"),
    callerDocId: v.id("users"),
  },
  handler: async (ctx, args) => {
    const target = await ctx.db.get(args.targetDocId);
    if (!target) throw new Error("Target user not found");
    if (target.platformRole !== "admin") {
      return { ok: true, alreadyMember: true };
    }
    // Refuse last-admin-out: there must be either another schema
    // admin OR the env-var owner allowlist is populated.
    const admins = await ctx.db
      .query("users")
      .withIndex("by_platformRole", (q) => q.eq("platformRole", "admin"))
      .collect();
    const otherAdmins = admins.filter((u) => u._id !== target._id);
    const allowlistConfigured =
      (process.env.CLOUD_PREVIEW_OWNER_EMAILS || "").trim() !== "" ||
      (process.env.CLOUD_PREVIEW_OWNER_USER_IDS || "").trim() !== "";
    if (otherAdmins.length === 0 && !allowlistConfigured) {
      throw new Error(
        "Refusing to demote the last admin — set CLOUD_PREVIEW_OWNER_EMAILS " +
          "first so you can re-bootstrap if needed.",
      );
    }
    await ctx.db.patch(target._id, { platformRole: undefined });
    await logAdminAction(ctx, args.callerDocId, "admin.demote", {
      targetUserDocId: String(target._id),
      targetEmail: target.email,
    });
    return { ok: true };
  },
});

/** Delete every session token for a user. Forces re-auth on next
 *  request from every surface (web, mobile, CLI). */
export const signOutUserAllSessions = mutation({
  args: {
    targetDocId: v.id("users"),
    callerDocId: v.id("users"),
  },
  handler: async (ctx, args) => {
    const sessions = await ctx.db
      .query("sessions")
      .withIndex("by_userId", (q) => q.eq("userId", args.targetDocId))
      .collect();
    for (const s of sessions) await ctx.db.delete(s._id);
    const target = await ctx.db.get(args.targetDocId);
    await logAdminAction(ctx, args.callerDocId, "admin.sign_out_all", {
      targetUserDocId: String(args.targetDocId),
      targetEmail: target?.email,
      revoked: sessions.length,
    });
    return { ok: true, revoked: sessions.length };
  },
});

/** Cascade delete a user across every user-scoped table. GDPR
 *  right-to-erasure. Audit row is written BEFORE the user row is
 *  deleted so the foreign-key reference resolves. */
export const deleteUserCascade = mutation({
  args: {
    targetDocId: v.id("users"),
    callerDocId: v.id("users"),
  },
  handler: async (ctx, args) => {
    const target = await ctx.db.get(args.targetDocId);
    if (!target) throw new Error("Target user not found");
    if (target._id === args.callerDocId) {
      throw new Error("Refusing self-delete from admin console.");
    }

    await logAdminAction(ctx, args.callerDocId, "admin.delete_user", {
      targetUserDocId: String(args.targetDocId),
      targetEmail: target.email,
    });

    const tables = [
      "sessions",
      "devices",
      "userSettings",
      "developerSurveys",
      "runnerUsage",
      "dailyTaskCounts",
      "deviceMetrics",
      "deviceEvents",
      "authIdentities",
      "passkeys",
      "securityEvents",
      "userActivity",
      "agentRescueCommands",
    ] as const;
    const counts: Record<string, number> = {};
    for (const table of tables) {
      try {
        const docs = await ctx.db.query(table as any).collect();
        let n = 0;
        for (const d of docs as any[]) {
          if (d.userId === args.targetDocId || d.ownerUserId === args.targetDocId) {
            await ctx.db.delete(d._id);
            n++;
          }
        }
        counts[table] = n;
      } catch (err) {
        // Table may not exist on a self-hosted deployment — log and
        // continue, don't abort the rest of the cascade.
        console.warn(`[admin.delete] skipping ${table}:`, err);
        counts[table] = -1;
      }
    }

    await ctx.db.delete(args.targetDocId);
    counts.users = 1;
    return { ok: true, email: target.email, deleted: counts };
  },
});

/** Full single-user bundle for GDPR export. Same shape as
 *  exportUserBundleByEmail but takes the doc id directly. */
export const exportUserBundleById = query({
  args: { targetDocId: v.id("users") },
  handler: async (ctx, args) => {
    const user = await ctx.db.get(args.targetDocId);
    if (!user) return null;
    const settings = await ctx.db
      .query("userSettings")
      .withIndex("by_userId", (q) => q.eq("userId", user._id))
      .first();
    const devices = await ctx.db
      .query("devices")
      .withIndex("by_userId", (q) => q.eq("userId", user._id))
      .collect();
    const identities = await ctx.db
      .query("authIdentities")
      .withIndex("by_userId", (q) => q.eq("userId", user._id))
      .collect();
    const activity = await ctx.db
      .query("userActivity")
      .withIndex("by_user", (q) => q.eq("userId", user._id))
      .take(1000);
    return {
      exportedAt: new Date().toISOString(),
      user: { ...user, totpSecret: undefined, passwordHash: undefined },
      settings,
      devices,
      identities,
      activity,
    };
  },
});

// ── Session actions ──────────────────────────────────────────────────

/** Revoke one session by its document id. */
export const revokeSession = mutation({
  args: {
    sessionDocId: v.id("sessions"),
    callerDocId: v.id("users"),
  },
  handler: async (ctx, args) => {
    const session = await ctx.db.get(args.sessionDocId);
    if (!session) return { ok: true, alreadyGone: true };
    await ctx.db.delete(args.sessionDocId);
    await logAdminAction(ctx, args.callerDocId, "admin.revoke_session", {
      sessionDocId: String(args.sessionDocId),
      targetUserDocId: String(session.userId),
      deviceId: session.deviceId ?? null,
    });
    return { ok: true };
  },
});

// ── Device actions ───────────────────────────────────────────────────

/** Queue a rescue command for the device. The agent polls
 *  agentRescueCommands on every heartbeat. Commands are strict-enum
 *  so a compromised UI cannot inject arbitrary shell. */
export const queueAgentRescue = mutation({
  args: {
    deviceDocId: v.id("devices"),
    command: v.union(
      v.literal("restart"),
      v.literal("reinstall-latest"),
      v.literal("tunnel-reset"),
      v.literal("auth-reset"),
    ),
    callerDocId: v.id("users"),
  },
  handler: async (ctx, args) => {
    const device = await ctx.db.get(args.deviceDocId);
    if (!device) throw new Error("Device not found");
    const now = Date.now();
    const id = await ctx.db.insert("agentRescueCommands", {
      deviceId: device.deviceId,
      ownerUserId: device.userId,
      command: args.command,
      status: "pending",
      createdAt: now,
      expiresAt: now + 5 * 60 * 1000,
      sourceSurface: "web",
    });
    await logAdminAction(ctx, args.callerDocId, "admin.queue_rescue", {
      command: args.command,
      deviceId: device.deviceId,
      targetUserDocId: String(device.userId),
    });
    return { ok: true, rescueDocId: String(id) };
  },
});

/** Detach a device from its owner: delete every session attached to
 *  the deviceId and the device row itself. The agent will fail its
 *  next heartbeat and prompt the user to re-auth. */
export const revokeDevice = mutation({
  args: {
    deviceDocId: v.id("devices"),
    callerDocId: v.id("users"),
  },
  handler: async (ctx, args) => {
    const device = await ctx.db.get(args.deviceDocId);
    if (!device) return { ok: true, alreadyGone: true };
    const sessions = await ctx.db
      .query("sessions")
      .withIndex("by_deviceId", (q) => q.eq("deviceId", device.deviceId))
      .collect();
    for (const s of sessions) await ctx.db.delete(s._id);
    await ctx.db.delete(args.deviceDocId);
    await logAdminAction(ctx, args.callerDocId, "admin.revoke_device", {
      deviceId: device.deviceId,
      targetUserDocId: String(device.userId),
      sessionsKilled: sessions.length,
    });
    return { ok: true, sessionsKilled: sessions.length };
  },
});

// ── Org policy CRUD ──────────────────────────────────────────────────

/** Read the org policy singleton — null when the deployment has not
 *  yet configured one. Callers fall back to defaults. */
export const getOrgPolicy = query({
  args: {},
  handler: async (ctx) => {
    const row = await ctx.db
      .query("orgPolicy")
      .withIndex("by_singleton", (q) => q.eq("singletonKey", "org"))
      .first();
    if (!row) return null;
    return row;
  },
});

export const setOrgPolicy = mutation({
  args: {
    callerDocId: v.id("users"),
    enforceRelay: v.optional(v.boolean()),
    allowedRunners: v.optional(v.array(v.string())),
    allowedProviders: v.optional(v.array(v.string())),
    idleTimeoutMin: v.optional(v.number()),
    auditRetentionDays: v.optional(v.number()),
    requireMfaForAdmins: v.optional(v.boolean()),
  },
  handler: async (ctx, args) => {
    const { callerDocId, ...patch } = args;
    const existing = await ctx.db
      .query("orgPolicy")
      .withIndex("by_singleton", (q) => q.eq("singletonKey", "org"))
      .first();
    const now = Date.now();
    if (existing) {
      await ctx.db.patch(existing._id, {
        ...patch,
        updatedAt: now,
        updatedBy: callerDocId,
      });
    } else {
      await ctx.db.insert("orgPolicy", {
        singletonKey: "org",
        ...patch,
        updatedAt: now,
        updatedBy: callerDocId,
      });
    }
    await logAdminAction(ctx, callerDocId, "admin.policy_update", {
      patch,
    });
    return { ok: true };
  },
});

// ── OIDC config CRUD ─────────────────────────────────────────────────

/** Read the OIDC config — returns the safe-to-display shape (no
 *  ciphertext / secret bytes). hasClientSecret signals whether the
 *  caller needs to provide a new secret on save. */
export const getOidcConfig = query({
  args: {},
  handler: async (ctx) => {
    const row = await ctx.db
      .query("oidcConfig")
      .withIndex("by_singleton", (q) => q.eq("singletonKey", "org"))
      .first();
    if (!row) return null;
    return {
      _id: String(row._id),
      enabled: row.enabled,
      issuerUrl: row.issuerUrl,
      clientId: row.clientId,
      tenant: row.tenant ?? "",
      hasClientSecret: row.clientSecretEnc.length > 0,
      authorizationEndpoint: row.authorizationEndpoint ?? null,
      tokenEndpoint: row.tokenEndpoint ?? null,
      userinfoEndpoint: row.userinfoEndpoint ?? null,
      jwksUri: row.jwksUri ?? null,
      discoveredAt: row.discoveredAt ?? null,
      updatedAt: row.updatedAt,
    };
  },
});

/** Internal — returns the full row including ciphertext. Only the
 *  HTTP callback handler should call this. */
export const getOidcConfigRaw = query({
  args: {},
  handler: async (ctx) => {
    return await ctx.db
      .query("oidcConfig")
      .withIndex("by_singleton", (q) => q.eq("singletonKey", "org"))
      .first();
  },
});

export const setOidcConfig = mutation({
  args: {
    callerDocId: v.id("users"),
    enabled: v.boolean(),
    issuerUrl: v.string(),
    clientId: v.string(),
    /** Plaintext. Encrypted before storage. Empty string = keep
     *  existing secret. */
    clientSecret: v.string(),
    tenant: v.optional(v.string()),
    /** Discovered endpoints from the .well-known fetch. Caller is the
     *  HTTP layer which has just resolved them. */
    discovered: v.optional(v.object({
      authorizationEndpoint: v.string(),
      tokenEndpoint: v.string(),
      userinfoEndpoint: v.string(),
      jwksUri: v.string(),
    })),
  },
  handler: async (ctx, args) => {
    const existing = await ctx.db
      .query("oidcConfig")
      .withIndex("by_singleton", (q) => q.eq("singletonKey", "org"))
      .first();
    const enc = args.clientSecret.length === 0
      ? existing?.clientSecretEnc ?? ""
      : await encryptOidcSecret(args.clientSecret);
    if (args.clientSecret.length === 0 && !existing) {
      throw new Error("Client secret is required on first save.");
    }
    const now = Date.now();
    const patch = {
      enabled: args.enabled,
      issuerUrl: args.issuerUrl.trim(),
      clientId: args.clientId.trim(),
      clientSecretEnc: enc,
      tenant: (args.tenant ?? "").trim() || undefined,
      authorizationEndpoint: args.discovered?.authorizationEndpoint,
      tokenEndpoint: args.discovered?.tokenEndpoint,
      userinfoEndpoint: args.discovered?.userinfoEndpoint,
      jwksUri: args.discovered?.jwksUri,
      discoveredAt: args.discovered ? now : existing?.discoveredAt,
      updatedAt: now,
      updatedBy: args.callerDocId,
    };
    if (existing) {
      await ctx.db.patch(existing._id, patch);
    } else {
      await ctx.db.insert("oidcConfig", { singletonKey: "org", ...patch });
    }
    await logAdminAction(ctx, args.callerDocId, "admin.oidc_update", {
      issuerUrl: patch.issuerUrl,
      enabled: patch.enabled,
      tenant: patch.tenant ?? "",
      clientSecretRotated: args.clientSecret.length > 0,
    });
    return { ok: true };
  },
});

export const clearOidcConfig = mutation({
  args: { callerDocId: v.id("users") },
  handler: async (ctx, args) => {
    const existing = await ctx.db
      .query("oidcConfig")
      .withIndex("by_singleton", (q) => q.eq("singletonKey", "org"))
      .first();
    if (existing) await ctx.db.delete(existing._id);
    await logAdminAction(ctx, args.callerDocId, "admin.oidc_clear", {});
    return { ok: true };
  },
});

// ── OIDC ephemeral state (PKCE + nonce + return-to) ──────────────────

export const startOidcAttempt = mutation({
  args: {
    state: v.string(),
    codeVerifier: v.string(),
    nonce: v.string(),
    returnTo: v.optional(v.string()),
  },
  handler: async (ctx, args) => {
    const ttlMs = 10 * 60 * 1000;
    await ctx.db.insert("oidcAuthAttempts", {
      state: args.state,
      codeVerifier: args.codeVerifier,
      nonce: args.nonce,
      returnTo: args.returnTo,
      createdAt: Date.now(),
      expiresAt: Date.now() + ttlMs,
    });
  },
});

export const consumeOidcAttempt = mutation({
  args: { state: v.string() },
  handler: async (ctx, args) => {
    const row = await ctx.db
      .query("oidcAuthAttempts")
      .withIndex("by_state", (q) => q.eq("state", args.state))
      .unique();
    if (!row) return null;
    await ctx.db.delete(row._id);
    if (row.expiresAt < Date.now()) return null;
    return {
      codeVerifier: row.codeVerifier,
      nonce: row.nonce,
      returnTo: row.returnTo ?? null,
    };
  },
});

/** Upsert a user signed in via OIDC. Provider = "oidc"; providerId =
 *  `${issuer}|${sub}` so two different IdPs with the same subject
 *  identifier don't collide. */
export const upsertOidcUser = mutation({
  args: {
    issuer: v.string(),
    sub: v.string(),
    email: v.string(),
    name: v.optional(v.string()),
    avatarUrl: v.optional(v.string()),
  },
  handler: async (ctx, args) => {
    const providerId = `${args.issuer}|${args.sub}`;
    const byProvider = await ctx.db
      .query("users")
      .withIndex("by_provider", (q) =>
        q.eq("provider", "oidc").eq("providerId", providerId),
      )
      .unique();
    const byEmail = args.email
      ? await ctx.db
          .query("users")
          .withIndex("by_email", (q) => q.eq("email", args.email))
          .unique()
      : null;
    let userDocId;
    const now = Date.now();
    if (byProvider) {
      await ctx.db.patch(byProvider._id, {
        email: args.email || byProvider.email,
        fullName: args.name || byProvider.fullName,
        avatarUrl: args.avatarUrl ?? byProvider.avatarUrl,
        emailVerified: true,
        emailVerifiedAt: byProvider.emailVerifiedAt ?? now,
      });
      userDocId = byProvider._id;
    } else if (byEmail && !byEmail.passwordHash) {
      // Email match but no provider link yet — adopt the row as an
      // OIDC user. Skip if the user has a password (don't silently
      // hand their account to an OIDC IdP without explicit linking).
      await ctx.db.patch(byEmail._id, {
        provider: "oidc",
        providerId,
        fullName: args.name || byEmail.fullName,
        avatarUrl: args.avatarUrl ?? byEmail.avatarUrl,
        emailVerified: true,
        emailVerifiedAt: byEmail.emailVerifiedAt ?? now,
      });
      userDocId = byEmail._id;
    } else {
      userDocId = await ctx.db.insert("users", {
        userId: `oidc_${args.sub}`,
        email: args.email,
        fullName: args.name || args.email,
        provider: "oidc",
        providerId,
        avatarUrl: args.avatarUrl,
        emailVerified: true,
        emailVerifiedAt: now,
        createdAt: now,
      });
    }
    // Maintain an authIdentities row for the provider-list UI in
    // settings — same pattern as the other OAuth providers.
    const existingIdentity = await ctx.db
      .query("authIdentities")
      .withIndex("by_provider", (q) =>
        q.eq("provider", "oidc").eq("providerId", providerId),
      )
      .unique();
    if (existingIdentity) {
      await ctx.db.patch(existingIdentity._id, { lastUsedAt: now });
    } else {
      await ctx.db.insert("authIdentities", {
        userId: userDocId,
        provider: "oidc",
        providerId,
        email: args.email,
        createdAt: now,
        lastUsedAt: now,
      });
    }
    return { userDocId, userId: `oidc_${args.sub}`, email: args.email };
  },
});
