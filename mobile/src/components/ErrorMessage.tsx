import React from "react";
import { Ionicons } from "@expo/vector-icons";
import { Pressable, StyleSheet, Text, View } from "react-native";
import { useColors } from "../context/ThemeContext";
import { monoFamily, spacing } from "../theme/tokens";

// Best-effort smart-retry detection. Each rule reads the error
// message and returns either null or a short suggestion the user can
// tap. Keep the rules narrow — false positives here send the user
// down the wrong path. New patterns belong here, not inline at call
// sites. Intentionally case-insensitive on the haystack.
export interface SmartRetrySuggestion {
  label: string;
  /** Raw key for analytics so we can see which suggestions get tapped. */
  kind: "skip-git-repo-check" | "api-key-missing" | "node-modules" | "permission";
}

export function detectSmartRetry(message: string): SmartRetrySuggestion | null {
  const m = String(message || "").toLowerCase();
  if (!m) return null;
  if (m.includes("skip-git-repo-check") && m.includes("not specified")) {
    return { label: "Retry with --skip-git-repo-check", kind: "skip-git-repo-check" };
  }
  if (
    m.includes("api key not found") ||
    m.includes("missing api key") ||
    m.includes("no api key") ||
    m.includes("api_key_missing")
  ) {
    return { label: "Open API key settings", kind: "api-key-missing" };
  }
  if (
    m.includes("node_modules") ||
    m.includes("cannot find module") ||
    m.includes("module not found")
  ) {
    return { label: "Try `npm install` first", kind: "node-modules" };
  }
  if (m.includes("permission denied") || m.includes("eacces")) {
    return { label: "Check directory permissions", kind: "permission" };
  }
  return null;
}

export interface ErrorMessageProps {
  /** The raw error string from the agent. */
  message: string;
  /** Optional title; defaults to "Task failed". */
  title?: string;
  /** Tapping the smart-retry suggestion. Pass `undefined` to hide it
   *  even if a suggestion is detected. */
  onSmartRetry?: (suggestion: SmartRetrySuggestion) => void;
  /** Tapping "Open in agent" — escalates to the full log/REPL view. */
  onOpenInAgent?: () => void;
  /** Tapping "Copy error". Should copy and toast. */
  onCopyError?: () => void;
}

export function ErrorMessage({
  message,
  title = "Task failed",
  onSmartRetry,
  onOpenInAgent,
  onCopyError,
}: ErrorMessageProps) {
  const c = useColors();
  const suggestion = detectSmartRetry(message);
  const hasActions = (suggestion && onSmartRetry) || onOpenInAgent || onCopyError;

  return (
    <View style={styles.row}>
      <View
        style={[
          styles.card,
          {
            backgroundColor: c.errorBg,
            borderLeftColor: c.error,
          },
        ]}
      >
        <View style={styles.header}>
          <Ionicons name="warning" size={18} color={c.error} style={styles.icon} />
          <Text style={[styles.title, { color: c.error }]}>{title}</Text>
        </View>
        <Text
          style={[
            styles.body,
            { color: c.textPrimary, fontFamily: monoFamily },
          ]}
        >
          {message}
        </Text>
        {hasActions ? (
          <View style={styles.actions}>
            {suggestion && onSmartRetry ? (
              <Pressable
                style={({ pressed }) => [
                  styles.btnPrimary,
                  { backgroundColor: c.brandPrimary },
                  pressed && { opacity: 0.85, transform: [{ scale: 0.97 }] },
                ]}
                onPress={() => onSmartRetry(suggestion)}
                accessibilityRole="button"
                accessibilityLabel={suggestion.label}
              >
                <Text style={styles.btnPrimaryText}>{suggestion.label}</Text>
              </Pressable>
            ) : null}
            {onOpenInAgent ? (
              <Pressable
                style={({ pressed }) => [
                  styles.btnSecondary,
                  { borderColor: c.borderStrong },
                  pressed && { opacity: 0.7 },
                ]}
                onPress={onOpenInAgent}
                accessibilityRole="button"
                accessibilityLabel="Open full agent log"
              >
                <Text style={[styles.btnSecondaryText, { color: c.textPrimary }]}>
                  Open in agent
                </Text>
              </Pressable>
            ) : null}
            {onCopyError ? (
              <Pressable
                style={({ pressed }) => [
                  styles.btnTertiary,
                  pressed && { opacity: 0.55 },
                ]}
                onPress={onCopyError}
                accessibilityRole="button"
                accessibilityLabel="Copy error message"
              >
                <Text style={[styles.btnTertiaryText, { color: c.textSecondary }]}>
                  Copy error
                </Text>
              </Pressable>
            ) : null}
          </View>
        ) : null}
      </View>
    </View>
  );
}

const styles = StyleSheet.create({
  row: {
    paddingHorizontal: spacing.lg,
    paddingVertical: spacing.xs,
  },
  card: {
    borderLeftWidth: 3,
    borderRadius: 12,
    padding: 14,
  },
  header: {
    flexDirection: "row",
    alignItems: "center",
    marginBottom: 4,
  },
  icon: { marginRight: 6 },
  title: {
    fontSize: 15,
    fontWeight: "600",
  },
  body: {
    fontSize: 14,
    lineHeight: 20,
  },
  actions: {
    flexDirection: "row",
    flexWrap: "wrap",
    alignItems: "center",
    gap: 8,
    marginTop: 10,
  },
  btnPrimary: {
    paddingHorizontal: 14,
    paddingVertical: 8,
    borderRadius: 10,
  },
  btnPrimaryText: {
    color: "#FFFFFF",
    fontSize: 13,
    fontWeight: "600",
  },
  btnSecondary: {
    paddingHorizontal: 12,
    paddingVertical: 7,
    borderRadius: 10,
    borderWidth: 1,
  },
  btnSecondaryText: {
    fontSize: 13,
    fontWeight: "600",
  },
  btnTertiary: {
    paddingHorizontal: 8,
    paddingVertical: 7,
  },
  btnTertiaryText: {
    fontSize: 13,
    fontWeight: "600",
  },
});
