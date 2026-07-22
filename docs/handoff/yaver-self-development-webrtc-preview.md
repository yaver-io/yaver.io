# Yaver Self-Development Preview Guard

> **Audit 2026-07-23 — the guard described below was not enforced.**
>
> Every symbol this doc named existed. Two of the three did nothing.
>
> 1. **The resolver guard was dead code.** `IsYaverSelfDevelopment`,
>    `ResolveSelfDevelopmentPreview` and `EscapeOwnerFor` had no production
>    caller — only their own tests. The file calls the block "a REFUSAL, not a
>    preference"; nothing refused anything.
> 2. **The only live enforcement was a UI.** `guardYaverSelfDevelopmentActions`
>    marks buttons unsupported in the mobile Projects sheet. Hiding a button is
>    not a guard: the web dashboard, `ops`/MCP verbs, the CLI, tvOS, a second
>    phone, and the feedback→vibe auto-fix path all POST `/dev/build-native`
>    directly and reached the trap unimpeded.
> 3. **Detection was path-based, so it would have misfired.** Matching the
>    substring `yaver.io` against a filesystem path marks every project under a
>    `yaver.io/` checkout as Yaver — including the in-tree
>    `demo/mobile/todo-rn` fixture, a third-party RN app that legitimately
>    wants Hermes.
>
> Fixed: the refusal now lives at the execution layer
> (`ShouldRefuseYaverSelfDevelopmentHermes` → `/dev/build-native`, 409
> `YAVER_SELF_DEVELOPMENT_RECURSION`), so every surface inherits it, and
> detection reads project IDENTITY (`IsYaverSelfDevelopmentDir`: package name,
> bundle identifier, monorepo root) instead of ancestor path components.
>
> Two further gaps found in the same audit:
>
> - **watchOS / Wear OS / CarPlay / Android Auto matched no case** and fell
>   through to `default:`, which answers "supported — web dev server". A watchOS
>   app rendered as a web page is not the user's app; this is the exact silent
>   downgrade the strategy file forbids for Swift. They now route to the real
>   runtime (Apple simulator / Android AVD).
> - **Option lists were composed per surface, in the UI.** Rules like
>   "Hermes only for RN" lived as inline conditionals in the mobile screen, so
>   every other surface had to reimplement them. There is now one
>   detection-driven answer — `project_preview_options` →
>   `DetectProjectPreviewCapabilities` — and Hermes is *absent*, not disabled,
>   for any stack without a React Native runtime.

## Context

When the user selects the Yaver monorepo or its `mobile/` app inside the Yaver mobile app, the preview must not default to Hermes/Open-in-Yaver.

Reason: loading Yaver into Yaver puts two identical shake/exit owners in the same React Native process. The inner preview can capture the escape gesture or overlay and trap the host app. For Yaver self-development, the safe and fast path is the browser/WebRTC preview: the previewed app is pixels streamed from a browser surface, and the phone's native chrome owns exit.

This does not change third-party React Native/Expo apps. They still keep the existing Hermes-first flow.

## Current Implementation

- `desktop/agent/workspace_preview_strategy.go` — strategy vocabulary and the
  identity checks:
  - `IsYaverSelfDevelopment` — substring match on an identity STRING
    (slug + repo URL). **Never pass it a filesystem path.**
  - `IsYaverSelfDevelopmentDir` — path-safe: reads package name / bundle
    identifier / monorepo layout. Use this for a directory.
  - `ShouldRefuseYaverSelfDevelopmentHermes` — the actual decision, applied by
    `/dev/build-native`. Only `mobile-hermes` is refused; the web targets are
    the recommended route and stay open.
  - `ResolveSelfDevelopmentPreview`, `EscapeOwnerFor` — plan-level vocabulary.
- `desktop/agent/project_preview_capabilities.go` — the detection layer that
  decides which options exist for a project, exposed to every surface as the
  `project_preview_options` ops verb. Hermes is emitted only for
  `expo` / `react-native`.
- `mobile/src/lib/mobileProjectActions.ts` adds the mobile Projects action-sheet guard:
  - `isYaverSelfDevelopmentProject(project, path, repoURL)`
  - `guardYaverSelfDevelopmentActions(actions, project, path, repoURL)`
- `mobile/app/(tabs)/apps.tsx` calls `guardYaverSelfDevelopmentActions(...)` after composing the action sheet.

For Yaver self-development, the mobile guard:

- Moves `remote-runtime` / `Stream over WebRTC` to the first action.
- Marks RN/Expo `open-native` and `compile-hermes` actions as unsupported.
- Shows a concrete reason explaining the Yaver-in-Yaver shake/exit collision.

For third-party RN/Expo projects, the action order and support flags are unchanged.

## Tests Run

From `mobile/`:

```sh
npx tsx src/lib/mobileProjectActions.test.mts
```

Result: 4 tests passed.

From `desktop/agent/`:

```sh
YAVER_VAULT_SKIP_KEYCHAIN=1 go test -run 'TestYaverSelfDevelopmentRecursionGuard|TestRNKeepsAllThreeOptions|TestFlutterIsOfferedTheBrowserTargetFirst|TestResolveDevServerURLPicksExpoSiblingForRN|TestCreateBrowserWindowForRNUsesWebPreviewPort|TestDefaultStreamingSurface|TestStreamingSurfaceOptionsDefaultFirst' -count=1 -v .
```

Result: passed.

From `desktop/agent/`:

```sh
YAVER_VAULT_SKIP_KEYCHAIN=1 go test -tags=integration -run TestBrowserWindowTapChangesTodoBackgroundOverWebRTC -count=1 -v .
```

Result: passed locally on this PC.

Measured result:

- `tapToGreen=718ms`
- `totalColorLoop=1.46s`

## Tests Added 2026-07-23

```sh
cd desktop/agent
YAVER_VAULT_SKIP_KEYCHAIN=1 go test . -count=1 -run \
  'TestIsYaverSelfDevelopment|TestBuildNativeRefuses|TestSelfDevelopmentGuard|TestPreviewMatrix|TestNativeStacks|TestWearable|TestEveryShippedSurface|TestCarSurface|TestWatchSurface|TestSharedTV|TestHermesOffered|TestFlutterLeads|TestPairedDevice|TestUnpairedRN|TestThirdPartyRN|TestDetectionOverrides|TestFrameworkHint|TestOpsProjectPreview|TestEveryOptionHas'

cd mobile
npx tsx src/lib/mobileProjectActions.test.mts
```

Covered: the recursion refusal from **every** calling surface; the
third-party-fixture-inside-the-repo regression; the RN / Flutter / Swift /
Kotlin / SwiftWasm / web preview matrix; wearable + car stacks not being
downgraded to a web preview; per-surface viewports for watch, car, glass/AR-VR,
tvOS, mobile, tablet and web; and that Hermes is absent — not disabled — for
every non-RN stack.

The two "allow" cases are asserted on the pure decision function rather than
through the HTTP handler: letting the handler proceed ran a real `npm install`
plus a Metro build (~50s and a network fetch). The refusal path still goes
through the real handler, because it returns before any build starts.

## Notes For Follow-Up

- Do not re-enable Hermes for Yaver self-development unless there is a separate, native, preview-proof exit affordance outside the guest bundle.
- Keep the guard narrow. Third-party RN/Expo apps should continue to prefer Hermes/Open-in-Yaver where appropriate.
- The browser/WebRTC route covers RN web behavior. Native container changes still require native-device validation.
