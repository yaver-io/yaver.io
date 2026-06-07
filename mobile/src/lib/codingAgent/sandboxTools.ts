// codingAgent/sandboxTools.ts — the CODING tool registry for the phone-local
// Mobile Sandbox. PURE + RN-free (tsx-tested). This is the heart of the
// "opencode behavior in Hermes" port (docs/agentic-coding-sandbox.md):
//
//   yaverAgentTools.ts  → control-plane tools (device.list/audit), coding-forbidden
//   sandboxTools.ts     → coding tools (read/list/grep/write/edit/delete) ← THIS
//
// These tools let an LLM drive an ITERATIVE read→grep→edit loop against one
// project's `src/` tree, instead of the single-shot "inline the whole tree,
// return one EditPlan" path (llmAnthropic.ts / llmOpenAI.ts). The win is cost
// (read only what you need — far fewer GLM input tokens), correctness (anchored
// edit_file instead of full-file rewrites), and scale (never holds the whole
// tree in context).
//
// Tools bind to a CodingSandbox — the subset of phoneSandboxSource the agent
// needs, scoped to a single slug. Production wires the real source store
// (sandboxBinding.ts); tests inject an in-memory map. Path safety, atomic
// writes, and traversal rejection all live in phoneSandboxSource — these tools
// never re-implement them, they just call through.

// ── The filesystem capability the tools operate on ─────────────────────
//
// Scoped to ONE project slug already (the binding closes over it), so tool
// args carry only relative paths — the model can never reach another project.

export interface CodingSandboxEntry {
  /** Posix-relative path inside the project's src/ tree. */
  path: string;
  isDirectory: boolean;
  /** Bytes; 0 for directories. */
  size: number;
}

export interface CodingSandbox {
  /** Read a file's UTF-8 contents. Rejects (any error) when missing. */
  readFile(path: string): Promise<string>;
  /** Recursive listing of the src/ tree, files + dirs, sorted by path. */
  listFiles(): Promise<CodingSandboxEntry[]>;
  /** Create or overwrite a file (atomic; parent dirs auto-created). */
  writeFile(path: string, content: string): Promise<void>;
  /** Delete a file. Idempotent — deleting a missing file is not an error. */
  deleteFile(path: string): Promise<void>;
}

// ── Provider-agnostic tool definition (mirrors yaverAgentTools) ─────────

export interface CodingTool<Args = any, Result = any> {
  name: string;
  description: string;
  parameters: Record<string, unknown>; // JSON Schema
  /** True for tools that change the tree. The runner gates these through a
   *  preview/confirm hook so the user keeps the human-in-loop they have today
   *  with EditPlan previews. Read/list/grep are side-effect-free → false. */
  mutating: boolean;
  invoke: (args: Args, box: CodingSandbox) => Promise<Result>;
}

// ── Tunables (caps that keep tool output from blowing the context window) ─

/** Max bytes returned from a single read_file before truncation. A coding
 *  model rarely needs more; truncation is flagged so it can read a range. */
export const READ_MAX_BYTES = 60_000;
/** Max grep matches returned in one call. */
export const GREP_MAX_MATCHES = 80;
/** Max files list_files returns before it flags truncation. */
export const LIST_MAX_FILES = 400;
/** Files larger than this are skipped by grep (almost certainly assets/lockfiles). */
export const GREP_MAX_FILE_BYTES = 400_000;

// ── glob → RegExp (tiny; supports * ** ? and literal segments) ──────────

/** Compile a simple glob to a RegExp anchored to the full relative path.
 *  `**` matches across "/", `*` matches within a segment, `?` one char.
 *  Returns null for an empty/falsy glob (caller treats that as "match all"). */
export function globToRegExp(glob: string | undefined): RegExp | null {
  const g = (glob ?? "").trim();
  if (!g) return null;
  let re = "";
  for (let i = 0; i < g.length; i++) {
    const c = g[i];
    if (c === "*") {
      if (g[i + 1] === "*") {
        re += ".*"; // ** — cross directory boundaries
        i++;
        if (g[i + 1] === "/") i++; // collapse **/ so "**/x" matches "x" too
      } else {
        re += "[^/]*"; // * — within a path segment
      }
    } else if (c === "?") {
      re += "[^/]";
    } else if ("\\^$.|+()[]{}".includes(c)) {
      re += "\\" + c; // escape regex metachars
    } else {
      re += c;
    }
  }
  return new RegExp("^" + re + "$");
}

// ── Tools ───────────────────────────────────────────────────────────────

const listFilesTool: CodingTool<{ glob?: string }, unknown> = {
  name: "list_files",
  description:
    "List the project's src/ files (paths + byte sizes, no contents). Call this " +
    "first to learn the tree. Optionally pass a glob (e.g. \"**/*.tsx\") to filter. " +
    "Cheap — prefer this over reading files you don't need.",
  parameters: {
    type: "object",
    properties: {
      glob: { type: "string", description: "Optional glob filter, e.g. \"src/**/*.ts\" or \"*.tsx\"." },
    },
    additionalProperties: false,
  },
  mutating: false,
  async invoke(args, box) {
    const re = globToRegExp(args?.glob);
    const all = (await box.listFiles()).filter((e) => !e.isDirectory);
    const matched = re ? all.filter((e) => re.test(e.path)) : all;
    const truncated = matched.length > LIST_MAX_FILES;
    const files = (truncated ? matched.slice(0, LIST_MAX_FILES) : matched).map((e) => ({
      path: e.path,
      size: e.size,
    }));
    return { files, count: matched.length, truncated };
  },
};

const readFileTool: CodingTool<{ path: string }, unknown> = {
  name: "read_file",
  description:
    "Read one file's full contents before editing it. ALWAYS read a file before " +
    "you edit_file it, so your old-text anchor is exact. Large files are truncated " +
    "(flagged in the result).",
  parameters: {
    type: "object",
    properties: {
      path: { type: "string", description: "Posix-relative path inside src/." },
    },
    required: ["path"],
    additionalProperties: false,
  },
  mutating: false,
  async invoke(args, box) {
    if (!args?.path) return { error: "read_file requires a path" };
    let content: string;
    try {
      content = await box.readFile(args.path);
    } catch (e) {
      return { error: `cannot read ${args.path}: ${errMsg(e)}` };
    }
    const bytes = byteLength(content);
    if (bytes > READ_MAX_BYTES) {
      return {
        path: args.path,
        content: sliceBytes(content, READ_MAX_BYTES),
        truncated: true,
        totalBytes: bytes,
      };
    }
    return { path: args.path, content, lines: content.split("\n").length, truncated: false };
  },
};

const grepTool: CodingTool<{ pattern: string; glob?: string; flags?: string }, unknown> = {
  name: "grep",
  description:
    "Search file contents with a JavaScript regular expression across the src/ " +
    "tree. Returns path:line: matching-text. Use this to find where a symbol is " +
    "defined or used before editing. Optionally restrict to a glob.",
  parameters: {
    type: "object",
    properties: {
      pattern: { type: "string", description: "JavaScript regex source, e.g. \"function\\\\s+App\"." },
      glob: { type: "string", description: "Optional glob to restrict which files are searched." },
      flags: { type: "string", description: "Optional regex flags (e.g. \"i\"). \"g\" is always implied per line." },
    },
    required: ["pattern"],
    additionalProperties: false,
  },
  mutating: false,
  async invoke(args, box) {
    if (!args?.pattern) return { error: "grep requires a pattern" };
    let re: RegExp;
    try {
      re = new RegExp(args.pattern, (args.flags ?? "").replace(/[gm]/g, "") || undefined);
    } catch (e) {
      return { error: `invalid regex: ${errMsg(e)}` };
    }
    const globRe = globToRegExp(args.glob);
    const files = (await box.listFiles()).filter(
      (e) => !e.isDirectory && e.size <= GREP_MAX_FILE_BYTES && (!globRe || globRe.test(e.path)),
    );
    const matches: Array<{ path: string; line: number; text: string }> = [];
    let truncated = false;
    for (const f of files) {
      let content: string;
      try {
        content = await box.readFile(f.path);
      } catch {
        continue; // raced deletion / unreadable — skip, don't fail the whole grep
      }
      const lines = content.split("\n");
      for (let i = 0; i < lines.length; i++) {
        if (re.test(lines[i])) {
          matches.push({ path: f.path, line: i + 1, text: lines[i].slice(0, 240) });
          if (matches.length >= GREP_MAX_MATCHES) {
            truncated = true;
            break;
          }
        }
      }
      if (truncated) break;
    }
    return { matches, count: matches.length, truncated };
  },
};

const writeFileTool: CodingTool<{ path: string; content: string }, unknown> = {
  name: "write_file",
  description:
    "Create a new file, or overwrite an existing one with FULL new contents. " +
    "Prefer edit_file for changing part of an existing file — write_file replaces " +
    "the whole thing. Paths must be posix-relative inside src/ (no '..', no leading slash).",
  parameters: {
    type: "object",
    properties: {
      path: { type: "string", description: "Posix-relative path inside src/." },
      content: { type: "string", description: "The COMPLETE new file contents." },
    },
    required: ["path", "content"],
    additionalProperties: false,
  },
  mutating: true,
  async invoke(args, box) {
    if (!args?.path) return { error: "write_file requires a path" };
    if (typeof args.content !== "string") return { error: "write_file requires string content" };
    try {
      await box.writeFile(args.path, args.content);
    } catch (e) {
      return { error: `cannot write ${args.path}: ${errMsg(e)}` };
    }
    return { ok: true, path: args.path, bytes: byteLength(args.content) };
  },
};

const editFileTool: CodingTool<
  { path: string; old: string; new: string; replaceAll?: boolean },
  unknown
> = {
  name: "edit_file",
  description:
    "Replace an exact snippet of an existing file. `old` must appear verbatim in " +
    "the current file (read_file first). If `old` matches more than once, the edit " +
    "is refused unless replaceAll is true — add surrounding context to make it " +
    "unique. Cheaper and safer than rewriting the whole file with write_file.",
  parameters: {
    type: "object",
    properties: {
      path: { type: "string", description: "Posix-relative path inside src/." },
      old: { type: "string", description: "Exact text to find (must currently exist in the file)." },
      new: { type: "string", description: "Replacement text." },
      replaceAll: { type: "boolean", description: "Replace every occurrence (default: require a unique match)." },
    },
    required: ["path", "old", "new"],
    additionalProperties: false,
  },
  mutating: true,
  async invoke(args, box) {
    if (!args?.path) return { error: "edit_file requires a path" };
    if (typeof args.old !== "string" || !args.old) return { error: "edit_file requires non-empty 'old'" };
    if (typeof args.new !== "string") return { error: "edit_file requires string 'new'" };
    let content: string;
    try {
      content = await box.readFile(args.path);
    } catch (e) {
      return { error: `cannot read ${args.path} to edit: ${errMsg(e)}` };
    }
    const occurrences = countOccurrences(content, args.old);
    if (occurrences === 0) {
      return { error: `'old' text not found in ${args.path} — read_file again; it must match verbatim` };
    }
    if (occurrences > 1 && !args.replaceAll) {
      return {
        error: `'old' matches ${occurrences} times in ${args.path} — add context to make it unique, or pass replaceAll:true`,
      };
    }
    const updated = args.replaceAll
      ? content.split(args.old).join(args.new)
      : content.replace(args.old, args.new);
    try {
      await box.writeFile(args.path, updated);
    } catch (e) {
      return { error: `cannot write ${args.path}: ${errMsg(e)}` };
    }
    return { ok: true, path: args.path, replaced: args.replaceAll ? occurrences : 1, bytes: byteLength(updated) };
  },
};

const deleteFileTool: CodingTool<{ path: string }, unknown> = {
  name: "delete_file",
  description: "Delete a file from src/. Idempotent — deleting a missing file is not an error.",
  parameters: {
    type: "object",
    properties: {
      path: { type: "string", description: "Posix-relative path inside src/." },
    },
    required: ["path"],
    additionalProperties: false,
  },
  mutating: true,
  async invoke(args, box) {
    if (!args?.path) return { error: "delete_file requires a path" };
    let existed = true;
    try {
      await box.readFile(args.path);
    } catch {
      existed = false;
    }
    try {
      await box.deleteFile(args.path);
    } catch (e) {
      return { error: `cannot delete ${args.path}: ${errMsg(e)}` };
    }
    return { ok: true, path: args.path, existed };
  },
};

// ── Registry + dispatcher ─────────────────────────────────────────────

export const CODING_TOOLS: CodingTool[] = [
  listFilesTool as CodingTool,
  readFileTool as CodingTool,
  grepTool as CodingTool,
  writeFileTool as CodingTool,
  editFileTool as CodingTool,
  deleteFileTool as CodingTool,
];

export function codingToolByName(name: string): CodingTool | undefined {
  return CODING_TOOLS.find((t) => t.name === name);
}

/** Dispatch a tool by name against a slug-scoped sandbox. Throws on unknown
 *  tool name so the model gets a clear signal (mirrors dispatchYaverAgentTool). */
export async function dispatchCodingTool(
  name: string,
  args: unknown,
  box: CodingSandbox,
): Promise<unknown> {
  const tool = codingToolByName(name);
  if (!tool) throw new Error(`unknown coding tool: ${name}`);
  return tool.invoke(args as never, box);
}

// ── Helpers ───────────────────────────────────────────────────────────

function countOccurrences(haystack: string, needle: string): number {
  if (!needle) return 0;
  let n = 0;
  let idx = haystack.indexOf(needle);
  while (idx !== -1) {
    n++;
    idx = haystack.indexOf(needle, idx + needle.length);
  }
  return n;
}

function byteLength(s: string): number {
  return new TextEncoder().encode(s).byteLength;
}

/** Truncate to at most maxBytes of UTF-8 without splitting a multibyte char. */
function sliceBytes(s: string, maxBytes: number): string {
  const enc = new TextEncoder();
  if (enc.encode(s).byteLength <= maxBytes) return s;
  // Walk back from a char boundary until we're under the cap.
  let lo = 0;
  let hi = s.length;
  while (lo < hi) {
    const mid = (lo + hi + 1) >> 1;
    if (enc.encode(s.slice(0, mid)).byteLength <= maxBytes) lo = mid;
    else hi = mid - 1;
  }
  return s.slice(0, lo);
}

function errMsg(e: unknown): string {
  return e instanceof Error ? e.message : String(e);
}
