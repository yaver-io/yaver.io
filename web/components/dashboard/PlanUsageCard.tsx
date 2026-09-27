"use client";

/**
 * PlanUsageCard — the settings "Plan & usage" section (web only, by design:
 * the phone shows connection state, the dashboard is where account/plan
 * questions get answered — like the Claude Code / Codex web UIs).
 *
 * Data comes from the relay's /my/bandwidth via agentClient.fetchMyRelayUsage:
 * the caller's Convex-verified plan plus usage rows scoped to their OWN
 * devices. Owner accounts see "no limits" AND their real numbers — "am I
 * capped" and "how much am I moving" are different questions and the
 * 2026-07-27 incident (a poll bug silently burning 1.9GB) is exactly why the
 * second one stays visible for everyone.
 */

import { useEffect, useState } from "react";
import { agentClient } from "@/lib/agent-client";

type UsageRow = { deviceId: string; usedMb: number; limitMb: number; isPaid: boolean; unmetered?: boolean };
type Usage = { plan: string; isPaid: boolean; unmetered: boolean; accountUsedMb: number; accountLimitMb: number; devices: UsageRow[] };

const PLAN_LABEL: Record<string, string> = {
  "owner-dev": "Owner",
  "cloud-workspace": "Legacy hosted plan",
  "relay-pro": "Relay Pro",
  free: "Free",
};

export function PlanUsageCard({ deviceNames }: { deviceNames?: Record<string, string> }) {
  const [usage, setUsage] = useState<Usage | null | undefined>(undefined);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    agentClient
      .fetchMyRelayUsage()
      .then((data) => { if (!cancelled) setUsage(data); })
      .catch((err) => { if (!cancelled) { setUsage(null); setError(err instanceof Error ? err.message : String(err)); } });
    return () => { cancelled = true; };
  }, []);

  const planLabel = usage ? (PLAN_LABEL[usage.plan] || usage.plan || "Free") : null;
  const isOwner = usage?.plan === "owner-dev";
  const accountPct = !usage || usage.unmetered || usage.accountLimitMb <= 0
    ? 0
    : Math.min(100, Math.round((usage.accountUsedMb / usage.accountLimitMb) * 100));

  return (
    <section className="mb-4 rounded-lg border border-surface-800 bg-surface-900/60 p-4">
      <div className="flex items-center justify-between gap-2">
        <h2 className="text-sm font-semibold text-surface-200">Plan &amp; usage</h2>
        {usage ? (
          <span
            className={`rounded-full border px-2.5 py-0.5 text-[11px] font-semibold ${
              isOwner
                ? "border-emerald-500/40 bg-emerald-500/10 text-emerald-600 dark:text-emerald-300"
                : usage.isPaid
                  ? "border-indigo-500/40 bg-indigo-500/10 text-indigo-600 dark:text-indigo-300"
                  : "border-surface-700 bg-surface-800/60 text-surface-300"
            }`}
          >
            {planLabel}
          </span>
        ) : null}
      </div>

      {usage === undefined ? (
        <p className="mt-2 text-[12px] text-surface-500">Loading usage…</p>
      ) : usage === null ? (
        <p className="mt-2 text-[12px] text-surface-500">
          {error
            ? `Couldn't load usage: ${error}`
            : "Connect to a device over the relay to see this account's plan and data usage."}
        </p>
      ) : (
        <>
          <p className="mt-1.5 text-[12px] text-surface-400">
            {isOwner
              ? "You are the owner — relay traffic is unmetered for your account, on every device and every lane. Usage is still recorded so a runaway loop stays visible:"
              : usage.isPaid
                ? `Paid plan — ${usage.accountUsedMb || 0} MB of ${usage.accountLimitMb || 0} MB used across your account today. Device breakdown:`
                : `Free plan — ${usage.accountUsedMb || 0} MB of ${usage.accountLimitMb || 0} MB used across your account today. Same-LAN connections don't count. Device breakdown:`}
          </p>
          {!isOwner && usage.accountLimitMb > 0 ? (
            <div className="mt-2 h-1.5 w-full overflow-hidden rounded-full bg-surface-800">
              <span
                className={`block h-full rounded-full ${accountPct >= 90 ? "bg-rose-500" : accountPct >= 70 ? "bg-amber-500" : "bg-emerald-500"}`}
                style={{ width: `${accountPct}%` }}
              />
            </div>
          ) : null}
          <ul className="mt-2 space-y-1.5">
            {usage.devices.length === 0 ? (
              <li className="text-[12px] text-surface-500">No devices are connected through the relay right now.</li>
            ) : (
              usage.devices.map((d) => {
                const name = deviceNames?.[d.deviceId] || `${d.deviceId.slice(0, 8)}…`;
                const uncapped = d.unmetered || isOwner;
                return (
                  <li key={d.deviceId} className="flex items-center gap-3 text-[12px]">
                    <span className="min-w-0 flex-1 truncate text-surface-300">{name}</span>
                    <span className="tabular-nums text-surface-400">
                      {d.usedMb} MB{uncapped ? " · no limit" : ""}
                    </span>
                    {uncapped ? (
                      <span className="text-[10px] font-semibold uppercase tracking-wide text-emerald-600 dark:text-emerald-400">∞</span>
                    ) : null}
                  </li>
                );
              })
            )}
          </ul>
        </>
      )}
    </section>
  );
}
