// screenRecorder.ts — JS bridge to the platform-native screen-capture
// module. Implements the contract `setNativeScreenRecorder` expects:
//
//   (durationSec) => Promise<ArrayBuffer | null>
//
// Both iOS (mobile/ios/Yaver/YaverScreenRecorder.swift) and Android
// (mobile/android/app/src/main/java/io/yaver/mobile/YaverScreenRecorder*)
// register the same module name, "ScreenRecorder", with the same surface:
//
//   startRecording(): Promise<true>      // permission flow happens here
//   stopRecording():  Promise<string>    // returns the MP4 path
//   isRecordingActive(): Promise<bool>
//
// We start the native recorder, wait `durationSec`, stop, fetch the MP4
// at the returned path, and resolve the ArrayBuffer that vibePreview.ts
// uploads to /vibing/preview/clip/upload.
//
// On call from a non-iOS / non-Android platform (web fallback for Expo
// Web during dev), returns null so the caller surfaces a clear error
// instead of crashing.

import { NativeModules, Platform } from "react-native";
import { setNativeScreenRecorder } from "./vibePreview";

interface RNScreenRecorder {
  startRecording(): Promise<true>;
  stopRecording(): Promise<string>; // returns absolute path to MP4
  isRecordingActive(): Promise<boolean>;
}

const native: RNScreenRecorder | undefined =
  (NativeModules as any).ScreenRecorder;

/**
 * recordPhoneScreen — start the native recorder, wait, stop, return
 * the MP4 bytes. Used by vibePreview.ts:recordAndUploadPhoneClip.
 *
 * Permission UX: on iOS the system shows a "Yaver wants to record this
 * app" dialog the first time per session — startRecording() resolves
 * after the user accepts. On Android, MediaProjection's permission
 * intent surfaces a system bottom sheet on every call (the framework
 * does not let apps remember the grant — by design).
 */
export async function recordPhoneScreen(durationSec: number): Promise<ArrayBuffer | null> {
  if (Platform.OS !== "ios" && Platform.OS !== "android") {
    return null;
  }
  if (!native?.startRecording || !native?.stopRecording) {
    return null;
  }
  await native.startRecording();
  await new Promise((r) => setTimeout(r, Math.max(1000, durationSec * 1000)));
  const path: string = await native.stopRecording();
  if (!path) return null;
  // RN's fetch supports file:// URIs on both iOS + Android; .arrayBuffer
  // is available on all Hermes builds since RN 0.71+.
  const fileUri = path.startsWith("file://") ? path : "file://" + path;
  try {
    const res = await fetch(fileUri);
    if (!res.ok) return null;
    return await res.arrayBuffer();
  } catch {
    return null;
  }
}

/**
 * registerNativeScreenRecorder — call once on app boot. Plugs the
 * native module into vibePreview.ts so recordAndUploadPhoneClip works
 * without each caller having to know how to wire it up.
 *
 * Idempotent — calling twice just overwrites the registration.
 */
export function registerNativeScreenRecorder(): void {
  if (Platform.OS !== "ios" && Platform.OS !== "android") {
    return;
  }
  if (!native) {
    return;
  }
  setNativeScreenRecorder(recordPhoneScreen);
}

/**
 * isNativeScreenRecorderAvailable — UI helper. Returns true if the
 * `ScreenRecorder` module is registered on this platform; the modal
 * uses this to grey out the "Phone" record button on web/desktop.
 */
export function isNativeScreenRecorderAvailable(): boolean {
  if (Platform.OS !== "ios" && Platform.OS !== "android") return false;
  return !!native?.startRecording && !!native?.stopRecording;
}
