# Email/Password Test Auth

Yaver's normal sign-in path should stay OAuth/passkey-first. Email/password is
an owner/test escape hatch for automated browser, redroid, and simulator runs
where Apple/Google browser approval is not practical.

## Security Model

- Email/password sign-in is closed by default.
- Opening it requires `YAVER_EMAIL_PASSWORD_AUTH_ENABLED=true` in Convex env.
- Allowed emails are controlled by `YAVER_EMAIL_PASSWORD_AUTH_ALLOWED_EMAILS`
  as a comma-separated list. If that env var is empty, the existing owner email
  allowlist is used as the fallback.
- Raw passwords must never go in Convex env, source files, checked-in fixtures,
  screenshots, or logs.
- Convex stores only a salted PBKDF2-SHA256 password hash on the existing user.
- Turning `YAVER_EMAIL_PASSWORD_AUTH_ENABLED` off stops new email/password
  signup, login, reset, change, and set-password requests. Existing OAuth,
  passkey, and session-token flows are unaffected.

## Setup

1. Sign in with the owner OAuth account, for example Google/Gmail.
2. Temporarily enable the route gate:

   ```bash
   yaver set emailOauth enable --allowed-emails owner@example.com
   ```

3. Open Dashboard Settings or mobile Settings.
4. In `Email / Password`, set the automation password. This links an `email`
   identity to the same account; it does not create a duplicate Yaver user.
5. Store the raw password only in the place that runs the test:

   ```bash
   # GitHub Actions repository or environment secrets
   YAVER_TEST_EMAIL=owner@example.com
   YAVER_TEST_PASSWORD=<strong generated password>
   ```

   For local runs, use macOS Keychain, 1Password CLI, direnv with an untracked
   `.env`, or another local secret store. Do not put the password in Convex env.

6. After the test window, close the route gate:

   ```bash
   yaver set emailOauth disable
   ```

## Automated Tests

Tests should read the email/password from their secret store and call the same
`/auth/login` endpoint the apps use. They should skip or fail clearly when
`GET /auth/config` reports `emailPasswordEnabled: false`.

Recommended secret names:

- `YAVER_TEST_EMAIL`
- `YAVER_TEST_PASSWORD`

The Yaver agent exposes a CLI wrapper for closed-loop runs:

```bash
yaver set emailOauth \
  --email "$YAVER_TEST_EMAIL" \
  --password-env YAVER_TEST_PASSWORD \
  --require-owner
```

`--password-stdin` is available for wrappers that read from macOS Keychain,
1Password, GitHub Actions, or a simulator harness and want to avoid putting the
password in argv. `--print-token` can be used by short-lived test wrappers that
need the session token on stdout; do not log that output.

To prepare a remote owned machine in one command, keep the same secret env var
on the remote and ask that machine to perform the login locally:

```bash
yaver set emailOauth \
  --machine magara \
  --email "$YAVER_TEST_EMAIL" \
  --password-env YAVER_TEST_PASSWORD \
  --require-owner
```

Remote mode intentionally requires `--password-env`; it does not accept
`--password` or `--print-token`. The controller sends the email, env-var name,
and Convex URL to the target agent. The target reads its own secret env var,
logs in, and stores only its own Yaver session token.

For MCP/runner flows, use `yaver_auth_capabilities` first. MCP should only
learn whether email/password auth is enabled and which secret names to use. It
must not fetch or store the raw password from Convex. The runner process should
receive the password from its local execution environment, call
`yaver set emailOauth` or `/auth/login`, and continue with the returned Yaver
session token.

Use a unique generated password for this path, rotate it after broad CI access,
and keep 2FA/passkeys/OAuth linked on the same owner account so password auth is
never the only recovery path.
