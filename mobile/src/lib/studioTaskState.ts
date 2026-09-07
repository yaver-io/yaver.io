import type { Task, TaskStatus } from "./quic";

/** Terminal task state is authoritative. Retained/replayed console bytes may
 * arrive after the terminal frame, but they describe output history — they do
 * not mean the runner started coding again. */
export function taskStatusIsTerminal(status: TaskStatus): boolean {
  return status === "ready" || status === "completed" || status === "review" || status === "failed" || status === "stopped";
}

/** Apply an observed status without allowing late transport frames or an
 * older list response to resurrect a finished task. */
export function withObservedTaskStatus(task: Task, observed: TaskStatus): Task {
  if (taskStatusIsTerminal(task.status) && !taskStatusIsTerminal(observed)) return task;
  return task.status === observed ? task : { ...task, status: observed };
}

/** An accepted follow-up is the one event that may deliberately move a
 * terminal conversation into a new running turn. Keep the last agent timestamp
 * unchanged: it is the revision fence that lets mergeTaskSnapshot reject a
 * compact list response captured before this POST completed. */
export function beginTaskTurn(task: Task): Task {
  return task.status === "running" ? task : { ...task, status: "running" };
}

/** Merge a fresh agent snapshot into the hydrated conversation while keeping
 * fields omitted by the compact `/vibing/tasks` list (notably turns). */
export function mergeTaskSnapshot(current: Task, snapshot: Task): Task {
  const currentUpdatedAt = Number(current.updatedAt) || 0;
  const snapshotUpdatedAt = Number(snapshot.updatedAt) || 0;
  const statusChanged = current.status !== snapshot.status;
  const snapshotIsNewer = snapshotUpdatedAt > currentUpdatedAt;
  // A list/get response with the same or an older revision may have been in
  // flight when a follow-up was accepted. It cannot end that new turn. In the
  // other direction, retained output from an old turn cannot resurrect a
  // terminal task unless the agent snapshot itself has a newer revision.
  const status = statusChanged && !snapshotIsNewer ? current.status : snapshot.status;
  return {
    ...current,
    ...snapshot,
    // sendTask stamps createdAt when this client observed acceptance. Keep it
    // across compact list refreshes: replacing it with a skewed remote wall
    // clock made a just-created task instantly look stalled on the phone.
    createdAt: current.createdAt || snapshot.createdAt,
    status,
    turns: snapshot.turns ?? current.turns,
    pendingFollowUps: snapshot.pendingFollowUps ?? current.pendingFollowUps,
  };
}
