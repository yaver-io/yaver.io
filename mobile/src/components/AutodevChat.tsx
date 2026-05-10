// AutodevChat — chat-style live view of an autodev run.
//
// Subscribes to /streams/autodev:<loopName> over SSE via quicClient
// and renders one bubble per structured event:
//   yaver_say     → cyan bubble on the left
//   runner_text   → white bubble on the right
//   runner_action → small dim inline tag
//   runner_result → bold separator line
//   line          → legacy plain-text frame, rendered as runner_text
//
// Generic across runners (Claude / Codex / Aider / Ollama). Pulls the
// daemon address from the live device via the existing quicClient
// singleton — no extra plumbing.

import React, { useEffect, useRef, useState } from "react";
import {
  FlatList,
  StyleSheet,
  Text,
  View,
  ActivityIndicator,
  useWindowDimensions,
} from "react-native";
import { useColors } from "../context/ThemeContext";
import { quicClient } from "../lib/quic";

export type AutodevChatEvent = {
  id: string;
  type: "yaver_say" | "runner_text" | "runner_action" | "runner_result" | "line";
  runner?: string;
  text?: string;
  tool?: string;
  detail?: string;
  status?: string;
  durationMs?: number;
  costUsd?: number;
};

type Props = {
  /** Stream name, e.g. "autodev:sfmg-autodev". */
  streamName: string;
  /** Optional row cap (default 500 — matches the daemon ring buffer). */
  maxRows?: number;
};

let nextEventId = 1;
function newId(): string {
  nextEventId += 1;
  return String(nextEventId);
}

export function AutodevChat({ streamName, maxRows = 500 }: Props) {
  const colors = useColors();
  const { width: winW } = useWindowDimensions();
  // Bubble cap — see MessageBubble.tsx for the same rule. Tablets
  // would otherwise produce 870pt-wide bubbles from 85% × 1024.
  const bubbleCapStyle = { maxWidth: Math.min(winW * 0.85, 640) };
  const [events, setEvents] = useState<AutodevChatEvent[]>([]);
  const [connected, setConnected] = useState(false);
  const listRef = useRef<FlatList<AutodevChatEvent>>(null);

  useEffect(() => {
    setConnected(false);
    setEvents([]);

    const abort = quicClient.streamLog(streamName, (raw) => {
      if (!connected) setConnected(true);
      const ev: AutodevChatEvent = {
        id: newId(),
        type: (raw?.type as AutodevChatEvent["type"]) || "line",
        runner: typeof raw?.runner === "string" ? raw.runner : undefined,
        text: typeof raw?.text === "string" ? raw.text : undefined,
        tool: typeof raw?.tool === "string" ? raw.tool : undefined,
        detail: typeof raw?.detail === "string" ? raw.detail : undefined,
        status: typeof raw?.status === "string" ? raw.status : undefined,
        durationMs:
          typeof raw?.duration_ms === "number" ? raw.duration_ms : undefined,
        costUsd:
          typeof raw?.cost_usd === "number" ? raw.cost_usd : undefined,
      };
      setEvents((prev) => {
        const next = prev.length >= maxRows ? prev.slice(-maxRows + 1) : prev;
        return [...next, ev];
      });
      // Keep latest in view.
      requestAnimationFrame(() => {
        listRef.current?.scrollToEnd({ animated: true });
      });
    });
    return () => abort();
  }, [streamName, maxRows]);

  const styles = makeStyles(colors);

  const renderItem = ({ item }: { item: AutodevChatEvent }) => {
    switch (item.type) {
      case "yaver_say":
        return (
          <View style={[styles.row, styles.rowLeft]}>
            <View style={[styles.bubble, styles.bubbleYaver, bubbleCapStyle]}>
              <Text style={styles.bubbleAuthor}>yaver</Text>
              <Text style={styles.bubbleText}>{item.text}</Text>
            </View>
          </View>
        );
      case "runner_action":
        return (
          <View style={[styles.row, styles.rowRight]}>
            <Text style={styles.actionTag}>
              {item.runner ?? "runner"} · {item.tool ?? ""}{" "}
              {item.detail ?? ""}
            </Text>
          </View>
        );
      case "runner_text":
        return (
          <View style={[styles.row, styles.rowRight]}>
            <View style={[styles.bubble, styles.bubbleRunner, bubbleCapStyle]}>
              <Text style={styles.bubbleAuthor}>{item.runner ?? "runner"}</Text>
              <Text style={styles.bubbleText}>{item.text}</Text>
            </View>
          </View>
        );
      case "runner_result": {
        const dur = item.durationMs ? `${(item.durationMs / 1000).toFixed(1)}s` : "";
        const cost = item.costUsd ? `$${item.costUsd.toFixed(4)}` : "";
        return (
          <View style={styles.resultRow}>
            <Text style={styles.resultText}>
              {(item.runner ?? "runner")} · {item.status ?? "done"}
              {dur ? ` · ${dur}` : ""}
              {cost ? ` · ${cost}` : ""}
            </Text>
          </View>
        );
      }
      case "line":
      default:
        return (
          <View style={[styles.row, styles.rowFull]}>
            <Text style={styles.lineText}>{item.text}</Text>
          </View>
        );
    }
  };

  return (
    <View style={styles.container}>
      {!connected && events.length === 0 ? (
        <View style={styles.connecting}>
          <ActivityIndicator />
          <Text style={styles.connectingText}>Connecting to {streamName}…</Text>
        </View>
      ) : null}
      <FlatList
        ref={listRef}
        data={events}
        keyExtractor={(e) => e.id}
        renderItem={renderItem}
        contentContainerStyle={styles.list}
        showsVerticalScrollIndicator={false}
        onContentSizeChange={() =>
          listRef.current?.scrollToEnd({ animated: false })
        }
      />
    </View>
  );
}

function makeStyles(colors: ReturnType<typeof useColors>) {
  return StyleSheet.create({
    container: {
      flex: 1,
      backgroundColor: colors.bg,
    },
    list: {
      paddingHorizontal: 12,
      paddingVertical: 8,
      gap: 6,
    },
    connecting: {
      paddingVertical: 24,
      alignItems: "center",
      gap: 8,
    },
    connectingText: {
      color: colors.textMuted,
      fontSize: 12,
    },
    row: {
      flexDirection: "row",
      marginVertical: 2,
    },
    rowLeft: { justifyContent: "flex-start" },
    rowRight: { justifyContent: "flex-end" },
    rowFull: { justifyContent: "flex-start" },
    bubble: {
      // Capped at 640pt on tablets so 85% of a 1024pt iPad doesn't
      // produce 870pt rivers. The cap applies to phones too but the
      // % is always smaller, so phone behaviour is unchanged.
      maxWidth: "85%",
      paddingVertical: 6,
      paddingHorizontal: 10,
      borderRadius: 12,
    },
    bubbleYaver: {
      backgroundColor: colors.bgCardElevated,
      borderTopLeftRadius: 2,
      borderLeftWidth: 3,
      borderLeftColor: colors.tabActive,
    },
    bubbleRunner: {
      backgroundColor: colors.bgCard,
      borderTopRightRadius: 2,
    },
    bubbleAuthor: {
      color: colors.textMuted,
      fontSize: 10,
      marginBottom: 2,
      letterSpacing: 0.5,
      textTransform: "uppercase",
    },
    bubbleText: {
      color: colors.textPrimary,
      fontSize: 13,
      lineHeight: 18,
    },
    actionTag: {
      color: colors.textMuted,
      fontSize: 11,
      fontFamily: "Menlo",
      marginVertical: 2,
    },
    resultRow: {
      paddingVertical: 6,
      borderTopWidth: StyleSheet.hairlineWidth,
      borderTopColor: colors.border,
      marginVertical: 4,
    },
    resultText: {
      color: colors.textPrimary,
      fontSize: 11,
      fontWeight: "600",
      textAlign: "center",
      letterSpacing: 0.5,
      textTransform: "uppercase",
    },
    lineText: {
      color: colors.textPrimary,
      fontSize: 12,
      fontFamily: "Menlo",
    },
  });
}
