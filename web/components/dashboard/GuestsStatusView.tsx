"use client";

import { useCallback, useEffect, useState } from "react";
import { CONVEX_URL } from "@/lib/constants";
import { useAuth } from "@/lib/use-auth";

// Read-only dashboard view that shows guest/host sharing state. Mutation
// (invite, accept, revoke) is done from mobile + CLI; the web surface is
// intentionally passive so there's no confusion about where the source of
// truth lives.

interface GuestInfo {
  email: string;
  status: "pending" | "accepted" | "revoked" | "expired";
  fullName?: string;
  userId?: string;
  inviteCode?: string;
  invitedByUserId?: boolean;
  proposedDeviceIds?: string[];
  createdAt: number;
  expiresAt?: number;
  acceptedAt?: number;
}

interface PendingHost {
  inviteId: string;
  inviteCode: string;
  hostUserId: string;
  hostName: string;
  hostEmail: string;
  hostUserIdString?: string;
  proposedDeviceIds?: string[];
  createdAt: number;
  expiresAt: number;
}

interface ActiveHost {
  hostUserId: string;
  hostName: string;
  hostEmail: string;
  grantedAt: number;
}

async function fetchGuests(token: string): Promise<GuestInfo[]> {
  const r = await fetch(`${CONVEX_URL}/guests/list`, {
    headers: { Authorization: `Bearer ${token}` },
  });
  if (!r.ok) throw new Error("Failed to load guests");
  return (await r.json()).guests ?? [];
}

async function fetchHosts(token: string): Promise<{ pending: PendingHost[]; active: ActiveHost[] }> {
  const r = await fetch(`${CONVEX_URL}/guests/hosts`, {
    headers: { Authorization: `Bearer ${token}` },
  });
  if (!r.ok) throw new Error("Failed to load hosts");
  return r.json();
}

function StatusBadge({ status }: { status: string }) {
  const map: Record<string, { bg: string; fg: string }> = {
    pending: { bg: "bg-amber-500/10 border-amber-500/40", fg: "text-amber-300" },
    accepted: { bg: "bg-emerald-500/10 border-emerald-500/40", fg: "text-emerald-300" },
    revoked: { bg: "bg-red-500/10 border-red-500/40", fg: "text-red-300" },
    expired: { bg: "bg-surface-800 border-surface-700", fg: "text-surface-400" },
    active: { bg: "bg-emerald-500/10 border-emerald-500/40", fg: "text-emerald-300" },
  };
  const tone = map[status] ?? map.pending;
  return (
    <span
      className={`inline-flex items-center rounded-full border px-2 py-0.5 text-[10px] font-bold uppercase tracking-wider ${tone.bg} ${tone.fg}`}
    >
      {status}
    </span>
  );
}

export default function GuestsStatusView() {
  const { token, user } = useAuth();
  const [guests, setGuests] = useState<GuestInfo[]>([]);
  const [hostsPending, setHostsPending] = useState<PendingHost[]>([]);
  const [hostsActive, setHostsActive] = useState<ActiveHost[]>([]);
  const [loading, setLoading] = useState(true);
  const [err, setErr] = useState<string | null>(null);

  const load = useCallback(async () => {
    if (!token) return;
    setLoading(true);
    setErr(null);
    try {
      const [g, h] = await Promise.all([fetchGuests(token), fetchHosts(token)]);
      setGuests(g);
      setHostsPending(h.pending || []);
      setHostsActive(h.active || []);
    } catch (e: any) {
      setErr(e?.message || String(e));
    } finally {
      setLoading(false);
    }
  }, [token]);

  useEffect(() => {
    load();
  }, [load]);

  async function copy(text: string) {
    try {
      await navigator.clipboard.writeText(text);
    } catch {
      /* noop */
    }
  }

  return (
    <div className="space-y-6">
      <div>
        <h2 className="text-xl font-semibold text-surface-100">Guest sharing</h2>
        <p className="text-sm text-surface-500">
          Read-only summary. Invite, accept, and revoke from the mobile app or the CLI (
          <code className="rounded bg-surface-900 px-1">yaver guests …</code>).
        </p>
      </div>

      {user?.id ? (
        <div className="rounded-lg border border-surface-800 bg-surface-900/40 p-4">
          <div className="flex items-center gap-3">
            <div className="flex-1">
              <div className="text-[10px] uppercase tracking-wider text-surface-500 font-bold">
                Your user ID
              </div>
              <div className="mt-1 font-mono text-sm text-surface-100 break-all">{user.id}</div>
              <div className="mt-1 text-xs text-surface-500">
                Share this so a friend can invite you without your email.
              </div>
            </div>
            <button
              onClick={() => copy(user.id)}
              className="rounded-lg border border-indigo-500/40 bg-indigo-500/10 px-3 py-2 text-xs font-semibold text-indigo-300 hover:bg-indigo-500/20"
            >
              Copy
            </button>
          </div>
        </div>
      ) : null}

      {err && <div className="rounded border border-red-500/40 bg-red-500/10 p-3 text-sm text-red-200">{err}</div>}
      {loading && <div className="text-sm text-surface-500">Loading…</div>}

      {/* Guests I'm hosting */}
      <section className="space-y-2">
        <h3 className="text-xs uppercase tracking-wider text-surface-500 font-bold">
          People I share with
        </h3>
        {guests.length === 0 ? (
          <p className="text-sm text-surface-500">No guests yet. Invite from the mobile app.</p>
        ) : (
          <ul className="divide-y divide-surface-800 rounded-lg border border-surface-800 bg-surface-900/40">
            {guests.map((g, i) => (
              <li key={g.email + String(i)} className="flex items-center gap-3 p-3">
                <div className="flex-1 min-w-0">
                  <div className="flex items-center gap-2">
                    <span className="text-sm text-surface-100 truncate">
                      {g.fullName || g.email || `user ${g.userId ?? ""}`}
                    </span>
                    <StatusBadge status={g.status} />
                    {g.invitedByUserId && (
                      <span className="rounded bg-indigo-500/10 border border-indigo-500/40 px-2 py-0.5 text-[10px] font-semibold text-indigo-300">
                        BY USER ID
                      </span>
                    )}
                  </div>
                  <div className="mt-1 text-xs text-surface-500">
                    {g.email ? g.email + " · " : ""}
                    {g.acceptedAt
                      ? "joined " + new Date(g.acceptedAt).toLocaleDateString()
                      : g.createdAt
                        ? "invited " + new Date(g.createdAt).toLocaleDateString()
                        : ""}
                    {g.proposedDeviceIds && g.proposedDeviceIds.length > 0
                      ? ` · scoped to ${g.proposedDeviceIds.length} machine${g.proposedDeviceIds.length === 1 ? "" : "s"}`
                      : ""}
                  </div>
                </div>
                {g.status === "pending" && g.inviteCode && (
                  <div className="flex items-center gap-2">
                    <code className="rounded bg-surface-900 px-2 py-1 text-xs font-mono text-surface-200 border border-surface-700">
                      {g.inviteCode}
                    </code>
                    <button
                      onClick={() => copy(g.inviteCode!)}
                      className="rounded border border-surface-700 bg-surface-900 px-2 py-1 text-[11px] text-surface-300 hover:bg-surface-800"
                    >
                      Copy
                    </button>
                  </div>
                )}
              </li>
            ))}
          </ul>
        )}
      </section>

      {/* Hosts I'm a guest of */}
      <section className="space-y-2">
        <h3 className="text-xs uppercase tracking-wider text-surface-500 font-bold">
          Machines shared with me
        </h3>
        {hostsPending.length === 0 && hostsActive.length === 0 ? (
          <p className="text-sm text-surface-500">Nobody has shared a machine with you yet.</p>
        ) : (
          <ul className="divide-y divide-surface-800 rounded-lg border border-surface-800 bg-surface-900/40">
            {hostsPending.map((h) => (
              <li key={h.inviteId} className="flex items-center gap-3 p-3">
                <div className="flex-1 min-w-0">
                  <div className="flex items-center gap-2">
                    <span className="text-sm text-surface-100 truncate">
                      {h.hostName}
                      <span className="text-surface-500">{" · "}{h.hostEmail}</span>
                    </span>
                    <StatusBadge status="pending" />
                  </div>
                  <div className="mt-1 text-xs text-surface-500">
                    Invited {new Date(h.createdAt).toLocaleDateString()} · expires{" "}
                    {new Date(h.expiresAt).toLocaleDateString()}
                    {h.proposedDeviceIds && h.proposedDeviceIds.length > 0
                      ? ` · scope: ${h.proposedDeviceIds.length} machine${h.proposedDeviceIds.length === 1 ? "" : "s"}`
                      : ""}
                  </div>
                </div>
                <span className="text-[11px] text-surface-400">Accept in mobile</span>
              </li>
            ))}
            {hostsActive.map((h) => (
              <li key={h.hostUserId} className="flex items-center gap-3 p-3">
                <div className="flex-1 min-w-0">
                  <div className="flex items-center gap-2">
                    <span className="text-sm text-surface-100 truncate">
                      {h.hostName}
                      <span className="text-surface-500">{" · "}{h.hostEmail}</span>
                    </span>
                    <StatusBadge status="active" />
                  </div>
                  <div className="mt-1 text-xs text-surface-500">
                    Since {new Date(h.grantedAt).toLocaleDateString()}
                  </div>
                </div>
              </li>
            ))}
          </ul>
        )}
      </section>
    </div>
  );
}
