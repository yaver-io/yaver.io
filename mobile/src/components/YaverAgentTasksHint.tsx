/**
 * YaverAgentTasksHint — empty-state banner shown in the Tasks tab when
 * the user has no device connected (or no primary set). It explains
 * that the mobile-embedded yaver-agent can still help with control-
 * plane tasks (auth, status, primary management) without first pairing
 * a runner host, and offers tappable suggestion chips that prefill the
 * task composer.
 *
 * The chips don't dispatch a task themselves — they just open the
 * composer with a sensible prompt so the user can review/edit and
 * send. Actual yaver-agent execution is driven by the LLM provider
 * adapter (separate turn).
 */
import React from "react";
import { Pressable, StyleSheet, Text, View } from "react-native";
import { Ionicons } from "@expo/vector-icons";
import { useColors } from "../context/ThemeContext";

export interface YaverAgentSuggestion {
  /** Short label shown on the chip. */
  label: string;
  /** Full prompt prefilled into the composer when tapped. */
  prompt: string;
}

interface Props {
  /** True when the user has zero registered devices yet. */
  hasZeroDevices: boolean;
  /** True when a primary device is set (regardless of online state). */
  primarySet: boolean;
  /** Tapping a chip prefills + opens the composer with this prompt. */
  onSuggestion: (prompt: string) => void;
  /** Tapping the gear opens settings (so user can configure provider). */
  onOpenSettings: () => void;
}

const DEFAULT_SUGGESTIONS_NO_DEVICE: YaverAgentSuggestion[] = [
  { label: "Connect primary device", prompt: "Connect my primary device — walk me through it." },
  { label: "Pair a new box", prompt: "I have a new Linux box. How do I pair it from this phone?" },
];

const DEFAULT_SUGGESTIONS_DISCONNECTED: YaverAgentSuggestion[] = [
  { label: "Authenticate primary", prompt: "Authenticate my primary device." },
  { label: "Audit primary runners", prompt: "What runners are authenticated on my primary device?" },
  { label: "Set up Codex on primary", prompt: "Set up Codex on the primary device." },
  { label: "Set up GLM via OpenCode", prompt: "Configure OpenCode with GLM on the primary device." },
];

export function YaverAgentTasksHint({
  hasZeroDevices,
  primarySet,
  onSuggestion,
  onOpenSettings,
}: Props) {
  const c = useColors();

  // Pick suggestion set based on the state we're in. Chips always
  // render because tapping them just prefills the composer — useful
  // even before the LLM adapter is wired (the user can review and
  // edit before sending).
  const suggestions = hasZeroDevices
    ? DEFAULT_SUGGESTIONS_NO_DEVICE
    : DEFAULT_SUGGESTIONS_DISCONNECTED;

  const headline = hasZeroDevices
    ? "No device connected"
    : !primarySet
    ? "No primary device set"
    : "Primary device offline";

  return (
    <View
      style={[
        styles.card,
        { backgroundColor: c.warnBg, borderColor: c.warnBorder },
      ]}
    >
      <View style={styles.headerRow}>
        <View style={styles.headerLeft}>
          <Ionicons name="alert-circle-outline" size={18} color={c.warn} />
          <Text style={[styles.headline, { color: c.warn }]}>{headline}</Text>
        </View>
        <Pressable
          onPress={onOpenSettings}
          hitSlop={{ top: 8, bottom: 8, left: 8, right: 8 }}
          accessibilityRole="button"
          accessibilityLabel="Open Yaver Agent settings"
        >
          <Ionicons name="settings-outline" size={18} color={c.warn} />
        </Pressable>
      </View>

      <Text style={[styles.sub, { color: c.textPrimary }]}>
        Yaver Agent runs control-plane tasks (auth, status, primary
        management) without needing a connected machine. Configure a
        provider in Settings, then tap a suggestion below to start.
      </Text>

      <View style={styles.chipsRow}>
        {suggestions.map((s) => (
          <Pressable
            key={s.label}
            onPress={() => onSuggestion(s.prompt)}
            style={({ pressed }) => [
              styles.chip,
              {
                backgroundColor: pressed ? c.warnBg : "transparent",
                borderColor: c.warn,
              },
            ]}
            accessibilityRole="button"
            accessibilityLabel={`Use suggestion: ${s.label}`}
          >
            <Text style={[styles.chipText, { color: c.warn }]} numberOfLines={1}>
              {s.label}
            </Text>
          </Pressable>
        ))}
      </View>
    </View>
  );
}

const styles = StyleSheet.create({
  card: {
    marginHorizontal: 16,
    marginTop: 12,
    borderWidth: 1,
    borderRadius: 12,
    padding: 14,
  },
  headerRow: {
    flexDirection: "row",
    alignItems: "center",
    justifyContent: "space-between",
  },
  headerLeft: {
    flexDirection: "row",
    alignItems: "center",
    gap: 8,
    flexShrink: 1,
  },
  headline: {
    fontWeight: "700",
    fontSize: 13,
  },
  sub: {
    marginTop: 8,
    fontSize: 12,
    lineHeight: 18,
  },
  cta: {
    marginTop: 10,
    paddingHorizontal: 14,
    paddingVertical: 8,
    borderRadius: 999,
    borderWidth: 1,
    alignSelf: "flex-start",
  },
  ctaText: { fontWeight: "700", fontSize: 12 },
  chipsRow: {
    marginTop: 10,
    flexDirection: "row",
    flexWrap: "wrap",
    gap: 8,
  },
  chip: {
    paddingHorizontal: 12,
    paddingVertical: 7,
    borderRadius: 999,
    borderWidth: 1,
  },
  chipText: { fontSize: 12, fontWeight: "600" },
});
