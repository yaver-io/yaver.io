[CmdletBinding()]
param(
  [switch] $RunPreflight,
  [string] $PreflightReport = ""
)

$ErrorActionPreference = "Stop"
$repo = (Resolve-Path "$PSScriptRoot\..\..").Path
$electron = Join-Path $repo "electron"
$versions = Get-Content (Join-Path $repo "versions.json") -Raw | ConvertFrom-Json
$package = Get-Content (Join-Path $electron "package.json") -Raw | ConvertFrom-Json
if ($versions.gui -ne $package.version) { throw "GUI version mismatch: versions.json=$($versions.gui), electron=$($package.version)" }
foreach ($name in @("YAVER_STORE_IDENTITY_NAME", "YAVER_STORE_PUBLISHER", "YAVER_STORE_PUBLISHER_DISPLAY_NAME", "YAVER_STORE_DISPLAY_NAME")) {
  $value = [Environment]::GetEnvironmentVariable($name)
  if ([string]::IsNullOrWhiteSpace($value) -or $value -match "PENDING|VALIDATION") {
    throw "$name must contain the exact Partner Center Product identity value."
  }
}

$env:YAVER_STORE_PRODUCTION = "1"
$agentDirectory = Join-Path $electron "resources\bin"
New-Item -ItemType Directory -Force -Path $agentDirectory | Out-Null
$agent = Join-Path $agentDirectory "yaver.exe"
Push-Location (Join-Path $repo "desktop\agent")
try {
  $env:GOOS = "windows"
  $env:GOARCH = "amd64"
  $env:CGO_ENABLED = "0"
  go build -trimpath -ldflags "-s -w -X main.version=$($versions.cli)" -o $agent .
  if ($LASTEXITCODE -ne 0) { throw "Native Windows agent build failed." }
} finally { Pop-Location }
Push-Location $electron
try {
  npm run dist:win:store
  if ($LASTEXITCODE -ne 0) { throw "Microsoft Store package build failed." }
} finally { Pop-Location }

$artifact = Join-Path $electron "dist-microsoft-store\Yaver-$($package.version)-x64-store.appx"
if (-not (Test-Path -LiteralPath $artifact -PathType Leaf)) { throw "Expected package was not produced: $artifact" }
$arguments = @{
  PackagePath = $artifact
  ExpectedArchitecture = "x64"
  RequireProductionIdentity = $true
  TestNativeAgent = $true
}
if ($RunPreflight -and $PreflightReport) { $arguments.ReportPath = $PreflightReport }
& (Join-Path $repo "electron\store\assert-microsoft-store-package.ps1") @arguments
Write-Host "Built: $artifact"
