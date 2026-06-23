import React, { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { ActivityIndicator, Animated, FlatList, PanResponder, Pressable, ScrollView, StyleSheet, Text, View } from "react-native";
import { GestureHandlerRootView } from "react-native-gesture-handler";
import DraggableFlatList, { ScaleDecorator } from "react-native-draggable-flatlist";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { useLocalSearchParams, useRouter } from "expo-router";
import { useColors } from "../src/context/ThemeContext";
import { AppBackButton } from "../src/components/AppBackButton";
import {
  browsePhoneTable,
  deletePhoneRow,
  getPhoneProject,
  getPhoneProjectAccess,
  updatePhoneRow,
  type PhoneAppSpec,
  type PhoneProject,
  type PhoneProjectAccess,
  type PhoneSchema,
} from "../src/lib/phoneProjects";

// SwipeableCard — end-user swipe-to-delete (runtime "yaver draggable mode"),
// built on core RN PanResponder so it needs no GestureHandlerRootView. Active
// only when the builder enabled swipeDelete on the list node. Swiping a card
// left past the threshold deletes the row.
function SwipeableCard({
  enabled,
  onDelete,
  style,
  children,
}: {
  enabled: boolean;
  onDelete: () => void;
  style: any;
  children: React.ReactNode;
}) {
  const tx = useRef(new Animated.Value(0)).current;
  const pan = useMemo(
    () =>
      PanResponder.create({
        onMoveShouldSetPanResponder: (_, g) => enabled && g.dx < -8 && Math.abs(g.dx) > Math.abs(g.dy),
        onPanResponderMove: (_, g) => { if (g.dx < 0) tx.setValue(g.dx); },
        onPanResponderRelease: (_, g) => {
          if (g.dx < -120) {
            Animated.timing(tx, { toValue: -500, duration: 150, useNativeDriver: true }).start(() => onDelete());
          } else {
            Animated.spring(tx, { toValue: 0, useNativeDriver: true }).start();
          }
        },
      }),
    [enabled, onDelete, tx],
  );
  if (!enabled) return <View style={style}>{children}</View>;
  return (
    <Animated.View {...pan.panHandlers} style={[style, { transform: [{ translateX: tx }] }]}>
      {children}
    </Animated.View>
  );
}

// KanbanCard — a card the end user drags across columns. On release it reports
// the drop x (screen coords) so the board can hit-test which column it landed in.
function KanbanCard({
  enabled,
  onGrab,
  onDrop,
  style,
  children,
}: {
  enabled: boolean;
  onGrab: () => void;
  onDrop: (moveX: number, moveY: number) => void;
  style: any;
  children: React.ReactNode;
}) {
  const tx = useRef(new Animated.Value(0)).current;
  const ty = useRef(new Animated.Value(0)).current;
  const pan = useMemo(
    () =>
      PanResponder.create({
        onMoveShouldSetPanResponder: (_, g) => enabled && (Math.abs(g.dx) > 6 || Math.abs(g.dy) > 6),
        onPanResponderGrant: () => onGrab(),
        onPanResponderMove: (_, g) => { tx.setValue(g.dx); ty.setValue(g.dy); },
        onPanResponderRelease: (_, g) => {
          onDrop(g.moveX, g.moveY);
          Animated.parallel([
            Animated.spring(tx, { toValue: 0, useNativeDriver: true }),
            Animated.spring(ty, { toValue: 0, useNativeDriver: true }),
          ]).start();
        },
      }),
    [enabled, onGrab, onDrop, tx, ty],
  );
  if (!enabled) return <View style={style}>{children}</View>;
  return (
    <Animated.View {...pan.panHandlers} style={[style, { transform: [{ translateX: tx }, { translateY: ty }], zIndex: 10 }]}>
      {children}
    </Animated.View>
  );
}

// KanbanBoard — columns of draggable cards. Drag a card into another column to
// change its group-by value (persisted via updatePhoneRow). Columns are measured
// on drag-start so the drop hit-test is accurate even after horizontal scroll.
function KanbanBoard({
  groups,
  pk,
  enabled,
  onMove,
  renderBody,
  c,
  marginTop,
}: {
  groups: Array<{ key: string; rows: Array<Record<string, unknown>> }>;
  pk: string;
  enabled: boolean;
  onMove: (rowId: string, targetKey: string) => void;
  renderBody: (item: Record<string, unknown>) => React.ReactNode;
  c: any;
  marginTop: number;
}) {
  const colRefs = useRef<Record<string, View | null>>({});
  const frames = useRef<Record<string, { x: number; w: number }>>({});
  const measure = useCallback(() => {
    for (const k of Object.keys(colRefs.current)) {
      const node = colRefs.current[k];
      node?.measureInWindow((x, _y, w) => { frames.current[k] = { x, w }; });
    }
  }, []);
  const hitTest = useCallback((moveX: number): string | null => {
    for (const k of Object.keys(frames.current)) {
      const f = frames.current[k];
      if (f && moveX >= f.x && moveX <= f.x + f.w) return k;
    }
    return null;
  }, []);
  return (
    <ScrollView
      key="list"
      horizontal
      showsHorizontalScrollIndicator={false}
      style={{ flex: 1, marginTop }}
      contentContainerStyle={{ gap: 12, padding: 16 }}
    >
      {groups.map((g) => (
        <View
          key={g.key}
          ref={(r) => { colRefs.current[g.key] = r; }}
          style={[styles.bcol, { backgroundColor: c.bgCard, borderColor: c.border }]}
        >
          <Text style={[styles.bcolh, { color: c.textMuted }]}>
            {g.key} ({g.rows.length})
          </Text>
          {g.rows.map((item, i) => (
            <KanbanCard
              key={i}
              enabled={enabled}
              onGrab={measure}
              onDrop={(moveX) => {
                const target = hitTest(moveX);
                if (target && target !== g.key) onMove(String(item[pk] ?? ""), target);
              }}
              style={[styles.card, { borderColor: c.border, backgroundColor: c.bg }]}
            >
              {renderBody(item)}
            </KanbanCard>
          ))}
        </View>
      ))}
    </ScrollView>
  );
}

// run-app — generic READ-ONLY renderer for a Yaver Serverless app on mobile.
// Mirrors the web RunSharedApp: it reads the project's app.yaml screens + table
// schema and renders an interactive list view backed by the live /data API.
// This is the "USE the app" runtime for serverless-lite projects on mobile
// (distinct from the Hermes path used for full third-party RN code).
//
// Today it renders against the CONNECTED agent (owner/preview). The friend-link
// path (remote host + scoped read-only token from a share) reuses this exact
// renderer; wiring the deep link + remote data source is the next step.

function tablesFor(schema: PhoneSchema | null | undefined, app: PhoneAppSpec | null | undefined): string[] {
  const fromScreens = (app?.screens ?? []).map((s) => s.table).filter((t): t is string => !!t);
  if (fromScreens.length) return Array.from(new Set(fromScreens));
  return (schema?.tables ?? []).map((t) => t.name);
}

export default function RunAppScreen() {
  const { slug } = useLocalSearchParams<{ slug: string }>();
  const slugStr = String(slug ?? "");
  const c = useColors();
  const insets = useSafeAreaInsets();
  const router = useRouter();

  const [project, setProject] = useState<PhoneProject | null>(null);
  const [active, setActive] = useState<string | null>(null);
  const [rows, setRows] = useState<Array<Record<string, unknown>>>([]);
  const [loading, setLoading] = useState(true);
  const [err, setErr] = useState<string | null>(null);
  const [access, setAccess] = useState<PhoneProjectAccess | null>(null);

  const tables = useMemo(() => tablesFor(project?.schema, project?.app), [project]);

  const loadRows = useCallback(
    async (table: string) => {
      try {
        const resolved = await getPhoneProjectAccess(slugStr);
        setAccess(resolved);
        const res = await browsePhoneTable(slugStr, table, "", 100, resolved);
        setRows(res?.rows ?? []);
      } catch (e) {
        setErr(e instanceof Error ? e.message : String(e));
      }
    },
    [slugStr],
  );

  useEffect(() => {
    (async () => {
      try {
        const resolved = await getPhoneProjectAccess(slugStr);
        const p = await getPhoneProject(slugStr, resolved);
        setProject(p);
        const t = tablesFor(p?.schema, p?.app);
        if (t.length) {
          setActive(t[0]);
          await loadRows(t[0]);
        }
      } catch (e) {
        setErr(e instanceof Error ? e.message : String(e));
      } finally {
        setLoading(false);
      }
    })();
  }, [slugStr, loadRows]);

  // Mini-figma design layer authored on web/sandbox — rides in app.yaml, so the
  // same overrides render here. Node ids honored on mobile: nav, title, list.
  const ui = project?.app?.design?.ui ?? {};
  const nodeUi = useCallback((id: string) => ui[id] ?? {}, [ui]);
  const blockOrder = useMemo(() => {
    const valid = ["nav", "title", "list"];
    const want = (project?.app?.design?.layout ?? []).filter((id) => valid.includes(id));
    for (const id of valid) if (!want.includes(id)) want.push(id);
    return want;
  }, [project]);

  const screenTitle = useMemo(() => {
    const s = (project?.app?.screens ?? []).find((x) => x.table === active);
    return nodeUi("title").title || s?.title || active || project?.name || "App";
  }, [project, active, nodeUi]);

  const listPk = useMemo(() => {
    const t = project?.schema?.tables?.find((x) => x.name === active);
    return t?.columns?.find((cc) => cc.primary)?.name || "id";
  }, [project, active]);
  const swipeOn = !!nodeUi("list").swipeDelete;
  const reorderOn = !!nodeUi("list").reorderable;
  const orderCol = useMemo(() => {
    const t = project?.schema?.tables?.find((x) => x.name === active);
    const cols = t?.columns ?? [];
    for (const cand of ["position", "order", "sort", "rank", "idx", "sort_order"]) {
      const found = cols.find((cc) => cc.name.toLowerCase() === cand);
      if (found) return found.name;
    }
    return null;
  }, [project, active]);

  const moveCard = useCallback(
    async (rowId: string, targetKey: string) => {
      if (!active || !rowId) return;
      const groupBy = nodeUi("list").board?.groupBy;
      if (!groupBy) return;
      try {
        await updatePhoneRow(slugStr, active, rowId, { [groupBy]: targetKey }, access);
        await loadRows(active);
      } catch {
        /* read-only share or write failed — ignore */
      }
    },
    [active, access, slugStr, loadRows, nodeUi],
  );

  const persistReorder = useCallback(
    async (data: Array<Record<string, unknown>>) => {
      setRows(data);
      if (!orderCol || !active) return;
      for (let i = 0; i < data.length; i++) {
        const id = String(data[i][listPk] ?? "");
        if (id) {
          try {
            await updatePhoneRow(slugStr, active, id, { [orderCol]: i }, access);
          } catch {
            /* ignore */
          }
        }
      }
    },
    [orderCol, active, listPk, slugStr, access],
  );

  const boardGroupBy = nodeUi("list").board?.groupBy ?? null;
  const boardGroups = useMemo(() => {
    if (!boardGroupBy || !rows.length || !(boardGroupBy in rows[0])) return null;
    const groups: Record<string, Array<Record<string, unknown>>> = {};
    const order: string[] = [];
    for (const r of rows) {
      const v = r[boardGroupBy];
      const key = v === null || v === undefined || v === "" ? "—" : String(v);
      if (!groups[key]) { groups[key] = []; order.push(key); }
      groups[key].push(r);
    }
    return order.map((k) => ({ key: k, rows: groups[k] }));
  }, [boardGroupBy, rows]);

  if (loading) {
    return (
      <View style={[styles.center, { backgroundColor: c.bg }]}>
        <ActivityIndicator color={c.accent} />
      </View>
    );
  }

  const renderCardBody = (item: Record<string, unknown>) =>
    Object.entries(item).map(([k, v]) => (
      <View key={k} style={styles.kv}>
        <Text style={[styles.k, { color: c.textMuted }]}>{k}</Text>
        <Text style={[styles.v, { color: c.textPrimary }]} numberOfLines={3}>
          {v === null || v === undefined ? "—" : typeof v === "object" ? JSON.stringify(v) : String(v)}
        </Text>
      </View>
    ));

  const navBlock = nodeUi("nav").hidden ? null : (
    <ScrollView
      key="nav"
      horizontal
      showsHorizontalScrollIndicator={false}
      style={[styles.nav, { marginTop: nodeUi("nav").marginTop ?? 0 }]}
      contentContainerStyle={{ gap: 8, paddingHorizontal: 16 }}
    >
      {tables.map((t) => (
        <Pressable
          key={t}
          onPress={() => {
            setActive(t);
            void loadRows(t);
          }}
          style={[styles.chip, { borderColor: c.border, backgroundColor: active === t ? c.accent : "transparent" }]}
        >
          <Text style={{ color: active === t ? "#fff" : c.textMuted, fontSize: 12 }}>{t}</Text>
        </Pressable>
      ))}
    </ScrollView>
  );

  const titleBlock = nodeUi("title").hidden ? null : (
    <Text key="title" style={[styles.screenTitle, { color: c.textPrimary, marginHorizontal: 16, marginTop: nodeUi("title").marginTop ?? 8 }]}>
      {screenTitle}
    </Text>
  );

  const listMargin = nodeUi("list").marginTop ?? 0;
  const listBlock = nodeUi("list").hidden ? null : err ? (
    <Text key="list" style={{ color: "#ef4444", padding: 16 }}>
      {err}
    </Text>
  ) : boardGroups ? (
    // Kanban: drag a card into another column to change its group-by value.
    <KanbanBoard
      key="list"
      groups={boardGroups}
      pk={listPk}
      enabled
      onMove={moveCard}
      renderBody={renderCardBody}
      c={c}
      marginTop={listMargin}
    />
  ) : reorderOn ? (
    // End-user drag-to-reorder. Persists to an order column when the schema has
    // one (position/order/sort/rank/idx); otherwise it reorders for the session.
    <DraggableFlatList
      key="list"
      data={rows}
      style={{ flex: 1, marginTop: listMargin }}
      keyExtractor={(item, i) => String(item[listPk] ?? i)}
      containerStyle={{ flex: 1 }}
      contentContainerStyle={{ padding: 16, gap: 10 }}
      onDragEnd={({ data }) => void persistReorder(data)}
      ListEmptyComponent={<Text style={{ color: c.textMuted }}>No rows yet.</Text>}
      renderItem={({ item, drag, isActive }) => (
        <ScaleDecorator>
          <Pressable
            onLongPress={drag}
            disabled={isActive}
            style={[styles.card, { borderColor: c.border, backgroundColor: isActive ? c.bgCard : c.bgCard }]}
          >
            {renderCardBody(item)}
          </Pressable>
        </ScaleDecorator>
      )}
    />
  ) : (
    <FlatList
      key="list"
      data={rows}
      style={{ flex: 1, marginTop: listMargin }}
      keyExtractor={(_, i) => String(i)}
      contentContainerStyle={{ padding: 16, gap: 10 }}
      ListEmptyComponent={<Text style={{ color: c.textMuted }}>No rows yet.</Text>}
      renderItem={({ item }) => {
        const id = String(item[listPk] ?? "");
        return (
          <SwipeableCard
            enabled={swipeOn && !!id}
            style={[styles.card, { borderColor: c.border, backgroundColor: c.bgCard }]}
            onDelete={async () => {
              if (!id || !active) return;
              await deletePhoneRow(slugStr, active, id, access);
              await loadRows(active);
            }}
          >
            {renderCardBody(item)}
          </SwipeableCard>
        );
      }}
    />
  );

  const blocks: Record<string, React.ReactNode> = { nav: navBlock, title: titleBlock, list: listBlock };

  return (
    <GestureHandlerRootView style={{ flex: 1, backgroundColor: c.bg, paddingTop: insets.top }}>
      <View style={styles.header}>
        <AppBackButton onPress={() => router.back()} />
        <View style={{ flex: 1 }}>
          <Text style={[styles.title, { color: c.textPrimary }]}>{project?.name || slugStr}</Text>
          <Text style={[styles.sub, { color: c.textMuted }]}>
            {boardGroups
              ? "Running on Yaver Serverless · drag cards between columns"
              : reorderOn
                ? "Running on Yaver Serverless · long-press to reorder"
                : swipeOn
                  ? "Running on Yaver Serverless · swipe a card to delete"
                  : "Running on Yaver Serverless · read-only"}
          </Text>
        </View>
      </View>
      {blockOrder.map((id) => blocks[id])}
    </GestureHandlerRootView>
  );
}

const styles = StyleSheet.create({
  center: { flex: 1, alignItems: "center", justifyContent: "center" },
  header: { flexDirection: "row", alignItems: "center", gap: 8, paddingHorizontal: 12, paddingVertical: 10 },
  title: { fontSize: 18, fontWeight: "600" },
  sub: { fontSize: 12 },
  nav: { maxHeight: 44, marginBottom: 4 },
  chip: { borderWidth: 1, borderRadius: 999, paddingHorizontal: 12, paddingVertical: 6 },
  screenTitle: { fontSize: 16, fontWeight: "600", marginBottom: 6 },
  card: { borderWidth: 1, borderRadius: 10, padding: 12, gap: 6 },
  bcol: { width: 220, borderWidth: 1, borderRadius: 10, padding: 8, gap: 8 },
  bcolh: { fontSize: 11, fontWeight: "600", textTransform: "uppercase" },
  kv: { flexDirection: "row", gap: 8 },
  k: { fontSize: 12, width: 96 },
  v: { fontSize: 13, flex: 1 },
});
