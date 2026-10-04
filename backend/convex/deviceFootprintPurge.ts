import { v } from "convex/values";
import { internalMutation } from "./_generated/server";

// Tables whose rows are wholly scoped to one device identity. This is the
// final privacy cleanup after the normal owner-authorized removeDevice flow.
// Managed/BYO machine bookkeeping is intentionally excluded: losing lifecycle
// state must never become an accidental provider-resource deletion or orphan.
const DEVICE_ROWS = [
  ["sessions", "deviceId"],
  ["credentialHandoffDevices", "deviceId"],
  ["pendingDeviceClaims", "deviceId"],
  ["provisionedDevices", "deviceId"],
  ["agentRescueCommands", "deviceId"],
  ["publishJobs", "deviceId"],
  ["deviceFlightEvents", "deviceId"],
  ["deviceMetrics", "deviceId"],
  ["deviceEvents", "deviceId"],
  ["runnerUsage", "deviceId"],
  ["taskRuns", "deviceId"],
  ["agentTaskSnapshots", "deviceId"],
  ["cloudWorkspaces", "runnerDeviceId"],
  ["workloadCredentials", "runnerDeviceId"],
  ["deviceCodes", "deviceId"],
  ["infraAccessGrantDevices", "deviceId"],
  ["hostShareInvites", "hostDeviceId"],
  ["hostShareSessions", "hostDeviceId"],
  ["hostShareSessions", "guestDeviceId"],
  ["projectShares", "hostDeviceId"],
  ["sdkTokens", "targetDeviceId"],
  ["dogfoodApps", "targetDeviceId"],
  ["dogfoodControlDevices", "deviceId"],
  ["whatsappInvites", "targetDeviceId"],
  ["whatsappContacts", "targetDeviceId"],
  ["whatsappCommandReceipts", "targetDeviceId"],
  ["userProjects", "deviceId"],
  ["projectProfiles", "sourceDeviceId"],
  ["taskPlacements", "targetDeviceId"],
  ["taskDispatchIntents", "targetDeviceId"],
  ["wakeRuns", "targetDeviceId"],
  ["userServices", "deviceId"],
  ["userDeployments", "deviceId"],
  ["userActivity", "deviceId"],
  ["companionProjects", "deviceId"],
  ["taskPackages", "deviceId"],
  ["packageAllocations", "runnerDeviceId"],
  ["gpuRentals", "deviceId"],
  ["meshNodes", "deviceId"],
  ["meshTags", "deviceId"],
  ["tmuxRunnerSessions", "deviceId"],
] as const;

/**
 * Permanently remove the residual control-plane footprint of a device that
 * has already gone through removeDevice. Internal-only and deliberately
 * refuses managed/BYO records so it can never deprovision or orphan a VPS.
 */
export const purgeRemovedDevice = internalMutation({
  args: { deviceId: v.string(), dryRun: v.optional(v.boolean()) },
  handler: async (ctx, { deviceId, dryRun }) => {
    const db: any = ctx.db;
    const device = await db.query("devices")
      .filter((q: any) => q.eq(q.field("deviceId"), deviceId))
      .unique();
    if (!device) return { ok: true, alreadyPurged: true, counts: {} };
    if (device.removed !== true) throw new Error("DEVICE_NOT_REMOVED");

    const [managed, byo] = await Promise.all([
      db.query("cloudMachines").filter((q: any) => q.eq(q.field("deviceId"), deviceId)).first(),
      db.query("byoMachines").filter((q: any) => q.eq(q.field("deviceId"), deviceId)).first(),
    ]);
    if (managed || byo) throw new Error("DEVICE_HAS_CLOUD_LIFECYCLE_ROW");

    const counts: Record<string, number> = {};
    const idsByTable = new Map<string, Set<string>>();
    for (const [table, field] of DEVICE_ROWS) {
      const rows = await db.query(table)
        .filter((q: any) => q.eq(q.field(field), deviceId))
        .take(500);
      if (rows.length) counts[`${table}.${field}`] = rows.length;
      const ids = idsByTable.get(table) ?? new Set<string>();
      for (const row of rows) ids.add(String(row._id));
      idsByTable.set(table, ids);
    }

    const settings = await db.query("userSettings")
      .filter((q: any) => q.eq(q.field("userId"), device.userId))
      .unique();
    const settingsPatch: Record<string, unknown> = {};
    if (settings) {
      if (settings.primaryDeviceId === deviceId) settingsPatch.primaryDeviceId = undefined;
      if (settings.secondaryDeviceId === deviceId) settingsPatch.secondaryDeviceId = undefined;
      if (Array.isArray(settings.workerDeviceIds) && settings.workerDeviceIds.includes(deviceId)) {
        settingsPatch.workerDeviceIds = settings.workerDeviceIds.filter((id: string) => id !== deviceId);
      }
      for (const field of [
        "primaryRunnerByDevice", "opencodeConfigByDevice", "defaultRuntimeProjectByDevice",
        "defaultRuntimeTargetByDevice", "runtimeProjectCatalogByDevice", "mcpCatalogByDevice",
        "mcpServersByDevice",
      ]) {
        if (Array.isArray(settings[field]) && settings[field].some((row: any) => row?.deviceId === deviceId)) {
          settingsPatch[field] = settings[field].filter((row: any) => row?.deviceId !== deviceId);
        }
      }
      if (Array.isArray(settings.machineRolesByProject) && settings.machineRolesByProject.some((row: any) =>
        row?.runnerDeviceId === deviceId || row?.secondaryRunnerDeviceId === deviceId ||
        row?.renderDeviceId === deviceId || row?.secondaryRenderDeviceId === deviceId
      )) {
        settingsPatch.machineRolesByProject = settings.machineRolesByProject.filter((row: any) =>
          row?.runnerDeviceId !== deviceId && row?.secondaryRunnerDeviceId !== deviceId &&
          row?.renderDeviceId !== deviceId && row?.secondaryRenderDeviceId !== deviceId
        );
      }
      if (Object.keys(settingsPatch).length) counts.userSettings = 1;
    }

    if (!dryRun) {
      for (const ids of idsByTable.values()) {
        for (const id of ids) await db.delete(id);
      }
      if (settings && Object.keys(settingsPatch).length) await db.patch(settings._id, settingsPatch);
      await db.delete(device._id);
      counts.devices = 1;
    } else {
      counts.devices = 1;
    }
    return { ok: true, alreadyPurged: false, dryRun: dryRun === true, counts };
  },
});
