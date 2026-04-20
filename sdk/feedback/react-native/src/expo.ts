/**
 * Expo-specific auto-initialization for the Yaver Feedback SDK.
 *
 * Reads agent URL from Expo Constants manifest extra if available,
 * otherwise falls back to LAN auto-discovery.
 *
 * @example
 * ```tsx
 * import { initExpo, FeedbackModal } from '@yaver/feedback-react-native';
 *
 * initExpo(); // auto-discovers your dev machine
 *
 * function App() {
 *   return (
 *     <>
 *       <YourApp />
 *       <FeedbackModal />
 *     </>
 *   );
 * }
 * ```
 */
import { YaverFeedback } from './YaverFeedback';
import type { FeedbackConfig } from './types';

/**
 * Initialize the Yaver Feedback SDK for Expo projects.
 *
 * Attempts to read the agent URL from Expo Constants (`expo.extra.yaverAgentUrl`
 * in app.json). If not set, the SDK auto-discovers agents on the local network.
 *
 * Defaults:
 * - trigger: 'shake'
 * - enabled: __DEV__ (only active in development)
 *
 * @param overrides - Optional partial config to override defaults
 */
export function initExpo(overrides?: Partial<FeedbackConfig>): void {
  let agentUrl: string | undefined;

  // Try reading agent URL from Expo Constants manifest extra
  try {
    // Dynamic require so this doesn't hard-fail if expo-constants isn't installed
    const Constants = require('expo-constants').default;
    agentUrl =
      Constants.expoConfig?.extra?.yaverAgentUrl ??
      Constants.manifest?.extra?.yaverAgentUrl ??
      Constants.manifest2?.extra?.expoClient?.extra?.yaverAgentUrl;
  } catch {
    // expo-constants not available — auto-discovery will be used
  }

  YaverFeedback.init({
    authToken: '', // LAN auto-discovery doesn't require a token
    trigger: 'shake',
    enabled: __DEV__,
    ...overrides,
    ...(agentUrl ? { agentUrl } : {}),
  });
}
