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
import { useColors } from "../context/ThemeContext";
import { useAuth } from "../context/AuthContext";
import { useDevice, type Device, type PendingDeviceClaim } from "../context/DeviceContext";
import { probeMobileDeviceStatus } from "../lib/deviceStatus";

type AttentionItem =
  | { kind: "pending"; count: number; first: PendingDeviceClaim }
  | { kind: "bootstrap"; device: Device }
  | { kind: "auth-expired"; device: Device };

function pickAttention(
  devices: Device[],
  pendingClaims: PendingDeviceClaim[],
  activeDevice: Device | null,
  agentAuthExpired: boolean,
  isConnected: boolean,
): AttentionItem | null {
  if (pendingClaims.length > 0) {
    return { kind: "pending", count: pendingClaims.length, first: pendingClaims[0] };
  }
  // Active-device branch — order matters here:
  // 1. agentAuthExpired wins over needsAuth. Both can be set on the
  //    same row but they describe different things. agentAuthExpired
  //    comes from a live /info probe that read `lifecycleState:
  //    "yaver-auth-expired"` or `authExpired: true` straight off the
  //    agent — that's what the agent IS reporting RIGHT NOW. needsAuth
  //    on the other hand is a heartbeat-driven Convex flag that lags
  //    seconds-to-minutes behind reality. When both are true the user
  //    saw "Reclaim" (bootstrap CTA) on the banner while the in-tab
  //    indicator correctly said "Agent session expired" — confusing
  //    contradictory copy. Auth-expired routes through the same
  //    recoverDeviceAuth() path with the right framing ("Re-auth").
  if (activeDevice && agentAuthExpired) {
    return { kind: "auth-expired", device: activeDevice };
  }
  // 2. Bootstrap only when needsAuth is set AND we're NOT happily
  //    connected. Connection itself is proof the agent is past
  //    bootstrap and accepting our token, so a stale needsAuth=true
  //    flag from Convex shouldn't surface a banner that contradicts
  //    what the user is doing right now.
  if (activeDevice && activeDevice.needsAuth && !isConnected) {
    return { kind: "bootstrap", device: activeDevice };
  }
  // 3. Any OTHER device (not the one we're currently connected to)
  //    that's online/recently-online and lost auth. Skip long-offline
  //    boxes so the banner only flags actionable state.
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
  const { token } = useAuth();
  const {
    devices,
    pendingClaims,
    activeDevice,
    agentAuthExpired,
    isLoadingDevices,
    connectionStatus,
    recoverDeviceAuth,
    claimPendingDevice,
    refreshDevices,
  } = useDevice();
  const [busy, setBusy] = useState(false);
  const [errorText, setErrorText] = useState<string | null>(null);

  // Pre-flight check before firing recoverDeviceAuth: probe the
  // device's /info via relay/direct first. If lifecycleState is
  // already "ready-to-connect" and there's no auth-expired flag,
  // the device has self-recovered (or our local state was just
  // stale) and we should NOT call recovery — calling owner-claim
  // against a healthy agent returns 404 because the bootstrap
  // pair-session endpoint isn't registered in active mode, and the
  // user sees a confusing "Reclaim failed" error against a perfectly
  // working device. Returns true if recovery should proceed,
  // false if the device is fine and we should just refresh state.
  const preflightStillNeedsRecovery = async (device: Device): Promise<boolean> => {
    const probe = await probeMobileDeviceStatus(device, token, 3000).catch(() => null);
    if (!probe) return true; // unreachable — let recoverDeviceAuth try
    if (probe.lifecycleState === "ready-to-connect") return false;
    if (!probe.bootstrap && !probe.authExpired) return false;
    return true;
  };

  const isConnected = connectionStatus === "connected" && !!activeDevice;

  const item = useMemo(
    () => pickAttention(devices, pendingClaims, activeDevice, agentAuthExpired, isConnected),
    [devices, pendingClaims, activeDevice, agentAuthExpired, isConnected],
  );

  if (isLoadingDevices && !item) return null;
  if (!item) return null;

  const navigateToDevices = () => {
    router.navigate("/(tabs)/devices" as any);
  };

  let title = "";
  let subtitle = "";
  let actionLabel = "Open";
  // Theme-aware palette so the banner matches the rest of the app in
  // both dark + light. The previous hardcoded `#8b5cf6 + alpha hex`
  // style produced a faint, washed purple on dark UIs that didn't fit
  // the surrounding bgCard tone. The DarkColors / LightColors tokens
  // already define a coherent warn family (bg / fg / border) we can
  // lean on for every CTA state — pending / bootstrap / auth-expired
  // are all "needs your attention" of the same severity, so they
  // share the same surface and only differ in the subtitle copy and
  // the action button label.
  let bg = c.warnBg;
  let border = c.warnBorder;
  let fg = c.warn;
  let buttonBg = c.warn;
  let buttonFg = c.textInverse;
  let onAction: () => void | Promise<void> = navigateToDevices;

  if (item.kind === "pending") {
    title = item.count === 1
      ? `1 new device waiting to be claimed`
      : `${item.count} new devices waiting to be claimed`;
    subtitle = `${item.first.name || item.first.deviceId.slice(0, 8)} joined your relay. Tap Claim to add it.`;
    actionLabel = "Claim";
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
    onAction = async () => {
      if (busy) return;
      setBusy(true); setErrorText(null);
      try {
        // Re-probe before recovering: the device may have self-
        // recovered since the last cached probe, in which case
        // owner-claim would return 404 against an already-active
        // agent and the user would see a confusing failure.
        const stillNeeds = await preflightStillNeedsRecovery(item.device);
        if (!stillNeeds) {
          // Already healthy — refresh devices list so the banner clears.
          await refreshDevices();
          navigateToDevices();
          return;
        }
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
    onAction = async () => {
      if (busy) return;
      setBusy(true); setErrorText(null);
      try {
        // Same pre-flight idea as the bootstrap branch — if the
        // agent has already cleared its authExpired flag (its
        // internal retry loop succeeded, or another client just
        // reauth'd it), don't fire another recovery on top.
        const stillNeeds = await preflightStillNeedsRecovery(item.device);
        if (!stillNeeds) {
          await refreshDevices();
          navigateToDevices();
          return;
        }
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
          backgroundColor: bg,
          borderBottomColor: border,
          // 3px left accent stripe — visually anchors the banner as an
          // alert without needing an icon. Same stripe color as the
          // primary text so the eye groups them together.
          borderLeftColor: fg,
          borderLeftWidth: 3,
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
        <Text style={[styles.title, { color: fg }]} numberOfLines={1}>
          {title}
        </Text>
        <Text style={[styles.subtitle, { color: c.textSecondary }]} numberOfLines={2}>
          {errorText || subtitle}
        </Text>
      </Pressable>
      <Pressable
        style={[styles.actionButton, { backgroundColor: buttonBg }, busy && styles.actionButtonBusy]}
        onPress={() => { void onAction(); }}
        disabled={busy}
        accessibilityRole="button"
        accessibilityLabel={actionLabel}
      >
        {busy ? (
          <ActivityIndicator size="small" color={buttonFg} />
        ) : (
          <Text style={[styles.actionText, { color: buttonFg }]}>{actionLabel}</Text>
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
    paddingTop: 10,
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
  actionText: { fontSize: 12, fontWeight: "700" },
});
