"use client";

// BillingView — #11: a Cloudflare-style billing section. Read-only
// view of the subscription + every managed cloud resource (status,
// onboarding phase, runner-auth), with the self-heal and decommission
// actions inline. Reuses GET /subscription (already returns
// subscription + machines) — no new backend. Honest about test mode.

import { useCallback, useEffect, useState } from "react";
import { CONVEX_URL } from "@/lib/constants";

interface SubMachine {
  id: string;
  machineType?: string;
  status?: string;
  region?: string;
  hetznerServerId?: string;
  provisionPhase?: string | null;
  provisionProgress?: number | null;
  runnersAuthorized?: boolean;
  errorMessage?: string;
}
interface SubResp {
  subscription?: { status?: string; plan?: string; currentPeriodEnd?: number } | null;
  machines?: SubMachine[];
}

export default function BillingView({ token }: { token: string | null | undefined }) {
  const [data, setData] = useState<SubResp>({});
  const [busy, setBusy] = useState<string | null>(null);
  const [msg, setMsg] = useState<string | null>(null);

  const load = useCallback(async () => {
    if (!token) return;
    try {
      const res = await fetch(`${CONVEX_URL}/subscription`, {
        headers: { Authorization: `Bearer ${token}` },
      });
      setData(await res.json().catch(() => ({})));
    } catch (e: any) {
      setMsg(e?.message || String(e));
    }
  }, [token]);

  useEffect(() => {
    void load();
    const iv = setInterval(() => void load(), 8000);
    return () => clearInterval(iv);
  }, [load]);

  const act = async (label: string, path: string, body: Record<string, unknown>) => {
    setBusy(label);
    setMsg(null);
    try {
      const res = await fetch(`${CONVEX_URL}${path}`, {
        method: "POST",
        headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/json" },
        body: JSON.stringify(body),
      });
      const j = await res.json().catch(() => ({}));
      setMsg(res.ok ? `✓ ${label}` : `✗ ${j?.error || res.status}`);
      await load();
    } catch (e: any) {
      setMsg(`✗ ${e?.message || String(e)}`);
    } finally {
      setBusy(null);
    }
  };

  const sub = data.subscription;
  const machines = data.machines ?? [];
  const active = machines.filter((m) => m.status === "active").length;

  return (
    <div className="space-y-4">
      <div>
        <h2 className="text-lg font-semibold text-slate-800 dark:text-surface-100">Billing</h2>
        <p className="text-xs text-slate-500 dark:text-surface-400">
          Subscription &amp; managed cloud resources. Charges are
          metered by Lemon Squeezy; managed boxes bill hourly on the
          underlying provider while they exist.
        </p>
      </div>

      {/* Subscription card */}
      <div className="rounded-xl border border-slate-300 bg-white/60 p-4 dark:border-surface-700 dark:bg-[rgba(20,21,27,0.6)]">
        <div className="mb-2 text-xs font-semibold uppercase tracking-wider text-slate-500 dark:text-surface-400">
          Subscription
        </div>
        {sub ? (
          <div className="flex flex-wrap items-center gap-4 text-sm">
            <span>
              Plan:{" "}
              <span className="font-mono">{sub.plan ?? "—"}</span>
            </span>
            <span>
              Status:{" "}
              <span
                className={`rounded px-1.5 py-0.5 text-xs font-semibold ${
                  sub.status === "active"
                    ? "bg-emerald-500/15 text-emerald-700 dark:text-emerald-300"
                    : "bg-amber-500/15 text-amber-700 dark:text-amber-300"
                }`}
              >
                {sub.status ?? "unknown"}
              </span>
            </span>
            {sub.currentPeriodEnd ? (
              <span className="text-slate-500 dark:text-surface-400">
                Renews {new Date(sub.currentPeriodEnd).toLocaleDateString()}
              </span>
            ) : null}
          </div>
        ) : (
          <p className="text-sm text-slate-500 dark:text-surface-400">
            No active subscription. Buy a managed box from Devices →
            Managed cloud.
          </p>
        )}
      </div>

      {/* Resources card */}
      <div className="rounded-xl border border-slate-300 bg-white/60 p-4 dark:border-surface-700 dark:bg-[rgba(20,21,27,0.6)]">
        <div className="mb-2 flex items-center justify-between">
          <span className="text-xs font-semibold uppercase tracking-wider text-slate-500 dark:text-surface-400">
            Managed cloud resources ({active} active / {machines.length})
          </span>
          <button
            disabled={busy !== null}
            onClick={() => void act("re-provision missing", "/billing/yaver-cloud/reconcile", {})}
            className="rounded border border-slate-300 px-2 py-0.5 text-[11px] font-semibold disabled:opacity-50 dark:border-surface-700"
          >
            {busy === "re-provision missing" ? "…" : "Re-provision missing"}
          </button>
        </div>
        {machines.length === 0 ? (
          <p className="text-sm text-slate-500 dark:text-surface-400">
            No managed resources.
          </p>
        ) : (
          <div className="space-y-2">
            {machines.map((m) => (
              <div
                key={m.id}
                className="flex flex-wrap items-center justify-between gap-2 rounded-md border border-slate-200 px-3 py-2 text-xs dark:border-surface-800"
              >
                <span className="font-mono">
                  {m.machineType ?? "cpu"} · {m.region ?? "eu"} · srv{" "}
                  {m.hetznerServerId ?? "—"} ·{" "}
                  <span
                    className={
                      m.status === "error"
                        ? "font-semibold text-rose-600 dark:text-rose-400"
                        : m.status === "active"
                          ? "font-semibold text-emerald-600 dark:text-emerald-400"
                          : ""
                    }
                  >
                    {m.status ?? "?"}
                  </span>
                  {m.provisionPhase && m.status !== "active" && m.status !== "stopped"
                    ? ` (${m.provisionPhase}${
                        typeof m.provisionProgress === "number"
                          ? ` ${m.provisionProgress}%`
                          : ""
                      })`
                    : ""}
                  {m.status === "active" && m.runnersAuthorized === false
                    ? " · ⚠ runners unauthorized"
                    : ""}
                </span>
                {m.status !== "stopped" && m.status !== "stopping" ? (
                  <button
                    disabled={busy !== null}
                    onClick={() => {
                      if (
                        !window.confirm(
                          `Decommission this box (srv ${m.hetznerServerId ?? "—"})? ` +
                            `Destroys the server, stops billing, and cancels the subscription. Cannot be undone.`,
                        )
                      )
                        return;
                      void act("decommission", "/billing/yaver-cloud/dev-deprovision", {
                        machineId: m.id,
                      });
                    }}
                    className="rounded border border-rose-400/50 px-2 py-0.5 font-semibold text-rose-600 disabled:opacity-50 dark:text-rose-400"
                  >
                    ♻ Decommission
                  </button>
                ) : (
                  <span className="text-[10px] text-slate-400">{m.status}</span>
                )}
              </div>
            ))}
          </div>
        )}
      </div>

      {msg ? (
        <p
          className={`text-xs ${
            msg.startsWith("✗") ? "text-rose-600 dark:text-rose-400" : "text-emerald-600 dark:text-emerald-400"
          }`}
        >
          {msg}
        </p>
      ) : null}
    </div>
  );
}
