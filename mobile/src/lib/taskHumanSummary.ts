import type { CommandCardModel } from "./commandEvents";

export type HumanTaskStatus = "queued" | "running" | "ready" | "review" | "completed" | "failed" | "stopped" | string;

export interface HumanTaskLike {
  title: string;
  status: HumanTaskStatus;
  output?: string[];
  resultText?: string;
  failure?: {
    title?: string;
    reason?: string;
    remedy?: string;
  };
  commitSha?: string;
  commitSubject?: string;
  diffShortstat?: string;
  progressLine?: string;
  presentationDetail?: string;
  agentVersion?: string;
  latestAgentVersion?: string;
  agentVersionDistance?: number;
}

export type HumanSummaryTone = "active" | "success" | "error" | "muted" | "warning";
export type HumanStepState = "running" | "succeeded" | "failed" | "completed" | "seen";

export interface HumanTaskStep {
  id: string;
  label: string;
  command: string;
  state: HumanStepState;
  detail?: string;
}

export interface HumanTaskSummary {
  title: string;
  detail: string;
  tone: HumanSummaryTone;
  nextAction?: string;
  facts: string[];
  steps: HumanTaskStep[];
}

const ANSI_RE = /\x1B(?:\[[0-?]*[ -/]*[@-~]|\][^\x07]*(?:\x07|\x1B\\))/g;

function cleanLine(value: string): string {
  return String(value || "")
    .replace(ANSI_RE, "")
    .replace(/^\s{0,3}#{1,6}\s+/, "")
    .replace(/^\s*[-*]\s+/, "")
    .replace(/[*_`]/g, "")
    .replace(/\s+/g, " ")
    .trim();
}

function clamp(value: string, max = 180): string {
  const text = cleanLine(value);
  if (text.length <= max) return text;
  return `${text.slice(0, max - 3).trimEnd()}...`;
}

function shortCommand(command: string): string {
  const text = cleanLine(command).replace(/^\$\s*/, "");
  return text.length > 76 ? `${text.slice(0, 73).trimEnd()}...` : text;
}

/** Turn shell syntax into a sentence a non-terminal user can scan. */
export function humanizeTaskCommand(command: string): string {
  const cmd = shortCommand(command);
  const lower = cmd.toLowerCase();
  if (/\b(?:go test|pytest|vitest|jest|cargo test|xcodebuild\b.*\btest|gradle\b.*\btest|(?:pnpm|npm|yarn|bun)\b.*\btest)\b/.test(lower)) return "Run tests";
  if (/\b(?:tsc|typecheck|type-check)\b/.test(lower)) return "Check types";
  if (/\b(?:eslint|biome check|golangci-lint|swiftlint|ruff check|\blint\b)/.test(lower)) return "Check code quality";
  if (/\b(?:go build|cargo build|xcodebuild|gradle|assemble|bundle|(?:pnpm|npm|yarn|bun)\b.*\bbuild)\b/.test(lower)) return "Build the project";
  if (/\bgit\s+(?:diff|status|show)\b/.test(lower)) return "Review changes";
  if (/\bgit\s+commit\b/.test(lower)) return "Save a commit";
  if (/\bgit\s+push\b/.test(lower)) return "Push changes";
  if (/\b(?:deploy|wrangler deploy|vercel|firebase deploy)\b/.test(lower)) return "Deploy updates";
  if (/\b(?:pnpm|npm|yarn|bun)\s+(?:install|i)\b|\b(?:brew|apt(?:-get)?|dnf)\s+install\b/.test(lower)) return "Install dependencies";
  if (/^(?:rg|grep|find|fd|ls|sed|cat|head|tail|wc)\b/.test(lower)) return "Inspect the project";
  // The exact command remains in `HumanTaskStep.command` and the folded
  // console. Unknown shell syntax is not a useful primary status sentence and
  // can be hundreds of characters long, so keep the visible fallback calm.
  return cmd ? "Work in the project" : "Run a command";
}

function commandDetail(model: CommandCardModel): string | undefined {
  if (model.status === "error" && typeof model.exitCode === "number") return `Exited with code ${model.exitCode}`;
  if (typeof model.durationMs === "number" && model.durationMs >= 1000) {
    return `Finished in ${(model.durationMs / 1000).toFixed(1)}s`;
  }
  return undefined;
}

function structuredSteps(models?: Record<string, CommandCardModel>): HumanTaskStep[] {
  return Object.values(models || {})
    .sort((a, b) => a.startedAt - b.startedAt)
    .slice(-4)
    .map((model) => ({
      id: model.id,
      label: humanizeTaskCommand(model.command),
      command: shortCommand(model.command),
      state: model.status === "ok"
        ? "succeeded"
        : model.status === "error"
          ? "failed"
          : model.status === "running"
            ? "running"
            : "completed",
      detail: commandDetail(model),
    }));
}

/** Recover action names after reopening a task, when live command events are gone. */
function persistedSteps(output: string[] = []): HumanTaskStep[] {
  const commands: string[] = [];
  const seen = new Set<string>();
  const tail = output.slice(-240);
  for (const raw of tail) {
    const line = String(raw || "").replace(ANSI_RE, "").trim();
    const markdown = line.match(/^\*\*\$\s+(.+?)\*\*$/);
    const shell = line.match(/^\$\s+(.+)$/);
    const command = shortCommand(markdown?.[1] || shell?.[1] || "");
    if (!command || seen.has(command)) continue;
    seen.add(command);
    commands.push(command);
  }
  return commands.slice(-4).map((command, index) => ({
    id: `persisted-${index}-${command}`,
    label: humanizeTaskCommand(command),
    command,
    // Output proves the command was seen, not that it exited successfully.
    state: "seen",
  }));
}

function latestStepDetail(step: HumanTaskStep | undefined, running: boolean): string {
  if (!step) return running
    ? "The runner is working. Meaningful actions will appear here as they happen."
    : "";
  if (step.state === "running") return `${step.label} is running now.`;
  if (step.state === "failed") return running
    ? `${step.label} failed. The runner is still working on the task.`
    : `${step.label} failed.`;
  if (step.state === "succeeded") return running
    ? `${step.label} succeeded. The runner is continuing.`
    : `${step.label} succeeded.`;
  if (step.state === "completed") return running
    ? `${step.label} finished. The runner is continuing.`
    : `${step.label} finished.`;
  return `${step.label} is the latest recorded action.`;
}

function joinSentences(parts: Array<string | undefined>): string {
  return parts
    .map((part) => clamp(part || ""))
    .filter(Boolean)
    .join(" ");
}

function settledDetail(task: HumanTaskLike, latest: HumanTaskStep | undefined, fallback: string): string {
  const detail = joinSentences([task.presentationDetail, latestStepDetail(latest, false)]);
  return detail || fallback;
}

function versionFact(task: HumanTaskLike): string | null {
  const current = cleanLine(task.agentVersion || "");
  if (!current) return null;
  const latest = cleanLine(task.latestAgentVersion || "");
  const distance = typeof task.agentVersionDistance === "number" ? task.agentVersionDistance : -1;
  if (latest && distance > 0) {
    return `Yaver ${current} -> ${latest} (${distance} behind)`;
  }
  return `Yaver ${current}`;
}

export function buildTaskHumanSummary(
  task: HumanTaskLike,
  commands?: Record<string, CommandCardModel>,
): HumanTaskSummary {
  const liveSteps = structuredSteps(commands);
  const steps = liveSteps.length > 0 ? liveSteps : persistedSteps(task.output);
  const latest = steps[steps.length - 1];
  const running = task.status === "running" || task.status === "queued";
  const failureReason = clamp(task.failure?.reason || task.failure?.title || "");
  const facts: string[] = [];

  if (liveSteps.length > 0) {
    const succeeded = liveSteps.filter((step) => step.state === "succeeded").length;
    const failed = liveSteps.filter((step) => step.state === "failed").length;
    const active = liveSteps.filter((step) => step.state === "running").length;
    if (succeeded > 0) facts.push(`${succeeded} command${succeeded === 1 ? "" : "s"} succeeded`);
    if (failed > 0) facts.push(`${failed} command${failed === 1 ? "" : "s"} failed`);
    if (active > 0) facts.push(`${active} running now`);
  } else if (steps.length > 0) {
    facts.push(`${steps.length} recent action${steps.length === 1 ? "" : "s"}`);
  }
  if (task.diffShortstat) facts.push(clamp(task.diffShortstat, 80));
  if (task.commitSha) facts.push(`Commit ${task.commitSha.slice(0, 8)}`);
  const version = versionFact(task);
  if (version) facts.push(version);

  if (task.failure || task.status === "failed") {
    return {
      title: task.failure?.title || "Task failed",
      detail: failureReason || settledDetail(task, latest, "The task stopped without a clear failure reason."),
      tone: "error",
      nextAction: clamp(task.failure?.remedy || ""),
      facts,
      steps,
    };
  }
  if (task.status === "stopped") {
    return {
      title: "Task stopped",
      detail: settledDetail(task, latest, "The task was stopped before it finished."),
      tone: "muted",
      facts,
      steps,
    };
  }
  if (task.status === "completed") {
    return {
      title: "Completed",
      detail: settledDetail(task, latest, "The task finished successfully."),
      tone: "success",
      facts,
      steps,
    };
  }
  if (task.status === "review") {
    return {
      title: "Ready for review",
      detail: settledDetail(task, latest, "The runner finished and the result is ready to review."),
      tone: "success",
      facts,
      steps,
    };
  }
  if (task.status === "queued") {
    return {
      title: "Waiting to start",
      detail: joinSentences([
        task.presentationDetail || "The task is queued and has not started running yet.",
        task.progressLine,
      ]),
      tone: "warning",
      nextAction: task.agentVersionDistance && task.agentVersionDistance > 0
        ? `Update the box from Yaver ${cleanLine(task.agentVersion || "")} to ${cleanLine(task.latestAgentVersion || "")}.`
        : undefined,
      facts,
      steps,
    };
  }
  return {
    title: "Work in progress",
    detail: joinSentences([
      task.presentationDetail,
      latestStepDetail(latest, running),
      task.progressLine,
    ]),
    tone: "active",
    nextAction: task.agentVersionDistance && task.agentVersionDistance > 0
      ? `Update the box from Yaver ${cleanLine(task.agentVersion || "")} to ${cleanLine(task.latestAgentVersion || "")} after this task.`
      : undefined,
    facts,
    steps,
  };
}
