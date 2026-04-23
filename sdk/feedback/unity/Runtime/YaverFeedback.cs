using System;
using System.Collections;
using System.Collections.Generic;
using System.IO;
using UnityEngine;
using UnityEngine.SceneManagement;

namespace Yaver.Feedback
{
    public static class YaverFeedback
    {
        private static readonly List<YaverCapturedError> Errors = new List<YaverCapturedError>(8);
        private static readonly List<string> PendingScreenshots = new List<string>(4);

        private static YaverFeedbackConfig _config;
        private static YaverP2PClient _client;
        private static Coroutine _commandStream;
        private static bool _crashUploadInFlight;
        private static string _lastSceneName = string.Empty;

        public static bool IsInitialized => _config != null;
        public static bool IsEnabled => _config != null && _config.Enabled;
        public static YaverFeedbackConfig CurrentConfig => _config;
        public static YaverP2PClient Client => _client;
        public static bool IsAuthenticated => YaverAuth.IsAuthenticated;
        public static string AuthToken => YaverAuth.StoredToken;
        public static bool IsDesktopRuntime =>
            Application.platform == RuntimePlatform.WindowsPlayer ||
            Application.platform == RuntimePlatform.OSXPlayer ||
            Application.platform == RuntimePlatform.LinuxPlayer ||
            Application.platform == RuntimePlatform.WindowsEditor ||
            Application.platform == RuntimePlatform.OSXEditor ||
            Application.platform == RuntimePlatform.LinuxEditor;

        public static event Action<YaverAgentCommand> CommandReceived;
        public static event Action<YaverAgentCommand> ReloadRequested;
        public static event Action<string, string> ReloadBundleRequested;
        public static event Action<string> ContentRefreshRequested;
        public static event Action<YaverAgentCommand> RelaunchRequested;

        public static void Initialize(YaverFeedbackConfig config)
        {
            _config = config ?? new YaverFeedbackConfig();
            YaverRuntime.Instance.gameObject.hideFlags = HideFlags.HideInHierarchy;
            YaverAuth.Restore();
            if (string.IsNullOrEmpty(_config.AuthToken) && !string.IsNullOrEmpty(YaverAuth.StoredToken))
            {
                _config.AuthToken = YaverAuth.StoredToken;
            }

            var url = string.IsNullOrEmpty(_config.AgentUrl) ? YaverDiscovery.StoredAgentUrl : _config.AgentUrl;
            if (!string.IsNullOrEmpty(url))
            {
                _client = new YaverP2PClient(url, _config.AuthToken);
            }

            Application.deepLinkActivated -= OnDeepLinkActivated;
            Application.deepLinkActivated += OnDeepLinkActivated;
            if (!string.IsNullOrEmpty(Application.absoluteURL))
            {
                OnDeepLinkActivated(Application.absoluteURL);
            }

            if (_config.CaptureUnityLogs)
            {
                Application.logMessageReceived -= OnUnityLogMessage;
                Application.logMessageReceived += OnUnityLogMessage;
            }

            if (_client == null && !string.IsNullOrEmpty(_config.AuthToken) && _config.AutoDiscoverAgentFromCloud)
            {
                YaverRuntime.Instance.StartCoroutine(ConnectBestAgentCoroutine(null));
            }

            if (_config.AutoStartBlackBox && _client != null)
            {
                YaverBlackBox.Start(_client, _config);
            }

            if (_config.ConnectCommandStream && _client != null)
            {
                StartCommandStream();
            }

            if (_config.ShowOverlay)
            {
                YaverOverlay.Ensure(_config);
            }
        }

        public static void Shutdown()
        {
            Application.logMessageReceived -= OnUnityLogMessage;
            Application.deepLinkActivated -= OnDeepLinkActivated;
            YaverBlackBox.Stop();
            if (_commandStream != null)
            {
                YaverRuntime.Instance.StopCoroutine(_commandStream);
                _commandStream = null;
            }
        }

        public static void SetAgent(string agentUrl, string authToken = null)
        {
            if (_config == null)
            {
                _config = new YaverFeedbackConfig();
            }

            _config.AgentUrl = agentUrl ?? string.Empty;
            if (authToken != null)
            {
                _config.AuthToken = authToken;
            }

            _client = new YaverP2PClient(_config.AgentUrl, _config.AuthToken);
            YaverDiscovery.StoredAgentUrl = YaverDiscovery.NormalizeUrl(agentUrl);
        }

        public static void BeginOAuthSignIn(string provider)
        {
            if (_config == null)
            {
                _config = new YaverFeedbackConfig();
            }

            YaverAuth.BeginOAuthSignIn(provider, _config);
        }

        public static IEnumerator LoginWithEmailCoroutine(string email, string password, Action<YaverAuthResult> onComplete = null, Action<string> onError = null)
        {
            if (_config == null)
            {
                onError?.Invoke("Yaver is not initialized.");
                yield break;
            }

            yield return YaverAuth.LoginWithEmailCoroutine(email, password, _config, result =>
            {
                _config.AuthToken = result != null ? result.token : string.Empty;
                if (_client != null)
                {
                    _client.AuthToken = _config.AuthToken;
                }
                onComplete?.Invoke(result);
            }, onError);

            if (!string.IsNullOrEmpty(_config.AuthToken) && _config.AutoDiscoverAgentFromCloud)
            {
                yield return ConnectBestAgentCoroutine(null);
            }
        }

        public static IEnumerator SignupWithEmailCoroutine(string fullName, string email, string password, Action<YaverAuthResult> onComplete = null, Action<string> onError = null)
        {
            if (_config == null)
            {
                onError?.Invoke("Yaver is not initialized.");
                yield break;
            }

            yield return YaverAuth.SignupWithEmailCoroutine(fullName, email, password, _config, result =>
            {
                _config.AuthToken = result != null ? result.token : string.Empty;
                if (_client != null)
                {
                    _client.AuthToken = _config.AuthToken;
                }
                onComplete?.Invoke(result);
            }, onError);

            if (!string.IsNullOrEmpty(_config.AuthToken) && _config.AutoDiscoverAgentFromCloud)
            {
                yield return ConnectBestAgentCoroutine(null);
            }
        }

        public static IEnumerator ConnectBestAgentCoroutine(Action<bool> onComplete)
        {
            if (_config == null)
            {
                onComplete?.Invoke(false);
                yield break;
            }

            if (!string.IsNullOrEmpty(_config.AgentUrl))
            {
                SetAgent(_config.AgentUrl, _config.AuthToken);
                onComplete?.Invoke(true);
                yield break;
            }

            YaverDiscoveryResult discovery = null;
            if (!string.IsNullOrEmpty(_config.AuthToken) && _config.AutoDiscoverAgentFromCloud)
            {
                yield return YaverAuth.DiscoverAgentCoroutine(_config, result => discovery = result);
            }

            if (discovery == null)
            {
                yield return YaverDiscovery.DiscoverCoroutine(result => discovery = result);
            }

            if (discovery == null)
            {
                onComplete?.Invoke(false);
                yield break;
            }

            SetAgent(discovery.Url, _config.AuthToken);
            if (_config.AutoStartBlackBox && _client != null && !YaverBlackBox.IsStreaming)
            {
                YaverBlackBox.Start(_client, _config);
            }
            if (_config.ConnectCommandStream && _client != null && _commandStream == null)
            {
                StartCommandStream();
            }
            onComplete?.Invoke(true);
        }

        public static void SignOut()
        {
            YaverAuth.SignOut();
            if (_config != null)
            {
                _config.AuthToken = string.Empty;
            }
            if (_client != null)
            {
                _client.AuthToken = string.Empty;
            }
        }

        internal static void NotifyLifecycle(string message)
        {
            if (!IsEnabled)
            {
                return;
            }

            YaverBlackBox.Lifecycle(message);
        }

        internal static void NotifySceneChanged(string sceneName, string previousSceneName)
        {
            if (!IsEnabled)
            {
                return;
            }

            if (string.IsNullOrEmpty(sceneName))
            {
                return;
            }

            if (string.Equals(sceneName, _lastSceneName, StringComparison.Ordinal))
            {
                return;
            }

            _lastSceneName = sceneName;
            YaverBlackBox.Navigation(sceneName, previousSceneName);
            YaverBlackBox.State("scene:" + sceneName);
        }

        public static IEnumerator SendFeedbackAndFixCoroutine(string note, string sceneName = null, string metadataJson = null, Action<string> onComplete = null, Action<string> onError = null)
        {
            string uploadedFeedbackId = null;
            yield return SendFeedbackCoroutine(
                note,
                sceneName,
                metadataJson,
                onComplete: feedbackId => { uploadedFeedbackId = feedbackId; },
                onError: onError
            );

            if (string.IsNullOrEmpty(uploadedFeedbackId))
            {
                yield break;
            }

            yield return TriggerFixFromFeedbackCoroutine(
                uploadedFeedbackId,
                onComplete: fix => { onComplete?.Invoke(uploadedFeedbackId); },
                onError: onError
            );
        }

        public static IEnumerator ReloadCoroutine(bool preferDevReload, Action<YaverReloadAck> onComplete = null, Action<string> onError = null)
        {
            if (_client == null)
            {
                onError?.Invoke("No Yaver agent configured.");
                yield break;
            }

            yield return _client.ReloadAppCoroutine(
                preferDevReload ? "dev" : "bundle",
                _config != null ? _config.ProjectName : string.Empty,
                _config != null ? _config.ProjectPath : string.Empty,
                _config != null && !string.IsNullOrEmpty(_config.BundleId) ? _config.BundleId : Application.identifier,
                onComplete,
                onError
            );
        }

        public static string GetReloadActionLabel()
        {
            var strategy = _config != null && !string.IsNullOrEmpty(_config.ReloadStrategy)
                ? _config.ReloadStrategy.ToLowerInvariant()
                : "scene";

            switch (strategy)
            {
                case "relaunch":
                case "remote":
                case "redeploy":
                    return IsDesktopRuntime ? "Relaunch" : "Redeploy";
                default:
                    return "Reload";
            }
        }

        public static bool HasUnityBuildConfiguration()
        {
            return _config != null && !string.IsNullOrEmpty(_config.UnityBuildExecuteMethod);
        }

        public static bool HasUnityDesktopExecutable()
        {
            return _config != null && !string.IsNullOrEmpty(_config.UnityDesktopExecutablePath);
        }

        public static string BuildDefaultVibingPrompt()
        {
            var runtime = ResolveRuntimeProfile();
            if (runtime == "desktop")
            {
                return "Continue this Unity desktop session until the current objective is complete. Add or update tests for behavior changes, run the relevant checks when feasible, and relaunch the build when needed.";
            }

            return "Continue this Unity mobile session until the current objective is complete. Add or update tests for behavior changes and run the relevant checks when feasible.";
        }

        public static string FormatUnityRunSummary(YaverUnityRunResult result)
        {
            if (result == null)
            {
                return "Unity action completed.";
            }

            var pieces = new List<string>(4);
            if (!string.IsNullOrEmpty(result.stage))
            {
                pieces.Add(result.stage);
            }
            if (!string.IsNullOrEmpty(result.status))
            {
                pieces.Add(result.status);
            }
            if (!string.IsNullOrEmpty(result.summary))
            {
                pieces.Add(result.summary);
            }
            if (!string.IsNullOrEmpty(result.nextAction))
            {
                pieces.Add("Next: " + result.nextAction);
            }

            return pieces.Count > 0 ? string.Join(" | ", pieces.ToArray()) : "Unity action completed.";
        }

        public static IEnumerator StartVibingCoroutine(string prompt, Action<YaverVibingResult> onComplete = null, Action<string> onError = null)
        {
            if (_client == null)
            {
                onError?.Invoke("No Yaver agent configured.");
                yield break;
            }

            yield return _client.StartVibingCoroutine(
                prompt,
                _config != null ? _config.ProjectName : string.Empty,
                _config != null ? _config.ProjectPath : string.Empty,
                _config != null && !string.IsNullOrEmpty(_config.BundleId) ? _config.BundleId : Application.identifier,
                onComplete,
                onError
            );
        }

        public static IEnumerator TriggerFixFromFeedbackCoroutine(string feedbackId, Action<YaverFixResult> onComplete = null, Action<string> onError = null)
        {
            if (_client == null)
            {
                onError?.Invoke("No Yaver agent configured.");
                yield break;
            }

            yield return _client.TriggerFixCoroutine(feedbackId, onComplete, onError);
        }

        public static IEnumerator RunUnityTestsCoroutine(string testMode = "EditMode", Action<YaverUnityRunResult> onComplete = null, Action<string> onError = null)
        {
            if (_client == null)
            {
                onError?.Invoke("No Yaver agent configured.");
                yield break;
            }

            yield return _client.RunUnityTestsCoroutine(
                _config != null ? _config.ProjectName : string.Empty,
                _config != null ? _config.ProjectPath : string.Empty,
                string.IsNullOrEmpty(testMode) && _config != null ? _config.UnityTestMode : testMode,
                onComplete,
                onError
            );
        }

        public static IEnumerator BuildUnityCoroutine(string buildTarget, string executeMethod, string outputPath = null, Action<YaverUnityRunResult> onComplete = null, Action<string> onError = null)
        {
            if (_client == null)
            {
                onError?.Invoke("No Yaver agent configured.");
                yield break;
            }

            yield return _client.BuildUnityCoroutine(
                _config != null ? _config.ProjectName : string.Empty,
                _config != null ? _config.ProjectPath : string.Empty,
                buildTarget,
                executeMethod,
                outputPath ?? string.Empty,
                onComplete,
                onError
            );
        }

        public static IEnumerator BuildConfiguredUnityCoroutine(Action<YaverUnityRunResult> onComplete = null, Action<string> onError = null)
        {
            if (_config == null)
            {
                onError?.Invoke("Yaver is not initialized.");
                yield break;
            }
            if (string.IsNullOrEmpty(_config.UnityBuildExecuteMethod))
            {
                onError?.Invoke("Missing UnityBuildExecuteMethod in Yaver config.");
                yield break;
            }

            yield return BuildUnityCoroutine(
                _config.UnityBuildTarget,
                _config.UnityBuildExecuteMethod,
                _config.UnityBuildOutputPath,
                onComplete,
                onError
            );
        }

        public static IEnumerator RelaunchConfiguredUnityCoroutine(Action<YaverUnityRunResult> onComplete = null, Action<string> onError = null)
        {
            if (_config == null)
            {
                onError?.Invoke("Yaver is not initialized.");
                yield break;
            }
            if (string.IsNullOrEmpty(_config.UnityDesktopExecutablePath))
            {
                onError?.Invoke("Missing UnityDesktopExecutablePath in Yaver config.");
                yield break;
            }

            yield return RelaunchUnityCoroutine(_config.UnityDesktopExecutablePath, onComplete, onError);
        }

        public static IEnumerator RelaunchUnityCoroutine(string executablePath, Action<YaverUnityRunResult> onComplete = null, Action<string> onError = null)
        {
            if (_client == null)
            {
                onError?.Invoke("No Yaver agent configured.");
                yield break;
            }

            yield return _client.RelaunchUnityCoroutine(
                _config != null ? _config.ProjectName : string.Empty,
                _config != null ? _config.ProjectPath : string.Empty,
                executablePath,
                onComplete,
                onError
            );
        }

        public static void AttachError(string message, string stack, bool isFatal = false, string metadataJson = null)
        {
            Errors.Add(new YaverCapturedError
            {
                Message = message ?? string.Empty,
                Stack = stack ?? string.Empty,
                IsFatal = isFatal,
                Timestamp = DateTimeOffset.UtcNow.ToUnixTimeMilliseconds(),
                MetadataJson = metadataJson
            });

            if (Errors.Count > 8)
            {
                Errors.RemoveAt(0);
            }
        }

        public static string CaptureScreenshot(string fileName = null)
        {
            var finalName = string.IsNullOrEmpty(fileName)
                ? "yaver-" + DateTime.UtcNow.ToString("yyyyMMdd-HHmmss") + ".png"
                : fileName;
            var path = Path.Combine(Application.persistentDataPath, finalName);
            ScreenCapture.CaptureScreenshot(path);
            PendingScreenshots.Add(path);
            return path;
        }

        public static IEnumerator SendFeedbackCoroutine(string note, string sceneName = null, string metadataJson = null, Action<string> onComplete = null, Action<string> onError = null)
        {
            if (_client == null)
            {
                onError?.Invoke("No Yaver agent configured.");
                yield break;
            }

            // Give ScreenCapture a frame to finish file creation for the common case.
            yield return null;

            var bundle = new YaverFeedbackBundle
            {
                Timestamp = DateTime.UtcNow.ToString("o"),
                Note = note ?? string.Empty,
                SceneName = string.IsNullOrEmpty(sceneName) ? SceneManager.GetActiveScene().name : sceneName,
                MetadataJson = metadataJson,
                Device = BuildDeviceInfo(),
                Errors = Errors.ToArray(),
                ScreenshotPaths = PendingScreenshots.ToArray()
            };

            yield return _client.UploadFeedbackCoroutine(bundle, id =>
            {
                PendingScreenshots.Clear();
                Errors.Clear();
                onComplete?.Invoke(id);
            }, onError);
        }

        public static IEnumerator EnsureConnectedCoroutine(Action<bool> onComplete)
        {
            if (_client != null)
            {
                var healthy = false;
                yield return _client.HealthCoroutine(ok => healthy = ok);
                if (healthy)
                {
                    onComplete?.Invoke(true);
                    yield break;
                }
            }

            yield return ConnectBestAgentCoroutine(onComplete);
        }

        public static void ExecuteDefaultCommand(YaverAgentCommand command)
        {
            if (command == null || string.IsNullOrEmpty(command.command))
            {
                return;
            }

            switch (command.command)
            {
                case "reload":
                    YaverBlackBox.Lifecycle("agent-reload-requested");
                    ExecuteReloadStrategy(command, false);
                    break;
                case "reload_bundle":
                    YaverBlackBox.Lifecycle("agent-reload-bundle-requested");
                    ExecuteReloadStrategy(command, true);
                    break;
                default:
                    YaverBlackBox.Log("Unhandled agent command: " + command.command, "YaverFeedback");
                    break;
            }
        }

        private static void StartCommandStream()
        {
            if (_client == null)
            {
                return;
            }

            if (_commandStream != null)
            {
                YaverRuntime.Instance.StopCoroutine(_commandStream);
            }

            var deviceId = string.IsNullOrEmpty(_config.DeviceId) ? SystemInfo.deviceUniqueIdentifier : _config.DeviceId;
            var appName = string.IsNullOrEmpty(_config.AppName) ? Application.productName : _config.AppName;
            _commandStream = YaverRuntime.Instance.StartCoroutine(_client.ListenForCommandsCoroutine(deviceId, appName, command =>
            {
                CommandReceived?.Invoke(command);
            }));
        }

        private static void OnUnityLogMessage(string condition, string stackTrace, LogType type)
        {
            if (!IsEnabled)
            {
                return;
            }

            YaverBlackBox.CaptureUnityLog(condition, stackTrace, type);
            if (type == LogType.Exception || type == LogType.Error || type == LogType.Assert)
            {
                AttachError(condition, stackTrace, type == LogType.Exception);
                if (_config != null && _config.AutoCaptureScreenshotOnException)
                {
                    CaptureScreenshot("yaver-crash-" + DateTime.UtcNow.ToString("yyyyMMdd-HHmmss") + ".png");
                }
                if (_config != null && _config.AutoSendCrashReports && !_crashUploadInFlight && _client != null)
                {
                    _crashUploadInFlight = true;
                    YaverRuntime.Instance.StartCoroutine(SendCrashReportCoroutine(condition));
                }
            }
        }

        private static void OnDeepLinkActivated(string url)
        {
            if (!YaverAuth.TryConsumeOAuthCallback(url))
            {
                return;
            }

            if (_config == null)
            {
                _config = new YaverFeedbackConfig();
            }

            _config.AuthToken = YaverAuth.StoredToken;
            if (_client != null)
            {
                _client.AuthToken = _config.AuthToken;
            }

            YaverRuntime.Instance.StartCoroutine(YaverAuth.ValidateStoredTokenCoroutine(_config, user =>
            {
                YaverRuntime.Instance.StartCoroutine(ConnectBestAgentCoroutine(null));
            }));
        }

        private static YaverDeviceInfo BuildDeviceInfo()
        {
            return new YaverDeviceInfo
            {
                Platform = Application.platform.ToString(),
                DeviceModel = SystemInfo.deviceModel,
                OperatingSystem = SystemInfo.operatingSystem,
                AppName = string.IsNullOrEmpty(_config.AppName) ? Application.productName : _config.AppName,
                BundleId = string.IsNullOrEmpty(_config.BundleId) ? Application.identifier : _config.BundleId,
                BuildVersion = string.IsNullOrEmpty(_config.BuildVersion) ? Application.version : _config.BuildVersion
                ,
                RuntimeProfile = ResolveRuntimeProfile(),
                DeploymentMode = _config != null && !string.IsNullOrEmpty(_config.DeploymentMode) ? _config.DeploymentMode : "self-hosted",
                TeamName = _config != null ? _config.TeamName : string.Empty,
                RunnerName = _config != null ? _config.RunnerName : string.Empty
            };
        }

        private static void ReloadActiveScene()
        {
            var active = SceneManager.GetActiveScene();
            if (active.buildIndex >= 0)
            {
                SceneManager.LoadScene(active.buildIndex);
                return;
            }

            if (!string.IsNullOrEmpty(active.name))
            {
                SceneManager.LoadScene(active.name);
            }
        }

        private static void ExecuteReloadStrategy(YaverAgentCommand command, bool bundleAware)
        {
            var strategy = _config != null && !string.IsNullOrEmpty(_config.ReloadStrategy)
                ? _config.ReloadStrategy.ToLowerInvariant()
                : "scene";

            switch (strategy)
            {
                case "content":
                    if (ContentRefreshRequested != null)
                    {
                        var contentUrl = command.data != null && !string.IsNullOrEmpty(command.data.contentUrl)
                            ? command.data.contentUrl
                            : command.data != null ? command.data.bundleUrl : string.Empty;
                        if (!string.IsNullOrEmpty(contentUrl))
                        {
                            ContentRefreshRequested.Invoke(contentUrl);
                            return;
                        }
                    }
                    if (bundleAware && ReloadBundleRequested != null)
                    {
                        ReloadBundleRequested.Invoke(
                            command.data != null ? command.data.bundleUrl : string.Empty,
                            command.data != null ? command.data.assetsUrl : string.Empty
                        );
                        return;
                    }
                    if (ReloadRequested != null)
                    {
                        ReloadRequested.Invoke(command);
                        return;
                    }
                    ReloadActiveScene();
                    break;
                case "custom":
                    if (bundleAware)
                    {
                        ReloadBundleRequested?.Invoke(
                            command.data != null ? command.data.bundleUrl : string.Empty,
                            command.data != null ? command.data.assetsUrl : string.Empty
                        );
                    }
                    ReloadRequested?.Invoke(command);
                    break;
                case "none":
                case "remote":
                case "redeploy":
                    YaverBlackBox.State("reload-request-acknowledged-remote");
                    break;
                case "relaunch":
                    if (RelaunchRequested != null)
                    {
                        RelaunchRequested.Invoke(command);
                        return;
                    }
                    YaverBlackBox.State("reload-request-acknowledged-relaunch");
                    break;
                case "scene":
                default:
                    if (bundleAware && ReloadBundleRequested != null)
                    {
                        ReloadBundleRequested.Invoke(
                            command.data != null ? command.data.bundleUrl : string.Empty,
                            command.data != null ? command.data.assetsUrl : string.Empty
                        );
                        return;
                    }
                    if (ReloadRequested != null)
                    {
                        ReloadRequested.Invoke(command);
                        return;
                    }
                    ReloadActiveScene();
                    break;
            }
        }

        public static IEnumerator DownloadTextCoroutine(string url, Action<string> onComplete, Action<string> onError = null)
        {
            if (string.IsNullOrEmpty(url))
            {
                onError?.Invoke("Missing URL.");
                yield break;
            }

            using (var request = new UnityEngine.Networking.UnityWebRequest(url, UnityEngine.Networking.UnityWebRequest.kHttpVerbGET))
            {
                request.downloadHandler = new UnityEngine.Networking.DownloadHandlerBuffer();
                request.timeout = 20;
                yield return request.SendWebRequest();
#if UNITY_2020_2_OR_NEWER
                if (request.result != UnityEngine.Networking.UnityWebRequest.Result.Success)
#else
                if (request.isNetworkError || request.isHttpError)
#endif
                {
                    onError?.Invoke(request.error);
                    yield break;
                }

                onComplete?.Invoke(request.downloadHandler.text);
            }
        }

        private static IEnumerator SendCrashReportCoroutine(string condition)
        {
            yield return SendFeedbackCoroutine(
                "Crash detected: " + (condition ?? "Unknown exception"),
                metadataJson: "{\"source\":\"unity-crash\",\"scene\":\"" + EscapeJson(SceneManager.GetActiveScene().name) + "\"}",
                onComplete: feedbackId =>
                {
                    if (_config != null && _config.AutoTriggerFixOnCrash && !string.IsNullOrEmpty(feedbackId))
                    {
                        YaverRuntime.Instance.StartCoroutine(TriggerFixFromFeedbackCoroutine(feedbackId));
                    }
                }
            );
            _crashUploadInFlight = false;
        }

        private static string ResolveRuntimeProfile()
        {
            if (_config != null && !string.IsNullOrEmpty(_config.RuntimeProfile) && !_config.RuntimeProfile.Equals("auto", StringComparison.OrdinalIgnoreCase))
            {
                return _config.RuntimeProfile.ToLowerInvariant();
            }

            return IsDesktopRuntime ? "desktop" : "mobile";
        }

        private static string EscapeJson(string value)
        {
            if (string.IsNullOrEmpty(value))
            {
                return string.Empty;
            }

            return value.Replace("\\", "\\\\").Replace("\"", "\\\"").Replace("\n", "\\n").Replace("\r", "\\r");
        }
    }
}
