import React from "react";
import { ActivityIndicator, Pressable, StyleSheet, Text, View } from "react-native";
import { Ionicons } from "@expo/vector-icons";
import { useColors } from "../context/ThemeContext";

// EmptyState — the one empty state for every list surface.
//
// Deliberately CHROMELESS: no card, no border, no elevated background.
// Projects / Reload / Files each used to hand-roll a bordered card, so an
// empty screen stacked three frames — the tab banner, the card, and the
// filled button — to deliver a single sentence. On a black phone screen that
// reads as clutter, not hierarchy. An empty region needs no frame: there is
// nothing to separate it FROM. Type + space carry it.
//
// One primary action, at most one quiet link. If a surface wants a second
// button, that's a signal the state itself is ambiguous — fix the state.

export interface EmptyStateAction {
  label: string;
  onPress: () => void;
  /** Renders a spinner + disables the press while work is in flight. */
  busy?: boolean;
}

export interface EmptyStateProps {
  icon?: keyof typeof Ionicons.glyphMap;
  title: string;
  body?: string;
  /** Filled pill. The single obvious next move. */
  action?: EmptyStateAction;
  /** Text link under the pill. For escape hatches (diagnostics), never a
   *  second primary. */
  link?: EmptyStateAction;
  /** Swaps the icon for a spinner — work already running, no action needed. */
  busy?: boolean;
}

export default function EmptyState({ icon, title, body, action, link, busy }: EmptyStateProps) {
  const c = useColors();

  return (
    <View style={styles.wrap}>
      <View style={styles.glyph}>
        {busy ? (
          <ActivityIndicator size="small" color={c.textMuted} />
        ) : icon ? (
          <Ionicons name={icon} size={30} color={c.textMuted} />
        ) : null}
      </View>

      <Text style={[styles.title, { color: c.textPrimary }]}>{title}</Text>

      {body ? <Text style={[styles.body, { color: c.textSecondary }]}>{body}</Text> : null}

      {action ? (
        <Pressable
          onPress={action.onPress}
          disabled={action.busy}
          style={({ pressed }) => [
            styles.action,
            {
              backgroundColor: action.busy ? c.bgInput : c.accent,
              opacity: pressed ? 0.85 : 1,
            },
          ]}
        >
          <Text style={[styles.actionText, { color: action.busy ? c.textSecondary : c.textInverse }]}>
            {action.label}
          </Text>
        </Pressable>
      ) : null}

      {link ? (
        <Pressable onPress={link.onPress} disabled={link.busy} hitSlop={8} style={styles.link}>
          <Text style={[styles.linkText, { color: c.accent }]}>{link.label}</Text>
        </Pressable>
      ) : null}
    </View>
  );
}

const styles = StyleSheet.create({
  // Sits high in the viewport rather than dead-centre: on a tall phone a
  // vertically-centred empty state floats in a void with the tab bar miles
  // below it. ~15% down keeps it in the reading path.
  wrap: {
    alignItems: "center",
    paddingTop: 72,
    paddingHorizontal: 32,
    paddingBottom: 40,
  },
  glyph: {
    height: 30,
    justifyContent: "center",
    marginBottom: 16,
    opacity: 0.75,
  },
  title: {
    fontSize: 17,
    fontWeight: "600",
    letterSpacing: -0.2,
    textAlign: "center",
  },
  body: {
    fontSize: 13,
    lineHeight: 19,
    textAlign: "center",
    marginTop: 6,
    maxWidth: 260,
  },
  action: {
    marginTop: 22,
    height: 42,
    paddingHorizontal: 22,
    borderRadius: 21,
    alignItems: "center",
    justifyContent: "center",
  },
  actionText: {
    fontSize: 14,
    fontWeight: "600",
  },
  link: {
    marginTop: 14,
    paddingVertical: 4,
  },
  linkText: {
    fontSize: 13,
    fontWeight: "600",
  },
});
