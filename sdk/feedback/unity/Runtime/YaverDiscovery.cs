using System;
using System.Collections;
using UnityEngine;
using UnityEngine.Networking;

namespace Yaver.Feedback
{
    public static class YaverDiscovery
    {
        private const string StoredAgentUrlKey = "yaver_feedback_agent_url";

        public static string StoredAgentUrl
        {
            get => PlayerPrefs.GetString(StoredAgentUrlKey, string.Empty);
            set
            {
                if (string.IsNullOrEmpty(value))
                {
                    PlayerPrefs.DeleteKey(StoredAgentUrlKey);
                }
                else
                {
                    PlayerPrefs.SetString(StoredAgentUrlKey, value);
                }
            }
        }

        public static IEnumerator ProbeCoroutine(string url, Action<YaverDiscoveryResult> onComplete)
        {
            var normalized = NormalizeUrl(url);
            var startedAt = Time.realtimeSinceStartup;
            using (var request = UnityWebRequest.Get(normalized + "/health"))
            {
                request.timeout = 3;
                yield return request.SendWebRequest();

#if UNITY_2020_2_OR_NEWER
                var ok = request.result == UnityWebRequest.Result.Success;
#else
                var ok = !request.isNetworkError && !request.isHttpError;
#endif
                if (!ok)
                {
                    onComplete?.Invoke(null);
                    yield break;
                }

                var info = new YaverDiscoveryResult
                {
                    Url = normalized,
                    Hostname = ParseHealthField(request.downloadHandler.text, "hostname"),
                    Version = ParseHealthField(request.downloadHandler.text, "version"),
                    LatencyMs = Mathf.RoundToInt((Time.realtimeSinceStartup - startedAt) * 1000f)
                };

                StoredAgentUrl = normalized;
                onComplete?.Invoke(info);
            }
        }

        public static IEnumerator DiscoverCoroutine(Action<YaverDiscoveryResult> onComplete)
        {
            var candidates = new[]
            {
                StoredAgentUrl,
                "http://127.0.0.1:18080",
                "http://localhost:18080"
            };

            foreach (var candidate in candidates)
            {
                if (string.IsNullOrEmpty(candidate))
                {
                    continue;
                }

                YaverDiscoveryResult result = null;
                yield return ProbeCoroutine(candidate, discovered => result = discovered);
                if (result != null)
                {
                    onComplete?.Invoke(result);
                    yield break;
                }
            }

            onComplete?.Invoke(null);
        }

        public static string NormalizeUrl(string url)
        {
            var value = (url ?? string.Empty).Trim();
            if (string.IsNullOrEmpty(value))
            {
                return string.Empty;
            }

            if (!value.StartsWith("http://", StringComparison.OrdinalIgnoreCase) &&
                !value.StartsWith("https://", StringComparison.OrdinalIgnoreCase))
            {
                value = "http://" + value;
            }

            return value.TrimEnd('/');
        }

        private static string ParseHealthField(string json, string field)
        {
            if (string.IsNullOrEmpty(json))
            {
                return string.Empty;
            }

            var token = "\"" + field + "\"";
            var start = json.IndexOf(token, StringComparison.OrdinalIgnoreCase);
            if (start < 0)
            {
                return string.Empty;
            }

            start = json.IndexOf(':', start);
            if (start < 0)
            {
                return string.Empty;
            }

            start += 1;
            while (start < json.Length && (json[start] == ' ' || json[start] == '"'))
            {
                start++;
            }

            var end = start;
            while (end < json.Length && json[end] != '"' && json[end] != ',' && json[end] != '}')
            {
                end++;
            }

            return json.Substring(start, Mathf.Max(0, end - start)).Trim();
        }
    }
}
