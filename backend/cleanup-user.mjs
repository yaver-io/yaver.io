#!/usr/bin/env node
/**
 * Remove specific user data from Convex (users, sessions, devices).
 *
 * Usage:
 *   cd backend && node cleanup-user.mjs                           # dry-run, all providers
 *   cd backend && node cleanup-user.mjs --confirm                 # delete all providers
 *   cd backend && node cleanup-user.mjs --confirm --apple         # delete only Apple accounts
 *   cd backend && node cleanup-user.mjs --confirm --google        # delete only Google accounts
 *   cd backend && node cleanup-user.mjs --confirm --microsoft     # delete only Microsoft accounts
 *   cd backend && node cleanup-user.mjs --confirm --apple --google  # delete Apple + Google
 *
 * Only deletes data for the emails listed in EMAILS below.
 */

import { ConvexHttpClient } from "convex/browser";
import { api } from "./convex/_generated/api.js";

const EMAILS = [
  "kivanc.cakmak@simkab.com",
  "kivanccakmak@gmail.com",
  "kivanc.cakmak@icloud.com",
];

const CONVEX_URL =
  process.env.CONVEX_URL;

if (!CONVEX_URL) {
  console.error("ERROR: CONVEX_URL must be set explicitly — no default.");
  console.error("       Use the dev URL to clean dev data, or the prod URL");
  console.error("       (see backend/.env.local for the dev URL; prod is");
  console.error("       https://perceptive-minnow-557.eu-west-1.convex.cloud).");
  console.error("");
  console.error("Example:");
  console.error("  CONVEX_URL=https://shocking-echidna-394.eu-west-1.convex.cloud node cleanup-user.mjs");
  process.exit(2);
}

const args = process.argv.slice(2);
const dryRun = !args.includes("--confirm");
const filterApple = args.includes("--apple");
const filterGoogle = args.includes("--google");
const filterMicrosoft = args.includes("--microsoft");
const hasProviderFilter = filterApple || filterGoogle || filterMicrosoft;

const allowedProviders = new Set();
if (filterApple) allowedProviders.add("apple");
if (filterGoogle) allowedProviders.add("google");
if (filterMicrosoft) allowedProviders.add("microsoft");

async function run() {
  const client = new ConvexHttpClient(CONVEX_URL);

  console.log(`Target: ${CONVEX_URL}`);
  console.log(`Mode: ${dryRun ? "DRY RUN (pass --confirm to delete)" : "DELETING"}`);
  console.log(`Providers: ${hasProviderFilter ? [...allowedProviders].join(", ") : "all"}`);
  console.log(`Emails: ${EMAILS.join(", ")}\n`);

  for (const email of EMAILS) {
    console.log(`--- ${email} ---`);

    const users = await client.query(api.admin.getUsersByEmail, { email });

    if (!users || users.length === 0) {
      console.log("  No user found.\n");
      continue;
    }

    for (const user of users) {
      if (hasProviderFilter && !allowedProviders.has(user.provider)) {
        console.log(`  Skipping: ${user._id} (${user.fullName}, provider: ${user.provider})`);
        continue;
      }

      console.log(`  User: ${user._id} (${user.fullName}, provider: ${user.provider})`);
      console.log(`  Created: ${new Date(user.createdAt).toISOString()}`);

      if (!dryRun) {
        const result = await client.mutation(api.admin.deleteUserData, { userId: user._id });
        console.log(`  Deleted: ${result.sessionsDeleted} sessions, ${result.devicesDeleted} devices, 1 user`);
      } else {
        console.log("  (would delete user + all sessions + all devices)");
      }
    }
    console.log();
  }

  console.log(dryRun ? "Dry run complete. Pass --confirm to actually delete." : "Done.");
}

run().catch((e) => {
  console.error(e);
  process.exit(1);
});
