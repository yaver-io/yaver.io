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

# Partner Center permanently remembers whether a published device-family lane
# used a bundle. Yaver's live Windows Desktop lane was introduced as a bundle,
# so every later submission must remain a real MakeAppx bundle even when it
# contains only the x64 package. A renamed zip/appxupload is not a bundle.
$makeAppx = Get-Command makeappx.exe -ErrorAction SilentlyContinue
if (-not $makeAppx) {
  $sdkRoot = Join-Path ${env:ProgramFiles(x86)} "Windows Kits\10\bin"
  $makeAppx = Get-ChildItem -Path "$sdkRoot\*\x64\makeappx.exe" -File -ErrorAction SilentlyContinue |
    Sort-Object { [version]$_.Directory.Parent.Name } -Descending |
    Select-Object -First 1
}
if (-not $makeAppx) { throw "MakeAppx.exe is required to produce the mandatory Store bundle." }
$makeAppxPath = if ($makeAppx.Source) { $makeAppx.Source } else { $makeAppx.FullName }
$bundleInput = Join-Path $env:RUNNER_TEMP ("yaver-store-bundle-input-" + [guid]::NewGuid().ToString("N"))
$bundleInspect = Join-Path $env:RUNNER_TEMP ("yaver-store-bundle-inspect-" + [guid]::NewGuid().ToString("N"))
New-Item -ItemType Directory -Path $bundleInput, $bundleInspect | Out-Null
Copy-Item -LiteralPath $artifact -Destination (Join-Path $bundleInput ([IO.Path]::GetFileName($artifact)))
$bundleVersion = "$($package.version).0"
$bundle = Join-Path $electron "dist-microsoft-store\Yaver-$($package.version)-x64-store.appxbundle"
& $makeAppxPath bundle /v /o /bv $bundleVersion /d $bundleInput /p $bundle
if ($LASTEXITCODE -ne 0 -or -not (Test-Path -LiteralPath $bundle -PathType Leaf)) {
  throw "Microsoft Store bundle build failed."
}
& $makeAppxPath unbundle /v /o /p $bundle /d $bundleInspect
if ($LASTEXITCODE -ne 0) { throw "Microsoft Store bundle validation failed." }
$bundledPackages = @(Get-ChildItem -LiteralPath $bundleInspect -Filter *.appx -File -Recurse)
if ($bundledPackages.Count -ne 1) { throw "Expected exactly one x64 AppX in the Store bundle, found $($bundledPackages.Count)." }
$sourceHash = (Get-FileHash -LiteralPath $artifact -Algorithm SHA256).Hash
$bundledHash = (Get-FileHash -LiteralPath $bundledPackages[0].FullName -Algorithm SHA256).Hash
if ($sourceHash -ne $bundledHash) { throw "Bundling changed the validated inner AppX." }
$bundleHash = (Get-FileHash -LiteralPath $bundle -Algorithm SHA256).Hash.ToLowerInvariant()
if ($PreflightReport) {
  Add-Content -LiteralPath $PreflightReport -Encoding UTF8 -Value @(
    "Bundle: $bundle",
    "Bundle version: $bundleVersion",
    "Bundle SHA-256: $bundleHash",
    "PASS: real MakeAppx Desktop bundle contains the byte-identical validated x64 package"
  )
}
Write-Host "Built package: $artifact"
Write-Host "Built bundle: $bundle"
