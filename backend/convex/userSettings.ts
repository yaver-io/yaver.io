import { mutation, query, internalMutation, internalQuery } from "./_generated/server";
import { v } from "convex/values";
import { validateSessionInternal, randomHex } from "./auth";
import { isOwner } from "./ownerAllowlist";
import { sanitizeRuntimeGitRemote } from "./runtimeGitRemote";

// Shared validator for the per-subsystem managed toggle. Each field
// accepts boolean (true=Yaver-managed, false=self-hosted) or null
// (explicit clear). Omitting a key leaves its stored value untouched.
// Adding a new subsystem here + in schema.ts is the only place a
// developer touches to surface a new toggle across every Yaver UI.
const managedPatchValidator = v.object({
  relay:     v.optional(v.union(v.boolean(), v.null())),
  dns:       v.optional(v.union(v.boolean(), v.null())),
  analytics: v.optional(v.union(v.boolean(), v.null())),
  storage:   v.optional(v.union(v.boolean(), v.null())),
  email:     v.optional(v.union(v.boolean(), v.null())),
  ci:        v.optional(v.union(v.boolean(), v.null())),
  voice:     v.optional(v.union(v.boolean(), v.null())),
  llm:       v.optional(v.union(v.boolean(), v.null())),
});

const deployPreferencePatchValidator = v.object({
  web: v.optional(v.union(v.string(), v.null())),
  convex: v.optional(v.union(v.string(), v.null())),
  npm: v.optional(v.union(v.string(), v.null())),
  testflight: v.optional(v.union(v.string(), v.null())),
  play: v.optional(v.union(v.string(), v.null())),
});

const appearanceThemePatchValidator = v.object({
  surface: v.union(
    v.literal("mobile"),
    v.literal("web"),
    v.literal("tvos"),
    v.literal("androidtv"),
    v.literal("xbox"),
    v.literal("playstation"),
    v.literal("watchos"),
    v.literal("wearos"),
    v.literal("visionos"),
    v.literal("carplay"),
  ),
  theme: v.union(v.literal("light"), v.literal("dark")),
});

type AppearanceThemeRow = { surface: string; theme: "light" | "dark"; updatedAt: number };

function mergeAppearanceTheme(
  existing: AppearanceThemeRow[] | undefined,
  patch: { surface: string; theme: "light" | "dark" },
): AppearanceThemeRow[] {
  const rows = (existing ?? []).filter((row) => row.surface !== patch.surface);
  return [...rows, {
    surface: patch.surface,
    theme: patch.theme,
    updatedAt: Date.now(),
  }].slice(-10);
}

const openCodeConfigSnapshotPatchValidator = v.object({
  deviceId: v.string(),
  model: v.optional(v.union(v.string(), v.null())),
  provider: v.optional(v.union(v.string(), v.null())),
  defaultAgent: v.optional(v.union(v.string(), v.null())),
  buildModel: v.optional(v.union(v.string(), v.null())),
  planModel: v.optional(v.union(v.string(), v.null())),
  models: v.optional(v.array(v.object({
    id: v.string(),
    name: v.optional(v.string()),
    provider: v.optional(v.string()),
    isDefault: v.optional(v.boolean()),
    source: v.optional(v.string()),
  }))),
  providers: v.optional(v.array(v.object({
    id: v.string(),
    name: v.optional(v.string()),
    baseUrl: v.optional(v.string()),
    hasApiKey: v.optional(v.boolean()),
    models: v.optional(v.array(v.string())),
  }))),
  agents: v.optional(v.array(v.object({
    name: v.string(),
    model: v.optional(v.string()),
    description: v.optional(v.string()),
    isBuiltin: v.optional(v.boolean()),
  }))),
  diagnostics: v.optional(v.array(v.string())),
  updatedAt: v.optional(v.number()),
});

const runtimeProjectPreferenceValidator = v.object({
  deviceId: v.string(),
  projectName: v.optional(v.union(v.string(), v.null())),
  repoName: v.optional(v.union(v.string(), v.null())),
  gitProvider: v.optional(v.union(v.string(), v.null())),
  gitRemote: v.optional(v.union(v.string(), v.null())),
  branch: v.optional(v.union(v.string(), v.null())),
  framework: v.optional(v.union(v.string(), v.null())),
  updatedAt: v.optional(v.number()),
});

const mcpServersPreferenceValidator = v.object({
  deviceId: v.string(),
  mcpServers: v.optional(v.union(v.array(v.string()), v.null())),
  includeYaverMcp: v.optional(v.union(v.boolean(), v.null())),
  updatedAt: v.optional(v.number()),
});

const runtimeProjectCatalogValidator = v.object({
  deviceId: v.string(),
  projects: v.array(v.object({
    projectName: v.string(),
    repoName: v.optional(v.union(v.string(), v.null())),
    gitProvider: v.optional(v.union(v.string(), v.null())),
    gitRemote: v.optional(v.union(v.string(), v.null())),
    branch: v.optional(v.union(v.string(), v.null())),
    framework: v.optional(v.union(v.string(), v.null())),
    updatedAt: v.optional(v.number()),
  })),
  updatedAt: v.optional(v.number()),
});

const mcpCatalogValidator = v.object({
  deviceId: v.string(),
  servers: v.array(v.object({
    name: v.string(),
    url: v.string(),
    enabled: v.boolean(),
    toolCount: v.optional(v.union(v.number(), v.null())),
  })),
  updatedAt: v.optional(v.number()),
});

// The saved render target completes the auto-vibe pair: when a machine has
// BOTH a default project and a default target, the Vibing tab renders without
// a click. Keyed per (deviceId, projectName) so one box can prefer the
// browser iframe for a web app and a physical phone for an RN app; a row
// without projectName is the machine-wide fallback. Only the stable target
// identity is stored — no URLs, ports or device serials.
const runtimeTargetPreferenceValidator = v.object({
  deviceId: v.string(),
  projectName: v.optional(v.union(v.string(), v.null())),
  // null clears the saved target for this (device, project) scope.
  targetId: v.union(v.string(), v.null()),
  targetKind: v.optional(v.union(v.string(), v.null())),
  updatedAt: v.optional(v.number()),
});

type RuntimeTargetPreferenceRow = {
  deviceId: string;
  projectName?: string;
  targetId: string;
  targetKind?: string;
  updatedAt: number;
};

type RuntimeTargetPreferencePatch = {
  deviceId: string;
  projectName?: string | null;
  targetId: string | null;
  targetKind?: string | null;
  updatedAt?: number;
};

function mergeRuntimeTargetPreference(
  existing: RuntimeTargetPreferenceRow[] | undefined,
  payload: RuntimeTargetPreferencePatch,
): RuntimeTargetPreferenceRow[] | undefined {
  const deviceId = cleanRuntimeText(payload.deviceId, 120);
  if (!deviceId) return existing;
  const projectName = cleanRuntimeText(payload.projectName ?? undefined, 200);
  const filtered = (existing ?? []).filter(
    (row) => !(row.deviceId === deviceId && (row.projectName || "") === (projectName || "")),
  );
  const targetId = cleanRuntimeText(payload.targetId ?? undefined, 120);
  if (!targetId) return filtered.length > 0 ? filtered : undefined;
  const row: RuntimeTargetPreferenceRow = {
    deviceId,
    ...(projectName ? { projectName } : {}),
    targetId,
    ...(cleanRuntimeText(payload.targetKind ?? undefined, 120)
      ? { targetKind: cleanRuntimeText(payload.targetKind ?? undefined, 120) }
      : {}),
    updatedAt: payload.updatedAt ?? Date.now(),
  };
  // Bounded like the project catalog: preferences are per (device, project)
  // and a runaway writer must not grow the settings doc without limit.
  return [...filtered, row].slice(-200);
}

// Machine-role slicing (docs/architecture/RUNNER_RENDER_SPLIT.md): which of
// the account's machines runs the AI task (runner) and which serves/builds
// the app (render). OPTIONAL — no row means today's single-box behavior.
// Row without projectName = the account-wide favorite; per-project rows
// override. Identity only: deviceIds + mode flags, never hostnames/paths.
const machineRolesValidator = v.object({
  projectName: v.optional(v.union(v.string(), v.null())),
  // null runnerDeviceId clears the row for this project scope.
  runnerDeviceId: v.union(v.string(), v.null()),
  secondaryRunnerDeviceId: v.optional(v.union(v.string(), v.null())),
  renderDeviceId: v.optional(v.union(v.string(), v.null())),
  secondaryRenderDeviceId: v.optional(v.union(v.string(), v.null())),
  workspace: v.optional(v.union(v.literal("runner-clone"), v.literal("render-ssh"), v.null())),
  autoPush: v.optional(v.union(v.literal("never"), v.literal("ask"), v.literal("always"), v.null())),
  updatedAt: v.optional(v.number()),
});

type MachineRolesRow = {
  projectName?: string;
  runnerDeviceId: string;
  secondaryRunnerDeviceId?: string;
  renderDeviceId?: string;
  secondaryRenderDeviceId?: string;
  workspace?: "runner-clone" | "render-ssh";
  autoPush?: "never" | "ask" | "always";
  updatedAt: number;
};

type MachineRolesPatch = {
  projectName?: string | null;
  runnerDeviceId: string | null;
  secondaryRunnerDeviceId?: string | null;
  renderDeviceId?: string | null;
  secondaryRenderDeviceId?: string | null;
  workspace?: "runner-clone" | "render-ssh" | null;
  autoPush?: "never" | "ask" | "always" | null;
  updatedAt?: number;
};

// Write-time ownership gate for machine-role rows: every referenced device
// must belong to the CALLER. Enforcement of what a role can DO stays at each
// box (owner bearer, fail-closed), so a forged row grants nothing —
// this gate exists so the config store can't even hold another tenant's
// deviceId, same posture as normalizeOwnedDeviceId for primary/secondary.
async function assertMachineRolesOwned(
  ctx: any,
  userId: any,
  payload: MachineRolesPatch,
): Promise<void> {
  for (const [slot, id] of [
    ["runnerDeviceId", payload.runnerDeviceId],
    ["secondaryRunnerDeviceId", payload.secondaryRunnerDeviceId],
    ["renderDeviceId", payload.renderDeviceId],
    ["secondaryRenderDeviceId", payload.secondaryRenderDeviceId],
  ] as const) {
    if (!id) continue;
    const device = await ctx.db
      .query("devices")
      .withIndex("by_deviceId", (q: any) => q.eq("deviceId", id))
      .first();
    if (!device || String(device.userId) !== String(userId)) {
      throw new Error(`${slot} must refer to one of the caller's devices`);
    }
  }
}

function mergeMachineRoles(
  existing: MachineRolesRow[] | undefined,
  payload: MachineRolesPatch,
): MachineRolesRow[] | undefined {
  const projectName = cleanRuntimeText(payload.projectName ?? undefined, 200);
  const filtered = (existing ?? []).filter(
    (row) => (row.projectName || "") !== (projectName || ""),
  );
  const runnerDeviceId = cleanRuntimeText(payload.runnerDeviceId ?? undefined, 120);
  if (!runnerDeviceId) return filtered.length > 0 ? filtered : undefined;
  const secondaryRunnerDeviceId = cleanRuntimeText(payload.secondaryRunnerDeviceId ?? undefined, 120);
  const renderDeviceId = cleanRuntimeText(payload.renderDeviceId ?? undefined, 120);
  const secondaryRenderDeviceId = cleanRuntimeText(payload.secondaryRenderDeviceId ?? undefined, 120);
  const row: MachineRolesRow = {
    ...(projectName ? { projectName } : {}),
    runnerDeviceId,
    ...(secondaryRunnerDeviceId && secondaryRunnerDeviceId !== runnerDeviceId ? { secondaryRunnerDeviceId } : {}),
    ...(renderDeviceId ? { renderDeviceId } : {}),
    ...(secondaryRenderDeviceId && secondaryRenderDeviceId !== (renderDeviceId || runnerDeviceId) ? { secondaryRenderDeviceId } : {}),
    ...(payload.workspace ? { workspace: payload.workspace } : {}),
    ...(payload.autoPush ? { autoPush: payload.autoPush } : {}),
    updatedAt: payload.updatedAt ?? Date.now(),
  };
  return [...filtered, row].slice(-100);
}

type OpenCodeConfigSnapshotPatch = {
  deviceId: string;
  model?: string | null;
  provider?: string | null;
  defaultAgent?: string | null;
  buildModel?: string | null;
  planModel?: string | null;
  models?: Array<{ id: string; name?: string; provider?: string; isDefault?: boolean; source?: string }>;
  providers?: Array<{ id: string; name?: string; baseUrl?: string; hasApiKey?: boolean; models?: string[] }>;
  agents?: Array<{ name: string; model?: string; description?: string; isBuiltin?: boolean }>;
  diagnostics?: string[];
  updatedAt?: number;
};

type RuntimeProjectPreferencePatch = {
  deviceId: string;
  projectName?: string | null;
  repoName?: string | null;
  gitProvider?: string | null;
  gitRemote?: string | null;
  branch?: string | null;
  framework?: string | null;
  updatedAt?: number;
};

type RuntimeProjectPreferenceRow = {
  deviceId: string;
  projectName: string;
  repoName?: string;
  gitProvider?: string;
  gitRemote?: string;
  branch?: string;
  framework?: string;
  updatedAt: number;
};

type RuntimeProjectCatalogPatch = {
  deviceId: string;
  projects: Array<Omit<RuntimeProjectPreferencePatch, "deviceId">>;
  updatedAt?: number;
};

type RuntimeProjectCatalogRow = {
  deviceId: string;
  projects: Array<Omit<RuntimeProjectPreferenceRow, "deviceId">>;
  updatedAt: number;
};

// Per-device MCP selection preference (2026-08-09). Mirrors the
// defaultRuntimeProjectByDevice pattern: replace-by-deviceId so the last
// write wins regardless of surface. `includeYaverMcp` defaults true.
type MCPServersPreferencePatch = {
  deviceId: string;
  mcpServers?: string[] | null;
  includeYaverMcp?: boolean | null;
  updatedAt?: number;
};

type MCPServersPreferenceRow = {
  deviceId: string;
  mcpServers?: string[];
  includeYaverMcp?: boolean;
  updatedAt: number;
};

// Per-device external-MCP catalog (2026-08-13). Which registered MCP servers
// live on which machine — seeded by the Go agent heartbeat so the web chat +
// mobile composers can render another machine's MCPs. Privacy-limited:
// names/URLs/cached tool counts only; auth tokens never leave the agent.
type MCPCatalogPatch = {
  deviceId: string;
  servers: Array<{
    name: string;
    url: string;
    enabled: boolean;
    toolCount?: number | null;
  }>;
  updatedAt?: number;
};

type MCPCatalogRow = {
  deviceId: string;
  servers: Array<{
    name: string;
    url: string;
    enabled: boolean;
    toolCount?: number;
  }>;
  updatedAt: number;
};

type OpenCodeConfigSnapshotRow = {
  deviceId: string;
  model?: string;
  provider?: string;
  defaultAgent?: string;
  buildModel?: string;
  planModel?: string;
  models?: Array<{ id: string; name?: string; provider?: string; isDefault?: boolean; source?: string }>;
  providers?: Array<{ id: string; name?: string; baseUrl?: string; hasApiKey?: boolean; models?: string[] }>;
  agents?: Array<{ name: string; model?: string; description?: string; isBuiltin?: boolean }>;
  diagnostics?: string[];
  updatedAt: number;
};

function cleanRuntimeText(value: string | null | undefined, max = 180): string | undefined {
  const text = String(value ?? "").trim();
  if (!text) return undefined;
  return text.slice(0, max);
}


function sanitizeRuntimeProjectPreference(
  payload: RuntimeProjectPreferencePatch,
): RuntimeProjectPreferenceRow | undefined {
  const deviceId = cleanRuntimeText(payload.deviceId, 120);
  const projectName = cleanRuntimeText(payload.projectName, 160);
  if (!deviceId || !projectName) return undefined;
  const row: RuntimeProjectPreferenceRow = {
    deviceId,
    projectName,
    updatedAt: payload.updatedAt ?? Date.now(),
  };
  const repoName = cleanRuntimeText(payload.repoName, 180);
  const gitProvider = cleanRuntimeText(payload.gitProvider, 60);
  const gitRemote = sanitizeRuntimeGitRemote(payload.gitRemote);
  const branch = cleanRuntimeText(payload.branch, 120);
  const framework = cleanRuntimeText(payload.framework, 80);
  if (repoName) row.repoName = repoName;
  if (gitProvider) row.gitProvider = gitProvider;
  if (gitRemote) row.gitRemote = gitRemote;
  if (branch) row.branch = branch;
  if (framework) row.framework = framework;
  return row;
}

function mergeRuntimeProjectPreference(
  existing: RuntimeProjectPreferenceRow[] | undefined,
  payload: RuntimeProjectPreferencePatch,
): RuntimeProjectPreferenceRow[] | undefined {
  const deviceId = cleanRuntimeText(payload.deviceId, 120);
  if (!deviceId) return existing;
  const filtered = (existing ?? []).filter((row) => row.deviceId !== deviceId);
  const row = sanitizeRuntimeProjectPreference(payload);
  const next = row ? [...filtered, row] : filtered;
  return next.length > 0 ? next : undefined;
}

function mergeRuntimeProjectCatalog(
  existing: RuntimeProjectCatalogRow[] | undefined,
  payload: RuntimeProjectCatalogPatch,
): RuntimeProjectCatalogRow[] | undefined {
  const deviceId = cleanRuntimeText(payload.deviceId, 120);
  if (!deviceId) return existing;
  const seen = new Set<string>();
  const projects = payload.projects
    .map((project) => sanitizeRuntimeProjectPreference({ ...project, deviceId }))
    .filter((row): row is RuntimeProjectPreferenceRow => !!row)
    .map(({ deviceId: _deviceId, ...project }) => project)
    .filter((project) => {
      const key = `${project.gitRemote || ""}|${project.repoName || ""}|${project.projectName}`.toLowerCase();
      if (seen.has(key)) return false;
      seen.add(key);
      return true;
    })
    .slice(0, 200);
  const filtered = (existing ?? []).filter((row) => row.deviceId !== deviceId);
  const next = [...filtered, { deviceId, projects, updatedAt: payload.updatedAt ?? Date.now() }];
  return next.length > 0 ? next : undefined;
}

function sanitizeMCPServersPreference(payload: MCPServersPreferencePatch): MCPServersPreferenceRow | undefined {
  const deviceId = cleanRuntimeText(payload.deviceId, 120);
  if (!deviceId) return undefined;
  const row: MCPServersPreferenceRow = {
    deviceId,
    updatedAt: payload.updatedAt ?? Date.now(),
  };
  const servers = (payload.mcpServers ?? [])
    .map((name) => cleanRuntimeText(name, 80))
    .filter((name): name is string => !!name)
    .slice(0, 40);
  if (servers.length > 0) row.mcpServers = servers;
  if (payload.includeYaverMcp !== undefined && payload.includeYaverMcp !== null) {
    row.includeYaverMcp = !!payload.includeYaverMcp;
  }
  return row;
}

function mergeMCPServersPreference(
  existing: MCPServersPreferenceRow[] | undefined,
  payload: MCPServersPreferencePatch,
): MCPServersPreferenceRow[] | undefined {
  const deviceId = cleanRuntimeText(payload.deviceId, 120);
  if (!deviceId) return existing;
  const filtered = (existing ?? []).filter((row) => row.deviceId !== deviceId);
  const row = sanitizeMCPServersPreference(payload);
  const next = row ? [...filtered, row] : filtered;
  return next.length > 0 ? next : undefined;
}

function sanitizeMCPCatalog(payload: MCPCatalogPatch): MCPCatalogRow | undefined {
  const deviceId = cleanRuntimeText(payload.deviceId, 120);
  if (!deviceId) return undefined;
  const seen = new Set<string>();
  const servers = (payload.servers ?? [])
    .map((server) => {
      const name = cleanRuntimeText(server.name, 80);
      // sanitizeRuntimeGitRemote strips URL credentials + hash — an MCP URL
      // carrying `https://token@host/...` must not let the token into Convex.
      // Non-URL MCP endpoints are invalid anyway, so they're dropped.
      const url = sanitizeRuntimeGitRemote(server.url);
      if (!name || !url) return null;
      return {
        name,
        url,
        enabled: !!server.enabled,
        ...(typeof server.toolCount === "number" && server.toolCount >= 0
          ? { toolCount: Math.min(Math.floor(server.toolCount), 100_000) }
          : {}),
      };
    })
    .filter((server): server is { name: string; url: string; enabled: boolean; toolCount?: number } => {
      if (!server) return false;
      const key = server.name.toLowerCase();
      if (seen.has(key)) return false;
      seen.add(key);
      return true;
    })
    .slice(0, 60);
  if (servers.length === 0) return undefined;
  return { deviceId, servers, updatedAt: payload.updatedAt ?? Date.now() };
}

function mergeMCPCatalog(
  existing: MCPCatalogRow[] | undefined,
  payload: MCPCatalogPatch,
): MCPCatalogRow[] | undefined {
  const deviceId = cleanRuntimeText(payload.deviceId, 120);
  if (!deviceId) return existing;
  const row = sanitizeMCPCatalog(payload);
  if (!row) return existing;
  const filtered = (existing ?? []).filter((r) => r.deviceId !== deviceId);
  const next = [...filtered, row];
  return next.length > 0 ? next : undefined;
}

function sanitizeOpenCodeConfigSnapshot(payload: OpenCodeConfigSnapshotPatch): OpenCodeConfigSnapshotRow {
  const row: OpenCodeConfigSnapshotRow = {
    deviceId: payload.deviceId,
    updatedAt: payload.updatedAt ?? Date.now(),
  };
  const copyString = (key: "model" | "provider" | "defaultAgent" | "buildModel" | "planModel") => {
    const value = payload[key];
    if (typeof value === "string" && value.trim()) row[key] = value.trim();
  };
  copyString("model");
  copyString("provider");
  copyString("defaultAgent");
  copyString("buildModel");
  copyString("planModel");
  const models = (payload.models ?? [])
    .map((m) => ({
      id: String(m.id || "").trim(),
      ...(m.name ? { name: String(m.name) } : {}),
      ...(m.provider ? { provider: String(m.provider) } : {}),
      ...(m.isDefault !== undefined ? { isDefault: !!m.isDefault } : {}),
      ...(m.source ? { source: String(m.source) } : {}),
    }))
    .filter((m) => m.id)
    .slice(0, 80);
  if (models.length) row.models = models;
  const providers = (payload.providers ?? [])
    .map((p) => ({
      id: String(p.id || "").trim(),
      ...(p.name ? { name: String(p.name) } : {}),
      ...(p.baseUrl ? { baseUrl: String(p.baseUrl) } : {}),
      ...(p.hasApiKey !== undefined ? { hasApiKey: !!p.hasApiKey } : {}),
      ...(p.models?.length ? { models: p.models.map((m) => String(m)).filter(Boolean).slice(0, 80) } : {}),
    }))
    .filter((p) => p.id)
    .slice(0, 40);
  if (providers.length) row.providers = providers;
  const agents = (payload.agents ?? [])
    .map((a) => ({
      name: String(a.name || "").trim(),
      ...(a.model ? { model: String(a.model) } : {}),
      ...(a.description ? { description: String(a.description) } : {}),
      ...(a.isBuiltin !== undefined ? { isBuiltin: !!a.isBuiltin } : {}),
    }))
    .filter((a) => a.name)
    .slice(0, 80);
  if (agents.length) row.agents = agents;
  const diagnostics = (payload.diagnostics ?? []).map((d) => String(d).trim()).filter(Boolean).slice(0, 40);
  if (diagnostics.length) row.diagnostics = diagnostics;
  return row;
}

function mergeOpenCodeConfigSnapshot(
  existing: OpenCodeConfigSnapshotRow[] | undefined,
  payload: OpenCodeConfigSnapshotPatch,
): OpenCodeConfigSnapshotRow[] | undefined {
  const row = sanitizeOpenCodeConfigSnapshot(payload);
  const filtered = (existing ?? []).filter((cur) => cur.deviceId !== row.deviceId);
  const next = [...filtered, row];
  return next.length > 0 ? next : undefined;
}

function seedOpenCodePrimaryRunnerRow(
  rows: Array<{ deviceId: string; runnerId: string; model?: string; mode?: string; provider?: string }> | undefined,
  snapshot: OpenCodeConfigSnapshotPatch,
): Array<{ deviceId: string; runnerId: string; model?: string; mode?: string; provider?: string }> | undefined {
  const current = rows ?? [];
  const idx = current.findIndex((row) => row.deviceId === snapshot.deviceId);
  if (idx < 0 || current[idx].runnerId !== "opencode") return rows;
  const row = { ...current[idx] };
  const model = typeof snapshot.model === "string" && snapshot.model.trim()
    ? snapshot.model.trim()
    : typeof snapshot.buildModel === "string" && snapshot.buildModel.trim()
      ? snapshot.buildModel.trim()
      : typeof snapshot.planModel === "string" && snapshot.planModel.trim()
        ? snapshot.planModel.trim()
        : snapshot.models?.find((m) => m.isDefault)?.id;
  const provider = typeof snapshot.provider === "string" && snapshot.provider.trim()
    ? snapshot.provider.trim()
    : model && model.includes("/")
      ? model.split("/")[0]
      : undefined;
  let changed = false;
  if (!row.model && model) {
    row.model = model;
    changed = true;
  }
  if (!row.provider && provider) {
    row.provider = provider;
    changed = true;
  }
  if (!row.mode && typeof snapshot.defaultAgent === "string" && snapshot.defaultAgent.trim()) {
    row.mode = snapshot.defaultAgent.trim();
    changed = true;
  }
  if (!changed) return rows;
  const next = current.slice();
  next[idx] = row;
  return next;
}

// WHICH CODEX MODEL A CHATGPT-ACCOUNT LOGIN CAN RUN IS A MOVING TARGET, AND
// BOTH DIRECTIONS OF GUESS HAVE NOW SHIPPED BROKEN (settled 2026-08-02 by
// PROBING THE ACCOUNT, not by reasoning about names).
//
// Run on the owner's box with `codex exec --model <id>`:
//
//   gpt-5.6-terra   WORKS
//   gpt-5.6-luna    WORKS
//   gpt-5.4         WORKS  (but OpenAI retires it for ChatGPT sign-in 2026-08-31)
//   gpt-5.3-codex   REJECTED — "not supported when using Codex with a ChatGPT
//                   account" (withdrawn for ChatGPT auth on 2026-06-02)
//
// The history here is a warning about naming instinct: "codex-native must be
// the safe one" is intuitive and WRONG — gpt-5.3-codex is the dead one. A
// change landed earlier the same day made every picker lead with it, which is
// what actually broke the vibe loop on every surface (runner.model.not_supported
// on each turn) while looking like a fix for the opposite problem.
//
// So the current id is the RECOMMENDED REPLACEMENT that also outlives the
// 2026-08-31 retirement, and the obsolete set is every id measured or
// announced as unusable on a subscription login. When this needs revisiting,
// probe first: `codex exec --model <id> "reply OK"` on a box signed in with
// the ChatGPT account. Do not infer from the id.
const OBSOLETE_CODEX_MODEL_IDS = new Set([
  "o3-mini",
  "gpt-5-codex",
  "gpt-5.2-codex",
  "gpt-5.3-codex",
]);
const CURRENT_CODEX_MODEL_ID = "gpt-5.6-sol";

type PrimaryRunnerRow = { deviceId: string; runnerId: string; model?: string; reasoningEffort?: string; mode?: string; provider?: string };

const CODEX_REASONING_EFFORTS = new Set(["none", "low", "medium", "high", "xhigh", "max", "ultra"]);

function normalizedRunnerReasoningEffort(runnerId: string, value: unknown): string | undefined {
  if (String(runnerId || "").trim().toLowerCase() !== "codex") return undefined;
  const effort = String(value ?? "").trim().toLowerCase();
  if (!effort) return undefined;
  if (!CODEX_REASONING_EFFORTS.has(effort)) throw new Error("Invalid Codex reasoning effort");
  return effort;
}

export function normalizePrimaryRunnerRowsForClient(
  rows: PrimaryRunnerRow[] | undefined,
): PrimaryRunnerRow[] | undefined {
  if (!Array.isArray(rows) || rows.length === 0) return rows;
  return rows.map((row) => {
    if (
      String(row.runnerId || "").trim().toLowerCase() === "codex" &&
      OBSOLETE_CODEX_MODEL_IDS.has(String(row.model || "").trim())
    ) {
      return { ...row, model: CURRENT_CODEX_MODEL_ID };
    }
    return row;
  });
}

function normalizeSettingsForClient<T extends { primaryRunnerByDevice?: PrimaryRunnerRow[] } | null>(settings: T): T {
  if (!settings?.primaryRunnerByDevice) return settings;
  return {
    ...settings,
    primaryRunnerByDevice: normalizePrimaryRunnerRowsForClient(settings.primaryRunnerByDevice),
  };
}

// Exported: devices.resolveDeviceSig reuses this so the relay's SIGNATURE
// auth path learns the caller's entitlement too — before that, only the
// password path resolved isPaid/plan and sig-authenticated callers were all
// metered as free tier.
export async function relayEntitlementForUser(ctx: any, userId: any): Promise<{
  plan: "free" | "relay-pro" | "cloud-workspace" | "owner-dev";
  isPaid: boolean;
}> {
  const user = await ctx.db.get(userId);
  if (user && isOwner((user as any).email, String(userId))) {
    return { plan: "owner-dev", isPaid: true };
  }
  const subscriptions = await ctx.db
    .query("subscriptions")
    .withIndex("by_user", (q: any) => q.eq("userId", userId))
    .collect();
  const active = subscriptions
    .filter((s: any) => s.status === "active" || s.status === "past_due")
    .sort((a: any, b: any) => (b.updatedAt ?? b.createdAt ?? 0) - (a.updatedAt ?? a.createdAt ?? 0));
  const cloud = active.find((s: any) => {
    const plan = String(s.plan || "");
    return plan === "cloud-workspace" || plan === "cloud-agent" || plan.startsWith("yaver-cloud");
  });
  if (cloud) return { plan: "cloud-workspace", isPaid: true };
  const relay = active.find((s: any) => {
    const plan = String(s.plan || "");
    return plan === "relay-pro" || plan === "relay-monthly" || plan === "relay-yearly" || plan === "managed-relay";
  });
  if (relay) return { plan: "relay-pro", isPaid: true };
  return { plan: "free", isPaid: false };
}

// mergeManagedPatch applies a caller's patch to the existing managed
// object. Booleans win; nulls clear; undefined keeps the previous
// value. Returns the new object with empty keys elided so we don't
// persist fields the user never touched.
function mergeManagedPatch(
  existing: Record<string, boolean | undefined> | undefined,
  patch: Record<string, boolean | null | undefined>,
): Record<string, boolean> | undefined {
  const merged: Record<string, boolean> = {};
  for (const [k, v] of Object.entries(existing ?? {})) {
    if (typeof v === "boolean") merged[k] = v;
  }
  for (const [k, v] of Object.entries(patch)) {
    if (v === null) {
      delete merged[k];
    } else if (typeof v === "boolean") {
      merged[k] = v;
    }
  }
  return Object.keys(merged).length === 0 ? undefined : merged;
}

function mergeDeployPreferencePatch(
  existing: Record<string, string | undefined> | undefined,
  patch: Record<string, string | null | undefined>,
): Record<string, string> | undefined {
  const merged: Record<string, string> = {};
  for (const [k, v] of Object.entries(existing ?? {})) {
    if (typeof v === "string" && v) merged[k] = v;
  }
  for (const [k, v] of Object.entries(patch)) {
    if (v === null) {
      delete merged[k];
    } else if (typeof v === "string" && v) {
      merged[k] = v;
    }
  }
  return Object.keys(merged).length === 0 ? undefined : merged;
}

// normalizeOwnedDeviceId enforces the backend invariant for any
// elevated-slot device pointer (primary, secondary):
//   - exactly zero or one slot value per user
//   - any non-empty slot value must point at a device row owned by
//     that same user
//
// The "only one" part is structural because userSettings stores a
// single scalar per slot, not a per-device flag. The slot label feeds
// the error message so callers can tell which field tripped.
async function normalizeOwnedDeviceId(
  ctx: any,
  userId: string,
  deviceId: string | null | undefined,
  slot: "primaryDeviceId" | "secondaryDeviceId",
): Promise<string | undefined> {
  if (deviceId === undefined) {
    return undefined;
  }
  const next = deviceId ?? undefined;
  if (!next) {
    return undefined;
  }
  const device = await ctx.db
    .query("devices")
    .withIndex("by_deviceId", (q: any) => q.eq("deviceId", next))
    .first();
  if (!device || device.userId !== userId || device.removed) {
    throw new Error(`${slot} must refer to one of the caller's devices`);
  }
  return next;
}

async function patchOwnedDeviceRuntimeProjectCache(
  ctx: any,
  userId: any,
  args: {
    defaultRuntimeProjectForDevice?: RuntimeProjectPreferencePatch;
    runtimeProjectCatalogForDevice?: RuntimeProjectCatalogPatch;
    mcpServersForDevice?: MCPServersPreferencePatch;
    mcpCatalogForDevice?: MCPCatalogPatch;
  },
) {
  const touched = new Set<string>();
  if (args.defaultRuntimeProjectForDevice?.deviceId) touched.add(args.defaultRuntimeProjectForDevice.deviceId);
  if (args.runtimeProjectCatalogForDevice?.deviceId) touched.add(args.runtimeProjectCatalogForDevice.deviceId);
  if (args.mcpServersForDevice?.deviceId) touched.add(args.mcpServersForDevice.deviceId);
  if (args.mcpCatalogForDevice?.deviceId) touched.add(args.mcpCatalogForDevice.deviceId);
  for (const deviceId of touched) {
    const device = await ctx.db
      .query("devices")
      .withIndex("by_deviceId", (q: any) => q.eq("deviceId", deviceId))
      .first();
    if (!device || String(device.userId) !== String(userId)) {
      throw new Error("runtime project device must refer to one of the caller's devices");
    }
    const patch: Record<string, unknown> = {};
    if (args.defaultRuntimeProjectForDevice?.deviceId === deviceId) {
      const row = sanitizeRuntimeProjectPreference(args.defaultRuntimeProjectForDevice);
      if (row) {
        const { deviceId: _deviceId, ...project } = row;
        patch.defaultRuntimeProject = project;
      } else {
        patch.defaultRuntimeProject = undefined;
      }
    }
    if (args.runtimeProjectCatalogForDevice?.deviceId === deviceId) {
      const row = mergeRuntimeProjectCatalog([], args.runtimeProjectCatalogForDevice)?.[0];
      patch.runtimeProjectCatalog = row?.projects ?? [];
    }
    if (args.mcpCatalogForDevice?.deviceId === deviceId) {
      const row = mergeMCPCatalog([], args.mcpCatalogForDevice)?.[0];
      patch.mcpCatalog = row?.servers ?? [];
    }
    if (Object.keys(patch).length > 0) await ctx.db.patch(device._id, patch);
  }
}

export const get = internalQuery({
  args: { userId: v.id("users") },
  handler: async (ctx, args) => {
    const settings = await ctx.db
      .query("userSettings")
      .withIndex("by_userId", (q) => q.eq("userId", args.userId))
      .first();
    return normalizeSettingsForClient(settings);
  },
});

/** Get settings by auth token (used from HTTP endpoints). */
export const getByToken = query({
  args: { tokenHash: v.string() },
  handler: async (ctx, args) => {
    const session = await validateSessionInternal(ctx, args.tokenHash);
    if (!session) return null;
    const settings = await ctx.db
      .query("userSettings")
      .withIndex("by_userId", (q) => q.eq("userId", session.user._id))
      .first();
    return normalizeSettingsForClient(settings);
  },
});

/** Get the RAW settings row by userId (internal — used by the managed-relay
 *  wiring so it can decide whether to repoint the user's relayUrl without
 *  overwriting a custom self-hosted relay). */
export const getByUserId = internalQuery({
  args: { userId: v.id("users") },
  handler: async (ctx, { userId }) => {
    return await ctx.db
      .query("userSettings")
      .withIndex("by_userId", (q) => q.eq("userId", userId))
      .first();
  },
});

export const set = internalMutation({
  args: {
    userId: v.id("users"),
    forceRelay: v.optional(v.boolean()),
    runnerId: v.optional(v.string()),
    customRunnerCommand: v.optional(v.string()),
    relayUrl: v.optional(v.string()),
    relayPassword: v.optional(v.string()),
    tunnelUrl: v.optional(v.string()),
    speechProvider: v.optional(v.string()),
    // Legacy only. Do not accept or persist new provider API keys here:
    // speech credentials must stay in device SecureStore or the local
    // agent vault/P2P vault sync path, never Convex.
    speechApiKey: v.optional(v.string()),
    ttsEnabled: v.optional(v.boolean()),
    ttsProvider: v.optional(v.string()),
    ttsTaskMode: v.optional(v.boolean()),
    keyStorage: v.optional(v.string()),
    // Mobile per-task device + agent picker. Stored on the user record
    // so the toggle roams across phones / re-installs.
    multiTargetMode: v.optional(v.boolean()),
    autoRenderVibing: v.optional(v.boolean()),
    appearanceThemeForSurface: v.optional(appearanceThemePatchValidator),
    connectionMode: v.optional(v.string()),
    moreOptionalTools: v.optional(v.array(v.string())),
    // null sentinel = clear the preference; undefined = leave untouched.
    primaryDeviceId: v.optional(v.union(v.string(), v.null())),
    secondaryDeviceId: v.optional(v.union(v.string(), v.null())),
    // Set or clear the primary runner for a single device. The whole
    // primaryRunnerByDevice list lives on the userSettings row, but
    // mutations only ever touch one entry at a time so the wire shape
    // stays small. runnerId=null clears the entry for that device.
    primaryRunnerForDevice: v.optional(
      v.object({
        deviceId: v.string(),
        runnerId: v.union(v.string(), v.null()),
        // Optional model hint. null clears just the model (keeps the
        // runner selection). undefined leaves the existing model alone.
        model: v.optional(v.union(v.string(), v.null())),
        // Codex-only. null clears; undefined preserves when the runner is
        // unchanged. Model-specific support is reconciled against the live
        // agent catalog by clients before dispatch.
        reasoningEffort: v.optional(v.union(v.string(), v.null())),
        // Optional runner sub-selection. Used by OpenCode's
        // `--agent <mode>` path. null clears the saved mode.
        mode: v.optional(v.union(v.string(), v.null())),
        // Optional provider hint such as "zai" / "glm" / "ollama".
        // Secrets remain host-local; this only remembers the user's
        // preference across surfaces.
        provider: v.optional(v.union(v.string(), v.null())),
      }),
    ),
    opencodeConfigForDevice: v.optional(openCodeConfigSnapshotPatchValidator),
    defaultRuntimeProjectForDevice: v.optional(runtimeProjectPreferenceValidator),
    mcpServersForDevice: v.optional(mcpServersPreferenceValidator),
    defaultRuntimeTargetForDevice: v.optional(runtimeTargetPreferenceValidator),
    machineRolesForProject: v.optional(machineRolesValidator),
    runtimeProjectCatalogForDevice: v.optional(runtimeProjectCatalogValidator),
    mcpCatalogForDevice: v.optional(mcpCatalogValidator),
    // Per-subsystem managed: true (Yaver-hosted) | false (user-hosted)
    // | null (unset → use legacy default). Clients send only the
    // subsystem(s) they're changing; unspecified keys retain their
    // existing value. Null on any key clears that subsystem.
    managed: v.optional(managedPatchValidator),
    deployPreferences: v.optional(deployPreferencePatchValidator),
    // Which tab the mobile app opens on. Stored here so the choice follows the
    // ACCOUNT across devices; the phone keeps a local copy because boot cannot
    // wait on a round-trip without flashing the wrong tab.
    startupScreen: v.optional(v.union(v.literal("projects"), v.literal("tasks"), v.null())),
  },
  handler: async (ctx, args) => {
    const normalizedPrimaryDeviceId = await normalizeOwnedDeviceId(
      ctx,
      args.userId,
      args.primaryDeviceId,
      "primaryDeviceId",
    );
    const normalizedSecondaryDeviceId = await normalizeOwnedDeviceId(
      ctx,
      args.userId,
      args.secondaryDeviceId,
      "secondaryDeviceId",
    );
    const existing = await ctx.db
      .query("userSettings")
      .withIndex("by_userId", (q) => q.eq("userId", args.userId))
      .first();
    // Only include fields that were explicitly provided — undefined fields must NOT
    // overwrite existing values (e.g. relayUrl/relayPassword set during signup).
    const patch: Record<string, unknown> = {};
    if (args.forceRelay !== undefined) patch.forceRelay = args.forceRelay;
    if (args.runnerId !== undefined) patch.runnerId = args.runnerId;
    if (args.customRunnerCommand !== undefined) patch.customRunnerCommand = args.customRunnerCommand;
    if (args.relayUrl !== undefined) patch.relayUrl = args.relayUrl;
    if (args.relayPassword !== undefined) patch.relayPassword = args.relayPassword;
    if (args.tunnelUrl !== undefined) patch.tunnelUrl = args.tunnelUrl;
    if (args.speechProvider !== undefined) patch.speechProvider = args.speechProvider;
    // Intentionally ignored. See speechApiKey validator comment above.
    if (args.ttsEnabled !== undefined) patch.ttsEnabled = args.ttsEnabled;
    if (args.ttsProvider !== undefined) patch.ttsProvider = args.ttsProvider;
    if (args.ttsTaskMode !== undefined) patch.ttsTaskMode = args.ttsTaskMode;
    if (args.keyStorage !== undefined) patch.keyStorage = args.keyStorage;
    if (args.multiTargetMode !== undefined) patch.multiTargetMode = args.multiTargetMode;
    if (args.autoRenderVibing !== undefined) patch.autoRenderVibing = args.autoRenderVibing;
    if (args.appearanceThemeForSurface !== undefined) {
      patch.appearanceThemeBySurface = mergeAppearanceTheme(
        existing?.appearanceThemeBySurface as AppearanceThemeRow[] | undefined,
        args.appearanceThemeForSurface,
      );
    }
    // Only the two values the clients understand. An unknown string would
    // silently read as "not single" everywhere and be impossible to debug.
    if (args.connectionMode !== undefined && (args.connectionMode === "all" || args.connectionMode === "single")) {
      patch.connectionMode = args.connectionMode;
    }
    if (args.moreOptionalTools !== undefined) patch.moreOptionalTools = args.moreOptionalTools;
    if (args.primaryDeviceId !== undefined) {
      patch.primaryDeviceId = normalizedPrimaryDeviceId;
    }
    if (args.secondaryDeviceId !== undefined) {
      patch.secondaryDeviceId = normalizedSecondaryDeviceId;
    }
    if (args.primaryRunnerForDevice !== undefined) {
      const cur = (existing?.primaryRunnerByDevice ?? []) as PrimaryRunnerRow[];
      const payload = args.primaryRunnerForDevice;
      const filtered = cur.filter((row) => row.deviceId !== payload.deviceId);
      let next = filtered;
      if (payload.runnerId) {
        // Resolve the effective model: explicit string → use it; null →
        // clear any existing model on that device; undefined → preserve
        // the current model if the runner is unchanged.
        const prevRow = cur.find((row) => row.deviceId === payload.deviceId);
        let model: string | undefined;
        if (payload.model === null) {
          model = undefined;
        } else if (payload.model !== undefined) {
          model = payload.model;
        } else if (prevRow?.runnerId === payload.runnerId) {
          model = prevRow.model;
        }
        let mode: string | undefined;
        if (payload.mode === null) {
          mode = undefined;
        } else if (payload.mode !== undefined) {
          mode = payload.mode;
        } else if (prevRow?.runnerId === payload.runnerId) {
          mode = prevRow.mode;
        }
        let reasoningEffort: string | undefined;
        if (payload.reasoningEffort === null) {
          reasoningEffort = undefined;
        } else if (payload.reasoningEffort !== undefined) {
          reasoningEffort = normalizedRunnerReasoningEffort(payload.runnerId, payload.reasoningEffort);
        } else if (prevRow?.runnerId === payload.runnerId) {
          reasoningEffort = normalizedRunnerReasoningEffort(payload.runnerId, prevRow.reasoningEffort);
        }
        let provider: string | undefined;
        if (payload.provider === null) {
          provider = undefined;
        } else if (payload.provider !== undefined) {
          provider = payload.provider;
        } else if (prevRow?.runnerId === payload.runnerId) {
          provider = prevRow.provider;
        }
        const row: PrimaryRunnerRow = {
          deviceId: payload.deviceId,
          runnerId: payload.runnerId,
        };
        if (model) row.model = model;
        if (reasoningEffort) row.reasoningEffort = reasoningEffort;
        if (mode) row.mode = mode;
        if (provider) row.provider = provider;
        next = [...filtered, row];
      }
      patch.primaryRunnerByDevice = next.length > 0 ? next : undefined;
    }
    if (args.opencodeConfigForDevice !== undefined) {
      patch.opencodeConfigByDevice = mergeOpenCodeConfigSnapshot(
        existing?.opencodeConfigByDevice as OpenCodeConfigSnapshotRow[] | undefined,
        args.opencodeConfigForDevice as OpenCodeConfigSnapshotPatch,
      );
      const seeded = seedOpenCodePrimaryRunnerRow(
        (patch.primaryRunnerByDevice as PrimaryRunnerRow[] | undefined) ??
          (existing?.primaryRunnerByDevice as PrimaryRunnerRow[] | undefined),
        args.opencodeConfigForDevice as OpenCodeConfigSnapshotPatch,
      );
      if (seeded !== undefined) patch.primaryRunnerByDevice = seeded;
    }
    if (args.defaultRuntimeProjectForDevice !== undefined) {
      patch.defaultRuntimeProjectByDevice = mergeRuntimeProjectPreference(
        existing?.defaultRuntimeProjectByDevice as RuntimeProjectPreferenceRow[] | undefined,
        args.defaultRuntimeProjectForDevice as RuntimeProjectPreferencePatch,
      );
    }
    if (args.mcpServersForDevice !== undefined) {
      patch.mcpServersByDevice = mergeMCPServersPreference(
        existing?.mcpServersByDevice as MCPServersPreferenceRow[] | undefined,
        args.mcpServersForDevice as MCPServersPreferencePatch,
      );
    }
    if (args.defaultRuntimeTargetForDevice !== undefined) {
      patch.defaultRuntimeTargetByDevice = mergeRuntimeTargetPreference(
        existing?.defaultRuntimeTargetByDevice as RuntimeTargetPreferenceRow[] | undefined,
        args.defaultRuntimeTargetForDevice as RuntimeTargetPreferencePatch,
      );
    }
    if (args.machineRolesForProject !== undefined) {
      await assertMachineRolesOwned(ctx, args.userId, args.machineRolesForProject as MachineRolesPatch);
      patch.machineRolesByProject = mergeMachineRoles(
        existing?.machineRolesByProject as MachineRolesRow[] | undefined,
        args.machineRolesForProject as MachineRolesPatch,
      );
    }
    if (args.runtimeProjectCatalogForDevice !== undefined) {
      patch.runtimeProjectCatalogByDevice = mergeRuntimeProjectCatalog(
        existing?.runtimeProjectCatalogByDevice as RuntimeProjectCatalogRow[] | undefined,
        args.runtimeProjectCatalogForDevice as RuntimeProjectCatalogPatch,
      );
    }
    if (args.mcpCatalogForDevice !== undefined) {
      patch.mcpCatalogByDevice = mergeMCPCatalog(
        existing?.mcpCatalogByDevice as MCPCatalogRow[] | undefined,
        args.mcpCatalogForDevice as MCPCatalogPatch,
      );
    }
    await patchOwnedDeviceRuntimeProjectCache(ctx, args.userId, {
      defaultRuntimeProjectForDevice: args.defaultRuntimeProjectForDevice as RuntimeProjectPreferencePatch | undefined,
      mcpServersForDevice: args.mcpServersForDevice as MCPServersPreferencePatch | undefined,
      runtimeProjectCatalogForDevice: args.runtimeProjectCatalogForDevice as RuntimeProjectCatalogPatch | undefined,
      mcpCatalogForDevice: args.mcpCatalogForDevice as MCPCatalogPatch | undefined,
    });
    if (args.managed !== undefined) {
      patch.managed = mergeManagedPatch(
        existing?.managed as Record<string, boolean | undefined> | undefined,
        args.managed as Record<string, boolean | null | undefined>,
      );
    }
    if (args.deployPreferences !== undefined) {
      patch.deployPreferences = mergeDeployPreferencePatch(
        existing?.deployPreferences as Record<string, string | undefined> | undefined,
        args.deployPreferences as Record<string, string | null | undefined>,
      );
    }
    const normalizedPrimaryRunnerRows = normalizePrimaryRunnerRowsForClient(
      (patch.primaryRunnerByDevice as PrimaryRunnerRow[] | undefined) ??
        (existing?.primaryRunnerByDevice as PrimaryRunnerRow[] | undefined),
    );
    if (normalizedPrimaryRunnerRows !== undefined) patch.primaryRunnerByDevice = normalizedPrimaryRunnerRows;
    if (existing) {
      await ctx.db.patch(existing._id, patch);
    } else {
      await ctx.db.insert("userSettings", {
        userId: args.userId,
        ...patch,
      });
    }
  },
});

/** Set settings by auth token (used from HTTP endpoints). */
export const setByToken = mutation({
  args: {
    tokenHash: v.string(),
    forceRelay: v.optional(v.boolean()),
    runnerId: v.optional(v.string()),
    customRunnerCommand: v.optional(v.string()),
    relayUrl: v.optional(v.string()),
    relayPassword: v.optional(v.string()),
    tunnelUrl: v.optional(v.string()),
    speechProvider: v.optional(v.string()),
    // Legacy only; ignored on write. Provider keys stay local/vault-only.
    speechApiKey: v.optional(v.string()),
    ttsEnabled: v.optional(v.boolean()),
    ttsProvider: v.optional(v.string()),
    ttsTaskMode: v.optional(v.boolean()),
    keyStorage: v.optional(v.string()),
    multiTargetMode: v.optional(v.boolean()),
    autoRenderVibing: v.optional(v.boolean()),
    appearanceThemeForSurface: v.optional(appearanceThemePatchValidator),
    connectionMode: v.optional(v.string()),
    moreOptionalTools: v.optional(v.array(v.string())),
    primaryDeviceId: v.optional(v.union(v.string(), v.null())),
    secondaryDeviceId: v.optional(v.union(v.string(), v.null())),
    primaryRunnerForDevice: v.optional(
      v.object({
        deviceId: v.string(),
        runnerId: v.union(v.string(), v.null()),
        // Optional model hint. null clears just the model (keeps the
        // runner selection). undefined leaves the existing model alone.
        model: v.optional(v.union(v.string(), v.null())),
        reasoningEffort: v.optional(v.union(v.string(), v.null())),
        mode: v.optional(v.union(v.string(), v.null())),
        provider: v.optional(v.union(v.string(), v.null())),
      }),
    ),
    opencodeConfigForDevice: v.optional(openCodeConfigSnapshotPatchValidator),
    defaultRuntimeProjectForDevice: v.optional(runtimeProjectPreferenceValidator),
    mcpServersForDevice: v.optional(mcpServersPreferenceValidator),
    defaultRuntimeTargetForDevice: v.optional(runtimeTargetPreferenceValidator),
    machineRolesForProject: v.optional(machineRolesValidator),
    runtimeProjectCatalogForDevice: v.optional(runtimeProjectCatalogValidator),
    managed: v.optional(managedPatchValidator),
    deployPreferences: v.optional(deployPreferencePatchValidator),
    // Which tab the mobile app opens on. Stored here so the choice follows the
    // ACCOUNT across devices; the phone keeps a local copy because boot cannot
    // wait on a round-trip without flashing the wrong tab.
    startupScreen: v.optional(v.union(v.literal("projects"), v.literal("tasks"), v.null())),
  },
  handler: async (ctx, args) => {
    const session = await validateSessionInternal(ctx, args.tokenHash);
    if (!session) throw new Error("Unauthorized");
    const userId = session.user._id;
    const normalizedPrimaryDeviceId = await normalizeOwnedDeviceId(
      ctx,
      userId,
      args.primaryDeviceId,
      "primaryDeviceId",
    );
    const normalizedSecondaryDeviceId = await normalizeOwnedDeviceId(
      ctx,
      userId,
      args.secondaryDeviceId,
      "secondaryDeviceId",
    );
    const existing = await ctx.db
      .query("userSettings")
      .withIndex("by_userId", (q) => q.eq("userId", userId))
      .first();
    // Only include fields that were explicitly provided — undefined fields must NOT
    // overwrite existing values (e.g. relayUrl/relayPassword set during signup).
    const patch: Record<string, unknown> = {};
    if (args.forceRelay !== undefined) patch.forceRelay = args.forceRelay;
    if (args.runnerId !== undefined) patch.runnerId = args.runnerId;
    if (args.customRunnerCommand !== undefined) patch.customRunnerCommand = args.customRunnerCommand;
    if (args.relayUrl !== undefined) patch.relayUrl = args.relayUrl;
    if (args.relayPassword !== undefined) patch.relayPassword = args.relayPassword;
    if (args.tunnelUrl !== undefined) patch.tunnelUrl = args.tunnelUrl;
    if (args.speechProvider !== undefined) patch.speechProvider = args.speechProvider;
    // Intentionally ignored. Provider keys must not be written to Convex.
    if (args.ttsEnabled !== undefined) patch.ttsEnabled = args.ttsEnabled;
    if (args.ttsProvider !== undefined) patch.ttsProvider = args.ttsProvider;
    if (args.ttsTaskMode !== undefined) patch.ttsTaskMode = args.ttsTaskMode;
    if (args.keyStorage !== undefined) patch.keyStorage = args.keyStorage;
    if (args.multiTargetMode !== undefined) patch.multiTargetMode = args.multiTargetMode;
    if (args.autoRenderVibing !== undefined) patch.autoRenderVibing = args.autoRenderVibing;
    if (args.appearanceThemeForSurface !== undefined) {
      patch.appearanceThemeBySurface = mergeAppearanceTheme(
        existing?.appearanceThemeBySurface as AppearanceThemeRow[] | undefined,
        args.appearanceThemeForSurface,
      );
    }
    // Only the two values the clients understand. An unknown string would
    // silently read as "not single" everywhere and be impossible to debug.
    if (args.connectionMode !== undefined && (args.connectionMode === "all" || args.connectionMode === "single")) {
      patch.connectionMode = args.connectionMode;
    }
    if (args.moreOptionalTools !== undefined) patch.moreOptionalTools = args.moreOptionalTools;
    if (args.primaryDeviceId !== undefined) {
      patch.primaryDeviceId = normalizedPrimaryDeviceId;
    }
    if (args.secondaryDeviceId !== undefined) {
      patch.secondaryDeviceId = normalizedSecondaryDeviceId;
    }
    if (args.primaryRunnerForDevice !== undefined) {
      const cur = (existing?.primaryRunnerByDevice ?? []) as PrimaryRunnerRow[];
      const payload = args.primaryRunnerForDevice;
      const filtered = cur.filter((row) => row.deviceId !== payload.deviceId);
      let next = filtered;
      if (payload.runnerId) {
        // Resolve the effective model: explicit string → use it; null →
        // clear any existing model on that device; undefined → preserve
        // the current model if the runner is unchanged.
        const prevRow = cur.find((row) => row.deviceId === payload.deviceId);
        let model: string | undefined;
        if (payload.model === null) {
          model = undefined;
        } else if (payload.model !== undefined) {
          model = payload.model;
        } else if (prevRow?.runnerId === payload.runnerId) {
          model = prevRow.model;
        }
        let mode: string | undefined;
        if (payload.mode === null) {
          mode = undefined;
        } else if (payload.mode !== undefined) {
          mode = payload.mode;
        } else if (prevRow?.runnerId === payload.runnerId) {
          mode = prevRow.mode;
        }
        let reasoningEffort: string | undefined;
        if (payload.reasoningEffort === null) {
          reasoningEffort = undefined;
        } else if (payload.reasoningEffort !== undefined) {
          reasoningEffort = normalizedRunnerReasoningEffort(payload.runnerId, payload.reasoningEffort);
        } else if (prevRow?.runnerId === payload.runnerId) {
          reasoningEffort = normalizedRunnerReasoningEffort(payload.runnerId, prevRow.reasoningEffort);
        }
        let provider: string | undefined;
        if (payload.provider === null) {
          provider = undefined;
        } else if (payload.provider !== undefined) {
          provider = payload.provider;
        } else if (prevRow?.runnerId === payload.runnerId) {
          provider = prevRow.provider;
        }
        const row: PrimaryRunnerRow = {
          deviceId: payload.deviceId,
          runnerId: payload.runnerId,
        };
        if (model) row.model = model;
        if (reasoningEffort) row.reasoningEffort = reasoningEffort;
        if (mode) row.mode = mode;
        if (provider) row.provider = provider;
        next = [...filtered, row];
      }
      patch.primaryRunnerByDevice = next.length > 0 ? next : undefined;
    }
    if (args.opencodeConfigForDevice !== undefined) {
      patch.opencodeConfigByDevice = mergeOpenCodeConfigSnapshot(
        existing?.opencodeConfigByDevice as OpenCodeConfigSnapshotRow[] | undefined,
        args.opencodeConfigForDevice as OpenCodeConfigSnapshotPatch,
      );
      const seeded = seedOpenCodePrimaryRunnerRow(
        (patch.primaryRunnerByDevice as PrimaryRunnerRow[] | undefined) ??
          (existing?.primaryRunnerByDevice as PrimaryRunnerRow[] | undefined),
        args.opencodeConfigForDevice as OpenCodeConfigSnapshotPatch,
      );
      if (seeded !== undefined) patch.primaryRunnerByDevice = seeded;
    }
    if (args.defaultRuntimeProjectForDevice !== undefined) {
      patch.defaultRuntimeProjectByDevice = mergeRuntimeProjectPreference(
        existing?.defaultRuntimeProjectByDevice as RuntimeProjectPreferenceRow[] | undefined,
        args.defaultRuntimeProjectForDevice as RuntimeProjectPreferencePatch,
      );
    }
    if (args.mcpServersForDevice !== undefined) {
      patch.mcpServersByDevice = mergeMCPServersPreference(
        existing?.mcpServersByDevice as MCPServersPreferenceRow[] | undefined,
        args.mcpServersForDevice as MCPServersPreferencePatch,
      );
    }
    if (args.defaultRuntimeTargetForDevice !== undefined) {
      patch.defaultRuntimeTargetByDevice = mergeRuntimeTargetPreference(
        existing?.defaultRuntimeTargetByDevice as RuntimeTargetPreferenceRow[] | undefined,
        args.defaultRuntimeTargetForDevice as RuntimeTargetPreferencePatch,
      );
    }
    if (args.machineRolesForProject !== undefined) {
      await assertMachineRolesOwned(ctx, userId, args.machineRolesForProject as MachineRolesPatch);
      patch.machineRolesByProject = mergeMachineRoles(
        existing?.machineRolesByProject as MachineRolesRow[] | undefined,
        args.machineRolesForProject as MachineRolesPatch,
      );
    }
    if (args.runtimeProjectCatalogForDevice !== undefined) {
      patch.runtimeProjectCatalogByDevice = mergeRuntimeProjectCatalog(
        existing?.runtimeProjectCatalogByDevice as RuntimeProjectCatalogRow[] | undefined,
        args.runtimeProjectCatalogForDevice as RuntimeProjectCatalogPatch,
      );
    }
    await patchOwnedDeviceRuntimeProjectCache(ctx, userId, {
      defaultRuntimeProjectForDevice: args.defaultRuntimeProjectForDevice as RuntimeProjectPreferencePatch | undefined,
      mcpServersForDevice: args.mcpServersForDevice as MCPServersPreferencePatch | undefined,
      runtimeProjectCatalogForDevice: args.runtimeProjectCatalogForDevice as RuntimeProjectCatalogPatch | undefined,
    });
    if (args.managed !== undefined) {
      patch.managed = mergeManagedPatch(
        existing?.managed as Record<string, boolean | undefined> | undefined,
        args.managed as Record<string, boolean | null | undefined>,
      );
    }
    if (args.deployPreferences !== undefined) {
      patch.deployPreferences = mergeDeployPreferencePatch(
        existing?.deployPreferences as Record<string, string | undefined> | undefined,
        args.deployPreferences as Record<string, string | null | undefined>,
      );
    }
    const normalizedPrimaryRunnerRows = normalizePrimaryRunnerRowsForClient(
      (patch.primaryRunnerByDevice as PrimaryRunnerRow[] | undefined) ??
        (existing?.primaryRunnerByDevice as PrimaryRunnerRow[] | undefined),
    );
    if (normalizedPrimaryRunnerRows !== undefined) patch.primaryRunnerByDevice = normalizedPrimaryRunnerRows;
    if (existing) {
      await ctx.db.patch(existing._id, patch);
    } else {
      await ctx.db.insert("userSettings", {
        userId,
        ...patch,
      });
    }
  },
});

/** Admin: set settings by email (for manual user configuration). */
export const setByEmail = internalMutation({
  args: {
    email: v.string(),
    speechProvider: v.optional(v.string()),
    speechApiKey: v.optional(v.string()),
    ttsEnabled: v.optional(v.boolean()),
    ttsProvider: v.optional(v.string()),
    ttsTaskMode: v.optional(v.boolean()),
    keyStorage: v.optional(v.string()),
    forceRelay: v.optional(v.boolean()),
    runnerId: v.optional(v.string()),
    customRunnerCommand: v.optional(v.string()),
    relayUrl: v.optional(v.string()),
    relayPassword: v.optional(v.string()),
    tunnelUrl: v.optional(v.string()),
  },
  handler: async (ctx, args) => {
    const user = await ctx.db
      .query("users")
      .filter((q) => q.eq(q.field("email"), args.email))
      .first();
    if (!user) throw new Error(`User not found: ${args.email}`);
    const existing = await ctx.db
      .query("userSettings")
      .withIndex("by_userId", (q) => q.eq("userId", user._id))
      .first();
    const { email: _, ...fields } = args;
    if (existing) {
      await ctx.db.patch(existing._id, fields);
    } else {
      await ctx.db.insert("userSettings", { userId: user._id, ...fields });
    }
    return { ok: true, userId: user._id };
  },
});

/**
 * Seed default settings for all users who don't have settings yet.
 * Also generates per-user relay passwords and sets relayUrl for users missing them.
 * Run once: npx convex run userSettings:seedDefaults
 */
export const seedDefaults = internalMutation({
  args: {},
  handler: async (ctx) => {
    // Fetch default relay URL from platform config
    const config = await ctx.db
      .query("platformConfig")
      .withIndex("by_key", (q) => q.eq("key", "relay_servers"))
      .unique();
    let defaultRelayUrl: string | undefined;
    let platformRelayPassword: string | undefined;
    if (config?.value) {
      try {
        const relays = JSON.parse(config.value);
        if (Array.isArray(relays) && relays.length > 0) {
          defaultRelayUrl = relays[0].httpUrl;
          platformRelayPassword = relays[0].password;
        }
      } catch { /* ignore */ }
    }

    const allUsers = await ctx.db.query("users").collect();
    let seeded = 0;
    let updated = 0;
    for (const user of allUsers) {
      const existing = await ctx.db
        .query("userSettings")
        .withIndex("by_userId", (q) => q.eq("userId", user._id))
        .first();
      if (!existing) {
        await ctx.db.insert("userSettings", {
          userId: user._id,
          forceRelay: false,
          relayUrl: defaultRelayUrl,
          relayPassword: randomHex(24),
          moreOptionalTools: [],
          deployPreferences: { web: "auto", convex: "auto", npm: "ask", testflight: "ask", play: "ask" },
        });
        seeded++;
      } else if (!existing.relayPassword || existing.relayPassword === platformRelayPassword || existing.relayUrl !== defaultRelayUrl || existing.moreOptionalTools === undefined || existing.deployPreferences === undefined) {
        // Keep relay URL synced to the platform free relay, but never
        // copy the platform/shared relay password into user rows. Public
        // clients get per-user random relay credentials.
        const patch: Record<string, unknown> = {};
        if (!existing.relayPassword || existing.relayPassword === platformRelayPassword) {
          patch.relayPassword = randomHex(24);
        }
        if (defaultRelayUrl && existing.relayUrl !== defaultRelayUrl) {
          patch.relayUrl = defaultRelayUrl;
        }
        if (existing.moreOptionalTools === undefined) {
          patch.moreOptionalTools = [];
        }
        if (existing.deployPreferences === undefined) {
          patch.deployPreferences = { web: "auto", convex: "auto", npm: "ask", testflight: "ask", play: "ask" };
        }
        if (Object.keys(patch).length > 0) {
          await ctx.db.patch(existing._id, patch);
          updated++;
        }
      }
    }
    return { seeded, updated, total: allUsers.length };
  },
});

/**
 * Phase 2C — point this user's OTHER self-hosted devices at THEIR
 * managed-cloud box as relay (instead of the shared free platform
 * relay). Called from cloudMachines.provision once the box is up;
 * each other device picks the new endpoint up on its next
 * FetchUserSettings (main.go:2478) → relayUrl + relayPassword.
 * Upserts: inserts a minimal row if absent. Privacy-safe — relayUrl
 * is the box's public hostname (same class as the relayUrl seedDefaults
 * already writes); relayPassword stays on the box + the managedRelays
 * row. Pass undefined for either field to leave it untouched (e.g. on
 * decommission, clear by passing the platform default back in).
 * NOTE Phase 2D gap (main.go:2492-2503): the agent currently drops
 * userSettings.RelayUrl that doesn't match a platformConfig entry —
 * fix is to synthesize a RelayServerInfo from the URL. Until that
 * ships in a `cli/v*` release, OTHER devices won't actually use this.
 */
export const setRelayForUser = internalMutation({
  args: {
    userId: v.id("users"),
    relayUrl: v.optional(v.string()),
    relayPassword: v.optional(v.string()),
  },
  handler: async (ctx, args) => {
    const existing = await ctx.db
      .query("userSettings")
      .withIndex("by_userId", (q) => q.eq("userId", args.userId))
      .first();
    if (!existing) {
      await ctx.db.insert("userSettings", {
        userId: args.userId,
        forceRelay: false,
        relayUrl: args.relayUrl,
        relayPassword: args.relayPassword,
      });
      return;
    }
    const patch: Record<string, unknown> = {};
    if (args.relayUrl !== undefined) patch.relayUrl = args.relayUrl;
    if (args.relayPassword !== undefined) patch.relayPassword = args.relayPassword;
    if (Object.keys(patch).length > 0) {
      await ctx.db.patch(existing._id, patch);
    }
  },
});

/**
 * clearRelayForUser — remove the userSettings relay pointer (relayUrl +
 * relayPassword) that the managed-box provision wired. 2026-08-10: decommission
 * never cleared this, so the NEXT provisioned box's agent fetched userSettings
 * on serve start and cached the PREVIOUS (dead) box's relay — the new box then
 * dialed `mn72z84j.cloud.yaver.io:4433`, an address deleted at decommission,
 * and became unreachable via relay (dashboard "not verified" + no device row).
 * Call this when the decommissioned machine WAS the user's relay (its hostname
 * matches the stored relayUrl). setRelayForUser cannot clear (undefined means
 * "don't touch") — a patch with explicit undefined deletes the field.
 */
export const clearRelayForUser = internalMutation({
  args: { userId: v.id("users") },
  handler: async (ctx, { userId }) => {
    const existing = await ctx.db
      .query("userSettings")
      .withIndex("by_userId", (q) => q.eq("userId", userId))
      .first();
    if (!existing) return;
    await ctx.db.patch(existing._id, {
      relayUrl: undefined,
      relayPassword: undefined,
    });
  },
});

/**
 * Repair the caller's userSettings row so it has a per-user free-relay
 * credential and the current platform-managed relay URL. Used when
 * the preview iframe keeps getting 401 "invalid relay password" from
 * the managed relay — typically because the row is missing a password
 * or points at an old relay URL.
 *
 * Safe by design: the secret is per-user random material generated by
 * Convex, never copied from platformConfig and never baked into the app.
 */
export const repairRelayPassword = internalMutation({
  args: { tokenHash: v.string() },
  handler: async (ctx, args) => {
    const session = await validateSessionInternal(ctx, args.tokenHash);
    if (!session) {
      return { ok: false, repaired: false, reason: "unauthorized" };
    }

    let defaultRelayUrl: string | undefined;
    let platformRelayPassword: string | undefined;
    const config = await ctx.db
      .query("platformConfig")
      .withIndex("by_key", (q) => q.eq("key", "relay_servers"))
      .unique();
    if (config?.value) {
      try {
        const relays = JSON.parse(config.value);
        if (Array.isArray(relays) && relays.length > 0) {
          defaultRelayUrl = relays[0].httpUrl || undefined;
          platformRelayPassword = relays[0].password || undefined;
        }
      } catch { /* ignore */ }
    }

    const existing = await ctx.db
      .query("userSettings")
      .withIndex("by_userId", (q) => q.eq("userId", session.user._id))
      .first();
    if (!existing) {
      await ctx.db.insert("userSettings", {
        userId: session.user._id,
        forceRelay: false,
        relayUrl: defaultRelayUrl,
        relayPassword: randomHex(24),
      });
      return { ok: true, repaired: true, reason: "seeded missing settings" };
    }

    if (
      existing.relayPassword &&
      existing.relayPassword !== platformRelayPassword &&
      (!defaultRelayUrl || existing.relayUrl === defaultRelayUrl)
    ) {
      return { ok: true, repaired: false, reason: "already in sync" };
    }

    const patch: Record<string, unknown> = {};
    if (!existing.relayPassword || existing.relayPassword === platformRelayPassword) {
      patch.relayPassword = randomHex(24);
    }
    if (defaultRelayUrl && existing.relayUrl !== defaultRelayUrl) {
      patch.relayUrl = defaultRelayUrl;
    }
    await ctx.db.patch(existing._id, patch);
    return { ok: true, repaired: true, reason: "synced to platform default" };
  },
});

/**
 * Validate a relay password for a relay action. Called by relay servers.
 *
 * action=register:
 *   - password must belong to the same signed-in user as the agent token.
 *   - if the device row already exists, it must belong to that user.
 * action=proxy:
 *   - password owner must own the target device row.
 * empty/legacy action:
 *   - only returns the password owner. Kept for bus/admin compatibility.
 */
export const validateRelayPassword = internalQuery({
  args: {
    password: v.string(),
    deviceId: v.optional(v.string()),
    action: v.optional(v.string()),
    tokenHash: v.optional(v.string()),
  },
  // Return shape (audit §3, 2026-07-19):
  //   { ok: true,  userId, plan?, isPaid? }                — allowed
  //   { ok: false, reason: "bad_password" }                — no userSettings row matches
  //   { ok: false, reason: "dead_token" }                  — the session token is missing/expired/foreign;
  //                                                          the password itself may still be correct
  //   { ok: false, reason: "device_mismatch" }             — password owner is not the deviceId owner
  // Previously EVERY failure returned bare `null`, which merged bad-password
  // and dead-token into one wire-string ("invalid relay credentials (password
  // or session token)") and misrouted the desktop's recovery into a password
  // refetch that could not possibly help. The extra reason field is the whole
  // point of this API change — the client uses it to pick the right remedy.
  handler: async (ctx, args) => {
    if (!args.password) return { ok: false, reason: "bad_password" } as const;
    const match = await ctx.db
      .query("userSettings")
      .withIndex("by_relayPassword", (q) => q.eq("relayPassword", args.password))
      .first();
    if (!match) return { ok: false, reason: "bad_password" } as const;
    const action = (args.action || "").trim().toLowerCase();
    const deviceId = (args.deviceId || "").trim();

    if (action === "register") {
      if (!args.tokenHash) return { ok: false, reason: "dead_token" } as const;
      const session = await validateSessionInternal(ctx, args.tokenHash);
      if (!session || session.user._id !== match.userId) {
        return { ok: false, reason: "dead_token" } as const;
      }
      if (deviceId) {
        const device = await ctx.db
          .query("devices")
          .withIndex("by_deviceId", (q) => q.eq("deviceId", deviceId))
          .first();
        if (device && device.userId !== match.userId) {
          return { ok: false, reason: "device_mismatch" } as const;
        }
        // Phone mesh nodes have no devices row, so bind them to their meshNodes
        // owner instead — otherwise anyone could register (and intercept the
        // DERP frame stream for) another user's phone deviceId.
        if (!device) {
          const node = await ctx.db
            .query("meshNodes")
            .withIndex("by_device", (q) => q.eq("deviceId", deviceId))
            .first();
          if (node && node.userId !== match.userId) {
            return { ok: false, reason: "device_mismatch" } as const;
          }
        }
      }
      return {
        ok: true as const,
        userId: match.userId,
        ...(await relayEntitlementForUser(ctx, match.userId)),
      };
    }

    if (action === "proxy") {
      if (!deviceId) return { ok: false, reason: "device_mismatch" } as const;
      const device = await ctx.db
        .query("devices")
        .withIndex("by_deviceId", (q) => q.eq("deviceId", deviceId))
        .first();
      if (!device || device.userId !== match.userId) {
        return { ok: false, reason: "device_mismatch" } as const;
      }
      return {
        ok: true as const,
        userId: match.userId,
        ...(await relayEntitlementForUser(ctx, match.userId)),
      };
    }

    return {
      ok: true as const,
      userId: match.userId,
      ...(await relayEntitlementForUser(ctx, match.userId)),
    };
  },
});

/**
 * Migrate all existing users to forceRelay: false.
 * Run once: npx convex run userSettings:migrateForceRelayOff
 */
export const migrateForceRelayOff = internalMutation({
  args: {},
  handler: async (ctx) => {
    const allSettings = await ctx.db.query("userSettings").collect();
    let updated = 0;
    for (const settings of allSettings) {
      if (settings.forceRelay === true || settings.forceRelay === undefined) {
        await ctx.db.patch(settings._id, { forceRelay: false });
        updated++;
      }
    }
    return { updated, total: allSettings.length };
  },
});
