[CmdletBinding()]
param(
  [Parameter(Mandatory = $true)]
  [ValidateScript({ Test-Path -LiteralPath $_ -PathType Leaf })]
  [string] $InstallerPath,
  [string] $ExpectedSha256 = "",
  [string] $ExpectedVersion = "",
  [int] $TimeoutSeconds = 300,
  [switch] $TestNativeAgent,
  [switch] $TestUninstall,
  [string] $ReportPath = ""
)

$ErrorActionPreference = "Stop"
Set-StrictMode -Version Latest
$installer = (Resolve-Path -LiteralPath $InstallerPath).Path
if (-not $ReportPath) {
  $ReportPath = Join-Path ([Environment]::GetFolderPath("Desktop")) ("Yaver-Store-Preflight-{0}.txt" -f (Get-Date -Format "yyyyMMdd-HHmmss"))
}
$report = [Collections.Generic.List[string]]::new()
function Record([string] $Message) { $line = "[{0}] {1}" -f (Get-Date -Format s), $Message; $report.Add($line); Write-Host $line }
function Save { $report | Set-Content -LiteralPath $ReportPath -Encoding UTF8 }
function Fail([string] $Message) { Record "FAIL: $Message"; Save; throw $Message }
function Entries {
  @("HKCU:\SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall\*", "HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall\*", "HKLM:\SOFTWARE\WOW6432Node\Microsoft\Windows\CurrentVersion\Uninstall\*") |
    ForEach-Object { Get-ItemProperty -Path $_ -ErrorAction SilentlyContinue } |
    Where-Object { $_.DisplayName -eq "Yaver" }
}
function IsPE([string] $Path) {
  $stream = $null
  try { $stream = [IO.File]::Open($Path, "Open", "Read", "ReadWrite"); return $stream.Length -ge 2 -and $stream.ReadByte() -eq 0x4D -and $stream.ReadByte() -eq 0x5A }
  finally { if ($stream) { $stream.Dispose() } }
}
function AssertX64PE([string] $Path) {
  $stream = [IO.File]::Open($Path, "Open", "Read", "ReadWrite")
  $reader = [IO.BinaryReader]::new($stream)
  try {
    if ($stream.Length -lt 64 -or $reader.ReadUInt16() -ne 0x5A4D) { Fail "Not a PE file: $Path" }
    $stream.Position = 0x3C
    $peOffset = $reader.ReadInt32()
    if ($peOffset -lt 0 -or $peOffset + 6 -gt $stream.Length) { Fail "Invalid PE header: $Path" }
    $stream.Position = $peOffset
    if ($reader.ReadUInt32() -ne 0x00004550) { Fail "Invalid PE signature: $Path" }
    $machine = $reader.ReadUInt16()
    if ($machine -ne 0x8664) { Fail ("Expected x64 PE machine 0x8664, found 0x{0:X4}: {1}" -f $machine, $Path) }
  } finally { $reader.Dispose(); $stream.Dispose() }
  Record "Valid x64 PE: $Path"
}
function ValidSignature([string] $Path, [bool] $RequireSimkab) {
  $signature = Get-AuthenticodeSignature -LiteralPath $Path
  if ($signature.Status -ne "Valid" -or -not $signature.SignerCertificate) { Fail "Invalid Authenticode signature: $Path [$($signature.Status)]" }
  if ($RequireSimkab -and $signature.SignerCertificate.Subject -notmatch "(?i)simkab|sİmkab") { Fail "Unexpected Yaver publisher: $($signature.SignerCertificate.Subject)" }
  if ($RequireSimkab -and -not $signature.TimeStamperCertificate) { Fail "Yaver signature has no trusted timestamp: $Path" }
  Record "Valid signature: $Path"
}

try {
  Record "Yaver Microsoft Store EXE preflight"
  if (@(Entries).Count -ne 0) { Fail "Yaver is already installed; use a clean disposable Windows VM." }
  $hash = (Get-FileHash -LiteralPath $installer -Algorithm SHA256).Hash.ToLowerInvariant()
  Record "Installer SHA-256: $hash"
  if ($ExpectedSha256 -and $hash -ne $ExpectedSha256.ToLowerInvariant()) { Fail "Installer hash does not match the immutable candidate." }
  ValidSignature $installer $true

  $install = Start-Process -FilePath $installer -ArgumentList "/S" -PassThru
  if (-not $install.WaitForExit($TimeoutSeconds * 1000)) { try { $install.Kill() } catch {}; Fail "Silent install timed out." }
  if ($install.ExitCode -ne 0) { Fail "Silent install exited $($install.ExitCode)." }
  Start-Sleep -Seconds 3
  $entries = @(Entries)
  if ($entries.Count -ne 1) { Fail "Expected one Installed apps entry, found $($entries.Count)." }
  if (-not [string]$entries[0].Publisher) { Fail "Installed apps Publisher is blank." }
  if ([string]$entries[0].Publisher -notmatch "(?i)simkab|sİmkab") {
    Fail "Installed apps Publisher does not identify SIMKAB: $($entries[0].Publisher)"
  }
  if (-not [string]$entries[0].DisplayVersion) { Fail "Installed apps Version is blank." }
  if ($ExpectedVersion -and [string]$entries[0].DisplayVersion -ne $ExpectedVersion) {
    Fail "Installed apps Version is $($entries[0].DisplayVersion), expected $ExpectedVersion."
  }
  Record "Installed apps metadata: Yaver $($entries[0].DisplayVersion), publisher $($entries[0].Publisher)"
  $location = [string]$entries[0].InstallLocation
  if (-not $location) { $location = Join-Path $env:LOCALAPPDATA "Programs\Yaver" }
  if (-not (Test-Path -LiteralPath $location -PathType Container)) { Fail "Install directory not found: $location" }
  $app = Join-Path $location "Yaver.exe"
  $agent = Join-Path $location "resources\bin\yaver.exe"
  if (-not (Test-Path -LiteralPath $app)) { Fail "Main app missing: $app" }
  if (-not (Test-Path -LiteralPath $agent)) { Fail "Embedded agent missing: $agent" }
  $foreignAgent = Join-Path $location "resources\bin\yaver"
  if (Test-Path -LiteralPath $foreignAgent) { Fail "Windows install contains a non-Windows host agent: $foreignAgent" }
  $bundledLinux = @(Get-ChildItem -LiteralPath $location -Recurse -File | Where-Object {
    $_.Name -ieq "wsl.exe" -or $_.Extension -ieq ".vhdx" -or $_.Name -match "(?i)^(install|rootfs)[.]tar([.]gz)?$"
  })
  if ($bundledLinux.Count -ne 0) { Fail "Windows install unexpectedly bundles WSL/Linux distribution payloads: $($bundledLinux.FullName -join ', ')" }
  Record "No bundled WSL executable, virtual disk, or Linux root filesystem found."
  $license = Join-Path $location "resources\LICENSE.txt"
  if (-not (Test-Path -LiteralPath $license)) { Fail "Redistributed FSL license is missing: $license" }
  AssertX64PE $app
  AssertX64PE $agent
  ValidSignature $app $true
  ValidSignature $agent $true
  $peFiles = @(Get-ChildItem -LiteralPath $location -Recurse -File | Where-Object { IsPE $_.FullName })
  foreach ($file in $peFiles) { ValidSignature $file.FullName $false }
  Record "Installed PE signature scan passed for $($peFiles.Count) files."

  if ($TestNativeAgent) {
    schtasks.exe /Query /TN YaverAgent *> $null
    if ($LASTEXITCODE -eq 0) { Fail "YaverAgent scheduled task already exists; use a clean disposable Windows VM." }
    $port = Get-Random -Minimum 19080 -Maximum 29080
    $tempRoot = if ($env:RUNNER_TEMP) { $env:RUNNER_TEMP } else { [IO.Path]::GetTempPath() }
    $smokeRoot = Join-Path $tempRoot "yaver-native-smoke"
    New-Item -ItemType Directory -Force -Path $smokeRoot | Out-Null
    $oldSkip = $env:YAVER_SKIP_AUTO_START
    $oldVault = $env:YAVER_VAULT_SKIP_KEYCHAIN
    $env:YAVER_SKIP_AUTO_START = "1"
    $env:YAVER_VAULT_SKIP_KEYCHAIN = "1"
    $native = $null
    try {
      $native = Start-Process -FilePath $agent -ArgumentList @(
        "serve", "--debug", "--port", [string]$port, "--no-relay", "--no-quic", "--no-tls", ("--work-dir={0}" -f $smokeRoot)
      ) -WindowStyle Hidden -PassThru
      $health = $null
      $deadline = (Get-Date).AddSeconds(45)
      while ((Get-Date) -lt $deadline -and -not $health) {
        if ($native.HasExited) { Fail "Native yaver.exe exited before its health operation answered (exit $($native.ExitCode))." }
        try { $health = Invoke-RestMethod -Uri "http://127.0.0.1:$port/health" -TimeoutSec 2 }
        catch { Start-Sleep -Milliseconds 500 }
      }
      if (-not $health -or $health.ok -ne $true) { Fail "Native yaver.exe did not return a healthy /health response." }
      $wslChildren = @(Get-CimInstance Win32_Process -Filter "Name = 'wsl.exe'" -ErrorAction SilentlyContinue | Where-Object { $_.ParentProcessId -eq $native.Id })
      if ($wslChildren.Count -ne 0) { Fail "Native startup invoked WSL even though core startup must not require it." }
      schtasks.exe /Query /TN YaverAgent *> $null
      if ($LASTEXITCODE -eq 0) { Fail "Native smoke created a YaverAgent scheduled task despite the no-auto-start contract." }
      $mode = if ($health.PSObject.Properties.Name -contains "mode") { [string]$health.mode } else { "authenticated" }
      Record "Native x64 agent startup passed without WSL or an agent-owned scheduled task (mode $mode)."
    } finally {
      if ($native -and -not $native.HasExited) { Stop-Process -Id $native.Id -Force -ErrorAction SilentlyContinue }
      $env:YAVER_SKIP_AUTO_START = $oldSkip
      $env:YAVER_VAULT_SKIP_KEYCHAIN = $oldVault
    }
  } else { Record "Native agent startup skipped; rerun with -TestNativeAgent before submission." }
  Record "MANUAL: launch, sign in with the synthetic account, run a task and browser preview, test offline UI, reconnect remotely, and run Windows App Certification Kit."

  if ($TestUninstall) {
    $command = [string]$entries[0].QuietUninstallString
    if (-not $command) { $command = [string]$entries[0].UninstallString }
    if ($command -match '^\s*"([^"]+[.]exe)"') { $uninstaller = $Matches[1] }
    elseif ($command -match '^\s*([^\s]+[.]exe)') { $uninstaller = $Matches[1] }
    else { Fail "Could not resolve the registered uninstaller." }
    $uninstall = Start-Process -FilePath $uninstaller -ArgumentList "/S" -Wait -PassThru
    if ($uninstall.ExitCode -ne 0) { Fail "Silent uninstall exited $($uninstall.ExitCode)." }
    Start-Sleep -Seconds 5
    if (@(Entries).Count -ne 0) { Fail "Installed apps entry remains after uninstall." }
    if (Test-Path -LiteralPath $location) {
      $remnants = @(Get-ChildItem -LiteralPath $location -Force -ErrorAction SilentlyContinue)
      if ($remnants.Count -ne 0) { Fail "Install directory contains remnants after uninstall: $location" }
    }
    Record "Silent uninstall passed."
  } else { Record "Uninstall skipped; rerun with -TestUninstall before submission." }
  Record "PASS: automated Yaver Store checks completed."
  Save
} catch { if (-not (Test-Path -LiteralPath $ReportPath)) { Save }; Write-Error $_; exit 1 }
