import { internalMutation, internalQuery } from "./_generated/server";
import { v } from "convex/values";

/**
 * ─── Shared relay pool ──────────────────────────────────────────────────────
 *
 * Relay Pro rides a SHARED multi-tenant host.
 *
 * WHY: a dedicated always-on VM per $9 subscriber leaves little or negative
 * contribution after merchant-of-record fees and provider price changes. A
 * relay cannot scale to zero while remaining reachable. Pool hosts therefore
 * pack conservatively to 20 tenants, while provisioning enforces an absolute
 * €0.04/hour host ceiling (about €1.46/tenant/month when full). Revisit both
 * values from measured traffic and current invoices, never an old SKU price.
 *
 * WHY SHARING IS SAFE — and this is the part that must not be eroded:
 *   1. The relay executes no tenant code and holds no coding-agent/provider
 *      credentials. It does terminate its own transport and process proxy
 *      envelopes, so do not market the HTTP relay path as end-to-end encrypted.
 *   2. Cross-tenant bridging is refused by Convex-backed authorization:
 *      `devices.ts` (signature path) and `userSettings.ts` (password path)
 *      require signer.userId === target.userId, and the target agent applies
 *      its own bearer/device authorization again.
 *   3. Free vs Pro is **explicitly not a security boundary** — Pro buys
 *      capacity. So co-tenanting Pro users changes no trust relationship.
 *
 * Any change that weakens same-owner/access-graph checks or lets the relay
 * execute tenant workloads invalidates this model.
 */

/**
 * Pure pool policy (slot selection, deletion decision, snapshot decision) —
 * importable without Convex so it is unit-testable under node
 * --experimental-strip-types. See relayPoolPolicy.ts.
 */
import type { RelayPoolAssignment } from "./relayPoolPolicy";
import {
  RELAY_TENANTS_PER_HOST,
  relayHostKey,
  relayHostKeyFromSeed,
  relayPublicHostname,
  selectRelayHostSlot,
  sharedHostDeletionDecision,
  sharedHostGraceSnapshotDecision,
} from "./relayPoolPolicy";
import { relayFleetCreationDecision } from "./relayDeploymentPolicy";
export type { RelayPoolAssignment } from "./relayPoolPolicy";
export {
  RELAY_TENANTS_PER_HOST,
  relayHostKey,
  relayHostKeyFromSeed,
  relayPublicHostname,
  selectRelayHostSlot,
  sharedHostDeletionDecision,
  sharedHostGraceSnapshotDecision,
};

/** Live tenant counts per shared host in a region. This diagnostic reads one
 * row per physical host, not one row per subscriber. */
export const hostCountsForRegion = internalQuery({
  args: { region: v.string() },
  handler: async (ctx, { region }) => {
    const rows = await ctx.db
      .query("relayPoolHosts")
      .withIndex("by_region_placement", (q) => q.eq("region", region))
      .collect();
    const counts: Record<string, number> = {};
    for (const row of rows) {
      if (row.lifecycleStatus === "deleted") continue;
      counts[row.hostKey] = row.tenantCount;
    }
    return counts;
  },
});

/**
 * Assign a relay row to a shared host, returning whether a box must be created.
 *
 * The caller provisions only when `needsProvision` is true — that is the entire
 * saving. Every subsequent tenant in the region reuses the existing box and
 * costs nothing but its share.
 */
export const assignToPool = internalMutation({
  args: {
    relayId: v.id("managedRelays"),
    region: v.string(),
    allowDynamicHost: v.boolean(),
    maxDynamicHosts: v.number(),
  },
  handler: async (ctx, { relayId, region, allowDynamicHost, maxDynamicHosts }): Promise<RelayPoolAssignment> => {
    const relay = await ctx.db.get(relayId);
    if (!relay) throw new Error("relay not found");
    // Already placed — assignment must be idempotent, because a retried
    // webhook must never migrate a live tenant to a different host.
    if (relay.sharedHostKey) {
      const host = await ctx.db
        .query("relayPoolHosts")
        .withIndex("by_host_key", (q) => q.eq("hostKey", relay.sharedHostKey!))
        .first();
      const now = Date.now();
      const mayReclaimProvision = Boolean(
        host && !host.serverId && host.lifecycleStatus === "provisioning" &&
        Number(host.provisionLeaseUntil || 0) <= now,
      );
      if (host && mayReclaimProvision) {
        await ctx.db.patch(host._id, {
          provisionLeaseUntil: now + 10 * 60_000,
          updatedAt: now,
        });
      }
      return {
        hostKey: relay.sharedHostKey,
        needsProvision: mayReclaimProvision,
        tenantsOnHost: host?.tenantCount ?? 0,
        reason: mayReclaimProvision
          ? `reclaimed expired provisioning lease for ${relay.sharedHostKey}`
          : `already on ${relay.sharedHostKey}`,
      };
    }
    const capacity = RELAY_TENANTS_PER_HOST;
    // Convex mutations are serializable, so selecting and incrementing the
    // indexed open-host row in this transaction cannot oversubscribe it.
    const openHosts = await ctx.db
      .query("relayPoolHosts")
      .withIndex("by_region_placement", (q) =>
        q.eq("region", region).eq("placementStatus", "available"),
      )
      .take(8);
    const open = openHosts.find(
      (host) => host.tenantCount < host.capacity &&
        (host.lifecycleStatus === "provisioning" || host.lifecycleStatus === "active"),
    );
    if (open) {
      const tenantsOnHost = open.tenantCount + 1;
      await ctx.db.patch(open._id, {
        tenantCount: tenantsOnHost,
        placementStatus: tenantsOnHost >= open.capacity ? "full" : "available",
        updatedAt: Date.now(),
      });
      await ctx.db.patch(relayId, { sharedHostKey: open.hostKey, updatedAt: Date.now() });
      return {
        hostKey: open.hostKey,
        needsProvision: false,
        tenantsOnHost,
        reason: `joined shared host ${open.hostKey} (${tenantsOnHost}/${open.capacity})`,
      };
    }

    // This mutation is the final spend boundary before a provider call. Keep
    // the decision in the same serializable transaction as placement so two
    // simultaneous purchases cannot both observe the final fleet slot.
    const allHosts = await ctx.db.query("relayPoolHosts").collect();
    const dynamicHosts = allHosts.filter(
      (host) => !host.pinned && host.lifecycleStatus !== "deleted",
    ).length;
    const fleetDecision = relayFleetCreationDecision({
      dynamicProvisioningEnabled: allowDynamicHost,
      currentDynamicHosts: dynamicHosts,
      maxDynamicHosts: Math.max(0, Math.floor(maxDynamicHosts)),
    });
    if (!fleetDecision.allow) throw new Error(fleetDecision.reason);

    const hostKey = relayHostKeyFromSeed(region, String(relayId));
    await ctx.db.insert("relayPoolHosts", {
      hostKey,
      region,
      placementStatus: capacity <= 1 ? "full" : "available",
      lifecycleStatus: "provisioning",
      hybrid: true,
      pinned: false,
      tenantCount: 1,
      capacity,
      provisionLeaseUntil: Date.now() + 10 * 60_000,
      createdAt: Date.now(),
      updatedAt: Date.now(),
    });
    await ctx.db.patch(relayId, {
      sharedHostKey: hostKey,
      updatedAt: Date.now(),
    });
    return {
      hostKey,
      needsProvision: true,
      tenantsOnHost: 1,
      reason: `new shared host ${hostKey}`,
    };
  },
});

/** Adopt the already-running public relay as a pinned hybrid Free + Pro node.
 * Inventory comes from deployment env, never source. Repeated calls are safe;
 * a conflicting provider id is rejected rather than silently repointing the
 * control plane at a different server. */
export const upsertPinnedHybridHost = internalMutation({
  args: {
    hostKey: v.string(),
    region: v.string(),
    hostname: v.string(),
    serverId: v.string(),
    serverIp: v.string(),
    serverIpv6: v.optional(v.string()),
    capacity: v.number(),
  },
  handler: async (ctx, args) => {
    const existing = await ctx.db
      .query("relayPoolHosts")
      .withIndex("by_host_key", (q) => q.eq("hostKey", args.hostKey))
      .first();
    const now = Date.now();
    if (existing) {
      if (existing.serverId && existing.serverId !== args.serverId) {
        throw new Error(`Pinned relay ${args.hostKey} conflicts with existing provider inventory`);
      }
      const capacity = Math.max(1, Math.floor(args.capacity));
      await ctx.db.patch(existing._id, {
        region: args.region,
        hostname: args.hostname,
        serverId: args.serverId,
        serverIp: args.serverIp,
        serverIpv6: args.serverIpv6,
        capacity,
        pinned: true,
        hybrid: true,
        lifecycleStatus: "active",
        placementStatus: existing.tenantCount >= capacity ? "full" : "available",
        provisionLeaseUntil: undefined,
        errorMessage: undefined,
        updatedAt: now,
      });
      return existing._id;
    }
    return await ctx.db.insert("relayPoolHosts", {
      hostKey: args.hostKey,
      region: args.region,
      hostname: args.hostname,
      serverId: args.serverId,
      serverIp: args.serverIp,
      serverIpv6: args.serverIpv6,
      pinned: true,
      hybrid: true,
      placementStatus: "available",
      lifecycleStatus: "active",
      tenantCount: 0,
      capacity: Math.max(1, Math.floor(args.capacity)),
      createdAt: now,
      updatedAt: now,
    });
  },
});

/** Publish the provider endpoint once the first tenant creates the host. */
export const recordHostEndpoint = internalMutation({
  args: {
    hostKey: v.string(),
    serverId: v.string(),
    serverIp: v.string(),
    serverIpv6: v.optional(v.string()),
    hostname: v.optional(v.string()),
  },
  handler: async (ctx, args) => {
    const host = await ctx.db
      .query("relayPoolHosts")
      .withIndex("by_host_key", (q) => q.eq("hostKey", args.hostKey))
      .first();
    if (!host) throw new Error(`relay pool host ${args.hostKey} not found`);
    await ctx.db.patch(host._id, {
      serverId: args.serverId,
      serverIp: args.serverIp,
      serverIpv6: args.serverIpv6,
      hostname: args.hostname,
      provisionLeaseUntil: undefined,
      lifecycleStatus: "active",
      errorMessage: undefined,
      updatedAt: Date.now(),
    });
  },
});

export const markHostError = internalMutation({
  args: { hostKey: v.string(), errorMessage: v.string() },
  handler: async (ctx, args) => {
    const host = await ctx.db
      .query("relayPoolHosts")
      .withIndex("by_host_key", (q) => q.eq("hostKey", args.hostKey))
      .first();
    if (!host) return;
    await ctx.db.patch(host._id, {
      placementStatus: "closed",
      lifecycleStatus: "error",
      errorMessage: args.errorMessage,
      updatedAt: Date.now(),
    });
  },
});

/** Release a ledger slot when placement failed before Hetzner returned a
 * server id. It is safe to make the row reusable only by deleting it from
 * placement: no provider resource exists, so this cannot orphan a bill. */
export const abandonUncreatedHost = internalMutation({
  args: { hostKey: v.string() },
  handler: async (ctx, { hostKey }) => {
    const host = await ctx.db
      .query("relayPoolHosts")
      .withIndex("by_host_key", (q) => q.eq("hostKey", hostKey))
      .first();
    if (!host || host.pinned || host.serverId) return false;
    await ctx.db.patch(host._id, {
      placementStatus: "closed",
      lifecycleStatus: "deleted",
      tenantCount: 0,
      provisionLeaseUntil: undefined,
      updatedAt: Date.now(),
    });
    return true;
  },
});

/** Atomically release a tenant slot. Repeated deprovision calls are harmless. */
export const releaseTenant = internalMutation({
  args: { relayId: v.id("managedRelays") },
  handler: async (ctx, { relayId }) => {
    const relay = await ctx.db.get(relayId);
    if (!relay) return { hostKey: null, tenants: 0, empty: true, pinned: false };
    const hostKey = relay.sharedHostKey ?? null;
    if (relay.status !== "stopped") {
      await ctx.db.patch(relayId, { status: "stopped", updatedAt: Date.now() });
    }
    if (!hostKey) return { hostKey: null, tenants: 0, empty: true, pinned: false };

    const host = await ctx.db
      .query("relayPoolHosts")
      .withIndex("by_host_key", (q) => q.eq("hostKey", hostKey))
      .first();
    if (host) {
      const tenants = relay.status === "stopped"
        ? host.tenantCount
        : Math.max(0, host.tenantCount - 1);
      await ctx.db.patch(host._id, {
        tenantCount: tenants,
        placementStatus:
          (tenants === 0 && !host.pinned) || host.lifecycleStatus === "error"
            ? "closed"
            : tenants >= host.capacity ? "full" : "available",
        lifecycleStatus: tenants === 0 && !host.pinned ? "draining" : host.lifecycleStatus,
        updatedAt: Date.now(),
      });
      return { hostKey, tenants, empty: tenants === 0, pinned: Boolean(host.pinned) };
    }

    // Compatibility for pre-ledger v2 hosts. The indexed lookup is bounded to
    // this physical host instead of scanning the entire subscriber table.
    const rows = await ctx.db
      .query("managedRelays")
      .withIndex("by_shared_host", (q) => q.eq("sharedHostKey", hostKey))
      .collect();
    const tenants = rows.filter((row) => row.status !== "stopped" && row.status !== "error").length;
    return { hostKey, tenants, empty: tenants === 0, pinned: false };
  },
});

export const markHostDeleted = internalMutation({
  args: { hostKey: v.string() },
  handler: async (ctx, { hostKey }) => {
    const host = await ctx.db
      .query("relayPoolHosts")
      .withIndex("by_host_key", (q) => q.eq("hostKey", hostKey))
      .first();
    if (!host) return;
    if (host.pinned) throw new Error(`refusing to delete pinned relay host ${hostKey}`);
    await ctx.db.patch(host._id, {
      placementStatus: "closed",
      lifecycleStatus: "deleted",
      tenantCount: 0,
      updatedAt: Date.now(),
    });
  },
});

/**
 * Does this host still serve anyone? Input to draining an empty host.
 *
 * An empty shared host is the pool's own version of the always-on cost this
 * design removes — it must be deleted, not left running, exactly like any other
 * idle Hetzner box.
 */
export const hostIsEmpty = internalQuery({
  args: { hostKey: v.string() },
  handler: async (ctx, { hostKey }) => {
    const host = await ctx.db
      .query("relayPoolHosts")
      .withIndex("by_host_key", (q) => q.eq("hostKey", hostKey))
      .first();
    if (host) return { hostKey, tenants: host.tenantCount, empty: host.tenantCount === 0 };
    const rows = await ctx.db
      .query("managedRelays")
      .withIndex("by_shared_host", (q) => q.eq("sharedHostKey", hostKey))
      .collect();
    const live = rows.filter(
      (r) => r.sharedHostKey === hostKey && r.status !== "stopped" && r.status !== "error",
    );
    return { hostKey, tenants: live.length, empty: live.length === 0 };
  },
});

/**
 * The provider box already serving a pool slot, if any.
 *
 * This is what turns the pool from bookkeeping into a real saving: when it
 * returns a server, the caller must NOT create another one. Reading any live
 * tenant on the host is sufficient — they all point at the same box by
 * construction.
 */
export const hostEndpoint = internalQuery({
  args: { hostKey: v.string() },
  handler: async (ctx, { hostKey }): Promise<{
    serverId: string;
    serverIp: string;
    serverIpv6?: string;
    hostname?: string;
    pinned?: boolean;
  } | null> => {
    const host = await ctx.db
      .query("relayPoolHosts")
      .withIndex("by_host_key", (q) => q.eq("hostKey", hostKey))
      .first();
    if (host?.lifecycleStatus === "active" && host.serverId && host.serverIp) {
      return {
        serverId: host.serverId,
        serverIp: host.serverIp,
        ...(host.serverIpv6 ? { serverIpv6: host.serverIpv6 } : {}),
        ...(host.hostname ? { hostname: host.hostname } : {}),
        ...(host.pinned ? { pinned: true } : {}),
      };
    }
    const rows = await ctx.db
      .query("managedRelays")
      .withIndex("by_shared_host", (q) => q.eq("sharedHostKey", hostKey))
      .collect();
    for (const r of rows) {
      if (r.sharedHostKey !== hostKey) continue;
      if (r.status === "stopped" || r.status === "error") continue;
      if (r.hetznerServerId && r.serverIp) {
        return {
          serverId: String(r.hetznerServerId),
          serverIp: String(r.serverIp),
          ...(r.serverIpv6 ? { serverIpv6: String(r.serverIpv6) } : {}),
        };
      }
    }
    return null;
  },
});
