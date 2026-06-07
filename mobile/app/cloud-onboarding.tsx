// Glass-friendly post-purchase setup for Yaver managed cloud.
//
// Store-policy boundary: this screen does not sell managed cloud, open
// checkout URLs, mention pricing, or call any payment route. It only finishes
// setup for a machine already active on the user's account.
//
// Visible as a 4-step checklist:
//   ☐ find cloud box
//   ☐ wait for box (Convex device row appears)
//   ☐ wait for agent (yaver serve up on the new box)
//   ☐ mirror runner (push ~/.claude/.credentials.json verbatim)
//   ✅ done
//
// Reusing the existing ops verbs means we don't add server-side state
// for this flow — it's pure client orchestration on top of stuff that
// already shipped (per memory project_managed_cloud_verified_state).

import React, { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  ActivityIndicator,
  Alert,
  Platform,
  Pressable,
  ScrollView,
  StyleSheet,
  Text,
  View,
} from "react-native";
import { useRouter } from "expo-router";
import { AppBackButton } from "../src/components/AppBackButton";
import { useSafeAreaInsets } from "react-native-safe-area-context";

import { useDevice } from "../src/context/DeviceContext";
import {
  runManagedCloudFlow,
  type FlowProgress,
  type FlowStep,
  type ManagedCloudBox,
} from "../src/lib/managedCloudFlow";

const PAL = {
  bg: "#000000",
  fg: "#e5e7eb",
  muted: "#9ca3af",
  border: "#1f2937",
  chip: "#111827",
  accent: "#a78bfa",
  ok: "#34d399",
  err: "#f87171",
};

const STEPS: { id: FlowStep; label: string }[] = [
  { id: "find_box",        label: "find cloud box" },
  { id: "wait_for_box",    label: "wait for box" },
  { id: "wait_for_agent",  label: "wait for agent" },
  { id: "mirror_runner",   label: "mirror runner token" },
  { id: "done",            label: "ready" },
];

export default function CloudOnboardingScreen(): React.ReactElement {
  const router = useRouter();
  const insets = useSafeAreaInsets();
  const { refreshDevices } = useDevice();
  const [running, setRunning] = useState(false);
  const [progress, setProgress] = useState<FlowProgress[]>([]);
  const [err, setErr] = useState<string | null>(null);
  const [newBox, setNewBox] = useState<ManagedCloudBox | null>(null);
  const abortRef = useRef<AbortController | null>(null);

  const currentStepIndex = useMemo(() => {
    if (progress.length === 0) return -1;
    const last = progress[progress.length - 1].step;
    return STEPS.findIndex((s) => s.id === last);
  }, [progress]);

  const start = useCallback(async () => {
    if (running) return;
    setRunning(true);
    setErr(null);
    setProgress([]);
    setNewBox(null);
    abortRef.current = new AbortController();
    try {
      await runManagedCloudFlow({
        runner: "claude",
        signal: abortRef.current.signal,
        onProgress: (p) => {
          setProgress((prev) => [...prev, p]);
          if (p.newBox) setNewBox(p.newBox);
        },
      });
      // Refresh the device list so the new box shows up in pickers.
      try { await refreshDevices?.(); } catch { /* non-fatal */ }
    } catch (e: unknown) {
      setErr(e instanceof Error ? e.message : String(e));
    } finally {
      setRunning(false);
      abortRef.current = null;
    }
  }, [running, refreshDevices]);

  const cancel = useCallback(() => {
    abortRef.current?.abort();
  }, []);

  useEffect(() => {
    return () => { abortRef.current?.abort(); };
  }, []);

  return (
    <View style={[styles.root, { paddingTop: insets.top }]}>
      <View style={[styles.header, { borderBottomColor: PAL.border }]}>
        <AppBackButton variant="chevron" color={PAL.muted} onPress={() => router.back()} />
        <Text style={[styles.headerTitle, { color: PAL.fg }]}>set up cloud box</Text>
        <View style={{ width: 30 }} />
      </View>

      <ScrollView style={{ flex: 1 }} contentContainerStyle={{ padding: 16 }}>
        <Text style={[styles.lead, { color: PAL.muted }]}>
          Finds an existing Yaver managed-cloud Linux box on this account,
          waits for it to come online, then pushes your Claude Code runner
          token to it. Total time: ~3–5 minutes.
        </Text>

        <View style={{ marginTop: 16 }}>
          {STEPS.map((s, i) => {
            const reached = i <= currentStepIndex;
            const isCurrent = i === currentStepIndex;
            const stepProgress = [...progress].reverse().find((p) => p.step === s.id);
            const color = reached ? (i < currentStepIndex || s.id === "done" ? PAL.ok : PAL.accent) : PAL.muted;
            return (
              <View key={s.id} style={{ flexDirection: "row", marginBottom: 10 }}>
                <Text style={{
                  color,
                  fontFamily: Platform.select({ ios: "Menlo", android: "monospace" }),
                  fontSize: 14,
                  marginRight: 8,
                  width: 18,
                }}>
                  {reached && s.id === "done" ? "✓" : reached ? (isCurrent ? "●" : "✓") : "○"}
                </Text>
                <View style={{ flex: 1 }}>
                  <Text style={{
                    color: reached ? PAL.fg : PAL.muted,
                    fontFamily: Platform.select({ ios: "Menlo", android: "monospace" }),
                    fontSize: 13,
                  }}>
                    {s.label}
                  </Text>
                  {stepProgress?.message ? (
                    <Text style={{
                      color: PAL.muted,
                      fontFamily: Platform.select({ ios: "Menlo", android: "monospace" }),
                      fontSize: 11,
                      marginTop: 2,
                    }}>
                      {stepProgress.message}
                    </Text>
                  ) : null}
                  {isCurrent && running ? (
                    <View style={{ marginTop: 4 }}>
                      <ActivityIndicator size="small" color={color} />
                    </View>
                  ) : null}
                </View>
              </View>
            );
          })}
        </View>

        {err ? (
          <View style={{ marginTop: 12, padding: 12, borderRadius: 6, backgroundColor: "#3f0f0f", borderColor: PAL.err, borderWidth: 1 }}>
            <Text style={{ color: PAL.err, fontFamily: "Menlo", fontSize: 11 }}>{err}</Text>
          </View>
        ) : null}

        {newBox ? (
          <View style={{ marginTop: 16, padding: 12, borderRadius: 6, backgroundColor: PAL.chip, borderColor: PAL.border, borderWidth: 1 }}>
            <Text style={{ color: PAL.muted, fontFamily: "Menlo", fontSize: 10 }}>CLOUD BOX</Text>
            <Text style={{ color: PAL.fg, fontFamily: "Menlo", fontSize: 13, marginTop: 4 }}>{newBox.label}</Text>
            <Text style={{ color: PAL.muted, fontFamily: "Menlo", fontSize: 10, marginTop: 4 }}>
              deviceId: {newBox.deviceId}
            </Text>
            <Text style={{ color: PAL.muted, fontFamily: "Menlo", fontSize: 10 }}>
              status:   {newBox.status}
            </Text>
          </View>
        ) : null}

        <View style={{ flexDirection: "row", marginTop: 24, gap: 12 }}>
          {!running ? (
            <Pressable
              onPress={() => {
                Alert.alert(
                  "Set up cloud box?",
                  "Yaver will look for an existing managed-cloud machine on this account and mirror your runner token to it.",
                  [
                    { text: "Cancel", style: "cancel" },
                    { text: "Continue", onPress: () => void start() },
                  ],
                );
              }}
              style={{
                flex: 1,
                paddingVertical: 12,
                borderRadius: 8,
                backgroundColor: PAL.accent,
                alignItems: "center",
              }}
            >
              <Text style={{ color: "#000", fontFamily: "Menlo", fontSize: 13, fontWeight: "600" }}>
                start
              </Text>
            </Pressable>
          ) : (
            <Pressable
              onPress={cancel}
              style={{
                flex: 1,
                paddingVertical: 12,
                borderRadius: 8,
                borderColor: PAL.err,
                borderWidth: 1,
                alignItems: "center",
              }}
            >
              <Text style={{ color: PAL.err, fontFamily: "Menlo", fontSize: 13 }}>cancel</Text>
            </Pressable>
          )}
        </View>
      </ScrollView>
    </View>
  );
}

const styles = StyleSheet.create({
  root: { flex: 1, backgroundColor: PAL.bg },
  header: {
    flexDirection: "row",
    alignItems: "center",
    paddingHorizontal: 14,
    paddingVertical: 8,
    borderBottomWidth: StyleSheet.hairlineWidth,
  },
  headerBtn: { fontSize: 26, fontWeight: "300", paddingHorizontal: 4 },
  headerTitle: {
    flex: 1,
    textAlign: "center",
    fontFamily: Platform.select({ ios: "Menlo", android: "monospace" }),
    fontSize: 13,
    fontWeight: "600",
  },
  lead: {
    fontFamily: Platform.select({ ios: "Menlo", android: "monospace" }),
    fontSize: 12,
    lineHeight: 16,
  },
});
