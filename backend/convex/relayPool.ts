import { internalMutation, internalQuery } from "./_generated/server";
import { v } from "convex/values";

/**
 * ─── Shared relay pool ──────────────────────────────────────────────────────
 *
 * Relay Pro rides a SHARED multi-tenant host.
 *
 * WHY (measured 2026-07-21): a dedicated Hetzner box per subscriber — `cax11`
 * at €6.99/mo, necessarily always-on — against $9/mo revenue is **16% gross**.
 * A relay is useless when off, so it cannot scale to zero: that margin is
 * structural, not a tuning problem, and one support ticket erases it. Shared
 * across ~20 tenants the same box is €0.35/user and **96%**.
 *
 * WHY IT IS SAFE — and this is the part that must not be eroded:
 *   1. The relay is **pass-through**. It forwards ciphertext, authorizes
 *      nothing, and executes no tenant code. That is categorically different
 *      from sharing a dev box, which runs arbitrary code and holds the user's
 *      mirrored Claude/Codex credentials.
 *   2. Cross-tenant bridging is refused in **Convex**, before the relay ever
 *      forwards: `devices.ts` (signature path) and `userSettings.ts` (password
 *      path) both require signer.userId === target.userId.
 *   3. Free vs Pro is **explicitly not a security boundary** — Pro buys
 *      capacity. So co-tenanting Pro users changes no trust relationship.
 *
 * Any change that makes the relay authorize, terminate, or inspect tenant
 * traffic invalidates (1) and this whole model with it.
 */

/**
 * Tenants per shared host.
 *
 * 20 already yields 96%, so there is no reason to chase the last two points.
 * Oversubscription converts a margin win into an outage, and the relay's
 * scarce resource is BANDWIDTH (Hetzner's ~20 TB/mo allowance), not CPU — a
 * small box has ample CPU for pass-through. Raise this only with measured
 * per-tenant throughput, never optimistically.
 */
export const RELAY_TENANTS_PER_HOST = Number(process.env.YAVER_RELAY_TENANTS_PER_HOST) || 20;

/** Host key for a (region, index) slot. Stable and human-readable in logs. */
export function relayHostKey(region: string, index: number): string {
  const r = String(region || "eu").trim().toLowerCase();
  return `relay-${r}-${Math.max(0, index)}`;
}

export type RelayPoolAssignment = {
  hostKey: string;
  /** True when this tenant is the first on the host, so a box must be created. */
  needsProvision: boolean;
  tenantsOnHost: number;
  reason: string;
};

/**
 * Pure slot selection: first host in the region under capacity, else a new one.
 *
 * Deterministic and side-effect free so the packing rule can be reasoned about
 * (and tested) without touching Convex or a provider.
 */
export function selectRelayHostSlot(args: {
  region: string;
  /** Existing tenant counts, keyed by host. */
  hostCounts: Record<string, number>;
  capacity?: number;
}): RelayPoolAssignment {
  const capacity = args.capacity && args.capacity > 0 ? args.capacity : RELAY_TENANTS_PER_HOST;
  // Deliberately FIRST-FIT, not least-loaded: first-fit keeps hosts densely
  // packed so an idle host can eventually be drained and deleted. Least-loaded
  // spreads tenants evenly and guarantees every host stays half-empty forever,
  // which is the same always-on cost this pool exists to remove.
  for (let i = 0; i < 1000; i++) {
    const key = relayHostKey(args.region, i);
    const count = args.hostCounts[key] ?? 0;
    if (count < capacity) {
      return {
        hostKey: key,
        needsProvision: count === 0,
        tenantsOnHost: count + 1,
        reason: count === 0
          ? `new shared host ${key}`
          : `joined shared host ${key} (${count + 1}/${capacity})`,
      };
    }
  }
  throw new Error(`relay pool exhausted for region ${args.region}`);
}

/** Live tenant counts per shared host in a region. */
export const hostCountsForRegion = internalQuery({
  args: { region: v.string() },
  handler: async (ctx, { region }) => {
    const rows = await ctx.db.query("managedRelays").collect();
    const counts: Record<string, number> = {};
    for (const r of rows) {
      if (r.region !== region) continue;
      if (r.isDedicated) continue; // Private Relay does not consume pool slots
      // Only rows that still occupy capacity. A stopped/errored relay is not
      // serving anyone, and counting it would strand a slot forever.
      if (r.status === "stopped" || r.status === "error") continue;
      const key = r.sharedHostKey;
      if (!key) continue;
      counts[key] = (counts[key] ?? 0) + 1;
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
  args: { relayId: v.id("managedRelays"), region: v.string() },
  handler: async (ctx, { relayId, region }): Promise<RelayPoolAssignment> => {
    const relay = await ctx.db.get(relayId);
    if (!relay) throw new Error("relay not found");
    // Already placed — assignment must be idempotent, because a retried
    // webhook must never migrate a live tenant to a different host.
    if (relay.sharedHostKey) {
      return {
        hostKey: relay.sharedHostKey,
        needsProvision: false,
        tenantsOnHost: 0,
        reason: `already on ${relay.sharedHostKey}`,
      };
    }
    const rows = await ctx.db.query("managedRelays").collect();
    const counts: Record<string, number> = {};
    for (const r of rows) {
      if (r.region !== region || r.isDedicated) continue;
      if (r.status === "stopped" || r.status === "error") continue;
      if (!r.sharedHostKey) continue;
      counts[r.sharedHostKey] = (counts[r.sharedHostKey] ?? 0) + 1;
    }
    const slot = selectRelayHostSlot({ region, hostCounts: counts });
    await ctx.db.patch(relayId, {
      sharedHostKey: slot.hostKey,
      updatedAt: Date.now(),
    });
    return slot;
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
    const rows = await ctx.db.query("managedRelays").collect();
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
  handler: async (ctx, { hostKey }): Promise<{ serverId: string; serverIp: string } | null> => {
    const rows = await ctx.db.query("managedRelays").collect();
    for (const r of rows) {
      if (r.sharedHostKey !== hostKey) continue;
      if (r.status === "stopped" || r.status === "error") continue;
      if (r.hetznerServerId && r.serverIp) {
        return { serverId: String(r.hetznerServerId), serverIp: String(r.serverIp) };
      }
    }
    return null;
  },
});
