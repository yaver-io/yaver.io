# Mobile App Permissions + Policy Generator

## Goal

When a user starts a new mobile-first fullstack project from the Yaver mobile app, the initialization flow should explicitly ask:

- which native mobile capabilities the app will use
- the short human-readable reasons for those permissions
- the minimum legal/policy inputs needed for launch

Those answers should immediately drive generated outputs so the user does not discover missing plist text, missing policy pages, or missing review notes at App Store / Play submission time.

This is primarily a **mobile app feature**, but it should also generate aligned legal/policy surfaces for web and landing surfaces when those are included in the project scaffold.

## Product Intent

The current risk is:

- user creates a project from phone
- project scaffolds fine
- later, during submission, they realize:
  - Info.plist usage descriptions are missing or weak
  - Android permissions are not justified
  - privacy policy / terms do not exist
  - store review notes are not prepared

The fix is to treat permissions + policies as part of **initialization**, not release cleanup.

## UX Placement

This should live inside the mobile app builder / onboarding / initialization flow.

In the phone UI, add clear grouped sections:

- `Permissions`
- `Policies`

Do not present this as generic backend config. Frame it as:

- "What mobile features will your app use?"
- "Why do you need each permission?"
- "Who publishes this app and what legal contact should appear in the policy?"

## Required Questions

### Mobile capability toggles

Ask boolean questions for:

- camera
- photo library / gallery (read)
- photo library / gallery (save, conditional on read)
- microphone
- location (when-in-use)
- location (always / background, conditional on when-in-use)
- Bluetooth / BLE
- notifications
- App Tracking Transparency / IDFA (third-party tracking SDKs)

For each capability enabled, ask one short follow-up text input for the usage reason. These answers should be short, App-Store-friendly sentences.

Examples:

- "Scan QR codes to pair nearby devices."
- "Attach screenshots to support requests."
- "Save generated images to your photo library when you tap Export."
- "Record voice prompts for on-device transcription."
- "Discover and connect to nearby BLE hardware."
- "Send build and task status notifications."
- "Measure ad performance and personalize offers across apps."

### Store posture questions

In addition to per-permission toggles, ask:

- **Account deletion** — bool, default true. Apple has required an in-app deletion flow since 2022-06-30; Google Play has required a public deletion URL since 2024-05-31. Only set this to false if the app creates no accounts at all.
- **Data collection profile** — enum (`none`, `minimal`, `standard`, `tracking`). Drives the Apple App Privacy nutrition label and Google Play Data Safety templates.
- **Audience children** — bool. If true, triggers COPPA copy in the privacy policy and Families Policy / Kids Category notes in generated review docs.

### Legal / policy inputs

Ask for:

- legal entity / publisher name
- support or privacy contact email
- governing law / jurisdiction
- optional extra privacy notes

These should be captured during initialization so generated legal text is not blank.

## Data Model

The wizard state should persist these answers in the same answer map/session used by project generation.

Suggested keys:

- `mobile_permission_camera`
- `mobile_permission_camera_usage`
- `mobile_permission_photos`
- `mobile_permission_photos_usage`
- `mobile_permission_microphone`
- `mobile_permission_microphone_usage`
- `mobile_permission_location`
- `mobile_permission_location_usage`
- `mobile_permission_bluetooth`
- `mobile_permission_bluetooth_usage`
- `mobile_permission_notifications`
- `mobile_permission_notifications_usage`
- `legal_entity_name`
- `legal_support_email`
- `legal_jurisdiction`
- `legal_privacy_notes`

Behavior rules:

- if mobile app is disabled, hide all `mobile_permission_*` questions
- only show `*_usage` text input when the corresponding permission toggle is `true`
- legal fields can still be shown when mobile/web/landing is included

## Generated Outputs

The answers should generate all of the following.

### 1. Mobile config output

For Expo / React Native mobile projects:

- populate iOS `infoPlist` usage descriptions in generated `app.json`
- populate Android permission list in generated `app.json` or manifest config

Map permissions roughly as:

- camera -> `NSCameraUsageDescription`, `android.permission.CAMERA`
- photos (read) -> `NSPhotoLibraryUsageDescription`, `READ_MEDIA_IMAGES` / `READ_MEDIA_VIDEO`
- photos (save) -> `NSPhotoLibraryAddUsageDescription` (no separate Android key)
- microphone -> `NSMicrophoneUsageDescription`, `android.permission.RECORD_AUDIO`
- location (when-in-use) -> `NSLocationWhenInUseUsageDescription`, `ACCESS_COARSE_LOCATION` / `ACCESS_FINE_LOCATION`
- location (always) -> `NSLocationAlwaysAndWhenInUseUsageDescription`, `ACCESS_BACKGROUND_LOCATION`
- Bluetooth -> `NSBluetoothAlwaysUsageDescription`, `BLUETOOTH_SCAN` / `BLUETOOTH_CONNECT`
- notifications -> `android.permission.POST_NOTIFICATIONS` (Android 13+)
- tracking / IDFA -> `NSUserTrackingUsageDescription`, `com.google.android.gms.permission.AD_ID`

Important:

- only include permissions that the user explicitly enabled
- do not over-request permissions
- permission rationale text should come directly from onboarding answers
- always emit `ITSAppUsesNonExemptEncryption=false` in `infoPlist` so TestFlight does not block every submission with a "Missing Compliance" error

### 2. Mobile starter UI output

The generated mobile starter should contain a visible section that summarizes:

- selected runtime permissions
- the short reason for each
- legal/publisher contact details

This is not the final settings screen. It is a generated starter section so the project already has the information in-app.

### 3. Legal source docs

Generate files such as:

- `legal/privacy.md`
- `legal/terms.md`
- `legal/app-review.md`
- `legal/play-data-safety.md`
- `legal/app-privacy-nutrition.md`

Purpose:

- `privacy.md`: starter privacy policy (includes GDPR, CCPA/CPRA, COPPA when the audience is children, and account-deletion language)
- `terms.md`: starter terms and conditions
- `app-review.md`: concise store-submission checklist with permission justifications and 2026 submission gates (iOS Privacy Manifest, encryption compliance, account deletion, target API, ATT/AD_ID declarations, background location, Families Policy)
- `play-data-safety.md`: pasteable Google Play Data Safety answers driven by the selected data-collection profile
- `app-privacy-nutrition.md`: pasteable Apple App Privacy nutrition label driven by the same profile

### 3a. iOS Privacy Manifest

Generate `apps/mobile/ios/PrivacyInfo.xcprivacy`. Apple has required this for every app submitted since 2024-05-01. The generated manifest:

- sets `NSPrivacyTracking` based on the ATT toggle
- populates `NSPrivacyCollectedDataTypes` from the data-collection profile plus selected permissions
- leaves `NSPrivacyAccessedAPITypes` empty with a comment listing the common required-reason categories (UserDefaults, file timestamps, disk space, system boot time) so the developer can extend it per SDK

### 3b. Account deletion surfaces

When the account-deletion toggle is on (default), generate:

- `apps/mobile/screens/DeleteAccount.tsx` — in-app deletion screen with a typed "DELETE" confirmation
- `apps/web/app/account/delete/page.tsx` — public web page (required by Google Play)
- `apps/landing/account-delete.html` — static equivalent for landing-only setups
- a dedicated "Account deletion" section in `legal/privacy.md` with the 30-day SLA and retention-exception language both stores look for

### 4. Web legal surfaces

If web app is included:

- generate `/privacy`
- generate `/terms`

If landing page is included:

- generate `/privacy.html`
- generate `/terms.html`

These can be starter pages, but they must exist and use the collected legal inputs.

### 5. Setup / next-step guidance

Generated setup docs should explicitly tell the user to review:

- permission scope
- policy text
- store review notes

This should happen before launch, not after rejection.

## Mobile App UI Requirements

In the Yaver mobile app initialization UI:

- section labels should clearly show `Permissions` and `Policies`
- summary chips / preview should reflect selected permissions
- rationale fields should appear conditionally
- copy should make clear this is for:
  - plist text
  - Android manifest justification
  - App Store / Play review readiness
  - privacy policy / terms generation

Recommended hero copy direction:

> Capture mobile permissions and policy inputs now so plist text, review notes, and legal pages are generated before submission week.

## Acceptance Criteria

Claude Code should consider this complete only when:

1. The mobile app initialization flow asks for mobile permissions and legal inputs.
2. Permission reason fields are conditional on enabled permissions.
3. Generated mobile config includes only the selected permissions.
4. Generated iOS usage descriptions use the user-provided short explanations.
5. Generated project includes privacy policy and terms source files.
6. Generated project includes app review / submission notes for permissions.
7. Generated mobile starter UI surfaces permission + legal summaries.
8. Generated web / landing policy pages exist when those surfaces are included.
9. Setup docs mention permission/policy review before launch.
10. Generated `apps/mobile/ios/PrivacyInfo.xcprivacy` exists whenever mobile is enabled (Apple requirement since 2024-05-01).
11. Generated `app.json` sets `ITSAppUsesNonExemptEncryption=false` so TestFlight submissions are not blocked on export compliance.
12. When account-deletion is on (default), an in-app screen, a public web route, and a privacy-policy section are all generated.
13. When mobile is on, `legal/play-data-safety.md` and `legal/app-privacy-nutrition.md` are generated with pasteable answers.
14. Privacy policy contains GDPR, CCPA/CPRA, and (if audience = children) COPPA sections.
15. Conditional follow-ups (photos-save, background location, tracking reason) only appear when the parent permission is on.

## Non-Goals

This feature should not:

- try to provide real legal advice
- claim generated policy text is production-lawyer-approved
- request every possible device permission by default
- bury permission rationale in release-only tooling

This is a scaffolding and launch-readiness feature, not a substitute for legal review.

## Implementation Notes

Good implementation style:

- keep the mobile UI simple and short-answer oriented
- keep the generator deterministic
- keep permission mapping centralized in one place
- avoid duplicate hardcoded permission strings across files
- make the generated outputs readable and easy to edit later

If there is already a project wizard / generator:

- extend the existing wizard
- do not create a disconnected second flow unless absolutely necessary

## Suggested Deliverables

- updated mobile initialization UI in Yaver mobile app
- updated project wizard question catalog
- centralized permission mapping helper
- generated Expo permission config
- generated legal markdown files
- generated web / landing privacy and terms pages
- generated app-review checklist doc
- updated setup guide and next steps

## Handoff Summary

This feature is mainly about making **mobile-first project creation store-safe from day one**.

The user should be able to start a project on phone, answer a few short permission/policy questions, and get a generated project that already contains:

- mobile permission config
- plist-ready reason strings
- Android permission list
- privacy policy starter
- terms starter
- app review notes
- web policy pages if relevant

That is the bar.
