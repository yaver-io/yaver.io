using System.Collections;
using UnityEngine;
using Yaver.Feedback;

public sealed class YaverContentReloadDemo : MonoBehaviour
{
    [SerializeField] private YaverGameConfigApplier gameConfigApplier;
    [SerializeField] private string lastContentPayload = "No content refresh yet.";

    private void OnEnable()
    {
        YaverFeedback.ContentRefreshRequested += OnContentRefreshRequested;
        YaverFeedback.ReloadRequested += OnReloadRequested;
        YaverFeedback.ReloadBundleRequested += OnReloadBundleRequested;
    }

    private void OnDisable()
    {
        YaverFeedback.ContentRefreshRequested -= OnContentRefreshRequested;
        YaverFeedback.ReloadRequested -= OnReloadRequested;
        YaverFeedback.ReloadBundleRequested -= OnReloadBundleRequested;
    }

    private void OnContentRefreshRequested(string contentUrl)
    {
        StartCoroutine(RefreshContent(contentUrl));
    }

    private void OnReloadRequested(YaverAgentCommand command)
    {
        Debug.Log("Yaver custom reload requested: " + (command != null ? command.command : "<none>"));
    }

    private void OnReloadBundleRequested(string bundleUrl, string assetsUrl)
    {
        Debug.Log("Yaver bundle reload requested. bundle=" + bundleUrl + " assets=" + assetsUrl);
    }

    private IEnumerator RefreshContent(string contentUrl)
    {
        YaverBlackBox.State("content-refresh-requested");
        yield return YaverFeedback.DownloadTextCoroutine(
            contentUrl,
            onComplete: payload =>
            {
                lastContentPayload = string.IsNullOrEmpty(payload) ? "<empty>" : payload;
                if (gameConfigApplier != null)
                {
                    gameConfigApplier.ApplyPayload(lastContentPayload);
                }
                Debug.Log("Yaver content refresh applied: " + lastContentPayload);
            },
            onError: error => Debug.LogError("Yaver content refresh failed: " + error)
        );
    }

    public string GetLastContentPayload()
    {
        return lastContentPayload;
    }
}
