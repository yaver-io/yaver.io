// Store Studio — generate App Store / Play Store assets for your app.
// Today: the permission-justification path — analyze an app's permission usage
// on a device that holds its repo (your own box, on-prem & free, or a
// Yaver-managed-cloud box) and produce the Play Console prose + demo-video
// shot-list. The capture/record path (redroid surface) is driven agent-side.
import React, { useEffect, useState } from "react";
import { ActivityIndicator, Pressable, ScrollView, Text, TextInput, View } from "react-native";
import { useRouter } from "expo-router";
import { AppScreenHeader } from "../src/components/AppScreenHeader";
import { useColors } from "../src/context/ThemeContext";
import { useDevice } from "../src/context/DeviceContext";
import {
  studioClient,
  getStudioDeviceId,
  setStudioDeviceId,
  type PermissionProse,
  type StudioJob,
  type StudioTarget,
} from "../src/lib/studioClient";

const COMMON_PERMS = [
  "FOREGROUND_SERVICE_SPECIAL_USE",
  "FOREGROUND_SERVICE_DATA_SYNC",
  "FOREGROUND_SERVICE_LOCATION",
  "FOREGROUND_SERVICE_MEDIA_PLAYBACK",
];

export default function StudioScreen() {
  const c = useColors();
  const router = useRouter();
  const deviceCtx = useDevice();
  const devices = ((deviceCtx as any).devices as any[]) || [];

  const [deviceId, setDeviceId] = useState("");
  const [permission, setPermission] = useState(COMMON_PERMS[0]);
  const [path, setPath] = useState("");
  const [app, setApp] = useState("");
  const [what, setWhat] = useState("");
  const [busy, setBusy] = useState(false);
  const [res, setRes] = useState<PermissionProse | null>(null);
  const [err, setErr] = useState<string | null>(null);

  // Record-video mode (the agentic capture job + live status).
  const [apk, setApk] = useState("");
  const [hostWorkDir, setHostWorkDir] = useState("");
  const [sshHost, setSshHost] = useState("");
  const [startAction, setStartAction] = useState("");
  const [job, setJob] = useState<StudioJob | null>(null);

  useEffect(() => {
    getStudioDeviceId().then((id) => {
      if (id) setDeviceId(id);
    });
  }, []);

  // Poll job status while a capture is in flight so the user sees live progress.
  useEffect(() => {
    if (!job?.id || job.state === "completed" || job.state === "failed") return;
    const target: StudioTarget = { id: deviceId };
    const t = setInterval(async () => {
      try {
        const s = await studioClient.jobStatus(target, job.id);
        if (s && (s.id || s.state)) setJob(s);
      } catch {
        /* keep polling */
      }
    }, 3000);
    return () => clearInterval(t);
  }, [job?.id, job?.state, deviceId]);

  const startRecord = async () => {
    if (!deviceId) {
      setErr("Pick a device first.");
      return;
    }
    if (!apk.trim() || !hostWorkDir.trim()) {
      setErr("Recording needs the APK path and a host work dir on the device.");
      return;
    }
    setErr(null);
    setJob(null);
    try {
      const target: StudioTarget = { id: deviceId };
      const j = await studioClient.startPermissionJob(target, {
        permission,
        apk: apk.trim(),
        hostWorkDir: hostWorkDir.trim(),
        path: path.trim() || undefined,
        startAction: startAction.trim() || undefined,
        sshHost: sshHost.trim() || undefined,
        app: app.trim() || undefined,
        what: what.trim() || undefined,
      });
      if (j?.ok === false) setErr(j.error || "failed to start");
      else setJob(j);
    } catch (e: any) {
      setErr(String(e?.message || e));
    }
  };

  const pickDevice = (id: string) => {
    setDeviceId(id);
    setStudioDeviceId(id);
  };

  const generate = async () => {
    if (!deviceId) {
      setErr("Pick a device that has your app's repo first.");
      return;
    }
    setBusy(true);
    setErr(null);
    setRes(null);
    try {
      const target: StudioTarget = { id: deviceId };
      const r = await studioClient.permissionProse(target, {
        permission,
        path: path.trim() || undefined,
        app: app.trim() || undefined,
        what: what.trim() || undefined,
      });
      if (r?.ok === false) setErr(r.error || "failed");
      else setRes(r);
    } catch (e: any) {
      setErr(String(e?.message || e));
    } finally {
      setBusy(false);
    }
  };

  const label = { color: c.textMuted, fontSize: 12, marginTop: 14, marginBottom: 4 } as const;
  const input = {
    backgroundColor: c.bgCard,
    borderColor: c.border,
    borderWidth: 1,
    borderRadius: 8,
    color: c.textPrimary,
    paddingHorizontal: 12,
    paddingVertical: 10,
  } as const;

  return (
    <View style={{ flex: 1, backgroundColor: c.bg }}>
      <AppScreenHeader title="Store Studio" onBack={() => router.back()} />
      <ScrollView contentContainerStyle={{ padding: 16, paddingBottom: 60 }}>
        <Text style={{ color: c.textMuted, fontSize: 13 }}>
          Generate a Play Console permission justification (prose + demo-video shot-list) for your app.
        </Text>

        <Text style={label}>Device with your app's repo</Text>
        <ScrollView horizontal showsHorizontalScrollIndicator={false}>
          {devices.map((d) => {
            const id = d.deviceId || d.id;
            const on = id === deviceId;
            return (
              <Pressable
                key={id}
                onPress={() => pickDevice(id)}
                style={{
                  backgroundColor: on ? c.accent : c.bgCard,
                  borderColor: c.border,
                  borderWidth: 1,
                  borderRadius: 8,
                  paddingHorizontal: 12,
                  paddingVertical: 8,
                  marginRight: 8,
                }}
              >
                <Text style={{ color: on ? "#fff" : c.textPrimary, fontSize: 13 }}>{d.name || id}</Text>
                <Text style={{ color: on ? "#fff" : c.textMuted, fontSize: 10 }}>{d.platform || ""} · {d.status || ""}</Text>
              </Pressable>
            );
          })}
        </ScrollView>

        <Text style={label}>Permission</Text>
        <ScrollView horizontal showsHorizontalScrollIndicator={false}>
          {COMMON_PERMS.map((p) => (
            <Pressable
              key={p}
              onPress={() => setPermission(p)}
              style={{
                backgroundColor: p === permission ? c.accent : c.bgCard,
                borderColor: c.border, borderWidth: 1, borderRadius: 8,
                paddingHorizontal: 10, paddingVertical: 6, marginRight: 8,
              }}
            >
              <Text style={{ color: p === permission ? "#fff" : c.textPrimary, fontSize: 11 }}>
                {p.replace("FOREGROUND_SERVICE_", "FGS_")}
              </Text>
            </Pressable>
          ))}
        </ScrollView>
        <TextInput style={[input, { marginTop: 8 }]} value={permission} onChangeText={setPermission} autoCapitalize="characters" />

        <Text style={label}>Project path on the device (optional)</Text>
        <TextInput style={input} value={path} onChangeText={setPath} placeholder="/home/you/myapp" placeholderTextColor={c.textMuted} autoCapitalize="none" />

        <Text style={label}>App name (optional)</Text>
        <TextInput style={input} value={app} onChangeText={setApp} placeholder="My App" placeholderTextColor={c.textMuted} />

        <Text style={label}>What the service does (optional)</Text>
        <TextInput style={input} value={what} onChangeText={setWhat} placeholder="an on-device sync engine the user starts" placeholderTextColor={c.textMuted} />

        <Pressable
          onPress={generate}
          disabled={busy}
          style={{ backgroundColor: c.accent, borderRadius: 10, paddingVertical: 14, alignItems: "center", marginTop: 20, opacity: busy ? 0.6 : 1 }}
        >
          {busy ? <ActivityIndicator color="#fff" /> : <Text style={{ color: "#fff", fontWeight: "600" }}>Generate justification</Text>}
        </Pressable>

        {err && (
          <View style={{ backgroundColor: "#3a1212", borderRadius: 8, padding: 12, marginTop: 16 }}>
            <Text style={{ color: "#ffb4b4", fontSize: 13 }}>{err}</Text>
          </View>
        )}

        {res && (
          <View style={{ marginTop: 16 }}>
            {res.service ? (
              <Text style={{ color: c.textMuted, fontSize: 12, marginBottom: 8 }}>
                service: {res.service} · type: {res.fgsType}{res.subtype ? ` · ${res.subtype}` : ""}{res.trigger ? `\ntrigger: ${res.trigger}` : ""}
              </Text>
            ) : null}

            {res.warnings && res.warnings.length > 0 && (
              <View style={{ backgroundColor: "#3a2e12", borderRadius: 8, padding: 12, marginBottom: 12 }}>
                {res.warnings.map((w, i) => (
                  <Text key={i} style={{ color: "#ffd98a", fontSize: 12 }}>⚠ {w}</Text>
                ))}
              </View>
            )}

            <Text style={label}>"What tasks" → Other</Text>
            <Text selectable style={{ color: c.textPrimary, fontSize: 14, lineHeight: 20 }}>{res.taskOther}</Text>

            <Text style={label}>Describe your app's use</Text>
            <Text selectable style={{ color: c.textPrimary, fontSize: 14, lineHeight: 20 }}>{res.description}</Text>

            <Text style={label}>Demo video shot-list</Text>
            {(res.shotList || []).map((s, i) => (
              <Text key={i} style={{ color: c.textPrimary, fontSize: 14, lineHeight: 22 }}>• {s}</Text>
            ))}
          </View>
        )}

        {/* Record demo video — async capture job with live status */}
        <View style={{ marginTop: 28, borderTopWidth: 1, borderTopColor: c.border, paddingTop: 16 }}>
          <Text style={{ color: c.textPrimary, fontSize: 15, fontWeight: "600" }}>Record the demo video</Text>
          <Text style={{ color: c.textMuted, fontSize: 12, marginTop: 2 }}>
            Drives a redroid surface on the device to record the proof video. Leave SSH host empty for a Yaver-managed-cloud box (agent runs there); set it for an on-prem box.
          </Text>

          <Text style={label}>APK path (built for the surface arch)</Text>
          <TextInput style={input} value={apk} onChangeText={setApk} placeholder="/home/you/app-x86_64.apk" placeholderTextColor={c.textMuted} autoCapitalize="none" />
          <Text style={label}>Host work dir (redroid /data mount)</Text>
          <TextInput style={input} value={hostWorkDir} onChangeText={setHostWorkDir} placeholder="/home/you/redroid-data" placeholderTextColor={c.textMuted} autoCapitalize="none" />
          <Text style={label}>SSH host (on-prem only, optional)</Text>
          <TextInput style={input} value={sshHost} onChangeText={setSshHost} placeholder="user@10.0.0.45" placeholderTextColor={c.textMuted} autoCapitalize="none" />
          <Text style={label}>FGS start action (optional)</Text>
          <TextInput style={input} value={startAction} onChangeText={setStartAction} placeholder="io.yaver.mobile.sandbox.START" placeholderTextColor={c.textMuted} autoCapitalize="none" />

          <Pressable
            onPress={startRecord}
            disabled={!!job && job.state !== "completed" && job.state !== "failed"}
            style={{ backgroundColor: c.accent, borderRadius: 10, paddingVertical: 14, alignItems: "center", marginTop: 16, opacity: job && job.state !== "completed" && job.state !== "failed" ? 0.6 : 1 }}
          >
            <Text style={{ color: "#fff", fontWeight: "600" }}>
              {job && (job.state === "running" || job.state === "queued") ? "Recording…" : "Record demo video"}
            </Text>
          </Pressable>

          {job && (
            <View style={{ backgroundColor: c.bgCard, borderColor: c.border, borderWidth: 1, borderRadius: 10, padding: 12, marginTop: 14 }}>
              <View style={{ flexDirection: "row", alignItems: "center" }}>
                {(job.state === "running" || job.state === "queued") && <ActivityIndicator size="small" color={c.accent} style={{ marginRight: 8 }} />}
                <Text style={{ color: c.textPrimary, fontWeight: "600", fontSize: 13 }}>
                  {job.state === "completed" ? "✓ Done" : job.state === "failed" ? "✗ Failed" : `${job.phase || job.state || "starting"}…`}
                </Text>
                {typeof job.durationSec === "number" && (
                  <Text style={{ color: c.textMuted, fontSize: 12, marginLeft: 8 }}>{job.durationSec}s</Text>
                )}
              </View>
              {job.error ? <Text style={{ color: "#ffb4b4", fontSize: 12, marginTop: 6 }}>{job.error}</Text> : null}
              {(job.log || []).slice(-8).map((l, i) => (
                <Text key={i} style={{ color: c.textMuted, fontSize: 11, fontFamily: "Menlo", marginTop: 2 }}>{l}</Text>
              ))}
              {job.state === "completed" && job.artifacts && (
                <View style={{ marginTop: 8 }}>
                  <Text style={{ color: c.accent, fontSize: 12 }}>
                    {job.artifacts.captionedMp4 || job.artifacts.mp4}
                  </Text>
                  <Text style={{ color: c.textMuted, fontSize: 11 }}>
                    {job.artifacts.captionCount ? `${job.artifacts.captionCount} captions · ` : ""}saved on the device
                  </Text>
                </View>
              )}
            </View>
          )}
        </View>
      </ScrollView>
    </View>
  );
}
