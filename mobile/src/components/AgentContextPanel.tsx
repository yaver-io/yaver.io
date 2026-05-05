import React, { useEffect, useState } from "react";
import { Ionicons } from "@expo/vector-icons";
import { Pressable, StyleSheet, Text, View } from "react-native";
import { useColors } from "../context/ThemeContext";
import { monoFamily, spacing } from "../theme/tokens";

// Background context the user (and a YC partner testing the app)
// wants visible when wondering what the agent is "actually doing".
// Collapsed by default; auto-expanded on failure (drive externally
// via `defaultExpanded`). Source data flows in from the screen — the
// panel itself just renders.
export interface AgentContextRow {
  /** Short label, e.g. "Working dir". */
  label: string;
  /** Value to show. Render mono if it looks like a path/branch. */
  value: string;
  /** When true, formats with mono font. Auto-on for path-shaped values. */
  mono?: boolean;
}

export interface AgentContextPanelProps {
  rows: AgentContextRow[];
  /** Force-open initially. Drive this externally (auto-open on FAILED). */
  defaultExpanded?: boolean;
  /** Custom title — defaults to "Agent context". */
  title?: string;
}

export function AgentContextPanel({
  rows,
  defaultExpanded = false,
  title = "Agent context",
}: AgentContextPanelProps) {
  const c = useColors();
  const [expanded, setExpanded] = useState(defaultExpanded);

  // Re-sync when the parent toggles default state (e.g., task moves
  // from RUNNING to FAILED, parent flips defaultExpanded). Without
  // this, the panel stays collapsed on a status flip because
  // initial state is captured once.
  useEffect(() => {
    setExpanded(defaultExpanded);
  }, [defaultExpanded]);

  if (rows.length === 0) return null;

  return (
    <View style={[styles.container, { borderColor: c.border }]}>
      <Pressable
        onPress={() => setExpanded((v) => !v)}
        style={({ pressed }) => [
          styles.toggle,
          { backgroundColor: c.surface },
          pressed && { opacity: 0.7 },
        ]}
        accessibilityRole="button"
        accessibilityLabel={expanded ? "Hide agent context" : "Show agent context"}
        accessibilityState={{ expanded }}
      >
        <Ionicons
          name={expanded ? "chevron-down" : "chevron-forward"}
          size={14}
          color={c.textSecondary}
          style={styles.chevron}
        />
        <Text style={[styles.toggleText, { color: c.textSecondary }]}>{title}</Text>
        {!expanded && rows.length > 0 ? (
          <Text style={[styles.toggleHint, { color: c.textTertiary }]} numberOfLines={1}>
            · {rows.length} item{rows.length === 1 ? "" : "s"}
          </Text>
        ) : null}
      </Pressable>
      {expanded ? (
        <View style={[styles.body, { backgroundColor: c.surface, borderTopColor: c.border }]}>
          {rows.map((row, i) => (
            <View key={`${row.label}-${i}`} style={styles.line}>
              <Text style={[styles.lineLabel, { color: c.textTertiary }]}>{row.label}</Text>
              <Text
                style={[
                  styles.lineValue,
                  {
                    color: c.textPrimary,
                    fontFamily: row.mono === false ? undefined : monoFamily,
                  },
                ]}
                numberOfLines={1}
              >
                {row.value}
              </Text>
            </View>
          ))}
        </View>
      ) : null}
    </View>
  );
}

const styles = StyleSheet.create({
  container: {
    marginHorizontal: spacing.lg,
    marginVertical: spacing.sm,
    borderWidth: 1,
    borderRadius: 10,
    overflow: "hidden",
  },
  toggle: {
    flexDirection: "row",
    alignItems: "center",
    paddingHorizontal: 12,
    paddingVertical: 8,
  },
  chevron: { marginRight: 6 },
  toggleText: {
    fontSize: 12,
    fontWeight: "600",
    letterSpacing: 0.2,
  },
  toggleHint: {
    fontSize: 11,
    fontWeight: "500",
    marginLeft: 4,
  },
  body: {
    borderTopWidth: 1,
    paddingHorizontal: 12,
    paddingVertical: 8,
  },
  line: {
    flexDirection: "row",
    justifyContent: "space-between",
    alignItems: "center",
    paddingVertical: 3,
    gap: 12,
  },
  lineLabel: {
    fontSize: 11,
    fontWeight: "600",
    letterSpacing: 0.3,
    textTransform: "uppercase",
    flexShrink: 0,
  },
  lineValue: {
    fontSize: 12,
    flex: 1,
    textAlign: "right",
  },
});
