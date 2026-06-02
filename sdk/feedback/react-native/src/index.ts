/**
 * yaver-feedback-react-native — Visual feedback SDK for Yaver.
 *
 * Shake-to-report surface with three launch actions:
 *  1. Hot Reload              — instant JS reload
 *  2. Vibing                  — open a vibing session on the agent
 *  3. Screenshot & Fix        — capture the current screen and trigger
 *                               the fix loop
 *
 * The small quick-access icon stays hidden until the first shake by
 * default on mobile, then remains available unless the user hides it.
 *
 * @example
 * ```tsx
 * import { YaverFeedback, FeedbackModal } from 'yaver-feedback-react-native';
 *
 * YaverFeedback.init({
 *   agentUrl: 'http://192.168.1.10:18080',
 *   authToken: 'your-token',
 *   trigger: 'shake',
 *   strictNativeAuth: true,
 * });
 *
 * <>
 *   <App />
 *   <FeedbackModal />
 * </>
 * ```
 */

export { YaverFeedback } from './YaverFeedback';
export { captureStoreScreenshots } from './storeShots';
export type {
  CaptureStoreScreenshotsOptions,
  CaptureStoreScreenshotsResult,
  StoreShotFrame,
} from './storeShots';
export { BlackBox } from './BlackBox';
export { YaverUpdates } from './YaverUpdates';
export type { YaverUpdatesConfig, PendingUpdate } from './YaverUpdates';
export { initExpo } from './expo';
export { YaverDiscovery } from './Discovery';
export { P2PClient } from './P2PClient';
export { YaverConnectionScreen } from './ConnectionScreen';
export { YaverLoginScreen } from './LoginScreen';
export type { YaverLoginScreenProps } from './LoginScreen';
export { YaverMachinePickerScreen } from './MachinePickerScreen';
export type { YaverMachinePickerProps } from './MachinePickerScreen';
export { YaverGuestOnboardingScreen } from './GuestOnboardingScreen';
export type { YaverGuestOnboardingScreenProps } from './GuestOnboardingScreen';
export { PairDeviceModal } from './PairDeviceModal';
export type { PairDeviceModalProps } from './PairDeviceModal';
export { AuthOverlay } from './AuthOverlay';
export { ShakeDetector } from './ShakeDetector';
export { FloatingButton } from './FloatingButton';
export { FeedbackModal } from './FeedbackModal';
export { QuickActionIcon } from './QuickActionIcon';
export type { QuickActionIconProps } from './QuickActionIcon';
export { FixReport } from './FixReport';
export {
  getQuickIconDisabled,
  setQuickIconDisabled,
  clearQuickIconDisabled,
} from './preferences';
export {
  configureAuthEndpoints,
  getConvexSiteUrl,
  getWebBaseUrl,
  getToken,
  saveToken,
  clearToken,
  getUser,
  saveUser,
  getSelectedDeviceId,
  saveSelectedDeviceId,
  clearSelectedDeviceId,
  validateToken,
  signInWithApple,
  signInWithOAuth,
  signupWithEmail,
  loginWithEmail,
  listReachableDevices,
  fetchGuestHosts,
  findInviteByCode,
  acceptGuestByCode,
  acceptGuestInvitation,
  DEFAULT_CONVEX_SITE_URL,
  DEFAULT_WEB_BASE_URL,
  DEFAULT_OAUTH_REDIRECT,
} from './auth';
export type {
  OAuthProvider,
  User,
  RemoteDevice,
  DeviceList,
  GuestInvitation,
  ActiveGuestHost,
  GuestHostsResponse,
  InvitationHostDevice,
  InvitationPreview,
} from './auth';
export {
  captureScreenshot,
  pickFeedbackFile,
  startVideoRecording,
  stopVideoRecording,
  isVideoRecording,
} from './capture';
export { uploadFeedback } from './upload';
export type {
  FeedbackConfig,
  FeedbackBundle,
  FeedbackMetadata,
  DeviceInfo,
  AppInfo,
  TimelineEvent,
  FeedbackReport,
  FeedbackStreamEvent,
  VoiceCapability,
  CapturedError,
  TestFix,
  TestSession,
} from './types';
export type { BlackBoxEvent, BlackBoxConfig, BlackBoxCommand, CommandHandler } from './BlackBox';
export type { DiscoveryResult } from './Discovery';
export type { FeedbackEvent } from './P2PClient';
