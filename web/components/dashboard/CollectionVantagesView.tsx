"use client";

// CollectionVantagesView — drive Yaver's general data-collection cell from the
// web dashboard over the relay. See this runtime's egress identity, lend/borrow
// egress between the owner's own machines (peer-egress), inspect per-vantage
// source health + blocks, and view the cross-vantage diff for a source. Web is
// relay-only by design. All verbs are the same the mobile cell calls.
//
// Collected DATA stays on the device (local-first, never Convex). This view only
// reads it back live via ops verbs.
import { useCallback, useEffect, useRef, useState } from "react";
import { AgentClient, agentClient } from "@/lib/agent-client";
import type { Device } from "@/lib/use-devices";

type Egress = {
  ip?: string;
  country?: string;
  region?: string;
  city?: string;
  asn?: string;
  org?: string;
  stable?: boolean;
  stableKnown?: boolean;
  source?: string;
};
type ProxyPolicy = { enabled?: boolean; allowPrivateTargets?: boolean; allowedPorts?: number[] };
type HealthRow = {
  sourceId: string;
  vantageId: string;
  state: string;
  geoBlockCount24h?: number;
  ipBlockCount24h?: number;
  rateLimitCount24h?: number;
  lastRows?: number;
};
type CompareRow = {
  vantageId: string;
  egressIp?: string;
  egressGeo?: string;
  egressCountry?: string;
  egressPolicy?: string;
  state?: string;
  values?: Record<string, unknown>;
};
type Compare = { sourceId: string; dataset?: string; fields?: string[]; vantages?: CompareRow[] };

const card = "rounded-xl border border-white/10 bg-white/[0.03] p-4";
const btn = "rounded-lg px-3 py-1.5 text-sm font-medium transition disabled:opacity-40";
const btnNeutral = `${btn} bg-white/5 hover:bg-white/10`;
const btnAccent = `${btn} bg-sky-500/80 text-black hover:bg-sky-400`;
const input = "rounded-lg border border-white/10 bg-black/30 px-3 py-1.5 text-sm outline-none focus:border-white/30";

function stateColor(state?: string): string {
  if (!state) return "text-white/40";
  if (state === "healthy") return "text-emerald-300";
  if (state.startsWith("blocked_") || state === "rate_limited") return "text-rose-300";
  return "text-amber-300";
}

export default function CollectionVantagesView({ devices, token }: { devices: Device[]; token: string | null }) {
  const [deviceId, setDeviceId] = useState("");
  const [egress, setEgress] = useState<Egress | null>(null);
  const [policy, setPolicy] = useState<ProxyPolicy | null>(null);
  const [health, setHealth] = useState<HealthRow[]>([]);
  const [blocked, setBlocked] = useState<HealthRow[]>([]);
  const [sourceId, setSourceId] = useState("");
  const [dataset, setDataset] = useState("");
  const [compare, setCompare] = useState<Compare | null>(null);
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState<string | null>(null);
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
        setMsg(e?.message || "connection failed");
        return { ok: false, error: e?.message || "failed" };
      }
    },
    [deviceId, ensureClient],
  );

  const refresh = useCallback(async () => {
    if (!deviceId) return;
    setBusy(true);
    setErr(null);
    setMsg(null);
    const eg = await call("runtime_egress");
    if (eg?.egress) setEgress(eg.egress);
    const st = await call("egress_proxy_status");
    if (st?.policy) setPolicy(st.policy);
    const h = await call("collection_source_health");
    if (Array.isArray(h?.health)) setHealth(h.health);
    const b = await call("block_list");
    if (Array.isArray(b?.blocked)) setBlocked(b.blocked);
    setBusy(false);
  }, [deviceId, call]);

  useEffect(() => {
    if (deviceId) refresh();
  }, [deviceId, refresh]);

  const toggleLending = useCallback(async () => {
    setBusy(true);
    setErr(null);
    const next = !(policy?.enabled ?? false);
    const r = await call("egress_proxy_set", { enabled: next });
    setBusy(false);
    if (r?.ok === false) {
      setErr(r.error || "could not update egress policy");
      return;
    }
    if (r?.policy) setPolicy(r.policy);
    setMsg(next ? "Egress lending enabled (owner-only, opt-in)" : "Egress lending disabled");
  }, [policy, call]);

  const doCompare = useCallback(async () => {
    if (!sourceId.trim()) {
      setErr("enter a sourceId to compare");
      return;
    }
    setBusy(true);
    setErr(null);
    setMsg(null);
    const r = await call("collection_vantage_compare", { sourceId: sourceId.trim(), dataset: dataset.trim() || undefined });
    setBusy(false);
    if (r?.ok === false) {
      setErr(r.error || "compare failed");
      return;
    }
    setCompare(r);
  }, [sourceId, dataset, call]);

  return (
    <div className="space-y-4">
      <div>
        <h1 className="text-lg font-semibold">Data Collection · Vantages</h1>
        <p className="text-sm text-white/50">
          Egress identity, peer-egress lending, per-vantage source health, and the cross-vantage diff. Collected data stays on
          the device.
        </p>
      </div>

      {/* device picker */}
      <div className={card}>
        <label className="mb-1 block text-xs text-white/50">Runtime (device)</label>
        <div className="flex flex-wrap items-center gap-2">
          <select value={deviceId} onChange={(e) => setDeviceId(e.target.value)} className={input}>
            <option value="">Select a device…</option>
            {devices.map((d) => (
              <option key={d.id} value={d.id}>
                {d.name || d.id}
              </option>
            ))}
          </select>
          <button className={btnNeutral} disabled={!deviceId || busy} onClick={refresh}>
            {busy ? "Refreshing…" : "Refresh"}
          </button>
        </div>
      </div>

      {err && <div className="rounded-lg border border-rose-500/40 bg-rose-500/10 px-3 py-2 text-sm text-rose-200">{err}</div>}
      {!err && msg && <p className="text-sm text-white/60">{msg}</p>}

      {/* egress identity */}
      {egress && (
        <div className={card}>
          <h2 className="mb-2 text-sm font-semibold">Egress identity</h2>
          <div className="grid grid-cols-2 gap-2 font-mono text-xs sm:grid-cols-4">
            <div>
              <div className="text-white/40">IP</div>
              <div>{egress.ip || "—"}</div>
            </div>
            <div>
              <div className="text-white/40">Geo</div>
              <div>{[egress.region, egress.country].filter(Boolean).join(" / ") || "—"}</div>
            </div>
            <div>
              <div className="text-white/40">ASN</div>
              <div>{egress.asn || "—"}</div>
            </div>
            <div>
              <div className="text-white/40">Stable</div>
              <div>{egress.stableKnown ? (egress.stable ? "yes" : "no") : "unknown"}</div>
            </div>
          </div>
        </div>
      )}

      {/* peer-egress lending policy */}
      {policy && (
        <div className={card}>
          <div className="flex items-center justify-between">
            <div>
              <h2 className="text-sm font-semibold">Egress lending</h2>
              <p className="text-xs text-white/50">
                Lend this box&apos;s IP to your own other devices. Opt-in, owner-only, never an open proxy. Default ports{" "}
                {(policy.allowedPorts && policy.allowedPorts.length ? policy.allowedPorts : [80, 443]).join("/")}.
              </p>
            </div>
            <button className={policy.enabled ? `${btn} bg-emerald-500/80 text-black` : btnNeutral} disabled={busy} onClick={toggleLending}>
              {policy.enabled ? "Enabled" : "Disabled"}
            </button>
          </div>
        </div>
      )}

      {/* cross-vantage compare */}
      <div className={card}>
        <h2 className="mb-2 text-sm font-semibold">Cross-vantage compare</h2>
        <div className="mb-3 flex flex-wrap items-center gap-2">
          <input className={input} placeholder="sourceId" value={sourceId} onChange={(e) => setSourceId(e.target.value)} />
          <input className={input} placeholder="dataset (optional)" value={dataset} onChange={(e) => setDataset(e.target.value)} />
          <button className={btnAccent} disabled={!deviceId || busy} onClick={doCompare}>
            Compare
          </button>
        </div>
        {compare?.vantages?.length ? (
          <div className="overflow-x-auto">
            <table className="w-full font-mono text-xs">
              <thead>
                <tr className="text-left text-white/40">
                  <th className="py-1 pr-3">vantage</th>
                  <th className="py-1 pr-3">egress</th>
                  <th className="py-1 pr-3">state</th>
                  {(compare.fields || []).map((f) => (
                    <th key={f} className="py-1 pr-3">
                      {f}
                    </th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {compare.vantages.map((v) => (
                  <tr key={v.vantageId} className="border-t border-white/5">
                    <td className="py-1 pr-3">{v.vantageId}</td>
                    <td className="py-1 pr-3 text-white/60">{[v.egressGeo, v.egressCountry, v.egressIp].filter(Boolean).join(" ") || "—"}</td>
                    <td className={`py-1 pr-3 ${stateColor(v.state)}`}>{v.state || "—"}</td>
                    {(compare.fields || []).map((f) => (
                      <td key={f} className="py-1 pr-3">
                        {v.values && v.values[f] !== undefined ? String(v.values[f]) : "—"}
                      </td>
                    ))}
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        ) : (
          <p className="text-xs text-white/40">No comparison loaded. Enter a sourceId and Compare.</p>
        )}
      </div>

      {/* per-vantage health */}
      <div className={card}>
        <h2 className="mb-2 text-sm font-semibold">Source health (per vantage)</h2>
        {health.length ? (
          <div className="overflow-x-auto">
            <table className="w-full font-mono text-xs">
              <thead>
                <tr className="text-left text-white/40">
                  <th className="py-1 pr-3">source</th>
                  <th className="py-1 pr-3">vantage</th>
                  <th className="py-1 pr-3">state</th>
                  <th className="py-1 pr-3">geo/ip/rate (24h)</th>
                </tr>
              </thead>
              <tbody>
                {health.map((h) => (
                  <tr key={`${h.sourceId}|${h.vantageId}`} className="border-t border-white/5">
                    <td className="py-1 pr-3">{h.sourceId}</td>
                    <td className="py-1 pr-3">{h.vantageId}</td>
                    <td className={`py-1 pr-3 ${stateColor(h.state)}`}>{h.state}</td>
                    <td className="py-1 pr-3 text-white/60">
                      {h.geoBlockCount24h ?? 0}/{h.ipBlockCount24h ?? 0}/{h.rateLimitCount24h ?? 0}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        ) : (
          <p className="text-xs text-white/40">No health rows yet.</p>
        )}
      </div>

      {/* blocks */}
      {blocked.length > 0 && (
        <div className={`${card} border-rose-500/20`}>
          <h2 className="mb-2 text-sm font-semibold text-rose-200">Blocked vantages</h2>
          <p className="mb-2 text-xs text-white/50">Recorded as findings — Yaver does not rotate IPs to route around a block.</p>
          <ul className="space-y-1 font-mono text-xs">
            {blocked.map((b) => (
              <li key={`${b.sourceId}|${b.vantageId}`} className={stateColor(b.state)}>
                {b.sourceId} · {b.vantageId} · {b.state}
              </li>
            ))}
          </ul>
        </div>
      )}
    </div>
  );
}
