using System.Collections;
using UnityEngine;
using UnityEngine.SceneManagement;

namespace Yaver.Feedback
{
    internal sealed class YaverOverlay : MonoBehaviour
    {
        private const float Margin = 12f;
        private const float PanelWidth = 360f;
        private const float ExpandedHeight = 548f;
        private const float CollapsedHeight = 56f;
        private const float BubbleSize = 52f;
        private const int MaxActivityCount = 8;

        private Rect _windowRect;
        private bool _expanded;
        private bool _busy;
        private bool _bubbleMode = true;
        private string _feedbackNote = "Describe the issue here.";
        private string _vibingPrompt = string.Empty;
        private string _status = "Idle";
        private string _email = string.Empty;
        private string _password = string.Empty;
        private string _fullName = string.Empty;
        private bool _signupMode;
        private Vector2 _noteScroll;
        private Vector2 _promptScroll;
        private Vector2 _activityScroll;
        private readonly System.Collections.Generic.List<string> _activity = new System.Collections.Generic.List<string>(MaxActivityCount);

        internal static YaverOverlay Ensure(YaverFeedbackConfig config)
        {
            var runtime = YaverRuntime.Instance;
            var overlay = runtime.GetComponent<YaverOverlay>();
            if (overlay == null)
            {
                overlay = runtime.gameObject.AddComponent<YaverOverlay>();
            }

            overlay.ApplyConfig(config);
            return overlay;
        }

        private void ApplyConfig(YaverFeedbackConfig config)
        {
            _expanded = !(config != null && config.StartOverlayCollapsed);
            _windowRect = new Rect(Margin, Margin, PanelWidth, _expanded ? ExpandedHeight : CollapsedHeight);
            if (string.IsNullOrEmpty(_vibingPrompt))
            {
                _vibingPrompt = YaverFeedback.BuildDefaultVibingPrompt();
            }
            PushActivity("overlay-ready");
        }

        private void Update()
        {
            var config = YaverFeedback.CurrentConfig;
            if (config == null)
            {
                return;
            }

            if (Input.GetKeyDown(config.ToggleOverlayKey))
            {
                _expanded = !_expanded;
                _windowRect.height = _expanded ? ExpandedHeight : CollapsedHeight;
            }
        }

        private void OnGUI()
        {
            if (!YaverFeedback.IsEnabled)
            {
                return;
            }

            GUI.depth = -1000;
            if (!_expanded && _bubbleMode)
            {
                _windowRect.width = BubbleSize;
                _windowRect.height = BubbleSize;
            }
            else
            {
                _windowRect.width = PanelWidth;
                _windowRect.height = _expanded ? ExpandedHeight : CollapsedHeight;
            }
            _windowRect = GUI.Window(GetInstanceID(), _windowRect, DrawWindow, "Yaver");
        }

        private void DrawWindow(int windowId)
        {
            var config = YaverFeedback.CurrentConfig;
            if (config == null)
            {
                return;
            }

            var lineY = 24f;
            if (!_expanded)
            {
                if (_bubbleMode)
                {
                    if (GUI.Button(new Rect(6f, 18f, 40f, 28f), "Y"))
                    {
                        _expanded = true;
                    }
                    GUI.DragWindow(new Rect(0f, 0f, _windowRect.width, _windowRect.height));
                    return;
                }

                if (GUI.Button(new Rect(8f, lineY, 72f, 24f), "Open"))
                {
                    _expanded = true;
                }
                GUI.Label(new Rect(88f, lineY + 4f, 240f, 20f), _status);
                GUI.DragWindow(new Rect(0f, 0f, _windowRect.width, 22f));
                return;
            }

            if (GUI.Button(new Rect(8f, lineY, 72f, 24f), "Hide"))
            {
                _expanded = false;
                _bubbleMode = true;
                return;
            }
            if (GUI.Button(new Rect(88f, lineY, 56f, 24f), _bubbleMode ? "Panel" : "Bubble"))
            {
                _bubbleMode = !_bubbleMode;
            }

            GUI.Label(new Rect(152f, lineY + 4f, 184f, 20f), SceneManager.GetActiveScene().name + " | " + _status);
            lineY += 32f;

            GUI.Label(new Rect(8f, lineY, 120f, 20f), YaverFeedback.IsAuthenticated ? "Authenticated" : "Authentication");
            lineY += 18f;
            if (!YaverFeedback.IsAuthenticated)
            {
                if (GUI.Button(new Rect(8f, lineY, 64f, 24f), "Google"))
                {
                    YaverFeedback.BeginOAuthSignIn("google");
                    SetStatus("Opened Google sign-in");
                }
                if (GUI.Button(new Rect(76f, lineY, 64f, 24f), "GitHub"))
                {
                    YaverFeedback.BeginOAuthSignIn("github");
                    SetStatus("Opened GitHub sign-in");
                }
                if (GUI.Button(new Rect(144f, lineY, 64f, 24f), "GitLab"))
                {
                    YaverFeedback.BeginOAuthSignIn("gitlab");
                    SetStatus("Opened GitLab sign-in");
                }
                if (GUI.Button(new Rect(212f, lineY, 64f, 24f), "Apple"))
                {
                    YaverFeedback.BeginOAuthSignIn("apple");
                    SetStatus("Opened Apple sign-in");
                }
                if (GUI.Button(new Rect(280f, lineY, 72f, 24f), "MS"))
                {
                    YaverFeedback.BeginOAuthSignIn("microsoft");
                    SetStatus("Opened Microsoft sign-in");
                }
                lineY += 32f;

                if (_signupMode)
                {
                    GUI.Label(new Rect(8f, lineY, 72f, 20f), "Name");
                    _fullName = GUI.TextField(new Rect(84f, lineY, 268f, 22f), _fullName ?? string.Empty);
                    lineY += 28f;
                }
                GUI.Label(new Rect(8f, lineY, 72f, 20f), "Email");
                _email = GUI.TextField(new Rect(84f, lineY, 268f, 22f), _email ?? string.Empty);
                lineY += 28f;
                GUI.Label(new Rect(8f, lineY, 72f, 20f), "Password");
                _password = GUI.PasswordField(new Rect(84f, lineY, 268f, 22f), _password ?? string.Empty, '*');
                lineY += 30f;

                GUI.enabled = !_busy;
                if (GUI.Button(new Rect(8f, lineY, 108f, 26f), _signupMode ? "Create" : "Login"))
                {
                    StartCoroutine(_signupMode ? Signup() : Login());
                }
                if (GUI.Button(new Rect(124f, lineY, 108f, 26f), _signupMode ? "Have account" : "Need account"))
                {
                    _signupMode = !_signupMode;
                }
                if (GUI.Button(new Rect(240f, lineY, 112f, 26f), "Connect"))
                {
                    StartCoroutine(ConnectAgent());
                }
                GUI.enabled = true;
                lineY += 36f;
            }
            else
            {
                var identity = YaverAuth.CurrentUser != null && !string.IsNullOrEmpty(YaverAuth.CurrentUser.email)
                    ? YaverAuth.CurrentUser.email
                    : "Signed in";
                GUI.Label(new Rect(8f, lineY, 240f, 20f), identity);
                if (GUI.Button(new Rect(280f, lineY - 2f, 72f, 24f), "Logout"))
                {
                    YaverFeedback.SignOut();
                    SetStatus("Signed out");
                }
                lineY += 28f;
                if (GUI.Button(new Rect(8f, lineY, 108f, 26f), "Connect"))
                {
                    StartCoroutine(ConnectAgent());
                }
                GUI.Label(new Rect(124f, lineY + 4f, 228f, 20f), YaverFeedback.Client != null ? "Agent connected" : "Agent not connected");
                lineY += 34f;
            }

            GUI.Label(new Rect(8f, lineY, 120f, 20f), "Feedback");
            lineY += 18f;
            _noteScroll = GUI.BeginScrollView(new Rect(8f, lineY, 344f, 56f), _noteScroll, new Rect(0f, 0f, 324f, 72f));
            _feedbackNote = GUI.TextArea(new Rect(0f, 0f, 324f, 72f), _feedbackNote ?? string.Empty);
            GUI.EndScrollView();
            lineY += 64f;

            if (GUI.Button(new Rect(8f, lineY, 108f, 28f), "Screenshot"))
            {
                var path = YaverFeedback.CaptureScreenshot();
                SetStatus("Queued screenshot");
                YaverBlackBox.State("overlay-screenshot");
                Debug.Log("Yaver screenshot queued: " + path);
            }

            GUI.enabled = !_busy;
            if (GUI.Button(new Rect(124f, lineY, 108f, 28f), "Send"))
            {
                StartCoroutine(SendFeedback());
            }

            if (GUI.Button(new Rect(240f, lineY, 112f, 28f), YaverFeedback.GetReloadActionLabel()))
            {
                StartCoroutine(RequestReload());
            }
            GUI.enabled = true;
            lineY += 36f;

            GUI.Label(new Rect(8f, lineY, 120f, 20f), "Vibing");
            lineY += 18f;
            _promptScroll = GUI.BeginScrollView(new Rect(8f, lineY, 344f, 56f), _promptScroll, new Rect(0f, 0f, 324f, 72f));
            _vibingPrompt = GUI.TextArea(new Rect(0f, 0f, 324f, 72f), _vibingPrompt ?? string.Empty);
            GUI.EndScrollView();
            lineY += 64f;

            GUI.enabled = !_busy;
            if (GUI.Button(new Rect(8f, lineY, 108f, 28f), "Vibe"))
            {
                StartCoroutine(StartVibing());
            }
            if (GUI.Button(new Rect(124f, lineY, 108f, 28f), "Marker"))
            {
                YaverBlackBox.State("overlay-marker");
                SetStatus("Captured marker");
            }
            if (YaverFeedback.IsDesktopRuntime && GUI.Button(new Rect(240f, lineY, 112f, 28f), "Tests"))
            {
                StartCoroutine(RunUnityTests());
            }
            GUI.enabled = true;
            lineY += 38f;

            if (YaverFeedback.IsDesktopRuntime)
            {
                GUI.Label(new Rect(8f, lineY, 120f, 20f), "Desktop");
                lineY += 18f;
                GUI.enabled = !_busy && YaverFeedback.HasUnityBuildConfiguration();
                if (GUI.Button(new Rect(8f, lineY, 108f, 28f), "Build"))
                {
                    StartCoroutine(BuildUnityPlayer());
                }
                GUI.enabled = !_busy && YaverFeedback.HasUnityDesktopExecutable();
                if (GUI.Button(new Rect(124f, lineY, 108f, 28f), "Launch"))
                {
                    StartCoroutine(RelaunchUnityPlayer());
                }
                GUI.enabled = true;
                GUI.Label(new Rect(240f, lineY + 4f, 112f, 20f), "runner");
                lineY += 38f;
            }

            GUI.Label(new Rect(8f, lineY, 120f, 20f), "Activity");
            lineY += 18f;
            _activityScroll = GUI.BeginScrollView(new Rect(8f, lineY, 344f, 82f), _activityScroll, new Rect(0f, 0f, 324f, Mathf.Max(82f, _activity.Count * 18f)));
            for (var i = 0; i < _activity.Count; i++)
            {
                GUI.Label(new Rect(0f, i * 18f, 324f, 18f), _activity[i]);
            }
            GUI.EndScrollView();

            GUI.DragWindow(new Rect(0f, 0f, _windowRect.width, 22f));
        }

        private IEnumerator SendFeedback()
        {
            _busy = true;
            SetStatus("Sending feedback");
            YaverBlackBox.State("overlay-send-feedback");
            yield return YaverFeedback.SendFeedbackAndFixCoroutine(
                _feedbackNote,
                metadataJson: "{\"source\":\"unity-overlay\"}",
                onComplete: feedbackId =>
                {
                    SetStatus(string.IsNullOrEmpty(feedbackId) ? "Feedback sent" : "Feedback " + feedbackId);
                },
                onError: error => { SetStatus(error); }
            );
            _busy = false;
        }

        private IEnumerator Login()
        {
            _busy = true;
            SetStatus("Logging in");
            yield return YaverFeedback.LoginWithEmailCoroutine(
                _email,
                _password,
                onComplete: result => { SetStatus("Logged in"); },
                onError: error => { SetStatus(error); }
            );
            _busy = false;
        }

        private IEnumerator Signup()
        {
            _busy = true;
            SetStatus("Creating account");
            yield return YaverFeedback.SignupWithEmailCoroutine(
                _fullName,
                _email,
                _password,
                onComplete: result => { SetStatus("Signed up"); },
                onError: error => { SetStatus(error); }
            );
            _busy = false;
        }

        private IEnumerator StartVibing()
        {
            _busy = true;
            SetStatus("Starting vibing");
            YaverBlackBox.State("overlay-start-vibing");
            yield return YaverFeedback.StartVibingCoroutine(
                _vibingPrompt,
                onComplete: result =>
                {
                    SetStatus(result != null && !string.IsNullOrEmpty(result.taskId) ? "Vibing " + result.taskId : "Vibing started");
                },
                onError: error => { SetStatus(error); }
            );
            _busy = false;
        }

        private IEnumerator RequestReload()
        {
            _busy = true;
            SetStatus("Requesting reload");
            YaverBlackBox.State("overlay-request-reload");
            yield return YaverFeedback.ReloadCoroutine(
                preferDevReload: false,
                onComplete: ack =>
                {
                    SetStatus(ack != null && !string.IsNullOrEmpty(ack.message) ? ack.message : "Reload requested");
                },
                onError: error => { SetStatus(error); }
            );
            _busy = false;
        }

        private IEnumerator ConnectAgent()
        {
            _busy = true;
            SetStatus("Connecting");
            yield return YaverFeedback.ConnectBestAgentCoroutine(ok =>
            {
                SetStatus(ok ? "Connected" : "No agent found");
            });
            _busy = false;
        }

        private IEnumerator RunUnityTests()
        {
            _busy = true;
            SetStatus("Running Unity tests");
            yield return YaverFeedback.RunUnityTestsCoroutine(
                "EditMode",
                onComplete: result =>
                {
                    SetStatus(YaverFeedback.FormatUnityRunSummary(result));
                },
                onError: error => { SetStatus(error); }
            );
            _busy = false;
        }

        private IEnumerator BuildUnityPlayer()
        {
            _busy = true;
            SetStatus("Building Unity player");
            yield return YaverFeedback.BuildConfiguredUnityCoroutine(
                onComplete: result =>
                {
                    SetStatus(YaverFeedback.FormatUnityRunSummary(result));
                },
                onError: error => { SetStatus(error); }
            );
            _busy = false;
        }

        private IEnumerator RelaunchUnityPlayer()
        {
            _busy = true;
            SetStatus("Launching Unity player");
            yield return YaverFeedback.RelaunchConfiguredUnityCoroutine(
                onComplete: result =>
                {
                    SetStatus(YaverFeedback.FormatUnityRunSummary(result));
                },
                onError: error => { SetStatus(error); }
            );
            _busy = false;
        }

        private void SetStatus(string value)
        {
            _status = string.IsNullOrEmpty(value) ? "Idle" : value;
            PushActivity(_status);
        }

        private void PushActivity(string value)
        {
            if (string.IsNullOrEmpty(value))
            {
                return;
            }

            var entry = System.DateTime.UtcNow.ToString("HH:mm:ss") + " " + value;
            _activity.Insert(0, entry);
            if (_activity.Count > MaxActivityCount)
            {
                _activity.RemoveAt(_activity.Count - 1);
            }
        }
    }
}
