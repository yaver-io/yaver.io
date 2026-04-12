import { Platform } from 'react-native';
import { FeedbackBundle } from './types';

/**
 * Upload a feedback bundle to the Yaver agent via multipart POST.
 *
 * The agent receives the bundle at POST /feedback with:
 * - `metadata` (JSON string)
 * - `screenshot_0`, `screenshot_1`, ... (image files)
 * - `audio` (audio file, if present)
 * - `video` (video file, if present)
 *
 * @returns The feedback report ID from the agent response.
 */
export async function uploadFeedback(
  agentUrl: string,
  authToken: string,
  bundle: FeedbackBundle,
): Promise<string> {
  const formData = new FormData();

  // Attach metadata as JSON
  formData.append('metadata', JSON.stringify(bundle.metadata));

  // Attach screenshots
  for (let i = 0; i < bundle.screenshots.length; i++) {
    const path = bundle.screenshots[i];
    formData.append(`screenshot_${i}`, {
      uri: Platform.OS === 'android' ? `file://${path}` : path,
      type: 'image/png',
      name: `screenshot_${i}.png`,
    } as any);
  }

  // Attach audio
  if (bundle.audio) {
    formData.append('audio', {
      uri:
        Platform.OS === 'android' ? `file://${bundle.audio}` : bundle.audio,
      type: 'audio/m4a',
      name: 'voice_note.m4a',
    } as any);
  }

  // Attach video
  if (bundle.video) {
    formData.append('video', {
      uri:
        Platform.OS === 'android' ? `file://${bundle.video}` : bundle.video,
      type: 'video/mp4',
      name: 'screen_recording.mp4',
    } as any);
  }

  const url = agentUrl.replace(/\/$/, '') + '/feedback';

  const response = await fetch(url, {
    method: 'POST',
    headers: {
      Authorization: `Bearer ${authToken}`,
    },
    body: formData,
  });

  if (!response.ok) {
    const text = await response.text().catch(() => '');
    throw new Error(
      `[YaverFeedback] Upload failed (${response.status}): ${text}`,
    );
  }

  const result = await response.json();
  return result.id ?? result.reportId ?? 'unknown';
}
