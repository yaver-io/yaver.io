// codingAgent/codingAgentRun.ts — RN glue that runs the agentic coding loop on a
// phone-local project with (a) the full git tool set available to the agent and
// (b) automatic before/after git checkpoints wrapping the run, so any autonomous
// edit is one tap from revert. This is the production entry the sandbox editor
// calls; it pulls in expo (via sandboxBinding + gitFsExpo), so headless tests
// don't import it — the pieces it composes are each tested:
//   runWithCheckpoints (sandboxGitOps.test), makeGitTools (gitTools.test),
//   the loop itself (runner/sandboxTools tests).

import { createExpoGitFs, gitDirForSlug } from "./gitFsExpo";
import { sandboxForSlug } from "./sandboxBinding";
import { CODING_TOOLS, type CodingSandbox, type CodingTool } from "./sandboxTools";
import { makeGitTools } from "./gitTools";
import { runWithCheckpoints, type NetOptions, type CheckpointedRun } from "./sandboxGitOps";
import type { SandboxGitOptions } from "./sandboxGit";
import {
  runCodingAgent,
  type CodingAgentConfig,
  type CodingAgentProgress,
  type CodingAgentResult,
} from "./runner";

/** The git context (fs + virtual dir) for a phone-local project slug. */
export function gitContextForSlug(slug: string): SandboxGitOptions {
  return { fs: createExpoGitFs(), dir: gitDirForSlug(slug) };
}

export interface AgenticRunOptions {
  slug: string;
  prompt: string;
  config: CodingAgentConfig;
  /** Provide to enable git_push (http = isomorphic-git/http/web + onAuth). */
  net?: NetOptions;
  /** Human-in-the-loop gate for mutating file/git tools. Omit for yolo. */
  confirmMutation?: (call: { name: string; args: unknown }) => Promise<boolean> | boolean;
  onProgress?: (e: CodingAgentProgress) => void;
  signal?: AbortSignal;
  maxSteps?: number;
  /** Disable the before/after checkpoints (default: checkpoints ON). */
  noCheckpoint?: boolean;
  /**
   * The file-tool sandbox. Defaults to sandboxForSlug (src/-only — right for
   * in-app SQLite projects). Pass repoSandboxForSlug(slug) for a CLONED repo so
   * the agent can edit the whole tree (package.json, convex/, app/, src/), not
   * just src/. Either way it stays rooted at the same dir git operates on.
   */
  sandbox?: CodingSandbox;
}

/**
 * Run the agentic coding loop against a phone-local project. The agent gets the
 * file tools (read/grep/edit) AND the git tools (commit/branch/merge/conflict/
 * push), and the whole run is bracketed by git checkpoints unless disabled.
 */
export async function runAgenticCoding(opts: AgenticRunOptions): Promise<CheckpointedRun<CodingAgentResult>> {
  const sandbox = opts.sandbox ?? sandboxForSlug(opts.slug);
  const git = gitContextForSlug(opts.slug);
  const tools: CodingTool[] = [...CODING_TOOLS, ...makeGitTools(git, opts.net)];

  const run = () =>
    runCodingAgent({
      prompt: opts.prompt,
      sandbox,
      config: opts.config,
      tools,
      confirmMutation: opts.confirmMutation,
      onProgress: opts.onProgress,
      signal: opts.signal,
      maxSteps: opts.maxSteps,
    });

  if (opts.noCheckpoint) {
    const result = await run();
    return { result, before: null, after: null, changed: [] };
  }

  const label = opts.prompt.replace(/\s+/g, " ").trim().slice(0, 60) || "agent run";
  return runWithCheckpoints(git, label, run);
}
