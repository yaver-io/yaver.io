"use client";

// RobotCellView — drive a Yaver robot cell from the web dashboard, over the
// relay (browsers can't reach the LAN). Same agent verbs as the mobile app:
// pick a robot device, then jog / screwdriver-rotate / calibrate (camera Z
// touch-off + seat torque) / teach-and-repeat / drive screws home to torque.
// The controls shown follow the device's PROFILE (cartesian / +screwdriver /
// screwdriver-only). Web is relay-only by design.
import { useCallback, useEffect, useRef, useState } from "react";
import { AgentClient, agentClient } from "@/lib/agent-client";
import type { Device } from "@/lib/use-devices";

type Module = "motion" | "tool" | "rotate" | "gpio" | "camera";
type Pos = { x: number; y: number; z: number; homed: boolean };
type Status = {
  ok?: boolean;
  connected?: boolean;
  position?: Pos;
  tool?: string;
  estopped?: boolean;
  cameraOk?: boolean;
  error?: string;
  code?: string;
  profile?: string;
  modules?: Module[];
  label?: string;
  companion?: boolean;
  targetTorqueNmm?: number;
  zEngage?: number;
  zSafe?: number;
};
type Step = {
  type: string;
  axis?: string;
  dist?: number;
  x?: number;
  y?: number;
  z?: number;
  feed?: number;
  on?: boolean;
  turns?: number;
  rpm?: number;
  ccw?: boolean;
  label?: string;
};
type Program = { name: string; steps: Step[] };
type Profile = { kind: string; label: string; modules: Module[]; desc: string };

const FEED_XY = 3000;
const FEED_Z = 600;
const moduleOn = (s: Status | null, m: Module) => (!s?.modules ? true : s.modules.includes(m));

export default function RobotCellView({ devices, token }: { devices: Device[]; token: string | null }) {
  const [deviceId, setDeviceId] = useState("");
  const [status, setStatus] = useState<Status | null>(null);
  const [frame, setFrame] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [step, setStep] = useState(10);
  const [zStep, setZStep] = useState(0.5);
  const [verify, setVerify] = useState<"agent" | "frames" | "off">("frames");
  const [profiles, setProfiles] = useState<Profile[]>([]);
  const [programs, setPrograms] = useState<Program[]>([]);
  const [recording, setRecording] = useState(false);
  const [steps, setSteps] = useState<Step[]>([]);
  const [progName, setProgName] = useState("");
  const [liveTorque, setLiveTorque] = useState<number | null>(null);
  const [target, setTarget] = useState(200);
  const [last, setLast] = useState<any>(null);
  const [connErr, setConnErr] = useState<string | null>(null);

  const clientRef = useRef<AgentClient | null>(null);
  const connectedTo = useRef("");
  const snapBusy = useRef(false);
  const tqBusy = useRef(false);

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

  const callRobot = useCallback(
    async (verb: string, payload: Record<string, unknown> = {}): Promise<any> => {
      try {
        const client = await ensureClient(deviceId);
        if (!client) return { ok: false, error: "not connected" };
        const res = await client.callOps(verb, payload);
        if (res?.ok === false) return { ok: false, code: res.code, error: res.error };
        return (res as any)?.initial ?? res;
      } catch (e: any) {
        setConnErr(e?.message || "connection failed");
        return { ok: false, error: e?.message || "failed" };
      }
    },
    [deviceId, ensureClient],
  );

  const refresh = useCallback(async () => {
    if (!deviceId) return;
    const s = await callRobot("robot_status");
    setStatus(s);
    setConnErr(null);
  }, [deviceId, callRobot]);

  const loadMeta = useCallback(async () => {
    if (!deviceId) return;
    const [pf, pg] = await Promise.all([callRobot("robot_profiles"), callRobot("robot_program_list")]);
    if (pf?.profiles) setProfiles(pf.profiles);
    if (pg?.programs) setPrograms(pg.programs);
  }, [deviceId, callRobot]);

  // pick first robot-capable device on mount (best-effort)
  useEffect(() => {
    if (deviceId || devices.length === 0) return;
    const online = devices.find((d) => (d as any).online !== false);
    if (online) setDeviceId(online.id);
  }, [devices, deviceId]);

  useEffect(() => {
    if (!deviceId) return;
    setStatus(null);
    setFrame(null);
    refresh();
    loadMeta();
  }, [deviceId, refresh, loadMeta]);

  // camera snapshot poll
  useEffect(() => {
    if (!deviceId) return;
    let stop = false;
    const tick = async () => {
      if (snapBusy.current || stop) return;
      snapBusy.current = true;
      try {
        const r = await callRobot("robot_snapshot");
        if (!stop && r?.image) setFrame(r.image);
      } finally {
        snapBusy.current = false;
      }
    };
    tick();
    const iv = setInterval(tick, 1600);
    return () => {
      stop = true;
      clearInterval(iv);
    };
  }, [deviceId, callRobot]);

  // live torque poll while a companion is present
  useEffect(() => {
    if (!deviceId || !status?.companion) {
      setLiveTorque(null);
      return;
    }
    let stop = false;
    const tick = async () => {
      if (tqBusy.current || stop) return;
      tqBusy.current = true;
      try {
        const r = await callRobot("robot_torque");
        if (!stop && typeof r?.torqueNmm === "number") setLiveTorque(r.torqueNmm);
      } finally {
        tqBusy.current = false;
      }
    };
    tick();
    const iv = setInterval(tick, 900);
    return () => {
      stop = true;
      clearInterval(iv);
    };
  }, [deviceId, status?.companion, callRobot]);

  useEffect(() => () => clientRef.current?.disconnect(), []);

  const state: "no-device" | "offline" | "needs-auth" | "ok" = !deviceId
    ? "no-device"
    : connErr
      ? "offline"
      : !status
        ? "offline"
        : status.ok === false
          ? /different user|unauthor|authenti|enroll|disabled|forbidden/.test(`${status.error || ""} ${status.code || ""}`.toLowerCase())
            ? "needs-auth"
            : status.connected
              ? "ok"
              : "offline"
          : "ok";
  const disabled = busy || state !== "ok";
  const hasMotion = moduleOn(status, "motion");
  const hasTool = moduleOn(status, "tool");
  const hasRotate = moduleOn(status, "rotate");
  const pos = status?.position;
  const seated = status?.companion && liveTorque != null && status?.targetTorqueNmm ? liveTorque >= status.targetTorqueNmm : false;

  const run = async (verb: string, payload: Record<string, unknown>, record?: Step) => {
    if (disabled) return;
    setBusy(true);
    try {
      const r = await callRobot(verb, payload);
      setLast(r);
      if (recording && record && r?.ok !== false) setSteps((p) => [...p, record]);
      await refresh();
    } finally {
      setBusy(false);
    }
  };
  const jog = (axis: "X" | "Y" | "Z", dir: 1 | -1) =>
    run("robot_jog", { axis, dist: dir * step, feed: axis === "Z" ? FEED_Z : FEED_XY, verify }, { type: "jog", axis, dist: dir * step, feed: axis === "Z" ? FEED_Z : FEED_XY });
  const jogZfine = (d: number) => run("robot_jog", { axis: "Z", dist: d, feed: FEED_Z, verify: "off" });
  const home = () => run("robot_home", { verify }, { type: "home" });
  const tool = (on: boolean) => run("robot_tool", { on }, { type: "tool", on });
  const rotate = (turns: number, ccw: boolean) => run("robot_screw_rotate", { turns, rpm: 300, ccw }, { type: "rotate", turns, rpm: 300, ccw });
  const driveHome = () => run("robot_screw", { verify, targetTorqueNmm: target }, recording && pos ? { type: "screw", x: pos.x, y: pos.y, label: "drive home" } : undefined);

  const estop = async () => {
    if (!deviceId) return;
    setBusy(true);
    try {
      await callRobot("robot_estop");
      await refresh();
    } finally {
      setBusy(false);
    }
  };
  const reset = async () => {
    if (!deviceId) return;
    setBusy(true);
    try {
      await callRobot("robot_reset");
      await refresh();
    } finally {
      setBusy(false);
    }
  };

  const patchConfig = async (patch: Record<string, unknown>) => {
    setBusy(true);
    try {
      const cur = await callRobot("robot_config_get");
      await callRobot("robot_config_set", { ...(cur?.config || {}), ...patch });
      await refresh();
    } finally {
      setBusy(false);
    }
  };
  const setEngage = () => pos?.z != null && patchConfig({ zEngage: pos.z });
  const setSafe = () => pos?.z != null && patchConfig({ zSafe: pos.z });
  const selectProfile = async (kind: string) => {
    const cur = await callRobot("robot_config_get");
    await patchConfig({ ...(cur?.config || {}), profile: kind });
    await loadMeta();
  };

  const addScrewPoint = async () => {
    const s = await callRobot("robot_status");
    const p: Pos | undefined = s?.position;
    if (!p) return;
    setSteps((prev) => [...prev, { type: "move", x: p.x, y: p.y, feed: FEED_XY, label: "to screw" }, { type: "screw", x: p.x, y: p.y, label: "drive home" }]);
  };
  const markWaypoint = async () => {
    const s = await callRobot("robot_status");
    const p: Pos | undefined = s?.position;
    if (!p) return;
    setSteps((prev) => [...prev, { type: "move", x: p.x, y: p.y, z: p.z, feed: FEED_XY, label: "waypoint" }]);
  };
  const saveProgram = async () => {
    if (!progName.trim() || steps.length === 0) return;
    await callRobot("robot_program_save", { program: { name: progName.trim(), steps } });
    setSteps([]);
    setProgName("");
    setRecording(false);
    await loadMeta();
  };
  const playProgram = async (name: string) => {
    if (disabled) return;
    setBusy(true);
    try {
      setLast(await callRobot("robot_program_run", { name, verify }));
      await refresh();
    } finally {
      setBusy(false);
    }
  };
  const deleteProgram = async (name: string) => {
    await callRobot("robot_program_delete", { name });
    await loadMeta();
  };

  // ---- render helpers ----
  const card = "rounded-xl border border-surface-800 bg-surface-900/50 p-4";
  const btn = "rounded-lg border border-surface-700 bg-surface-900 px-3 py-2 text-sm font-medium text-surface-200 hover:bg-surface-800 disabled:opacity-40";
  const btnA = "rounded-lg bg-brand px-3 py-2 text-sm font-semibold text-brand-fg hover:opacity-90 disabled:opacity-40";
  const jogBtn = "h-12 w-20 rounded-lg border border-surface-700 bg-surface-900 text-sm font-semibold text-surface-100 hover:bg-surface-800 disabled:opacity-40";

  return (
    <div className="space-y-4">
      {/* header */}
      <div className="flex items-center justify-between gap-3">
        <div>
          <h2 className="text-lg font-bold text-surface-100">Robot Cell</h2>
          <div className="flex items-center gap-2 text-sm text-surface-400">
            <span className={`h-2 w-2 rounded-full ${state === "ok" ? "bg-emerald-500" : state === "needs-auth" ? "bg-amber-500" : "bg-rose-500"}`} />
            {state === "ok" ? (busy ? "Busy" : "Idle") : state === "needs-auth" ? "Needs auth" : state === "offline" ? "Offline" : "No robot"}
            {status?.profile && state === "ok" ? ` · ${profiles.find((p) => p.kind === status.profile)?.label || status.profile}` : ""}
          </div>
        </div>
        <select value={deviceId} onChange={(e) => setDeviceId(e.target.value)} className="rounded-lg border border-surface-700 bg-surface-900 px-3 py-2 text-sm text-surface-200">
          <option value="">Pick robot device…</option>
          {devices.map((d) => (
            <option key={d.id} value={d.id}>
              {d.name || d.id.slice(0, 8)}
            </option>
          ))}
        </select>
      </div>

      {/* profile picker */}
      {state === "ok" && profiles.length > 0 && (
        <div className="flex flex-wrap gap-2">
          {profiles.map((p) => (
            <button key={p.kind} onClick={() => selectProfile(p.kind)} disabled={busy} title={p.desc} className={p.kind === status?.profile ? btnA : btn}>
              {p.label}
            </button>
          ))}
        </div>
      )}

      {/* fail-case banner */}
      {deviceId && state !== "ok" && (
        <div className={`${card} ${state === "needs-auth" ? "border-amber-600/50" : "border-rose-600/50"}`}>
          <div className="font-semibold text-surface-100">
            {state === "needs-auth" ? "Device needs re-authorization" : "Device offline"}
          </div>
          <p className="mt-1 text-sm text-surface-400">
            {state === "needs-auth"
              ? "The agent lost its sign-in (token rotated / signed out). Re-authorize it as your account on the device (yaver auth), then recheck."
              : connErr || "Can't reach the device over the relay. Check it's powered on and running the Yaver agent."}
          </p>
          <button onClick={refresh} className={`${btnA} mt-3`}>
            Recheck
          </button>
        </div>
      )}

      {/* camera */}
      <div className={`relative flex aspect-video items-center justify-center overflow-hidden rounded-xl bg-black ${state === "ok" ? "" : "opacity-50"}`}>
        {frame ? (
          <img src={frame} alt="robot camera" className="h-full w-full object-contain" />
        ) : (
          <span className="text-surface-500">{!deviceId ? "Pick a robot device" : "starting camera…"}</span>
        )}
        {frame && (
          <span className="absolute left-3 top-2 flex items-center gap-1 text-xs font-bold text-white">
            <span className="h-2 w-2 rounded-full bg-rose-500" /> LIVE
          </span>
        )}
      </div>

      {/* status strip */}
      <div className={`${card} flex flex-wrap items-center gap-x-6 gap-y-2`}>
        {hasMotion && <Stat label="X" v={pos ? pos.x.toFixed(0) : "—"} />}
        {hasMotion && <Stat label="Y" v={pos ? pos.y.toFixed(0) : "—"} />}
        {hasMotion && <Stat label="Z" v={pos ? pos.z.toFixed(0) : "—"} />}
        {hasMotion && <Stat label="Homed" v={pos?.homed ? "yes" : "no"} good={pos?.homed} />}
        <Stat label="Cam" v={status?.cameraOk ? "ok" : "—"} good={status?.cameraOk} />
        {status?.companion && <Stat label="Torque" v={liveTorque != null ? liveTorque.toFixed(0) : "sensor"} good />}
        <Stat label="E-stop" v={status?.estopped ? "TRIP" : "clear"} good={!status?.estopped} />
        <button onClick={refresh} className="ml-auto text-sm text-brand">
          ↻ refresh
        </button>
      </div>

      {/* screwdriver */}
      {(hasTool || hasRotate) && (
        <div className={`${card} space-y-3`}>
          <div className="flex items-center justify-between">
            <span className="font-semibold text-surface-100">Screwdriver</span>
            {hasTool && (
              <button onClick={() => tool(status?.tool !== "on")} disabled={disabled} className={status?.tool === "on" ? btnA : btn}>
                {status?.tool === "on" ? "ON" : "Off"}
              </button>
            )}
          </div>
          {hasRotate && (
            <div className="flex gap-2">
              <button onClick={() => rotate(1, false)} disabled={disabled} className={btnA}>
                Drive ⟳ 1
              </button>
              <button onClick={() => rotate(1, true)} disabled={disabled} className={btn}>
                Loosen ⟲ 1
              </button>
              {hasTool && (
                <button onClick={driveHome} disabled={disabled} className={`${btnA} ml-auto`}>
                  Drive home → {target} N·mm
                </button>
              )}
            </div>
          )}
        </div>
      )}

      {/* calibration */}
      {hasTool && (
        <div className={`${card} space-y-3`}>
          <div className="flex items-center justify-between">
            <span className="font-semibold text-surface-100">Calibration (Z touch-off + torque)</span>
            <span className="text-xs text-surface-400">Z {pos ? pos.z.toFixed(2) : "—"}mm</span>
          </div>
          <p className="text-xs text-surface-400">Lower Z slowly until the bit meets the screw head (watch the camera), then set engage.</p>
          <div className="flex items-center gap-2">
            {[0.1, 0.5, 1, 5].map((s) => (
              <button key={s} onClick={() => setZStep(s)} className={zStep === s ? btnA : btn}>
                {s}
              </button>
            ))}
            <button onClick={() => hasMotion && jogZfine(zStep)} disabled={disabled} className={`${btn} ml-auto`}>
              Z ↑
            </button>
            <button onClick={() => hasMotion && jogZfine(-zStep)} disabled={disabled} className={btnA}>
              Z ↓
            </button>
          </div>
          <div className="flex gap-2">
            <button onClick={setEngage} disabled={disabled} className={`${btn} flex-1`}>
              Set engage {status?.zEngage ? `(${status.zEngage.toFixed(2)})` : ""}
            </button>
            <button onClick={setSafe} disabled={disabled} className={`${btn} flex-1`}>
              Set safe {status?.zSafe ? `(${status.zSafe.toFixed(2)})` : ""}
            </button>
          </div>
          <div className="flex flex-wrap items-center gap-2 border-t border-surface-800 pt-3">
            <span className="text-xs text-surface-400">Seat torque N·mm:</span>
            {[50, 100, 200, 400, 600].map((t) => (
              <button key={t} onClick={() => { setTarget(t); patchConfig({ targetTorqueNmm: t }); }} className={(status?.targetTorqueNmm || target) === t ? btnA : btn}>
                {t}
              </button>
            ))}
            {status?.companion && (
              <span className={`ml-auto font-mono text-lg font-bold ${seated ? "text-emerald-400" : "text-surface-200"}`}>
                {liveTorque != null ? liveTorque.toFixed(0) : "—"}
                <span className="text-xs text-surface-500"> / {status?.targetTorqueNmm || target}</span>
              </span>
            )}
          </div>
        </div>
      )}

      {/* motion */}
      {hasMotion && (
        <div className={`${card} space-y-3`}>
          <div className="flex items-center justify-between">
            <div className="flex gap-2">
              {[1, 10, 50].map((s) => (
                <button key={s} onClick={() => setStep(s)} className={step === s ? btnA : btn}>
                  {s}mm
                </button>
              ))}
            </div>
            <div className="flex gap-2">
              <button onClick={() => setVerify(verify === "frames" ? "agent" : verify === "agent" ? "off" : "frames")} className={btn}>
                verify: {verify}
              </button>
              <button onClick={home} disabled={disabled} className={btn}>
                Home
              </button>
            </div>
          </div>
          <div className="flex flex-col items-center gap-2">
            <button onClick={() => jog("Y", 1)} disabled={disabled} className={jogBtn}>
              Y +{step}
            </button>
            <div className="flex gap-2">
              <button onClick={() => jog("X", -1)} disabled={disabled} className={jogBtn}>
                X -{step}
              </button>
              <button onClick={() => jog("Z", 1)} disabled={disabled} className={`${jogBtn} border-brand text-brand`}>
                Z +{step}
              </button>
              <button onClick={() => jog("X", 1)} disabled={disabled} className={jogBtn}>
                X +{step}
              </button>
            </div>
            <div className="flex gap-2">
              <button onClick={() => jog("Y", -1)} disabled={disabled} className={jogBtn}>
                Y -{step}
              </button>
              <button onClick={() => jog("Z", -1)} disabled={disabled} className={`${jogBtn} border-brand text-brand`}>
                Z -{step}
              </button>
            </div>
          </div>
        </div>
      )}

      {/* teach & repeat */}
      <div className={`${card} space-y-3`}>
        <div className="flex items-center justify-between">
          <span className="font-semibold text-surface-100">Teach &amp; Repeat</span>
          <button onClick={() => setRecording((r) => !r)} className={recording ? "rounded-lg border border-rose-600 bg-rose-600/20 px-3 py-2 text-sm font-semibold text-rose-300" : btn}>
            {recording ? "● Recording" : "Record"}
          </button>
        </div>
        {recording && hasMotion && (
          <div className="flex gap-2">
            <button onClick={markWaypoint} disabled={disabled} className={`${btn} flex-1`}>
              Mark waypoint
            </button>
            {hasTool && (
              <button onClick={addScrewPoint} disabled={disabled} className={`${btnA} flex-1`}>
                Add screw point
              </button>
            )}
          </div>
        )}
        {steps.length > 0 && (
          <div className="space-y-1">
            {steps.map((s, i) => (
              <div key={i} className="text-sm text-surface-300">
                <span className="mr-2 text-surface-500">{i + 1}</span>
                {describe(s)}
              </div>
            ))}
            <div className="flex gap-2 pt-1">
              <input value={progName} onChange={(e) => setProgName(e.target.value)} placeholder="program name" className="flex-1 rounded-lg border border-surface-700 bg-surface-900 px-3 py-2 text-sm text-surface-200" />
              <button onClick={saveProgram} disabled={!progName.trim() || busy} className={btnA}>
                Save
              </button>
              <button onClick={() => setSteps([])} className={btn}>
                Clear
              </button>
            </div>
          </div>
        )}
        {programs.length > 0 && (
          <div className="space-y-2 border-t border-surface-800 pt-3">
            <div className="text-xs font-semibold text-surface-500">SAVED PROGRAMS</div>
            {programs.map((p) => (
              <div key={p.name} className="flex items-center justify-between">
                <div>
                  <div className="font-medium text-surface-200">{p.name}</div>
                  <div className="text-xs text-surface-500">{p.steps?.length ?? 0} steps</div>
                </div>
                <div className="flex gap-2">
                  <button onClick={() => playProgram(p.name)} disabled={disabled} className={btnA}>
                    ▶ Run
                  </button>
                  <button onClick={() => deleteProgram(p.name)} className={btn}>
                    ✕
                  </button>
                </div>
              </div>
            ))}
          </div>
        )}
      </div>

      {/* run result */}
      {last?.program !== undefined && (
        <div className={`${card} ${last.ok ? "border-emerald-600/50" : "border-rose-600/50"}`}>
          <span className={last.ok ? "font-semibold text-emerald-400" : "font-semibold text-rose-400"}>
            {last.ok ? "Program complete ✓" : "Program halted ✗"} — {last.completed ?? 0}/{last.total ?? 0} steps
          </span>
          {!last.ok && last.error && <p className="mt-1 text-sm text-surface-400">{last.error}</p>}
        </div>
      )}

      {/* e-stop */}
      <div className="flex gap-3">
        <button onClick={estop} className="flex-[2] rounded-xl bg-rose-600 py-4 text-lg font-bold tracking-wide text-white hover:bg-rose-500">
          E-STOP
        </button>
        <button onClick={reset} className="flex-1 rounded-xl border border-surface-700 py-4 font-semibold text-surface-200 hover:bg-surface-800">
          Reset
        </button>
      </div>
    </div>
  );
}

function Stat({ label, v, good }: { label: string; v: string; good?: boolean }) {
  return (
    <div>
      <div className="text-[11px] text-surface-500">{label}</div>
      <div className={`text-sm font-semibold ${good === undefined ? "text-surface-100" : good ? "text-emerald-400" : "text-rose-400"}`}>{v}</div>
    </div>
  );
}

function describe(s: Step): string {
  switch (s.type) {
    case "home":
      return "Home (G28)";
    case "jog":
      return `Jog ${s.axis} ${(s.dist ?? 0) >= 0 ? "+" : ""}${s.dist}mm`;
    case "move":
      return `Move${s.x != null ? ` X${s.x.toFixed?.(1) ?? s.x}` : ""}${s.y != null ? ` Y${s.y.toFixed?.(1) ?? s.y}` : ""}${s.z != null ? ` Z${s.z}` : ""}`;
    case "tool":
      return `Screwdriver ${s.on ? "ON" : "OFF"}`;
    case "rotate":
      return `Rotate ${s.ccw ? "⟲" : "⟳"} ${s.turns} @ ${s.rpm}rpm`;
    case "screw":
      return "Drive screw home (torque)";
    case "dwell":
      return `Wait ${s.ms}ms`;
    default:
      return s.type;
  }
}
