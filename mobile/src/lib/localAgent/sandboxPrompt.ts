// localAgent/sandboxPrompt.ts — monorepo-aware prompt builder for on-device
// Mobile Sandbox coding (the Coder tier). PURE + RN-free (tsx-tested).
//
// The Sandbox is phone-only (local SQLite project, no paired machine). When a
// coder-tier model is active, this assembles the model's context EXPLICITLY —
// system prompt + workspace layout + the open file + the user's request — so
// on-device codegen is grounded, not blind. The Tasks empty-state already says
// the Sandbox "expects a monorepo workspace"; we honor yaver.workspace.yaml
// conventions so edits target the right package.
//
// Output is a single string prompt + a small budget guard (tiny models have
// tiny context windows, so we trim the least-important context first). The
// native engine adapter feeds the returned prompt to llama.rn; the result is
// validated/applied with preview before touching the SQLite project.

export interface WorkspacePackage {
  /** Package name, e.g. "@app/web" or "api". */
  name: string;
  /** Path within the workspace, e.g. "packages/web". */
  path: string;
  /** Optional one-line role hint ("Next.js frontend", "Convex backend"). */
  role?: string;
}

export interface SandboxContext {
  /** Workspace packages from yaver.workspace.yaml (empty for a single-package project). */
  packages?: WorkspacePackage[];
  /** Relative file tree (paths only), already scoped to the relevant package. */
  fileTree?: string[];
  /** The file the user currently has open (path + contents). */
  openFile?: { path: string; contents: string };
  /** Which package the request targets, if known (name or path). */
  targetPackage?: string;
  /** Detected stack hints ("typescript","react","convex","sqlite"). */
  stack?: string[];
}

export interface SandboxRequest {
  /** The user's instruction (typed or transcribed). */
  instruction: string;
  /** "scaffold" | "edit" | "explain" | "fix" — shapes the system guidance. */
  mode: SandboxMode;
}

export type SandboxMode = "scaffold" | "edit" | "explain" | "fix";

export interface PromptBudget {
  /** Rough char budget for the whole prompt (model context ~ tokens*4). */
  maxChars: number;
}

const DEFAULT_BUDGET: PromptBudget = { maxChars: 8000 }; // ~2k tokens, safe for 1.5-3B

const MODE_GUIDANCE: Record<SandboxMode, string> = {
  scaffold:
    "Create new files for the request. Output each file as a fenced block prefixed with its path. Keep it minimal and runnable.",
  edit:
    "Modify the open file (or the named target) to satisfy the request. Output the full updated file in a fenced block prefixed with its path. Do not rewrite unrelated code.",
  explain:
    "Explain the relevant code clearly and concisely. Do not modify files unless asked.",
  fix:
    "Diagnose and fix the problem. Output only the changed files as fenced blocks prefixed with their paths, plus a one-line note on the cause.",
};

function header(stack?: string[]): string {
  const s = stack && stack.length ? ` The stack is: ${stack.join(", ")}.` : "";
  return [
    "You are an on-device coding assistant working inside the Yaver Mobile Sandbox",
    "— a phone-only project (local SQLite-backed, no remote machine).",
    s,
    "You are a small model: be precise, prefer the smallest correct change, and",
    "never invent files or APIs you weren't shown. If unsure, say so briefly.",
  ].join(" ");
}

function workspaceBlock(pkgs?: WorkspacePackage[], target?: string): string {
  if (!pkgs || pkgs.length === 0) return "";
  const lines = pkgs.map((p) => {
    const mark = target && (p.name === target || p.path === target) ? "→ " : "  ";
    return `${mark}${p.name} (${p.path})${p.role ? ` — ${p.role}` : ""}`;
  });
  const note = target
    ? `\nTarget package: ${target}. Put changes there unless the request clearly belongs elsewhere.`
    : "";
  return `Monorepo workspace (yaver.workspace.yaml):\n${lines.join("\n")}${note}`;
}

function treeBlock(tree?: string[], budget = 60): string {
  if (!tree || tree.length === 0) return "";
  const shown = tree.slice(0, budget);
  const more = tree.length > budget ? `\n…(+${tree.length - budget} more files)` : "";
  return `Files:\n${shown.join("\n")}${more}`;
}

function openFileBlock(f?: { path: string; contents: string }, maxChars = 4000): string {
  if (!f) return "";
  const body = f.contents.length > maxChars
    ? f.contents.slice(0, maxChars) + "\n…(truncated)"
    : f.contents;
  return `Open file — ${f.path}:\n\`\`\`\n${body}\n\`\`\``;
}

/**
 * Build the full prompt. Trims least-important context first to fit the budget:
 * file tree → open file body → workspace list (request + mode guidance always kept).
 */
export function buildSandboxPrompt(
  ctx: SandboxContext,
  req: SandboxRequest,
  budget: PromptBudget = DEFAULT_BUDGET,
): string {
  const must = [
    header(ctx.stack),
    "",
    MODE_GUIDANCE[req.mode],
    "",
    `Request: ${req.instruction.trim()}`,
  ].join("\n");

  // Optional context blocks, in priority order (most useful last so it's
  // closest to the request — but we add from cheapest-to-drop downward).
  const ws = workspaceBlock(ctx.packages, ctx.targetPackage);
  let openF = openFileBlock(ctx.openFile);
  let tree = treeBlock(ctx.fileTree);

  // Assemble + trim to budget. Drop tree first, then shrink open file.
  const join = (parts: string[]) => parts.filter(Boolean).join("\n\n");
  let prompt = join([must, ws, openF, tree]);
  if (prompt.length <= budget.maxChars) return prompt;

  tree = ""; // drop file tree
  prompt = join([must, ws, openF, tree]);
  if (prompt.length <= budget.maxChars) return prompt;

  // Shrink the open file to whatever budget remains after the must-keep parts.
  const overhead = join([must, ws]).length + 40;
  const room = Math.max(0, budget.maxChars - overhead);
  openF = ctx.openFile ? openFileBlock(ctx.openFile, Math.max(400, room)) : "";
  prompt = join([must, ws, openF]);
  if (prompt.length <= budget.maxChars) return prompt;

  // Last resort: just the must-keep core.
  return must;
}

/** Heuristic: infer the mode from a spoken/typed instruction when the UI
 *  didn't set one explicitly. */
export function inferSandboxMode(instruction: string): SandboxMode {
  const s = instruction.toLowerCase();
  if (/\b(explain|what does|how does|why does|understand)\b/.test(s)) return "explain";
  if (/\b(fix|bug|broken|error|failing|crash|wrong)\b/.test(s)) return "fix";
  if (/\b(create|add|new|scaffold|generate|set up|build a)\b/.test(s)) return "scaffold";
  return "edit";
}
