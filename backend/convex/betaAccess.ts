// betaAccess.ts — owner-gated "invisible infra share" for the beta
// soft-launch. See beta-invisible-infra-share-design.md.
//
// Lets the OWNER (kivanc.cakmak@icloud.com, via ownerAllowlist env) seed a
// beta user so they transparently use the owner's GLM key (Cloudflare
// gateway) + the owner's Hetzner box (hidden infra grant). The included
// model lane is opencode/GLM through the gateway; claude/codex are allowed
// only as BYO OAuth runners inside the tenant runtime. The beta user sees a
// "Beta" badge and nothing else —
// no device, no guest relationship, no owner identity.
//
// Reuses, never re-implements, the existing rails:
//   • inference key  → cloudLifecycle.topUp (free wallet grant)
//                      + gatewayPolicy.setPolicyInternal (caps)
//   • visible badge  → cloudLifecycle.grantIncludedHours(plan:"beta")
//   • box access     → infraAccessGrants row (hidden:true, beta:true)
//   • revoke         → gatewayPolicy enabled:false + grant revoked
//                      + gatewayTokens.revokeAllForUserInternal
//
// Owner gating: the HOST of every grant is forced to an owner userId and
// asserted via ownerAllowlist.isOwnerUserId. A non-owner host id is
// refused. Invoke as the deployment owner:
//   npx convex run betaAccess:seedBetaUser '{"guestUserId":"<id>"}'
//   npx convex run betaAccess:revokeBetaUser '{"guestUserId":"<id>"}'

import { v } from "convex/values";
import { internalMutation, internalAction, internalQuery } from "./_generated/server";
import { internal } from "./_generated/api";
import { Id } from "./_generated/dataModel";
import { isOwnerUserId } from "./ownerAllowlist";

// Default beta envelope (override per call). These are the *only* numbers
// that bound your exposure: grantCents is the hard inference ceiling per
// user; sum across beta users is your global ceiling.
const DEFAULT_BETA_GRANT_CENTS = 500; // ~$5 of inference, per user
const DEFAULT_BETA_DAILY_CAP_CENTS = 200; // $2/day
const DEFAULT_BETA_HOURLY_CAP_CENTS = 50; // $0.50/hr — catches runaway loops
const DEFAULT_BETA_MAX_TOKENS_PER_REQUEST = 4096;
const DEFAULT_BETA_MAX_CENTS_PER_REQUEST = 25;
const DEFAULT_BETA_INCLUDED_HOURS = 20; // box hours (visible Beta allowance)

// Resolve the owner (host) userId: explicit arg wins, else the first entry
// of CLOUD_PREVIEW_OWNER_USER_IDS. Asserts it is actually an owner.
function resolveOwnerHost(hostUserId?: string): Id<"users"> {
  let host = (hostUserId ?? "").trim();
  if (!host) {
    host = (process.env.CLOUD_PREVIEW_OWNER_USER_IDS || "")
      .split(",")
      .map((s) => s.trim())
      .filter(Boolean)[0] ?? "";
  }
  if (!host) {
    throw new Error(
      "betaAccess: no host owner — set CLOUD_PREVIEW_OWNER_USER_IDS or pass hostUserId",
    );
  }
  if (!isOwnerUserId(host)) {
    throw new Error("betaAccess: host is not an owner (ownerAllowlist) — refused");
  }
  return host as Id<"users">;
}

const DEFAULT_BETA_ALLOWED_RUNNERS = ["opencode", "claude", "codex"];

function normalizeAllowedRunners(runners?: string[]): string[] {
  const src = runners && runners.length ? runners : DEFAULT_BETA_ALLOWED_RUNNERS;
  const out = new Set<string>();
  for (const raw of src) {
    const r = raw.trim().toLowerCase();
    if (r === "claude-code") out.add("claude");
    else if (r === "claude" || r === "codex" || r === "opencode") out.add(r);
  }
  if (out.size === 0) out.add("opencode");
  return [...out];
}

// Create/refresh the HIDDEN infra grant host(owner) → guest(beta). Honored
// by access-control (the beta user can reach the box) but skipped by UI
// listing (listVisibleInfraGrantsForGuest) so it never shows. Isolation
// required; never the host's API keys.
export const createHiddenBetaGrant = internalMutation({
  args: {
    hostUserId: v.id("users"),
    guestUserId: v.id("users"),
    sharedProject: v.optional(v.string()),
    allowedRunners: v.optional(v.array(v.string())),
  },
  handler: async (ctx, { hostUserId, guestUserId, sharedProject, allowedRunners }) => {
    const now = Date.now();
    const existing = await ctx.db
      .query("infraAccessGrants")
      .withIndex("by_host_guest", (q) =>
        q.eq("hostUserId", hostUserId).eq("guestUserId", guestUserId),
      )
      .first();
    const fields = {
      status: "active" as const,
      hidden: true,
      beta: true,
      requireIsolation: true,
      useHostApiKeys: false, // inference goes through the gateway, never host keys
      allowGuestProvidedApiKeys: false,
      allowDesktopControl: false,
      allowBrowserControl: false,
      allowTunnelForward: false,
      shareAllMachines: true, // run beta tenants on a DEDICATED box (not your personal one)
      shareAllDevices: false,
      // opencode = included gateway/GLM lane. claude/codex = BYO OAuth only;
      // the agent must run them in the tenant runtime with useHostApiKeys=false.
      allowedRunners: normalizeAllowedRunners(allowedRunners),
      sharedProject: sharedProject ?? existing?.sharedProject,
      updatedAt: now,
      revokedAt: undefined,
    };
    if (existing) {
      await ctx.db.patch(existing._id, fields);
      return { grantId: String(existing._id), created: false };
    }
    const id = await ctx.db.insert("infraAccessGrants", {
      hostUserId,
      guestUserId,
      grantedAt: now,
      ...fields,
    });
    return { grantId: String(id), created: true };
  },
});

// Revoke the hidden beta grant (host → guest). Leaves the row for audit.
export const revokeBetaGrant = internalMutation({
  args: { hostUserId: v.id("users"), guestUserId: v.id("users") },
  handler: async (ctx, { hostUserId, guestUserId }) => {
    const now = Date.now();
    const grants = await ctx.db
      .query("infraAccessGrants")
      .withIndex("by_host_guest", (q) =>
        q.eq("hostUserId", hostUserId).eq("guestUserId", guestUserId),
      )
      .collect();
    let revoked = 0;
    for (const g of grants) {
      if (g.beta && g.status === "active") {
        await ctx.db.patch(g._id, { status: "revoked", revokedAt: now, updatedAt: now });
        revoked++;
      }
    }
    return { revoked };
  },
});

type SeedResult = {
  ok: boolean;
  hostUserId: string;
  guestUserId: string;
  grantId: string;
  grantCents: number;
};

// One-call seed. Owner-only (host asserted via ownerAllowlist).
export const seedBetaUser = internalAction({
  args: {
    guestUserId: v.id("users"),
    hostUserId: v.optional(v.id("users")),
    sharedProject: v.optional(v.string()),
    grantCents: v.optional(v.number()),
    dailyCapCents: v.optional(v.number()),
    hourlyCapCents: v.optional(v.number()),
    includedHours: v.optional(v.number()),
    allowedRunners: v.optional(v.array(v.string())),
  },
  handler: async (ctx, args): Promise<SeedResult> => {
    const host = resolveOwnerHost(args.hostUserId);
    const grantCents = args.grantCents ?? DEFAULT_BETA_GRANT_CENTS;

    // 1. Free inference wallet grant (hard per-user ceiling).
    await ctx.runMutation(internal.cloudLifecycle.topUp, {
      userId: args.guestUserId,
      amountCents: grantCents,
    });

    // 2. Gateway caps (operator-set, user-immutable). enabled = the tap.
    await ctx.runMutation(internal.gatewayPolicy.setPolicyInternal, {
      userId: args.guestUserId,
      enabled: true,
      dailyCapCents: args.dailyCapCents ?? DEFAULT_BETA_DAILY_CAP_CENTS,
      hourlyCapCents: args.hourlyCapCents ?? DEFAULT_BETA_HOURLY_CAP_CENTS,
      maxTokensPerRequest: DEFAULT_BETA_MAX_TOKENS_PER_REQUEST,
      maxCentsPerRequest: DEFAULT_BETA_MAX_CENTS_PER_REQUEST,
      freeGrantCents: grantCents,
      note: "beta soft-launch (owner-funded)",
      setBy: String(host),
    });

    // 3. Visible "Beta" badge + included box hours (the ONLY thing the
    //    beta user sees — plan:"beta" surfaces via the entitlement layer).
    await ctx.runMutation(internal.cloudLifecycle.grantIncludedHours, {
      userId: args.guestUserId,
      plan: "beta",
      hours: args.includedHours ?? DEFAULT_BETA_INCLUDED_HOURS,
      source: "beta-grant",
    });

    // 4. Hidden infra grant → reach the owner's box, opencode only, unseen.
    //    sharedProject (e.g. "sfmg"/"carrotbet") tells the box-side seeder
    //    which repo to scrub+clone into the tenant partition.
    const grant = await ctx.runMutation(internal.betaAccess.createHiddenBetaGrant, {
      hostUserId: host,
      guestUserId: args.guestUserId,
      sharedProject: args.sharedProject,
      allowedRunners: args.allowedRunners,
    });

    return {
      ok: true,
      hostUserId: String(host),
      guestUserId: String(args.guestUserId),
      grantId: grant.grantId,
      grantCents,
    };
  },
});

// Resolve a (possibly not-yet-existing) email to a userId. Used so the
// owner can seed by email instead of hunting for a Convex _id.
export const resolveUserByEmail = internalQuery({
  args: { email: v.string() },
  handler: async (ctx, { email }) => {
    const normalized = email.trim().toLowerCase();
    const u = await ctx.db
      .query("users")
      .withIndex("by_email", (q) => q.eq("email", normalized))
      .first();
    return u ? { userId: String(u._id) } : null;
  },
});

// Beta entitlement for a user — what the web/PC + mobile clients read to
// decide whether to render the Beta workspace view (project + vibe box)
// instead of the normal infra/wallet UI. Counters/labels only. The hidden
// infra grant + gateway policy stay invisible; only this distilled status
// surfaces. Wired into GET /subscription as `beta`.
export const getBetaStatus = internalQuery({
  args: { userId: v.id("users") },
  handler: async (ctx, { userId }) => {
    const period = new Date().toISOString().slice(0, 7); // "YYYY-MM" (UTC)
    const allowances = await ctx.db
      .query("includedAllowance")
      .withIndex("by_user_period_type", (q) => q.eq("userId", userId))
      .collect();
    const betaAllow = allowances.find((a) => a.plan === "beta" && a.period === period);
    const grants = await ctx.db
      .query("infraAccessGrants")
      .withIndex("by_guestUserId", (q) => q.eq("guestUserId", userId))
      .filter((q) => q.eq(q.field("status"), "active"))
      .collect();
    const grant = grants.find((g) => g.beta);
    const policy = await ctx.db
      .query("gatewayPolicy")
      .withIndex("by_user", (q) => q.eq("userId", userId))
      .unique();
    const isBeta = !!betaAllow || !!grant;
    return {
      isBeta,
      plan: isBeta ? "beta" : null,
      sharedProject: grant?.sharedProject ?? null, // "sfmg" | "carrotbet" | null
      includedHours: betaAllow ? Math.round(betaAllow.includedSeconds / 3600) : 0,
      usedHours: betaAllow ? Math.round(betaAllow.usedSeconds / 3600) : 0,
      aiEnabled: policy?.enabled ?? false,
    };
  },
});

type SeedByEmailResult =
  | (SeedResult & { found: true })
  | { ok: false; found: false; email: string };

// Owner convenience: seed by EMAIL. Errors if the user hasn't signed up
// yet (no pending-invite machinery in this phase — ask them to install +
// sign in once, then re-run). Run:
//   npx convex run betaAccess:seedBetaUserByEmail \
//     '{"email":"dsahinbas@gmail.com","sharedProject":"carrotbet"}'
export const seedBetaUserByEmail = internalAction({
  args: {
    email: v.string(),
    sharedProject: v.optional(v.string()),
    hostUserId: v.optional(v.id("users")),
    grantCents: v.optional(v.number()),
    includedHours: v.optional(v.number()),
    allowedRunners: v.optional(v.array(v.string())),
  },
  handler: async (ctx, args): Promise<SeedByEmailResult> => {
    const found = await ctx.runQuery(internal.betaAccess.resolveUserByEmail, {
      email: args.email,
    });
    if (!found) return { ok: false, found: false, email: args.email };
    const r: SeedResult = await ctx.runAction(internal.betaAccess.seedBetaUser, {
      guestUserId: found.userId as Id<"users">,
      hostUserId: args.hostUserId,
      sharedProject: args.sharedProject,
      grantCents: args.grantCents,
      includedHours: args.includedHours,
      allowedRunners: args.allowedRunners,
    });
    return { ...r, found: true };
  },
});

// SAFE clean-slate (NOT account deletion). Removes a beta user's device
// rows so a reinstall comes in fresh with no machine of their own — the
// reversible way to get the "remove their machines" result without nuking
// identity/auth/data. Refuses to touch an owner. cloudMachines are NOT
// deleted here (deleting a row without destroying the Hetzner box would
// orphan billing) — the count is returned so you can tear any down via the
// deliberate existing destroy flow.
export const resetBetaUserDevices = internalMutation({
  args: { guestUserId: v.id("users") },
  handler: async (ctx, { guestUserId }) => {
    if (isOwnerUserId(String(guestUserId))) {
      throw new Error("resetBetaUserDevices: refusing to wipe an owner's devices");
    }
    const devices = await ctx.db
      .query("devices")
      .withIndex("by_userId", (q) => q.eq("userId", guestUserId))
      .collect();
    for (const d of devices) await ctx.db.delete(d._id);
    const machines = (await ctx.db.query("cloudMachines").collect()).filter(
      (m: any) => m.userId === guestUserId,
    );
    return {
      ok: true,
      removedDevices: devices.length,
      cloudMachinesRemaining: machines.length, // tear down via the destroy flow if you want them gone
    };
  },
});

// FULL PURGE (destructive, irreversible) — removes a user's Yaver identity
// + work so they can re-create a fresh account later. Friends-only,
// owner-authorized. Hard guards: refuses to purge an owner, requires
// confirm:true, and REFUSES if the user still has a cloud machine with a
// live Hetzner server (delete that via the real destroy flow first, or you
// orphan a paid box). Deletes: devices, infra grants (as guest AND host),
// wallet, gateway policy + tokens, included allowances, then the user row.
//   npx convex run betaAccess:purgeBetaUser '{"guestUserId":"<id>","confirm":true}'
export const purgeBetaUser = internalMutation({
  args: { guestUserId: v.id("users"), confirm: v.boolean() },
  handler: async (ctx, { guestUserId, confirm }) => {
    if (!confirm) throw new Error("purgeBetaUser: pass confirm:true (irreversible)");
    if (isOwnerUserId(String(guestUserId))) {
      throw new Error("purgeBetaUser: refusing to purge an owner");
    }
    // Guard: never orphan a billable box.
    const machines = (await ctx.db.query("cloudMachines").collect()).filter(
      (m: any) => m.userId === guestUserId,
    );
    const liveBox = machines.find((m: any) => m.hetznerServerId);
    if (liveBox) {
      throw new Error(
        `purgeBetaUser: user has ${machines.length} cloud machine(s), one with a live Hetzner server — destroy them via the cloud destroy flow first (avoids orphaned billing).`,
      );
    }

    const deleted: Record<string, number> = {};
    const wipe = async (rows: { _id: any }[], key: string) => {
      for (const r of rows) await ctx.db.delete(r._id);
      deleted[key] = rows.length;
    };

    await wipe(
      await ctx.db.query("devices").withIndex("by_userId", (q) => q.eq("userId", guestUserId)).collect(),
      "devices",
    );
    await wipe(machines, "cloudMachineRows");
    await wipe(
      await ctx.db.query("infraAccessGrants").withIndex("by_guestUserId", (q) => q.eq("guestUserId", guestUserId)).collect(),
      "grantsAsGuest",
    );
    await wipe(
      await ctx.db.query("infraAccessGrants").withIndex("by_hostUserId", (q) => q.eq("hostUserId", guestUserId)).collect(),
      "grantsAsHost",
    );
    await wipe(
      await ctx.db.query("prepaidCredits").withIndex("by_user", (q) => q.eq("userId", guestUserId)).collect(),
      "wallet",
    );
    await wipe(
      await ctx.db.query("gatewayPolicy").withIndex("by_user", (q) => q.eq("userId", guestUserId)).collect(),
      "gatewayPolicy",
    );
    await wipe(
      await ctx.db.query("gatewayTokens").withIndex("by_user", (q) => q.eq("userId", guestUserId)).collect(),
      "gatewayTokens",
    );
    await wipe(
      await ctx.db.query("includedAllowance").withIndex("by_user_period_type", (q) => q.eq("userId", guestUserId)).collect(),
      "includedAllowance",
    );

    await ctx.db.delete(guestUserId);
    deleted["userRow"] = 1;
    return { ok: true, purged: deleted };
  },
});

type RevokeResult = { ok: boolean; revokedGrants: number };

// One-call revoke. Closes the gateway tap, revokes the hidden grant, and
// kills any scoped ygw_ tokens. The wallet grant is left to drain.
export const revokeBetaUser = internalAction({
  args: { guestUserId: v.id("users"), hostUserId: v.optional(v.id("users")) },
  handler: async (ctx, args): Promise<RevokeResult> => {
    const host = resolveOwnerHost(args.hostUserId);
    await ctx.runMutation(internal.gatewayPolicy.setPolicyInternal, {
      userId: args.guestUserId,
      enabled: false,
      note: "beta access revoked",
      setBy: String(host),
    });
    const r = await ctx.runMutation(internal.betaAccess.revokeBetaGrant, {
      hostUserId: host,
      guestUserId: args.guestUserId,
    });
    await ctx.runMutation(internal.gatewayTokens.revokeAllForUserInternal, {
      userId: args.guestUserId,
    });
    return { ok: true, revokedGrants: r.revoked };
  },
});
