import { v } from "convex/values";
import { mutation, query } from "./_generated/server";
import { Doc } from "./_generated/dataModel";
import { validateSessionInternal } from "./auth";
import {
  getLegacyGuestAccess,
  listActiveInfraGrantsForGuest,
  listGrantedDeviceIdsForGrant,
} from "./access";
import { recommendPlacement } from "./edgePlacement";

// HEARTBEAT_STALE_MS: how long after the last heartbeat we still
// trust the device's `isOnline` flag. The agent beats every 30 s,
// so 90 s is "missed three beats" — far less likely than network
// jitter. Without this server-side gate, a SIGKILL'd / power-cut /
// wifi-dropped agent looks online forever (the flag never gets
// downgraded by the markOffline mutation that the dying process
// can't run). Mobile / web read this derived value via the listing
// queries and the device card stops flickering.
const HEARTBEAT_STALE_MS = 90 * 1000;

/**
 * deriveIsOnline returns the user-visible online state, reconciling
 * the explicit isOnline flag with heartbeat freshness. Use this
 * everywhere a query returns isOnline to clients.
 */
function deriveIsOnline(d: { isOnline: boolean; lastHeartbeat: number }): boolean {
  if (!d.isOnline) return false;
  const age = Date.now() - d.lastHeartbeat;
  return age < HEARTBEAT_STALE_MS;
}

type ListedDevice = {
  deviceId: string;
  name: string;
  platform: string;
  publicKey?: string;
  hardwareId?: string;
  quicHost: string;
  localIps: string[];
  publicEndpoints: string[];
  quicPort: number;
  isOnline: boolean;
  needsAuth: boolean;
  runnerDown: boolean;
  runners: Doc<"devices">["runners"];
  lastHeartbeat: number;
  isGuest: boolean;
  hostUserId?: string;
  hostName?: string;
  hostEmail?: string;
  hostUserIdString?: string;
  accessScope: "owner" | "shared-scoped" | "shared-legacy";
  tunnelUrl?: string;
  priorityMode?: string;
  useHostApiKeys?: boolean;
  allowGuestProvidedApiKeys?: boolean;
  sharedWithGuests?: boolean;
  sharesAllProjects?: boolean;
  sharedProjects?: string[];
  sharesAllRunners?: boolean;
  sharedRunners?: string[];
  sessionBinding?: "dedicated" | "legacy-shared";
  deviceClass?: "desktop" | "edge-mobile" | "server";
  edgeProfile?: Doc<"devices">["edgeProfile"];
};

type ShareRule = {
  allowedProjects?: string[];
  allowedRunners?: string[];
};

function normalizeDeviceName(name: string | undefined): string {
  return String(name || "").trim().toLowerCase().replace(/\.local$/i, "");
}

function normalizeDeviceHost(host: string | undefined): string {
  return String(host || "").trim().toLowerCase().replace(/\.local$/i, "");
}

function listedDeviceIdentityKey(device: ListedDevice): string {
  if (device.hardwareId) return `hwid:${device.hardwareId}`;
  if (device.publicKey) return `pub:${device.publicKey}`;
  if (device.isGuest) {
    const scope = device.hostEmail || device.hostName || "guest";
    return `guest:${scope}:${device.deviceId || device.name}`;
  }
  const normalizedName = normalizeDeviceName(device.name);
  const normalizedPlatform = String(device.platform || "").trim().toLowerCase();
  if (normalizedName && normalizedPlatform) return `host:${normalizedPlatform}:${normalizedName}`;
  if (device.deviceId) return `id:${device.deviceId}`;
  return `name:${device.name}`;
}

function listedDeviceAliasKey(device: ListedDevice): string | null {
  if (device.isGuest) return null;
  const normalizedName = normalizeDeviceName(device.name);
  const normalizedPlatform = String(device.platform || "").trim().toLowerCase();
  if (!normalizedName || !normalizedPlatform) return null;
  return `${normalizedPlatform}:${normalizedName}`;
}

function listedDeviceEndpointKey(device: ListedDevice): string | null {
  if (device.isGuest) return null;
  const normalizedHost = normalizeDeviceHost(device.quicHost);
  if (!normalizedHost) return null;
  return `${normalizedHost}:${device.quicPort || 0}`;
}

function mergeListedDevices(a: ListedDevice, b: ListedDevice): ListedDevice {
  const incomingWins =
    (!!a.needsAuth && !b.needsAuth) ||
    (b.lastHeartbeat || 0) > (a.lastHeartbeat || 0) ||
    (!!b.isOnline && !a.isOnline);
  const base = incomingWins ? b : a;
  const other = incomingWins ? a : b;
  return {
    ...other,
    ...base,
    quicHost: base.quicHost || other.quicHost,
    quicPort: base.quicPort || other.quicPort,
    isOnline: base.isOnline || other.isOnline,
    runnerDown: base.runnerDown && other.runnerDown,
    publicKey: base.publicKey || other.publicKey,
    hardwareId: base.hardwareId || other.hardwareId,
    lastHeartbeat: Math.max(a.lastHeartbeat || 0, b.lastHeartbeat || 0),
    localIps: [...new Set([...(a.localIps || []), ...(b.localIps || [])].filter(Boolean))],
    publicEndpoints: [...new Set([...(a.publicEndpoints || []), ...(b.publicEndpoints || [])].filter(Boolean))],
    runners: (base.runners && base.runners.length > 0) ? base.runners : other.runners,
    sharedWithGuests: base.sharedWithGuests ?? other.sharedWithGuests,
    sharesAllProjects: (base.sharesAllProjects ?? false) || (other.sharesAllProjects ?? false),
    sharedProjects: [...new Set([...(a.sharedProjects || []), ...(b.sharedProjects || [])].filter(Boolean))],
    sharesAllRunners: (base.sharesAllRunners ?? false) || (other.sharesAllRunners ?? false),
    sharedRunners: [...new Set([...(a.sharedRunners || []), ...(b.sharedRunners || [])].filter(Boolean))],
  };
}

function pickActiveListedDevice(a: ListedDevice, b: ListedDevice): ListedDevice | null {
  const aDead = a.needsAuth && !a.isOnline;
  const bDead = b.needsAuth && !b.isOnline;
  const aLive = !a.needsAuth && a.isOnline;
  const bLive = !b.needsAuth && b.isOnline;
  if (aDead && bLive) return b;
  if (bDead && aLive) return a;
  return null;
}

function collapseListedDevices(devices: ListedDevice[]): ListedDevice[] {
  if (!Array.isArray(devices) || devices.length === 0) return [];

  const byIdentity = new Map<string, ListedDevice>();
  for (const device of devices) {
    const key = listedDeviceIdentityKey(device);
    const existing = byIdentity.get(key);
    byIdentity.set(key, existing ? mergeListedDevices(existing, device) : device);
  }

  const byAlias = new Map<string, ListedDevice>();
  for (const device of byIdentity.values()) {
    const key = listedDeviceAliasKey(device);
    if (!key) {
      byAlias.set(`id:${device.deviceId}`, device);
      continue;
    }
    const existing = byAlias.get(key);
    if (!existing) {
      byAlias.set(key, device);
      continue;
    }
    const strongConflict =
      (!!existing.hardwareId && !!device.hardwareId && existing.hardwareId !== device.hardwareId) ||
      (!!existing.publicKey && !!device.publicKey && existing.publicKey !== device.publicKey);
    if (strongConflict) {
      const winner = pickActiveListedDevice(existing, device);
      if (winner) {
        byAlias.set(key, winner);
        continue;
      }
    }
    byAlias.set(key, mergeListedDevices(existing, device));
  }

  const byEndpoint = new Map<string, ListedDevice>();
  for (const device of byAlias.values()) {
    const key = listedDeviceEndpointKey(device);
    if (!key) {
      byEndpoint.set(`id:${device.deviceId}`, device);
      continue;
    }
    const existing = byEndpoint.get(key);
    byEndpoint.set(key, existing ? mergeListedDevices(existing, device) : device);
  }

  return [...byEndpoint.values()];
}

function normalizeScopedList(items: string[] | undefined): string[] {
  return Array.isArray(items)
    ? items.map((item) => String(item).trim()).filter(Boolean)
    : [];
}

function summarizeShareRules(rules: ShareRule[]): Pick<ListedDevice, "sharedWithGuests" | "sharesAllProjects" | "sharedProjects" | "sharesAllRunners" | "sharedRunners"> {
  const projects = new Set<string>();
  const runners = new Set<string>();
  let sharesAllProjects = false;
  let sharesAllRunners = false;

  for (const rule of rules) {
    const allowedProjects = normalizeScopedList(rule.allowedProjects);
    const allowedRunners = normalizeScopedList(rule.allowedRunners);

    if (allowedProjects.length === 0) {
      sharesAllProjects = true;
    } else {
      allowedProjects.forEach((project) => projects.add(project));
    }

    if (allowedRunners.length === 0) {
      sharesAllRunners = true;
    } else {
      allowedRunners.forEach((runner) => runners.add(runner));
    }
  }

  return {
    sharedWithGuests: rules.length > 0,
    sharesAllProjects,
    sharedProjects: [...projects],
    sharesAllRunners,
    sharedRunners: [...runners],
  };
}

/**
 * Register or update a device for peer discovery.
 * Requires a valid session tokenHash.
 */
export const registerDevice = mutation({
  args: {
    tokenHash: v.string(),
    deviceId: v.string(),
    name: v.string(),
    platform: v.union(
      v.literal("macos"),
      v.literal("windows"),
      v.literal("linux"),
      v.literal("android"),
      v.literal("ios")
    ),
    deviceClass: v.optional(
      v.union(
        v.literal("desktop"),
        v.literal("edge-mobile"),
        v.literal("server")
      )
    ),
    edgeProfile: v.optional(v.object({
      supportsLocalInference: v.boolean(),
      maxModelClass: v.union(
        v.literal("none"),
        v.literal("tiny"),
        v.literal("small"),
        v.literal("medium")
      ),
      preferredTasks: v.array(v.union(
        v.literal("speech"),
        v.literal("ocr"),
        v.literal("vision"),
        v.literal("embedding"),
        v.literal("rerank"),
        v.literal("automation"),
        v.literal("small-llm")
      )),
      memoryMb: v.optional(v.number()),
      batteryPct: v.optional(v.number()),
      isCharging: v.optional(v.boolean()),
      thermalState: v.optional(v.union(v.literal("nominal"), v.literal("warm"), v.literal("hot"))),
    })),
    publicKey: v.optional(v.string()),
    quicHost: v.string(),
    quicPort: v.number(),
    publicEndpoints: v.optional(v.array(v.string())),
    hardwareId: v.optional(v.string()),
  },
  handler: async (ctx, args) => {
    const session = await validateSessionInternal(ctx, args.tokenHash);
    if (!session) throw new Error("Unauthorized");

    const existing = await ctx.db
      .query("devices")
      .withIndex("by_deviceId", (q) => q.eq("deviceId", args.deviceId))
      .unique();

    if (existing) {
      // Only allow the owner to update their own device
      if (existing.userId !== session.user._id) {
        throw new Error("Device belongs to another user");
      }
      // Every register call requires a valid session (validated
      // above) — so by definition this device is owner-authenticated
      // and is not in needs-auth / bootstrap state anymore. Clear
      // the flag here so it doesn't stay stuck at true after a
      // bootstrap → owner-mode transition (which otherwise surfaces
      // as a permanent yellow "Needs pairing" row in every client
      // picker). Patches the existing row in place — no new
      // heartbeat backlog, one row per device always.
      await ctx.db.patch(existing._id, {
        name: args.name,
        platform: args.platform,
        deviceClass: args.deviceClass,
        edgeProfile: args.edgeProfile,
        publicKey: args.publicKey,
        quicHost: args.quicHost,
        quicPort: args.quicPort,
        publicEndpoints: args.publicEndpoints,
        isOnline: true,
        needsAuth: false,
        lastHeartbeat: Date.now(),
        ...(args.hardwareId ? { hardwareId: args.hardwareId } : {}),
      });
      return existing._id;
    }

    // If this is the user's first device and they have no primary set yet,
    // auto-mark it as primary so single-device users skip the "pick one"
    // prompt forever. Existing multi-device users are untouched — they
    // have to explicitly choose a primary.
    const ownDeviceCount = (await ctx.db
      .query("devices")
      .withIndex("by_userId", (q) => q.eq("userId", session.user._id))
      .collect()).length;
    if (ownDeviceCount === 0) {
      const settings = await ctx.db
        .query("userSettings")
        .withIndex("by_userId", (q) => q.eq("userId", session.user._id))
        .first();
      if (!settings) {
        await ctx.db.insert("userSettings", {
          userId: session.user._id,
          primaryDeviceId: args.deviceId,
        });
      } else if (!settings.primaryDeviceId) {
        await ctx.db.patch(settings._id, { primaryDeviceId: args.deviceId });
      }
    }

    return await ctx.db.insert("devices", {
      userId: session.user._id,
      deviceId: args.deviceId,
      name: args.name,
      platform: args.platform,
      deviceClass: args.deviceClass,
      edgeProfile: args.edgeProfile,
      publicKey: args.publicKey,
      quicHost: args.quicHost,
      quicPort: args.quicPort,
      publicEndpoints: args.publicEndpoints,
      isOnline: true,
      lastHeartbeat: Date.now(),
      createdAt: Date.now(),
      hardwareId: args.hardwareId,
    });
  },
});

/**
 * Look up the owner of a device by its stable hardware ID.
 * Used by the agent's /auth/recover endpoint to verify that the
 * caller (mobile app) is the original host of a machine that has
 * lost its auth token. No tokenHash required — the agent calls
 * this on behalf of the caller and the host check is what gates
 * the recovery action.
 */
export const ownerByHardwareId = query({
  args: {
    hardwareId: v.string(),
  },
  handler: async (ctx, args) => {
    const device = await ctx.db
      .query("devices")
      .withIndex("by_hardwareId", (q) => q.eq("hardwareId", args.hardwareId))
      .first();
    if (!device) return null;
    return {
      deviceId: device.deviceId,
      ownerUserId: device.userId,
      name: device.name,
    };
  },
});

/**
 * Update device heartbeat — marks it as online.
 */
export const heartbeat = mutation({
  args: {
    tokenHash: v.string(),
    deviceId: v.string(),
    runners: v.optional(v.array(v.object({
      taskId: v.string(),
      runnerId: v.string(),
      model: v.optional(v.string()),
      pid: v.number(),
      status: v.string(),
      title: v.string(),
    }))),
    quicHost: v.optional(v.string()),
    localIps: v.optional(v.array(v.string())),
    publicEndpoints: v.optional(v.array(v.string())),
    hardwareId: v.optional(v.string()),
    deviceClass: v.optional(
      v.union(
        v.literal("desktop"),
        v.literal("edge-mobile"),
        v.literal("server")
      )
    ),
    edgeProfile: v.optional(v.object({
      supportsLocalInference: v.boolean(),
      maxModelClass: v.union(
        v.literal("none"),
        v.literal("tiny"),
        v.literal("small"),
        v.literal("medium")
      ),
      preferredTasks: v.array(v.union(
        v.literal("speech"),
        v.literal("ocr"),
        v.literal("vision"),
        v.literal("embedding"),
        v.literal("rerank"),
        v.literal("automation"),
        v.literal("small-llm")
      )),
      memoryMb: v.optional(v.number()),
      batteryPct: v.optional(v.number()),
      isCharging: v.optional(v.boolean()),
      thermalState: v.optional(v.union(v.literal("nominal"), v.literal("warm"), v.literal("hot"))),
    })),
  },
  handler: async (ctx, args) => {
    const session = await validateSessionInternal(ctx, args.tokenHash);
    if (!session) throw new Error("Unauthorized");

    const device = await ctx.db
      .query("devices")
      .withIndex("by_deviceId", (q) => q.eq("deviceId", args.deviceId))
      .unique();

    if (!device) throw new Error("Device not found");
    if (device.userId !== session.user._id) throw new Error("Unauthorized");

    const patch: Record<string, unknown> = {
      isOnline: true,
      lastHeartbeat: Date.now(),
      needsAuth: false, // valid session → not in bootstrap mode
      runners: args.runners ?? [],
    };
    // Update stored IP if the agent reports a new one
    if (args.quicHost && args.quicHost !== device.quicHost) {
      patch.quicHost = args.quicHost;
    }
    // Replace the full IP set on each heartbeat — interfaces come and
    // go (Tailscale up/down, Wi-Fi switches), so a delta-merge would
    // strand stale addresses on the record forever.
    if (args.localIps !== undefined) {
      patch.localIps = args.localIps;
    }
    if (args.publicEndpoints !== undefined) {
      patch.publicEndpoints = args.publicEndpoints;
    }
    // Capture hardwareId on heartbeats too — older agents that
    // were registered before the field existed will pick it up
    // on their next heartbeat.
    if (args.hardwareId && args.hardwareId !== device.hardwareId) {
      patch.hardwareId = args.hardwareId;
    }
    if (args.deviceClass) {
      patch.deviceClass = args.deviceClass;
    }
    if (args.edgeProfile) {
      patch.edgeProfile = args.edgeProfile;
    }
    await ctx.db.patch(device._id, patch);
  },
});

/**
 * Relay-driven presence update — called only by Yaver's relay server
 * via the /devices/presence HTTP action, which validates a shared
 * secret before running this mutation. The mutation itself doesn't
 * need user auth because the HTTP layer has already gated access.
 *
 * Flips `isOnline` immediately (no heartbeat wait) and records the
 * last tunnel event so reactive clients can show "just disconnected"
 * vs "offline for hours" accurately.
 */
export const presenceUpdate = mutation({
  args: {
    deviceId: v.string(),
    online: v.boolean(),
    peerAddr: v.optional(v.string()),
    connectedAt: v.optional(v.number()),
    durationSec: v.optional(v.number()),
  },
  handler: async (ctx, args) => {
    const device = await ctx.db
      .query("devices")
      .withIndex("by_deviceId", (q) => q.eq("deviceId", args.deviceId))
      .unique();
    if (!device) {
      // Silently ignore unknown devices — the relay may have tunnels
      // for devices that were removed from this user's account.
      return;
    }
    const patch: Record<string, unknown> = {
      isOnline: args.online,
      lastTunnelEvent: {
        online: args.online,
        at: Date.now(),
        peerAddr: args.peerAddr,
        connectedAt: args.connectedAt,
        durationSec: args.durationSec,
      },
    };
    // Refresh lastHeartbeat on connect so heartbeat-staleness checks
    // don't fight the tunnel-up signal. On disconnect leave it alone
    // — the agent is still alive elsewhere (maybe on LAN).
    if (args.online) {
      patch.lastHeartbeat = Date.now();
    }
    await ctx.db.patch(device._id, patch);
  },
});

/**
 * List all devices belonging to the authenticated user,
 * plus devices from hosts who granted them guest access.
 */
export const listMyDevices = query({
  args: {
    tokenHash: v.string(),
  },
  handler: async (ctx, args) => {
    const session = await validateSessionInternal(ctx, args.tokenHash);
    if (!session) throw new Error("Unauthorized");

    const allSessions = await ctx.db
      .query("sessions")
      .withIndex("by_userId", (q) => q.eq("userId", session.user._id))
      .collect();
    const dedicatedSessionDeviceIds = new Set(
      allSessions
        .map((row) => row.deviceId)
        .filter((deviceId): deviceId is string => typeof deviceId === "string" && deviceId.trim() !== ""),
    );

    // Own devices
    const ownDevices = await ctx.db
      .query("devices")
      .withIndex("by_userId", (q) => q.eq("userId", session.user._id))
      .collect();

    const activeGuestAccessRecords = await ctx.db
      .query("guestAccess")
      .withIndex("by_hostUserId", (q) => q.eq("hostUserId", session.user._id))
      .filter((q) => q.eq(q.field("revokedAt"), undefined))
      .collect();

    const activeGuestAccessByGuest = new Map(
      activeGuestAccessRecords.map((access) => [access.guestUserId.toString(), access]),
    );

    const activeHostInfraGrants = await ctx.db
      .query("infraAccessGrants")
      .withIndex("by_hostUserId", (q) => q.eq("hostUserId", session.user._id))
      .filter((q) => q.eq(q.field("status"), "active"))
      .collect();

    const shareRulesByDeviceId = new Map<string, ShareRule[]>();
    const pushShareRule = (deviceId: string, rule: ShareRule) => {
      if (!deviceId) return;
      const existing = shareRulesByDeviceId.get(deviceId) || [];
      existing.push(rule);
      shareRulesByDeviceId.set(deviceId, existing);
    };

    for (const grant of activeHostInfraGrants) {
      const access = activeGuestAccessByGuest.get(grant.guestUserId.toString());
      const rule: ShareRule = {
        allowedProjects: access?.allowedProjects,
        allowedRunners: grant.allowedRunners ?? access?.allowedRunners,
      };
      if (grant.shareAllDevices) {
        for (const device of ownDevices) pushShareRule(device.deviceId, rule);
        continue;
      }
      for (const deviceId of await listGrantedDeviceIdsForGrant(ctx, grant._id)) {
        pushShareRule(deviceId, rule);
      }
    }

    for (const access of activeGuestAccessRecords) {
      const hasScopedGrant = activeHostInfraGrants.some(
        (grant) => grant.guestUserId.toString() === access.guestUserId.toString(),
      );
      if (hasScopedGrant) continue;
      const rule: ShareRule = {
        allowedProjects: access.allowedProjects,
        allowedRunners: access.allowedRunners,
      };
      for (const device of ownDevices) pushShareRule(device.deviceId, rule);
    }

    const result: ListedDevice[] = ownDevices.map((d) => ({
      deviceId: d.deviceId,
      name: d.name,
      platform: d.platform,
      publicKey: d.publicKey,
      hardwareId: d.hardwareId,
      quicHost: d.quicHost,
      localIps: d.localIps ?? [],
      publicEndpoints: d.publicEndpoints ?? [],
      quicPort: d.quicPort,
      isOnline: deriveIsOnline(d),
      needsAuth: d.needsAuth ?? false,
      runnerDown: d.runnerDown ?? false,
      runners: d.runners ?? [],
      lastHeartbeat: d.lastHeartbeat,
      isGuest: false as boolean,
      hostName: undefined as string | undefined,
      hostEmail: undefined as string | undefined,
      hostUserId: undefined as string | undefined,
      hostUserIdString: undefined as string | undefined,
      accessScope: "owner" as "owner" | "shared-scoped" | "shared-legacy",
      tunnelUrl: undefined as string | undefined,
      priorityMode: undefined as string | undefined,
      useHostApiKeys: undefined as boolean | undefined,
      allowGuestProvidedApiKeys: undefined as boolean | undefined,
      ...summarizeShareRules(shareRulesByDeviceId.get(d.deviceId) || []),
      sessionBinding: dedicatedSessionDeviceIds.has(d.deviceId) ? "dedicated" as const : "legacy-shared" as const,
      deviceClass: d.deviceClass,
      edgeProfile: d.edgeProfile,
    }));

    const scopedGrants = await listActiveInfraGrantsForGuest(ctx, session.user._id);
    const scopedHosts = new Set<string>();

    for (const grant of scopedGrants) {
      scopedHosts.add(grant.hostUserId.toString());
      const host = await ctx.db.get(grant.hostUserId);
      if (!host) continue;
      const access = await getLegacyGuestAccess(ctx, grant.hostUserId, session.user._id);
      const hostSettings = await ctx.db
        .query("userSettings")
        .withIndex("by_userId", (q) => q.eq("userId", grant.hostUserId))
        .first();

      const hostDevices = grant.shareAllDevices
        ? await ctx.db
            .query("devices")
            .withIndex("by_userId", (q) => q.eq("userId", grant.hostUserId))
            .collect()
        : await Promise.all(
            (await listGrantedDeviceIdsForGrant(ctx, grant._id)).map(async (deviceId) =>
              await ctx.db
                .query("devices")
                .withIndex("by_deviceId", (q) => q.eq("deviceId", deviceId))
                .unique(),
            ),
          ).then((devices) => devices.filter((device): device is Doc<"devices"> => device !== null));
      // An account-level tunnelUrl can only be attached safely when this grant
      // resolves to exactly one host device. Otherwise the guest could be given
      // a transport hint that belongs to another box under the same host.
      const sharedTunnelUrl = hostDevices.length === 1 ? hostSettings?.tunnelUrl : undefined;

      for (const d of hostDevices) {
        result.push({
          deviceId: d.deviceId,
          name: d.name,
          platform: d.platform,
          publicKey: d.publicKey,
          hardwareId: d.hardwareId,
          quicHost: d.quicHost,
          localIps: d.localIps ?? [],
          publicEndpoints: d.publicEndpoints ?? [],
          quicPort: d.quicPort,
          isOnline: deriveIsOnline(d),
          needsAuth: d.needsAuth ?? false,
          runnerDown: d.runnerDown ?? false,
          runners: d.runners ?? [],
          lastHeartbeat: d.lastHeartbeat,
          isGuest: true,
          hostUserId: String(grant.hostUserId),
          hostName: host.fullName,
          hostEmail: host.email,
          hostUserIdString: host.userId,
          accessScope: "shared-scoped",
          tunnelUrl: sharedTunnelUrl,
          priorityMode: grant.priorityMode,
          useHostApiKeys: grant.useHostApiKeys,
          allowGuestProvidedApiKeys: grant.allowGuestProvidedApiKeys,
          ...summarizeShareRules([{
            allowedProjects: access?.allowedProjects,
            allowedRunners: grant.allowedRunners ?? access?.allowedRunners,
          }]),
          sessionBinding: undefined as "dedicated" | "legacy-shared" | undefined,
          deviceClass: d.deviceClass,
          edgeProfile: d.edgeProfile,
        });
      }
    }

    // Backward-compatibility: if a host has not been migrated to a scoped grant yet,
    // preserve the older host-wide guest access behavior.
    const guestAccessRecords = await ctx.db
      .query("guestAccess")
      .withIndex("by_guestUserId", (q) => q.eq("guestUserId", session.user._id))
      .filter((q) => q.eq(q.field("revokedAt"), undefined))
      .collect();

    for (const access of guestAccessRecords) {
      if (scopedHosts.has(access.hostUserId.toString())) continue;
      const host = await ctx.db.get(access.hostUserId);
      if (!host) continue;
      const hostSettings = await ctx.db
        .query("userSettings")
        .withIndex("by_userId", (q) => q.eq("userId", access.hostUserId))
        .first();

      const hostDevices = await ctx.db
        .query("devices")
        .withIndex("by_userId", (q) => q.eq("userId", access.hostUserId))
        .collect();
      const sharedTunnelUrl = hostDevices.length === 1 ? hostSettings?.tunnelUrl : undefined;

      for (const d of hostDevices) {
        result.push({
          deviceId: d.deviceId,
          name: d.name,
          platform: d.platform,
          publicKey: d.publicKey,
          hardwareId: d.hardwareId,
          quicHost: d.quicHost,
          localIps: d.localIps ?? [],
          publicEndpoints: d.publicEndpoints ?? [],
          quicPort: d.quicPort,
          isOnline: deriveIsOnline(d),
          needsAuth: d.needsAuth ?? false,
          runnerDown: d.runnerDown ?? false,
          runners: d.runners ?? [],
          lastHeartbeat: d.lastHeartbeat,
          isGuest: true,
          hostUserId: String(access.hostUserId),
          hostName: host.fullName,
          hostEmail: host.email,
          hostUserIdString: host.userId,
          accessScope: "shared-legacy",
          tunnelUrl: sharedTunnelUrl,
          priorityMode: access.usageMode === "idle-only" ? "spare-capacity" : undefined,
          useHostApiKeys: undefined,
          allowGuestProvidedApiKeys: true,
          ...summarizeShareRules([{
            allowedProjects: access.allowedProjects,
            allowedRunners: access.allowedRunners,
          }]),
          sessionBinding: undefined as "dedicated" | "legacy-shared" | undefined,
          deviceClass: d.deviceClass,
          edgeProfile: d.edgeProfile,
        });
      }
    }

    return collapseListedDevices(result);
  },
});

export const recommendTaskPlacement = query({
  args: {
    tokenHash: v.string(),
    taskKind: v.union(
      v.literal("speech-transcription"),
      v.literal("ocr"),
      v.literal("vision-labeling"),
      v.literal("embedding"),
      v.literal("rerank"),
      v.literal("small-local-agent"),
      v.literal("batch-preprocessing"),
      v.literal("big-llm-chat"),
      v.literal("long-context-reasoning"),
    ),
  },
  handler: async (ctx, args) => {
    const session = await validateSessionInternal(ctx, args.tokenHash);
    if (!session) throw new Error("Unauthorized");

    const ownDevices = await ctx.db
      .query("devices")
      .withIndex("by_userId", (q) => q.eq("userId", session.user._id))
      .collect();

    return recommendPlacement(
      collapseListedDevices(
        ownDevices.map((device) => ({
          deviceId: device.deviceId,
          name: device.name,
          platform: device.platform,
          publicKey: device.publicKey,
          hardwareId: device.hardwareId,
          quicHost: device.quicHost,
          localIps: device.localIps ?? [],
          publicEndpoints: device.publicEndpoints ?? [],
          quicPort: device.quicPort,
          isOnline: deriveIsOnline(device),
          needsAuth: device.needsAuth ?? false,
          runnerDown: device.runnerDown ?? false,
          runners: device.runners ?? [],
          lastHeartbeat: device.lastHeartbeat,
          isGuest: false,
          accessScope: "owner",
          deviceClass: device.deviceClass,
          edgeProfile: device.edgeProfile,
        })),
      ).map((device) => ({
        deviceId: device.deviceId,
        name: device.name,
        platform: device.platform,
        // Same staleness gate as listMyDevices — placement decisions
        // never route work to a device whose last heartbeat is older
        // than HEARTBEAT_STALE_MS, even if its isOnline flag was
        // never explicitly cleared.
        isOnline: deriveIsOnline(device),
        needsAuth: device.needsAuth,
        runnerDown: device.runnerDown,
        deviceClass: device.deviceClass,
        edgeProfile: device.edgeProfile,
      })),
      args.taskKind,
    );
  },
});

/**
 * Update the runnerDown flag for a device.
 * Called by the desktop agent when runner crashes with all retries exhausted,
 * or when runner is successfully restarted.
 */
export const setRunnerDown = mutation({
  args: {
    tokenHash: v.string(),
    deviceId: v.string(),
    runnerDown: v.boolean(),
  },
  handler: async (ctx, args) => {
    const session = await validateSessionInternal(ctx, args.tokenHash);
    if (!session) throw new Error("Unauthorized");

    const device = await ctx.db
      .query("devices")
      .withIndex("by_deviceId", (q) => q.eq("deviceId", args.deviceId))
      .unique();

    if (!device) throw new Error("Device not found");
    if (device.userId !== session.user._id) throw new Error("Unauthorized");

    await ctx.db.patch(device._id, { runnerDown: args.runnerDown });
  },
});

/**
 * Mark a device as offline.
 * Called by the desktop agent on stop/signout.
 */
/**
 * Mark a device as in bootstrap mode (agent running, no valid token).
 * Authenticates on (deviceId, hardwareId, publicKey) triple — these
 * match the existing Convex record set during the first `yaver auth`.
 * If all three match, we update needsAuth=true + isOnline=true + heartbeat.
 * This lets mobile/web show the device as "NEEDS AUTH" in the list so
 * the user can push an encrypted token to re-auth it remotely.
 */
export const markBootstrap = mutation({
  args: {
    deviceId: v.string(),
    hardwareId: v.string(),
    publicKey: v.string(),
    quicHost: v.optional(v.string()),
    quicPort: v.optional(v.number()),
  },
  handler: async (ctx, args) => {
    const device = await ctx.db
      .query("devices")
      .withIndex("by_deviceId", (q) => q.eq("deviceId", args.deviceId))
      .unique();
    if (!device) throw new Error("Device not found");
    // Identity proof: hardwareId + publicKey must match what was stored
    // during the initial `yaver auth`. Prevents a random caller from
    // toggling arbitrary devices into bootstrap mode.
    if (device.hardwareId !== args.hardwareId) throw new Error("Hardware ID mismatch");
    if (device.publicKey !== args.publicKey) throw new Error("Public key mismatch");
    const patch: any = {
      isOnline: true,
      needsAuth: true,
      lastHeartbeat: Date.now(),
    };
    if (args.quicHost) patch.quicHost = args.quicHost;
    if (args.quicPort) patch.quicPort = args.quicPort;
    await ctx.db.patch(device._id, patch);
    return { ok: true, userId: device.userId };
  },
});

/**
 * Mark a device as no longer in bootstrap mode (just got a token).
 * Authed via tokenHash. Clears needsAuth flag.
 */
export const clearBootstrap = mutation({
  args: {
    tokenHash: v.string(),
    deviceId: v.string(),
  },
  handler: async (ctx, args) => {
    const session = await validateSessionInternal(ctx, args.tokenHash);
    if (!session) throw new Error("Unauthorized");
    const device = await ctx.db
      .query("devices")
      .withIndex("by_deviceId", (q) => q.eq("deviceId", args.deviceId))
      .unique();
    if (!device) return { ok: false };
    if (device.userId !== session.user._id) throw new Error("Unauthorized");
    await ctx.db.patch(device._id, { needsAuth: false, lastHeartbeat: Date.now() });
    return { ok: true };
  },
});

export const markOffline = mutation({
  args: {
    tokenHash: v.string(),
    deviceId: v.string(),
  },
  handler: async (ctx, args) => {
    const session = await validateSessionInternal(ctx, args.tokenHash);
    if (!session) throw new Error("Unauthorized");

    const device = await ctx.db
      .query("devices")
      .withIndex("by_deviceId", (q) => q.eq("deviceId", args.deviceId))
      .unique();

    if (!device) throw new Error("Device not found");
    if (device.userId !== session.user._id) throw new Error("Unauthorized");

    await ctx.db.patch(device._id, {
      isOnline: false,
      lastHeartbeat: Date.now(),
    });
  },
});

/**
 * Remove (unregister) a device.
 */
export const removeDevice = mutation({
  args: {
    tokenHash: v.string(),
    deviceId: v.string(),
  },
  handler: async (ctx, args) => {
    const session = await validateSessionInternal(ctx, args.tokenHash);
    if (!session) throw new Error("Unauthorized");

    const device = await ctx.db
      .query("devices")
      .withIndex("by_deviceId", (q) => q.eq("deviceId", args.deviceId))
      .unique();

    if (!device) throw new Error("Device not found");
    if (device.userId !== session.user._id) throw new Error("Unauthorized");

    const deviceSessions = await ctx.db
      .query("sessions")
      .withIndex("by_deviceId", (q) => q.eq("deviceId", args.deviceId))
      .collect();
    for (const deviceSession of deviceSessions) {
      if (deviceSession.userId === session.user._id) {
        await ctx.db.delete(deviceSession._id);
      }
    }

    await ctx.db.delete(device._id);
  },
});
