/**
 * yaver-sdk — Embed Yaver's P2P AI agent connectivity into your apps.
 *
 * Works in React Native, Node.js, and browsers.
 *
 * @example
 * ```ts
 * import { YaverClient } from 'yaver-sdk';
 *
 * const client = new YaverClient('http://localhost:18080', 'your-token');
 * const task = await client.createTask('Fix the login bug');
 * for await (const chunk of client.streamOutput(task.id)) {
 *   process.stdout.write(chunk);
 * }
 * ```
 */

export { YaverClient } from './client';
export { YaverAuthClient } from './auth';
export { transcribe, SPEECH_PROVIDERS } from './speech';
export type {
  Task,
  Turn,
  CreateTaskOptions,
  SpeechContext,
  AgentInfo,
  User,
  Device,
  UserSettings,
  SpeechProvider,
  SpeechProviderInfo,
  TranscriptionResult,
  ExecSession,
  ExecOptions,
  RunnerInfo,
  RunnerAuthSession,
  RunnerSetupOptions,
  YaverCapability,
} from './types';

// Phone-backend runtime — what a third-party RN/web app uses to hit the
// developer's Yaver-hosted project.
export { createYaverBackendClient, YaverBackendError } from './backend';
export type {
  YaverBackendClient,
  YaverBackendClientOptions,
  YaverCollection,
} from './backend';

// Client connection ladder (@yaver/client) + server broker (@yaver/server)
export { YaverConvexClient, DEFAULT_CONVEX_URL } from './discovery';
export type { DeviceCoords, RelayServer, YaverSettings } from './discovery';
export { connect, pickTransport, buildCandidates, AgentSession } from './connect';
export type { Transport, TransportKind, ConnectOptions } from './connect';
export { YaverBroker } from './broker';
export type { ConnectBundle, YaverBrokerOptions } from './broker';
