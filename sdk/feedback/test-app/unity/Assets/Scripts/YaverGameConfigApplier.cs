using UnityEngine;
using Yaver.Feedback;

public sealed class YaverGameConfigApplier : MonoBehaviour
{
    [SerializeField] private YaverContentRefreshHandler refreshHandler;
    [SerializeField] private YaverGameConfig currentConfig = new YaverGameConfig();
    [SerializeField] private string lastRawPayload = string.Empty;

    public YaverGameConfig CurrentConfig => currentConfig;
    public string LastRawPayload => lastRawPayload;

    private void Awake()
    {
        if (refreshHandler == null)
        {
            refreshHandler = GetComponent<YaverContentRefreshHandler>();
        }
    }

    private void OnEnable()
    {
        if (refreshHandler != null)
        {
            refreshHandler.Bind();
        }

        YaverFeedback.ContentRefreshRequested += OnContentRefreshRequested;
    }

    private void OnDisable()
    {
        YaverFeedback.ContentRefreshRequested -= OnContentRefreshRequested;
        if (refreshHandler != null)
        {
            refreshHandler.Unbind();
        }
    }

    private void OnContentRefreshRequested(string _)
    {
        ApplyPayload(refreshHandler != null ? refreshHandler.LastPayload : string.Empty);
    }

    public void ApplyPayload(string payload)
    {
        lastRawPayload = payload ?? string.Empty;
        if (string.IsNullOrEmpty(lastRawPayload))
        {
            Debug.LogWarning("Yaver game config payload was empty.");
            return;
        }

        try
        {
            var parsed = JsonUtility.FromJson<YaverGameConfig>(lastRawPayload);
            if (parsed == null)
            {
                Debug.LogWarning("Yaver game config payload could not be parsed.");
                return;
            }

            currentConfig = parsed;
            YaverBlackBox.State("game-config-applied");
            Debug.Log("Yaver game config applied: theme=" + currentConfig.theme + ", speed=" + currentConfig.playerSpeed);
        }
        catch (System.Exception error)
        {
            Debug.LogError("Yaver game config apply failed: " + error.Message);
        }
    }
}
