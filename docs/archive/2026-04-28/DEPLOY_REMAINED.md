# Deploy Remained

Date: 2026-04-23

This file is the exact handoff for finishing the pending deploy work later from home.

## Current State

### SFMG

- Repo: `/Users/kivanccakmak/Workspace/sfmg`
- Branch: `main`
- Head: `595fafdbb69d6556c3e7d4a5df6ff668f1c34a39`
- Commit already pushed:
  - `595fafd` `Fix Yaver feedback auth launch responsiveness`
- What this contains:
  - updated `patches/yaver-feedback-react-native+0.8.4.patch`
  - fix for Y icon auth launch responsiveness
  - one-tap one-open behavior
  - in-flight suppression to avoid double login/modal open
  - quick icon dim/busy feedback during launch

### Yaver Feedback SDK Source

- Repo: `/Users/kivanccakmak/Workspace/yaver.io`
- Branch: `release-20260423-ios-sdk-cli`
- Head: `021c0c417ad3498b9e4ce446b7a28171cf9910d5`
- Commit already pushed on that branch:
  - `021c0c41` `Fix feedback SDK auth launch responsiveness`
- Source files changed:
  - `sdk/feedback/react-native/src/AuthOverlay.tsx`
  - `sdk/feedback/react-native/src/QuickActionIcon.tsx`
  - `sdk/feedback/react-native/src/YaverFeedback.ts`

### npm State

- Current published npm version:
  - `yaver-feedback-react-native@0.8.4`
- Intended next publish:
  - `yaver-feedback-react-native@0.8.5`
- Local package version in source repo is already:
  - `0.8.5`

### Failed SDK Publish Run

- Workflow run:
  - `https://github.com/kivanccakmak/yaver.io/actions/runs/24833163706`
- Ref used:
  - `release-20260423-ios-sdk-cli`
- Failure reason:
  - npm `EOTP`
  - log showed `NODE_AUTH_TOKEN` was present, but publish still required an authenticator one-time password
- Meaning:
  - the package is ready
  - the code is not the blocker
  - npm account/token policy is the blocker

## Verified Good

### SDK Local Validation

Ran in `sdk/feedback/react-native`:

```bash
npm install --no-audit --no-fund
npm run build
npm test -- --runInBand
```

Result:

- build passed
- tests passed
- `6` suites passed
- `71` tests passed

### SFMG Deploy Script Exists

- `sfmg/scripts/deploy-testflight.sh`

This script:

- runs disk preflight
- runs iOS preflight
- bumps build number using `scripts/last-build.txt`
- archives
- exports IPA
- uploads to TestFlight
- updates TestFlight metadata

### Yaver Mobile Deploy Script Exists

- `yaver.io/scripts/deploy-testflight.sh`

This script:

- bumps `CFBundleVersion`
- archives Yaver iOS app
- exports and uploads to TestFlight

## Important Blockers

### 1. SDK npm publish is not finished

You cannot honestly say SFMG is deployed with the latest npm SDK until `0.8.5` is published.

Current blocker:

- npm publish requires OTP or an automation token that bypasses OTP

### 2. Yaver mobile repo is not in a clean release state

`/Users/kivanccakmak/Workspace/yaver.io` currently has many unrelated mobile changes beyond this SDK fix. Do not blindly release mobile from that branch unless that is intentionally the desired release snapshot.

### 3. SFMG worktree is locally dirty

At the time of handoff, `sfmg` still had local changes in:

- `app.json`
- `package.json`
- `package-lock.json`
- `scripts/last-build.txt`
- `src/app/settings.tsx`

The Y icon fix itself is already safely committed and pushed. Review local dirty state before any final TestFlight upload.

## Resume Order

Do these in this order.

### Step 1. Publish `yaver-feedback-react-native@0.8.5`

Preferred path if npm auth is fixed locally:

```bash
cd /Users/kivanccakmak/Workspace/yaver.io/sdk/feedback/react-native
npm whoami
npm publish --access public
```

If npm still requires OTP:

```bash
npm publish --access public --otp <CODE>
```

Alternative CI path after rotating GitHub `NPM_TOKEN` to an automation token:

```bash
cd /Users/kivanccakmak/Workspace/yaver.io
gh workflow run release-sdk.yml \
  --ref release-20260423-ios-sdk-cli \
  -f publish_js=false \
  -f publish_python=false \
  -f publish_flutter_core=false \
  -f publish_feedback_rn=true \
  -f publish_feedback_web=false \
  -f publish_feedback_flutter=false \
  -f publish_mobile_headless=false
```

Then verify:

```bash
npm view yaver-feedback-react-native version
```

Expected result:

- `0.8.5`

### Step 2. Refresh SFMG to the published SDK

After npm shows `0.8.5`:

```bash
cd /Users/kivanccakmak/Workspace/sfmg
npm install
```

Then verify the installed version:

```bash
node -p "require('./node_modules/yaver-feedback-react-native/package.json').version"
```

Expected:

- `0.8.5`

Note:

- Once `0.8.5` is installed from npm, the old local `patch-package` setup may no longer be needed for this fix if the published SDK already contains it.
- Re-check whether `patches/yaver-feedback-react-native+0.8.4.patch` should remain, be replaced, or be removed.

### Step 3. Deploy SFMG to TestFlight

Before running deploy:

```bash
cd /Users/kivanccakmak/Workspace/sfmg
git status --short
```

Make sure you understand any remaining local dirty files.

Then deploy:

```bash
cd /Users/kivanccakmak/Workspace/sfmg
bash scripts/deploy-testflight.sh
```

If you need a manual build number:

```bash
BUILD_NUMBER=105 bash scripts/deploy-testflight.sh
```

### Step 4. Decide the exact Yaver mobile release source

Before deploying Yaver mobile:

```bash
cd /Users/kivanccakmak/Workspace/yaver.io
git branch --show-current
git status --short mobile sdk/feedback/react-native
```

Do not release blindly from a dirty branch unless that is intentionally your release branch.

If the intended release branch is `release-20260423-ios-sdk-cli`, keep using it.

If you want `main`, first merge/cherry-pick the intended mobile changes properly.

### Step 5. Deploy Yaver mobile to TestFlight

Once the intended branch/snapshot is settled:

```bash
cd /Users/kivanccakmak/Workspace/yaver.io
bash scripts/deploy-testflight.sh
```

Required env vars for that script:

- `APP_STORE_KEY_PATH`
- `APP_STORE_KEY_ID`
- `APP_STORE_KEY_ISSUER`
- `APPLE_TEAM_ID`

## Fast Sanity Checks

### SDK

```bash
npm view yaver-feedback-react-native version
```

Expected:

- `0.8.5`

### SFMG installed SDK

```bash
cd /Users/kivanccakmak/Workspace/sfmg
node -p "require('./node_modules/yaver-feedback-react-native/package.json').version"
```

Expected:

- `0.8.5`

### Y Icon behavior to verify on device

After SFMG is rebuilt:

- tap Y once
- auth screen should open once
- closing it should not cause a second delayed reopen
- repeated quick taps during launch should be ignored
- icon should visibly dim while the auth/report launch is in flight

## Summary

Real remaining work is:

1. publish SDK `0.8.5`
2. refresh SFMG to that published SDK
3. deploy SFMG
4. choose the exact Yaver mobile release branch/snapshot
5. deploy Yaver mobile

The only hard blocker hit during this session was npm publish auth:

- GitHub Actions had `NODE_AUTH_TOKEN`
- npm still required OTP
- so the release stopped at package-security policy, not code quality
