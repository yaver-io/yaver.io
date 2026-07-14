import React, { useCallback, useEffect, useMemo, useState } from "react";
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
import { getConvexSiteUrlSync } from "../../src/lib/backendConfig";
import { useLocalSearchParams, router } from "expo-router";
import { Device, useDevice } from "../../src/context/DeviceContext";
import { appTag } from "../../src/lib/appVersion";
import { useAuth } from "../../src/context/AuthContext";
import { useColors, useTheme } from "../../src/context/ThemeContext";
import { chipPalette } from "../../src/lib/chipPalette";
import { quicClient } from "../../src/lib/quic";
import DeviceDetailsModal from "../../src/components/DeviceDetailsModal";
import { AppScreenHeader } from "../../src/components/AppScreenHeader";
import { beaconListener, type DiscoveredDevice } from "../../src/lib/beacon";
import { submitPair, fetchPairInfo } from "../../src/lib/pairDevice";
import {
  classifyTransport,
  fetchRelayHealth,
  transportToneRGB,
  type TransportInfo,
} from "../../src/lib/transport";
import {
  deriveMobileDeviceLifecycleState,
  probeMobileDeviceStatus,
  type MobileDeviceLifecycleState,
  type MobileDeviceStatusProbe,
} from "../../src/lib/deviceStatus";
import { lightCardShadow, spacing, typography } from "../../src/theme/tokens";
import { useResponsiveLayout } from "../../src/hooks/useResponsiveLayout";
import { useTabletContentStyle } from "../../src/hooks/useTabletContentStyle";

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
  const c = useColors();
  const t = transportFor(device);
  const toneColor =
    t.tone === "emerald" ? c.success
    : t.tone === "blue" ? c.info
    : t.tone === "amber" ? c.warn
    : c.textSecondary;
  return (
    <View style={[styles.neutralPill, { backgroundColor: c.bgInput, borderColor: c.borderSubtle }]}>
      <View style={[styles.neutralPillDot, { backgroundColor: toneColor }]} />
      <Text style={[styles.neutralPillText, { color: c.textSecondary }]}>{t.label}</Text>
    </View>
  );
}

function StatusChip({ tone, label, isDark }: {
  tone: import("../../src/lib/chipPalette").ChipTone;
  label: string;
  isDark: boolean;
}) {
  const c = useColors();
  const toneColor =
    tone === "emerald" ? c.success
    : tone === "blue" ? c.info
    : tone === "violet" || tone === "indigo" ? c.accent
    : tone === "amber" ? c.warn
    : c.textSecondary;
  const lead =
    label.includes("★") ? "★"
    : label.includes("☆") ? "☆"
    : label === "CONNECTED" || label.includes("relay") || label.includes("READY") ? "●"
    : null;
  const cleanLabel = label.replace(" ★", "").replace(" ☆", "").replace("YAVER AUTH EXPIRED", "Auth expired").replace("READY TO CONNECT", "Ready").replace("CONNECTED", "Connected").replace("BOOTSTRAP", "Bootstrap").replace("SHARED", "Shared").replace("PRIMARY", "Primary").replace("SECONDARY", "Secondary").replace("RECOVERING…", "Recovering…").replace("PAIRING…", "Pairing…");
  return (
    <View style={[styles.neutralPill, { backgroundColor: c.bgInput, borderColor: c.borderSubtle }]}>
      {lead ? <Text style={[styles.neutralPillLead, { color: toneColor }]}>{lead}</Text> : <View style={[styles.neutralPillDot, { backgroundColor: toneColor }]} />}
      <Text style={[styles.neutralPillText, { color: c.textSecondary }]}>{cleanLabel}</Text>
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

// labelForRunnerId humanizes a stored runner id ("claude" / "codex" /
// "opencode" / "claude-code" legacy alias) for the device card badge.
// Defaults to the raw id if we don't recognize it — better to show
// something than nothing.
function labelForRunnerId(id: string): string {
  switch (id) {
    case "claude":
    case "claude-code":
      return "Claude";
    case "codex":
      return "Codex";
    case "opencode":
      return "OpenCode";
    default:
      return id;
  }
}

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

function ConnectionBadge({ status, label }: { status: string; label?: string }) {
  const c = useColors();
  const color =
    status === "connected" ? c.success
    : status === "connecting" ? c.warn
    : status === "error" ? c.error
    : c.textMuted;
  return (
    <View style={[styles.connBadge, { backgroundColor: color + "22" }]}>
      <View style={[styles.connDot, { backgroundColor: color }]} />
      <Text style={[styles.connText, { color }]}>{label || status}</Text>
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
  connectionStatus,
  authExpired,
  isStale,
  isPrimary,
  isSecondary,
  isPooledConnected,
  defaultRunner,
  onSelect,
  onLongPress,
  onRecoverAuth,
  onSetSecondary,
  onUnsetSecondary,
  onSetPrimary,
  token,
  forceDetailsOpen,
  onOpenDetails,
}: {
  device: Device;
  isActive: boolean;
  connectionStatus: "disconnected" | "connecting" | "connected" | "error";
  authExpired: boolean;
  // isStale = Convex still says online but the last connect we tried
  // failed. Drives the YELLOW badge + the explicit "Try to connect"
  // button instead of the old green/red flicker.
  isStale: boolean;
  isPrimary: boolean;
  // Optional secondary slot. Same elevated treatment as primary
  // for the watchdog + auto-connect. Renders ☆ instead of ★.
  isSecondary?: boolean;
  // True when the connection-manager pool currently has a live
  // QuicClient for this device — used to show the CONNECTED badge
  // even on non-focused boxes once the user opens parallel
  // connections from the Devices tab. Without it, only `isActive`
  // (focused) devices ever showed CONNECTED, hiding the rest of
  // the multi-device pool from the UI.
  isPooledConnected?: boolean;
  // Runner the user picked as the default for this device in
  // DeviceDetailsModal. Empty string means none picked. Surfaced as a
  // small badge on the card so the user can see which agent runs
  // without having to open the details modal.
  defaultRunner: string;
  onSelect: () => Promise<void> | void;
  onLongPress: () => void;
  onRecoverAuth: () => Promise<void>;
  /** One-tap setters for the primary / secondary roles, surfaced as
   *  pill buttons on the card so the user doesn't have to discover
   *  the long-press menu just to mark a fallback box. The long-press
   *  flow stays as-is for guest devices and the destructive Remove. */
  onSetSecondary?: () => void;
  onUnsetSecondary?: () => void;
  onSetPrimary?: () => void;
  token: string | null;
  // When true (set by DevicesScreen via openDetails query param —
  // e.g. the "Open recovery" alert from DeviceContext fires
  // router.push("/(tabs)/devices?openDetails=<deviceId>")), the
  // matching card opens its DeviceDetailsModal automatically. Used
  // for the auto-guide-to-recovery flow on the active device.
  forceDetailsOpen?: boolean;
  // When set, the card defers to the parent for showing details
  // (tablet master-detail) instead of opening its own modal.
  onOpenDetails?: () => void;
}) {
  const c = useColors();
  const { isDark } = useTheme();
  const [pingState, setPingState] = useState<{ pinging: boolean; rttMs?: number; ok?: boolean }>({ pinging: false });
  const [recovering, setRecovering] = useState(false);
  const [runtimeLabel, setRuntimeLabel] = useState<string | null>(null);
  const [projectSummary, setProjectSummary] = useState<DeviceProjectSummary | null>(null);
  const [agentVersion, setAgentVersion] = useState<string | null>(null);
  const [remoteAuthExpired, setRemoteAuthExpired] = useState(false);
  const [detailsOpen, setDetailsOpenLocal] = useState(false);
  const [boxBusy, setBoxBusy] = useState(false);

  // Up/down for a Yaver-hosted (managed) box — same Convex route the web uses.
  // "stop" = snapshot + delete the server so billing halts; "start" = recreate
  // from the pause snapshot (~2-3 min). Entitlement/safety is server-side.
  const handlePauseResume = useCallback(
    (action: "stop" | "start") => {
      if (!token || !device.machineId) return;
      const verb = action === "stop" ? "Pause" : "Resume";
      Alert.alert(
        `${verb} ${device.name}?`,
        action === "stop"
          ? "Snapshots the disk, then deletes the cloud server so it stops billing. Resume recreates it from the snapshot in ~2-3 min (new IP)."
          : "Recreates the box from its pause snapshot (~2-3 min).",
        [
          { text: "Cancel", style: "cancel" },
          {
            text: verb,
            style: action === "stop" ? "destructive" : "default",
            onPress: async () => {
              setBoxBusy(true);
              try {
                const res = await fetch(`${getConvexSiteUrlSync()}/billing/yaver-cloud/${action}`, {
                  method: "POST",
                  headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/json" },
                  body: JSON.stringify({ machineId: device.machineId }),
                });
                const j = await res.json().catch(() => ({}) as any);
                if (!res.ok) Alert.alert(`${verb} failed`, j?.error || `HTTP ${res.status}`);
              } catch (e: any) {
                Alert.alert(`${verb} failed`, e?.message || String(e));
              } finally {
                setBoxBusy(false);
              }
            },
          },
        ],
      );
    },
    [token, device.machineId, device.name],
  );
  // openDetails routes through the parent (master-detail right
  // pane) when onOpenDetails is provided; otherwise pops the
  // local modal as before. All four call sites (smart-connect,
  // primary button, Details button, forceDetailsOpen effect) go
  // through this helper.
  const setDetailsOpen = (open: boolean) => {
    if (open && onOpenDetails) {
      onOpenDetails();
      return;
    }
    setDetailsOpenLocal(open);
  };
  useEffect(() => {
    if (forceDetailsOpen) setDetailsOpen(true);
  }, [forceDetailsOpen]);
  // Seed needsAuth from Convex device record so the badge shows immediately
  // (without waiting for the /info poll to complete).
  const [needsAuth, setNeedsAuth] = useState<boolean>(device.needsAuth === true);
  const [autoPairing, setAutoPairing] = useState(false);
  const [statusProbe, setStatusProbe] = useState<MobileDeviceStatusProbe | null>(null);
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

  // Probe relay/direct /info for bootstrap/auth state — shows the
  // correct device lifecycle and auto-pairs boxes that are up in
  // bootstrap mode even when the direct host address is not reachable
  // from the phone.
  useEffect(() => {
    if (!device.host) return;
    let cancelled = false;
    let paired = false;
    const check = async () => {
      if (paired || cancelled) return;
      try {
        const probe = await probeMobileDeviceStatus(device, token, 3000);
        if (cancelled) return;
        setStatusProbe(probe);
        const info = probe.info || {};
        const inBootstrap = probe.bootstrap;
        const autoStartType = String((info as any)?.autoStart?.type || "").trim().toLowerCase();
        if (typeof (info as any)?.version === "string" && !cancelled) {
          setAgentVersion((info as any).version);
        }
        if (autoStartType.startsWith("wsl-") && !cancelled) {
          setRuntimeLabel("WSL");
        }
        if (cancelled) return;
        setNeedsAuth(inBootstrap);
        if (!inBootstrap) return;
        if (!token) return;
        setAutoPairing(true);
        try {
          const { submitEncryptedPair } = await import("../../src/lib/encryptedPair");
          const { submitPair } = await import("../../src/lib/pairDevice");
          const targetUrl =
            probe.path === "relay" && quicClient.getRelayServers()[0]
              ? `${quicClient.getRelayServers()[0].httpUrl}/d/${device.id}`
              : `http://${device.host}:${device.port || 18080}`;
          const pubKey = device.publicKey || (info as any).devicePublicKey;
          if (pubKey) {
            const r = await submitEncryptedPair(targetUrl, token, pubKey, (info as any).bootstrapPasskey);
            if (r.ok) {
              paired = true;
              setNeedsAuth(false);
              setStatusProbe((prev) => prev ? { ...prev, bootstrap: false } : prev);
              return;
            }
          }
          const passkey = (info as any).bootstrapPasskey;
          if (passkey) {
            const r = await submitPair({ code: passkey, targetUrl, token, userId: "" });
            if (r.ok) {
              paired = true;
              setNeedsAuth(false);
              setStatusProbe((prev) => prev ? { ...prev, bootstrap: false } : prev);
            }
          }
        } finally {
          if (!cancelled) setAutoPairing(false);
        }
      } catch {
        if (!cancelled) setStatusProbe(null);
      }
    };
    check();
    // Probe interval: 8s baseline, but drop to 3s when the device
    // is in a degraded auth state (bootstrap or auth-expired). This
    // closes the "stale banner" window — once the agent recovers
    // (either via the user's reauth tap or the agent's own retry
    // loop), the next probe lands within ~3s instead of up to 8s,
    // so the banner clears almost as soon as the device is healthy.
    let currentInterval = 8000;
    let iv = setInterval(check, currentInterval);
    const adjustInterval = () => {
      const wantsFast = needsAuth || authExpired || remoteAuthExpired;
      const target = wantsFast ? 3000 : 8000;
      if (target !== currentInterval) {
        clearInterval(iv);
        currentInterval = target;
        iv = setInterval(check, currentInterval);
      }
    };
    const adjustIv = setInterval(adjustInterval, 1500);
    return () => { cancelled = true; clearInterval(iv); clearInterval(adjustIv); };
  }, [device.id, device.host, device.port, device.publicKey, token, needsAuth, authExpired, remoteAuthExpired]);
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
  const isConnecting = isActive && connectionStatus === "connecting";
  // CONNECTED reflects "is there a live pooled client for this device"
  // — true for the focused device when its connectionStatus is
  // "connected", AND for any other box in the multi-device pool that's
  // currently up. The user explicitly asked for parallel connections
  // to be visible from this tab, so a pooled-but-not-focused box
  // shouldn't be misreported as ready-to-connect.
  const isConnected = (isActive && connectionStatus === "connected") || !!isPooledConnected;
  const lifecycleState: MobileDeviceLifecycleState = deriveMobileDeviceLifecycleState({
    device,
    probe: statusProbe,
    authExpired: authExpired || remoteAuthExpired,
    isConnected,
    unreachable: isStale,
  });
  const statusLabel =
    isConnecting
      ? "connecting"
      : lifecycleState === "connected"
      ? "connected"
      : lifecycleState === "bootstrap"
        ? "bootstrap"
        : lifecycleState === "yaver-auth-expired"
          ? "yaver auth expired"
          : lifecycleState === "ready-to-connect"
            ? "ready to connect"
            : "offline";
  const statusChipTone: import("../../src/lib/chipPalette").ChipTone =
    isConnecting
      ? "amber"
      : lifecycleState === "connected"
        ? "emerald"
        : lifecycleState === "bootstrap"
          ? "violet"
          : lifecycleState === "yaver-auth-expired"
            ? "amber"
            : lifecycleState === "ready-to-connect"
              ? "blue"
              : "slate";
  const statusChip = chipPalette(statusChipTone, isDark);
  // statusTone drives the descriptive line + the right-side dot. Use the
  // palette text color (theme-aware), except for the offline state which
  // should defer to the muted theme color so it doesn't shout.
  const statusTone =
    lifecycleState === "offline" ? c.textMuted : statusChip.text;
  const primaryActionLabel =
    lifecycleState === "bootstrap"
      ? "Reclaim & Connect"
      : lifecycleState === "yaver-auth-expired"
        ? "Re-auth & Connect"
        : lifecycleState === "connected" && !isActive
          ? "Use This Device"
        : lifecycleState === "ready-to-connect"
          ? "Connect"
          : "Details";
  const primaryActionTone =
    lifecycleState === "bootstrap"
      ? chipPalette("violet", isDark).text
      : lifecycleState === "yaver-auth-expired"
        ? chipPalette("amber", isDark).text
        : lifecycleState === "ready-to-connect"
          ? chipPalette("blue", isDark).text
          : c.accent;
  const handleSmartConnect = async () => {
    if (recovering) return;
    // Pooled-connected but not the focused device — switch focus to it.
    if (lifecycleState === "connected" && !isActive) {
      await onSelect();
      return;
    }
    // Offline device → only place where the silent Details modal
    // remains. The user can see why it's offline + try recovery
    // actions from there.
    if (lifecycleState === "offline") {
      setDetailsOpen(true);
      return;
    }
    // Already-connected AND focused: previously this also opened
    // the Details modal silently, which read as "Devices can't
    // connect to my primary" — the connection-status banner could
    // still show "Connecting" while the card said Ready, and a
    // tap did nothing visible. Re-selecting refreshes the focus,
    // clears any stale "connecting" pill, and re-runs the connect
    // probe so a half-connected pool client gets repaired. iOS
    // shares this code path — same fix.
    if (lifecycleState === "connected") {
      await onSelect();
      return;
    }
    if (lifecycleState === "ready-to-connect") {
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
        {
          backgroundColor: c.bgCard,
          borderColor: c.borderSubtle,
          borderWidth: 1,
          shadowColor: !isDark ? c.shadowSm : "transparent",
          shadowOpacity: !isDark ? 0.14 : 0,
          shadowRadius: !isDark ? 12 : 0,
          shadowOffset: { width: 0, height: 4 },
          elevation: !isDark ? 2 : 0,
        },
        pressed && styles.cardPressed,
      ]}
      onPress={() => { void handleSmartConnect(); }}
      onLongPress={onLongPress}
    >
      <View style={styles.cardRow}>
        <View style={styles.cardInfo}>
          <View style={{ flexDirection: "row", alignItems: "center", gap: 6, flexWrap: "wrap" }}>
            <Text style={[styles.deviceName, { color: c.textPrimary }]}>{device.name}</Text>
            {device.isGuest ? <StatusChip tone="blue" label="SHARED" isDark={isDark} /> : null}
            {!device.isGuest && device.hosting === "yaver-hosted" ? <StatusChip tone="blue" label="YAVER-HOSTED" isDark={isDark} /> : null}
            {!device.isGuest && device.hosting === "byo" ? <StatusChip tone="amber" label="BYO" isDark={isDark} /> : null}
            {!device.isGuest && device.hosting === "self-hosted" ? <StatusChip tone="slate" label="SELF-HOSTED" isDark={isDark} /> : null}
            {isPrimary ? <StatusChip tone="indigo" label="PRIMARY ★" isDark={isDark} /> : null}
            {!isPrimary && isSecondary ? <StatusChip tone="violet" label="SECONDARY ☆" isDark={isDark} /> : null}
            {defaultRunner ? <StatusChip tone="violet" label={`★ ${labelForRunnerId(defaultRunner)}`} isDark={isDark} /> : null}
            {recovering ? (
              <StatusChip tone="amber" label="RECOVERING…" isDark={isDark} />
            ) : autoPairing ? (
              <StatusChip tone="indigo" label="PAIRING…" isDark={isDark} />
            ) : lifecycleState === "bootstrap" ? (
              <StatusChip tone="violet" label="BOOTSTRAP" isDark={isDark} />
            ) : lifecycleState === "yaver-auth-expired" ? (
              <StatusChip tone="amber" label="YAVER AUTH EXPIRED" isDark={isDark} />
            ) : isConnecting ? (
              <StatusChip tone="amber" label="CONNECTING" isDark={isDark} />
            ) : lifecycleState === "connected" ? (
              <StatusChip tone="emerald" label="CONNECTED" isDark={isDark} />
            ) : lifecycleState === "ready-to-connect" ? (
              <StatusChip tone="blue" label="READY TO CONNECT" isDark={isDark} />
            ) : null}
          </View>
          <View style={{ marginTop: 6 }}>
            <TransportBadge device={device} />
          </View>
          <Text style={[styles.deviceMeta, { color: c.textSecondary }]}>
            {[platformLabel, device.host, device.isGuest && device.hostName ? `shared from ${device.hostName}` : ""].filter(Boolean).join(" · ")}
          </Text>
          <Text style={[styles.deviceMeta, { color: statusTone, marginTop: 4 }]}>
            {statusLabel}
            {device.lastSeen > 0 ? ` · ${timeSince(device.lastSeen)}` : ""}
          </Text>
          {lifecycleState === "bootstrap" || lifecycleState === "yaver-auth-expired" || lifecycleState === "ready-to-connect" || lifecycleState === "offline" || isActive ? (
            <Text style={[styles.deviceMeta, { color: c.textSecondary, marginTop: 4 }]}>
              {lifecycleState === "bootstrap"
                ? "Machine is up in bootstrap mode. Reclaim Yaver from this phone."
                : lifecycleState === "yaver-auth-expired"
                  ? "Machine is up, but the agent session expired."
                  : lifecycleState === "ready-to-connect"
                    ? "Recent heartbeat — reachability not verified yet."
                    : lifecycleState === "offline"
                      ? "No recent heartbeat. Power on and run yaver serve."
                      : "This is the phone you're using."}
            </Text>
          ) : null}
          {agentVersion || projectSummary ? (
            <Text style={[styles.deviceMeta, { color: c.textSecondary, marginTop: 4 }]}>
              {[agentVersion ? `Yaver v${agentVersion}` : "", projectSummary ? `${projectCount} project${projectCount === 1 ? "" : "s"}` : ""].filter(Boolean).join(" · ")}
            </Text>
          ) : null}
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
                backgroundColor: "transparent",
                borderWidth: 1,
                borderColor: c.accent + "55",
                opacity: recovering ? 0.7 : 1,
              },
            ]}
            onPress={() => {
              if (lifecycleState !== "connected") {
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
          {primaryActionLabel === "Details" ? null : (
            <Pressable
              style={[styles.pingBtn, { backgroundColor: "transparent", borderWidth: 1, borderColor: c.accent + "55" }]}
              onPress={() => setDetailsOpen(true)}
            >
              <Text style={[styles.pingBtnText, { color: c.accent, fontWeight: "700" }]}>Details</Text>
            </Pressable>
          )}
          {/* Inline elevation actions. Guest devices and the device
              that's already primary get nothing here — the long-press
              menu still owns those edge cases. Otherwise we surface
              "Make secondary" / "Unmark secondary" as a pill so the
              fallback role is reachable in a single tap from the list. */}
          {!device.isGuest && !isPrimary ? (
            isSecondary && onUnsetSecondary ? (
              <Pressable
                style={[styles.pingBtn, { backgroundColor: "transparent", borderWidth: 1, borderColor: c.accent + "55" }]}
                onPress={onUnsetSecondary}
              >
                <Text style={[styles.pingBtnText, { color: c.accent, fontWeight: "700" }]}>★ Unmark Secondary</Text>
              </Pressable>
            ) : onSetSecondary ? (
              <Pressable
                style={[styles.pingBtn, { backgroundColor: "transparent", borderWidth: 1, borderColor: c.accent + "55" }]}
                onPress={onSetSecondary}
              >
                <Text style={[styles.pingBtnText, { color: c.accent, fontWeight: "700" }]}>☆ Make Secondary</Text>
              </Pressable>
            ) : null
          ) : null}
          {!device.isGuest && !isPrimary && !isSecondary && onSetPrimary ? (
            <Pressable
              style={[styles.pingBtn, { backgroundColor: "transparent", borderWidth: 1, borderColor: c.accent + "55" }]}
              onPress={onSetPrimary}
            >
              <Text style={[styles.pingBtnText, { color: c.accent, fontWeight: "700" }]}>★ Make Primary</Text>
            </Pressable>
          ) : null}
          {/* Up/down for a Yaver-hosted (managed) box. Resume when paused/stopped,
              else Pause. Self-hosted boxes have no machineId ⇒ nothing here. */}
          {!device.isGuest && device.machineId ? (
            device.machineStatus === "paused" ||
            device.machineStatus === "stopped" ||
            device.machineStatus === "suspended" ? (
              <Pressable
                style={[styles.pingBtn, { backgroundColor: "transparent", borderWidth: 1, borderColor: c.success + "55", opacity: boxBusy ? 0.6 : 1 }]}
                onPress={() => { void handlePauseResume("start"); }}
                disabled={boxBusy}
              >
                <Text style={[styles.pingBtnText, { color: c.success, fontWeight: "700" }]}>{boxBusy ? "…" : "▲ Resume"}</Text>
              </Pressable>
            ) : (
              <Pressable
                style={[styles.pingBtn, { backgroundColor: "transparent", borderWidth: 1, borderColor: c.warn + "55", opacity: boxBusy ? 0.6 : 1 }]}
                onPress={() => { void handlePauseResume("stop"); }}
                disabled={boxBusy}
              >
                <Text style={[styles.pingBtnText, { color: c.warn, fontWeight: "700" }]}>{boxBusy ? "…" : "▼ Pause"}</Text>
              </Pressable>
            )
          ) : null}
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
          <Text style={[styles.stepLabel, { color: c.textSecondary }]}>1. Install with npm</Text>
          <CopyableCommand command="npm install -g yaver-cli" />

          <Text style={[styles.stepLabel, { color: c.textSecondary }]}>2. Sign in & start</Text>
          <CopyableCommand command="yaver auth" />
        </View>
      )}

      {platform === "linux" && (
        <View style={styles.steps}>
          <Text style={[styles.stepLabel, { color: c.textSecondary }]}>1. Install with npm</Text>
          <CopyableCommand command="npm install -g yaver-cli" />

          <Text style={[styles.stepLabel, { color: c.textSecondary }]}>2. Sign in & start</Text>
          <CopyableCommand command="yaver auth" />
        </View>
      )}

      {platform === "windows" && (
        <View style={styles.steps}>
          <Text style={[styles.stepLabel, { color: c.textSecondary }]}>1. Install with npm inside WSL2</Text>
          <CopyableCommand command="npm install -g yaver-cli" />

          <Text style={[styles.stepLabel, { color: c.textSecondary }]}>Supported Windows path</Text>
          <Text style={[styles.stepHint, { color: c.textMuted }]}>
            Run Yaver inside WSL2, then sign in from the browser that opens on Windows.
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
  const layout = useResponsiveLayout();
  const tabletContent = useTabletContentStyle("wide");
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
    disconnectDevice,
    refreshDevices,
    detachDevice,
    removeDevice,
    acceptGuestByCode,
    unreachableDeviceIds,
    primaryDeviceId,
    setPrimaryDevice,
    secondaryDeviceId,
    setSecondaryDevice,
    primaryRunnerByDevice,
    pendingClaims,
    refreshPendingClaims,
    claimPendingDevice,
    connectedDeviceIds,
  } = useDevice();
  const connectedSet = useMemo(() => new Set(connectedDeviceIds), [connectedDeviceIds]);
  const anyPoolConnected = connectedDeviceIds.length > 0;
  // Cards already treat the connection pool as the source of truth:
  // any device with a live pooled QuicClient renders CONNECTED even if
  // the focused client is mid-switch. Promote the header banner the
  // same way so it doesn't claim "connecting" while multiple cards
  // directly below it are green.
  const effectiveConnectionStatus: "disconnected" | "connecting" | "connected" =
    connectionStatus === "connected"
      ? "connected"
      : connectionStatus === "error"
        ? "connecting"
        : anyPoolConnected
          ? "connected"
          : connectionStatus;
  const connectionBadgeLabel =
    effectiveConnectionStatus === "connected" && connectedDeviceIds.length > 1
      ? `${connectedDeviceIds.length} connected`
      : effectiveConnectionStatus;
  const [pendingBusyId, setPendingBusyId] = useState<string | null>(null);

  // Auto-open device details when navigated in with ?openDetails=<id>.
  // DeviceContext fires router.push("/(tabs)/devices?openDetails=...")
  // when the active device hits auth-expired and the silent recovery
  // loop fails — that drops the user directly on the per-card
  // DeviceDetailsModal scrolled to the Recover Yaver Auth button so
  // they can run the manual recovery without hunting through the UI.
  const params = useLocalSearchParams<{ openDetails?: string; focus?: string }>();
  const openDetailsId = typeof params.openDetails === "string" ? params.openDetails : "";
  // Clear the param once we've consumed it so back-navigation /
  // refresh doesn't re-open the modal endlessly.
  useEffect(() => {
    if (!openDetailsId) return;
    const t = setTimeout(() => {
      router.setParams({ openDetails: undefined, focus: undefined } as any);
    }, 800);
    return () => clearTimeout(t);
  }, [openDetailsId]);

  const [guestCode, setGuestCode] = useState("");
  const [guestLoading, setGuestLoading] = useState(false);
  const [peerStates, setPeerStates] = useState<Record<string, { state: "online" | "stale" | "offline"; lastSeen?: number }>>({});
  // Tablet master-detail: when in landscape, picking a device on
  // the left list opens its details in a persistent right pane
  // instead of a modal. Phone + portrait keep the modal flow.
  const useMasterDetail = layout.layoutClass === "tablet-landscape";
  const [selectedDetailDeviceId, setSelectedDetailDeviceId] = useState<string | null>(null);
  const detailDevice = useMasterDetail
    ? devices.find((d) => d.id === selectedDetailDeviceId) ?? null
    : null;
  // Auto-select first device when entering master-detail with no
  // selection — empty right pane reads as a bug otherwise.
  useEffect(() => {
    if (!useMasterDetail) return;
    if (selectedDetailDeviceId) return;
    if (devices.length > 0) setSelectedDetailDeviceId(devices[0].id);
  }, [useMasterDetail, selectedDetailDeviceId, devices]);
  const gridCols = layout.gridCols("devices");
  // Suppress numColumns when in master-detail so the narrow list
  // stays single-column. Tablet portrait keeps 2-col grid.
  const listNumColumns = useMasterDetail ? 1 : gridCols;

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
    // Sort priority — high to low — keeping the role-aware ordering
    // even when the user has multiple connected boxes:
    //   1. CONNECTED primary  (the box new tasks default to)
    //   2. CONNECTED secondary (watchdog fallback)
    //   3. CONNECTED active focus (whatever's selected right now)
    //   4. Other connected (pool clients live but not focused)
    //   5. Primary offline (still pinned above stale rows)
    //   6. Secondary offline
    //   7. Active focus offline
    //   8. Everything else (registration order preserved)
    // The previous sort only pinned active + primary — secondary and
    // other connected boxes fell back to "registration order", which
    // shoved a connected secondary below disconnected/offline rows.
    const isConnected = (id: string) =>
      connectedSet.has(id) || (activeDevice?.id === id && connectionStatus === "connected");
    const rank = (id: string): number => {
      const conn = isConnected(id);
      if (id === primaryDeviceId) return conn ? 0 : 4;
      if (id === secondaryDeviceId) return conn ? 1 : 5;
      if (id === activeDevice?.id) return conn ? 2 : 6;
      if (conn) return 3;
      return 7;
    };
    const ra = rank(a.id);
    const rb = rank(b.id);
    if (ra !== rb) return ra - rb;
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
      // Inline classifier (mirrors more.tsx's guest-invite error shape, kept
      // local so we don't reach into another agent's file). Never surface the
      // raw e.message as the primary line.
      const raw = e instanceof Error ? e.message : String(e);
      const lower = raw.toLowerCase();
      const friendly = /expired|expire/.test(lower)
        ? "Invite codes expire after 2 days — ask the host to resend."
        : /network|fetch|timeout|econn|offline|unreach|connection/.test(lower)
          ? "Couldn't reach the server — check your connection."
          : /not found|invalid|no such|bad code|unknown/.test(lower)
            ? "Double-check the 6-char code."
            : "Couldn't join with that code. Double-check it and try again.";
      Alert.alert("Couldn't join", friendly);
    }
    setGuestLoading(false);
  };

  return (
    <SafeAreaView style={[styles.safeArea, { backgroundColor: c.bg }]} edges={["bottom"]}>
      <AppScreenHeader title="Devices" onBack={() => router.navigate("/(tabs)/more" as any)} />
      <View style={[styles.container, useMasterDetail ? { flexDirection: "row" } : null]}>
      <View style={useMasterDetail ? { width: 380, borderRightWidth: 1, borderRightColor: c.border } : { flex: 1 }}>
        {(activeDevice || anyPoolConnected) && effectiveConnectionStatus !== "disconnected" && (
          <View style={[styles.statusBar, { borderBottomColor: c.border }]}>
            <ConnectionBadge status={effectiveConnectionStatus} label={connectionBadgeLabel} />
            {(anyPoolConnected || activeDevice) && (
              <Pressable style={styles.disconnectBtn} onPress={disconnect}>
                <Text style={[styles.disconnectText, { color: c.error }]}>Disconnect</Text>
              </Pressable>
            )}
          </View>
        )}

        {/* Guest code input */}
        <View style={[styles.guestCodeRow, { borderBottomColor: c.border }]}>
          <TextInput
            style={[styles.guestCodeInput, { backgroundColor: c.bgCard, borderColor: c.borderSubtle, color: c.textPrimary }]}
            placeholder="Invite code"
            placeholderTextColor={c.textMuted}
            value={guestCode}
            onChangeText={setGuestCode}
            autoCapitalize="characters"
            maxLength={6}
          />
          <Pressable
            style={[styles.guestCodeBtn, { backgroundColor: guestCode.trim().length >= 6 ? c.accent : c.accent + "33" }]}
            onPress={handleAcceptGuestCode}
            disabled={guestCode.trim().length < 6 || guestLoading}
          >
            <Text style={styles.guestCodeBtnText}>{guestLoading ? "..." : "Join"}</Text>
          </Pressable>
        </View>

        {/* Zero-touch: claim a Yaver-powered device by scanning its label QR
            (DPP-style). Opens the camera scanner; the box self-credentials
            on its next boot. See app/provision-add.tsx. */}
        <Pressable
          style={[styles.addDeviceBtn, { borderColor: c.accent }]}
          onPress={() => router.push("/provision-add")}
        >
          <Text style={[styles.addDeviceBtnText, { color: c.accent }]}>+ Add a device (scan QR)</Text>
        </Pressable>

        {/* Pending claims section: fresh yaver boxes that joined the
            user's relay but have no Convex devices row yet. Different
            reachability path from the LAN bootstrap section above
            (those use UDP beacon discovery, these are surfaced via
            the relay -> Convex pending-claim table) but the user
            intent is the same: adopt the box so it shows up as a
            normal device. */}
        {pendingClaims.length > 0 && (
          <View style={{ paddingHorizontal: 16, paddingTop: 12 }}>
            <Text style={{ color: c.textMuted, fontSize: 12, fontWeight: "600", marginBottom: 6 }}>
              PENDING CLAIMS ({pendingClaims.length})
            </Text>
            <Text style={{ color: c.textMuted, fontSize: 12, marginBottom: 10 }}>
              Boxes joined your relay but haven&apos;t been signed in yet. Tap Claim to add them to your account.
            </Text>
            {pendingClaims.map((pc) => {
              const isBusy = pendingBusyId === pc.deviceId;
              return (
                <Pressable
                  key={pc.id}
                  onPress={async () => {
                    if (isBusy) return;
                    setPendingBusyId(pc.deviceId);
                    try {
                      const result = await claimPendingDevice(pc.deviceId, pc.name);
                      if (!result.ok) {
                        Alert.alert("Claim failed", result.error || "Unknown error");
                      }
                    } finally {
                      setPendingBusyId(null);
                    }
                  }}
                  onLongPress={() => { void refreshPendingClaims(); }}
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
                      {pc.name || `Pending ${pc.deviceId.slice(0, 8)}`}
                    </Text>
                    <Text style={{ color: c.textMuted, fontSize: 12, marginTop: 2 }}>
                      {(pc.platform || "unknown")} — tap to claim
                      {pc.relayLabel ? ` · ${pc.relayLabel}` : ""}
                    </Text>
                  </View>
                  {isBusy ? (
                    <ActivityIndicator color={c.accent} />
                  ) : (
                    <Text style={{ color: c.accent, fontSize: 13, fontWeight: "600" }}>Claim</Text>
                  )}
                </Pressable>
              );
            })}
          </View>
        )}

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
          // FlatList needs to remount when numColumns changes; use the
          // count itself as the key so portrait↔landscape rotations
          // don't crash with "Changing numColumns on the fly".
          key={`devices-cols-${listNumColumns}`}
          numColumns={listNumColumns}
          columnWrapperStyle={listNumColumns > 1 ? { gap: 12 } : undefined}
          contentContainerStyle={[styles.listContent, useMasterDetail ? null : tabletContent]}
          refreshing={isLoadingDevices}
          onRefresh={refreshDevices}
          ListEmptyComponent={isLoadingDevices ? (
            <View style={styles.center}>
              <ActivityIndicator size="large" color={c.accent} />
            </View>
          ) : <SetupInstructions />}
          renderItem={({ item }) => (
            <View style={listNumColumns > 1 ? { flex: 1, maxWidth: `${100 / listNumColumns}%` } : undefined}>
            <DeviceCard
              device={item}
              isActive={activeDevice?.id === item.id}
              connectionStatus={connectionStatus}
              isStale={unreachableDeviceIds.includes(item.id)}
              isPrimary={primaryDeviceId === item.id}
              isSecondary={secondaryDeviceId === item.id}
              isPooledConnected={connectedSet.has(item.id)}
              defaultRunner={primaryRunnerByDevice[item.id] || ""}
              onSelect={() => selectDevice(item)}
              onSetPrimary={() => {
                void setPrimaryDevice(item.id).catch((e: any) =>
                  Alert.alert("Error", e?.message || "Failed"),
                );
              }}
              onSetSecondary={() => {
                void setSecondaryDevice(item.id).catch((e: any) =>
                  Alert.alert("Error", e?.message || "Failed"),
                );
              }}
              onUnsetSecondary={() => {
                void setSecondaryDevice(null).catch((e: any) =>
                  Alert.alert("Error", e?.message || "Failed"),
                );
              }}
              authExpired={activeDevice?.id === item.id && connectionStatus === "connected" && agentAuthExpired}
              forceDetailsOpen={openDetailsId === item.id}
              onLongPress={() => {
                const actionLabel = item.isGuest ? "Detach" : "Remove";
                const isConnectedHere = connectedSet.has(item.id);
                const message = item.isGuest
                  ? isConnectedHere
                    ? "Disconnect from this shared machine, or remove it from your list? It will reappear if the host shares it again."
                    : "Remove this shared machine from your list? It will reappear if the host shares it again."
                  : isConnectedHere
                    ? "Disconnect from this machine, or remove it from your account? The node will need to re-register before it shows up again."
                    : "Remove this device from your account? The node will need to re-register before it shows up again.";
                // Guest machines can't be elevated — they can vanish on host revocation.
                const isThisPrimary = primaryDeviceId === item.id;
                const isThisSecondary = secondaryDeviceId === item.id;
                const primaryAction = item.isGuest
                  ? null
                  : isThisPrimary
                    ? { text: "Unset primary", onPress: async () => {
                        try { await setPrimaryDevice(null); } catch (e: any) { Alert.alert("Error", e?.message || "Failed"); }
                      } }
                    : { text: "Set as primary", onPress: async () => {
                        try { await setPrimaryDevice(item.id); } catch (e: any) { Alert.alert("Error", e?.message || "Failed"); }
                      } };
                const secondaryAction = item.isGuest || isThisPrimary
                  ? null
                  : isThisSecondary
                    ? { text: "Unset secondary", onPress: async () => {
                        try { await setSecondaryDevice(null); } catch (e: any) { Alert.alert("Error", e?.message || "Failed"); }
                      } }
                    : { text: "Set as secondary", onPress: async () => {
                        try { await setSecondaryDevice(item.id); } catch (e: any) { Alert.alert("Error", e?.message || "Failed"); }
                      } };
                const buttons: any[] = [{ text: "Cancel", style: "cancel" }];
                if (primaryAction) buttons.push(primaryAction);
                if (secondaryAction) buttons.push(secondaryAction);
                if (isConnectedHere) {
                  buttons.push({
                    text: "Disconnect",
                    onPress: () => {
                      disconnectDevice(item.id);
                    },
                  });
                }
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
                  if (result?.rateLimited) {
                    Alert.alert(
                      "Agent rate-limited",
                      `Agent's per-IP recovery cooldown is still active (5s window). Wait a few seconds and tap Re-auth again.\n\n${appTag()}`,
                    );
                    return;
                  }
                  Alert.alert(
                    "Recovery Failed",
                    `${result?.error || "Could not recover this machine from the phone."}\n\n${appTag()}`,
                  );
                } catch (e: any) {
                  Alert.alert(
                    "Recovery Failed",
                    `${e?.message || "Could not recover this machine from the phone."}\n\n${appTag()}`,
                  );
                }
              }}
              token={token}
              onOpenDetails={useMasterDetail ? () => setSelectedDetailDeviceId(item.id) : undefined}
            />
            </View>
          )}
        />
      </View>
      {useMasterDetail ? (
        <View style={{ flex: 1 }}>
          {detailDevice ? (
            <DeviceDetailsModal
              device={detailDevice}
              visible
              inline
              onClose={() => setSelectedDetailDeviceId(null)}
            />
          ) : (
            <View style={{ flex: 1, alignItems: "center", justifyContent: "center", padding: 24 }}>
              <Text style={{ color: c.textMuted, fontSize: 14 }}>
                Select a device to see details.
              </Text>
            </View>
          )}
        </View>
      ) : null}
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
    borderRadius: 10,
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
    borderRadius: 10,
    justifyContent: "center",
    alignItems: "center",
  },
  guestCodeBtnText: {
    color: "#fff",
    fontSize: 14,
    fontWeight: "600",
  },
  addDeviceBtn: {
    marginHorizontal: 12,
    marginTop: 10,
    marginBottom: 4,
    height: 44,
    borderWidth: 1,
    borderRadius: 10,
    justifyContent: "center",
    alignItems: "center",
  },
  addDeviceBtnText: {
    fontSize: 14,
    fontWeight: "700",
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
    borderRadius: 999,
  },
  connDot: { width: 6, height: 6, borderRadius: 3, marginRight: 6 },
  connText: { fontSize: 12, fontWeight: "600", textTransform: "capitalize" },
  disconnectBtn: {
    paddingHorizontal: 4,
    paddingVertical: 2,
  },
  disconnectText: { fontSize: 13, fontWeight: "600" },
  listContent: { padding: 16, flexGrow: 1 },
  center: {
    flex: 1,
    justifyContent: "center",
    alignItems: "center",
    padding: 32,
  },
  emptyTitle: { ...typography.pageTitle, fontSize: 22, textAlign: "center" },
  emptySubtitle: {
    ...typography.body,
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
    borderRadius: 14,
    paddingHorizontal: spacing.lg,
    paddingVertical: 16,
    marginBottom: spacing.md,
    borderWidth: 1,
    ...lightCardShadow,
  },
  cardPressed: { opacity: 0.7 },
  cardRow: { flexDirection: "row", justifyContent: "space-between" },
  cardInfo: { flex: 1, marginRight: 12 },
  deviceName: { ...typography.cardTitle, fontSize: 17 },
  deviceMeta: { ...typography.caption, marginTop: 4 },
  neutralPill: {
    minHeight: 24,
    paddingHorizontal: 8,
    paddingVertical: 4,
    borderRadius: 999,
    borderWidth: 1,
    flexDirection: "row",
    alignItems: "center",
    gap: 6,
  },
  neutralPillDot: { width: 6, height: 6, borderRadius: 3 },
  neutralPillLead: { fontSize: 11, fontWeight: "700" },
  neutralPillText: { fontSize: 12, fontWeight: "600" },
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
    paddingHorizontal: 12,
    paddingVertical: 6,
    borderRadius: 999,
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
