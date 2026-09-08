import React, { useCallback, useEffect, useRef, useState } from "react";
import { ActivityIndicator, Platform, Pressable, StyleSheet, Text, View } from "react-native";
import type { ThemeColors } from "../constants/colors";
import { gapBody, gapFixLabel, gapTitle, type CapabilityGap } from "../lib/capabilityGap";
import { formatFixElapsed, runCapabilityGapFix } from "../lib/capabilityGapFix";
import { connectionManager } from "../lib/connectionManager";
import { loadSilentInputConfig, saveSilentInputConfig } from "../lib/silentInput/config";
import { probeVSRCapabilities, type VSRCapabilities } from "../lib/silentInput/client";
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
  const status = !config.enabled
    ? "Off"
    : Platform.OS === "web"
      ? "Camera capture requires the native Yaver app on iPhone; browser features remain available"
    : probing
      ? "Checking the selected machine…"
      : capability?.available
        ? "Ready — camera crop and remote inference are available"
        : probeError || capability?.reason || "Not ready";

  return (
    <View style={[styles.root, compact && styles.compact]} accessibilityLabel="Silent Input lip reading controls">
      <Text style={[styles.intro, { color: c.textSecondary }]}>No audio. Your phone sends only temporary 96×96 grayscale mouth crops over the authenticated Yaver connection.</Text>
      <View style={styles.choiceRow}>
        <Choice label="Off" selected={!config.enabled} onPress={() => void choose(false)} colors={c} />
        <Choice label="Remote box" selected={config.enabled} onPress={() => void choose(true)} colors={c} />
      </View>
      <Text style={[styles.unavailable, { color: c.textMuted }]}>On-device edge is not offered: this build contains no verified mobile VSR model.</Text>

      <View style={[styles.status, { borderColor: capability?.available ? c.accent : c.border, backgroundColor: c.bgInput }]}>
        {probing ? <ActivityIndicator size="small" color={c.accent} /> : null}
        <Text accessibilityRole={probeError || (config.enabled && capability && !capability.available) ? "alert" : "text"} style={{ color: capability?.available ? c.accent : probeError ? c.error : c.textSecondary, fontSize: 12, lineHeight: 17, flex: 1 }}>{status}</Text>
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

      {config.enabled ? (
        <View style={styles.actions}>
          <Pressable onPress={() => void probe()} disabled={probing || installing} style={[styles.secondary, { borderColor: c.border }]} accessibilityRole="button"><Text style={{ color: c.textSecondary, fontWeight: "700", fontSize: 12 }}>Check again</Text></Pressable>
          <Pressable onPress={() => onTestRequested ? onTestRequested() : setShowTest(true)} disabled={!capability?.available || Platform.OS !== "ios"} style={[styles.action, { backgroundColor: c.accent, opacity: capability?.available && Platform.OS === "ios" ? 1 : 0.4 }]} accessibilityRole="button" accessibilityLabel="Test lip reading with the front camera"><Text style={styles.actionText}>Test lip reading</Text></Pressable>
        </View>
      ) : null}
      {testResult ? <Text accessibilityLabel="Lip reading test transcript" style={{ color: c.textPrimary, fontSize: 13, marginTop: 10 }}>Test transcript: “{testResult}”</Text> : null}

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
  root: { padding: 14 },
  compact: { paddingHorizontal: 0, paddingVertical: 10 },
  intro: { fontSize: 12, lineHeight: 17 },
  choiceRow: { flexDirection: "row", gap: 8, marginTop: 10 },
  choice: { flex: 1, borderWidth: 1, borderRadius: 10, paddingVertical: 9, alignItems: "center" },
  unavailable: { fontSize: 10, lineHeight: 14, marginTop: 7 },
  status: { flexDirection: "row", alignItems: "center", gap: 8, borderWidth: 1, borderRadius: 10, padding: 10, marginTop: 10 },
  gap: { borderWidth: 1, borderRadius: 10, padding: 10, marginTop: 8 },
  actions: { flexDirection: "row", justifyContent: "flex-end", gap: 8, marginTop: 10 },
  action: { minHeight: 38, borderRadius: 9, paddingHorizontal: 13, alignItems: "center", justifyContent: "center", marginTop: 9 },
  actionText: { color: "#fff", fontWeight: "700", fontSize: 12 },
  secondary: { minHeight: 38, borderRadius: 9, borderWidth: 1, paddingHorizontal: 13, alignItems: "center", justifyContent: "center", marginTop: 9 },
  logs: { borderRadius: 8, padding: 8, gap: 3, marginTop: 8 },
});
