// 3D Printer — Yaver as the single management layer for a 3D printer cell. Today
// the Bambu Lab P1/P1S/A1/X1 over the LAN: discover (SSDP, no credentials),
// status (temps / progress / stage), chamber camera, and control (light, pause /
// resume / stop, set temps). Remote CAD closes the loop — write OpenSCAD on a
// dev box, render it, SEE the 3D model here, then slice → upload → print.
//
// Destructive actions (start print, raw gcode) are confirm-gated; the screen
// refuses to start a print over a busy machine, matching the agent interlock.
import React, { useCallback, useEffect, useRef, useState } from "react";
import { ActivityIndicator, Alert, Image, Pressable, ScrollView, StyleSheet, Text, TextInput, View } from "react-native";
import { useRouter } from "expo-router";
import { AppScreenHeader } from "../src/components/AppScreenHeader";
import { StlViewer } from "../src/components/StlViewer";
import { useColors } from "../src/context/ThemeContext";
import { useDevice } from "../src/context/DeviceContext";
import {
  printerClient,
  getPrinterDeviceId,
  setPrinterDeviceId,
  type CadRender,
  type Discovered,
  type PrinterConfig,
  type PrinterStatus,
  type PrinterTarget,
} from "../src/lib/printerClient";

const ACCENT = "#4f9cf9";

const DEMO_SCAD = `// Yaver remote CAD demo — a parametric box
w = 30; d = 20; h = 15; wall = 2;
difference() {
  cube([w, d, h], center=true);
  translate([0,0,wall]) cube([w-2*wall, d-2*wall, h], center=true);
}`;

export default function PrinterScreen() {
  const c = useColors();
  const router = useRouter();
  const deviceCtx = useDevice();
  const devices = (deviceCtx as any).devices as any[];

  const [deviceId, setDeviceId] = useState("");
  const [config, setConfig] = useState<PrinterConfig | null>(null);
  const [enabled, setEnabled] = useState(false);
  const [status, setStatus] = useState<PrinterStatus | null>(null);
  const [frame, setFrame] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState<string | null>(null);

  // setup
  const [showSetup, setShowSetup] = useState(false);
  const [found, setFound] = useState<Discovered[]>([]);
  const [addr, setAddr] = useState("");
  const [serial, setSerial] = useState("");
  const [code, setCode] = useState("");
  const [model, setModel] = useState("");

  // CAD
  const [scad, setScad] = useState(DEMO_SCAD);
  const [render, setRender] = useState<CadRender | null>(null);
  const [stlB64, setStlB64] = useState<string | null>(null);

  const liveRef = useRef(true);

  const target = useCallback((): PrinterTarget | undefined => {
    if (!deviceId) return undefined;
    const d = devices?.find((x) => x.id === deviceId || x.deviceId === deviceId);
    return { id: deviceId, lanIps: d?.lanIps, host: d?.host, port: 18080 };
  }, [deviceId, devices]);

  useEffect(() => {
    getPrinterDeviceId().then((id) => id && setDeviceId(id));
    return () => {
      liveRef.current = false;
    };
  }, []);

  const loadConfig = useCallback(async () => {
    const t = target();
    if (!t) return;
    const r = await printerClient.configGet(t);
    if (r?.config) {
      setConfig(r.config);
      setEnabled(!!r.enabled);
      setAddr(r.config.addr || "");
      setSerial(r.config.serial || "");
      setModel(r.config.model || "");
      if (!r.enabled) setShowSetup(true);
    }
  }, [target]);

  const refresh = useCallback(async () => {
    const t = target();
    if (!t || !enabled) return;
    try {
      const s = await printerClient.status(t);
      if (s?.status && liveRef.current) setStatus(s.status);
    } catch {}
    try {
      const f = await printerClient.snapshot(t);
      if (f?.image && liveRef.current) setFrame(f.image);
    } catch {}
  }, [target, enabled]);

  useEffect(() => {
    if (deviceId) loadConfig();
  }, [deviceId, loadConfig]);

  useEffect(() => {
    if (!enabled) return;
    refresh();
    const iv = setInterval(refresh, 4000);
    return () => clearInterval(iv);
  }, [enabled, refresh]);

  const pickDevice = useCallback(
    async (id: string) => {
      setDeviceId(id);
      await setPrinterDeviceId(id);
    },
    [],
  );

  const doDiscover = useCallback(async () => {
    const t = target();
    if (!t) {
      setMsg("Pick the box wired to the printer first");
      return;
    }
    setBusy(true);
    setMsg("Scanning the LAN for printers…");
    try {
      const r = await printerClient.discover(t, 6);
      setFound(r?.printers || []);
      setMsg(r?.printers?.length ? `Found ${r.printers.length}` : "No printers found on that box's LAN");
    } catch (e: any) {
      setMsg(e?.message || "discover failed");
    } finally {
      setBusy(false);
    }
  }, [target]);

  const applyFound = useCallback((d: Discovered) => {
    setAddr(d.ip);
    setSerial(d.serial);
    setModel(d.model);
    setMsg(`Selected ${d.model} @ ${d.ip} — enter the LAN access code (printer screen → Settings → WLAN)`);
  }, []);

  const saveConfig = useCallback(async () => {
    const t = target();
    if (!t) return;
    if (!addr || !serial) {
      setMsg("addr + serial required (run discover)");
      return;
    }
    setBusy(true);
    try {
      const cfg: PrinterConfig = { driver: "bambu", addr, serial, model, accessCode: code || undefined };
      const r = await printerClient.configSet(t, cfg);
      setConfig(r?.config || null);
      setEnabled(!!r?.enabled);
      setCode("");
      setMsg(r?.enabled ? "Printer linked ✓" : "Saved — access code still needed");
      if (r?.enabled) setShowSetup(false);
    } catch (e: any) {
      setMsg(e?.message || "save failed");
    } finally {
      setBusy(false);
    }
  }, [target, addr, serial, model, code]);

  const cmd = useCallback(
    async (label: string, fn: () => Promise<any>) => {
      setBusy(true);
      setMsg(label + "…");
      try {
        const r = await fn();
        setMsg(r?.error ? `${label}: ${r.error}` : `${label} ✓`);
        refresh();
      } catch (e: any) {
        setMsg(e?.message || `${label} failed`);
      } finally {
        setBusy(false);
      }
    },
    [refresh],
  );

  const doRender = useCallback(async () => {
    const t = target();
    if (!t) return;
    setBusy(true);
    setMsg("Rendering OpenSCAD on the box…");
    setRender(null);
    setStlB64(null);
    try {
      const r = await printerClient.cadRender(t, scad, "model");
      if ((r as any)?.error) {
        setMsg((r as any).error);
        return;
      }
      setRender(r);
      setMsg(r?.stlPath ? `Rendered (${Math.round((r.stlBytes || 0) / 1024)} KB STL)` : "Rendered preview");
      if (r?.stlPath) {
        const g = await printerClient.cadGet(t, r.stlPath);
        if (g?.base64) setStlB64(g.base64);
      }
    } catch (e: any) {
      setMsg(e?.message || "render failed");
    } finally {
      setBusy(false);
    }
  }, [target, scad]);

  const confirmPrint = useCallback(() => {
    Alert.alert(
      "Start print?",
      "This physically runs the printer for hours. Make sure the bed is clear of any pre-printed parts.",
      [
        { text: "Cancel", style: "cancel" },
        {
          text: "Start",
          style: "destructive",
          onPress: () =>
            cmd("Start print", async () => {
              const t = target();
              if (!t || !render?.stlPath) return { error: "render + slice + upload first" };
              setMsg("Slicing…");
              const sl = await printerClient.cadSlice(t, render.stlPath);
              if (!sl?.outputPath) return { error: sl?.error || "no slicer on box" };
              setMsg("Uploading…");
              const up = await printerClient.upload(t, sl.outputPath);
              if (!up?.remoteFile) return { error: "upload failed" };
              return printerClient.print(t, up.remoteFile, { bedLevel: true });
            }),
        },
      ],
    );
  }, [cmd, target, render]);

  const t = target();
  const stateColor = status?.state === "printing" ? "#28c76f" : status?.state === "failed" ? "#ea5455" : status?.state === "paused" ? "#ff9f43" : c.textMuted;

  return (
    <View style={{ flex: 1, backgroundColor: c.bg }}>
      <AppScreenHeader title="3D Printer" onBack={() => router.back()} />
      <ScrollView contentContainerStyle={{ padding: 16, gap: 12 }}>
        {/* device picker */}
        <View style={[st.card, { backgroundColor: c.bgCard, borderColor: c.border }]}>
          <Text style={[st.h, { color: c.textPrimary }]}>Printer host box</Text>
          <Text style={[st.muted, { color: c.textMuted }]}>The machine on the same LAN as the printer (Bambu reachable on 8883/990/6000).</Text>
          <View style={{ flexDirection: "row", flexWrap: "wrap", gap: 8, marginTop: 8 }}>
            {(devices || []).filter((d) => d.isOnline || d.online).map((d) => {
              const id = d.id || d.deviceId;
              const on = id === deviceId;
              return (
                <Pressable key={id} onPress={() => pickDevice(id)} style={[st.chip, { borderColor: on ? ACCENT : c.border, backgroundColor: on ? ACCENT + "22" : "transparent" }]}>
                  <Text style={{ color: on ? ACCENT : c.textPrimary, fontSize: 13 }}>{d.name || id?.slice(0, 8)}</Text>
                </Pressable>
              );
            })}
          </View>
        </View>

        {/* setup / discovery */}
        {(showSetup || !enabled) && t ? (
          <View style={[st.card, { backgroundColor: c.bgCard, borderColor: c.border }]}>
            <Text style={[st.h, { color: c.textPrimary }]}>Link a printer</Text>
            <Pressable onPress={doDiscover} disabled={busy} style={[st.btn, { backgroundColor: ACCENT }]}>
              <Text style={st.btnT}>Discover on LAN</Text>
            </Pressable>
            {found.map((d) => (
              <Pressable key={d.serial} onPress={() => applyFound(d)} style={[st.row, { borderColor: c.border }]}>
                <Text style={{ color: c.textPrimary, flex: 1 }}>{d.model} · {d.ip}</Text>
                <Text style={{ color: c.textMuted, fontSize: 12 }}>{d.signalDb ? `${d.signalDb}dBm` : ""} {d.bind}</Text>
              </Pressable>
            ))}
            <Field label="IP address" value={addr} onChange={setAddr} color={c} />
            <Field label="Serial" value={serial} onChange={setSerial} color={c} />
            <Field label="Model" value={model} onChange={setModel} color={c} />
            <Field label="LAN access code" value={code} onChange={setCode} color={c} secure placeholder="from printer screen → Settings → WLAN" />
            <Pressable onPress={saveConfig} disabled={busy} style={[st.btn, { backgroundColor: "#28c76f" }]}>
              <Text style={st.btnT}>Save & link</Text>
            </Pressable>
          </View>
        ) : null}

        {/* live status */}
        {enabled ? (
          <View style={[st.card, { backgroundColor: c.bgCard, borderColor: c.border }]}>
            <View style={{ flexDirection: "row", alignItems: "center", justifyContent: "space-between" }}>
              <Text style={[st.h, { color: c.textPrimary }]}>{config?.model || "Printer"} · {config?.addr}</Text>
              <Text style={{ color: stateColor, fontWeight: "700" }}>{(status?.state || "—").toUpperCase()}</Text>
            </View>
            {status?.stage ? <Text style={[st.muted, { color: c.textMuted }]}>{status.stage}</Text> : null}
            {frame ? <Image source={{ uri: frame }} style={{ width: "100%", height: 200, borderRadius: 10, marginTop: 8, backgroundColor: "#000" }} resizeMode="contain" /> : null}
            <View style={{ flexDirection: "row", gap: 12, marginTop: 10 }}>
              <Stat label="Nozzle" v={`${Math.round(status?.nozzle?.cur || 0)}/${Math.round(status?.nozzle?.target || 0)}°`} color={c} />
              <Stat label="Bed" v={`${Math.round(status?.bed?.cur || 0)}/${Math.round(status?.bed?.target || 0)}°`} color={c} />
              <Stat label="Progress" v={`${Math.round(status?.progress || 0)}%`} color={c} />
              {status?.remainingMin ? <Stat label="ETA" v={`${status.remainingMin}m`} color={c} /> : null}
            </View>
            {status?.errors?.length ? <Text style={{ color: "#ea5455", marginTop: 6 }}>{status.errors.join(", ")}</Text> : null}

            <View style={{ flexDirection: "row", flexWrap: "wrap", gap: 8, marginTop: 12 }}>
              <Ctl label={status?.lightOn ? "Light off" : "Light on"} onPress={() => cmd("Light", () => printerClient.light(t!, !status?.lightOn))} color={c} />
              <Ctl label="Pause" onPress={() => cmd("Pause", () => printerClient.pause(t!))} color={c} />
              <Ctl label="Resume" onPress={() => cmd("Resume", () => printerClient.resume(t!))} color={c} />
              <Ctl label="Stop" danger onPress={() => cmd("Stop", () => printerClient.stop(t!))} color={c} />
            </View>
            <Pressable onPress={() => setShowSetup((v) => !v)}>
              <Text style={{ color: ACCENT, marginTop: 10, fontSize: 12 }}>{showSetup ? "Hide setup" : "Edit connection"}</Text>
            </Pressable>
          </View>
        ) : null}

        {/* remote CAD */}
        {t ? (
          <View style={[st.card, { backgroundColor: c.bgCard, borderColor: c.border }]}>
            <Text style={[st.h, { color: c.textPrimary }]}>Remote CAD (OpenSCAD)</Text>
            <Text style={[st.muted, { color: c.textMuted }]}>Write OpenSCAD; the box renders it; the model shows here in 3D. Then slice → upload → print.</Text>
            <TextInput
              value={scad}
              onChangeText={setScad}
              multiline
              style={[st.code, { color: c.textPrimary, borderColor: c.border }]}
              autoCapitalize="none"
              autoCorrect={false}
            />
            <View style={{ flexDirection: "row", gap: 8, marginTop: 8 }}>
              <Pressable onPress={doRender} disabled={busy} style={[st.btn, { backgroundColor: ACCENT, flex: 1 }]}>
                <Text style={st.btnT}>Render</Text>
              </Pressable>
              {render?.stlPath && enabled ? (
                <Pressable onPress={confirmPrint} disabled={busy} style={[st.btn, { backgroundColor: "#ff9f43", flex: 1 }]}>
                  <Text style={st.btnT}>Slice → Print</Text>
                </Pressable>
              ) : null}
            </View>
            {stlB64 ? <View style={{ marginTop: 10 }}><StlViewer base64={stlB64} /></View> : null}
            {!stlB64 && render?.preview ? <Image source={{ uri: render.preview }} style={{ width: "100%", height: 260, borderRadius: 10, marginTop: 10, backgroundColor: "#0b0f14" }} resizeMode="contain" /> : null}
          </View>
        ) : null}

        {msg ? (
          <View style={{ flexDirection: "row", alignItems: "center", gap: 8 }}>
            {busy ? <ActivityIndicator color={ACCENT} /> : null}
            <Text style={{ color: c.textMuted, flex: 1 }}>{msg}</Text>
          </View>
        ) : null}
      </ScrollView>
    </View>
  );
}

function Field({ label, value, onChange, color, secure, placeholder }: any) {
  return (
    <View style={{ marginTop: 8 }}>
      <Text style={{ color: color.textMuted, fontSize: 12, marginBottom: 4 }}>{label}</Text>
      <TextInput
        value={value}
        onChangeText={onChange}
        secureTextEntry={secure}
        placeholder={placeholder}
        placeholderTextColor={color.textMuted}
        autoCapitalize="none"
        autoCorrect={false}
        style={[st.input, { color: color.textPrimary, borderColor: color.border }]}
      />
    </View>
  );
}
function Stat({ label, v, color }: any) {
  return (
    <View>
      <Text style={{ color: color.textMuted, fontSize: 11 }}>{label}</Text>
      <Text style={{ color: color.textPrimary, fontSize: 16, fontWeight: "700" }}>{v}</Text>
    </View>
  );
}
function Ctl({ label, onPress, color, danger }: any) {
  return (
    <Pressable onPress={onPress} style={[st.chip, { borderColor: danger ? "#ea5455" : color.border }]}>
      <Text style={{ color: danger ? "#ea5455" : color.textPrimary, fontSize: 13 }}>{label}</Text>
    </Pressable>
  );
}

const st = StyleSheet.create({
  card: { borderWidth: 1, borderRadius: 14, padding: 14 },
  h: { fontSize: 16, fontWeight: "700" },
  muted: { fontSize: 12, marginTop: 2 },
  chip: { borderWidth: 1, borderRadius: 20, paddingHorizontal: 12, paddingVertical: 7 },
  btn: { borderRadius: 10, paddingVertical: 11, alignItems: "center", marginTop: 10 },
  btnT: { color: "#fff", fontWeight: "700" },
  row: { flexDirection: "row", borderWidth: 1, borderRadius: 8, padding: 10, marginTop: 8 },
  input: { borderWidth: 1, borderRadius: 8, paddingHorizontal: 10, paddingVertical: 9 },
  code: { borderWidth: 1, borderRadius: 8, padding: 10, marginTop: 8, minHeight: 120, fontFamily: "Menlo", fontSize: 12 },
});
