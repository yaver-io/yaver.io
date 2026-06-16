// Robotic Arm — Yaver as the single management layer for any multi-DOF arm
// (Fairino / Elephant myCobot / PAROL6 / generic) over the mesh. DOF + joints
// come from arm_describe, so this ONE screen renders N joint sliders for any
// robot — nothing hardcoded. Jog / MoveJ / home / e-stop, hand-guide learning
// mode (free-drive → capture → save → repeat), and the box's shared camera
// (also visible to the host coding agent via the robot_camera MCP tool).
import React, { useCallback, useEffect, useRef, useState } from "react";
import { ActivityIndicator, Image, Pressable, ScrollView, Text, TextInput, View } from "react-native";
import { useRouter } from "expo-router";
import { AppScreenHeader } from "../src/components/AppScreenHeader";
import { useColors } from "../src/context/ThemeContext";
import { useDevice } from "../src/context/DeviceContext";
import {
  armClient,
  getArmDeviceId,
  setArmDeviceId,
  type ArmConfig,
  type ArmInfo,
  type ArmProgram,
  type ArmStatus,
  type ArmTarget,
  type JointState,
  type RobotModel,
  type VerifyMode,
  type Waypoint,
} from "../src/lib/armClient";

const STEPS = [1, 5, 15, 45];
const DRIVERS = ["sim", "fairino", "mycobot", "parol6", "generic_tcp", "generic_serial", "bridge"];

export default function ArmScreen() {
  const c = useColors();
  const router = useRouter();
  const deviceCtx = useDevice();
  const devices = (deviceCtx as any).devices as any[];

  const [deviceId, setDeviceId] = useState("");
  const [info, setInfo] = useState<ArmInfo | null>(null);
  const [status, setStatus] = useState<ArmStatus | null>(null);
  const [config, setConfig] = useState<ArmConfig | null>(null);
  const [joints, setJoints] = useState<JointState[]>([]);
  const [frame, setFrame] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState<string | null>(null);
  const [step, setStep] = useState(15);
  const [vel, setVel] = useState(30);
  const [verify, setVerify] = useState<VerifyMode>("frames");
  const [freedrive, setFreedrive] = useState(false);
  const [waypoints, setWaypoints] = useState<Waypoint[]>([]);
  const [progName, setProgName] = useState("");
  const [programs, setPrograms] = useState<ArmProgram[]>([]);
  const [models, setModels] = useState<RobotModel[]>([]);
  const [showSetup, setShowSetup] = useState(false);
  const liveRef = useRef(true);

  const target = useCallback((): ArmTarget | undefined => {
    const d = devices?.find((x) => x.id === deviceId || x.deviceId === deviceId);
    if (!deviceId) return undefined;
    return { id: deviceId, lanIps: d?.lanIps, host: d?.host, port: 18080 };
  }, [deviceId, devices]);

  useEffect(() => {
    getArmDeviceId().then((id) => id && setDeviceId(id));
  }, []);

  const refresh = useCallback(async () => {
    const t = target();
    if (!t) return;
    try {
      const s = await armClient.status(t);
      if (s?.status) {
        setStatus(s.status);
        if (s.status.joints) setJoints(s.status.joints);
      }
      if (s?.info) setInfo(s.info);
    } catch (e: any) {
      setMsg(String(e?.message || e));
    }
  }, [target]);

  // initial load on device pick
  useEffect(() => {
    if (!deviceId) return;
    setArmDeviceId(deviceId);
    const t = target();
    if (!t) return;
    (async () => {
      try {
        const cfg = await armClient.configGet(t);
        if (cfg?.config) setConfig(cfg.config);
        const d = await armClient.describe(t);
        if (d?.info) setInfo(d.info);
        const pl = await armClient.programList(t);
        if (pl?.programs) setPrograms(pl.programs);
        const m = await armClient.models(t);
        if (m?.models) setModels(m.models);
      } catch {}
      refresh();
    })();
  }, [deviceId]); // eslint-disable-line

  // camera + state poll
  useEffect(() => {
    liveRef.current = true;
    const t = target();
    if (!t) return;
    let stop = false;
    const loop = async () => {
      while (!stop && liveRef.current) {
        try {
          const snap = await armClient.snapshot(t);
          if (snap?.image) setFrame(snap.image);
        } catch {}
        try {
          const st = await armClient.state(t);
          if (st?.joints) setJoints(st.joints);
        } catch {}
        await new Promise((r) => setTimeout(r, 900));
      }
    };
    loop();
    return () => {
      stop = true;
      liveRef.current = false;
    };
  }, [deviceId]); // eslint-disable-line

  const run = useCallback(
    async (fn: () => Promise<any>) => {
      setBusy(true);
      setMsg(null);
      try {
        const r = await fn();
        if (r?.ok === false) setMsg(r.error || r.code || "failed");
        await refresh();
        return r;
      } catch (e: any) {
        setMsg(String(e?.message || e));
      } finally {
        setBusy(false);
      }
    },
    [refresh],
  );

  const jog = (joint: string, dir: 1 | -1) => {
    const t = target();
    if (t) run(() => armClient.jog(t, joint, dir * step, vel, verify));
  };

  const toggleFreedrive = async () => {
    const t = target();
    if (!t) return;
    const next = !freedrive;
    const r = await run(() => armClient.freedrive(t, next));
    if (r?.ok !== false) setFreedrive(next);
  };

  const capture = async () => {
    const t = target();
    if (!t) return;
    const r = await armClient.teachCapture(t, `wp${waypoints.length + 1}`, vel);
    if (r?.waypoint) setWaypoints((w) => [...w, { ...r.waypoint, velPct: vel, verify }]);
  };

  const saveProgram = async () => {
    const t = target();
    if (!t || !progName.trim() || waypoints.length === 0) return;
    await run(() => armClient.programSave(t, { name: progName.trim(), waypoints }));
    const pl = await armClient.programList(t);
    if (pl?.programs) setPrograms(pl.programs);
    setWaypoints([]);
  };

  // ---------- styles ----------
  const card = { backgroundColor: c.bgCard, borderColor: c.borderSubtle, borderWidth: 1, borderRadius: 12, padding: 14, marginBottom: 12 };
  const btn = (bg: string) => ({ backgroundColor: bg, paddingVertical: 10, paddingHorizontal: 14, borderRadius: 10, alignItems: "center" as const });
  const label = { color: c.textSecondary, fontSize: 12, marginBottom: 6 };
  const h = { color: c.textPrimary, fontSize: 15, fontWeight: "700" as const, marginBottom: 10 };

  if (!deviceId) {
    return (
      <View style={{ flex: 1, backgroundColor: c.bg }}>
        <AppScreenHeader title="Robotic Arm" onBack={() => router.back()} />
        <ScrollView contentContainerStyle={{ padding: 16 }}>
          <Text style={[h, { marginBottom: 14 }]}>Pick the device the arm is wired to</Text>
          {(devices || []).map((d) => (
            <Pressable key={d.id || d.deviceId} onPress={() => setDeviceId(d.id || d.deviceId)} style={[card, { flexDirection: "row", justifyContent: "space-between" }]}>
              <Text style={{ color: c.textPrimary, fontWeight: "600" }}>{d.name || d.alias || d.id || d.deviceId}</Text>
              <Text style={{ color: c.textMuted }}>{d.online ? "online" : "offline"}</Text>
            </Pressable>
          ))}
          {(!devices || devices.length === 0) && <Text style={{ color: c.textMuted }}>No devices yet. Sign a box in first.</Text>}
        </ScrollView>
      </View>
    );
  }

  return (
    <View style={{ flex: 1, backgroundColor: c.bg }}>
      <AppScreenHeader title="Robotic Arm" onBack={() => router.back()} />
      <ScrollView contentContainerStyle={{ padding: 16 }}>
        {/* identity + status */}
        <View style={[card, { flexDirection: "row", justifyContent: "space-between", alignItems: "center" }]}>
          <View style={{ flex: 1 }}>
            <View style={{ flexDirection: "row", alignItems: "center", gap: 6 }}>
              <Text style={{ color: c.textPrimary, fontWeight: "700" }}>
                {info?.vendor ? `${info.vendor} ${info?.model || ""}` : config?.driver || "arm"} · {info?.dof ?? "?"} DOF
              </Text>
              {config?.driver === "sim" && (
                <View style={{ backgroundColor: c.accent, borderRadius: 6, paddingHorizontal: 6, paddingVertical: 2 }}>
                  <Text style={{ color: c.textInverse, fontSize: 10, fontWeight: "800" }}>SIM</Text>
                </View>
              )}
            </View>
            <Text style={{ color: c.textMuted, fontSize: 12 }}>
              {config?.driver === "sim"
                ? `simulator · ${config?.sim?.engine || "pybullet"}`
                : status?.connected
                  ? status?.estopped
                    ? "E-STOPPED"
                    : status?.enabled
                      ? "enabled"
                      : "ready"
                  : "offline"}
              {info?.source ? ` · ${info.source}` : ""}
            </Text>
          </View>
          <Pressable onPress={() => setShowSetup((v) => !v)} style={btn(c.bgCardElevated)}>
            <Text style={{ color: c.textPrimary }}>Setup</Text>
          </Pressable>
        </View>

        {showSetup && (
          <View style={card}>
            {models.length > 0 && (
              <>
                <Text style={h}>Your robot model</Text>
                <Text style={[label, { marginTop: -4, marginBottom: 8 }]}>Pick one to prefill DOF + joints</Text>
                <ScrollView horizontal showsHorizontalScrollIndicator={false} style={{ marginBottom: 12 }}>
                  <View style={{ flexDirection: "row", gap: 8 }}>
                    {models.map((m) => {
                      const isSim = m.driver === "sim";
                      const picked = isSim
                        ? config?.driver === "sim" && config?.sim?.model === m.simSource
                        : config?.driver === m.driver && config?.info?.model === m.info.model;
                      return (
                        <Pressable
                          key={`${m.vendor}-${m.model}`}
                          onPress={() =>
                            setConfig((cfg) => ({
                              ...(cfg || { info: { dof: 0, joints: [] } }),
                              driver: m.driver,
                              info: m.info,
                              addr: isSim ? cfg?.addr || "" : cfg?.addr || (m.driver === "fairino" ? "192.168.58.2" : cfg?.addr || ""),
                              ...(isSim ? { sim: { ...(cfg?.sim || {}), engine: "pybullet", model: m.simSource } } : {}),
                            }))
                          }
                          style={[btn(picked ? c.accent : c.bgCardElevated), { minWidth: 120, borderWidth: isSim ? 1 : 0, borderColor: c.accent }]}
                        >
                          <Text style={{ color: picked ? c.textInverse : c.textPrimary, fontWeight: "700", fontSize: 12 }}>
                            {isSim ? "🖥 " : ""}{m.model}
                          </Text>
                          <Text style={{ color: picked ? c.textInverse : c.textMuted, fontSize: 10 }}>
                            {m.vendor} · {m.info.dof}DOF{m.payloadKg ? ` · ${m.payloadKg}kg` : ""}
                          </Text>
                        </Pressable>
                      );
                    })}
                  </View>
                </ScrollView>
              </>
            )}
            <Text style={h}>Driver</Text>
            <View style={{ flexDirection: "row", flexWrap: "wrap", gap: 8 }}>
              {DRIVERS.map((dv) => (
                <Pressable
                  key={dv}
                  onPress={() => setConfig((cfg) => ({ ...(cfg || { info: { dof: 0, joints: [] } }), driver: dv }))}
                  style={btn(config?.driver === dv ? c.accent : c.bgCardElevated)}
                >
                  <Text style={{ color: config?.driver === dv ? c.textInverse : c.textPrimary, fontSize: 12 }}>{dv}</Text>
                </Pressable>
              ))}
            </View>
            <Text style={[label, { marginTop: 10 }]}>Address (ip / ip:port / /dev/tty… / bridge URL)</Text>
            <TextInput
              value={config?.addr || ""}
              onChangeText={(v) => setConfig((cfg) => ({ ...(cfg || { info: { dof: 0, joints: [] } }), addr: v }))}
              placeholder="192.168.58.2"
              placeholderTextColor={c.textMuted}
              autoCapitalize="none"
              style={{ color: c.textPrimary, backgroundColor: c.bgInput, borderRadius: 8, padding: 10, borderWidth: 1, borderColor: c.border }}
            />
            <Text style={[label, { marginTop: 10 }]}>Camera (external / http(s)://snapshot / /dev/video0)</Text>
            <TextInput
              value={config?.camera || ""}
              onChangeText={(v) => setConfig((cfg) => ({ ...(cfg || { info: { dof: 0, joints: [] } }), camera: v }))}
              placeholder="external"
              placeholderTextColor={c.textMuted}
              autoCapitalize="none"
              style={{ color: c.textPrimary, backgroundColor: c.bgInput, borderRadius: 8, padding: 10, borderWidth: 1, borderColor: c.border }}
            />
            <Pressable
              onPress={async () => {
                const t = target();
                if (!t || !config) return;
                await run(() => armClient.configSet(t, config));
                const d = await armClient.describe(t);
                if (d?.info) setInfo(d.info);
                setShowSetup(false);
              }}
              style={[btn(c.accent), { marginTop: 12 }]}
            >
              <Text style={{ color: c.textInverse, fontWeight: "700" }}>Save & connect</Text>
            </Pressable>
          </View>
        )}

        {/* camera */}
        <View style={[card, { padding: 0, overflow: "hidden", aspectRatio: 4 / 3, backgroundColor: "#000", justifyContent: "center" }]}>
          {frame ? (
            <Image source={{ uri: frame }} style={{ width: "100%", height: "100%" }} resizeMode="contain" />
          ) : (
            <Text style={{ color: "#888", textAlign: "center" }}>{status?.cameraOk === false ? "no camera configured" : "waiting for camera…"}</Text>
          )}
        </View>

        {/* speed + step + verify */}
        <View style={[card, { flexDirection: "row", justifyContent: "space-between" }]}>
          <View>
            <Text style={label}>Step (°)</Text>
            <View style={{ flexDirection: "row", gap: 6 }}>
              {STEPS.map((s) => (
                <Pressable key={s} onPress={() => setStep(s)} style={btn(step === s ? c.accent : c.bgCardElevated)}>
                  <Text style={{ color: step === s ? c.textInverse : c.textPrimary }}>{s}</Text>
                </Pressable>
              ))}
            </View>
          </View>
          <View>
            <Text style={label}>Speed {vel}%</Text>
            <View style={{ flexDirection: "row", gap: 6 }}>
              {[10, 30, 60, 100].map((s) => (
                <Pressable key={s} onPress={() => setVel(s)} style={btn(vel === s ? c.accent : c.bgCardElevated)}>
                  <Text style={{ color: vel === s ? c.textInverse : c.textPrimary }}>{s}</Text>
                </Pressable>
              ))}
            </View>
          </View>
        </View>

        {/* joints — data-driven from arm_describe */}
        <View style={card}>
          <Text style={h}>Joints</Text>
          {(info?.joints || []).map((j) => {
            const cur = joints.find((x) => x.name.toLowerCase() === j.name.toLowerCase());
            return (
              <View key={j.name} style={{ flexDirection: "row", alignItems: "center", marginBottom: 10 }}>
                <Text style={{ width: 44, color: c.textPrimary, fontWeight: "700" }}>{j.name}</Text>
                <Text style={{ flex: 1, color: c.textSecondary }}>
                  {cur ? cur.position.toFixed(1) : "—"} {j.unit || "deg"}
                  <Text style={{ color: c.textMuted, fontSize: 11 }}>  [{j.min}…{j.max}]</Text>
                </Text>
                <Pressable disabled={busy} onPress={() => jog(j.name, -1)} style={[btn(c.bgCardElevated), { marginRight: 6 }]}>
                  <Text style={{ color: c.textPrimary, fontWeight: "700" }}>−</Text>
                </Pressable>
                <Pressable disabled={busy} onPress={() => jog(j.name, 1)} style={btn(c.bgCardElevated)}>
                  <Text style={{ color: c.textPrimary, fontWeight: "700" }}>+</Text>
                </Pressable>
              </View>
            );
          })}
          {(!info?.joints || info.joints.length === 0) && <Text style={{ color: c.textMuted }}>No joints — open Setup, pick a driver, and Save & connect.</Text>}
        </View>

        {/* primary controls */}
        <View style={[card, { flexDirection: "row", flexWrap: "wrap", gap: 8 }]}>
          <Pressable disabled={busy} onPress={() => { const t = target(); if (t) run(() => armClient.enable(t, !(status?.enabled))); }} style={btn(c.bgCardElevated)}>
            <Text style={{ color: c.textPrimary }}>{status?.enabled ? "Disable" : "Enable"}</Text>
          </Pressable>
          <Pressable disabled={busy} onPress={() => { const t = target(); if (t) run(() => armClient.home(t, vel, verify)); }} style={btn(c.bgCardElevated)}>
            <Text style={{ color: c.textPrimary }}>Home</Text>
          </Pressable>
          <Pressable disabled={busy} onPress={() => { const t = target(); if (t) run(() => armClient.stop(t)); }} style={btn(c.warn)}>
            <Text style={{ color: "#000" }}>Stop</Text>
          </Pressable>
          <Pressable disabled={busy} onPress={() => { const t = target(); if (t) run(() => armClient.estop(t)); }} style={btn(c.error)}>
            <Text style={{ color: "#fff", fontWeight: "700" }}>E-STOP</Text>
          </Pressable>
          {status?.estopped && (
            <Pressable disabled={busy} onPress={() => { const t = target(); if (t) run(() => armClient.reset(t)); }} style={btn(c.accent)}>
              <Text style={{ color: c.textInverse }}>Reset</Text>
            </Pressable>
          )}
          {config?.driver === "sim" && (
            <Pressable disabled={busy} onPress={() => { const t = target(); if (t) run(() => armClient.simReset(t)); }} style={btn(c.bgCardElevated)}>
              <Text style={{ color: c.textPrimary }}>Reset sim</Text>
            </Pressable>
          )}
        </View>

        {/* learning mode: hand-guide → capture → save → repeat */}
        <View style={card}>
          <Text style={h}>Learning mode (hand-guide & repeat)</Text>
          <View style={{ flexDirection: "row", gap: 8, marginBottom: 10 }}>
            <Pressable disabled={busy} onPress={toggleFreedrive} style={btn(freedrive ? c.accent : c.bgCardElevated)}>
              <Text style={{ color: freedrive ? c.textInverse : c.textPrimary }}>{freedrive ? "Free-drive ON" : "Free-drive"}</Text>
            </Pressable>
            <Pressable disabled={busy} onPress={capture} style={btn(c.bgCardElevated)}>
              <Text style={{ color: c.textPrimary }}>Capture waypoint ({waypoints.length})</Text>
            </Pressable>
          </View>
          <View style={{ flexDirection: "row", gap: 8 }}>
            <TextInput
              value={progName}
              onChangeText={setProgName}
              placeholder="program name"
              placeholderTextColor={c.textMuted}
              autoCapitalize="none"
              style={{ flex: 1, color: c.textPrimary, backgroundColor: c.bgInput, borderRadius: 8, padding: 10, borderWidth: 1, borderColor: c.border }}
            />
            <Pressable disabled={busy || !progName.trim() || waypoints.length === 0} onPress={saveProgram} style={btn(c.accent)}>
              <Text style={{ color: c.textInverse }}>Save</Text>
            </Pressable>
          </View>
          {programs.length > 0 && (
            <View style={{ marginTop: 12 }}>
              <Text style={label}>Saved programs (tap to repeat)</Text>
              {programs.map((p) => (
                <View key={p.name} style={{ flexDirection: "row", justifyContent: "space-between", alignItems: "center", paddingVertical: 6 }}>
                  <Text style={{ color: c.textPrimary }}>{p.name} · {p.waypoints?.length || 0} pts</Text>
                  <Pressable disabled={busy} onPress={() => { const t = target(); if (t) run(() => armClient.programRun(t, p.name, verify)); }} style={btn(c.bgCardElevated)}>
                    <Text style={{ color: c.textPrimary }}>▶ Repeat</Text>
                  </Pressable>
                </View>
              ))}
            </View>
          )}
        </View>

        {busy && <ActivityIndicator color={c.accent} style={{ marginVertical: 8 }} />}
        {msg && <Text style={{ color: c.error, textAlign: "center", marginBottom: 12 }}>{msg}</Text>}
        <Pressable onPress={() => setDeviceId("")} style={[btn(c.bgCardElevated), { marginBottom: 30 }]}>
          <Text style={{ color: c.textMuted }}>Switch device</Text>
        </Pressable>
      </ScrollView>
    </View>
  );
}
