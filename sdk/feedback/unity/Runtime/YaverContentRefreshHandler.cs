using System;
using System.Collections;
using UnityEngine;
using UnityEngine.Events;

namespace Yaver.Feedback
{
    [Serializable]
    public sealed class YaverStringEvent : UnityEvent<string>
    {
    }

    public sealed class YaverContentRefreshHandler : MonoBehaviour
    {
        [SerializeField] private bool autoBind = true;
        [SerializeField] private bool captureBundleReloads = true;
        [SerializeField] private string lastPayload = string.Empty;
        [SerializeField] private string lastSourceUrl = string.Empty;

        [Header("Callbacks")]
        [SerializeField] private YaverStringEvent onContentUpdated = new YaverStringEvent();
        [SerializeField] private YaverStringEvent onContentRefreshError = new YaverStringEvent();

        public string LastPayload => lastPayload;
        public string LastSourceUrl => lastSourceUrl;

        private void OnEnable()
        {
            if (!autoBind)
            {
                return;
            }

            Bind();
        }

        private void OnDisable()
        {
            if (!autoBind)
            {
                return;
            }

            Unbind();
        }

        public void Bind()
        {
            YaverFeedback.ContentRefreshRequested -= HandleContentRefreshRequested;
            YaverFeedback.ContentRefreshRequested += HandleContentRefreshRequested;
            if (captureBundleReloads)
            {
                YaverFeedback.ReloadBundleRequested -= HandleBundleRefreshRequested;
                YaverFeedback.ReloadBundleRequested += HandleBundleRefreshRequested;
            }
        }

        public void Unbind()
        {
            YaverFeedback.ContentRefreshRequested -= HandleContentRefreshRequested;
            YaverFeedback.ReloadBundleRequested -= HandleBundleRefreshRequested;
        }

        private void HandleContentRefreshRequested(string contentUrl)
        {
            StartCoroutine(RefreshContent(contentUrl));
        }

        private void HandleBundleRefreshRequested(string bundleUrl, string assetsUrl)
        {
            if (!string.IsNullOrEmpty(bundleUrl))
            {
                StartCoroutine(RefreshContent(bundleUrl));
            }
        }

        private IEnumerator RefreshContent(string contentUrl)
        {
            lastSourceUrl = contentUrl ?? string.Empty;
            YaverBlackBox.State("content-refresh-handler-requested");
            yield return YaverFeedback.DownloadTextCoroutine(
                contentUrl,
                onComplete: payload =>
                {
                    lastPayload = payload ?? string.Empty;
                    YaverBlackBox.State("content-refresh-handler-applied");
                    onContentUpdated.Invoke(lastPayload);
                },
                onError: error =>
                {
                    YaverBlackBox.Log("Content refresh failed: " + error, "YaverContentRefreshHandler");
                    onContentRefreshError.Invoke(error ?? "Content refresh failed.");
                }
            );
        }
    }
}
