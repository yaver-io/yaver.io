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
  kind: "skip-git-repo-check" | "api-key-missing" | "node-modules" | "permission" | "chown-fix" | "runner-auth-needed";
  /** Optional payload tied to the suggestion. For chown-fix this
   *  carries the exact `sudo chown -R …` command pulled out of the
   *  agent's preflight error so the UI can offer a Copy button without
   *  having to re-derive it. For runner-auth-needed it carries the
   *  runner id ("claude" | "codex") so the caller can open the right
   *  RunnerAuthModal pre-filled. */
  payload?: string;
}

/** Extract the `sudo chown -R <uid:gid> <path>` line from the agent's
 *  workdir-not-writable preflight error. The agent emits it verbatim
 *  inside backticks; pull it out so the mobile UI can put it on the
 *  clipboard with one tap. Returns "" when nothing matches.
 */
function extractChownCommand(raw: string): string {
  // Backtick-delimited form — current agent text (1.99.156+).
  const backtick = raw.match(/`(sudo chown -R [^`]+)`/i);
  if (backtick && backtick[1]) return backtick[1];
  // Defensive fallback for log lines without backticks. Match up to the
  // next sentence boundary or newline so we don't swallow trailing
  // explanatory text.
  const bare = raw.match(/sudo chown -R [^\s][^.\n]*/i);
  if (bare && bare[0]) return bare[0].trim();
  return "";
}

// Runner-auth detection: claude prints "Not logged in · Please run /login"
// (subscription token expired / Keychain locked / no creds), codex prints
// "Sign in required" / similar. Match BEFORE api-key-missing so we never
// route the user toward an API-key-style fix when the real answer is OAuth.
// Returns the runner id when we can attribute the failure.
function detectRunnerAuthFailure(haystack: string): "claude" | "codex" | null {
  const m = haystack.toLowerCase();
  const looksLikeClaude =
    (m.includes("not logged in") && (m.includes("/login") || m.includes("please run"))) ||
    m.includes("invalid bearer token") ||
    m.includes("invalid authentication credentials") ||
    m.includes("claude code-credentials");
  if (looksLikeClaude) return "claude";
  const looksLikeCodex =
    (m.includes("sign in required") && (m.includes("codex") || m.includes("chatgpt"))) ||
    m.includes("codex login --device-auth") ||
    (m.includes("not authenticated") && m.includes("codex"));
  if (looksLikeCodex) return "codex";
  return null;
}

export function detectSmartRetry(message: string): SmartRetrySuggestion | null {
  const raw = String(message || "");
  const m = raw.toLowerCase();
  if (!m) return null;
  // Subscription-OAuth failures take priority over generic api-key hints
  // — claude/codex auth needs the browser flow, never an API key.
  const runner = detectRunnerAuthFailure(raw);
  if (runner) {
    return {
      label: runner === "codex" ? "Sign in to Codex" : "Sign in to Claude Code",
      kind: "runner-auth-needed",
      payload: runner,
    };
  }
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
  // Codex bwrap workdir-not-writable preflight (agent 1.99.156+). The
  // error embeds the exact `sudo chown -R <uid:gid> <path>` to copy —
  // present it as a one-tap fix so the user doesn't have to re-derive
  // it from the message. Match this BEFORE the generic "permission
  // denied" rule so we offer the actionable suggestion first.
  if (m.includes("codex sandbox cannot write") || m.includes("must be owned by the user running yaver")) {
    const cmd = extractChownCommand(raw);
    return {
      label: cmd ? "Copy chown command" : "Open Permissions doctor",
      kind: "chown-fix",
      payload: cmd || undefined,
    };
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
