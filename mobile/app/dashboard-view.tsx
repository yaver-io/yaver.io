import React, { useMemo, useState } from "react";
import { ActivityIndicator, Pressable, StyleSheet, Text, View } from "react-native";
import { WebView } from "react-native-webview";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { useLocalSearchParams, useRouter } from "expo-router";
import { useColors } from "../src/context/ThemeContext";
import { quicClient } from "../src/lib/quic";
import { AppBackButton } from "../src/components/AppBackButton";

// Renders the *real* dashboard UI for Convex / Supabase / PocketBase /
// Drizzle / Mailpit by tunnelling `/proxy/{id}/*` through the agent's
// studio proxy (desktop/agent/studio_proxy.go). The agent middleware
// validates the Bearer token on the initial request, then streams HTML
// + JS + CSS + any WebSocket traffic back. No WebView for guest RN
// bundles — just for third-party web dashboards, which is permitted.

const KIND_TO_PROXY: Record<string, { id: string; label: string }> = {
  convex: { id: "convex", label: "Convex Dashboard" },
  supabase: { id: "supabase", label: "Supabase Studio" },
  postgres: { id: "drizzle", label: "Drizzle Studio" },
  sqlite: { id: "drizzle", label: "Drizzle Studio" },
  pocketbase: { id: "pocketbase", label: "PocketBase Admin" },
  mailpit: { id: "mailpit", label: "Mailpit" },
  minio: { id: "minio", label: "MinIO Console" },
};

export default function DashboardViewScreen() {
  const c = useColors();
  const router = useRouter();
  const insets = useSafeAreaInsets();
  const params = useLocalSearchParams<{ dir?: string; kind?: string; id?: string }>();
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const kind = (typeof params.kind === "string" ? params.kind : "convex").toLowerCase();
  const target = KIND_TO_PROXY[kind] ?? KIND_TO_PROXY.convex;
  const id = typeof params.id === "string" ? params.id : target.id;
  const label = target.label;

  // The agent's studio proxy routes /proxy/{id}/* to localhost. We point
  // the WebView at that URL. Auth is injected via the initial request
  // headers — after that the dashboard serves its own relative URLs.
  const url = useMemo(() => `${quicClient.baseUrl}/proxy/${id}/`, [id]);
  const headers = useMemo(() => quicClient.getAuthHeaders(), []);

  return (
    <View style={[styles.container, { backgroundColor: c.bg }]}>
      <View style={[styles.header, { borderBottomColor: c.border, paddingTop: insets.top + 12 }]}>
        <AppBackButton onPress={() => router.back()} />
        <Text style={{ fontSize: 15, fontWeight: "700", color: c.textPrimary }} numberOfLines={1}>
          {label}
        </Text>
        <View style={{ width: 50 }} />
      </View>

      {error && (
        <View style={{ padding: 16, backgroundColor: "#ef4444" + "20", borderBottomWidth: 1, borderBottomColor: c.border }}>
          <Text style={{ color: "#ef4444", fontSize: 12, fontFamily: "Menlo" }}>{error}</Text>
          <Text style={{ color: c.textMuted, fontSize: 11, marginTop: 4 }}>
            Start the dashboard: <Text style={{ fontFamily: "Menlo" }}>yaver services start {id}</Text>
          </Text>
        </View>
      )}

      <View style={{ flex: 1 }}>
        <WebView
          source={{ uri: url, headers }}
          style={{ flex: 1, backgroundColor: c.bg }}
          onLoadStart={() => setLoading(true)}
          onLoadEnd={() => setLoading(false)}
          onError={(e) => setError(e.nativeEvent.description || "Failed to load dashboard")}
          onHttpError={(e) => {
            const code = e.nativeEvent.statusCode;
            if (code >= 400) setError(`HTTP ${code} — is ${id} running on the host?`);
          }}
          javaScriptEnabled
          domStorageEnabled
          thirdPartyCookiesEnabled
          sharedCookiesEnabled
          startInLoadingState
        />
        {loading && (
          <View style={styles.loadingOverlay}>
            <ActivityIndicator size="small" color={c.accent} />
          </View>
        )}
      </View>
    </View>
  );
}

const styles = StyleSheet.create({
  container: { flex: 1 },
  header: {
    flexDirection: "row",
    alignItems: "center",
    justifyContent: "space-between",
    paddingHorizontal: 16,
    paddingBottom: 12,
    borderBottomWidth: 1,
  },
  loadingOverlay: {
    position: "absolute",
    top: 12,
    right: 12,
    padding: 6,
  },
});
