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
  AccountLinkSession,
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
export { connect, connectHandle, pickTransport, buildCandidates, AgentSession } from './connect';
export type { Transport, TransportKind, ConnectOptions } from './connect';
export { YaverBroker } from './broker';
export type { ConnectBundle, YaverBrokerOptions } from './broker';

// Fleet — drive a SET of remote machines from code (select by tag, fan exec).
export { Fleet, Machine, Selection } from './fleet';
export type {
  FleetConnectOptions,
  MachineInfo,
  SelectFilter,
  ExecLine,
  ExecResult,
  ExecOpts,
} from './fleet';

// Developer-API facade (the boundary consumers use)
export { YaverApp } from './app';
export type { YaverAppOptions, SessionHandle, AppStatus } from './app';
export type { AgentStatus, AgentRunnerState } from './connect';

// Generic policy + runtime resolver (the "OpenRouter of coding agents" spine).
// Apps register an AppProfile; Yaver core stays free of app vocabulary.
export { YaverPolicyClient, selectRunner, selectProvider, isWorkKindEnabled } from './policy';
export type {
  CompanyAIOptions,
  CompanyAIOptionsResponse,
  ResolvedSession,
  ResolveRequest,
  ResolveSource,
  AppProfile,
  WorkKindDef,
  RolePolicy,
  ProviderDef,
  ProviderKeyPolicy,
  TenantComputeProvider,
  RuntimeMode,
  CredentialMode,
  TeamSummary,
} from './policy';

// Composable ACL — the new company policy is ONE layer that intersects with
// Yaver's existing layers (guest grants, SDK-token scopes, host-share, peer
// ACL, the user's own prefs). Jointly inclusive, never forcing.
export {
  composeEntitlements,
  entitlementAllows,
  entitlementFromResolved,
  entitlementFromGuest,
  entitlementFromSdkToken,
  entitlementFromHostShare,
  entitlementFromUser,
  LAYER4_DENIED_TOOLS,
} from './acl';
export type { Entitlement, EffectiveEntitlement } from './acl';

// Companion compute — crons + workers for serverless projects
// (yaver.companion.yaml). Standalone client; talks to a resolved agent baseURL.
export { CompanionClient } from './companion';
export type {
  CompanionClientOptions,
  CompanionDetectItem,
  CompanionDetectResult,
  CompanionStatus,
  CompanionCronStatus,
  CompanionSvcStatus,
  CompanionProjectSummary,
} from './companion';
