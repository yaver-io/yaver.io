// codingBackendStore.ts — the RN-bound glue that turns the pure codingBackend
// selection + the three provider factories into a live LlmProvider for the
// Mobile Sandbox editor. NOT tsx-tested (imports expo-secure-store /
// expo-file-system / the native engine). The pure, tested logic lives in
// codingBackend.ts + llm{Local,OpenAI,Anthropic}.ts; this is the wiring.
//
//   pref  ── loadCodingBackendPref / saveCodingBackendPref  (SecureStore)
//   keys  ── from auth.ts LOCAL_KEYS (BYO OpenAI / GLM / Anthropic)
//   local ── a downloaded coder GGUF + the linked llama.rn engine
//
// All three coding backends produce the same EditPlan (llmClient.ts) which the
// editor previews + applies against the phone-local src/ tree.

import * as FileSystem from "expo-file-system";

import {
  LOCAL_KEYS,
  getLocalSecret,
  saveLocalSecret,
} from "./auth";
import {
  resolveBackend,
  type CodingBackendAvailability,
  type CodingBackendId,
  type CodingBackendPref,
  type ResolvedBackend,
} from "./codingBackend";
import type { LlmProvider } from "./llmClient";
import { createAnthropicProvider } from "./llmAnthropic";
import { createClaudeSubscriptionProvider } from "./llmClaudeSubscription";
import { createOpenAiProvider } from "./llmOpenAI";
import { createLocalProvider } from "./llmLocal";
import { createRemoteProvider } from "./llmRemote";
import { quicClient } from "./quic";
import { hasSubscription } from "./subscriptionStore";
import { engineAvailable, loadModel, type LoadedModel } from "./localAgent/engine";
import { MODEL_REGISTRY, type ModelEntry } from "./localAgent/models";
import { readDeviceCapability } from "./deviceCapability";
import { canRunModel } from "./deviceCapabilityCore";

const FS = FileSystem as any;

// ── Preference (which backend the user picked) ──────────────────────

const VALID_PREFS = new Set<CodingBackendPref>(["auto", "local", "subscription", "anthropic", "openai", "glm", "remote"]);

export async function loadCodingBackendPref(): Promise<CodingBackendPref> {
  const raw = await getLocalSecret(LOCAL_KEYS.mobileCodingProvider);
  return raw && VALID_PREFS.has(raw as CodingBackendPref) ? (raw as CodingBackendPref) : "auto";
}

export async function saveCodingBackendPref(pref: CodingBackendPref): Promise<void> {
  await saveLocalSecret(LOCAL_KEYS.mobileCodingProvider, pref);
}

// ── On-device model files ───────────────────────────────────────────
// Downloaded GGUFs live under documentDirectory/yaver-models/<id>.gguf
// (the download adapter writes here; this is the single path convention).

export function localModelsDir(): string {
  const dir = FS.documentDirectory;
  return dir ? `${dir}yaver-models/` : "";
}

export function localModelPath(id: string): string {
  return `${localModelsDir()}${id}.gguf`;
}

async function fileExists(uri: string): Promise<boolean> {
  if (!uri) return false;
  try {
    const info = await FileSystem.getInfoAsync(uri);
    return !!info.exists && (info as { size?: number }).size !== 0;
  } catch {
    return false;
  }
}

/** Coder-tier models from the registry that are downloaded to the cache. */
export async function installedCoderModels(): Promise<ModelEntry[]> {
  const coders = MODEL_REGISTRY.filter((m) => m.tier === "coder");
  const out: ModelEntry[] = [];
  for (const m of coders) {
    if (await fileExists(localModelPath(m.id))) out.push(m);
  }
  return out;
}

/**
 * The coder model we'd actually LOAD for on-device coding: the largest
 * installed coder THIS DEVICE CAN SAFELY RUN. Platform-aware — on a phone
 * without the RAM headroom this returns null (even if a big GGUF was somehow
 * downloaded), so we never risk an iOS jetsam kill loading a too-large model.
 */
export async function activeCoderModel(): Promise<ModelEntry | null> {
  const cap = readDeviceCapability();
  const runnable = (await installedCoderModels()).filter((m) => canRunModel(m.minRamMb, cap));
  if (runnable.length === 0) return null;
  return runnable.sort((a, b) => b.approxSizeMb - a.approxSizeMb)[0];
}

// ── Availability snapshot ───────────────────────────────────────────

export async function loadCodingAvailability(): Promise<CodingBackendAvailability> {
  const [anthropic, openai, glm, coder, subscription] = await Promise.all([
    getLocalSecret(LOCAL_KEYS.anthropicApiKey),
    getLocalSecret(LOCAL_KEYS.openAiApiKey),
    getLocalSecret(LOCAL_KEYS.glmApiKey),
    activeCoderModel(),
    hasSubscription(),
  ]);
  return {
    // local is only "ready" when the native engine is linked AND a coder GGUF
    // is downloaded — otherwise the user must add a cloud key or download a model.
    localModelReady: engineAvailable() && coder !== null,
    // Claude on the user's plan — free, full quality. Preferred over the metered key.
    claudeSubscription: subscription,
    anthropicKey: !!anthropic?.trim(),
    openaiKey: !!openai?.trim(),
    glmKey: !!glm?.trim(),
    // The remote GLM runner needs a connected box to dispatch to. We don't
    // probe the box's GLM config here (that surfaces as an error at run time
    // with a clear "configure ZAI_API_KEY on the box" message) — connectivity
    // is the gate for offering the option.
    remoteRunner: quicClient.isConnected,
  };
}

// ── Provider factory ────────────────────────────────────────────────

export interface MakeProviderContext {
  /** Path of the file the user has open (the on-device edit target). */
  openPath?: string;
  /** Framework hint for the system prompt ("react-native"). */
  framework?: string;
}

// Cache one loaded on-device model so repeated edits don't reload the GGUF.
let _loaded: { id: string; model: LoadedModel } | null = null;

async function getLoadedCoder(): Promise<LoadedModel | null> {
  const coder = await activeCoderModel();
  if (!coder) return null;
  if (_loaded?.id === coder.id) return _loaded.model;
  // loadModel can throw if llama.rn's JS shim is present but the native module
  // isn't linked (a half-installed build). Treat any failure as "unavailable"
  // so the editor falls back to a cloud key instead of crashing.
  let model: LoadedModel | null = null;
  try {
    model = await loadModel(coder.id, localModelPath(coder.id));
  } catch {
    model = null;
  }
  if (!model) return null;
  // Release a previously-loaded different model to free RAM.
  if (_loaded && _loaded.id !== coder.id) await _loaded.model.release().catch(() => {});
  _loaded = { id: coder.id, model };
  return model;
}

/** Free the cached on-device model (e.g. when the user leaves the editor). */
export async function releaseLocalModel(): Promise<void> {
  if (_loaded) {
    await _loaded.model.release().catch(() => {});
    _loaded = null;
  }
}

/**
 * Build a live LlmProvider for a concrete backend id, or null when it can't be
 * built (missing key / engine / model). The editor resolves the id via
 * resolveActiveBackend first, so a null here is an unexpected race, not the
 * normal "not configured" path.
 */
export async function makeProvider(
  id: CodingBackendId,
  ctx: MakeProviderContext = {},
): Promise<LlmProvider | null> {
  switch (id) {
    case "subscription": {
      // No key lookup — the provider draws from the mirrored plan token at call
      // time. Returns a provider unconditionally; if the token is missing the
      // call throws with a "mirror from desktop" hint (resolveActiveBackend
      // already gated usability on hasSubscription()).
      return createClaudeSubscriptionProvider();
    }
    case "anthropic": {
      const key = (await getLocalSecret(LOCAL_KEYS.anthropicApiKey))?.trim();
      return key ? createAnthropicProvider({ apiKey: key }) : null;
    }
    case "openai": {
      const key = (await getLocalSecret(LOCAL_KEYS.openAiApiKey))?.trim();
      return key ? createOpenAiProvider({ flavor: "openai", apiKey: key }) : null;
    }
    case "glm": {
      const key = (await getLocalSecret(LOCAL_KEYS.glmApiKey))?.trim();
      return key ? createOpenAiProvider({ flavor: "glm", apiKey: key }) : null;
    }
    case "remote": {
      // No phone-side key — the box runs the GLM runner with its own z.ai
      // credential. Requires a live connection; null if the box dropped.
      if (!quicClient.isConnected) return null;
      return createRemoteProvider({
        dispatch: (body) => quicClient.sandboxRun(body),
      });
    }
    case "local": {
      const model = await getLoadedCoder();
      if (!model) return null;
      return createLocalProvider({
        modelId: model.id,
        openPath: ctx.openPath,
        stack: ctx.framework ? [ctx.framework] : undefined,
        complete: async (prompt, opts) => {
          const r = await model.complete({ prompt, maxTokens: opts?.maxTokens });
          return r.text;
        },
      });
    }
  }
}

export interface ActiveBackendResult extends ResolvedBackend {
  availability: CodingBackendAvailability;
  pref: CodingBackendPref;
}

/** Load pref + availability and resolve which backend will actually run. */
export async function resolveActiveBackend(): Promise<ActiveBackendResult> {
  const [pref, availability] = await Promise.all([
    loadCodingBackendPref(),
    loadCodingAvailability(),
  ]);
  return { ...resolveBackend(pref, availability), availability, pref };
}
