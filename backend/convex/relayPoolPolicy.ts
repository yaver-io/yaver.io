// relayPoolPolicy.ts — PURE shared relay pool policy. No Convex imports, so
// it is unit-testable under `node --experimental-strip-types` (see
// scripts/test-suite.sh policy-test list). relayPool.ts re-exports these for
// the internal actions/mutations that need them.

/**
 * Tenants per shared host.
 *
 * 20 already yields ~96% gross on Relay Pro, so there is no reason to chase
 * the last two points. Oversubscription converts a margin win into an outage,
 * and the relay's scarce resource is BANDWIDTH (Hetzner's ~20 TB/mo
 * allowance), not CPU — a small box has ample CPU for pass-through. Raise this
 * only with measured per-tenant throughput, never optimistically.
 */
export function relayTenantsPerHost(
  env: Record<string, string | undefined> = process.env,
): number {
  const parsed = Number(env.YAVER_RELAY_TENANTS_PER_HOST);
  // The value is a packing target, not a licence for an environment typo to
  // put an unbounded number of paid accounts on one failure domain.
  return Number.isSafeInteger(parsed) && parsed >= 1 && parsed <= 50 ? parsed : 20;
}

export const RELAY_TENANTS_PER_HOST = relayTenantsPerHost();

/** Host key for a (region, index) slot. Stable and human-readable in logs. */
export function relayHostKey(region: string, index: number): string {
  const r = String(region || "eu").trim().toLowerCase();
  // v2 hosts use one certified hostname per pool slot. The version prevents a
  // new tenant from joining a legacy host that was certified only for its
  // first tenant's per-user hostname.
  return `relay-v2-${r}-${Math.max(0, index)}`;
}

/** Collision-resistant v3 key for the indexed pool-host ledger. The seed is a
 * Convex relay id, so creating a host never needs a region-wide counter or a
 * bounded scan for the next numeric slot. */
export function relayHostKeyFromSeed(region: string, seed: string): string {
  const r = String(region || "eu").trim().toLowerCase().replace(/[^a-z0-9-]/g, "-");
  const suffix = String(seed || "host").toLowerCase().replace(/[^a-z0-9]/g, "").slice(-16) || "host";
  return `relay-v3-${r}-${suffix}`;
}

/** Public TLS name: pooled tenants share one certified host name; dedicated
 * tenants retain a per-user name. Authentication, not DNS, isolates tenants. */
export function relayPublicHostname(args: {
  dedicated: boolean;
  shortUserId: string;
  hostKey?: string | null;
}): { subdomain: string; domain: string } {
  const label = args.dedicated
    ? `${args.shortUserId}.relay`
    : `${args.hostKey || "relay-unassigned"}.relay`;
  return { subdomain: label, domain: `${label}.yaver.io` };
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
  // With N occupied keys, first-fit must find a free key within N + 1 probes.
  // Deriving the bound from observed state avoids a hidden 1,000-host ceiling
  // while still making malformed input terminate deterministically.
  const maximumProbe = Object.keys(args.hostCounts).length;
  for (let i = 0; i <= maximumProbe; i++) {
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
  throw new Error(`relay pool placement invariant failed for region ${args.region}`);
}

/**
 * ─── THE deprovision decision — never delete a box other tenants still use ──
 *
 * A shared host serves up to RELAY_TENANTS_PER_HOST tenants from ONE Hetzner
 * box. The pre-2026-08-09 deprovision deleted the box unconditionally, so the
 * FIRST tenant to cancel took the relay offline for everyone else on the host.
 *
 * Rule: mark the departing tenant's row stopped FIRST (so hostIsEmpty no longer
 * counts it), then delete the provider box ONLY when the host is empty. A
 * dedicated relay (no sharedHostKey) is tenant-private and always deletable.
 */
export function sharedHostDeletionDecision(args: {
  sharedHostKey?: string | null;
  /** A pre-existing hybrid/free anchor is infrastructure, not subscription inventory. */
  pinned?: boolean;
  /** Live tenant count AFTER this tenant's row has been marked stopped. */
  liveTenantsOnHost: number;
}): { deleteServer: boolean; reason: string } {
  if (!args.sharedHostKey) {
    return { deleteServer: true, reason: "dedicated relay — box is tenant-private" };
  }
  if (args.pinned) {
    return { deleteServer: false, reason: "pinned hybrid relay — never delete from subscription churn" };
  }
  if (args.liveTenantsOnHost <= 0) {
    return { deleteServer: true, reason: "last tenant on shared host — drain and delete the box" };
  }
  return {
    deleteServer: false,
    reason: `${args.liveTenantsOnHost} tenant(s) still on shared host — box must stay`,
  };
}

/**
 * Should deprovision take a grace snapshot before deleting the box?
 *
 * DEDICATED relays: YES — the box is tenant-private, so a resubscribe can be
 * restored from the snapshot.
 *
 * SHARED pool hosts: NO — the host has no durable tenant workspace data worth
 * restoring), and a drained host's snapshot is a billed orphan with no restore
 * path. Measured 2026-08-09: a 0.39 GB `yaver-predelete-relay-*` snapshot was
 * left billed on the owner's account by a shared-host teardown and had to be
 * deleted by hand.
 */
export function sharedHostGraceSnapshotDecision(sharedHostKey?: string | null): boolean {
  return !sharedHostKey;
}
