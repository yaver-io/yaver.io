"use client";

// ManagedCloudPanel — owner/dev surface for the managed-cloud
// lifecycle (docs/managed-cloud-host-lifecycle.md). Lets the owner
// (allowlist-gated server-side, no LemonSqueezy) ADOPT an existing
// Hetzner box as a managed machine and DECOMMISSION it (snapshot +
// delete via the managed teardown path). Every managed row carries
// the `origin` provenance tag ("managed" = bought from / adopted by
// Yaver; plain BYO devices in the list above are "self-hosted").
//
// Non-owners just see an empty list / 403s — the gate is the server
// (isCloudPreviewUser), never the client.

import { useCallback, useEffect, useState } from "react";
import { CONVEX_URL } from "@/lib/constants";

interface ManagedMachine {
  _id: string;
  machineType?: string;
  status?: string;
  origin?: "managed" | "self-hosted";
  hetznerServerId?: string;
  region?: string;
  serverIp?: string;
  errorMessage?: string;
}

export function ManagedCloudPanel({ token }: { token: string | null | undefined }) {
  const [machines, setMachines] = useState<ManagedMachine[]>([]);
  const [open, setOpen] = useState(false);
  const [adoptId, setAdoptId] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [note, setNote] = useState<string | null>(null);

  const load = useCallback(async () => {
    if (!token) return;
    try {
      const res = await fetch(`${CONVEX_URL}/subscription`, {
        headers: { Authorization: `Bearer ${token}` },
      });
      const data = await res.json().catch(() => ({}));
      setMachines(Array.isArray(data?.machines) ? data.machines : []);
    } catch (e: any) {
      setError(e?.message || String(e));
    }
  }, [token]);

  useEffect(() => {
    if (open) void load();
  }, [open, load]);

  async function post(path: string, body: Record<string, unknown>) {
    setBusy(true);
    setError(null);
    setNote(null);
    try {
      const res = await fetch(`${CONVEX_URL}${path}`, {
        method: "POST",
        headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/json" },
        body: JSON.stringify(body),
      });
      const data = await res.json().catch(() => ({}));
      if (!res.ok) {
        setError(data?.error || `${path} failed: ${res.status}`);
      } else {
        setNote(data?.note || data?.mode || "ok");
        await load();
        // deprovision schedules an async destroy action — poll so the
        // final row state (stopped, or error w/ the missing-token
        // message) surfaces without a manual refresh.
        if (path.includes("dev-deprovision")) {
          [2000, 5000, 9000].forEach((ms) => setTimeout(() => void load(), ms));
        }
      }
    } catch (e: any) {
      setError(e?.message || String(e));
    } finally {
      setBusy(false);
    }
  }

  if (!token) return null;

  return (
    <div className="mt-4 rounded-xl border border-slate-300 bg-white/60 p-4 dark:border-surface-700 dark:bg-[rgba(20,21,27,0.6)]">
      <button
        onClick={() => setOpen((v) => !v)}
        className="flex w-full items-center justify-between text-left text-sm font-semibold text-slate-700 dark:text-surface-200"
      >
        <span>☁ Managed cloud (owner) — adopt / decommission</span>
        <span className="text-xs opacity-60">{open ? "▾" : "▸"}</span>
      </button>

      {open ? (
        <div className="mt-3 space-y-3">
          <p className="text-xs text-slate-500 dark:text-surface-400">
            Boxes here are <b>managed</b> (provisioned/adopted by Yaver). Every
            other device in the list above is <b>self-hosted</b> (your own
            Hetzner / hardware). Adopt imitates a managed purchase for an
            existing box; Decommission snapshots then deletes it.
          </p>

          <div className="flex flex-wrap items-center gap-2">
            <input
              value={adoptId}
              onChange={(e) => setAdoptId(e.target.value)}
              placeholder="Existing Hetzner server id to adopt"
              className="flex-1 rounded-md border border-slate-300 bg-white px-2.5 py-1.5 text-xs dark:border-surface-700 dark:bg-[rgba(12,12,16,0.9)]"
            />
            <button
              disabled={busy || adoptId.trim() === ""}
              onClick={() => post("/billing/yaver-cloud/dev-adopt", { hetznerServerId: adoptId.trim() })}
              className="rounded-md border border-slate-300 px-3 py-1.5 text-xs font-semibold disabled:opacity-50 dark:border-surface-700"
            >
              {busy ? "…" : "Adopt as managed"}
            </button>
          </div>

          <div className="space-y-2">
            {machines.length === 0 ? (
              <p className="text-xs text-slate-400">No managed machines.</p>
            ) : (
              machines.map((m) => (
                <div
                  key={m._id}
                  className="rounded-md border border-slate-200 px-3 py-2 text-xs dark:border-surface-800"
                >
                 <div className="flex flex-wrap items-center justify-between gap-2">
                  <div className="flex items-center gap-2">
                    <span
                      className={`rounded-full px-2 py-0.5 text-[10px] font-semibold uppercase tracking-wider ${
                        (m.origin ?? "managed") === "managed"
                          ? "bg-sky-500/15 text-sky-600 dark:text-sky-300"
                          : "bg-slate-500/15 text-slate-500 dark:text-surface-300"
                      }`}
                    >
                      {m.origin ?? "managed"}
                    </span>
                    <span className="font-mono opacity-80">
                      {m.machineType ?? "cpu"} · srv {m.hetznerServerId ?? "—"} · {m.region ?? "eu"} ·{" "}
                      <span className={m.status === "error" ? "font-semibold text-rose-600 dark:text-rose-400" : ""}>
                        {m.status ?? "?"}
                      </span>
                    </span>
                  </div>
                  <button
                    disabled={busy}
                    onClick={() => {
                      if (!window.confirm(`Decommission managed machine ${m._id}?\nSnapshots then deletes the Hetzner box.`)) return;
                      void post("/billing/yaver-cloud/dev-deprovision", { machineId: m._id });
                    }}
                    className="rounded-md border border-rose-400/50 px-2.5 py-1 text-[11px] font-semibold text-rose-600 disabled:opacity-50 dark:text-rose-300"
                  >
                    Decommission
                  </button>
                 </div>
                  {m.errorMessage ? (
                    <p className="mt-1.5 text-[11px] text-rose-600 dark:text-rose-400">
                      {m.errorMessage}
                    </p>
                  ) : null}
                </div>
              ))
            )}
          </div>

          {note ? <p className="text-xs text-emerald-600 dark:text-emerald-400">✓ {note}</p> : null}
          {error ? <p className="text-xs text-rose-600 dark:text-rose-400">{error}</p> : null}
        </div>
      ) : null}
    </div>
  );
}
