import type { AbstractCloudProvider } from "./abstract";
import { createManagedCloudProviderRegistry } from "./registry";
import type { ProviderId, RequiredCapability } from "./types";

/**
 * Server-side compute-provider selection for Cloud Workspace.
 *
 * ─── The product rule this enforces ─────────────────────────────────────────
 * End users MUST NOT choose a provider. Cloud Workspace is Yaver-managed:
 * placement comes from capacity, cost, credits and health, and the user sees a
 * workspace label and a location. Provider names may appear as read-only
 * diagnostics, never as a control.
 *
 * Therefore this module takes NO caller-supplied provider. The only override
 * is a server-side environment variable that a client cannot influence, and it
 * exists for operator/adapter testing — not for customers. If a `provider`
 * value ever needs to come from a request, it must be gated by the owner
 * allowlist at the HTTP boundary and passed as `operatorOverride`, never
 * forwarded blindly.
 *
 * ─── Why the default is still Hetzner ───────────────────────────────────────
 * As of 2026-07-21 Hetzner is the only provider that can prove the things a
 * paid placement requires: delete-stops-spend, durable volume, snapshot wake,
 * real tagged cleanup, and a stable egress address. The other three adapters
 * are implemented but declare `productionEligible:false`, and this function
 * refuses to select them for real placement. That refusal is the safety
 * property — do not "temporarily" relax it to test an adapter; use the
 * explicit operator override, which is loud and auditable.
 */

export type ProviderSelectionInput = {
  /** Capabilities the workload genuinely requires. */
  required?: RequiredCapability[];
  /**
   * Operator/adapter-testing override. MUST already have been authorized
   * against the owner allowlist by the caller — this module cannot see the
   * session and deliberately does not try.
   */
  operatorOverride?: ProviderId;
  env?: Record<string, string | undefined>;
};

export type ProviderSelection = {
  provider: AbstractCloudProvider;
  providerId: ProviderId;
  /** True when a non-production-eligible adapter was force-selected. */
  operatorForced: boolean;
  reason: string;
};

export const DEFAULT_COMPUTE_PROVIDER: ProviderId = "hetzner";

/**
 * Capabilities every PAID workspace placement requires. A provider that cannot
 * declare all of these cannot hold a customer's workspace:
 *  - delete-stops-compute-spend: park is delete-not-stop; if delete does not
 *    stop the meter, scale-to-zero is a lie.
 *  - durable-volume: state must survive the park.
 *  - tagged-cleanup: a leak we cannot detect is worse than one we can.
 */
export const PAID_PLACEMENT_CAPABILITIES: RequiredCapability[] = [
  "delete-stops-compute-spend",
  "durable-volume",
  "tagged-cleanup",
];

export function selectComputeProvider(input: ProviderSelectionInput = {}): ProviderSelection {
  const env = input.env ?? process.env;
  const registry = createManagedCloudProviderRegistry(env);
  const byId = new Map<ProviderId, AbstractCloudProvider>();
  for (const p of registry.computeProviders) byId.set(p.id, p as AbstractCloudProvider);

  const required = Array.from(
    new Set<RequiredCapability>([...PAID_PLACEMENT_CAPABILITIES, ...(input.required ?? [])]),
  );

  // 1. Operator override — explicit, server-side, loud.
  const forced = input.operatorOverride
    ?? (env.YAVER_FORCE_COMPUTE_PROVIDER as ProviderId | undefined);
  if (forced) {
    const provider = byId.get(forced);
    if (!provider) {
      throw new Error(
        `Compute provider "${forced}" was force-selected but is not configured on this deployment (missing credentials?)`,
      );
    }
    const caps = provider.describeCapabilities();
    return {
      provider,
      providerId: forced,
      operatorForced: !caps.productionEligible,
      reason: caps.productionEligible
        ? `operator override → ${forced} (production-eligible)`
        : `operator override → ${forced} (NOT production-eligible; adapter testing only)`,
    };
  }

  // 2. Normal placement: production-eligible providers that satisfy the
  //    capability floor. Default first so behaviour is stable and boring.
  const ordered: ProviderId[] = [
    DEFAULT_COMPUTE_PROVIDER,
    ...Array.from(byId.keys()).filter((id) => id !== DEFAULT_COMPUTE_PROVIDER),
  ];
  const rejected: string[] = [];
  for (const id of ordered) {
    const provider = byId.get(id);
    if (!provider) continue;
    const caps = provider.describeCapabilities();
    if (!caps.productionEligible) {
      rejected.push(`${id}: not production-eligible`);
      continue;
    }
    const missing = required.filter((c) => !caps.capabilities.includes(c));
    if (missing.length) {
      rejected.push(`${id}: missing ${missing.join(", ")}`);
      continue;
    }
    return {
      provider,
      providerId: id,
      operatorForced: false,
      reason: `selected ${id} (production-eligible, satisfies ${required.join(", ")})`,
    };
  }

  throw new Error(
    `No compute provider is eligible for paid placement. Rejected: ${rejected.join("; ") || "none configured"}`,
  );
}
