import React from "react";
import { StyleSheet, Text, View, useWindowDimensions, type TextStyle } from "react-native";
import { useColors } from "../context/ThemeContext";
import { monoFamily, spacing, typography } from "../theme/tokens";

// Tablet-aware bubble width. On wide screens, percent-based maxWidths
// (75% / 90% / 100%) blow out to 700-900pt rivers. Cap at 640pt — the
// reading-line sweet spot — without hurting the phone behaviour where
// the percent never exceeds the cap anyway.
const BUBBLE_HARD_CAP = 640;
function bubbleMaxWidth(windowWidth: number, pct: number): number {
  return Math.min(windowWidth * pct, BUBBLE_HARD_CAP);
}

// Variant-aware chat bubble shell.
//   user   — brand-filled right bubble (used for what the human sent)
//   tool   — small left chip with mono content; for ephemeral tool-call rows
//   error  — red-tinted left card with title + body
//   system — centered banner copy (connection events: Connected,
//            Reconnecting, Session restored)
//
// The "agent" variant is intentionally not in this primitive: the
// existing `ChatBubble` in tasks.tsx handles markdown rendering,
// tokens chip, raw-stream long-press, collapse — that's a richer
// concern than a bubble shell. Use this primitive for the simpler
// surrounding variants and let ChatBubble own assistant rows.
export type MessageVariant = "user" | "tool" | "error" | "system";

export interface MessageBubbleProps {
  variant: MessageVariant;
  content?: string;
  title?: string;
  /** Force mono font on the body. Auto-on for tool variant; auto-on
   *  for user variant (user content is a command — terminal-shaped). */
  mono?: boolean;
  /** Slot for buttons / icons. For error: action row underneath the
   *  body. For tool: leading icon. */
  children?: React.ReactNode;
  /** Optional leading icon node, rendered before the content. Used by
   *  ToolCallRow which passes its own Ionicons element. */
  leading?: React.ReactNode;
}

export function MessageBubble({
  variant,
  content,
  title,
  mono,
  children,
  leading,
}: MessageBubbleProps) {
  const c = useColors();
  const { width: winW } = useWindowDimensions();
  const userMax = bubbleMaxWidth(winW, 0.75);
  const toolMax = bubbleMaxWidth(winW, 0.9);
  const errorMax = bubbleMaxWidth(winW, 1.0);

  if (variant === "system") {
    return (
      <View style={styles.systemRow}>
        {content ? (
          <Text style={[styles.systemText, { color: c.textTertiary }]}>{content}</Text>
        ) : null}
        {children}
      </View>
    );
  }

  if (variant === "user") {
    const bodyStyle: TextStyle = {
      color: "#FFFFFF",
      fontFamily: mono === false ? undefined : monoFamily,
      fontSize: 14,
      lineHeight: 20,
    };
    return (
      <View style={styles.userRow}>
        <View style={[styles.userBubble, { backgroundColor: c.brandPrimary, maxWidth: userMax }]}>
          {content ? <Text style={bodyStyle}>{content}</Text> : null}
          {children}
        </View>
      </View>
    );
  }

  if (variant === "tool") {
    return (
      <View style={styles.toolRow}>
        <View style={[styles.toolBubble, { backgroundColor: c.surfaceMuted, maxWidth: toolMax }]}>
          {leading ? <View style={styles.toolLeading}>{leading}</View> : null}
          {content ? (
            <Text
              style={[
                styles.toolText,
                { color: c.textSecondary, fontFamily: monoFamily },
              ]}
              numberOfLines={2}
            >
              {content}
            </Text>
          ) : null}
          {children}
        </View>
      </View>
    );
  }

  // error
  return (
    <View style={styles.errorRow}>
      <View
        style={[
          styles.errorCard,
          {
            backgroundColor: c.errorBg,
            borderLeftColor: c.error,
            maxWidth: errorMax,
          },
        ]}
      >
        {title ? (
          <Text style={[styles.errorTitle, { color: c.error }]}>{title}</Text>
        ) : null}
        {content ? (
          <Text
            style={[
              styles.errorBody,
              {
                color: c.textPrimary,
                fontFamily: mono ? monoFamily : undefined,
              },
            ]}
          >
            {content}
          </Text>
        ) : null}
        {children}
      </View>
    </View>
  );
}

const styles = StyleSheet.create({
  // user
  userRow: {
    flexDirection: "row",
    justifyContent: "flex-end",
    paddingHorizontal: spacing.lg,
    paddingVertical: spacing.xs,
  },
  userBubble: {
    maxWidth: "75%",
    paddingHorizontal: 14,
    paddingVertical: 10,
    borderRadius: 18,
    borderTopRightRadius: 4,
  },
  // tool
  toolRow: {
    flexDirection: "row",
    justifyContent: "flex-start",
    paddingHorizontal: spacing.lg,
    paddingVertical: 3,
  },
  toolBubble: {
    maxWidth: "90%",
    flexDirection: "row",
    alignItems: "center",
    paddingHorizontal: 10,
    paddingVertical: 6,
    borderRadius: 8,
  },
  toolLeading: { marginRight: 8 },
  toolText: {
    ...typography.monoCaption,
    flexShrink: 1,
  },
  // error
  errorRow: {
    paddingHorizontal: spacing.lg,
    paddingVertical: spacing.xs,
  },
  errorCard: {
    maxWidth: "100%",
    borderLeftWidth: 3,
    borderRadius: 12,
    paddingVertical: 12,
    paddingHorizontal: 14,
  },
  errorTitle: {
    fontSize: 15,
    fontWeight: "600",
    marginBottom: 4,
  },
  errorBody: {
    fontSize: 14,
    lineHeight: 20,
  },
  // system
  systemRow: {
    paddingVertical: 6,
    paddingHorizontal: spacing.lg,
    alignItems: "center",
  },
  systemText: {
    fontSize: 12,
    fontWeight: "500",
  },
});
