// Run: node --experimental-strip-types --test convex/accessSigPolicy.test.mts
// (from backend/). Wired into scripts/test-suite.sh as the "convex unit" step.

import test from "node:test";
import assert from "node:assert/strict";

import { resolveSigReach } from "./accessSigPolicy.ts";

// The relay's signature path decides ONE thing: may the relay carry bytes from
// this signer to this target. Until 2026-07-23 it answered "same owner only",
// which silently pushed every cross-account session (guest, host-share,
// project-share, support link) onto the legacy shared-password path. That was
// invisible in /authmix, and the planned password cutover would have killed all
// of them at once. These tests pin the rule so it cannot drift back.

test("same owner is always allowed", () => {
  assert.equal(
    resolveSigReach({ signerUserId: "u1", targetUserId: "u1", grantCoversTarget: false }),
    "same-owner",
  );
});

test("same owner does not depend on a grant", () => {
  assert.equal(
    resolveSigReach({ signerUserId: "u1", targetUserId: "u1", grantCoversTarget: true }),
    "same-owner",
  );
});

test("cross-account is allowed when an active grant covers the target device", () => {
  assert.equal(
    resolveSigReach({ signerUserId: "guest", targetUserId: "host", grantCoversTarget: true }),
    "access-graph",
  );
});

test("cross-account with no grant is denied", () => {
  assert.equal(
    resolveSigReach({ signerUserId: "guest", targetUserId: "host", grantCoversTarget: false }),
    "deny",
  );
});

test("a stranger cannot reach a device just by signing — this is the multi-tenancy boundary", () => {
  // The relay is shared by unrelated users and the code is public. A signature
  // proves WHO you are, never WHAT you may reach; the grant is the only thing
  // that opens a cross-account path.
  assert.equal(
    resolveSigReach({ signerUserId: "attacker", targetUserId: "victim", grantCoversTarget: false }),
    "deny",
  );
});

test("empty ids never collapse into a match", () => {
  // Two unknown/missing owners must not read as "same owner". This is the
  // failure mode that would turn a Convex hiccup into an open relay.
  assert.equal(
    resolveSigReach({ signerUserId: "", targetUserId: "", grantCoversTarget: false }),
    "deny",
  );
  assert.equal(
    resolveSigReach({ signerUserId: "", targetUserId: "host", grantCoversTarget: true }),
    "deny",
  );
  assert.equal(
    resolveSigReach({ signerUserId: "guest", targetUserId: "", grantCoversTarget: true }),
    "deny",
  );
});

test("whitespace-only ids are treated as empty", () => {
  assert.equal(
    resolveSigReach({ signerUserId: "  ", targetUserId: "  ", grantCoversTarget: false }),
    "deny",
  );
});
