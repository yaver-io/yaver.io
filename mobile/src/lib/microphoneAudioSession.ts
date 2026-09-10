/**
 * Process-wide microphone audio-mode ownership for expo-av recorders.
 *
 * expo-av explicitly requires allowsRecordingIOS to be reset to false after a
 * recording. Leaving it true makes the next playback use PlayAndRecord, which
 * can keep a car/headset on Bluetooth HFP even though no microphone is active.
 * Every clip recorder goes through this module so start failure, stop failure,
 * cancellation, and unmount all restore non-interrupting playback mode.
 */

import { AppState } from "react-native";

type RecordingLike = {
  stopAndUnloadAsync: () => Promise<unknown>;
  getURI: () => string | null;
};

const liveRecordings = new Map<RecordingLike, boolean>();

// Ordinary composer/test recordings never belong in the background. Car voice
// opts in explicitly; every other live recorder is discarded as soon as the
// app resigns active so switching apps cannot leave Bluetooth on HFP.
AppState.addEventListener("change", (state) => {
  if (state === "active") return;
  for (const [recording, keepInBackground] of [...liveRecordings]) {
    if (!keepInBackground) void discardMicrophoneRecording(recording);
  }
});

export async function prepareNonInterruptingPlaybackAudioMode(
  staysActiveInBackground = false,
): Promise<void> {
  // Do not change the process-wide expo-av mode out from under another live
  // recorder. Its own stop path performs this restoration when the last mic
  // owner releases the session.
  if (liveRecordings.size > 0) return;
  const { Audio, InterruptionModeIOS } = require("expo-av");
  await Audio.setAudioModeAsync({
    allowsRecordingIOS: false,
    interruptionModeIOS: InterruptionModeIOS.MixWithOthers,
    playsInSilentModeIOS: true,
    staysActiveInBackground,
  });
}

export async function createMicrophoneRecording(
  recordingOptions: unknown,
  staysActiveInBackground = false,
): Promise<RecordingLike> {
  const { Audio, InterruptionModeIOS } = require("expo-av");
  try {
    await Audio.setAudioModeAsync({
      allowsRecordingIOS: true,
      interruptionModeIOS: InterruptionModeIOS.MixWithOthers,
      playsInSilentModeIOS: true,
      staysActiveInBackground,
    });
    const { recording } = await Audio.Recording.createAsync(recordingOptions);
    liveRecordings.set(recording, staysActiveInBackground);
    if (!staysActiveInBackground && AppState.currentState !== "active") {
      await discardMicrophoneRecording(recording);
      throw new Error("Recording cancelled because Yaver left the foreground.");
    }
    return recording;
  } catch (error) {
    if (liveRecordings.size === 0) {
      await prepareNonInterruptingPlaybackAudioMode().catch(() => {});
    }
    throw error;
  }
}

export async function stopMicrophoneRecording(
  recording: RecordingLike,
  keepPlaybackInBackground = false,
): Promise<string | null> {
  try {
    await recording.stopAndUnloadAsync();
    return recording.getURI();
  } finally {
    liveRecordings.delete(recording);
    if (liveRecordings.size === 0) {
      await prepareNonInterruptingPlaybackAudioMode(keepPlaybackInBackground);
    }
  }
}

export async function discardMicrophoneRecording(recording: RecordingLike): Promise<void> {
  try {
    await recording.stopAndUnloadAsync();
  } catch {
    // The recorder may already have stopped. Audio-mode restoration below is
    // the important invariant for cancellation and component teardown.
  } finally {
    liveRecordings.delete(recording);
    if (liveRecordings.size === 0) {
      await prepareNonInterruptingPlaybackAudioMode().catch(() => {});
    }
  }
}
