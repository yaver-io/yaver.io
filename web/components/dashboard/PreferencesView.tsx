"use client";

import { useState, useEffect, useCallback } from "react";
import { CONVEX_URL } from "@/lib/constants";

interface PreferencesViewProps {
  token: string | null;
}

interface UserSettings {
  speechProvider?: string;
  ttsEnabled?: boolean;
  ttsProvider?: string;
  verbosity?: number;
  voiceKeyStorage?: string;
  runnerId?: string;
  customRunnerCommand?: string;
}

const SPEECH_PROVIDERS: Record<string, { name: string; description: string }> = {
  "on-device": { name: "On-device (Whisper)", description: "Local transcription, no API key needed" },
  openai: { name: "OpenAI", description: "Cloud transcription and speech via OpenAI API" },
  deepgram: { name: "Deepgram Flux", description: "Low-latency cloud transcription with end-of-turn detection" },
  assemblyai: { name: "AssemblyAI", description: "Cloud transcription via AssemblyAI API" },
};

const TTS_PROVIDERS: Record<string, { name: string; description: string }> = {
  device: { name: "Local device voice", description: "Uses the device browser or OS voice" },
  openai: { name: "OpenAI voice", description: "Uses OpenAI text-to-speech" },
  cartesia: { name: "Cartesia Sonic", description: "Low-latency agent voice readback via Cartesia" },
};

export default function PreferencesView({ token }: PreferencesViewProps) {
  const [settings, setSettings] = useState<UserSettings>({});
  const [loading, setLoading] = useState(true);
  const [editing, setEditing] = useState(false);

  // Editable fields
  const [speechProvider, setSpeechProvider] = useState("");
  const [ttsEnabled, setTtsEnabled] = useState(false);
  const [ttsProvider, setTtsProvider] = useState("device");
  const [verbosity, setVerbosity] = useState(10);

  const [saving, setSaving] = useState(false);
  const [saveMessage, setSaveMessage] = useState<{ type: "ok" | "error"; text: string } | null>(null);

  const loadSettings = useCallback(async () => {
    if (!token) return;
    try {
      const res = await fetch(`${CONVEX_URL}/settings`, {
        headers: { Authorization: `Bearer ${token}` },
      });
      if (res.ok) {
        const data = await res.json();
        const s = data.settings || {};
        setSettings(s);
        setSpeechProvider(s.speechProvider || "");
        setTtsEnabled(s.ttsEnabled || false);
        setTtsProvider(s.ttsProvider || "device");
        setVerbosity(s.verbosity ?? 10);
      }
    } catch {
      // ignore
    } finally {
      setLoading(false);
    }
  }, [token]);

  useEffect(() => {
    loadSettings();
  }, [loadSettings]);

  const handleSave = async () => {
    if (!token) return;
    setSaving(true);
    setSaveMessage(null);
    try {
      const res = await fetch(`${CONVEX_URL}/settings`, {
        method: "POST",
        headers: {
          Authorization: `Bearer ${token}`,
          "Content-Type": "application/json",
        },
        body: JSON.stringify({
          speechProvider: speechProvider || undefined,
          ttsEnabled,
          ttsProvider,
          verbosity,
        }),
      });
      if (res.ok) {
        setSaveMessage({ type: "ok", text: "Preferences saved." });
        setSettings({
          ...settings,
          speechProvider,
          ttsEnabled,
          ttsProvider,
          verbosity,
          voiceKeyStorage: "local-vault",
        });
        setEditing(false);
      } else {
        const text = await res.text();
        setSaveMessage({ type: "error", text: text || "Failed to save." });
      }
    } catch {
      setSaveMessage({ type: "error", text: "Network error. Please try again." });
    } finally {
      setSaving(false);
      setTimeout(() => setSaveMessage(null), 4000);
    }
  };

  if (loading) {
    return (
      <div className="card mb-6">
        <h2 className="mb-4 text-lg font-semibold text-surface-50">Preferences</h2>
        <div className="flex items-center justify-center py-4">
          <div className="h-5 w-5 animate-spin rounded-full border-2 border-surface-600 border-t-surface-50" />
        </div>
      </div>
    );
  }

  const providerInfo = SPEECH_PROVIDERS[settings.speechProvider || ""] || null;
  return (
    <div className="card mb-6">
      <div className="mb-4 flex items-center justify-between">
        <h2 className="text-lg font-semibold text-surface-50">Preferences</h2>
        {!editing && (
          <button
            onClick={() => setEditing(true)}
            className="rounded-lg border border-surface-700 px-3 py-1.5 text-xs text-surface-300 transition-colors hover:border-surface-500 hover:text-surface-50"
          >
            Edit
          </button>
        )}
      </div>

      {!editing ? (
        /* ── Read-only view ── */
        <div className="space-y-3">
          {/* Speech Provider */}
          <div className="flex items-start justify-between">
            <div>
              <p className="text-xs font-medium text-surface-400">Speech Provider</p>
              <p className="mt-0.5 text-sm text-surface-200">
                {providerInfo ? providerInfo.name : (settings.speechProvider || "Not configured")}
              </p>
            </div>
            {providerInfo && (
              <span className="mt-0.5 inline-flex items-center rounded-full bg-surface-800 px-2 py-0.5 text-[10px] font-medium text-surface-400">
                {settings.speechProvider === "on-device" ? "local" : "cloud"}
              </span>
            )}
          </div>

          {/* API Key */}
          {settings.speechProvider && settings.speechProvider !== "on-device" && (
            <div>
              <p className="text-xs font-medium text-surface-400">Provider Key</p>
              <p className="mt-0.5 text-sm text-surface-200">Local only: device SecureStore or agent vault</p>
            </div>
          )}

          {/* Key Storage */}
          <div>
            <p className="text-xs font-medium text-surface-400">Key Storage</p>
            <p className="mt-0.5 text-sm text-surface-200">
              <span className="inline-flex items-center gap-1.5">
                <span className="inline-block h-1.5 w-1.5 rounded-full bg-surface-500" />
                Local only (device SecureStore / agent vault)
              </span>
            </p>
          </div>

          {/* TTS */}
          <div className="flex items-center justify-between">
            <div>
              <p className="text-xs font-medium text-surface-400">Text-to-Speech</p>
              <p className="mt-0.5 text-sm text-surface-200">
                {settings.ttsEnabled ? (TTS_PROVIDERS[settings.ttsProvider || "device"]?.name || "Enabled") : "Disabled"}
              </p>
            </div>
            <span className={`inline-block h-2 w-2 rounded-full ${settings.ttsEnabled ? "bg-green-400" : "bg-surface-600"}`} />
          </div>

          {/* Verbosity */}
          <div>
            <p className="text-xs font-medium text-surface-400">Verbosity</p>
            <div className="mt-1 flex items-center gap-2">
              <div className="h-1.5 flex-1 rounded-full bg-surface-800">
                <div
                  className="h-1.5 rounded-full bg-surface-400"
                  style={{ width: `${((settings.verbosity ?? 10) / 10) * 100}%` }}
                />
              </div>
              <span className="text-xs font-mono text-surface-400">{settings.verbosity ?? 10}/10</span>
            </div>
          </div>

          {/* Runner */}
          {settings.runnerId && (
            <div>
              <p className="text-xs font-medium text-surface-400">AI Runner</p>
              <p className="mt-0.5 text-sm text-surface-200">{settings.runnerId}</p>
              {settings.customRunnerCommand && (
                <p className="mt-0.5 font-mono text-xs text-surface-500">{settings.customRunnerCommand}</p>
              )}
            </div>
          )}

          {/* Status messages */}
          {saveMessage && (
            <p className={`text-sm ${saveMessage.type === "ok" ? "text-green-400" : "text-red-400"}`}>
              {saveMessage.text}
            </p>
          )}
        </div>
      ) : (
        /* ── Edit view ── */
        <div className="space-y-4">
          <div>
            <label className="mb-1.5 block text-xs font-medium text-surface-400">Voice Engine</label>
            <div className="grid grid-cols-2 gap-2">
              {[
                { id: "local", label: "Local", sub: "Free", stt: "on-device", tts: "device" },
                { id: "openai", label: "OpenAI", sub: "API key", stt: "openai", tts: "openai" },
                { id: "deepgram-cartesia", label: "Flux + Cartesia", sub: "Agent vault", stt: "deepgram", tts: "cartesia" },
              ].map((engine) => {
                const selected = speechProvider === engine.stt && ttsProvider === engine.tts;
                return (
                  <button
                    key={engine.id}
                    type="button"
                    onClick={() => {
                      setSpeechProvider(engine.stt);
                      setTtsProvider(engine.tts);
                      if (engine.tts === "openai") setTtsEnabled(true);
                    }}
                    className={`rounded-lg border px-3 py-2 text-left text-xs transition-colors ${
                      selected
                        ? "border-surface-500 bg-surface-800 text-surface-50"
                        : "border-surface-700 text-surface-400 hover:border-surface-600"
                    }`}
                  >
                    <span className="block font-medium">{engine.label}</span>
                    <span className="mt-0.5 block text-[10px] opacity-60">{engine.sub}</span>
                  </button>
                );
              })}
            </div>
          </div>

          {/* Speech Provider */}
          <div>
            <label className="mb-1.5 block text-xs font-medium text-surface-400">Speech Provider</label>
            <select
              value={speechProvider}
              onChange={(e) => setSpeechProvider(e.target.value)}
              className="w-full rounded-lg border border-surface-700 bg-surface-850 px-4 py-2.5 text-sm text-surface-200 outline-none transition-colors focus:border-surface-500"
            >
              <option value="">None</option>
              {Object.entries(SPEECH_PROVIDERS).map(([key, info]) => (
                <option key={key} value={key}>{info.name}</option>
              ))}
            </select>
            {speechProvider && SPEECH_PROVIDERS[speechProvider] && (
              <p className="mt-1 text-xs text-surface-500">{SPEECH_PROVIDERS[speechProvider].description}</p>
            )}
          </div>

          {/* API Key */}
          {((speechProvider && speechProvider !== "on-device") || ttsProvider === "openai" || ttsProvider === "cartesia") && (
            <div>
              <label className="mb-1.5 block text-xs font-medium text-surface-400">Provider Credentials</label>
              <p className="rounded-lg border border-surface-700 bg-surface-850 px-4 py-2.5 text-xs text-surface-400">
                Set API keys on the connected agent with <code>yaver voice setup deepgram-cartesia</code> or from the mobile Voice screen. Keys are never stored in Convex.
              </p>
            </div>
          )}

          {/* Provider keys intentionally have no cloud storage control. */}

          {/* TTS toggle */}
          <div className="flex items-center justify-between">
            <div>
              <label className="text-xs font-medium text-surface-400">Text-to-Speech</label>
              <p className="text-[10px] text-surface-500">Read responses aloud</p>
            </div>
            <button
              type="button"
              onClick={() => setTtsEnabled(!ttsEnabled)}
              className={`relative h-6 w-11 rounded-full transition-colors ${ttsEnabled ? "bg-green-500/60" : "bg-surface-700"}`}
            >
              <span
                className={`absolute top-0.5 h-5 w-5 rounded-full bg-white shadow transition-transform ${ttsEnabled ? "left-[22px]" : "left-0.5"}`}
              />
            </button>
          </div>

          {ttsEnabled && (
            <div>
              <label className="mb-1.5 block text-xs font-medium text-surface-400">Text-to-Speech Provider</label>
              <div className="grid grid-cols-2 gap-2">
                {Object.entries(TTS_PROVIDERS).map(([key, info]) => (
                  <button
                    key={key}
                    type="button"
                    onClick={() => setTtsProvider(key)}
                    className={`rounded-lg border px-3 py-2 text-left text-xs transition-colors ${
                      ttsProvider === key
                        ? "border-surface-500 bg-surface-800 text-surface-50"
                        : "border-surface-700 text-surface-400 hover:border-surface-600"
                    }`}
                  >
                    <span className="block font-medium">{info.name}</span>
                    <span className="mt-0.5 block text-[10px] opacity-60">{info.description}</span>
                  </button>
                ))}
              </div>
            </div>
          )}

          {/* Verbosity slider */}
          <div>
            <label className="mb-1.5 block text-xs font-medium text-surface-400">
              Verbosity <span className="font-mono text-surface-500">{verbosity}/10</span>
            </label>
            <input
              type="range"
              min={0}
              max={10}
              step={1}
              value={verbosity}
              onChange={(e) => setVerbosity(Number(e.target.value))}
              className="w-full accent-surface-400"
            />
            <div className="flex justify-between text-[10px] text-surface-600">
              <span>Summary</span>
              <span>Full detail</span>
            </div>
          </div>

          {/* Status messages */}
          {saveMessage && (
            <p className={`text-sm ${saveMessage.type === "ok" ? "text-green-400" : "text-red-400"}`}>
              {saveMessage.text}
            </p>
          )}

          {/* Buttons */}
          <div className="flex gap-2">
            <button
              onClick={handleSave}
              disabled={saving}
              className="rounded-lg border border-surface-700 px-4 py-2 text-sm text-surface-200 transition-colors hover:border-surface-500 hover:text-surface-50 disabled:opacity-30"
            >
              {saving ? "Saving..." : "Save"}
            </button>
            <button
              onClick={() => {
                setEditing(false);
                // Reset to stored values
                setSpeechProvider(settings.speechProvider || "");
                setTtsEnabled(settings.ttsEnabled || false);
                setTtsProvider(settings.ttsProvider || "device");
                setVerbosity(settings.verbosity ?? 10);
              }}
              disabled={saving}
              className="rounded-lg border border-surface-700 px-4 py-2 text-sm text-surface-400 transition-colors hover:border-surface-500 hover:text-surface-300 disabled:opacity-30"
            >
              Cancel
            </button>
          </div>
        </div>
      )}
    </div>
  );
}
