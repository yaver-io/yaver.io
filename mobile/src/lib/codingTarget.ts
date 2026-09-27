// codingTarget.ts — pure execution-target contract for Tasks/Vibing.
//
// The important rule is negative: an explicit remote target must never silently
// fall back to the phone, and a phone-local target must never silently wake or
// bill a Cloud Workspace. The UI can render the returned code and route without
// inventing its own target-selection semantics.

export type CodingTargetKind = "phone-local" | "remote-box" | "cloud-workspace" | "provider-ci";

export type CodingTarget =
  | { kind: "phone-local"; checkoutId: string; branch: string }
  | { kind: "remote-box"; deviceId: string; projectId: string; branch?: string }
  | { kind: "cloud-workspace"; workspaceId: string; projectId: string; branch: string }
  | { kind: "provider-ci"; provider: "github" | "gitlab"; repository: string; workflow: string; ref: string };

export type CodingTargetBlockCode =
  | "remote_runtime_unavailable"
  | "phone_checkout_missing"
  | "cloud_workspace_confirmation_required"
  | "provider_ci_configuration_missing";

export type CodingTargetDecision =
  | { ok: true; target: CodingTarget }
  | {
      ok: false;
      code: CodingTargetBlockCode;
      /** Stable route name for the existing task/chat surface. */
      route: "devices" | "clone-repository" | "cloud-workspace" | "configure-ci";
      message: string;
    };

export interface CodingTargetReadiness {
  remoteConnected?: boolean;
  phoneCheckoutReady?: boolean;
  cloudWorkspaceConfirmed?: boolean;
  providerCiConfigured?: boolean;
}

/** Resolve an already-selected target without fallback or side effects. */
export function resolveCodingTarget(
  target: CodingTarget,
  readiness: CodingTargetReadiness = {},
): CodingTargetDecision {
  switch (target.kind) {
    case "remote-box":
      return readiness.remoteConnected === false
        ? {
            ok: false,
            code: "remote_runtime_unavailable",
            route: "devices",
            message: "The selected remote machine is unavailable. Connect or select a different machine; this task will not run on the phone automatically.",
          }
        : { ok: true, target };
    case "phone-local":
      return readiness.phoneCheckoutReady === false
        ? {
            ok: false,
            code: "phone_checkout_missing",
            route: "clone-repository",
            message: "This iPhone has no ready checkout for the selected project. Clone or select a local repository first.",
          }
        : { ok: true, target };
    case "cloud-workspace":
      return readiness.cloudWorkspaceConfirmed === false
        ? {
            ok: false,
            code: "cloud_workspace_confirmation_required",
            route: "cloud-workspace",
            message: "This legacy hosted target is no longer offered. Choose your own machine or VPS before starting the task.",
          }
        : { ok: true, target };
    case "provider-ci":
      return readiness.providerCiConfigured === false
        ? {
            ok: false,
            code: "provider_ci_configuration_missing",
            route: "configure-ci",
            message: "The selected CI workflow is not configured or cannot be dispatched from this account.",
          }
        : { ok: true, target };
  }
}

export function targetNeedsRemoteRuntime(target: CodingTarget): boolean {
  return target.kind === "remote-box" || target.kind === "cloud-workspace" || target.kind === "provider-ci";
}
