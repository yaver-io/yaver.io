// robotCameraStream — when THIS phone is the robot box, it captures its OWN
// camera and pushes frames into the co-located agent's "external" camera buffer
// (robot_camera_push) over loopback. That makes the box's camera available to:
//   • the on-box move-and-verify loop (robot_jog/move --verify, robot_look), and
//   • a HOST Claude Code / Codex via the desktop MCP tool `robot_camera`
//     (which reads the same frame back over the mesh as a viewable image).
//
// Android has no /dev/video0, so the agent's GstCamera can't capture the phone's
// own camera — the app is the producer instead. This module is camera-LIBRARY
// AGNOSTIC: you supply a `captureFrame()` that returns a base64 JPEG (e.g. from
// react-native-vision-camera's takeSnapshot/takePhoto, or expo-camera's
// takePictureAsync({ base64: true })). The loop handles cadence, backoff, and
// loopback delivery so the camera wiring stays in the UI layer.
//
// Set the box's robot camera source to "external" first (robot_config_set
// { camera: "external" } or YAVER_ROBOT_CAMERA=external) so the buffer exists.

import { robotClient, type RobotTarget } from "./robotClient";

// The box pushes to its own agent: prefer the loopback interface so frames never
// leave the device. Falls back to the passed target's addresses if loopback is
// rejected (rare; e.g. agent bound to LAN only).
export function loopbackTarget(selfDeviceId: string, port = 18080): RobotTarget {
  return { id: selfDeviceId, host: "127.0.0.1", port, lanIps: ["127.0.0.1"] };
}

export type CaptureFrame = () => Promise<string | null | undefined>;

export type RobotCameraStreamOptions = {
  // Frames per second to push. 1–15; default 6. Loopback JPEG is cheap, but the
  // bottleneck is the capture call on the producer side.
  fps?: number;
  // Called on each successful push with the frame age the agent reports.
  onPush?: (info: { bytes?: number; ageMs?: number }) => void;
  // Called when a push or capture fails (non-fatal; the loop keeps going).
  onError?: (err: unknown) => void;
};

export type RobotCameraStream = {
  stop: () => void;
  running: () => boolean;
};

// startRobotCameraStream begins pushing frames from captureFrame() into the
// box's external camera buffer. Returns a handle; call stop() to end it (e.g.
// on screen blur / unmount). Self-throttles: a slow captureFrame() simply lowers
// the effective fps rather than queuing.
export function startRobotCameraStream(
  target: RobotTarget,
  captureFrame: CaptureFrame,
  opts: RobotCameraStreamOptions = {},
): RobotCameraStream {
  const fps = Math.min(15, Math.max(1, opts.fps ?? 6));
  const minInterval = Math.floor(1000 / fps);
  let alive = true;
  let inFlight = false;

  const tick = async () => {
    if (!alive || inFlight) return;
    inFlight = true;
    const started = Date.now();
    try {
      const frame = await captureFrame();
      if (alive && frame) {
        const res = await robotClient.cameraPush(target, frame);
        if (res?.ok === false) {
          opts.onError?.(new Error(res.error || "camera push rejected"));
        } else {
          opts.onPush?.({ bytes: res?.bytes, ageMs: res?.ageMs });
        }
      }
    } catch (err) {
      opts.onError?.(err);
    } finally {
      inFlight = false;
      if (alive) {
        // Pace to the target fps; if capture+push already took longer, go again
        // immediately (effective fps drops, no backlog).
        const elapsed = Date.now() - started;
        const wait = Math.max(0, minInterval - elapsed);
        setTimeout(tick, wait);
      }
    }
  };

  // Kick off on the next macrotask so the caller can wire up state first.
  setTimeout(tick, 0);

  return {
    stop: () => {
      alive = false;
    },
    running: () => alive,
  };
}
