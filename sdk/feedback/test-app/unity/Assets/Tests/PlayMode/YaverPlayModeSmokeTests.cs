using System.Collections;
using NUnit.Framework;
using UnityEngine;
using UnityEngine.TestTools;

public sealed class YaverPlayModeSmokeTests
{
    [UnityTest]
    public IEnumerator ContentRefreshHandler_ComponentRetainsReferences()
    {
        var go = new GameObject("YaverPlayModeHost");
        try
        {
            var handler = go.AddComponent<Yaver.Feedback.YaverContentRefreshHandler>();
            var applier = go.AddComponent<YaverGameConfigApplier>();

            Assert.That(handler, Is.Not.Null);
            Assert.That(applier, Is.Not.Null);

            applier.ApplyPayload("{\"theme\":\"arcade\",\"playerSpeed\":6.2,\"spawnInterval\":1.1,\"startingLives\":4}");
            yield return null;

            Assert.That(applier.CurrentConfig.theme, Is.EqualTo("arcade"));
            Assert.That(applier.CurrentConfig.playerSpeed, Is.EqualTo(6.2f).Within(0.001f));
            Assert.That(applier.CurrentConfig.startingLives, Is.EqualTo(4));
        }
        finally
        {
            Object.Destroy(go);
        }
    }
}
