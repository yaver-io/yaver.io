// Global banner that surfaces a device needing the user's attention —
// pending claim, bootstrap reclaim, or expired auth session — across
// every tab. Without this the only entry point to the claim/reauth CTA
// is the per-row button on the Devices tab; a user who lands on Apps
// or Tasks first sees an empty list with no hint of what's wrong.
//
// Priority (highest first):
//   1. pending claim — fresh box that joined the user's relay but has
//      no Convex devices row yet. One-tap creates the row.
//   2. active device in bootstrap — what the user is currently looking
//      at is up but unauthenticated. One-tap fires recoverDeviceAuth.
//   3. active device with auth-expired — same shape, different copy.
//   4. any other device in bootstrap (must be online/peer-recent so we
//      don't shout about a long-offline box).

import React, { useMemo, useState } from "react";
import { ActivityIndicator, Pressable, StyleSheet, Text, View } from "react-native";
import { useRouter } from "expo-router";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { useColors } from "../context/ThemeContext";
import { useDevice, type Device, type PendingDeviceClaim } from "../context/DeviceContext";

type AttentionItem =
  | { kind: "pending"; count: number; first: PendingDeviceClaim }
  | { kind: "bootstrap"; device: Device }
  | { kind: "auth-expired"; device: Device };

function pickAttention(
  devices: Device[],
  pendingClaims: PendingDeviceClaim[],
  activeDevice: Device | null,
  agentAuthExpired: boolean,
): AttentionItem | null {
  if (pendingClaims.length > 0) {
    return { kind: "pending", count: pendingClaims.length, first: pendingClaims[0] };
  }
  if (activeDevice && activeDevice.needsAuth) {
    return { kind: "bootstrap", device: activeDevice };
  }
  if (activeDevice && agentAuthExpired) {
    return { kind: "auth-expired", device: activeDevice };
  }
  // Any other device that's online/recently-online and lost auth.
  // Skip long-offline boxes so the banner only flags actionable state.
  const reachable = devices.find((d) =>
    d.id !== activeDevice?.id &&
    d.needsAuth &&
    (d.online || d.peerState === "online" || d.peerState === "stale"),
  );
  if (reachable) return { kind: "bootstrap", device: reachable };
  return null;
}

export function DeviceAttentionBanner() {
  const c = useColors();
  const router = useRouter();
  const insets = useSafeAreaInsets();
  const {
    devices,
    pendingClaims,
    activeDevice,
    agentAuthExpired,
    isLoadingDevices,
    recoverDeviceAuth,
    claimPendingDevice,
  } = useDevice();
  const [busy, setBusy] = useState(false);
  const [errorText, setErrorText] = useState<string | null>(null);

  const item = useMemo(
    () => pickAttention(devices, pendingClaims, activeDevice, agentAuthExpired),
    [devices, pendingClaims, activeDevice, agentAuthExpired],
  );

  if (isLoadingDevices && !item) return null;
  if (!item) return null;

  const navigateToDevices = () => {
    router.navigate("/(tabs)/devices" as any);
  };

  let title = "";
  let subtitle = "";
  let actionLabel = "Open";
  let tone = "#8b5cf6"; // bootstrap purple
  let onAction: () => void | Promise<void> = navigateToDevices;

  if (item.kind === "pending") {
    title = item.count === 1
      ? `1 new device waiting to be claimed`
      : `${item.count} new devices waiting to be claimed`;
    subtitle = `${item.first.name || item.first.deviceId.slice(0, 8)} joined your relay. Tap Claim to add it.`;
    actionLabel = "Claim";
    tone = "#f59e0b";
    onAction = async () => {
      if (busy) return;
      setBusy(true); setErrorText(null);
      try {
        const r = await claimPendingDevice(item.first.deviceId);
        if (!r.ok) setErrorText(r.error || "Claim failed");
        else navigateToDevices();
      } catch (e: any) {
        setErrorText(e?.message || "Claim failed");
      } finally {
        setBusy(false);
      }
    };
  } else if (item.kind === "bootstrap") {
    title = `${item.device.name} needs to be reclaimed`;
    subtitle = `Bootstrap mode — tap to restore the Yaver session from this phone.`;
    actionLabel = "Reclaim";
    tone = "#8b5cf6";
    onAction = async () => {
      if (busy) return;
      setBusy(true); setErrorText(null);
      try {
        const r = await recoverDeviceAuth(item.device);
        if (r && (r as any).ok === false) {
          setErrorText((r as any).error || "Reclaim failed");
        } else {
          // Land on Devices so the user sees the card go green.
          navigateToDevices();
        }
      } catch (e: any) {
        setErrorText(e?.message || "Reclaim failed");
      } finally {
        setBusy(false);
      }
    };
  } else {
    // auth-expired
    title = `${item.device.name} session expired`;
    subtitle = `Tap to refresh the agent's Yaver session.`;
    actionLabel = "Re-auth";
    tone = "#f59e0b";
    onAction = async () => {
      if (busy) return;
      setBusy(true); setErrorText(null);
      try {
        const r = await recoverDeviceAuth(item.device);
        if (r && (r as any).ok === false) {
          setErrorText((r as any).error || "Re-auth failed");
        } else {
          navigateToDevices();
        }
      } catch (e: any) {
        setErrorText(e?.message || "Re-auth failed");
      } finally {
        setBusy(false);
      }
    };
  }

  return (
    <View
      style={[
        styles.container,
        {
          paddingTop: insets.top > 0 ? insets.top + 8 : 12,
          backgroundColor: tone + "1a",
          borderBottomColor: tone + "55",
        },
      ]}
    >
      <Pressable
        style={styles.textArea}
        onPress={navigateToDevices}
        disabled={busy}
        accessibilityRole="button"
        accessibilityLabel={`${title}. ${subtitle}`}
      >
        <Text style={[styles.title, { color: tone }]} numberOfLines={1}>
          {title}
        </Text>
        <Text style={[styles.subtitle, { color: c.textMuted }]} numberOfLines={2}>
          {errorText || subtitle}
        </Text>
      </Pressable>
      <Pressable
        style={[styles.actionButton, { backgroundColor: tone }, busy && styles.actionButtonBusy]}
        onPress={() => { void onAction(); }}
        disabled={busy}
        accessibilityRole="button"
        accessibilityLabel={actionLabel}
      >
        {busy ? (
          <ActivityIndicator size="small" color="#fff" />
        ) : (
          <Text style={styles.actionText}>{actionLabel}</Text>
        )}
      </Pressable>
    </View>
  );
}

const styles = StyleSheet.create({
  container: {
    flexDirection: "row",
    alignItems: "center",
    paddingHorizontal: 12,
    paddingBottom: 10,
    borderBottomWidth: 1,
    gap: 10,
  },
  textArea: { flex: 1, minWidth: 0 },
  title: { fontSize: 13, fontWeight: "700" },
  subtitle: { fontSize: 11, marginTop: 2, lineHeight: 14 },
  actionButton: {
    paddingHorizontal: 14,
    paddingVertical: 8,
    borderRadius: 6,
    minWidth: 78,
    alignItems: "center",
    justifyContent: "center",
  },
  actionButtonBusy: { opacity: 0.6 },
  actionText: { color: "#fff", fontSize: 12, fontWeight: "700" },
});
