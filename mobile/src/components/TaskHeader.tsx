import React, { useEffect, useRef } from "react";
import { Ionicons } from "@expo/vector-icons";
import { Animated, Pressable, StyleSheet, Text, View } from "react-native";
import { useColors } from "../context/ThemeContext";
import { spacing } from "../theme/tokens";

// Two-line task header following the polish pass:
//   Row 1 — Back  ·  empty title slot  ·  primary action (Stop / Retry / —)
//   Row 2 — ● status · device          ·  Logs link
//
// The legacy header stacked title + status + device + Logs over
// three rows and duplicated the first user message in the title.
// Keeping the title slot empty (the user's first command becomes
// the chat bubble) is intentional and one of the spec's main calls.
export type TaskHeaderStatus = "queued" | "running" | "completed" | "failed" | "stopped";
export type PrimaryAction = "stop" | "retry" | "detach" | "none";

export interface TaskHeaderProps {
  status: TaskHeaderStatus;
  /** Device alias / hostname rendered next to the status dot. */
  deviceName?: string;
  /** Runner display name (e.g. "Codex"). Rendered as a chip on the
   *  third line so the user can see at-a-glance which agent is running
   *  the task without expanding Agent context. */
  runnerLabel?: string;
  /** Model display name (e.g. "GPT-5.4"). Paired with runnerLabel in
   *  the same chip. Renders only when runnerLabel is also present. */
  modelLabel?: string;
  /** Tap "Logs" — already wired in tasks.tsx. */
  onOpenLogs?: () => void;
  onBack: () => void;
  primaryAction: PrimaryAction;
  onStop?: () => void;
  /** Long-press on Stop to force-kill (parity with current behavior). */
  onForceKill?: () => void;
  onRetry?: () => void;
  onDetach?: () => void;
}

export function TaskHeader({
  status,
  deviceName,
  runnerLabel,
  modelLabel,
  onOpenLogs,
  onBack,
  primaryAction,
  onStop,
  onForceKill,
  onRetry,
  onDetach,
}: TaskHeaderProps) {
  const c = useColors();
  const palette = statusPalette(c, status);

  // Pulsing dot for in-flight statuses. Single property (opacity),
  // single element — under X6 budget.
  const pulse = useRef(new Animated.Value(1)).current;
  useEffect(() => {
    if (status !== "running" && status !== "queued") {
      pulse.setValue(1);
      return;
    }
    const loop = Animated.loop(
      Animated.sequence([
        Animated.timing(pulse, { toValue: 0.45, duration: 700, useNativeDriver: true }),
        Animated.timing(pulse, { toValue: 1, duration: 700, useNativeDriver: true }),
      ]),
    );
    loop.start();
    return () => loop.stop();
  }, [pulse, status]);

  return (
    <View style={[styles.wrap, { borderBottomColor: c.border }]}>
      {/* Row 1: Back · (title slot empty) · primary action */}
      <View style={styles.row}>
        <Pressable
          style={({ pressed }) => [
            styles.backBtn,
            { backgroundColor: c.brandPrimary + "15" },
            pressed && { opacity: 0.6 },
          ]}
          onPress={onBack}
          accessibilityRole="button"
          accessibilityLabel="Back to tasks list"
        >
          <Text style={[styles.backChevron, { color: c.brandPrimary }]}>{"‹"}</Text>
          <Text style={[styles.backText, { color: c.brandPrimary }]}>Back</Text>
        </Pressable>

        <View style={styles.titleSlot} />

        {primaryAction === "stop" && onStop ? (
          <Pressable
            style={({ pressed }) => [
              styles.stopBtn,
              { backgroundColor: c.errorBg },
              pressed && { opacity: 0.6 },
            ]}
            onPress={onStop}
            onLongPress={onForceKill}
            accessibilityRole="button"
            accessibilityLabel="Stop task"
          >
            <Text style={[styles.stopGlyph, { color: c.error }]}>{"■"}</Text>
            <Text style={[styles.stopText, { color: c.error }]}>Stop</Text>
          </Pressable>
        ) : primaryAction === "retry" && onRetry ? (
          <Pressable
            style={({ pressed }) => [
              styles.retryBtn,
              { backgroundColor: c.brandPrimary },
              pressed && { opacity: 0.85, transform: [{ scale: 0.97 }] },
            ]}
            onPress={onRetry}
            accessibilityRole="button"
            accessibilityLabel="Retry task"
          >
            <Ionicons name="refresh" size={14} color="#FFFFFF" style={styles.retryIcon} />
            <Text style={styles.retryText}>Retry</Text>
          </Pressable>
        ) : primaryAction === "detach" && onDetach ? (
          <Pressable
            style={({ pressed }) => [
              styles.detachBtn,
              { backgroundColor: "#8b5cf618" },
              pressed && { opacity: 0.6 },
            ]}
            onPress={onDetach}
          >
            <Text style={styles.detachGlyph}>{"⏏"}</Text>
            <Text style={styles.detachText}>Detach</Text>
          </Pressable>
        ) : (
          <View style={styles.spacer} />
        )}
      </View>

      {/* Row 2: status · device · Logs */}
      <View style={styles.metaRow}>
        <View style={styles.metaLeft}>
          <Animated.View
            style={[
              styles.statusDot,
              { backgroundColor: palette.dot, opacity: pulse },
            ]}
          />
          <Text style={[styles.statusText, { color: palette.fg }]}>
            {status.toUpperCase()}
          </Text>
          {deviceName ? (
            <>
              <Text style={[styles.metaDot, { color: c.textTertiary }]}>·</Text>
              <Text
                style={[styles.deviceText, { color: c.textSecondary }]}
                numberOfLines={1}
              >
                {deviceName.replace(/\.local$/, "")}
              </Text>
            </>
          ) : null}
        </View>
        {onOpenLogs ? (
          <Pressable
            onPress={onOpenLogs}
            hitSlop={{ top: 8, bottom: 8, left: 8, right: 8 }}
            accessibilityRole="button"
            accessibilityLabel="Open logs"
          >
            <View style={styles.logsBtn}>
              <Ionicons name="document-text-outline" size={13} color={c.brandPrimary} />
              <Text style={[styles.logsText, { color: c.brandPrimary }]}>Logs</Text>
            </View>
          </Pressable>
        ) : null}
      </View>

      {/* Row 3: runner · model chip — surfaces "what's actually
          running this task" without forcing the user to expand
          Agent context. Replaces the redundant ThinkingBubble pill
          that used to render the same info inside the chat. */}
      {runnerLabel ? (
        <View style={styles.chipRow}>
          <View
            style={[
              styles.runnerChip,
              {
                backgroundColor: c.surfaceElevated,
                borderColor: c.border,
              },
            ]}
          >
            <View style={[styles.runnerChipDot, { backgroundColor: palette.dot }]} />
            <Text
              style={[styles.runnerChipText, { color: c.textPrimary }]}
              numberOfLines={1}
            >
              {runnerLabel}
              {modelLabel ? (
                <Text style={{ color: c.textTertiary }}>
                  {"  ·  "}
                  {modelLabel}
                </Text>
              ) : null}
            </Text>
          </View>
        </View>
      ) : null}
    </View>
  );
}

// Status colour palette — reuses the project's status tokens. RUNNING
// uses statusInfo (blue), not brandPrimary, so the user message
// bubble (purple) doesn't shadow the status badge.
function statusPalette(
  c: ReturnType<typeof useColors>,
  status: TaskHeaderStatus,
): { dot: string; fg: string } {
  switch (status) {
    case "running":
    case "queued":
      return { dot: c.info, fg: c.info };
    case "completed":
      return { dot: c.success, fg: c.success };
    case "failed":
      return { dot: c.error, fg: c.error };
    case "stopped":
    default:
      return { dot: c.textTertiary, fg: c.textTertiary };
  }
}

const styles = StyleSheet.create({
  wrap: {
    paddingHorizontal: 12,
    paddingTop: 10,
    paddingBottom: 8,
    borderBottomWidth: 1,
    gap: 6,
  },
  row: {
    flexDirection: "row",
    alignItems: "center",
    justifyContent: "space-between",
    gap: 8,
  },
  backBtn: {
    flexDirection: "row",
    alignItems: "center",
    gap: 4,
    paddingVertical: 6,
    paddingHorizontal: 10,
    paddingRight: 14,
    borderRadius: 8,
  },
  backChevron: { fontSize: 18, fontWeight: "600" },
  backText: { fontSize: 13, fontWeight: "600" },
  titleSlot: { flex: 1 },
  spacer: { width: 60 },
  // Stop — red text on errorBg (X3: not a fully red filled button)
  stopBtn: {
    flexDirection: "row",
    alignItems: "center",
    gap: 5,
    paddingVertical: 6,
    paddingHorizontal: 10,
    borderRadius: 8,
  },
  stopGlyph: { fontSize: 14 },
  stopText: { fontSize: 13, fontWeight: "600" },
  // Retry — primary brand button
  retryBtn: {
    flexDirection: "row",
    alignItems: "center",
    gap: 5,
    paddingVertical: 6,
    paddingHorizontal: 12,
    borderRadius: 8,
  },
  retryIcon: { marginRight: 2 },
  retryText: { color: "#FFFFFF", fontSize: 13, fontWeight: "600" },
  // Detach — purple-soft (existing convention)
  detachBtn: {
    flexDirection: "row",
    alignItems: "center",
    gap: 5,
    paddingVertical: 6,
    paddingHorizontal: 10,
    borderRadius: 8,
  },
  detachGlyph: { fontSize: 14, color: "#8b5cf6" },
  detachText: { fontSize: 13, fontWeight: "600", color: "#8b5cf6" },
  metaRow: {
    flexDirection: "row",
    alignItems: "center",
    justifyContent: "space-between",
    paddingHorizontal: 4,
  },
  metaLeft: {
    flexDirection: "row",
    alignItems: "center",
    gap: 6,
    flex: 1,
    minWidth: 0,
  },
  statusDot: {
    width: 6,
    height: 6,
    borderRadius: 3,
  },
  statusText: {
    fontSize: 11,
    fontWeight: "700",
    letterSpacing: 0.4,
  },
  metaDot: { fontSize: 12 },
  deviceText: {
    fontSize: 12,
    fontWeight: "500",
    flexShrink: 1,
  },
  logsBtn: {
    flexDirection: "row",
    alignItems: "center",
    gap: 4,
    paddingHorizontal: 4,
  },
  logsText: { fontSize: 13, fontWeight: "600" },
  chipRow: {
    flexDirection: "row",
    alignItems: "center",
    paddingHorizontal: 4,
    marginTop: 2,
  },
  runnerChip: {
    flexDirection: "row",
    alignItems: "center",
    gap: 6,
    paddingVertical: 3,
    paddingHorizontal: 8,
    borderRadius: 999,
    borderWidth: 1,
    maxWidth: "100%",
  },
  runnerChipDot: {
    width: 5,
    height: 5,
    borderRadius: 2.5,
  },
  runnerChipText: {
    fontSize: 11,
    fontWeight: "600",
    letterSpacing: 0.2,
    flexShrink: 1,
  },
});
