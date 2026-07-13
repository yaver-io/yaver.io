import { internalMutation, internalQuery } from "./_generated/server";
import { v } from "convex/values";

// ─── Queries ────────────────────────────────────────────────────

/** Get team by teamId. */
export const getByTeamId = internalQuery({
  args: { teamId: v.string() },
  handler: async (ctx, { teamId }) => {
    return await ctx.db
      .query("teams")
      .withIndex("by_teamId", (q) => q.eq("teamId", teamId))
      .first();
  },
});

/** Get all teams owned by a user. */
export const getByOwner = internalQuery({
  args: { ownerId: v.id("users") },
  handler: async (ctx, { ownerId }) => {
    return await ctx.db
      .query("teams")
      .withIndex("by_owner", (q) => q.eq("ownerId", ownerId))
      .collect();
  },
});

/** Get all teams a user is a member of. */
export const getTeamsForUser = internalQuery({
  args: { userId: v.id("users") },
  handler: async (ctx, { userId }) => {
    const memberships = await ctx.db
      .query("teamMembers")
      .withIndex("by_user", (q) => q.eq("userId", userId))
      .collect();

    const teams = await Promise.all(
      memberships.map(async (m) => {
        const team = await ctx.db
          .query("teams")
          .withIndex("by_teamId", (q) => q.eq("teamId", m.teamId))
          .first();
        return team ? { ...team, role: m.role } : null;
      })
    );

    return teams.filter(Boolean);
  },
});

/** List members of a team. */
export const listMembers = internalQuery({
  args: { teamId: v.string() },
  handler: async (ctx, { teamId }) => {
    const members = await ctx.db
      .query("teamMembers")
      .withIndex("by_team", (q) => q.eq("teamId", teamId))
      .collect();

    // Enrich with user info
    return await Promise.all(
      members.map(async (m) => {
        const user = await ctx.db.get(m.userId);
        return {
          userId: m.userId,
          role: m.role,
          joinedAt: m.joinedAt,
          email: user?.email ?? "unknown",
          fullName: user?.fullName ?? "unknown",
          provider: user?.provider,
        };
      })
    );
  },
});

/** Check if a user is a member of a team. */
export const isMember = internalQuery({
  args: { teamId: v.string(), userId: v.id("users") },
  handler: async (ctx, { teamId, userId }) => {
    const member = await ctx.db
      .query("teamMembers")
      .withIndex("by_team_user", (q) => q.eq("teamId", teamId).eq("userId", userId))
      .first();
    return member !== null;
  },
});

// ─── Mutations ──────────────────────────────────────────────────

/** Create a new team. The creator becomes the admin. */
export const create = internalMutation({
  args: {
    name: v.string(),
    ownerId: v.id("users"),
    plan: v.string(),
    maxMembers: v.number(),
  },
  handler: async (ctx, args) => {
    // Generate short team ID
    const teamId = "team_" + Math.random().toString(36).substring(2, 10);
    const now = Date.now();

    await ctx.db.insert("teams", {
      teamId,
      name: args.name,
      ownerId: args.ownerId,
      plan: args.plan,
      maxMembers: args.maxMembers,
      createdAt: now,
      updatedAt: now,
    });

    // Add owner as admin member
    await ctx.db.insert("teamMembers", {
      teamId,
      userId: args.ownerId,
      role: "admin",
      joinedAt: now,
    });

    return teamId;
  },
});

/** Invite a user to a team by email. Creates membership if user exists. */
export const addMember = internalMutation({
  args: {
    teamId: v.string(),
    userEmail: v.string(),
    role: v.optional(v.string()),
    invitedBy: v.optional(v.id("users")),
  },
  handler: async (ctx, args) => {
    const team = await ctx.db
      .query("teams")
      .withIndex("by_teamId", (q) => q.eq("teamId", args.teamId))
      .first();

    if (!team) throw new Error("Team not found");

    // Check seat limit
    const members = await ctx.db
      .query("teamMembers")
      .withIndex("by_team", (q) => q.eq("teamId", args.teamId))
      .collect();

    if (members.length >= team.maxMembers) {
      throw new Error(`Team is full (${team.maxMembers} seats)`);
    }

    // Find user by email
    const user = await ctx.db
      .query("users")
      .withIndex("by_email", (q) => q.eq("email", args.userEmail))
      .first();

    if (!user) {
      throw new Error("User not found. They need to sign up at yaver.io first.");
    }

    // Check if already a member
    const existing = await ctx.db
      .query("teamMembers")
      .withIndex("by_team_user", (q) => q.eq("teamId", args.teamId).eq("userId", user._id))
      .first();

    if (existing) {
      throw new Error("User is already a team member");
    }

    await ctx.db.insert("teamMembers", {
      teamId: args.teamId,
      userId: user._id,
      role: args.role ?? "member",
      invitedBy: args.invitedBy,
      joinedAt: Date.now(),
    });

    return { userId: user._id, email: user.email };
  },
});

/** Remove a member from a team. */
export const removeMember = internalMutation({
  args: {
    teamId: v.string(),
    userId: v.id("users"),
  },
  handler: async (ctx, { teamId, userId }) => {
    const member = await ctx.db
      .query("teamMembers")
      .withIndex("by_team_user", (q) => q.eq("teamId", teamId).eq("userId", userId))
      .first();

    if (!member) throw new Error("Member not found");

    // Don't allow removing the last admin
    if (member.role === "admin") {
      const admins = await ctx.db
        .query("teamMembers")
        .withIndex("by_team", (q) => q.eq("teamId", teamId))
        .filter((q) => q.eq(q.field("role"), "admin"))
        .collect();

      if (admins.length <= 1) {
        throw new Error("Cannot remove the last admin");
      }
    }

    await ctx.db.delete(member._id);
  },
});

/** Update team info. */
export const update = internalMutation({
  args: {
    teamId: v.string(),
    name: v.optional(v.string()),
    maxMembers: v.optional(v.number()),
    subscriptionId: v.optional(v.id("subscriptions")),
  },
  handler: async (ctx, args) => {
    const team = await ctx.db
      .query("teams")
      .withIndex("by_teamId", (q) => q.eq("teamId", args.teamId))
      .first();

    if (!team) throw new Error("Team not found");

    const updates: Record<string, unknown> = { updatedAt: Date.now() };
    if (args.name !== undefined) updates.name = args.name;
    if (args.maxMembers !== undefined) updates.maxMembers = args.maxMembers;
    if (args.subscriptionId !== undefined) updates.subscriptionId = args.subscriptionId;

    await ctx.db.patch(team._id, updates);
  },
});
