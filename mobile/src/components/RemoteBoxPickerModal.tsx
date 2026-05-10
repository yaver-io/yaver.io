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
    latestCliVersion,
    lastError,
  } = useDevice();

  const connectedSet = React.useMemo(() => new Set(connectedDeviceIds), [connectedDeviceIds]);
  const eligibleDevices = React.useMemo(
    () => eligibleRemoteBoxDevices(devices, connectedSet, activeDevice?.id),
    [devices, connectedSet, activeDevice?.id],
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

  const handleContinue = React.useCallback(async () => {
    if (!pickedDevice) return;
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
      if (activeDevice?.id !== pickedDevice.id) {
        await selectDevice(pickedDevice);
      }
      if (!connectionManager.clientFor(pickedDevice.id).isConnected) {
        const detail = (lastError || "").trim();
        throw new Error(
          detail
            ? `Couldn't reach ${pickedDevice.name}: ${detail}`
            : `Couldn't reach ${pickedDevice.name}.`,
        );
      }
      onSelected?.(pickedDevice);
      onClose();
    } catch (err: any) {
      setSwitchError(err?.message || "Failed to switch remote box.");
    } finally {
      setSwitching(false);
    }
  }, [pickedDevice, selectDevice, activeDevice?.id, lastError, onSelected, onClose]);

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
              Pick which machine should own Hermes builds, reloads, and project discovery on this tab.
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
                return (
                  <Pressable
                    key={device.id}
                    onPress={() => setPickedDeviceId(device.id)}
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
                  : activeDevice?.id === pickedDevice.id
                    ? "Keep using this machine"
                    : "Switch to this machine"}
              </Text>
            </Pressable>
          </ScrollView>
        )}
      </View>
    </Modal>
  );
}
