// taskPackages.ts — cross-user Task Package sharing (docs/yaver-task-packages.md).
//
// An owner authors a package and publishes its BOOKKEEPING here (never the
// collector spec / secrets / IPs — those stay on-device). The owner then SHARES
// it to a runner (often a different user) by email or link; the runner sees an
// informed-consent screen and ACCEPTS, which materializes a scoped
// infraAccessGrants row (origin "task-package") so the runner is recognized on
// the owner's channel. The runner's device then pulls the real spec from the
// owner box and pushes results back — Convex only ever holds counters.
//
// Two auth styles (mirroring support_link.ts): agent sync calls use resolveUser
// (ctx.auth); web/mobile user actions use a session-token hash.

import { mutation, query } from "./_generated/server";
import { v } from "convex/values";
import type { Id } from "./_generated/dataModel";
import { resolveUser } from "./agentSync";
import { validateSessionInternal } from "./auth";

async function userFromToken(ctx: any, tokenHash: string): Promise<Id<"users">> {
  const session = await validateSessionInternal(ctx, tokenHash);
  if (!session) throw new Error("Unauthorized");
  return session.user._id;
}

function genCode(): string {
  const chars = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"; // no 0/O/1/I
  const buf = new Uint8Array(8);
  crypto.getRandomValues(buf);
  return Array.from(buf).map((b) => chars[b % chars.length]).join("");
}

// --- agent sync: owner publishes package bookkeeping ------------------------

export const upsertPackage = mutation({
  args: {
    deviceId: v.string(),
    name: v.string(),
    version: v.number(),
    kind: v.string(),
    tier: v.string(),
    description: v.optional(v.string()),
    domains: v.array(v.string()),
    runtimes: v.array(v.string()),
    engines: v.array(v.string()),
    vantageGeo: v.array(v.string()),
    vantageResidential: v.boolean(),
    schedule: v.optional(v.string()),
    consentSummary: v.optional(v.string()),
    willNot: v.array(v.string()),
    dataShown: v.array(v.string()),
    status: v.string(),
  },
  handler: async (ctx, args) => {
    const ownerUserId = await resolveUser(ctx);
    const existing = await ctx.db
      .query("taskPackages")
      .withIndex("by_owner_name", (q: any) =>
        q.eq("ownerUserId", ownerUserId).eq("name", args.name),
      )
      .first();
    const now = Date.now();
    const row = { ...args, ownerUserId, updatedAt: now };
    if (existing) {
      await ctx.db.patch(existing._id, row);
      return { id: existing._id };
    }
    const id = await ctx.db.insert("taskPackages", row);
    return { id };
  },
});

// --- owner: list + share ----------------------------------------------------

export const myPackages = query({
  args: { tokenHash: v.string() },
  handler: async (ctx, args) => {
    const userId = await userFromToken(ctx, args.tokenHash);
    const pkgs = await ctx.db
      .query("taskPackages")
      .withIndex("by_owner", (q: any) => q.eq("ownerUserId", userId))
      .collect();
    return pkgs.map((p) => ({
      id: p._id, name: p.name, kind: p.kind, tier: p.tier, version: p.version,
      description: p.description ?? "", domains: p.domains, runtimes: p.runtimes,
      engines: p.engines, vantageGeo: p.vantageGeo, status: p.status,
    }));
  },
});

export const packageRunners = query({
  args: { tokenHash: v.string(), packageId: v.optional(v.id("taskPackages")) },
  handler: async (ctx, args) => {
    const userId = await userFromToken(ctx, args.tokenHash);
    let allocs = await ctx.db
      .query("packageAllocations")
      .withIndex("by_owner", (q: any) => q.eq("ownerUserId", userId))
      .collect();
    if (args.packageId) allocs = allocs.filter((a) => a.packageId === args.packageId);
    return allocs.map((a) => ({
      id: a._id, packageName: a.packageName, target: a.target, status: a.status,
      runnerEmail: a.runnerEmail ?? "", runnerDeviceId: a.runnerDeviceId ?? "",
      inviteCode: a.inviteCode, runCount: a.runCount, blockCount: a.blockCount,
      lastRunAt: a.lastRunAt ?? 0, lastStatus: a.lastStatus ?? "",
      lastCountry: a.lastCountry ?? "", consentAt: a.consentAt ?? 0,
    }));
  },
});

export const shareToRunner = mutation({
  args: {
    tokenHash: v.string(),
    packageId: v.id("taskPackages"),
    target: v.string(),
    runnerEmail: v.optional(v.string()),
    wifiOnly: v.optional(v.boolean()),
    chargingOnly: v.optional(v.boolean()),
    expiresAt: v.optional(v.number()),
  },
  handler: async (ctx, args) => {
    const ownerUserId = await userFromToken(ctx, args.tokenHash);
    const pkg = await ctx.db.get(args.packageId);
    if (!pkg || pkg.ownerUserId !== ownerUserId) throw new Error("package not found");
    let code = genCode();
    for (let i = 0; i < 5; i++) {
      const clash = await ctx.db
        .query("packageAllocations")
        .withIndex("by_code", (q: any) => q.eq("inviteCode", code))
        .first();
      if (!clash) break;
      code = genCode();
    }
    const now = Date.now();
    const id = await ctx.db.insert("packageAllocations", {
      packageId: args.packageId,
      packageName: pkg.name,
      ownerUserId,
      runnerEmail: args.runnerEmail,
      target: args.target || "mobile",
      status: "proposed" as const,
      inviteCode: code,
      consentSummary: pkg.consentSummary,
      wifiOnly: args.wifiOnly ?? true,
      chargingOnly: args.chargingOnly ?? false,
      expiresAt: args.expiresAt,
      runCount: 0,
      blockCount: 0,
      createdAt: now,
      updatedAt: now,
    });
    return { id, inviteCode: code };
  },
});

// --- runner: discover + accept ----------------------------------------------

export const allocationByCode = query({
  args: { code: v.string() },
  handler: async (ctx, args) => {
    const a = await ctx.db
      .query("packageAllocations")
      .withIndex("by_code", (q: any) => q.eq("inviteCode", args.code))
      .first();
    if (!a) return null;
    const pkg = await ctx.db.get(a.packageId);
    return {
      id: a._id, status: a.status, target: a.target,
      packageName: a.packageName,
      kind: pkg?.kind ?? "", tier: pkg?.tier ?? "",
      domains: pkg?.domains ?? [],
      schedule: pkg?.schedule ?? "",
      consentSummary: pkg?.consentSummary ?? a.consentSummary ?? "",
      willNot: pkg?.willNot ?? [],
      dataShown: pkg?.dataShown ?? [],
    };
  },
});

export const sharedWithMe = query({
  args: { tokenHash: v.string() },
  handler: async (ctx, args) => {
    const userId = await userFromToken(ctx, args.tokenHash);
    const mine = await ctx.db
      .query("packageAllocations")
      .withIndex("by_runner", (q: any) => q.eq("runnerUserId", userId))
      .collect();
    return mine.map((a) => ({
      id: a._id, packageName: a.packageName, target: a.target, status: a.status,
      consentSummary: a.consentSummary ?? "", inviteCode: a.inviteCode,
      wifiOnly: a.wifiOnly, chargingOnly: a.chargingOnly,
      runCount: a.runCount, lastRunAt: a.lastRunAt ?? 0, lastStatus: a.lastStatus ?? "",
    }));
  },
});

export const acceptAllocation = mutation({
  args: {
    tokenHash: v.string(),
    code: v.string(),
    deviceId: v.string(),
    wifiOnly: v.optional(v.boolean()),
    chargingOnly: v.optional(v.boolean()),
  },
  handler: async (ctx, args) => {
    const runnerUserId = await userFromToken(ctx, args.tokenHash);
    const a = await ctx.db
      .query("packageAllocations")
      .withIndex("by_code", (q: any) => q.eq("inviteCode", args.code))
      .first();
    if (!a) throw new Error("allocation not found");
    if (a.status === "revoked") throw new Error("allocation revoked");
    if (a.ownerUserId === runnerUserId) throw new Error("cannot accept your own package");

    const now = Date.now();
    // Scoped owner→runner grant (origin task-package): marks the channel; the
    // runner pulls the real spec + pushes results to the owner box under it.
    const grantId = await ctx.db.insert("infraAccessGrants", {
      hostUserId: a.ownerUserId,
      guestUserId: runnerUserId,
      status: "active" as const,
      shareAllDevices: false,
      shareAllMachines: false,
      allowDesktopControl: false,
      allowBrowserControl: false,
      allowTunnelForward: false,
      useHostApiKeys: false,
      allowGuestProvidedApiKeys: false,
      resourcePreset: "task-package",
      grantedAt: now,
      updatedAt: now,
      expiresAt: a.expiresAt,
      origin: "task-package",
    });

    await ctx.db.patch(a._id, {
      runnerUserId,
      runnerDeviceId: args.deviceId,
      status: "active" as const,
      consentAt: now,
      wifiOnly: args.wifiOnly ?? a.wifiOnly,
      chargingOnly: args.chargingOnly ?? a.chargingOnly,
      grantId,
      updatedAt: now,
    });
    return { ok: true, allocationId: a._id };
  },
});

// --- status changes (owner or runner) + run counters (agent) ----------------

export const setAllocationStatus = mutation({
  args: {
    tokenHash: v.string(),
    allocationId: v.id("packageAllocations"),
    status: v.union(v.literal("active"), v.literal("paused"), v.literal("revoked")),
  },
  handler: async (ctx, args) => {
    const userId = await userFromToken(ctx, args.tokenHash);
    const a = await ctx.db.get(args.allocationId);
    if (!a) throw new Error("allocation not found");
    if (a.ownerUserId !== userId && a.runnerUserId !== userId) {
      throw new Error("not your allocation");
    }
    await ctx.db.patch(a._id, { status: args.status, updatedAt: Date.now() });
    if (args.status === "revoked" && a.grantId) {
      const g = await ctx.db.get(a.grantId);
      if (g) await ctx.db.patch(a.grantId, { status: "revoked", revokedAt: Date.now() });
    }
    return { ok: true };
  },
});

export const recordAllocationRun = mutation({
  args: {
    allocationId: v.id("packageAllocations"),
    status: v.string(),
    country: v.optional(v.string()),
    blocked: v.optional(v.boolean()),
  },
  handler: async (ctx, args) => {
    await resolveUser(ctx); // authenticated agent only
    const a = await ctx.db.get(args.allocationId);
    if (!a) throw new Error("allocation not found");
    await ctx.db.patch(a._id, {
      lastRunAt: Date.now(),
      runCount: a.runCount + 1,
      blockCount: a.blockCount + (args.blocked ? 1 : 0),
      lastStatus: args.status,
      lastCountry: args.country ?? a.lastCountry,
      updatedAt: Date.now(),
    });
    return { ok: true };
  },
});
