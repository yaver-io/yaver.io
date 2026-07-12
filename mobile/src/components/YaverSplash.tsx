import React, { useEffect, useRef } from "react";
import { Animated, Easing, Image, StyleSheet, Text, View } from "react-native";
import { useColors, useTheme } from "../context/ThemeContext";

// Real app icon (same asset expo uses for the home-screen icon) — shown on
// the cold-start splash instead of a bare "Y" glyph so the mark matches the
// installed app icon.
const APP_ICON = require("../../assets/icon.png");

// YaverSplash — the branded cold-start overlay ("Remote AI Runtime").
//
// Rendered ON TOP of the app the instant the native expo splash hides, so
// there's no flash of an empty shell while DeviceContext / auth hydrate.
// It runs a short intro (wordmark pop + a soft monochrome glow + tagline
// rise), then fades itself out and calls onDone so the host can unmount it.
// Pure Animated API — no extra native deps, no blur/gradient library — so it
// works inside the Hermes container on every surface.
//
// Palette is strictly monochrome — black / gray / white, theme-aware (white
// wordmark on black in dark mode, near-black on white in light mode). No brand
// accent color. Copy is always English.

/** Product tagline — English on every locale. */
const SPLASH_TAGLINE = "Remote AI Runtime";

export interface YaverSplashProps {
  /** Fired after the intro plays and the overlay has faded out. The host
   *  unmounts the splash in this callback. */
  onDone?: () => void;
}

export default function YaverSplash({ onDone }: YaverSplashProps) {
  const c = useColors();
  const { isDark } = useTheme();
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
  const glowOpacity = glow.interpolate({ inputRange: [0.35, 1], outputRange: [0.06, 0.16] });
  const taglineShift = row.interpolate({ inputRange: [0, 1], outputRange: [8, 0] });

  // Strict monochrome palette — the halo and mark tile are neutral gray,
  // the glyph/wordmark are the theme's primary ink (white on black in dark,
  // near-black on white in light). No brand accent.
  const glowColor = isDark ? "#FFFFFF" : "#000000";
  const tileBg = c.surfaceElevated;
  const tileBorder = c.borderStrong;
  const ink = c.textPrimary;

  return (
    <Animated.View
      style={[StyleSheet.absoluteFill, styles.root, { backgroundColor: c.bg, opacity: screen }]}
      pointerEvents="none"
    >
      <View style={styles.center}>
        <Animated.View
          style={[
            styles.glow,
            { backgroundColor: glowColor, opacity: glowOpacity, transform: [{ scale: glowScale }] },
          ]}
        />
        <Animated.View style={{ transform: [{ scale: mark }], alignItems: "center" }}>
          <View style={[styles.markTile, { backgroundColor: tileBg, borderColor: tileBorder }]}>
            <Image source={APP_ICON} style={styles.markImage} resizeMode="cover" />
          </View>
          <Text style={[styles.wordmark, { color: ink }]}>yaver</Text>
        </Animated.View>
        <Animated.Text
          style={[
            styles.tagline,
            { color: c.textMuted, opacity: row, transform: [{ translateY: taglineShift }] },
          ]}
        >
          {SPLASH_TAGLINE.toUpperCase()}
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
    overflow: "hidden",
  },
  markImage: {
    width: "100%",
    height: "100%",
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
