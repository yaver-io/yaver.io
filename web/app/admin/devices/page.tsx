// Org admin — Devices fleet. Sortable table with rescue / reinstall /
// revoke row actions. Destructive actions go through ConfirmDestructive
// with the device alias as the type-to-confirm phrase.
"use client";

import { useMemo, useState } from "react";
import { AdminShell } from "@/components/admin/AdminShell";
import { AdminTable, type Column } from "@/components/admin/AdminTable";
import { AdminBadge } from "@/components/admin/AdminBadge";
import { ConfirmDestructive } from "@/components/admin/ConfirmDestructive";
import { EmptyState } from "@/components/admin/EmptyState";
import { adminPost, useAdminFetch } from "@/components/admin/useAdminFetch";
import { RefreshCcw, Search } from "@/components/admin/icons";
import { useToast } from "@/components/admin/Toaster";

type DeviceRow = {
  _id: string;
  deviceId: string;
  name: string;
  alias: string | null;
  ownerEmail: string;
  ownerId: string;
  platform: "macos" | "windows" | "linux" | "android" | "ios";
  agentVersion: string | null;
  lastHeartbeat: number;
  isOnline: boolean;
  runnerDown: boolean;
  needsAuth: boolean;
};

function shortHeartbeat(ms: number): string {
  if (!ms) return "never";
  const diff = Date.now() - ms;
  const s = Math.floor(diff / 1000);
  if (s < 60) return `${s}s ago`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}m ago`;
  const h = Math.floor(m / 60);
  if (h < 24) return `${h}h ago`;
  return `${Math.floor(h / 24)}d ago`;
}

type PendingAction =
  | { kind: "rescue"; device: DeviceRow }
  | { kind: "reinstall"; device: DeviceRow }
  | { kind: "revoke"; device: DeviceRow };

export default function AdminDevices() {
  const { data, error, loading, refresh } = useAdminFetch<{ rows: DeviceRow[] }>("/admin/devices");
  const [query, setQuery] = useState("");
  const [pending, setPending] = useState<PendingAction | null>(null);
  const toast = useToast();

  const filtered = useMemo(() => {
    const all = data?.rows ?? [];
    const q = query.trim().toLowerCase();
    if (!q) return all;
    return all.filter(
      (d) =>
        d.name.toLowerCase().includes(q) ||
        (d.alias ?? "").toLowerCase().includes(q) ||
        d.ownerEmail.toLowerCase().includes(q) ||
        d.deviceId.toLowerCase().includes(q) ||
        d.platform.includes(q),
    );
  }, [data, query]);

  const columns: Column<DeviceRow>[] = [
    {
      key: "status",
      header: "",
      cellClass: "w-6",
      render: (d) => (
        <span
          className={`block h-2 w-2 rounded-full ${
            d.runnerDown || d.needsAuth
              ? "bg-danger"
              : d.isOnline
                ? "bg-success"
                : "bg-surface-600"
          }`}
          title={
            d.runnerDown
              ? "runner down"
              : d.needsAuth
                ? "needs re-auth"
                : d.isOnline
                  ? "online"
                  : "offline"
          }
        />
      ),
    },
    {
      key: "name",
      header: "Name",
      sort: (a, b) => a.name.localeCompare(b.name),
      render: (d) => (
        <div className="min-w-0">
          <div className="truncate text-[13px] text-surface-100">{d.name}</div>
          <div className="truncate font-mono text-[11px] text-surface-400">
            {d.alias ? `@${d.alias}` : d.deviceId.slice(0, 12)}
          </div>
        </div>
      ),
    },
    {
      key: "owner",
      header: "Owner",
      sort: (a, b) => a.ownerEmail.localeCompare(b.ownerEmail),
      render: (d) => <span className="text-[13px] text-surface-200">{d.ownerEmail}</span>,
    },
    {
      key: "platform",
      header: "Platform",
      sort: (a, b) => a.platform.localeCompare(b.platform),
      render: (d) => <AdminBadge tone="muted">{d.platform}</AdminBadge>,
    },
    {
      key: "agent",
      header: "Agent",
      sort: (a, b) => (a.agentVersion ?? "").localeCompare(b.agentVersion ?? ""),
      render: (d) => (
        <span className="font-mono text-[12px] text-surface-300">
          {d.agentVersion ?? "—"}
        </span>
      ),
    },
    {
      key: "heartbeat",
      header: "Last seen",
      sort: (a, b) => a.lastHeartbeat - b.lastHeartbeat,
      render: (d) => (
        <span className="font-mono text-[12px] tabular-nums text-surface-300">
          {shortHeartbeat(d.lastHeartbeat)}
        </span>
      ),
    },
    {
      key: "flags",
      header: "Flags",
      cellClass: "min-w-[140px]",
      render: (d) => (
        <div className="flex flex-wrap items-center gap-1">
          {d.needsAuth && <AdminBadge tone="danger">needs auth</AdminBadge>}
          {d.runnerDown && <AdminBadge tone="warning">runner down</AdminBadge>}
          {!d.needsAuth && !d.runnerDown && (
            <AdminBadge tone={d.isOnline ? "success" : "muted"}>
              {d.isOnline ? "online" : "offline"}
            </AdminBadge>
          )}
        </div>
      ),
    },
    {
      key: "actions",
      header: "",
      cellClass: "w-[260px] text-right",
      render: (d) => (
        <div className="flex items-center justify-end gap-1">
          <button
            onClick={() => setPending({ kind: "rescue", device: d })}
            className="rounded border border-surface-700 px-2 py-1 text-[11px] text-surface-200 hover:border-surface-500 hover:text-surface-100"
            title="Send rescue (tunnel-reset) command on next heartbeat"
          >
            Rescue
          </button>
          <button
            onClick={() => setPending({ kind: "reinstall", device: d })}
            className="rounded border border-surface-700 px-2 py-1 text-[11px] text-surface-200 hover:border-surface-500 hover:text-surface-100"
            title="Queue reinstall-latest on next heartbeat"
          >
            Reinstall
          </button>
          <button
            onClick={() => setPending({ kind: "revoke", device: d })}
            className="rounded border border-danger/40 bg-danger-soft px-2 py-1 text-[11px] font-medium text-danger-softFg hover:opacity-90"
          >
            Revoke
          </button>
        </div>
      ),
    },
  ];

  return (
    <AdminShell
      pageTitle="Devices"
      pageSubtitle="Every agent registered against this deployment. Heartbeats older than 5 minutes show as offline."
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
      <div className="mb-3 flex items-center gap-2">
        <div className="relative w-full max-w-sm">
          <Search className="pointer-events-none absolute left-2.5 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-surface-400" />
          <input
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            placeholder="Filter by name, alias, owner, platform…"
            className="w-full rounded border border-surface-700 bg-surface-900 py-1.5 pl-8 pr-2.5 text-[13px] text-surface-100 outline-none placeholder:text-surface-400 focus:border-warning"
          />
        </div>
      </div>

      {error && (
        <div className="mb-3 rounded border border-danger/40 bg-danger-soft p-3 text-[12px] text-danger-softFg">
          Failed to load: {error}
        </div>
      )}

      <AdminTable<DeviceRow>
        rows={filtered}
        columns={columns}
        defaultSortKey="heartbeat"
        defaultSortDir="desc"
        total={data?.rows.length}
        rowKey={(d) => d._id}
        emptyState={
          loading ? (
            <span className="text-[12px] text-surface-400">Loading…</span>
          ) : (
            <EmptyState
              title="No devices in this deployment"
              body="A device shows up here on its first heartbeat after `yaver auth` succeeds. If you expected one, check that the agent is reaching the same Convex URL as this dashboard."
            />
          )
        }
      />

      {pending && (
        <ConfirmDestructive
          open
          title={
            pending.kind === "rescue"
              ? `Send rescue to ${pending.device.name}`
              : pending.kind === "reinstall"
                ? `Reinstall agent on ${pending.device.name}`
                : `Revoke ${pending.device.name} from ${pending.device.ownerEmail}`
          }
          body={
            pending.kind === "rescue" ? (
              <span>
                Queues a <span className="font-mono">tunnel-reset</span> rescue command. The
                agent will pick it up on next heartbeat. Safe — does not delete the device.
              </span>
            ) : pending.kind === "reinstall" ? (
              <span>
                Queues <span className="font-mono">reinstall-latest</span>. The agent will
                download the current signed binary and bounce itself. Connections drop for
                ~10 seconds.
              </span>
            ) : (
              <span>
                Detaches this device from <span className="font-mono">{pending.device.ownerEmail}</span>{" "}
                and invalidates its session token. Owner has to re-auth to use the device
                again.
              </span>
            )
          }
          confirmLabel={
            pending.kind === "rescue"
              ? "Send rescue"
              : pending.kind === "reinstall"
                ? "Queue reinstall"
                : "Revoke device"
          }
          confirmPhrase={pending.device.alias || pending.device.deviceId.slice(0, 8)}
          destructive={pending.kind === "revoke"}
          onClose={() => setPending(null)}
          onConfirm={async () => {
            const d = pending.device;
            if (pending.kind === "revoke") {
              const result = await adminPost<{ ok: boolean; sessionsKilled?: number }>(
                "/admin/devices/revoke",
                { deviceDocId: d._id },
              );
              toast.push({
                tone: "success",
                title: `Revoked ${d.name}`,
                body: `Cleared ${result.sessionsKilled ?? 0} sessions; owner must re-auth.`,
              });
            } else {
              const command = pending.kind === "rescue" ? "tunnel-reset" : "reinstall-latest";
              await adminPost("/admin/devices/rescue", {
                deviceDocId: d._id,
                command,
              });
              toast.push({
                tone: "success",
                title: pending.kind === "rescue" ? "Rescue queued" : "Reinstall queued",
                body: `Agent will pick up on next heartbeat (${d.name}).`,
              });
            }
            refresh();
          }}
        />
      )}
    </AdminShell>
  );
}
