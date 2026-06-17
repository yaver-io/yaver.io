// Home Control — the "single kumanda" universal remote + activities, driven over
// the Yaver mesh (LAN-first, relay fallback) via homeClient → home_* ops verbs
// (desktop/agent/ops_home.go). This lives under the separate "Home" hub
// (tv-home.tsx), NOT the coding-agent tabs — the remote/appliance/camera
// features never pollute the dev UI.
//
// Pick the hub device (the box that runs the agent next to your AV gear), pick
// one of its registered devices (Apple TV / Mi Box / …), and drive it with the
// logical D-pad; or run a saved activity ("Watch Apple TV", "Good night").
import React, { useCallback, useEffect, useState } from "react";
import { ActivityIndicator, Image, Pressable, ScrollView, Text, View } from "react-native";
import { useRouter } from "expo-router";
import { AppScreenHeader } from "../src/components/AppScreenHeader";
import { useColors } from "../src/context/ThemeContext";
import { useDevice } from "../src/context/DeviceContext";
import {
  homeClient,
  type HomeActivity,
  type HomeDevice,
  type HomeKey,
  type HomeTarget,
} from "../src/lib/homeClient";

const DPAD: { key: HomeKey; label: string }[][] = [
  [{ key: "power", label: "⏻" }, { key: "up", label: "▲" }, { key: "menu", label: "≡" }],
  [{ key: "left", label: "◀" }, { key: "ok", label: "OK" }, { key: "right", label: "▶" }],
  [{ key: "back", label: "↩" }, { key: "down", label: "▼" }, { key: "home", label: "⌂" }],
];
const TRANSPORT: { key: HomeKey; label: string }[] = [
  { key: "previous", label: "⏮" },
  { key: "play_pause", label: "⏯" },
  { key: "next", label: "⏭" },
  { key: "vol_down", label: "🔉" },
  { key: "vol_up", label: "🔊" },
];

export default function HomeControlScreen() {
  const c = useColors();
  const router = useRouter();
  const deviceCtx = useDevice();
  const devices = (deviceCtx as any).devices as any[];

  const [hubId, setHubId] = useState("");
  const [homeDevices, setHomeDevices] = useState<HomeDevice[]>([]);
  const [activities, setActivities] = useState<HomeActivity[]>([]);
  const [selected, setSelected] = useState("");
  const [cameras, setCameras] = useState<{ id: string; name?: string }[]>([]);
  const [snapshot, setSnapshot] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState<string | null>(null);

  const target = useCallback((): HomeTarget | undefined => {
    if (!hubId) return undefined;
    const d = devices?.find((x) => x.id === hubId || x.deviceId === hubId);
    return { id: hubId, lanIps: d?.lanIps, host: d?.host, port: 18080 };
  }, [hubId, devices]);

  const refresh = useCallback(async () => {
    const t = target();
    if (!t) return;
    setBusy(true);
    try {
      const [d, a, cams] = await Promise.all([homeClient.listDevices(t), homeClient.listActivities(t), homeClient.listCameras(t)]);
      const dl = (d as any)?.devices || [];
      setHomeDevices(dl);
      setActivities((a as any)?.activities || []);
      setCameras((cams as any)?.cameras || []);
      if (!selected && dl.length) setSelected(dl[0].id);
    } catch {
      setMsg("Couldn't reach the hub");
    } finally {
      setBusy(false);
    }
  }, [target, selected]);

  useEffect(() => {
    if (hubId) refresh();
  }, [hubId, refresh]);

  const send = useCallback(
    async (key: HomeKey) => {
      const t = target();
      if (!t || !selected) {
        setMsg("Pick a hub and a device first");
        return;
      }
      const r = await homeClient.key(t, selected, key);
      if ((r as any)?.ok === false) setMsg((r as any)?.error || "Failed");
      else setMsg(null);
    },
    [target, selected],
  );

  const runActivity = useCallback(
    async (name: string) => {
      const t = target();
      if (!t) return;
      setBusy(true);
      try {
        const r = await homeClient.runActivity(t, name);
        setMsg(r?.completed ? `✓ ${name}` : `${name}: stopped early`);
      } finally {
        setBusy(false);
      }
    },
    [target],
  );

  const grabSnapshot = useCallback(
    async (id: string) => {
      const t = target();
      if (!t) return;
      setBusy(true);
      try {
        const r = await homeClient.cameraSnapshot(t, id);
        if ((r as any)?.image_b64) setSnapshot(`data:${(r as any).mime || "image/jpeg"};base64,${(r as any).image_b64}`);
        else setMsg((r as any)?.error || "No frame");
      } finally {
        setBusy(false);
      }
    },
    [target],
  );

  const Chip = ({ label, active, onPress }: { label: string; active?: boolean; onPress: () => void }) => (
    <Pressable
      onPress={onPress}
      style={{
        paddingHorizontal: 12,
        paddingVertical: 8,
        borderRadius: 10,
        marginRight: 8,
        marginBottom: 8,
        backgroundColor: active ? c.accent : c.bgCard,
        borderWidth: 1,
        borderColor: c.border,
      }}
    >
      <Text style={{ color: active ? c.bg : c.textPrimary, fontWeight: "600" }}>{label}</Text>
    </Pressable>
  );

  const PadBtn = ({ label, onPress }: { label: string; onPress: () => void }) => (
    <Pressable
      onPress={onPress}
      style={{
        width: 72,
        height: 56,
        margin: 4,
        borderRadius: 12,
        alignItems: "center",
        justifyContent: "center",
        backgroundColor: c.bgCard,
        borderWidth: 1,
        borderColor: c.border,
      }}
    >
      <Text style={{ color: c.textPrimary, fontSize: 20, fontWeight: "700" }}>{label}</Text>
    </Pressable>
  );

  return (
    <View style={{ flex: 1, backgroundColor: c.bg }}>
      <AppScreenHeader title="Home Control" onBack={() => router.back()} />
      <ScrollView contentContainerStyle={{ padding: 16 }}>
        <Pressable onPress={() => router.push("/home-setup")} style={{ alignSelf: "flex-end", marginBottom: 8 }}>
          <Text style={{ color: c.accent, fontWeight: "600" }}>⚙ Setup</Text>
        </Pressable>
        <Text style={{ color: c.textMuted, marginBottom: 6 }}>Hub device</Text>
        <View style={{ flexDirection: "row", flexWrap: "wrap", marginBottom: 12 }}>
          {(devices || []).map((d) => (
            <Chip key={d.id || d.deviceId} label={d.name || d.id || d.deviceId} active={hubId === (d.id || d.deviceId)} onPress={() => setHubId(d.id || d.deviceId)} />
          ))}
        </View>

        {hubId ? (
          <>
            <Text style={{ color: c.textMuted, marginBottom: 6 }}>Device</Text>
            <View style={{ flexDirection: "row", flexWrap: "wrap", marginBottom: 12 }}>
              {homeDevices.length === 0 ? (
                <Text style={{ color: c.textMuted }}>No devices registered yet. Add Apple TV / Mi Box from the agent (home_device_add).</Text>
              ) : (
                homeDevices.map((d) => (
                  <Chip key={d.id} label={`${d.name || d.id} · ${d.kind}`} active={selected === d.id} onPress={() => setSelected(d.id)} />
                ))
              )}
            </View>

            <View style={{ alignItems: "center", marginVertical: 12 }}>
              {DPAD.map((row, i) => (
                <View key={i} style={{ flexDirection: "row" }}>
                  {row.map((b) => (
                    <PadBtn key={b.key} label={b.label} onPress={() => send(b.key)} />
                  ))}
                </View>
              ))}
              <View style={{ flexDirection: "row", marginTop: 8 }}>
                {TRANSPORT.map((b) => (
                  <PadBtn key={b.key} label={b.label} onPress={() => send(b.key)} />
                ))}
              </View>
            </View>

            <Text style={{ color: c.textMuted, marginBottom: 6 }}>Activities</Text>
            <View style={{ flexDirection: "row", flexWrap: "wrap" }}>
              {activities.length === 0 ? (
                <Text style={{ color: c.textMuted }}>No activities yet.</Text>
              ) : (
                activities.map((a) => <Chip key={a.name} label={`▶ ${a.name}`} onPress={() => runActivity(a.name)} />)
              )}
            </View>

            {cameras.length > 0 ? (
              <>
                <Text style={{ color: c.textMuted, marginTop: 16, marginBottom: 6 }}>Cameras</Text>
                <View style={{ flexDirection: "row", flexWrap: "wrap" }}>
                  {cameras.map((cam) => (
                    <Chip key={cam.id} label={`📷 ${cam.name || cam.id}`} onPress={() => grabSnapshot(cam.id)} />
                  ))}
                </View>
                {snapshot ? (
                  <Image source={{ uri: snapshot }} style={{ width: "100%", height: 200, marginTop: 8, borderRadius: 12, backgroundColor: c.bgCard }} resizeMode="contain" />
                ) : null}
              </>
            ) : null}

            {busy ? <ActivityIndicator style={{ marginTop: 16 }} color={c.accent} /> : null}
            {msg ? <Text style={{ color: c.textMuted, marginTop: 16 }}>{msg}</Text> : null}
          </>
        ) : (
          <Text style={{ color: c.textMuted }}>Pick the hub device that runs the agent next to your AV gear.</Text>
        )}
      </ScrollView>
    </View>
  );
}
