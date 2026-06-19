import React, { useCallback, useEffect, useMemo, useState } from "react";
import { ActivityIndicator, Pressable, ScrollView, Text, TextInput, View } from "react-native";
import { useRouter } from "expo-router";
import { AppScreenHeader } from "../src/components/AppScreenHeader";
import { useColors } from "../src/context/ThemeContext";
import { useDevice } from "../src/context/DeviceContext";
import {
  getTwinDeviceId,
  setTwinDeviceId,
  twinClient,
  type TwinJob,
  type TwinSurface,
  type TwinTarget,
} from "../src/lib/twinClient";

const SURFACES: { key: TwinSurface; label: string }[] = [
  { key: "android-redroid", label: "Android redroid" },
  { key: "web-playwright", label: "Web Playwright" },
  { key: "web-chromedp", label: "Web ChromeDP" },
];

export default function TwinScreen() {
  const c = useColors();
  const router = useRouter();
  const deviceCtx = useDevice();
  const devices = ((deviceCtx as any).devices || []) as any[];

  const [deviceId, setDeviceId] = useState("");
  const [surface, setSurface] = useState<TwinSurface>("android-redroid");
  const [job, setJob] = useState<TwinJob | null>(null);
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState<string | null>(null);

  const [apk, setApk] = useState("");
  const [pkg, setPkg] = useState("io.yaver.mobile");
  const [activity, setActivity] = useState(".MainActivity");
  const [hostWorkDir, setHostWorkDir] = useState("/tmp/yaver-redroid-data");
  const [sshHost, setSshHost] = useState("");
  const [workDir, setWorkDir] = useState("");
  const [url, setUrl] = useState("https://example.com");
  const [remoteDebuggingUrl, setRemoteDebuggingUrl] = useState("");
  const [stepsText, setStepsText] = useState("");
  const [maxSec, setMaxSec] = useState("90");

  useEffect(() => {
    getTwinDeviceId().then((id) => id && setDeviceId(id));
  }, []);

  useEffect(() => {
    if (deviceId) setTwinDeviceId(deviceId);
  }, [deviceId]);

  const target = useCallback((): TwinTarget | undefined => {
    if (!deviceId) return undefined;
    const d = devices.find((x) => x.id === deviceId || x.deviceId === deviceId);
    return { id: deviceId, lanIps: d?.lanIps || d?.localIps, host: d?.host, port: 18080 };
  }, [deviceId, devices]);

  const running = job?.state === "queued" || job?.state === "running";

  useEffect(() => {
    if (!running || !job?.id) return;
    const t = target();
    if (!t) return;
    const timer = setInterval(async () => {
      const next = await twinClient.status(t, job.id);
      setJob(next);
    }, 2500);
    return () => clearInterval(timer);
  }, [running, job?.id, target]);

  const parsedSteps = useMemo(() => {
    const raw = stepsText.trim();
    if (!raw) return [];
    try {
      const parsed = JSON.parse(raw);
      return Array.isArray(parsed) ? parsed : [];
    } catch {
      return [];
    }
  }, [stepsText]);

  const start = async () => {
    const t = target();
    if (!t) {
      setMsg("Pick a remote dev machine first.");
      return;
    }
    if (stepsText.trim() && parsedSteps.length === 0) {
      setMsg("Steps must be a JSON array.");
      return;
    }
    setBusy(true);
    setMsg(null);
    const res = await twinClient.start(t, {
      surface,
      mode: "scripted",
      record: true,
      maxSec: Number(maxSec) || 90,
      sshHost: sshHost.trim() || undefined,
      workDir: workDir.trim() || undefined,
      apk: apk.trim() || undefined,
      package: pkg.trim() || undefined,
      activity: activity.trim() || undefined,
      hostWorkDir: hostWorkDir.trim() || undefined,
      url: url.trim() || undefined,
      remoteDebuggingUrl: remoteDebuggingUrl.trim() || undefined,
      trace: surface === "web-playwright",
      steps: parsedSteps,
    });
    setBusy(false);
    setJob(res);
    if (res?.ok === false || res?.error) setMsg(res.error || "Twin job failed to start.");
  };

  const card = { backgroundColor: c.bgCard, borderColor: c.borderSubtle, borderWidth: 1, borderRadius: 10, padding: 14, marginBottom: 12 };
  const label = { color: c.textSecondary, fontSize: 12, marginBottom: 6 };
  const input = { backgroundColor: c.bg, borderColor: c.borderSubtle, borderWidth: 1, borderRadius: 8, padding: 10, color: c.textPrimary, marginBottom: 10 };
  const pill = (active: boolean) => ({
    paddingVertical: 8,
    paddingHorizontal: 12,
    borderRadius: 8,
    borderWidth: 1,
    borderColor: active ? c.accent : c.borderSubtle,
    backgroundColor: active ? c.accent : "transparent",
    marginRight: 8,
    marginBottom: 8,
  });

  return (
    <View style={{ flex: 1, backgroundColor: c.bg }}>
      <AppScreenHeader title="Twin Mode" onBack={() => router.back()} />
      <ScrollView contentContainerStyle={{ padding: 16, paddingBottom: 36 }}>
        <View style={card}>
          <Text style={{ color: c.textPrimary, fontSize: 15, fontWeight: "700", marginBottom: 10 }}>Remote machine</Text>
          <Text style={label}>Device id</Text>
          <TextInput style={input} value={deviceId} onChangeText={setDeviceId} placeholder="pick or paste a device id" placeholderTextColor={c.textMuted} autoCapitalize="none" />
          <View style={{ flexDirection: "row", flexWrap: "wrap" }}>
            {devices.slice(0, 8).map((d) => {
              const id = d.id || d.deviceId;
              if (!id) return null;
              return (
                <Pressable key={id} onPress={() => setDeviceId(id)} style={pill(deviceId === id)}>
                  <Text style={{ color: deviceId === id ? "#fff" : c.textPrimary, fontSize: 12 }}>{d.name || id.slice(0, 10)}</Text>
                </Pressable>
              );
            })}
          </View>
          <Text style={label}>SSH host for nested remote runner</Text>
          <TextInput style={input} value={sshHost} onChangeText={setSshHost} placeholder="optional user@host" placeholderTextColor={c.textMuted} autoCapitalize="none" />
          <Text style={label}>Remote work dir</Text>
          <TextInput style={input} value={workDir} onChangeText={setWorkDir} placeholder="optional; defaults to ~/.yaver/twin/job" placeholderTextColor={c.textMuted} autoCapitalize="none" />
        </View>

        <View style={card}>
          <Text style={{ color: c.textPrimary, fontSize: 15, fontWeight: "700", marginBottom: 10 }}>Surface</Text>
          <View style={{ flexDirection: "row", flexWrap: "wrap" }}>
            {SURFACES.map((s) => (
              <Pressable key={s.key} onPress={() => setSurface(s.key)} style={pill(surface === s.key)}>
                <Text style={{ color: surface === s.key ? "#fff" : c.textPrimary, fontSize: 12 }}>{s.label}</Text>
              </Pressable>
            ))}
          </View>

          {surface === "android-redroid" ? (
            <View>
              <Text style={label}>APK path on controller/agent</Text>
              <TextInput style={input} value={apk} onChangeText={setApk} placeholder="/path/to/app-x86_64.apk" placeholderTextColor={c.textMuted} autoCapitalize="none" />
              <Text style={label}>Package</Text>
              <TextInput style={input} value={pkg} onChangeText={setPkg} placeholder="io.yaver.mobile" placeholderTextColor={c.textMuted} autoCapitalize="none" />
              <Text style={label}>Activity</Text>
              <TextInput style={input} value={activity} onChangeText={setActivity} placeholder=".MainActivity" placeholderTextColor={c.textMuted} autoCapitalize="none" />
              <Text style={label}>Redroid host work dir</Text>
              <TextInput style={input} value={hostWorkDir} onChangeText={setHostWorkDir} placeholder="/tmp/yaver-redroid-data" placeholderTextColor={c.textMuted} autoCapitalize="none" />
            </View>
          ) : (
            <View>
              <Text style={label}>URL</Text>
              <TextInput style={input} value={url} onChangeText={setUrl} placeholder="https://..." placeholderTextColor={c.textMuted} autoCapitalize="none" />
              {surface === "web-chromedp" ? (
                <>
                  <Text style={label}>Remote debugging URL</Text>
                  <TextInput
                    style={input}
                    value={remoteDebuggingUrl}
                    onChangeText={setRemoteDebuggingUrl}
                    placeholder="optional http://127.0.0.1:9222"
                    placeholderTextColor={c.textMuted}
                    autoCapitalize="none"
                  />
                </>
              ) : null}
            </View>
          )}

          <Text style={label}>Max seconds</Text>
          <TextInput style={input} value={maxSec} onChangeText={setMaxSec} keyboardType="number-pad" />
        </View>

        <View style={card}>
          <Text style={{ color: c.textPrimary, fontSize: 15, fontWeight: "700", marginBottom: 10 }}>Scripted flow</Text>
          <Text style={label}>Steps JSON</Text>
          <TextInput
            style={[input, { minHeight: 120, textAlignVertical: "top", fontFamily: "Menlo", fontSize: 12 }]}
            value={stepsText}
            onChangeText={setStepsText}
            multiline
            placeholder={'[{"action":"tapText","text":"Settings"},{"action":"expand_notifications","holdSec":4}]'}
            placeholderTextColor={c.textMuted}
            autoCapitalize="none"
          />
          <Pressable
            onPress={start}
            disabled={busy || running}
            style={{ backgroundColor: c.accent, borderRadius: 10, paddingVertical: 14, alignItems: "center", opacity: busy || running ? 0.6 : 1 }}
          >
            <Text style={{ color: "#fff", fontWeight: "700" }}>{busy || running ? "Running" : "Start remote recording"}</Text>
          </Pressable>
          {msg ? <Text style={{ color: "#ffb4b4", marginTop: 10, fontSize: 12 }}>{msg}</Text> : null}
        </View>

        {job ? (
          <View style={card}>
            <View style={{ flexDirection: "row", alignItems: "center", marginBottom: 8 }}>
              {running ? <ActivityIndicator size="small" color={c.accent} style={{ marginRight: 8 }} /> : null}
              <Text style={{ color: c.textPrimary, fontWeight: "700" }}>
                {job.state === "completed" ? "Complete" : job.state === "failed" ? "Failed" : job.phase || job.state || "Starting"}
              </Text>
              {typeof job.durationSec === "number" ? <Text style={{ color: c.textMuted, marginLeft: 8, fontSize: 12 }}>{job.durationSec}s</Text> : null}
            </View>
            {job.error ? <Text style={{ color: "#ffb4b4", fontSize: 12, marginBottom: 8 }}>{job.error}</Text> : null}
            {(job.log || []).slice(-10).map((line, i) => (
              <Text key={i} style={{ color: c.textMuted, fontFamily: "Menlo", fontSize: 11, marginTop: 2 }}>{line}</Text>
            ))}
            {job.artifacts ? (
              <View style={{ marginTop: 10 }}>
                {job.artifacts.video ? <Text style={{ color: c.accent, fontSize: 12 }}>video: {job.artifacts.video}</Text> : null}
                {job.artifacts.trace ? <Text style={{ color: c.accent, fontSize: 12 }}>trace: {job.artifacts.trace}</Text> : null}
                {job.artifacts.frames ? <Text style={{ color: c.accent, fontSize: 12 }}>frames: {job.artifacts.frames}</Text> : null}
                {job.artifacts.logs ? <Text style={{ color: c.accent, fontSize: 12 }}>logs: {job.artifacts.logs}</Text> : null}
                {job.artifacts.crash ? <Text style={{ color: "#ffb4b4", fontSize: 12 }}>crash: {job.artifacts.crash}</Text> : null}
                {job.artifacts.dir ? <Text style={{ color: c.textMuted, fontSize: 11 }}>dir: {job.artifacts.dir}</Text> : null}
              </View>
            ) : null}
          </View>
        ) : null}
      </ScrollView>
    </View>
  );
}
