import { mutation, query } from "./_generated/server";
import { v } from "convex/values";
import { validateSessionInternal } from "./auth";

// User-defined one-tap shortcuts for the mobile Shortcuts tab. A shortcut
// is an ordered chain of deterministic actions executed client-side on the
// phone (connect → open project → Hermes reload → …).
//
// PRIVACY: rows store ONLY deviceId (uuid) + project slug + flags + UI
// labels. No absolute paths, no task-prompt text — the same contract as
// userProjects. The validator below is the enforced boundary; if you add a
// field, make sure it can never carry a path or a prompt, and update
// desktop/agent/convex_privacy_test.go.
const stepValidator = v.object({
  kind: v.string(),
  deviceId: v.optional(v.string()),
  deviceName: v.optional(v.string()),
  projectSlug: v.optional(v.string()),
  mode: v.optional(v.string()),
  framework: v.optional(v.string()),
  label: v.optional(v.string()),
});

/** List the caller's shortcuts, ordered for the grid. */
export const listByToken = query({
  args: { tokenHash: v.string() },
  handler: async (ctx, args) => {
    const session = await validateSessionInternal(ctx, args.tokenHash);
    if (!session) return [];
    return await ctx.db
      .query("userShortcuts")
      .withIndex("by_user", (q) => q.eq("userId", session.user._id))
      .collect();
  },
});

/** Create or update one shortcut. `id` present = update (ownership
 *  checked); absent = insert. Returns the row id. */
export const upsertByToken = mutation({
  args: {
    tokenHash: v.string(),
    id: v.optional(v.id("userShortcuts")),
    name: v.string(),
    icon: v.optional(v.string()),
    color: v.optional(v.string()),
    order: v.optional(v.number()),
    steps: v.array(stepValidator),
  },
  handler: async (ctx, args) => {
    const session = await validateSessionInternal(ctx, args.tokenHash);
    if (!session) throw new Error("Unauthorized");
    const userId = session.user._id;
    const now = Date.now();

    if (args.id) {
      const existing = await ctx.db.get(args.id);
      if (!existing || existing.userId !== userId) {
        throw new Error("Shortcut not found");
      }
      await ctx.db.patch(args.id, {
        name: args.name,
        icon: args.icon,
        color: args.color,
        ...(args.order !== undefined ? { order: args.order } : {}),
        steps: args.steps,
        updatedAt: now,
      });
      return args.id;
    }

    // New shortcut — append to the end of the grid if no order given.
    let order = args.order;
    if (order === undefined) {
      const all = await ctx.db
        .query("userShortcuts")
        .withIndex("by_user", (q) => q.eq("userId", userId))
        .collect();
      order = all.reduce((max, s) => Math.max(max, s.order ?? 0), -1) + 1;
    }
    return await ctx.db.insert("userShortcuts", {
      userId,
      name: args.name,
      icon: args.icon,
      color: args.color,
      order,
      steps: args.steps,
      updatedAt: now,
    });
  },
});

/** Delete one of the caller's shortcuts. No-op if it isn't theirs. */
export const deleteByToken = mutation({
  args: { tokenHash: v.string(), id: v.id("userShortcuts") },
  handler: async (ctx, args) => {
    const session = await validateSessionInternal(ctx, args.tokenHash);
    if (!session) throw new Error("Unauthorized");
    const existing = await ctx.db.get(args.id);
    if (existing && existing.userId === session.user._id) {
      await ctx.db.delete(args.id);
    }
    return { ok: true };
  },
});
