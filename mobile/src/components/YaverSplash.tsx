import React, { useEffect, useRef } from "react";
import { Animated, Easing, Platform, StyleSheet, Text, View } from "react-native";
import { getLocales } from "expo-localization";
import { useColors } from "../context/ThemeContext";

// YaverSplash — the branded cold-start overlay ("Remote Runtime AI").
//
// Rendered ON TOP of the app the instant the native expo splash hides, so
// there's no flash of an empty shell while DeviceContext / auth hydrate.
// It runs a short intro (wordmark pop + a pulsing brand-purple glow +
// tagline rise), then fades itself out and calls onDone so the host can
// unmount it. Pure Animated API — no extra native deps, no blur/gradient
// library — so it works inside the Hermes container on every surface.
//
// The tagline is locale-aware: Turkish on tr-locale devices, English
// everywhere else (the user asked for "both / locale-aware").

/** Localized product tagline — Turkish for tr-locale devices, English otherwise. */
function splashTagline(): string {
  try {
    const locales = getLocales?.() ?? [];
    const code = (locales[0]?.languageCode ?? "").toLowerCase();
    if (code === "tr") return "Uzaktan Çalışan Yapay Zeka";
  } catch {
    // getLocales can throw on a stripped Hermes build — fall through to English.
  }
  return "Remote Runtime AI";
}

export interface YaverSplashProps {
  /** Fired after the intro plays and the overlay has faded out. The host
   *  unmounts the splash in this callback. */
  onDone?: () => void;
}

export default function YaverSplash({ onDone }: YaverSplashProps) {
  const c = useColors();
  const screen = useRef(new Animated.Value(0)).current; // whole-overlay fade
  const mark = useRef(new Animated.Value(0.82)).current; // wordmark pop-in
  const glow = useRef(new Animated.Value(0.35)).current; // pulsing ring
  const row = useRef(new Animated.Value(0)).current; // tagline rise

  useEffect(() => {
    const intro = Animated.parallel([
      Animated.timing(screen, {
        toValue: 1,
        duration: 300,
        easing: Easing.out(Easing.quad),
        useNativeDriver: true,
      }),
      Animated.spring(mark, {
        toValue: 1,
        friction: 7,
        tension: 60,
        useNativeDriver: true,
      }),
      Animated.timing(row, {
        toValue: 1,
        duration: 520,
        delay: 200,
        easing: Easing.out(Easing.cubic),
        useNativeDriver: true,
      }),
    ]);
    const pulse = Animated.loop(
      Animated.sequence([
        Animated.timing(glow, {
          toValue: 1,
          duration: 900,
          easing: Easing.inOut(Easing.quad),
          useNativeDriver: true,
        }),
        Animated.timing(glow, {
          toValue: 0.35,
          duration: 900,
          easing: Easing.inOut(Easing.quad),
          useNativeDriver: true,
        }),
      ]),
    );
    intro.start();
    pulse.start();

    const timer = setTimeout(() => {
      pulse.stop();
      Animated.timing(screen, {
        toValue: 0,
        duration: 380,
        easing: Easing.in(Easing.quad),
        useNativeDriver: true,
      }).start(() => onDone?.());
    }, 1650);

    return () => {
      clearTimeout(timer);
      pulse.stop();
    };
  }, [screen, mark, glow, row, onDone]);

  const glowScale = glow.interpolate({ inputRange: [0.35, 1], outputRange: [0.9, 1.28] });
  const glowOpacity = glow.interpolate({ inputRange: [0.35, 1], outputRange: [0.1, 0.34] });
  const taglineShift = row.interpolate({ inputRange: [0, 1], outputRange: [8, 0] });

  return (
    <Animated.View
      style={[StyleSheet.absoluteFill, styles.root, { backgroundColor: c.bg, opacity: screen }]}
      pointerEvents="none"
    >
      <View style={styles.center}>
        <Animated.View
          style={[
            styles.glow,
            { backgroundColor: c.accent, opacity: glowOpacity, transform: [{ scale: glowScale }] },
          ]}
        />
        <Animated.View style={{ transform: [{ scale: mark }], alignItems: "center" }}>
          <View style={[styles.markTile, { backgroundColor: c.accentSoft, borderColor: c.accent }]}>
            <Text style={[styles.markGlyph, { color: c.accent }]}>Y</Text>
          </View>
          <Text style={[styles.wordmark, { color: c.textPrimary }]}>yaver</Text>
        </Animated.View>
        <Animated.Text
          style={[
            styles.tagline,
            { color: c.textMuted, opacity: row, transform: [{ translateY: taglineShift }] },
          ]}
        >
          {splashTagline().toUpperCase()}
        </Animated.Text>
      </View>
    </Animated.View>
  );
}

const styles = StyleSheet.create({
  root: { alignItems: "center", justifyContent: "center", zIndex: 9999 },
  center: { alignItems: "center", justifyContent: "center" },
  glow: {
    position: "absolute",
    width: 240,
    height: 240,
    borderRadius: 120,
    top: -60,
  },
  markTile: {
    width: 78,
    height: 78,
    borderRadius: 20,
    borderWidth: 1,
    alignItems: "center",
    justifyContent: "center",
    marginBottom: 18,
  },
  markGlyph: {
    fontSize: 46,
    fontWeight: "800",
    fontFamily: Platform.OS === "ios" ? undefined : undefined,
    includeFontPadding: false,
  },
  wordmark: {
    fontSize: 34,
    fontWeight: "800",
    letterSpacing: 0.5,
  },
  tagline: {
    marginTop: 16,
    fontSize: 12,
    fontWeight: "700",
    letterSpacing: 3,
    textAlign: "center",
  },
});
