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
  Share,
  StyleSheet,
  Text,
  TextInput,
  View,
} from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { useLocalSearchParams, useRouter } from "expo-router";
import { useColors } from "../../src/context/ThemeContext";
import { useDevice, type Device } from "../../src/context/DeviceContext";
import { quicClient } from "../../src/lib/quic";
import {
  EscapeRoute,
  PhoneProjectAccess,
  PhoneBrowseResult,
  PhoneProject,
  PhonePushResult,
  PhonePushTarget,
  PhoneDeployCostHint,
  PhoneDeployCostHints,
  bindPhoneProjectToTarget,
  browsePhoneTable,
  clearPhoneProjectBinding,
  deletePhoneProject,
  deletePhoneRow,
  getPhoneProjectAccess,
  getPhoneProject,
  insertPhoneRow,
  listEscapeRoutes,
  listPhoneTablesAt,
  phoneBundleSize,
  phoneDeployCostHints,
  phoneExportUrl,
  promotePhoneProject,
  pushPhoneProject,
} from "../../src/lib/phoneProjects";

// Advanced escape-route rows are fetched live from /escape/routes so the
// curated catalog in desktop/agent/phone_escape.go is the single source of
// truth. Positioning (per user): trust signal, not headline — shown behind
// the existing "Advanced" collapsible, never in the primary Deploy surface.

const YAVER_CLOUD_BASE = "https://cloud.yaver.io";

// A "dev machine" target is an owned, online Yaver-running device that is
// not the currently-connected source (the phone itself). This is the yc.md
// [Your Dev Machine] button's candidate pool.
function pickDevMachines(all: Device[], currentId: string | undefined): Device[] {
  return all.filter(
    (d) =>
      d.online &&
      !d.isGuest &&
      !d.needsAuth &&
      d.id !== currentId &&
      // Filter out mobile-only devices — they can't accept /phone/projects/receive
      // uploads (they don't run `yaver serve` on a reachable port).
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
  const [promoting, setPromoting] = useState<string | null>(null);

  // Deploy state (yc.md §Wedge Demo)
  const { devices, activeDevice } = useDevice();
  const devMachines = useMemo(
    () => pickDevMachines(devices, activeDevice?.id),
    [devices, activeDevice?.id],
  );
  const [selectedDevMachineId, setSelectedDevMachineId] = useState<string | null>(null);
  const [deploying, setDeploying] = useState<"dev-hw" | "yaver-cloud" | null>(null);
  const [lastDeploy, setLastDeploy] = useState<{ kind: "dev-hw" | "yaver-cloud"; url: string; via?: string } | null>(null);
  const [showAdvancedPromote, setShowAdvancedPromote] = useState(false);
  const [escapeRoutes, setEscapeRoutes] = useState<EscapeRoute[]>([]);
  const [costHints, setCostHints] = useState<PhoneDeployCostHints | null>(null);

  // Pull the agent's advisory cost map + bundle-cap once on mount. Used by
  // the deploy confirm to show "About to upload X.Y MB — <advice>" before
  // the user taps OK. Keeps the user from a surprise data-plan hit.
  useEffect(() => {
    void (async () => {
      const h = await phoneDeployCostHints();
      if (h) setCostHints(h);
    })();
  }, []);

  // Pull curated escape routes once the user opens the Advanced collapsible.
  // Phone projects are SQLite-backed, so we ask for "yaver"-origin routes
  // plus cross-family inbound routes (Convex/Supabase → Yaver) that explain
  // the continuum to a potential migrator.
  useEffect(() => {
    if (!showAdvancedPromote || escapeRoutes.length) return;
    void (async () => {
      const outbound = await listEscapeRoutes({ from: "yaver" });
      const inbound = await listEscapeRoutes({ to: "yaver-cloud" });
      // De-dupe by id; outbound first (this is primarily what the user can
      // actually execute from a phone project — the "switch to X" story).
      const seen = new Set<string>();
      const merged: EscapeRoute[] = [];
      for (const r of [...outbound, ...inbound]) {
        if (!seen.has(r.id)) {
          seen.add(r.id);
          merged.push(r);
        }
      }
      setEscapeRoutes(merged);
    })();
  }, [showAdvancedPromote, escapeRoutes.length]);

  // Auto-select the first dev machine so the primary button is a one-tap action.
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
  }, [slugStr, selectedTable]);

  const loadRows = useCallback(async () => {
    if (!slugStr || !selectedTable) return;
    try {
      const resolved = access ?? (await getPhoneProjectAccess(slugStr));
      if (!access) setAccess(resolved);
      const r = await browsePhoneTable(slugStr, selectedTable, "", 50, resolved);
      setRows(r?.rows ?? []);
    } catch (e: any) {
      setErr(e?.message ?? "browse failed");
    }
  }, [access, slugStr, selectedTable]);

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

  async function doExport() {
    const ref = phoneExportUrl(slugStr, access);
    if (!ref) {
      Alert.alert("Export", "Agent not reachable");
      return;
    }
    // Share the URL — the user's system share sheet can save it, hand it to a
    // HTTP client, etc. The agent streams tgz bytes with proper headers.
    try {
      await Share.share({
        message: `Yaver phone-project export:\n${ref.uri}\n\n(Authenticated URL — paste into curl with X-Relay-Password / Authorization headers.)`,
        url: ref.uri,
      });
    } catch (e: any) {
      Alert.alert("Export", e?.message ?? "share failed");
    }
  }

  async function doPromote(targetID: string, label: string) {
    if (!slugStr) return;
    Alert.alert(
      `Plan migration to ${label}?`,
      "This produces a step-by-step switch plan with a 7-day rollback window. You can run it or keep it as a dry-run.",
      [
        { text: "Cancel", style: "cancel" },
        {
          text: "Dry run",
          onPress: async () => {
            setPromoting(targetID);
            try {
              const r = await promotePhoneProject(slugStr, targetID, { dryRun: true, run: true });
              if (r?.error) Alert.alert("Promote", r.error);
              else Alert.alert("Plan saved", `Switch plan ${r?.state?.id ?? ""} created. Review in Switch history.`);
            } catch (e: any) {
              Alert.alert("Promote", e?.message ?? "failed");
            } finally {
              setPromoting(null);
            }
          },
        },
        {
          text: "Plan",
          onPress: async () => {
            setPromoting(targetID);
            try {
              const r = await promotePhoneProject(slugStr, targetID, {});
              if (r?.error) Alert.alert("Promote", r.error);
              else Alert.alert("Plan saved", `Complexity: ${r?.state?.complexity}. Run it from Switch history when ready.`);
            } catch (e: any) {
              Alert.alert("Promote", e?.message ?? "failed");
            } finally {
              setPromoting(null);
            }
          },
        },
      ],
    );
  }

  // ── Deploy (yc.md §Wedge Demo) ──────────────────────────────────────

  // Pre-flight guard for runPush — fetches the bundle size + cost hint and
  // asks the user to confirm BEFORE any bytes hit the wire. Keeps a
  // surprise Hetzner/Cloudflare/Convex bill off the table. Server side
  // enforces the cap again in handlePhoneReceive so this is transparency
  // not security.
  function costHintFor(kind: "dev-hw" | "yaver-cloud"): PhoneDeployCostHint | null {
    if (!costHints) return null;
    return costHints.hints.find((h) => h.targetKind === kind) ?? null;
  }

  async function confirmDeploySize(target: PhonePushTarget, kind: "dev-hw" | "yaver-cloud"): Promise<boolean> {
    const bytes = await phoneBundleSize(slugStr, { includeData: true });
    const capMB = costHints?.bundleCapMB ?? 50;
    const mb = bytes ? (bytes / (1024 * 1024)).toFixed(2) : "?";
    const hint = costHintFor(kind);
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
    const label = kind === "dev-hw" ? selectedDevMachine?.name ?? "your dev machine" : "Yaver Cloud";
    const advice = hint?.advice ?? "";
    const budget = hint?.free ?? "";
    const message = [sizeLine, "", budget ? `Plan: ${budget}` : "", advice].filter(Boolean).join("\n");
    return new Promise<boolean>((resolve) => {
      Alert.alert(
        `Deploy to ${label}?`,
        message,
        [
          { text: "Cancel", style: "cancel", onPress: () => resolve(false) },
          { text: "Deploy", style: "default", onPress: () => resolve(true) },
        ],
        { cancelable: true, onDismiss: () => resolve(false) },
      );
    });
  }

  async function runPush(target: PhonePushTarget, kindLabel: "dev-hw" | "yaver-cloud") {
    const ok = await confirmDeploySize(target, kindLabel);
    if (!ok) return;
    setDeploying(kindLabel);
    try {
      const result: PhonePushResult = await pushPhoneProject(slugStr, target, {
        onConflict: "overwrite",
        includeData: true,
      });
      const via =
        target.kind === "dev-hw" ? selectedDevMachine?.name ?? "dev machine" : "Yaver Cloud";
      await bindPhoneProjectToTarget(slugStr, target, result, via);
      const rebound = await getPhoneProjectAccess(slugStr);
      setAccess(rebound);
      const url = result.browseUrl?.startsWith("http")
        ? result.browseUrl
        : deriveTargetUrl(target, result);
      setLastDeploy({ kind: kindLabel, url, via });
      await load();
      await loadRows();
    } catch (e: any) {
      // Agent returns a descriptive message on 413 — surface it verbatim.
      Alert.alert("Deploy failed", e?.message ?? "push failed");
    } finally {
      setDeploying(null);
    }
  }

  async function deployToDevMachine() {
    if (!slugStr) return;
    if (!selectedDevMachine) {
      Alert.alert(
        "No dev machine paired",
        "Install Yaver on your Mac/Linux/Pi and sign in with the same account. It'll appear here.",
      );
      return;
    }
    const relayHttpUrl = quicClient.activeRelayHttpUrl;
    if (!relayHttpUrl) {
      Alert.alert(
        "No relay in use",
        "Your phone is connected directly to this device. Switch to a relay-routed connection to deploy to a different machine, or use Yaver Cloud.",
      );
      return;
    }
    await runPush(
      { kind: "dev-hw", deviceId: selectedDevMachine.id, relayHttpUrl },
      "dev-hw",
    );
  }

  async function deployToCloud() {
    if (!slugStr) return;
    await runPush({ kind: "yaver-cloud", cloudBaseUrl: YAVER_CLOUD_BASE }, "yaver-cloud");
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
      ...devMachines.map((d) => ({
        text: `${d.name}${d.local ? " (LAN)" : ""}`,
        onPress: () => setSelectedDevMachineId(d.id),
      })),
      { text: "Cancel", style: "cancel" as const },
    ]);
  }

  async function doDelete() {
    Alert.alert("Delete project?", `This removes ${project?.name ?? slugStr} and its SQLite file.`, [
      { text: "Cancel", style: "cancel" },
      {
        text: "Delete",
        style: "destructive",
        onPress: async () => {
          await deletePhoneProject(slugStr, access);
          router.back();
        },
      },
    ]);
  }

  async function useLocalBackend() {
    await clearPhoneProjectBinding(slugStr);
    const localAccess: PhoneProjectAccess = {
      sourceSlug: slugStr,
      slug: slugStr,
      kind: "local",
      label: "This device",
    };
    setAccess(localAccess);
    await load();
    await loadRows();
  }

  const rowIdKey = useMemo(() => {
    if (!rows.length) return "id";
    return Object.keys(rows[0]).find((k) => k === "id") ?? Object.keys(rows[0])[0];
  }, [rows]);

  function openVibeCoding() {
    if (!project?.dir) {
      Alert.alert("Vibe coding", "Project directory is not available yet.");
      return;
    }
    const prompt = buildSandboxPrompt(project);
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

  if (!project) {
    return (
      <View style={[styles.empty, { backgroundColor: c.bg }]}>
        <Text style={{ color: c.textMuted }}>{err ?? "Project not found"}</Text>
        <Pressable onPress={() => router.back()} style={{ marginTop: 12 }}>
          <Text style={{ color: c.accent }}>Back</Text>
        </Pressable>
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
        <Pressable onPress={() => router.back()}>
          <Text style={{ color: c.accent, marginBottom: 8 }}>‹ Back</Text>
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
            Active backend: {access?.label ?? "This device"}
          </Text>
          <Text style={{ color: c.textMuted, fontSize: 11, marginTop: 2 }}>
            {access?.kind === "local"
              ? "Reads and writes stay on the currently connected agent."
              : `Reads and writes are rebound to ${access?.label}.`}
          </Text>
          {access?.kind !== "local" ? (
            <Pressable onPress={useLocalBackend} style={{ marginTop: 8 }}>
              <Text style={{ color: c.accent, fontSize: 12, fontWeight: "600" }}>
                Use local backend again
              </Text>
            </Pressable>
          ) : null}
        </View>

        <View style={{ flexDirection: "row", gap: 8, marginTop: 12 }}>
          <Pressable
            onPress={() => router.navigate(`/phone-project/run/${slugStr}` as any)}
            style={[styles.btn, { backgroundColor: c.accent, flex: 1 }]}
          >
            <Text style={{ color: c.bg, fontWeight: "700" }}>Open app</Text>
          </Pressable>
          <Pressable
            onPress={openVibeCoding}
            style={[styles.btnSecondary, { borderColor: c.border, flex: 1 }]}
          >
            <Text style={[styles.btnText, { color: c.textPrimary }]}>Vibe code</Text>
          </Pressable>
        </View>
        <View style={{ flexDirection: "row", gap: 8, marginTop: 8 }}>
          <Pressable
            onPress={doExport}
            style={[styles.btnSecondary, { borderColor: c.border, flex: 1 }]}
          >
            <Text style={[styles.btnText, { color: c.textPrimary }]}>Export .tgz</Text>
          </Pressable>
          <Pressable
            onPress={doDelete}
            style={[styles.btnSecondary, { borderColor: "#ff6b6b", flex: 1 }]}
          >
            <Text style={[styles.btnText, { color: "#ff6b6b" }]}>Delete</Text>
          </Pressable>
        </View>

        <Pressable
          onPress={() =>
            router.navigate({
              pathname: "/phone-project/oauth" as any,
              params: { slug: slugStr },
            })
          }
          style={[styles.btnSecondary, { borderColor: c.border, marginTop: 8 }]}
        >
          <Text style={[styles.btnText, { color: c.textPrimary }]}>
            OAuth providers (Apple · Google · Microsoft) ›
          </Text>
        </Pressable>

        <Pressable
          onPress={() =>
            router.navigate({
              pathname: "/phone-project/dns" as any,
              params: { slug: slugStr },
            })
          }
          style={[styles.btnSecondary, { borderColor: c.border, marginTop: 8 }]}
        >
          <Text style={[styles.btnText, { color: c.textPrimary }]}>
            Custom domain (Cloudflare DNS) ›
          </Text>
        </Pressable>
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
        {tables.map((t) => (
          <Pressable
            key={t.name}
            onPress={() => setSelectedTable(t.name)}
            style={[
              styles.chip,
              {
                backgroundColor: selectedTable === t.name ? c.accent : c.bgCard,
                borderColor: c.border,
              },
            ]}
          >
            <Text style={{ color: selectedTable === t.name ? c.bg : c.textPrimary, fontWeight: "500" }}>
              {t.name}
            </Text>
            {typeof t.rowCount === "number" ? (
              <Text style={{ color: selectedTable === t.name ? c.bg : c.textMuted, fontSize: 11, marginLeft: 4 }}>
                {t.rowCount}
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
              onPress={() => setShowInsert((v) => !v)}
              style={[styles.btnSecondary, { borderColor: c.border, paddingHorizontal: 12 }]}
            >
              <Text style={{ color: c.textPrimary, fontWeight: "500" }}>{showInsert ? "Cancel" : "+ Insert"}</Text>
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
            keyExtractor={(item, idx) => String(item[rowIdKey] ?? idx)}
            ItemSeparatorComponent={() => <View style={{ height: 6 }} />}
            renderItem={({ item }) => (
              <Pressable
                onLongPress={() => doDeleteRow(item[rowIdKey])}
                style={[styles.card, { backgroundColor: c.bgCard, borderColor: c.border }]}
              >
                {Object.entries(item).map(([k, v]) => (
                  <View key={k} style={styles.kv}>
                    <Text style={[styles.k, { color: c.textMuted }]}>{k}</Text>
                    <Text style={[styles.v, { color: c.textPrimary }]} numberOfLines={2}>
                      {formatValue(v)}
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

      <Text style={[styles.section, { color: c.textPrimary }]}>Deploy</Text>
      <View style={{ paddingHorizontal: 16 }}>
        <Text style={{ color: c.textMuted, fontSize: 13, marginBottom: 12 }}>
          Ship this mini-backend in one tap. Your dev machine is free; Yaver Cloud is
          a managed Hetzner tenant.
        </Text>

        {/* [Your Dev Machine] */}
        <Pressable
          onPress={deployToDevMachine}
          onLongPress={pickDevMachine}
          disabled={deploying !== null}
          style={[
            styles.deployCard,
            {
              backgroundColor: c.accent,
              borderColor: c.accent,
              opacity: deploying !== null && deploying !== "dev-hw" ? 0.5 : 1,
            },
          ]}
        >
          <View style={{ flex: 1 }}>
            <Text style={[styles.deployLabel, { color: c.bg }]}>Your Dev Machine</Text>
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
          {deploying === "dev-hw" ? (
            <ActivityIndicator color={c.bg} />
          ) : (
            <Text style={[styles.deployArrow, { color: c.bg }]}>→</Text>
          )}
        </Pressable>

        {/* [Yaver Cloud] */}
        <Pressable
          onPress={deployToCloud}
          disabled={deploying !== null}
          style={[
            styles.deployCard,
            {
              backgroundColor: c.bgCard,
              borderColor: c.accent,
              opacity: deploying !== null && deploying !== "yaver-cloud" ? 0.5 : 1,
              marginTop: 8,
            },
          ]}
        >
          <View style={{ flex: 1 }}>
            <Text style={[styles.deployLabel, { color: c.textPrimary }]}>Yaver Cloud</Text>
            <Text style={[styles.deploySub, { color: c.textMuted }]}>
              Managed — shareable URL at {YAVER_CLOUD_BASE.replace(/^https?:\/\//, "")}
            </Text>
          </View>
          {deploying === "yaver-cloud" ? (
            <ActivityIndicator color={c.accent} />
          ) : (
            <Text style={[styles.deployArrow, { color: c.accent }]}>→</Text>
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
              ✓ Running on {lastDeploy.via ?? lastDeploy.kind}
            </Text>
            <Text style={{ color: c.textMuted, fontSize: 11, marginTop: 2 }} numberOfLines={1}>
              {lastDeploy.url}
            </Text>
          </Pressable>
        ) : null}

        {/* Advanced: escape routes (trust signal — not the headline). The
            curated list is served by the agent so it stays in sync with the
            SwitchEngine target set. Phone projects are SQLite-backed, so we
            ask for "yaver"-origin rows (where the user can actually execute
            an escape today) plus a few highlight inbound cases for context. */}
        <Pressable
          onPress={() => setShowAdvancedPromote((v) => !v)}
          style={{ marginTop: 20, paddingVertical: 8 }}
        >
          <Text style={{ color: c.textMuted, fontSize: 12 }}>
            {showAdvancedPromote ? "▾" : "▸"} Advanced — escape to another backend (no lock-in)
          </Text>
        </Pressable>
        {showAdvancedPromote ? (
          <View>
            <Text style={{ color: c.textMuted, fontSize: 12, marginBottom: 8 }}>
              Same manifest, different backend. Plans go through the switch engine with a 7-day rollback snapshot.
            </Text>
            {escapeRoutes.length === 0 ? (
              <ActivityIndicator color={c.textMuted} style={{ marginTop: 4 }} />
            ) : (
              escapeRoutes.map((r) => {
                const tag = r.complexity || "";
                const tagColor =
                  tag === "hard"
                    ? "#f97316"
                    : tag === "medium"
                      ? "#eab308"
                      : tag === "easy"
                        ? c.accent
                        : c.textMuted;
                return (
                  <Pressable
                    key={r.id}
                    onPress={() => doPromote(r.toTargetId, r.label)}
                    disabled={promoting === r.toTargetId}
                    style={[styles.card, { backgroundColor: c.bgCard, borderColor: c.border, marginBottom: 8 }]}
                  >
                    <View style={{ flexDirection: "row", justifyContent: "space-between", alignItems: "center" }}>
                      <View style={{ flex: 1 }}>
                        <View style={{ flexDirection: "row", alignItems: "center", flexWrap: "wrap", gap: 6 }}>
                          <Text style={[styles.promoteLabel, { color: c.textPrimary }]}>{r.label}</Text>
                          {r.highlight ? (
                            <Text style={{ color: c.accent, fontSize: 10, fontWeight: "700" }}>· PITCH</Text>
                          ) : null}
                          {tag ? (
                            <Text style={{ color: tagColor, fontSize: 10, fontWeight: "600", textTransform: "uppercase" }}>
                              · {tag}
                            </Text>
                          ) : null}
                        </View>
                        <Text style={{ color: c.textMuted, fontSize: 12, marginTop: 2 }}>{r.blurb}</Text>
                      </View>
                      {promoting === r.toTargetId ? (
                        <ActivityIndicator color={c.accent} />
                      ) : (
                        <Text style={{ color: c.accent, fontSize: 18, marginLeft: 8 }}>›</Text>
                      )}
                    </View>
                  </Pressable>
                );
              })
            )}
          </View>
        ) : null}
      </View>
    </ScrollView>
  );
}

// deriveTargetUrl falls back to a sensible "view it on the target" URL when
// the target agent didn't return a ready-made one in PhonePushResult.
function deriveTargetUrl(target: PhonePushTarget, result: PhonePushResult): string {
  const slug = encodeURIComponent(result.slug);
  switch (target.kind) {
    case "dev-hw":
      return `${target.relayHttpUrl.replace(/\/$/, "")}/d/${target.deviceId}/phone/projects/browse?slug=${slug}`;
    case "yaver-cloud":
      return `${(target.cloudBaseUrl ?? YAVER_CLOUD_BASE).replace(/\/$/, "")}/phone/projects/browse?slug=${slug}`;
    case "custom":
      return `${target.baseUrl.replace(/\/$/, "")}/phone/projects/browse?slug=${slug}`;
  }
}

function formatValue(v: unknown): string {
  if (v === null || v === undefined) return "—";
  if (typeof v === "object") return JSON.stringify(v);
  return String(v);
}

function formatBytes(n: number): string {
  if (!n) return "0 B";
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`;
  return `${(n / 1024 / 1024).toFixed(1)} MB`;
}

function buildSandboxPrompt(project: PhoneProject): string {
  const primaryEntity = project.app?.primaryEntity || project.schema?.tables?.find((table) => table.name !== "users")?.name || "items";
  const screenTitles = (project.app?.screens ?? []).map((screen) => screen.title).join(", ");
  return [
    `You are vibe-coding inside Yaver's mobile sandbox for the project "${project.name}".`,
    `Keep the implementation mobile-first, SQLite-backed, and exportable to the user's hardware or Yaver Cloud later.`,
    `Primary entity: ${primaryEntity}.`,
    screenTitles ? `Current screens: ${screenTitles}.` : "",
    `Improve the sandbox app directly. Prefer concrete edits that make the app feel closer to a polished solo-dev mobile product.`,
    `After changes, explain what changed in the sandbox and what the next best prompt is.`,
  ]
    .filter(Boolean)
    .join("\n");
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
  promoteLabel: { fontWeight: "600", fontSize: 14 },
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
