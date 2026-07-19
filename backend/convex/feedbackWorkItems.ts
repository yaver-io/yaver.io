// feedbackWorkItems.ts — Feedback SDK queue for owner-reviewed task/issue work.
//
// This table intentionally stores only bounded feedback text, coarse labels,
// artifact ids, and HTTPS attachment URLs. It does not launch runners, write
// Git providers, store screenshots/base64, local paths, OAuth, stdout, or
// provider credentials.

import { v } from "convex/values";
import { mutation, query } from "./_generated/server";
import type { Id } from "./_generated/dataModel";
import { validateSessionInternal } from "./auth";
import { normalizeRelayBranch, parseRelaySourceProviderTarget, relaySourceSlug } from "./relaySourceIntents";

const kinds = new Set(["bug", "idea", "task", "question", "other"]);
const priorities = new Set(["low", "normal", "high"]);
const targets = new Set(["task", "issue", "branch", "triage"]);
const pendingFeedbackStatuses = new Set(["queued", "claimed"]);
const defaultOwnerPendingFeedbackLimit = 200;
const defaultSharePendingFeedbackLimit = 80;

const statuses = v.union(
  v.literal("queued"),
  v.literal("claimed"),
  v.literal("task_created"),
  v.literal("issue_draft_created"),
  v.literal("issue_created"),
  v.literal("branch_created"),
  v.literal("blocked"),
  v.literal("cancelled"),
  v.literal("rejected"),
  v.literal("expired"),
);

export type FeedbackWorkStatus =
  | "queued"
  | "claimed"
  | "task_created"
  | "issue_draft_created"
  | "issue_created"
  | "branch_created"
  | "blocked"
  | "cancelled"
  | "rejected"
  | "expired";

const terminalFeedbackWorkStatuses = new Set<FeedbackWorkStatus>([
  "task_created",
  "issue_draft_created",
  "issue_created",
  "branch_created",
  "blocked",
  "cancelled",
  "rejected",
  "expired",
]);

const reroutableTerminalFeedbackWorkStatuses = new Set<FeedbackWorkStatus>([
  "blocked",
]);
const reusableFeedbackRelaySourceStatuses = new Set(["queued", "claimed", "committed", "handoff_ready"]);

export function isTerminalFeedbackWorkStatus(status: unknown): boolean {
  return terminalFeedbackWorkStatuses.has(String(status || "") as FeedbackWorkStatus);
}

export function canRouteFeedbackWorkStatus(status: unknown): boolean {
  const normalized = String(status || "") as FeedbackWorkStatus;
  if (!terminalFeedbackWorkStatuses.has(normalized)) return true;
  return reroutableTerminalFeedbackWorkStatuses.has(normalized);
}

export function feedbackPendingLimit(envValue: unknown, fallback: number): number {
  const parsed = Number(envValue);
  if (Number.isFinite(parsed) && parsed >= 0) return Math.floor(parsed);
  return fallback;
}

export function feedbackPendingLimitExceeded(args: { pendingCount: number; limit: number }): boolean {
  if (args.limit <= 0) return false;
  return args.pendingCount >= args.limit;
}

async function countPendingFeedbackForIndex(
  ctx: any,
  tableIndex: "by_owner_status" | "by_share_status",
  field: "ownerUserId" | "shareId",
  value: Id<"users"> | Id<"projectShares">,
  now: number,
): Promise<number> {
  let count = 0;
  for (const status of pendingFeedbackStatuses) {
    const rows = await ctx.db
      .query("feedbackWorkItems")
      .withIndex(tableIndex, (q: any) => q.eq(field, value).eq("status", status))
      .collect();
    count += rows.filter((row: any) => row.expiresAt > now).length;
  }
  return count;
}

async function enforceFeedbackQueueLimits(
  ctx: any,
  args: { ownerUserId: Id<"users">; shareId: Id<"projectShares">; now: number },
) {
  const ownerLimit = feedbackPendingLimit(process.env.YAVER_FEEDBACK_OWNER_PENDING_LIMIT, defaultOwnerPendingFeedbackLimit);
  const shareLimit = feedbackPendingLimit(process.env.YAVER_FEEDBACK_PROJECT_PENDING_LIMIT, defaultSharePendingFeedbackLimit);
  const [ownerPending, sharePending] = await Promise.all([
    countPendingFeedbackForIndex(ctx, "by_owner_status", "ownerUserId", args.ownerUserId, args.now),
    countPendingFeedbackForIndex(ctx, "by_share_status", "shareId", args.shareId, args.now),
  ]);
  if (feedbackPendingLimitExceeded({ pendingCount: ownerPending, limit: ownerLimit })) {
    throw new Error(`feedback queue limit reached for owner: ${ownerPending}/${ownerLimit}`);
  }
  if (feedbackPendingLimitExceeded({ pendingCount: sharePending, limit: shareLimit })) {
    throw new Error(`feedback queue limit reached for project: ${sharePending}/${shareLimit}`);
  }
}

async function userFromSessionToken(ctx: any, tokenHash: string): Promise<Id<"users">> {
  const session = await validateSessionInternal(ctx, tokenHash);
  if (!session) throw new Error("Unauthorized");
  return session.user._id;
}

async function sdkContext(ctx: any, sdkTokenHash: string): Promise<{
  ownerUserId: Id<"users">;
  requesterUserId: Id<"users">;
  scopes: string[];
  allowedProjects: string[];
  sourceSurface?: string;
  label?: string;
}> {
  const row = await ctx.db
    .query("sdkTokens")
    .withIndex("by_tokenHash", (q: any) => q.eq("tokenHash", sdkTokenHash))
    .unique();
  if (!row || row.expiresAt < Date.now()) throw new Error("Unauthorized");
  if (row.replacedAt && Date.now() - row.replacedAt > 5 * 60_000) throw new Error("Unauthorized");
  const scopes = Array.isArray(row.scopes) ? row.scopes.map((s: unknown) => String(s)) : [];
  if (!scopes.includes("feedback")) throw new Error("SDK token lacks feedback scope");
  return {
    ownerUserId: row.userId,
    requesterUserId: row.delegatedGuestUserId ?? row.userId,
    scopes,
    allowedProjects: Array.isArray(row.allowedProjects)
      ? row.allowedProjects.map((item: unknown) => normalizeProjectSlug(String(item))).filter(Boolean) as string[]
      : [],
    sourceSurface: trimLabel(row.sourceSurface, 80),
    label: trimLabel(row.label, 120),
  };
}

function trimLabel(value: unknown, max: number): string | undefined {
  const text = String(value ?? "").trim().replace(/\s+/g, " ");
  return text ? text.slice(0, max) : undefined;
}

function trimBody(value: unknown): string {
  return String(value ?? "").trim().slice(0, 4000);
}

export function normalizeFeedbackKind(value: unknown): string {
  const raw = String(value || "").trim().toLowerCase().replace(/[^a-z0-9._-]+/g, "-");
  return kinds.has(raw) ? raw : "other";
}

export function normalizeFeedbackPriority(value: unknown): string {
  const raw = String(value || "").trim().toLowerCase();
  return priorities.has(raw) ? raw : "normal";
}

export function normalizeFeedbackTarget(value: unknown): "task" | "issue" | "branch" | "triage" {
  const raw = String(value || "").trim().toLowerCase();
  return (targets.has(raw) ? raw : "triage") as "task" | "issue" | "branch" | "triage";
}

export function normalizeProjectSlug(value: unknown): string | undefined {
  const s = String(value || "").trim();
  if (!s || /[\\/]/.test(s)) return undefined;
  return s.slice(0, 80);
}

export function normalizeAttachmentUrl(value: unknown): string | undefined {
  const raw = String(value ?? "").trim();
  if (!raw) return undefined;
  if (raw.length > 2048) throw new Error("attachment url too long");
  let parsed: URL;
  try {
    parsed = new URL(raw);
  } catch {
    throw new Error("attachment url must be absolute https");
  }
  if (parsed.protocol !== "https:") throw new Error("attachment url must be https");
  parsed.username = "";
  parsed.password = "";
  return parsed.toString();
}

export function normalizeAttachmentUrls(values: unknown): string[] | undefined {
  if (!Array.isArray(values)) return undefined;
  const out: string[] = [];
  const seen = new Set<string>();
  for (const value of values.slice(0, 8)) {
    const url = normalizeAttachmentUrl(value);
    if (!url || seen.has(url)) continue;
    seen.add(url);
    out.push(url);
  }
  return out.length ? out : undefined;
}

export function uniqueFeedbackArtifactIds(values: unknown): string[] | undefined {
  if (!Array.isArray(values)) return undefined;
  const out: string[] = [];
  const seen = new Set<string>();
  for (const value of values) {
    const id = String(value || "").trim();
    if (!id || seen.has(id)) continue;
    seen.add(id);
    out.push(id);
    if (out.length >= 12) break;
  }
  return out.length ? out : undefined;
}

export function canAttachArtifactToFeedback(
  row: any,
  args: {
    shareId: unknown;
    requesterUserId: unknown;
    ownerUserId: unknown;
    now?: number;
  },
): boolean {
  if (!row) return false;
  const now = args.now ?? Date.now();
  if (String(row.shareId) !== String(args.shareId)) return false;
  if (String(row.ownerUserId) !== String(args.ownerUserId)) return false;
  if (row.status !== "active") return false;
  if (row.expiresAt && row.expiresAt <= now) return false;
  if (row.visibility === "private") {
    return String(row.userId) === String(args.requesterUserId) ||
      String(args.requesterUserId) === String(args.ownerUserId);
  }
  return true;
}

async function resolveFeedbackArtifactIds(
  ctx: any,
  ids: Id<"projectArtifacts">[] | undefined,
  args: {
    shareId: Id<"projectShares">;
    requesterUserId: Id<"users">;
    ownerUserId: Id<"users">;
  },
): Promise<Id<"projectArtifacts">[] | undefined> {
  const unique = uniqueFeedbackArtifactIds(ids) as Id<"projectArtifacts">[] | undefined;
  if (!unique?.length) return undefined;
  const out: Id<"projectArtifacts">[] = [];
  const now = Date.now();
  for (const id of unique) {
    const row = await ctx.db.get(id);
    if (!canAttachArtifactToFeedback(row, { ...args, now })) {
      throw new Error("artifact not found for this project");
    }
    out.push(id);
  }
  return out;
}

async function resolveShareForOwner(
  ctx: any,
  ownerUserId: Id<"users">,
  args: { shareId?: Id<"projectShares">; projectSlug?: string },
) {
  let share = args.shareId ? await ctx.db.get(args.shareId) : null;
  const projectSlug = normalizeProjectSlug(args.projectSlug);
  if (!share && projectSlug) {
    share = await ctx.db
      .query("projectShares")
      .withIndex("by_owner", (q: any) => q.eq("ownerUserId", ownerUserId))
      .filter((q: any) => q.eq(q.field("slug"), projectSlug))
      .filter((q: any) => q.eq(q.field("status"), "active"))
      .first();
  }
  if (!share || share.status !== "active" || String(share.ownerUserId) !== String(ownerUserId)) {
    throw new Error("project share not found");
  }
  return share;
}

async function membershipForRequester(ctx: any, shareId: Id<"projectShares">, requesterUserId: Id<"users">) {
  const membership = await ctx.db
    .query("projectMemberships")
    .withIndex("by_share_user", (q: any) => q.eq("shareId", shareId).eq("userId", requesterUserId))
    .first();
  return membership?.status === "active" ? membership : null;
}

function assertProjectAllowed(projectSlug: string, allowedProjects: string[]) {
  if (allowedProjects.length === 0) return;
  if (!allowedProjects.includes(projectSlug)) throw new Error("SDK token is not allowed for this project");
}

function serialize(row: any) {
  return {
    id: row._id,
    shareId: row.shareId,
    membershipId: row.membershipId ?? null,
    projectSlug: row.projectSlug,
    sourceSurface: row.sourceSurface ?? null,
    title: row.title,
    body: row.body,
    kind: row.kind,
    priority: row.priority,
    component: row.component ?? null,
    appVersion: row.appVersion ?? null,
    platform: row.platform ?? null,
    artifactIds: row.artifactIds ?? [],
    attachmentUrls: row.attachmentUrls ?? [],
    target: row.target,
    status: row.status,
    relaySourceIntentId: row.relaySourceIntentId ?? null,
    taskId: row.taskId ?? null,
    issueUrl: row.issueUrl ?? null,
    branch: row.branch ?? null,
    reason: row.reason ?? null,
    lastError: row.lastError ?? null,
    workerId: row.workerId ?? null,
    attempts: row.attempts,
    expiresAt: row.expiresAt,
    createdAt: row.createdAt,
    updatedAt: row.updatedAt,
    completedAt: row.completedAt ?? null,
  };
}

export const createFromSdk = mutation({
  args: {
    sdkTokenHash: v.string(),
    shareId: v.optional(v.id("projectShares")),
    projectSlug: v.optional(v.string()),
    title: v.string(),
    body: v.string(),
    kind: v.optional(v.string()),
    priority: v.optional(v.string()),
    component: v.optional(v.string()),
    appVersion: v.optional(v.string()),
    platform: v.optional(v.string()),
    artifactIds: v.optional(v.array(v.id("projectArtifacts"))),
    attachmentUrls: v.optional(v.array(v.string())),
    target: v.optional(v.string()),
    ttlMs: v.optional(v.number()),
  },
  handler: async (ctx, args) => {
    const sdk = await sdkContext(ctx, args.sdkTokenHash);
    const share = await resolveShareForOwner(ctx, sdk.ownerUserId, {
      shareId: args.shareId,
      projectSlug: args.projectSlug,
    });
    assertProjectAllowed(share.slug, sdk.allowedProjects);
    const title = trimLabel(args.title, 160);
    const body = trimBody(args.body);
    if (!title) throw new Error("title required");
    if (!body) throw new Error("body required");
    const membership = String(sdk.requesterUserId) === String(sdk.ownerUserId)
      ? null
      : await membershipForRequester(ctx, share._id, sdk.requesterUserId);
    if (String(sdk.requesterUserId) !== String(sdk.ownerUserId) && !membership && sdk.allowedProjects.length === 0) {
      throw new Error("guest SDK token is not project-scoped");
    }
    const now = Date.now();
    await enforceFeedbackQueueLimits(ctx, {
      ownerUserId: share.ownerUserId,
      shareId: share._id,
      now,
    });
    const expiresAt = now + Math.max(60 * 60_000, Math.min(args.ttlMs ?? 30 * 24 * 60 * 60_000, 180 * 24 * 60 * 60_000));
    const artifactIds = await resolveFeedbackArtifactIds(ctx, args.artifactIds, {
      shareId: share._id,
      requesterUserId: sdk.requesterUserId,
      ownerUserId: share.ownerUserId,
    });
    const id = await ctx.db.insert("feedbackWorkItems", {
      userId: sdk.requesterUserId,
      ownerUserId: share.ownerUserId,
      shareId: share._id,
      membershipId: membership?._id,
      projectSlug: share.slug,
      sourceSurface: trimLabel(sdk.sourceSurface || "feedback-sdk", 80),
      sourceTokenLabel: sdk.label,
      title,
      body,
      kind: normalizeFeedbackKind(args.kind),
      priority: normalizeFeedbackPriority(args.priority),
      component: trimLabel(args.component, 120),
      appVersion: trimLabel(args.appVersion, 80),
      platform: trimLabel(args.platform, 80),
      artifactIds,
      attachmentUrls: normalizeAttachmentUrls(args.attachmentUrls),
      target: normalizeFeedbackTarget(args.target),
      status: "queued" as FeedbackWorkStatus,
      attempts: 0,
      expiresAt,
      createdAt: now,
      updatedAt: now,
    });
    const row = await ctx.db.get(id);
    if (!row) throw new Error("feedback work item insert failed");
    return serialize(row);
  },
});

export const list = query({
  args: {
    tokenHash: v.string(),
    shareId: v.optional(v.id("projectShares")),
    projectSlug: v.optional(v.string()),
    scope: v.optional(v.union(v.literal("owned"), v.literal("mine"))),
    status: v.optional(v.string()),
    limit: v.optional(v.number()),
  },
  handler: async (ctx, args) => {
    const userId = await userFromSessionToken(ctx, args.tokenHash);
    const limit = Math.max(1, Math.min(args.limit ?? 50, 100));
    const now = Date.now();
    const scope = args.scope ?? "owned";
    let rows: any[] = [];
    if (scope === "mine") {
      rows = await ctx.db
        .query("feedbackWorkItems")
        .withIndex("by_user_created", (q: any) => q.eq("userId", userId))
        .order("desc")
        .take(limit);
    } else {
      let shareId = args.shareId;
      if (!shareId && args.projectSlug) {
        const share = await resolveShareForOwner(ctx, userId, { projectSlug: args.projectSlug });
        shareId = share._id;
      }
      if (shareId) {
        const share = await ctx.db.get(shareId);
        if (!share || String(share.ownerUserId) !== String(userId)) throw new Error("project share not found");
        rows = await ctx.db
          .query("feedbackWorkItems")
          .withIndex("by_share_created", (q: any) => q.eq("shareId", shareId))
          .order("desc")
          .take(limit);
      } else {
        rows = await ctx.db
          .query("feedbackWorkItems")
          .withIndex("by_owner_created", (q: any) => q.eq("ownerUserId", userId))
          .order("desc")
          .take(limit);
      }
    }
    return rows
      .filter((row: any) => !args.status || row.status === args.status)
      .filter((row: any) => row.status !== "expired" && row.expiresAt > now)
      .map(serialize);
  },
});

export const claimNext = mutation({
  args: {
    tokenHash: v.string(),
    shareId: v.optional(v.id("projectShares")),
    projectSlug: v.optional(v.string()),
    workerId: v.optional(v.string()),
  },
  handler: async (ctx, args) => {
    const userId = await userFromSessionToken(ctx, args.tokenHash);
    let shareId = args.shareId;
    if (!shareId && args.projectSlug) {
      const share = await resolveShareForOwner(ctx, userId, { projectSlug: args.projectSlug });
      shareId = share._id;
    }
    const rows = shareId
      ? await ctx.db
          .query("feedbackWorkItems")
          .withIndex("by_share_created", (q: any) => q.eq("shareId", shareId))
          .order("asc")
          .collect()
      : await ctx.db
          .query("feedbackWorkItems")
          .withIndex("by_owner_created", (q: any) => q.eq("ownerUserId", userId))
          .order("asc")
          .collect();
    const now = Date.now();
    const row = rows.find((candidate: any) =>
      String(candidate.ownerUserId) === String(userId) &&
      candidate.status === "queued" &&
      candidate.expiresAt > now
    );
    if (!row) return null;
    const patch = {
      status: "claimed" as FeedbackWorkStatus,
      workerId: trimLabel(args.workerId, 120),
      attempts: (row.attempts || 0) + 1,
      updatedAt: now,
    };
    await ctx.db.patch(row._id, patch);
    return serialize({ ...row, ...patch });
  },
});

export const update = mutation({
  args: {
    tokenHash: v.string(),
    itemId: v.id("feedbackWorkItems"),
    status: statuses,
    taskId: v.optional(v.string()),
    issueUrl: v.optional(v.string()),
    branch: v.optional(v.string()),
    reason: v.optional(v.string()),
    lastError: v.optional(v.string()),
    workerId: v.optional(v.string()),
  },
  handler: async (ctx, args) => {
    const userId = await userFromSessionToken(ctx, args.tokenHash);
    const row = await ctx.db.get(args.itemId);
    if (!row || String(row.ownerUserId) !== String(userId)) throw new Error("feedback work item not found");
    const now = Date.now();
    const terminal = isTerminalFeedbackWorkStatus(args.status);
    const patch = {
      status: args.status,
      taskId: trimLabel(args.taskId, 160),
      issueUrl: normalizeAttachmentUrl(args.issueUrl),
      branch: trimLabel(args.branch, 160),
      reason: trimLabel(args.reason, 240),
      lastError: trimLabel(args.lastError, 240),
      workerId: trimLabel(args.workerId, 120),
      updatedAt: now,
      ...(terminal ? { completedAt: now } : {}),
    };
    await ctx.db.patch(row._id, patch);
    return serialize({ ...row, ...patch });
  },
});

export const route = mutation({
  args: {
    tokenHash: v.string(),
    itemId: v.id("feedbackWorkItems"),
    target: v.union(v.literal("task"), v.literal("issue"), v.literal("branch"), v.literal("triage")),
    reason: v.optional(v.string()),
    workerId: v.optional(v.string()),
  },
  handler: async (ctx, args) => {
    const userId = await userFromSessionToken(ctx, args.tokenHash);
    const row = await ctx.db.get(args.itemId);
    if (!row || String(row.ownerUserId) !== String(userId)) throw new Error("feedback work item not found");
    if (row.expiresAt <= Date.now() || row.status === "expired") throw new Error("feedback work item expired");
    if (!canRouteFeedbackWorkStatus(row.status)) {
      throw new Error("feedback work item is already terminal");
    }
    const now = Date.now();
    const patch = {
      target: args.target,
      status: "queued" as FeedbackWorkStatus,
      relaySourceIntentId: undefined,
      taskId: undefined,
      issueUrl: undefined,
      branch: undefined,
      reason: trimLabel(args.reason, 240),
      lastError: undefined,
      workerId: trimLabel(args.workerId, 120),
      updatedAt: now,
    };
    await ctx.db.patch(row._id, patch);
    return serialize({ ...row, ...patch });
  },
});

function serializeRelaySourceIntent(row: any) {
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

export function feedbackRelayLocalTaskId(itemId: unknown): string {
  return `feedback:${String(itemId || "").trim().slice(0, 160)}`;
}

export function feedbackRelayReason(kind: unknown, title: unknown): string {
  return trimLabel(`feedback:${normalizeFeedbackKind(kind)}:${String(title || "").trim()}`, 240) || "feedback";
}

export function feedbackRelayIntentMetadata(args: {
  itemId: unknown;
  projectSlug: unknown;
  title: unknown;
  kind: unknown;
  repoUrl?: string;
  defaultBranch?: string;
  branch?: string;
}) {
  const localTaskId = feedbackRelayLocalTaskId(args.itemId);
  const fallbackSeed = `${normalizeProjectSlug(args.projectSlug) || "project"}-${relaySourceSlug(String(args.title || ""), "feedback")}`;
  const branch = normalizeRelayBranch(args.branch, fallbackSeed);
  const baseBranch = trimLabel(args.defaultBranch, 80) || "main";
  const providerTarget = parseRelaySourceProviderTarget(args.repoUrl, branch);
  return {
    localTaskId,
    baseBranch,
    branch,
    reason: feedbackRelayReason(args.kind, args.title),
    ...providerTarget,
  };
}

export function canReuseFeedbackRelaySourceIntent(
  row: any,
  args: {
    ownerUserId: unknown;
    shareId: unknown;
    localTaskId: unknown;
  },
): boolean {
  if (!row) return false;
  if (String(row.ownerUserId) !== String(args.ownerUserId)) return false;
  if (String(row.shareId) !== String(args.shareId)) return false;
  if (String(row.localTaskId) !== String(args.localTaskId)) return false;
  return reusableFeedbackRelaySourceStatuses.has(String(row.status || ""));
}

export function feedbackRelaySourceLocalTaskCollision(
  row: any,
  args: {
    ownerUserId: unknown;
    shareId: unknown;
    localTaskId: unknown;
  },
): boolean {
  if (!row) return false;
  if (String(row.localTaskId) !== String(args.localTaskId)) return true;
  return String(row.ownerUserId) !== String(args.ownerUserId) || String(row.shareId) !== String(args.shareId);
}

export const queueRelaySource = mutation({
  args: {
    tokenHash: v.string(),
    itemId: v.id("feedbackWorkItems"),
    branch: v.optional(v.string()),
    workerId: v.optional(v.string()),
    ttlMs: v.optional(v.number()),
  },
  handler: async (ctx, args) => {
    const ownerUserId = await userFromSessionToken(ctx, args.tokenHash);
    const row = await ctx.db.get(args.itemId);
    if (!row || String(row.ownerUserId) !== String(ownerUserId)) throw new Error("feedback work item not found");
    if (row.expiresAt <= Date.now() || row.status === "expired") throw new Error("feedback work item expired");
    if (!canRouteFeedbackWorkStatus(row.status)) {
      throw new Error("feedback work item is already terminal");
    }
    if (row.relaySourceIntentId) {
      const existing = await ctx.db.get(row.relaySourceIntentId);
      if (canReuseFeedbackRelaySourceIntent(existing, {
        ownerUserId,
        shareId: row.shareId,
        localTaskId: feedbackRelayLocalTaskId(row._id),
      })) {
        return { item: serialize(row), relaySourceIntent: serializeRelaySourceIntent(existing) };
      }
    }
    const share = await ctx.db.get(row.shareId);
    if (!share || share.status !== "active" || String(share.ownerUserId) !== String(ownerUserId)) {
      throw new Error("project share not found");
    }
    const now = Date.now();
    const metadata = feedbackRelayIntentMetadata({
      itemId: row._id,
      projectSlug: row.projectSlug,
      title: row.title,
      kind: row.kind,
      repoUrl: share.repoUrl,
      defaultBranch: share.defaultBranch,
      branch: args.branch,
    });
    const expiresAt = now + Math.max(5 * 60_000, Math.min(args.ttlMs ?? 24 * 60 * 60_000, 7 * 24 * 60 * 60_000));
    const existingByLocalTask = await ctx.db
      .query("relaySourceIntents")
      .withIndex("by_local_task", (q: any) => q.eq("localTaskId", metadata.localTaskId))
      .first();
    if (feedbackRelaySourceLocalTaskCollision(existingByLocalTask, {
      ownerUserId,
      shareId: row.shareId,
      localTaskId: metadata.localTaskId,
    })) {
      throw new Error("relay source local task collision");
    }
    const intentPatch = {
      userId: row.userId,
      ownerUserId,
      shareId: row.shareId,
      membershipId: row.membershipId,
      localTaskId: metadata.localTaskId,
      sourceSurface: "feedback-work-item",
      projectSlug: row.projectSlug,
      repoUrl: share.repoUrl,
      baseBranch: metadata.baseBranch,
      branch: metadata.branch,
      providerKind: metadata.providerKind,
      providerHost: metadata.providerHost,
      providerRepo: metadata.providerRepo,
      providerBranch: metadata.providerBranch,
      providerBranchUrl: metadata.providerBranchUrl,
      providerAuthMode: metadata.providerAuthMode,
      providerAuthStatus: metadata.providerAuthStatus,
      kind: "feedback",
      status: "queued" as const,
      reason: metadata.reason,
      attempts: existingByLocalTask?.attempts ?? 0,
      expiresAt,
      updatedAt: now,
    };
    let intentId: Id<"relaySourceIntents">;
    let intentRow: any;
    if (existingByLocalTask && String(existingByLocalTask.ownerUserId) === String(ownerUserId)) {
      intentId = existingByLocalTask._id;
      await ctx.db.patch(intentId, intentPatch);
      intentRow = { ...existingByLocalTask, ...intentPatch };
    } else {
      intentId = await ctx.db.insert("relaySourceIntents", {
        ...intentPatch,
        createdAt: now,
      });
      intentRow = await ctx.db.get(intentId);
      if (!intentRow) throw new Error("relay source intent insert failed");
    }
    const itemPatch = {
      status: "claimed" as FeedbackWorkStatus,
      relaySourceIntentId: intentId,
      branch: metadata.branch,
      reason: "queued branch-scoped relay source work",
      workerId: trimLabel(args.workerId, 120),
      updatedAt: now,
    };
    await ctx.db.patch(row._id, itemPatch);
    return {
      item: serialize({ ...row, ...itemPatch }),
      relaySourceIntent: serializeRelaySourceIntent(intentRow),
    };
  },
});
