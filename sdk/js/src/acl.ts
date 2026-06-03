/**
 * acl — generic, COMPOSABLE entitlements (@yaver/server).
 *
 * Yaver already enforces several ACL layers on the agent, each with allowlist
 * semantics where an empty/absent list means "this layer does not constrain
 * that dimension":
 *
 *   - guest grants     (scope + allowedRunners + allowedProjects + deviceIds)
 *   - SDK-token scopes (allowedProjects + allowedCIDRs + delegated guest scope)
 *   - host-share policy(allowedRunners + allowedProjects + tooling preset)
 *   - peer / PC-sharing ACL (registered peers; layer-4 secret tools never cross)
 *   - company AI policy (allowedRunners + providers + per-role tool policy)  ← new
 *   - the user's own preferences / device-sharing rules
 *
 * The company AI policy is NOT a replacement for those — it is one more layer.
 * This module composes them so they are *jointly inclusive, not exclusive*:
 *
 *   - A dimension is constrained only by the layers that explicitly set it.
 *   - The effective allowlist for a dimension is the INTERSECTION of every
 *     present (non-empty) allowlist for it. A layer that omits a dimension does
 *     not narrow it — it never forces.
 *   - Denylists (e.g. layer-4 secret tools) are UNIONED and always subtracted.
 *   - Numeric caps take the MIN of present values (tightest layer wins).
 *
 * This mirrors the agent's own semantics; the agent stays the authoritative
 * enforcer. Use this to (a) bake the effective allowed-runner scope into a
 * minted token/handle, and (b) render an honest UI of what a given caller may
 * actually do once every layer is applied.
 */

/** Secret/auth tools that never cross devices (mirrors agent layer-4 denylist). */
export const LAYER4_DENIED_TOOLS: readonly string[] = [
  'vault_set', 'vault_get', 'vault_list', 'vault_delete',
  'sdk_token_create', 'sdk_token_list', 'sdk_token_rotate', 'sdk_token_revoke',
  'env_import', 'env_inject', 'deploy_cred_set', 'deploy_cred_list',
];

/**
 * One ACL layer's constraints. EVERY field is optional. An absent or empty
 * allowlist means the layer does not constrain that dimension (inclusive). A
 * tool allowlist containing `'*'` also means "all tools" (no constraint).
 */
export interface Entitlement {
  /** Where this layer came from, for audit/debug. */
  source: string;
  allowedRunners?: string[];
  allowedProviders?: string[];
  allowedProjects?: string[];
  allowedWorkKinds?: string[];
  /** Tool allowlist; `'*'` = all. Intersected across layers. */
  allowedTools?: string[];
  /** Tool denylist; unioned across layers and always subtracted. */
  deniedTools?: string[];
  allowedDeviceIds?: string[];
  allowedCIDRs?: string[];
  /** Numeric caps — the tightest (min) present value wins. */
  dailyTokenLimit?: number;
  cpuLimitPercent?: number;
  ramLimitMb?: number;
  sessionTtlMinutes?: number;
}

/** The composed result. `undefined` on a list dimension means unconstrained. */
export interface EffectiveEntitlement {
  sources: string[];
  allowedRunners?: string[];
  allowedProviders?: string[];
  allowedProjects?: string[];
  allowedWorkKinds?: string[];
  /** undefined = all tools allowed (minus deniedTools). */
  allowedTools?: string[];
  deniedTools: string[];
  allowedDeviceIds?: string[];
  allowedCIDRs?: string[];
  dailyTokenLimit?: number;
  cpuLimitPercent?: number;
  ramLimitMb?: number;
  sessionTtlMinutes?: number;
}

/** Intersect the present (non-empty) allowlists; absent ones don't constrain. */
function intersectAllow(lists: Array<string[] | undefined>): string[] | undefined {
  const present = lists.filter((l): l is string[] => Array.isArray(l) && l.length > 0);
  if (present.length === 0) return undefined; // unconstrained
  return present.reduce((acc, cur) => acc.filter((x) => cur.includes(x)));
}

/** Tool allowlists treat `'*'` as "no constraint". */
function intersectTools(lists: Array<string[] | undefined>): string[] | undefined {
  const present = lists
    .filter((l): l is string[] => Array.isArray(l) && l.length > 0)
    .filter((l) => !l.includes('*'));
  if (present.length === 0) return undefined;
  return present.reduce((acc, cur) => acc.filter((x) => cur.includes(x)));
}

function minPresent(vals: Array<number | undefined>): number | undefined {
  const present = vals.filter((v): v is number => typeof v === 'number');
  return present.length ? Math.min(...present) : undefined;
}

/**
 * Compose ACL layers into one effective entitlement. Jointly inclusive: a
 * dimension is narrowed only by the layers that set it; the result is the
 * intersection of present allowlists, the union of denylists, and the min of
 * numeric caps. No layer is forced onto another.
 */
export function composeEntitlements(layers: Entitlement[]): EffectiveEntitlement {
  const present = layers.filter(Boolean);
  const denied = Array.from(new Set(present.flatMap((l) => l.deniedTools ?? [])));
  let allowedTools = intersectTools(present.map((l) => l.allowedTools));
  if (allowedTools && denied.length) allowedTools = allowedTools.filter((t) => !denied.includes(t));
  return {
    sources: present.map((l) => l.source),
    allowedRunners: intersectAllow(present.map((l) => l.allowedRunners)),
    allowedProviders: intersectAllow(present.map((l) => l.allowedProviders)),
    allowedProjects: intersectAllow(present.map((l) => l.allowedProjects)),
    allowedWorkKinds: intersectAllow(present.map((l) => l.allowedWorkKinds)),
    allowedTools,
    deniedTools: denied,
    allowedDeviceIds: intersectAllow(present.map((l) => l.allowedDeviceIds)),
    allowedCIDRs: intersectAllow(present.map((l) => l.allowedCIDRs)),
    dailyTokenLimit: minPresent(present.map((l) => l.dailyTokenLimit)),
    cpuLimitPercent: minPresent(present.map((l) => l.cpuLimitPercent)),
    ramLimitMb: minPresent(present.map((l) => l.ramLimitMb)),
    sessionTtlMinutes: minPresent(present.map((l) => l.sessionTtlMinutes)),
  };
}

/** True if `value` passes an effective allowlist (undefined list = allowed). */
export function entitlementAllows(list: string[] | undefined, value: string): boolean {
  return list === undefined || list.includes(value);
}

// ── Builders from the existing layers (so the SDK reads, not reinvents) ──

/** From a company-policy resolved session (see policy.ts ResolvedSession). */
export function entitlementFromResolved(resolved: {
  role?: string;
  runner?: { allowedRunners?: string[] };
  provider?: { allowedProviders?: string[] };
  workKind?: string;
  mcp?: { toolPolicyByRole?: Array<{ role: string; allowedTools: string[] }> };
}): Entitlement {
  const toolPolicy = resolved.mcp?.toolPolicyByRole?.find((r) => r.role === resolved.role);
  return {
    source: 'company-policy',
    allowedRunners: resolved.runner?.allowedRunners,
    allowedProviders: resolved.provider?.allowedProviders,
    allowedWorkKinds: resolved.workKind ? [resolved.workKind] : undefined,
    allowedTools: toolPolicy?.allowedTools,
  };
}

/** From a guest grant row (guests.ts). Empty lists stay unconstrained. */
export function entitlementFromGuest(grant: {
  scope?: string;
  allowedRunners?: string[];
  allowedProjects?: string[];
  deviceIds?: string[];
  dailyTokenLimit?: number;
  cpuLimitPercent?: number;
  ramLimitMb?: number;
}): Entitlement {
  return {
    source: `guest:${grant.scope ?? 'full'}`,
    allowedRunners: grant.allowedRunners,
    allowedProjects: grant.allowedProjects,
    allowedDeviceIds: grant.deviceIds,
    deniedTools: [...LAYER4_DENIED_TOOLS],
    dailyTokenLimit: grant.dailyTokenLimit,
    cpuLimitPercent: grant.cpuLimitPercent,
    ramLimitMb: grant.ramLimitMb,
  };
}

/** From an SDK-token scope (auth.ts sdkTokens row). */
export function entitlementFromSdkToken(scope: {
  allowedProjects?: string[];
  allowedCIDRs?: string[];
  delegatedGuestScope?: string;
}): Entitlement {
  return {
    source: scope.delegatedGuestScope ? `sdk-token:guest:${scope.delegatedGuestScope}` : 'sdk-token',
    allowedProjects: scope.allowedProjects,
    allowedCIDRs: scope.allowedCIDRs,
    deniedTools: [...LAYER4_DENIED_TOOLS],
  };
}

/** From a host-share policy (auth.go HostSharePolicy mirrored over the wire). */
export function entitlementFromHostShare(policy: {
  allowedRunners?: string[];
  allowedProjects?: string[];
  sessionTtlMinutes?: number;
}): Entitlement {
  return {
    source: 'host-share',
    allowedRunners: policy.allowedRunners,
    allowedProjects: policy.allowedProjects,
    sessionTtlMinutes: policy.sessionTtlMinutes,
    deniedTools: [...LAYER4_DENIED_TOOLS],
  };
}

/** A user's own preferences/limits — also just a layer, never forced. */
export function entitlementFromUser(prefs: {
  allowedRunners?: string[];
  allowedProviders?: string[];
  allowedProjects?: string[];
  allowedDeviceIds?: string[];
}): Entitlement {
  return {
    source: 'user',
    allowedRunners: prefs.allowedRunners,
    allowedProviders: prefs.allowedProviders,
    allowedProjects: prefs.allowedProjects,
    allowedDeviceIds: prefs.allowedDeviceIds,
  };
}
