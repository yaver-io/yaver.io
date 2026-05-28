// Org admin — Audit log. Merged userActivity + securityEvents feed
// with cursor-based pagination, date/actor/event-type filters, and
// CSV export of the current view.
"use client";

import { useEffect, useMemo, useState } from "react";
import { AdminShell } from "@/components/admin/AdminShell";
import { AdminTable, type Column } from "@/components/admin/AdminTable";
import { AdminBadge } from "@/components/admin/AdminBadge";
import { EmptyState } from "@/components/admin/EmptyState";
import { CONVEX_URL } from "@/lib/constants";
import { Download, Loader2, RefreshCcw } from "@/components/admin/icons";

type AuditRow = {
  timestamp: number;
  kind: "activity" | "security";
  actor: string;
  actorId: string;
  action: string;
  target: string;
  outcome: string;
  details: string;
};

type Page = { rows: AuditRow[]; nextCursor: number | null };

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

function isoDate(ms: number): string {
  return new Date(ms).toISOString();
}

function toCsv(rows: AuditRow[]): string {
  const header = ["timestamp_iso", "kind", "actor_email", "action", "target", "outcome", "details"];
  const lines = [header.join(",")];
  for (const r of rows) {
    const fields = [
      isoDate(r.timestamp),
      r.kind,
      r.actor,
      r.action,
      r.target,
      r.outcome,
      r.details,
    ].map((field) => {
      const s = String(field ?? "");
      if (/[,"\n]/.test(s)) return `"${s.replace(/"/g, '""')}"`;
      return s;
    });
    lines.push(fields.join(","));
  }
  return lines.join("\n");
}

export default function AdminAudit() {
  const [actor, setActor] = useState("");
  const [eventType, setEventType] = useState("");
  const [sinceLocal, setSinceLocal] = useState<string>("");
  const [untilLocal, setUntilLocal] = useState<string>("");

  const [pages, setPages] = useState<AuditRow[]>([]);
  const [cursor, setCursor] = useState<number | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [exhausted, setExhausted] = useState(false);
  const [filtersTick, setFiltersTick] = useState(0);

  const queryString = useMemo(() => {
    const sp = new URLSearchParams();
    sp.set("limit", "50");
    if (actor.trim()) sp.set("actor", actor.trim());
    if (eventType.trim()) sp.set("event", eventType.trim());
    if (sinceLocal) sp.set("since", String(new Date(sinceLocal).getTime()));
    if (untilLocal) sp.set("until", String(new Date(untilLocal).getTime()));
    return sp.toString();
  }, [actor, eventType, sinceLocal, untilLocal]);

  // Reset on filter change.
  useEffect(() => {
    setPages([]);
    setCursor(null);
    setExhausted(false);
  }, [actor, eventType, sinceLocal, untilLocal, filtersTick]);

  async function loadMore() {
    const token = getStoredToken();
    if (!token) {
      setError("Not signed in");
      return;
    }
    setLoading(true);
    setError(null);
    try {
      const sp = new URLSearchParams(queryString);
      if (cursor != null) sp.set("cursor", String(cursor));
      const res = await fetch(`${CONVEX_URL}/admin/audit?${sp.toString()}`, {
        headers: { Authorization: `Bearer ${token}` },
      });
      if (!res.ok) {
        const t = await res.text().catch(() => "");
        throw new Error(`${res.status} ${t || res.statusText}`);
      }
      const json: Page = await res.json();
      setPages((cur) => [...cur, ...json.rows]);
      setCursor(json.nextCursor);
      setExhausted(json.nextCursor == null || json.rows.length === 0);
    } catch (err: any) {
      setError(String(err?.message || err));
    } finally {
      setLoading(false);
    }
  }

  useEffect(() => {
    if (pages.length === 0 && !loading && !exhausted) loadMore();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [queryString, filtersTick]);

  function exportCsv() {
    const csv = toCsv(pages);
    const blob = new Blob([csv], { type: "text/csv;charset=utf-8" });
    const url = URL.createObjectURL(blob);
    const a = document.createElement("a");
    const stamp = new Date().toISOString().replace(/[:.]/g, "-");
    a.href = url;
    a.download = `yaver-audit-${stamp}.csv`;
    document.body.appendChild(a);
    a.click();
    document.body.removeChild(a);
    URL.revokeObjectURL(url);
  }

  const columns: Column<AuditRow>[] = [
    {
      key: "ts",
      header: "Timestamp",
      cellClass: "w-[200px]",
      sort: (a, b) => a.timestamp - b.timestamp,
      render: (r) => (
        <span className="font-mono text-[12px] tabular-nums text-surface-300">
          {isoDate(r.timestamp)}
        </span>
      ),
    },
    {
      key: "kind",
      header: "Kind",
      cellClass: "w-[100px]",
      sort: (a, b) => a.kind.localeCompare(b.kind),
      render: (r) => (
        <AdminBadge tone={r.kind === "security" ? "warning" : "muted"} uppercase>
          {r.kind}
        </AdminBadge>
      ),
    },
    {
      key: "action",
      header: "Action",
      sort: (a, b) => a.action.localeCompare(b.action),
      render: (r) => (
        <span className="font-mono text-[12px] text-surface-100">{r.action}</span>
      ),
    },
    {
      key: "target",
      header: "Target",
      render: (r) => (
        <span className="font-mono text-[12px] text-surface-300">{r.target || "—"}</span>
      ),
    },
    {
      key: "outcome",
      header: "Outcome",
      cellClass: "w-[120px]",
      render: (r) => {
        const tone =
          r.outcome === "success"
            ? "success"
            : r.outcome === "failure" || r.outcome === "error"
              ? "danger"
              : "muted";
        return <AdminBadge tone={tone as any}>{r.outcome || "—"}</AdminBadge>;
      },
    },
    {
      key: "actor",
      header: "Actor",
      sort: (a, b) => a.actor.localeCompare(b.actor),
      render: (r) => <span className="text-[12px] text-surface-200">{r.actor}</span>,
    },
  ];

  return (
    <AdminShell
      pageTitle="Audit log"
      pageSubtitle="Merged feed of userActivity + securityEvents, newest first. Filters apply server-side."
      actions={
        <>
          <button
            onClick={exportCsv}
            disabled={pages.length === 0}
            className="inline-flex items-center gap-1.5 rounded border border-surface-700 px-2.5 py-1.5 text-[12px] text-surface-200 hover:border-surface-500 hover:text-surface-100 disabled:opacity-40"
          >
            <Download className="h-3.5 w-3.5" />
            Export CSV
          </button>
          <button
            onClick={() => setFiltersTick((n) => n + 1)}
            className="inline-flex items-center gap-1.5 rounded border border-surface-700 px-2.5 py-1.5 text-[12px] text-surface-200 hover:border-surface-500 hover:text-surface-100"
          >
            <RefreshCcw className="h-3.5 w-3.5" />
            Refresh
          </button>
        </>
      }
    >
      {/* Filters */}
      <div className="mb-3 grid grid-cols-1 gap-2 rounded-md border border-surface-800 bg-surface-900 p-3 sm:grid-cols-4">
        <div>
          <label className="block text-[10px] font-medium uppercase tracking-wider text-surface-400">
            Actor email
          </label>
          <input
            value={actor}
            onChange={(e) => setActor(e.target.value)}
            placeholder="exact match"
            className="mt-1 w-full rounded border border-surface-700 bg-surface-950 px-2 py-1 font-mono text-[12px] text-surface-100 outline-none focus:border-warning"
          />
        </div>
        <div>
          <label className="block text-[10px] font-medium uppercase tracking-wider text-surface-400">
            Event type
          </label>
          <input
            value={eventType}
            onChange={(e) => setEventType(e.target.value)}
            placeholder="e.g. deploy, token_rotated"
            className="mt-1 w-full rounded border border-surface-700 bg-surface-950 px-2 py-1 font-mono text-[12px] text-surface-100 outline-none focus:border-warning"
          />
        </div>
        <div>
          <label className="block text-[10px] font-medium uppercase tracking-wider text-surface-400">
            Since
          </label>
          <input
            type="datetime-local"
            value={sinceLocal}
            onChange={(e) => setSinceLocal(e.target.value)}
            className="mt-1 w-full rounded border border-surface-700 bg-surface-950 px-2 py-1 font-mono text-[12px] text-surface-100 outline-none focus:border-warning"
          />
        </div>
        <div>
          <label className="block text-[10px] font-medium uppercase tracking-wider text-surface-400">
            Until
          </label>
          <input
            type="datetime-local"
            value={untilLocal}
            onChange={(e) => setUntilLocal(e.target.value)}
            className="mt-1 w-full rounded border border-surface-700 bg-surface-950 px-2 py-1 font-mono text-[12px] text-surface-100 outline-none focus:border-warning"
          />
        </div>
      </div>

      {error && (
        <div className="mb-3 rounded border border-danger/40 bg-danger-soft p-3 text-[12px] text-danger-softFg">
          Failed to load: {error}
        </div>
      )}

      <AdminTable<AuditRow>
        rows={pages}
        columns={columns}
        defaultSortKey="ts"
        defaultSortDir="desc"
        rowKey={(r, i) => `${r.timestamp}-${r.actorId}-${i}`}
        emptyState={
          loading ? (
            <span className="text-[12px] text-surface-400">Loading…</span>
          ) : (
            <EmptyState
              title="No matching events"
              body="Try widening the date range or clearing actor/event-type filters. Activity is written by the agent on every task completion; security events come from auth flows."
            />
          )
        }
      />

      <div className="mt-3 flex items-center justify-between text-[12px] text-surface-400">
        <span>
          {pages.length > 0 && (
            <>Latest cursor: <span className="font-mono">{cursor ?? "—"}</span></>
          )}
        </span>
        <div className="flex items-center gap-2">
          {exhausted && pages.length > 0 && (
            <span className="text-surface-400">No more events in window.</span>
          )}
          {!exhausted && (
            <button
              onClick={loadMore}
              disabled={loading}
              className="inline-flex items-center gap-1.5 rounded border border-surface-700 px-2.5 py-1.5 text-[12px] text-surface-200 hover:border-surface-500 hover:text-surface-100 disabled:opacity-40"
            >
              {loading ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : null}
              Load more
            </button>
          )}
        </div>
      </div>
    </AdminShell>
  );
}
