import React, { useCallback, useEffect, useRef, useState } from "react";
import { Ionicons } from "@expo/vector-icons";
import { ActivityIndicator, Platform, Pressable, StyleSheet, Switch, Text, View } from "react-native";
import type { ThemeColors } from "../constants/colors";
import { gapBody, gapFixLabel, gapTitle, type CapabilityGap } from "../lib/capabilityGap";
import { formatFixElapsed, runCapabilityGapFix } from "../lib/capabilityGapFix";
import { connectionManager } from "../lib/connectionManager";
import { loadSilentInputConfig, saveSilentInputConfig } from "../lib/silentInput/config";
import { probeVSRCapabilities, type VSRCapabilities } from "../lib/silentInput/client";
import { silentInputSetupPresentation } from "../lib/silentInput/presentation";
import { DEFAULT_SILENT_INPUT_CONFIG, type SilentInputConfig } from "../lib/silentInput/types";
import { SilentInputModal } from "./SilentInputModal";

type Props = {
  colors: ThemeColors;
  targetDeviceId?: string | null;
  projectName?: string;
  compact?: boolean;
  onConfigChange?(config: SilentInputConfig): void;
  onUseTranscription?(text: string): void;
  onTestRequested?(): void;
};

/** One operational Silent Input surface shared by Settings and Tasks > … .
 * It saves the preference, probes the selected box, consumes the agent's typed
 * recovery route, streams installation, and finally runs a real camera→crop→
 * transfer→inference test. A selected radio button alone is never "ready". */
export function SilentInputControlPanel({
  colors: c,
  targetDeviceId,
  projectName,
  compact = false,
  onConfigChange,
  onUseTranscription,
  onTestRequested,
}: Props) {
  const [config, setConfig] = useState<SilentInputConfig>(DEFAULT_SILENT_INPUT_CONFIG);
  const [capability, setCapability] = useState<VSRCapabilities | null>(null);
  const [probing, setProbing] = useState(false);
  const [probeError, setProbeError] = useState("");
  const [showTest, setShowTest] = useState(false);
  const [testResult, setTestResult] = useState("");
  const [installing, setInstalling] = useState(false);
  const [installStartedAt, setInstallStartedAt] = useState(0);
  const [now, setNow] = useState(Date.now());
  const [installLines, setInstallLines] = useState<string[]>([]);
  const cancelInstall = useRef<(() => void) | null>(null);

  useEffect(() => {
    let active = true;
    void loadSilentInputConfig().then((saved) => { if (active) setConfig(saved); });
    return () => { active = false; };
  }, []);

  const probe = useCallback(async () => {
    if (!targetDeviceId) {
      setCapability(null);
      setProbeError("Connect a Yaver machine to use private remote lip reading.");
      return;
    }
    setProbing(true);
    setProbeError("");
    try {
      setCapability(await probeVSRCapabilities(targetDeviceId));
    } catch (cause) {
      setCapability(null);
      setProbeError(cause instanceof Error ? cause.message : String(cause));
    } finally {
      setProbing(false);
    }
  }, [targetDeviceId]);

  useEffect(() => {
    // Browser automation can exercise the real RN application, but browsers
    // cannot load VisionCamera. Do not start camera-backend work merely because
    // a native preference was restored; the rest of Settings must stay quiet
    // and testable. Native platforms keep their existing probe path.
    if (Platform.OS !== "web" && config.enabled && config.backend === "user-machine") void probe();
    else {
      setCapability(null);
      setProbeError("");
    }
  }, [config.backend, config.enabled, probe]);
  useEffect(() => () => cancelInstall.current?.(), []);
  useEffect(() => {
    if (!installing) return;
    const timer = setInterval(() => setNow(Date.now()), 1_000);
    return () => clearInterval(timer);
  }, [installing]);

  const choose = useCallback(async (enabled: boolean) => {
    const next: SilentInputConfig = {
      ...config,
      enabled,
      // No verified phone or cloud VSR model ships. Never persist one of the
      // historical dead choices; remote inference is the only honest backend.
      backend: "user-machine",
    };
    setConfig(next);
    setTestResult("");
    try {
      await saveSilentInputConfig(next);
      onConfigChange?.(next);
    } catch (cause) {
      setConfig(config);
      setProbeError(cause instanceof Error ? cause.message : String(cause));
    }
  }, [config, onConfigChange]);

  const install = useCallback((gap: CapabilityGap) => {
    if (!targetDeviceId || installing) return;
    const client = connectionManager.clientFor(targetDeviceId);
    setInstalling(true);
    setInstallStartedAt(Date.now());
    setNow(Date.now());
    setInstallLines([]);
    cancelInstall.current = runCapabilityGapFix({
      installTool: (tool) => client.installTool(tool, targetDeviceId),
      subscribeStream: (name, onLine, onResult, onEvent) => client.subscribeStream(name, onLine, onResult, onEvent),
    }, gap, {
      onLine: (line) => setInstallLines((old) => [...old, line.trimEnd()].filter(Boolean).slice(-6)),
      onDone: (ok, error) => {
        cancelInstall.current = null;
        setInstalling(false);
        if (!ok) {
          setProbeError(error || "VSR installation failed.");
          return;
        }
        void probe();
      },
    });
  }, [installing, probe, targetDeviceId]);

  const gap = capability?.capabilityGap || null;
  const presentation = silentInputSetupPresentation({
    enabled: config.enabled,
    platform: Platform.OS,
    targetDeviceId,
    probing,
    capabilityAvailable: capability?.available,
    unavailableReason: probeError || capability?.reason,
  });
  const statusColor = presentation.statusKind === "ready"
    ? c.success
    : presentation.statusKind === "attention"
      ? c.warn
      : c.textSecondary;
  const statusBackground = presentation.statusKind === "ready"
    ? c.successBg
    : presentation.statusKind === "attention"
      ? c.warnBg
      : c.bgInput;
  const statusBorder = presentation.statusKind === "ready"
    ? c.successBorder
    : presentation.statusKind === "attention"
      ? c.warnBorder
      : c.border;

  return (
    <View style={[styles.root, compact && styles.compact]} accessibilityLabel="Silent Input lip reading controls">
      {compact ? (
        <>
          <Text style={[styles.compactIntro, { color: c.textSecondary }]}>No audio. Use your connected Yaver machine to read private mouth crops.</Text>
          <View style={styles.choiceRow}>
            <Choice label="Off" selected={!config.enabled} onPress={() => void choose(false)} colors={c} />
            <Choice label="Remote box" selected={config.enabled} onPress={() => void choose(true)} colors={c} />
          </View>
        </>
      ) : (
        <>
          <View style={styles.header}>
            <View style={[styles.icon, { backgroundColor: c.accentSoft }]}>
              <Ionicons name="eye-outline" size={22} color={c.accent} />
            </View>
            <View style={styles.headerCopy}>
              <Text style={[styles.title, { color: c.textPrimary }]}>Lip reading</Text>
              <Text style={[styles.subtitle, { color: c.textMuted }]}>Type without speaking</Text>
            </View>
            <View style={styles.toggleWrap}>
              <Text style={[styles.toggleLabel, { color: config.enabled ? c.accent : c.textMuted }]}>{config.enabled ? "On" : "Off"}</Text>
              <Switch
                value={config.enabled}
                onValueChange={(enabled) => void choose(enabled)}
                trackColor={{ false: c.border, true: c.accent }}
                accessibilityLabel="Enable lip reading"
                accessibilityHint="Uses the front camera and your connected Yaver machine"
              />
            </View>
          </View>
          <Text style={[styles.intro, { color: c.textSecondary }]}>Turn it on, wait for Ready to test, then use the front camera to silently mouth a short phrase.</Text>
        </>
      )}

      <View style={[styles.status, { borderColor: statusBorder, backgroundColor: statusBackground }]}>
        {probing ? <ActivityIndicator size="small" color={c.accent} /> : null}
        {!probing ? <Ionicons name={presentation.statusKind === "ready" ? "checkmark-circle" : presentation.statusKind === "attention" ? "alert-circle-outline" : "ellipse-outline"} size={18} color={statusColor} /> : null}
        <View style={styles.statusCopy}>
          <Text accessibilityRole={presentation.statusKind === "attention" ? "alert" : "text"} style={[styles.statusTitle, { color: statusColor }]}>{presentation.status}</Text>
          <Text style={[styles.statusGuidance, { color: c.textSecondary }]}>{presentation.guidance}</Text>
        </View>
      </View>

      {config.enabled && gap ? (
        <View style={[styles.gap, { borderColor: c.border }]}>
          <Text style={{ color: c.textPrimary, fontWeight: "700", fontSize: 13 }}>{gapTitle(gap)}</Text>
          {gapBody(gap) ? <Text style={{ color: c.textMuted, fontSize: 11, lineHeight: 16, marginTop: 4 }}>{gapBody(gap)}</Text> : null}
          {gapFixLabel(gap) ? (
            <Pressable disabled={installing} onPress={() => install(gap)} style={[styles.action, { backgroundColor: c.accent, opacity: installing ? 0.65 : 1 }]} accessibilityRole="button" accessibilityLabel={gapFixLabel(gap) || "Install VSR"}>
              <Text style={styles.actionText}>{installing ? `Installing… ${formatFixElapsed(installStartedAt, now)}` : gapFixLabel(gap)}</Text>
            </Pressable>
          ) : null}
        </View>
      ) : null}
      {installLines.length ? <View style={[styles.logs, { backgroundColor: c.bg }]}>{installLines.map((line, index) => <Text key={`${index}-${line}`} style={{ color: c.textMuted, fontFamily: Platform.OS === "ios" ? "Menlo" : "monospace", fontSize: 10 }} numberOfLines={2}>{line}</Text>)}</View> : null}

      <View style={styles.actions}>
        {config.enabled && Platform.OS === "ios" ? <Pressable onPress={() => void probe()} disabled={probing || installing || !targetDeviceId} style={[styles.secondary, { borderColor: c.border, opacity: probing || installing || !targetDeviceId ? 0.45 : 1 }]} accessibilityRole="button" accessibilityLabel="Check lip reading setup again"><Text style={{ color: c.textSecondary, fontWeight: "700", fontSize: 12 }}>Check again</Text></Pressable> : null}
        <Pressable
          onPress={() => onTestRequested ? onTestRequested() : setShowTest(true)}
          disabled={!presentation.canTest}
          style={[styles.testAction, { backgroundColor: presentation.canTest ? c.accent : c.bgCardElevated, borderColor: presentation.canTest ? c.accent : c.border }]}
          accessibilityRole="button"
          accessibilityLabel="Test lip reading with the front camera"
          accessibilityState={{ disabled: !presentation.canTest }}
        >
          <Ionicons name="camera-outline" size={17} color={presentation.canTest ? "#fff" : c.textMuted} />
          <Text style={[styles.actionText, { color: presentation.canTest ? "#fff" : c.textMuted }]}>Test with front camera</Text>
        </Pressable>
      </View>
      {testResult ? <Text accessibilityLabel="Lip reading test transcript" style={{ color: c.textPrimary, fontSize: 13, marginTop: 10 }}>Test transcript: “{testResult}”</Text> : null}

      {!compact ? <Text style={[styles.privacy, { color: c.textMuted }]}>No audio is recorded. Only temporary 96×96 grayscale mouth crops cross your encrypted Yaver connection.</Text> : null}

      {Platform.OS === "ios" && targetDeviceId ? (
        <SilentInputModal
          visible={showTest}
          colors={c}
          targetDeviceId={targetDeviceId}
          projectName={projectName}
          backend="user-machine"
          onCancel={() => setShowTest(false)}
          onTranscription={(text) => {
            setTestResult(text);
            setShowTest(false);
            onUseTranscription?.(text);
          }}
        />
      ) : null}
    </View>
  );
}

function Choice({ label, selected, onPress, colors: c }: { label: string; selected: boolean; onPress(): void; colors: ThemeColors }) {
  return <Pressable accessibilityRole="radio" accessibilityState={{ selected }} onPress={onPress} style={[styles.choice, { borderColor: selected ? c.accent : c.border, backgroundColor: selected ? c.accent + "1f" : c.bgInput }]}><Text style={{ color: selected ? c.accent : c.textSecondary, fontWeight: "700", fontSize: 12 }}>{label}</Text></Pressable>;
}

const styles = StyleSheet.create({
  root: { padding: 16 },
  compact: { paddingHorizontal: 0, paddingVertical: 10 },
  header: { flexDirection: "row", alignItems: "center", gap: 10 },
  icon: { width: 38, height: 38, borderRadius: 10, alignItems: "center", justifyContent: "center" },
  headerCopy: { flex: 1 },
  title: { fontSize: 16, fontWeight: "700" },
  subtitle: { fontSize: 12, lineHeight: 17, marginTop: 1 },
  toggleWrap: { alignItems: "center", gap: 2 },
  toggleLabel: { fontSize: 10, fontWeight: "700", textTransform: "uppercase" },
  intro: { fontSize: 12, lineHeight: 18, marginTop: 12 },
  compactIntro: { fontSize: 12, lineHeight: 17 },
  choiceRow: { flexDirection: "row", gap: 8, marginTop: 10 },
  choice: { flex: 1, borderWidth: 1, borderRadius: 10, paddingVertical: 9, alignItems: "center" },
  status: { flexDirection: "row", alignItems: "center", gap: 8, borderWidth: 1, borderRadius: 10, padding: 10, marginTop: 10 },
  statusCopy: { flex: 1 },
  statusTitle: { fontSize: 12, lineHeight: 17, fontWeight: "700" },
  statusGuidance: { fontSize: 11, lineHeight: 16, marginTop: 1 },
  gap: { borderWidth: 1, borderRadius: 10, padding: 10, marginTop: 8 },
  actions: { flexDirection: "row", justifyContent: "flex-end", gap: 8, marginTop: 10 },
  action: { minHeight: 38, borderRadius: 9, paddingHorizontal: 13, alignItems: "center", justifyContent: "center", marginTop: 9 },
  actionText: { color: "#fff", fontWeight: "700", fontSize: 12 },
  testAction: { minHeight: 42, flex: 1, borderRadius: 9, borderWidth: 1, paddingHorizontal: 13, flexDirection: "row", gap: 7, alignItems: "center", justifyContent: "center" },
  secondary: { minHeight: 42, borderRadius: 9, borderWidth: 1, paddingHorizontal: 13, alignItems: "center", justifyContent: "center" },
  logs: { borderRadius: 8, padding: 8, gap: 3, marginTop: 8 },
  privacy: { fontSize: 10, lineHeight: 15, marginTop: 10 },
});
