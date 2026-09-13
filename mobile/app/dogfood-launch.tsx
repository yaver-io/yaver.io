import { router } from "expo-router";
import React, { useEffect, useMemo, useRef, useState } from "react";
import { ActivityIndicator, PixelRatio, Platform, Pressable, ScrollView, StyleSheet, Text, useWindowDimensions, View } from "react-native";
import { SafeAreaView } from "react-native-safe-area-context";

import { AppScreenHeader } from "../src/components/AppScreenHeader";
import { useDogfoodOverlay } from "../src/context/DogfoodOverlayContext";
import { useColors } from "../src/context/ThemeContext";
import { useRouteParamsCompat } from "../src/lib/useRouteParamsCompat";
import { mobileSessionSettings } from "../src/lib/appVersion";
import { DogfoodLaunchingWidget, DogfoodLiveConsole } from "../../sdk/feedback/react-native/src/DogfoodSessionUi";
import type { DogfoodLane, DogfoodPhase } from "../../sdk/feedback/react-native/src/DogfoodRuntime";

/**
 * Visible first stage of Dogfood.
 *
 * Launch used to start the root controller and immediately replace this route
 * with Tasks. The browser logs were retained correctly but rendered nowhere,
 * making a slow Expo start and a failed start indistinguishable. This screen
 * stays until the runtime is proven, shows the shared live console, and then
 * offers the real surface. Tasks are a parallel control surface: visiting them
 * never stops or takes ownership from the prepared runtime.
 */
export default function DogfoodLaunchScreen() {
  const c = useColors();
  const runtime = useDogfoodOverlay();
  const window = useWindowDimensions();
  const startedRef = useRef(false);
  const openedRef = useRef(false);
  const [opening, setOpening] = useState(false);
  const [openError, setOpenError] = useState("");
  const [stopping, setStopping] = useState(false);
  const params = useRouteParamsCompat<{
    workDir?: string;
    runner?: string;
    deviceId?: string;
    deviceName?: string;
    lane?: string;
    fallbackLane?: string;
    usageMode?: string;
    startBehavior?: string;
    renderBehavior?: string;
    sessionBehavior?: string;
  }>();
  const requestedLane: DogfoodLane = params.lane === "webrtc" || params.lane === "hermes" ? params.lane : "browser";
  const browserViewport = useMemo(() => {
    const session = mobileSessionSettings();
    const mobile = session.deviceClass === "phone" || session.deviceClass === "tablet" || session.deviceClass === "watch";
    return {
      width: Math.round(window.width),
      height: Math.round(window.height),
      deviceScaleFactor: PixelRatio.get(),
      mobile,
      touch: mobile || Platform.OS === "ios" || Platform.OS === "android",
      surface: session.clientSurface,
    };
  }, [window.height, window.width]);

  useEffect(() => {
    if (startedRef.current) return;
    startedRef.current = true;
    const fallbackLane: DogfoodLane | undefined = params.fallbackLane === "browser" || params.fallbackLane === "webrtc" || params.fallbackLane === "hermes"
      ? params.fallbackLane
      : undefined;
    runtime.begin({
      workDir: String(params.workDir || ""),
      runner: String(params.runner || "codex"),
      deviceId: String(params.deviceId || ""),
      deviceName: String(params.deviceName || "the primary device"),
      lane: requestedLane,
      fallbackLane,
      usageMode: params.usageMode === "chat-only" || params.usageMode === "reload-and-chat" ? params.usageMode : "reload-only",
      startBehavior: params.startBehavior === "render-on-open" ? "render-on-open" : "vibe-first",
      renderBehavior: params.renderBehavior === "auto-on-request" ? "auto-on-request" : "manual",
      sessionBehavior: params.sessionBehavior === "new-session" ? "new-session" : "resume-last",
      browserViewport,
    });
  }, [browserViewport, params, requestedLane, runtime.begin]);

  const snapshot = runtime.snapshot;
  const phase: DogfoodPhase = snapshot?.phase || "preparing";
  const lane = snapshot?.project.lane || requestedLane;
  const branch = typeof snapshot?.result?.metadata?.branch === "string"
    ? snapshot.result.metadata.branch
    : "";
  const sourceLabel = useMemo(() => {
    const box = runtime.request?.deviceName || String(params.deviceName || "the primary device");
    return `${box} · ${branch || "checking branch"}`;
  }, [branch, params.deviceName, runtime.request?.deviceName]);
  const ready = phase === "ready";
  const failed = phase === "failed";

  const stopAndReturn = async () => {
    if (stopping) return;
    setStopping(true);
    try {
      await runtime.end();
    } finally {
      router.replace("/(tabs)/dogfood" as any);
    }
  };

  const openDogfood = async () => {
    if (opening || openedRef.current) return;
    openedRef.current = true;
    setOpening(true);
    setOpenError("");
    try {
      await runtime.open();
    } catch (error) {
      openedRef.current = false;
      setOpenError(error instanceof Error ? error.message : String(error));
    } finally {
      setOpening(false);
    }
  };

  useEffect(() => {
    // render-on-open is already owned by the root controller; vibe-first now
    // auto-hands off here after its visible console reaches ready.
    if (ready && !openError && params.startBehavior !== "render-on-open") void openDogfood();
  }, [ready, openError, params.startBehavior]);

  return (
    <SafeAreaView style={[styles.root, { backgroundColor: c.bg }]} edges={["bottom"]}>
      <AppScreenHeader title="Launch Dogfood" onBack={() => void stopAndReturn()} />
      <ScrollView contentContainerStyle={styles.content}>
        <View style={[styles.summary, { backgroundColor: c.bgCard, borderColor: c.border }]}>
          <Text style={[styles.title, { color: c.textPrimary }]}>
            {ready ? "Dogfood is ready" : failed ? "Dogfood could not start" : "Starting Dogfood"}
          </Text>
          <Text style={[styles.detail, { color: c.textMuted }]}>
            {branch
              ? `Tasks and reloads use ${branch} in the prepared checkout.`
              : "Yaver is checking the checkout, branch, Expo, and the phone route before opening the app."}
          </Text>
        </View>

        <DogfoodLiveConsole
          lane={lane}
          sourceLabel={sourceLabel}
          phase={phase}
          message={snapshot?.message || "Connecting to the selected box…"}
          logs={snapshot?.logs || []}
          failure={snapshot?.failure}
          colors={{
            background: c.bgCard,
            border: c.border,
            text: c.textPrimary,
            muted: c.textMuted,
            accent: c.accent,
            accentSoft: c.accentSoft,
            ready: c.success,
            attention: c.warn,
            blocked: c.error,
            console: c.bgCard,
          }}
        />

        {ready && !openError ? (
          <DogfoodLaunchingWidget
            message="Launching Dogfood…"
            detail="The verified Yaver surface opens automatically."
            colors={{ background: c.bgCard, border: c.border, text: c.textPrimary, muted: c.textMuted, accent: c.accent, accentSoft: c.accentSoft }}
          />
        ) : openError ? (
          <>
            <Text accessibilityRole="alert" style={[styles.openError, { color: c.error }]}>{openError}</Text>
            <Pressable accessibilityRole="button" accessibilityLabel="Retry opening Dogfood" onPress={() => void openDogfood()} style={[styles.primary, { backgroundColor: c.accent }]}>
              <Text style={styles.primaryText}>Retry opening Dogfood</Text>
            </Pressable>
          </>
        ) : failed ? (
          <Pressable
            accessibilityRole="button"
            accessibilityLabel="Retry Dogfood launch"
            onPress={runtime.retry}
            style={({ pressed }) => [styles.primary, { backgroundColor: c.accent }, pressed && styles.pressed]}
          >
            <Text style={styles.primaryText}>Retry launch</Text>
          </Pressable>
        ) : (
          <>
            <View style={styles.working} accessibilityLabel="Dogfood launch is running">
              <ActivityIndicator color={c.accent} />
              <Text style={[styles.workingText, { color: c.textMuted }]}>Keep this open to follow live build logs</Text>
            </View>
            <Pressable
              accessibilityRole="button"
              accessibilityLabel="Stop Dogfood launch"
              disabled={stopping}
              onPress={() => void stopAndReturn()}
              style={({ pressed }) => [styles.secondary, { borderColor: c.error }, (pressed || stopping) && styles.pressed]}
            >
              {stopping ? <ActivityIndicator color={c.error} /> : <Text style={[styles.secondaryText, { color: c.error }]}>Stop Dogfood</Text>}
            </Pressable>
          </>
        )}

      </ScrollView>
    </SafeAreaView>
  );
}

const styles = StyleSheet.create({
  root: { flex: 1 },
  content: { padding: 16, paddingBottom: 40, gap: 12 },
  summary: { borderWidth: 1, borderRadius: 16, padding: 16 },
  title: { fontSize: 19, fontWeight: "800" },
  detail: { marginTop: 5, fontSize: 12, lineHeight: 18 },
  primary: { minHeight: 50, borderRadius: 13, alignItems: "center", justifyContent: "center" },
  primaryText: { color: "#fff", fontSize: 14, fontWeight: "800" },
  secondary: { minHeight: 48, borderRadius: 13, borderWidth: 1, alignItems: "center", justifyContent: "center" },
  secondaryText: { fontSize: 14, fontWeight: "700" },
  working: { minHeight: 48, flexDirection: "row", alignItems: "center", justifyContent: "center", gap: 9 },
  workingText: { fontSize: 12, fontWeight: "600" },
  openError: { fontSize: 12, lineHeight: 18 },
  pressed: { opacity: 0.7 },
});
