// llmLocal.ts — on-device implementation of the LlmProvider contract from
// llmClient.ts. The third coding backend (alongside llmAnthropic / llmOpenAI),
// but the only one that runs entirely on the phone.
//
// Tiny GGUF coder models can't reliably emit tool-calls, so instead of forcing
// JSON we (1) build a grounded prompt with the shared sandboxPrompt layer and
// (2) parse the model's fenced, path-prefixed code blocks into an EditPlan.
// The fenced-block contract matches MODE_GUIDANCE in sandboxPrompt.ts
// ("Output each file as a fenced block prefixed with its path").
//
// The native engine (engine.ts → llama.rn) is injected as a `complete`
// function so this file is PURE + RN-free and unit-tests under tsx. The RN
// factory (codingBackendStore.ts) supplies a real complete() backed by a
// loaded GGUF; when the engine isn't linked, the backend is simply marked
// unavailable and the user falls back to a cloud key.

import {
  assertRequestSize,
  type EditFilesRequest,
  type EditPlan,
  type FileEdit,
  type LlmProvider,
} from "./llmClient";
import { buildSandboxPrompt, inferSandboxMode, type SandboxContext } from "./localAgent/sandboxPrompt";

/** The injected on-device completion fn (wraps engine.ts LoadedModel.complete). */
export type LocalComplete = (prompt: string, opts?: { maxTokens?: number }) => Promise<string>;

export interface LocalProviderOptions {
  /** Stable id used by the picker (the GGUF model id). */
  modelId: string;
  /** The native completion fn. */
  complete: LocalComplete;
  /** Path of the file the user currently has open, so it's the edit target. */
  openPath?: string;
  /** Stack hints for the prompt header ("typescript","react-native",…). */
  stack?: string[];
  maxTokens?: number;
}

/** Build an LlmProvider backed by an on-device GGUF coder model. */
export function createLocalProvider(opts: LocalProviderOptions): LlmProvider {
  return {
    id: "local",
    model: opts.modelId,

    async editFiles(req: EditFilesRequest): Promise<EditPlan> {
      assertRequestSize(req);

      const mode = inferSandboxMode(req.prompt);
      const open =
        req.files.find((f) => f.path === opts.openPath) ?? req.files[0] ?? undefined;
      const ctx: SandboxContext = {
        fileTree: req.files.map((f) => f.path),
        openFile: open ? { path: open.path, contents: open.content } : undefined,
        stack: opts.stack ?? (req.framework ? [req.framework] : undefined),
      };
      const prompt = buildSandboxPrompt(ctx, { instruction: req.prompt, mode });
      const text = await opts.complete(prompt, { maxTokens: opts.maxTokens });
      const { rationale, edits } = parseFencedEdits(text, req.files.map((f) => f.path));
      return { rationale, edits };
    },
  };
}

interface FencedBlock {
  /** Resolved relative path, or null when we couldn't find one. */
  path: string | null;
  body: string;
  /** char index in the source where this block started (for rationale slicing). */
  startIndex: number;
}

const FENCE_RE = /```([^\n`]*)\n([\s\S]*?)```/g;

/** Does a token look like a source-file path? (has a / or a .ext, no spaces) */
function looksLikePath(token: string): boolean {
  const t = token.trim().replace(/^`+|`+$/g, "").replace(/^[#*\-\s]+/, "");
  if (!t || /\s/.test(t)) return false;
  return t.includes("/") || /\.[A-Za-z0-9]+$/.test(t);
}

function cleanPathToken(token: string): string {
  return token
    .trim()
    .replace(/^`+|`+$/g, "") // surrounding backticks
    .replace(/^(file|path)\s*[:=]\s*/i, "") // "File: " / "path = "
    .replace(/^[#>*\-\s]+/, "") // markdown bullets / headers
    .replace(/[:：]\s*$/, "") // trailing colon
    .trim();
}

/**
 * Parse a small model's response into an EditPlan. Recognizes a file path in
 * any of three places, in priority order:
 *   1. the fence info string:           ```tsx path/to/X.tsx   (or just ```path)
 *   2. the last non-empty line before the opening fence (path on its own line)
 *   3. a leading `// path` / `--- path ---` comment inside the block
 * Blocks with no resolvable path are skipped. Prose before the first block
 * becomes the rationale.
 */
export function parseFencedEdits(
  text: string,
  knownPaths: string[] = [],
): { rationale: string; edits: FileEdit[] } {
  const known = new Set(knownPaths);
  const blocks: FencedBlock[] = [];
  FENCE_RE.lastIndex = 0;
  let m: RegExpExecArray | null;
  while ((m = FENCE_RE.exec(text)) !== null) {
    const info = m[1] ?? "";
    let body = m[2] ?? "";
    const startIndex = m.index;

    let path: string | null = null;

    // 1. path in the fence info string (after an optional language word).
    const infoTokens = info.trim().split(/\s+/).filter(Boolean);
    for (const tok of infoTokens) {
      if (looksLikePath(tok)) {
        path = cleanPathToken(tok);
        break;
      }
    }

    // 2. last non-empty line before the fence.
    if (!path) {
      const before = text.slice(0, startIndex).split("\n");
      for (let i = before.length - 1; i >= 0 && before.length - i <= 3; i--) {
        const line = before[i].trim();
        if (!line) continue;
        if (looksLikePath(line)) path = cleanPathToken(line);
        break; // only inspect the nearest non-empty line
      }
    }

    // 3. a leading path comment inside the block (and strip it from the body).
    if (!path) {
      const lines = body.split("\n");
      const first = (lines[0] ?? "").trim();
      const stripped = first.replace(/^(\/\/|#|<!--|---)\s*/, "").replace(/\s*(-->|---)\s*$/, "");
      if (looksLikePath(stripped)) {
        path = cleanPathToken(stripped);
        body = lines.slice(1).join("\n");
      }
    }

    blocks.push({ path, body: body.replace(/\n$/, ""), startIndex });
  }

  const usable = blocks.filter((b) => b.path);
  const edits: FileEdit[] = usable.map((b) => ({
    action: known.has(b.path!) ? "update" : "create",
    path: b.path!,
    content: b.body,
  }));

  // Rationale: text before the first block (any block, even unusable ones).
  const firstBlockAt = blocks.length ? blocks[0].startIndex : text.length;
  let rationale = text.slice(0, firstBlockAt).trim();
  if (!rationale && edits.length === 0) {
    rationale = text.trim() || "model returned no parseable edits";
  }
  return { rationale, edits };
}
