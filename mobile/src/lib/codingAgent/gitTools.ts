// codingAgent/gitTools.ts — git as AGENTIC tools. Exposes the sandboxGitOps lib
// to the coding loop (runner.ts) as CodingTool[], so the on-device agent can
// commit, branch, diff, merge, resolve conflicts, and push exactly like a human
// would — the "all agentic stuff" path. Mutating tools are flagged so the runner
// gates them through the same confirm hook as file edits.
//
// Git tools need a {fs, dir} (+ optional network) context that the file-oriented
// CodingSandbox box doesn't carry, so this is a FACTORY that closes over the git
// context; the box arg is ignored. Wire with:
//   runCodingAgent({ ..., tools: [...CODING_TOOLS, ...makeGitTools(git, net)] })

import type { CodingTool } from "./sandboxTools";
import type { SandboxGitOptions } from "./sandboxGit";
import { commitAll, log } from "./sandboxGit";
import {
  currentBranch,
  listBranches,
  createBranch,
  switchBranch,
  deleteBranch,
  mergeBranch,
  listConflicts,
  resolveConflict,
  completeMerge,
  diffStatus,
  fileLineDiff,
  addRemote,
  listRemotes,
  push,
  type NetOptions,
} from "./sandboxGitOps";

const NO_PROPS = { type: "object", properties: {}, required: [] as string[] } as const;

function obj(props: Record<string, unknown>, required: string[] = []) {
  return { type: "object", properties: props, required };
}

const STR = { type: "string" } as const;

/**
 * Build the git tool set for a project's repo. Pass `net` (http + onAuth) to
 * enable push; without it, git_push is omitted so the agent can't attempt a
 * network op it has no credentials for.
 */
export function makeGitTools(git: SandboxGitOptions, net?: NetOptions): CodingTool[] {
  const tools: CodingTool[] = [
    {
      name: "git_status",
      description: "Current branch + the list of working-tree changes vs the last commit (added/modified/deleted).",
      parameters: NO_PROPS,
      mutating: false,
      async invoke() {
        return { branch: await currentBranch(git), changes: await diffStatus(git) };
      },
    },
    {
      name: "git_diff",
      description: "Line-level diff of one file: its last-committed version vs the current working-tree content.",
      parameters: obj({ path: STR }, ["path"]),
      mutating: false,
      async invoke(a: { path: string }) {
        return { path: a.path, diff: await fileLineDiff(git, a.path) };
      },
    },
    {
      name: "git_log",
      description: "Recent commits (newest first): oid, message, timestamp.",
      parameters: obj({ depth: { type: "number" } }),
      mutating: false,
      async invoke(a: { depth?: number }) {
        return { commits: await log(git, a.depth ?? 20) };
      },
    },
    {
      name: "git_commit",
      description: "Stage ALL current changes and commit them with the given message. Returns the new commit oid, or null if nothing changed.",
      parameters: obj({ message: STR }, ["message"]),
      mutating: true,
      async invoke(a: { message: string }) {
        return { oid: await commitAll(git, a.message) };
      },
    },
    {
      name: "git_branch_list",
      description: "List local branches.",
      parameters: NO_PROPS,
      mutating: false,
      async invoke() {
        return { branches: await listBranches(git), current: await currentBranch(git) };
      },
    },
    {
      name: "git_branch",
      description: "Create a branch at HEAD (optionally switch to it), or switch to / delete an existing one. action: 'create' | 'switch' | 'delete'.",
      parameters: obj({ action: { type: "string", enum: ["create", "switch", "delete"] }, name: STR, checkout: { type: "boolean" } }, ["action", "name"]),
      mutating: true,
      async invoke(a: { action: "create" | "switch" | "delete"; name: string; checkout?: boolean }) {
        if (a.action === "create") await createBranch(git, a.name, { checkout: a.checkout ?? true });
        else if (a.action === "switch") await switchBranch(git, a.name);
        else await deleteBranch(git, a.name);
        return { ok: true, current: await currentBranch(git) };
      },
    },
    {
      name: "git_merge",
      description: "Merge another branch into the current one. Clean merges auto-commit; on conflict, returns the conflicting file paths (now carrying <<<<<<< markers) and a theirsOid — resolve each with git_resolve_conflict then call git_complete_merge with that theirsOid.",
      parameters: obj({ branch: STR }, ["branch"]),
      mutating: true,
      async invoke(a: { branch: string }) {
        return mergeBranch(git, a.branch);
      },
    },
    {
      name: "git_list_conflicts",
      description: "List files that currently contain unresolved conflict markers.",
      parameters: NO_PROPS,
      mutating: false,
      async invoke() {
        return { conflicts: await listConflicts(git) };
      },
    },
    {
      name: "git_resolve_conflict",
      description: "Write the final, conflict-free content for a conflicted file and stage it. Provide the FULL resolved file content (no markers).",
      parameters: obj({ path: STR, content: STR }, ["path", "content"]),
      mutating: true,
      async invoke(a: { path: string; content: string }) {
        await resolveConflict(git, a.path, a.content);
        return { ok: true, remaining: await listConflicts(git) };
      },
    },
    {
      name: "git_complete_merge",
      description: "After all conflicts are resolved, create the merge commit (two parents). Pass the theirsOid returned by git_merge.",
      parameters: obj({ message: STR, theirsOid: STR }, ["message"]),
      mutating: true,
      async invoke(a: { message: string; theirsOid?: string }) {
        return { oid: await completeMerge(git, a.message, { theirsOid: a.theirsOid }) };
      },
    },
    {
      name: "git_remote",
      description: "Add or list remotes. action: 'add' | 'list'. For 'add', provide name + url.",
      parameters: obj({ action: { type: "string", enum: ["add", "list"] }, name: STR, url: STR }, ["action"]),
      mutating: true,
      async invoke(a: { action: "add" | "list"; name?: string; url?: string }) {
        if (a.action === "add") {
          if (!a.name || !a.url) throw new Error("git_remote add needs name + url");
          await addRemote(git, a.name, a.url);
        }
        return { remotes: await listRemotes(git) };
      },
    },
  ];

  if (net) {
    tools.push({
      name: "git_push",
      description: "Push the current branch (or the given ref) to a remote (default 'origin'). Requires configured auth.",
      parameters: obj({ remote: STR, ref: STR, force: { type: "boolean" } }),
      mutating: true,
      async invoke(a: { remote?: string; ref?: string; force?: boolean }) {
        return push(git, net, a);
      },
    });
  }

  return tools;
}

/** Names of the git tools, for prompt/allowlist wiring. */
export function gitToolNames(withPush: boolean): string[] {
  const base = [
    "git_status",
    "git_diff",
    "git_log",
    "git_commit",
    "git_branch_list",
    "git_branch",
    "git_merge",
    "git_list_conflicts",
    "git_resolve_conflict",
    "git_complete_merge",
    "git_remote",
  ];
  return withPush ? [...base, "git_push"] : base;
}
