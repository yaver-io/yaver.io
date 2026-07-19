"use client";

import { useState, useEffect, useCallback } from "react";
import { CONVEX_URL } from "@/lib/constants";

interface RelayServerViewProps {
  token: string | null;
}

export default function RelayServerView({ token }: RelayServerViewProps) {
  const [relayUrl, setRelayUrl] = useState("");
  const [relayPassword, setRelayPassword] = useState("");
  const [showPassword, setShowPassword] = useState(false);
  const [saving, setSaving] = useState(false);
  const [saveMessage, setSaveMessage] = useState<{ type: "ok" | "error"; text: string } | null>(null);
  const [testResult, setTestResult] = useState<{ type: "ok" | "error"; text: string } | null>(null);
  const [testing, setTesting] = useState(false);
  const [loading, setLoading] = useState(true);
  const [hasRelay, setHasRelay] = useState(false);

  // Load current settings
  const loadSettings = useCallback(async () => {
    if (!token) return;
    try {
      const res = await fetch(`${CONVEX_URL}/settings`, {
        headers: { Authorization: `Bearer ${token}` },
      });
      if (res.ok) {
        const data = await res.json();
        const s = data.settings;
        if (s?.relayUrl) {
          setRelayUrl(s.relayUrl);
          setRelayPassword(s.relayPassword || "");
          setHasRelay(true);
        }
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
          relayUrl: relayUrl.trim() || undefined,
          relayPassword: relayPassword || undefined,
        }),
      });
      if (res.ok) {
        setSaveMessage({ type: "ok", text: "Relay configuration saved." });
        setHasRelay(!!relayUrl.trim());
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

  const handleTest = async () => {
    const url = relayUrl.trim();
    if (!url) {
      setTestResult({ type: "error", text: "Enter a relay URL first." });
      return;
    }
    setTesting(true);
    setTestResult(null);
    const start = performance.now();
    try {
      const healthUrl = url.replace(/\/+$/, "") + "/health";
      const res = await fetch(healthUrl, { signal: AbortSignal.timeout(5000) });
      const elapsed = Math.round(performance.now() - start);
      if (res.ok) {
        setTestResult({ type: "ok", text: `Connected (${elapsed}ms)` });
      } else {
        setTestResult({ type: "error", text: `Server returned ${res.status}` });
      }
    } catch {
      setTestResult({ type: "error", text: "Failed to connect" });
    } finally {
      setTesting(false);
    }
  };

  const handleRemove = async () => {
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
          relayUrl: undefined,
          relayPassword: undefined,
        }),
      });
      if (res.ok) {
        setRelayUrl("");
        setRelayPassword("");
        setHasRelay(false);
        setTestResult(null);
        setSaveMessage({ type: "ok", text: "Relay configuration removed." });
      } else {
        setSaveMessage({ type: "error", text: "Failed to remove relay." });
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
        <h2 className="mb-4 text-lg font-semibold text-surface-50">Relay Server</h2>
        <div className="flex items-center justify-center py-4">
          <div className="h-5 w-5 animate-spin rounded-full border-2 border-surface-600 border-t-surface-50" />
        </div>
      </div>
    );
  }

  return (
    <>
      <div className="card mb-6">
        <h2 className="mb-4 text-lg font-semibold text-surface-50">Relay Server</h2>

        {!hasRelay && !relayUrl && (
          <p className="mb-4 text-sm text-surface-400">
            No relay server configured. You only need a relay if your phone and dev machine are on
            different networks.{" "}
            <a
              href="/docs/self-hosting"
              className="text-surface-300 underline underline-offset-2 transition-colors hover:text-surface-50"
            >
              Learn about self-hosting
            </a>
          </p>
        )}

        {/* Relay URL */}
        <label className="mb-1.5 block text-xs font-medium text-surface-400">Relay URL</label>
        <input
          type="text"
          value={relayUrl}
          onChange={(e) => setRelayUrl(e.target.value)}
          placeholder="https://relay.example.com"
          disabled={saving}
          className="mb-3 w-full rounded-lg border border-surface-700 bg-surface-850 px-4 py-2.5 text-sm text-surface-200 placeholder-surface-600 outline-none transition-colors focus:border-surface-500 disabled:opacity-50"
        />

        {/* Relay Password */}
        <label className="mb-1.5 block text-xs font-medium text-surface-400">Relay Password</label>
        <div className="relative mb-4">
          <input
            type={showPassword ? "text" : "password"}
            value={relayPassword}
            onChange={(e) => setRelayPassword(e.target.value)}
            placeholder="Optional"
            disabled={saving}
            className="w-full rounded-lg border border-surface-700 bg-surface-850 px-4 py-2.5 pr-10 text-sm text-surface-200 placeholder-surface-600 outline-none transition-colors focus:border-surface-500 disabled:opacity-50"
          />
          <button
            type="button"
            onClick={() => setShowPassword(!showPassword)}
            className="absolute right-3 top-1/2 -translate-y-1/2 text-surface-500 transition-colors hover:text-surface-300"
            aria-label={showPassword ? "Hide password" : "Show password"}
          >
            {showPassword ? (
              <svg className="h-4 w-4" fill="none" viewBox="0 0 24 24" strokeWidth={1.5} stroke="currentColor">
                <path strokeLinecap="round" strokeLinejoin="round" d="M3.98 8.223A10.477 10.477 0 001.934 12C3.226 16.338 7.244 19.5 12 19.5c.993 0 1.953-.138 2.863-.395M6.228 6.228A10.45 10.45 0 0112 4.5c4.756 0 8.773 3.162 10.065 7.498a10.523 10.523 0 01-4.293 5.774M6.228 6.228L3 3m3.228 3.228l3.65 3.65m7.894 7.894L21 21m-3.228-3.228l-3.65-3.65m0 0a3 3 0 10-4.243-4.243m4.242 4.242L9.88 9.88" />
              </svg>
            ) : (
              <svg className="h-4 w-4" fill="none" viewBox="0 0 24 24" strokeWidth={1.5} stroke="currentColor">
                <path strokeLinecap="round" strokeLinejoin="round" d="M2.036 12.322a1.012 1.012 0 010-.639C3.423 7.51 7.36 4.5 12 4.5c4.638 0 8.573 3.007 9.963 7.178.07.207.07.431 0 .639C20.577 16.49 16.64 19.5 12 19.5c-4.638 0-8.573-3.007-9.963-7.178z" />
                <path strokeLinecap="round" strokeLinejoin="round" d="M15 12a3 3 0 11-6 0 3 3 0 016 0z" />
              </svg>
            )}
          </button>
        </div>

        {/* Status messages */}
        {saveMessage && (
          <p className={`mb-3 text-sm ${saveMessage.type === "ok" ? "text-green-400" : "text-red-400"}`}>
            {saveMessage.text}
          </p>
        )}
        {testResult && (
          <p className={`mb-3 text-sm ${testResult.type === "ok" ? "text-green-400" : "text-red-400"}`}>
            {testResult.type === "ok" ? (
              <span className="inline-flex items-center gap-1.5">
                <span className="inline-block h-2 w-2 rounded-full bg-green-400" />
                {testResult.text}
              </span>
            ) : (
              <span className="inline-flex items-center gap-1.5">
                <span className="inline-block h-2 w-2 rounded-full bg-red-400" />
                {testResult.text}
              </span>
            )}
          </p>
        )}

        {/* Buttons */}
        <div className="flex gap-2">
          <button
            onClick={handleSave}
            disabled={saving || !relayUrl.trim()}
            className="rounded-lg border border-surface-700 px-4 py-2 text-sm text-surface-200 transition-colors hover:border-surface-500 hover:text-surface-50 disabled:opacity-30 disabled:hover:border-surface-700"
          >
            {saving ? "Saving..." : "Save"}
          </button>
          <button
            onClick={handleTest}
            disabled={testing || !relayUrl.trim()}
            className="rounded-lg border border-surface-700 px-4 py-2 text-sm text-surface-200 transition-colors hover:border-surface-500 hover:text-surface-50 disabled:opacity-30 disabled:hover:border-surface-700"
          >
            {testing ? "Testing..." : "Test"}
          </button>
          {hasRelay && (
            <button
              onClick={handleRemove}
              disabled={saving}
              className="rounded-lg border border-red-500/30 px-4 py-2 text-sm text-red-400 transition-colors hover:bg-red-500/10 disabled:opacity-30"
            >
              Remove
            </button>
          )}
        </div>

        <p className="mt-4 text-xs leading-relaxed text-surface-500">
          Relay settings saved here are stored in your account and sync across your devices. If you
          prefer to keep relay credentials local-only, configure them in the mobile app or CLI instead.
        </p>
      </div>

      {/* Info box */}
      <div className="mb-6 rounded-xl border border-surface-800 bg-surface-900/50 px-5 py-4">
        <p className="text-xs leading-relaxed text-surface-500">
          A relay server helps connect your devices when they are on different networks (e.g., phone
          on cellular, dev machine at home). If you are always on the same WiFi, direct LAN can work;
          for normal remote use, Yaver Relay is the supported path.
        </p>
      </div>
    </>
  );
}
