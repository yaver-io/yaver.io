// tv-home.tsx — 10-foot, D-pad-driven launcher for the lean-back surfaces that
// make sense on a TV: the Apple TV remote/now-playing card, the capture/stream
// dashboard, Remote Desktop, and device/agent status. Touch-first screens are
// unusable as-is on a TV (focus, not touch — see
// docs/yaver-tv-car-deployment-roadmap.md §1 / §2.1), so this screen is the
// re-laid-out entry point: big focusable tiles, explicit focus styling, and a
// preferred-focus default so the remote lands somewhere on open.
//
// app/index.tsx routes an authenticated TV build here (Platform.isTV) instead of
// the phone tab bar, which is cramped and not focus-friendly on a remote.
import React, { useCallback, useState } from "react";
import { Platform, Pressable, ScrollView, StyleSheet, Text, View } from "react-native";
import { SafeAreaView } from "react-native-safe-area-context";
import { useRouter } from "expo-router";
import { Ionicons } from "@expo/vector-icons";

import { useAuth } from "../src/context/AuthContext";
import { useColors } from "../src/context/ThemeContext";
import type { ThemeColors } from "../src/constants/colors";

type Tile = {
  key: string;
  label: string;
  detail: string;
  icon: keyof typeof Ionicons.glyphMap;
  route: string;
};

// Only lean-back-friendly surfaces. Editing code / tabs / forms are phone+web
// surfaces; a TV remote can't drive them, so they're deliberately omitted.
const TILES: Tile[] = [
  { key: "games", label: "Yaver Games", detail: "SFMG · strategy runtime", icon: "game-controller-outline", route: "/apps?surface=games" },
  { key: "home", label: "Home Control", detail: "Universal remote · activities", icon: "home-outline", route: "/home-control" },
  { key: "appletv", label: "Apple TV", detail: "Remote · now playing", icon: "tv-outline", route: "/appletv-remote" },
  { key: "capture", label: "Capture & Stream", detail: "Capture card · scenes", icon: "videocam-outline", route: "/appletv-remote?surface=glass" },
  { key: "desktop", label: "Remote Desktop", detail: "Watch a box's screen", icon: "desktop-outline", route: "/remote-desktop" },
  { key: "devices", label: "Devices", detail: "Status · reachability", icon: "hardware-chip-outline", route: "/connections" },
  { key: "camera", label: "Phone Camera", detail: "Push a camera source", icon: "camera-outline", route: "/stream-camera" },
  { key: "assistant", label: "Assistant", detail: "Ask Yaver", icon: "chatbubbles-outline", route: "/assistant" },
];

export default function TVHomeScreen() {
  const c = useColors();
  const router = useRouter();
  const { logout } = useAuth();
  const styles = makeStyles(c);

  return (
    <SafeAreaView style={styles.safe}>
      <ScrollView contentContainerStyle={styles.scroll}>
        <View style={styles.header}>
          <Text style={styles.brand}>Yaver</Text>
          <Text style={styles.sub}>Lean-back control for your devices</Text>
        </View>

        <View style={styles.grid}>
          {TILES.map((t, i) => (
            <TVTile
              key={t.key}
              tile={t}
              colors={c}
              preferred={i === 0}
              onPress={() => router.push(t.route as any)}
            />
          ))}
        </View>

        <TVTextButton
          colors={c}
          label="Sign out"
          icon="log-out-outline"
          onPress={async () => {
            await logout();
            router.replace("/tv-signin");
          }}
        />
      </ScrollView>
    </SafeAreaView>
  );
}

function TVTile({
  tile,
  colors,
  preferred,
  onPress,
}: {
  tile: Tile;
  colors: ThemeColors;
  preferred: boolean;
  onPress: () => void;
}) {
  const [focused, setFocused] = useState(false);
  const onFocus = useCallback(() => setFocused(true), []);
  const onBlur = useCallback(() => setFocused(false), []);
  const styles = makeStyles(colors);

  return (
    <Pressable
      // hasTVPreferredFocus lands the remote on the first tile when the screen
      // opens; RN gives D-pad focus traversal for free across focusable views.
      hasTVPreferredFocus={preferred}
      focusable
      onFocus={onFocus}
      onBlur={onBlur}
      onPress={onPress}
      style={[
        styles.tile,
        focused && styles.tileFocused,
        // Web/phone fallback: highlight on hover/press too so the screen is not
        // dead when previewed off-TV.
        Platform.isTV ? null : { opacity: 1 },
      ]}
    >
      <Ionicons name={tile.icon} size={40} color={focused ? colors.textInverse : colors.accent} />
      <Text style={[styles.tileLabel, focused && { color: colors.textInverse }]}>{tile.label}</Text>
      <Text style={[styles.tileDetail, focused && { color: colors.textInverse }]}>{tile.detail}</Text>
    </Pressable>
  );
}

function TVTextButton({
  colors,
  label,
  icon,
  onPress,
}: {
  colors: ThemeColors;
  label: string;
  icon: keyof typeof Ionicons.glyphMap;
  onPress: () => void;
}) {
  const [focused, setFocused] = useState(false);
  const styles = makeStyles(colors);
  return (
    <Pressable
      focusable
      onFocus={() => setFocused(true)}
      onBlur={() => setFocused(false)}
      onPress={onPress}
      style={[styles.textBtn, focused && styles.textBtnFocused]}
    >
      <Ionicons name={icon} size={22} color={focused ? colors.textInverse : colors.textMuted} />
      <Text style={[styles.textBtnLabel, focused && { color: colors.textInverse }]}>{label}</Text>
    </Pressable>
  );
}

function makeStyles(c: ThemeColors) {
  return StyleSheet.create({
    safe: { flex: 1, backgroundColor: c.bg },
    scroll: { padding: 48, paddingTop: 56 },
    header: { marginBottom: 40 },
    brand: { fontSize: 46, fontWeight: "800", letterSpacing: -1, color: c.textPrimary },
    sub: { fontSize: 20, color: c.textSecondary, marginTop: 8 },
    grid: { flexDirection: "row", flexWrap: "wrap", gap: 24 },
    tile: {
      width: 280,
      height: 180,
      borderRadius: 20,
      padding: 24,
      backgroundColor: c.bgCard,
      borderWidth: 2,
      borderColor: c.border,
      justifyContent: "space-between",
    },
    tileFocused: {
      backgroundColor: c.accent,
      borderColor: c.accent,
      // A subtle lift; transform scale reads well at 10 feet.
      transform: [{ scale: 1.06 }],
    },
    tileLabel: { fontSize: 24, fontWeight: "700", color: c.textPrimary, marginTop: 12 },
    tileDetail: { fontSize: 16, color: c.textSecondary },
    textBtn: {
      marginTop: 40,
      flexDirection: "row",
      alignItems: "center",
      gap: 10,
      alignSelf: "flex-start",
      paddingVertical: 14,
      paddingHorizontal: 22,
      borderRadius: 14,
      borderWidth: 2,
      borderColor: c.border,
      backgroundColor: c.bgCard,
    },
    textBtnFocused: { backgroundColor: c.accent, borderColor: c.accent },
    textBtnLabel: { fontSize: 18, fontWeight: "600", color: c.textSecondary },
  });
}
