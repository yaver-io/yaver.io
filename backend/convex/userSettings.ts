import { mutation, query } from "./_generated/server";
import { v } from "convex/values";
import { validateSessionInternal, randomHex } from "./auth";

// Shared validator for the per-subsystem managed toggle. Each field
// accepts boolean (true=Yaver-managed, false=self-hosted) or null
// (explicit clear). Omitting a key leaves its stored value untouched.
// Adding a new subsystem here + in schema.ts is the only place a
// developer touches to surface a new toggle across every Yaver UI.
const managedPatchValidator = v.object({
  relay:     v.optional(v.union(v.boolean(), v.null())),
  dns:       v.optional(v.union(v.boolean(), v.null())),
  analytics: v.optional(v.union(v.boolean(), v.null())),
  storage:   v.optional(v.union(v.boolean(), v.null())),
  email:     v.optional(v.union(v.boolean(), v.null())),
  ci:        v.optional(v.union(v.boolean(), v.null())),
  voice:     v.optional(v.union(v.boolean(), v.null())),
  llm:       v.optional(v.union(v.boolean(), v.null())),
});

// mergeManagedPatch applies a caller's patch to the existing managed
// object. Booleans win; nulls clear; undefined keeps the previous
// value. Returns the new object with empty keys elided so we don't
// persist fields the user never touched.
function mergeManagedPatch(
  existing: Record<string, boolean | undefined> | undefined,
  patch: Record<string, boolean | null | undefined>,
): Record<string, boolean> | undefined {
  const merged: Record<string, boolean> = {};
  for (const [k, v] of Object.entries(existing ?? {})) {
    if (typeof v === "boolean") merged[k] = v;
  }
  for (const [k, v] of Object.entries(patch)) {
    if (v === null) {
      delete merged[k];
    } else if (typeof v === "boolean") {
      merged[k] = v;
    }
  }
  return Object.keys(merged).length === 0 ? undefined : merged;
}

// normalizeOwnedDeviceId enforces the backend invariant for any
// elevated-slot device pointer (primary, secondary):
//   - exactly zero or one slot value per user
//   - any non-empty slot value must point at a device row owned by
//     that same user
//
// The "only one" part is structural because userSettings stores a
// single scalar per slot, not a per-device flag. The slot label feeds
// the error message so callers can tell which field tripped.
async function normalizeOwnedDeviceId(
  ctx: any,
  userId: string,
  deviceId: string | null | undefined,
  slot: "primaryDeviceId" | "secondaryDeviceId",
): Promise<string | undefined> {
  if (deviceId === undefined) {
    return undefined;
  }
  const next = deviceId ?? undefined;
  if (!next) {
    return undefined;
  }
  const device = await ctx.db
    .query("devices")
    .withIndex("by_deviceId", (q: any) => q.eq("deviceId", next))
    .first();
  if (!device || device.userId !== userId) {
    throw new Error(`${slot} must refer to one of the caller's devices`);
  }
  return next;
}

export const get = query({
  args: { userId: v.id("users") },
  handler: async (ctx, args) => {
    return await ctx.db
      .query("userSettings")
      .withIndex("by_userId", (q) => q.eq("userId", args.userId))
      .first();
  },
});

/** Get settings by auth token (used from HTTP endpoints). */
export const getByToken = query({
  args: { tokenHash: v.string() },
  handler: async (ctx, args) => {
    const session = await validateSessionInternal(ctx, args.tokenHash);
    if (!session) return null;
    return await ctx.db
      .query("userSettings")
      .withIndex("by_userId", (q) => q.eq("userId", session.user._id))
      .first();
  },
});

export const set = mutation({
  args: {
    userId: v.id("users"),
    forceRelay: v.optional(v.boolean()),
    runnerId: v.optional(v.string()),
    customRunnerCommand: v.optional(v.string()),
    relayUrl: v.optional(v.string()),
    relayPassword: v.optional(v.string()),
    tunnelUrl: v.optional(v.string()),
    speechProvider: v.optional(v.string()),
    speechApiKey: v.optional(v.string()),
    ttsEnabled: v.optional(v.boolean()),
    verbosity: v.optional(v.number()),
    keyStorage: v.optional(v.string()),
    // null sentinel = clear the preference; undefined = leave untouched.
    primaryDeviceId: v.optional(v.union(v.string(), v.null())),
    secondaryDeviceId: v.optional(v.union(v.string(), v.null())),
    // Set or clear the primary runner for a single device. The whole
    // primaryRunnerByDevice list lives on the userSettings row, but
    // mutations only ever touch one entry at a time so the wire shape
    // stays small. runnerId=null clears the entry for that device.
    primaryRunnerForDevice: v.optional(
      v.object({
        deviceId: v.string(),
        runnerId: v.union(v.string(), v.null()),
        // Optional model hint. null clears just the model (keeps the
        // runner selection). undefined leaves the existing model alone.
        model: v.optional(v.union(v.string(), v.null())),
        // Optional runner sub-selection. Used by OpenCode's
        // `--agent <mode>` path. null clears the saved mode.
        mode: v.optional(v.union(v.string(), v.null())),
        // Optional provider hint such as "zai" / "glm" / "ollama".
        // Secrets remain host-local; this only remembers the user's
        // preference across surfaces.
        provider: v.optional(v.union(v.string(), v.null())),
      }),
    ),
    // Per-subsystem managed: true (Yaver-hosted) | false (user-hosted)
    // | null (unset → use legacy default). Clients send only the
    // subsystem(s) they're changing; unspecified keys retain their
    // existing value. Null on any key clears that subsystem.
    managed: v.optional(managedPatchValidator),
  },
  handler: async (ctx, args) => {
    const normalizedPrimaryDeviceId = await normalizeOwnedDeviceId(
      ctx,
      args.userId,
      args.primaryDeviceId,
      "primaryDeviceId",
    );
    const normalizedSecondaryDeviceId = await normalizeOwnedDeviceId(
      ctx,
      args.userId,
      args.secondaryDeviceId,
      "secondaryDeviceId",
    );
    const existing = await ctx.db
      .query("userSettings")
      .withIndex("by_userId", (q) => q.eq("userId", args.userId))
      .first();
    // Only include fields that were explicitly provided — undefined fields must NOT
    // overwrite existing values (e.g. relayUrl/relayPassword set during signup).
    const patch: Record<string, unknown> = {};
    if (args.forceRelay !== undefined) patch.forceRelay = args.forceRelay;
    if (args.runnerId !== undefined) patch.runnerId = args.runnerId;
    if (args.customRunnerCommand !== undefined) patch.customRunnerCommand = args.customRunnerCommand;
    if (args.relayUrl !== undefined) patch.relayUrl = args.relayUrl;
    if (args.relayPassword !== undefined) patch.relayPassword = args.relayPassword;
    if (args.tunnelUrl !== undefined) patch.tunnelUrl = args.tunnelUrl;
    if (args.speechProvider !== undefined) patch.speechProvider = args.speechProvider;
    if (args.speechApiKey !== undefined) patch.speechApiKey = args.speechApiKey;
    if (args.ttsEnabled !== undefined) patch.ttsEnabled = args.ttsEnabled;
    if (args.verbosity !== undefined) patch.verbosity = args.verbosity;
    if (args.keyStorage !== undefined) patch.keyStorage = args.keyStorage;
    if (args.primaryDeviceId !== undefined) {
      patch.primaryDeviceId = normalizedPrimaryDeviceId;
    }
    if (args.secondaryDeviceId !== undefined) {
      patch.secondaryDeviceId = normalizedSecondaryDeviceId;
    }
    if (args.primaryRunnerForDevice !== undefined) {
      const cur = (existing?.primaryRunnerByDevice ?? []) as Array<{ deviceId: string; runnerId: string; model?: string; mode?: string; provider?: string }>;
      const payload = args.primaryRunnerForDevice;
      const filtered = cur.filter((row) => row.deviceId !== payload.deviceId);
      let next = filtered;
      if (payload.runnerId) {
        // Resolve the effective model: explicit string → use it; null →
        // clear any existing model on that device; undefined → preserve
        // the current model if the runner is unchanged.
        const prevRow = cur.find((row) => row.deviceId === payload.deviceId);
        let model: string | undefined;
        if (payload.model === null) {
          model = undefined;
        } else if (payload.model !== undefined) {
          model = payload.model;
        } else if (prevRow?.runnerId === payload.runnerId) {
          model = prevRow.model;
        }
        let mode: string | undefined;
        if (payload.mode === null) {
          mode = undefined;
        } else if (payload.mode !== undefined) {
          mode = payload.mode;
        } else if (prevRow?.runnerId === payload.runnerId) {
          mode = prevRow.mode;
        }
        let provider: string | undefined;
        if (payload.provider === null) {
          provider = undefined;
        } else if (payload.provider !== undefined) {
          provider = payload.provider;
        } else if (prevRow?.runnerId === payload.runnerId) {
          provider = prevRow.provider;
        }
        const row: { deviceId: string; runnerId: string; model?: string; mode?: string; provider?: string } = {
          deviceId: payload.deviceId,
          runnerId: payload.runnerId,
        };
        if (model) row.model = model;
        if (mode) row.mode = mode;
        if (provider) row.provider = provider;
        next = [...filtered, row];
      }
      patch.primaryRunnerByDevice = next.length > 0 ? next : undefined;
    }
    if (args.managed !== undefined) {
      patch.managed = mergeManagedPatch(
        existing?.managed as Record<string, boolean | undefined> | undefined,
        args.managed as Record<string, boolean | null | undefined>,
      );
    }
    if (existing) {
      await ctx.db.patch(existing._id, patch);
    } else {
      await ctx.db.insert("userSettings", {
        userId: args.userId,
        ...patch,
      });
    }
  },
});

/** Set settings by auth token (used from HTTP endpoints). */
export const setByToken = mutation({
  args: {
    tokenHash: v.string(),
    forceRelay: v.optional(v.boolean()),
    runnerId: v.optional(v.string()),
    customRunnerCommand: v.optional(v.string()),
    relayUrl: v.optional(v.string()),
    relayPassword: v.optional(v.string()),
    tunnelUrl: v.optional(v.string()),
    speechProvider: v.optional(v.string()),
    speechApiKey: v.optional(v.string()),
    ttsEnabled: v.optional(v.boolean()),
    verbosity: v.optional(v.number()),
    keyStorage: v.optional(v.string()),
    primaryDeviceId: v.optional(v.union(v.string(), v.null())),
    secondaryDeviceId: v.optional(v.union(v.string(), v.null())),
    primaryRunnerForDevice: v.optional(
      v.object({
        deviceId: v.string(),
        runnerId: v.union(v.string(), v.null()),
        // Optional model hint. null clears just the model (keeps the
        // runner selection). undefined leaves the existing model alone.
        model: v.optional(v.union(v.string(), v.null())),
        mode: v.optional(v.union(v.string(), v.null())),
        provider: v.optional(v.union(v.string(), v.null())),
      }),
    ),
    managed: v.optional(managedPatchValidator),
  },
  handler: async (ctx, args) => {
    const session = await validateSessionInternal(ctx, args.tokenHash);
    if (!session) throw new Error("Unauthorized");
    const userId = session.user._id;
    const normalizedPrimaryDeviceId = await normalizeOwnedDeviceId(
      ctx,
      userId,
      args.primaryDeviceId,
      "primaryDeviceId",
    );
    const normalizedSecondaryDeviceId = await normalizeOwnedDeviceId(
      ctx,
      userId,
      args.secondaryDeviceId,
      "secondaryDeviceId",
    );
    const existing = await ctx.db
      .query("userSettings")
      .withIndex("by_userId", (q) => q.eq("userId", userId))
      .first();
    // Only include fields that were explicitly provided — undefined fields must NOT
    // overwrite existing values (e.g. relayUrl/relayPassword set during signup).
    const patch: Record<string, unknown> = {};
    if (args.forceRelay !== undefined) patch.forceRelay = args.forceRelay;
    if (args.runnerId !== undefined) patch.runnerId = args.runnerId;
    if (args.customRunnerCommand !== undefined) patch.customRunnerCommand = args.customRunnerCommand;
    if (args.relayUrl !== undefined) patch.relayUrl = args.relayUrl;
    if (args.relayPassword !== undefined) patch.relayPassword = args.relayPassword;
    if (args.tunnelUrl !== undefined) patch.tunnelUrl = args.tunnelUrl;
    if (args.speechProvider !== undefined) patch.speechProvider = args.speechProvider;
    if (args.speechApiKey !== undefined) patch.speechApiKey = args.speechApiKey;
    if (args.ttsEnabled !== undefined) patch.ttsEnabled = args.ttsEnabled;
    if (args.verbosity !== undefined) patch.verbosity = args.verbosity;
    if (args.keyStorage !== undefined) patch.keyStorage = args.keyStorage;
    if (args.primaryDeviceId !== undefined) {
      patch.primaryDeviceId = normalizedPrimaryDeviceId;
    }
    if (args.secondaryDeviceId !== undefined) {
      patch.secondaryDeviceId = normalizedSecondaryDeviceId;
    }
    if (args.primaryRunnerForDevice !== undefined) {
      const cur = (existing?.primaryRunnerByDevice ?? []) as Array<{ deviceId: string; runnerId: string; model?: string; mode?: string; provider?: string }>;
      const payload = args.primaryRunnerForDevice;
      const filtered = cur.filter((row) => row.deviceId !== payload.deviceId);
      let next = filtered;
      if (payload.runnerId) {
        // Resolve the effective model: explicit string → use it; null →
        // clear any existing model on that device; undefined → preserve
        // the current model if the runner is unchanged.
        const prevRow = cur.find((row) => row.deviceId === payload.deviceId);
        let model: string | undefined;
        if (payload.model === null) {
          model = undefined;
        } else if (payload.model !== undefined) {
          model = payload.model;
        } else if (prevRow?.runnerId === payload.runnerId) {
          model = prevRow.model;
        }
        let mode: string | undefined;
        if (payload.mode === null) {
          mode = undefined;
        } else if (payload.mode !== undefined) {
          mode = payload.mode;
        } else if (prevRow?.runnerId === payload.runnerId) {
          mode = prevRow.mode;
        }
        let provider: string | undefined;
        if (payload.provider === null) {
          provider = undefined;
        } else if (payload.provider !== undefined) {
          provider = payload.provider;
        } else if (prevRow?.runnerId === payload.runnerId) {
          provider = prevRow.provider;
        }
        const row: { deviceId: string; runnerId: string; model?: string; mode?: string; provider?: string } = {
          deviceId: payload.deviceId,
          runnerId: payload.runnerId,
        };
        if (model) row.model = model;
        if (mode) row.mode = mode;
        if (provider) row.provider = provider;
        next = [...filtered, row];
      }
      patch.primaryRunnerByDevice = next.length > 0 ? next : undefined;
    }
    if (args.managed !== undefined) {
      patch.managed = mergeManagedPatch(
        existing?.managed as Record<string, boolean | undefined> | undefined,
        args.managed as Record<string, boolean | null | undefined>,
      );
    }
    if (existing) {
      await ctx.db.patch(existing._id, patch);
    } else {
      await ctx.db.insert("userSettings", {
        userId,
        ...patch,
      });
    }
  },
});

/** Admin: set settings by email (for manual user configuration). */
export const setByEmail = mutation({
  args: {
    email: v.string(),
    speechProvider: v.optional(v.string()),
    speechApiKey: v.optional(v.string()),
    ttsEnabled: v.optional(v.boolean()),
    verbosity: v.optional(v.number()),
    keyStorage: v.optional(v.string()),
    forceRelay: v.optional(v.boolean()),
    runnerId: v.optional(v.string()),
    customRunnerCommand: v.optional(v.string()),
    relayUrl: v.optional(v.string()),
    relayPassword: v.optional(v.string()),
    tunnelUrl: v.optional(v.string()),
  },
  handler: async (ctx, args) => {
    const user = await ctx.db
      .query("users")
      .filter((q) => q.eq(q.field("email"), args.email))
      .first();
    if (!user) throw new Error(`User not found: ${args.email}`);
    const existing = await ctx.db
      .query("userSettings")
      .withIndex("by_userId", (q) => q.eq("userId", user._id))
      .first();
    const { email: _, ...fields } = args;
    if (existing) {
      await ctx.db.patch(existing._id, fields);
    } else {
      await ctx.db.insert("userSettings", { userId: user._id, ...fields });
    }
    return { ok: true, userId: user._id };
  },
});

/**
 * Seed default settings for all users who don't have settings yet.
 * Also generates per-user relay passwords and sets relayUrl for users missing them.
 * Run once: npx convex run userSettings:seedDefaults
 */
export const seedDefaults = mutation({
  args: {},
  handler: async (ctx) => {
    // Fetch default relay URL from platform config
    const config = await ctx.db
      .query("platformConfig")
      .withIndex("by_key", (q) => q.eq("key", "relay_servers"))
      .unique();
    let defaultRelayUrl: string | undefined;
    let defaultRelayPassword: string | undefined;
    if (config?.value) {
      try {
        const relays = JSON.parse(config.value);
        if (Array.isArray(relays) && relays.length > 0) {
          defaultRelayUrl = relays[0].httpUrl;
          defaultRelayPassword = relays[0].password;
        }
      } catch { /* ignore */ }
    }

    const allUsers = await ctx.db.query("users").collect();
    let seeded = 0;
    let updated = 0;
    for (const user of allUsers) {
      const existing = await ctx.db
        .query("userSettings")
        .withIndex("by_userId", (q) => q.eq("userId", user._id))
        .first();
      if (!existing) {
        await ctx.db.insert("userSettings", {
          userId: user._id,
          forceRelay: false,
          relayUrl: defaultRelayUrl,
          relayPassword: defaultRelayPassword,
        });
        seeded++;
      } else if (existing.relayPassword !== defaultRelayPassword || existing.relayUrl !== defaultRelayUrl) {
        // Sync relay config to match platform config
        const patch: Record<string, unknown> = {};
        if (defaultRelayPassword && existing.relayPassword !== defaultRelayPassword) {
          patch.relayPassword = defaultRelayPassword;
        }
        if (defaultRelayUrl && existing.relayUrl !== defaultRelayUrl) {
          patch.relayUrl = defaultRelayUrl;
        }
        if (Object.keys(patch).length > 0) {
          await ctx.db.patch(existing._id, patch);
          updated++;
        }
      }
    }
    return { seeded, updated, total: allUsers.length };
  },
});

/**
 * Repair the caller's userSettings row so the relay password matches
 * the current platform-managed value. Used by the web dashboard when
 * the preview iframe keeps getting 401 "invalid relay password" from
 * the managed relay — typically because the row's `relayPassword` was
 * seeded before a rotation, or (fresh-install race) never got written
 * at all.
 *
 * Safe by design: only rewrites with the CURRENT platform default (same
 * value every other synced user has). Never generates a random secret.
 * If the platform config has no password configured, this is a no-op
 * and returns `repaired:false reason:"no platform default"`.
 */
export const repairRelayPassword = mutation({
  args: { tokenHash: v.string() },
  handler: async (ctx, args) => {
    const session = await validateSessionInternal(ctx, args.tokenHash);
    if (!session) {
      return { ok: false, repaired: false, reason: "unauthorized" };
    }

    let defaultRelayUrl: string | undefined;
    let defaultRelayPassword: string | undefined;
    const config = await ctx.db
      .query("platformConfig")
      .withIndex("by_key", (q) => q.eq("key", "relay_servers"))
      .unique();
    if (config?.value) {
      try {
        const relays = JSON.parse(config.value);
        if (Array.isArray(relays) && relays.length > 0) {
          defaultRelayUrl = relays[0].httpUrl || undefined;
          defaultRelayPassword = relays[0].password || undefined;
        }
      } catch { /* ignore */ }
    }
    if (!defaultRelayPassword) {
      return { ok: true, repaired: false, reason: "no platform default" };
    }

    const existing = await ctx.db
      .query("userSettings")
      .withIndex("by_userId", (q) => q.eq("userId", session.user._id))
      .first();
    if (!existing) {
      await ctx.db.insert("userSettings", {
        userId: session.user._id,
        forceRelay: false,
        relayUrl: defaultRelayUrl,
        relayPassword: defaultRelayPassword,
      });
      return { ok: true, repaired: true, reason: "seeded missing settings" };
    }

    if (existing.relayPassword === defaultRelayPassword) {
      return { ok: true, repaired: false, reason: "already in sync" };
    }

    const patch: Record<string, unknown> = { relayPassword: defaultRelayPassword };
    if (defaultRelayUrl && existing.relayUrl !== defaultRelayUrl) {
      patch.relayUrl = defaultRelayUrl;
    }
    await ctx.db.patch(existing._id, patch);
    return { ok: true, repaired: true, reason: "synced to platform default" };
  },
});

/**
 * Validate a relay password — checks if any user has this relayPassword.
 * Called by relay servers via POST /relay/validate to authenticate per-user passwords.
 */
export const validateRelayPassword = query({
  args: { password: v.string() },
  handler: async (ctx, args) => {
    if (!args.password) return null;
    const allSettings = await ctx.db.query("userSettings").collect();
    const match = allSettings.find((s) => s.relayPassword === args.password);
    if (!match) return null;
    return { userId: match.userId };
  },
});

/**
 * Migrate all existing users to forceRelay: false.
 * Run once: npx convex run userSettings:migrateForceRelayOff
 */
export const migrateForceRelayOff = mutation({
  args: {},
  handler: async (ctx) => {
    const allSettings = await ctx.db.query("userSettings").collect();
    let updated = 0;
    for (const settings of allSettings) {
      if (settings.forceRelay === true || settings.forceRelay === undefined) {
        await ctx.db.patch(settings._id, { forceRelay: false });
        updated++;
      }
    }
    return { updated, total: allSettings.length };
  },
});
