import React, { useState } from "react";
import { ActivityIndicator, Pressable, ScrollView, Text, View } from "react-native";
import { useRouter } from "expo-router";
import { useDevice } from "../context/DeviceContext";
import { useColors } from "../context/ThemeContext";
import { isDeviceAsleep } from "../lib/wakeMachine";
import { autoConnectSentence } from "../lib/autoConnectStatus";
import EmptyState from "./EmptyState";
import RemoteBoxPickerModal from "./RemoteBoxPickerModal";

// NoMachineEmpty — what a list surface shows when nothing is selected to
// read FROM.
//
// The old design offered a single "Choose machine" button — which the user
// (with a live primary online!) read as a dead-end wall: "it shows choose
// machine although it could connect." Two changes fix that:
//   1. While the auto-connect sweep is in flight, show WHICH box we're
//      reaching for ("Primary (Mac mini) is online — connecting…"), with a
//      Cancel, instead of a bare pick prompt.
//   2. When the user genuinely must choose, show the available machines
//      INLINE and tappable, not hidden behind a button that opens a modal.

export interface NoMachineEmptyProps {
  /** What this surface would show. "projects" → "…to see its projects". */
  noun: string;
  /** Fired after the picker resolves, so the tab can kick a fresh scan. */
  onDeviceChange?: (deviceId: string) => void;
}

export default function NoMachineEmpty({ noun, onDeviceChange }: NoMachineEmptyProps) {
  const router = useRouter();
  const c = useColors();
  const {
    devices,
    activeDevice,
    everHadDevices,
    isLoadingDevices,
    connectedDeviceIds,
    primaryDeviceId,
    secondaryDeviceId,
    autoConnecting,
    autoConnectTarget,
    autoConnectStage,
    cancelAutoConnect,
    selectDevice,
  } = useDevice();
  const [pickerVisible, setPickerVisible] = useState(false);

  // Mirrors RemoteBoxBanner's derivation so the banner and the body never
  // disagree about which state we're in.
  const stillLooking = !activeDevice && isLoadingDevices && devices.length === 0;
  const noDevicesYet =
    devices.length === 0 && !activeDevice && !everHadDevices && !isLoadingDevices;

  const pick = (deviceId: string) => {
    const device = devices.find((d) => (d.id || (d as any).deviceId) === deviceId);
    if (device) void selectDevice(device);
    onDeviceChange?.(deviceId);
  };

  // 1) Auto-connecting — narrate + let them bail to a manual pick.
  if (autoConnecting) {
    return (
      <View style={{ flex: 1, alignItems: "center", justifyContent: "center", padding: 24 }}>
        <ActivityIndicator color={c.accent} />
        <Text
          style={{ color: c.textPrimary, fontSize: 17, fontWeight: "700", marginTop: 16, textAlign: "center" }}
        >
          {autoConnectSentence(autoConnectTarget)}
        </Text>
        <Text style={{ color: c.textMuted, fontSize: 13, marginTop: 6, textAlign: "center" }}>
          {/* Prefer the live per-rung stage ("Pinging X…", "Repaired — re-checking
              X…") over the static reassurance. The manual switch modal has always
              narrated these; showing one frozen sentence for a multi-second sweep
              made a stall look identical to a hang. */}
          {autoConnectStage ?? "Connecting automatically. This tab opens the moment it's ready."}
        </Text>
        <Pressable
          onPress={cancelAutoConnect}
          style={{
            marginTop: 20,
            paddingVertical: 12,
            paddingHorizontal: 20,
            borderRadius: 12,
            borderWidth: 1,
            borderColor: c.borderSubtle,
            backgroundColor: c.bgInput,
          }}
        >
          <Text style={{ color: c.textSecondary, fontWeight: "600" }}>Choose a machine myself</Text>
        </Pressable>
      </View>
    );
  }

  if (stillLooking) {
    return <EmptyState busy title="Looking for your machines…" />;
  }

  if (noDevicesYet) {
    return (
      <EmptyState
        icon="desktop-outline"
        title="Connect a computer"
        body={`Run Yaver on your dev machine and its ${noun} show up here.`}
        action={{ label: "Set up", onPress: () => router.push("/onboarding-pair" as any) }}
      />
    );
  }

  // 2) Have machines, none selected → show them inline, tappable.
  const isOnline = (d: any) => connectedDeviceIds.includes(d.id) || d.online === true;
  const roleOf = (id: string) =>
    id === primaryDeviceId ? "Primary" : id === secondaryDeviceId ? "Secondary" : null;
  // Online first, then by name — the box you can actually use should be on top.
  const sorted = [...devices].sort((a, b) => {
    const ao = isOnline(a) ? 0 : 1;
    const bo = isOnline(b) ? 0 : 1;
    if (ao !== bo) return ao - bo;
    return (a.name || "").localeCompare(b.name || "");
  });

  return (
    <>
      <ScrollView contentContainerStyle={{ padding: 16 }}>
        <Text style={{ color: c.textPrimary, fontSize: 17, fontWeight: "800", marginBottom: 4 }}>
          Pick a machine
        </Text>
        <Text style={{ color: c.textMuted, fontSize: 13, marginBottom: 14 }}>
          Your {noun} live on your dev machine. Tap one to connect.
        </Text>
        {sorted.map((d) => {
          const id = d.id || (d as any).deviceId;
          const online = isOnline(d);
          const asleep = isDeviceAsleep(d as any);
          const role = roleOf(id);
          return (
            <Pressable
              key={id}
              onPress={() => pick(id)}
              style={{
                flexDirection: "row",
                alignItems: "center",
                justifyContent: "space-between",
                backgroundColor: c.bgCard,
                borderColor: c.border,
                borderWidth: 1,
                borderRadius: 12,
                paddingVertical: 14,
                paddingHorizontal: 14,
                marginBottom: 10,
              }}
            >
              <View style={{ flexDirection: "row", alignItems: "center", flex: 1, minWidth: 0, gap: 10 }}>
                <View
                  style={{
                    width: 9,
                    height: 9,
                    borderRadius: 5,
                    backgroundColor: online ? c.success : asleep ? c.accent : c.textMuted,
                  }}
                />
                <Text style={{ color: c.textPrimary, fontSize: 16, fontWeight: "600", flexShrink: 1 }} numberOfLines={1}>
                  {d.name || (d as any).alias || id}
                </Text>
                {role ? (
                  <View style={{ paddingHorizontal: 8, paddingVertical: 2, borderRadius: 999, backgroundColor: c.bgInput, borderWidth: 1, borderColor: c.borderSubtle }}>
                    <Text style={{ color: c.textSecondary, fontSize: 11, fontWeight: "600" }}>{role}</Text>
                  </View>
                ) : null}
              </View>
              <Text style={{ color: online ? c.success : asleep ? c.accent : c.textMuted, fontSize: 12, fontWeight: "600" }}>
                {online ? "connect ›" : asleep ? "asleep · wake" : "offline"}
              </Text>
            </Pressable>
          );
        })}
        <Pressable onPress={() => setPickerVisible(true)} style={{ paddingVertical: 12, alignItems: "center" }}>
          <Text style={{ color: c.accent, fontSize: 14, fontWeight: "600" }}>More options</Text>
        </Pressable>
      </ScrollView>

      <RemoteBoxPickerModal
        visible={pickerVisible}
        onClose={() => setPickerVisible(false)}
        onSelected={(picked) => {
          if (picked?.id) pick(picked.id);
        }}
      />
    </>
  );
}
