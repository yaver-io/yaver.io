import React, { useCallback, useEffect, useMemo, useState } from "react";
import {
  ActivityIndicator,
  Alert,
  Modal,
  Pressable,
  RefreshControl,
  ScrollView,
  StyleSheet,
  Text,
  TextInput,
  View,
} from "react-native";
import { SafeAreaView, useSafeAreaInsets } from "react-native-safe-area-context";
import { useRouter } from "expo-router";
import { Ionicons } from "@expo/vector-icons";
import { useColors } from "../../src/context/ThemeContext";
import { useDevice } from "../../src/context/DeviceContext";
import { useAuth } from "../../src/context/AuthContext";
import { quicClient } from "../../src/lib/quic";
import {
  listShortcuts,
  saveShortcut,
  deleteShortcut,
  describeStep,
  type Shortcut,
  type ShortcutStep,
  type ShortcutStepKind,
} from "../../src/lib/shortcuts";
import { runShortcut, type StepPhase } from "../../src/lib/runShortcut";

// Mobile Shortcuts tab — one-tap, chainable action shortcuts (connect →
// open → reload, Talos-style). Storage is Convex-synced (userShortcuts);
// the chain runs client-side via runShortcut.ts. Creation is seeded with
// common-case templates plus a step editor for custom chains.

const STEP_KINDS: { kind: ShortcutStepKind; label: string; icon: keyof typeof Ionicons.glyphMap; needsDevice: boolean; needsProject: boolean }[] = [
  { kind: "select-device", label: "Connect to device", icon: "desktop-outline", needsDevice: true, needsProject: false },
  { kind: "open-project", label: "Open project on phone", icon: "play-circle-outline", needsDevice: true, needsProject: true },
  { kind: "start-dev", label: "Start dev server", icon: "rocket-outline", needsDevice: true, needsProject: true },
  { kind: "hermes-reload", label: "Hermes reload", icon: "flash-outline", needsDevice: true, needsProject: true },
];

const CARD_COLORS = ["#6366f1", "#0ea5e9", "#10b981", "#f59e0b", "#ef4444", "#a855f7"];

type RunState = { id: string; steps: Record<number, StepPhase>; error?: string } | null;

export default function ShortcutsScreen() {
  const c = useColors();
  const insets = useSafeAreaInsets();
  const router = useRouter();
  const { token } = useAuth();
  const { devices, activeDevice, selectDevice, connectionStatus, setPrimaryRunnerForDevice } = useDevice();
  const connected = connectionStatus === "connected";

  const [shortcuts, setShortcuts] = useState<Shortcut[]>([]);
  const [loading, setLoading] = useState(true);
  const [refreshing, setRefreshing] = useState(false);
  const [run, setRun] = useState<RunState>(null);

  // Editor state
  const [editorOpen, setEditorOpen] = useState(false);
  const [editingId, setEditingId] = useState<string | undefined>(undefined);
  const [draftName, setDraftName] = useState("");
  const [draftColor, setDraftColor] = useState(CARD_COLORS[0]);
  const [draftSteps, setDraftSteps] = useState<ShortcutStep[]>([]);
  const [saving, setSaving] = useState(false);
  const [projects, setProjects] = useState<string[]>([]);
  const [runners, setRunners] = useState<{ id: string; name: string; models: { id: string; name: string }[] }[]>([]);
  const [templatePickerOpen, setTemplatePickerOpen] = useState(false);

  // Flattened agent·model options for the runner picker. "Off" (value "")
  // leaves the device's current agent alone. Each combo carries the runner
  // id + model id we persist on the step (flags only — privacy-safe).
  const runnerOptions = useMemo(() => {
    const opts: { value: string; label: string; runner: string; model: string }[] = [
      { value: "", label: "Off", runner: "", model: "" },
    ];
    for (const r of runners) {
      const models = r.models?.length ? r.models : [{ id: "", name: "" }];
      for (const m of models) {
        opts.push({
          value: `${r.id}:${m.id}`,
          label: m.name ? `${r.name} · ${m.name}` : r.name,
          runner: r.id,
          model: m.id,
        });
      }
    }
    return opts;
  }, [runners]);

  const load = useCallback(async () => {
    if (!token) { setShortcuts([]); setLoading(false); return; }
    const rows = await listShortcuts(token);
    rows.sort((a, b) => (a.order ?? 0) - (b.order ?? 0));
    setShortcuts(rows);
    setLoading(false);
  }, [token]);

  useEffect(() => { load(); }, [load]);

  const onRefresh = useCallback(async () => {
    setRefreshing(true);
    await load();
    setRefreshing(false);
  }, [load]);

  // Pull the connected box's project slugs for the editor's project
  // picker. Best-effort; the editor also allows typing a slug.
  const loadProjects = useCallback(async () => {
    if (!connected) return;
    try {
      const list = await quicClient.listProjects();
      setProjects(list.map((p) => p.name).filter(Boolean));
    } catch { /* leave manual entry */ }
  }, [connected]);

  // Pull the connected box's installed agents + their models for the
  // runner picker. Best-effort; empty → only "Off" is offered.
  const loadRunners = useCallback(async () => {
    if (!connected) return;
    try {
      const list = await quicClient.getRunners();
      setRunners(
        list.map((r) => ({
          id: r.id,
          name: r.name,
          models: (r.models || []).map((m: any) => ({ id: m.id, name: m.name })),
        })),
      );
    } catch { /* leave empty → Off only */ }
  }, [connected]);

  // ── Run a shortcut ─────────────────────────────────────────────────
  const handleRun = useCallback(async (sc: Shortcut) => {
    if (run) return; // one at a time
    setRun({ id: sc._id, steps: {} });
    try {
      await runShortcut(sc, {
        connectDevice: async (deviceId) => {
          const d = devices.find((x) => x.id === deviceId);
          if (!d) throw new Error("device not in your list (offline?)");
          await selectDevice(d);
        },
        openProjectsTab: () => router.push("/(tabs)/apps"),
        setAgent: async (deviceId, runner, model) => {
          try { await setPrimaryRunnerForDevice(deviceId, runner, model || ""); } catch { /* best-effort preset */ }
        },
        onProgress: (i, phase) => {
          setRun((cur) => (cur && cur.id === sc._id ? { ...cur, steps: { ...cur.steps, [i]: phase } } : cur));
        },
      });
      // Brief success dwell, then clear.
      setTimeout(() => setRun((cur) => (cur && cur.id === sc._id ? null : cur)), 1400);
    } catch (e) {
      const msg = e instanceof Error ? e.message : String(e);
      setRun((cur) => (cur && cur.id === sc._id ? { ...cur, error: msg } : cur));
      Alert.alert(sc.name, msg);
      setTimeout(() => setRun((cur) => (cur && cur.id === sc._id ? null : cur)), 200);
    }
  }, [run, devices, selectDevice, router, setPrimaryRunnerForDevice]);

  // ── Editor ─────────────────────────────────────────────────────────
  const openEditor = useCallback((sc?: Shortcut) => {
    setEditingId(sc?._id);
    setDraftName(sc?.name ?? "");
    setDraftColor(sc?.color ?? CARD_COLORS[shortcuts.length % CARD_COLORS.length]);
    setDraftSteps(sc?.steps ? JSON.parse(JSON.stringify(sc.steps)) : []);
    setEditorOpen(true);
    loadProjects();
    loadRunners();
  }, [shortcuts.length, loadProjects, loadRunners]);

  const applyTemplate = useCallback((tpl: "reload" | "open" | "startReload" | "full") => {
    const dId = activeDevice?.id;
    const dName = activeDevice?.name;
    const proj = projects[0] || "";
    const dev: ShortcutStep = { kind: "select-device", deviceId: dId, deviceName: dName };
    const steps: Record<typeof tpl, ShortcutStep[]> = {
      reload: [dev, { kind: "hermes-reload", deviceId: dId, mode: "bundle" }],
      open: [dev, { kind: "open-project", deviceId: dId, projectSlug: proj }],
      startReload: [dev, { kind: "start-dev", deviceId: dId, projectSlug: proj }, { kind: "hermes-reload", deviceId: dId, mode: "bundle" }],
      full: [dev, { kind: "open-project", deviceId: dId, projectSlug: proj }, { kind: "hermes-reload", deviceId: dId, mode: "bundle" }],
    };
    const names: Record<typeof tpl, string> = {
      reload: `Reload on ${dName || "device"}`,
      open: `Open ${proj || "project"}`,
      startReload: `Start + reload ${proj || "project"}`,
      full: `Open + reload ${proj || "project"}`,
    };
    setEditingId(undefined);
    setDraftName(names[tpl]);
    setDraftColor(CARD_COLORS[shortcuts.length % CARD_COLORS.length]);
    setDraftSteps(steps[tpl]);
    setTemplatePickerOpen(false);
    setEditorOpen(true);
    loadProjects();
    loadRunners();
  }, [activeDevice, projects, shortcuts.length, loadProjects, loadRunners]);

  const handleSave = useCallback(async () => {
    if (!token) return;
    if (!draftName.trim()) { Alert.alert("Name required", "Give your shortcut a name."); return; }
    if (draftSteps.length === 0) { Alert.alert("No steps", "Add at least one step."); return; }
    setSaving(true);
    try {
      await saveShortcut(token, {
        id: editingId,
        name: draftName.trim(),
        color: draftColor,
        steps: draftSteps,
      });
      setEditorOpen(false);
      await load();
    } catch (e) {
      Alert.alert("Couldn't save", e instanceof Error ? e.message : String(e));
    } finally {
      setSaving(false);
    }
  }, [token, draftName, draftColor, draftSteps, editingId, load]);

  const handleDelete = useCallback((sc: Shortcut) => {
    Alert.alert("Delete shortcut", `Delete "${sc.name}"?`, [
      { text: "Cancel", style: "cancel" },
      {
        text: "Delete",
        style: "destructive",
        onPress: async () => {
          if (token) await deleteShortcut(token, sc._id);
          await load();
        },
      },
    ]);
  }, [token, load]);

  // ── Step editing helpers ───────────────────────────────────────────
  const addStep = (kind: ShortcutStepKind) => {
    const spec = STEP_KINDS.find((k) => k.kind === kind)!;
    const next: ShortcutStep = { kind };
    if (spec.needsDevice) { next.deviceId = activeDevice?.id; next.deviceName = activeDevice?.name; }
    if (spec.needsProject) next.projectSlug = projects[0] || "";
    if (kind === "hermes-reload") next.mode = "bundle";
    setDraftSteps((s) => [...s, next]);
  };
  const updateStep = (i: number, patch: Partial<ShortcutStep>) =>
    setDraftSteps((s) => s.map((st, idx) => (idx === i ? { ...st, ...patch } : st)));
  const removeStep = (i: number) => setDraftSteps((s) => s.filter((_, idx) => idx !== i));
  const moveStep = (i: number, dir: -1 | 1) =>
    setDraftSteps((s) => {
      const j = i + dir;
      if (j < 0 || j >= s.length) return s;
      const copy = s.slice();
      [copy[i], copy[j]] = [copy[j], copy[i]];
      return copy;
    });

  // ── Render ─────────────────────────────────────────────────────────
  return (
    <SafeAreaView style={[styles.safe, { backgroundColor: c.bg }]} edges={["bottom"]}>
      <ScrollView
        contentContainerStyle={{ padding: 16, paddingBottom: 40 }}
        refreshControl={<RefreshControl refreshing={refreshing} onRefresh={onRefresh} tintColor={c.accent} />}
      >
        <View style={styles.headerRow}>
          <Text style={[styles.subtle, { color: c.textMuted }]}>
            One-tap chains — connect, open a project, push a reload.
          </Text>
        </View>

        {!connected && (
          <View style={[styles.notice, { backgroundColor: c.bgCard, borderColor: c.border }]}>
            <Ionicons name="information-circle-outline" size={18} color={c.textMuted} />
            <Text style={{ color: c.textMuted, flex: 1, marginLeft: 8, fontSize: 13 }}>
              Not connected to a machine. You can still create shortcuts; connect a device to run them.
            </Text>
          </View>
        )}

        {loading ? (
          <ActivityIndicator color={c.accent} style={{ marginTop: 40 }} />
        ) : shortcuts.length === 0 ? (
          <View style={styles.empty}>
            <Ionicons name="flash-outline" size={40} color={c.textMuted} />
            <Text style={{ color: c.textPrimary, fontWeight: "600", marginTop: 12, fontSize: 16 }}>No shortcuts yet</Text>
            <Text style={{ color: c.textMuted, marginTop: 6, textAlign: "center", fontSize: 13 }}>
              Create a one-tap shortcut to reload your app on a connected dev machine.
            </Text>
          </View>
        ) : (
          shortcuts.map((sc) => {
            const isRunning = run?.id === sc._id;
            return (
              <Pressable
                key={sc._id}
                onPress={() => handleRun(sc)}
                onLongPress={() => openEditor(sc)}
                disabled={!!run && !isRunning}
                style={({ pressed }) => [
                  styles.card,
                  { backgroundColor: c.bgCard, borderColor: c.border },
                  pressed && { opacity: 0.85 },
                ]}
              >
                <View style={[styles.cardIcon, { backgroundColor: (sc.color || c.accent) + "22" }]}>
                  <Ionicons name="flash" size={22} color={sc.color || c.accent} />
                </View>
                <View style={{ flex: 1 }}>
                  <Text style={{ color: c.textPrimary, fontWeight: "700", fontSize: 15 }}>{sc.name}</Text>
                  <Text style={{ color: c.textMuted, fontSize: 12, marginTop: 2 }} numberOfLines={1}>
                    {sc.steps.map(describeStep).join(" → ")}
                  </Text>
                  {isRunning && (
                    <View style={{ marginTop: 8, gap: 3 }}>
                      {sc.steps.map((st, i) => {
                        const phase = run?.steps[i];
                        const color = phase === "ok" ? "#10b981" : phase === "fail" ? c.error : phase === "running" ? c.accent : c.textMuted;
                        const glyph = phase === "ok" ? "checkmark-circle" : phase === "fail" ? "close-circle" : phase === "running" ? "ellipse" : "ellipse-outline";
                        return (
                          <View key={i} style={{ flexDirection: "row", alignItems: "center", gap: 6 }}>
                            <Ionicons name={glyph as keyof typeof Ionicons.glyphMap} size={13} color={color} />
                            <Text style={{ color, fontSize: 12 }}>{describeStep(st)}</Text>
                          </View>
                        );
                      })}
                    </View>
                  )}
                </View>
                {isRunning ? (
                  <ActivityIndicator color={sc.color || c.accent} />
                ) : (
                  <Pressable hitSlop={10} onPress={() => openEditor(sc)} style={{ padding: 4 }}>
                    <Ionicons name="ellipsis-vertical" size={18} color={c.textMuted} />
                  </Pressable>
                )}
              </Pressable>
            );
          })
        )}

        <Pressable
          onPress={() => setTemplatePickerOpen(true)}
          style={({ pressed }) => [styles.newButton, { backgroundColor: c.brandPrimary }, pressed && { opacity: 0.85 }]}
        >
          <Ionicons name="add" size={22} color="#fff" />
          <Text style={{ color: "#fff", fontWeight: "700", marginLeft: 6 }}>New shortcut</Text>
        </Pressable>
      </ScrollView>

      {/* Template chooser */}
      <Modal visible={templatePickerOpen} transparent animationType="slide" onRequestClose={() => setTemplatePickerOpen(false)}>
        <Pressable style={styles.sheetBackdrop} onPress={() => setTemplatePickerOpen(false)}>
          <Pressable style={[styles.sheet, { backgroundColor: c.bg }]} onPress={() => {}}>
            <Text style={[styles.sheetTitle, { color: c.textPrimary }]}>Start from a template</Text>
            {([
              { key: "reload", label: "Reload on this device", sub: "Connect → Hermes reload", icon: "flash-outline" },
              { key: "startReload", label: "Start dev + reload", sub: "Connect → start dev server → reload", icon: "rocket-outline" },
              { key: "open", label: "Open project on phone", sub: "Connect → load project", icon: "play-circle-outline" },
              { key: "full", label: "Open + reload", sub: "Connect → open → reload", icon: "albums-outline" },
            ] as const).map((t) => (
              <Pressable
                key={t.key}
                onPress={() => applyTemplate(t.key)}
                style={({ pressed }) => [styles.templateRow, { borderColor: c.border }, pressed && { backgroundColor: c.bgCard }]}
              >
                <Ionicons name={t.icon} size={22} color={c.accent} />
                <View style={{ flex: 1, marginLeft: 12 }}>
                  <Text style={{ color: c.textPrimary, fontWeight: "600" }}>{t.label}</Text>
                  <Text style={{ color: c.textMuted, fontSize: 12, marginTop: 1 }}>{t.sub}</Text>
                </View>
                <Ionicons name="chevron-forward" size={18} color={c.textMuted} />
              </Pressable>
            ))}
            <Pressable
              onPress={() => { setTemplatePickerOpen(false); openEditor(); }}
              style={({ pressed }) => [styles.templateRow, { borderColor: c.border }, pressed && { backgroundColor: c.bgCard }]}
            >
              <Ionicons name="construct-outline" size={22} color={c.textMuted} />
              <View style={{ flex: 1, marginLeft: 12 }}>
                <Text style={{ color: c.textPrimary, fontWeight: "600" }}>Custom (blank)</Text>
                <Text style={{ color: c.textMuted, fontSize: 12, marginTop: 1 }}>Build the chain step by step</Text>
              </View>
              <Ionicons name="chevron-forward" size={18} color={c.textMuted} />
            </Pressable>
          </Pressable>
        </Pressable>
      </Modal>

      {/* Editor */}
      <Modal visible={editorOpen} animationType="slide" onRequestClose={() => setEditorOpen(false)}>
        <View style={{ flex: 1, backgroundColor: c.bg }}>
          <View style={[styles.editorHeader, { borderBottomColor: c.border, paddingTop: insets.top + 12 }]}>
            <Pressable onPress={() => setEditorOpen(false)} hitSlop={10}>
              <Text style={{ color: c.textMuted, fontSize: 16 }}>Cancel</Text>
            </Pressable>
            <Text style={{ color: c.textPrimary, fontWeight: "700", fontSize: 16 }}>
              {editingId ? "Edit shortcut" : "New shortcut"}
            </Text>
            <Pressable onPress={handleSave} disabled={saving} hitSlop={10}>
              {saving ? <ActivityIndicator color={c.accent} /> : <Text style={{ color: c.accent, fontSize: 16, fontWeight: "700" }}>Save</Text>}
            </Pressable>
          </View>
          <ScrollView contentContainerStyle={{ padding: 16, paddingBottom: 60 + insets.bottom }}>
            <Text style={[styles.fieldLabel, { color: c.textMuted }]}>NAME</Text>
            <TextInput
              value={draftName}
              onChangeText={setDraftName}
              placeholder="e.g. Reload SFMG"
              placeholderTextColor={c.textMuted}
              style={[styles.input, { backgroundColor: c.bgCard, color: c.textPrimary, borderColor: c.border }]}
            />

            <Text style={[styles.fieldLabel, { color: c.textMuted, marginTop: 16 }]}>COLOR</Text>
            <View style={{ flexDirection: "row", gap: 10, marginTop: 6 }}>
              {CARD_COLORS.map((col) => (
                <Pressable key={col} onPress={() => setDraftColor(col)} style={[styles.swatch, { backgroundColor: col, borderColor: draftColor === col ? c.textPrimary : "transparent" }]} />
              ))}
            </View>

            <Text style={[styles.fieldLabel, { color: c.textMuted, marginTop: 20 }]}>STEPS</Text>
            {draftSteps.map((st, i) => (
              <StepEditor
                key={i}
                step={st}
                index={i}
                total={draftSteps.length}
                devices={devices.map((d) => ({ id: d.id, name: d.name }))}
                projects={projects}
                runnerOptions={runnerOptions}
                connected={connected}
                onConnect={async () => {
                  // One-tap connect-guide: connect this phone to the step's
                  // device (or the active one) so the project + runner lists
                  // populate, then reload them.
                  const target = devices.find((d) => d.id === st.deviceId) || activeDevice;
                  if (!target) { Alert.alert("No device", "Add a device to this step first."); return; }
                  try {
                    await selectDevice(target as any);
                    await loadProjects();
                    await loadRunners();
                  } catch (e) {
                    Alert.alert("Couldn't connect", e instanceof Error ? e.message : String(e));
                  }
                }}
                onChange={(patch) => updateStep(i, patch)}
                onRemove={() => removeStep(i)}
                onMove={(dir) => moveStep(i, dir)}
              />
            ))}

            <View style={{ marginTop: 12, gap: 8 }}>
              {STEP_KINDS.map((k) => (
                <Pressable
                  key={k.kind}
                  onPress={() => addStep(k.kind)}
                  style={({ pressed }) => [styles.addStepBtn, { borderColor: c.border }, pressed && { backgroundColor: c.bgCard }]}
                >
                  <Ionicons name={k.icon} size={18} color={c.accent} />
                  <Text style={{ color: c.textPrimary, marginLeft: 10 }}>{k.label}</Text>
                  <Ionicons name="add" size={18} color={c.textMuted} style={{ marginLeft: "auto" }} />
                </Pressable>
              ))}
            </View>

            {editingId && (
              <Pressable
                onPress={() => { const sc = shortcuts.find((s) => s._id === editingId); if (sc) { setEditorOpen(false); handleDelete(sc); } }}
                style={({ pressed }) => [styles.deleteBtn, pressed && { opacity: 0.7 }]}
              >
                <Ionicons name="trash-outline" size={18} color={c.error} />
                <Text style={{ color: c.error, marginLeft: 8, fontWeight: "600" }}>Delete shortcut</Text>
              </Pressable>
            )}
          </ScrollView>
        </View>
      </Modal>
    </SafeAreaView>
  );
}

// One step's editor card: kind label + device/project/mode pickers.
function StepEditor({
  step, index, total, devices, projects, runnerOptions, connected, onConnect, onChange, onRemove, onMove,
}: {
  step: ShortcutStep;
  index: number;
  total: number;
  devices: { id: string; name: string }[];
  projects: string[];
  runnerOptions: { value: string; label: string; runner: string; model: string }[];
  connected: boolean;
  onConnect: () => void;
  onChange: (patch: Partial<ShortcutStep>) => void;
  onRemove: () => void;
  onMove: (dir: -1 | 1) => void;
}) {
  const c = useColors();
  const spec = STEP_KINDS.find((k) => k.kind === step.kind);
  return (
    <View style={[stepStyles.card, { backgroundColor: c.bgCard, borderColor: c.border }]}>
      <View style={{ flexDirection: "row", alignItems: "center" }}>
        <Ionicons name={spec?.icon || "ellipse-outline"} size={18} color={c.accent} />
        <Text style={{ color: c.textPrimary, fontWeight: "600", marginLeft: 8, flex: 1 }}>{spec?.label || step.kind}</Text>
        <Pressable hitSlop={8} disabled={index === 0} onPress={() => onMove(-1)} style={{ padding: 4, opacity: index === 0 ? 0.3 : 1 }}>
          <Ionicons name="chevron-up" size={18} color={c.textMuted} />
        </Pressable>
        <Pressable hitSlop={8} disabled={index === total - 1} onPress={() => onMove(1)} style={{ padding: 4, opacity: index === total - 1 ? 0.3 : 1 }}>
          <Ionicons name="chevron-down" size={18} color={c.textMuted} />
        </Pressable>
        <Pressable hitSlop={8} onPress={onRemove} style={{ padding: 4 }}>
          <Ionicons name="close" size={18} color={c.error} />
        </Pressable>
      </View>

      {spec?.needsDevice && (
        <Chips
          label="Device"
          items={devices.map((d) => ({ value: d.id, label: d.name }))}
          selected={step.deviceId}
          onSelect={(value, label) => onChange({ deviceId: value, deviceName: label })}
        />
      )}
      {spec?.needsProject && (
        projects.length > 0 ? (
          <Chips
            label="Project"
            items={projects.map((p) => ({ value: p, label: p }))}
            selected={step.projectSlug}
            onSelect={(value) => onChange({ projectSlug: value })}
          />
        ) : !connected ? (
          // Project list comes from the connected dev machine. Guide the
          // user to connect with one tap instead of leaving an empty picker.
          <View style={{ marginTop: 10 }}>
            <Text style={{ color: c.textMuted, fontSize: 11, marginBottom: 6 }}>PROJECT</Text>
            <Pressable
              onPress={onConnect}
              style={({ pressed }) => [
                { flexDirection: "row", alignItems: "center", gap: 8, paddingVertical: 10, paddingHorizontal: 12, borderRadius: 10, borderWidth: 1, borderColor: c.accent, backgroundColor: c.accent + "14" },
                pressed && { opacity: 0.6 },
              ]}
            >
              <Ionicons name="link-outline" size={16} color={c.accent} />
              <Text style={{ color: c.accent, fontSize: 13, fontWeight: "600" }}>Connect device to load projects</Text>
            </Pressable>
            <TextInput
              value={step.projectSlug || ""}
              onChangeText={(t) => onChange({ projectSlug: t })}
              placeholder="…or type a project slug (e.g. sfmg)"
              placeholderTextColor={c.textMuted}
              autoCapitalize="none"
              style={[stepStyles.smallInput, { backgroundColor: c.bg, color: c.textPrimary, borderColor: c.border, marginTop: 8 }]}
            />
          </View>
        ) : (
          <TextInput
            value={step.projectSlug || ""}
            onChangeText={(t) => onChange({ projectSlug: t })}
            placeholder="project slug (e.g. sfmg)"
            placeholderTextColor={c.textMuted}
            autoCapitalize="none"
            style={[stepStyles.smallInput, { backgroundColor: c.bg, color: c.textPrimary, borderColor: c.border }]}
          />
        )
      )}
      {step.kind === "hermes-reload" && (
        // Agent + model to preset on the device when the chain runs. "Off"
        // leaves the device's current agent alone. Hermes mode is always the
        // full bundle now — no Metro/dev fast-path toggle.
        <Chips
          label="Runner"
          items={runnerOptions.map((o) => ({ value: o.value, label: o.label }))}
          selected={`${step.runner || ""}:${step.model || ""}` === ":" ? "" : `${step.runner || ""}:${step.model || ""}`}
          onSelect={(value, label) => {
            const opt = runnerOptions.find((o) => o.value === value);
            onChange({
              runner: opt?.runner || "",
              model: opt?.model || "",
              runnerLabel: opt && opt.value ? label : undefined,
            });
          }}
        />
      )}
    </View>
  );
}

function Chips({ label, items, selected, onSelect }: {
  label: string;
  items: { value: string; label: string }[];
  selected?: string;
  onSelect: (value: string, label: string) => void;
}) {
  const c = useColors();
  return (
    <View style={{ marginTop: 10 }}>
      <Text style={{ color: c.textMuted, fontSize: 11, marginBottom: 6 }}>{label.toUpperCase()}</Text>
      <ScrollView horizontal showsHorizontalScrollIndicator={false} contentContainerStyle={{ gap: 8 }}>
        {items.length === 0 ? (
          <Text style={{ color: c.textMuted, fontSize: 12 }}>none available</Text>
        ) : items.map((it) => {
          const on = selected === it.value;
          return (
            <Pressable
              key={it.value}
              onPress={() => onSelect(it.value, it.label)}
              style={[stepStyles.chip, { backgroundColor: on ? c.accent + "22" : c.bg, borderColor: on ? c.accent : c.border }]}
            >
              <Text style={{ color: on ? c.accent : c.textPrimary, fontSize: 13 }}>{it.label}</Text>
            </Pressable>
          );
        })}
      </ScrollView>
    </View>
  );
}

const styles = StyleSheet.create({
  safe: { flex: 1 },
  headerRow: { marginBottom: 12 },
  subtle: { fontSize: 13 },
  notice: { flexDirection: "row", alignItems: "center", padding: 12, borderRadius: 12, borderWidth: 1, marginBottom: 16 },
  empty: { alignItems: "center", paddingVertical: 48, paddingHorizontal: 24 },
  card: { flexDirection: "row", alignItems: "center", padding: 14, borderRadius: 16, borderWidth: 1, marginBottom: 12, gap: 12 },
  cardIcon: { width: 44, height: 44, borderRadius: 12, alignItems: "center", justifyContent: "center" },
  newButton: { flexDirection: "row", alignItems: "center", justifyContent: "center", paddingVertical: 14, borderRadius: 14, marginTop: 8 },
  sheetBackdrop: { flex: 1, backgroundColor: "rgba(0,0,0,0.5)", justifyContent: "flex-end" },
  sheet: { borderTopLeftRadius: 20, borderTopRightRadius: 20, padding: 20, paddingBottom: 36 },
  sheetTitle: { fontSize: 17, fontWeight: "700", marginBottom: 16 },
  templateRow: { flexDirection: "row", alignItems: "center", paddingVertical: 14, borderTopWidth: StyleSheet.hairlineWidth },
  editorHeader: { flexDirection: "row", alignItems: "center", justifyContent: "space-between", paddingHorizontal: 16, paddingVertical: 12, borderBottomWidth: StyleSheet.hairlineWidth },
  fieldLabel: { fontSize: 11, fontWeight: "600", letterSpacing: 0.5 },
  input: { borderWidth: 1, borderRadius: 12, paddingHorizontal: 14, paddingVertical: 12, fontSize: 15, marginTop: 6 },
  swatch: { width: 32, height: 32, borderRadius: 16, borderWidth: 2 },
  addStepBtn: { flexDirection: "row", alignItems: "center", padding: 12, borderRadius: 12, borderWidth: 1, borderStyle: "dashed" },
  deleteBtn: { flexDirection: "row", alignItems: "center", justifyContent: "center", marginTop: 28, padding: 12 },
});

const stepStyles = StyleSheet.create({
  card: { borderWidth: 1, borderRadius: 14, padding: 12, marginTop: 10 },
  chip: { paddingHorizontal: 12, paddingVertical: 7, borderRadius: 20, borderWidth: 1 },
  smallInput: { borderWidth: 1, borderRadius: 10, paddingHorizontal: 12, paddingVertical: 8, fontSize: 14, marginTop: 10 },
});
