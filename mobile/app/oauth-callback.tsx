import React, { useEffect, useRef } from "react";
import { ActivityIndicator, Text, View } from "react-native";
import { useLocalSearchParams, router } from "expo-router";
import { useAuth } from "../src/context/AuthContext";

// OAuth deep-link landing page.
//
// When the OAuth callback redirects to `yaver://oauth-callback?…`,
// expo-router claims the URL and mounts THIS screen, which unmounts
// whichever screen previously held a `Linking.addEventListener`
// (LoginScreen for sign-in, Settings for link). That used to drop
// the token on the floor for fresh-tablet sign-ins because the
// listener never fired. So this screen now does the work itself:
//
//   • If `?token=…` is present → call login(token) and go to "/".
//   • If `?linked=1&linkedProvider=…` is present → forward to
//     Settings so the existing focus-effect can re-fetch identities.
//
// Either form gets handled here, regardless of whether other
// screens are mounted. This is the canonical handler.

export default function OAuthCallbackScreen() {
  const params = useLocalSearchParams();
  const { login } = useAuth();
  const navigated = useRef(false);

  useEffect(() => {
    if (navigated.current) return;
    navigated.current = true;

    const token = typeof params.token === "string" ? params.token : undefined;
    const linkedProvider = typeof params.linkedProvider === "string" ? params.linkedProvider : undefined;
    const intent = typeof params.intent === "string" ? params.intent : undefined;

    if (token) {
      (async () => {
        try {
          await login(token);
          router.replace("/");
        } catch {
          router.replace("/login");
        }
      })();
      return;
    }

    const target =
      intent === "link" || linkedProvider
        ? { pathname: "/(tabs)/settings", params: { linkedProvider: linkedProvider || "" } }
        : "/(tabs)/settings";

    const timer = setTimeout(() => {
      try {
        router.replace(target as any);
      } catch {
        router.replace("/(tabs)/settings");
      }
    }, 200);
    return () => clearTimeout(timer);
  }, [params, login]);

  return (
    <View style={{ flex: 1, alignItems: "center", justifyContent: "center", backgroundColor: "#000" }}>
      <ActivityIndicator size="small" color="#818cf8" />
      <Text style={{ color: "#9ca3af", marginTop: 12, fontSize: 14 }}>Returning to Yaver…</Text>
    </View>
  );
}
