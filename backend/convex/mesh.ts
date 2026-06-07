// Yaver Mesh control-plane mutations/queries — the optional WireGuard overlay
// (Tailscale alternative). Called by the Yaver agent (desktop/agent/mesh_cmd.go)
// when the user opts in via `yaver mesh up`, and read by the web/mobile console.
//
// PRIVACY: meshNodes rows hold PUBLIC keys + UDP endpoints + the assigned
// overlay IP ONLY. The WireGuard private key never leaves the device (it lives
// in the agent vault). `wgPrivateKey` is on the Convex forbidden-field list and
// desktop/agent/convex_privacy_test.go pins that the join payload is clean.
//
// Mesh membership mirrors the EXISTING sharing model: a peer is visible to you
// if it is your own device OR a device shared to you through an active
// infraAccessGrant. There is no new sharing primitive — `listMeshPeers` derives
// the topology from the same grant tables the rest of the app uses.

import { v } from "convex/values";
import { mutation, query } from "./_generated/server";
import type { Id } from "./_generated/dataModel";
import { resolveUser } from "./agentSync";
import { validateSessionInternal } from "./auth";
import {
  listActiveInfraGrantsForGuest,
  listGrantedDeviceIdsForGrant,
} from "./access";

// webUser resolves the session-token-hash the web/mobile console sends into a
// userId (the agent uses ctx.auth identity via resolveUser; the console uses a
// hashed session token, same as devices/list). Throws Unauthorized on a stale
// or unknown token so the client can route to re-auth.
async function webUser(ctx: any, tokenHash: string): Promise<Id<"users">> {
  const session = await validateSessionInternal(ctx, tokenHash);
  if (!session) throw new Error("Unauthorized");
  return session.user._id;
}

// Yaver's overlay address space. Deliberately OUTSIDE Tailscale's 100.64.0.0/10
// CGNAT range so a machine can run both Tailscale and Yaver Mesh side by side.
// 100.96.0.0/12 = 100.96.0.0 .. 100.111.255.255 (~1M hosts).
const MESH_BASE = (100 << 24) | (96 << 16); // 100.96.0.0
const MESH_HOST_MIN = 2; // skip .0 (network) and .1 (reserved gateway)
const MESH_HOST_MAX = (1 << 20) - 2; // /12 host space

function intToIPv4(n: number): string {
  return [
    (n >>> 24) & 0xff,
    (n >>> 16) & 0xff,
    (n >>> 8) & 0xff,
    n & 0xff,
  ].join(".");
}

function ipv4ToInt(ip: string): number {
  const parts = ip.split(".").map((p) => parseInt(p, 10));
  if (parts.length !== 4 || parts.some((p) => Number.isNaN(p) || p < 0 || p > 255)) {
    return 0;
  }
  return ((parts[0] << 24) | (parts[1] << 16) | (parts[2] << 8) | parts[3]) >>> 0;
}

/**
 * Allocate the lowest free overlay IP across ALL meshNodes. Convex serializes
 * conflicting transactions, so a scan-then-insert is collision-safe under
 * concurrent joins (a loser retries and sees the winner's row).
 */
async function allocateMeshIPv4(ctx: any): Promise<string> {
  const all = await ctx.db.query("meshNodes").collect();
  const used = new Set<number>();
  for (const row of all) {
    const n = ipv4ToInt(row.meshIPv4);
    if (n) used.add(n);
  }
  for (let host = MESH_HOST_MIN; host <= MESH_HOST_MAX; host++) {
    const candidate = (MESH_BASE | host) >>> 0;
    if (!used.has(candidate)) return intToIPv4(candidate);
  }
  throw new Error("mesh address space exhausted");
}

type JoinArgs = {
  deviceId: string;
  wgPublicKey: string;
  endpoints: string[];
  meshIPv6?: string;
  advertisedRoutes?: string[];
  isExitNode?: boolean;
};

async function joinMeshForUser(ctx: any, userId: Id<"users">, args: JoinArgs) {
  const existing = await ctx.db
    .query("meshNodes")
    .withIndex("by_device", (q: any) => q.eq("deviceId", args.deviceId))
    .first();

  const now = Date.now();
  if (existing) {
    await ctx.db.patch(existing._id, {
      wgPublicKey: args.wgPublicKey,
      endpoints: args.endpoints,
      meshIPv6: args.meshIPv6 ?? existing.meshIPv6,
      advertisedRoutes: args.advertisedRoutes ?? existing.advertisedRoutes,
      isExitNode: args.isExitNode ?? existing.isExitNode,
      online: true,
      updatedAt: now,
    });
    return { meshIPv4: existing.meshIPv4, meshIPv6: existing.meshIPv6 };
  }

  const meshIPv4 = await allocateMeshIPv4(ctx);
  await ctx.db.insert("meshNodes", {
    userId,
    deviceId: args.deviceId,
    wgPublicKey: args.wgPublicKey,
    meshIPv4,
    meshIPv6: args.meshIPv6,
    endpoints: args.endpoints,
    advertisedRoutes: args.advertisedRoutes,
    isExitNode: args.isExitNode,
    online: true,
    updatedAt: now,
  });
  return { meshIPv4, meshIPv6: args.meshIPv6 };
}

/**
 * Join the mesh (or refresh an existing node). Assigns a stable overlay IP on
 * first join and reuses it thereafter. Agent entry point (Convex identity).
 */
export const joinMesh = mutation({
  args: {
    deviceId: v.string(),
    wgPublicKey: v.string(),
    endpoints: v.array(v.string()),
    meshIPv6: v.optional(v.string()),
    advertisedRoutes: v.optional(v.array(v.string())),
    isExitNode: v.optional(v.boolean()),
  },
  handler: async (ctx, args) => {
    const userId = await resolveUser(ctx);
    return joinMeshForUser(ctx, userId, args);
  },
});

/** Mark this device offline / remove it from the mesh on `yaver mesh down`. */
export const leaveMesh = mutation({
  args: { deviceId: v.string() },
  handler: async (ctx, args) => {
    await resolveUser(ctx);
    const existing = await ctx.db
      .query("meshNodes")
      .withIndex("by_device", (q: any) => q.eq("deviceId", args.deviceId))
      .first();
    if (existing) {
      await ctx.db.patch(existing._id, { online: false, updatedAt: Date.now() });
    }
    return null;
  },
});

/** Refresh endpoints + liveness from the agent heartbeat while the mesh is up. */
export const updateMeshEndpoints = mutation({
  args: {
    deviceId: v.string(),
    endpoints: v.array(v.string()),
    lastHandshake: v.optional(v.number()),
    online: v.optional(v.boolean()),
  },
  handler: async (ctx, args) => {
    await resolveUser(ctx);
    const existing = await ctx.db
      .query("meshNodes")
      .withIndex("by_device", (q: any) => q.eq("deviceId", args.deviceId))
      .first();
    if (!existing) return null;
    await ctx.db.patch(existing._id, {
      endpoints: args.endpoints,
      lastHandshake: args.lastHandshake ?? existing.lastHandshake,
      online: args.online ?? true,
      updatedAt: Date.now(),
    });
    return null;
  },
});

/**
 * List the mesh peers visible to the caller: their own devices' mesh nodes plus
 * any nodes belonging to devices shared to them via an active infra grant. This
 * is the agent's source of truth for which WireGuard peers to configure.
 */
async function meshPeersForUser(ctx: any, userId: Id<"users">) {
    const own = await ctx.db
      .query("meshNodes")
      .withIndex("by_user", (q: any) => q.eq("userId", userId))
      .collect();

    // Devices shared TO this user through active infra grants.
    const grants = await listActiveInfraGrantsForGuest(ctx, userId);
    const sharedDeviceIds = new Set<string>();
    for (const grant of grants) {
      if (grant.shareAllDevices) {
        const hostNodes = await ctx.db
          .query("meshNodes")
          .withIndex("by_user", (q: any) => q.eq("userId", grant.hostUserId))
          .collect();
        for (const n of hostNodes) sharedDeviceIds.add(n.deviceId);
      } else {
        const ids = await listGrantedDeviceIdsForGrant(ctx, grant._id);
        for (const id of ids) sharedDeviceIds.add(id);
      }
    }

    const sharedNodes = [];
    const seenDevice = new Set<string>(own.map((n: any) => n.deviceId));
    for (const deviceId of sharedDeviceIds) {
      const node = await ctx.db
        .query("meshNodes")
        .withIndex("by_device", (q: any) => q.eq("deviceId", deviceId))
        .first();
      if (node && node.userId !== userId && !seenDevice.has(node.deviceId)) {
        seenDevice.add(node.deviceId);
        sharedNodes.push(node);
      }
    }

    // Reverse direction: for grants where I'm the HOST, the GUEST's nodes must
    // also be WG peers, or the return path fails — WireGuard requires BOTH ends
    // to list each other. This is what lets a supporter actually reach a friend
    // who shared a device to them (and vice-versa for any infra grant).
    const now = Date.now();
    const hostGrants = (
      await ctx.db
        .query("infraAccessGrants")
        .withIndex("by_hostUserId", (q: any) => q.eq("hostUserId", userId))
        .filter((q: any) => q.eq(q.field("status"), "active"))
        .collect()
    ).filter((g: any) => !g.expiresAt || g.expiresAt > now);
    const counterpartNodes = [];
    const counterpartSeen = new Set<string>();
    for (const g of hostGrants) {
      if (counterpartSeen.has(String(g.guestUserId))) continue;
      counterpartSeen.add(String(g.guestUserId));
      const guestNodes = await ctx.db
        .query("meshNodes")
        .withIndex("by_user", (q: any) => q.eq("userId", g.guestUserId))
        .collect();
      for (const n of guestNodes) {
        if (n.userId === userId || seenDevice.has(n.deviceId)) continue;
        seenDevice.add(n.deviceId);
        counterpartNodes.push(n);
      }
    }

    // Resolve display/telemetry metadata from the device row (alias, platform,
    // agent version). The meshNodes row holds only the WireGuard-relevant state;
    // these fields live on `devices` and are ALREADY in Convex (read-only here,
    // so no new sync path / no privacy-test change). Phone mesh nodes have no
    // device row → meta is undefined and the fields are simply absent.
    const deviceMetaFor = async (
      deviceId: string
    ): Promise<{ alias?: string; os?: string; clientVersion?: string }> => {
      const dev = await ctx.db
        .query("devices")
        .withIndex("by_deviceId", (q: any) => q.eq("deviceId", deviceId))
        .first();
      if (!dev) return {};
      return { alias: dev.alias ?? dev.name, os: dev.platform, clientVersion: dev.agentVersion };
    };

    const shape = async (n: any, scope: "owner" | "shared" | "peer") => {
      const meta = await deviceMetaFor(n.deviceId);
      return {
      deviceId: n.deviceId,
      ownerUserId: n.userId, // for agent-side ACL "user" resolution
      alias: meta.alias,
      // MagicDNS name (<alias>.mesh) — the overlay's resolvable hostname,
      // surfaced so the mobile/web node detail can show + copy it.
      magicDns: meta.alias ? `${meta.alias}.mesh` : undefined,
      os: meta.os,
      clientVersion: meta.clientVersion,
      wgPublicKey: n.wgPublicKey,
      meshIPv4: n.meshIPv4,
      meshIPv6: n.meshIPv6,
      endpoints: n.endpoints,
      advertisedRoutes: n.advertisedRoutes ?? [],
      isExitNode: n.isExitNode ?? false,
      online: n.online,
      lastHandshake: n.lastHandshake,
      accessScope: scope,
      // Desired state (console intent) — echoed so the agent converges and the
      // UI reflects pending changes.
      wantEnabled: n.wantEnabled,
      wantExitNode: n.wantExitNode ?? false,
      wantUseExitNode: n.wantUseExitNode ?? "",
      wantRoutes: n.wantRoutes ?? [],
      desiredAt: n.desiredAt,
      };
    };

    return {
      peers: [
        ...(await Promise.all(own.map((n: any) => shape(n, "owner")))),
        ...(await Promise.all(sharedNodes.map((n: any) => shape(n, "shared")))),
        ...(await Promise.all(counterpartNodes.map((n: any) => shape(n, "peer")))),
      ],
    };
}

/** Agent entry point — auth via Convex identity. */
export const listMeshPeers = query({
  args: {},
  handler: async (ctx) => meshPeersForUser(ctx, await resolveUser(ctx)),
});

/** Web/mobile console entry point — auth via session-token hash. */
export const listMeshPeersWeb = query({
  args: { tokenHash: v.string() },
  handler: async (ctx, { tokenHash }) => meshPeersForUser(ctx, await webUser(ctx, tokenHash)),
});

// ---------------------------------------------------------------------------
// Phase 4 — ACLs + tags. Schema + mutations land now so the control plane is
// complete; the agent-side packet filter that consumes them is built in Phase 4.
// ---------------------------------------------------------------------------

const aclRule = v.object({
  srcType: v.union(
    v.literal("tag"),
    v.literal("device"),
    v.literal("user"),
    v.literal("any")
  ),
  src: v.string(),
  dstType: v.union(
    v.literal("tag"),
    v.literal("device"),
    v.literal("user"),
    v.literal("any")
  ),
  dst: v.string(),
  ports: v.array(v.string()),
  action: v.union(v.literal("accept"), v.literal("drop")),
});

async function setMeshAclsForUser(ctx: any, userId: Id<"users">, rules: any[]) {
  const existing = await ctx.db
    .query("meshAcls")
    .withIndex("by_user", (q: any) => q.eq("userId", userId))
    .collect();
  for (const row of existing) await ctx.db.delete(row._id);
  const now = Date.now();
  for (const rule of rules) {
    await ctx.db.insert("meshAcls", { userId, ...rule, updatedAt: now });
  }
  return null;
}

async function meshAclsForUser(ctx: any, userId: Id<"users">) {
  const rows = await ctx.db
    .query("meshAcls")
    .withIndex("by_user", (q: any) => q.eq("userId", userId))
    .collect();
  return rows.map((r: any) => ({
    srcType: r.srcType,
    src: r.src,
    dstType: r.dstType,
    dst: r.dst,
    ports: r.ports,
    action: r.action,
  }));
}

async function meshTagsForUser(ctx: any, userId: Id<"users">) {
  const rows = await ctx.db
    .query("meshTags")
    .withIndex("by_user", (q: any) => q.eq("userId", userId))
    .collect();
  return rows.map((r: any) => ({ deviceId: r.deviceId, tag: r.tag }));
}

async function tagDeviceForUser(ctx: any, userId: Id<"users">, deviceId: string, tags: string[]) {
  const existing = await ctx.db
    .query("meshTags")
    .withIndex("by_device", (q: any) => q.eq("deviceId", deviceId))
    .collect();
  for (const row of existing) {
    if (row.userId === userId) await ctx.db.delete(row._id);
  }
  const now = Date.now();
  for (const tag of tags) {
    await ctx.db.insert("meshTags", { userId, deviceId, tag, updatedAt: now });
  }
  return null;
}

/** Replace the caller's full mesh ACL ruleset (whole-set upsert). */
export const setMeshAcls = mutation({
  args: { rules: v.array(aclRule) },
  handler: async (ctx, args) => setMeshAclsForUser(ctx, await resolveUser(ctx), args.rules),
});

/** Read the caller's mesh ACL ruleset (agent). */
export const listMeshAcls = query({
  args: {},
  handler: async (ctx) => meshAclsForUser(ctx, await resolveUser(ctx)),
});

/** List the caller's device tags, for the agent to resolve tag-based ACLs. */
export const listMeshTags = query({
  args: {},
  handler: async (ctx) => meshTagsForUser(ctx, await resolveUser(ctx)),
});

/** Set the tags on a device (whole-set replace for that device). */
export const tagDevice = mutation({
  args: { deviceId: v.string(), tags: v.array(v.string()) },
  handler: async (ctx, args) => tagDeviceForUser(ctx, await resolveUser(ctx), args.deviceId, args.tags),
});

// --- Web/mobile console entry points (session-token-hash auth) ---

export const listMeshAclsWeb = query({
  args: { tokenHash: v.string() },
  handler: async (ctx, { tokenHash }) => meshAclsForUser(ctx, await webUser(ctx, tokenHash)),
});

export const setMeshAclsWeb = mutation({
  args: { tokenHash: v.string(), rules: v.array(aclRule) },
  handler: async (ctx, { tokenHash, rules }) => setMeshAclsForUser(ctx, await webUser(ctx, tokenHash), rules),
});

export const listMeshTagsWeb = query({
  args: { tokenHash: v.string() },
  handler: async (ctx, { tokenHash }) => meshTagsForUser(ctx, await webUser(ctx, tokenHash)),
});

export const tagDeviceWeb = mutation({
  args: { tokenHash: v.string(), deviceId: v.string(), tags: v.array(v.string()) },
  handler: async (ctx, { tokenHash, deviceId, tags }) => tagDeviceForUser(ctx, await webUser(ctx, tokenHash), deviceId, tags),
});

/**
 * Set DESIRED mesh config on a node from the console (exit-node advertisement,
 * which exit node to route through, subnet routes, or enable/disable). The
 * agent that owns the node converges to this on its next reconcile tick — the
 * Tailscale model where the control plane holds intent. Owner-only.
 */
/** Mobile/console entry point for joining the mesh (session-token-hash auth) —
 *  used by the on-device tunnel (docs/mesh-mobile-tunnel.md) so the phone gets
 *  an overlay IP without a Convex-identity token. */
export const joinMeshWeb = mutation({
  args: {
    tokenHash: v.string(),
    deviceId: v.string(),
    wgPublicKey: v.string(),
    endpoints: v.array(v.string()),
    meshIPv6: v.optional(v.string()),
    advertisedRoutes: v.optional(v.array(v.string())),
    isExitNode: v.optional(v.boolean()),
  },
  handler: async (ctx, { tokenHash, ...args }) =>
    joinMeshForUser(ctx, await webUser(ctx, tokenHash), args),
});

/** Console leave (session-token-hash auth). */
export const leaveMeshWeb = mutation({
  args: { tokenHash: v.string(), deviceId: v.string() },
  handler: async (ctx, { tokenHash, deviceId }) => {
    await webUser(ctx, tokenHash);
    const existing = await ctx.db
      .query("meshNodes")
      .withIndex("by_device", (q: any) => q.eq("deviceId", deviceId))
      .first();
    if (existing) await ctx.db.patch(existing._id, { online: false, updatedAt: Date.now() });
    return null;
  },
});

export const setMeshNodeConfigWeb = mutation({
  args: {
    tokenHash: v.string(),
    deviceId: v.string(),
    wantEnabled: v.optional(v.boolean()),
    wantExitNode: v.optional(v.boolean()),
    wantUseExitNode: v.optional(v.string()),
    wantRoutes: v.optional(v.array(v.string())),
  },
  handler: async (ctx, args) => {
    const userId = await webUser(ctx, args.tokenHash);
    const node = await ctx.db
      .query("meshNodes")
      .withIndex("by_device", (q) => q.eq("deviceId", args.deviceId))
      .first();
    if (!node) throw new Error("node not found");
    if (node.userId !== userId) throw new Error("Forbidden: not your node");
    const patch: Record<string, unknown> = { desiredAt: Date.now() };
    if (args.wantEnabled !== undefined) patch.wantEnabled = args.wantEnabled;
    if (args.wantExitNode !== undefined) patch.wantExitNode = args.wantExitNode;
    if (args.wantUseExitNode !== undefined) patch.wantUseExitNode = args.wantUseExitNode;
    if (args.wantRoutes !== undefined) patch.wantRoutes = args.wantRoutes;
    await ctx.db.patch(node._id, patch);
    return null;
  },
});
