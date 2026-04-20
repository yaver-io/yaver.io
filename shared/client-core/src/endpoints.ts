/**
 * Endpoint paths for every service the Yaver clients talk to.
 *
 * Kept here so a typo in one consumer doesn't cause a subtle 404 that
 * only shows up in one client. Any new agent endpoint added to
 * `desktop/agent/httpserver.go` should get a matching entry here.
 *
 * Source of truth — see ARCHITECTURE_CLIENT_CORE.md. Copies in
 * `mobile/src/_core/` and `sdk/feedback/react-native/src/_core/`
 * must remain byte-identical; do not edit those directly.
 */

/** Routes on the user's Go agent (http://<host>:18080). */
export const AGENT_ENDPOINTS = {
  health: '/health',
  info: '/info',
  agentStatus: '/agent/status',
  runners: '/agent/runners',

  feedback: '/feedback',
  feedbackFix: (id: string) => `/feedback/${encodeURIComponent(id)}/fix`,
  feedbackStream: '/feedback/stream',

  devReload: '/dev/reload',
  devReloadApp: '/dev/reload-app',
  devStart: '/dev/start',
  devStop: '/dev/stop',
  devStatus: '/dev/status',
  devEvents: '/dev/events',
  devBuildNative: '/dev/build-native',

  builds: '/builds',
  buildArtifact: (id: string) => `/builds/${encodeURIComponent(id)}/artifact`,

  tasks: '/tasks',
  taskById: (id: string) => `/tasks/${encodeURIComponent(id)}`,
  taskStop: (id: string) => `/tasks/${encodeURIComponent(id)}/stop`,

  vibing: '/vibing',
  vibingExecute: '/vibing/execute',

  voiceStatus: '/voice/status',
  voiceTranscribe: '/voice/transcribe',

  blackboxEvents: '/blackbox/events',
  blackboxCommandStream: '/blackbox/command-stream',

  sdkTokenRotate: '/sdk/token/rotate',
  testAppStart: '/test-app/start',
  testAppStop: '/test-app/stop',
  testAppStatus: '/test-app/status',
} as const;

/** Routes on the yaver.io Convex deployment. */
export const CONVEX_ENDPOINTS = {
  devicesList: '/devices/list',
  deviceRemove: '/devices/remove',
  authValidate: '/auth/validate',
  authAppleNative: '/auth/apple-native',
  authSignup: '/auth/signup',
  authLogin: '/auth/login',
  userSettings: '/settings',
  platformConfig: '/config',
  guestsList: '/guests/list',
  guestsHosts: '/guests/hosts',
  guestsAllowed: '/guests/allowed',
  guestsInvite: '/guests/invite',
  guestsAccept: '/guests/accept',
  guestsAcceptCode: '/guests/accept-code',
  guestsRevoke: '/guests/revoke',
} as const;

/** Routes on a relay server. */
export const RELAY_ENDPOINTS = {
  presence: '/presence',
  forDevice: (deviceId: string) => `/d/${encodeURIComponent(deviceId)}`,
} as const;
