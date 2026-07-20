import type { AgentStatus, RunnerInfo } from "./quic";

export type RunnerFetchState =
  | "idle"
  | "loading"
  | "ok"
  | "timed-out"
  | "http-error"
  | "network-error";

export type RunnerBannerKind =
  | "ok"
  | "loading"
  | "failed"
  | "authNeeded"
  | "needsConfig"
  | "notRunnable"
  | "notInstalled"
  | "blocked";

const RUNNER_BANNER_TONES: Record<RunnerBannerKind, string> = {
  ok: "#4ade80",
  loading: "#93c5fd",
  failed: "#fbbf24",
  authNeeded: "#fbbf24",
  needsConfig: "#fbbf24",
  notRunnable: "#fbbf24",
  notInstalled: "#f87171",
  blocked: "#fbbf24",
};

export type RunnerBannerState = {
  text: string;
  tone: string;
  kind: RunnerBannerKind;
  runnerId?: string;
};

function normalizeTaskRunnerId(id?: string | null): string {
  const raw = String(id || "").trim().toLowerCase();
  return raw === "claude-code" ? "claude" : raw;
}

function displayRunnerLabel(id?: string | null): string {
  const key = normalizeTaskRunnerId(id);
  if (key === "claude") return "Claude Code";
  if (key === "codex") return "OpenAI Codex";
  if (key === "opencode") return "OpenCode";
  return String(id || "Agent");
}

export function deriveRunnerBannerState(
  runners: RunnerInfo[],
  agentStatus: AgentStatus | null,
  selectedRunnerId?: string,
  fetchState: RunnerFetchState = "idle",
): RunnerBannerState | null {
  const wantId = normalizeTaskRunnerId(selectedRunnerId);
  const selectedRow = wantId && wantId !== "custom"
    ? runners.find((r) => normalizeTaskRunnerId(r.id) === wantId)
    : null;
  const selectedLabel = selectedRow?.name || displayRunnerLabel(wantId || agentStatus?.runner?.id || "");
  const make = (kind: RunnerBannerKind, text: string, runnerId?: string): RunnerBannerState => ({
    text,
    tone: RUNNER_BANNER_TONES[kind],
    kind,
    runnerId,
  });

  if (fetchState === "loading") {
    if (wantId && wantId !== "custom") return make("loading", `${selectedLabel} status loading`, selectedRow?.id || wantId);
    return make("loading", "Checking agents...");
  }
  if (fetchState !== "idle" && fetchState !== "ok") {
    if (wantId && wantId !== "custom") return make("failed", `${selectedLabel} status unavailable`, selectedRow?.id || wantId);
    return make("failed", "Agent status unavailable");
  }
  if (runners.length === 0 && !agentStatus) return null;

  const installed = runners.filter((runner) => runner.installed);
  const runnable = installed.filter((runner) => !runner.error);
  const authed = installed.filter((runner) => runner.authConfigured);

  if (wantId && wantId !== "custom") {
    const label = selectedLabel;
    if (!selectedRow || selectedRow.installed === false) {
      return make("notInstalled", `${label} not installed`);
    }
    if (selectedRow.authConfigured === false) {
      return make("authNeeded", `${label} needs sign-in`, selectedRow.id);
    }
    if (selectedRow.error) {
      if (wantId === "opencode") {
        return make("needsConfig", `${label} needs setup`);
      }
      return make("blocked", `${label} blocked`);
    }
    if (selectedRow.ready === false) {
      return make("blocked", `${label} not ready`);
    }
    return make(
      "ok",
      `${label} ready${agentStatus?.runningTasks ? ` · ${agentStatus.runningTasks} running` : ""}`,
    );
  }

  if (installed.length === 0) {
    return make("notInstalled", "No agents available");
  }
  if (runnable.length === 0 && authed.length === 0) {
    return make("authNeeded", "Agents available, none authenticated");
  }
  if (runnable.length === 0) {
    return make("notRunnable", "Agents available, none runnable");
  }
  const current = agentStatus?.runner;
  if (current?.installed === false) {
    return make("notInstalled", `${current.name} not installed`);
  }
  if (current?.error && !current?.authConfigured) {
    return make("authNeeded", `${current.name} needs sign-in`, current.id);
  }
  if (current?.error) {
    return make("blocked", `${current.name} blocked`);
  }
  if (current?.name) {
    if (current.authConfigured === false) {
      return make("authNeeded", `${current.name} needs sign-in`, current.id);
    }
    if (current.ready === false) {
      return make("blocked", `${current.name} not ready`);
    }
    return make(
      "ok",
      `${current.name} ready${agentStatus?.runningTasks ? ` · ${agentStatus.runningTasks} running` : ""}`,
    );
  }
  return make("ok", `${runnable.length} agent${runnable.length > 1 ? "s" : ""} ready`);
}
