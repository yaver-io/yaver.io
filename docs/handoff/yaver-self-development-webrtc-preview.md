# Yaver Self-Development Preview Guard

## Context

When the user selects the Yaver monorepo or its `mobile/` app inside the Yaver mobile app, the preview must not default to Hermes/Open-in-Yaver.

Reason: loading Yaver into Yaver puts two identical shake/exit owners in the same React Native process. The inner preview can capture the escape gesture or overlay and trap the host app. For Yaver self-development, the safe and fast path is the browser/WebRTC preview: the previewed app is pixels streamed from a browser surface, and the phone's native chrome owns exit.

This does not change third-party React Native/Expo apps. They still keep the existing Hermes-first flow.

## Current Implementation

- `desktop/agent/workspace_preview_strategy.go` already has the resolver-level guard:
  - `IsYaverSelfDevelopment`
  - `ResolveSelfDevelopmentPreview`
  - `EscapeOwnerFor`
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

## Notes For Follow-Up

- Do not re-enable Hermes for Yaver self-development unless there is a separate, native, preview-proof exit affordance outside the guest bundle.
- Keep the guard narrow. Third-party RN/Expo apps should continue to prefer Hermes/Open-in-Yaver where appropriate.
- The browser/WebRTC route covers RN web behavior. Native container changes still require native-device validation.
