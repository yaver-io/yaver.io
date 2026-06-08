"use client";

// ArmCellView — drive ANY multi-DOF arm (Fairino / Elephant myCobot / PAROL6 /
// generic) from the web dashboard over the relay. DOF + joint limits come from
// arm_describe, so this one view renders N joint controls for any robot —
// nothing hardcoded. Jog / MoveJ / home / e-stop, hand-guide learning mode
// (free-drive → capture → save → repeat), and the box's shared camera (the same
// frame the host coding agent sees via the robot_camera MCP tool). Web is
// relay-only by design.
import { useCallback, useEffect, useRef, useState } from "react";
import { AgentClient, agentClient } from "@/lib/agent-client";
import type { Device } from "@/lib/use-devices";

type JointSpec = { name: string; type?: string; min: number; max: number; home?: number; unit?: string };
type ArmInfo = { model?: string; vendor?: string; dof: number; joints: JointSpec[]; hasCartesian?: boolean; source?: string };
type JointState = { name: string; position: number; unit?: string };
type ArmStatus = { ok?: boolean; connected?: boolean; enabled?: boolean; estopped?: boolean; joints?: JointState[]; cameraOk?: boolean; error?: string };
type ArmConfig = { driver?: string; addr?: string; camera?: string; info?: ArmInfo };
type Program = { name: string; waypoints?: any[] };
type Waypoint = { joints?: Record<string, number>; pose?: any; velPct?: number; verify?: string; label?: string };
type RobotModel = { vendor: string; model: string; driver: string; transport: string; payloadKg?: number; reachMm?: number; info: ArmInfo; note?: string };

const DRIVERS = ["fairino", "mycobot", "parol6", "generic_tcp", "generic_serial", "bridge"];
const STEPS = [1, 5, 15, 45];
const SPEEDS = [10, 30, 60, 100];

export default function ArmCellView({ devices, token }: { devices: Device[]; token: string | null }) {
  const [deviceId, setDeviceId] = useState("");
  const [info, setInfo] = useState<ArmInfo | null>(null);
  const [status, setStatus] = useState<ArmStatus | null>(null);
  const [config, setConfig] = useState<ArmConfig | null>(null);
  const [joints, setJoints] = useState<JointState[]>([]);
  const [frame, setFrame] = useState<string | null>(null);
  const [programs, setPrograms] = useState<Program[]>([]);
  const [models, setModels] = useState<RobotModel[]>([]);
  const [waypoints, setWaypoints] = useState<Waypoint[]>([]);
  const [progName, setProgName] = useState("");
  const [step, setStep] = useState(15);
  const [vel, setVel] = useState(30);
  const [verify, setVerify] = useState<"frames" | "agent" | "off">("frames");
  const [freedrive, setFreedrive] = useState(false);
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

  const callArm = useCallback(
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
    const s = await callArm("arm_status");
    if (s?.status) {
      setStatus(s.status);
      if (s.status.joints) setJoints(s.status.joints);
    }
    if (s?.info) setInfo(s.info);
  }, [deviceId, callArm]);

  // load config/describe/programs on device pick
  useEffect(() => {
    if (!deviceId) return;
    (async () => {
      const cfg = await callArm("arm_config_get");
      if (cfg?.config) setConfig(cfg.config);
      const d = await callArm("arm_describe");
      if (d?.info) setInfo(d.info);
      const pl = await callArm("arm_program_list");
      if (pl?.programs) setPrograms(pl.programs);
      const m = await callArm("arm_models");
      if (m?.models) setModels(m.models);
      refresh();
    })();
  }, [deviceId]); // eslint-disable-line

  // camera + state poll
  useEffect(() => {
    if (!deviceId) return;
    liveRef.current = true;
    let stop = false;
    (async () => {
      while (!stop && liveRef.current) {
        const snap = await callArm("arm_snapshot");
        if (snap?.image) setFrame(snap.image);
        const st = await callArm("arm_state");
        if (st?.joints) setJoints(st.joints);
        await new Promise((r) => setTimeout(r, 1000));
      }
    })();
    return () => {
      stop = true;
      liveRef.current = false;
    };
  }, [deviceId]); // eslint-disable-line

  const run = useCallback(
    async (fn: () => Promise<any>) => {
      setBusy(true);
      setMsg(null);
      const r = await fn();
      if (r?.ok === false) setMsg(r.error || r.code || "failed");
      await refresh();
      setBusy(false);
      return r;
    },
    [refresh],
  );

  const jog = (joint: string, dir: 1 | -1) => run(() => callArm("arm_jog", { joint, delta: dir * step, velPct: vel, verify }));

  const toggleFreedrive = async () => {
    const next = !freedrive;
    const r = await run(() => callArm("arm_freedrive", { on: next }));
    if (r?.ok !== false) setFreedrive(next);
  };

  const capture = async () => {
    const r = await callArm("arm_teach_capture", { label: `wp${waypoints.length + 1}`, velPct: vel });
    if (r?.waypoint) setWaypoints((w) => [...w, { ...r.waypoint, velPct: vel, verify }]);
  };

  const saveProgram = async () => {
    if (!progName.trim() || waypoints.length === 0) return;
    await run(() => callArm("arm_program_save", { program: { name: progName.trim(), waypoints } }));
    const pl = await callArm("arm_program_list");
    if (pl?.programs) setPrograms(pl.programs);
    setWaypoints([]);
  };

  const armDevices = devices;

  const btn = "rounded-md px-3 py-1.5 text-sm border border-neutral-700 bg-neutral-800 text-neutral-100 hover:bg-neutral-700 disabled:opacity-40";
  const btnAccent = "rounded-md px-3 py-1.5 text-sm bg-indigo-600 text-white hover:bg-indigo-500 disabled:opacity-40";

  if (!deviceId) {
    return (
      <div className="space-y-3">
        <h2 className="text-lg font-semibold text-neutral-100">Robotic Arm — pick the device</h2>
        <p className="text-sm text-neutral-400">Yaver is one layer for every arm (Fairino / myCobot / PAROL6 / generic). Pick the box the arm is wired to.</p>
        {armDevices.map((d) => (
          <button key={d.id} onClick={() => setDeviceId(d.id)} className="flex w-full items-center justify-between rounded-lg border border-neutral-800 bg-neutral-900 px-4 py-3 text-left hover:border-neutral-600">
            <span className="font-medium text-neutral-100">{d.name || d.id}</span>
            <span className="text-xs text-neutral-500">{(d as any).online ? "online" : "offline"}</span>
          </button>
        ))}
        {armDevices.length === 0 && <p className="text-sm text-neutral-500">No devices yet.</p>}
      </div>
    );
  }

  return (
    <div className="space-y-4">
      {/* identity + status */}
      <div className="flex items-center justify-between rounded-lg border border-neutral-800 bg-neutral-900 p-4">
        <div>
          <div className="font-semibold text-neutral-100">
            {info?.vendor ? `${info.vendor} ${info?.model || ""}` : config?.driver || "arm"} · {info?.dof ?? "?"} DOF
          </div>
          <div className="text-xs text-neutral-400">
            {status?.connected ? (status?.estopped ? "E-STOPPED" : status?.enabled ? "enabled" : "ready") : "offline"}
            {info?.source ? ` · ${info.source}` : ""}
          </div>
        </div>
        <div className="flex gap-2">
          <button className={btn} onClick={() => setShowSetup((v) => !v)}>Setup</button>
          <button className={btn} onClick={() => setDeviceId("")}>Switch</button>
        </div>
      </div>

      {showSetup && (
        <div className="space-y-3 rounded-lg border border-neutral-800 bg-neutral-900 p-4">
          <div className="text-sm font-semibold text-neutral-200">Robot model</div>
          <select
            className="w-full rounded-md border border-neutral-700 bg-neutral-950 px-3 py-2 text-sm text-neutral-100"
            defaultValue=""
            onChange={(e) => {
              const m = models.find((x) => `${x.vendor}__${x.model}` === e.target.value);
              if (m) setConfig((c) => ({ ...(c || {}), driver: m.driver, info: m.info, addr: c?.addr || (m.driver === "fairino" ? "192.168.58.2" : c?.addr || "") }));
            }}
          >
            <option value="">— pick your robot (prefills DOF + joints) —</option>
            {models.map((m) => (
              <option key={`${m.vendor}__${m.model}`} value={`${m.vendor}__${m.model}`}>
                {m.vendor} {m.model} · {m.info.dof} DOF{m.payloadKg ? ` · ${m.payloadKg}kg` : ""}{m.reachMm ? ` · ${m.reachMm}mm` : ""}
              </option>
            ))}
          </select>
          <div className="text-sm font-semibold text-neutral-200">Driver</div>
          <div className="flex flex-wrap gap-2">
            {DRIVERS.map((dv) => (
              <button key={dv} className={config?.driver === dv ? btnAccent : btn} onClick={() => setConfig((c) => ({ ...(c || {}), driver: dv }))}>{dv}</button>
            ))}
          </div>
          <label className="block text-xs text-neutral-400">Address (ip / ip:port / /dev/tty… / bridge URL)</label>
          <input className="w-full rounded-md border border-neutral-700 bg-neutral-950 px-3 py-2 text-sm text-neutral-100" value={config?.addr || ""} onChange={(e) => setConfig((c) => ({ ...(c || {}), addr: e.target.value }))} placeholder="192.168.58.2" />
          <label className="block text-xs text-neutral-400">Camera (external / http(s)://snapshot / /dev/video0)</label>
          <input className="w-full rounded-md border border-neutral-700 bg-neutral-950 px-3 py-2 text-sm text-neutral-100" value={config?.camera || ""} onChange={(e) => setConfig((c) => ({ ...(c || {}), camera: e.target.value }))} placeholder="external" />
          <button
            className={btnAccent}
            onClick={async () => {
              if (!config) return;
              await run(() => callArm("arm_config_set", config as any));
              const d = await callArm("arm_describe");
              if (d?.info) setInfo(d.info);
              setShowSetup(false);
            }}
          >
            Save &amp; connect
          </button>
        </div>
      )}

      {/* camera */}
      <div className="flex aspect-[4/3] items-center justify-center overflow-hidden rounded-lg border border-neutral-800 bg-black">
        {frame ? <img src={frame} alt="arm camera" className="max-h-full max-w-full object-contain" /> : <span className="text-sm text-neutral-500">{status?.cameraOk === false ? "no camera configured" : "waiting for camera…"}</span>}
      </div>

      {/* step + speed */}
      <div className="flex flex-wrap items-center gap-4 rounded-lg border border-neutral-800 bg-neutral-900 p-4">
        <div>
          <div className="mb-1 text-xs text-neutral-400">Step (°)</div>
          <div className="flex gap-1.5">{STEPS.map((s) => <button key={s} className={step === s ? btnAccent : btn} onClick={() => setStep(s)}>{s}</button>)}</div>
        </div>
        <div>
          <div className="mb-1 text-xs text-neutral-400">Speed %</div>
          <div className="flex gap-1.5">{SPEEDS.map((s) => <button key={s} className={vel === s ? btnAccent : btn} onClick={() => setVel(s)}>{s}</button>)}</div>
        </div>
        <div>
          <div className="mb-1 text-xs text-neutral-400">Verify</div>
          <div className="flex gap-1.5">{(["frames", "agent", "off"] as const).map((v) => <button key={v} className={verify === v ? btnAccent : btn} onClick={() => setVerify(v)}>{v}</button>)}</div>
        </div>
      </div>

      {/* joints — data-driven */}
      <div className="rounded-lg border border-neutral-800 bg-neutral-900 p-4">
        <div className="mb-3 text-sm font-semibold text-neutral-200">Joints</div>
        {(info?.joints || []).map((j) => {
          const cur = joints.find((x) => x.name.toLowerCase() === j.name.toLowerCase());
          return (
            <div key={j.name} className="mb-2 flex items-center gap-3">
              <span className="w-10 font-semibold text-neutral-100">{j.name}</span>
              <span className="flex-1 text-sm text-neutral-300">
                {cur ? cur.position.toFixed(1) : "—"} {j.unit || "deg"} <span className="text-xs text-neutral-500">[{j.min}…{j.max}]</span>
              </span>
              <button className={btn} disabled={busy} onClick={() => jog(j.name, -1)}>−</button>
              <button className={btn} disabled={busy} onClick={() => jog(j.name, 1)}>+</button>
            </div>
          );
        })}
        {(!info?.joints || info.joints.length === 0) && <p className="text-sm text-neutral-500">No joints — open Setup, pick a driver, Save &amp; connect.</p>}
      </div>

      {/* primary controls */}
      <div className="flex flex-wrap gap-2 rounded-lg border border-neutral-800 bg-neutral-900 p-4">
        <button className={btn} disabled={busy} onClick={() => run(() => callArm("arm_enable", { on: !status?.enabled }))}>{status?.enabled ? "Disable" : "Enable"}</button>
        <button className={btn} disabled={busy} onClick={() => run(() => callArm("arm_home", { velPct: vel, verify }))}>Home</button>
        <button className="rounded-md px-3 py-1.5 text-sm bg-amber-500 text-black" disabled={busy} onClick={() => run(() => callArm("arm_stop"))}>Stop</button>
        <button className="rounded-md px-3 py-1.5 text-sm bg-red-600 font-semibold text-white" disabled={busy} onClick={() => run(() => callArm("arm_estop"))}>E-STOP</button>
        {status?.estopped && <button className={btnAccent} disabled={busy} onClick={() => run(() => callArm("arm_reset"))}>Reset</button>}
      </div>

      {/* learning mode */}
      <div className="space-y-3 rounded-lg border border-neutral-800 bg-neutral-900 p-4">
        <div className="text-sm font-semibold text-neutral-200">Learning mode (hand-guide &amp; repeat)</div>
        <div className="flex gap-2">
          <button className={freedrive ? btnAccent : btn} disabled={busy} onClick={toggleFreedrive}>{freedrive ? "Free-drive ON" : "Free-drive"}</button>
          <button className={btn} disabled={busy} onClick={capture}>Capture waypoint ({waypoints.length})</button>
        </div>
        <div className="flex gap-2">
          <input className="flex-1 rounded-md border border-neutral-700 bg-neutral-950 px-3 py-2 text-sm text-neutral-100" value={progName} onChange={(e) => setProgName(e.target.value)} placeholder="program name" />
          <button className={btnAccent} disabled={busy || !progName.trim() || waypoints.length === 0} onClick={saveProgram}>Save</button>
        </div>
        {programs.length > 0 && (
          <div className="space-y-1">
            <div className="text-xs text-neutral-400">Saved programs</div>
            {programs.map((p) => (
              <div key={p.name} className="flex items-center justify-between py-1">
                <span className="text-sm text-neutral-200">{p.name} · {p.waypoints?.length || 0} pts</span>
                <button className={btn} disabled={busy} onClick={() => run(() => callArm("arm_program_run", { name: p.name, verify }))}>▶ Repeat</button>
              </div>
            ))}
          </div>
        )}
      </div>

      {msg && <p className="text-center text-sm text-red-400">{msg}</p>}
    </div>
  );
}
