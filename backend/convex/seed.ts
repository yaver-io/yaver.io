import { mutation, internalMutation } from "./_generated/server";
import { PREDEFINED_RUNNERS } from "./aiRunners";
import { PREDEFINED_MODELS } from "./aiModels";
import { providerCatalogDefaults } from "./providerCatalog";

/**
 * Unified seed mutation — bootstraps a fresh Convex instance with all predefined data.
 *
 * Run with: npx convex run seed:all
 *
 * This seeds:
 * - AI runners (Claude Code, Codex, Aider, Custom)
 * - AI models per runner (Sonnet, Opus, Haiku, o3-mini, etc.)
 * - Default platform config values
 *
 * All seed data is idempotent — safe to run multiple times.
 * It will update existing records and insert missing ones.
 *
 * Seed data vs user data:
 * - Seed data (runners, models, config): predefined, shared across all instances
 * - User data (users, sessions, devices, metrics): created at runtime, never seeded
 */
export const all = mutation({
  args: {},
  handler: async (ctx) => {
    // ── AI Runners ──
    let runnersCreated = 0;
    let runnersUpdated = 0;
    for (const runner of PREDEFINED_RUNNERS) {
      const existing = await ctx.db
        .query("aiRunners")
        .withIndex("by_runnerId", (q) => q.eq("runnerId", runner.runnerId))
        .first();
      if (existing) {
        await ctx.db.patch(existing._id, runner);
        runnersUpdated++;
      } else {
        await ctx.db.insert("aiRunners", runner);
        runnersCreated++;
      }
    }

    // ── AI Models ──
    let modelsCreated = 0;
    let modelsUpdated = 0;
    for (const model of PREDEFINED_MODELS) {
      const existing = await ctx.db
        .query("aiModels")
        .withIndex("by_modelId", (q) =>
          q.eq("modelId", model.modelId).eq("runnerId", model.runnerId)
        )
        .first();
      if (existing) {
        await ctx.db.patch(existing._id, model);
        modelsUpdated++;
      } else {
        await ctx.db.insert("aiModels", model);
        modelsCreated++;
      }
    }

    // ── Default Platform Config ──
    const defaults: Record<string, string> = {
      relay_servers: JSON.stringify([{
        id: "public-free",
        quicAddr: "relay.example.com:4433",
        httpUrl: "https://public.yaver.io",
        region: "eu",
        priority: 1,
        label: "Free (EU)",
      }]),
      cli_version: "0.0.0",
      mobile_version: "0.0.0",
      relay_version: "0.0.0",
      web_version: "0.0.0",
      backend_version: "0.0.0",
      ...providerCatalogDefaults(),
    };

    let configCreated = 0;
    for (const [key, value] of Object.entries(defaults)) {
      const existing = await ctx.db
        .query("platformConfig")
        .withIndex("by_key", (q) => q.eq("key", key))
        .first();
      if (!existing) {
        await ctx.db.insert("platformConfig", {
          key,
          value,
          updatedAt: Date.now(),
        });
        configCreated++;
      }
      // Don't overwrite existing config — only set defaults for missing keys
    }

    return {
      runners: { created: runnersCreated, updated: runnersUpdated },
      models: { created: modelsCreated, updated: modelsUpdated },
      config: { created: configCreated },
    };
  },
});

/**
 * Clear all user/session/device data. Keeps platform data intact.
 *
 * Run with: npx convex run seed:clearUserData
 *
 * KEEPS: aiRunners, aiModels, platformConfig, downloads
 * DELETES: users, sessions, pendingAuth, deviceCodes, devices,
 *          deviceMetrics, deviceEvents, userSettings, developerSurveys,
 *          runnerUsage, dailyTaskCounts, authLogs, developerLogs,
 *          mobileStreamLogs
 */
export const clearUserData = mutation({
  args: {},
  handler: async (ctx) => {
    const tablesToClear = [
      "users",
      "sessions",
      "pendingAuth",
      "deviceCodes",
      "devices",
      "deviceMetrics",
      "deviceEvents",
      "userSettings",
      "developerSurveys",
      "runnerUsage",
      "dailyTaskCounts",
      "authLogs",
      "developerLogs",
      "mobileStreamLogs",
    ] as const;

    const results: Record<string, number> = {};

    for (const table of tablesToClear) {
      let count = 0;
      const docs = await ctx.db.query(table).collect();
      for (const doc of docs) {
        await ctx.db.delete(doc._id);
        count++;
      }
      results[table] = count;
    }

    return results;
  },
});
