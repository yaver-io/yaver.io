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
import type * as byoMachines from "../byoMachines.js";
import type * as cleanup from "../cleanup.js";
import type * as cloudLifecycle from "../cloudLifecycle.js";
import type * as cloudMachines from "../cloudMachines.js";
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
import type * as gatewaySecret from "../gatewaySecret.js";
import type * as gpuRentals from "../gpuRentals.js";
import type * as guests from "../guests.js";
import type * as hostShare from "../hostShare.js";
import type * as http from "../http.js";
import type * as managedMeter from "../managedMeter.js";
import type * as managedRelays from "../managedRelays.js";
import type * as managedServices from "../managedServices.js";
import type * as mesh from "../mesh.js";
import type * as mobileStreamLogs from "../mobileStreamLogs.js";
import type * as ownerAllowlist from "../ownerAllowlist.js";
import type * as packages from "../packages.js";
import type * as passkeys from "../passkeys.js";
import type * as passkeysDb from "../passkeysDb.js";
import type * as pendingDeviceClaims from "../pendingDeviceClaims.js";
import type * as platformConfig from "../platformConfig.js";
import type * as privacyMigrations from "../privacyMigrations.js";
import type * as projectShares from "../projectShares.js";
import type * as provisionRelay from "../provisionRelay.js";
import type * as provisioning from "../provisioning.js";
import type * as publishJobs from "../publishJobs.js";
import type * as runnerUsage from "../runnerUsage.js";
import type * as seed from "../seed.js";
import type * as shortcuts from "../shortcuts.js";
import type * as subscriptions from "../subscriptions.js";
import type * as support_link from "../support_link.js";
import type * as survey from "../survey.js";
import type * as teams from "../teams.js";
import type * as totp from "../totp.js";
import type * as userDomains from "../userDomains.js";
import type * as userSettings from "../userSettings.js";

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
  byoMachines: typeof byoMachines;
  cleanup: typeof cleanup;
  cloudLifecycle: typeof cloudLifecycle;
  cloudMachines: typeof cloudMachines;
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
  gatewaySecret: typeof gatewaySecret;
  gpuRentals: typeof gpuRentals;
  guests: typeof guests;
  hostShare: typeof hostShare;
  http: typeof http;
  managedMeter: typeof managedMeter;
  managedRelays: typeof managedRelays;
  managedServices: typeof managedServices;
  mesh: typeof mesh;
  mobileStreamLogs: typeof mobileStreamLogs;
  ownerAllowlist: typeof ownerAllowlist;
  packages: typeof packages;
  passkeys: typeof passkeys;
  passkeysDb: typeof passkeysDb;
  pendingDeviceClaims: typeof pendingDeviceClaims;
  platformConfig: typeof platformConfig;
  privacyMigrations: typeof privacyMigrations;
  projectShares: typeof projectShares;
  provisionRelay: typeof provisionRelay;
  provisioning: typeof provisioning;
  publishJobs: typeof publishJobs;
  runnerUsage: typeof runnerUsage;
  seed: typeof seed;
  shortcuts: typeof shortcuts;
  subscriptions: typeof subscriptions;
  support_link: typeof support_link;
  survey: typeof survey;
  teams: typeof teams;
  totp: typeof totp;
  userDomains: typeof userDomains;
  userSettings: typeof userSettings;
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
