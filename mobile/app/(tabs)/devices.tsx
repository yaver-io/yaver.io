import React, { useCallback, useEffect, useState } from "react";
import {
  ActivityIndicator,
  FlatList,
  Platform,
  Pressable,
  ScrollView,
  StyleSheet,
  Text,
  View,
} from "react-native";
import * as Clipboard from "expo-clipboard";
import { SafeAreaView } from "react-native-safe-area-context";
import { Alert } from "react-native";
import { Device, RunnerInfo, useDevice } from "../../src/context/DeviceContext";
import { useAuth } from "../../src/context/AuthContext";
import { useColors } from "../../src/context/ThemeContext";
import { quicClient } from "../../src/lib/quic";

function ConnectionBadge({ status }: { status: string }) {
  const c = useColors();
  const color =
    status === "connected" ? c.success
    : status === "connecting" ? c.warn
    : status === "error" ? c.error
    : c.textMuted;
  return (
    <View style={[styles.connBadge, { backgroundColor: color + "22" }]}>
      <View style={[styles.connDot, { backgroundColor: color }]} />
      <Text style={[styles.connText, { color }]}>{status}</Text>
    </View>
  );
}

function buildDeviceUrl(device: Device, token: string | null): string | null {
  const relays = quicClient.getRelayServers();
  if (relays.length > 0) return `${relays[0].httpUrl}/d/${device.id}`;
  return `http://${device.host}:${device.port}`;
}

function DeviceCard({
  device,
  isActive,
  onSelect,
  onLongPress,
  token,
}: {
  device: Device;
  isActive: boolean;
  onSelect: () => void;
  onLongPress: () => void;
  token: string | null;
}) {
  const c = useColors();
  const [pingState, setPingState] = useState<{ pinging: boolean; rttMs?: number; ok?: boolean }>({ pinging: false });
  const [killing, setKilling] = useState<string | null>(null);
  const isOnline = device.online;
  const runners = device.runners || [];
  const activeRunners = runners.filter((r) => r.status === "running");

  const timeSince = (ts: number) => {
    if (!ts) return "never";
    const seconds = Math.floor((Date.now() - ts) / 1000);
    if (seconds < 60) return "just now";
    if (seconds < 3600) return `${Math.floor(seconds / 60)}m ago`;
    if (seconds < 86400) return `${Math.floor(seconds / 3600)}h ago`;
    const d = new Date(ts);
    return d.toLocaleDateString(undefined, { month: "short", day: "numeric" }) + " " + d.toLocaleTimeString(undefined, { hour: "2-digit", minute: "2-digit" });
  };

  const handlePing = async () => {
    setPingState({ pinging: true });
    const relays = quicClient.getRelayServers();
    const urls = [
      ...relays.map((r) => `${r.httpUrl}/d/${device.id}`),
      `http://${device.host}:${device.port}`,
    ];
    for (const url of urls) {
      const start = Date.now();
      try {
        const controller = new AbortController();
        const timeout = setTimeout(() => controller.abort(), 5000);
        const res = await fetch(`${url}/health`, {
          headers: token ? { Authorization: `Bearer ${token}` } : {},
          signal: controller.signal,
        });
        clearTimeout(timeout);
        if (res.ok) {
          setPingState({ pinging: false, ok: true, rttMs: Date.now() - start });
          return;
        }
      } catch {
        continue;
      }
    }
    setPingState({ pinging: false, ok: false });
  };

  const killTask = async (taskId: string) => {
    const baseUrl = buildDeviceUrl(device, token);
    if (!baseUrl || !token) return;
    setKilling(taskId);
    try {
      await fetch(`${baseUrl}/tasks/${taskId}/stop`, {
        method: "POST",
        headers: { Authorization: `Bearer ${token}` },
      });
    } catch {}
    setKilling(null);
  };

  const killAll = async () => {
    for (const r of activeRunners) {
      await killTask(r.taskId);
    }
  };

  const shutdownAgent = () => {
    Alert.alert("Shutdown Agent", `Stop the Yaver agent on ${device.name}?`, [
      { text: "Cancel", style: "cancel" },
      {
        text: "Shutdown", style: "destructive", onPress: async () => {
          const baseUrl = buildDeviceUrl(device, token);
          if (!baseUrl || !token) return;
          try {
            await fetch(`${baseUrl}/agent/shutdown`, {
              method: "POST",
              headers: { Authorization: `Bearer ${token}` },
            });
          } catch {}
        },
      },
    ]);
  };

  // Group runners by runnerId for summary
  const runnerCounts = activeRunners.reduce<Record<string, number>>((acc, r) => {
    acc[r.runnerId] = (acc[r.runnerId] || 0) + 1;
    return acc;
  }, {});

  return (
    <Pressable
      style={({ pressed }) => [
        styles.card,
        { backgroundColor: c.bgCard, borderColor: isActive ? c.accent : c.border },
        pressed && styles.cardPressed,
      ]}
      onPress={onSelect}
      onLongPress={onLongPress}
    >
      <View style={styles.cardRow}>
        <View style={styles.cardInfo}>
          <Text style={[styles.deviceName, { color: c.textPrimary }]}>{device.name}</Text>
          <Text style={[styles.deviceMeta, { color: c.textMuted }]}>
            {device.os} &middot; {device.host}:{device.port}
          </Text>
        </View>
        <View style={styles.cardRight}>
          <View style={[styles.onlineDot, { backgroundColor: isOnline ? c.success : c.textMuted }]} />
          <Text style={[styles.lastSeen, { color: c.textMuted }]}>
            {isOnline ? "online" : "offline"}
          </Text>
          {device.lastSeen > 0 && (
            <Text style={[styles.lastSeen, { color: c.textMuted, marginTop: 2 }]}>
              {timeSince(device.lastSeen)}
            </Text>
          )}
        </View>
      </View>

      {/* Runner + status badges */}
      <View style={styles.runnerBadges}>
        {Object.entries(runnerCounts).map(([rid, count]) => (
          <View key={rid} style={[styles.runnerBadge, { backgroundColor: c.accent + "18" }]}>
            <Text style={[styles.runnerBadgeText, { color: c.accent }]}>
              {rid} x{count}
            </Text>
          </View>
        ))}
      </View>

      {/* Runner list + actions — always visible */}
      {isOnline && (
        <View style={[styles.menuSection, { borderTopColor: c.border }]}>
          {activeRunners.length > 0 && (
            <>
              {activeRunners.map((r) => (
                <View key={r.taskId} style={styles.runnerRow}>
                  <View style={{ flex: 1 }}>
                    <Text style={[styles.runnerTitle, { color: c.textPrimary }]} numberOfLines={1}>
                      {r.title}
                    </Text>
                    <Text style={[styles.runnerMeta, { color: c.textMuted }]}>
                      {r.runnerId}{r.model ? ` / ${r.model}` : ""} &middot; PID {r.pid}
                    </Text>
                  </View>
                  <Pressable
                    style={[styles.killBtn, { backgroundColor: c.error + "18" }]}
                    onPress={() => killTask(r.taskId)}
                    disabled={killing === r.taskId}
                  >
                    <Text style={[styles.killBtnText, { color: c.error }]}>
                      {killing === r.taskId ? "..." : "Kill"}
                    </Text>
                  </Pressable>
                </View>
              ))}
              {activeRunners.length > 1 && (
                <Pressable
                  style={[styles.killAllBtn, { backgroundColor: c.error + "12" }]}
                  onPress={killAll}
                >
                  <Text style={[styles.killBtnText, { color: c.error }]}>Kill All</Text>
                </Pressable>
              )}
            </>
          )}
          {activeRunners.length === 0 && (
            <Text style={[styles.runnerMeta, { color: c.textMuted, paddingVertical: 4 }]}>No active runners</Text>
          )}
          <View style={[styles.menuActions, { borderTopColor: c.border }]}>
            <Pressable style={[styles.menuActionBtn, { backgroundColor: c.error + "12" }]} onPress={shutdownAgent}>
              <Text style={[styles.menuActionText, { color: c.error }]}>Shutdown Agent</Text>
            </Pressable>
          </View>
        </View>
      )}

      <View style={styles.cardBottom}>
        {isActive && (
          <View style={[styles.activeLabel, { backgroundColor: c.accent + "22" }]}>
            <Text style={[styles.activeLabelText, { color: c.accent }]}>Active</Text>
          </View>
        )}
        <Pressable
          style={[styles.pingBtn, { backgroundColor: c.bgCardElevated || c.bg }]}
          onPress={() => handlePing()}
          disabled={pingState.pinging}
        >
          <Text style={[styles.pingBtnText, {
            color: pingState.pinging ? c.textMuted
              : pingState.ok === true ? c.success
              : pingState.ok === false ? c.error
              : c.textSecondary,
          }]}>
            {pingState.pinging ? "..." :
              pingState.ok === true ? `${pingState.rttMs}ms` :
              pingState.ok === false ? "unreachable" : "ping"}
          </Text>
        </Pressable>
        {!isOnline && (
          <>
            <Pressable
              style={[styles.pingBtn, { backgroundColor: "#f59e0b18" }]}
              onPress={() => Alert.alert(
                "Wake Machine",
                "Send a Wake-on-LAN magic packet to power on this machine.\n\n" +
                "Requirements:\n" +
                "• WoL enabled in BIOS\n" +
                "• Wired ethernet (most WiFi cards don't support WoL)\n" +
                "• Same network or Tailscale\n\n" +
                "For always-on setup: yaver.io/manuals/auto-boot",
                [{ text: "OK" }]
              )}
            >
              <Text style={[styles.pingBtnText, { color: "#f59e0b" }]}>Wake</Text>
            </Pressable>
            <Pressable
              style={[styles.pingBtn, { backgroundColor: "#6366f118" }]}
              onPress={() => Alert.alert(
                "Always-on Setup",
                "1. Enable auto-boot in BIOS\n" +
                "2. Run: yaver serve --install-systemd\n" +
                "3. Run: sudo loginctl enable-linger $USER\n\n" +
                "Full guide: yaver.io/manuals/auto-boot",
                [{ text: "OK" }]
              )}
            >
              <Text style={[styles.pingBtnText, { color: c.accent }]}>Setup</Text>
            </Pressable>
          </>
        )}
      </View>
    </Pressable>
  );
}

function CopyableCommand({ command }: { command: string }) {
  const c = useColors();
  const [copied, setCopied] = useState(false);

  const handleCopy = useCallback(async () => {
    await Clipboard.setStringAsync(command);
    setCopied(true);
    setTimeout(() => setCopied(false), 2000);
  }, [command]);

  return (
    <Pressable
      style={[styles.codeBlock, { backgroundColor: c.bg }]}
      onPress={handleCopy}
    >
      <Text style={[styles.codeText, { color: c.textPrimary }]}>{command}</Text>
      <Text style={[styles.copyHint, { color: copied ? c.success : c.textMuted }]}>
        {copied ? "Copied!" : "Tap to copy"}
      </Text>
    </Pressable>
  );
}

function PlatformIcon({ platform, color }: { platform: string; color?: string }) {
  const labels: Record<string, string> = { mac: "⌘", linux: "🐧", windows: "⊞" };
  return <Text style={{ fontSize: 16, marginRight: 6, color }}>{labels[platform] || ""}</Text>;
}

function PlatformTab({
  platform,
  label,
  active,
  onPress,
}: {
  platform: string;
  label: string;
  active: boolean;
  onPress: () => void;
}) {
  const c = useColors();
  return (
    <Pressable
      style={[
        styles.platformTab,
        {
          backgroundColor: active ? c.textPrimary + "12" : "transparent",
          borderColor: active ? c.textPrimary : c.border,
        },
      ]}
      onPress={onPress}
    >
      <PlatformIcon platform={platform} color={active ? c.textPrimary : c.textMuted} />
      <Text style={[styles.platformTabText, { color: active ? c.textPrimary : c.textMuted }]}>
        {label}
      </Text>
    </Pressable>
  );
}

function SetupInstructions() {
  const c = useColors();
  const [platform, setPlatform] = useState<"mac" | "linux" | "windows">("mac");

  return (
    <ScrollView contentContainerStyle={styles.setupContainer}>
      <Text style={[styles.emptyTitle, { color: c.textPrimary }]}>Set Up Your Desktop</Text>
      <Text style={[styles.emptySubtitle, { color: c.textSecondary }]}>
        Install the Yaver agent on your dev machine, then pull to refresh.
      </Text>

      <View style={styles.platformTabs}>
        <PlatformTab platform="mac" label="macOS" active={platform === "mac"} onPress={() => setPlatform("mac")} />
        <PlatformTab platform="linux" label="Linux" active={platform === "linux"} onPress={() => setPlatform("linux")} />
        <PlatformTab platform="windows" label="Windows" active={platform === "windows"} onPress={() => setPlatform("windows")} />
      </View>

      {platform === "mac" && (
        <View style={styles.steps}>
          <Text style={[styles.stepLabel, { color: c.textSecondary }]}>1. Install via Homebrew</Text>
          <CopyableCommand command="brew tap kivanccakmak/yaver && brew install yaver" />

          <Text style={[styles.stepLabel, { color: c.textSecondary }]}>2. Sign in & start</Text>
          <CopyableCommand command="yaver auth" />
        </View>
      )}

      {platform === "linux" && (
        <View style={styles.steps}>
          <Text style={[styles.stepLabel, { color: c.textSecondary }]}>1. Install via Homebrew</Text>
          <CopyableCommand command="brew tap kivanccakmak/yaver && brew install yaver" />

          <Text style={[styles.stepLabel, { color: c.textSecondary }]}>Or download directly</Text>
          <CopyableCommand command={'curl -fsSL https://github.com/kivanccakmak/yaver-cli/releases/latest/download/yaver-linux-amd64 -o yaver && chmod +x yaver && sudo mv yaver /usr/local/bin/'} />

          <Text style={[styles.stepLabel, { color: c.textSecondary }]}>2. Sign in & start</Text>
          <CopyableCommand command="yaver auth" />
        </View>
      )}

      {platform === "windows" && (
        <View style={styles.steps}>
          <Text style={[styles.stepLabel, { color: c.textSecondary }]}>1. Install via Scoop (PowerShell)</Text>
          <CopyableCommand command="scoop bucket add yaver https://github.com/kivanccakmak/scoop-yaver && scoop install yaver" />

          <Text style={[styles.stepLabel, { color: c.textSecondary }]}>Or download manually</Text>
          <Text style={[styles.stepHint, { color: c.textMuted }]}>
            Download from yaver.io/download and add to your PATH.
          </Text>

          <Text style={[styles.stepLabel, { color: c.textSecondary }]}>2. Sign in & start</Text>
          <CopyableCommand command="yaver auth" />
        </View>
      )}

      <Text style={[styles.refreshHint, { color: c.textMuted }]}>
        Pull down to refresh after setup
      </Text>
    </ScrollView>
  );
}

export default function DevicesScreen() {
  const c = useColors();
  const { token } = useAuth();
  const {
    devices,
    activeDevice,
    connectionStatus,
    isLoadingDevices,
    selectDevice,
    disconnect,
    refreshDevices,
    detachDevice,
  } = useDevice();

  // Device polling is handled by DeviceContext (every 3s from any screen)

  return (
    <SafeAreaView style={[styles.safeArea, { backgroundColor: c.bg }]} edges={["bottom"]}>
      <View style={styles.container}>
        {activeDevice && connectionStatus !== "disconnected" && (
          <View style={[styles.statusBar, { borderBottomColor: c.border }]}>
            <ConnectionBadge status={connectionStatus} />
            {(connectionStatus === "connected" || connectionStatus === "error") && (
              <Pressable style={[styles.disconnectBtn, { backgroundColor: c.bgCardElevated }]} onPress={disconnect}>
                <Text style={[styles.disconnectText, { color: c.error }]}>Disconnect</Text>
              </Pressable>
            )}
          </View>
        )}

        <FlatList
          data={devices}
          keyExtractor={(item) => item.id}
          contentContainerStyle={styles.listContent}
          refreshing={isLoadingDevices}
          onRefresh={refreshDevices}
          ListEmptyComponent={isLoadingDevices ? (
            <View style={styles.center}>
              <ActivityIndicator size="large" color={c.accent} />
            </View>
          ) : <SetupInstructions />}
          renderItem={({ item }) => (
            <DeviceCard
              device={item}
              isActive={activeDevice?.id === item.id}
              onSelect={() => selectDevice(item)}
              onLongPress={() => {
                Alert.alert(
                  item.name,
                  "Remove this device from your list? It will reappear if it connects again.",
                  [
                    { text: "Cancel", style: "cancel" },
                    { text: "Detach", style: "destructive", onPress: () => detachDevice(item) },
                  ]
                );
              }}
              token={token}
            />
          )}
        />
      </View>
    </SafeAreaView>
  );
}

const styles = StyleSheet.create({
  safeArea: { flex: 1 },
  container: { flex: 1 },
  statusBar: {
    flexDirection: "row",
    alignItems: "center",
    justifyContent: "space-between",
    paddingHorizontal: 16,
    paddingVertical: 12,
    borderBottomWidth: 1,
  },
  connBadge: {
    flexDirection: "row",
    alignItems: "center",
    paddingHorizontal: 10,
    paddingVertical: 6,
    borderRadius: 8,
  },
  connDot: { width: 6, height: 6, borderRadius: 3, marginRight: 6 },
  connText: { fontSize: 12, fontWeight: "600", textTransform: "capitalize" },
  disconnectBtn: {
    paddingHorizontal: 12,
    paddingVertical: 6,
    borderRadius: 8,
  },
  disconnectText: { fontSize: 13, fontWeight: "600" },
  listContent: { padding: 16, flexGrow: 1 },
  center: {
    flex: 1,
    justifyContent: "center",
    alignItems: "center",
    padding: 32,
  },
  emptyTitle: { fontSize: 20, fontWeight: "700", textAlign: "center" },
  emptySubtitle: {
    fontSize: 14,
    textAlign: "center",
    marginTop: 8,
    lineHeight: 20,
  },
  setupContainer: {
    padding: 8,
    paddingTop: 24,
    alignItems: "center",
  },
  platformTabs: {
    flexDirection: "row",
    gap: 8,
    marginTop: 20,
    marginBottom: 20,
  },
  platformTab: {
    flexDirection: "row",
    alignItems: "center",
    paddingHorizontal: 14,
    paddingVertical: 8,
    borderRadius: 8,
    borderWidth: 1,
  },
  platformTabText: {
    fontSize: 13,
    fontWeight: "600",
  },
  steps: {
    width: "100%",
    gap: 6,
  },
  stepLabel: {
    fontSize: 13,
    fontWeight: "600",
    marginTop: 10,
    marginBottom: 2,
  },
  stepHint: {
    fontSize: 12,
    marginTop: 4,
    lineHeight: 18,
  },
  codeBlock: {
    width: "100%",
    borderRadius: 8,
    padding: 12,
    flexDirection: "row",
    justifyContent: "space-between",
    alignItems: "center",
  },
  codeText: {
    fontSize: 12,
    fontFamily: Platform.OS === "ios" ? "Menlo" : "monospace",
    flex: 1,
    marginRight: 8,
  },
  copyHint: {
    fontSize: 10,
    flexShrink: 0,
  },
  refreshHint: {
    fontSize: 12,
    marginTop: 24,
    textAlign: "center",
  },
  card: {
    borderRadius: 12,
    padding: 16,
    marginBottom: 12,
    borderWidth: 1,
  },
  cardPressed: { opacity: 0.7 },
  cardRow: { flexDirection: "row", justifyContent: "space-between" },
  cardInfo: { flex: 1, marginRight: 12 },
  deviceName: { fontSize: 16, fontWeight: "600" },
  deviceMeta: { fontSize: 13, marginTop: 4 },
  cardRight: { alignItems: "flex-end" },
  onlineDot: { width: 8, height: 8, borderRadius: 4, marginBottom: 4 },
  lastSeen: { fontSize: 11 },
  cardBottom: {
    flexDirection: "row",
    alignItems: "center",
    justifyContent: "space-between",
    marginTop: 10,
  },
  activeLabel: {
    alignSelf: "flex-start",
    paddingHorizontal: 10,
    paddingVertical: 4,
    borderRadius: 6,
  },
  activeLabelText: { fontSize: 12, fontWeight: "600" },
  pingBtn: {
    paddingHorizontal: 10,
    paddingVertical: 4,
    borderRadius: 6,
    marginLeft: "auto",
  },
  pingBtnText: { fontSize: 12, fontWeight: "600" },
  runnerBadges: { flexDirection: "row", flexWrap: "wrap", gap: 6, marginTop: 8 },
  runnerBadge: { paddingHorizontal: 8, paddingVertical: 3, borderRadius: 6 },
  runnerBadgeText: { fontSize: 11, fontWeight: "600" },
  menuSection: { marginTop: 10, paddingTop: 10, borderTopWidth: 1 },
  runnerRow: { flexDirection: "row", alignItems: "center", marginBottom: 8 },
  runnerTitle: { fontSize: 13, fontWeight: "500" },
  runnerMeta: { fontSize: 11, marginTop: 1 },
  killBtn: { paddingHorizontal: 10, paddingVertical: 4, borderRadius: 6, marginLeft: 8 },
  killAllBtn: { paddingHorizontal: 10, paddingVertical: 6, borderRadius: 6, alignSelf: "flex-start", marginTop: 4 },
  killBtnText: { fontSize: 12, fontWeight: "600" },
  menuActions: { marginTop: 8, paddingTop: 8, borderTopWidth: 1 },
  menuActionBtn: { paddingHorizontal: 12, paddingVertical: 6, borderRadius: 6, alignSelf: "flex-start" },
  menuActionText: { fontSize: 12, fontWeight: "600" },
});
