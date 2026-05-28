// Org admin — Users. Live list + promote/demote/sign-out/export/delete
// row actions. Destructive ops go through type-to-confirm.
"use client";

import { useMemo, useState } from "react";
import { AdminShell } from "@/components/admin/AdminShell";
import { AdminTable, type Column } from "@/components/admin/AdminTable";
import { AdminBadge } from "@/components/admin/AdminBadge";
import { ConfirmDestructive } from "@/components/admin/ConfirmDestructive";
import { EmptyState } from "@/components/admin/EmptyState";
import { adminPost, useAdminFetch } from "@/components/admin/useAdminFetch";
import { useToast } from "@/components/admin/Toaster";
import { CONVEX_URL } from "@/lib/constants";
import { RefreshCcw, Search } from "@/components/admin/icons";

type UserAction =
  | { kind: "promote"; user: UserRow }
  | { kind: "demote"; user: UserRow }
  | { kind: "sign-out"; user: UserRow }
  | { kind: "delete"; user: UserRow };

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

type UserRow = {
  _id: string;
  email: string;
  fullName: string;
  provider: string;
  mfaEnabled: boolean;
  emailVerified: boolean;
  teamCount: number;
  lastSeenAt: number;
  createdAt: number;
  platformRole: string | null;
};

function rel(ms: number): string {
  if (!ms) return "never";
  const d = Date.now() - ms;
  const m = Math.floor(d / 60000);
  if (m < 60) return `${m}m ago`;
  const h = Math.floor(m / 60);
  if (h < 24) return `${h}h ago`;
  return `${Math.floor(h / 24)}d ago`;
}

export default function AdminUsers() {
  const { data, error, loading, refresh } = useAdminFetch<{ rows: UserRow[] }>("/admin/users");
  const [query, setQuery] = useState("");
  const [pending, setPending] = useState<UserAction | null>(null);
  const [exportingId, setExportingId] = useState<string | null>(null);
  const toast = useToast();

  async function exportUser(u: UserRow) {
    const token = getStoredToken();
    if (!token) {
      toast.push({ tone: "danger", title: "Not signed in" });
      return;
    }
    setExportingId(u._id);
    try {
      const res = await fetch(`${CONVEX_URL}/admin/users/export`, {
        method: "POST",
        headers: {
          Authorization: `Bearer ${token}`,
          "Content-Type": "application/json",
        },
        body: JSON.stringify({ targetDocId: u._id }),
      });
      if (!res.ok) {
        const text = await res.text().catch(() => "");
        throw new Error(`${res.status} ${text || res.statusText}`);
      }
      const blob = await res.blob();
      const url = URL.createObjectURL(blob);
      const a = document.createElement("a");
      a.href = url;
      a.download = `yaver-user-${u.email}.json`;
      document.body.appendChild(a);
      a.click();
      document.body.removeChild(a);
      URL.revokeObjectURL(url);
      toast.push({
        tone: "success",
        title: "Exported user bundle",
        body: u.email,
      });
    } catch (err: any) {
      toast.push({
        tone: "danger",
        title: "Export failed",
        body: String(err?.message || err),
      });
    } finally {
      setExportingId(null);
    }
  }

  const filtered = useMemo(() => {
    const all = data?.rows ?? [];
    const q = query.trim().toLowerCase();
    if (!q) return all;
    return all.filter(
      (u) =>
        u.email.toLowerCase().includes(q) ||
        u.fullName.toLowerCase().includes(q) ||
        u.provider.includes(q),
    );
  }, [data, query]);

  const columns: Column<UserRow>[] = [
    {
      key: "email",
      header: "Email",
      sort: (a, b) => a.email.localeCompare(b.email),
      render: (u) => (
        <div className="min-w-0">
          <div className="truncate text-[13px] text-surface-100">{u.email || "—"}</div>
          {u.fullName && (
            <div className="truncate text-[11px] text-surface-400">{u.fullName}</div>
          )}
        </div>
      ),
    },
    {
      key: "provider",
      header: "Provider",
      sort: (a, b) => a.provider.localeCompare(b.provider),
      render: (u) => <AdminBadge tone="muted">{u.provider}</AdminBadge>,
    },
    {
      key: "mfa",
      header: "MFA",
      sort: (a, b) => Number(b.mfaEnabled) - Number(a.mfaEnabled),
      render: (u) =>
        u.mfaEnabled ? (
          <AdminBadge tone="success">on</AdminBadge>
        ) : (
          <AdminBadge tone="warning">off</AdminBadge>
        ),
    },
    {
      key: "verified",
      header: "Email",
      render: (u) =>
        u.emailVerified ? (
          <AdminBadge tone="success">verified</AdminBadge>
        ) : (
          <AdminBadge tone="muted">unverified</AdminBadge>
        ),
    },
    {
      key: "teams",
      header: "Teams",
      sort: (a, b) => a.teamCount - b.teamCount,
      cellClass: "text-right",
      render: (u) => (
        <span className="font-mono tabular-nums text-[12px] text-surface-300">
          {u.teamCount}
        </span>
      ),
    },
    {
      key: "lastSeen",
      header: "Last seen",
      sort: (a, b) => a.lastSeenAt - b.lastSeenAt,
      render: (u) => (
        <span className="font-mono text-[12px] tabular-nums text-surface-300">
          {rel(u.lastSeenAt)}
        </span>
      ),
    },
    {
      key: "role",
      header: "Role",
      render: (u) =>
        u.platformRole === "admin" ? (
          <AdminBadge tone="warning">admin</AdminBadge>
        ) : (
          <AdminBadge tone="muted">member</AdminBadge>
        ),
    },
    {
      key: "actions",
      header: "",
      cellClass: "w-[300px] text-right",
      render: (u) => (
        <div className="flex items-center justify-end gap-1">
          {u.platformRole === "admin" ? (
            <button
              onClick={() => setPending({ kind: "demote", user: u })}
              className="rounded border border-surface-700 px-2 py-1 text-[11px] text-surface-200 hover:border-surface-500 hover:text-surface-100"
            >
              Demote
            </button>
          ) : (
            <button
              onClick={() => setPending({ kind: "promote", user: u })}
              className="rounded border border-surface-700 px-2 py-1 text-[11px] text-surface-200 hover:border-surface-500 hover:text-surface-100"
            >
              Promote
            </button>
          )}
          <button
            onClick={() => setPending({ kind: "sign-out", user: u })}
            className="rounded border border-surface-700 px-2 py-1 text-[11px] text-surface-200 hover:border-surface-500 hover:text-surface-100"
          >
            Sign-out
          </button>
          <button
            onClick={() => exportUser(u)}
            disabled={exportingId === u._id}
            className="rounded border border-surface-700 px-2 py-1 text-[11px] text-surface-200 hover:border-surface-500 hover:text-surface-100 disabled:opacity-40"
          >
            {exportingId === u._id ? "…" : "Export"}
          </button>
          <button
            onClick={() => setPending({ kind: "delete", user: u })}
            className="rounded border border-danger/40 bg-danger-soft px-2 py-1 text-[11px] font-medium text-danger-softFg hover:opacity-90"
          >
            Delete
          </button>
        </div>
      ),
    },
  ];

  return (
    <AdminShell
      pageTitle="Users"
      pageSubtitle="Every signed-in identity on this deployment. Read-only this release; admin actions wire in next."
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
      <div className="mb-3 space-y-3">
        <div className="relative w-full max-w-sm">
          <Search className="pointer-events-none absolute left-2.5 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-surface-400" />
          <input
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            placeholder="Filter by email, name, provider…"
            className="w-full rounded border border-surface-700 bg-surface-900 py-1.5 pl-8 pr-2.5 text-[13px] text-surface-100 outline-none placeholder:text-surface-400 focus:border-warning"
          />
        </div>
      </div>

      {error && (
        <div className="mb-3 rounded border border-danger/40 bg-danger-soft p-3 text-[12px] text-danger-softFg">
          Failed to load: {error}
        </div>
      )}

      <AdminTable<UserRow>
        rows={filtered}
        columns={columns}
        defaultSortKey="lastSeen"
        defaultSortDir="desc"
        total={data?.rows.length}
        rowKey={(u) => u._id}
        emptyState={
          loading ? (
            <span className="text-[12px] text-surface-400">Loading…</span>
          ) : (
            <EmptyState
              title="No users yet"
              body="Users register here after their first OAuth or email sign-in. A fresh deployment is normal to see empty for a few hours."
            />
          )
        }
      />

      {pending && (
        <ConfirmDestructive
          open
          title={
            pending.kind === "promote"
              ? `Promote ${pending.user.email} to platform admin`
              : pending.kind === "demote"
                ? `Demote ${pending.user.email} from platform admin`
                : pending.kind === "sign-out"
                  ? `Sign out ${pending.user.email} everywhere`
                  : `Delete ${pending.user.email} and all their data`
          }
          body={
            pending.kind === "promote" ? (
              <span>
                Grants org-wide admin: access to <span className="font-mono">/admin</span>,
                fleet visibility, ability to promote/demote/delete. Does not change the
                user&apos;s normal dashboard. Reversible.
              </span>
            ) : pending.kind === "demote" ? (
              <span>
                Removes the admin role. The user keeps their account and normal
                dashboard. Reversible. Refused if this is the last admin and no env-var
                allowlist is set.
              </span>
            ) : pending.kind === "sign-out" ? (
              <span>
                Invalidates every session token for this user across web, mobile, and
                CLI. They will need to re-authenticate. Their data is untouched.
              </span>
            ) : (
              <span>
                <strong>Destructive and irreversible.</strong> Cascades the user row and
                every user-scoped table: sessions, devices, settings, identities,
                passkeys, activity, security events. Use only for GDPR right-to-erasure
                requests; otherwise prefer sign-out.
              </span>
            )
          }
          confirmLabel={
            pending.kind === "promote"
              ? "Promote"
              : pending.kind === "demote"
                ? "Demote"
                : pending.kind === "sign-out"
                  ? "Sign out everywhere"
                  : "Delete user"
          }
          confirmPhrase={pending.user.email}
          destructive={pending.kind === "delete"}
          onClose={() => setPending(null)}
          onConfirm={async () => {
            const u = pending.user;
            if (pending.kind === "promote") {
              await adminPost("/admin/users/promote", { targetEmail: u.email });
              toast.push({ tone: "success", title: "Promoted to admin", body: u.email });
            } else if (pending.kind === "demote") {
              await adminPost("/admin/users/demote", { targetDocId: u._id });
              toast.push({ tone: "success", title: "Demoted from admin", body: u.email });
            } else if (pending.kind === "sign-out") {
              const result = await adminPost<{ revoked: number }>(
                "/admin/users/sign-out",
                { targetDocId: u._id },
              );
              toast.push({
                tone: "success",
                title: `Signed out ${u.email}`,
                body: `${result.revoked} session(s) revoked.`,
              });
            } else {
              await adminPost("/admin/users/delete", { targetDocId: u._id });
              toast.push({
                tone: "warning",
                title: "User deleted",
                body: `${u.email} and all user-scoped data removed.`,
              });
            }
            refresh();
          }}
        />
      )}
    </AdminShell>
  );
}
