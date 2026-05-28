// YaverWorkspace — i3-style tile container for the Yaver mobile app.
// Hosts N panes (1–6) in a fixed grid layout chosen at mount time.
//
// V1 design (intentionally minimal so it ships fast and doesn't break
// the load-bearing single-pane glass-terminal flow):
//   • Layouts: "1x1", "1x2" (stacked), "2x1" (side-by-side), "2x2",
//     "1x3" (column of 3), "2x3" (six panes).
//   • Tap a pane to focus. The focused pane gets a thicker accent border
//     and a Cmd-N hint visible in its title bar.
//   • No resize / drag in V1 — fixed splits keep AR-glasses readability
//     predictable. Resize lands in phase 2 with Reanimated.
//   • Cmd-N keyboard nav (BT keyboard) is wired through useWorkspaceKeyboard.
//
// The component is content-agnostic. Callers pass a `panes` array
// where each entry brings its own renderer — see the YaverPaneKind /
// YaverPaneProps types in YaverPane.tsx.

import React, { useCallback, useMemo, useState } from "react";
import { StyleSheet, View } from "react-native";

import { YaverPane, type YaverPaneKind, type YaverPaneProps } from "./YaverPane";
import { useWorkspaceKeyboard } from "./useWorkspaceKeyboard";

export type WorkspaceLayout = "1x1" | "1x2" | "2x1" | "2x2" | "1x3" | "2x3";

export interface WorkspacePaneDef {
  id: string;
  title: string;
  kind: YaverPaneKind;
  render: (api: { focused: boolean }) => React.ReactNode;
  status?: string;
  statusColor?: string;
  onLongPress?: () => void;
}

export interface YaverWorkspaceProps {
  panes: WorkspacePaneDef[];
  /** Initial layout. Auto-derived from panes.length when omitted. */
  layout?: WorkspaceLayout;
  /** Initial focused pane id. Defaults to panes[0]. */
  initialFocusId?: string;
  /** Notified when the user focuses a different pane. */
  onFocusChange?: (id: string) => void;
}

export function YaverWorkspace(props: YaverWorkspaceProps): React.ReactElement {
  const { panes, layout, initialFocusId, onFocusChange } = props;
  const resolvedLayout = layout ?? defaultLayoutForCount(panes.length);
  const [focusId, setFocusId] = useState<string>(initialFocusId ?? panes[0]?.id ?? "");

  const focusPane = useCallback((id: string) => {
    setFocusId(id);
    onFocusChange?.(id);
  }, [onFocusChange]);

  // Keyboard: Cmd-1..6 jump to slot; Cmd-J / Cmd-K cycle. Escape unfocuses.
  // No-op on the older RN/Expo builds that don't expose hardware key events —
  // useWorkspaceKeyboard returns silently in that case.
  useWorkspaceKeyboard({
    panes,
    focusId,
    onFocus: focusPane,
    onUnfocus: () => setFocusId(""),
  });

  const grid = useMemo(() => layoutGrid(resolvedLayout, panes.length), [resolvedLayout, panes.length]);

  return (
    <View style={styles.root}>
      {grid.rows.map((row, rIdx) => (
        <View key={`row-${rIdx}`} style={[styles.row, { flex: row.flex }]}>
          {row.cols.map((col, cIdx) => {
            const idx = rowColToIndex(grid, rIdx, cIdx);
            const def = panes[idx];
            if (!def) return <View key={`empty-${rIdx}-${cIdx}`} style={{ flex: col.flex }} />;
            const focused = def.id === focusId;
            return (
              <View key={def.id} style={{ flex: col.flex }}>
                <YaverPane
                  id={def.id}
                  title={def.title}
                  index={idx + 1}
                  kind={def.kind}
                  focused={focused}
                  status={def.status}
                  statusColor={def.statusColor}
                  onFocus={() => focusPane(def.id)}
                  onLongPress={def.onLongPress}
                >
                  {def.render({ focused })}
                </YaverPane>
              </View>
            );
          })}
        </View>
      ))}
    </View>
  );
}

function defaultLayoutForCount(n: number): WorkspaceLayout {
  switch (n) {
    case 1: return "1x1";
    case 2: return "2x1";  // side-by-side reads better on landscape XREAL
    case 3: return "1x3";  // column of three (terminal up top, two below)
    case 4: return "2x2";
    case 5:
    case 6: return "2x3";
    default: return "2x2";
  }
}

interface GridSpec {
  rows: Array<{ flex: number; cols: Array<{ flex: number }> }>;
  // Linear index ordering used by rowColToIndex.
  order: Array<{ row: number; col: number }>;
}

function layoutGrid(layout: WorkspaceLayout, paneCount: number): GridSpec {
  const equal = (n: number) => Array.from({ length: n }, () => ({ flex: 1 }));
  switch (layout) {
    case "1x1":
      return { rows: [{ flex: 1, cols: equal(1) }], order: [{ row: 0, col: 0 }] };
    case "1x2":
      return {
        rows: [
          { flex: 1, cols: equal(1) },
          { flex: 1, cols: equal(1) },
        ],
        order: [{ row: 0, col: 0 }, { row: 1, col: 0 }],
      };
    case "2x1":
      return {
        rows: [{ flex: 1, cols: equal(2) }],
        order: [{ row: 0, col: 0 }, { row: 0, col: 1 }],
      };
    case "2x2":
      return {
        rows: [
          { flex: 1, cols: equal(2) },
          { flex: 1, cols: equal(2) },
        ],
        order: [
          { row: 0, col: 0 }, { row: 0, col: 1 },
          { row: 1, col: 0 }, { row: 1, col: 1 },
        ],
      };
    case "1x3":
      // First row is the "primary" pane (taller); two narrower below.
      return {
        rows: [
          { flex: 1.6, cols: equal(1) },
          { flex: 1.0, cols: equal(2) },
        ],
        order: [
          { row: 0, col: 0 },
          { row: 1, col: 0 }, { row: 1, col: 1 },
        ],
      };
    case "2x3":
      return {
        rows: [
          { flex: 1, cols: equal(3) },
          { flex: 1, cols: equal(3) },
        ],
        order: [
          { row: 0, col: 0 }, { row: 0, col: 1 }, { row: 0, col: 2 },
          { row: 1, col: 0 }, { row: 1, col: 1 }, { row: 1, col: 2 },
        ],
      };
  }
}

function rowColToIndex(g: GridSpec, row: number, col: number): number {
  return g.order.findIndex((o) => o.row === row && o.col === col);
}

const styles = StyleSheet.create({
  root: { flex: 1, backgroundColor: "#000000" },
  row: { flexDirection: "row" },
});

// Re-export YaverPane types so consumers can import everything from
// "../components/workspace" without per-file imports.
export type { YaverPaneKind, YaverPaneProps };
