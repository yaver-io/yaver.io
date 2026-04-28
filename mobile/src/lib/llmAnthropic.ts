// llmAnthropic.ts — Anthropic Messages API implementation of the
// LlmProvider contract from llmClient.ts.
//
// Uses tool-use with a forced `apply_edits` tool to coerce the
// model into returning a structured EditPlan instead of free-form
// markdown. That gives us reliable JSON we can apply without a
// fragile parser.
//
// Auth: BYO Anthropic API key (`sk-ant-...`). The phone reads the
// key from expo-secure-store before calling — this file never
// looks at storage. Key in / EditPlan out.

import {
  assertRequestSize,
  type EditFilesRequest,
  type EditPlan,
  type FileEdit,
  type LlmProvider,
} from "./llmClient";

const ANTHROPIC_BASE = "https://api.anthropic.com/v1";
/** Anthropic requires this header to authorise the browser-style
 *  origin Yaver mobile app uses. Same header the existing agent
 *  side sends. */
const ANTHROPIC_VERSION = "2023-06-01";

const APPLY_EDITS_TOOL = {
  name: "apply_edits",
  description:
    "Apply a set of file edits to the project's src/ tree. Use this to create, update, or delete files in response to the user's request.",
  input_schema: {
    type: "object" as const,
    properties: {
      rationale: {
        type: "string" as const,
        description: "Plain-text reasoning shown to the user before they apply.",
      },
      edits: {
        type: "array" as const,
        items: {
          type: "object" as const,
          properties: {
            action: {
              type: "string" as const,
              enum: ["create", "update", "delete"] as const,
            },
            path: {
              type: "string" as const,
              description: "Posix-relative path inside src/. No leading slash, no '..'.",
            },
            content: {
              type: "string" as const,
              description: "Required for create/update. The full new file contents.",
            },
            reason: {
              type: "string" as const,
              description: "Per-edit rationale.",
            },
          },
          required: ["action", "path"] as const,
        },
      },
    },
    required: ["rationale", "edits"] as const,
  },
};

export interface AnthropicProviderOptions {
  apiKey: string;
  /** Override the model. Default: claude-opus-4-7 (per CLAUDE.md). */
  model?: string;
  /** For testing. Default: globalThis.fetch. */
  fetchImpl?: typeof fetch;
  /** Override the API base. Default: https://api.anthropic.com/v1.
   *  Tests point this at a local mock; production never uses it. */
  baseUrl?: string;
  /** Max output tokens. Default 4096 — enough for ~30 KB of edited
   *  files, which is plenty for Slice 3's small projects. */
  maxTokens?: number;
}

/** Build an LlmProvider backed by the Anthropic Messages API. */
export function createAnthropicProvider(opts: AnthropicProviderOptions): LlmProvider {
  if (!opts.apiKey) {
    throw new Error("createAnthropicProvider: apiKey is required (BYO Anthropic key).");
  }
  const f = opts.fetchImpl ?? fetch;
  const base = (opts.baseUrl ?? ANTHROPIC_BASE).replace(/\/+$/, "");
  const model = opts.model ?? "claude-opus-4-7";
  const maxTokens = opts.maxTokens ?? 4096;

  return {
    id: "anthropic",
    model,

    async editFiles(req: EditFilesRequest): Promise<EditPlan> {
      assertRequestSize(req);

      const systemPrompt = buildSystemPrompt(req);
      const userMessage = buildUserMessage(req);

      const ctrl = new AbortController();
      const timer = setTimeout(() => ctrl.abort(), req.timeoutMs ?? 60_000);
      let res: Response;
      try {
        res = await f(`${base}/messages`, {
          method: "POST",
          headers: {
            "Content-Type": "application/json",
            "x-api-key": opts.apiKey,
            "anthropic-version": ANTHROPIC_VERSION,
            // The phone is the user's own device; no proxy. dangerous-direct-browser-access
            // is the official escape hatch Anthropic documents for direct
            // mobile / browser calls.
            "anthropic-dangerous-direct-browser-access": "true",
          },
          body: JSON.stringify({
            model,
            max_tokens: maxTokens,
            system: systemPrompt,
            messages: [{ role: "user", content: userMessage }],
            tools: [APPLY_EDITS_TOOL],
            tool_choice: { type: "tool", name: "apply_edits" },
          }),
          signal: ctrl.signal,
        });
      } finally {
        clearTimeout(timer);
      }

      if (!res.ok) {
        const text = await res.text().catch(() => "");
        throw new Error(`Anthropic ${res.status}: ${text.slice(0, 400)}`);
      }
      const body = (await res.json()) as AnthropicResponse;
      return parseAnthropicResponse(body);
    },
  };
}

// ---- Prompt construction --------------------------------------------

function buildSystemPrompt(req: EditFilesRequest): string {
  const lines: string[] = [
    "You are an AI coding assistant editing a phone-authored project.",
    "The user is on their phone — there is no desktop, no shell, no terminal.",
    "Every change you make goes directly into a sandboxed src/ tree on the device.",
    "",
    "Hard rules:",
    "  1. ALWAYS respond by calling the apply_edits tool. No prose outside it.",
    "  2. Path safety: every edit's `path` must be a posix-relative path inside src/.",
    "     No leading slash, no '..' segments, no absolute paths.",
    "  3. For create/update: include the FULL new file contents in `content`.",
    "     The phone doesn't apply diffs — it overwrites the file with what you give it.",
    "  4. Be conservative. If the user's prompt is ambiguous, pick the smallest reasonable",
    "     interpretation and explain in `rationale`. Don't rewrite unrelated files.",
    "  5. Don't introduce new dependencies (package.json edits) unless the user asks.",
  ];
  if (req.framework) {
    lines.push("", `Framework: ${req.framework}`);
  }
  if (req.schema?.tables?.length) {
    lines.push("", "Backend schema (CRUD endpoints already exist for these tables):");
    for (const table of req.schema.tables) {
      const cols = (table.columns ?? []).map((c) => `${c.name}:${c.type}`).join(", ");
      lines.push(`  ${table.name}(${cols})`);
    }
  }
  return lines.join("\n");
}

function buildUserMessage(req: EditFilesRequest): string {
  const parts: string[] = [];
  parts.push(`Request:\n${req.prompt.trim()}`);
  if (req.files.length === 0) {
    parts.push(
      "\nThe project's src/ tree is empty. Create the files needed to satisfy the request.",
    );
  } else {
    parts.push("\nCurrent src/ tree:");
    for (const f of req.files) {
      parts.push(`\n--- ${f.path} ---\n${f.content}`);
    }
  }
  return parts.join("\n");
}

// ---- Response parsing -----------------------------------------------

interface AnthropicContentText {
  type: "text";
  text: string;
}

interface AnthropicContentToolUse {
  type: "tool_use";
  name: string;
  input: unknown;
  id: string;
}

type AnthropicContent = AnthropicContentText | AnthropicContentToolUse;

interface AnthropicResponse {
  id: string;
  model: string;
  content: AnthropicContent[];
  usage?: { input_tokens?: number; output_tokens?: number };
  stop_reason?: string;
}

function parseAnthropicResponse(body: AnthropicResponse): EditPlan {
  const toolUse = body.content.find(
    (c): c is AnthropicContentToolUse => c.type === "tool_use" && c.name === "apply_edits",
  );
  if (!toolUse) {
    // No tool call. Surface what the model said as the rationale so
    // the UI can display *something* instead of throwing.
    const textBlock = body.content.find(
      (c): c is AnthropicContentText => c.type === "text",
    );
    return {
      rationale: textBlock?.text ?? "model returned no edits",
      edits: [],
      inputTokens: body.usage?.input_tokens,
      outputTokens: body.usage?.output_tokens,
      debug: body,
    };
  }
  const input = toolUse.input as { rationale?: unknown; edits?: unknown };
  const rationale = typeof input.rationale === "string" ? input.rationale : "";
  const edits: FileEdit[] = Array.isArray(input.edits)
    ? input.edits.map(coerceEdit).filter((x): x is FileEdit => x !== null)
    : [];
  return {
    rationale,
    edits,
    inputTokens: body.usage?.input_tokens,
    outputTokens: body.usage?.output_tokens,
    debug: body,
  };
}

function coerceEdit(raw: unknown): FileEdit | null {
  if (!raw || typeof raw !== "object") return null;
  const r = raw as { action?: unknown; path?: unknown; content?: unknown; reason?: unknown };
  if (r.action !== "create" && r.action !== "update" && r.action !== "delete") return null;
  if (typeof r.path !== "string" || !r.path) return null;
  return {
    action: r.action,
    path: r.path,
    content: typeof r.content === "string" ? r.content : undefined,
    reason: typeof r.reason === "string" ? r.reason : undefined,
  };
}
