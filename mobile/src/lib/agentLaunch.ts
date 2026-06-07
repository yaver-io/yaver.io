// agentLaunch.ts — the canonical one-tap "launch a coding agent in the PTY"
// commands, shared by the mobile shell terminal (and mirrored in the web
// TerminalView). PURE + dependency-free (tsx-tested) so the exact dangerous
// flags are pinned in one place and can't silently drift.
//
// These are typed straight into the remote shell over /ws/terminal, so the
// agent launches on the BOX (magara, etc.) — not on the phone. The "skip
// permissions / bypass approvals" flags are the yolo modes the user runs
// interactively (feedback_runners_always_dangerous): claude-code, codex, and
// opencode are the supported runners.

export interface AgentLaunch {
  id: "claude" | "codex" | "opencode";
  label: string;
  /** The exact command line typed into the shell to OPEN it (no trailing newline). */
  command: string;
  /** The slash command typed to CLOSE/quit the runner's TUI (no newline).
   *  All three accept `/exit`; we also expose a Ctrl-C fallback in the UI. */
  closeCommand: string;
  /** Short gloss for the button's accessibility label / tooltip. */
  hint: string;
}

export const AGENT_LAUNCHERS: readonly AgentLaunch[] = [
  {
    id: "claude",
    label: "Claude",
    command: "claude --dangerously-skip-permissions",
    closeCommand: "/exit",
    hint: "Launch Claude Code with permission prompts skipped",
  },
  {
    id: "codex",
    label: "Codex",
    command: "codex --dangerously-bypass-approvals-and-sandbox",
    closeCommand: "/exit",
    hint: "Launch Codex with approvals + sandbox bypassed",
  },
  {
    id: "opencode",
    label: "OpenCode",
    command: "opencode",
    closeCommand: "/exit",
    hint: "Launch OpenCode (bring-your-own-provider TUI)",
  },
] as const;

/** Bytes to send to OPEN a runner: the command + Enter. */
export function launchLine(l: AgentLaunch): string {
  return `${l.command}\n`;
}

/** Bytes to send to CLOSE a runner: its `/exit` slash command + Enter. */
export function closeLine(l: AgentLaunch): string {
  return `${l.closeCommand}\n`;
}
