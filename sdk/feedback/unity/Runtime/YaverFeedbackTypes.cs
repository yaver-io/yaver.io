using System;

namespace Yaver.Feedback
{
    [Serializable]
    public sealed class YaverFeedbackConfig
    {
        public string AgentUrl = string.Empty;
        public string AuthToken = string.Empty;
        public string ConvexSiteUrl = "https://yaver-production.convex.site";
        public string WebBaseUrl = "https://yaver.io";
        public string OAuthRedirectUrl = "yaver://oauth-callback";
        public string AppName = string.Empty;
        public string BundleId = string.Empty;
        public string ProjectName = string.Empty;
        public string ProjectPath = string.Empty;
        public string BuildVersion = string.Empty;
        public string RuntimeProfile = "auto";
        public string DeploymentMode = "self-hosted";
        public string TeamName = string.Empty;
        public string RunnerName = string.Empty;
        public string RunnerUrl = string.Empty;
        public string UnityTestMode = "EditMode";
        public string UnityBuildTarget = string.Empty;
        public string UnityBuildExecuteMethod = string.Empty;
        public string UnityBuildOutputPath = string.Empty;
        public string UnityDesktopExecutablePath = string.Empty;
        public bool Enabled = true;
        public bool AutoLogin = true;
        public bool AutoDiscoverAgentFromCloud = true;
        public bool AutoStartBlackBox = true;
        public bool CaptureUnityLogs = true;
        public bool ConnectCommandStream = true;
        public bool ShowOverlay = true;
        public bool StartOverlayCollapsed = true;
        public KeyCode ToggleOverlayKey = KeyCode.BackQuote;
        public bool AutoCaptureScreenshotOnException = true;
        public bool AutoSendCrashReports = true;
        public bool AutoTriggerFixOnCrash = true;
        public string ReloadStrategy = "scene";
        public float BlackBoxFlushIntervalSeconds = 2f;
        public int BlackBoxMaxBufferSize = 50;
        public string DeviceId = string.Empty;
    }

    [Serializable]
    public sealed class YaverDeviceInfo
    {
        public string Platform;
        public string DeviceModel;
        public string OperatingSystem;
        public string AppName;
        public string BundleId;
        public string BuildVersion;
        public string RuntimeProfile;
        public string DeploymentMode;
        public string TeamName;
        public string RunnerName;
    }

    [Serializable]
    public sealed class YaverCapturedError
    {
        public string Message;
        public string Stack;
        public bool IsFatal;
        public long Timestamp;
        public string MetadataJson;
    }

    [Serializable]
    public sealed class YaverFeedbackBundle
    {
        public string Timestamp;
        public string Note;
        public string SceneName;
        public string MetadataJson;
        public YaverDeviceInfo Device;
        public YaverCapturedError[] Errors;
        public string[] ScreenshotPaths;
    }

    [Serializable]
    public sealed class YaverBlackBoxEvent
    {
        public string Type;
        public string Level;
        public string Message;
        public long Timestamp;
        public string Source;
        public string MetadataJson;
        public float DurationMs;
        public string Route;
        public string PreviousRoute;
        public string Stack;
        public bool IsFatal;
    }

    [Serializable]
    public sealed class YaverBlackBoxBatch
    {
        public YaverBlackBoxEvent[] Events;
    }

    [Serializable]
    public sealed class YaverDiscoveryResult
    {
        public string Url;
        public string Hostname;
        public string Version;
        public int LatencyMs;
    }

    [Serializable]
    public sealed class YaverAuthUser
    {
        public string id;
        public string email;
        public string name;
        public string provider;
        public string avatarUrl;
    }

    [Serializable]
    public sealed class YaverAuthResult
    {
        public string token;
        public string userId;
        public bool requires2fa;
        public string error;
    }

    [Serializable]
    public sealed class YaverRemoteDevice
    {
        public string deviceId;
        public string name;
        public string platform;
        public bool isOnline;
        public bool needsAuth;
        public bool runnerDown;
        public long lastHeartbeat;
        public bool isGuest;
        public string hostName;
        public string hostEmail;
        public string accessScope;
        public string quicHost;
        public int quicPort;
        public int httpPort;
        public string publicKey;
    }

    [Serializable]
    public sealed class YaverCommandEnvelope
    {
        public string type;
        public YaverAgentCommand command;
    }

    [Serializable]
    public sealed class YaverAgentCommand
    {
        public string command;
        public YaverAgentCommandData data;
        public string message;
    }

    [Serializable]
    public sealed class YaverAgentCommandData
    {
        public string bundleUrl;
        public string assetsUrl;
        public string contentUrl;
    }

    [Serializable]
    internal sealed class YaverFeedbackUploadResponse
    {
        public string feedbackId;
        public string id;
    }

    [Serializable]
    public sealed class YaverReloadAck
    {
        public bool ok;
        public string mode;
        public bool acknowledged;
        public bool nativeChangesDetected;
        public string changeClass;
        public string message;
    }

    [Serializable]
    public sealed class YaverVibingResult
    {
        public string taskId;
    }

    [Serializable]
    public sealed class YaverFixResult
    {
        public string taskId;
        public string prompt;
    }

    [Serializable]
    public sealed class YaverUnityRunResult
    {
        public bool ok;
        public string status;
        public string stage;
        public string projectPath;
        public string mode;
        public string buildTarget;
        public string executeMethod;
        public string outputPath;
        public string executablePath;
        public string logPath;
        public string resultsPath;
        public string summary;
        public string[] artifacts;
        public string nextAction;
        public string[] command;
    }

    [Serializable]
    internal sealed class YaverValidateResponse
    {
        public YaverValidateUser user;
    }

    [Serializable]
    internal sealed class YaverValidateUser
    {
        public string userId;
        public string id;
        public string email;
        public string fullName;
        public string name;
        public string provider;
        public string avatarUrl;
    }

    [Serializable]
    internal sealed class YaverDeviceListEnvelope
    {
        public YaverRemoteDevice[] devices;
    }
}
