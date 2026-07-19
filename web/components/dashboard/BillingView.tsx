"use client";

// BillingView — flat-plan billing for Yaver Relay Pro and Cloud Workspace.
//
// Sections, top to bottom:
//   1. Plan cards — Free, Relay Pro, Cloud Workspace.
//   2. Included allowance — Cloud Workspace monthly standard credits.
//   3. Subscription + managed cloud resources.
//
// Purchases are web-only. Mobile may show status/control, but must not start
// checkout or cancellation flows because of App Store policy.

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
interface SubRelay {
  status?: string;
  domain?: string;
  region?: string;
  quicPort?: number;
  httpPort?: number;
}
interface SubResp {
  subscription?: { status?: string; plan?: string; currentPeriodEnd?: number } | null;
  relay?: SubRelay | null;
  machines?: SubMachine[];
}

interface Wallet {
  balanceCents: number;
  currency?: string;
  allowance?: {
    plan?: string | null;
    unit?: "standard_credits" | string;
    includedStandardCredits?: number;
    usedStandardCredits?: number;
    remainingStandardCredits?: number;
    includedSeconds: number;
    usedSeconds: number;
    remainingSeconds: number;
  };
  inference?: {
    enabled: boolean;
    dailyCapCents: number;
    spentTodayCents: number;
  };
}

type BillingProductId = "relay-pro" | "cloud-workspace";

const BILLING_PRODUCTS: Array<{
  id: BillingProductId;
  name: string;
  price: string;
  detail: string;
  included: string[];
}> = [
  {
    id: "relay-pro",
    name: "Relay Pro",
    price: "$9/mo",
    detail: "Private managed relay for users who keep coding on their own machine.",
    included: ["Private relay", "Higher limits", "Web checkout only"],
  },
  {
    id: "cloud-workspace",
    name: "Cloud Workspace",
    price: "$29/mo",
    detail: "Saved cloud workspace for full-stack projects. Relay Pro is included.",
    included: ["Saved workspace", "Relay Pro included", "Auto-sleep"],
  },
];

const money = (cents: number | null | undefined) =>
  typeof cents === "number" ? `$${(cents / 100).toFixed(2)}` : "—";

function productLabel(plan: string | null | undefined): string {
  const value = String(plan || "");
  if (value === "cloud-workspace" || value === "cloud-agent" || value.startsWith("yaver-cloud")) {
    return "Cloud Workspace";
  }
  if (value === "relay-pro" || value === "relay-monthly" || value === "relay-yearly" || value === "managed-relay") {
    return "Relay Pro";
  }
  return "Free";
}

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
  const [hasAllowance, setHasAllowance] = useState<boolean | null>(null);
  const [selectedProduct, setSelectedProduct] = useState<BillingProductId>("cloud-workspace");

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
      const b = await authedGet(`/billing/yaver-cloud/balance`);
      // 403 on balance = not in the cloud preview → no allowance to show.
      if (b?.__status === 403) {
        setHasAllowance(false);
        return;
      }
      setHasAllowance(true);
      if (!b?.__status) setWallet(b as Wallet);
    } catch {
      /* non-fatal — sections just stay hidden */
    }
  }, [token, authedGet]);

  useEffect(() => {
    void loadSub();
    const iv = setInterval(() => void loadSub(), 12000);
    return () => clearInterval(iv);
  }, [loadSub]);

  useEffect(() => {
    void loadWallet();
  }, [loadWallet]);

  const startCheckout = async (productId: BillingProductId) => {
    setBusy(`checkout:${productId}`);
    setMsg(null);
    try {
      const res = await fetch(`${CONVEX_URL}/billing/checkout`, {
        method: "POST",
        headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/json" },
        body: JSON.stringify({ productId, region: "eu" }),
      });
      const j = await res.json().catch(() => ({}));
      if (!res.ok || !j?.url) {
        setMsg(
          res.status === 503
            ? `✗ ${productLabel(productId)} checkout is not configured yet.`
            : `✗ ${j?.error || "Couldn't start checkout. Please try again."}`,
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

  const openBillingPortal = async () => {
    setBusy("portal");
    setMsg(null);
    try {
      const res = await fetch(`${CONVEX_URL}/billing/portal`, {
        headers: { Authorization: `Bearer ${token}` },
      });
      const j = await res.json().catch(() => ({}));
      const url = j?.portalUrl || j?.updatePaymentUrl;
      if (!res.ok || !url) {
        setMsg(`✗ ${j?.reason || j?.error || "Billing portal is not available yet."}`);
        return;
      }
      window.location.href = url;
    } catch (e: any) {
      setMsg(`✗ ${e?.message || String(e)}`);
    } finally {
      setBusy(null);
    }
  };

  const cancelSubscription = async () => {
    const label = productLabel(sub?.plan);
    if (
      !window.confirm(
        `Cancel ${label}? This stops future renewal. Linked Yaver-managed relay/workspace resources will be scheduled for teardown.`
      )
    ) {
      return;
    }
    setBusy("cancel-subscription");
    setMsg(null);
    try {
      const res = await fetch(`${CONVEX_URL}/billing/cancel`, {
        method: "POST",
        headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/json" },
        body: JSON.stringify({ confirm: true }),
      });
      const j = await res.json().catch(() => ({}));
      if (!res.ok || j?.ok === false) {
        setMsg(`✗ ${j?.error || "Couldn't cancel subscription."}`);
        return;
      }
      setMsg(`✓ ${label} cancelled`);
      await Promise.all([loadSub(), loadWallet()]);
    } catch (e: any) {
      setMsg(`✗ ${e?.message || String(e)}`);
    } finally {
      setBusy(null);
    }
  };

  const upgradeToCloudWorkspace = async () => {
    setBusy("upgrade-cloud-workspace");
    setMsg(null);
    try {
      const res = await fetch(`${CONVEX_URL}/billing/yaver-cloud/change-plan`, {
        method: "POST",
        headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/json" },
        body: JSON.stringify({ plan: "cloud-workspace", region: "eu" }),
      });
      const j = await res.json().catch(() => ({}));
      if (!res.ok || j?.ok === false) {
        setMsg(`✗ ${j?.reason || j?.error || "Couldn't upgrade to Cloud Workspace."}`);
        return;
      }
      setMsg("✓ Upgraded to Cloud Workspace");
      await Promise.all([loadSub(), loadWallet()]);
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
  const activeProductLabel = productLabel(sub?.plan);
  const isCloudWorkspaceSub = activeProductLabel === "Cloud Workspace";
  const isRelayProSub = activeProductLabel === "Relay Pro";
  const relay = data.relay ?? null;
  const allMachines = data.machines ?? [];
  const machines = allMachines.filter((m) => m.status !== "stopped");
  const active = machines.filter((m) => m.status === "active").length;
  const noBox = isCloudWorkspaceSub && sub?.status === "active" && machines.length === 0;

  return (
    <div className="space-y-4">
      <div>
        <h2 className="text-lg font-semibold text-slate-800 dark:text-surface-100">Billing</h2>
        <p className="text-xs text-slate-500 dark:text-surface-400">
          Web-only subscriptions for Relay Pro and Cloud Workspace. Lemon
          Squeezy handles checkout and billing; Cloud Workspace auto-sleeps to
          protect spend.
        </p>
      </div>

      {/* ── Included this month ────────────────────────────────────── */}
      {hasAllowance !== false &&
      ((wallet?.allowance?.includedSeconds ?? 0) > 0 || wallet?.inference) ? (
        <Section title="Included this month">
          {/* Active-hours fuel gauge: covered by the flat plan before any
              internal overage guardrail matters. */}
          {(wallet?.allowance?.includedSeconds ?? 0) > 0
            ? (() => {
                const inc = wallet!.allowance!.includedSeconds;
                const used = Math.min(wallet!.allowance!.usedSeconds, inc);
                const includedCredits = wallet!.allowance!.includedStandardCredits ?? Math.round(inc / 3600);
                const usedCredits = wallet!.allowance!.usedStandardCredits ?? Math.round((used / 3600) * 10) / 10;
                const remainingCredits = wallet!.allowance!.remainingStandardCredits ?? Math.max(0, Math.round(((inc - used) / 3600) * 10) / 10);
                const pct = includedCredits > 0 ? Math.min(100, Math.round((usedCredits / includedCredits) * 100)) : 0;
                const remCredits = remainingCredits.toFixed(1);
                return (
                  <div className="mb-3">
                    <div className="mb-1 flex items-center justify-between text-xs">
                      <span className="font-medium text-slate-700 dark:text-surface-200">
                        Workspace allowance
                      </span>
                      <span className="tabular-nums text-slate-500 dark:text-surface-400">
                        {remCredits} standard credits left of {includedCredits}
                      </span>
                    </div>
                    <div className="h-1.5 w-full overflow-hidden rounded-full bg-slate-200 dark:bg-surface-800">
                      <div
                        className={`h-full rounded-full ${pct >= 100 ? "bg-amber-500" : "bg-sky-500"}`}
                        style={{ width: `${Math.max(2, pct)}%` }}
                      />
                    </div>
                    <p className="mt-1 text-[10px] text-slate-400 dark:text-surface-500">
                      Standard work uses 1 credit per hour; heavy/build work
                      uses more. If allowance is exhausted, the workspace
                      pauses until the next period or billing settings are
                      updated.
                    </p>
                  </div>
                );
              })()
            : null}

          {/* Managed-AI status — platform-managed limit or BYOK. */}
          {wallet?.inference ? (
            wallet.inference.enabled ? (
              wallet.inference.dailyCapCents > 0 ? (
                (() => {
                  const cap = wallet.inference.dailyCapCents;
                  const spent = Math.min(wallet.inference.spentTodayCents, cap);
                  const pct = Math.min(100, Math.round((spent / cap) * 100));
                  return (
                    <div>
                      <div className="mb-1 flex items-center justify-between text-xs">
                        <span className="font-medium text-slate-700 dark:text-surface-200">
                          Managed AI · today
                        </span>
                        <span className="tabular-nums text-slate-500 dark:text-surface-400">
                          {money(spent)} of {money(cap)}/day
                        </span>
                      </div>
                      <div className="h-1.5 w-full overflow-hidden rounded-full bg-slate-200 dark:bg-surface-800">
                        <div
                          className={`h-full rounded-full ${pct >= 100 ? "bg-amber-500" : "bg-violet-500"}`}
                          style={{ width: `${Math.max(2, pct)}%` }}
                        />
                      </div>
                    </div>
                  );
                })()
              ) : (
                <p className="text-xs text-slate-500 dark:text-surface-400">
                  Managed AI · controlled by workspace limits.
                </p>
              )
            ) : (
              <p className="text-xs text-slate-500 dark:text-surface-400">
                Managed AI off · bring your own key (BYOK) - set it under
                Tools → runner.
              </p>
            )
          ) : null}
        </Section>
      ) : null}

      {/* ── 2 · Subscription ───────────────────────────────────────── */}
      <Section title="Subscription">
        {sub ? (
          <>
            <div className="flex flex-wrap items-center justify-between gap-3">
              <div className="flex flex-wrap items-center gap-4 text-sm text-slate-700 dark:text-surface-200">
                <span>
                  Plan: <span className="font-semibold">{activeProductLabel}</span>
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
              <div className="flex flex-wrap items-center gap-1.5">
                {isRelayProSub && sub.status === "active" ? (
                  <button
                    disabled={busy !== null}
                    onClick={() => void upgradeToCloudWorkspace()}
                    className="rounded border border-sky-500/50 bg-sky-500/10 px-2.5 py-1 text-xs font-semibold text-sky-700 disabled:opacity-50 dark:text-sky-300"
                  >
                    {busy === "upgrade-cloud-workspace" ? "…" : "Upgrade to Cloud Workspace"}
                  </button>
                ) : null}
                <button
                  disabled={busy !== null}
                  onClick={() => void openBillingPortal()}
                  className="rounded border border-slate-300 px-2.5 py-1 text-xs font-semibold text-slate-700 disabled:opacity-50 dark:border-surface-700 dark:text-surface-200"
                >
                  {busy === "portal" ? "…" : "Manage billing"}
                </button>
                <button
                  disabled={busy !== null || (sub.status !== "active" && sub.status !== "past_due")}
                  onClick={() => void cancelSubscription()}
                  className="rounded border border-rose-400/50 px-2.5 py-1 text-xs font-semibold text-rose-600 disabled:opacity-50 dark:text-rose-400"
                >
                  {busy === "cancel-subscription" ? "…" : "Cancel plan"}
                </button>
              </div>
            </div>
            <p className="mt-2 text-[11px] text-slate-500 dark:text-surface-400">
              Manage billing opens LemonSqueezy for invoices, payment method,
              and self-service cancellation. Cancel plan uses Yaver&apos;s
              backend path and schedules linked Yaver-managed resources for
              teardown.
            </p>
            {noBox ? (
              <p className="mt-2 rounded-md border border-amber-500/30 bg-amber-500/5 px-2.5 py-1.5 text-xs text-amber-700 dark:text-amber-300">
                This Cloud Workspace subscription is active but has no managed
                workspace attached. Use Manage billing or Cancel plan here, or
                re-provision from the workspace controls.
              </p>
            ) : null}
          </>
        ) : (
          <div className="space-y-3">
            <p className="text-sm text-slate-500 dark:text-surface-400">
              Free plan — limited shared public relay. Paid products are web-only:
              Relay Pro for a private relay, or Cloud Workspace for a saved
              workspace with Relay Pro included.
            </p>
            <div className="grid gap-2 md:grid-cols-2">
              {BILLING_PRODUCTS.map((product) => {
                const activeProduct = selectedProduct === product.id;
                return (
                  <button
                    key={product.id}
                    type="button"
                    onClick={() => setSelectedProduct(product.id)}
                    className={`rounded-lg border p-3 text-left transition-colors ${
                      activeProduct
                        ? "border-sky-500/60 bg-sky-500/10"
                        : "border-slate-300 bg-white/40 hover:border-sky-500/50 dark:border-surface-700 dark:bg-[rgba(12,12,16,0.5)]"
                    }`}
                  >
                    <div className="flex items-start justify-between gap-3">
                      <div>
                        <p className="text-sm font-bold text-slate-800 dark:text-surface-100">
                          {product.name}
                        </p>
                        <p className="mt-0.5 text-[11px] leading-5 text-slate-500 dark:text-surface-400">
                          {product.detail}
                        </p>
                      </div>
                      <span className="shrink-0 text-sm font-bold text-slate-900 dark:text-surface-50">
                        {product.price}
                      </span>
                    </div>
                    <div className="mt-2 flex flex-wrap gap-1">
                      {product.included.map((item) => (
                        <span
                          key={item}
                          className="rounded-full bg-slate-500/10 px-2 py-0.5 text-[10px] font-medium text-slate-500 dark:text-surface-300"
                        >
                          {item}
                        </span>
                      ))}
                    </div>
                  </button>
                );
              })}
            </div>
            <button
              disabled={busy !== null}
              onClick={() => void startCheckout(selectedProduct)}
              className="rounded-md border border-sky-500/50 bg-sky-500/10 px-3 py-1.5 text-sm font-semibold text-sky-700 disabled:opacity-50 dark:text-sky-300"
            >
              {busy === `checkout:${selectedProduct}` ? "…" : `Subscribe to ${productLabel(selectedProduct)}`}
            </button>
          </div>
        )}
      </Section>

      {/* ── 3 · Managed resources ──────────────────────────────────── */}
      <Section
        title={`Managed resources (${active} active workspace${active === 1 ? "" : "s"} / ${machines.length})`}
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
        {!relay && machines.length === 0 ? (
          isRelayProSub ? (
            <p className="rounded-md border border-amber-500/30 bg-amber-500/5 px-2.5 py-1.5 text-xs text-amber-700 dark:text-amber-300">
              Relay Pro is active but no managed relay is attached yet. Use
              re-provision missing to create the relay resource.
            </p>
          ) : (
            <p className="text-sm text-slate-500 dark:text-surface-400">No managed Yaver resources.</p>
          )
        ) : (
          <div className="space-y-2">
            {relay ? (
              <div className="flex flex-wrap items-center justify-between gap-2 rounded-md border border-slate-200 px-3 py-2 text-xs dark:border-surface-800">
                <span className="font-mono text-slate-600 dark:text-surface-300">
                  relay · {relay.region ?? "eu"} · {relay.domain ?? "private"} ·{" "}
                  <span
                    className={
                      relay.status === "active"
                        ? "font-semibold text-emerald-600 dark:text-emerald-400"
                        : relay.status === "error"
                          ? "font-semibold text-rose-600 dark:text-rose-400"
                          : ""
                    }
                  >
                    {relay.status ?? "?"}
                  </span>
                </span>
                <span className="text-[10px] uppercase tracking-wider text-slate-400 dark:text-surface-500">
                  Relay Pro
                </span>
              </div>
            ) : null}
            {machines.map((m) => (
              <div
                key={m.id}
                className="flex flex-wrap items-center justify-between gap-2 rounded-md border border-slate-200 px-3 py-2 text-xs dark:border-surface-800"
              >
                <span className="font-mono text-slate-600 dark:text-surface-300">
                  {m.machineType ?? "standard"} · {m.region ?? "eu"} ·{" "}
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
                  {m.status === "active" ? " · running" : ""}
                  {m.status === "paused" || m.status === "suspended" ? " · parked" : ""}
                </span>
                <div className="flex items-center gap-1.5">
                  {m.status === "active" ? (
                    <button
                      disabled={busy !== null}
                      onClick={() => {
                        if (
                          !window.confirm(
                            "Pause this workspace? It preserves state, deletes active compute, " +
                              "and stops compute spend. Resume recreates it when you need it.",
                          )
                        )
                          return;
                        void act("pause", "/billing/yaver-cloud/stop", { machineId: m.id });
                      }}
                      className="rounded border border-amber-400/50 px-2 py-0.5 font-semibold text-amber-600 disabled:opacity-50 dark:text-amber-400"
                      title="Preserve state and stop active compute spend"
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
                            "Decommission this Cloud Workspace? " +
                              "This removes the managed cloud resource, stops its billing, and cancels the subscription. Cannot be undone - use Pause if you only want to save cost temporarily.",
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
