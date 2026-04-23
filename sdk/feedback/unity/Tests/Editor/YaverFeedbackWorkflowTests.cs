using System.Reflection;
using NUnit.Framework;

namespace Yaver.Feedback.Tests
{
    public sealed class YaverFeedbackWorkflowTests
    {
        private static readonly FieldInfo ConfigField = typeof(YaverFeedback).GetField("_config", BindingFlags.Static | BindingFlags.NonPublic);
        private YaverFeedbackConfig _originalConfig;

        [SetUp]
        public void SetUp()
        {
            _originalConfig = ConfigField != null ? (YaverFeedbackConfig)ConfigField.GetValue(null) : null;
        }

        [TearDown]
        public void TearDown()
        {
            if (ConfigField != null)
            {
                ConfigField.SetValue(null, _originalConfig);
            }
        }

        [Test]
        public void BuildDefaultVibingPrompt_UsesDesktopLanguageForDesktopProfile()
        {
            ConfigField.SetValue(null, new YaverFeedbackConfig
            {
                RuntimeProfile = "desktop"
            });

            var prompt = YaverFeedback.BuildDefaultVibingPrompt();

            Assert.That(prompt, Does.Contain("Unity desktop session"));
            Assert.That(prompt, Does.Contain("Add or update tests"));
            Assert.That(prompt, Does.Contain("relaunch the build"));
        }

        [Test]
        public void BuildDefaultVibingPrompt_UsesMobileLanguageForMobileProfile()
        {
            ConfigField.SetValue(null, new YaverFeedbackConfig
            {
                RuntimeProfile = "mobile"
            });

            var prompt = YaverFeedback.BuildDefaultVibingPrompt();

            Assert.That(prompt, Does.Contain("Unity mobile session"));
            Assert.That(prompt, Does.Contain("Add or update tests"));
            Assert.That(prompt, Does.Not.Contain("relaunch the build"));
        }

        [Test]
        public void FormatUnityRunSummary_CombinesStructuredFields()
        {
            var result = new YaverUnityRunResult
            {
                stage = "build",
                status = "completed",
                summary = "Windows player created.",
                nextAction = "Relaunch the build"
            };

            var text = YaverFeedback.FormatUnityRunSummary(result);

            Assert.That(text, Is.EqualTo("build | completed | Windows player created. | Next: Relaunch the build"));
        }

        [Test]
        public void FormatUnityRunSummary_FallsBackForNullResult()
        {
            Assert.That(YaverFeedback.FormatUnityRunSummary(null), Is.EqualTo("Unity action completed."));
        }

        [Test]
        public void ReloadAndBuildHelpersReflectConfiguredUnityPaths()
        {
            ConfigField.SetValue(null, new YaverFeedbackConfig
            {
                ReloadStrategy = "relaunch",
                UnityBuildExecuteMethod = "YaverBuildTools.BuildWindows64",
                UnityDesktopExecutablePath = "Builds/Desktop/Yaver.exe"
            });

            Assert.That(YaverFeedback.GetReloadActionLabel(), Is.EqualTo("Relaunch"));
            Assert.That(YaverFeedback.HasUnityBuildConfiguration(), Is.True);
            Assert.That(YaverFeedback.HasUnityDesktopExecutable(), Is.True);
        }
    }
}
