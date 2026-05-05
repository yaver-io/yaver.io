import React, { useEffect, useRef } from "react";
import { Ionicons } from "@expo/vector-icons";
import { Animated, StyleSheet, Text, View } from "react-native";
import { useColors } from "../context/ThemeContext";
import { monoFamily, spacing } from "../theme/tokens";

// Visual primitive for a single agent tool call. Maps to an SF
// Symbol-flavored Ionicons glyph so the list reads as actions, not
// chat. Fades in 200ms on mount — staggered reveal at the call site
// gives the "agent is thinking" rhythm the spec asks for. Keep one
// animation per row and one property (opacity) — composes safely
// under X6 (no more than two row-mounts per frame should ever be
// scheduled together).
export type ToolKind = "shell" | "edit" | "read" | "search" | "build" | "generic";

const ICON_BY_KIND: Record<ToolKind, React.ComponentProps<typeof Ionicons>["name"]> = {
  shell: "terminal-outline",
  edit: "create-outline",
  read: "document-text-outline",
  search: "search-outline",
  build: "hammer-outline",
  generic: "ellipse-outline",
};

export interface ToolCallRowProps {
  kind?: ToolKind;
  /** Verb + object summary — "Reading src/foo.ts", "Running npm install". */
  label: string;
  /** Elapsed seconds since the call started. Renders as "· 4s". */
  elapsedSec?: number;
  /** When the call has finished, dim the row to gray it out. */
  done?: boolean;
}

export function ToolCallRow({ kind = "generic", label, elapsedSec, done }: ToolCallRowProps) {
  const c = useColors();
  const opacity = useRef(new Animated.Value(0)).current;

  useEffect(() => {
    Animated.timing(opacity, {
      toValue: done ? 0.55 : 1,
      duration: 200,
      useNativeDriver: true,
    }).start();
  }, [opacity, done]);

  const iconName = ICON_BY_KIND[kind];

  return (
    <Animated.View style={[styles.row, { opacity }]}>
      <View style={[styles.chip, { backgroundColor: c.surfaceMuted }]}>
        <Ionicons name={iconName} size={13} color={c.textSecondary} style={styles.icon} />
        <Text
          style={[
            styles.label,
            { color: c.textSecondary, fontFamily: monoFamily },
          ]}
          numberOfLines={1}
        >
          {label}
        </Text>
        {typeof elapsedSec === "number" ? (
          <Text style={[styles.elapsed, { color: c.textTertiary }]}>
            · {Math.max(0, Math.round(elapsedSec))}s
          </Text>
        ) : null}
      </View>
    </Animated.View>
  );
}

// Small wrapper for the "X actions taken" rollup once a sequence is
// complete. Same visual chip, italic, no icon.
export function ToolCallSummary({ count }: { count: number }) {
  const c = useColors();
  return (
    <View style={styles.row}>
      <View style={[styles.chip, { backgroundColor: c.surfaceMuted }]}>
        <Text
          style={[
            styles.summary,
            { color: c.textTertiary, fontFamily: monoFamily },
          ]}
        >
          {count} action{count === 1 ? "" : "s"} taken
        </Text>
      </View>
    </View>
  );
}

const styles = StyleSheet.create({
  row: {
    paddingHorizontal: spacing.lg,
    paddingVertical: 2,
    flexDirection: "row",
    justifyContent: "flex-start",
  },
  chip: {
    flexDirection: "row",
    alignItems: "center",
    paddingHorizontal: 10,
    paddingVertical: 5,
    borderRadius: 8,
    maxWidth: "90%",
  },
  icon: { marginRight: 6 },
  label: {
    fontSize: 12,
    flexShrink: 1,
  },
  elapsed: {
    fontSize: 11,
    marginLeft: 6,
  },
  summary: {
    fontSize: 12,
    fontStyle: "italic",
  },
});
