// relaySourceIntents.ts — prompt-free, branch-scoped source work for relay.
//
// This is the control-plane ledger for "relay can do cheap source prep while
// compute wakes". It deliberately stores only ids, normalized repo labels,
// yaver/* branch names, coarse status/reason text, and counters. Prompts,
// diffs, file paths, stdout, secrets, vault references, provider tokens, and
// runner OAuth never belong here.

import { v } from "convex/values";
import { mutation, query } from "./_generated/server";
import type { Id } from "./_generated/dataModel";
import { validateSessionInternal } from "./auth";

const statuses = v.union(
  v.literal("queued"),
  v.literal("claimed"),
  v.literal("committed"),
  v.literal("handoff_ready"),
  v.literal("blocked"),
  v.literal("failed"),
  v.literal("cancelled"),
  v.literal("expired"),
);

type RelaySourceStatus =
  | "queued"
  | "claimed"
  | "committed"
  | "handoff_ready"
  | "blocked"
  | "failed"
  | "cancelled"
  | "expired";

async function userFromToken(ctx: any, tokenHash: string): Promise<Id<"users">> {
  const session = await validateSessionInternal(ctx, tokenHash);
  if (!session) throw new Error("Unauthorized");
  return session.user._id;
}

function trimLabel(value: string | undefined, max: number): string | undefined {
  const text = String(value || "").trim();
  return text ? text.slice(0, max) : undefined;
}

export function relaySourceSlug(value: string | undefined, fallback = "task"): string {
  const s = String(value || "")
    .trim()
    .toLowerCase()
    .replace(/[^a-z0-9._/-]+/g, "-")
    .replace(/\.\.+/g, ".")
    .replace(/(^|\/)\.+(?=\/|$)/g, "/")
    .replace(/\/+/g, "/")
    .replace(/^-+|-+$/g, "")
    .replace(/\/-+|-+\//g, "/");
  const leaf = s.split("/").filter(Boolean).join("/");
  return (leaf || fallback).slice(0, 96);
}

export function normalizeRelayBranch(branch: string | undefined, fallbackSeed: string): string {
  const raw = relaySourceSlug(branch, "");
  const branchName = raw.startsWith("yaver/") ? raw : `yaver/source/${relaySourceSlug(fallbackSeed)}`;
  const cleaned = branchName
    .replace(/\.\.+/g, ".")
    .replace(/\/\.+/g, "/")
    .replace(/\.+\//g, "/")
    .replace(/\/+/g, "/")
    .replace(/\.lock$/i, "");
  if (!cleaned.startsWith("yaver/")) return `yaver/source/${relaySourceSlug(fallbackSeed)}`;
  if (cleaned === "yaver/main" || cleaned === "yaver/master") return `yaver/source/${relaySourceSlug(fallbackSeed)}`;
  return cleaned.slice(0, 120);
}

function normalizeProjectSlug(slug: string | undefined): string | undefined {
  const s = String(slug || "").trim();
  if (!s || /[\\/]/.test(s)) return undefined;
  return s.slice(0, 80);
}

function safeProviderLabel(value: string | undefined, max = 120): string | undefined {
  const text = String(value || "").trim();
  if (!text || /[\s\x00]/.test(text)) return undefined;
  if (/token|password|secret|bearer|oauth|sk-/i.test(text)) return undefined;
  return text.slice(0, max);
}

export function parseRelaySourceProviderTarget(repoUrl: string | undefined, branch: string) {
  const raw = String(repoUrl || "").trim();
  if (!raw) return {};
  let host = "";
  let path = "";
  const scp = raw.match(/^(?:[^@\s]+@)?([^:\s/]+):(.+)$/);
  if (scp && !raw.includes("://")) {
    host = scp[1] || "";
    path = scp[2] || "";
  } else {
    try {
      const parsed = new URL(raw.includes("://") ? raw : `https://${raw}`);
      if (parsed.username || parsed.password) return {};
      host = parsed.hostname;
      path = parsed.pathname.replace(/^\/+/, "");
    } catch {
      return {};
    }
  }
  host = safeProviderLabel(host, 120) || "";
  path = path.replace(/\.git$/i, "").replace(/^\/+|\/+$/g, "");
  const repo = safeProviderLabel(path, 180);
  if (!host || !repo || !repo.includes("/")) return {};
  const providerKind = host.includes("github") ? "github" : host.includes("gitlab") ? "gitlab" : undefined;
  if (!providerKind) return {};
  const providerBranch = normalizeRelayBranch(branch, repo);
  const encodedBranch = providerBranch.split("/").map(encodeURIComponent).join("/");
  const providerBranchUrl =
    providerKind === "github"
      ? `https://${host}/${repo}/tree/${encodedBranch}`
      : `https://${host}/${repo}/-/tree/${encodedBranch}`;
  return {
    providerKind,
    providerHost: host,
    providerRepo: repo,
    providerBranch,
    providerBranchUrl,
    providerAuthMode: "none",
    providerAuthStatus: "required",
  };
}

async function resolveShareForUser(
  ctx: any,
  userId: Id<"users">,
  args: { shareId?: Id<"projectShares">; projectSlug?: string },
) {
  let share = args.shareId ? await ctx.db.get(args.shareId) : null;
  const projectSlug = normalizeProjectSlug(args.projectSlug);
  if (!share && projectSlug) {
    share = await ctx.db
      .query("projectShares")
      .withIndex("by_owner", (q: any) => q.eq("ownerUserId", userId))
      .filter((q: any) => q.eq(q.field("slug"), projectSlug))
      .filter((q: any) => q.eq(q.field("status"), "active"))
      .first();
    if (!share) {
      const memberships = await ctx.db
        .query("projectMemberships")
        .withIndex("by_user", (q: any) => q.eq("userId", userId))
        .collect();
      for (const membership of memberships) {
        if (membership.status !== "active") continue;
        const candidate = await ctx.db.get(membership.shareId);
        if (candidate?.status === "active" && candidate.slug === projectSlug) {
          share = candidate;
          break;
        }
      }
    }
  }
  if (!share || share.status !== "active") throw new Error("project share not found");

  const owner = String(share.ownerUserId) === String(userId);
  let membership = null;
  if (!owner) {
    membership = await ctx.db
      .query("projectMemberships")
      .withIndex("by_share_user", (q: any) => q.eq("shareId", share._id).eq("userId", userId))
      .first();
    if (!membership || membership.status !== "active") throw new Error("project membership not active");
    if (membership.role === "viewer") throw new Error("viewer role cannot create relay source work");
  }
  return { share, membership };
}

function serialize(row: any) {
  return {
    id: row._id,
    localTaskId: row.localTaskId,
    taskId: row.taskId ?? null,
    placementId: row.placementId ?? null,
    shareId: row.shareId,
    membershipId: row.membershipId ?? null,
    sourceSurface: row.sourceSurface ?? null,
    projectSlug: row.projectSlug,
    repoUrl: row.repoUrl,
    baseBranch: row.baseBranch,
    branch: row.branch,
    providerKind: row.providerKind ?? null,
    providerHost: row.providerHost ?? null,
    providerRepo: row.providerRepo ?? null,
    providerBranch: row.providerBranch ?? null,
    providerBranchUrl: row.providerBranchUrl ?? null,
    providerAppInstallationId: row.providerAppInstallationId ?? null,
    providerAuthMode: row.providerAuthMode ?? null,
    providerAuthStatus: row.providerAuthStatus ?? null,
    kind: row.kind,
    status: row.status,
    reason: row.reason ?? null,
    lastError: row.lastError ?? null,
    relayId: row.relayId ?? null,
    attempts: row.attempts,
    expiresAt: row.expiresAt,
    createdAt: row.createdAt,
    updatedAt: row.updatedAt,
    completedAt: row.completedAt ?? null,
  };
}

function canUpdateRelaySourceIntent(row: any, userId: Id<"users">): boolean {
  return String(row.userId) === String(userId) || String(row.ownerUserId) === String(userId);
}

export const create = mutation({
  args: {
    tokenHash: v.string(),
    localTaskId: v.string(),
    placementId: v.optional(v.id("taskPlacements")),
    shareId: v.optional(v.id("projectShares")),
    projectSlug: v.optional(v.string()),
    sourceSurface: v.optional(v.string()),
    kind: v.optional(v.string()),
    branch: v.optional(v.string()),
    reason: v.optional(v.string()),
    ttlMs: v.optional(v.number()),
  },
  handler: async (ctx, args) => {
    const userId = await userFromToken(ctx, args.tokenHash);
    const localTaskId = String(args.localTaskId || "").trim();
    if (!localTaskId) throw new Error("localTaskId required");
    if (localTaskId.length > 160) throw new Error("localTaskId too long");
    if (args.placementId) {
      const placement = await ctx.db.get(args.placementId);
      if (!placement || String(placement.userId) !== String(userId)) throw new Error("placement not found");
      if (placement.lane !== "relay_source") throw new Error("placement is not relay_source");
    }

    const { share, membership } = await resolveShareForUser(ctx, userId, {
      shareId: args.shareId,
      projectSlug: args.projectSlug,
    });
    const now = Date.now();
    const expiresAt = now + Math.max(5 * 60_000, Math.min(args.ttlMs ?? 24 * 60 * 60_000, 7 * 24 * 60 * 60_000));
    const fallbackSeed = `${share.slug}-${localTaskId}`;
    const branch = normalizeRelayBranch(args.branch || membership?.branch, fallbackSeed);
    const baseBranch = trimLabel(share.defaultBranch, 80) || "main";
    const providerTarget = parseRelaySourceProviderTarget(share.repoUrl, branch);
    const existing = await ctx.db
      .query("relaySourceIntents")
      .withIndex("by_local_task", (q: any) => q.eq("localTaskId", localTaskId))
      .first();
    const patch = {
      userId,
      ownerUserId: share.ownerUserId,
      shareId: share._id,
      membershipId: membership?._id,
      placementId: args.placementId,
      localTaskId,
      sourceSurface: trimLabel(args.sourceSurface, 80),
      projectSlug: share.slug,
      repoUrl: share.repoUrl,
      baseBranch,
      branch,
      ...providerTarget,
      kind: trimLabel(args.kind, 40) || "source",
      status: "queued" as RelaySourceStatus,
      reason: trimLabel(args.reason, 240),
      attempts: existing?.attempts ?? 0,
      expiresAt,
      updatedAt: now,
    };
    if (existing && String(existing.userId) === String(userId)) {
      await ctx.db.patch(existing._id, patch);
      return serialize({ ...existing, ...patch });
    }
    const id = await ctx.db.insert("relaySourceIntents", {
      ...patch,
      createdAt: now,
    });
    const row = await ctx.db.get(id);
    if (!row) throw new Error("relay source intent insert failed");
    return serialize(row);
  },
});

export const update = mutation({
  args: {
    tokenHash: v.string(),
    intentId: v.optional(v.id("relaySourceIntents")),
    localTaskId: v.optional(v.string()),
    status: statuses,
    taskId: v.optional(v.string()),
    relayId: v.optional(v.string()),
    reason: v.optional(v.string()),
    lastError: v.optional(v.string()),
    providerKind: v.optional(v.string()),
    providerHost: v.optional(v.string()),
    providerRepo: v.optional(v.string()),
    providerBranch: v.optional(v.string()),
    providerBranchUrl: v.optional(v.string()),
    providerAppInstallationId: v.optional(v.string()),
    providerAuthMode: v.optional(v.string()),
    providerAuthStatus: v.optional(v.string()),
    bumpAttempt: v.optional(v.boolean()),
  },
  handler: async (ctx, args) => {
    const userId = await userFromToken(ctx, args.tokenHash);
    const row = args.intentId
      ? await ctx.db.get(args.intentId)
      : args.localTaskId
        ? await ctx.db
            .query("relaySourceIntents")
            .withIndex("by_local_task", (q: any) => q.eq("localTaskId", args.localTaskId!.trim()))
            .first()
        : null;
    if (!row || !canUpdateRelaySourceIntent(row, userId)) throw new Error("relay source intent not found");
    const terminal = args.status === "cancelled" || args.status === "expired" || args.status === "failed";
    const now = Date.now();
    const patch: Record<string, any> = {
      status: args.status,
      attempts: args.bumpAttempt ? (row.attempts ?? 0) + 1 : row.attempts ?? 0,
      updatedAt: now,
    };
    if (args.taskId !== undefined) patch.taskId = trimLabel(args.taskId, 160);
    if (args.relayId !== undefined) patch.relayId = trimLabel(args.relayId, 120);
    if (args.reason !== undefined) patch.reason = trimLabel(args.reason, 240);
    if (args.lastError !== undefined) patch.lastError = trimLabel(args.lastError, 240);
    if (args.providerKind !== undefined) patch.providerKind = safeProviderLabel(args.providerKind, 40);
    if (args.providerHost !== undefined) patch.providerHost = safeProviderLabel(args.providerHost, 120);
    if (args.providerRepo !== undefined) patch.providerRepo = safeProviderLabel(args.providerRepo, 180);
    if (args.providerBranch !== undefined) patch.providerBranch = normalizeRelayBranch(args.providerBranch, row.branch);
    if (args.providerBranchUrl !== undefined) {
      const url = String(args.providerBranchUrl || "").trim();
      patch.providerBranchUrl = /^https:\/\/[^@\s]+$/i.test(url) ? url.slice(0, 320) : undefined;
    }
    if (args.providerAppInstallationId !== undefined) patch.providerAppInstallationId = safeProviderLabel(args.providerAppInstallationId, 120);
    if (args.providerAuthMode !== undefined) patch.providerAuthMode = safeProviderLabel(args.providerAuthMode, 40);
    if (args.providerAuthStatus !== undefined) patch.providerAuthStatus = safeProviderLabel(args.providerAuthStatus, 60);
    if (terminal) patch.completedAt = now;
    await ctx.db.patch(row._id, patch);
    return serialize({ ...row, ...patch });
  },
});

export const claimNext = mutation({
  args: {
    tokenHash: v.string(),
    projectSlug: v.optional(v.string()),
    relayId: v.optional(v.string()),
  },
  handler: async (ctx, args) => {
    const ownerUserId = await userFromToken(ctx, args.tokenHash);
    const now = Date.now();
    const projectSlug = normalizeProjectSlug(args.projectSlug);
    const rows = await ctx.db
      .query("relaySourceIntents")
      .withIndex("by_owner_created", (q: any) => q.eq("ownerUserId", ownerUserId))
      .order("asc")
      .take(50);
    const row = rows.find((candidate: any) =>
      candidate.status === "queued" &&
      candidate.expiresAt > now &&
      (!projectSlug || candidate.projectSlug === projectSlug)
    );
    if (!row) return null;
    const patch = {
      status: "claimed" as RelaySourceStatus,
      relayId: trimLabel(args.relayId, 120),
      reason: "relay claimed branch-scoped source work",
      attempts: (row.attempts ?? 0) + 1,
      updatedAt: now,
    };
    await ctx.db.patch(row._id, patch);
    return serialize({ ...row, ...patch });
  },
});

export const listRecent = query({
  args: {
    tokenHash: v.string(),
    projectSlug: v.optional(v.string()),
    limit: v.optional(v.number()),
    includeTerminal: v.optional(v.boolean()),
    scope: v.optional(v.union(v.literal("mine"), v.literal("owned"), v.literal("all"))),
  },
  handler: async (ctx, args) => {
    const userId = await userFromToken(ctx, args.tokenHash);
    const n = Math.max(1, Math.min(100, args.limit ?? 25));
    const ownRows = args.scope === "owned"
      ? []
      : await ctx.db
          .query("relaySourceIntents")
          .withIndex("by_user_created", (q: any) => q.eq("userId", userId))
          .order("desc")
          .take(n);
    const ownerRows = args.scope === "mine"
      ? []
      : await ctx.db
          .query("relaySourceIntents")
          .withIndex("by_owner_created", (q: any) => q.eq("ownerUserId", userId))
          .order("desc")
          .take(n);
    const byId = new Map<string, any>();
    for (const row of [...ownRows, ...ownerRows]) byId.set(String(row._id), row);
    const rows = [...byId.values()]
      .sort((a: any, b: any) => (b.createdAt ?? 0) - (a.createdAt ?? 0))
      .slice(0, n);
    const now = Date.now();
    const projectSlug = normalizeProjectSlug(args.projectSlug);
    return rows
      .filter((row: any) => !projectSlug || row.projectSlug === projectSlug)
      .filter((row: any) =>
        args.includeTerminal ||
        (row.expiresAt > now && !["failed", "cancelled", "expired"].includes(row.status))
      )
      .map(serialize);
  },
});

export const githubAppAuthTarget = query({
  args: {
    tokenHash: v.string(),
    intentId: v.optional(v.id("relaySourceIntents")),
    localTaskId: v.optional(v.string()),
  },
  handler: async (ctx, args) => {
    const userId = await userFromToken(ctx, args.tokenHash);
    const row = args.intentId
      ? await ctx.db.get(args.intentId)
      : args.localTaskId
        ? await ctx.db
            .query("relaySourceIntents")
            .withIndex("by_local_task", (q: any) => q.eq("localTaskId", args.localTaskId!.trim()))
            .first()
        : null;
    if (!row || String(row.ownerUserId) !== String(userId)) throw new Error("relay source intent not found");
    if (row.providerKind !== "github") throw new Error("relay source intent is not a GitHub target");
    if (!String(row.providerBranch || row.branch || "").startsWith("yaver/")) throw new Error("relay source provider branch must be under yaver/");
    if (!row.providerRepo || !row.providerHost) throw new Error("relay source provider target missing");
    return {
      id: row._id,
      localTaskId: row.localTaskId,
      providerKind: row.providerKind,
      providerHost: row.providerHost,
      providerRepo: row.providerRepo,
      providerBranch: row.providerBranch || row.branch,
      providerBranchUrl: row.providerBranchUrl ?? null,
      providerAppInstallationId: row.providerAppInstallationId ?? null,
      providerAuthMode: row.providerAuthMode ?? "none",
      providerAuthStatus: row.providerAuthStatus ?? "required",
    };
  },
});

export const gitlabScopedAuthTarget = query({
  args: {
    tokenHash: v.string(),
    intentId: v.optional(v.id("relaySourceIntents")),
    localTaskId: v.optional(v.string()),
  },
  handler: async (ctx, args) => {
    const userId = await userFromToken(ctx, args.tokenHash);
    const row = args.intentId
      ? await ctx.db.get(args.intentId)
      : args.localTaskId
        ? await ctx.db
            .query("relaySourceIntents")
            .withIndex("by_local_task", (q: any) => q.eq("localTaskId", args.localTaskId!.trim()))
            .first()
        : null;
    if (!row || String(row.ownerUserId) !== String(userId)) throw new Error("relay source intent not found");
    if (row.providerKind !== "gitlab") throw new Error("relay source intent is not a GitLab target");
    if (!String(row.providerBranch || row.branch || "").startsWith("yaver/")) throw new Error("relay source provider branch must be under yaver/");
    if (!row.providerRepo || !row.providerHost) throw new Error("relay source provider target missing");
    return {
      id: row._id,
      localTaskId: row.localTaskId,
      providerKind: row.providerKind,
      providerHost: row.providerHost,
      providerRepo: row.providerRepo,
      providerBranch: row.providerBranch || row.branch,
      providerBranchUrl: row.providerBranchUrl ?? null,
      providerAuthMode: row.providerAuthMode ?? "none",
      providerAuthStatus: row.providerAuthStatus ?? "required",
    };
  },
});
