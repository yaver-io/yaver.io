// Circuit Simulator — Yaver as the single management layer for an electrical
// circuit cell. Import a SPICE netlist, a KiCad export, or an EPLAN/harness
// connection list; simulate with the dependency-free built-in MNA engine (or
// ngspice on the box) across op/dc/tran/ac; run a generic ERC; and SEE the
// waveform PNG (the same image the host coding agent gets via circuit_plot).
// Transport mirrors the printer/arm cells: LAN-first, relay fallback, your
// bearer. Netlists stay on the box vault — never on Convex.
import React, { useCallback, useEffect, useRef, useState } from "react";
import { ActivityIndicator, Pressable, ScrollView, StyleSheet, Text, TextInput, View } from "react-native";
import { useRouter } from "expo-router";
import { AppScreenHeader } from "../src/components/AppScreenHeader";
import { WaveformChart } from "../src/components/WaveformChart";
import { useColors } from "../src/context/ThemeContext";
import { useDevice } from "../src/context/DeviceContext";
import {
  circuitClient,
  getCircuitDeviceId,
  setCircuitDeviceId,
  type Analysis,
  type CircuitDesignSummary,
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
  const [design, setDesign] = useState(""); // "" = default slot
  const [designs, setDesigns] = useState<CircuitDesignSummary[]>([]);
  const [newDesign, setNewDesign] = useState("");
  const [engines, setEngines] = useState<EngineCap[]>([]);
  const [engine, setEngine] = useState("auto");
  const [info, setInfo] = useState<CircuitInfo | null>(null);
  const [netlist, setNetlist] = useState(EXAMPLE);
  const [analysis, setAnalysis] = useState<Analysis["type"]>("tran");
  const [sim, setSim] = useState<SimResult | null>(null);
  const [erc, setErc] = useState<ERCReport | null>(null);
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState<string | null>(null);
  const [err, setErr] = useState<string | null>(null);

  const liveRef = useRef(true);
  const lastImported = useRef("");

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
    const e = await circuitClient.engines(t, design);
    if (e?.engines) setEngines(e.engines);
    if (e?.active) setEngine(e.active);
    const ds = await circuitClient.designs(t);
    if (ds?.designs) setDesigns(ds.designs);
    const cfg = await circuitClient.configGet(t, design);
    if (cfg?.engine) setEngine(cfg.engine);
    if (cfg?.info) setInfo(cfg.info);
    setSim(null);
    setErc(null);
    if ((cfg?.info?.elementCount ?? 0) > 0) {
      const ex = await circuitClient.exportNetlist(t, "spice", design);
      if (ex?.spice && ex.spice.trim()) {
        setNetlist(ex.spice);
        lastImported.current = ex.spice;
      }
    } else {
      lastImported.current = "";
    }
  }, [target, design]);

  // syncNetlist pushes the editor text to the box if it changed, so Run/ERC
  // always reflect what's on screen. Returns CircuitInfo or null on error.
  const syncNetlist = useCallback(async (): Promise<CircuitInfo | null> => {
    const t = target();
    if (!t) {
      setErr("pick a device first");
      return null;
    }
    if (netlist === lastImported.current && info) return info;
    const r = await circuitClient.importNetlist(t, netlist, "auto", design);
    if ((r as any)?.ok === false) {
      setErr((r as any).error || "import failed");
      return null;
    }
    if (r?.info) {
      setInfo(r.info);
      lastImported.current = netlist;
      return r.info;
    }
    return null;
  }, [target, netlist, info, design]);

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
    setBusy(true);
    setMsg(null);
    setErr(null);
    lastImported.current = "";
    const i = await syncNetlist();
    setBusy(false);
    if (i) setMsg(`Imported ${i.elementCount} elements · ${i.nodeCount} nets`);
  }, [syncNetlist]);

  const doSimulate = useCallback(async () => {
    const t = target();
    if (!t) return setErr("pick a device first");
    setBusy(true);
    setMsg(null);
    setErr(null);
    const i = await syncNetlist();
    if (!i) return setBusy(false);
    if (!i.hasGround) {
      setBusy(false);
      return setErr("No ground node (0) — the circuit has no voltage reference.");
    }
    if (analysis !== "op" && !i.simulatable) {
      setBusy(false);
      return setErr("Connection list (KiCad/EPLAN) — run ERC, not a simulation.");
    }
    if (analysis === "op") {
      const r = await circuitClient.measure(t, design);
      setBusy(false);
      if ((r as any)?.ok === false) return setErr((r as any).error || "sim failed");
      setSim({ analysis: "op", signals: [], samples: [], nodeVoltages: r.nodeVoltages, branchCurrents: r.branchCurrents, engine: r.engine });
      setMsg(`operating point · ${r.engine}`);
      return;
    }
    const r = await circuitClient.simulate(t, analysisPayload(), design);
    setBusy(false);
    if ((r as any)?.ok === false) return setErr((r as any).error || "sim failed");
    if (r?.result) {
      setSim(r.result);
      setMsg(`${r.result.samples?.length ?? 0} samples · ${r.result.engine}`);
    }
  }, [target, syncNetlist, analysis, analysisPayload, design]);

  const doErc = useCallback(async () => {
    const t = target();
    if (!t) return setErr("pick a device first");
    setBusy(true);
    setMsg(null);
    setErr(null);
    const i = await syncNetlist();
    if (!i) return setBusy(false);
    const r = await circuitClient.erc(t, design);
    setBusy(false);
    if (r?.report) setErc(r.report);
  }, [target, syncNetlist, design]);

  const setEng = useCallback(
    async (eng: string) => {
      setEngine(eng);
      const t = target();
      if (t) await circuitClient.configSet(t, { engine: eng }, design);
    },
    [target, design],
  );

  // pickDesign switches the active netlist slot and reloads it from the box.
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
    if (!designs.some((x) => x.design === id)) {
      setDesigns((d) => [...d, { design: id }]);
    }
    pickDesign(id);
  }, [newDesign, designs, pickDesign]);

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
            {/* design slot — one box holds many designs (Talos panels, OCPP revs…) */}
            <Text style={s.label}>Design</Text>
            <ScrollView horizontal showsHorizontalScrollIndicator={false} style={{ marginBottom: 8 }}>
              {[{ design: "", title: "default" } as CircuitDesignSummary, ...designs.filter((x) => x.design && x.design !== "default")].map((dg) => {
                const sel = design === dg.design || (design === "" && dg.design === "");
                const labelTxt = dg.design === "" ? "default" : dg.design;
                return (
                  <Pressable key={dg.design || "default"} onPress={() => pickDesign(dg.design)} style={[s.chip, sel && { backgroundColor: ACCENT, borderColor: ACCENT }]}>
                    <Text style={[s.chipText, sel && { color: "#fff" }]}>
                      {labelTxt}
                      {typeof dg.elements === "number" && dg.elements > 0 ? ` · ${dg.elements}` : ""}
                    </Text>
                  </Pressable>
                );
              })}
            </ScrollView>
            <View style={[s.row, { marginBottom: 12 }]}>
              <TextInput
                value={newDesign}
                onChangeText={setNewDesign}
                placeholder="new design id (e.g. panel-1)"
                placeholderTextColor={c.textMuted}
                autoCapitalize="none"
                autoCorrect={false}
                style={[s.code, { minHeight: 0, flex: 1, color: c.textPrimary, fontFamily: undefined, marginBottom: 0 }]}
              />
              <Pressable onPress={createDesign} style={[s.btn, { backgroundColor: "#2d6fd6" }]}>
                <Text style={[s.btnText, { color: "#fff" }]}>＋ New</Text>
              </Pressable>
            </View>

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
                <Text style={[s.btnText, { color: "#fff", textAlign: "center" }]}>{busy ? "Running…" : "▶ Run"}</Text>
              </Pressable>
              <Pressable onPress={doErc} disabled={busy} style={[s.btn, { backgroundColor: "#b7791f" }]}>
                <Text style={[s.btnText, { color: "#fff" }]}>ERC</Text>
              </Pressable>
            </View>

            {busy && <ActivityIndicator color={ACCENT} style={{ marginTop: 12 }} />}
            {err ? (
              <View style={s.errBox}>
                <Text style={s.errText}>{err}</Text>
              </View>
            ) : (
              msg && <Text style={[s.muted, { marginTop: 10 }]}>{msg}</Text>
            )}

            {sim && sim.samples?.length > 0 && (
              <View style={s.card}>
                <Text style={s.cardTitle}>
                  {sim.analysis.toUpperCase()} {sim.analysis === "ac" ? "(Bode)" : ""} · {sim.engine}
                </Text>
                <WaveformChart result={sim} />
              </View>
            )}

            {sim?.nodeVoltages && (
              <View style={s.card}>
                <Text style={s.cardTitle}>Operating point ({sim.engine})</Text>
                {Object.entries(sim.nodeVoltages).map(([n, v]) => (
                  <View key={n} style={s.kv}>
                    <Text style={s.kvKey}>V({n})</Text>
                    <Text style={s.kvVal}>{fmt(v)} V</Text>
                  </View>
                ))}
                {sim.branchCurrents &&
                  Object.entries(sim.branchCurrents).map(([n, i]) => (
                    <View key={n} style={s.kv}>
                      <Text style={s.kvKey}>I({n})</Text>
                      <Text style={[s.kvVal, { color: "#e6b450" }]}>{fmt(i)} A</Text>
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
    card: { backgroundColor: c.bgCard, borderRadius: 10, borderWidth: 1, borderColor: c.border, padding: 12, marginTop: 14 },
    cardTitle: { color: c.textPrimary, fontWeight: "700", fontSize: 14, marginBottom: 8 },
    kv: { flexDirection: "row", justifyContent: "space-between", paddingVertical: 2 },
    kvKey: { color: c.textMuted, fontFamily: "Menlo", fontSize: 12 },
    kvVal: { color: "#9be3a8", fontFamily: "Menlo", fontSize: 12 },
    finding: { fontSize: 12, marginBottom: 4, lineHeight: 16 },
    errBox: { marginTop: 10, borderRadius: 8, borderWidth: 1, borderColor: "#e2555566", backgroundColor: "#e2555518", padding: 10 },
    errText: { color: "#f3a3a3", fontSize: 13, lineHeight: 18 },
  });
}
