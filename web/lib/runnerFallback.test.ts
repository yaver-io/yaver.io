/**
 * Guards for runnerFallback.
 *
 * The fixtures are the owner's REAL fleet, read from the live device rows on
 * 2026-08-02 — not invented shapes. That matters: the whole design turns on
 * `authSource` telling api-key runners apart from subscription-OAuth ones, and
 * inventing those labels would prove nothing about the product.
 *
 * Run: npx tsx web/lib/runnerFallback.test.ts
 */
import { fixButtonLabel, planRunnerFix, runnerAuthMechanism } from "./runnerFallback";

let failures = 0;
function eq(got: unknown, want: unknown, label: string) {
  if (got === want) console.log(`ok   ${label}`);
  else { console.error(`FAIL ${label}: got ${JSON.stringify(got)}, want ${JSON.stringify(want)}`); failures++; }
}
const ok = (c: unknown, label: string) => eq(Boolean(c), true, label);

// ── the real fleet ────────────────────────────────────────────────────────
const MAGARA = [
  { id: "claude", installed: true, ready: true, authConfigured: true, authPresent: true, authVerified: false, authSource: "claude.ai · max" },
  { id: "codex", installed: true, ready: false, authConfigured: false, authPresent: false, authVerified: false, error: "Codex is installed but no credentials were found." },
  { id: "opencode", installed: true, ready: true, authConfigured: true, authPresent: false, authVerified: false, authSource: "GLM API key" },
];
const UBUNTU = [
  { id: "claude", installed: true, ready: true, authConfigured: true, authPresent: true, authVerified: false, authSource: "claude.ai · max" },
  { id: "codex", installed: true, ready: true, authConfigured: true, authPresent: true, authVerified: false, authSource: "codex login status" },
  { id: "opencode", installed: true, ready: true, authConfigured: true, authPresent: false, authVerified: false, authSource: "GLM API key" },
];

// ── mechanism inference ───────────────────────────────────────────────────
eq(runnerAuthMechanism({ id: "opencode", authSource: "GLM API key" }), "api-key",
  "a GLM API key is an api-key mechanism — no OAuth to expire");
eq(runnerAuthMechanism({ id: "claude", authSource: "claude.ai · max" }), "subscription-oauth",
  "claude.ai · max is a subscription OAuth grant");
eq(runnerAuthMechanism({ id: "codex", authSource: "codex login status" }), "subscription-oauth",
  "codex login status is a subscription OAuth grant");
eq(runnerAuthMechanism({ id: "x", authSource: "" }), "unknown", "no label = unknown");
eq(runnerAuthMechanism({ id: "x", authSource: "something new" }), "unknown",
  "NO FALSE GREEN: an unrecognised label is never guessed into a bucket — recommending a mis-labelled OAuth runner is worst exactly when OAuth is what broke");

// ── THE CASE THAT STARTED THIS ────────────────────────────────────────────
// Codex's OAuth died on ubuntu. Offering Codex again dispatched the same
// failure; the button re-appeared; nothing progressed.
const codexAuthDied = planRunnerFix("codex", "auth", UBUNTU);
ok(codexAuthDied.candidate, "an OAuth failure still yields a fix candidate");
eq(codexAuthDied.candidate?.runner, "opencode",
  "it picks the API-key runner, which CANNOT have the sign-in failure that just happened");
ok(codexAuthDied.candidate?.immune, "…and marks it structurally immune, not merely different");
ok(/api key/i.test(codexAuthDied.candidate?.why || ""), "the reason names the mechanism, so the user can check it");
eq(fixButtonLabel(codexAuthDied), "Fix with OpenCode", "the button says which runner");

// It must never re-offer the runner whose own credential failed.
ok(codexAuthDied.candidate?.runner !== "codex", "never re-offers the runner that just failed on auth");

// The entitlement 400 is account-bound too: another runner is a real escape.
const entitlement = planRunnerFix("codex", "model-not-supported", UBUNTU);
eq(entitlement.candidate?.runner, "opencode", "a model-entitlement refusal routes to a different account's runner");
ok(entitlement.candidate?.runner !== "codex", "…and not back to the one whose plan lacks the model");

// Billing is account-bound as well — the credential is fine, the account is not.
eq(planRunnerFix("claude", "billing", UBUNTU).candidate?.runner, "opencode",
  "an out-of-credit account routes to a runner on a different account");

// ── per-box readiness ─────────────────────────────────────────────────────
// On magara codex has NO credentials, so it must never be proposed there.
const onMagara = planRunnerFix("claude", "auth", MAGARA);
eq(onMagara.candidate?.runner, "opencode", "on magara the only ready alternative is opencode");
ok(onMagara.candidate?.runner !== "codex",
  "codex has no credentials on magara — a globally-chosen fallback would have sent the fix to a runner that cannot start");

// ── NO FALSE REDS ─────────────────────────────────────────────────────────
// A plain build error is NOT account-bound: retrying with the same runner is
// correct, and forcing a switch would be worse than the bug.
const buildError = planRunnerFix("codex", "subprocess", UBUNTU);
ok(buildError.candidate, "a build failure still offers a fix");
ok(!buildError.runnerAgnostic, "…and is not treated as a machine problem");

// Machine problems must not offer a runner at all — an LLM cannot fix an
// offline box or a missing project.
for (const kind of ["project-missing", "relay-presence", "relay-auth", "agent-verb-skew"]) {
  const p = planRunnerFix("codex", kind, UBUNTU);
  eq(p.candidate, null, `${kind} offers NO runner`);
  ok(p.runnerAgnostic, `${kind} is marked runner-agnostic`);
  ok(!!p.blocked, `${kind} says why instead of going silent`);
}

// ── nothing available ─────────────────────────────────────────────────────
const soloOauth = [
  { id: "codex", installed: true, ready: true, authConfigured: true, authPresent: true, authVerified: false, authSource: "codex login status" },
];
const stuck = planRunnerFix("codex", "auth", soloOauth);
eq(stuck.candidate, null, "with only the broken runner present there is no candidate");
ok(/encrypted terminal/i.test(stuck.blocked || ""),
  "…and it names the native PTY route out rather than remote OAuth or a spinner");
ok(!/fix with/i.test(stuck.blocked || ""), "it must not dangle a Fix button it cannot honour");

// A proven credential outranks a merely-present one when OAuth is not at issue.
const proven = planRunnerFix("codex", "subprocess", [
  { id: "codex", installed: true, ready: true, authConfigured: true, authPresent: true },
  { id: "claude", installed: true, ready: true, authConfigured: true, authVerified: true, authSource: "claude.ai · max" },
]);
eq(proven.candidate?.runner, "claude", "an EXERCISED credential outranks one that has only been seen");

// Unusable rows are excluded however they are expressed.
eq(planRunnerFix("codex", "auth", [
  { id: "codex", installed: true, ready: true, authConfigured: true, authSource: "codex login status" },
  { id: "claude", installed: false },
  { id: "opencode", installed: true, ready: false },
]).candidate, null, "not-installed and not-ready runners are never proposed");

if (failures) { console.error(`\nrunnerFallback: ${failures} FAILED`); process.exitCode = 1; }
else console.log("\nrunnerFallback: ALL PASS");
