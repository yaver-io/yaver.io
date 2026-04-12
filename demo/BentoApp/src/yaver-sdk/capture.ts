/**
 * Screen capture and audio recording helpers.
 *
 * Screenshot capture requires `react-native-view-shot` as a peer dependency.
 * Audio recording requires `react-native-audio-recorder-player` or a
 * similar library — the implementation below uses a minimal approach
 * that works when one of those is available.
 */

let audioRecorderModule: any = null;

/**
 * Capture the current screen as a PNG image.
 * Requires `react-native-view-shot` to be installed.
 * @returns File path of the captured screenshot.
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
      '[YaverFeedback] Screenshot capture failed. Make sure react-native-view-shot is installed. ' +
        String(err),
    );
  }
}

/**
 * Start recording an audio voice note.
 * Requires `react-native-audio-recorder-player` to be installed.
 */
export async function startAudioRecording(): Promise<void> {
  try {
    const AudioRecorderPlayer =
      require('react-native-audio-recorder-player').default;
    audioRecorderModule = new AudioRecorderPlayer();
    await audioRecorderModule.startRecorder();
  } catch (err) {
    audioRecorderModule = null;
    throw new Error(
      '[YaverFeedback] Audio recording failed to start. Make sure react-native-audio-recorder-player is installed. ' +
        String(err),
    );
  }
}

/**
 * Stop the current audio recording.
 * @returns Object with the file path and duration in seconds.
 */
export async function stopAudioRecording(): Promise<{
  path: string;
  duration: number;
}> {
  if (!audioRecorderModule) {
    throw new Error('[YaverFeedback] No audio recording in progress.');
  }

  try {
    const result = await audioRecorderModule.stopRecorder();
    const recorder = audioRecorderModule;
    audioRecorderModule = null;

    // result is the file path on most implementations
    const path = typeof result === 'string' ? result : result?.uri ?? '';
    // Duration tracking — recorder-player provides currentPosition in ms
    const durationMs =
      typeof recorder.currentPosition === 'number'
        ? recorder.currentPosition
        : 0;

    return { path, duration: durationMs / 1000 };
  } catch (err) {
    audioRecorderModule = null;
    throw new Error(
      '[YaverFeedback] Failed to stop audio recording. ' + String(err),
    );
  }
}
