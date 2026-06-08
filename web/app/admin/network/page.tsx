// Org admin — Yaver Mesh fleet. Read-only org-wide view of every mesh node
// across all users: overlay IP, owner, online, exit-node / advertised routes,
// last handshake. Per-user node controls live in the dashboard NetworkView;
// this is the operator's bird's-eye view.
"use client";

import { useMemo, useState } from "react";
import { AdminShell } from "@/components/admin/AdminShell";
import { AdminTable, type Column } from "@/components/admin/AdminTable";
import { AdminBadge } from "@/components/admin/AdminBadge";
import { EmptyState } from "@/components/admin/EmptyState";
import { useAdminFetch } from "@/components/admin/useAdminFetch";
import { Globe, RefreshCcw, Search } from "@/components/admin/icons";

type MeshNodeRow = {
  deviceId: string;
  alias: string | null;
  ownerEmail: string;
  meshIPv4: string;
  online: boolean;
  isExitNode: boolean;
  advertisedRoutes: string[];
  lastHandshake: number;
  updatedAt: number;
};

type FleetMesh = { aclCount: number; nodes: MeshNodeRow[] };

function shortAgo(ms: number): string {
  if (!ms) return "never";
  const s = Math.floor((Date.now() - ms) / 1000);
  if (s < 60) return `${s}s ago`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}m ago`;
  const h = Math.floor(m / 60);
  if (h < 24) return `${h}h ago`;
  return `${Math.floor(h / 24)}d ago`;
}

export default function AdminNetworkPage() {
  const { data, loading, error, refresh } = useAdminFetch<FleetMesh>("/admin/mesh");
  const [q, setQ] = useState("");

  const filtered = useMemo(() => {
    const nodes = data?.nodes ?? [];
    if (!q.trim()) return nodes;
    const needle = q.toLowerCase();
    return nodes.filter(
      (n) =>
        n.ownerEmail.toLowerCase().includes(needle) ||
        (n.alias ?? "").toLowerCase().includes(needle) ||
        n.meshIPv4.includes(needle) ||
        n.deviceId.toLowerCase().includes(needle)
    );
  }, [data, q]);

  const columns: Column<MeshNodeRow>[] = [
    {
      key: "status",
      header: "",
      render: (n) => (
        <span
          className={`inline-block h-2 w-2 rounded-full ${n.online ? "bg-emerald-400" : "bg-surface-600"}`}
          title={n.online ? "online" : "offline"}
        />
      ),
    },
    {
      key: "node",
      header: "Node",
      sort: (a, b) => (a.alias ?? a.deviceId).localeCompare(b.alias ?? b.deviceId),
      render: (n) => (
        <span className="text-[13px] text-surface-100">{n.alias ?? n.deviceId.slice(0, 12)}</span>
      ),
    },
    {
      key: "ip",
      header: "Overlay IP",
      sort: (a, b) => a.meshIPv4.localeCompare(b.meshIPv4),
      render: (n) => <code className="text-[12px] text-emerald-700 dark:text-emerald-300">{n.meshIPv4}</code>,
    },
    {
      key: "owner",
      header: "Owner",
      sort: (a, b) => a.ownerEmail.localeCompare(b.ownerEmail),
      render: (n) => <span className="text-[13px] text-surface-200">{n.ownerEmail}</span>,
    },
    {
      key: "roles",
      header: "Roles",
      render: (n) => (
        <span className="flex flex-wrap gap-1">
          {n.isExitNode && <AdminBadge tone="warning">exit node</AdminBadge>}
          {n.advertisedRoutes.filter((r) => r !== "0.0.0.0/0").map((r) => (
            <AdminBadge key={r} tone="info">{r}</AdminBadge>
          ))}
          {!n.isExitNode && n.advertisedRoutes.filter((r) => r !== "0.0.0.0/0").length === 0 && (
            <span className="text-[12px] text-surface-500">—</span>
          )}
        </span>
      ),
    },
    {
      key: "handshake",
      header: "Last handshake",
      sort: (a, b) => a.lastHandshake - b.lastHandshake,
      render: (n) => <span className="text-[12px] text-surface-400">{shortAgo(n.lastHandshake)}</span>,
    },
  ];

  return (
    <AdminShell
      pageTitle="Mesh"
      pageSubtitle="Org-wide WireGuard overlay — every node across all users"
      actions={
        <button
          onClick={refresh}
          className="flex items-center gap-1.5 rounded-lg border border-surface-700 bg-surface-900 px-3 py-1.5 text-[13px] text-surface-200 hover:bg-surface-800"
        >
          <RefreshCcw className="h-3.5 w-3.5" /> Refresh
        </button>
      }
    >
      {error ? (
        <div className="rounded-xl border border-red-500/30 bg-red-500/10 p-4 text-sm text-red-700 dark:text-red-200">{error}</div>
      ) : null}

      <div className="mb-4 flex items-center gap-3">
        <div className="flex items-center gap-2 text-surface-300">
          <Globe className="h-4 w-4" />
          <span className="text-[13px]">
            {data ? `${data.nodes.length} node${data.nodes.length === 1 ? "" : "s"} · ${data.aclCount} ACL rule${data.aclCount === 1 ? "" : "s"}` : "—"}
          </span>
        </div>
        <div className="relative ml-auto">
          <Search className="pointer-events-none absolute left-2 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-surface-500" />
          <input
            value={q}
            onChange={(e) => setQ(e.target.value)}
            placeholder="Search owner / alias / IP…"
            className="w-64 rounded-lg border border-surface-700 bg-surface-950 py-1.5 pl-7 pr-2 text-[13px] text-surface-200"
          />
        </div>
      </div>

      <AdminTable<MeshNodeRow>
        rows={filtered}
        columns={columns}
        defaultSortKey="owner"
        defaultSortDir="asc"
        total={data?.nodes.length}
        rowKey={(n) => n.deviceId}
        emptyState={
          loading ? (
            <span className="text-[12px] text-surface-400">Loading…</span>
          ) : (
            <EmptyState title="No mesh nodes" body="Nodes appear here after a device runs `yaver mesh up`." />
          )
        }
      />
    </AdminShell>
  );
}
