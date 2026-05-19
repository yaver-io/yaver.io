// Mirror of desktop/agent/command_events.go (schema v1). Keep in
// lockstep with web/lib/command-events.ts. These events ride the task
// SSE stream P2P only — never Convex.

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
  exitCode?: number; // omitted when the runner gave no exit status
  durationMs?: number; // omitted when unknown
  truncated: boolean;
  ts: number;
}

export type CommandEvent =
  | CommandStartEvent
  | CommandOutputEvent
  | CommandEndEvent;

// Accumulated view of one command for the card UI.
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

// Pure reducer: fold a CommandEvent into the id→model map. Ordering of
// command_output is restored via `seq` so a raced SSE delivery can't
// scramble the transcript.
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
  if (!cur) return prev; // output/end before start — ignore
  if (e.type === "command_output") {
    next[e.id] = {
      ...cur,
      [e.stream]: (cur[e.stream] as string) + e.chunk,
    };
    return next;
  }
  // command_end
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
