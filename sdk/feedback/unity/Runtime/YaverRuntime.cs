using UnityEngine;
using UnityEngine.SceneManagement;

namespace Yaver.Feedback
{
    internal sealed class YaverRuntime : MonoBehaviour
    {
        private static YaverRuntime _instance;
        private string _lastSceneName = string.Empty;

        internal static YaverRuntime Instance
        {
            get
            {
                if (_instance != null)
                {
                    return _instance;
                }

                var existing = FindObjectOfType<YaverRuntime>();
                if (existing != null)
                {
                    _instance = existing;
                    return _instance;
                }

                var go = new GameObject("YaverRuntime");
                DontDestroyOnLoad(go);
                _instance = go.AddComponent<YaverRuntime>();
                return _instance;
            }
        }

        private void Awake()
        {
            if (_instance != null && _instance != this)
            {
                Destroy(gameObject);
                return;
            }

            _instance = this;
            DontDestroyOnLoad(gameObject);
            SceneManager.activeSceneChanged -= OnActiveSceneChanged;
            SceneManager.activeSceneChanged += OnActiveSceneChanged;
            _lastSceneName = SceneManager.GetActiveScene().name;
        }

        private void Start()
        {
            if (!string.IsNullOrEmpty(_lastSceneName))
            {
                YaverFeedback.NotifySceneChanged(_lastSceneName, string.Empty);
            }
        }

        private void OnDestroy()
        {
            if (_instance == this)
            {
                SceneManager.activeSceneChanged -= OnActiveSceneChanged;
                _instance = null;
            }
        }

        private void OnApplicationPause(bool pauseStatus)
        {
            YaverFeedback.NotifyLifecycle(pauseStatus ? "application-paused" : "application-resumed");
        }

        private void OnApplicationFocus(bool hasFocus)
        {
            YaverFeedback.NotifyLifecycle(hasFocus ? "application-focus-gained" : "application-focus-lost");
        }

        private void OnApplicationQuit()
        {
            YaverFeedback.NotifyLifecycle("application-quit");
        }

        private void OnActiveSceneChanged(Scene previous, Scene next)
        {
            var previousName = previous.IsValid() ? previous.name : _lastSceneName;
            var nextName = next.IsValid() ? next.name : string.Empty;
            _lastSceneName = nextName;
            YaverFeedback.NotifySceneChanged(nextName, previousName);
        }
    }
}
