# Yaver Store Publishing

The code → published-app pipeline for third-party ("normie") developers. Yaver
derives everything it can from the app's code, does the automatable parts, and
routes the human-only parts to the exact Apple/Google page. **Source of truth is
the Go code in `desktop/agent/` — this doc is a map; grep the code before acting
on it.**

## Design principles

- **Derive from code.** Permissions, privacy/Data-Safety, and listing facts come
  from the app's dependencies + config — not a questionnaire the user guesses at.
- **auto / assisted / manual, honestly.** Account creation has no API and is
  identity/payment-gated, so it's `manual` + routed. We never claim to automate
  what we can't (or what a store's ToS forbids — no shared accounts).
- **Generate config, not artifacts.** Permissions are written to `app.json` /
  Expo config (regen-safe), never raw `Info.plist`/`AndroidManifest` (which
  `expo prebuild --clean` overwrites).
- **Truthful privacy.** App Privacy / Data Safety are derived from the SDK graph
  so the declaration matches actual behaviour (auditable, not hand-guessed).
- **Safe by default.** Live store writes are guarded behind `--apply` and never
  blind; keystore generation refuses without a writable vault (a lost random
  keystore password = you can never update the app).
- **Secrets are vault-only.** Signing keys / store creds live in the encrypted
  vault, never in Convex (privacy contract).

## Commands

| Command | What |
|---|---|
| `yaver stores [id] [--json]` | Onboarding concierge: account, keys, TestFlight, IAP, sign-in — each tagged auto/assisted/manual + the official route URL |
| `yaver caps scan\|list [--json]` | Infer required iOS/Android permissions from `package.json` deps |
| `yaver caps generate [--write]` | Merge inferred Info.plist usage strings + Android permissions into `app.json` (additive, preserves your edits) |
| `yaver keys init --platform android\|ios` | Generate the Android upload keystore (keytool) or iOS distribution key + CSR → vault |
| `yaver keys sha1 \| signin-google` | Print the keystore SHA-1 (+ package) to paste into Google Cloud |
| `yaver listing [--json]` | Canonical `StoreListing` derived from code (identity + truthful privacy) |
| `yaver listing draft` | AI marketing copy, grounded on detected capabilities (can't invent features) |
| `yaver listing push --store apple\|google [--live]` | Push plan (API vs Console split); `--live` verifies creds + connectivity |
| `yaver listing status` | One "ready to ship?" verdict (blockers vs warnings) |
| `yaver listing plan` | The exact ordered list of commands to reach shippable |
| `yaver assets plan\|capture` | Capture native-size screenshots (simulator/redroid) + compose the feature graphic |
| `yaver doctor build --target testflight\|playstore` | Toolchain + **permissions preflight** (missing iOS usage strings block the deploy) |

## Agent HTTP (for web/mobile UIs)

`GET /stores`, `GET /capabilities`, `GET /listing`, `GET /publish/status` — all
authed, all reading the same Go source of truth. The web (`StoresView.tsx`) and
mobile (`app/stores.tsx`) render one **Publish** surface (Setup · Permissions ·
Listing) with a "Ready to ship?" banner, routing to official pages for the
human-only steps.

## What's automated vs routed

| Step | Apple | Google |
|---|---|---|
| Enroll account | manual + route ($99/yr, ID) | manual + route ($25, ID) |
| Signing material | `yaver keys` (CSR → cert via ASC API) | `yaver keys` (keytool keystore) |
| TestFlight / internal | `yaver deploy ship --target testflight` | `… --target playstore` |
| Listing text | ASC API (auto) | Play API (auto) |
| Screenshots | ASC `appScreenshotSets` | Play `edits.images` |
| App Privacy / Data Safety | ASC `appDataUsages` (auto) | **Console** (drafted + routed) |
| Age / content rating | ASC `ageRatingDeclaration` (auto) | **Console** IARC (drafted + routed) |
| IAP products | ASC API after Paid-Apps agreement (manual) | Play API after merchant profile (manual) |
| Sign in with Apple / Google | Services ID + key / OAuth clients (assisted) | — |

## Source files

`desktop/agent/`: `setup_guide.go` (concierge), `capabilities.go` +
`caps_generate.go` (permissions), `store_listing.go` (model + truthful privacy),
`store_copy.go` (AI copy), `store_projectors.go` (ASC/Play plan + ES256 JWT),
`store_push_live.go` (creds + auth verify; RS256 Google grant), `store_assets.go`
+ `store_compositor.go` (screenshots + feature graphic), `keys_cmd.go` (signing),
`doctor_permissions.go` (preflight), `publish_status.go` + `publish_plan.go`
(readiness + action plan).

## Honest constraints / not-yet-done

- **Live store writes** (`--apply`) and **asset upload** are gated and NOT
  enabled until verified against a real test account (won't blind-write a live
  listing).
- **iOS cert from CSR** via the ASC API needs real ASC creds to exercise.
- **AI copy** needs `YAVER_GATEWAY_URL` + auth; falls back to printing the prompt.
- Store **review** is still Apple/Google's — Yaver gets you to "submit"
  perfectly prepared, not past review.
