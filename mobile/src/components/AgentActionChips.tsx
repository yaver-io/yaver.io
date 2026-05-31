/**
 * AgentActionChips — renders the contextual action buttons under an agent /
 * LLM response message (the "Debug" chip pattern from the chat screenshot).
 *
 * It's the visual half of localAgent/interpreter.ts: the parent runs
 * interpretMessage(text, ctx) (deterministic fast-path, or the selected brain
 * for free-form text), then hands the resulting chips here. Tapping a chip
 * calls back to the host, which dispatches through the SAME catalog/disposition
 * gate the voice runtime uses — so a chip can never trigger a BLOCKED action,
 * and CONFIRM chips prompt first. This component is purely presentational.
 */
import React from "react";
import { ActivityIndicator, Pressable, StyleSheet, Text, View } from "react-native";
import { Ionicons } from "@expo/vector-icons";
import { useColors } from "../context/ThemeContext";
import type { ActionChip } from "../lib/localAgent/interpreter";

interface Props {
  /** Optional one-line gloss of what the message means. */
  summary?: string;
  chips: ActionChip[];
  /** Id of a chip currently executing (shows a spinner on it). */
  busyActionId?: string | null;
  /**
   * Tap handler. The host resolves disposition: "auto" runs immediately,
   * "confirm" shows a confirm sheet first. UI chips (logs/debug/context/retry)
   * just open the relevant panel.
   */
  onChip: (chip: ActionChip) => void;
}

function iconFor(chip: ActionChip): keyof typeof Ionicons.glyphMap {
  if (chip.ui === "logs") return "document-text-outline";
  if (chip.ui === "debug") return "bug-outline";
  if (chip.ui === "context") return "layers-outline";
  if (chip.ui === "retry") return "refresh-outline";
  switch (chip.actionId) {
    case "device.recoverAuth":
    case "recovery.reauthStart":
    case "recovery.targetStart":
      return "refresh-circle-outline";
    case "recycle":
      return "reload-outline";
    case "runner.install":
      return "log-in-outline";
    case "runner.switch":
      return "swap-horizontal-outline";
    case "build":
      return "hammer-outline";
    case "test":
      return "checkmark-done-outline";
    case "deploy":
      return "rocket-outline";
    case "status":
      return "pulse-outline";
    case "device.select":
      return "link-outline";
    default:
      return "flash-outline";
  }
}

export default function AgentActionChips({ summary, chips, busyActionId, onChip }: Props) {
  const c = useColors();
  if (!summary && (!chips || chips.length === 0)) return null;

  return (
    <View style={styles.wrap}>
      {summary ? (
        <Text style={[styles.summary, { color: c.textSecondary }]}>{summary}</Text>
      ) : null}
      {chips.length > 0 ? (
        <View style={styles.row}>
          {chips.map((chip) => {
            const busy = busyActionId === chip.actionId;
            const needsConfirm = chip.disposition === "confirm";
            return (
              <Pressable
                key={`${chip.actionId}:${chip.label}`}
                onPress={() => !busy && onChip(chip)}
                disabled={busy}
                style={({ pressed }) => [
                  styles.chip,
                  {
                    backgroundColor: c.bgCard,
                    borderColor: needsConfirm ? c.accent + "66" : c.border,
                  },
                  pressed && { opacity: 0.7 },
                  busy && { opacity: 0.6 },
                ]}
                accessibilityRole="button"
                accessibilityLabel={chip.label}
              >
                {busy ? (
                  <ActivityIndicator size="small" color={c.accent} />
                ) : (
                  <Ionicons name={iconFor(chip)} size={15} color={needsConfirm ? c.accent : c.textSecondary} />
                )}
                <Text style={[styles.chipLabel, { color: c.textPrimary }]}>{chip.label}</Text>
              </Pressable>
            );
          })}
        </View>
      ) : null}
    </View>
  );
}

const styles = StyleSheet.create({
  wrap: { marginTop: 8, marginBottom: 4, gap: 8 },
  summary: { fontSize: 13, lineHeight: 18, paddingHorizontal: 2 },
  row: { flexDirection: "row", flexWrap: "wrap", gap: 8 },
  chip: {
    flexDirection: "row",
    alignItems: "center",
    gap: 6,
    paddingVertical: 9,
    paddingHorizontal: 14,
    borderRadius: 10,
    borderWidth: 1,
    minHeight: 38,
  },
  chipLabel: { fontSize: 14, fontWeight: "600" },
});
