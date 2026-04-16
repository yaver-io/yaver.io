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
import { TextInput } from "react-native";
import { Device, RunnerInfo, useDevice } from "../../src/context/DeviceContext";
import { useAuth } from "../../src/context/AuthContext";
import { useColors } from "../../src/context/ThemeContext";
import { quicClient } from "../../src/lib/quic";
import { beaconListener, type DiscoveredDevice } from "../../src/lib/beacon";
import { submitPair, fetchPairInfo } from "../../src/lib/pairDevice";

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
  // Seed needsAuth from Convex device record so the badge shows immediately
  // (without waiting for the /info poll to complete).
  const [needsAuth, setNeedsAuth] = useState<boolean>(device.needsAuth === true);
  const [autoPairing, setAutoPairing] = useState(false);
  const isOnline = device.online;

  // Keep state in sync when Convex list refreshes
  useEffect(() => {
    setNeedsAuth(device.needsAuth === true);
  }, [device.needsAuth]);

  // Poll /info for bootstrap/auth state — shows a "needs auth" badge
  // on the card AND auto-pairs when the remote agent is in bootstrap.
  // This runs for every visible device card (not just the "active" one),
  // so a Convex-offline device that's really in bootstrap gets paired
  // without the user having to "activate" it first.
  useEffect(() => {
    if (!device.host || !token) return;
    let cancelled = false;
    let paired = false;
    const check = async () => {
      if (paired || cancelled) return;
      const targetUrl = `http://${device.host}:${device.port || 18080}`;
      console.log(`[auto-pair] polling ${targetUrl}/info for ${device.name}`);
      try {
        const res = await fetch(`${targetUrl}/info`, { signal: AbortSignal.timeout(3000) });
        if (!res.ok) {
          console.log(`[auto-pair] ${device.name}: /info returned ${res.status}`);
          return;
        }
        if (cancelled) return;
        const info = await res.json();
        const inBootstrap = info.needsAuth === true || info.mode === "bootstrap";
        console.log(`[auto-pair] ${device.name}: needsAuth=${info.needsAuth} mode=${info.mode} → inBootstrap=${inBootstrap}`);
        if (cancelled) return;
        setNeedsAuth(inBootstrap);
        if (!inBootstrap) return;
        setAutoPairing(true);
        try {
          const { submitEncryptedPair } = await import("../../src/lib/encryptedPair");
          const { submitPair } = await import("../../src/lib/pairDevice");
          const pubKey = device.publicKey || info.devicePublicKey;
          if (pubKey) {
            console.log(`[auto-pair] ${device.name}: trying encrypted pair with pubkey ${pubKey.slice(0,12)}...`);
            const r = await submitEncryptedPair(targetUrl, token, pubKey, info.bootstrapPasskey);
            console.log(`[auto-pair] ${device.name}: encrypted pair result ok=${r.ok} error=${r.error}`);
            if (r.ok) {
              paired = true;
              setNeedsAuth(false);
              return;
            }
          }
          const passkey = info.bootstrapPasskey;
          if (passkey) {
            console.log(`[auto-pair] ${device.name}: trying passkey pair ${passkey}`);
            const r = await submitPair({ code: passkey, targetUrl, token, userId: "" });
            console.log(`[auto-pair] ${device.name}: passkey pair result ok=${r.ok} error=${r.error}`);
            if (r.ok) {
              paired = true;
              setNeedsAuth(false);
            }
          } else {
            console.log(`[auto-pair] ${device.name}: no passkey in /info — cannot fall back`);
          }
        } finally {
          if (!cancelled) setAutoPairing(false);
        }
      } catch (err: any) {
        console.log(`[auto-pair] ${device.name}: error ${err?.message || err}`);
      }
    };
    check();
    const iv = setInterval(check, 8000);
    return () => { cancelled = true; clearInterval(iv); };
  }, [device.host, device.port, device.publicKey, token]);
  const runners = device.runners || [];
  const activeRunners = runners.filter((r) => r.status === "running");
  const workerLabel =
    device.deviceClass === "edge-mobile"
      ? "MOBILE WORKER"
      : device.deviceClass === "server"
        ? "SERVER"
        : undefined;

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
          <View style={{ flexDirection: "row", alignItems: "center", gap: 6, flexWrap: "wrap" }}>
            <Text style={[styles.deviceName, { color: c.textPrimary }]}>{device.name}</Text>
            {device.isGuest ? (
              <View style={{
                paddingHorizontal: 8, paddingVertical: 2, borderRadius: 10,
                backgroundColor: "#60a5fa22", borderWidth: 1, borderColor: "#60a5fa66",
              }}>
                <Text style={{ color: "#60a5fa", fontSize: 10, fontWeight: "700" }}>SHARED</Text>
              </View>
            ) : null}
            {device.isGuest && device.priorityMode === "spare-capacity" ? (
              <View style={{
                paddingHorizontal: 8, paddingVertical: 2, borderRadius: 10,
                backgroundColor: "#a78bfa22", borderWidth: 1, borderColor: "#a78bfa66",
              }}>
                <Text style={{ color: "#a78bfa", fontSize: 10, fontWeight: "700" }}>SPARE</Text>
              </View>
            ) : null}
            {!device.isGuest && device.sessionBinding ? (
              <View style={{
                paddingHorizontal: 8, paddingVertical: 2, borderRadius: 10,
                backgroundColor: device.sessionBinding === "dedicated" ? "#22c55e22" : "#f59e0b22",
                borderWidth: 1,
                borderColor: device.sessionBinding === "dedicated" ? "#22c55e66" : "#f59e0b66",
              }}>
                <Text
                  style={{
                    color: device.sessionBinding === "dedicated" ? "#22c55e" : "#f59e0b",
                    fontSize: 10,
                    fontWeight: "700",
                  }}
                >
                  {device.sessionBinding === "dedicated" ? "DEDICATED SESSION" : "LEGACY SESSION"}
                </Text>
              </View>
            ) : null}
            {workerLabel ? (
              <View style={{
                paddingHorizontal: 8, paddingVertical: 2, borderRadius: 10,
                backgroundColor: "#0ea5e922", borderWidth: 1, borderColor: "#0ea5e966",
              }}>
                <Text style={{ color: "#38bdf8", fontSize: 10, fontWeight: "700" }}>{workerLabel}</Text>
              </View>
            ) : null}
            {autoPairing ? (
              <View style={{
                paddingHorizontal: 8, paddingVertical: 2, borderRadius: 10,
                backgroundColor: "#818cf822", borderWidth: 1, borderColor: "#818cf866",
              }}>
                <Text style={{ color: "#818cf8", fontSize: 10, fontWeight: "700" }}>PAIRING…</Text>
              </View>
            ) : needsAuth ? (
              <View style={{
                paddingHorizontal: 8, paddingVertical: 2, borderRadius: 10,
                backgroundColor: "#eab30822", borderWidth: 1, borderColor: "#eab30866",
              }}>
                <Text style={{ color: "#eab308", fontSize: 10, fontWeight: "700" }}>NEEDS AUTH</Text>
              </View>
            ) : isOnline ? (
              <View style={{
                paddingHorizontal: 8, paddingVertical: 2, borderRadius: 10,
                backgroundColor: "#22c55e22", borderWidth: 1, borderColor: "#22c55e66",
              }}>
                <Text style={{ color: "#22c55e", fontSize: 10, fontWeight: "700" }}>AUTHENTICATED</Text>
              </View>
            ) : null}
          </View>
          <Text style={[styles.deviceMeta, { color: c.textMuted }]}>
            {device.os} &middot; {device.host}:{device.port}
            {device.isGuest && device.hostName ? ` · shared from ${device.hostName}` : ""}
          </Text>
          {device.edgeProfile ? (
            <Text style={[styles.deviceMeta, { color: c.textMuted, marginTop: 4 }]}>
              {device.edgeProfile.supportsLocalInference ? "local inference" : "no local inference"}
              {` · max ${device.edgeProfile.maxModelClass}`}
              {device.edgeProfile.thermalState ? ` · ${device.edgeProfile.thermalState}` : ""}
              {typeof device.edgeProfile.batteryPct === "number" ? ` · ${device.edgeProfile.batteryPct}% battery` : ""}
              {device.edgeProfile.isCharging ? " · charging" : ""}
            </Text>
          ) : null}
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
  const { token, user } = useAuth();
  const {
    devices,
    activeDevice,
    connectionStatus,
    isLoadingDevices,
    selectDevice,
    disconnect,
    refreshDevices,
    detachDevice,
    removeDevice,
    acceptGuestByCode,
  } = useDevice();

  const [guestCode, setGuestCode] = useState("");
  const [guestLoading, setGuestLoading] = useState(false);

  // Bootstrap devices — fresh yaver boxes on the LAN that are
  // running `yaver serve` in unauthenticated mode. Tapping one
  // pushes this phone's token to it so the box joins the user's
  // account without ever needing SSH/terminal access.
  const [bootstrapDevices, setBootstrapDevices] = useState<DiscoveredDevice[]>([]);
  const [adoptingId, setAdoptingId] = useState<string | null>(null);

  useEffect(() => {
    const refresh = () => setBootstrapDevices(beaconListener.getBootstrapDevices());
    refresh();
    const iv = setInterval(refresh, 2000);
    return () => clearInterval(iv);
  }, []);

  const handleAdoptBootstrap = useCallback(
    async (dev: DiscoveredDevice) => {
      if (!token) {
        Alert.alert("Not signed in", "Sign into the Yaver app first, then try again.");
        return;
      }
      if (!dev.bootstrapPasskey) {
        Alert.alert(
          "Passkey hidden",
          "This box has hidden its passkey from the beacon. Open More → Pair a device and type the 6-character passkey shown on the machine."
        );
        return;
      }
      const targetUrl = `http://${dev.ip}:${dev.port}`;
      setAdoptingId(dev.deviceId);
      try {
        const info = await fetchPairInfo(targetUrl);
        if (!info.ok) {
          Alert.alert("Pair failed", info.error ?? "Target is not in pairing mode.");
          return;
        }
        const res = await submitPair({
          code: dev.bootstrapPasskey,
          targetUrl,
          token,
          userId: user?.id,
        });
        if (!res.ok) {
          Alert.alert("Pair failed", res.error ?? "Target rejected the token.");
          return;
        }
        Alert.alert(
          "Paired",
          `Signed ${user?.email ?? "your account"} into ${res.host ?? dev.name ?? "the machine"}. It should appear as online shortly.`
        );
        // Refresh devices so the newly paired box shows up once
        // it registers with Convex.
        setTimeout(() => refreshDevices(), 3000);
      } finally {
        setAdoptingId(null);
      }
    },
    [token, user, refreshDevices],
  );

  const handleAcceptGuestCode = async () => {
    const code = guestCode.trim();
    if (!code || code.length < 4) return;
    setGuestLoading(true);
    try {
      const result = await acceptGuestByCode(code);
      Alert.alert("Joined!", `You now have access to ${result.hostName}'s machine.`);
      setGuestCode("");
      refreshDevices();
    } catch (e: any) {
      Alert.alert("Error", e.message || "Invalid code");
    }
    setGuestLoading(false);
  };

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

        {/* Guest code input */}
        <View style={[styles.guestCodeRow, { borderBottomColor: c.border }]}>
          <TextInput
            style={[styles.guestCodeInput, { backgroundColor: c.bgCard, borderColor: c.border, color: c.textPrimary }]}
            placeholder="Invite code"
            placeholderTextColor={c.textMuted}
            value={guestCode}
            onChangeText={setGuestCode}
            autoCapitalize="characters"
            maxLength={6}
          />
          <Pressable
            style={[styles.guestCodeBtn, { backgroundColor: c.accent, opacity: guestCode.trim().length < 4 ? 0.4 : 1 }]}
            onPress={handleAcceptGuestCode}
            disabled={guestCode.trim().length < 4 || guestLoading}
          >
            <Text style={styles.guestCodeBtnText}>{guestLoading ? "..." : "Join"}</Text>
          </Pressable>
        </View>

        {/* Needs-auth section: fresh yaver boxes on this LAN */}
        {bootstrapDevices.length > 0 && (
          <View style={{ paddingHorizontal: 16, paddingTop: 12 }}>
            <Text style={{ color: c.textMuted, fontSize: 12, fontWeight: "600", marginBottom: 6 }}>
              NEEDS AUTH ({bootstrapDevices.length})
            </Text>
            <Text style={{ color: c.textMuted, fontSize: 12, marginBottom: 10 }}>
              A yaver machine on this Wi-Fi is waiting for a sign-in.
              Tap to sign it into {user?.email ? `${user.email}` : "your account"}
              {user?.provider ? ` (${user.provider})` : ""}.
            </Text>
            {bootstrapDevices.map((d) => {
              const isBusy = adoptingId === d.deviceId;
              return (
                <Pressable
                  key={d.deviceId}
                  onPress={() => handleAdoptBootstrap(d)}
                  disabled={isBusy}
                  style={{
                    flexDirection: "row",
                    alignItems: "center",
                    padding: 12,
                    borderRadius: 10,
                    borderWidth: 1,
                    borderColor: c.border,
                    backgroundColor: c.bgCard,
                    marginBottom: 8,
                    gap: 12,
                    opacity: isBusy ? 0.6 : 1,
                  }}
                >
                  <View style={{ width: 10, height: 10, borderRadius: 5, backgroundColor: c.warn }} />
                  <View style={{ flex: 1 }}>
                    <Text style={{ color: c.textPrimary, fontSize: 15, fontWeight: "600" }}>
                      {d.name || d.deviceId}
                    </Text>
                    <Text style={{ color: c.textMuted, fontSize: 12, marginTop: 2 }}>
                      {d.ip}:{d.port} — tap to adopt
                    </Text>
                  </View>
                  {isBusy ? (
                    <ActivityIndicator color={c.accent} />
                  ) : (
                    <Text style={{ color: c.accent, fontSize: 13, fontWeight: "600" }}>Adopt</Text>
                  )}
                </Pressable>
              );
            })}
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
                const actionLabel = item.isGuest ? "Detach" : "Remove";
                const message = item.isGuest
                  ? "Remove this shared machine from your list? It will reappear if the host shares it again."
                  : "Remove this device from your account? The node will need to re-register before it shows up again.";
                Alert.alert(
                  item.name,
                  message,
                  [
                    { text: "Cancel", style: "cancel" },
                    {
                      text: actionLabel,
                      style: "destructive",
                      onPress: async () => {
                        try {
                          if (item.isGuest) {
                            await detachDevice(item);
                          } else {
                            await removeDevice(item);
                          }
                        } catch (e: any) {
                          Alert.alert("Error", e?.message || "Failed");
                        }
                      },
                    },
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
  guestCodeRow: {
    flexDirection: "row",
    alignItems: "center",
    gap: 8,
    paddingHorizontal: 16,
    paddingVertical: 10,
    borderBottomWidth: 1,
  },
  guestCodeInput: {
    flex: 1,
    height: 40,
    borderWidth: 1,
    borderRadius: 8,
    paddingHorizontal: 12,
    fontSize: 16,
    fontFamily: Platform.OS === "ios" ? "Menlo" : "monospace",
    letterSpacing: 4,
    textAlign: "center",
    textTransform: "uppercase",
  },
  guestCodeBtn: {
    height: 40,
    paddingHorizontal: 20,
    borderRadius: 8,
    justifyContent: "center",
    alignItems: "center",
  },
  guestCodeBtnText: {
    color: "#fff",
    fontSize: 14,
    fontWeight: "600",
  },
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
