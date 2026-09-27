#!/usr/bin/env node

import { createHash } from "node:crypto";
import { readFile, stat } from "node:fs/promises";
import path from "node:path";

const API = "https://manage.devcenter.microsoft.com/v1.0/my";
const fail = (message) => { process.stderr.write(`microsoft-store-appx-submission: ${message}\n`); process.exit(1); };
const required = (name) => {
  const value = String(process.env[name] || "").trim();
  if (!value) fail(`${name} is required in the protected GitHub environment`);
  return value;
};
const argument = (name) => process.argv.find((value) => value.startsWith(`--${name}=`))?.slice(name.length + 3) || "";

async function accessToken() {
  const response = await fetch(`https://login.microsoftonline.com/${encodeURIComponent(required("YAVER_MS_STORE_TENANT_ID"))}/oauth2/token`, {
    method: "POST",
    headers: { "content-type": "application/x-www-form-urlencoded" },
    body: new URLSearchParams({
      grant_type: "client_credentials",
      client_id: required("YAVER_MS_STORE_CLIENT_ID"),
      client_secret: required("YAVER_MS_STORE_CLIENT_SECRET"),
      resource: "https://manage.devcenter.microsoft.com",
    }),
  });
  const body = await response.json().catch(() => ({}));
  if (!response.ok || !body.access_token) fail(`Microsoft Entra token request failed (HTTP ${response.status})`);
  return body.access_token;
}

async function request(token, pathname, method = "GET", body) {
  const response = await fetch(`${API}${pathname}`, {
    method,
    headers: {
      authorization: `Bearer ${token}`,
      accept: "application/json",
      ...(body === undefined ? {} : { "content-type": "application/json" }),
    },
    ...(body === undefined ? {} : { body: JSON.stringify(body) }),
  });
  const text = await response.text();
  const json = text ? JSON.parse(text) : {};
  if (!response.ok) fail(`${method} ${pathname} failed (HTTP ${response.status})`);
  return json;
}

const command = process.argv[2] || "status";
if (!new Set(["status", "release"]).has(command)) fail("usage: microsoft-store-appx-submission.mjs [status|release]");
const applicationId = required("YAVER_MS_STORE_APPLICATION_ID");
if (!/^[A-Za-z0-9]+$/.test(applicationId)) fail("YAVER_MS_STORE_APPLICATION_ID is invalid");
const token = await accessToken();
const applicationPath = `/applications/${encodeURIComponent(applicationId)}`;
const application = await request(token, applicationPath);

if (command === "status") {
  process.stdout.write(`${JSON.stringify({
    applicationId,
    pendingSubmissionId: application.pendingApplicationSubmission?.id || null,
    lastSubmissionId: application.lastPublishedApplicationSubmission?.id || null,
  }, null, 2)}\n`);
  process.exit(0);
}

if (process.env.YAVER_MS_STORE_ALLOW_SUBMIT !== "YES") fail("submission requires YAVER_MS_STORE_ALLOW_SUBMIT=YES");
if (argument("confirm-application") !== applicationId) fail(`submission requires --confirm-application=${applicationId}`);
if (application.pendingApplicationSubmission?.id) fail(`Partner Center already has pending submission ${application.pendingApplicationSubmission.id}`);

const packagePath = path.resolve(argument("package"));
const archivePath = path.resolve(argument("archive"));
if (!/[.](appx|msix|appxbundle|msixbundle)$/i.test(packagePath)) fail("--package must be an AppX/MSIX package");
if (!/[.]zip$/i.test(archivePath)) fail("--archive must be a ZIP containing the package at its root");
const [packageInfo, archiveInfo] = await Promise.all([stat(packagePath), stat(archivePath)]).catch(() => fail("package or archive is missing"));
if (!packageInfo.isFile() || !archiveInfo.isFile() || packageInfo.size === 0 || archiveInfo.size === 0) fail("package and archive must be non-empty files");

const fileName = path.basename(packagePath);
const submission = await request(token, `${applicationPath}/submissions`, "POST");
if (!submission.id || !submission.fileUploadUrl) fail("Partner Center did not return a submission ID and upload URL");
const submissionPath = `${applicationPath}/submissions/${encodeURIComponent(submission.id)}`;
submission.applicationPackages = [
  ...(Array.isArray(submission.applicationPackages) ? submission.applicationPackages.map((entry) => ({
    fileName: entry.fileName,
    fileStatus: "PendingDelete",
    minimumDirectXVersion: entry.minimumDirectXVersion || "None",
    minimumSystemRam: entry.minimumSystemRam || "None",
  })) : []),
  { fileName, fileStatus: "PendingUpload", minimumDirectXVersion: "None", minimumSystemRam: "None" },
];
submission.notesForCertification = String(process.env.YAVER_MS_STORE_CERTIFICATION_NOTES || submission.notesForCertification || "").trim();
await request(token, submissionPath, "PUT", submission);

const upload = await fetch(submission.fileUploadUrl, {
  method: "PUT",
  headers: { "x-ms-blob-type": "BlockBlob", "content-type": "application/zip" },
  body: await readFile(archivePath),
});
if (!upload.ok) fail(`submission ZIP upload failed (HTTP ${upload.status}); submission ${submission.id} was not committed`);
await request(token, `${submissionPath}/commit`, "POST");

let statusName = "CommitStarted";
let statusDetails = {};
for (let attempt = 0; attempt < 40 && statusName === "CommitStarted"; attempt += 1) {
  const status = await request(token, `${submissionPath}/status`);
  statusName = String(status.status || "Unknown");
  statusDetails = status.statusDetails || {};
  if (statusName === "CommitStarted") await new Promise((resolve) => setTimeout(resolve, 15_000));
}
const acceptedStatuses = new Set(["PreProcessing", "Certification", "PendingPublication", "Publishing", "Published"]);
if (!acceptedStatuses.has(statusName)) {
  const detail = JSON.stringify(statusDetails).slice(0, 2000);
  fail(`submission ${submission.id} ended in ${statusName}${detail && detail !== "{}" ? `: ${detail}` : ""}`);
}
const sha256 = createHash("sha256").update(await readFile(packagePath)).digest("hex");
process.stdout.write(`${JSON.stringify({ applicationId, submissionId: submission.id, status: statusName, fileName, sizeBytes: packageInfo.size, sha256 }, null, 2)}\n`);
