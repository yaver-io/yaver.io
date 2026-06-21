"use client";

// StoresView — the store-onboarding concierge checklist. Renders the
// catalogue the agent serves at /stores (single source of truth in Go's
// setup_guide.go) and, for every step Yaver can't do for you (legal
// identity, payment, store review), routes you to the EXACT official
// Apple/Google page. Status is best-effort from your device's vault.

import { useCallback, useEffect, useState } from "react";
import { AgentClient, type StoreTask } from "@/lib/agent-client";

const STATUS: Record<
  StoreTask["status"],
  { glyph: string; label: string; cls: string }
> = {
  done: { glyph: "✓", label: "done", cls: "bg-emerald-500/15 text-emerald-700 dark:text-emerald-300" },
  todo: { glyph: "○", label: "to do", cls: "bg-sky-500/15 text-sky-700 dark:text-sky-300" },
  action: { glyph: "◆", label: "your action", cls: "bg-amber-500/15 text-amber-700 dark:text-amber-300" },
  blocked: { glyph: "⋯", label: "blocked", cls: "bg-slate-500/15 text-slate-500 dark:text-surface-400" },
  unknown: { glyph: "·", label: "—", cls: "bg-slate-500/10 text-slate-400 dark:text-surface-500" },
};

const AUTOMATION: Record<StoreTask["automation"], { label: string; cls: string }> = {
  auto: { label: "Yaver does it", cls: "bg-violet-500/15 text-violet-700 dark:text-violet-300" },
  assisted: { label: "guided", cls: "bg-sky-500/15 text-sky-700 dark:text-sky-300" },
  manual: { label: "you (legal/payment)", cls: "bg-amber-500/15 text-amber-700 dark:text-amber-300" },
};

const GROUPS: { key: StoreTask["platform"]; label: string }[] = [
  { key: "apple", label: "Apple" },
  { key: "google", label: "Google" },
  { key: "both", label: "Cross-platform" },
];

function TaskCard({ t }: { t: StoreTask }) {
  const [open, setOpen] = useState(false);
  const [copied, setCopied] = useState(false);
  const st = STATUS[t.status] ?? STATUS.unknown;
  const auto = AUTOMATION[t.automation];

  const copyCmd = async () => {
    if (!t.yaverCmd) return;
    try {
      await navigator.clipboard.writeText(t.yaverCmd);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {
      /* clipboard blocked — non-fatal */
    }
  };

  return (
    <div className="rounded-lg border border-slate-200 px-3 py-2.5 dark:border-surface-800">
      <button
        onClick={() => setOpen((v) => !v)}
        className="flex w-full items-start justify-between gap-3 text-left"
      >
        <div className="flex min-w-0 items-start gap-2">
          <span className={`mt-0.5 inline-flex h-5 w-5 shrink-0 items-center justify-center rounded-full text-xs ${st.cls}`}>
            {st.glyph}
          </span>
          <div className="min-w-0">
            <div className="font-medium text-slate-800 dark:text-surface-100">{t.title}</div>
            <div className="mt-0.5 line-clamp-2 text-xs text-slate-500 dark:text-surface-400">{t.summary}</div>
          </div>
        </div>
        <span className={`shrink-0 rounded px-1.5 py-0.5 text-[10px] font-semibold ${auto.cls}`}>{auto.label}</span>
      </button>

      {open ? (
        <div className="mt-3 space-y-3 border-t border-slate-200 pt-3 dark:border-surface-800">
          {t.dependsOn && t.dependsOn.length > 0 ? (
            <div className="text-[11px] text-slate-400 dark:text-surface-500">
              Needs first: {t.dependsOn.join(", ")}
            </div>
          ) : null}
          {t.steps && t.steps.length > 0 ? (
            <ol className="list-decimal space-y-1 pl-5 text-xs text-slate-600 dark:text-surface-300">
              {t.steps.map((s, i) => (
                <li key={i}>{s}</li>
              ))}
            </ol>
          ) : null}
          {t.yaverCmd ? (
            <div className="flex items-center gap-2">
              <code className="flex-1 overflow-x-auto rounded bg-slate-100 px-2 py-1 text-[11px] text-slate-700 dark:bg-surface-900 dark:text-surface-200">
                {t.yaverCmd}
              </code>
              <button
                onClick={copyCmd}
                className="shrink-0 rounded border border-slate-300 px-2 py-1 text-[11px] font-semibold text-slate-600 dark:border-surface-700 dark:text-surface-300"
              >
                {copied ? "✓" : "copy"}
              </button>
            </div>
          ) : null}
          {t.needsSecret && t.needsSecret.length > 0 ? (
            <div className="text-[11px] text-slate-400 dark:text-surface-500">
              Done when these are in your vault: {t.needsSecret.join(", ")}
            </div>
          ) : null}
          {t.routeUrl ? (
            <a
              href={t.routeUrl}
              target="_blank"
              rel="noreferrer"
              className="inline-flex items-center gap-1 rounded-lg border border-sky-500/50 bg-sky-500/10 px-3 py-1.5 text-xs font-semibold text-sky-700 hover:bg-sky-500/20 dark:text-sky-300"
            >
              Open the {t.platform === "apple" ? "Apple" : t.platform === "google" ? "Google" : "official"} page ↗
            </a>
          ) : null}
        </div>
      ) : null}
    </div>
  );
}

export default function StoresView({ token: _token }: { token: string | null | undefined }) {
  const [tasks, setTasks] = useState<StoreTask[] | null>(null);
  const [err, setErr] = useState<string | null>(null);

  const load = useCallback(async () => {
    setErr(null);
    try {
      const client = new AgentClient();
      const t = await client.getStores();
      setTasks(t);
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
      setTasks([]);
    }
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  return (
    <div className="space-y-4">
      <div>
        <h2 className="text-lg font-semibold text-slate-800 dark:text-surface-100">Publish to the stores</h2>
        <p className="text-xs text-slate-500 dark:text-surface-400">
          Everything to get your app onto the App Store &amp; Google Play. Yaver does what it can; for
          the parts only you can do (identity, payment), it opens the exact official page. Status comes
          from your connected device&apos;s vault.
        </p>
      </div>

      {err ? (
        <p className="rounded-md border border-amber-500/30 bg-amber-500/5 px-2.5 py-1.5 text-xs text-amber-700 dark:text-amber-300">
          Couldn&apos;t reach your device agent ({err}). Connect a device to see live status — the steps
          and links below still apply.
        </p>
      ) : null}

      {tasks === null ? (
        <p className="text-sm text-slate-500 dark:text-surface-400">Loading…</p>
      ) : tasks.length === 0 && !err ? (
        <p className="text-sm text-slate-500 dark:text-surface-400">No setup steps available.</p>
      ) : (
        GROUPS.map((g) => {
          const rows = tasks.filter((t) => t.platform === g.key);
          if (rows.length === 0) return null;
          return (
            <div key={g.key} className="rounded-xl border border-slate-300 bg-white/60 p-4 dark:border-surface-700 dark:bg-[rgba(20,21,27,0.6)]">
              <div className="mb-3 text-xs font-semibold uppercase tracking-wider text-slate-500 dark:text-surface-400">
                {g.label}
              </div>
              <div className="space-y-2">
                {rows.map((t) => (
                  <TaskCard key={t.id} t={t} />
                ))}
              </div>
            </div>
          );
        })
      )}
    </div>
  );
}
