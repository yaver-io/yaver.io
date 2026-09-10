/**
 * Regression guard for the 2026-09-10 Bluetooth-audio incident.
 *
 * The Tasks tab activated AVAudioSession PlayAndRecord while mounting. On an
 * iPhone connected to a car/headset that eagerly selected Bluetooth HFP and
 * interrupted or degraded music even though the mic had never been tapped.
 * Recording sessions now belong to startRealtimeTranscribe and restore to an
 * inactive state on stop or failure.
 *
 * Run: node --experimental-strip-types mobile/src/lib/audioSessionLifecycle.test.mts
 */
import { readdirSync, readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
const tasks = readFileSync(join(here, "../../app/(tabs)/tasks.tsx"), "utf8");
const speech = readFileSync(join(here, "speech.ts"), "utf8");
const audioOwner = readFileSync(join(here, "microphoneAudioSession.ts"), "utf8");
const screenRecorder = readFileSync(
  join(here, "../../ios/Yaver/YaverScreenRecorder.swift"),
  "utf8",
);
const tvSpeech = readFileSync(
  join(here, "../../local-pods/YaverSpeech/YaverSpeech.swift"),
  "utf8",
);
const whisperPatch = readFileSync(
  join(here, "../../patches/whisper.rn+0.5.5.patch"),
  "utf8",
);
const feedbackCapture = readFileSync(
  join(here, "../../../sdk/feedback/react-native/src/capture.ts"),
  "utf8",
);
const feedbackChat = readFileSync(
  join(here, "../../../sdk/feedback/react-native/src/VibeChatScreen.tsx"),
  "utf8",
);

let failures = 0;
const ok = (condition: unknown, label: string) => {
  if (condition) console.log(`ok   ${label}`);
  else { console.error(`FAIL ${label}`); failures++; }
};

function eagerActivations(sources: string[]): string[] {
  return sources.filter((source) => /AudioSessionIos\.setActive\(true\)/.test(source));
}

type RuntimeSource = { path: string; source: string };

function runtimeTypeScriptSources(dir: string): RuntimeSource[] {
  return readdirSync(dir, { withFileTypes: true }).flatMap((entry) => {
    const path = join(dir, entry.name);
    if (entry.isDirectory()) return runtimeTypeScriptSources(path);
    if (!/\.tsx?$/.test(entry.name) || /\.test\.[cm]?tsx?$/.test(entry.name)) return [];
    return [{ path, source: readFileSync(path, "utf8") }];
  });
}

const runtimeSources = [
  ...runtimeTypeScriptSources(join(here, "../../app")),
  ...runtimeTypeScriptSources(join(here, "..")),
];

ok(
  eagerActivations(runtimeSources.map(({ source }) => source)).length === 0,
  "app runtime code never activates an iOS recording session eagerly",
);
const directExpoRecorders = runtimeSources.filter(
  ({ source }) => /Audio\.Recording\.createAsync|\.stopAndUnloadAsync\(/.test(source),
);
ok(
  directExpoRecorders.length === 1 &&
    directExpoRecorders[0]?.path.endsWith("/lib/microphoneAudioSession.ts"),
  "all expo-av microphone lifecycles use the process-wide session owner",
);
ok(
  /allowsRecordingIOS:\s*false/.test(audioOwner) &&
    /InterruptionModeIOS\.MixWithOthers/.test(audioOwner) &&
    /if \(liveRecordings\.size > 0\) return/.test(audioOwner),
  "idle and playback mode disables recording, mixes audio, and preserves live mic owners",
);
ok(
  /AppState\.addEventListener\("change"/.test(audioOwner) &&
    /discardMicrophoneRecording\(recording\)/.test(audioOwner),
  "non-car expo recordings are discarded when the app backgrounds",
);
ok(
  /opts\.audioSessionOnStopIos\s*\?\?[\s\S]{0,120}"restore"/.test(speech),
  "realtime capture defaults to restoring the previous iOS audio session",
);
ok(
  /AudioSessionIos\?\.Category/.test(speech) &&
    /AudioSessionIos\?\.CategoryOption/.test(speech) &&
    /AudioSessionIos\?\.Mode/.test(speech),
  "realtime capture uses whisper.rn's runtime audio-session enum namespace",
);
ok(
  (speech.match(/deactivateRealtimeAudioSessionIos\(\)/g) ?? []).length >= 3,
  "failed native start and stop paths both deactivate the iOS recording session",
);
ok(
  /ensureRealtimeMicrophonePermission\(\)/.test(speech) &&
    /Audio\.getPermissionsAsync\(\)/.test(speech),
  "microphone permission is requested at capture time",
);
ok(
  /isMicrophoneEnabled\s*=\s*false/.test(screenRecorder) &&
    /case \.audioMic:[\s\S]{0,180}break/.test(screenRecorder),
  "ReplayKit screen feedback cannot capture or retain the microphone",
);
ok(
  /setActive\(false, options: \[\.notifyOthersOnDeactivation\]\)/.test(tvSpeech) &&
    (tvSpeech.match(/releaseRecordingSession\(\)/g) ?? []).length >= 4,
  "native TV speech releases its audio session on stop and both start failures",
);
ok(
  /AVAudioSessionSetActiveOptionNotifyOthersOnDeactivation/.test(whisperPatch),
  "whisper session deactivation notifies paused external music to resume",
);
ok(
  /allowsRecordingIOS:\s*false/.test(feedbackCapture) &&
    /InterruptionModeIOS\?\.MixWithOthers/.test(feedbackCapture) &&
    (feedbackCapture.match(/prepareNonInterruptingAudioPlayback\(\)/g) ?? []).length >= 4,
  "feedback SDK restores non-interrupting playback after every microphone path",
);
ok(
  /AppState\.addEventListener\('change'/.test(feedbackCapture) &&
    /stopPcmRecording\(\)\.catch/.test(feedbackChat),
  "feedback SDK releases hidden microphones on background and unmount",
);

// Negative control: prove the first assertion detects the exact regression,
// rather than passing because its fixture accidentally stopped matching.
ok(
  eagerActivations([`${tasks}\nAudioSessionIos.setActive(true);`]).length === 1,
  "guard catches a reintroduced eager PlayAndRecord activation",
);

if (failures) {
  console.error(`\naudioSessionLifecycle: ${failures} FAILED`);
  process.exitCode = 1;
} else {
  console.log("\naudioSessionLifecycle: ALL PASS");
}
