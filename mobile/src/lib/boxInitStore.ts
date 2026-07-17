// boxInitStore.ts — RN-coupled I/O shell around boxInit.ts.
//
// boxInit.ts is pure: it turns raw agent reports into a BoxReadiness checklist.
// This module does the I/O it can't: gather those raw reports over QUIC, run a
// single remediation (runBoxAction), and sequence all pending fixes into one
// progress stream (runBoxInit) — the same shape managedCloudFlow.ts uses so the
// screen can render a checklist with spinners/checks.
//
// Every remediation here is a TARGET-LOCAL call against
// connectionManager.clientFor(deviceId): runnerAuthSetup (install + browser
// OAuth on the box) and machineOnboardingApply (git/CI creds). No mirror is
// assumed — the managed-cloud post-purchase accelerator stays in
// managedCloudFlow.ts where the source box is known.

import { connectionManager } from "./connectionManager";
import { hasSubscription } from "./subscriptionStore";
import {
  computeBoxReadiness,
  type BoxActionId,
  type BoxCheck,
  type BoxReadiness,
  type ProviderReadinessInput,
  type RunnerReadinessInput,
} from "./boxInit";

export interface LoadReadinessOpts {
  /** When true, evaluate the local phone (sandbox) instead of a remote box. */
  isLocalDevice?: boolean;
}

/** Gather raw agent reports for a box and compute its readiness. Never throws —
 *  an unreachable box comes back as agent-offline (not an error). */
export async function loadBoxReadiness(deviceId: string, opts: LoadReadinessOpts = {}): Promise<BoxReadiness> {
  if (opts.isLocalDevice) {
    const sub = await hasSubscription().catch(() => false);
    return computeBoxReadiness({
      agentOnline: true,
      isLocalDevice: true,
      claudeSubscription: sub,
      runners: [],
      providers: [],
    });
  }

  const client = connectionManager.clientFor(deviceId);
  const [runnersRaw, providersRaw, info] = await Promise.all([
    client.runnerAuthStatusOrNull().catch(() => null), // null ⇒ unreachable
    client.machineOnboardingStatus().catch(() => []),
    client.getInfo().catch(() => null),
  ]);

  const agentOnline = runnersRaw !== null;
  const runners: RunnerReadinessInput[] = (runnersRaw ?? []).map((r) => ({
    id: r.id,
    installed: r.installed,
    ready: r.ready,
    authConfigured: r.authConfigured,
    version: r.version,
  }));
  const providers: ProviderReadinessInput[] = (providersRaw ?? []).map((p) => ({
    id: String(p.id),
    ready: p.ready,
    configured: p.configured,
  }));

  return computeBoxReadiness({
    agentOnline,
    agentVersion: info?.version,
    runners,
    providers,
  });
}

/** Tokens the git/provider remediations may need. Claude/Codex runner auth is plan OAuth, not API keys. */
export interface BoxActionParams {
  githubToken?: string;
  gitlabToken?: string;
  gitlabHost?: string;
  /** Legacy/provider fields retained for non-plan backends; not used for Claude/Codex plan OAuth. */
  anthropicApiKey?: string;
  openaiApiKey?: string;
  glmApiKey?: string;
}

export interface BoxActionResult {
  ok: boolean;
  detail?: string;
  error?: string;
}

/**
 * Execute one remediation against a box. Maps a BoxActionId from the checklist
 * to the concrete quic call. Returns ok=false with an error string rather than
 * throwing so the UI can render per-step failures inline.
 */
export async function runBoxAction(
  deviceId: string,
  action: BoxActionId,
  params: BoxActionParams = {},
): Promise<BoxActionResult> {
  const client = connectionManager.clientFor(deviceId);
  try {
    switch (action) {
      case "none":
        return { ok: true };

      case "wait_agent": {
        // Re-probe reachability; readiness reload happens on the caller side.
        const status = await client.runnerAuthStatusOrNull();
        return status === null
          ? { ok: false, error: "box still unreachable" }
          : { ok: true, detail: "agent reachable" };
      }

      case "setup_claude":
      case "mirror_claude": {
        // Both resolve to a target-local install + auth. (mirror is offered for
        // the local phone, where importMirrored seeds the token out-of-band;
        // for a remote box we install + browser-auth the CLI here.)
        const r = await client.runnerAuthSetup({
          runner: "claude",
          installIfMissing: true,
          allowInstallOnly: true,
          setupMcp: true,
        });
        return resultFromSetup(r);
      }

      case "setup_codex": {
        const r = await client.runnerAuthSetup({
          runner: "codex",
          installIfMissing: true,
          allowInstallOnly: true,
          setupMcp: true,
        });
        return resultFromSetup(r);
      }

      case "setup_opencode": {
        const r = await client.runnerAuthSetup({
          runner: "opencode",
          installIfMissing: true,
          allowInstallOnly: true,
          setupMcp: true,
          glmApiKey: params.glmApiKey,
        });
        return resultFromSetup(r);
      }

      case "configure_git_github": {
        if (!params.githubToken) return { ok: false, error: "a GitHub token is required" };
        const r = await client.machineOnboardingApply({
          githubToken: params.githubToken,
          applyClone: true,
          applyCiToken: true,
        });
        return r.ok ? { ok: true, detail: `applied: ${r.applied.join(", ") || "github"}` } : { ok: false, error: r.error };
      }

      case "configure_git_gitlab": {
        if (!params.gitlabToken) return { ok: false, error: "a GitLab token is required" };
        const r = await client.machineOnboardingApply({
          gitlabToken: params.gitlabToken,
          gitlabHost: params.gitlabHost,
          applyClone: true,
          applyCiToken: true,
        });
        return r.ok ? { ok: true, detail: `applied: ${r.applied.join(", ") || "gitlab"}` } : { ok: false, error: r.error };
      }
    }
  } catch (e) {
    return { ok: false, error: e instanceof Error ? e.message : String(e) };
  }
}

function resultFromSetup(r: {
  ok: boolean;
  ready: boolean;
  authConfigured: boolean;
  installed: boolean;
  detail?: string;
  warning?: string;
  error?: string;
}): BoxActionResult {
  if (r.error) return { ok: false, error: r.error };
  if (r.ready && r.authConfigured) return { ok: true, detail: "installed + signed in" };
  if (r.installed && !r.authConfigured) {
    // Non-terminal: CLI is on the box but still needs the user to finish the
    // browser handshake. Surface it as a soft "needs sign-in" not a failure.
    return { ok: true, detail: r.detail || r.warning || "installed — finish sign-in on the box" };
  }
  return { ok: r.ok, detail: r.detail, error: r.warning };
}

// ---- orchestrator ---------------------------------------------------------

export interface BoxInitProgress {
  /** The check currently being remediated, or "load"/"done". */
  step: "load" | BoxActionId | "done";
  message: string;
  /** Latest readiness snapshot, refreshed after each action. */
  readiness?: BoxReadiness;
  done?: boolean;
}

export interface RunBoxInitOpts {
  deviceId: string;
  isLocalDevice?: boolean;
  /** Tokens for git/provider remediations; Claude/Codex runner setup uses plan OAuth. */
  params?: BoxActionParams;
  /** Skip git checks even if missing (they're non-blocking). Default false. */
  skipGit?: boolean;
  onProgress: (p: BoxInitProgress) => void;
  signal?: AbortSignal;
}

/**
 * Bring a box to "ready" by running each pending remediation in checklist order,
 * reloading readiness after every step. Git steps are skipped when their token
 * isn't supplied (or skipGit is set) since git is non-blocking. Returns the
 * final readiness.
 */
export async function runBoxInit(opts: RunBoxInitOpts): Promise<BoxReadiness> {
  const { deviceId, isLocalDevice, params = {}, skipGit = false, onProgress, signal } = opts;

  abortGuard(signal);
  onProgress({ step: "load", message: "checking what this box needs…" });
  let readiness = await loadBoxReadiness(deviceId, { isLocalDevice });
  onProgress({ step: "load", message: "loaded box status", readiness });

  for (const check of readiness.pending) {
    abortGuard(signal);
    if (skipGit && isGitCheck(check)) continue;
    if (isGitCheck(check) && !gitTokenFor(check, params)) continue; // can't fix without a token

    onProgress({ step: check.action, message: `fixing: ${check.label}…`, readiness });
    const res = await runBoxAction(deviceId, check.action, params);
    if (!res.ok) {
      onProgress({ step: check.action, message: `${check.label}: ${res.error ?? "failed"}`, readiness });
      // keep going — one failed step shouldn't abort the rest
    }
    readiness = await loadBoxReadiness(deviceId, { isLocalDevice });
    onProgress({ step: check.action, message: `${check.label} done`, readiness });
  }

  onProgress({
    step: "done",
    message:
      readiness.overall === "ready"
        ? "box is ready to code"
        : `box is ${readiness.overall.replace("-", " ")} — ${readiness.pending.length} item(s) still need attention`,
    readiness,
    done: true,
  });
  return readiness;
}

function isGitCheck(c: BoxCheck): boolean {
  return c.key === "git_github" || c.key === "git_gitlab";
}

function gitTokenFor(c: BoxCheck, p: BoxActionParams): boolean {
  return c.key === "git_github" ? !!p.githubToken : !!p.gitlabToken;
}

function abortGuard(signal?: AbortSignal): void {
  if (signal?.aborted) throw new Error("aborted");
}
