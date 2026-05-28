// Org admin — Overview. Aggregated counts, 30-day activity sparkline,
// last 5 audit events, and fleet-health alert banners.
"use client";

import { AdminShell } from "@/components/admin/AdminShell";
import { MetricCard } from "@/components/admin/MetricCard";
import { Sparkline } from "@/components/admin/Sparkline";
import { AdminBadge } from "@/components/admin/AdminBadge";
import { EmptyState } from "@/components/admin/EmptyState";
import { useAdminFetch } from "@/components/admin/useAdminFetch";
import { AlertTriangle, RefreshCcw } from "@/components/admin/icons";

type OverviewResponse = {
  counts: { users: number; devices: number; activeSessions: number; teams: number };
  alerts: { staleDevices: number; usersWithoutMfa: number };
  sparkline: number[];
  recent: Array<{
    timestamp: number;
    kind: "activity" | "security";
    actor: string;
    action: string;
    target: string;
    outcome: string;
  }>;
};

function relativeTime(ms: number): string {
  const diff = Math.max(0, Date.now() - ms);
  const s = Math.floor(diff / 1000);
  if (s < 60) return `${s}s ago`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}m ago`;
  const h = Math.floor(m / 60);
  if (h < 24) return `${h}h ago`;
  const d = Math.floor(h / 24);
  return `${d}d ago`;
}

export default function AdminOverview() {
  const { data, error, loading, refresh } = useAdminFetch<OverviewResponse>("/admin/overview");

  return (
    <AdminShell
      pageTitle="Overview"
      pageSubtitle="Fleet-wide health, activity, and recent audit events."
      actions={
        <button
          onClick={refresh}
          className="inline-flex items-center gap-1.5 rounded border border-surface-700 px-2.5 py-1.5 text-[12px] text-surface-200 hover:border-surface-500 hover:text-surface-100"
        >
          <RefreshCcw className="h-3.5 w-3.5" />
          Refresh
        </button>
      }
    >
      {error && (
        <div className="mb-4 rounded border border-danger/40 bg-danger-soft p-3 text-[12px] text-danger-softFg">
          Failed to load: {error}
        </div>
      )}

      {/* Metric grid */}
      <div className="grid grid-cols-2 gap-3 lg:grid-cols-4">
        <MetricCard
          label="Users"
          value={loading ? "—" : data?.counts.users ?? 0}
          sub={loading ? "loading…" : "registered on this deployment"}
        />
        <MetricCard
          label="Devices"
          value={loading ? "—" : data?.counts.devices ?? 0}
          sub={loading ? "loading…" : `${data?.counts.activeSessions ?? 0} active sessions`}
        />
        <MetricCard
          label="Teams"
          value={loading ? "—" : data?.counts.teams ?? 0}
          sub={loading ? "loading…" : "owner + members"}
        />
        <MetricCard
          label="Stale devices"
          tone={(data?.alerts.staleDevices ?? 0) > 0 ? "warning" : "neutral"}
          value={loading ? "—" : data?.alerts.staleDevices ?? 0}
          sub="no heartbeat in 7 days"
        />
      </div>

      {/* Sparkline + alerts row */}
      <div className="mt-4 grid grid-cols-1 gap-3 lg:grid-cols-3">
        <div className="rounded-md border border-surface-800 bg-surface-900 p-4 lg:col-span-2">
          <div className="flex items-baseline justify-between">
            <div>
              <div className="text-[11px] font-medium uppercase tracking-wider text-surface-400">
                Activity, last 30 days
              </div>
              <div className="mt-1 font-mono text-[20px] tabular-nums text-surface-100">
                {data?.sparkline ? data.sparkline.reduce((a, b) => a + b, 0) : "—"}
              </div>
              <div className="mt-0.5 text-[12px] text-surface-400">
                userActivity rows by day
              </div>
            </div>
            <Sparkline values={data?.sparkline ?? []} width={300} height={56} />
          </div>
        </div>

        <div className="rounded-md border border-surface-800 bg-surface-900 p-4">
          <div className="text-[11px] font-medium uppercase tracking-wider text-surface-400">
            Fleet alerts
          </div>
          <ul className="mt-3 space-y-2 text-[13px]">
            <li className="flex items-start justify-between gap-3">
              <span className="text-surface-300">Devices stale &gt; 7 days</span>
              <AdminBadge tone={(data?.alerts.staleDevices ?? 0) > 0 ? "warning" : "muted"}>
                {data?.alerts.staleDevices ?? 0}
              </AdminBadge>
            </li>
            <li className="flex items-start justify-between gap-3">
              <span className="text-surface-300">Users without MFA (TOTP)</span>
              <AdminBadge tone={(data?.alerts.usersWithoutMfa ?? 0) > 0 ? "warning" : "muted"}>
                {data?.alerts.usersWithoutMfa ?? 0}
              </AdminBadge>
            </li>
            <li className="flex items-start justify-between gap-3">
              <span className="text-surface-300">SSO providers configured</span>
              <AdminBadge tone="muted">0</AdminBadge>
            </li>
          </ul>
          {(data?.alerts.staleDevices ?? 0) + (data?.alerts.usersWithoutMfa ?? 0) > 0 && (
            <div className="mt-3 flex items-start gap-2 rounded border border-warning/40 bg-warning-soft p-2 text-[11px] text-warning-softFg">
              <AlertTriangle className="mt-0.5 h-3.5 w-3.5 shrink-0" />
              Review on the relevant tab to act.
            </div>
          )}
        </div>
      </div>

      {/* Recent audit */}
      <div className="mt-4 rounded-md border border-surface-800 bg-surface-900">
        <div className="flex items-center justify-between border-b border-surface-800 px-4 py-2.5">
          <div className="text-[11px] font-medium uppercase tracking-wider text-surface-400">
            Last 5 audit events
          </div>
          <a
            href="/admin/audit"
            className="text-[12px] text-surface-300 hover:text-surface-100"
          >
            View all →
          </a>
        </div>
        {data && data.recent.length === 0 ? (
          <div className="p-4">
            <EmptyState
              title="No events yet"
              body="Audit events appear here as users authenticate, devices act, and admins make changes. Check back after the deployment sees real usage."
            />
          </div>
        ) : (
          <ul className="divide-y divide-surface-850">
            {(data?.recent ?? []).map((ev, i) => (
              <li
                key={`${ev.timestamp}-${i}`}
                className="flex items-center gap-3 px-4 py-2.5 text-[13px]"
              >
                <span className="font-mono text-[11px] text-surface-400 w-[72px] shrink-0 tabular-nums">
                  {relativeTime(ev.timestamp)}
                </span>
                <AdminBadge tone={ev.kind === "security" ? "warning" : "muted"} uppercase>
                  {ev.kind}
                </AdminBadge>
                <span className="truncate font-mono text-[12px] text-surface-100">
                  {ev.action}
                </span>
                <span className="ml-auto truncate text-[12px] text-surface-400">
                  {ev.actor}
                </span>
              </li>
            ))}
            {loading && data == null && (
              <li className="px-4 py-3 text-[12px] text-surface-400">Loading…</li>
            )}
          </ul>
        )}
      </div>
    </AdminShell>
  );
}
