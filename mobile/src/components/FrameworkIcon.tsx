// Branded framework icons rendered via @expo/vector-icons. Replaces the
// emoji glyph map in mobile/app/(tabs)/{hotreload,apps}.tsx — emoji
// rendered fine for color but readers couldn't tell at a glance whether
// a row was "Apple Swift" or just "an apple", and Kotlin was a literal
// purple square. Vector icons + brand colors give an unambiguous read.
//
// Color palette is the authoritative brand-guideline color for each
// framework, NOT the closest react-native-friendly approximation. The
// logos render well on the dark Yaver card background at the existing
// 20pt size; Apple's tinted SF-Symbol variant is dropped on purpose
// (looks washed out on dark cards).
import React from "react";
import { MaterialCommunityIcons } from "@expo/vector-icons";

type FrameworkID =
  | "expo"
  | "react-native"
  | "react"
  | "flutter"
  | "swift"
  | "kotlin"
  | "nextjs"
  | "vite";

interface IconSpec {
  name: keyof typeof MaterialCommunityIcons.glyphMap;
  color: string;
}

const FRAMEWORK_ICON_SPECS: Record<string, IconSpec> = {
  expo: { name: "react", color: "#A78BFA" },               // Expo's purple-tinted React identity
  "react-native": { name: "react", color: "#61DAFB" },     // React-cyan
  react: { name: "react", color: "#61DAFB" },
  flutter: { name: "flutter", color: "#54C5F8" },          // Flutter sky-blue
  swift: { name: "language-swift", color: "#FA7343" },     // Swift orange
  kotlin: { name: "language-kotlin", color: "#7F52FF" },   // Kotlin purple
  nextjs: { name: "vercel", color: "#FAFAFA" },            // Vercel triangle reads as Next.js
  vite: { name: "lightning-bolt", color: "#FFC107" },      // Vite lightning yellow
};

interface Props {
  framework: string | null | undefined;
  size?: number;
  /** Override color (e.g. when the row is disabled and should be muted). */
  color?: string;
}

export function FrameworkIcon({ framework, size = 22, color }: Props) {
  const id = String(framework || "").trim().toLowerCase();
  const spec = FRAMEWORK_ICON_SPECS[id];
  if (!spec) {
    // Fallback for unknown frameworks — neutral grey play-arrow keeps the
    // row alignment intact instead of leaving a blank slot.
    return (
      <MaterialCommunityIcons
        name="play-circle-outline"
        size={size}
        color={color ?? "#94a3b8"}
      />
    );
  }
  return (
    <MaterialCommunityIcons
      name={spec.name}
      size={size}
      color={color ?? spec.color}
    />
  );
}

/** Detects whether a framework string would render with our branded icon
 *  (vs the neutral fallback). Useful for feature gates that only want to
 *  surface the icon when it's recognisable. */
export function isKnownFramework(framework: string | null | undefined): framework is FrameworkID {
  const id = String(framework || "").trim().toLowerCase();
  return id in FRAMEWORK_ICON_SPECS;
}
