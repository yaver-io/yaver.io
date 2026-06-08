"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { type Device } from "@/lib/use-devices";
import { AgentClient, agentClient } from "@/lib/agent-client";

// NetCaptureModal — the web surface for the wire-observe & deep-analysis layer.
// Starts a network (tcpdump) or serial (RS232/RS485) capture on the selected
// device, streams decoded events live over the existing SSE channel, and renders
// the structured deep-analysis (per-protocol stats, disconnect timeline, and the
// deterministic findings). Inline SVG icons only (repo policy — no icon libs).

type NcEvent = {
  type?: string;
  ts?: number;
  proto?: string;
  src?: string;
  dst?: string;
  summary?: string;
  severity?: string;
};

type Finding = { severity: string; code: string; title: string; detail?: string };
type Flow = {
  key: string;
  appProto?: string;
  packets: number;
  bytes: number;
  state: string;
  retransmits: number;
  rttMs?: number;
};
type Disconnect = { ts: number; flow: string; cause: string; note?: string };
type Analysis = {
  kind?: string;
  source?: string;
  status?: string;
  packets?: number;
  bytes?: number;
  protocols?: Record<string, number>;
  flows?: Flow[];
  disconnects?: Disconnect[];
  findings?: Finding[];
};

function sevColor(sev?: string): string {
  switch (sev) {
    case "error":
      return "text-rose-400";
    case "warn":
      return "text-amber-400";
    default:
      return "text-surface-400";
  }
}

export function NetCaptureModal({
  device,
  token,
  onClose,
}: {
  device: Device;
  token: string | null;
  onClose: () => void;
}) {
  const clientRef = useRef<AgentClient | null>(null);
  const stopStreamRef = useRef<(() => void) | null>(null);
  const [connState, setConnState] = useState<"connecting" | "ready" | "error">("connecting");
  const [connErr, setConnErr] = useState<string>("");

  const [kind, setKind] = useState<"net" | "serial">("net");
  const [iface, setIface] = useState("any");
  const [filter, setFilter] = useState("");
  const [serialDevice, setSerialDevice] = useState("");
  const [decoder, setDecoder] = useState("auto");

  const [session, setSession] = useState("");
  const [running, setRunning] = useState(false);
  const [events, setEvents] = useState<NcEvent[]>([]);
  const [analysis, setAnalysis] = useState<Analysis | null>(null);
  const [busy, setBusy] = useState(false);

  // Connector-box one-tap connect + self-test (box_* ops over the box SoftAP).
  const [boxBusy, setBoxBusy] = useState<"" | "connect" | "selftest">("");
  const [boxResult, setBoxResult] = useState<{ ok: boolean; text: string } | null>(null);
  const [boxControl, setBoxControl] = useState("");
  const [boxUnit, setBoxUnit] = useState("1");
  const [boxStart, setBoxStart] = useState("0");

  // Connect a dedicated client to the target device on mount.
  useEffect(() => {
    let cancelled = false;
    const client = new AgentClient();
    client.setRelayServers(agentClient.configuredRelayServers.map((r) => ({ ...r })));
    clientRef.current = client;
    (async () => {
      try {
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
        if (!token) throw new Error("not signed in");
        await client.connect(device.host, device.port, token, device.id, { tunnelUrls });
        if (!cancelled) setConnState("ready");
      } catch (e) {
        if (!cancelled) {
          setConnErr(e instanceof Error ? e.message : String(e));
          setConnState("error");
        }
      }
    })();
    return () => {
      cancelled = true;
      try {
        stopStreamRef.current?.();
      } catch {
        /* ignore */
      }
      try {
        client.disconnect();
      } catch {
        /* ignore */
      }
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const refreshAnalysis = useCallback(async () => {
    const client = clientRef.current;
    if (!client || !session) return;
    try {
      const res = await client.netcaptureAnalysis(session);
      if (res?.analysis) setAnalysis(res.analysis);
    } catch {
      /* ignore */
    }
  }, [session]);

  // Poll analysis while running so the findings/flows stay fresh.
  useEffect(() => {
    if (!running || !session) return;
    const t = setInterval(refreshAnalysis, 2500);
    return () => clearInterval(t);
  }, [running, session, refreshAnalysis]);

  const start = useCallback(async () => {
    const client = clientRef.current;
    if (!client || busy) return;
    setBusy(true);
    setEvents([]);
    setAnalysis(null);
    try {
      const res = await client.netcaptureStart(
        kind === "serial"
          ? { kind, device: serialDevice, decoder }
          : { kind, iface, filter },
      );
      if (!res?.session) throw new Error(res?.warning || "failed to start capture");
      setSession(res.session);
      setRunning(true);
      stopStreamRef.current = client.streamLog(res.stream, (ev: NcEvent) => {
        if (ev?.type !== "netcapture") return;
        setEvents((prev) => {
          const next = [...prev, ev];
          return next.length > 300 ? next.slice(next.length - 300) : next;
        });
      });
    } catch (e) {
      setConnErr(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  }, [busy, kind, iface, filter, serialDevice, decoder]);

  const boxAutoconnect = useCallback(async () => {
    const client = clientRef.current;
    if (!client || boxBusy) return;
    setBoxBusy("connect");
    setBoxResult(null);
    try {
      const r = await client.callOps("box_autoconnect", {
        control: boxControl || undefined,
        unit: Number(boxUnit) || 1,
        start: Number(boxStart) || 0,
      });
      const i = r?.initial;
      if (r?.ok && i) {
        setBoxResult({ ok: true, text: `Connected — A/B ${i.abSwap ? "swapped" : "normal"}, termination ${i.termination ? "on" : "off"}; read ${JSON.stringify(i.values)}` });
      } else {
        setBoxResult({ ok: false, text: r?.error || "no Modbus reply on any A/B × termination combo" });
      }
    } catch (e) {
      setBoxResult({ ok: false, text: e instanceof Error ? e.message : String(e) });
    } finally {
      setBoxBusy("");
    }
  }, [boxBusy, boxControl, boxUnit, boxStart]);

  const boxSelftest = useCallback(async () => {
    const client = clientRef.current;
    if (!client || boxBusy) return;
    setBoxBusy("selftest");
    setBoxResult(null);
    try {
      const r = await client.callOps("box_selftest", {
        control: boxControl || undefined,
        unit: Number(boxUnit) || 1,
      });
      const i = r?.initial;
      if (i?.checks) {
        const line = (i.checks as any[]).map((c) => `${c.name}:${c.result}`).join("  ");
        setBoxResult({ ok: !!r?.ok, text: `${i.summary || ""} — ${line}` });
      } else {
        setBoxResult({ ok: false, text: r?.error || "self-test unreachable" });
      }
    } catch (e) {
      setBoxResult({ ok: false, text: e instanceof Error ? e.message : String(e) });
    } finally {
      setBoxBusy("");
    }
  }, [boxBusy, boxControl, boxUnit]);

  const stop = useCallback(async () => {
    const client = clientRef.current;
    if (!client || !session) return;
    setBusy(true);
    try {
      stopStreamRef.current?.();
      stopStreamRef.current = null;
      const res = await client.netcaptureStop(session);
      if (res?.analysis) setAnalysis(res.analysis);
      setRunning(false);
    } catch {
      /* ignore */
    } finally {
      setBusy(false);
    }
  }, [session]);

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 p-4 backdrop-blur-sm"
      onClick={onClose}
    >
      <div
        className="flex max-h-[88vh] w-full max-w-3xl flex-col overflow-hidden rounded-xl border border-surface-800 bg-surface-950 shadow-2xl"
        onClick={(e) => e.stopPropagation()}
      >
        {/* header */}
        <div className="flex items-center justify-between border-b border-surface-800 px-4 py-3">
          <div>
            <div className="text-sm font-semibold text-surface-100">Network / Wire Monitor</div>
            <div className="text-[11px] text-surface-500">{device.name || device.id} · deep packet + serial analysis</div>
          </div>
          <button onClick={onClose} className="rounded-md p-1 text-surface-400 hover:bg-surface-800 hover:text-surface-100">
            <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round">
              <path d="M6 6l12 12M18 6L6 18" />
            </svg>
          </button>
        </div>

        {connState === "error" ? (
          <div className="p-4 text-sm text-rose-400">Couldn’t reach the agent: {connErr}</div>
        ) : connState === "connecting" ? (
          <div className="p-4 text-sm text-surface-400">Connecting to {device.name || device.id}…</div>
        ) : (
          <div className="flex min-h-0 flex-1 flex-col">
            {/* controls */}
            <div className="flex flex-wrap items-end gap-2 border-b border-surface-800 px-4 py-3">
              <label className="flex flex-col gap-1 text-[10px] uppercase tracking-widest text-surface-500">
                Source
                <select
                  value={kind}
                  onChange={(e) => setKind(e.target.value as "net" | "serial")}
                  disabled={running}
                  className="rounded-md border border-surface-700 bg-surface-900 px-2 py-1 text-xs text-surface-100"
                >
                  <option value="net">Network (tcpdump)</option>
                  <option value="serial">Serial (RS232/RS485)</option>
                </select>
              </label>

              {kind === "net" ? (
                <>
                  <label className="flex flex-col gap-1 text-[10px] uppercase tracking-widest text-surface-500">
                    Interface
                    <input
                      value={iface}
                      onChange={(e) => setIface(e.target.value)}
                      disabled={running}
                      placeholder="any / eth0 / en0"
                      className="w-28 rounded-md border border-surface-700 bg-surface-900 px-2 py-1 text-xs text-surface-100"
                    />
                  </label>
                  <label className="flex flex-1 flex-col gap-1 text-[10px] uppercase tracking-widest text-surface-500">
                    BPF filter
                    <input
                      value={filter}
                      onChange={(e) => setFilter(e.target.value)}
                      disabled={running}
                      placeholder="tcp port 502  ·  host 10.0.0.50"
                      className="w-full rounded-md border border-surface-700 bg-surface-900 px-2 py-1 text-xs text-surface-100"
                    />
                  </label>
                </>
              ) : (
                <>
                  <label className="flex flex-col gap-1 text-[10px] uppercase tracking-widest text-surface-500">
                    Device
                    <input
                      value={serialDevice}
                      onChange={(e) => setSerialDevice(e.target.value)}
                      disabled={running}
                      placeholder="/dev/ttyUSB0 (blank = fed)"
                      className="w-44 rounded-md border border-surface-700 bg-surface-900 px-2 py-1 text-xs text-surface-100"
                    />
                  </label>
                  <label className="flex flex-col gap-1 text-[10px] uppercase tracking-widest text-surface-500">
                    Decoder
                    <select
                      value={decoder}
                      onChange={(e) => setDecoder(e.target.value)}
                      disabled={running}
                      className="rounded-md border border-surface-700 bg-surface-900 px-2 py-1 text-xs text-surface-100"
                    >
                      <option value="auto">auto (Modbus-RTU)</option>
                      <option value="modbus_rtu">modbus_rtu</option>
                      <option value="marlin">marlin (G-code)</option>
                      <option value="ascii">ascii</option>
                    </select>
                  </label>
                </>
              )}

              {running ? (
                <button
                  onClick={stop}
                  disabled={busy}
                  className="rounded-md border border-rose-500/40 bg-rose-500/10 px-3 py-1.5 text-xs font-semibold text-rose-700 dark:text-rose-200 hover:border-rose-400 disabled:opacity-50"
                >
                  Stop
                </button>
              ) : (
                <button
                  onClick={start}
                  disabled={busy}
                  className="rounded-md border border-emerald-500/40 bg-emerald-500/10 px-3 py-1.5 text-xs font-semibold text-emerald-700 dark:text-emerald-200 hover:border-emerald-400 disabled:opacity-50"
                >
                  {busy ? "Starting…" : "Start capture"}
                </button>
              )}
            </div>

            {/* connector-box one-tap connect */}
            <div className="flex flex-wrap items-center gap-2 border-b border-surface-800 bg-surface-900/30 px-4 py-2">
              <span className="text-[10px] font-semibold uppercase tracking-widest text-surface-500">Machine connect</span>
              <input
                value={boxControl}
                onChange={(e) => setBoxControl(e.target.value)}
                placeholder="box 192.168.4.1:8347"
                className="w-40 rounded-md border border-surface-700 bg-surface-900 px-2 py-1 text-[11px] text-surface-100"
              />
              <input value={boxUnit} onChange={(e) => setBoxUnit(e.target.value)} placeholder="unit" className="w-12 rounded-md border border-surface-700 bg-surface-900 px-2 py-1 text-[11px] text-surface-100" />
              <input value={boxStart} onChange={(e) => setBoxStart(e.target.value)} placeholder="reg" className="w-12 rounded-md border border-surface-700 bg-surface-900 px-2 py-1 text-[11px] text-surface-100" />
              <button
                onClick={boxAutoconnect}
                disabled={boxBusy !== ""}
                className="rounded-md border border-sky-500/40 bg-sky-500/10 px-2.5 py-1 text-[11px] font-semibold text-sky-700 dark:text-sky-200 hover:border-sky-400 disabled:opacity-50"
                title="Auto-resolve RS485 A/B polarity + termination and verify with a real Modbus read through the box."
              >
                {boxBusy === "connect" ? "Connecting…" : "Connect to machine"}
              </button>
              <button
                onClick={boxSelftest}
                disabled={boxBusy !== ""}
                className="rounded-md border border-surface-700 px-2.5 py-1 text-[11px] font-semibold text-surface-300 hover:border-surface-500 disabled:opacity-50"
              >
                {boxBusy === "selftest" ? "Testing…" : "Self-test"}
              </button>
              {boxResult && (
                <span className={`text-[11px] ${boxResult.ok ? "text-emerald-400" : "text-rose-400"}`}>
                  {boxResult.ok ? "✓ " : "✗ "}{boxResult.text}
                </span>
              )}
            </div>

            {/* body: live feed + analysis */}
            <div className="grid min-h-0 flex-1 grid-cols-1 gap-0 overflow-hidden md:grid-cols-2">
              {/* live feed */}
              <div className="flex min-h-0 flex-col border-b border-surface-800 md:border-b-0 md:border-r">
                <div className="px-4 py-2 text-[10px] font-semibold uppercase tracking-widest text-surface-500">
                  Live ({events.length})
                </div>
                <div className="min-h-0 flex-1 overflow-auto px-4 pb-3 font-mono text-[11px] leading-relaxed">
                  {events.length === 0 ? (
                    <div className="text-surface-600">No frames yet. Start a capture to watch the wire.</div>
                  ) : (
                    events.map((e, i) => (
                      <div key={i} className={sevColor(e.severity)}>
                        <span className="text-surface-600">{e.proto}</span> {e.summary}
                      </div>
                    ))
                  )}
                </div>
              </div>

              {/* analysis */}
              <div className="flex min-h-0 flex-col">
                <div className="flex items-center justify-between px-4 py-2">
                  <div className="text-[10px] font-semibold uppercase tracking-widest text-surface-500">Deep analysis</div>
                  <button onClick={refreshAnalysis} className="text-[10px] text-surface-400 hover:text-surface-100">
                    refresh
                  </button>
                </div>
                <div className="min-h-0 flex-1 overflow-auto px-4 pb-4 text-xs">
                  {!analysis ? (
                    <div className="text-surface-600">Analysis appears here once packets/frames arrive.</div>
                  ) : (
                    <div className="space-y-3">
                      <div className="text-surface-400">
                        {analysis.packets ?? 0} pkts · {analysis.bytes ?? 0} B ·{" "}
                        {Object.entries(analysis.protocols || {})
                          .map(([k, v]) => `${k}:${v}`)
                          .join("  ")}
                      </div>

                      {(analysis.findings || []).length > 0 && (
                        <div className="space-y-1.5">
                          {(analysis.findings || []).map((f, i) => (
                            <div key={i} className="rounded-md border border-surface-800 bg-surface-900/50 p-2">
                              <div className={`font-semibold ${sevColor(f.severity)}`}>{f.title}</div>
                              {f.detail && <div className="mt-0.5 text-[11px] text-surface-400">{f.detail}</div>}
                            </div>
                          ))}
                        </div>
                      )}

                      {(analysis.disconnects || []).length > 0 && (
                        <div>
                          <div className="mb-1 text-[10px] uppercase tracking-widest text-surface-500">Disconnect timeline</div>
                          {(analysis.disconnects || []).slice(-8).map((d, i) => (
                            <div key={i} className="text-[11px] text-amber-700 dark:text-amber-300">
                              {d.cause} · <span className="text-surface-500">{d.flow}</span>
                              {d.note ? ` (${d.note})` : ""}
                            </div>
                          ))}
                        </div>
                      )}

                      {(analysis.flows || []).length > 0 && (
                        <div>
                          <div className="mb-1 text-[10px] uppercase tracking-widest text-surface-500">Top flows</div>
                          {(analysis.flows || []).slice(0, 8).map((fl, i) => (
                            <div key={i} className="flex justify-between gap-2 text-[11px] text-surface-300">
                              <span className="truncate">
                                {fl.appProto ? `[${fl.appProto}] ` : ""}
                                {fl.key}
                              </span>
                              <span className="shrink-0 text-surface-500">
                                {fl.packets}p {fl.state}
                                {fl.retransmits > 0 ? ` ↻${fl.retransmits}` : ""}
                                {fl.rttMs ? ` ${fl.rttMs}ms` : ""}
                              </span>
                            </div>
                          ))}
                        </div>
                      )}
                    </div>
                  )}
                </div>
              </div>
            </div>
          </div>
        )}
      </div>
    </div>
  );
}
