import test from "node:test";
import assert from "node:assert/strict";

import {
  githubAppEnvFromProcess,
  githubInstallationTokenRequestBody,
  normalizeGitHubPrivateKey,
  parseGitHubRepoFullName,
} from "./githubAppAuth.js";

test("normalizeGitHubPrivateKey accepts escaped PEM and base64 PEM", () => {
  const pem = "-----BEGIN PRIVATE KEY-----\nabc\n-----END PRIVATE KEY-----";
  assert.equal(normalizeGitHubPrivateKey(pem.replace(/\n/g, "\\n")), pem);
  assert.equal(normalizeGitHubPrivateKey(Buffer.from(pem).toString("base64")), pem);
});

test("githubAppEnvFromProcess accepts yaver-prefixed env and trims api base", () => {
  const env = githubAppEnvFromProcess({
    YAVER_GITHUB_APP_ID: "123",
    YAVER_GITHUB_APP_PRIVATE_KEY: "-----BEGIN PRIVATE KEY-----\\nabc\\n-----END PRIVATE KEY-----",
    YAVER_GITHUB_API_BASE_URL: "https://github.example/api/v3/",
    YAVER_GITHUB_API_VERSION: "2022-11-28",
  });
  assert.equal(env?.appId, "123");
  assert.equal(env?.apiBaseUrl, "https://github.example/api/v3");
  assert.equal(env?.privateKey.includes("\nabc\n"), true);
});

test("parseGitHubRepoFullName rejects anything outside owner/repo labels", () => {
  assert.deepEqual(parseGitHubRepoFullName("acme/app"), {
    owner: "acme",
    repo: "app",
    fullName: "acme/app",
  });
  assert.equal(parseGitHubRepoFullName("acme/app/extra"), null);
  assert.equal(parseGitHubRepoFullName("https://github.com/acme/app"), null);
  assert.equal(parseGitHubRepoFullName("token@acme/app"), null);
});

test("githubInstallationTokenRequestBody narrows repository and contents permission", () => {
  assert.deepEqual(githubInstallationTokenRequestBody({ owner: "acme", repo: "app", fullName: "acme/app" }), {
    repositories: ["app"],
    permissions: { contents: "write" },
  });
});
