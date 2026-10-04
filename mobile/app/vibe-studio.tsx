// vibe-studio.tsx — tablet Vibe Studio.
//
// Landscape (tablet-landscape): true split mirroring the tvOS
// RemoteRuntimeWebRTCView (40/58) and web VibeCodingView shapes —
//   LEFT  ≈55%  live app view
//   RIGHT ≈45%  chat + live console (StudioChatPane)
// The left pane has two lanes:
//   - "Browser" — DevPreview's WebView browser lane (interactive; box only
//     runs Metro/Vite, zero extra box load). This is the default when the
//     box reports a web target.
//   - "Live"    — LivePreviewPane frame lane (/vibing/preview/* SSE frames;
//     headless Chrome runs on the box, so use relay-wifi/cell profiles).
//   - "Device"  — a continuously streamed, interactive physical Android
//     phone/tablet attached to the remote Yaver host.
//
// Portrait (tablet-portrait): single-pane chat base with a "preview peek"
// panel — swipe/expand to bring the app view above the chat, collapsible.
//
// Everything here is additive and reuses the shared components (DevPreview,
// StudioChatPane, LivePreviewPane, AnsiConsoleText) — no task or preview
// machinery is re-implemented on this screen.

import React, { useCallback, useEffect, useRef, useState } from "react";
import {
  ActivityIndicator,
  Pressable,
  ScrollView,
  StyleSheet,
  Text,
  View,
} from "react-native";
import { Ionicons } from "@expo/vector-icons";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { useLocalSearchParams, useRouter } from "expo-router";
import { AppScreenHeader } from "../src/components/AppScreenHeader";
import { DevPreview } from "../src/components/DevPreview";
import { LivePreviewPane } from "../src/components/studio/LivePreviewPane";
import { RealDevicePane } from "../src/components/studio/RealDevicePane";
import { StudioChatPane } from "../src/components/studio/StudioChatPane";
import { useResponsiveLayout } from "../src/hooks/useResponsiveLayout";
import { useColors } from "../src/context/ThemeContext";
import { useDevice } from "../src/context/DeviceContext";
import { quicClient } from "../src/lib/quic";

type Lane = "device" | "browser" | "live";

type Project = { name: string; path: string; framework?: string; surfaces?: string[] };

export default function VibeStudioScreen() {
  const c = useColors();
  const insets = useSafeAreaInsets();
  const router = useRouter();
  const layout = useResponsiveLayout();
  const { activeDevice, connectionStatus } = useDevice();
  const params = useLocalSearchParams<{ project?: string }>();
  const requestedProject = typeof params.project === "string" ? params.project.trim().toLowerCase() : "";
  const connected = connectionStatus === "connected" && !!activeDevice;

  const [projects, setProjects] = useState<Project[]>([]);
  const [loadingProjects, setLoadingProjects] = useState(false);
  const [project, setProject] = useState<Project | null>(null);
  const [showProjectPicker, setShowProjectPicker] = useState(false);
  const [paramMissed, setParamMissed] = useState<string | null>(null);
  const [lane, setLane] = useState<Lane>("device");
  const [peekOpen, setPeekOpen] = useState(false);
  const [previewTargetUrl, setPreviewTargetUrl] = useState<string | null>(null);
  const [previewStarting, setPreviewStarting] = useState(false);
  const [previewError, setPreviewError] = useState<string | null>(null);
  const [previewLogState, setPreviewLogState] = useState<{ lines: string[]; live: boolean }>({ lines: [], live: false });
  // Drag-divider split ratio (web parity — WebReloadView/RuntimeLabView). The
  // left preview pane flexes to splitRatio, the right chat pane to 1-splitRatio.
  const [splitRatio, setSplitRatio] = useState(0.55);
  const dividerDragRef = useRef<{ startX: number; startRatio: number } | null>(null);
  const rowWidthRef = useRef(0);
  const loadedOnceRef = useRef(false);

  const landscape = layout.layoutClass === "tablet-landscape";
  const mobileTarget = !!project && (
    (project.surfaces || []).includes("mobile") ||
    /expo|react-native|flutter|ios|android|mobile/i.test(project.framework || "")
  );

  // Load projects from the box on connect. Auto-select the first mobile/web
  // project so the pane isn't empty on first open; the user can change it.
  const loadProjects = useCallback(async () => {
    if (!connected) return;
    setLoadingProjects(true);
    try {
      // The running preview is stronger evidence than the discovery inventory.
      // A nested project may already be serving while listProjects is stale or
      // has not finished scanning; keep that real workDir selectable.
      const [list, servingStatus] = await Promise.all([
        quicClient.listProjects(true),
        quicClient.getDevServerStatus().catch(() => null),
      ]);
      const mapped: Project[] = (list || [])
        .map((p) => ({ name: p.name, path: p.path, framework: p.framework, surfaces: p.surfaces }))
        .filter((p) => p.name && p.path);
      if (servingStatus?.workDir && !mapped.some((candidate) => candidate.path === servingStatus.workDir)) {
        mapped.unshift({
          name: servingStatus.workDir.split("/").filter(Boolean).pop() || servingStatus.framework || "Preview",
          path: servingStatus.workDir,
          framework: servingStatus.framework,
          surfaces: servingStatus.platform ? [servingStatus.platform] : undefined,
        });
      }
      setProjects(mapped);
      if (!loadedOnceRef.current) {
        loadedOnceRef.current = true;
        // Older entry points sent the human-facing label ("sfmg / mobile")
        // instead of the workDir. Accept its project-name prefix so an already
        // selected preview cannot fall into a false project-picker dead end.
        const requestedProjectName = requestedProject.split(/\s+\/\s+/)[0]?.trim() || requestedProject;
        const byParam = requestedProject
          ? mapped.find(
              (p) =>
                p.name.trim().toLowerCase() === requestedProject ||
                p.path.trim().toLowerCase() === requestedProject ||
                p.path.trim().toLowerCase().endsWith(`/${requestedProject}`) ||
                p.name.trim().toLowerCase() === requestedProjectName,
            )
          : undefined;
        if (requestedProject && !byParam) {
          // The URL pinned a project the box does not have. Say so instead of
          // silently opening the first mobile project.
          setParamMissed(`Project "${requestedProject}" isn't on the connected box — pick one below.`);
          setProject(null);
        } else {
          setParamMissed(null);
          setProject(
            byParam ||
              mapped.find((p) => (p.surfaces || []).includes("mobile") || /expo|react-native|flutter|mobile/i.test(p.framework || "")) ||
              mapped[0] ||
              null,
          );
        }
      }
    } catch {
      // advisory — the picker stays available, the pane shows a connect hint
    } finally {
      setLoadingProjects(false);
    }
  }, [connected, requestedProject]);

  useEffect(() => {
    void loadProjects();
  }, [loadProjects]);

  // Mark requests as coming from a tablet so the box tunes behavior for a
  // tablet-over-relay client (e.g. vibe-preview frame profile). Cleared on
  // unmount so phone navigation never inherits the marker.
  useEffect(() => {
    quicClient.setSurfaceMarker("mobile-tablet");
    return () => quicClient.clearSurfaceMarker();
  }, []);

  const handleRequestProject = useCallback(() => setShowProjectPicker(true), []);

  const pickProject = useCallback((p: Project) => {
    setProject(p);
    setParamMissed(null);
    setPreviewTargetUrl(null);
    setPreviewError(null);
    setShowProjectPicker(false);
  }, []);

  const refreshPreviewTarget = useCallback(async () => {
    if (!connected || !project) {
      setPreviewTargetUrl(null);
      return null;
    }
    try {
      const status = await quicClient.getDevServerStatus();
      const normalizedWorkDir = status?.workDir?.replace(/\/$/, "");
      const normalizedProject = project.path.replace(/\/$/, "");
      const sameProject = !normalizedWorkDir ||
        normalizedWorkDir === normalizedProject ||
        normalizedWorkDir.startsWith(`${normalizedProject}/`) ||
        normalizedProject.startsWith(`${normalizedWorkDir}/`);
      const ready = !!status && sameProject && (status.serving ?? status.running) && status.port > 0;
      const next = ready ? `http://127.0.0.1:${status.port}/` : null;
      setPreviewTargetUrl(next);
      return next;
    } catch {
      setPreviewTargetUrl(null);
      return null;
    }
  }, [connected, project]);

  useEffect(() => {
    void refreshPreviewTarget();
  }, [refreshPreviewTarget]);

  const startBrowserPreview = useCallback(async () => {
    if (!connected || !project || previewStarting) return;
    setPreviewStarting(true);
    setPreviewError(null);
    try {
      const started = await quicClient.startDevServer({
        framework: project.framework || "",
        workDir: project.path,
        web: true,
      });
      let url = started && (started.serving ?? started.running) && started.port > 0
        ? `http://127.0.0.1:${started.port}/`
        : null;
      // /dev/start may return while a cold web compile is still starting.
      for (let attempt = 0; !url && attempt < 12; attempt += 1) {
        await new Promise((resolve) => setTimeout(resolve, 1500));
        url = await refreshPreviewTarget();
      }
      if (!url) {
        throw new Error("The box accepted the preview request but did not begin serving it yet.");
      }
      setPreviewTargetUrl(url);
    } catch (e) {
      setPreviewError(e instanceof Error ? e.message : "Could not start the project preview.");
    } finally {
      setPreviewStarting(false);
    }
  }, [connected, previewStarting, project, refreshPreviewTarget]);

  const headerRight = (
    <View style={styles.headerRight}>
      {landscape ? (
        <View style={styles.laneSwitcher}>
          {(["device", "browser", "live"] as Lane[]).map((l) => (
            <Pressable
              key={l}
              onPress={() => setLane(l)}
              style={[styles.laneBtn, lane === l && { backgroundColor: c.accentSoft }]}
              accessibilityRole="button"
              accessibilityState={{ selected: lane === l }}
              accessibilityLabel={`${l === "device" ? "Real device" : l === "browser" ? "Browser" : "Live"} preview lane`}
            >
              <Text style={[styles.laneBtnText, { color: lane === l ? c.accent : c.textSecondary }]}>
                {l === "device" ? "Device" : l === "browser" ? "Browser" : "Live"}
              </Text>
            </Pressable>
          ))}
        </View>
      ) : null}
      {!project && (!requestedProject || Boolean(paramMissed)) ? (
        <Pressable
          onPress={handleRequestProject}
          style={[styles.projectBtn, { borderColor: c.border }]}
          accessibilityRole="button"
          accessibilityLabel="Pick project"
        >
          <Text style={{ color: c.textPrimary, fontSize: 12, fontWeight: "700" }} numberOfLines={1}>
            {loadingProjects ? "Loading projects…" : "Pick project"}
          </Text>
        </Pressable>
      ) : null}
    </View>
  );

  const projectPicker = showProjectPicker ? (
    <View style={[styles.pickerOverlay, { backgroundColor: "rgba(0,0,0,0.5)" }]}>
      <View
        style={[styles.pickerCard, { backgroundColor: c.bgCard, borderColor: c.border }]}
        accessibilityViewIsModal
      >
        <View style={[styles.pickerHeader, { borderBottomColor: c.borderSubtle }]}>
          <Text style={[styles.pickerTitle, { color: c.textPrimary }]}>Project</Text>
          <Pressable
            onPress={() => setShowProjectPicker(false)}
            hitSlop={10}
            accessibilityRole="button"
            accessibilityLabel="Close project picker"
          >
            <Ionicons name="close" size={18} color={c.textMuted} />
          </Pressable>
        </View>
        <ScrollView style={styles.pickerList} showsVerticalScrollIndicator>
          {loadingProjects ? <ActivityIndicator color={c.textMuted} style={{ padding: 16 }} /> : null}
          {projects.map((p) => (
            <Pressable
              key={p.path}
              onPress={() => pickProject(p)}
              style={[styles.pickerRow, project?.path === p.path && { backgroundColor: c.accentSoft }]}
            >
              <Ionicons name="folder-open" size={16} color={c.accent} />
              <View style={{ flex: 1, minWidth: 0 }}>
                <Text style={{ color: c.textPrimary, fontSize: 14, fontWeight: "600" }} numberOfLines={1}>
                  {p.name}
                </Text>
                <Text style={{ color: c.textMuted, fontSize: 11 }} numberOfLines={1}>
                  {p.path}
                </Text>
              </View>
              {p.framework ? <Text style={{ color: c.textMuted, fontSize: 11 }}>{p.framework}</Text> : null}
            </Pressable>
          ))}
          {projects.length === 0 && !loadingProjects ? (
            <Text style={{ color: c.textTertiary, padding: 16, fontSize: 13 }}>
              No projects discovered yet — connect a box and refresh.
            </Text>
          ) : null}
        </ScrollView>
      </View>
    </View>
  ) : null;

  return (
    <View style={[styles.safe, { backgroundColor: c.bg }]}>
      <AppScreenHeader title="Vibing" onBack={() => router.back()} right={headerRight} />

      {landscape ? (
        /* ── LANDSCAPE: preview LEFT / chat RIGHT ─────────────────── */
        <View
          style={styles.landscapeRow}
          testID="studio-landscape-row"
          onLayout={(e) => {
            rowWidthRef.current = e.nativeEvent.layout.width;
          }}
        >
          <View style={[styles.leftPane, { flex: splitRatio }]} testID="studio-left-pane">
            <View style={styles.deviceStage}>
              <View style={[
                mobileTarget || lane === "device" ? styles.deviceFrame : styles.browserFrame,
                { backgroundColor: "#09090b", borderColor: c.border },
              ]}>
                {mobileTarget || lane === "device" ? <View style={[styles.deviceSpeaker, { backgroundColor: c.border }]} /> : null}
                <View style={[
                  styles.deviceScreen,
                  !mobileTarget && lane !== "device" && styles.browserScreen,
                  { backgroundColor: c.bgCard },
                ]}>
                  {/* Persistent phone-frame content. The base empty pane sits
                      beneath EVERY lane so the frame is never a blank box while
                      the box has no dev server or the preview has not rendered
                      (paneMode DevPreview returns null until a status exists). */}
                  <View style={[styles.emptyPaneFill, { borderColor: c.borderSubtle }]}> 
                    <Ionicons name={mobileTarget ? "phone-portrait-outline" : "browsers-outline"} size={28} color={c.textTertiary} />
                    <Text style={{ color: c.textTertiary, fontSize: 13, marginTop: 8, textAlign: "center" }}>
                      {project
                        ? "Preview this project beside the conversation."
                        : requestedProject
                          ? connected ? "Opening the selected project…" : "Connect the box to open the selected project."
                          : "Pick a project to open its preview."}
                    </Text>
                    {paramMissed ? (
                      <Text style={{ color: c.warn, fontSize: 12, marginTop: 8, textAlign: "center", paddingHorizontal: 12 }}>
                        {paramMissed}
                      </Text>
                    ) : null}
                    {previewError ? (
                      <Text style={{ color: c.error, fontSize: 12, marginTop: 8, textAlign: "center", paddingHorizontal: 12 }}>
                        {previewError}
                      </Text>
                    ) : null}
                    {project && connected && !previewTargetUrl ? (
                      <Pressable
                        onPress={() => void startBrowserPreview()}
                        disabled={previewStarting}
                        style={[styles.startPreviewBtn, { backgroundColor: c.accentSoft }]}
                        accessibilityRole="button"
                        accessibilityLabel="Start project preview"
                      >
                        {previewStarting ? <ActivityIndicator size="small" color={c.accent} /> : null}
                        <Text style={{ color: c.accent, fontSize: 13, fontWeight: "700" }}>
                          {previewStarting ? "Starting preview…" : "Start preview"}
                        </Text>
                      </Pressable>
                    ) : null}
                  </View>
                  {lane === "device" && project ? (
                    <View style={styles.paneHost}>
                      <RealDevicePane projectPath={project.path} framework={project.framework || "react-native"} />
                    </View>
                  ) : lane === "browser" ? (
                    <View style={styles.paneHost}>
                      <DevPreview paneMode exitLabel="Go to Vibe" onLogStateChange={setPreviewLogState} />
                    </View>
                  ) : project && previewTargetUrl ? (
                    <View style={styles.paneHost}>
                      <LivePreviewPane key={`${project.path}:${previewTargetUrl}`} project={project.name} targetUrl={previewTargetUrl} />
                    </View>
                  ) : null}
                </View>
              </View>
            </View>
          </View>
          {/* Drag divider — same pointer pattern as the web's split panes. */}
          <View
            style={styles.divider}
            testID="studio-divider"
            accessibilityRole="adjustable"
            accessibilityLabel="Resize preview split"
            accessibilityValue={{ min: 32, max: 72, now: Math.round(splitRatio * 100), text: `${Math.round(splitRatio * 100)} percent preview` }}
            accessibilityActions={[{ name: "increment" }, { name: "decrement" }]}
            onAccessibilityAction={(event) => {
              const delta = event.nativeEvent.actionName === "increment" ? 0.05 : -0.05;
              setSplitRatio((value) => Math.max(0.32, Math.min(0.72, value + delta)));
            }}
            onStartShouldSetResponder={() => true}
            onResponderGrant={(e) => {
              dividerDragRef.current = { startX: e.nativeEvent.pageX, startRatio: splitRatio };
            }}
            onResponderMove={(e) => {
              const d = dividerDragRef.current;
              if (!d) return;
              const w = rowWidthRef.current || 1;
              const ratio = Math.max(0.32, Math.min(0.72, d.startRatio + (e.nativeEvent.pageX - d.startX) / w));
              setSplitRatio(ratio);
            }}
            onResponderRelease={() => {
              dividerDragRef.current = null;
            }}
            onResponderTerminate={() => {
              dividerDragRef.current = null;
            }}
          >
            <View style={[styles.dividerKnob, { backgroundColor: c.border }]} />
          </View>
          <View style={[styles.rightPane, { flex: 1 - splitRatio }]} testID="studio-right-pane">
            <StudioChatPane
              projectPath={project?.path}
              projectName={project?.name}
              onRequestProject={handleRequestProject}
              previewLogs={previewLogState.lines}
              previewLogsLive={previewLogState.live || previewStarting}
            />
          </View>
        </View>
      ) : (
        /* ── PORTRAIT: chat base + preview peek ────────────────────── */
        <View style={styles.portraitCol}>
          <View style={styles.portraitChat}>
            <StudioChatPane
              projectPath={project?.path}
              projectName={project?.name}
              onRequestProject={handleRequestProject}
              previewLogs={previewLogState.lines}
              previewLogsLive={previewLogState.live || previewStarting}
            />
          </View>
          {peekOpen ? (
            <View style={[styles.peekPanel, { borderTopColor: c.border }]}>
              <View style={[styles.peekHeader, { borderBottomColor: c.borderSubtle }]}>
                <Text style={[styles.peekTitle, { color: c.textSecondary }]}>Preview</Text>
                <Pressable
                  onPress={() => setPeekOpen(false)}
                  hitSlop={8}
                  style={({ pressed }) => [styles.laneBtn, pressed && { opacity: 0.6 }]}
                >
                  <Text style={{ color: c.textMuted, fontSize: 12, fontWeight: "600" }}>Collapse</Text>
                </Pressable>
              </View>
              {project && previewTargetUrl ? (
                <LivePreviewPane key={`${project.path}:${previewTargetUrl}`} project={project.name} targetUrl={previewTargetUrl} height={Math.min(360, Math.round(layout.height * 0.36))} />
              ) : (
                <View style={[styles.emptyPane, { borderColor: c.borderSubtle }]}>
                  <Text style={{ color: c.textTertiary, fontSize: 13, textAlign: "center" }}>
                    {project ? previewError || "Start the project preview to show it here." : "Pick a project to preview."}
                  </Text>
                  {project && connected ? (
                    <Pressable
                      onPress={() => void startBrowserPreview()}
                      disabled={previewStarting}
                      style={[styles.startPreviewBtn, { backgroundColor: c.accentSoft }]}
                      accessibilityRole="button"
                      accessibilityLabel="Start project preview"
                    >
                      {previewStarting ? <ActivityIndicator size="small" color={c.accent} /> : null}
                      <Text style={{ color: c.accent, fontSize: 13, fontWeight: "700" }}>
                        {previewStarting ? "Starting preview…" : "Start preview"}
                      </Text>
                    </Pressable>
                  ) : null}
                </View>
              )}
            </View>
          ) : (
            <Pressable
              onPress={() => setPeekOpen(true)}
              style={[styles.peekTab, { borderTopColor: c.border, backgroundColor: c.surface }]}
              accessibilityRole="button"
              accessibilityLabel="Open project preview"
            >
              <Ionicons name="expand-outline" size={14} color={c.textSecondary} />
              <Text style={{ color: c.textSecondary, fontSize: 12, fontWeight: "600" }}>Preview</Text>
            </Pressable>
          )}
        </View>
      )}

      {projectPicker}
      <View style={{ height: insets.bottom }} />
    </View>
  );
}

const styles = StyleSheet.create({
  safe: { flex: 1 },
  headerRight: { flexDirection: "row", alignItems: "center", gap: 10 },
  laneSwitcher: {
    flexDirection: "row",
    borderWidth: StyleSheet.hairlineWidth,
    borderRadius: 8,
    overflow: "hidden",
  },
  laneBtn: { paddingHorizontal: 10, paddingVertical: 5, borderRadius: 6 },
  laneBtnText: { fontSize: 12, fontWeight: "700" },
  projectBtn: {
    borderWidth: StyleSheet.hairlineWidth,
    borderRadius: 8,
    paddingHorizontal: 10,
    paddingVertical: 6,
    maxWidth: 160,
  },
  landscapeRow: { flex: 1, flexDirection: "row", minHeight: 0 },
  leftPane: {
    minWidth: 0,
    padding: 10,
    borderRightWidth: StyleSheet.hairlineWidth,
    borderRightColor: "transparent",
  },
  divider: {
    width: 16,
    alignItems: "center",
    justifyContent: "center",
    alignSelf: "stretch",
    marginHorizontal: -4,
    zIndex: 3,
  },
  dividerKnob: {
    width: 4,
    height: 48,
    borderRadius: 2,
    opacity: 0.45,
  },
  deviceStage: { flex: 1, alignItems: "center", justifyContent: "center", minHeight: 0 },
  deviceFrame: {
    width: "100%",
    maxWidth: 430,
    height: "100%",
    maxHeight: 760,
    borderRadius: 30,
    borderWidth: 8,
    paddingTop: 8,
    paddingHorizontal: 4,
    paddingBottom: 8,
    alignItems: "center",
  },
  browserFrame: {
    width: "100%",
    height: "100%",
    borderRadius: 18,
    borderWidth: 4,
    padding: 4,
  },
  deviceSpeaker: { width: 76, height: 5, borderRadius: 3, marginBottom: 6 },
  deviceScreen: { flex: 1, width: "100%", minHeight: 0, borderRadius: 20, overflow: "hidden" },
  browserScreen: { borderRadius: 12 },
  // Absolute-fill base beneath every lane so the phone frame is never blank
  // while the preview is loading or the box has no dev server.
  emptyPaneFill: {
    ...StyleSheet.absoluteFillObject,
    alignItems: "center",
    justifyContent: "center",
    borderWidth: StyleSheet.hairlineWidth,
    borderRadius: 20,
  },
  paneHost: {
    ...StyleSheet.absoluteFillObject,
    overflow: "hidden",
  },
  rightPane: { minWidth: 0 },
  portraitCol: { flex: 1, minHeight: 0 },
  portraitChat: { flex: 1, minHeight: 0 },
  peekPanel: {
    borderTopWidth: StyleSheet.hairlineWidth,
  },
  peekHeader: {
    flexDirection: "row",
    alignItems: "center",
    justifyContent: "space-between",
    paddingHorizontal: 12,
    paddingVertical: 6,
    borderBottomWidth: StyleSheet.hairlineWidth,
  },
  peekTitle: { fontSize: 12, fontWeight: "700", textTransform: "uppercase", letterSpacing: 0.4 },
  peekTab: {
    flexDirection: "row",
    alignItems: "center",
    justifyContent: "center",
    gap: 6,
    paddingVertical: 8,
    borderTopWidth: StyleSheet.hairlineWidth,
  },
  emptyPane: {
    minHeight: 220,
    alignItems: "center",
    justifyContent: "center",
    padding: 18,
    gap: 10,
    borderWidth: StyleSheet.hairlineWidth,
    borderRadius: 12,
  },
  startPreviewBtn: {
    minHeight: 38,
    borderRadius: 10,
    paddingHorizontal: 14,
    paddingVertical: 9,
    flexDirection: "row",
    alignItems: "center",
    justifyContent: "center",
    gap: 8,
    marginTop: 10,
  },
  pickerOverlay: {
    ...StyleSheet.absoluteFillObject,
    justifyContent: "center",
    alignItems: "center",
    padding: 24,
  },
  pickerCard: {
    width: "90%",
    maxWidth: 480,
    maxHeight: "70%",
    borderRadius: 16,
    borderWidth: 1,
    overflow: "hidden",
  },
  pickerHeader: {
    flexDirection: "row",
    alignItems: "center",
    justifyContent: "space-between",
    paddingHorizontal: 16,
    paddingVertical: 12,
    borderBottomWidth: StyleSheet.hairlineWidth,
  },
  pickerTitle: { fontSize: 15, fontWeight: "700" },
  pickerList: { paddingVertical: 6 },
  pickerRow: {
    flexDirection: "row",
    alignItems: "center",
    gap: 10,
    paddingHorizontal: 16,
    paddingVertical: 10,
  },
});
