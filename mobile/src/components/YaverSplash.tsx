import React, { useEffect, useMemo, useRef } from "react";
import {
  ActivityIndicator,
  Animated,
  Dimensions,
  Easing,
  Image,
  StyleSheet,
  Text,
  View,
} from "react-native";
import Svg, { Defs, LinearGradient, RadialGradient, Rect, Stop } from "react-native-svg";
import { MaterialCommunityIcons } from "@expo/vector-icons";

// Real app icon (the "Y" mark expo uses for the home-screen icon) — shown on
// the cold-start splash so the mark matches the installed app icon.
const APP_ICON = require("../../assets/icon.png");

// YaverSplash — the branded cold-start overlay ("Remote AI Runtime").
//
// An elegant, fixed dark-navy brand moment (monogram-tiled backdrop of the
// things Yaver does: coding, AI, remote, cloud) with a glowing YAVER wordmark
// beside the Y icon, feature pillars, and a soft loader. Rendered ON TOP of the
// app the instant the native splash hides so there's no flash of an empty
// shell while DeviceContext / auth hydrate; it plays a short intro, then fades
// out and calls onDone so the host can unmount it.
//
// Pure Animated + react-native-svg + @expo/vector-icons — no blur/gradient
// native dep — so it works inside the Hermes container on every surface.

/** Product tagline — English on every locale. */
const SPLASH_TAGLINE = "Remote AI Runtime";

// Faint, tiled domain glyphs — the things Yaver actually does: coding, AI,
// remote machines, cloud. Deterministic tiling (no Math.random at runtime).
const TILE_ICONS = [
  "code-braces", "robot", "cloud-outline", "server-network", "console-line",
  "chip", "brain", "cellphone-link", "api", "database-outline", "source-branch",
  "rocket-launch", "cog", "terminal", "access-point-network", "laptop",
  "function-variant", "cube-outline",
] as const;

// The pillars the splash surfaces: coding · AI · remote · cloud.
const PILLARS: { icon: keyof typeof MaterialCommunityIcons.glyphMap; label: string }[] = [
  { icon: "code-braces", label: "Code" },
  { icon: "robot", label: "AI" },
  { icon: "access-point-network", label: "Remote" },
  { icon: "cloud-outline", label: "Cloud" },
];

export interface YaverSplashProps {
  /** Fired after the intro plays and the overlay has faded out. The host
   *  unmounts the splash in this callback. */
  onDone?: () => void;
}

export default function YaverSplash({ onDone }: YaverSplashProps) {
  const { width, height } = Dimensions.get("window");
  const screen = useRef(new Animated.Value(0)).current; // whole-overlay fade
  const fade = useRef(new Animated.Value(0)).current; // center block fade-in
  const rise = useRef(new Animated.Value(14)).current; // center block rise
  const pulse = useRef(new Animated.Value(0)).current; // glow pulse

  useEffect(() => {
    Animated.parallel([
      Animated.timing(screen, {
        toValue: 1,
        duration: 300,
        easing: Easing.out(Easing.quad),
        useNativeDriver: true,
      }),
      Animated.timing(fade, { toValue: 1, duration: 650, useNativeDriver: true }),
      Animated.spring(rise, { toValue: 0, friction: 7, tension: 60, useNativeDriver: true }),
    ]).start();
    const loop = Animated.loop(
      Animated.sequence([
        Animated.timing(pulse, { toValue: 1, duration: 1400, useNativeDriver: true }),
        Animated.timing(pulse, { toValue: 0, duration: 1400, useNativeDriver: true }),
      ]),
    );
    loop.start();

    const timer = setTimeout(() => {
      loop.stop();
      Animated.timing(screen, {
        toValue: 0,
        duration: 400,
        easing: Easing.in(Easing.quad),
        useNativeDriver: true,
      }).start(() => onDone?.());
    }, 1900);

    return () => {
      clearTimeout(timer);
      loop.stop();
    };
  }, [screen, fade, rise, pulse, onDone]);

  // Brick-offset tile grid; deterministic rotation (no Math.random at runtime).
  const TILE = 66;
  const tiles = useMemo(() => {
    const cols = Math.ceil(width / TILE) + 1;
    const rows = Math.ceil(height / TILE) + 1;
    const out: { key: string; x: number; y: number; name: string; rot: number }[] = [];
    let i = 0;
    for (let r = 0; r < rows; r++) {
      for (let c = 0; c < cols; c++) {
        out.push({
          key: `${r}-${c}`,
          x: c * TILE + (r % 2 ? TILE / 2 : 0) - 8,
          y: r * TILE + 6,
          name: TILE_ICONS[i % TILE_ICONS.length],
          rot: ((r * 7 + c * 13) % 7) * 6 - 18,
        });
        i++;
      }
    }
    return out;
  }, [width, height]);

  const glow = pulse.interpolate({ inputRange: [0, 1], outputRange: [0.18, 0.5] });
  const logoScale = pulse.interpolate({ inputRange: [0, 1], outputRange: [1, 1.035] });

  return (
    <Animated.View
      style={[StyleSheet.absoluteFill, styles.root, { opacity: screen }]}
      pointerEvents="none"
    >
      {/* Branded gradient + soft center glow */}
      <Svg style={StyleSheet.absoluteFill} width={width} height={height}>
        <Defs>
          <LinearGradient id="yv-bg" x1="0" y1="0" x2="0.35" y2="1">
            <Stop offset="0" stopColor="#0f1b34" />
            <Stop offset="0.55" stopColor="#152540" />
            <Stop offset="1" stopColor="#070b13" />
          </LinearGradient>
          <RadialGradient id="yv-halo" cx="50%" cy="44%" r="60%">
            <Stop offset="0" stopColor="#3c5e9e" stopOpacity="0.35" />
            <Stop offset="1" stopColor="#3c5e9e" stopOpacity="0" />
          </RadialGradient>
        </Defs>
        <Rect x="0" y="0" width={width} height={height} fill="url(#yv-bg)" />
        <Rect x="0" y="0" width={width} height={height} fill="url(#yv-halo)" />
      </Svg>

      {/* Faint tiled domain glyphs (the monogram) */}
      <View style={StyleSheet.absoluteFill} pointerEvents="none">
        {tiles.map((t) => (
          <MaterialCommunityIcons
            key={t.key}
            name={t.name as keyof typeof MaterialCommunityIcons.glyphMap}
            size={30}
            color="rgba(150,180,222,0.055)"
            style={[styles.tile, { left: t.x, top: t.y, transform: [{ rotate: `${t.rot}deg` }] }]}
          />
        ))}
      </View>

      {/* Center brand block */}
      <Animated.View style={[styles.center, { opacity: fade, transform: [{ translateY: rise }] }]}>
        <Animated.View style={[styles.logoRow, { transform: [{ scale: logoScale }] }]}>
          <View style={styles.markTile}>
            <Image source={APP_ICON} style={styles.markImage} resizeMode="cover" />
          </View>
          <Text style={styles.wordmark}>YAVER</Text>
        </Animated.View>

        <Animated.View style={[styles.glowBar, { opacity: glow }]} />

        <Text style={styles.tagline}>{SPLASH_TAGLINE.toUpperCase()}</Text>

        <View style={styles.pillars}>
          {PILLARS.map((p, i) => (
            <React.Fragment key={p.label}>
              {i > 0 ? <View style={styles.pillarDot} /> : null}
              <View style={styles.pillar}>
                <MaterialCommunityIcons name={p.icon} size={15} color="#9fbcef" />
                <Text style={styles.pillarText}>{p.label}</Text>
              </View>
            </React.Fragment>
          ))}
        </View>

        <View style={styles.loaderRow}>
          <ActivityIndicator color="#9ab6e8" />
          <Text style={styles.loaderText}>Starting Yaver…</Text>
        </View>
      </Animated.View>
    </Animated.View>
  );
}

const styles = StyleSheet.create({
  root: { backgroundColor: "#070b13", alignItems: "center", justifyContent: "center", zIndex: 9999 },
  tile: { position: "absolute" },
  center: { alignItems: "center", paddingHorizontal: 32 },
  logoRow: { flexDirection: "row", alignItems: "center", gap: 14 },
  markTile: {
    width: 44,
    height: 44,
    borderRadius: 12,
    overflow: "hidden",
    borderWidth: 1,
    borderColor: "rgba(150,180,222,0.25)",
  },
  markImage: { width: "100%", height: "100%" },
  wordmark: {
    color: "#f1f5f9",
    fontSize: 44,
    fontWeight: "800",
    letterSpacing: 7,
    textShadowColor: "rgba(120,160,230,0.55)",
    textShadowOffset: { width: 0, height: 0 },
    textShadowRadius: 18,
  },
  glowBar: { marginTop: 14, width: 132, height: 3, borderRadius: 2, backgroundColor: "#5b86c4" },
  tagline: {
    color: "#9fb2cd",
    fontSize: 12,
    fontWeight: "700",
    letterSpacing: 3,
    marginTop: 14,
    textAlign: "center",
  },
  pillars: {
    flexDirection: "row",
    alignItems: "center",
    justifyContent: "center",
    gap: 8,
    marginTop: 16,
    flexWrap: "wrap",
  },
  pillar: { flexDirection: "row", alignItems: "center", gap: 5 },
  pillarText: { color: "#c7d6ef", fontSize: 12, fontWeight: "700", letterSpacing: 0.3 },
  pillarDot: { width: 3, height: 3, borderRadius: 2, backgroundColor: "#3f5a73" },
  loaderRow: { flexDirection: "row", alignItems: "center", gap: 10, marginTop: 28 },
  loaderText: { color: "#aebfd6", fontSize: 13.5, fontWeight: "600" },
});
