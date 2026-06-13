"use client";

// Screw-cell shop-floor analytics, rendered from the SAME agent ops verbs the
// firmware pushes to (screw_cell_record) and the coding agent reads via MCP:
// screw_cell_analytics / screw_cell_runs / screw_cell_by_order. Web is
// relay-first; the cell runs (per-block PASS/FAIL) live in the agent's vault
// ("screw-cell"/"runs"), never on Convex. This is the visual twin of the
// CircuitCellView: pick a device, see KPIs + a daily fail-rate trend + the
// flagged production orders + the worst blocks + recent runs.
import { useCallback, useEffect, useRef, useState } from "react";
import { AgentClient, agentClient } from "@/lib/agent-client";
import type { Device } from "@/lib/use-devices";

type Totals = { runs: number; screws: number; passed: number; failed: number; failRate: number };
type TrendPoint = { date: string; screws: number; failRate: number };
type LabelRow = { label: string; runs: number; screws: number; passed: number; failRate: number };
type FlaggedOrder = {
  ficheno: string;
  productId?: string;
  blocks: number;
  flaggedBlocks: number;
  screws: number;
  failed: number;
  failRate: number;
  lastAt?: number;
};
type RecentRun = { id: string; label?: string; ficheno?: string; screws: number; passed: number; failRate: number; createdAt?: number };
type Analytics = {
  window?: { days: number };
  totals: Totals;
  trend: TrendPoint[];
  byLabel: LabelRow[];
  flaggedOrders: FlaggedOrder[];
  recent: RecentRun[];
};

const WINDOWS = [7, 30, 90] as const;

function fmtTime(t?: number): string {
  if (!t) return "—";
  const ms = t > 1e12 ? t : t * 1000; // accept seconds or millis
  try {
    return new Date(ms).toLocaleString();
  } catch {
    return String(t);
  }
}

function rateColor(r: number): string {
  if (r <= 0) return "text-emerald-400";
  if (r < 5) return "text-amber-300";
  return "text-rose-400";
}
function rateFill(r: number): string {
  if (r <= 0) return "#34d399"; // emerald
  if (r < 5) return "#fcd34d"; // amber
  return "#fb7185"; // rose
}

export default function ScrewCellView({ devices, token }: { devices: Device[]; token: string | null }) {
  const [deviceId, setDeviceId] = useState("");
  const [days, setDays] = useState<(typeof WINDOWS)[number]>(30);
  const [data, setData] = useState<Analytics | null>(null);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  const clientRef = useRef<AgentClient | null>(null);
  const connectedTo = useRef("");

  const ensureClient = useCallback(
    async (id: string): Promise<AgentClient | null> => {
      const device = devices.find((d) => d.id === id);
      if (!device || !token) return null;
      if (clientRef.current && connectedTo.current === id) return clientRef.current;
      try {
        clientRef.current?.disconnect();
      } catch {}
      clientRef.current = null;
      connectedTo.current = "";
      const client = new AgentClient();
      client.setRelayServers(agentClient.configuredRelayServers.map((r) => ({ ...r })));
      const tunnelUrls = Array.from(
        new Set([...(Array.isArray(device.publicEndpoints) ? device.publicEndpoints : []), ...(device.tunnelUrl ? [device.tunnelUrl] : [])]),
      );
      await client.connect(device.host, device.port, token, device.id, { tunnelUrls });
      clientRef.current = client;
      connectedTo.current = id;
      return client;
    },
    [devices, token],
  );

  const call = useCallback(
    async (verb: string, payload: Record<string, unknown> = {}): Promise<any> => {
      try {
        const client = await ensureClient(deviceId);
        if (!client) return { ok: false, error: "not connected" };
        const res = await client.callOps(verb, payload);
        if (res?.ok === false) return { ok: false, code: res.code, error: res.error };
        return (res as any)?.initial ?? res;
      } catch (e: any) {
        setErr(e?.message || "connection failed");
        return { ok: false, error: e?.message || "failed" };
      }
    },
    [deviceId, ensureClient],
  );

  const refresh = useCallback(async () => {
    if (!deviceId) return;
    setBusy(true);
    setErr(null);
    const r = await call("screw_cell_analytics", { days });
    if (r?.ok === false) setErr(r.error || "failed");
    else if (r?.totals) setData(r as Analytics);
    setBusy(false);
  }, [call, deviceId, days]);

  useEffect(() => {
    if (deviceId) refresh();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [deviceId, days]);

  const card = "rounded-xl border border-white/10 bg-white/[0.03] p-4";
  const btn = "rounded-lg px-3 py-1.5 text-sm font-medium transition disabled:opacity-40";

  const trend = data?.trend ?? [];
  const maxRate = Math.max(5, ...trend.map((t) => t.failRate)); // floor at 5% so a clean line isn't a full bar

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between gap-3">
        <h2 className="text-lg font-semibold">🔩 Screw Cell</h2>
        <div className="flex items-center gap-2">
          <select
            value={deviceId}
            onChange={(e) => setDeviceId(e.target.value)}
            className="rounded-lg border border-white/10 bg-black/30 px-2 py-1.5 text-sm"
          >
            <option value="">Pick a device…</option>
            {devices.map((d) => (
              <option key={d.id} value={d.id}>
                {d.name || d.id}
              </option>
            ))}
          </select>
        </div>
      </div>

      {!deviceId && (
        <p className="text-sm text-white/50">
          Select a device whose Yaver agent receives screw-cell runs (firmware pushes via <code className="text-white/70">cell_runner.py --yaver</code>).
        </p>
      )}

      {deviceId && (
        <>
          <div className="flex flex-wrap items-center gap-2">
            <span className="text-sm font-medium">Window</span>
            {WINDOWS.map((w) => (
              <button
                key={w}
                onClick={() => setDays(w)}
                className={`${btn} ${days === w ? "bg-emerald-500/80 text-black" : "bg-white/5 hover:bg-white/10"}`}
              >
                {w}d
              </button>
            ))}
            <button onClick={refresh} disabled={busy} className={`${btn} ml-2 bg-sky-500/80 text-black hover:bg-sky-400`}>
              {busy ? "…" : "Refresh"}
            </button>
          </div>

          {err && <p className="text-sm text-rose-400">{err}</p>}

          {!data && !busy && <p className="text-sm text-white/50">No runs in this window yet.</p>}

          {data && (
            <>
              {/* KPI cards */}
              <div className="grid grid-cols-2 gap-3 sm:grid-cols-4">
                {[
                  { k: "Runs", v: data.totals.runs, c: "text-white" },
                  { k: "Screws", v: data.totals.screws, c: "text-white" },
                  { k: "Passed", v: data.totals.passed, c: "text-emerald-400" },
                  { k: "Failed", v: data.totals.failed, c: "text-rose-400" },
                ].map((m) => (
                  <div key={m.k} className={card}>
                    <div className="text-xs text-white/50">{m.k}</div>
                    <div className={`text-2xl font-semibold ${m.c}`}>{m.v}</div>
                  </div>
                ))}
              </div>
              <div className={card}>
                <div className="flex items-baseline justify-between">
                  <span className="text-sm text-white/60">Fail rate ({data.window?.days ?? days}d)</span>
                  <span className={`text-3xl font-bold ${rateColor(data.totals.failRate)}`}>{data.totals.failRate}%</span>
                </div>
              </div>

              {/* Daily fail-rate trend (inline SVG bars) */}
              <div className={card}>
                <div className="mb-3 text-sm font-medium">Daily fail rate</div>
                {trend.length === 0 ? (
                  <p className="text-xs text-white/40">No daily data.</p>
                ) : (
                  <svg viewBox={`0 0 ${Math.max(trend.length * 28, 60)} 120`} className="h-32 w-full" preserveAspectRatio="none">
                    {/* gridlines */}
                    {[0, 0.5, 1].map((g) => (
                      <line key={g} x1={0} x2={trend.length * 28} y1={10 + g * 90} y2={10 + g * 90} stroke="rgba(255,255,255,0.08)" strokeWidth={1} />
                    ))}
                    {trend.map((t, i) => {
                      const h = Math.max(1, (t.failRate / maxRate) * 90);
                      return (
                        <g key={t.date}>
                          <rect x={i * 28 + 6} y={100 - h} width={16} height={h} rx={2} fill={rateFill(t.failRate)}>
                            <title>{`${t.date}: ${t.failRate}% (${t.screws} screws)`}</title>
                          </rect>
                        </g>
                      );
                    })}
                  </svg>
                )}
                <div className="mt-1 flex justify-between text-[10px] text-white/40">
                  <span>{trend[0]?.date}</span>
                  <span>peak {maxRate}%</span>
                  <span>{trend[trend.length - 1]?.date}</span>
                </div>
              </div>

              {/* Flagged production orders */}
              <div className={card}>
                <div className="mb-2 text-sm font-medium">
                  Flagged production orders {data.flaggedOrders.length > 0 && <span className="text-rose-400">({data.flaggedOrders.length})</span>}
                </div>
                {data.flaggedOrders.length === 0 ? (
                  <p className="text-xs text-emerald-400/80">None flagged — every order passed clean. ✅</p>
                ) : (
                  <table className="w-full text-left text-xs">
                    <thead className="text-white/40">
                      <tr>
                        <th className="py-1 pr-2">Ficheno</th>
                        <th className="py-1 pr-2">Product</th>
                        <th className="py-1 pr-2 text-right">Blocks</th>
                        <th className="py-1 pr-2 text-right">Flagged</th>
                        <th className="py-1 pr-2 text-right">Failed</th>
                        <th className="py-1 pr-2 text-right">Fail %</th>
                        <th className="py-1 pr-2">Last</th>
                      </tr>
                    </thead>
                    <tbody>
                      {data.flaggedOrders.map((o) => (
                        <tr key={o.ficheno} className="border-t border-white/5">
                          <td className="py-1 pr-2 font-mono text-white/80">{o.ficheno}</td>
                          <td className="py-1 pr-2 text-white/60">{o.productId || "—"}</td>
                          <td className="py-1 pr-2 text-right">{o.blocks}</td>
                          <td className="py-1 pr-2 text-right text-rose-300">{o.flaggedBlocks}</td>
                          <td className="py-1 pr-2 text-right">{o.failed}</td>
                          <td className={`py-1 pr-2 text-right font-semibold ${rateColor(o.failRate)}`}>{o.failRate}%</td>
                          <td className="py-1 pr-2 text-white/40">{fmtTime(o.lastAt)}</td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                )}
              </div>

              {/* Per-block (worst first) */}
              <div className={card}>
                <div className="mb-2 text-sm font-medium">Blocks — worst first</div>
                {data.byLabel.length === 0 ? (
                  <p className="text-xs text-white/40">No labelled blocks.</p>
                ) : (
                  <table className="w-full text-left text-xs">
                    <thead className="text-white/40">
                      <tr>
                        <th className="py-1 pr-2">Block</th>
                        <th className="py-1 pr-2 text-right">Runs</th>
                        <th className="py-1 pr-2 text-right">Screws</th>
                        <th className="py-1 pr-2 text-right">Passed</th>
                        <th className="py-1 pr-2 text-right">Fail %</th>
                      </tr>
                    </thead>
                    <tbody>
                      {data.byLabel.map((b) => (
                        <tr key={b.label} className="border-t border-white/5">
                          <td className="py-1 pr-2 text-white/80">{b.label || "(unlabelled)"}</td>
                          <td className="py-1 pr-2 text-right">{b.runs}</td>
                          <td className="py-1 pr-2 text-right">{b.screws}</td>
                          <td className="py-1 pr-2 text-right text-emerald-300">{b.passed}</td>
                          <td className={`py-1 pr-2 text-right font-semibold ${rateColor(b.failRate)}`}>{b.failRate}%</td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                )}
              </div>

              {/* Recent runs */}
              <div className={card}>
                <div className="mb-2 text-sm font-medium">Recent runs</div>
                {data.recent.length === 0 ? (
                  <p className="text-xs text-white/40">No recent runs.</p>
                ) : (
                  <div className="space-y-1">
                    {data.recent.map((r) => (
                      <div key={r.id} className="flex items-center justify-between border-t border-white/5 py-1 text-xs">
                        <span className="text-white/70">
                          {r.label || r.id}
                          {r.ficheno ? <span className="ml-2 font-mono text-white/40">{r.ficheno}</span> : null}
                        </span>
                        <span className="flex items-center gap-3">
                          <span className="text-white/50">
                            {r.passed}/{r.screws}
                          </span>
                          <span className={`font-semibold ${rateColor(r.failRate)}`}>{r.failRate}%</span>
                          <span className="text-white/30">{fmtTime(r.createdAt)}</span>
                        </span>
                      </div>
                    ))}
                  </div>
                )}
              </div>
            </>
          )}
        </>
      )}
    </div>
  );
}
