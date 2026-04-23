using System;
using System.Collections;
using System.Text;
using UnityEngine;
using UnityEngine.Networking;

namespace Yaver.Feedback
{
    public static class YaverAuth
    {
        private const string StoredTokenKey = "yaver_feedback_auth_token";
        private const string StoredUserJsonKey = "yaver_feedback_auth_user";

        public static YaverAuthUser CurrentUser { get; private set; }
        public static bool IsAuthenticated => !string.IsNullOrEmpty(StoredToken);

        public static event Action<YaverAuthUser> AuthChanged;

        public static string StoredToken
        {
            get => PlayerPrefs.GetString(StoredTokenKey, string.Empty);
            private set
            {
                if (string.IsNullOrEmpty(value))
                {
                    PlayerPrefs.DeleteKey(StoredTokenKey);
                }
                else
                {
                    PlayerPrefs.SetString(StoredTokenKey, value);
                }
            }
        }

        public static void Restore()
        {
            var raw = PlayerPrefs.GetString(StoredUserJsonKey, string.Empty);
            CurrentUser = string.IsNullOrEmpty(raw) ? null : JsonUtility.FromJson<YaverAuthUser>(raw);
        }

        public static void SignOut()
        {
            StoredToken = string.Empty;
            CurrentUser = null;
            PlayerPrefs.DeleteKey(StoredUserJsonKey);
            AuthChanged?.Invoke(null);
        }

        public static void BeginOAuthSignIn(string provider, YaverFeedbackConfig config)
        {
            if (string.IsNullOrEmpty(provider) || config == null)
            {
                return;
            }

            var authUrl = (config.WebBaseUrl ?? string.Empty).TrimEnd('/') + "/api/auth/oauth/" + UnityWebRequest.EscapeURL(provider) + "?client=mobile";
            Application.OpenURL(authUrl);
        }

        public static bool TryConsumeOAuthCallback(string url)
        {
            if (string.IsNullOrEmpty(url) || url.IndexOf("oauth-callback", StringComparison.OrdinalIgnoreCase) < 0)
            {
                return false;
            }

            var token = ReadQueryParam(url, "token");
            if (string.IsNullOrEmpty(token))
            {
                return false;
            }

            StoredToken = token;
            return true;
        }

        public static IEnumerator ValidateStoredTokenCoroutine(YaverFeedbackConfig config, Action<YaverAuthUser> onComplete)
        {
            if (string.IsNullOrEmpty(StoredToken))
            {
                onComplete?.Invoke(null);
                yield break;
            }

            yield return ValidateTokenCoroutine(StoredToken, config, onComplete);
        }

        public static IEnumerator ValidateTokenCoroutine(string token, YaverFeedbackConfig config, Action<YaverAuthUser> onComplete)
        {
            if (string.IsNullOrEmpty(token) || config == null || string.IsNullOrEmpty(config.ConvexSiteUrl))
            {
                onComplete?.Invoke(null);
                yield break;
            }

            using (var request = UnityWebRequest.Get(config.ConvexSiteUrl.TrimEnd('/') + "/auth/validate"))
            {
                request.timeout = 8;
                request.SetRequestHeader("Authorization", "Bearer " + token);
                yield return request.SendWebRequest();

                if (HasError(request))
                {
                    onComplete?.Invoke(null);
                    yield break;
                }

                var response = JsonUtility.FromJson<YaverValidateResponse>(request.downloadHandler.text);
                if (response == null || response.user == null)
                {
                    onComplete?.Invoke(null);
                    yield break;
                }

                CurrentUser = new YaverAuthUser
                {
                    id = string.IsNullOrEmpty(response.user.userId) ? response.user.id : response.user.userId,
                    email = response.user.email,
                    name = string.IsNullOrEmpty(response.user.fullName) ? response.user.name : response.user.fullName,
                    provider = response.user.provider,
                    avatarUrl = response.user.avatarUrl
                };
                StoredToken = token;
                PlayerPrefs.SetString(StoredUserJsonKey, JsonUtility.ToJson(CurrentUser));
                AuthChanged?.Invoke(CurrentUser);
                onComplete?.Invoke(CurrentUser);
            }
        }

        public static IEnumerator LoginWithEmailCoroutine(string email, string password, YaverFeedbackConfig config, Action<YaverAuthResult> onComplete, Action<string> onError)
        {
            var payload = "{\"email\":\"" + EscapeJson(email) + "\",\"password\":\"" + EscapeJson(password) + "\"}";
            yield return PostAuthCoroutine(config, "/auth/login", payload, onComplete, onError);
        }

        public static IEnumerator SignupWithEmailCoroutine(string fullName, string email, string password, YaverFeedbackConfig config, Action<YaverAuthResult> onComplete, Action<string> onError)
        {
            var payload = "{\"fullName\":\"" + EscapeJson(fullName) + "\",\"email\":\"" + EscapeJson(email) + "\",\"password\":\"" + EscapeJson(password) + "\"}";
            yield return PostAuthCoroutine(config, "/auth/signup", payload, onComplete, onError);
        }

        public static IEnumerator DiscoverAgentCoroutine(YaverFeedbackConfig config, Action<YaverDiscoveryResult> onComplete)
        {
            if (config == null || string.IsNullOrEmpty(config.ConvexSiteUrl) || string.IsNullOrEmpty(StoredToken))
            {
                onComplete?.Invoke(null);
                yield break;
            }

            using (var request = UnityWebRequest.Get(config.ConvexSiteUrl.TrimEnd('/') + "/devices/list"))
            {
                request.timeout = 8;
                request.SetRequestHeader("Authorization", "Bearer " + StoredToken);
                yield return request.SendWebRequest();

                if (HasError(request))
                {
                    onComplete?.Invoke(null);
                    yield break;
                }

                var text = request.downloadHandler.text;
                var envelope = JsonUtility.FromJson<YaverDeviceListEnvelope>(text);
                var devices = envelope != null && envelope.devices != null ? envelope.devices : JsonArrayHelper.FromJson<YaverRemoteDevice>(text);
                if (devices == null)
                {
                    onComplete?.Invoke(null);
                    yield break;
                }

                for (var i = 0; i < devices.Length; i++)
                {
                    var device = devices[i];
                    if (device == null || !device.isOnline || string.IsNullOrEmpty(device.quicHost))
                    {
                        continue;
                    }

                    var port = device.httpPort > 0 ? device.httpPort : (device.quicPort > 0 ? device.quicPort : 18080);
                    YaverDiscoveryResult discovered = null;
                    yield return YaverDiscovery.ProbeCoroutine("http://" + device.quicHost + ":" + port, result => discovered = result);
                    if (discovered != null)
                    {
                        onComplete?.Invoke(discovered);
                        yield break;
                    }
                }
            }

            onComplete?.Invoke(null);
        }

        private static IEnumerator PostAuthCoroutine(YaverFeedbackConfig config, string path, string payload, Action<YaverAuthResult> onComplete, Action<string> onError)
        {
            if (config == null || string.IsNullOrEmpty(config.ConvexSiteUrl))
            {
                onError?.Invoke("Missing Convex site URL.");
                yield break;
            }

            using (var request = new UnityWebRequest(config.ConvexSiteUrl.TrimEnd('/') + path, UnityWebRequest.kHttpVerbPOST))
            {
                request.uploadHandler = new UploadHandlerRaw(Encoding.UTF8.GetBytes(payload));
                request.downloadHandler = new DownloadHandlerBuffer();
                request.timeout = 10;
                request.SetRequestHeader("Content-Type", "application/json");
                yield return request.SendWebRequest();

                if (HasError(request))
                {
                    onError?.Invoke(string.IsNullOrEmpty(request.downloadHandler.text) ? request.error : request.downloadHandler.text);
                    yield break;
                }

                var result = JsonUtility.FromJson<YaverAuthResult>(request.downloadHandler.text);
                if (result == null)
                {
                    onError?.Invoke("Authentication failed.");
                    yield break;
                }

                if (result.requires2fa)
                {
                    onError?.Invoke("2FA is enabled on this account. Use OAuth instead.");
                    yield break;
                }

                StoredToken = result.token;
                onComplete?.Invoke(result);
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

        private static string ReadQueryParam(string url, string key)
        {
            var match = key + "=";
            var start = url.IndexOf(match, StringComparison.OrdinalIgnoreCase);
            if (start < 0)
            {
                return string.Empty;
            }

            start += match.Length;
            var end = url.IndexOf('&', start);
            if (end < 0)
            {
                end = url.Length;
            }

            return UnityWebRequest.UnEscapeURL(url.Substring(start, Mathf.Max(0, end - start)));
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

    internal static class JsonArrayHelper
    {
        [Serializable]
        private sealed class Wrapper<T>
        {
            public T[] items;
        }

        public static T[] FromJson<T>(string json)
        {
            if (string.IsNullOrEmpty(json) || json[0] != '[')
            {
                return null;
            }

            var result = JsonUtility.FromJson<Wrapper<T>>("{\"items\":" + json + "}");
            return result != null ? result.items : null;
        }
    }
}
