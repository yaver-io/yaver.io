// Home Setup — configure the "single kumanda" without the CLI/MCP: register
// devices (Apple TV / Mi Box / Android TV / IR / switch), cameras, and ACs, learn
// IR codes, and pair an Android TV. Drives the home_*/ir_*/ac_*/camera_*/atv2_*
// ops verbs over the mesh (homeClient). Separate "Home" surface — not the dev UI.
import React, { useCallback, useEffect, useState } from "react";
import { ActivityIndicator, Pressable, ScrollView, Text, TextInput, View } from "react-native";
import { AppScreenHeader } from "../src/components/AppScreenHeader";
import { useColors } from "../src/context/ThemeContext";
import { useDevice } from "../src/context/DeviceContext";
import { homeClient, type HomeTarget } from "../src/lib/homeClient";

const KINDS = ["apple_tv", "mibox", "androidtv", "ir", "switch"];

export default function HomeSetupScreen() {
  const c = useColors();
  const deviceCtx = useDevice();
  const devices = (deviceCtx as any).devices as any[];

  const [hubId, setHubId] = useState("");
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState<string | null>(null);

  // device form
  const [dId, setDId] = useState("");
  const [dName, setDName] = useState("");
  const [dKind, setDKind] = useState("apple_tv");
  const [dAddr, setDAddr] = useState("");
  // camera form
  const [cId, setCId] = useState("");
  const [cName, setCName] = useState("");
  const [cUrl, setCUrl] = useState("");
  // ac form
  const [aId, setAId] = useState("");
  const [aHost, setAHost] = useState("");
  const [aDevId, setADevId] = useState("");
  const [aKey, setAKey] = useState("");
  // ir learn
  const [irDevice, setIrDevice] = useState("");
  const [irKey, setIrKey] = useState("power");
  const [irHost, setIrHost] = useState("");
  // android tv pair
  const [atvHost, setAtvHost] = useState("");
  const [atvCode, setAtvCode] = useState("");
  const [atvId, setAtvId] = useState("");

  const target = useCallback((): HomeTarget | undefined => {
    if (!hubId) return undefined;
    const d = devices?.find((x) => x.id === hubId || x.deviceId === hubId);
    return { id: hubId, lanIps: d?.lanIps, host: d?.host, port: 18080 };
  }, [hubId, devices]);

  useEffect(() => {
    if (!hubId && devices?.length) setHubId(devices[0].id || devices[0].deviceId);
  }, [devices, hubId]);

  const run = useCallback(
    async (label: string, fn: (t: HomeTarget) => Promise<any>) => {
      const t = target();
      if (!t) {
        setMsg("Pick a hub first");
        return;
      }
      setBusy(true);
      setMsg(null);
      try {
        const r = await fn(t);
        setMsg(r?.ok === false ? `${label}: ${r.error || "failed"}` : `✓ ${label}`);
      } catch (e: any) {
        setMsg(`${label}: ${e?.message || "failed"}`);
      } finally {
        setBusy(false);
      }
    },
    [target],
  );

  const Field = ({ label, value, onChange, placeholder }: { label: string; value: string; onChange: (s: string) => void; placeholder?: string }) => (
    <View style={{ marginBottom: 8 }}>
      <Text style={{ color: c.textMuted, fontSize: 12, marginBottom: 2 }}>{label}</Text>
      <TextInput
        value={value}
        onChangeText={onChange}
        placeholder={placeholder}
        placeholderTextColor={c.textMuted}
        autoCapitalize="none"
        autoCorrect={false}
        style={{ color: c.textPrimary, backgroundColor: c.bgCard, borderColor: c.border, borderWidth: 1, borderRadius: 8, paddingHorizontal: 10, paddingVertical: 8 }}
      />
    </View>
  );

  const Btn = ({ label, onPress }: { label: string; onPress: () => void }) => (
    <Pressable onPress={onPress} style={{ backgroundColor: c.accent, borderRadius: 8, paddingVertical: 10, alignItems: "center", marginTop: 4 }}>
      <Text style={{ color: c.bg, fontWeight: "700" }}>{label}</Text>
    </Pressable>
  );

  const Card = ({ title, children }: { title: string; children: React.ReactNode }) => (
    <View style={{ backgroundColor: c.bgCard, borderColor: c.border, borderWidth: 1, borderRadius: 12, padding: 12, marginBottom: 14 }}>
      <Text style={{ color: c.textPrimary, fontWeight: "700", marginBottom: 8 }}>{title}</Text>
      {children}
    </View>
  );

  return (
    <View style={{ flex: 1, backgroundColor: c.bg }}>
      <AppScreenHeader title="Home Setup" />
      <ScrollView contentContainerStyle={{ padding: 16 }}>
        <Text style={{ color: c.textMuted, marginBottom: 6 }}>Hub device</Text>
        <View style={{ flexDirection: "row", flexWrap: "wrap", marginBottom: 14 }}>
          {(devices || []).map((d) => {
            const id = d.id || d.deviceId;
            return (
              <Pressable key={id} onPress={() => setHubId(id)} style={{ borderWidth: 1, borderColor: c.border, borderRadius: 10, paddingHorizontal: 12, paddingVertical: 8, marginRight: 8, marginBottom: 8, backgroundColor: hubId === id ? c.accent : c.bgCard }}>
                <Text style={{ color: hubId === id ? c.bg : c.textPrimary, fontWeight: "600" }}>{d.name || id}</Text>
              </Pressable>
            );
          })}
        </View>

        <Card title="Add device">
          <Field label="ID" value={dId} onChange={setDId} placeholder="livingtv" />
          <Field label="Name" value={dName} onChange={setDName} placeholder="Living Room TV" />
          <Text style={{ color: c.textMuted, fontSize: 12, marginBottom: 2 }}>Kind</Text>
          <View style={{ flexDirection: "row", flexWrap: "wrap", marginBottom: 8 }}>
            {KINDS.map((k) => (
              <Pressable key={k} onPress={() => setDKind(k)} style={{ borderWidth: 1, borderColor: c.border, borderRadius: 8, paddingHorizontal: 10, paddingVertical: 6, marginRight: 6, marginBottom: 6, backgroundColor: dKind === k ? c.accent : c.bgCard }}>
                <Text style={{ color: dKind === k ? c.bg : c.textPrimary, fontSize: 12 }}>{k}</Text>
              </Pressable>
            ))}
          </View>
          <Field label="Address" value={dAddr} onChange={setDAddr} placeholder="Apple TV id / adb serial / TV ip / blaster ip / switch URL{cmd}" />
          <Btn label="Add device" onPress={() => run("device added", (t) => homeClient.addDevice(t, { id: dId, name: dName, kind: dKind, address: dAddr }))} />
        </Card>

        <Card title="Add camera">
          <Field label="ID" value={cId} onChange={setCId} placeholder="frontdoor" />
          <Field label="Name" value={cName} onChange={setCName} placeholder="Front Door" />
          <Field label="RTSP / HTTP URL" value={cUrl} onChange={setCUrl} placeholder="rtsp://user:pass@ip:554/Streaming/Channels/101" />
          <Btn label="Add camera" onPress={() => run("camera added", (t) => homeClient.addCamera(t, { id: cId, name: cName, url: cUrl }))} />
        </Card>

        <Card title="Add WiFi AC (Tuya local)">
          <Field label="ID" value={aId} onChange={setAId} placeholder="bedroom" />
          <Field label="Host (IP)" value={aHost} onChange={setAHost} placeholder="192.168.1.60" />
          <Field label="Device ID" value={aDevId} onChange={setADevId} placeholder="tuya devid" />
          <Field label="Local key" value={aKey} onChange={setAKey} placeholder="tuya local key" />
          <Btn label="Add AC" onPress={() => run("AC added", (t) => homeClient.addAC(t, { id: aId, kind: "tuya", host: aHost, devid: aDevId, localkey: aKey }))} />
        </Card>

        <Card title="Learn IR code">
          <Field label="Device ID (an ir device)" value={irDevice} onChange={setIrDevice} placeholder="satellite" />
          <Field label="Logical key" value={irKey} onChange={setIrKey} placeholder="power / ok / channel_up" />
          <Field label="Blaster host (IP)" value={irHost} onChange={setIrHost} placeholder="192.168.1.70" />
          <Btn label="Scan blasters" onPress={() => run("scan", (t) => homeClient.irScan(t))} />
          <Btn label="Learn (press the remote now)" onPress={() => run("learned — press the button", (t) => homeClient.irLearn(t, irDevice, irKey, irHost))} />
        </Card>

        <Card title="Pair Android TV / Mi Box">
          <Field label="TV host (IP)" value={atvHost} onChange={setAtvHost} placeholder="192.168.1.80" />
          <Btn label="Start pairing (code shows on TV)" onPress={() => run("pairing started", (t) => homeClient.atv2PairBegin(t, atvHost))} />
          <Field label="Code from TV" value={atvCode} onChange={setAtvCode} placeholder="123456" />
          <Field label="Device ID to save as" value={atvId} onChange={setAtvId} placeholder="livingmibox" />
          <Btn label="Finish pairing" onPress={() => run("paired", (t) => homeClient.atv2PairFinish(t, { host: atvHost, code: atvCode, id: atvId }))} />
        </Card>

        {busy ? <ActivityIndicator color={c.accent} /> : null}
        {msg ? <Text style={{ color: c.textMuted, marginTop: 8 }}>{msg}</Text> : null}
      </ScrollView>
    </View>
  );
}
