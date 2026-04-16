import { Doc, Id } from "./_generated/dataModel";
import { MutationCtx, QueryCtx } from "./_generated/server";

type AccessCtx = QueryCtx | MutationCtx;

export type ActiveInfraGrant = Doc<"infraAccessGrants">;

export async function getActiveInfraGrant(
  ctx: AccessCtx,
  hostUserId: Id<"users">,
  guestUserId: Id<"users">,
): Promise<ActiveInfraGrant | null> {
  return await ctx.db
    .query("infraAccessGrants")
    .withIndex("by_host_guest", (q) =>
      q.eq("hostUserId", hostUserId).eq("guestUserId", guestUserId),
    )
    .filter((q) => q.eq(q.field("status"), "active"))
    .first();
}

export async function getLegacyGuestAccess(
  ctx: AccessCtx,
  hostUserId: Id<"users">,
  guestUserId: Id<"users">,
) {
  return await ctx.db
    .query("guestAccess")
    .withIndex("by_host_guest", (q) =>
      q.eq("hostUserId", hostUserId).eq("guestUserId", guestUserId),
    )
    .filter((q) => q.eq(q.field("revokedAt"), undefined))
    .first();
}

export async function listActiveInfraGrantsForGuest(
  ctx: AccessCtx,
  guestUserId: Id<"users">,
): Promise<ActiveInfraGrant[]> {
  return await ctx.db
    .query("infraAccessGrants")
    .withIndex("by_guestUserId", (q) => q.eq("guestUserId", guestUserId))
    .filter((q) => q.eq(q.field("status"), "active"))
    .collect();
}

export async function listGrantedDeviceIdsForGrant(
  ctx: AccessCtx,
  grantId: Id<"infraAccessGrants">,
): Promise<string[]> {
  const rows = await ctx.db
    .query("infraAccessGrantDevices")
    .withIndex("by_grant", (q) => q.eq("grantId", grantId))
    .collect();
  return rows.map((row) => row.deviceId);
}

export async function listGrantedMachineIdsForGrant(
  ctx: AccessCtx,
  grantId: Id<"infraAccessGrants">,
): Promise<Id<"cloudMachines">[]> {
  const rows = await ctx.db
    .query("infraAccessGrantMachines")
    .withIndex("by_grant", (q) => q.eq("grantId", grantId))
    .collect();
  return rows.map((row) => row.machineId);
}

export async function guestCanReachHostDevice(
  ctx: AccessCtx,
  hostUserId: Id<"users">,
  guestUserId: Id<"users">,
): Promise<boolean> {
  const grant = await getActiveInfraGrant(ctx, hostUserId, guestUserId);
  if (grant) {
    if (grant.shareAllDevices) return true;
    const rows = await ctx.db
      .query("infraAccessGrantDevices")
      .withIndex("by_grant", (q) => q.eq("grantId", grant._id))
      .first();
    return rows !== null;
  }
  const legacy = await getLegacyGuestAccess(ctx, hostUserId, guestUserId);
  return legacy !== null;
}

export async function guestCanReachSpecificHostDevice(
  ctx: AccessCtx,
  hostUserId: Id<"users">,
  guestUserId: Id<"users">,
  deviceId: string,
): Promise<boolean> {
  const normalizedDeviceID = deviceId.trim();
  if (!normalizedDeviceID) {
    return guestCanReachHostDevice(ctx, hostUserId, guestUserId);
  }

  const grant = await getActiveInfraGrant(ctx, hostUserId, guestUserId);
  if (grant) {
    if (grant.shareAllDevices) return true;
    const match = await ctx.db
      .query("infraAccessGrantDevices")
      .withIndex("by_device_guest", (q) =>
        q.eq("deviceId", normalizedDeviceID).eq("guestUserId", guestUserId),
      )
      .first();
    return match !== null && match.hostUserId === hostUserId;
  }

  const legacy = await getLegacyGuestAccess(ctx, hostUserId, guestUserId);
  return legacy !== null;
}

export async function revokeInfraGrantsBetweenUsers(
  ctx: MutationCtx,
  hostUserId: Id<"users">,
  guestUserId: Id<"users">,
  revokedAt = Date.now(),
): Promise<void> {
  const grants = await ctx.db
    .query("infraAccessGrants")
    .withIndex("by_host_guest", (q) =>
      q.eq("hostUserId", hostUserId).eq("guestUserId", guestUserId),
    )
    .collect();

  for (const grant of grants) {
    const deviceLinks = await ctx.db
      .query("infraAccessGrantDevices")
      .withIndex("by_grant", (q) => q.eq("grantId", grant._id))
      .collect();
    for (const link of deviceLinks) {
      await ctx.db.delete(link._id);
    }

    const machineLinks = await ctx.db
      .query("infraAccessGrantMachines")
      .withIndex("by_grant", (q) => q.eq("grantId", grant._id))
      .collect();
    for (const link of machineLinks) {
      await ctx.db.delete(link._id);
    }

    await ctx.db.patch(grant._id, {
      status: "revoked",
      revokedAt,
      updatedAt: revokedAt,
    });
  }
}

export async function deleteInfraGrantArtifactsForUser(
  ctx: MutationCtx,
  userId: Id<"users">,
): Promise<void> {
  const hostGrants = await ctx.db
    .query("infraAccessGrants")
    .withIndex("by_hostUserId", (q) => q.eq("hostUserId", userId))
    .collect();
  const guestGrants = await ctx.db
    .query("infraAccessGrants")
    .withIndex("by_guestUserId", (q) => q.eq("guestUserId", userId))
    .collect();

  const seenGrantIds = new Set<string>();
  for (const grant of [...hostGrants, ...guestGrants]) {
    const key = grant._id.toString();
    if (seenGrantIds.has(key)) continue;
    seenGrantIds.add(key);

    const deviceLinks = await ctx.db
      .query("infraAccessGrantDevices")
      .withIndex("by_grant", (q) => q.eq("grantId", grant._id))
      .collect();
    for (const link of deviceLinks) {
      await ctx.db.delete(link._id);
    }

    const machineLinks = await ctx.db
      .query("infraAccessGrantMachines")
      .withIndex("by_grant", (q) => q.eq("grantId", grant._id))
      .collect();
    for (const link of machineLinks) {
      await ctx.db.delete(link._id);
    }

    await ctx.db.delete(grant._id);
  }
}
