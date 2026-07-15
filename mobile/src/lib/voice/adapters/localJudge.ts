/**
 * localJudge.ts — wires the semantic completeness judge to the on-device
 * llama.rn engine (FREE, offline — the only judge placement that avoids the
 * "two payments" the product owner flagged; see the on-device-judge audit).
 *
 * Graceful degradation is the whole point:
 *  - If a model is loaded, ambiguous end-of-utterance cases get a real semantic
 *    verdict ({complete, wantsAnswer}) via a GBNF-constrained completion.
 *  - If the engine isn't linked or no model is installed yet (today's builds —
 *    the bundled GGUF/voice-helper wiring hasn't shipped), the judge still works
 *    on its heuristic + silence fallback. Local voice stays functional now and
 *    only gets sharper when the model lands.
 *
 * When the bundled-model install path exists, wire it in `resolveModelComplete`
 * — that's the single seam.
 */
import {
  createCompletenessJudge,
  type ModelComplete,
} from "../completenessJudge";
import type { CompletenessJudge } from "../types";
import { engineAvailable, loadModel, type LoadedModel } from "../../localAgent/engine";
import { bundledModel } from "../../localAgent/models";

export interface LocalJudgeOptions {
  /** Absolute on-device path to a GGUF. When omitted we try to resolve the
   *  bundled model; if that can't be resolved, the judge runs heuristic-only. */
  modelPath?: string;
  /** Escape hatch / test seam: provide the completion fn directly. */
  complete?: ModelComplete | null;
}

/**
 * Build the judge. The model is loaded LAZILY on first ambiguous case (never on
 * construction) so entering the voice loop is instant; the load result is cached
 * for the surface's lifetime.
 */
export function createLocalJudge(opts: LocalJudgeOptions = {}): CompletenessJudge {
  if (opts.complete !== undefined) {
    return createCompletenessJudge({ complete: opts.complete });
  }

  let loaded: LoadedModel | null | undefined; // undefined = not tried yet
  let loading: Promise<LoadedModel | null> | null = null;

  const lazyComplete: ModelComplete = async (o) => {
    if (loaded === undefined && !loading) {
      loading = resolveModel(opts.modelPath).catch(() => null);
    }
    if (loading) {
      loaded = await loading;
      loading = null;
    }
    if (!loaded) throw new Error("no on-device model"); // → judge silence fallback
    const r = await loaded.complete({
      prompt: o.prompt,
      grammar: o.grammar,
      maxTokens: o.maxTokens,
      temperature: o.temperature,
    });
    return { text: r.text };
  };

  return createCompletenessJudge({ complete: lazyComplete });
}

/**
 * Resolve + load a model for the judge. Returns null (heuristic-only) whenever
 * the engine isn't linked or no path is available. Kept isolated so the model
 * install path is a one-line wire-up when it ships.
 */
async function resolveModel(modelPath?: string): Promise<LoadedModel | null> {
  if (!engineAvailable()) return null;
  const entry = bundledModel();
  const path = modelPath; // TODO(model-install): resolve bundledModel() → on-device GGUF path
  if (!path) return null;
  return loadModel(entry.id, path);
}
