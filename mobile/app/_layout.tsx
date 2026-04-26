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
  // Wire the native screen-recorder bridge once on first render. Idempotent
  // — vibePreview.ts.setNativeScreenRecorder just stores the latest fn.
  useEffect(() => {
    registerNativeScreenRecorder();
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
