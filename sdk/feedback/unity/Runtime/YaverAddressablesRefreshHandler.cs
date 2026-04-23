using System.Collections;
using System.Reflection;
using UnityEngine;

namespace Yaver.Feedback
{
    public sealed class YaverAddressablesRefreshHandler : MonoBehaviour
    {
        [SerializeField] private bool autoBind = true;
        [SerializeField] private bool loadContentCatalogFromUrl = true;
        [SerializeField] private string lastContentUrl = string.Empty;
        [SerializeField] private string lastStatus = "idle";

        public string LastContentUrl => lastContentUrl;
        public string LastStatus => lastStatus;

        private void OnEnable()
        {
            if (autoBind)
            {
                YaverFeedback.ContentRefreshRequested += HandleContentRefreshRequested;
            }
        }

        private void OnDisable()
        {
            YaverFeedback.ContentRefreshRequested -= HandleContentRefreshRequested;
        }

        private void HandleContentRefreshRequested(string contentUrl)
        {
            if (!string.IsNullOrEmpty(contentUrl))
            {
                StartCoroutine(RefreshAddressables(contentUrl));
            }
        }

        private IEnumerator RefreshAddressables(string contentUrl)
        {
            lastContentUrl = contentUrl ?? string.Empty;
            lastStatus = "refreshing";

            var addressablesType = System.Type.GetType("UnityEngine.AddressableAssets.Addressables, Unity.Addressables");
            if (addressablesType == null)
            {
                lastStatus = "addressables-missing";
                YaverBlackBox.Log("Addressables package is not installed in this project.", "YaverAddressablesRefreshHandler");
                yield break;
            }

            if (loadContentCatalogFromUrl)
            {
                var loadCatalog = addressablesType.GetMethod(
                    "LoadContentCatalogAsync",
                    BindingFlags.Public | BindingFlags.Static,
                    null,
                    new[] { typeof(string), typeof(bool), typeof(string) },
                    null
                );

                if (loadCatalog != null)
                {
                    object handle = null;
                    try
                    {
                        handle = loadCatalog.Invoke(null, new object[] { contentUrl, true, null });
                    }
                    catch (System.Exception error)
                    {
                        lastStatus = "catalog-load-failed";
                        YaverBlackBox.Log("Addressables catalog load failed: " + error.Message, "YaverAddressablesRefreshHandler");
                        yield break;
                    }

                    yield return WaitForAsyncOperation(handle);
                }
            }

            var initialize = addressablesType.GetMethod("InitializeAsync", BindingFlags.Public | BindingFlags.Static, null, System.Type.EmptyTypes, null);
            if (initialize != null)
            {
                object handle = null;
                try
                {
                    handle = initialize.Invoke(null, null);
                }
                catch (System.Exception error)
                {
                    lastStatus = "initialize-failed";
                    YaverBlackBox.Log("Addressables initialize failed: " + error.Message, "YaverAddressablesRefreshHandler");
                    yield break;
                }

                yield return WaitForAsyncOperation(handle);
            }

            lastStatus = "ready";
            YaverBlackBox.State("addressables-refresh-applied");
        }

        private static IEnumerator WaitForAsyncOperation(object handle)
        {
            if (handle == null)
            {
                yield break;
            }

            var type = handle.GetType();
            var isDoneProperty = type.GetProperty("IsDone", BindingFlags.Public | BindingFlags.Instance);
            var operationExceptionProperty = type.GetProperty("OperationException", BindingFlags.Public | BindingFlags.Instance);

            while (isDoneProperty != null && !(bool)isDoneProperty.GetValue(handle, null))
            {
                yield return null;
            }

            if (operationExceptionProperty != null)
            {
                var operationException = operationExceptionProperty.GetValue(handle, null);
                if (operationException != null)
                {
                    Debug.LogError("Yaver Addressables refresh failed: " + operationException);
                }
            }
        }
    }
}
