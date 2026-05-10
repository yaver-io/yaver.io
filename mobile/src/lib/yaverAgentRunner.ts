/**
 * yaverAgentRunner — provider-agnostic tool-use loop for the embedded
 * yaver-agent. Reads provider / model / api-key from SecureStore, picks
 * the matching Anthropic or OpenAI-compatible adapter, and drives a
 * conversation against the YAVER_AGENT_TOOLS registry until the model
 * stops calling tools.
 *
 * Design constraints:
 *
 *   - Phone-side ONLY. Runs entirely on the mobile device — no agent
 *     host required. Provider call goes direct to Anthropic / GLM /
 *     OpenAI / OpenRouter.
 *   - BYOK. The user's API key never leaves the phone (well, except
 *     to the provider). Stored in expo-secure-store under a fixed
 *     name; mirrored from the YaverAgentSettings card.
 *   - Tools never carry secret values. Tool schemas use vault entry
 *     names / device aliases as handles. The system prompt explicitly
 *     forbids the LLM from echoing secrets in tool inputs or outputs.
 *
 * What this module does NOT do:
 *
 *   - It doesn't render UI. Callers wire onProgress to whatever they
 *     want (toast, sheet, log).
 *   - It doesn't decide whether to invoke yaver-agent vs the existing
 *     runner-driven task path. That's the Tasks-tab orchestration job.
 */

import * as SecureStore from "expo-secure-store";
import {
  YAVER_AGENT_TOOLS,
  dispatchYaverAgentTool,
  type YaverAgentTool,
  type YaverAgentToolContext,
} from "./yaverAgentTools";
import type { YaverAgentProviderId } from "./quic";

// ── Local persistence ─────────────────────────────────────────────────

const SECURE_STORE_KEY = "yaver_agent_provider_v1";

export interface YaverAgentLocalConfig {
  provider: YaverAgentProviderId;
  model: string;
  baseUrl?: string;
  apiKey: string;
}

/** Save the runtime config to the phone's SecureStore so the runner
 *  can fire even when no host device is connected. Mirrors the
 *  vault-side save the YaverAgentSettings card already does. */
export async function saveYaverAgentLocalConfig(cfg: YaverAgentLocalConfig): Promise<void> {
  await SecureStore.setItemAsync(SECURE_STORE_KEY, JSON.stringify(cfg));
}

export async function loadYaverAgentLocalConfig(): Promise<YaverAgentLocalConfig | null> {
  const raw = await SecureStore.getItemAsync(SECURE_STORE_KEY).catch(() => null);
  if (!raw) return null;
  try {
    const parsed = JSON.parse(raw);
    if (
      typeof parsed?.provider === "string" &&
      typeof parsed?.model === "string" &&
      typeof parsed?.apiKey === "string" &&
      parsed.apiKey.length > 0
    ) {
      return parsed as YaverAgentLocalConfig;
    }
  } catch {
    /* fall through */
  }
  return null;
}

export async function clearYaverAgentLocalConfig(): Promise<void> {
  await SecureStore.deleteItemAsync(SECURE_STORE_KEY).catch(() => {});
}

// ── System prompt ─────────────────────────────────────────────────────

/** The system prompt is intentionally tight: define identity, scope,
 *  forbid coding tasks, forbid leaking secrets. Provider-agnostic. */
export const YAVER_AGENT_SYSTEM_PROMPT = `You are Yaver Agent, an assistant embedded in the Yaver mobile app for control-plane tasks only.

In scope:
- Yaver-level device authentication (sign in, headless re-auth, primary management)
- Device + runner status checks (which devices are online, which runners are authed)
- Setting up runners on remote boxes (claude-code, codex, opencode)
- Vault read/write (creating entries, listing keys — never showing secret values back)
- Reading and explaining audit results

Out of scope — refuse politely and tell the user to connect a coding runner:
- Writing, editing, refactoring, or reviewing code
- Running builds, tests, or deploys
- Anything that involves opening a project in src/

Hard rules:
1. Use device.list whenever the user references a device by name. Match aliases case-insensitively.
2. Never put API keys, OAuth tokens, vault values, or other secrets in tool inputs OR your textual replies. Tools take handles (vault names, device ids), not raw values.
3. After device.audit, prefer to summarize using the recommendations array — call device.next_step if the user asked for a single suggestion.
4. When a recommendation has an "action" field, you may invoke that tool name directly (after confirming with the user for irreversible actions).
5. When the user's intent is ambiguous, ask one short clarifying question instead of guessing.

Reply concisely. Use plain text — no markdown headings, no code fences.`;

// ── Tool schema translation per provider format ───────────────────────

interface AnthropicToolSpec {
  name: string;
  description: string;
  input_schema: Record<string, unknown>;
}

interface OpenAIToolSpec {
  type: "function";
  function: {
    name: string;
    description: string;
    parameters: Record<string, unknown>;
  };
}

function toAnthropicTools(tools: YaverAgentTool[]): AnthropicToolSpec[] {
  return tools.map((t) => ({
    name: t.name,
    description: t.description,
    input_schema: t.parameters,
  }));
}

function toOpenAITools(tools: YaverAgentTool[]): OpenAIToolSpec[] {
  return tools.map((t) => ({
    type: "function",
    function: {
      name: t.name,
      description: t.description,
      parameters: t.parameters,
    },
  }));
}

// ── Run-loop result types ─────────────────────────────────────────────

export interface YaverAgentToolCall {
  name: string;
  args: unknown;
  result: unknown;
  error?: string;
}

export type YaverAgentProgressEvent =
  | { kind: "model_text"; text: string }
  | { kind: "tool_call"; call: YaverAgentToolCall }
  | { kind: "step_complete"; step: number };

export interface YaverAgentRunResult {
  finalText: string;
  toolCalls: YaverAgentToolCall[];
  steps: number;
  inputTokens?: number;
  outputTokens?: number;
}

/** Provider-agnostic conversation turn used for follow-up runs. The
 *  runner converts these to provider-specific messages (Anthropic
 *  blocks vs OpenAI messages) before sending. Tool-use details are NOT
 *  preserved across turns — the model gets plain text history plus a
 *  fresh tool-use loop for the new prompt. That's intentional: it keeps
 *  the persisted conversation provider-portable and avoids leaking
 *  intra-turn tool args back into context. */
export interface YaverAgentHistoryTurn {
  role: "user" | "assistant";
  text: string;
}

export interface RunYaverAgentOptions {
  /** Free-form user prompt. */
  prompt: string;
  /** Tool dispatcher context (devices, primary id, selectDevice). */
  ctx: YaverAgentToolContext;
  /** Optional override; defaults to SecureStore-cached config. */
  config?: YaverAgentLocalConfig;
  /** Prior conversation. Empty/omitted = first turn. The runner
   *  prepends these as `user`/`assistant` messages before the new
   *  prompt so follow-ups continue the same context. */
  history?: YaverAgentHistoryTurn[];
  /** Cap on tool-use rounds. Defaults to 6 — generous for control-plane
   *  flows, tight enough to surface infinite-loop bugs. */
  maxSteps?: number;
  /** Per-request abort timeout, ms. Default 45 s. */
  timeoutMs?: number;
  /** External cancellation. When this signal aborts, the runner
   *  cancels the in-flight fetch and stops looping. The thrown error
   *  bubbles up to the caller as `AbortError` so UI can mark the run
   *  stopped vs failed. */
  signal?: AbortSignal;
  /** Optional progress callback for UI streaming. */
  onProgress?: (event: YaverAgentProgressEvent) => void;
  /** Test seam — defaults to globalThis.fetch. */
  fetchImpl?: typeof fetch;
}

// ── Public entry point ────────────────────────────────────────────────

export async function runYaverAgent(opts: RunYaverAgentOptions): Promise<YaverAgentRunResult> {
  const cfg = opts.config ?? (await loadYaverAgentLocalConfig());
  if (!cfg) {
    throw new Error(
      "Yaver Agent is not configured yet — open Settings → Yaver Agent and save a provider + API key.",
    );
  }
  if (cfg.provider === "anthropic") {
    return runAnthropic(cfg, opts);
  }
  // glm, openai, openrouter all use the OpenAI-compatible chat-completions API.
  return runOpenAICompatible(cfg, opts);
}

// ── Anthropic native ──────────────────────────────────────────────────
//
// Tool-use loop: every assistant turn either ends with a `stop_reason` of
// "end_turn" (we're done) or "tool_use" (one or more tool_use blocks
// need to be executed and fed back via a `user` message containing
// tool_result blocks).

const ANTHROPIC_BASE = "https://api.anthropic.com/v1";
const ANTHROPIC_VERSION = "2023-06-01";

interface AnthropicContentBlock {
  type: "text" | "tool_use" | "tool_result";
  // text:
  text?: string;
  // tool_use:
  id?: string;
  name?: string;
  input?: unknown;
  // tool_result:
  tool_use_id?: string;
  content?: unknown;
  is_error?: boolean;
}

interface AnthropicMessage {
  role: "user" | "assistant";
  content: AnthropicContentBlock[];
}

interface AnthropicResponse {
  stop_reason: "end_turn" | "tool_use" | "max_tokens" | "stop_sequence";
  content: AnthropicContentBlock[];
  usage?: { input_tokens?: number; output_tokens?: number };
}

async function runAnthropic(
  cfg: YaverAgentLocalConfig,
  opts: RunYaverAgentOptions,
): Promise<YaverAgentRunResult> {
  const f = opts.fetchImpl ?? fetch;
  const base = (cfg.baseUrl?.trim() || ANTHROPIC_BASE).replace(/\/+$/, "");
  const tools = toAnthropicTools(YAVER_AGENT_TOOLS);
  const messages: AnthropicMessage[] = [];
  for (const turn of opts.history ?? []) {
    if (!turn.text.trim()) continue;
    messages.push({ role: turn.role, content: [{ type: "text", text: turn.text }] });
  }
  messages.push({ role: "user", content: [{ type: "text", text: opts.prompt }] });

  const toolCalls: YaverAgentToolCall[] = [];
  let inputTokens = 0;
  let outputTokens = 0;
  let finalText = "";

  const maxSteps = opts.maxSteps ?? 6;
  for (let step = 0; step < maxSteps; step++) {
    if (opts.signal?.aborted) throw abortError();
    const fetchSignal = combineSignals(opts.signal, opts.timeoutMs ?? 45_000);

    let raw: AnthropicResponse;
    try {
      const res = await f(`${base}/messages`, {
        method: "POST",
        signal: fetchSignal.signal,
        headers: {
          "Content-Type": "application/json",
          "x-api-key": cfg.apiKey,
          "anthropic-version": ANTHROPIC_VERSION,
          "anthropic-dangerous-direct-browser-access": "true",
        },
        body: JSON.stringify({
          model: cfg.model,
          max_tokens: 1024,
          system: YAVER_AGENT_SYSTEM_PROMPT,
          tools,
          messages,
        }),
      });
      if (!res.ok) {
        const body = await res.text().catch(() => "");
        throw new Error(`Anthropic ${res.status}: ${truncateForError(body)}`);
      }
      raw = (await res.json()) as AnthropicResponse;
    } finally {
      fetchSignal.dispose();
    }

    inputTokens += raw.usage?.input_tokens ?? 0;
    outputTokens += raw.usage?.output_tokens ?? 0;

    const assistantBlocks: AnthropicContentBlock[] = [];
    const toolUses: AnthropicContentBlock[] = [];
    for (const block of raw.content) {
      assistantBlocks.push(block);
      if (block.type === "text" && typeof block.text === "string") {
        finalText = block.text; // always keep latest assistant text
        opts.onProgress?.({ kind: "model_text", text: block.text });
      } else if (block.type === "tool_use") {
        toolUses.push(block);
      }
    }
    messages.push({ role: "assistant", content: assistantBlocks });

    if (raw.stop_reason !== "tool_use" || toolUses.length === 0) {
      opts.onProgress?.({ kind: "step_complete", step });
      return { finalText, toolCalls, steps: step + 1, inputTokens, outputTokens };
    }

    // Execute every tool_use block, then feed the results back.
    const resultBlocks: AnthropicContentBlock[] = [];
    for (const tu of toolUses) {
      if (opts.signal?.aborted) throw abortError();
      const call: YaverAgentToolCall = {
        name: tu.name ?? "",
        args: tu.input,
        result: null,
      };
      try {
        call.result = await dispatchYaverAgentTool(call.name, call.args, opts.ctx);
        resultBlocks.push({
          type: "tool_result",
          tool_use_id: tu.id,
          content: JSON.stringify(call.result),
        });
      } catch (e) {
        call.error = e instanceof Error ? e.message : String(e);
        resultBlocks.push({
          type: "tool_result",
          tool_use_id: tu.id,
          content: JSON.stringify({ error: call.error }),
          is_error: true,
        });
      }
      toolCalls.push(call);
      opts.onProgress?.({ kind: "tool_call", call });
    }
    messages.push({ role: "user", content: resultBlocks });
    opts.onProgress?.({ kind: "step_complete", step });
  }

  return {
    finalText: finalText || "(yaver-agent stopped after max steps)",
    toolCalls,
    steps: maxSteps,
    inputTokens,
    outputTokens,
  };
}

// ── OpenAI-compatible (GLM / OpenAI / OpenRouter) ────────────────────

interface OpenAIToolCall {
  id: string;
  type: "function";
  function: { name: string; arguments: string };
}

interface OpenAIMessage {
  role: "system" | "user" | "assistant" | "tool";
  content?: string | null;
  tool_calls?: OpenAIToolCall[];
  tool_call_id?: string;
  name?: string;
}

interface OpenAIResponse {
  choices: Array<{
    finish_reason: string;
    message: OpenAIMessage;
  }>;
  usage?: { prompt_tokens?: number; completion_tokens?: number };
}

const OPENAI_DEFAULT_BASE = "https://api.openai.com/v1";

async function runOpenAICompatible(
  cfg: YaverAgentLocalConfig,
  opts: RunYaverAgentOptions,
): Promise<YaverAgentRunResult> {
  const f = opts.fetchImpl ?? fetch;
  const base = (cfg.baseUrl?.trim() || OPENAI_DEFAULT_BASE).replace(/\/+$/, "");
  const tools = toOpenAITools(YAVER_AGENT_TOOLS);

  const messages: OpenAIMessage[] = [
    { role: "system", content: YAVER_AGENT_SYSTEM_PROMPT },
  ];
  for (const turn of opts.history ?? []) {
    if (!turn.text.trim()) continue;
    messages.push({ role: turn.role, content: turn.text });
  }
  messages.push({ role: "user", content: opts.prompt });

  const toolCalls: YaverAgentToolCall[] = [];
  let inputTokens = 0;
  let outputTokens = 0;
  let finalText = "";

  const maxSteps = opts.maxSteps ?? 6;
  for (let step = 0; step < maxSteps; step++) {
    if (opts.signal?.aborted) throw abortError();
    const fetchSignal = combineSignals(opts.signal, opts.timeoutMs ?? 45_000);

    let raw: OpenAIResponse;
    try {
      const res = await f(`${base}/chat/completions`, {
        method: "POST",
        signal: fetchSignal.signal,
        headers: {
          "Content-Type": "application/json",
          Authorization: `Bearer ${cfg.apiKey}`,
        },
        body: JSON.stringify({
          model: cfg.model,
          messages,
          tools,
          tool_choice: "auto",
        }),
      });
      if (!res.ok) {
        const body = await res.text().catch(() => "");
        throw new Error(`Provider ${res.status}: ${truncateForError(body)}`);
      }
      raw = (await res.json()) as OpenAIResponse;
    } finally {
      fetchSignal.dispose();
    }

    inputTokens += raw.usage?.prompt_tokens ?? 0;
    outputTokens += raw.usage?.completion_tokens ?? 0;

    const choice = raw.choices?.[0];
    if (!choice) {
      throw new Error("provider returned no choices");
    }
    const msg = choice.message;
    if (typeof msg.content === "string" && msg.content) {
      finalText = msg.content;
      opts.onProgress?.({ kind: "model_text", text: msg.content });
    }
    messages.push(msg);

    const calls = msg.tool_calls ?? [];
    if (choice.finish_reason !== "tool_calls" || calls.length === 0) {
      opts.onProgress?.({ kind: "step_complete", step });
      return { finalText, toolCalls, steps: step + 1, inputTokens, outputTokens };
    }

    for (const tc of calls) {
      if (opts.signal?.aborted) throw abortError();
      const args = safeJSONParse(tc.function.arguments);
      const call: YaverAgentToolCall = {
        name: tc.function.name,
        args,
        result: null,
      };
      try {
        call.result = await dispatchYaverAgentTool(call.name, args, opts.ctx);
        messages.push({
          role: "tool",
          tool_call_id: tc.id,
          content: JSON.stringify(call.result),
        });
      } catch (e) {
        call.error = e instanceof Error ? e.message : String(e);
        messages.push({
          role: "tool",
          tool_call_id: tc.id,
          content: JSON.stringify({ error: call.error }),
        });
      }
      toolCalls.push(call);
      opts.onProgress?.({ kind: "tool_call", call });
    }
    opts.onProgress?.({ kind: "step_complete", step });
  }

  return {
    finalText: finalText || "(yaver-agent stopped after max steps)",
    toolCalls,
    steps: maxSteps,
    inputTokens,
    outputTokens,
  };
}

// ── Helpers ───────────────────────────────────────────────────────────

function truncateForError(s: string): string {
  if (s.length <= 240) return s;
  return s.slice(0, 240) + "…";
}

function safeJSONParse(s: string): unknown {
  try {
    return JSON.parse(s);
  } catch {
    return {};
  }
}

/** AbortError mirrors what fetch throws on abort, so callers can reuse
 *  one `name === "AbortError"` check across fetch + tool-loop aborts. */
function abortError(): Error {
  const e = new Error("yaver-agent aborted");
  e.name = "AbortError";
  return e;
}

/** combineSignals returns a single AbortSignal that fires when EITHER
 *  the external signal aborts or the per-request timeout elapses.
 *  The dispose() call clears the timer + listener so we don't leak
 *  on early returns. */
function combineSignals(external: AbortSignal | undefined, timeoutMs: number): {
  signal: AbortSignal;
  dispose: () => void;
} {
  const ctrl = new AbortController();
  const timer = setTimeout(() => ctrl.abort(), timeoutMs);
  const onExternal = () => ctrl.abort();
  if (external) {
    if (external.aborted) {
      ctrl.abort();
    } else {
      external.addEventListener("abort", onExternal);
    }
  }
  return {
    signal: ctrl.signal,
    dispose: () => {
      clearTimeout(timer);
      external?.removeEventListener("abort", onExternal);
    },
  };
}

// ── Cheap connectivity test ──────────────────────────────────────────
//
// pingYaverAgent does NOT exercise the tool loop — it just sends one
// short user message with no tools. Used by the Settings card's "Test
// connection" button to validate provider + model + key without
// engaging the device list.

export interface YaverAgentPingResult {
  ok: boolean;
  message?: string;
  error?: string;
  latencyMs: number;
}

export async function pingYaverAgent(
  cfg?: YaverAgentLocalConfig,
  fetchImpl?: typeof fetch,
): Promise<YaverAgentPingResult> {
  const started = Date.now();
  try {
    const config = cfg ?? (await loadYaverAgentLocalConfig());
    if (!config) {
      return { ok: false, error: "Yaver Agent is not configured yet.", latencyMs: 0 };
    }
    const f = fetchImpl ?? fetch;
    if (config.provider === "anthropic") {
      const base = (config.baseUrl?.trim() || ANTHROPIC_BASE).replace(/\/+$/, "");
      const res = await f(`${base}/messages`, {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
          "x-api-key": config.apiKey,
          "anthropic-version": ANTHROPIC_VERSION,
          "anthropic-dangerous-direct-browser-access": "true",
        },
        body: JSON.stringify({
          model: config.model,
          max_tokens: 16,
          messages: [{ role: "user", content: "Reply with the single word: ok" }],
        }),
      });
      if (!res.ok) {
        const body = await res.text().catch(() => "");
        return { ok: false, error: `${res.status}: ${truncateForError(body)}`, latencyMs: Date.now() - started };
      }
      const json = (await res.json()) as AnthropicResponse;
      const text = json.content?.find((b) => b.type === "text")?.text ?? "";
      return { ok: true, message: text.trim(), latencyMs: Date.now() - started };
    }
    const base = (config.baseUrl?.trim() || OPENAI_DEFAULT_BASE).replace(/\/+$/, "");
    const res = await f(`${base}/chat/completions`, {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        Authorization: `Bearer ${config.apiKey}`,
      },
      body: JSON.stringify({
        model: config.model,
        max_tokens: 16,
        messages: [{ role: "user", content: "Reply with the single word: ok" }],
      }),
    });
    if (!res.ok) {
      const body = await res.text().catch(() => "");
      return { ok: false, error: `${res.status}: ${truncateForError(body)}`, latencyMs: Date.now() - started };
    }
    const json = (await res.json()) as OpenAIResponse;
    const text = json.choices?.[0]?.message?.content ?? "";
    return { ok: true, message: typeof text === "string" ? text.trim() : "", latencyMs: Date.now() - started };
  } catch (e) {
    return {
      ok: false,
      error: e instanceof Error ? e.message : String(e),
      latencyMs: Date.now() - started,
    };
  }
}
