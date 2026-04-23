using System.Collections;
using UnityEngine;
using Yaver.Feedback;

public sealed class YaverFeedbackDemo : MonoBehaviour
{
    [TextArea]
    [SerializeField] private string feedbackNote = "Player got stuck after tapping retry on level 3.";
    [TextArea]
    [SerializeField] private string vibingPrompt = "Continue the current Unity mobile game session until the active objective is complete. If you change behavior, add or update tests and run the relevant checks when feasible.";
    [SerializeField] private bool preferDevReload;
    [SerializeField] private string unityTestMode = "EditMode";

    public void CaptureMarker()
    {
        YaverBlackBox.State("manual-marker");
        Debug.Log("Yaver marker captured.");
    }

    public void CaptureScreenshot()
    {
        var path = YaverFeedback.CaptureScreenshot();
        Debug.Log("Screenshot queued: " + path);
    }

    public void SendFeedback()
    {
        StartCoroutine(SendFeedbackFlow());
    }

    public void StartVibing()
    {
        StartCoroutine(YaverFeedback.StartVibingCoroutine(
            vibingPrompt,
            onComplete: result => Debug.Log("Yaver vibing task: " + (result != null ? result.taskId : "<none>")),
            onError: error => Debug.LogError("Yaver vibing failed: " + error)
        ));
    }

    public void ReloadOrRedeploy()
    {
        StartCoroutine(YaverFeedback.ReloadCoroutine(
            preferDevReload,
            onComplete: ack => Debug.Log("Yaver reload ack: " + (ack != null ? ack.message : "<none>")),
            onError: error => Debug.LogError("Yaver reload failed: " + error)
        ));
    }

    public void RunUnityTests()
    {
        StartCoroutine(YaverFeedback.RunUnityTestsCoroutine(
            unityTestMode,
            onComplete: result => Debug.Log("Yaver Unity tests: " + (result != null ? result.summary : "<none>")),
            onError: error => Debug.LogError("Yaver Unity tests failed: " + error)
        ));
    }

    public void BuildUnityPlayer()
    {
        StartCoroutine(YaverFeedback.BuildConfiguredUnityCoroutine(
            onComplete: result => Debug.Log("Yaver Unity build: " + (result != null ? result.summary : "<none>")),
            onError: error => Debug.LogError("Yaver Unity build failed: " + error)
        ));
    }

    public void LaunchUnityPlayer()
    {
        StartCoroutine(YaverFeedback.RelaunchConfiguredUnityCoroutine(
            onComplete: result => Debug.Log("Yaver Unity relaunch: " + (result != null ? result.summary : "<none>")),
            onError: error => Debug.LogError("Yaver Unity relaunch failed: " + error)
        ));
    }

    private IEnumerator SendFeedbackFlow()
    {
        YaverBlackBox.State("feedback-demo-send");
        YaverFeedback.CaptureScreenshot();
        yield return YaverFeedback.SendFeedbackCoroutine(
            feedbackNote,
            metadataJson: "{\"source\":\"unity-test-app\"}",
            onComplete: feedbackId =>
            {
                Debug.Log("Yaver feedback uploaded: " + feedbackId);
                if (!string.IsNullOrEmpty(feedbackId))
                {
                    StartCoroutine(TriggerFix(feedbackId));
                }
            },
            onError: error => Debug.LogError("Yaver feedback failed: " + error)
        );
    }

    private IEnumerator TriggerFix(string feedbackId)
    {
        yield return YaverFeedback.TriggerFixFromFeedbackCoroutine(
            feedbackId,
            onComplete: result => Debug.Log("Yaver fix task: " + (result != null ? result.taskId : "<none>")),
            onError: error => Debug.LogError("Yaver fix trigger failed: " + error)
        );
    }
}
