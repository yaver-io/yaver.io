import React, { useState } from "react";
import { Platform, Pressable, StyleSheet, Text, View } from "react-native";
import { useDevice } from "../context/DeviceContext";
import { useAuth } from "../context/AuthContext";
import { useColors } from "../context/ThemeContext";
import { typography } from "../theme/tokens";
import RemoteBoxPickerModal from "./RemoteBoxPickerModal";
import { deriveEffectiveConnectionState, type EffectiveConnectionState } from "../lib/connectionState";
import { isDeviceAsleep, useMachineLifecycle } from "../lib/wakeMachine";
import WakeProgress from "./WakeProgress";

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
  const { activeDevice, devices, connectionStatus, connectedDeviceIds, primaryDeviceId, secondaryDeviceId, deviceListError, everHadDevices, isLoadingDevices, refreshDevices } = useDevice();
  const { token } = useAuth();

  // Live reachability of the focused device (transport truth). Computed up
  // front so the wake/park lifecycle hook can tell "booting" from "online".
  const activeLive = !!activeDevice && connectedDeviceIds.includes(activeDevice.id);

  // A managed box that auto-off'd (self-park after idle) reports
  // machineStatus paused/stopped and has no live endpoint — that's why the
  // runner reads "Disconnected". Surface it as its own "Asleep" state with a
  // one-tap Wake on every tab, and drive the shared wake/park progress ladder
  // (resuming → booting → connecting → online → ready) so the ~1-2 min boot is
  // never an invisible dead spot or a misleading re-tappable "Asleep".
  const asleep = isDeviceAsleep(activeDevice as any);
  const lifecycle = useMachineLifecycle({
    token,
    device: activeDevice as any,
    deviceReachable: activeLive,
    onTick: refreshDevices,
  });
  const running = lifecycle.direction !== null || lifecycle.phase === "error";
  // "Never added a remote device" is distinct from "have devices but none
  // selected/reachable" — show a create/pair prompt rather than a misleading
  // "Disconnected".
  // Only "never added a device" for a genuine first-run user. If they've ever
  // had devices, a transient empty (VPN/network) must NOT read as "No remote
  // device added" — it's the "No device selected / reconnecting" case instead.
  //
  // …and NOT while the list is still loading. everHadDevices is false right
  // after a fresh sign-in (no history yet), so the first render of a user with
  // ten machines announced "No remote device added" while the spinner beneath
  // it still said "Looking for devices…". Two contradictory claims on screen at
  // once, and the alarming one was the lie. An empty list mid-load is UNKNOWN,
  // never a definitive negative — don't state a fact you haven't finished
  // checking.
  const noDevicesYet =
    devices.length === 0 && !activeDevice && !everHadDevices && !isLoadingDevices;
  const [pickerVisible, setPickerVisible] = useState(false);

  // Honest status: connectionStatus can be optimistically "connected"
  // (set the instant selectDevice's connect resolves — including when a
  // relay tunnel comes up but the agent behind it is unreachable), which
  // painted a green "Connected" for a box whose transport was still
  // pending and whose ping failed. When a device IS selected, only call
  // it connected if that exact device is in the LIVE connected pool
  // (connectionManager's transport truth); otherwise it's still
  // connecting. With no device selected, fall back to the presence
  // derivation used by the cold-start / pool-only cases. (activeLive is
  // computed up top so the lifecycle hook can consume it.)
  const effective = noDevicesYet
    ? "disconnected"
    : activeDevice
    ? (activeLive ? "connected" : connectionStatus === "error" ? "error" : "connecting")
    : deriveEffectiveConnectionState(connectionStatus, connectedDeviceIds);
  const palette: BannerPalette = {
    connected: { stripe: c.success, dot: c.success, text: c.textSecondary, label: "Connected" },
    connecting: { stripe: c.warn, dot: c.warn, text: c.textSecondary, label: activeLive ? "Reconnecting" : "Connecting" },
    error: { stripe: c.error, dot: c.error, text: c.textPrimary, label: "Disconnected" },
    disconnected: { stripe: c.textMuted, dot: c.textMuted, text: c.textSecondary, label: "Disconnected" },
  }[effective];

  // Lead with the focused device name so the user sees immediately
  // which box this tab is reading from. When no device is selected
  // (cold start, after explicit disconnect, after deletion) the
  // banner reads "No device selected" and the Switch chip changes
  // to "Pick" so the action verb matches the empty state.
  // While the list is still loading, say so. "No device selected" is a verdict;
  // mid-load the honest word is "Looking".
  const stillLooking = isLoadingDevices && devices.length === 0 && !activeDevice;
  const deviceLabel =
    activeDevice?.name?.trim() ||
    (stillLooking
      ? "Looking for devices…"
      : noDevicesYet
        ? "No remote device added"
        : "No device selected");
  const ctaLabel = activeDevice ? "Switch" : noDevicesYet ? "Add" : "Pick";
  // "Pool is warm but you haven't chosen which box runs your tasks" is its own
  // state — painting it green "Connected" (because some pooled client is live)
  // while the row also says "No device selected" reads as a contradiction. Flag
  // it as an attention state with a prominent CTA so the next action is obvious.
  const needsPick = !activeDevice && !noDevicesYet;
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
        <View style={[styles.accent, { backgroundColor: running ? (lifecycle.phase === "error" ? c.error : c.accent) : needsPick ? c.warn : asleep ? c.accent : palette.stripe }]} />
        <View style={styles.stack}>
          <View style={styles.row}>
            <View style={styles.rowMain}>
              <View style={styles.statusLine}>
                <View style={[styles.dot, { backgroundColor: running ? (lifecycle.phase === "error" ? c.error : c.accent) : needsPick ? c.warn : asleep ? c.accent : palette.dot }]} />
                <Text style={[styles.label, { color: running ? (lifecycle.phase === "error" ? c.error : c.accent) : needsPick ? c.warn : asleep ? c.accent : palette.text }]} numberOfLines={1}>
                  {noDevicesYet ? "Not set up" : needsPick ? "No machine selected" : running ? lifecycle.meta.short : asleep ? "Asleep" : palette.label}
                </Text>
              </View>
              {roleLabel ? (
                <View style={[styles.inlineChip, styles.rolePill, { backgroundColor: c.bgInput, borderColor: c.borderSubtle }]}>
                  <Text style={[styles.roleText, { color: c.textSecondary }]}>{roleLabel}</Text>
                </View>
              ) : null}
              {/* When nothing is picked, the status label already says "No
                  machine selected" and the chip already says "Pick ›". The old
                  third copy ("Tap to choose where tasks run") only ever
                  rendered as "Tap to choose wh…" — a truncated restatement
                  squeezed between the two lines that already said it.
                  Exception: mid-load this slot carries "Looking for devices…",
                  which is NOT a restatement — it's the one line distinguishing
                  "still checking" from "nothing there". Keep it. */}
              {needsPick && !stillLooking ? null : (
                <Text
                  style={[styles.deviceText, { color: stillLooking ? c.textMuted : c.textPrimary }]}
                  numberOfLines={1}
                >
                  {noDevicesYet && deviceListError ? deviceListError : deviceLabel}
                </Text>
              )}
            </View>
            {running ? null : asleep ? (
              // Wake takes priority over Switch when the focused box is
              // asleep — one tap resumes it from its snapshot. Its own
              // Pressable so it fires Wake (not the row's picker tap), and
              // it shows even on disableTap surfaces (a paused box is
              // actionable regardless of the row's switch UX). Once tapped,
              // `running` flips and the chip yields to the progress ladder.
              <Pressable
                onPress={lifecycle.wake}
                disabled={lifecycle.busy}
                hitSlop={6}
                style={[
                  styles.inlineChip,
                  styles.ctaPill,
                  styles.wakeChip,
                  { borderColor: c.accent, backgroundColor: c.accentSoft, opacity: lifecycle.busy ? 0.6 : 1 },
                ]}
              >
                <Text style={[styles.cta, { color: c.accent }]}>Wake</Text>
              </Pressable>
            ) : !disableTap ? (
              <View
                style={[
                  styles.inlineChip,
                  styles.ctaPill,
                  needsPick
                    ? { borderColor: c.accent, backgroundColor: c.accentSoft }
                    : { borderColor: c.borderSubtle, backgroundColor: c.bgInput },
                ]}
              >
                <Text style={[styles.cta, { color: needsPick ? c.accent : c.textSecondary }]}>{ctaLabel} ›</Text>
              </View>
            ) : null}
          </View>
          {running ? <WakeProgress state={lifecycle} compact /> : null}
          {/* Per-tab affordances (transport · ping · re-auth) get their OWN row
              below the status line. Inlining them into rowMain made the row wrap
              to two lines while the Switch/Pick chip stayed vertically centered —
              so Ping and Pick floated at different heights and read as misaligned. */}
          {extra ? <View style={styles.extraRow}>{extra}</View> : null}
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
  // `flex-start` left the Switch chip pinned to the top of the row
  // while the rowMain content (status pill + role + device name) sat
  // a few pt lower because of the dot + line-height baseline. On
  // tablet that gap was wide enough that the Switch chip and the
  // adjacent latency chip read as floating in their own column.
  // Centering keeps the right-side pills aligned to the row's
  // vertical midline so they read as part of the banner, not above it.
  stack: { gap: 8 },
  row: { flexDirection: "row", alignItems: "center", justifyContent: "space-between", gap: 12 },
  rowMain: { flexDirection: "row", alignItems: "center", flex: 1, minWidth: 0, gap: 8 },
  // Affordance row: full-width, left-aligned, wraps on its own beneath the
  // status line so it never fights the right-side Switch/Pick chip.
  extraRow: { flexDirection: "row", alignItems: "center", flexWrap: "wrap", gap: 8, minWidth: 0 },
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
  wakeChip: {
    flexDirection: "row",
    alignItems: "center",
    gap: 6,
  },
  cta: {
    ...typography.caption,
    fontWeight: "700",
  },
});
