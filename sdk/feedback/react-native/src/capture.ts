/**
 * Screen capture helpers — screenshot + video + voice recording.
 *
 * Peer deps (all optional — loaded lazily):
 *   - `react-native-view-shot`        — screenshot
 *   - `react-native-record-screen`    — video recording (iOS ReplayKit /
 *                                       Android MediaProjection)
 *   - `expo-av`                       — audio recording for voice notes
 *
 * Each helper surfaces a clear error if the module is missing so a host
 * app knows exactly which peer dep to add. Voice-note capture was
 * removed in 0.7.0 and re-added in 0.7.14 as a narrow, "press to
 * record → stop → transcribe via the agent → attach to feedback" flow.
 * The broader live/narrated/batch modes from pre-0.7.0 stay removed.
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

/**
 * Capture a screenshot AND return it as base64 + mime type, ready to
 * embed in a `/tasks` payload's `images` array. Used by the converged
 * vibe-feedback flow (FeedbackModal → P2PClient.createFeedbackTask).
 *
 * Returns null when capture isn't possible (peer dep missing / user
 * permission denied / running in a context where view-shot can't
 * grab the screen). Caller should treat null as "send without
 * screenshot" rather than aborting the whole feedback.
 */
export async function captureScreenshotBase64(): Promise<{
  base64: string;
  mimeType: string;
} | null> {
  try {
    const ViewShot = require('react-native-view-shot');
    const result = await ViewShot.captureScreen({
      format: 'jpg',
      quality: 0.7,
      result: 'base64',
    });
    if (typeof result === 'string' && result.length > 0) {
      // ViewShot returns a bare base64 string (no `data:` prefix).
      return { base64: result, mimeType: 'image/jpeg' };
    }
    return null;
  } catch {
    return null;
  }
}

export interface PickedFeedbackFile {
  path: string;
  name: string;
  mimeType?: string;
  kind: 'image' | 'video' | 'audio' | 'unknown';
}

function classifyPickedFile(name: string, mimeType?: string): PickedFeedbackFile['kind'] {
  const lowerName = name.toLowerCase();
  const lowerMime = (mimeType ?? '').toLowerCase();
  if (
    lowerMime.startsWith('image/') ||
    lowerName.endsWith('.png') ||
    lowerName.endsWith('.jpg') ||
    lowerName.endsWith('.jpeg') ||
    lowerName.endsWith('.webp')
  ) {
    return 'image';
  }
  if (
    lowerMime.startsWith('video/') ||
    lowerName.endsWith('.mp4') ||
    lowerName.endsWith('.mov') ||
    lowerName.endsWith('.m4v')
  ) {
    return 'video';
  }
  if (
    lowerMime.startsWith('audio/') ||
    lowerName.endsWith('.m4a') ||
    lowerName.endsWith('.aac') ||
    lowerName.endsWith('.wav') ||
    lowerName.endsWith('.mp3')
  ) {
    return 'audio';
  }
  return 'unknown';
}

/**
 * Pick an existing media file from the device. Requires
 * `expo-document-picker` to be installed.
 */
export async function pickFeedbackFile(): Promise<PickedFeedbackFile> {
  try {
    const picker = require('expo-document-picker');
    const result = await picker.getDocumentAsync({
      copyToCacheDirectory: true,
      multiple: false,
      type: ['image/*', 'video/*', 'audio/*'],
    });
    if (result?.canceled) {
      throw new Error('File selection canceled.');
    }
    const asset = result?.assets?.[0];
    if (!asset?.uri) {
      throw new Error('No file selected.');
    }
    const name = asset.name || asset.uri.split('/').pop() || 'attachment';
    const mimeType = asset.mimeType as string | undefined;
    return {
      path: asset.uri,
      name,
      mimeType,
      kind: classifyPickedFile(name, mimeType),
    };
  } catch (err) {
    throw new Error(
      '[YaverFeedback] File upload needs `expo-document-picker` as an optional peer dependency. ' +
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

// ── Voice note recording ────────────────────────────────────────────
//
// Short audio recording, stopped on user tap. Lazy-loaded via expo-av —
// the SDK doesn't declare a hard dep on it so bare-RN apps that don't
// have Expo can still install the SDK. If expo-av is missing, the
// helpers throw a clear error and the modal's voice button gracefully
// hides itself.

let audioRecorderRef: any = null;
let audioRecorderActive = false;

function loadExpoAvOrThrow(): any {
  try {
    const mod = require('expo-av');
    if (!mod?.Audio?.Recording) {
      throw new Error('expo-av is installed but missing Audio.Recording');
    }
    return mod;
  } catch (err) {
    throw new Error(
      '[YaverFeedback] Voice notes need `expo-av` as a peer dependency. ' +
        'Add it with `npx expo install expo-av` and rebuild. ' +
        String(err),
    );
  }
}

/**
 * Returns true if `expo-av` is installed and Audio.Recording is
 * available — lets the modal hide the voice button in apps that
 * haven't installed the peer dep, instead of throwing on tap.
 */
export function isVoiceCaptureSupported(): boolean {
  try {
    const mod = require('expo-av');
    return !!mod?.Audio?.Recording;
  } catch {
    return false;
  }
}

/**
 * Begin recording audio from the device microphone. Requests
 * microphone permission on first use. Resolves once recording has
 * actually started, so the UI can flip to "Stop" immediately.
 */
export async function startAudioRecording(): Promise<void> {
  if (audioRecorderActive) {
    throw new Error('[YaverFeedback] An audio recording is already in progress.');
  }
  const ExpoAv = loadExpoAvOrThrow();
  const { Audio } = ExpoAv;

  const perm = await Audio.requestPermissionsAsync();
  if (!perm.granted) {
    throw new Error(
      '[YaverFeedback] Microphone permission denied. Enable it in Settings ▸ Your App ▸ Microphone.',
    );
  }

  // Use the iOS/Android high-quality preset — transcription providers
  // (Whisper / Deepgram / OpenAI) prefer 16 kHz+ mono but also handle
  // the higher sample rates fine. Default preset is portable.
  await Audio.setAudioModeAsync({
    allowsRecordingIOS: true,
    playsInSilentModeIOS: true,
    staysActiveInBackground: false,
  });

  const recording = new Audio.Recording();
  await recording.prepareToRecordAsync(Audio.RecordingOptionsPresets.HIGH_QUALITY);
  await recording.startAsync();
  audioRecorderRef = recording;
  audioRecorderActive = true;
}

/**
 * Stop the current audio recording and return the on-device file path
 * (usually a .m4a on iOS / .3gp on Android). Returns null if no
 * recording was active.
 */
export async function stopAudioRecording(): Promise<{ path: string; duration: number } | null> {
  if (!audioRecorderActive || !audioRecorderRef) return null;
  const recording = audioRecorderRef;
  audioRecorderRef = null;
  audioRecorderActive = false;
  try {
    await recording.stopAndUnloadAsync();
  } catch {
    // Second stop calls throw; ignore and use whatever state we have.
  }
  const uri = typeof recording.getURI === 'function' ? recording.getURI() : null;
  if (!uri) {
    throw new Error('[YaverFeedback] Audio recording produced no file.');
  }
  let durationMs = 0;
  try {
    const status = await recording.getStatusAsync();
    durationMs = status?.durationMillis ?? 0;
  } catch {
    // Status can fail after unload; leave duration at 0, transcription
    // still works.
  }
  return { path: uri, duration: durationMs / 1000 };
}

/** Whether a voice-note recording is currently active. */
export function isAudioRecording(): boolean {
  return audioRecorderActive;
}
