"use client";

// PrinterCellView — drive a 3D printer cell (Bambu Lab P1/P1S/A1/X1) from the web
// dashboard over the relay. Discover (SSDP, no credentials), live status (temps /
// progress / stage), the chamber camera, and control (light, pause/resume/stop,
// set temps). Remote CAD closes the loop — write OpenSCAD on the box, render it,
// SEE the PNG preview here, then slice → upload → print. Web is relay-only by
// design. Destructive actions are confirm-gated, matching the agent interlock.
import { useCallback, useEffect, useRef, useState } from "react";
import { AgentClient, agentClient } from "@/lib/agent-client";
import type { Device } from "@/lib/use-devices";

type TempPair = { cur: number; target: number };
type PrinterStatus = {
  online: boolean;
  state: string;
  stage?: string;
  nozzle: TempPair;
  bed: TempPair;
  chamber?: TempPair;
  progress: number;
  layerNum?: number;
  totalLayers?: number;
  remainingMin?: number;
  subtaskName?: string;
  lightOn?: boolean | null;
  errors?: string[];
};
type PrinterConfig = { driver?: string; addr?: string; serial?: string; model?: string; name?: string; accessCode?: string };
type Discovered = { ip: string; serial: string; model: string; firmware?: string; signalDb?: number; bind?: string };

const DEMO_SCAD = `// Yaver remote CAD demo — a parametric box
w = 30; d = 20; h = 15; wall = 2;
difference() {
  cube([w, d, h], center=true);
  translate([0,0,wall]) cube([w-2*wall, d-2*wall, h], center=true);
}`;

export default function PrinterCellView({ devices, token }: { devices: Device[]; token: string | null }) {
  const [deviceId, setDeviceId] = useState("");
  const [config, setConfig] = useState<PrinterConfig | null>(null);
  const [enabled, setEnabled] = useState(false);
  const [status, setStatus] = useState<PrinterStatus | null>(null);
  const [frame, setFrame] = useState<string | null>(null);
  const [found, setFound] = useState<Discovered[]>([]);
  const [addr, setAddr] = useState("");
  const [serial, setSerial] = useState("");
  const [model, setModel] = useState("");
  const [code, setCode] = useState("");
  const [scad, setScad] = useState(DEMO_SCAD);
  const [preview, setPreview] = useState<string | null>(null);
  const [stlPath, setStlPath] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState<string | null>(null);
  const [showSetup, setShowSetup] = useState(false);

  const clientRef = useRef<AgentClient | null>(null);
  const connectedTo = useRef("");
  const liveRef = useRef(true);

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
      const tunnelUrls = Array.from(new Set([...(Array.isArray(device.publicEndpoints) ? device.publicEndpoints : []), ...(device.tunnelUrl ? [device.tunnelUrl] : [])]));
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

  const loadConfig = useCallback(async () => {
    if (!deviceId) return;
    const r = await call("printer_config_get");
    if (r?.config) {
      setConfig(r.config);
      setEnabled(!!r.enabled);
      setAddr(r.config.addr || "");
      setSerial(r.config.serial || "");
      setModel(r.config.model || "");
      if (!r.enabled) setShowSetup(true);
    }
  }, [deviceId, call]);

  const refresh = useCallback(async () => {
    if (!deviceId || !enabled) return;
    const s = await call("printer_status");
    if (s?.status && liveRef.current) setStatus(s.status);
    const f = await call("printer_snapshot");
    if (f?.image && liveRef.current) setFrame(f.image);
  }, [deviceId, enabled, call]);

  useEffect(() => {
    liveRef.current = true;
    return () => {
      liveRef.current = false;
    };
  }, []);
  useEffect(() => {
    if (deviceId) loadConfig();
  }, [deviceId, loadConfig]);
  useEffect(() => {
    if (!enabled) return;
    refresh();
    const iv = setInterval(refresh, 4000);
    return () => clearInterval(iv);
  }, [enabled, refresh]);

  const run = useCallback(
    async (label: string, fn: () => Promise<any>) => {
      setBusy(true);
      setMsg(label + "…");
      const r = await fn();
      setMsg(r?.error ? `${label}: ${r.error}` : `${label} ✓`);
      setBusy(false);
      refresh();
    },
    [refresh],
  );

  const doDiscover = () =>
    run("Discover", async () => {
      const r = await call("printer_discover", { seconds: 6 });
      setFound(r?.printers || []);
      return r?.printers?.length ? {} : { error: "no printers found on that box's LAN" };
    });

  const saveConfig = () =>
    run("Link printer", async () => {
      if (!addr || !serial) return { error: "addr + serial required (discover first)" };
      const r = await call("printer_config_set", { driver: "bambu", addr, serial, model, accessCode: code || undefined });
      setConfig(r?.config || null);
      setEnabled(!!r?.enabled);
      setCode("");
      if (r?.enabled) setShowSetup(false);
      return r;
    });

  const doRender = () =>
    run("Render", async () => {
      setPreview(null);
      setStlPath(null);
      const r = await call("cad_render", { scad, name: "model" });
      if (r?.error) return r;
      if (r?.preview) setPreview(r.preview);
      if (r?.stlPath) setStlPath(r.stlPath);
      return r;
    });

  const doPrint = () => {
    if (!stlPath) {
      setMsg("Render first");
      return;
    }
    if (!confirm("Start a print? This runs the printer for hours — make sure the bed is clear.")) return;
    run("Slice→Print", async () => {
      const sl = await call("cad_slice", { modelPath: stlPath });
      if (!sl?.outputPath) return { error: sl?.error || "no slicer on box" };
      const up = await call("printer_upload", { localPath: sl.outputPath });
      if (!up?.remoteFile) return { error: "upload failed" };
      return call("printer_print", { remoteFile: up.remoteFile, confirm: true, bedLevel: true });
    });
  };

  const online = devices.filter((d) => (d as any).isOnline ?? (d as any).online ?? true);
  const stateColor = status?.state === "printing" ? "#28c76f" : status?.state === "failed" ? "#ea5455" : status?.state === "paused" ? "#ff9f43" : "#9aa4b2";

  return (
    <div className="flex flex-col gap-3 text-sm">
      <h2 className="text-lg font-semibold">3D Printer cell</h2>

      <div className="rounded-xl border border-white/10 p-3">
        <div className="mb-2 text-xs text-white/50">Host box on the printer's LAN</div>
        <div className="flex flex-wrap gap-2">
          {online.map((d) => (
            <button
              key={d.id}
              onClick={() => setDeviceId(d.id)}
              className={`rounded-full border px-3 py-1.5 ${d.id === deviceId ? "border-sky-400 bg-sky-400/20 text-sky-300" : "border-white/15 text-white/80"}`}
            >
              {d.name || d.id.slice(0, 8)}
            </button>
          ))}
        </div>
      </div>

      {deviceId && (showSetup || !enabled) ? (
        <div className="rounded-xl border border-white/10 p-3">
          <div className="mb-2 font-medium">Link a printer</div>
          <button onClick={doDiscover} disabled={busy} className="rounded-lg bg-sky-500 px-3 py-2 font-medium text-white disabled:opacity-50">
            Discover on LAN
          </button>
          <div className="mt-2 flex flex-col gap-1">
            {found.map((d) => (
              <button key={d.serial} onClick={() => { setAddr(d.ip); setSerial(d.serial); setModel(d.model); }} className="flex items-center justify-between rounded-lg border border-white/10 px-3 py-2 text-left hover:bg-white/5">
                <span>{d.model} · {d.ip}</span>
                <span className="text-xs text-white/50">{d.signalDb ? `${d.signalDb}dBm` : ""} {d.bind}</span>
              </button>
            ))}
          </div>
          <div className="mt-2 grid grid-cols-2 gap-2">
            <input value={addr} onChange={(e) => setAddr(e.target.value)} placeholder="IP" className="rounded-lg border border-white/15 bg-transparent px-2 py-1.5" />
            <input value={serial} onChange={(e) => setSerial(e.target.value)} placeholder="Serial" className="rounded-lg border border-white/15 bg-transparent px-2 py-1.5" />
            <input value={model} onChange={(e) => setModel(e.target.value)} placeholder="Model" className="rounded-lg border border-white/15 bg-transparent px-2 py-1.5" />
            <input value={code} onChange={(e) => setCode(e.target.value)} type="password" placeholder="LAN access code" className="rounded-lg border border-white/15 bg-transparent px-2 py-1.5" />
          </div>
          <button onClick={saveConfig} disabled={busy} className="mt-2 rounded-lg bg-emerald-500 px-3 py-2 font-medium text-white disabled:opacity-50">
            Save &amp; link
          </button>
        </div>
      ) : null}

      {enabled ? (
        <div className="rounded-xl border border-white/10 p-3">
          <div className="flex items-center justify-between">
            <div className="font-medium">{config?.model || "Printer"} · {config?.addr}</div>
            <div style={{ color: stateColor }} className="font-bold">{(status?.state || "—").toUpperCase()}</div>
          </div>
          {status?.stage ? <div className="text-xs text-white/50">{status.stage}</div> : null}
          {frame ? <img src={frame} alt="chamber" className="mt-2 max-h-72 w-full rounded-lg bg-black object-contain" /> : null}
          <div className="mt-2 flex flex-wrap gap-4">
            <Stat label="Nozzle" v={`${Math.round(status?.nozzle?.cur || 0)}/${Math.round(status?.nozzle?.target || 0)}°`} />
            <Stat label="Bed" v={`${Math.round(status?.bed?.cur || 0)}/${Math.round(status?.bed?.target || 0)}°`} />
            <Stat label="Progress" v={`${Math.round(status?.progress || 0)}%`} />
            {status?.remainingMin ? <Stat label="ETA" v={`${status.remainingMin}m`} /> : null}
          </div>
          {status?.errors?.length ? <div className="mt-1 text-red-400">{status.errors.join(", ")}</div> : null}
          <div className="mt-3 flex flex-wrap gap-2">
            <Btn onClick={() => run("Light", () => call("printer_light", { on: !status?.lightOn }))}>{status?.lightOn ? "Light off" : "Light on"}</Btn>
            <Btn onClick={() => run("Pause", () => call("printer_pause"))}>Pause</Btn>
            <Btn onClick={() => run("Resume", () => call("printer_resume"))}>Resume</Btn>
            <Btn danger onClick={() => run("Stop", () => call("printer_stop"))}>Stop</Btn>
          </div>
          <button onClick={() => setShowSetup((v) => !v)} className="mt-2 text-xs text-sky-400">
            {showSetup ? "Hide setup" : "Edit connection"}
          </button>
        </div>
      ) : null}

      {deviceId ? (
        <div className="rounded-xl border border-white/10 p-3">
          <div className="font-medium">Remote CAD (OpenSCAD)</div>
          <div className="mb-2 text-xs text-white/50">Write OpenSCAD; the box renders it; the preview shows here. Then slice → upload → print.</div>
          <textarea value={scad} onChange={(e) => setScad(e.target.value)} rows={7} spellCheck={false} className="w-full rounded-lg border border-white/15 bg-black/30 p-2 font-mono text-xs" />
          <div className="mt-2 flex gap-2">
            <button onClick={doRender} disabled={busy} className="flex-1 rounded-lg bg-sky-500 px-3 py-2 font-medium text-white disabled:opacity-50">Render</button>
            {stlPath && enabled ? <button onClick={doPrint} disabled={busy} className="flex-1 rounded-lg bg-amber-500 px-3 py-2 font-medium text-white disabled:opacity-50">Slice → Print</button> : null}
          </div>
          {preview ? <img src={preview} alt="cad" className="mt-2 max-h-80 w-full rounded-lg bg-[#0b0f14] object-contain" /> : null}
        </div>
      ) : null}

      {msg ? <div className="text-white/50">{busy ? "⏳ " : ""}{msg}</div> : null}
    </div>
  );
}

function Stat({ label, v }: { label: string; v: string }) {
  return (
    <div>
      <div className="text-[11px] text-white/50">{label}</div>
      <div className="text-base font-bold">{v}</div>
    </div>
  );
}
function Btn({ children, onClick, danger }: { children: React.ReactNode; onClick: () => void; danger?: boolean }) {
  return (
    <button onClick={onClick} className={`rounded-full border px-3 py-1.5 ${danger ? "border-red-500 text-red-400" : "border-white/15 text-white/80"}`}>
      {children}
    </button>
  );
}
