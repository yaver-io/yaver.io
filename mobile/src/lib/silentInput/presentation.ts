export type SilentInputSetupPresentation = {
  canTest: boolean;
  status: string;
  statusKind: "idle" | "checking" | "ready" | "attention";
  guidance: string;
};

type SilentInputSetupInput = {
  enabled: boolean;
  platform: string;
  targetDeviceId?: string | null;
  probing: boolean;
  capabilityAvailable?: boolean;
  unavailableReason?: string;
};

/** Keeps the Settings setup path explicit and honest. Lip reading is only
 * testable after the user opts in, an iPhone camera is available, and the
 * selected Yaver machine proves that remote VSR inference is ready. */
export function silentInputSetupPresentation({
  enabled,
  platform,
  targetDeviceId,
  probing,
  capabilityAvailable,
  unavailableReason,
}: SilentInputSetupInput): SilentInputSetupPresentation {
  if (!enabled) {
    return {
      canTest: false,
      status: "Lip reading is off",
      statusKind: "idle",
      guidance: "Turn it on to check your Yaver machine and unlock the camera test.",
    };
  }

  if (platform === "web") {
    return {
      canTest: false,
      status: "Open Yaver on an iPhone to test",
      statusKind: "attention",
      guidance: "Lip reading needs the front camera in the native mobile app.",
    };
  }

  if (platform !== "ios") {
    return {
      canTest: false,
      status: "iPhone camera required",
      statusKind: "attention",
      guidance: "This experimental lip-reading test is currently available on iPhone.",
    };
  }

  if (!targetDeviceId) {
    return {
      canTest: false,
      status: "Connect a Yaver machine",
      statusKind: "attention",
      guidance: "Your connected machine reads the private mouth crops from this phone.",
    };
  }

  if (probing) {
    return {
      canTest: false,
      status: "Checking your Yaver machine…",
      statusKind: "checking",
      guidance: "The camera test will unlock when the machine is ready.",
    };
  }

  if (capabilityAvailable) {
    return {
      canTest: true,
      status: "Ready to test",
      statusKind: "ready",
      guidance: "Tap Test with front camera, silently mouth a short phrase, then review the result.",
    };
  }

  return {
    canTest: false,
    status: unavailableReason || "Lip reading needs setup",
    statusKind: "attention",
    guidance: "Finish the setup below, then check again to unlock the camera test.",
  };
}
