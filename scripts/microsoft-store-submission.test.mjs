import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";

const source = readFileSync(new URL("./microsoft-store-submission.mjs", import.meta.url), "utf8");
const manifest = JSON.parse(readFileSync(new URL("../electron/store/submission-manifest.json", import.meta.url), "utf8"));

test("Partner Center automation is read-only by default and release is explicitly confirmed", () => {
  assert.match(source, /process\.argv\[2\] \|\| "status"/);
  assert.match(source, /--confirm-product=/);
  assert.match(source, /YAVER_MS_STORE_ALLOW_SUBMIT/);
  assert.match(source, /ongoingSubmissionId/);
  assert.doesNotMatch(source, /method[^\n]*"DELETE"|, "DELETE"/);
});

test("release verifies immutable public bytes before replacing the draft package", () => {
  assert.match(source, /createHash\("sha256"\)/);
  assert.match(source, /redirect: "error"/);
  assert.match(source, /installerSizeBytes/);
  assert.match(source, /packages\/commit/);
  assert.match(source, /entry\.packageUrl === manifest\.installerUrl/);
  assert.match(source, /isSilentInstall: false/);
  assert.match(source, /manifest\.releaseStatus !== "approved"/);
  // The reviewed EXE is intentionally retired now that the Store-managed
  // AppX lane exists. Any non-approved state remains fail-closed in the
  // submission script; `superseded` records why this immutable candidate may
  // never be released.
  assert.equal(manifest.releaseStatus, "superseded");
  assert.equal(manifest.installerSizeBytes, 130029648);
  assert.equal(manifest.installerSha256, "479c95cbd7f68745892ff0d5f4b59c30403de22826d241d47dcd7648ea685ee0");
});

test("Partner Center credentials are environment-only", () => {
  for (const name of [
    "YAVER_MS_STORE_PRODUCT_ID",
    "YAVER_MS_STORE_TENANT_ID",
    "YAVER_MS_STORE_CLIENT_ID",
    "YAVER_MS_STORE_CLIENT_SECRET",
    "YAVER_MS_STORE_SELLER_ID",
  ]) {
    assert.match(source, new RegExp(`requiredEnvironment\\("${name}"\\)`));
  }
  assert.doesNotMatch(source, /console\.log\([^\n]*(clientSecret|access_token)/);
});
