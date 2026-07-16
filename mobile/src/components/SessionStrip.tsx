/**
 * SessionStrip — horizontal chip strip showing active Claude Code
 * sessions at a glance. Mobile mirror of the user's 3-pane tmux
 * workflow at the desk (see user_parallel_tmux_workflow.md).
 *
 * Each chip:
 *   ● <title>          12s · 4.2k tok
 *
 * Status dot colour comes from lib/agentStatus.ts — the one vocabulary every
 * surface reads. It used to be defined here, and it disagreed with the Tasks
 * screen: running was emerald here and blue there, completed was blue here and
 * green there, so one task changed colour when you changed screens.
 *
 * Tap dispatches onPress(task). Strip auto-polls /tasks via
 * quicClient.listTasks() on a 4s cadence; the parent screen doesn't need to
 * thread tasks in.
 *
 * Glasses HUD (Mentra G2 / visionOS / Quest) re-uses the same
 * useActiveSessions() hook with a different renderer.
 */

import React, { useCallback, useEffect, useRef, useState } from "react";
import { Pressable, ScrollView, StyleSheet, Text, View } from "react-native";
import { useColors } from "../context/ThemeContext";
import { agentSignalFromTask, agentStateColor } from "../lib/agentStatus";
import { quicClient, Task } from "../lib/quic";
import { YaverGlass } from "./YaverGlass";

interface Props {
  /** Called when the user taps a chip. */
  onPress?: (task: Task) => void;
  /** Max chips to render. Older tasks fall off. Default 8. */
  maxChips?: number;
  /** Poll interval in ms. Default 4000. */
  pollMs?: number;
  /** Filter — default keeps running + review + recently completed (last 2 min). */
  filter?: (task: Task) => boolean;
}


/** Hook the strip + the glasses HUD both consume. */
export function useActiveSessions(opts?: { pollMs?: number; filter?: (t: Task) => boolean }): {
  tasks: Task[];
  error: string;
  refresh: () => Promise<void>;
} {
  const pollMs = opts?.pollMs ?? 4000;
  const filter = opts?.filter ?? defaultFilter;
  const [tasks, setTasks] = useState<Task[]>([]);
  const [error, setError] = useState("");
  const mounted = useRef(true);

  const refresh = useCallback(async () => {
    try {
      const all = await quicClient.listTasks();
      if (!mounted.current) return;
      setTasks(all.filter(filter));
      setError("");
    } catch (e: any) {
      if (!mounted.current) return;
      setError(e?.message ?? "fetch failed");
    }
  }, [filter]);

  useEffect(() => {
    mounted.current = true;
    void refresh();
    const i = setInterval(refresh, pollMs);
    return () => {
      mounted.current = false;
      clearInterval(i);
    };
  }, [refresh, pollMs]);

  return { tasks, error, refresh };
}

/** Default filter: anything live + anything completed in the last 2min. */
function defaultFilter(t: Task): boolean {
  if (t.status === "running" || t.status === "queued" || t.status === "review") return true;
  if (t.status === "completed" || t.status === "failed") {
    // Treat very recently finished tasks as still "interesting"
    return ageSeconds(t) < 120;
  }
  return false;
}

function ageSeconds(t: Task): number {
  const ts = (t as any).startedAt ?? (t as any).createdAt;
  if (!ts) return 9999;
  const d = typeof ts === "string" ? Date.parse(ts) : Number(ts);
  if (!isFinite(d)) return 9999;
  return Math.max(0, Math.round((Date.now() - d) / 1000));
}

function elapsedLabel(secs: number): string {
  if (secs < 60) return `${secs}s`;
  if (secs < 3600) return `${Math.floor(secs / 60)}m`;
  return `${Math.floor(secs / 3600)}h`;
}

function tokensLabel(t: Task): string {
  const total = (t.inputTokens ?? 0) + (t.outputTokens ?? 0);
  if (total < 1) return "";
  if (total < 1000) return `${total} tok`;
  return `${(total / 1000).toFixed(1)}k tok`;
}

function shortTitle(s: string, max = 22): string {
  const trimmed = (s ?? "").trim();
  if (trimmed.length <= max) return trimmed || "task";
  return trimmed.slice(0, max - 1) + "…";
}

export function SessionStrip({ onPress, maxChips = 8, pollMs, filter }: Props): React.JSX.Element | null {
  const c = useColors();
  const { tasks, error } = useActiveSessions({ pollMs, filter });

  if (error) {
    return (
      <View style={[styles.row, { paddingHorizontal: 12 }]}>
        <Text style={{ color: "#ef4444", fontSize: 11 }}>sessions: {error}</Text>
      </View>
    );
  }
  if (tasks.length === 0) return null;

  const visible = tasks.slice(0, maxChips);

  return (
    <ScrollView
      horizontal
      showsHorizontalScrollIndicator={false}
      contentContainerStyle={styles.scroll}
    >
      {visible.map((t) => {
        const age = ageSeconds(t);
        const tok = tokensLabel(t);
        const signal = agentSignalFromTask(t);
        const dot = agentStateColor(signal.state, c);
        return (
          <YaverGlass key={t.id} shape="capsule" tint={c.bgCard} style={styles.chipGlass}>
            <Pressable
              onPress={() => onPress?.(t)}
              style={({ pressed }) => [
                styles.chip,
                { borderColor: c.border, opacity: pressed ? 0.7 : 1 },
              ]}
            >
              <View
                style={[
                  styles.dot,
                  // Hollow reads as "unconfirmed" — queued, or contact lost.
                  signal.hollow
                    ? { borderWidth: 1.5, borderColor: dot, backgroundColor: "transparent" }
                    : { backgroundColor: dot },
                ]}
              />
              <View style={styles.body}>
                <Text style={{ color: c.textPrimary, fontSize: 12, fontWeight: "600" }} numberOfLines={1}>
                  {shortTitle(t.title)}
                </Text>
                <Text style={{ color: c.textMuted, fontSize: 10, fontFamily: "Menlo" }} numberOfLines={1}>
                  {elapsedLabel(age)}
                  {tok ? " · " + tok : ""}
                </Text>
              </View>
            </Pressable>
          </YaverGlass>
        );
      })}
    </ScrollView>
  );
}

const styles = StyleSheet.create({
  scroll: { paddingVertical: 6, paddingHorizontal: 12, gap: 8 },
  row: { flexDirection: "row", alignItems: "center", gap: 8 },
  chipGlass: { marginRight: 8, minWidth: 140, maxWidth: 220 },
  chip: {
    flexDirection: "row",
    alignItems: "center",
    gap: 8,
    paddingHorizontal: 10,
    paddingVertical: 8,
    borderRadius: 999,
    borderWidth: 1,
  },
  dot: { width: 8, height: 8, borderRadius: 4 },
  body: { flexShrink: 1 },
});

export default SessionStrip;
