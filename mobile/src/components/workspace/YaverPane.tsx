// YaverPane — one tile inside a YaverWorkspace. Wraps any RN content
// (terminal output, WebView, clip player, image, plain text) with a
// numbered title bar that doubles as the focus target. Tap to focus.
// Long-press for context actions (close, swap, full-screen) — deferred
// to phase 2; the title bar fires onLongPress today so consumers can
// hook in.
//
// Design constraints, in priority order:
//   1. AR-glasses friendly — 1080p × 50° FoV is the bottleneck.
//      Title bar is one Menlo-monospace line at fontSize 11, so 4
//      panes in a 2×2 grid each get a usable strip.
//   2. Mobile-app dev parity — when the workspace hosts a Hermes-
//      pushed app under test, that pane is just a YaverPane with a
//      custom `renderContent` slot. We never re-implement the bundle
//      loader; it stays in YaverBundleLoader.swift territory.
//   3. Zero new dependencies. Pure RN primitives + Pressable.
//      Resize/drag (Reanimated + gesture-handler) is intentionally
//      deferred to a separate phase so this PR ships fast.
//
// The pane is *content-shape agnostic*. It does NOT know what's inside
// — that's the consumer's job via `children`. We just paint the chrome
// and forward focus state.

import React from "react";
import { Platform, Pressable, StyleSheet, Text, View } from "react-native";

export type YaverPaneKind =
  | "terminal"   // PTY over /ws/terminal — full shell
  | "agent"      // runYaverAgent loop (Claude/Codex/Opencode locally)
  | "webview"    // react-native-webview (web preview iframe, dashboards)
  | "clip"       // vibe-preview MP4 player
  | "image"      // browser-MCP screenshot, last frame
  | "text"       // arbitrary text (logs, doctor output, test results)
  | "custom";    // consumer fully owns rendering (e.g. Hermes guest app)

export interface YaverPaneProps {
  /** Stable id — used by the keyboard layer for Cmd-N selection. */
  id: string;
  /** Visible above the content. Truncated to one line. */
  title: string;
  /** 1-based slot index used for the keyboard shortcut hint ("1", "2", …). */
  index: number;
  /** Pane type — informs the title-bar accent only; rendering is via children. */
  kind: YaverPaneKind;
  /** Is this pane the currently-focused tile? */
  focused: boolean;
  /** Tap on the chrome → request focus. */
  onFocus: () => void;
  /** Long-press on the title bar (context actions). Optional. */
  onLongPress?: () => void;
  /** Content. Should fill the available space. */
  children?: React.ReactNode;
  /** Optional status string shown in the title bar (e.g. "● live", "○ off"). */
  status?: string;
  /** Status colour — defaults to the muted palette colour. */
  statusColor?: string;
}

// Palette mirrors glass-terminal.tsx so the workspace doesn't look like
// a different app from the single-pane terminal.
const PAL = {
  bg: "#000000",
  fg: "#e5e7eb",
  muted: "#9ca3af",
  border: "#1f2937",
  borderFocused: "#a78bfa",
  chip: "#111827",
  chipText: "#d1d5db",
  accent: "#a78bfa",
};

const KIND_ACCENT: Record<YaverPaneKind, string> = {
  terminal: "#34d399", // green — same as model output in glass-terminal
  agent:    "#fbbf24", // amber — same as user prompt
  webview:  "#60a5fa", // blue — same as prompt char
  clip:     "#f472b6", // pink — clips are time-based
  image:    "#a78bfa", // accent — same as focus
  text:     "#9ca3af", // muted — neutral
  custom:   "#a78bfa",
};

export function YaverPane(props: YaverPaneProps): React.ReactElement {
  const { id, title, index, kind, focused, onFocus, onLongPress, children, status, statusColor } = props;
  const accent = KIND_ACCENT[kind];
  return (
    <View
      style={[
        styles.pane,
        {
          borderColor: focused ? PAL.borderFocused : PAL.border,
          borderWidth: focused ? 2 : StyleSheet.hairlineWidth,
        },
      ]}
      testID={`pane:${id}`}
    >
      <Pressable
        onPress={onFocus}
        onLongPress={onLongPress}
        delayLongPress={400}
        hitSlop={4}
        style={[styles.titleBar, { borderBottomColor: PAL.border }]}
      >
        <Text style={{
          color: accent,
          fontFamily: Platform.select({ ios: "Menlo", android: "monospace" }),
          fontSize: 10,
          marginRight: 6,
        }}>{index}</Text>
        <Text
          numberOfLines={1}
          style={{
            color: focused ? PAL.fg : PAL.muted,
            fontFamily: Platform.select({ ios: "Menlo", android: "monospace" }),
            fontSize: 11,
            fontWeight: focused ? "600" : "400",
            flex: 1,
          }}
        >
          {title}
        </Text>
        {status ? (
          <Text style={{
            color: statusColor ?? PAL.muted,
            fontFamily: Platform.select({ ios: "Menlo", android: "monospace" }),
            fontSize: 9,
            marginLeft: 6,
          }}>{status}</Text>
        ) : null}
      </Pressable>
      <View style={styles.content}>{children}</View>
    </View>
  );
}

const styles = StyleSheet.create({
  pane: {
    flex: 1,
    backgroundColor: PAL.bg,
    borderRadius: 6,
    overflow: "hidden",
    margin: 2,
  },
  titleBar: {
    flexDirection: "row",
    alignItems: "center",
    paddingHorizontal: 8,
    paddingVertical: 4,
    borderBottomWidth: StyleSheet.hairlineWidth,
  },
  content: { flex: 1 },
});
