using NUnit.Framework;

namespace Yaver.Feedback.Tests
{
    public sealed class YaverFeedbackConfigTests
    {
        [SetUp]
        public void SetUp()
        {
            YaverAuth.SignOut();
        }

        [TearDown]
        public void TearDown()
        {
            YaverAuth.SignOut();
        }

        [Test]
        public void ConfigDefaultsEnableFastIterationScaffold()
        {
            var config = new YaverFeedbackConfig();

            Assert.That(config.Enabled, Is.True);
            Assert.That(config.AutoLogin, Is.True);
            Assert.That(config.AutoDiscoverAgentFromCloud, Is.True);
            Assert.That(config.AutoStartBlackBox, Is.True);
            Assert.That(config.CaptureUnityLogs, Is.True);
            Assert.That(config.ConnectCommandStream, Is.True);
            Assert.That(config.ShowOverlay, Is.True);
            Assert.That(config.StartOverlayCollapsed, Is.True);
            Assert.That(config.AutoCaptureScreenshotOnException, Is.True);
            Assert.That(config.AutoSendCrashReports, Is.True);
            Assert.That(config.AutoTriggerFixOnCrash, Is.True);
            Assert.That(config.ReloadStrategy, Is.EqualTo("scene"));
            Assert.That(config.RuntimeProfile, Is.EqualTo("auto"));
            Assert.That(config.DeploymentMode, Is.EqualTo("self-hosted"));
            Assert.That(config.UnityTestMode, Is.EqualTo("EditMode"));
            Assert.That(config.UnityBuildExecuteMethod, Is.Empty);
            Assert.That(config.UnityDesktopExecutablePath, Is.Empty);
        }

        [Test]
        public void OAuthCallbackStoresTokenFromDeepLink()
        {
            var consumed = YaverAuth.TryConsumeOAuthCallback("yaver://oauth-callback?token=abc%20123&state=test");

            Assert.That(consumed, Is.True);
            Assert.That(YaverAuth.IsAuthenticated, Is.True);
            Assert.That(YaverAuth.StoredToken, Is.EqualTo("abc 123"));
        }

        [Test]
        public void OAuthCallbackIgnoresUrlsWithoutToken()
        {
            var consumed = YaverAuth.TryConsumeOAuthCallback("yaver://oauth-callback?state=test");

            Assert.That(consumed, Is.False);
            Assert.That(YaverAuth.IsAuthenticated, Is.False);
            Assert.That(YaverAuth.StoredToken, Is.Empty);
        }
    }
}
