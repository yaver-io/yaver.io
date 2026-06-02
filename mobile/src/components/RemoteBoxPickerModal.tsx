import React from "react";
import {
  ActivityIndicator,
  Modal,
  Pressable,
  ScrollView,
  Text,
  View,
} from "react-native";

import { useColors } from "../context/ThemeContext";
import { useDevice, type Device } from "../context/DeviceContext";
import { useTabletContentStyle } from "../hooks/useTabletContentStyle";
import { connectionManager } from "../lib/connectionManager";
import { quicClient } from "../lib/quic";
import { eligibleRemoteBoxDevices, versionPatchDistance } from "../lib/devicePicker";

interface Props {
  visible: boolean;
  onClose: () => void;
  onSelected?: (device: Device) => void;
}

export default function RemoteBoxPickerModal({ visible, onClose, onSelected }: Props) {
  const c = useColors();
  const tabletContent = useTabletContentStyle("regular");
  const {
    devices,
    activeDevice,
    selectDevice,
    connectedDeviceIds,
    primaryDeviceId,
    secondaryDeviceId,
    latestCliVersion,
    lastError,
  } = useDevice();

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
  const [pingByDevice, setPingByDevice] = React.useState<
    Record<string, { rttMs: number; ok: boolean; at: number }>
  >({});
  const [hermesReadyByDevice, setHermesReadyByDevice] = React.useState<
    Record<string, { enabled: boolean; reason?: string; notes?: string[] } | null>
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
        const detail = (lastError || "").trim();
        throw new Error(
          detail
            ? `Couldn't reach ${target.name}: ${detail}`
            : `Couldn't reach ${target.name}.`,
        );
      }
      onSelected?.(target);
      onClose();
    } catch (err: any) {
      setSwitchError(err?.message || "Failed to switch remote box.");
    } finally {
      setSwitching(false);
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
    <Modal visible={visible} animationType="slide" presentationStyle="pageSheet" onRequestClose={onClose}>
      <View style={{ flex: 1, backgroundColor: c.bg }}>
        <View
          style={{
            flexDirection: "row",
            alignItems: "center",
            justifyContent: "space-between",
            paddingHorizontal: 16,
            paddingTop: 14,
            paddingBottom: 10,
            borderBottomWidth: 1,
            borderBottomColor: c.border,
          }}
        >
          <Pressable
            onPress={onClose}
            hitSlop={10}
            style={({ pressed }) => ({
              paddingHorizontal: 12,
              paddingVertical: 7,
              borderRadius: 8,
              borderWidth: 1,
              borderColor: c.border,
              backgroundColor: pressed ? c.bgCard : "transparent",
              opacity: pressed ? 0.85 : 1,
            })}
          >
            <Text style={{ color: c.textPrimary, fontSize: 13, fontWeight: "600" }}>Cancel</Text>
          </Pressable>
          <Text style={{ color: c.textPrimary, fontSize: 14, fontWeight: "700" }}>
            {switching ? "Switching" : "Remote Box"}
          </Text>
          <View style={{ width: 70 }} />
        </View>

        {switching ? (
          <View style={{ flex: 1, alignItems: "center", justifyContent: "center", padding: 24 }}>
            {switchError ? (
              <>
                <Text style={{ color: c.textPrimary, fontSize: 16, fontWeight: "600", marginBottom: 6 }}>
                  Couldn't switch
                </Text>
                <Text style={{ color: c.textMuted, fontSize: 13, marginBottom: 20, textAlign: "center" }}>
                  {switchError}
                </Text>
                <Pressable
                  onPress={() => {
                    setSwitchError(null);
                    setSwitching(false);
                  }}
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
              Switch remote box
            </Text>
            <Text style={{ color: c.textMuted, fontSize: 13, marginBottom: 20 }}>
              Pick which machine should own Hermes builds, reloads, and project discovery. This choice applies across the whole app.
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
                const statusLine = connectedSet.has(device.id)
                  ? ping && ping.ok
                    ? `Connected · ${ping.rttMs}ms`
                    : ping && !ping.ok
                      ? "Connected (pool) · ping failed"
                      : "Connected · pinging…"
                  : device.online
                    ? "Live · tap to connect"
                    : "Offline";
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
                      if (switching) return;
                      if (activeDevice?.id === device.id && connectedSet.has(device.id)) {
                        onSelected?.(device);
                        onClose();
                        return;
                      }
                      void handleContinue(device);
                    }}
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
                        <Text style={{ color: c.textMuted, fontSize: 11, marginTop: 4 }}>
                          {statusLine}
                          {activeDevice?.id === device.id ? " · Focused" : ""}
                          {versionSuffix && !outdated ? versionSuffix : ""}
                        </Text>
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
                        {outdated ? (
                          <Text style={{ color: c.warn, fontSize: 11, marginTop: 2, fontWeight: "600" }}>
                            yaver {agentVer} · {distance} version{distance === 1 ? "" : "s"} behind {latestCliVersion}
                          </Text>
                        ) : null}
                      </View>
                      <Text style={{ color: selected ? c.accent : c.textMuted, fontSize: 12, fontWeight: "700" }}>
                        {selected ? "SELECTED" : connectedSet.has(device.id) ? "CONNECTED" : "LIVE"}
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
                    : "Switch to this machine"}
              </Text>
            </Pressable>
          </ScrollView>
        )}
      </View>
    </Modal>
  );
}
