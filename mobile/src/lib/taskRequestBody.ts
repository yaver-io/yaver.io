import type { ClientSessionSettings } from "./appVersion";

export type SendTaskRequestBodyArgs = {
  title: string;
  description: string;
  model?: string;
  reasoningEffort?: "none" | "low" | "medium" | "high" | "xhigh" | "max" | "ultra";
  runner?: string;
  customCommand?: string;
  speechContext?: Record<string, unknown> | undefined;
  images?: unknown[];
  workDir?: string;
  projectName?: string;
  projectDir?: string;
  mcpServers?: string[];
  mode?: string;
  video?: { enabled?: boolean; source?: "browser" | "sim-ios" | "sim-android" | "phone" };
  codeMode?: boolean;
  allowLocalFallback?: boolean;
  /** Yaver goal-mode objective (opencode goal plugin). When set, the task
   *  runs as a persistent goal the opencode runner keeps working toward
   *  across turns (create_goal + idle auto-continue) until complete with
   *  evidence, blocked, or a safety limit. Empty = one-shot task. Only the
   *  opencode runner honors it; other runners ignore the field. Surfaces
   *  set this when the composer input is `/goal <objective>`. */
  goal?: string;
  /** Runs the task as a grounded deep question-answer (askModePreamble:
   *  file:line citations, explain-first, confirm gate) instead of a work
   *  run — the phone's "deep audit" frame. The web dashboard already sends
   *  it (agent-client.ts buildCreateTaskBody); this field closes the
   *  mobile gap so a phone-triggered ask is indistinguishable from a
   *  dashboard one. */
  askMode?: boolean;
  /** Whether the runner sees Yaver's own `yaver mcp` doorway. New tasks
   *  default false; send true only after an explicit user opt-in. */
  includeYaverMcp?: boolean;
  /** Product-owned kickoff text can drive the runner without appearing as a
   *  user-authored chat bubble. Ordinary composer sends must leave this off. */
  hideInitialPrompt?: boolean;
  sessionStartedFrom?: "tasks" | "vibing" | "new-application" | "mobile-workspace";
  startedFromSurface?: string;
  sessionSettings?: ClientSessionSettings;
  /** Bounded, redacted phone-side connection evidence for coding runners.
   *  The agent places this in hidden runner briefing, never chat display. */
  connectionDiagnostics?: string[];
};

export function buildSendTaskRequestBody(args: SendTaskRequestBodyArgs): Record<string, unknown> {
  return {
    title: args.title,
    description: args.description,
    source: args.codeMode ? "mobile-code" : "mobile",
    ...(args.model ? { model: args.model } : {}),
    ...(args.reasoningEffort ? { reasoningEffort: args.reasoningEffort } : {}),
    ...(args.runner ? { runner: args.runner } : {}),
    ...(args.mode ? { mode: args.mode } : {}),
    ...(args.customCommand ? { customCommand: args.customCommand } : {}),
    ...(args.speechContext ? { speechContext: args.speechContext } : {}),
    ...(args.images?.length ? { images: args.images } : {}),
    ...(args.workDir ? { workDir: args.workDir } : {}),
    ...(args.projectName ? { projectName: args.projectName } : {}),
    ...(args.projectDir ? { projectDir: args.projectDir } : {}),
    ...(args.mcpServers?.length ? { mcpServers: args.mcpServers } : {}),
    ...(args.video?.enabled ? { videoEnabled: true } : {}),
    ...(args.video?.source ? { videoSource: args.video.source } : {}),
    ...(args.allowLocalFallback ? { allowLocalFallback: true } : {}),
    ...(args.goal ? { goal: args.goal } : {}),
    ...(args.askMode ? { askMode: true } : {}),
    ...(typeof args.includeYaverMcp === "boolean" ? { includeYaverMcp: args.includeYaverMcp } : {}),
    ...(args.hideInitialPrompt ? { hideInitialPrompt: true } : {}),
    ...(args.sessionStartedFrom ? { sessionStartedFrom: args.sessionStartedFrom } : {}),
    ...(args.startedFromSurface ? { startedFromSurface: args.startedFromSurface } : {}),
    ...(args.sessionSettings ? { sessionSettings: args.sessionSettings } : {}),
    ...(args.connectionDiagnostics?.length ? { connectionDiagnostics: args.connectionDiagnostics } : {}),
  };
}
