#!/usr/bin/env node
// Regression guard for the "public Convex function trusts a caller-supplied
// identity" class (the 2026-07-13 account-takeover audit). Flags every
// `export const X = mutation|query|action(...)` whose ARG validator includes a
// raw identity field (userId / targetDocId / callerDocId / providerId /
// passwordHash / hardwareId / machineId) BUT whose handler never derives the
// caller from a session (validateSessionInternal / args.tokenHash / ctx.auth).
//
// Such a function is directly callable at the public deployment URL with an
// attacker-chosen identity, bypassing the http.ts auth layer. The fix is
// always: make it internal*, or take tokenHash + validateSessionInternal.
//
// Run: node backend/scripts/check-convex-public-auth.mjs  (exit 1 on findings)
// Known-safe exceptions (fail-closed via resolveUser, or intentionally public)
// live in ALLOWLIST below — add with a one-line justification.

import { readdirSync, readFileSync } from "node:fs";
import { join, dirname } from "node:path";
import { fileURLToPath } from "node:url";

const CONVEX_DIR = join(dirname(fileURLToPath(import.meta.url)), "..", "convex");
const IDENTITY_ARGS = /\b(userId|targetDocId|callerDocId|providerId|passwordHash|hardwareId|machineId)\s*:/;
const HAS_SESSION_CHECK = /validateSessionInternal|args\.tokenHash|ctx\.auth\.getUserIdentity|webUser\(|sessionUser\(|resolveUser\(/;
const EXPORT_RE = /export const (\w+)\s*=\s*(mutation|query|action)\(/g;

// module:function → reason it is safe despite matching the heuristic.
const ALLOWLIST = new Set([
  // Proof-of-possession: rotates by presenting the CURRENT valid token hash
  // (looked up + expiry/replaced checks). No session needed — holding the
  // token IS the proof.
  "auth:rotateSdkToken",
  // Anonymous auth surface BY DESIGN — you have no session while logging in /
  // signing up. loginFinish/signupFinish resolve by WebAuthn assertion (login)
  // or a brand-new email (signup); neither takes a victim userId as input (the
  // heuristic matched a `userId:` field in their RETURN type). Cannot take over
  // an existing account: an attacker can't forge a passkey assertion, and
  // signup errors if the email already exists.
  "passkeys:loginStart",
  "passkeys:loginFinish",
  "passkeys:signupStart",
  "passkeys:signupFinish",
]);

function fnBody(src, startIdx) {
  // Scan only THIS function's own text — from the match to the start of the
  // next `export const` (or 6000 chars, whichever is first). A fixed window
  // bled into the next declaration and false-flagged anonymous auth flows
  // (deviceCode poll, passkeys login/signup) whose neighbor takes a userId.
  const rest = src.slice(startIdx + 1);
  const nextExport = rest.indexOf("\nexport const ");
  const end = nextExport === -1 ? Math.min(rest.length, 6000) : nextExport;
  return src.slice(startIdx, startIdx + 1 + end);
}

const findings = [];
for (const file of readdirSync(CONVEX_DIR).filter((f) => f.endsWith(".ts"))) {
  const src = readFileSync(join(CONVEX_DIR, file), "utf8");
  let m;
  while ((m = EXPORT_RE.exec(src)) !== null) {
    const [, name, kind] = m;
    const body = fnBody(src, m.index);
    if (IDENTITY_ARGS.test(body) && !HAS_SESSION_CHECK.test(body)) {
      const key = `${file.replace(/\.ts$/, "")}:${name}`;
      if (!ALLOWLIST.has(key)) findings.push(`${key} (${kind}) — takes a raw identity arg with no session check`);
    }
  }
}

if (findings.length) {
  console.error(`✖ ${findings.length} public Convex function(s) trust a caller-supplied identity:\n`);
  for (const f of findings) console.error("  - " + f);
  console.error("\nFix: make it internal*, or take tokenHash + validateSessionInternal. See SECURITY_AUDIT_FINDINGS.md.");
  process.exit(1);
}
console.log("✓ no public Convex function trusts a caller-supplied identity");
