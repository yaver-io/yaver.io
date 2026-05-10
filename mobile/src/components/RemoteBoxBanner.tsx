import React, { useState } from "react";
import { Platform, Pressable, StyleSheet, Text, View } from "react-native";
import { useDevice } from "../context/DeviceContext";
import { useColors } from "../context/ThemeContext";
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

interface BannerPalette {
  stripe: string;
  dot: string;
  text: string;
  label: string;
}

export default function RemoteBoxBanner({ extra, onDeviceChange, disableTap }: RemoteBoxBannerProps) {
  const c = useColors();
  const { activeDevice, connectionStatus, connectedDeviceIds, primaryDeviceId, secondaryDeviceId } = useDevice();
  const [pickerVisible, setPickerVisible] = useState(false);

  const effective = deriveEffectiveConnectionState(connectionStatus, connectedDeviceIds);
  const palette: BannerPalette = {
    connected: { stripe: c.success, dot: c.success, text: c.textSecondary, label: "Connected" },
    connecting: { stripe: c.warn, dot: c.warn, text: c.textSecondary, label: "Reconnecting" },
    error: { stripe: c.error, dot: c.error, text: c.textPrimary, label: "Disconnected" },
    disconnected: { stripe: c.textMuted, dot: c.textMuted, text: c.textSecondary, label: "Disconnected" },
  }[effective];

  // Lead with the focused device name so the user sees immediately
  // which box this tab is reading from. When no device is selected
  // (cold start, after explicit disconnect, after deletion) the
  // banner reads "No device selected" and the Switch chip changes
  // to "Pick" so the action verb matches the empty state.
  const deviceLabel = activeDevice?.name?.trim() || "No device selected";
  const ctaLabel = activeDevice ? "Switch" : "Pick";
  const roleLabel =
    activeDevice?.id === primaryDeviceId
      ? "Primary"
      : activeDevice?.id === secondaryDeviceId
        ? "Secondary"
        : null;

  const Wrapper: any = disableTap ? View : Pressable;
  const wrapperProps = disableTap ? {} : { onPress: () => setPickerVisible(true), hitSlop: 6 };

  return (
    <>
      <Wrapper
        {...wrapperProps}
        style={[
          styles.banner,
          {
            backgroundColor: c.bgCardElevated,
            borderBottomColor: c.borderSubtle,
          },
        ]}
      >
        <View style={[styles.accent, { backgroundColor: palette.stripe }]} />
        <View style={styles.row}>
          <View style={styles.rowMain}>
            <View style={styles.statusLine}>
              <View style={[styles.dot, { backgroundColor: palette.dot }]} />
              <Text style={[styles.label, { color: palette.text }]} numberOfLines={1}>
                {palette.label}
              </Text>
            </View>
            {roleLabel ? (
              <View style={[styles.inlineChip, styles.rolePill, { backgroundColor: c.bgInput, borderColor: c.borderSubtle }]}>
                <Text style={[styles.roleText, { color: c.textSecondary }]}>{roleLabel}</Text>
              </View>
            ) : null}
            <Text style={[styles.deviceText, { color: c.textPrimary }]} numberOfLines={1}>
              {deviceLabel}
            </Text>
            {extra ? <View style={styles.extraInline}>{extra}</View> : null}
          </View>
          {!disableTap && (
            <View style={[styles.inlineChip, styles.ctaPill, { borderColor: c.borderSubtle, backgroundColor: c.bgInput }]}>
              <Text style={[styles.cta, { color: c.textSecondary }]}>{ctaLabel} ›</Text>
            </View>
          )}
        </View>
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
    paddingVertical: 11,
    borderBottomWidth: 1,
    position: "relative",
    overflow: "hidden",
  },
  accent: { position: "absolute", left: 0, top: 0, bottom: 0, width: 4 },
  row: { flexDirection: "row", alignItems: "flex-start", justifyContent: "space-between", gap: 12 },
  rowMain: { flexDirection: "row", alignItems: "center", flex: 1, minWidth: 0, gap: 8, flexWrap: "wrap" },
  statusLine: { flexDirection: "row", alignItems: "center", flexShrink: 0 },
  dot: { width: 8, height: 8, borderRadius: 4, marginRight: 8 },
  label: {
    ...typography.caption,
    fontWeight: "600",
    letterSpacing: 0.1,
    fontFamily: Platform.OS === "ios" ? undefined : undefined,
  },
  deviceText: {
    ...typography.caption,
    fontWeight: "600",
    flexShrink: 1,
    minWidth: 0,
  },
  inlineChip: {
    minHeight: 24,
    paddingHorizontal: 8,
    paddingVertical: 4,
    borderRadius: 999,
    borderWidth: 1,
    justifyContent: "center",
  },
  rolePill: {
    flexShrink: 0,
  },
  roleText: {
    ...typography.caption,
    fontSize: 12,
    fontWeight: "600",
  },
  ctaPill: {
    marginLeft: 8,
    flexShrink: 0,
  },
  cta: {
    ...typography.caption,
    fontWeight: "700",
  },
  extraInline: {
    flexDirection: "row",
    alignItems: "center",
    gap: 6,
    flexWrap: "wrap",
    minWidth: 0,
    flexShrink: 1,
    backgroundColor: "transparent",
  },
});
