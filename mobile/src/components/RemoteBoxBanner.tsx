import React, { useState } from "react";
import { Platform, Pressable, StyleSheet, Text, View } from "react-native";
import { useDevice } from "../context/DeviceContext";
import { useColors, useTheme } from "../context/ThemeContext";
import { typography } from "../theme/tokens";
import RemoteBoxPickerModal from "./RemoteBoxPickerModal";
import { deriveEffectiveConnectionState, type EffectiveConnectionState } from "../lib/connectionState";

// RemoteBoxBanner — single shared widget for the per-tab connection
// status + machine switcher.
//
// The same row used to be rebuilt three times (Reload's Remote Box
// card, Tasks' bespoke big banner, Projects' bare empty state) with
// three different rules for "is connected?" — see the user's
// screenshot complaint: Devices showed "2 Connected" while Reload
// and Projects showed "Not connected" because each tab consulted a
// different source of truth.
//
// This component:
//   - reads the SAME effectiveConnectionState across every tab
//     (focused-device status OR pool fallback — see lib/connectionState),
//   - renders one consistent green/yellow/gray bar with the focused
//     device name + a clear Switch › chip,
//   - is fully tappable: anywhere on the bar opens the device picker.
//     The discoverability of "tap to switch" beats hunting for a chip,
//     and matches the Reload Remote Box card's existing affordance.
//
// Tab-specific affordances (Tasks' Re-auth chip, Projects' discover
// hint, Reload's Hermes-ready badge) plug in via the `extra` slot so
// the shared widget stays focused on the connection-state contract.

export interface RemoteBoxBannerProps {
  /** Optional rows rendered below the main status line. Use for
   *  per-tab affordances that don't belong in the shared widget:
   *  ping latency, runner state, re-auth chip, Hermes-ready note. */
  extra?: React.ReactNode;
  /** Callback fired AFTER the picker resolved with a different
   *  device. Tabs use this to clear stale state + kick a fresh
   *  scan (Reload + Projects do this for project discovery). */
  onDeviceChange?: (deviceId: string) => void;
  /** When true, the banner does not open the picker on tap. Used by
   *  surfaces that already host an external switch UX (e.g. the
   *  Devices tab's primary/secondary picker). Default false. */
  disableTap?: boolean;
}

const DARK_PALETTE: Record<EffectiveConnectionState, BannerPalette> = {
  connected: { bg: "#15151A", border: "#1F1F26", dot: "#22c55e", text: "#22c55e", label: "Connected" },
  connecting: { bg: "#15151A", border: "#1F1F26", dot: "#f59e0b", text: "#f59e0b", label: "Reconnecting" },
  error: { bg: "#15151A", border: "#1F1F26", dot: "#ef4444", text: "#ef4444", label: "Disconnected" },
  disconnected: { bg: "#15151A", border: "#1F1F26", dot: "#A8A8B0", text: "#A8A8B0", label: "Disconnected" },
};

const LIGHT_PALETTE: Record<EffectiveConnectionState, BannerPalette> = {
  connected: { bg: "#f0fdf4", border: "#bbf7d0", dot: "#22c55e", text: "#15803d", label: "Connected" },
  connecting: { bg: "#fffbeb", border: "#fde68a", dot: "#f59e0b", text: "#b45309", label: "Reconnecting" },
  error: { bg: "#fef2f2", border: "#fecaca", dot: "#ef4444", text: "#b91c1c", label: "Disconnected" },
  disconnected: { bg: "#f5f5f5", border: "#e5e5e5", dot: "#9ca3af", text: "#6b7280", label: "Disconnected" },
};

interface BannerPalette {
  bg: string;
  border: string;
  dot: string;
  text: string;
  label: string;
}

export default function RemoteBoxBanner({ extra, onDeviceChange, disableTap }: RemoteBoxBannerProps) {
  const c = useColors();
  const { isDark } = useTheme();
  const { activeDevice, connectionStatus, connectedDeviceIds } = useDevice();
  const [pickerVisible, setPickerVisible] = useState(false);

  const effective = deriveEffectiveConnectionState(connectionStatus, connectedDeviceIds);
  const palette = (isDark ? DARK_PALETTE : LIGHT_PALETTE)[effective];

  // Lead with the focused device name so the user sees immediately
  // which box this tab is reading from. When no device is selected
  // (cold start, after explicit disconnect, after deletion) the
  // banner reads "No device selected" and the Switch chip changes
  // to "Pick" so the action verb matches the empty state.
  const deviceLabel = activeDevice?.name?.trim() || "No device selected";
  const ctaLabel = activeDevice ? "Switch ›" : "Pick ›";

  const Wrapper: any = disableTap ? View : Pressable;
  const wrapperProps = disableTap ? {} : { onPress: () => setPickerVisible(true), hitSlop: 6 };

  return (
    <>
      <Wrapper
        {...wrapperProps}
        style={[
          styles.banner,
          {
            backgroundColor: palette.bg,
            borderBottomColor: palette.border,
          },
        ]}
      >
        <View style={[styles.accent, { backgroundColor: palette.dot }]} />
        <View style={styles.row}>
          <View style={[styles.dot, { backgroundColor: palette.dot }]} />
          <Text
            style={[styles.label, { color: palette.text }]}
            numberOfLines={1}
          >
            {palette.label} {"·"} {deviceLabel}
          </Text>
          {!disableTap && (
            <Text style={[styles.cta, { color: palette.text }]}>{ctaLabel}</Text>
          )}
        </View>
        {extra ? <View style={styles.extra}>{extra}</View> : null}
      </Wrapper>
      <RemoteBoxPickerModal
        visible={pickerVisible}
        onClose={() => setPickerVisible(false)}
        onSelected={(picked) => {
          // Fire the callback before the modal close animation so
          // tabs can kick their scan / clear stale state during the
          // transition rather than a render later. ID-based to keep
          // the contract narrow — receivers re-resolve from devices.
          if (picked?.id && onDeviceChange) onDeviceChange(picked.id);
        }}
      />
    </>
  );
}

const styles = StyleSheet.create({
  banner: {
    paddingHorizontal: 16,
    paddingVertical: 12,
    borderBottomWidth: 1,
    position: "relative",
    overflow: "hidden",
  },
  accent: { position: "absolute", left: 0, top: 0, bottom: 0, width: 3 },
  row: { flexDirection: "row", alignItems: "center" },
  dot: { width: 8, height: 8, borderRadius: 4, marginRight: 8 },
  label: {
    ...typography.captionStrong,
    letterSpacing: 0.1,
    flex: 1,
    fontFamily: Platform.OS === "ios" ? undefined : undefined,
  },
  cta: {
    ...typography.captionStrong,
    fontWeight: "700",
    marginLeft: 8,
  },
  extra: { marginTop: 8, marginLeft: 18 },
});
