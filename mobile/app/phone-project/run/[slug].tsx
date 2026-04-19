import React, { useCallback, useEffect, useMemo, useState } from "react";
import {
  ActivityIndicator,
  Alert,
  Pressable,
  RefreshControl,
  ScrollView,
  StyleSheet,
  Switch,
  Text,
  TextInput,
  View,
} from "react-native";
import { useLocalSearchParams, useRouter } from "expo-router";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { useColors } from "../../../src/context/ThemeContext";
import { AppBackButton } from "../../../src/components/AppBackButton";
import {
  PhoneColumn,
  PhonePersona,
  PhoneProject,
  PhoneProjectAccess,
  browsePhoneTable,
  deletePhoneRow,
  getPhoneProject,
  getPhoneProjectAccess,
  insertPhoneRow,
  updatePhoneRow,
} from "../../../src/lib/phoneProjects";

type DraftValue = string | boolean;

export default function PhoneProjectRuntimeScreen() {
  const c = useColors();
  const insets = useSafeAreaInsets();
  const router = useRouter();
  const { slug } = useLocalSearchParams<{ slug: string }>();
  const slugStr = String(slug ?? "");

  const [loading, setLoading] = useState(true);
  const [refreshing, setRefreshing] = useState(false);
  const [saving, setSaving] = useState(false);
  const [project, setProject] = useState<PhoneProject | null>(null);
  const [access, setAccess] = useState<PhoneProjectAccess | null>(null);
  const [rows, setRows] = useState<Array<Record<string, unknown>>>([]);
  const [selectedPersonaId, setSelectedPersonaId] = useState<string | null>(null);
  const [draft, setDraft] = useState<Record<string, DraftValue>>({});

  const primaryTable = useMemo(() => {
    if (project?.app?.primaryEntity) return project.app.primaryEntity;
    const appTable = project?.app?.screens?.find((screen) => screen.table)?.table;
    if (appTable) return appTable;
    const nonUsers = project?.schema?.tables?.find((table) => table.name !== "users")?.name;
    return nonUsers ?? project?.schema?.tables?.[0]?.name ?? null;
  }, [project]);

  const primaryScreen = useMemo(() => {
    if (!project?.app?.screens?.length || !primaryTable) return null;
    return (
      project.app.screens.find((screen) => screen.table === primaryTable) ??
      project.app.screens[0]
    );
  }, [project, primaryTable]);

  const primarySchema = useMemo(() => {
    if (!project?.schema?.tables?.length || !primaryTable) return null;
    return project.schema.tables.find((table) => table.name === primaryTable) ?? null;
  }, [project, primaryTable]);

  const personas = useMemo(() => project?.auth?.personas ?? [], [project]);

  const visibleRows = useMemo(() => {
    if (!rows.length) return rows;
    if (!selectedPersonaId) return rows;
    return rows.filter((row) => {
      if (row.owner_id !== undefined) return String(row.owner_id) === selectedPersonaId;
      if (row.user_id !== undefined) return String(row.user_id) === selectedPersonaId;
      return true;
    });
  }, [rows, selectedPersonaId]);

  const editableColumns = useMemo(() => {
    const cols = primarySchema?.columns ?? [];
    return cols.filter((column) => {
      if (column.primary) return false;
      if (column.name === "created_at" || column.name === "updated_at") return false;
      return true;
    });
  }, [primarySchema]);

  const buildInitialDraft = useCallback(
    (cols: PhoneColumn[], personaId: string | null) => {
      const next: Record<string, DraftValue> = {};
      cols.forEach((column) => {
        if (column.type === "bool") {
          next[column.name] = column.default === "true";
          return;
        }
        if (column.name === "owner_id" || column.name === "user_id") {
          next[column.name] = personaId ?? "";
          return;
        }
        next[column.name] = "";
      });
      return next;
    },
    [],
  );

  const load = useCallback(async () => {
    if (!slugStr) return;
    try {
      const resolved = await getPhoneProjectAccess(slugStr);
      setAccess(resolved);
      const p = await getPhoneProject(slugStr, resolved);
      setProject(p);
      const people = p?.auth?.personas ?? [];
      const defaultPersona = selectedPersonaId ?? people[0]?.id ?? null;
      setSelectedPersonaId(defaultPersona);
      const entity =
        p?.app?.primaryEntity ??
        p?.app?.screens?.find((screen) => screen.table)?.table ??
        p?.schema?.tables?.find((table) => table.name !== "users")?.name ??
        p?.schema?.tables?.[0]?.name;
      if (entity) {
        const res = await browsePhoneTable(slugStr, entity, "", 100, resolved);
        setRows(res?.rows ?? []);
      } else {
        setRows([]);
      }
      const schema =
        p?.schema?.tables?.find((table) => table.name === entity) ??
        p?.schema?.tables?.[0] ??
        null;
      setDraft(buildInitialDraft((schema?.columns ?? []).filter((column) => !column.primary), defaultPersona));
    } catch (e: any) {
      Alert.alert("Runtime", e?.message ?? "failed to load app");
    } finally {
      setLoading(false);
      setRefreshing(false);
    }
  }, [buildInitialDraft, selectedPersonaId, slugStr]);

  useEffect(() => {
    void load();
  }, [load]);

  useEffect(() => {
    if (!editableColumns.length) return;
    setDraft((current) => {
      const next = { ...current };
      editableColumns.forEach((column) => {
        if (column.name === "owner_id" || column.name === "user_id") {
          next[column.name] = selectedPersonaId ?? "";
        }
      });
      return next;
    });
  }, [editableColumns, selectedPersonaId]);

  async function refreshRows() {
    if (!primaryTable || !slugStr) return;
    const resolved = access ?? (await getPhoneProjectAccess(slugStr));
    const res = await browsePhoneTable(slugStr, primaryTable, "", 100, resolved);
    setRows(res?.rows ?? []);
  }

  async function createRow() {
    if (!primaryTable || !primarySchema) return;
    setSaving(true);
    try {
      const doc: Record<string, unknown> = {};
      editableColumns.forEach((column) => {
        const raw = draft[column.name];
        if (column.type === "bool") {
          doc[column.name] = !!raw;
          return;
        }
        const text = String(raw ?? "").trim();
        if (!text && !column.required) return;
        if (!text && column.required) {
          throw new Error(`${column.name} is required`);
        }
        if (column.type === "int") doc[column.name] = Number(text);
        else if (column.type === "real") doc[column.name] = Number(text);
        else doc[column.name] = text;
      });
      await insertPhoneRow(slugStr, primaryTable, doc, access);
      setDraft(buildInitialDraft(editableColumns, selectedPersonaId));
      await refreshRows();
    } catch (e: any) {
      Alert.alert("Create failed", e?.message ?? "failed to create row");
    } finally {
      setSaving(false);
    }
  }

  async function toggleDone(row: Record<string, unknown>) {
    if (!primaryTable || row.id === undefined || !hasBoolColumn(primarySchema?.columns ?? [], "done")) return;
    const current = normalizeBool(row.done);
    await updatePhoneRow(slugStr, primaryTable, String(row.id), { done: !current }, access);
    await refreshRows();
  }

  async function removeRow(row: Record<string, unknown>) {
    if (!primaryTable || row.id === undefined) return;
    Alert.alert("Delete item?", String(row.id), [
      { text: "Cancel", style: "cancel" },
      {
        text: "Delete",
        style: "destructive",
        onPress: async () => {
          await deletePhoneRow(slugStr, primaryTable, String(row.id), access);
          await refreshRows();
        },
      },
    ]);
  }

  function openVibeCoding() {
    if (!project?.dir) {
      Alert.alert(
        "Coding loop unavailable",
        "This phone sandbox runs locally in-app. Move it to a Yaver agent before opening the coding loop.",
      );
      return;
    }
    const prompt = [
      `We are iterating on the mobile sandbox app "${project.name}".`,
      `Focus on the in-app experience first. Keep SQLite as the local backend and preserve exportability to remote dev hardware later.`,
      `Primary entity: ${primaryTable}.`,
      primaryScreen?.title ? `Current runtime screen: ${primaryScreen.title}.` : "",
      `Make the next concrete improvement to the app and narrate progress through the task stream.`,
    ]
      .filter(Boolean)
      .join("\n");
    router.navigate({
      pathname: "/(tabs)/tasks" as any,
      params: {
        dir: project.dir,
        prompt,
        title: `Vibe ${project.name}`,
        openNew: "1",
      },
    });
  }

  if (loading) {
    return (
      <View style={[styles.empty, { backgroundColor: c.bg }]}>
        <ActivityIndicator color={c.textMuted} />
      </View>
    );
  }

  if (!project || !primaryTable || !primarySchema) {
    return (
      <View style={[styles.empty, { backgroundColor: c.bg, padding: 24 }]}>
        <Text style={{ color: c.textPrimary, fontSize: 16, fontWeight: "600" }}>
          App runtime unavailable
        </Text>
        <Text style={{ color: c.textMuted, marginTop: 6, textAlign: "center" }}>
          This project does not have enough schema/app metadata to render a runtime screen yet.
        </Text>
      </View>
    );
  }

  return (
    <ScrollView
      style={{ backgroundColor: c.bg }}
      contentContainerStyle={{ paddingTop: insets.top + 8, paddingBottom: insets.bottom + 40 }}
      refreshControl={
        <RefreshControl
          refreshing={refreshing}
          onRefresh={() => {
            setRefreshing(true);
            void load();
          }}
          tintColor={c.textMuted}
        />
      }
    >
      <View style={{ paddingHorizontal: 16 }}>
        <AppBackButton onPress={() => router.back()} style={{ marginBottom: 8 }} />
        <Pressable onPress={() => router.navigate(`/phone-project/workspace/${slugStr}` as any)}>
          <Text style={{ color: c.accent, marginBottom: 8 }}>Workspace ›</Text>
        </Pressable>
        <Text style={[styles.h1, { color: c.textPrimary }]}>
          {primaryScreen?.title ?? project.name}
        </Text>
        <Text style={{ color: c.textMuted, marginTop: 4 }}>
          {project.app?.summary ?? "Generated app runtime"}
        </Text>
        <View style={[styles.card, { backgroundColor: c.bgCard, borderColor: c.border, marginTop: 12 }]}>
          <Text style={{ color: c.textPrimary, fontWeight: "600" }}>
            Active backend: {access?.label ?? "This device"}
          </Text>
          <Text style={{ color: c.textMuted, fontSize: 12, marginTop: 4 }}>
            Table: {primaryTable} · {visibleRows.length} row{visibleRows.length === 1 ? "" : "s"}
          </Text>
          <Pressable onPress={openVibeCoding} style={[styles.ctaSecondary, { borderColor: c.border }]}>
            <Text style={{ color: c.textPrimary, fontWeight: "700" }}>Vibe code this app</Text>
          </Pressable>
        </View>

        {personas.length ? (
          <View style={{ marginTop: 16 }}>
            <Text style={[styles.sectionLabel, { color: c.textMuted }]}>Signed in as</Text>
            <ScrollView horizontal showsHorizontalScrollIndicator={false}>
              {personas.map((persona) => {
                const active = persona.id === selectedPersonaId;
                return (
                  <Pressable
                    key={persona.id}
                    onPress={() => setSelectedPersonaId(persona.id)}
                    style={[
                      styles.personaChip,
                      {
                        backgroundColor: active ? c.accent : c.bgCard,
                        borderColor: c.border,
                      },
                    ]}
                  >
                    <Text style={{ color: active ? c.bg : c.textPrimary, fontWeight: "600" }}>
                      {persona.name || persona.email}
                    </Text>
                  </Pressable>
                );
              })}
            </ScrollView>
          </View>
        ) : null}

        <View style={{ marginTop: 20 }}>
          <Text style={[styles.sectionLabel, { color: c.textMuted }]}>Create</Text>
          <View style={[styles.card, { backgroundColor: c.bgCard, borderColor: c.border }]}>
            {editableColumns.map((column) => (
              <View key={column.name} style={{ marginTop: 10 }}>
                <Text style={{ color: c.textMuted, fontSize: 12, marginBottom: 6 }}>
                  {column.name}
                </Text>
                {column.type === "bool" ? (
                  <View style={styles.switchRow}>
                    <Text style={{ color: c.textPrimary }}>Off / On</Text>
                    <Switch
                      value={Boolean(draft[column.name])}
                      onValueChange={(value) =>
                        setDraft((current) => ({ ...current, [column.name]: value }))
                      }
                    />
                  </View>
                ) : (
                  <TextInput
                    value={String(draft[column.name] ?? "")}
                    onChangeText={(value) =>
                      setDraft((current) => ({ ...current, [column.name]: value }))
                    }
                    editable={!(column.name === "owner_id" || column.name === "user_id")}
                    placeholder={column.required ? "Required" : "Optional"}
                    placeholderTextColor={c.textMuted}
                    style={[
                      styles.input,
                      {
                        color: c.textPrimary,
                        borderColor: c.border,
                        opacity:
                          column.name === "owner_id" || column.name === "user_id" ? 0.65 : 1,
                      },
                    ]}
                  />
                )}
              </View>
            ))}
            <Pressable
              onPress={createRow}
              disabled={saving}
              style={[
                styles.cta,
                { backgroundColor: c.accent, opacity: saving ? 0.7 : 1 },
              ]}
            >
              <Text style={{ color: c.bg, fontWeight: "700" }}>
                {saving ? "Saving…" : primaryScreen?.actions?.[0]?.label ?? "Create item"}
              </Text>
            </Pressable>
          </View>
        </View>

        <View style={{ marginTop: 20 }}>
          <Text style={[styles.sectionLabel, { color: c.textMuted }]}>
            {primaryScreen?.title ?? primaryTable}
          </Text>
          {visibleRows.length === 0 ? (
            <View style={[styles.card, { backgroundColor: c.bgCard, borderColor: c.border }]}>
              <Text style={{ color: c.textMuted }}>
                {primaryScreen?.emptyState ?? "No items yet."}
              </Text>
            </View>
          ) : (
            visibleRows.map((row, index) => (
              <Pressable
                key={String(row.id ?? index)}
                onLongPress={() => removeRow(row)}
                style={[styles.card, { backgroundColor: c.bgCard, borderColor: c.border, marginTop: 8 }]}
              >
                <View style={styles.rowHeader}>
                  <View style={{ flex: 1 }}>
                    <Text style={{ color: c.textPrimary, fontSize: 16, fontWeight: "600" }}>
                      {String(pickPrimaryLabel(row))}
                    </Text>
                    <Text style={{ color: c.textMuted, fontSize: 12, marginTop: 2 }}>
                      {summarizeRow(row)}
                    </Text>
                  </View>
                  {hasBoolColumn(primarySchema.columns, "done") ? (
                    <Switch
                      value={normalizeBool(row.done)}
                      onValueChange={() => void toggleDone(row)}
                    />
                  ) : null}
                </View>
              </Pressable>
            ))
          )}
        </View>
      </View>
    </ScrollView>
  );
}

function hasBoolColumn(columns: PhoneColumn[], name: string): boolean {
  return columns.some((column) => column.name === name && column.type === "bool");
}

function normalizeBool(value: unknown): boolean {
  if (typeof value === "boolean") return value;
  if (typeof value === "number") return value !== 0;
  if (typeof value === "string") return value === "true" || value === "1";
  return false;
}

function pickPrimaryLabel(row: Record<string, unknown>): unknown {
  return row.title ?? row.name ?? row.email ?? row.id ?? "Untitled";
}

function summarizeRow(row: Record<string, unknown>): string {
  const keys = Object.keys(row).filter((key) => !["id", "title", "name"].includes(key));
  return keys
    .slice(0, 3)
    .map((key) => `${key}: ${String(row[key] ?? "—")}`)
    .join(" · ");
}

const styles = StyleSheet.create({
  empty: { flex: 1, alignItems: "center", justifyContent: "center" },
  h1: { fontSize: 28, fontWeight: "700" },
  sectionLabel: {
    fontSize: 12,
    fontWeight: "700",
    textTransform: "uppercase",
    letterSpacing: 0.5,
    marginBottom: 8,
  },
  card: {
    borderWidth: 1,
    borderRadius: 14,
    padding: 14,
  },
  personaChip: {
    paddingHorizontal: 12,
    paddingVertical: 8,
    borderRadius: 999,
    borderWidth: 1,
    marginRight: 8,
  },
  input: {
    borderWidth: 1,
    borderRadius: 10,
    paddingHorizontal: 12,
    paddingVertical: 10,
    fontSize: 15,
  },
  cta: {
    marginTop: 16,
    borderRadius: 12,
    paddingVertical: 14,
    alignItems: "center",
  },
  ctaSecondary: {
    marginTop: 12,
    borderWidth: 1,
    borderRadius: 12,
    paddingVertical: 12,
    alignItems: "center",
  },
  switchRow: {
    flexDirection: "row",
    alignItems: "center",
    justifyContent: "space-between",
  },
  rowHeader: {
    flexDirection: "row",
    alignItems: "center",
    gap: 12,
  },
});
