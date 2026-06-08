"use client";

// BillingView — OpenAI-developer-portal-style "Billing & usage" page.
//
// Sections, top to bottom:
//   1. Credit balance hero — prepaid wallet balance, add-credit packs,
//      burn rate + estimated days left, lifetime added/used.
//   2. Usage — per-meter spend breakdown (compute / inference / backend /
//      web / publish) over a selectable window, with an honest
//      real-vs-simulated split (metering is dry-run until launch — we
//      never claim a charge that didn't happen).
//   3. Activity — merged ledger of top-ups (credits) and metering ticks
//      (debits).
//   4. Subscription + managed cloud resources — the existing read-only
//      view with pause / resume / decommission actions.
//
// Reads are best-effort: the credit/usage endpoints are private-preview
// (403 for non-cloud users) — those sections simply don't render rather
// than erroring. No new deps; inline SVG icons only.

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

interface Wallet {
  balanceCents: number;
  totalAddedCents: number;
  totalUsedCents: number;
  currency?: string;
  estimatedHourlyCents?: number;
  reservedCents?: number;
  lowBalance?: boolean;
  lastTopupAt?: number;
  lastMeteredAt?: number;
}
interface Cockpit {
  windowChargedCents: number;
  estPerDayCents: number;
  estDaysLeft: number | null;
  lowBalance: boolean;
  empty: boolean;
}
interface BurnRow {
  kind: string;
  chargedCents: number;
  providerCostCents: number;
  quantity: number;
  count: number;
  dryRunCents: number;
}
interface Burn {
  rows: BurnRow[];
  totalChargedCents: number;
  realChargedCents: number;
  totalDryRunCents: number;
}
interface UsageTick {
  machineId: string | null;
  date: string;
  state: string;
  seconds: number;
  chargedCents: number;
  ratePerHourCents: number;
  dryRun: boolean;
  createdAt: number;
}
interface Topup {
  orderId: string;
  source: string;
  packId: string | null;
  amountCents: number;
  createdAt: number;
}
interface Pack {
  id: string;
  cents: number;
  label: string;
}

const money = (cents: number | null | undefined) =>
  typeof cents === "number" ? `$${(cents / 100).toFixed(2)}` : "—";

// Human labels for the five meter kinds (managedMeter.ts + creditUsage).
const METER: Record<string, { label: string; tone: string }> = {
  compute: { label: "Compute · managed boxes", tone: "sky" },
  inference: { label: "AI inference", tone: "violet" },
  backend: { label: "Managed backend", tone: "emerald" },
  web: { label: "Managed web", tone: "amber" },
  publish: { label: "App publishing", tone: "fuchsia" },
};
const meterMeta = (kind: string) =>
  METER[kind] ?? { label: kind, tone: "slate" };

// Tailwind can't see runtime-built class names, so map tones to full
// literal classes (both modes).
const BAR_FILL: Record<string, string> = {
  sky: "bg-sky-500",
  violet: "bg-violet-500",
  emerald: "bg-emerald-500",
  amber: "bg-amber-500",
  fuchsia: "bg-fuchsia-500",
  slate: "bg-slate-400 dark:bg-surface-500",
};

function Section({
  title,
  right,
  children,
}: {
  title: string;
  right?: React.ReactNode;
  children: React.ReactNode;
}) {
  return (
    <div className="rounded-xl border border-slate-300 bg-white/60 p-4 dark:border-surface-700 dark:bg-[rgba(20,21,27,0.6)]">
      <div className="mb-3 flex items-center justify-between gap-2">
        <span className="text-xs font-semibold uppercase tracking-wider text-slate-500 dark:text-surface-400">
          {title}
        </span>
        {right}
      </div>
      {children}
    </div>
  );
}

export default function BillingView({ token }: { token: string | null | undefined }) {
  const [data, setData] = useState<SubResp>({});
  const [busy, setBusy] = useState<string | null>(null);
  const [msg, setMsg] = useState<string | null>(null);

  const [wallet, setWallet] = useState<Wallet | null>(null);
  const [cockpit, setCockpit] = useState<Cockpit | null>(null);
  const [burn, setBurn] = useState<Burn | null>(null);
  const [usage, setUsage] = useState<UsageTick[]>([]);
  const [topups, setTopups] = useState<Topup[]>([]);
  const [packs, setPacks] = useState<Pack[]>([]);
  const [days, setDays] = useState(30);
  const [hasCredit, setHasCredit] = useState<boolean | null>(null);

  const authedGet = useCallback(
    async (path: string) => {
      const res = await fetch(`${CONVEX_URL}${path}`, {
        headers: { Authorization: `Bearer ${token}` },
      });
      if (!res.ok) return { __status: res.status } as any;
      return res.json().catch(() => ({}));
    },
    [token],
  );

  const loadSub = useCallback(async () => {
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

  const loadWallet = useCallback(async () => {
    if (!token) return;
    try {
      const [b, c, br, u, p] = await Promise.all([
        authedGet(`/billing/yaver-cloud/balance`),
        authedGet(`/managed/cockpit?days=${days}`),
        authedGet(`/managed/burn?days=${days}`),
        authedGet(`/billing/yaver-cloud/usage`),
        authedGet(`/billing/credits/packs`),
      ]);
      // 403 on balance = not in the cloud preview → no wallet to show.
      if (b?.__status === 403) {
        setHasCredit(false);
        return;
      }
      setHasCredit(true);
      if (!b?.__status) setWallet(b as Wallet);
      if (!c?.__status) setCockpit(c as Cockpit);
      if (!br?.__status) setBurn(br as Burn);
      if (!u?.__status) {
        setUsage(Array.isArray(u?.usage) ? u.usage : []);
        setTopups(Array.isArray(u?.topups) ? u.topups : []);
      }
      if (!p?.__status && Array.isArray(p?.packs)) setPacks(p.packs);
    } catch {
      /* non-fatal — sections just stay hidden */
    }
  }, [token, days, authedGet]);

  useEffect(() => {
    void loadSub();
    const iv = setInterval(() => void loadSub(), 12000);
    return () => clearInterval(iv);
  }, [loadSub]);

  useEffect(() => {
    void loadWallet();
  }, [loadWallet]);

  // Add credit → one-time LemonSqueezy pack checkout; webhook credits
  // the wallet on payment. Mirrors ManagedCloudPanel.addCredit.
  const addCredit = async (packId: string) => {
    setBusy(`pack:${packId}`);
    setMsg(null);
    try {
      const res = await fetch(`${CONVEX_URL}/billing/credits/checkout`, {
        method: "POST",
        headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/json" },
        body: JSON.stringify({ packId }),
      });
      const j = await res.json().catch(() => ({}));
      if (!res.ok || !j?.url) {
        setMsg(
          res.status === 503
            ? "✗ Credit packs aren't configured yet (owner: set the LemonSqueezy pack variant ids)."
            : `✗ ${j?.error || "Couldn't start top-up. Please try again."}`,
        );
        return;
      }
      window.location.href = j.url;
    } catch (e: any) {
      setMsg(`✗ ${e?.message || String(e)}`);
    } finally {
      setBusy(null);
    }
  };

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
      await Promise.all([loadSub(), loadWallet()]);
    } catch (e: any) {
      setMsg(`✗ ${e?.message || String(e)}`);
    } finally {
      setBusy(null);
    }
  };

  const sub = data.subscription;
  const allMachines = data.machines ?? [];
  const machines = allMachines.filter((m) => m.status !== "stopped");
  const active = machines.filter((m) => m.status === "active").length;
  const noBox = sub?.status === "active" && machines.length === 0;

  const packOptions: Pack[] = packs.length
    ? packs
    : [
        { id: "p10", cents: 1000, label: "$10" },
        { id: "p25", cents: 2500, label: "$25" },
        { id: "p50", cents: 5000, label: "$50" },
      ];

  // Merge top-ups + usage ticks into one reverse-chron activity feed.
  const activity = [
    ...topups.map((t) => ({
      kind: "topup" as const,
      at: t.createdAt,
      amountCents: t.amountCents,
      label: t.packId ? `Credit pack ${t.packId}` : "Credit added",
      source: t.source,
      dryRun: false,
    })),
    ...usage.map((u) => ({
      kind: "usage" as const,
      at: u.createdAt,
      amountCents: -u.chargedCents,
      label: `Compute · ${u.state} · ${Math.round(u.seconds / 60)} min`,
      source: u.machineId ? `box ${u.machineId.slice(-6)}` : "",
      dryRun: u.dryRun,
    })),
  ]
    .sort((a, b) => b.at - a.at)
    .slice(0, 24);

  const realBurn = burn ? burn.realChargedCents : 0;
  const simBurn = burn ? burn.totalDryRunCents : 0;
  const burnMax = burn && burn.rows.length ? Math.max(...burn.rows.map((r) => r.chargedCents), 1) : 1;

  return (
    <div className="space-y-4">
      <div>
        <h2 className="text-lg font-semibold text-slate-800 dark:text-surface-100">Billing &amp; usage</h2>
        <p className="text-xs text-slate-500 dark:text-surface-400">
          Prepaid credit, metered usage, and your managed cloud
          resources. Top-ups are processed by Lemon Squeezy; compute is
          billed from your balance while a box exists.
        </p>
      </div>

      {/* ── 1 · Credit balance ─────────────────────────────────────── */}
      {hasCredit !== false ? (
        <Section title="Credit balance">
          <div className="flex flex-wrap items-end justify-between gap-4">
            <div>
              <div className="flex items-baseline gap-2">
                <span className="text-3xl font-bold tabular-nums text-slate-900 dark:text-surface-50">
                  {money(wallet?.balanceCents ?? 0)}
                </span>
                <span className="text-xs uppercase text-slate-400 dark:text-surface-500">
                  {(wallet?.currency ?? "usd").toUpperCase()}
                </span>
              </div>
              <div className="mt-1 flex flex-wrap gap-x-4 gap-y-1 text-[11px] text-slate-500 dark:text-surface-400">
                {cockpit && cockpit.estPerDayCents > 0 ? (
                  <span>≈ {money(cockpit.estPerDayCents)}/day recent pace</span>
                ) : null}
                {cockpit && cockpit.estDaysLeft !== null ? (
                  <span>
                    ≈ {cockpit.estDaysLeft} day{cockpit.estDaysLeft === 1 ? "" : "s"} left
                  </span>
                ) : null}
                {typeof wallet?.estimatedHourlyCents === "number" ? (
                  <span>{money(wallet.estimatedHourlyCents)}/hr while running</span>
                ) : null}
              </div>
            </div>
            <div className="flex flex-col items-end gap-1.5">
              <span className="text-[10px] font-semibold uppercase tracking-wider text-slate-400 dark:text-surface-500">
                Add to balance
              </span>
              <div className="flex flex-wrap gap-1.5">
                {packOptions.map((p) => (
                  <button
                    key={p.id}
                    disabled={busy !== null}
                    onClick={() => void addCredit(p.id)}
                    className="rounded-lg border border-sky-500/50 bg-sky-500/10 px-3 py-1.5 text-sm font-semibold text-sky-700 transition-colors hover:bg-sky-500/20 disabled:opacity-50 dark:text-sky-300"
                  >
                    {busy === `pack:${p.id}` ? "…" : `+ ${p.label}`}
                  </button>
                ))}
              </div>
            </div>
          </div>

          {wallet?.lowBalance || cockpit?.lowBalance ? (
            <p className="mt-3 rounded-md border border-amber-500/30 bg-amber-500/5 px-2.5 py-1.5 text-xs text-amber-700 dark:text-amber-300">
              ⚠ Low balance — a running box auto-pauses before it hits zero
              (it snapshots first, so nothing is lost). Top up to keep it live.
            </p>
          ) : null}

          <div className="mt-3 grid grid-cols-2 gap-3 border-t border-slate-200 pt-3 text-xs sm:grid-cols-3 dark:border-surface-800">
            <div>
              <div className="text-[10px] uppercase tracking-wider text-slate-400 dark:text-surface-500">
                Lifetime added
              </div>
              <div className="font-semibold tabular-nums text-slate-700 dark:text-surface-200">
                {money(wallet?.totalAddedCents ?? 0)}
              </div>
            </div>
            <div>
              <div className="text-[10px] uppercase tracking-wider text-slate-400 dark:text-surface-500">
                Lifetime used
              </div>
              <div className="font-semibold tabular-nums text-slate-700 dark:text-surface-200">
                {money(wallet?.totalUsedCents ?? 0)}
              </div>
            </div>
            <div>
              <div className="text-[10px] uppercase tracking-wider text-slate-400 dark:text-surface-500">
                Last top-up
              </div>
              <div className="font-semibold text-slate-700 dark:text-surface-200">
                {wallet?.lastTopupAt
                  ? new Date(wallet.lastTopupAt).toLocaleDateString()
                  : "—"}
              </div>
            </div>
          </div>
        </Section>
      ) : null}

      {/* ── 2 · Usage breakdown ────────────────────────────────────── */}
      {hasCredit !== false ? (
        <Section
          title="Usage"
          right={
            <div className="flex gap-1">
              {[7, 30, 90].map((d) => (
                <button
                  key={d}
                  onClick={() => setDays(d)}
                  className={`rounded px-2 py-0.5 text-[11px] font-semibold ${
                    days === d
                      ? "bg-slate-800 text-white dark:bg-surface-200 dark:text-surface-950"
                      : "text-slate-500 hover:bg-slate-100 dark:text-surface-400 dark:hover:bg-surface-800"
                  }`}
                >
                  {d}d
                </button>
              ))}
            </div>
          }
        >
          <div className="mb-3 flex flex-wrap items-baseline gap-x-4 gap-y-1">
            <span className="text-2xl font-bold tabular-nums text-slate-900 dark:text-surface-50">
              {money(realBurn)}
            </span>
            <span className="text-xs text-slate-500 dark:text-surface-400">
              charged · last {days} days
            </span>
            {simBurn > 0 ? (
              <span className="rounded bg-slate-500/15 px-1.5 py-0.5 text-[11px] font-semibold text-slate-600 dark:text-surface-300">
                + {money(simBurn)} simulated (metering preview)
              </span>
            ) : null}
          </div>

          {burn && burn.rows.length ? (
            <div className="space-y-2.5">
              {burn.rows.map((r) => {
                const m = meterMeta(r.kind);
                const pct = Math.max(2, Math.round((r.chargedCents / burnMax) * 100));
                const isSim = r.dryRunCents >= r.chargedCents && r.chargedCents > 0;
                return (
                  <div key={r.kind}>
                    <div className="mb-1 flex items-center justify-between text-xs">
                      <span className="font-medium text-slate-700 dark:text-surface-200">
                        {m.label}
                        {isSim ? (
                          <span className="ml-1.5 rounded bg-slate-500/15 px-1 py-0.5 text-[9px] font-semibold uppercase text-slate-500 dark:text-surface-400">
                            sim
                          </span>
                        ) : null}
                      </span>
                      <span className="tabular-nums text-slate-500 dark:text-surface-400">
                        {money(r.chargedCents)}
                        <span className="ml-1 text-[10px] text-slate-400 dark:text-surface-500">
                          · {r.count}×
                        </span>
                      </span>
                    </div>
                    <div className="h-1.5 w-full overflow-hidden rounded-full bg-slate-200 dark:bg-surface-800">
                      <div
                        className={`h-full rounded-full ${BAR_FILL[m.tone] ?? BAR_FILL.slate} ${isSim ? "opacity-40" : ""}`}
                        style={{ width: `${pct}%` }}
                      />
                    </div>
                  </div>
                );
              })}
            </div>
          ) : (
            <p className="text-sm text-slate-500 dark:text-surface-400">
              No usage in this window yet.
            </p>
          )}
        </Section>
      ) : null}

      {/* ── 3 · Activity ───────────────────────────────────────────── */}
      {hasCredit !== false && activity.length > 0 ? (
        <Section title="Activity">
          <div className="-my-1 divide-y divide-slate-200 dark:divide-surface-800">
            {activity.map((a, i) => (
              <div key={i} className="flex items-center justify-between gap-3 py-2 text-xs">
                <div className="flex min-w-0 items-center gap-2">
                  <span
                    className={`flex h-6 w-6 shrink-0 items-center justify-center rounded-full text-[11px] ${
                      a.kind === "topup"
                        ? "bg-emerald-500/15 text-emerald-700 dark:text-emerald-300"
                        : "bg-slate-500/15 text-slate-500 dark:text-surface-300"
                    }`}
                  >
                    {a.kind === "topup" ? "+" : "−"}
                  </span>
                  <div className="min-w-0">
                    <div className="truncate font-medium text-slate-700 dark:text-surface-200">
                      {a.label}
                      {a.dryRun ? (
                        <span className="ml-1.5 rounded bg-slate-500/15 px-1 py-0.5 text-[9px] font-semibold uppercase text-slate-500 dark:text-surface-400">
                          sim
                        </span>
                      ) : null}
                    </div>
                    <div className="truncate text-[10px] text-slate-400 dark:text-surface-500">
                      {new Date(a.at).toLocaleString()}
                      {a.source ? ` · ${a.source}` : ""}
                    </div>
                  </div>
                </div>
                <span
                  className={`shrink-0 tabular-nums font-semibold ${
                    a.amountCents >= 0
                      ? "text-emerald-600 dark:text-emerald-400"
                      : "text-slate-500 dark:text-surface-300"
                  }`}
                >
                  {a.amountCents >= 0 ? "+" : "−"}
                  {money(Math.abs(a.amountCents))}
                </span>
              </div>
            ))}
          </div>
        </Section>
      ) : null}

      {/* ── 4 · Subscription ───────────────────────────────────────── */}
      <Section title="Subscription">
        {sub ? (
          <>
            <div className="flex flex-wrap items-center gap-4 text-sm text-slate-700 dark:text-surface-200">
              <span>
                Plan: <span className="font-mono">{sub.plan ?? "—"}</span>
              </span>
              <span>
                Status:{" "}
                <span
                  className={`rounded px-1.5 py-0.5 text-xs font-semibold ${
                    sub.status === "active" && !noBox
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
            {noBox ? (
              <p className="mt-2 rounded-md border border-amber-500/30 bg-amber-500/5 px-2.5 py-1.5 text-xs text-amber-700 dark:text-amber-300">
                ⚠ This subscription is active but has no managed box — it
                keeps renewing unused. Buy or re-provision a box from
                Devices → Managed cloud, or decommission it to stop billing.
              </p>
            ) : null}
          </>
        ) : (
          <p className="text-sm text-slate-500 dark:text-surface-400">
            No subscription — managed cloud is prepaid (no recurring
            charge). Add credit above and spin up a box from Devices →
            Managed cloud.
          </p>
        )}
      </Section>

      {/* ── 5 · Managed cloud resources ────────────────────────────── */}
      <Section
        title={`Managed cloud resources (${active} active / ${machines.length})`}
        right={
          <button
            disabled={busy !== null}
            onClick={() => void act("re-provision missing", "/billing/yaver-cloud/reconcile", {})}
            className="rounded border border-slate-300 px-2 py-0.5 text-[11px] font-semibold disabled:opacity-50 dark:border-surface-700"
          >
            {busy === "re-provision missing" ? "…" : "Re-provision missing"}
          </button>
        }
      >
        {machines.length === 0 ? (
          <p className="text-sm text-slate-500 dark:text-surface-400">No managed resources.</p>
        ) : (
          <div className="space-y-2">
            {machines.map((m) => (
              <div
                key={m.id}
                className="flex flex-wrap items-center justify-between gap-2 rounded-md border border-slate-200 px-3 py-2 text-xs dark:border-surface-800"
              >
                <span className="font-mono text-slate-600 dark:text-surface-300">
                  {m.machineType ?? "cpu"} · {m.region ?? "eu"} · resource{" "}
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
                  {m.provisionPhase && (m.status === "provisioning" || m.status === "error")
                    ? ` (${m.provisionPhase}${
                        typeof m.provisionProgress === "number" ? ` ${m.provisionProgress}%` : ""
                      })`
                    : ""}
                  {m.status === "active" && m.runnersAuthorized === false
                    ? " · ⚠ runners unauthorized"
                    : ""}
                  {m.status === "active" ? " · ~€30/mo running" : ""}
                  {m.status === "paused" || m.status === "suspended" ? " · ~€0.50/mo paused" : ""}
                </span>
                <div className="flex items-center gap-1.5">
                  {m.status === "active" ? (
                    <button
                      disabled={busy !== null}
                      onClick={() => {
                        if (
                          !window.confirm(
                            "Pause this box? It snapshots the disk, then deletes the cloud " +
                              "server so it stops billing — ~€0.50/mo while paused vs ~€30/mo " +
                              "running. Resume recreates it from the snapshot in ~2-3 min (new IP).",
                          )
                        )
                          return;
                        void act("pause", "/billing/yaver-cloud/stop", { machineId: m.id });
                      }}
                      className="rounded border border-amber-400/50 px-2 py-0.5 font-semibold text-amber-600 disabled:opacity-50 dark:text-amber-400"
                      title="Snapshot + delete the server to stop billing — resumable"
                    >
                      ⏸ Pause
                    </button>
                  ) : null}
                  {m.status === "paused" || m.status === "suspended" ? (
                    <button
                      disabled={busy !== null}
                      onClick={() => void act("resume", "/billing/yaver-cloud/start", { machineId: m.id })}
                      className="rounded border border-emerald-500/50 px-2 py-0.5 font-semibold text-emerald-700 disabled:opacity-50 dark:text-emerald-300"
                    >
                      ▶ Resume
                    </button>
                  ) : null}
                  {m.status !== "stopped" && m.status !== "stopping" && m.status !== "resuming" ? (
                    <button
                      disabled={busy !== null}
                      onClick={() => {
                        if (
                          !window.confirm(
                            `Decommission this box (resource ${m.hetznerServerId ?? "—"})? ` +
                              `Decommissions the cloud resource, stops billing, and cancels the subscription. Cannot be undone — use Pause if you only want to save cost temporarily.`,
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
              </div>
            ))}
          </div>
        )}
      </Section>

      {msg ? (
        <p
          className={`text-xs ${
            msg.startsWith("✗")
              ? "text-rose-600 dark:text-rose-400"
              : "text-emerald-600 dark:text-emerald-400"
          }`}
        >
          {msg}
        </p>
      ) : null}
    </div>
  );
}
