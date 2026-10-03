[CmdletBinding()]
param(
  [Parameter(Mandatory = $true)]
  [ValidateScript({ Test-Path -LiteralPath $_ -PathType Leaf })]
  [string] $PackagePath,
  [ValidateSet("x64")]
  [string] $ExpectedArchitecture = "x64",
  [switch] $RequireProductionIdentity,
  [switch] $TestNativeAgent,
  [string] $ReportPath = ""
)

$ErrorActionPreference = "Stop"
Set-StrictMode -Version Latest
$report = [Collections.Generic.List[string]]::new()
function Record([string] $Message) { $report.Add($Message); Write-Host $Message }
function Save { if ($ReportPath) { $report | Set-Content -LiteralPath $ReportPath -Encoding UTF8 } }
function Fail([string] $Message) { Record "FAIL: $Message"; Save; throw $Message }

function Get-PeMachine([string] $Path) {
  $stream = [IO.File]::OpenRead($Path)
  $reader = [IO.BinaryReader]::new($stream)
  try {
    if ($reader.ReadUInt16() -ne 0x5A4D) { Fail "Not a PE file: $Path" }
    $stream.Position = 0x3C
    $offset = $reader.ReadUInt32()
    $stream.Position = $offset
    if ($reader.ReadUInt32() -ne 0x00004550) { Fail "Invalid PE signature: $Path" }
    return $reader.ReadUInt16()
  } finally { $reader.Dispose(); $stream.Dispose() }
}

$sevenZip = Get-Command 7z.exe -ErrorAction SilentlyContinue
if (-not $sevenZip) { $sevenZip = Get-Command 7z -ErrorAction SilentlyContinue }
if (-not $sevenZip) { throw "7-Zip is required to inspect the Store package." }
if ([IO.Path]::GetExtension($PackagePath) -notin @(".appx", ".msix")) { throw "Expected an AppX or MSIX package." }

$temporary = Join-Path ([IO.Path]::GetTempPath()) ("yaver-store-" + [guid]::NewGuid().ToString("N"))
New-Item -ItemType Directory -Path $temporary | Out-Null
try {
  & $sevenZip.Source x -y "-o$temporary" $PackagePath | Out-Null
  if ($LASTEXITCODE -ne 0) { Fail "Could not extract the Store package." }

  $required = @(
    "AppxManifest.xml",
    "AppxBlockMap.xml",
    "[Content_Types].xml",
    "app\Yaver.exe",
    "app\resources\bin\yaver.exe",
    "app\resources\LICENSE.txt",
    "app\resources\yaver-store-channel.json"
  )
  foreach ($relative in $required) {
    if (-not (Test-Path -LiteralPath (Join-Path $temporary $relative) -PathType Leaf)) { Fail "Store package is missing $relative" }
  }

  $forbidden = @(
    "app\resources\bin\yaver",
    "app\resources\wsl.exe"
  )
  foreach ($relative in $forbidden) {
    if (Test-Path -LiteralPath (Join-Path $temporary $relative)) { Fail "Store package contains a forbidden non-Windows payload: $relative" }
  }
  $forbiddenPayloads = @(Get-ChildItem -LiteralPath (Join-Path $temporary "app") -Recurse -File | Where-Object {
    $_.Extension -ieq ".vhdx" -or $_.Name -match "(?i)^(install|rootfs)[.]tar([.]gz)?$"
  })
  if ($forbiddenPayloads.Count -ne 0) { Fail "Store package contains bundled WSL/Linux payloads." }

  [xml] $manifest = Get-Content -LiteralPath (Join-Path $temporary "AppxManifest.xml") -Raw
  $identity = $manifest.Package.Identity
  if ([string]$identity.ProcessorArchitecture -ne $ExpectedArchitecture) {
    Fail "Package architecture is $($identity.ProcessorArchitecture), expected $ExpectedArchitecture."
  }
  if ($RequireProductionIdentity) {
    foreach ($pair in @(
      @{ Name = "YAVER_STORE_IDENTITY_NAME"; Actual = [string]$identity.Name },
      @{ Name = "YAVER_STORE_PUBLISHER"; Actual = [string]$identity.Publisher },
      @{ Name = "YAVER_STORE_PUBLISHER_DISPLAY_NAME"; Actual = [string]$manifest.Package.Properties.PublisherDisplayName },
      @{ Name = "YAVER_STORE_DISPLAY_NAME"; Actual = [string]$manifest.Package.Properties.DisplayName }
    )) {
      $expected = [Environment]::GetEnvironmentVariable($pair.Name)
      if ([string]::IsNullOrWhiteSpace($expected) -or $expected -match "PENDING|VALIDATION") { Fail "$($pair.Name) is not configured." }
      if ($pair.Actual -cne $expected) { Fail "$($pair.Name) differs from the built manifest." }
    }
  }

  $manifestText = Get-Content -LiteralPath (Join-Path $temporary "AppxManifest.xml") -Raw
  if ($manifestText -notmatch 'TargetDeviceFamily Name="Windows[.]Desktop"') { Fail "Package is not scoped to Windows.Desktop." }
  if ($manifestText -notmatch 'EntryPoint="Windows[.]FullTrustApplication"') { Fail "Electron full-trust entry point is missing." }
  foreach ($capability in @("internetClient", "privateNetworkClientServer", "runFullTrust")) {
    if ($manifestText -notmatch ('Name="' + [regex]::Escape($capability) + '"')) { Fail "Required capability is missing: $capability" }
  }
  foreach ($capability in @("broadFileSystemAccess", "allowElevation", "packagedServices")) {
    if ($manifestText -match ('Name="' + [regex]::Escape($capability) + '"')) { Fail "Unexpected Store capability: $capability" }
  }

  $machine = Get-PeMachine (Join-Path $temporary "app\Yaver.exe")
  if ($machine -ne 0x8664) { Fail ("Yaver.exe is not x64 (machine 0x{0:X4})." -f $machine) }
  $agent = Join-Path $temporary "app\resources\bin\yaver.exe"
  $agentMachine = Get-PeMachine $agent
  if ($agentMachine -ne 0x8664) { Fail ("Embedded yaver.exe is not x64 (machine 0x{0:X4})." -f $agentMachine) }
  $channel = Get-Content -LiteralPath (Join-Path $temporary "app\resources\yaver-store-channel.json") -Raw | ConvertFrom-Json
  if ($channel.channel -ne "microsoft-store-full-node" -or $channel.localAgent -ne $true -or $channel.bundledWsl -ne $false) {
    Fail "Microsoft Store channel marker is invalid."
  }

  if ($TestNativeAgent) {
    schtasks.exe /Query /TN YaverAgent *> $null
    if ($LASTEXITCODE -eq 0) { Fail "YaverAgent task exists before the package probe; use a clean runner." }
    $port = Get-Random -Minimum 19080 -Maximum 29080
    $work = Join-Path $temporary "agent-smoke"
    New-Item -ItemType Directory -Path $work | Out-Null
    $oldSkip = $env:YAVER_SKIP_AUTO_START
    $oldVault = $env:YAVER_VAULT_SKIP_KEYCHAIN
    $env:YAVER_SKIP_AUTO_START = "1"
    $env:YAVER_VAULT_SKIP_KEYCHAIN = "1"
    $process = $null
    try {
      $process = Start-Process -FilePath $agent -ArgumentList @(
        "serve", "--debug", "--port", [string]$port, "--no-relay", "--no-quic", "--no-tls", ("--work-dir={0}" -f $work)
      ) -WindowStyle Hidden -PassThru
      $health = $null
      $deadline = (Get-Date).AddSeconds(45)
      while ((Get-Date) -lt $deadline -and -not $health) {
        if ($process.HasExited) { Fail "Packaged native agent exited before /health answered." }
        try { $health = Invoke-RestMethod -Uri "http://127.0.0.1:$port/health" -TimeoutSec 2 }
        catch { Start-Sleep -Milliseconds 500 }
      }
      if (-not $health -or $health.ok -ne $true) { Fail "Packaged native agent did not return healthy /health." }
      $wsl = @(Get-CimInstance Win32_Process -Filter "Name = 'wsl.exe'" -ErrorAction SilentlyContinue | Where-Object { $_.ParentProcessId -eq $process.Id })
      if ($wsl.Count -ne 0) { Fail "Packaged native agent invoked WSL during core startup." }
      schtasks.exe /Query /TN YaverAgent *> $null
      if ($LASTEXITCODE -eq 0) { Fail "Packaged native agent created a scheduled task without consent." }
      Record "Native x64 agent /health passed without WSL or a scheduled task."
    } finally {
      if ($process -and -not $process.HasExited) { Stop-Process -Id $process.Id -Force -ErrorAction SilentlyContinue }
      $env:YAVER_SKIP_AUTO_START = $oldSkip
      $env:YAVER_VAULT_SKIP_KEYCHAIN = $oldVault
    }
  }

  $sha = (Get-FileHash -LiteralPath $PackagePath -Algorithm SHA256).Hash.ToLowerInvariant()
  Record "PASS: x64 full-node Microsoft Store package (client, agent, and combined modes)"
  Record "Package: $PackagePath"
  Record "Identity: $($identity.Name)"
  Record "Publisher: $($identity.Publisher)"
  Record "Version: $($identity.Version)"
  Record "SHA-256: $sha"
  Save
} finally {
  Remove-Item -LiteralPath $temporary -Recurse -Force -ErrorAction SilentlyContinue
}

# Native command probes intentionally use non-zero exit codes to mean "not
# present" (for example, schtasks /Query after the agent smoke test). PowerShell
# otherwise leaks the last native exit code to the workflow even after every
# assertion passed and the PASS report was written.
exit 0
