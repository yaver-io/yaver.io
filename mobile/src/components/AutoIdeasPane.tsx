// AutoIdeasPane — checkbox UI over /autoideas/file. User picks
// items, hits "Implement selected" → /autoideas/select kicks
// autodev with a curated --remained checklist. Generation
// continues in parallel with implementation.
//
// Polls every 5s while the user is on the pane so newly-generated
// ideas show up without manual refresh.

import React, { useCallback, useEffect, useRef, useState } from "react";
import {
  ActivityIndicator,
  FlatList,
  Pressable,
  StyleSheet,
  Text,
  View,
  RefreshControl,
} from "react-native";
import { useColors } from "../context/ThemeContext";
import { quicClient } from "../lib/quic";

type Item = { line: number; checked: boolean; title: string };

type Props = {
  workDir: string;
  project?: string;
  output?: string;
  /** Default engine for the implementation autodev triggered by Select. */
  defaultEngine?: string;
  onStarted?: (res: { loopName?: string; streamName?: string }) => void;
};

export function AutoIdeasPane({
  workDir,
  project,
  output = "ideas.md",
  defaultEngine = "",
  onStarted,
}: Props) {
  const colors = useColors();
  const [items, setItems] = useState<Item[]>([]);
  const [picked, setPicked] = useState<Set<number>>(new Set());
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState<string>("");
  const [path, setPath] = useState("");
  const pollRef = useRef<number | null>(null);

  const refresh = useCallback(async () => {
    try {
      const res = await quicClient.autoideasFile(workDir, output);
      setItems(res.items ?? []);
      setPath(res.path ?? "");
    } catch {
      // network / auth error — leave list as-is
    } finally {
      setLoading(false);
    }
  }, [workDir, output]);

  useEffect(() => {
    refresh();
    // poll while mounted
    pollRef.current = setInterval(refresh, 5000) as unknown as number;
    return () => {
      if (pollRef.current) clearInterval(pollRef.current);
    };
  }, [refresh]);

  const toggle = (line: number) => {
    setPicked((prev) => {
      const next = new Set(prev);
      if (next.has(line)) next.delete(line);
      else next.add(line);
      return next;
    });
  };

  const selectAll = () => {
    const open = items.filter((i) => !i.checked).map((i) => i.line);
    setPicked(new Set(open));
  };
  const clearAll = () => setPicked(new Set());
  const openItems = items.filter((i) => !i.checked);
  const doneItems = items.filter((i) => i.checked);

  const generateMore = async () => {
    setBusy("Generating new ideas…");
    try {
      await quicClient.autoideasStart({
        work_dir: workDir,
        project,
        output,
        max_batches: 1,
        tick: 1,
      });
      setTimeout(refresh, 3000);
    } catch (e: any) {
      setBusy(`Failed: ${e?.message ?? e}`);
      return;
    }
    setBusy("");
  };

  const implementSelected = async () => {
    if (picked.size === 0) return;
    setBusy(`Starting autodev on ${picked.size} item(s)…`);
    try {
      const res = await quicClient.autoideasSelect({
        work_dir: workDir,
        project,
        output,
        lines: Array.from(picked),
        engine: defaultEngine || undefined,
      });
      if (res.ok) {
        setPicked(new Set());
        setBusy(`Started ${res.loop_name} — switch to Chat tab to watch.`);
        onStarted?.({ loopName: res.loop_name, streamName: res.stream_name });
      } else {
        setBusy(`Failed: ${res.error ?? "unknown"}`);
      }
    } catch (e: any) {
      setBusy(`Failed: ${e?.message ?? e}`);
    }
  };

  const styles = makeStyles(colors);

  if (loading) {
    return (
      <View style={styles.center}>
        <ActivityIndicator />
        <Text style={styles.dim}>Loading ideas from {workDir}…</Text>
      </View>
    );
  }

  return (
    <View style={styles.container}>
      <View style={styles.hero}>
        <Text style={styles.heroTitle}>Auto-generated backlog</Text>
        <Text style={styles.heroBody}>
          Pick one or many ideas, then start implementation directly on the machine.
        </Text>
        <View style={styles.heroStats}>
          <View style={styles.statPill}>
            <Text style={styles.statLabel}>Open</Text>
            <Text style={styles.statValue}>{openItems.length}</Text>
          </View>
          <View style={styles.statPill}>
            <Text style={styles.statLabel}>Selected</Text>
            <Text style={styles.statValue}>{picked.size}</Text>
          </View>
          <View style={styles.statPill}>
            <Text style={styles.statLabel}>Done</Text>
            <Text style={styles.statValue}>{doneItems.length}</Text>
          </View>
        </View>
      </View>
      <View style={styles.toolbar}>
        <Pressable style={styles.btn} onPress={selectAll}>
          <Text style={styles.btnText}>Select all open</Text>
        </Pressable>
        <Pressable style={styles.btn} onPress={clearAll}>
          <Text style={styles.btnText}>Clear</Text>
        </Pressable>
        <Pressable style={[styles.btn, styles.btnPrimary]} onPress={generateMore}>
          <Text style={styles.btnText}>+ Generate more</Text>
        </Pressable>
      </View>
      <View style={styles.toolbar}>
        <Pressable
          style={[styles.btn, styles.btnAccent, picked.size === 0 && styles.btnDisabled]}
          onPress={implementSelected}
          disabled={picked.size === 0}
        >
          <Text style={styles.btnText}>
            ▶ Implement {picked.size > 0 ? `(${picked.size})` : "selected"}
          </Text>
        </Pressable>
      </View>
      {busy ? <Text style={styles.busy}>{busy}</Text> : null}
      <FlatList
        data={items}
        keyExtractor={(it) => String(it.line)}
        refreshControl={<RefreshControl refreshing={false} onRefresh={refresh} />}
        ListEmptyComponent={
          <Text style={styles.dim}>
            No ideas yet at {path || output}. Tap “+ Generate more” or run{" "}
            <Text style={styles.code}>yaver autoideas</Text> on the dev machine.
          </Text>
        }
        renderItem={({ item }) => {
          const sel = picked.has(item.line);
          return (
            <Pressable
              style={[styles.row, item.checked && styles.rowDone, sel && styles.rowSelected]}
              onPress={() => !item.checked && toggle(item.line)}
            >
              <View style={styles.rowTop}>
                <View
                  style={[
                    styles.box,
                    item.checked && styles.boxDone,
                    sel && styles.boxSelected,
                  ]}
                >
                  <Text style={styles.boxMark}>
                    {item.checked ? "✓" : sel ? "●" : ""}
                  </Text>
                </View>
                <View style={styles.rowTextWrap}>
                  <Text
                    style={[
                      styles.title,
                      item.checked && styles.titleDone,
                    ]}
                    numberOfLines={3}
                  >
                    {item.title}
                  </Text>
                  <View style={styles.badges}>
                    <Text style={styles.badge}>
                      {item.checked ? "implemented" : sel ? "queued" : "ready"}
                    </Text>
                    <Text style={styles.badge}>line {item.line}</Text>
                  </View>
                </View>
              </View>
            </Pressable>
          );
        }}
      />
    </View>
  );
}

function makeStyles(c: ReturnType<typeof useColors>) {
  return StyleSheet.create({
    container: { flex: 1, backgroundColor: c.bg },
    center: { flex: 1, alignItems: "center", justifyContent: "center", gap: 8 },
    hero: {
      marginHorizontal: 12,
      marginTop: 10,
      marginBottom: 4,
      padding: 14,
      borderRadius: 14,
      backgroundColor: c.bgCard,
      borderWidth: StyleSheet.hairlineWidth,
      borderColor: c.border,
      gap: 8,
    },
    heroTitle: {
      color: c.textPrimary,
      fontSize: 16,
      fontWeight: "700",
    },
    heroBody: {
      color: c.textSecondary,
      fontSize: 13,
      lineHeight: 18,
    },
    heroStats: {
      flexDirection: "row",
      gap: 8,
      flexWrap: "wrap",
    },
    statPill: {
      paddingHorizontal: 10,
      paddingVertical: 8,
      borderRadius: 999,
      backgroundColor: c.bgCardElevated,
      borderWidth: StyleSheet.hairlineWidth,
      borderColor: c.border,
      minWidth: 72,
    },
    statLabel: {
      color: c.textMuted,
      fontSize: 10,
      textTransform: "uppercase",
      marginBottom: 2,
    },
    statValue: {
      color: c.textPrimary,
      fontSize: 16,
      fontWeight: "700",
    },
    dim: { color: c.textMuted, fontSize: 12, textAlign: "center", paddingHorizontal: 24 },
    code: { fontFamily: "Menlo", fontSize: 12 },
    toolbar: {
      flexDirection: "row",
      gap: 8,
      paddingHorizontal: 12,
      paddingTop: 8,
    },
    btn: {
      paddingVertical: 8,
      paddingHorizontal: 12,
      borderRadius: 8,
      backgroundColor: c.bgCard,
      borderWidth: StyleSheet.hairlineWidth,
      borderColor: c.border,
    },
    btnPrimary: { backgroundColor: c.bgCardElevated },
    btnAccent: { backgroundColor: c.tabActive, flex: 1 },
    btnDisabled: { opacity: 0.4 },
    btnText: { color: c.textPrimary, fontSize: 12, fontWeight: "600" },
    busy: {
      color: c.textMuted,
      fontSize: 12,
      paddingHorizontal: 12,
      paddingVertical: 6,
    },
    row: {
      paddingHorizontal: 12,
      paddingVertical: 12,
      gap: 10,
      borderBottomWidth: StyleSheet.hairlineWidth,
      borderBottomColor: c.border,
      backgroundColor: c.bg,
    },
    rowSelected: {
      backgroundColor: c.bgCard,
    },
    rowDone: {
      opacity: 0.72,
    },
    rowTop: {
      flexDirection: "row",
      alignItems: "flex-start",
      gap: 10,
    },
    rowTextWrap: {
      flex: 1,
      gap: 6,
    },
    badges: {
      flexDirection: "row",
      flexWrap: "wrap",
      gap: 6,
    },
    badge: {
      color: c.textMuted,
      fontSize: 11,
      paddingHorizontal: 8,
      paddingVertical: 4,
      borderRadius: 999,
      borderWidth: StyleSheet.hairlineWidth,
      borderColor: c.border,
      backgroundColor: c.bgCardElevated,
      textTransform: "uppercase",
    },
    box: {
      width: 22,
      height: 22,
      borderRadius: 4,
      borderWidth: 1.5,
      borderColor: c.border,
      backgroundColor: c.bg,
      alignItems: "center",
      justifyContent: "center",
    },
    boxDone: { backgroundColor: c.bgCardElevated, borderColor: c.bgCardElevated },
    boxSelected: { backgroundColor: c.tabActive, borderColor: c.tabActive },
    boxMark: { color: c.textPrimary, fontWeight: "700", fontSize: 13 },
    title: {
      color: c.textPrimary,
      fontSize: 14,
      lineHeight: 18,
    },
    titleDone: {
      color: c.textMuted,
      textDecorationLine: "line-through",
    },
  });
}
