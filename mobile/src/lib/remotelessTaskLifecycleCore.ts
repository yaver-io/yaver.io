// Pure state machine for finite phone-local work. Native Android/iOS glue lives
// in remotelessTaskLifecycle.ts; keeping transitions here makes the important
// "an interrupted task is failed, never falsely completed" rule testable.

export type RemotelessTaskKind = "coding" | "git-clone" | "git-commit" | "git-push";
export type RemotelessTaskState = "running" | "ready" | "completed" | "failed" | "stopped" | "review";

export interface RemotelessTaskRecord {
  id: string;
  title: string;
  projectSlug: string;
  kind: RemotelessTaskKind;
  phase: string;
  state: RemotelessTaskState;
  startedAt: number;
  updatedAt: number;
  finishedAt?: number;
  backgroundProtection: "pending" | "active" | "bounded" | "unavailable";
  detail?: string;
}

export function startRemotelessRecord(
  input: Pick<RemotelessTaskRecord, "id" | "title" | "projectSlug" | "kind"> & { phase?: string },
  now: number,
): RemotelessTaskRecord {
  return {
    ...input,
    phase: input.phase || initialPhase(input.kind),
    state: "running",
    startedAt: now,
    updatedAt: now,
    backgroundProtection: "pending",
  };
}

export function advanceRemotelessRecord(
  record: RemotelessTaskRecord,
  patch: Partial<Pick<RemotelessTaskRecord, "phase" | "backgroundProtection" | "detail">>,
  now: number,
): RemotelessTaskRecord {
  if (record.state !== "running") return record;
  return { ...record, ...patch, updatedAt: now };
}

export function finishRemotelessRecord(
  record: RemotelessTaskRecord,
  state: Extract<RemotelessTaskState, "ready" | "completed" | "failed" | "stopped" | "review">,
  now: number,
  detail?: string,
): RemotelessTaskRecord {
  if (record.state !== "running") return record;
  return {
    ...record,
    state,
    phase: state === "completed" ? "done" : state,
    updatedAt: now,
    finishedAt: now,
    detail: detail || record.detail,
  };
}

/** A fresh JS process cannot safely replay file mutations. Preserve the repo
 * bytes and name the missing completion claim instead of fabricating Review. */
export function recoverInterruptedRecords(
  records: RemotelessTaskRecord[],
  activeIds: ReadonlySet<string>,
  now: number,
): RemotelessTaskRecord[] {
  return records.map((record) =>
    record.state === "running" && !activeIds.has(record.id)
      ? finishRemotelessRecord(
          record,
          "failed",
          now,
          "Background execution ended before the task reported completion. Inspect the working tree, then continue or retry.",
        )
      : record,
  );
}

function initialPhase(kind: RemotelessTaskKind): string {
  if (kind === "git-commit") return "committing";
  if (kind === "git-push") return "pushing";
  return "starting";
}
