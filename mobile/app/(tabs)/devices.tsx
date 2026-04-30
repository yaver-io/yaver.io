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
import { Device, useDevice } from "../../src/context/DeviceContext";
import { useAuth } from "../../src/context/AuthContext";
import { useColors } from "../../src/context/ThemeContext";
import { quicClient } from "../../src/lib/quic";
import DeviceDetailsModal from "../../src/components/DeviceDetailsModal";
import { beaconListener, type DiscoveredDevice } from "../../src/lib/beacon";
import { submitPair, fetchPairInfo } from "../../src/lib/pairDevice";
import {
  classifyTransport,
  fetchRelayHealth,
  transportToneRGB,
  type TransportInfo,
} from "../../src/lib/transport";

function transportFor(device: Device): TransportInfo {
  return classifyTransport({
    host: device.host,
    port: device.port,
    localIps: device.lanIps,
    publicEndpoints: device.publicEndpoints,
    tunnelUrl: device.tunnelUrl,
    activeRelayUrl: quicClient.activeRelayBaseUrl ?? null,
    activeTunnelUrl: quicClient.activeTunnelBaseUrl ?? null,
    platform: device.os,
    name: device.name,
  });
}

function TransportBadge({ device }: { device: Device }) {
  const t = transportFor(device);
  const palette = transportToneRGB(t.tone);
  return (
    <View style={{
      paddingHorizontal: 6, paddingVertical: 2, borderRadius: 6,
      backgroundColor: palette.bg, borderWidth: 1, borderColor: palette.border,
      alignSelf: "flex-start",
    }}>
      <Text style={{ color: palette.text, fontSize: 9, fontWeight: "700", letterSpacing: 0.6 }}>
        {t.label.toUpperCase()}
      </Text>
    </View>
  );
}

type DeviceProjectSummary = {
  total: number;
};

type DeviceRuntimeSummary = {
  version: string | null;
  authExpired: boolean;
  mode: string | null;
};

type MachineSummary = {
  projectSummary: DeviceProjectSummary | null;
  runtime: DeviceRuntimeSummary | null;
  fetchedAt: number;
};

const MACHINE_SUMMARY_TTL_MS = 30_000;
const machineSummaryCache = new Map<string, MachineSummary>();

function isLikelyWSLDevice(device: Pick<Device, "name" | "os" | "host">): boolean {
  const os = String(device.os || "").trim().toLowerCase();
  if (os !== "linux") return false;
  const name = String(device.name || "").trim().toUpperCase();
  const host = String(device.host || "").trim();
  const windowsHostLike =
    name.startsWith("DESKTOP-") ||
    name.startsWith("LAPTOP-") ||
    name.startsWith("WIN-");
  const wslNatLike = /^172\.(1[6-9]|2\d|3[0-1])\.\d{1,3}\.\d{1,3}$/.test(host);
  return windowsHostLike || wslNatLike;
}

function formatDevicePlatform(device: Pick<Device, "name" | "os" | "host">, exactRuntime?: string | null): string {
  const os = String(device.os || "").trim();
  if (exactRuntime) return exactRuntime;
  if (isLikelyWSLDevice(device)) return "Linux (likely WSL)";
  return os;
}

function hasRecentLiveSignal(device: Pick<Device, "lastTunnelEvent">, maxAgeMs = 90_000): boolean {
  return Boolean(
    device.lastTunnelEvent &&
    device.lastTunnelEvent.online &&
    device.lastTunnelEvent.at > 0 &&
    (Date.now() - device.lastTunnelEvent.at) < maxAgeMs
  );
}

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

function buildDeviceRequestContext(
  device: Pick<Device, "id" | "host" | "port">,
  token: string | null,
): { baseUrl: string; headers: Record<string, string> } | null {
  if (!token) return null;
  const relay = quicClient.getRelayServers()[0];
  if (relay?.httpUrl) {
    const headers: Record<string, string> = {
      Authorization: `Bearer ${token}`,
      "X-Client-Platform": Platform.OS,
    };
    if (relay.password) headers["X-Relay-Password"] = relay.password;
    return {
      baseUrl: `${relay.httpUrl}/d/${encodeURIComponent(device.id)}`,
      headers,
    };
  }
  return {
    baseUrl: `http://${device.host}:${device.port}`,
    headers: {
      Authorization: `Bearer ${token}`,
      "X-Client-Platform": Platform.OS,
    },
  };
}

async function fetchMachineSummaryWithHeaders(
  baseUrl: string,
  headers: Record<string, string>,
  opts?: { force?: boolean },
): Promise<MachineSummary> {
  const cacheKey = `${baseUrl}|${JSON.stringify(headers)}`;
  const cached = machineSummaryCache.get(cacheKey);
  if (!opts?.force && cached && Date.now() - cached.fetchedAt < MACHINE_SUMMARY_TTL_MS) {
    return cached;
  }
  const [projectsRes] = await Promise.allSettled([
    fetch(`${baseUrl}/projects`, { headers, signal: AbortSignal.timeout(5000) }),
  ]);
  const [infoRes] = await Promise.allSettled([
    fetch(`${baseUrl}/info`, { headers, signal: AbortSignal.timeout(5000) }),
  ]);

  let projectSummary: DeviceProjectSummary | null = null;
  let runtime: DeviceRuntimeSummary | null = null;

  if (projectsRes.status === "fulfilled" && projectsRes.value.ok) {
    const projectsJson = await projectsRes.value.json();
    const projects = Array.isArray(projectsJson?.projects) ? projectsJson.projects : [];
    projectSummary = {
      total: projects.length,
    };
  }

  if (infoRes.status === "fulfilled" && infoRes.value.ok) {
    const infoJson = await infoRes.value.json().catch(() => ({}));
    runtime = {
      version: typeof infoJson?.version === "string" ? infoJson.version : null,
      authExpired: infoJson?.authExpired === true,
      mode: typeof infoJson?.mode === "string" ? infoJson.mode : null,
    };
  }

  const summary: MachineSummary = {
    projectSummary,
    runtime,
    fetchedAt: Date.now(),
  };
  machineSummaryCache.set(cacheKey, summary);
  return summary;
}

function DeviceCard({
  device,
  isActive,
  authExpired,
  isStale,
  isPrimary,
  onSelect,
  onLongPress,
  onRecoverAuth,
  token,
}: {
  device: Device;
  isActive: boolean;
  authExpired: boolean;
  // isStale = Convex still says online but the last connect we tried
  // failed. Drives the YELLOW badge + the explicit "Try to connect"
  // button instead of the old green/red flicker.
  isStale: boolean;
  isPrimary: boolean;
  onSelect: () => Promise<void> | void;
  onLongPress: () => void;
  onRecoverAuth: () => Promise<void>;
  token: string | null;
}) {
  const c = useColors();
  const [pingState, setPingState] = useState<{ pinging: boolean; rttMs?: number; ok?: boolean }>({ pinging: false });
  const [recovering, setRecovering] = useState(false);
  const [runtimeLabel, setRuntimeLabel] = useState<string | null>(null);
  const [projectSummary, setProjectSummary] = useState<DeviceProjectSummary | null>(null);
  const [agentVersion, setAgentVersion] = useState<string | null>(null);
  const [remoteAuthExpired, setRemoteAuthExpired] = useState(false);
  const [detailsOpen, setDetailsOpen] = useState(false);
  // Seed needsAuth from Convex device record so the badge shows immediately
  // (without waiting for the /info poll to complete).
  const [needsAuth, setNeedsAuth] = useState<boolean>(device.needsAuth === true);
  const [autoPairing, setAutoPairing] = useState(false);
  const [directReachable, setDirectReachable] = useState<boolean | null>(null);
  // Three-state status: green / yellow / gray. The old binary
  // online|offline missed the "Convex thinks live, we can't reach"
  // case which flickered between two wrong answers.
  const authRecoverable = needsAuth || authExpired || remoteAuthExpired;
  const hasBusLiveSignal = device.peerState === "online";
  const hasBusStaleSignal = device.peerState === "stale";
  const isOnline = (device.online || hasBusLiveSignal) && !(isStale && !hasBusLiveSignal) && !authRecoverable;
  const isOffline = !device.online && !hasBusLiveSignal;

  // Keep state in sync when Convex list refreshes
  useEffect(() => {
    setNeedsAuth(device.needsAuth === true);
  }, [device.needsAuth]);

  useEffect(() => {
    setRuntimeLabel(null);
  }, [device.id]);

  useEffect(() => {
    const ctx = buildDeviceRequestContext(device, token);
    if (!ctx || !token) {
      setProjectSummary(null);
      setAgentVersion(null);
      setRemoteAuthExpired(false);
      return;
    }

    let cancelled = false;

    const loadMachineSummary = async (force = false) => {
      try {
        const cacheKey = `${ctx.baseUrl}|${JSON.stringify(ctx.headers)}`;
        const cached = machineSummaryCache.get(cacheKey);
        if (cached && !cancelled) {
          setProjectSummary(cached.projectSummary);
          setAgentVersion(cached.runtime?.version ?? null);
          setRemoteAuthExpired(cached.runtime?.authExpired === true);
        }

        if (!device.online && cached) return;

        const summary = await fetchMachineSummaryWithHeaders(ctx.baseUrl, ctx.headers, { force });
        if (!cancelled) {
          setProjectSummary(summary.projectSummary);
          setAgentVersion(summary.runtime?.version ?? null);
          setRemoteAuthExpired(summary.runtime?.authExpired === true);
        }
      } catch {
        const cacheKey = `${ctx.baseUrl}|${JSON.stringify(ctx.headers)}`;
        const cached = machineSummaryCache.get(cacheKey);
        if (!cancelled) {
          setProjectSummary(cached?.projectSummary ?? null);
          setAgentVersion(cached?.runtime?.version ?? null);
          setRemoteAuthExpired(cached?.runtime?.authExpired === true);
        }
      }
    };

    void loadMachineSummary();
    return () => {
      cancelled = true;
    };
  }, [device.id, device.host, device.port, device.online, token]);

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
        const autoStartType = String(info?.autoStart?.type || "").trim().toLowerCase();
        if (typeof info?.version === "string" && !cancelled) {
          setAgentVersion(info.version);
        }
        if (autoStartType.startsWith("wsl-") && !cancelled) {
          setRuntimeLabel("WSL");
        }
        console.log(`[auto-pair] ${device.name}: needsAuth=${info.needsAuth} mode=${info.mode} → inBootstrap=${inBootstrap}`);
        if (cancelled) return;
        setDirectReachable(true);
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
        if (!cancelled) setDirectReachable(false);
        console.log(`[auto-pair] ${device.name}: error ${err?.message || err}`);
      }
    };
    check();
    const iv = setInterval(check, 8000);
    return () => { cancelled = true; clearInterval(iv); };
  }, [device.host, device.port, device.publicKey, token]);
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

  const platformLabel = formatDevicePlatform(device, runtimeLabel);
  const projectCount = projectSummary?.total ?? 0;
  const statusLabel = needsAuth
    ? "yaver needs auth"
    : (authExpired || remoteAuthExpired)
      ? "yaver expired"
      : isOnline
        ? "authenticated"
        : directReachable
          ? "reachable"
          : (hasBusStaleSignal || isStale)
            ? "stale"
            : "offline";
  const statusTone = authRecoverable
    ? "#f59e0b"
    : isOnline
      ? c.success
      : directReachable
        ? "#38bdf8"
        : (hasBusStaleSignal || isStale)
          ? "#eab308"
          : c.textMuted;
  const primaryActionLabel = authRecoverable
    ? "Recover & Connect"
    : (isStale || isOffline)
      ? "Connect"
      : "Details";
  const primaryActionTone = authRecoverable
    ? "#f59e0b"
    : isStale
      ? "#eab308"
      : c.accent;
  const handleSmartConnect = async () => {
    if (recovering) return;
    if (!authRecoverable) {
      await onSelect();
      return;
    }
    setRecovering(true);
    try {
      await onRecoverAuth();
      await onSelect();
    } finally {
      setRecovering(false);
    }
  };

  return (
    <Pressable
      style={({ pressed }) => [
        styles.card,
        { backgroundColor: c.bgCard, borderColor: isActive ? c.accent : c.border },
        pressed && styles.cardPressed,
      ]}
      onPress={() => { void handleSmartConnect(); }}
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
            {isPrimary ? (
              <View style={{
                paddingHorizontal: 8, paddingVertical: 2, borderRadius: 10,
                backgroundColor: "#6366f122", borderWidth: 1, borderColor: "#6366f166",
              }}>
                <Text style={{ color: "#818cf8", fontSize: 10, fontWeight: "700" }}>PRIMARY ★</Text>
              </View>
            ) : null}
            {isActive ? (
              <View style={{
                paddingHorizontal: 8, paddingVertical: 2, borderRadius: 10,
                backgroundColor: c.accent + "22", borderWidth: 1, borderColor: c.accent + "55",
              }}>
                <Text style={{ color: c.accent, fontSize: 10, fontWeight: "700" }}>ACTIVE</Text>
              </View>
            ) : null}
            {recovering ? (
              <View style={{
                paddingHorizontal: 8, paddingVertical: 2, borderRadius: 10,
                backgroundColor: "#f59e0b22", borderWidth: 1, borderColor: "#f59e0b66",
              }}>
                <Text style={{ color: "#f59e0b", fontSize: 10, fontWeight: "700" }}>RECOVERING…</Text>
              </View>
            ) : autoPairing ? (
              <View style={{
                paddingHorizontal: 8, paddingVertical: 2, borderRadius: 10,
                backgroundColor: "#818cf822", borderWidth: 1, borderColor: "#818cf866",
              }}>
                <Text style={{ color: "#818cf8", fontSize: 10, fontWeight: "700" }}>PAIRING…</Text>
              </View>
            ) : authExpired || remoteAuthExpired ? (
              <View style={{
                paddingHorizontal: 8, paddingVertical: 2, borderRadius: 10,
                backgroundColor: "#f59e0b22", borderWidth: 1, borderColor: "#f59e0b66",
              }}>
                <Text style={{ color: "#f59e0b", fontSize: 10, fontWeight: "700" }}>YAVER EXPIRED</Text>
              </View>
            ) : needsAuth ? (
              <View style={{
                paddingHorizontal: 8, paddingVertical: 2, borderRadius: 10,
                backgroundColor: "#eab30822", borderWidth: 1, borderColor: "#eab30866",
              }}>
                <Text style={{ color: "#eab308", fontSize: 10, fontWeight: "700" }}>YAVER NEEDS AUTH</Text>
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
          <View style={{ marginTop: 6 }}>
            <TransportBadge device={device} />
          </View>
          <Text style={[styles.deviceMeta, { color: c.textMuted }]}>
            {platformLabel} &middot; {device.host}
            {device.isGuest && device.hostName ? ` · shared from ${device.hostName}` : ""}
          </Text>
          <Text style={[styles.deviceMeta, { color: statusTone, marginTop: 4 }]}>
            {statusLabel}
            {device.lastSeen > 0 ? ` · ${timeSince(device.lastSeen)}` : ""}
          </Text>
          <Text style={[styles.deviceMeta, { color: c.textMuted, marginTop: 4 }]}>
            {agentVersion ? `Yaver v${agentVersion}` : "Yaver version unknown"}
            {projectSummary ? ` · ${projectCount} project${projectCount === 1 ? "" : "s"}` : ""}
          </Text>
        </View>
        <View style={styles.cardRight}>
          <View
            style={[
              styles.onlineDot,
              {
                backgroundColor: statusTone,
              },
            ]}
          />
        </View>
      </View>

      <View style={styles.cardBottom}>
        <View style={styles.cardActions}>
          <Pressable
            style={[
              styles.pingBtn,
              {
                backgroundColor: authRecoverable ? "#f59e0b18" : (isStale || isOffline) ? primaryActionTone + "22" : c.accent + "18",
                borderWidth: 1,
                borderColor: authRecoverable ? "#f59e0b44" : (isStale || isOffline) ? primaryActionTone + "55" : c.accent + "40",
                opacity: recovering ? 0.7 : 1,
              },
            ]}
            onPress={() => {
              if (authRecoverable || isStale || isOffline) {
                void handleSmartConnect();
              } else {
                setDetailsOpen(true);
              }
            }}
            disabled={recovering}
          >
            <Text style={[styles.pingBtnText, { color: primaryActionTone, fontWeight: "700" }]}>
              {recovering ? "Recovering..." : primaryActionLabel}
            </Text>
          </Pressable>
          <Pressable
            style={[styles.pingBtn, { backgroundColor: c.accent + "18" }]}
            onPress={() => setDetailsOpen(true)}
          >
            <Text style={[styles.pingBtnText, { color: c.accent, fontWeight: "700" }]}>Details</Text>
          </Pressable>
        </View>
      </View>
      <DeviceDetailsModal
        device={device}
        agentVersion={agentVersion}
        visible={detailsOpen}
        onClose={() => setDetailsOpen(false)}
      />
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
    agentAuthExpired,
    connectionStatus,
    isLoadingDevices,
    recoverDeviceAuth,
    selectDevice,
    disconnect,
    refreshDevices,
    detachDevice,
    removeDevice,
    acceptGuestByCode,
    unreachableDeviceIds,
    primaryDeviceId,
    setPrimaryDevice,
  } = useDevice();

  const [guestCode, setGuestCode] = useState("");
  const [guestLoading, setGuestLoading] = useState(false);
  const [peerStates, setPeerStates] = useState<Record<string, { state: "online" | "stale" | "offline"; lastSeen?: number }>>({});

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

  useEffect(() => {
    if (connectionStatus !== "connected") {
      setPeerStates({});
      return;
    }
    let cancelled = false;
    const refreshPeerStates = async () => {
      try {
        const peers = await quicClient.machinePeers();
        if (cancelled) return;
        const next: Record<string, { state: "online" | "stale" | "offline"; lastSeen?: number }> = {};
        for (const peer of peers) {
          if (!peer?.deviceId) continue;
          const peerLastSeen = Date.parse(peer.lastSeen);
          next[peer.deviceId] = {
            state: peer.state,
            lastSeen: Number.isNaN(peerLastSeen) ? undefined : peerLastSeen,
          };
        }
        setPeerStates(next);
      } catch {
        if (!cancelled) setPeerStates({});
      }
    };
    void refreshPeerStates();
    const interval = setInterval(refreshPeerStates, 5000);
    return () => {
      cancelled = true;
      clearInterval(interval);
    };
  }, [connectionStatus, activeDevice?.id]);

  const displayDevices = devices.map((device) => {
    const peer = peerStates[device.id];
    if (!peer) return device;
    return {
      ...device,
      peerState: peer.state,
      peerLastSeen: peer.lastSeen,
      online: peer.state === "online" ? true : device.online,
      lastSeen: peer.lastSeen && peer.lastSeen > device.lastSeen ? peer.lastSeen : device.lastSeen,
    };
  }).sort((a, b) => {
    const aActive = activeDevice?.id === a.id ? 1 : 0;
    const bActive = activeDevice?.id === b.id ? 1 : 0;
    if (aActive !== bActive) return bActive - aActive;
    const aPrimary = primaryDeviceId === a.id ? 1 : 0;
    const bPrimary = primaryDeviceId === b.id ? 1 : 0;
    if (aPrimary !== bPrimary) return bPrimary - aPrimary;
    return 0;
  });

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
          data={displayDevices}
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
              isStale={unreachableDeviceIds.includes(item.id)}
              isPrimary={primaryDeviceId === item.id}
              onSelect={() => selectDevice(item)}
              authExpired={activeDevice?.id === item.id && connectionStatus === "connected" && agentAuthExpired}
              onLongPress={() => {
                const actionLabel = item.isGuest ? "Detach" : "Remove";
                const message = item.isGuest
                  ? "Remove this shared machine from your list? It will reappear if the host shares it again."
                  : "Remove this device from your account? The node will need to re-register before it shows up again.";
                // Guest machines can't be the primary — they can vanish on host revocation.
                const isThisPrimary = primaryDeviceId === item.id;
                const primaryAction = item.isGuest
                  ? null
                  : isThisPrimary
                    ? { text: "Unset primary", onPress: async () => {
                        try { await setPrimaryDevice(null); } catch (e: any) { Alert.alert("Error", e?.message || "Failed"); }
                      } }
                    : { text: "Set as primary", onPress: async () => {
                        try { await setPrimaryDevice(item.id); } catch (e: any) { Alert.alert("Error", e?.message || "Failed"); }
                      } };
                const buttons: any[] = [{ text: "Cancel", style: "cancel" }];
                if (primaryAction) buttons.push(primaryAction);
                buttons.push({
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
                });
                Alert.alert(item.name, message, buttons);
              }}
              onRecoverAuth={async () => {
                try {
                  const result = await recoverDeviceAuth(item);
                  if (result?.ok && result.mode === "device-code") {
                    Alert.alert("Continue In Browser", "Finish sign-in in your phone browser. Yaver already opened the authorization page.");
                    return;
                  }
                  if (result?.ok) {
                    Alert.alert("Recovered", `${item.name} is signing back into Yaver now.`);
                    return;
                  }
                  Alert.alert("Recovery Failed", result?.error || "Could not recover this machine from the phone.");
                } catch (e: any) {
                  Alert.alert("Recovery Failed", e?.message || "Could not recover this machine from the phone.");
                }
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
  scopeSection: { marginTop: 8, gap: 6 },
  machineSummarySection: { marginTop: 10 },
  scopeRow: { flexDirection: "row", flexWrap: "wrap", gap: 6 },
  scopeChip: {
    paddingHorizontal: 8,
    paddingVertical: 3,
    borderRadius: 999,
    borderWidth: 1,
  },
  scopeChipText: { fontSize: 10, fontWeight: "700" },
  cardRight: { alignItems: "flex-end" },
  onlineDot: { width: 8, height: 8, borderRadius: 4, marginBottom: 4 },
  lastSeen: { fontSize: 11 },
  cardBottom: {
    flexDirection: "column",
    alignItems: "flex-start",
    marginTop: 10,
    gap: 8,
  },
  cardActions: {
    flexDirection: "row",
    flexWrap: "wrap",
    alignItems: "flex-start",
    gap: 8,
    width: "100%",
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
    maxWidth: "100%",
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
  menuActions: { marginTop: 8, paddingTop: 8, borderTopWidth: 1, flexDirection: "row", flexWrap: "wrap", gap: 8 },
  menuActionBtn: { paddingHorizontal: 12, paddingVertical: 6, borderRadius: 6, alignSelf: "flex-start" },
  menuActionText: { fontSize: 12, fontWeight: "600" },
});
