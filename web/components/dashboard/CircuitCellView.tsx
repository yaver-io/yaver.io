"use client";

// CircuitCellView — drive Yaver's electrical-circuit cell from the web dashboard
// over the relay. Import a SPICE netlist, a KiCad export, or an EPLAN/harness
// connection list; simulate it with the dependency-free built-in MNA engine (or
// an installed ngspice); run a generic ERC; and view the waveform PNG the host
// coding agent also sees via the circuit_plot MCP tool. Web is relay-only by
// design. All circuit_* verbs are the same the mobile cell calls.
import { useCallback, useEffect, useRef, useState } from "react";
import { AgentClient, agentClient } from "@/lib/agent-client";
import type { Device } from "@/lib/use-devices";
import WaveformChart from "@/components/dashboard/WaveformChart";

type Net = { name: string; connCount: number; domainV?: number; isGround?: boolean };
type ElementInfo = { name: string; kind: string; nodes: string[]; value?: number; display?: string };
type CircuitInfo = {
  title?: string;
  nets?: Net[];
  elements?: ElementInfo[];
  nodeCount?: number;
  elementCount?: number;
  sources?: string[];
  hasGround?: boolean;
  simulatable?: boolean;
  source?: string;
};
type EngineCap = { engine: string; available: boolean; analyses: string[]; elements: string[]; nonlinear?: boolean; note?: string };
type ERCFinding = { rule: string; severity: string; net?: string; element?: string; message: string };
type ERCReport = { findings?: ERCFinding[]; errors: number; warnings: number; ok: boolean };
type SimResult = {
  analysis: string;
  signals: string[];
  samples: number[][];
  nodeVoltages?: Record<string, number>;
  branchCurrents?: Record<string, number>;
  engine: string;
};
type CircuitDesignSummary = { design: string; title?: string; elements?: number; simulatable?: boolean; engine?: string; updatedAt?: number };

const ANALYSES = ["op", "tran", "ac", "dc"] as const;
const EXAMPLE = `* RC low-pass example
V1 in 0 DC 0 AC 1 SIN(0 5 1k)
R1 in out 1k
C1 out 0 100n
.end`;

export default function CircuitCellView({ devices, token }: { devices: Device[]; token: string | null }) {
  const [deviceId, setDeviceId] = useState("");
  const [design, setDesign] = useState(""); // "" = default slot
  const [designs, setDesigns] = useState<CircuitDesignSummary[]>([]);
  const [newDesign, setNewDesign] = useState("");
  const [engines, setEngines] = useState<EngineCap[]>([]);
  const [engine, setEngine] = useState("auto");
  const [info, setInfo] = useState<CircuitInfo | null>(null);
  const [netlist, setNetlist] = useState(EXAMPLE);
  const [format, setFormat] = useState("auto");
  const [analysis, setAnalysis] = useState<(typeof ANALYSES)[number]>("tran");
  const [tstop, setTstop] = useState("5m");
  const [fstart, setFstart] = useState("1");
  const [fstop, setFstop] = useState("100k");
  const [sweepSrc, setSweepSrc] = useState("V1");
  const [sweep, setSweep] = useState({ start: "0", stop: "5", step: "0.1" });
  const [sim, setSim] = useState<SimResult | null>(null);
  const [erc, setErc] = useState<ERCReport | null>(null);
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState<string | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [showInspect, setShowInspect] = useState(false);
  const lastImported = useRef<string>("");

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
        // Inject the active design slot (S-2) unless the caller set one explicitly.
        const body = design && (payload as any).design === undefined ? { ...payload, design } : payload;
        const res = await client.callOps(verb, body);
        if (res?.ok === false) return { ok: false, code: res.code, error: res.error };
        return (res as any)?.initial ?? res;
      } catch (e: any) {
        setMsg(e?.message || "connection failed");
        return { ok: false, error: e?.message || "failed" };
      }
    },
    [deviceId, ensureClient, design],
  );

  // load engines + design list + current config on device/design change
  useEffect(() => {
    if (!deviceId) return;
    (async () => {
      const e = await call("circuit_engines");
      if (e?.engines) setEngines(e.engines);
      if (e?.active) setEngine(e.active);
      const ds = await call("circuit_designs");
      if (ds?.designs) setDesigns(ds.designs);
      const cfg = await call("circuit_config_get");
      if (cfg?.engine) setEngine(cfg.engine);
      setInfo(cfg?.info ?? null);
      setSim(null);
      setErc(null);
      const ex = await call("circuit_export", { format: "spice" });
      if (ex?.spice && ex.spice.trim() && (ex.info?.elementCount ?? cfg?.info?.elementCount ?? 0) > 0) {
        setNetlist(ex.spice);
        lastImported.current = ex.spice; // device already has this loaded
      } else {
        lastImported.current = "";
      }
    })();
  }, [deviceId, design]); // eslint-disable-line

  const pickDesign = useCallback((id: string) => {
    setNetlist("");
    lastImported.current = "";
    setInfo(null);
    setDesign(id);
  }, []);

  const createDesign = useCallback(() => {
    const id = newDesign.trim().toLowerCase();
    if (!id) return;
    setNewDesign("");
    setDesigns((d) => (d.some((x) => x.design === id) ? d : [...d, { design: id }]));
    pickDesign(id);
  }, [newDesign, pickDesign]);

  const analysisPayload = useCallback(() => {
    if (analysis === "tran") return { type: "tran", tstop: parseEng(tstop) };
    if (analysis === "ac") return { type: "ac", fstart: parseEng(fstart), fstop: parseEng(fstop), points: 30 };
    if (analysis === "dc")
      return { type: "dc", sweepSrc, sweepStart: parseEng(sweep.start), sweepStop: parseEng(sweep.stop), sweepStep: parseEng(sweep.step) };
    return { type: "op" };
  }, [analysis, tstop, fstart, fstop, sweepSrc, sweep]);

  // syncNetlist pushes the editor's current text to the device if it differs
  // from what was last loaded, so Run/ERC always reflect what's on screen.
  // Returns the imported CircuitInfo, or null on parse error (msg already set).
  const syncNetlist = useCallback(async (): Promise<CircuitInfo | null> => {
    if (netlist === lastImported.current && info) return info;
    const r = await call("circuit_import", { format, text: netlist });
    if (r?.ok === false) {
      setErr(r.error || "import failed");
      return null;
    }
    if (r?.info) {
      setInfo(r.info);
      lastImported.current = netlist;
      return r.info;
    }
    return null;
  }, [call, format, netlist, info]);

  const doImport = useCallback(async () => {
    setBusy(true);
    setMsg(null);
    setErr(null);
    lastImported.current = ""; // force re-import
    const i = await syncNetlist();
    setBusy(false);
    if (i) setMsg(`Imported ${i.elementCount} elements, ${i.nodeCount} nets`);
  }, [syncNetlist]);

  const doSimulate = useCallback(async () => {
    setBusy(true);
    setMsg(null);
    setErr(null);
    const i = await syncNetlist();
    if (!i) return setBusy(false);
    if (!i.hasGround) {
      setBusy(false);
      return setErr("No ground node (0). Add a connection to node 0 — the circuit has no voltage reference.");
    }
    if (analysis !== "op" && !i.simulatable) {
      setBusy(false);
      return setErr("This is a connection list (KiCad multi-pin / EPLAN). Run ERC instead of a simulation.");
    }
    if (analysis === "op") {
      const r = await call("circuit_measure");
      setBusy(false);
      if (r?.ok === false) return setErr(r.error || "sim failed");
      setSim({ analysis: "op", signals: [], samples: [], nodeVoltages: r.nodeVoltages, branchCurrents: r.branchCurrents, engine: r.engine });
      setMsg(`operating point · ${r.engine}`);
      return;
    }
    const r = await call("circuit_simulate", analysisPayload());
    setBusy(false);
    if (r?.ok === false) return setErr(r.error || "sim failed");
    if (r?.result) {
      setSim(r.result);
      setMsg(`${r.result.samples?.length ?? 0} samples · ${r.result.engine}`);
    }
  }, [call, syncNetlist, analysis, analysisPayload]);

  const doErc = useCallback(async () => {
    setBusy(true);
    setMsg(null);
    setErr(null);
    const i = await syncNetlist();
    if (!i) return setBusy(false);
    const r = await call("circuit_erc");
    setBusy(false);
    if (r?.report) setErc(r.report);
  }, [call, syncNetlist]);

  const setEngineCfg = useCallback(
    async (eng: string) => {
      setEngine(eng);
      await call("circuit_config_set", { engine: eng });
    },
    [call],
  );

  const card = "rounded-xl border border-white/10 bg-white/[0.03] p-4";
  const btn = "rounded-lg px-3 py-1.5 text-sm font-medium transition disabled:opacity-40";

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between gap-3">
        <h2 className="text-lg font-semibold">⚡ Circuit Simulator</h2>
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

      {!deviceId && <p className="text-sm text-white/50">Select a device running the Yaver agent to design & simulate circuits.</p>}

      {deviceId && (
        <>
          <div className={card}>
            <div className="mb-2 flex flex-wrap items-center gap-2">
              <span className="text-sm font-medium">Design</span>
              {[{ design: "" } as CircuitDesignSummary, ...designs.filter((x) => x.design && x.design !== "default")].map((dg) => {
                const sel = design === dg.design;
                const labelTxt = dg.design === "" ? "default" : dg.design;
                return (
                  <button
                    key={dg.design || "default"}
                    onClick={() => pickDesign(dg.design)}
                    className={`${btn} ${sel ? "bg-sky-500/80 text-black" : "bg-white/5 hover:bg-white/10"}`}
                  >
                    {labelTxt}
                    {typeof dg.elements === "number" && dg.elements > 0 ? ` · ${dg.elements}` : ""}
                  </button>
                );
              })}
              {design !== "" && (
                <button
                  onClick={async () => {
                    const id = design;
                    const r = await call("circuit_design_delete", { design: id });
                    if (r?.ok !== false) {
                      setDesigns((d) => d.filter((x) => x.design !== id));
                      pickDesign("");
                    }
                  }}
                  className={`${btn} bg-rose-500/20 text-rose-300 hover:bg-rose-500/30`}
                  title="Delete this design slot"
                >
                  ✕ delete
                </button>
              )}
            </div>
            <div className="flex items-center gap-2">
              <input
                value={newDesign}
                onChange={(e) => setNewDesign(e.target.value)}
                onKeyDown={(e) => e.key === "Enter" && createDesign()}
                placeholder="new design id (e.g. panel-1, gridpilot-rev-c)"
                className="flex-1 rounded-lg border border-white/10 bg-black/30 px-2 py-1.5 text-sm"
              />
              <button onClick={createDesign} className={`${btn} bg-sky-600/80 text-white hover:bg-sky-600`}>＋ New</button>
            </div>
            <p className="mt-2 text-xs text-white/40">One sim node holds many designs — Talos panels, OCPP board revs… each persisted on the box, never on Convex.</p>
          </div>

          <div className={card}>
            <div className="mb-2 flex flex-wrap items-center gap-2">
              <span className="text-sm font-medium">Engine</span>
              {engines.map((e) => (
                <button
                  key={e.engine}
                  disabled={!e.available}
                  onClick={() => setEngineCfg(e.engine)}
                  title={e.note}
                  className={`${btn} ${engine === e.engine ? "bg-emerald-500/80 text-black" : "bg-white/5 hover:bg-white/10"} ${!e.available ? "line-through" : ""}`}
                >
                  {e.engine}
                </button>
              ))}
              <button onClick={() => setEngineCfg("auto")} className={`${btn} ${engine === "auto" ? "bg-emerald-500/80 text-black" : "bg-white/5 hover:bg-white/10"}`}>
                auto
              </button>
            </div>
            <p className="text-xs text-white/40">{engines.find((e) => e.engine === (engine === "auto" ? "builtin" : engine))?.note || "auto → ngspice if installed, else the built-in dependency-free solver"}</p>
          </div>

          <div className={card}>
            <div className="mb-2 flex items-center justify-between">
              <span className="text-sm font-medium">Netlist</span>
              <div className="flex items-center gap-2">
                <select value={format} onChange={(e) => setFormat(e.target.value)} className="rounded border border-white/10 bg-black/30 px-2 py-1 text-xs">
                  <option value="auto">auto-detect</option>
                  <option value="spice">SPICE</option>
                  <option value="kicad">KiCad</option>
                  <option value="eplan">EPLAN / wirelist</option>
                </select>
                <button onClick={doImport} disabled={busy} className={`${btn} bg-sky-500/80 text-black hover:bg-sky-400`}>
                  Import
                </button>
              </div>
            </div>
            <textarea
              value={netlist}
              onChange={(e) => setNetlist(e.target.value)}
              spellCheck={false}
              rows={8}
              className="w-full rounded-lg border border-white/10 bg-black/40 p-2 font-mono text-xs text-emerald-200"
            />
            {info && (
              <p className="mt-2 text-xs text-white/50">
                {info.title || "(untitled)"} · {info.elementCount} elements · {info.nodeCount} nets · {info.hasGround ? "grounded" : "NO GROUND"} ·{" "}
                {info.simulatable ? "simulatable" : "connection-list (ERC only)"} · src: {info.source}
              </p>
            )}
          </div>

          <div className={card}>
            <div className="flex flex-wrap items-center gap-2">
              {ANALYSES.map((a) => (
                <button key={a} onClick={() => setAnalysis(a)} className={`${btn} ${analysis === a ? "bg-indigo-500/80 text-black" : "bg-white/5 hover:bg-white/10"}`}>
                  {a.toUpperCase()}
                </button>
              ))}
              <div className="ml-2 flex items-center gap-2 text-xs text-white/60">
                {analysis === "tran" && (
                  <label className="flex items-center gap-1">
                    stop <input value={tstop} onChange={(e) => setTstop(e.target.value)} className="w-16 rounded bg-black/40 px-1 py-0.5" />s
                  </label>
                )}
                {analysis === "ac" && (
                  <>
                    <label className="flex items-center gap-1">
                      f <input value={fstart} onChange={(e) => setFstart(e.target.value)} className="w-14 rounded bg-black/40 px-1 py-0.5" />→
                      <input value={fstop} onChange={(e) => setFstop(e.target.value)} className="w-16 rounded bg-black/40 px-1 py-0.5" />Hz
                    </label>
                  </>
                )}
                {analysis === "dc" && (
                  <>
                    <input value={sweepSrc} onChange={(e) => setSweepSrc(e.target.value)} className="w-14 rounded bg-black/40 px-1 py-0.5" />
                    <input value={sweep.start} onChange={(e) => setSweep({ ...sweep, start: e.target.value })} className="w-12 rounded bg-black/40 px-1 py-0.5" />→
                    <input value={sweep.stop} onChange={(e) => setSweep({ ...sweep, stop: e.target.value })} className="w-12 rounded bg-black/40 px-1 py-0.5" />/
                    <input value={sweep.step} onChange={(e) => setSweep({ ...sweep, step: e.target.value })} className="w-12 rounded bg-black/40 px-1 py-0.5" />
                  </>
                )}
              </div>
              <div className="ml-auto flex gap-2">
                <button onClick={doSimulate} disabled={busy} className={`${btn} bg-emerald-500/80 text-black hover:bg-emerald-400`}>
                  {busy ? "Running…" : "▶ Run"}
                </button>
                <button onClick={doErc} disabled={busy} className={`${btn} bg-amber-500/80 text-black hover:bg-amber-400`}>
                  ERC
                </button>
              </div>
            </div>
          </div>

          {err && (
            <div className="rounded-lg border border-red-500/40 bg-red-500/10 px-3 py-2 text-sm text-red-200">{err}</div>
          )}
          {!err && msg && <p className="text-sm text-white/60">{msg}</p>}

          {sim && sim.samples?.length > 0 && (
            <div className={card}>
              <div className="mb-2 flex items-center justify-between">
                <span className="text-sm font-medium">
                  {sim.analysis.toUpperCase()} {sim.analysis === "ac" ? "(Bode magnitude)" : ""}
                </span>
                <span className="text-xs text-white/40">
                  {sim.samples.length} pts · {sim.engine}
                </span>
              </div>
              <WaveformChart result={sim} />
              {(() => {
                const stats = signalStats(sim);
                if (stats.length === 0) return null;
                const unit = sim.analysis === "ac" ? "dB" : "V";
                return (
                  <div className="mt-3 overflow-x-auto">
                    <table className="w-full font-mono text-xs">
                      <thead className="text-white/40">
                        <tr className="text-left">
                          <th className="pb-1 font-normal">signal</th>
                          <th className="pb-1 text-right font-normal">min ({unit})</th>
                          <th className="pb-1 text-right font-normal">max ({unit})</th>
                          <th className="pb-1 text-right font-normal">{sim.analysis === "tran" ? "final" : "last"}</th>
                          <th className="pb-1 text-right font-normal">pk-pk</th>
                        </tr>
                      </thead>
                      <tbody>
                        {stats.map((s) => (
                          <tr key={s.name} className="text-white/80">
                            <td className="text-sky-300">{s.name}</td>
                            <td className="text-right">{fmt(s.min)}</td>
                            <td className="text-right">{fmt(s.max)}</td>
                            <td className="text-right">{fmt(s.last)}</td>
                            <td className="text-right text-white/50">{fmt(s.max - s.min)}</td>
                          </tr>
                        ))}
                      </tbody>
                    </table>
                  </div>
                );
              })()}
            </div>
          )}

          {sim?.nodeVoltages && (
            <div className={card}>
              <p className="mb-2 text-sm font-medium">Operating point ({sim.engine})</p>
              <div className="grid grid-cols-2 gap-x-6 gap-y-1 font-mono text-xs sm:grid-cols-3">
                {Object.entries(sim.nodeVoltages).map(([n, v]) => (
                  <div key={n} className="flex justify-between">
                    <span className="text-white/50">V({n})</span>
                    <span className="text-emerald-200">{fmt(v)} V</span>
                  </div>
                ))}
              </div>
              {sim.branchCurrents && Object.keys(sim.branchCurrents).length > 0 && (
                <div className="mt-2 grid grid-cols-2 gap-x-6 gap-y-1 border-t border-white/10 pt-2 font-mono text-xs sm:grid-cols-3">
                  {Object.entries(sim.branchCurrents).map(([n, i]) => (
                    <div key={n} className="flex justify-between">
                      <span className="text-white/50">I({n})</span>
                      <span className="text-amber-200">{fmt(i)} A</span>
                    </div>
                  ))}
                </div>
              )}
            </div>
          )}

          {info && (info.elements?.length ?? 0) > 0 && (
            <div className={card}>
              <button onClick={() => setShowInspect((v) => !v)} className="flex w-full items-center justify-between text-sm font-medium">
                <span>Inspector — {info.elementCount} elements, {info.nodeCount} nets</span>
                <span className="text-white/40">{showInspect ? "▾" : "▸"}</span>
              </button>
              {showInspect && (
                <div className="mt-3 grid gap-4 sm:grid-cols-2">
                  <div>
                    <p className="mb-1 text-xs uppercase tracking-wide text-white/40">Elements</p>
                    <div className="space-y-0.5 font-mono text-xs">
                      {(info.elements || []).map((el) => (
                        <div key={el.name} className="flex items-center gap-2">
                          <span className="w-12 text-emerald-300">{el.name}</span>
                          <span className="text-white/40">{el.nodes.join("–")}</span>
                          <span className="ml-auto text-white/70">{el.display || ""}</span>
                        </div>
                      ))}
                    </div>
                  </div>
                  <div>
                    <p className="mb-1 text-xs uppercase tracking-wide text-white/40">Nets</p>
                    <div className="space-y-0.5 font-mono text-xs">
                      {(info.nets || []).map((n) => (
                        <div key={n.name} className="flex items-center gap-2">
                          <span className={n.isGround ? "text-white/40" : "text-sky-300"}>{n.name}</span>
                          <span className="text-white/30">×{n.connCount}</span>
                          {n.domainV ? <span className="ml-auto text-amber-300">{n.domainV}V</span> : null}
                        </div>
                      ))}
                    </div>
                  </div>
                </div>
              )}
            </div>
          )}

          {erc && (
            <div className={card}>
              <p className="mb-2 text-sm font-medium">
                ERC — {erc.ok ? "✅ pass" : `❌ ${erc.errors} errors`}
                {erc.warnings > 0 ? `, ${erc.warnings} warnings` : ""}
              </p>
              <ul className="space-y-1 text-xs">
                {(erc.findings || []).map((f, i) => (
                  <li key={i} className="flex gap-2">
                    <span className={sevColor(f.severity)}>{sevIcon(f.severity)}</span>
                    <span className="text-white/40">[{f.rule}]</span>
                    <span className="text-white/80">{f.message}</span>
                  </li>
                ))}
                {(erc.findings || []).length === 0 && <li className="text-white/40">No findings.</li>}
              </ul>
            </div>
          )}
        </>
      )}
    </div>
  );
}

function signalStats(sim: SimResult): { name: string; min: number; max: number; last: number }[] {
  const out: { name: string; min: number; max: number; last: number }[] = [];
  for (let c = 1; c < sim.signals.length; c++) {
    const name = sim.signals[c];
    if (sim.analysis === "ac" && name.endsWith("deg")) continue;
    let min = Infinity,
      max = -Infinity,
      last = NaN;
    for (const row of sim.samples) {
      const v = row[c];
      if (!Number.isFinite(v)) continue;
      min = Math.min(min, v);
      max = Math.max(max, v);
      last = v;
    }
    if (Number.isFinite(min)) out.push({ name, min, max, last });
  }
  return out;
}

function sevColor(s: string) {
  return s === "error" ? "text-red-400" : s === "warning" ? "text-amber-400" : "text-sky-400";
}
function sevIcon(s: string) {
  return s === "error" ? "✖" : s === "warning" ? "▲" : "ℹ";
}
function fmt(v: number) {
  if (Math.abs(v) >= 1000 || (Math.abs(v) < 1e-3 && v !== 0)) return v.toExponential(3);
  return v.toFixed(4);
}
// parseEng turns "5m" / "100k" / "1.5" into a number (SI suffixes).
function parseEng(s: string): number {
  const m = String(s).trim().match(/^([-+]?[0-9.]+(?:e[-+]?[0-9]+)?)\s*([a-zµ]*)$/i);
  if (!m) return Number(s) || 0;
  const base = parseFloat(m[1]);
  const suf = m[2].toLowerCase();
  const mult: Record<string, number> = { t: 1e12, g: 1e9, meg: 1e6, k: 1e3, "": 1, m: 1e-3, u: 1e-6, µ: 1e-6, n: 1e-9, p: 1e-12, f: 1e-15 };
  return base * (suf === "meg" ? 1e6 : mult[suf[0] || ""] ?? 1);
}
