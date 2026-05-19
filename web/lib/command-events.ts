// Mirror of desktop/agent/command_events.go (schema v1). Keep in
// lockstep with mobile/src/lib/commandEvents.ts. These events ride the
// task SSE stream P2P only — never Convex.

export const COMMAND_EVENT_SCHEMA = 1;

export interface CommandStartEvent {
  type: "command_start";
  schema: number;
  id: string;
  command: string;
  args: string[];
  cwd: string;
  runner: string;
  ts: number;
}

export interface CommandOutputEvent {
  type: "command_output";
  schema: number;
  id: string;
  stream: "stdout" | "stderr";
  chunk: string;
  seq: number;
  ts: number;
}

export interface CommandEndEvent {
  type: "command_end";
  schema: number;
  id: string;
  exitCode?: number;
  durationMs?: number;
  truncated: boolean;
  ts: number;
}

export type CommandEvent =
  | CommandStartEvent
  | CommandOutputEvent
  | CommandEndEvent;

export interface CommandCardModel {
  id: string;
  command: string;
  args: string[];
  cwd: string;
  runner: string;
  startedAt: number;
  stdout: string;
  stderr: string;
  status: "running" | "done" | "ok" | "error";
  exitCode?: number;
  durationMs?: number;
  truncated: boolean;
}

export function isCommandEvent(e: unknown): e is CommandEvent {
  if (!e || typeof e !== "object") return false;
  const t = (e as { type?: unknown }).type;
  return (
    t === "command_start" ||
    t === "command_output" ||
    t === "command_end"
  );
}

export function reduceCommandEvent(
  prev: Record<string, CommandCardModel>,
  e: CommandEvent,
): Record<string, CommandCardModel> {
  const next = { ...prev };
  if (e.type === "command_start") {
    next[e.id] = {
      id: e.id,
      command: e.command,
      args: e.args ?? [],
      cwd: e.cwd ?? "",
      runner: e.runner ?? "",
      startedAt: e.ts,
      stdout: "",
      stderr: "",
      status: "running",
      truncated: false,
    };
    return next;
  }
  const cur = next[e.id];
  if (!cur) return prev;
  if (e.type === "command_output") {
    next[e.id] = {
      ...cur,
      [e.stream]: (cur[e.stream] as string) + e.chunk,
    };
    return next;
  }
  let status: CommandCardModel["status"] = "done";
  if (typeof e.exitCode === "number") {
    status = e.exitCode === 0 ? "ok" : "error";
  }
  next[e.id] = {
    ...cur,
    status,
    exitCode: e.exitCode,
    durationMs: e.durationMs,
    truncated: e.truncated,
  };
  return next;
}
