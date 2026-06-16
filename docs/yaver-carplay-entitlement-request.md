# CarPlay entitlement request — draft justification

> Status: draft (2026-06-17). Companion: `docs/yaver-tv-car-deployment-roadmap.md`,
> `docs/yaver-ev-charging-turkey.md`. **You** submit this (Apple form, your identity);
> I can only prepare the text. Without a granted entitlement the CarPlay scene never loads.

## Which entitlement, in what order

1. **`com.apple.developer.carplay-charging`** — lead with this. Real charging apps get it; it's
   templated (`CPListTemplate` / `CPPointOfInterestTemplate` / `CPMapTemplate`); Yaver has a
   genuine EV charging surface (discovery + navigate + status, and start/stop via the private
   driver behind the seam). **Highest approval odds.**
2. `com.apple.developer.carplay-driving-task` — optional, later. Most permissive door; the
   CarPlay analog to Android's IoT for a *narrow* device-control glance. Apple still judges
   "appropriate while driving."
3. `com.apple.developer.carplay-communication` — optional, **strictest** review (Apple wants
   proof you're a genuine messaging platform). This is the voice-coding-as-messaging path. Do
   **not** lead with it; it slows the whole request.

## Draft text for the request form (charging)

> **App name:** Yaver
> **CarPlay app type:** EV Charging
> **What the app does in CarPlay:** Yaver helps EV drivers find and navigate to charging
> stations and view connector/power/availability while driving, using Apple-provided CarPlay
> templates only (list, point-of-interest, map). Drivers filter by connector (CCS2/Type 2),
> minimum power, and network. For chargers the user operates themselves, the app can also start
> and stop a session.
> **Why it needs the charging entitlement:** the in-car experience is exclusively EV-charging
> discovery, navigation, and session status — it does not present any general-purpose or
> distracting UI. All interaction is via standard CarPlay templates and voice.
> **Driver-distraction handling:** no custom rendering; list/POI/map templates only; no text
> entry while moving; status is summarized and can be read via Siri/TTS.
> **Regions / networks:** launch market Turkey (Trugo, ZES, Eşarj, Sharz.net, Voltrun, …) with
> CCS2/Type 2 coverage; station data via OpenChargeMap and (self-hosted, user-authorized) the
> user's own network accounts.

## Technical checklist before/after the grant
- [ ] Add `CPTemplateApplicationScene` to the iOS app's `Info.plist` scene manifest.
- [ ] Add the granted entitlement to the app's `.entitlements`.
- [ ] Drive templates from JS via **`react-native-carplay`** (no full Swift rewrite).
- [ ] Templates: charging-station list → POI detail (connectors/power/price/availability) →
      map/navigate handoff. Start/stop button only renders when a `ChargeController` is
      registered (private driver) — otherwise hidden/deep-link to the network app.
- [ ] Test in the iOS Simulator's CarPlay environment with a development entitlement first.
- [ ] No text entry while in motion; all actions template- or voice-driven.

## Realistic timeline
- Entitlement decision: days to a couple of weeks after submission.
- **The Android side (Android Auto charging + messaging) has no entitlement gate and ships
  first** — don't block the EV car work on Apple.
