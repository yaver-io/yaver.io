/**
 * @yaver/feedback-react-native — Visual feedback SDK for Yaver.
 *
 * Shake-to-report, screenshots, voice annotations, P2P connection,
 * device discovery, and live/narrated/batch feedback modes for vibe coding.
 *
 * @example
 * ```tsx
 * import { YaverFeedback, FeedbackProvider } from '@yaver/feedback-react-native';
 *
 * YaverFeedback.init({
 *   agentUrl: 'http://192.168.1.10:18080',
 *   authToken: 'your-token',
 *   trigger: 'shake',
 *   feedbackMode: 'live',
 * });
 *
 * // Wrap your app root:
 * <FeedbackProvider>
 *   <App />
 * </FeedbackProvider>
 * ```
 */

export { YaverFeedback } from './YaverFeedback';
export { BlackBox } from './BlackBox';
export { initExpo } from './expo';
export { YaverDiscovery } from './Discovery';
export { P2PClient } from './P2PClient';
export { YaverConnectionScreen } from './ConnectionScreen';
export { ShakeDetector } from './ShakeDetector';
export { FloatingButton } from './FloatingButton';
export { FeedbackModal } from './FeedbackModal';
export { FixReport } from './FixReport';
export { captureScreenshot, startAudioRecording, stopAudioRecording } from './capture';
export { uploadFeedback } from './upload';
export type {
  FeedbackConfig,
  FeedbackBundle,
  FeedbackMetadata,
  DeviceInfo,
  AppInfo,
  TimelineEvent,
  FeedbackReport,
  AgentCommentary,
  FeedbackStreamEvent,
  VoiceCapability,
  CapturedError,
  TestFix,
  TestSession,
} from './types';
export type { BlackBoxEvent, BlackBoxConfig } from './BlackBox';
export type { DiscoveryResult } from './Discovery';
export type { FeedbackEvent } from './P2PClient';
