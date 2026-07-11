// Crypto + tweetnacl setup. MUST be the first import — everything
// else (DeviceContext → encryptedPair → tweetnacl) depends on the
// PRNG being installed before tweetnacl's import-time IIFE runs.
// See ../src/lib/cryptoSetup.ts for why this is two steps.
import "../src/lib/cryptoSetup";

// Runtime polyfills — Hermes lacks the static AbortSignal.timeout()/any()
// helpers, so every `fetch(url, { signal: AbortSignal.timeout(ms) })` threw
// "undefined is not a function" (broke mesh enable + presence probes). Install
// before any network code runs. Side-effect import.
import "../src/lib/polyfills";

// Runtime debug — install global JS error + unhandled-rejection
// handlers so uncaught errors land in the appLog ring buffer AND
// (when a device is connected) get forwarded to that agent's
// BlackBox stream. Pairs with the agent's `debug=true` build flag
// in /dev/build-native — once SFMG/yaver bundle is compiled with
// hermesc -g + sourcemaps, the captured stacks here can be
// symbolicated against the .map sidecar to point at real source
// lines. Side-effect import; install fires at module load.
import { installRuntimeDebugHandlers } from "../src/lib/runtimeDebug";
installRuntimeDebugHandlers();

import { Stack, usePathname, useRouter } from "expo-router";
import { StatusBar } from "expo-status-bar";
import * as SplashScreen from "expo-splash-screen";
import * as ScreenOrientation from "expo-screen-orientation";
import React, { useEffect, useState } from "react";
import { AppState, Dimensions, NativeModules, Platform, ScrollView, Text, View } from "react-native";
import { breakpoints } from "../src/theme/tokens";
import { AuthProvider } from "../src/context/AuthContext";
import { DeviceProvider } from "../src/context/DeviceContext";
import { ThemeProvider, useTheme } from "../src/context/ThemeContext";
import { FeedbackOverlay } from "../src/components/FeedbackOverlay";
import { ShareComposeModal } from "../src/components/ShareComposeModal";
import { DogfoodCaptureHost } from "../src/components/DogfoodCaptureHost";
import { RunningTasksPill } from "../src/components/RunningTasksPill";
import { WatchBridgeHost } from "../src/components/WatchBridgeHost";
import YaverSplash from "../src/components/YaverSplash";
import { AuthPushHost } from "../src/components/AuthPushHost";
import { PairLinkHandler } from "../src/lib/pairLinkHandler";
import { ShareIntentReceiver } from "../src/lib/shareReceiver";
import { registerNativeScreenRecorder } from "../src/lib/screenRecorder";
import { startFeedbackShakeBridge } from "../src/lib/feedbackTrigger";
import { loadDogfoodMode, recordDogfoodRoute } from "../src/lib/dogfoodMode";
import { useAuth } from "../src/context/AuthContext";

class ErrorBoundary extends React.Component<
  { children: React.ReactNode },
  { error: Error | null }
> {
  state = { error: null as Error | null };

  static getDerivedStateFromError(error: Error) {
    return { error };
  }

  render() {
    if (this.state.error) {
      return (
        <View style={{ flex: 1, backgroundColor: "#0a0a0a", justifyContent: "center", padding: 24 }}>
          <Text style={{ color: "#ef4444", fontSize: 18, fontWeight: "700", marginBottom: 12 }}>
            App Error
          </Text>
          <ScrollView style={{ maxHeight: 400 }}>
            <Text style={{ color: "#ffffff", fontSize: 13, fontFamily: "monospace" }}>
              {this.state.error.message}
            </Text>
            <Text style={{ color: "#888888", fontSize: 11, fontFamily: "monospace", marginTop: 8 }}>
              {this.state.error.stack}
            </Text>
          </ScrollView>
        </View>
      );
    }
    return this.props.children;
  }
}

function InnerLayout() {
  const { isDark, colors } = useTheme();
  const { user } = useAuth();
  const router = useRouter();
  const pathname = usePathname();
  // Branded cold-start overlay ("Remote Runtime AI"). Shows on top of the
  // app the moment the native splash hides, then fades itself out via
  // onDone. One-shot per app launch.
  const [showSplash, setShowSplash] = useState(true);
  useEffect(() => {
    void SplashScreen.hideAsync().catch(() => {});
  }, []);
  // Wire the native screen-recorder bridge once on first render. Idempotent
  // — vibePreview.ts.setNativeScreenRecorder just stores the latest fn.
  useEffect(() => {
    registerNativeScreenRecorder();
  }, []);
  useEffect(() => {
    return startFeedbackShakeBridge(user?.id);
  }, [user?.id]);
  useEffect(() => {
    if (Platform.OS !== "ios") return;
    let mounted = true;
    const consume = async () => {
      try {
        const pending = await (NativeModules as any)?.YaverInfo?.consumePendingCarVoiceLaunch?.();
        if (!mounted || !pending) return;
        router.navigate({
          pathname: "/car-voice-coding",
          params: { autostart: "1", surface: "ios-car" },
        } as any);
      } catch {
        // Optional native bridge; no-op on builds without the method.
      }
    };
    void consume();
    const sub = AppState.addEventListener("change", (state) => {
      if (state === "active") void consume();
    });
    return () => {
      mounted = false;
      sub.remove();
    };
  }, [router]);
  // Dogfood mode: re-arm the sticky toggle on sign-in (per-user flag). When on,
  // this starts the native screenshot auto-catch + breadcrumb recorder.
  useEffect(() => {
    void loadDogfoodMode(user?.id);
  }, [user?.id]);
  // Feed the active route into dogfood breadcrumbs + the native capture payload
  // so a caught screenshot knows which screen it's on. No-op when dogfood is off.
  useEffect(() => {
    recordDogfoodRoute(pathname);
  }, [pathname]);
  // Orientation policy: phones stay portrait (system rotation lock
  // is unreliable across Android OEMs, so enforce in-app); tablets
  // run free so split-pane layouts can use landscape. Decision is
  // made by short-edge dp at boot — a foldable will reach this
  // hook again on configuration change because react-native
  // remounts layout on size class shifts when the OS reports it.
  useEffect(() => {
    const { width, height } = Dimensions.get("window");
    const isTablet = Math.min(width, height) >= breakpoints.tablet;
    (async () => {
      try {
        if (isTablet) {
          await ScreenOrientation.unlockAsync();
        } else {
          await ScreenOrientation.lockAsync(ScreenOrientation.OrientationLock.PORTRAIT_UP);
        }
      } catch {
        // Some Android OEMs / iPad multitasking modes reject lock
        // requests; falling back to manifest defaults is fine.
      }
    })();
  }, []);
  return (
    <>
      <StatusBar style={isDark ? "light" : "dark"} />
      <Stack
        screenOptions={{
          headerShown: false,
          contentStyle: { backgroundColor: colors.bg },
          animation: "fade",
        }}
      />
      <FeedbackOverlay />
      <RunningTasksPill />
      <PairLinkHandler />
      <ShareIntentReceiver />
      <ShareComposeModal />
      <DogfoodCaptureHost />
      <WatchBridgeHost />
      <AuthPushHost />
      {showSplash ? <YaverSplash onDone={() => setShowSplash(false)} /> : null}
    </>
  );
}

export default function RootLayout() {
  return (
    <ErrorBoundary>
      <ThemeProvider>
        <AuthProvider>
          <DeviceProvider>
            <InnerLayout />
          </DeviceProvider>
        </AuthProvider>
      </ThemeProvider>
    </ErrorBoundary>
  );
}
