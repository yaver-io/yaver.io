# Yaver Test Coverage Handoff

Status: 2026-06-12. This handoff reflects the code after the signup E2E work in
`4cd6087f`, plus a follow-up pass adding TOTP, passkey, account-security, and
Selenium sign-in browser coverage and redroid QA ephemeral credential
injection. As always in this repo, code wins over docs.

## What Is Covered Now

### Web Browser E2E

Location: `e2e/`

The Playwright suite now runs against the same Convex deployment the local web
app uses by default:

- `e2e/playwright.config.ts` starts the web app on `127.0.0.1:3217` by default,
  avoiding accidental reuse of unrelated apps on port 3000.
- `E2E_CONVEX_URL` overrides the backend. If unset, tests use
  `NEXT_PUBLIC_CONVEX_SITE_URL`, then the current web default
  `https://perceptive-minnow-557.eu-west-1.convex.site`.
- Global setup creates a randomized `e2e-*@yaver.test` user.
- Tests can register additional throwaway users via `rememberTestUser`.
- Global teardown deletes every tracked throwaway user.

Current web coverage:

- Landing page render and public navigation.
- `/auth` sign-in success and wrong-password failure.
- Full email signup UI:
  - creates a new account through the browser UI;
  - persists `yaver_auth_token`;
  - validates the bearer via `/auth/validate`;
  - redirects to `/survey` or `/dashboard`;
  - blocks mismatched passwords before the backend call;
  - blocks short passwords before the backend call;
  - surfaces duplicate-email signup errors;
  - supports signup, local logout, then password sign-in with the same account.
- Bootstrap auto-pair Convex contract:
  - previous registered machine can enter bootstrap with matching
    `hardwareId` + `publicKey`;
  - `/devices/list` shows `needsAuth=true`;
  - wrong hardware ID, wrong public key, missing fields, and unknown device are
    rejected.
- Vibe Preview dashboard tab smoke:
  - mocked device connection;
  - opens Vibe Preview;
  - fills current `Project name` field;
  - starts a preview;
  - verifies stop/record/live-waiting UI.
- Two-factor (TOTP) login (`totp-2fa.spec.ts`):
  - enrolls TOTP via `/auth/totp/setup` + `/auth/totp/enable`;
  - derives codes in-test with a dependency-free RFC 6238 generator
    (`e2e/lib/totp.ts`) that mirrors the backend (SHA-1 / 6 digits / 30s);
  - password login now routes to the `/auth/totp` verify step;
  - a valid code completes login and mints a session;
  - a wrong code is rejected and no session is minted;
  - a recovery code also completes the step;
  - 2FA can be disabled and login no longer requires a code.
- Passkey / WebAuthn (`passkey.spec.ts`):
  - CDP virtual authenticator with a resident key;
  - passkey signup mints a session, then passkey login signs back in;
  - self-skips when the origin is not in the backend allowlist (see below).
- Account security (`account-security.spec.ts`):
  - change-password updates the password used to sign in (old fails, new works);
  - change-password rejects a wrong current password;
  - logout invalidates the session token (`/auth/validate` → 401);
  - logout-all invalidates every session for the account.

Verification run:

```bash
cd e2e
npx tsc --noEmit
CI=1 npx playwright test --project=chromium --workers=1 --retries=0
```

The passkey spec self-skips on the default `127.0.0.1:3217` server because the
backend WebAuthn allowlist only accepts `yaver.io`, `localhost:3000`, and
`localhost:3001` (plus `WEBAUTHN_EXTRA_ORIGINS`). To run it, serve the web app
on an allowlisted origin:

```bash
npm --prefix web run dev -- --port 3001 --hostname 127.0.0.1
E2E_BASE_URL=http://localhost:3001 \
  npx playwright test passkey.spec.ts --project=chromium
```

Last local result: TOTP + account-security + the pre-existing specs pass; the
TOTP enroll test can need one retry on the very first cold compile of the
`/auth/totp` route (CI retries absorb it).

Skipped:

- `managed-cloud-delete.spec.ts`: requires owner `YAVER_E2E_TOKEN` and a real
  managed cloud machine.
- `dashboard-autodev.spec.ts`: skipped because it targeted the removed
  Console -> Autodev dashboard workbench. The mock remains as old endpoint
  contract reference, but there is no current UI surface to drive.

### Selenium / WebDriver

Location:
- `e2e/selenium/signup-onboarding.selenium.py` — UI signup smoke.
- `e2e/selenium/signin.selenium.py` — UI sign-in smoke (companion).

Signup smoke coverage:

- Opens `/auth` in Chrome through Selenium Manager.
- Switches to email signup.
- Creates a randomized `e2e-selenium-*@yaver.test` account.
- Verifies redirect to `/survey` or `/dashboard`.
- Reads `yaver_auth_token` from localStorage.
- Calls `/auth/validate`.
- Deletes the throwaway account.

Sign-in smoke coverage:

- API-creates a throwaway account (no UI), then drives the *sign-in* form.
- Verifies redirect, the persisted `yaver_auth_token`, and `/auth/validate`.
- Deletes the throwaway account.

Together they cover both halves of the email auth flow under WebDriver.

Run:

```bash
NEXT_PUBLIC_CONVEX_SITE_URL=https://perceptive-minnow-557.eu-west-1.convex.site \
  npm --prefix web run dev -- --port 3217 --hostname 127.0.0.1

E2E_BASE_URL=http://127.0.0.1:3217 \
E2E_CONVEX_URL=https://perceptive-minnow-557.eu-west-1.convex.site \
  python3 e2e/selenium/signup-onboarding.selenium.py
```

Last local result: passed with Selenium `4.41.0`.

This is intentionally a smoke, not the deep browser suite. Playwright remains
the primary web E2E layer because it gives trace/video/screenshots, routing,
and faster CI ergonomics.

### Yaver Testkit Deterministic Specs

Location: `yaver-tests/specs/` and `yaver-tests/*.test.yaml`

Current coverage:

- `yaver-tests/landing.test.yaml`: deterministic web landing smoke.
- `yaver-tests/mobile-android-smoke.test.yaml`: Android emulator smoke that
  launches a package/APK using env-driven AVD/APK/package settings.
- `yaver-tests/mobile-ios-smoke.test.yaml`: iOS simulator smoke.
- `yaver-tests/specs/01-launch.test.yaml`: deterministic app launch spec.

The Go testkit supports:

- `web` through Chrome/CDP;
- `ios-sim`;
- `android-emu`;
- `android-redroid`;
- `device`;
- screenshots, network/console capture, HAR, a11y, snapshots, screencast
  frames, and flake retries.

### Mobile / Redroid Agentic QA

Location: `yaver-tests/flows/` plus `desktop/agent/ops_qa.go`

Current flow coverage:

- `01-launch-and-home.flow.yaml`: launch Yaver app and reach home/main UI, or
  stop if sign-in appears.
- `02-settings-navigation.flow.yaml`: Android settings navigation sanity flow.
- `03-signup-onboarding.flow.yaml`: new native signup/onboarding scaffold.

Current redroid infrastructure:

- `desktop/agent/testkit/driver_androidredroid.go` implements
  `android-redroid` as a first-class testkit target over the Studio redroid
  surface.
- `desktop/agent/ops_qa.go` exposes:
  - `qa_base_build`;
  - `qa_base_up`;
  - `qa_base_list`;
  - `qa_run`;
  - `qa_report`;
  - `qa_base_gc`.
- Warm base images can avoid cold boot and repeated app install.
- Agentic flows are catch-only by default; they report bugs, not auto-commit
  fixes.

## What Is Not Covered Yet

### Auth / Signup Gaps

Now covered (see Web Browser E2E above):

- Passkey signup/login with a virtual WebAuthn authenticator.
- TOTP setup and TOTP-required login in browser E2E (+ recovery code + disable).
- Change-password (success + wrong current password).
- Logout / logout-all session invalidation.

Not covered yet:

- OAuth provider signup/login end-to-end for Apple, GitHub, GitLab, Google,
  Microsoft.
- Email verification flow.
- Forgot-password/reset-password flow.
- Account merge/linking flows.
- OAuth duplicate-email linking.
- Rate-limit and abuse protection behavior.

Can be covered:

- Password reset can be covered if the test backend exposes a safe test email
  token capture endpoint or local mail sink. (The reset token is emailed, not
  returned, so it needs a mail sink or a test-only capture endpoint.)
- OAuth can be covered with provider sandboxes or a local OIDC test provider.
  There is already a `/auth/test/oauth-signin` hook gated on `TEST_MODE_ENABLED`
  — enabling it on a test deploy unlocks unattended OAuth E2E.

Hard/not worth covering in normal PR CI:

- Real Apple/Google/GitHub OAuth against live consumer accounts. That belongs
  in manual release checks or a dedicated sandbox workflow with secrets.
- Real payment/managed-cloud delete without owner credentials and disposable
  infra.

### Web Dashboard Gaps

Not fully covered:

- Device details workflows beyond bootstrap contract.
- Webview tab through a live agent in local Playwright.
- Shell/PTY modal with real agent.
- Billing/managed cloud purchase flow.
- Guest invitation lifecycle from browser UI.
- Vault/settings mutation flows.
- Mesh/network UI flows.
- Project and Git workflows.

Existing external coverage:

- `selenium-webapp-sfmg.yml` drives the live dashboard/Webview/Web App path
  against the ephemeral box with Selenium and an injected token.
- Protocol and relay smokes in `.github/workflows/*` cover live agent paths
  outside local Playwright.

### Mobile Native Gaps

Not covered unattended yet:

- Native signup with real throwaway credentials on redroid.
- Native logout/re-login.
- Native OAuth/passkey flows.
- Native permission prompts.
- Native push/Hermes app-insert signup edge cases.
- Cross-device pairing after signup from the mobile app UI.

Credential injection — DONE (`qa_run { testAccount: "ephemeral" }`):

- `desktop/agent/qa_testaccount.go` mints a randomized
  `e2e-redroid-*@yaver.test` account against Convex, substitutes
  `{{email}}` / `{{password}}` / `{{fullName}}` placeholders into every flow's
  goal + expectations in-memory, and deletes the account when the run finishes.
- `desktop/agent/qa_jobs.go` (`qaRunRequest.TestAccount` / `.ConvexURL`) wires
  this into `startQARun`: the account is created synchronously (so a bad config
  fails fast) and torn down in the job goroutine's defer. Unit-tested in
  `qa_testaccount_test.go` (templating, placeholder detection, URL resolution).
- `yaver-tests/flows/03-signup-onboarding.flow.yaml` now uses the placeholders
  and is unattended when run with `testAccount: "ephemeral"`. Without it, the
  placeholders stay literal and the brain is told to stop and report rather than
  type real credentials. A `flowsReferenceTestAccount` guard logs a warning if a
  flow uses placeholders but `testAccount` wasn't set.

Still needs a device/redroid + model lane to exercise end to end (the Go side
builds and unit-tests pass; the live drive is hardware-gated).

Still not covered unattended:

- Native logout/re-login, OAuth/passkey, permission prompts, Hermes app-insert
  signup edge cases, cross-device pairing from the mobile UI.

### Selenium Gaps

Current Selenium is intentionally narrow.

Not covered:

- Full browser matrix.
- Dashboard workflows.
- Mobile browser emulation.
- Network throttling.
- Trace/video artifacts comparable to Playwright.

Can be covered:

- Add Selenium wrappers for the same critical signup/sign-in flows if an
  external WebDriver grid requires them.
- Keep Selenium as compatibility smoke; do not duplicate the whole Playwright
  suite unless there is a concrete external runner requirement.

## Recommended Next Work

Done in this pass:

- ~~Add Playwright virtual-WebAuthn tests for passkey signup/login.~~ →
  `passkey.spec.ts` (runs on an allowlisted origin).
- ~~Add TOTP setup and TOTP login tests.~~ → `totp-2fa.spec.ts` + `lib/totp.ts`.
- ~~Add QA credential injection for redroid flows.~~ → `qa_testaccount.go`,
  `qaRunRequest.TestAccount`.
- ~~Convert `03-signup-onboarding.flow.yaml` to unattended.~~ → uses
  `{{email}}`/`{{password}}`/`{{fullName}}` placeholders.
- Also added: change-password + logout/logout-all E2E, Selenium sign-in smoke.

Still open:

1. Add password reset E2E with a local/test mail capture path (token is emailed,
   not returned).
2. Drive the unattended redroid signup flow on real hardware + a model lane
   (Go side is built and unit-tested; the live run is hardware-gated).
3. Wire `/auth/test/oauth-signin` (gated on `TEST_MODE_ENABLED`) into an OAuth
   E2E on a test deploy.
4. Replace the skipped Autodev dashboard spec with a current UI target, or
   delete it if Autodev is no longer product surface.
5. Add a nightly browser matrix for Chromium/Firefox/WebKit and mobile
   viewport. Keep PR CI on Chromium for speed.

## Commands To Reproduce

Deep web suite:

```bash
cd e2e
npm install
npx playwright install --with-deps chromium
CI=1 npx playwright test --project=chromium --workers=1 --retries=0
```

Signup-only Playwright:

```bash
cd e2e
CI=1 npx playwright test tests/signup-onboarding.spec.ts --project=chromium --workers=1 --retries=0
```

TOTP / account-security Playwright:

```bash
cd e2e
CI=1 npx playwright test totp-2fa.spec.ts account-security.spec.ts --project=chromium --workers=1
```

Passkey Playwright (allowlisted origin required):

```bash
npm --prefix web run dev -- --port 3001 --hostname 127.0.0.1   # one shell
cd e2e
E2E_BASE_URL=http://localhost:3001 npx playwright test passkey.spec.ts --project=chromium
```

Selenium sign-in smoke:

```bash
E2E_BASE_URL=http://127.0.0.1:3217 \
E2E_CONVEX_URL=https://perceptive-minnow-557.eu-west-1.convex.site \
  python3 e2e/selenium/signin.selenium.py
```

Redroid agentic QA, unattended signup (ephemeral account injection):

```bash
yaver ops qa_run '{"package":"io.yaver.mobile","base":"<version>","flowsDir":"yaver-tests/flows","mode":"catch","testAccount":"ephemeral"}'
```

Selenium signup smoke:

```bash
NEXT_PUBLIC_CONVEX_SITE_URL=https://perceptive-minnow-557.eu-west-1.convex.site \
  npm --prefix web run dev -- --port 3217 --hostname 127.0.0.1

E2E_BASE_URL=http://127.0.0.1:3217 \
E2E_CONVEX_URL=https://perceptive-minnow-557.eu-west-1.convex.site \
  python3 e2e/selenium/signup-onboarding.selenium.py
```

Redroid agentic QA:

```bash
yaver studio base build --yaver-apk ./app-release.apk
yaver ops qa_run '{"package":"io.yaver.mobile","base":"<version>","flowsDir":"yaver-tests/flows","mode":"catch"}'
yaver ops qa_report '{"jobId":"<jobId>"}'
```

Deterministic testkit examples:

```bash
yaver test run yaver-tests/landing.test.yaml --verbose
yaver test run yaver-tests/mobile-android-smoke.test.yaml --verbose
yaver test run yaver-tests/mobile-ios-smoke.test.yaml --verbose
```
