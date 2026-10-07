import { Platform } from "react-native";
import * as LocalAuthentication from "expo-local-authentication";

/** Native user-presence gate shared by QR/code and in-pane remote approval.
 * A cancelled/failed OS prompt never falls through to authorizing the device.
 * Browser approval uses its separately authenticated web session/confirmation.
 */
export async function confirmDeviceApprovalPresence(machine: string): Promise<void> {
 if (Platform.OS === "web") return;
 const level=await LocalAuthentication.getEnrolledLevelAsync();
 if(level===LocalAuthentication.SecurityLevel.NONE) {
  throw new Error("Set a device passcode or enroll Face ID / Touch ID in Settings before approving remote sign-in.");
 }
 const result=await LocalAuthentication.authenticateAsync({
  promptMessage:`Approve Yaver sign-in for ${machine}`,
  disableDeviceFallback:false,
  fallbackLabel:"Use passcode",
  cancelLabel:"Cancel",
  biometricsSecurityLevel:"strong",
 });
 if(!result.success) throw new Error("Sign-in was not approved. Confirm with Face ID, Touch ID or your device passcode to continue.");
}
