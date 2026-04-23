using UnityEngine;
using Yaver.Feedback;

public sealed class YaverBootstrap : MonoBehaviour
{
    [SerializeField] private string agentUrl = "http://127.0.0.1:18080";
    [SerializeField] private string authToken = "";
    [SerializeField] private bool autoStartBlackBox = true;
    [SerializeField] private bool captureUnityLogs = true;
    [SerializeField] private bool connectCommandStream = true;
    [SerializeField] private string projectPath = "";
    [SerializeField] private string unityTestMode = "EditMode";
    [SerializeField] private string unityBuildTarget = "StandaloneWindows64";
    [SerializeField] private string unityBuildExecuteMethod = "YaverBuildTools.BuildWindows64";
    [SerializeField] private string unityBuildOutputPath = "Builds/YaverDesktop";
    [SerializeField] private string unityDesktopExecutablePath = "Builds/YaverDesktop/YaverDemo.exe";

    private void Awake()
    {
        var config = new YaverFeedbackConfig
        {
            AgentUrl = agentUrl,
            AuthToken = authToken,
            ConvexSiteUrl = "https://yaver-production.convex.site",
            WebBaseUrl = "https://yaver.io",
            AppName = Application.productName,
            BundleId = Application.identifier,
            ProjectName = Application.productName,
            ProjectPath = projectPath,
            BuildVersion = Application.version,
            RuntimeProfile = "auto",
            DeploymentMode = "self-hosted",
            AutoLogin = true,
            AutoDiscoverAgentFromCloud = true,
            AutoStartBlackBox = autoStartBlackBox,
            CaptureUnityLogs = captureUnityLogs,
            ConnectCommandStream = connectCommandStream,
            ShowOverlay = true,
            AutoCaptureScreenshotOnException = true,
            AutoSendCrashReports = true,
            AutoTriggerFixOnCrash = true,
            UnityTestMode = unityTestMode,
            UnityBuildTarget = unityBuildTarget,
            UnityBuildExecuteMethod = unityBuildExecuteMethod,
            UnityBuildOutputPath = unityBuildOutputPath,
            UnityDesktopExecutablePath = unityDesktopExecutablePath
        };

        YaverFeedback.Initialize(config);
        YaverFeedback.CommandReceived += YaverFeedback.ExecuteDefaultCommand;
        YaverBlackBox.State("unity-test-app-bootstrap-ready");
    }

    private void OnDestroy()
    {
        YaverFeedback.CommandReceived -= YaverFeedback.ExecuteDefaultCommand;
    }
}
