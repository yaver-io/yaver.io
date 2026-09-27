param(
  [Parameter(Mandatory = $true)]
  [string[]] $Path,

  [switch] $VerifyOnly
)

$ErrorActionPreference = "Stop"

function Find-SignTool {
  $kitsRoot = ${env:ProgramFiles(x86)}
  if ($kitsRoot) {
    $candidate = Get-ChildItem "$kitsRoot\Windows Kits\10\bin\*\x64\signtool.exe" -ErrorAction SilentlyContinue |
      Sort-Object FullName -Descending |
      Select-Object -First 1 -ExpandProperty FullName
    if ($candidate) { return $candidate }
  }
  $command = Get-Command signtool.exe -ErrorAction SilentlyContinue
  if ($command) { return $command.Source }
  throw "signtool.exe was not found. Install the Windows SDK signing tools."
}

function Resolve-Files([string[]] $requested) {
  $files = @()
  foreach ($item in $requested) {
    if (-not (Test-Path -LiteralPath $item)) {
      throw "Signing target does not exist: $item"
    }
    $entry = Get-Item -LiteralPath $item
    if ($entry.PSIsContainer) {
      $files += Get-ChildItem -LiteralPath $entry.FullName -Recurse -File |
        Where-Object { $_.Extension -in @(".exe", ".dll", ".node") }
    } elseif ($entry.Extension -in @(".exe", ".dll", ".node")) {
      $files += $entry
    } else {
      throw "Signing target is not a PE file or directory: $item"
    }
  }
  $unique = @($files | Sort-Object FullName -Unique)
  if (-not $unique) { throw "No Windows PE files were found in the requested targets." }
  return $unique
}

function Assert-Signature($file, [string] $publisherPattern) {
  $signature = Get-AuthenticodeSignature -LiteralPath $file.FullName
  if ($signature.Status -ne "Valid") {
    throw "Authenticode verification failed for $($file.FullName): $($signature.Status) — $($signature.StatusMessage)"
  }
  if ($signature.SignerCertificate.Subject -notmatch $publisherPattern) {
    throw "Unexpected Windows publisher on $($file.FullName): $($signature.SignerCertificate.Subject). Expected /$publisherPattern/."
  }
  if (-not $signature.TimeStamperCertificate) {
    throw "Authenticode signature has no trusted timestamp: $($file.FullName)"
  }
  Write-Host "Valid timestamped Authenticode: $($file.FullName)"
}

$files = Resolve-Files $Path
$publisherPattern = if ($env:WINDOWS_PUBLISHER_PATTERN) { $env:WINDOWS_PUBLISHER_PATTERN } else { "(?i)simkab" }
$timestampUrl = if ($env:WINDOWS_TIMESTAMP_URL) { $env:WINDOWS_TIMESTAMP_URL } else { "http://time.certum.pl" }

if (-not $VerifyOnly) {
  $signtool = Find-SignTool
  $pfxPath = ""
  try {
    if ($env:WIN_CSC_LINK -and $env:WIN_CSC_KEY_PASSWORD) {
      $encoded = $env:WIN_CSC_LINK -replace '^data:[^;]+;base64,', ''
      $pfxPath = Join-Path $env:RUNNER_TEMP "yaver-windows-signing.pfx"
      [IO.File]::WriteAllBytes($pfxPath, [Convert]::FromBase64String($encoded))
      foreach ($file in $files) {
        & $signtool sign /fd SHA256 /tr $timestampUrl /td SHA256 /f $pfxPath /p $env:WIN_CSC_KEY_PASSWORD $file.FullName
        if ($LASTEXITCODE -ne 0) { throw "signtool failed for $($file.FullName) with exit code $LASTEXITCODE" }
      }
    } elseif ($env:WIN_CERTIFICATE_SHA1) {
      $thumbprint = ($env:WIN_CERTIFICATE_SHA1 -replace '\s', '').ToUpperInvariant()
      $certificate = Get-Item "Cert:\CurrentUser\My\$thumbprint" -ErrorAction SilentlyContinue
      if (-not $certificate) {
        throw "WIN_CERTIFICATE_SHA1 does not resolve in Cert:\CurrentUser\My. Log in to SimplySign Desktop on the dedicated signing runner and expose the cloud certificate first."
      }
      foreach ($file in $files) {
        & $signtool sign /fd SHA256 /tr $timestampUrl /td SHA256 /sha1 $thumbprint $file.FullName
        if ($LASTEXITCODE -ne 0) { throw "signtool failed for $($file.FullName) with exit code $LASTEXITCODE" }
      }
    } else {
      throw "No Windows signing identity is configured. Provide WIN_CSC_LINK/WIN_CSC_KEY_PASSWORD, or run on the dedicated SimplySign runner with WIN_CERTIFICATE_SHA1."
    }
  } finally {
    if ($pfxPath) { Remove-Item -Force -ErrorAction SilentlyContinue $pfxPath }
  }
}

foreach ($file in $files) {
  Assert-Signature $file $publisherPattern
}
