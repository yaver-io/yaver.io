using System;
using System.Collections;
using System.Collections.Generic;
using UnityEngine;

namespace Yaver.Feedback
{
    public static class YaverBlackBox
    {
        private static readonly List<YaverBlackBoxEvent> Buffer = new List<YaverBlackBoxEvent>(128);

        private static YaverP2PClient _client;
        private static string _deviceId = string.Empty;
        private static string _appName = string.Empty;
        private static int _maxBufferSize = 50;
        private static float _flushIntervalSeconds = 2f;
        private static bool _started;
        private static Coroutine _flushLoop;

        public static bool IsStreaming => _started;

        internal static void Start(YaverP2PClient client, YaverFeedbackConfig config)
        {
            if (client == null)
            {
                return;
            }

            _client = client;
            _deviceId = string.IsNullOrEmpty(config.DeviceId) ? SystemInfo.deviceUniqueIdentifier : config.DeviceId;
            _appName = string.IsNullOrEmpty(config.AppName) ? Application.productName : config.AppName;
            _maxBufferSize = Mathf.Max(10, config.BlackBoxMaxBufferSize);
            _flushIntervalSeconds = Mathf.Max(0.5f, config.BlackBoxFlushIntervalSeconds);

            if (_started)
            {
                return;
            }

            _started = true;
            Lifecycle("black-box-started");
            _flushLoop = YaverRuntime.Instance.StartCoroutine(FlushLoop());
        }

        public static void Stop()
        {
            if (!_started)
            {
                return;
            }

            Lifecycle("black-box-stopped");
            _started = false;
            if (_flushLoop != null)
            {
                YaverRuntime.Instance.StopCoroutine(_flushLoop);
                _flushLoop = null;
            }

            YaverRuntime.Instance.StartCoroutine(FlushCoroutine());
        }

        public static void Log(string message, string source = null, string metadataJson = null)
        {
            Push(new YaverBlackBoxEvent
            {
                Type = "log",
                Level = "info",
                Message = message,
                Timestamp = DateTimeOffset.UtcNow.ToUnixTimeMilliseconds(),
                Source = source,
                MetadataJson = metadataJson
            });
        }

        public static void Warn(string message, string source = null, string metadataJson = null)
        {
            Push(new YaverBlackBoxEvent
            {
                Type = "log",
                Level = "warn",
                Message = message,
                Timestamp = DateTimeOffset.UtcNow.ToUnixTimeMilliseconds(),
                Source = source,
                MetadataJson = metadataJson
            });
        }

        public static void Error(string message, string source = null, string metadataJson = null)
        {
            Push(new YaverBlackBoxEvent
            {
                Type = "log",
                Level = "error",
                Message = message,
                Timestamp = DateTimeOffset.UtcNow.ToUnixTimeMilliseconds(),
                Source = source,
                MetadataJson = metadataJson
            });
        }

        public static void CaptureException(Exception error, bool isFatal = false, string metadataJson = null)
        {
            if (error == null)
            {
                return;
            }

            Push(new YaverBlackBoxEvent
            {
                Type = "error",
                Message = error.Message,
                Timestamp = DateTimeOffset.UtcNow.ToUnixTimeMilliseconds(),
                Stack = error.ToString(),
                IsFatal = isFatal,
                MetadataJson = metadataJson
            });

            YaverFeedback.AttachError(error.Message, error.ToString(), isFatal, metadataJson);
        }

        public static void Navigation(string route, string previousRoute = null, string metadataJson = null)
        {
            Push(new YaverBlackBoxEvent
            {
                Type = "navigation",
                Message = route,
                Timestamp = DateTimeOffset.UtcNow.ToUnixTimeMilliseconds(),
                Route = route,
                PreviousRoute = previousRoute,
                MetadataJson = metadataJson
            });
        }

        public static void Lifecycle(string message, string metadataJson = null)
        {
            Push(new YaverBlackBoxEvent
            {
                Type = "lifecycle",
                Message = message,
                Timestamp = DateTimeOffset.UtcNow.ToUnixTimeMilliseconds(),
                MetadataJson = metadataJson
            });
        }

        public static void State(string description, string metadataJson = null)
        {
            Push(new YaverBlackBoxEvent
            {
                Type = "state",
                Message = description,
                Timestamp = DateTimeOffset.UtcNow.ToUnixTimeMilliseconds(),
                MetadataJson = metadataJson
            });
        }

        internal static void CaptureUnityLog(string condition, string stackTrace, LogType type)
        {
            Push(new YaverBlackBoxEvent
            {
                Type = type == LogType.Exception ? "error" : "log",
                Level = MapLogLevel(type),
                Message = condition,
                Timestamp = DateTimeOffset.UtcNow.ToUnixTimeMilliseconds(),
                Stack = stackTrace,
                IsFatal = type == LogType.Exception
            });
        }

        internal static IEnumerator FlushCoroutine()
        {
            if (_client == null || Buffer.Count == 0)
            {
                yield break;
            }

            var payload = Buffer.ToArray();
            Buffer.Clear();
            yield return _client.SendBlackBoxBatchCoroutine(payload, _deviceId, _appName);
        }

        private static void Push(YaverBlackBoxEvent item)
        {
            if (!_started || item == null)
            {
                return;
            }

            Buffer.Add(item);
            if (Buffer.Count >= _maxBufferSize)
            {
                YaverRuntime.Instance.StartCoroutine(FlushCoroutine());
            }
        }

        private static IEnumerator FlushLoop()
        {
            while (_started)
            {
                yield return new WaitForSeconds(_flushIntervalSeconds);
                yield return FlushCoroutine();
            }
        }

        private static string MapLogLevel(LogType type)
        {
            switch (type)
            {
                case LogType.Warning:
                    return "warn";
                case LogType.Error:
                case LogType.Assert:
                case LogType.Exception:
                    return "error";
                default:
                    return "info";
            }
        }
    }
}
