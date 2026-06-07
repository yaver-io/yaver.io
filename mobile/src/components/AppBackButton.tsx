import React from "react";
import { Pressable, StyleProp, StyleSheet, Text, TextStyle, ViewStyle } from "react-native";
import { Ionicons } from "@expo/vector-icons";
import { useColors } from "../context/ThemeContext";

/**
 * AppBackButton — the single back affordance for the whole app.
 *
 * Variants (so every screen can use this one component instead of rolling its
 * own chevron / icon):
 *   - "label"   (default) → "‹ Back" accent text. The canonical style.
 *   - "chevron"           → a lone "‹" (compact headers with a separate title).
 *   - "icon"              → Ionicons chevron-back (icon-only headers).
 *
 * Color defaults to the theme accent; pass `color` to override (e.g. a header
 * over a photo). `label` is ignored by the non-label variants.
 */
export type AppBackButtonVariant = "label" | "chevron" | "icon";

export function AppBackButton({
  onPress,
  label = "Back",
  variant = "label",
  color,
  style,
  textStyle,
}: {
  onPress: () => void;
  label?: string;
  variant?: AppBackButtonVariant;
  color?: string;
  style?: StyleProp<ViewStyle>;
  textStyle?: StyleProp<TextStyle>;
}) {
  const c = useColors();
  const tint = color ?? c.accent;

  return (
    <Pressable onPress={onPress} style={[styles.button, style]} hitSlop={8}>
      {variant === "icon" ? (
        <Ionicons name="chevron-back" size={24} color={tint} />
      ) : variant === "chevron" ? (
        <Text style={[styles.chevron, { color: tint }, textStyle]}>{"‹"}</Text>
      ) : (
        <Text style={[styles.label, { color: tint }, textStyle]}>{"‹"} {label}</Text>
      )}
    </Pressable>
  );
}

const styles = StyleSheet.create({
  button: {
    paddingVertical: 8,
  },
  label: {
    fontSize: 15,
    fontWeight: "600",
  },
  chevron: {
    fontSize: 26,
    fontWeight: "400",
    lineHeight: 28,
  },
});
