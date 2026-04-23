using UnityEngine;

namespace Yaver.Feedback.Examples
{
    public sealed class YaverExampleBootstrap : MonoBehaviour
    {
        [SerializeField] private string agentUrl = "http://192.168.1.10:18080";
        [SerializeField] private string authToken = "";

        private void Awake()
        {
            var config = new YaverFeedbackConfig
            {
                AgentUrl = agentUrl,
                AuthToken = authToken,
                AppName = Application.productName,
                ProjectName = Application.productName,
                BuildVersion = Application.version,
                AutoStartBlackBox = true,
                CaptureUnityLogs = true
            };

            YaverFeedback.Initialize(config);
            YaverFeedback.CommandReceived += YaverFeedback.ExecuteDefaultCommand;
        }

        private void OnDestroy()
        {
            YaverFeedback.CommandReceived -= YaverFeedback.ExecuteDefaultCommand;
        }
    }
}
