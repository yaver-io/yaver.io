# Task: Perfect Chrome-Based Yaver Container Control + Feedback SDK Dogfood

Build and harden the Chrome/browser-based control lane for Yaver containers so it can reliably operate third-party apps, capture real feedback through the SDK, and dogfood the same automation path Yaver users will depend on.

This is a deep product-quality pass, not a shallow demo. Prefer real end-to-end evidence over proxy checks.

## Core Goal

Make Yaver autorun capable of driving a Chrome-backed container session against real third-party app surfaces, proving that:

1. the session is reachable and rendered, not merely offered
2. control input actually reaches the app
3. feedback comes through the in-app SDK path where applicable
4. failures produce actionable diagnostics
5. the same path can be dogfooded by Yaver itself

## Work Areas

### 1. Chrome-Based Control Of Yaver Containers

- Audit the existing browser/container control path.
- Verify that Chrome discovery, launch, navigation, screenshot, input, and shutdown are bounded and deterministic.
- Exercise the real browser where possible. Do not replace the browser with a fake unless the test is explicitly unit-level.
- Add or improve tests that prove frames are non-blank and interactive.
- Detect the common false-green states:
  - target offered but blank
  - target enabled but no rendered frame
  - navigate accepted but page unchanged
  - input accepted but app state unchanged
  - browser process alive but disconnected from the session

### 2. Third-Party App Control

- Use realistic third-party app shapes, not only internal toy pages.
- Cover at least one web app, one mobile/web-preview style app, and one containerized app path if supported by the checkout.
- Verify app-specific readiness with rendered pixels, DOM/app state, or SDK feedback rather than process existence.
- Keep tenant isolation and relay security invariants intact. Any control path must be scoped to the correct owner/session/container.

### 3. Feedback SDK Perfection

- Trace the feedback transport used by the browser/container lane.
- Ensure the preferred path is the in-app SDK feedback channel, not viewer-triggered or synthetic feedback.
- Add assertions or diagnostics that report which feedback transport was used.
- Prove feedback survives navigation, reload, app restart, and reconnect where those flows exist.
- Make failures explain the exact missing capability: SDK absent, handshake failed, transport downgraded, app never emitted feedback, or relay/session mismatch.

### 4. Yaver Dogfooding

- Make Yaver exercise this same autorun path against its own demos or internal apps.
- Prefer one repeatable command that launches the target, controls it through Chrome/container automation, verifies rendered state, sends feedback, and reports a concise result.
- Avoid special internal shortcuts that a real user would not have.
- If dogfood reveals a real incident class, encode it into a doctor probe, preflight, or autorun diagnostic before calling the task done.

### 5. tvOS Deep Analysis

- Audit tvOS architecture, build, deploy, feedback, and remote-control assumptions.
- Identify gaps between browser/container control and tvOS control surfaces.
- Check whether tvOS deployment, simulator, signing, networking, feedback SDK, and session bootstrap paths are documented and tested.
- Add actionable diagnostics or tests for any false-green state found.
- Do not deploy to TestFlight or mutate Apple/provider state without explicit user confirmation.

### 6. Deployment Readiness

- Review deploy scripts and preflights relevant to browser/container control, feedback SDK, and tvOS.
- Ensure deployment checks validate real capability rather than inventory.
- For any failure-prone deploy step, make the error name the likely fix.
- Do not push, tag, publish npm, deploy mobile, or publish TestFlight without explicit user permission.

## Required Verification Style

Use real end-to-end checks where the product risk is integration failure:

- rendered pixels for browser/container sessions
- app-observable state changes for control input
- SDK-observable feedback for feedback transport
- real route/handler existence for documented endpoints
- real signing/deploy dry-run capability where safe

Skip honestly when a local prerequisite is missing. A skipped test must explain the missing real capability.

## Suggested Gates

Run only targeted gates appropriate to changed areas. Avoid broad commands known to mutate local Yaver state.

For `desktop/agent` browser-lane work:

```sh
cd desktop/agent
go build -o /dev/null .
go vet ./...
go test -count=1 -run 'Browser|Chrome|Container|Feedback|Navigate|DevServer' .
```

For tvOS work, inspect existing scripts/docs first and use the narrowest safe build or diagnostic command. Do not publish or deploy without confirmation.

## Safety Rules

- Code is authoritative. Read docs for context, then grep the code before relying on any claim.
- Stay scoped to Yaver resources in this checkout unless the user explicitly names another resource.
- Never commit or push without explicit permission.
- Never deploy mobile, tvOS, npm, tags, or provider state without explicit permission.
- Never commit private keys, tokens, customer IPs, relay hostnames, or secrets.
- Never make advisory work block the critical path. Diagnostics must be bounded and degrade clearly.
- Every real failure should leave behind a faster way to diagnose it next time.

## Done Means

- Chrome/container control has real rendered-frame and input evidence.
- Feedback SDK transport is proven or failures identify the missing piece.
- Yaver dogfoods the same autorun path without privileged shortcuts.
- tvOS gaps are deeply analyzed and converted into tests, diagnostics, or explicit follow-up tasks.
- Deployment readiness checks are sharper and safer than before.
- The final report names changed files, commands run, skips, and any remaining risks.
