# Managed Cloud Store Policy Handoff

Date: 2026-05-28

## Goal

Yaver mobile should support Yaver Managed Cloud as an infrastructure/dev-machine
product without triggering Apple App Store or Google Play billing commission.

The safe product shape is:

- Web/CLI owns checkout, billing, top-ups, invoices, and cancellation.
- Mobile is a free companion/control app.
- Mobile can consume an already-active entitlement: list machines, monitor
  provisioning, connect, start/stop, run Hermes builds, and mirror runner auth.
- Mobile must not initiate purchase, open checkout URLs, show prices, show
  top-up controls, or present purchase CTAs for managed cloud.

## Policy Position

Apple positioning:

- Use App Review Guideline 3.1.3(f): free stand-alone companion to a paid
  web-based web-hosting/cloud-development service.
- Avoid in-app purchase CTAs or external checkout links in mobile.

Google Play positioning:

- Consumption-only companion.
- Users can sign in and use managed cloud machines acquired elsewhere.
- The Play-distributed app must not sell access or route users to alternate
  payment methods.

## Implemented Changes

### `mobile/src/lib/managedCloudFlow.ts`

Converted the managed cloud flow from buy/provision/setup into post-purchase
setup only.

Before:

- Called `ops cloud_checkout`.
- Received a LemonSqueezy checkout URL.
- Emitted `checkoutUrl` to mobile UI.
- Mobile could open checkout.

Now:

- Calls only `ops cloud_status`.
- Picks an existing managed cloud machine on the account.
- Waits for the machine agent to come online.
- Calls `runner_auth_mirror`.
- Returns the cloud device id.

Important invariant:

- Do not reintroduce `cloud_checkout`, `checkoutUrl`, or payment routes into
  this mobile helper.

### `mobile/app/cloud-onboarding.tsx`

Converted the screen from a purchase front door into post-purchase setup.

Before:

- Header: `buy a dev box`.
- Opened LemonSqueezy via `Linking.openURL`.
- Rendered `re-open checkout`.
- Alert copy said the user would be charged.

Now:

- Header: `set up cloud box`.
- No `Linking` import.
- No checkout URL rendering.
- Alert says Yaver will look for an existing managed-cloud machine and mirror
  the runner token.

### `mobile/app/glass-terminal.tsx`

Changed the device-picker CTA:

- Before: `buy a dev box`
- After: `set up cloud box`

The route still opens `/cloud-onboarding`, but that screen is now setup-only.

### `mobile/src/components/ManagedCloudCard.tsx`

Removed mobile top-up and external-buy language.

Before:

- Rendered `Dev top-up`.
- Called `devTopUpManagedCloud`.
- Empty state said `No active subscription — buy from the web dashboard...`.
- Displayed `Plan ${sub.plan}`.

Now:

- No top-up button.
- No `devTopUpManagedCloud` import.
- Empty state says `No managed cloud machine is active on this account.`
- Status line says `Managed cloud · ${sub.status}` instead of plan name.

### `mobile/src/lib/phoneProjects.ts`

Updated the 402/payment-required handling comment and user-facing error.

Important behavior:

- The code still preserves `checkoutUrl` in `PhonePushPaymentRequired` for
  web/CLI callers.
- Mobile UI must not display or open that URL.

New error copy:

- `this cloud tenant requires an active managed cloud machine on the account`

### `mobile/store-metadata/infrastructure-declaration.md`

Rewritten from an overbroad “infrastructure exemption / reader app” claim into
a narrower store-review declaration.

Core rules now documented:

- Mobile does not sell managed cloud.
- Mobile does not create, display, or open external checkout URLs.
- Mobile does not show prices, top-up controls, or purchase CTAs.
- Checkout, credits, invoices, and cancellation live outside mobile.
- Mobile only consumes existing entitlements.

## Verification Performed

Command:

```bash
git diff --check -- mobile/src/lib/managedCloudFlow.ts mobile/app/cloud-onboarding.tsx mobile/app/glass-terminal.tsx mobile/src/components/ManagedCloudCard.tsx mobile/src/lib/phoneProjects.ts mobile/store-metadata/infrastructure-declaration.md
```

Result:

- Passed.

Command:

```bash
cd mobile && npx --no-install tsc -p tsconfig.json --noEmit
```

Result:

- Failed on pre-existing unrelated errors:
  - `app/(tabs)/publish.tsx`: `ThemeColors.danger`
  - `app/(tabs)/tasks.tsx`: runner object missing `ready`
  - `app/phone-project/code/[slug].tsx`: implicit `any`
  - `src/lib/phoneSandboxFsExpo.ts`: Expo FileSystem type mismatches

No TypeScript errors from the touched managed-cloud files were reported before
the existing errors stopped the check.

## Follow-Up Review Checklist

1. Search mobile for managed-cloud purchase language before the next store
   build:

   ```bash
   rg -n "buy a dev box|Buy a dev box|open checkout|re-open checkout|cloud_checkout|Dev top-up|buy from the web|3\\.1\\.3\\(a\\)|30%" mobile -S --glob '!node_modules/**'
   ```

2. Confirm no mobile UI catches `PhonePushPaymentRequired` and displays or opens
   `checkoutUrl`.

3. Confirm web dashboard and CLI still provide the purchase path:

   - `web/components/dashboard/ManagedCloudPanel.tsx`
   - `desktop/agent/cloud.go`
   - `backend/convex/http.ts` `/billing/yaver-cloud/checkout`

4. Consider build-time gating if there are enterprise/internal mobile builds
   where checkout is acceptable. Store builds should keep checkout disabled.

5. Review mobile dependencies:

   - `@stripe/stripe-react-native`
   - `react-native-iap`
   - `react-native-purchases`

   These are not automatically disallowed, but make sure no managed-cloud
   payment UI is reachable in the App Store / Play Store app.

## Worktree Note

This handoff originally mentioned unrelated dirty files that existed while it
was written. That note is no longer authoritative; check `git status --short`
and the code before relying on any worktree-state claim.

## Bottom Line

The current mobile implementation is now aligned with the intended
no-commission strategy: mobile is setup/consumption-only for managed cloud,
while payment and checkout remain outside the store-distributed app.
