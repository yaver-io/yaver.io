// Org admin — Sessions. Live list of active sessions + per-row revoke
// (deletes the session row server-side; tokens become invalid on next
// request via validateSession returning null).
"use client";

import { useState } from "react";
import { AdminShell } from "@/components/admin/AdminShell";
import { AdminTable, type Column } from "@/components/admin/AdminTable";
import { AdminBadge } from "@/components/admin/AdminBadge";
import { ConfirmDestructive } from "@/components/admin/ConfirmDestructive";
import { EmptyState } from "@/components/admin/EmptyState";
import { adminPost, useAdminFetch } from "@/components/admin/useAdminFetch";
import { useToast } from "@/components/admin/Toaster";
import { RefreshCcw } from "@/components/admin/icons";

type SessionRow = {
  _id: string;
  email: string;
  userId: string;
  deviceId: string | null;
  surface: string;
  createdAt: number;
  lastRefreshAt: number;
  expiresAt: number;
};

function isoShort(ms: number): string {
  if (!ms) return "—";
  return new Date(ms).toISOString().replace("T", " ").slice(0, 19);
}

function rel(ms: number): string {
  if (!ms) return "—";
  const d = Date.now() - ms;
  const m = Math.floor(d / 60000);
  if (m < 60) return `${m}m ago`;
  const h = Math.floor(m / 60);
  if (h < 24) return `${h}h ago`;
  return `${Math.floor(h / 24)}d ago`;
}

export default function AdminSessions() {
  const { data, error, loading, refresh } = useAdminFetch<{ rows: SessionRow[] }>("/admin/sessions");
  const [pending, setPending] = useState<SessionRow | null>(null);
  const toast = useToast();

  const columns: Column<SessionRow>[] = [
    {
      key: "user",
      header: "User",
      sort: (a, b) => a.email.localeCompare(b.email),
      render: (s) => <span className="text-[13px] text-surface-100">{s.email}</span>,
    },
    {
      key: "surface",
      header: "Surface",
      cellClass: "w-[100px]",
      sort: (a, b) => a.surface.localeCompare(b.surface),
      render: (s) => <AdminBadge tone="muted">{s.surface}</AdminBadge>,
    },
    {
      key: "device",
      header: "Device",
      render: (s) => (
        <span className="font-mono text-[12px] text-surface-300">
          {s.deviceId ? s.deviceId.slice(0, 12) : "—"}
        </span>
      ),
    },
    {
      key: "created",
      header: "Created",
      sort: (a, b) => a.createdAt - b.createdAt,
      render: (s) => (
        <span className="font-mono text-[12px] tabular-nums text-surface-300">
          {isoShort(s.createdAt)}
        </span>
      ),
    },
    {
      key: "refresh",
      header: "Last refresh",
      sort: (a, b) => a.lastRefreshAt - b.lastRefreshAt,
      render: (s) => (
        <span className="font-mono text-[12px] tabular-nums text-surface-300">
          {rel(s.lastRefreshAt)}
        </span>
      ),
    },
    {
      key: "actions",
      header: "",
      cellClass: "w-[100px] text-right",
      render: (s) => (
        <button
          onClick={() => setPending(s)}
          className="rounded border border-danger/40 bg-danger-soft px-2 py-1 text-[11px] font-medium text-danger-softFg hover:opacity-90"
        >
          Revoke
        </button>
      ),
    },
  ];

  return (
    <AdminShell
      pageTitle="Sessions"
      pageSubtitle="Active session tokens across web, mobile, and CLI surfaces."
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
        <div className="mb-3 rounded border border-danger/40 bg-danger-soft p-3 text-[12px] text-danger-softFg">
          Failed to load: {error}
        </div>
      )}

      <AdminTable<SessionRow>
        rows={data?.rows ?? []}
        columns={columns}
        defaultSortKey="refresh"
        defaultSortDir="desc"
        rowKey={(s) => s._id}
        emptyState={
          loading ? (
            <span className="text-[12px] text-surface-400">Loading…</span>
          ) : (
            <EmptyState
              title="No active sessions"
              body="Sessions appear here as users sign in on web, mobile, or CLI. Expired sessions are excluded server-side."
            />
          )
        }
      />

      {pending && (
        <ConfirmDestructive
          open
          title={`Revoke session for ${pending.email}`}
          body={
            <span>
              Invalidates this one session token (
              <span className="font-mono">{pending.surface}</span> surface on device{" "}
              <span className="font-mono">{pending.deviceId?.slice(0, 12) ?? "—"}</span>). Their other
              sessions stay live. Use the Users tab if you want to sign them out everywhere.
            </span>
          }
          confirmLabel="Revoke session"
          confirmPhrase={pending.email}
          destructive
          onClose={() => setPending(null)}
          onConfirm={async () => {
            await adminPost("/admin/sessions/revoke", { sessionDocId: pending._id });
            toast.push({
              tone: "success",
              title: "Session revoked",
              body: `${pending.email} (${pending.surface})`,
            });
            refresh();
          }}
        />
      )}
    </AdminShell>
  );
}
