"use client";

// DeviceStorageFold — the storage-reclaim panel folded into a device's details
// on the dashboard.
//
// A <details> fold, matching the "Other available agents (N)" / "Projects on
// this machine" pattern already used in DevicesView: collapsed by default,
// costs nothing until opened, and keeps the main dashboard exactly as it was.
// The scan shells out to `du` across the box's home dir, so it MUST stay
// lazy — it only runs when a human opens the fold.
//
// Connects a dedicated AgentClient to the target device (same as
// NetCaptureModal): the globally-connected agentClient points at whichever
// device is "current", which is not necessarily the one whose card you expanded.

import { useCallback, useRef, useState } from "react";
import { AgentClient, agentClient } from "@/lib/agent-client";
import type { Device } from "@/lib/use-devices";

interface Filesystem {
  mount: string;
  totalGb: number;
  usedGb: number;
  freeGb: number;
  usedPct: number;
}
interface Target {
  id: string;
  label: string;
  project?: string;
  sizeBytes: number;
  lastUsedMs?: number;
  rebuild: string;
}
interface Group {
  project: string;
  sizeBytes: number;
  targets: Target[];
}
interface Scan {
  filesystems: Filesystem[];
  groups: Group[];
  totalReclaimableBytes: number;
  partial?: boolean;
}

function fmtBytes(b: number): string {
  if (!b || b < 0) return "0 B";
  if (b >= 1 << 30) return `${(b / (1 << 30)).toFixed(1)} GB`;
  if (b >= 1 << 20) return `${(b / (1 << 20)).toFixed(0)} MB`;
  return `${(b / (1 << 10)).toFixed(0)} KB`;
}

function barColor(pct: number): string {
  if (pct >= 95) return "bg-rose-500";
  if (pct >= 85) return "bg-amber-400";
  return "bg-emerald-500";
}

export function DeviceStorageFold({ device, token }: { device: Device; token: string | null }) {
  const clientRef = useRef<AgentClient | null>(null);
  const [scan, setScan] = useState<Scan | null>(null);
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [loading, setLoading] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [msg, setMsg] = useState<string | null>(null);
  const [unsupported, setUnsupported] = useState(false);

  // Connect lazily, on first open — not on mount. A dashboard with 12 devices
  // must not open 12 relay connections just because the cards exist.
  const ensureClient = useCallback(async (): Promise<AgentClient> => {
    if (clientRef.current) return clientRef.current;
    if (!token) throw new Error("not signed in");
    const client = new AgentClient();
    client.setRelayServers(agentClient.configuredRelayServers.map((r) => ({ ...r })));
    const tunnelUrls = Array.from(
      new Set(
        [
          ...(Array.isArray(device.publicEndpoints) ? device.publicEndpoints : []),
          ...(device.tunnelUrl ? [device.tunnelUrl] : []),
        ]
          .map((u) => String(u || "").trim())
          .filter(Boolean),
      ),
    );
    await client.connect(device.host, device.port, token, device.id, { tunnelUrls });
    clientRef.current = client;
    return client;
  }, [device, token]);

  const refresh = useCallback(
    async (force: boolean) => {
      setLoading(true);
      setError(null);
      try {
        const client = await ensureClient();
        const res = await client.agentFetch(`/storage/scan${force ? "?refresh=1" : ""}`);
        // An agent older than this feature simply has no such route. Say so
        // plainly instead of rendering a scary failure.
        if (res.status === 404) {
          setUnsupported(true);
          return;
        }
        if (!res.ok) throw new Error(`scan failed (${res.status})`);
        const json = await res.json();
        setScan(json.scan);
        setSelected(new Set());
        setMsg(null);
      } catch (e) {
        setError(e instanceof Error ? e.message : String(e));
      } finally {
        setLoading(false);
      }
    },
    [ensureClient],
  );

  const selectedTargets: Target[] =
    scan?.groups.flatMap((g) => g.targets.filter((t) => selected.has(t.id))) ?? [];
  const selectedBytes = selectedTargets.reduce((sum, t) => sum + t.sizeBytes, 0);

  const reclaim = useCallback(async () => {
    // Name what's about to be deleted. Approving a number you were never shown
    // isn't approving anything.
    const summary = selectedTargets
      .slice(0, 10)
      .map((t) => `• ${t.project ? `${t.project} — ` : ""}${t.label} (${fmtBytes(t.sizeBytes)})`)
      .join("\n");
    const ok = window.confirm(
      `Free ${fmtBytes(selectedBytes)} on ${device.name}?\n\n${summary}` +
        (selectedTargets.length > 10 ? `\n…and ${selectedTargets.length - 10} more` : "") +
        `\n\nEvery one of these is a rebuildable cache — the toolchain regenerates it. Worst case is a slower next build.`,
    );
    if (!ok) return;

    setBusy(true);
    setError(null);
    try {
      const client = await ensureClient();
      const res = await client.agentFetch("/storage/reclaim", {
        method: "POST",
        body: JSON.stringify({ ids: Array.from(selected), confirm: true }),
      });
      if (!res.ok) throw new Error(`reclaim failed (${res.status})`);
      const json = await res.json();
      const r = json.result;
      const failed = (r.outcomes || []).filter((o: { ok: boolean }) => !o.ok);
      setMsg(
        `Freed ${r.freed} · ${r.rootFreeGbAfter?.toFixed(1)} GB free` +
          (failed.length ? ` · ${failed.length} target(s) failed` : ""),
      );
      if (failed.length) {
        setError(
          failed.map((o: { label: string; error: string }) => `${o.label}: ${o.error}`).join("; "),
        );
      }
      await refresh(true);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  }, [ensureClient, selected, selectedTargets, selectedBytes, device.name, refresh]);

  const toggle = (id: string) =>
    setSelected((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });

  return (
    <details
      className="mt-3 rounded-lg border border-slate-200 bg-slate-50/70 dark:border-surface-800 dark:bg-surface-900/30"
      onToggle={(e) => {
        if ((e.currentTarget as HTMLDetailsElement).open && !scan && !unsupported && !loading) {
          void refresh(false);
        }
      }}
    >
      <summary className="cursor-pointer select-none px-3 py-2 text-xs font-semibold text-surface-400">
        Storage & reclaim
        {scan ? (
          <span className="ml-2 font-normal text-surface-500">
            {fmtBytes(scan.totalReclaimableBytes)} reclaimable
          </span>
        ) : null}
      </summary>

      <div className="border-t border-slate-200 px-3 py-3 dark:border-surface-800">
        {unsupported ? (
          <p className="text-xs text-surface-500">
            This agent is too old for storage reclaim — update it to enable this panel.
          </p>
        ) : loading && !scan ? (
          <p className="text-xs text-surface-500">Scanning…</p>
        ) : (
          <>
            {scan?.filesystems.map((fs) => (
              <div key={fs.mount} className="mb-3">
                <div className="flex items-center justify-between text-[11px] text-surface-400">
                  <span className="font-mono">{fs.mount}</span>
                  <span>
                    {fs.freeGb.toFixed(1)} / {fs.totalGb.toFixed(0)} GB free
                  </span>
                </div>
                <div className="mt-1 h-1.5 w-full overflow-hidden rounded-full bg-surface-800">
                  <div
                    className={`h-full rounded-full ${barColor(fs.usedPct)}`}
                    style={{ width: `${Math.min(100, fs.usedPct)}%` }}
                  />
                </div>
              </div>
            ))}

            {scan && scan.groups.length === 0 && (
              <p className="text-xs text-surface-500">
                Nothing worth reclaiming — the caches on this box are already small.
              </p>
            )}

            {scan?.groups.map((g) => (
              <div key={g.project || "__shared"} className="mb-2">
                <div className="flex items-center justify-between text-[11px] font-semibold text-surface-300">
                  <span>{g.project || "Shared caches"}</span>
                  <span className="text-surface-500">{fmtBytes(g.sizeBytes)}</span>
                </div>
                {g.targets.map((t) => (
                  <label
                    key={t.id}
                    className="flex cursor-pointer items-center gap-2 py-0.5 pl-3 text-[11px] text-surface-400"
                    title={t.rebuild}
                  >
                    <input
                      type="checkbox"
                      checked={selected.has(t.id)}
                      onChange={() => toggle(t.id)}
                      className="h-3 w-3"
                    />
                    <span className="flex-1 truncate">{t.label}</span>
                    <span className="text-surface-500">{fmtBytes(t.sizeBytes)}</span>
                  </label>
                ))}
              </div>
            ))}

            <div className="mt-3 flex items-center gap-2">
              <button
                onClick={() => void refresh(true)}
                disabled={loading || busy}
                className="rounded border border-surface-700 px-2 py-1 text-[11px] text-surface-300 hover:bg-surface-800 disabled:opacity-50"
              >
                {loading ? "Scanning…" : "Rescan"}
              </button>
              {selected.size > 0 && (
                <button
                  onClick={() => void reclaim()}
                  disabled={busy}
                  className="rounded border border-rose-500/40 bg-rose-500/10 px-2 py-1 text-[11px] font-semibold text-rose-300 hover:bg-rose-500/20 disabled:opacity-50"
                >
                  {busy ? "Freeing…" : `Free ${fmtBytes(selectedBytes)} · ${selected.size} target(s)`}
                </button>
              )}
            </div>

            {msg && <p className="mt-2 text-[11px] text-emerald-400">{msg}</p>}
            {error && <p className="mt-2 text-[11px] text-amber-400">{error}</p>}
          </>
        )}
      </div>
    </details>
  );
}
