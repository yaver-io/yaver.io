// Privacy migrations — one-shot scripts to scrub historical data that
// pre-dates a privacy-contract tightening. Each migration is an
// `internalMutation` so it must be invoked explicitly via
// `npx convex run privacyMigrations:<name>`. None of them run on a
// cron.

import { internalMutation } from "./_generated/server";

/**
 * stripProjectPaths
 *
 * Context: before 2026-04-17 the Yaver agent shipped
 * `userProjects.path` which was the absolute filesystem path on the
 * host (e.g. `/Users/<username>/Workspace/...`). Those strings embed
 * the user's home-dir username, which leaks when Convex is read by a
 * support engineer or via a future dashboard feature. The new agent no
 * longer sends the field and the mutation strips it at the boundary,
 * but rows written by old agents still have it.
 *
 * This mutation walks every `userProjects` row in batches of 500 and
 * patches `path: undefined` so Convex drops the column.
 *
 * Run with:  npx convex run privacyMigrations:stripProjectPaths
 * Run repeatedly until it returns 0 — one call processes one batch.
 */
export const stripProjectPaths = internalMutation({
  args: {},
  handler: async (ctx) => {
    const batch = await ctx.db.query("userProjects").take(500);
    let cleaned = 0;
    for (const row of batch) {
      // `path` was removed from the schema but existing rows may
      // still carry it. `patch({ path: undefined })` is how you drop
      // an optional field in Convex.
      if ((row as Record<string, unknown>).path !== undefined) {
        await ctx.db.patch(row._id, { path: undefined } as Partial<typeof row>);
        cleaned++;
      }
    }
    return { scanned: batch.length, cleaned };
  },
});
