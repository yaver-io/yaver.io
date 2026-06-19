// boxInit.ts — the single "is this box ready to code?" model. PURE + RN-free
// (tsx-tested), mirroring codingBackend.ts.
//
// Today the pieces that make a box codeable live in five different screens with
// five different status shapes: agent health (settings), runner auth
// (runnerAuthStatus), git/CI creds (machineOnboardingStatus), the runner
// credential mirror (managedCloudFlow), and toolchain sync. There is no single
// answer to "can I run a coding task on box X right now?".
//
// This module normalizes those raw agent reports into ONE BoxReadiness object: a
// short checklist (agent / opencode / glm / claude / codex / git) where every entry carries a
// status AND the remediation the UI can trigger. The RN layer (boxInitStore.ts)
// gathers the raw inputs over QUIC and drives the suggested actions; the screen
// just renders checks and wires buttons to actions. All policy lives here so it
// stays auditable and testable on any host.
//
// "A box" is any compute target: the local phone (sandbox), a paired LAN
// machine, a managed Yaver Cloud box, or a BYO Hetzner box. The local phone is
// handled specially (no remote agent, no git mirror — its claude capability is
// the mirrored subscription token), so the same checklist describes every
// target the user can code on.

/** Runner families the checklist cares about. */
export type RunnerKind = "claude" | "codex" | "opencode" | "glm";

/** Subset of the agent's RunnerAuthStatusRow we need. The store maps the full
 *  quic shape (which uses ids like "claude" | "claude-code" | "codex" |
 *  "opencode") down to this. */
export interface RunnerReadinessInput {
  /** Raw runner id from the agent, e.g. "claude", "claude-code", "codex". */
  id: string;
  installed: boolean;
  ready: boolean;
  authConfigured: boolean;
  version?: string;
}

/** Subset of the agent's MachineOnboardingProviderStatus (git/CI providers). */
export interface ProviderReadinessInput {
  /** "github" | "gitlab" | "openai" | … */
  id: string;
  ready: boolean;
  configured: boolean;
}

export interface BoxReadinessInput {
  /** Agent reachable + serving (false ⇒ nothing else matters yet). */
  agentOnline: boolean;
  agentVersion?: string;
  /** True when this box is the local phone: no remote agent, no git mirror,
   *  claude capability comes from the mirrored subscription token. */
  isLocalDevice?: boolean;
  /** Local-device only: a Claude Max/Pro subscription token is mirrored on this
   *  phone (see claudeSubscription.ts). Ignored for remote boxes. */
  claudeSubscription?: boolean;
  runners: RunnerReadinessInput[];
  providers: ProviderReadinessInput[];
}

export type CheckStatus =
  | "ok" // configured and ready
  | "warn" // installed/partial but needs a quick fix (e.g. not authed)
  | "missing" // not present at all
  | "n-a"; // not applicable to this kind of box

/** Remediation the UI can trigger for a check. The store maps each to a concrete
 *  quic call (mirror / setup / onboarding-apply). "none" = nothing to do. */
export type BoxActionId =
  | "none"
  | "wait_agent"
  | "mirror_claude"
  | "setup_claude"
  | "setup_codex"
  | "setup_opencode"
  | "setup_glm"
  | "configure_git_github"
  | "configure_git_gitlab";

export type CheckKey = "agent" | "opencode" | "glm" | "claude" | "codex" | "git_github" | "git_gitlab";

export interface BoxCheck {
  key: CheckKey;
  label: string;
  status: CheckStatus;
  detail: string;
  /** Suggested fix; "none" when status is "ok" or "n-a". */
  action: BoxActionId;
}

/** not-ready: can't code at all (agent offline, or no coding runner ready).
 *  partial: can code, but at least one check still wants attention.
 *  ready: everything that applies is green. */
export type OverallReadiness = "not-ready" | "partial" | "ready";

export interface BoxReadiness {
  overall: OverallReadiness;
  checks: BoxCheck[];
  /** Checks that need user action (status is "warn" or "missing"). */
  pending: BoxCheck[];
}

function isClaude(id: string): boolean {
  return id.toLowerCase().includes("claude");
}

function isCodex(id: string): boolean {
  return id.toLowerCase().includes("codex");
}

function isOpenCode(id: string): boolean {
  return id.toLowerCase().includes("opencode");
}

function isGLM(id: string): boolean {
  const normalized = id.toLowerCase();
  return normalized === "glm" || normalized.includes("z.ai") || normalized.includes("zai");
}

function findRunner(runners: RunnerReadinessInput[], pred: (id: string) => boolean): RunnerReadinessInput | undefined {
  return runners.find((r) => pred(r.id));
}

/** Claude readiness. On the local phone, "claude" means the mirrored
 *  subscription token; on a remote box it means the claude/claude-code CLI is
 *  installed and authenticated. */
function claudeCheck(input: BoxReadinessInput): BoxCheck {
  if (input.isLocalDevice) {
    if (input.claudeSubscription) {
      return { key: "claude", label: "Claude (your plan)", status: "ok", detail: "subscription token mirrored", action: "none" };
    }
    return {
      key: "claude",
      label: "Claude (your plan)",
      status: "missing",
      detail: "no subscription token on this phone",
      action: "mirror_claude",
    };
  }
  const r = findRunner(input.runners, isClaude);
  if (!r || !r.installed) {
    return { key: "claude", label: "Claude Code", status: "missing", detail: "not installed", action: "setup_claude" };
  }
  if (r.authConfigured && r.ready) {
    return { key: "claude", label: "Claude Code", status: "ok", detail: r.version ? `ready · ${r.version}` : "installed + authed", action: "none" };
  }
  // Installed but not authed: sign in on the box (browser OAuth) — a
  // target-local action that always works. (A faster mirror-from-desktop path
  // exists for managed-cloud post-purchase; see managedCloudFlow.ts.)
  return {
    key: "claude",
    label: "Claude Code",
    status: "warn",
    detail: "installed, not signed in",
    action: "setup_claude",
  };
}

function opencodeCheck(input: BoxReadinessInput): BoxCheck {
  if (input.isLocalDevice) {
    return { key: "opencode", label: "OpenCode", status: "n-a", detail: "runs on a paired box", action: "none" };
  }
  const r = findRunner(input.runners, isOpenCode);
  if (!r || !r.installed) {
    return { key: "opencode", label: "OpenCode", status: "missing", detail: "not installed", action: "setup_opencode" };
  }
  if (r.authConfigured && r.ready) {
    return { key: "opencode", label: "OpenCode", status: "ok", detail: r.version ? `ready · ${r.version}` : "installed + configured", action: "none" };
  }
  return { key: "opencode", label: "OpenCode", status: "warn", detail: "installed, provider not configured", action: "setup_opencode" };
}

function glmCheck(input: BoxReadinessInput): BoxCheck {
  if (input.isLocalDevice) {
    return { key: "glm", label: "GLM (z.ai)", status: "n-a", detail: "runs on a paired box", action: "none" };
  }
  const r = findRunner(input.runners, isGLM);
  if (!r || !r.installed) {
    return { key: "glm", label: "GLM (z.ai)", status: "missing", detail: "not configured", action: "setup_glm" };
  }
  if (r.authConfigured && r.ready) {
    return { key: "glm", label: "GLM (z.ai)", status: "ok", detail: r.version ? `ready · ${r.version}` : "API key configured", action: "none" };
  }
  return { key: "glm", label: "GLM (z.ai)", status: "warn", detail: "API key not configured", action: "setup_glm" };
}

function codexCheck(input: BoxReadinessInput): BoxCheck {
  if (input.isLocalDevice) {
    // No codex CLI on the phone sandbox — claude/subscription is the path.
    return { key: "codex", label: "Codex", status: "n-a", detail: "not used on phone", action: "none" };
  }
  const r = findRunner(input.runners, isCodex);
  if (!r || !r.installed) {
    return { key: "codex", label: "Codex", status: "missing", detail: "not installed", action: "setup_codex" };
  }
  if (r.authConfigured && r.ready) {
    return { key: "codex", label: "Codex", status: "ok", detail: r.version ? `ready · ${r.version}` : "installed + authed", action: "none" };
  }
  return { key: "codex", label: "Codex", status: "warn", detail: "installed, not signed in", action: "setup_codex" };
}

function gitCheck(input: BoxReadinessInput, provider: "github" | "gitlab"): BoxCheck {
  const key: CheckKey = provider === "github" ? "git_github" : "git_gitlab";
  const label = provider === "github" ? "Git: GitHub" : "Git: GitLab";
  const action: BoxActionId = provider === "github" ? "configure_git_github" : "configure_git_gitlab";
  if (input.isLocalDevice) {
    return { key, label, status: "n-a", detail: "phone clones via a paired box", action: "none" };
  }
  const p = input.providers.find((x) => x.id === provider);
  if (p && p.configured && p.ready) {
    return { key, label, status: "ok", detail: "token configured", action: "none" };
  }
  // Git is recommended, not blocking — a box can still run a task without it.
  return { key, label, status: "missing", detail: "no token configured", action };
}

function agentCheck(input: BoxReadinessInput): BoxCheck {
  if (input.isLocalDevice) {
    return { key: "agent", label: "Local agent", status: "ok", detail: "on this phone", action: "none" };
  }
  if (input.agentOnline) {
    return { key: "agent", label: "Agent online", status: "ok", detail: input.agentVersion ? input.agentVersion : "reachable", action: "none" };
  }
  return { key: "agent", label: "Agent online", status: "missing", detail: "box not reachable", action: "wait_agent" };
}

/**
 * Compute the one-screen readiness for a box from raw agent reports.
 *
 * Order is deliberate: agent first (gates everything), then the coding runners
 * (opencode, glm, claude, codex), then git (recommended). The overall verdict:
 *   - not-ready when the agent is offline, or no coding runner is "ok"
 *     (you literally can't run a task).
 *   - partial when you CAN code but a check is still warn/missing.
 *   - ready when every applicable check is green.
 */
export function computeBoxReadiness(input: BoxReadinessInput): BoxReadiness {
  const checks: BoxCheck[] = [
    agentCheck(input),
    opencodeCheck(input),
    glmCheck(input),
    claudeCheck(input),
    codexCheck(input),
    gitCheck(input, "github"),
    gitCheck(input, "gitlab"),
  ];

  const agentOk = checks[0].status === "ok";
  const canCode = checks.some((c) => (c.key === "opencode" || c.key === "glm" || c.key === "claude" || c.key === "codex") && c.status === "ok");
  const pending = checks.filter((c) => c.status === "warn" || c.status === "missing");

  let overall: OverallReadiness;
  if (!agentOk || !canCode) {
    overall = "not-ready";
  } else if (pending.length > 0) {
    overall = "partial";
  } else {
    overall = "ready";
  }

  return { overall, checks, pending };
}

/** One-line human summary for a collapsed row, e.g. "ready" or "partial — 2 to fix". */
export function readinessSummary(r: BoxReadiness): string {
  switch (r.overall) {
    case "ready":
      return "ready";
    case "partial":
      return `partial — ${r.pending.length} to fix`;
    case "not-ready":
      return r.checks[0].status !== "ok" ? "agent offline" : "not ready";
  }
}
