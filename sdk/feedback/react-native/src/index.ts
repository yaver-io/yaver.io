/**
 * yaver-feedback-react-native — Visual feedback SDK for Yaver.
 *
 * Shake-to-report surface with one primary action: Chat. The connected agent
 * receives the current-screen context and can use its MCP tools for fixes,
 * reloads, and deploys without duplicating those commands as modal buttons.
 * Machine, runner, model, and Dogfood configuration remain under Settings.
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
export type {
  DogfoodOnboardingOptions,
  DogfoodFlowPhase,
  DogfoodFlowState,
  DogfoodControlTriggerState,
} from './YaverFeedback';
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
export { BrowserShortcutController, suggestBrowserShortcutOrigin, verifyBrowserShortcutAssets } from './BrowserShortcut';
export { BrowserShortcutStatusRail } from './BrowserShortcutStatusRail';
export type { BrowserShortcutStatusColors } from './BrowserShortcutStatusRail';
export type {
  BrowserShortcutBrand,
  BrowserShortcutBuildResult,
  BrowserShortcutDriver,
  BrowserShortcutEnrollment,
  BrowserShortcutFixRoute,
  BrowserShortcutPhase,
  BrowserShortcutPreflight,
  BrowserShortcutRelease,
  BrowserShortcutRequest,
  BrowserShortcutSnapshot,
  BrowserShortcutStep,
} from './BrowserShortcut';
export type {
  DogfoodDevEvent,
  DogfoodDevServerStatus,
  DogfoodRemoteRuntimeCapabilities,
  DogfoodRemoteRuntimeSession,
  DogfoodRemoteRuntimeTarget,
} from './P2PClient';
export type { DogfoodReloadOptions, ReloadAck } from './P2PClient';
export type { DogfoodRuntimeSelection } from './preferences';
export { createP2PDogfoodDriver, type P2PDogfoodDriverOptions } from './P2PDogfoodDriver';
export {
  reloadActions,
  reloadRequest,
  reloadFrameworkFamily,
  describeReloadFailure,
  RELOAD_PATH,
  RELOAD_APP_PATH,
} from './reloadActions';
export type {
  ReloadAction,
  ReloadActionId,
  ReloadActionsOptions,
  ReloadWireMode,
  DevServerSnapshot,
} from './reloadActions';
export { YaverConnectionScreen } from './ConnectionScreen';
export { YaverLoginScreen } from './LoginScreen';
export type { YaverLoginScreenProps } from './LoginScreen';
export { YaverMachinePickerScreen } from './MachinePickerScreen';
export type { YaverMachinePickerProps } from './MachinePickerScreen';
export { PairDeviceModal } from './PairDeviceModal';
export type { PairDeviceModalProps } from './PairDeviceModal';
export { AuthOverlay } from './AuthOverlay';
export { ShakeDetector } from './ShakeDetector';
export { FloatingButton } from './FloatingButton';
// The polite "you're inside Yaver" mark. Rendered automatically by the SDK's
// overlay host when config.modeBadge !== false; exported so an app that manages
// its own overlay tree can place it itself.
export { YaverModeBadge, hideYaverModeBadge, showYaverModeBadge, isYaverModeBadgeHidden } from './YaverModeBadge';
export type { YaverModeBadgeProps } from './YaverModeBadge';
export {
  resolveSDKDogfood,
  resolveDogfoodRenderBehavior,
  resolveDogfoodSessionBehavior,
  resolveDogfoodStartBehavior,
} from './dogfoodPolicy';
export type {
  DogfoodAccessSnapshot,
  DogfoodFlowSnapshot,
  DogfoodRenderBehavior,
  DogfoodSessionBehavior,
  DogfoodStartBehavior,
  DogfoodUsageMode,
  SDKDogfoodConfig,
  SDKDogfoodStatus,
} from './dogfoodPolicy';
export { YaverDeviceDogfood } from './deviceDogfood';
export type { DeviceDogfoodOptions, DeviceDogfoodSession, DeviceDogfoodState } from './deviceDogfood';
export {
  DogfoodController,
  DogfoodRuntimeError,
  defaultDogfoodLane,
  dogfoodLanePlan,
  dogfoodLaneOptions,
  dogfoodLogLinesFromDevEvent,
  runtimeLogLinesFromDevEvent,
  validateDogfoodProject,
} from './DogfoodRuntime';
export type {
  DogfoodControllerOptions,
  DogfoodDriver,
  DogfoodFailure,
  DogfoodLane,
  DogfoodLaneOption,
  DogfoodLanePlan,
  DogfoodLogLine,
  DogfoodPhase,
  DogfoodProject,
  DogfoodResult,
  DogfoodRunContext,
  DogfoodSnapshot,
} from './DogfoodRuntime';
export { DogfoodLanePicker, DogfoodLaunchingWidget, DogfoodLiveConsole, DogfoodStatusRail } from './DogfoodSessionUi';
export type {
  DogfoodStatusStep,
  DogfoodStatusTone,
  DogfoodUiColors,
} from './DogfoodSessionUi';
export { FeedbackModal } from './FeedbackModal';
export { DogfoodQuickControls } from './DogfoodQuickControls';
/** Semantic name for the in-app surface; DogfoodQuickControls remains as a
 * backwards-compatible export. */
export { DogfoodQuickControls as DogfoodUsage } from './DogfoodQuickControls';
export { DogfoodEntryIcon } from './DogfoodEntryIcon';
export type { DogfoodEntryIconProps } from './DogfoodEntryIcon';
export { getDogfoodEntryIconHidden, setDogfoodEntryIconHidden } from './preferences';
export { getDogfoodModeActive, setDogfoodModeActive } from './preferences';
export { DogfoodNativeMenu } from './DogfoodNativeMenu';
export type { DogfoodNativeMenuProps } from './DogfoodNativeMenu';
export { DogfoodSettings } from './DogfoodSettings';
export type { DogfoodSettingsProps } from './DogfoodSettings';
export { QuickActionIcon } from './QuickActionIcon';
export type { QuickActionIconProps } from './QuickActionIcon';
export { FixReport } from './FixReport';
export {
  getQuickIconDisabled,
  setQuickIconDisabled,
  clearQuickIconDisabled,
  getPreferredDogfoodLane,
  setPreferredDogfoodLane,
  getDogfoodStartBehavior,
  setDogfoodStartBehavior,
  getDogfoodRenderBehavior,
  setDogfoodRenderBehavior,
  getDogfoodSessionBehavior,
  setDogfoodSessionBehavior,
} from './preferences';
export type { VibeThreadSummary } from './P2PClient';
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
  getDogfoodAccountAccess,
  saveSelectedDeviceId,
  clearSelectedDeviceId,
  validateToken,
  signInWithApple,
  signInWithOAuth,
  signupWithEmail,
  loginWithEmail,
  listReachableDevices,
  DEFAULT_CONVEX_SITE_URL,
  DEFAULT_WEB_BASE_URL,
  DEFAULT_OAUTH_REDIRECT,
} from './auth';
export type {
  OAuthProvider,
  User,
  RemoteDevice,
  DeviceList,
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
