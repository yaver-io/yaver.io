const DIGEST_PINNED_IMAGE = /^[a-z0-9.-]+(?::[0-9]+)?\/[a-z0-9._/-]+@sha256:[a-f0-9]{64}$/;

/** Managed relay hosts must run an immutable image. A mutable tag plus an
 * unattended updater can change every tenant host without a release gate or a
 * rollback target. */
export function relayImageRef(env: Record<string, string | undefined> = process.env): string | null {
  const value = String(env.YAVER_RELAY_IMAGE || "").trim().toLowerCase();
  return DIGEST_PINNED_IMAGE.test(value) ? value : null;
}

export const RELAY_IMAGE_REQUIRED =
  "YAVER_RELAY_IMAGE must be a digest-pinned image such as ghcr.io/org/yaver-relay@sha256:<64 hex chars>";

export function relayDynamicProvisioningEnabled(
  env: Record<string, string | undefined> = process.env,
): boolean {
  return String(env.YAVER_RELAY_DYNAMIC_PROVISIONING_ENABLED || "").trim().toLowerCase() === "true";
}

/** Absolute control-plane ceiling. Revenue may justify increasing this later,
 * but a webhook burst or placement bug must never create an unbounded fleet. */
export function relayMaxDynamicHosts(
  env: Record<string, string | undefined> = process.env,
): number {
  const parsed = Number(env.YAVER_RELAY_MAX_DYNAMIC_HOSTS);
  return Number.isSafeInteger(parsed) && parsed >= 0 ? parsed : 2;
}

/** Hard release-time spend ceiling. Environment configuration may make the
 * guard stricter, but cannot silently make a $9 plan buy a larger machine.
 * Raising this value therefore requires a reviewed code release. */
export const RELAY_HARD_MAX_HOURLY_EUR = 0.04;

export function relayMaxHourlyEur(
  env: Record<string, string | undefined> = process.env,
): number {
  const configured = Number(env.YAVER_RELAY_MAX_HOURLY_EUR);
  if (!Number.isFinite(configured) || configured <= 0) return RELAY_HARD_MAX_HOURLY_EUR;
  return Math.min(configured, RELAY_HARD_MAX_HOURLY_EUR);
}

export function relayFleetCreationDecision(args: {
  dynamicProvisioningEnabled: boolean;
  currentDynamicHosts: number;
  maxDynamicHosts: number;
}): { allow: boolean; reason: string } {
  if (!args.dynamicProvisioningEnabled) {
    return {
      allow: false,
      reason: "Dynamic relay provisioning is disabled; existing hybrid capacity is full",
    };
  }
  if (args.currentDynamicHosts >= args.maxDynamicHosts) {
    return {
      allow: false,
      reason: `Relay fleet ceiling reached (${args.currentDynamicHosts}/${args.maxDynamicHosts} dynamic hosts)`,
    };
  }
  return {
    allow: true,
    reason: `Dynamic host permitted (${args.currentDynamicHosts + 1}/${args.maxDynamicHosts})`,
  };
}

export type PinnedHybridRelay = {
  hostKey: string;
  region: string;
  hostname: string;
  serverId: string;
  serverIp: string;
  serverIpv6?: string;
  capacity: number;
};

/** Parse the existing hybrid node without ever baking provider inventory into
 * the public repository. A partial configuration is an operator error, not a
 * reason to fall through and buy a replacement VPS. */
export function pinnedHybridRelay(
  env: Record<string, string | undefined> = process.env,
): PinnedHybridRelay | null {
  const fields = {
    hostKey: String(env.YAVER_RELAY_HYBRID_HOST_KEY || "").trim(),
    region: String(env.YAVER_RELAY_HYBRID_REGION || "").trim().toLowerCase(),
    hostname: String(env.YAVER_RELAY_HYBRID_DOMAIN || "").trim().toLowerCase(),
    serverId: String(env.YAVER_RELAY_HYBRID_SERVER_ID || "").trim(),
    serverIp: String(env.YAVER_RELAY_HYBRID_SERVER_IP || "").trim(),
    serverIpv6: String(env.YAVER_RELAY_HYBRID_SERVER_IPV6 || "").trim(),
  };
  const present = Object.values(fields).filter(Boolean).length;
  if (present === 0) return null;
  const required = [fields.hostKey, fields.region, fields.hostname, fields.serverId, fields.serverIp];
  if (required.some((value) => !value)) {
    throw new Error("Pinned hybrid relay configuration is partial; refusing provider spend");
  }
  if (!/^[a-z0-9][a-z0-9-]{2,62}$/.test(fields.hostKey)) {
    throw new Error("YAVER_RELAY_HYBRID_HOST_KEY is invalid");
  }
  if (!/^[a-z0-9.-]+$/.test(fields.hostname) || !fields.hostname.includes(".")) {
    throw new Error("YAVER_RELAY_HYBRID_DOMAIN must be a DNS hostname without a scheme");
  }
  const rawCapacity = Number(env.YAVER_RELAY_HYBRID_PAID_CAPACITY);
  const capacity = Number.isSafeInteger(rawCapacity) && rawCapacity > 0 ? rawCapacity : 10;
  return {
    hostKey: fields.hostKey,
    region: fields.region,
    hostname: fields.hostname,
    serverId: fields.serverId,
    serverIp: fields.serverIp,
    ...(fields.serverIpv6 ? { serverIpv6: fields.serverIpv6 } : {}),
    capacity,
  };
}
