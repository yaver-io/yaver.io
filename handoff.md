# Yaver Test Coverage Handoff

Status: 2026-06-12. This handoff reflects the code after the signup E2E work in
`4cd6087f` plus this document. As always in this repo, code wins over docs.

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

Verification run:

```bash
cd e2e
npx tsc --noEmit
CI=1 npx playwright test --project=chromium --workers=1 --retries=0
```

Last local result: 18 passed, 2 skipped.

Skipped:

- `managed-cloud-delete.spec.ts`: requires owner `YAVER_E2E_TOKEN` and a real
  managed cloud machine.
- `dashboard-autodev.spec.ts`: skipped because it targeted the removed
  Console -> Autodev dashboard workbench. The mock remains as old endpoint
  contract reference, but there is no current UI surface to drive.

### Selenium / WebDriver

Location: `e2e/selenium/signup-onboarding.selenium.py`

Coverage:

- Opens `/auth` in Chrome through Selenium Manager.
- Switches to email signup.
- Creates a randomized `e2e-selenium-*@yaver.test` account.
- Verifies redirect to `/survey` or `/dashboard`.
- Reads `yaver_auth_token` from localStorage.
- Calls `/auth/validate`.
- Deletes the throwaway account.

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

Not covered yet:

- OAuth provider signup/login end-to-end for Apple, GitHub, GitLab, Google,
  Microsoft.
- Passkey signup/login with virtual WebAuthn.
- Email verification flow.
- Forgot-password/reset-password flow.
- TOTP setup and TOTP-required login in browser E2E.
- Account merge/linking flows.
- OAuth duplicate-email linking.
- Rate-limit and abuse protection behavior.
- Full logout endpoint/session invalidation behavior.

Can be covered:

- Passkeys can be covered with Playwright virtual authenticators.
- Password reset can be covered if the test backend exposes a safe test email
  token capture endpoint or local mail sink.
- TOTP can be covered using the existing setup endpoint plus a test OTP
  generator.
- OAuth can be covered with provider sandboxes or a local OIDC test provider.

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

Why:

- `qa_flow.go` currently loads only `name`, `goal`, `package`,
  `expectations`, and `max_steps`.
- Flow YAML has no safe secret/template injection yet.
- Hardcoding credentials in tracked YAML is not allowed.

What should be added next:

- `qa_run { testAccount: "ephemeral" }` or equivalent.
- The runner creates a randomized account, exposes only generated values to the
  local QA brain, and deletes the account at run end.
- Then `03-signup-onboarding.flow.yaml` can become fully unattended.

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

1. Add Playwright virtual-WebAuthn tests for passkey signup/login.
2. Add password reset E2E with a local/test mail capture path.
3. Add TOTP setup and TOTP login tests.
4. Add QA credential injection for redroid flows.
5. Convert `03-signup-onboarding.flow.yaml` from scaffold to unattended native
   redroid test.
6. Replace the skipped Autodev dashboard spec with a current UI target, or
   delete it if Autodev is no longer product surface.
7. Add a nightly browser matrix for Chromium/Firefox/WebKit and mobile
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
