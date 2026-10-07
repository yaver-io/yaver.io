import React, { useCallback, useEffect, useState } from "react";
import { ActivityIndicator, Pressable, ScrollView, StyleSheet, Text, View } from "react-native";
import { SafeAreaView, useSafeAreaInsets } from "react-native-safe-area-context";
import { Ionicons } from "@expo/vector-icons";
import { useRouter } from "expo-router";

import RemoteBoxBanner from "../../src/components/RemoteBoxBanner";
import NoMachineEmpty from "../../src/components/NoMachineEmpty";
import { useColors } from "../../src/context/ThemeContext";
import { useDevice } from "../../src/context/DeviceContext";

/** First-class SSH home for phones and tablets. The full xterm PTY stays on
 * /shell so leaving this screen detaches from, rather than kills, tmux. */
export default function SSHHomeScreen() {
  const c = useColors();
  const insets = useSafeAreaInsets();
  const router = useRouter();
  const { activeDevice, devices, connectionStatus, connectedDeviceIds, selectDevice } = useDevice();
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const [showAgent, setShowAgent] = useState(false);
  const [connecting, setConnecting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (selectedId) return;
    const connected = activeDevice && connectedDeviceIds.includes(activeDevice.id)
      ? activeDevice
      : devices.find((device) => connectedDeviceIds.includes(device.id));
    setSelectedId((connected || activeDevice || devices[0])?.id ?? null);
  }, [activeDevice, connectedDeviceIds, devices, selectedId]);

  const choices = [...devices].sort((a, b) => {
    const connectionOrder = Number(!connectedDeviceIds.includes(a.id)) - Number(!connectedDeviceIds.includes(b.id));
    return connectionOrder || (a.name || "").localeCompare(b.name || "");
  });
  const selected = choices.find((device) => device.id === selectedId) || activeDevice;

  const open = useCallback(async () => {
    if (!selected) return;
    setConnecting(true);
    setError(null);
    try {
      if (!connectedDeviceIds.includes(selected.id)) await selectDevice(selected);
      router.push("/shell" as any);
    } catch (cause: any) {
      setError(cause?.message || `Could not connect to ${selected.name || "this machine"}.`);
    } finally {
      setConnecting(false);
    }
  }, [connectedDeviceIds, router, selectDevice, selected]);

  const pending = connecting || connectionStatus === "connecting";
  return (
    <SafeAreaView style={[styles.safe, { backgroundColor: c.bg }]} edges={["bottom"]}>
      <View style={[styles.header, { paddingTop: insets.top + 8, borderBottomColor: c.border }]}>
        <Text style={[styles.title, { color: c.textPrimary }]}>SSH</Text>
      </View>
      <ScrollView contentContainerStyle={styles.content} showsVerticalScrollIndicator={false}>
        <Pressable accessibilityRole="button" accessibilityLabel="Connect with direct SSH" onPress={() => router.push("/plain")}
          style={[styles.launch, {backgroundColor:c.bgCard,borderColor:c.border}]}>
          <Ionicons name="terminal-outline" size={22} color={c.accent} />
          <View style={{flex:1}}><Text style={[styles.launchTitle,{color:c.textPrimary}]}>Connect with SSH</Text>
            <Text style={[styles.launchDetail,{color:c.textMuted}]}>Your tmux panes · no Yaver sign-in required</Text></View>
          <Ionicons name="chevron-forward" size={20} color={c.textMuted} />
        </Pressable>
        <Pressable accessibilityRole="button" accessibilityState={{expanded:showAgent}} onPress={()=>setShowAgent(!showAgent)} style={styles.section}>
          <Text style={{color:c.textMuted}}>Yaver agent terminals {showAgent ? "⌃" : "⌄"}</Text>
        </Pressable>
        {showAgent && <>
        <RemoteBoxBanner disableTap />
        {error ? <Text style={[styles.error, { color: c.error }]}>{error}</Text> : null}
        {choices.length ? (
          <View style={styles.section}>
            <Text style={[styles.label, { color: c.textMuted }]}>MACHINE</Text>
            {choices.map((device) => {
              const picked = selected?.id === device.id;
              const connected = connectedDeviceIds.includes(device.id);
              return (
                <Pressable
                  key={device.id}
                  accessibilityRole="button"
                  accessibilityState={{ selected: picked }}
                  accessibilityLabel={`Use ${device.name || device.alias || "machine"} for SSH`}
                  onPress={() => { setError(null); setSelectedId(device.id); }}
                  style={({ pressed }) => [styles.machine, {
                    borderColor: picked ? c.accent : c.borderSubtle,
                    backgroundColor: picked || pressed ? c.accent + "16" : c.bgCard,
                  }]}
                >
                  <View style={[styles.dot, { backgroundColor: connected ? c.success : c.textMuted }]} />
                  <Text style={[styles.machineName, { color: c.textPrimary }]} numberOfLines={1}>
                    {device.alias ? `@${device.alias}` : device.name}
                  </Text>
                  {connected ? <Text style={[styles.connected, { color: c.success }]}>CONNECTED</Text> : null}
                </Pressable>
              );
            })}
          </View>
        ) : <NoMachineEmpty noun="SSH sessions" onDeviceChange={setSelectedId} />}

        {selected ? (
          <View style={[styles.launch, { backgroundColor: c.bgCard, borderColor: c.border }]}>
            <View style={[styles.icon, { backgroundColor: c.accent + "1f" }]}>
              {pending ? <ActivityIndicator size="small" color={c.accent} /> :
                <Ionicons name="terminal-outline" size={22} color={c.accent} />}
            </View>
            <View style={{ flex: 1 }}>
              <Text style={[styles.launchTitle, { color: c.textPrimary }]}>Open terminal</Text>
              <Text style={[styles.launchDetail, { color: c.textMuted }]}>Raw shell, tmux, Codex, Claude Code, or OpenCode</Text>
            </View>
            <Pressable disabled={pending} onPress={() => { void open(); }} style={[styles.openButton, { backgroundColor: c.accent }]}>
              <Text style={styles.openText}>Open</Text>
            </Pressable>
          </View>
        ) : null}
        </>}
      </ScrollView>
    </SafeAreaView>
  );
}

const styles = StyleSheet.create({
  safe: { flex: 1 },
  header: { paddingHorizontal: 20, paddingBottom: 10, borderBottomWidth: StyleSheet.hairlineWidth },
  title: { fontSize: 20, fontWeight: "800" },
  content: { flexGrow: 1, paddingBottom: 28 },
  error: { padding: 12, textAlign: "center", fontSize: 12 },
  section: { width: "100%", maxWidth: 640, alignSelf: "center", paddingHorizontal: 16, paddingTop: 14, gap: 8 },
  label: { fontSize: 10, fontWeight: "800", letterSpacing: 1.1 },
  machine: { minHeight: 48, borderWidth: 1, borderRadius: 12, paddingHorizontal: 12, flexDirection: "row", alignItems: "center", gap: 9 },
  dot: { width: 8, height: 8, borderRadius: 4 },
  machineName: { flex: 1, fontSize: 14, fontWeight: "700" },
  connected: { fontSize: 9, fontWeight: "900", letterSpacing: 0.7 },
  launch: { width: "100%", maxWidth: 640, alignSelf: "center", marginTop: 16, borderWidth: 1, borderRadius: 18, padding: 16, flexDirection: "row", alignItems: "center", gap: 12 },
  icon: { width: 44, height: 44, borderRadius: 12, alignItems: "center", justifyContent: "center" },
  launchTitle: { fontSize: 17, fontWeight: "800" },
  launchDetail: { fontSize: 11, lineHeight: 16, marginTop: 2 },
  openButton: { paddingHorizontal: 16, paddingVertical: 10, borderRadius: 10 },
  openText: { color: "#fff", fontWeight: "800" },
});
