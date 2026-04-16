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
};

export function AutoIdeasPane({ workDir, project, output = "ideas.md", defaultEngine = "" }: Props) {
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
            <Pressable style={styles.row} onPress={() => !item.checked && toggle(item.line)}>
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
              <Text
                style={[
                  styles.title,
                  item.checked && styles.titleDone,
                ]}
                numberOfLines={3}
              >
                {item.title}
              </Text>
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
      flexDirection: "row",
      alignItems: "center",
      paddingHorizontal: 12,
      paddingVertical: 10,
      gap: 10,
      borderBottomWidth: StyleSheet.hairlineWidth,
      borderBottomColor: c.border,
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
      flex: 1,
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
