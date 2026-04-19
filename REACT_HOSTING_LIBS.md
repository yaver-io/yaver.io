# React Native Hosting Libraries — What Yaver Ships vs. What's Missing

**Scope:** mobile super-host only. This is the "native module surface" available to guest
React Native bundles loaded into the Yaver container via Hermes push. Purely-JS libraries
(Redux, Zustand, React Query, Axios, Tailwind/NativeWind JS runtime, date-fns, Zod, etc.)
always work — they don't need a manifest entry and are not tracked here.

**Source of truth:** `mobile/sdk-manifest.json` (76 native modules as of 1.17.27).
**Target:** `mobile/package.json` and iOS/Android Podfile.lock / Gradle must match the
manifest — mismatches trigger the false "missing module" reports SFMG saw.

## 1. What Yaver currently ships (76 modules, RN 0.81.5 / Hermes BC 96 / New Arch on)

### Expo SDK 54 core (60)
`expo`, `@expo/vector-icons`, `@expo/metro-runtime`, `expo-apple-authentication`,
`expo-asset`, `expo-auth-session`, `expo-av`, `expo-background-fetch`, `expo-battery`,
`expo-blur`, `expo-brightness`, `expo-calendar`, `expo-camera`, `expo-clipboard`,
`expo-constants`, `expo-contacts`, `expo-crypto`, `expo-device`, `expo-document-picker`,
`expo-file-system`, `expo-font`, `expo-haptics`, `expo-image`, `expo-image-manipulator`,
`expo-image-picker`, `expo-keep-awake`, `expo-linear-gradient`, `expo-linking`,
`expo-local-authentication`, `expo-localization`, `expo-location`, `expo-mail-composer`,
`expo-media-library`, `expo-notifications`, `expo-print`, `expo-router`,
`expo-screen-capture`, `expo-screen-orientation`, `expo-secure-store`, `expo-sensors`,
`expo-share-intent`, `expo-sharing`, `expo-speech`, `expo-splash-screen`, `expo-sqlite`,
`expo-status-bar`, `expo-system-ui`, `expo-task-manager`, `expo-updates`, `expo-video`,
`expo-video-thumbnails`, `expo-web-browser`.

### Community essentials (16)
`@gorhom/bottom-sheet`, `@react-native-async-storage/async-storage`,
`@react-native-community/datetimepicker`, `@react-native-community/netinfo`,
`@react-native-community/slider`, `@react-native-masked-view/masked-view`,
`@react-native-picker/picker`, `@react-native-segmented-control/segmented-control`,
`@shopify/flash-list`, `@shopify/react-native-skia`, `lottie-react-native`,
`react-native-ble-plx`, `react-native-gesture-handler`, `react-native-get-random-values`,
`react-native-iap`, `react-native-maps`, `react-native-markdown-display`,
`react-native-mmkv`, `react-native-nitro-modules`, `react-native-pager-view`,
`react-native-reanimated@4`, `react-native-safe-area-context`, `react-native-screens`,
`react-native-svg`, `react-native-udp`, `react-native-video`, `react-native-view-shot`,
`react-native-web`, `react-native-webview`, `react-native-worklets`, `victory-native`,
`whisper.rn`.

**Coverage verdict:** strong baseline for 90% of Expo-style apps. Missing gaps are
concentrated in (a) payments/subs, (b) analytics/crash, (c) third-party auth, (d)
advanced camera/ML, (e) a few ergonomic niceties devs reach for in 2026.

## 2. Most popular RN libraries Yaver is currently MISSING

Ranked by real-world developer pull + mindshare (weekly npm downloads and 2025/2026
community polls). "Impact" = how often a dev will fail to onboard to Yaver because of
this gap.

### Tier 1 — must-add (block real apps)

| Library | Why | Weekly DL (approx) |
|--------|----|-------------------|
| **`react-native-vision-camera`** | The dominant camera library for ML / QR / barcode / frame processors. expo-camera covers simple use cases but any team doing scanning, AI frame analysis, or low-level control reaches for this. ~450k/wk. | ~450k |
| **`@react-native-firebase/app`** + `/auth` + `/messaging` + `/analytics` + `/firestore` + `/crashlytics` | Firebase is the #1 BaaS in mobile. Every other React Native template ships with it. Missing this = "can't use Firebase" = instant churn for a big segment. | ~900k (app) |
| **`@react-native-google-signin/google-signin`** | Yaver has Apple Sign-In native but not Google's. `expo-auth-session` works but is inferior UX (web redirect vs. native picker). Almost every auth flow wants both buttons. | ~400k |
| **`@sentry/react-native`** | Dominant crash + error tracking. Essentially every production RN app ships it. | ~600k |
| **`@stripe/stripe-react-native`** | Native PaymentSheet + Apple Pay / Google Pay. Complements (does not replace) react-native-iap — IAP is for App Store subs, Stripe is for physical goods / B2B / one-off card charges. | ~300k |
| **`react-native-purchases`** (RevenueCat) | Mindshare-winner for subscription management on top of StoreKit / Play Billing. Pairs with (or replaces) react-native-iap. | ~150k |

### Tier 2 — high-value adds (common papercuts)

| Library | Why |
|--------|----|
| **`posthog-react-native`** | Analytics + feature flags + session replay — now the default for product-led teams. |
| **`react-native-keyboard-controller`** | Form-heavy apps can't reasonably live without this in 2026; solves the iOS-Android keyboard-avoiding divergence cleanly. |
| **`react-native-reanimated-carousel`** | Most-starred carousel; only needs Reanimated which Yaver already has. |
| **`react-native-qrcode-svg`** | Trivially pairs with `react-native-svg` (already shipped); very common paired with camera. |
| **`react-native-toast-message`** / **`sonner-native`** | Toast UX. Currently guest apps have to roll their own. |
| **`react-native-modal`** | Rich modal — much better than the built-in `Modal`. Extremely popular. |
| **`@notifee/react-native`** | Advanced local + styled notifications beyond `expo-notifications`. Used heavily where Expo's surface isn't enough. |
| **`@rnmapbox/maps`** | Mapbox alternative to Google/Apple maps — teams choose one or the other. |
| **`react-native-permissions`** | Granular cross-platform permission prompts; some teams prefer this over Expo's per-module APIs. |
| **`react-native-device-info`** | Deeper metadata than `expo-device` (carrier, IP, fingerprint, etc.). |
| **`react-native-pdf`** | PDF viewing — no built-in equivalent; common in B2B apps. |
| **`react-native-share`** | Richer than `expo-sharing` (social-specific targets, WhatsApp/Instagram channels). |
| **`@intercom/intercom-react-native`** | Customer support / in-app messaging — extremely common in SaaS apps. |
| **`amplitude-react-native`** / **`@amplitude/analytics-react-native`** | Analytics alternative to PostHog / Firebase. |
| **`@shopify/restyle`** | Design-system engine for teams that need strict typed themes. |

### Tier 3 — ecosystem-nice (rarely a blocker)

| Library | Why |
|--------|----|
| `@react-native-menu/menu` | Native iOS/Android context menus. |
| `react-native-background-geolocation` (Transistor Soft) | Heavy-duty background GPS — paid, but the standard for fleet/rideshare apps. |
| `react-native-background-actions` | Long-running background work beyond Expo's offering. |
| `react-native-audio-api` | Native audio synthesis / processing. |
| `react-native-nitro-image` | Nitro-Modules-based image library (Yaver has nitro-modules). |
| `react-native-orientation-locker` | Redundant with `expo-screen-orientation` but occasionally imported directly. |

### Tier 4 — JS-only, already works (do NOT add to manifest)

These have zero native code — they ride on what Yaver already ships. Listing them so we
don't waste a manifest slot or build time:

- Styling: **NativeWind**, **Tamagui** (JS runtime; their compiler steps happen at
  bundle-time via Metro plugins — compatible), **Restyle** (JS-only; type system)
- State: Redux Toolkit, Zustand, Jotai, Recoil, Valtio
- Data: TanStack Query, SWR, Apollo Client, Relay
- HTTP: Axios, Ky, Wretch, ofetch
- Forms: React Hook Form, Formik, Yup, Zod
- Navigation helpers: `@react-navigation/*` (pure JS; rides on `react-native-screens`
  + `react-native-safe-area-context` which Yaver ships)
- Dates: date-fns, dayjs, luxon, Temporal polyfills
- UI (JS-only): **React Native Paper** (uses react-native-vector-icons which we ship),
  **NativeBase** (rides on gesture-handler + reanimated + svg — all shipped),
  **Gluestack v3** (NativeWind-based, JS-only)
- i18n: i18next, react-i18next (expo-localization shipped)
- Testing: Jest, Detox client (only needs its native pod for e2e — not in-app)

**Note on `react-native-paper` / `NativeBase` / `Gluestack`:** these *are* UI kits but
they are fundamentally JS components built on top of native primitives. They work inside
Yaver today without any manifest change as long as the primitives they depend on
(reanimated, gesture-handler, svg, vector-icons, safe-area-context) are present — which
they are.

## 3. Why the SFMG false-negative happened (and won't repeat)

SFMG's compatibility check hit a stale detection path in the agent that didn't know about
recently-added modules like `react-native-nitro-modules`, `react-native-mmkv`,
`react-native-reanimated@4`, `react-native-worklets`. The 34fc0b69 fix wires manifest-backed
reporting so the host tells the truth about what it has. Going forward, adding a module =
(a) `mobile/package.json`, (b) iOS pod + Android gradle, (c) `mobile/sdk-manifest.json`
(all four copies — see CLAUDE.md §SDK Manifest Contract), (d) `cli/sdk-manifest.json`.

## 4. Recommended prioritization for the next manifest bump

Do not batch all of Tier 1 in a single release — each one widens the native binary and
increases the chance of a build regression. Suggested order:

1. **`@sentry/react-native`** — zero surface-area change for guests, huge operator win
   (every SDK-error the SFMG / talos / botox teams hit could be routed to Sentry instead
   of shake-feedback). Low risk.
2. **`@react-native-google-signin/google-signin`** — unblocks every auth flow that wants
   a Google button next to the existing Apple button.
3. **`react-native-vision-camera`** — opens ML / scanning apps. Watch for iOS frame
   processor interop with existing expo-camera; both can coexist.
4. **`@react-native-firebase/app` + `auth` + `messaging` + `analytics` + `firestore` +
   `crashlytics`** — large binary impact but unlocks a big chunk of the market. Ship as
   a single Firebase bundle so guests can use it coherently.
5. **`@stripe/stripe-react-native`** then **`react-native-purchases`** — revenue unlock
   for guest apps, after the basics.
6. Tier 2 items can be folded in opportunistically.

Each addition needs:
- `mobile/package.json` + `expo prebuild`
- Pod install on iOS / Gradle sync on Android
- `mobile/sdk-manifest.json` + 3 mirrors (see CLAUDE.md §SDK Manifest Contract)
- `cli/sdk-manifest.json`
- A bump of `sdkVersion` in the manifest so older CLIs know to warn
- A line in the release notes for the mobile TestFlight / Play build

## 5. Quick self-check for "is this lib compatible with Yaver?"

```
Is it a React Native library?
├─ No → doesn't apply
└─ Yes
   │
   ├─ Does it have a native iOS/Android pod-spec / gradle module?
   │   ├─ No (pure JS) → always compatible, never needs manifest entry
   │   └─ Yes
   │
   ├─ Is it listed in mobile/sdk-manifest.json?
   │   ├─ Yes → supported; hermes BC must match
   │   └─ No → currently unsupported
   │
   ├─ Does it use `TurboModuleRegistry.getEnforcing()` or Fabric views?
   │   └─ Must be added via ExpoReactNativeFactory + RCTAppDependencyProvider
   │      (already wired for all existing modules; new modules get it automatically)
   │
   └─ Does it ship ≥ 20 MB of native assets?
       └─ Flag for release review — Yaver mobile app binary budget
```
