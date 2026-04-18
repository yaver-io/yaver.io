import React, { useEffect, useRef } from "react";
import { ActivityIndicator, Text, View } from "react-native";
import { useLocalSearchParams, router } from "expo-router";

// OAuth deep-link landing page.
//
// When the user links a provider from the web UI, the callback
// redirects to `yaver://oauth-callback?linkedProvider=…&linked=1`.
// On iOS + Android the OS hands that to Expo Router, which used to
// show the default "Unmatched Route" screen because no file served
// the path. The listeners inside (tabs)/settings.tsx and login.tsx
// still fire when the URL lands *while* those screens are mounted,
// but a cold launch (Safari → Yaver) routes here first and the
// router kept winning.
//
// So this screen exists purely to catch the deep link, forward the
// success into the Settings tab, and navigate there. It stays on
// screen for a beat with a spinner so the user gets a clean
// "returning to Yaver…" feel instead of a 404-looking page.

export default function OAuthCallbackScreen() {
  const params = useLocalSearchParams();
  const navigated = useRef(false);

  useEffect(() => {
    if (navigated.current) return;
    navigated.current = true;

    const linkedProvider = typeof params.linkedProvider === "string" ? params.linkedProvider : undefined;
    const intent = typeof params.intent === "string" ? params.intent : undefined;

    // Navigate to settings with query params preserved so the
    // Settings screen's focus effect can recognise this as a just-
    // completed link and refresh identities.
    const target =
      intent === "link" || linkedProvider
        ? { pathname: "/(tabs)/settings", params: { linkedProvider: linkedProvider || "" } }
        : "/(tabs)/settings";

    // Small delay so the Linking subscribers in other screens that
    // are already mounted (login, settings) also see the URL event.
    const timer = setTimeout(() => {
      try {
        router.replace(target as any);
      } catch {
        router.replace("/(tabs)/settings");
      }
    }, 200);
    return () => clearTimeout(timer);
  }, [params]);

  return (
    <View style={{ flex: 1, alignItems: "center", justifyContent: "center", backgroundColor: "#000" }}>
      <ActivityIndicator size="small" color="#818cf8" />
      <Text style={{ color: "#9ca3af", marginTop: 12, fontSize: 14 }}>Returning to Yaver…</Text>
    </View>
  );
}
