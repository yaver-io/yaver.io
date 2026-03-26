/* eslint-disable */
/**
 * Generated `api` utility.
 *
 * THIS CODE IS AUTOMATICALLY GENERATED.
 *
 * To regenerate, run `npx convex dev`.
 * @module
 */

import type * as admin from "../admin.js";
import type * as aiModels from "../aiModels.js";
import type * as aiRunners from "../aiRunners.js";
import type * as auth from "../auth.js";
import type * as authLogs from "../authLogs.js";
import type * as cleanup from "../cleanup.js";
import type * as cloudMachines from "../cloudMachines.js";
import type * as crons from "../crons.js";
import type * as developerLogs from "../developerLogs.js";
import type * as deviceCode from "../deviceCode.js";
import type * as deviceEvents from "../deviceEvents.js";
import type * as deviceMetrics from "../deviceMetrics.js";
import type * as devices from "../devices.js";
import type * as downloads from "../downloads.js";
import type * as http from "../http.js";
import type * as managedRelays from "../managedRelays.js";
import type * as mobileStreamLogs from "../mobileStreamLogs.js";
import type * as platformConfig from "../platformConfig.js";
import type * as provisionRelay from "../provisionRelay.js";
import type * as runnerUsage from "../runnerUsage.js";
import type * as seed from "../seed.js";
import type * as subscriptions from "../subscriptions.js";
import type * as survey from "../survey.js";
import type * as teams from "../teams.js";
import type * as totp from "../totp.js";
import type * as userSettings from "../userSettings.js";

import type {
  ApiFromModules,
  FilterApi,
  FunctionReference,
} from "convex/server";

declare const fullApi: ApiFromModules<{
  admin: typeof admin;
  aiModels: typeof aiModels;
  aiRunners: typeof aiRunners;
  auth: typeof auth;
  authLogs: typeof authLogs;
  cleanup: typeof cleanup;
  cloudMachines: typeof cloudMachines;
  crons: typeof crons;
  developerLogs: typeof developerLogs;
  deviceCode: typeof deviceCode;
  deviceEvents: typeof deviceEvents;
  deviceMetrics: typeof deviceMetrics;
  devices: typeof devices;
  downloads: typeof downloads;
  http: typeof http;
  managedRelays: typeof managedRelays;
  mobileStreamLogs: typeof mobileStreamLogs;
  platformConfig: typeof platformConfig;
  provisionRelay: typeof provisionRelay;
  runnerUsage: typeof runnerUsage;
  seed: typeof seed;
  subscriptions: typeof subscriptions;
  survey: typeof survey;
  teams: typeof teams;
  totp: typeof totp;
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
