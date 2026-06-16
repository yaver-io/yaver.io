# ADR: How to put Yaver on Apple TV (tvOS)

> Status: **Proposed** (2026-06-17). Companion: `docs/yaver-tv-car-deployment-roadmap.md`.
> Decision owner: maintainer. This ADR exists because CLAUDE.md says the
> `react-native-tvos` fork is a one-way-ish commitment that needs an explicit call
> before any code.

## Context

Stock React Native has **no tvOS support**. Android TV is *not* in this bind — it's just
Android, so the existing RN app runs there with focus/D-pad handling and a leanback launcher
(no fork). Apple TV is the only one that forces a decision.

The TV surface we actually want is **narrow and lean-back**: the existing Apple TV control +
capture-card dashboard (`appletv.go` / `capture.go`), device/agent status, and remote-desktop
*viewing*. It is **not** the full Yaver app — you will not author code or drive the agentic UI
from a couch with a Siri Remote.

## Options

### Option A — `react-native-tvos` fork (whole RN app on tvOS)
Adopt the community fork, add a tvOS target, port the focus engine, ship the whole app.
- **Pros:** one codebase; every existing screen *could* appear on TV.
- **Cons:** the fork **lags RN core releases**; New Architecture / Hermes / pod / gradle
  compatibility must be re-validated on every RN bump; it taxes *all* future mobile work
  (every `expo prebuild`, every native overlay in `mobile/ios/`); and 90% of the app is
  touch-first UI that's useless on a TV anyway. High maintenance, low payoff.

### Option B — thin **native tvOS app** (SwiftUI) over the agent API ✅ recommended
A small standalone tvOS app (SwiftUI, focus-native) that talks to the agent over the existing
HTTP/relay API and reuses the Apple TV/capture dashboard + device status surfaces.
- **Pros:** **no RN fork, zero tax on the mobile build**; native focus/parallax for free;
  matches the actual lean-back scope; ships the `appletv`/`capture` slice — which is *already*
  a TV-shaped experience — first. Same auth (SDK token), same relay.
- **Cons:** a second (small) UI to maintain; not every RN screen is reachable (by design).

### Option C — defer tvOS
Do Android TV (no fork) now; revisit Apple TV later.

## Decision

**Option B.** Build a thin native SwiftUI tvOS app scoped to the lean-back surfaces
(Apple TV/capture dashboard, device + agent status, remote-desktop view). Reserve
`react-native-tvos` (Option A) *only* if a future requirement genuinely needs the full RN app
on TV — and re-open this ADR then.

Android TV proceeds independently on the existing RN app (focus nav + leanback), no fork, and
is the first TV target to ship.

## Consequences

- New (small) native target; same SDK-token auth + relay transport as everything else.
- The mobile RN build stays unforked — no per-release fork-rebase tax.
- TV scope is deliberately a subset; we `log()`/document what's intentionally not on TV so it
  doesn't read as "missing."
- If Option A is ever chosen later, it's additive to this decision, not blocked by it.

## Status of the Option-B build

A source-only scaffold lands the skeleton (2026-06-17): `tvos/YaverTV/*.swift` — device-code
auth (`Backend.swift`, same Convex contract as `mobile/src/lib/tvSignIn.ts`), a LAN `/ops`
client (`AgentClient.swift`, mirrors `appletvClient.ts`), and the SwiftUI sign-in / dashboard /
Apple-TV-remote views. It is **not** wired to a build pipeline; creating the one-time Xcode
tvOS target is documented in `tvos/README.md`. Relay (off-LAN) transport is the next follow-up.
