import { mutation, query, internalMutation } from "./_generated/server";
import { v } from "convex/values";
import { internal } from "./_generated/api";
import { listActiveInfraGrantsForGuest, listGrantedMachineIdsForGrant } from "./access";

// Machine specs by type
const MACHINE_SPECS = {
  cpu: {
    hetznerType: "cx42",     // 8 vCPU, 16 GB RAM, 160 GB NVMe
    vcpu: 8,
    ramGb: 16,
    diskGb: 160,
    arch: "amd64" as const,
  },
  gpu: {
    hetznerType: "gex44",    // Dedicated NVIDIA RTX 4000, 20 GB VRAM
    vcpu: 16,
    ramGb: 64,
    diskGb: 320,
    arch: "amd64" as const,
    gpu: "rtx4000",
    vram: 20,
  },
};

// ─── Queries ────────────────────────────────────────────────────

/** Get all machines for a user (owned + team-shared). */
export const listForUser = query({
  args: { userId: v.id("users") },
  handler: async (ctx, { userId }) => {
    // Direct machines
    const owned = await ctx.db
      .query("cloudMachines")
      .withIndex("by_user", (q) => q.eq("userId", userId))
      .collect();

    // Team machines (find user's teams, then machines for those teams)
    const memberships = await ctx.db
      .query("teamMembers")
      .withIndex("by_user", (q) => q.eq("userId", userId))
      .collect();

    const teamMachines: typeof owned = [];
    for (const m of memberships) {
      const machines = await ctx.db
        .query("cloudMachines")
        .withIndex("by_team", (q) => q.eq("teamId", m.teamId))
        .collect();
      teamMachines.push(...machines);
    }

    const grantedMachines: typeof owned = [];
    const grants = await listActiveInfraGrantsForGuest(ctx, userId);
    for (const grant of grants) {
      if (grant.shareAllMachines) {
        const hostMachines = await ctx.db
          .query("cloudMachines")
          .withIndex("by_user", (q) => q.eq("userId", grant.hostUserId))
          .collect();
        grantedMachines.push(...hostMachines);
        continue;
      }
      const machineIds = await listGrantedMachineIdsForGrant(ctx, grant._id);
      for (const machineId of machineIds) {
        const machine = await ctx.db.get(machineId);
        if (!machine) continue;
        if (machine.userId !== grant.hostUserId) continue;
        grantedMachines.push(machine);
      }
    }

    // Deduplicate (user might own a team machine or receive the same machine twice)
    const seen = new Set<string>();
    const all = [...owned, ...teamMachines, ...grantedMachines].filter((m) => {
      const id = m._id.toString();
      if (seen.has(id)) return false;
      seen.add(id);
      return true;
    });

    return all;
  },
});

/** Get a specific machine by ID. */
export const get = query({
  args: { machineId: v.id("cloudMachines") },
  handler: async (ctx, { machineId }) => {
    return await ctx.db.get(machineId);
  },
});

// ─── Mutations ──────────────────────────────────────────────────

/** Create a new cloud machine and start provisioning. */
export const create = mutation({
  args: {
    userId: v.id("users"),
    machineType: v.string(),        // "cpu" | "gpu"
    teamId: v.optional(v.string()), // if team-owned
    region: v.optional(v.string()), // "eu" | "us", default "eu"
    repoUrl: v.optional(v.string()),
    sshPublicKey: v.optional(v.string()),
    subscriptionId: v.optional(v.id("subscriptions")),
  },
  handler: async (ctx, args) => {
    const specDef = MACHINE_SPECS[args.machineType as keyof typeof MACHINE_SPECS];
    if (!specDef) throw new Error("Invalid machine type: " + args.machineType);

    const now = Date.now();
    const tools = ["nodejs", "python", "go", "rust", "docker", "expo-cli", "eas-cli"];
    if (args.machineType === "gpu") {
      tools.push("ollama", "personaplex", "whisper", "cuda");
    }

    const specs: {
      vcpu: number;
      ramGb: number;
      diskGb: number;
      arch: string;
      gpu?: string;
      vram?: number;
    } = {
      vcpu: specDef.vcpu,
      ramGb: specDef.ramGb,
      diskGb: specDef.diskGb,
      arch: specDef.arch,
    };
    if ("gpu" in specDef) {
      specs.gpu = specDef.gpu;
      specs.vram = specDef.vram;
    }

    const machineId = await ctx.db.insert("cloudMachines", {
      userId: args.userId,
      teamId: args.teamId,
      subscriptionId: args.subscriptionId,
      machineType: args.machineType,
      status: "provisioning",
      multiUser: !!args.teamId,
      region: args.region ?? "eu",
      tools,
      repoUrl: args.repoUrl,
      sshPublicKey: args.sshPublicKey,
      specs,
      createdAt: now,
      updatedAt: now,
    });

    // Schedule provisioning (runs async)
    await ctx.scheduler.runAfter(0, internal.cloudMachines.provision, {
      machineId,
    });

    return machineId;
  },
});

/** Internal: Provision a cloud machine on Hetzner. */
export const provision = internalMutation({
  args: { machineId: v.id("cloudMachines") },
  handler: async (ctx, { machineId }) => {
    const machine = await ctx.db.get(machineId);
    if (!machine) return;

    // Get owner info for the provisioning script
    const owner = await ctx.db.get(machine.userId);
    if (!owner) return;

    const specDef = MACHINE_SPECS[machine.machineType as keyof typeof MACHINE_SPECS];
    if (!specDef) {
      await ctx.db.patch(machineId, {
        status: "error",
        errorMessage: "Unknown machine type: " + machine.machineType,
        updatedAt: Date.now(),
      });
      return;
    }

    try {
      // The actual Hetzner API call happens from a Convex action (not mutation).
      // For now, update status to show provisioning is in progress.
      // The provision-machine.sh script is called externally by the webhook handler.
      await ctx.db.patch(machineId, {
        status: "provisioning",
        updatedAt: Date.now(),
      });

      // Schedule health check in 5 minutes
      await ctx.scheduler.runAfter(5 * 60 * 1000, internal.cloudMachines.healthCheck, {
        machineId,
        attempt: 1,
      });
    } catch (e: unknown) {
      const msg = e instanceof Error ? e.message : String(e);
      await ctx.db.patch(machineId, {
        status: "error",
        errorMessage: msg,
        updatedAt: Date.now(),
      });
    }
  },
});

/** Internal: Health check for a provisioned machine. */
export const healthCheck = internalMutation({
  args: {
    machineId: v.id("cloudMachines"),
    attempt: v.number(),
  },
  handler: async (ctx, { machineId, attempt }) => {
    const machine = await ctx.db.get(machineId);
    if (!machine || machine.status !== "provisioning") return;

    // The actual HTTP health check to the machine's /health endpoint
    // is done via a Convex action. For now, we track the attempt.
    if (attempt >= 10) {
      await ctx.db.patch(machineId, {
        status: "error",
        errorMessage: "Health check timed out after 10 attempts",
        updatedAt: Date.now(),
      });
      return;
    }

    // Retry in 2 minutes
    await ctx.scheduler.runAfter(2 * 60 * 1000, internal.cloudMachines.healthCheck, {
      machineId,
      attempt: attempt + 1,
    });
  },
});

/** Update machine status (called by provisioning scripts via webhook). */
export const updateStatus = mutation({
  args: {
    machineId: v.id("cloudMachines"),
    status: v.string(),
    serverIp: v.optional(v.string()),
    hostname: v.optional(v.string()),
    hetznerServerId: v.optional(v.string()),
    errorMessage: v.optional(v.string()),
  },
  handler: async (ctx, args) => {
    const machine = await ctx.db.get(args.machineId);
    if (!machine) throw new Error("Machine not found");

    const updates: Record<string, unknown> = {
      status: args.status,
      updatedAt: Date.now(),
    };
    if (args.serverIp) updates.serverIp = args.serverIp;
    if (args.hostname) updates.hostname = args.hostname;
    if (args.hetznerServerId) updates.hetznerServerId = args.hetznerServerId;
    if (args.errorMessage) updates.errorMessage = args.errorMessage;
    if (args.status === "active") updates.lastHealthCheck = Date.now();

    await ctx.db.patch(args.machineId, updates);
  },
});

/** Stop and deprovision a machine. */
export const deprovision = mutation({
  args: { machineId: v.id("cloudMachines") },
  handler: async (ctx, { machineId }) => {
    const machine = await ctx.db.get(machineId);
    if (!machine) throw new Error("Machine not found");

    await ctx.db.patch(machineId, {
      status: "stopping",
      updatedAt: Date.now(),
    });

    // Actual Hetzner server deletion is handled externally
  },
});
