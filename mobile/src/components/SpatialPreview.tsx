/**
 * SpatialPreview — embeds the /spatial web route inside the mobile app
 * so kivanc (and every user) can see what their glasses / VR / HUD
 * surface looks like without owning the hardware.
 *
 * Renders an inline WebView pointing at https://yaver.io/spatial with
 * the current device's agent URL + a fresh SDK token in the query
 * string. Tap-to-expand pushes it fullscreen.
 *
 * Same route that Quest 3 / Vision Pro / Ray-Ban Web App load — see
 * web/app/spatial/page.tsx.
 */

import React, { useCallback, useEffect, useMemo, useState } from "react";
import { Modal, Pressable, StyleSheet, Text, View } from "react-native";
import { WebView } from "react-native-webview";
import { Ionicons } from "@expo/vector-icons";
import { useColors } from "../context/ThemeContext";
import { quicClient } from "../lib/quic";
import { YaverGlass } from "./YaverGlass";

interface Props {
  /** Override the web base. Defaults to https://yaver.io. */
  webBase?: string;
  /** Inline height. Defaults to 220. Expanded view is always fullscreen. */
  inlineHeight?: number;
}

const DEFAULT_WEB_BASE = "https://yaver.io";

export function SpatialPreview({ webBase = DEFAULT_WEB_BASE, inlineHeight = 220 }: Props): React.JSX.Element {
  const c = useColors();
  const [expanded, setExpanded] = useState(false);

  const url = useMemo(() => {
    const agent = quicClient.baseUrl;
    const headers = quicClient.getAuthHeaders();
    // Authorization header has form "Bearer XYZ" — extract the token.
    const auth = headers["Authorization"] ?? "";
    const token = auth.startsWith("Bearer ") ? auth.slice(7) : auth;
    if (!agent || !token) return null;
    const u = new URL(webBase + "/spatial");
    u.searchParams.set("agent", agent);
    u.searchParams.set("token", token);
    return u.toString();
  }, [webBase]);

  if (!url) {
    return (
      <View style={[styles.empty, { borderColor: c.border, backgroundColor: c.bgCard }]}>
        <Text style={{ color: c.textMuted, fontSize: 12 }}>
          Spatial preview unavailable — sign in + connect to an agent first.
        </Text>
      </View>
    );
  }

  return (
    <YaverGlass tint={c.bgCard} style={{ borderRadius: 12, overflow: "hidden", borderWidth: 1, borderColor: c.border }}>
      <View style={styles.header}>
        <Text style={{ color: c.textMuted, fontSize: 10, fontWeight: "700", textTransform: "uppercase" }}>
          Spatial Preview
        </Text>
        <Pressable onPress={() => setExpanded(true)} style={styles.expandBtn} hitSlop={8}>
          <Ionicons name="expand-outline" size={16} color={c.textMuted} />
        </Pressable>
      </View>
      <View style={[styles.inline, { height: inlineHeight, backgroundColor: "#000" }]}>
        <WebView
          source={{ uri: url }}
          style={{ flex: 1, backgroundColor: "transparent" }}
          originWhitelist={["*"]}
          javaScriptEnabled
          domStorageEnabled
          mediaPlaybackRequiresUserAction={false}
          allowsInlineMediaPlayback
          androidLayerType="hardware"
          incognito={false}
          // Marker so /spatial's surfaceDetect classifies this as
          // "mobile-webview" — different defaults (no Enter VR, smaller
          // panes) than the same URL opened in desktop Chrome.
          applicationNameForUserAgent="Yaver-RN-WebView"
        />
      </View>

      <Modal visible={expanded} animationType="slide" onRequestClose={() => setExpanded(false)}>
        <View style={{ flex: 1, backgroundColor: "#000" }}>
          <View style={[styles.modalHeader, { backgroundColor: c.bgCard, borderColor: c.border }]}>
            <Text style={{ color: c.textPrimary, fontSize: 14, fontWeight: "600" }}>Spatial Preview</Text>
            <Pressable onPress={() => setExpanded(false)} hitSlop={12}>
              <Ionicons name="close" size={22} color={c.textPrimary} />
            </Pressable>
          </View>
          <WebView
            source={{ uri: url }}
            style={{ flex: 1, backgroundColor: "transparent" }}
            originWhitelist={["*"]}
            javaScriptEnabled
            domStorageEnabled
            mediaPlaybackRequiresUserAction={false}
            allowsInlineMediaPlayback
            androidLayerType="hardware"
            applicationNameForUserAgent="Yaver-RN-WebView"
          />
        </View>
      </Modal>
    </YaverGlass>
  );
}

const styles = StyleSheet.create({
  outer: {
    borderWidth: 1,
    borderRadius: 10,
    overflow: "hidden",
  },
  empty: {
    padding: 14,
    borderWidth: 1,
    borderRadius: 10,
    alignItems: "center",
  },
  header: {
    flexDirection: "row",
    alignItems: "center",
    justifyContent: "space-between",
    paddingHorizontal: 10,
    paddingVertical: 8,
  },
  expandBtn: { padding: 4 },
  inline: { width: "100%" },
  modalHeader: {
    flexDirection: "row",
    alignItems: "center",
    justifyContent: "space-between",
    paddingHorizontal: 16,
    paddingVertical: 12,
    borderBottomWidth: 1,
  },
});

export default SpatialPreview;
