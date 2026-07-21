/* eslint-disable */
/**
 * Generated `api` utility.
 *
 * THIS CODE IS AUTOMATICALLY GENERATED.
 *
 * To regenerate, run `npx convex dev`.
 * @module
 */

import type * as access from "../access.js";
import type * as admin from "../admin.js";
import type * as agentRescue from "../agentRescue.js";
import type * as agentSync from "../agentSync.js";
import type * as aiModels from "../aiModels.js";
import type * as aiRunners from "../aiRunners.js";
import type * as auth from "../auth.js";
import type * as authLogs from "../authLogs.js";
import type * as authPasswordPolicy from "../authPasswordPolicy.js";
import type * as byoMachines from "../byoMachines.js";
import type * as cleanup from "../cleanup.js";
import type * as cloudLifecycle from "../cloudLifecycle.js";
import type * as cloudMachines from "../cloudMachines.js";
import type * as cloudPlacementCapacity from "../cloudPlacementCapacity.js";
import type * as cloudPoolPlacement from "../cloudPoolPlacement.js";
import type * as cloudProviderPlacement from "../cloudProviderPlacement.js";
import type * as cloudProviders_abstract from "../cloudProviders/abstract.js";
import type * as cloudProviders_aws from "../cloudProviders/aws.js";
import type * as cloudProviders_azure from "../cloudProviders/azure.js";
import type * as cloudProviders_bedrockInference from "../cloudProviders/bedrockInference.js";
import type * as cloudProviders_credentials from "../cloudProviders/credentials.js";
import type * as cloudProviders_gcp from "../cloudProviders/gcp.js";
import type * as cloudProviders_hetzner from "../cloudProviders/hetzner.js";
import type * as cloudProviders_openaiCompatibleInference from "../cloudProviders/openaiCompatibleInference.js";
import type * as cloudProviders_placementLadder from "../cloudProviders/placementLadder.js";
import type * as cloudProviders_registry from "../cloudProviders/registry.js";
import type * as cloudProviders_selection from "../cloudProviders/selection.js";
import type * as cloudProviders_types from "../cloudProviders/types.js";
import type * as companion from "../companion.js";
import type * as companyAIOptions from "../companyAIOptions.js";
import type * as connections from "../connections.js";
import type * as cronSecret from "../cronSecret.js";
import type * as crons from "../crons.js";
import type * as developerLogs from "../developerLogs.js";
import type * as deviceCode from "../deviceCode.js";
import type * as deviceEvents from "../deviceEvents.js";
import type * as deviceLabels from "../deviceLabels.js";
import type * as deviceMetrics from "../deviceMetrics.js";
import type * as devices from "../devices.js";
import type * as downloads from "../downloads.js";
import type * as edgePlacement from "../edgePlacement.js";
import type * as email from "../email.js";
import type * as feedbackWorkItems from "../feedbackWorkItems.js";
import type * as gatewayPolicy from "../gatewayPolicy.js";
import type * as gatewaySecret from "../gatewaySecret.js";
import type * as gatewayTokens from "../gatewayTokens.js";
import type * as githubAppAuth from "../githubAppAuth.js";
import type * as gpuRentals from "../gpuRentals.js";
import type * as guests from "../guests.js";
import type * as hostShare from "../hostShare.js";
import type * as http from "../http.js";
import type * as inferenceBackends from "../inferenceBackends.js";
import type * as inferencePlacement from "../inferencePlacement.js";
import type * as machineLinkFix from "../machineLinkFix.js";
import type * as managedMeter from "../managedMeter.js";
import type * as managedRelays from "../managedRelays.js";
import type * as managedServices from "../managedServices.js";
import type * as mesh from "../mesh.js";
import type * as mobileStreamLogs from "../mobileStreamLogs.js";
import type * as openrouterKeys from "../openrouterKeys.js";
import type * as ownerAllowlist from "../ownerAllowlist.js";
import type * as packages from "../packages.js";
import type * as passkeys from "../passkeys.js";
import type * as passkeysDb from "../passkeysDb.js";
import type * as pendingDeviceClaims from "../pendingDeviceClaims.js";
import type * as plans from "../plans.js";
import type * as platformConfig from "../platformConfig.js";
import type * as privacyMigrations from "../privacyMigrations.js";
import type * as projectArtifacts from "../projectArtifacts.js";
import type * as projectShares from "../projectShares.js";
import type * as providerCatalog from "../providerCatalog.js";
import type * as providerTroubleshooting from "../providerTroubleshooting.js";
import type * as provisionRelay from "../provisionRelay.js";
import type * as provisioning from "../provisioning.js";
import type * as publishJobs from "../publishJobs.js";
import type * as pushNotifications from "../pushNotifications.js";
import type * as relayPool from "../relayPool.js";
import type * as relaySourceIntents from "../relaySourceIntents.js";
import type * as runnerUsage from "../runnerUsage.js";
import type * as runtimeSlices from "../runtimeSlices.js";
import type * as seed from "../seed.js";
import type * as serverlessPool from "../serverlessPool.js";
import type * as shortcuts from "../shortcuts.js";
import type * as subscriptions from "../subscriptions.js";
import type * as support_link from "../support_link.js";
import type * as survey from "../survey.js";
import type * as taskDispatchIntents from "../taskDispatchIntents.js";
import type * as taskPackages from "../taskPackages.js";
import type * as taskPlacement from "../taskPlacement.js";
import type * as taskPlacementClassifier from "../taskPlacementClassifier.js";
import type * as teams from "../teams.js";
import type * as totp from "../totp.js";
import type * as unitEconomics from "../unitEconomics.js";
import type * as userDomains from "../userDomains.js";
import type * as userSettings from "../userSettings.js";
import type * as wakeRuns from "../wakeRuns.js";
import type * as whatsapp from "../whatsapp.js";

import type {
  ApiFromModules,
  FilterApi,
  FunctionReference,
} from "convex/server";

declare const fullApi: ApiFromModules<{
  access: typeof access;
  admin: typeof admin;
  agentRescue: typeof agentRescue;
  agentSync: typeof agentSync;
  aiModels: typeof aiModels;
  aiRunners: typeof aiRunners;
  auth: typeof auth;
  authLogs: typeof authLogs;
  authPasswordPolicy: typeof authPasswordPolicy;
  byoMachines: typeof byoMachines;
  cleanup: typeof cleanup;
  cloudLifecycle: typeof cloudLifecycle;
  cloudMachines: typeof cloudMachines;
  cloudPlacementCapacity: typeof cloudPlacementCapacity;
  cloudPoolPlacement: typeof cloudPoolPlacement;
  cloudProviderPlacement: typeof cloudProviderPlacement;
  "cloudProviders/abstract": typeof cloudProviders_abstract;
  "cloudProviders/aws": typeof cloudProviders_aws;
  "cloudProviders/azure": typeof cloudProviders_azure;
  "cloudProviders/bedrockInference": typeof cloudProviders_bedrockInference;
  "cloudProviders/credentials": typeof cloudProviders_credentials;
  "cloudProviders/gcp": typeof cloudProviders_gcp;
  "cloudProviders/hetzner": typeof cloudProviders_hetzner;
  "cloudProviders/openaiCompatibleInference": typeof cloudProviders_openaiCompatibleInference;
  "cloudProviders/placementLadder": typeof cloudProviders_placementLadder;
  "cloudProviders/registry": typeof cloudProviders_registry;
  "cloudProviders/selection": typeof cloudProviders_selection;
  "cloudProviders/types": typeof cloudProviders_types;
  companion: typeof companion;
  companyAIOptions: typeof companyAIOptions;
  connections: typeof connections;
  cronSecret: typeof cronSecret;
  crons: typeof crons;
  developerLogs: typeof developerLogs;
  deviceCode: typeof deviceCode;
  deviceEvents: typeof deviceEvents;
  deviceLabels: typeof deviceLabels;
  deviceMetrics: typeof deviceMetrics;
  devices: typeof devices;
  downloads: typeof downloads;
  edgePlacement: typeof edgePlacement;
  email: typeof email;
  feedbackWorkItems: typeof feedbackWorkItems;
  gatewayPolicy: typeof gatewayPolicy;
  gatewaySecret: typeof gatewaySecret;
  gatewayTokens: typeof gatewayTokens;
  githubAppAuth: typeof githubAppAuth;
  gpuRentals: typeof gpuRentals;
  guests: typeof guests;
  hostShare: typeof hostShare;
  http: typeof http;
  inferenceBackends: typeof inferenceBackends;
  inferencePlacement: typeof inferencePlacement;
  machineLinkFix: typeof machineLinkFix;
  managedMeter: typeof managedMeter;
  managedRelays: typeof managedRelays;
  managedServices: typeof managedServices;
  mesh: typeof mesh;
  mobileStreamLogs: typeof mobileStreamLogs;
  openrouterKeys: typeof openrouterKeys;
  ownerAllowlist: typeof ownerAllowlist;
  packages: typeof packages;
  passkeys: typeof passkeys;
  passkeysDb: typeof passkeysDb;
  pendingDeviceClaims: typeof pendingDeviceClaims;
  plans: typeof plans;
  platformConfig: typeof platformConfig;
  privacyMigrations: typeof privacyMigrations;
  projectArtifacts: typeof projectArtifacts;
  projectShares: typeof projectShares;
  providerCatalog: typeof providerCatalog;
  providerTroubleshooting: typeof providerTroubleshooting;
  provisionRelay: typeof provisionRelay;
  provisioning: typeof provisioning;
  publishJobs: typeof publishJobs;
  pushNotifications: typeof pushNotifications;
  relayPool: typeof relayPool;
  relaySourceIntents: typeof relaySourceIntents;
  runnerUsage: typeof runnerUsage;
  runtimeSlices: typeof runtimeSlices;
  seed: typeof seed;
  serverlessPool: typeof serverlessPool;
  shortcuts: typeof shortcuts;
  subscriptions: typeof subscriptions;
  support_link: typeof support_link;
  survey: typeof survey;
  taskDispatchIntents: typeof taskDispatchIntents;
  taskPackages: typeof taskPackages;
  taskPlacement: typeof taskPlacement;
  taskPlacementClassifier: typeof taskPlacementClassifier;
  teams: typeof teams;
  totp: typeof totp;
  unitEconomics: typeof unitEconomics;
  userDomains: typeof userDomains;
  userSettings: typeof userSettings;
  wakeRuns: typeof wakeRuns;
  whatsapp: typeof whatsapp;
}>;

/**
 * A utility for referencing Convex functions in your app's public API.
 *
 * Usage:
 * ```js
 * const myFunctionReference = api.myModule.myFunction;
 * ```
 */
export declare const api: FilterApi<
  typeof fullApi,
  FunctionReference<any, "public">
>;

/**
 * A utility for referencing Convex functions in your app's internal API.
 *
 * Usage:
 * ```js
 * const myFunctionReference = internal.myModule.myFunction;
 * ```
 */
export declare const internal: FilterApi<
  typeof fullApi,
  FunctionReference<any, "internal">
>;

export declare const components: {};
