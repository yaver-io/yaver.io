param(
  [string]$OutputPath = "$env:USERPROFILE\Pictures\yaver-windows-store.png",
  [int]$Width = 1600,
  [int]$Height = 900,
  [int]$SettleSeconds = 3
)

$ErrorActionPreference = "Stop"

Add-Type -AssemblyName System.Drawing
Add-Type @"
using System;
using System.Runtime.InteropServices;

public static class YaverStoreCapture {
  [StructLayout(LayoutKind.Sequential)]
  public struct RECT {
    public int Left;
    public int Top;
    public int Right;
    public int Bottom;
  }

  [DllImport("user32.dll")]
  public static extern bool GetWindowRect(IntPtr hWnd, out RECT rect);

  [DllImport("user32.dll")]
  public static extern bool SetWindowPos(
    IntPtr hWnd,
    IntPtr hWndInsertAfter,
    int x,
    int y,
    int cx,
    int cy,
    uint flags
  );

  [DllImport("user32.dll")]
  public static extern bool ShowWindow(IntPtr hWnd, int command);

  [DllImport("user32.dll")]
  public static extern bool SetForegroundWindow(IntPtr hWnd);
}
"@

$process = Get-Process Yaver |
  Where-Object { $_.MainWindowHandle -ne 0 -and $_.MainWindowTitle -eq "Yaver" } |
  Select-Object -First 1

if (-not $process) {
  throw "No visible Yaver desktop window was found."
}

$handle = $process.MainWindowHandle
[YaverStoreCapture]::ShowWindow($handle, 9) | Out-Null
[YaverStoreCapture]::SetWindowPos($handle, [IntPtr]::Zero, 120, 80, $Width, $Height, 0x0040) | Out-Null
[YaverStoreCapture]::SetForegroundWindow($handle) | Out-Null
if ($SettleSeconds -gt 0) {
  Start-Sleep -Seconds $SettleSeconds
}

$rect = New-Object YaverStoreCapture+RECT
if (-not [YaverStoreCapture]::GetWindowRect($handle, [ref]$rect)) {
  throw "Could not read the Yaver window bounds."
}

$captureWidth = $rect.Right - $rect.Left
$captureHeight = $rect.Bottom - $rect.Top
if ($captureWidth -lt 1366 -or $captureHeight -lt 768) {
  throw "Yaver window is only ${captureWidth}x${captureHeight}; Store capture requires at least 1366x768."
}

$parent = Split-Path -Parent $OutputPath
if ($parent) {
  New-Item -ItemType Directory -Force -Path $parent | Out-Null
}

$bitmap = New-Object System.Drawing.Bitmap $captureWidth, $captureHeight
$graphics = [System.Drawing.Graphics]::FromImage($bitmap)
try {
  $graphics.CopyFromScreen($rect.Left, $rect.Top, 0, 0, $bitmap.Size)
  $bitmap.Save($OutputPath, [System.Drawing.Imaging.ImageFormat]::Png)
} finally {
  $graphics.Dispose()
  $bitmap.Dispose()
}

Write-Output "CAPTURE=$OutputPath"
Write-Output "SIZE=${captureWidth}x${captureHeight}"
