# QR Pairing Rollout Plan

## Goal

Add a first-class QR-driven pairing flow for Yaver devices, especially headless
Go agents and the Raspberry Pi image, without breaking any existing working auth
or recovery path.

The desired user experience is:

1. A Yaver box boots or enters pairing mode.
2. The console prints a QR code and a plain HTTPS URL.
3. The Yaver mobile app scans the QR.
4. If the user is already signed in, pairing completes with one confirmation.
5. If the user is not signed in, the app or web handoff completes OAuth/email login.
6. If the account has 2FA enabled, a TOTP / recovery-code challenge is completed.
7. The phone submits the bearer token to the agent and the device is adopted.

## Existing Working Pieces

### Agent

- `desktop/agent/devicecode.go`
  - Headless device-code auth already exists.
  - A terminal QR is already printed for `https://yaver.io/auth/device?code=...`.
- `desktop/agent/auth_pair.go`
  - One-shot pair sessions already exist.
  - `/auth/pair/info` and `/auth/pair/submit` are stable.
  - `yaver auth pair` prints a passkey and reachable URLs.
- `desktop/agent/auth_bootstrap.go`
  - Bootstrap pairing for unauthenticated `yaver serve` already exists.
  - LAN beacon + `/info` + `/auth/pair/*` already support no-token adoption.
- `desktop/agent/auth_recover.go`
  - Recovery already supports pair mode and device-code mode.
- `desktop/agent/beacon.go`
  - LAN beacon already broadcasts `needsAuth`, passkey, and optional encrypted-pair hints.

### Mobile

- `mobile/src/lib/pairDevice.ts`
  - Signed-in mobile app can already submit a token to `/auth/pair/submit`.
- `mobile/src/context/DeviceContext.tsx`
  - Auto-pair and auth-recovery flows already exist.
- `mobile/src/lib/quic.ts`
  - Recovery can already open pair mode or device-code mode remotely.

### Web / Backend

- `web/app/auth/device/*`
  - Device-code authorization page already exists.
- `web/app/api/auth/oauth/*`
  - OAuth providers already exist:
    - Apple
    - Google
    - Microsoft / O365
    - GitHub
    - GitLab
- `mobile/src/lib/auth.ts`
  - Mobile already understands these providers.
- `backend/convex/totp.ts`
  - Optional TOTP-backed 2FA already exists.
- `desktop/agent/two_factor_cmd.go`
  - CLI setup already supports Microsoft Authenticator / Google Authenticator /
    1Password / Authy style apps.

## Constraints

1. Do not break:
   - `yaver auth --headless`
   - `yaver auth pair`
   - bootstrap pairing
   - auth recovery
   - existing sessions
   - optional TOTP 2FA
2. Do not require custom camera URI-scheme handling.
   - Use `https://yaver.io/pair?...`, not a `yaver-pair://...` QR target.
3. Keep 2FA scoped to session issuance only.
   - Do not add new per-request or per-device 2FA semantics.
4. Keep current direct-submit and device-code flows as fallbacks.

## Implementation Audit

This section maps the proposal to the current code so the first slice is scoped
against reality, not just desired UX.

### Current Agent Reality

- `desktop/agent/auth_pair.go`
  - Pairing is currently keyed by a single global in-memory `activePairing`.
  - The trust secret is the 6-character `Code`.
  - `GET /auth/pair/info` returns only `host` and `expiresAt`.
  - `POST /auth/pair/submit?code=...` consumes the code and stores the token.
  - There is no pair-session ID, no pair URL builder, and no QR output in
    `yaver auth pair`.
- `desktop/agent/auth_bootstrap.go`
  - Bootstrap pairing already reuses the same one-shot pair session machinery.
  - Any new QR pair URL must work for bootstrap mode without changing current
    `/info` or `/auth/pair/submit` semantics.
- `desktop/agent/auth_recover.go`
  - Recovery can open either `pair` mode or `device-code` mode.
  - Recovery pair mode also reuses the same global `activePairing` session.
  - This means any move from `code` to `sid` is not a local change to
    `yaver auth pair`; it touches recovery and bootstrap too.
- `desktop/agent/devicecode.go`
  - Device-code auth already has durable pending state on disk and already
    prints a plain URL plus terminal QR.
  - Device-code is the existing fallback path and already handles the
    signed-out + OAuth + 2FA case end to end.

### Current Mobile Reality

- `mobile/src/lib/pairDevice.ts`
  - Signed-in mobile pairing today is direct: `{token, convexSiteUrl, userId}`
    is POSTed to `/auth/pair/submit?code=...`.
  - The current mobile path assumes the user already has a valid session token.
- `mobile/app/login.tsx`
  - OAuth deep links currently end at `yaver://oauth-callback?token=...` and the
    screen routes the user into the app home flow.
  - There is no existing continuation payload for "resume pending pair session".
- `mobile/app/two-factor-challenge.tsx`
  - 2FA verification currently ends by logging in and routing home.
  - There is no existing continuation payload for "finish pairing after TOTP".
- `mobile/src/context/DeviceContext.tsx`
  - Auto-pair and recovery already know how to submit a token once one exists.
  - That means the signed-in QR slice is small, but signed-out continuation is
    materially larger.

### Current Web Reality

- `web/app/auth/page.tsx`
  - Web sign-in already preserves a `return` parameter and can bounce the user
    back to a web route after OAuth or email/password login.
  - Today this is primarily used for `/auth/device`.
- `web/app/api/auth/oauth/[provider]/callback/route.ts`
  - OAuth callback has explicit special handling for device-code authorization.
  - Outside the device-code case, the callback just creates a session and
    redirects to web callback or mobile deep link.
  - There is no first-class pair-session continuation yet.
- `web/app/auth/totp/page.tsx`
  - Web TOTP already preserves `client` and `return`, but only returns the user
    to a route after session issuance.
  - A pair flow still needs a route or deep link that knows how to resume.

### Hidden Scope Callouts

1. Introducing `sid` is a data-model change, not just a new endpoint.
   - Current pair flows are code-keyed and global.
   - If we add `sid`, we must define whether `code` remains the real submit
     secret, whether `sid` is only a locator, and how bootstrap/recovery reuse
     the same in-memory session.
2. `requiresAuth` cannot be derived by the unauthenticated agent endpoint.
   - Whether the scanning phone is already signed in is client-local state.
   - The agent can report "pair session exists and accepts direct submit"; it
     cannot truthfully report whether the current scanner already has a session.
3. OAuth continuation is not automatic.
   - Mobile login, mobile 2FA, web OAuth callback, and web TOTP each currently
     terminate at "you are signed in".
   - Pair resumption needs explicit context fields and explicit resume handlers.

### Recommended Scope Split

#### Slice A: Signed-In QR Pairing

Ship this first:

1. agent prints canonical HTTPS pair URL
2. agent prints QR for that URL
3. mobile can parse the URL
4. signed-in mobile resolves metadata and submits token

This slice should not require OAuth continuation changes.

#### Slice B: Signed-Out Continuation

Ship this second:

1. web `/pair` route
2. mobile deep-link continuation payload
3. web OAuth callback continuation
4. web TOTP continuation
5. mobile login / mobile 2FA continuation

This is the slice that actually makes "scan while signed out" work.

## Product Model

The new pairing layer is additive and first-class.

The QR path is a new entrypoint, not a replacement for the existing pairing
contract.

Specifically:

- keep `yaver auth pair` passkey pairing working exactly as it does today
- keep `/auth/pair/info` and `/auth/pair/submit?code=...` valid
- keep bootstrap pairing valid
- keep auth recovery pair mode valid
- keep device-code auth as a separate working path
- treat QR as a wrapper that helps users reach the existing pair session faster
- allow QR as an optional convenience in other auth surfaces too:
  - web auth UI
  - headless device-code OAuth
  - recovery/device-code handoff
- never make QR the only entrypoint on any surface that already works with a
  plain URL, code, or button flow

### Canonical Pairing Modes

1. `instant-pair`
   - User already signed in on mobile.
   - Scan QR and submit token immediately.

2. `signin-then-pair`
   - User scans QR while signed out.
   - App/web offers provider choice.
   - On success, app resumes the pending pairing session and submits token.

3. `2fa-then-pair`
   - Same as `signin-then-pair`, but account requires TOTP.
   - Login flow shows TOTP/recovery-code challenge before pairing continues.

4. `recovery-pair`
   - Agent auth expired or device lost auth.
   - Pair session is opened via recovery and the app restores auth.

5. `device-code-fallback`
   - If pair-submit cannot complete, fall back to the existing device-code page.

## UX

### Console UX

For `yaver auth pair`, bootstrap mode, and Pi image first boot:

- print a plain HTTPS pairing URL
- print a terminal QR code for that URL
- keep printing the short passkey
- keep printing raw reachable URLs as fallback

Console should say:

- "Scan with Yaver mobile to pair this device"
- "Already signed in? Scan and confirm"
- "Not signed in? Scan and choose Apple / Google / Microsoft / GitHub / GitLab / email"
- "If your account uses 2FA, complete the authenticator challenge on your phone"

For headless OAuth / device-code auth:

- keep printing the plain `https://yaver.io/auth/device?code=...` URL
- keep printing the device code text
- keep printing the terminal QR
- treat the QR as a convenience for phone handoff, not a replacement for the
  existing URL + code flow

### Mobile UX

Entry points:

- QR scan from app
- deep link from web pairing URL

Flow:

1. parse pairing payload
2. fetch session metadata
3. show device summary
4. if signed in, continue
5. if signed out, offer provider buttons
6. if 2FA required, show TOTP / recovery-code challenge
7. submit pair token
8. show success and open device

### Web UX

If the QR is scanned with the system camera instead of the app:

- `https://yaver.io/pair?...` opens a web route
- web route attempts deep-link into app
- fallback page explains:
  - open in Yaver app
  - or continue sign-in in browser then handoff

For web auth UI more broadly:

- QR may be shown as an optional "continue on phone" affordance
- existing provider buttons, email/password form, and direct browser flow stay
  primary and fully functional without QR

## Technical Design

### Phase 1: Canonical Pair URL

Add a canonical Yaver pairing URL generated by the agent:

- base: `https://yaver.io/pair`
- params:
  - `sid`: pairing session id
  - `mode`: `pair` | `bootstrap` | `recovery`
  - `device`: device id or short id
  - `host`: display hostname
  - `target`: preferred target URL
  - `exp`: expiry timestamp
  - `code`: optional short passkey fallback

The URL is not the trust anchor by itself; it is a locator + session handle.
The trust anchor remains the short-lived one-shot pair session and/or the
existing secure submit path.

For the first slice, the QR URL should resolve back onto the existing pair
session model rather than replacing it.

### Phase 2: Pair Session Metadata Endpoint

Add an agent endpoint that returns normalized pairing session metadata:

- `GET /auth/pair/session?...`

Recommended first-slice lookup rules:

- support lookup by existing `code`
- optionally accept `sid` later once a real session-id model exists
- keep the first slice compatible with the current single active in-memory pair
  session

Suggested fields:

- `ok`
- `sessionId` if present
- `mode`
- `hostname`
- `deviceId`
- `expiresAt`
- `canDirectSubmit`
- `targetUrls`
- `supportsEncryptedPair`

This endpoint is read-only and must not reveal more than existing `/auth/pair/info`
already reveals plus session routing data.

`requiresAuth` is intentionally omitted here. Whether the phone is already
signed in is client-local state, not something the unauthenticated agent can
determine.

This endpoint is additive to the current surface. It must not replace or change
the meaning of `/auth/pair/info` for existing clients.

### Phase 3: Agent QR Output

Update:

- `runAuthPair`
- bootstrap console
- optionally recovery/device-code display

to print:

- pairing URL
- QR for pairing URL
- raw passkey
- raw direct CLI fallback

This does not remove the current passkey flow.
It also does not change the existing `yaver auth send <code> <target-url>`
workflow; QR is just a faster on-ramp into the same pairing window.

For `yaver auth --headless` / device-code auth, keep the current QR behavior
and apply the same additive rule:

- plain URL remains mandatory output
- typed user code remains mandatory output
- QR remains optional convenience output for humans at a real terminal

### Phase 4: Mobile Deep Link / QR Handling

Add a mobile entrypoint for:

- scanned QR `https://yaver.io/pair?...`
- web-to-app deep link continuation

The mobile app should:

- parse the payload
- call session metadata endpoint
- route to a dedicated pairing screen

### Phase 5: OAuth Continuation

After user authenticates via:

- Apple
- Google
- Microsoft / O365
- GitHub
- GitLab
- email/password

the auth flow must resume the pending pairing session instead of stopping at
"you are signed in".

This means preserving pairing context through:

- mobile OAuth redirect
- web OAuth callback
- browser fallback flow
- email/password web login
- mobile email/password login

Concrete implementation requirement:

- define one canonical continuation payload for pair resume
- carry it through OAuth state, TOTP pending flow, and app deep links
- make the final callback target perform "resume pair submit" instead of routing
  home immediately

### Phase 6: 2FA Continuation

Keep current TOTP semantics:

- 2FA only happens if account has TOTP enabled
- TOTP challenge occurs at login/session issuance
- accepted authenticators:
  - Microsoft Authenticator
  - Google Authenticator
  - 1Password
  - Authy
  - equivalent TOTP apps
- recovery codes remain supported

Do not create a second device-specific 2FA system.

Concrete implementation requirement:

- web `auth/totp` must preserve the pair continuation payload
- mobile `two-factor-challenge` must preserve the pair continuation payload
- successful verification must resume pairing, not just mark the user signed in

### Phase 7: Pi Image UX

The Pi image should show QR pairing from first boot.

Because the image already auto-updates Yaver and first-boot installs the heavy
stack, we should not require a new image for every pairing improvement.

Preferred long-term model:

- image is stable
- first boot installs current stack
- Yaver binary and pairing flow auto-update via the existing image timer

So future Go-agent pairing improvements should usually land via Yaver update,
not a full new Pi image.

## No-Regression Rules

1. Existing `yaver auth --headless` stays unchanged and available.
2. Existing `/auth/pair/submit` stays valid.
3. Existing mobile recovery flow stays valid.
4. Existing bootstrap beacon adoption stays valid.
5. Existing TOTP setup/disable/status stays valid.
6. New QR flow is layered on top, never a hard replacement.
7. Any web UI QR affordance must be optional; provider buttons and email/password
   login remain complete without scanning.
8. Headless device-code auth keeps working with the printed URL and typed code
   even if the QR is ignored.

## Initial Implementation Slice

The first implementation slice should be:

1. add canonical pair URL builder in agent
2. keep the current 6-character code as the submit secret
3. print that URL + QR in `auth pair` and bootstrap mode
4. add a lightweight session metadata endpoint
5. add a mobile QR/deep-link parser for `yaver.io/pair`
6. route scanned sessions to current signed-in pair submit where possible

Guardrails for this slice:

- do not require OAuth continuation
- do not require TOTP continuation
- do not remove `/auth/pair/info`
- do not remove `/auth/pair/submit?code=...`
- do not remove or downgrade printed passkey instructions
- do not remove or downgrade printed reachable target URLs
- do not change the behavior of `yaver auth send`
- do not change recovery `pair` mode semantics
- do not change bootstrap pairing semantics
- do not change headless device-code semantics
- if `sid` is introduced, treat it as a locator while `code` remains the submit
  proof for the first slice

This gives immediate UX value while leaving signed-out continuation as the next
slice.

## Follow-Up Slice

1. web `yaver.io/pair` route
2. OAuth continuation through pairing session
3. 2FA challenge continuation
4. polished mobile pairing screen

Implementation checklist for this slice:

1. define pair continuation shape
2. propagate it through web OAuth state
3. propagate it through mobile OAuth deep link
4. propagate it through web TOTP
5. propagate it through mobile TOTP
6. land on a final resume handler that submits the token to the agent or falls
   back to device-code

## Release Strategy

Because the Pi image now auto-updates Yaver and installs the heavy tool stack
at first boot, this feature should be shipped as:

- Go agent update
- mobile app update
- web update
- docs update
- optional Pi image refresh if we want the console UX baked into the latest public image

But the long-term goal is:

- future pairing improvements should not require a new base Pi image every time
- the image should fetch and run the latest suitable Yaver release automatically

Release acceptance rule:

- a user who ignores the QR entirely must still be able to complete pairing
  through the current passkey and device-code fallbacks exactly as before
- a user on the web UI must still be able to complete auth without scanning
  anything
