"use client";

// Relay Pro is the only paid product in this release. Checkout stays web-only;
// native clients consume entitlement/status without embedding payment flows.

import { useCallback, useEffect, useState } from "react";
import { CONVEX_URL } from "@/lib/constants";
import { ENABLE_RELAY_PRO_CHECKOUT } from "@/lib/launchFlags";

interface RelayResource {
  status?: string;
  domain?: string;
  region?: string;
}

interface SubscriptionResponse {
  subscription?: { status?: string; plan?: string; currentPeriodEnd?: number } | null;
  relay?: RelayResource | null;
  machines?: Array<{ id: string; status?: string }>;
}

function isRelayPlan(plan: string | null | undefined): boolean {
  const value = String(plan || "");
  return value === "relay-pro" || value === "relay-monthly" || value === "relay-yearly" || value === "managed-relay";
}

function Section({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <div className="rounded-xl border border-slate-300 bg-white/60 p-4 dark:border-surface-700 dark:bg-[rgba(20,21,27,0.6)]">
      <div className="mb-3 text-xs font-semibold uppercase tracking-wider text-slate-500 dark:text-surface-400">
        {title}
      </div>
      {children}
    </div>
  );
}

export default function BillingView({ token }: { token: string | null | undefined }) {
  const [data, setData] = useState<SubscriptionResponse>({});
  const [busy, setBusy] = useState<string | null>(null);
  const [message, setMessage] = useState<string | null>(null);

  const load = useCallback(async () => {
    if (!token) return;
    try {
      const response = await fetch(`${CONVEX_URL}/subscription`, {
        headers: { Authorization: `Bearer ${token}` },
      });
      setData(await response.json().catch(() => ({})));
    } catch (error) {
      setMessage(error instanceof Error ? error.message : String(error));
    }
  }, [token]);

  useEffect(() => {
    void load();
    const interval = setInterval(() => void load(), 12_000);
    return () => clearInterval(interval);
  }, [load]);

  const request = async (label: string, path: string, body?: Record<string, unknown>) => {
    setBusy(label);
    setMessage(null);
    try {
      const response = await fetch(`${CONVEX_URL}${path}`, {
        method: body ? "POST" : "GET",
        headers: {
          Authorization: `Bearer ${token}`,
          ...(body ? { "Content-Type": "application/json" } : {}),
        },
        ...(body ? { body: JSON.stringify(body) } : {}),
      });
      const result = await response.json().catch(() => ({}));
      if (!response.ok || result?.ok === false) {
        setMessage(`✗ ${result?.reason || result?.error || "Request failed"}`);
        return;
      }
      if (result?.url || result?.portalUrl || result?.updatePaymentUrl) {
        window.location.href = result.url || result.portalUrl || result.updatePaymentUrl;
        return;
      }
      setMessage(`✓ ${label}`);
      await load();
    } catch (error) {
      setMessage(`✗ ${error instanceof Error ? error.message : String(error)}`);
    } finally {
      setBusy(null);
    }
  };

  const subscription = data.subscription;
  const relay = data.relay;
  const relaySubscription = isRelayPlan(subscription?.plan);

  return (
    <div className="space-y-4">
      <div>
        <h2 className="text-lg font-semibold text-slate-800 dark:text-surface-100">Billing</h2>
        <p className="text-xs text-slate-500 dark:text-surface-400">
          Relay Pro is $9/month. Lemon Squeezy handles checkout, invoices, taxes, payment methods, and cancellation.
        </p>
      </div>

      <Section title="Relay Pro">
        {subscription && relaySubscription ? (
          <div className="space-y-3">
            <div className="flex flex-wrap items-center justify-between gap-3">
              <div className="text-sm text-slate-700 dark:text-surface-200">
                <span className="font-semibold">$9/month</span>
                <span className="ml-3">Status: {subscription.status || "unknown"}</span>
                {subscription.currentPeriodEnd ? (
                  <span className="ml-3 text-slate-500 dark:text-surface-400">
                    {subscription.status === "cancelled" ? "Ends" : "Renews"} {new Date(subscription.currentPeriodEnd).toLocaleDateString()}
                  </span>
                ) : null}
              </div>
              <div className="flex gap-2">
                <button disabled={busy !== null} onClick={() => void request("billing portal opened", "/billing/portal")} className="rounded border border-slate-300 px-2.5 py-1 text-xs font-semibold disabled:opacity-50 dark:border-surface-700">
                  Manage billing
                </button>
                <button
                  disabled={busy !== null || !["active", "past_due"].includes(subscription.status || "")}
                  onClick={() => {
                    if (window.confirm("Cancel Relay Pro at the end of the paid period?")) {
                      void request("Relay Pro will cancel at period end", "/billing/cancel", { confirm: true });
                    }
                  }}
                  className="rounded border border-rose-400/50 px-2.5 py-1 text-xs font-semibold text-rose-600 disabled:opacity-50 dark:text-rose-400"
                >
                  Cancel plan
                </button>
              </div>
            </div>

            {relay ? (
              <div className="rounded-md border border-slate-200 px-3 py-2 text-xs dark:border-surface-800">
                <span className="font-mono text-slate-600 dark:text-surface-300">
                  relay · {relay.region || "eu"} · {relay.domain || "provisioning"} · {relay.status || "unknown"}
                </span>
              </div>
            ) : subscription.status === "active" ? (
              <div className="flex flex-wrap items-center justify-between gap-2 rounded-md border border-amber-500/30 bg-amber-500/5 px-3 py-2 text-xs text-amber-700 dark:text-amber-300">
                <span>Your payment is active, but no relay is attached yet.</span>
                <button disabled={busy !== null} onClick={() => void request("Relay repair started", "/billing/relay-pro/reconcile", {})} className="rounded border border-amber-500/40 px-2 py-1 font-semibold disabled:opacity-50">
                  Repair relay
                </button>
              </div>
            ) : null}
          </div>
        ) : subscription ? (
          <div className="space-y-3">
            <p className="text-sm text-slate-600 dark:text-surface-300">
              This account has a legacy managed-resource subscription. New hosted-compute purchases are closed, but billing and teardown controls remain available so the subscription can be ended safely.
            </p>
            <p className="text-xs text-slate-500 dark:text-surface-400">
              Status: {subscription.status || "unknown"} · resources: {data.machines?.filter((machine) => machine.status !== "stopped").length || 0}
              {subscription.currentPeriodEnd ? ` · paid through ${new Date(subscription.currentPeriodEnd).toLocaleDateString()}` : ""}
            </p>
            <div className="flex gap-2">
              <button disabled={busy !== null} onClick={() => void request("billing portal opened", "/billing/portal")} className="rounded border border-slate-300 px-2.5 py-1 text-xs font-semibold disabled:opacity-50 dark:border-surface-700">
                Manage legacy billing
              </button>
              <button
                disabled={busy !== null || !["active", "past_due"].includes(subscription.status || "")}
                onClick={() => {
                  if (window.confirm("Cancel this legacy subscription at the end of the paid period? Linked resources will then be decommissioned.")) {
                    void request("Legacy subscription will cancel at period end", "/billing/cancel", { confirm: true });
                  }
                }}
                className="rounded border border-rose-400/50 px-2.5 py-1 text-xs font-semibold text-rose-600 disabled:opacity-50 dark:text-rose-400"
              >
                Cancel legacy plan
              </button>
            </div>
          </div>
        ) : (
          <div className="space-y-4">
            <div>
              <div className="flex items-baseline gap-2">
                <span className="text-3xl font-bold text-slate-900 dark:text-surface-50">$9</span>
                <span className="text-sm text-slate-500">/month</span>
              </div>
              <p className="mt-2 text-sm text-slate-600 dark:text-surface-300">
                Managed private connectivity to your own Mac, PC, Linux box, VPS, or Pi—across Yaver apps, browser lane, Hermes previews, and the Feedback SDK.
              </p>
              <p className="mt-2 text-xs text-slate-500 dark:text-surface-400">
                Uses shared pass-through infrastructure with account- and device-scoped authentication. It is not a dedicated server and does not include hosted compute or AI model usage.
              </p>
            </div>
            {ENABLE_RELAY_PRO_CHECKOUT ? (
              <button disabled={busy !== null} onClick={() => void request("checkout opened", "/billing/checkout", { productId: "relay-pro", region: "eu" })} className="rounded-md border border-sky-500/50 bg-sky-500/10 px-3 py-1.5 text-sm font-semibold text-sky-700 disabled:opacity-50 dark:text-sky-300">
                {busy ? "Opening…" : "Subscribe to Relay Pro"}
              </button>
            ) : (
              <div className="rounded-md border border-slate-300 bg-slate-100/70 px-3 py-2 text-sm text-slate-600 dark:border-surface-700 dark:bg-surface-900/60 dark:text-surface-300">
                Relay Pro payments are not open yet. The free and self-hosted relay paths remain available.
              </div>
            )}
          </div>
        )}
      </Section>

      {message ? <p className={`text-xs ${message.startsWith("✗") ? "text-rose-600 dark:text-rose-400" : "text-emerald-600 dark:text-emerald-400"}`}>{message}</p> : null}
    </div>
  );
}
