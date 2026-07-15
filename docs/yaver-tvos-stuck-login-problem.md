# tvOS stuck-login — QR sign-in never completes

Handoff for the next session. Everything below was checked against the code on
2026-07-15, not recalled. The bug is real and reproducible; the fix is scoped
but not yet built.

## Symptom (user repro, verbatim)

> I downloaded Yaver to the TV and the phone, both signed out. Opened the TV, it
> showed a QR. I scanned the QR with my phone camera, it opened the Yaver mobile
> app, I did Apple sign-in there, the phone signed in — but the **Apple TV stayed
> stuck** at the QR / "waiting for approval" stage.

So: the phone ends up authenticated, the TV never does. The device code is
never approved.

## The flow, and where it breaks

The TV shows a QR encoding `https://yaver.io/auth/device?code=XXXX-YYYY`
(`tvos/YaverTV/Backend.swift` `DeviceCodeStart.verifyURL`). On iOS this is a
universal link (`web/public/.well-known/apple-app-site-association` maps
`/auth/device*` to `io.yaver.mobile`), so scanning opens the **mobile app**, not
Safari.

The mobile approver route exists: `mobile/app/auth/device.tsx` (added in
`662edc3e8`, so it IS in TestFlight build 436). It reads `?code=`, GETs
`/auth/device-code/info`, and on Approve POSTs `/auth/device-code/authorize`
with the phone's bearer token. When the phone is **already signed in**, this
works.

**The break is the signed-OUT path.** When the QR opens `/auth/device?code=X`
on a phone with no session:

1. `app/auth/device.tsx:109-113` calls `getToken()`, gets null, and just shows
   *"You're not signed in on this phone. Sign in first, then scan the code
   again."* — it does **not** stash the code.
2. The user signs in with Apple. The OAuth flow hardcodes
   **`returnTo: "/dashboard"`** (`mobile/src/lib/auth.ts:897`), so after auth the
   app lands on the dashboard/Tasks tab.
3. The `?code=` from the QR is now **gone**. The approver never runs again. The
   TV's `/auth/device-code/poll` loop keeps returning `pending` forever.

There is **no pending-deep-link persistence** across sign-in anywhere in the app
(grepped `pendingDeepLink|returnTo|postLoginRedirect|deferredLink` — only the
hardcoded OAuth `returnTo` exists). So any deep link that requires auth is lost
the moment the user has to sign in.

## What must work (three cases)

1. **Signed-out phone, scan QR** → sign in → **resume approval automatically**
   with the code carried across sign-in. (Today: stuck.)
2. **Signed-in phone, scan QR with the camera** → approver opens → one-tap
   approve → TV signs in. (Today: works, via `app/auth/device.tsx` — needs a
   re-verify on a real device to be sure the universal link resolves the route
   and not the Tasks fallback.)
3. **Signed-in phone, scan QR from INSIDE the app** → the user wants an in-app
   "Scan QR" affordance so they don't have to leave the app and use the camera
   roll. (Today: does not exist — no scanner in the app.)

## Fix plan

### A. Carry the device code across sign-in (fixes case 1 — the actual stuck bug)

- In `app/auth/device.tsx`, when `getToken()` is null: **stash the code**
  (`AsyncStorage.setItem("pendingDeviceCode", code)`) and route to sign-in
  instead of dead-ending on "scan again".
- After a successful sign-in, check for `pendingDeviceCode`: if present, navigate
  back to `/auth/device?code=<stashed>` (or call the approve directly) and clear
  it. Best hook: the post-auth landing (`AuthContext` success path, or wherever
  `returnTo` is honored).
- Make the OAuth `returnTo` (`auth.ts:897`) carry the intended destination
  instead of a hardcoded `/dashboard` when a pending device code exists — e.g.
  `returnTo: "/auth/device?code=" + code`.
- Result: signed-out scan → Apple sign-in → app returns to the approver → one
  tap (or auto-approve) → TV signs in.

### B. In-app QR scanner for the authenticated path (fixes case 3)

- Add a "Scan a device code" entry (e.g. in the Remote Box picker header or
  More tab). Use `expo-camera`'s barcode scanner (already an Expo app; confirm
  `expo-camera` is a dep or add it) to read the QR.
- Parse the scanned URL for `?code=` and route to `/auth/device?code=…` — reuses
  the existing approver. No new backend.
- Because the user is already authed here, it flows straight to Approve.

### C. Re-verify the authenticated camera path (case 2) on device

- On a real iPhone with the app signed in, scan the TV QR and confirm iOS
  resolves the universal link to `app/auth/device.tsx` (not the Tasks tab). If it
  lands on Tasks, the AASA/route matching needs a look — but the route file is
  present and correct, so this is a verify step, not assumed-broken.

## Files

| Concern | File |
|---|---|
| Approver route (exists) | `mobile/app/auth/device.tsx` |
| OAuth returnTo (hardcoded /dashboard — fix) | `mobile/src/lib/auth.ts:897` |
| Token accessor | `mobile/src/lib/auth.ts` `getToken` |
| Universal-link mapping | `web/public/.well-known/apple-app-site-association` (`/auth/device*`) |
| TV QR source | `tvos/YaverTV/Backend.swift` `DeviceCodeStart.verifyURL`, `tvos/YaverTV/Views/SignInView.swift` |
| Approve/info/poll endpoints | `backend/convex/http.ts` `/auth/device-code/{authorize,info,poll}`, `backend/convex/deviceCode.ts` |

## Note on the TV side

The tvOS `SignInView` is already correct: it polls every 5s, shows "Waiting for
approval…", and signs in the moment the code is approved (verified — I approved a
code by hand during the release and the TV picked it up in ~5s). The TV is not
the bug; the phone-side approval never fires in the signed-out case. No tvOS
change is needed for this fix — it's entirely mobile-app + the OAuth returnTo.
