# Yaver Feedback for Unity

Yaver's Unity SDK is a self-hosted feedback loop for Unity projects. It is designed for real-device testing, QA, hypercasual prototype iteration, casual game workflows, desktop/mobile feedback, and publisher feedback flows where the developer's own machine runs the Yaver agent.

This package is intentionally scoped differently from the React Native/Hermes path:

- Unity support is about feedback, logs, screenshots, replay, and build/install orchestration.
- It is not marketed as universal runtime code injection for arbitrary Unity mobile builds.
- Vibing and reload/redeploy are supported as control-plane actions against the Yaver agent.

## Product fit

Best fit:

- Unity mobile projects
- Unity desktop projects
- hypercasual and hybrid-casual prototypes
- casual games with richer QA/debug flows
- device-only bug capture
- publisher and tester feedback loops
- solo developers and small teams using their own build machines

Less appropriate as a promise:

- "Hermes-like hot reload"
- runtime code injection for all Unity mobile builds

## What this scaffold includes

- runtime config
- agent discovery helpers
- direct HTTP client for the Yaver agent
- Convex/web auth helpers for Unity login
- black-box event buffer
- Unity log capture via `Application.logMessageReceived`
- automatic scene navigation capture
- automatic app lifecycle capture (pause, resume, focus, quit)
- screenshot capture helper
- drop-in content refresh handler component
- optional Addressables refresh handler
- multipart feedback upload
- SSE command stream listener scaffold
- in-app overlay for testers and developers
- browser OAuth kickoff + deep-link callback consumption
- email signup/login
- stored token reuse
- authenticated agent discovery via `/devices/list`
- remote vibing request helper
- reload/redeploy request helper
- feedback-to-fix task trigger helper
- crash auto-reporting with optional fix trigger

## Install

Single entry point:

```bash
npm install -g yaver-cli
yaver sdk add feedback --platform unity --dir /path/to/UnityProject
yaver test unity --dir /path/to/UnityProject --mode EditMode
```

That patches `Packages/manifest.json` to add the local UPM dependency.

Use as a local Unity Package Manager dependency:

```json
{
  "dependencies": {
    "io.yaver.feedback.unity": "file:../../sdk/feedback/unity"
  }
}
```

Or copy the package into your own internal UPM registry flow later.

## GitHub CI

This repo already includes GitHub Actions workflows for Unity package and sample testing.

- package tests: [`.github/workflows/unity-sdk-tests.yml`](/Users/kivanccakmak/Workspace/yaver.io/.github/workflows/unity-sdk-tests.yml:1)
- sample tests/builds: [`.github/workflows/unity-sample-ci.yml`](/Users/kivanccakmak/Workspace/yaver.io/.github/workflows/unity-sample-ci.yml:1)
- setup notes: [`docs/unity-github-ci.md`](/Users/kivanccakmak/Workspace/yaver.io/docs/unity-github-ci.md:1)

Current GitHub-hosted coverage in this repo:

- package `EditMode` tests
- sample `EditMode` tests
- sample `PlayMode` tests
- sample builds for `StandaloneWindows64`, `StandaloneLinux64`, `Android`, and `WebGL`
- uploaded Unity code coverage artifacts for the test jobs

## Quick start

Create a bootstrap MonoBehaviour in your Unity project:

```csharp
using UnityEngine;
using Yaver.Feedback;

public sealed class YaverBootstrap : MonoBehaviour
{
    [SerializeField] private string agentUrl = "http://192.168.1.10:18080";
    [SerializeField] private string authToken = "";

    private void Awake()
    {
        var config = new YaverFeedbackConfig
        {
            AgentUrl = agentUrl,
            AuthToken = authToken,
            ConvexSiteUrl = "https://yaver-production.convex.site",
            WebBaseUrl = "https://yaver.io",
            AppName = Application.productName,
            BuildVersion = Application.version,
            AutoStartBlackBox = true,
            CaptureUnityLogs = true,
            ShowOverlay = true
    };

        YaverFeedback.Initialize(config);
    }
}
```

A minimal consumer scaffold also lives at `sdk/feedback/test-app/unity`.

## Overlay

The package now includes a built-in in-app overlay. It is meant as the default tester surface for Unity projects:

- Google/GitHub/GitLab/Apple/Microsoft OAuth kickoff
- email login/signup
- connect to the best available agent
- toggle with backquote by default
- minimized draggable bubble mode
- capture screenshot
- send feedback
- trigger feedback-to-fix
- start vibing
- request reload/redeploy
- recent activity/status list

It uses Unity `OnGUI` so it works without requiring UGUI or a prefab setup.

Config knobs:

```csharp
new YaverFeedbackConfig
{
    ShowOverlay = true,
    StartOverlayCollapsed = true,
    ToggleOverlayKey = KeyCode.BackQuote,
    AutoCaptureScreenshotOnException = true,
    AutoSendCrashReports = true,
    AutoTriggerFixOnCrash = true,
    ReloadStrategy = "scene"
};
```

OAuth redirect:

- default redirect is `yaver://oauth-callback`
- Unity should forward deep links so `Application.deepLinkActivated` fires
- when the callback contains `token=...`, the SDK stores it, validates it, and tries to connect to the user's agent

Start a remote vibing task from the game:

```csharp
StartCoroutine(YaverFeedback.StartVibingCoroutine(
    "Analyze the current Unity mobile project, inspect the last feedback context, and propose the next fix.",
    onComplete: result => Debug.Log("Vibing task: " + result.taskId),
    onError: err => Debug.LogError(err)
));
```

Request a reload/redeploy cycle from the agent:

```csharp
// preferDevReload=true is a fast path when the project is in a compatible dev flow.
// For Unity mobile, bundle/redeploy is usually the realistic path.
StartCoroutine(YaverFeedback.ReloadCoroutine(
    preferDevReload: false,
    onComplete: ack => Debug.Log("Reload ack: " + ack.message),
    onError: err => Debug.LogError(err)
));
```

Report a bug from code:

```csharp
using UnityEngine;
using Yaver.Feedback;

public sealed class ReportBugButton : MonoBehaviour
{
    public void ReportBug()
    {
        YaverBlackBox.State("Bug report triggered from UI");
        YaverFeedback.CaptureScreenshot("bug.png");
        StartCoroutine(YaverFeedback.SendFeedbackCoroutine(
            "Player got stuck after tapping retry",
            "Level_03",
            onComplete: feedbackId =>
            {
                Debug.Log($"Uploaded report: {feedbackId}");
                StartCoroutine(YaverFeedback.TriggerFixFromFeedbackCoroutine(
                    feedbackId,
                    onComplete: fix => Debug.Log("Fix task: " + fix.taskId)
                ));
            }
        ));
    }
}
```

## Runtime model

The Unity SDK follows the same Yaver philosophy as the other feedback SDKs:

- phone or game UI captures context
- your own machine receives the report
- cloud is optional later

Current focus:

- screenshots
- logs
- exceptions
- automatic scene transitions
- application lifecycle state
- device/build metadata
- scene metadata
- agent command channel scaffold
- vibing task kickoff
- reload/redeploy request kickoff

Desktop games fit this architecture well too:

- the same SDK can run inside Windows/macOS/Linux Unity games
- overlay and auth flows are easier to host than on mobile
- reload can mean scene/content refresh first, then rebuild/relaunch
- remote machines can keep iterating while the developer is away

## Content refresh handler

The package now includes a reusable `YaverContentRefreshHandler` component.

What it does:

- subscribes to `YaverFeedback.ContentRefreshRequested`
- optionally treats bundle reload commands as content refresh requests too
- downloads text payloads from the provided URL
- stores the last payload/source URL
- exposes UnityEvents for success and error handling

This is the first reusable SDK-side implementation of the `content` reload strategy.

Typical use:

1. add `YaverContentRefreshHandler` to a GameObject
2. wire `On Content Updated` to your own config/parser/apply method
3. set `ReloadStrategy = "content"`

That gives a Unity project a real content-first reload path without committing to any specific backend like Addressables yet.

If the project already uses Addressables, the package also includes `YaverAddressablesRefreshHandler`.

It is intentionally optional:

- if Addressables is installed, it will attempt a content-catalog-driven refresh
- if Addressables is not installed, the handler simply logs that the package is unavailable

That keeps the base SDK usable for simpler projects while giving Addressables-based projects a cleaner integration point.

The sample app includes a simple JSON gameplay-config flow built on top of this handler:

- `YaverGameConfig`
- `YaverGameConfigApplier`
- `YaverContentReloadDemo`

That is the recommended first step for hypercasual/casual teams: tune mutable values remotely before moving to heavier asset/content systems.

Useful config fields for solo and studio setups:

- `RuntimeProfile`
  - `auto` by default
  - resolves to `desktop` on Windows/macOS/Linux builds and editor, `mobile` elsewhere
- `DeploymentMode`
  - `self-hosted` by default
  - can be used to tag remote-runner or studio deployments later
- `ProjectName`
- `ProjectPath`
- `TeamName`
- `RunnerName`
- `RunnerUrl`
- `UnityTestMode`
- `UnityBuildTarget`
- `UnityBuildExecuteMethod`
- `UnityBuildOutputPath`
- `UnityDesktopExecutablePath`

## Remote vibing

The Unity package can ask the local/owned Yaver agent to start a vibing task through `/vibing/execute`.

Important:

- this usually requires an owner/CLI-grade token, not a narrow feedback-only SDK token
- the Unity package therefore starts with a bring-your-own token model

Recommended config additions when you know them:

- `ProjectName`
- `ProjectPath`
- `BundleId`

Those help the agent route work to the correct repo/project.

## Reload and redeploy

The Unity package exposes a reload/redeploy request helper, but "reload" should be interpreted differently than in Hermes-first React Native flows.

For Unity mobile, realistic meanings are:

- ask the agent to rebuild/redeploy
- ask the host app to restart the active scene
- refresh content/config in a host-defined way

Recommended remote reload ladder:

1. `custom`
   Use your own content refresh or patch path. Subscribe to:
   - `YaverFeedback.ReloadRequested`
   - `YaverFeedback.ReloadBundleRequested`

2. `content`
   Fastest Unity-native iteration path for remote use. Subscribe to:
   - `YaverFeedback.ContentRefreshRequested`
   and use it to refresh Addressables, Remote Config, JSON gameplay data, level definitions, or other mutable content.

3. `scene`
   Default. If no custom handler is registered, the SDK reloads the active scene.

4. `remote` / `redeploy`
   Acknowledge the command as a remote rebuild/redeploy request and let the host machine perform the heavy work.

5. `relaunch`
   Desktop-oriented path. The agent can rebuild or relaunch the local player and the SDK treats the action as a desktop iteration loop rather than a mobile redeploy.

## Desktop test/build/relaunch helpers

The Unity SDK can now call dedicated agent endpoints for desktop-oriented Unity workflows:

- `POST /unity/test`
- `POST /unity/build`
- `POST /unity/relaunch`

SDK helpers:

- `YaverFeedback.RunUnityTestsCoroutine(...)`
- `YaverFeedback.BuildUnityCoroutine(...)`
- `YaverFeedback.RelaunchUnityCoroutine(...)`
- `YaverFeedback.BuildConfiguredUnityCoroutine(...)`
- `YaverFeedback.RelaunchConfiguredUnityCoroutine(...)`

CLI helper:

- `yaver test unity --dir /path/to/UnityProject --mode EditMode`

Current intent:

- run Unity EditMode or PlayMode tests from the agent machine
- trigger project-specific batch builds through a Unity `-executeMethod`
- relaunch a built desktop player from the agent machine

Builds are intentionally conservative right now:

- Yaver requires a Unity `-executeMethod` to build
- that keeps project-specific build logic inside the Unity project where it belongs

Example:

```csharp
StartCoroutine(YaverFeedback.RunUnityTestsCoroutine(
    "EditMode",
    onComplete: result => Debug.Log(result.summary),
    onError: err => Debug.LogError(err)
));
```

With config-driven desktop defaults:

```csharp
var config = new YaverFeedbackConfig
{
    ProjectPath = "/absolute/path/to/UnityProject",
    UnityTestMode = "EditMode",
    UnityBuildTarget = "StandaloneWindows64",
    UnityBuildExecuteMethod = "YaverBuildTools.BuildWindows64",
    UnityBuildOutputPath = "Builds/YaverDesktop",
    UnityDesktopExecutablePath = "Builds/YaverDesktop/YaverDemo.exe"
};
```

For a concrete sample build target, see:

- `sdk/feedback/test-app/unity/Assets/Editor/YaverBuildTools.cs`

That sample provides:

- `YaverBuildTools.BuildWindows64`
- `YaverBuildTools.BuildMacOS`
- `YaverBuildTools.BuildLinux64`

For a concrete content reload example, see:

- `sdk/feedback/test-app/unity/Assets/Scripts/YaverContentReloadDemo.cs`

## CI without local Unity

You do not need Unity installed locally just to keep the package moving.

This repo now includes a GitHub Actions workflow at `.github/workflows/unity-sdk-tests.yml` that runs the Unity SDK's EditMode package tests with GameCI in a Linux container.

What you need in GitHub:

- `UNITY_LICENSE` secret configured for Actions

What it gives you:

- package-level Unity tests in CI
- no local Unity install required for day-to-day SDK work
- a reasonable confidence floor before handing the package to a game developer

Config:

```csharp
new YaverFeedbackConfig
{
    ReloadStrategy = "content" // content | custom | scene | remote | redeploy | none
};
```

The package includes `ExecuteDefaultCommand(...)`, which simply reloads the active scene for `reload` and `reload_bundle`. That is only a starter behavior. Real games will usually replace it with something better.

## Current limitations

1. This package is the initial scaffold, not a fully polished Unity asset.
2. Screen replay is not yet implemented inside the package.
3. Vibing uses the existing Yaver agent route, but auth/scope expectations depend on the token you provide.
4. The command stream listens for commands, but the host game must decide what "reload" means unless it opts into the default helper.
5. Unity mobile builds should not be treated as if they support Hermes-like code swapping.

## Recommended interpretation of commands

- `reload`: reload content, restart a scene, refresh config, or trigger a host-defined verify action
- `reload_bundle`: reserved for future content/package flows, not a universal code injection contract

## Suggested rollout order

1. Use the SDK for internal QA and prototype loops.
2. Add build/install orchestration from the agent side.
3. Add content/config flush workflows where your game architecture supports it.

## License

MIT.
