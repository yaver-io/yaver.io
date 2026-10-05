import React, { useCallback, useEffect, useState } from "react";
import { ActivityIndicator, Pressable, ScrollView, StyleSheet, Text, View } from "react-native";
import { SafeAreaView, useSafeAreaInsets } from "react-native-safe-area-context";
import { Ionicons } from "@expo/vector-icons";
import { useRouter } from "expo-router";

import RemoteBoxBanner from "../../src/components/RemoteBoxBanner";
import NoMachineEmpty from "../../src/components/NoMachineEmpty";
import { useColors } from "../../src/context/ThemeContext";
import { useDevice } from "../../src/context/DeviceContext";

/** Connection-first SSH home. The full PTY is a separate route so Exit SSH
 * returns here without killing the persistent tmux session. */
export default function SSHHomeScreen() {
  const c = useColors();
  const insets = useSafeAreaInsets();
  const router = useRouter();
  const { activeDevice, devices, connectionStatus, connectedDeviceIds, selectDevice } = useDevice();
  const [connecting, setConnecting] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [launchDeviceId, setLaunchDeviceId] = useState<string | null>(null);
  useEffect(() => {
    if (launchDeviceId) return;
    const alreadyConnected = activeDevice && connectedDeviceIds.includes(activeDevice.id)
      ? activeDevice
      : devices.find((candidate) => connectedDeviceIds.includes(candidate.id));
    const initiallyOpened = alreadyConnected || activeDevice || devices[0];
    if (initiallyOpened?.id) setLaunchDeviceId(initiallyOpened.id);
  }, [activeDevice, connectedDeviceIds, devices, launchDeviceId]);

  const selectedDeviceId = launchDeviceId || null;
  const selectedDevice = devices.find((candidate) => candidate.id === selectedDeviceId) || activeDevice;
  const machineChoices = [...devices].sort((a, b) => {
    const aConnected = connectedDeviceIds.includes(a.id) ? 0 : 1;
    const bConnected = connectedDeviceIds.includes(b.id) ? 0 : 1;
    if (aConnected !== bConnected) return aConnected - bConnected;
    return (a.name || "").localeCompare(b.name || "");
  });

  const chooseLaunchForDevice = useCallback((deviceId: string) => {
    setError(null);
    setLaunchDeviceId(deviceId);
  }, []);

  const launch = useCallback(async (mode: "codex" | "claude" | "opencode" | "raw") => {
    const deviceId = selectedDeviceId;
    if (!deviceId) return;
    const device = devices.find((candidate) => candidate.id === deviceId);
    setConnecting(true);
    setError(null);
    try {
      if (device && !connectedDeviceIds.includes(deviceId)) await selectDevice(device);
      router.push({ pathname: "/shell", params: { deviceId, source: "ssh-home", launch: mode } } as any);
    } catch (e: any) {
      setError(e?.message || `Could not connect to ${device?.name || "this machine"}.`);
    } finally {
      setConnecting(false);
    }
  }, [connectedDeviceIds, devices, router, selectDevice, selectedDeviceId]);

  const pending = connecting || connectionStatus === "connecting";

  return (
    <SafeAreaView style={[styles.safe, { backgroundColor: c.bg }]} edges={["bottom"]}>
      <View style={[styles.header, { paddingTop: insets.top + 8, borderBottomColor: c.border }]}> 
        <Text style={[styles.title, { color: c.textPrimary }]}>SSH</Text>
      </View>
      <ScrollView
        style={styles.contentScroll}
        contentContainerStyle={styles.content}
        showsVerticalScrollIndicator={false}
      >
        <RemoteBoxBanner disableTap />
        {error ? <Text style={[styles.error, { color: c.error }]}>{error}</Text> : null}
        {machineChoices.length > 0 ? (
          <View style={styles.machineWidgetWrap}>
            <Text style={[styles.widgetLabel, { color: c.textMuted }]}>MACHINE</Text>
            <View style={styles.machineChoices}>
              {machineChoices.map((device) => {
                const selected = device.id === selectedDeviceId;
                const connected = connectedDeviceIds.includes(device.id);
                return (
                  <Pressable
                    key={device.id}
                    accessibilityRole="button"
                    accessibilityState={{ selected }}
                    accessibilityLabel={`Use ${device.name || device.alias || "machine"} for SSH`}
                    onPress={() => chooseLaunchForDevice(device.id)}
                    style={({ pressed }) => [
                      styles.machineChoice,
                      {
                        borderColor: selected ? c.accent : c.borderSubtle,
                        backgroundColor: selected || pressed ? c.accent + "16" : c.bgCard,
                      },
                    ]}
                  >
                    <View style={[styles.machineDot, { backgroundColor: connected ? c.success : c.textMuted }]} />
                    <Text style={[styles.machineName, { color: c.textPrimary }]} numberOfLines={1}>
                      {device.alias ? `@${device.alias}` : device.name}
                    </Text>
                    {connected ? <Text style={[styles.connectedLabel, { color: c.success }]}>CONNECTED</Text> : null}
                  </Pressable>
                );
              })}
            </View>
          </View>
        ) : (
          <NoMachineEmpty noun="SSH sessions" inlineOnly onDeviceChange={chooseLaunchForDevice} />
        )}
        {selectedDevice ? (
          <View style={styles.launchWidgetWrap}>
            <View style={[styles.launchCard, { backgroundColor: c.bgCard, borderColor: c.border }]}>
              <View style={styles.launchHeadingRow}>
                <View style={[styles.launchIcon, { backgroundColor: c.accent + "1f" }]}>
                  {pending ? <ActivityIndicator size="small" color={c.accent} /> : <Ionicons name="terminal-outline" size={20} color={c.accent} />}
                </View>
                <View style={{ flex: 1 }}>
                  <Text style={[styles.launchTitle, { color: c.textPrimary }]}>Open SSH</Text>
                  <Text style={[styles.launchSubtitle, { color: c.textMuted }]}> 
                    {selectedDevice.alias ? `@${selectedDevice.alias}` : selectedDevice.name} · choose what starts
                  </Text>
                </View>
              </View>
              {([
                ["codex", "Codex", "Bypass approvals and sandbox"],
                ["claude", "Claude Code", "Skip permission prompts"],
                ["opencode", "OpenCode", "Start in auto mode"],
                ["raw", "Raw shell", "Plain login shell"],
              ] as const).map(([mode, label, detail]) => (
                <Pressable
                  key={mode}
                  accessibilityRole="button"
                  accessibilityLabel={`Launch ${label}`}
                  onPress={() => { void launch(mode); }}
                  disabled={pending}
                  style={({ pressed }) => [styles.launchChoice, { borderColor: c.borderSubtle, backgroundColor: pressed ? c.bgInput : "transparent" }]}
                >
                  <View style={{ flex: 1 }}>
                    <Text style={[styles.launchChoiceLabel, { color: c.textPrimary }]}>{label}</Text>
                    <Text style={[styles.launchChoiceDetail, { color: c.textMuted }]}>{detail}</Text>
                  </View>
                  <Ionicons name={mode === "raw" ? "terminal-outline" : "sparkles-outline"} size={18} color={c.accent} />
                </Pressable>
              ))}
            </View>
          </View>
        ) : null}
      </ScrollView>
    </SafeAreaView>
  );
}

const styles = StyleSheet.create({
  safe: { flex: 1 },
  header: { paddingHorizontal: 20, paddingBottom: 10, borderBottomWidth: StyleSheet.hairlineWidth },
  title: { fontSize: 20, fontWeight: "800" },
  contentScroll: { flex: 1 },
  content: { flexGrow: 1, paddingBottom: 24 },
  error: { fontSize: 12, lineHeight: 17, textAlign: "center", paddingHorizontal: 16, paddingTop: 10 },
  machineWidgetWrap: { width: "100%", maxWidth: 520, alignSelf: "center", paddingHorizontal: 16, paddingTop: 14 },
  widgetLabel: { fontSize: 10, lineHeight: 14, fontWeight: "800", letterSpacing: 1.1, marginBottom: 7 },
  machineChoices: { gap: 8 },
  machineChoice: { minHeight: 44, borderWidth: 1, borderRadius: 12, paddingHorizontal: 12, flexDirection: "row", alignItems: "center", gap: 9 },
  machineDot: { width: 8, height: 8, borderRadius: 4 },
  machineName: { flex: 1, minWidth: 0, fontSize: 14, fontWeight: "700" },
  connectedLabel: { fontSize: 9, lineHeight: 12, fontWeight: "900", letterSpacing: 0.7 },
  launchWidgetWrap: { width: "100%", alignItems: "center", paddingHorizontal: 16, paddingTop: 16 },
  launchCard: { width: "100%", maxWidth: 520, borderWidth: 1, borderRadius: 18, padding: 16 },
  launchHeadingRow: { flexDirection: "row", alignItems: "center", gap: 12, marginBottom: 7 },
  launchIcon: { width: 42, height: 42, borderRadius: 12, alignItems: "center", justifyContent: "center" },
  launchTitle: { fontSize: 19, fontWeight: "800" },
  launchSubtitle: { fontSize: 12, lineHeight: 18, marginTop: 2 },
  launchChoice: { minHeight: 61, borderWidth: 1, borderRadius: 12, paddingHorizontal: 14, paddingVertical: 10, marginTop: 8, flexDirection: "row", alignItems: "center", gap: 12 },
  launchChoiceLabel: { fontSize: 15, fontWeight: "700" },
  launchChoiceDetail: { fontSize: 11, lineHeight: 16, marginTop: 2 },
});
