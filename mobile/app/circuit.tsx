// Circuit Simulator — Yaver as the single management layer for an electrical
// circuit cell. Import a SPICE netlist, a KiCad export, or an EPLAN/harness
// connection list; simulate with the dependency-free built-in MNA engine (or
// ngspice on the box) across op/dc/tran/ac; run a generic ERC; and SEE the
// waveform PNG (the same image the host coding agent gets via circuit_plot).
// Transport mirrors the printer/arm cells: LAN-first, relay fallback, your
// bearer. Netlists stay on the box vault — never on Convex.
import React, { useCallback, useEffect, useRef, useState } from "react";
import { ActivityIndicator, Image, Pressable, ScrollView, StyleSheet, Text, TextInput, View } from "react-native";
import { useRouter } from "expo-router";
import { AppScreenHeader } from "../src/components/AppScreenHeader";
import { useColors } from "../src/context/ThemeContext";
import { useDevice } from "../src/context/DeviceContext";
import {
  circuitClient,
  getCircuitDeviceId,
  setCircuitDeviceId,
  type Analysis,
  type CircuitInfo,
  type CircuitTarget,
  type EngineCap,
  type ERCReport,
  type SimResult,
} from "../src/lib/circuitClient";

const ACCENT = "#4f9cf9";
const ANALYSES: Array<Analysis["type"]> = ["op", "tran", "ac", "dc"];

const EXAMPLE = `* RC low-pass example
V1 in 0 DC 0 AC 1 SIN(0 5 1k)
R1 in out 1k
C1 out 0 100n
.end`;

export default function CircuitScreen() {
  const c = useColors();
  const router = useRouter();
  const deviceCtx = useDevice();
  const devices = (deviceCtx as any).devices as any[];

  const [deviceId, setDeviceId] = useState("");
  const [engines, setEngines] = useState<EngineCap[]>([]);
  const [engine, setEngine] = useState("auto");
  const [info, setInfo] = useState<CircuitInfo | null>(null);
  const [netlist, setNetlist] = useState(EXAMPLE);
  const [analysis, setAnalysis] = useState<Analysis["type"]>("tran");
  const [plot, setPlot] = useState<string | null>(null);
  const [sim, setSim] = useState<SimResult | null>(null);
  const [erc, setErc] = useState<ERCReport | null>(null);
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState<string | null>(null);

  const liveRef = useRef(true);

  const target = useCallback((): CircuitTarget | undefined => {
    if (!deviceId) return undefined;
    const d = devices?.find((x) => x.id === deviceId || x.deviceId === deviceId);
    return { id: deviceId, lanIps: d?.lanIps, host: d?.host, port: 18080 };
  }, [deviceId, devices]);

  useEffect(() => {
    getCircuitDeviceId().then((id) => id && setDeviceId(id));
    return () => {
      liveRef.current = false;
    };
  }, []);

  const load = useCallback(async () => {
    const t = target();
    if (!t) return;
    const e = await circuitClient.engines(t);
    if (e?.engines) setEngines(e.engines);
    if (e?.active) setEngine(e.active);
    const cfg = await circuitClient.configGet(t);
    if (cfg?.engine) setEngine(cfg.engine);
    if (cfg?.info) setInfo(cfg.info);
    if ((cfg?.info?.elementCount ?? 0) > 0) {
      const ex = await circuitClient.exportNetlist(t, "spice");
      if (ex?.spice && ex.spice.trim()) setNetlist(ex.spice);
    }
  }, [target]);

  useEffect(() => {
    if (deviceId) load();
  }, [deviceId, load]);

  const pickDevice = useCallback(async (id: string) => {
    setDeviceId(id);
    await setCircuitDeviceId(id);
  }, []);

  const analysisPayload = useCallback((): Analysis => {
    switch (analysis) {
      case "tran":
        return { type: "tran", tstop: 5e-3 };
      case "ac":
        return { type: "ac", fstart: 1, fstop: 1e5, points: 30 };
      case "dc":
        return { type: "dc", sweepSrc: "V1", sweepStart: 0, sweepStop: 5, sweepStep: 0.1 };
      default:
        return { type: "op" };
    }
  }, [analysis]);

  const doImport = useCallback(async () => {
    const t = target();
    if (!t) return setMsg("pick a device first");
    setBusy(true);
    setMsg(null);
    const r = await circuitClient.importNetlist(t, netlist, "auto");
    setBusy(false);
    if ((r as any)?.ok === false) return setMsg((r as any).error || "import failed");
    if (r?.info) {
      setInfo(r.info);
      setMsg(`Imported ${r.info.elementCount} elements · ${r.info.nodeCount} nets`);
    }
  }, [target, netlist]);

  const doSimulate = useCallback(async () => {
    const t = target();
    if (!t) return setMsg("pick a device first");
    setBusy(true);
    setMsg(null);
    setPlot(null);
    if (analysis === "op") {
      const r = await circuitClient.measure(t);
      setBusy(false);
      if ((r as any)?.ok === false) return setMsg((r as any).error || "sim failed");
      setSim({ analysis: "op", signals: [], samples: [], nodeVoltages: r.nodeVoltages, engine: r.engine });
      return;
    }
    const r = await circuitClient.simulate(t, analysisPayload());
    if (r?.result) setSim(r.result);
    const p = await circuitClient.plot(t, analysisPayload());
    setBusy(false);
    if ((p as any)?.ok === false) return setMsg((p as any).error || "plot failed");
    if (p?.image) setPlot(p.image);
  }, [target, analysis, analysisPayload]);

  const doErc = useCallback(async () => {
    const t = target();
    if (!t) return setMsg("pick a device first");
    setBusy(true);
    setMsg(null);
    const r = await circuitClient.erc(t);
    setBusy(false);
    if (r?.report) setErc(r.report);
  }, [target]);

  const setEng = useCallback(
    async (eng: string) => {
      setEngine(eng);
      const t = target();
      if (t) await circuitClient.configSet(t, { engine: eng });
    },
    [target],
  );

  const s = makeStyles(c);

  return (
    <View style={{ flex: 1, backgroundColor: c.bg }}>
      <AppScreenHeader title="Circuit Simulator" onBack={() => router.back()} />
      <ScrollView contentContainerStyle={{ padding: 16, paddingBottom: 48 }}>
        {/* device picker */}
        <Text style={s.label}>Device</Text>
        <ScrollView horizontal showsHorizontalScrollIndicator={false} style={{ marginBottom: 12 }}>
          {(devices || []).map((d) => {
            const id = d.id || d.deviceId;
            const sel = id === deviceId;
            return (
              <Pressable key={id} onPress={() => pickDevice(id)} style={[s.chip, sel && { backgroundColor: ACCENT, borderColor: ACCENT }]}>
                <Text style={[s.chipText, sel && { color: "#fff" }]}>{d.name || id}</Text>
              </Pressable>
            );
          })}
          {(devices || []).length === 0 && <Text style={s.muted}>No devices online.</Text>}
        </ScrollView>

        {deviceId ? (
          <>
            {/* engine */}
            <Text style={s.label}>Engine</Text>
            <View style={s.row}>
              {[...engines.map((e) => e.engine), "auto"].filter((v, i, a) => a.indexOf(v) === i).map((eng) => {
                const cap = engines.find((e) => e.engine === eng);
                const disabled = cap ? !cap.available : false;
                const sel = engine === eng;
                return (
                  <Pressable
                    key={eng}
                    disabled={disabled}
                    onPress={() => setEng(eng)}
                    style={[s.btn, sel && { backgroundColor: ACCENT }, disabled && { opacity: 0.4 }]}
                  >
                    <Text style={[s.btnText, sel && { color: "#fff" }, disabled && { textDecorationLine: "line-through" }]}>{eng}</Text>
                  </Pressable>
                );
              })}
            </View>

            {/* netlist editor */}
            <Text style={s.label}>Netlist (SPICE / KiCad / EPLAN)</Text>
            <TextInput
              value={netlist}
              onChangeText={setNetlist}
              multiline
              autoCapitalize="none"
              autoCorrect={false}
              style={s.code}
            />
            <View style={s.row}>
              <Pressable onPress={doImport} disabled={busy} style={[s.btn, { backgroundColor: "#2d6fd6" }]}>
                <Text style={[s.btnText, { color: "#fff" }]}>Import</Text>
              </Pressable>
              {info && (
                <Text style={[s.muted, { flex: 1, marginLeft: 8 }]} numberOfLines={2}>
                  {info.elementCount} el · {info.nodeCount} nets · {info.hasGround ? "gnd" : "NO GND"} · {info.simulatable ? "sim" : "ERC-only"}
                </Text>
              )}
            </View>

            {/* analysis */}
            <Text style={s.label}>Analysis</Text>
            <View style={s.row}>
              {ANALYSES.map((a) => (
                <Pressable key={a} onPress={() => setAnalysis(a)} style={[s.btn, analysis === a && { backgroundColor: ACCENT }]}>
                  <Text style={[s.btnText, analysis === a && { color: "#fff" }]}>{String(a).toUpperCase()}</Text>
                </Pressable>
              ))}
            </View>
            <View style={[s.row, { marginTop: 8 }]}>
              <Pressable onPress={doSimulate} disabled={busy} style={[s.btn, { backgroundColor: "#1f9d55", flex: 1 }]}>
                <Text style={[s.btnText, { color: "#fff", textAlign: "center" }]}>{busy ? "Running…" : "Simulate"}</Text>
              </Pressable>
              <Pressable onPress={doErc} disabled={busy} style={[s.btn, { backgroundColor: "#b7791f" }]}>
                <Text style={[s.btnText, { color: "#fff" }]}>ERC</Text>
              </Pressable>
            </View>

            {busy && <ActivityIndicator color={ACCENT} style={{ marginTop: 12 }} />}
            {msg && <Text style={[s.muted, { marginTop: 10 }]}>{msg}</Text>}

            {plot && <Image source={{ uri: plot }} style={s.plot} resizeMode="contain" />}

            {sim?.nodeVoltages && (
              <View style={s.card}>
                <Text style={s.cardTitle}>Operating point ({sim.engine})</Text>
                {Object.entries(sim.nodeVoltages).map(([n, v]) => (
                  <View key={n} style={s.kv}>
                    <Text style={s.kvKey}>V({n})</Text>
                    <Text style={s.kvVal}>{fmt(v)} V</Text>
                  </View>
                ))}
              </View>
            )}

            {erc && (
              <View style={s.card}>
                <Text style={s.cardTitle}>
                  ERC — {erc.ok ? "✅ pass" : `❌ ${erc.errors} errors`}
                  {erc.warnings > 0 ? `, ${erc.warnings} warn` : ""}
                </Text>
                {(erc.findings || []).map((f, i) => (
                  <Text key={i} style={[s.finding, { color: sevColor(f.severity) }]}>
                    {sevIcon(f.severity)} [{f.rule}] {f.message}
                  </Text>
                ))}
                {(erc.findings || []).length === 0 && <Text style={s.muted}>No findings.</Text>}
              </View>
            )}
          </>
        ) : (
          <Text style={s.muted}>Pick a device running the Yaver agent to design & simulate circuits.</Text>
        )}
      </ScrollView>
    </View>
  );
}

function fmt(v: number) {
  if (Math.abs(v) >= 1000 || (Math.abs(v) < 1e-3 && v !== 0)) return v.toExponential(3);
  return v.toFixed(4);
}
function sevColor(sev: string) {
  return sev === "error" ? "#e25555" : sev === "warning" ? "#d6a032" : "#4f9cf9";
}
function sevIcon(sev: string) {
  return sev === "error" ? "✖" : sev === "warning" ? "▲" : "ℹ";
}

function makeStyles(c: any) {
  return StyleSheet.create({
    label: { color: c.textMuted, fontSize: 13, fontWeight: "600", marginBottom: 6, marginTop: 4 },
    muted: { color: c.textMuted, fontSize: 13 },
    row: { flexDirection: "row", flexWrap: "wrap", alignItems: "center", gap: 8 },
    chip: { borderWidth: 1, borderColor: c.border, borderRadius: 18, paddingHorizontal: 14, paddingVertical: 7, marginRight: 8, backgroundColor: c.bgCard },
    chipText: { color: c.textPrimary, fontSize: 13, fontWeight: "600" },
    btn: { borderRadius: 8, paddingHorizontal: 14, paddingVertical: 8, backgroundColor: c.bgCard, borderWidth: 1, borderColor: c.border },
    btnText: { color: c.textPrimary, fontSize: 13, fontWeight: "600" },
    code: {
      backgroundColor: "#0c0f16",
      color: "#9be3a8",
      fontFamily: "Menlo",
      fontSize: 12,
      borderRadius: 8,
      padding: 10,
      minHeight: 150,
      textAlignVertical: "top",
      borderWidth: 1,
      borderColor: c.border,
      marginBottom: 8,
    },
    plot: { width: "100%", height: 230, marginTop: 14, borderRadius: 8, backgroundColor: "#12141a" },
    card: { backgroundColor: c.bgCard, borderRadius: 10, borderWidth: 1, borderColor: c.border, padding: 12, marginTop: 14 },
    cardTitle: { color: c.textPrimary, fontWeight: "700", fontSize: 14, marginBottom: 8 },
    kv: { flexDirection: "row", justifyContent: "space-between", paddingVertical: 2 },
    kvKey: { color: c.textMuted, fontFamily: "Menlo", fontSize: 12 },
    kvVal: { color: "#9be3a8", fontFamily: "Menlo", fontSize: 12 },
    finding: { fontSize: 12, marginBottom: 4, lineHeight: 16 },
  });
}
