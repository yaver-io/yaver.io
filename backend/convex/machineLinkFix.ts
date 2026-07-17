import { v } from "convex/values";
import { internalMutation } from "./_generated/server";

/**
 * Repair the machine↔device link for a box whose row lost track of its device.
 *
 * A box boots from a snapshot, and its identity (deviceId) lives ON that disk.
 * So a box recreated from a snapshot taken of a DIFFERENT machine registers
 * under the OLD machine's deviceId, while its own row keeps `deviceId: ""`.
 * The result is a box wearing two identities: the live row owns the snapshot but
 * has no device, and a dead row owns the deviceId the box actually uses. Every
 * surface then joins the box to the dead row and reports it unwakeable — with
 * its snapshot sitting right there.
 *
 * This records the truth: point the live row at the deviceId the box really
 * registers under, and retire the row that was squatting it.
 *
 * The real cure is identity-free images (inject identity at boot, never bake it
 * into a snapshot). This is the cleanup for boxes that already have it baked in.
 */
export const linkMachineToDevice = internalMutation({
  args: {
    machineId: v.id("cloudMachines"),
    deviceId: v.string(),
    // The row squatting this deviceId, if any. Marked `removed` so it stops
    // shadowing the live row (listMyDevices skips removed rows when it builds
    // its deviceId → machine map).
    retireMachineId: v.optional(v.id("cloudMachines")),
  },
  handler: async (ctx, args) => {
    const row = await ctx.db.get(args.machineId);
    if (!row) throw new Error(`machine ${args.machineId} not found`);

    const before = {
      deviceId: (row as any).deviceId ?? "",
      status: (row as any).status ?? "",
      lastSnapshotId: (row as any).lastSnapshotId ?? "",
    };

    await ctx.db.patch(args.machineId, {
      deviceId: args.deviceId,
      updatedAt: Date.now(),
    });

    let retired: string | null = null;
    if (args.retireMachineId) {
      const zombie = await ctx.db.get(args.retireMachineId);
      if (zombie) {
        if ((zombie as any).userId !== (row as any).userId) {
          throw new Error("refusing to retire a row owned by a different user");
        }
        await ctx.db.patch(args.retireMachineId, {
          status: "removed",
          updatedAt: Date.now(),
        });
        retired = String(args.retireMachineId);
      }
    }

    return { before, linked: args.deviceId, retired };
  },
});
