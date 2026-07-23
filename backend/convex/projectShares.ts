import { mutation, query } from "./_generated/server";
import { v } from "convex/values";
import { validateSessionInternal } from "./auth";
import { Id } from "./_generated/dataModel";

// Shared projects — a thin wrapper that binds {repo, host, roster, roles}
// into one object you can "ask someone to join". Accepting a membership
// MATERIALIZES an infraAccessGrant + guestAccess + allowedProjects + the
// per-project role, via the same path guests.accept uses.
//
// Enforcement split, and it is worth being precise because this comment used
// to lie (fixed 2026-07-23):
//
//   * scope + allowedProjects — enforced in the agent's auth middleware
//     (desktop/agent/guest_scope.go). This part always worked.
//   * per-project capabilities — enforced in
//     desktop/agent/guest_project_role.go. This part did NOT work: nothing
//     role-shaped was ever sent to the agent. scopeForRole() collapses "dev"
//     and "normie" onto the same scope="full", so the agent had no field that
//     could tell a restricted collaborator from an unrestricted one, and the
//     "normie" restrictions this comment promised were unenforceable. A host
//     who picked "normie" in the UI got a full teammate.
//
// The role is a PRESET, not the policy. roleCapabilityPreset() below turns a
// role into explicit capability flags at materialize time; those flags are
// what travel and what the agent enforces. Nothing maps a role name to a
// permission inside the agent binary — otherwise one opinion of what "normie"
// means would be frozen into every install, and widening it for a single host
// would require an agent release.
//
// Enforced today: canDeploy. Carried but NOT yet enforced: canPush,
// requirePullRequest, pinnedBranch — they are wired end-to-end and read by the
// agent, and the git seam that would honor them is listed as an explicit TODO
// in guest_project_role.go. Do not claim enforcement here without a test that
// fails when the enforcement is removed.

type Role = "owner" | "dev" | "normie" | "viewer";

/** Map a project role to the guest-access scope it grants. */
function scopeForRole(role: Role): "full" | "feedback-only" {
  // dev/normie code (full agent surface, narrowed to the one project).
  // viewer only observes (feedback-only is the hardened read-ish tier).
  //
  // NOTE: this is lossy on purpose — scope is a coarse path allow-list and
  // cannot express "may code but may not deploy". The role travels separately
  // on guestAccess.projectRoles; do not try to encode more of it here.
  return role === "viewer" ? "feedback-only" : "full";
}

/** One member's effective permissions on one shared project. */
type ProjectRoleEntry = {
  project: string;
  role: Role;
  canDeploy?: boolean;
  canPush?: boolean;
  requirePullRequest?: boolean;
  pinnedBranch?: string;
};

/**
 * Default capabilities for a role. This is the ONLY place a role name becomes
 * a permission, and it runs server-side — so a host (or a future per-share
 * override UI) can change what "normie" means without anyone shipping a new
 * agent binary.
 *
 *   owner  — unrestricted.
 *   dev    — trusted teammate: codes, pushes, deploys.
 *   normie — non-technical collaborator: codes, but changes land as a PR on
 *            their own branch and they cannot deploy.
 *   viewer — observes only (scope="feedback-only" already blocks the rest;
 *            the flags are set anyway so enforcement never depends on scope
 *            and role agreeing).
 */
function roleCapabilityPreset(role: Role): Omit<ProjectRoleEntry, "project" | "role"> {
  switch (role) {
    case "owner":
      return { canDeploy: true, canPush: true, requirePullRequest: false };
    case "dev":
      return { canDeploy: true, canPush: true, requirePullRequest: false };
    case "normie":
      return { canDeploy: false, canPush: true, requirePullRequest: true };
    case "viewer":
      return { canDeploy: false, canPush: false, requirePullRequest: true };
  }
}

/**
 * Merge one project's entry into an existing projectRoles list.
 * Last write wins per project, order-stable, so a setRole on project A never
 * disturbs the member's permissions on project B.
 */
function mergeProjectRole(
  existing: unknown,
  entry: ProjectRoleEntry,
): ProjectRoleEntry[] {
  const list: ProjectRoleEntry[] = Array.isArray(existing)
    ? (existing as ProjectRoleEntry[]).filter((e) => e && typeof e.project === "string" && e.project)
    : [];
  const out = list.filter((e) => e.project.toLowerCase() !== entry.project.toLowerCase());
  out.push(entry);
  return out;
}

function generateShareCode(): string {
  const chars = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"; // no 0/O/1/I
  const buf = new Uint8Array(8);
  crypto.getRandomValues(buf);
  return Array.from(buf).map((b) => chars[b % chars.length]).join("");
}

/** Slugify a display name into a git-safe branch suffix. */
function branchSlug(name: string, fallback: string): string {
  const s = (name || "").toLowerCase().replace(/[^a-z0-9]+/g, "-").replace(/^-+|-+$/g, "");
  return s || fallback;
}

/** Strip embedded creds + scheme, normalize to host/owner/repo. */
function normalizeRepoUrl(raw: string): string {
  let s = (raw || "").trim();
  if (!s) return s;
  // git@host:owner/repo(.git)
  const ssh = s.match(/^[^@]+@([^:]+):(.+?)(?:\.git)?$/);
  if (ssh) return `${ssh[1]}/${ssh[2]}`.replace(/\/+$/, "");
  // https://user:pass@host/owner/repo(.git)
  s = s.replace(/^[a-z]+:\/\//i, "").replace(/^[^@/]+@/, "");
  s = s.replace(/\.git$/, "").replace(/\/+$/, "");
  return s;
}

async function resolveTargetUser(ctx: any, userId?: string, email?: string): Promise<any | null> {
  const rawUserId = (userId ?? "").trim();
  if (rawUserId) {
    const matches = await ctx.db
      .query("users")
      .filter((q: any) => q.eq(q.field("userId"), rawUserId))
      .collect();
    return matches[0] ?? null;
  }
  const rawEmail = (email ?? "").trim().toLowerCase();
  if (rawEmail) {
    return await ctx.db.query("users").withIndex("by_email", (q: any) => q.eq("email", rawEmail)).first();
  }
  return null;
}

/**
 * Materialize the access grant for an accepted membership. Mirrors
 * guests.materializeInvitationAccept but scoped from a projectShare:
 * scope from role, allowedProjects=[share.slug], device scope to the host
 * device when known. Returns the grantId (or undefined for whole-account).
 */
async function materializeProjectGrant(
  ctx: any,
  share: any,
  guestUserDocId: Id<"users">,
  role: Role,
  now: number,
  /** The member's assigned working branch, when the membership has one. */
  membershipBranch?: string,
): Promise<Id<"infraAccessGrants"> | undefined> {
  const scope = scopeForRole(role);

  // guestAccess row carries scope + project narrowing for the agent's
  // 10s config refresh (guest_config.go).
  const existing = await ctx.db
    .query("guestAccess")
    .withIndex("by_host_guest", (q: any) =>
      q.eq("hostUserId", share.ownerUserId).eq("guestUserId", guestUserDocId),
    )
    .filter((q: any) => q.eq(q.field("revokedAt"), undefined))
    .first();
  // The role's capability preset, resolved once and stored explicitly. The
  // agent enforces these flags; it never maps a role name to a permission.
  const roleEntry: ProjectRoleEntry = {
    project: share.slug,
    role,
    ...roleCapabilityPreset(role),
    ...(membershipBranch ? { pinnedBranch: membershipBranch } : {}),
  };

  if (!existing) {
    await ctx.db.insert("guestAccess", {
      hostUserId: share.ownerUserId,
      guestUserId: guestUserDocId,
      grantedAt: now,
      scope,
      allowedProjects: [share.slug],
      projectRoles: [roleEntry],
    });
  } else {
    // Already a guest of this owner (another shared project). Union this
    // project's slug into the allowlist so both projects stay reachable,
    // and widen scope if this role needs more than the existing grant.
    const current = Array.isArray(existing.allowedProjects) ? existing.allowedProjects : [];
    const widenScope = existing.scope === "feedback-only" && scope === "full";
    // Empty allowlist == "all projects" — never narrow that. Only union when
    // the existing grant is already a non-empty allowlist.
    const shouldMerge = current.length > 0 && !current.includes(share.slug);
    await ctx.db.patch(existing._id, {
      ...(shouldMerge ? { allowedProjects: [...current, share.slug] } : {}),
      ...(widenScope ? { scope: "full" } : {}),
      // Per-project, so widening scope for project A never silently grants
      // project B's permissions.
      projectRoles: mergeProjectRole(existing.projectRoles, roleEntry),
    });
  }

  // Scope the infra grant to the host device when we have one.
  const hostDeviceId: string | undefined = share.hostDeviceId;
  const grantId = await ctx.db.insert("infraAccessGrants", {
    hostUserId: share.ownerUserId,
    guestUserId: guestUserDocId,
    status: "active",
    shareAllDevices: !hostDeviceId,
    grantedAt: now,
    updatedAt: now,
    origin: "project-share",
  });
  if (hostDeviceId) {
    await ctx.db.insert("infraAccessGrantDevices", {
      grantId,
      hostUserId: share.ownerUserId,
      guestUserId: guestUserDocId,
      deviceId: hostDeviceId,
      createdAt: now,
    });
  }
  return grantId;
}

/** Revoke a single infra grant + its device/machine links. */
async function revokeGrant(ctx: any, grantId: Id<"infraAccessGrants"> | undefined, now: number) {
  if (!grantId) return;
  const grant = await ctx.db.get(grantId);
  if (!grant) return;
  await ctx.db.patch(grantId, { status: "revoked", revokedAt: now, updatedAt: now });
  const devLinks = await ctx.db
    .query("infraAccessGrantDevices")
    .withIndex("by_grant", (q: any) => q.eq("grantId", grantId))
    .collect();
  for (const link of devLinks) await ctx.db.delete(link._id);
  const machineLinks = await ctx.db
    .query("infraAccessGrantMachines")
    .withIndex("by_grant", (q: any) => q.eq("grantId", grantId))
    .collect();
  for (const link of machineLinks) await ctx.db.delete(link._id);
}

// ─── Mutations ──────────────────────────────────────────────────

/** Create a shared project. The caller is the owner. */
export const create = mutation({
  args: {
    tokenHash: v.string(),
    slug: v.string(),
    repoUrl: v.string(),
    defaultBranch: v.optional(v.string()),
    hostKind: v.union(v.literal("owner-device"), v.literal("managed-cloud")),
    hostDeviceId: v.optional(v.string()),
    hostMachineId: v.optional(v.id("cloudMachines")),
    payer: v.optional(v.union(v.literal("owner"), v.literal("invitee"))),
  },
  handler: async (ctx, args) => {
    const session = await validateSessionInternal(ctx, args.tokenHash);
    if (!session) throw new Error("Unauthorized");
    const owner = session.user._id;

    const slug = args.slug.trim();
    if (!slug) throw new Error("slug is required");
    const repoUrl = normalizeRepoUrl(args.repoUrl);
    if (!repoUrl) throw new Error("repoUrl is required");

    // Validate host device ownership when targeting an owner device.
    const hostDeviceId = (args.hostDeviceId ?? "").trim() || undefined;
    if (args.hostKind === "owner-device" && hostDeviceId) {
      const dev = await ctx.db
        .query("devices")
        .withIndex("by_deviceId", (q) => q.eq("deviceId", hostDeviceId))
        .unique();
      if (!dev || dev.userId !== owner) throw new Error("Host device not owned by you");
    }

    const now = Date.now();
    const shareId = await ctx.db.insert("projectShares", {
      ownerUserId: owner,
      slug,
      repoUrl,
      ...(args.defaultBranch ? { defaultBranch: args.defaultBranch.trim() } : {}),
      hostKind: args.hostKind,
      ...(hostDeviceId ? { hostDeviceId } : {}),
      ...(args.hostMachineId ? { hostMachineId: args.hostMachineId } : {}),
      payer: args.payer ?? "owner",
      shareCode: generateShareCode(),
      status: "active",
      createdAt: now,
      updatedAt: now,
    });

    // Owner membership (always active).
    await ctx.db.insert("projectMemberships", {
      shareId,
      ownerUserId: owner,
      userId: owner,
      role: "owner",
      status: "active",
      invitedAt: now,
      acceptedAt: now,
    });

    const share = await ctx.db.get(shareId);
    return { ok: true, shareId, shareCode: share?.shareCode };
  },
});

/** Invite a person to a project. Owner only. */
export const invite = mutation({
  args: {
    tokenHash: v.string(),
    shareId: v.id("projectShares"),
    peerUserId: v.optional(v.string()),
    peerEmail: v.optional(v.string()),
    role: v.optional(v.union(v.literal("dev"), v.literal("normie"), v.literal("viewer"))),
  },
  handler: async (ctx, args) => {
    const session = await validateSessionInternal(ctx, args.tokenHash);
    if (!session) throw new Error("Unauthorized");
    const owner = session.user._id;

    const share = await ctx.db.get(args.shareId);
    if (!share) throw new Error("Project not found");
    if (share.ownerUserId !== owner) throw new Error("Only the owner can invite");

    const role: Role = args.role ?? "normie";
    const peer = await resolveTargetUser(ctx, args.peerUserId, args.peerEmail);
    const peerEmail = (args.peerEmail ?? "").trim().toLowerCase() || (peer?.email?.toLowerCase() ?? "");

    if (!peer && !peerEmail) throw new Error("Provide peerUserId or peerEmail");
    if (peer && peer._id === owner) throw new Error("You are already the owner");

    // Existing membership?
    if (peer) {
      const existing = await ctx.db
        .query("projectMemberships")
        .withIndex("by_share_user", (q) => q.eq("shareId", args.shareId).eq("userId", peer._id))
        .first();
      if (existing && existing.status !== "revoked") {
        throw new Error("This user is already on the project");
      }
    }

    const now = Date.now();
    const branch = `yaver/${branchSlug(peer?.fullName ?? peerEmail, "guest")}`;
    const membershipId = await ctx.db.insert("projectMemberships", {
      shareId: args.shareId,
      ownerUserId: owner,
      ...(peer ? { userId: peer._id } : {}),
      ...(peerEmail ? { invitedEmail: peerEmail } : {}),
      role,
      branch,
      status: "invited",
      invitedAt: now,
    });

    return { ok: true, membershipId, shareCode: share.shareCode, role, branch };
  },
});

/** Accept a project invitation by share code. */
export const accept = mutation({
  args: { tokenHash: v.string(), shareCode: v.string() },
  handler: async (ctx, args) => {
    const session = await validateSessionInternal(ctx, args.tokenHash);
    if (!session) throw new Error("Unauthorized");
    const me = session.user._id;
    const myEmail = session.user.email.toLowerCase();

    const code = args.shareCode.toUpperCase().trim();
    const share = await ctx.db
      .query("projectShares")
      .withIndex("by_shareCode", (q) => q.eq("shareCode", code))
      .first();
    if (!share || share.status !== "active") throw new Error("Invalid project code");
    if (share.ownerUserId === me) throw new Error("You own this project");

    // Find the membership pinned to me (by userId) or my email.
    let membership = await ctx.db
      .query("projectMemberships")
      .withIndex("by_share_user", (q) => q.eq("shareId", share._id).eq("userId", me))
      .first();
    if (!membership) {
      const byEmail = await ctx.db
        .query("projectMemberships")
        .withIndex("by_share", (q) => q.eq("shareId", share._id))
        .collect();
      membership = byEmail.find((m) => (m.invitedEmail ?? "") === myEmail && m.status === "invited") ?? null;
    }
    if (!membership) throw new Error("No invitation found for your account");
    if (membership.status === "active") {
      return { ok: true, alreadyMember: true, slug: share.slug, repoUrl: share.repoUrl, role: membership.role, branch: membership.branch };
    }

    const now = Date.now();
    const role = membership.role as Role;
    const grantId = await materializeProjectGrant(ctx, share, me, role, now, membership.branch);

    await ctx.db.patch(membership._id, {
      userId: me,
      status: "active",
      acceptedAt: now,
      ...(grantId ? { grantId } : {}),
      ...(membership.branch ? {} : { branch: `yaver/${branchSlug(session.user.fullName, "guest")}` }),
    });

    return {
      ok: true,
      slug: share.slug,
      repoUrl: share.repoUrl,
      defaultBranch: share.defaultBranch,
      role,
      branch: membership.branch,
      hostKind: share.hostKind,
      hostDeviceId: share.hostDeviceId,
      ownerUserId: share.ownerUserId,
    };
  },
});

/** Change a member's role. Owner only. Re-materializes the grant scope. */
export const setRole = mutation({
  args: {
    tokenHash: v.string(),
    shareId: v.id("projectShares"),
    memberUserId: v.string(),
    role: v.union(v.literal("dev"), v.literal("normie"), v.literal("viewer")),
  },
  handler: async (ctx, args) => {
    const session = await validateSessionInternal(ctx, args.tokenHash);
    if (!session) throw new Error("Unauthorized");
    const share = await ctx.db.get(args.shareId);
    if (!share) throw new Error("Project not found");
    if (share.ownerUserId !== session.user._id) throw new Error("Only the owner can change roles");

    const member = await resolveTargetUser(ctx, args.memberUserId);
    if (!member) throw new Error("User not found");
    const membership = await ctx.db
      .query("projectMemberships")
      .withIndex("by_share_user", (q) => q.eq("shareId", args.shareId).eq("userId", member._id))
      .first();
    if (!membership || membership.status === "revoked") throw new Error("Not a member");
    if (membership.role === "owner") throw new Error("Cannot change the owner's role");

    const now = Date.now();
    await ctx.db.patch(membership._id, { role: args.role });
    // If active, swap the grant scope by revoking + re-materializing.
    if (membership.status === "active") {
      await revokeGrant(ctx, membership.grantId, now);
      const grantId = await materializeProjectGrant(
        ctx,
        share,
        member._id,
        args.role,
        now,
        membership.branch,
      );
      await ctx.db.patch(membership._id, grantId ? { grantId } : { grantId: undefined });
    }
    return { ok: true };
  },
});

/** Remove a member from a project + revoke their grant. Owner only. */
export const revokeMember = mutation({
  args: { tokenHash: v.string(), shareId: v.id("projectShares"), memberUserId: v.string() },
  handler: async (ctx, args) => {
    const session = await validateSessionInternal(ctx, args.tokenHash);
    if (!session) throw new Error("Unauthorized");
    const share = await ctx.db.get(args.shareId);
    if (!share) throw new Error("Project not found");
    if (share.ownerUserId !== session.user._id) throw new Error("Only the owner can remove members");

    const member = await resolveTargetUser(ctx, args.memberUserId);
    if (!member) throw new Error("User not found");
    const membership = await ctx.db
      .query("projectMemberships")
      .withIndex("by_share_user", (q) => q.eq("shareId", args.shareId).eq("userId", member._id))
      .first();
    if (!membership) throw new Error("Not a member");
    if (membership.role === "owner") throw new Error("Cannot remove the owner");

    const now = Date.now();
    await revokeGrant(ctx, membership.grantId, now);
    await ctx.db.patch(membership._id, { status: "revoked", revokedAt: now, grantId: undefined });

    // Also revoke the orphaned guestAccess row scoped to this project, if any.
    const access = await ctx.db
      .query("guestAccess")
      .withIndex("by_host_guest", (q) => q.eq("hostUserId", share.ownerUserId).eq("guestUserId", member._id))
      .filter((q) => q.eq(q.field("revokedAt"), undefined))
      .first();
    if (access && Array.isArray(access.allowedProjects) && access.allowedProjects.length === 1 && access.allowedProjects[0] === share.slug) {
      await ctx.db.patch(access._id, { revokedAt: now });
    } else if (access) {
      // The grant survives because this member is on OTHER shared projects of
      // the same host. Drop only this project's slug + permissions — leaving
      // the entry would keep a removed member's capabilities addressable if
      // the slug were ever re-shared.
      const slugs = Array.isArray(access.allowedProjects) ? access.allowedProjects : [];
      const roles = Array.isArray(access.projectRoles) ? access.projectRoles : [];
      await ctx.db.patch(access._id, {
        ...(slugs.length > 0
          ? { allowedProjects: slugs.filter((s: string) => s !== share.slug) }
          : {}),
        projectRoles: roles.filter(
          (e: { project?: string }) =>
            (e?.project ?? "").toLowerCase() !== String(share.slug).toLowerCase(),
        ),
      });
    }
    return { ok: true };
  },
});

/** Archive a project (owner). Revokes all member grants. */
export const archive = mutation({
  args: { tokenHash: v.string(), shareId: v.id("projectShares") },
  handler: async (ctx, args) => {
    const session = await validateSessionInternal(ctx, args.tokenHash);
    if (!session) throw new Error("Unauthorized");
    const share = await ctx.db.get(args.shareId);
    if (!share) throw new Error("Project not found");
    if (share.ownerUserId !== session.user._id) throw new Error("Only the owner can archive");

    const now = Date.now();
    const members = await ctx.db
      .query("projectMemberships")
      .withIndex("by_share", (q) => q.eq("shareId", args.shareId))
      .collect();
    for (const m of members) {
      if (m.role === "owner") continue;
      await revokeGrant(ctx, m.grantId, now);
      if (m.status !== "revoked") {
        await ctx.db.patch(m._id, { status: "revoked", revokedAt: now, grantId: undefined });
      }
    }
    await ctx.db.patch(args.shareId, { status: "archived", archivedAt: now, updatedAt: now });
    return { ok: true };
  },
});

// ─── Queries ────────────────────────────────────────────────────

async function rosterFor(ctx: any, shareId: Id<"projectShares">) {
  const members = await ctx.db
    .query("projectMemberships")
    .withIndex("by_share", (q: any) => q.eq("shareId", shareId))
    .collect();
  const out: any[] = [];
  for (const m of members) {
    if (m.status === "revoked") continue;
    let profile = { userId: "", fullName: m.invitedEmail ?? "Invited", email: m.invitedEmail ?? "" };
    if (m.userId) {
      const u = await ctx.db.get(m.userId);
      profile = { userId: u?.userId ?? "", fullName: u?.fullName ?? "Unknown", email: u?.email ?? "" };
    }
    out.push({
      userId: profile.userId,
      fullName: profile.fullName,
      email: profile.email,
      role: m.role,
      branch: m.branch,
      status: m.status,
      invitedAt: m.invitedAt,
      acceptedAt: m.acceptedAt,
    });
  }
  return out;
}

/** List shared projects I own and projects I'm a member of. */
export const listMine = query({
  args: { tokenHash: v.string() },
  handler: async (ctx, args) => {
    const session = await validateSessionInternal(ctx, args.tokenHash);
    if (!session) return { owned: [], joined: [] };
    const me = session.user._id;

    const ownedShares = await ctx.db
      .query("projectShares")
      .withIndex("by_owner", (q) => q.eq("ownerUserId", me))
      .filter((q) => q.eq(q.field("status"), "active"))
      .collect();
    const owned = await Promise.all(
      ownedShares.map(async (s) => ({
        shareId: s._id,
        slug: s.slug,
        repoUrl: s.repoUrl,
        defaultBranch: s.defaultBranch,
        hostKind: s.hostKind,
        hostDeviceId: s.hostDeviceId,
        payer: s.payer,
        shareCode: s.shareCode,
        createdAt: s.createdAt,
        roster: await rosterFor(ctx, s._id),
      })),
    );

    // Projects I'm a member of (not owner).
    const myMemberships = await ctx.db
      .query("projectMemberships")
      .withIndex("by_user", (q) => q.eq("userId", me))
      .collect();
    const joined: any[] = [];
    for (const m of myMemberships) {
      if (m.role === "owner" || m.status === "revoked") continue;
      const s = await ctx.db.get(m.shareId);
      if (!s || s.status !== "active") continue;
      const ownerUser = await ctx.db.get(s.ownerUserId);
      joined.push({
        shareId: s._id,
        slug: s.slug,
        repoUrl: s.repoUrl,
        defaultBranch: s.defaultBranch,
        hostKind: s.hostKind,
        hostDeviceId: s.hostDeviceId,
        role: m.role,
        branch: m.branch,
        status: m.status,
        ownerName: ownerUser?.fullName ?? "Unknown",
        ownerUserId: ownerUser?.userId ?? "",
      });
    }
    return { owned, joined };
  },
});

/** Preview a project by share code before accepting. */
export const findByCode = query({
  args: { tokenHash: v.string(), shareCode: v.string() },
  handler: async (ctx, args) => {
    const session = await validateSessionInternal(ctx, args.tokenHash);
    if (!session) return null;
    const code = args.shareCode.toUpperCase().trim();
    const share = await ctx.db
      .query("projectShares")
      .withIndex("by_shareCode", (q) => q.eq("shareCode", code))
      .first();
    if (!share || share.status !== "active") return null;
    const owner = await ctx.db.get(share.ownerUserId);

    // What role is this user invited as (if any)?
    const me = session.user._id;
    const myEmail = session.user.email.toLowerCase();
    let myMembership = await ctx.db
      .query("projectMemberships")
      .withIndex("by_share_user", (q) => q.eq("shareId", share._id).eq("userId", me))
      .first();
    if (!myMembership) {
      const all = await ctx.db
        .query("projectMemberships")
        .withIndex("by_share", (q) => q.eq("shareId", share._id))
        .collect();
      myMembership = all.find((m) => (m.invitedEmail ?? "") === myEmail) ?? null;
    }

    return {
      shareId: share._id,
      slug: share.slug,
      repoUrl: share.repoUrl,
      defaultBranch: share.defaultBranch,
      hostKind: share.hostKind,
      ownerName: owner?.fullName ?? "Unknown",
      ownerUserId: owner?.userId ?? "",
      myRole: myMembership?.role,
      myStatus: myMembership?.status,
    };
  },
});
