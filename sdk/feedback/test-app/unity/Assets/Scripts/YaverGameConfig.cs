using System;
using UnityEngine;

[Serializable]
public sealed class YaverGameConfig
{
    public string theme = "default";
    public float playerSpeed = 5f;
    public float spawnInterval = 1.25f;
    public int startingLives = 3;
}
