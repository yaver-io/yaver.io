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
  /** The exact command line typed into the shell (no trailing newline). */
  command: string;
  /** Short gloss for the button's accessibility label / tooltip. */
  hint: string;
}

export const AGENT_LAUNCHERS: readonly AgentLaunch[] = [
  {
    id: "claude",
    label: "Claude",
    command: "claude --dangerously-skip-permissions",
    hint: "Launch Claude Code with permission prompts skipped",
  },
  {
    id: "codex",
    label: "Codex",
    command: "codex --dangerously-bypass-approvals-and-sandbox",
    hint: "Launch Codex with approvals + sandbox bypassed",
  },
  {
    id: "opencode",
    label: "OpenCode",
    command: "opencode",
    hint: "Launch OpenCode (bring-your-own-provider TUI)",
  },
] as const;

/** The bytes to send to the PTY to run a launcher: the command + Enter. */
export function launchLine(l: AgentLaunch): string {
  return `${l.command}\n`;
}
