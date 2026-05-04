import React from "react";
import { StyleSheet, Text, View } from "react-native";
import { useColors } from "../context/ThemeContext";
import { typography } from "../theme/tokens";

type BadgeVariant = "tech" | "live" | "ready" | "brand" | "warning" | "error";

export function Badge({
  label,
  variant,
  uppercase = false,
}: {
  label: string;
  variant: BadgeVariant;
  uppercase?: boolean;
}) {
  const c = useColors();
  const palette =
    variant === "live"
      ? { bg: c.successBg, fg: c.success, border: "transparent" }
      : variant === "ready"
        ? { bg: c.infoBg, fg: c.info, border: "transparent" }
        : variant === "brand"
          ? { bg: c.brandPrimarySoft, fg: c.brandPrimary, border: c.brandPrimary }
          : variant === "error"
            ? { bg: c.errorBg, fg: c.error, border: "transparent" }
          : variant === "warning"
            ? { bg: c.warnBg, fg: c.warn, border: "transparent" }
            : { bg: c.neutralBg, fg: c.textSecondary, border: "transparent" };

  return (
    <View style={[styles.badge, { backgroundColor: palette.bg, borderColor: palette.border, borderWidth: variant === "brand" ? 1 : 0 }]}>
      <Text style={[styles.text, { color: palette.fg }]}>
        {uppercase ? label.toUpperCase() : label}
      </Text>
    </View>
  );
}

const styles = StyleSheet.create({
  badge: {
    paddingHorizontal: 8,
    paddingVertical: 4,
    borderRadius: 6,
    alignSelf: "flex-start",
  },
  text: {
    ...typography.badge,
  },
});
