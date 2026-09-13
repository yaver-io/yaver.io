"use client";

// Silent Input web capture — the browser/macOS counterpart to the iOS
// `nativeSource.ts`. Turns the front camera into the exact frame contract the
// agent enforces: 96x96 gray8 mouth crops at 25 fps, base64-encoded.
//
// WHY IT LIVES ON WEB. The macOS desktop app is an Electron shell around the
// web dashboard (electron/src/main.js -> /dashboard). A web capture source is
// therefore the macOS front-camera path *and* the remote path at once: the
// frames go to whichever Yaver device was chosen as the recognizer, so a Mac
// can capture locally while a heavier box runs the model.
//
// MVP CROP. This revision downscales the largest centered square of the video
// frame. It is dependency-free and deliberately leaves the mouth-crop policy
// behind the same `MouthFrame` contract, so a later revision can swap in
// face-landmark-guided mouth localization without touching the client, the
// panel, or the agent. Until then the capture guide asks the user to center
// their mouth in the frame.

export type WebMouthFrame = {
  timestamp: number;
  width: number;
  height: number;
  format: "gray8";
  data: string;
};

export type CaptureOptions = {
  /** Hard cap mirrors the iOS 8 second interaction. */
  durationMs?: number;
  fps?: number;
  facingMode?: "user" | "environment";
  signal?: AbortSignal;
  /** 0..1 crop-center bias; 0.5 is the frame center. */
  centerY?: number;
};

export const VSR_FRAME_SIZE = 96;
export const VSR_MAX_DURATION_MS = 8_000;
export const VSR_MAX_FPS = 25;

function sleep(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, Math.max(0, ms)));
}

function bytesToBase64(bytes: Uint8Array): string {
  let binary = "";
  const chunk = 0x8000;
  for (let i = 0; i < bytes.length; i += chunk) {
    binary += String.fromCharCode(...bytes.subarray(i, i + chunk));
  }
  return btoa(binary);
}

export function cameraSupported(): boolean {
  return (
    typeof navigator !== "undefined" &&
    !!navigator.mediaDevices &&
    typeof navigator.mediaDevices.getUserMedia === "function"
  );
}

/**
 * Capture one short front-camera interaction and return normalized mouth
 * frames. The camera stream is ALWAYS stopped before returning, capture or
 * throw — the browser shows the recording indicator for exactly as long as
 * this promise is pending.
 */
export async function captureMouthFrames(options: CaptureOptions = {}): Promise<WebMouthFrame[]> {
  if (!cameraSupported()) {
    throw new Error(
      "This browser cannot access a camera (navigator.mediaDevices.getUserMedia is unavailable). Use a physical camera device.",
    );
  }
  const durationMs = Math.min(Math.max(options.durationMs ?? VSR_MAX_DURATION_MS, 250), VSR_MAX_DURATION_MS);
  const fps = Math.min(Math.max(options.fps ?? VSR_MAX_FPS, 1), VSR_MAX_FPS);
  const centerY = Math.min(Math.max(options.centerY ?? 0.5, 0), 1);

  const stream = await navigator.mediaDevices.getUserMedia({
    audio: false,
    video: { facingMode: options.facingMode ?? "user", width: { ideal: 640 }, height: { ideal: 480 } },
  });

  const video = document.createElement("video");
  video.muted = true;
  video.playsInline = true;
  video.srcObject = stream;

  try {
    await video.play();
    // Dimensions are not available until the first frame decodes.
    const readyDeadline = performance.now() + 3_000;
    while ((!video.videoWidth || !video.videoHeight) && performance.now() < readyDeadline) {
      await sleep(30);
    }
    if (!video.videoWidth || !video.videoHeight) {
      throw new Error("The camera produced no video frames. Check the camera permission and that no other app is using it.");
    }

    const canvas = document.createElement("canvas");
    canvas.width = VSR_FRAME_SIZE;
    canvas.height = VSR_FRAME_SIZE;
    const ctx = canvas.getContext("2d", { willReadFrequently: true });
    if (!ctx) throw new Error("Could not acquire a 2D canvas context for mouth cropping.");

    const frames: WebMouthFrame[] = [];
    const startedAt = performance.now();
    const interval = 1000 / fps;
    let nextSampleAt = startedAt;

    while (performance.now() - startedAt < durationMs) {
      if (options.signal?.aborted) break;
      const now = performance.now();
      if (now < nextSampleAt) {
        await sleep(nextSampleAt - now);
        continue;
      }
      nextSampleAt += interval;

      const side = Math.min(video.videoWidth, video.videoHeight);
      const sx = (video.videoWidth - side) / 2;
      const sy = Math.min(Math.max((video.videoHeight - side) * centerY, 0), video.videoHeight - side);
      ctx.drawImage(video, sx, sy, side, side, 0, 0, VSR_FRAME_SIZE, VSR_FRAME_SIZE);

      const image = ctx.getImageData(0, 0, VSR_FRAME_SIZE, VSR_FRAME_SIZE);
      const gray = new Uint8Array(VSR_FRAME_SIZE * VSR_FRAME_SIZE);
      for (let i = 0, p = 0; i < gray.length; i += 1, p += 4) {
        gray[i] = (image.data[p] * 0.299 + image.data[p + 1] * 0.587 + image.data[p + 2] * 0.114) | 0;
      }

      frames.push({
        timestamp: Math.round(performance.now() - startedAt),
        width: VSR_FRAME_SIZE,
        height: VSR_FRAME_SIZE,
        format: "gray8",
        data: bytesToBase64(gray),
      });
    }

    return frames;
  } finally {
    stream.getTracks().forEach((track) => track.stop());
    video.srcObject = null;
  }
}
