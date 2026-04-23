using System;
using System.IO;
using UnityEditor;
using UnityEditor.SceneManagement;
using UnityEditor.Build.Reporting;
using UnityEngine;

public static class YaverBuildTools
{
    public static void BuildWindows64()
    {
        BuildPlayer("StandaloneWindows64", "YaverDemo.exe");
    }

    public static void BuildMacOS()
    {
        BuildPlayer("StandaloneOSX", "YaverDemo.app");
    }

    public static void BuildLinux64()
    {
        BuildPlayer("StandaloneLinux64", "YaverDemo.x86_64");
    }

    public static void BuildAndroid()
    {
        BuildPlayer("Android", "YaverDemo.apk");
    }

    public static void BuildWebGL()
    {
        BuildPlayer("WebGL", "WebGL");
    }

    private static void BuildPlayer(string buildTargetName, string fileName)
    {
        var outputDir = ResolveOutputDir();
        Directory.CreateDirectory(outputDir);
        var locationPath = buildTargetName == "WebGL" ? outputDir : Path.Combine(outputDir, fileName);

        var target = (BuildTarget)Enum.Parse(typeof(BuildTarget), buildTargetName);
        var options = new BuildPlayerOptions
        {
            scenes = FindEnabledScenes(),
            locationPathName = locationPath,
            target = target,
            options = BuildOptions.None
        };

        var report = BuildPipeline.BuildPlayer(options);
        if (report.summary.result != BuildResult.Succeeded)
        {
            throw new Exception("Yaver Unity sample build failed: " + report.summary.result);
        }

        WriteBuildManifest(outputDir, locationPath, buildTargetName);
        UnityEngine.Debug.Log("Yaver build output: " + locationPath);
    }

    private static string[] FindEnabledScenes()
    {
        var scenes = EditorBuildSettings.scenes;
        var enabled = new System.Collections.Generic.List<string>(scenes.Length);
        for (var i = 0; i < scenes.Length; i++)
        {
            if (scenes[i] != null && scenes[i].enabled && !string.IsNullOrEmpty(scenes[i].path))
            {
                enabled.Add(scenes[i].path);
            }
        }

        if (enabled.Count == 0)
        {
            var generatedScene = CreateGeneratedScene();
            enabled.Add(generatedScene);
        }

        return enabled.ToArray();
    }

    private static string CreateGeneratedScene()
    {
        var sceneDir = Path.Combine("Assets", "YaverGenerated");
        Directory.CreateDirectory(sceneDir);
        var scenePath = Path.Combine(sceneDir, "YaverGeneratedScene.unity");
        var scene = EditorSceneManager.NewScene(NewSceneSetup.EmptyScene, NewSceneMode.Single);
        var camera = new GameObject("Main Camera");
        camera.tag = "MainCamera";
        camera.AddComponent<Camera>();
        if (!EditorSceneManager.SaveScene(scene, scenePath))
        {
            throw new Exception("Failed to create generated build scene at " + scenePath);
        }

        AssetDatabase.Refresh();
        return scenePath.Replace("\\", "/");
    }

    private static string ResolveOutputDir()
    {
        var args = Environment.GetCommandLineArgs();
        for (var i = 0; i < args.Length - 1; i++)
        {
            if (args[i] == "-yaverBuildOutput")
            {
                return args[i + 1];
            }
        }

        return Path.Combine("Builds", "Yaver");
    }

    private static string ResolveManifestPath()
    {
        var args = Environment.GetCommandLineArgs();
        for (var i = 0; i < args.Length - 1; i++)
        {
            if (args[i] == "-yaverBuildManifest")
            {
                return args[i + 1];
            }
        }

        return string.Empty;
    }

    private static void WriteBuildManifest(string outputDir, string executablePath, string buildTargetName)
    {
        var manifestPath = ResolveManifestPath();
        if (string.IsNullOrEmpty(manifestPath))
        {
            return;
        }

        var manifest = new YaverBuildManifest
        {
            outputPath = outputDir,
            executablePath = executablePath,
            buildTarget = buildTargetName,
            executeMethod = ResolveExecuteMethod()
        };

        var parent = Path.GetDirectoryName(manifestPath);
        if (!string.IsNullOrEmpty(parent))
        {
            Directory.CreateDirectory(parent);
        }

        File.WriteAllText(manifestPath, JsonUtility.ToJson(manifest, true));
    }

    [Serializable]
    private sealed class YaverBuildManifest
    {
        public string executablePath;
        public string outputPath;
        public string buildTarget;
        public string executeMethod;
    }

    private static string ResolveExecuteMethod()
    {
        var args = Environment.GetCommandLineArgs();
        for (var i = 0; i < args.Length - 1; i++)
        {
            if (args[i] == "-executeMethod")
            {
                return args[i + 1];
            }
        }

        return string.Empty;
    }
}
