/**
 * Provider-neutral deployment planning for chat/tasks.
 *
 * Planning is deliberately pure: it does not read credentials, call a
 * provider, mutate Git, or imply that a build/render happened. Execution is
 * a later, separately approved operation. Remote-box targets remain valid and
 * are never silently replaced by a phone-local target.
 */

export type DeployProvider =
  | "cloudflare-pages"
  | "cloudflare-workers"
  | "convex"
  | "github-actions"
  | "gitlab-ci"
  | "testflight"
  | "google-play-internal"
  | "npm";

export type DeployExecution = "direct-api" | "provider-ci" | "remote-box" | "cloud-workspace";

export type DeployTarget = {
  projectId: string;
  targetId: string;
  provider: DeployProvider;
  execution: DeployExecution;
  environment?: string;
  ref: string;
  commit?: string;
  workflow?: string;
  deviceId?: string;
  workspaceId?: string;
};

export type DeployPlan = {
  ok: true;
  operation: "deploy.plan";
  projectId: string;
  targetId: string;
  provider: DeployProvider;
  execution: DeployExecution;
  ref: string;
  environment: string;
  idempotencyKey: string;
  requiresConfirmation: true;
  requiresRemoteRuntime: boolean;
  costRisk: "none-known" | "provider-usage" | "ci-minutes" | "workspace-runtime";
  route: { method: "POST"; path: "/deploy/confirm" };
};

export type DeployPlanFailure = {
  ok: false;
  code:
    | "deploy_project_missing"
    | "deploy_target_missing"
    | "deploy_ref_missing"
    | "deploy_execution_missing"
    | "deploy_workflow_missing"
    | "deploy_device_missing"
    | "deploy_workspace_missing"
    | "deploy_phone_runtime_unsupported";
  message: string;
  route: { method: "GET" | "POST"; path: string };
};

/** The only payload an execution adapter may receive from chat/tasks. */
export type DeployRequest = {
  operation: "deploy.run";
  idempotencyKey: string;
  projectId: string;
  targetId: string;
  provider: DeployProvider;
  execution: DeployExecution;
  ref: string;
  environment: string;
  confirmationId: string;
};

export type DeployStatus = {
  operation: "deploy.status";
  idempotencyKey: string;
  state: "queued" | "running" | "succeeded" | "failed" | "cancelled" | "unknown";
  provider: DeployProvider;
  targetId: string;
  url?: string;
  version?: string;
  message?: string;
};

/** Convert a confirmed plan into an adapter-safe request. No credentials,
 * source contents, absolute paths, or provider response blobs are accepted. */
export function makeDeployRequest(plan: DeployPlan, confirmationId: string): DeployRequest | DeployPlanFailure {
  const confirmation = clean(confirmationId);
  if (!confirmation) {
    return { ok: false, code: "deploy_execution_missing", message: "Confirm this deploy before running it.", route: { method: "POST", path: "/deploy/confirm" } };
  }
  return {
    operation: "deploy.run",
    idempotencyKey: plan.idempotencyKey,
    projectId: plan.projectId,
    targetId: plan.targetId,
    provider: plan.provider,
    execution: plan.execution,
    ref: plan.ref,
    environment: plan.environment,
    confirmationId: confirmation,
  };
}

const statusMap: Record<string, DeployStatus["state"]> = {
  queued: "queued",
  pending: "queued",
  waiting: "queued",
  running: "running",
  in_progress: "running",
  success: "succeeded",
  succeeded: "succeeded",
  completed: "succeeded",
  failure: "failed",
  failed: "failed",
  cancelled: "cancelled",
  canceled: "cancelled",
};

/** Normalize an adapter response before it reaches task events or UI. */
export function normalizeDeployStatus(
  plan: DeployPlan,
  raw: { state?: unknown; status?: unknown; url?: unknown; version?: unknown; message?: unknown },
): DeployStatus {
  const rawState = clean(typeof raw.state === "string" ? raw.state : typeof raw.status === "string" ? raw.status : undefined).toLowerCase();
  const state = statusMap[rawState] ?? "unknown";
  return {
    operation: "deploy.status",
    idempotencyKey: plan.idempotencyKey,
    state,
    provider: plan.provider,
    targetId: plan.targetId,
    ...(typeof raw.url === "string" && /^https:\/\//i.test(raw.url) ? { url: raw.url } : {}),
    ...(typeof raw.version === "string" ? { version: raw.version.slice(0, 200) } : {}),
    ...(typeof raw.message === "string" ? { message: raw.message.slice(0, 1000) } : {}),
  };
}

function clean(value: string | undefined): string {
  return value?.trim() ?? "";
}

function stableKey(target: DeployTarget, environment: string): string {
  return [
    target.projectId,
    target.targetId,
    target.provider,
    target.execution,
    target.ref,
    clean(target.commit) || "working-tree",
    environment,
  ].join(":");
}

export function planDeploy(target: DeployTarget): DeployPlan | DeployPlanFailure {
  const projectId = clean(target.projectId);
  if (!projectId) {
    return { ok: false, code: "deploy_project_missing", message: "Choose a project before planning a deploy.", route: { method: "GET", path: "/projects" } };
  }
  const ref = clean(target.ref);
  if (!ref) {
    return { ok: false, code: "deploy_ref_missing", message: "Choose a branch, tag, or commit before planning a deploy.", route: { method: "GET", path: "/git/status" } };
  }
  const targetId = clean(target.targetId);
  if (!targetId) {
    return { ok: false, code: "deploy_target_missing", message: "Choose what to deploy before planning a deploy.", route: { method: "POST", path: "/deploy/discover" } };
  }
  if (!target.execution) {
    return { ok: false, code: "deploy_execution_missing", message: "Choose where this deploy should run: provider API, CI, or a paired machine/VPS.", route: { method: "POST", path: "/deploy/discover" } };
  }
  if ((target.execution === "provider-ci") && !clean(target.workflow)) {
    return { ok: false, code: "deploy_workflow_missing", message: "This CI deploy needs an existing workflow or pipeline.", route: { method: "POST", path: "/deploy/discover" } };
  }
  if (target.execution === "remote-box" && !clean(target.deviceId)) {
    return { ok: false, code: "deploy_device_missing", message: "Select a connected remote box for this deploy.", route: { method: "GET", path: "/devices" } };
  }
  if (target.execution === "cloud-workspace" && !clean(target.workspaceId)) {
    return { ok: false, code: "deploy_workspace_missing", message: "Select a paired machine or VPS before deploying.", route: { method: "POST", path: "/devices" } };
  }
  if (target.execution === "direct-api" && !["cloudflare-pages", "cloudflare-workers"].includes(target.provider)) {
    return { ok: false, code: "deploy_phone_runtime_unsupported", message: "This provider needs a build/runtime lane; use CI or a capable paired machine/VPS.", route: { method: "POST", path: "/deploy/discover" } };
  }

  const environment = clean(target.environment) || "production";
  const costRisk = target.execution === "provider-ci"
    ? "ci-minutes"
    : target.execution === "cloud-workspace"
      ? "workspace-runtime"
      : target.execution === "direct-api"
        ? "provider-usage"
        : "none-known";

  return {
    ok: true,
    operation: "deploy.plan",
    projectId,
    targetId,
    provider: target.provider,
    execution: target.execution,
    ref,
    environment,
    idempotencyKey: stableKey({ ...target, projectId, ref }, environment),
    requiresConfirmation: true,
    requiresRemoteRuntime: target.execution === "remote-box" || target.execution === "cloud-workspace" || target.execution === "provider-ci",
    costRisk,
    route: { method: "POST", path: "/deploy/confirm" },
  };
}
