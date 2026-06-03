import { mutation, query } from "./_generated/server";
import { v } from "convex/values";
import { validateSessionInternal } from "./auth";

const tenantComputeProvider = v.union(
  v.literal("hetzner"),
  v.literal("aws"),
  v.literal("gcp"),
  v.literal("azure"),
  v.literal("onprem"),
  v.literal("byo-yaver-device"),
);

const runtimeMode = v.union(
  v.literal("dedicated-compute"),
  v.literal("bring-your-own-yaver"),
  v.literal("local-only"),
);

const credentialMode = v.union(
  v.literal("user-auth-on-runtime"),
  v.literal("company-api-key-on-runtime"),
  v.literal("local-model-on-runtime"),
  v.literal("external-onprem-endpoint"),
);

const providerDef = v.object({
  id: v.string(),
  label: v.string(),
  baseUrl: v.optional(v.string()),
  models: v.array(v.string()),
  keyPolicy: v.union(
    v.literal("company-secret"),
    v.literal("user-secret"),
    v.literal("none"),
  ),
  keyConfigured: v.optional(v.boolean()),
});

// Generic app-contributed profile. Lets any app register its own work kinds,
// per-role caps, and provider catalog without baking app vocabulary into the
// Yaver core. Config only — never secrets.
const appProfileValidator = v.object({
  app: v.string(),
  workKinds: v.array(v.object({
    key: v.string(),
    label: v.optional(v.string()),
    enabled: v.optional(v.boolean()),
    requiredTools: v.optional(v.array(v.string())),
    requiredMcp: v.optional(v.array(v.string())),
    approvals: v.optional(v.array(v.string())),
    promptHints: v.optional(v.array(v.string())),
    artifactKinds: v.optional(v.array(v.string())),
    allowedRoles: v.optional(v.array(v.string())),
  })),
  roles: v.optional(v.array(v.object({
    role: v.string(),
    allowedTools: v.optional(v.array(v.string())),
    allowedRunners: v.optional(v.array(v.string())),
    allowedProviders: v.optional(v.array(v.string())),
    allowedWorkKinds: v.optional(v.array(v.string())),
  }))),
  providers: v.optional(v.array(providerDef)),
});

const optionsValidator = v.object({
  enabled: v.boolean(),
  runtime: v.object({
    mode: runtimeMode,
    defaultProvider: tenantComputeProvider,
    defaultDeviceId: v.optional(v.string()),
    fallbackDeviceIds: v.optional(v.array(v.string())),
    region: v.optional(v.string()),
  }),
  convex: v.object({
    deploymentKind: v.union(
      v.literal("dedicated"),
      v.literal("shared-isolated"),
      v.literal("external"),
    ),
    deploymentName: v.optional(v.string()),
    siteUrl: v.optional(v.string()),
    envName: v.string(),
  }),
  runners: v.object({
    defaultRunner: v.string(),
    allowedRunners: v.array(v.string()),
    defaultModelByRunner: v.optional(v.array(v.object({
      runner: v.string(),
      model: v.string(),
    }))),
    allowUserOverride: v.boolean(),
    requireRunnerAuthPerUser: v.boolean(),
    credentialMode,
  }),
  opencode: v.optional(v.object({
    providers: v.array(providerDef),
    defaultAgent: v.optional(v.string()),
  })),
  mcp: v.object({
    enabledServers: v.array(v.string()),
    requiredServers: v.array(v.string()),
    toolPolicyByRole: v.optional(v.array(v.object({
      role: v.string(),
      allowedTools: v.array(v.string()),
    }))),
  }),
  workKinds: v.object({
    appCode: v.boolean(),
    erpFlow: v.boolean(),
    convex: v.boolean(),
    webUi: v.boolean(),
    harnessCad: v.boolean(),
    openScadCad: v.boolean(),
    robotTrial: v.boolean(),
    inspection: v.boolean(),
  }),
  approvals: v.object({
    requireApprovalForProductionWrites: v.boolean(),
    requireApprovalForDeploy: v.boolean(),
    requireApprovalForRobotMotion: v.boolean(),
    requireApprovalForSecretsAccess: v.boolean(),
  }),
  dataPolicy: v.object({
    allowCustomerDataInPrompts: v.boolean(),
    allowScreenshotsInPrompts: v.boolean(),
    allowTelemetryInPrompts: v.boolean(),
    redactPII: v.boolean(),
    retentionDays: v.number(),
  }),
  appProfile: v.optional(appProfileValidator),
});

function defaultOptions() {
  return {
    enabled: false,
    runtime: {
      mode: "dedicated-compute" as const,
      defaultProvider: "hetzner" as const,
      fallbackDeviceIds: [],
    },
    convex: {
      deploymentKind: "dedicated" as const,
      envName: "production",
    },
    runners: {
      defaultRunner: "claude",
      allowedRunners: ["claude", "codex", "opencode"],
      allowUserOverride: true,
      requireRunnerAuthPerUser: false,
      // OAuth-first: Yaver wraps Claude Code / Codex / OpenCode using the
      // user's OWN subscription OAuth (Claude Max/Pro, ChatGPT Plus) on the
      // runtime — never an API key (that double-bills and breaks the
      // "all agents on one plan" promise). The runtime signs in via the
      // existing `--claudeai` runner-auth browser/device/mirror flow. The
      // company-api-key / local-model modes stay available for genuine
      // on-prem local inference, but OAuth is the default and the focus.
      credentialMode: "user-auth-on-runtime" as const,
    },
    opencode: {
      providers: [],
      defaultAgent: "build",
    },
    mcp: {
      enabledServers: ["talos", "yaver"],
      requiredServers: ["talos", "yaver"],
      toolPolicyByRole: [
        { role: "admin", allowedTools: ["*"] },
        { role: "engineer", allowedTools: ["talos_*", "code_*", "web_preview_*", "vibe_preview_*"] },
        { role: "operator", allowedTools: ["talos_robot_status", "talos_robot_trial_plan", "talos_harness_*"] },
        { role: "viewer", allowedTools: [] },
      ],
    },
    workKinds: {
      appCode: true,
      erpFlow: true,
      convex: true,
      webUi: true,
      harnessCad: true,
      openScadCad: true,
      robotTrial: false,
      inspection: true,
    },
    approvals: {
      requireApprovalForProductionWrites: true,
      requireApprovalForDeploy: true,
      requireApprovalForRobotMotion: true,
      requireApprovalForSecretsAccess: true,
    },
    dataPolicy: {
      allowCustomerDataInPrompts: false,
      allowScreenshotsInPrompts: true,
      allowTelemetryInPrompts: false,
      redactPII: true,
      retentionDays: 30,
    },
  };
}

function optionsFromRow(row: any) {
  const defaults = defaultOptions();
  if (!row) return defaults;
  return {
    ...defaults,
    enabled: row.enabled ?? defaults.enabled,
    runtime: { ...defaults.runtime, ...(row.runtime || {}) },
    convex: { ...defaults.convex, ...(row.convex || {}) },
    runners: { ...defaults.runners, ...(row.runners || {}) },
    opencode: row.opencode ?? defaults.opencode,
    mcp: { ...defaults.mcp, ...(row.mcp || {}) },
    workKinds: { ...defaults.workKinds, ...(row.workKinds || {}) },
    approvals: { ...defaults.approvals, ...(row.approvals || {}) },
    dataPolicy: { ...defaults.dataPolicy, ...(row.dataPolicy || {}) },
    appProfile: row.appProfile ?? undefined,
  };
}

async function userForToken(ctx: any, tokenHash: string) {
  const session = await validateSessionInternal(ctx, tokenHash);
  return session?.user ?? null;
}

async function membershipForUser(ctx: any, teamId: string, userId: string) {
  return await ctx.db
    .query("teamMembers")
    .withIndex("by_team_user", (q: any) => q.eq("teamId", teamId).eq("userId", userId))
    .first();
}

function canAdmin(role: string | undefined) {
  return role === "admin" || role === "owner";
}

// Legacy fixed work-kind map. New apps register generic kinds in
// options.appProfile.workKinds; this stays as the fallback for the Talos
// vocabulary the dashboard still ships.
const workKindToOptionKey = {
  "app-code": "appCode",
  "erp-flow": "erpFlow",
  convex: "convex",
  "web-ui": "webUi",
  "harness-cad": "harnessCad",
  "openscad-cad": "openScadCad",
  "robot-trial": "robotTrial",
  inspection: "inspection",
} as const;

function roleCap(options: any, role: string | undefined, field: "allowedRunners" | "allowedProviders"): string[] | undefined {
  const entry = options.appProfile?.roles?.find((r: any) => r.role === role);
  const list = entry?.[field];
  return Array.isArray(list) && list.length > 0 ? list : undefined;
}

function normalizeRunner(options: any, requestedRunner?: string, role?: string) {
  let allowed: string[] = options.runners.allowedRunners.length
    ? options.runners.allowedRunners
    : [options.runners.defaultRunner || "opencode"];
  const cap = roleCap(options, role, "allowedRunners");
  if (cap) allowed = allowed.filter((r: string) => cap.includes(r));
  if (allowed.length === 0) allowed = [options.runners.defaultRunner || "opencode"];
  if (requestedRunner && options.runners.allowUserOverride && allowed.includes(requestedRunner)) {
    return { runner: requestedRunner, allowed };
  }
  if (allowed.includes(options.runners.defaultRunner)) return { runner: options.runners.defaultRunner, allowed };
  return { runner: allowed[0] || "opencode", allowed };
}

// Resolve the model backend (BYOK/on-prem provider) a runner should target.
// Catalog = opencode.providers ∪ appProfile.providers, narrowed by role caps.
function normalizeProvider(options: any, requestedProvider?: string, role?: string) {
  const catalog: any[] = [
    ...(options.opencode?.providers ?? []),
    ...(options.appProfile?.providers ?? []),
  ];
  const allowedIdsAll = catalog.map((p) => p.id);
  let allowedIds = allowedIdsAll;
  const cap = roleCap(options, role, "allowedProviders");
  if (cap) allowedIds = allowedIds.filter((id: string) => cap.includes(id));
  const pickable = catalog.filter((p) => allowedIds.includes(p.id));
  let selected: any = null;
  if (requestedProvider && options.runners.allowUserOverride) {
    selected = pickable.find((p) => p.id === requestedProvider) ?? null;
  }
  if (!selected) selected = pickable[0] ?? null;
  return { selected, allowedProviders: pickable.map((p) => p.id) };
}

function workKindDef(options: any, workKind: string): any | undefined {
  return options.appProfile?.workKinds?.find((w: any) => w.key === workKind);
}

function isWorkKindEnabled(options: any, workKind: string): boolean {
  const def = workKindDef(options, workKind);
  if (def) return def.enabled !== false;
  const key = (workKindToOptionKey as Record<string, string>)[workKind];
  return key ? Boolean(options.workKinds[key]) : false;
}

function modelForRunner(options: any, runner: string, requestedModel?: string) {
  if (requestedModel && options.runners.allowUserOverride) return requestedModel;
  return options.runners.defaultModelByRunner?.find((entry: { runner: string; model: string }) => entry.runner === runner)?.model;
}

function approvalsForWorkKind(options: any, workKind: string) {
  const required = ["secrets-access"];
  // App profile work kinds can declare their own approval gates.
  const def = workKindDef(options, workKind);
  if (def?.approvals) for (const a of def.approvals) if (!required.includes(a)) required.push(a);
  if (options.approvals.requireApprovalForProductionWrites && (workKind === "convex" || workKind === "erp-flow")) {
    required.push("production-write");
  }
  if (options.approvals.requireApprovalForDeploy && (workKind === "app-code" || workKind === "web-ui" || workKind === "convex")) {
    required.push("deploy");
  }
  if (options.approvals.requireApprovalForRobotMotion && workKind === "robot-trial") {
    required.push("robot-motion");
  }
  return Array.from(new Set(required));
}

function recommendedPromptPolicy(options: any, workKind: string) {
  // App-declared hints win over the built-in heuristic.
  const def = workKindDef(options, workKind);
  if (def && (def.promptHints?.length || def.artifactKinds?.length)) {
    return {
      systemHints: def.promptHints ?? [
        "Keep edits scoped to the requested project.",
        "Stream progress and surface approval requests before risky actions.",
      ],
      artifactKinds: def.artifactKinds ?? ["patch", "log", "preview", "diagnostics"],
    };
  }
  if (workKind === "openscad-cad" || workKind === "harness-cad") {
    return {
      systemHints: [
        "Prefer small, reviewable CAD iterations.",
        "Return renderable OpenSCAD or CAD source plus a short change summary.",
        "When rendering fails, feed compiler errors into the next iteration.",
      ],
      artifactKinds: ["source", "render-image", "mesh", "diagnostics"],
    };
  }
  if (workKind === "robot-trial") {
    return {
      systemHints: [
        "Plan before motion.",
        "Require operator approval before any real robot movement.",
        "Separate simulation, dry run, and live trial steps.",
      ],
      artifactKinds: ["plan", "harness-log", "inspection-image", "approval-record"],
    };
  }
  return {
    systemHints: [
      "Keep edits scoped to the requested project.",
      "Stream progress and surface approval requests before risky actions.",
    ],
    artifactKinds: ["patch", "log", "preview", "diagnostics"],
  };
}

export const getByToken = query({
  args: {
    tokenHash: v.string(),
    teamId: v.string(),
  },
  handler: async (ctx, { tokenHash, teamId }) => {
    const user = await userForToken(ctx, tokenHash);
    if (!user) return null;

    const membership = await membershipForUser(ctx, teamId, user._id);
    if (!membership) return null;

    const row = await ctx.db
      .query("companyAIOptions")
      .withIndex("by_teamId", (q) => q.eq("teamId", teamId))
      .first();
    const options = optionsFromRow(row);

    return {
      teamId,
      role: membership.role,
      options: row ? {
        ...options,
        createdAt: row.createdAt,
        updatedAt: row.updatedAt,
      } : options,
      canEdit: canAdmin(membership.role),
    };
  },
});

export const resolveForToken = query({
  args: {
    tokenHash: v.string(),
    teamId: v.string(),
    // Generic: any app-defined work-kind key. Validated against the team's
    // app profile (or the legacy Talos map) inside the handler.
    workKind: v.string(),
    requestedRunner: v.optional(v.string()),
    requestedModel: v.optional(v.string()),
    requestedProvider: v.optional(v.string()),
    requestedDeviceId: v.optional(v.string()),
    source: v.optional(v.union(
      v.literal("talos-web"),
      v.literal("talos-mobile"),
      v.literal("talos-desktop"),
      v.literal("yaver-web"),
      v.literal("yaver-mobile"),
      v.literal("yaver-desktop"),
      v.literal("mcp"),
      v.literal("api"),
    )),
  },
  handler: async (ctx, args) => {
    const user = await userForToken(ctx, args.tokenHash);
    if (!user) return null;

    const membership = await membershipForUser(ctx, args.teamId, user._id);
    if (!membership) return null;

    const row = await ctx.db
      .query("companyAIOptions")
      .withIndex("by_teamId", (q) => q.eq("teamId", args.teamId))
      .first();
    const options = optionsFromRow(row);

    const role = membership.role;
    const workKindEnabled = isWorkKindEnabled(options as any, args.workKind);
    const { runner, allowed: allowedRunners } = normalizeRunner(options as any, args.requestedRunner, role);
    const model = modelForRunner(options as any, runner, args.requestedModel);
    const { selected: provider, allowedProviders } = normalizeProvider(options as any, args.requestedProvider, role);
    const selectedDeviceId = args.requestedDeviceId || options.runtime.defaultDeviceId || null;
    const runtimeReady = Boolean(options.enabled && workKindEnabled && selectedDeviceId);
    // A company-secret provider that isn't configured on the runtime needs setup.
    const providerKeyMissing = Boolean(
      provider && provider.keyPolicy !== "none" && provider.keyConfigured === false,
    );

    return {
      ok: true,
      teamId: args.teamId,
      role,
      source: args.source || "api",
      workKind: args.workKind,
      enabled: options.enabled,
      workKindEnabled,
      runtimeReady,
      runtime: {
        mode: options.runtime.mode,
        provider: options.runtime.defaultProvider,
        region: options.runtime.region,
        deviceId: selectedDeviceId,
        fallbackDeviceIds: options.runtime.fallbackDeviceIds || [],
      },
      convex: options.convex,
      runner: {
        id: runner,
        model,
        allowedRunners,
        credentialMode: options.runners.credentialMode,
        requireRunnerAuthPerUser: options.runners.requireRunnerAuthPerUser,
        allowUserOverride: options.runners.allowUserOverride,
      },
      provider: {
        id: provider?.id ?? null,
        label: provider?.label,
        baseUrl: provider?.baseUrl,
        keyPolicy: provider?.keyPolicy,
        keyConfigured: provider?.keyConfigured,
        allowedProviders,
      },
      mcp: {
        enabledServers: options.mcp.enabledServers,
        requiredServers: options.mcp.requiredServers,
        toolPolicyByRole: options.mcp.toolPolicyByRole || [],
      },
      approvals: {
        required: approvalsForWorkKind(options as any, args.workKind),
        requireApprovalForProductionWrites: options.approvals.requireApprovalForProductionWrites,
        requireApprovalForDeploy: options.approvals.requireApprovalForDeploy,
        requireApprovalForRobotMotion: options.approvals.requireApprovalForRobotMotion,
        requireApprovalForSecretsAccess: options.approvals.requireApprovalForSecretsAccess,
      },
      dataPolicy: options.dataPolicy,
      promptPolicy: recommendedPromptPolicy(options as any, args.workKind),
      nextActions: {
        configureCompanyAI: !options.enabled,
        configureRuntimeDevice: !selectedDeviceId,
        enableWorkKind: !workKindEnabled,
        reauthRunner: options.runners.credentialMode === "user-auth-on-runtime",
        configureProviderKey: providerKeyMissing,
      },
      dispatch: {
        target: selectedDeviceId ? "yaver-device" : "unresolved",
        deviceId: selectedDeviceId,
        createTaskPath: "/tasks",
        runnerSwitchPath: "/agent/runner/switch",
        runnerStatusPath: "/agent/runners",
        taskOutputPathTemplate: "/tasks/{taskId}/output",
        // OAuth-first runner credential flow. A client that sees a runner
        // is not signed in (runnerAuthStatusPath) starts the subscription
        // OAuth (`--claudeai` / ChatGPT) browser flow on the SELECTED
        // runtime — no raw API keys are ever collected in any UI. For a
        // remote runtime these are reached through the agent's peer proxy
        // (device-targeted), not a public URL.
        runnerAuth: {
          statusPath: "/runner-auth/status",
          browserStartPath: "/runner-auth/browser/start",
          browserStatusPath: "/runner-auth/browser/status",
          browserSubmitCodePath: "/runner-auth/browser/submit-code",
          browserCancelPath: "/runner-auth/browser/cancel",
          // Mirror the owner's already-signed-in local subscription creds
          // onto the runtime instead of re-running OAuth per box.
          credentialsImportPath: "/runner-auth/credentials/import",
        },
      },
    };
  },
});

export const setByToken = mutation({
  args: {
    tokenHash: v.string(),
    teamId: v.string(),
    options: optionsValidator,
  },
  handler: async (ctx, { tokenHash, teamId, options }) => {
    const user = await userForToken(ctx, tokenHash);
    if (!user) throw new Error("Unauthorized");

    const membership = await membershipForUser(ctx, teamId, user._id);
    if (!membership || !canAdmin(membership.role)) {
      throw new Error("Only team admins can update company AI options");
    }

    const now = Date.now();
    const existing = await ctx.db
      .query("companyAIOptions")
      .withIndex("by_teamId", (q) => q.eq("teamId", teamId))
      .first();

    const patch = {
      ...options,
      updatedAt: now,
      updatedBy: user._id,
    };

    if (existing) {
      await ctx.db.patch(existing._id, patch);
      return existing._id;
    }

    return await ctx.db.insert("companyAIOptions", {
      teamId,
      ...options,
      createdAt: now,
      updatedAt: now,
      updatedBy: user._id,
    });
  },
});
