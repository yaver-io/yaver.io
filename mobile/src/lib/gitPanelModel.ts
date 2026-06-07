// gitPanelModel.ts — PURE view-model for the sandbox git panel. Turns raw
// sandboxGitOps results into the grouped/labelled shape the RN component renders,
// so the component stays thin and the logic is tsx-tested. No RN imports.

import type { FileDiff } from "./codingAgent/sandboxGitOps";

export interface GroupedChanges {
  added: string[];
  modified: string[];
  deleted: string[];
  total: number;
}

export function groupChanges(changes: FileDiff[]): GroupedChanges {
  const g: GroupedChanges = { added: [], modified: [], deleted: [], total: changes.length };
  for (const c of changes) {
    if (c.status === "added") g.added.push(c.path);
    else if (c.status === "modified") g.modified.push(c.path);
    else g.deleted.push(c.path);
  }
  g.added.sort();
  g.modified.sort();
  g.deleted.sort();
  return g;
}

/** Header summary line, e.g. "main · 3 changes (2 +, 1 ~)" or "main · clean". */
export function statusSummary(branch: string | null, changes: FileDiff[]): string {
  const b = branch ?? "(detached)";
  if (changes.length === 0) return `${b} · clean`;
  const g = groupChanges(changes);
  const parts: string[] = [];
  if (g.added.length) parts.push(`${g.added.length} +`);
  if (g.modified.length) parts.push(`${g.modified.length} ~`);
  if (g.deleted.length) parts.push(`${g.deleted.length} −`);
  return `${b} · ${changes.length} change${changes.length === 1 ? "" : "s"} (${parts.join(", ")})`;
}

/** A default commit message from the change set, e.g.
 *  "Update App.tsx" / "Add 2 files, update 1". Used to pre-fill the commit box. */
export function suggestCommitMessage(changes: FileDiff[]): string {
  if (changes.length === 0) return "";
  const g = groupChanges(changes);
  if (changes.length === 1) {
    const c = changes[0];
    const verb = c.status === "added" ? "Add" : c.status === "deleted" ? "Delete" : "Update";
    return `${verb} ${basename(c.path)}`;
  }
  const bits: string[] = [];
  if (g.added.length) bits.push(`add ${g.added.length} file${g.added.length === 1 ? "" : "s"}`);
  if (g.modified.length) bits.push(`update ${g.modified.length}`);
  if (g.deleted.length) bits.push(`delete ${g.deleted.length}`);
  const joined = bits.join(", ");
  return joined.charAt(0).toUpperCase() + joined.slice(1);
}

function basename(p: string): string {
  const i = p.lastIndexOf("/");
  return i < 0 ? p : p.slice(i + 1);
}

export type PushState = "idle" | "no-token" | "no-remote" | "pushing" | "ok" | "error";

/** Whether the Push button should be enabled + the reason if not. */
export function pushability(opts: { hasToken: boolean; hasRemote: boolean; busy: boolean }): {
  enabled: boolean;
  hint: string;
} {
  if (opts.busy) return { enabled: false, hint: "Pushing…" };
  if (!opts.hasToken) return { enabled: false, hint: "Add a GitHub token to push" };
  if (!opts.hasRemote) return { enabled: false, hint: "Set a remote (owner/repo) to push" };
  return { enabled: true, hint: "Push to GitHub" };
}
