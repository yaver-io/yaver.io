import { v } from "convex/values";
import { internalQuery, internalMutation } from "./_generated/server";

/**
 * Platform-level configuration managed by Yaver (not by customers).
 * Stores relay server list and other infrastructure config.
 *
 * Key configs:
 *   "relay_servers" — JSON array of relay servers:
 *     [
 *       {"id":"relay1","quicAddr":"<your-ip>:4433","httpUrl":"http://<your-ip>:8443","region":"eu","priority":1},
 *       {"id":"relay2","quicAddr":"<your-ip>:4433","httpUrl":"http://<your-ip>:8443","region":"us","priority":2}
 *     ]
 *   Clients connect to all available relays for redundancy.
 *   If one goes down, traffic automatically routes through others.
 */

/** Get a config value by key. */
export const get = internalQuery({
  args: { key: v.string() },
  handler: async (ctx, args) => {
    const config = await ctx.db
      .query("platformConfig")
      .withIndex("by_key", (q) => q.eq("key", args.key))
      .unique();
    return config?.value ?? null;
  },
});

/** Get all config values needed by clients (relay servers, etc.). */
export const getClientConfig = internalQuery({
  args: {},
  handler: async (ctx) => {
    const configs = await ctx.db.query("platformConfig").collect();
    const result: Record<string, string> = {};
    for (const c of configs) {
      result[c.key] = c.value;
    }
    return result;
  },
});

/**
 * Set a config value.
 * This mutation is only callable from the Convex dashboard or via `npx convex run`.
 * It is NOT exposed via any HTTP endpoint, so users who clone this repo
 * cannot modify platform config from the client side.
 */
export const set = internalMutation({
  args: {
    key: v.string(),
    value: v.string(),
  },
  handler: async (ctx, args) => {
    const existing = await ctx.db
      .query("platformConfig")
      .withIndex("by_key", (q) => q.eq("key", args.key))
      .unique();

    if (existing) {
      await ctx.db.patch(existing._id, {
        value: args.value,
        updatedAt: Date.now(),
      });
    } else {
      await ctx.db.insert("platformConfig", {
        key: args.key,
        value: args.value,
        updatedAt: Date.now(),
      });
    }
  },
});
