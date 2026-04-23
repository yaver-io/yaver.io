"use client";

import { useEffect, useState } from "react";
import { agentClient, type GitProviderStatusRow } from "@/lib/agent-client";

export default function GitView() {
  const [providers, setProviders] = useState<GitProviderStatusRow[] | null>(null);
  const [detecting, setDetecting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let alive = true;
    (async () => {
      try {
        const rows = await agentClient.gitProviderStatus();
        if (alive) setProviders(rows);
      } catch (e: any) {
        if (alive) setError(e?.message || "Failed to load providers");
      }
    })();
    return () => {
      alive = false;
    };
  }, []);

  async function handleDetect() {
    setDetecting(true);
    setError(null);
    try {
      const rows = await agentClient.gitProviderDetect();
      setProviders(rows);
    } catch (e: any) {
      setError(e?.message || "Detect failed");
    }
    setDetecting(false);
  }

  return (
    <div className="space-y-4">
      <header className="flex items-start justify-between">
        <div>
          <h2 className="text-lg font-semibold text-surface-100">Git</h2>
          <p className="mt-1 text-xs text-surface-500 max-w-md">
            Linked GitHub / GitLab accounts on the connected agent. Detect pulls credentials from the host&apos;s <code className="rounded bg-surface-800 px-1 text-surface-400">gh</code> and <code className="rounded bg-surface-800 px-1 text-surface-400">glab</code> CLIs.
          </p>
        </div>
        <button
          onClick={handleDetect}
          disabled={detecting}
          className="rounded-md border border-indigo-500/30 bg-indigo-500/10 px-3 py-1.5 text-xs font-medium text-indigo-200 hover:bg-indigo-500/15 disabled:opacity-50"
        >
          {detecting ? "Detecting…" : "Auto-detect"}
        </button>
      </header>

      {error && (
        <div className="rounded-md border border-red-500/30 bg-red-500/5 p-3 text-xs text-red-300">
          {error}
        </div>
      )}

      {providers === null ? (
        <div className="rounded-lg border border-surface-800 bg-surface-900/40 p-6 text-center text-xs text-surface-500">
          Loading providers…
        </div>
      ) : providers.length === 0 ? (
        <div className="rounded-lg border border-dashed border-surface-800 bg-surface-900/40 p-8 text-center">
          <p className="text-sm text-surface-300">No git provider linked yet.</p>
          <p className="mt-2 text-xs text-surface-500">
            On the connected agent, sign in with the GitHub or GitLab CLI and click Auto-detect:
          </p>
          <div className="mt-3 flex flex-col items-center gap-1 text-[11px]">
            <code className="rounded bg-surface-800 px-2 py-1 text-surface-300 font-mono">gh auth login</code>
            <code className="rounded bg-surface-800 px-2 py-1 text-surface-300 font-mono">glab auth login</code>
          </div>
          <p className="mt-4 text-[11px] text-surface-600">
            Manual token entry + OAuth flow from the web are follow-up work.
          </p>
        </div>
      ) : (
        <ul className="space-y-2">
          {providers.map((p) => (
            <li
              key={`${p.host}-${p.provider}-${p.username}`}
              className="flex items-center gap-3 rounded-xl border border-surface-800 bg-surface-900/50 p-3"
            >
              <div className="flex h-10 w-10 items-center justify-center rounded-lg border border-surface-800 bg-surface-950 text-sm text-surface-300">
                {p.provider === "github" ? "GH" : p.provider === "gitlab" ? "GL" : p.provider?.slice(0, 2).toUpperCase()}
              </div>
              <div className="min-w-0 flex-1">
                <p className="truncate text-sm font-semibold text-surface-100">
                  {p.username}
                  <span className="ml-2 text-[11px] font-normal text-surface-500">@{p.host}</span>
                </p>
                <div className="mt-1 flex flex-wrap items-center gap-1 text-[10px] uppercase tracking-wider">
                  <span className="rounded border border-emerald-500/30 bg-emerald-500/10 px-1.5 py-[1px] text-emerald-200">
                    {p.provider}
                  </span>
                  {p.hasSsh && (
                    <span className="rounded border border-sky-500/30 bg-sky-500/10 px-1.5 py-[1px] text-sky-200">
                      SSH
                    </span>
                  )}
                  <span className="ml-auto normal-case tracking-normal text-[10px] text-surface-500">
                    since {new Date(p.setupAt).toLocaleDateString()}
                  </span>
                </div>
              </div>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
