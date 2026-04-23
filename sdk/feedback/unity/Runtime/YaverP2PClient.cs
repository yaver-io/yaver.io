using System;
using System.Collections;
using System.IO;
using System.Text;
using UnityEngine;
using UnityEngine.Networking;

namespace Yaver.Feedback
{
    public sealed class YaverP2PClient
    {
        public string BaseUrl { get; private set; }
        public string AuthToken { get; set; }

        public YaverP2PClient(string baseUrl, string authToken)
        {
            BaseUrl = YaverDiscovery.NormalizeUrl(baseUrl);
            AuthToken = authToken ?? string.Empty;
        }

        public IEnumerator HealthCoroutine(Action<bool> onComplete)
        {
            using (var request = UnityWebRequest.Get(BaseUrl + "/health"))
            {
                request.timeout = 3;
                ApplyAuth(request);
                yield return request.SendWebRequest();

#if UNITY_2020_2_OR_NEWER
                onComplete?.Invoke(request.result == UnityWebRequest.Result.Success);
#else
                onComplete?.Invoke(!request.isNetworkError && !request.isHttpError);
#endif
            }
        }

        public IEnumerator UploadFeedbackCoroutine(YaverFeedbackBundle bundle, Action<string> onComplete, Action<string> onError = null)
        {
            var form = new WWWForm();
            form.AddField("metadata", JsonUtility.ToJson(bundle));

            if (bundle.ScreenshotPaths != null)
            {
                foreach (var path in bundle.ScreenshotPaths)
                {
                    if (string.IsNullOrEmpty(path) || !File.Exists(path))
                    {
                        continue;
                    }

                    form.AddBinaryData("screenshots", File.ReadAllBytes(path), Path.GetFileName(path), "image/png");
                }
            }

            using (var request = UnityWebRequest.Post(BaseUrl + "/feedback", form))
            {
                ApplyAuth(request);
                yield return request.SendWebRequest();

                if (HasError(request))
                {
                    onError?.Invoke(request.error);
                    yield break;
                }

                var response = JsonUtility.FromJson<YaverFeedbackUploadResponse>(request.downloadHandler.text);
                var id = response != null && !string.IsNullOrEmpty(response.feedbackId)
                    ? response.feedbackId
                    : response != null ? response.id : string.Empty;
                onComplete?.Invoke(id);
            }
        }

        public IEnumerator TriggerFixCoroutine(string feedbackId, Action<YaverFixResult> onComplete, Action<string> onError = null)
        {
            if (string.IsNullOrEmpty(feedbackId))
            {
                onError?.Invoke("Missing feedback ID.");
                yield break;
            }

            using (var request = new UnityWebRequest(BaseUrl + "/feedback/" + UnityWebRequest.EscapeURL(feedbackId) + "/fix", UnityWebRequest.kHttpVerbPOST))
            {
                request.uploadHandler = new UploadHandlerRaw(Encoding.UTF8.GetBytes("{}"));
                request.downloadHandler = new DownloadHandlerBuffer();
                request.timeout = 30;
                ApplyAuth(request);
                request.SetRequestHeader("Content-Type", "application/json");
                yield return request.SendWebRequest();

                if (HasError(request))
                {
                    onError?.Invoke(request.error);
                    yield break;
                }

                onComplete?.Invoke(JsonUtility.FromJson<YaverFixResult>(request.downloadHandler.text));
            }
        }

        public IEnumerator SendBlackBoxBatchCoroutine(YaverBlackBoxEvent[] events, string deviceId, string appName)
        {
            var payload = new YaverBlackBoxBatch { Events = events };
            var bytes = Encoding.UTF8.GetBytes(JsonUtility.ToJson(payload));

            using (var request = new UnityWebRequest(BaseUrl + "/blackbox/events", UnityWebRequest.kHttpVerbPOST))
            {
                request.uploadHandler = new UploadHandlerRaw(bytes);
                request.downloadHandler = new DownloadHandlerBuffer();
                request.timeout = 5;
                ApplyAuth(request);
                request.SetRequestHeader("Content-Type", "application/json");
                request.SetRequestHeader("X-Device-ID", deviceId ?? string.Empty);
                request.SetRequestHeader("X-App-Name", appName ?? string.Empty);
                yield return request.SendWebRequest();
            }
        }

        public IEnumerator ReloadAppCoroutine(string mode, string projectName, string projectPath, string bundleId, Action<YaverReloadAck> onComplete, Action<string> onError = null)
        {
            var desiredMode = string.Equals(mode, "dev", StringComparison.OrdinalIgnoreCase) ? "dev" : "bundle";

            if (desiredMode == "dev")
            {
                using (var request = new UnityWebRequest(BaseUrl + "/dev/reload", UnityWebRequest.kHttpVerbPOST))
                {
                    request.uploadHandler = new UploadHandlerRaw(new byte[0]);
                    request.downloadHandler = new DownloadHandlerBuffer();
                    request.timeout = 15;
                    ApplyAuth(request);
                    yield return request.SendWebRequest();

                    if (!HasError(request))
                    {
                        var text = request.downloadHandler != null ? request.downloadHandler.text : string.Empty;
                        var ack = string.IsNullOrEmpty(text)
                            ? new YaverReloadAck { ok = true, acknowledged = true, mode = "dev", message = "Hot reload request accepted." }
                            : JsonUtility.FromJson<YaverReloadAck>(text);
                        if (ack == null)
                        {
                            ack = new YaverReloadAck { ok = true, acknowledged = true, mode = "dev", message = "Hot reload request accepted." };
                        }

                        onComplete?.Invoke(ack);
                        yield break;
                    }
                }
            }

            var payload = "{\"mode\":\"bundle\",\"projectName\":\"" + EscapeJson(projectName) +
                          "\",\"projectPath\":\"" + EscapeJson(projectPath) +
                          "\",\"bundleId\":\"" + EscapeJson(bundleId) + "\"}";

            using (var request = new UnityWebRequest(BaseUrl + "/dev/reload-app", UnityWebRequest.kHttpVerbPOST))
            {
                request.uploadHandler = new UploadHandlerRaw(Encoding.UTF8.GetBytes(payload));
                request.downloadHandler = new DownloadHandlerBuffer();
                request.timeout = 120;
                ApplyAuth(request);
                request.SetRequestHeader("Content-Type", "application/json");
                yield return request.SendWebRequest();

                if (HasError(request))
                {
                    onError?.Invoke(request.error);
                    yield break;
                }

                var text = request.downloadHandler != null ? request.downloadHandler.text : string.Empty;
                var ack = string.IsNullOrEmpty(text)
                    ? new YaverReloadAck { ok = true, acknowledged = true, mode = "bundle", message = "Reload request acknowledged." }
                    : JsonUtility.FromJson<YaverReloadAck>(text);
                if (ack == null)
                {
                    ack = new YaverReloadAck { ok = true, acknowledged = true, mode = "bundle", message = "Reload request acknowledged." };
                }

                onComplete?.Invoke(ack);
            }
        }

        public IEnumerator StartVibingCoroutine(string prompt, string projectName, string projectPath, string bundleId, Action<YaverVibingResult> onComplete, Action<string> onError = null)
        {
            var payload = "{\"prompt\":\"" + EscapeJson(prompt) +
                          "\",\"projectName\":\"" + EscapeJson(projectName) +
                          "\",\"projectPath\":\"" + EscapeJson(projectPath) +
                          "\",\"bundleId\":\"" + EscapeJson(bundleId) + "\"}";

            using (var request = new UnityWebRequest(BaseUrl + "/vibing/execute", UnityWebRequest.kHttpVerbPOST))
            {
                request.uploadHandler = new UploadHandlerRaw(Encoding.UTF8.GetBytes(payload));
                request.downloadHandler = new DownloadHandlerBuffer();
                request.timeout = 60;
                ApplyAuth(request);
                request.SetRequestHeader("Content-Type", "application/json");
                yield return request.SendWebRequest();

                if (HasError(request))
                {
                    onError?.Invoke(request.error);
                    yield break;
                }

                onComplete?.Invoke(JsonUtility.FromJson<YaverVibingResult>(request.downloadHandler.text));
            }
        }

        public IEnumerator RunUnityTestsCoroutine(string projectName, string projectPath, string testMode, Action<YaverUnityRunResult> onComplete, Action<string> onError = null)
        {
            var payload = "{\"projectName\":\"" + EscapeJson(projectName) +
                          "\",\"projectPath\":\"" + EscapeJson(projectPath) +
                          "\",\"testMode\":\"" + EscapeJson(testMode) + "\"}";
            yield return PostUnityRunCoroutine("/unity/test", payload, onComplete, onError);
        }

        public IEnumerator BuildUnityCoroutine(string projectName, string projectPath, string buildTarget, string executeMethod, string outputPath, Action<YaverUnityRunResult> onComplete, Action<string> onError = null)
        {
            var payload = "{\"projectName\":\"" + EscapeJson(projectName) +
                          "\",\"projectPath\":\"" + EscapeJson(projectPath) +
                          "\",\"buildTarget\":\"" + EscapeJson(buildTarget) +
                          "\",\"executeMethod\":\"" + EscapeJson(executeMethod) +
                          "\",\"outputPath\":\"" + EscapeJson(outputPath) + "\"}";
            yield return PostUnityRunCoroutine("/unity/build", payload, onComplete, onError);
        }

        public IEnumerator RelaunchUnityCoroutine(string projectName, string projectPath, string executablePath, Action<YaverUnityRunResult> onComplete, Action<string> onError = null)
        {
            var payload = "{\"projectName\":\"" + EscapeJson(projectName) +
                          "\",\"projectPath\":\"" + EscapeJson(projectPath) +
                          "\",\"executablePath\":\"" + EscapeJson(executablePath) + "\"}";
            yield return PostUnityRunCoroutine("/unity/relaunch", payload, onComplete, onError);
        }

        public IEnumerator ListenForCommandsCoroutine(string deviceId, string appName, Action<YaverAgentCommand> onCommand)
        {
            var url = BaseUrl + "/blackbox/command-stream?device=" + UnityWebRequest.EscapeURL(deviceId ?? string.Empty);
            while (true)
            {
                using (var request = new UnityWebRequest(url, UnityWebRequest.kHttpVerbGET))
                {
                    var handler = new YaverSseDownloadHandler(raw =>
                    {
                        var envelope = JsonUtility.FromJson<YaverCommandEnvelope>(raw);
                        if (envelope != null && envelope.type == "command" && envelope.command != null)
                        {
                            onCommand?.Invoke(envelope.command);
                        }
                    });

                    request.downloadHandler = handler;
                    request.timeout = 0;
                    ApplyAuth(request);
                    request.SetRequestHeader("Accept", "text/event-stream");
                    request.SetRequestHeader("X-Device-ID", deviceId ?? string.Empty);
                    request.SetRequestHeader("X-App-Name", appName ?? string.Empty);
                    yield return request.SendWebRequest();
                }

                yield return new WaitForSeconds(2f);
            }
        }

        private void ApplyAuth(UnityWebRequest request)
        {
            if (!string.IsNullOrEmpty(AuthToken))
            {
                request.SetRequestHeader("Authorization", "Bearer " + AuthToken);
            }
        }

        private IEnumerator PostUnityRunCoroutine(string path, string payload, Action<YaverUnityRunResult> onComplete, Action<string> onError)
        {
            using (var request = new UnityWebRequest(BaseUrl + path, UnityWebRequest.kHttpVerbPOST))
            {
                request.uploadHandler = new UploadHandlerRaw(Encoding.UTF8.GetBytes(payload));
                request.downloadHandler = new DownloadHandlerBuffer();
                request.timeout = 300;
                ApplyAuth(request);
                request.SetRequestHeader("Content-Type", "application/json");
                yield return request.SendWebRequest();

                if (HasError(request))
                {
                    var text = request.downloadHandler != null ? request.downloadHandler.text : string.Empty;
                    onError?.Invoke(string.IsNullOrEmpty(text) ? request.error : text);
                    yield break;
                }

                onComplete?.Invoke(JsonUtility.FromJson<YaverUnityRunResult>(request.downloadHandler.text));
            }
        }

        private static bool HasError(UnityWebRequest request)
        {
#if UNITY_2020_2_OR_NEWER
            return request.result != UnityWebRequest.Result.Success;
#else
            return request.isNetworkError || request.isHttpError;
#endif
        }

        private static string EscapeJson(string value)
        {
            if (string.IsNullOrEmpty(value))
            {
                return string.Empty;
            }

            return value
                .Replace("\\", "\\\\")
                .Replace("\"", "\\\"")
                .Replace("\n", "\\n")
                .Replace("\r", "\\r")
                .Replace("\t", "\\t");
        }
    }
}
