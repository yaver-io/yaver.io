import * as FileSystem from 'expo-file-system/legacy';
import { NativeModules, Platform } from 'react-native';

export interface FeedbackSession {
  id: string;
  recording: boolean;
  startTime: number;
  screenshots: string[]; // file paths
  voiceNotes: Array<{ time: number; text: string }>;
  mode: 'live' | 'narrated' | 'batch';
}

export interface FeedbackBundle {
  videoPath?: string;
  audioPath?: string;
  screenshots: string[];
  timeline: Array<{ time: number; type: string; text?: string; file?: string }>;
  deviceInfo: {
    platform: string;
    model: string;
    osVersion: string;
  };
  appVersion?: string;
}

const FEEDBACK_DIR = `${FileSystem.cacheDirectory}feedback/`;

async function ensureFeedbackDir() {
  const info = await FileSystem.getInfoAsync(FEEDBACK_DIR);
  if (!info.exists) await FileSystem.makeDirectoryAsync(FEEDBACK_DIR, { intermediates: true });
}

// Start a feedback session
export async function startFeedbackSession(mode: 'live' | 'narrated' | 'batch' = 'narrated'): Promise<FeedbackSession> {
  await ensureFeedbackDir();
  const id = Date.now().toString(36);

  // Start screen recording via native module (if available)
  try {
    if (NativeModules.ScreenRecorder) {
      await NativeModules.ScreenRecorder.startRecording();
    }
  } catch (e) {
    console.warn('[feedback] Screen recording not available:', e);
  }

  return {
    id,
    recording: true,
    startTime: Date.now(),
    screenshots: [],
    voiceNotes: [],
    mode,
  };
}

// Take screenshot at current timestamp
export async function captureScreenshot(session: FeedbackSession, annotation?: string): Promise<void> {
  const elapsed = (Date.now() - session.startTime) / 1000;
  // Screenshots would be captured via native module or react-native-view-shot
  // For now, record the annotation
  if (annotation) {
    session.voiceNotes.push({ time: elapsed, text: annotation });
  }
}

// Stop recording and prepare bundle
export async function stopFeedbackSession(session: FeedbackSession): Promise<FeedbackBundle> {
  session.recording = false;

  let videoPath: string | undefined;
  try {
    if (NativeModules.ScreenRecorder) {
      videoPath = await NativeModules.ScreenRecorder.stopRecording();
    }
  } catch (e) {
    console.warn('[feedback] Stop recording error:', e);
  }

  return {
    videoPath,
    screenshots: session.screenshots,
    timeline: session.voiceNotes.map(n => ({
      time: n.time,
      type: 'voice',
      text: n.text,
    })),
    deviceInfo: {
      platform: Platform.OS,
      model: Platform.OS === 'ios' ? 'iPhone' : 'Android',
      osVersion: Platform.Version?.toString() || 'unknown',
    },
  };
}

// Upload feedback bundle to agent via multipart
export async function uploadFeedback(
  baseUrl: string,
  authHeaders: Record<string, string>,
  bundle: FeedbackBundle,
  onProgress?: (percent: number) => void,
): Promise<string> {
  const formData = new FormData();

  // Add metadata
  formData.append('metadata', JSON.stringify({
    source: 'yaver-app',
    deviceInfo: bundle.deviceInfo,
    timeline: bundle.timeline,
    appVersion: bundle.appVersion,
  }));

  // Add video
  if (bundle.videoPath) {
    formData.append('video', {
      uri: bundle.videoPath,
      type: 'video/mp4',
      name: 'recording.mp4',
    } as any);
  }

  // Add screenshots
  for (let i = 0; i < bundle.screenshots.length; i++) {
    formData.append(`screenshot_${i}`, {
      uri: bundle.screenshots[i],
      type: 'image/jpeg',
      name: `screenshot_${i}.jpg`,
    } as any);
  }

  const resp = await fetch(`${baseUrl}/feedback`, {
    method: 'POST',
    headers: authHeaders,
    body: formData,
  });

  if (!resp.ok) throw new Error(`Upload failed: ${resp.status}`);
  const result = await resp.json();
  return result.id;
}

// Clear cached feedback files
export async function clearFeedbackCache(): Promise<void> {
  await FileSystem.deleteAsync(FEEDBACK_DIR, { idempotent: true });
  await ensureFeedbackDir();
}
