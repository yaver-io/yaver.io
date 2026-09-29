# Yaver Meta Horizon release plan

Status: planning baseline, not a release attestation  
Date checked: 2026-09-29

This document records the registration, packaging, verification, and release
path for Yaver on Meta Quest and Meta AI glasses. It does not authorize a store
submission or deploy. Recheck Meta's current requirements before implementation
or submission because SDK, permission, device, and review rules change.

This plan updates `docs/planning/REGISTRATIONS.md`: a Horizon Store listing is
now an intended release lane rather than indefinitely deferred. The existing
WebXR `/spatial` route remains a useful browser experience, but it does not
replace a reviewed store application.

## Decision

Google Play is not sufficient for Meta hardware.

- Google Play distributes Yaver's Android phone application.
- Meta Quest applications are registered, uploaded, tested, reviewed, and
  released independently through the Meta Horizon Developer Dashboard and
  Meta Horizon Store.
- Meta AI/Ray-Ban/Oakley glasses use the separate Wearables Developer Center.
- Apple visionOS, Google Android XR, Meta Quest/Horizon OS, WebXR, and Meta AI
  glasses are separate release targets even when they share product logic.

Ship the smallest useful Quest product first:

1. Yaver as a resizable standard Android 2D panel.
2. Preserve `/spatial` as the optional Quest Browser/WebXR route.
3. Add spatial windows or hybrid behavior after the panel release passes review
   and real-device testing.
4. Build fully immersive OpenXR behavior only where immersion improves the
   remote development/control experience.
5. Treat Meta AI glasses as a separate companion/web/connector program.

## Registration and ownership

Use the verified company Meta developer organization/team. Do not create a
personal, single-admin publishing dependency.

- Create or select a Meta account and Meta Horizon profile.
- Create/join the publisher team in the Meta Horizon Developer Dashboard.
- Complete account verification and organization verification. Use business
  verification when the legal publisher is available; otherwise follow Meta's
  supported admin-verification route.
- Add at least one backup administrator.
- Create a dedicated **Meta Horizon Store** app page named `Yaver`.
- Keep Yaver's Meta App ID, package identity, signing, listing, release
  channels, secrets, and review evidence separate from every other product.
- Store app secrets/access tokens only through Yaver's approved vault/secret
  lanes; never commit them.
- Configure payout/tax details only when paid distribution or Meta-managed
  purchases require them.
- Create an internal/alpha release channel before public review.

## Current code truth

The shared Android mobile artifact is not an acceptable Quest submission as-is.
The current tracked manifest and Gradle configuration include concerns for
phones, Android TV, Wear OS, Android Auto, and Google Play:

- Google/Firebase configuration and Google Play Services;
- location, camera, Bluetooth, contacts, calendar, broad storage, overlay,
  settings, notifications, and multiple foreground-service permissions;
- TV leanback launcher entries and Wear OS listener/service integration;
- `com.oculus.intent.category.VR` despite the binary not being a dedicated
  immersive OpenXR application.

Meta explicitly warns that Quest applications cannot rely on Google Play
Services, and its review process rejects unsupported permissions and requires
case-by-case explanations for review-sensitive permissions. The merged Quest
manifest—not `app.json` alone—is the evidence.

The `/spatial` web route already distinguishes Quest, Vision Pro, display
glasses, and other viewports. Keep it as a browser-based immersive option and
test it independently. A browser bookmark or WebXR URL does not make Yaver
discoverable in the Meta Horizon Store and does not satisfy native store VRCs.

## Dedicated Quest artifact

Add a `quest` Android product flavor or equivalent source set that produces a
Meta-reviewable release APK while sharing application logic with the mobile
client. Do not weaken or strip functionality from the normal Play artifact to
make the Quest build pass.

The Quest variant should:

- preserve the stable Yaver product name and use a Yaver-only package/signing
  identity;
- target Android API 34 and use a Meta-supported minimum SDK;
- set `installLocation="auto"`;
- launch as a standard Android panel, not an OpenXR activity;
- set the launch activity's `excludeFromRecents="true"`;
- declare supported Quest devices and the current Meta VR OS SDK level;
- omit VR head tracking or declare it optional;
- omit `com.oculus.intent.category.VR` for the panel build;
- omit Android TV leanback launcher behavior;
- remove Firebase/Google Play Services, Wear OS, Android Auto, contacts,
  calendar, system overlay/settings, broad storage, camera, precise location,
  Bluetooth, Play notification, and unrelated foreground-service declarations
  from the merged manifest unless a Quest-specific need is supported, tested,
  and review-justified;
- use device-code sign-in and direct/relay connectivity that work without a
  paired phone or Google Mobile Services;
- support system gaze/pinch, hands, controllers, keyboard, mouse, resizable
  panels, landscape layout, focus, back navigation, and readable type;
- degrade honestly when a local-network, microphone, or runtime capability is
  unavailable.

The initial headset UX should follow Yaver's less-is-more and semantic-task
contracts: show the task answer/state and one next action. Keep raw terminal
bytes, patches, and dense diagnostics folded or absent on the headset. Preserve
explicit user control over render/reload and never refresh a working surface
merely because a coding task completed.

## Meta packaging baseline

Before uploading, inspect the final merged release manifest and verify the
current Meta rules. The baseline checked on 2026-09-29 includes:

- release builds must be signed; Meta's native Quest guidance requires Android
  v2 APK signing and continuing updates must use the same certificate;
- new Meta app records require `targetSdkVersion` 34;
- panel applications may omit head tracking or mark it not required;
- the OpenXR/VR activity category belongs only in immersive/OpenXR builds;
- the launch activity must contain the normal `MAIN`/`LAUNCHER` filter and set
  `excludeFromRecents="true"`;
- `com.oculus.supportedDevices` must name the supported Quest models;
- unsupported permissions cause rejection, while review-requiring permissions
  require a concrete justification and denial must degrade gracefully;
- Meta uploads signed APKs through the Developer Dashboard, Meta Quest Developer
  Hub, or supported command-line uploader; a Google Play AAB alone is not the
  submission artifact.

Do not interpret a successful upload as a review pass. Run the applicable 2D
Virtual Reality Checks and exercise the exact candidate on real hardware.

## Build and release integration

The Quest lane must be explicit and must not silently reuse the Google Play
deploy command.

Add a build-only command to the canonical `./deploy/deploy.sh` front door after
the flavor exists. A future upload/release command must remain owner-only,
require the exact Yaver Meta app/channel target, use project-scoped credentials,
and report the Meta build identifier and postconditions. Until that guarded
path is implemented and reviewed, build and validate locally without uploading.

Signing continuity requirements:

- use a stable Yaver Meta release key and do not substitute the debug signer;
- keep the owner-only local copy outside Git;
- keep a client-side-encrypted recovery copy in Yaver's own project namespace,
  separate from the Google Play key and every other product;
- pin and verify the public certificate fingerprint during builds/uploads;
- never print keystore paths containing credentials, passwords, tokens, or
  private-key material in logs or documentation.

## Verification gates

Require both headless package evidence and a closed loop on a physical Quest 3
or Quest 3S.

Headless/package gates:

- release APK signer and v2 signature verified;
- unique, monotonically increasing version code;
- merged manifest archived and audited;
- no unexpected permissions, features, services, receivers, or providers;
- no required Google Mobile Services dependency;
- Meta manifest validator and applicable 2D VRCs pass;
- device-code auth, direct/relay API operations, task status, and logout pass
  without a paired phone;
- every retained runtime permission passes grant, denial, and retry tests;
- a regression test fails if a removed phone/TV/Wear/Google dependency returns.

Closed-loop gates:

- clean install from the Meta alpha channel;
- first launch, device-code sign-in, restart, token refresh, and logout;
- task list/detail, semantic streaming state, action-required state, completion,
  approval/handoff, stop, and explicitly requested render/reload;
- controller, hand, gaze/pinch, keyboard, and mouse navigation;
- window resize, landscape sizing, text readability, scroll, focus, and back;
- LAN unavailable, relay fallback, offline, reconnect, expired auth, and runner
  unavailable states with a named cause and usable route to recovery;
- no camera/location/telephony/Google-service-dependent dead ends;
- product-correct branding, privacy links, and reviewer instructions;
- screenshots and recordings contain no credentials, customer/project names,
  machine addresses, source code, private task output, notifications, or
  unrelated applications.

Break the manifest/dependency guard deliberately, observe the intended failure,
then restore it. Plan at least two weeks for Meta review and correction cycles.

## Spatial roadmap after the panel release

1. Add additional spatial windows through Meta's Android layout capabilities.
2. Evaluate Meta Spatial SDK for a cooperative operations room with task cards,
   previews, and a focused active surface.
3. Use a hybrid application when a normal Android panel should transition into
   an immersive activity.
4. Use OpenXR only for genuinely immersive preview or remote-runtime use cases.
5. Keep `/spatial` as the cross-platform WebXR lane and test it separately from
   the native Quest artifact.
6. Preserve the security boundary: no unbounded unauthenticated shell, no token
   in URLs, no weaker relay/auth rules, and no false success for remote actions.

## Meta AI glasses lane

Quest registration does not cover Meta AI glasses. Register Yaver separately
in the Wearables Developer Center and track three delivery paths:

- Wearables Device Access Toolkit integrated into Yaver mobile for supported
  camera/audio/motion/display/input capabilities;
- the existing hosted compact `/spatial?surface=ray-ban-display` Web App path
  for display glasses, updated against the current Meta Web App APIs;
- a Meta AI Connector exposing a small, explicit, permissioned set of Yaver
  actions rather than a generic shell.

As checked on 2026-09-29, Meta says Wearables Device Access Toolkit 1.0 begins
rolling out on 2026-09-30, while public submission/discovery for developer
experiences is still described as coming soon. Register and prototype now, but
do not claim public glasses-store availability until Meta opens submission and
the exact Yaver experience is approved.

The first glasses UX should be voice-first and minimal: current task state,
answer, one approval/handoff/stop action, and an explicit way to continue on a
larger surface. Never render secrets, raw source, full terminal output, or
customer/project identity on a glanceable display.

## Execution checklist

### Account work

- [ ] Create/join and verify the company Meta publisher team.
- [ ] Add backup administrator.
- [ ] Create the Yaver Horizon Store app record.
- [ ] Create internal/alpha release channels.
- [ ] Register Yaver in the Wearables Developer Center.

### Implementation

- [ ] Add the dedicated Quest flavor/source set and signed APK build.
- [ ] Remove Google/phone/TV/Wear-only components from the Quest variant.
- [ ] Add manifest, dependency, permission, and signer regression tests.
- [ ] Add the guarded build-only Quest target to the canonical deploy front
      door without enabling upload by default.
- [ ] Complete headless package gates.
- [ ] Complete physical-Quest closed-loop gates.
- [ ] Prepare scrubbed listing and review evidence.

### Submission

- [ ] Upload the candidate to Yaver's own Meta alpha channel.
- [ ] Run applicable 2D VRCs and fix every candidate-specific failure.
- [ ] Complete metadata, privacy/data protection, content rating, permissions,
      app access, pricing, and ads declarations.
- [ ] Submit for review and retain review correspondence.
- [ ] Release only the reviewed candidate; archive signer, checksum, manifest,
      version, commit, evidence status, and Meta transaction/build identifiers.

## Official references

- [Get started with Android apps on Horizon OS](https://developers.meta.com/horizon/documentation/android-apps/horizon-os-apps/)
- [Create Meta Horizon applications](https://developers.meta.com/horizon/resources/publish-create-app/)
- [Application manifest requirements](https://developers.meta.com/horizon/resources/publish-mobile-manifest/)
- [Upload Meta Quest applications](https://developers.meta.com/horizon/resources/publish-upload-overview/)
- [Submit applications for review](https://developers.meta.com/horizon/resources/publish-submit/)
- [VRC permission requirements](https://developers.meta.com/horizon/resources/vrc-quest-security-2/)
- [Review-requiring Android permissions](https://developers.meta.com/horizon/resources/permissions-review-required/)
- [Meta Wearables developer overview](https://developers.meta.com/wearables/)
- [Meta Wearables FAQ](https://developers.meta.com/wearables/faq/)

