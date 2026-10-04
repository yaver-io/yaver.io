import React, { useCallback, useEffect, useState } from "react";
import { ActivityIndicator, Alert, Platform, Pressable, Text, View } from "react-native";
import { hetznerClientCloud } from "../lib/clientCloudProvider";
import type { HetznerServer } from "../lib/hetznerDirectCore";
import { useDevice } from "../context/DeviceContext";
import { isBoundHetznerDevice } from "../lib/hetznerDeviceBinding";

function statusLabel(status: string): string {
  if (status === "running") return "Running";
  if (status === "off") return "Off — allocated";
  if (status === "starting") return "Powering on…";
  if (status === "stopping") return "Shutting down…";
  return status || "Unknown";
}

/**
 * Provider-authoritative status for the one locally pinned Hetzner server.
 * This is deliberately separate from Yaver agent reachability: a VPS may be
 * running while its Yaver agent is stopped. Web never receives the credential.
 */
export function HetznerManagedDeviceCard({ c }: { c: any }) {
  const { devices } = useDevice();
  const [server, setServer] = useState<HetznerServer | null>(null);
  const [busy, setBusy] = useState(false);
  const [configured, setConfigured] = useState(false);

  const refresh = useCallback(async () => {
    if (Platform.OS === "web") return;
    const connected = await hetznerClientCloud.isConnected().catch(() => false);
    const selected = connected ? await hetznerClientCloud.getManagedServer().catch(() => null) : null;
    setConfigured(Boolean(selected));
    if (!selected) {
      setServer(null);
      return;
    }
    const servers = await hetznerClientCloud.listServers();
    setServer(servers.find((candidate) => candidate.id === selected.id) || null);
  }, []);

  useEffect(() => {
    void refresh().catch(() => {});
  }, [refresh]);

  if (Platform.OS === "web" || !configured) return null;
  const bindingValid = Boolean(server && devices.some((device) => isBoundHetznerDevice(device, server)));

  const setPower = async (action: "power_on" | "shutdown") => {
    if (!server || !bindingValid) {
      Alert.alert("Power control blocked", "This Hetzner server does not match a current Yaver device by public IP or alias.");
      return;
    }
    setBusy(true);
    setServer({ ...server, status: action === "power_on" ? "starting" : "stopping" });
    try {
      setServer(await hetznerClientCloud.setPower(server.id, action));
    } catch (error: any) {
      await refresh().catch(() => {});
      Alert.alert("Hetzner power control failed", error?.message || "Try again.");
    } finally {
      setBusy(false);
    }
  };

  const requestShutdown = () => {
    if (!server) return;
    Alert.alert(
      `Shut down ${server.name}?`,
      "This asks Hetzner for a graceful shutdown. The VPS, disks, and Primary IP remain allocated and billable.",
      [
        { text: "Cancel", style: "cancel" },
        { text: "Shut down", onPress: () => { void setPower("shutdown"); } },
      ],
    );
  };

  return (
    <View testID="devices-hetzner-managed-card" style={{ backgroundColor: c.bgCard, borderColor: c.border, borderWidth: 1, borderRadius: 14, padding: 12, marginBottom: 12 }}>
      <View style={{ flexDirection: "row", alignItems: "center", justifyContent: "space-between", gap: 8 }}>
        <View style={{ flex: 1 }}>
          <Text style={{ color: c.textPrimary, fontSize: 13, fontWeight: "800" }}>Hetzner managed VPS</Text>
          <Text style={{ color: c.textMuted, fontSize: 10, marginTop: 2 }}>Direct from this device · not Yaver Cloud</Text>
        </View>
        <Pressable disabled={busy} onPress={() => { void refresh().catch((error: any) => Alert.alert("Couldn't refresh Hetzner", error?.message || "Try again.")); }}>
          {busy ? <ActivityIndicator size="small" color={c.textMuted} /> : <Text style={{ color: c.accent, fontSize: 11, fontWeight: "700" }}>Refresh</Text>}
        </Pressable>
      </View>
      {server ? (
        <>
          <Text style={{ color: c.textPrimary, fontSize: 13, fontWeight: "700", marginTop: 8 }}>{server.name}</Text>
          <Text style={{ color: server.status === "running" ? c.success : c.warn, fontSize: 11, marginTop: 2 }}>
            Provider status: {statusLabel(server.status)} · {server.ip || "no public IPv4"}
          </Text>
          <Text style={{ color: c.textMuted, fontSize: 10, marginTop: 3 }}>Provider power state is separate from Yaver agent connectivity.</Text>
          {!bindingValid ? (
            <View style={{ marginTop: 8, gap: 7 }}>
              <Text style={{ color: c.warn, fontSize: 11, fontWeight: "700" }}>
                Power control blocked — this server is not a current Yaver device.
              </Text>
              <Pressable
                onPress={() => { void hetznerClientCloud.clearManagedServer().then(refresh); }}
                style={{ alignSelf: "flex-start", borderWidth: 1, borderColor: c.border, borderRadius: 8, paddingHorizontal: 10, paddingVertical: 6 }}
              >
                <Text style={{ color: c.textSecondary, fontSize: 11, fontWeight: "700" }}>Clear stale selection</Text>
              </Pressable>
            </View>
          ) : <Pressable
            disabled={busy || (server.status !== "running" && server.status !== "off")}
            onPress={server.status === "off" ? () => { void setPower("power_on"); } : requestShutdown}
            style={{ alignSelf: "flex-start", borderWidth: 1, borderColor: server.status === "off" ? c.success : c.warn, borderRadius: 8, paddingHorizontal: 10, paddingVertical: 6, marginTop: 8, opacity: busy ? 0.5 : 1 }}
          >
            <Text style={{ color: server.status === "off" ? c.success : c.warn, fontSize: 11, fontWeight: "700" }}>
              {server.status === "off" ? "Power on" : server.status === "running" ? "Shut down" : statusLabel(server.status)}
            </Text>
          </Pressable>}
        </>
      ) : (
        <Text style={{ color: c.warn, fontSize: 11, marginTop: 8 }}>The selected server was not returned by Hetzner. Open Settings → Bring your own cloud to review it.</Text>
      )}
    </View>
  );
}
