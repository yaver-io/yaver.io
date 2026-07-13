import React from "react";
import {
  ActivityIndicator,
  Alert,
  Modal,
  Platform,
  Pressable,
  ScrollView,
  Text,
  View,
} from "react-native";

import { useSafeAreaInsets } from "react-native-safe-area-context";
import { useColors } from "../context/ThemeContext";
import { AppScreenHeader } from "./AppScreenHeader";
import { useDevice, type Device } from "../context/DeviceContext";
import { useTabletContentStyle } from "../hooks/useTabletContentStyle";
import { connectionManager } from "../lib/connectionManager";
import { quicClient } from "../lib/quic";
import { eligibleRemoteBoxDevices, versionPatchDistance } from "../lib/devicePicker";
import {
  lastSeenLabel,
  probeMobileDeviceStatus,
  type CodingRunnerProbe,
} from "../lib/deviceStatus";

interface Props {
  visible: boolean;
  onClose: () => void;
  onSelected?: (device: Device) => void;
}

type CodingStatus = {
  ready: boolean;
  runners: CodingRunnerProbe[];
  path?: "relay" | "direct";
  error?: string;
};

const CODING_RUNNER_ORDER = ["codex", "claude", "claude-code", "opencode"];

function runnerDisplayName(id: string): string {
  const normalized = id.toLowerCase();
  if (normalized === "codex") return "Codex";
  if (normalized === "claude" || normalized === "claude-code") return "Claude";
  if (normalized === "opencode") return "OpenCode";
  return id;
}

function sortedCodingRunners(runners: CodingRunnerProbe[]): CodingRunnerProbe[] {
  return [...runners]
    .filter((r) => CODING_RUNNER_ORDER.includes(r.id))
    .sort((a, b) => {
      const ai = CODING_RUNNER_ORDER.indexOf(a.id);
      const bi = CODING_RUNNER_ORDER.indexOf(b.id);
      return (ai < 0 ? 99 : ai) - (bi < 0 ? 99 : bi);
    });
}

async function waitForClientConnected(deviceId: string, timeoutMs = 2500): Promise<boolean> {
  const start = Date.now();
  while (Date.now() - start < timeoutMs) {
    if (connectionManager.clientFor(deviceId).isConnected) return true;
    await new Promise((resolve) => setTimeout(resolve, 150));
  }
  return connectionManager.clientFor(deviceId).isConnected;
}

export default function RemoteBoxPickerModal({ visible, onClose, onSelected }: Props) {
  const c = useColors();
  const insets = useSafeAreaInsets();
  const tabletContent = useTabletContentStyle("regular");
  const deviceCtx = useDevice();
  const {
    devices,
    activeDevice,
    selectDevice,
    connectedDeviceIds,
    primaryDeviceId,
    secondaryDeviceId,
    latestCliVersion,
    lastError,
  } = deviceCtx;
  const token = (deviceCtx as any).token as string | null;

  const connectedSet = React.useMemo(() => new Set(connectedDeviceIds), [connectedDeviceIds]);
  const eligibleDevices = React.useMemo(
    () =>
      eligibleRemoteBoxDevices(devices, connectedSet, activeDevice?.id).sort((a, b) => {
        const rank = (device: Device) => {
          if (device.id === primaryDeviceId) return 0;
          if (device.id === secondaryDeviceId) return 1;
          if (device.id === activeDevice?.id) return 2;
          return 3;
        };
        const delta = rank(a) - rank(b);
        if (delta !== 0) return delta;
        return a.name.localeCompare(b.name);
      }),
    [devices, connectedSet, activeDevice?.id, primaryDeviceId, secondaryDeviceId],
  );

  const [pickedDeviceId, setPickedDeviceId] = React.useState<string | null>(null);
  const [switching, setSwitching] = React.useState(false);
  const [switchError, setSwitchError] = React.useState<string | null>(null);
  // Brief "✓ connected" confirmation shown before the modal auto-closes, so a
  // successful switch isn't an instant silent dismiss (which read as "did it
  // even work?"). Holds the connected device's name.
  const [switchSuccess, setSwitchSuccess] = React.useState<string | null>(null);
  const [pingByDevice, setPingByDevice] = React.useState<
    Record<string, { rttMs: number; ok: boolean; at: number }>
  >({});
  const [hermesReadyByDevice, setHermesReadyByDevice] = React.useState<
    Record<string, { enabled: boolean; reason?: string; notes?: string[] } | null>
  >({});
  const [codingStatusByDevice, setCodingStatusByDevice] = React.useState<
    Record<string, CodingStatus | null>
  >({});
  // Per-device "Fix this machine" remediation state. Drives the
  // /install/mobile flow (Node LTS + hermesc) on the box the user
  // tapped, streaming live progress so a stalled apt/npm is never an
  // invisible spinner. Keyed by deviceId so fixing one box never
  // blocks inspecting another.
  const [fixByDevice, setFixByDevice] = React.useState<
    Record<string, { running: boolean; lastLine?: string; error?: string; done?: boolean }>
  >({});
  const fixUnsubsRef = React.useRef<Record<string, () => void>>({});

  React.useEffect(() => {
    if (visible) return;
    // Modal closed — tear down any in-flight log subscriptions so we
    // don't leak SSE readers. The install itself keeps running on the
    // agent; reopening re-subscribes from history.
    for (const unsub of Object.values(fixUnsubsRef.current)) {
      try { unsub(); } catch { /* ignore */ }
    }
    fixUnsubsRef.current = {};
  }, [visible]);

  React.useEffect(() => {
    if (!visible) {
      setSwitching(false);
      setSwitchError(null);
      setSwitchSuccess(null);
      return;
    }
    if (activeDevice?.id && eligibleDevices.some((d) => d.id === activeDevice.id)) {
      setPickedDeviceId(activeDevice.id);
      return;
    }
    setPickedDeviceId(eligibleDevices[0]?.id ?? null);
  }, [visible, activeDevice?.id, eligibleDevices]);

  const runPing = React.useCallback(async (device: Device) => {
    const direct = connectionManager.clientFor(device.id);
    if (!direct.isConnected) return;
    try {
      const result = await direct.ping();
      setPingByDevice((prev) => ({
        ...prev,
        [device.id]: { rttMs: result.rttMs, ok: result.ok, at: Date.now() },
      }));
    } catch {
      setPingByDevice((prev) => ({
        ...prev,
        [device.id]: { rttMs: -1, ok: false, at: Date.now() },
      }));
    }
  }, []);

  // Active reachability probe for a box that Convex reports as DOWN. Unlike
  // runPing (which only pings already-pooled clients), this walks relay →
  // direct so the user can confirm a machine is actually up before
  // committing to a switch. A reachable result flips the row to connectable.
  const [offlineProbe, setOfflineProbe] = React.useState<
    Record<string, { ok: boolean; line: string; busy?: boolean }>
  >({});
  const probeOffline = React.useCallback(
    async (device: Device) => {
      setOfflineProbe((p) => ({ ...p, [device.id]: { ok: false, line: "", busy: true } }));
      try {
        const r = await probeMobileDeviceStatus(
          { id: device.id, host: (device as any).host, port: (device as any).port, lanIps: (device as any).lanIps },
          token,
          8000,
        );
        setOfflineProbe((p) => ({
          ...p,
          [device.id]: r.reachable
            ? { ok: true, line: `reachable · ${r.path === "relay" ? "relay" : "direct"}` }
            : { ok: false, line: "still unreachable" },
        }));
      } catch {
        setOfflineProbe((p) => ({ ...p, [device.id]: { ok: false, line: "ping failed" } }));
      }
    },
    [token],
  );

  React.useEffect(() => {
    if (!visible) return;
    for (const device of eligibleDevices) {
      if (connectedSet.has(device.id)) {
        void runPing(device);
      }
    }
  }, [visible, eligibleDevices, connectedSet, runPing]);

  React.useEffect(() => {
    if (!visible) return;
    let cancelled = false;
    for (const device of eligibleDevices) {
      if (hermesReadyByDevice[device.id] !== undefined) continue;
      const direct = connectionManager.clientFor(device.id);
      const load = async () => {
        try {
          const snapshot = direct.isConnected
            ? await direct.capabilitySnapshot()
            : await quicClient.capabilitySnapshot(device.id);
          if (cancelled) return;
          const ready = snapshot?.targets?.["mobile-hermes"];
          setHermesReadyByDevice((prev) => ({
            ...prev,
            [device.id]: ready
              ? {
                  enabled: !!ready.enabled,
                  reason: ready.reason,
                  notes: Array.isArray(ready.notes) ? ready.notes : undefined,
                }
              : null,
          }));
        } catch {
          if (cancelled) return;
          setHermesReadyByDevice((prev) => ({ ...prev, [device.id]: null }));
        }
      };
      void load();
    }
    return () => {
      cancelled = true;
    };
  }, [visible, eligibleDevices, hermesReadyByDevice]);

  React.useEffect(() => {
    if (!visible) return;
    let cancelled = false;
    for (const device of eligibleDevices) {
      if (codingStatusByDevice[device.id] !== undefined) continue;
      const load = async () => {
        try {
          const probe = await probeMobileDeviceStatus(
            { id: device.id, host: (device as any).host, port: (device as any).port, lanIps: (device as any).lanIps },
            token,
            8000,
          );
          if (cancelled) return;
          setCodingStatusByDevice((prev) => ({
            ...prev,
            [device.id]: probe.reachable
              ? {
                  ready: probe.codingReady,
                  runners: probe.codingRunners,
                  path: probe.path,
                }
              : {
                  ready: false,
                  runners: [],
                  error: probe.error || "Coding agent status unavailable",
                },
          }));
        } catch (err: any) {
          if (cancelled) return;
          setCodingStatusByDevice((prev) => ({
            ...prev,
            [device.id]: {
              ready: false,
              runners: [],
              error: err?.message || "Coding agent status unavailable",
            },
          }));
        }
      };
      void load();
    }
    return () => {
      cancelled = true;
    };
  }, [visible, eligibleDevices, codingStatusByDevice, token]);

  const pickedDevice = eligibleDevices.find((d) => d.id === pickedDeviceId) ?? null;

  const handleContinue = React.useCallback(async (targetOverride?: Device | null) => {
    const target = targetOverride ?? pickedDevice;
    if (!target) return;
    setSwitching(true);
    setSwitchError(null);
    try {
      // Always route through DeviceContext.selectDevice — even when
      // the picked box already has a pooled-connected client. The
      // earlier optimization called connectionManager.setFocused()
      // directly in that case, which updates the focused pointer
      // in the pool but does NOT update activeDevice /
      // connectionStatus in React state. Result: the legacy
      // quicClient Proxy correctly forwarded to the new device,
      // but the Reload tab kept reading stale activeDevice +
      // showed "Not connected" after a successful switch.
      // selectDevice short-circuits internally when the client is
      // already connected (see DeviceContext.selectDevice ~line
      // 1032 — sets connectionStatus straight back to "connected"
      // after the optimistic "connecting" tick), so calling it
      // unconditionally is safe and idempotent.
      if (activeDevice?.id !== target.id || !connectionManager.clientFor(target.id).isConnected) {
        await selectDevice(target);
      }
      if (!connectionManager.clientFor(target.id).isConnected) {
        await waitForClientConnected(target.id);
      }
      if (!connectionManager.clientFor(target.id).isConnected) {
        const detail = (lastError || "").trim();
        throw new Error(
          detail
            ? `Couldn't reach ${target.name}: ${detail}`
            : `Couldn't reach ${target.name}.`,
        );
      }
      // Success — show a brief "✓ connected" confirmation instead of a silent
      // dismiss, then hand off + close. Keep `switching` true so the success
      // view stays up during the short delay.
      setSwitchSuccess(target.name);
      setTimeout(() => {
        onSelected?.(target);
        onClose();
      }, 1100);
    } catch (err: any) {
      // Keep `switching` true (do NOT clear it) so the error view with the
      // failure detail + Try again renders instead of dropping back to the
      // list, which made failures look identical to successes.
      setSwitchError(err?.message || "Failed to switch remote box.");
    }
  }, [pickedDevice, selectDevice, activeDevice?.id, lastError, onSelected, onClose]);

  // "Fix this machine" — provision the Hermes reload stack (Node LTS +
  // hermesc) on the tapped box via the agent's POST /install/mobile,
  // streaming live progress over /streams/install:mobile. Streaming
  // only works over a direct connection (streamLog hits ${baseUrl}
  // without the /peer prefix), so we connect to the box first if it
  // isn't already the focused client. On a terminal success we drop
  // the cached hermes-readiness so the row re-checks and flips green.
  const runFix = React.useCallback(async (device: Device) => {
    const id = device.id;
    if (fixByDevice[id]?.running) return;
    setFixByDevice((prev) => ({ ...prev, [id]: { running: true, lastLine: "Connecting…" } }));
    try {
      // Bring the box up directly so its install log can stream back.
      if (!connectionManager.clientFor(id).isConnected) {
        await selectDevice(device);
      }
      const client = connectionManager.clientFor(id);
      if (!client.isConnected) {
        throw new Error((lastError || "").trim() || `Couldn't reach ${device.name}.`);
      }
      const started = await client.installTool("mobile");
      if (!started.ok) {
        throw new Error(started.error || "Couldn't start the fix on this machine.");
      }
      // Subscribe to live progress. The terminal frame is
      // {type:"result", status:"ok"|"error"} (see install_http.go).
      let settled = false;
      const finish = (patch: { error?: string; done?: boolean }) => {
        if (settled) return;
        settled = true;
        try { fixUnsubsRef.current[id]?.(); } catch { /* ignore */ }
        delete fixUnsubsRef.current[id];
        setFixByDevice((prev) => ({ ...prev, [id]: { running: false, ...patch } }));
        if (patch.done && !patch.error) {
          // Force a fresh capability re-check for this device — clearing
          // the entry makes the loader effect re-run and flip the row.
          setHermesReadyByDevice((prev) => {
            const next = { ...prev };
            delete next[id];
            return next;
          });
        }
      };
      const unsub = client.streamLog(
        started.stream,
        (ev: any) => {
          if (ev?.type === "result") {
            if (ev.status === "ok") finish({ done: true });
            else finish({ error: ev.error || "Fix failed on this machine.", done: true });
            return;
          }
          const line = typeof ev?.text === "string" ? ev.text : "";
          if (line) {
            setFixByDevice((prev) => ({
              ...prev,
              [id]: { ...(prev[id] || { running: true }), running: true, lastLine: line },
            }));
          }
        },
        () => {
          // Stream ended without a terminal result frame — treat as
          // done so the spinner doesn't hang forever; the row re-check
          // is the source of truth for whether it actually worked.
          finish({ done: true });
        },
      );
      fixUnsubsRef.current[id] = unsub;
    } catch (err: any) {
      setFixByDevice((prev) => ({
        ...prev,
        [id]: { running: false, error: err?.message || "Fix failed." },
      }));
    }
  }, [fixByDevice, selectDevice, lastError]);

  const pickedDeviceIsCurrent = !!pickedDevice && activeDevice?.id === pickedDevice.id;
  const pickedDeviceIsConnected = !!pickedDevice && connectedSet.has(pickedDevice.id);

  return (
    <Modal visible={visible} animationType="slide" presentationStyle="fullScreen" onRequestClose={onClose}>
      <View style={{ flex: 1, backgroundColor: c.bg }}>
        <AppScreenHeader
          title={switchSuccess ? "Connected" : switching ? "Switching" : "Remote Box"}
          backLabel="Cancel"
          onBack={onClose}
          style={{ paddingTop: Math.max(insets.top, 12) + 6 }}
        />

        {switching ? (
          <View style={{ flex: 1, alignItems: "center", justifyContent: "center", padding: 24 }}>
            {switchSuccess ? (
              <>
                <View
                  style={{
                    width: 64,
                    height: 64,
                    borderRadius: 32,
                    backgroundColor: c.success + "22",
                    alignItems: "center",
                    justifyContent: "center",
                    marginBottom: 16,
                  }}
                >
                  <Text style={{ color: c.success, fontSize: 34, fontWeight: "800", marginTop: -2 }}>{"✓"}</Text>
                </View>
                <Text style={{ color: c.textPrimary, fontSize: 17, fontWeight: "700", marginBottom: 4 }}>
                  Connected
                </Text>
                <Text style={{ color: c.textMuted, fontSize: 13, textAlign: "center" }}>
                  Now using {switchSuccess}
                </Text>
              </>
            ) : switchError ? (
              <>
                <Text style={{ color: c.warn, fontSize: 17, fontWeight: "700", marginBottom: 8 }}>
                  Couldn't switch
                </Text>
                <View
                  style={{
                    alignSelf: "stretch",
                    maxHeight: 220,
                    backgroundColor: c.bgCard,
                    borderWidth: 1,
                    borderColor: c.border,
                    borderRadius: 10,
                    padding: 12,
                    marginBottom: 20,
                  }}
                >
                  <ScrollView>
                    <Text
                      selectable
                      style={{ color: c.textMuted, fontSize: 12, fontFamily: Platform.OS === "ios" ? "Menlo" : "monospace", lineHeight: 18 }}
                    >
                      {switchError}
                    </Text>
                  </ScrollView>
                </View>
                <View style={{ flexDirection: "row", gap: 12 }}>
                  <Pressable
                    onPress={() => {
                      setSwitchError(null);
                      setSwitching(false);
                    }}
                    style={({ pressed }) => ({
                      borderWidth: 1,
                      borderColor: c.border,
                      paddingVertical: 12,
                      paddingHorizontal: 20,
                      borderRadius: 10,
                      opacity: pressed ? 0.7 : 1,
                    })}
                  >
                    <Text style={{ color: c.textPrimary, fontWeight: "600" }}>Back to list</Text>
                  </Pressable>
                  <Pressable
                    onPress={() => { void handleContinue(); }}
                    style={({ pressed }) => ({
                      backgroundColor: c.accent,
                      paddingVertical: 12,
                      paddingHorizontal: 22,
                      borderRadius: 10,
                      opacity: pressed ? 0.85 : 1,
                    })}
                  >
                    <Text style={{ color: "#000", fontWeight: "700" }}>Try again</Text>
                  </Pressable>
                </View>
              </>
            ) : (
              <>
                <ActivityIndicator color={c.accent} size="large" />
                <Text style={{ color: c.textMuted, fontSize: 13, marginTop: 14 }}>
                  Connecting to {pickedDevice?.name || "remote box"}…
                </Text>
              </>
            )}
          </View>
        ) : (
          <ScrollView
            style={{ flex: 1 }}
            contentContainerStyle={[{ padding: 16, paddingBottom: 32 }, tabletContent]}
          >
            <Text style={{ color: c.textPrimary, fontSize: 20, fontWeight: "700", marginBottom: 4 }}>
              Choose remote box
            </Text>
            <Text style={{ color: c.textMuted, fontSize: 13, marginBottom: 20 }}>
              Select the machine Yaver should use for app builds, live reload, and project tools. Confirm at the bottom when you're ready.
            </Text>
            {eligibleDevices.length === 0 ? (
              <View style={{ padding: 16, borderRadius: 10, borderWidth: 1, borderColor: c.border, backgroundColor: c.bgCard }}>
                <Text style={{ color: c.textPrimary, fontWeight: "600", marginBottom: 6 }}>
                  No remote boxes ready
                </Text>
                <Text style={{ color: c.textMuted, fontSize: 12 }}>
                  Devices still handles pairing, auth recovery, and deep diagnostics. Come back here once a machine shows as live.
                </Text>
              </View>
            ) : (
              eligibleDevices.map((device) => {
                const ping = pingByDevice[device.id];
                const hermesReady = hermesReadyByDevice[device.id];
                const codingStatus = codingStatusByDevice[device.id];
                const codingRunners = codingStatus ? sortedCodingRunners(codingStatus.runners) : [];
                const readyCodingRunners = codingRunners.filter((r) => r.ready);
                const fix = fixByDevice[device.id];
                const selected = pickedDeviceId === device.id;
                const agentVer = (device.agentVersion || "").trim();
                const distance = agentVer && latestCliVersion
                  ? versionPatchDistance(agentVer, latestCliVersion)
                  : -1;
                const outdated = distance > 0;
                const versionSuffix = !agentVer
                  ? ""
                  : distance < 0
                    ? ` · yaver ${agentVer}`
                    : distance === 0
                      ? ` · yaver ${agentVer} · current`
                      : ` · yaver ${agentVer} · ${distance} behind`;
                const probe = offlineProbe[device.id];
                const reachableNow = !!probe?.ok;
                const isDown = !connectedSet.has(device.id) && !device.online && !reachableNow;
                const statusLine = connectedSet.has(device.id)
                  ? ping && ping.ok
                    ? `Connected · ${ping.rttMs}ms`
                    : ping && !ping.ok
                      ? "Connected (pool) · ping failed"
                      : "Connected · pinging…"
                  : device.online
                    ? "Online · tap to select"
                    : reachableNow
                      ? `Reachable · ${probe?.line ?? ""} · tap to select`
                      : `Down · ${lastSeenLabel((device as any).lastSeen)}`;
                const roleLabel =
                  device.id === primaryDeviceId
                    ? "Primary"
                    : device.id === secondaryDeviceId
                      ? "Secondary"
                      : null;
                return (
                  <Pressable
                    key={device.id}
                    onPress={() => {
                      setPickedDeviceId(device.id);
                    }}
                    // Long-press a device → quick actions (Disconnect). Tearing
                    // down the client for this device frees its relay tunnel +
                    // stream slots without leaving the picker.
                    onLongPress={() => {
                      const connected = connectionManager.clientFor(device.id).isConnected;
                      Alert.alert(
                        device.name,
                        device.alias ? `@${device.alias}` : undefined,
                        [
                          {
                            text: connected ? "Disconnect" : "Disconnect (not connected)",
                            style: "destructive",
                            onPress: () => connectionManager.disconnect(device.id),
                          },
                          { text: "Cancel", style: "cancel" },
                        ],
                      );
                    }}
                    delayLongPress={350}
                    style={({ pressed }) => ({
                      marginBottom: 10,
                      padding: 14,
                      borderRadius: 10,
                      borderWidth: selected ? 1.5 : 1,
                      borderColor: selected ? c.accent : c.border,
                      backgroundColor: c.bgCard,
                      opacity: pressed ? 0.85 : 1,
                    })}
                  >
                    <View style={{ flexDirection: "row", alignItems: "center", justifyContent: "space-between" }}>
                      <View style={{ flex: 1, paddingRight: 12 }}>
                        <Text style={{ color: c.textPrimary, fontSize: 15, fontWeight: "600" }} numberOfLines={1}>
                          {device.name}
                          {device.alias ? <Text style={{ color: c.textMuted, fontWeight: "400" }}>  @{device.alias}</Text> : null}
                        </Text>
                        {roleLabel ? (
                          <View
                            style={{
                              alignSelf: "flex-start",
                              marginTop: 6,
                              paddingHorizontal: 8,
                              paddingVertical: 3,
                              borderRadius: 999,
                              borderWidth: 1,
                              borderColor: c.accent + "44",
                              backgroundColor: c.accent + "16",
                            }}
                          >
                            <Text style={{ color: c.accent, fontSize: 10, fontWeight: "700" }}>{roleLabel}</Text>
                          </View>
                        ) : null}
                        <Text
                          style={{ color: isDown ? c.warn : c.textMuted, fontSize: 11, marginTop: 4 }}
                        >
                          {statusLine}
                          {activeDevice?.id === device.id ? " · Focused" : ""}
                          {versionSuffix && !outdated ? versionSuffix : ""}
                        </Text>
                        {isDown ? (
                          <Pressable
                            onPress={(e) => {
                              // Probe reachability without triggering the row's
                              // switch (offline boxes often have a stale Convex
                              // flag but are actually reachable over relay).
                              (e as any)?.stopPropagation?.();
                              if (!probe?.busy) void probeOffline(device);
                            }}
                            style={({ pressed }) => ({
                              alignSelf: "flex-start",
                              marginTop: 8,
                              paddingHorizontal: 12,
                              paddingVertical: 6,
                              borderRadius: 8,
                              borderWidth: 1,
                              borderColor: c.accent,
                              backgroundColor: pressed ? c.accent + "22" : "transparent",
                              flexDirection: "row",
                              alignItems: "center",
                              opacity: probe?.busy ? 0.5 : 1,
                            })}
                          >
                            {probe?.busy ? <ActivityIndicator size="small" color={c.accent} /> : null}
                            <Text style={{ color: c.accent, fontSize: 11, fontWeight: "700", marginLeft: probe?.busy ? 6 : 0 }}>
                              {probe?.busy ? "Pinging…" : probe ? "Ping again" : "Ping"}
                            </Text>
                          </Pressable>
                        ) : null}
                        {probe && !probe.ok && !probe.busy ? (
                          <Text style={{ color: c.textMuted, fontSize: 10, marginTop: 4 }}>
                            {probe.line} — make sure it's powered on and running the agent
                          </Text>
                        ) : null}
                        {hermesReady === undefined ? (
                          <Text style={{ color: c.textMuted, fontSize: 11, marginTop: 4 }}>
                            Checking Hermes reload prerequisites…
                          </Text>
                        ) : hermesReady?.enabled ? (
                          <Text style={{ color: c.success, fontSize: 11, marginTop: 4, fontWeight: "600" }}>
                            Hermes reload ready
                          </Text>
                        ) : (
                          <Text style={{ color: c.warn, fontSize: 11, marginTop: 4, fontWeight: "600" }}>
                            {hermesReady?.reason || "Hermes reload prerequisites missing"}
                          </Text>
                        )}
                        {hermesReady && !hermesReady.enabled && hermesReady.notes?.[0] ? (
                          <Text style={{ color: c.textMuted, fontSize: 10, marginTop: 2 }} numberOfLines={2}>
                            {hermesReady.notes[0]}
                          </Text>
                        ) : null}
                        {hermesReady && !hermesReady.enabled ? (
                          fix?.running ? (
                            <View style={{ flexDirection: "row", alignItems: "center", marginTop: 8 }}>
                              <ActivityIndicator color={c.accent} size="small" />
                              <Text
                                style={{ color: c.textMuted, fontSize: 10, marginLeft: 8, flex: 1 }}
                                numberOfLines={1}
                              >
                                {fix.lastLine || "Fixing…"}
                              </Text>
                            </View>
                          ) : (
                            <>
                              <Pressable
                                onPress={(e) => {
                                  // Don't let the row's onPress (switch
                                  // device) also fire — fixing is a
                                  // distinct intent from switching to it.
                                  (e as any)?.stopPropagation?.();
                                  void runFix(device);
                                }}
                                style={({ pressed }) => ({
                                  alignSelf: "flex-start",
                                  marginTop: 8,
                                  paddingHorizontal: 12,
                                  paddingVertical: 7,
                                  borderRadius: 8,
                                  borderWidth: 1,
                                  borderColor: c.accent,
                                  backgroundColor: pressed ? c.accent + "22" : "transparent",
                                  opacity: pressed ? 0.85 : 1,
                                })}
                              >
                                <Text style={{ color: c.accent, fontSize: 11, fontWeight: "700" }}>
                                  {fix?.error ? "Retry fix" : "Fix this machine"}
                                </Text>
                              </Pressable>
                              {fix?.error ? (
                                <Text style={{ color: c.warn, fontSize: 10, marginTop: 4 }} numberOfLines={2}>
                                  {fix.error}
                                </Text>
                              ) : null}
                            </>
                          )
                        ) : null}
                        {codingStatus === undefined ? (
                          <Text style={{ color: c.textMuted, fontSize: 11, marginTop: 4 }}>
                            Checking coding agents…
                          </Text>
                        ) : readyCodingRunners.length > 0 ? (
                          <Text style={{ color: c.success, fontSize: 11, marginTop: 4, fontWeight: "600" }} numberOfLines={1}>
                            {readyCodingRunners.map((r) => `${runnerDisplayName(r.id)} ready`).join(" · ")}
                            {codingStatus?.path ? ` · ${codingStatus.path}` : ""}
                          </Text>
                        ) : codingRunners.length > 0 ? (
                          <Text style={{ color: c.warn, fontSize: 11, marginTop: 4, fontWeight: "600" }} numberOfLines={1}>
                            {codingRunners.map((r) => {
                              if (!r.installed) return `${runnerDisplayName(r.id)} not installed`;
                              if (!r.authConfigured) return `${runnerDisplayName(r.id)} auth needed`;
                              return `${runnerDisplayName(r.id)} not ready`;
                            }).join(" · ")}
                          </Text>
                        ) : (
                          <Text style={{ color: c.warn, fontSize: 11, marginTop: 4, fontWeight: "600" }} numberOfLines={1}>
                            {codingStatus?.error || "No coding agents ready"}
                          </Text>
                        )}
                        {outdated ? (
                          <Text style={{ color: c.warn, fontSize: 11, marginTop: 2, fontWeight: "600" }}>
                            yaver {agentVer} · {distance} version{distance === 1 ? "" : "s"} behind {latestCliVersion}
                          </Text>
                        ) : null}
                      </View>
                      <Text style={{ color: selected ? c.accent : isDown ? c.warn : c.textMuted, fontSize: 12, fontWeight: "700" }}>
                        {selected
                          ? "SELECTED"
                          : connectedSet.has(device.id)
                            ? "CONNECTED"
                            : isDown
                              ? "DOWN"
                              : "LIVE"}
                      </Text>
                    </View>
                  </Pressable>
                );
              })
            )}
            <View style={{ height: 16 }} />
            <Pressable
              onPress={() => { void handleContinue(); }}
              disabled={!pickedDevice}
              style={({ pressed }) => ({
                backgroundColor: !pickedDevice ? c.border : c.accent,
                paddingVertical: 14,
                borderRadius: 10,
                alignItems: "center",
                opacity: pressed ? 0.85 : 1,
              })}
            >
              <Text style={{ color: !pickedDevice ? c.textMuted : "#000", fontWeight: "700" }}>
                {!pickedDevice
                  ? "Pick a machine to continue"
                  : pickedDeviceIsCurrent && pickedDeviceIsConnected
                    ? "Keep using this machine"
                    : pickedDeviceIsCurrent
                      ? "Reconnect to this machine"
                      : "Use selected machine"}
              </Text>
            </Pressable>
          </ScrollView>
        )}
      </View>
    </Modal>
  );
}
