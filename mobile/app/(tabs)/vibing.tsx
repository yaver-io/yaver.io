import { urlHost } from "../../src/lib/urlHost";
import React, { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { ActivityIndicator, Image, Platform, Pressable, ScrollView, StyleSheet, Text, TextInput, View } from "react-native";
import { Redirect, router } from "expo-router";
import { SafeAreaView } from "react-native-safe-area-context";
import AsyncStorage from "@react-native-async-storage/async-storage";
import { useAuth } from "../../src/context/AuthContext";
import { Device, useDevice } from "../../src/context/DeviceContext";
import { useColors } from "../../src/context/ThemeContext";
import { useCloudStudio } from "../../src/context/CloudStudioContext";
import { quicClient } from "../../src/lib/quic";
import { getUserSettings } from "../../src/lib/auth";
import { takePendingVibingProject } from "../../src/lib/vibingStore";
import { listLocalWorkspaces, validateLocalWorkspace, type LocalWorkspace, type StaticValidationReport } from "../../src/lib/coding-runtime";
import { remoteRenderRequiredFailure } from "../../src/lib/renderCapability";
import { useResponsiveLayout } from "../../src/hooks/useResponsiveLayout";
import { connectionManager } from "../../src/lib/connectionManager";
import { resolveRemotelessPlacement, type ExecutionCandidate } from "../../src/_core/remoteless";

type Project = { name: string; path: string; framework?: string };
type DevStatus = {
  framework?: string;
  kind?: string;
  running: boolean;
  serving: boolean;
  servingLabel?: string;
  port?: number;
  vibeSessionId?: string;
  previewHealth?: { state?: string; reason?: string };
};

type BoxCapabilities = {
  browser?: boolean;
  android?: boolean;
  iosSimulator?: boolean;
  tvosSimulator?: boolean;
  xr?: boolean;
};
type PreviewOption = { id: "web" | "android" | "ios" | "tvos" | "xr"; label: string; detail: string };
type StreamLog = { id: number; at: string; level: "info" | "success" | "warn" | "error"; message: string };

const isTV = (Platform as typeof Platform & { isTV?: boolean }).isTV === true;
const isAppleSurface = Platform.OS === "ios";

function previewOptionsFor(framework: string | undefined, caps: BoxCapabilities): PreviewOption[] {
  const f = (framework || "").toLowerCase();
  const options: PreviewOption[] = [];
  const web = /next|react|vue|svelte|angular|web|expo|flutter/.test(f);
  // A runtime being present is not enough: an option is production-ready only
  // when the agent can launch it *and* return frames. Today that full path is
  // implemented for browser previews; native target launch/capture adapters
  // will add their options here together with their agent handlers.
  if (web && caps.browser) options.push({ id: "web", label: "Web UI", detail: "Chrome on the runner" });
  return options;
}

function deviceBaseUrl(device: Device, token: string | null): string | null {
  const relays = quicClient.getRelayServers();
  if (relays.length > 0) return `${relays[0].httpUrl}/d/${device.id}`;
  return `http://${urlHost(device.host)}:${device.port}`;
}

export default function VibingScreen() {
  const c = useColors();
  const layout = useResponsiveLayout();
  const { token } = useAuth();
  const { activeDevice, disconnect, devices, connectedDeviceIds, primaryDeviceId, secondaryDeviceId, machineRoles, codingMode } = useDevice();
  const { activeProjectSession } = useCloudStudio();
  const legacyTvRunner = isTV
    && activeDevice?.name.trim().toLowerCase().replace(/\.local$/, "") === "ubuntu-4gb-hel1-1"
    && !activeDevice.cloudWorkspaceId;
  const [localWorkspace, setLocalWorkspace] = useState<LocalWorkspace | null>(null);
  const [validation, setValidation] = useState<StaticValidationReport | null>(null);
  const [validating, setValidating] = useState(false);
  const [projects, setProjects] = useState<Project[]>([]);
  const [selected, setSelected] = useState<string>("");
  const [projectSelected, setProjectSelected] = useState(false);
  // No capability is selectable until the connected runner reports it. The
  // old optimistic browser=true made an unselected box look renderable.
  const [capabilities, setCapabilities] = useState<BoxCapabilities>({});
  const [previewTarget, setPreviewTarget] = useState<PreviewOption["id"]>("web");
  const [status, setStatus] = useState<DevStatus | null>(null);
  const [working, setWorking] = useState(false);
  const [laneHtml, setLaneHtml] = useState<string>("");
  const [streamLogs, setStreamLogs] = useState<StreamLog[]>([]);
  const [frameUri, setFrameUri] = useState<string>("");
  const [frameError, setFrameError] = useState<string>("");
  const [framePollingEnabled, setFramePollingEnabled] = useState(true);
  const [frameOverride, setFrameOverride] = useState<string>("");
  const [transport, setTransport] = useState<"auto" | "sse" | "webrtc">("auto");
  const [relayTier, setRelayTier] = useState<"free" | "pro">("free");
  const pollRef = useRef<ReturnType<typeof setInterval> | null>(null);
  const selectedProject = useMemo(() => projects.find((p) => p.path === selected), [projects, selected]);

  const renderCandidates = useMemo(() => {
    const rows: ExecutionCandidate[] = [];
    const seen = new Set<string>();
    const add = (id: string | null | undefined, role: ExecutionCandidate["role"]) => {
      if (!id || seen.has(id)) return;
      const device = devices.find((row) => row.id === id);
      if (!device) return;
      seen.add(id);
      rows.push({ id, name: device.name || id.slice(0, 8), role, connected: connectedDeviceIds.includes(id) });
    };
    add(machineRoles?.renderDeviceId || machineRoles?.runnerDeviceId, "primary");
    add(machineRoles?.secondaryRenderDeviceId || machineRoles?.secondaryRunnerDeviceId, "secondary");
    add(primaryDeviceId, "primary");
    add(secondaryDeviceId, "secondary");
    add(activeDevice?.id, "focused");
    return rows;
  }, [activeDevice?.id, connectedDeviceIds, devices, machineRoles, primaryDeviceId, secondaryDeviceId]);
  const renderPlacement = useMemo(
    () => resolveRemotelessPlacement({
      capability: selectedProject?.framework?.toLowerCase() === "flutter" ? "flutter-render" : "dev-server",
      surface: Platform.OS === "ios" ? "ios" : Platform.OS === "android" ? "android" : "web",
      candidates: renderCandidates,
      forceLocal: codingMode === "local-only",
    }),
    [codingMode, renderCandidates, selectedProject?.framework],
  );
  const renderDevice = renderPlacement.lane === "remote"
    ? devices.find((device) => device.id === renderPlacement.target.id) || activeDevice
    : activeDevice;
  const runtimeClient = renderPlacement.lane === "remote"
    ? connectionManager.clientFor(renderPlacement.target.id)
    : quicClient;
  const base = renderDevice && token && renderPlacement.lane === "remote" ? deviceBaseUrl(renderDevice, token) : null;
  const isLocalVibing = !isTV && (codingMode === "local-only" || (codingMode === "auto-fallback" && renderPlacement.lane !== "remote"));
  const localDeviceName = (Platform as any).isTV ? "this Apple TV" : "this device";
  const appendStreamLog = useCallback((level: StreamLog["level"], message: string) => {
    setStreamLogs((current) => [...current, { id: Date.now() + Math.random(), at: new Date().toLocaleTimeString(), level, message }].slice(-16));
  }, []);
  const previewOptions = useMemo(
    () => previewOptionsFor(selectedProject?.framework, capabilities),
    [selectedProject?.framework, capabilities],
  );
  const selectedPreview = previewOptions.find((option) => option.id === previewTarget) || previewOptions[0];
  const renderFailure = remoteRenderRequiredFailure(
    isTV ? "This TV" : Platform.OS === "ios" ? "This iPhone/iPad" : "This Android device",
    selectedProject?.framework?.toLowerCase() === "flutter" ? "flutter-render" : "dev-server",
    isTV ? "companion" : Platform.OS === "ios" ? "ios" : Platform.OS === "android" ? "android" : "web",
  );

  useEffect(() => {
    if (!base || !token) return;
    runtimeClient.requestAgent("/vibing/capabilities")
      .then((r) => r.ok ? r.json() : null)
      .then((data) => { if (data?.capabilities) setCapabilities(data.capabilities); })
      .catch(() => {});
  }, [base, token, runtimeClient]);

  useEffect(() => {
    if (previewOptions.length && !previewOptions.some((option) => option.id === previewTarget)) {
      setPreviewTarget(previewOptions[0].id);
    }
  }, [previewOptions, previewTarget]);

  // Load transport preference
  useEffect(() => {
    getUserSettings(token ?? "").then((s) => {
      if (s.vibingTransport) setTransport(s.vibingTransport);
      if (s.relayTier === "pro" || s.relayTier === "free") setRelayTier(s.relayTier);
    }).catch(() => {});
  }, [token]);

  useEffect(() => {
    if (isTV) {
      setLocalWorkspace(null);
    } else {
      listLocalWorkspaces().then((workspaces) => setLocalWorkspace(workspaces[0] ?? null)).catch(() => {});
    }
  }, []);

  const runStaticValidation = async () => {
    if (!localWorkspace) return;
    setValidating(true);
    try { setValidation(await validateLocalWorkspace(localWorkspace)); }
    finally { setValidating(false); }
  };

  // Dev/test: optional local frame-source override (e.g. http://localhost:8787)
  useEffect(() => {
    AsyncStorage.getItem("@yaver/vibing_frame_url").then((v) => {
      if (v) setFrameOverride(v);
    }).catch(() => {});
  }, []);

  // Load projects on connect
  useEffect(() => {
    if (isTV && !legacyTvRunner) {
      if (activeProjectSession) {
        const project = { name: activeProjectSession.repositoryName, path: activeProjectSession.reviewBranch, framework: "web" };
        setProjects([project]);
        setSelected(project.path);
        setProjectSelected(true);
      } else {
        setProjects([]);
        setSelected("");
        setProjectSelected(false);
      }
      return;
    }
    if (!base || !token) return;
    runtimeClient.listProjects(true)
      .then((list) => {
        setProjects(list);
        const pending = takePendingVibingProject();
        if (list.length > 0) {
          const hasPendingProject = !!pending && list.some((p) => p.path === pending);
          setSelected(hasPendingProject ? pending! : list[0].path);
          // Project → Vibing deep links should preserve their direct workflow;
          // entering from the tvOS home always begins at project selection.
          setProjectSelected(hasPendingProject);
        }
      })
      .catch(() => {});
  }, [base, token, activeProjectSession?.projectSessionId, legacyTvRunner, runtimeClient]);

  const refreshStatus = useCallback(async () => {
    if (!base || !token) return;
    try {
      if (isTV && !legacyTvRunner) {
        if (!activeProjectSession) return;
        const next = await runtimeClient.getProjectSessionPreviewStatus(activeProjectSession.projectSessionId);
        setStatus((previous) => {
          if (next.serving && !previous?.serving) appendStreamLog("success", `Serving ${next.framework || "preview"} on port ${next.port || "?"}`);
          if (!next.serving && previous?.serving) appendStreamLog("warn", "Preview stopped on the Cloud Runner");
          return next;
        });
        return;
      }
      const r = await runtimeClient.requestAgent("/dev/status");
      if (r.ok) {
        const next = await r.json() as DevStatus;
        setStatus((previous) => {
          if (next.serving && !previous?.serving) appendStreamLog("success", `Serving ${next.framework || "preview"} on port ${next.port || "?"}`);
          if (!next.serving && previous?.serving) appendStreamLog("warn", "Preview stopped on the runner");
          return next;
        });
      }
    } catch {}
  }, [base, token, appendStreamLog, activeProjectSession?.projectSessionId, legacyTvRunner, runtimeClient]);

  useEffect(() => {
    refreshStatus();
    pollRef.current = setInterval(refreshStatus, 4000);
    return () => {
      if (pollRef.current) clearInterval(pollRef.current);
    };
  }, [refreshStatus]);

  const startPreview = async (path?: string) => {
    const workDir = path || selected;
    if (!base || !token || !workDir) return;
    setWorking(true);
    setLaneHtml("");
    setFramePollingEnabled(true);
    setStreamLogs([]);
    appendStreamLog("info", `Launching ${selectedProject?.name || workDir} as ${selectedPreview?.label || "preview"}…`);
    try {
      if (isTV && !legacyTvRunner) {
        if (!activeProjectSession) throw new Error("Select a Project Session first");
        const started = await runtimeClient.startProjectSessionPreview(activeProjectSession.projectSessionId, previewTarget);
        setStatus(started);
        appendStreamLog("info", `Cloud Runner accepted launch · ${started.framework || "detecting framework"}${started.port ? ` · port ${started.port}` : ""}`);
        await refreshStatus();
        setWorking(false);
        return;
      }
      const r = await runtimeClient.requestAgent("/dev/start", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ workDir, previewTarget }),
      });
      if (r.ok) {
        const started = await r.json() as DevStatus;
        setStatus(started);
        appendStreamLog("info", `Runner accepted launch · ${started.framework || "detecting framework"}${started.port ? ` · port ${started.port}` : ""}`);
      } else {
        appendStreamLog("error", `Launch rejected (${r.status})`);
      }
      await refreshStatus();
    } catch {
      appendStreamLog("error", "Could not reach the runner to launch preview");
    }
    setWorking(false);
  };

  const stopPreview = async () => {
    if (!base || !token) return;
    setWorking(true);
    try {
      if (isTV && !legacyTvRunner) {
        if (activeProjectSession) await runtimeClient.stopProjectSessionPreview(activeProjectSession.projectSessionId);
      } else {
        await runtimeClient.requestAgent("/dev/stop", { method: "POST" });
      }
      setStatus(null);
      setLaneHtml("");
      await refreshStatus();
    } catch {}
    setWorking(false);
  };

  const verifyLane = async () => {
    // Headless lane proof: fetch the running app HTML with auth.
    if (!base || !token || !status?.serving) return;
    try {
      if (isTV && !legacyTvRunner) {
        if (!activeProjectSession) return;
        const html = await runtimeClient.fetchProjectSessionPreview(activeProjectSession.projectSessionId);
        setLaneHtml(html.slice(0, 400));
        appendStreamLog("success", `Live browser lane verified · ${html.length} bytes received`);
        return;
      }
      const r = await runtimeClient.requestAgent("/dev/stream");
      if (r.ok) {
        const html = await r.text();
        setLaneHtml(html.slice(0, 400));
        appendStreamLog("success", `Live browser lane verified · ${html.length} bytes received`);
      }
    } catch {
      appendStreamLog("error", "Live lane verification failed");
    }
  };

  // Live frame lane: poll the agent's /vibing/frame (headless Chrome capture of
  // the local dev server) and render as an image. 404 → endpoint not on this box.
  // A dev override (frameOverride) points at a local frame server instead.
  const fetchFrame = useCallback(async () => {
    if (!base || !token || !status?.serving || !status?.port) return;
    try {
      const override = frameOverride.replace(/\/$/, "");
      const r = override
        ? await fetch(`${override}/frame?url=${encodeURIComponent(`${override}/sample`)}`, { headers: { Authorization: `Bearer ${token}` } })
        : await runtimeClient.requestAgent(`/vibing/frame?url=${encodeURIComponent(`http://localhost:${status.port}/`)}`);
      if (r.status === 404) {
        setFrameError(override ? "Local frame server not found" : "Frame endpoint not available on this box");
        setFramePollingEnabled(false);
        appendStreamLog("warn", override ? "Local frame server was not found" : "Box has no /vibing/frame endpoint");
        return;
      }
      if (!r.ok) {
        setFramePollingEnabled(false);
        setFrameError(`Frame capture stopped after box error ${r.status}`);
        appendStreamLog("warn", `Frame capture disabled after box error ${r.status}`);
        return;
      }
      const buf = await r.arrayBuffer();
      const bytes = new Uint8Array(buf);
      let b64 = "";
      for (let i = 0; i < bytes.length; i += 0x8000) {
        b64 += String.fromCharCode(...Array.from(bytes.subarray(i, i + 0x8000)));
      }
      setFrameUri(`data:image/png;base64,${btoa(b64)}`);
      setFrameError("");
      appendStreamLog("success", `Frame received · ${(buf.byteLength / 1024).toFixed(0)} KB`);
    } catch {
      setFramePollingEnabled(false);
      setFrameError("Frame capture stopped after a connection error");
      appendStreamLog("warn", "Frame capture disabled after a connection error");
    }
  }, [base, token, status, frameOverride, appendStreamLog, runtimeClient]);

  useEffect(() => {
    if (!status?.serving) {
      setFrameUri("");
      return;
    }
    if (!framePollingEnabled) return;
    setFrameUri("");
    setFrameError("");
    fetchFrame();
    const iv = setInterval(fetchFrame, 2500);
    return () => clearInterval(iv);
  }, [status?.serving, fetchFrame, framePollingEnabled]);

  const serving = !!status?.serving;
  const building = !serving && status?.running === false && !!status?.port;

  // Keep legacy phone/tvOS behavior reachable, but tablets have one canonical
  // workspace. This also makes old /vibing deep links converge instead of
  // opening a fourth tablet layout.
  if (layout.isTablet && !isTV) {
    return <Redirect href="/vibe-studio" />;
  }

  if (!isLocalVibing && renderPlacement.lane !== "remote") {
    return (
      <SafeAreaView style={[styles.safe, { backgroundColor: c.bg }]} edges={["bottom"]}>
        <ScrollView contentContainerStyle={styles.container}>
          <View style={styles.headerRow}>
            <View style={styles.titleBlock}>
              <Text style={[styles.title, { color: c.textPrimary }]}>Vibing</Text>
              <Text style={[styles.subtitle, { color: c.textSecondary }]}>{renderFailure.title}</Text>
            </View>
            <View style={[styles.badge, { backgroundColor: c.warn + "22" }]}>
              <Text style={[styles.badgeText, { color: c.warn }]}>Unavailable</Text>
            </View>
          </View>
          <View style={[styles.card, { backgroundColor: c.bgCard, borderColor: c.border }]}>
            <Text style={[styles.cardLabel, { color: c.textPrimary }]}>{renderFailure.code}</Text>
            <Text style={[styles.hint, { color: c.textSecondary, marginTop: 8 }]}>{renderFailure.message}</Text>
            <Pressable
              hasTVPreferredFocus
              onPress={() => router.push(renderFailure.action.route)}
              style={({ focused }) => [styles.btnPrimary, { backgroundColor: c.accent, marginTop: 16 }, focused && styles.focused]}
            >
              <Text style={styles.btnPrimaryText}>{renderFailure.action.label}</Text>
            </Pressable>
            {renderFailure.alternativeAction ? (
              <Pressable
                onPress={() => router.push(renderFailure.alternativeAction!.route)}
                style={({ focused }) => [styles.changeProject, { borderColor: c.border, backgroundColor: c.bgCard, marginTop: 10 }, focused && styles.focused]}
              >
                <Text style={{ color: c.textSecondary, fontSize: 15 }}>{renderFailure.alternativeAction.label}</Text>
              </Pressable>
            ) : null}
          </View>
          {!isTV && (
            <Pressable
              onPress={() => router.push("/tasks")}
              style={({ focused }) => [styles.changeProject, { borderColor: c.border, backgroundColor: c.bgCard }, focused && styles.focused]}
            >
              <Text style={{ color: c.textMuted, fontSize: 15 }}>Use boxless local coding and audit</Text>
            </Pressable>
          )}
        </ScrollView>
      </SafeAreaView>
    );
  }

  if (isLocalVibing) {
    return (
      <SafeAreaView style={[styles.safe, { backgroundColor: c.bg }]} edges={["bottom"]}>
        <ScrollView contentContainerStyle={styles.container}>
          <View style={styles.headerRow}>
            <View style={styles.titleBlock}>
              <Text style={[styles.title, { color: c.textPrimary }]}>Vibing</Text>
              <Text style={[styles.subtitle, { color: c.textSecondary }]}>{renderPlacement.banner}</Text>
            </View>
            <View style={[styles.badge, { backgroundColor: c.warn + "22" }]}><Text style={[styles.badgeText, { color: c.warn }]}>{codingMode === "local-only" ? "Phone only" : "Fallback"}</Text></View>
          </View>

          <View style={[styles.card, { backgroundColor: c.bgCard, borderColor: c.border }]}>
            <Text style={[styles.cardLabel, { color: c.textPrimary }]}>Device-local coding</Text>
            <Text style={[styles.hint, { color: c.textSecondary }]}>Chat, file edits, Git status/diff, explicit commits, and review-branch pushes run on {localDeviceName}. Your model API key and Git token stay in this device's secure storage.</Text>
            <Text style={[styles.hint, { color: c.warn, marginTop: 12 }]}>{renderFailure.code} · {renderFailure.message}</Text>
            <Pressable onPress={() => router.push(renderFailure.action.route)} style={({ focused }) => [styles.btnGhost, { borderColor: c.border, marginTop: 14 }, focused && styles.focused]}>
              <Text style={[styles.btnGhostText, { color: c.accent }]}>{renderFailure.action.label}</Text>
            </Pressable>
          </View>

          <View style={[styles.card, { backgroundColor: c.bgCard, borderColor: c.border }]}>
            <Text style={[styles.cardLabel, { color: c.textPrimary }]}>Local static preflight</Text>
            <Text style={[styles.hint, { color: c.textSecondary }]}>Scans {localWorkspace ? localWorkspace.name : "your local workspace"} for merge-conflict markers, malformed JSON configuration, and changed files. This is validation only—not compilation or tests.</Text>
            <Pressable disabled={!localWorkspace || validating} onPress={runStaticValidation} style={({ focused }) => [styles.btnGhost, { borderColor: c.border, marginTop: 14 }, focused && styles.focused, (!localWorkspace || validating) && { opacity: 0.5 }]}><Text style={[styles.btnGhostText, { color: c.accent }]}>{validating ? "Checking…" : "Run static preflight"}</Text></Pressable>
            {validation && <View style={styles.validationResult}>
              <Text style={[styles.hint, { color: validation.issues.some((issue) => issue.severity === "error") ? c.error : c.success }]}>{validation.issues.some((issue) => issue.severity === "error") ? "Static preflight found issues" : "Static preflight passed"} · {validation.checkedFiles} files · {validation.changedFiles.length} changed</Text>
              {validation.issues.slice(0, 5).map((issue, index) => <Text key={`${issue.path}-${index}`} style={[styles.hint, { color: issue.severity === "error" ? c.error : c.warn }]}>{issue.path}: {issue.message}</Text>)}
              <Text style={[styles.hint, { color: c.textMuted }]}>Compile: not run · Tests: not run</Text>
            </View>}
          </View>

          <View style={styles.controls}>
            <Pressable hasTVPreferredFocus onPress={() => router.push("/tasks")} style={({ focused }) => [styles.btnPrimary, { backgroundColor: c.accent }, focused && styles.focused]}><Text style={styles.btnPrimaryText}>Open local coding chat</Text></Pressable>
            {activeDevice && <Pressable onPress={disconnect} style={({ focused }) => [styles.btnGhost, { borderColor: c.border }, focused && styles.focused]}><Text style={[styles.btnGhostText, { color: c.error }]}>Disconnect machine</Text></Pressable>}
          </View>

          <Pressable onPress={() => router.push("/devices")} style={({ focused }) => [styles.changeProject, { borderColor: c.border, backgroundColor: c.bgCard }, focused && styles.focused]}>
            <Text style={{ color: c.textMuted, fontSize: 15 }}>{activeDevice ? "Select another machine" : "Connect or select a machine"}</Text>
          </Pressable>
        </ScrollView>
      </SafeAreaView>
    );
  }

  return (
    <SafeAreaView style={[styles.safe, { backgroundColor: c.bg }]} edges={["bottom"]}>
      <ScrollView contentContainerStyle={[styles.container, serving && styles.streamingContainer]}>
        <View style={styles.headerRow}>
          <View style={styles.titleBlock}>
            <Text style={[styles.title, { color: c.textPrimary }]}>Vibing</Text>
            <Text style={[styles.subtitle, { color: c.textSecondary }]}>
              {renderDevice ? `Live preview · ${renderDevice.name}` : isTV ? "Connect to the assigned Cloud Runner" : "Connect or select a machine first"}
            </Text>
          </View>
          <View style={[styles.badge, { backgroundColor: serving ? c.success + "22" : c.textMuted + "22" }]}>
            <Text style={[styles.badgeText, { color: serving ? c.success : c.textMuted }]}>
              {serving ? "Serving" : "Idle"}
            </Text>
          </View>
        </View>
        {renderPlacement.lane === "remote" && renderPlacement.banner ? (
          <View style={[styles.card, { backgroundColor: c.warn + "12", borderColor: c.warn + "55" }]}>
            <Text style={[styles.hint, { color: c.warn }]}>{renderPlacement.banner}</Text>
          </View>
        ) : null}
        {activeDevice && (
          <View style={styles.remoteActions}>
            <Pressable onPress={() => router.push("/devices")} style={({ focused }) => [styles.headerAction, { borderColor: c.border }, focused && styles.focused]}><Text style={{ color: c.textSecondary }}>Switch</Text></Pressable>
            <Pressable onPress={disconnect} style={({ focused }) => [styles.headerAction, { borderColor: c.border }, focused && styles.focused]}><Text style={{ color: c.error }}>Disconnect</Text></Pressable>
          </View>
        )}

        {!projectSelected ? (
          <View style={styles.projectSelection}>
            <Text style={[styles.sectionLabel, { color: c.textPrimary }]}>Choose a project to preview</Text>
            <Text style={[styles.hint, { color: c.textMuted, marginBottom: 12 }]}>Select a discovered project, then choose how to run its preview.</Text>
            {projects.length === 0 ? (
              isTV ? (
                <Pressable hasTVPreferredFocus onPress={() => router.push("/projects")} style={({ focused }) => [styles.btnPrimary, { backgroundColor: c.accent }, focused && styles.focused]}><Text style={styles.btnPrimaryText}>Open Projects</Text></Pressable>
              ) : <ActivityIndicator color={c.accent} style={{ marginVertical: 24 }} />
            ) : (
              projects.map((p) => (
                <Pressable
                  key={p.path}
                  hasTVPreferredFocus={p.path === selected}
                  onPress={() => {
                    setSelected(p.path);
                    setProjectSelected(true);
                  }}
                  style={({ focused }) => [styles.projectCard, { backgroundColor: c.bgCard, borderColor: focused ? c.accent : c.border }, focused && styles.focused]}
                >
                  <Text style={styles.projectIcon}>📁</Text>
                  <View style={{ flex: 1 }}>
                    <Text style={[styles.projectName, { color: c.textPrimary }]} numberOfLines={1}>{p.name}</Text>
                    <Text style={[styles.projectPath, { color: c.textSecondary }]} numberOfLines={1}>{p.path}</Text>
                  </View>
                  {p.framework ? <Text style={[styles.projectFramework, { color: c.accent }]}>{p.framework}</Text> : null}
                </Pressable>
              ))
            )}
          </View>
        ) : (
          <>
        {/* Transport */}
        <View style={[styles.card, { backgroundColor: c.bgCard, borderColor: c.border }]}>
          <View style={styles.cardRow}>
            <Text style={[styles.cardLabel, { color: c.textPrimary }]}>Transport</Text>
            <Text style={[styles.cardValue, { color: c.accent }]}>
              {transport === "webrtc" ? "WebRTC" : transport === "sse" ? "Frames" : "Auto"}
            </Text>
          </View>
          {isAppleSurface ? (
            <Text style={[styles.hint, { color: c.textMuted }]}>The connected runner selects an authenticated preview transport supported by this Apple TV.</Text>
          ) : (
            <Text style={[styles.hint, { color: c.textMuted }]}>{relayTier === "pro" ? "Low-latency transport is available for this account." : "Frames are available for this account."}</Text>
          )}
        </View>

        {/* Repository-aware preview target selection. The box only offers
            targets it reported as installed and runnable. */}
        <View style={[styles.card, { backgroundColor: c.bgCard, borderColor: c.border }]}>
          <Text style={[styles.cardLabel, { color: c.textPrimary }]}>{isTV ? "Run on Cloud Runner" : "Run on this machine"}</Text>
          <Text style={[styles.hint, { color: c.textMuted }]}>
            {selectedProject?.framework ? `${selectedProject.framework} project · choose a discovered target` : `Choose a target supported by this project and ${isTV ? "runner" : "machine"}`}
          </Text>
          {previewOptions.length ? (
            <View style={styles.previewOptions}>
              {previewOptions.map((option) => {
                const active = selectedPreview?.id === option.id;
                return (
                  <Pressable
                    key={option.id}
                    onPress={() => setPreviewTarget(option.id)}
                    style={({ focused }) => [styles.previewOption, { backgroundColor: active ? c.accent + "20" : c.bg, borderColor: active ? c.accent : c.border }, focused && styles.focused]}
                  >
                    <Text style={{ color: active ? c.accent : c.textPrimary, fontSize: 17, fontWeight: "700" }}>{option.label}</Text>
                    <Text style={{ color: c.textMuted, fontSize: 12, marginTop: 3 }}>{option.detail}</Text>
                  </Pressable>
                );
              })}
            </View>
          ) : (
            <Text style={[styles.hint, { color: c.warn }]}>No runnable preview target was discovered on this runner.</Text>
          )}
        </View>

        <Pressable
          onPress={() => isTV && !legacyTvRunner ? router.push("/projects") : setProjectSelected(false)}
          style={({ focused }) => [styles.changeProject, { borderColor: c.border, backgroundColor: c.bgCard }, focused && styles.focused]}
        >
          <Text style={{ color: c.textMuted, fontSize: 15 }}>Previewing {selectedProject?.name || "project"} · Change project</Text>
        </Pressable>

        {/* Status */}
        <View style={[styles.card, { backgroundColor: c.bgCard, borderColor: c.border }]}>
          <View style={styles.cardRow}>
            <Text style={[styles.cardLabel, { color: c.textPrimary }]}>Dev server</Text>
            <Text style={[styles.cardValue, { color: c.textSecondary }]}>
              {status ? status.servingLabel || (building ? "Starting…" : "Not serving") : "Not serving"}
            </Text>
          </View>
          {status && (
            <>
              <View style={styles.cardRow}>
                <Text style={[styles.cardLabel, { color: c.textPrimary }]}>Framework</Text>
                <Text style={[styles.cardValue, { color: c.textSecondary }]}>{status.framework || "-"} · port {status.port || "-"}</Text>
              </View>
              {status.vibeSessionId && (
                <View style={styles.cardRow}>
                  <Text style={[styles.cardLabel, { color: c.textPrimary }]}>Session</Text>
                  <Text style={[styles.cardValue, { color: c.textSecondary }]}>{status.vibeSessionId}</Text>
                </View>
              )}
            </>
          )}
          {status?.previewHealth?.reason ? (
            <Text style={[styles.hint, { color: c.warn }]}>{status.previewHealth.reason}</Text>
          ) : null}
        </View>

        {/* Controls */}
        <View style={styles.controls}>
          <Pressable
            hasTVPreferredFocus
            disabled={working || !selected || !selectedPreview}
            onPress={() => startPreview()}
            style={({ focused }) => [styles.btnPrimary, { backgroundColor: c.accent }, focused && styles.focused, (working || !selected || !selectedPreview) && { opacity: 0.5 }]}
          >
            <Text style={styles.btnPrimaryText}>{working ? "Working…" : serving ? "Restart preview" : `Run ${selectedPreview?.label || "preview"}`}</Text>
          </Pressable>
          <Pressable
            disabled={working || !serving}
            onPress={stopPreview}
            style={({ focused }) => [styles.btnGhost, { borderColor: c.border }, focused && styles.focused, (!serving || working) && { opacity: 0.5 }]}
          >
            <Text style={[styles.btnGhostText, { color: c.error }]}>Stop</Text>
          </Pressable>
        </View>

        {/* Dev frame-source override (test live frames without the box endpoint) */}
        <View style={[styles.card, { backgroundColor: c.bgCard, borderColor: c.border, marginTop: 8 }]}>
          <Text style={[styles.cardLabel, { color: c.textPrimary }]}>Frame source (dev)</Text>
          <TextInput
            style={[styles.devInput, { backgroundColor: c.bg, borderColor: c.border, color: c.textPrimary }]}
            placeholder="e.g. http://localhost:8787 (empty = box)"
            placeholderTextColor={c.textMuted}
            value={frameOverride}
            onChangeText={(v) => {
              setFrameOverride(v);
              AsyncStorage.setItem("@yaver/vibing_frame_url", v).catch(() => {});
            }}
            autoCapitalize="none"
            autoCorrect={false}
          />
        </View>

        {/* Lane proof */}
        {serving && (
          <>
            {frameUri ? (
              <View style={[styles.card, { backgroundColor: c.bgCard, borderColor: c.border, alignItems: "center" }, styles.livePreviewPanel]}>
                <Text style={[styles.cardLabel, { color: c.textPrimary, alignSelf: "flex-start" }]}>
                  Live preview {frameError ? "" : "· frames"} ✓
                </Text>
                <Image
                  source={{ uri: frameUri }}
                  style={[styles.liveFrame, serving && styles.liveFrameStreaming]}
                  resizeMode="contain"
                />
              </View>
            ) : (
              <Pressable
                onPress={verifyLane}
                style={({ focused }) => [styles.card, { backgroundColor: c.bgCard, borderColor: c.border }, styles.livePreviewPanel, focused && styles.focused]}
              >
                <Text style={[styles.cardLabel, { color: c.textPrimary }]}>
                  {laneHtml ? "Live lane verified ✓" : "Verify live lane (headless)"}
                </Text>
                {laneHtml ? (
                  <Text style={[styles.laneSnippet, { color: c.textSecondary }]} numberOfLines={4}>{laneHtml}</Text>
                ) : (
                  <Text style={[styles.hint, { color: c.textMuted }]}>
                    {frameError || "Fetching the running app from the runner with authentication. Visual rendering requires a supported Frames or WebRTC endpoint."}
                  </Text>
                )}
                {streamLogs.length > 0 && (
                  <View style={styles.streamLogPanel}>
                    {streamLogs.slice(-6).reverse().map((entry) => (
                      <Text key={entry.id} style={[styles.streamLogLine, { color: entry.level === "error" ? c.error : entry.level === "warn" ? c.warn : entry.level === "success" ? c.success : c.textSecondary }]} numberOfLines={1}>
                        {entry.at} · {entry.message}
                      </Text>
                    ))}
                  </View>
                )}
              </Pressable>
            )}
          </>
        )}
          </>
        )}
      </ScrollView>
    </SafeAreaView>
  );
}

const styles = StyleSheet.create({
  safe: { flex: 1 },
  container: { padding: 48, paddingBottom: 80 },
  headerRow: { flexDirection: "row", justifyContent: "space-between", alignItems: "center", marginBottom: 28 },
  remoteActions: { flexDirection: "row", gap: 8, alignItems: "center", marginBottom: 16 },
  headerAction: { borderWidth: 1, borderRadius: 8, paddingHorizontal: 10, paddingVertical: 7 },
  validationResult: { marginTop: 12, gap: 3 },
  titleBlock: {},
  title: { fontSize: 48, fontWeight: "800" },
  subtitle: { fontSize: 20, marginTop: 4 },
  badge: { borderRadius: 10, paddingHorizontal: 14, paddingVertical: 8 },
  badgeText: { fontSize: 16, fontWeight: "700", textTransform: "uppercase" },
  sectionLabel: { fontSize: 22, fontWeight: "700", marginTop: 18, marginBottom: 8 },
  card: { borderRadius: 16, borderWidth: 1, padding: 20, marginBottom: 12 },
  cardRow: { flexDirection: "row", justifyContent: "space-between", alignItems: "center", marginBottom: 8 },
  cardLabel: { fontSize: 17, fontWeight: "600" },
  cardValue: { fontSize: 17 },
  hint: { fontSize: 14, lineHeight: 20, marginTop: 4 },
  changeProject: { alignSelf: "flex-start", borderWidth: 1, borderRadius: 10, paddingHorizontal: 14, paddingVertical: 9, marginBottom: 12 },
  controls: { flexDirection: "row", gap: 16, marginTop: 8 },
  btnPrimary: { borderRadius: 12, paddingHorizontal: 28, paddingVertical: 16, alignItems: "center" },
  btnPrimaryText: { color: "#fff", fontSize: 20, fontWeight: "700" },
  btnGhost: { borderRadius: 12, borderWidth: 1, paddingHorizontal: 24, paddingVertical: 16, alignItems: "center" },
  btnGhostText: { fontSize: 20, fontWeight: "600" },
  focused: { transform: [{ scale: 1.03 }], opacity: 0.92 },
  laneSnippet: { fontSize: 12, marginTop: 8, fontFamily: "monospace" },
  liveFrame: { width: "100%", height: 420, marginTop: 12, borderRadius: 12 },
  streamingContainer: { paddingRight: "54%" },
  livePreviewPanel: { position: "absolute", top: 120, right: 48, width: "48%" },
  liveFrameStreaming: { height: 640 },
  projectSelection: { maxWidth: 1100, alignSelf: "center", width: "100%" },
  projectCard: { flexDirection: "row", alignItems: "center", borderWidth: 2, borderRadius: 16, padding: 22, marginBottom: 14 },
  projectIcon: { fontSize: 30, marginRight: 18 },
  projectName: { fontSize: 22, fontWeight: "700" },
  projectPath: { fontSize: 15, marginTop: 4 },
  projectFramework: { fontSize: 15, fontWeight: "600", marginLeft: 16 },
  previewOptions: { flexDirection: "row", flexWrap: "wrap", gap: 12, marginTop: 14 },
  previewOption: { minWidth: 180, borderWidth: 1, borderRadius: 12, padding: 14 },
  streamLogPanel: { marginTop: 14, borderTopWidth: StyleSheet.hairlineWidth, borderTopColor: "rgba(255,255,255,0.15)", paddingTop: 10, gap: 5 },
  streamLogLine: { fontSize: 12, fontFamily: "monospace" },
  devInput: { borderWidth: 1, borderRadius: 10, padding: 12, fontSize: 16, marginTop: 8 },
});
