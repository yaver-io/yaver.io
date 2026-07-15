// /auth/device?code=… — the universal-link landing for an Apple TV / device-code
// QR. The real approver is app/approve-device.tsx (scanner, biometric gate,
// machine info), reached via PairLinkHandler. This route exists only so a COLD
// START on the universal link has a home to render instead of falling back to
// the Tasks tab (the original "scan → lands on Tasks" bug); it immediately
// forwards to the canonical approver, carrying the code.
//
// Do NOT reimplement approval here — one approver, one place. Signed-out
// handling and code-preservation-across-login live in approve-device.tsx.

import { Redirect, useLocalSearchParams } from "expo-router";

export default function DeviceApproveRedirect() {
  const params = useLocalSearchParams<{ code?: string }>();
  const code = typeof params.code === "string" ? params.code : undefined;
  return <Redirect href={code ? `/approve-device?code=${encodeURIComponent(code)}` : "/approve-device"} />;
}
