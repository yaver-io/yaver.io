// Crypto + tweetnacl setup. MUST be the first import — everything
// else (DeviceContext → encryptedPair → tweetnacl) depends on the
// PRNG being installed before tweetnacl's import-time IIFE runs.
// See ../src/lib/cryptoSetup.ts for why this is two steps.
import "../src/lib/cryptoSetup";

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

import { Stack } from "expo-router";
import { StatusBar } from "expo-status-bar";
import React, { useEffect } from "react";
import { ScrollView, Text, View } from "react-native";
import { AuthProvider } from "../src/context/AuthContext";
import { DeviceProvider } from "../src/context/DeviceContext";
import { ThemeProvider, useTheme } from "../src/context/ThemeContext";
import { FeedbackOverlay } from "../src/components/FeedbackOverlay";
import { PairLinkHandler } from "../src/lib/pairLinkHandler";
import { registerNativeScreenRecorder } from "../src/lib/screenRecorder";
import { startFeedbackShakeBridge } from "../src/lib/feedbackTrigger";
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
  // Wire the native screen-recorder bridge once on first render. Idempotent
  // — vibePreview.ts.setNativeScreenRecorder just stores the latest fn.
  useEffect(() => {
    registerNativeScreenRecorder();
  }, []);
  useEffect(() => {
    return startFeedbackShakeBridge(user?.id);
  }, [user?.id]);
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
      <PairLinkHandler />
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
