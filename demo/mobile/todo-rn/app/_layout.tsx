import { useEffect } from "react";
import { Stack } from "expo-router";
import { StatusBar } from "expo-status-bar";
import { GestureHandlerRootView } from "react-native-gesture-handler";
import { SafeAreaProvider } from "react-native-safe-area-context";
import { YaverFeedback, BlackBox, FeedbackModal } from "yaver-feedback-react-native";

// One-time SDK boot. Zero config — agent URL, auth token, and device
// picker all come from the embedded login + discovery UI in
// <FeedbackModal />. Same call works in both:
//   - YAVER_SDK_MODE: standalone TestFlight/Play install. Shake →
//     modal → user reports a bug straight to their dev box.
//   - YAVER_HOST_MODE: Hermes bundle loaded inside the Yaver
//     container. SDK boots passive; Yaver's own shake overlay owns
//     the gesture and dispatches `yaverFeedback:startReport` when
//     the user taps Feedback. Detection is automatic via the
//     YaverInfo native module.
YaverFeedback.init({ trigger: "shake" });
BlackBox.start();
BlackBox.wrapConsole();

export default function RootLayout() {
  useEffect(() => {
    // Re-init is a no-op if already initialised, but this keeps the
    // SDK alive across Fast Refresh in dev — without it, hot-reloading
    // _layout.tsx can leave the module-level init() above stale.
    YaverFeedback.init({ trigger: "shake" });
  }, []);

  return (
    <GestureHandlerRootView style={{ flex: 1 }}>
      <SafeAreaProvider>
        <StatusBar style="light" />
        <Stack screenOptions={{ headerShown: false }}>
          <Stack.Screen name="index" />
        </Stack>
        <FeedbackModal />
      </SafeAreaProvider>
    </GestureHandlerRootView>
  );
}
