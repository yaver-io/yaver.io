#!/usr/bin/env node

import { createHash } from "node:crypto";
import { readFile } from "node:fs/promises";
import path from "node:path";
import { fileURLToPath } from "node:url";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const manifestPath = path.join(root, "electron/store/submission-manifest.json");
const apiOrigin = "https://api.store.microsoft.com";
const scope = "https://api.store.microsoft.com/.default";

function fail(message) {
  process.stderr.write(`microsoft-store-submission: ${message}\n`);
  process.exit(1);
}

function requiredEnvironment(name) {
  const value = String(process.env[name] || "").trim();
  if (!value) fail(`${name} is required; keep it in the authorized secret store, never in Git or command arguments`);
  return value;
}

async function parseResponse(response, operation) {
  const correlationId = response.headers.get("x-correlation-id") || "";
  const body = await response.json().catch(() => ({}));
  if (!response.ok || body.isSuccess === false) {
    const errors = Array.isArray(body.errors)
      ? body.errors.map((entry) => `${entry.code || "error"}:${entry.target || "unknown"}:${entry.message || ""}`).join("; ")
      : `HTTP ${response.status}`;
    fail(`${operation} failed${correlationId ? ` (correlation ${correlationId})` : ""}: ${errors}`);
  }
  return { body, correlationId };
}

async function accessToken(tenantId, clientId, clientSecret) {
  const response = await fetch(`https://login.microsoftonline.com/${encodeURIComponent(tenantId)}/oauth2/v2.0/token`, {
    method: "POST",
    headers: { "content-type": "application/x-www-form-urlencoded" },
    body: new URLSearchParams({
      grant_type: "client_credentials",
      client_id: clientId,
      client_secret: clientSecret,
      scope,
    }),
  });
  const result = await response.json().catch(() => ({}));
  if (!response.ok || typeof result.access_token !== "string") {
    fail(`Microsoft Entra token request failed (HTTP ${response.status})`);
  }
  return result.access_token;
}

async function storeRequest(token, sellerId, pathname, method = "GET", jsonBody = undefined) {
  const response = await fetch(`${apiOrigin}${pathname}`, {
    method,
    headers: {
      authorization: `Bearer ${token}`,
      "x-seller-account-id": sellerId,
      accept: "application/json",
      ...(jsonBody === undefined ? {} : { "content-type": "application/json" }),
    },
    ...(jsonBody === undefined ? {} : { body: JSON.stringify(jsonBody) }),
  });
  return parseResponse(response, `${method} ${pathname}`);
}

async function verifyPublishedArtifact(manifest) {
  if (!Number.isSafeInteger(manifest.installerSizeBytes) || manifest.installerSizeBytes <= 0) {
    fail("submission manifest installerSizeBytes is not finalized");
  }
  if (!/^[a-f0-9]{64}$/.test(String(manifest.installerSha256 || ""))) {
    fail("submission manifest installerSha256 is not finalized");
  }
  const response = await fetch(manifest.installerUrl, { redirect: "error" });
  if (!response.ok || !response.body) fail(`published installer readback failed (HTTP ${response.status})`);
  const hash = createHash("sha256");
  let size = 0;
  for await (const chunk of response.body) {
    size += chunk.length;
    hash.update(chunk);
  }
  if (size !== manifest.installerSizeBytes) {
    fail(`published installer size mismatch: expected ${manifest.installerSizeBytes}, got ${size}`);
  }
  if (hash.digest("hex") !== manifest.installerSha256) {
    fail("published installer SHA-256 does not match the reviewed submission manifest");
  }
}

function packagesFrom(response) {
  return Array.isArray(response?.responseData?.packages) ? response.responseData.packages : [];
}

const command = process.argv[2] || "status";
if (!["status", "release"].includes(command)) {
  fail("usage: microsoft-store-submission.mjs [status|release] [--confirm-product=<product-id>]");
}

const manifest = JSON.parse(await readFile(manifestPath, "utf8"));
if (String(manifest.submissionType || "").toUpperCase() !== "EXE") fail("submission manifest must describe an EXE");
if (command === "release" && manifest.releaseStatus !== "approved") {
  fail(`submission manifest is ${manifest.releaseStatus || "unreviewed"}; only an approved candidate can be submitted`);
}
const productId = requiredEnvironment("YAVER_MS_STORE_PRODUCT_ID");
if (!/^[a-f0-9-]{36}$/i.test(productId)) fail("YAVER_MS_STORE_PRODUCT_ID must be the Partner Center product GUID");

const tenantId = requiredEnvironment("YAVER_MS_STORE_TENANT_ID");
const clientId = requiredEnvironment("YAVER_MS_STORE_CLIENT_ID");
const clientSecret = requiredEnvironment("YAVER_MS_STORE_CLIENT_SECRET");
const sellerId = requiredEnvironment("YAVER_MS_STORE_SELLER_ID");
const token = await accessToken(tenantId, clientId, clientSecret);
const base = `/submission/v1/product/${encodeURIComponent(productId)}`;
const [{ body: status, correlationId }, { body: packages }] = await Promise.all([
  storeRequest(token, sellerId, `${base}/status`),
  storeRequest(token, sellerId, `${base}/packages`),
]);

const summary = {
  productId,
  isReady: status?.responseData?.isReady === true,
  ongoingSubmissionId: status?.responseData?.ongoingSubmissionId || null,
  packageUrls: packagesFrom(packages).map((entry) => entry.packageUrl),
  correlationId: correlationId || null,
};

if (command === "status") {
  process.stdout.write(`${JSON.stringify(summary, null, 2)}\n`);
  process.exit(0);
}

const confirmation = process.argv.find((value) => value.startsWith("--confirm-product="))?.slice("--confirm-product=".length);
if (confirmation !== productId) fail(`release requires --confirm-product=${productId}`);
if (process.env.YAVER_MS_STORE_ALLOW_SUBMIT !== "YES") fail("set YAVER_MS_STORE_ALLOW_SUBMIT=YES for this release operation");
if (summary.ongoingSubmissionId) fail(`Partner Center already has an active submission: ${summary.ongoingSubmissionId}`);

await verifyPublishedArtifact(manifest);
const errorDetails = Object.entries(manifest.installerReturnCodes || {}).map(([errorScenario, errorValue]) => ({
  errorScenario,
  errorScenarioDetails: [{
    errorValue: String(errorValue),
    errorUrl: manifest.installerReturnCodeDocumentationUrl,
  }],
}));

await storeRequest(token, sellerId, `${base}/packages`, "PUT", {
  packages: [{
    packageUrl: manifest.installerUrl,
    languages: manifest.languages,
    architectures: manifest.architectures,
    // Microsoft defines this as "needs no switch". Yaver needs /S, so false.
    isSilentInstall: false,
    installerParameters: manifest.installerParameters,
    genericDocUrl: manifest.installerReturnCodeDocumentationUrl,
    errorDetails,
    packageType: "exe",
  }],
});
await storeRequest(token, sellerId, `${base}/packages/commit`, "POST");

let ready = false;
for (let attempt = 0; attempt < 40; attempt += 1) {
  const { body: polled } = await storeRequest(token, sellerId, `${base}/status`);
  if (polled?.responseData?.ongoingSubmissionId) {
    fail(`Partner Center reported active submission ${polled.responseData.ongoingSubmissionId} while committing the package`);
  }
  if (polled?.responseData?.isReady === true) {
    ready = true;
    break;
  }
  await new Promise((resolve) => setTimeout(resolve, 15_000));
}
if (!ready) fail("Partner Center package commit did not become ready within 10 minutes");

const { body: committedPackages } = await storeRequest(token, sellerId, `${base}/packages`);
const matches = packagesFrom(committedPackages).filter((entry) => entry.packageUrl === manifest.installerUrl);
if (matches.length !== 1) fail("Partner Center draft does not contain exactly the reviewed Store installer URL");
const candidate = matches[0];
if (String(candidate.packageType || "").toLowerCase() !== "exe") fail("Partner Center draft package type is not EXE");
if (!Array.isArray(candidate.architectures) || !candidate.architectures.some((value) => String(value).toUpperCase() === "X64")) {
  fail("Partner Center draft package architecture is not X64");
}
if (candidate.installerParameters !== manifest.installerParameters) fail("Partner Center draft installer parameters changed");

const { body: submission, correlationId: submitCorrelationId } = await storeRequest(token, sellerId, `${base}/submit`, "POST");
process.stdout.write(`${JSON.stringify({
  productId,
  submissionId: submission?.responseData?.submissionId || null,
  pollingUrl: submission?.responseData?.pollingUrl || null,
  correlationId: submitCorrelationId || null,
}, null, 2)}\n`);
