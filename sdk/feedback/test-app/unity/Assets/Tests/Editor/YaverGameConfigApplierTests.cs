using NUnit.Framework;
using UnityEngine;

public sealed class YaverGameConfigApplierTests
{
    [Test]
    public void ApplyPayload_ParsesJsonIntoRuntimeConfig()
    {
        var go = new GameObject("ConfigApplier");
        try
        {
            var applier = go.AddComponent<YaverGameConfigApplier>();
            applier.ApplyPayload("{\"theme\":\"night\",\"playerSpeed\":7.5,\"spawnInterval\":0.8,\"startingLives\":5}");

            Assert.That(applier.LastRawPayload, Does.Contain("\"theme\":\"night\""));
            Assert.That(applier.CurrentConfig, Is.Not.Null);
            Assert.That(applier.CurrentConfig.theme, Is.EqualTo("night"));
            Assert.That(applier.CurrentConfig.playerSpeed, Is.EqualTo(7.5f).Within(0.001f));
            Assert.That(applier.CurrentConfig.spawnInterval, Is.EqualTo(0.8f).Within(0.001f));
            Assert.That(applier.CurrentConfig.startingLives, Is.EqualTo(5));
        }
        finally
        {
            Object.DestroyImmediate(go);
        }
    }
}
