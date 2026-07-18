// followUpPlan.ts — decide what sending a follow-up message actually does.
//
// Extracted from app/(tabs)/tasks.tsx for the same reason as
// probeTargets.ts and directProbeFailure.ts: the decision is pure, it is
// load-bearing, and it cannot be exercised from a test while it is buried in a
// 5000-line React component. `npx tsx` can run this without React Native.
//
// WHY THIS EXISTS AT ALL
//
// The reported bug was "I write a message, I cannot see it at all, then it
// shows a new task". Both halves come from this decision:
//
//   - A task that has FINISHED cannot be continued in place — resuming a
//     runner's completed session is unreliable — so the follow-up forks into a
//     child task. Tasks finish quickly, so in practice almost every follow-up
//     forked, and the UI swapped to a fresh child. From the outside that reads
//     as "my chat was replaced by a new empty task".
//   - The fork was SILENT for the finished-parent case (by design: the user
//     just typed and hit send, a dialog would be noise), so nothing explained
//     the new thread.
//
// The forking itself is correct. What was wrong is that the UI presented a
// continuation as a discontinuity. Naming the outcomes here lets the caller
// carry the conversation across a fork and show the user's own message
// immediately, so the thread reads continuously the way a chat app should.

export type FollowUpAction =
  /** Resume the same task in place. The runner is still live. */
  | "continue"
  /** Fork to a child task, no dialog — same runner, parent already finished. */
  | "fork-silent"
  /** Fork to a child task, but confirm first — the user changed the runner. */
  | "fork-confirm"
  /** Adopted tmux session: input goes straight to the pane. */
  | "tmux-input";

export interface FollowUpPlan {
  action: FollowUpAction;
  /** Runner the child should use. Empty for continue/tmux. */
  forkRunner: string;
  /**
   * True whenever the user's turn must be carried into a new task view.
   * The caller uses this to keep prior turns visible across the fork.
   */
  carriesConversation: boolean;
}

export interface FollowUpInput {
  isAdopted?: boolean;
  /** Runner recorded on the task when it started. */
  parentRunner?: string | null;
  /** Runner currently selected in the picker. */
  desiredRunner?: string | null;
  status?: string | null;
}

/** Statuses that mean the runner session is over and cannot be resumed. */
const FINISHED = new Set(["completed", "review", "failed", "stopped"]);

export function planFollowUp(input: FollowUpInput): FollowUpPlan {
  if (input.isAdopted) {
    return { action: "tmux-input", forkRunner: "", carriesConversation: false };
  }

  const parentRunner = (input.parentRunner || "").trim();
  const desiredRunner = (input.desiredRunner || "").trim();

  // A runner change only counts when we actually know BOTH sides. Treating an
  // unknown parent as "changed" would fork every legacy task on its first
  // follow-up, which is the loudest possible failure for the quietest cause.
  const runnerChanged = !!desiredRunner && !!parentRunner && desiredRunner !== parentRunner;
  const parentFinished = FINISHED.has((input.status || "").trim());

  if (runnerChanged) {
    return { action: "fork-confirm", forkRunner: desiredRunner, carriesConversation: true };
  }
  if (parentFinished) {
    // Same runner, dead session. Fork quietly and keep the thread continuous.
    // `|| "claude"` mirrors the agent side: fork requires a non-empty runner
    // and legacy tasks may have none recorded.
    return {
      action: "fork-silent",
      forkRunner: parentRunner || desiredRunner || "claude",
      carriesConversation: true,
    };
  }
  return { action: "continue", forkRunner: "", carriesConversation: false };
}
