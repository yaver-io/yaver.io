// Robot Cell — drive a robot device over the YAVER MESH (not a hardcoded IP).
// Pick which Yaver device is the robot (multi-robot fleet); the app shows only
// the controls its PROFILE enables (cartesian / +screwdriver / screwdriver-only).
// Jog / home / screwdriver-rotate / teach-and-repeat, each camera+encoder
// verified, all over the mesh through any gateway. If the device loses auth the
// controls grey out and guide you to re-authorize it.
// docs/yaver-robot-fleet-mesh-design.md + yaver-robot-teach-motor-multicam.md.
import React, { useCallback, useEffect, useRef, useState } from "react";
import { ActivityIndicator, Image, Pressable, ScrollView, Share, Text, View } from "react-native";
import { Ionicons } from "@expo/vector-icons";
import { useColors } from "../../src/context/ThemeContext";
import { useDevice } from "../../src/context/DeviceContext";
import {
  getRobotDeviceId,
  moduleEnabled,
  robotClient,
  setRobotDeviceId,
  type ProfileOption,
  type RobotMoveResponse,
  type RobotProgram,
  type RobotRunResult,
  type RobotStatus,
  type RobotStep,
  type RobotTarget,
  type VerifyMode,
} from "../../src/lib/robotClient";
import { ScrewdriverPanel } from "../../src/components/robot/ScrewdriverPanel";
import { TeachPendant } from "../../src/components/robot/TeachPendant";
import { ProfileSheet } from "../../src/components/robot/ProfileSheet";
import { CalibrationPanel } from "../../src/components/robot/CalibrationPanel";
import { MachineSetupPanel } from "../../src/components/robot/MachineSetupPanel";
import { ArrayPanel } from "../../src/components/robot/ArrayPanel";
import type { ArrayParams, JigParams } from "../../src/lib/robotClient";

const STEPS = [1, 10, 50];
const FEED_XY = 3000;
const FEED_Z = 600;
const DANGER = "#ef4444";
const OK = "#22c55e";
const WARN = "#f59e0b";

type CellState = "no-device" | "offline" | "needs-auth" | "ok";

function cellState(deviceId: string, connected: boolean, status: RobotStatus | null): CellState {
  if (!deviceId) return "no-device";
  if (!connected || !status) return "offline";
  if (status.ok === false) {
    const s = `${status.error || ""} ${status.code || ""}`.toLowerCase();
    if (/different user|unauthor|authenti|enroll|disabled|forbidden/.test(s)) return "needs-auth";
    if (status.connected) return "ok";
    return "offline";
  }
  return "ok";
}

export default function RobotScreen() {
  const c = useColors();
  const { devices, connectionStatus } = useDevice();
  const connected = connectionStatus === "connected";
  const [deviceId, setDeviceId] = useState("");
  const [showPicker, setShowPicker] = useState(false);
  const [showProfiles, setShowProfiles] = useState(false);
  const [status, setStatus] = useState<RobotStatus | null>(null);
  const [busy, setBusy] = useState(false);
  const [live, setLive] = useState(true);
  const [frame, setFrame] = useState<string | null>(null);
  const [camErr, setCamErr] = useState<string | null>(null);
  const [step, setStep] = useState(10);
  const [verify, setVerify] = useState<VerifyMode>("frames");
  const [last, setLast] = useState<RobotMoveResponse | null>(null);
  const [profiles, setProfiles] = useState<ProfileOption[]>([]);
  const [programs, setPrograms] = useState<RobotProgram[]>([]);
  const [recording, setRecording] = useState(false);
  const [steps, setSteps] = useState<RobotStep[]>([]);
  const [runResult, setRunResult] = useState<RobotRunResult | null>(null);
  const [showCalib, setShowCalib] = useState(false);
  const [liveTorque, setLiveTorque] = useState<number | null>(null);
  const polling = useRef(false);
  const torquePolling = useRef(false);

  const robotDevice = devices.find((d) => d.id === deviceId);
  const state = cellState(deviceId, connected, status);
  const controlsDisabled = busy || state !== "ok";

  const hasMotion = moduleEnabled(status ?? undefined, "motion");
  const hasTool = moduleEnabled(status ?? undefined, "tool");
  const hasRotate = moduleEnabled(status ?? undefined, "rotate");
  const hasGpio = moduleEnabled(status ?? undefined, "gpio");

  const buildTarget = useCallback(
    (id: string): RobotTarget | undefined => {
      if (!id) return undefined;
      const d = devices.find((x) => x.id === id);
      return { id, lanIps: (d as any)?.lanIps, host: (d as any)?.host, port: (d as any)?.port };
    },
    [devices],
  );

  const refresh = useCallback(
    async (id: string) => {
      const t = buildTarget(id);
      if (!t) return;
      setStatus(await robotClient.status(t));
    },
    [buildTarget],
  );

  // profiles + saved programs (best-effort: silently empty on an older agent)
  const loadMeta = useCallback(
    async (id: string) => {
      const t = buildTarget(id);
      if (!t) return;
      const [pf, pg] = await Promise.all([robotClient.profiles(t), robotClient.programList(t)]);
      if (pf?.profiles) setProfiles(pf.profiles);
      if (pg?.programs) setPrograms(pg.programs);
    },
    [buildTarget],
  );

  useEffect(() => {
    (async () => {
      const id = await getRobotDeviceId();
      setDeviceId(id);
      if (id) {
        await refresh(id);
        await loadMeta(id);
      } else setShowPicker(true);
    })();
  }, [refresh, loadMeta]);

  // Camera: poll robot_snapshot over the mesh while live + reachable.
  useEffect(() => {
    if (!live || !deviceId || !connected) return;
    let cancelled = false;
    const tick = async () => {
      if (polling.current || cancelled) return;
      polling.current = true;
      try {
        const t = buildTarget(deviceId);
        if (!t) return;
        const r = await robotClient.snapshot(t);
        if (cancelled) return;
        if (r?.image) {
          setFrame(r.image);
          setCamErr(null);
        } else if (r?.error) setCamErr(r.error);
      } finally {
        polling.current = false;
      }
    };
    tick();
    const iv = setInterval(tick, 1500);
    return () => {
      cancelled = true;
      clearInterval(iv);
    };
  }, [live, deviceId, connected, buildTarget]);

  // Live torque: poll robot_torque while calibrating + a companion is present.
  useEffect(() => {
    if (!showCalib || !status?.companion || !deviceId || !connected) {
      setLiveTorque(null);
      return;
    }
    let cancelled = false;
    const tick = async () => {
      if (torquePolling.current || cancelled) return;
      torquePolling.current = true;
      try {
        const t = buildTarget(deviceId);
        if (!t) return;
        const r = await robotClient.torque(t);
        if (!cancelled && typeof r?.torqueNmm === "number") setLiveTorque(r.torqueNmm);
      } finally {
        torquePolling.current = false;
      }
    };
    tick();
    const iv = setInterval(tick, 700);
    return () => {
      cancelled = true;
      clearInterval(iv);
    };
  }, [showCalib, status?.companion, deviceId, connected, buildTarget]);

  const pick = useCallback(
    async (id: string) => {
      await setRobotDeviceId(id);
      setDeviceId(id);
      setShowPicker(false);
      setFrame(null);
      setStatus(null);
      await refresh(id);
      await loadMeta(id);
    },
    [refresh, loadMeta],
  );

  // run a control action; if recording + it succeeded, capture it as a step.
  const run = useCallback(
    async (fn: () => Promise<RobotMoveResponse>, recordStep?: RobotStep) => {
      if (!deviceId || controlsDisabled) return;
      setBusy(true);
      try {
        const res = await fn();
        setLast(res);
        if (recording && recordStep && (res as any)?.ok !== false) {
          setSteps((prev) => [...prev, recordStep]);
        }
        await refresh(deviceId);
      } finally {
        setBusy(false);
      }
    },
    [deviceId, controlsDisabled, recording, refresh],
  );

  const jog = (axis: "X" | "Y" | "Z", dir: 1 | -1) =>
    run(
      () => robotClient.jog(buildTarget(deviceId)!, axis, dir * step, axis === "Z" ? FEED_Z : FEED_XY, verify, `carriage moved ${dir > 0 ? "+" : "-"}${step}mm on ${axis}`),
      { type: "jog", axis, dist: dir * step, feed: axis === "Z" ? FEED_Z : FEED_XY },
    );
  const home = () => run(() => robotClient.home(buildTarget(deviceId)!, verify, "carriage moved to the home corner"), { type: "home" });
  const tool = (on: boolean) => run(() => robotClient.tool(buildTarget(deviceId)!, on), { type: "tool", on });
  const rotate = (turns: number, rpm: number, ccw: boolean) =>
    run(() => robotClient.rotate(buildTarget(deviceId)!, turns, rpm, ccw), { type: "rotate", turns, rpm, ccw });
  const gpio = (pin: number, value: number) => run(() => robotClient.gpio(buildTarget(deviceId)!, pin, value));
  const power = (on: boolean) => run(() => robotClient.power(buildTarget(deviceId)!, on));
  const motorsOff = () => run(() => robotClient.motorsOff(buildTarget(deviceId)!));

  // fine Z jog for touch-off — exact mm from the calibration panel, no recording
  const jogZfine = (dist: number) =>
    run(() => robotClient.jog(buildTarget(deviceId)!, "Z", dist, FEED_Z, "off", `Z touch-off ${dist >= 0 ? "+" : ""}${dist}mm`));

  // --- calibration (camera-guided Z touch-off) + drive-home test ---
  const patchConfig = async (patch: Record<string, unknown>) => {
    const t = buildTarget(deviceId);
    if (!t) return;
    setBusy(true);
    try {
      const cur = await robotClient.configGet(t);
      await robotClient.configSet(t, { ...(cur?.config || {}), ...patch } as any);
      await refresh(deviceId);
    } finally {
      setBusy(false);
    }
  };
  const setEngage = () => {
    const z = status?.position?.z;
    if (z != null) patchConfig({ zEngage: z });
  };
  const setSafe = () => {
    const z = status?.position?.z;
    if (z != null) patchConfig({ zSafe: z });
  };
  const setTarget = (nmm: number) => patchConfig({ targetTorqueNmm: nmm });
  const testScrew = () =>
    run(() => robotClient.screw(buildTarget(deviceId)!, { verify }) as any, recording ? screwStepAtCurrent() : undefined);

  // record a move-to-here + screw step (one fastening point in the program)
  const screwStepAtCurrent = (): RobotStep | undefined => {
    const p = status?.position;
    if (!p) return undefined;
    return { type: "screw", x: p.x, y: p.y, label: "drive home" };
  };
  const addScrewPoint = async () => {
    const t = buildTarget(deviceId);
    if (!t) return;
    const st = await robotClient.status(t);
    const p = st?.position;
    if (!p) return;
    setSteps((prev) => [
      ...prev,
      { type: "move", x: p.x, y: p.y, feed: 3000, label: "to screw" },
      { type: "screw", x: p.x, y: p.y, label: "drive home" },
    ]);
  };
  const markWaypoint = async () => {
    const t = buildTarget(deviceId);
    if (!t) return;
    const st = await robotClient.status(t);
    const p = st?.position;
    if (!p) return;
    setSteps((prev) => [...prev, { type: "move", x: p.x, y: p.y, z: p.z, feed: 3000, label: "waypoint" }]);
  };
  // e-stop / reset bypass the controlsDisabled gate — safety always works.
  const estop = async () => {
    if (!deviceId) return;
    setBusy(true);
    try {
      setLast((await robotClient.estop(buildTarget(deviceId)!)) as any);
      await refresh(deviceId);
    } finally {
      setBusy(false);
    }
  };
  const reset = async () => {
    if (!deviceId) return;
    setBusy(true);
    try {
      await robotClient.reset(buildTarget(deviceId)!);
      await refresh(deviceId);
    } finally {
      setBusy(false);
    }
  };

  const selectProfile = async (kind: string) => {
    const t = buildTarget(deviceId);
    if (!t) return;
    setBusy(true);
    try {
      const cur = await robotClient.configGet(t);
      const next = { ...(cur?.config || {}), profile: kind };
      await robotClient.configSet(t, next as any);
      await refresh(deviceId);
      await loadMeta(deviceId);
    } finally {
      setBusy(false);
      setShowProfiles(false);
    }
  };

  const saveProgram = async (name: string) => {
    const t = buildTarget(deviceId);
    if (!t || steps.length === 0) return;
    setBusy(true);
    try {
      await robotClient.programSave(t, { name, steps });
      setSteps([]);
      setRecording(false);
      await loadMeta(deviceId);
    } finally {
      setBusy(false);
    }
  };
  const playProgram = async (name: string) => {
    const t = buildTarget(deviceId);
    if (!t || controlsDisabled) return;
    setBusy(true);
    try {
      setRunResult(await robotClient.programRun(t, name, verify));
      await refresh(deviceId);
    } finally {
      setBusy(false);
    }
  };
  const deleteProgram = async (name: string) => {
    const t = buildTarget(deviceId);
    if (!t) return;
    await robotClient.programDelete(t, name);
    await loadMeta(deviceId);
  };
  const generateArray = async (params: ArrayParams) => {
    const t = buildTarget(deviceId);
    if (!t) return;
    setBusy(true);
    try {
      await robotClient.arrayBuild(t, params);
      await loadMeta(deviceId);
    } finally {
      setBusy(false);
    }
  };
  const getJig = async (jp: JigParams) => {
    const t = buildTarget(deviceId);
    if (!t) return;
    setBusy(true);
    try {
      const r = await robotClient.jigScad(t, jp);
      if (r?.scad) await Share.share({ message: r.scad, title: r.filename || "klemens-jig.scad" });
    } finally {
      setBusy(false);
    }
  };

  const pos = status?.position;
  const toolOn = status?.tool === "on";
  const stateLabel =
    state === "no-device" ? "no robot" : state === "offline" ? "Offline" : state === "needs-auth" ? "Needs auth" : busy ? "Busy" : "Idle";
  const stateColor = state === "ok" ? (busy ? WARN : OK) : state === "needs-auth" ? WARN : state === "no-device" ? c.tabInactive : DANGER;
  const card = { backgroundColor: c.bgCard ?? c.bg, borderColor: c.borderSubtle, borderWidth: 1, borderRadius: 16, padding: 14 } as const;
  const cross = last?.encoderCrossCheck;
  const v = last?.verify;
  const profileLabel = profiles.find((p) => p.kind === status?.profile)?.label || status?.profile;

  return (
    <ScrollView style={{ flex: 1, backgroundColor: c.bg }} contentContainerStyle={{ padding: 16, gap: 14 }}>
      {/* Header */}
      <View style={{ flexDirection: "row", alignItems: "center", justifyContent: "space-between" }}>
        <View style={{ flex: 1 }}>
          <Text style={{ color: c.textPrimary, fontSize: 22, fontWeight: "800" }}>{robotDevice?.name || "Robot Cell"}</Text>
          <View style={{ flexDirection: "row", alignItems: "center", gap: 6, marginTop: 2 }}>
            <View style={{ width: 8, height: 8, borderRadius: 4, backgroundColor: stateColor }} />
            <Text style={{ color: c.tabInactive }}>
              {stateLabel}
              {robotDevice ? "  ·  over mesh" : ""}
              {profileLabel && state === "ok" ? `  ·  ${profileLabel}` : ""}
            </Text>
          </View>
        </View>
        {deviceId && state === "ok" && (hasTool || hasMotion) && (
          <Pressable onPress={() => setShowCalib((s) => !s)} hitSlop={8} style={{ padding: 8 }}>
            <Ionicons name="construct-outline" size={20} color={showCalib ? c.accent : c.tabInactive} />
          </Pressable>
        )}
        {deviceId && state === "ok" && profiles.length > 0 && (
          <Pressable onPress={() => setShowProfiles((s) => !s)} hitSlop={8} style={{ padding: 8 }}>
            <Ionicons name="options-outline" size={20} color={c.accent} />
          </Pressable>
        )}
        <Pressable onPress={() => setShowPicker((s) => !s)} hitSlop={10} style={{ padding: 8 }}>
          <Ionicons name="swap-horizontal" size={20} color={c.accent} />
        </Pressable>
      </View>

      {/* Device picker */}
      {showPicker && (
        <View style={card}>
          <Text style={{ color: c.tabInactive, fontSize: 12, marginBottom: 8 }}>PICK THE ROBOT DEVICE</Text>
          {devices.length === 0 && <Text style={{ color: c.tabInactive }}>No devices. Pair a machine first.</Text>}
          {devices.map((d) => (
            <Pressable key={d.id} onPress={() => pick(d.id)} style={{ flexDirection: "row", alignItems: "center", justifyContent: "space-between", paddingVertical: 10, borderBottomColor: c.borderSubtle, borderBottomWidth: 1 }}>
              <View>
                <Text style={{ color: c.textPrimary, fontWeight: "600" }}>{d.name}</Text>
                <Text style={{ color: c.tabInactive, fontSize: 12 }}>{d.os || d.deviceClass || d.id.slice(0, 8)}</Text>
              </View>
              {d.id === deviceId && <Ionicons name="checkmark-circle" size={20} color={OK} />}
            </Pressable>
          ))}
        </View>
      )}

      {/* Profile picker */}
      {showProfiles && profiles.length > 0 && (
        <ProfileSheet c={c} current={status?.profile} profiles={profiles} onSelect={selectProfile} busy={busy} />
      )}

      {/* Fail-case banner — disconnected / needs re-auth, controls greyed below */}
      {deviceId && state !== "ok" && (
        <View style={[card, { borderColor: state === "needs-auth" ? WARN : DANGER, backgroundColor: (state === "needs-auth" ? WARN : DANGER) + "14", gap: 8 }]}>
          <View style={{ flexDirection: "row", alignItems: "center", gap: 8 }}>
            <Ionicons name={state === "needs-auth" ? "lock-closed" : "cloud-offline"} size={20} color={state === "needs-auth" ? WARN : DANGER} />
            <Text style={{ color: c.textPrimary, fontWeight: "800", fontSize: 15 }}>
              {state === "needs-auth" ? `${robotDevice?.name || "Robot"} needs re-authorization` : `${robotDevice?.name || "Robot"} is offline`}
            </Text>
          </View>
          <Text style={{ color: c.tabInactive, fontSize: 13 }}>
            {state === "needs-auth"
              ? "The device's agent lost its sign-in (token rotated or signed out). Re-authorize it as your account, then recheck. On the device run: yaver auth — or open Yaver there and sign in."
              : "Can't reach the device over your mesh. Make sure it's powered on, connected, and running the Yaver agent."}
          </Text>
          <View style={{ flexDirection: "row", gap: 8 }}>
            <Pressable onPress={() => refresh(deviceId)} style={{ backgroundColor: c.accent, borderRadius: 10, paddingHorizontal: 16, paddingVertical: 10 }}>
              <Text style={{ color: "#fff", fontWeight: "700" }}>Recheck</Text>
            </Pressable>
            <Pressable onPress={() => setShowPicker(true)} style={{ borderColor: c.borderSubtle, borderWidth: 1, borderRadius: 10, paddingHorizontal: 16, paddingVertical: 10 }}>
              <Text style={{ color: c.textPrimary, fontWeight: "700" }}>Switch device</Text>
            </Pressable>
          </View>
        </View>
      )}

      {/* LIVE camera */}
      <View style={{ borderRadius: 18, overflow: "hidden", backgroundColor: "#000", aspectRatio: 16 / 10, alignItems: "center", justifyContent: "center", opacity: state === "ok" ? 1 : 0.5 }}>
        {frame ? (
          <Image source={{ uri: frame }} style={{ width: "100%", height: "100%" }} resizeMode="contain" />
        ) : (
          <View style={{ alignItems: "center", gap: 8, padding: 20 }}>
            <Ionicons name="videocam-outline" size={34} color="#888" />
            <Text style={{ color: "#aaa", textAlign: "center" }}>{!deviceId ? "Pick a robot device" : !connected ? "Connect to your mesh" : camErr || "starting camera…"}</Text>
          </View>
        )}
        <View style={{ position: "absolute", top: 10, left: 12, flexDirection: "row", alignItems: "center", gap: 6 }}>
          {live && frame && <View style={{ width: 7, height: 7, borderRadius: 4, backgroundColor: DANGER }} />}
          {live && frame && <Text style={{ color: "#fff", fontWeight: "700", fontSize: 12 }}>LIVE</Text>}
        </View>
        <Pressable onPress={() => setLive((l) => !l)} style={{ position: "absolute", top: 8, right: 10, padding: 6 }}>
          <Ionicons name={live ? "pause" : "play"} size={20} color="#fff" />
        </Pressable>
      </View>

      {/* Status strip */}
      <View style={[card, { flexDirection: "row", justifyContent: "space-between", alignItems: "center" }]}>
        <View style={{ flexDirection: "row", flexWrap: "wrap", gap: 16 }}>
          {hasMotion && <Stat label="X" value={pos ? pos.x.toFixed(0) : "—"} c={c} />}
          {hasMotion && <Stat label="Y" value={pos ? pos.y.toFixed(0) : "—"} c={c} />}
          {hasMotion && <Stat label="Z" value={pos ? pos.z.toFixed(0) : "—"} c={c} />}
          {hasMotion && <Stat label="Homed" value={pos?.homed ? "yes" : "no"} good={pos?.homed} c={c} />}
          <Stat label="Cam" value={status?.cameraOk ? "ok" : "—"} good={status?.cameraOk} c={c} />
          {status?.companion && <Stat label="Torque" value={liveTorque != null ? `${liveTorque.toFixed(0)}` : "sensor"} good={status?.companion} c={c} />}
          <Stat label="E-stop" value={status?.estopped ? "TRIP" : "clear"} good={!status?.estopped} c={c} />
        </View>
        <Pressable onPress={() => refresh(deviceId)} hitSlop={10}><Ionicons name="refresh" size={18} color={c.accent} /></Pressable>
      </View>

      {/* Screwdriver (tool/rotate) — also the whole control set in screwdriver-only */}
      {(hasTool || hasRotate) && (
        <ScrewdriverPanel
          c={c}
          disabled={controlsDisabled}
          hasTool={hasTool}
          hasGpio={hasGpio}
          toolOn={toolOn}
          onTool={hasTool ? tool : undefined}
          onRotate={rotate}
          onGpio={hasGpio ? gpio : undefined}
        />
      )}

      {/* Calibration — camera-guided Z touch-off + seat torque (screwdriver cells) */}
      {showCalib && hasTool && (
        <CalibrationPanel
          c={c}
          disabled={controlsDisabled}
          homed={!!pos?.homed}
          currentZ={pos?.z}
          companion={status?.companion}
          liveTorque={liveTorque}
          zEngage={status?.zEngage}
          zSafe={status?.zSafe}
          targetTorqueNmm={status?.targetTorqueNmm}
          onJogZ={(d) => hasMotion && jogZfine(d)}
          onSetEngage={setEngage}
          onSetSafe={setSafe}
          onSetTarget={setTarget}
          onTestScrew={testScrew}
        />
      )}

      {/* Machine setup — Fuju steps/mm + envelope (motion cells, calibrate view) */}
      {showCalib && hasMotion && <MachineSetupPanel c={c} busy={busy} onApply={(patch) => patchConfig(patch)} />}

      {/* Motion: home + jog pad (cartesian profiles only) */}
      {hasMotion && (
        <>
          <Pressable onPress={home} disabled={controlsDisabled} style={[card, { flexDirection: "row", alignItems: "center", justifyContent: "center", gap: 8, opacity: controlsDisabled ? 0.5 : 1 }]}>
            <Ionicons name="home" size={20} color={c.accent} />
            <Text style={{ color: c.accent, fontWeight: "700" }}>Home (G28)</Text>
          </Pressable>
          <View style={card}>
            <View style={{ flexDirection: "row", justifyContent: "space-between", alignItems: "center", marginBottom: 10 }}>
              <View style={{ flexDirection: "row", gap: 8 }}>
                {STEPS.map((s) => (
                  <Pressable key={s} onPress={() => setStep(s)} style={{ paddingHorizontal: 12, paddingVertical: 6, borderRadius: 10, backgroundColor: step === s ? c.accent : "transparent", borderColor: c.borderSubtle, borderWidth: 1 }}>
                    <Text style={{ color: step === s ? "#fff" : c.textPrimary, fontWeight: "700" }}>{s}mm</Text>
                  </Pressable>
                ))}
              </View>
              <Pressable onPress={() => setVerify(verify === "frames" ? "agent" : verify === "agent" ? "off" : "frames")} style={{ paddingHorizontal: 10, paddingVertical: 6, borderRadius: 10, borderColor: c.borderSubtle, borderWidth: 1 }}>
                <Text style={{ color: c.textPrimary, fontSize: 12 }}>verify: {verify}</Text>
              </Pressable>
            </View>
            <View style={{ alignItems: "center", gap: 8 }}>
              <JogBtn label={`Y +${step}`} onPress={() => jog("Y", 1)} c={c} disabled={controlsDisabled} />
              <View style={{ flexDirection: "row", gap: 8 }}>
                <JogBtn label={`X -${step}`} onPress={() => jog("X", -1)} c={c} disabled={controlsDisabled} />
                <JogBtn label={`Z +${step}`} onPress={() => jog("Z", 1)} c={c} disabled={controlsDisabled} accent />
                <JogBtn label={`X +${step}`} onPress={() => jog("X", 1)} c={c} disabled={controlsDisabled} />
              </View>
              <View style={{ flexDirection: "row", gap: 8 }}>
                <JogBtn label={`Y -${step}`} onPress={() => jog("Y", -1)} c={c} disabled={controlsDisabled} />
                <JogBtn label={`Z -${step}`} onPress={() => jog("Z", -1)} c={c} disabled={controlsDisabled} accent />
              </View>
            </View>
          </View>
        </>
      )}

      {/* Teach actions — capture fastening points while recording (jog there first) */}
      {recording && hasMotion && (
        <View style={{ flexDirection: "row", gap: 10 }}>
          <Pressable onPress={markWaypoint} disabled={controlsDisabled} style={[card, { flex: 1, flexDirection: "row", gap: 6, alignItems: "center", justifyContent: "center", opacity: controlsDisabled ? 0.5 : 1 }]}>
            <Ionicons name="location-outline" size={18} color={c.textPrimary} />
            <Text style={{ color: c.textPrimary, fontWeight: "700" }}>Mark waypoint</Text>
          </Pressable>
          {hasTool && (
            <Pressable onPress={addScrewPoint} disabled={controlsDisabled} style={[card, { flex: 1, flexDirection: "row", gap: 6, alignItems: "center", justifyContent: "center", backgroundColor: c.accent + "1A", borderColor: c.accent, opacity: controlsDisabled ? 0.5 : 1 }]}>
              <Ionicons name="add-circle-outline" size={18} color={c.accent} />
              <Text style={{ color: c.accent, fontWeight: "700" }}>Add screw point</Text>
            </Pressable>
          )}
        </View>
      )}

      {/* Klemens array → fastening program (jig grid or rail) */}
      {hasMotion && hasTool && <ArrayPanel c={c} disabled={controlsDisabled} busy={busy} onGenerate={generateArray} onJig={getJig} />}

      {/* Teach & Repeat */}
      <TeachPendant
        c={c}
        recording={recording}
        onToggleRecord={() => setRecording((r) => !r)}
        steps={steps}
        onClear={() => setSteps([])}
        onSave={saveProgram}
        programs={programs}
        onPlay={playProgram}
        onDelete={deleteProgram}
        busy={controlsDisabled}
      />

      {/* Program run result */}
      {runResult && (
        <View style={[card, { borderColor: runResult.ok ? OK : DANGER }]}>
          <Text style={{ color: runResult.ok ? OK : DANGER, fontWeight: "800" }}>
            {runResult.ok ? "Program complete ✓" : "Program halted ✗"} — {runResult.completed ?? 0}/{runResult.total ?? 0} steps
          </Text>
          {!runResult.ok && runResult.error && <Text style={{ color: c.tabInactive, marginTop: 4 }}>{runResult.error}</Text>}
        </View>
      )}

      {/* Machine power (M80/M81) + release motors (M84) */}
      {hasMotion && (
        <View style={card}>
          <View style={{ flexDirection: "row", alignItems: "center", justifyContent: "space-between" }}>
            <Text style={{ color: c.textPrimary, fontWeight: "700" }}>Machine power</Text>
            <View style={{ flexDirection: "row", gap: 8 }}>
              <Pressable onPress={() => power(true)} disabled={controlsDisabled} style={{ paddingHorizontal: 14, paddingVertical: 8, borderRadius: 10, borderColor: OK, borderWidth: 1, opacity: controlsDisabled ? 0.5 : 1 }}>
                <Text style={{ color: OK, fontWeight: "700" }}>On</Text>
              </Pressable>
              <Pressable onPress={() => power(false)} disabled={controlsDisabled} style={{ paddingHorizontal: 14, paddingVertical: 8, borderRadius: 10, borderColor: c.borderSubtle, borderWidth: 1, opacity: controlsDisabled ? 0.5 : 1 }}>
                <Text style={{ color: c.textPrimary, fontWeight: "700" }}>Off</Text>
              </Pressable>
              <Pressable onPress={motorsOff} disabled={controlsDisabled} style={{ paddingHorizontal: 14, paddingVertical: 8, borderRadius: 10, borderColor: c.borderSubtle, borderWidth: 1, opacity: controlsDisabled ? 0.5 : 1 }}>
                <Text style={{ color: c.textPrimary, fontWeight: "700" }}>Release motors</Text>
              </Pressable>
            </View>
          </View>
          <Text style={{ color: c.tabInactive, fontSize: 11, marginTop: 8 }}>
            On/Off uses M80/M81 — works only if the board has PSU-control wiring. For a guaranteed cut, use a smart plug.
          </Text>
        </View>
      )}

      {/* E-STOP — always live */}
      <View style={{ flexDirection: "row", gap: 10 }}>
        <Pressable onPress={estop} style={{ flex: 2, backgroundColor: DANGER, borderRadius: 14, paddingVertical: 18, alignItems: "center" }}>
          <Text style={{ color: "#fff", fontSize: 18, fontWeight: "800", letterSpacing: 1 }}>E-STOP</Text>
        </Pressable>
        <Pressable onPress={reset} style={{ flex: 1, borderColor: c.borderSubtle, borderWidth: 1, borderRadius: 14, paddingVertical: 18, alignItems: "center" }}>
          <Text style={{ color: c.textPrimary, fontWeight: "700" }}>Reset</Text>
        </Pressable>
      </View>

      {busy && (
        <View style={{ flexDirection: "row", gap: 8, alignItems: "center", justifyContent: "center" }}>
          <ActivityIndicator color={c.accent} />
          <Text style={{ color: c.tabInactive }}>working…</Text>
        </View>
      )}

      {last && (
        <View style={card}>
          <Text style={{ color: c.textPrimary, fontSize: 15, fontWeight: "700", marginBottom: 8 }}>Last action — verification</Text>
          {!last.ok && <Text style={{ color: DANGER }}>{last.code ? `[${last.code}] ` : ""}{last.error}</Text>}
          {last.frames?.after && <Image source={{ uri: last.frames.after }} style={{ width: "100%", aspectRatio: 16 / 9, borderRadius: 10, backgroundColor: "#000" }} resizeMode="contain" />}
          {v && v.mode === "agent" && <Badge good={!!v.moved && !v.obstruction} text={v.obstruction ? "OBSTRUCTION — stopped" : v.moved ? `moved ✓ ${Math.round((v.confidence || 0) * 100)}%` : "did not move ✗"} c={c} />}
          {cross && <Badge good={cross.agree} text={`encoder ${cross.agree ? "agrees ✓" : "DISAGREES ✗"} (Δ ${fmtDelta(cross.observedDelta)} vs ${fmtDelta(cross.expectedDelta)})`} c={c} />}
          {last.position && <Text style={{ color: c.tabInactive, marginTop: 8 }}>now X {last.position.x.toFixed(1)} Y {last.position.y.toFixed(1)} Z {last.position.z.toFixed(1)} · {last.tookMs}ms</Text>}
        </View>
      )}
      <View style={{ height: 40 }} />
    </ScrollView>
  );
}

function Stat({ label, value, good, c }: { label: string; value: string; good?: boolean; c: any }) {
  return (
    <View>
      <Text style={{ color: c.tabInactive, fontSize: 11 }}>{label}</Text>
      <Text style={{ color: good === undefined ? c.textPrimary : good ? OK : DANGER, fontSize: 15, fontWeight: "700" }}>{value}</Text>
    </View>
  );
}
function JogBtn({ label, onPress, c, disabled, accent }: { label: string; onPress: () => void; c: any; disabled?: boolean; accent?: boolean }) {
  return (
    <Pressable onPress={onPress} disabled={disabled} style={{ width: 88, height: 54, borderRadius: 12, alignItems: "center", justifyContent: "center", backgroundColor: accent ? c.accent + "1A" : (c.bgCard ?? c.bg), borderColor: accent ? c.accent : c.borderSubtle, borderWidth: 1, opacity: disabled ? 0.5 : 1 }}>
      <Text style={{ color: accent ? c.accent : c.textPrimary, fontWeight: "700" }}>{label}</Text>
    </Pressable>
  );
}
function Badge({ text, good, c }: { text: string; good: boolean; c: any }) {
  return (
    <View style={{ marginTop: 8, alignSelf: "flex-start", backgroundColor: (good ? OK : DANGER) + "22", borderColor: good ? OK : DANGER, borderWidth: 1, borderRadius: 8, paddingHorizontal: 10, paddingVertical: 6 }}>
      <Text style={{ color: good ? OK : DANGER, fontWeight: "700" }}>{text}</Text>
    </View>
  );
}
function fmtDelta(d?: Record<string, number>): string {
  if (!d) return "—";
  return Object.entries(d).map(([k, val]) => `${k}${val >= 0 ? "+" : ""}${val}`).join(" ");
}
