// attach.tsx — the ATTACHED surface. Yaver rendering Yaver, full-screen.
//
// ── The escape lives out here, and that is the whole safety argument ────────
//
// Browser Dogfood runs in a WebView, while Hermes Dogfood uses the native
// AppDelegate/YaverShakeDetector escape that survives replacement of the JS
// runtime. Both lanes therefore keep their exit owner outside guest content.
//
// This browser-lane escape stays OUTSIDE the WebView. BrowserVibeBubble is a
// native sibling, always mounted above the attached content.
//
// ── What the user sees ─────────────────────────────────────────────────────
//
// The attached app, full-screen. A quiet Y exits after confirmation; a quiet ↻
// re-renders Yaver. Status only appears while it is useful. Not a diagnostics
// wall: an advisory that squeezes the action lane to zero height is a worse bug
// than missing information (build 482).

import { router, usePathname } from "expo-router";
import { StatusBar } from "expo-status-bar";
import React, { useCallback, useEffect, useRef, useState } from "react";
import type { WebView as NativeWebView } from "react-native-webview";
import {
  ActivityIndicator,
  Alert,
  Platform,
  Pressable,
  ScrollView,
  StyleSheet,
  Text,
  View,
} from "react-native";
import { WebView } from "../src/components/WebViewCompat";
import { useTheme } from "../src/context/ThemeContext";
import { useDevice } from "../src/context/DeviceContext";
import {
  ATTACH_REFRESH_MS,
  refreshAttachSession,
  reloadAttachedDogfoodBrowserLane,
  requestDogfoodFixWithAI,
  stopAttachSession,
} from "../src/lib/attachClient";
import { ATTACH_SENTINEL_KEY } from "../src/lib/attachMode";
import { planRevert } from "../src/lib/runtimeMode";
import AsyncStorage from "@react-native-async-storage/async-storage";
import {
  setActivePreviewLane,
  subscribeBrowserRender,
} from "../src/lib/feedbackTrigger";
import { appLog } from "../src/lib/logger";
import { DOGFOOD_CHECKOUT_KEY, parseDogfoodRenderMessage } from "../src/lib/dogfoodRenderBridge";
import {
  DOGFOOD_EXCEPTION_CAPTURE_SCRIPT,
  dogfoodExceptionFixPrompt,
  parseDogfoodGuestException,
  type DogfoodGuestException,
} from "../src/lib/dogfoodExceptionBridge";
import { openTaskBus } from "../src/lib/runningTasksBus";
import { BrowserVibeBubble } from "../src/components/BrowserVibeBubble";
import { useRouteParamsCompat } from "../src/lib/useRouteParamsCompat";
import { useDogfoodOverlay } from "../src/context/DogfoodOverlayContext";
import { isAgentPreviewDocumentRequest } from "../src/lib/agentPreviewUrl";
import { PREVIEW_READY_SCRIPT } from "../src/lib/previewReadyScript";

function elapsedLabel(sinceMs: number): string {
  const secs = Math.max(0, Math.floor((Date.now() - sinceMs) / 1000));
  const m = Math.floor(secs / 60);
  const s = secs % 60;
  return m > 0 ? `${m}:${String(s).padStart(2, "0")}` : `${s}s`;
}

const ATTACHED_RENDER_TIMEOUT_MS = 45_000;

function loadingPhaseForProbe(state: { reason?: string; mountChildren?: number } | undefined): string {
  switch (state?.reason) {
    case "document_not_ready":
    case "empty_body":
      return "Receiving the Yaver page";
    case "empty_mount":
    case "mount_without_visible_content":
      return "Starting the Yaver interface";
    case "agent_starting_response":
      return "Waiting for the Yaver web build";
    default:
      return "Opening Yaver";
  }
}

export default function AttachScreen() {
  const pathname = usePathname();
  const { colors: c, theme } = useTheme();
  const { activeDevice } = useDevice();
  const { end: endDogfoodOverlay, goHome, reportIssue } = useDogfoodOverlay();
  const params = useRouteParamsCompat<{
    sessionId?: string;
    url?: string;
    workDir?: string;
    runner?: string;
    deviceId?: string;
    deviceName?: string;
    usageMode?: string;
    renderBehavior?: string;
    sessionBehavior?: string;
  }>();

  const webViewRef = useRef<NativeWebView | null>(null);
  const [webViewKey, setWebViewKey] = useState(0);
  const [loading, setLoading] = useState(true);
  const [loadingSince, setLoadingSince] = useState(() => Date.now());
  const [loadingPhase, setLoadingPhase] = useState("Opening Yaver");
  const [loadingLogs, setLoadingLogs] = useState<string[]>(["Preparing the phone WebView…"]);
  const [, forceTick] = useState(0);
  const [lastEvent, setLastEvent] = useState<{ label: string; at: number } | null>(null);
  const [fatal, setFatal] = useState<{ code: string; message: string; remedy?: string } | null>(null);
  const [guestException, setGuestException] = useState<DogfoodGuestException | null>(null);
  const [fixing, setFixing] = useState(false);
  const [fixTaskId, setFixTaskId] = useState<string | null>(null);
  const reloadInFlight = useRef(false);
  const deviceId = params.deviceId || activeDevice?.id || "";
  const deviceName = params.deviceName || activeDevice?.name || "the box";
  const sessionId = params.sessionId || "";
  const attachedUrl = params.url || "";
  const pushLoadingLog = useCallback((line: string) => {
    setLoadingLogs((current) => current[current.length - 1] === line
      ? current
      : [...current, line].slice(-5));
  }, []);

  const reloadDogfoodSurface = useCallback(async (source: string, mode: "fast" | "full" = "fast") => {
    if (reloadInFlight.current) {
      throw new Error("A Dogfood reload is already in progress. Wait for the preview to finish loading.");
    }
    reloadInFlight.current = true;
    appLog("info", `dogfood: refreshing attached surface (${source})`);
    try {
      const result = await reloadAttachedDogfoodBrowserLane(deviceId, params.workDir || "", mode);
      if (!result.ok) {
        const message = result.message || result.error || "Dogfood reload failed.";
        setFatal({
          code: result.code || "DOGFOOD_RELOAD_FAILED",
          message,
          remedy: result.remedy || "Return to Dogfood Settings and restart the Browser lane for this checkout.",
        });
        throw new Error(result.remedy ? `${message} ${result.remedy}` : message);
      }
      setFatal(null);
      setGuestException(null);
      setFixTaskId(null);
      setLoading(true);
      setLoadingSince(Date.now());
      setLoadingPhase("Opening the refreshed Yaver");
      setLoadingLogs(["Reload accepted by the selected machine…"]);
      setLastEvent({
        label: source === "manual" ? (mode === "full" ? "Restarting Yaver" : "Re-rendering Yaver") : "Refreshing after task completion",
        at: Date.now(),
      });
      setWebViewKey((k) => k + 1);
      return true;
    } finally {
      setTimeout(() => {
        reloadInFlight.current = false;
      }, 1500);
    }
  }, [deviceId, params.workDir]);

  const startGuestExceptionFix = useCallback(async () => {
    if (!guestException || fixing || !deviceId) return;
    setFixing(true);
    try {
      const prompt = dogfoodExceptionFixPrompt({
        exception: guestException,
        checkout: params.workDir || "",
        previewUrl: attachedUrl,
        deviceName,
      });
      const { taskId } = await requestDogfoodFixWithAI(
        deviceId,
        params.workDir || "",
        params.runner || "",
        prompt,
      );
      setFixTaskId(taskId);
      setLastEvent({ label: "Exception sent to the coding runner", at: Date.now() });
      router.navigate("/(tabs)/tasks" as any);
      openTaskBus.publish(taskId);
    } catch (err) {
      setFatal({
        code: "DOGFOOD_AI_FIX_START_FAILED",
        message: err instanceof Error ? err.message : String(err),
        remedy: "Reconnect the coding box, then retry Fix exception. The captured stack remains available on this preview.",
      });
    } finally {
      setFixing(false);
    }
  }, [attachedUrl, deviceId, deviceName, fixing, guestException, params.runner, params.workDir]);

  useEffect(() => {
    reportIssue(guestException ? {
      message: `${guestException.code} · ${guestException.message}`,
      fix: startGuestExceptionFix,
    } : null);
    return () => reportIssue(null);
  }, [guestException, reportIssue, startGuestExceptionFix]);

  useEffect(() => {
    if (attachedUrl) return;
    setFatal({
      code: "DOGFOOD_NO_RENDER_URL",
      message: "The primary device did not provide a browser-lane URL for Yaver.",
      remedy: "Return to Production and enter Dogfood mode again. Entry will re-run the Expo and browser probes.",
    });
  }, [attachedUrl]);

  useEffect(() => {
    if (!lastEvent) return;
    const timer = setTimeout(() => setLastEvent(null), 4500);
    return () => clearTimeout(timer);
  }, [lastEvent]);

  // The status line must keep MOVING. A frozen "connecting" is the thing that
  // makes a working system look hung, so tick once a second while attached.
  useEffect(() => {
    const t = setInterval(() => forceTick((n) => n + 1), 1000);
    return () => clearInterval(t);
  }, []);

  // Keep the capability alive while the surface is open. The agent's token TTL
  // is 10 minutes; we re-mint every 4 so a slow network cannot let it lapse
  // mid-session and drop the user onto a 401 they did nothing to cause.
  useEffect(() => {
    if (!sessionId || !deviceId) return;
    let cancelled = false;
    const tick = async () => {
      const res = await refreshAttachSession(deviceId, sessionId);
      if (cancelled) return;
      if (!res.ok) {
        // Say it. A silently-expired session would present as the attached app
        // mysteriously failing to load anything.
        setFatal({
          code: res.code || "DOGFOOD_SESSION_REFRESH_FAILED",
          message: res.error || "The attach session expired.",
          remedy: res.remedy || "Switch to Production, then open Dogfood mode again.",
        });
      }
    };
    const timer = setInterval(tick, ATTACH_REFRESH_MS);
    return () => {
      cancelled = true;
      clearInterval(timer);
    };
  }, [sessionId, deviceId]);

  // This surface IS a browser-lane preview, so it claims the lane and listens
  // for the post-task render the Tasks tab queues.
  useEffect(() => {
    setActivePreviewLane("browser");
    const unsub = subscribeBrowserRender((source) => {
      void reloadDogfoodSurface(source).catch((error) => {
        appLog("warn", `dogfood: automatic browser refresh failed: ${error instanceof Error ? error.message : String(error)}`);
      });
    });
    return () => {
      unsub();
      setActivePreviewLane(null);
    };
  }, [reloadDogfoodSurface]);

  // REVERT — back to the app you actually installed.
  //
  // In Attach Mode the host native app IS the installed TestFlight/Play build;
  // the dev copy is only a WebView on top of it. So reverting is always
  // possible and always fast: drop the surface and the native app underneath
  // is the real one. That is a property worth stating, because it is what
  // makes the whole mode safe to hand someone.
  //
  // planRevert() enumerates what must happen so none of it can be quietly
  // skipped. Both halves of the revoke matter: clearing local state while a
  // capability stays live on the box is the false green this repo keeps
  // finding — the UI says reverted, the operation says attached.
  const detach = useCallback(async () => {
    const plan = planRevert("attached-yaver")!;

    if (plan.revokeAttachSession) {
      const revoked = await stopAttachSession(deviceId, sessionId);
      if (!revoked) {
        // Non-fatal: the session idles out on the box within 30 minutes even
        // if this call never lands. Say it rather than swallow it.
        appLog("warn", "attach: server-side revoke did not confirm; session will idle out");
      }
    }
    if (plan.clearAttachSentinel) {
      // A stale sentinel would make the NEXT launch think it is still the
      // attached copy, hide Attach Mode behind the nesting guard, and show a
      // dev badge on the real app.
      try {
        await AsyncStorage.removeItem(ATTACH_SENTINEL_KEY);
      } catch {
        // best-effort
      }
    }
    setActivePreviewLane(null);
    await endDogfoodOverlay();
    router.replace("/(tabs)/tasks" as any);
  }, [deviceId, endDogfoodOverlay, sessionId]);

  const confirmDetach = useCallback(() => {
    Alert.alert(
      "Switch to Production mode",
      "Stop rendering the development build and return to the Yaver you installed. " +
        "Your work on the box is untouched — only this preview ends.",
      [
        { text: "Cancel", style: "cancel" },
        { text: "Production", style: "destructive", onPress: () => void detach() },
      ],
    );
  }, [detach]);

  useEffect(() => {
    if (!loading || !attachedUrl) return;
    const remaining = Math.max(0, ATTACHED_RENDER_TIMEOUT_MS - (Date.now() - loadingSince));
    const timeout = setTimeout(() => {
      setLoading(false);
      reloadInFlight.current = false;
      setFatal({
        code: "DOGFOOD_WEBVIEW_RENDER_TIMEOUT",
        message: "The phone reached Yaver, but its WebView did not paint the interface within 45 seconds.",
        remedy: "Retry the attached surface. If it repeats, keep this named failure visible and use Fix with AI.",
      });
    }, remaining);
    return () => clearTimeout(timeout);
  }, [attachedUrl, loading, loadingSince]);

  // The sentinel tells the INNER Yaver what it is, so it refuses to offer
  // Attach Mode again (an infinite mirror). It carries no authority — the real
  // capability is an HttpOnly cookie this JS cannot read, which is the point.
  const injectedBeforeLoad = `${DOGFOOD_EXCEPTION_CAPTURE_SCRIPT}
  (function(){try{
    window.localStorage.setItem(${JSON.stringify(ATTACH_SENTINEL_KEY)}, "1");
    window.localStorage.setItem(${JSON.stringify(DOGFOOD_CHECKOUT_KEY)}, ${JSON.stringify(params.workDir || "")});
    window.localStorage.setItem("yaver.secure.yaver_theme", ${JSON.stringify(theme)});
  }catch(e){}})(); true;`;

  return (
    <View style={[styles.root, { backgroundColor: c.bg }]}>
      {/*
        The browser app owns its own safe areas. Wrapping it in a native
        SafeAreaView created a second top/bottom inset: the exact white bands
        seen on iPhone, plus a shorter viewport than the real app. Keep the
        WebView edge-to-edge and let its viewport-fit=cover document place its
        own header/tab bar around the system areas once.
      */}
      <StatusBar style={theme === "dark" ? "light" : "dark"} translucent />
      <View style={styles.surface}>
        {attachedUrl ? (
          <WebView
            key={webViewKey}
            ref={webViewRef}
            source={{ uri: attachedUrl }}
            injectedJavaScriptBeforeContentLoaded={injectedBeforeLoad}
            injectedJavaScript={PREVIEW_READY_SCRIPT}
            sharedCookiesEnabled
            thirdPartyCookiesEnabled
            // The attach capability is a cookie; without this the WebView would
            // not send it and every request would 401.
            originWhitelist={["*"]}
            automaticallyAdjustContentInsets={false}
            contentInsetAdjustmentBehavior="never"
            bounces={false}
            overScrollMode="never"
            scalesPageToFit={false}
            textZoom={100}
            onLoadStart={() => {
              setLoading(true);
              setLoadingSince(Date.now());
              setLoadingPhase("Receiving the Yaver page");
              setLoadingLogs(["Requesting the verified Dogfood route…"]);
            }}
            onLoadEnd={() => {
              // Expo can finish the document load before React paints, or keep
              // HMR requests alive after it paints. First-paint messages below
              // own the verdict; onLoadEnd is not operational proof.
              setLoadingPhase("Starting the Yaver interface");
              pushLoadingLog("Document received; waiting for Yaver to paint…");
            }}
            onMessage={(event) => {
              try {
                const readiness = JSON.parse(event.nativeEvent.data);
                if (readiness?.t === "yaver-preview-probe") {
                  if (readiness.state?.reason === "http_error_document") {
                    setLoading(false);
                    reloadInFlight.current = false;
                    setFatal({
                      code: "DOGFOOD_WEBVIEW_HTTP_DOCUMENT",
                      message: "The phone received the agent's 404 page instead of Yaver.",
                      remedy: "Update the Yaver agent on this machine, return to Production, and retry Dogfood.",
                    });
                    return;
                  }
                  const phase = loadingPhaseForProbe(readiness.state);
                  setLoadingPhase(phase);
                  pushLoadingLog(`${phase} · ${String(readiness.state?.reason || "checking")}`);
                  return;
                }
                if (readiness?.t === "yaver-rendered") {
                  setLoading(false);
                  reloadInFlight.current = false;
                  setLastEvent((current) => current ? { label: "Yaver rendered", at: Date.now() } : null);
                  return;
                }
                if (readiness?.t === "yaver-preview-timeout") {
                  setLoading(false);
                  reloadInFlight.current = false;
                  setFatal({
                    code: "DOGFOOD_WEBVIEW_RENDER_TIMEOUT",
                    message: "Yaver loaded on the phone, but the interface never painted.",
                    remedy: "Retry the attached surface. If it repeats, use Fix with AI with this named failure visible.",
                  });
                  return;
                }
              } catch {
                // Non-JSON messages belong to the existing Dogfood bridges.
              }
              const exception = parseDogfoodGuestException(event.nativeEvent.data);
              if (exception) {
                setLoading(false);
                reloadInFlight.current = false;
                setGuestException(exception);
                setLastEvent({ label: "Preview exception captured", at: Date.now() });
                return;
              }
              const message = parseDogfoodRenderMessage(event.nativeEvent.data);
              if (message) {
                void reloadDogfoodSurface(message.source).catch((error) => {
                  appLog("warn", `dogfood: guest-requested refresh failed: ${error instanceof Error ? error.message : String(error)}`);
                });
              }
            }}
            onError={(e) => {
              setLoading(false);
              reloadInFlight.current = false;
              const d = e.nativeEvent;
              setFatal({
                code: "DOGFOOD_WEBVIEW_LOAD_FAILED",
                message: `The attached surface failed to load: ${d.description || "unknown error"}`,
                remedy:
                  "Check the dev server is still running on the box. Detach and re-attach to restart it.",
              });
            }}
            onHttpError={(e) => {
              const status = e.nativeEvent.statusCode;
              const requestUrl = (e.nativeEvent as { url?: string }).url;
              if (!isAgentPreviewDocumentRequest(requestUrl, attachedUrl)) {
                // A WebView uses this callback for every resource. The exact
                // Dogfood document was already probed before handoff, and the
                // running app must survive an optional asset/backend 404.
                appLog("warn", `dogfood: ignored subresource HTTP ${status}`);
                pushLoadingLog(`Optional page resource returned HTTP ${status}; continuing…`);
                return;
              }
              setLoading(false);
              reloadInFlight.current = false;
              setFatal({
                code: "DOGFOOD_WEBVIEW_HTTP_FAILED",
                message: `The attached surface returned HTTP ${status}.`,
                remedy: status === 404
                  ? "The selected machine is serving Yaver, but this device received the wrong preview route. Return to Production and retry after updating Yaver."
                  : "Return to Production and retry. If it repeats, keep the named HTTP status visible and use Fix with AI.",
              });
            }}
            style={{ flex: 1, backgroundColor: c.bg }}
          />
        ) : (
          <View style={styles.center}>
            <Text style={{ color: c.textMuted, fontSize: 13, textAlign: "center", paddingHorizontal: 24 }}>
              No Dogfood URL was passed to this screen. Return to More and open Dogfood mode again.
            </Text>
          </View>
        )}

        {loading ? (
          <View style={[styles.loading, { backgroundColor: c.bg + "CC" }]} pointerEvents="none">
            <View style={[styles.loadingCard, { backgroundColor: c.bgCard, borderColor: c.border }]}>
              <ActivityIndicator color={c.accent} />
              <Text style={[styles.loadingTitle, { color: c.textPrimary }]}>{loadingPhase}</Text>
              <Text style={[styles.loadingDetail, { color: c.textMuted }]}>
                {`${deviceName} · ${elapsedLabel(loadingSince)}`}
              </Text>
              <Text style={[styles.loadingHint, { color: c.textMuted }]}>Yaver will show a named failure if first paint does not arrive.</Text>
              <ScrollView
                style={[styles.loadingLog, { borderColor: c.border }]}
                contentContainerStyle={styles.loadingLogContent}
                accessibilityLabel="Dogfood loading logs"
                showsVerticalScrollIndicator={false}
              >
                {loadingLogs.map((line, index) => (
                  <Text key={`${index}-${line}`} style={[styles.loadingLogLine, { color: c.textMuted }]}>{line}</Text>
                ))}
              </ScrollView>
            </View>
          </View>
        ) : null}
      </View>

      {/* Native sibling: Vibing, Fast Reload, two-level routing, and escape. */}
      {pathname === "/attach" ? <BrowserVibeBubble
        projectPath={params.workDir}
        projectName="Yaver"
        usageMode={params.usageMode === "chat-only" || params.usageMode === "reload-and-chat" ? params.usageMode : "reload-only"}
        renderBehavior={params.renderBehavior === "auto-on-request" ? "auto-on-request" : "manual"}
        sessionBehavior={params.sessionBehavior === "new-session" ? "new-session" : "resume-last"}
        exitLabel="Open Dogfood"
        onGoHome={goHome}
        onExitPreview={confirmDetach}
        onReload={(kind) => reloadDogfoodSurface("manual", kind)}
      /> : null}

      {lastEvent ? (
        <View pointerEvents="none" style={[styles.quietStatus, { backgroundColor: c.bgCard, borderColor: c.border }]}>
          <Text numberOfLines={1} style={{ color: c.textMuted, fontSize: 11 }}>
            {lastEvent.label} · {deviceName}
          </Text>
        </View>
      ) : null}

      {fatal ? (
        <View style={styles.fatalScrim}>
          <View style={[styles.fatal, { borderColor: c.errorBorder, backgroundColor: c.bgCard }]}>
            <Text style={{ color: c.textPrimary, fontSize: 13, fontWeight: "700", lineHeight: 18 }}>
              Dogfood render stopped
            </Text>
            <Text style={{ color: c.error, fontSize: 11, marginTop: 4, fontWeight: "700" }}>
              {fatal.code}
            </Text>
            <Text style={{ color: c.textPrimary, fontSize: 13, marginTop: 6, lineHeight: 18 }}>
              {fatal.message}
            </Text>
            {fatal.remedy ? (
              <Text style={{ color: c.textSecondary, fontSize: 12, marginTop: 6, lineHeight: 17 }}>
                {fatal.remedy}
              </Text>
            ) : null}
            {fixTaskId ? (
              <Text style={{ color: c.success, fontSize: 12, marginTop: 7, lineHeight: 17 }}>
                AI fix task {fixTaskId} started. You can follow it in Tasks, then retry this render.
              </Text>
            ) : null}
            <View style={styles.fatalActions}>
              <Pressable
                onPress={confirmDetach}
                style={({ pressed }) => [styles.fatalAction, { borderColor: c.border }, pressed && styles.pressed]}
              >
                <Text style={{ color: c.textPrimary, fontSize: 12, fontWeight: "600" }}>Production</Text>
              </Pressable>
              <Pressable
                onPress={() => reloadDogfoodSurface("manual")}
                style={({ pressed }) => [styles.fatalAction, { borderColor: c.accent }, pressed && styles.pressed]}
              >
                <Text style={{ color: c.accent, fontSize: 12, fontWeight: "700" }}>Try re-render</Text>
              </Pressable>
              <Pressable
                disabled={fixing}
                onPress={() => {
                  if (fixing) return;
                  setFixing(true);
                  const prompt = `Fix Yaver Dogfood mode in ${params.workDir || "the active Yaver checkout"}. The live browser surface failed with ${fatal.code}: ${fatal.message}. ${fatal.remedy || "Restore the Expo browser lane."} Preserve local work, never force-push, run focused tests, and leave the app ready to re-render.`;
                  void requestDogfoodFixWithAI(deviceId, params.workDir || "", params.runner || "", prompt)
                    .then(({ taskId }) => setFixTaskId(taskId))
                    .catch((err) => setFatal({
                      code: "DOGFOOD_AI_FIX_START_FAILED",
                      message: err instanceof Error ? err.message : String(err),
                      remedy: "Return to Production, reconnect the primary device, and retry the AI fix.",
                    }))
                    .finally(() => setFixing(false));
                }}
                style={({ pressed }) => [styles.fatalAction, { borderColor: c.accent, backgroundColor: c.accentSoft }, pressed && styles.pressed]}
              >
                <Text style={{ color: c.accent, fontSize: 12, fontWeight: "700" }}>
                  {fixing ? "Starting…" : "Fix with AI"}
                </Text>
              </Pressable>
            </View>
          </View>
        </View>
      ) : null}
    </View>
  );
}

const styles = StyleSheet.create({
  root: { flex: 1 },
  surface: { flex: 1 },
  center: { flex: 1, alignItems: "center", justifyContent: "center" },
  pressed: { opacity: 0.62, transform: [{ scale: 0.96 }] },
  quietStatus: {
    position: "absolute",
    bottom: 10,
    alignSelf: "center",
    maxWidth: "78%",
    paddingHorizontal: 10,
    paddingVertical: 6,
    borderRadius: 14,
    borderWidth: StyleSheet.hairlineWidth,
    zIndex: 20,
    elevation: 20,
  },
  loading: {
    ...StyleSheet.absoluteFillObject,
    alignItems: "center",
    justifyContent: "center",
    paddingHorizontal: 24,
  },
  loadingCard: {
    width: "100%",
    maxWidth: 360,
    alignItems: "center",
    borderWidth: 1,
    borderRadius: 18,
    paddingHorizontal: 20,
    paddingVertical: 22,
  },
  loadingTitle: { marginTop: 12, fontSize: 17, fontWeight: "800", textAlign: "center" },
  loadingDetail: { marginTop: 6, fontSize: 12, fontWeight: "600", textAlign: "center" },
  loadingHint: { marginTop: 12, fontSize: 11, lineHeight: 16, textAlign: "center" },
  loadingLog: {
    alignSelf: "stretch",
    maxHeight: 104,
    marginTop: 14,
    borderTopWidth: StyleSheet.hairlineWidth,
  },
  loadingLogContent: { paddingTop: 10, gap: 4 },
  loadingLogLine: { fontSize: 10, lineHeight: 14, fontFamily: Platform.OS === "ios" ? "Menlo" : "monospace" },
  fatalScrim: {
    ...StyleSheet.absoluteFillObject,
    alignItems: "center",
    justifyContent: "center",
    paddingHorizontal: 20,
    backgroundColor: "rgba(0,0,0,0.28)",
    zIndex: 25,
    elevation: 25,
  },
  fatal: {
    width: "100%",
    maxWidth: 420,
    padding: 12,
    borderWidth: 1,
    borderRadius: 12,
  },
  fatalActions: { flexDirection: "row", justifyContent: "flex-end", gap: 8, marginTop: 12 },
  fatalAction: { borderWidth: 1, borderRadius: 9, paddingHorizontal: 12, paddingVertical: 9 },
});

// Web note: RN-web has no react-native-webview. Attach Mode is a phone surface;
// on web the dashboard already renders previews in an iframe. Guarded here so
// the bundle does not explode if this route is ever reached under RN-web.
export const unstable_settings = { initialRouteName: "attach" };
void Platform;
