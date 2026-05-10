import React, { useEffect, useRef, useState } from "react";
import { ActivityIndicator, Pressable, Text, View } from "react-native";
import { useLocalSearchParams, router } from "expo-router";
import * as Linking from "expo-linking";
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
//
// Failure handling: when `login(token)` throws (network error,
// invalid token, missing token in deep link), we render the
// reason inline and offer a "Back to login" button instead of
// silently bouncing back. The silent path made it impossible to
// tell whether OAuth had actually succeeded — every Android-side
// failure looked identical to the user (and to the developer).

export default function OAuthCallbackScreen() {
  const params = useLocalSearchParams();
  const { login } = useAuth();
  const navigated = useRef(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (navigated.current) return;
    navigated.current = true;

    (async () => {
      let token = typeof params.token === "string" ? params.token : undefined;
      const linkedProvider =
        typeof params.linkedProvider === "string" ? params.linkedProvider : undefined;
      const intent = typeof params.intent === "string" ? params.intent : undefined;

      // Android fallback: when expo-router cold-mounts this screen via
      // a deep link, useLocalSearchParams sometimes lags by a tick.
      // Re-derive from the initial URL so we don't drop a valid token
      // on the floor.
      if (!token && !linkedProvider) {
        try {
          const initial = await Linking.getInitialURL();
          if (initial) {
            const parsed = Linking.parse(initial);
            const t = parsed.queryParams?.token;
            if (typeof t === "string" && t.length > 0) {
              token = t;
            }
          }
        } catch {
          // ignore — we'll fall through to the no-token error path
        }
      }

      if (token) {
        try {
          await login(token);
          router.replace("/");
        } catch (e: unknown) {
          const msg = e instanceof Error ? e.message : String(e);
          setError(msg || "Sign-in failed for an unknown reason.");
        }
        return;
      }

      if (intent === "link" || linkedProvider) {
        const target = {
          pathname: "/(tabs)/settings",
          params: { linkedProvider: linkedProvider || "" },
        };
        setTimeout(() => {
          try {
            router.replace(target as any);
          } catch {
            router.replace("/(tabs)/settings");
          }
        }, 200);
        return;
      }

      setError("OAuth callback opened without a token in the URL. The browser may have stripped the query string.");
    })();
  }, [params, login]);

  if (error) {
    return (
      <View
        style={{
          flex: 1,
          alignItems: "center",
          justifyContent: "center",
          backgroundColor: "#000",
          paddingHorizontal: 24,
        }}
      >
        <Text style={{ color: "#f87171", fontSize: 16, fontWeight: "600", marginBottom: 12 }}>
          Sign-in failed
        </Text>
        <Text
          style={{ color: "#d1d5db", fontSize: 14, textAlign: "center", marginBottom: 24 }}
          selectable
        >
          {error}
        </Text>
        <Pressable
          onPress={() => router.replace("/login")}
          style={{
            backgroundColor: "#1f2937",
            paddingHorizontal: 20,
            paddingVertical: 10,
            borderRadius: 8,
            borderColor: "#374151",
            borderWidth: 1,
          }}
        >
          <Text style={{ color: "#e5e7eb", fontSize: 14 }}>Back to login</Text>
        </Pressable>
      </View>
    );
  }

  return (
    <View style={{ flex: 1, alignItems: "center", justifyContent: "center", backgroundColor: "#000" }}>
      <ActivityIndicator size="small" color="#818cf8" />
      <Text style={{ color: "#9ca3af", marginTop: 12, fontSize: 14 }}>Returning to Yaver…</Text>
    </View>
  );
}
