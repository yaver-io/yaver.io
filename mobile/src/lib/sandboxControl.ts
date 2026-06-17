// sandboxControl.ts — JS control plane for the Android on-device agent
// (NativeModules.YaverSandbox ↔ SandboxService.kt). RN-coupled; not tsx-tested.
// The pure logic it leans on (localBox device/probe) is tested in localBox.test.mts.
//
// Lifecycle:
//   1. installRootfs(...) once — download/verify/extract the Alpine rootfs.
//   2. startSandbox(token) — start the foreground service, wait for the agent to
//      answer on loopback, then register 127.0.0.1:18080 as a connectionManager
//      client under LOCAL_BOX_DEVICE_ID so the terminal / runner toggles drive
//      it exactly like a remote box.
//   3. The phone appears in the device list via buildLocalBoxDevice (injected by
//      DeviceContext); selecting it focuses the loopback client.

import { NativeModules, NativeEventEmitter, Platform } from "react-native";

import { connectionManager } from "./connectionManager";
import {
  LOCAL_BOX_DEVICE_ID,
  probeLocalBox,
  buildLocalBoxDevice,
  type LocalBoxProbe,
} from "./localBox";

const Native = (NativeModules as any).YaverSandbox as
  | {
      start(): Promise<boolean>;
      startHomeHost(): Promise<boolean>;
      stop(): Promise<boolean>;
      openFactoryResetSettings(): Promise<boolean>;
      status(): Promise<SandboxNativeStatus>;
      installRootfs(url: string, sha256: string, version: string, force: boolean): Promise<boolean>;
    }
  | undefined;

export interface SandboxNativeStatus {
  running: boolean;
  rootfsInstalled: boolean;
  version: string | null;
  nativeLibDir: string;
  credHome: string;
  prootPresent: boolean;
  agentPresent: boolean;
  homeHostMode?: boolean;
  relayOnly?: boolean;
  batteryPercent?: number;
  charging?: boolean;
}

/** Android-only, and only when the native module is linked (a build that shipped
 *  the jniLibs payload). iOS / web always false → the app falls back to remote +
 *  Hermes per codingSession policy. */
export function isSandboxSupported(): boolean {
  return Platform.OS === "android" && !!Native;
}

export async function sandboxStatus(): Promise<SandboxNativeStatus | null> {
  if (!Native) return null;
  try {
    return await Native.status();
  } catch {
    return null;
  }
}

export interface InstallProgress {
  phase: string;
  bytes: number;
  total: number;
}

/** Subscribe to rootfs install progress. Returns an unsubscribe fn. */
export function onInstallProgress(cb: (p: InstallProgress) => void): () => void {
  if (!Native) return () => {};
  const emitter = new NativeEventEmitter(NativeModules.YaverSandbox);
  const sub = emitter.addListener("YaverSandboxProgress", cb);
  return () => sub.remove();
}

export async function installRootfs(
  url: string,
  sha256: string,
  version: string,
  force = false,
): Promise<boolean> {
  if (!Native) throw new Error("on-device sandbox not available on this build/platform");
  return Native.installRootfs(url, sha256, version, force);
}

/** Start the on-device agent and wire it into connectionManager on loopback.
 *  `token` is the phone's auth token (the loopback agent authenticates the user
 *  the same way any device does). Polls up to ~6s for the agent to bind. */
export async function startSandbox(token: string): Promise<LocalBoxProbe> {
  if (!Native) throw new Error("on-device sandbox not available on this build/platform");
  await Native.start();

  let probe: LocalBoxProbe = { reachable: false };
  for (let i = 0; i < 20; i++) {
    probe = await probeLocalBox();
    if (probe.reachable) break;
    await delay(500);
  }
  // Do NOT throw if the loopback HTTP hasn't bound yet. Native.start() succeeded,
  // the foreground service is up, and the "Yaver sandbox running" notification is
  // posted — so Start has succeeded from the user's POV. The agent can still be
  // warming up (proot rootfs init) past this window; the status refresh in the UI
  // and the device picker reflect reachability when it lands. Failing here just
  // produced a misleading red "did not come up" banner over a box that IS running.
  if (!probe.reachable) {
    return probe; // reachable:false, but no error — the box is running
  }

  // Best-effort: wiring the loopback box into connectionManager can still be
  // pending when the agent is in bootstrap mode (waiting to be paired from the
  // device picker) — don't block the Start UI on a spinner forever.
  try {
    await Promise.race([
      connectionManager.ensureConnected(LOCAL_BOX_DEVICE_ID, {
        host: "127.0.0.1",
        port: 18080,
        token,
      }),
      delay(8000).then(() => {
        throw new Error("loopback connect pending");
      }),
    ]);
  } catch {
    // box is running; pair it from the device picker ("This phone" → needs auth)
  }
  return probe;
}

/** Start this phone as a single-owner home host. This is Android-only and
 *  deliberately NOT operator mode: the agent serves the signed-in user and binds
 *  direct listeners to loopback via --relay-only, so off-device access must come
 *  through the authenticated relay path. It does not require the proot rootfs. */
export async function startHomeHostSandbox(token: string): Promise<LocalBoxProbe> {
  if (!Native) throw new Error("on-device sandbox not available on this build/platform");
  await Native.startHomeHost();

  let probe: LocalBoxProbe = { reachable: false };
  for (let i = 0; i < 20; i++) {
    probe = await probeLocalBox();
    if (probe.reachable) break;
    await delay(500);
  }
  if (!probe.reachable) return probe;

  try {
    await Promise.race([
      connectionManager.ensureConnected(LOCAL_BOX_DEVICE_ID, {
        host: "127.0.0.1",
        port: 18080,
        token,
      }),
      delay(8000).then(() => {
        throw new Error("loopback connect pending");
      }),
    ]);
  } catch {
    // The home-host service is up; device registration/relay can settle after boot.
  }
  return probe;
}

export async function stopSandbox(): Promise<void> {
  if (!Native) return;
  try {
    await Native.stop();
  } finally {
    connectionManager.disconnect(LOCAL_BOX_DEVICE_ID);
  }
}

export async function openFactoryResetSettings(): Promise<boolean> {
  if (!Native) throw new Error("Android reset settings are not available on this build/platform");
  return Native.openFactoryResetSettings();
}

/** The synthetic "This phone" device to inject into the device list, or null
 *  when the sandbox isn't running/supported. `runnerIds` come from the native
 *  status (which runners the rootfs has) — for now derive from rootfsInstalled. */
export async function localBoxDeviceIfRunning(): Promise<ReturnType<typeof buildLocalBoxDevice> | null> {
  if (!isSandboxSupported()) return null;
  const st = await sandboxStatus();
  if (!st?.running) return null;
  const probe = await probeLocalBox();
  if (!probe.reachable) return null;
  return buildLocalBoxDevice({
    platform: "android",
    // The baked rootfs ships all three; refine once the agent reports installed runners.
    runnerIds: st.rootfsInstalled ? ["claude", "codex", "opencode"] : [],
    agentVersion: probe.agentVersion,
    online: true,
  });
}

function delay(ms: number): Promise<void> {
  return new Promise((r) => setTimeout(r, ms));
}
