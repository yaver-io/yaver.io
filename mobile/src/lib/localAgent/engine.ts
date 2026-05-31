// localAgent/engine.ts — the llama.rn engine adapter (the ONE on-device
// inference engine; see models.ts). Thin, lazy, and graceful:
//
//  - It lazy-requires `llama.rn` so a build where the native module isn't
//    linked yet (or web/test) doesn't crash — engineAvailable() just returns
//    false and the app falls back to the remote brain or scripted guidance.
//  - It exposes a tiny surface the orchestrator needs: load a GGUF, run a
//    constrained completion (GBNF grammar for tool-routing / JSON), unload.
//  - No model bytes or prompts are logged; nothing here touches Convex.
//
// The pure decision layers (brain/catalog/interpreter/resolver/tiers) decide
// WHAT to do; this is the only place that actually talks to the native model.
// Kept deliberately small so it's the single thing to fill in on-device.

export interface CompletionOptions {
  prompt: string;
  /** Optional GBNF grammar to constrain output (e.g. JSON tool-call shape). */
  grammar?: string;
  maxTokens?: number;
  temperature?: number;
  /** Stop sequences. */
  stop?: string[];
}

export interface CompletionResult {
  text: string;
  /** True when the native engine produced this; false when it's a fallback. */
  fromModel: boolean;
}

export interface LoadedModel {
  id: string;
  complete(opts: CompletionOptions): Promise<CompletionResult>;
  release(): Promise<void>;
}

// Lazily resolve the native module exactly once. Returns null when absent.
let _rnllama: any | null | undefined;
function getNative(): any | null {
  if (_rnllama !== undefined) return _rnllama;
  try {
    // eslint-disable-next-line @typescript-eslint/no-var-requires
    _rnllama = require("llama.rn");
  } catch {
    _rnllama = null;
  }
  return _rnllama;
}

/** True when the native llama.rn engine is linked and usable on this build. */
export function engineAvailable(): boolean {
  const n = getNative();
  return !!n && typeof n.initLlama === "function";
}

/**
 * Load a GGUF from a local file path into the engine. Returns a LoadedModel,
 * or null when the native engine isn't available (caller falls back).
 */
export async function loadModel(id: string, filePath: string): Promise<LoadedModel | null> {
  const n = getNative();
  if (!n || typeof n.initLlama !== "function") return null;
  // llama.rn: initLlama({ model, n_ctx, ... }) → context with .completion()
  const ctx = await n.initLlama({ model: filePath, n_ctx: 4096, n_gpu_layers: 99 });
  return {
    id,
    async complete(opts: CompletionOptions): Promise<CompletionResult> {
      const res = await ctx.completion({
        prompt: opts.prompt,
        grammar: opts.grammar,
        n_predict: opts.maxTokens ?? 256,
        temperature: opts.temperature ?? 0.2,
        stop: opts.stop ?? [],
      });
      return { text: (res?.text ?? "").trim(), fromModel: true };
    },
    async release(): Promise<void> {
      try {
        await ctx.release?.();
      } catch {
        // best-effort
      }
    },
  };
}

// HBC/GGUF validation magic for sanity-checking a downloaded file before load.
// GGUF files start with the ASCII magic "GGUF" (0x47 0x47 0x55 0x46).
export const GGUF_MAGIC = [0x47, 0x47, 0x55, 0x46] as const;

/** Check the first 4 bytes look like a GGUF file. Pure — caller reads the head. */
export function looksLikeGGUF(head: ArrayLike<number>): boolean {
  if (!head || head.length < 4) return false;
  return GGUF_MAGIC.every((b, i) => head[i] === b);
}
