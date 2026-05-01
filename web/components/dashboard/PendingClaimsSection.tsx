"use client";

// PendingClaimsSection — surfaces bootstrap-pending boxes that joined
// the user's relay but have no Convex devices row yet. Without this,
// a freshly-installed remote box (no prior `yaver auth`, only a relay
// tunnel up) is invisible from the dashboard. With it, a one-click
// claim creates the proper devices row and the existing reauth flow
// finishes the pairing handshake automatically.

import { useState } from "react";
import type { PendingDeviceClaim } from "@/lib/use-devices";

function formatRelativeTime(ms: number): string {
  if (!ms) return "just now";
  const delta = Date.now() - ms;
  if (delta < 60_000) return "just now";
  if (delta < 3_600_000) return `${Math.floor(delta / 60_000)}m ago`;
  if (delta < 86_400_000) return `${Math.floor(delta / 3_600_000)}h ago`;
  return `${Math.floor(delta / 86_400_000)}d ago`;
}

export default function PendingClaimsSection(props: {
  items: PendingDeviceClaim[];
  onClaim: (deviceId: string, name?: string) => Promise<{ ok: boolean; error?: string }>;
  onRefresh: () => Promise<void>;
}) {
  const { items, onClaim, onRefresh } = props;
  const [busyId, setBusyId] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [renameDraft, setRenameDraft] = useState<Record<string, string>>({});

  if (items.length === 0) return null;

  const handleClaim = async (item: PendingDeviceClaim) => {
    setBusyId(item.deviceId);
    setError(null);
    try {
      const name = renameDraft[item.deviceId]?.trim() || undefined;
      const result = await onClaim(item.deviceId, name);
      if (!result.ok) {
        setError(`${item.name || item.deviceId.slice(0, 8)}: ${result.error || "claim failed"}`);
        return;
      }
      setRenameDraft((prev) => {
        const next = { ...prev };
        delete next[item.deviceId];
        return next;
      });
    } finally {
      setBusyId(null);
    }
  };

  return (
    <section
      aria-label="Pending device claims"
      className="rounded-lg border border-amber-300 bg-amber-50 dark:border-amber-700 dark:bg-amber-950/30 p-4"
    >
      <header className="flex items-center justify-between gap-2 mb-3">
        <div>
          <h2 className="text-sm font-semibold text-amber-900 dark:text-amber-100">
            New devices waiting to be claimed ({items.length})
          </h2>
          <p className="text-xs text-amber-800/80 dark:text-amber-200/70 mt-0.5">
            These boxes joined your relay but haven't been paired yet. Claiming creates a proper device entry so you can sign it in from here.
          </p>
        </div>
        <button
          type="button"
          onClick={() => { void onRefresh(); }}
          className="text-xs text-amber-800 dark:text-amber-200 hover:underline"
        >
          Refresh
        </button>
      </header>

      {error ? (
        <div className="mb-2 rounded border border-red-300 bg-red-50 dark:border-red-700 dark:bg-red-950/40 px-3 py-2 text-xs text-red-700 dark:text-red-200">
          {error}
        </div>
      ) : null}

      <ul className="space-y-2">
        {items.map((item) => {
          const draft = renameDraft[item.deviceId] ?? item.name ?? "";
          const platformLabel = item.platform || "unknown";
          return (
            <li
              key={item.id}
              className="flex flex-col sm:flex-row sm:items-center gap-2 rounded border border-amber-200 dark:border-amber-800 bg-white dark:bg-surface-900 px-3 py-2"
            >
              <div className="flex-1 min-w-0">
                <div className="text-sm font-medium truncate">
                  {item.name || `Pending ${item.deviceId.slice(0, 8)}`}
                </div>
                <div className="text-[11px] text-surface-500 truncate">
                  {platformLabel} · id {item.deviceId.slice(0, 8)}… · seen {formatRelativeTime(item.lastSeenAt)}
                  {item.relayLabel ? ` · via ${item.relayLabel}` : ""}
                </div>
              </div>
              <input
                type="text"
                value={draft}
                onChange={(e) => setRenameDraft((prev) => ({ ...prev, [item.deviceId]: e.target.value }))}
                placeholder="Rename (optional)"
                aria-label={`Rename ${item.name || item.deviceId}`}
                className="w-full sm:w-44 text-xs px-2 py-1 rounded border border-surface-300 dark:border-surface-700 bg-transparent"
                disabled={busyId === item.deviceId}
              />
              <button
                type="button"
                onClick={() => { void handleClaim(item); }}
                disabled={busyId === item.deviceId}
                className="text-xs px-3 py-1.5 rounded bg-amber-600 hover:bg-amber-700 disabled:opacity-50 text-white font-medium"
              >
                {busyId === item.deviceId ? "Claiming…" : "Claim"}
              </button>
            </li>
          );
        })}
      </ul>
    </section>
  );
}
