import test from "node:test";
import assert from "node:assert/strict";

import {
  emailPasswordAllowedEmails,
  emailPasswordAuthEnabled,
  emailPasswordEmailAllowed,
  hashPassword,
  verifyPassword,
} from "./authPasswordPolicy.js";

test("email/password auth is disabled by default and parses explicit env values", () => {
  assert.equal(emailPasswordAuthEnabled({}), false);
  assert.equal(emailPasswordAuthEnabled({ YAVER_EMAIL_PASSWORD_AUTH_ENABLED: "true" }), true);
  assert.equal(emailPasswordAuthEnabled({ YAVER_EMAIL_PASSWORD_AUTH_ENABLED: "1" }), true);
  assert.equal(emailPasswordAuthEnabled({ YAVER_EMAIL_PASSWORD_AUTH_ENABLED: "off" }), false);
  assert.equal(emailPasswordAuthEnabled({ YAVER_EMAIL_PASSWORD_AUTH_ENABLED: "unexpected" }), false);
});

test("email/password allowlist normalizes case and whitespace", () => {
  const env = {
    YAVER_EMAIL_PASSWORD_AUTH_ALLOWED_EMAILS: " Owner@Example.com, test@example.com ",
  };
  assert.deepEqual(emailPasswordAllowedEmails(env), ["owner@example.com", "test@example.com"]);
  assert.equal(emailPasswordEmailAllowed("OWNER@example.com", env, () => false), true);
  assert.equal(emailPasswordEmailAllowed(" other@example.com ", env, () => true), false);
});

test("email/password allowlist falls back to owner email only when explicit list is empty", () => {
  assert.equal(
    emailPasswordEmailAllowed("owner@example.com", {}, (email) => email === "owner@example.com"),
    true,
  );
  assert.equal(
    emailPasswordEmailAllowed("user@example.com", {}, (email) => email === "owner@example.com"),
    false,
  );
});

test("password hashes are salted, verify correctly, and do not contain raw password", async () => {
  const password = "owner-test-password-32-chars";
  const first = await hashPassword(password);
  const second = await hashPassword(password);

  assert.notEqual(first, second, "same password should get a different salt");
  assert.doesNotMatch(first, new RegExp(password), "stored hash must not include the raw password");
  assert.equal(first.split(":").length, 2, "stored hash is salt:hash");
  assert.equal(await verifyPassword(password, first), true);
  assert.equal(await verifyPassword("wrong-password", first), false);
  assert.equal(await verifyPassword(password, "not-a-valid-hash"), false);
});
