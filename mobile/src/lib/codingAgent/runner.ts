// codingAgent/runner.ts — provider-agnostic AGENTIC coding loop for the phone
// Mobile Sandbox. PURE-ish: no expo/SecureStore/fetch import (fetchImpl is
// injected), so the whole loop is tsx-tested. RN wiring (key lookup, the real
// CodingSandbox binding) lives in sandboxBinding.ts.
//
// This is the upgrade from single-shot EditPlan to the opencode-style loop
// (docs/agentic-coding-sandbox.md). It is a fork of yaverAgentRunner.ts's
// tool-use loop — same two transports (Anthropic native + OpenAI-compatible),
// but generic over a tool registry and wired to the CODING tools
// (sandboxTools.ts) instead of the coding-forbidden control-plane tools.
//
// Default provider is GLM (glm-4.6 @ z.ai): cheapest capable BYO key, and the
// agentic read-only-what-you-need pattern is where GLM's price advantage
// compounds (see the design doc's optimization section).

import {
  CODING_TOOLS,
  dispatchCodingTool,
  type CodingSandbox,
  type CodingTool,
} from "./sandboxTools";

// ── Config ────────────────────────────────────────────────────────────

export type CodingProvider = "glm" | "openai" | "openrouter" | "anthropic";

export interface CodingAgentConfig {
  provider: CodingProvider;
  model: string;
  apiKey: string;
  /** Override the API base. Defaults per provider. */
  baseUrl?: string;
}

const PROVIDER_DEFAULTS: Record<CodingProvider, { baseUrl: string; model: string }> = {
  // GLM Coding Plan endpoint (the cheap flat-rate subscription). NOTE: this is
  // the /coding/ base, NOT the general /paas/v4 — a Coding-Plan key 429s
  // ("Insufficient balance") on the general endpoint, which is what the
  // single-shot GLM provider (llmOpenAI.ts) still uses. Pay-as-you-go users can
  // override baseUrl to https://api.z.ai/api/paas/v4.
  glm: { baseUrl: "https://api.z.ai/api/coding/paas/v4", model: "glm-4.6" },
  openai: { baseUrl: "https://api.openai.com/v1", model: "gpt-4.1" },
  openrouter: { baseUrl: "https://openrouter.ai/api/v1", model: "z-ai/glm-4.6" },
  anthropic: { baseUrl: "https://api.anthropic.com/v1", model: "claude-opus-4-7" },
};

/** GLM-by-default config from a bare API key — the cheap path the design doc
 *  recommends as the standalone default. */
export function defaultCodingAgentConfig(apiKey: string): CodingAgentConfig {
  return { provider: "glm", model: PROVIDER_DEFAULTS.glm.model, apiKey, baseUrl: PROVIDER_DEFAULTS.glm.baseUrl };
}

// ── System prompt (opencode discipline, distilled) ─────────────────────

export const CODING_AGENT_SYSTEM_PROMPT = `You are Yaver's coding agent, editing a project's src/ tree directly on the user's phone. There is no desktop, no shell on this device — you act ONLY through the tools provided.

Work in a loop:
1. Understand the request, then list_files / grep / read_file to learn the relevant code. Do NOT guess file contents — read before you edit.
2. Make the SMALLEST change that satisfies the request. Prefer edit_file (anchored replace) over write_file (whole-file overwrite). The 'old' anchor must match the current file verbatim, so read_file immediately before editing.
3. Only create files the request needs. Don't refactor unrelated code or add dependencies (package.json) unless asked.
4. When the change is complete, STOP calling tools and reply with a short plain-text summary of what you changed and why. No markdown headings, no code fences.

Path rules: every path is posix-relative inside src/ — no leading slash, no '..'.
If a tool returns an error, read the message and adjust (e.g. re-read the file if an anchor didn't match). Don't repeat a failing call unchanged.
Be concise. Don't narrate every step; act.`;

// ── Provider tool-schema translation ───────────────────────────────────

function toAnthropicTools(tools: CodingTool[]) {
  return tools.map((t) => ({ name: t.name, description: t.description, input_schema: t.parameters }));
}

function toOpenAITools(tools: CodingTool[]) {
  return tools.map((t) => ({
    type: "function" as const,
    function: { name: t.name, description: t.description, parameters: t.parameters },
  }));
}

// ── Run-loop public types ──────────────────────────────────────────────

export interface CodingToolCall {
  name: string;
  args: unknown;
  result: unknown;
  /** True for write/edit/delete (a tree mutation actually applied). */
  mutating: boolean;
  error?: string;
  /** Set when a confirmMutation hook denied the call (result is the denial). */
  denied?: boolean;
}

export type CodingAgentProgress =
  | { kind: "model_text"; text: string }
  | { kind: "tool_call"; call: CodingToolCall }
  | { kind: "step_complete"; step: number };

export interface CodingAgentResult {
  finalText: string;
  toolCalls: CodingToolCall[];
  /** Paths that were created/overwritten/deleted (mutating, non-denied). */
  mutatedPaths: string[];
  steps: number;
  inputTokens: number;
  outputTokens: number;
  /** True when the loop hit maxSteps without the model stopping on its own. */
  hitMaxSteps: boolean;
}

export interface RunCodingAgentOptions {
  prompt: string;
  /** Slug-scoped sandbox the tools act on. */
  sandbox: CodingSandbox;
  config: CodingAgentConfig;
  /** Cap on tool-use rounds. Coding needs more than control-plane; default 16. */
  maxSteps?: number;
  /** Per-request abort timeout, ms. Default 90 s (coding turns are longer). */
  timeoutMs?: number;
  /** External cancellation — aborts the in-flight fetch and stops the loop. */
  signal?: AbortSignal;
  /** Override the tool set (tests / future LSP tools). Default CODING_TOOLS. */
  tools?: CodingTool[];
  /**
   * Human-in-the-loop gate. Called BEFORE any mutating tool runs. Return false
   * to refuse the write (the model is told the user rejected it and can adjust).
   * Omit for yolo mode (all mutations auto-applied) — matches the user's
   * always-dangerous runner preference, but the sandbox UI can wire a preview.
   */
  confirmMutation?: (call: { name: string; args: unknown }) => Promise<boolean> | boolean;
  onProgress?: (e: CodingAgentProgress) => void;
  /** Test seam — defaults to globalThis.fetch. */
  fetchImpl?: typeof fetch;
}

// ── Entry point ────────────────────────────────────────────────────────

export async function runCodingAgent(opts: RunCodingAgentOptions): Promise<CodingAgentResult> {
  if (!opts.config?.apiKey) {
    throw new Error("coding agent not configured — add a provider API key (GLM recommended).");
  }
  return opts.config.provider === "anthropic" ? runAnthropic(opts) : runOpenAICompatible(opts);
}

// ── Shared mutation execution ──────────────────────────────────────────
//
// One place decides: is this tool mutating? should we ask? apply or refuse,
// and record the path. Both transports funnel every tool through here so the
// gate + mutatedPaths accounting can't drift between providers.

interface ExecState {
  toolCalls: CodingToolCall[];
  mutatedPaths: string[];
}

async function execTool(
  name: string,
  args: unknown,
  opts: RunCodingAgentOptions,
  tools: CodingTool[],
  state: ExecState,
): Promise<{ resultJson: string; isError: boolean }> {
  const def = tools.find((t) => t.name === name);
  const mutating = !!def?.mutating;
  const call: CodingToolCall = { name, args, result: null, mutating };

  // Gate mutations through the confirm hook (yolo when absent).
  if (mutating && opts.confirmMutation) {
    let ok = true;
    try {
      ok = await opts.confirmMutation({ name, args });
    } catch {
      ok = false;
    }
    if (!ok) {
      call.denied = true;
      call.result = { error: "user rejected this change" };
      state.toolCalls.push(call);
      opts.onProgress?.({ kind: "tool_call", call });
      return { resultJson: JSON.stringify(call.result), isError: true };
    }
  }

  try {
    const result = await dispatchCodingTool(name, args, opts.sandbox);
    call.result = result;
    // A tool may apply nothing and return {error} (e.g. anchor not found) — only
    // count a real mutation toward mutatedPaths when the tool reported ok.
    if (mutating && result && typeof result === "object" && (result as any).ok) {
      const p = (result as any).path;
      if (typeof p === "string" && !state.mutatedPaths.includes(p)) state.mutatedPaths.push(p);
    }
    state.toolCalls.push(call);
    opts.onProgress?.({ kind: "tool_call", call });
    const isError = !!(result && typeof result === "object" && (result as any).error);
    return { resultJson: JSON.stringify(result), isError };
  } catch (e) {
    call.error = e instanceof Error ? e.message : String(e);
    call.result = { error: call.error };
    state.toolCalls.push(call);
    opts.onProgress?.({ kind: "tool_call", call });
    return { resultJson: JSON.stringify(call.result), isError: true };
  }
}

// ── OpenAI-compatible transport (GLM / OpenAI / OpenRouter) ─────────────

interface OAIToolCall {
  id: string;
  type?: "function";
  function: { name: string; arguments: string };
}
interface OAIMessage {
  role: "system" | "user" | "assistant" | "tool";
  content?: string | null;
  tool_calls?: OAIToolCall[];
  tool_call_id?: string;
}
interface OAIResponse {
  choices?: Array<{ finish_reason?: string; message: OAIMessage }>;
  usage?: { prompt_tokens?: number; completion_tokens?: number };
}

async function runOpenAICompatible(opts: RunCodingAgentOptions): Promise<CodingAgentResult> {
  const f = opts.fetchImpl ?? fetch;
  const tools = opts.tools ?? CODING_TOOLS;
  const cfg = opts.config;
  const base = (cfg.baseUrl?.trim() || PROVIDER_DEFAULTS[cfg.provider].baseUrl).replace(/\/+$/, "");

  const messages: OAIMessage[] = [
    { role: "system", content: CODING_AGENT_SYSTEM_PROMPT },
    { role: "user", content: opts.prompt },
  ];
  const oaiTools = toOpenAITools(tools);
  const state: ExecState = { toolCalls: [], mutatedPaths: [] };
  let inputTokens = 0;
  let outputTokens = 0;
  let finalText = "";

  const maxSteps = opts.maxSteps ?? 16;
  let stepsRun = 0;
  for (let step = 0; step < maxSteps; step++) {
    stepsRun = step + 1;
    if (opts.signal?.aborted) throw abortError();
    const sig = combineSignals(opts.signal, opts.timeoutMs ?? 90_000);
    let raw: OAIResponse;
    try {
      const res = await f(`${base}/chat/completions`, {
        method: "POST",
        signal: sig.signal,
        headers: { "Content-Type": "application/json", Authorization: `Bearer ${cfg.apiKey}` },
        body: JSON.stringify({ model: cfg.model, messages, tools: oaiTools, tool_choice: "auto" }),
      });
      if (!res.ok) {
        const body = await res.text().catch(() => "");
        throw new Error(`${cfg.provider} ${res.status}: ${truncateForError(body)}`);
      }
      raw = (await res.json()) as OAIResponse;
    } finally {
      sig.dispose();
    }

    inputTokens += raw.usage?.prompt_tokens ?? 0;
    outputTokens += raw.usage?.completion_tokens ?? 0;

    const choice = raw.choices?.[0];
    if (!choice) throw new Error(`${cfg.provider} returned no choices`);
    const msg = choice.message;
    if (typeof msg.content === "string" && msg.content) {
      finalText = msg.content;
      opts.onProgress?.({ kind: "model_text", text: msg.content });
    }
    messages.push(msg);

    const calls = msg.tool_calls ?? [];
    // Robustness for GLM: decide purely on whether tool_calls are present, not
    // on finish_reason (GLM sometimes returns tool calls with a non-"tool_calls"
    // reason). No calls → the model is done.
    if (calls.length === 0) {
      opts.onProgress?.({ kind: "step_complete", step });
      return done(false);
    }

    for (const tc of calls) {
      if (opts.signal?.aborted) throw abortError();
      const args = safeJSONParse(tc.function.arguments);
      const { resultJson } = await execTool(tc.function.name, args, opts, tools, state);
      messages.push({ role: "tool", tool_call_id: tc.id, content: resultJson });
    }
    opts.onProgress?.({ kind: "step_complete", step });
  }
  return done(true);

  function done(hitMax: boolean): CodingAgentResult {
    return {
      finalText: finalText || (hitMax ? "(coding agent stopped after max steps)" : ""),
      toolCalls: state.toolCalls,
      mutatedPaths: state.mutatedPaths,
      steps: stepsRun,
      inputTokens,
      outputTokens,
      hitMaxSteps: hitMax,
    };
  }
}

// ── Anthropic native transport ─────────────────────────────────────────

interface AntBlock {
  type: "text" | "tool_use" | "tool_result";
  text?: string;
  id?: string;
  name?: string;
  input?: unknown;
  tool_use_id?: string;
  content?: unknown;
  is_error?: boolean;
}
interface AntMessage {
  role: "user" | "assistant";
  content: AntBlock[];
}
interface AntResponse {
  stop_reason: "end_turn" | "tool_use" | "max_tokens" | "stop_sequence";
  content: AntBlock[];
  usage?: { input_tokens?: number; output_tokens?: number };
}

const ANTHROPIC_VERSION = "2023-06-01";

async function runAnthropic(opts: RunCodingAgentOptions): Promise<CodingAgentResult> {
  const f = opts.fetchImpl ?? fetch;
  const tools = opts.tools ?? CODING_TOOLS;
  const cfg = opts.config;
  const base = (cfg.baseUrl?.trim() || PROVIDER_DEFAULTS.anthropic.baseUrl).replace(/\/+$/, "");

  const antTools = toAnthropicTools(tools);
  const messages: AntMessage[] = [{ role: "user", content: [{ type: "text", text: opts.prompt }] }];
  const state: ExecState = { toolCalls: [], mutatedPaths: [] };
  let inputTokens = 0;
  let outputTokens = 0;
  let finalText = "";

  const maxSteps = opts.maxSteps ?? 16;
  let stepsRun = 0;
  for (let step = 0; step < maxSteps; step++) {
    stepsRun = step + 1;
    if (opts.signal?.aborted) throw abortError();
    const sig = combineSignals(opts.signal, opts.timeoutMs ?? 90_000);
    let raw: AntResponse;
    try {
      const res = await f(`${base}/messages`, {
        method: "POST",
        signal: sig.signal,
        headers: {
          "Content-Type": "application/json",
          "x-api-key": cfg.apiKey,
          "anthropic-version": ANTHROPIC_VERSION,
          "anthropic-dangerous-direct-browser-access": "true",
        },
        body: JSON.stringify({
          model: cfg.model,
          max_tokens: 8192,
          system: CODING_AGENT_SYSTEM_PROMPT,
          tools: antTools,
          messages,
        }),
      });
      if (!res.ok) {
        const body = await res.text().catch(() => "");
        throw new Error(`anthropic ${res.status}: ${truncateForError(body)}`);
      }
      raw = (await res.json()) as AntResponse;
    } finally {
      sig.dispose();
    }

    inputTokens += raw.usage?.input_tokens ?? 0;
    outputTokens += raw.usage?.output_tokens ?? 0;

    const assistantBlocks: AntBlock[] = [];
    const toolUses: AntBlock[] = [];
    for (const block of raw.content) {
      assistantBlocks.push(block);
      if (block.type === "text" && typeof block.text === "string") {
        finalText = block.text;
        opts.onProgress?.({ kind: "model_text", text: block.text });
      } else if (block.type === "tool_use") {
        toolUses.push(block);
      }
    }
    messages.push({ role: "assistant", content: assistantBlocks });

    if (raw.stop_reason !== "tool_use" || toolUses.length === 0) {
      opts.onProgress?.({ kind: "step_complete", step });
      return result(false);
    }

    const resultBlocks: AntBlock[] = [];
    for (const tu of toolUses) {
      if (opts.signal?.aborted) throw abortError();
      const { resultJson, isError } = await execTool(tu.name ?? "", tu.input, opts, tools, state);
      resultBlocks.push({
        type: "tool_result",
        tool_use_id: tu.id,
        content: resultJson,
        is_error: isError || undefined,
      });
    }
    messages.push({ role: "user", content: resultBlocks });
    opts.onProgress?.({ kind: "step_complete", step });
  }
  return result(true);

  function result(hitMax: boolean): CodingAgentResult {
    return {
      finalText: finalText || (hitMax ? "(coding agent stopped after max steps)" : ""),
      toolCalls: state.toolCalls,
      mutatedPaths: state.mutatedPaths,
      steps: stepsRun,
      inputTokens,
      outputTokens,
      hitMaxSteps: hitMax,
    };
  }
}

// ── Helpers (shared with yaverAgentRunner's pattern) ───────────────────

function truncateForError(s: string): string {
  return s.length <= 240 ? s : s.slice(0, 240) + "…";
}

function safeJSONParse(s: string): unknown {
  try {
    return JSON.parse(s);
  } catch {
    return {};
  }
}

function abortError(): Error {
  const e = new Error("coding agent aborted");
  e.name = "AbortError";
  return e;
}

function combineSignals(
  external: AbortSignal | undefined,
  timeoutMs: number,
): { signal: AbortSignal; dispose: () => void } {
  const ctrl = new AbortController();
  const timer = setTimeout(() => ctrl.abort(), timeoutMs);
  const onExternal = () => ctrl.abort();
  if (external) {
    if (external.aborted) ctrl.abort();
    else external.addEventListener("abort", onExternal);
  }
  return {
    signal: ctrl.signal,
    dispose: () => {
      clearTimeout(timer);
      external?.removeEventListener("abort", onExternal);
    },
  };
}
