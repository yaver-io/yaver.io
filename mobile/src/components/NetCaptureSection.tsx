// NetCaptureSection — the wire-observe & deep-analysis panel embedded inside a
// device's details (mirrors ScreenlogSection). Peer-aware: for a non-active
// device it routes through /peer/<id>. Start a network (tcpdump) or serial
// (RS232/RS485) capture, watch decoded frames live, and read the structured
// deep-analysis (per-protocol stats, disconnect timeline, deterministic
// findings) — all from the phone. This is the same engine that powers the
// phone-plugged-into-a-machine (USB-RS485) troubleshooting flow.

import React, { useCallback, useEffect, useRef, useState } from "react";
import { ActivityIndicator, Pressable, ScrollView, Text, TextInput, View } from "react-native";
import { useColors } from "../context/ThemeContext";
import { useDevice, type Device } from "../context/DeviceContext";
import { quicClient } from "../lib/quic";
import { linkCallOps } from "../lib/bleTransport";

type NcEvent = { type?: string; proto?: string; summary?: string; severity?: string };
type Finding = { severity: string; code: string; title: string; detail?: string };
type Flow = { key: string; appProto?: string; packets: number; state: string; retransmits: number; rttMs?: number };
type Disconnect = { cause: string; flow: string; note?: string };
type Analysis = {
  packets?: number;
  bytes?: number;
  protocols?: Record<string, number>;
  flows?: Flow[];
  disconnects?: Disconnect[];
  findings?: Finding[];
};

export function NetCaptureSection({ device }: { device: Device }) {
  const c = useColors();
  const { activeDevice, connectionStatus } = useDevice();
  const isActive = Boolean(activeDevice && activeDevice.id === device.id && connectionStatus === "connected");
  const target = isActive ? undefined : device.id;

  const [open, setOpen] = useState(false);
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
  const [error, setError] = useState<string | null>(null);
  const stopStreamRef = useRef<(() => void) | null>(null);

  // connector-box one-tap connect + self-test
  const [boxBusy, setBoxBusy] = useState<"" | "connect" | "selftest">("");
  const [boxResult, setBoxResult] = useState<{ ok: boolean; text: string } | null>(null);
  const [boxControl, setBoxControl] = useState("");
  const [boxUnit, setBoxUnit] = useState("1");

  const refresh = useCallback(async () => {
    if (!session) return;
    try {
      const res = await quicClient.netcaptureAnalysisFor(session, target);
      if (res?.analysis) setAnalysis(res.analysis);
    } catch {
      /* ignore */
    }
  }, [session, target]);

  useEffect(() => {
    if (!running || !session) return;
    const t = setInterval(refresh, 2500);
    return () => clearInterval(t);
  }, [running, session, refresh]);

  useEffect(() => () => { try { stopStreamRef.current?.(); } catch { /* ignore */ } }, []);

  const start = useCallback(async () => {
    if (busy) return;
    setBusy(true);
    setError(null);
    setEvents([]);
    setAnalysis(null);
    try {
      const res = await quicClient.netcaptureStart(
        kind === "serial" ? { kind, device: serialDevice, decoder } : { kind, iface, filter },
        target,
      );
      if (!res?.session) throw new Error(res?.warning || "failed to start");
      setSession(res.session);
      setRunning(true);
      stopStreamRef.current = quicClient.netcaptureStream(res.stream, target, (ev: NcEvent) => {
        if (ev?.type !== "netcapture") return;
        setEvents((prev) => {
          const next = [...prev, ev];
          return next.length > 200 ? next.slice(next.length - 200) : next;
        });
      });
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  }, [busy, kind, iface, filter, serialDevice, decoder, target]);

  const callBox = useCallback(
    (verb: string, payload: Record<string, unknown>) =>
      target ? quicClient.callOpsOnDevice(device.id, verb, payload) : quicClient.callOps(verb, payload),
    [target, device.id],
  );

  const boxConnect = useCallback(async () => {
    if (boxBusy) return;
    setBoxBusy("connect");
    setBoxResult(null);
    const payload = { control: boxControl || undefined, unit: Number(boxUnit) || 1, start: 0 };
    // try mesh/LAN; auto-fall-back to BLE on a no-Wi-Fi floor.
    const r = await linkCallOps("box_autoconnect", payload, () => callBox("box_autoconnect", payload));
    const i = r?.initial;
    const via = r?.via === "ble" ? " · via BLE" : "";
    if (r?.ok && i) setBoxResult({ ok: true, text: `A/B ${i.abSwap ? "swapped" : "normal"}, term ${i.termination ? "on" : "off"}${via}` });
    else setBoxResult({ ok: false, text: (r?.error || "no Modbus reply on any combo") + via });
    setBoxBusy("");
  }, [boxBusy, callBox, boxControl, boxUnit]);

  const boxSelftest = useCallback(async () => {
    if (boxBusy) return;
    setBoxBusy("selftest");
    setBoxResult(null);
    const payload = { control: boxControl || undefined, unit: Number(boxUnit) || 1 };
    const r = await linkCallOps("box_selftest", payload, () => callBox("box_selftest", payload));
    const i = r?.initial;
    const via = r?.via === "ble" ? " · via BLE" : "";
    if (i?.summary) setBoxResult({ ok: !!r?.ok, text: i.summary + via });
    else setBoxResult({ ok: false, text: (r?.error || "self-test unreachable") + via });
    setBoxBusy("");
  }, [boxBusy, callBox, boxControl, boxUnit]);

  const stop = useCallback(async () => {
    if (!session) return;
    setBusy(true);
    try {
      stopStreamRef.current?.();
      stopStreamRef.current = null;
      const res = await quicClient.netcaptureStop(session, target);
      if (res?.analysis) setAnalysis(res.analysis);
      setRunning(false);
    } catch {
      /* ignore */
    } finally {
      setBusy(false);
    }
  }, [session, target]);

  const sevColor = (sev?: string) =>
    sev === "error" ? c.error : sev === "warn" ? c.warn : c.textMuted;

  const inputStyle = {
    backgroundColor: c.surfaceMuted,
    color: c.textPrimary,
    borderColor: c.borderSubtle,
    borderWidth: 1,
    borderRadius: 8,
    paddingHorizontal: 10,
    paddingVertical: 6,
    fontSize: 13,
  } as const;

  return (
    <View style={{ marginTop: 14 }}>
      <Pressable
        onPress={() => setOpen((o) => !o)}
        style={{ flexDirection: "row", alignItems: "center", justifyContent: "space-between", paddingVertical: 8 }}
      >
        <Text style={{ color: c.textPrimary, fontSize: 14, fontWeight: "600" }}>Network / Wire Monitor</Text>
        <Text style={{ color: c.textMuted, fontSize: 18 }}>{open ? "−" : "+"}</Text>
      </Pressable>

      {open && (
        <View style={{ gap: 10 }}>
          <Text style={{ color: c.textMuted, fontSize: 11 }}>
            Deep packet + serial analysis (Modbus, S7/LOGO!, OPC-UA, SQL/TDS, HTTP, RS232/RS485). Agent must run with --netcapture.
          </Text>

          {/* source toggle */}
          <View style={{ flexDirection: "row", gap: 8 }}>
            {(["net", "serial"] as const).map((k) => (
              <Pressable
                key={k}
                onPress={() => !running && setKind(k)}
                style={{
                  paddingHorizontal: 12,
                  paddingVertical: 6,
                  borderRadius: 8,
                  borderWidth: 1,
                  borderColor: kind === k ? c.accent : c.borderSubtle,
                  backgroundColor: kind === k ? c.accentSoft : "transparent",
                  opacity: running ? 0.5 : 1,
                }}
              >
                <Text style={{ color: kind === k ? c.accent : c.textSecondary, fontSize: 12 }}>
                  {k === "net" ? "Network" : "Serial"}
                </Text>
              </Pressable>
            ))}
          </View>

          {kind === "net" ? (
            <>
              <TextInput value={iface} onChangeText={setIface} editable={!running} placeholder="interface (any/eth0)" placeholderTextColor={c.textMuted} style={inputStyle} />
              <TextInput value={filter} onChangeText={setFilter} editable={!running} placeholder="BPF filter e.g. tcp port 502" placeholderTextColor={c.textMuted} style={inputStyle} autoCapitalize="none" />
            </>
          ) : (
            <>
              <TextInput value={serialDevice} onChangeText={setSerialDevice} editable={!running} placeholder="/dev/ttyUSB0 (blank = fed)" placeholderTextColor={c.textMuted} style={inputStyle} autoCapitalize="none" />
              <View style={{ flexDirection: "row", gap: 6, flexWrap: "wrap" }}>
                {["auto", "modbus_rtu", "marlin", "ascii"].map((d) => (
                  <Pressable
                    key={d}
                    onPress={() => !running && setDecoder(d)}
                    style={{ paddingHorizontal: 10, paddingVertical: 5, borderRadius: 8, borderWidth: 1, borderColor: decoder === d ? c.accent : c.borderSubtle, opacity: running ? 0.5 : 1 }}
                  >
                    <Text style={{ color: decoder === d ? c.accent : c.textSecondary, fontSize: 11 }}>{d}</Text>
                  </Pressable>
                ))}
              </View>
            </>
          )}

          <Pressable
            onPress={running ? stop : start}
            disabled={busy}
            style={{
              alignItems: "center",
              paddingVertical: 10,
              borderRadius: 8,
              backgroundColor: running ? c.errorBg : c.successBg,
              borderWidth: 1,
              borderColor: running ? c.errorBorder : c.successBorder,
              opacity: busy ? 0.6 : 1,
            }}
          >
            {busy ? (
              <ActivityIndicator color={c.textPrimary} />
            ) : (
              <Text style={{ color: running ? c.error : c.success, fontWeight: "600", fontSize: 13 }}>
                {running ? "Stop capture" : "Start capture"}
              </Text>
            )}
          </Pressable>

          {error && <Text style={{ color: c.error, fontSize: 12 }}>{error}</Text>}

          {/* connector-box one-tap connect */}
          <View style={{ borderTopWidth: 1, borderTopColor: c.divider, paddingTop: 8, gap: 6 }}>
            <Text style={{ color: c.textMuted, fontSize: 10 }}>MACHINE CONNECT (Yaver box)</Text>
            <View style={{ flexDirection: "row", gap: 6 }}>
              <TextInput value={boxControl} onChangeText={setBoxControl} editable={!boxBusy} placeholder="box 192.168.4.1:8347" placeholderTextColor={c.textMuted} style={[inputStyle, { flex: 1 }]} autoCapitalize="none" />
              <TextInput value={boxUnit} onChangeText={setBoxUnit} editable={!boxBusy} placeholder="unit" placeholderTextColor={c.textMuted} style={[inputStyle, { width: 56 }]} keyboardType="number-pad" />
            </View>
            <View style={{ flexDirection: "row", gap: 8 }}>
              <Pressable onPress={boxConnect} disabled={!!boxBusy} style={{ flex: 1, alignItems: "center", paddingVertical: 9, borderRadius: 8, backgroundColor: c.accentSoft, borderWidth: 1, borderColor: c.accent, opacity: boxBusy ? 0.6 : 1 }}>
                <Text style={{ color: c.accent, fontWeight: "600", fontSize: 12 }}>{boxBusy === "connect" ? "Connecting…" : "Connect to machine"}</Text>
              </Pressable>
              <Pressable onPress={boxSelftest} disabled={!!boxBusy} style={{ paddingHorizontal: 14, alignItems: "center", justifyContent: "center", paddingVertical: 9, borderRadius: 8, borderWidth: 1, borderColor: c.borderSubtle, opacity: boxBusy ? 0.6 : 1 }}>
                <Text style={{ color: c.textSecondary, fontSize: 12 }}>{boxBusy === "selftest" ? "…" : "Self-test"}</Text>
              </Pressable>
            </View>
            {boxResult && (
              <Text style={{ color: boxResult.ok ? c.success : c.error, fontSize: 11 }}>
                {boxResult.ok ? "✓ " : "✗ "}{boxResult.text}
              </Text>
            )}
          </View>

          {/* findings (the deep analysis) */}
          {analysis && (
            <View style={{ gap: 6 }}>
              <Text style={{ color: c.textMuted, fontSize: 11 }}>
                {analysis.packets ?? 0} pkts · {Object.entries(analysis.protocols || {}).map(([k, v]) => `${k}:${v}`).join("  ")}
              </Text>
              {(analysis.findings || []).map((f, i) => (
                <View key={i} style={{ borderWidth: 1, borderColor: c.borderSubtle, borderRadius: 8, padding: 8, backgroundColor: c.surfaceMuted }}>
                  <Text style={{ color: sevColor(f.severity), fontSize: 12, fontWeight: "600" }}>{f.title}</Text>
                  {f.detail ? <Text style={{ color: c.textMuted, fontSize: 11, marginTop: 2 }}>{f.detail}</Text> : null}
                </View>
              ))}
              {(analysis.disconnects || []).slice(-5).map((d, i) => (
                <Text key={`d${i}`} style={{ color: c.warn, fontSize: 11 }}>
                  {d.cause} · {d.flow}{d.note ? ` (${d.note})` : ""}
                </Text>
              ))}
            </View>
          )}

          {/* live feed */}
          {events.length > 0 && (
            <View style={{ borderWidth: 1, borderColor: c.borderSubtle, borderRadius: 8, maxHeight: 200 }}>
              <ScrollView style={{ padding: 8 }}>
                {events.slice(-80).map((e, i) => (
                  <Text key={i} style={{ color: sevColor(e.severity), fontSize: 11, fontFamily: "Menlo" }}>
                    {e.proto} · {e.summary}
                  </Text>
                ))}
              </ScrollView>
            </View>
          )}
        </View>
      )}
    </View>
  );
}
