/**
 * Screen capture helpers — screenshot + video recording.
 *
 * Peer deps (all optional — loaded lazily):
 *   - `react-native-view-shot`        — screenshot
 *   - `react-native-record-screen`    — video recording (iOS ReplayKit /
 *                                       Android MediaProjection)
 *
 * Each helper surfaces a clear error if the module is missing so a host
 * app knows exactly which peer dep to add. Audio-note / voice-command
 * recording was removed in 0.7.0 — see FeedbackModal for the new
 * 5-button surface.
 */

/**
 * Capture the current screen as a PNG image.
 * Requires `react-native-view-shot` to be installed.
 *
 * Note: the feedback modal should hide itself *before* calling this so the
 * screenshot contains the underlying app state (the actual bug), not the
 * modal. See `FeedbackModal.handleScreenshotForFix`.
 */
export async function captureScreenshot(): Promise<string> {
  try {
    const ViewShot = require('react-native-view-shot');
    const uri = await ViewShot.captureScreen({
      format: 'png',
      quality: 0.9,
    });
    return uri;
  } catch (err) {
    throw new Error(
      '[YaverFeedback] Screenshot capture failed. Install react-native-view-shot as a peer dep. ' +
        String(err),
    );
  }
}

let videoRecorderModule: any = null;
let videoRecordingActive = false;

/**
 * Start a screen-recording session. Requires
 * `react-native-record-screen` as a peer dep.
 *
 * The user must grant the iOS ReplayKit / Android MediaProjection
 * permission the first time; the prompt is shown by the native module,
 * not the SDK.
 */
export async function startVideoRecording(): Promise<void> {
  if (videoRecordingActive) {
    throw new Error('[YaverFeedback] A video recording is already in progress.');
  }
  try {
    videoRecorderModule = require('react-native-record-screen').default ??
      require('react-native-record-screen');
    if (typeof videoRecorderModule.startRecording !== 'function') {
      throw new Error('react-native-record-screen missing startRecording()');
    }
    const result = await videoRecorderModule.startRecording({
      mic: false,
      width: 720,
      bitrate: 1024 * 1000,
    });
    if (result && result.status && result.status !== 'success') {
      throw new Error(`startRecording returned ${result.status}`);
    }
    videoRecordingActive = true;
  } catch (err) {
    videoRecorderModule = null;
    videoRecordingActive = false;
    throw new Error(
      '[YaverFeedback] Could not start screen recording. Install react-native-record-screen. ' +
        String(err),
    );
  }
}

/**
 * Stop the current video recording and return the on-device file path.
 */
export async function stopVideoRecording(): Promise<{
  path: string;
  duration: number;
}> {
  if (!videoRecordingActive || !videoRecorderModule) {
    throw new Error('[YaverFeedback] No video recording in progress.');
  }
  try {
    const res = await videoRecorderModule.stopRecording();
    videoRecordingActive = false;
    const path =
      typeof res === 'string'
        ? res
        : (res?.result?.outputURL as string) ??
          (res?.outputURL as string) ??
          (res?.uri as string) ??
          '';
    const durationMs =
      typeof res?.result?.duration === 'number'
        ? res.result.duration
        : typeof res?.duration === 'number'
          ? res.duration
          : 0;
    if (!path) {
      throw new Error('stopRecording() returned no file path');
    }
    return { path, duration: durationMs / 1000 };
  } catch (err) {
    videoRecordingActive = false;
    throw new Error(
      '[YaverFeedback] Failed to stop screen recording. ' + String(err),
    );
  }
}

/** Whether a video recording is currently active. */
export function isVideoRecording(): boolean {
  return videoRecordingActive;
}
