// taskPlacement.ts — privacy-safe machine placement policy.
//
// This module is the central decision layer for "where should this task run?"
// across phone sandbox, Yaver Relay source runner, owned machines, and managed
// cloud. It stores only coarse labels/counters/reasons in Convex. Task prompts,
// stdout, repo paths, files, package names, and secrets stay on the user's
// devices.

import { v } from "convex/values";
import { internalMutation, internalQuery, mutation, query } from "./_generated/server";
import type { Id } from "./_generated/dataModel";
import { validateSessionInternal } from "./auth";
import { estimatedHourlyCents, includedHoursForPlan } from "./cloudLifecycle";
import { isOwner } from "./ownerAllowlist";
import {
  classifyProjectForPlacement,
  strongestResourceClass,
  type PlacementResourceClass,
  type PlacementTaskKind,
} from "./taskPlacementClassifier";

const lanes = v.union(
  v.literal("phone_sandbox"),
  v.literal("relay_source"),
  v.literal("owned_machine"),
  v.literal("cloud_standard"),
  v.literal("cloud_heavy"),
  v.literal("cloud_build"),
  v.literal("external_deploy"),
  v.literal("manual"),
);

const resourceClasses = v.union(
  v.literal("phone"),
  v.literal("relay-source"),
  v.literal("standard"),
  v.literal("heavy"),
  v.literal("build"),
);

const taskKinds = v.union(
  v.literal("vibe"),
  v.literal("build"),
  v.literal("deploy"),
  v.literal("test"),
  v.literal("source"),
  v.literal("autorun"),
  v.literal("unknown"),
);

type Lane =
  | "phone_sandbox"
  | "relay_source"
  | "owned_machine"
  | "cloud_standard"
  | "cloud_heavy"
  | "cloud_build"
  | "external_deploy"
  | "manual";

type ResourceClass = PlacementResourceClass;
type TaskKind = PlacementTaskKind;
type BillingScope = "none" | "relay-included" | "cloud-included-then-metered" | "external";

type CreditEstimate = {
  unit: "usd_cents";
  estimatedCents: number;
  hourlyCents: number;
  estimatedMinutes: number;
  includedHoursBucket?: number;
  billingScope: BillingScope;
  resourceClass: ResourceClass;
  display: string;
};

function isCloudWorkspacePlan(plan: string | undefined): boolean {
  if (!plan) return false;
  return plan === "cloud-workspace" || plan === "cloud-agent" || plan.startsWith("yaver-cloud");
}

function isRelayProPlan(plan: string | undefined): boolean {
  if (!plan) return false;
  return plan === "relay-pro" || plan === "relay-monthly" || plan === "relay-yearly" || plan === "managed-relay";
}

type Profile = {
  resourceClass: ResourceClass;
  stack?: string;
  hasNativeMobile?: boolean;
  hasDocker?: boolean;
  appCount?: number;
  repoSizeMb?: number;
  fileCount?: number;
};

type PlacementDecision = {
  lane: Lane;
  resourceClass: ResourceClass;
  targetDeviceId?: string;
  cloudMachineId?: Id<"cloudMachines">;
  subscriptionPlan?: string;
  entitlement: "free" | "relay-pro" | "cloud-workspace" | "owner-dev";
  status: "planned";
  reason: string;
  wakeRequired: boolean;
  wakeTargetMs?: number;
  estimatedCreditCost?: number;
  creditEstimate: CreditEstimate;
};

function estimateMinutes(kind: TaskKind, resourceClass: ResourceClass): number {
  if (kind === "deploy" || kind === "build" || resourceClass === "build") return 60;
  if (resourceClass === "heavy") return 45;
  if (kind === "test" || kind === "autorun" || resourceClass === "standard") return 30;
  return 10;
}

function billingScopeForLane(lane: Lane): BillingScope {
  if (lane === "relay_source") return "relay-included";
  if (lane.startsWith("cloud_")) return "cloud-included-then-metered";
  if (lane === "external_deploy") return "external";
  return "none";
}

function machineTypeForResource(resourceClass: ResourceClass): "standard" | "heavy" | "build" {
  if (resourceClass === "build") return "build";
  if (resourceClass === "heavy") return "heavy";
  return "standard";
}

function creditEstimateFor(args: {
  kind: TaskKind;
  lane: Lane;
  resourceClass: ResourceClass;
  plan?: string;
  estimatedCents?: number | null;
}): CreditEstimate {
  const scope = billingScopeForLane(args.lane);
  const minutes = estimateMinutes(args.kind, args.resourceClass);
  const machineType = machineTypeForResource(args.resourceClass);
  const hourlyCents = scope === "cloud-included-then-metered"
    ? estimatedHourlyCents(machineType)
    : 0;
  const estimatedCents = typeof args.estimatedCents === "number" && Number.isFinite(args.estimatedCents)
    ? Math.max(0, Math.round(args.estimatedCents))
    : Math.ceil((hourlyCents * minutes) / 60);
  const includedHoursBucket = scope === "cloud-included-then-metered" && args.plan
    ? includedHoursForPlan(args.plan, machineType)
    : undefined;
  const display = scope === "cloud-included-then-metered"
    ? `Included hours first, then ~$${(estimatedCents / 100).toFixed(2)} for ~${minutes}m`
    : scope === "relay-included"
      ? "Included in Relay"
      : scope === "external"
        ? "External provider billing"
        : "No Yaver compute charge";
  return {
    unit: "usd_cents",
    estimatedCents,
    hourlyCents,
    estimatedMinutes: minutes,
    includedHoursBucket,
    billingScope: scope,
    resourceClass: args.resourceClass,
    display,
  };
}

function withCreditEstimate<T extends Omit<PlacementDecision, "creditEstimate">>(
  decision: T,
  kind: TaskKind,
): T & { creditEstimate: CreditEstimate } {
  const creditEstimate = creditEstimateFor({
    kind,
    lane: decision.lane,
    resourceClass: decision.resourceClass,
    plan: decision.subscriptionPlan,
    estimatedCents: decision.estimatedCreditCost,
  });
  return {
    ...decision,
    estimatedCreditCost: creditEstimate.estimatedCents,
    creditEstimate,
  };
}

async function userFromToken(ctx: any, tokenHash: string): Promise<Id<"users">> {
  const session = await validateSessionInternal(ctx, tokenHash);
  if (!session) throw new Error("Unauthorized");
  return session.user._id;
}

function normalizeProjectSlug(slug: string | undefined): string | undefined {
  const s = (slug ?? "").trim();
  if (!s) return undefined;
  // Basename-ish slugs only. Do not allow paths to sneak into Convex.
  const leaf = s.split(/[\\/]/).filter(Boolean).pop() ?? "";
  return leaf.slice(0, 80) || undefined;
}

function normalizeStackLabel(stack: string | undefined): string | undefined {
  const s = (stack ?? "").trim().toLowerCase();
  if (!s || /[\\/]/.test(s)) return undefined;
  return s.replace(/[^a-z0-9._+ -]/g, "").slice(0, 80) || undefined;
}

function clampMetric(value: number | undefined, max: number): number | undefined {
  if (typeof value !== "number" || !Number.isFinite(value)) return undefined;
  return Math.max(0, Math.min(max, Math.round(value)));
}

async function activeSubscription(ctx: any, userId: Id<"users">) {
  const rows = await ctx.db
    .query("subscriptions")
    .withIndex("by_user", (q: any) => q.eq("userId", userId))
    .collect();
  return rows
    .filter((s: any) => s.status === "active" || s.status === "past_due")
    .sort((a: any, b: any) => (b.updatedAt ?? b.createdAt ?? 0) - (a.updatedAt ?? a.createdAt ?? 0))[0];
}

async function isOwnerDev(ctx: any, userId: Id<"users">): Promise<boolean> {
  const user = await ctx.db.get(userId);
  return !!user && isOwner((user as any).email, String(userId));
}

async function latestProfile(
  ctx: any,
  userId: Id<"users">,
  projectSlug: string | undefined,
): Promise<Profile | null> {
  if (!projectSlug) return null;
  const p = await ctx.db
    .query("projectProfiles")
    .withIndex("by_user_slug", (q: any) => q.eq("userId", userId).eq("projectSlug", projectSlug))
    .first();
  if (!p) return null;
  return {
    resourceClass: p.resourceClass,
    stack: p.stack,
    hasNativeMobile: p.hasNativeMobile,
    hasDocker: p.hasDocker,
    appCount: p.appCount,
    repoSizeMb: p.repoSizeMb,
    fileCount: p.fileCount,
  };
}

async function candidateOwnedDevice(
  ctx: any,
  userId: Id<"users">,
  args: { targetDeviceId?: string; runnerId?: string; needsBuild: boolean },
) {
  const devices = await ctx.db
    .query("devices")
    .withIndex("by_userId", (q: any) => q.eq("userId", userId))
    .collect();
  const online = devices.filter((d: any) => d.isOnline && !d.needsAuth);
  const selected = args.targetDeviceId
    ? online.find((d: any) => d.deviceId === args.targetDeviceId)
    : undefined;
  const pool = selected ? [selected] : online;
  return pool.find((d: any) => {
    if (args.runnerId && Array.isArray(d.installedRunnerIds) && !d.installedRunnerIds.includes(args.runnerId)) {
      return false;
    }
    if (!args.needsBuild) return true;
    return Array.isArray(d.publishCapabilities) && d.publishCapabilities.length > 0;
  }) ?? null;
}

async function candidateCloudMachine(ctx: any, userId: Id<"users">, resourceClass: ResourceClass) {
  const machines = await ctx.db
    .query("cloudMachines")
    .withIndex("by_user", (q: any) => q.eq("userId", userId))
    .collect();
  const activeish = machines
    .filter((m: any) => ["active", "paused", "stopped", "resuming", "provisioning"].includes(m.status))
    .sort((a: any, b: any) => (b.updatedAt ?? b.createdAt ?? 0) - (a.updatedAt ?? a.createdAt ?? 0));
  if (activeish.length === 0) return null;
  if (resourceClass === "build") {
    return activeish.find((m: any) => (m.specs?.ramGb ?? 0) >= 24) ?? activeish[0];
  }
  if (resourceClass === "heavy") {
    return activeish.find((m: any) => (m.specs?.ramGb ?? 0) >= 16) ?? activeish[0];
  }
  return activeish[0];
}

async function decidePlacement(
  ctx: any,
  userId: Id<"users">,
  args: {
    kind: TaskKind;
    projectSlug?: string;
    requestedRunner?: string;
    targetDeviceId?: string;
    forceCloud?: boolean;
    forceRelaySource?: boolean;
    sourceSurface?: string;
    appCount?: number;
    repoSizeMb?: number;
    fileCount?: number;
    hasNativeMobile?: boolean;
    hasDocker?: boolean;
  },
): Promise<PlacementDecision> {
  const sub = await activeSubscription(ctx, userId);
  const plan = sub?.plan as string | undefined;
  const ownerDev = await isOwnerDev(ctx, userId);
  const hasCloudWorkspace = sub?.status === "active" && isCloudWorkspacePlan(plan);
  const hasRelayPro = (sub?.status === "active" || sub?.status === "past_due") && isRelayProPlan(plan);
  const entitlement = ownerDev
    ? "owner-dev"
    : hasCloudWorkspace
    ? "cloud-workspace"
    : hasRelayPro
      ? "relay-pro"
      : "free";
  const profile = await latestProfile(ctx, userId, args.projectSlug);
  const hinted = classifyProjectForPlacement(args).resourceClass;
  const resourceClass = profile
    ? strongestResourceClass(profile.resourceClass, classifyProjectForPlacement({
        kind: args.kind,
        projectSlug: args.projectSlug,
        stack: profile.stack,
        appCount: profile.appCount ?? args.appCount,
        repoSizeMb: profile.repoSizeMb ?? args.repoSizeMb,
        fileCount: profile.fileCount ?? args.fileCount,
        hasNativeMobile: profile.hasNativeMobile ?? args.hasNativeMobile,
        hasDocker: profile.hasDocker ?? args.hasDocker,
      }).resourceClass)
    : hinted;
  const needsBuild = args.kind === "build" || args.kind === "deploy" || resourceClass === "build";

  if (args.forceRelaySource) {
    return withCreditEstimate({
      lane: "relay_source",
      resourceClass: "relay-source",
      entitlement,
      status: "planned",
      reason: "relay source mode requested; suitable for git/source-only vibing while compute wakes",
      wakeRequired: false,
      estimatedCreditCost: 0,
    }, args.kind);
  }

  const owned = await candidateOwnedDevice(ctx, userId, {
    targetDeviceId: args.targetDeviceId,
    runnerId: args.requestedRunner,
    needsBuild,
  });
  if (owned && !args.forceCloud) {
    return withCreditEstimate({
      lane: "owned_machine",
      resourceClass,
      targetDeviceId: owned.deviceId,
      entitlement,
      status: "planned",
      reason: needsBuild
        ? "owned online machine has build/publish capability"
        : "owned online machine can run the requested runner",
      wakeRequired: false,
      estimatedCreditCost: 0,
    }, args.kind);
  }

  if (!needsBuild && (resourceClass === "relay-source" || args.kind === "vibe" || args.kind === "source")) {
    return withCreditEstimate({
      lane: "relay_source",
      resourceClass: "relay-source",
      entitlement,
      status: "planned",
      reason: "source-only task can start on relay while a full workspace is optional",
      wakeRequired: false,
      estimatedCreditCost: 0,
    }, args.kind);
  }

  if ((hasCloudWorkspace || ownerDev) && (args.forceCloud || needsBuild || resourceClass !== "relay-source")) {
    const cloud = await candidateCloudMachine(ctx, userId, resourceClass);
    const lane: Lane = resourceClass === "build"
      ? "cloud_build"
      : resourceClass === "heavy"
        ? "cloud_heavy"
        : "cloud_standard";
    return withCreditEstimate({
      lane,
      resourceClass: resourceClass === "relay-source" ? "standard" : resourceClass,
      cloudMachineId: cloud?._id,
      targetDeviceId: cloud?.deviceId,
      subscriptionPlan: plan,
      entitlement: ownerDev ? "owner-dev" : hasCloudWorkspace ? "cloud-workspace" : entitlement,
      status: "planned",
      reason: cloud
        ? "cloud workspace selected for real build/runtime capacity"
        : "cloud workspace entitlement selected; machine should be provisioned or woken",
      wakeRequired: !cloud || cloud.status !== "active",
      wakeTargetMs: 60_000,
    }, args.kind);
  }

  return withCreditEstimate({
    lane: "manual",
    resourceClass,
    entitlement,
    status: "planned",
    reason: args.forceCloud && !hasCloudWorkspace && !ownerDev
      ? "Cloud Workspace was requested but this account does not have a Cloud Workspace subscription"
      : needsBuild
        ? "build/deploy needs an owned machine or Cloud Workspace"
      : "no suitable online owned machine, relay lane, or Cloud Workspace entitlement found",
    wakeRequired: false,
  }, args.kind);
}

export const upsertProjectProfile = mutation({
  args: {
    tokenHash: v.string(),
    projectSlug: v.string(),
    sourceDeviceId: v.optional(v.string()),
    stack: v.optional(v.string()),
    appCount: v.optional(v.number()),
    repoSizeMb: v.optional(v.number()),
    fileCount: v.optional(v.number()),
    hasNativeMobile: v.optional(v.boolean()),
    hasDocker: v.optional(v.boolean()),
    resourceClass: v.optional(resourceClasses),
    confidence: v.optional(v.number()),
  },
  handler: async (ctx, args) => {
    const userId = await userFromToken(ctx, args.tokenHash);
    const projectSlug = normalizeProjectSlug(args.projectSlug);
    if (!projectSlug) throw new Error("projectSlug required");
    const now = Date.now();
    const appCount = clampMetric(args.appCount, 100);
    const repoSizeMb = clampMetric(args.repoSizeMb, 1_000_000);
    const fileCount = clampMetric(args.fileCount, 10_000_000);
    const stack = normalizeStackLabel(args.stack);
    const resourceClass = args.resourceClass ?? classifyProjectForPlacement({
      kind: "unknown",
      projectSlug,
      stack,
      appCount,
      repoSizeMb,
      fileCount,
      hasNativeMobile: args.hasNativeMobile,
      hasDocker: args.hasDocker,
    }).resourceClass;
    const existing = await ctx.db
      .query("projectProfiles")
      .withIndex("by_user_slug", (q: any) => q.eq("userId", userId).eq("projectSlug", projectSlug))
      .first();
    const row = {
      userId,
      projectSlug,
      sourceDeviceId: args.sourceDeviceId?.slice(0, 160),
      stack,
      appCount,
      repoSizeMb,
      fileCount,
      hasNativeMobile: args.hasNativeMobile,
      hasDocker: args.hasDocker,
      resourceClass,
      confidence: typeof args.confidence === "number" && Number.isFinite(args.confidence)
        ? Math.max(0, Math.min(1, args.confidence))
        : undefined,
      updatedAt: now,
    };
    if (existing) {
      await ctx.db.patch(existing._id, row);
      return { id: existing._id, resourceClass };
    }
    const id = await ctx.db.insert("projectProfiles", row);
    return { id, resourceClass };
  },
});

export const preview = query({
  args: {
    tokenHash: v.string(),
    kind: taskKinds,
    projectSlug: v.optional(v.string()),
    requestedRunner: v.optional(v.string()),
    targetDeviceId: v.optional(v.string()),
    forceCloud: v.optional(v.boolean()),
    forceRelaySource: v.optional(v.boolean()),
    sourceSurface: v.optional(v.string()),
    appCount: v.optional(v.number()),
    repoSizeMb: v.optional(v.number()),
    fileCount: v.optional(v.number()),
    hasNativeMobile: v.optional(v.boolean()),
    hasDocker: v.optional(v.boolean()),
  },
  handler: async (ctx, args) => {
    const userId = await userFromToken(ctx, args.tokenHash);
    return await decidePlacement(ctx, userId, {
      ...args,
      projectSlug: normalizeProjectSlug(args.projectSlug),
    });
  },
});

export const record = mutation({
  args: {
    tokenHash: v.string(),
    taskId: v.string(),
    kind: taskKinds,
    sourceSurface: v.optional(v.string()),
    projectSlug: v.optional(v.string()),
    requestedRunner: v.optional(v.string()),
    targetDeviceId: v.optional(v.string()),
    forceCloud: v.optional(v.boolean()),
    forceRelaySource: v.optional(v.boolean()),
    appCount: v.optional(v.number()),
    repoSizeMb: v.optional(v.number()),
    fileCount: v.optional(v.number()),
    hasNativeMobile: v.optional(v.boolean()),
    hasDocker: v.optional(v.boolean()),
  },
  handler: async (ctx, args) => {
    const userId = await userFromToken(ctx, args.tokenHash);
    const projectSlug = normalizeProjectSlug(args.projectSlug);
    const decision = await decidePlacement(ctx, userId, { ...args, projectSlug });
    const now = Date.now();
    const id = await ctx.db.insert("taskPlacements", {
      userId,
      taskId: args.taskId,
      sourceSurface: args.sourceSurface,
      projectSlug,
      requestedRunner: args.requestedRunner,
      kind: args.kind,
      lane: decision.lane,
      resourceClass: decision.resourceClass,
      targetDeviceId: decision.targetDeviceId,
      cloudMachineId: decision.cloudMachineId,
      subscriptionPlan: decision.subscriptionPlan,
      entitlement: decision.entitlement,
      status: decision.status,
      reason: decision.reason,
      wakeRequired: decision.wakeRequired,
      wakeTargetMs: decision.wakeTargetMs,
      estimatedCreditCost: decision.estimatedCreditCost,
      createdAt: now,
      updatedAt: now,
    });
    return { id, ...decision };
  },
});

export const listRecent = query({
  args: {
    tokenHash: v.string(),
    projectSlug: v.optional(v.string()),
    limit: v.optional(v.number()),
  },
  handler: async (ctx, args) => {
    const userId = await userFromToken(ctx, args.tokenHash);
    const n = Math.max(1, Math.min(100, args.limit ?? 25));
    const projectSlug = normalizeProjectSlug(args.projectSlug);
    const rows = projectSlug
      ? await ctx.db
          .query("taskPlacements")
          .withIndex("by_project", (q: any) => q.eq("userId", userId).eq("projectSlug", projectSlug))
          .order("desc")
          .take(n)
      : await ctx.db
          .query("taskPlacements")
          .withIndex("by_user_created", (q: any) => q.eq("userId", userId))
          .order("desc")
          .take(n);
    return rows.map((r: any) => ({
      id: r._id,
      taskId: r.taskId,
      sourceSurface: r.sourceSurface ?? null,
      projectSlug: r.projectSlug ?? null,
      kind: r.kind,
      lane: r.lane,
      resourceClass: r.resourceClass,
      targetDeviceId: r.targetDeviceId ?? null,
      cloudMachineId: r.cloudMachineId ? String(r.cloudMachineId) : null,
      subscriptionPlan: r.subscriptionPlan ?? null,
      entitlement: r.entitlement ?? null,
      status: r.status,
      reason: r.reason,
      wakeRequired: r.wakeRequired,
      wakeTargetMs: r.wakeTargetMs ?? null,
      estimatedCreditCost: r.estimatedCreditCost ?? null,
      creditEstimate: creditEstimateFor({
        kind: (r.kind ?? "unknown") as TaskKind,
        lane: r.lane as Lane,
        resourceClass: r.resourceClass as ResourceClass,
        plan: r.subscriptionPlan,
        estimatedCents: r.estimatedCreditCost,
      }),
      createdAt: r.createdAt,
      updatedAt: r.updatedAt,
    }));
  },
});

export const getForActivation = internalQuery({
  args: {
    userId: v.id("users"),
    placementId: v.optional(v.id("taskPlacements")),
    taskId: v.optional(v.string()),
  },
  handler: async (ctx, args) => {
    const row = args.placementId
      ? await ctx.db.get(args.placementId)
      : args.taskId
        ? (await ctx.db
            .query("taskPlacements")
            .withIndex("by_task", (q: any) => q.eq("taskId", args.taskId!))
            .collect())
            .find((candidate: any) => String(candidate.userId) === String(args.userId))
        : null;
    if (!row || String(row.userId) !== String(args.userId)) return null;
    return row;
  },
});

export const attachCloudMachine = internalMutation({
  args: {
    userId: v.id("users"),
    placementId: v.id("taskPlacements"),
    cloudMachineId: v.id("cloudMachines"),
    targetDeviceId: v.optional(v.string()),
    status: v.optional(v.union(
      v.literal("queued"),
      v.literal("running"),
    )),
  },
  handler: async (ctx, args) => {
    const row = await ctx.db.get(args.placementId);
    if (!row || String(row.userId) !== String(args.userId)) throw new Error("placement not found");
    await ctx.db.patch(args.placementId, {
      cloudMachineId: args.cloudMachineId,
      targetDeviceId: args.targetDeviceId,
      status: args.status ?? row.status,
      updatedAt: Date.now(),
    });
    return { ok: true };
  },
});

export const markStatus = mutation({
  args: {
    tokenHash: v.string(),
    placementId: v.id("taskPlacements"),
    status: v.union(
      v.literal("queued"),
      v.literal("running"),
      v.literal("completed"),
      v.literal("failed"),
      v.literal("superseded"),
    ),
  },
  handler: async (ctx, args) => {
    const userId = await userFromToken(ctx, args.tokenHash);
    const row = await ctx.db.get(args.placementId);
    if (!row || row.userId !== userId) throw new Error("placement not found");
    await ctx.db.patch(args.placementId, { status: args.status, updatedAt: Date.now() });
    return { ok: true };
  },
});

export const rebindTask = mutation({
  args: {
    tokenHash: v.string(),
    placementId: v.id("taskPlacements"),
    taskId: v.string(),
    status: v.optional(v.union(
      v.literal("queued"),
      v.literal("running"),
      v.literal("completed"),
      v.literal("failed"),
      v.literal("superseded"),
    )),
  },
  handler: async (ctx, args) => {
    const userId = await userFromToken(ctx, args.tokenHash);
    const row = await ctx.db.get(args.placementId);
    if (!row || row.userId !== userId) throw new Error("placement not found");
    const taskId = args.taskId.trim();
    if (!taskId) throw new Error("taskId required");
    if (taskId.length > 200) throw new Error("taskId too long");
    const now = Date.now();
    await ctx.db.patch(args.placementId, {
      taskId,
      status: args.status ?? row.status,
      updatedAt: now,
    });
    const wakeRuns = await ctx.db
      .query("wakeRuns")
      .withIndex("by_placement", (q: any) => q.eq("placementId", args.placementId))
      .collect();
    await Promise.all(
      wakeRuns
        .filter((run: any) => String(run.userId) === String(userId))
        .map((run: any) => ctx.db.patch(run._id, { taskId, updatedAt: now })),
    );
    return { ok: true };
  },
});
