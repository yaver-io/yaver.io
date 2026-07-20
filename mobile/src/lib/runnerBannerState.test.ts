import type { AgentStatus, RunnerInfo } from "./quic";
import { deriveRunnerBannerState } from "./runnerBannerState";

let failures = 0;

function check(name: string, cond: boolean, detail?: string) {
  if (cond) {
    console.log(`ok   ${name}`);
    return;
  }
  failures++;
  console.error(`FAIL ${name}${detail ? ` — ${detail}` : ""}`);
}

const baseStatus: AgentStatus = {
  runner: {
    id: "claude",
    name: "Claude Code",
    installed: true,
    ready: true,
    authConfigured: true,
    models: [],
  },
  runningTasks: 0,
  status: "ok",
};

const claudeReady: RunnerInfo = {
  id: "claude",
  name: "Claude Code",
  command: "claude",
  installed: true,
  ready: true,
  authConfigured: true,
  models: [],
};

console.log("runnerBannerState");

check(
  "loading beats empty-list false negative",
  deriveRunnerBannerState([], null, "claude", "loading")?.text === "Claude Code status loading",
);

check(
  "failed beats stale no-runner fact",
  deriveRunnerBannerState([], null, "claude", "network-error")?.text === "Claude Code status unavailable",
);

check(
  "selected runner auth needed is explicit",
  deriveRunnerBannerState([{ ...claudeReady, authConfigured: false, ready: false }], baseStatus, "claude", "ok")?.text === "Claude Code needs sign-in",
);

check(
  "loaded empty list says no agents available",
  deriveRunnerBannerState([], { ...baseStatus, runner: undefined as any }, "", "ok")?.text === "No agents available",
);

check(
  "selected runner ready stays specific",
  deriveRunnerBannerState([claudeReady], baseStatus, "claude", "ok")?.text === "Claude Code ready",
);

process.exit(failures);
