// Org admin — Policy. Loads + persists the orgPolicy singleton.
// Solo-dev safety: no policy row means no enforcement; Save is the
// only way to opt in. Idle timeout and runner/provider allowlists are
// stored but not yet enforced agent-side — surfaced in copy.
"use client";

import { useEffect, useMemo, useState } from "react";
import { AdminShell } from "@/components/admin/AdminShell";
import { ConfirmDestructive } from "@/components/admin/ConfirmDestructive";
import { adminPost, useAdminFetch } from "@/components/admin/useAdminFetch";
import { useToast } from "@/components/admin/Toaster";
import { CONVEX_URL } from "@/lib/constants";
import { Save } from "@/components/admin/icons";

const RUNNERS = ["claude-code", "codex", "opencode"] as const;
const PROVIDERS = ["google", "microsoft", "apple", "github", "gitlab", "email", "passkey"] as const;

type Policy = {
  _id?: string;
  enforceRelay?: boolean;
  allowedRunners?: string[];
  allowedProviders?: string[];
  idleTimeoutMin?: number;
  auditRetentionDays?: number;
  requireMfaForAdmins?: boolean;
};

function getStoredToken(): string | null {
  if (typeof window === "undefined") return null;
  const ls = localStorage.getItem("yaver_auth_token");
  if (ls) return ls;
  for (const cookie of document.cookie.split(";")) {
    const [name, value] = cookie.trim().split("=");
    if (name === "yaver_session" || name === "yaver_auth_token") return value || null;
  }
  return null;
}

export default function AdminPolicy() {
  const { data, error, loading, refresh } = useAdminFetch<{ policy: Policy | null }>(
    "/admin/policy",
  );
  const toast = useToast();

  // Local form state — initialized from server on first load, then
  // edited freely until Save.
  const [enforceRelay, setEnforceRelay] = useState(false);
  const [allowedRunners, setAllowedRunners] = useState<string[]>([...RUNNERS]);
  const [allowedProviders, setAllowedProviders] = useState<string[]>([...PROVIDERS]);
  const [idleTimeoutMin, setIdleTimeoutMin] = useState(0);
  const [auditRetentionDays, setAuditRetentionDays] = useState(7);
  const [mfaForAdmins, setMfaForAdmins] = useState(false);
  const [hydrated, setHydrated] = useState(false);
  const [saving, setSaving] = useState(false);
  const [confirmOpen, setConfirmOpen] = useState(false);

  useEffect(() => {
    if (!data || hydrated) return;
    const p = data.policy;
    if (p) {
      setEnforceRelay(!!p.enforceRelay);
      setAllowedRunners(p.allowedRunners ?? [...RUNNERS]);
      setAllowedProviders(p.allowedProviders ?? [...PROVIDERS]);
      setIdleTimeoutMin(p.idleTimeoutMin ?? 0);
      setAuditRetentionDays(p.auditRetentionDays ?? 7);
      setMfaForAdmins(!!p.requireMfaForAdmins);
    }
    setHydrated(true);
  }, [data, hydrated]);

  function toggle(value: string, set: string[], setter: (s: string[]) => void) {
    setter(set.includes(value) ? set.filter((s) => s !== value) : [...set, value]);
  }

  const dirty = useMemo(() => {
    if (!hydrated) return false;
    const p = data?.policy;
    const cur = {
      enforceRelay,
      allowedRunners,
      allowedProviders,
      idleTimeoutMin,
      auditRetentionDays,
      requireMfaForAdmins: mfaForAdmins,
    };
    const srv = {
      enforceRelay: !!p?.enforceRelay,
      allowedRunners: p?.allowedRunners ?? [...RUNNERS],
      allowedProviders: p?.allowedProviders ?? [...PROVIDERS],
      idleTimeoutMin: p?.idleTimeoutMin ?? 0,
      auditRetentionDays: p?.auditRetentionDays ?? 7,
      requireMfaForAdmins: !!p?.requireMfaForAdmins,
    };
    return JSON.stringify(cur) !== JSON.stringify(srv);
  }, [
    hydrated,
    data,
    enforceRelay,
    allowedRunners,
    allowedProviders,
    idleTimeoutMin,
    auditRetentionDays,
    mfaForAdmins,
  ]);

  async function save() {
    const token = getStoredToken();
    if (!token) return;
    setSaving(true);
    try {
      const res = await fetch(`${CONVEX_URL}/admin/policy`, {
        method: "PUT",
        headers: {
          Authorization: `Bearer ${token}`,
          "Content-Type": "application/json",
        },
        body: JSON.stringify({
          enforceRelay,
          allowedRunners,
          allowedProviders,
          idleTimeoutMin,
          auditRetentionDays,
          requireMfaForAdmins: mfaForAdmins,
        }),
      });
      if (!res.ok) {
        const t = await res.text().catch(() => "");
        throw new Error(`${res.status} ${t || res.statusText}`);
      }
      toast.push({ tone: "success", title: "Policy saved", body: "Effective immediately." });
      refresh();
    } catch (err: any) {
      toast.push({ tone: "danger", title: "Save failed", body: String(err?.message || err) });
    } finally {
      setSaving(false);
    }
  }

  return (
    <AdminShell
      pageTitle="Policy"
      pageSubtitle="Org-wide defaults. No policy row = solo-dev defaults; opt in only by clicking Save."
      actions={
        <button
          onClick={() => setConfirmOpen(true)}
          disabled={!dirty || saving}
          className="inline-flex items-center gap-1.5 rounded bg-danger px-2.5 py-1.5 text-[12px] font-medium text-danger-fg disabled:opacity-40"
        >
          <Save className="h-3.5 w-3.5" />
          {saving ? "Saving…" : "Save policy"}
        </button>
      }
    >
      {error && (
        <div className="mb-4 rounded border border-danger/40 bg-danger-soft p-3 text-[12px] text-danger-softFg">
          Failed to load: {error}
        </div>
      )}
      {loading && !data && (
        <div className="mb-4 text-[12px] text-surface-400">Loading current policy…</div>
      )}

      <div className="grid grid-cols-1 gap-4 lg:grid-cols-2">
        <section className="rounded-md border border-surface-800 bg-surface-900 p-4">
          <h2 className="text-[13px] font-semibold text-surface-100">Connectivity</h2>
          <p className="mt-1 text-[12px] text-surface-400">
            Persisted; full agent-side enforcement lands when the agent reads policy on
            heartbeat. Until then this row is informational — surfaces in audit + status.
          </p>
          <label className="mt-3 flex cursor-pointer items-center gap-2 text-[13px] text-surface-200">
            <input
              type="checkbox"
              checked={enforceRelay}
              onChange={(e) => setEnforceRelay(e.target.checked)}
              className="h-4 w-4 accent-warning"
            />
            Require platform relay
          </label>
        </section>

        <section className="rounded-md border border-surface-800 bg-surface-900 p-4">
          <h2 className="text-[13px] font-semibold text-surface-100">Sessions</h2>
          <p className="mt-1 text-[12px] text-surface-400">
            Stored; enforcement at <span className="font-mono">authenticateRequest</span>{" "}
            is intentionally deferred so solo-dev tmux sessions never get killed mid-flow.
            Value flows into a future server-side pruner.
          </p>
          <label className="mt-3 block text-[11px] uppercase tracking-wider text-surface-400">
            Idle timeout (minutes)
          </label>
          <input
            type="number"
            min={0}
            value={idleTimeoutMin}
            onChange={(e) => setIdleTimeoutMin(Math.max(0, Number(e.target.value || 0)))}
            className="mt-1 w-32 rounded border border-surface-700 bg-surface-950 px-2 py-1 font-mono text-[13px] text-surface-100 outline-none focus:border-warning"
          />
        </section>

        <section className="rounded-md border border-surface-800 bg-surface-900 p-4">
          <h2 className="text-[13px] font-semibold text-surface-100">Runners</h2>
          <p className="mt-1 text-[12px] text-surface-400">
            Allowed coding-agent runners. Persisted today; full enforcement happens at
            the spawn point in the agent on the next heartbeat pass.
          </p>
          <div className="mt-3 flex flex-wrap gap-2">
            {RUNNERS.map((r) => (
              <label
                key={r}
                className={`cursor-pointer rounded border px-2 py-1 text-[12px] ${
                  allowedRunners.includes(r)
                    ? "border-warning bg-warning-soft text-warning-softFg"
                    : "border-surface-700 text-surface-400"
                }`}
              >
                <input
                  type="checkbox"
                  className="hidden"
                  checked={allowedRunners.includes(r)}
                  onChange={() => toggle(r, allowedRunners, setAllowedRunners)}
                />
                {r}
              </label>
            ))}
          </div>
        </section>

        <section className="rounded-md border border-surface-800 bg-surface-900 p-4">
          <h2 className="text-[13px] font-semibold text-surface-100">Identity providers</h2>
          <p className="mt-1 text-[12px] text-surface-400">
            OAuth + native providers that can authenticate on this deployment. Persisted
            today; sign-in page reads this list in the next pass.
          </p>
          <div className="mt-3 flex flex-wrap gap-2">
            {PROVIDERS.map((p) => (
              <label
                key={p}
                className={`cursor-pointer rounded border px-2 py-1 text-[12px] ${
                  allowedProviders.includes(p)
                    ? "border-warning bg-warning-soft text-warning-softFg"
                    : "border-surface-700 text-surface-400"
                }`}
              >
                <input
                  type="checkbox"
                  className="hidden"
                  checked={allowedProviders.includes(p)}
                  onChange={() => toggle(p, allowedProviders, setAllowedProviders)}
                />
                {p}
              </label>
            ))}
          </div>
        </section>

        <section className="rounded-md border border-surface-800 bg-surface-900 p-4">
          <h2 className="text-[13px] font-semibold text-surface-100">Audit retention</h2>
          <p className="mt-1 text-[12px] text-surface-400">
            Replaces the hard-coded 7-day default in{" "}
            <span className="font-mono">cleanup.ts</span>. Wired through immediately — the
            next cron tick uses this value. Floors at 1 day. Empty policy ⇒ 7 days.
          </p>
          <label className="mt-3 block text-[11px] uppercase tracking-wider text-surface-400">
            Retention (days)
          </label>
          <input
            type="number"
            min={1}
            value={auditRetentionDays}
            onChange={(e) => setAuditRetentionDays(Math.max(1, Number(e.target.value || 7)))}
            className="mt-1 w-32 rounded border border-surface-700 bg-surface-950 px-2 py-1 font-mono text-[13px] text-surface-100 outline-none focus:border-warning"
          />
        </section>

        <section className="rounded-md border border-surface-800 bg-surface-900 p-4">
          <h2 className="text-[13px] font-semibold text-surface-100">MFA</h2>
          <p className="mt-1 text-[12px] text-surface-400">
            Refuses admin-route access (<span className="font-mono">/admin/*</span>) when
            a schema-promoted admin has no TOTP. Solo-dev bootstrap admins (env-var
            allowlist) are <strong>permanently exempt</strong> so you cannot lock yourself
            out by toggling this on.
          </p>
          <label className="mt-3 flex cursor-pointer items-center gap-2 text-[13px] text-surface-200">
            <input
              type="checkbox"
              checked={mfaForAdmins}
              onChange={(e) => setMfaForAdmins(e.target.checked)}
              className="h-4 w-4 accent-warning"
            />
            Require MFA for platform admins
          </label>
        </section>
      </div>

      {confirmOpen && (
        <ConfirmDestructive
          open
          title="Apply org policy"
          body={
            <span>
              These settings affect every user in the deployment. Retention change takes
              effect on the next cron tick (within ~15 min); MFA gate takes effect
              immediately on the next admin-route request.
            </span>
          }
          confirmLabel="Apply policy"
          confirmPhrase="apply"
          destructive={false}
          onClose={() => setConfirmOpen(false)}
          onConfirm={async () => {
            await save();
          }}
        />
      )}
    </AdminShell>
  );
}
