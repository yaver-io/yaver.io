import React, { useState } from "react";
import { useRouter } from "expo-router";
import { useDevice } from "../context/DeviceContext";
import EmptyState from "./EmptyState";
import RemoteBoxPickerModal from "./RemoteBoxPickerModal";

// NoMachineEmpty — what a list surface shows when nothing is selected to
// read FROM.
//
// Projects and Reload both used to render "No projects yet · Rediscover ·
// Diagnose discovery" in this state. All three are lies: with no machine
// selected there is no box to scan, so Rediscover POSTs into the void and
// the diagnostics panel has no target. The screen offered two buttons and a
// link, none of which could work, while the ONE thing that unblocks the user
// (choose a machine) was a truncated chip in the banner.
//
// So: one state, one action. Which action depends on whether the user has a
// machine at all — a picker is useless with an empty roster, and the pairing
// flow is condescending once you have three boxes.

export interface NoMachineEmptyProps {
  /** What this surface would show. "projects" → "…to see its projects". */
  noun: string;
  /** Fired after the picker resolves, so the tab can kick a fresh scan. */
  onDeviceChange?: (deviceId: string) => void;
}

export default function NoMachineEmpty({ noun, onDeviceChange }: NoMachineEmptyProps) {
  const router = useRouter();
  const { devices, activeDevice, everHadDevices, isLoadingDevices } = useDevice();
  const [pickerVisible, setPickerVisible] = useState(false);

  // Mirrors RemoteBoxBanner's derivation so the banner and the body of the
  // screen never disagree about which state we're in — including the
  // isLoadingDevices guard: everHadDevices is false right after a fresh
  // sign-in, so without it a user with ten machines gets told to go connect
  // one while the fetch that would list them is still in flight.
  const stillLooking = !activeDevice && isLoadingDevices && devices.length === 0;
  const noDevicesYet = devices.length === 0 && !activeDevice && !everHadDevices && !isLoadingDevices;

  return (
    <>
      {stillLooking ? (
        <EmptyState busy title="Looking for your machines…" />
      ) : noDevicesYet ? (
        <EmptyState
          icon="desktop-outline"
          title="Connect a computer"
          body={`Run Yaver on your dev machine and its ${noun} show up here.`}
          action={{
            label: "Set up",
            onPress: () => router.push("/onboarding-pair" as any),
          }}
        />
      ) : (
        <EmptyState
          icon="desktop-outline"
          title="Pick a machine"
          body={`Your ${noun} live on your dev machine. Choose which one this tab reads from.`}
          action={{ label: "Choose machine", onPress: () => setPickerVisible(true) }}
        />
      )}

      <RemoteBoxPickerModal
        visible={pickerVisible}
        onClose={() => setPickerVisible(false)}
        onSelected={(picked) => {
          if (picked?.id && onDeviceChange) onDeviceChange(picked.id);
        }}
      />
    </>
  );
}
