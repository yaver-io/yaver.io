import { internalMutation, internalQuery } from "./_generated/server";
import { v } from "convex/values";

/**
 * ─── Shared serverless host pool ────────────────────────────────────────────
 *
 * Yaver Serverless hosts the user's backend. A backend must STAY UP to serve
 * requests, so it can never park — which removes the one mechanism that makes
 * Cloud Workspace profitable.
 *
 * MEASURED 2026-07-21: a dedicated always-on box (`cpx22`, €22.99/mo) against
 * $29/mo revenue is 14% gross; clearing a 70% floor dedicated would need ~$95,
 * which is not a price this product can carry. Shared across ~20 tenants the
 * same box is €1.15/user → 86%.
 *
 * So the same rule that fixed Relay Pro applies: **either it parks, or it's
 * shared.** Serverless cannot park, therefore it must be shared.
 *
 * ─── ⚠️ THE ISOLATION CAVEAT — read before extending this ───────────────────
 *
 * This module is the CONTROL PLANE (placement, capacity, accounting). It does
 * NOT by itself make co-tenancy safe.
 *
 * Sharing a relay is safe because the relay executes no tenant code. Sharing a
 * serverless host is NOT the same claim: tenant functions run. What makes it
 * *more* defensible than sharing a dev box is narrower and worth stating
 * precisely:
 *
 *   - a serverless host runs the user's DEPLOYED FUNCTIONS, not an interactive
 *     agent with `--dangerously-skip-permissions`;
 *   - it does NOT hold the user's mirrored Claude/Codex credentials, which is
 *     the thing that makes co-tenanting a workspace unacceptable;
 *   - the blast radius of an escape is the tenants' application data, not their
 *     source tree plus their model subscription.
 *
 * That is a real reduction in risk, NOT an elimination of it. Before this
 * carries third-party production traffic, the runtime must provide per-tenant
 * isolation stronger than a shared-kernel container — microVM (Firecracker) or
 * an equivalent. `desktop/agent/container_runner.go` + `Dockerfile.sandbox`
 * exist and are explicitly deferred with end-to-end testing outstanding.
 *
 * Placement being ready does not mean the runtime is ready. Do not read a
 * green pool as permission to co-tenant.
 */

/**
 * Tenants per shared serverless host.
 *
 * Lower than the relay's 20 on purpose: a relay is pass-through and bounded by
 * bandwidth, whereas serverless tenants consume CPU and RAM that we cannot
 * predict. Oversubscribing here degrades every tenant's backend at once —
 * their users see it, not just them. Raise only on measured headroom.
 */
export const SERVERLESS_TENANTS_PER_HOST =
  Number(process.env.YAVER_SERVERLESS_TENANTS_PER_HOST) || 10;

export function serverlessHostKey(region: string, index: number): string {
  const r = String(region || "eu").trim().toLowerCase();
  return `serverless-${r}-${Math.max(0, index)}`;
}

export type ServerlessPlacement = {
  hostKey: string;
  needsProvision: boolean;
  tenantsOnHost: number;
  reason: string;
};

/**
 * First-fit slot selection — same packing rule as the relay pool, and for the
 * same reason: dense packing lets an emptied host be drained and DELETED,
 * whereas least-loaded leaves every host permanently half-empty, which is the
 * always-on cost this pool exists to remove.
 */
export function selectServerlessHostSlot(args: {
  region: string;
  hostCounts: Record<string, number>;
  capacity?: number;
}): ServerlessPlacement {
  const capacity =
    args.capacity && args.capacity > 0 ? args.capacity : SERVERLESS_TENANTS_PER_HOST;
  for (let i = 0; i < 1000; i++) {
    const key = serverlessHostKey(args.region, i);
    const count = args.hostCounts[key] ?? 0;
    if (count < capacity) {
      return {
        hostKey: key,
        needsProvision: count === 0,
        tenantsOnHost: count + 1,
        reason:
          count === 0
            ? `new shared serverless host ${key}`
            : `joined ${key} (${count + 1}/${capacity})`,
      };
    }
  }
  throw new Error(`serverless pool exhausted for region ${args.region}`);
}

/**
 * Live tenant counts per shared serverless host.
 *
 * Hosted backends ride `cloudMachines` rows with tier="hosted" — the schema
 * already models them (hostedConvexUrl, grace/deprovision machinery), so this
 * reuses that rather than inventing a parallel table.
 */
export const hostCountsForRegion = internalQuery({
  args: { region: v.string() },
  handler: async (ctx, { region }) => {
    const rows = await ctx.db.query("cloudMachines").collect();
    const counts: Record<string, number> = {};
    for (const m of rows) {
      if (m.tier !== "hosted") continue;
      if (m.region !== region) continue;
      // A removed/errored backend is not serving anyone; counting it would
      // strand a slot forever.
      if (m.status === "removed" || m.status === "error" || m.status === "stopped") continue;
      const key = m.serverlessHostKey;
      if (!key) continue;
      counts[key] = (counts[key] ?? 0) + 1;
    }
    return counts;
  },
});

/**
 * Assign a hosted backend to a shared serverless host.
 *
 * Idempotent: a retried webhook must never migrate a LIVE backend to a
 * different host — that would move the user's production app mid-flight.
 */
export const assignToPool = internalMutation({
  args: { machineId: v.id("cloudMachines"), region: v.string() },
  handler: async (ctx, { machineId, region }): Promise<ServerlessPlacement> => {
    const machine = await ctx.db.get(machineId);
    if (!machine) throw new Error("machine not found");
    if (machine.serverlessHostKey) {
      return {
        hostKey: machine.serverlessHostKey,
        needsProvision: false,
        tenantsOnHost: 0,
        reason: `already on ${machine.serverlessHostKey}`,
      };
    }
    const rows = await ctx.db.query("cloudMachines").collect();
    const counts: Record<string, number> = {};
    for (const m of rows) {
      if (m.tier !== "hosted" || m.region !== region) continue;
      if (m.status === "removed" || m.status === "error" || m.status === "stopped") continue;
      if (!m.serverlessHostKey) continue;
      counts[m.serverlessHostKey] = (counts[m.serverlessHostKey] ?? 0) + 1;
    }
    const slot = selectServerlessHostSlot({ region, hostCounts: counts });
    await ctx.db.patch(machineId, {
      serverlessHostKey: slot.hostKey,
      updatedAt: Date.now(),
    });
    return slot;
  },
});

/** The box already serving a slot — when set, do NOT create another. */
export const hostEndpoint = internalQuery({
  args: { hostKey: v.string() },
  handler: async (ctx, { hostKey }): Promise<{ serverId: string; serverIp: string } | null> => {
    const rows = await ctx.db.query("cloudMachines").collect();
    for (const m of rows) {
      if (m.serverlessHostKey !== hostKey) continue;
      if (m.status === "removed" || m.status === "error") continue;
      const id = m.hetznerServerId ?? m.cloudResourceId;
      if (id && m.serverIp) return { serverId: String(id), serverIp: String(m.serverIp) };
    }
    return null;
  },
});

/**
 * Serverless usage is metered SEPARATELY from Cloud Workspace hours.
 *
 * Mixing them would be wrong twice over. Arithmetically: a backend is always-on
 * (730 h/month) against a 120 h workspace allowance, so it would exhaust the
 * month in ~5 days and take the workspace down with it. Diagnostically: the
 * workspace tier would then *look* like it was failing when the backend was
 * consuming the budget — a billing bug wearing a capacity bug's clothes.
 *
 * `machineType: "serverless"` keeps it in its own `includedAllowance` bucket,
 * which the schema already supports.
 */
export const SERVERLESS_MACHINE_TYPE = "serverless";

/** Per-tenant monthly cost of a shared host — input to the margin guard. */
export function serverlessCostPerTenantEur(monthlyHostEur: number, tenants: number): number {
  if (tenants <= 0) return monthlyHostEur;
  return monthlyHostEur / tenants;
}
