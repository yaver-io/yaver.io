/**
 * Dogfood Yaver — the "improve Yaver with Yaver" thread.
 *
 * Sticky toggle → silent screenshot auto-catch (the global DogfoodCaptureHost
 * pops the annotate modal). This screen is the history + control surface:
 * config (repo dir / base prompt / runner / mode), a batch bar for staged
 * drafts, and a per-item live agentic session that streams the coding agent's
 * output and links into the Tasks tab.
 */

import React from "react";
import {
  ActivityIndicator,
  Alert,
  Image,
  Pressable,
  ScrollView,
  StyleSheet,
  Switch,
  Text,
  TextInput,
  View,
} from "react-native";
import { useRouter } from "expo-router";
import * as ImagePicker from "expo-image-picker";
import { AppScreenHeader } from "../../src/components/AppScreenHeader";
import { DogfoodAnnotateModal, type DogfoodAnnotateResult } from "../../src/components/DogfoodAnnotateModal";
import { useColors } from "../../src/context/ThemeContext";
import { useAuth } from "../../src/context/AuthContext";
import { useDevice } from "../../src/context/DeviceContext";
import { useTabletContentStyle } from "../../src/hooks/useTabletContentStyle";
import {
  isDogfoodModeEnabled,
  setDogfoodModeEnabled,
  subscribeDogfoodMode,
} from "../../src/lib/dogfoodMode";
import { isDogfoodCaptureAvailable } from "../../src/lib/dogfoodCapture";
import {
  loadDogfoodConfig,
  saveDogfoodConfig,
  type DogfoodConfig,
  type DogfoodMode,
} from "../../src/lib/dogfoodConfig";
import {
  subscribeDogfoodThread,
  dispatchDogfoodItems,
  stageDogfoodItem,
  updateDogfoodItem,
  removeDogfoodItem,
  clearDogfoodThread,
  type DogfoodItem,
  type DogfoodItemStatus,
} from "../../src/lib/dogfoodThread";
import { quicClient } from "../../src/lib/quic";
import { speakText } from "../../src/lib/speech";

const RUNNERS = ["claude-code", "codex", "opencode"];

const STATUS_META: Record<DogfoodItemStatus, { label: string; color: (c: any) => string }> = {
  draft: { label: "DRAFT", color: (c) => c.textMuted },
  sent: { label: "SENDING", color: (c) => c.accent },
  working: { label: "WORKING", color: (c) => c.accent },
  done: { label: "DONE", color: (c) => c.success },
  failed: { label: "FAILED", color: (c) => c.warn },
};

type YaverSourceState = {
  checking: boolean;
  installing: boolean;
  cloned: boolean;
  initialized: boolean;
  initializing?: boolean;
  path?: string;
  branch?: string;
  dirty?: boolean;
  error?: string;
};

export default function DogfoodScreen() {
  const router = useRouter();
  const c = useColors();
  const tabletContent = useTabletContentStyle("regular");
  const { user } = useAuth();
  const { activeDevice, connectionStatus } = useDevice();

  const [enabled, setEnabled] = React.useState(isDogfoodModeEnabled());
  const [config, setConfig] = React.useState<DogfoodConfig | null>(null);
  const [items, setItems] = React.useState<DogfoodItem[]>([]);
  const [showConfig, setShowConfig] = React.useState(false);
  const [manualShot, setManualShot] = React.useState<string | null>(null);
  const [batchBusy, setBatchBusy] = React.useState(false);
  const [sourceState, setSourceState] = React.useState<YaverSourceState>({
    checking: false,
    installing: false,
    cloned: false,
    initialized: false,
  });

  const connected = connectionStatus === "connected" && !!activeDevice;
  const repoDir = config?.repoDir?.trim() || "";
  const vibeAvailable = !!repoDir && connected;

  React.useEffect(() => subscribeDogfoodMode(setEnabled), []);
  React.useEffect(() => subscribeDogfoodThread(setItems), []);
  React.useEffect(() => {
    void loadDogfoodConfig(user?.id).then(setConfig);
  }, [user?.id]);

  const drafts = React.useMemo(() => items.filter((i) => i.status === "draft"), [items]);

  const patchConfig = React.useCallback(
    (patch: Partial<DogfoodConfig>) => {
      setConfig((prev) => ({ ...(prev ?? { repoDir: "", prompt: "", runner: "claude-code", mode: "vibe" }), ...patch }));
      void saveDogfoodConfig(user?.id, patch);
    },
    [user?.id],
  );

  const refreshYaverSource = React.useCallback(async () => {
    if (!activeDevice?.id || connectionStatus !== "connected") {
      setSourceState({ checking: false, installing: false, cloned: false, initialized: false });
      return;
    }
    setSourceState((prev) => ({ ...prev, checking: true, error: undefined }));
    const status = await quicClient.dogfoodYaverSourceStatus(activeDevice.id);
    const next: YaverSourceState = {
      checking: false,
      installing: false,
      cloned: status.cloned,
      initialized: status.initialized,
      path: status.path,
      branch: status.branch,
      dirty: status.dirty,
      error: status.ok ? undefined : status.error || "Could not inspect Yaver source on this box.",
    };
    setSourceState(next);
    if (next.path && !repoDir) {
      patchConfig({ repoDir: next.path });
    }
  }, [activeDevice?.id, connectionStatus, patchConfig, repoDir]);

  React.useEffect(() => {
    void refreshYaverSource();
  }, [refreshYaverSource]);

  const installYaverSource = React.useCallback(async () => {
    if (!activeDevice?.id) {
      Alert.alert("No box connected", "Connect a remote box first.");
      return;
    }
    setSourceState((prev) => ({ ...prev, installing: true, error: undefined }));
    const res = await quicClient.dogfoodYaverSourceInstall(activeDevice.id, {
      autoInit: true,
      runner: config?.runner ?? "claude-code",
    });
    if (!res.ok) {
      setSourceState((prev) => ({ ...prev, installing: false, error: res.error || "Could not install Yaver source." }));
      Alert.alert("Install failed", res.error || "Could not clone Yaver onto this box.");
      return;
    }
    if (res.path) {
      patchConfig({ repoDir: res.path });
    }
    await refreshYaverSource();
    if (res.autoinit?.started) {
      setSourceState((prev) => ({ ...prev, initializing: true, path: res.path ?? prev.path }));
    }
  }, [activeDevice?.id, config?.runner, patchConfig, refreshYaverSource]);

  const dispatchItems = React.useCallback(
    async (toSend: DogfoodItem[], mode: DogfoodMode) => {
      if (!toSend.length) return;
      if (mode === "vibe" && !vibeAvailable) {
        Alert.alert("Can't vibe yet", "Set a repo dir below and connect a box, or use PR mode.");
        return;
      }
      if (!activeDevice?.id) {
        Alert.alert("No box connected", "Connect a remote box first.");
        return;
      }
      setBatchBusy(true);
      await Promise.all(toSend.map((it) => updateDogfoodItem(it.id, { status: "sent", mode })));
      const res = await dispatchDogfoodItems({
        items: toSend.map((it) => ({ ...it, status: "sent", mode })),
        mode,
        deviceId: activeDevice.id,
        deviceName: activeDevice.name,
        repoDir,
        basePrompt: config?.prompt ?? "",
        runner: config?.runner ?? "claude-code",
      });
      setBatchBusy(false);
      if (!res.ok) Alert.alert("Couldn't send", res.error || "Failed to reach the agent.");
    },
    [activeDevice?.id, activeDevice?.name, vibeAvailable, repoDir, config],
  );

  const handleManualAdd = React.useCallback(async () => {
    try {
      const res = await ImagePicker.launchImageLibraryAsync({
        mediaTypes: ImagePicker.MediaTypeOptions.Images,
        quality: 0.9,
      });
      if (!res.canceled && res.assets?.[0]?.uri) {
        setManualShot(res.assets[0].uri);
      }
    } catch {
      Alert.alert("Couldn't open photos", "Photo access may be denied in Settings.");
    }
  }, []);

  const handleManualConfirm = React.useCallback(
    async (result: DogfoodAnnotateResult) => {
      const path = manualShot;
      setManualShot(null);
      if (!path) return;
      const item = await stageDogfoodItem({
        shotPath: path,
        base64: result.base64,
        caption: result.caption,
        mode: result.mode,
      });
      if (result.send) await dispatchItems([item], result.mode);
    },
    [manualShot, dispatchItems],
  );

  return (
    <View style={{ flex: 1, backgroundColor: c.bg }}>
      <AppScreenHeader
        title="Dogfood Yaver"
        onBack={() => router.navigate("/(tabs)/more" as any)}
        right={
          items.length ? (
            <Pressable
              hitSlop={8}
              onPress={() =>
                Alert.alert("Clear thread?", "Removes all dogfood items + their images on this phone.", [
                  { text: "Cancel", style: "cancel" },
                  { text: "Clear", style: "destructive", onPress: () => void clearDogfoodThread() },
                ])
              }
            >
              <Text style={{ color: c.warn, fontSize: 13, fontWeight: "600" }}>Clear</Text>
            </Pressable>
          ) : null
        }
      />

      <ScrollView contentContainerStyle={[{ padding: 16, paddingBottom: 120 }, tabletContent]}>
        {/* Toggle */}
        <View style={[styles.card, { backgroundColor: c.bgCard, borderColor: c.border }]}>
          <View style={{ flexDirection: "row", alignItems: "center", justifyContent: "space-between" }}>
            <View style={{ flex: 1, paddingRight: 12 }}>
              <Text style={{ color: c.textPrimary, fontSize: 16, fontWeight: "700" }}>Dogfood mode</Text>
              <Text style={{ color: c.textMuted, fontSize: 12, marginTop: 3 }}>
                {enabled
                  ? "On — take a screenshot anywhere in Yaver to improve it."
                  : "Off — turn on to auto-catch screenshots for fixes."}
              </Text>
            </View>
            <Switch
              value={enabled}
              onValueChange={(v) => void setDogfoodModeEnabled(v, user?.id)}
              trackColor={{ true: c.accent }}
            />
          </View>
          {enabled && !isDogfoodCaptureAvailable() ? (
            <Text style={{ color: c.warn, fontSize: 11, marginTop: 10 }}>
              Auto-catch needs a native build (`yaver wireless push`). Use “Add screenshot” below meanwhile.
            </Text>
          ) : null}
        </View>

        {/* Target + mode summary */}
        <Pressable
          onPress={() => setShowConfig((s) => !s)}
          style={[styles.card, { backgroundColor: c.bgCard, borderColor: c.border }]}
        >
          <View style={{ flexDirection: "row", alignItems: "center", justifyContent: "space-between" }}>
            <View style={{ flex: 1, paddingRight: 12 }}>
              <Text style={{ color: c.textPrimary, fontSize: 14, fontWeight: "600" }}>
                {connected ? activeDevice?.name : "No box connected"}
              </Text>
              <Text style={{ color: c.textMuted, fontSize: 12, marginTop: 3 }}>
                {vibeAvailable ? "Vibe ready · edits + Hermes reload" : "PR mode · opens a GitHub PR"}
                {repoDir ? ` · ${repoDir}` : ""}
              </Text>
            </View>
            <Text style={{ color: c.textMuted, fontSize: 16 }}>{showConfig ? "▾" : "›"}</Text>
          </View>

          {showConfig ? (
            <View style={{ marginTop: 14, gap: 12 }} onStartShouldSetResponder={() => true}>
              <Field label="Yaver repo dir on the box (Vibe mode)" c={c}>
                <TextInput
                  value={config?.repoDir ?? ""}
                  onChangeText={(t) => patchConfig({ repoDir: t })}
                  placeholder="/Users/you/Workspace/yaver.io"
                  placeholderTextColor={c.textMuted}
                  autoCapitalize="none"
                  autoCorrect={false}
                  style={[styles.input, { color: c.textPrimary, backgroundColor: c.bgInput, borderColor: c.border }]}
                />
              </Field>
              <Field label="Base instruction" c={c}>
                <TextInput
                  value={config?.prompt ?? ""}
                  onChangeText={(t) => patchConfig({ prompt: t })}
                  multiline
                  style={[styles.input, { color: c.textPrimary, backgroundColor: c.bgInput, borderColor: c.border, minHeight: 64 }]}
                />
              </Field>
              <Field label="Runner" c={c}>
                <View style={{ flexDirection: "row", gap: 8 }}>
                  {RUNNERS.map((r) => (
                    <Pressable
                      key={r}
                      onPress={() => patchConfig({ runner: r })}
                      style={{
                        paddingVertical: 8,
                        paddingHorizontal: 12,
                        borderRadius: 8,
                        borderWidth: 1,
                        borderColor: config?.runner === r ? c.accent : c.border,
                        backgroundColor: config?.runner === r ? c.accent + "16" : "transparent",
                      }}
                    >
                      <Text style={{ color: config?.runner === r ? c.accent : c.textPrimary, fontSize: 12, fontWeight: "600" }}>
                        {r}
                      </Text>
                    </Pressable>
                  ))}
                </View>
              </Field>
            </View>
          ) : null}
        </Pressable>

        <View style={[styles.card, { backgroundColor: c.bgCard, borderColor: sourceState.error ? c.warn : sourceState.initialized ? c.success : c.border }]}>
          <View style={{ flexDirection: "row", alignItems: "center", justifyContent: "space-between", gap: 12 }}>
            <View style={{ flex: 1 }}>
              <Text style={{ color: c.textPrimary, fontSize: 14, fontWeight: "700" }}>
                Yaver source on this box
              </Text>
              <Text style={{ color: sourceState.error ? c.warn : sourceState.initialized ? c.success : c.textMuted, fontSize: 12, marginTop: 4 }} numberOfLines={2}>
                {!connected
                  ? "Connect a dev machine to inspect or install."
                  : sourceState.checking
                    ? "Checking source state..."
                    : sourceState.error
                      ? sourceState.error
                    : sourceState.initialized
                      ? `Ready${sourceState.path ? ` · ${sourceState.path}` : ""}`
                    : sourceState.initializing
                      ? `Initializing init.md${sourceState.path ? ` · ${sourceState.path}` : ""}`
                    : sourceState.cloned
                      ? `Cloned, needs init.md${sourceState.path ? ` · ${sourceState.path}` : ""}`
                    : "Not cloned yet"}
              </Text>
              {sourceState.cloned && sourceState.branch ? (
                <Text style={{ color: c.textMuted, fontSize: 11, marginTop: 3 }}>
                  {sourceState.branch}{sourceState.dirty ? " · dirty" : " · clean"}
                </Text>
              ) : null}
            </View>
            {sourceState.checking || sourceState.installing ? (
              <ActivityIndicator color={c.accent} />
            ) : (
              <Pressable
                disabled={!connected}
                onPress={sourceState.initialized || sourceState.initializing ? refreshYaverSource : installYaverSource}
                style={({ pressed }) => ({
                  paddingVertical: 9,
                  paddingHorizontal: 12,
                  borderRadius: 9,
                  backgroundColor: connected ? c.accent : c.bgInput,
                  opacity: pressed ? 0.8 : connected ? 1 : 0.55,
                })}
              >
                <Text style={{ color: connected ? "#000" : c.textMuted, fontSize: 12, fontWeight: "800" }}>
                  {sourceState.cloned ? (sourceState.initialized || sourceState.initializing ? "Check" : "Init") : "Clone"}
                </Text>
              </Pressable>
            )}
          </View>
          {sourceState.path && sourceState.path !== repoDir ? (
            <Pressable onPress={() => sourceState.path && patchConfig({ repoDir: sourceState.path })} style={{ alignSelf: "flex-start", marginTop: 10 }}>
              <Text style={{ color: c.accent, fontSize: 12, fontWeight: "700" }}>Use this path for Vibe mode</Text>
            </Pressable>
          ) : null}
        </View>

        {/* Manual add */}
        <Pressable
          onPress={handleManualAdd}
          style={[styles.card, { backgroundColor: c.bgCard, borderColor: c.border, alignItems: "center" }]}
        >
          <Text style={{ color: c.accent, fontSize: 14, fontWeight: "600" }}>＋ Add screenshot from Photos</Text>
        </Pressable>

        {/* History */}
        {items.length === 0 ? (
          <View style={{ alignItems: "center", paddingVertical: 40 }}>
            <Text style={{ color: c.textPrimary, fontSize: 15, fontWeight: "600", marginBottom: 6 }}>
              No fixes yet
            </Text>
            <Text style={{ color: c.textMuted, fontSize: 13, textAlign: "center", paddingHorizontal: 24 }}>
              With dogfood on, screenshot anything that should change. Annotate it, say what's wrong, and Yaver fixes itself.
            </Text>
          </View>
        ) : (
          items.map((item) => (
            <DogfoodCard
              key={item.id}
              item={item}
              c={c}
              onSendNow={(mode) => dispatchItems([item], mode)}
              onDelete={() => void removeDogfoodItem(item.id)}
              onOpenTasks={() =>
                item.taskId
                  ? router.navigate({ pathname: "/(tabs)/tasks" as any, params: { taskId: item.taskId } } as any)
                  : undefined
              }
            />
          ))
        )}
      </ScrollView>

      {/* Batch bar */}
      {drafts.length > 0 ? (
        <View style={[styles.batchBar, { backgroundColor: c.bgCardElevated ?? c.bgCard, borderTopColor: c.border }]}>
          <Text style={{ color: c.textPrimary, fontSize: 13, fontWeight: "600" }}>
            {drafts.length} staged
          </Text>
          <Pressable
            onPress={() => dispatchItems(drafts, vibeAvailable ? "vibe" : "pr")}
            disabled={batchBusy}
            style={({ pressed }) => ({
              backgroundColor: c.accent,
              paddingVertical: 11,
              paddingHorizontal: 18,
              borderRadius: 10,
              opacity: pressed || batchBusy ? 0.85 : 1,
            })}
          >
            {batchBusy ? (
              <ActivityIndicator color="#000" />
            ) : (
              <Text style={{ color: "#000", fontWeight: "700" }}>
                Send batch ({drafts.length}) →
              </Text>
            )}
          </Pressable>
        </View>
      ) : null}

      <DogfoodAnnotateModal
        visible={!!manualShot}
        imagePath={manualShot}
        defaultMode={vibeAvailable ? config?.mode ?? "vibe" : "pr"}
        vibeAvailable={vibeAvailable}
        onCancel={() => setManualShot(null)}
        onConfirm={handleManualConfirm}
      />
    </View>
  );
}

function Field({ label, c, children }: { label: string; c: any; children: React.ReactNode }) {
  return (
    <View>
      <Text style={{ color: c.textMuted, fontSize: 11, fontWeight: "600", marginBottom: 5 }}>{label}</Text>
      {children}
    </View>
  );
}

function DogfoodCard({
  item,
  c,
  onSendNow,
  onDelete,
  onOpenTasks,
}: {
  item: DogfoodItem;
  c: any;
  onSendNow: (mode: DogfoodMode) => void;
  onDelete: () => void;
  onOpenTasks: () => void;
}) {
  const [expanded, setExpanded] = React.useState(false);
  const meta = STATUS_META[item.status];
  const uri = item.imagePath.startsWith("file") ? item.imagePath : `file://${item.imagePath}`;

  return (
    <View style={[styles.card, { backgroundColor: c.bgCard, borderColor: item.status === "working" ? c.accent : c.border }]}>
      <Pressable
        onPress={() => (item.taskId ? setExpanded((e) => !e) : undefined)}
        style={{ flexDirection: "row", gap: 12 }}
      >
        <Image source={{ uri }} style={{ width: 56, height: 90, borderRadius: 8, backgroundColor: c.bgInput }} resizeMode="cover" />
        <View style={{ flex: 1 }}>
          <View style={{ flexDirection: "row", alignItems: "center", justifyContent: "space-between" }}>
            <Text style={{ color: meta.color(c), fontSize: 10, fontWeight: "800", letterSpacing: 0.4 }}>
              {meta.label} · {item.mode === "pr" ? "PR" : "VIBE"}
            </Text>
            <Text style={{ color: c.textMuted, fontSize: 10 }}>{timeAgo(item.createdAt)}</Text>
          </View>
          <Text style={{ color: c.textPrimary, fontSize: 14, marginTop: 4 }} numberOfLines={2}>
            {item.caption || "(no note)"}
          </Text>
          {item.route ? (
            <Text style={{ color: c.textMuted, fontSize: 11, marginTop: 3 }} numberOfLines={1}>
              {item.route}
              {item.deviceName ? ` · ${item.deviceName}` : ""}
            </Text>
          ) : null}
          {item.status === "failed" && item.error ? (
            <Text style={{ color: c.warn, fontSize: 11, marginTop: 4 }} numberOfLines={2}>
              {item.error}
            </Text>
          ) : null}
        </View>
      </Pressable>

      {/* Draft / failed actions */}
      {item.status === "draft" || item.status === "failed" ? (
        <View style={{ flexDirection: "row", gap: 10, marginTop: 12 }}>
          <Pressable
            onPress={onDelete}
            style={({ pressed }) => ({ paddingVertical: 8, paddingHorizontal: 14, borderRadius: 8, borderWidth: 1, borderColor: c.border, opacity: pressed ? 0.7 : 1 })}
          >
            <Text style={{ color: c.textMuted, fontSize: 12, fontWeight: "600" }}>Delete</Text>
          </Pressable>
          <Pressable
            onPress={() => onSendNow(item.mode)}
            style={({ pressed }) => ({ flex: 1, paddingVertical: 8, borderRadius: 8, backgroundColor: c.accent, alignItems: "center", opacity: pressed ? 0.85 : 1 })}
          >
            <Text style={{ color: "#000", fontSize: 12, fontWeight: "700" }}>
              {item.status === "failed" ? "Retry" : "Send now"}
            </Text>
          </Pressable>
        </View>
      ) : null}

      {/* Live agentic session */}
      {item.taskId && expanded ? (
        <DogfoodSession item={item} c={c} onOpenTasks={onOpenTasks} />
      ) : item.taskId ? (
        <Text style={{ color: c.accent, fontSize: 12, marginTop: 10, fontWeight: "600" }}>
          Tap to watch the agent →
        </Text>
      ) : null}
    </View>
  );
}

function DogfoodSession({ item, c, onOpenTasks }: { item: DogfoodItem; c: any; onOpenTasks: () => void }) {
  const [lines, setLines] = React.useState<string[]>([]);
  const [status, setStatus] = React.useState<string>(item.status);
  const bufRef = React.useRef<string[]>([]);

  React.useEffect(() => {
    if (!item.taskId) return;
    const abort = quicClient.streamTaskOutput(
      item.taskId,
      (text: string) => {
        const incoming = text.split("\n").filter(Boolean);
        bufRef.current = [...bufRef.current, ...incoming].slice(-120);
        setLines(bufRef.current.slice());
      },
      (st: string) => {
        setStatus(st);
        const done = st === "completed" || st === "review";
        void updateDogfoodItem(item.id, { status: done ? "done" : st === "failed" ? "failed" : item.status });
      },
    );
    return () => {
      try {
        abort();
      } catch {
        // ignore
      }
    };
  }, [item.taskId]);

  return (
    <View style={{ marginTop: 12, borderTopWidth: 1, borderTopColor: c.border, paddingTop: 10 }}>
      <View
        style={{
          maxHeight: 200,
          backgroundColor: c.bg,
          borderWidth: 1,
          borderColor: c.border,
          borderRadius: 8,
          padding: 10,
        }}
      >
        <ScrollView>
          {lines.length === 0 ? (
            <View style={{ flexDirection: "row", alignItems: "center", gap: 8 }}>
              <ActivityIndicator size="small" color={c.accent} />
              <Text style={{ color: c.textMuted, fontSize: 12 }}>Agent starting…</Text>
            </View>
          ) : (
            <Text
              selectable
              style={{ color: c.textMuted, fontSize: 11, lineHeight: 16, fontFamily: "monospace" }}
            >
              {lines.join("\n")}
            </Text>
          )}
        </ScrollView>
      </View>
      <View style={{ flexDirection: "row", gap: 10, marginTop: 10 }}>
        <Pressable
          onPress={() => speakText(lines.slice(-12).join(". ")).catch(() => {})}
          style={({ pressed }) => ({ paddingVertical: 8, paddingHorizontal: 12, borderRadius: 8, borderWidth: 1, borderColor: c.border, opacity: pressed ? 0.7 : 1 })}
        >
          <Text style={{ color: c.textPrimary, fontSize: 12, fontWeight: "600" }}>🔊 Read</Text>
        </Pressable>
        <Pressable
          onPress={onOpenTasks}
          style={({ pressed }) => ({ flex: 1, paddingVertical: 8, borderRadius: 8, backgroundColor: c.accent + "16", borderWidth: 1, borderColor: c.accent, alignItems: "center", opacity: pressed ? 0.7 : 1 })}
        >
          <Text style={{ color: c.accent, fontSize: 12, fontWeight: "700" }}>Open in Tasks (reply, approve) →</Text>
        </Pressable>
      </View>
      {status === "review" || status === "completed" ? (
        <Text style={{ color: c.success, fontSize: 11, marginTop: 8, fontWeight: "600" }}>
          {item.mode === "pr" ? "✓ Done — check the PR in Tasks" : "✓ Done — pull to reload Yaver"}
        </Text>
      ) : null}
    </View>
  );
}

function timeAgo(ts: number): string {
  const s = Math.max(0, Math.floor((Date.now() - ts) / 1000));
  if (s < 60) return "just now";
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}m ago`;
  const h = Math.floor(m / 60);
  if (h < 24) return `${h}h ago`;
  return `${Math.floor(h / 24)}d ago`;
}

const styles = StyleSheet.create({
  card: {
    borderRadius: 14,
    borderWidth: 1,
    padding: 14,
    marginBottom: 12,
  },
  input: {
    borderWidth: 1,
    borderRadius: 10,
    paddingHorizontal: 12,
    paddingVertical: 10,
    fontSize: 14,
  },
  batchBar: {
    position: "absolute",
    left: 0,
    right: 0,
    bottom: 0,
    flexDirection: "row",
    alignItems: "center",
    justifyContent: "space-between",
    paddingHorizontal: 16,
    paddingTop: 12,
    paddingBottom: 28,
    borderTopWidth: 1,
  },
});
