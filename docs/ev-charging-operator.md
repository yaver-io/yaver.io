# Yaver EV Charging Operator

Date: 2026-06-17

Audience: Yaver implementation agents and product engineering.

Purpose: define the architecture for using Yaver mobile as the user's EV charging
control surface while real provider apps such as Esarj, ZES, Trugo, and others
remain the authority for accounts, payment, OTP, and charger session start/stop.

## Executive Summary

The goal is:

```text
User scans a charger QR in Yaver.
Yaver identifies provider, station, connector, and available execution routes.
Yaver uses the safest route: official API, provider app deep link, remote Android,
or manual assist.
The user explicitly approves login, OTP, payment, start, and stop operations.
Yaver tracks the local charging intent, status, evidence, timers, and receipt.
```

The implementation should not try to build one universal hidden charging API.
EV charging providers differ in account model, QR format, payment flow, app
integrity checks, location requirements, BLE/NFC requirements, station firmware,
and regulation. The dependable product is a supervised operator:

```text
Yaver = user-present command layer + remote runtime + audit/state machine
Provider app/API = source of truth for money, identity, and charger session
```

This is also the right safety posture. Starting a real charging session can spend
money and can reserve/occupy physical infrastructure. Yaver must treat it as a
high-impact operation requiring visible user confirmation.

## Code-Backed Existing Primitives

Docs drift. Before implementing any claim here, grep the code named below. The
current repo already has the building blocks needed for an MVP:

- Mobile camera QR scanning:
  - `mobile/src/components/ProvisionScanner.tsx`
  - `mobile/src/components/DeviceCodeScanner.tsx`
  - `mobile/package.json` includes `expo-camera`
- Mobile deep-link routing:
  - `mobile/src/lib/pairLinkHandler.tsx`
- Remote Android control from Yaver mobile:
  - `mobile/app/droid-control.tsx`
  - `desktop/agent/droid_interactive_http.go`
  - routes wired in `desktop/agent/httpserver.go`:
    - `GET /droid/status`
    - `GET /droid/frame`
    - `POST /droid/input`
    - `GET /droid/ui`
    - `POST /droid/launch`
- Generic human-in-loop browser:
  - `desktop/agent/browser_interactive.go`
  - `desktop/agent/browser_interactive_http.go`
  - `mobile/app/browser-interactive.tsx`
- Device connectivity and remote transport:
  - `mobile/src/context/DeviceContext.tsx`
  - `mobile/src/lib/quic.ts`
  - `desktop/agent/httpserver.go`
- Sensitive local storage / vault posture:
  - `desktop/agent/vault.go`
  - `mobile/app/vault.tsx`
  - Convex privacy tests in `desktop/agent/convex_privacy_test.go`

These primitives imply the first implementation should be mostly mobile UI,
provider parsing, and a small agent-side EV ops surface that composes existing
Android/browser control. Do not add a mobile build as part of this work unless
the user explicitly requests one.

## Product Thesis

EV charging is a physical-world operation with a digital payment/auth layer.
The user is typically standing at a charger with a phone and a vehicle. Yaver's
advantage is that it can combine:

- local QR/camera capture on the user's phone
- remote machine control over LAN, Tailscale, Cloudflare tunnel, or Yaver relay
- remote Android app operation through adb
- human-in-loop OAuth/SMS/payment approvals
- AI-assisted UI reading and next-action planning
- local/P2P storage of sensitive session data

The product should feel like this:

```text
Open Yaver -> EV Charging -> Scan QR -> Yaver says:

"This looks like ZES, charger TR-..., connector 2.
I can open the ZES app on this phone, or continue through your remote Android
device where ZES is already signed in. Starting may require SMS/payment approval.
Nothing starts until you approve the final provider screen."
```

## Non-Goals

Yaver must not:

- bypass provider app integrity, root/emulator detection, CAPTCHA, OTP, KYC, or
  payment confirmation
- scrape or reverse-engineer private provider APIs without authorization
- silently start a paid charging session
- silently stop a session if that would strand a user or create billing risk
- store provider passwords, raw SMS contents, card details, or session cookies in
  Convex
- create accounts with charging providers for the user
- hide provider tariffs, warnings, payment screens, or legal terms
- impersonate a different device, location, or user
- defeat proximity/location/BLE/NFC checks

When a provider requires a user-present step, the correct behavior is:

```text
pause -> show the user what is needed -> route to provider app or remote Android
-> wait for user approval -> continue
```

## Core Runtime Routes

Each provider/session can choose among four execution routes.

### 1. Official API Route

Use this only when a provider exposes a legitimate API or integration route for
the user's account.

Capabilities:

- authenticate with provider-supported OAuth/API credentials
- read station/connector state
- read tariffs and wallet/account status
- start/stop sessions when explicitly approved
- fetch receipts

This is the highest-quality path, but it will be uncommon until individual
provider partnerships or public APIs exist.

### 2. Provider App Deep-Link Route

Use when the QR code encodes a provider URL/app link that the installed provider
app can handle.

Flow:

```text
Yaver scan -> parse provider URL -> show summary -> open provider app
-> user completes provider UI -> Yaver tracks timer/status manually or via return
```

This works best on the same physical phone that is at the charger. It respects
provider app camera/GPS/payment UX.

### 3. Remote Android Route

Use when the user has a remote machine with an attached Android device or a
Redroid-style runtime and provider apps installed.

Flow:

```text
Yaver mobile -> active Yaver agent -> /droid/launch provider app
-> /droid/frame shows the Android screen
-> /droid/input sends taps/text/swipes
-> /droid/ui reads visible text where possible
-> user approves OTP/payment/start/stop
```

This is useful for:

- account setup and login
- wallet checks
- invoice download
- checking app-only station state
- provider support workflows
- supervised start/stop only when provider does not require physical proximity

Remote Android is not guaranteed for physical session start. Providers may
require the real on-site phone, GPS, BLE, NFC, camera ownership, or attested app
state.

### 4. Manual Assist Route

Use when provider unknown, provider blocks automation, QR format is unsupported,
or a physical/proximity check is required.

Yaver still helps by:

- decoding visible station/connector IDs
- identifying likely provider
- opening the right app/site
- showing a checklist
- tracking timer, kWh, parking duration, cost estimate, and receipt reminders
- saving local notes/evidence

Manual assist is a valid first-class success mode, not a failure.

## State Machine

The first implementation should use an explicit state machine so UI, agent ops,
and later AI narration stay coherent.

```text
idle
  -> scanning
  -> qr_captured
  -> provider_identified
  -> route_selected
  -> auth_check
  -> awaiting_user_confirmation
  -> starting
  -> charging
  -> stopping
  -> complete
```

Failure and pause states:

```text
provider_unknown
qr_unreadable
connector_unknown
auth_required
otp_required
payment_required
proximity_required
provider_app_required
remote_android_unavailable
blocked_by_provider
manual_only
cancelled
failed
```

High-impact transitions require explicit user approval:

- `auth_required -> provider login`
- `otp_required -> submit OTP`
- `payment_required -> provider payment confirmation`
- `awaiting_user_confirmation -> starting`
- `charging -> stopping`

The user-facing confirmation must include, when known:

- provider
- station
- connector
- tariff
- wallet/payment method
- vehicle/account nickname
- route being used: local app, remote Android, official API, manual

## Data Model

Keep v1 local-first/P2P. Do not store sensitive provider data in Convex.

Suggested TypeScript shape:

```ts
export type EVProviderId =
  | "esarj"
  | "zes"
  | "trugo"
  | "unknown";

export type EVRouteKind =
  | "official_api"
  | "provider_deeplink"
  | "remote_android"
  | "manual_assist";

export type EVChargingState =
  | "idle"
  | "scanning"
  | "qr_captured"
  | "provider_identified"
  | "route_selected"
  | "auth_check"
  | "awaiting_user_confirmation"
  | "starting"
  | "charging"
  | "stopping"
  | "complete"
  | "provider_unknown"
  | "qr_unreadable"
  | "connector_unknown"
  | "auth_required"
  | "otp_required"
  | "payment_required"
  | "proximity_required"
  | "provider_app_required"
  | "remote_android_unavailable"
  | "blocked_by_provider"
  | "manual_only"
  | "cancelled"
  | "failed";

export interface EVChargingIntent {
  id: string;
  createdAt: number;
  updatedAt: number;
  state: EVChargingState;
  provider: EVProviderId;
  route?: EVRouteKind;
  rawQr?: string;
  normalizedUrl?: string;
  stationId?: string;
  connectorId?: string;
  chargerId?: string;
  socketLabel?: string;
  locationHint?: {
    lat?: number;
    lon?: number;
    address?: string;
  };
  tariffHint?: {
    currency?: string;
    pricePerKwh?: number;
    parkingFee?: string;
    rawText?: string;
  };
  remoteAndroid?: {
    deviceId: string;
    packageHint?: string;
    attachedSerial?: string;
  };
  approvals: EVApproval[];
  events: EVEvent[];
}

export interface EVApproval {
  id: string;
  at: number;
  kind: "login" | "otp" | "payment" | "start" | "stop";
  label: string;
  approved: boolean;
}

export interface EVEvent {
  at: number;
  type: string;
  message: string;
  data?: Record<string, unknown>;
}
```

Convex may store only non-sensitive summaries if needed later:

- feature usage counts
- provider enum
- route enum
- success/failure class
- timestamps rounded enough for analytics

Convex must not store:

- raw QR if it contains account/session tokens
- screenshots
- OTP/SMS values
- provider cookies/tokens
- card/payment data
- exact user location unless separately designed and consented
- charger photos

## Provider Adapter Contract

Provider adapters should be capability descriptors, not hardcoded UI scripts.

```ts
export interface EVProviderAdapter {
  id: EVProviderId;
  label: string;
  domains: string[];
  androidPackageHints: string[];
  parseQr(raw: string): EVParsedQR | null;
  buildRoutes(intent: EVChargingIntent, env: EVRouteEnv): EVRouteOption[];
}

export interface EVParsedQR {
  provider: EVProviderId;
  normalizedUrl?: string;
  stationId?: string;
  connectorId?: string;
  chargerId?: string;
  socketLabel?: string;
  confidence: "high" | "medium" | "low";
  notes?: string[];
}

export interface EVRouteEnv {
  hasActiveYaverDevice: boolean;
  hasRemoteAndroid: boolean;
  localProviderAppKnown?: boolean;
}

export interface EVRouteOption {
  kind: EVRouteKind;
  label: string;
  risk: "low" | "medium" | "high";
  requiresUserPresent: boolean;
  requiresApproval: boolean;
  available: boolean;
  unavailableReason?: string;
}
```

Start with static provider detection:

- Esarj: known domains/package hints after code/user verification
- ZES: known domains/package hints after code/user verification
- Trugo: known domains/package hints after code/user verification
- Unknown: generic URL/domain/connector parser

Do not guess package IDs from memory in code. Before wiring package names, verify
from an installed test device, Play Store metadata, or user-provided config.

## QR Parsing Rules

Parsing should be conservative:

1. Preserve raw QR only in local memory unless the user saves a session.
2. Normalize URL with the platform URL parser.
3. Identify provider by verified hostname/app scheme patterns.
4. Extract connector/station IDs from query params and path segments.
5. Assign confidence:
   - high: provider domain + explicit connector/station fields
   - medium: provider domain + opaque token/path
   - low: generic URL/text with station-like values
6. If raw QR may contain a one-time token, never display it fully or send it to
   the remote agent unless the user approves that route.

The mobile app already uses `expo-camera` scanners that ignore irrelevant QR
payloads. EV scanning should be similar, but it should accept any QR and then
classify, because charger QR formats vary widely.

## Remote Android Operations

The v1 remote Android integration should use existing generic endpoints:

- `POST /droid/launch` to open provider apps by package hint
- `GET /droid/status` to detect attached device and focus
- `GET /droid/frame` to show the screen
- `POST /droid/input` for tap/text/key/swipe
- `GET /droid/ui` to collect visible text for hints

Do not create provider-specific adb scripts for payments/start in v1. Provider UI
automation is brittle and high-impact. The first product layer should be:

```text
AI/user sees the screen -> Yaver suggests next step -> user taps/approves
```

Provider-specific automation may be added only for low-impact navigation:

- open app
- open scan/deeplink target
- navigate to invoices/receipts
- read visible labels
- recover from known login screens

Never automate final payment/start/stop without an explicit confirmation event.

## SMS, OTP, OAuth, and Payment

Treat each as a separate auth plane, similar to the existing Yaver capability
ladder thinking.

Planes:

1. Yaver account auth
   - already handled by mobile/agent auth
2. Remote Android access
   - owned Yaver device + authenticated `/droid/*`
3. Provider app auth
   - provider-owned account session
4. OTP/SMS auth
   - user-present one-time approval
5. Payment auth
   - card/wallet/bank/3DS provider flow

Rules:

- OTP values are user-entered and should not be stored.
- Yaver may offer a text input that sends a typed OTP to `/droid/input`, but the
  event log should record only `otp_submitted_by_user`, not the value.
- OAuth should prefer system browser/in-app browser flows already used elsewhere
  in Yaver. Do not scrape callback pages for tokens.
- Payment screens must remain visible to the user.
- The final start confirmation must be user-approved even if the provider app is
  already logged in.

## UX Surfaces

### Mobile EV Screen

Route suggestion: `mobile/app/ev-charging.tsx`.

Primary controls:

- Scan QR
- Paste QR/link
- Provider selector when unknown
- Route selector:
  - Open provider app
  - Use remote Android
  - Manual assist
- Approval sheet for start/stop/payment
- Charging timer/status card
- Event log

The screen should reuse existing app patterns:

- `useDevice()` for active remote machine
- `quicClient.agentRequest()` for agent calls
- `useColors()` for theme
- `AppBackButton`
- `CameraView` from `expo-camera`

Do not add a landing page. The first viewport should be the usable charging
operation surface.

### Remote Android Pane

V1 can deep-link to the existing `droid-control` screen after setting a provider
package hint. Later, embed a focused pane inside `ev-charging.tsx`.

Needed convenience:

- launch selected provider package
- pass raw/deep link when safe
- show current focus/package
- show one-line extracted UI text hints

### Provider App Same-Phone Route

For same-phone flows:

- use `Linking.openURL()` for provider URLs when safe
- show warning that Yaver cannot see provider app after handoff unless the user
  returns
- keep a local charging timer and receipt reminder

## Agent-Side EV Surface

V1 can work without new agent endpoints by calling `/droid/*` directly.

Add a thin ops layer later if repeated patterns emerge:

```text
GET  /ev/status
POST /ev/provider/launch
POST /ev/intent/start-remote-android
POST /ev/intent/event
```

This layer should not contain provider credentials or QR secrets. It should only
compose existing owned-device operations and return low-sensitivity status.

## Security and Privacy

Principles:

- local-first state
- P2P for remote control
- no raw provider secrets in Convex
- explicit approval for high-impact actions
- auditable user intent
- no hidden provider automation

Audit locally:

- QR scanned
- provider detected
- route selected
- remote Android launched
- user approved login/payment/start/stop
- session completed/failed

Do not audit:

- OTP value
- passwords
- card details
- full raw QR when token-like
- screenshots by default

If screenshots/evidence are later added, make them opt-in and local-only first.

## Implementation Plan

### Phase 0 - Documentation and Guardrails

- Add this document.
- Add a TODO section to the implementation handoff after code starts.
- Keep mobile build/deploy out of this thread unless explicitly requested.

### Phase 1 - Mobile QR Intent MVP

Files likely involved:

- `mobile/app/ev-charging.tsx`
- `mobile/src/lib/evCharging/providers.ts`
- `mobile/src/lib/evCharging/types.ts`
- `mobile/src/lib/evCharging/qr.ts`

Build:

- camera scanner for any QR
- paste/manual input fallback
- provider classifier
- intent state machine
- route options
- local event log
- no network start/stop yet

Verification:

- TypeScript/static checks only if lightweight and already present.
- No mobile build.

### Phase 2 - Remote Android Launch and Supervised Pane

Build:

- detect active Yaver device
- call `/droid/status`
- launch provider package hints via `/droid/launch`
- route to or embed remote Android view
- add user approval events

Use existing `/droid/*` code instead of duplicating adb handling.

### Phase 3 - Provider Adapter Expansion

Build:

- verified domains/package hints for Esarj, ZES, Trugo
- QR fixture tests with user-provided/observed examples
- confidence scoring and unknown-provider fallback

Do not encode unverified package IDs or private endpoint assumptions.

### Phase 4 - Session Tracking and Receipts

Build:

- charging timer
- estimated cost/kWh fields
- manual stop reminder
- local receipt/note attachment
- optional remote Android receipt retrieval

### Phase 5 - Official API Connectors

Only after provider API availability is verified:

- OAuth/API setup
- provider capability flags
- API start/stop with explicit approval
- receipt/status sync

## Open Questions

- Which providers should be first-class in Turkey for v1 beyond Esarj, ZES, and
  Trugo?
- What real QR payloads do those providers emit? Need examples from chargers.
- Do provider apps accept QR/deep links directly, or only in-app camera scans?
- Which Android runtime is expected: physical Android phone, emulator, or Redroid?
- Will remote Android have Play Services and pass provider integrity checks?
- Should Yaver same-phone flow open provider apps on iOS and Android, or only
  Android initially?
- What is the minimum receipt format the user wants?

## Immediate Engineering Rule

The first code change should be a harmless mobile feature surface:

```text
scan/paste QR -> classify -> show intent/routes -> optionally open remote Android
```

No real charging start/stop, no payment automation, no provider credential
storage, and no mobile build unless explicitly requested.
