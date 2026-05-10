"use client";

// YaverAgentSettings — provider/model/api-key for the mobile-embedded
// yaver-agent (the small LLM that handles control-plane tasks like
// "authenticate primary device" or "what runners are authed on the mac
// mini"). Mirrors mobile/src/components/YaverAgentSettings.tsx.
//
// Stored in vault under project "yaver-agent" via /yaver-agent/config.
// API-key value is write-only; GET never returns it. POST with apiKey
// omitted = leave existing; apiKey: "" = clear; apiKey: "<value>" =
// replace.

import { useCallback, useEffect, useMemo, useState } from "react";
import {
  agentClient,
  type YaverAgentConfig,
  type YaverAgentProviderDefault,
  type YaverAgentProviderId,
  type YaverAgentSetRequest,
} from "@/lib/agent-client";

const PROVIDER_LABELS: Record<YaverAgentProviderId, string> = {
  glm: "GLM",
  anthropic: "Anthropic",
  openai: "OpenAI",
  openrouter: "OpenRouter",
};

// Providers where exposing a base URL field actually helps the user.
// glm + anthropic have fixed canonical URLs.
const BASE_URL_PROVIDERS: YaverAgentProviderId[] = ["openai", "openrouter"];

interface Props {
  /** When false, the form renders read-only with a connect-prompt. */
  connected?: boolean;
}

export default function YaverAgentSettings({ connected = true }: Props) {
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [info, setInfo] = useState<string | null>(null);

  const [provider, setProvider] = useState<YaverAgentProviderId>("glm");
  const [model, setModel] = useState<string>("");
  const [baseUrl, setBaseUrl] = useState<string>("");
  const [apiKey, setApiKey] = useState<string>("");
  const [hadKey, setHadKey] = useState<boolean>(false);
  const [defaults, setDefaults] = useState<YaverAgentProviderDefault[]>([]);

  const refresh = useCallback(async () => {
    if (!connected) {
      setLoading(false);
      return;
    }
    setLoading(true);
    setError(null);
    try {
      const data = await agentClient.yaverAgentConfigGet();
      const cfg: YaverAgentConfig = data.config;
      if (cfg.provider) setProvider(cfg.provider as YaverAgentProviderId);
      setModel(cfg.model || "");
      setBaseUrl(cfg.baseUrl || "");
      setHadKey(!!cfg.hasApiKey);
      setDefaults(data.defaults || []);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setLoading(false);
    }
  }, [connected]);

  useEffect(() => {
    void refresh();
  }, [refresh]);

  const currentDefault = useMemo(
    () => defaults.find((d) => d.provider === provider),
    [defaults, provider],
  );

  const switchProvider = (next: YaverAgentProviderId) => {
    setProvider(next);
    const def = defaults.find((d) => d.provider === next);
    setModel(def?.model || "");
    setBaseUrl(def?.baseUrl || "");
    setInfo(null);
  };

  const save = async () => {
    setSaving(true);
    setError(null);
    setInfo(null);
    try {
      const req: YaverAgentSetRequest = {
        provider,
        model: model.trim(),
        baseUrl: baseUrl.trim(),
      };
      if (apiKey.trim() !== "") req.apiKey = apiKey.trim();
      const data = await agentClient.yaverAgentConfigSet(req);
      setHadKey(!!data.config.hasApiKey);
      setApiKey("");
      setInfo("Saved.");
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setSaving(false);
    }
  };

  const showBaseUrl = BASE_URL_PROVIDERS.includes(provider);

  const keyHint = hadKey
    ? apiKey.trim() === ""
      ? "API key on file (leave blank to keep)."
      : "Will replace existing key."
    : apiKey.trim() === ""
    ? "No key on file yet."
    : "New key will be saved.";

  return (
    <div className="card mb-6">
      <h3 className="mb-2 text-sm font-medium uppercase tracking-wider text-surface-400">
        Yaver Agent
      </h3>
      <p className="mb-4 text-xs text-surface-500">
        Tiny LLM that runs inside the mobile app for control-plane tasks
        only — device auth, status checks, primary management. It never
        handles your code, and Claude Max tokens never enter it. For
        Anthropic this uses your <em>API key</em>, not your Max plan.
      </p>

      {!connected && (
        <div className="mb-4 rounded-md border border-amber-500/30 bg-amber-500/5 px-3 py-2 text-xs text-amber-300">
          Connect to a host device to save these settings. They live in
          that device&apos;s vault, not in the browser.
        </div>
      )}

      {loading ? (
        <p className="text-xs text-surface-500">Loading…</p>
      ) : (
        <>
          <div className="mb-4">
            <label className="mb-2 block text-xs font-medium text-surface-300">Provider</label>
            <div className="flex flex-wrap gap-2">
              {(Object.keys(PROVIDER_LABELS) as YaverAgentProviderId[]).map((id) => {
                const active = provider === id;
                return (
                  <button
                    key={id}
                    type="button"
                    onClick={() => switchProvider(id)}
                    className={`rounded-full border px-3 py-1.5 text-xs font-medium transition-colors ${
                      active
                        ? "border-brand-500 bg-brand-500/20 text-brand-200"
                        : "border-surface-700 bg-surface-900/40 text-surface-300 hover:bg-surface-800/60"
                    }`}
                  >
                    {PROVIDER_LABELS[id]}
                  </button>
                );
              })}
            </div>
            {currentDefault?.note && (
              <p className="mt-2 text-[11px] text-surface-500">{currentDefault.note}</p>
            )}
          </div>

          <div className="mb-4">
            <label className="mb-2 block text-xs font-medium text-surface-300">Model</label>
            <input
              type="text"
              value={model}
              onChange={(e) => setModel(e.target.value)}
              placeholder={currentDefault?.model || "model id"}
              className="w-full rounded-md border border-surface-700 bg-surface-950 px-3 py-2 text-sm text-surface-100 focus:border-brand-500 focus:outline-none"
              autoCapitalize="none"
              autoCorrect="off"
              spellCheck={false}
            />
            <p className="mt-1 text-[11px] text-surface-500">
              Leave blank to use{" "}
              <span className="text-surface-300">{currentDefault?.model || "default"}</span>.
            </p>
          </div>

          {showBaseUrl && (
            <div className="mb-4">
              <label className="mb-2 block text-xs font-medium text-surface-300">
                Base URL (optional)
              </label>
              <input
                type="text"
                value={baseUrl}
                onChange={(e) => setBaseUrl(e.target.value)}
                placeholder={currentDefault?.baseUrl || "https://api.example.com/v1"}
                className="w-full rounded-md border border-surface-700 bg-surface-950 px-3 py-2 text-sm text-surface-100 focus:border-brand-500 focus:outline-none"
                autoCapitalize="none"
                autoCorrect="off"
                spellCheck={false}
              />
            </div>
          )}

          <div className="mb-2">
            <label className="mb-2 block text-xs font-medium text-surface-300">API Key</label>
            <input
              type="password"
              value={apiKey}
              onChange={(e) => setApiKey(e.target.value)}
              placeholder={hadKey ? "•••••• (saved)" : "paste provider api key"}
              className="w-full rounded-md border border-surface-700 bg-surface-950 px-3 py-2 text-sm text-surface-100 focus:border-brand-500 focus:outline-none"
              autoCapitalize="none"
              autoCorrect="off"
              spellCheck={false}
            />
            <p className="mt-1 text-[11px] text-surface-500">{keyHint}</p>
          </div>

          {error && <p className="mt-3 text-xs text-red-400">{error}</p>}
          {info && <p className="mt-3 text-xs text-emerald-300">{info}</p>}

          <button
            type="button"
            onClick={save}
            disabled={!connected || saving}
            className="mt-4 w-full rounded-lg bg-brand-500 px-4 py-2.5 text-sm font-medium text-white transition-colors hover:bg-brand-400 disabled:cursor-not-allowed disabled:opacity-50"
          >
            {saving ? "Saving…" : "Save Yaver Agent settings"}
          </button>
        </>
      )}
    </div>
  );
}
