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
import {
  smartDeviceLabel,
  smartAliasSlug,
  uniqueAliasSlug,
  isRawHostname,
  type LabelSignals,
} from "./deviceLabels";

const recoveryPostureValidator = v.object({
  status: v.string(),
  mobileApprovedTransports: v.array(v.string()),
  webApprovedTransports: v.array(v.string()),
  hasPrivateTransport: v.boolean(),
  hasBrowserTransport: v.boolean(),
  publicDirectRecoveryClosed: v.boolean(),
  summary: v.string(),
});

const connectionPreferenceValidator = v.object({
  kind: v.union(
    v.literal("direct-lan"),
    v.literal("tailscale"),
    v.literal("headscale"),
    v.literal("own-vpn"),
    v.literal("https-tunnel"),
    v.literal("free-relay"),
    v.literal("private-relay")
  ),
  active: v.boolean(),
  preferred: v.boolean(),
  source: v.union(
    v.literal("agent-detected"),
    v.literal("user-config"),
    v.literal("platform-config"),
    v.literal("relay-presence")
  ),
});

const hardwareProfileValidator = v.object({
  os: v.optional(v.string()),
  osVersion: v.optional(v.string()),
  cpu: v.optional(v.string()),
  gpu: v.optional(v.string()),
  ramMb: v.optional(v.number()),
  vramMb: v.optional(v.number()),
  numCores: v.optional(v.number()),
  arch: v.optional(v.string()),
  iosSimulators: v.optional(v.array(v.string())),
  androidEmulators: v.optional(v.array(v.string())),
});

// HEARTBEAT_STALE_MS: how long after the last heartbeat we still
// trust the device's `isOnline` flag. The agent beats every 5 min
// (see `desktop/agent/main.go::heartbeatLoop`), so 6 min is "missed
// one beat plus 60 s of jitter" — enough to ride out network jitter,
// GC pauses, and Convex write latency without flapping a healthy
// device offline. Without this server-side gate, a SIGKILL'd /
// power-cut / wifi-dropped agent looks online forever (the flag
// never gets downgraded by the markOffline mutation that the dying
// process can't run). Mobile / web read this derived value via the
// listing queries and the device card stops flickering.
//
// Sub-minute death detection is now provided by the P2P bus
// (`desktop/agent/bus.go`) which has its own keepalive — clients can
// subscribe to /bus/events for live presence instead of polling
// Convex. Keep this constant in sync with mobile/_core/constants.ts
// and web/lib/use-devices.ts.
const HEARTBEAT_STALE_MS = 360 * 1000;

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
  /**
   * Optional per-user alias for this Yaver device. Set via
   * `yaver alias set ...` or the dashboard/device UI; stored on the
   * device row in Convex so CLI, web, and mobile all resolve the same
   * short name.
   */
  alias?: string;
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
  installedRunnerIds?: string[];
  lastHeartbeat: number;
  lastTunnelEvent?: Doc<"devices">["lastTunnelEvent"];
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
  sharedGuests?: Array<{
    name?: string;
    email?: string;
  }>;
  sharesAllProjects?: boolean;
  sharedProjects?: string[];
  sharesAllRunners?: boolean;
  sharedRunners?: string[];
  sessionBinding?: "dedicated" | "legacy-shared";
  deviceClass?: "desktop" | "edge-mobile" | "server";
  publishCapabilities?: string[];
  edgeProfile?: Doc<"devices">["edgeProfile"];
  recoveryPosture?: Doc<"devices">["recoveryPosture"];
  connectionPreferences?: Doc<"devices">["connectionPreferences"];
  hardwareProfile?: Doc<"devices">["hardwareProfile"];
  agentVersion?: string;
  agentVersionReportedAt?: number;
};

function mergeHardwareProfile(
  a: ListedDevice["hardwareProfile"] | undefined,
  b: ListedDevice["hardwareProfile"] | undefined,
): ListedDevice["hardwareProfile"] | undefined {
  if (!a) return b;
  if (!b) return a;
  return {
    os: b.os || a.os,
    osVersion: b.osVersion || a.osVersion,
    cpu: b.cpu || a.cpu,
    gpu: b.gpu || a.gpu,
    ramMb: b.ramMb ?? a.ramMb,
    vramMb: b.vramMb ?? a.vramMb,
    numCores: b.numCores ?? a.numCores,
    arch: b.arch || a.arch,
    iosSimulators: (b.iosSimulators && b.iosSimulators.length > 0) ? b.iosSimulators : a.iosSimulators,
    androidEmulators: (b.androidEmulators && b.androidEmulators.length > 0) ? b.androidEmulators : a.androidEmulators,
  };
}

type ShareRule = {
  allowedProjects?: string[];
  allowedRunners?: string[];
  guestName?: string;
  guestEmail?: string;
};

function normalizeDeviceName(name: string | undefined): string {
  return String(name || "").trim().toLowerCase().replace(/\.local$/i, "");
}

function normalizeDeviceHost(host: string | undefined): string {
  return String(host || "").trim().toLowerCase().replace(/\.local$/i, "");
}

function listedDeviceIdentityKey(device: ListedDevice): string {
  if (device.isGuest) {
    const scope = device.hostEmail || device.hostName || "guest";
    return `guest:${scope}:${device.deviceId || device.name}`;
  }
  if (device.hardwareId) return `hwid:${device.hardwareId}`;
  if (device.publicKey) return `pub:${device.publicKey}`;
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
    hardwareProfile: mergeHardwareProfile(other.hardwareProfile, base.hardwareProfile),
    lastHeartbeat: Math.max(a.lastHeartbeat || 0, b.lastHeartbeat || 0),
    lastTunnelEvent:
      (() => {
        const aAt = a.lastTunnelEvent?.at || 0;
        const bAt = b.lastTunnelEvent?.at || 0;
        if (aAt === 0) return b.lastTunnelEvent;
        if (bAt === 0) return a.lastTunnelEvent;
        return bAt > aAt ? b.lastTunnelEvent : a.lastTunnelEvent;
      })(),
    localIps: [...new Set([...(a.localIps || []), ...(b.localIps || [])].filter(Boolean))],
    publicEndpoints: [...new Set([...(a.publicEndpoints || []), ...(b.publicEndpoints || [])].filter(Boolean))],
    runners: (base.runners && base.runners.length > 0) ? base.runners : other.runners,
    installedRunnerIds:
      (base.installedRunnerIds && base.installedRunnerIds.length > 0)
        ? base.installedRunnerIds
        : other.installedRunnerIds,
    sharedWithGuests: base.sharedWithGuests ?? other.sharedWithGuests,
    sharedGuests: [
      ...new Map(
        [...(a.sharedGuests || []), ...(b.sharedGuests || [])]
          .filter((guest) => guest?.name || guest?.email)
          .map((guest) => [`${guest.email || ""}:${guest.name || ""}`, guest]),
      ).values(),
    ],
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

function mergeConnectionPreferences(
  existing: Doc<"devices">["connectionPreferences"] | undefined,
  incoming: Doc<"devices">["connectionPreferences"] | undefined,
): Doc<"devices">["connectionPreferences"] | undefined {
  if (incoming === undefined) return existing;
  const byKind = new Map<string, NonNullable<Doc<"devices">["connectionPreferences"]>[number]>();
  for (const pref of incoming || []) {
    byKind.set(pref.kind, pref);
  }
  for (const pref of existing || []) {
    if (pref.source !== "user-config") continue;
    const runtime = byKind.get(pref.kind);
    if (runtime) {
      byKind.set(pref.kind, {
        ...runtime,
        active: runtime.active || pref.active,
        preferred: runtime.preferred || pref.preferred,
        source: "user-config",
      });
    } else {
      byKind.set(pref.kind, pref);
    }
  }
  return [...byKind.values()];
}

function ipKindForConnectionPreference(raw: string): "direct-lan" | "tailscale" | null {
  const parts = String(raw || "").trim().split(".").map((p) => Number(p));
  if (parts.length !== 4 || parts.some((p) => !Number.isInteger(p) || p < 0 || p > 255)) return null;
  const [a, b] = parts;
  if (a === 100 && b >= 64 && b <= 127) return "tailscale";
  if (a === 10 || (a === 172 && b >= 16 && b <= 31) || (a === 192 && b === 168)) return "direct-lan";
  return null;
}

function inferConnectionPreferencesForDevice(
  device: Pick<Doc<"devices">, "localIps" | "publicEndpoints" | "lastTunnelEvent" | "connectionPreferences">,
): Doc<"devices">["connectionPreferences"] {
  const out: NonNullable<Doc<"devices">["connectionPreferences"]> = [];
  const push = (kind: NonNullable<Doc<"devices">["connectionPreferences"]>[number]["kind"], preferred = false, source: NonNullable<Doc<"devices">["connectionPreferences"]>[number]["source"] = "agent-detected") => {
    if (out.some((pref) => pref.kind === kind)) return;
    out.push({ kind, active: true, preferred, source });
  };

  const existingUserPrefs = (device.connectionPreferences || []).filter((pref) => pref.source === "user-config");
  const forceHeadscale = existingUserPrefs.some((pref) => pref.kind === "headscale" && (pref.active || pref.preferred));
  for (const ip of device.localIps || []) {
    const kind = ipKindForConnectionPreference(ip);
    if (kind === "direct-lan") push("direct-lan", true);
    if (kind === "tailscale") push(forceHeadscale ? "headscale" : "tailscale", true);
  }

  for (const endpoint of device.publicEndpoints || []) {
    const value = String(endpoint || "").trim().toLowerCase();
    if (!value.startsWith("https://")) continue;
    if (value.includes(".yaver.io/") || value.endsWith(".yaver.io")) {
      push("free-relay", false, "relay-presence");
    } else {
      push("https-tunnel", false);
    }
  }
  if (device.lastTunnelEvent?.online) {
    push("free-relay", false, "relay-presence");
  }
  return mergeConnectionPreferences(device.connectionPreferences, out) || out;
}

function summarizeShareRules(rules: ShareRule[]): Pick<ListedDevice, "sharedWithGuests" | "sharedGuests" | "sharesAllProjects" | "sharedProjects" | "sharesAllRunners" | "sharedRunners"> {
  const projects = new Set<string>();
  const runners = new Set<string>();
  const guests = new Map<string, { name?: string; email?: string }>();
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

    const guestName = String(rule.guestName || "").trim();
    const guestEmail = String(rule.guestEmail || "").trim();
    if (guestName || guestEmail) {
      guests.set(`${guestEmail}:${guestName}`, {
        name: guestName || undefined,
        email: guestEmail || undefined,
      });
    }
  }

  return {
    sharedWithGuests: rules.length > 0,
    sharedGuests: [...guests.values()],
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
    hardwareProfile: v.optional(hardwareProfileValidator),
    recoveryPosture: v.optional(recoveryPostureValidator),
    connectionPreferences: v.optional(v.array(connectionPreferenceValidator)),
    agentVersion: v.optional(v.string()),
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
        ...(args.hardwareProfile ? { hardwareProfile: args.hardwareProfile } : {}),
        ...(args.recoveryPosture ? { recoveryPosture: args.recoveryPosture } : {}),
        ...(args.connectionPreferences
          ? { connectionPreferences: mergeConnectionPreferences(existing.connectionPreferences, args.connectionPreferences) }
          : {}),
        // register is always an authoritative refresh — stamp version
        // if the agent reported one (older agents omit the field).
        ...(args.agentVersion
          ? { agentVersion: args.agentVersion, agentVersionReportedAt: Date.now() }
          : {}),
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

    // Smart auto-label + auto-seeded alias slug (deviceLabels.ts). Friendly
    // names ("Hetzner box", "MacBook", "Linux box") and a memorable alias
    // ("hetzner", "mac", "linux") make device pickers readable and let the
    // on-device voice helper resolve "my hetzner box" deterministically.
    // If this box is a managed cloud machine, pull provider/region for the
    // best label. Cloud row is linked by deviceId once the agent registers.
    const cloudRow = await ctx.db
      .query("cloudMachines")
      .withIndex("by_deviceId", (q) => q.eq("deviceId", args.deviceId))
      .first();
    // Read optional hardware fields through a loose view: the validator
    // shape varies across agent versions, so we don't want a hard compile
    // dependency on every field being present.
    const hw = (args.hardwareProfile ?? {}) as Record<string, unknown>;
    const labelSignals: LabelSignals = {
      platform: args.platform,
      hostname: args.name,
      cloudProvider: cloudRow?.provider,
      cloudRegion: cloudRow?.region,
      // hardwareProfileValidator currently has cpu/gpu (no isWsl); read
      // defensively so newer agents that add isWsl/model still work.
      isWsl: typeof hw.isWsl === "boolean" ? hw.isWsl : undefined,
      hardwareModel: String(hw.cpu || hw.gpu || hw.model || "").toLowerCase(),
    };
    // Only replace the agent-sent name when it's a raw/uninformative
    // hostname — a user-meaningful hostname is left untouched.
    const smartLabel = smartDeviceLabel(labelSignals);
    const resolvedName =
      smartLabel && isRawHostname(args.name, args.platform) ? smartLabel : args.name;

    // Auto-seed a unique alias slug if the user hasn't got one for this box
    // yet. Collect existing aliases to avoid collisions (alias is per-user
    // unique). Best-effort: never block registration on aliasing.
    let autoAlias: string | undefined;
    try {
      const ownDevices = await ctx.db
        .query("devices")
        .withIndex("by_userId", (q) => q.eq("userId", session.user._id))
        .collect();
      const taken = new Set(
        ownDevices.map((d) => d.alias).filter((a): a is string => !!a),
      );
      const slug = uniqueAliasSlug(smartAliasSlug(labelSignals), taken);
      if (slug) autoAlias = slug;
    } catch {
      // aliasing is a nicety, not a requirement — ignore failures
    }

    return await ctx.db.insert("devices", {
      userId: session.user._id,
      deviceId: args.deviceId,
      name: resolvedName,
      ...(autoAlias ? { alias: autoAlias } : {}),
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
      hardwareProfile: args.hardwareProfile,
      recoveryPosture: args.recoveryPosture,
      connectionPreferences: args.connectionPreferences,
      agentVersion: args.agentVersion,
      agentVersionReportedAt: args.agentVersion ? Date.now() : undefined,
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
 * Caller-aware variant of ownerByHardwareId. The plain query returns
 * `.first()` on a non-unique index — when multiple device rows share
 * the same hardwareId (test-fixture registrations, prior owners,
 * re-claims), the wrong row gets returned and the agent's
 * verifyHostToken (called by /auth/recover) reports `isOwner: false`
 * for the legit owner. This variant `.collect()`s and prefers a row
 * owned by the caller, falling back to first when none match.
 *
 * Wired into POST /devices/owner-by-hardware so the existing agent
 * code path (auth_recover.go::verifyHostToken) starts succeeding for
 * users whose hardwareId is shared with stale rows owned by other
 * userIds.
 */
export const ownerByHardwareIdForCaller = query({
  args: {
    hardwareId: v.string(),
    callerUserId: v.string(),
  },
  handler: async (ctx, args) => {
    const devices = await ctx.db
      .query("devices")
      .withIndex("by_hardwareId", (q) => q.eq("hardwareId", args.hardwareId))
      .collect();
    if (devices.length === 0) return null;
    const ownByCaller = devices.find((d) => String(d.userId) === args.callerUserId);
    const picked = ownByCaller || devices[0];
    return {
      deviceId: picked.deviceId,
      ownerUserId: picked.userId,
      name: picked.name,
      // Diagnostic: how many duplicate rows exist for this hardwareId.
      // Lets the dashboard surface a "we found N rows with the same
      // hardware fingerprint" warning so the user can clean them up.
      duplicateCount: devices.length,
    };
  },
});

/**
 * Report the agent version for a device the caller owns. Used by the
 * dashboard to seed `agentVersion` on currently-running machines that
 * haven't yet been upgraded to a build that sends the field in its own
 * register/heartbeat payload. The browser probes `/info` on a device
 * it can reach, then calls this with the observed version string.
 *
 * Uses the same 24h + change-detection gate as heartbeat so repeat calls
 * are cheap.
 */
export const reportAgentVersion = mutation({
  args: {
    tokenHash: v.string(),
    deviceId: v.string(),
    agentVersion: v.string(),
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

    const trimmed = args.agentVersion.trim();
    if (!trimmed) return;
    const changed = trimmed !== device.agentVersion;
    const stale =
      !device.agentVersionReportedAt ||
      Date.now() - device.agentVersionReportedAt > 24 * 60 * 60 * 1000;
    if (changed || stale) {
      await ctx.db.patch(device._id, {
        agentVersion: trimmed,
        agentVersionReportedAt: Date.now(),
      });
    }
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
    installedRunnerIds: v.optional(v.array(v.string())),
    quicHost: v.optional(v.string()),
    localIps: v.optional(v.array(v.string())),
    publicEndpoints: v.optional(v.array(v.string())),
    hardwareId: v.optional(v.string()),
    hardwareProfile: v.optional(hardwareProfileValidator),
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
    recoveryPosture: v.optional(recoveryPostureValidator),
    connectionPreferences: v.optional(v.array(connectionPreferenceValidator)),
    agentVersion: v.optional(v.string()),
    publishCapabilities: v.optional(v.array(v.string())),
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
      installedRunnerIds: args.installedRunnerIds ?? [],
    };
    // Version write is gated to once per 24h OR on change. Agents may
    // send it on every heartbeat (and they do for simplicity); the
    // cadence lives here so we don't rewrite the row every 30s for
    // a string that hasn't moved.
    if (args.agentVersion) {
      const changed = args.agentVersion !== device.agentVersion;
      const stale =
        !device.agentVersionReportedAt ||
        Date.now() - device.agentVersionReportedAt > 24 * 60 * 60 * 1000;
      if (changed || stale) {
        patch.agentVersion = args.agentVersion;
        patch.agentVersionReportedAt = Date.now();
      }
    }
    // Update stored IP if the agent reports a new one. An empty string
    // is a deliberate clear (agent had a stale Docker-bridge address it
    // wants to retract) — apply it instead of treating empty-string as
    // "no opinion." Mobile reads quicHost as the direct-connect target;
    // a stale 172.18.0.1 keeps mobile stuck CONNECTING instead of
    // falling through to the relay.
    if (args.quicHost !== undefined && args.quicHost !== device.quicHost) {
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
    if (args.hardwareProfile) {
      patch.hardwareProfile = args.hardwareProfile;
    }
    if (args.deviceClass) {
      patch.deviceClass = args.deviceClass;
    }
    if (args.edgeProfile) {
      patch.edgeProfile = args.edgeProfile;
    }
    if (args.recoveryPosture) {
      patch.recoveryPosture = args.recoveryPosture;
    }
    if (args.connectionPreferences !== undefined) {
      patch.connectionPreferences = mergeConnectionPreferences(device.connectionPreferences, args.connectionPreferences);
    }
    if (args.publishCapabilities !== undefined) {
      patch.publishCapabilities = args.publishCapabilities;
    }
    await ctx.db.patch(device._id, patch);
    return {
      connectionPreferences: (patch.connectionPreferences as Doc<"devices">["connectionPreferences"] | undefined)
        ?? device.connectionPreferences
        ?? [],
    };
  },
});

/**
 * Store a user's explicit per-machine transport preferences in Convex.
 * Heartbeat preserves these rows while refreshing agent-detected active
 * state, so a user can mark "headscale preferred" once and keep that
 * product-level intent across agent restarts.
 */
export const setConnectionPreferences = mutation({
  args: {
    tokenHash: v.string(),
    deviceId: v.string(),
    preferences: v.array(connectionPreferenceValidator),
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

    const userPrefs = args.preferences.map((pref) => ({
      ...pref,
      source: "user-config" as const,
    }));
    const runtimePrefs = (device.connectionPreferences || []).filter((pref) => pref.source !== "user-config");
    await ctx.db.patch(device._id, {
      connectionPreferences: mergeConnectionPreferences(runtimePrefs, userPrefs),
    });
  },
});

/**
 * Fleet/user backfill for existing device rows. New agents seed
 * connectionPreferences on heartbeat, but existing offline machines may
 * not heartbeat for days. This infers a privacy-safe best effort from
 * already-stored localIps/publicEndpoints/relay presence. It is
 * idempotent and preserves user-config rows.
 */
export const backfillConnectionPreferences = mutation({
  args: {
    tokenHash: v.optional(v.string()),
    dryRun: v.optional(v.boolean()),
    limit: v.optional(v.number()),
  },
  handler: async (ctx, args) => {
    let scopedSession: Awaited<ReturnType<typeof validateSessionInternal>> = null;
    if (args.tokenHash) {
      scopedSession = await validateSessionInternal(ctx, args.tokenHash);
      if (!scopedSession) throw new Error("Unauthorized");
    }

    const allDevices = scopedSession
      ? await ctx.db
          .query("devices")
          .withIndex("by_userId", (q) => q.eq("userId", scopedSession!.user._id))
          .collect()
      : await ctx.db.query("devices").collect();
    const devices = typeof args.limit === "number" && args.limit > 0
      ? allDevices.slice(0, Math.floor(args.limit))
      : allDevices;

    let updated = 0;
    let unchanged = 0;
    let inferred = 0;
    for (const device of devices) {
      const next = inferConnectionPreferencesForDevice(device);
      if (next && next.length > 0) inferred++;
      const before = JSON.stringify(device.connectionPreferences || []);
      const after = JSON.stringify(next || []);
      if (before === after) {
        unchanged++;
        continue;
      }
      if (!args.dryRun) {
        await ctx.db.patch(device._id, { connectionPreferences: next });
      }
      updated++;
    }
    return { ok: true, dryRun: !!args.dryRun, total: devices.length, inferred, updated, unchanged };
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
    // Relay-auto-provisioned <id>.<expose-domain> URL — pushed by
    // the relay on tunnel-up so the dashboard sees it instantly
    // without waiting for the agent's next 5-min heartbeat.
    assignedUrl: v.optional(v.string()),
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
    // Merge the relay-assigned URL into publicEndpoints. Idempotent:
    // if it's already there, no change. Order doesn't matter for the
    // dashboard's transport classifier — it picks the *.yaver.io
    // subdomain by suffix match. On disconnect we LEAVE the URL in
    // place so the dashboard can still try it (the relay just won't
    // route until the next reconnect; the URL itself is durable).
    if (args.online && args.assignedUrl && /^https:\/\//.test(args.assignedUrl)) {
      const existing = (device.publicEndpoints ?? []) as string[];
      if (!existing.includes(args.assignedUrl)) {
        patch.publicEndpoints = [...existing, args.assignedUrl];
      }
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
      const guest = await ctx.db.get(grant.guestUserId);
      const rule: ShareRule = {
        allowedProjects: access?.allowedProjects,
        allowedRunners: grant.allowedRunners ?? access?.allowedRunners,
        guestName: guest?.fullName,
        guestEmail: guest?.email,
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
        guestName: undefined,
        guestEmail: undefined,
      };
      const guest = await ctx.db.get(access.guestUserId);
      if (guest) {
        rule.guestName = guest.fullName;
        rule.guestEmail = guest.email;
      }
      for (const device of ownDevices) pushShareRule(device.deviceId, rule);
    }

    const result: ListedDevice[] = ownDevices.map((d) => ({
      deviceId: d.deviceId,
      name: d.name,
      alias: d.alias,
      platform: d.platform,
      publishCapabilities: d.publishCapabilities,
      publicKey: d.publicKey,
      hardwareId: d.hardwareId,
      hardwareProfile: d.hardwareProfile,
      quicHost: d.quicHost,
      localIps: d.localIps ?? [],
      publicEndpoints: d.publicEndpoints ?? [],
      connectionPreferences: d.connectionPreferences,
      quicPort: d.quicPort,
      isOnline: deriveIsOnline(d),
      needsAuth: d.needsAuth ?? false,
      runnerDown: d.runnerDown ?? false,
      runners: d.runners ?? [],
      installedRunnerIds: d.installedRunnerIds ?? [],
      lastHeartbeat: d.lastHeartbeat,
      lastTunnelEvent: d.lastTunnelEvent,
      agentVersion: d.agentVersion,
      agentVersionReportedAt: d.agentVersionReportedAt,
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
      recoveryPosture: d.recoveryPosture,
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
          hardwareProfile: d.hardwareProfile,
          quicHost: d.quicHost,
          localIps: d.localIps ?? [],
          publicEndpoints: d.publicEndpoints ?? [],
          quicPort: d.quicPort,
          isOnline: deriveIsOnline(d),
          needsAuth: d.needsAuth ?? false,
          runnerDown: d.runnerDown ?? false,
          runners: d.runners ?? [],
          installedRunnerIds: d.installedRunnerIds ?? [],
          lastHeartbeat: d.lastHeartbeat,
          lastTunnelEvent: d.lastTunnelEvent,
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
          recoveryPosture: d.recoveryPosture,
          connectionPreferences: d.connectionPreferences,
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
          hardwareProfile: d.hardwareProfile,
          quicHost: d.quicHost,
          localIps: d.localIps ?? [],
          publicEndpoints: d.publicEndpoints ?? [],
          quicPort: d.quicPort,
          isOnline: deriveIsOnline(d),
          needsAuth: d.needsAuth ?? false,
          runnerDown: d.runnerDown ?? false,
          runners: d.runners ?? [],
          installedRunnerIds: d.installedRunnerIds ?? [],
          lastHeartbeat: d.lastHeartbeat,
          lastTunnelEvent: d.lastTunnelEvent,
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
          recoveryPosture: d.recoveryPosture,
          connectionPreferences: d.connectionPreferences,
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
          hardwareProfile: device.hardwareProfile,
          quicHost: device.quicHost,
          localIps: device.localIps ?? [],
          publicEndpoints: device.publicEndpoints ?? [],
          quicPort: device.quicPort,
          isOnline: deriveIsOnline(device),
          needsAuth: device.needsAuth ?? false,
          runnerDown: device.runnerDown ?? false,
          runners: device.runners ?? [],
          lastHeartbeat: device.lastHeartbeat,
          lastTunnelEvent: device.lastTunnelEvent,
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

    // Cascade — every Convex row that names this deviceId becomes
    // dangling the moment we delete the device. The orphaned sdkTokens
    // are the security-critical case (long-lived tokens still validate
    // for /info, /tasks, etc. until natural expiry); userProjects /
    // userServices / userDeployments are functional dangling pointers
    // (the dashboard still rows them as "live on <deviceId>"); the
    // primaryDeviceId pointer breaks `yaver ssh primary` everywhere.
    //
    // Each cascade query is gated by the same userId match used for
    // the device row above, so this mutation can never wipe rows the
    // caller doesn't own (defense-in-depth — every table also has
    // its own userId column we filter on).

    const sdkTokensForDevice = await ctx.db
      .query("sdkTokens")
      .withIndex("by_userId", (q) => q.eq("userId", session.user._id))
      .collect();
    for (const tok of sdkTokensForDevice) {
      if (tok.targetDeviceId === args.deviceId) {
        await ctx.db.delete(tok._id);
      }
    }

    const projectsForDevice = await ctx.db
      .query("userProjects")
      .withIndex("by_device", (q) => q.eq("deviceId", args.deviceId))
      .collect();
    for (const p of projectsForDevice) {
      if (p.userId === session.user._id) {
        await ctx.db.delete(p._id);
      }
    }

    const servicesForDevice = await ctx.db
      .query("userServices")
      .withIndex("by_device", (q) => q.eq("deviceId", args.deviceId))
      .collect();
    for (const svc of servicesForDevice) {
      if (svc.userId === session.user._id) {
        await ctx.db.delete(svc._id);
      }
    }

    // userSettings.primaryDeviceId — clear it if it points at the
    // device we're about to delete. Patch (not delete) the settings
    // row so the user's other prefs (runner choice, relay creds,
    // verbosity) survive.
    const settings = await ctx.db
      .query("userSettings")
      .withIndex("by_userId", (q) => q.eq("userId", session.user._id))
      .unique();
    if (settings && settings.primaryDeviceId === args.deviceId) {
      await ctx.db.patch(settings._id, { primaryDeviceId: undefined });
    }

    await ctx.db.delete(device._id);
  },
});

// ALIAS_PATTERN: a-z, 0-9, dash, underscore, dot. 1–48 chars. Lower-cased
// before storage; we intentionally reject whitespace and uppercase so
// `yaver ssh prod-mac` is the same identifier across CLI / web / mobile
// without callers having to remember the original casing the user typed.
const ALIAS_PATTERN = /^[a-z0-9._-]{1,48}$/;

/**
 * Set or clear the per-user alias for one of the caller's Yaver devices.
 *
 * Pass alias: "" (or omit) to clear. This writes the normalized alias
 * onto the device's Convex row so every surface that lists devices
 * (`yaver devices`, `yaver ssh <alias>`, web, mobile) sees the same
 * short name. Aliases are normalized to lower case before storage and
 * must be unique within a single user's set of devices — re-using an
 * alias for a different device of the same user is rejected (callers
 * should clear the old one first or pass the same deviceId to rename
 * in place).
 *
 * Throws:
 *   "Unauthorized"        — session invalid or device not owned
 *   "Device not found"
 *   "alias invalid"       — failed ALIAS_PATTERN
 *   "alias already used"  — another device of this user owns it
 */
export const setDeviceAlias = mutation({
  args: {
    tokenHash: v.string(),
    deviceId: v.string(),
    alias: v.optional(v.string()),
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

    const raw = (args.alias ?? "").trim().toLowerCase();
    if (raw === "") {
      if (device.alias !== undefined) {
        await ctx.db.patch(device._id, { alias: undefined });
      }
      return { ok: true, alias: null };
    }

    if (!ALIAS_PATTERN.test(raw)) {
      throw new Error("alias invalid: use 1-48 chars from a-z, 0-9, '.', '-', '_'");
    }

    // Per-user uniqueness — scan the caller's devices and reject if
    // any other row already holds this alias.
    const peers = await ctx.db
      .query("devices")
      .withIndex("by_userId", (q) => q.eq("userId", session.user._id))
      .collect();
    for (const peer of peers) {
      if (peer._id === device._id) continue;
      if ((peer.alias ?? "").toLowerCase() === raw) {
        throw new Error(`alias already used by device ${peer.deviceId.slice(0, 8)} (${peer.name})`);
      }
    }

    await ctx.db.patch(device._id, { alias: raw });
    return { ok: true, alias: raw };
  },
});

/**
 * One-shot backfill: assign every device a relay-auto-provisioned
 * <deviceId>.<exposeDomain> publicEndpoint if it doesn't already
 * have one. Run once after wildcard DNS + cert is wired so the
 * dashboard's transport classifier picks up the clean URL for
 * every existing box without waiting for them all to reconnect to
 * relay v0.1.11+.
 *
 * Idempotent — skips devices that already have a *.exposeDomain
 * entry. Owner-scoped mutations (caller's bearer must list each
 * device under their account); guests can't seed someone else's.
 *
 * Args:
 *   exposeDomain: the relay's expose-domain ("dev.yaver.io")
 *
 * Run once with:
 *   npx convex run devices:seedAutoPublicUrls --arg exposeDomain=dev.yaver.io
 */
export const seedAutoPublicUrls = mutation({
  args: {
    exposeDomain: v.string(),
    tokenHash: v.optional(v.string()),
  },
  handler: async (ctx, args) => {
    const domain = String(args.exposeDomain || "").trim().toLowerCase();
    if (!domain || !/^[a-z0-9.-]+$/.test(domain)) {
      throw new Error("invalid exposeDomain");
    }

    // If a tokenHash is provided we scope to that user's devices —
    // safer + matches the rest of the API surface. If no hash is
    // provided we operate fleet-wide (admin one-shot via convex
    // CLI which inherently has admin auth).
    let scopedSession: Awaited<ReturnType<typeof validateSessionInternal>> = null;
    if (args.tokenHash) {
      scopedSession = await validateSessionInternal(ctx, args.tokenHash);
      if (!scopedSession) throw new Error("Unauthorized");
    }

    const devices = scopedSession
      ? await ctx.db
          .query("devices")
          .withIndex("by_userId", (q) => q.eq("userId", scopedSession!.user._id))
          .collect()
      : await ctx.db.query("devices").collect();

    let updated = 0;
    let skipped = 0;
    for (const d of devices) {
      const deviceId = (d.deviceId || "").toLowerCase().trim();
      if (!deviceId) {
        skipped++;
        continue;
      }
      const url = `https://${deviceId}.${domain}`;
      const eps = (d.publicEndpoints ?? []) as string[];
      if (eps.includes(url)) {
        skipped++;
        continue;
      }
      await ctx.db.patch(d._id, {
        publicEndpoints: [...eps, url],
      });
      updated++;
    }
    return { ok: true, updated, skipped, total: devices.length };
  },
});
