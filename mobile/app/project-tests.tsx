// Project Tests — from the Projects tab, run a project's web tests on a chosen
// REMOTE PC via chromedp YAML, Playwright YAML, or native Playwright, watch the
// live log, and read a feature-based report. Mirrors qa.tsx's transport pattern.
import React, { useEffect, useState } from "react";
import { ActivityIndicator, Image, Pressable, ScrollView, Text, TextInput, View } from "react-native";
import { ResizeMode, Video } from "expo-av";
import * as FileSystem from "expo-file-system/legacy";
import { useLocalSearchParams, useRouter } from "expo-router";
import { AppScreenHeader } from "../src/components/AppScreenHeader";
import { useColors } from "../src/context/ThemeContext";
import { useDevice } from "../src/context/DeviceContext";
import {
  testkitClient,
  type TKArtifactRef,
  type TKFeature,
  type TKJob,
  type TKPlaywrightStatus,
  type TKQualityReport,
  type TKReport,
  type TKTarget,
  type TKTraceInspect,
  type TKGrowPlan,
} from "../src/lib/testkitClient";

type RunMode = "chromedp" | "playwright-yaml" | "playwright-native";

export default function ProjectTestsScreen() {
  const c = useColors();
  const router = useRouter();
  const params = useLocalSearchParams<{ project?: string; path?: string }>();
  const project = (params.project as string) || "";
  const dir = (params.path as string) || "";

  const deviceCtx = useDevice();
  const devices = ((deviceCtx as any).devices as any[]) || [];

  const [deviceId, setDeviceId] = useState("");
  const [token, setToken] = useState(""); // optional ${ENV} secret for authed apps
  const [mode, setMode] = useState<RunMode>("playwright-yaml");
  const [profile, setProfile] = useState("");
  const [devCommand, setDevCommand] = useState("");
  const [waitURL, setWaitURL] = useState("");
  const [nativeProject, setNativeProject] = useState("");
  const [nativeGrep, setNativeGrep] = useState("");
  const [trace, setTrace] = useState(true);
  const [pwStatus, setPwStatus] = useState<TKPlaywrightStatus | null>(null);
  const [profiles, setProfiles] = useState<{ name: string }[]>([]);
  const [authURL, setAuthURL] = useState("");
  const [authSuccessURL, setAuthSuccessURL] = useState("");
  const [authJob, setAuthJob] = useState<TKJob | null>(null);
  const [runRedroid, setRunRedroid] = useState(false);
  const [qaPackage, setQAPackage] = useState("");
  const [qaAPK, setQAAPK] = useState("");
  const [qaBase, setQABase] = useState("");
  const [qualityReport, setQualityReport] = useState<TKQualityReport | null>(null);
  const [runs, setRuns] = useState<any[]>([]);
  const [gcResult, setGCResult] = useState<any | null>(null);
  const [busy, setBusy] = useState(false);
  const [job, setJob] = useState<TKJob | null>(null);
  const [report, setReport] = useState<TKReport | null>(null);
  const [grow, setGrow] = useState<TKGrowPlan | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [deps, setDeps] = useState<{ ready?: boolean; deps?: { name: string; present: boolean }[] } | null>(null);
  const [depsBusy, setDepsBusy] = useState(false);

  const target = (): TKTarget | undefined => {
    const d = devices.find((x) => (x.deviceId || x.id) === deviceId);
    if (!d) return deviceId ? { id: deviceId } : undefined;
    return { id: deviceId, lanIps: d.lanIps || d.localIps, host: d.host };
  };

  // Poll while running, then pull the report.
  useEffect(() => {
    if (!job?.id || (job.state !== "running" && job.state !== "queued")) return;
    const t = target();
    if (!t) return;
    const iv = setInterval(async () => {
      const s = await testkitClient.jobStatus(t, job.id);
      setJob(s);
      if (s.state === "completed") {
        if (job.kind === "talos-quality") {
          const qr = await testkitClient.qualityReport(t, job.id!);
          if (!qr?.error) {
            setQualityReport(qr);
            if (qr.web) setReport(qr.web);
          }
        } else {
          const r = await testkitClient.report(t, job.id!);
          if (!r?.error) setReport(r);
        }
      }
    }, 3000);
    return () => clearInterval(iv);
  }, [job?.id, job?.state]);

  // Check deps when a remote PC is picked so the user never fails a run on
  // missing tooling — surface a one-tap installer instead.
  useEffect(() => {
    const t = target();
    if (!t) { setDeps(null); return; }
    let alive = true;
    testkitClient.depsCheck(t).then((d) => { if (alive) setDeps(d as any); }).catch(() => {});
    return () => { alive = false; };
  }, [deviceId]);

  const installDeps = async () => {
    const t = target();
    if (!t) return setErr("Pick a remote PC first.");
    setDepsBusy(true);
    setErr(null);
    const j = await testkitClient.depsInstall(t);
    if (!j?.id) { setDepsBusy(false); return setErr((j as any)?.error || "could not start install"); }
    const iv = setInterval(async () => {
      const s = await testkitClient.jobStatus(t, j.id);
      if (s.state === "completed" || s.state === "failed") {
        clearInterval(iv);
        setDepsBusy(false);
        const d = await testkitClient.depsCheck(t);
        setDeps(d as any);
      }
    }, 3000);
  };

  const envFor = () => {
    const e: Record<string, string> = {};
    const tok = token.trim();
    if (tok) e.TALOS_SESSION_TOKEN = tok; // common case; specs can read any ${ENV}
    return e;
  };

  const run = async () => {
    const t = target();
    if (!t) return setErr("Pick a remote PC that holds the project repo first.");
    if (!dir) return setErr("No project path — open this from the Projects tab.");
    setBusy(true);
    setErr(null);
    setReport(null);
    setQualityReport(null);
    setGrow(null);
    const base = {
      project,
      dir,
      env: envFor(),
      video: true,
      trace,
      profile: profile.trim() || undefined,
      devCommand: devCommand.trim() || undefined,
      waitURL: waitURL.trim() || undefined,
    };
    const nativeArgs = {
      dir,
      project: nativeProject.trim() || undefined,
      grep: nativeGrep.trim() || undefined,
      trace: trace ? "retain-on-failure" : "off",
      devCommand: devCommand.trim() || undefined,
      waitURL: waitURL.trim() || undefined,
      env: envFor(),
    };
    const j = mode === "chromedp"
      ? await testkitClient.run(t, base)
      : mode === "playwright-native"
        ? await testkitClient.playwrightNativeRun(t, nativeArgs)
        : await testkitClient.playwrightRun(t, base);
    setBusy(false);
    if ((j as any)?.error || !j?.id) return setErr((j as any)?.error || "could not start run");
    setJob(j);
  };

  const checkPlaywright = async () => {
    const t = target();
    if (!t) return setErr("Pick a remote PC first.");
    setBusy(true);
    setErr(null);
    const s = await testkitClient.playwrightStatus(t, dir);
    setBusy(false);
    if ((s as any)?.error) return setErr((s as any).error);
    setPwStatus(s);
  };

  const repairPlaywright = async () => {
    const t = target();
    if (!t) return setErr("Pick a remote PC first.");
    setBusy(true);
    setErr(null);
    const j = await testkitClient.playwrightRepair(t, ["node", "playwright", "ffmpeg"]);
    setBusy(false);
    if ((j as any)?.error || !j?.id) return setErr((j as any)?.error || "could not start Playwright repair");
    setJob(j);
  };

  const loadProfiles = async () => {
    const t = target();
    if (!t) return setErr("Pick a remote PC first.");
    setBusy(true);
    setErr(null);
    const p = await testkitClient.playwrightProfiles(t);
    setBusy(false);
    if ((p as any)?.error) return setErr((p as any).error);
    setProfiles(p.profiles || []);
  };

  const startProfileAuth = async () => {
    const t = target();
    if (!t) return setErr("Pick a remote PC first.");
    if (!profile.trim()) return setErr("Profile name is required.");
    if (!authURL.trim()) return setErr("Login URL is required.");
    setBusy(true);
    setErr(null);
    const j = await testkitClient.playwrightProfileAuth(t, { dir, url: authURL.trim(), successURL: authSuccessURL.trim() || undefined, profile: profile.trim(), timeoutSec: 300 });
    setBusy(false);
    if ((j as any)?.error || !j?.id) return setErr((j as any)?.error || "could not start profile auth");
    setAuthJob(j);
  };

  const signalProfileAuth = async (signal: "finish" | "cancel") => {
    const t = target();
    if (!t || !authJob?.id) return;
    setBusy(true);
    setErr(null);
    const j = signal === "finish"
      ? await testkitClient.playwrightProfileAuthFinish(t, authJob.id)
      : await testkitClient.playwrightProfileAuthCancel(t, authJob.id);
    const s = await testkitClient.jobStatus(t, authJob.id);
    setBusy(false);
    if ((j as any)?.error) return setErr((j as any).error);
    setAuthJob(s);
    if (signal === "finish") await loadProfiles();
  };

  const loadRuns = async () => {
    const t = target();
    if (!t) return setErr("Pick a remote PC first.");
    setBusy(true);
    const r = await testkitClient.playwrightRuns(t, 20);
    setBusy(false);
    if ((r as any)?.error) return setErr((r as any).error);
    setRuns(r.runs || []);
  };

  const gcRuns = async () => {
    const t = target();
    if (!t) return setErr("Pick a remote PC first.");
    setBusy(true);
    const r = await testkitClient.playwrightGC(t, { olderThanHours: 168, dryRun: true });
    const rr = await testkitClient.playwrightRuns(t, 20);
    setBusy(false);
    if ((r as any)?.error) return setErr((r as any).error);
    setGCResult(r);
    setRuns(rr.runs || []);
  };

  const runQuality = async () => {
    const t = target();
    if (!t) return setErr("Pick a remote PC that holds the project repo first.");
    if (!dir) return setErr("No project path — open this from the Projects tab.");
    setBusy(true);
    setErr(null);
    setReport(null);
    setQualityReport(null);
    setGrow(null);
    const browser = {
      project,
      dir,
      env: envFor(),
      video: true,
      trace,
      profile: profile.trim() || undefined,
      devCommand: devCommand.trim() || undefined,
      waitURL: waitURL.trim() || undefined,
    };
    const native = {
      dir,
      project: nativeProject.trim() || undefined,
      grep: nativeGrep.trim() || undefined,
      trace: trace ? "retain-on-failure" : "off",
      devCommand: devCommand.trim() || undefined,
      waitURL: waitURL.trim() || undefined,
      env: envFor(),
    };
    const j = await testkitClient.qualityRun(t, {
      browserMode: mode,
      browser,
      native,
      runQA: runRedroid,
      qa: {
        package: qaPackage.trim() || undefined,
        apk: qaAPK.trim() || undefined,
        base: qaBase.trim() || undefined,
        mode: "catch",
      },
    });
    setBusy(false);
    if ((j as any)?.error || !j?.id) return setErr((j as any)?.error || "could not start quality run");
    setJob(j);
  };

  const doGrow = async () => {
    const t = target();
    if (!t) return setErr("Pick a remote PC first.");
    if (!dir) return setErr("No project path.");
    setBusy(true);
    setErr(null);
    const plan = await testkitClient.grow(t, dir, { apply: true, author: true });
    setBusy(false);
    if ((plan as any)?.error) return setErr((plan as any).error);
    setGrow(plan);
  };

  const running = job?.state === "running" || job?.state === "queued";
  const card = { backgroundColor: c.bgCard, borderColor: c.border, borderWidth: 1, borderRadius: 10, padding: 12 } as const;
  const btn = (bg: string) => ({ backgroundColor: bg, borderRadius: 8, paddingVertical: 12, paddingHorizontal: 16, alignItems: "center" } as const);

  return (
    <View style={{ flex: 1, backgroundColor: c.bg }}>
      <AppScreenHeader title="Project Tests" onBack={() => router.back()} />
      <ScrollView contentContainerStyle={{ padding: 16, gap: 14 }}>
        <View style={card}>
          <Text style={{ color: c.textPrimary, fontWeight: "700", fontSize: 16 }}>{project || "Project"}</Text>
          <Text style={{ color: c.textMuted, fontSize: 12, marginTop: 2 }}>{dir || "(no path)"}</Text>
        </View>

        {/* Remote PC picker — the device whose agent runs the suite */}
        <View style={{ gap: 6 }}>
          <Text style={{ color: c.textMuted, fontSize: 13 }}>Remote PC (runner)</Text>
          <View style={{ flexDirection: "row", flexWrap: "wrap", gap: 8 }}>
            {devices.map((d) => {
              const id = d.deviceId || d.id;
              const on = id === deviceId;
              return (
                <Pressable key={id} onPress={() => setDeviceId(id)}
                  style={{ backgroundColor: on ? c.accent : c.bgCard, borderRadius: 8, paddingVertical: 8, paddingHorizontal: 12, borderWidth: 1, borderColor: c.border }}>
                  <Text style={{ color: on ? "#fff" : c.textPrimary, fontSize: 13 }}>{d.name || id}</Text>
                </Pressable>
              );
            })}
            {devices.length === 0 && <Text style={{ color: c.textMuted, fontSize: 12 }}>No paired devices.</Text>}
          </View>
        </View>

        {/* Optional auth token for logged-in app tests (injected as ${ENV}) */}
        <View style={{ gap: 6 }}>
          <Text style={{ color: c.textMuted, fontSize: 13 }}>Session token (optional — for authed pages)</Text>
          <TextInput value={token} onChangeText={setToken} placeholder="TALOS_SESSION_TOKEN…" placeholderTextColor={c.textMuted}
            autoCapitalize="none" secureTextEntry
            style={{ color: c.textPrimary, backgroundColor: c.bgCard, borderColor: c.border, borderWidth: 1, borderRadius: 8, padding: 10 }} />
        </View>

        <View style={{ flexDirection: "row", flexWrap: "wrap", gap: 8 }}>
          <ModePill label="YAML chromedp" active={mode === "chromedp"} onPress={() => setMode("chromedp")} c={c} />
          <ModePill label="YAML Playwright" active={mode === "playwright-yaml"} onPress={() => setMode("playwright-yaml")} c={c} />
          <ModePill label="Native Playwright" active={mode === "playwright-native"} onPress={() => setMode("playwright-native")} c={c} />
        </View>

        {mode !== "chromedp" ? (
          <View style={{ gap: 10 }}>
            <TextInput value={profile} onChangeText={setProfile} placeholder="Profile name" placeholderTextColor={c.textMuted}
              autoCapitalize="none"
              style={{ color: c.textPrimary, backgroundColor: c.bgCard, borderColor: c.border, borderWidth: 1, borderRadius: 8, padding: 10 }} />
            <TextInput value={devCommand} onChangeText={setDevCommand} placeholder="Dev command, e.g. npm run dev" placeholderTextColor={c.textMuted}
              autoCapitalize="none"
              style={{ color: c.textPrimary, backgroundColor: c.bgCard, borderColor: c.border, borderWidth: 1, borderRadius: 8, padding: 10 }} />
            <TextInput value={waitURL} onChangeText={setWaitURL} placeholder="Wait URL, e.g. http://127.0.0.1:3000" placeholderTextColor={c.textMuted}
              autoCapitalize="none"
              style={{ color: c.textPrimary, backgroundColor: c.bgCard, borderColor: c.border, borderWidth: 1, borderRadius: 8, padding: 10 }} />
            {mode === "playwright-native" ? (
              <View style={{ flexDirection: "row", gap: 8 }}>
                <TextInput value={nativeProject} onChangeText={setNativeProject} placeholder="Project" placeholderTextColor={c.textMuted}
                  autoCapitalize="none"
                  style={{ flex: 1, color: c.textPrimary, backgroundColor: c.bgCard, borderColor: c.border, borderWidth: 1, borderRadius: 8, padding: 10 }} />
                <TextInput value={nativeGrep} onChangeText={setNativeGrep} placeholder="Grep" placeholderTextColor={c.textMuted}
                  autoCapitalize="none"
                  style={{ flex: 1, color: c.textPrimary, backgroundColor: c.bgCard, borderColor: c.border, borderWidth: 1, borderRadius: 8, padding: 10 }} />
              </View>
            ) : null}
            <Pressable onPress={() => setTrace((v) => !v)} style={{ alignSelf: "flex-start", backgroundColor: trace ? c.accent : c.bgCard, borderWidth: 1, borderColor: c.border, borderRadius: 8, paddingVertical: 8, paddingHorizontal: 12 }}>
              <Text style={{ color: trace ? "#fff" : c.textPrimary, fontWeight: "700" }}>{trace ? "Trace on" : "Trace off"}</Text>
            </Pressable>
            <View style={{ flexDirection: "row", flexWrap: "wrap", gap: 8 }}>
              <Pressable disabled={busy || running} onPress={checkPlaywright} style={[btn(c.bgCard), { borderWidth: 1, borderColor: c.border, opacity: busy ? 0.6 : 1 }]}>
                <Text style={{ color: c.textPrimary, fontWeight: "700" }}>Check Playwright</Text>
              </Pressable>
              <Pressable disabled={busy || running} onPress={repairPlaywright} style={[btn(c.bgCard), { borderWidth: 1, borderColor: c.border, opacity: busy ? 0.6 : 1 }]}>
                <Text style={{ color: c.textPrimary, fontWeight: "700" }}>Repair</Text>
              </Pressable>
              <Pressable disabled={busy || running} onPress={loadProfiles} style={[btn(c.bgCard), { borderWidth: 1, borderColor: c.border, opacity: busy ? 0.6 : 1 }]}>
                <Text style={{ color: c.textPrimary, fontWeight: "700" }}>Profiles</Text>
              </Pressable>
            </View>
            {pwStatus ? (
              <Text style={{ color: pwStatus.ready ? "#2fbf71" : "#ffd166", fontSize: 12 }}>
                {pwStatus.ready ? "Playwright ready" : "Playwright needs repair"}{pwStatus.nodeVersion ? ` · ${pwStatus.nodeVersion}` : ""}
              </Text>
            ) : null}
            {profiles.length ? (
              <View style={{ flexDirection: "row", flexWrap: "wrap", gap: 8 }}>
                {profiles.map((p) => (
                  <Pressable key={p.name} onPress={() => setProfile(p.name)}
                    style={{ backgroundColor: p.name === profile ? c.accent : c.bgCard, borderRadius: 8, paddingVertical: 6, paddingHorizontal: 10, borderWidth: 1, borderColor: c.border }}>
                    <Text style={{ color: p.name === profile ? "#fff" : c.textPrimary, fontSize: 12 }}>{p.name}</Text>
                  </Pressable>
                ))}
              </View>
            ) : null}
            <View style={{ backgroundColor: c.bgCard, borderColor: c.border, borderWidth: 1, borderRadius: 10, padding: 10, gap: 8 }}>
              <Text style={{ color: c.textPrimary, fontWeight: "700", fontSize: 13 }}>Playwright Profile Auth</Text>
              <TextInput value={authURL} onChangeText={setAuthURL} placeholder="Login URL" placeholderTextColor={c.textMuted}
                autoCapitalize="none"
                style={{ color: c.textPrimary, backgroundColor: c.bg, borderColor: c.border, borderWidth: 1, borderRadius: 8, padding: 10 }} />
              <TextInput value={authSuccessURL} onChangeText={setAuthSuccessURL} placeholder="Success URL substring" placeholderTextColor={c.textMuted}
                autoCapitalize="none"
                style={{ color: c.textPrimary, backgroundColor: c.bg, borderColor: c.border, borderWidth: 1, borderRadius: 8, padding: 10 }} />
              <View style={{ flexDirection: "row", flexWrap: "wrap", gap: 8 }}>
                <Pressable disabled={busy || running} onPress={startProfileAuth} style={[btn(c.bgCard), { borderWidth: 1, borderColor: c.border, opacity: busy ? 0.6 : 1 }]}>
                  <Text style={{ color: c.textPrimary, fontWeight: "700" }}>Start Auth</Text>
                </Pressable>
                <Pressable disabled={busy || !authJob?.id} onPress={() => signalProfileAuth("finish")} style={[btn("#14532d"), { opacity: busy || !authJob?.id ? 0.6 : 1 }]}>
                  <Text style={{ color: "#fff", fontWeight: "700" }}>Finish</Text>
                </Pressable>
                <Pressable disabled={busy || !authJob?.id} onPress={() => signalProfileAuth("cancel")} style={[btn("#7f1d1d"), { opacity: busy || !authJob?.id ? 0.6 : 1 }]}>
                  <Text style={{ color: "#fff", fontWeight: "700" }}>Cancel</Text>
                </Pressable>
              </View>
              {authJob ? <Text style={{ color: c.textMuted, fontSize: 11 }}>{authJob.id} · {authJob.phase || authJob.state}</Text> : null}
            </View>
          </View>
        ) : null}

        <View style={{ backgroundColor: c.bgCard, borderColor: c.border, borderWidth: 1, borderRadius: 10, padding: 12, gap: 10 }}>
          <View style={{ flexDirection: "row", justifyContent: "space-between", alignItems: "center" }}>
            <Text style={{ color: c.textPrimary, fontWeight: "700", fontSize: 13 }}>Full Quality</Text>
            <Pressable onPress={() => setRunRedroid((v) => !v)} style={{ backgroundColor: runRedroid ? c.accent : c.bg, borderRadius: 8, paddingVertical: 7, paddingHorizontal: 10, borderWidth: 1, borderColor: c.border }}>
              <Text style={{ color: runRedroid ? "#fff" : c.textPrimary, fontWeight: "700", fontSize: 12 }}>{runRedroid ? "Redroid on" : "Redroid off"}</Text>
            </Pressable>
          </View>
          {runRedroid ? (
            <View style={{ gap: 8 }}>
              <TextInput value={qaPackage} onChangeText={setQAPackage} placeholder="Android package" placeholderTextColor={c.textMuted}
                autoCapitalize="none"
                style={{ color: c.textPrimary, backgroundColor: c.bg, borderColor: c.border, borderWidth: 1, borderRadius: 8, padding: 10 }} />
              <TextInput value={qaAPK} onChangeText={setQAAPK} placeholder="APK path optional" placeholderTextColor={c.textMuted}
                autoCapitalize="none"
                style={{ color: c.textPrimary, backgroundColor: c.bg, borderColor: c.border, borderWidth: 1, borderRadius: 8, padding: 10 }} />
              <TextInput value={qaBase} onChangeText={setQABase} placeholder="Warm base optional" placeholderTextColor={c.textMuted}
                autoCapitalize="none"
                style={{ color: c.textPrimary, backgroundColor: c.bg, borderColor: c.border, borderWidth: 1, borderRadius: 8, padding: 10 }} />
            </View>
          ) : null}
          <Pressable disabled={busy || running} onPress={runQuality} style={[btn("#047857"), { opacity: busy || running ? 0.6 : 1 }]}>
            <Text style={{ color: "#fff", fontWeight: "700" }}>{running ? "Running…" : "Run Full Quality"}</Text>
          </Pressable>
        </View>

        <View style={{ flexDirection: "row", gap: 10 }}>
          <Pressable disabled={busy || running} onPress={run} style={[btn(c.accent), { flex: 1, opacity: busy || running ? 0.6 : 1 }]}>
            <Text style={{ color: "#fff", fontWeight: "700" }}>
              {running ? "Running…" : mode === "playwright-native" ? "Run Native" : mode === "playwright-yaml" ? "Run Playwright" : "Run Web Tests"}
            </Text>
          </Pressable>
          <Pressable disabled={busy || running} onPress={doGrow} style={[btn(c.bgCard), { flex: 1, borderWidth: 1, borderColor: c.border, opacity: busy ? 0.6 : 1 }]}>
            <Text style={{ color: c.textPrimary, fontWeight: "700" }}>🌱 Grow Tests</Text>
          </Pressable>
        </View>

        {mode !== "chromedp" ? (
          <View style={{ flexDirection: "row", flexWrap: "wrap", gap: 8 }}>
            <Pressable disabled={busy || running} onPress={loadRuns} style={[btn(c.bgCard), { borderWidth: 1, borderColor: c.border, opacity: busy ? 0.6 : 1 }]}>
              <Text style={{ color: c.textPrimary, fontWeight: "700" }}>Runs</Text>
            </Pressable>
            <Pressable disabled={busy || running} onPress={gcRuns} style={[btn(c.bgCard), { borderWidth: 1, borderColor: c.border, opacity: busy ? 0.6 : 1 }]}>
              <Text style={{ color: c.textPrimary, fontWeight: "700" }}>GC Dry Run</Text>
            </Pressable>
          </View>
        ) : null}

        {deps && deps.ready === false && (
          <View style={{ backgroundColor: "#3a2a10", borderColor: "#a86a1a", borderWidth: 1, borderRadius: 10, padding: 12, gap: 8 }}>
            <Text style={{ color: "#ffd166", fontSize: 13 }}>
              Test tools missing: {(deps.deps || []).filter((d) => !d.present).map((d) => d.name).join(", ")}
            </Text>
            <Pressable disabled={depsBusy} onPress={installDeps} style={[btn("#a86a1a"), { opacity: depsBusy ? 0.6 : 1 }]}>
              <Text style={{ color: "#fff", fontWeight: "700" }}>{depsBusy ? "Installing…" : "🔧 Install test tools (once)"}</Text>
            </Pressable>
          </View>
        )}

        {err && <Text style={{ color: "#ff5d5d", fontSize: 13 }}>{err}</Text>}

        {(runs.length > 0 || gcResult) ? (
          <View style={card}>
            {gcResult ? <Text style={{ color: c.textMuted, fontSize: 11 }}>GC: {((gcResult as any).deleted || []).length} deleted candidates</Text> : null}
            {runs.slice(0, 8).map((r, i) => (
              <Text key={i} style={{ color: c.textMuted, fontSize: 11 }}>{r.kind || r.source || "run"} · {r.name || r.path}</Text>
            ))}
          </View>
        ) : null}

        {job && (
          <View style={card}>
            <Text style={{ color: c.textPrimary, fontWeight: "600" }}>
              {job.state === "completed" ? "✓ " : running ? "● " : ""}{job.phase || job.state}
              {running && <ActivityIndicator size="small" color={c.accent} />}
            </Text>
            <Text selectable style={{ color: c.textMuted, fontSize: 11, marginTop: 6 }}>
              {(job.log || []).slice(-12).join("\n")}
            </Text>
          </View>
        )}

        {qualityReport ? <QualityView report={qualityReport} c={c} /> : null}
        {report && <ReportView report={report} c={c} target={target()} jobId={qualityReport?.browserJobId || job?.id} playwright={mode !== "chromedp"} />}
        {grow && <GrowView plan={grow} c={c} />}
      </ScrollView>
    </View>
  );
}

function ReportView({ report, c, target, jobId, playwright }: { report: TKReport; c: any; target?: TKTarget; jobId?: string; playwright?: boolean }) {
  const features = report.features || [];
  const ok = (report.failed ?? 0) === 0;
  return (
    <View style={{ gap: 10 }}>
      <View style={{ flexDirection: "row", gap: 10 }}>
        <Stat label="Passed" value={report.passed ?? 0} color="#2fbf71" c={c} />
        <Stat label="Failed" value={report.failed ?? 0} color="#ff5d5d" c={c} />
        <Stat label="Features" value={report.total ?? features.length} color="#3b82f6" c={c} />
      </View>
      <Text style={{ color: ok ? "#2fbf71" : "#ff5d5d", fontWeight: "700" }}>
        {ok ? "PASS — all Features green" : `${report.failed} Feature(s) failing`}
      </Text>
      {report.reelPath ? (
        <Text style={{ color: c.textMuted, fontSize: 12 }}>🎬 Highlight reel: {report.reelPath}</Text>
      ) : null}
      {report.artifacts?.length ? <ArtifactRefs artifacts={report.artifacts} c={c} target={target} jobId={jobId} /> : null}
      {features.map((f, i) => (
        <View key={i} style={{ backgroundColor: c.bgCard, borderRadius: 8, padding: 10, borderLeftWidth: 3, borderLeftColor: f.status === "pass" ? "#2fbf71" : "#ff5d5d" }}>
          <View style={{ flexDirection: "row", justifyContent: "space-between" }}>
            <Text style={{ color: c.textPrimary, fontWeight: "600", flex: 1 }}>{f.name}</Text>
            <Text style={{ color: f.status === "pass" ? "#2fbf71" : "#ff5d5d", fontWeight: "700", fontSize: 12 }}>
              {f.status === "pass" ? "PASS" : "FAIL"}
            </Text>
          </View>
          <Text style={{ color: c.textMuted, fontSize: 11, marginTop: 2 }}>
            {f.target}{f.url ? " · " + f.url : ""} · {Math.round((f.durationMs ?? 0) / 100) / 10}s · {f.steps ?? 0} steps
            {f.screenshots?.length ? ` · ${f.screenshots.length} shots` : ""}
          </Text>
          {f.error ? <Text style={{ color: "#ff9f43", fontSize: 11, marginTop: 4 }}>step {f.failStep}: {f.error}</Text> : null}
          {f.tracePath ? <Text style={{ color: c.textMuted, fontSize: 11, marginTop: 4 }}>trace: {f.tracePath}</Text> : null}
          <FeatureMedia feature={f} c={c} target={target} jobId={jobId} playwright={playwright} />
        </View>
      ))}
    </View>
  );
}

// FeatureMedia shows the short success/fail evidence for one Feature: an
// auto-loaded screenshot thumbnail and a tap-to-play highlight clip (mp4),
// both fetched from the runner via project_test_artifact.
function FeatureMedia({ feature, c, target, jobId, playwright }: { feature: TKFeature; c: any; target?: TKTarget; jobId?: string; playwright?: boolean }) {
  const [shot, setShot] = useState<string | null>(null);
  const [clipUri, setClipUri] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);

  useEffect(() => {
    let alive = true;
    // Prefer the tiny poster (a few KB) over a full screenshot so the result is
    // visible instantly even on a weak link; fall back to the last screenshot.
    const thumb = feature.posterPath || (feature.screenshots && feature.screenshots[feature.screenshots.length - 1]);
    if (thumb && target && jobId) {
      const fetcher = playwright ? testkitClient.playwrightArtifact : testkitClient.artifact;
      fetcher(target, jobId, thumb).then((a) => {
        if (alive && a?.base64) setShot(`data:${a.mimeType || "image/jpeg"};base64,${a.base64}`);
      });
    }
    return () => { alive = false; };
  }, [jobId, feature?.name]);

  const playClip = async () => {
    if (!feature.clipPath || !target || !jobId) return;
    setLoading(true);
    try {
      const a = await (playwright ? testkitClient.playwrightArtifact(target, jobId, feature.clipPath) : testkitClient.artifact(target, jobId, feature.clipPath));
      if (a?.base64) {
        const path = (FileSystem.cacheDirectory || "") + (a.name || "clip.mp4");
        await FileSystem.writeAsStringAsync(path, a.base64, { encoding: FileSystem.EncodingType.Base64 });
        setClipUri(path);
      }
    } catch { /* fall back to screenshot */ }
    setLoading(false);
  };

  return (
    <View style={{ marginTop: 6 }}>
      {clipUri ? (
        <Video source={{ uri: clipUri }} style={{ width: "100%", height: 200, borderRadius: 8, backgroundColor: "#000" }} useNativeControls resizeMode={ResizeMode.CONTAIN} shouldPlay isLooping />
      ) : shot ? (
        <Image source={{ uri: shot }} style={{ width: "100%", height: 170, borderRadius: 8 }} resizeMode="cover" />
      ) : null}
      {feature.clipPath && !clipUri ? (
        <Pressable onPress={playClip} disabled={loading}
          style={{ marginTop: 6, alignSelf: "flex-start", backgroundColor: c.bgCard, borderWidth: 1, borderColor: c.border, borderRadius: 8, paddingVertical: 6, paddingHorizontal: 12 }}>
          <Text style={{ color: c.textPrimary, fontSize: 12 }}>{loading ? "Loading…" : "▶ Play highlight"}</Text>
        </Pressable>
      ) : null}
    </View>
  );
}

function ArtifactRefs({ artifacts, c, target, jobId }: { artifacts: TKArtifactRef[]; c: any; target?: TKTarget; jobId?: string }) {
  const [traceInfo, setTraceInfo] = useState<TKTraceInspect | null>(null);
  const inspectTrace = async (a: TKArtifactRef) => {
    if (!target || !jobId) return;
    const info = await testkitClient.playwrightTraceInspect(target, jobId, a.path);
    if (!info.error) setTraceInfo(info);
  };
  return (
    <View style={{ backgroundColor: c.bgCard, borderRadius: 8, padding: 10, borderWidth: 1, borderColor: c.border, gap: 4 }}>
      <Text style={{ color: c.textPrimary, fontWeight: "700", fontSize: 12 }}>Artifacts</Text>
      {artifacts.slice(0, 20).map((a, i) => (
        <Pressable key={`${a.path}-${i}`} onPress={() => a.kind === "trace" ? inspectTrace(a) : undefined}>
          <Text style={{ color: a.kind === "trace" ? c.accent : c.textMuted, fontSize: 11 }}>{a.kind}: {a.name || a.path}</Text>
        </Pressable>
      ))}
      {traceInfo ? (
        <View style={{ marginTop: 6, gap: 2 }}>
          <Text style={{ color: c.textPrimary, fontSize: 11, fontWeight: "700" }}>
            {traceInfo.entryCount ?? 0} entries · {traceInfo.resources ?? 0} resources · {traceInfo.screenshots ?? 0} screenshots
          </Text>
          {(traceInfo.entries || []).slice(0, 8).map((e, i) => (
            <Text key={i} style={{ color: c.textMuted, fontSize: 10 }}>{e.name}</Text>
          ))}
        </View>
      ) : null}
    </View>
  );
}

function QualityView({ report, c }: { report: TKQualityReport; c: any }) {
  return (
    <View style={{ backgroundColor: c.bgCard, borderRadius: 10, padding: 12, gap: 6, borderWidth: 1, borderColor: c.border }}>
      <Text style={{ color: report.passed ? "#2fbf71" : "#ff5d5d", fontWeight: "800" }}>
        {report.passed ? "FULL QUALITY PASS" : "FULL QUALITY FOUND FAILURES"}
      </Text>
      {(report.summary || []).map((s, i) => (
        <Text key={i} style={{ color: c.textMuted, fontSize: 12 }}>{s}</Text>
      ))}
      {report.preflight ? (
        <Text style={{ color: (report.preflight as any).ready ? "#2fbf71" : "#ffd166", fontSize: 12 }}>
          Preflight: {(report.preflight as any).ready ? "ready" : "needs attention"}
        </Text>
      ) : null}
      {report.android ? (
        <Text style={{ color: c.textPrimary, fontSize: 12 }}>
          Redroid: {report.android.caught ?? 0} caught · {report.android.fixed ?? 0} fixed · {(report.android.flows || []).length} flows
        </Text>
      ) : null}
      {report.browserJobId ? <Text style={{ color: c.textMuted, fontSize: 11 }}>web job: {report.browserJobId}</Text> : null}
      {report.qaJobId ? <Text style={{ color: c.textMuted, fontSize: 11 }}>redroid job: {report.qaJobId}</Text> : null}
    </View>
  );
}

function ModePill({ label, active, onPress, c }: { label: string; active: boolean; onPress: () => void; c: any }) {
  return (
    <Pressable onPress={onPress}
      style={{ backgroundColor: active ? c.accent : c.bgCard, borderRadius: 8, paddingVertical: 8, paddingHorizontal: 12, borderWidth: 1, borderColor: c.border }}>
      <Text style={{ color: active ? "#fff" : c.textPrimary, fontSize: 13, fontWeight: "700" }}>{label}</Text>
    </Pressable>
  );
}

function GrowView({ plan, c }: { plan: TKGrowPlan; c: any }) {
  const un = plan.uncovered || [];
  return (
    <View style={{ backgroundColor: c.bgCard, borderRadius: 10, padding: 12, gap: 6, borderWidth: 1, borderColor: c.border }}>
      <Text style={{ color: c.textPrimary, fontWeight: "700" }}>🌱 Self-grow plan</Text>
      <Text style={{ color: c.textMuted, fontSize: 12 }}>
        {plan.coveredCount ?? 0} covered · {un.length} uncovered route(s){plan.applied ? " · ledger updated" : ""}
      </Text>
      {plan.taskId ? <Text style={{ color: "#2fbf71", fontSize: 12 }}>🤖 runner authoring specs (task {plan.taskId})</Text> : null}
      {un.slice(0, 30).map((u, i) => (
        <Text key={i} style={{ color: c.textPrimary, fontSize: 12 }}>• {u.suggestedName}  <Text style={{ color: c.textMuted }}>({u.route})</Text></Text>
      ))}
      <Text style={{ color: c.textMuted, fontSize: 11, marginTop: 6 }}>
        The Yaver runner authors these as new specs (no user YAML). Trigger it during vibe-coding or run "Grow" again after changes.
      </Text>
    </View>
  );
}

function Stat({ label, value, color, c }: { label: string; value: number; color: string; c: any }) {
  return (
    <View style={{ flex: 1, backgroundColor: c.bgCard, borderRadius: 8, padding: 10, alignItems: "center", borderWidth: 1, borderColor: c.border }}>
      <Text style={{ color, fontWeight: "800", fontSize: 20 }}>{value}</Text>
      <Text style={{ color: c.textMuted, fontSize: 11 }}>{label}</Text>
    </View>
  );
}
