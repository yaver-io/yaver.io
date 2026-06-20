// llmOpenAI.ts — OpenAI / GLM (Zhipu) implementation of the LlmProvider
// contract from llmClient.ts. Sibling to llmAnthropic.ts.
//
// Both OpenAI and GLM speak the same OpenAI-style /chat/completions API with
// function/tool calling, so one provider factory covers both — only the base
// URL + default model differ. We force a single `apply_edits` tool so the model
// returns a structured EditPlan (JSON in tool_call arguments) instead of
// fragile fenced markdown.
//
// Auth: BYO key read from expo-secure-store by the caller (codingBackendStore).
// This file never touches storage — key in / EditPlan out. fetchImpl is
// injectable so the same code path runs in production RN and tsx tests.

import {
  assertRequestSize,
  type EditFilesRequest,
  type EditPlan,
  type FileEdit,
  type LlmProvider,
} from "./llmClient";

export type OpenAiFlavor = "openai" | "glm";

const FLAVOR_DEFAULTS: Record<OpenAiFlavor, { baseUrl: string; model: string; label: string }> = {
  openai: {
    baseUrl: "https://api.openai.com/v1",
    model: "gpt-4.1-mini",
    label: "OpenAI",
  },
  glm: {
    // GLM Coding Plan endpoint (the cheap flat-rate subscription). A Coding-Plan
    // key 429s ("Insufficient balance") on the general /api/paas/v4 endpoint;
    // it's provisioned for /api/coding/paas/v4 instead. Pay-as-you-go (prepaid
    // balance) keys also work here. Matches codingAgent/runner.ts.
    baseUrl: "https://api.z.ai/api/coding/paas/v4",
    model: "glm-4.7",
    label: "GLM",
  },
};

const APPLY_EDITS_TOOL = {
  type: "function" as const,
  function: {
    name: "apply_edits",
    description:
      "Apply a set of file edits to the project's src/ tree. Create, update, or delete files to satisfy the user's request.",
    parameters: {
      type: "object",
      properties: {
        rationale: {
          type: "string",
          description: "Plain-text reasoning shown to the user before they apply.",
        },
        edits: {
          type: "array",
          items: {
            type: "object",
            properties: {
              action: { type: "string", enum: ["create", "update", "delete"] },
              path: {
                type: "string",
                description: "Posix-relative path inside src/. No leading slash, no '..'.",
              },
              content: {
                type: "string",
                description: "Required for create/update. The FULL new file contents.",
              },
              reason: { type: "string", description: "Per-edit rationale." },
            },
            required: ["action", "path"],
          },
        },
      },
      required: ["rationale", "edits"],
    },
  },
};

export interface OpenAiProviderOptions {
  flavor: OpenAiFlavor;
  apiKey: string;
  /** Override the model (defaults per flavor). */
  model?: string;
  /** Override the base URL (tests point this at a local mock). */
  baseUrl?: string;
  /** For testing. Default: globalThis.fetch. */
  fetchImpl?: typeof fetch;
  maxTokens?: number;
}

/** Build an LlmProvider backed by an OpenAI-compatible /chat/completions API. */
export function createOpenAiProvider(opts: OpenAiProviderOptions): LlmProvider {
  if (!opts.apiKey) {
    throw new Error("createOpenAiProvider: apiKey is required (BYO key).");
  }
  const defaults = FLAVOR_DEFAULTS[opts.flavor];
  const f = opts.fetchImpl ?? fetch;
  const base = (opts.baseUrl ?? defaults.baseUrl).replace(/\/+$/, "");
  const model = opts.model ?? defaults.model;
  const maxTokens = opts.maxTokens ?? 4096;

  return {
    id: opts.flavor,
    model,

    async editFiles(req: EditFilesRequest): Promise<EditPlan> {
      assertRequestSize(req);

      const ctrl = new AbortController();
      const timer = setTimeout(() => ctrl.abort(), req.timeoutMs ?? 60_000);
      let res: Response;
      try {
        res = await f(`${base}/chat/completions`, {
          method: "POST",
          headers: {
            "Content-Type": "application/json",
            Authorization: `Bearer ${opts.apiKey}`,
          },
          body: JSON.stringify({
            model,
            max_tokens: maxTokens,
            temperature: 0.2,
            tools: [APPLY_EDITS_TOOL],
            tool_choice: { type: "function", function: { name: "apply_edits" } },
            messages: [
              { role: "system", content: buildSystemPrompt(req) },
              { role: "user", content: buildUserMessage(req) },
            ],
          }),
          signal: ctrl.signal,
        });
      } finally {
        clearTimeout(timer);
      }

      if (!res.ok) {
        const text = await res.text().catch(() => "");
        throw new Error(`${defaults.label} ${res.status}: ${text.slice(0, 400)}`);
      }
      const body = (await res.json()) as ChatCompletionResponse;
      return parseChatCompletion(body);
    },
  };
}

// ---- Prompt construction (shared shape with llmAnthropic) -----------

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
    "     The phone overwrites the file with what you give it (no diffs).",
    "  4. Be conservative. Smallest reasonable change; don't rewrite unrelated files.",
    "  5. Don't introduce new dependencies unless the user asks.",
  ];
  if (req.framework) lines.push("", `Framework: ${req.framework}`);
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
  const parts: string[] = [`Request:\n${req.prompt.trim()}`];
  if (req.files.length === 0) {
    parts.push("\nThe project's src/ tree is empty. Create the files needed to satisfy the request.");
  } else {
    parts.push("\nCurrent src/ tree:");
    for (const file of req.files) parts.push(`\n--- ${file.path} ---\n${file.content}`);
  }
  return parts.join("\n");
}

// ---- Response parsing -----------------------------------------------

interface ChatCompletionResponse {
  choices?: Array<{
    message?: {
      content?: string | null;
      tool_calls?: Array<{ function?: { name?: string; arguments?: string } }>;
    };
  }>;
  usage?: { prompt_tokens?: number; completion_tokens?: number };
}

/** Parse an OpenAI-style completion into an EditPlan. Prefers the forced
 *  tool_call; falls back to parsing JSON out of message content. */
export function parseChatCompletion(body: ChatCompletionResponse): EditPlan {
  const msg = body.choices?.[0]?.message;
  const usage = {
    inputTokens: body.usage?.prompt_tokens,
    outputTokens: body.usage?.completion_tokens,
  };

  const call = msg?.tool_calls?.find((c) => c.function?.name === "apply_edits");
  const argStr = call?.function?.arguments;
  if (argStr) {
    const parsed = safeJson(argStr);
    if (parsed) return { ...coercePlan(parsed), ...usage, debug: body };
  }

  // Fallback: some GLM responses put the JSON in content instead of tool_calls.
  if (typeof msg?.content === "string" && msg.content.trim()) {
    const parsed = safeJson(extractJsonObject(msg.content));
    if (parsed) return { ...coercePlan(parsed), ...usage, debug: body };
    return { rationale: msg.content.trim(), edits: [], ...usage, debug: body };
  }

  return { rationale: "model returned no edits", edits: [], ...usage, debug: body };
}

function coercePlan(raw: unknown): { rationale: string; edits: FileEdit[] } {
  const r = (raw ?? {}) as { rationale?: unknown; edits?: unknown };
  const rationale = typeof r.rationale === "string" ? r.rationale : "";
  const edits = Array.isArray(r.edits)
    ? r.edits.map(coerceEdit).filter((x): x is FileEdit => x !== null)
    : [];
  return { rationale, edits };
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

function safeJson(s: string): unknown | null {
  try {
    return JSON.parse(s);
  } catch {
    return null;
  }
}

/** Pull the first balanced {...} object out of a string (handles models that
 *  wrap JSON in prose or ```json fences). Returns "{}" when none found. */
export function extractJsonObject(text: string): string {
  const start = text.indexOf("{");
  if (start < 0) return "{}";
  let depth = 0;
  let inStr = false;
  let esc = false;
  for (let i = start; i < text.length; i++) {
    const ch = text[i];
    if (inStr) {
      if (esc) esc = false;
      else if (ch === "\\") esc = true;
      else if (ch === '"') inStr = false;
      continue;
    }
    if (ch === '"') inStr = true;
    else if (ch === "{") depth++;
    else if (ch === "}") {
      depth--;
      if (depth === 0) return text.slice(start, i + 1);
    }
  }
  return "{}";
}

/** Lightweight connectivity check ("pong") for a provider — a 1-token
 *  /chat/completions call. Used by the Mobile Sandbox create flow to show a
 *  live "Connecting to GLM → ✓" checklist step so setup feels real. Returns
 *  true on a 2xx, false on any error/timeout. Never throws. */
export async function pingProvider(opts: {
  flavor: OpenAiFlavor;
  apiKey: string;
  fetchImpl?: typeof fetch;
  timeoutMs?: number;
}): Promise<boolean> {
  if (!opts.apiKey) return false;
  const d = FLAVOR_DEFAULTS[opts.flavor];
  const f = opts.fetchImpl ?? fetch;
  const ctrl = new AbortController();
  const to = setTimeout(() => ctrl.abort(), opts.timeoutMs ?? 12000);
  try {
    const res = await f(`${d.baseUrl.replace(/\/+$/, "")}/chat/completions`, {
      method: "POST",
      headers: { "Content-Type": "application/json", Authorization: `Bearer ${opts.apiKey}` },
      body: JSON.stringify({ model: d.model, max_tokens: 1, messages: [{ role: "user", content: "ping" }] }),
      signal: ctrl.signal,
    });
    return res.ok;
  } catch {
    return false;
  } finally {
    clearTimeout(to);
  }
}
