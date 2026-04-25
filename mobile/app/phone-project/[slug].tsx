import React, { useCallback, useEffect, useMemo, useState } from "react";
import {
  ActivityIndicator,
  Alert,
  FlatList,
  Linking,
  Platform,
  Pressable,
  RefreshControl,
  ScrollView,
  StyleSheet,
  Text,
  TextInput,
  View,
} from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { useLocalSearchParams, useRouter } from "expo-router";
import { useColors } from "../../src/context/ThemeContext";
import { useDevice, type Device } from "../../src/context/DeviceContext";
import { AppBackButton } from "../../src/components/AppBackButton";
import { quicClient } from "../../src/lib/quic";
import {
  PhoneBrowseResult,
  PhoneDeployCostHint,
  PhoneDeployCostHints,
  PhoneProject,
  PhoneProjectAccess,
  PhonePushResult,
  PhonePushTarget,
  bindPhoneProjectToTarget,
  browsePhoneTable,
  clearPhoneProjectBinding,
  deletePhoneRow,
  getPhoneProject,
  getPhoneProjectAccess,
  insertPhoneRow,
  listPhoneTablesAt,
  phoneBundleSize,
  phoneDeployCostHints,
  pushPhoneProject,
} from "../../src/lib/phoneProjects";

function pickDevMachines(all: Device[], currentId: string | undefined): Device[] {
  return all.filter(
    (d) =>
      d.online &&
      !d.isGuest &&
      !d.needsAuth &&
      d.id !== currentId &&
      d.deviceClass !== "edge-mobile",
  );
}

export default function PhoneProjectDetailScreen() {
  const c = useColors();
  const insets = useSafeAreaInsets();
  const router = useRouter();
  const { slug } = useLocalSearchParams<{ slug: string }>();
  const slugStr = String(slug ?? "");

  const [project, setProject] = useState<PhoneProject | null>(null);
  const [access, setAccess] = useState<PhoneProjectAccess | null>(null);
  const [tables, setTables] = useState<Array<{ name: string; rowCount?: number }>>([]);
  const [selectedTable, setSelectedTable] = useState<string | null>(null);
  const [rows, setRows] = useState<PhoneBrowseResult["rows"]>([]);
  const [loading, setLoading] = useState(true);
  const [refreshing, setRefreshing] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const [showInsert, setShowInsert] = useState(false);
  const [insertJSON, setInsertJSON] = useState("{}");

  const { devices, activeDevice } = useDevice();
  const devMachines = useMemo(
    () => pickDevMachines(devices, activeDevice?.id),
    [devices, activeDevice?.id],
  );
  const [selectedDevMachineId, setSelectedDevMachineId] = useState<string | null>(null);
  const [deploying, setDeploying] = useState(false);
  const [lastDeploy, setLastDeploy] = useState<{ url: string; via?: string } | null>(null);
  const [costHints, setCostHints] = useState<PhoneDeployCostHints | null>(null);

  useEffect(() => {
    void (async () => {
      const hints = await phoneDeployCostHints();
      if (hints) setCostHints(hints);
    })();
  }, []);

  useEffect(() => {
    if (!selectedDevMachineId && devMachines.length) {
      setSelectedDevMachineId(devMachines[0].id);
    }
  }, [devMachines, selectedDevMachineId]);

  const selectedDevMachine = useMemo(
    () => devMachines.find((d) => d.id === selectedDevMachineId) ?? null,
    [devMachines, selectedDevMachineId],
  );

  const load = useCallback(async () => {
    if (!slugStr) return;
    try {
      setErr(null);
      const resolved = await getPhoneProjectAccess(slugStr);
      setAccess(resolved);
      const [p, ts] = await Promise.all([
        getPhoneProject(slugStr, resolved),
        listPhoneTablesAt(slugStr, resolved),
      ]);
      setProject(p);
      setTables(ts);
      if (!selectedTable && ts.length) setSelectedTable(ts[0].name);
    } catch (e: any) {
      setErr(e?.message ?? "failed to load");
    } finally {
      setLoading(false);
      setRefreshing(false);
    }
  }, [selectedTable, slugStr]);

  const loadRows = useCallback(async () => {
    if (!slugStr || !selectedTable) return;
    try {
      const resolved = access ?? (await getPhoneProjectAccess(slugStr));
      if (!access) setAccess(resolved);
      const result = await browsePhoneTable(slugStr, selectedTable, "", 50, resolved);
      setRows(result?.rows ?? []);
    } catch (e: any) {
      setErr(e?.message ?? "browse failed");
    }
  }, [access, selectedTable, slugStr]);

  useEffect(() => {
    void load();
  }, [load]);

  useEffect(() => {
    void loadRows();
  }, [loadRows]);

  async function doInsert() {
    if (!slugStr || !selectedTable) return;
    try {
      const doc = JSON.parse(insertJSON || "{}");
      if (!doc || typeof doc !== "object") throw new Error("JSON must be an object");
      await insertPhoneRow(slugStr, selectedTable, doc, access);
      setInsertJSON("{}");
      setShowInsert(false);
      await loadRows();
    } catch (e: any) {
      Alert.alert("Insert failed", e?.message ?? "invalid JSON");
    }
  }

  async function doDeleteRow(id: unknown) {
    if (!slugStr || !selectedTable || !id) return;
    Alert.alert("Delete row?", String(id), [
      { text: "Cancel", style: "cancel" },
      {
        text: "Delete",
        style: "destructive",
        onPress: async () => {
          await deletePhoneRow(slugStr, selectedTable, String(id), access);
          await loadRows();
        },
      },
    ]);
  }

  function costHintFor(kind: "dev-hw"): PhoneDeployCostHint | null {
    if (!costHints) return null;
    return costHints.hints.find((hint) => hint.targetKind === kind) ?? null;
  }

  async function confirmDeploySize(): Promise<boolean> {
    const bytes = await phoneBundleSize(slugStr, { includeData: true });
    const capMB = costHints?.bundleCapMB ?? 50;
    const mb = bytes ? (bytes / (1024 * 1024)).toFixed(2) : "?";
    const hint = costHintFor("dev-hw");
    const sizeLine = bytes
      ? `Uploading ~${mb} MB (cap: ${capMB} MB).`
      : `Bundle size unknown — cap: ${capMB} MB.`;
    if (bytes && bytes > (costHints?.bundleCapBytes ?? 50 * 1024 * 1024)) {
      Alert.alert(
        "Bundle too large",
        `${mb} MB exceeds the ${capMB} MB cap. Deploy without --include-data, or drop some rows first.`,
      );
      return false;
    }
    const advice = hint?.advice ?? "";
    const budget = hint?.free ?? "";
    const message = [sizeLine, "", budget ? `Plan: ${budget}` : "", advice]
      .filter(Boolean)
      .join("\n");
    return new Promise<boolean>((resolve) => {
      Alert.alert(
        `Ship to ${selectedDevMachine?.name ?? "your dev machine"}?`,
        message,
        [
          { text: "Cancel", style: "cancel", onPress: () => resolve(false) },
          { text: "Ship", style: "default", onPress: () => resolve(true) },
        ],
        { cancelable: true, onDismiss: () => resolve(false) },
      );
    });
  }

  async function deployToDevMachine() {
    if (!slugStr) return;
    if (!selectedDevMachine) {
      if (devMachines.length === 0) {
        Alert.alert(
          "No dev machine paired",
          "Install Yaver on your Mac/Linux/Pi and sign in with the same account. It'll appear here.",
        );
        return;
      }
      if (devMachines.length > 1) {
        pickDevMachine();
        return;
      }
    }
    const target = selectedDevMachine ?? devMachines[0];
    if (!target) return;
    const relayHttpUrl = quicClient.activeRelayHttpUrl;
    if (!relayHttpUrl) {
      Alert.alert(
        "No relay in use",
        "Your phone is connected directly to this device. Switch to a relay-routed connection to ship to a different machine.",
      );
      return;
    }
    const ok = await confirmDeploySize();
    if (!ok) return;

    setDeploying(true);
    try {
      const pushTarget: PhonePushTarget = {
        kind: "dev-hw",
        deviceId: target.id,
        relayHttpUrl,
      };
      const result: PhonePushResult = await pushPhoneProject(slugStr, pushTarget, {
        onConflict: "overwrite",
        includeData: true,
        containerize: true,
      });
      const via = target.name || "dev machine";
      await bindPhoneProjectToTarget(slugStr, pushTarget, result, via);
      const rebound = await getPhoneProjectAccess(slugStr);
      setAccess(rebound);
      const url = result.browseUrl?.startsWith("http")
        ? result.browseUrl
        : deriveTargetUrl(pushTarget, result);
      setLastDeploy({ url, via });
      await load();
      await loadRows();
    } catch (e: any) {
      Alert.alert("Ship failed", e?.message ?? "push failed");
    } finally {
      setDeploying(false);
    }
  }

  function pickDevMachine() {
    if (devMachines.length === 0) {
      Alert.alert(
        "No dev machines online",
        "Install Yaver on your Mac/Linux/Pi and sign in with the same account.",
      );
      return;
    }
    Alert.alert("Pick a dev machine", "Choose the target for this deploy.", [
      ...devMachines.map((device) => ({
        text: `${device.name}${device.local ? " (LAN)" : ""}`,
        onPress: () => setSelectedDevMachineId(device.id),
      })),
      { text: "Cancel", style: "cancel" as const },
    ]);
  }

  async function useLocalBackend() {
    await clearPhoneProjectBinding(slugStr);
    const localAccess: PhoneProjectAccess = {
      sourceSlug: slugStr,
      slug: slugStr,
      kind: "local",
      label: "This phone",
    };
    setAccess(localAccess);
    await load();
    await loadRows();
  }

  function openScreenshotsTask() {
    if (!project?.dir) {
      Alert.alert(
        "Screenshots unavailable",
        "Ship this project to your dev machine first, then run screenshots from there.",
      );
      return;
    }
    router.navigate({
      pathname: "/(tabs)/tasks" as any,
      params: {
        dir: project.dir,
        prompt: `Generate App Store and Google Play screenshots for ${project.name}. Capture the key flows, save them into a screenshots/ folder in the project, and report the generated files.`,
        title: `Screenshots ${project.name}`,
        openNew: "1",
      },
    });
  }

  const rowIdKey = useMemo(() => {
    if (!rows.length) return "id";
    return Object.keys(rows[0]).find((key) => key === "id") ?? Object.keys(rows[0])[0];
  }, [rows]);

  if (loading) {
    return (
      <View style={[styles.empty, { backgroundColor: c.bg }]}>
        <ActivityIndicator color={c.textMuted} />
      </View>
    );
  }

  if (!project) {
    return (
      <View style={[styles.empty, { backgroundColor: c.bg }]}>
        <Text style={{ color: c.textMuted }}>{err ?? "Project not found"}</Text>
        <AppBackButton onPress={() => router.back()} style={{ marginTop: 12 }} />
      </View>
    );
  }

  return (
    <ScrollView
      style={{ backgroundColor: c.bg }}
      contentContainerStyle={{ paddingTop: insets.top + 8, paddingBottom: 60 + insets.bottom }}
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
        <Text style={[styles.h1, { color: c.textPrimary }]}>{project.name}</Text>
        <Text style={{ color: c.textMuted, fontSize: 12, marginTop: 2 }}>
          {project.slug} · {project.template ?? "custom"}
        </Text>
        {project.stats ? (
          <Text style={{ color: c.textMuted, fontSize: 12, marginTop: 8 }}>
            {project.stats.tableCount} table{project.stats.tableCount === 1 ? "" : "s"} ·{" "}
            {project.stats.rowCount} row{project.stats.rowCount === 1 ? "" : "s"} ·{" "}
            {formatBytes(project.stats.dbBytes)} on disk
          </Text>
        ) : null}

        <View
          style={[
            styles.deployResult,
            {
              backgroundColor: c.bgCard,
              borderColor: access?.kind === "local" ? c.border : c.accent,
              marginTop: 10,
            },
          ]}
        >
          <Text
            style={{
              color: access?.kind === "local" ? c.textPrimary : c.accent,
              fontSize: 12,
              fontWeight: "600",
            }}
          >
            Active backend: {access?.label ?? "This phone"}
          </Text>
          <Text style={{ color: c.textMuted, fontSize: 11, marginTop: 2 }}>
            {access?.kind === "local"
              ? "Reads and writes stay in the local phone sandbox."
              : access?.kind === "current-agent"
                ? `Reads and writes go through ${access?.label}.`
                : `Reads and writes are rebound to ${access?.label}.`}
          </Text>
          {access?.kind !== "local" ? (
            <Pressable onPress={useLocalBackend} style={{ marginTop: 8 }}>
              <Text style={{ color: c.accent, fontSize: 12, fontWeight: "600" }}>
                Use this phone again
              </Text>
            </Pressable>
          ) : null}
        </View>

        <View style={{ flexDirection: "row", gap: 8, marginTop: 12 }}>
          <Pressable
            onPress={deployToDevMachine}
            disabled={deploying}
            style={[styles.btn, { backgroundColor: c.accent, flex: 1, opacity: deploying ? 0.7 : 1 }]}
          >
            <Text style={{ color: c.bg, fontWeight: "700" }}>
              {deploying ? "Shipping..." : "Ship it"}
            </Text>
          </Pressable>
          <Pressable
            onPress={openScreenshotsTask}
            style={[styles.btnSecondary, { borderColor: c.border, flex: 1 }]}
          >
            <Text style={[styles.btnText, { color: c.textPrimary }]}>Screenshots</Text>
          </Pressable>
        </View>
      </View>

      {project.app ? (
        <>
          <Text style={[styles.section, { color: c.textPrimary }]}>App plan</Text>
          <View style={{ paddingHorizontal: 16 }}>
            <View style={[styles.card, { backgroundColor: c.bgCard, borderColor: c.border }]}>
              {project.app.summary ? (
                <Text style={{ color: c.textPrimary, fontSize: 14, lineHeight: 20 }}>
                  {project.app.summary}
                </Text>
              ) : null}
              {project.app.primaryEntity ? (
                <Text style={{ color: c.textMuted, fontSize: 12, marginTop: 6 }}>
                  Primary entity: {project.app.primaryEntity}
                </Text>
              ) : null}
              {(project.app.screens ?? []).map((screen) => (
                <View key={screen.id} style={styles.appScreen}>
                  <Text style={{ color: c.textPrimary, fontSize: 14, fontWeight: "600" }}>
                    {screen.title}
                  </Text>
                  <Text style={{ color: c.textMuted, fontSize: 12, marginTop: 2 }}>
                    {screen.kind}
                    {screen.table ? ` · ${screen.table}` : ""}
                  </Text>
                  {screen.emptyState ? (
                    <Text style={{ color: c.textMuted, fontSize: 12, marginTop: 4 }}>
                      Empty: {screen.emptyState}
                    </Text>
                  ) : null}
                  {(screen.actions ?? []).length ? (
                    <Text style={{ color: c.textMuted, fontSize: 12, marginTop: 6 }}>
                      Actions:{" "}
                      {(screen.actions ?? [])
                        .map((action) => action.label)
                        .filter(Boolean)
                        .join(" · ")}
                    </Text>
                  ) : null}
                </View>
              ))}
            </View>
          </View>
        </>
      ) : null}

      <Text style={[styles.section, { color: c.textPrimary }]}>Tables</Text>
      <ScrollView horizontal showsHorizontalScrollIndicator={false} contentContainerStyle={{ paddingHorizontal: 16 }}>
        {tables.map((table) => (
          <Pressable
            key={table.name}
            onPress={() => setSelectedTable(table.name)}
            style={[
              styles.chip,
              {
                backgroundColor: selectedTable === table.name ? c.accent : c.bgCard,
                borderColor: c.border,
              },
            ]}
          >
            <Text style={{ color: selectedTable === table.name ? c.bg : c.textPrimary, fontWeight: "500" }}>
              {table.name}
            </Text>
            {typeof table.rowCount === "number" ? (
              <Text style={{ color: selectedTable === table.name ? c.bg : c.textMuted, fontSize: 11, marginLeft: 4 }}>
                {table.rowCount}
              </Text>
            ) : null}
          </Pressable>
        ))}
      </ScrollView>

      {selectedTable ? (
        <View style={{ paddingHorizontal: 16, marginTop: 8 }}>
          <View style={{ flexDirection: "row", justifyContent: "space-between", alignItems: "center" }}>
            <Text style={{ color: c.textMuted, fontSize: 13 }}>
              {rows.length} row{rows.length === 1 ? "" : "s"}
            </Text>
            <Pressable
              onPress={() => setShowInsert((value) => !value)}
              style={[styles.btnSecondary, { borderColor: c.border, paddingHorizontal: 12 }]}
            >
              <Text style={{ color: c.textPrimary, fontWeight: "500" }}>
                {showInsert ? "Cancel" : "+ Insert"}
              </Text>
            </Pressable>
          </View>

          {showInsert ? (
            <View style={[styles.card, { backgroundColor: c.bgCard, borderColor: c.border, marginTop: 8 }]}>
              <Text style={{ color: c.textMuted, fontSize: 12, marginBottom: 4 }}>Row JSON</Text>
              <TextInput
                value={insertJSON}
                onChangeText={setInsertJSON}
                multiline
                style={[styles.codeInput, { color: c.textPrimary, borderColor: c.border }]}
                autoCapitalize="none"
                autoCorrect={false}
                placeholder='{"id":"x","name":"hello"}'
                placeholderTextColor={c.textMuted}
              />
              <Pressable
                onPress={doInsert}
                style={[styles.btn, { backgroundColor: c.accent, marginTop: 8 }]}
              >
                <Text style={{ color: c.bg, fontWeight: "600" }}>Insert row</Text>
              </Pressable>
            </View>
          ) : null}

          <FlatList
            scrollEnabled={false}
            data={rows}
            keyExtractor={(item, index) => String(item[rowIdKey] ?? index)}
            ItemSeparatorComponent={() => <View style={{ height: 6 }} />}
            renderItem={({ item }) => (
              <Pressable
                onLongPress={() => doDeleteRow(item[rowIdKey])}
                style={[styles.card, { backgroundColor: c.bgCard, borderColor: c.border }]}
              >
                {Object.entries(item).map(([key, value]) => (
                  <View key={key} style={styles.kv}>
                    <Text style={[styles.k, { color: c.textMuted }]}>{key}</Text>
                    <Text style={[styles.v, { color: c.textPrimary }]} numberOfLines={2}>
                      {formatValue(value)}
                    </Text>
                  </View>
                ))}
              </Pressable>
            )}
            ListEmptyComponent={
              <Text style={{ color: c.textMuted, textAlign: "center", marginTop: 16 }}>
                No rows yet. Tap + Insert to add one.
              </Text>
            }
            style={{ marginTop: 8 }}
          />
        </View>
      ) : null}

      <Text style={[styles.section, { color: c.textPrimary }]}>Ship it</Text>
      <View style={{ paddingHorizontal: 16 }}>
        <Text style={{ color: c.textMuted, fontSize: 13, marginBottom: 12 }}>
          Push this project to your dev machine.
        </Text>
        <Pressable
          onPress={deployToDevMachine}
          onLongPress={pickDevMachine}
          disabled={deploying}
          style={[
            styles.deployCard,
            {
              backgroundColor: c.accent,
              borderColor: c.accent,
              opacity: deploying ? 0.7 : 1,
            },
          ]}
        >
          <View style={{ flex: 1 }}>
            <Text style={[styles.deployLabel, { color: c.bg }]}>Ship to your dev machine</Text>
            <Text style={[styles.deploySub, { color: c.bg, opacity: 0.8 }]}>
              {selectedDevMachine
                ? `→ ${selectedDevMachine.name}${selectedDevMachine.local ? " · LAN" : " · via relay"}`
                : "No dev machine online. Long-press to pick one."}
            </Text>
            {devMachines.length > 1 ? (
              <Text style={[styles.deploySub, { color: c.bg, opacity: 0.6, fontSize: 11 }]}>
                Long-press to switch target ({devMachines.length} available)
              </Text>
            ) : null}
          </View>
          {deploying ? (
            <ActivityIndicator color={c.bg} />
          ) : (
            <Text style={[styles.deployArrow, { color: c.bg }]}>→</Text>
          )}
        </Pressable>

        {lastDeploy ? (
          <Pressable
            onPress={() => Linking.openURL(lastDeploy.url).catch(() => undefined)}
            style={[
              styles.deployResult,
              { backgroundColor: c.bgCard, borderColor: c.success ?? "#22c55e" },
            ]}
          >
            <Text style={{ color: c.success ?? "#22c55e", fontSize: 12, fontWeight: "600" }}>
              ✓ Running on {lastDeploy.via ?? "dev machine"}
            </Text>
            <Text style={{ color: c.textMuted, fontSize: 11, marginTop: 2 }} numberOfLines={1}>
              {lastDeploy.url}
            </Text>
          </Pressable>
        ) : null}
      </View>
    </ScrollView>
  );
}

function deriveTargetUrl(target: Extract<PhonePushTarget, { kind: "dev-hw" }>, result: PhonePushResult): string {
  const slug = encodeURIComponent(result.slug);
  return `${target.relayHttpUrl.replace(/\/$/, "")}/d/${target.deviceId}/phone/projects/browse?slug=${slug}`;
}

function formatValue(value: unknown): string {
  if (value === null || value === undefined) return "—";
  if (typeof value === "object") return JSON.stringify(value);
  return String(value);
}

function formatBytes(bytes: number): string {
  if (!bytes) return "0 B";
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  return `${(bytes / 1024 / 1024).toFixed(1)} MB`;
}

const styles = StyleSheet.create({
  h1: { fontSize: 24, fontWeight: "700" },
  section: {
    fontSize: 13,
    fontWeight: "600",
    marginTop: 20,
    marginBottom: 8,
    paddingHorizontal: 16,
    textTransform: "uppercase",
    letterSpacing: 0.5,
  },
  chip: {
    paddingHorizontal: 12,
    paddingVertical: 8,
    borderRadius: 16,
    borderWidth: 1,
    marginRight: 6,
    flexDirection: "row",
    alignItems: "center",
  },
  card: { borderWidth: 1, borderRadius: 10, padding: 12 },
  appScreen: {
    marginTop: 12,
    paddingTop: 12,
    borderTopWidth: StyleSheet.hairlineWidth,
    borderTopColor: "rgba(127,127,127,0.25)",
  },
  kv: { marginBottom: 4 },
  k: { fontSize: 11, textTransform: "uppercase", letterSpacing: 0.5 },
  v: { fontSize: 14 },
  codeInput: {
    borderWidth: 1,
    borderRadius: 8,
    padding: 10,
    fontFamily: Platform.select({ ios: "Menlo", android: "monospace", default: "monospace" }),
    minHeight: 90,
    fontSize: 13,
    textAlignVertical: "top",
  },
  btn: { paddingVertical: 12, borderRadius: 8, alignItems: "center" },
  btnSecondary: { paddingVertical: 10, borderRadius: 8, alignItems: "center", borderWidth: 1 },
  btnText: { fontWeight: "600", fontSize: 14 },
  deployCard: {
    flexDirection: "row",
    alignItems: "center",
    borderWidth: 2,
    borderRadius: 12,
    paddingVertical: 14,
    paddingHorizontal: 14,
  },
  deployLabel: { fontSize: 17, fontWeight: "700" },
  deploySub: { fontSize: 12, marginTop: 2 },
  deployArrow: { fontSize: 22, fontWeight: "700", marginLeft: 12 },
  deployResult: {
    borderWidth: 1,
    borderRadius: 8,
    padding: 10,
    marginTop: 10,
  },
  empty: { flex: 1, alignItems: "center", justifyContent: "center" },
});
