# Signup E2E Coverage Audit

Status: updated 2026-06-11. Code is the source of truth; this audit was checked
against the current `web/app/auth/page.tsx`, Convex HTTP routes, Playwright
suite, `testkit`, and redroid QA wiring.

## What Exists Now

Browser auth coverage:

- `e2e/global-setup.ts` creates a throwaway Convex user through
  `POST /auth/signup`.
- `e2e/tests/auth-flow.spec.ts` covers email/password sign-in, bad password,
  redirect to an authenticated route, and localStorage token persistence.
- `e2e/tests/signup-onboarding.spec.ts` covers the actual signup UI path:
  account creation, token persistence, `/auth/validate`, password mismatch,
  short-password client validation, duplicate-email UX, and
  signup→logout→signin.
- `.github/workflows/e2e.yml` now runs the full Playwright suite instead of
  only `vibe-preview.spec.ts`.
- `e2e/global-teardown.ts` deletes every tracked throwaway account, including
  accounts created inside individual tests.

Selenium coverage:

- `.github/workflows/selenium-webapp-sfmg.yml` drives the live dashboard
  Webview/Web App path with Selenium and an injected token from the ephemeral
  box.
- `e2e/selenium/signup-onboarding.selenium.py` is a local WebDriver smoke that
  creates a real email account through the UI, validates its bearer token, and
  deletes it.

Android/redroid coverage:

- `desktop/agent/testkit/driver_androidredroid.go` is a first-class
  `android-redroid` target using the Studio redroid surface, warm base images,
  UIAutomator dumps, tap/type/screenshot, and shared Android selector logic.
- `desktop/agent/ops_qa.go` exposes `qa_base_build`, `qa_base_up`, `qa_run`,
  `qa_report`, and `qa_base_gc`.
- `yaver-tests/flows/*.flow.yaml` are agentic redroid flows. The new
  `03-signup-onboarding.flow.yaml` records the native signup/onboarding target
  without embedding credentials.

Yaver testkit coverage:

- `desktop/agent/testkit` supports deterministic YAML specs for web, iOS sim,
  Android emulator, Android redroid, and physical-device targets.
- The web target already drives Chrome through CDP, captures console/network
  instrumentation, HAR, screenshots, screencast frames, a11y audits, snapshots,
  and flake retries.

## Gaps

- OAuth signup is not fully automated. Apple/GitHub/GitLab/Google/Microsoft
  require provider sandboxes or test IdPs; current coverage only verifies the
  button routing surface indirectly.
- Passkey signup/login is not automated. Browser WebAuthn can be virtualized,
  but the current Playwright suite does not yet register a virtual
  authenticator.
- Email verification and password reset are not in the signup E2E path.
- The mobile/redroid signup flow cannot safely create an account by itself
  because `qa_flow.go` only loads `name`, `goal`, `package`, `expectations`,
  and `max_steps`; there is no secret/template injection for throwaway
  credentials yet.
- No nightly matrix currently runs web signup across Chromium + WebKit +
  Firefox, slow network, and mobile viewport.
- Backend auth route tests exist through E2E and scattered Go smoke tests, but
  there is no dedicated Convex HTTP contract suite for every `/auth/*` route.

## Rerunnable Design

Account lifecycle:

- Every E2E account uses a randomized `e2e-* @yaver.test` email.
- Tests persist created users to `e2e/.playwright/test-users.json`.
- Each test cleans up immediately when it can; global teardown is the backstop.
- No real credential, customer email, relay hostname, or device identifier is
  checked in.

Browser layers:

- Playwright is the deep browser suite because it gives trace, video,
  screenshots, route interception, and CI browser install ergonomics.
- Selenium stays as a compatibility smoke and for external WebDriver runners.
- Both paths hit the same production-shaped Convex HTTP routes by default, with
  `E2E_CONVEX_URL` and `E2E_BASE_URL` overrides for previews/local backends.

Native layers:

- Deterministic mobile regressions should live under
  `yaver-tests/specs/*.test.yaml` when selectors are stable.
- Exploratory native signup/onboarding should live under
  `yaver-tests/flows/*.flow.yaml` and run with `qa_run` on a warm redroid base.
- The next required primitive is credential templating for QA flows, for
  example `qa_run { testAccount: "ephemeral" }` creating/deleting an account
  and exposing only the generated values to the local brain.

## Recommended Next Steps

1. Add virtual-WebAuthn Playwright tests for passkey signup/login.
2. Add a Convex auth HTTP contract suite for signup/login/validate/refresh/logout/delete/reset.
3. Add QA-flow credential injection so `03-signup-onboarding.flow.yaml` can run
   unattended on redroid.
4. Add a nightly full-browser matrix; keep PR CI on Chromium to control wall time.
